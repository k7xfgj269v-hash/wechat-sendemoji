package main

import (
	"bytes"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

type automationCountersV1 struct {
	Run               uint64 `json:"run"`
	Action            uint64 `json:"action"`
	Approval          uint64 `json:"approval"`
	Outbox            uint64 `json:"outbox"`
	Audit             uint64 `json:"audit"`
	PermissionRequest uint64 `json:"permission_request"`
	PermissionRule    uint64 `json:"permission_rule"`
}

type automationRunV1 struct {
	ID                     string              `json:"id"`
	IdempotencyKey         string              `json:"idempotency_key"`
	EngineVersion          string              `json:"engine_version"`
	ProviderName           string              `json:"provider_name"`
	SourceMessageEventID   string              `json:"source_message_event_id"`
	SourceMessageKey       string              `json:"source_message_key"`
	SourceMessageVersion   uint64              `json:"source_message_version"`
	Status                 AutomationRunStatus `json:"status"`
	Version                uint64              `json:"version"`
	Attempt                uint32              `json:"attempt"`
	RetryOfRunID           string              `json:"retry_of_run_id,omitempty"`
	FailureStage           string              `json:"failure_stage,omitempty"`
	FailureCode            string              `json:"failure_code,omitempty"`
	FailureMessage         string              `json:"failure_message,omitempty"`
	ProviderResponseSHA256 string              `json:"provider_response_sha256,omitempty"`
	ActionIDs              []string            `json:"action_ids"`
	CreatedAt              time.Time           `json:"created_at"`
	StartedAt              *time.Time          `json:"started_at,omitempty"`
	CompletedAt            *time.Time          `json:"completed_at,omitempty"`
}

type automationActionV1 struct {
	ID                 string                         `json:"id"`
	RunID              string                         `json:"run_id"`
	ProviderCallID     string                         `json:"provider_call_id"`
	Tool               AutomationToolName             `json:"tool"`
	Status             AutomationActionStatus         `json:"status"`
	Risk               AutomationRisk                 `json:"risk"`
	PolicyDecision     AutomationPolicyDecision       `json:"policy_decision"`
	PolicyVersion      uint64                         `json:"policy_version"`
	RiskReasons        []string                       `json:"risk_reasons"`
	Confidence         float64                        `json:"confidence"`
	Evidence           []AutomationEvidence           `json:"evidence"`
	SourceMessage      AutomationSourceMessageRef     `json:"source_message"`
	Preconditions      []AutomationEntityPrecondition `json:"preconditions"`
	DependsOnActionIDs []string                       `json:"depends_on_action_ids"`
	Recipient          *AutomationRecipient           `json:"recipient,omitempty"`
	Payload            json.RawMessage                `json:"payload"`
	Result             json.RawMessage                `json:"result,omitempty"`
	ErrorCode          string                         `json:"error_code,omitempty"`
	ErrorMessage       string                         `json:"error_message,omitempty"`
	Retryable          bool                           `json:"retryable"`
	Attempt            uint32                         `json:"attempt"`
	RetryOfActionID    string                         `json:"retry_of_action_id,omitempty"`
	ReplacesActionID   string                         `json:"replaces_action_id,omitempty"`
	ReplacedByActionID string                         `json:"replaced_by_action_id,omitempty"`
	Version            uint64                         `json:"version"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
}

type automationStateV1 struct {
	SchemaVersion               int                                    `json:"schema_version"`
	Counters                    automationCountersV1                   `json:"counters"`
	Policy                      AutomationPolicy                       `json:"policy"`
	Runs                        []automationRunV1                      `json:"runs"`
	Actions                     []automationActionV1                   `json:"actions"`
	Approvals                   []AutomationApprovalRecord             `json:"approvals"`
	Outbox                      []AutomationOutboxEntry                `json:"outbox"`
	Idempotency                 map[string]AutomationIdempotencyRecord `json:"idempotency"`
	PendingAudit                []AutomationAuditRecord                `json:"pending_audit"`
	PermissionRequests          []AutomationPermissionRequest          `json:"permission_requests"`
	PermissionRules             []AutomationPermissionRule             `json:"permission_rules"`
	LastObservedCRMEventCounter uint64                                 `json:"last_observed_crm_event_counter"`
}

func decodeAndMigrateAutomationStateV1(data []byte) (AutomationState, error) {
	var legacy automationStateV1
	if err := decodeAutomationJSONObject(
		"snapshot",
		data,
		&legacy,
		"schema_version", "counters", "policy", "runs", "actions", "approvals", "outbox",
		"idempotency", "pending_audit", "permission_requests", "permission_rules",
		"last_observed_crm_event_counter",
	); err != nil {
		return AutomationState{}, err
	}
	if legacy.SchemaVersion != 1 {
		return AutomationState{}, NewAutomationValidationError("schema_version", "must be version 1")
	}
	if err := validateAutomationV1RequiredFields(data); err != nil {
		return AutomationState{}, err
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		return AutomationState{}, WrapAutomationPersistence("convert version 1 snapshot", err)
	}
	var state AutomationState
	if err := json.Unmarshal(encoded, &state); err != nil {
		return AutomationState{}, WrapAutomationPersistence("convert version 1 snapshot", err)
	}
	state.SchemaVersion = 1
	state.Sessions = []AutomationSession{}
	state.Turns = []AutomationTurn{}
	state.TurnEvents = []AutomationTurnEvent{}
	state.Results = []AutomationResult{}
	normalizeAutomationOutboxChatTypes(&state)
	if err := validateAutomationStateV1(&state); err != nil {
		return AutomationState{}, err
	}
	return migrateAutomationStateV1(state)
}

func validateAutomationV1RequiredFields(data []byte) error {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return NewAutomationValidationError("snapshot", "contains invalid JSON")
	}
	if err := requireAutomationJSONObjectFields(
		"snapshot.counters", top["counters"],
		"run", "action", "approval", "outbox", "audit", "permission_request", "permission_rule",
	); err != nil {
		return err
	}
	if err := requireAutomationJSONObjectFields(
		"snapshot.policy", top["policy"],
		"version", "mode", "r1_auto_approve", "r1_min_confidence", "r1_allowed_tools",
		"r2_private_reply_auto_approve", "r2_min_confidence", "r2_allowed_intents", "updated_at",
	); err != nil {
		return err
	}
	checkArray := func(name string, required []string, nested func(string, map[string]json.RawMessage) error) error {
		var items []json.RawMessage
		if err := json.Unmarshal(top[name], &items); err != nil {
			return NewAutomationValidationError("snapshot."+name, "contains invalid JSON")
		}
		for i, item := range items {
			field := automationIndexedField("snapshot."+name, i)
			if err := requireAutomationJSONObjectFields(field, item, required...); err != nil {
				return err
			}
			if nested != nil {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(item, &object); err != nil {
					return NewAutomationValidationError(field, "contains invalid JSON")
				}
				if err := nested(field, object); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := checkArray("runs", []string{
		"id", "idempotency_key", "engine_version", "provider_name", "source_message_event_id",
		"source_message_key", "source_message_version", "status", "version", "attempt", "action_ids", "created_at",
	}, nil); err != nil {
		return err
	}
	if err := checkArray("actions", []string{
		"id", "run_id", "provider_call_id", "tool", "status", "risk", "policy_decision", "policy_version",
		"risk_reasons", "confidence", "evidence", "source_message", "preconditions", "depends_on_action_ids",
		"payload", "retryable", "attempt", "version", "created_at", "updated_at",
	}, validateAutomationV1ActionRequiredFields); err != nil {
		return err
	}
	if err := checkArray("approvals", []string{"id", "action_id", "action_version", "decision", "operator_id", "created_at"}, nil); err != nil {
		return err
	}
	if err := checkArray("outbox", []string{
		"id", "action_id", "idempotency_key", "chat_type", "peer_id", "text", "status",
		"attempt", "dispatch_attempted", "created_at", "updated_at",
	}, nil); err != nil {
		return err
	}
	if err := checkArray("pending_audit", []string{"id", "event_type", "actor_type", "data", "created_at"}, nil); err != nil {
		return err
	}
	if err := checkArray("permission_requests", []string{
		"id", "run_id", "action_id", "tool", "risk", "status", "version", "created_at", "updated_at",
	}, nil); err != nil {
		return err
	}
	if err := checkArray("permission_rules", []string{
		"id", "effect", "tool", "scope_key", "scope", "enabled", "version", "created_by", "created_at", "updated_at",
	}, nil); err != nil {
		return err
	}
	var idempotency map[string]json.RawMessage
	if err := json.Unmarshal(top["idempotency"], &idempotency); err != nil {
		return NewAutomationValidationError("snapshot.idempotency", "contains invalid JSON")
	}
	for key, raw := range idempotency {
		if err := requireAutomationJSONObjectFields(
			"snapshot.idempotency["+strconv.Quote(key)+"]", raw, "kind", "resource_id", "created_at",
		); err != nil {
			return err
		}
	}
	return nil
}

func validateAutomationV1ActionRequiredFields(field string, object map[string]json.RawMessage) error {
	if err := requireAutomationJSONObjectFields(
		field+".source_message", object["source_message"],
		"event_id", "message_key", "expected_version", "chat_type", "peer_id", "suspicious",
	); err != nil {
		return err
	}
	for name, required := range map[string][]string{
		"evidence":      {"source", "excerpt"},
		"preconditions": {"entity_type", "entity_id", "expected_version"},
	} {
		var items []json.RawMessage
		if err := json.Unmarshal(object[name], &items); err != nil {
			return NewAutomationValidationError(field+"."+name, "contains invalid JSON")
		}
		for i, item := range items {
			if err := requireAutomationJSONObjectFields(
				automationIndexedField(field+"."+name, i), item, required...,
			); err != nil {
				return err
			}
		}
	}
	if raw, exists := object["recipient"]; exists && !bytes.Equal(trimAutomationJSONWhitespace(raw), []byte("null")) {
		if err := requireAutomationJSONObjectFields(field+".recipient", raw, "chat_type", "peer_id"); err != nil {
			return err
		}
	}
	return nil
}

func requireAutomationJSONObjectFields(field string, data []byte, required ...string) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return NewAutomationValidationError(field, "must contain one JSON object")
	}
	for _, name := range required {
		value, exists := object[name]
		if !exists {
			return NewAutomationValidationError(field+"."+name, "is required")
		}
		if bytes.Equal(trimAutomationJSONWhitespace(value), []byte("null")) {
			return NewAutomationValidationError(field+"."+name, "must not be null")
		}
	}
	return nil
}

func migrateAutomationStateV1(state AutomationState) (AutomationState, error) {
	state.SchemaVersion = AutomationSnapshotSchemaVersion
	allocator := &AutomationIDAllocator{counters: state.Counters, active: true}
	defer func() {
		allocator.active = false
	}()

	runIndexes := make([]int, len(state.Runs))
	for i := range runIndexes {
		runIndexes[i] = i
	}
	sort.Slice(runIndexes, func(i, j int) bool {
		left := state.Runs[runIndexes[i]]
		right := state.Runs[runIndexes[j]]
		if left.CreatedAt.Equal(right.CreatedAt) {
			return left.ID < right.ID
		}
		return left.CreatedAt.Before(right.CreatedAt)
	})
	for _, index := range runIndexes {
		if err := appendAutomationRunTrace(&state, allocator, &state.Runs[index], AutomationSessionKindLegacy); err != nil {
			return AutomationState{}, err
		}
	}
	if err := reconcileAutomationTrace(&state, &AutomationState{}, allocator); err != nil {
		return AutomationState{}, err
	}
	allocator.active = false
	state.Counters = allocator.counters
	if err := ValidateAutomationState(&state); err != nil {
		return AutomationState{}, err
	}
	return state, nil
}
