package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func validateRecruitingRouteTools(field string, event *MessageEvent) error {
	if event.RecruitingAllowedTools == nil || len(event.RecruitingAllowedTools) == 0 {
		return NewCRMValidationError(field+".recruiting_allowed_tools", "must not be empty")
	}
	if len(event.RecruitingAllowedTools) > 32 {
		return NewCRMValidationError(field+".recruiting_allowed_tools", "contains too many tools")
	}
	allowed := make(map[AutomationToolName]struct{})
	switch event.RecruitingConversationKind {
	case automationConversationWeChatPrivate:
		for _, tool := range recruitingPrivateAutomationTools() {
			allowed[tool] = struct{}{}
		}
	case automationConversationWeChatSourceGroup:
		allowed[AutomationToolMessageClassify] = struct{}{}
		allowed[AutomationToolJobCreate] = struct{}{}
	case automationConversationWeChatGroupNoise:
		allowed[AutomationToolMessageClassify] = struct{}{}
	default:
		return NewCRMValidationError(field+".recruiting_conversation_kind", "is invalid")
	}
	seen := make(map[AutomationToolName]struct{}, len(event.RecruitingAllowedTools))
	hasClassify := false
	for i, tool := range event.RecruitingAllowedTools {
		itemField := fmt.Sprintf("%s.recruiting_allowed_tools[%d]", field, i)
		if !IsValidAutomationToolName(tool) {
			return NewCRMValidationError(itemField, "is invalid")
		}
		if _, exists := seen[tool]; exists {
			return NewCRMValidationError(itemField, "duplicates an earlier tool")
		}
		if _, permitted := allowed[tool]; !permitted {
			return NewCRMValidationError(itemField, "is not allowed for the frozen conversation kind")
		}
		seen[tool] = struct{}{}
		hasClassify = hasClassify || tool == AutomationToolMessageClassify
	}
	if !hasClassify {
		return NewCRMValidationError(
			field+".recruiting_allowed_tools",
			"must include message.classify",
		)
	}
	return nil
}

func defaultMessageDisposition(chatType ChatType) MessageDisposition {
	if chatType == ChatTypePrivate {
		return MessageDispositionPrivateCandidate
	}
	return MessageDispositionGroupNoise
}

func validateForwardRule(field string, rule *ForwardRule) error {
	if err := validateStoredCRMString(field+".group_id", rule.GroupID, 256, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMStringList(field+".keywords", rule.Keywords, 32, 120); err != nil {
		return err
	}
	if rule.CooldownSeconds < 0 || rule.CooldownSeconds > 86400 {
		return NewCRMValidationError(field+".cooldown_seconds", "must be between 0 and 86400")
	}
	return nil
}

func validateVersionedTimes(field string, version uint64, createdAt, updatedAt time.Time) error {
	if err := validateCRMVersion(field+".version", version); err != nil {
		return err
	}
	if err := validateCRMTime(field+".created_at", createdAt); err != nil {
		return err
	}
	if err := validateCRMTime(field+".updated_at", updatedAt); err != nil {
		return err
	}
	if updatedAt.Before(createdAt) {
		return NewCRMValidationError(field+".updated_at", "must not precede created_at")
	}
	return nil
}

func validateCRMOptionalTime(field string, value *time.Time) error {
	if value == nil {
		return nil
	}
	return validateCRMTime(field, *value)
}

func isValidCRMCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for _, character := range currency {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}

func normalizeCRMUpdateMeta(meta CRMUpdateMeta) (CRMUpdateMeta, error) {
	var err error
	if strings.TrimSpace(meta.Actor) == "" {
		meta.Actor = "system"
	}
	if strings.TrimSpace(meta.Operation) == "" {
		meta.Operation = "update"
	}
	if meta.Actor, err = normalizeCRMString("actor", meta.Actor, 80, true, false); err != nil {
		return CRMUpdateMeta{}, err
	}
	if meta.Operation, err = normalizeCRMString("operation", meta.Operation, 120, true, false); err != nil {
		return CRMUpdateMeta{}, err
	}
	if meta.EntityType, err = normalizeCRMString("entity_type", meta.EntityType, 80, false, false); err != nil {
		return CRMUpdateMeta{}, err
	}
	if meta.EntityType != "" && !isValidCRMAuditEntityType(meta.EntityType) {
		return CRMUpdateMeta{}, NewCRMValidationError("entity_type", "is invalid")
	}
	if meta.EntityID, err = normalizeCRMString("entity_id", meta.EntityID, 80, false, false); err != nil {
		return CRMUpdateMeta{}, err
	}
	if meta.EntityID != "" && meta.EntityType == "" {
		return CRMUpdateMeta{}, NewCRMValidationError("entity_type", "is required when entity_id is set")
	}
	if meta.SourceEventID, err = normalizeCRMString("source_event_id", meta.SourceEventID, 80, false, false); err != nil {
		return CRMUpdateMeta{}, err
	}
	return meta, nil
}

func isValidCRMAuditEntityType(entityType string) bool {
	switch entityType {
	case "candidate",
		"job",
		"demand_revision",
		"lead",
		"lead_stage_event",
		"onboarding_case",
		"service_request",
		"fund_callback",
		"fund_callback_ledger",
		"operation_batch",
		"service_queue",
		"recruiting_source_group",
		"job_provenance",
		"task",
		"note",
		"message_event",
		"forwarding_rule",
		"audit_event":
		return true
	default:
		return false
	}
}

func crmAuditEntityFieldMatches(entityType, entityField string) bool {
	prefixes := map[string]string{
		"candidate":               "candidates[",
		"job":                     "jobs[",
		"demand_revision":         "demand_revisions[",
		"lead":                    "leads[",
		"lead_stage_event":        "lead_stage_events[",
		"onboarding_case":         "onboarding_cases[",
		"service_request":         "service_requests[",
		"fund_callback":           "fund_callbacks[",
		"fund_callback_ledger":    "fund_callback_ledger[",
		"operation_batch":         "operation_batches[",
		"service_queue":           "service_queues[",
		"recruiting_source_group": "recruiting_project.source_groups[",
		"job_provenance":          "job_provenance[",
		"task":                    "tasks[",
		"note":                    "notes[",
		"message_event":           "message_events[",
		"forwarding_rule":         "forwarding_rules[",
		"audit_event":             "audit_events[",
	}
	return strings.HasPrefix(entityField, prefixes[entityType])
}

func validateStoredCRMString(field, value string, maxRunes int, required, multiline bool) error {
	normalized, err := normalizeCRMString(field, value, maxRunes, required, multiline)
	if err != nil {
		return err
	}
	if value != normalized {
		return NewCRMValidationError(field, "must be trimmed")
	}
	return nil
}

func validateStoredCRMStringList(field string, values []string, maxCount, maxRunes int) error {
	if values == nil {
		return NewCRMValidationError(field, "must not be null")
	}
	normalized, err := normalizeCRMStringList(field, values, maxCount, maxRunes)
	if err != nil {
		return err
	}
	for i := range values {
		if values[i] != normalized[i] {
			return NewCRMValidationError(indexedField(field, i), "must be trimmed")
		}
	}
	return nil
}

func indexedField(field string, index int) string {
	return field + "[" + strconv.Itoa(index) + "]"
}

func totalCRMEntities(state *CRMState) int {
	return len(state.RecruitingProject.SourceGroups) +
		len(state.Candidates) +
		len(state.Jobs) +
		len(state.Leads) +
		len(state.LeadStageEvents) +
		len(state.OnboardingCases) +
		len(state.ServiceRequests) +
		len(state.FundCallbacks) +
		len(state.DemandRevisions) +
		len(state.JobProvenance) +
		len(state.OperationBatches) +
		len(state.ServiceQueues) +
		len(state.FundCallbackLedger) +
		len(state.AuditEvents) +
		len(state.Tasks) +
		len(state.Notes) +
		len(state.MessageEvents) +
		len(state.ForwardingRules)
}
