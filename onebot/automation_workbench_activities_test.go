package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type m202ActivitiesFixture struct {
	references crmActivityTestFixture
	notes      []Note
	tasks      []FollowUpTask
	message    MessageEvent
	auditIDs   []string
}

func TestWorkbenchActivitiesReadFiltersPaginationAndExactProjections(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	fixture := m202SeedActivities(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	notes := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationNotesList,
		WorkbenchNoteListArguments{
			TargetType: NoteTargetCandidate,
			TargetID:   fixture.references.CandidateOne,
			Limit:      1,
			Offset:     1,
		},
	)
	m202AssertSuccess(t, notes)
	m202AssertPage(t, notes.Page, 1, 1, 1, 2, false, 0)
	m202AssertExactKeys(t, notes.Data, "notes")
	noteItems := m202ArrayField(t, notes.Data, "notes")
	if len(noteItems) != 1 {
		t.Fatalf("notes.list returned %d notes, want 1", len(noteItems))
	}
	m202AssertExactKeys(
		t,
		noteItems[0],
		"id",
		"target_type",
		"target_id",
		"author",
		"body",
		"created_at",
	)
	var note Note
	if err := json.Unmarshal(noteItems[0], &note); err != nil {
		t.Fatal(err)
	}
	if note != fixture.notes[1] {
		t.Fatalf("notes.list note = %+v, want %+v", note, fixture.notes[1])
	}
	notesRepeat := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationNotesList,
		WorkbenchNoteListArguments{
			TargetType: NoteTargetCandidate,
			TargetID:   fixture.references.CandidateOne,
			Limit:      1,
			Offset:     1,
		},
	)
	if !reflect.DeepEqual(notes, notesRepeat) {
		t.Fatalf("notes.list pagination is unstable: first=%+v second=%+v", notes, notesRepeat)
	}
	emptyNotes := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationNotesList,
		WorkbenchNoteListArguments{
			TargetType: NoteTargetCandidate,
			TargetID:   fixture.references.CandidateTwo,
			Limit:      WorkbenchMaxPageLimit,
		},
	)
	m202AssertSuccess(t, emptyNotes)
	m202AssertPage(
		t,
		emptyNotes.Page,
		WorkbenchMaxPageLimit,
		0,
		0,
		0,
		false,
		0,
	)
	if items := m202ArrayField(t, emptyNotes.Data, "notes"); len(items) != 0 {
		t.Fatalf("notes.list empty target returned %d notes", len(items))
	}

	owner := " alice "
	overdue := true
	taskFilters := []struct {
		name string
		args WorkbenchTaskListArguments
		want []string
	}{
		{
			name: "owner",
			args: WorkbenchTaskListArguments{
				Owner:  &owner,
				Limit:  WorkbenchMaxPageLimit,
				Offset: 0,
			},
			want: []string{fixture.tasks[0].ID, fixture.tasks[1].ID},
		},
		{
			name: "status",
			args: WorkbenchTaskListArguments{
				Status: FollowUpStatusCompleted,
				Limit:  WorkbenchMaxPageLimit,
				Offset: 0,
			},
			want: []string{fixture.tasks[2].ID},
		},
		{
			name: "overdue",
			args: WorkbenchTaskListArguments{
				Overdue: &overdue,
				Limit:   WorkbenchMaxPageLimit,
				Offset:  0,
			},
			want: []string{fixture.tasks[0].ID},
		},
		{
			name: "inclusive due range",
			args: WorkbenchTaskListArguments{
				DueFrom: &fixture.tasks[1].DueAt,
				DueTo:   &fixture.tasks[1].DueAt,
				Limit:   WorkbenchMaxPageLimit,
				Offset:  0,
			},
			want: []string{fixture.tasks[1].ID, fixture.tasks[2].ID},
		},
		{
			name: "candidate resolves through lead",
			args: WorkbenchTaskListArguments{
				CandidateID: fixture.references.CandidateOne,
				Limit:       WorkbenchMaxPageLimit,
				Offset:      0,
			},
			want: []string{fixture.tasks[0].ID, fixture.tasks[1].ID},
		},
		{
			name: "lead",
			args: WorkbenchTaskListArguments{
				LeadID: fixture.references.Lead,
				Limit:  WorkbenchMaxPageLimit,
				Offset: 0,
			},
			want: []string{fixture.tasks[1].ID},
		},
	}
	for _, test := range taskFilters {
		t.Run("tasks.list "+test.name, func(t *testing.T) {
			result := m202Dispatch(
				t,
				dispatcher,
				AutomationToolWorkbenchQuery,
				WorkbenchOperationTasksList,
				test.args,
			)
			m202AssertSuccess(t, result)
			if got := m202TaskIDs(t, result.Data); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("tasks.list IDs = %v, want %v", got, test.want)
			}
		})
	}

	taskPage := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationTasksList,
		WorkbenchTaskListArguments{Limit: 1, Offset: 1},
	)
	m202AssertSuccess(t, taskPage)
	m202AssertPage(t, taskPage.Page, 1, 1, 1, 3, true, 2)
	m202AssertExactKeys(t, taskPage.Data, "tasks")
	taskItems := m202ArrayField(t, taskPage.Data, "tasks")
	if len(taskItems) != 1 {
		t.Fatalf("tasks.list returned %d tasks, want 1", len(taskItems))
	}
	m202AssertTaskListItemKeys(t, taskItems[0])
	var taskItem TaskListItem
	if err := json.Unmarshal(taskItems[0], &taskItem); err != nil {
		t.Fatal(err)
	}
	if taskItem.Task.ID != fixture.tasks[1].ID ||
		taskItem.CandidateName != "Ada" ||
		taskItem.LeadStage != LeadStageInterview ||
		taskItem.JobID != fixture.references.Job ||
		taskItem.JobTitle != "Backend Engineer" {
		t.Fatalf("tasks.list projection = %+v", taskItem)
	}
	taskPageRepeat := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationTasksList,
		WorkbenchTaskListArguments{Limit: 1, Offset: 1},
	)
	if !reflect.DeepEqual(taskPage, taskPageRepeat) {
		t.Fatalf("tasks.list pagination is unstable: first=%+v second=%+v", taskPage, taskPageRepeat)
	}
	noOwner := "nobody"
	emptyTasks := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationTasksList,
		WorkbenchTaskListArguments{
			Owner: &noOwner,
			Limit: WorkbenchMaxPageLimit,
		},
	)
	m202AssertSuccess(t, emptyTasks)
	m202AssertPage(
		t,
		emptyTasks.Page,
		WorkbenchMaxPageLimit,
		0,
		0,
		0,
		false,
		0,
	)
	if items := m202ArrayField(t, emptyTasks.Data, "tasks"); len(items) != 0 {
		t.Fatalf("tasks.list empty filter returned %d tasks", len(items))
	}

	taskDetail := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationTasksGet,
		WorkbenchIDArguments{ID: fixture.tasks[1].ID},
	)
	m202AssertSuccess(t, taskDetail)
	m202AssertTaskListItemKeys(t, taskDetail.Data)
	var detail TaskListItem
	if err := json.Unmarshal(taskDetail.Data, &detail); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(detail, taskItem) {
		t.Fatalf("tasks.get = %+v, want list projection %+v", detail, taskItem)
	}

	audit := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCRMAuditList,
		WorkbenchAuditListArguments{
			Operation:     WorkbenchOperation("activity.audit"),
			EntityType:    "task",
			EntityID:      fixture.tasks[1].ID,
			SourceEventID: fixture.message.ID,
			Limit:         1,
			Offset:        0,
		},
	)
	m202AssertSuccess(t, audit)
	m202AssertPage(t, audit.Page, 1, 0, 1, 2, true, 1)
	m202AssertExactKeys(t, audit.Data, "audit_events")
	auditItems := m202ArrayField(t, audit.Data, "audit_events")
	if len(auditItems) != 1 {
		t.Fatalf("crm_audit.list returned %d events, want 1", len(auditItems))
	}
	m202AssertExactKeys(
		t,
		auditItems[0],
		"id",
		"actor",
		"operation",
		"entity_type",
		"entity_id",
		"source_event_id",
		"created_at",
	)
	var projected workbenchCRMAuditEventProjection
	if err := json.Unmarshal(auditItems[0], &projected); err != nil {
		t.Fatal(err)
	}
	if projected.ID != fixture.auditIDs[0] ||
		projected.Actor != "operator-a" ||
		projected.Operation != "activity.audit" ||
		projected.EntityType != "task" ||
		projected.EntityID != fixture.tasks[1].ID ||
		projected.SourceEventID != fixture.message.ID {
		t.Fatalf("crm_audit.list projection = %+v", projected)
	}
	auditRepeat := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCRMAuditList,
		WorkbenchAuditListArguments{
			Operation:     WorkbenchOperation("activity.audit"),
			EntityType:    "task",
			EntityID:      fixture.tasks[1].ID,
			SourceEventID: fixture.message.ID,
			Limit:         1,
			Offset:        0,
		},
	)
	if !reflect.DeepEqual(audit, auditRepeat) {
		t.Fatalf("crm_audit.list pagination is unstable: first=%+v second=%+v", audit, auditRepeat)
	}
	auditPastEnd := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCRMAuditList,
		WorkbenchAuditListArguments{
			Operation: WorkbenchOperation("activity.audit"),
			Limit:     WorkbenchMaxPageLimit,
			Offset:    2,
		},
	)
	m202AssertSuccess(t, auditPastEnd)
	m202AssertPage(
		t,
		auditPastEnd.Page,
		WorkbenchMaxPageLimit,
		2,
		0,
		2,
		false,
		0,
	)
	if items := m202ArrayField(t, auditPastEnd.Data, "audit_events"); len(items) != 0 {
		t.Fatalf("crm_audit.list exhausted page returned %d events", len(items))
	}
}

func TestWorkbenchActivitiesListResultsStayWithinEnvelopeAtMaximumText(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	references := crmActivityTestSeedReferences(t, store)
	now := time.Date(2026, time.July, 30, 10, 0, 0, 0, time.UTC)
	auditOperation := strings.Repeat("o", 120)
	message, _, err := crmMessageCaptureNormalizedInboundToStore(
		store,
		&ForwardRecord{
			T:      now.Unix(),
			Chat:   string(ChatTypePrivate),
			Dir:    "in",
			Peer:   "wxid_maximum_text",
			Author: "wxid_maximum_text",
			Text:   "maximum text audit source",
			Kinds:  []string{"text"},
			Mid:    "mid-maximum-text",
		},
		now,
	)
	if err != nil {
		t.Fatalf("seed maximum text message: %v", err)
	}

	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		for index := 0; index < WorkbenchMaxPageLimit; index++ {
			noteID, err := allocator.Next(IDKindNote)
			if err != nil {
				return err
			}
			state.Notes = append(state.Notes, Note{
				ID:         noteID,
				TargetType: NoteTargetCandidate,
				TargetID:   references.CandidateTwo,
				Author:     strings.Repeat("a", 80),
				Body:       strings.Repeat("b", 4000),
				CreatedAt:  now.Add(time.Duration(index) * time.Second),
			})

			taskID, err := allocator.Next(IDKindTask)
			if err != nil {
				return err
			}
			state.Tasks = append(state.Tasks, FollowUpTask{
				ID:          taskID,
				Title:       strings.Repeat("t", 200),
				Description: strings.Repeat("d", 4000),
				Owner:       strings.Repeat("o", 80),
				Status:      FollowUpStatusOpen,
				DueAt:       now.Add(time.Hour),
				CandidateID: references.CandidateTwo,
				Version:     1,
				CreatedAt:   now,
				UpdatedAt:   now,
			})

			auditID, err := allocator.Next(IDKindAudit)
			if err != nil {
				return err
			}
			state.AuditEvents = append(state.AuditEvents, CRMAuditEvent{
				ID:            auditID,
				Actor:         strings.Repeat("a", 80),
				Operation:     auditOperation,
				EntityType:    "task",
				EntityID:      taskID,
				SourceEventID: message.ID,
				CreatedAt:     now.Add(time.Duration(index) * time.Second),
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed maximum text fixtures: %v", err)
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	lists := []struct {
		name  string
		field string
		run   func(int) WorkbenchToolResult
	}{
		{
			name:  "notes.list",
			field: "notes",
			run: func(limit int) WorkbenchToolResult {
				return m202Dispatch(
					t,
					dispatcher,
					AutomationToolWorkbenchQuery,
					WorkbenchOperationNotesList,
					WorkbenchNoteListArguments{
						TargetType: NoteTargetCandidate,
						TargetID:   references.CandidateTwo,
						Limit:      limit,
					},
				)
			},
		},
		{
			name:  "tasks.list",
			field: "tasks",
			run: func(limit int) WorkbenchToolResult {
				return m202Dispatch(
					t,
					dispatcher,
					AutomationToolWorkbenchQuery,
					WorkbenchOperationTasksList,
					WorkbenchTaskListArguments{
						CandidateID: references.CandidateTwo,
						Limit:       limit,
					},
				)
			},
		},
		{
			name:  "crm_audit.list",
			field: "audit_events",
			run: func(limit int) WorkbenchToolResult {
				return m202Dispatch(
					t,
					dispatcher,
					AutomationToolWorkbenchQuery,
					WorkbenchOperationCRMAuditList,
					WorkbenchAuditListArguments{
						Operation: WorkbenchOperation(auditOperation),
						Limit:     limit,
					},
				)
			},
		},
	}

	for _, list := range lists {
		for _, pageLimit := range []struct {
			name       string
			requested  int
			normalized int
		}{
			{name: "default limit", requested: 0, normalized: WorkbenchDefaultPageLimit},
			{name: "maximum limit", requested: WorkbenchMaxPageLimit, normalized: WorkbenchMaxPageLimit},
		} {
			t.Run(list.name+" "+pageLimit.name, func(t *testing.T) {
				result := list.run(pageLimit.requested)
				m202AssertSuccess(t, result)
				if result.Page == nil ||
					result.Page.Limit != pageLimit.normalized ||
					result.Page.Offset != 0 ||
					result.Page.Returned < 1 ||
					result.Page.Returned > pageLimit.normalized ||
					result.Page.Total != WorkbenchMaxPageLimit ||
					!result.Page.HasMore ||
					result.Page.NextOffset != result.Page.Returned {
					t.Fatalf("%s page = %+v", list.name, result.Page)
				}
				if pageLimit.normalized == WorkbenchMaxPageLimit &&
					result.Page.Returned == WorkbenchMaxPageLimit {
					t.Fatalf("%s maximum-text fixture did not exercise envelope truncation", list.name)
				}
				if items := m202ArrayField(t, result.Data, list.field); len(items) != result.Page.Returned {
					t.Fatalf(
						"%s item count = %d, returned = %d",
						list.name,
						len(items),
						result.Page.Returned,
					)
				}
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if len(encoded) > WorkbenchMaxResultJSONBytes {
					t.Fatalf(
						"%s encoded result = %d bytes, maximum = %d",
						list.name,
						len(encoded),
						WorkbenchMaxResultJSONBytes,
					)
				}
				repeated := list.run(pageLimit.requested)
				if !reflect.DeepEqual(result, repeated) {
					t.Fatalf("%s bounded page is not deterministic", list.name)
				}
			})
		}
	}
}

func TestWorkbenchActivitiesWritesErrorsAndRejectedAuditAtomicity(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	fixture := m202SeedActivities(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	updatedTitle := "Updated lead follow-up"
	updatedOwner := " carol "
	beforeUpdateAudits := len(store.Snapshot().AuditEvents)
	taskUpdate := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationTasksUpdate,
		WorkbenchTaskUpdateArguments{
			TaskID: fixture.tasks[1].ID,
			Update: TaskUpdate{
				ExpectedVersion: fixture.tasks[1].Version,
				Title:           &updatedTitle,
				Owner:           &updatedOwner,
			},
		},
	)
	m202AssertSuccess(t, taskUpdate)
	m202AssertExactKeys(
		t,
		taskUpdate.Data,
		"id",
		"title",
		"description",
		"owner",
		"status",
		"due_at",
		"lead_id",
		"version",
		"created_at",
		"updated_at",
	)
	var updatedTask FollowUpTask
	if err := json.Unmarshal(taskUpdate.Data, &updatedTask); err != nil {
		t.Fatal(err)
	}
	if updatedTask.Title != updatedTitle ||
		updatedTask.Owner != "carol" ||
		updatedTask.Version != fixture.tasks[1].Version+1 {
		t.Fatalf("tasks.update = %+v", updatedTask)
	}
	if got := len(store.Snapshot().AuditEvents); got != beforeUpdateAudits+1 {
		t.Fatalf("tasks.update audit count = %d, want %d", got, beforeUpdateAudits+1)
	}

	rejectedAuditCount := len(store.Snapshot().AuditEvents)
	for attempt := 0; attempt < 2; attempt++ {
		stale := m202Dispatch(
			t,
			dispatcher,
			AutomationToolWorkbenchMutate,
			WorkbenchOperationTasksUpdate,
			WorkbenchTaskUpdateArguments{
				TaskID: fixture.tasks[1].ID,
				Update: TaskUpdate{
					ExpectedVersion: fixture.tasks[1].Version,
					Owner:           &updatedOwner,
				},
			},
		)
		m202AssertFailure(t, stale, WorkbenchErrorConflict)
	}
	if got := len(store.Snapshot().AuditEvents); got != rejectedAuditCount {
		t.Fatalf("stale tasks.update wrote audits: got %d, want %d", got, rejectedAuditCount)
	}

	emptyTitle := " "
	invalidUpdate := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationTasksUpdate,
		WorkbenchTaskUpdateArguments{
			TaskID: updatedTask.ID,
			Update: TaskUpdate{
				ExpectedVersion: updatedTask.Version,
				Title:           &emptyTitle,
			},
		},
	)
	m202AssertFailure(t, invalidUpdate, WorkbenchErrorInvalidArguments)
	if got := len(store.Snapshot().AuditEvents); got != rejectedAuditCount {
		t.Fatalf("invalid tasks.update wrote audits: got %d, want %d", got, rejectedAuditCount)
	}

	beforeReviewAudits := len(store.Snapshot().AuditEvents)
	review := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationMessagesReview,
		WorkbenchMessageReviewArguments{
			MessageID:       fixture.message.ID,
			ExpectedVersion: fixture.message.Version,
			ReviewStatus:    ReviewStatusReviewed,
		},
	)
	m202AssertSuccess(t, review)
	m202AssertExactKeys(
		t,
		review.Data,
		"id",
		"message_key",
		"source",
		"mid",
		"chat_type",
		"peer_id",
		"author_id",
		"text",
		"kinds",
		"timestamp",
		"suspicious",
		"disposition",
		"review_status",
		"version",
		"created_at",
		"updated_at",
	)
	var reviewed crmMessageEventProjection
	if err := json.Unmarshal(review.Data, &reviewed); err != nil {
		t.Fatal(err)
	}
	if reviewed.ID != fixture.message.ID ||
		reviewed.ReviewStatus != ReviewStatusReviewed ||
		reviewed.Version != fixture.message.Version+1 ||
		reviewed.JobIngestion != nil {
		t.Fatalf("messages.review = %+v", reviewed)
	}
	if got := len(store.Snapshot().AuditEvents); got != beforeReviewAudits+1 {
		t.Fatalf("messages.review audit count = %d, want %d", got, beforeReviewAudits+1)
	}

	rejectedAuditCount = len(store.Snapshot().AuditEvents)
	for attempt := 0; attempt < 2; attempt++ {
		stale := m202Dispatch(
			t,
			dispatcher,
			AutomationToolWorkbenchMutate,
			WorkbenchOperationMessagesReview,
			WorkbenchMessageReviewArguments{
				MessageID:       fixture.message.ID,
				ExpectedVersion: fixture.message.Version,
				ReviewStatus:    ReviewStatusReviewed,
			},
		)
		m202AssertFailure(t, stale, WorkbenchErrorConflict)
	}
	if got := len(store.Snapshot().AuditEvents); got != rejectedAuditCount {
		t.Fatalf("stale messages.review wrote audits: got %d, want %d", got, rejectedAuditCount)
	}

	var linked MessageEvent
	err := store.Update(func(state *CRMState, _ *IDAllocator) error {
		for index := range state.MessageEvents {
			if state.MessageEvents[index].ID != fixture.message.ID {
				continue
			}
			state.MessageEvents[index].CandidateID = fixture.references.CandidateOne
			state.MessageEvents[index].ReviewStatus = ReviewStatusReviewed
			state.MessageEvents[index].Version++
			linked = state.MessageEvents[index]
			return nil
		}
		return crmMessageErrNotFound
	})
	if err != nil {
		t.Fatal(err)
	}
	rejectedAuditCount = len(store.Snapshot().AuditEvents)
	linkedInvalid := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationMessagesReview,
		WorkbenchMessageReviewArguments{
			MessageID:       linked.ID,
			ExpectedVersion: linked.Version,
			ReviewStatus:    ReviewStatusUnreviewed,
		},
	)
	m202AssertFailure(t, linkedInvalid, WorkbenchErrorInvalidArguments)
	if got := len(store.Snapshot().AuditEvents); got != rejectedAuditCount {
		t.Fatalf("linked invalid messages.review wrote audits: got %d, want %d", got, rejectedAuditCount)
	}
}

func TestWorkbenchMessagesReviewParityPersistenceRollbackAndIngestionProjection(t *testing.T) {
	t.Run("persistence rollback", func(t *testing.T) {
		store := crmMessageOpenTestStore(t)
		fixture := m202SeedActivities(t, store)
		before := store.Snapshot()

		blockingFile := filepath.Join(t.TempDir(), "blocking-file")
		if err := os.WriteFile(blockingFile, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		store.path = filepath.Join(blockingFile, "crm.json")

		result := m202Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store),
			AutomationToolWorkbenchMutate,
			WorkbenchOperationMessagesReview,
			WorkbenchMessageReviewArguments{
				MessageID:       fixture.message.ID,
				ExpectedVersion: fixture.message.Version,
				ReviewStatus:    ReviewStatusReviewed,
			},
		)
		m202AssertFailure(t, result, WorkbenchErrorUnavailable)
		if !result.Error.Retryable {
			t.Fatalf("persistence failure = %+v, want retryable", result.Error)
		}
		after := store.Snapshot()
		if !reflect.DeepEqual(before.MessageEvents, after.MessageEvents) ||
			!reflect.DeepEqual(before.AuditEvents, after.AuditEvents) {
			t.Fatalf("failed messages.review changed persisted state")
		}
	})

	t.Run("job ingestion projection", func(t *testing.T) {
		store := crmJobIngestionOpenStore(t)
		groupID := "workbench-review@chatroom"
		crmJobIngestionSeedSourceGroup(t, store, groupID, true)
		event := crmJobIngestionSeedEvent(
			t,
			store,
			groupID,
			"mid-workbench-review",
			"workbench review source job",
		)
		ingested, err := store.IngestSourceJob(SourceJobIngestion{
			EventID: event.ID,
			GroupID: groupID,
			Job:     crmJobIngestionDraft("SYN-WORKBENCH-REVIEW"),
		})
		if err != nil {
			t.Fatalf("ingest source job: %v", err)
		}
		current, found := crmMessageFindEvent(store.Snapshot(), event.ID)
		if !found {
			t.Fatalf("message event %q not found after ingestion", event.ID)
		}

		result := m202Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store),
			AutomationToolWorkbenchMutate,
			WorkbenchOperationMessagesReview,
			WorkbenchMessageReviewArguments{
				MessageID:       current.ID,
				ExpectedVersion: current.Version,
				ReviewStatus:    ReviewStatusReviewed,
			},
		)
		m202AssertSuccess(t, result)
		var projected crmMessageEventProjection
		if err := json.Unmarshal(result.Data, &projected); err != nil {
			t.Fatal(err)
		}
		if projected.Version != current.Version+1 ||
			projected.ReviewStatus != ReviewStatusReviewed ||
			projected.JobIngestion == nil ||
			projected.JobIngestion.Decision != ingested.Decision ||
			projected.JobIngestion.Provenance != ingested.Provenance {
			t.Fatalf("messages.review ingestion projection = %+v", projected)
		}
	})
}

func TestWorkbenchActivitiesNotFoundInvalidArgumentsNilCRMAndPermissionDeny(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	fixture := m202SeedActivities(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	notFound := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
	}{
		{
			name:      "notes.list",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationNotesList,
			arguments: WorkbenchNoteListArguments{
				TargetType: NoteTargetCandidate,
				TargetID:   "cand_99999999",
				Limit:      1,
			},
		},
		{
			name:      "tasks.get",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationTasksGet,
			arguments: WorkbenchIDArguments{ID: "task_99999999"},
		},
		{
			name:      "tasks.update",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationTasksUpdate,
			arguments: WorkbenchTaskUpdateArguments{
				TaskID: "task_99999999",
				Update: TaskUpdate{ExpectedVersion: 1},
			},
		},
		{
			name:      "messages.review",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationMessagesReview,
			arguments: WorkbenchMessageReviewArguments{
				MessageID:       "event_99999999",
				ExpectedVersion: 1,
				ReviewStatus:    ReviewStatusReviewed,
			},
		},
	}
	for _, test := range notFound {
		t.Run(test.name+" not_found", func(t *testing.T) {
			result := m202Dispatch(t, dispatcher, test.tool, test.operation, test.arguments)
			m202AssertFailure(t, result, WorkbenchErrorNotFound)
		})
	}

	dueFrom := fixture.tasks[1].DueAt.Add(time.Hour)
	dueTo := fixture.tasks[1].DueAt
	invalidRange := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationTasksList,
		WorkbenchTaskListArguments{
			DueFrom: &dueFrom,
			DueTo:   &dueTo,
			Limit:   1,
		},
	)
	m202AssertFailure(t, invalidRange, WorkbenchErrorInvalidArguments)

	invalidAuditFilter := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCRMAuditList,
		WorkbenchAuditListArguments{
			ActionID: "aiaction_00000001",
			Limit:    1,
		},
	)
	m202AssertFailure(t, invalidAuditFilter, WorkbenchErrorInvalidArguments)

	decodeErrors := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
	}{
		{
			name:      "invalid note target",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationNotesList,
			arguments: WorkbenchNoteListArguments{
				TargetType: NoteTargetType("job"),
				TargetID:   fixture.references.Job,
				Limit:      1,
			},
		},
		{
			name:      "limit too large",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationTasksList,
			arguments: WorkbenchTaskListArguments{Limit: WorkbenchMaxPageLimit + 1},
		},
		{
			name:      "negative offset",
			tool:      AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationCRMAuditList,
			arguments: WorkbenchAuditListArguments{Limit: 1, Offset: -1},
		},
		{
			name:      "zero task version",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationTasksUpdate,
			arguments: WorkbenchTaskUpdateArguments{
				TaskID: fixture.tasks[0].ID,
				Update: TaskUpdate{},
			},
		},
		{
			name:      "invalid review status",
			tool:      AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationMessagesReview,
			arguments: WorkbenchMessageReviewArguments{
				MessageID:       fixture.message.ID,
				ExpectedVersion: fixture.message.Version,
				ReviewStatus:    ReviewStatus("pending"),
			},
		},
	}
	for _, test := range decodeErrors {
		t.Run(test.name, func(t *testing.T) {
			if _, err := m202Decode(test.tool, test.operation, test.arguments); err == nil {
				t.Fatalf("%s/%s accepted invalid arguments", test.tool, test.operation)
			}
		})
	}

	owner := "owner"
	nilCRM := NewDefaultWorkbenchToolDispatcher(nil)
	nilCases := []struct {
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
	}{
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationNotesList,
			WorkbenchNoteListArguments{
				TargetType: NoteTargetCandidate,
				TargetID:   "cand_99999999",
				Limit:      1,
			},
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationTasksList,
			WorkbenchTaskListArguments{Limit: 1},
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationTasksGet,
			WorkbenchIDArguments{ID: "task_99999999"},
		},
		{
			AutomationToolWorkbenchMutate,
			WorkbenchOperationTasksUpdate,
			WorkbenchTaskUpdateArguments{
				TaskID: "task_99999999",
				Update: TaskUpdate{ExpectedVersion: 1, Owner: &owner},
			},
		},
		{
			AutomationToolWorkbenchMutate,
			WorkbenchOperationMessagesReview,
			WorkbenchMessageReviewArguments{
				MessageID:       "event_99999999",
				ExpectedVersion: 1,
				ReviewStatus:    ReviewStatusReviewed,
			},
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationCRMAuditList,
			WorkbenchAuditListArguments{Limit: 1},
		},
	}
	for _, test := range nilCases {
		t.Run(string(test.operation)+" nil CRM", func(t *testing.T) {
			result := m202Dispatch(t, nilCRM, test.tool, test.operation, test.arguments)
			m202AssertFailure(t, result, WorkbenchErrorUnavailable)
			if !result.Error.Retryable || result.Error.Message != "CRM store is unavailable" {
				t.Fatalf("%s nil CRM error = %+v", test.operation, result.Error)
			}
		})
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{
		workbenchOperationDenyKey(
			AutomationToolWorkbenchMutate,
			WorkbenchOperationTasksUpdate,
		): {},
		workbenchOperationDenyKey(
			AutomationToolWorkbenchMutate,
			WorkbenchOperationMessagesReview,
		): {},
	})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	beforeDenied := store.Snapshot()
	deniedTask := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationTasksUpdate,
		WorkbenchTaskUpdateArguments{
			TaskID: fixture.tasks[0].ID,
			Update: TaskUpdate{
				ExpectedVersion: fixture.tasks[0].Version,
				Owner:           &owner,
			},
		},
	)
	m202AssertFailure(t, deniedTask, WorkbenchErrorUnavailable)
	deniedReview := m202Dispatch(
		t,
		dispatcher,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationMessagesReview,
		WorkbenchMessageReviewArguments{
			MessageID:       fixture.message.ID,
			ExpectedVersion: fixture.message.Version,
			ReviewStatus:    ReviewStatusReviewed,
		},
	)
	m202AssertFailure(t, deniedReview, WorkbenchErrorUnavailable)
	afterDenied := store.Snapshot()
	if !reflect.DeepEqual(beforeDenied.Tasks, afterDenied.Tasks) ||
		!reflect.DeepEqual(beforeDenied.MessageEvents, afterDenied.MessageEvents) ||
		len(afterDenied.AuditEvents) != len(beforeDenied.AuditEvents) {
		t.Fatalf(
			"permission-denied writes changed CRM: tasks=%t messages=%t audits=%d->%d",
			!reflect.DeepEqual(beforeDenied.Tasks, afterDenied.Tasks),
			!reflect.DeepEqual(beforeDenied.MessageEvents, afterDenied.MessageEvents),
			len(beforeDenied.AuditEvents),
			len(afterDenied.AuditEvents),
		)
	}
}

func TestWorkbenchCRMAuditListStrictSanitization(t *testing.T) {
	safe := CRMAuditEvent{
		ID:            "audit_00000001",
		Actor:         "operator-a",
		Operation:     "activity.audit",
		EntityType:    "task",
		EntityID:      "task_00000001",
		SourceEventID: "event_00000001",
		CreatedAt:     time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC),
	}
	fixtures := []struct {
		name  string
		value string
		set   func(*CRMAuditEvent, string)
	}{
		{
			name:  "id camel case api key",
			value: "apiKeyAIzaSensitiveValue",
			set:   func(event *CRMAuditEvent, value string) { event.ID = value },
		},
		{
			name:  "actor concatenated access token",
			value: "accessTokenSensitiveValue",
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
		{
			name:  "operation concatenated client secret",
			value: "clientSecretSensitiveValue",
			set:   func(event *CRMAuditEvent, value string) { event.Operation = value },
		},
		{
			name:  "entity type provider payload",
			value: "providerPayloadSensitiveValue",
			set:   func(event *CRMAuditEvent, value string) { event.EntityType = value },
		},
		{
			name:  "entity id gitlab token",
			value: "glpat-sensitive-value",
			set:   func(event *CRMAuditEvent, value string) { event.EntityID = value },
		},
		{
			name:  "source event id opt path",
			value: "/opt/hr/config.json",
			set:   func(event *CRMAuditEvent, value string) { event.SourceEventID = value },
		},
		{
			name:  "credential assignment",
			value: "apikey=AIzaSensitiveValue",
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
		{
			name:  "unix absolute path",
			value: "/etc/workbench/config.json",
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
		{
			name:  "windows drive absolute path",
			value: `C:\workbench\config.json`,
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
		{
			name:  "windows unc absolute path",
			value: `\\server\share\config.json`,
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
		{
			name:  "overlong actor",
			value: strings.Repeat("a", 81),
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
		{
			name:  "unsafe punctuation",
			value: "operator/a",
			set:   func(event *CRMAuditEvent, value string) { event.Actor = value },
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			store := crmMessageOpenTestStore(t)
			event := safe
			fixture.set(&event, fixture.value)
			store.mu.Lock()
			store.state.AuditEvents = append(store.state.AuditEvents, event)
			store.mu.Unlock()

			result := m202Dispatch(
				t,
				NewDefaultWorkbenchToolDispatcher(store),
				AutomationToolWorkbenchQuery,
				WorkbenchOperationCRMAuditList,
				WorkbenchAuditListArguments{Limit: 10},
			)
			m202AssertFailure(t, result, WorkbenchErrorInternal)
			if result.Error.Message != "CRM operation failed" {
				t.Fatalf("unsafe audit error message = %q", result.Error.Message)
			}
			if strings.Contains(strings.ToLower(result.Error.Message), strings.ToLower(fixture.value)) {
				t.Fatalf("sensitive audit text escaped in error: %+v", result.Error)
			}
		})
	}
}

func TestWorkbenchActivitiesConcurrentVersionedWritesSingleWinner(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	fixture := m202SeedActivities(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	owner := "concurrent-owner"
	taskRequest, err := m202Decode(
		AutomationToolWorkbenchMutate,
		WorkbenchOperationTasksUpdate,
		WorkbenchTaskUpdateArguments{
			TaskID: fixture.tasks[0].ID,
			Update: TaskUpdate{
				ExpectedVersion: fixture.tasks[0].Version,
				Owner:           &owner,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeTaskAudits := len(store.Snapshot().AuditEvents)
	taskResults := m202ConcurrentDispatch(t, dispatcher, taskRequest)
	m202AssertOneSuccessOneConflict(t, taskResults)
	afterTask := store.Snapshot()
	if len(afterTask.AuditEvents) != beforeTaskAudits+1 {
		t.Fatalf(
			"concurrent tasks.update audits = %d, want %d",
			len(afterTask.AuditEvents),
			beforeTaskAudits+1,
		)
	}
	var currentTask FollowUpTask
	for _, task := range afterTask.Tasks {
		if task.ID == fixture.tasks[0].ID {
			currentTask = task
			break
		}
	}
	if currentTask.Version != fixture.tasks[0].Version+1 || currentTask.Owner != owner {
		t.Fatalf("concurrent tasks.update stored task = %+v", currentTask)
	}

	reviewRequest, err := m202Decode(
		AutomationToolWorkbenchMutate,
		WorkbenchOperationMessagesReview,
		WorkbenchMessageReviewArguments{
			MessageID:       fixture.message.ID,
			ExpectedVersion: fixture.message.Version,
			ReviewStatus:    ReviewStatusReviewed,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	beforeReviewAudits := len(afterTask.AuditEvents)
	reviewResults := m202ConcurrentDispatch(t, dispatcher, reviewRequest)
	m202AssertOneSuccessOneConflict(t, reviewResults)
	afterReview := store.Snapshot()
	if len(afterReview.AuditEvents) != beforeReviewAudits+1 {
		t.Fatalf(
			"concurrent messages.review audits = %d, want %d",
			len(afterReview.AuditEvents),
			beforeReviewAudits+1,
		)
	}
	currentMessage, found := crmMessageFindEvent(afterReview, fixture.message.ID)
	if !found ||
		currentMessage.Version != fixture.message.Version+1 ||
		currentMessage.ReviewStatus != ReviewStatusReviewed {
		t.Fatalf("concurrent messages.review stored event = %+v, found=%t", currentMessage, found)
	}
}

func m202SeedActivities(t *testing.T, store *CRMStore) m202ActivitiesFixture {
	t.Helper()
	references := crmActivityTestSeedReferences(t, store)
	service := crmActivityNewService(store)
	noteTime := time.Date(2026, time.July, 30, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return noteTime }

	noteOne, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetCandidate,
		TargetID:   references.CandidateOne,
		Author:     "operator-a",
		Body:       "first note",
	})
	if err != nil {
		t.Fatal(err)
	}
	noteTwo, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetCandidate,
		TargetID:   references.CandidateOne,
		Author:     "operator-b",
		Body:       "second note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateNote(NoteCreate{
		TargetType: NoteTargetLead,
		TargetID:   references.Lead,
		Author:     "operator-c",
		Body:       "lead note",
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	overdue, err := service.CreateTask(TaskCreate{
		Title:       "Overdue candidate task",
		Description: "call candidate",
		Owner:       "alice",
		DueAt:       now.Add(-2 * time.Hour),
		CandidateID: references.CandidateOne,
	})
	if err != nil {
		t.Fatal(err)
	}
	sharedDue := now.Add(2 * time.Hour)
	leadTask, err := service.CreateTask(TaskCreate{
		Title:       "Lead follow-up",
		Description: "prepare interview",
		Owner:       "alice",
		DueAt:       sharedDue,
		LeadID:      references.Lead,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := service.CreateTask(TaskCreate{
		Title:       "Second candidate task",
		Description: "send details",
		Owner:       "bob",
		DueAt:       sharedDue,
		CandidateID: references.CandidateTwo,
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err = service.SetTaskStatus(completed.ID, TaskStatusRequest{
		ExpectedVersion: completed.Version,
		Status:          FollowUpStatusCompleted,
	})
	if err != nil {
		t.Fatal(err)
	}

	message, _, err := crmMessageCaptureNormalizedInboundToStore(
		store,
		&ForwardRecord{
			T:      1_700_000_000,
			Chat:   string(ChatTypePrivate),
			Dir:    "in",
			Peer:   "wxid_activity_peer",
			Author: "wxid_activity_peer",
			Text:   "activity review fixture",
			Kinds:  []string{"text"},
			Mid:    "mid-activity-review",
		},
		now,
	)
	if err != nil {
		t.Fatal(err)
	}

	auditTime := time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC)
	auditIDs := make([]string, 0, 2)
	err = store.UpdateWithMeta(
		CRMUpdateMeta{Actor: "fixture", Operation: "fixture.commit"},
		func(state *CRMState, allocator *IDAllocator) error {
			for _, actor := range []string{"operator-a", "operator-b"} {
				id, allocateErr := allocator.Next(IDKindAudit)
				if allocateErr != nil {
					return allocateErr
				}
				state.AuditEvents = append(state.AuditEvents, CRMAuditEvent{
					ID:            id,
					Actor:         actor,
					Operation:     "activity.audit",
					EntityType:    "task",
					EntityID:      leadTask.ID,
					SourceEventID: message.ID,
					CreatedAt:     auditTime,
				})
				auditIDs = append(auditIDs, id)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	return m202ActivitiesFixture{
		references: references,
		notes:      []Note{noteOne, noteTwo},
		tasks:      []FollowUpTask{overdue, leadTask, completed},
		message:    message,
		auditIDs:   auditIDs,
	}
}

func m202Dispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	decoded, err := m202Decode(tool, operation, arguments)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", tool, operation, err)
	}
	return dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
}

func m202Decode(
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) (WorkbenchDecodedToolRequest, error) {
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	return DecodeWorkbenchToolRequest(tool, raw)
}

func m202AssertSuccess(t *testing.T, result WorkbenchToolResult) {
	t.Helper()
	if !result.OK || result.Error != nil {
		t.Fatalf("result = %+v, want success", result)
	}
}

func m202AssertFailure(
	t *testing.T,
	result WorkbenchToolResult,
	code WorkbenchErrorCode,
) {
	t.Helper()
	if result.OK || result.Error == nil || result.Error.Code != code {
		t.Fatalf("result = %+v, want failure %s", result, code)
	}
	if len(result.Data) != 0 || result.Page != nil {
		t.Fatalf("failure returned data or page: %+v", result)
	}
}

func m202AssertPage(
	t *testing.T,
	page *WorkbenchPage,
	limit int,
	offset int,
	returned int,
	total int,
	hasMore bool,
	nextOffset int,
) {
	t.Helper()
	want := &WorkbenchPage{
		Limit:      limit,
		Offset:     offset,
		Returned:   returned,
		Total:      total,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("page = %+v, want %+v", page, want)
	}
}

func m202AssertExactKeys(t *testing.T, raw json.RawMessage, want ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode object keys: %v; data=%s", err, raw)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	want = append([]string(nil), want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v; data=%s", got, want, raw)
	}
}

func m202ArrayField(
	t *testing.T,
	raw json.RawMessage,
	field string,
) []json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(object[field], &items); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return items
}

func m202AssertTaskListItemKeys(t *testing.T, raw json.RawMessage) {
	t.Helper()
	m202AssertExactKeys(
		t,
		raw,
		"task",
		"candidate_name",
		"lead_stage",
		"job_id",
		"job_title",
	)
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatal(err)
	}
	m202AssertExactKeys(
		t,
		item["task"],
		"id",
		"title",
		"description",
		"owner",
		"status",
		"due_at",
		"lead_id",
		"version",
		"created_at",
		"updated_at",
	)
}

func m202TaskIDs(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	items := m202ArrayField(t, raw, "tasks")
	ids := make([]string, len(items))
	for index, rawItem := range items {
		var item TaskListItem
		if err := json.Unmarshal(rawItem, &item); err != nil {
			t.Fatal(err)
		}
		ids[index] = item.Task.ID
	}
	return ids
}

func m202ConcurrentDispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	decoded WorkbenchDecodedToolRequest,
) []WorkbenchToolResult {
	t.Helper()
	start := make(chan struct{})
	results := make(chan WorkbenchToolResult, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- dispatcher.Dispatch(
				context.Background(),
				WorkbenchExecutionContext{},
				decoded,
			)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	collected := make([]WorkbenchToolResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func m202AssertOneSuccessOneConflict(
	t *testing.T,
	results []WorkbenchToolResult,
) {
	t.Helper()
	successes := 0
	conflicts := 0
	for _, result := range results {
		switch {
		case result.OK:
			successes++
		case result.Error != nil && result.Error.Code == WorkbenchErrorConflict:
			conflicts++
		default:
			t.Fatalf("concurrent dispatch result = %+v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"concurrent results: successes=%d conflicts=%d, want 1 and 1",
			successes,
			conflicts,
		)
	}
}
