package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

type AutomationService struct {
	store       *AutomationStore
	crm         *CRMStore
	engine      AutomationRunEngine
	executor    AutomationCommandExecutor
	risk        AutomationRiskEvaluator
	audit       *AutomationAuditLedger
	events      *AutomationRuntimeEventBroker
	chat        *automationChatCoordinator
	schedulerMu sync.Mutex
}

type automationActionMutationRequest struct {
	ExpectedVersion uint64          `json:"expected_version"`
	OperatorID      string          `json:"operator_id"`
	Reason          string          `json:"reason,omitempty"`
	Arguments       json.RawMessage `json:"arguments,omitempty"`
}

type automationPolicyUpdateRequest struct {
	ExpectedVersion    uint64               `json:"expected_version"`
	Mode               AutomationMode       `json:"mode"`
	R1AutoApprove      bool                 `json:"r1_auto_approve"`
	R1MinConfidence    float64              `json:"r1_min_confidence"`
	R1AllowedTools     []AutomationToolName `json:"r1_allowed_tools"`
	R2PrivateReplyAuto bool                 `json:"r2_private_reply_auto_approve"`
	R2MinConfidence    float64              `json:"r2_min_confidence"`
	R2AllowedIntents   []string             `json:"r2_allowed_intents"`
}

func NewAutomationService(
	store *AutomationStore,
	crm *CRMStore,
	engine AutomationRunEngine,
	executor AutomationCommandExecutor,
	risk AutomationRiskEvaluator,
	audit *AutomationAuditLedger,
) *AutomationService {
	if evaluator, ok := risk.(*ServerAutomationRiskEvaluator); ok {
		evaluator.crm = crm
	}
	service := &AutomationService{
		store:    store,
		crm:      crm,
		engine:   engine,
		executor: executor,
		risk:     risk,
		audit:    audit,
		events:   NewAutomationRuntimeEventBroker(256),
	}
	service.chat = newAutomationChatCoordinator(service)
	if defaultEngine, ok := engine.(*defaultAutomationRunEngine); ok {
		defaultEngine.supervision = service
	}
	return service
}

func (s *AutomationService) ProcessEvent(ctx context.Context, event MessageEvent) (AutomationRun, error) {
	if s == nil || s.engine == nil {
		return AutomationRun{}, NewAutomationValidationError("automation_service", "run engine is unavailable")
	}
	run, _, err := s.engine.EnsureRun(ctx, event, "")
	if err != nil {
		return AutomationRun{}, err
	}
	if run.Status == AutomationRunQueued {
		if err := s.engine.ProcessRun(ctx, run.ID); err != nil {
			return run, err
		}
	}
	for _, item := range s.store.Snapshot().Runs {
		if item.ID == run.ID {
			return item, nil
		}
	}
	return run, nil
}

func (s *AutomationService) ListActions(
	status AutomationActionStatus,
	tool AutomationToolName,
	risk AutomationRisk,
	sourceEventID string,
	limit int,
	offset int,
) (int, []automationActionProjection, error) {
	if s == nil || s.store == nil {
		return 0, nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	state := s.store.Snapshot()
	items := make([]AutomationAction, 0)
	for _, action := range state.Actions {
		if status != "" && action.Status != status {
			continue
		}
		if tool != "" && action.Tool != tool {
			continue
		}
		if risk != "" && action.Risk != risk {
			continue
		}
		if sourceEventID != "" && action.SourceMessage.EventID != sourceEventID {
			continue
		}
		items = append(items, action)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	total := len(items)
	start, end := automationPage(total, limit, offset)
	projected := make([]automationActionProjection, 0, end-start)
	for _, action := range items[start:end] {
		projected = append(projected, automationProjectAction(state, action))
	}
	return total, projected, nil
}

func (s *AutomationService) ActionDetail(id string) (map[string]any, error) {
	if s == nil || s.store == nil || s.crm == nil {
		return nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	state := s.store.Snapshot()
	index := automationActionIndex(&state, id)
	if index < 0 {
		return nil, errors.New("automation action not found")
	}
	action := state.Actions[index]
	projectedAction := automationProjectAction(state, action)
	runIndex := automationRunIndex(&state, action.RunID)
	var run AutomationRun
	if runIndex >= 0 {
		run = state.Runs[runIndex]
	}
	source, _ := automationFindMessageEvent(s.crm.Snapshot(), action.SourceMessage.EventID)
	approvals := make([]AutomationApprovalRecord, 0)
	for _, approval := range state.Approvals {
		if approval.ActionID == action.ID {
			approvals = append(approvals, approval)
		}
	}
	var outbox any
	for _, entry := range state.Outbox {
		if entry.ActionID == action.ID {
			value := entry
			outbox = value
			break
		}
	}
	checks := make([]map[string]any, 0, len(action.Preconditions))
	crmState := s.crm.Snapshot()
	for _, check := range action.Preconditions {
		current, ok := automationCRMEntityVersion(crmState, check.EntityType, check.EntityID)
		checks = append(checks, map[string]any{
			"entity_type":      check.EntityType,
			"entity_id":        check.EntityID,
			"expected_version": check.ExpectedVersion,
			"current_version":  current,
			"state":            map[bool]string{true: "current", false: "stale"}[ok && current == check.ExpectedVersion],
		})
	}
	return map[string]any{
		"action":              projectedAction,
		"run":                 run,
		"source_message":      source,
		"approvals":           approvals,
		"outbox":              outbox,
		"audit_events":        s.auditFor(action.RunID, action.ID),
		"dependencies":        projectedAction.Dependencies,
		"dependency_state":    projectedAction.Scheduler.State,
		"not_ready_reason":    projectedAction.NotReadyReason,
		"result_entity":       projectedAction.ResultEntity,
		"precondition_checks": checks,
		"diff":                map[string]any{"changes": []any{}},
	}, nil
}

func (s *AutomationService) ApproveAction(
	ctx context.Context,
	actionID string,
	request automationActionMutationRequest,
) (AutomationAction, error) {
	return s.decideLegacyAction(ctx, actionID, request, AutomationPermissionAllowOnce)
}

func (s *AutomationService) RejectAction(
	actionID string,
	request automationActionMutationRequest,
) (AutomationAction, error) {
	return s.decideLegacyAction(context.Background(), actionID, request, AutomationPermissionDenyOnce)
}

func (s *AutomationService) EditApproveAction(
	ctx context.Context,
	actionID string,
	request automationActionMutationRequest,
) (AutomationAction, error) {
	if len(request.Arguments) == 0 {
		return AutomationAction{}, NewAutomationValidationError("arguments", "is required")
	}
	var replacement AutomationAction
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		original := &state.Actions[index]
		if err := automationValidateReviewRequest(*original, request); err != nil {
			return err
		}
		operation := WorkbenchOperation("")
		var canonical json.RawMessage
		if IsWorkbenchToolName(original.Tool) {
			decoded, err := DecodeWorkbenchToolRequest(original.Tool, request.Arguments)
			if err != nil {
				return err
			}
			operation = decoded.Request.Operation
			canonical = append(json.RawMessage(nil), decoded.CanonicalRequest...)
		} else {
			arguments, err := DecodeAutomationToolArguments(original.Tool, request.Arguments)
			if err != nil {
				return err
			}
			canonical, err = json.Marshal(arguments)
			if err != nil {
				return err
			}
		}
		call := AutomationToolCall{
			CallID:     original.ProviderCallID,
			Tool:       original.Tool,
			Confidence: original.Confidence,
			Evidence:   original.Evidence,
			Arguments:  canonical,
		}
		callToAction := make(map[string]string)
		for _, action := range state.Actions {
			if action.RunID == original.RunID {
				callToAction[action.ProviderCallID] = action.ID
			}
		}
		preconditions, dependencies, err := automationActionReferences(call, callToAction)
		if err != nil {
			return err
		}
		id, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		source := original.SourceMessage
		policy := s.risk.Evaluate(state.Policy, AutomationPolicyInput{
			Tool:           original.Tool,
			Confidence:     original.Confidence,
			Payload:        canonical,
			SourceMessage:  source,
			RunActionCount: 1,
		})
		status := AutomationActionApproved
		if policy.Decision == AutomationDecisionDeny {
			status = AutomationActionDenied
		}
		now := automationMutationTime(original.UpdatedAt)
		replacement = *original
		replacement.ID = id
		replacement.ProviderCallID = original.ProviderCallID + "-edit-" + id
		replacement.Operation = operation
		replacement.Status = status
		replacement.Risk = policy.Risk
		replacement.PolicyDecision = policy.Decision
		replacement.PolicyVersion = state.Policy.Version
		replacement.RiskReasons = append([]string{}, policy.RiskReasons...)
		replacement.Preconditions = preconditions
		replacement.DependsOnActionIDs = dependencies
		replacement.Recipient = policy.Recipient
		replacement.Payload = canonical
		replacement.Result = nil
		replacement.ErrorCode = ""
		replacement.ErrorMessage = ""
		replacement.Retryable = false
		replacement.Attempt = 1
		replacement.ReplacesActionID = original.ID
		replacement.ReplacedByActionID = ""
		replacement.Version = 1
		replacement.CreatedAt = now
		replacement.UpdatedAt = now

		original.Status = AutomationActionSuperseded
		original.ReplacedByActionID = id
		original.Version++
		original.UpdatedAt = now
		for i := range state.PermissionRequests {
			pending := &state.PermissionRequests[i]
			if pending.ActionID == original.ID && pending.Status == AutomationPermissionStatusPending {
				pending.Status = AutomationPermissionStatusExpired
				pending.Version++
				pending.UpdatedAt = now
			}
		}
		state.Actions = append(state.Actions, replacement)
		runIndex := automationRunIndex(state, original.RunID)
		state.Runs[runIndex].ActionIDs = append(state.Runs[runIndex].ActionIDs, id)
		approvalID, err := allocator.Next(AutomationIDKindApproval)
		if err != nil {
			return err
		}
		state.Approvals = append(state.Approvals, AutomationApprovalRecord{
			ID:                  approvalID,
			ActionID:            original.ID,
			ActionVersion:       original.Version,
			Decision:            "edit_approve",
			OperatorID:          strings.TrimSpace(request.OperatorID),
			Reason:              strings.TrimSpace(request.Reason),
			ReplacementActionID: id,
			CreatedAt:           now,
		})
		if replacement.Tool == AutomationToolWorkbenchAllowOnce &&
			replacement.Status == AutomationActionApproved {
			permissionID, err := allocator.Next(AutomationIDKindPermissionRequest)
			if err != nil {
				return err
			}
			operatorID := strings.TrimSpace(request.OperatorID)
			permission := AutomationPermissionRequest{
				ID:         permissionID,
				RunID:      replacement.RunID,
				ActionID:   replacement.ID,
				Tool:       replacement.Tool,
				Operation:  replacement.Operation,
				Risk:       replacement.Risk,
				Status:     AutomationPermissionStatusAllowed,
				Version:    1,
				Decision:   AutomationPermissionAllowOnce,
				OperatorID: operatorID,
				Reason:     strings.TrimSpace(request.Reason),
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			state.PermissionRequests = append(state.PermissionRequests, permission)
			if _, err := appendAutomationSupervisionAudit(
				state,
				allocator,
				AutomationAuditEventPermissionAllowed,
				replacement,
				permission.ID,
				operatorID,
				now,
			); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AutomationAction{}, err
	}
	if replacement.Status == AutomationActionApproved {
		if err := s.ExecuteReadyActions(ctx, replacement.RunID); err != nil {
			return AutomationAction{}, err
		}
	}
	return s.getAction(replacement.ID)
}

func (s *AutomationService) RetryAction(
	ctx context.Context,
	actionID string,
	request automationActionMutationRequest,
) (AutomationAction, error) {
	var retry AutomationAction
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		source := &state.Actions[index]
		if source.Version != request.ExpectedVersion {
			return NewAutomationValidationError("expected_version", "action version conflict")
		}
		if strings.TrimSpace(request.OperatorID) == "" {
			return NewAutomationValidationError("operator_id", "is required")
		}
		if !source.Retryable || source.Status != AutomationActionFailed {
			return NewAutomationValidationError("action", "is not retryable")
		}
		for _, entry := range state.Outbox {
			if entry.ActionID == source.ID && entry.DispatchAttempted {
				return NewAutomationValidationError("action", "dispatch was already attempted")
			}
		}
		id, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		now := automationMutationTime(source.UpdatedAt)
		retry = *source
		retry.ID = id
		retry.ProviderCallID = source.ProviderCallID + "-retry-" + id
		retry.Status = AutomationActionApproved
		if source.Tool == AutomationToolWorkbenchAllowOnce {
			retry.Status = AutomationActionAwaitingReview
		}
		retry.Result = nil
		retry.ErrorCode = ""
		retry.ErrorMessage = ""
		retry.Retryable = false
		retry.Attempt = source.Attempt + 1
		retry.RetryOfActionID = source.ID
		retry.Version = 1
		retry.CreatedAt = now
		retry.UpdatedAt = now
		state.Actions = append(state.Actions, retry)
		runIndex := automationRunIndex(state, source.RunID)
		state.Runs[runIndex].ActionIDs = append(state.Runs[runIndex].ActionIDs, id)
		if retry.Status == AutomationActionAwaitingReview {
			if _, err := PersistAutomationPermissionRequest(state, allocator, retry, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return AutomationAction{}, err
	}
	if retry.Status == AutomationActionApproved {
		if err := s.ExecuteReadyActions(ctx, retry.RunID); err != nil {
			return AutomationAction{}, err
		}
	}
	return s.getAction(retry.ID)
}

func (s *AutomationService) Policy() map[string]any {
	policy := s.store.Snapshot().Policy
	return map[string]any{
		"policy": policy,
		"immutable_rules": map[string]any{
			"r3": []string{
				"群发、群聊发送、录用、Offer、拒绝、归档和敏感承诺始终人工审核",
			},
			"r4": []string{
				"任意目标、SQL、Shell、支付、凭据、文件路径和非受控 HTTP 操作始终拒绝",
			},
		},
	}
}

func (s *AutomationService) UpdatePolicy(request automationPolicyUpdateRequest) (map[string]any, error) {
	policy := AutomationPolicy{
		Version:                   request.ExpectedVersion + 1,
		Mode:                      request.Mode,
		R1AutoApprove:             request.R1AutoApprove,
		R1MinConfidence:           request.R1MinConfidence,
		R1AllowedTools:            append([]AutomationToolName{}, request.R1AllowedTools...),
		R2PrivateReplyAutoApprove: request.R2PrivateReplyAuto,
		R2MinConfidence:           request.R2MinConfidence,
		R2AllowedIntents:          append([]string{}, request.R2AllowedIntents...),
		UpdatedAt:                 time.Now().UTC(),
	}
	if err := ValidateAutomationPolicy(policy); err != nil {
		return nil, err
	}
	err := s.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		if state.Policy.Version != request.ExpectedVersion {
			return NewAutomationValidationError("expected_version", "policy version conflict")
		}
		state.Policy = policy
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Policy(), nil
}

func (s *AutomationService) ListRuns(
	status AutomationRunStatus,
	sourceEventID string,
	limit int,
	offset int,
) (int, []automationRunProjection, error) {
	state := s.store.Snapshot()
	items := make([]AutomationRun, 0)
	for _, run := range state.Runs {
		if status != "" && run.Status != status {
			continue
		}
		if sourceEventID != "" && run.SourceMessageEventID != sourceEventID {
			continue
		}
		items = append(items, run)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	total := len(items)
	start, end := automationPage(total, limit, offset)
	projected := make([]automationRunProjection, 0, end-start)
	for _, run := range items[start:end] {
		projected = append(projected, automationRunProjection{
			AutomationRun: run,
			ActionSummary: automationRunSummary(state, run.ID),
		})
	}
	return total, projected, nil
}

func (s *AutomationService) RunDetail(id string) (map[string]any, error) {
	state := s.store.Snapshot()
	index := automationRunIndex(&state, id)
	if index < 0 {
		return nil, errors.New("automation run not found")
	}
	run := state.Runs[index]
	source, _ := automationFindMessageEvent(s.crm.Snapshot(), run.SourceMessageEventID)
	actions := make([]automationActionProjection, 0, len(run.ActionIDs))
	for _, action := range state.Actions {
		if action.RunID == run.ID {
			actions = append(actions, automationProjectAction(state, action))
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		return actions[i].ID < actions[j].ID
	})
	return map[string]any{
		"run":            run,
		"source_message": source,
		"actions":        actions,
		"action_summary": automationRunSummary(state, run.ID),
		"audit_events":   s.auditFor(run.ID, ""),
	}, nil
}

func (s *AutomationService) reviewAction(
	actionID string,
	request automationActionMutationRequest,
	decision string,
	target AutomationActionStatus,
) (AutomationAction, error) {
	var updated AutomationAction
	err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		action := &state.Actions[index]
		if err := automationValidateReviewRequest(*action, request); err != nil {
			return err
		}
		now := automationMutationTime(action.UpdatedAt)
		action.Status = target
		action.Version++
		action.UpdatedAt = now
		approvalID, err := allocator.Next(AutomationIDKindApproval)
		if err != nil {
			return err
		}
		state.Approvals = append(state.Approvals, AutomationApprovalRecord{
			ID:            approvalID,
			ActionID:      action.ID,
			ActionVersion: action.Version,
			Decision:      decision,
			OperatorID:    strings.TrimSpace(request.OperatorID),
			Reason:        strings.TrimSpace(request.Reason),
			CreatedAt:     now,
		})
		updated = *action
		return nil
	})
	return updated, err
}

func automationValidateReviewRequest(
	action AutomationAction,
	request automationActionMutationRequest,
) error {
	if action.Status != AutomationActionAwaitingReview {
		return NewAutomationValidationError("action", "is not awaiting review")
	}
	if action.Version != request.ExpectedVersion {
		return NewAutomationValidationError("expected_version", "action version conflict")
	}
	if strings.TrimSpace(request.OperatorID) == "" {
		return NewAutomationValidationError("operator_id", "is required")
	}
	return nil
}

func (s *AutomationService) getAction(id string) (AutomationAction, error) {
	state := s.store.Snapshot()
	index := automationActionIndex(&state, id)
	if index < 0 {
		return AutomationAction{}, errors.New("automation action not found")
	}
	return state.Actions[index], nil
}

func (s *AutomationService) auditFor(runID, actionID string) []AutomationAuditRecord {
	records := make([]AutomationAuditRecord, 0)
	if s.audit != nil {
		if committed, err := s.audit.ReadAll(); err == nil {
			records = append(records, committed...)
		}
	}
	records = append(records, s.store.Snapshot().PendingAudit...)
	filtered := records[:0]
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		if runID != "" && record.RunID != runID {
			continue
		}
		if actionID != "" && record.ActionID != actionID {
			continue
		}
		if _, ok := seen[record.ID]; ok {
			continue
		}
		seen[record.ID] = struct{}{}
		filtered = append(filtered, record)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID < filtered[j].ID
		}
		return filtered[i].CreatedAt.Before(filtered[j].CreatedAt)
	})
	return filtered
}

func automationPage(total, limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
}
