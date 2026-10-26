package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func workbenchRecruitingForwardEvent(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationForwardingSendEvent
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	if unavailable := workbenchRecruitingRequireOutbound(dispatcher, operation, false); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchForwardEventArguments)
	parent, err := workbenchRecruitingApprovedParent(
		dispatcher.automation,
		execution,
		operation,
		arguments,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	payload, err := json.Marshal(ForwardEventArgs{
		EventID: arguments.EventID,
		RuleID:  arguments.RuleID,
		Reason:  strings.TrimSpace(arguments.Reason),
	})
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if replay := workbenchRecruitingForwardReplay(
		dispatcher.automation,
		parent.ID,
		arguments.EventID,
		arguments.RuleID,
		payload,
	); replay != nil {
		return *replay
	}
	_, _, err = workbenchRecruitingResolveForward(
		dispatcher.crm,
		dispatcher.forwardRules,
		arguments.EventID,
		arguments.RuleID,
		false,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	bridge, _, err := workbenchRecruitingEnsureBridgeAction(
		ctx,
		dispatcher.automation,
		parent,
		execution,
		workbenchRecruitingBridgeSpec{
			Kind:       workbenchRecruitingBridgeForward,
			Tool:       AutomationToolForwardEvent,
			Payload:    payload,
			RiskReason: automationReasonR3ConfiguredForward,
		},
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	bridge, claimed, err := workbenchRecruitingClaimBridgeAction(
		dispatcher.automation,
		bridge.ID,
		execution.OperatorID,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if !claimed {
		return workbenchRecruitingForwardBridgeResult(bridge, arguments.EventID, arguments.RuleID)
	}

	delivery := workbenchRecruitingExecuteForwardEvent(
		ctx,
		dispatcher,
		bridge,
		ForwardEventArgs{
			EventID: arguments.EventID,
			RuleID:  arguments.RuleID,
			Reason:  strings.TrimSpace(arguments.Reason),
		},
	)
	delivery = workbenchRecruitingPublicExecution(delivery, "configured forward failed")
	bridge, finishErr := workbenchRecruitingFinishBridgeAction(
		dispatcher.automation,
		bridge.ID,
		delivery,
		execution.OperatorID,
	)
	if finishErr != nil {
		return workbenchRecruitingFailure(operation, finishErr)
	}
	return workbenchRecruitingForwardBridgeResult(bridge, arguments.EventID, arguments.RuleID)
}

func workbenchRecruitingExecuteForwardEvent(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	action AutomationAction,
	arguments ForwardEventArgs,
) AutomationExecutionResult {
	sink := strings.TrimSpace(config.Sink)
	if _, _, err := parseForwardSink(sink); err != nil {
		return automationExecutionFailure("forward_sink_unavailable", err.Error(), true)
	}
	event, rule, err := workbenchRecruitingResolveForward(
		dispatcher.crm,
		dispatcher.forwardRules,
		arguments.EventID,
		arguments.RuleID,
		true,
	)
	if err != nil {
		switch {
		case errors.Is(err, errForwardRuleCooldown):
			return automationExecutionFailure("forward_cooldown_active", err.Error(), true)
		case errors.Is(err, errForwardRuleNotFound), errors.Is(err, errForwardRuleDisabled):
			return automationExecutionFailure("forward_rule_unavailable", err.Error(), false)
		default:
			return automationExecutionFailure("forward_rule_mismatch", err.Error(), false)
		}
	}
	payload, err := json.Marshal(forwardRecordFromMessageEvent(event))
	if err != nil {
		return automationExecutionFailure("forward_payload_invalid", err.Error(), false)
	}
	if err := deliverSinkSync(ctx, sink, payload); err != nil {
		return automationExecutionFailure("forward_delivery_failed", err.Error(), true)
	}
	auditData, err := json.Marshal(struct {
		EventID string `json:"event_id"`
		RuleID  string `json:"rule_id"`
		Reason  string `json:"reason,omitempty"`
	}{
		EventID: event.ID,
		RuleID:  rule.ID,
		Reason:  arguments.Reason,
	})
	if err != nil {
		return automationExecutionFailure("forward_audit_failed", err.Error(), false)
	}
	err = dispatcher.automation.Update(func(
		state *AutomationState,
		allocator *AutomationIDAllocator,
	) error {
		_, appendErr := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: AutomationAuditEventForwardDispatched,
			RunID:     action.RunID,
			ActionID:  action.ID,
			ActorType: "system",
			Data:      sanitizeAutomationJSON(auditData),
			CreatedAt: time.Now().UTC(),
		})
		return appendErr
	})
	if err != nil {
		return AutomationExecutionResult{
			Status:       AutomationActionUnknown,
			ErrorCode:    "forward_audit_unknown",
			ErrorMessage: err.Error(),
		}
	}
	return AutomationExecutionResult{
		Status: AutomationActionSucceeded,
		Result: AutomationActionResult{},
	}
}

func workbenchRecruitingForwardPolicyResolver(
	evaluator *ServerAutomationRiskEvaluator,
	_ AutomationPolicyInput,
	decoded WorkbenchDecodedToolRequest,
) (*AutomationRecipient, error) {
	if evaluator == nil || evaluator.crm == nil {
		return nil, NewAutomationValidationError("recipient", "CRM store is unavailable")
	}
	arguments, ok := decoded.Arguments.(*WorkbenchForwardEventArguments)
	if !ok {
		return nil, NewAutomationValidationError("recipient", "forward arguments are invalid")
	}
	rules := NewForwardRuleStore(evaluator.crm)
	_, _, err := workbenchRecruitingResolveForward(
		evaluator.crm,
		rules,
		arguments.EventID,
		arguments.RuleID,
		false,
	)
	return nil, err
}

func workbenchRecruitingResolveForward(
	crm *CRMStore,
	rules *ForwardRuleStore,
	eventID string,
	ruleID string,
	reserve bool,
) (MessageEvent, ForwardRule, error) {
	if crm == nil {
		return MessageEvent{}, ForwardRule{}, errors.New("CRM store is unavailable")
	}
	if rules == nil {
		return MessageEvent{}, ForwardRule{}, errors.New("forward rule store is unavailable")
	}
	event, ok := automationFindMessageEvent(crm.Snapshot(), eventID)
	if !ok || event.Source != crmMessageSource {
		return MessageEvent{}, ForwardRule{}, NewAutomationValidationError("event_id", "persisted inbound event is unavailable")
	}
	record := forwardRecordFromMessageEvent(event)
	rule, err := rules.MatchRule(ruleID, &record, time.Now().UTC(), reserve)
	if err != nil {
		return MessageEvent{}, ForwardRule{}, err
	}
	if !workbenchRecruitingSafeForwardRule(rule) {
		return MessageEvent{}, ForwardRule{}, errWorkbenchRecruitingUnsafeData
	}
	return event, rule, nil
}

func workbenchRecruitingForwardReplay(
	store *AutomationStore,
	parentID string,
	eventID string,
	ruleID string,
	payload json.RawMessage,
) *WorkbenchToolResult {
	bridge, ok := workbenchRecruitingFindBridge(store.Snapshot(), parentID, workbenchRecruitingBridgeForward)
	if !ok {
		return nil
	}
	if !jsonEqual(bridge.Payload, payload) {
		result := NewWorkbenchToolFailure(
			WorkbenchOperationForwardingSendEvent,
			WorkbenchErrorConflict,
			"approved forward conflicts with an existing execution",
			false,
		)
		return &result
	}
	result := workbenchRecruitingForwardBridgeResult(bridge, eventID, ruleID)
	return &result
}

func workbenchRecruitingForwardBridgeResult(
	bridge AutomationAction,
	eventID string,
	ruleID string,
) WorkbenchToolResult {
	operation := WorkbenchOperationForwardingSendEvent
	if bridge.Status == AutomationActionApproved || bridge.Status == AutomationActionExecuting {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorConflict, "approved forward is already executing", false)
	}
	if bridge.Status != AutomationActionSucceeded {
		code := workbenchRecruitingExecutionCode(bridge)
		return NewWorkbenchToolFailure(
			operation,
			code,
			"configured forward failed",
			bridge.Retryable || code == WorkbenchErrorUnavailable,
		)
	}
	return NewWorkbenchToolSuccess(operation, workbenchRecruitingForwardSendResult{
		EventID: eventID, RuleID: ruleID, Forwarded: true,
	}, nil)
}
