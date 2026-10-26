package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type workbenchPipelineRevisionListResult struct {
	Revisions []workbenchPipelineRevisionListItemProjection `json:"revisions"`
}

type workbenchPipelineFixture struct {
	store      *CRMStore
	dispatcher WorkbenchToolDispatcher
	candidate  Candidate
	job        Job
	lead       Lead
}

func TestWorkbenchPipelineOperationsNormalSchemasAndR1Writes(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	fixture := newWorkbenchPipelineFixture(t)
	for _, operation := range []WorkbenchOperation{
		WorkbenchOperationCandidatesUpdate,
		WorkbenchOperationJobsUpdate,
		WorkbenchOperationLeadsUpdate,
	} {
		entry, ok := WorkbenchOperationCatalogEntryFor(AutomationToolWorkbenchMutate, operation)
		if !ok ||
			entry.Risk != AutomationRiskR1 ||
			entry.SideEffect != WorkbenchSideEffectReversible ||
			entry.Availability != WorkbenchAvailabilityAvailable {
			t.Fatalf("R1 catalog entry %s = %+v, found=%t", operation, entry, ok)
		}
	}

	auditCount := len(fixture.store.Snapshot().AuditEvents)
	candidateOwner := "candidate-owner"
	candidateResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationCandidatesUpdate,
		WorkbenchCandidateUpdateArguments{
			CandidateID: fixture.candidate.ID,
			Update: CandidateUpdate{
				ExpectedVersion: fixture.candidate.Version,
				Owner:           &candidateOwner,
			},
		},
	)
	state := fixture.store.Snapshot()
	updatedCandidate := workbenchPipelineCandidateByID(t, state, fixture.candidate.ID)
	if updatedCandidate.Owner != candidateOwner ||
		updatedCandidate.Version != fixture.candidate.Version+1 {
		t.Fatalf("updated candidate = %+v", updatedCandidate)
	}
	assertWorkbenchPipelineSuccess(
		t,
		candidateResult,
		WorkbenchOperationCandidatesUpdate,
		workbenchPipelineProjectCandidateUpdate(updatedCandidate),
		nil,
	)
	assertWorkbenchPipelineAuditDelta(t, state, auditCount, 1)
	auditCount++

	jobTitle := "Senior Engineer"
	jobResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationJobsUpdate,
		WorkbenchJobUpdateArguments{
			JobID: fixture.job.ID,
			Update: JobUpdate{
				ExpectedVersion: fixture.job.Version,
				Title:           &jobTitle,
			},
		},
	)
	state = fixture.store.Snapshot()
	updatedJob := workbenchPipelineJobByID(t, state, fixture.job.ID)
	if updatedJob.Title != jobTitle ||
		updatedJob.Version != fixture.job.Version+1 ||
		updatedJob.DemandRevisionNumber != 2 {
		t.Fatalf("updated job = %+v", updatedJob)
	}
	assertWorkbenchPipelineSuccess(
		t,
		jobResult,
		WorkbenchOperationJobsUpdate,
		workbenchPipelineProjectJobUpdate(updatedJob),
		nil,
	)
	assertWorkbenchPipelineAuditDelta(t, state, auditCount, 1)
	auditCount++

	leadOwner := "lead-owner"
	leadResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationLeadsUpdate,
		WorkbenchLeadUpdateArguments{
			LeadID: fixture.lead.ID,
			Update: LeadUpdate{
				ExpectedVersion: fixture.lead.Version,
				Owner:           &leadOwner,
			},
		},
	)
	state = fixture.store.Snapshot()
	updatedLead := workbenchPipelineLeadByID(t, state, fixture.lead.ID)
	if updatedLead.Owner != leadOwner ||
		updatedLead.Version != fixture.lead.Version+1 {
		t.Fatalf("updated lead = %+v", updatedLead)
	}
	assertWorkbenchPipelineSuccess(
		t,
		leadResult,
		WorkbenchOperationLeadsUpdate,
		updatedLead,
		nil,
	)
	assertWorkbenchPipelineAuditDelta(t, state, auditCount, 1)

	board, err := crmPipelineBoard(fixture.store, "", "")
	if err != nil {
		t.Fatal(err)
	}
	projectedBoard := workbenchPipelineEmptyResponse(board)
	for stageIndex := range board.Stages {
		for leadIndex := range board.Stages[stageIndex].Leads {
			projectedBoard.Stages[stageIndex].Leads = append(
				projectedBoard.Stages[stageIndex].Leads,
				workbenchPipelineProjectLead(board.Stages[stageIndex].Leads[leadIndex]),
			)
		}
	}
	pipelineResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationPipelineGet,
		WorkbenchPageArguments{Limit: 100, Offset: 0},
	)
	assertWorkbenchPipelineSuccess(
		t,
		pipelineResult,
		WorkbenchOperationPipelineGet,
		projectedBoard,
		&WorkbenchPage{Limit: 100, Offset: 0, Returned: 1, Total: 1, HasMore: false},
	)

	revisions := workbenchPipelineRevisionsForJob(state, fixture.job.ID)
	if len(revisions) != 2 {
		t.Fatalf("revisions = %+v, want two", revisions)
	}
	listResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsList,
		WorkbenchRevisionListArguments{JobID: fixture.job.ID, Limit: 100, Offset: 0},
	)
	assertWorkbenchPipelineSuccess(
		t,
		listResult,
		WorkbenchOperationRevisionsList,
		workbenchPipelineRevisionListResult{
			Revisions: workbenchPipelineProjectRevisionListItems(revisions),
		},
		&WorkbenchPage{Limit: 100, Offset: 0, Returned: 2, Total: 2, HasMore: false},
	)

	getResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsGet,
		WorkbenchRevisionGetArguments{
			JobID:      fixture.job.ID,
			RevisionID: revisions[0].ID,
		},
	)
	assertWorkbenchPipelineSuccess(
		t,
		getResult,
		WorkbenchOperationRevisionsGet,
		workbenchPipelineProjectRevisionDetail(revisions[0]),
		nil,
	)

	compareResult := dispatchWorkbenchPipelineOperation(
		t,
		fixture.dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsCompare,
		WorkbenchRevisionCompareArguments{
			JobID:          fixture.job.ID,
			FromRevisionID: revisions[1].ID,
			ToRevisionID:   revisions[0].ID,
		},
	)
	assertWorkbenchPipelineSuccess(
		t,
		compareResult,
		WorkbenchOperationRevisionsCompare,
		workbenchPipelineProjectRevisionComparison(crmPipelineDemandRevisionComparison{
			JobID:         fixture.job.ID,
			From:          revisions[1],
			To:            revisions[0],
			ChangedFields: []string{"title"},
			Changes: []crmPipelineDemandRevisionChange{{
				Field:  "title",
				Before: fixture.job.Title,
				After:  jobTitle,
			}},
		}),
		nil,
	)
}

func TestWorkbenchPipelineUpdateFailuresDoNotAudit(t *testing.T) {
	for _, test := range []struct {
		name      string
		operation WorkbenchOperation
		arguments func(workbenchPipelineFixture) any
		wantCode  WorkbenchErrorCode
	}{
		{
			name:      "candidate stale",
			operation: WorkbenchOperationCandidatesUpdate,
			arguments: func(fixture workbenchPipelineFixture) any {
				owner := "candidate-owner"
				return WorkbenchCandidateUpdateArguments{
					CandidateID: fixture.candidate.ID,
					Update: CandidateUpdate{
						ExpectedVersion: fixture.candidate.Version + 1,
						Owner:           &owner,
					},
				}
			},
			wantCode: WorkbenchErrorConflict,
		},
		{
			name:      "job stale",
			operation: WorkbenchOperationJobsUpdate,
			arguments: func(fixture workbenchPipelineFixture) any {
				title := "Senior Engineer"
				return WorkbenchJobUpdateArguments{
					JobID: fixture.job.ID,
					Update: JobUpdate{
						ExpectedVersion: fixture.job.Version + 1,
						Title:           &title,
					},
				}
			},
			wantCode: WorkbenchErrorConflict,
		},
		{
			name:      "lead stale",
			operation: WorkbenchOperationLeadsUpdate,
			arguments: func(fixture workbenchPipelineFixture) any {
				owner := "lead-owner"
				return WorkbenchLeadUpdateArguments{
					LeadID: fixture.lead.ID,
					Update: LeadUpdate{
						ExpectedVersion: fixture.lead.Version + 1,
						Owner:           &owner,
					},
				}
			},
			wantCode: WorkbenchErrorConflict,
		},
		{
			name:      "candidate empty update",
			operation: WorkbenchOperationCandidatesUpdate,
			arguments: func(fixture workbenchPipelineFixture) any {
				return WorkbenchCandidateUpdateArguments{
					CandidateID: fixture.candidate.ID,
					Update: CandidateUpdate{
						ExpectedVersion: fixture.candidate.Version,
					},
				}
			},
			wantCode: WorkbenchErrorInvalidArguments,
		},
		{
			name:      "job empty update",
			operation: WorkbenchOperationJobsUpdate,
			arguments: func(fixture workbenchPipelineFixture) any {
				return WorkbenchJobUpdateArguments{
					JobID: fixture.job.ID,
					Update: JobUpdate{
						ExpectedVersion: fixture.job.Version,
					},
				}
			},
			wantCode: WorkbenchErrorInvalidArguments,
		},
		{
			name:      "lead empty update",
			operation: WorkbenchOperationLeadsUpdate,
			arguments: func(fixture workbenchPipelineFixture) any {
				return WorkbenchLeadUpdateArguments{
					LeadID: fixture.lead.ID,
					Update: LeadUpdate{
						ExpectedVersion: fixture.lead.Version,
					},
				}
			},
			wantCode: WorkbenchErrorInvalidArguments,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			applyWorkbenchOperationDenySet(map[string]struct{}{})
			defer applyWorkbenchOperationDenySet(map[string]struct{}{})

			fixture := newWorkbenchPipelineFixture(t)
			before := fixture.store.Snapshot()
			result := dispatchWorkbenchPipelineOperation(
				t,
				fixture.dispatcher,
				AutomationToolWorkbenchMutate,
				test.operation,
				test.arguments(fixture),
			)
			assertWorkbenchPipelineFailure(t, result, test.operation, test.wantCode)
			after := fixture.store.Snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected %s mutated CRM state:\nbefore=%+v\nafter=%+v", test.operation, before, after)
			}
		})
	}
}

func TestWorkbenchPipelineDecoderRejectsInvalidArguments(t *testing.T) {
	for _, test := range []struct {
		name          string
		tool          AutomationToolName
		operation     WorkbenchOperation
		argumentsJSON string
	}{
		{
			name:          "candidate update",
			tool:          AutomationToolWorkbenchMutate,
			operation:     WorkbenchOperationCandidatesUpdate,
			argumentsJSON: `{"candidate_id":"bad","update":{"expected_version":1,"owner":"owner"}}`,
		},
		{
			name:          "job update",
			tool:          AutomationToolWorkbenchMutate,
			operation:     WorkbenchOperationJobsUpdate,
			argumentsJSON: `{"job_id":"job_00000001","update":{"expected_version":0,"title":"title"}}`,
		},
		{
			name:          "lead update",
			tool:          AutomationToolWorkbenchMutate,
			operation:     WorkbenchOperationLeadsUpdate,
			argumentsJSON: `{"lead_id":"bad","update":{"expected_version":1,"owner":"owner"}}`,
		},
		{
			name:          "pipeline page",
			tool:          AutomationToolWorkbenchQuery,
			operation:     WorkbenchOperationPipelineGet,
			argumentsJSON: `{"limit":101,"offset":0}`,
		},
		{
			name:          "revision list",
			tool:          AutomationToolWorkbenchQuery,
			operation:     WorkbenchOperationRevisionsList,
			argumentsJSON: `{"job_id":"bad","limit":1,"offset":0}`,
		},
		{
			name:          "revision get",
			tool:          AutomationToolWorkbenchQuery,
			operation:     WorkbenchOperationRevisionsGet,
			argumentsJSON: `{"job_id":"job_00000001","revision_id":"bad"}`,
		},
		{
			name:          "revision compare",
			tool:          AutomationToolWorkbenchQuery,
			operation:     WorkbenchOperationRevisionsCompare,
			argumentsJSON: `{"job_id":"job_00000001","from_revision_id":"bad","to_revision_id":"demand_00000002"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(WorkbenchToolRequest{
				Operation:     test.operation,
				ArgumentsJSON: test.argumentsJSON,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeWorkbenchToolRequest(test.tool, raw); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("DecodeWorkbenchToolRequest(%s/%s) error = %v, want validation", test.tool, test.operation, err)
			}
		})
	}
}

func TestWorkbenchPipelineOperationsRequireCRM(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	owner := "owner"
	title := "title"
	for _, test := range []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
	}{
		{
			name:      "candidate update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationCandidatesUpdate,
			arguments: WorkbenchCandidateUpdateArguments{
				CandidateID: "cand_00000001",
				Update: CandidateUpdate{
					ExpectedVersion: 1,
					Owner:           &owner,
				},
			},
		},
		{
			name:      "job update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationJobsUpdate,
			arguments: WorkbenchJobUpdateArguments{
				JobID: "job_00000001",
				Update: JobUpdate{
					ExpectedVersion: 1,
					Title:           &title,
				},
			},
		},
		{
			name:      "lead update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationLeadsUpdate,
			arguments: WorkbenchLeadUpdateArguments{
				LeadID: "lead_00000001",
				Update: LeadUpdate{
					ExpectedVersion: 1,
					Owner:           &owner,
				},
			},
		},
		{
			name:      "pipeline get",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationPipelineGet,
			arguments: WorkbenchPageArguments{Limit: 1, Offset: 0},
		},
		{
			name:      "revision list",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsList,
			arguments: WorkbenchRevisionListArguments{
				JobID: "job_00000001",
				Limit: 1,
			},
		},
		{
			name:      "revision get",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsGet,
			arguments: WorkbenchRevisionGetArguments{
				JobID:      "job_00000001",
				RevisionID: "demand_00000001",
			},
		},
		{
			name:      "revision compare",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsCompare,
			arguments: WorkbenchRevisionCompareArguments{
				JobID:          "job_00000001",
				FromRevisionID: "demand_00000001",
				ToRevisionID:   "demand_00000002",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := dispatchWorkbenchPipelineOperation(
				t,
				NewDefaultWorkbenchToolDispatcher(nil),
				test.tool,
				test.operation,
				test.arguments,
			)
			assertWorkbenchPipelineFailure(t, result, test.operation, WorkbenchErrorUnavailable)
			if !result.Error.Retryable {
				t.Fatalf("%s unavailable result is not retryable: %+v", test.operation, result)
			}
		})
	}
}

func TestWorkbenchPipelineOperationsMapNotFoundWithoutAudit(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	owner := "owner"
	title := "title"
	for _, test := range []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
	}{
		{
			name:      "candidate update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationCandidatesUpdate,
			arguments: WorkbenchCandidateUpdateArguments{
				CandidateID: "cand_99999999",
				Update: CandidateUpdate{
					ExpectedVersion: 1,
					Owner:           &owner,
				},
			},
		},
		{
			name:      "job update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationJobsUpdate,
			arguments: WorkbenchJobUpdateArguments{
				JobID: "job_99999999",
				Update: JobUpdate{
					ExpectedVersion: 1,
					Title:           &title,
				},
			},
		},
		{
			name:      "lead update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationLeadsUpdate,
			arguments: WorkbenchLeadUpdateArguments{
				LeadID: "lead_99999999",
				Update: LeadUpdate{
					ExpectedVersion: 1,
					Owner:           &owner,
				},
			},
		},
		{
			name:      "revision list",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsList,
			arguments: WorkbenchRevisionListArguments{
				JobID: "job_99999999",
				Limit: 1,
			},
		},
		{
			name:      "revision get",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsGet,
			arguments: WorkbenchRevisionGetArguments{
				JobID:      "job_99999999",
				RevisionID: "demand_99999999",
			},
		},
		{
			name:      "revision compare",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsCompare,
			arguments: WorkbenchRevisionCompareArguments{
				JobID:          "job_99999999",
				FromRevisionID: "demand_99999998",
				ToRevisionID:   "demand_99999999",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := store.Snapshot()
			result := dispatchWorkbenchPipelineOperation(
				t,
				dispatcher,
				test.tool,
				test.operation,
				test.arguments,
			)
			assertWorkbenchPipelineFailure(t, result, test.operation, WorkbenchErrorNotFound)
			after := store.Snapshot()
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("not_found %s mutated CRM state", test.operation)
			}
		})
	}
}

func TestWorkbenchPipelinePaginationIsStableAndBounded(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:             "Pipeline Job",
		Company:           "Example",
		Requirements:      []string{},
		RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]Candidate, 4)
	leads := make([]Lead, 4)
	for i := range candidates {
		candidates[i] = crmPipelineTestCandidate(t, store, CandidateStatusActive)
		leads[i], err = crmPipelineCreateLead(store, LeadCreate{
			CandidateID: candidates[i].ID,
			JobID:       job.ID,
			Owner:       "owner",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	leads[2], err = crmPipelineTransitionLead(store, leads[2].ID, LeadTransition{
		ExpectedVersion: leads[2].Version,
		FromStage:       LeadStageSourced,
		ToStage:         LeadStageScreening,
	})
	if err != nil {
		t.Fatal(err)
	}
	leads[3], err = crmPipelineTransitionLead(store, leads[3].ID, LeadTransition{
		ExpectedVersion: leads[3].Version,
		FromStage:       LeadStageSourced,
		ToStage:         LeadStageScreening,
	})
	if err != nil {
		t.Fatal(err)
	}
	leads[3], err = crmPipelineTransitionLead(store, leads[3].ID, LeadTransition{
		ExpectedVersion: leads[3].Version,
		FromStage:       LeadStageScreening,
		ToStage:         LeadStageInterview,
	})
	if err != nil {
		t.Fatal(err)
	}

	base := time.Now().UTC().Add(time.Hour)
	if err := store.Update(func(state *CRMState, _ *IDAllocator) error {
		for i := range state.Leads {
			switch state.Leads[i].ID {
			case leads[0].ID:
				state.Leads[i].UpdatedAt = base.Add(time.Minute)
			case leads[1].ID:
				state.Leads[i].UpdatedAt = base.Add(2 * time.Minute)
			case leads[2].ID:
				state.Leads[i].UpdatedAt = base.Add(3 * time.Minute)
			case leads[3].ID:
				state.Leads[i].UpdatedAt = base.Add(4 * time.Minute)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	first := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationPipelineGet,
		WorkbenchPageArguments{Limit: 2, Offset: 1},
	)
	second := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationPipelineGet,
		WorkbenchPageArguments{Limit: 2, Offset: 1},
	)
	if !bytes.Equal(first.Data, second.Data) || !reflect.DeepEqual(first.Page, second.Page) {
		t.Fatalf("repeated pipeline page is unstable:\nfirst=%+v\nsecond=%+v", first, second)
	}
	var page workbenchPipelineResponseProjection
	if err := json.Unmarshal(first.Data, &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 4 || len(page.Stages) != len(LeadStages()) {
		t.Fatalf("pipeline page = %+v", page)
	}
	for i, stage := range page.Stages {
		if stage.Stage != LeadStages()[i] || stage.Leads == nil {
			t.Fatalf("stage %d = %+v", i, stage)
		}
	}
	if got, want := workbenchPipelineLeadIDs(page), []string{leads[0].ID, leads[2].ID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paged lead IDs = %v, want %v", got, want)
	}
	if first.Page == nil ||
		first.Page.Limit != 2 ||
		first.Page.Offset != 1 ||
		first.Page.Returned != 2 ||
		first.Page.Total != 4 ||
		!first.Page.HasMore ||
		first.Page.NextOffset != 3 {
		t.Fatalf("pipeline page metadata = %+v", first.Page)
	}

	beyond := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationPipelineGet,
		WorkbenchPageArguments{Limit: 2, Offset: 4},
	)
	var empty workbenchPipelineResponseProjection
	if err := json.Unmarshal(beyond.Data, &empty); err != nil {
		t.Fatal(err)
	}
	if len(workbenchPipelineLeadIDs(empty)) != 0 ||
		len(empty.Stages) != len(LeadStages()) ||
		beyond.Page == nil ||
		beyond.Page.Returned != 0 ||
		beyond.Page.HasMore ||
		beyond.Page.NextOffset != 0 {
		t.Fatalf("pipeline boundary page = data:%+v page:%+v", empty, beyond.Page)
	}
}

func TestWorkbenchPipelineMaximumValidCandidateUpdateResultStaysBoundedAndCommitsOnce(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	candidate := workbenchPipelineCreateMaximumCandidate(t, store, true)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	before := store.Snapshot()
	owner := "bounded-owner"
	arguments := WorkbenchCandidateUpdateArguments{
		CandidateID: candidate.ID,
		Update: CandidateUpdate{
			ExpectedVersion: candidate.Version,
			Owner:           &owner,
		},
	}
	result := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationCandidatesUpdate,
		arguments,
	)
	assertWorkbenchPipelineResultBounded(
		t,
		result,
		WorkbenchOperationCandidatesUpdate,
	)

	after := store.Snapshot()
	updated := workbenchPipelineCandidateByID(t, after, candidate.ID)
	if updated.Owner != owner ||
		updated.Version != candidate.Version+1 ||
		updated.FullName != candidate.FullName ||
		updated.WeChatID != candidate.WeChatID ||
		updated.Phone != candidate.Phone ||
		updated.Email != candidate.Email ||
		updated.Location != candidate.Location ||
		updated.Source != candidate.Source ||
		updated.SourceEventID != candidate.SourceEventID ||
		!reflect.DeepEqual(updated.Skills, candidate.Skills) ||
		updated.Status != candidate.Status ||
		updated.Summary != candidate.Summary ||
		!updated.CreatedAt.Equal(candidate.CreatedAt) ||
		!updated.UpdatedAt.After(candidate.UpdatedAt) {
		t.Fatalf("maximum candidate stored state = %+v", updated)
	}
	if len(after.AuditEvents) != len(before.AuditEvents)+1 ||
		after.Counters.Audit != before.Counters.Audit+1 {
		t.Fatalf(
			"maximum candidate audit delta = events:%d counter:%d",
			len(after.AuditEvents)-len(before.AuditEvents),
			after.Counters.Audit-before.Counters.Audit,
		)
	}

	var projection workbenchPipelineCandidateUpdateProjection
	if err := json.Unmarshal(result.Data, &projection); err != nil {
		t.Fatal(err)
	}
	if want := workbenchPipelineProjectCandidateUpdate(updated); !reflect.DeepEqual(projection, want) {
		t.Fatalf("maximum candidate projection = %+v, want %+v", projection, want)
	}
	if projection.ID != candidate.ID ||
		projection.SourceEventID != candidate.SourceEventID ||
		projection.Status != candidate.Status ||
		projection.Version != candidate.Version+1 ||
		projection.Skills.Total != 32 ||
		len(projection.Skills.Items) != workbenchPipelineListExcerptItems ||
		!projection.Skills.HasMore ||
		!projection.FullName.Truncated ||
		!projection.WeChatID.Truncated ||
		!projection.Email.Truncated ||
		!projection.Location.Truncated ||
		!projection.Source.Truncated ||
		!projection.Summary.Truncated {
		t.Fatalf("maximum candidate projection metadata = %+v", projection)
	}

	replayOwner := "replay-owner"
	arguments.Update.Owner = &replayOwner
	replay := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationCandidatesUpdate,
		arguments,
	)
	assertWorkbenchPipelineFailure(
		t,
		replay,
		WorkbenchOperationCandidatesUpdate,
		WorkbenchErrorConflict,
	)
	if replayed := store.Snapshot(); !reflect.DeepEqual(replayed, after) {
		t.Fatalf("stale maximum candidate replay mutated CRM state")
	}
}

func TestWorkbenchPipelineMaximumEscapedSingleLeadStaysBounded(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	candidate := workbenchPipelineCreateMaximumCandidate(t, store, false)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:             strings.Repeat("<", 160),
		Company:           "Example",
		Requirements:      []string{},
		RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{
		CandidateID: candidate.ID,
		JobID:       job.ID,
		Owner:       strings.Repeat("<", 80),
	})
	if err != nil {
		t.Fatal(err)
	}
	reason := strings.Repeat("<", 2000)
	actor := strings.Repeat("<", 80)
	transitions := []struct {
		from LeadStage
		to   LeadStage
	}{
		{from: LeadStageSourced, to: LeadStageScreening},
		{from: LeadStageScreening, to: LeadStageInterview},
		{from: LeadStageInterview, to: LeadStageOffer},
	}
	for _, transition := range transitions {
		lead, err = crmPipelineTransitionLead(store, lead.ID, LeadTransition{
			ExpectedVersion: lead.Version,
			FromStage:       transition.from,
			ToStage:         transition.to,
			Reason:          reason,
			Actor:           actor,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	first := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationPipelineGet,
		WorkbenchPageArguments{Limit: 1, Offset: 0},
	)
	assertWorkbenchPipelineResultBounded(t, first, WorkbenchOperationPipelineGet)
	repeated := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationPipelineGet,
		WorkbenchPageArguments{Limit: 1, Offset: 0},
	)
	if !bytes.Equal(first.Data, repeated.Data) || !reflect.DeepEqual(first.Page, repeated.Page) {
		t.Fatalf("maximum single-lead pipeline result is unstable")
	}

	var page workbenchPipelineResponseProjection
	if err := json.Unmarshal(first.Data, &page); err != nil {
		t.Fatal(err)
	}
	items := workbenchPipelineProjectedLeads(page)
	if first.Page == nil ||
		first.Page.Limit != 1 ||
		first.Page.Offset != 0 ||
		first.Page.Returned != 1 ||
		first.Page.Total != 1 ||
		first.Page.HasMore ||
		first.Page.NextOffset != 0 ||
		page.Total != 1 ||
		len(items) != 1 {
		t.Fatalf("maximum single-lead page = data:%+v page:%+v", page, first.Page)
	}
	item := items[0]
	if item.Lead.ID != lead.ID ||
		item.Lead.CandidateID != candidate.ID ||
		item.Lead.JobID != job.ID ||
		item.Lead.Stage != LeadStageOffer ||
		item.Lead.Version != lead.Version ||
		!item.CandidateName.Truncated ||
		!item.JobTitle.Truncated ||
		item.StageEvents.Total != len(transitions) ||
		len(item.StageEvents.Items) != len(transitions) ||
		item.StageEvents.HasMore {
		t.Fatalf("maximum single-lead projection = %+v", item)
	}
	for i, event := range item.StageEvents.Items {
		if event.LeadID != lead.ID ||
			event.FromStage != transitions[i].from ||
			event.ToStage != transitions[i].to ||
			!event.Reason.Truncated ||
			event.Actor.Truncated ||
			event.Actor.Excerpt != actor {
			t.Fatalf("maximum stage event %d = %+v", i, event)
		}
	}
}

func TestWorkbenchPipelineAggregateBytePaginationStable(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:             strings.Repeat("<", 160),
		Company:           "Example",
		Requirements:      []string{},
		RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	workbenchPipelineSeedMaximumLeads(t, store, job.ID, 100)
	board, err := crmPipelineBoard(store, "", "")
	if err != nil {
		t.Fatal(err)
	}
	wantLeads := workbenchPipelineDomainLeads(board)
	wantIDs := workbenchPipelineDomainLeadIDs(board)
	if len(wantIDs) != 100 {
		t.Fatalf("maximum pipeline seed IDs = %d, want 100", len(wantIDs))
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	offset := 0
	pageCount := 0
	gotIDs := make([]string, 0, len(wantIDs))
	seen := make(map[string]struct{}, len(wantIDs))
	for {
		result := dispatchWorkbenchPipelineOperation(
			t,
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationPipelineGet,
			WorkbenchPageArguments{Limit: 100, Offset: offset},
		)
		assertWorkbenchPipelineResultBounded(t, result, WorkbenchOperationPipelineGet)
		repeated := dispatchWorkbenchPipelineOperation(
			t,
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationPipelineGet,
			WorkbenchPageArguments{Limit: 100, Offset: offset},
		)
		if !bytes.Equal(result.Data, repeated.Data) ||
			!reflect.DeepEqual(result.Page, repeated.Page) {
			t.Fatalf("maximum pipeline page at offset %d is unstable", offset)
		}

		var page workbenchPipelineResponseProjection
		if err := json.Unmarshal(result.Data, &page); err != nil {
			t.Fatal(err)
		}
		pageIDs := workbenchPipelineLeadIDs(page)
		if result.Page == nil ||
			result.Page.Limit != 100 ||
			result.Page.Offset != offset ||
			result.Page.Total != len(wantIDs) ||
			result.Page.Returned != len(pageIDs) ||
			page.Total != len(wantIDs) ||
			len(page.Stages) != len(LeadStages()) ||
			len(pageIDs) == 0 {
			t.Fatalf("maximum pipeline page at offset %d = data:%+v page:%+v", offset, page, result.Page)
		}
		for i, stage := range page.Stages {
			if stage.Stage != LeadStages()[i] || stage.Leads == nil {
				t.Fatalf("maximum pipeline stage %d = %+v", i, stage)
			}
		}
		for _, id := range pageIDs {
			if _, duplicate := seen[id]; duplicate {
				t.Fatalf("maximum pipeline duplicate lead %s", id)
			}
			seen[id] = struct{}{}
			gotIDs = append(gotIDs, id)
		}
		if pageCount == 0 {
			if result.Page.Returned >= result.Page.Limit || !result.Page.HasMore {
				t.Fatalf("maximum pipeline first page did not hit byte budget: %+v", result.Page)
			}
			firstItem := workbenchPipelineProjectedLeads(page)[0]
			if !firstItem.CandidateName.Truncated ||
				!firstItem.JobTitle.Truncated ||
				firstItem.StageEvents.Total != 3 ||
				len(firstItem.StageEvents.Items) != 3 ||
				firstItem.StageEvents.HasMore {
				t.Fatalf("maximum pipeline first item = %+v", firstItem)
			}
			expanded := workbenchPipelineCloneProjectedResponse(page)
			next := wantLeads[result.Page.NextOffset]
			for stageIndex := range expanded.Stages {
				if expanded.Stages[stageIndex].Stage == next.Lead.Stage {
					expanded.Stages[stageIndex].Leads = append(
						expanded.Stages[stageIndex].Leads,
						workbenchPipelineProjectLead(next),
					)
					break
				}
			}
			oversized := NewWorkbenchToolSuccess(
				WorkbenchOperationPipelineGet,
				expanded,
				newWorkbenchPage(
					100,
					0,
					len(wantLeads),
					result.Page.Returned+1,
				),
			)
			if oversized.OK ||
				oversized.Error == nil ||
				oversized.Error.Code != WorkbenchErrorResultTooLarge {
				t.Fatalf("maximum pipeline accepted the first over-budget lead: %+v", oversized)
			}
		}
		pageCount++
		if !result.Page.HasMore {
			if result.Page.NextOffset != 0 {
				t.Fatalf("terminal maximum pipeline next_offset = %d", result.Page.NextOffset)
			}
			break
		}
		if result.Page.NextOffset != offset+len(pageIDs) ||
			result.Page.NextOffset <= offset {
			t.Fatalf("maximum pipeline page did not advance: %+v", result.Page)
		}
		offset = result.Page.NextOffset
	}
	if pageCount < 2 ||
		len(seen) != len(wantIDs) ||
		!reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf(
			"maximum pipeline pages=%d IDs=%v want=%v",
			pageCount,
			gotIDs,
			wantIDs,
		)
	}
}

func TestWorkbenchPipelineRevisionOrderingComparisonAndMisses(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:             "Revision One",
		Company:           "Example",
		Headcount:         1,
		Requirements:      []string{"Go"},
		RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	title := "Revision Two"
	job, err = crmPipelineUpdateJob(store, job.ID, JobUpdate{
		ExpectedVersion: job.Version,
		Title:           &title,
	})
	if err != nil {
		t.Fatal(err)
	}
	headcount := 3
	job, err = crmPipelineUpdateJob(store, job.ID, JobUpdate{
		ExpectedVersion: job.Version,
		Headcount:       &headcount,
	})
	if err != nil {
		t.Fatal(err)
	}
	revisions := workbenchPipelineRevisionsForJob(store.Snapshot(), job.ID)
	if got := workbenchPipelineRevisionNumbers(revisions); !reflect.DeepEqual(got, []uint64{3, 2, 1}) {
		t.Fatalf("seed revision order = %v", got)
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	list := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsList,
		WorkbenchRevisionListArguments{JobID: job.ID, Limit: 2, Offset: 1},
	)
	assertWorkbenchPipelineSuccess(
		t,
		list,
		WorkbenchOperationRevisionsList,
		workbenchPipelineRevisionListResult{
			Revisions: workbenchPipelineProjectRevisionListItems(revisions[1:]),
		},
		&WorkbenchPage{Limit: 2, Offset: 1, Returned: 2, Total: 3, HasMore: false},
	)

	get := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsGet,
		WorkbenchRevisionGetArguments{JobID: job.ID, RevisionID: revisions[1].ID},
	)
	assertWorkbenchPipelineSuccess(
		t,
		get,
		WorkbenchOperationRevisionsGet,
		workbenchPipelineProjectRevisionDetail(revisions[1]),
		nil,
	)

	compare := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsCompare,
		WorkbenchRevisionCompareArguments{
			JobID:          job.ID,
			FromRevisionID: revisions[2].ID,
			ToRevisionID:   revisions[0].ID,
		},
	)
	assertWorkbenchPipelineSuccess(
		t,
		compare,
		WorkbenchOperationRevisionsCompare,
		workbenchPipelineProjectRevisionComparison(crmPipelineDemandRevisionComparison{
			JobID:         job.ID,
			From:          revisions[2],
			To:            revisions[0],
			ChangedFields: []string{"title", "headcount"},
			Changes: []crmPipelineDemandRevisionChange{
				{Field: "title", Before: "Revision One", After: "Revision Two"},
				{Field: "headcount", Before: 1, After: 3},
			},
		}),
		nil,
	)

	for _, test := range []struct {
		name      string
		operation WorkbenchOperation
		arguments any
	}{
		{
			name:      "list missing job",
			operation: WorkbenchOperationRevisionsList,
			arguments: WorkbenchRevisionListArguments{
				JobID: "job_99999999",
				Limit: 1,
			},
		},
		{
			name:      "get missing revision",
			operation: WorkbenchOperationRevisionsGet,
			arguments: WorkbenchRevisionGetArguments{
				JobID:      job.ID,
				RevisionID: "demand_99999999",
			},
		},
		{
			name:      "compare missing from",
			operation: WorkbenchOperationRevisionsCompare,
			arguments: WorkbenchRevisionCompareArguments{
				JobID:          job.ID,
				FromRevisionID: "demand_99999999",
				ToRevisionID:   revisions[0].ID,
			},
		},
		{
			name:      "compare missing to",
			operation: WorkbenchOperationRevisionsCompare,
			arguments: WorkbenchRevisionCompareArguments{
				JobID:          job.ID,
				FromRevisionID: revisions[2].ID,
				ToRevisionID:   "demand_99999999",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := store.Snapshot()
			result := dispatchWorkbenchPipelineOperation(
				t,
				dispatcher,
				AutomationToolWorkbenchQuery,
				test.operation,
				test.arguments,
			)
			assertWorkbenchPipelineFailure(t, result, test.operation, WorkbenchErrorNotFound)
			if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s mutated CRM on not_found", test.operation)
			}
		})
	}
}

func TestWorkbenchPipelineMaximumValidJobAndRevisionResultsStayBounded(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	store := crmPipelineTestStore(t)
	startAt := time.Date(2026, time.January, 1, 8, 0, 0, 0, time.UTC)
	deadlineAt := startAt.Add(24 * time.Hour)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:                 strings.Repeat("<", 160),
		Company:               strings.Repeat("<", 160),
		Location:              strings.Repeat("<", 160),
		EmploymentType:        strings.Repeat("<", 80),
		Description:           strings.Repeat("<", 8000),
		Requirements:          workbenchPipelineMaximumStringList(64, 1000, "<"),
		DemandCode:            strings.Repeat("<", 120),
		SiteName:              strings.Repeat("<", 160),
		OnsiteContact:         strings.Repeat("<", 160),
		Headcount:             1,
		Priority:              JobPriorityNormal,
		StartAt:               &startAt,
		DeadlineAt:            &deadlineAt,
		Shift:                 strings.Repeat("<", 160),
		WorkSchedule:          strings.Repeat("<", 2000),
		CompensationSummary:   strings.Repeat("<", 2000),
		Accommodation:         strings.Repeat("<", 2000),
		TransportRequirements: strings.Repeat("<", 2000),
		RequiredDocuments:     workbenchPipelineMaximumStringList(64, 200, "<"),
		Owner:                 strings.Repeat("<", 80),
		Status:                JobStatusOpen,
	})
	if err != nil {
		t.Fatal(err)
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	before := store.Snapshot()
	updatedTitle := strings.Repeat(">", 160)
	updateResult := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationJobsUpdate,
		WorkbenchJobUpdateArguments{
			JobID: job.ID,
			Update: JobUpdate{
				ExpectedVersion: job.Version,
				Title:           &updatedTitle,
			},
		},
	)
	assertWorkbenchPipelineResultBounded(
		t,
		updateResult,
		WorkbenchOperationJobsUpdate,
	)
	after := store.Snapshot()
	updatedJob := workbenchPipelineJobByID(t, after, job.ID)
	if updatedJob.Version != job.Version+1 ||
		updatedJob.DemandRevisionNumber != job.DemandRevisionNumber+1 ||
		len(after.AuditEvents) != len(before.AuditEvents)+1 ||
		len(after.DemandRevisions) != len(before.DemandRevisions)+1 {
		t.Fatalf(
			"maximum job update state = version:%d revision:%d audits:%d revisions:%d",
			updatedJob.Version,
			updatedJob.DemandRevisionNumber,
			len(after.AuditEvents),
			len(after.DemandRevisions),
		)
	}
	var updateProjection workbenchPipelineJobUpdateProjection
	if err := json.Unmarshal(updateResult.Data, &updateProjection); err != nil {
		t.Fatal(err)
	}
	if updateProjection.ID != updatedJob.ID ||
		updateProjection.Version != updatedJob.Version ||
		updateProjection.Status != updatedJob.Status ||
		updateProjection.ActiveDemandRevisionID != updatedJob.ActiveDemandRevisionID ||
		updateProjection.DemandRevisionNumber != updatedJob.DemandRevisionNumber ||
		!updateProjection.Snapshot.Description.Truncated ||
		updateProjection.Snapshot.Requirements.Total != 64 ||
		len(updateProjection.Snapshot.Requirements.Items) != workbenchPipelineListExcerptItems ||
		!updateProjection.Snapshot.Requirements.HasMore ||
		updateProjection.Snapshot.RequiredDocuments.Total != 64 ||
		len(updateProjection.Snapshot.RequiredDocuments.Items) != workbenchPipelineListExcerptItems ||
		!updateProjection.Snapshot.RequiredDocuments.HasMore {
		t.Fatalf("maximum job projection = %+v", updateProjection)
	}

	company := strings.Repeat(">", 160)
	location := strings.Repeat(">", 160)
	employmentType := strings.Repeat(">", 80)
	description := strings.Repeat(">", 8000)
	requirements := workbenchPipelineMaximumStringList(64, 1000, ">")
	demandCode := strings.Repeat(">", 120)
	siteName := strings.Repeat(">", 160)
	onsiteContact := strings.Repeat(">", 160)
	headcount := 1000000
	priority := JobPriorityUrgent
	nextStartAt := startAt.Add(31 * 24 * time.Hour)
	nextDeadlineAt := nextStartAt.Add(24 * time.Hour)
	shift := strings.Repeat(">", 160)
	workSchedule := strings.Repeat(">", 2000)
	compensation := strings.Repeat(">", 2000)
	accommodation := strings.Repeat(">", 2000)
	transport := strings.Repeat(">", 2000)
	documents := workbenchPipelineMaximumStringList(64, 200, ">")
	updatedJob, err = crmPipelineUpdateJob(store, job.ID, JobUpdate{
		ExpectedVersion:       updatedJob.Version,
		Company:               &company,
		Location:              &location,
		EmploymentType:        &employmentType,
		Description:           &description,
		Requirements:          &requirements,
		DemandCode:            &demandCode,
		SiteName:              &siteName,
		OnsiteContact:         &onsiteContact,
		Headcount:             &headcount,
		Priority:              &priority,
		StartAt:               &nextStartAt,
		DeadlineAt:            &nextDeadlineAt,
		Shift:                 &shift,
		WorkSchedule:          &workSchedule,
		CompensationSummary:   &compensation,
		Accommodation:         &accommodation,
		TransportRequirements: &transport,
		RequiredDocuments:     &documents,
	})
	if err != nil {
		t.Fatal(err)
	}
	maximumRevisions := workbenchPipelineRevisionsForJob(store.Snapshot(), job.ID)
	if got := workbenchPipelineRevisionNumbers(maximumRevisions); !reflect.DeepEqual(got, []uint64{3, 2, 1}) {
		t.Fatalf("maximum revision numbers = %v", got)
	}

	getResult := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsGet,
		WorkbenchRevisionGetArguments{
			JobID:      job.ID,
			RevisionID: maximumRevisions[0].ID,
		},
	)
	assertWorkbenchPipelineResultBounded(
		t,
		getResult,
		WorkbenchOperationRevisionsGet,
	)
	var detail workbenchPipelineRevisionDetailProjection
	if err := json.Unmarshal(getResult.Data, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != maximumRevisions[0].ID ||
		detail.JobID != job.ID ||
		detail.Number != 3 ||
		!detail.Snapshot.Description.Truncated ||
		detail.Snapshot.Requirements.Total != 64 ||
		detail.Snapshot.RequiredDocuments.Total != 64 {
		t.Fatalf("maximum revision detail = %+v", detail)
	}

	compareResult := dispatchWorkbenchPipelineOperation(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationRevisionsCompare,
		WorkbenchRevisionCompareArguments{
			JobID:          job.ID,
			FromRevisionID: maximumRevisions[2].ID,
			ToRevisionID:   maximumRevisions[0].ID,
		},
	)
	assertWorkbenchPipelineResultBounded(
		t,
		compareResult,
		WorkbenchOperationRevisionsCompare,
	)
	var comparison workbenchPipelineRevisionComparisonProjection
	if err := json.Unmarshal(compareResult.Data, &comparison); err != nil {
		t.Fatal(err)
	}
	wantChangedFields := []string{
		"title",
		"company",
		"location",
		"employment_type",
		"description",
		"requirements",
		"demand_code",
		"site_name",
		"onsite_contact",
		"headcount",
		"priority",
		"start_at",
		"deadline_at",
		"shift",
		"work_schedule",
		"compensation_summary",
		"accommodation",
		"transport_requirements",
		"required_documents",
	}
	if comparison.JobID != job.ID ||
		comparison.From.ID != maximumRevisions[2].ID ||
		comparison.To.ID != maximumRevisions[0].ID ||
		!reflect.DeepEqual(comparison.ChangedFields, wantChangedFields) ||
		len(comparison.Changes) != len(wantChangedFields) {
		t.Fatalf("maximum revision comparison = %+v", comparison)
	}
	for _, change := range comparison.Changes {
		switch change.Field {
		case "description":
			if !change.Before.Truncated || !change.After.Truncated {
				t.Fatalf("description comparison is not truncated: %+v", change)
			}
		case "requirements", "required_documents":
			if change.Before.Type != "string_list" ||
				change.After.Type != "string_list" ||
				change.Before.ItemCount != 64 ||
				change.After.ItemCount != 64 ||
				!change.Before.Truncated ||
				!change.After.Truncated {
				t.Fatalf("%s comparison projection = %+v", change.Field, change)
			}
		}
	}

	if err := store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "maximum-test",
		Operation:  "demand-revision.seed",
		EntityType: "job",
		EntityID:   job.ID,
	}, func(state *CRMState, allocator *IDAllocator) error {
		jobIndex := crmPipelineJobIndex(state.Jobs, job.ID)
		if jobIndex < 0 {
			return &crmPipelineNotFoundError{resource: "job"}
		}
		createdAt := state.Jobs[jobIndex].UpdatedAt.Truncate(time.Second).Add(123456789)
		if !createdAt.After(state.Jobs[jobIndex].UpdatedAt) {
			createdAt = createdAt.Add(time.Second)
		}
		state.Jobs[jobIndex].UpdatedAt = createdAt
		historical := JobDemandSnapshot{
			Title:             "0123456789",
			Company:           "Example",
			Requirements:      workbenchPipelineMaximumStringList(64, 10, "r"),
			Priority:          JobPriorityNormal,
			RequiredDocuments: workbenchPipelineMaximumStringList(64, 10, "d"),
		}
		for number := uint64(99999903); number <= 99999999; number++ {
			id, nextErr := allocator.Next(IDKindDemandRevision)
			if nextErr != nil {
				return nextErr
			}
			state.DemandRevisions = append(state.DemandRevisions, DemandRevision{
				ID:        id,
				JobID:     job.ID,
				Number:    number,
				Snapshot:  historical,
				CreatedBy: "abcdefghij",
				Reason:    "klmnopqrst",
				CreatedAt: createdAt,
			})
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	wantRevisions := workbenchPipelineRevisionsForJob(store.Snapshot(), job.ID)
	wantRevisionNumbers := workbenchPipelineRevisionNumbers(wantRevisions)
	offset := 0
	pageCount := 0
	returnedRevisionNumbers := make([]uint64, 0, len(wantRevisionNumbers))
	var maximumListItem *workbenchPipelineRevisionListItemProjection
	for {
		listResult := dispatchWorkbenchPipelineOperation(
			t,
			dispatcher,
			AutomationToolWorkbenchQuery,
			WorkbenchOperationRevisionsList,
			WorkbenchRevisionListArguments{JobID: job.ID, Limit: 100, Offset: offset},
		)
		assertWorkbenchPipelineResultBounded(
			t,
			listResult,
			WorkbenchOperationRevisionsList,
		)
		var list workbenchPipelineRevisionListResult
		if err := json.Unmarshal(listResult.Data, &list); err != nil {
			t.Fatal(err)
		}
		if listResult.Page == nil ||
			listResult.Page.Limit != 100 ||
			listResult.Page.Offset != offset ||
			listResult.Page.Returned != len(list.Revisions) ||
			listResult.Page.Total != 100 ||
			len(list.Revisions) == 0 {
			t.Fatalf("maximum revision list page = data:%+v page:%+v", list, listResult.Page)
		}
		if pageCount == 0 {
			repeated := dispatchWorkbenchPipelineOperation(
				t,
				dispatcher,
				AutomationToolWorkbenchQuery,
				WorkbenchOperationRevisionsList,
				WorkbenchRevisionListArguments{JobID: job.ID, Limit: 100, Offset: 0},
			)
			if !bytes.Equal(repeated.Data, listResult.Data) ||
				!reflect.DeepEqual(repeated.Page, listResult.Page) {
				t.Fatalf("maximum revision list first page is unstable")
			}
			if listResult.Page.Returned >= 100 ||
				!listResult.Page.HasMore ||
				listResult.Page.NextOffset != listResult.Page.Returned ||
				list.Revisions[0].Number != 99999999 ||
				list.Revisions[0].CreatedByExcerpt != "abcdefghij" ||
				list.Revisions[0].CreatedByTruncated ||
				list.Revisions[0].ReasonExcerpt != "klmnopqrst" ||
				list.Revisions[0].ReasonTruncated ||
				list.Revisions[0].TitleExcerpt != "0123456789" ||
				list.Revisions[0].TitleTruncated ||
				list.Revisions[0].CreatedAt.Nanosecond() != 123456789 ||
				list.Revisions[0].RequirementsCount != 64 ||
				list.Revisions[0].RequiredDocumentsCount != 64 {
				t.Fatalf("maximum revision list first page = data:%+v page:%+v", list, listResult.Page)
			}
			withNext := append(
				append([]workbenchPipelineRevisionListItemProjection{}, list.Revisions...),
				workbenchPipelineProjectRevisionListItem(
					wantRevisions[listResult.Page.NextOffset],
				),
			)
			oversized := NewWorkbenchToolSuccess(
				WorkbenchOperationRevisionsList,
				workbenchPipelineRevisionListProjection{Revisions: withNext},
				newWorkbenchPage(100, 0, len(wantRevisions), len(withNext)),
			)
			if oversized.OK ||
				oversized.Error == nil ||
				oversized.Error.Code != WorkbenchErrorResultTooLarge {
				t.Fatalf("revision list accepted the first over-budget item: %+v", oversized)
			}
		}
		for i := range list.Revisions {
			returnedRevisionNumbers = append(returnedRevisionNumbers, list.Revisions[i].Number)
			if list.Revisions[i].Number == 3 {
				maximumListItem = &list.Revisions[i]
			}
		}
		pageCount++
		if !listResult.Page.HasMore {
			if listResult.Page.NextOffset != 0 {
				t.Fatalf("terminal revision page next_offset = %d", listResult.Page.NextOffset)
			}
			break
		}
		if listResult.Page.NextOffset != offset+len(list.Revisions) ||
			listResult.Page.NextOffset <= offset {
			t.Fatalf("revision list did not advance: %+v", listResult.Page)
		}
		offset = listResult.Page.NextOffset
	}
	if pageCount < 2 ||
		!reflect.DeepEqual(returnedRevisionNumbers, wantRevisionNumbers) ||
		maximumListItem == nil ||
		maximumListItem.RequirementsCount != 64 ||
		maximumListItem.RequiredDocumentsCount != 64 {
		t.Fatalf(
			"maximum revision pages=%d numbers=%v want=%v maximum=%+v",
			pageCount,
			returnedRevisionNumbers,
			wantRevisionNumbers,
			maximumListItem,
		)
	}
}

func TestWorkbenchPipelineConcurrentSameVersionDispatcherUpdates(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	tests := []struct {
		name              string
		operation         WorkbenchOperation
		arguments         func(workbenchPipelineFixture) []any
		initialVersion    func(workbenchPipelineFixture) uint64
		storedVersion     func(*testing.T, CRMState, workbenchPipelineFixture) uint64
		wantRevisionDelta int
	}{
		{
			name:      "candidate",
			operation: WorkbenchOperationCandidatesUpdate,
			arguments: func(fixture workbenchPipelineFixture) []any {
				alice := "alice"
				bob := "bob"
				return []any{
					WorkbenchCandidateUpdateArguments{
						CandidateID: fixture.candidate.ID,
						Update: CandidateUpdate{
							ExpectedVersion: fixture.candidate.Version,
							Owner:           &alice,
						},
					},
					WorkbenchCandidateUpdateArguments{
						CandidateID: fixture.candidate.ID,
						Update: CandidateUpdate{
							ExpectedVersion: fixture.candidate.Version,
							Owner:           &bob,
						},
					},
				}
			},
			initialVersion: func(fixture workbenchPipelineFixture) uint64 {
				return fixture.candidate.Version
			},
			storedVersion: func(t *testing.T, state CRMState, fixture workbenchPipelineFixture) uint64 {
				return workbenchPipelineCandidateByID(t, state, fixture.candidate.ID).Version
			},
		},
		{
			name:      "job",
			operation: WorkbenchOperationJobsUpdate,
			arguments: func(fixture workbenchPipelineFixture) []any {
				first := "First"
				second := "Second"
				return []any{
					WorkbenchJobUpdateArguments{
						JobID: fixture.job.ID,
						Update: JobUpdate{
							ExpectedVersion: fixture.job.Version,
							Title:           &first,
						},
					},
					WorkbenchJobUpdateArguments{
						JobID: fixture.job.ID,
						Update: JobUpdate{
							ExpectedVersion: fixture.job.Version,
							Title:           &second,
						},
					},
				}
			},
			initialVersion: func(fixture workbenchPipelineFixture) uint64 {
				return fixture.job.Version
			},
			storedVersion: func(t *testing.T, state CRMState, fixture workbenchPipelineFixture) uint64 {
				return workbenchPipelineJobByID(t, state, fixture.job.ID).Version
			},
			wantRevisionDelta: 1,
		},
		{
			name:      "lead",
			operation: WorkbenchOperationLeadsUpdate,
			arguments: func(fixture workbenchPipelineFixture) []any {
				alice := "alice"
				bob := "bob"
				return []any{
					WorkbenchLeadUpdateArguments{
						LeadID: fixture.lead.ID,
						Update: LeadUpdate{
							ExpectedVersion: fixture.lead.Version,
							Owner:           &alice,
						},
					},
					WorkbenchLeadUpdateArguments{
						LeadID: fixture.lead.ID,
						Update: LeadUpdate{
							ExpectedVersion: fixture.lead.Version,
							Owner:           &bob,
						},
					},
				}
			},
			initialVersion: func(fixture workbenchPipelineFixture) uint64 {
				return fixture.lead.Version
			},
			storedVersion: func(t *testing.T, state CRMState, fixture workbenchPipelineFixture) uint64 {
				return workbenchPipelineLeadByID(t, state, fixture.lead.ID).Version
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newWorkbenchPipelineFixture(t)
			before := fixture.store.Snapshot()
			arguments := test.arguments(fixture)
			decoded := make([]WorkbenchDecodedToolRequest, len(arguments))
			for i := range arguments {
				decoded[i] = decodeWorkbenchPipelineOperation(
					t,
					AutomationToolWorkbenchMutate,
					test.operation,
					arguments[i],
				)
			}

			start := make(chan struct{})
			results := make(chan WorkbenchToolResult, len(decoded))
			var wait sync.WaitGroup
			for i := range decoded {
				wait.Add(1)
				go func(request WorkbenchDecodedToolRequest) {
					defer wait.Done()
					<-start
					results <- fixture.dispatcher.Dispatch(
						context.Background(),
						WorkbenchExecutionContext{},
						request,
					)
				}(decoded[i])
			}
			close(start)
			wait.Wait()
			close(results)

			successes := make([]WorkbenchToolResult, 0, 1)
			conflicts := 0
			for result := range results {
				switch {
				case result.OK:
					successes = append(successes, result)
				case result.Error != nil && result.Error.Code == WorkbenchErrorConflict:
					conflicts++
					assertWorkbenchPipelineFailure(
						t,
						result,
						test.operation,
						WorkbenchErrorConflict,
					)
				default:
					t.Fatalf("concurrent %s result = %+v", test.operation, result)
				}
			}
			if len(successes) != 1 || conflicts != 1 {
				t.Fatalf(
					"concurrent %s successes=%d conflicts=%d",
					test.operation,
					len(successes),
					conflicts,
				)
			}
			assertWorkbenchPipelineResultBounded(t, successes[0], test.operation)
			var returned struct {
				Version uint64 `json:"version"`
			}
			if err := json.Unmarshal(successes[0].Data, &returned); err != nil {
				t.Fatal(err)
			}

			after := fixture.store.Snapshot()
			initialVersion := test.initialVersion(fixture)
			if storedVersion := test.storedVersion(t, after, fixture); storedVersion != initialVersion+1 {
				t.Fatalf(
					"concurrent %s stored version=%d, want %d",
					test.operation,
					storedVersion,
					initialVersion+1,
				)
			}
			if returned.Version != initialVersion+1 {
				t.Fatalf(
					"concurrent %s returned version=%d, want %d",
					test.operation,
					returned.Version,
					initialVersion+1,
				)
			}
			if len(after.AuditEvents) != len(before.AuditEvents)+1 ||
				after.Counters.Audit != before.Counters.Audit+1 {
				t.Fatalf(
					"concurrent %s audit delta = events:%d counter:%d",
					test.operation,
					len(after.AuditEvents)-len(before.AuditEvents),
					after.Counters.Audit-before.Counters.Audit,
				)
			}
			if delta := len(after.DemandRevisions) - len(before.DemandRevisions); delta != test.wantRevisionDelta {
				t.Fatalf(
					"concurrent %s revision delta=%d, want %d",
					test.operation,
					delta,
					test.wantRevisionDelta,
				)
			}
		})
	}
}

func TestWorkbenchPipelinePermissionDenyPreventsWrites(t *testing.T) {
	fixture := newWorkbenchPipelineFixture(t)
	revision := workbenchPipelineRevisionsForJob(fixture.store.Snapshot(), fixture.job.ID)[0]
	owner := "denied-owner"
	title := "Denied Title"
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
	}{
		{
			name:      "candidate update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationCandidatesUpdate,
			arguments: WorkbenchCandidateUpdateArguments{
				CandidateID: fixture.candidate.ID,
				Update: CandidateUpdate{
					ExpectedVersion: fixture.candidate.Version,
					Owner:           &owner,
				},
			},
		},
		{
			name:      "job update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationJobsUpdate,
			arguments: WorkbenchJobUpdateArguments{
				JobID: fixture.job.ID,
				Update: JobUpdate{
					ExpectedVersion: fixture.job.Version,
					Title:           &title,
				},
			},
		},
		{
			name:      "lead update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationLeadsUpdate,
			arguments: WorkbenchLeadUpdateArguments{
				LeadID: fixture.lead.ID,
				Update: LeadUpdate{
					ExpectedVersion: fixture.lead.Version,
					Owner:           &owner,
				},
			},
		},
		{
			name:      "pipeline get",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationPipelineGet,
			arguments: WorkbenchPageArguments{Limit: 1},
		},
		{
			name:      "revision list",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsList,
			arguments: WorkbenchRevisionListArguments{
				JobID: fixture.job.ID,
				Limit: 1,
			},
		},
		{
			name:      "revision get",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsGet,
			arguments: WorkbenchRevisionGetArguments{
				JobID:      fixture.job.ID,
				RevisionID: revision.ID,
			},
		},
		{
			name:      "revision compare",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationRevisionsCompare,
			arguments: WorkbenchRevisionCompareArguments{
				JobID:          fixture.job.ID,
				FromRevisionID: revision.ID,
				ToRevisionID:   revision.ID,
			},
		},
	}

	denied := make(map[string]struct{}, len(tests))
	for _, test := range tests {
		denied[workbenchOperationDenyKey(test.tool, test.operation)] = struct{}{}
	}
	applyWorkbenchOperationDenySet(denied)
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	before := fixture.store.Snapshot()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry, ok := WorkbenchOperationCatalogEntryFor(test.tool, test.operation)
			if !ok ||
				entry.Availability != WorkbenchAvailabilityUnavailable ||
				len(entry.ValidDecisions) != 0 {
				t.Fatalf("denied catalog entry %s/%s = %+v, found=%t", test.tool, test.operation, entry, ok)
			}
			result := dispatchWorkbenchPipelineOperation(
				t,
				fixture.dispatcher,
				test.tool,
				test.operation,
				test.arguments,
			)
			assertWorkbenchPipelineFailure(t, result, test.operation, WorkbenchErrorUnavailable)
		})
	}
	if after := fixture.store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("permission-denied operations mutated CRM:\nbefore=%+v\nafter=%+v", before, after)
	}
}

func newWorkbenchPipelineFixture(t *testing.T) workbenchPipelineFixture {
	t.Helper()
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:             "Engineer",
		Company:           "Example",
		Requirements:      []string{"Go"},
		RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{
		CandidateID: candidate.ID,
		JobID:       job.ID,
		Owner:       "recruiter",
	})
	if err != nil {
		t.Fatal(err)
	}
	return workbenchPipelineFixture{
		store:      store,
		dispatcher: NewDefaultWorkbenchToolDispatcher(store),
		candidate:  candidate,
		job:        job,
		lead:       lead,
	}
}

func dispatchWorkbenchPipelineOperation(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	decoded := decodeWorkbenchPipelineOperation(t, tool, operation, arguments)
	return dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
}

func decodeWorkbenchPipelineOperation(
	t *testing.T,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchDecodedToolRequest {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(tool, raw)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", tool, operation, err)
	}
	return decoded
}

func assertWorkbenchPipelineSuccess(
	t *testing.T,
	result WorkbenchToolResult,
	operation WorkbenchOperation,
	wantData any,
	wantPage *WorkbenchPage,
) {
	t.Helper()
	if !result.OK ||
		result.Operation != operation ||
		result.Error != nil ||
		!reflect.DeepEqual(result.Page, wantPage) {
		t.Fatalf("%s result = %+v, want success page %+v", operation, result, wantPage)
	}
	wantJSON, err := json.Marshal(wantData)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result.Data, wantJSON) {
		t.Fatalf("%s data = %s, want %s", operation, result.Data, wantJSON)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolResult(encoded)
	if err != nil {
		t.Fatalf("DecodeWorkbenchToolResult(%s) error = %v", operation, err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("%s result round trip = %+v, want %+v", operation, decoded, result)
	}
}

func assertWorkbenchPipelineResultBounded(
	t *testing.T,
	result WorkbenchToolResult,
	operation WorkbenchOperation,
) {
	t.Helper()
	if !result.OK ||
		result.Operation != operation ||
		result.Error != nil {
		t.Fatalf("%s result = %+v, want bounded success", operation, result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		t.Fatalf(
			"%s result size=%d, want at most %d",
			operation,
			len(encoded),
			WorkbenchMaxResultJSONBytes,
		)
	}
	decoded, err := DecodeWorkbenchToolResult(encoded)
	if err != nil {
		t.Fatalf("DecodeWorkbenchToolResult(%s) error = %v", operation, err)
	}
	if !reflect.DeepEqual(decoded, result) {
		t.Fatalf("%s bounded result round trip = %+v, want %+v", operation, decoded, result)
	}
}

func assertWorkbenchPipelineFailure(
	t *testing.T,
	result WorkbenchToolResult,
	operation WorkbenchOperation,
	code WorkbenchErrorCode,
) {
	t.Helper()
	if result.OK ||
		result.Operation != operation ||
		result.Error == nil ||
		result.Error.Code != code ||
		len(result.Data) != 0 ||
		result.Page != nil {
		t.Fatalf("%s result = %+v, want failure %s", operation, result, code)
	}
}

func assertWorkbenchPipelineAuditDelta(t *testing.T, state CRMState, before, delta int) {
	t.Helper()
	if len(state.AuditEvents) != before+delta ||
		state.Counters.Audit != uint64(before+delta) {
		t.Fatalf(
			"audit state = events:%d counter:%d, want events/counter %d",
			len(state.AuditEvents),
			state.Counters.Audit,
			before+delta,
		)
	}
}

func workbenchPipelineCandidateByID(t *testing.T, state CRMState, id string) Candidate {
	t.Helper()
	for _, candidate := range state.Candidates {
		if candidate.ID == id {
			return candidate
		}
	}
	t.Fatalf("candidate %s not found", id)
	return Candidate{}
}

func workbenchPipelineJobByID(t *testing.T, state CRMState, id string) Job {
	t.Helper()
	for _, job := range state.Jobs {
		if job.ID == id {
			return job
		}
	}
	t.Fatalf("job %s not found", id)
	return Job{}
}

func workbenchPipelineLeadByID(t *testing.T, state CRMState, id string) Lead {
	t.Helper()
	for _, lead := range state.Leads {
		if lead.ID == id {
			return lead
		}
	}
	t.Fatalf("lead %s not found", id)
	return Lead{}
}

func workbenchPipelineRevisionsForJob(state CRMState, jobID string) []DemandRevision {
	revisions := make([]DemandRevision, 0)
	for _, revision := range state.DemandRevisions {
		if revision.JobID == jobID {
			revisions = append(revisions, revision)
		}
	}
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].Number == revisions[j].Number {
			return revisions[i].ID < revisions[j].ID
		}
		return revisions[i].Number > revisions[j].Number
	})
	return revisions
}

func workbenchPipelineRevisionNumbers(revisions []DemandRevision) []uint64 {
	numbers := make([]uint64, len(revisions))
	for i, revision := range revisions {
		numbers[i] = revision.Number
	}
	return numbers
}

func workbenchPipelineProjectRevisionListItems(
	revisions []DemandRevision,
) []workbenchPipelineRevisionListItemProjection {
	projected := make([]workbenchPipelineRevisionListItemProjection, len(revisions))
	for i := range revisions {
		projected[i] = workbenchPipelineProjectRevisionListItem(revisions[i])
	}
	return projected
}

func workbenchPipelineMaximumStringList(count, maxRunes int, fill string) []string {
	values := make([]string, count)
	for i := range values {
		suffix := fmt.Sprintf("%02d", i)
		values[i] = strings.Repeat(fill, maxRunes-len(suffix)) + suffix
	}
	return values
}

func workbenchPipelineCreateMaximumCandidate(
	t *testing.T,
	store *CRMStore,
	withSourceEvent bool,
) Candidate {
	t.Helper()
	var candidate Candidate
	if err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		candidateID, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		candidate = Candidate{
			ID:        candidateID,
			FullName:  strings.Repeat("<", 120),
			WeChatID:  strings.Repeat("<", 128),
			Phone:     strings.Repeat("<", 40),
			Email:     strings.Repeat("<", 254),
			Location:  strings.Repeat("<", 160),
			Source:    strings.Repeat("<", 120),
			Skills:    workbenchPipelineMaximumStringList(32, 64, "<"),
			Owner:     strings.Repeat("<", 80),
			Status:    CandidateStatusActive,
			Summary:   strings.Repeat("<", 4000),
			Version:   1,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if withSourceEvent {
			eventID, nextErr := allocator.Next(IDKindEvent)
			if nextErr != nil {
				return nextErr
			}
			event := crmCandidateTestEvent(eventID, "maximum-candidate-mid", "maximum-author")
			event.CandidateID = candidateID
			event.ReviewStatus = ReviewStatusReviewed
			candidate.SourceEventID = eventID
			state.MessageEvents = append(state.MessageEvents, event)
		}
		state.Candidates = append(state.Candidates, candidate)
		return nil
	}); err != nil {
		t.Fatalf("create maximum candidate: %v", err)
	}
	return candidate
}

func workbenchPipelineSeedMaximumLeads(
	t *testing.T,
	store *CRMStore,
	jobID string,
	count int,
) {
	t.Helper()
	if err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		base := time.Now().UTC().Add(-time.Hour)
		reason := strings.Repeat("<", 2000)
		actor := strings.Repeat("<", 80)
		owner := strings.Repeat("<", 80)
		transitions := []struct {
			from LeadStage
			to   LeadStage
		}{
			{from: LeadStageSourced, to: LeadStageScreening},
			{from: LeadStageScreening, to: LeadStageInterview},
			{from: LeadStageInterview, to: LeadStageOffer},
		}
		for i := 0; i < count; i++ {
			candidateID, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			leadID, err := allocator.Next(IDKindLead)
			if err != nil {
				return err
			}
			createdAt := base.Add(time.Duration(i) * time.Minute)
			updatedAt := createdAt.Add(time.Duration(len(transitions)) * time.Second)
			state.Candidates = append(state.Candidates, Candidate{
				ID:        candidateID,
				FullName:  strings.Repeat("<", 120),
				Skills:    []string{},
				Status:    CandidateStatusActive,
				Version:   1,
				CreatedAt: createdAt,
				UpdatedAt: createdAt,
			})
			state.Leads = append(state.Leads, Lead{
				ID:             leadID,
				CandidateID:    candidateID,
				JobID:          jobID,
				Stage:          LeadStageOffer,
				Owner:          owner,
				Version:        uint64(len(transitions) + 1),
				CreatedAt:      createdAt,
				UpdatedAt:      updatedAt,
				StageChangedAt: updatedAt,
			})
			for eventIndex, transition := range transitions {
				eventID, nextErr := allocator.Next(IDKindStageEvent)
				if nextErr != nil {
					return nextErr
				}
				state.LeadStageEvents = append(state.LeadStageEvents, LeadStageEvent{
					ID:        eventID,
					LeadID:    leadID,
					FromStage: transition.from,
					ToStage:   transition.to,
					Reason:    reason,
					Actor:     actor,
					CreatedAt: createdAt.Add(time.Duration(eventIndex+1) * time.Second),
				})
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed maximum pipeline leads: %v", err)
	}
}

func workbenchPipelineLeadIDs(board workbenchPipelineResponseProjection) []string {
	ids := make([]string, 0, board.Total)
	for _, stage := range board.Stages {
		for _, item := range stage.Leads {
			ids = append(ids, item.Lead.ID)
		}
	}
	return ids
}

func workbenchPipelineProjectedLeads(
	board workbenchPipelineResponseProjection,
) []workbenchPipelineLeadListItemProjection {
	items := make([]workbenchPipelineLeadListItemProjection, 0, board.Total)
	for _, stage := range board.Stages {
		items = append(items, stage.Leads...)
	}
	return items
}

func workbenchPipelineDomainLeadIDs(board PipelineResponse) []string {
	ids := make([]string, 0, board.Total)
	for _, stage := range board.Stages {
		for _, item := range stage.Leads {
			ids = append(ids, item.Lead.ID)
		}
	}
	return ids
}

func workbenchPipelineDomainLeads(board PipelineResponse) []LeadListItem {
	items := make([]LeadListItem, 0, board.Total)
	for _, stage := range board.Stages {
		items = append(items, stage.Leads...)
	}
	return items
}

func workbenchPipelineCloneProjectedResponse(
	board workbenchPipelineResponseProjection,
) workbenchPipelineResponseProjection {
	cloned := workbenchPipelineResponseProjection{
		Stages: make([]workbenchPipelineStageProjection, len(board.Stages)),
		Total:  board.Total,
	}
	for i := range board.Stages {
		cloned.Stages[i] = workbenchPipelineStageProjection{
			Stage: board.Stages[i].Stage,
			Leads: append(
				[]workbenchPipelineLeadListItemProjection{},
				board.Stages[i].Leads...,
			),
		}
	}
	return cloned
}
