package main

import (
	"fmt"
)

func ValidateAutomationState(state *AutomationState) error {
	if state == nil {
		return NewAutomationValidationError("state", "is required")
	}
	if state.SchemaVersion != AutomationSnapshotSchemaVersion {
		return NewAutomationValidationError(
			"schema_version",
			fmt.Sprintf("unsupported schema version %d", state.SchemaVersion),
		)
	}
	return validateAutomationStateContents(state, true)
}

// validateAutomationStateLight checks the structural invariants that retention
// pruning could conceivably disturb, without re-running the full per-entity
// validation that already passed on the same state immediately before pruning.
// Retention only removes whole unreferenced subtrees while keeping every
// referrer of a kept entity, so the cross-entity references that remain valid
// before pruning stay valid after it; the one exception is the action
// dependency graph, which can span runs, so it is re-checked explicitly.
func validateAutomationStateLight(state *AutomationState) error {
	if state == nil {
		return NewAutomationValidationError("state", "is required")
	}
	if state.SchemaVersion != AutomationSnapshotSchemaVersion {
		return NewAutomationValidationError(
			"schema_version",
			fmt.Sprintf("unsupported schema version %d", state.SchemaVersion),
		)
	}
	if err := validateAutomationStateCounts(state, true); err != nil {
		return err
	}
	if err := validateAutomationCounterBounds(state.Counters, true); err != nil {
		return err
	}
	return validateAutomationDependencyGraph(state.Actions)
}

func validateAutomationStateV1(state *AutomationState) error {
	if state == nil || state.SchemaVersion != 1 {
		return NewAutomationValidationError("schema_version", "must be version 1")
	}
	return validateAutomationStateContents(state, false)
}

func validateAutomationStateContents(state *AutomationState, requireTrace bool) error {
	if err := validateAutomationStateCollections(state, requireTrace); err != nil {
		return err
	}
	if err := validateAutomationStateCounts(state, requireTrace); err != nil {
		return err
	}
	if err := validateAutomationCounterBounds(state.Counters, requireTrace); err != nil {
		return err
	}
	if err := validateAutomationPolicy("policy", &state.Policy); err != nil {
		return err
	}

	allIDs := make(map[string]string, totalAutomationEntities(state))
	runs := make(map[string]*AutomationRun, len(state.Runs))
	actions := make(map[string]*AutomationAction, len(state.Actions))
	approvals := make(map[string]*AutomationApprovalRecord, len(state.Approvals))
	outbox := make(map[string]*AutomationOutboxEntry, len(state.Outbox))
	audits := make(map[string]*AutomationAuditRecord, len(state.PendingAudit))
	permissionRequests := make(map[string]*AutomationPermissionRequest, len(state.PermissionRequests))
	permissionRules := make(map[string]*AutomationPermissionRule, len(state.PermissionRules))
	runIdempotency := make(map[string]string, len(state.Runs))
	outboxIdempotency := make(map[string]string, len(state.Outbox))
	providerCalls := make(map[string]string, len(state.Actions))

	for i := range state.Runs {
		run := &state.Runs[i]
		field := automationIndexedField("runs", i)
		if err := validateAutomationStoredID(field+".id", run.ID, "airun", state.Counters.Run, allIDs); err != nil {
			return err
		}
		if err := validateAutomationRun(field, run, requireTrace); err != nil {
			return err
		}
		if previous, exists := runIdempotency[run.IdempotencyKey]; exists {
			return NewAutomationValidationError(field+".idempotency_key", "duplicates run "+previous)
		}
		runIdempotency[run.IdempotencyKey] = run.ID
		runs[run.ID] = run
	}

	for i := range state.Actions {
		action := &state.Actions[i]
		field := automationIndexedField("actions", i)
		if err := validateAutomationStoredID(field+".id", action.ID, "aiact", state.Counters.Action, allIDs); err != nil {
			return err
		}
		if err := validateAutomationAction(field, action, requireTrace); err != nil {
			return err
		}
		callKey := action.RunID + "\x00" + action.ProviderCallID
		if previous, exists := providerCalls[callKey]; exists {
			return NewAutomationValidationError(field+".provider_call_id", "duplicates action "+previous)
		}
		providerCalls[callKey] = action.ID
		actions[action.ID] = action
	}

	for i := range state.Approvals {
		approval := &state.Approvals[i]
		field := automationIndexedField("approvals", i)
		if err := validateAutomationStoredID(
			field+".id",
			approval.ID,
			"aiapproval",
			state.Counters.Approval,
			allIDs,
		); err != nil {
			return err
		}
		if err := validateAutomationApproval(field, approval); err != nil {
			return err
		}
		approvals[approval.ID] = approval
	}

	for i := range state.Outbox {
		entry := &state.Outbox[i]
		field := automationIndexedField("outbox", i)
		if err := validateAutomationStoredID(field+".id", entry.ID, "aiout", state.Counters.Outbox, allIDs); err != nil {
			return err
		}
		if err := validateAutomationOutboxEntry(field, entry); err != nil {
			return err
		}
		if previous, exists := outboxIdempotency[entry.IdempotencyKey]; exists {
			return NewAutomationValidationError(field+".idempotency_key", "duplicates outbox entry "+previous)
		}
		outboxIdempotency[entry.IdempotencyKey] = entry.ID
		outbox[entry.ID] = entry
	}

	for i := range state.PendingAudit {
		record := &state.PendingAudit[i]
		field := automationIndexedField("pending_audit", i)
		if err := validateAutomationStoredID(field+".id", record.ID, "aiaudit", state.Counters.Audit, allIDs); err != nil {
			return err
		}
		if err := validateAutomationAuditRecord(field, record); err != nil {
			return err
		}
		audits[record.ID] = record
	}
	for i := range state.PermissionRequests {
		request := &state.PermissionRequests[i]
		field := automationIndexedField("permission_requests", i)
		if err := validateAutomationStoredID(field+".id", request.ID, "aipermreq", state.Counters.PermissionRequest, allIDs); err != nil {
			return err
		}
		if err := validateAutomationPermissionRequest(field, request); err != nil {
			return err
		}
		permissionRequests[request.ID] = request
	}
	for i := range state.PermissionRules {
		rule := &state.PermissionRules[i]
		field := automationIndexedField("permission_rules", i)
		if err := validateAutomationStoredID(field+".id", rule.ID, "aipermrule", state.Counters.PermissionRule, allIDs); err != nil {
			return err
		}
		if err := validateAutomationPermissionRule(field, rule); err != nil {
			return err
		}
		permissionRules[rule.ID] = rule
	}

	if requireTrace {
		sessions, turns, err := validateAutomationTraceEntities(state, allIDs, runs, actions, permissionRequests)
		if err != nil {
			return err
		}
		if err := validateAutomationRunRelationships(state, runs, actions, sessions, turns, requireTrace); err != nil {
			return err
		}
	} else {
		if err := validateAutomationRunRelationships(state, runs, actions, nil, nil, requireTrace); err != nil {
			return err
		}
	}
	if err := validateAutomationActionRelationships(state, runs, actions); err != nil {
		return err
	}
	if err := validateAutomationApprovalRelationships(state, actions); err != nil {
		return err
	}
	if err := validateAutomationOutboxRelationships(state, actions); err != nil {
		return err
	}
	if err := validateAutomationAuditRelationships(state, runs, actions, outbox); err != nil {
		return err
	}
	if err := validateAutomationIdempotency(state); err != nil {
		return err
	}
	if err := validateAutomationDependencyGraph(state.Actions); err != nil {
		return err
	}
	for i := range state.PermissionRequests {
		request := &state.PermissionRequests[i]
		field := automationIndexedField("permission_requests", i)
		action, ok := actions[request.ActionID]
		if !ok || action.RunID != request.RunID || action.Tool != request.Tool ||
			action.Operation != request.Operation || action.Risk != request.Risk {
			return NewAutomationValidationError(field+".action_id", "does not match the referenced action")
		}
		if request.RuleID != "" {
			if _, ok := permissionRules[request.RuleID]; !ok {
				return NewAutomationValidationError(field+".rule_id", "references a missing permission rule")
			}
		}
	}

	_ = approvals
	_ = audits
	_ = permissionRequests
	return nil
}

func validateAutomationStateCollections(state *AutomationState, requireTrace bool) error {
	if requireTrace {
		switch {
		case state.Sessions == nil:
			return NewAutomationValidationError("sessions", "must not be null")
		case state.Turns == nil:
			return NewAutomationValidationError("turns", "must not be null")
		case state.TurnEvents == nil:
			return NewAutomationValidationError("turn_events", "must not be null")
		case state.Results == nil:
			return NewAutomationValidationError("results", "must not be null")
		}
	}
	switch {
	case state.Runs == nil:
		return NewAutomationValidationError("runs", "must not be null")
	case state.Actions == nil:
		return NewAutomationValidationError("actions", "must not be null")
	case state.Approvals == nil:
		return NewAutomationValidationError("approvals", "must not be null")
	case state.Outbox == nil:
		return NewAutomationValidationError("outbox", "must not be null")
	case state.Idempotency == nil:
		return NewAutomationValidationError("idempotency", "must not be null")
	case state.PendingAudit == nil:
		return NewAutomationValidationError("pending_audit", "must not be null")
	case state.PermissionRequests == nil:
		return NewAutomationValidationError("permission_requests", "must not be null")
	case state.PermissionRules == nil:
		return NewAutomationValidationError("permission_rules", "must not be null")
	default:
		return nil
	}
}

func validateAutomationStateCounts(state *AutomationState, requireTrace bool) error {
	values := []struct {
		field string
		count int
		limit int
	}{
		{"runs", len(state.Runs), automationMaxEntityCount},
		{"actions", len(state.Actions), automationMaxEntityCount},
		{"approvals", len(state.Approvals), automationMaxEntityCount},
		{"outbox", len(state.Outbox), automationMaxEntityCount},
		{"idempotency", len(state.Idempotency), automationMaxEntityCount * 2},
		{"pending_audit", len(state.PendingAudit), automationMaxAuditCount},
		{"permission_requests", len(state.PermissionRequests), automationMaxEntityCount},
		{"permission_rules", len(state.PermissionRules), automationMaxEntityCount},
	}
	if requireTrace {
		values = append(values,
			struct {
				field string
				count int
				limit int
			}{"sessions", len(state.Sessions), automationMaxEntityCount},
			struct {
				field string
				count int
				limit int
			}{"turns", len(state.Turns), automationMaxEntityCount},
			struct {
				field string
				count int
				limit int
			}{"turn_events", len(state.TurnEvents), automationMaxAuditCount},
			struct {
				field string
				count int
				limit int
			}{"results", len(state.Results), automationMaxEntityCount},
		)
	}
	for _, value := range values {
		if value.count > value.limit {
			return NewAutomationValidationError(value.field, "entity limit exceeded")
		}
	}
	return nil
}

func validateAutomationCounterBounds(counters AutomationCounters, requireTrace bool) error {
	values := []struct {
		field string
		value uint64
	}{
		{"run", counters.Run},
		{"action", counters.Action},
		{"approval", counters.Approval},
		{"outbox", counters.Outbox},
		{"audit", counters.Audit},
		{"permission_request", counters.PermissionRequest},
		{"permission_rule", counters.PermissionRule},
	}
	if requireTrace {
		values = append(values,
			struct {
				field string
				value uint64
			}{"session", counters.Session},
			struct {
				field string
				value uint64
			}{"turn", counters.Turn},
			struct {
				field string
				value uint64
			}{"turn_event", counters.TurnEvent},
			struct {
				field string
				value uint64
			}{"result", counters.Result},
		)
	}
	for _, value := range values {
		if value.value > automationMaxIDCounter {
			return NewAutomationValidationError("counters."+value.field, "exceeds the ID range")
		}
	}
	return nil
}

func validateAutomationPolicy(field string, policy *AutomationPolicy) error {
	if policy.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if !IsValidAutomationMode(policy.Mode) {
		return NewAutomationValidationError(field+".mode", "is invalid")
	}
	if err := validateAutomationConfidence(field+".r1_min_confidence", policy.R1MinConfidence); err != nil {
		return err
	}
	if err := validateAutomationConfidence(field+".r2_min_confidence", policy.R2MinConfidence); err != nil {
		return err
	}
	if policy.R1AllowedTools == nil {
		return NewAutomationValidationError(field+".r1_allowed_tools", "must not be null")
	}
	if len(policy.R1AllowedTools) > 32 {
		return NewAutomationValidationError(field+".r1_allowed_tools", "must contain at most 32 values")
	}
	seenTools := make(map[AutomationToolName]struct{}, len(policy.R1AllowedTools))
	for i, tool := range policy.R1AllowedTools {
		itemField := automationIndexedField(field+".r1_allowed_tools", i)
		if !IsValidAutomationToolName(tool) {
			return NewAutomationValidationError(itemField, "is invalid")
		}
		if _, exists := seenTools[tool]; exists {
			return NewAutomationValidationError(itemField, "duplicates an earlier tool")
		}
		seenTools[tool] = struct{}{}
	}
	if err := validateStoredAutomationStringList(field+".r2_allowed_intents", policy.R2AllowedIntents, 32, 80); err != nil {
		return err
	}
	for i, intent := range policy.R2AllowedIntents {
		if !IsValidAutomationReplyIntent(intent) {
			return NewAutomationValidationError(
				automationIndexedField(field+".r2_allowed_intents", i),
				"is invalid",
			)
		}
	}
	return validateAutomationStoreTime(field+".updated_at", policy.UpdatedAt)
}

func validateAutomationRun(field string, run *AutomationRun, requireTrace bool) error {
	if requireTrace {
		if err := validateStoredAutomationString(field+".session_id", run.SessionID, 80, true, false); err != nil {
			return err
		}
		if err := validateStoredAutomationString(field+".turn_id", run.TurnID, 80, true, false); err != nil {
			return err
		}
		if err := validateAutomationExternalID(field+".prompt_revision_id", run.PromptRevisionID); err != nil {
			return err
		}
	}
	if err := validateStoredAutomationString(field+".idempotency_key", run.IdempotencyKey, 512, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".engine_version", run.EngineVersion, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".provider_name", run.ProviderName, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(
		field+".source_message_event_id",
		run.SourceMessageEventID,
		80,
		true,
		false,
	); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".source_message_key", run.SourceMessageKey, 1024, true, false); err != nil {
		return err
	}
	if run.SourceMessageVersion == 0 {
		return NewAutomationValidationError(field+".source_message_version", "must be greater than zero")
	}
	if !IsValidAutomationRunStatus(run.Status) {
		return NewAutomationValidationError(field+".status", "is invalid")
	}
	if run.Version == 0 {
		return NewAutomationValidationError(field+".version", "must be greater than zero")
	}
	if run.Attempt == 0 {
		return NewAutomationValidationError(field+".attempt", "must be greater than zero")
	}
	if err := validateStoredAutomationString(field+".retry_of_run_id", run.RetryOfRunID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".failure_stage", run.FailureStage, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".failure_code", run.FailureCode, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredAutomationString(field+".failure_message", run.FailureMessage, 4000, false, true); err != nil {
		return err
	}
	if run.ProviderResponseSHA256 != "" {
		if len(run.ProviderResponseSHA256) != 64 || !isAutomationLowerHex(run.ProviderResponseSHA256) {
			return NewAutomationValidationError(field+".provider_response_sha256", "must be a lowercase SHA-256 digest")
		}
	}
	if err := validateStoredAutomationStringList(field+".action_ids", run.ActionIDs, AutomationMaxActionsPerRun, 80); err != nil {
		return err
	}
	if err := validateAutomationStoreTime(field+".created_at", run.CreatedAt); err != nil {
		return err
	}
	if err := validateOptionalAutomationTime(field+".started_at", run.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalAutomationTime(field+".completed_at", run.CompletedAt); err != nil {
		return err
	}
	if run.StartedAt != nil && run.StartedAt.Before(run.CreatedAt) {
		return NewAutomationValidationError(field+".started_at", "must not precede created_at")
	}
	completionFloor := run.CreatedAt
	if run.StartedAt != nil {
		completionFloor = *run.StartedAt
	}
	if run.CompletedAt != nil && run.CompletedAt.Before(completionFloor) {
		return NewAutomationValidationError(field+".completed_at", "must not precede run start")
	}
	switch run.Status {
	case AutomationRunQueued:
		if run.StartedAt != nil || run.CompletedAt != nil {
			return NewAutomationValidationError(field+".status", "queued runs must not have start or completion times")
		}
	case AutomationRunRunning:
		if run.StartedAt == nil || run.CompletedAt != nil {
			return NewAutomationValidationError(field+".status", "running runs require only a start time")
		}
	case AutomationRunSucceeded, AutomationRunFailed:
		if run.StartedAt == nil || run.CompletedAt == nil {
			return NewAutomationValidationError(field+".status", "completed runs require start and completion times")
		}
	}
	return nil
}
