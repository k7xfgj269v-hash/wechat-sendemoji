package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

func validateAutomationImmutableTrace(before, after *AutomationState) error {
	results := make(map[string]AutomationResult, len(after.Results))
	for _, result := range after.Results {
		results[result.ID] = result
	}
	for _, result := range before.Results {
		current, exists := results[result.ID]
		if !exists || !reflect.DeepEqual(current, result) {
			return NewAutomationValidationError("results", "immutable result "+result.ID+" was modified or deleted")
		}
	}
	requests := make(map[string]AutomationPermissionRequest, len(after.PermissionRequests))
	for _, request := range after.PermissionRequests {
		requests[request.ID] = request
	}
	for _, request := range before.PermissionRequests {
		if request.OperatorID != automationPermissionOperatorAuto {
			continue
		}
		current, exists := requests[request.ID]
		if !exists || !reflect.DeepEqual(current, request) {
			return NewAutomationValidationError(
				"permission_requests",
				"automatic permission request "+request.ID+" was modified or deleted",
			)
		}
	}
	return nil
}

func reconcileAutomationTrace(
	state *AutomationState,
	before *AutomationState,
	allocator *AutomationIDAllocator,
) error {
	// Existing actions and permission requests were already normalized when
	// they were created or loaded; re-normalizing them on every mutation
	// would decode and re-encode every workbench payload. Only entities that
	// are new since the previous state need normalization.
	if len(state.Actions) != len(before.Actions) ||
		len(state.PermissionRequests) != len(before.PermissionRequests) {
		if err := normalizeAutomationWorkbenchState(state); err != nil {
			return err
		}
	}
	existingRuns := make(map[string]struct{}, len(before.Runs))
	for _, run := range before.Runs {
		existingRuns[run.ID] = struct{}{}
	}
	for i := range state.Runs {
		run := &state.Runs[i]
		if _, existed := existingRuns[run.ID]; existed {
			continue
		}
		if run.SessionID == "" || run.TurnID == "" {
			run.SessionID = ""
			run.TurnID = ""
			if err := appendAutomationRunTrace(state, allocator, run, AutomationSessionKindAutomation); err != nil {
				return err
			}
		}
	}
	reconcileAutomationRunTraceStatuses(state)

	actionIndexes := make([]int, len(state.Actions))
	for i := range actionIndexes {
		actionIndexes[i] = i
	}
	sort.Slice(actionIndexes, func(i, j int) bool {
		left := state.Actions[actionIndexes[i]]
		right := state.Actions[actionIndexes[j]]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	existingActions := make(map[string]struct{}, len(before.Actions))
	for _, action := range before.Actions {
		existingActions[action.ID] = struct{}{}
	}
	requestsByAction := make(map[string][]*AutomationPermissionRequest, len(state.PermissionRequests))
	for i := range state.PermissionRequests {
		request := &state.PermissionRequests[i]
		requestsByAction[request.ActionID] = append(requestsByAction[request.ActionID], request)
	}
	for _, index := range actionIndexes {
		action := &state.Actions[index]
		requests := requestsByAction[action.ID]
		if len(requests) > 1 {
			continue
		}
		if len(requests) == 0 {
			if action.Status == AutomationActionAwaitingReview {
				return NewAutomationValidationError(
					"permission_requests",
					"awaiting-review action "+action.ID+" requires a human permission request",
				)
			}
			id, err := allocator.Next(AutomationIDKindPermissionRequest)
			if err != nil {
				return err
			}
			status := AutomationPermissionStatusAllowed
			decision := AutomationPermissionAllowOnce
			operatorID := automationPermissionOperatorAuto
			reason := "automatic " + string(action.PolicyDecision)
			ruleID := ""
			if action.PolicyDecision == AutomationDecisionDeny || action.Status == AutomationActionDenied ||
				action.Status == AutomationActionRejected {
				status = AutomationPermissionStatusDenied
				decision = AutomationPermissionDenyOnce
			} else if action.Tool == AutomationToolWorkbenchAllowOnce &&
				persistentHighRiskSendEnabled() &&
				isPersistentHighRiskSendOperation(action.Operation) {
				effect, matchedRuleID, matched := matchAutomationPermissionRule(state, *action)
				if matched && effect == AutomationPermissionEffectAllow {
					var matchedRule *AutomationPermissionRule
					for i := range state.PermissionRules {
						if state.PermissionRules[i].ID == matchedRuleID {
							matchedRule = &state.PermissionRules[i]
							break
						}
					}
					if matchedRule == nil || strings.TrimSpace(matchedRule.CreatedBy) == "" {
						return NewAutomationValidationError(
							"permission_rules",
							"matched persistent outbound rule is invalid",
						)
					}
					decision = AutomationPermissionAllowPersistent
					operatorID = matchedRule.CreatedBy
					reason = "authorized by persistent outbound permission rule"
					ruleID = matchedRule.ID
				}
			}
			request := AutomationPermissionRequest{
				ID:         id,
				RunID:      action.RunID,
				ActionID:   action.ID,
				Tool:       action.Tool,
				Operation:  action.Operation,
				Risk:       action.Risk,
				Status:     status,
				Version:    1,
				Decision:   decision,
				OperatorID: operatorID,
				Reason:     reason,
				RuleID:     ruleID,
				CreatedAt:  action.CreatedAt,
				UpdatedAt:  action.CreatedAt,
			}
			state.PermissionRequests = append(state.PermissionRequests, request)
			requestsByAction[action.ID] = []*AutomationPermissionRequest{&state.PermissionRequests[len(state.PermissionRequests)-1]}
		}
	}

	resultsByID := make(map[string]*AutomationResult, len(state.Results))
	resultsByAction := make(map[string][]*AutomationResult, len(state.Results))
	for i := range state.Results {
		result := &state.Results[i]
		resultsByID[result.ID] = result
		resultsByAction[result.ActionID] = append(resultsByAction[result.ActionID], result)
	}
	for _, index := range actionIndexes {
		action := &state.Actions[index]
		_, existed := existingActions[action.ID]
		if !existed && action.ResultID != "" {
			if result := resultsByID[action.ResultID]; result == nil || result.ActionID != action.ID {
				action.ResultID = ""
			}
		}
		if !IsTerminalAutomationActionStatus(action.Status) {
			if !existed {
				action.ResultID = ""
			}
			continue
		}
		if result := resultsByID[action.ResultID]; result != nil && result.ActionID == action.ID {
			continue
		}
		if len(resultsByAction[action.ID]) != 0 {
			continue
		}
		requests := requestsByAction[action.ID]
		if len(requests) != 1 {
			continue
		}
		id, err := allocator.Next(AutomationIDKindResult)
		if err != nil {
			return err
		}
		result := AutomationResult{
			ID:                  id,
			ActionID:            action.ID,
			PermissionRequestID: requests[0].ID,
			Status:              automationResultStatus(action.Status),
			Data:                cloneAutomationBytes(action.Result),
			ErrorCode:           action.ErrorCode,
			ErrorMessage:        action.ErrorMessage,
			CreatedAt:           action.UpdatedAt,
		}
		state.Results = append(state.Results, result)
		action.ResultID = id
		resultsByID[id] = &state.Results[len(state.Results)-1]
		resultsByAction[action.ID] = []*AutomationResult{&state.Results[len(state.Results)-1]}
	}
	return nil
}

func reconcileAutomationRunTraceStatuses(state *AutomationState) bool {
	sessionIndexes := make(map[string]int, len(state.Sessions))
	for i := range state.Sessions {
		sessionIndexes[state.Sessions[i].ID] = i
	}
	turnIndexes := make(map[string]int, len(state.Turns))
	for i := range state.Turns {
		turnIndexes[state.Turns[i].ID] = i
	}

	changed := false
	for i := range state.Runs {
		run := &state.Runs[i]
		sessionIndex, sessionExists := sessionIndexes[run.SessionID]
		turnIndex, turnExists := turnIndexes[run.TurnID]
		if !sessionExists || !turnExists {
			continue
		}
		session := &state.Sessions[sessionIndex]
		turn := &state.Turns[turnIndex]
		if session.Kind == AutomationSessionKindChat || turn.SessionID != session.ID {
			continue
		}

		turnStatus := AutomationTurnQueued
		sessionStatus := AutomationSessionStatusActive
		updatedAt := run.CreatedAt
		switch run.Status {
		case AutomationRunRunning:
			if run.StartedAt == nil {
				continue
			}
			turnStatus = AutomationTurnRunning
			updatedAt = *run.StartedAt
		case AutomationRunSucceeded:
			if run.StartedAt == nil || run.CompletedAt == nil {
				continue
			}
			turnStatus = AutomationTurnCompleted
			sessionStatus = AutomationSessionStatusCompleted
			updatedAt = *run.CompletedAt
		case AutomationRunFailed:
			if run.StartedAt == nil || run.CompletedAt == nil {
				continue
			}
			turnStatus = AutomationTurnFailed
			sessionStatus = AutomationSessionStatusCompleted
			updatedAt = *run.CompletedAt
		}

		turnChanged := turn.Status != turnStatus ||
			!turn.UpdatedAt.Equal(updatedAt) ||
			!reflect.DeepEqual(turn.StartedAt, run.StartedAt) ||
			!reflect.DeepEqual(turn.CompletedAt, run.CompletedAt)
		if turnChanged {
			turn.Status = turnStatus
			turn.UpdatedAt = updatedAt
			turn.StartedAt = cloneAutomationTimePointer(run.StartedAt)
			turn.CompletedAt = cloneAutomationTimePointer(run.CompletedAt)
			turn.Version++
			changed = true
		}

		if session.Status != sessionStatus || !session.UpdatedAt.Equal(updatedAt) {
			session.Status = sessionStatus
			session.UpdatedAt = updatedAt
			session.Version++
			changed = true
		}
	}
	return changed
}

func normalizeAutomationWorkbenchState(state *AutomationState) error {
	actions := make(map[string]*AutomationAction, len(state.Actions))
	for i := range state.Actions {
		action := &state.Actions[i]
		actions[action.ID] = action
		if !IsWorkbenchToolName(action.Tool) {
			continue
		}
		decoded, err := DecodeWorkbenchToolRequest(action.Tool, action.Payload)
		if err != nil {
			return NewAutomationValidationError(
				automationIndexedField("actions", i)+".payload",
				"does not match the persisted workbench schema",
			)
		}
		action.Operation = decoded.Request.Operation
		action.Payload = append(json.RawMessage(nil), decoded.CanonicalRequest...)
	}
	for i := range state.PermissionRequests {
		request := &state.PermissionRequests[i]
		if action := actions[request.ActionID]; action != nil {
			request.Operation = action.Operation
		}
	}
	return nil
}

func automationActionSupportsTextOutbox(action AutomationAction) bool {
	if action.Tool == AutomationToolReplyPrivateText || action.Tool == AutomationToolReplyAudienceText {
		return true
	}
	return action.Tool == AutomationToolWorkbenchAllowOnce &&
		(action.Operation == WorkbenchOperationMessagesSendPrivate ||
			action.Operation == WorkbenchOperationMessagesSendGroup)
}

func appendAutomationRunTrace(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	run *AutomationRun,
	kind AutomationSessionKind,
) error {
	sessionID, err := allocator.Next(AutomationIDKindSession)
	if err != nil {
		return err
	}
	turnID, err := allocator.Next(AutomationIDKindTurn)
	if err != nil {
		return err
	}
	updatedAt := run.CreatedAt
	if run.CompletedAt != nil {
		updatedAt = *run.CompletedAt
	} else if run.StartedAt != nil {
		updatedAt = *run.StartedAt
	}
	sessionStatus := AutomationSessionStatusActive
	if run.Status == AutomationRunSucceeded || run.Status == AutomationRunFailed {
		sessionStatus = AutomationSessionStatusCompleted
	}
	turnStatus := AutomationTurnQueued
	switch run.Status {
	case AutomationRunRunning:
		turnStatus = AutomationTurnRunning
	case AutomationRunSucceeded:
		turnStatus = AutomationTurnCompleted
	case AutomationRunFailed:
		turnStatus = AutomationTurnFailed
	}
	state.Sessions = append(state.Sessions, AutomationSession{
		ID:        sessionID,
		Kind:      kind,
		Title:     "Automation run " + run.ID,
		Status:    sessionStatus,
		Version:   1,
		CreatedAt: run.CreatedAt,
		UpdatedAt: updatedAt,
	})
	state.Turns = append(state.Turns, AutomationTurn{
		ID:               turnID,
		SessionID:        sessionID,
		Status:           turnStatus,
		ProviderName:     run.ProviderName,
		PromptRevisionID: run.PromptRevisionID,
		Version:          1,
		CreatedAt:        run.CreatedAt,
		UpdatedAt:        updatedAt,
		StartedAt:        cloneAutomationTimePointer(run.StartedAt),
		CompletedAt:      cloneAutomationTimePointer(run.CompletedAt),
	})
	run.SessionID = sessionID
	run.TurnID = turnID
	return nil
}

func automationResultStatus(status AutomationActionStatus) AutomationDeliveryStatus {
	switch status {
	case AutomationActionSucceeded:
		return AutomationDeliverySent
	case AutomationActionUnknown:
		return AutomationDeliveryUnknown
	default:
		return AutomationDeliveryFailed
	}
}
