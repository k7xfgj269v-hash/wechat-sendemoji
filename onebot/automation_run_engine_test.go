package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutomationRunEngineEnsureRunIgnoresMessageVersion(t *testing.T) {
	engine, store := automationRunEngineTestOpen(t)
	ctx := context.Background()
	event := automationRunEngineTestEvent("event_00000001", "mid-1", 1)

	first, created, err := engine.EnsureRun(ctx, event, "provider-v1")
	if err != nil || !created {
		t.Fatalf("first EnsureRun() = %#v, %v, %v", first, created, err)
	}
	updated := event
	updated.Version = 2
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
	second, created, err := engine.EnsureRun(ctx, updated, "provider-v2")
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("updated EnsureRun() = %#v, %v, %v; want run %q", second, created, err, first.ID)
	}
	if second.SourceMessageVersion != event.Version || second.ProviderName != first.ProviderName {
		t.Fatalf("updated EnsureRun() changed original run = %#v", second)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Runs) != 1 || len(snapshot.Idempotency) != 1 {
		t.Fatalf("state contains duplicate run data: runs=%d idempotency=%d", len(snapshot.Runs), len(snapshot.Idempotency))
	}
}

func TestAutomationRunEngineEnsureRunReusesPersistedLegacyKey(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	event := automationRunEngineTestEvent("event_00000001", "mid-legacy", 1)
	legacyKey := fmt.Sprintf("run:%s:%s:%d", AutomationEngineVersion, event.MessageKey, event.Version)
	var legacy AutomationRun
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		legacy = AutomationRun{
			ID:                   id,
			IdempotencyKey:       legacyKey,
			EngineVersion:        AutomationEngineVersion,
			ProviderName:         "legacy-provider",
			SourceMessageEventID: event.ID,
			SourceMessageKey:     event.MessageKey,
			SourceMessageVersion: event.Version,
			Status:               AutomationRunQueued,
			Version:              1,
			Attempt:              1,
			ActionIDs:            []string{},
			CreatedAt:            event.CreatedAt,
		}
		state.Runs = append(state.Runs, legacy)
		state.Idempotency[legacyKey] = AutomationIdempotencyRecord{
			Kind:       "run",
			ResourceID: id,
			CreatedAt:  event.CreatedAt,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	engine := automationRunEngineTestNewEngine(t, store)
	updated := event
	updated.Version = 2
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
	reused, created, err := engine.EnsureRun(context.Background(), updated, "")
	if err != nil || created || reused.ID != legacy.ID || reused.IdempotencyKey != legacyKey {
		t.Fatalf("legacy EnsureRun() = %#v, %v, %v; want %#v", reused, created, err, legacy)
	}
	snapshot := store.Snapshot()
	canonicalKey := fmt.Sprintf("run:%s:%s", AutomationEngineVersion, event.MessageKey)
	if len(snapshot.Runs) != 1 || len(snapshot.Idempotency) != 1 {
		t.Fatalf("legacy state contains duplicate run data: runs=%d idempotency=%d", len(snapshot.Runs), len(snapshot.Idempotency))
	}
	if _, exists := snapshot.Idempotency[canonicalKey]; exists {
		t.Fatalf("legacy reuse added canonical alias %q", canonicalKey)
	}
	if err := ValidateAutomationState(&snapshot); err != nil {
		t.Fatalf("legacy state validation error = %v", err)
	}
}

func TestAutomationRunEngineEnsureRunSeparatesMessageKeys(t *testing.T) {
	engine, store := automationRunEngineTestOpen(t)
	ctx := context.Background()
	firstEvent := automationRunEngineTestEvent("event_00000001", "mid-1", 1)
	secondEvent := automationRunEngineTestEvent("event_00000002", "mid-2", 1)

	first, firstCreated, err := engine.EnsureRun(ctx, firstEvent, "")
	if err != nil || !firstCreated {
		t.Fatalf("first EnsureRun() = %#v, %v, %v", first, firstCreated, err)
	}
	second, secondCreated, err := engine.EnsureRun(ctx, secondEvent, "")
	if err != nil || !secondCreated {
		t.Fatalf("second EnsureRun() = %#v, %v, %v", second, secondCreated, err)
	}
	if first.ID == second.ID || first.SourceMessageKey == second.SourceMessageKey {
		t.Fatalf("different messages reused a run: first=%#v second=%#v", first, second)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Runs) != 2 || len(snapshot.Idempotency) != 2 {
		t.Fatalf("state counts = runs:%d idempotency:%d, want 2/2", len(snapshot.Runs), len(snapshot.Idempotency))
	}
}

func TestAutomationRunEngineRetryRunCreatesRetry(t *testing.T) {
	engine, store := automationRunEngineTestOpen(t)
	source, created, err := engine.EnsureRun(
		context.Background(),
		automationRunEngineTestEvent("event_00000001", "mid-retry", 1),
		"",
	)
	if err != nil || !created {
		t.Fatalf("EnsureRun() = %#v, %v, %v", source, created, err)
	}
	err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		for i := range state.Runs {
			if state.Runs[i].ID != source.ID {
				continue
			}
			startedAt := state.Runs[i].CreatedAt.Add(time.Second)
			completedAt := startedAt.Add(time.Second)
			state.Runs[i].Status = AutomationRunFailed
			state.Runs[i].Version++
			state.Runs[i].FailureStage = "provider"
			state.Runs[i].FailureCode = "provider_failed"
			state.Runs[i].FailureMessage = "synthetic failure"
			state.Runs[i].StartedAt = &startedAt
			state.Runs[i].CompletedAt = &completedAt
			source = state.Runs[i]
			return nil
		}
		return fmt.Errorf("source run %q not found", source.ID)
	})
	if err != nil {
		t.Fatal(err)
	}

	retry, err := engine.RetryRun(context.Background(), source.ID, source.Version, "operator-1", "retry")
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID == source.ID ||
		retry.RetryOfRunID != source.ID ||
		retry.Attempt != source.Attempt+1 ||
		retry.IdempotencyKey != fmt.Sprintf("retry:%s:%d", source.ID, source.Attempt+1) {
		t.Fatalf("RetryRun() = %#v, source = %#v", retry, source)
	}
	snapshot := store.Snapshot()
	if len(snapshot.Runs) != 2 || len(snapshot.Idempotency) != 2 {
		t.Fatalf("retry state counts = runs:%d idempotency:%d, want 2/2", len(snapshot.Runs), len(snapshot.Idempotency))
	}
	if err := ValidateAutomationState(&snapshot); err != nil {
		t.Fatalf("retry state validation error = %v", err)
	}
}

func TestAutomationRunEngineRecoversQueuedRunAfterRouteFreeze(t *testing.T) {
	engine, store, crm, router := automationRunEngineTestOpenRecruiting(t)
	event := automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false)
	event.Mid = "queued-route-recovery"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}

	legacyEngine := NewDefaultAutomationRunEngine(
		store,
		crm,
		noopAutomationProvider{},
		NewServerAutomationRiskEvaluator(),
		nil,
	)
	legacy, created, err := legacyEngine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created || legacy.SourceMessageVersion != persisted.Version {
		t.Fatalf("legacy EnsureRun() = %#v, created=%t, err=%v", legacy, created, err)
	}
	frozen, err := router.Route(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Event.Version != persisted.Version+1 ||
		frozen.Event.RecruitingRouteSourceVersion != persisted.Version {
		t.Fatalf("frozen event = %#v", frozen.Event)
	}

	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	recovered := automationRunEngineTestRun(t, store.Snapshot(), legacy.ID)
	if recovered.Status != AutomationRunSucceeded ||
		recovered.SourceMessageVersion != frozen.Event.Version {
		t.Fatalf("recovered run = %#v, frozen event = %#v", recovered, frozen.Event)
	}
}

func TestAutomationRunEngineQueuedRecoveryFailsClosedOnVersionGap(t *testing.T) {
	engine, store, crm, router := automationRunEngineTestOpenRecruiting(t)
	event := automationRecruitingTestEvent(ChatTypePrivate, "fictional_candidate", false)
	event.Mid = "queued-route-gap"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}

	legacyEngine := NewDefaultAutomationRunEngine(
		store,
		crm,
		noopAutomationProvider{},
		NewServerAutomationRiskEvaluator(),
		nil,
	)
	legacy, created, err := legacyEngine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created {
		t.Fatalf("legacy EnsureRun() = %#v, created=%t, err=%v", legacy, created, err)
	}
	frozen, err := router.Route(persisted)
	if err != nil {
		t.Fatal(err)
	}
	err = crm.Update(func(state *CRMState, _ *IDAllocator) error {
		index := sourceJobEventIndex(state.MessageEvents, persisted.ID)
		state.MessageEvents[index].ReviewStatus = ReviewStatusReviewed
		state.MessageEvents[index].Version++
		state.MessageEvents[index].UpdatedAt = crmPipelineMutationTime(state.MessageEvents[index].UpdatedAt)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := automationRunEngineTestRun(t, store.Snapshot(), legacy.ID)
	if failed.Status != AutomationRunFailed ||
		failed.SourceMessageVersion != persisted.Version ||
		failed.FailureCode != "source_stale" {
		t.Fatalf("version-gap run = %#v, frozen event = %#v", failed, frozen.Event)
	}
}

func TestAutomationRunEngineRetryUsesFrozenEventVersion(t *testing.T) {
	engine, store, crm, router := automationRunEngineTestOpenRecruiting(t)
	event := automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false)
	event.Mid = "failed-route-retry"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}

	legacyEngine := NewDefaultAutomationRunEngine(
		store,
		crm,
		noopAutomationProvider{},
		NewServerAutomationRiskEvaluator(),
		nil,
	)
	source, created, err := legacyEngine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created {
		t.Fatalf("legacy EnsureRun() = %#v, created=%t, err=%v", source, created, err)
	}
	err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationRunIndex(state, source.ID)
		startedAt := state.Runs[index].CreatedAt.Add(time.Second)
		completedAt := startedAt.Add(time.Second)
		state.Runs[index].Status = AutomationRunFailed
		state.Runs[index].Version++
		state.Runs[index].FailureStage = "provider"
		state.Runs[index].FailureCode = "provider_failed"
		state.Runs[index].FailureMessage = "synthetic failure"
		state.Runs[index].StartedAt = &startedAt
		state.Runs[index].CompletedAt = &completedAt
		source = state.Runs[index]
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := router.Route(persisted)
	if err != nil {
		t.Fatal(err)
	}

	retry, err := engine.RetryRun(
		context.Background(),
		source.ID,
		source.Version,
		"operator-1",
		"retry after route migration",
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.SourceMessageVersion != frozen.Event.Version ||
		retry.SourceMessageVersion != source.SourceMessageVersion+1 ||
		retry.RetryOfRunID != source.ID {
		t.Fatalf("retry = %#v, source = %#v, frozen event = %#v", retry, source, frozen.Event)
	}
}

func TestAutomationRunEngineProcessQueuedResumesApprovedActionsWithoutReexecution(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionSucceeded,
		mustAutomationSchedulerTestResult(t, AutomationEntityCandidate, "cand_00000001", 1),
	)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	executor := &automationSchedulerTestExecutor{
		results: map[string]AutomationExecutionResult{
			"child": automationEntityExecutionSuccess(
				AutomationEntityCandidate,
				"cand_00000001",
				2,
			),
		},
	}
	engine := &defaultAutomationRunEngine{
		store:    reopened,
		executor: executor,
	}
	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := executor.Calls(); !automationSchedulerTestStringsEqual(got, []string{"child"}) {
		t.Fatalf("restart execution calls = %v, want [child]", got)
	}
	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := executor.Calls(); !automationSchedulerTestStringsEqual(got, []string{"child"}) {
		t.Fatalf("second recovery re-executed completed actions: %v", got)
	}
	state := reopened.Snapshot()
	if action := automationSchedulerTestAction(t, state, fixture.parentID); action.Status != AutomationActionSucceeded {
		t.Fatalf("parent status after recovery = %s", action.Status)
	}
	if action := automationSchedulerTestAction(t, state, fixture.childID); action.Status != AutomationActionSucceeded {
		t.Fatalf("child status after recovery = %s", action.Status)
	}
}

func TestAutomationRunEngineStartupActionRecoveryLeavesProviderBacklogUntouched(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionSucceeded,
		mustAutomationSchedulerTestResult(t, AutomationEntityCandidate, "cand_00000001", 1),
	)
	queued := automationRunEngineTestAppendBacklogRun(t, store, AutomationRunQueued, time.Time{})
	startedAt := time.Now().UTC().Add(-time.Hour)
	running := automationRunEngineTestAppendBacklogRun(t, store, AutomationRunRunning, startedAt)
	executor := &automationSchedulerTestExecutor{
		results: map[string]AutomationExecutionResult{
			"child": automationEntityExecutionSuccess(
				AutomationEntityCandidate,
				"cand_00000001",
				2,
			),
		},
	}
	engine := &defaultAutomationRunEngine{
		store:    store,
		executor: executor,
	}

	if err := engine.RecoverScheduledActions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := executor.Calls(); !automationSchedulerTestStringsEqual(got, []string{"child"}) {
		t.Fatalf("startup action recovery calls = %v, want [child]", got)
	}
	state := store.Snapshot()
	if got := automationRunEngineTestRun(t, state, queued.ID); got.Status != AutomationRunQueued ||
		got.Version != queued.Version {
		t.Fatalf("queued provider backlog changed = %#v", got)
	}
	if got := automationRunEngineTestRun(t, state, running.ID); got.Status != AutomationRunRunning ||
		got.Version != running.Version ||
		got.StartedAt == nil ||
		!got.StartedAt.Equal(startedAt) {
		t.Fatalf("running provider backlog changed = %#v", got)
	}
	if action := automationSchedulerTestAction(t, state, fixture.childID); action.Status != AutomationActionSucceeded {
		t.Fatalf("approved child status after startup recovery = %s", action.Status)
	}
}

func TestAutomationRunEngineProviderTimeoutFailsRunWithoutRetry(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	crm := automationRecruitingOpenTestStore(t)
	event := automationRecruitingTestEvent(ChatTypePrivate, "timeout_candidate", false)
	event.Mid = "provider-timeout"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}
	provider := &automationRunEngineBlockingProvider{}
	engine := NewDefaultAutomationRunEngine(
		store,
		crm,
		provider,
		NewServerAutomationRiskEvaluator(),
		nil,
	).(*defaultAutomationRunEngine)
	engine.providerTimeout = func() time.Duration { return 25 * time.Millisecond }
	run, created, err := engine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created {
		t.Fatalf("EnsureRun() = %#v, created=%t, err=%v", run, created, err)
	}

	if err := engine.ProcessRun(context.Background(), run.ID); err == nil {
		t.Fatal("ProcessRun() succeeded for a timed out provider")
	}
	state := store.Snapshot()
	failed := automationRunEngineTestRun(t, state, run.ID)
	if failed.Status != AutomationRunFailed ||
		failed.FailureStage != "provider" ||
		failed.FailureCode != "provider_timeout" ||
		failed.StartedAt == nil ||
		failed.CompletedAt == nil {
		t.Fatalf("timed out run = %#v", failed)
	}
	for _, candidate := range state.Runs {
		if candidate.Status == AutomationRunRunning {
			t.Fatalf("running residue after timeout = %#v", candidate)
		}
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
}

func TestAutomationRunEngineProcessQueuedLimitsBatch(t *testing.T) {
	tests := []struct {
		name       string
		runCount   int
		configured int
		wantCalls  int
	}{
		{name: "default", runCount: 11, wantCalls: automationQueuedRunBatchDefault},
		{name: "configured above hard maximum", runCount: 11, configured: 11, wantCalls: 10},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &automationRunEngineRecordingProvider{}
			engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
			engine.queuedBatchLimit = test.configured
			for i := 1; i <= test.runCount; i++ {
				automationRunEngineTestEnsureQueued(t, engine, crm, i)
			}

			if err := engine.ProcessQueued(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := len(provider.Calls()); got != test.wantCalls {
				t.Fatalf("provider calls = %d, want %d", got, test.wantCalls)
			}
			state := store.Snapshot()
			succeeded := 0
			queued := 0
			for _, run := range state.Runs {
				switch run.Status {
				case AutomationRunSucceeded:
					succeeded++
				case AutomationRunQueued:
					queued++
				}
			}
			if succeeded != test.wantCalls || queued != test.runCount-test.wantCalls {
				t.Fatalf(
					"run statuses = succeeded:%d queued:%d, want %d/%d",
					succeeded,
					queued,
					test.wantCalls,
					test.runCount-test.wantCalls,
				)
			}
		})
	}
}

func TestAutomationRunEngineProcessQueuedParentContextProviderFailureStartsCooldown(t *testing.T) {
	tests := []struct {
		name       string
		newContext func() (context.Context, context.CancelFunc)
		cancel     bool
		wantErr    error
	}{
		{
			name: "cancellation",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			cancel:  true,
			wantErr: context.Canceled,
		},
		{
			name: "deadline",
			newContext: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			},
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &automationRunEngineRecordingProvider{
				started: make(chan string, 2),
				release: make(chan struct{}),
			}
			engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
			now := time.Date(2026, time.July, 26, 12, 30, 0, 0, time.UTC)
			engine.queuedClock = func() time.Time { return now }
			failed := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
			queued := automationRunEngineTestEnsureQueued(t, engine, crm, 2)
			provider.blockedRunID = failed.ID
			ctx, cancel := test.newContext()
			defer cancel()

			done := make(chan error, 1)
			go func() {
				done <- engine.ProcessQueued(ctx)
			}()
			select {
			case started := <-provider.started:
				if started != failed.ID {
					t.Fatalf("first provider run = %q, want %q", started, failed.ID)
				}
			case <-time.After(time.Second):
				t.Fatal("queued provider did not start")
			}
			if test.cancel {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("ProcessQueued() error = %v, want %v", err, test.wantErr)
				}
			case <-time.After(time.Second):
				t.Fatal("ProcessQueued() did not return after parent context ended")
			}

			state := store.Snapshot()
			if run := automationRunEngineTestRun(t, state, failed.ID); run.Status != AutomationRunFailed ||
				run.FailureStage != "provider" {
				t.Fatalf("persisted provider failure = %#v", run)
			}
			if run := automationRunEngineTestRun(t, state, queued.ID); run.Status != AutomationRunQueued {
				t.Fatalf("queued run after parent context failure = %#v", run)
			}
			engine.queuedControlMu.Lock()
			cooldownUntil := engine.queuedCooldownUntil
			engine.queuedControlMu.Unlock()
			if want := now.Add(automationQueuedRunCooldownDefault); !cooldownUntil.Equal(want) {
				t.Fatalf("cooldown until = %v, want %v", cooldownUntil, want)
			}

			if err := engine.ProcessQueued(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{failed.ID}) {
				t.Fatalf("provider calls during cooldown = %v, want [%s]", got, failed.ID)
			}

			now = now.Add(automationQueuedRunCooldownDefault)
			if err := engine.ProcessQueued(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{failed.ID, queued.ID}) {
				t.Fatalf("provider calls after cooldown = %v, want [%s %s]", got, failed.ID, queued.ID)
			}
			state = store.Snapshot()
			if run := automationRunEngineTestRun(t, state, queued.ID); run.Status != AutomationRunSucceeded {
				t.Fatalf("queued run after cooldown = %#v", run)
			}
			if len(state.Runs) != 2 {
				t.Fatalf("run count = %d, want 2 without automatic retries", len(state.Runs))
			}
		})
	}
}

func TestAutomationRunEngineProcessQueuedProviderCooldown(t *testing.T) {
	provider := &automationRunEngineRecordingProvider{
		failures: make(map[string]error),
	}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	engine.queuedClock = func() time.Time { return now }
	first := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
	second := automationRunEngineTestEnsureQueued(t, engine, crm, 2)
	provider.failures[first.ID] = fmt.Errorf("provider unavailable")

	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{first.ID}) {
		t.Fatalf("provider calls after failure = %v, want [%s]", got, first.ID)
	}
	state := store.Snapshot()
	if failed := automationRunEngineTestRun(t, state, first.ID); failed.Status != AutomationRunFailed ||
		failed.FailureStage != "provider" {
		t.Fatalf("provider failure = %#v", failed)
	}
	if queued := automationRunEngineTestRun(t, state, second.ID); queued.Status != AutomationRunQueued {
		t.Fatalf("second run after provider failure = %#v", queued)
	}

	now = now.Add(automationQueuedRunCooldownDefault - time.Second)
	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(provider.Calls()); got != 1 {
		t.Fatalf("provider calls during cooldown = %d, want 1", got)
	}

	now = now.Add(time.Second)
	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{first.ID, second.ID}) {
		t.Fatalf("provider calls after cooldown = %v, want [%s %s]", got, first.ID, second.ID)
	}
	state = store.Snapshot()
	if completed := automationRunEngineTestRun(t, state, second.ID); completed.Status != AutomationRunSucceeded {
		t.Fatalf("second run after cooldown = %#v", completed)
	}
	if len(state.Runs) != 2 {
		t.Fatalf("run count = %d, want 2 without automatic retries", len(state.Runs))
	}
	for _, run := range state.Runs {
		if run.RetryOfRunID != "" {
			t.Fatalf("automatic retry created = %#v", run)
		}
	}
}

func TestAutomationRunEngineProcessQueuedAutoRetriesLatestProviderFailureAfterCooldown(t *testing.T) {
	provider := &automationRunEngineRecordingProvider{
		failures: make(map[string]error),
	}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	now := time.Now().UTC()
	engine.queuedClock = func() time.Time { return now }
	engine.queuedProviderCooldown = time.Second
	source := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
	provider.failures[source.ID] = ErrOpenAIAutomationRequestFailed

	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	failed := automationRunEngineTestRun(t, state, source.ID)
	if failed.Status != AutomationRunFailed ||
		failed.FailureStage != "provider" ||
		len(failed.ActionIDs) != 0 ||
		len(state.Runs) != 1 {
		t.Fatalf("first attempt = %#v, runs=%d", failed, len(state.Runs))
	}

	delete(provider.failures, source.ID)
	now = failed.CompletedAt.Add(time.Second)
	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	state = store.Snapshot()
	if len(state.Runs) != 2 {
		t.Fatalf("run count = %d, want 2", len(state.Runs))
	}
	retry := state.Runs[1]
	if retry.Status != AutomationRunSucceeded ||
		retry.Attempt != 2 ||
		retry.RetryOfRunID != source.ID ||
		len(retry.ActionIDs) != 0 {
		t.Fatalf("automatic retry = %#v", retry)
	}
	if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{source.ID, retry.ID}) {
		t.Fatalf("provider calls = %v, want [%s %s]", got, source.ID, retry.ID)
	}
}

func TestAutomationRunEngineAutoRetryRejectsUnsafeStaleAndExhaustedRuns(t *testing.T) {
	now := time.Now().UTC()
	completedAt := now.Add(-time.Minute)
	source := automationRunEngineTestEvent("event_00000001", "mid-auto-retry", 1)
	source.CreatedAt = completedAt.Add(-time.Minute)
	source.UpdatedAt = source.CreatedAt
	source.MessageKey = MessageKeyForEvent(source)
	run := AutomationRun{
		ID:                   "airun_00000001",
		SessionID:            "aisession_00000001",
		SourceMessageEventID: source.ID,
		SourceMessageKey:     source.MessageKey,
		Status:               AutomationRunFailed,
		Attempt:              1,
		FailureStage:         "provider",
		ActionIDs:            []string{},
		CompletedAt:          &completedAt,
	}
	state := AutomationState{
		Sessions: []AutomationSession{{
			ID: run.SessionID, Kind: AutomationSessionKindAutomation,
		}},
	}
	crmState := CRMState{MessageEvents: []MessageEvent{source}}
	if !automationRunCanAutoRetry(state, crmState, run, now, time.Second) {
		t.Fatal("latest side-effect-free provider failure was not retryable")
	}

	withAction := state
	withAction.Actions = []AutomationAction{{
		ID: "aiact_00000001", RunID: run.ID,
	}}
	if automationRunCanAutoRetry(withAction, crmState, run, now, time.Second) {
		t.Fatal("run with an action was retryable")
	}

	newer := source
	newer.ID = "event_00000002"
	newer.Mid = "mid-newer"
	newer.CreatedAt = source.CreatedAt.Add(time.Second)
	newer.UpdatedAt = newer.CreatedAt
	newer.MessageKey = MessageKeyForEvent(newer)
	if automationRunCanAutoRetry(
		state,
		CRMState{MessageEvents: []MessageEvent{source, newer}},
		run,
		now,
		time.Second,
	) {
		t.Fatal("run superseded by a newer inbound message was retryable")
	}

	exhausted := run
	exhausted.Attempt = automationRunAutoRetryLimit + 1
	if automationRunCanAutoRetry(state, crmState, exhausted, now, time.Second) {
		t.Fatal("exhausted run was retryable")
	}

	old := run
	oldCompletedAt := now.Add(-automationRunAutoRetryWindow - time.Second)
	old.CompletedAt = &oldCompletedAt
	if automationRunCanAutoRetry(state, crmState, old, now, time.Second) {
		t.Fatal("stale failed run was retryable")
	}
}

func TestAutomationRunEngineProcessQueuedProviderCooldownBounds(t *testing.T) {
	tests := []struct {
		name       string
		configured time.Duration
		want       time.Duration
	}{
		{
			name:       "above maximum clamps to five minutes",
			configured: automationQueuedRunCooldownMaximum + time.Nanosecond,
			want:       automationQueuedRunCooldownMaximum,
		},
		{
			name:       "non-positive selects default",
			configured: -time.Nanosecond,
			want:       automationQueuedRunCooldownDefault,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider := &automationRunEngineRecordingProvider{
				failures: make(map[string]error),
			}
			engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
			now := time.Date(2026, time.July, 26, 13, 0, 0, 0, time.UTC)
			engine.queuedClock = func() time.Time { return now }
			engine.queuedProviderCooldown = test.configured
			failed := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
			queued := automationRunEngineTestEnsureQueued(t, engine, crm, 2)
			provider.failures[failed.ID] = fmt.Errorf("provider unavailable")

			if err := engine.ProcessQueued(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{failed.ID}) {
				t.Fatalf("provider calls = %v, want [%s]", got, failed.ID)
			}
			state := store.Snapshot()
			if run := automationRunEngineTestRun(t, state, failed.ID); run.Status != AutomationRunFailed ||
				run.FailureStage != "provider" {
				t.Fatalf("provider failure = %#v", run)
			}
			if run := automationRunEngineTestRun(t, state, queued.ID); run.Status != AutomationRunQueued {
				t.Fatalf("queued run after provider failure = %#v", run)
			}
			engine.queuedControlMu.Lock()
			cooldownUntil := engine.queuedCooldownUntil
			engine.queuedControlMu.Unlock()
			if want := now.Add(test.want); !cooldownUntil.Equal(want) {
				t.Fatalf("cooldown until = %v, want %v", cooldownUntil, want)
			}
		})
	}
}

func TestAutomationRunEngineProcessQueuedContinuesAfterValidationFailure(t *testing.T) {
	provider := &automationRunEngineValidationProvider{}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	failed := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
	succeeded := automationRunEngineTestEnsureQueued(t, engine, crm, 2)
	provider.invalidRunID = failed.ID

	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if run := automationRunEngineTestRun(t, state, failed.ID); run.Status != AutomationRunFailed ||
		run.FailureStage != "validation" ||
		run.FailureCode != "provider_response_invalid" {
		t.Fatalf("validation failure = %#v", run)
	}
	if run := automationRunEngineTestRun(t, state, succeeded.ID); run.Status != AutomationRunSucceeded {
		t.Fatalf("run after validation failure = %#v", run)
	}
	if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{failed.ID, succeeded.ID}) {
		t.Fatalf("provider calls = %v, want [%s %s]", got, failed.ID, succeeded.ID)
	}
	engine.queuedControlMu.Lock()
	cooldownUntil := engine.queuedCooldownUntil
	engine.queuedControlMu.Unlock()
	if !cooldownUntil.IsZero() {
		t.Fatalf("validation failure started provider cooldown until %v", cooldownUntil)
	}
}

func TestAutomationRunEngineProcessQueuedContinuesAfterContextFailure(t *testing.T) {
	provider := &automationRunEngineRecordingProvider{}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	missingEvent := automationRunEngineTestEvent("event_00000001", "missing-source", 1)
	failed, created, err := engine.EnsureRun(context.Background(), missingEvent, "")
	if err != nil || !created {
		t.Fatalf("EnsureRun(missing source) = %#v, created=%t, err=%v", failed, created, err)
	}
	succeeded := automationRunEngineTestEnsureQueued(t, engine, crm, 2)

	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	if run := automationRunEngineTestRun(t, state, failed.ID); run.Status != AutomationRunFailed ||
		run.FailureStage != "context" ||
		run.FailureCode != "source_stale" {
		t.Fatalf("context failure = %#v", run)
	}
	if run := automationRunEngineTestRun(t, state, succeeded.ID); run.Status != AutomationRunSucceeded {
		t.Fatalf("run after context failure = %#v", run)
	}
	if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{succeeded.ID}) {
		t.Fatalf("provider calls = %v, want [%s]", got, succeeded.ID)
	}
}

func TestAutomationRunEngineExplicitRunBypassesQueuedControls(t *testing.T) {
	provider := &automationRunEngineRecordingProvider{
		started: make(chan string, 2),
		release: make(chan struct{}),
	}
	engine, store, crm := automationRunEngineTestOpenQueued(t, provider)
	backlog := automationRunEngineTestEnsureQueued(t, engine, crm, 1)
	live := automationRunEngineTestEnsureQueued(t, engine, crm, 2)
	provider.blockedRunID = backlog.ID

	queuedDone := make(chan error, 1)
	go func() {
		queuedDone <- engine.ProcessQueued(context.Background())
	}()
	select {
	case started := <-provider.started:
		if started != backlog.ID {
			t.Fatalf("first provider run = %q, want %q", started, backlog.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("queued batch did not start")
	}

	secondBatchDone := make(chan error, 1)
	go func() {
		secondBatchDone <- engine.ProcessQueued(context.Background())
	}()
	select {
	case err := <-secondBatchDone:
		if err != nil {
			t.Fatalf("concurrent ProcessQueued() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("concurrent ProcessQueued() did not bypass the active batch")
	}

	engine.queuedControlMu.Lock()
	engine.queuedCooldownUntil = time.Now().UTC().Add(time.Minute)
	engine.queuedControlMu.Unlock()
	liveDone := make(chan error, 1)
	go func() {
		liveDone <- engine.ProcessRun(context.Background(), live.ID)
	}()
	select {
	case started := <-provider.started:
		if started != live.ID {
			t.Fatalf("live provider run = %q, want %q", started, live.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit run was blocked by queued controls")
	}
	select {
	case err := <-liveDone:
		if err != nil {
			t.Fatalf("ProcessRun(live) error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("explicit run did not complete while queued batch was active")
	}

	close(provider.release)
	select {
	case err := <-queuedDone:
		if err != nil {
			t.Fatalf("ProcessQueued() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued batch did not finish")
	}
	state := store.Snapshot()
	if run := automationRunEngineTestRun(t, state, backlog.ID); run.Status != AutomationRunSucceeded {
		t.Fatalf("backlog run = %#v", run)
	}
	if run := automationRunEngineTestRun(t, state, live.ID); run.Status != AutomationRunSucceeded {
		t.Fatalf("live run = %#v", run)
	}
	if got := provider.Calls(); !automationSchedulerTestStringsEqual(got, []string{backlog.ID, live.ID}) {
		t.Fatalf("provider calls = %v, want [%s %s]", got, backlog.ID, live.ID)
	}
}

func mustAutomationSchedulerTestResult(
	t *testing.T,
	entityType string,
	entityID string,
	version uint64,
) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(AutomationActionResult{
		Entity: &AutomationEntityResult{
			EntityType: entityType,
			EntityID:   entityID,
			Version:    version,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func automationRunEngineTestOpen(t *testing.T) (AutomationRunEngine, *AutomationStore) {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return automationRunEngineTestNewEngine(t, store), store
}

func automationRunEngineTestOpenRecruiting(
	t *testing.T,
) (AutomationRunEngine, *AutomationStore, *CRMStore, *RecruitingAutomationRouter) {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	crm := automationRecruitingOpenTestStore(t)
	automationRecruitingConfigureProject(t, crm)
	router := NewRecruitingAutomationRouter(crm)
	engine := NewDefaultAutomationRunEngine(
		store,
		crm,
		noopAutomationProvider{},
		NewServerAutomationRiskEvaluator(),
		nil,
	)
	return newRecruitingAutomationRunEngine(router, engine), store, crm, router
}

func automationRunEngineTestRun(t *testing.T, state AutomationState, id string) AutomationRun {
	t.Helper()
	index := automationRunIndex(&state, id)
	if index < 0 {
		t.Fatalf("automation run %q not found", id)
	}
	return state.Runs[index]
}

func automationRunEngineTestAppendBacklogRun(
	t *testing.T,
	store *AutomationStore,
	status AutomationRunStatus,
	startedAt time.Time,
) AutomationRun {
	t.Helper()
	var run AutomationRun
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		createdAt := now
		if status == AutomationRunRunning {
			createdAt = startedAt.Add(-time.Second)
		}
		key := "backlog:" + id
		run = AutomationRun{
			ID: id, IdempotencyKey: key, EngineVersion: AutomationEngineVersion,
			ProviderName: "backlog-provider", SourceMessageEventID: "event_" + id,
			SourceMessageKey: "message:" + id, SourceMessageVersion: 1,
			Status: status, Version: 1, Attempt: 1,
			ActionIDs: []string{}, CreatedAt: createdAt,
		}
		if status == AutomationRunRunning {
			run.StartedAt = &startedAt
			run.Version = 2
		}
		state.Runs = append(state.Runs, run)
		state.Idempotency[key] = AutomationIdempotencyRecord{
			Kind: "run", ResourceID: id, CreatedAt: createdAt,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

type automationRunEngineBlockingProvider struct {
	calls atomic.Int32
}

func (p *automationRunEngineBlockingProvider) Name() string {
	return "blocking"
}

func (p *automationRunEngineBlockingProvider) Generate(
	ctx context.Context,
	_ AutomationProviderInput,
) ([]byte, error) {
	p.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *automationRunEngineBlockingProvider) Stream(
	context.Context,
	AutomationProviderInput,
) (AutomationProviderStream, error) {
	return nil, nil
}

func automationRunEngineTestNewEngine(t *testing.T, store *AutomationStore) AutomationRunEngine {
	t.Helper()
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	return NewDefaultAutomationRunEngine(
		store,
		crm,
		noopAutomationProvider{},
		NewServerAutomationRiskEvaluator(),
		nil,
	)
}

func automationRunEngineTestOpenQueued(
	t *testing.T,
	provider AutomationProvider,
) (*defaultAutomationRunEngine, *AutomationStore, *CRMStore) {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	crm := automationRecruitingOpenTestStore(t)
	engine := NewDefaultAutomationRunEngine(
		store,
		crm,
		provider,
		NewServerAutomationRiskEvaluator(),
		nil,
	).(*defaultAutomationRunEngine)
	return engine, store, crm
}

func TestAutomationRunEngineBindsPromptRevisionAtRunCreation(t *testing.T) {
	engine, _, crm := automationRunEngineTestOpenQueued(t, &automationRunEngineRecordingProvider{})
	engine.activePromptRevisionID = func() string { return "prompt_00000007" }

	run := automationRunEngineTestEnsureQueued(t, engine, crm, 7)
	if run.PromptRevisionID != "prompt_00000007" {
		t.Fatalf("prompt revision = %q", run.PromptRevisionID)
	}
}

func automationRunEngineTestEnsureQueued(
	t *testing.T,
	engine *defaultAutomationRunEngine,
	crm *CRMStore,
	index int,
) AutomationRun {
	t.Helper()
	event := automationRunEngineTestEvent(
		fmt.Sprintf("event_%08d", index),
		fmt.Sprintf("queued-mid-%d", index),
		1,
	)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event %d = %#v, created=%t, err=%v", index, persisted, created, err)
	}
	run, created, err := engine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created {
		t.Fatalf("EnsureRun(%d) = %#v, created=%t, err=%v", index, run, created, err)
	}
	return run
}

type automationRunEngineRecordingProvider struct {
	mu           sync.Mutex
	calls        []string
	failures     map[string]error
	blockedRunID string
	started      chan string
	release      chan struct{}
	delay        time.Duration

	deadlineRemaining time.Duration
}

type automationRunEngineValidationProvider struct {
	automationRunEngineRecordingProvider
	invalidRunID string
}

func (p *automationRunEngineValidationProvider) GenerateOutput(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	output, err := p.automationRunEngineRecordingProvider.GenerateOutput(ctx, input)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	if input.RunID == p.invalidRunID {
		output.ContinuationState = json.RawMessage("{")
	}
	return output, nil
}

func (p *automationRunEngineRecordingProvider) Name() string {
	return "recording"
}

func (p *automationRunEngineRecordingProvider) Generate(
	ctx context.Context,
	input AutomationProviderInput,
) ([]byte, error) {
	output, err := p.GenerateOutput(ctx, input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(AutomationToolEnvelope{
		SchemaVersion:    AutomationToolEnvelopeVersion,
		AssistantMessage: output.AssistantText,
		Actions:          output.ToolCalls,
	})
}

func (p *automationRunEngineRecordingProvider) GenerateOutput(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	p.mu.Lock()
	p.calls = append(p.calls, input.RunID)
	failure := p.failures[input.RunID]
	blocked := input.RunID == p.blockedRunID
	started := p.started
	release := p.release
	delay := p.delay
	if deadline, ok := ctx.Deadline(); ok {
		p.deadlineRemaining = time.Until(deadline)
	}
	p.mu.Unlock()
	if started != nil {
		started <- input.RunID
	}
	if blocked && release != nil {
		select {
		case <-ctx.Done():
			return AutomationProviderOutput{}, ctx.Err()
		case <-release:
		}
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return AutomationProviderOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
	if failure != nil {
		return AutomationProviderOutput{}, failure
	}
	return AutomationProviderOutput{ToolCalls: []AutomationToolCall{}}, nil
}

func (p *automationRunEngineRecordingProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderStream, error) {
	output, err := p.GenerateOutput(ctx, input)
	if err != nil {
		return nil, err
	}
	return &singleAutomationProviderStream{output: output}, nil
}

func (p *automationRunEngineRecordingProvider) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func automationRunEngineTestEvent(id string, mid string, version uint64) MessageEvent {
	createdAt := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	event := MessageEvent{
		ID:           id,
		Source:       "wechat",
		Mid:          mid,
		ChatType:     ChatTypePrivate,
		PeerID:       "wxid_peer",
		AuthorID:     "wxid_author",
		Text:         "hello",
		Kinds:        []string{"text"},
		Timestamp:    1_753_275_600,
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      version,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt.Add(time.Duration(version-1) * time.Minute),
	}
	event.MessageKey = MessageKeyForEvent(event)
	return event
}
