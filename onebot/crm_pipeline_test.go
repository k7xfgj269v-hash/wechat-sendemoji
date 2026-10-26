package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCRMPipelineJobAndLeadLifecycle(t *testing.T) {
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)

	job, err := crmPipelineCreateJob(store, JobDraft{
		Title: "Backend Engineer", Company: "Example", Requirements: []string{"Go"}, Owner: "ludwig",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if job.ID != "job_00000001" || job.Status != JobStatusOpen || job.Version != 1 {
		t.Fatalf("unexpected job: %#v", job)
	}

	lead, err := crmPipelineCreateLead(store, LeadCreate{
		CandidateID: candidate.ID, JobID: job.ID, Owner: "recruiter",
	})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if lead.ID != "lead_00000001" || lead.Stage != LeadStageSourced || lead.Version != 1 {
		t.Fatalf("unexpected lead: %#v", lead)
	}

	owner := "lead-owner"
	lead, err = crmPipelineUpdateLead(store, lead.ID, LeadUpdate{
		ExpectedVersion: lead.Version, Owner: &owner,
	})
	if err != nil {
		t.Fatalf("update lead: %v", err)
	}
	lead, err = crmPipelineTransitionLead(store, lead.ID, LeadTransition{
		ExpectedVersion: lead.Version, FromStage: LeadStageSourced, ToStage: LeadStageScreening,
	})
	if err != nil {
		t.Fatalf("transition lead: %v", err)
	}
	if lead.Stage != LeadStageScreening || lead.Version != 3 || lead.Owner != owner {
		t.Fatalf("unexpected transitioned lead: %#v", lead)
	}
	leadItem, err := crmPipelineGetLead(store, lead.ID)
	if err != nil {
		t.Fatalf("get transitioned lead: %v", err)
	}
	if len(leadItem.StageEvents) != 1 ||
		leadItem.StageEvents[0].FromStage != LeadStageSourced ||
		leadItem.StageEvents[0].ToStage != LeadStageScreening ||
		leadItem.StageEvents[0].Actor != "system" {
		t.Fatalf("unexpected stage events: %#v", leadItem.StageEvents)
	}

	board, err := crmPipelineBoard(store, job.ID, owner)
	if err != nil {
		t.Fatalf("pipeline board: %v", err)
	}
	if board.Total != 1 || len(board.Stages) != len(LeadStages()) {
		t.Fatalf("unexpected board: %#v", board)
	}
	if board.Stages[1].Stage != LeadStageScreening || len(board.Stages[1].Leads) != 1 {
		t.Fatalf("lead not grouped in screening: %#v", board.Stages)
	}

	detail, err := crmPipelineGetJob(store, job.ID)
	if err != nil {
		t.Fatalf("job detail: %v", err)
	}
	if detail.LeadCount != 1 || len(detail.LeadIDs) != 1 || detail.LeadIDs[0] != lead.ID {
		t.Fatalf("unexpected job detail: %#v", detail)
	}
}

func TestCRMPipelineJobResponsesProjectProvenanceAndRevisions(t *testing.T) {
	store := crmPipelineTestStore(t)
	groupID := "pipeline_projection@chatroom"
	crmJobIngestionSeedSourceGroup(t, store, groupID, true)
	draft := crmJobIngestionDraft("PIPELINE-PROJECTION-1")
	createEvent := crmJobIngestionSeedEvent(t, store, groupID, "mid-pipeline-create", "create projected job")
	created, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: createEvent.ID,
		GroupID: groupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatal(err)
	}
	updatedDraft := draft
	updatedDraft.Headcount++
	updateEvent := crmJobIngestionSeedEvent(t, store, groupID, "mid-pipeline-update", "update projected job")
	updated, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: updateEvent.ID,
		GroupID: groupID,
		Job:     updatedDraft,
	})
	if err != nil {
		t.Fatal(err)
	}

	total, jobs, err := crmPipelineListJobs(store, crmPipelineJobFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 ||
		len(jobs) != 1 ||
		jobs[0].Job.ID != created.Job.ID ||
		jobs[0].ProvenanceCount != 2 ||
		jobs[0].LatestIngestionDecision != JobIngestionUpdate {
		t.Fatalf("job list projection = %+v", jobs)
	}

	detail, err := crmPipelineGetJob(store, created.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Provenance) != 2 ||
		detail.Provenance[0] != updated.Provenance ||
		detail.Provenance[1] != created.Provenance ||
		len(detail.Revisions) != 2 ||
		detail.Revisions[0].Number != 2 ||
		detail.Revisions[1].Number != 1 {
		t.Fatalf("job detail projection = %+v", detail)
	}

	mux := http.NewServeMux()
	registerCRMPipelineRoutes(mux, store)
	response := crmPipelineRequest(t, mux, http.MethodGet, "/api/crm/jobs/"+created.Job.ID, "")
	if response.Code != http.StatusOK {
		t.Fatalf("job detail route status = %d, body = %s", response.Code, response.Body.String())
	}
	var routed JobDetail
	crmPipelineDecodeResponse(t, response, &routed)
	if len(routed.Provenance) != 2 || len(routed.Revisions) != 2 {
		t.Fatalf("job detail route projection = %+v", routed)
	}
}

func TestCRMPipelineRejectsInvalidReferencesAndDuplicateActiveLead(t *testing.T) {
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, err := crmPipelineCreateJob(store, JobDraft{Title: "Role", Company: "Example"})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: "cand_99999999", JobID: job.ID}); err == nil {
		t.Fatal("expected missing candidate error")
	} else {
		var notFound *crmPipelineNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	}
	if _, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID}); err != nil {
		t.Fatalf("create first lead: %v", err)
	}
	if _, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("expected duplicate conflict, got %v", err)
	}

	closed, err := crmPipelineSetJobStatus(store, job.ID, crmPipelineJobStatusRequest{
		ExpectedVersion: job.Version, Status: JobStatusClosed,
	})
	if err != nil {
		t.Fatalf("close job: %v", err)
	}
	other := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	if _, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: other.ID, JobID: closed.ID}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("expected closed job conflict, got %v", err)
	}
}

func TestCRMPipelineTransitionsAndVersions(t *testing.T) {
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, _ := crmPipelineCreateJob(store, JobDraft{Title: "Role", Company: "Example"})
	lead, _ := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})

	if _, err := crmPipelineTransitionLead(store, lead.ID, LeadTransition{
		ExpectedVersion: lead.Version, FromStage: LeadStageSourced, ToStage: LeadStageOffer,
	}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("expected prohibited transition conflict, got %v", err)
	}
	if _, err := crmPipelineTransitionLead(store, lead.ID, LeadTransition{
		ExpectedVersion: lead.Version + 1, FromStage: LeadStageSourced, ToStage: LeadStageScreening,
	}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("expected stale transition conflict, got %v", err)
	}
	if _, err := crmPipelineSetJobStatus(store, job.ID, crmPipelineJobStatusRequest{
		ExpectedVersion: job.Version, Status: JobStatusArchived,
	}); err != nil {
		t.Fatalf("archive job: %v", err)
	}
	if _, err := crmPipelineSetJobStatus(store, job.ID, crmPipelineJobStatusRequest{
		ExpectedVersion: job.Version + 1, Status: JobStatusOpen,
	}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("expected terminal job conflict, got %v", err)
	}
}

func TestCRMPipelineConcurrentTransitionHasSingleWinner(t *testing.T) {
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, _ := crmPipelineCreateJob(store, JobDraft{Title: "Role", Company: "Example"})
	lead, _ := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})

	results := make(chan error, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := crmPipelineTransitionLead(store, lead.ID, LeadTransition{
				ExpectedVersion: lead.Version,
				FromStage:       LeadStageSourced,
				ToStage:         LeadStageScreening,
			})
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCRMConflict):
			conflicts++
		default:
			t.Fatalf("unexpected transition error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestCRMPipelineHiredTransitionCreatesOnboarding(t *testing.T) {
	store := crmPipelineTestStore(t)
	lead := crmOnboardingTestHiredLead(t, store)
	state := store.Snapshot()
	if len(state.OnboardingCases) != 1 {
		t.Fatalf("onboarding cases=%d, want 1", len(state.OnboardingCases))
	}
	onboarding := state.OnboardingCases[0]
	if onboarding.LeadID != lead.ID ||
		onboarding.CandidateID != lead.CandidateID ||
		onboarding.JobID != lead.JobID {
		t.Fatalf("unexpected hired handoff: %#v", onboarding)
	}
}

func TestCRMPipelineRestartPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, err := crmPipelineCreateJob(store, JobDraft{Title: "Persistent", Company: "Example"})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	detail, err := crmPipelineGetJob(reopened, job.ID)
	if err != nil {
		t.Fatalf("get reopened job: %v", err)
	}
	item, err := crmPipelineGetLead(reopened, lead.ID)
	if err != nil {
		t.Fatalf("get reopened lead: %v", err)
	}
	if detail.Job.Title != "Persistent" || item.Lead.JobID != job.ID {
		t.Fatalf("unexpected reopened data: %#v %#v", detail, item)
	}
}

func TestCRMPipelineRoutes(t *testing.T) {
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	mux := http.NewServeMux()
	registerCRMPipelineRoutes(mux, store)

	createJob := crmPipelineRequest(t, mux, http.MethodPost, "/api/crm/jobs",
		`{"title":"Backend","company":"Example","requirements":["Go"]}`)
	if createJob.Code != http.StatusCreated {
		t.Fatalf("create job status=%d body=%s", createJob.Code, createJob.Body.String())
	}
	var job Job
	crmPipelineDecodeResponse(t, createJob, &job)

	createLead := crmPipelineRequest(t, mux, http.MethodPost, "/api/crm/leads",
		`{"candidate_id":"`+candidate.ID+`","job_id":"`+job.ID+`"}`)
	if createLead.Code != http.StatusCreated {
		t.Fatalf("create lead status=%d body=%s", createLead.Code, createLead.Body.String())
	}
	var lead Lead
	crmPipelineDecodeResponse(t, createLead, &lead)

	transitionBody := `{"expected_version":1,"from_stage":"sourced","to_stage":"screening"}`
	transition := crmPipelineRequest(t, mux, http.MethodPost, "/api/crm/leads/"+lead.ID+"/transition", transitionBody)
	if transition.Code != http.StatusOK {
		t.Fatalf("transition status=%d body=%s", transition.Code, transition.Body.String())
	}

	board := crmPipelineRequest(t, mux, http.MethodGet, "/api/crm/pipeline?job_id="+job.ID, "")
	if board.Code != http.StatusOK {
		t.Fatalf("board status=%d body=%s", board.Code, board.Body.String())
	}
	var response PipelineResponse
	crmPipelineDecodeResponse(t, board, &response)
	if response.Total != 1 || len(response.Stages) != 7 {
		t.Fatalf("unexpected board: %#v", response)
	}

	unknown := crmPipelineRequest(t, mux, http.MethodPost, "/api/crm/jobs", `{"title":"x","company":"y","extra":true}`)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
	var apiError map[string]string
	crmPipelineDecodeResponse(t, unknown, &apiError)
	if apiError["code"] != crmPipelineCodeInvalidJSON {
		t.Fatalf("unexpected error: %#v", apiError)
	}

	method := crmPipelineRequest(t, mux, http.MethodDelete, "/api/crm/jobs/"+job.ID, "")
	if method.Code != http.StatusMethodNotAllowed || method.Header().Get("Allow") != "GET, PATCH" {
		t.Fatalf("method response=%d allow=%q", method.Code, method.Header().Get("Allow"))
	}
}

func TestCRMPipelineFiltersAndOrdering(t *testing.T) {
	store := crmPipelineTestStore(t)
	first, _ := crmPipelineCreateJob(store, JobDraft{
		Title: "Backend", Company: "Alpha", Owner: "alice", Requirements: []string{"Go"},
	})
	second, _ := crmPipelineCreateJob(store, JobDraft{
		Title: "Frontend", Company: "Beta", Owner: "bob", Requirements: []string{"TypeScript"},
	})
	total, jobs, err := crmPipelineListJobs(store, crmPipelineJobFilter{Query: "typescript", Limit: 50})
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if total != 1 || jobs[0].Job.ID != second.ID {
		t.Fatalf("unexpected search result: %#v", jobs)
	}
	total, jobs, err = crmPipelineListJobs(store, crmPipelineJobFilter{Owner: "alice", Limit: 50})
	if err != nil {
		t.Fatalf("list jobs by owner: %v", err)
	}
	if total != 1 || jobs[0].Job.ID != first.ID {
		t.Fatalf("unexpected owner result: %#v", jobs)
	}
	if _, _, err := crmPipelineListJobs(store, crmPipelineJobFilter{Limit: CRMMaxListLimit + 1}); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("expected invalid limit, got %v", err)
	}
}

func TestCRMPipelineJobOnsiteFieldsUpdateClearAndFilter(t *testing.T) {
	store := crmPipelineTestStore(t)
	startAt := time.Date(2026, time.July, 24, 7, 30, 0, 0, time.UTC)
	deadlineAt := startAt.Add(48 * time.Hour)
	requirements := []string{"forklift"}
	documents := []string{"ID card", "health certificate"}

	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:                 "Warehouse Operator",
		Company:               "Example Logistics",
		Location:              "Berlin",
		EmploymentType:        "contract",
		Description:           "Onsite warehouse staffing",
		Requirements:          requirements,
		DemandCode:            "DEM-2026-0042",
		SiteName:              "North Hub",
		OnsiteContact:         "Marta +49 30 1234",
		Headcount:             24,
		Priority:              JobPriorityUrgent,
		StartAt:               &startAt,
		DeadlineAt:            &deadlineAt,
		Shift:                 "night",
		WorkSchedule:          "Four nights on, two nights off",
		CompensationSummary:   "Hourly rate plus night allowance",
		Accommodation:         "Shared accommodation available",
		TransportRequirements: "Shuttle from central station",
		RequiredDocuments:     documents,
		Owner:                 "alice",
	})
	if err != nil {
		t.Fatalf("create onsite job: %v", err)
	}
	requirements[0] = "mutated"
	documents[0] = "mutated"
	if job.DemandCode != "DEM-2026-0042" ||
		job.SiteName != "North Hub" ||
		job.Headcount != 24 ||
		job.Priority != JobPriorityUrgent ||
		job.StartAt == nil || !job.StartAt.Equal(startAt) ||
		job.DeadlineAt == nil || !job.DeadlineAt.Equal(deadlineAt) ||
		job.RequiredDocuments[0] != "ID card" {
		t.Fatalf("unexpected onsite job: %#v", job)
	}
	if got := store.Snapshot().Jobs[0]; got.Requirements[0] != "forklift" || got.RequiredDocuments[0] != "ID card" {
		t.Fatalf("job slices were not cloned: %#v", got)
	}

	siteName := "South Hub"
	headcount := 30
	priority := JobPriorityHigh
	transport := "Coach pickup required"
	updated, err := crmPipelineUpdateJob(store, job.ID, JobUpdate{
		ExpectedVersion:       job.Version,
		SiteName:              &siteName,
		Headcount:             &headcount,
		Priority:              &priority,
		TransportRequirements: &transport,
		ClearStartAt:          true,
		ClearDeadlineAt:       true,
	})
	if err != nil {
		t.Fatalf("update onsite job: %v", err)
	}
	if updated.SiteName != siteName ||
		updated.Headcount != headcount ||
		updated.Priority != priority ||
		updated.TransportRequirements != transport ||
		updated.StartAt != nil ||
		updated.DeadlineAt != nil {
		t.Fatalf("unexpected updated onsite job: %#v", updated)
	}

	total, jobs, err := crmPipelineListJobs(store, crmPipelineJobFilter{
		Priority: JobPriorityHigh,
		SiteName: "south",
		Query:    "health certificate",
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("filter onsite jobs: %v", err)
	}
	if total != 1 || len(jobs) != 1 || jobs[0].Job.ID != job.ID {
		t.Fatalf("unexpected onsite filter result: total=%d jobs=%#v", total, jobs)
	}
}

func TestCRMPipelineJobOnsiteRoutesAndStrictJSON(t *testing.T) {
	store := crmPipelineTestStore(t)
	mux := http.NewServeMux()
	registerCRMPipelineRoutes(mux, store)

	body := `{
		"title":"Warehouse Operator",
		"company":"Example Logistics",
		"location":"Berlin",
		"requirements":["forklift"],
		"demand_code":"DEM-42",
		"site_name":"North Hub",
		"onsite_contact":"Marta",
		"headcount":12,
		"priority":"urgent",
		"start_at":"2026-07-24T07:30:00Z",
		"deadline_at":"2026-07-26T07:30:00Z",
		"shift":"night",
		"work_schedule":"four on, two off",
		"compensation_summary":"hourly plus allowance",
		"accommodation":"shared rooms",
		"transport_requirements":"shuttle",
		"required_documents":["ID card"]
	}`
	create := crmPipelineRequest(t, mux, http.MethodPost, "/api/crm/jobs", body)
	if create.Code != http.StatusCreated {
		t.Fatalf("create onsite job status=%d body=%s", create.Code, create.Body.String())
	}
	var job Job
	crmPipelineDecodeResponse(t, create, &job)
	if job.DemandCode != "DEM-42" || job.Priority != JobPriorityUrgent || job.Headcount != 12 {
		t.Fatalf("unexpected route job: %#v", job)
	}

	list := crmPipelineRequest(
		t,
		mux,
		http.MethodGet,
		"/api/crm/jobs?priority=urgent&site_name=North&q=ID%20card",
		"",
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list onsite jobs status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Total int           `json:"total"`
		Jobs  []JobListItem `json:"jobs"`
	}
	crmPipelineDecodeResponse(t, list, &listed)
	if listed.Total != 1 || listed.Jobs[0].Job.ID != job.ID {
		t.Fatalf("unexpected route filter result: %#v", listed)
	}

	clear := crmPipelineRequest(
		t,
		mux,
		http.MethodPatch,
		"/api/crm/jobs/"+job.ID,
		`{"expected_version":1,"clear_start_at":true,"clear_deadline_at":true}`,
	)
	if clear.Code != http.StatusOK {
		t.Fatalf("clear onsite times status=%d body=%s", clear.Code, clear.Body.String())
	}
	var cleared Job
	crmPipelineDecodeResponse(t, clear, &cleared)
	if cleared.StartAt != nil || cleared.DeadlineAt != nil {
		t.Fatalf("onsite times were not cleared: %#v", cleared)
	}

	invalid := crmPipelineRequest(
		t,
		mux,
		http.MethodPatch,
		"/api/crm/jobs/"+job.ID,
		`{"expected_version":2,"start_at":"2026-07-24T07:30:00Z","clear_start_at":true}`,
	)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("conflicting time mutation status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func crmPipelineTestStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatalf("open CRM store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func crmPipelineTestCandidate(t *testing.T, store *CRMStore, status CandidateStatus) Candidate {
	t.Helper()
	var candidate Candidate
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		candidate = Candidate{
			ID: id, FullName: "Candidate " + id, Skills: []string{}, Status: status,
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		state.Candidates = append(state.Candidates, candidate)
		return nil
	})
	if err != nil {
		t.Fatalf("create test candidate: %v", err)
	}
	return candidate
}

func crmPipelineRequest(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func crmPipelineDecodeResponse(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.Unmarshal(response.Body.Bytes(), destination); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}
