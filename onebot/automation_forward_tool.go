package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const AutomationAuditEventForwardDispatched = "forward.dispatched"

func (e *defaultAutomationCommandExecutor) executeForwardEvent(
	ctx context.Context,
	action AutomationAction,
	args ForwardEventArgs,
) AutomationExecutionResult {
	state := e.crm.Snapshot()
	event, ok := automationFindMessageEvent(state, args.EventID)
	if !ok || event.Source != crmMessageSource {
		return automationExecutionFailure("forward_event_unavailable", "persisted inbound event is unavailable", false)
	}
	record := forwardRecordFromMessageEvent(event)
	sink := strings.TrimSpace(config.Sink)
	if _, _, err := parseForwardSink(sink); err != nil {
		return automationExecutionFailure("forward_sink_unavailable", err.Error(), true)
	}
	rule, err := forwardRuleStore.MatchRuleAndReserve(args.RuleID, &record, time.Now().UTC())
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
	payload, err := json.Marshal(record)
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
		Reason:  args.Reason,
	})
	if err != nil {
		return automationExecutionFailure("forward_audit_failed", err.Error(), false)
	}
	err = e.automation.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: AutomationAuditEventForwardDispatched,
			RunID:     action.RunID,
			ActionID:  action.ID,
			ActorType: "system",
			Data:      sanitizeAutomationJSON(auditData),
			CreatedAt: time.Now().UTC(),
		})
		return err
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

func forwardRecordFromMessageEvent(event MessageEvent) ForwardRecord {
	return ForwardRecord{
		T:          event.Timestamp,
		Chat:       string(event.ChatType),
		Dir:        "in",
		Peer:       event.PeerID,
		Author:     event.AuthorID,
		GroupID:    event.GroupID,
		Text:       event.Text,
		Suspicious: event.Suspicious,
		Kinds:      append([]string{}, event.Kinds...),
		Mid:        event.Mid,
	}
}
