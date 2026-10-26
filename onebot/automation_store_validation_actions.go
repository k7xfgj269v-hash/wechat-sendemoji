package main

import (
	"bytes"
	"fmt"
)

func validateAutomationAction(field string, action *AutomationAction, requireTrace bool) error {
	if err := validateStoredAutomationString(field+".run_id", action.RunID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".provider_call_id", action.ProviderCallID, 120, true, false); err != nil {
		return err
	}
	if !IsValidAutomationToolName(action.Tool) {
		return NewAutomationValidationError(field+".tool", "is invalid")
	}
	if IsWorkbenchToolName(action.Tool) {
		decoded, err := DecodeWorkbenchToolRequest(action.Tool, action.Payload)
		if err != nil {
			return NewAutomationValidationError(field+".payload", "does not match the persisted workbench schema")
		}
		if action.Operation != decoded.Request.Operation {
			return NewAutomationValidationError(field+".operation", "does not match the workbench request")
		}
		if !bytes.Equal(action.Payload, decoded.CanonicalRequest) {
			return NewAutomationValidationError(field+".payload", "must contain the canonical workbench request")
		}
	} else if action.Operation != "" {
		return NewAutomationValidationError(field+".operation", "must be absent for legacy tools")
	}
	if !IsValidAutomationActionStatus(action.Status) {
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if !IsValidAutomationRisk(action.Risk) {
		return NewAutomationValidationError(field+".risk", "is invalid")
	}
	if !IsValidAutomationPolicyDecision(action.PolicyDecision) {
		return NewAutomationValidationError(field+".policy_decision", "is invalid")
	}
	if action.PolicyVersion == 0 {
		return NewAutomationValidationError(field+".policy_version", "must be greater than zero")
	}
	if err := validateStoredAutomationStringList(field+".risk_reasons", action.RiskReasons, 32, 500); err != nil {
		return err
	}
	if err := validateAutomationConfidence(field+".confidence", action.Confidence); err != nil {
		return err
	}
	if action.Evidence == nil {
		return NewAutomationValidationError(field+".evidence", "must not be null")
	}
	if len(action.Evidence) > AutomationMaxEvidencePerAction {
		return NewAutomationValidationError(
			field+".evidence",
			fmt.Sprintf("must contain at most %d values", AutomationMaxEvidencePerAction),
		)
	}
	for i := range action.Evidence {
		if err := validateAutomationEvidence(automationIndexedField(field+".evidence", i), &action.Evidence[i]); err != nil {
			return err
		}
	}
	if err := validateAutomationSourceMessage(field+".source_message", &action.SourceMessage); err != nil {
		return err
	}
	if action.Preconditions == nil {
		return NewAutomationValidationError(field+".preconditions", "must not be null")
	}
	if len(action.Preconditions) > 64 {
		return NewAutomationValidationError(field+".preconditions", "must contain at most 64 values")
	}
	preconditions := make(map[string]struct{}, len(action.Preconditions))
	for i := range action.Preconditions {
		itemField := automationIndexedField(field+".preconditions", i)
		if err := validateAutomationPrecondition(itemField, &action.Preconditions[i]); err != nil {
			return err
		}
		key := action.Preconditions[i].EntityType + "\x00" + action.Preconditions[i].EntityID
		if _, exists := preconditions[key]; exists {
			return NewAutomationValidationError(itemField, "duplicates an earlier precondition")
		}
		preconditions[key] = struct{}{}
	}
	if err := validateStoredAutomationStringList(
		field+".depends_on_action_ids",
		action.DependsOnActionIDs,
		AutomationMaxActionsPerRun,
		80,
	); err != nil {
		return err
	}
	if action.Recipient != nil {
		if err := validateStoredAutomationString(field+".recipient.audience_id", action.Recipient.AudienceID, 80, false, false); err != nil {
			return err
		}
		if !IsValidChatType(action.Recipient.ChatType) {
			return NewAutomationValidationError(field+".recipient.chat_type", "is invalid")
		}
		if err := validateStoredAutomationString(field+".recipient.peer_id", action.Recipient.PeerID, 256, true, false); err != nil {
			return err
		}
	}
	if err := validateAutomationRawJSON(field+".payload", action.Payload, AutomationMaxActionPayloadBytes, true); err != nil {
		return err
	}
	if _, err := DecodeAutomationToolArguments(action.Tool, action.Payload); err != nil {
		return NewAutomationValidationError(field+".payload", "does not match the persisted tool schema")
	}
	if err := validateAutomationRawJSON(field+".result", action.Result, AutomationMaxActionPayloadBytes, false); err != nil {
		return err
	}
	if requireTrace {
		if err := validateStoredAutomationString(field+".result_id", action.ResultID, 80, false, false); err != nil {
			return err
		}
	}
	if err := validateStoredAutomationString(field+".error_code", action.ErrorCode, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".error_message", action.ErrorMessage, 4000, false, true); err != nil {
		return err
	}
	if action.Attempt == 0 {
		return NewAutomationValidationError(field+".attempt", "must be greater than zero")
	}
	for name, value := range map[string]string{
		"retry_of_action_id":    action.RetryOfActionID,
		"replaces_action_id":    action.ReplacesActionID,
		"replaced_by_action_id": action.ReplacedByActionID,
	} {
		if err := validateStoredAutomationString(field+"."+name, value, 80, false, false); err != nil {
			return err
		}
	}
	if action.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if err := validateAutomationStoreTime(field+".created_at", action.CreatedAt); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".updated_at", action.UpdatedAt); err != nil {
		return err
	}
	if action.UpdatedAt.Before(action.CreatedAt) {
		return NewAutomationValidationError(field+".updated_at", "must not precede created_at")
	}
	return validateAutomationActionRecipient(field, action)
}

func validateAutomationActionRecipient(field string, action *AutomationAction) error {
	switch action.Tool {
	case AutomationToolReplyPrivateText:
		if action.Recipient == nil {
			if action.SourceMessage.ChatType == ChatTypeGroup &&
				action.Status == AutomationActionDenied &&
				action.Risk == AutomationRiskR4 &&
				action.PolicyDecision == AutomationDecisionDeny {
				return nil
			}
			return NewAutomationValidationError(field+".recipient", "is required for a private reply")
		}
		if action.Recipient.AudienceID != "" || action.Recipient.AudienceVersion != 0 {
			return NewAutomationValidationError(field+".recipient.audience_id", "must be absent for an original private reply")
		}
		if action.SourceMessage.ChatType != ChatTypePrivate ||
			action.Recipient.ChatType != ChatTypePrivate ||
			action.Recipient.PeerID != action.SourceMessage.PeerID {
			return NewAutomationValidationError(field+".recipient.peer_id", "must match the source private peer")
		}
		return nil

	case AutomationToolReplyAudienceText:
		if action.Recipient == nil {
			return NewAutomationValidationError(field+".recipient", "is required for an audience reply")
		}
		if action.Recipient.AudienceID == "" || action.Recipient.AudienceVersion == 0 {
			return NewAutomationValidationError(field+".recipient.audience_id", "requires a server-resolved audience")
		}
		arguments, err := DecodeAutomationToolArguments(action.Tool, action.Payload)
		if err != nil {
			return NewAutomationValidationError(field+".payload", "does not match the audience reply schema")
		}
		if arguments.(ReplyAudienceTextArgs).AudienceID != action.Recipient.AudienceID {
			return NewAutomationValidationError(field+".recipient.audience_id", "does not match the requested audience")
		}
		return nil

	case AutomationToolWorkbenchAllowOnce:
		switch action.Operation {
		case WorkbenchOperationMessagesSendPrivate, WorkbenchOperationMessagesSendGroup:
			if action.Recipient == nil {
				return NewAutomationValidationError(field+".recipient", "is required for a controlled send")
			}
			if action.Recipient.AudienceID == "" || action.Recipient.AudienceVersion == 0 {
				return NewAutomationValidationError(field+".recipient.audience_id", "must bind a persisted message event")
			}
			decoded, err := DecodeWorkbenchToolRequest(action.Tool, action.Payload)
			if err != nil {
				return NewAutomationValidationError(field+".payload", "does not match the controlled send schema")
			}
			arguments := decoded.Arguments.(*WorkbenchControlledSendArguments)
			if arguments.EventID != action.Recipient.AudienceID {
				return NewAutomationValidationError(field+".recipient.audience_id", "does not match the requested event")
			}
			expectedChatType := ChatTypePrivate
			if action.Operation == WorkbenchOperationMessagesSendGroup {
				expectedChatType = ChatTypeGroup
			}
			if action.Recipient.ChatType != expectedChatType {
				return NewAutomationValidationError(field+".recipient.chat_type", "does not match the send operation")
			}
			return nil
		default:
			if action.Recipient != nil {
				return NewAutomationValidationError(field+".recipient", "must be absent for this workbench operation")
			}
			return nil
		}

	default:
		if action.Recipient != nil {
			return NewAutomationValidationError(field+".recipient", "must be absent for non-reply tools")
		}
		return nil
	}
}

func validateAutomationEvidence(field string, evidence *AutomationEvidence) error {
	if err := validateStoredAutomationString(field+".source", evidence.Source, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".entity_id", evidence.EntityID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".field", evidence.Field, 120, false, false); err != nil {
		return err
	}
	return validateStoredAutomationString(field+".excerpt", evidence.Excerpt, 500, true, true)
}

func validateAutomationSourceMessage(field string, source *AutomationSourceMessageRef) error {
	if err := validateStoredAutomationString(field+".event_id", source.EventID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".message_key", source.MessageKey, 1024, true, false); err != nil {
		return err
	}
	if source.ExpectedVersion == 0 {
		return NewAutomationValidationError(field+".expected_version", "must be greater than zero")
	}
	if !IsValidChatType(source.ChatType) {
		return NewAutomationValidationError(field+".chat_type", "is invalid")
	}
	return validateStoredAutomationString(field+".peer_id", source.PeerID, 256, true, false)
}

func validateAutomationPrecondition(field string, precondition *AutomationEntityPrecondition) error {
	if !isValidAutomationEntityType(precondition.EntityType) {
		return NewAutomationValidationError(field+".entity_type", "is invalid")
	}
	if err := validateAutomationCRMEntityID(field+".entity_id", precondition.EntityID, precondition.EntityType); err != nil {
		return err
	}
	if precondition.ExpectedVersion == 0 {
		return NewAutomationValidationError(field+".expected_version", "must be greater than zero")
	}
	return nil
}

func validateAutomationApproval(field string, approval *AutomationApprovalRecord) error {
	if err := validateStoredAutomationString(field+".action_id", approval.ActionID, 80, true, false); err != nil {
		return err
	}
	if approval.ActionVersion == 0 {
		return NewAutomationValidationError(field+".action_version", "must be greater than zero")
	}
	if err := validateStoredAutomationString(field+".decision", approval.Decision, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".operator_id", approval.OperatorID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".reason", approval.Reason, 4000, false, true); err != nil {
		return err
	}
	if err := validateStoredAutomationString(
		field+".replacement_action_id",
		approval.ReplacementActionID,
		80,
		false,
		false,
	); err != nil {
		return err
	}
	return validateAutomationStoreTime(field+".created_at", approval.CreatedAt)
}

func validateAutomationOutboxEntry(field string, entry *AutomationOutboxEntry) error {
	if err := validateStoredAutomationString(field+".action_id", entry.ActionID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".idempotency_key", entry.IdempotencyKey, 512, true, false); err != nil {
		return err
	}
	if entry.ChatType != "" && !IsValidChatType(entry.ChatType) {
		return NewAutomationValidationError(field+".chat_type", "is invalid")
	}
	if err := validateStoredAutomationString(field+".peer_id", entry.PeerID, 256, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".text", entry.Text, 4000, true, true); err != nil {
		return err
	}
	if !IsValidAutomationOutboxStatus(entry.Status) {
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if err := validateStoredAutomationString(field+".error_code", entry.ErrorCode, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".error_message", entry.ErrorMessage, 4000, false, true); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".created_at", entry.CreatedAt); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".updated_at", entry.UpdatedAt); err != nil {
		return err
	}
	if entry.UpdatedAt.Before(entry.CreatedAt) {
		return NewAutomationValidationError(field+".updated_at", "must not precede created_at")
	}
	if err := validateOptionalAutomationTime(field+".dispatch_started_at", entry.DispatchStartedAt); err != nil {
		return err
	}
	if err := validateOptionalAutomationTime(field+".completed_at", entry.CompletedAt); err != nil {
		return err
	}
	if entry.DispatchStartedAt != nil {
		if entry.DispatchStartedAt.Before(entry.CreatedAt) || entry.DispatchStartedAt.After(entry.UpdatedAt) {
			return NewAutomationValidationError(field+".dispatch_started_at", "must be between created_at and updated_at")
		}
	}
	if entry.CompletedAt != nil {
		floor := entry.CreatedAt
		if entry.DispatchStartedAt != nil {
			floor = *entry.DispatchStartedAt
		}
		if entry.CompletedAt.Before(floor) || entry.CompletedAt.After(entry.UpdatedAt) {
			return NewAutomationValidationError(field+".completed_at", "must be between dispatch start and updated_at")
		}
	}
	switch entry.Status {
	case AutomationOutboxPending:
		if entry.Attempt != 0 || entry.DispatchAttempted || entry.DispatchStartedAt != nil || entry.CompletedAt != nil {
			return NewAutomationValidationError(field+".status", "pending entries require attempt zero and no dispatch state")
		}
	case AutomationOutboxDispatching:
		if entry.DispatchStartedAt == nil || entry.CompletedAt != nil {
			return NewAutomationValidationError(field+".status", "dispatching entries require only a dispatch start time")
		}
		if entry.Attempt == 0 {
			return NewAutomationValidationError(field+".attempt", "must be greater than zero after claim")
		}
	case AutomationOutboxSent, AutomationOutboxUnknown:
		if !entry.DispatchAttempted || entry.DispatchStartedAt == nil || entry.CompletedAt == nil {
			return NewAutomationValidationError(field+".status", "terminal dispatch entries require completed dispatch state")
		}
		if entry.Attempt == 0 {
			return NewAutomationValidationError(field+".attempt", "must be greater than zero after claim")
		}
	case AutomationOutboxFailed:
		if entry.DispatchAttempted || entry.CompletedAt == nil {
			return NewAutomationValidationError(field+".status", "failed entries must be definite pre-dispatch failures")
		}
		if entry.Attempt == 0 {
			return NewAutomationValidationError(field+".attempt", "must be greater than zero after claim")
		}
	}
	return nil
}

func validateAutomationAuditRecord(field string, record *AutomationAuditRecord) error {
	if err := validateStoredAutomationString(field+".event_type", record.EventType, 120, true, false); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"run_id":    record.RunID,
		"action_id": record.ActionID,
		"outbox_id": record.OutboxID,
	} {
		if err := validateStoredAutomationString(field+"."+name, value, 80, false, false); err != nil {
			return err
		}
	}
	if err := validateStoredAutomationString(field+".actor_type", record.ActorType, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".actor_id", record.ActorID, 80, false, false); err != nil {
		return err
	}
	if err := validateAutomationRawJSON(field+".data", record.Data, AutomationMaxActionPayloadBytes, true); err != nil {
		return err
	}
	return validateAutomationStoreTime(field+".created_at", record.CreatedAt)
}

func validateAutomationPermissionRequest(field string, request *AutomationPermissionRequest) error {
	for name, value := range map[string]string{
		"run_id":      request.RunID,
		"action_id":   request.ActionID,
		"operator_id": request.OperatorID,
		"rule_id":     request.RuleID,
	} {
		required := name == "run_id" || name == "action_id"
		if err := validateStoredAutomationString(field+"."+name, value, 80, required, false); err != nil {
			return err
		}
	}
	if !IsValidAutomationToolName(request.Tool) || !IsValidAutomationRisk(request.Risk) {
		return NewAutomationValidationError(field, "contains an invalid tool or risk")
	}
	if IsWorkbenchToolName(request.Tool) {
		if !IsValidWorkbenchOperation(request.Tool, request.Operation) {
			return NewAutomationValidationError(field+".operation", "is invalid for the workbench tool")
		}
	} else if request.Operation != "" {
		return NewAutomationValidationError(field+".operation", "must be absent for legacy tools")
	}
	switch request.Status {
	case AutomationPermissionStatusPending:
		if request.Decision != "" || request.OperatorID != "" || request.RuleID != "" {
			return NewAutomationValidationError(field+".status", "pending requests must not contain a decision")
		}
	case AutomationPermissionStatusAllowed, AutomationPermissionStatusDenied:
		if !IsValidAutomationPermissionDecision(request.Decision) || request.OperatorID == "" {
			return NewAutomationValidationError(field+".decision", "is required for a decided request")
		}
		if request.Tool == AutomationToolWorkbenchAllowOnce &&
			request.Decision != AutomationPermissionAllowOnce &&
			request.Decision != AutomationPermissionDenyOnce &&
			(request.Decision != AutomationPermissionAllowPersistent ||
				!isPersistentHighRiskSendOperation(request.Operation)) {
			return NewAutomationValidationError(
				field+".decision",
				"must be allow_once, deny_once, or allow_persistent for a high-risk outbound operation",
			)
		}
	case AutomationPermissionStatusExpired:
	default:
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if request.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if err := validateStoredAutomationString(field+".reason", request.Reason, 1000, false, true); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".created_at", request.CreatedAt); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".updated_at", request.UpdatedAt); err != nil {
		return err
	}
	if request.UpdatedAt.Before(request.CreatedAt) {
		return NewAutomationValidationError(field+".updated_at", "must not precede created_at")
	}
	return nil
}

func validateAutomationPermissionRule(field string, rule *AutomationPermissionRule) error {
	if rule.Effect != AutomationPermissionEffectAllow && rule.Effect != AutomationPermissionEffectDeny {
		return NewAutomationValidationError(field+".effect", "is invalid")
	}
	if !IsValidAutomationToolName(rule.Tool) {
		return NewAutomationValidationError(field+".tool", "is invalid")
	}
	if rule.Tool == AutomationToolWorkbenchAllowOnce &&
		!isPersistentHighRiskSendOperation(rule.Operation) {
		return NewAutomationValidationError(
			field+".operation",
			"reusable rules are limited to high-risk outbound operations",
		)
	}
	if IsWorkbenchToolName(rule.Tool) {
		if !IsValidWorkbenchOperation(rule.Tool, rule.Operation) {
			return NewAutomationValidationError(field+".operation", "is invalid for the workbench tool")
		}
	} else if rule.Operation != "" {
		return NewAutomationValidationError(field+".operation", "must be absent for legacy tools")
	}
	if err := validateStoredAutomationString(field+".scope_key", rule.ScopeKey, 256, true, false); err != nil {
		return err
	}
	if err := validateAutomationRawJSON(field+".scope", rule.Scope, AutomationMaxActionPayloadBytes, true); err != nil {
		return err
	}
	if trimmed := bytes.TrimSpace(rule.Scope); len(trimmed) < 2 || trimmed[0] != '{' {
		return NewAutomationValidationError(field+".scope", "must be a JSON object")
	}
	if rule.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if err := validateStoredAutomationString(field+".created_by", rule.CreatedBy, 80, true, false); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".created_at", rule.CreatedAt); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".updated_at", rule.UpdatedAt); err != nil {
		return err
	}
	if rule.UpdatedAt.Before(rule.CreatedAt) {
		return NewAutomationValidationError(field+".updated_at", "must not precede created_at")
	}
	return nil
}
