package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type automationRuntimeTestRunEngine struct {
	ensureRun               func(context.Context, MessageEvent, string) (AutomationRun, bool, error)
	processRun              func(context.Context, string) error
	recoverScheduledActions func(context.Context) error
	processQueued           func(context.Context) error
}

func (e *automationRuntimeTestRunEngine) EnsureRun(
	ctx context.Context,
	event MessageEvent,
	providerName string,
) (AutomationRun, bool, error) {
	if e.ensureRun == nil {
		return AutomationRun{}, false, nil
	}
	return e.ensureRun(ctx, event, providerName)
}

func (e *automationRuntimeTestRunEngine) RetryRun(
	context.Context,
	string,
	uint64,
	string,
	string,
) (AutomationRun, error) {
	return AutomationRun{}, nil
}

func (e *automationRuntimeTestRunEngine) ProcessRun(ctx context.Context, runID string) error {
	if e.processRun == nil {
		return nil
	}
	return e.processRun(ctx, runID)
}

func (e *automationRuntimeTestRunEngine) RecoverScheduledActions(ctx context.Context) error {
	if e.recoverScheduledActions == nil {
		return nil
	}
	return e.recoverScheduledActions(ctx)
}

func (e *automationRuntimeTestRunEngine) ProcessQueued(ctx context.Context) error {
	if e.processQueued == nil {
		return nil
	}
	return e.processQueued(ctx)
}

func TestAutomationRuntimeWorkerProcessesQueuedRunsOnceAndStopsOnCancellation(t *testing.T) {
	var queueScans atomic.Int32
	started := make(chan struct{})
	engine := &automationRuntimeTestRunEngine{
		processQueued: func(context.Context) error {
			queueScans.Add(1)
			close(started)
			return nil
		},
	}
	runtime := &AutomationRuntime{
		service: &AutomationService{engine: engine},
		events:  make(chan MessageEvent),
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.wg.Add(1)
	done := make(chan struct{})
	go func() {
		runtime.worker(ctx)
		close(done)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup queued-run recovery was not called")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation worker did not stop after cancellation")
	}
	if got := queueScans.Load(); got != 1 {
		t.Fatalf("ProcessQueued calls = %d, want 1", got)
	}
}

func TestAutomationRuntimeCloseCancelsWorkAndTimesOutBoundedly(t *testing.T) {
	t.Run("cancels work", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		runtime := &AutomationRuntime{
			ctx:          ctx,
			cancel:       cancel,
			done:         ctx.Done(),
			closeTimeout: time.Second,
		}
		runtime.wg.Add(1)
		finished := make(chan struct{})
		go func() {
			defer runtime.wg.Done()
			<-ctx.Done()
			close(finished)
		}()

		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("runtime work did not observe cancellation")
		}
	})

	t.Run("times out", func(t *testing.T) {
		blocked := make(chan struct{})
		runtime := &AutomationRuntime{closeTimeout: 10 * time.Millisecond}
		runtime.wg.Add(1)
		done := make(chan struct{})
		go func() {
			defer runtime.wg.Done()
			defer close(done)
			<-blocked
		}()

		start := time.Now()
		err := runtime.Close()
		if err == nil || err.Error() != "automation runtime shutdown timeout" {
			t.Fatalf("runtime close error = %v", err)
		}
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("runtime close took %s", elapsed)
		}
		if second := runtime.Close(); second == nil || second.Error() != err.Error() {
			t.Fatalf("second runtime close error = %v, want %v", second, err)
		}

		close(blocked)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed-out runtime work did not exit after release")
		}
	})
}

func TestAutomationRuntimeProcessQueuedUntilIdleDrainsMultipleBatches(t *testing.T) {
	provider := &automationRunEngineRecordingProvider{}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	for i := 0; i < automationQueuedRunBatchDefault+1; i++ {
		automationRunEngineTestEnsureQueued(t, engine, crm, i)
	}
	runtime := &AutomationRuntime{
		store:   store,
		service: &AutomationService{engine: engine},
	}

	if err := runtime.processQueuedUntilIdle(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if got := automationQueuedRunCount(store.Snapshot()); got != 0 {
		t.Fatalf("queued runs = %d, want 0", got)
	}
	if got := len(provider.Calls()); got != automationQueuedRunBatchDefault+1 {
		t.Fatalf("provider calls = %d, want %d", got, automationQueuedRunBatchDefault+1)
	}
}

func TestAutomationRuntimeProcessQueuedUntilIdleScansWithoutQueuedRuns(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var queueScans atomic.Int32
	engine := &automationRuntimeTestRunEngine{
		processQueued: func(context.Context) error {
			queueScans.Add(1)
			return nil
		},
	}
	runtime := &AutomationRuntime{
		store:   store,
		service: &AutomationService{engine: engine},
	}

	if err := runtime.processQueuedUntilIdle(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := queueScans.Load(); got != 1 {
		t.Fatalf("ProcessQueued calls = %d, want 1", got)
	}
}

func TestAutomationRuntimeWorkerResumesQueuedRunsAfterCooldown(t *testing.T) {
	provider := &automationRunEngineRecordingProvider{failures: map[string]error{}}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	first := automationRunEngineTestEnsureQueued(t, engine, crm, 0)
	second := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
	provider.failures[first.ID] = ErrOpenAIAutomationRequestFailed
	engine.queuedProviderCooldown = 10 * time.Millisecond

	runtime := &AutomationRuntime{
		store:             store,
		service:           &AutomationService{engine: engine},
		events:            make(chan MessageEvent),
		queueScanInterval: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.wg.Add(1)
	done := make(chan struct{})
	go func() {
		runtime.worker(ctx)
		close(done)
	}()

	deadline := time.After(2 * time.Second)
	for {
		state := store.Snapshot()
		recovered := automationRunEngineTestRun(t, state, second.ID)
		if recovered.Status == AutomationRunSucceeded {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("second queued run was not resumed: %#v", recovered)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation worker did not stop after cancellation")
	}
	if failed := automationRunEngineTestRun(t, store.Snapshot(), first.ID); failed.Status != AutomationRunFailed {
		t.Fatalf("first run = %#v", failed)
	}
}

func TestAutomationRuntimeWorkerDoesNotRetryStartupRecoveryError(t *testing.T) {
	var queueScans atomic.Int32
	attempts := make(chan struct{}, 2)
	engine := &automationRuntimeTestRunEngine{
		processQueued: func(context.Context) error {
			queueScans.Add(1)
			attempts <- struct{}{}
			return errors.New("synthetic recovery failure")
		},
	}
	runtime := &AutomationRuntime{
		service: &AutomationService{engine: engine},
		events:  make(chan MessageEvent),
	}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.wg.Add(1)
	done := make(chan struct{})
	go func() {
		runtime.worker(ctx)
		close(done)
	}()

	select {
	case <-attempts:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup queued-run recovery was not called")
	}
	select {
	case <-attempts:
		cancel()
		t.Fatal("startup approved-action recovery was retried")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation worker did not stop after cancellation")
	}
	if got := queueScans.Load(); got != 1 {
		t.Fatalf("ProcessQueued calls = %d, want 1", got)
	}
}

func TestRecoverInterruptedAutomationRunsRequeuesRunAndTrace(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	startedAt := time.Now().UTC().Add(-time.Minute)
	running := automationRunEngineTestAppendBacklogRun(
		t,
		store,
		AutomationRunRunning,
		startedAt,
	)

	if err := recoverInterruptedAutomationRuns(store); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	recovered := automationRunEngineTestRun(t, state, running.ID)
	turn := automationTurnByID(&state, recovered.TurnID)
	session := automationSessionByID(&state, recovered.SessionID)
	if recovered.Status != AutomationRunQueued ||
		recovered.StartedAt != nil ||
		recovered.Version != running.Version+1 {
		t.Fatalf("recovered run = %#v", recovered)
	}
	if turn == nil || turn.Status != AutomationTurnQueued || turn.StartedAt != nil {
		t.Fatalf("recovered turn = %#v", turn)
	}
	if session == nil || session.Status != AutomationSessionStatusActive {
		t.Fatalf("recovered session = %#v", session)
	}
}

func TestAutomationServiceRecoversInterruptedExecutingActionsAsUnknown(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionSucceeded,
		mustAutomationSchedulerTestResult(t, AutomationEntityCandidate, "cand_00000001", 1),
	)
	if err := store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationActionIndex(state, fixture.childID)
		state.Actions[index].Status = AutomationActionExecuting
		state.Actions[index].Version++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewAutomationService(store, nil, nil, nil, nil, nil)

	if err := service.recoverInterruptedExecutingActions(); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	action := automationSchedulerTestAction(t, state, fixture.childID)
	if action.Status != AutomationActionUnknown ||
		action.ErrorCode != "execution_interrupted_unknown" ||
		action.Retryable {
		t.Fatalf("recovered action = %#v", action)
	}
	foundAudit := false
	for _, record := range state.PendingAudit {
		if record.ActionID == action.ID &&
			record.EventType == AutomationAuditEventActionFailed {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatal("interrupted action recovery audit is missing")
	}
}

func TestAutomationRuntimeWorkerReturnsWhenStartupRecoveryIsCanceled(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	automationAuditAppendPending(t, store, time.Date(2026, 7, 26, 6, 0, 0, 0, time.UTC), 1)
	var eventCalls atomic.Int32
	engine := &automationRuntimeTestRunEngine{
		processQueued: func(context.Context) error {
			return context.Canceled
		},
		ensureRun: func(context.Context, MessageEvent, string) (AutomationRun, bool, error) {
			eventCalls.Add(1)
			return AutomationRun{}, false, nil
		},
	}
	runtime := &AutomationRuntime{
		audit:   ledger,
		service: &AutomationService{store: store, engine: engine},
		events:  make(chan MessageEvent, 1),
	}
	runtime.events <- MessageEvent{ID: "event_after_canceled_recovery"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime.wg.Add(1)
	done := make(chan struct{})
	go func() {
		runtime.worker(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation worker did not return after canceled startup recovery")
	}
	if got := eventCalls.Load(); got != 0 {
		t.Fatalf("event processing calls = %d, want 0", got)
	}
	if got := len(store.Snapshot().PendingAudit); got != 1 {
		t.Fatalf("pending audit count = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "audit", "2026-07-26.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup audit ledger exists after canceled recovery: %v", err)
	}
}

func TestAutomationRuntimeWorkerUsesContextForStartupAuditDrain(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	automationAuditAppendPending(t, store, time.Date(2026, 7, 26, 6, 30, 0, 0, time.UTC), 1)
	var eventCalls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	engine := &automationRuntimeTestRunEngine{
		processQueued: func(context.Context) error {
			cancel()
			return nil
		},
		ensureRun: func(context.Context, MessageEvent, string) (AutomationRun, bool, error) {
			eventCalls.Add(1)
			return AutomationRun{}, false, nil
		},
	}
	runtime := &AutomationRuntime{
		audit:   ledger,
		service: &AutomationService{store: store, engine: engine},
		events:  make(chan MessageEvent, 1),
	}
	runtime.events <- MessageEvent{ID: "event_after_canceled_startup_context"}
	runtime.wg.Add(1)
	done := make(chan struct{})
	go func() {
		runtime.worker(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation worker did not return after startup context cancellation")
	}
	if got := eventCalls.Load(); got != 0 {
		t.Fatalf("event processing calls = %d, want 0", got)
	}
	if got := len(store.Snapshot().PendingAudit); got != 1 {
		t.Fatalf("pending audit count = %d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "audit", "2026-07-26.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup audit ledger exists after context cancellation: %v", err)
	}
}

func TestAutomationRuntimeWorkerDrainsAuditAfterScheduledActionRecovery(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	automationAuditAppendPending(t, store, time.Date(2026, 7, 26, 7, 0, 0, 0, time.UTC), 1)
	auditPath := filepath.Join(dataDir, "audit", "2026-07-26.jsonl")

	var recoverySawPending atomic.Bool
	var eventSawDrained atomic.Bool
	processed := make(chan struct{})
	engine := &automationRuntimeTestRunEngine{
		processQueued: func(context.Context) error {
			_, statErr := os.Stat(auditPath)
			recoverySawPending.Store(
				len(store.Snapshot().PendingAudit) == 1 &&
					errors.Is(statErr, os.ErrNotExist),
			)
			return nil
		},
		ensureRun: func(context.Context, MessageEvent, string) (AutomationRun, bool, error) {
			data, readErr := os.ReadFile(auditPath)
			eventSawDrained.Store(
				len(store.Snapshot().PendingAudit) == 0 &&
					readErr == nil &&
					bytes.Count(data, []byte{'\n'}) == 1,
			)
			close(processed)
			return AutomationRun{}, false, nil
		},
	}
	runtime := &AutomationRuntime{
		audit:   ledger,
		service: &AutomationService{store: store, engine: engine},
		events:  make(chan MessageEvent, 1),
	}
	runtime.events <- MessageEvent{ID: "event_startup_order"}
	ctx, cancel := context.WithCancel(context.Background())
	runtime.wg.Add(1)
	done := make(chan struct{})
	go func() {
		runtime.worker(ctx)
		close(done)
	}()

	select {
	case <-processed:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("startup audit drain did not complete before event processing")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("automation worker did not stop after cancellation")
	}
	if !recoverySawPending.Load() {
		t.Fatal("queued-run recovery did not run before startup audit drain")
	}
	if !eventSawDrained.Load() {
		t.Fatal("event processing began before startup audit drain completed")
	}
}

func TestAutomationEnqueueEventBackpressuresUntilQueueDrains(t *testing.T) {
	events := make(chan MessageEvent, 1)
	done := make(chan struct{})
	runtime := &AutomationRuntime{events: events, done: done}
	automationRuntimeMu.Lock()
	previous := automationRuntime
	automationRuntime = runtime
	automationRuntimeMu.Unlock()
	t.Cleanup(func() {
		automationRuntimeMu.Lock()
		automationRuntime = previous
		automationRuntimeMu.Unlock()
		close(done)
	})

	first := MessageEvent{ID: "event_first"}
	second := MessageEvent{ID: "event_second"}
	automationEnqueueEvent(first)
	enqueued := make(chan struct{})
	go func() {
		automationEnqueueEvent(second)
		close(enqueued)
	}()
	select {
	case <-enqueued:
		t.Fatal("second event bypassed queue backpressure")
	case <-time.After(50 * time.Millisecond):
	}
	if got := <-events; got.ID != first.ID {
		t.Fatalf("first dequeued event = %#v", got)
	}
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		t.Fatal("blocked enqueue did not resume after queue drain")
	}
	if got := <-events; got.ID != second.ID {
		t.Fatalf("second dequeued event = %#v", got)
	}
}

func TestAutomationEnqueueEventUnblocksWhenRuntimeStops(t *testing.T) {
	events := make(chan MessageEvent, 1)
	done := make(chan struct{})
	runtime := &AutomationRuntime{events: events, done: done}
	automationRuntimeMu.Lock()
	previous := automationRuntime
	automationRuntime = runtime
	automationRuntimeMu.Unlock()
	t.Cleanup(func() {
		automationRuntimeMu.Lock()
		automationRuntime = previous
		automationRuntimeMu.Unlock()
	})

	events <- MessageEvent{ID: "event_full"}
	enqueued := make(chan struct{})
	go func() {
		automationEnqueueEvent(MessageEvent{ID: "event_waiting"})
		close(enqueued)
	}()
	select {
	case <-enqueued:
		t.Fatal("enqueue returned before runtime stopped")
	case <-time.After(50 * time.Millisecond):
	}
	close(done)
	select {
	case <-enqueued:
	case <-time.After(time.Second):
		t.Fatal("enqueue remained blocked after runtime stopped")
	}
}

func TestAutomationServiceProcessEventTargetsOnlyItsRun(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		state.Runs = append(state.Runs, AutomationRun{
			ID: id, IdempotencyKey: "old-backlog", EngineVersion: AutomationEngineVersion,
			ProviderName: "old-provider", SourceMessageEventID: "event_00000001",
			SourceMessageKey: "old-message", SourceMessageVersion: 1,
			Status: AutomationRunQueued, Version: 1, Attempt: 1,
			ActionIDs: []string{}, CreatedAt: now,
		})
		state.Idempotency["old-backlog"] = AutomationIdempotencyRecord{
			Kind: "run", ResourceID: id, CreatedAt: now,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	newRun := AutomationRun{
		ID: "airun_00000002", Status: AutomationRunQueued,
		SourceMessageEventID: "event_00000002",
	}
	var processed []string
	var queueScans atomic.Int32
	engine := &automationRuntimeTestRunEngine{
		ensureRun: func(context.Context, MessageEvent, string) (AutomationRun, bool, error) {
			return newRun, true, nil
		},
		processRun: func(_ context.Context, runID string) error {
			processed = append(processed, runID)
			return nil
		},
		processQueued: func(context.Context) error {
			queueScans.Add(1)
			return nil
		},
	}
	service := &AutomationService{store: store, engine: engine}
	event := automationRunEngineTestEvent("event_00000002", "new-event", 1)
	result, err := service.ProcessEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != newRun.ID {
		t.Fatalf("ProcessEvent() run = %#v, want %q", result, newRun.ID)
	}
	if len(processed) != 1 || processed[0] != newRun.ID {
		t.Fatalf("processed run IDs = %v, want [%s]", processed, newRun.ID)
	}
	if got := queueScans.Load(); got != 0 {
		t.Fatalf("ProcessEvent provider queue scans = %d, want 0", got)
	}
	old := store.Snapshot().Runs[0]
	if old.Status != AutomationRunQueued {
		t.Fatalf("old backlog run changed = %#v", old)
	}
}
