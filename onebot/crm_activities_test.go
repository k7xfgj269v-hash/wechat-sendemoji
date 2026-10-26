package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type crmActivityTestFixture struct {
	CandidateOne string
	CandidateTwo string
	Job          string
	Lead         string
}

func TestCRMActivityNotesValidationOrderingAndPagination(t *testing.T) {
	store, _ := crmActivityTestOpenStore(t)
	fixture := crmActivityTestSeedReferences(t, store)
	service := crmActivityNewService(store)
	clock := time.Date(2026, time.July, 21, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }

	first, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetCandidate,
		TargetID:   fixture.CandidateOne,
		Author:     " recruiter ",
		Body:       " later note ",
	})
	if err != nil {
		t.Fatalf("CreateNote(first) error = %v", err)
	}
	if first.Author != "recruiter" || first.Body != "later note" {
		t.Fatalf("CreateNote(first) = %+v, want trimmed fields", first)
	}

	clock = clock.Add(-time.Hour)
	second, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetCandidate,
		TargetID:   fixture.CandidateOne,
		Body:       "earlier note",
	})
	if err != nil {
		t.Fatalf("CreateNote(second) error = %v", err)
	}
	clock = clock.Add(2 * time.Hour)
	leadNote, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetLead,
		TargetID:   fixture.Lead,
		Body:       "lead note",
	})
	if err != nil {
		t.Fatalf("CreateNote(lead) error = %v", err)
	}

	response, err := service.ListNotes(crmActivityNoteFilter{
		TargetType: NoteTargetCandidate,
		TargetID:   fixture.CandidateOne,
	})
	if err != nil {
		t.Fatalf("ListNotes() error = %v", err)
	}
	if response.Total != 2 || len(response.Notes) != 2 {
		t.Fatalf("ListNotes() = %+v, want two candidate notes", response)
	}
	if response.Notes[0].ID != second.ID || response.Notes[1].ID != first.ID {
		t.Fatalf("note order = [%s %s], want [%s %s]", response.Notes[0].ID, response.Notes[1].ID, second.ID, first.ID)
	}

	page, err := service.ListNotes(crmActivityNoteFilter{
		TargetType: NoteTargetCandidate,
		TargetID:   fixture.CandidateOne,
		Limit:      1,
		Offset:     1,
	})
	if err != nil {
		t.Fatalf("ListNotes(page) error = %v", err)
	}
	if page.Total != 2 || len(page.Notes) != 1 || page.Notes[0].ID != first.ID {
		t.Fatalf("ListNotes(page) = %+v, want second chronological note", page)
	}

	leadResponse, err := service.ListNotes(crmActivityNoteFilter{
		TargetType: NoteTargetLead,
		TargetID:   fixture.Lead,
	})
	if err != nil {
		t.Fatalf("ListNotes(lead) error = %v", err)
	}
	if leadResponse.Total != 1 || leadResponse.Notes[0].ID != leadNote.ID {
		t.Fatalf("ListNotes(lead) = %+v, want lead note", leadResponse)
	}

	beforeCounter := store.Snapshot().Counters.Note
	tests := []struct {
		name   string
		create NoteCreate
		errIs  error
	}{
		{
			name: "missing target",
			create: NoteCreate{
				TargetType: NoteTargetCandidate,
				TargetID:   "cand_99999999",
				Body:       "missing",
			},
		},
		{
			name: "invalid target type",
			create: NoteCreate{
				TargetType: NoteTargetType("job"),
				TargetID:   fixture.Job,
				Body:       "invalid",
			},
			errIs: ErrCRMValidation,
		},
		{
			name: "oversized body",
			create: NoteCreate{
				TargetType: NoteTargetCandidate,
				TargetID:   fixture.CandidateOne,
				Body:       strings.Repeat("x", 4001),
			},
			errIs: ErrCRMValidation,
		},
		{
			name: "oversized author",
			create: NoteCreate{
				TargetType: NoteTargetCandidate,
				TargetID:   fixture.CandidateOne,
				Author:     strings.Repeat("x", 81),
				Body:       "invalid",
			},
			errIs: ErrCRMValidation,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := service.CreateNote(test.create)
			if test.errIs != nil {
				if !errors.Is(err, test.errIs) {
					t.Fatalf("CreateNote() error = %v, want %v", err, test.errIs)
				}
				return
			}
			if !crmActivityIsNotFound(err) {
				t.Fatalf("CreateNote() error = %v, want not found", err)
			}
		})
	}
	if got := store.Snapshot().Counters.Note; got != beforeCounter {
		t.Fatalf("note counter after rejected creates = %d, want %d", got, beforeCounter)
	}
}

func TestCRMActivityTaskLifecycleFiltersAndProjection(t *testing.T) {
	store, _ := crmActivityTestOpenStore(t)
	fixture := crmActivityTestSeedReferences(t, store)
	service := crmActivityNewService(store)
	clock := time.Date(2026, time.July, 21, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return clock }

	overdue, err := service.CreateTask(TaskCreate{
		Title:       " Call candidate ",
		Description: " discuss role ",
		Owner:       " alice ",
		DueAt:       clock.Add(-time.Hour),
		CandidateID: fixture.CandidateOne,
	})
	if err != nil {
		t.Fatalf("CreateTask(overdue) error = %v", err)
	}
	dueNow, err := service.CreateTask(TaskCreate{
		Title:  "Lead follow-up",
		Owner:  "alice",
		DueAt:  clock,
		LeadID: fixture.Lead,
	})
	if err != nil {
		t.Fatalf("CreateTask(due now) error = %v", err)
	}
	completed, err := service.CreateTask(TaskCreate{
		Title:       "Prepare interview",
		Owner:       "bob",
		DueAt:       clock.Add(time.Hour),
		CandidateID: fixture.CandidateOne,
		LeadID:      fixture.Lead,
	})
	if err != nil {
		t.Fatalf("CreateTask(completed) error = %v", err)
	}
	futureOffset := clock.Add(2 * time.Hour).In(time.FixedZone("UTC+2", 2*60*60))
	future, err := service.CreateTask(TaskCreate{
		Title:       "Second candidate",
		DueAt:       futureOffset,
		CandidateID: fixture.CandidateTwo,
	})
	if err != nil {
		t.Fatalf("CreateTask(future) error = %v", err)
	}
	if future.DueAt.Location() != time.UTC || !future.DueAt.Equal(clock.Add(2*time.Hour)) {
		t.Fatalf("future due_at = %v, want normalized UTC", future.DueAt)
	}
	completed, err = service.SetTaskStatus(completed.ID, TaskStatusRequest{
		ExpectedVersion: completed.Version,
		Status:          FollowUpStatusCompleted,
	})
	if err != nil {
		t.Fatalf("SetTaskStatus(completed) error = %v", err)
	}

	all, err := service.ListTasks(crmActivityTaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks(all) error = %v", err)
	}
	if all.Total != 4 || len(all.Tasks) != 4 {
		t.Fatalf("ListTasks(all) = %+v, want four tasks", all)
	}
	wantOrder := []string{overdue.ID, dueNow.ID, completed.ID, future.ID}
	for index, want := range wantOrder {
		if all.Tasks[index].Task.ID != want {
			t.Fatalf("task order[%d] = %s, want %s", index, all.Tasks[index].Task.ID, want)
		}
	}
	if all.Tasks[1].CandidateName != "Ada" ||
		all.Tasks[1].LeadStage != LeadStageInterview ||
		all.Tasks[1].JobID != fixture.Job ||
		all.Tasks[1].JobTitle != "Backend Engineer" {
		t.Fatalf("lead-only task projection = %+v", all.Tasks[1])
	}

	trueValue := true
	overdueOnly, err := service.ListTasks(crmActivityTaskFilter{Overdue: &trueValue})
	if err != nil {
		t.Fatalf("ListTasks(overdue) error = %v", err)
	}
	if overdueOnly.Total != 1 || overdueOnly.Tasks[0].Task.ID != overdue.ID {
		t.Fatalf("overdue tasks = %+v, want only %s", overdueOnly, overdue.ID)
	}
	falseValue := false
	notOverdue, err := service.ListTasks(crmActivityTaskFilter{Overdue: &falseValue})
	if err != nil {
		t.Fatalf("ListTasks(not overdue) error = %v", err)
	}
	if notOverdue.Total != 3 {
		t.Fatalf("not overdue total = %d, want 3", notOverdue.Total)
	}

	alice := "alice"
	filterTests := []struct {
		name   string
		filter crmActivityTaskFilter
		total  int
	}{
		{name: "owner", filter: crmActivityTaskFilter{Owner: &alice}, total: 2},
		{name: "open status", filter: crmActivityTaskFilter{Status: FollowUpStatusOpen}, total: 3},
		{name: "completed status", filter: crmActivityTaskFilter{Status: FollowUpStatusCompleted}, total: 1},
		{name: "candidate includes lead-only", filter: crmActivityTaskFilter{CandidateID: fixture.CandidateOne}, total: 3},
		{name: "lead", filter: crmActivityTaskFilter{LeadID: fixture.Lead}, total: 2},
		{
			name: "inclusive due range",
			filter: crmActivityTaskFilter{
				DueFrom: crmActivityTestTimePointer(clock),
				DueTo:   crmActivityTestTimePointer(clock.Add(time.Hour)),
			},
			total: 2,
		},
		{name: "page", filter: crmActivityTaskFilter{Limit: 2, Offset: 1}, total: 4},
	}
	for _, test := range filterTests {
		t.Run(test.name, func(t *testing.T) {
			response, err := service.ListTasks(test.filter)
			if err != nil {
				t.Fatalf("ListTasks() error = %v", err)
			}
			if response.Total != test.total {
				t.Fatalf("ListTasks() total = %d, want %d", response.Total, test.total)
			}
			if test.name == "page" {
				if len(response.Tasks) != 2 ||
					response.Tasks[0].Task.ID != dueNow.ID ||
					response.Tasks[1].Task.ID != completed.ID {
					t.Fatalf("ListTasks(page) = %+v", response.Tasks)
				}
			}
		})
	}

	if _, err := service.ListTasks(crmActivityTaskFilter{
		DueFrom: crmActivityTestTimePointer(clock.Add(time.Hour)),
		DueTo:   crmActivityTestTimePointer(clock),
	}); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("ListTasks(inverted range) error = %v, want validation error", err)
	}

	clock = clock.Add(10 * time.Minute)
	newOwner := " carol "
	newTitle := "Updated follow-up"
	newDue := clock.Add(3 * time.Hour).In(time.FixedZone("UTC-5", -5*60*60))
	updated, err := service.UpdateTask(overdue.ID, TaskUpdate{
		ExpectedVersion: overdue.Version,
		Title:           &newTitle,
		Owner:           &newOwner,
		DueAt:           &newDue,
	})
	if err != nil {
		t.Fatalf("UpdateTask() error = %v", err)
	}
	if updated.Version != 2 || updated.Owner != "carol" || updated.Title != newTitle || updated.DueAt.Location() != time.UTC {
		t.Fatalf("UpdateTask() = %+v", updated)
	}
	if _, err := service.UpdateTask(overdue.ID, TaskUpdate{
		ExpectedVersion: overdue.Version,
		Owner:           &newOwner,
	}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("UpdateTask(stale) error = %v, want conflict", err)
	}

	mismatchedCandidate := fixture.CandidateTwo
	leadID := fixture.Lead
	if _, err := service.UpdateTask(overdue.ID, TaskUpdate{
		ExpectedVersion: updated.Version,
		CandidateID:     &mismatchedCandidate,
		LeadID:          &leadID,
	}); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("UpdateTask(mismatched reference) error = %v, want validation error", err)
	}
	missingLead := "lead_99999999"
	if _, err := service.UpdateTask(overdue.ID, TaskUpdate{
		ExpectedVersion: updated.Version,
		LeadID:          &missingLead,
	}); !crmActivityIsNotFound(err) {
		t.Fatalf("UpdateTask(missing lead) error = %v, want not found", err)
	}

	empty := ""
	if _, err := service.UpdateTask(future.ID, TaskUpdate{
		ExpectedVersion: future.Version,
		CandidateID:     &empty,
		LeadID:          &empty,
	}); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("UpdateTask(clear targets) error = %v, want validation error", err)
	}

	clock = clock.Add(10 * time.Minute)
	completedUpdated, err := service.SetTaskStatus(updated.ID, TaskStatusRequest{
		ExpectedVersion: updated.Version,
		Status:          FollowUpStatusCompleted,
	})
	if err != nil {
		t.Fatalf("SetTaskStatus(complete) error = %v", err)
	}
	if completedUpdated.Version != 3 ||
		completedUpdated.CompletedAt == nil ||
		!completedUpdated.CompletedAt.Equal(clock) {
		t.Fatalf("completed task = %+v", completedUpdated)
	}
	if _, err := service.SetTaskStatus(updated.ID, TaskStatusRequest{
		ExpectedVersion: completedUpdated.Version,
		Status:          FollowUpStatusCompleted,
	}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("SetTaskStatus(same status) error = %v, want conflict", err)
	}

	clock = clock.Add(10 * time.Minute)
	reopened, err := service.SetTaskStatus(updated.ID, TaskStatusRequest{
		ExpectedVersion: completedUpdated.Version,
		Status:          FollowUpStatusOpen,
	})
	if err != nil {
		t.Fatalf("SetTaskStatus(reopen) error = %v", err)
	}
	if reopened.Version != 4 || reopened.CompletedAt != nil || reopened.Status != FollowUpStatusOpen {
		t.Fatalf("reopened task = %+v", reopened)
	}
	if _, err := service.SetTaskStatus(updated.ID, TaskStatusRequest{
		ExpectedVersion: reopened.Version,
		Status:          FollowUpStatus("cancelled"),
	}); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("SetTaskStatus(invalid) error = %v, want validation error", err)
	}

	beforeCounter := store.Snapshot().Counters.Task
	invalidCreates := []TaskCreate{
		{Title: "no target", DueAt: clock},
		{Title: "zero due", CandidateID: fixture.CandidateOne},
		{Title: strings.Repeat("x", 201), DueAt: clock, CandidateID: fixture.CandidateOne},
		{Title: "owner", Owner: strings.Repeat("x", 81), DueAt: clock, CandidateID: fixture.CandidateOne},
		{Title: "missing candidate", DueAt: clock, CandidateID: "cand_99999999"},
		{Title: "missing lead", DueAt: clock, LeadID: "lead_99999999"},
		{Title: "mismatch", DueAt: clock, CandidateID: fixture.CandidateTwo, LeadID: fixture.Lead},
	}
	for index, create := range invalidCreates {
		if _, err := service.CreateTask(create); err == nil {
			t.Fatalf("CreateTask(invalid %d) succeeded", index)
		}
	}
	if got := store.Snapshot().Counters.Task; got != beforeCounter {
		t.Fatalf("task counter after rejected creates = %d, want %d", got, beforeCounter)
	}
}

func TestCRMActivityConcurrentCompletion(t *testing.T) {
	store, _ := crmActivityTestOpenStore(t)
	fixture := crmActivityTestSeedReferences(t, store)
	service := crmActivityNewService(store)
	now := time.Date(2026, time.July, 21, 16, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	task, err := service.CreateTask(TaskCreate{
		Title:       "Concurrent completion",
		DueAt:       now.Add(time.Hour),
		CandidateID: fixture.CandidateOne,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := service.SetTaskStatus(task.ID, TaskStatusRequest{
				ExpectedVersion: task.Version,
				Status:          FollowUpStatusCompleted,
			})
			errs <- err
		}()
	}
	close(start)
	workers.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrCRMConflict):
			conflicts++
		default:
			t.Fatalf("concurrent completion error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent completion successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}

	item, err := service.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if item.Task.Status != FollowUpStatusCompleted ||
		item.Task.Version != 2 ||
		item.Task.CompletedAt == nil ||
		!item.Task.CompletedAt.Equal(now) {
		t.Fatalf("completed task after race = %+v", item.Task)
	}
}

func TestCRMActivityRestartPersistence(t *testing.T) {
	store, path := crmActivityTestOpenStore(t)
	fixture := crmActivityTestSeedReferences(t, store)
	service := crmActivityNewService(store)
	now := time.Date(2026, time.July, 21, 18, 0, 0, 123, time.UTC)
	service.now = func() time.Time { return now }

	note, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetLead,
		TargetID:   fixture.Lead,
		Author:     "operator",
		Body:       "persist this note",
	})
	if err != nil {
		t.Fatalf("CreateNote() error = %v", err)
	}
	task, err := service.CreateTask(TaskCreate{
		Title:  "Persistent task",
		Owner:  "operator",
		DueAt:  now.Add(24 * time.Hour),
		LeadID: fixture.Lead,
	})
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	now = now.Add(time.Minute)
	task, err = service.SetTaskStatus(task.ID, TaskStatusRequest{
		ExpectedVersion: task.Version,
		Status:          FollowUpStatusCompleted,
	})
	if err != nil {
		t.Fatalf("SetTaskStatus() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore(restart) error = %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	restarted := crmActivityNewService(reopened)
	restarted.now = func() time.Time { return now }

	notes, err := restarted.ListNotes(crmActivityNoteFilter{
		TargetType: NoteTargetLead,
		TargetID:   fixture.Lead,
	})
	if err != nil {
		t.Fatalf("ListNotes(restart) error = %v", err)
	}
	if notes.Total != 1 || notes.Notes[0].ID != note.ID || notes.Notes[0].Body != note.Body {
		t.Fatalf("notes after restart = %+v", notes)
	}
	item, err := restarted.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask(restart) error = %v", err)
	}
	if item.Task.Status != FollowUpStatusCompleted ||
		item.Task.Version != task.Version ||
		item.Task.CompletedAt == nil ||
		!item.Task.CompletedAt.Equal(*task.CompletedAt) {
		t.Fatalf("task after restart = %+v, want %+v", item.Task, task)
	}
	if item.CandidateName != "Ada" || item.JobID != fixture.Job {
		t.Fatalf("task projection after restart = %+v", item)
	}
}

func TestCRMActivityRoutesContract(t *testing.T) {
	store, _ := crmActivityTestOpenStore(t)
	fixture := crmActivityTestSeedReferences(t, store)
	mux := http.NewServeMux()
	registerCRMActivityRoutes(mux, store)

	noteBody := `{"target_type":"candidate","target_id":"` + fixture.CandidateOne + `","author":"operator","body":"route note"}`
	response := crmActivityTestRequest(t, mux, http.MethodPost, "/api/crm/notes", noteBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST notes status = %d, body = %s", response.Code, response.Body.String())
	}
	response = crmActivityTestRequest(
		t,
		mux,
		http.MethodGet,
		"/api/crm/notes?target_type=candidate&target_id="+fixture.CandidateOne,
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("GET notes status = %d, body = %s", response.Code, response.Body.String())
	}
	var noteList crmActivityNoteListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &noteList); err != nil {
		t.Fatalf("decode note list: %v", err)
	}
	if noteList.Total != 1 || len(noteList.Notes) != 1 {
		t.Fatalf("GET notes response = %+v", noteList)
	}

	dueAt := time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC)
	taskBody := `{"title":"Route task","owner":"alice","due_at":"` +
		dueAt.Format(time.RFC3339Nano) +
		`","candidate_id":"` + fixture.CandidateOne + `","lead_id":"` + fixture.Lead + `"}`
	response = crmActivityTestRequest(t, mux, http.MethodPost, "/api/crm/tasks", taskBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("POST tasks status = %d, body = %s", response.Code, response.Body.String())
	}
	var task FollowUpTask
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode task: %v", err)
	}

	response = crmActivityTestRequest(t, mux, http.MethodGet, "/api/crm/tasks/"+task.ID, "")
	if response.Code != http.StatusOK {
		t.Fatalf("GET task status = %d, body = %s", response.Code, response.Body.String())
	}
	var item TaskListItem
	if err := json.Unmarshal(response.Body.Bytes(), &item); err != nil {
		t.Fatalf("decode task item: %v", err)
	}
	if item.CandidateName != "Ada" || item.JobID != fixture.Job {
		t.Fatalf("GET task projection = %+v", item)
	}

	response = crmActivityTestRequest(
		t,
		mux,
		http.MethodPatch,
		"/api/crm/tasks/"+task.ID,
		`{"expected_version":1,"owner":"bob"}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("PATCH task status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode updated task: %v", err)
	}
	if task.Owner != "bob" || task.Version != 2 {
		t.Fatalf("PATCH task = %+v", task)
	}

	response = crmActivityTestRequest(
		t,
		mux,
		http.MethodPost,
		"/api/crm/tasks/"+task.ID+"/status",
		`{"expected_version":2,"status":"completed"}`,
	)
	if response.Code != http.StatusOK {
		t.Fatalf("POST task status = %d, body = %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &task); err != nil {
		t.Fatalf("decode completed task: %v", err)
	}
	if task.Status != FollowUpStatusCompleted || task.Version != 3 || task.CompletedAt == nil {
		t.Fatalf("completed task response = %+v", task)
	}

	response = crmActivityTestRequest(
		t,
		mux,
		http.MethodGet,
		"/api/crm/tasks?owner=bob&status=completed&candidate_id="+fixture.CandidateOne+
			"&lead_id="+fixture.Lead+
			"&due_from="+url.QueryEscape(dueAt.Format(time.RFC3339Nano))+
			"&due_to="+url.QueryEscape(dueAt.Format(time.RFC3339Nano)),
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("GET tasks filtered status = %d, body = %s", response.Code, response.Body.String())
	}
	var taskList crmActivityTaskListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &taskList); err != nil {
		t.Fatalf("decode task list: %v", err)
	}
	if taskList.Total != 1 || taskList.Tasks[0].Task.ID != task.ID {
		t.Fatalf("GET tasks filtered response = %+v", taskList)
	}

	errorTests := []struct {
		name   string
		method string
		target string
		body   string
		status int
		code   string
	}{
		{
			name:   "non-object JSON",
			method: http.MethodPost,
			target: "/api/crm/tasks",
			body:   `[]`,
			status: http.StatusBadRequest,
			code:   "invalid_json",
		},
		{
			name:   "unknown field",
			method: http.MethodPost,
			target: "/api/crm/tasks",
			body:   `{"title":"x","due_at":"2030-01-01T00:00:00Z","candidate_id":"` + fixture.CandidateOne + `","extra":true}`,
			status: http.StatusBadRequest,
			code:   "invalid_json",
		},
		{
			name:   "trailing JSON",
			method: http.MethodPost,
			target: "/api/crm/tasks",
			body:   `{"title":"x","due_at":"2030-01-01T00:00:00Z","candidate_id":"` + fixture.CandidateOne + `"} {}`,
			status: http.StatusBadRequest,
			code:   "invalid_json",
		},
		{
			name:   "oversized body",
			method: http.MethodPost,
			target: "/api/crm/tasks",
			body:   `{"title":"` + strings.Repeat("x", CRMMaxRequestBodyBytes+1) + `"}`,
			status: http.StatusRequestEntityTooLarge,
			code:   "body_too_large",
		},
		{
			name:   "missing reference",
			method: http.MethodPost,
			target: "/api/crm/tasks",
			body:   `{"title":"x","due_at":"2030-01-01T00:00:00Z","candidate_id":"cand_99999999"}`,
			status: http.StatusNotFound,
			code:   "not_found",
		},
		{
			name:   "stale version",
			method: http.MethodPatch,
			target: "/api/crm/tasks/" + task.ID,
			body:   `{"expected_version":1,"owner":"stale"}`,
			status: http.StatusConflict,
			code:   "conflict",
		},
		{
			name:   "invalid filter",
			method: http.MethodGet,
			target: "/api/crm/tasks?overdue=1",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "inverted range",
			method: http.MethodGet,
			target: "/api/crm/tasks?due_from=2030-01-02T00:00:00Z&due_to=2030-01-01T00:00:00Z",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "unknown query",
			method: http.MethodGet,
			target: "/api/crm/tasks?sort=due_at",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "missing note target",
			method: http.MethodGet,
			target: "/api/crm/notes",
			status: http.StatusBadRequest,
			code:   "validation_error",
		},
		{
			name:   "missing task",
			method: http.MethodGet,
			target: "/api/crm/tasks/task_99999999",
			status: http.StatusNotFound,
			code:   "not_found",
		},
		{
			name:   "method not allowed",
			method: http.MethodDelete,
			target: "/api/crm/tasks",
			status: http.StatusMethodNotAllowed,
			code:   "method_not_allowed",
		},
	}
	for _, test := range errorTests {
		t.Run(test.name, func(t *testing.T) {
			response := crmActivityTestRequest(t, mux, test.method, test.target, test.body)
			crmActivityTestAssertAPIError(t, response, test.status, test.code)
			if test.code == "method_not_allowed" && response.Header().Get("Allow") != "GET, POST" {
				t.Fatalf("Allow = %q, want GET, POST", response.Header().Get("Allow"))
			}
		})
	}
}

func crmActivityTestOpenStore(t *testing.T) (*CRMStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, path
}

func crmActivityTestSeedReferences(t *testing.T, store *CRMStore) crmActivityTestFixture {
	t.Helper()
	now := time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	var fixture crmActivityTestFixture
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		var err error
		if fixture.CandidateOne, err = allocator.Next(IDKindCandidate); err != nil {
			return err
		}
		if fixture.CandidateTwo, err = allocator.Next(IDKindCandidate); err != nil {
			return err
		}
		if fixture.Job, err = allocator.Next(IDKindJob); err != nil {
			return err
		}
		if fixture.Lead, err = allocator.Next(IDKindLead); err != nil {
			return err
		}
		state.Candidates = append(
			state.Candidates,
			Candidate{
				ID:        fixture.CandidateOne,
				FullName:  "Ada",
				Skills:    []string{},
				Status:    CandidateStatusActive,
				Version:   1,
				CreatedAt: now,
				UpdatedAt: now,
			},
			Candidate{
				ID:        fixture.CandidateTwo,
				FullName:  "Grace",
				Skills:    []string{},
				Status:    CandidateStatusActive,
				Version:   1,
				CreatedAt: now,
				UpdatedAt: now,
			},
		)
		state.Jobs = append(state.Jobs, Job{
			ID:           fixture.Job,
			Title:        "Backend Engineer",
			Company:      "Example",
			Requirements: []string{},
			Status:       JobStatusOpen,
			Version:      1,
			CreatedAt:    now,
			UpdatedAt:    now,
		})
		state.Leads = append(state.Leads, Lead{
			ID:             fixture.Lead,
			CandidateID:    fixture.CandidateOne,
			JobID:          fixture.Job,
			Stage:          LeadStageInterview,
			Owner:          "alice",
			Version:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
			StageChangedAt: now,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("seed references error = %v", err)
	}
	return fixture
}

func crmActivityTestTimePointer(value time.Time) *time.Time {
	return &value
}

func crmActivityTestRequest(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func crmActivityTestAssertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d; body = %s", response.Code, status, response.Body.String())
	}
	var apiError crmActivityAPIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode API error: %v", err)
	}
	if apiError.Code != code || apiError.Error == "" {
		t.Fatalf("API error = %+v, want code %q and non-empty message", apiError, code)
	}
}
