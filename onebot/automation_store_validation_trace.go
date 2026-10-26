package main

import (
	"bytes"
)

func validateAutomationTraceEntities(
	state *AutomationState,
	allIDs map[string]string,
	runs map[string]*AutomationRun,
	actions map[string]*AutomationAction,
	permissionRequests map[string]*AutomationPermissionRequest,
) (map[string]*AutomationSession, map[string]*AutomationTurn, error) {
	sessions := make(map[string]*AutomationSession, len(state.Sessions))
	for i := range state.Sessions {
		session := &state.Sessions[i]
		field := automationIndexedField("sessions", i)
		if err := validateAutomationStoredID(field+".id", session.ID, "aisession", state.Counters.Session, allIDs); err != nil {
			return nil, nil, err
		}
		if err := validateAutomationSession(field, session); err != nil {
			return nil, nil, err
		}
		sessions[session.ID] = session
	}

	turns := make(map[string]*AutomationTurn, len(state.Turns))
	for i := range state.Turns {
		turn := &state.Turns[i]
		field := automationIndexedField("turns", i)
		if err := validateAutomationStoredID(field+".id", turn.ID, "aiturn", state.Counters.Turn, allIDs); err != nil {
			return nil, nil, err
		}
		if err := validateAutomationTurn(field, turn); err != nil {
			return nil, nil, err
		}
		if _, exists := sessions[turn.SessionID]; !exists {
			return nil, nil, NewAutomationValidationError(field+".session_id", "references a missing session")
		}
		turns[turn.ID] = turn
	}
	for i := range state.Sessions {
		session := &state.Sessions[i]
		if session.ContextCheckpoint == nil {
			continue
		}
		turn, exists := turns[session.ContextCheckpoint.ThroughTurnID]
		if !exists || turn.SessionID != session.ID {
			return nil, nil, NewAutomationValidationError(
				automationIndexedField("sessions", i)+".context_checkpoint.through_turn_id",
				"must reference a turn in the same session",
			)
		}
		if turn.Status != AutomationTurnCompleted {
			return nil, nil, NewAutomationValidationError(
				automationIndexedField("sessions", i)+".context_checkpoint.through_turn_id",
				"must reference a completed turn",
			)
		}
	}

	sequences := make(map[uint64]string, len(state.TurnEvents))
	var previousSequence uint64
	for i := range state.TurnEvents {
		event := &state.TurnEvents[i]
		field := automationIndexedField("turn_events", i)
		if err := validateAutomationTurnEvent(field, event, state.Counters.TurnEvent); err != nil {
			return nil, nil, err
		}
		if prior, exists := sequences[event.Sequence]; exists {
			return nil, nil, NewAutomationValidationError(field+".sequence", "duplicates "+prior)
		}
		if event.Sequence <= previousSequence {
			return nil, nil, NewAutomationValidationError(field+".sequence", "must be globally increasing")
		}
		sequences[event.Sequence] = field
		previousSequence = event.Sequence
		turn, exists := turns[event.TurnID]
		if !exists || turn.SessionID != event.SessionID {
			return nil, nil, NewAutomationValidationError(field+".turn_id", "does not match the referenced session")
		}
	}

	requestByAction := make(map[string]*AutomationPermissionRequest, len(state.PermissionRequests))
	for i := range state.PermissionRequests {
		request := &state.PermissionRequests[i]
		field := automationIndexedField("permission_requests", i)
		if previous, exists := requestByAction[request.ActionID]; exists {
			return nil, nil, NewAutomationValidationError(field+".action_id", "duplicates permission request "+previous.ID)
		}
		requestByAction[request.ActionID] = request
	}

	resultByAction := make(map[string]*AutomationResult, len(state.Results))
	results := make(map[string]*AutomationResult, len(state.Results))
	for i := range state.Results {
		result := &state.Results[i]
		field := automationIndexedField("results", i)
		if err := validateAutomationStoredID(field+".id", result.ID, "airesult", state.Counters.Result, allIDs); err != nil {
			return nil, nil, err
		}
		if err := validateAutomationResult(field, result); err != nil {
			return nil, nil, err
		}
		action, exists := actions[result.ActionID]
		if !exists {
			return nil, nil, NewAutomationValidationError(field+".action_id", "references a missing action")
		}
		request, exists := permissionRequests[result.PermissionRequestID]
		if !exists || request.ActionID != action.ID {
			return nil, nil, NewAutomationValidationError(field+".permission_request_id", "does not match the result action")
		}
		if previous, exists := resultByAction[action.ID]; exists {
			return nil, nil, NewAutomationValidationError(field+".action_id", "duplicates result "+previous.ID)
		}
		resultByAction[action.ID] = result
		results[result.ID] = result
	}

	for _, action := range actions {
		request := requestByAction[action.ID]
		if request == nil {
			return nil, nil, NewAutomationValidationError("actions", "action "+action.ID+" has no permission request")
		}
		result := resultByAction[action.ID]
		if !IsTerminalAutomationActionStatus(action.Status) {
			if action.ResultID != "" || result != nil || len(action.Result) != 0 ||
				action.ErrorCode != "" || action.ErrorMessage != "" {
				return nil, nil, NewAutomationValidationError("actions", "nonterminal action "+action.ID+" must not have a result")
			}
			continue
		}
		if result == nil || action.ResultID == "" || results[action.ResultID] != result {
			return nil, nil, NewAutomationValidationError("actions", "terminal action "+action.ID+" must reference exactly one result")
		}
		if result.Status != automationResultStatus(action.Status) ||
			!bytes.Equal(result.Data, action.Result) ||
			result.ErrorCode != action.ErrorCode ||
			result.ErrorMessage != action.ErrorMessage {
			return nil, nil, NewAutomationValidationError("actions", "result projection does not match action "+action.ID)
		}
	}

	return sessions, turns, nil
}

func validateAutomationSession(field string, session *AutomationSession) error {
	if !IsValidAutomationSessionKind(session.Kind) {
		return NewAutomationValidationError(field+".kind", "is invalid")
	}
	if err := validateStoredAutomationString(field+".title", session.Title, 256, false, true); err != nil {
		return err
	}
	if !IsValidAutomationSessionStatus(session.Status) {
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if session.ContextCheckpoint != nil {
		if session.Kind != AutomationSessionKindChat {
			return NewAutomationValidationError(field+".context_checkpoint", "is only valid for chat sessions")
		}
		if err := validateAutomationChatCheckpoint(field+".context_checkpoint", session.ContextCheckpoint); err != nil {
			return err
		}
	}
	if session.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if err := validateAutomationStoreTime(field+".created_at", session.CreatedAt); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".updated_at", session.UpdatedAt); err != nil {
		return err
	}
	if session.UpdatedAt.Before(session.CreatedAt) {
		return NewAutomationValidationError(field+".updated_at", "must not precede created_at")
	}
	return nil
}

func validateAutomationTurn(field string, turn *AutomationTurn) error {
	if err := validateStoredAutomationString(field+".session_id", turn.SessionID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".user_text", turn.UserText, 16000, false, true); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".assistant_text", turn.AssistantText, 64000, false, true); err != nil {
		return err
	}
	if !IsValidAutomationTurnStatus(turn.Status) {
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if err := validateStoredAutomationString(field+".provider_name", turn.ProviderName, 120, true, false); err != nil {
		return err
	}
	if err := validateAutomationExternalID(field+".prompt_revision_id", turn.PromptRevisionID); err != nil {
		return err
	}
	if err := validateAutomationProviderUsage(field+".provider_usage", turn.ProviderUsage); err != nil {
		return err
	}
	if turn.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if err := validateAutomationStoreTime(field+".created_at", turn.CreatedAt); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".updated_at", turn.UpdatedAt); err != nil {
		return err
	}
	if err := validateOptionalAutomationTime(field+".started_at", turn.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalAutomationTime(field+".completed_at", turn.CompletedAt); err != nil {
		return err
	}
	if turn.UpdatedAt.Before(turn.CreatedAt) ||
		(turn.StartedAt != nil && turn.StartedAt.Before(turn.CreatedAt)) ||
		(turn.CompletedAt != nil && (turn.CompletedAt.Before(turn.CreatedAt) || turn.CompletedAt.After(turn.UpdatedAt))) {
		return NewAutomationValidationError(field, "contains inconsistent timestamps")
	}
	switch turn.Status {
	case AutomationTurnQueued:
		if turn.StartedAt != nil || turn.CompletedAt != nil {
			return NewAutomationValidationError(field+".status", "queued turns must not have execution timestamps")
		}
	case AutomationTurnRunning, AutomationTurnAwaitingReview:
		if turn.StartedAt == nil || turn.CompletedAt != nil {
			return NewAutomationValidationError(field+".status", "active turns require only a start time")
		}
	case AutomationTurnCompleted, AutomationTurnFailed, AutomationTurnCancelled:
		if turn.CompletedAt == nil {
			return NewAutomationValidationError(field+".status", "terminal turns require a completion time")
		}
	}
	return nil
}

func validateAutomationTurnEvent(field string, event *AutomationTurnEvent, counter uint64) error {
	if event.Sequence == 0 || event.Sequence > counter {
		return NewAutomationValidationError(field+".sequence", "must be nonzero and not exceed its counter")
	}
	if err := validateStoredAutomationString(field+".session_id", event.SessionID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".turn_id", event.TurnID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".type", event.Type, 120, true, false); err != nil {
		return err
	}
	if err := validateAutomationRawJSON(field+".data", event.Data, AutomationMaxActionPayloadBytes, true); err != nil {
		return err
	}
	return validateAutomationStoreTime(field+".created_at", event.CreatedAt)
}

func validateAutomationResult(field string, result *AutomationResult) error {
	if err := validateStoredAutomationString(field+".action_id", result.ActionID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".permission_request_id", result.PermissionRequestID, 80, true, false); err != nil {
		return err
	}
	switch result.Status {
	case AutomationDeliverySent, AutomationDeliveryFailed, AutomationDeliveryUnknown:
	default:
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if err := validateAutomationRawJSON(field+".data", result.Data, AutomationMaxActionPayloadBytes, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".error_code", result.ErrorCode, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".error_message", result.ErrorMessage, 4000, false, true); err != nil {
		return err
	}
	return validateAutomationStoreTime(field+".created_at", result.CreatedAt)
}

func validateAutomationRunRelationships(
	state *AutomationState,
	runs map[string]*AutomationRun,
	actions map[string]*AutomationAction,
	sessions map[string]*AutomationSession,
	turns map[string]*AutomationTurn,
	requireTrace bool,
) error {
	actualByRun := make(map[string]int, len(runs))
	for _, action := range actions {
		actualByRun[action.RunID]++
	}
	for i := range state.Runs {
		run := &state.Runs[i]
		field := automationIndexedField("runs", i)
		if requireTrace {
			if sessions[run.SessionID] == nil {
				return NewAutomationValidationError(field+".session_id", "references a missing session")
			}
			turn := turns[run.TurnID]
			if turn == nil || turn.SessionID != run.SessionID {
				return NewAutomationValidationError(field+".turn_id", "does not match the owning session")
			}
			if turn.ProviderName != run.ProviderName || turn.PromptRevisionID != run.PromptRevisionID {
				return NewAutomationValidationError(field+".turn_id", "does not match the run provider trace")
			}
		}
		if run.RetryOfRunID != "" {
			if run.RetryOfRunID == run.ID {
				return NewAutomationValidationError(field+".retry_of_run_id", "must not reference the same run")
			}
			if _, exists := runs[run.RetryOfRunID]; !exists {
				return NewAutomationValidationError(field+".retry_of_run_id", "references a missing run")
			}
		}
		listed := make(map[string]struct{}, len(run.ActionIDs))
		for j, actionID := range run.ActionIDs {
			action, exists := actions[actionID]
			if !exists {
				return NewAutomationValidationError(
					automationIndexedField(field+".action_ids", j),
					"references a missing action",
				)
			}
			if action.RunID != run.ID {
				return NewAutomationValidationError(
					automationIndexedField(field+".action_ids", j),
					"references an action from another run",
				)
			}
			listed[actionID] = struct{}{}
		}
		if len(listed) != actualByRun[run.ID] {
			return NewAutomationValidationError(field+".action_ids", "does not list every action in the run")
		}
	}
	return nil
}
