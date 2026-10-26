package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestCRMStateV3MigratesToV4DefaultsAndValidatesProvenance(t *testing.T) {
	legacy := crmStateV3{
		SchemaVersion: 3,
		Counters: crmIDCountersV3{
			Job:            1,
			Event:          2,
			DemandRevision: 1,
		},
		Candidates:      []Candidate{},
		Jobs:            []Job{crmStoreTestJob("job_00000001")},
		Leads:           []Lead{},
		LeadStageEvents: []LeadStageEvent{},
		OnboardingCases: []OnboardingCase{},
		ServiceRequests: []ServiceRequest{},
		FundCallbacks:   []FundCallback{},
		DemandRevisions: []DemandRevision{{
			ID:        "demand_00000001",
			JobID:     "job_00000001",
			Number:    1,
			Snapshot:  jobDemandSnapshot(crmStoreTestJob("job_00000001")),
			CreatedBy: "contract-test",
			Reason:    "fixture",
			CreatedAt: crmStoreTestTime,
		}},
		OperationBatches:   []OperationBatch{},
		ServiceQueues:      []ServiceQueue{},
		FundCallbackLedger: []FundCallbackLedgerEntry{},
		AuditEvents:        []CRMAuditEvent{},
		Tasks:              []FollowUpTask{},
		Notes:              []Note{},
		MessageEvents: []crmMessageEventV3{
			crmStoreTestMessageEventV3(crmStoreTestGroupEvent("event_00000001", "mid-v3-job", "legacy_jobs@chatroom", "")),
			crmStoreTestMessageEventV3(crmStoreTestGroupEvent("event_00000002", "mid-v3-noise", "legacy_noise@chatroom", "")),
		},
		ForwardingRules: []ForwardRule{},
	}
	legacy.Jobs[0].ActiveDemandRevisionID = "demand_00000001"
	legacy.Jobs[0].DemandRevisionNumber = 1

	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	state, migrated, err := decodeCRMStateWithMigration(data)
	if err != nil {
		t.Fatalf("decodeCRMStateWithMigration(v3) error = %v", err)
	}
	if !migrated ||
		state.SchemaVersion != CRMSchemaVersion ||
		state.RecruitingProject.OrganizationName != CRMDefaultOrganizationName ||
		state.RecruitingProject.Version != 1 ||
		!state.RecruitingProject.RequireAuthoritativeJob ||
		len(state.RecruitingProject.SourceGroups) != 0 ||
		len(state.JobProvenance) != 0 {
		t.Fatalf("unexpected migrated v4 state: migrated=%v state=%#v", migrated, state)
	}
	if state.MessageEvents[0].Disposition != MessageDispositionGroupNoise ||
		state.MessageEvents[1].Disposition != MessageDispositionGroupNoise {
		t.Fatalf("v3 message dispositions = %#v", state.MessageEvents)
	}
	for _, event := range state.MessageEvents {
		if event.RecruitingProjectVersion != 0 ||
			event.RecruitingRouteSourceVersion != 0 ||
			event.RecruitingConversationKind != "" ||
			event.RecruitingAllowedTools != nil {
			t.Fatalf("v3 migration fabricated a frozen route snapshot: %#v", event)
		}
	}

	valid := cloneCRMState(state)
	valid.Counters.JobProvenance = 1
	valid.JobProvenance = append(valid.JobProvenance, crmStoreTestJobProvenance(
		"provenance_00000001",
		"job_00000001",
		valid.MessageEvents[0],
		JobIngestionDuplicate,
	))
	if err := validateCRMStateStrict(&valid); err != nil {
		t.Fatalf("validateCRMStateStrict(valid provenance) error = %v", err)
	}

	mismatched := cloneCRMState(valid)
	mismatched.JobProvenance[0].GroupID = "other_jobs@chatroom"
	if err := validateCRMStateStrict(&mismatched); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("mismatched provenance error = %v, want validation error", err)
	}
}

func TestCRMStateV4RouteSnapshotRoundTripAndValidation(t *testing.T) {
	state := newCRMState()
	state.Counters.Event = 1
	event := crmStoreTestGroupEvent(
		"event_00000001",
		"route-snapshot-roundtrip",
		"noise@chatroom",
		"",
	)
	route := buildRecruitingAutomationRoute(event, state.RecruitingProject)
	freezeRecruitingAutomationRoute(&event, route, state.RecruitingProject.Version)
	event.Version++
	event.UpdatedAt = crmPipelineMutationTime(event.UpdatedAt)
	state.MessageEvents = append(state.MessageEvents, event)
	if err := validateCRMStateStrict(&state); err != nil {
		t.Fatalf("validateCRMStateStrict(route snapshot) error = %v", err)
	}

	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	decoded, migrated, err := decodeCRMStateWithMigration(data)
	if err != nil {
		t.Fatalf("decodeCRMStateWithMigration(v4) error = %v", err)
	}
	if migrated || !reflect.DeepEqual(decoded, state) {
		t.Fatalf("v4 roundtrip mismatch: migrated=%v got=%#v want=%#v", migrated, decoded, state)
	}

	cloned := cloneCRMState(state)
	cloned.MessageEvents[0].RecruitingAllowedTools[0] = AutomationToolForwardEvent
	if reflect.DeepEqual(cloned.MessageEvents[0].RecruitingAllowedTools, state.MessageEvents[0].RecruitingAllowedTools) {
		t.Fatal("route tool snapshot leaked through cloneCRMState")
	}

	tests := []struct {
		name   string
		mutate func(*MessageEvent)
	}{
		{
			name: "missing route source version",
			mutate: func(event *MessageEvent) {
				event.RecruitingRouteSourceVersion = 0
			},
		},
		{
			name: "missing conversation kind",
			mutate: func(event *MessageEvent) {
				event.RecruitingConversationKind = ""
			},
		},
		{
			name: "nil allowed tools",
			mutate: func(event *MessageEvent) {
				event.RecruitingAllowedTools = nil
			},
		},
		{
			name: "duplicate allowed tool",
			mutate: func(event *MessageEvent) {
				event.RecruitingAllowedTools = append(
					event.RecruitingAllowedTools,
					event.RecruitingAllowedTools[0],
				)
			},
		},
		{
			name: "outbound tool on group noise",
			mutate: func(event *MessageEvent) {
				event.RecruitingAllowedTools = append(
					event.RecruitingAllowedTools,
					AutomationToolReplyAudienceText,
				)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := cloneCRMState(state)
			test.mutate(&invalid.MessageEvents[0])
			if err := validateCRMStateStrict(&invalid); !errors.Is(err, ErrCRMValidation) {
				t.Fatalf("validateCRMStateStrict() error = %v, want validation error", err)
			}
		})
	}
}

func crmStoreTestMessageEventV3(event MessageEvent) crmMessageEventV3 {
	return crmMessageEventV3{
		ID:           event.ID,
		MessageKey:   event.MessageKey,
		Source:       event.Source,
		Mid:          event.Mid,
		ChatType:     event.ChatType,
		PeerID:       event.PeerID,
		AuthorID:     event.AuthorID,
		GroupID:      event.GroupID,
		Text:         event.Text,
		Kinds:        event.Kinds,
		Timestamp:    event.Timestamp,
		Suspicious:   event.Suspicious,
		ReviewStatus: event.ReviewStatus,
		CandidateID:  event.CandidateID,
		Version:      event.Version,
		CreatedAt:    event.CreatedAt,
		UpdatedAt:    event.UpdatedAt,
	}
}
