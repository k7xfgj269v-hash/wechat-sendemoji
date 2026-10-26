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
	"sync"
	"testing"
	"time"
)

func TestCaptureNormalizedInboundPersistsAndDeduplicates(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	now := time.Date(2026, time.July, 21, 7, 0, 0, 123, time.UTC)

	private := &ForwardRecord{
		T:          1_700_000_000,
		Chat:       "private",
		Dir:        "in",
		Peer:       "wxid_peer",
		Author:     "wxid_peer",
		Text:       "hello",
		Suspicious: false,
		Kinds:      []string{"text"},
		Mid:        "mid-private",
	}
	event, created, err := crmMessageCaptureNormalizedInboundToStore(store, private, now)
	if err != nil {
		t.Fatalf("captureNormalizedInboundToStore() error = %v", err)
	}
	if !created {
		t.Fatal("created = false, want true")
	}
	if event.ID != "event_00000001" || event.Source != "wechat" {
		t.Fatalf("event identity = %+v", event)
	}
	if event.MessageKey != "wechat:private:wxid_peer:mid-private" {
		t.Fatalf("MessageKey = %q", event.MessageKey)
	}
	if event.ChatType != ChatTypePrivate || event.PeerID != "wxid_peer" || event.AuthorID != "wxid_peer" {
		t.Fatalf("private event = %+v", event)
	}
	if event.ReviewStatus != ReviewStatusUnreviewed || event.Version != 1 {
		t.Fatalf("review metadata = %+v", event)
	}
	if !event.CreatedAt.Equal(now) || !event.UpdatedAt.Equal(now) {
		t.Fatalf("event times = %s / %s, want %s", event.CreatedAt, event.UpdatedAt, now)
	}

	duplicate, created, err := crmMessageCaptureNormalizedInboundToStore(store, private, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("duplicate capture error = %v", err)
	}
	if created {
		t.Fatal("duplicate created a second event")
	}
	if duplicate.ID != event.ID {
		t.Fatalf("duplicate ID = %q, want %q", duplicate.ID, event.ID)
	}

	group := &ForwardRecord{
		T:          1_700_000_001,
		Chat:       "group",
		Dir:        "in",
		Peer:       "group@chatroom",
		Author:     "wxid_member",
		GroupID:    "group@chatroom",
		Text:       "job",
		Suspicious: true,
		Kinds:      []string{"text", "at", "text"},
	}
	groupEvent, created, err := crmMessageCaptureNormalizedInboundToStore(store, group, now.Add(time.Second))
	if err != nil {
		t.Fatalf("group capture error = %v", err)
	}
	if !created {
		t.Fatal("group created = false, want true")
	}
	if groupEvent.ChatType != ChatTypeGroup || groupEvent.GroupID != group.GroupID || groupEvent.PeerID != group.Peer {
		t.Fatalf("group event = %+v", groupEvent)
	}
	if len(groupEvent.Kinds) != 2 || groupEvent.Kinds[0] != "text" || groupEvent.Kinds[1] != "at" {
		t.Fatalf("group kinds = %#v", groupEvent.Kinds)
	}
	if groupEvent.MessageKey[:len("wechat:sha256:")] != "wechat:sha256:" {
		t.Fatalf("fallback MessageKey = %q", groupEvent.MessageKey)
	}

	state := store.Snapshot()
	if len(state.MessageEvents) != 2 || state.Counters.Event != 2 {
		t.Fatalf("state events/counter = %d/%d, want 2/2", len(state.MessageEvents), state.Counters.Event)
	}
}

func TestCaptureNormalizedInboundConcurrentDuplicate(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	rec := &ForwardRecord{
		T:      1_700_000_000,
		Chat:   "private",
		Dir:    "in",
		Peer:   "wxid_peer",
		Author: "wxid_peer",
		Text:   "hello",
		Kinds:  []string{"text"},
		Mid:    "mid-concurrent",
	}

	const workers = 16
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := crmMessageCaptureNormalizedInboundToStore(store, rec, time.Now().UTC())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent capture error = %v", err)
		}
	}

	state := store.Snapshot()
	if len(state.MessageEvents) != 1 || state.Counters.Event != 1 {
		t.Fatalf("state events/counter = %d/%d, want 1/1", len(state.MessageEvents), state.Counters.Event)
	}
}

func TestCaptureNormalizedInboundDuplicateDoesNotEnqueueAgain(t *testing.T) {
	// 入向自动化默认关闭（微信侧对话循环由 DSH 驱动）。本用例验证的是"入队"语义，
	// 与队列/去重相关，因此显式打开该开关。
	t.Setenv("ONEBOT_INBOUND_AUTOMATION", "1")
	automationQueue := make(chan MessageEvent, 2)
	automationRuntimeMu.Lock()
	previousRuntime := automationRuntime
	automationRuntime = &AutomationRuntime{events: automationQueue}
	automationRuntimeMu.Unlock()
	t.Cleanup(func() {
		automationRuntimeMu.Lock()
		automationRuntime = previousRuntime
		automationRuntimeMu.Unlock()
	})

	store := crmMessageOpenTestStore(t)
	rec := &ForwardRecord{
		T:      1_700_000_000,
		Chat:   "private",
		Dir:    "in",
		Peer:   "wxid_peer",
		Author: "wxid_peer",
		Text:   "hello",
		Kinds:  []string{"text"},
		Mid:    "mid-enqueue-once",
	}
	first, created, err := crmMessageCaptureNormalizedInboundToStore(
		store,
		rec,
		time.Now().UTC(),
	)
	if err != nil || !created {
		t.Fatalf("first capture = %#v, created=%t, err=%v", first, created, err)
	}
	if _, created, err := crmMessageCaptureNormalizedInboundToStore(
		store,
		rec,
		time.Now().UTC(),
	); err != nil || created {
		t.Fatalf("duplicate capture created=%t, err=%v", created, err)
	}

	select {
	case queued := <-automationQueue:
		if queued.ID != first.ID {
			t.Fatalf("queued event ID = %q, want %q", queued.ID, first.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first capture did not enqueue automation event")
	}
	select {
	case queued := <-automationQueue:
		t.Fatalf("duplicate capture enqueued event %#v", queued)
	default:
	}
}

func TestHRWorkbenchInboundSubscriptionResumesCursorAndStoresMessageOnce(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	cursorPath := filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName)
	cursor, err := OpenHRWorkbenchAgentCursorStore(cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Checkpoint("5"); err != nil {
		t.Fatal(err)
	}
	cursor, err = OpenHRWorkbenchAgentCursorStore(cursorPath)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan AgentInboundEvent, 2)
	eventErrors := make(chan error)
	first := task003AgentInboundEvent("6", "mid-resume", "hello")
	duplicate := first
	duplicate.Cursor = "7"
	events <- first
	events <- duplicate

	client := &task003AgentClient{
		subscribeEvents: events,
		subscribeErrors: eventErrors,
	}
	subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
	subscription.retryDelay = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- subscription.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for subscription.State().Cursor != "7" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not stop after cancellation")
	}

	if len(client.subscribeFrom) == 0 || client.subscribeFrom[0] != "5" {
		t.Fatalf("subscribe cursors = %#v, want first cursor 5", client.subscribeFrom)
	}
	state := crm.Snapshot()
	if len(state.MessageEvents) != 1 || state.Counters.Event != 1 {
		t.Fatalf("message events/counter = %d/%d, want 1/1", len(state.MessageEvents), state.Counters.Event)
	}
	if state.MessageEvents[0].MessageKey != first.MessageKey {
		t.Fatalf("stored message key = %q, want %q", state.MessageEvents[0].MessageKey, first.MessageKey)
	}
	if got := subscription.State().Cursor; got != "7" {
		t.Fatalf("cursor = %q, want 7", got)
	}

	reopened, err := OpenHRWorkbenchAgentCursorStore(cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().Cursor; got != "7" {
		t.Fatalf("persisted cursor = %q, want 7", got)
	}
}

func TestHRWorkbenchInboundSubscriptionRecordsCursorExpiredForReconciliation(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	cursorPath := filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName)
	cursor, err := OpenHRWorkbenchAgentCursorStore(cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	expired := &AgentError{
		Code:      AgentErrorCodeCursorExpired,
		Message:   "cursor is older than retained history",
		Retryable: false,
	}
	client := &task003AgentClient{subscribeErr: expired}
	subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
	subscription.retryDelay = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- subscription.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for subscription.State().ReconciliationError == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state := subscription.State()
	if state.ReconciliationError != expired.Message ||
		state.LastError != expired.Message ||
		state.Connected {
		t.Fatalf("cursor expired state = %#v", state)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/agent/events/status", nil)
	request.RemoteAddr = "127.0.0.1:4567"
	hrWorkbenchAgentEventStatusHandler(subscription)(response, request)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"reconciliation_error":"`+expired.Message+`"`)) {
		t.Fatalf("status response = %d %s", response.Code, response.Body.String())
	}
	time.Sleep(25 * time.Millisecond)
	if client.callCount("events.subscribe") != 1 {
		t.Fatalf(
			"events.subscribe calls before cancellation = %d, want 1",
			client.callCount("events.subscribe"),
		)
	}
	reopened, err := OpenHRWorkbenchAgentCursorStore(cursorPath)
	if err != nil {
		t.Fatal(err)
	}
	persisted := reopened.Snapshot()
	if persisted.ReconciliationError != expired.Message ||
		persisted.LastError != expired.Message ||
		persisted.Connected {
		t.Fatalf("persisted reconciliation state = %#v", persisted)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cursor-expired subscription did not stop after cancellation")
	}
	if client.callCount("events.subscribe") != 1 {
		t.Fatalf("events.subscribe calls = %d, want 1", client.callCount("events.subscribe"))
	}
}

func TestHRWorkbenchInboundSubscriptionRecordsStreamCursorExpiredForReconciliation(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	cursor, err := OpenHRWorkbenchAgentCursorStore(
		filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	expired := &AgentError{
		Code:      AgentErrorCodeCursorExpired,
		Message:   "cursor is older than retained history",
		Retryable: false,
	}
	events := make(chan AgentInboundEvent)
	errs := make(chan error, 1)
	errs <- expired
	client := &task003AgentClient{
		subscribeEvents: events,
		subscribeErrors: errs,
	}
	subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- subscription.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for subscription.State().ReconciliationError == "" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	state := subscription.State()
	if state.ReconciliationError != expired.Message || state.LastError != expired.Message {
		cancel()
		t.Fatalf("stream cursor expired state = %#v", state)
	}
	if client.callCount("events.subscribe") != 1 {
		cancel()
		t.Fatalf("events.subscribe calls = %d, want 1", client.callCount("events.subscribe"))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream cursor-expired subscription did not stop after cancellation")
	}
}

func TestHRWorkbenchInboundSubscriptionClassifiesImmediateErrors(t *testing.T) {
	testHRWorkbenchInboundSubscriptionErrorClassification(t, false)
}

func TestHRWorkbenchInboundSubscriptionClassifiesStreamErrors(t *testing.T) {
	testHRWorkbenchInboundSubscriptionErrorClassification(t, true)
}

func testHRWorkbenchInboundSubscriptionErrorClassification(t *testing.T, stream bool) {
	t.Helper()
	tests := []struct {
		name  string
		err   error
		retry bool
	}{
		{
			name: "non-retryable agent error",
			err: &AgentError{
				Code:    AgentErrorCodeUnsupportedVersion,
				Message: "synthetic unsupported version",
			},
		},
		{
			name: "retryable agent error",
			err: &AgentError{
				Code:      AgentErrorCodeUnavailable,
				Message:   "synthetic agent outage",
				Retryable: true,
			},
			retry: true,
		},
		{
			name:  "transport error",
			err:   errors.New("synthetic transport failure"),
			retry: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			crm := crmMessageOpenTestStore(t)
			cursor, err := OpenHRWorkbenchAgentCursorStore(
				filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName),
			)
			if err != nil {
				t.Fatal(err)
			}
			client := &task003AgentClient{}
			client.subscribe = func(
				context.Context,
				string,
			) (<-chan AgentInboundEvent, <-chan error, error) {
				if !stream {
					return nil, nil, test.err
				}
				events := make(chan AgentInboundEvent)
				close(events)
				errs := make(chan error, 1)
				errs <- test.err
				close(errs)
				return events, errs, nil
			}
			subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
			subscription.retryDelay = time.Millisecond

			if !test.retry {
				err := subscription.Run(context.Background())
				if !errors.Is(err, test.err) {
					t.Fatalf("Run() error = %v, want %v", err, test.err)
				}
				if got := client.callCount("events.subscribe"); got != 1 {
					t.Fatalf("events.subscribe calls = %d, want 1", got)
				}
				return
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- subscription.Run(ctx)
			}()
			deadline := time.Now().Add(2 * time.Second)
			for client.callCount("events.subscribe") < 2 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if got := client.callCount("events.subscribe"); got < 2 {
				cancel()
				t.Fatalf("events.subscribe calls = %d, want at least 2", got)
			}
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Run() error = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("retryable subscription did not stop after cancellation")
			}
		})
	}
}

func TestHRWorkbenchInboundConnectionFailureBacksOffAndCancellationInterruptsWait(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	cursor, err := OpenHRWorkbenchAgentCursorStore(
		filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	attempts := make(chan time.Time, 4)
	client := &task003AgentClient{}
	client.subscribe = func(
		context.Context,
		string,
	) (<-chan AgentInboundEvent, <-chan error, error) {
		attempts <- time.Now()
		return nil, nil, &AgentError{
			Code:      AgentErrorCodeUnavailable,
			Message:   "synthetic agent outage",
			Retryable: true,
		}
	}
	subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
	subscription.retryDelay = 80 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- subscription.Run(ctx)
	}()

	var first time.Time
	select {
	case first = <-attempts:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("first subscription attempt did not occur")
	}
	select {
	case second := <-attempts:
		cancel()
		t.Fatalf("retry occurred without backoff after %s", second.Sub(first))
	case <-time.After(30 * time.Millisecond):
	}

	var second time.Time
	select {
	case second = <-attempts:
	case <-time.After(500 * time.Millisecond):
		cancel()
		t.Fatal("subscription did not retry after backoff")
	}
	if elapsed := second.Sub(first); elapsed < 60*time.Millisecond {
		cancel()
		t.Fatalf("retry delay = %s, want at least 60ms", elapsed)
	}

	cancelStarted := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cancellation did not interrupt the reconnect backoff")
	}
	if elapsed := time.Since(cancelStarted); elapsed >= subscription.retryDelay {
		t.Fatalf("cancellation exit = %s, want faster than retry delay %s", elapsed, subscription.retryDelay)
	}
}

func TestHRWorkbenchInboundDoesNotCheckpointFailedCRMCommit(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	blockingFile := filepath.Join(t.TempDir(), "blocking-file")
	if err := os.WriteFile(blockingFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	crm.path = filepath.Join(blockingFile, "crm.json")

	cursor, err := OpenHRWorkbenchAgentCursorStore(
		filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan AgentInboundEvent, 1)
	events <- task003AgentInboundEvent("1", "mid-failed-commit", "hello")
	client := &task003AgentClient{
		subscribeEvents: events,
		subscribeErrors: make(chan error),
	}
	done := make(chan error, 1)
	go func() {
		done <- NewHRWorkbenchInboundSubscription(client, crm, cursor).
			Run(context.Background())
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Run() succeeded despite CRM persistence failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscription did not report CRM persistence failure")
	}
	if got := cursor.Snapshot().Cursor; got != "0" {
		t.Fatalf("cursor = %q, want 0 after failed CRM commit", got)
	}
	if state := crm.Snapshot(); len(state.MessageEvents) != 0 {
		t.Fatalf("failed CRM commit mutated message events: %#v", state.MessageEvents)
	}
}

func TestHRWorkbenchInboundEnsuresRunBeforeCheckpoint(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	cursor, err := OpenHRWorkbenchAgentCursorStore(
		filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	event := task003AgentInboundEvent("1", "mid-ensure-failed", "hello")
	events := make(chan AgentInboundEvent, 1)
	events <- event
	client := &task003AgentClient{
		subscribeEvents: events,
		subscribeErrors: make(chan error),
	}
	ensureErr := errors.New("synthetic ensure failure")
	ensureCalled := false
	subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
	subscription.ensureRun = func(
		_ context.Context,
		captured MessageEvent,
	) (AutomationRun, bool, error) {
		ensureCalled = true
		if got := cursor.Snapshot().Cursor; got != "0" {
			t.Fatalf("cursor during EnsureRun = %q, want 0", got)
		}
		if captured.MessageKey != event.MessageKey || captured.ID == "" {
			t.Fatalf("captured event passed to EnsureRun = %#v", captured)
		}
		return AutomationRun{}, false, ensureErr
	}

	err = subscription.Run(context.Background())
	if !errors.Is(err, ensureErr) {
		t.Fatalf("Run() error = %v, want %v", err, ensureErr)
	}
	if !ensureCalled {
		t.Fatal("EnsureRun was not called")
	}
	if got := cursor.Snapshot().Cursor; got != "0" {
		t.Fatalf("cursor = %q, want 0 after EnsureRun failure", got)
	}
	state := crm.Snapshot()
	if len(state.MessageEvents) != 1 || state.MessageEvents[0].MessageKey != event.MessageKey {
		t.Fatalf("CRM event was not durably persisted before EnsureRun: %#v", state.MessageEvents)
	}
}

func TestHRWorkbenchInboundCrashReplayIsExactlyOnceByMessageKey(t *testing.T) {
	// 入向自动化默认关闭（微信侧对话循环由 DSH 驱动）。本用例验证的是"入队"语义，
	// 与队列/去重相关，因此显式打开该开关。
	t.Setenv("ONEBOT_INBOUND_AUTOMATION", "1")
	automationQueue := make(chan MessageEvent, 2)
	automationRuntimeMu.Lock()
	previousRuntime := automationRuntime
	automationRuntime = &AutomationRuntime{events: automationQueue}
	automationRuntimeMu.Unlock()
	t.Cleanup(func() {
		automationRuntimeMu.Lock()
		automationRuntime = previousRuntime
		automationRuntimeMu.Unlock()
	})

	directory := t.TempDir()
	crmPath := filepath.Join(directory, "crm.json")
	crm, err := OpenCRMStore(crmPath)
	if err != nil {
		t.Fatal(err)
	}
	event := task003AgentInboundEvent("1", "mid-crash-replay", "hello")
	captured, created, err := crmMessageCaptureAgentInboundToStore(
		crm,
		event,
		time.Now().UTC(),
	)
	if err != nil || !created {
		t.Fatalf("initial commit = %#v, created=%t, err=%v", captured, created, err)
	}
	if err := crm.Close(); err != nil {
		t.Fatal(err)
	}
	crm, err = OpenCRMStore(crmPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})

	cursor, err := OpenHRWorkbenchAgentCursorStore(
		filepath.Join(directory, defaultHRWorkbenchAgentCursorFileName),
	)
	if err != nil {
		t.Fatal(err)
	}

	events := make(chan AgentInboundEvent, 1)
	events <- event
	client := &task003AgentClient{
		subscribeEvents: events,
		subscribeErrors: make(chan error),
	}
	subscription := NewHRWorkbenchInboundSubscription(client, crm, cursor)
	ensureCalls := 0
	subscription.ensureRun = func(
		_ context.Context,
		replayed MessageEvent,
	) (AutomationRun, bool, error) {
		ensureCalls++
		if replayed.ID != captured.ID || replayed.MessageKey != captured.MessageKey {
			t.Fatalf("replayed event = %#v, want persisted event %#v", replayed, captured)
		}
		return AutomationRun{
			ID:     "run_00000001",
			Status: AutomationRunQueued,
		}, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- subscription.Run(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for cursor.Snapshot().Cursor != "1" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := cursor.Snapshot().Cursor; got != "1" {
		cancel()
		t.Fatalf("cursor = %q, want 1 after crash replay", got)
	}
	state := crm.Snapshot()
	if len(state.MessageEvents) != 1 ||
		state.Counters.Event != 1 ||
		state.MessageEvents[0].ID != captured.ID ||
		state.MessageEvents[0].MessageKey != event.MessageKey {
		cancel()
		t.Fatalf("crash replay duplicated message event: %#v", state.MessageEvents)
	}
	if ensureCalls != 1 {
		cancel()
		t.Fatalf("EnsureRun calls = %d, want 1", ensureCalls)
	}
	select {
	case queued := <-automationQueue:
		if queued.ID != captured.ID {
			cancel()
			t.Fatalf("replayed automation event ID = %q, want %q", queued.ID, captured.ID)
		}
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("crash replay did not enqueue automation event")
	}
	if got := len(automationQueue); got != 0 {
		cancel()
		t.Fatalf("automation replay queue length = %d, want 0 after draining", got)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("crash replay subscription did not stop")
	}
}

func TestHRWorkbenchCursorCheckpointRejectsRollback(t *testing.T) {
	cursor, err := OpenHRWorkbenchAgentCursorStore(
		filepath.Join(t.TempDir(), defaultHRWorkbenchAgentCursorFileName),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := cursor.Checkpoint("9"); err != nil {
		t.Fatal(err)
	}
	if err := cursor.Checkpoint("8"); err == nil {
		t.Fatal("Checkpoint() allowed the cursor to move backwards")
	}
	if got := cursor.Snapshot().Cursor; got != "9" {
		t.Fatalf("cursor = %q, want 9", got)
	}
}

func task003AgentInboundEvent(cursor, mid, text string) AgentInboundEvent {
	event := AgentInboundEvent{
		Cursor:    cursor,
		Source:    crmMessageSource,
		Mid:       mid,
		ChatType:  ChatTypePrivate,
		PeerID:    "wxid_peer",
		AuthorID:  "wxid_peer",
		Text:      text,
		Kinds:     []string{"text"},
		Timestamp: 1_700_000_000,
	}
	event.MessageKey = BuildMessageKey(
		event.ChatType,
		event.PeerID,
		event.Mid,
		event.AuthorID,
		event.GroupID,
		event.Timestamp,
		event.Text,
		event.Kinds,
	)
	return event
}

func TestCRMMessageRoutesListDetailAndReview(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	now := time.Date(2026, time.July, 20, 8, 0, 0, 0, time.UTC)
	records := []*ForwardRecord{
		{T: 100, Chat: "private", Dir: "in", Peer: "wxid_a", Author: "wxid_a", Text: "Go backend", Kinds: []string{"text"}, Mid: "mid-a"},
		{T: 300, Chat: "group", Dir: "in", Peer: "group@chatroom", Author: "wxid_b", GroupID: "group@chatroom", Text: "Rust role", Kinds: []string{"text"}, Mid: "mid-b"},
		{T: 200, Chat: "private", Dir: "in", Peer: "wxid_c", Author: "wxid_c", Text: "Go platform", Kinds: []string{"text"}, Mid: "mid-c"},
	}
	for i, rec := range records {
		if _, _, err := crmMessageCaptureNormalizedInboundToStore(store, rec, now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	registerCRMMessageRoutes(mux, store)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/crm/message_events?chat_type=private&review_status=unreviewed&linked=false&q=go&limit=1&offset=0", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var list struct {
		Total  int            `json:"total"`
		Events []MessageEvent `json:"events"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 2 || len(list.Events) != 1 || list.Events[0].Mid != "mid-c" {
		t.Fatalf("list response = %+v", list)
	}

	eventID := list.Events[0].ID
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/crm/message_events/"+eventID, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	body := bytes.NewBufferString(`{"expected_version":1,"review_status":"reviewed"}`)
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/crm/message_events/"+eventID, body))
	if response.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", response.Code, response.Body.String())
	}
	var updated MessageEvent
	if err := json.Unmarshal(response.Body.Bytes(), &updated); err != nil {
		t.Fatal(err)
	}
	if updated.ReviewStatus != ReviewStatusReviewed || updated.Version != 2 || !updated.UpdatedAt.After(updated.CreatedAt) {
		t.Fatalf("updated event = %+v", updated)
	}

	response = httptest.NewRecorder()
	body = bytes.NewBufferString(`{"expected_version":1,"review_status":"unreviewed"}`)
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/crm/message_events/"+eventID, body))
	crmMessageAssertError(t, response, http.StatusConflict, "conflict")
}

func TestCRMMessageRoutesExposeJobIngestionProjection(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	groupID := "message_projection@chatroom"
	crmJobIngestionSeedSourceGroup(t, store, groupID, true)
	event := crmJobIngestionSeedEvent(t, store, groupID, "mid-message-projection", "projected job")
	ingested, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: event.ID,
		GroupID: groupID,
		Job:     crmJobIngestionDraft("MESSAGE-PROJECTION-1"),
	})
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerCRMMessageRoutes(mux, store)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/crm/message_events", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", response.Code, response.Body.String())
	}
	var list struct {
		Total           int                           `json:"total"`
		Events          []crmMessageEventProjection   `json:"events"`
		IngestionTotals crmJobIngestionDecisionTotals `json:"ingestion_totals"`
		RecentDecisions []JobProvenance               `json:"recent_decisions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 ||
		len(list.Events) != 1 ||
		list.Events[0].JobIngestion == nil ||
		list.Events[0].JobIngestion.Decision != JobIngestionCreate ||
		list.Events[0].JobIngestion.Provenance != ingested.Provenance ||
		list.IngestionTotals.Create != 1 ||
		len(list.RecentDecisions) != 1 ||
		list.RecentDecisions[0] != ingested.Provenance {
		t.Fatalf("message ingestion list projection = %+v", list)
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/api/crm/message_events/"+event.ID, nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("detail status = %d, body = %s", response.Code, response.Body.String())
	}
	var detail crmMessageEventProjection
	if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail.ID != event.ID ||
		detail.JobIngestion == nil ||
		detail.JobIngestion.Provenance != ingested.Provenance {
		t.Fatalf("message ingestion detail projection = %+v", detail)
	}

	current, found := crmMessageFindEvent(store.Snapshot(), event.ID)
	if !found {
		t.Fatalf("message event %q not found after ingestion", event.ID)
	}
	patchBody, err := json.Marshal(ReviewUpdate{
		ExpectedVersion: current.Version,
		ReviewStatus:    ReviewStatusReviewed,
	})
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodPatch,
			"/api/crm/message_events/"+event.ID,
			bytes.NewReader(patchBody),
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status = %d, body = %s", response.Code, response.Body.String())
	}
	var patched crmMessageEventProjection
	if err := json.Unmarshal(response.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Version != current.Version+1 ||
		patched.ReviewStatus != ReviewStatusReviewed ||
		patched.JobIngestion == nil ||
		patched.JobIngestion.Provenance != ingested.Provenance {
		t.Fatalf("message ingestion patch projection = %+v", patched)
	}
}

func TestCRMMessageRoutesRejectInvalidInput(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	mux := http.NewServeMux()
	registerCRMMessageRoutes(mux, store)

	tests := []struct {
		name   string
		method string
		target string
		body   []byte
		status int
		code   string
	}{
		{name: "invalid review filter", method: http.MethodGet, target: "/api/crm/message_events?review_status=pending", status: http.StatusBadRequest, code: "validation_error"},
		{name: "invalid linked filter", method: http.MethodGet, target: "/api/crm/message_events?linked=yes", status: http.StatusBadRequest, code: "validation_error"},
		{name: "limit too large", method: http.MethodGet, target: "/api/crm/message_events?limit=201", status: http.StatusBadRequest, code: "validation_error"},
		{name: "unsupported method", method: http.MethodPost, target: "/api/crm/message_events", status: http.StatusMethodNotAllowed, code: "method_not_allowed"},
		{name: "unknown field", method: http.MethodPatch, target: "/api/crm/message_events/event_00000001", body: []byte(`{"expected_version":1,"review_status":"reviewed","extra":true}`), status: http.StatusBadRequest, code: "invalid_json"},
		{name: "non object", method: http.MethodPatch, target: "/api/crm/message_events/event_00000001", body: []byte(`null`), status: http.StatusBadRequest, code: "invalid_json"},
		{name: "trailing json", method: http.MethodPatch, target: "/api/crm/message_events/event_00000001", body: []byte(`{"expected_version":1,"review_status":"reviewed"} {}`), status: http.StatusBadRequest, code: "invalid_json"},
		{name: "body too large", method: http.MethodPatch, target: "/api/crm/message_events/event_00000001", body: bytes.Repeat([]byte("x"), CRMMaxRequestBodyBytes+1), status: http.StatusRequestEntityTooLarge, code: "body_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.target, bytes.NewReader(test.body))
			mux.ServeHTTP(response, request)
			crmMessageAssertError(t, response, test.status, test.code)
			if test.status == http.StatusMethodNotAllowed && response.Header().Get("Allow") == "" {
				t.Fatal("Allow header is empty")
			}
		})
	}
}

func TestCRMMessageReviewPersistenceFailureRollsBack(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	event, _, err := crmMessageCaptureNormalizedInboundToStore(store, &ForwardRecord{
		T:      1_700_000_000,
		Chat:   "private",
		Dir:    "in",
		Peer:   "wxid_peer",
		Author: "wxid_peer",
		Text:   "hello",
		Kinds:  []string{"text"},
		Mid:    "mid-review-failure",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	blockingFile := filepath.Join(t.TempDir(), "blocking-file")
	if err := os.WriteFile(blockingFile, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blockingFile, "crm.json")

	mux := http.NewServeMux()
	registerCRMMessageRoutes(mux, store)
	response := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"expected_version":1,"review_status":"reviewed"}`)
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/crm/message_events/"+event.ID, body))
	crmMessageAssertError(t, response, http.StatusInternalServerError, "persistence_error")

	current, found := crmMessageFindEvent(store.Snapshot(), event.ID)
	if !found {
		t.Fatal("message event disappeared after failed persistence")
	}
	if current.ReviewStatus != ReviewStatusUnreviewed || current.Version != 1 {
		t.Fatalf("event changed after failed persistence: %+v", current)
	}
}

func TestCRMMessageConcurrentReviewUpdateConflicts(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	event, _, err := crmMessageCaptureNormalizedInboundToStore(store, &ForwardRecord{
		T:      1_700_000_000,
		Chat:   "private",
		Dir:    "in",
		Peer:   "wxid_peer",
		Author: "wxid_peer",
		Text:   "hello",
		Kinds:  []string{"text"},
		Mid:    "mid-review-concurrent",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	registerCRMMessageRoutes(mux, store)
	statuses := make(chan int, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := httptest.NewRecorder()
			body := bytes.NewBufferString(`{"expected_version":1,"review_status":"reviewed"}`)
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/crm/message_events/"+event.ID, body))
			statuses <- response.Code
		}()
	}
	wg.Wait()
	close(statuses)

	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusConflict] != 1 {
		t.Fatalf("review statuses = %#v, want one 200 and one 409", counts)
	}
}

func TestCRMMessageListAndGetAreBounded(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	now := time.Date(2026, time.July, 26, 1, 0, 0, 0, time.UTC)
	for index, text := range []string{"oldest", "middle", "newest"} {
		if _, _, err := crmMessageCaptureNormalizedInboundToStore(store, &ForwardRecord{
			T: int64(index + 1), Chat: "private", Dir: "in",
			Peer: "wxid_peer", Author: "wxid_peer", Text: text,
			Kinds: []string{"text"}, Mid: "mid-" + text,
		}, now.Add(time.Duration(index)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	total, first, err := crmMessageList(store, crmMessageListFilter{Limit: 2, Offset: 0})
	if err != nil || total != 3 || len(first) != 2 ||
		first[0].Text != "newest" || first[1].Text != "middle" {
		t.Fatalf("first page total=%d events=%+v err=%v", total, first, err)
	}
	total, second, err := crmMessageList(store, crmMessageListFilter{Limit: 2, Offset: 2})
	if err != nil || total != 3 || len(second) != 1 || second[0].Text != "oldest" {
		t.Fatalf("second page total=%d events=%+v err=%v", total, second, err)
	}
	got, err := crmMessageGet(store, first[0].ID)
	if err != nil || got.ID != first[0].ID {
		t.Fatalf("message get = %+v, err=%v", got, err)
	}
	if _, err := crmMessageGet(store, "event_99999999"); !errors.Is(err, crmMessageErrNotFound) {
		t.Fatalf("missing message error = %v", err)
	}
	for _, limit := range []int{0, WorkbenchMaxPageLimit + 1} {
		if _, _, err := crmMessageList(store, crmMessageListFilter{Limit: limit}); !errors.Is(err, ErrCRMValidation) {
			t.Errorf("limit %d error = %v, want validation", limit, err)
		}
	}
}

func crmMessageOpenTestStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		crmMessageSetStore(nil)
		_ = store.Close()
	})
	return store
}

func crmMessageAssertError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, status, response.Body.String())
	}
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != code || body.Error == "" {
		t.Fatalf("error response = %+v, want code %q", body, code)
	}
}
