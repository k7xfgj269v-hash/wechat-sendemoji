package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func workbenchRecruitingEnsureBridgeAction(
	ctx context.Context,
	store *AutomationStore,
	parent AutomationAction,
	execution WorkbenchExecutionContext,
	spec workbenchRecruitingBridgeSpec,
) (AutomationAction, bool, error) {
	var bridge AutomationAction
	created := false
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		for i := range state.Actions {
			candidate := &state.Actions[i]
			if candidate.RetryOfActionID == parent.ID &&
				strings.HasPrefix(candidate.ProviderCallID, spec.Kind+"-") {
				if candidate.Tool != spec.Tool ||
					candidate.Operation != spec.Operation ||
					!jsonEqual(candidate.Payload, spec.Payload) {
					return NewAutomationValidationError("bridge_action", "conflicts with an existing approved execution")
				}
				bridge = cloneAutomationAction(*candidate)
				return errWorkbenchRecruitingNoMutation
			}
		}
		parentIndex := automationActionIndex(state, parent.ID)
		if parentIndex < 0 {
			return errors.New("approved workbench action was not found")
		}
		runIndex := automationRunIndex(state, parent.RunID)
		if runIndex < 0 {
			return errors.New("approved workbench run was not found")
		}
		if len(state.Runs[runIndex].ActionIDs) >= AutomationMaxActionsPerRun {
			return NewAutomationValidationError("bridge_action", "run action limit reached")
		}
		if spec.Audience != nil {
			if err := workbenchRecruitingAudienceRateLimit(
				*state,
				parent.RunID,
				*spec.Audience,
				time.Now().UTC(),
			); err != nil {
				return err
			}
		}
		id, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		now := automationMutationTime(state.Actions[parentIndex].UpdatedAt)
		bridge = AutomationAction{
			ID:                 id,
			RunID:              parent.RunID,
			ProviderCallID:     spec.Kind + "-" + id,
			Tool:               spec.Tool,
			Operation:          spec.Operation,
			Status:             AutomationActionApproved,
			Risk:               AutomationRiskR3,
			PolicyDecision:     AutomationDecisionReview,
			PolicyVersion:      parent.PolicyVersion,
			RiskReasons:        []string{spec.RiskReason},
			Confidence:         parent.Confidence,
			Evidence:           append([]AutomationEvidence{}, parent.Evidence...),
			SourceMessage:      parent.SourceMessage,
			Preconditions:      []AutomationEntityPrecondition{},
			DependsOnActionIDs: []string{},
			Recipient:          cloneAutomationRecipient(spec.Recipient),
			Payload:            append(json.RawMessage(nil), spec.Payload...),
			Attempt:            1,
			RetryOfActionID:    parent.ID,
			Version:            1,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		state.Actions = append(state.Actions, bridge)
		state.Runs[runIndex].ActionIDs = append(state.Runs[runIndex].ActionIDs, bridge.ID)
		permissionID, err := allocator.Next(AutomationIDKindPermissionRequest)
		if err != nil {
			return err
		}
		permission := AutomationPermissionRequest{
			ID:         permissionID,
			RunID:      bridge.RunID,
			ActionID:   bridge.ID,
			Tool:       bridge.Tool,
			Operation:  bridge.Operation,
			Risk:       bridge.Risk,
			Status:     AutomationPermissionStatusAllowed,
			Version:    1,
			Decision:   AutomationPermissionAllowOnce,
			OperatorID: strings.TrimSpace(execution.OperatorID),
			Reason:     "approved server-side execution for " + string(parent.Operation),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		state.PermissionRequests = append(state.PermissionRequests, permission)
		if _, err := appendAutomationSupervisionAudit(
			state,
			allocator,
			AutomationAuditEventActionProposed,
			bridge,
			"",
			execution.OperatorID,
			now,
		); err != nil {
			return err
		}
		if _, err := appendAutomationSupervisionAudit(
			state,
			allocator,
			AutomationAuditEventPermissionAllowed,
			bridge,
			permission.ID,
			execution.OperatorID,
			now,
		); err != nil {
			return err
		}
		created = true
		return nil
	})
	if errors.Is(err, errWorkbenchRecruitingNoMutation) {
		return bridge, false, nil
	}
	return bridge, created, err
}

func workbenchRecruitingClaimBridgeAction(
	store *AutomationStore,
	actionID string,
	operatorID string,
) (AutomationAction, bool, error) {
	var action AutomationAction
	claimed := false
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("approved execution record was not found")
		}
		current := &state.Actions[index]
		action = cloneAutomationAction(*current)
		if current.Status != AutomationActionApproved {
			return errWorkbenchRecruitingNoMutation
		}
		current.Status = AutomationActionExecuting
		current.Version++
		current.UpdatedAt = automationMutationTime(current.UpdatedAt)
		action = cloneAutomationAction(*current)
		claimed = true
		_, err := appendAutomationSupervisionAudit(
			state,
			allocator,
			AutomationAuditEventActionExecuting,
			action,
			"",
			operatorID,
			action.UpdatedAt,
		)
		return err
	})
	if errors.Is(err, errWorkbenchRecruitingNoMutation) {
		return action, false, nil
	}
	return action, claimed, err
}

func workbenchRecruitingFinishBridgeAction(
	store *AutomationStore,
	actionID string,
	result AutomationExecutionResult,
	operatorID string,
) (AutomationAction, error) {
	encoded, err := json.Marshal(result.Result)
	if err != nil {
		return AutomationAction{}, err
	}
	return workbenchRecruitingFinishBridgeActionWithResult(
		store,
		actionID,
		result,
		encoded,
		operatorID,
	)
}

func workbenchRecruitingFinishBridgeActionWithResult(
	store *AutomationStore,
	actionID string,
	result AutomationExecutionResult,
	persistedResult json.RawMessage,
	operatorID string,
) (AutomationAction, error) {
	var action AutomationAction
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("approved execution record was not found")
		}
		current := &state.Actions[index]
		if current.Status != AutomationActionExecuting {
			action = cloneAutomationAction(*current)
			return errWorkbenchRecruitingNoMutation
		}
		current.Status = result.Status
		current.Result = append(json.RawMessage(nil), persistedResult...)
		current.ErrorCode = result.ErrorCode
		current.ErrorMessage = result.ErrorMessage
		current.Retryable = result.Retryable
		current.Version++
		current.UpdatedAt = automationMutationTime(current.UpdatedAt)
		action = cloneAutomationAction(*current)
		eventType := AutomationAuditEventActionFailed
		if current.Status == AutomationActionSucceeded {
			eventType = AutomationAuditEventActionSucceeded
		}
		_, appendErr := appendAutomationSupervisionAudit(
			state,
			allocator,
			eventType,
			action,
			"",
			operatorID,
			action.UpdatedAt,
		)
		return appendErr
	})
	if errors.Is(err, errWorkbenchRecruitingNoMutation) {
		return action, nil
	}
	return action, err
}

func workbenchRecruitingApprovedParent(
	store *AutomationStore,
	execution WorkbenchExecutionContext,
	operation WorkbenchOperation,
	arguments any,
) (AutomationAction, error) {
	if store == nil {
		return AutomationAction{}, errors.New("automation store is unavailable")
	}
	actionID := strings.TrimSpace(execution.ActionID)
	permissionID := strings.TrimSpace(execution.PermissionRequestID)
	if actionID == "" || permissionID == "" {
		return AutomationAction{}, NewAutomationValidationError("execution", "approved action identity is required")
	}
	state := store.Snapshot()
	index := automationActionIndex(&state, actionID)
	if index < 0 {
		return AutomationAction{}, errors.New("approved workbench action was not found")
	}
	action := state.Actions[index]
	if action.Tool != AutomationToolWorkbenchAllowOnce ||
		action.Operation != operation ||
		action.Status != AutomationActionExecuting {
		return AutomationAction{}, NewAutomationValidationError("execution", "approved workbench action is not executing")
	}
	decoded, err := DecodeWorkbenchToolRequest(action.Tool, action.Payload)
	if err != nil {
		return AutomationAction{}, errors.New("approved workbench action payload is invalid")
	}
	approvedJSON, err := json.Marshal(decoded.Arguments)
	if err != nil {
		return AutomationAction{}, errors.New("approved workbench action payload is invalid")
	}
	requestedJSON, err := json.Marshal(arguments)
	if err != nil || string(approvedJSON) != string(requestedJSON) {
		return AutomationAction{}, NewAutomationValidationError(
			"execution",
			"approved workbench action does not match the requested arguments",
		)
	}
	for _, permission := range state.PermissionRequests {
		if permission.ID != permissionID || permission.ActionID != action.ID {
			continue
		}
		persistent := permission.Decision == AutomationPermissionAllowPersistent &&
			persistentHighRiskSendEnabled() &&
			isPersistentHighRiskSendOperation(operation) &&
			permission.RuleID != ""
		allowOnce := permission.Decision == AutomationPermissionAllowOnce &&
			permission.RuleID == ""
		if permission.Tool != action.Tool ||
			permission.Operation != action.Operation ||
			permission.Status != AutomationPermissionStatusAllowed ||
			(!allowOnce && !persistent) ||
			permission.OperatorID != strings.TrimSpace(execution.OperatorID) ||
			permission.OperatorID == automationPermissionOperatorAuto {
			break
		}
		return cloneAutomationAction(action), nil
	}
	return AutomationAction{}, NewAutomationValidationError("execution", "fresh allow_once approval is invalid")
}

func workbenchRecruitingFindBridge(
	state AutomationState,
	parentID string,
	kind string,
) (AutomationAction, bool) {
	for _, action := range state.Actions {
		if action.RetryOfActionID == parentID &&
			strings.HasPrefix(action.ProviderCallID, kind+"-") {
			return cloneAutomationAction(action), true
		}
	}
	return AutomationAction{}, false
}

func workbenchRecruitingDeleteBridgeResult(
	operation WorkbenchOperation,
	bridge AutomationAction,
) WorkbenchToolResult {
	if bridge.Status == AutomationActionApproved || bridge.Status == AutomationActionExecuting {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorConflict, "approved delete is already executing", false)
	}
	if bridge.Status != AutomationActionSucceeded {
		code := workbenchRecruitingExecutionCode(bridge)
		return NewWorkbenchToolFailure(
			operation,
			code,
			"approved delete failed",
			bridge.Retryable || code == WorkbenchErrorUnavailable,
		)
	}
	var data workbenchRecruitingDeleteResult
	if json.Unmarshal(bridge.Result, &data) != nil ||
		!data.Deleted ||
		strings.TrimSpace(data.ID) == "" {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInternal, "approved delete result is unavailable", false)
	}
	return NewWorkbenchToolSuccess(operation, data, nil)
}

func workbenchRecruitingContextExecution(
	ctx context.Context,
	result AutomationExecutionResult,
) AutomationExecutionResult {
	if ctx == nil || result.Status != AutomationActionFailed {
		return result
	}
	err := ctx.Err()
	if err == nil {
		return result
	}
	switch strings.TrimSpace(result.ErrorCode) {
	case "outbox_failed", "outbox_claim_failed", "context_canceled", "cancelled", "deadline_exceeded":
	default:
		return result
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return automationExecutionFailure("deadline_exceeded", err.Error(), false)
	}
	return automationExecutionFailure("cancelled", err.Error(), false)
}

func workbenchRecruitingPublicExecution(
	result AutomationExecutionResult,
	failureMessage string,
) AutomationExecutionResult {
	switch result.Status {
	case AutomationActionSucceeded:
		result.ErrorCode = ""
		result.ErrorMessage = ""
		result.Retryable = false
	case AutomationActionStale:
		result.ErrorCode = "conflict"
		result.ErrorMessage = failureMessage
		result.Retryable = false
	case AutomationActionUnknown:
		result.ErrorCode = "outcome_unknown"
		result.ErrorMessage = failureMessage
		result.Retryable = false
	default:
		result.Status = AutomationActionFailed
		if strings.TrimSpace(result.ErrorCode) == "" {
			result.ErrorCode = "execution_failed"
		}
		result.ErrorMessage = failureMessage
	}
	return result
}
