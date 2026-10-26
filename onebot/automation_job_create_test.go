package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestAutomationExecutorJobCreateIngestionLifecycle(t *testing.T) {
	automation := automationJobCreateTestStore(t)
	crm := crmJobIngestionOpenStore(t)
	groupID := "automation_jobs@chatroom"
	crmJobIngestionSeedSourceGroup(t, crm, groupID, true)
	executor := NewDefaultAutomationCommandExecutor(automation, crm, nil)

	draft := crmJobIngestionDraft("AUTO-LINE-001")
	createEvent := crmJobIngestionSeedEvent(t, crm, groupID, "mid-create", "same source content")
	createAction := automationJobCreateAction(t, createEvent, groupID, draft)
	created := executor.Execute(
		context.Background(),
		createAction,
	)
	automationAssertJobCreateResult(
		t, executor, crm, createAction, created, "job_00000001", 1, JobIngestionCreate,
	)

	duplicateEvent := crmJobIngestionSeedEvent(t, crm, groupID, "mid-duplicate", "same source content")
	duplicateAction := automationJobCreateAction(t, duplicateEvent, groupID, draft)
	duplicate := executor.Execute(
		context.Background(),
		duplicateAction,
	)
	automationAssertJobCreateResult(
		t, executor, crm, duplicateAction, duplicate, "job_00000001", 1, JobIngestionDuplicate,
	)

	noopDraft := draft
	noopDraft.Title = "  Synthetic Operator  "
	noopDraft.Requirements = []string{"Go", "assembly"}
	noopEvent := crmJobIngestionSeedEvent(t, crm, groupID, "mid-noop", "different source content")
	noopAction := automationJobCreateAction(t, noopEvent, groupID, noopDraft)
	noop := executor.Execute(
		context.Background(),
		noopAction,
	)
	automationAssertJobCreateResult(
		t, executor, crm, noopAction, noop, "job_00000001", 1, JobIngestionNoop,
	)

	updateDraft := draft
	updateDraft.Headcount = 24
	updateEvent := crmJobIngestionSeedEvent(t, crm, groupID, "mid-update", "updated source content")
	updateAction := automationJobCreateAction(t, updateEvent, groupID, updateDraft)
	updated := executor.Execute(
		context.Background(),
		updateAction,
	)
	automationAssertJobCreateResult(
		t, executor, crm, updateAction, updated, "job_00000001", 2, JobIngestionUpdate,
	)
}

func TestAutomationExecutorJobCreateRejectsUnverifiedSourceIdentity(t *testing.T) {
	tests := []struct {
		name       string
		mutateArgs func(*JobCreateArgs)
		mutate     func(*AutomationAction)
	}{
		{
			name: "event mismatch",
			mutateArgs: func(args *JobCreateArgs) {
				args.EventID = "event_00000099"
			},
		},
		{
			name: "group mismatch",
			mutateArgs: func(args *JobCreateArgs) {
				args.GroupID = "other_jobs@chatroom"
			},
		},
		{
			name: "non-group action source",
			mutate: func(action *AutomationAction) {
				action.SourceMessage.ChatType = ChatTypePrivate
			},
		},
		{
			name: "recipient supplied",
			mutate: func(action *AutomationAction) {
				action.Recipient = &AutomationRecipient{
					ChatType: ChatTypePrivate,
					PeerID:   "wxid_other",
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automation := automationJobCreateTestStore(t)
			crm := crmJobIngestionOpenStore(t)
			groupID := "automation_jobs@chatroom"
			crmJobIngestionSeedSourceGroup(t, crm, groupID, true)
			event := crmJobIngestionSeedEvent(t, crm, groupID, "mid-source", "source content")
			args := JobCreateArgs{
				EventID: event.ID,
				GroupID: groupID,
				Job:     crmJobIngestionDraft("AUTO-LINE-002"),
			}
			if test.mutateArgs != nil {
				test.mutateArgs(&args)
			}
			action := automationJobCreateActionWithArgs(t, event, args)
			if test.mutate != nil {
				test.mutate(&action)
			}

			result := NewDefaultAutomationCommandExecutor(automation, crm, nil).Execute(
				context.Background(),
				action,
			)
			if result.Status != AutomationActionFailed ||
				result.ErrorCode != "source_identity_invalid" {
				t.Fatalf("result = %+v, want source identity failure", result)
			}
			if state := crm.Snapshot(); len(state.Jobs) != 0 || len(state.JobProvenance) != 0 {
				t.Fatalf(
					"invalid source mutated CRM: jobs=%d provenance=%d",
					len(state.Jobs),
					len(state.JobProvenance),
				)
			}
		})
	}
}

func TestAutomationExecutorJobCreateRequiresEnabledSourceGroup(t *testing.T) {
	automation := automationJobCreateTestStore(t)
	crm := crmJobIngestionOpenStore(t)
	groupID := "disabled_jobs@chatroom"
	crmJobIngestionSeedSourceGroup(t, crm, groupID, false)
	event := crmJobIngestionSeedEvent(t, crm, groupID, "mid-disabled", "source content")

	result := NewDefaultAutomationCommandExecutor(automation, crm, nil).Execute(
		context.Background(),
		automationJobCreateAction(
			t,
			event,
			groupID,
			crmJobIngestionDraft("AUTO-LINE-003"),
		),
	)
	if result.Status != AutomationActionFailed || result.ErrorCode != "crm_failed" {
		t.Fatalf("result = %+v, want disabled source failure", result)
	}
	if state := crm.Snapshot(); len(state.Jobs) != 0 || len(state.JobProvenance) != 0 {
		t.Fatalf(
			"disabled source mutated CRM: jobs=%d provenance=%d",
			len(state.Jobs),
			len(state.JobProvenance),
		)
	}
}

func automationJobCreateTestStore(t *testing.T) *AutomationStore {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenAutomationStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func automationJobCreateAction(
	t *testing.T,
	event MessageEvent,
	groupID string,
	draft JobDraft,
) AutomationAction {
	t.Helper()
	return automationJobCreateActionWithArgs(t, event, JobCreateArgs{
		EventID: event.ID,
		GroupID: groupID,
		Job:     draft,
	})
}

func automationJobCreateActionWithArgs(
	t *testing.T,
	event MessageEvent,
	args JobCreateArgs,
) AutomationAction {
	t.Helper()
	payload, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal job.create arguments: %v", err)
	}
	return AutomationAction{
		Tool:    AutomationToolJobCreate,
		Payload: payload,
		SourceMessage: AutomationSourceMessageRef{
			EventID:         event.ID,
			MessageKey:      event.MessageKey,
			ExpectedVersion: event.Version,
			ChatType:        event.ChatType,
			PeerID:          event.PeerID,
		},
	}
}

func automationAssertJobCreateResult(
	t *testing.T,
	executor AutomationCommandExecutor,
	crm *CRMStore,
	action AutomationAction,
	result AutomationExecutionResult,
	jobID string,
	version uint64,
	decision JobIngestionDecision,
) {
	t.Helper()
	if result.Status != AutomationActionSucceeded ||
		result.Result.Entity == nil ||
		result.Result.Entity.EntityType != AutomationEntityJob ||
		result.Result.Entity.EntityID != jobID ||
		result.Result.Entity.Version != version {
		t.Fatalf("job execution result = %+v, want job %s version %d", result, jobID, version)
	}
	state := crm.Snapshot()
	if len(state.JobProvenance) == 0 {
		t.Fatal("job ingestion did not persist provenance")
	}
	if got := state.JobProvenance[len(state.JobProvenance)-1].Decision; got != decision {
		t.Fatalf("latest ingestion decision = %q, want %q", got, decision)
	}
	raw, err := automationMarshalExecutionResult(executor, action, result)
	if err != nil {
		t.Fatalf("automationMarshalExecutionResult() error = %v", err)
	}
	var output AutomationJobCreateResult
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("decode job.create execution result: %v", err)
	}
	if output.Entity == nil ||
		output.Entity.EntityType != AutomationEntityJob ||
		output.Entity.EntityID != jobID ||
		output.Entity.Version != version ||
		output.Decision != decision {
		t.Fatalf("job.create output = %+v", output)
	}
}
