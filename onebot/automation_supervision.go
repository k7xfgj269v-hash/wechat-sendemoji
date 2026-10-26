package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	AutomationAuditEventGateStarted         = "gate.started"
	AutomationAuditEventGateCompleted       = "gate.completed"
	AutomationAuditEventProviderStarted     = "provider.started"
	AutomationAuditEventProviderCompleted   = "provider.completed"
	AutomationAuditEventActionProposed      = "action.proposed"
	AutomationAuditEventPermissionRequested = "permission.requested"
	AutomationAuditEventPermissionAllowed   = "permission.allowed"
	AutomationAuditEventPermissionDenied    = "permission.denied"
	AutomationAuditEventActionExecuting     = "action.executing"
	AutomationAuditEventActionSucceeded     = "action.succeeded"
	AutomationAuditEventActionFailed        = "action.failed"
)

type AutomationRuntimeEventBroker struct {
	mu          sync.Mutex
	capacity    int
	sequence    uint64
	replay      []AutomationRuntimeEvent
	subscribers map[chan AutomationRuntimeEvent]struct{}
	closed      bool
}

type automationPermissionDecisionRequest struct {
	ExpectedVersion       uint64                       `json:"expected_version"`
	ExpectedActionVersion uint64                       `json:"expected_action_version"`
	Decision              AutomationPermissionDecision `json:"decision"`
	OperatorID            string                       `json:"operator_id"`
	Reason                string                       `json:"reason,omitempty"`
}

func NewAutomationRuntimeEventBroker(capacity int) *AutomationRuntimeEventBroker {
	if capacity < 1 {
		capacity = 1
	}
	return &AutomationRuntimeEventBroker{
		capacity:    capacity,
		replay:      make([]AutomationRuntimeEvent, 0, capacity),
		subscribers: make(map[chan AutomationRuntimeEvent]struct{}),
	}
}

func (b *AutomationRuntimeEventBroker) Publish(event AutomationRuntimeEvent) AutomationRuntimeEvent {
	if b == nil {
		return event
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return event
	}
	b.sequence++
	event.Sequence = b.sequence
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	} else {
		event.CreatedAt = event.CreatedAt.UTC()
	}
	event.Data = sanitizeAutomationJSON(event.Data)
	b.replay = append(b.replay, event)
	if len(b.replay) > b.capacity {
		b.replay = append([]AutomationRuntimeEvent(nil), b.replay[len(b.replay)-b.capacity:]...)
	}
	for subscriber := range b.subscribers {
		select {
		case subscriber <- cloneAutomationRuntimeEvent(event):
		default:
			delete(b.subscribers, subscriber)
			close(subscriber)
		}
	}
	return event
}

func (b *AutomationRuntimeEventBroker) Subscribe(after uint64) ([]AutomationRuntimeEvent, <-chan AutomationRuntimeEvent, func()) {
	if b == nil {
		channel := make(chan AutomationRuntimeEvent)
		close(channel)
		return nil, channel, func() {}
	}
	b.mu.Lock()
	replay := make([]AutomationRuntimeEvent, 0, len(b.replay))
	for _, event := range b.replay {
		if event.Sequence > after {
			replay = append(replay, cloneAutomationRuntimeEvent(event))
		}
	}
	channel := make(chan AutomationRuntimeEvent, 32)
	if b.closed {
		close(channel)
		b.mu.Unlock()
		return replay, channel, func() {}
	}
	b.subscribers[channel] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[channel]; ok {
			delete(b.subscribers, channel)
			close(channel)
		}
		b.mu.Unlock()
	}
	return replay, channel, cancel
}

func (b *AutomationRuntimeEventBroker) Close() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		for subscriber := range b.subscribers {
			delete(b.subscribers, subscriber)
			close(subscriber)
		}
	}
	b.mu.Unlock()
}

func (s *AutomationService) RuntimeEventBroker() *AutomationRuntimeEventBroker {
	if s == nil {
		return nil
	}
	return s.events
}

func (s *AutomationService) ListPermissionRequests(status string, limit, offset int) (int, []AutomationPermissionRequest, error) {
	if s == nil || s.store == nil {
		return 0, nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	if status == "" {
		status = AutomationPermissionStatusPending
	}
	switch status {
	case AutomationPermissionStatusPending, AutomationPermissionStatusAllowed,
		AutomationPermissionStatusDenied, AutomationPermissionStatusExpired:
	default:
		return 0, nil, NewAutomationValidationError("status", "is invalid")
	}
	items := make([]AutomationPermissionRequest, 0)
	for _, request := range s.store.Snapshot().PermissionRequests {
		if request.Status == status {
			items = append(items, request)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	total := len(items)
	start, end := automationPage(total, limit, offset)
	return total, append([]AutomationPermissionRequest(nil), items[start:end]...), nil
}

func (s *AutomationService) ListPermissionRules(limit, offset int) (int, []AutomationPermissionRule, error) {
	if s == nil || s.store == nil {
		return 0, nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	items := s.store.Snapshot().PermissionRules
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	total := len(items)
	start, end := automationPage(total, limit, offset)
	return total, append([]AutomationPermissionRule(nil), items[start:end]...), nil
}

func (s *AutomationService) DeletePermissionRule(id string) (AutomationPermissionRule, error) {
	if s == nil || s.store == nil {
		return AutomationPermissionRule{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	var result AutomationPermissionRule
	err := s.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		for i := range state.PermissionRules {
			if state.PermissionRules[i].ID != id {
				continue
			}
			state.PermissionRules[i].Enabled = false
			state.PermissionRules[i].Version++
			state.PermissionRules[i].UpdatedAt = automationMutationTime(state.PermissionRules[i].UpdatedAt)
			result = state.PermissionRules[i]
			return nil
		}
		return errors.New("automation permission rule not found")
	})
	return result, err
}

func (s *AutomationService) DecidePermissionRequest(
	ctx context.Context,
	requestID string,
	input automationPermissionDecisionRequest,
) (AutomationPermissionRequest, AutomationAction, error) {
	if ctx == nil {
		return AutomationPermissionRequest{}, AutomationAction{}, NewAutomationValidationError("context", "is required")
	}
	if s == nil || s.store == nil {
		return AutomationPermissionRequest{}, AutomationAction{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	input.OperatorID = strings.TrimSpace(input.OperatorID)
	input.Reason = strings.TrimSpace(input.Reason)
	if !IsValidAutomationPermissionDecision(input.Decision) {
		return AutomationPermissionRequest{}, AutomationAction{}, NewAutomationValidationError("decision", "is invalid")
	}
	if input.OperatorID == "" {
		return AutomationPermissionRequest{}, AutomationAction{}, NewAutomationValidationError("operator_id", "is required")
	}

	type permissionOutcome struct {
		request AutomationPermissionRequest
		action  AutomationAction
	}
	var decided AutomationPermissionRequest
	var action AutomationAction
	var outcomes []permissionOutcome
	allowed := input.Decision == AutomationPermissionAllowOnce ||
		input.Decision == AutomationPermissionAllowRun ||
		input.Decision == AutomationPermissionAllowPersistent
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		requestIndex := automationPermissionRequestIndex(state, requestID)
		if requestIndex < 0 {
			return errors.New("automation permission request not found")
		}
		request := &state.PermissionRequests[requestIndex]
		if request.Status != AutomationPermissionStatusPending {
			return NewAutomationValidationError("permission_request", "is not pending")
		}
		if request.Version != input.ExpectedVersion {
			return NewAutomationValidationError("expected_version", "permission request version conflict")
		}
		actionIndex := automationActionIndex(state, request.ActionID)
		if actionIndex < 0 {
			return errors.New("automation action not found")
		}
		current := &state.Actions[actionIndex]
		if current.Risk == AutomationRiskR4 || current.PolicyDecision == AutomationDecisionDeny {
			return NewAutomationValidationError("permission_request", "immutable policy denial cannot be overridden")
		}
		if current.Status != AutomationActionAwaitingReview {
			return NewAutomationValidationError("action", "is not awaiting review")
		}
		if IsWorkbenchToolName(current.Tool) &&
			!containsAutomationPermissionDecision(
				WorkbenchValidPermissionDecisions(current.Tool, current.Operation),
				input.Decision,
			) {
			return NewAutomationValidationError("decision", "is not allowed for this workbench operation")
		}
		if input.ExpectedActionVersion != 0 && current.Version != input.ExpectedActionVersion {
			return NewAutomationValidationError("expected_version", "action version conflict")
		}

		now := automationMutationTime(request.UpdatedAt)
		var rule *AutomationPermissionRule
		if input.Decision == AutomationPermissionAllowRun ||
			input.Decision == AutomationPermissionAllowPersistent ||
			input.Decision == AutomationPermissionDenyPersistent {
			derived, err := deriveAutomationPermissionRule(current, input, now, allocator)
			if err != nil {
				return err
			}
			state.PermissionRules = append(state.PermissionRules, derived)
			rule = &state.PermissionRules[len(state.PermissionRules)-1]
		}

		applyDecision := func(targetRequest *AutomationPermissionRequest, targetAction *AutomationAction) error {
			targetRequest.Decision = input.Decision
			targetRequest.OperatorID = input.OperatorID
			targetRequest.Reason = input.Reason
			targetRequest.Version++
			targetRequest.UpdatedAt = now
			if rule != nil {
				targetRequest.RuleID = rule.ID
			}
			if allowed {
				targetRequest.Status = AutomationPermissionStatusAllowed
				targetAction.Status = AutomationActionApproved
			} else {
				targetRequest.Status = AutomationPermissionStatusDenied
				targetAction.Status = AutomationActionDenied
			}
			targetAction.Version++
			targetAction.UpdatedAt = now
			approvalID, err := allocator.Next(AutomationIDKindApproval)
			if err != nil {
				return err
			}
			state.Approvals = append(state.Approvals, AutomationApprovalRecord{
				ID:            approvalID,
				ActionID:      targetAction.ID,
				ActionVersion: targetAction.Version,
				Decision:      string(input.Decision),
				OperatorID:    input.OperatorID,
				Reason:        input.Reason,
				CreatedAt:     now,
			})
			eventType := AutomationAuditEventPermissionDenied
			if allowed {
				eventType = AutomationAuditEventPermissionAllowed
			}
			if _, err := appendAutomationSupervisionAudit(
				state,
				allocator,
				eventType,
				*targetAction,
				targetRequest.ID,
				input.OperatorID,
				now,
			); err != nil {
				return err
			}
			outcomes = append(outcomes, permissionOutcome{request: *targetRequest, action: *targetAction})
			return nil
		}
		if err := applyDecision(request, current); err != nil {
			return err
		}
		decided = *request
		action = *current

		if rule != nil {
			for i := range state.PermissionRequests {
				otherRequest := &state.PermissionRequests[i]
				if otherRequest.ID == request.ID ||
					otherRequest.Status != AutomationPermissionStatusPending {
					continue
				}
				otherActionIndex := automationActionIndex(state, otherRequest.ActionID)
				if otherActionIndex < 0 {
					continue
				}
				otherAction := &state.Actions[otherActionIndex]
				if otherAction.Status != AutomationActionAwaitingReview {
					continue
				}
				_, matchedRuleID, matched := matchAutomationPermissionRule(state, *otherAction)
				if !matched || matchedRuleID != rule.ID {
					continue
				}
				if err := applyDecision(otherRequest, otherAction); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return AutomationPermissionRequest{}, AutomationAction{}, err
	}

	for _, outcome := range outcomes {
		s.publishPermissionEvent(outcome.request, outcome.action)
	}
	runIDs := make(map[string]struct{})
	for _, outcome := range outcomes {
		runIDs[outcome.action.RunID] = struct{}{}
	}
	orderedRunIDs := make([]string, 0, len(runIDs))
	for runID := range runIDs {
		orderedRunIDs = append(orderedRunIDs, runID)
	}
	sort.Strings(orderedRunIDs)
	for _, runID := range orderedRunIDs {
		if err := s.ExecuteReadyActions(ctx, runID); err != nil {
			return decided, action, err
		}
	}
	if allowed {
		action, err = s.getAction(action.ID)
		if err != nil {
			return decided, AutomationAction{}, err
		}
	}
	for _, outcome := range outcomes {
		s.reconcileChatTurnForRun(outcome.action.RunID)
	}
	return decided, action, nil
}

func (s *AutomationService) decideLegacyAction(
	ctx context.Context,
	actionID string,
	request automationActionMutationRequest,
	decision AutomationPermissionDecision,
) (AutomationAction, error) {
	if s == nil || s.store == nil {
		return AutomationAction{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	state := s.store.Snapshot()
	actionIndex := automationActionIndex(&state, actionID)
	if actionIndex < 0 {
		return AutomationAction{}, errors.New("automation action not found")
	}
	action := state.Actions[actionIndex]
	if err := automationValidateReviewRequest(action, request); err != nil {
		return AutomationAction{}, err
	}
	requestIndex := -1
	for i := range state.PermissionRequests {
		if state.PermissionRequests[i].ActionID == actionID &&
			state.PermissionRequests[i].Status == AutomationPermissionStatusPending {
			requestIndex = i
			break
		}
	}
	if requestIndex < 0 {
		return AutomationAction{}, errors.New("automation permission request not found")
	}
	permissionRequest := state.PermissionRequests[requestIndex]
	_, resultingAction, err := s.DecidePermissionRequest(ctx, permissionRequest.ID, automationPermissionDecisionRequest{
		ExpectedVersion:       permissionRequest.Version,
		ExpectedActionVersion: request.ExpectedVersion,
		Decision:              decision,
		OperatorID:            request.OperatorID,
		Reason:                request.Reason,
	})
	if err != nil {
		return AutomationAction{}, err
	}
	return resultingAction, nil
}

func PersistAutomationPermissionRequest(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	action AutomationAction,
	now time.Time,
) (AutomationPermissionRequest, error) {
	for _, request := range state.PermissionRequests {
		if request.ActionID == action.ID {
			return request, nil
		}
	}
	id, err := allocator.Next(AutomationIDKindPermissionRequest)
	if err != nil {
		return AutomationPermissionRequest{}, err
	}
	request := AutomationPermissionRequest{
		ID:        id,
		RunID:     action.RunID,
		ActionID:  action.ID,
		Tool:      action.Tool,
		Operation: action.Operation,
		Risk:      action.Risk,
		Status:    AutomationPermissionStatusPending,
		Version:   1,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}
	state.PermissionRequests = append(state.PermissionRequests, request)
	_, err = appendAutomationSupervisionAudit(
		state, allocator, AutomationAuditEventPermissionRequested, action, request.ID, "", now,
	)
	return request, err
}

func (s *AutomationService) executeSupervisedAction(ctx context.Context, actionID string) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if s == nil || s.store == nil || s.executor == nil {
		return NewAutomationValidationError("executor", "is unavailable")
	}
	var action AutomationAction
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		current := &state.Actions[index]
		if current.Status != AutomationActionApproved {
			return NewAutomationValidationError("action", "is not approved")
		}
		current.Status = AutomationActionExecuting
		current.Version++
		current.UpdatedAt = automationMutationTime(current.UpdatedAt)
		action = *current
		_, err := appendAutomationSupervisionAudit(
			state,
			allocator,
			AutomationAuditEventActionExecuting,
			action,
			"",
			"",
			action.UpdatedAt,
		)
		return err
	})
	if err != nil {
		return err
	}
	s.publishRuntimeEvent(AutomationRuntimeEvent{
		Type:     AutomationAuditEventActionExecuting,
		RunID:    action.RunID,
		ActionID: action.ID,
		Data:     automationRuntimeActionData(action, "", ""),
	})

	result, rawResult, err := executeAutomationActionForPersistence(ctx, s.executor, action)
	if err != nil {
		return err
	}
	var current AutomationAction
	eventType := AutomationAuditEventActionFailed
	err = s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		stored := &state.Actions[index]
		if stored.Status != AutomationActionExecuting {
			return NewAutomationValidationError("action", "is no longer executing")
		}
		stored.Status = result.Status
		stored.Result = rawResult
		stored.ErrorCode = strings.TrimSpace(result.ErrorCode)
		stored.ErrorMessage = strings.TrimSpace(result.ErrorMessage)
		stored.Retryable = result.Retryable
		stored.Version++
		stored.UpdatedAt = automationMutationTime(stored.UpdatedAt)
		current = *stored
		if current.Status == AutomationActionSucceeded {
			eventType = AutomationAuditEventActionSucceeded
		}
		_, err := appendAutomationSupervisionAudit(
			state,
			allocator,
			eventType,
			current,
			"",
			"",
			current.UpdatedAt,
		)
		return err
	})
	if err != nil {
		return err
	}
	current, err = s.getAction(current.ID)
	if err != nil {
		return err
	}
	s.publishRuntimeEvent(AutomationRuntimeEvent{
		Type:     eventType,
		RunID:    current.RunID,
		ActionID: current.ID,
		Data:     automationRuntimeActionData(current, "", ""),
	})
	s.reconcileChatTurnForRun(current.RunID)
	return nil
}

func (s *AutomationService) recoverInterruptedExecutingActions() error {
	if s == nil || s.store == nil {
		return NewAutomationValidationError("automation_service", "is unavailable")
	}
	runIDs := make(map[string]struct{})
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for i := range state.Actions {
			action := &state.Actions[i]
			if action.Status != AutomationActionExecuting {
				continue
			}
			now := automationMutationTime(action.UpdatedAt)
			action.Status = AutomationActionUnknown
			action.ErrorCode = "execution_interrupted_unknown"
			action.ErrorMessage = "workbench restarted while the action was executing; verify the external outcome before retrying"
			action.Retryable = false
			action.Version++
			action.UpdatedAt = now
			if _, err := appendAutomationSupervisionAudit(
				state,
				allocator,
				AutomationAuditEventActionFailed,
				*action,
				"",
				"",
				now,
			); err != nil {
				return err
			}
			runIDs[action.RunID] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}
	orderedRunIDs := make([]string, 0, len(runIDs))
	for runID := range runIDs {
		orderedRunIDs = append(orderedRunIDs, runID)
	}
	sort.Strings(orderedRunIDs)
	for _, runID := range orderedRunIDs {
		s.reconcileChatTurnForRun(runID)
	}
	return nil
}

func (s *AutomationService) appendActionAudit(eventType string, action AutomationAction, actorID string) error {
	return s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		_, err := appendAutomationSupervisionAudit(
			state, allocator, eventType, action, "", actorID, time.Now().UTC(),
		)
		return err
	})
}

func appendAutomationSupervisionAudit(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	eventType string,
	action AutomationAction,
	requestID string,
	actorID string,
	now time.Time,
) (AutomationAuditRecord, error) {
	data, err := json.Marshal(map[string]any{
		"permission_request_id": requestID,
		"provider_call_id":      action.ProviderCallID,
		"tool":                  action.Tool,
		"operation":             action.Operation,
		"risk":                  action.Risk,
		"status":                action.Status,
		"action_version":        action.Version,
		"result_id":             action.ResultID,
		"error_code":            action.ErrorCode,
		"error_message":         action.ErrorMessage,
	})
	if err != nil {
		return AutomationAuditRecord{}, err
	}
	actorType := "system"
	if actorID != "" {
		actorType = "operator"
	}
	return AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
		EventType: eventType,
		RunID:     action.RunID,
		ActionID:  action.ID,
		ActorType: actorType,
		ActorID:   actorID,
		Data:      sanitizeAutomationJSON(data),
		CreatedAt: now.UTC(),
	})
}

func deriveAutomationPermissionRule(
	action *AutomationAction,
	input automationPermissionDecisionRequest,
	now time.Time,
	allocator *AutomationIDAllocator,
) (AutomationPermissionRule, error) {
	if action.Tool == AutomationToolWorkbenchAllowOnce &&
		(!persistentHighRiskSendEnabled() ||
			!isPersistentHighRiskSendOperation(action.Operation)) {
		return AutomationPermissionRule{}, NewAutomationValidationError(
			"decision",
			"workbench.allow_once reusable rules are limited to enabled high-risk outbound operations",
		)
	}
	id, err := allocator.Next(AutomationIDKindPermissionRule)
	if err != nil {
		return AutomationPermissionRule{}, err
	}
	effect := AutomationPermissionEffectAllow
	if input.Decision == AutomationPermissionDenyPersistent {
		effect = AutomationPermissionEffectDeny
	}
	scopeJSON, scopeKey, err := automationPermissionScope(*action, input.Decision == AutomationPermissionAllowRun)
	if err != nil {
		return AutomationPermissionRule{}, err
	}
	return AutomationPermissionRule{
		ID:        id,
		Effect:    effect,
		Tool:      action.Tool,
		Operation: action.Operation,
		ScopeKey:  scopeKey,
		Scope:     scopeJSON,
		Enabled:   true,
		Version:   1,
		CreatedBy: input.OperatorID,
		CreatedAt: now.UTC(),
		UpdatedAt: now.UTC(),
	}, nil
}

func (s *AutomationService) publishPermissionEvent(request AutomationPermissionRequest, action AutomationAction) {
	if s == nil || s.events == nil {
		return
	}
	eventType := AutomationAuditEventPermissionDenied
	if request.Status == AutomationPermissionStatusAllowed {
		eventType = AutomationAuditEventPermissionAllowed
	}
	data, _ := json.Marshal(map[string]any{
		"decision":           request.Decision,
		"rule_id":            request.RuleID,
		"tool":               request.Tool,
		"operation":          request.Operation,
		"risk":               request.Risk,
		"permission_status":  request.Status,
		"permission_version": request.Version,
		"action_version":     action.Version,
		"operator_id":        request.OperatorID,
		"reason":             request.Reason,
	})
	s.publishRuntimeEvent(AutomationRuntimeEvent{
		Type:                eventType,
		RunID:               request.RunID,
		ActionID:            action.ID,
		PermissionRequestID: request.ID,
		Data:                data,
	})
}

func (s *AutomationService) publishPersistedActions(actionIDs []string) {
	if s == nil || s.store == nil {
		return
	}
	state := s.store.Snapshot()
	for _, actionID := range actionIDs {
		index := automationActionIndex(&state, actionID)
		if index < 0 {
			continue
		}
		action := state.Actions[index]
		var permissionRequest *AutomationPermissionRequest
		for _, request := range state.PermissionRequests {
			if request.ActionID == action.ID {
				requestCopy := request
				permissionRequest = &requestCopy
				break
			}
		}
		requestID := ""
		if permissionRequest != nil {
			requestID = permissionRequest.ID
		}
		s.publishRuntimeEvent(AutomationRuntimeEvent{
			Type:                AutomationAuditEventActionProposed,
			RunID:               action.RunID,
			ActionID:            action.ID,
			PermissionRequestID: requestID,
			Data:                automationRuntimeActionData(action, "", ""),
		})
		if permissionRequest != nil && permissionRequest.Status != AutomationPermissionStatusPending {
			s.publishPermissionEvent(*permissionRequest, action)
			continue
		}
		if permissionRequest != nil {
			s.publishRuntimeEvent(AutomationRuntimeEvent{
				Type:                AutomationAuditEventPermissionRequested,
				RunID:               action.RunID,
				ActionID:            action.ID,
				PermissionRequestID: requestID,
				Data:                automationRuntimeActionData(action, "", ""),
			})
			continue
		}
		effect, ruleID, matched := matchAutomationPermissionRule(&state, action)
		if action.PolicyDecision != AutomationDecisionReview &&
			action.Status != AutomationActionDenied {
			continue
		}
		eventType := AutomationAuditEventPermissionAllowed
		if !matched || effect == AutomationPermissionEffectDeny {
			eventType = AutomationAuditEventPermissionDenied
		}
		s.publishRuntimeEvent(AutomationRuntimeEvent{
			Type:     eventType,
			RunID:    action.RunID,
			ActionID: action.ID,
			Data:     automationRuntimeActionData(action, ruleID, effect),
		})
	}
}

func (s *AutomationService) publishRunEvent(eventType string, run AutomationRun, data json.RawMessage) {
	s.publishRuntimeEvent(AutomationRuntimeEvent{
		Type:  eventType,
		RunID: run.ID,
		Data:  data,
	})
}

func (s *AutomationService) publishRuntimeEvent(event AutomationRuntimeEvent) {
	if s == nil || s.events == nil {
		return
	}
	published := s.events.Publish(event)
	s.publishChatRuntimeEvent(published)
}

func automationPermissionRequestIndex(state *AutomationState, id string) int {
	for i := range state.PermissionRequests {
		if state.PermissionRequests[i].ID == id {
			return i
		}
	}
	return -1
}

func automationPermissionScope(action AutomationAction, runScoped bool) (json.RawMessage, string, error) {
	scope := map[string]any{}
	persistentOutbound := !runScoped &&
		action.Tool == AutomationToolWorkbenchAllowOnce &&
		persistentHighRiskSendEnabled() &&
		isPersistentHighRiskSendOperation(action.Operation)
	if !persistentOutbound {
		payloadDigest := sha256.Sum256(action.Payload)
		scope["arguments_sha256"] = hex.EncodeToString(payloadDigest[:])
	}
	if action.Operation != "" {
		scope["operation"] = action.Operation
	}
	if action.Recipient != nil {
		scope["chat_type"] = action.Recipient.ChatType
		scope["peer_id"] = action.Recipient.PeerID
		if !persistentOutbound {
			scope["audience_id"] = action.Recipient.AudienceID
		}
	}
	if runScoped {
		scope["run_id"] = action.RunID
	}
	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return nil, "", err
	}
	scopeDigest := sha256.Sum256(scopeJSON)
	return scopeJSON, string(action.Tool) + ":" + hex.EncodeToString(scopeDigest[:]), nil
}

func matchAutomationPermissionRule(state *AutomationState, action AutomationAction) (string, string, bool) {
	if action.Tool == AutomationToolWorkbenchAllowOnce &&
		(!persistentHighRiskSendEnabled() ||
			!isPersistentHighRiskSendOperation(action.Operation)) {
		return "", "", false
	}
	persistentScope, persistentKey, err := automationPermissionScope(action, false)
	if err != nil {
		return "", "", false
	}
	runScope, runKey, err := automationPermissionScope(action, true)
	if err != nil {
		return "", "", false
	}
	var allowedID string
	for _, rule := range state.PermissionRules {
		if !rule.Enabled || rule.Tool != action.Tool || rule.Operation != action.Operation {
			continue
		}
		matches := (rule.ScopeKey == persistentKey && bytes.Equal(rule.Scope, persistentScope)) ||
			(rule.ScopeKey == runKey && bytes.Equal(rule.Scope, runScope))
		if !matches {
			continue
		}
		if rule.Effect == AutomationPermissionEffectDeny {
			return rule.Effect, rule.ID, true
		}
		if rule.Effect == AutomationPermissionEffectAllow && allowedID == "" {
			allowedID = rule.ID
		}
	}
	if allowedID != "" {
		return AutomationPermissionEffectAllow, allowedID, true
	}
	return "", "", false
}

func automationRuntimeActionData(action AutomationAction, ruleID, effect string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"provider_call_id": action.ProviderCallID,
		"tool":             action.Tool,
		"operation":        action.Operation,
		"risk":             action.Risk,
		"status":           action.Status,
		"action_version":   action.Version,
		"arguments":        json.RawMessage(action.Payload),
		"result_id":        action.ResultID,
		"result":           json.RawMessage(action.Result),
		"error_code":       action.ErrorCode,
		"error_message":    action.ErrorMessage,
		"rule_id":          ruleID,
		"rule_effect":      effect,
	})
	return sanitizeAutomationJSON(data)
}

func containsAutomationPermissionDecision(
	decisions []AutomationPermissionDecision,
	target AutomationPermissionDecision,
) bool {
	for _, decision := range decisions {
		if decision == target {
			return true
		}
	}
	return false
}

func automationRuntimeRunData(run AutomationRun, values map[string]any) json.RawMessage {
	data := map[string]any{
		"provider_name": run.ProviderName,
		"status":        run.Status,
	}
	for key, value := range values {
		data[key] = value
	}
	encoded, _ := json.Marshal(data)
	return sanitizeAutomationJSON(encoded)
}

func cloneAutomationRuntimeEvent(event AutomationRuntimeEvent) AutomationRuntimeEvent {
	event.Data = cloneAutomationBytes(event.Data)
	return event
}

func sanitizeAutomationJSON(data json.RawMessage) json.RawMessage {
	if len(data) == 0 {
		return json.RawMessage(`{}`)
	}
	var value any
	if json.Unmarshal(data, &value) != nil {
		return json.RawMessage(`{}`)
	}
	value = sanitizeAutomationValue(value)
	canonical, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return canonical
}

func sanitizeAutomationValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "authorization") ||
				strings.Contains(lower, "api_key") ||
				strings.Contains(lower, "apikey") ||
				strings.Contains(lower, "token") ||
				strings.Contains(lower, "secret") ||
				strings.Contains(lower, "password") {
				typed[key] = "[REDACTED]"
				continue
			}
			typed[key] = sanitizeAutomationValue(item)
		}
		return typed
	case []any:
		for i := range typed {
			typed[i] = sanitizeAutomationValue(typed[i])
		}
	}
	return value
}
