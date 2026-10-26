package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"unicode/utf8"
)

func decodeAutomationState(data []byte) (AutomationState, error) {
	state, _, err := decodeAutomationStateVersioned(data)
	return state, err
}

func decodeAutomationStateVersioned(data []byte) (AutomationState, int, error) {
	version, err := decodeAutomationSchemaVersion(data)
	if err != nil {
		return AutomationState{}, 0, err
	}
	switch version {
	case 1:
		state, err := decodeAndMigrateAutomationStateV1(data)
		return state, 1, err
	case 2, AutomationSnapshotSchemaVersion:
		state, err := decodeAutomationStateV2(data)
		return state, version, err
	default:
		return AutomationState{}, version, NewAutomationValidationError(
			"schema_version",
			fmt.Sprintf("unsupported schema version %d", version),
		)
	}
}

func decodeAutomationSchemaVersion(data []byte) (int, error) {
	if !utf8.Valid(data) {
		return 0, NewAutomationValidationError("snapshot", "must contain valid UTF-8 JSON")
	}
	trimmed := trimAutomationJSONWhitespace(data)
	if len(trimmed) < 2 || trimmed[0] != '{' {
		return 0, NewAutomationValidationError("snapshot", "must contain one JSON object")
	}
	if err := rejectAutomationDuplicateJSONKeys("snapshot", trimmed); err != nil {
		return 0, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return 0, NewAutomationValidationError("snapshot", "contains invalid JSON")
	}
	raw, exists := object["schema_version"]
	if !exists {
		return 0, NewAutomationValidationError("snapshot.schema_version", "is required")
	}
	if bytes.Equal(trimAutomationJSONWhitespace(raw), []byte("null")) {
		return 0, NewAutomationValidationError("snapshot.schema_version", "must not be null")
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		return 0, NewAutomationValidationError("snapshot.schema_version", "is invalid")
	}
	return version, nil
}

func decodeAutomationStateV2(data []byte) (AutomationState, error) {
	var rawState automationRawStateV2
	if err := decodeAutomationJSONObject(
		"snapshot",
		data,
		&rawState,
		"schema_version",
		"counters",
		"policy",
		"sessions",
		"turns",
		"turn_events",
		"runs",
		"actions",
		"results",
		"approvals",
		"outbox",
		"idempotency",
		"pending_audit",
		"permission_requests",
		"permission_rules",
		"last_observed_crm_event_counter",
	); err != nil {
		return AutomationState{}, err
	}

	state := AutomationState{
		SchemaVersion:               rawState.SchemaVersion,
		LastObservedCRMEventCounter: rawState.LastObservedCRMEventCounter,
	}
	if err := decodeAutomationJSONObject(
		"snapshot.counters",
		rawState.Counters,
		&state.Counters,
		"session",
		"turn",
		"turn_event",
		"run",
		"action",
		"result",
		"approval",
		"outbox",
		"audit",
		"permission_request",
		"permission_rule",
	); err != nil {
		return AutomationState{}, err
	}
	if err := decodeAutomationJSONObject(
		"snapshot.policy",
		rawState.Policy,
		&state.Policy,
		"version",
		"mode",
		"r1_auto_approve",
		"r1_min_confidence",
		"r1_allowed_tools",
		"r2_private_reply_auto_approve",
		"r2_min_confidence",
		"r2_allowed_intents",
		"updated_at",
	); err != nil {
		return AutomationState{}, err
	}

	if rawState.Sessions != nil {
		state.Sessions = make([]AutomationSession, len(rawState.Sessions))
		for i := range rawState.Sessions {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.sessions", i),
				rawState.Sessions[i],
				&state.Sessions[i],
				"id", "kind", "title", "status", "version", "created_at", "updated_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.Turns != nil {
		state.Turns = make([]AutomationTurn, len(rawState.Turns))
		for i := range rawState.Turns {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.turns", i),
				rawState.Turns[i],
				&state.Turns[i],
				"id", "session_id", "user_text", "assistant_text", "status", "provider_name",
				"version", "created_at", "updated_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.TurnEvents != nil {
		state.TurnEvents = make([]AutomationTurnEvent, len(rawState.TurnEvents))
		for i := range rawState.TurnEvents {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.turn_events", i),
				rawState.TurnEvents[i],
				&state.TurnEvents[i],
				"sequence", "session_id", "turn_id", "type", "data", "created_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.Runs != nil {
		state.Runs = make([]AutomationRun, len(rawState.Runs))
		for i := range rawState.Runs {
			if err := decodeAutomationRunJSON(
				automationIndexedField("snapshot.runs", i),
				rawState.Runs[i],
				&state.Runs[i],
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.Actions != nil {
		state.Actions = make([]AutomationAction, len(rawState.Actions))
		for i := range rawState.Actions {
			if err := decodeAutomationActionJSON(
				automationIndexedField("snapshot.actions", i),
				rawState.Actions[i],
				&state.Actions[i],
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.Results != nil {
		state.Results = make([]AutomationResult, len(rawState.Results))
		for i := range rawState.Results {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.results", i),
				rawState.Results[i],
				&state.Results[i],
				"id", "action_id", "permission_request_id", "status", "created_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.Approvals != nil {
		state.Approvals = make([]AutomationApprovalRecord, len(rawState.Approvals))
		for i := range rawState.Approvals {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.approvals", i),
				rawState.Approvals[i],
				&state.Approvals[i],
				"id",
				"action_id",
				"action_version",
				"decision",
				"operator_id",
				"created_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.Outbox != nil {
		state.Outbox = make([]AutomationOutboxEntry, len(rawState.Outbox))
		for i := range rawState.Outbox {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.outbox", i),
				rawState.Outbox[i],
				&state.Outbox[i],
				"id",
				"action_id",
				"idempotency_key",
				"peer_id",
				"text",
				"status",
				"attempt",
				"dispatch_attempted",
				"chat_type",
				"created_at",
				"updated_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	normalizeAutomationOutboxChatTypes(&state)
	if rawState.Idempotency != nil {
		state.Idempotency = make(map[string]AutomationIdempotencyRecord, len(rawState.Idempotency))
		keys := make([]string, 0, len(rawState.Idempotency))
		for key := range rawState.Idempotency {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			var record AutomationIdempotencyRecord
			if err := decodeAutomationJSONObject(
				"snapshot.idempotency["+strconv.Quote(key)+"]",
				rawState.Idempotency[key],
				&record,
				"kind",
				"resource_id",
				"created_at",
			); err != nil {
				return AutomationState{}, err
			}
			state.Idempotency[key] = record
		}
	}
	if rawState.PendingAudit != nil {
		state.PendingAudit = make([]AutomationAuditRecord, len(rawState.PendingAudit))
		for i := range rawState.PendingAudit {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.pending_audit", i),
				rawState.PendingAudit[i],
				&state.PendingAudit[i],
				"id",
				"event_type",
				"actor_type",
				"data",
				"created_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.PermissionRequests != nil {
		state.PermissionRequests = make([]AutomationPermissionRequest, len(rawState.PermissionRequests))
		for i := range rawState.PermissionRequests {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.permission_requests", i),
				rawState.PermissionRequests[i],
				&state.PermissionRequests[i],
				"id", "run_id", "action_id", "tool", "risk", "status", "version", "created_at", "updated_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}
	if rawState.PermissionRules != nil {
		state.PermissionRules = make([]AutomationPermissionRule, len(rawState.PermissionRules))
		for i := range rawState.PermissionRules {
			if err := decodeAutomationJSONObject(
				automationIndexedField("snapshot.permission_rules", i),
				rawState.PermissionRules[i],
				&state.PermissionRules[i],
				"id", "effect", "tool", "scope_key", "scope", "enabled", "version", "created_by", "created_at", "updated_at",
			); err != nil {
				return AutomationState{}, err
			}
		}
	}

	if state.SchemaVersion == 2 {
		state.SchemaVersion = AutomationSnapshotSchemaVersion
	}
	if err := normalizeAutomationWorkbenchState(&state); err != nil {
		return AutomationState{}, err
	}
	if err := ValidateAutomationState(&state); err != nil {
		return AutomationState{}, err
	}
	return state, nil
}

func decodeAutomationRunJSON(field string, raw json.RawMessage, run *AutomationRun) error {
	return decodeAutomationJSONObject(
		field,
		raw,
		run,
		"id",
		"session_id",
		"turn_id",
		"idempotency_key",
		"engine_version",
		"provider_name",
		"source_message_event_id",
		"source_message_key",
		"source_message_version",
		"status",
		"version",
		"attempt",
		"action_ids",
		"created_at",
	)
}

func decodeAutomationActionJSON(field string, raw json.RawMessage, action *AutomationAction) error {
	if err := decodeAutomationJSONObject(
		field,
		raw,
		action,
		"id",
		"run_id",
		"provider_call_id",
		"tool",
		"status",
		"risk",
		"policy_decision",
		"policy_version",
		"risk_reasons",
		"confidence",
		"evidence",
		"source_message",
		"preconditions",
		"depends_on_action_ids",
		"payload",
		"retryable",
		"attempt",
		"version",
		"created_at",
		"updated_at",
	); err != nil {
		return err
	}

	var nested struct {
		Evidence      []json.RawMessage `json:"evidence"`
		SourceMessage json.RawMessage   `json:"source_message"`
		Preconditions []json.RawMessage `json:"preconditions"`
		Recipient     json.RawMessage   `json:"recipient"`
	}
	if err := json.Unmarshal(raw, &nested); err != nil {
		return NewAutomationValidationError(field, "contains invalid JSON")
	}
	for i := range nested.Evidence {
		var evidence AutomationEvidence
		if err := decodeAutomationJSONObject(
			automationIndexedField(field+".evidence", i),
			nested.Evidence[i],
			&evidence,
			"source",
			"excerpt",
		); err != nil {
			return err
		}
	}
	var source AutomationSourceMessageRef
	if err := decodeAutomationJSONObject(
		field+".source_message",
		nested.SourceMessage,
		&source,
		"event_id",
		"message_key",
		"expected_version",
		"chat_type",
		"peer_id",
		"suspicious",
	); err != nil {
		return err
	}
	for i := range nested.Preconditions {
		var precondition AutomationEntityPrecondition
		if err := decodeAutomationJSONObject(
			automationIndexedField(field+".preconditions", i),
			nested.Preconditions[i],
			&precondition,
			"entity_type",
			"entity_id",
			"expected_version",
		); err != nil {
			return err
		}
	}
	if len(nested.Recipient) != 0 && !bytes.Equal(bytes.TrimSpace(nested.Recipient), []byte("null")) {
		var recipient AutomationRecipient
		if err := decodeAutomationJSONObject(
			field+".recipient",
			nested.Recipient,
			&recipient,
			"chat_type",
			"peer_id",
		); err != nil {
			return err
		}
	}
	return nil
}
