package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var crmCandidateTestTime = time.Date(2026, time.July, 21, 7, 0, 0, 123, time.UTC)

func TestCRMCandidateRoutesCRUDFilteringAndDetail(t *testing.T) {
	store, _ := crmCandidateTestOpenStore(t)
	mux := http.NewServeMux()
	registerCRMCandidateRoutes(mux, store)

	ada := crmCandidateTestCreateViaHTTP(t, mux, `{
		"full_name":"  Ada Lovelace  ",
		"wechat_id":" ada_wx ",
		"phone":" 123 ",
		"email":" ada@example.com ",
		"location":" Berlin ",
		"source":" referral ",
		"skills":[" Go ","C"],
		"owner":" initial ",
		"summary":" First programmer "
	}`)
	if ada.ID != "cand_00000001" || ada.FullName != "Ada Lovelace" || ada.Status != CandidateStatusActive {
		t.Fatalf("created Ada = %+v", ada)
	}
	if len(ada.Skills) != 2 || ada.Skills[0] != "Go" || ada.Owner != "initial" {
		t.Fatalf("normalized Ada = %+v", ada)
	}
	grace := crmCandidateTestCreateViaHTTP(t, mux, `{
		"full_name":"Grace Hopper",
		"source":"conference",
		"skills":[],
		"owner":"bob",
		"status":"placed"
	}`)
	if grace.ID != "cand_00000002" || grace.Status != CandidateStatusPlaced || grace.Skills == nil {
		t.Fatalf("created Grace = %+v", grace)
	}

	response := crmCandidateTestRequest(mux, http.MethodPatch, "/api/crm/candidates/"+ada.ID, `{
		"expected_version":1,
		"owner":" alice ",
		"status":"paused",
		"summary":"compiler pioneer"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", response.Code, response.Body.String())
	}
	var updated Candidate
	crmCandidateTestDecode(t, response, &updated)
	if updated.Version != 2 || updated.Owner != "alice" || updated.Status != CandidateStatusPaused {
		t.Fatalf("updated candidate = %+v", updated)
	}

	response = crmCandidateTestRequest(mux, http.MethodPatch, "/api/crm/candidates/"+ada.ID, `{
		"expected_version":1,
		"owner":"stale"
	}`)
	crmCandidateTestAssertError(t, response, http.StatusConflict, crmCandidateCodeConflict)

	crmCandidateTestSeedDetailRelations(t, store, ada.ID)

	response = crmCandidateTestRequest(
		mux,
		http.MethodGet,
		"/api/crm/candidates?status=paused&owner=alice&source=referral&q=LOVELACE&limit=1&offset=0",
		"",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var list struct {
		Total      int                 `json:"total"`
		Candidates []CandidateListItem `json:"candidates"`
	}
	crmCandidateTestDecode(t, response, &list)
	if list.Total != 1 || len(list.Candidates) != 1 {
		t.Fatalf("filtered list = %+v", list)
	}
	if list.Candidates[0].Candidate.ID != ada.ID ||
		list.Candidates[0].ActiveLeadCount != 1 ||
		list.Candidates[0].OpenTaskCount != 2 {
		t.Fatalf("candidate projection = %+v", list.Candidates[0])
	}

	response = crmCandidateTestRequest(mux, http.MethodGet, "/api/crm/candidates/"+ada.ID, "")
	if response.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail CandidateDetail
	crmCandidateTestDecode(t, response, &detail)
	if detail.Candidate.ID != ada.ID || detail.NoteCount != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	if strings.Join(detail.MessageEventIDs, ",") != "event_00000001" ||
		strings.Join(detail.LeadIDs, ",") != "lead_00000001,lead_00000002" ||
		strings.Join(detail.OpenTaskIDs, ",") != "task_00000001,task_00000002" {
		t.Fatalf("detail relationships = %+v", detail)
	}

	response = crmCandidateTestRequest(mux, http.MethodPatch, "/api/crm/candidates/"+ada.ID, `{
		"expected_version":2,
		"status":"archived"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("archive status = %d, body = %s", response.Code, response.Body.String())
	}
	response = crmCandidateTestRequest(mux, http.MethodPatch, "/api/crm/candidates/"+ada.ID, `{
		"expected_version":3,
		"status":"active"
	}`)
	crmCandidateTestAssertError(t, response, http.StatusConflict, crmCandidateCodeConflict)

	response = crmCandidateTestRequest(mux, http.MethodGet, "/api/crm/candidates/cand_99999999", "")
	crmCandidateTestAssertError(t, response, http.StatusNotFound, crmCandidateCodeNotFound)
}

func TestCRMCandidateCreateFromMessageAndRestart(t *testing.T) {
	store, path := crmCandidateTestOpenStore(t)
	event := crmCandidateTestAddEvent(t, store, "author_wx")
	mux := http.NewServeMux()
	registerCRMCandidateRoutes(mux, store)

	response := crmCandidateTestRequest(mux, http.MethodPost, "/api/crm/message_candidate_links", `{
		"message_event_id":"`+event.ID+`",
		"expected_event_version":1,
		"candidate":{
			"full_name":"  Lin Chen  ",
			"skills":[" Go "],
			"owner":" recruiter "
		}
	}`)
	if response.Code != http.StatusCreated {
		t.Fatalf("create from message status = %d, body = %s", response.Code, response.Body.String())
	}
	var result crmCandidateLinkResult
	crmCandidateTestDecode(t, response, &result)
	if result.Candidate.ID != "cand_00000001" {
		t.Fatalf("link result = %+v", result)
	}
	if result.Candidate.FullName != "Lin Chen" ||
		result.Candidate.WeChatID != "author_wx" ||
		result.Candidate.Source != "wechat_message" ||
		result.Candidate.SourceEventID != event.ID ||
		result.Candidate.Owner != "recruiter" {
		t.Fatalf("manual candidate mapping = %+v", result.Candidate)
	}
	if result.Event.CandidateID != result.Candidate.ID ||
		result.Event.ReviewStatus != ReviewStatusReviewed ||
		result.Event.Version != 2 {
		t.Fatalf("linked event = %+v", result.Event)
	}

	beforeDuplicate := store.Snapshot()
	_, err := crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
		MessageEventID:       event.ID,
		ExpectedEventVersion: event.Version,
		Candidate:            &CandidateDraft{FullName: "Duplicate"},
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("duplicate link error = %v, want conflict", err)
	}
	afterDuplicate := store.Snapshot()
	if afterDuplicate.Counters.Candidate != beforeDuplicate.Counters.Candidate ||
		len(afterDuplicate.Candidates) != len(beforeDuplicate.Candidates) ||
		afterDuplicate.MessageEvents[0].Version != beforeDuplicate.MessageEvents[0].Version {
		t.Fatalf("duplicate link mutated state: before=%+v after=%+v", beforeDuplicate, afterDuplicate)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	state := reopened.Snapshot()
	if len(state.Candidates) != 1 || len(state.MessageEvents) != 1 {
		t.Fatalf("reopened state = %+v", state)
	}
	if state.Candidates[0].SourceEventID != state.MessageEvents[0].ID ||
		state.MessageEvents[0].CandidateID != state.Candidates[0].ID {
		t.Fatalf("reopened linkage = candidate:%+v event:%+v", state.Candidates[0], state.MessageEvents[0])
	}
}

func TestCRMCandidateLinkExistingMissingAndPersistenceRollback(t *testing.T) {
	store, path := crmCandidateTestOpenStore(t)
	mux := http.NewServeMux()
	registerCRMCandidateRoutes(mux, store)
	candidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Existing Candidate",
		Skills:   []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstEvent := crmCandidateTestAddEvent(t, store, "existing_wx")

	response := crmCandidateTestRequest(mux, http.MethodPost, "/api/crm/message_candidate_links", `{
		"message_event_id":"`+firstEvent.ID+`",
		"expected_event_version":1,
		"candidate_id":"`+candidate.ID+`"
	}`)
	if response.Code != http.StatusOK {
		t.Fatalf("link existing status = %d, body = %s", response.Code, response.Body.String())
	}
	var result crmCandidateLinkResult
	crmCandidateTestDecode(t, response, &result)
	if result.Candidate.Version != candidate.Version || result.Event.CandidateID != candidate.ID {
		t.Fatalf("link existing result = %+v", result)
	}

	missingEvent := crmCandidateTestAddEvent(t, store, "missing_wx")
	beforeMissing := store.Snapshot()
	_, err = crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
		MessageEventID:       missingEvent.ID,
		ExpectedEventVersion: missingEvent.Version,
		CandidateID:          "cand_99999999",
	})
	var notFound *crmCandidateNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("missing candidate error = %v, want not found", err)
	}
	afterMissing := store.Snapshot()
	missingIndex := crmCandidateEventIndexByID(afterMissing.MessageEvents, missingEvent.ID)
	if missingIndex < 0 ||
		afterMissing.MessageEvents[missingIndex].CandidateID != "" ||
		afterMissing.MessageEvents[missingIndex].Version != beforeMissing.MessageEvents[missingIndex].Version {
		t.Fatalf("missing candidate partially linked event: %+v", afterMissing.MessageEvents)
	}

	rollbackEvent := crmCandidateTestAddEvent(t, store, "rollback_wx")
	beforeRollback := store.Snapshot()
	beforeDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blockingFile, "crm.json")
	_, err = crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
		MessageEventID:       rollbackEvent.ID,
		ExpectedEventVersion: rollbackEvent.Version,
		Candidate:            &CandidateDraft{FullName: "Rolled Back"},
	})
	store.path = path
	if !errors.Is(err, ErrCRMPersistence) {
		t.Fatalf("persistence link error = %v, want persistence error", err)
	}
	afterRollback := store.Snapshot()
	if afterRollback.Counters.Candidate != beforeRollback.Counters.Candidate ||
		len(afterRollback.Candidates) != len(beforeRollback.Candidates) {
		t.Fatalf("candidate state changed after persistence failure: before=%+v after=%+v", beforeRollback, afterRollback)
	}
	rollbackIndex := crmCandidateEventIndexByID(afterRollback.MessageEvents, rollbackEvent.ID)
	if rollbackIndex < 0 ||
		afterRollback.MessageEvents[rollbackIndex].CandidateID != "" ||
		afterRollback.MessageEvents[rollbackIndex].Version != rollbackEvent.Version {
		t.Fatalf("event changed after persistence failure: %+v", afterRollback.MessageEvents)
	}
	afterDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterDisk, beforeDisk) {
		t.Fatal("snapshot changed after persistence failure")
	}
}

func TestCRMCandidateConcurrentVersionAndLinkConflicts(t *testing.T) {
	t.Run("candidate update", func(t *testing.T) {
		store, _ := crmCandidateTestOpenStore(t)
		candidate, err := crmCandidateCreate(store, CandidateDraft{FullName: "Concurrent", Skills: []string{}})
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		errorsChannel := make(chan error, 2)
		var wait sync.WaitGroup
		for _, owner := range []string{"alice", "bob"} {
			wait.Add(1)
			go func(owner string) {
				defer wait.Done()
				<-start
				_, updateErr := crmCandidateUpdate(store, candidate.ID, CandidateUpdate{
					ExpectedVersion: candidate.Version,
					Owner:           &owner,
				})
				errorsChannel <- updateErr
			}(owner)
		}
		close(start)
		wait.Wait()
		close(errorsChannel)

		var successes int
		var conflicts int
		for updateErr := range errorsChannel {
			switch {
			case updateErr == nil:
				successes++
			case errors.Is(updateErr, ErrCRMConflict):
				conflicts++
			default:
				t.Fatalf("concurrent update error = %v", updateErr)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("concurrent update successes=%d conflicts=%d", successes, conflicts)
		}
		got := store.Snapshot().Candidates[0]
		if got.Version != 2 || (got.Owner != "alice" && got.Owner != "bob") {
			t.Fatalf("candidate after concurrent update = %+v", got)
		}
	})

	t.Run("create from one event", func(t *testing.T) {
		store, _ := crmCandidateTestOpenStore(t)
		event := crmCandidateTestAddEvent(t, store, "concurrent_wx")

		const workers = 16
		start := make(chan struct{})
		errorsChannel := make(chan error, workers)
		var successes atomic.Int32
		var wait sync.WaitGroup
		for range workers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start
				_, linkErr := crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
					MessageEventID:       event.ID,
					ExpectedEventVersion: event.Version,
					Candidate:            &CandidateDraft{FullName: "Concurrent Link"},
				})
				if linkErr == nil {
					successes.Add(1)
				}
				errorsChannel <- linkErr
			}()
		}
		close(start)
		wait.Wait()
		close(errorsChannel)

		var conflicts int
		for linkErr := range errorsChannel {
			if linkErr == nil {
				continue
			}
			if !errors.Is(linkErr, ErrCRMConflict) {
				t.Fatalf("concurrent link error = %v", linkErr)
			}
			conflicts++
		}
		if successes.Load() != 1 || conflicts != workers-1 {
			t.Fatalf("concurrent link successes=%d conflicts=%d", successes.Load(), conflicts)
		}
		state := store.Snapshot()
		if state.Counters.Candidate != 1 || len(state.Candidates) != 1 {
			t.Fatalf("concurrent link candidates = %+v", state.Candidates)
		}
		if state.MessageEvents[0].CandidateID != state.Candidates[0].ID || state.MessageEvents[0].Version != 2 {
			t.Fatalf("concurrent link event = %+v", state.MessageEvents[0])
		}
	})
}

func TestCRMCandidatePrivateMessageIdentityResolution(t *testing.T) {
	t.Run("peer identity reuses and enriches one candidate", func(t *testing.T) {
		store, _ := crmCandidateTestOpenStore(t)
		existing, err := crmCandidateCreate(store, CandidateDraft{
			FullName: "Tessa Quill",
			Email:    "tessa@synthetic.invalid",
			Skills:   []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		event := crmCandidateTestAddPrivateEvent(t, store, "wxid_synthetic_tessa", "wxid_synthetic_operator")

		result, err := crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
			MessageEventID:       event.ID,
			ExpectedEventVersion: event.Version,
			Candidate: &CandidateDraft{
				FullName: "Untrusted Extracted Name",
				Phone:    "+49 170 555 0101",
				Email:    "TESSA@SYNTHETIC.INVALID",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Created || result.Candidate.ID != existing.ID {
			t.Fatalf("identity link result = %+v", result)
		}
		if result.Candidate.FullName != existing.FullName ||
			result.Candidate.WeChatID != event.PeerID ||
			result.Candidate.Phone != "+49 170 555 0101" ||
			result.Candidate.Version != existing.Version+1 {
			t.Fatalf("enriched candidate = %+v", result.Candidate)
		}
		state := store.Snapshot()
		if state.Counters.Candidate != 1 || len(state.Candidates) != 1 {
			t.Fatalf("identity reuse candidates = %+v", state.Candidates)
		}
	})

	t.Run("conflicting aliases remain reviewable", func(t *testing.T) {
		store, _ := crmCandidateTestOpenStore(t)
		wechatCandidate, err := crmCandidateCreate(store, CandidateDraft{
			FullName: "Rhea Moss",
			WeChatID: "wxid_synthetic_rhea",
			Skills:   []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		emailCandidate, err := crmCandidateCreate(store, CandidateDraft{
			FullName: "Soren Pike",
			Email:    "soren@synthetic.invalid",
			Skills:   []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		event := crmCandidateTestAddPrivateEvent(t, store, wechatCandidate.WeChatID, wechatCandidate.WeChatID)
		before := store.Snapshot()

		_, err = crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
			MessageEventID:       event.ID,
			ExpectedEventVersion: event.Version,
			Candidate: &CandidateDraft{
				FullName: "Conflicting Synthetic Link",
				Email:    emailCandidate.Email,
			},
		})
		if !errors.Is(err, ErrCRMConflict) {
			t.Fatalf("conflicting link error = %v, want conflict", err)
		}
		after := store.Snapshot()
		eventIndex := crmCandidateEventIndexByID(after.MessageEvents, event.ID)
		if after.Counters.Candidate != before.Counters.Candidate ||
			len(after.Candidates) != len(before.Candidates) ||
			after.Candidates[0].Version != before.Candidates[0].Version ||
			after.Candidates[1].Version != before.Candidates[1].Version ||
			after.MessageEvents[eventIndex].CandidateID != "" ||
			after.MessageEvents[eventIndex].Version != event.Version {
			t.Fatalf("conflicting link mutated state: before=%+v after=%+v", before, after)
		}
	})

	t.Run("source group author is never a candidate", func(t *testing.T) {
		store, _ := crmCandidateTestOpenStore(t)
		var event MessageEvent
		err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, nextErr := allocator.Next(IDKindEvent)
			if nextErr != nil {
				return nextErr
			}
			event = crmCandidateTestEvent(id, "mid-"+id, "wxid_synthetic_group_author")
			event.ChatType = ChatTypeGroup
			event.PeerID = "synthetic_jobs@chatroom"
			event.GroupID = event.PeerID
			event.Disposition = MessageDispositionSourceGroupJob
			event.MessageKey = MessageKeyForEvent(event)
			state.MessageEvents = append(state.MessageEvents, event)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}

		_, err = crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
			MessageEventID:       event.ID,
			ExpectedEventVersion: event.Version,
			Candidate:            &CandidateDraft{FullName: "Synthetic Group Author"},
		})
		if !errors.Is(err, ErrCRMConflict) {
			t.Fatalf("group candidate error = %v, want conflict", err)
		}
		state := store.Snapshot()
		if state.Counters.Candidate != 0 || len(state.Candidates) != 0 {
			t.Fatalf("group author became candidate: %+v", state.Candidates)
		}
		eventIndex := crmCandidateEventIndexByID(state.MessageEvents, event.ID)
		if state.MessageEvents[eventIndex].CandidateID != "" ||
			state.MessageEvents[eventIndex].Version != event.Version {
			t.Fatalf("group event mutated = %+v", state.MessageEvents[eventIndex])
		}
	})
}

func TestCRMCandidateConcurrentPrivateMessagesReuseIdentity(t *testing.T) {
	store, _ := crmCandidateTestOpenStore(t)
	const workers = 12
	events := make([]MessageEvent, workers)
	for index := range events {
		events[index] = crmCandidateTestAddPrivateEvent(
			t,
			store,
			"wxid_synthetic_concurrent",
			"wxid_synthetic_concurrent",
		)
	}

	start := make(chan struct{})
	results := make(chan crmCandidateLinkResult, workers)
	errorsChannel := make(chan error, workers)
	var wait sync.WaitGroup
	for index := range events {
		wait.Add(1)
		go func(event MessageEvent) {
			defer wait.Done()
			<-start
			result, err := crmCandidateLinkMessage(context.Background(), store, ManualCandidateExtractor{}, MessageCandidateLink{
				MessageEventID:       event.ID,
				ExpectedEventVersion: event.Version,
				Candidate: &CandidateDraft{
					FullName: "Pax Rowan",
					Email:    "pax@synthetic.invalid",
				},
			})
			results <- result
			errorsChannel <- err
		}(events[index])
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsChannel)

	var created int
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("concurrent identity link error = %v", err)
		}
	}
	for result := range results {
		if result.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created results = %d, want 1", created)
	}

	state := store.Snapshot()
	if state.Counters.Candidate != 1 || len(state.Candidates) != 1 {
		t.Fatalf("concurrent identity candidates = %+v", state.Candidates)
	}
	for _, event := range state.MessageEvents {
		if event.CandidateID != state.Candidates[0].ID ||
			event.ReviewStatus != ReviewStatusReviewed ||
			event.Version != 2 {
			t.Fatalf("concurrent linked event = %+v", event)
		}
	}
}

func TestCRMCandidateRoutesValidationAndStableErrors(t *testing.T) {
	store, _ := crmCandidateTestOpenStore(t)
	mux := http.NewServeMux()
	registerCRMCandidateRoutes(mux, store)
	candidate := crmCandidateTestCreateViaHTTP(t, mux, `{"full_name":"Ada","skills":[]}`)

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{
			name:       "malformed JSON",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `{"full_name":`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeInvalidJSON,
		},
		{
			name:       "unknown field",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `{"full_name":"Ada","unknown":true}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeInvalidJSON,
		},
		{
			name:       "trailing JSON",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `{"full_name":"Ada"} {"full_name":"Grace"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeInvalidJSON,
		},
		{
			name:       "non object",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `null`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeInvalidJSON,
		},
		{
			name:       "invalid owner",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `{"full_name":"Ada","owner":"` + strings.Repeat("x", 81) + `"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "invalid status",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `{"full_name":"Ada","status":"unknown"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "oversized summary",
			method:     http.MethodPost,
			path:       "/api/crm/candidates",
			body:       `{"full_name":"Ada","summary":"` + strings.Repeat("x", 4001) + `"}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "empty patch",
			method:     http.MethodPatch,
			path:       "/api/crm/candidates/" + candidate.ID,
			body:       `{"expected_version":1}`,
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "invalid query limit",
			method:     http.MethodGet,
			path:       "/api/crm/candidates?limit=201",
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "oversized query",
			method:     http.MethodGet,
			path:       "/api/crm/candidates?q=" + strings.Repeat("x", 201),
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "duplicate query",
			method:     http.MethodGet,
			path:       "/api/crm/candidates?q=Ada&q=Grace",
			wantStatus: http.StatusBadRequest,
			wantCode:   crmCandidateCodeValidationError,
		},
		{
			name:       "method not allowed",
			method:     http.MethodPut,
			path:       "/api/crm/candidates",
			wantStatus: http.StatusMethodNotAllowed,
			wantCode:   crmCandidateCodeMethodNotAllowed,
			wantAllow:  "GET, POST",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := crmCandidateTestRequest(mux, test.method, test.path, test.body)
			crmCandidateTestAssertError(t, response, test.wantStatus, test.wantCode)
			if test.wantAllow != "" && response.Header().Get("Allow") != test.wantAllow {
				t.Fatalf("Allow = %q, want %q", response.Header().Get("Allow"), test.wantAllow)
			}
		})
	}

	oversizedBody := `{"full_name":"Ada","summary":"` + strings.Repeat("x", CRMMaxRequestBodyBytes) + `"}`
	response := crmCandidateTestRequest(mux, http.MethodPost, "/api/crm/candidates", oversizedBody)
	crmCandidateTestAssertError(t, response, http.StatusRequestEntityTooLarge, crmCandidateCodeBodyTooLarge)

	response = crmCandidateTestRequest(mux, http.MethodPost, "/api/crm/message_candidate_links", `{
		"message_event_id":"event_00000001",
		"expected_event_version":1,
		"candidate_id":"cand_00000001",
		"candidate":{"full_name":"Ada"}
	}`)
	crmCandidateTestAssertError(t, response, http.StatusBadRequest, crmCandidateCodeValidationError)

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	response = crmCandidateTestRequest(mux, http.MethodPost, "/api/crm/candidates", `{"full_name":"Closed Store"}`)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("closed store status = %d, body = %s", response.Code, response.Body.String())
	}
	var persistencePayload map[string]string
	crmCandidateTestDecode(t, response, &persistencePayload)
	if len(persistencePayload) != 2 ||
		persistencePayload["code"] != crmCandidateCodePersistenceError ||
		strings.Contains(persistencePayload["error"], string(filepath.Separator)) {
		t.Fatalf("persistence error leaked a path: %#v", persistencePayload)
	}
}

type crmCandidateFailingExtractor struct {
	err error
}

func (e crmCandidateFailingExtractor) Draft(context.Context, MessageEvent, CandidateDraft) (CandidateDraft, error) {
	return CandidateDraft{}, e.err
}

func TestCRMCandidateExtractorFailureDoesNotMutate(t *testing.T) {
	store, _ := crmCandidateTestOpenStore(t)
	event := crmCandidateTestAddEvent(t, store, "extractor_wx")
	before := store.Snapshot()
	extractorError := errors.New("extractor unavailable")

	_, err := crmCandidateLinkMessage(context.Background(), store, crmCandidateFailingExtractor{err: extractorError}, MessageCandidateLink{
		MessageEventID:       event.ID,
		ExpectedEventVersion: event.Version,
		Candidate:            &CandidateDraft{FullName: "Ada"},
	})
	if !errors.Is(err, extractorError) {
		t.Fatalf("extractor error = %v, want %v", err, extractorError)
	}
	after := store.Snapshot()
	if after.Counters.Candidate != before.Counters.Candidate ||
		len(after.Candidates) != len(before.Candidates) ||
		after.MessageEvents[0].Version != before.MessageEvents[0].Version ||
		after.MessageEvents[0].CandidateID != "" {
		t.Fatalf("extractor failure mutated state: before=%+v after=%+v", before, after)
	}
}

func crmCandidateTestOpenStore(t *testing.T) (*CRMStore, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store, path
}

func crmCandidateTestCreateViaHTTP(t *testing.T, handler http.Handler, body string) Candidate {
	t.Helper()
	response := crmCandidateTestRequest(handler, http.MethodPost, "/api/crm/candidates", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var candidate Candidate
	crmCandidateTestDecode(t, response, &candidate)
	return candidate
}

func crmCandidateTestAddEvent(t *testing.T, store *CRMStore, authorID string) MessageEvent {
	t.Helper()
	return crmCandidateTestAddPrivateEvent(t, store, authorID, authorID)
}

func crmCandidateTestAddPrivateEvent(t *testing.T, store *CRMStore, peerID, authorID string) MessageEvent {
	t.Helper()
	var event MessageEvent
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindEvent)
		if err != nil {
			return err
		}
		event = crmCandidateTestEvent(id, "mid-"+id, authorID)
		event.PeerID = peerID
		event.AuthorID = authorID
		event.MessageKey = MessageKeyForEvent(event)
		state.MessageEvents = append(state.MessageEvents, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func crmCandidateTestSeedDetailRelations(t *testing.T, store *CRMStore, candidateID string) {
	t.Helper()
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		jobID, err := allocator.Next(IDKindJob)
		if err != nil {
			return err
		}
		state.Jobs = append(state.Jobs, Job{
			ID:           jobID,
			Title:        "Compiler Engineer",
			Company:      "Analytical Engines",
			Requirements: []string{},
			Status:       JobStatusOpen,
			Version:      1,
			CreatedAt:    crmCandidateTestTime,
			UpdatedAt:    crmCandidateTestTime,
		})

		activeLeadID, err := allocator.Next(IDKindLead)
		if err != nil {
			return err
		}
		terminalLeadID, err := allocator.Next(IDKindLead)
		if err != nil {
			return err
		}
		state.Leads = append(
			state.Leads,
			crmCandidateTestLead(activeLeadID, candidateID, jobID, LeadStageScreening),
			crmCandidateTestLead(terminalLeadID, candidateID, jobID, LeadStageHired),
		)

		directTaskID, err := allocator.Next(IDKindTask)
		if err != nil {
			return err
		}
		leadTaskID, err := allocator.Next(IDKindTask)
		if err != nil {
			return err
		}
		state.Tasks = append(
			state.Tasks,
			crmCandidateTestTask(directTaskID, candidateID, ""),
			crmCandidateTestTask(leadTaskID, "", activeLeadID),
		)

		noteID, err := allocator.Next(IDKindNote)
		if err != nil {
			return err
		}
		state.Notes = append(state.Notes, Note{
			ID:         noteID,
			TargetType: NoteTargetCandidate,
			TargetID:   candidateID,
			Body:       "Strong systems background",
			CreatedAt:  crmCandidateTestTime,
		})

		eventID, err := allocator.Next(IDKindEvent)
		if err != nil {
			return err
		}
		event := crmCandidateTestEvent(eventID, "detail-mid", "ada_wx")
		event.CandidateID = candidateID
		event.ReviewStatus = ReviewStatusReviewed
		state.MessageEvents = append(state.MessageEvents, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func crmCandidateTestEvent(id, mid, authorID string) MessageEvent {
	event := MessageEvent{
		ID:           id,
		Source:       "wechat",
		Mid:          mid,
		ChatType:     ChatTypePrivate,
		PeerID:       authorID,
		AuthorID:     authorID,
		Text:         "Interested in the role",
		Kinds:        []string{"text"},
		Timestamp:    crmCandidateTestTime.Unix(),
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      1,
		CreatedAt:    crmCandidateTestTime,
		UpdatedAt:    crmCandidateTestTime,
	}
	event.MessageKey = MessageKeyForEvent(event)
	return event
}

func crmCandidateTestLead(id, candidateID, jobID string, stage LeadStage) Lead {
	return Lead{
		ID:             id,
		CandidateID:    candidateID,
		JobID:          jobID,
		Stage:          stage,
		Version:        1,
		CreatedAt:      crmCandidateTestTime,
		UpdatedAt:      crmCandidateTestTime,
		StageChangedAt: crmCandidateTestTime,
	}
}

func crmCandidateTestTask(id, candidateID, leadID string) FollowUpTask {
	return FollowUpTask{
		ID:          id,
		Title:       "Follow up",
		Status:      FollowUpStatusOpen,
		DueAt:       crmCandidateTestTime.Add(24 * time.Hour),
		CandidateID: candidateID,
		LeadID:      leadID,
		Version:     1,
		CreatedAt:   crmCandidateTestTime,
		UpdatedAt:   crmCandidateTestTime,
	}
}

func crmCandidateTestRequest(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func crmCandidateTestDecode(t *testing.T, response *httptest.ResponseRecorder, destination any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		t.Fatalf("decode response %q: %v", response.Body.String(), err)
	}
}

func crmCandidateTestAssertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, status, response.Body.String())
	}
	var payload map[string]string
	crmCandidateTestDecode(t, response, &payload)
	if len(payload) != 2 || payload["code"] != code || payload["error"] == "" {
		t.Fatalf("error payload = %#v, want code %q and stable error", payload, code)
	}
}
