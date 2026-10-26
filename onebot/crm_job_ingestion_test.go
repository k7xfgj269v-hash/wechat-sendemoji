package main

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var crmJobIngestionTestTime = time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)

func TestCRMStoreIngestSourceJobLifecycleAndImmutableProvenance(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	groupID := "synthetic_jobs@chatroom"
	crmJobIngestionSeedSourceGroup(t, store, groupID, true)

	draft := crmJobIngestionDraft("SYN-LINE-001")
	event := crmJobIngestionSeedEvent(t, store, groupID, "mid-create", "synthetic line order")
	created, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: event.ID,
		GroupID: groupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(create) error = %v", err)
	}
	if created.Decision != JobIngestionCreate ||
		created.Idempotent ||
		created.Job.ID != "job_00000001" ||
		created.Job.Version != 1 ||
		created.Job.DemandRevisionNumber != 1 ||
		created.Provenance.ID != "provenance_00000001" ||
		created.Provenance.JobRevision != 1 {
		t.Fatalf("create result = %+v", created)
	}
	if created.Job.Status != JobStatusOpen ||
		created.Job.Requirements[0] != "assembly" ||
		created.Job.RequiredDocuments[0] != "ID" {
		t.Fatalf("created normalized job = %+v", created.Job)
	}
	if len(created.Provenance.ContentSHA256) != 64 ||
		len(created.Provenance.CanonicalFingerprint) != 64 ||
		created.Provenance.EventID != event.ID ||
		created.Provenance.MessageKey != event.MessageKey ||
		created.Provenance.GroupID != event.GroupID ||
		created.Provenance.AuthorID != event.AuthorID ||
		!created.Provenance.SourceTimestamp.Equal(time.Unix(event.Timestamp, 0).UTC()) {
		t.Fatalf("create provenance = %+v", created.Provenance)
	}

	idempotent, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: event.ID,
		GroupID: groupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(idempotent) error = %v", err)
	}
	if !idempotent.Idempotent ||
		idempotent.Decision != JobIngestionCreate ||
		idempotent.Job.ID != created.Job.ID ||
		idempotent.Provenance.ID != created.Provenance.ID {
		t.Fatalf("idempotent result = %+v", idempotent)
	}

	duplicateEvent := crmJobIngestionSeedEvent(
		t, store, groupID, "mid-duplicate", "synthetic line order",
	)
	duplicate, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: duplicateEvent.ID,
		GroupID: groupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(duplicate) error = %v", err)
	}
	if duplicate.Decision != JobIngestionDuplicate ||
		duplicate.Job.ID != created.Job.ID ||
		duplicate.Job.Version != 1 ||
		duplicate.Provenance.JobRevision != 1 {
		t.Fatalf("duplicate result = %+v", duplicate)
	}

	noopDraft := draft
	noopDraft.Title = "  Synthetic Operator  "
	noopDraft.Requirements = []string{"Go", "assembly"}
	noopEvent := crmJobIngestionSeedEvent(
		t, store, groupID, "mid-noop", "same normalized job with different wording",
	)
	noop, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: noopEvent.ID,
		GroupID: groupID,
		Job:     noopDraft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(noop) error = %v", err)
	}
	if noop.Decision != JobIngestionNoop ||
		noop.Job.ID != created.Job.ID ||
		noop.Job.Version != 1 ||
		noop.Provenance.JobRevision != 1 ||
		noop.Provenance.CanonicalFingerprint != created.Provenance.CanonicalFingerprint {
		t.Fatalf("noop result = %+v", noop)
	}

	updatedDraft := draft
	updatedDraft.Headcount = 24
	updatedDraft.Description = "Updated synthetic demand"
	updateEvent := crmJobIngestionSeedEvent(
		t, store, groupID, "mid-update", "synthetic line order updated to 24",
	)
	updated, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: updateEvent.ID,
		GroupID: groupID,
		Job:     updatedDraft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(update) error = %v", err)
	}
	if updated.Decision != JobIngestionUpdate ||
		updated.Job.ID != created.Job.ID ||
		updated.Job.Version != 2 ||
		updated.Job.Headcount != 24 ||
		updated.Job.DemandRevisionNumber != 2 ||
		updated.Provenance.JobRevision != 2 {
		t.Fatalf("update result = %+v", updated)
	}

	state := store.Snapshot()
	if len(state.Jobs) != 1 ||
		len(state.DemandRevisions) != 2 ||
		len(state.JobProvenance) != 4 ||
		state.MessageEvents[0].Disposition != MessageDispositionSourceGroupJob {
		t.Fatalf("lifecycle state = jobs:%d revisions:%d provenance:%d event:%+v",
			len(state.Jobs),
			len(state.DemandRevisions),
			len(state.JobProvenance),
			state.MessageEvents[0],
		)
	}
	if !reflect.DeepEqual(updated.Job, state.Jobs[0]) {
		t.Fatalf("returned updated job differs from persisted job: result=%+v persisted=%+v",
			updated.Job,
			state.Jobs[0],
		)
	}
	before := state.JobProvenance[0]
	err = store.Update(func(state *CRMState, _ *IDAllocator) error {
		state.JobProvenance[0].Decision = JobIngestionFailure
		return nil
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("mutate provenance error = %v, want conflict", err)
	}
	if got := store.Snapshot().JobProvenance[0]; got != before {
		t.Fatalf("immutable provenance changed: got %+v, want %+v", got, before)
	}
}

func TestCRMStoreIngestSourceJobFingerprintFallbackAndManualIsolation(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	groupID := "synthetic_fallback@chatroom"
	crmJobIngestionSeedSourceGroup(t, store, groupID, true)

	manual, err := crmPipelineCreateJob(store, crmJobIngestionDraft("SYN-MANUAL-001"))
	if err != nil {
		t.Fatalf("crmPipelineCreateJob() error = %v", err)
	}
	event := crmJobIngestionSeedEvent(t, store, groupID, "mid-authoritative", "authoritative copy")
	authoritative, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: event.ID,
		GroupID: groupID,
		Job:     crmJobIngestionDraft("SYN-MANUAL-001"),
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(authoritative) error = %v", err)
	}
	if authoritative.Decision != JobIngestionCreate ||
		authoritative.Job.ID == manual.ID {
		t.Fatalf("manual job was silently claimed: manual=%+v result=%+v", manual, authoritative)
	}

	fallbackDraft := crmJobIngestionDraft("")
	firstEvent := crmJobIngestionSeedEvent(t, store, groupID, "mid-fallback-1", "fallback order")
	first, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: firstEvent.ID,
		GroupID: groupID,
		Job:     fallbackDraft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(fallback create) error = %v", err)
	}
	secondEvent := crmJobIngestionSeedEvent(t, store, groupID, "mid-fallback-2", "fallback order")
	second, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: secondEvent.ID,
		GroupID: groupID,
		Job:     fallbackDraft,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(fallback duplicate) error = %v", err)
	}
	if second.Decision != JobIngestionDuplicate || second.Job.ID != first.Job.ID {
		t.Fatalf("fallback duplicate = %+v, first = %+v", second, first)
	}

	changed := fallbackDraft
	changed.Headcount++
	changedEvent := crmJobIngestionSeedEvent(t, store, groupID, "mid-fallback-3", "changed fallback")
	changedResult, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: changedEvent.ID,
		GroupID: groupID,
		Job:     changed,
	})
	if err != nil {
		t.Fatalf("IngestSourceJob(changed fallback) error = %v", err)
	}
	if changedResult.Decision != JobIngestionCreate || changedResult.Job.ID == first.Job.ID {
		t.Fatalf("changed fallback should create a new identity: %+v", changedResult)
	}
}

func TestCRMStoreIngestSourceJobValidatesConfiguredEnabledSourceAndBounds(t *testing.T) {
	longTitle := strings.Repeat("x", 161)
	tests := []struct {
		name      string
		configure bool
		enabled   bool
		groupID   string
		input     func(MessageEvent) SourceJobIngestion
	}{
		{
			name:      "unconfigured source",
			configure: false,
			groupID:   "synthetic_unconfigured@chatroom",
		},
		{
			name:      "disabled source",
			configure: true,
			enabled:   false,
			groupID:   "synthetic_disabled@chatroom",
		},
		{
			name:      "group mismatch",
			configure: true,
			enabled:   true,
			groupID:   "synthetic_mismatch@chatroom",
			input: func(event MessageEvent) SourceJobIngestion {
				return SourceJobIngestion{
					EventID: event.ID,
					GroupID: "other_synthetic@chatroom",
					Job:     crmJobIngestionDraft("SYN-BOUNDS-001"),
				}
			},
		},
		{
			name:      "job bounds",
			configure: true,
			enabled:   true,
			groupID:   "synthetic_bounds@chatroom",
			input: func(event MessageEvent) SourceJobIngestion {
				draft := crmJobIngestionDraft("SYN-BOUNDS-002")
				draft.Title = longTitle
				return SourceJobIngestion{EventID: event.ID, GroupID: event.GroupID, Job: draft}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := crmJobIngestionOpenStore(t)
			if test.configure {
				crmJobIngestionSeedSourceGroup(t, store, test.groupID, test.enabled)
			}
			event := crmJobIngestionSeedEvent(t, store, test.groupID, "mid-validation", "invalid source")
			input := SourceJobIngestion{
				EventID: event.ID,
				GroupID: event.GroupID,
				Job:     crmJobIngestionDraft("SYN-BOUNDS-001"),
			}
			if test.input != nil {
				input = test.input(event)
			}
			if _, err := store.IngestSourceJob(input); !errors.Is(err, ErrCRMValidation) {
				t.Fatalf("IngestSourceJob() error = %v, want validation error", err)
			}
			state := store.Snapshot()
			if len(state.Jobs) != 0 || len(state.JobProvenance) != 0 {
				t.Fatalf("invalid ingestion mutated state: jobs=%d provenance=%d",
					len(state.Jobs),
					len(state.JobProvenance),
				)
			}
		})
	}

	t.Run("non-WeChat event", func(t *testing.T) {
		store := crmJobIngestionOpenStore(t)
		groupID := "synthetic_non_wechat@chatroom"
		crmJobIngestionSeedSourceGroup(t, store, groupID, true)
		event := crmJobIngestionSeedEvent(t, store, groupID, "mid-non-wechat", "invalid source")
		err := store.Update(func(state *CRMState, _ *IDAllocator) error {
			state.MessageEvents[0].Source = "synthetic"
			return nil
		})
		if err != nil {
			t.Fatalf("replace synthetic event source error = %v", err)
		}
		if _, err := store.IngestSourceJob(SourceJobIngestion{
			EventID: event.ID,
			GroupID: groupID,
			Job:     crmJobIngestionDraft("SYN-NON-WECHAT"),
		}); !errors.Is(err, ErrCRMValidation) {
			t.Fatalf("IngestSourceJob(non-WeChat) error = %v, want validation error", err)
		}
	})
}

func TestCRMStoreIngestSourceJobConcurrentIdentityAndEventIdempotency(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	groupID := "synthetic_concurrent@chatroom"
	crmJobIngestionSeedSourceGroup(t, store, groupID, true)
	draft := crmJobIngestionDraft("SYN-CONCURRENT-001")

	const workers = 16
	events := make([]MessageEvent, workers)
	for index := range events {
		events[index] = crmJobIngestionSeedEvent(
			t,
			store,
			groupID,
			"mid-concurrent-"+string(rune('a'+index)),
			"concurrent synthetic order",
		)
	}
	var wg sync.WaitGroup
	results := make(chan JobIngestionResult, workers)
	errs := make(chan error, workers)
	for index := range events {
		wg.Add(1)
		go func(event MessageEvent) {
			defer wg.Done()
			result, err := store.IngestSourceJob(SourceJobIngestion{
				EventID: event.ID,
				GroupID: groupID,
				Job:     draft,
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(events[index])
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent identity ingestion error = %v", err)
	}
	jobID := ""
	createCount := 0
	for result := range results {
		if jobID == "" {
			jobID = result.Job.ID
		}
		if result.Job.ID != jobID {
			t.Errorf("concurrent result job = %q, want %q", result.Job.ID, jobID)
		}
		if result.Decision == JobIngestionCreate {
			createCount++
		} else if result.Decision != JobIngestionDuplicate {
			t.Errorf("concurrent decision = %q, want create or duplicate", result.Decision)
		}
	}
	if createCount != 1 {
		t.Fatalf("create decisions = %d, want 1", createCount)
	}
	state := store.Snapshot()
	if len(state.Jobs) != 1 || len(state.JobProvenance) != workers {
		t.Fatalf("concurrent identity state: jobs=%d provenance=%d",
			len(state.Jobs),
			len(state.JobProvenance),
		)
	}

	sameEvent := crmJobIngestionSeedEvent(
		t, store, groupID, "mid-same-event", "same event synthetic order",
	)
	results = make(chan JobIngestionResult, workers)
	errs = make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := store.IngestSourceJob(SourceJobIngestion{
				EventID: sameEvent.ID,
				GroupID: groupID,
				Job:     draft,
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("same event ingestion error = %v", err)
	}
	provenanceID := ""
	nonIdempotent := 0
	for result := range results {
		if provenanceID == "" {
			provenanceID = result.Provenance.ID
		}
		if result.Provenance.ID != provenanceID {
			t.Errorf("same event provenance = %q, want %q", result.Provenance.ID, provenanceID)
		}
		if !result.Idempotent {
			nonIdempotent++
		}
	}
	if nonIdempotent != 1 {
		t.Fatalf("same event non-idempotent results = %d, want 1", nonIdempotent)
	}
	state = store.Snapshot()
	if len(state.JobProvenance) != workers+1 {
		t.Fatalf("same event provenance count = %d, want %d",
			len(state.JobProvenance),
			workers+1,
		)
	}
}

func crmJobIngestionOpenStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(t.TempDir() + "/crm.json")
	if err != nil {
		t.Fatalf("OpenCRMStore() error = %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func crmJobIngestionSeedSourceGroup(
	t *testing.T,
	store *CRMStore,
	groupID string,
	enabled bool,
) RecruitingSourceGroup {
	t.Helper()
	var source RecruitingSourceGroup
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindSourceGroup)
		if err != nil {
			return err
		}
		source = RecruitingSourceGroup{
			ID:          id,
			GroupID:     groupID,
			DisplayName: "Synthetic Jobs",
			Enabled:     enabled,
			Version:     1,
			CreatedAt:   crmJobIngestionTestTime,
			UpdatedAt:   crmJobIngestionTestTime,
		}
		state.RecruitingProject.SourceGroups = append(
			state.RecruitingProject.SourceGroups,
			source,
		)
		return nil
	})
	if err != nil {
		t.Fatalf("seed source group error = %v", err)
	}
	return source
}

func crmJobIngestionSeedEvent(
	t *testing.T,
	store *CRMStore,
	groupID, mid, text string,
) MessageEvent {
	t.Helper()
	var event MessageEvent
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindEvent)
		if err != nil {
			return err
		}
		event = MessageEvent{
			ID:           id,
			Source:       "wechat",
			Mid:          mid,
			ChatType:     ChatTypeGroup,
			PeerID:       groupID,
			AuthorID:     "fictional_author",
			GroupID:      groupID,
			Text:         text,
			Kinds:        []string{"text"},
			Timestamp:    crmJobIngestionTestTime.Unix(),
			Disposition:  MessageDispositionGroupNoise,
			ReviewStatus: ReviewStatusUnreviewed,
			Version:      1,
			CreatedAt:    crmJobIngestionTestTime,
			UpdatedAt:    crmJobIngestionTestTime,
		}
		event.MessageKey = MessageKeyForEvent(event)
		state.MessageEvents = append(state.MessageEvents, event)
		return nil
	})
	if err != nil {
		t.Fatalf("seed event error = %v", err)
	}
	return event
}

func crmJobIngestionDraft(demandCode string) JobDraft {
	return JobDraft{
		Title:                 "Synthetic Operator",
		Company:               "Example Manufacturing",
		Location:              "Synthetic Park",
		EmploymentType:        "contract",
		Description:           "Synthetic demand only",
		Requirements:          []string{"assembly", "Go"},
		DemandCode:            demandCode,
		SiteName:              "Synthetic Site",
		OnsiteContact:         "Synthetic Contact",
		Headcount:             12,
		Priority:              JobPriorityHigh,
		Shift:                 "day",
		WorkSchedule:          "five synthetic days",
		CompensationSummary:   "synthetic compensation",
		Accommodation:         "synthetic accommodation",
		TransportRequirements: "synthetic shuttle",
		RequiredDocuments:     []string{"Passport", "ID"},
		Owner:                 "synthetic_recruiter",
		Status:                JobStatusOpen,
	}
}
