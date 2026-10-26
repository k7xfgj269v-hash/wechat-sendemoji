package main

import (
	"encoding/json"
	"time"
)

func newAutomationState() AutomationState {
	return newAutomationStateAt(time.Now().UTC())
}

func newAutomationStateAt(now time.Time) AutomationState {
	now = now.UTC()
	return AutomationState{
		SchemaVersion: AutomationSnapshotSchemaVersion,
		Policy: AutomationPolicy{
			Version:          1,
			Mode:             AutomationModeManual,
			R1MinConfidence:  1,
			R1AllowedTools:   []AutomationToolName{},
			R2MinConfidence:  1,
			R2AllowedIntents: []string{},
			UpdatedAt:        now,
		},
		Sessions:           []AutomationSession{},
		Turns:              []AutomationTurn{},
		TurnEvents:         []AutomationTurnEvent{},
		Runs:               []AutomationRun{},
		Actions:            []AutomationAction{},
		Results:            []AutomationResult{},
		Approvals:          []AutomationApprovalRecord{},
		Outbox:             []AutomationOutboxEntry{},
		Idempotency:        map[string]AutomationIdempotencyRecord{},
		PendingAudit:       []AutomationAuditRecord{},
		PermissionRequests: []AutomationPermissionRequest{},
		PermissionRules:    []AutomationPermissionRule{},
	}
}

func cloneAutomationState(state AutomationState) AutomationState {
	cloned := state
	cloned.Policy.R1AllowedTools = cloneAutomationSlice(state.Policy.R1AllowedTools)
	cloned.Policy.R2AllowedIntents = cloneAutomationSlice(state.Policy.R2AllowedIntents)

	if state.Sessions != nil {
		cloned.Sessions = make([]AutomationSession, len(state.Sessions))
		for i := range state.Sessions {
			cloned.Sessions[i] = state.Sessions[i]
			if state.Sessions[i].ContextCheckpoint != nil {
				checkpoint := cloneAutomationChatCheckpoint(*state.Sessions[i].ContextCheckpoint)
				cloned.Sessions[i].ContextCheckpoint = &checkpoint
			}
		}
	}
	if state.Turns != nil {
		cloned.Turns = make([]AutomationTurn, len(state.Turns))
		for i := range state.Turns {
			cloned.Turns[i] = state.Turns[i]
			cloned.Turns[i].StartedAt = cloneAutomationTimePointer(state.Turns[i].StartedAt)
			cloned.Turns[i].CompletedAt = cloneAutomationTimePointer(state.Turns[i].CompletedAt)
			if state.Turns[i].ProviderUsage != nil {
				usage := *state.Turns[i].ProviderUsage
				cloned.Turns[i].ProviderUsage = &usage
			}
		}
	}
	if state.TurnEvents != nil {
		cloned.TurnEvents = make([]AutomationTurnEvent, len(state.TurnEvents))
		for i := range state.TurnEvents {
			cloned.TurnEvents[i] = state.TurnEvents[i]
			cloned.TurnEvents[i].Data = cloneAutomationBytes(state.TurnEvents[i].Data)
		}
	}
	if state.Runs != nil {
		cloned.Runs = make([]AutomationRun, len(state.Runs))
		for i := range state.Runs {
			cloned.Runs[i] = state.Runs[i]
			cloned.Runs[i].ActionIDs = cloneAutomationSlice(state.Runs[i].ActionIDs)
			cloned.Runs[i].StartedAt = cloneAutomationTimePointer(state.Runs[i].StartedAt)
			cloned.Runs[i].CompletedAt = cloneAutomationTimePointer(state.Runs[i].CompletedAt)
		}
	}
	if state.Results != nil {
		cloned.Results = make([]AutomationResult, len(state.Results))
		for i := range state.Results {
			cloned.Results[i] = state.Results[i]
			cloned.Results[i].Data = cloneAutomationBytes(state.Results[i].Data)
		}
	}
	if state.Actions != nil {
		cloned.Actions = make([]AutomationAction, len(state.Actions))
		for i := range state.Actions {
			cloned.Actions[i] = state.Actions[i]
			cloned.Actions[i].RiskReasons = cloneAutomationSlice(state.Actions[i].RiskReasons)
			cloned.Actions[i].Evidence = cloneAutomationSlice(state.Actions[i].Evidence)
			cloned.Actions[i].Preconditions = cloneAutomationSlice(state.Actions[i].Preconditions)
			cloned.Actions[i].DependsOnActionIDs = cloneAutomationSlice(state.Actions[i].DependsOnActionIDs)
			if state.Actions[i].Recipient != nil {
				recipient := *state.Actions[i].Recipient
				cloned.Actions[i].Recipient = &recipient
			}
			cloned.Actions[i].Payload = cloneAutomationBytes(state.Actions[i].Payload)
			cloned.Actions[i].Result = cloneAutomationBytes(state.Actions[i].Result)
		}
	}
	cloned.Approvals = cloneAutomationSlice(state.Approvals)
	if state.Outbox != nil {
		cloned.Outbox = make([]AutomationOutboxEntry, len(state.Outbox))
		for i := range state.Outbox {
			cloned.Outbox[i] = state.Outbox[i]
			cloned.Outbox[i].DispatchStartedAt = cloneAutomationTimePointer(state.Outbox[i].DispatchStartedAt)
			cloned.Outbox[i].CompletedAt = cloneAutomationTimePointer(state.Outbox[i].CompletedAt)
		}
	}
	if state.Idempotency != nil {
		cloned.Idempotency = make(map[string]AutomationIdempotencyRecord, len(state.Idempotency))
		for key, record := range state.Idempotency {
			cloned.Idempotency[key] = record
		}
	}
	if state.PendingAudit != nil {
		cloned.PendingAudit = make([]AutomationAuditRecord, len(state.PendingAudit))
		for i := range state.PendingAudit {
			cloned.PendingAudit[i] = state.PendingAudit[i]
			cloned.PendingAudit[i].Data = cloneAutomationBytes(state.PendingAudit[i].Data)
		}
	}
	cloned.PermissionRequests = cloneAutomationSlice(state.PermissionRequests)
	if state.PermissionRules != nil {
		cloned.PermissionRules = make([]AutomationPermissionRule, len(state.PermissionRules))
		for i := range state.PermissionRules {
			cloned.PermissionRules[i] = state.PermissionRules[i]
			cloned.PermissionRules[i].Scope = cloneAutomationBytes(state.PermissionRules[i].Scope)
		}
	}
	return cloned
}

func cloneAutomationChatCheckpoint(value AutomationChatCheckpoint) AutomationChatCheckpoint {
	cloned := value
	cloned.Summary.Facts = cloneAutomationSlice(value.Summary.Facts)
	cloned.Summary.EntityRefs = cloneAutomationSlice(value.Summary.EntityRefs)
	cloned.Summary.Decisions = cloneAutomationSlice(value.Summary.Decisions)
	cloned.Summary.PendingItems = cloneAutomationSlice(value.Summary.PendingItems)
	cloned.Summary.ToolResultRefs = cloneAutomationSlice(value.Summary.ToolResultRefs)
	cloned.Summary.UserConstraints = cloneAutomationSlice(value.Summary.UserConstraints)
	return cloned
}

func normalizeAutomationOutboxChatTypes(state *AutomationState) {
	for i := range state.Outbox {
		if state.Outbox[i].ChatType == "" {
			state.Outbox[i].ChatType = ChatTypePrivate
		}
	}
}

func automationOutboxChatType(entry AutomationOutboxEntry) ChatType {
	if entry.ChatType == "" {
		return ChatTypePrivate
	}
	return entry.ChatType
}

func cloneAutomationSlice[T any](values []T) []T {
	if values == nil {
		return nil
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}

func cloneAutomationBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte(nil), value...)
}

func cloneAutomationTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

type automationRawStateV2 struct {
	SchemaVersion               int                        `json:"schema_version"`
	Counters                    json.RawMessage            `json:"counters"`
	Policy                      json.RawMessage            `json:"policy"`
	Sessions                    []json.RawMessage          `json:"sessions"`
	Turns                       []json.RawMessage          `json:"turns"`
	TurnEvents                  []json.RawMessage          `json:"turn_events"`
	Runs                        []json.RawMessage          `json:"runs"`
	Actions                     []json.RawMessage          `json:"actions"`
	Results                     []json.RawMessage          `json:"results"`
	Approvals                   []json.RawMessage          `json:"approvals"`
	Outbox                      []json.RawMessage          `json:"outbox"`
	Idempotency                 map[string]json.RawMessage `json:"idempotency"`
	PendingAudit                []json.RawMessage          `json:"pending_audit"`
	PermissionRequests          []json.RawMessage          `json:"permission_requests"`
	PermissionRules             []json.RawMessage          `json:"permission_rules"`
	LastObservedCRMEventCounter uint64                     `json:"last_observed_crm_event_counter"`
}
