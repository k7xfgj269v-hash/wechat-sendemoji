package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var automationStoreTestTime = time.Date(2026, time.July, 21, 6, 0, 0, 123, time.UTC)

func TestAutomationStoreDefaultStateAndPermissions(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	root := t.TempDir()
	parent := filepath.Join(root, "automation")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "state.json")

	store, err := OpenAutomationStore(parent)
	if err != nil {
		t.Fatalf("OpenAutomationStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	state := store.Snapshot()
	if state.SchemaVersion != AutomationSnapshotSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", state.SchemaVersion, AutomationSnapshotSchemaVersion)
	}
	if state.Policy.Version != 1 || state.Policy.Mode != AutomationModeManual {
		t.Fatalf("default policy = %#v", state.Policy)
	}
	assertAutomationCollectionsNonNil(t, state)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted snapshot: %v", err)
	}
	for _, field := range []string{
		"sessions", "turns", "turn_events", "runs", "actions", "results", "approvals", "outbox", "pending_audit",
	} {
		value, ok := persisted[field].([]any)
		if !ok || len(value) != 0 {
			t.Errorf("persisted %s = %#v, want []", field, persisted[field])
		}
	}
	if value, ok := persisted["idempotency"].(map[string]any); !ok || len(value) != 0 {
		t.Errorf("persisted idempotency = %#v, want {}", persisted["idempotency"])
	}
	assertAutomationMode(t, parent, 0o700)
	assertAutomationMode(t, path, 0o600)
}

func TestAutomationStoreWritesCompactJSON(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	got, err := os.ReadFile(filepath.Join(dataDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	if !bytes.Equal(got, want) {
		t.Fatalf("snapshot is not compact canonical JSON:\ngot  %q\nwant %q", got, want)
	}
	if bytes.Count(got, []byte{'\n'}) != 1 || got[len(got)-1] != '\n' {
		t.Fatalf("snapshot newline framing = %q, want one trailing newline", got)
	}
}

func TestAutomationStoreMigratesSchemaTwoSnapshot(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	legacy := newAutomationStateAt(automationStoreTestTime)
	legacy.SchemaVersion = 2
	automationStoreWriteFile(t, path, automationStoreMarshalState(t, legacy))

	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if got := store.Snapshot().SchemaVersion; got != AutomationSnapshotSchemaVersion {
		t.Fatalf("migrated schema version = %d", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.SchemaVersion != AutomationSnapshotSchemaVersion {
		t.Fatalf("persisted schema version = %d", persisted.SchemaVersion)
	}
}

func TestAutomationStoreRetentionConfiguration(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		got, err := readAutomationRetentionLimits()
		if err != nil {
			t.Fatal(err)
		}
		want := automationRetentionLimits{
			Sessions:     automationMaxEntityCount,
			Turns:        automationMaxEntityCount,
			Runs:         automationMaxEntityCount,
			Idempotency:  automationMaxEntityCount * 2,
			PendingAudit: automationMaxAuditCount,
		}
		if got != want {
			t.Fatalf("retention defaults = %#v, want %#v", got, want)
		}

		state := automationStoreTestTerminalHistory(3)
		before := cloneAutomationState(state)
		if changed := applyAutomationRetention(&state, got); changed {
			t.Fatal("default retention pruned valid history")
		}
		assertAutomationStatesEqual(t, state, before)
	})

	t.Run("valid bounds", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 0, 1, automationMaxEntityCount, 2, automationMaxAuditCount)
		got, err := readAutomationRetentionLimits()
		if err != nil {
			t.Fatal(err)
		}
		want := automationRetentionLimits{
			Sessions:     0,
			Turns:        1,
			Runs:         automationMaxEntityCount,
			Idempotency:  2,
			PendingAudit: automationMaxAuditCount,
		}
		if got != want {
			t.Fatalf("retention limits = %#v, want %#v", got, want)
		}
	})

	tests := []struct {
		name  string
		env   string
		value string
	}{
		{name: "empty", env: automationRetentionSessionsEnv, value: ""},
		{name: "negative", env: automationRetentionTurnsEnv, value: "-1"},
		{name: "non-numeric", env: automationRetentionRunsEnv, value: "many"},
		{
			name:  "sessions oversized",
			env:   automationRetentionSessionsEnv,
			value: fmt.Sprint(automationMaxEntityCount + 1),
		},
		{
			name:  "idempotency oversized",
			env:   automationRetentionIdempotencyEnv,
			value: fmt.Sprint(automationMaxEntityCount*2 + 1),
		},
		{
			name:  "pending audit oversized",
			env:   automationRetentionPendingAuditEnv,
			value: fmt.Sprint(automationMaxAuditCount + 1),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationStoreUnsetRetentionEnvironment(t)
			t.Setenv(test.env, test.value)
			dataDir := filepath.Join(t.TempDir(), "must-not-exist")
			if store, err := OpenAutomationStore(dataDir); !errors.Is(err, ErrAutomationValidation) {
				if store != nil {
					_ = store.Close()
				}
				t.Fatalf("OpenAutomationStore() error = %v, want validation error", err)
			}
			if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("data directory stat error = %v, want not exist", err)
			}
		})
	}

	t.Run("invalid config preserves existing snapshot", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		t.Setenv(automationRetentionRunsEnv, "invalid")
		dataDir := t.TempDir()
		path := filepath.Join(dataDir, "state.json")
		want := []byte("existing snapshot bytes")
		if err := os.WriteFile(path, want, 0o644); err != nil {
			t.Fatal(err)
		}
		if store, err := OpenAutomationStore(dataDir); !errors.Is(err, ErrAutomationValidation) {
			if store != nil {
				_ = store.Close()
			}
			t.Fatalf("OpenAutomationStore() error = %v, want validation error", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("existing snapshot bytes = %q, want %q", got, want)
		}
		assertAutomationMode(t, path, 0o644)
	})
}

func TestAutomationRetentionKeepsChatCheckpointAnchor(t *testing.T) {
	now := automationStoreTestTime
	state := newAutomationStateAt(now)
	state.Counters.Session = 1
	state.Counters.Turn = 3
	state.Sessions = append(state.Sessions, AutomationSession{
		ID: "aisession_00000001", Kind: AutomationSessionKindChat,
		Title: "checkpoint retention", Status: AutomationSessionStatusActive,
		ContextCheckpoint: &AutomationChatCheckpoint{
			SchemaVersion:   AutomationChatCheckpointVersion,
			ThroughTurnID:   "aiturn_00000001",
			SourceTurnCount: 1,
			Summary:         automationChatTestContextSummary(),
			CreatedAt:       now.Add(3 * time.Second),
			UpdatedAt:       now.Add(3 * time.Second),
		},
		Version: 1, CreatedAt: now, UpdatedAt: now.Add(3 * time.Second),
	})
	for index := 1; index <= 3; index++ {
		timestamp := now.Add(time.Duration(index) * time.Second)
		state.Turns = append(state.Turns, AutomationTurn{
			ID:        fmt.Sprintf("aiturn_%08d", index),
			SessionID: "aisession_00000001", Status: AutomationTurnCompleted,
			ProviderName: "test", Version: 1,
			CreatedAt: timestamp, UpdatedAt: timestamp,
			StartedAt: automationTimePointer(timestamp), CompletedAt: automationTimePointer(timestamp),
		})
	}

	changed := applyAutomationRetention(&state, automationRetentionLimits{
		Sessions: 1, Turns: 1, Runs: 0, Idempotency: 0, PendingAudit: 0,
	})
	if !changed {
		t.Fatal("retention did not prune terminal turns")
	}
	assertAutomationRetentionIDs(
		t,
		state.Turns,
		func(turn AutomationTurn) string { return turn.ID },
		"aiturn_00000001",
	)
	if err := validateAutomationStateContents(&state, true); err != nil {
		t.Fatalf("retained checkpoint state: %v", err)
	}
}

func TestAutomationStoreDefaultRetentionRejectsHardMaximumPlusOne(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	automationStoreWriteFile(
		t,
		path,
		automationStoreMarshalState(t, automationStoreTestStandaloneSessions(automationMaxEntityCount)),
	)
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	beforeState := store.Snapshot()
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeCounters := beforeState.Counters
	beforeOldestID := beforeState.Sessions[0].ID

	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindSession)
		if err != nil {
			return err
		}
		session := automationStoreTestStandaloneSession(automationMaxEntityCount + 1)
		if id != session.ID {
			return fmt.Errorf("allocated session ID %q, want %q", id, session.ID)
		}
		state.Sessions = append(state.Sessions, session)
		return nil
	})
	var validation *AutomationValidationError
	if !errors.As(err, &validation) ||
		validation.Field != "sessions" ||
		validation.Message != "entity limit exceeded" {
		t.Fatalf("Update() error = %v, want sessions hard-limit validation error", err)
	}

	gotState := store.Snapshot()
	if !reflect.DeepEqual(gotState, beforeState) {
		t.Fatal("memory changed after hard-limit rejection")
	}
	if gotState.Counters != beforeCounters {
		t.Fatalf("counters changed after hard-limit rejection: got %#v, want %#v", gotState.Counters, beforeCounters)
	}
	if gotState.Sessions[0].ID != beforeOldestID {
		t.Fatalf("oldest session = %q, want %q", gotState.Sessions[0].ID, beforeOldestID)
	}
	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, beforeBytes) {
		t.Fatal("persisted snapshot changed after hard-limit rejection")
	}
	assertNoAutomationAtomicArtifacts(t, dataDir, filepath.Base(path))
}

func TestAutomationStoreExplicitLowerRetentionStillPrunes(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	automationStoreSetRetentionEnvironment(
		t,
		1,
		automationMaxEntityCount,
		automationMaxEntityCount,
		automationMaxEntityCount*2,
		automationMaxAuditCount,
	)
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if err := store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		*state = automationStoreTestStandaloneSessions(3)
		return nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got := store.Snapshot()
	assertAutomationRetentionIDs(
		t,
		got.Sessions,
		func(value AutomationSession) string { return value.ID },
		"aisession_00000003",
	)
	if got.Counters.Session != 3 {
		t.Fatalf("session counter = %d, want high-water mark 3", got.Counters.Session)
	}
	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantBytes := automationStoreMarshalState(t, got)
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatal("explicit lower retention did not persist compact canonical state")
	}
	assertNoAutomationAtomicArtifacts(t, dataDir, filepath.Base(path))
}

func TestAutomationStoreOpenRejectsHardMaximumPlusOneBeforeRetention(t *testing.T) {
	oversizedBytes := automationStoreMarshalState(
		t,
		automationStoreTestStandaloneSessions(automationMaxEntityCount+1),
	)
	tests := []struct {
		name        string
		lowerWindow bool
	}{
		{name: "defaults"},
		{name: "explicit lower window", lowerWindow: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			automationStoreUnsetRetentionEnvironment(t)
			if test.lowerWindow {
				automationStoreSetRetentionEnvironment(
					t,
					1,
					automationMaxEntityCount,
					automationMaxEntityCount,
					automationMaxEntityCount*2,
					automationMaxAuditCount,
				)
			}
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, "state.json")
			automationStoreWriteFile(t, path, oversizedBytes)

			store, err := OpenAutomationStore(dataDir)
			if store != nil {
				_ = store.Close()
			}
			var validation *AutomationValidationError
			if !errors.As(err, &validation) ||
				validation.Field != "sessions" ||
				validation.Message != "entity limit exceeded" {
				t.Fatalf("OpenAutomationStore() error = %v, want sessions hard-limit validation error", err)
			}
			gotBytes, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(gotBytes, oversizedBytes) {
				t.Fatal("OpenAutomationStore changed an oversized snapshot before rejecting it")
			}
			assertAutomationMode(t, path, 0o600)
			assertNoAutomationAtomicArtifacts(t, dataDir, filepath.Base(path))
		})
	}
}

func TestAutomationStoreOpenAppliesExplicitLowerWindowIdempotently(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	automationStoreSetRetentionEnvironment(
		t,
		1,
		automationMaxEntityCount,
		automationMaxEntityCount,
		automationMaxEntityCount*2,
		automationMaxAuditCount,
	)
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	automationStoreWriteFile(
		t,
		path,
		automationStoreMarshalState(t, automationStoreTestStandaloneSessions(3)),
	)

	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	wantState := store.Snapshot()
	assertAutomationRetentionIDs(
		t,
		wantState.Sessions,
		func(value AutomationSession) string { return value.ID },
		"aisession_00000003",
	)
	if wantState.Counters.Session != 3 {
		t.Fatalf("session counter = %d, want high-water mark 3", wantState.Counters.Session)
	}
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wantBytes, automationStoreMarshalState(t, wantState)) {
		t.Fatal("first open did not persist compact canonical pruned state")
	}
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
	if got := reopened.Snapshot(); !reflect.DeepEqual(got, wantState) {
		t.Fatal("reopen changed an already-pruned state")
	}
	gotBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes, wantBytes) {
		t.Fatal("reopen rewrote an already-pruned snapshot")
	}
	assertNoAutomationAtomicArtifacts(t, dataDir, filepath.Base(path))
}

func TestAutomationStoreOpenRepairsRunTraceStatuses(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		runID = id
		state.Runs = append(state.Runs, AutomationRun{
			ID: id, IdempotencyKey: "run:trace-repair",
			EngineVersion: AutomationEngineVersion, ProviderName: "test-provider",
			SourceMessageEventID: "event_00000001", SourceMessageKey: "trace-repair",
			SourceMessageVersion: 1, Status: AutomationRunQueued, Version: 1, Attempt: 1,
			ActionIDs: []string{}, CreatedAt: automationStoreTestTime,
		})
		state.Idempotency["run:trace-repair"] = AutomationIdempotencyRecord{
			Kind: "run", ResourceID: id, CreatedAt: automationStoreTestTime,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	runIndex := automationRunIndex(&state, runID)
	if runIndex < 0 {
		t.Fatal("run was not persisted")
	}
	run := &state.Runs[runIndex]
	turn := automationTurnByID(&state, run.TurnID)
	session := automationSessionByID(&state, run.SessionID)
	if turn == nil || session == nil {
		t.Fatal("run trace was not created")
	}
	startedAt := automationStoreTestTime.Add(time.Minute)
	completedAt := startedAt.Add(time.Minute)
	run.Status = AutomationRunSucceeded
	run.Version++
	run.StartedAt = automationTimePointer(startedAt)
	run.CompletedAt = automationTimePointer(completedAt)
	if err := writeAutomationSnapshotAtomic(
		filepath.Join(dataDir, "state.json"),
		state,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repaired := reopened.Snapshot()
	repairedRun := automationRunEngineTestRun(t, repaired, runID)
	repairedTurn := automationTurnByID(&repaired, repairedRun.TurnID)
	repairedSession := automationSessionByID(&repaired, repairedRun.SessionID)
	if repairedTurn == nil || repairedSession == nil {
		t.Fatal("repaired run trace is missing")
	}
	if repairedTurn.Status != AutomationTurnCompleted ||
		repairedTurn.StartedAt == nil || !repairedTurn.StartedAt.Equal(startedAt) ||
		repairedTurn.CompletedAt == nil || !repairedTurn.CompletedAt.Equal(completedAt) ||
		!repairedTurn.UpdatedAt.Equal(completedAt) {
		t.Fatalf("repaired turn = %#v", repairedTurn)
	}
	if repairedSession.Status != AutomationSessionStatusCompleted ||
		!repairedSession.UpdatedAt.Equal(completedAt) {
		t.Fatalf("repaired session = %#v", repairedSession)
	}
}

func TestAutomationStoreRetentionPrunesTerminalAggregates(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	automationStoreSetRetentionEnvironment(t, 1, 1, 1, 2, 0)
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	automationStoreSetRetentionEnvironment(
		t,
		automationMaxEntityCount,
		automationMaxEntityCount,
		automationMaxEntityCount,
		automationMaxEntityCount*2,
		automationMaxAuditCount,
	)
	err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		*state = automationStoreTestTerminalHistory(3)
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	state := store.Snapshot()
	assertAutomationRetentionIDs(t, state.Sessions, func(value AutomationSession) string { return value.ID }, "aisession_00000003")
	assertAutomationRetentionIDs(t, state.Turns, func(value AutomationTurn) string { return value.ID }, "aiturn_00000003")
	assertAutomationRetentionIDs(t, state.Runs, func(value AutomationRun) string { return value.ID }, "airun_00000003")
	assertAutomationRetentionIDs(t, state.Actions, func(value AutomationAction) string { return value.ID }, "aiact_00000003")
	assertAutomationRetentionIDs(t, state.Results, func(value AutomationResult) string { return value.ID }, "airesult_00000003")
	assertAutomationRetentionIDs(
		t,
		state.PermissionRequests,
		func(value AutomationPermissionRequest) string { return value.ID },
		"aipermreq_00000003",
	)
	assertAutomationRetentionIDs(t, state.Outbox, func(value AutomationOutboxEntry) string { return value.ID }, "aiout_00000003")
	if len(state.TurnEvents) != 1 || state.TurnEvents[0].Sequence != 3 {
		t.Fatalf("turn events = %#v, want sequence 3", state.TurnEvents)
	}
	if len(state.Idempotency) != 2 {
		t.Fatalf("idempotency count = %d, want 2", len(state.Idempotency))
	}
	if state.Counters.Session != 3 || state.Counters.Turn != 3 || state.Counters.Run != 3 ||
		state.Counters.Action != 3 || state.Counters.Result != 3 || state.Counters.Outbox != 3 {
		t.Fatalf("counters regressed after pruning: %#v", state.Counters)
	}
	if state.SchemaVersion != AutomationSnapshotSchemaVersion || state.LastObservedCRMEventCounter != 77 {
		t.Fatalf("disk fields changed after pruning: schema=%d watermark=%d", state.SchemaVersion, state.LastObservedCRMEventCounter)
	}
	assertAutomationCollectionsNonNil(t, state)
	if err := ValidateAutomationState(&state); err != nil {
		t.Fatalf("retained state validation error = %v", err)
	}
}

func TestAutomationStoreRetentionPreservesProtectedClosure(t *testing.T) {
	t.Run("pending audit", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 2, 2, 2, 2, 0)
		state := automationStoreTestTerminalHistory(3)
		state.Counters.Audit = 1
		state.PendingAudit = []AutomationAuditRecord{automationStoreTestAudit("aiaudit_00000001", automationStoreTestTime)}
		state.PendingAudit[0].RunID = "airun_00000001"
		state.PendingAudit[0].ActionID = "aiact_00000001"
		state.PendingAudit[0].OutboxID = "aiout_00000001"
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("fixture validation error = %v", err)
		}

		dataDir := t.TempDir()
		automationStoreWriteFile(t, filepath.Join(dataDir, "state.json"), automationStoreMarshalState(t, state))
		store, err := OpenAutomationStore(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		got := store.Snapshot()
		assertAutomationRetentionIDs(
			t,
			got.Sessions,
			func(value AutomationSession) string { return value.ID },
			"aisession_00000001",
			"aisession_00000003",
		)
		assertAutomationRetentionIDs(
			t,
			got.Runs,
			func(value AutomationRun) string { return value.ID },
			"airun_00000001",
			"airun_00000003",
		)
		assertAutomationRetentionIDs(
			t,
			got.Outbox,
			func(value AutomationOutboxEntry) string { return value.ID },
			"aiout_00000001",
			"aiout_00000003",
		)
		if len(got.PendingAudit) != 1 || got.PendingAudit[0].ID != "aiaudit_00000001" {
			t.Fatalf("pending audit = %#v, want uncommitted record preserved", got.PendingAudit)
		}
		if got.Counters.Audit != 1 || len(got.Idempotency) != 4 {
			t.Fatalf("protected counters/idempotency = %#v/%d", got.Counters, len(got.Idempotency))
		}
		assertAutomationCollectionsNonNil(t, got)
	})

	t.Run("active pending and in-flight", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 0, 0, 0, 0, 0)
		state := automationStoreTestTerminalHistory(3)

		state.Sessions[0].Status = AutomationSessionStatusActive
		state.Turns[0].Status = AutomationTurnRunning
		state.Turns[0].CompletedAt = nil
		state.Runs[0].Status = AutomationRunRunning
		state.Runs[0].CompletedAt = nil

		state.PermissionRequests[1].Status = AutomationPermissionStatusPending
		state.PermissionRequests[1].Decision = ""
		state.PermissionRequests[1].OperatorID = ""
		state.PermissionRequests[1].Reason = ""

		state.Outbox[2].Status = AutomationOutboxPending
		state.Outbox[2].Attempt = 0
		state.Outbox[2].DispatchAttempted = false
		state.Outbox[2].UpdatedAt = state.Outbox[2].CreatedAt
		state.Outbox[2].DispatchStartedAt = nil
		state.Outbox[2].CompletedAt = nil
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("fixture validation error = %v", err)
		}

		dataDir := t.TempDir()
		automationStoreWriteFile(t, filepath.Join(dataDir, "state.json"), automationStoreMarshalState(t, state))
		store, err := OpenAutomationStore(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		got := store.Snapshot()
		assertAutomationRetentionIDs(
			t,
			got.Runs,
			func(value AutomationRun) string { return value.ID },
			"airun_00000001",
			"airun_00000002",
			"airun_00000003",
		)
		assertAutomationRetentionIDs(
			t,
			got.Outbox,
			func(value AutomationOutboxEntry) string { return value.ID },
			"aiout_00000001",
			"aiout_00000002",
			"aiout_00000003",
		)
		if len(got.Idempotency) != 6 {
			t.Fatalf("protected idempotency count = %d, want 6", len(got.Idempotency))
		}
		assertAutomationCollectionsNonNil(t, got)
		if err := ValidateAutomationState(&got); err != nil {
			t.Fatalf("retained state validation error = %v", err)
		}
	})

	t.Run("retry predecessor", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 1, 1, 1, 4, 0)
		state := automationStoreTestTerminalHistory(3)
		state.Runs[2].RetryOfRunID = state.Runs[1].ID
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("fixture validation error = %v", err)
		}

		dataDir := t.TempDir()
		automationStoreWriteFile(t, filepath.Join(dataDir, "state.json"), automationStoreMarshalState(t, state))
		store, err := OpenAutomationStore(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		got := store.Snapshot()
		assertAutomationRetentionIDs(
			t,
			got.Sessions,
			func(value AutomationSession) string { return value.ID },
			"aisession_00000002",
			"aisession_00000003",
		)
		assertAutomationRetentionIDs(
			t,
			got.Turns,
			func(value AutomationTurn) string { return value.ID },
			"aiturn_00000002",
			"aiturn_00000003",
		)
		assertAutomationRetentionIDs(
			t,
			got.Runs,
			func(value AutomationRun) string { return value.ID },
			"airun_00000002",
			"airun_00000003",
		)
		if err := ValidateAutomationState(&got); err != nil {
			t.Fatalf("retained state validation error = %v", err)
		}
	})
}

func TestAutomationStoreRetentionPreservesTerminalOutboxIdempotencyAfterAuditFlush(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	automationStoreSetRetentionEnvironment(t, 3, 3, 3, 0, 0)
	state := automationStoreTestTerminalHistory(3)
	state.Outbox[1].Status = AutomationOutboxFailed
	state.Outbox[1].DispatchAttempted = false
	state.Outbox[1].DispatchStartedAt = nil
	state.Outbox[2].Status = AutomationOutboxUnknown
	state.Counters.Audit = 3
	state.PendingAudit = []AutomationAuditRecord{
		automationStoreTestAudit("aiaudit_00000001", state.Outbox[0].UpdatedAt),
		automationStoreTestAudit("aiaudit_00000002", state.Outbox[1].UpdatedAt),
		automationStoreTestAudit("aiaudit_00000003", state.Outbox[2].UpdatedAt),
	}
	for i := range state.PendingAudit {
		state.PendingAudit[i].RunID = state.Runs[i].ID
		state.PendingAudit[i].ActionID = state.Actions[i].ID
		state.PendingAudit[i].OutboxID = state.Outbox[i].ID
	}
	if err := ValidateAutomationState(&state); err != nil {
		t.Fatalf("fixture validation error = %v", err)
	}

	dataDir := t.TempDir()
	automationStoreWriteFile(t, filepath.Join(dataDir, "state.json"), automationStoreMarshalState(t, state))
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending() error = %v", err)
	}
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}

	for index, name := range []string{"sent", "failed", "unknown"} {
		for attempt := 0; attempt < 2; attempt++ {
			pending, created, err := store.EnsurePending(
				context.Background(),
				state.Actions[index].ID,
				state.Actions[index].Recipient.PeerID,
				"changed "+name+" pending text",
			)
			if err != nil ||
				created ||
				pending.ID != state.Outbox[index].ID ||
				pending.Status != state.Outbox[index].Status {
				t.Fatalf("EnsurePending(%s) attempt %d = %#v, %v, %v", name, attempt, pending, created, err)
			}
			text, created, err := store.EnsureText(
				context.Background(),
				state.Actions[index],
				state.Actions[index].Recipient.ChatType,
				state.Actions[index].Recipient.PeerID,
				"changed "+name+" text",
			)
			if err != nil ||
				created ||
				text.ID != state.Outbox[index].ID ||
				text.Status != state.Outbox[index].Status {
				t.Fatalf("EnsureText(%s) attempt %d = %#v, %v, %v", name, attempt, text, created, err)
			}
		}
	}
}

func TestAutomationStoreRetentionAppliesIndependentIdempotencyWindow(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	automationStoreSetRetentionEnvironment(t, 1, 1, 1, 4, 0)
	state := automationStoreTestTerminalHistory(3)
	event := automationStoreTestRunEvent(2)
	run := &state.Runs[1]
	action := &state.Actions[1]
	oldKey := run.IdempotencyKey
	run.IdempotencyKey = fmt.Sprintf("run:%s:%s", AutomationEngineVersion, event.MessageKey)
	run.SourceMessageEventID = event.ID
	run.SourceMessageKey = event.MessageKey
	run.SourceMessageVersion = event.Version
	action.SourceMessage.EventID = event.ID
	action.SourceMessage.MessageKey = event.MessageKey
	action.SourceMessage.ExpectedVersion = event.Version
	record := state.Idempotency[oldKey]
	delete(state.Idempotency, oldKey)
	state.Idempotency[run.IdempotencyKey] = record
	if err := ValidateAutomationState(&state); err != nil {
		t.Fatalf("fixture validation error = %v", err)
	}

	dataDir := t.TempDir()
	automationStoreWriteFile(t, filepath.Join(dataDir, "state.json"), automationStoreMarshalState(t, state))
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	got := store.Snapshot()
	assertAutomationRetentionIDs(
		t,
		got.Runs,
		func(value AutomationRun) string { return value.ID },
		"airun_00000002",
		"airun_00000003",
	)
	assertAutomationRetentionIDs(
		t,
		got.Outbox,
		func(value AutomationOutboxEntry) string { return value.ID },
		"aiout_00000002",
		"aiout_00000003",
	)
	if len(got.Idempotency) != 4 {
		t.Fatalf("idempotency count = %d, want 4", len(got.Idempotency))
	}

	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	engine := NewDefaultAutomationRunEngine(
		store,
		crm,
		noopAutomationProvider{},
		NewServerAutomationRiskEvaluator(),
		nil,
	)
	reused, created, err := engine.EnsureRun(context.Background(), event, "")
	if err != nil || created || reused.ID != run.ID {
		t.Fatalf("duplicate EnsureRun() = %#v, %v, %v; want run %q", reused, created, err, run.ID)
	}
}

func TestAutomationStoreRetentionSelectsEqualTimestampIdempotencyDeterministically(t *testing.T) {
	limits := automationRetentionLimits{
		Sessions:     0,
		Turns:        0,
		Runs:         0,
		Idempotency:  1,
		PendingAudit: 0,
	}
	for attempt := 0; attempt < 20; attempt++ {
		state := automationStoreTestTerminalHistory(3)
		for i := range state.Outbox {
			delete(state.Idempotency, state.Outbox[i].IdempotencyKey)
		}
		state.Outbox = []AutomationOutboxEntry{}
		for key, record := range state.Idempotency {
			record.CreatedAt = automationStoreTestTime
			state.Idempotency[key] = record
		}
		if changed := applyAutomationRetention(&state, limits); !changed {
			t.Fatal("equal-timestamp fixture was not pruned")
		}
		assertAutomationRetentionIDs(
			t,
			state.Runs,
			func(value AutomationRun) string { return value.ID },
			"airun_00000003",
		)
		if len(state.Idempotency) != 1 {
			t.Fatalf("idempotency count = %d, want 1", len(state.Idempotency))
		}
		if _, exists := state.Idempotency["run:message-3"]; !exists {
			t.Fatalf("equal-timestamp selection = %#v, want run:message-3", state.Idempotency)
		}
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("retained state validation error = %v", err)
		}
	}
}

func TestAutomationStoreRetentionReopenIdempotenceAndRollback(t *testing.T) {
	t.Run("reopen idempotence", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 1, 1, 1, 2, 0)
		dataDir := t.TempDir()
		path := filepath.Join(dataDir, "state.json")
		automationStoreWriteFile(t, path, automationStoreMarshalState(t, automationStoreTestTerminalHistory(3)))

		store, err := OpenAutomationStore(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		wantState := store.Snapshot()
		wantBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
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
		assertAutomationStatesEqual(t, reopened.Snapshot(), wantState)
		gotBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Fatal("reopen rewrote an already-pruned snapshot")
		}
	})

	t.Run("persistence rollback", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 1, 1, 1, 2, 0)
		dataDir := t.TempDir()
		path := filepath.Join(dataDir, "state.json")
		automationStoreWriteFile(t, path, automationStoreMarshalState(t, automationStoreTestTerminalHistory(1)))
		store, err := OpenAutomationStore(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		beforeState := store.Snapshot()
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		injected := errors.New("retention snapshot sync failed")
		failed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepSyncSnapshot && !failed {
				failed = true
				return injected
			}
			return nil
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			*state = automationStoreTestTerminalHistory(2)
			return nil
		})
		if !errors.Is(err, injected) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("Update() error = %v, want injected persistence error", err)
		}
		if !failed {
			t.Fatal("retention persistence failure was not injected")
		}
		assertAutomationStateAndDisk(t, store, path, beforeState, beforeBytes)
	})

	t.Run("open-time persistence rollback", func(t *testing.T) {
		automationStoreUnsetRetentionEnvironment(t)
		automationStoreSetRetentionEnvironment(t, 1, 1, 1, 2, 0)
		dataDir := t.TempDir()
		path := filepath.Join(dataDir, "state.json")
		beforeBytes := automationStoreMarshalState(t, automationStoreTestTerminalHistory(3))
		automationStoreWriteFile(t, path, beforeBytes)

		injected := errors.New("open-time retention snapshot sync failed")
		failed := false
		store, err := openAutomationStoreWithOps(dataDir, automationPersistenceOps{
			syncDirectory: syncAutomationDirectory,
			beforeStep: func(step string) error {
				if step == automationPersistenceStepSyncSnapshot && !failed {
					failed = true
					return injected
				}
				return nil
			},
		})
		if store != nil {
			_ = store.Close()
		}
		if !errors.Is(err, injected) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("openAutomationStoreWithOps() error = %v, want injected persistence error", err)
		}
		if !failed {
			t.Fatal("open-time retention persistence failure was not injected")
		}
		gotBytes, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(gotBytes, beforeBytes) {
			t.Fatal("open-time retention failure changed the persisted snapshot")
		}
		assertNoAutomationAtomicArtifacts(t, dataDir, filepath.Base(path))
	})
}

func TestAutomationStoreDeterministicIDsAndFailureRollback(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	callbackErr := errors.New("callback failed")
	var leaked *AutomationIDAllocator
	err = store.Update(func(_ *AutomationState, allocator *AutomationIDAllocator) error {
		leaked = allocator
		id, nextErr := allocator.Next(AutomationIDKindRun)
		if nextErr != nil {
			t.Fatalf("Next(run) error = %v", nextErr)
		}
		if id != "airun_00000001" {
			t.Fatalf("Next(run) = %q, want airun_00000001", id)
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Update(callback failure) error = %v, want %v", err, callbackErr)
	}
	if _, err := leaked.Next(AutomationIDKindRun); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("Next() after callback error = %v, want validation error", err)
	}
	if got := store.Snapshot().Counters.Run; got != 0 {
		t.Fatalf("run counter after callback failure = %d, want 0", got)
	}

	beforeState := store.Snapshot()
	beforeDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if _, nextErr := allocator.Next(AutomationIDKindAudit); nextErr != nil {
			return nextErr
		}
		state.PendingAudit = nil
		return nil
	})
	if !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("Update(validation failure) error = %v, want validation error", err)
	}
	assertAutomationStateAndDisk(t, store, path, beforeState, beforeDisk)

	originalPath := store.path
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blockingFile, "state.json")
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, nextErr := allocator.Next(AutomationIDKindAudit)
		if nextErr != nil {
			return nextErr
		}
		state.PendingAudit = append(state.PendingAudit, automationStoreTestAudit(id, automationStoreTestTime))
		return nil
	})
	store.path = originalPath
	if !errors.Is(err, ErrAutomationPersistence) {
		t.Fatalf("Update(persistence failure) error = %v, want persistence error", err)
	}
	assertAutomationStateAndDisk(t, store, path, beforeState, beforeDisk)

	kinds := []struct {
		kind AutomationIDKind
		want string
	}{
		{AutomationIDKindSession, "aisession_00000001"},
		{AutomationIDKindTurn, "aiturn_00000001"},
		{AutomationIDKindRun, "airun_00000001"},
		{AutomationIDKindAction, "aiact_00000001"},
		{AutomationIDKindResult, "airesult_00000001"},
		{AutomationIDKindApproval, "aiapproval_00000001"},
		{AutomationIDKindOutbox, "aiout_00000001"},
		{AutomationIDKindAudit, "aiaudit_00000001"},
	}
	err = store.Update(func(_ *AutomationState, allocator *AutomationIDAllocator) error {
		for _, test := range kinds {
			id, nextErr := allocator.Next(test.kind)
			if nextErr != nil {
				return nextErr
			}
			if id != test.want {
				return fmt.Errorf("Next(%s) = %q, want %q", test.kind, id, test.want)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update(success) error = %v", err)
	}
	if _, err := leaked.Next(AutomationIDKindRun); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("leaked allocator Next() error = %v, want validation error", err)
	}
}

func TestAutomationStorePostRenameDirectorySyncRollback(t *testing.T) {
	syncErr := errors.New("injected directory sync failure")

	t.Run("existing snapshot", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		beforeState := store.Snapshot()
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		failed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepSyncSnapshot && !failed {
				failed = true
				assertAutomationRecoveryArtifacts(t, path, true, beforeBytes)
				return syncErr
			}
			return nil
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, syncErr) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("Update() error = %v, want injected persistence error", err)
		}
		if !failed {
			t.Fatal("post-rename directory sync failure was not injected")
		}
		assertAutomationStateAndDisk(t, store, path, beforeState, beforeBytes)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("initially missing snapshot", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		beforeState := newAutomationStateAt(automationStoreTestTime)
		failed := false
		store := &AutomationStore{
			path:          path,
			state:         cloneAutomationState(beforeState),
			syncDirectory: syncAutomationDirectory,
			beforePersistenceStep: func(step string) error {
				if step == automationPersistenceStepSyncSnapshot && !failed {
					failed = true
					assertAutomationRecoveryArtifacts(t, path, false, nil)
					return syncErr
				}
				return nil
			},
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		err := store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, syncErr) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("Update() error = %v, want injected persistence error", err)
		}
		if !failed {
			t.Fatal("post-rename directory sync failure was not injected")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("snapshot after rollback stat error = %v, want not exist", err)
		}
		if got := store.Snapshot(); !reflect.DeepEqual(got, beforeState) {
			t.Fatalf("memory after rollback = %#v, want %#v", got, beforeState)
		}
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})
}

func TestAutomationStoreResolvesInterruptedRecoveryBeforeDecode(t *testing.T) {
	t.Run("existing snapshot", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		oldState := newAutomationStateAt(automationStoreTestTime)
		oldState.LastObservedCRMEventCounter = 7
		newState := cloneAutomationState(oldState)
		newState.LastObservedCRMEventCounter = 8
		oldBytes := automationStoreMarshalState(t, oldState)
		automationStoreWriteFile(t, path, automationStoreMarshalState(t, newState))
		automationStoreWriteRecoveryArtifacts(t, path, true, oldBytes)

		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore() error = %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		assertAutomationStatesEqual(t, store.Snapshot(), oldState)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
		assertAutomationMode(t, path, 0o600)
	})

	t.Run("new snapshot", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		uncommitted := newAutomationStateAt(automationStoreTestTime)
		uncommitted.LastObservedCRMEventCounter = 42
		automationStoreWriteFile(t, path, automationStoreMarshalState(t, uncommitted))
		automationStoreWriteRecoveryArtifacts(t, path, false, nil)

		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore() error = %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		state := store.Snapshot()
		if state.LastObservedCRMEventCounter != 0 {
			t.Fatalf("recovered new-file watermark = %d, want 0", state.LastObservedCRMEventCounter)
		}
		assertAutomationCollectionsNonNil(t, state)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
		assertAutomationMode(t, path, 0o600)
	})
}

func TestAutomationStoreRecoveryStageFailures(t *testing.T) {
	t.Run("preparation failure leaves prior state", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		beforeState := store.Snapshot()
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("prepare rollback failed")
		failed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepPrepareRollback && !failed {
				failed = true
				return injected
			}
			return nil
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, injected) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("Update() error = %v, want preparation persistence error", err)
		}
		assertAutomationStateAndDisk(t, store, path, beforeState, beforeBytes)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("preparation directory sync failure leaves prior state", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		beforeState := store.Snapshot()
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("recovery preparation sync failed")
		failed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepSyncRecovery && !failed {
				failed = true
				return injected
			}
			return nil
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, injected) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("Update() error = %v, want preparation sync persistence error", err)
		}
		assertAutomationStateAndDisk(t, store, path, beforeState, beforeBytes)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("restore failure blocks later updates", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		beforeState := store.Snapshot()
		syncErr := errors.New("post-rename sync failed")
		restoreErr := errors.New("restore failed")
		store.beforePersistenceStep = func(step string) error {
			switch step {
			case automationPersistenceStepSyncSnapshot:
				return syncErr
			case automationPersistenceStepRestoreSnapshot:
				return restoreErr
			default:
				return nil
			}
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, ErrAutomationPersistence) ||
			!errors.Is(err, syncErr) ||
			!errors.Is(err, restoreErr) {
			t.Fatalf("Update() error = %v, want uncertain restore persistence error", err)
		}
		if got := store.Snapshot(); !reflect.DeepEqual(got, beforeState) {
			t.Fatalf("memory changed after failed restore: got %#v, want %#v", got, beforeState)
		}
		var callbackCalled atomic.Bool
		err = store.Update(func(_ *AutomationState, _ *AutomationIDAllocator) error {
			callbackCalled.Store(true)
			return nil
		})
		if !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("blocked Update() error = %v, want persistence error", err)
		}
		if callbackCalled.Load() {
			t.Fatal("blocked store executed another update callback")
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}

		reopened, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore(recovery) error = %v", err)
		}
		t.Cleanup(func() {
			_ = reopened.Close()
		})
		assertAutomationStatesEqual(t, reopened.Snapshot(), beforeState)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("recovery cleanup failure is retryable on open", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		beforeState := store.Snapshot()
		syncErr := errors.New("post-rename sync failed")
		cleanupErr := errors.New("recovery cleanup failed")
		cleanupFailed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepSyncSnapshot {
				return syncErr
			}
			if step == automationPersistenceStepCleanupRecovery && !cleanupFailed {
				cleanupFailed = true
				return cleanupErr
			}
			return nil
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, ErrAutomationPersistence) ||
			!errors.Is(err, syncErr) ||
			!errors.Is(err, cleanupErr) {
			t.Fatalf("Update() error = %v, want cleanup persistence error", err)
		}
		if got := store.Snapshot(); !reflect.DeepEqual(got, beforeState) {
			t.Fatalf("memory changed after cleanup failure: got %#v, want %#v", got, beforeState)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}

		reopened, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore(retry cleanup) error = %v", err)
		}
		t.Cleanup(func() {
			_ = reopened.Close()
		})
		assertAutomationStatesEqual(t, reopened.Snapshot(), beforeState)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("commit cleanup failure rolls back", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		beforeState := store.Snapshot()
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		injected := errors.New("marker cleanup failed")
		failed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepRemoveRecoveryMarker && !failed {
				failed = true
				return injected
			}
			return nil
		}
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if !errors.Is(err, injected) || !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("Update() error = %v, want cleanup persistence error", err)
		}
		assertAutomationStateAndDisk(t, store, path, beforeState, beforeBytes)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("rollback removal sync failure finalizes rename commit", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		beforeState := store.Snapshot()
		injected := errors.New("rollback removal sync failed")
		failed := false
		store.beforePersistenceStep = func(step string) error {
			if step == automationPersistenceStepSyncRollbackRemoval && !failed {
				failed = true
				return injected
			}
			return nil
		}
		// With the rename-based rollback the previous snapshot was already
		// unlinked by the time the rollback removal sync runs, so the commit
		// is final: the update succeeds and both memory and disk hold the new
		// state. A crash would leave at worst an orphan rollback file that
		// the next operation cleans up.
		err = store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			state.LastObservedCRMEventCounter++
			return nil
		})
		if err != nil {
			t.Fatalf("Update() error = %v, want committed success", err)
		}
		got := store.Snapshot()
		if got.LastObservedCRMEventCounter != beforeState.LastObservedCRMEventCounter+1 {
			t.Fatalf("memory watermark = %d, want %d", got.LastObservedCRMEventCounter, beforeState.LastObservedCRMEventCounter+1)
		}
		persistedBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := decodeAutomationState(persistedBytes)
		if err != nil {
			t.Fatalf("decode persisted snapshot: %v", err)
		}
		if persisted.LastObservedCRMEventCounter != beforeState.LastObservedCRMEventCounter+1 {
			t.Fatalf("disk watermark = %d, want %d", persisted.LastObservedCRMEventCounter, beforeState.LastObservedCRMEventCounter+1)
		}
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})
}

func TestAutomationStoreRejectsUnsafeRecoveryFiles(t *testing.T) {
	validBytes := automationStoreMarshalState(t, newAutomationStateAt(automationStoreTestTime))

	t.Run("symlink marker", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		automationStoreWriteFile(t, path, validBytes)
		rollbackPath, markerPath := automationRecoveryPaths(path)
		automationStoreWriteFile(t, rollbackPath, validBytes)
		target := filepath.Join(parent, "marker-target")
		automationStoreWriteFile(t, target, automationStoreRecoveryMarkerBytes(t, true))
		if err := os.Symlink(target, markerPath); err != nil {
			t.Fatal(err)
		}
		if store, err := OpenAutomationStore(parent); err == nil {
			_ = store.Close()
			t.Fatal("OpenAutomationStore() accepted a symlink recovery marker")
		} else if !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("OpenAutomationStore() error = %v, want persistence error", err)
		}
	})

	t.Run("directory rollback", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		automationStoreWriteFile(t, path, validBytes)
		rollbackPath, markerPath := automationRecoveryPaths(path)
		if err := os.Mkdir(rollbackPath, 0o700); err != nil {
			t.Fatal(err)
		}
		automationStoreWriteFile(t, markerPath, automationStoreRecoveryMarkerBytes(t, true))
		if store, err := OpenAutomationStore(parent); err == nil {
			_ = store.Close()
			t.Fatal("OpenAutomationStore() accepted a nonregular rollback snapshot")
		} else if !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("OpenAutomationStore() error = %v, want persistence error", err)
		}
	})
}

func TestAutomationStoreCallbackPanicRollsBackAndDeactivatesAllocator(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "state.json")
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	beforeState := store.Snapshot()
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	panicValue := &struct{ message string }{message: "callback panic"}
	var leaked *AutomationIDAllocator
	var allocatedID string
	var allocatedErr error
	var recovered any

	func() {
		defer func() {
			recovered = recover()
		}()
		_ = store.Update(func(_ *AutomationState, allocator *AutomationIDAllocator) error {
			leaked = allocator
			allocatedID, allocatedErr = allocator.Next(AutomationIDKindRun)
			panic(panicValue)
		})
		t.Fatal("Update() returned instead of propagating callback panic")
	}()

	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want original panic %#v", recovered, panicValue)
	}
	if allocatedErr != nil || allocatedID != "airun_00000001" {
		t.Fatalf("allocation before panic = %q, %v; want airun_00000001, nil", allocatedID, allocatedErr)
	}
	if _, err := leaked.Next(AutomationIDKindRun); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("leaked allocator Next() error = %v, want validation error", err)
	}
	assertAutomationStateAndDisk(t, store, path, beforeState, beforeBytes)
}

func TestAutomationStoreRestartRecoveryAndDeepCopyIsolation(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(automationStoreSeedFullState)
	if err != nil {
		t.Fatalf("seed Update() error = %v", err)
	}

	snapshot := store.Snapshot()
	snapshot.Policy.R1AllowedTools[0] = AutomationToolMessageClassify
	snapshot.Policy.R2AllowedIntents[0] = AutomationReplyIntentOffer
	snapshot.Runs[0].ActionIDs[0] = "mutated"
	snapshot.Runs[0].StartedAt = nil
	snapshot.Actions[0].RiskReasons[0] = "mutated"
	snapshot.Actions[0].Evidence[0].Excerpt = "mutated"
	snapshot.Actions[0].Recipient.PeerID = "mutated"
	snapshot.Actions[0].Payload[0] = '['
	snapshot.Actions[0].Result[0] = '['
	snapshot.Outbox[0].CompletedAt = nil
	record := snapshot.Idempotency["run:message-1"]
	record.ResourceID = "mutated"
	snapshot.Idempotency["run:message-1"] = record
	snapshot.PendingAudit[0].Data[0] = '['

	want := automationStoreTestFullState()
	assertAutomationStatesEqual(t, store.Snapshot(), want)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatalf("OpenAutomationStore(restart) error = %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	assertAutomationStatesEqual(t, reopened.Snapshot(), want)

	err = reopened.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindAudit)
		if err != nil {
			return err
		}
		if id != "aiaudit_00000002" {
			return fmt.Errorf("Next(audit) after restart = %q, want aiaudit_00000002", id)
		}
		state.PendingAudit = append(state.PendingAudit, automationStoreTestAudit(id, automationStoreTestTime.Add(10*time.Minute)))
		return nil
	})
	if err != nil {
		t.Fatalf("Update() after restart error = %v", err)
	}
}

func TestAutomationStoreRejectsStrictInvalidSnapshotsAndNonRegularFiles(t *testing.T) {
	validState := automationStoreTestFullState()
	validJSON, err := json.Marshal(validState)
	if err != nil {
		t.Fatal(err)
	}

	var unknown map[string]any
	if err := json.Unmarshal(validJSON, &unknown); err != nil {
		t.Fatal(err)
	}
	unknown["unexpected"] = true
	unknownJSON, err := json.Marshal(unknown)
	if err != nil {
		t.Fatal(err)
	}

	var missing map[string]any
	if err := json.Unmarshal(validJSON, &missing); err != nil {
		t.Fatal(err)
	}
	delete(missing, "actions")
	missingJSON, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}

	duplicateJSON := bytes.Replace(
		validJSON,
		[]byte(fmt.Sprintf(`"schema_version":%d`, AutomationSnapshotSchemaVersion)),
		[]byte(fmt.Sprintf(
			`"schema_version":%d,"schema_version":%d`,
			AutomationSnapshotSchemaVersion,
			AutomationSnapshotSchemaVersion,
		)),
		1,
	)
	var nullRuns map[string]any
	if err := json.Unmarshal(validJSON, &nullRuns); err != nil {
		t.Fatal(err)
	}
	nullRuns["runs"] = nil
	nullRunsJSON, err := json.Marshal(nullRuns)
	if err != nil {
		t.Fatal(err)
	}
	caseVariantJSON := bytes.Replace(validJSON, []byte(`"schema_version"`), []byte(`"SchemaVersion"`), 1)

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "whitespace", data: []byte(" \n\t")},
		{name: "corrupt", data: []byte(`{"schema_version":`)},
		{name: "unknown field", data: unknownJSON},
		{name: "missing field", data: missingJSON},
		{name: "duplicate field", data: duplicateJSON},
		{name: "null collection", data: nullRunsJSON},
		{name: "case variant field", data: caseVariantJSON},
		{name: "trailing JSON", data: append(append([]byte{}, validJSON...), []byte(` {}`)...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "state.json")
			if err := os.WriteFile(path, test.data, 0o644); err != nil {
				t.Fatal(err)
			}

			if store, err := OpenAutomationStore(parent); err == nil {
				_ = store.Close()
				t.Fatal("OpenAutomationStore() succeeded, want error")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, test.data) {
				t.Fatalf("snapshot was rewritten: got %q, want %q", got, test.data)
			}
		})
	}

	t.Run("directory snapshot", func(t *testing.T) {
		dataDir := t.TempDir()
		path := filepath.Join(dataDir, "state.json")
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if store, err := OpenAutomationStore(dataDir); err == nil {
			_ = store.Close()
			t.Fatal("OpenAutomationStore() accepted a directory")
		} else if !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("OpenAutomationStore() error = %v, want persistence error", err)
		}
	})

	t.Run("symlink snapshot", func(t *testing.T) {
		parent := t.TempDir()
		target := filepath.Join(parent, "target.json")
		link := filepath.Join(parent, "state.json")
		if err := os.WriteFile(target, validJSON, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if store, err := OpenAutomationStore(parent); err == nil {
			_ = store.Close()
			t.Fatal("OpenAutomationStore() accepted a symlink")
		} else if !errors.Is(err, ErrAutomationPersistence) {
			t.Fatalf("OpenAutomationStore() error = %v, want persistence error", err)
		}
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, validJSON) {
			t.Fatal("symlink target was modified")
		}
	})
}

func TestAutomationStoreRejectsMissingNestedRequiredFields(t *testing.T) {
	validJSON := automationStoreMarshalState(t, automationStoreTestFullState())
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "counter member",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONObject(t, snapshot["counters"]), "audit")
			},
		},
		{
			name: "policy version",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONObject(t, snapshot["policy"]), "version")
			},
		},
		{
			name: "policy boolean",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONObject(t, snapshot["policy"]), "r1_auto_approve")
			},
		},
		{
			name: "policy private reply boolean",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONObject(t, snapshot["policy"]), "r2_private_reply_auto_approve")
			},
		},
		{
			name: "policy R1 threshold",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONObject(t, snapshot["policy"]), "r1_min_confidence")
			},
		},
		{
			name: "policy threshold",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONObject(t, snapshot["policy"]), "r2_min_confidence")
			},
		},
		{
			name: "run source version",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["runs"], 0), "source_message_version")
			},
		},
		{
			name: "run version",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["runs"], 0), "version")
			},
		},
		{
			name: "run attempt",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["runs"], 0), "attempt")
			},
		},
		{
			name: "action policy version",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["actions"], 0), "policy_version")
			},
		},
		{
			name: "action confidence",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["actions"], 0), "confidence")
			},
		},
		{
			name: "action retryable boolean",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["actions"], 0), "retryable")
			},
		},
		{
			name: "action attempt",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["actions"], 0), "attempt")
			},
		},
		{
			name: "action version",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["actions"], 0), "version")
			},
		},
		{
			name: "evidence excerpt",
			mutate: func(snapshot map[string]any) {
				action := automationStoreJSONArrayObject(t, snapshot["actions"], 0)
				delete(automationStoreJSONArrayObject(t, action["evidence"], 0), "excerpt")
			},
		},
		{
			name: "source suspicious boolean",
			mutate: func(snapshot map[string]any) {
				action := automationStoreJSONArrayObject(t, snapshot["actions"], 0)
				delete(automationStoreJSONObject(t, action["source_message"]), "suspicious")
			},
		},
		{
			name: "source expected version",
			mutate: func(snapshot map[string]any) {
				action := automationStoreJSONArrayObject(t, snapshot["actions"], 0)
				delete(automationStoreJSONObject(t, action["source_message"]), "expected_version")
			},
		},
		{
			name: "recipient peer",
			mutate: func(snapshot map[string]any) {
				action := automationStoreJSONArrayObject(t, snapshot["actions"], 0)
				delete(automationStoreJSONObject(t, action["recipient"]), "peer_id")
			},
		},
		{
			name: "approval action version",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["approvals"], 0), "action_version")
			},
		},
		{
			name: "outbox attempt",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["outbox"], 0), "attempt")
			},
		},
		{
			name: "outbox dispatch attempted boolean",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["outbox"], 0), "dispatch_attempted")
			},
		},
		{
			name: "idempotency timestamp",
			mutate: func(snapshot map[string]any) {
				idempotency := automationStoreJSONObject(t, snapshot["idempotency"])
				delete(automationStoreJSONObject(t, idempotency["run:message-1"]), "created_at")
			},
		},
		{
			name: "audit data",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["pending_audit"], 0), "data")
			},
		},
		{
			name: "audit timestamp",
			mutate: func(snapshot map[string]any) {
				delete(automationStoreJSONArrayObject(t, snapshot["pending_audit"], 0), "created_at")
			},
		},
		{
			name: "top-level watermark",
			mutate: func(snapshot map[string]any) {
				delete(snapshot, "last_observed_crm_event_counter")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var snapshot map[string]any
			if err := json.Unmarshal(validJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			test.mutate(snapshot)
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeAutomationState(data); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("decodeAutomationState() error = %v, want validation error", err)
			}
		})
	}

	for _, counterField := range []string{"run", "action", "approval", "outbox", "audit"} {
		t.Run("all counter members/"+counterField, func(t *testing.T) {
			var snapshot map[string]any
			if err := json.Unmarshal(validJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			delete(automationStoreJSONObject(t, snapshot["counters"]), counterField)
			data, err := json.Marshal(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeAutomationState(data); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("decodeAutomationState() error = %v, want validation error", err)
			}
		})
	}

	t.Run("precondition expected version", func(t *testing.T) {
		state := automationStoreTestStateWithoutOutbox()
		action := &state.Actions[0]
		action.Tool = AutomationToolCandidateUpdate
		action.Recipient = nil
		action.Payload = json.RawMessage(
			`{"candidate":{"id":"cand_00000001","expected_version":3},"patch":{"full_name":"Alice"}}`,
		)
		action.Preconditions = []AutomationEntityPrecondition{{
			EntityType:      AutomationEntityCandidate,
			EntityID:        "cand_00000001",
			ExpectedVersion: 3,
		}}
		data := automationStoreMarshalState(t, state)
		var snapshot map[string]any
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		actionJSON := automationStoreJSONArrayObject(t, snapshot["actions"], 0)
		delete(automationStoreJSONArrayObject(t, actionJSON["preconditions"], 0), "expected_version")
		data, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeAutomationState(data); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("decodeAutomationState() error = %v, want validation error", err)
		}
	})
}

func TestValidateAutomationStateRejectsInvalidState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*AutomationState)
	}{
		{
			name: "nil top-level collection",
			mutate: func(state *AutomationState) {
				state.Actions = nil
			},
		},
		{
			name: "counter behind ID",
			mutate: func(state *AutomationState) {
				state.Counters.Action = 0
			},
		},
		{
			name: "missing owning run",
			mutate: func(state *AutomationState) {
				state.Actions[0].RunID = "airun_00000002"
			},
		},
		{
			name: "run omits action",
			mutate: func(state *AutomationState) {
				state.Runs[0].ActionIDs = []string{}
			},
		},
		{
			name: "missing run idempotency",
			mutate: func(state *AutomationState) {
				delete(state.Idempotency, state.Runs[0].IdempotencyKey)
			},
		},
		{
			name: "run idempotency kind mismatch",
			mutate: func(state *AutomationState) {
				record := state.Idempotency[state.Runs[0].IdempotencyKey]
				record.Kind = "outbox"
				state.Idempotency[state.Runs[0].IdempotencyKey] = record
			},
		},
		{
			name: "extra idempotency mapping",
			mutate: func(state *AutomationState) {
				state.Idempotency["extra"] = AutomationIdempotencyRecord{
					Kind:       "run",
					ResourceID: state.Runs[0].ID,
					CreatedAt:  automationStoreTestTime,
				}
			},
		},
		{
			name: "unsupported idempotency kind",
			mutate: func(state *AutomationState) {
				record := state.Idempotency[state.Runs[0].IdempotencyKey]
				record.Kind = "action"
				state.Idempotency[state.Runs[0].IdempotencyKey] = record
			},
		},
		{
			name: "outbox recipient mismatch",
			mutate: func(state *AutomationState) {
				state.Outbox[0].PeerID = "other"
			},
		},
		{
			name: "outbox idempotency kind mismatch",
			mutate: func(state *AutomationState) {
				record := state.Idempotency[state.Outbox[0].IdempotencyKey]
				record.Kind = "send"
				state.Idempotency[state.Outbox[0].IdempotencyKey] = record
			},
		},
		{
			name: "outbox idempotency resource mismatch",
			mutate: func(state *AutomationState) {
				record := state.Idempotency[state.Outbox[0].IdempotencyKey]
				record.ResourceID = state.Runs[0].ID
				state.Idempotency[state.Outbox[0].IdempotencyKey] = record
			},
		},
		{
			name: "invalid payload JSON",
			mutate: func(state *AutomationState) {
				state.Actions[0].Payload = json.RawMessage(`{"intent":`)
			},
		},
		{
			name: "dependency cycle",
			mutate: func(state *AutomationState) {
				second := state.Actions[0]
				second.ID = "aiact_00000002"
				second.ProviderCallID = "call-2"
				second.DependsOnActionIDs = []string{"aiact_00000001"}
				second.Recipient = nil
				second.Tool = AutomationToolMessageClassify
				state.Counters.Action = 2
				state.Actions[0].DependsOnActionIDs = []string{"aiact_00000002"}
				state.Actions = append(state.Actions, second)
				state.Runs[0].ActionIDs = append(state.Runs[0].ActionIDs, second.ID)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := automationStoreTestFullState()
			test.mutate(&state)
			if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("ValidateAutomationState() error = %v, want validation error", err)
			}
		})
	}
}

func TestValidateAutomationActionRecipientBinding(t *testing.T) {
	t.Run("private reply binds original peer", func(t *testing.T) {
		state := automationStoreTestFullState()
		state.Actions[0].Recipient.PeerID = "other-peer"
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState() error = %v, want validation error", err)
		}

		state = automationStoreTestFullState()
		state.Actions[0].Recipient = nil
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(nil recipient) error = %v, want validation error", err)
		}
	})

	t.Run("non-reply recipient is forbidden", func(t *testing.T) {
		state := automationStoreTestStateWithoutOutbox()
		action := &state.Actions[0]
		action.Tool = AutomationToolMessageClassify
		action.Payload = json.RawMessage(`{"labels":["general"],"summary":"hello"}`)
		action.Recipient = &AutomationRecipient{ChatType: ChatTypePrivate, PeerID: action.SourceMessage.PeerID}
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState() error = %v, want validation error", err)
		}
	})

	t.Run("group reply only persists as denied R4", func(t *testing.T) {
		state := automationStoreTestStateWithoutOutbox()
		action := &state.Actions[0]
		action.SourceMessage.ChatType = ChatTypeGroup
		action.SourceMessage.PeerID = "group-1"
		action.Recipient = nil
		action.Status = AutomationActionDenied
		action.Risk = AutomationRiskR4
		action.PolicyDecision = AutomationDecisionDeny
		state.PermissionRequests[0].Risk = AutomationRiskR4
		state.PermissionRequests[0].Status = AutomationPermissionStatusDenied
		state.PermissionRequests[0].Decision = AutomationPermissionDenyOnce
		state.PermissionRequests[0].OperatorID = automationPermissionOperatorAuto
		state.Results[0].Status = AutomationDeliveryFailed
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("ValidateAutomationState(denied group reply) error = %v", err)
		}

		state.Actions[0].Status = AutomationActionAwaitingReview
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(non-denied group reply) error = %v, want validation error", err)
		}
	})
}

func TestValidateAutomationActionPayloadAndReferenceConsistency(t *testing.T) {
	t.Run("strict payload shape", func(t *testing.T) {
		state := automationStoreTestFullState()
		state.Actions[0].Payload = json.RawMessage(`{"intent":"general","text":"hello","recipient":"other"}`)
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState() error = %v, want validation error", err)
		}
	})

	t.Run("existing entity reference", func(t *testing.T) {
		state := automationStoreTestStateWithoutOutbox()
		action := &state.Actions[0]
		action.Tool = AutomationToolCandidateUpdate
		action.Recipient = nil
		action.Payload = json.RawMessage(
			`{"candidate":{"id":"cand_00000001","expected_version":3},"patch":{"full_name":"Alice"}}`,
		)
		state.PermissionRequests[0].Tool = action.Tool
		action.Preconditions = []AutomationEntityPrecondition{{
			EntityType:      AutomationEntityCandidate,
			EntityID:        "cand_00000001",
			ExpectedVersion: 3,
		}}
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("ValidateAutomationState(matching existing reference) error = %v", err)
		}

		state.Actions[0].Preconditions[0].ExpectedVersion = 4
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(version mismatch) error = %v, want validation error", err)
		}

		state.Actions[0].Preconditions = append(
			state.Actions[0].Preconditions,
			AutomationEntityPrecondition{
				EntityType:      AutomationEntityJob,
				EntityID:        "job_00000001",
				ExpectedVersion: 1,
			},
		)
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(extra precondition) error = %v, want validation error", err)
		}
	})

	t.Run("prior call dependency", func(t *testing.T) {
		state := automationStoreTestPriorCallState()
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("ValidateAutomationState(matching prior call) error = %v", err)
		}

		state.Actions[1].DependsOnActionIDs = []string{}
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(missing dependency) error = %v, want validation error", err)
		}

		state = automationStoreTestPriorCallState()
		state.Actions[1].Payload = json.RawMessage(
			`{"candidate":{"from_call":"missing-call"},"patch":{"full_name":"Alice"}}`,
		)
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(missing provider call) error = %v, want validation error", err)
		}
	})
}

func TestValidateAutomationOutboxClaimStates(t *testing.T) {
	t.Run("pending creation attempt zero", func(t *testing.T) {
		state := automationStoreTestFullState()
		entry := &state.Outbox[0]
		entry.Status = AutomationOutboxPending
		entry.Attempt = 0
		entry.DispatchAttempted = false
		entry.UpdatedAt = entry.CreatedAt
		entry.DispatchStartedAt = nil
		entry.CompletedAt = nil
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("ValidateAutomationState(pending) error = %v", err)
		}
	})

	t.Run("pending rejects nonzero attempt", func(t *testing.T) {
		state := automationStoreTestFullState()
		entry := &state.Outbox[0]
		entry.Status = AutomationOutboxPending
		entry.Attempt = 1
		entry.DispatchAttempted = false
		entry.UpdatedAt = entry.CreatedAt
		entry.DispatchStartedAt = nil
		entry.CompletedAt = nil
		if err := ValidateAutomationState(&state); !errors.Is(err, ErrAutomationValidation) {
			t.Fatalf("ValidateAutomationState(pending attempt) error = %v, want validation error", err)
		}
	})

	t.Run("dispatching before worker acceptance", func(t *testing.T) {
		state := automationStoreTestFullState()
		entry := &state.Outbox[0]
		entry.Status = AutomationOutboxDispatching
		entry.DispatchAttempted = false
		entry.UpdatedAt = *entry.DispatchStartedAt
		entry.CompletedAt = nil
		if err := ValidateAutomationState(&state); err != nil {
			t.Fatalf("ValidateAutomationState(dispatching) error = %v", err)
		}
	})
}

func TestAutomationStoreCloseAndConcurrentUpdates(t *testing.T) {
	t.Run("close", func(t *testing.T) {
		store, err := OpenAutomationStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("second Close() error = %v", err)
		}
		var called atomic.Bool
		err = store.Update(func(_ *AutomationState, _ *AutomationIDAllocator) error {
			called.Store(true)
			return nil
		})
		if !errors.Is(err, ErrAutomationStoreClosed) {
			t.Fatalf("Update() after Close error = %v, want ErrAutomationStoreClosed", err)
		}
		if called.Load() {
			t.Fatal("Update callback ran after Close")
		}
		assertAutomationCollectionsNonNil(t, store.Snapshot())
	})

	t.Run("concurrent", func(t *testing.T) {
		store, err := OpenAutomationStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		const updates = 24
		start := make(chan struct{})
		errs := make(chan error, updates+4)
		var writers sync.WaitGroup
		for i := 0; i < updates; i++ {
			writers.Add(1)
			go func(index int) {
				defer writers.Done()
				<-start
				errs <- store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
					id, err := allocator.Next(AutomationIDKindAudit)
					if err != nil {
						return err
					}
					state.PendingAudit = append(
						state.PendingAudit,
						automationStoreTestAudit(id, automationStoreTestTime.Add(time.Duration(index+1)*time.Minute)),
					)
					return nil
				})
			}(i)
		}

		stopReaders := make(chan struct{})
		var readers sync.WaitGroup
		for i := 0; i < 4; i++ {
			readers.Add(1)
			go func() {
				defer readers.Done()
				for {
					select {
					case <-stopReaders:
						return
					default:
						snapshot := store.Snapshot()
						if snapshot.PendingAudit == nil || snapshot.Idempotency == nil {
							errs <- errors.New("Snapshot returned a nil collection")
							return
						}
						if len(snapshot.PendingAudit) > 0 {
							snapshot.PendingAudit[0].Data = json.RawMessage(`{"mutated":true}`)
						}
					}
				}
			}()
		}

		close(start)
		writers.Wait()
		close(stopReaders)
		readers.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent operation error = %v", err)
			}
		}

		state := store.Snapshot()
		if state.Counters.Audit != updates || len(state.PendingAudit) != updates {
			t.Fatalf("concurrent state counters=%d records=%d, want %d", state.Counters.Audit, len(state.PendingAudit), updates)
		}
		ids := make([]string, 0, updates)
		for _, record := range state.PendingAudit {
			ids = append(ids, record.ID)
			if !bytes.Equal(record.Data, json.RawMessage(`{"index":1}`)) {
				t.Fatalf("snapshot mutation leaked into audit %s: %s", record.ID, record.Data)
			}
		}
		sort.Strings(ids)
		for i, id := range ids {
			want := fmt.Sprintf("aiaudit_%08d", i+1)
			if id != want {
				t.Fatalf("audit ID[%d] = %q, want %q", i, id, want)
			}
		}
	})
}

func automationStoreSeedFullState(state *AutomationState, allocator *AutomationIDAllocator) error {
	kinds := []struct {
		kind AutomationIDKind
		want string
	}{
		{AutomationIDKindSession, "aisession_00000001"},
		{AutomationIDKindTurn, "aiturn_00000001"},
		{AutomationIDKindRun, "airun_00000001"},
		{AutomationIDKindAction, "aiact_00000001"},
		{AutomationIDKindResult, "airesult_00000001"},
		{AutomationIDKindApproval, "aiapproval_00000001"},
		{AutomationIDKindOutbox, "aiout_00000001"},
		{AutomationIDKindAudit, "aiaudit_00000001"},
		{AutomationIDKindPermissionRequest, "aipermreq_00000001"},
	}
	for _, item := range kinds {
		id, err := allocator.Next(item.kind)
		if err != nil {
			return err
		}
		if id != item.want {
			return fmt.Errorf("Next(%s) = %q, want %q", item.kind, id, item.want)
		}
	}
	fixture := automationStoreTestFullState()
	state.Policy = fixture.Policy
	state.Sessions = fixture.Sessions
	state.Turns = fixture.Turns
	state.TurnEvents = fixture.TurnEvents
	state.Runs = fixture.Runs
	state.Actions = fixture.Actions
	state.Results = fixture.Results
	state.Approvals = fixture.Approvals
	state.Outbox = fixture.Outbox
	state.Idempotency = fixture.Idempotency
	state.PendingAudit = fixture.PendingAudit
	state.PermissionRequests = fixture.PermissionRequests
	state.PermissionRules = fixture.PermissionRules
	state.LastObservedCRMEventCounter = fixture.LastObservedCRMEventCounter
	return nil
}

func automationStoreTestFullState() AutomationState {
	createdAt := automationStoreTestTime
	startedAt := createdAt.Add(time.Minute)
	completedAt := startedAt.Add(time.Minute)
	state := newAutomationStateAt(createdAt)
	state.Counters = AutomationCounters{
		Session:           1,
		Turn:              1,
		Run:               1,
		Action:            1,
		Result:            1,
		Approval:          1,
		Outbox:            1,
		Audit:             1,
		PermissionRequest: 1,
	}
	state.Policy.R1AllowedTools = []AutomationToolName{AutomationToolReplyPrivateText}
	state.Policy.R2AllowedIntents = []string{AutomationReplyIntentGeneral}
	state.Sessions = []AutomationSession{{
		ID:        "aisession_00000001",
		Kind:      AutomationSessionKindAutomation,
		Title:     "Automation run airun_00000001",
		Status:    AutomationSessionStatusCompleted,
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: completedAt,
	}}
	state.Turns = []AutomationTurn{{
		ID:           "aiturn_00000001",
		SessionID:    "aisession_00000001",
		Status:       AutomationTurnCompleted,
		ProviderName: "test-provider",
		Version:      1,
		CreatedAt:    createdAt,
		UpdatedAt:    completedAt,
		StartedAt:    automationTimePointer(startedAt),
		CompletedAt:  automationTimePointer(completedAt),
	}}
	state.Runs = []AutomationRun{{
		ID:                     "airun_00000001",
		SessionID:              "aisession_00000001",
		TurnID:                 "aiturn_00000001",
		IdempotencyKey:         "run:message-1",
		EngineVersion:          AutomationEngineVersion,
		ProviderName:           "test-provider",
		SourceMessageEventID:   "event_00000001",
		SourceMessageKey:       "message-1",
		SourceMessageVersion:   1,
		Status:                 AutomationRunSucceeded,
		Version:                1,
		Attempt:                1,
		ProviderResponseSHA256: strings.Repeat("a", 64),
		ActionIDs:              []string{"aiact_00000001"},
		CreatedAt:              createdAt,
		StartedAt:              automationTimePointer(startedAt),
		CompletedAt:            automationTimePointer(completedAt),
	}}
	state.Actions = []AutomationAction{{
		ID:             "aiact_00000001",
		RunID:          "airun_00000001",
		ProviderCallID: "call-1",
		Tool:           AutomationToolReplyPrivateText,
		Status:         AutomationActionSucceeded,
		Risk:           AutomationRiskR2,
		PolicyDecision: AutomationDecisionReview,
		PolicyVersion:  1,
		RiskReasons:    []string{"external communication"},
		Confidence:     0.9,
		Evidence: []AutomationEvidence{{
			Source:   "message",
			EntityID: "cand_00000001",
			Field:    "text",
			Excerpt:  "hello",
		}},
		SourceMessage: AutomationSourceMessageRef{
			EventID:         "event_00000001",
			MessageKey:      "message-1",
			ExpectedVersion: 1,
			ChatType:        ChatTypePrivate,
			PeerID:          "wxid_peer",
		},
		Preconditions:      []AutomationEntityPrecondition{},
		DependsOnActionIDs: []string{},
		Recipient: &AutomationRecipient{
			ChatType: ChatTypePrivate,
			PeerID:   "wxid_peer",
		},
		Payload:   json.RawMessage(`{"intent":"general","text":"hello"}`),
		ResultID:  "airesult_00000001",
		Result:    json.RawMessage(`{"sent":true}`),
		Attempt:   1,
		Version:   1,
		CreatedAt: createdAt,
		UpdatedAt: completedAt,
	}}
	state.PermissionRequests = []AutomationPermissionRequest{{
		ID:         "aipermreq_00000001",
		RunID:      "airun_00000001",
		ActionID:   "aiact_00000001",
		Tool:       AutomationToolReplyPrivateText,
		Risk:       AutomationRiskR2,
		Status:     AutomationPermissionStatusAllowed,
		Version:    1,
		Decision:   AutomationPermissionAllowOnce,
		OperatorID: "operator-1",
		Reason:     "reviewed",
		CreatedAt:  startedAt,
		UpdatedAt:  startedAt,
	}}
	state.Results = []AutomationResult{{
		ID:                  "airesult_00000001",
		ActionID:            "aiact_00000001",
		PermissionRequestID: "aipermreq_00000001",
		Status:              AutomationDeliverySent,
		Data:                json.RawMessage(`{"sent":true}`),
		CreatedAt:           completedAt,
	}}
	state.Approvals = []AutomationApprovalRecord{{
		ID:            "aiapproval_00000001",
		ActionID:      "aiact_00000001",
		ActionVersion: 1,
		Decision:      "approved",
		OperatorID:    "operator-1",
		Reason:        "reviewed",
		CreatedAt:     startedAt,
	}}
	state.Outbox = []AutomationOutboxEntry{{
		ID:                "aiout_00000001",
		ActionID:          "aiact_00000001",
		IdempotencyKey:    "send:aiact_00000001",
		ChatType:          ChatTypePrivate,
		PeerID:            "wxid_peer",
		Text:              "hello",
		Status:            AutomationOutboxSent,
		Attempt:           1,
		DispatchAttempted: true,
		CreatedAt:         createdAt,
		UpdatedAt:         completedAt,
		DispatchStartedAt: automationTimePointer(startedAt),
		CompletedAt:       automationTimePointer(completedAt),
	}}
	state.Idempotency = map[string]AutomationIdempotencyRecord{
		"run:message-1": {
			Kind:       "run",
			ResourceID: "airun_00000001",
			CreatedAt:  createdAt,
		},
		"send:aiact_00000001": {
			Kind:       "outbox",
			ResourceID: "aiout_00000001",
			CreatedAt:  createdAt,
		},
	}
	state.PendingAudit = []AutomationAuditRecord{automationStoreTestAudit("aiaudit_00000001", completedAt)}
	state.PendingAudit[0].RunID = "airun_00000001"
	state.PendingAudit[0].ActionID = "aiact_00000001"
	state.PendingAudit[0].OutboxID = "aiout_00000001"
	state.LastObservedCRMEventCounter = 1
	return state
}

func automationStoreTestStateWithoutOutbox() AutomationState {
	state := automationStoreTestFullState()
	state.Outbox = []AutomationOutboxEntry{}
	delete(state.Idempotency, "send:aiact_00000001")
	state.PendingAudit[0].OutboxID = ""
	return state
}

func automationStoreTestPriorCallState() AutomationState {
	state := automationStoreTestStateWithoutOutbox()
	first := state.Actions[0]
	first.ProviderCallID = "create-candidate"
	first.Tool = AutomationToolCandidateCreate
	first.Recipient = nil
	first.Payload = json.RawMessage(`{"full_name":"Alice","skills":[]}`)
	first.Preconditions = []AutomationEntityPrecondition{}
	first.DependsOnActionIDs = []string{}

	second := first
	second.ID = "aiact_00000002"
	second.ProviderCallID = "update-candidate"
	second.Tool = AutomationToolCandidateUpdate
	second.Payload = json.RawMessage(
		`{"candidate":{"from_call":"create-candidate"},"patch":{"full_name":"Alice Smith"}}`,
	)
	second.ResultID = "airesult_00000002"
	second.Result = json.RawMessage(`{"updated":true}`)
	second.DependsOnActionIDs = []string{first.ID}

	state.Counters.Action = 2
	state.Counters.Result = 2
	state.Counters.PermissionRequest = 2
	state.Actions = []AutomationAction{first, second}
	state.Runs[0].ActionIDs = []string{first.ID, second.ID}
	state.PermissionRequests[0].Tool = first.Tool
	state.PermissionRequests = append(state.PermissionRequests, AutomationPermissionRequest{
		ID:         "aipermreq_00000002",
		RunID:      second.RunID,
		ActionID:   second.ID,
		Tool:       second.Tool,
		Risk:       second.Risk,
		Status:     AutomationPermissionStatusAllowed,
		Version:    1,
		Decision:   AutomationPermissionAllowOnce,
		OperatorID: "operator-1",
		CreatedAt:  second.CreatedAt,
		UpdatedAt:  second.CreatedAt,
	})
	state.Results = append(state.Results, AutomationResult{
		ID:                  second.ResultID,
		ActionID:            second.ID,
		PermissionRequestID: "aipermreq_00000002",
		Status:              AutomationDeliverySent,
		Data:                cloneAutomationBytes(second.Result),
		CreatedAt:           second.UpdatedAt,
	})
	return state
}

func automationStoreTestAudit(id string, createdAt time.Time) AutomationAuditRecord {
	return AutomationAuditRecord{
		ID:        id,
		EventType: "test.event",
		ActorType: "system",
		Data:      json.RawMessage(`{"index":1}`),
		CreatedAt: createdAt,
	}
}

func automationTimePointer(value time.Time) *time.Time {
	return &value
}

func automationStoreMarshalState(t *testing.T, state AutomationState) []byte {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func automationStoreUnsetRetentionEnvironment(t testing.TB) {
	t.Helper()
	for _, name := range []string{
		automationRetentionSessionsEnv,
		automationRetentionTurnsEnv,
		automationRetentionRunsEnv,
		automationRetentionIdempotencyEnv,
		automationRetentionPendingAuditEnv,
	} {
		value, exists := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		name := name
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
}

func automationStoreSetRetentionEnvironment(
	t *testing.T,
	sessions int,
	turns int,
	runs int,
	idempotency int,
	pendingAudit int,
) {
	t.Helper()
	values := map[string]int{
		automationRetentionSessionsEnv:     sessions,
		automationRetentionTurnsEnv:        turns,
		automationRetentionRunsEnv:         runs,
		automationRetentionIdempotencyEnv:  idempotency,
		automationRetentionPendingAuditEnv: pendingAudit,
	}
	for name, value := range values {
		t.Setenv(name, fmt.Sprint(value))
	}
}

func automationStoreTestStandaloneSessions(count int) AutomationState {
	state := newAutomationStateAt(automationStoreTestTime)
	state.Counters.Session = uint64(count)
	state.Sessions = make([]AutomationSession, count)
	for index := 1; index <= count; index++ {
		state.Sessions[index-1] = automationStoreTestStandaloneSession(index)
	}
	return state
}

func automationStoreTestStandaloneSession(index int) AutomationSession {
	timestamp := automationStoreTestTime.Add(time.Duration(index) * time.Microsecond)
	suffix := fmt.Sprintf("%08d", index)
	return AutomationSession{
		ID:        "aisession_" + suffix,
		Kind:      AutomationSessionKindAutomation,
		Title:     "Session " + suffix,
		Status:    AutomationSessionStatusCompleted,
		Version:   1,
		CreatedAt: timestamp,
		UpdatedAt: timestamp,
	}
}

func automationStoreTestTerminalHistory(count int) AutomationState {
	state := newAutomationStateAt(automationStoreTestTime)
	state.Policy = automationStoreTestFullState().Policy
	state.LastObservedCRMEventCounter = 77
	state.Counters = AutomationCounters{
		Session:           uint64(count),
		Turn:              uint64(count),
		TurnEvent:         uint64(count),
		Run:               uint64(count),
		Action:            uint64(count),
		Result:            uint64(count),
		Approval:          uint64(count),
		Outbox:            uint64(count),
		PermissionRequest: uint64(count),
	}
	for index := 1; index <= count; index++ {
		createdAt := automationStoreTestTime.Add(time.Duration(index) * time.Hour)
		startedAt := createdAt.Add(time.Minute)
		completedAt := startedAt.Add(time.Minute)
		suffix := fmt.Sprintf("%08d", index)
		sessionID := "aisession_" + suffix
		turnID := "aiturn_" + suffix
		runID := "airun_" + suffix
		actionID := "aiact_" + suffix
		resultID := "airesult_" + suffix
		approvalID := "aiapproval_" + suffix
		outboxID := "aiout_" + suffix
		requestID := "aipermreq_" + suffix
		messageKey := fmt.Sprintf("message-%d", index)
		eventID := fmt.Sprintf("event_%08d", index)
		runKey := "run:" + messageKey
		outboxKey := "send:" + actionID

		state.Sessions = append(state.Sessions, AutomationSession{
			ID:        sessionID,
			Kind:      AutomationSessionKindAutomation,
			Title:     "Automation run " + runID,
			Status:    AutomationSessionStatusCompleted,
			Version:   1,
			CreatedAt: createdAt,
			UpdatedAt: completedAt,
		})
		state.Turns = append(state.Turns, AutomationTurn{
			ID:           turnID,
			SessionID:    sessionID,
			Status:       AutomationTurnCompleted,
			ProviderName: "test-provider",
			Version:      1,
			CreatedAt:    createdAt,
			UpdatedAt:    completedAt,
			StartedAt:    automationTimePointer(startedAt),
			CompletedAt:  automationTimePointer(completedAt),
		})
		state.TurnEvents = append(state.TurnEvents, AutomationTurnEvent{
			Sequence:  uint64(index),
			SessionID: sessionID,
			TurnID:    turnID,
			Type:      "test.event",
			Data:      json.RawMessage(`{"retained":true}`),
			CreatedAt: completedAt,
		})
		state.Runs = append(state.Runs, AutomationRun{
			ID:                     runID,
			SessionID:              sessionID,
			TurnID:                 turnID,
			IdempotencyKey:         runKey,
			EngineVersion:          AutomationEngineVersion,
			ProviderName:           "test-provider",
			SourceMessageEventID:   eventID,
			SourceMessageKey:       messageKey,
			SourceMessageVersion:   1,
			Status:                 AutomationRunSucceeded,
			Version:                1,
			Attempt:                1,
			ProviderResponseSHA256: strings.Repeat("a", 64),
			ActionIDs:              []string{actionID},
			CreatedAt:              createdAt,
			StartedAt:              automationTimePointer(startedAt),
			CompletedAt:            automationTimePointer(completedAt),
		})
		state.Actions = append(state.Actions, AutomationAction{
			ID:             actionID,
			RunID:          runID,
			ProviderCallID: fmt.Sprintf("call-%d", index),
			Tool:           AutomationToolReplyPrivateText,
			Status:         AutomationActionSucceeded,
			Risk:           AutomationRiskR2,
			PolicyDecision: AutomationDecisionReview,
			PolicyVersion:  1,
			RiskReasons:    []string{"external communication"},
			Confidence:     0.9,
			Evidence: []AutomationEvidence{{
				Source:  "message",
				Field:   "text",
				Excerpt: "hello",
			}},
			SourceMessage: AutomationSourceMessageRef{
				EventID:         eventID,
				MessageKey:      messageKey,
				ExpectedVersion: 1,
				ChatType:        ChatTypePrivate,
				PeerID:          "wxid_peer",
			},
			Preconditions:      []AutomationEntityPrecondition{},
			DependsOnActionIDs: []string{},
			Recipient: &AutomationRecipient{
				ChatType: ChatTypePrivate,
				PeerID:   "wxid_peer",
			},
			Payload:   json.RawMessage(`{"intent":"general","text":"hello"}`),
			ResultID:  resultID,
			Result:    json.RawMessage(`{"sent":true}`),
			Attempt:   1,
			Version:   1,
			CreatedAt: createdAt,
			UpdatedAt: completedAt,
		})
		state.Results = append(state.Results, AutomationResult{
			ID:                  resultID,
			ActionID:            actionID,
			PermissionRequestID: requestID,
			Status:              AutomationDeliverySent,
			Data:                json.RawMessage(`{"sent":true}`),
			CreatedAt:           completedAt,
		})
		state.Approvals = append(state.Approvals, AutomationApprovalRecord{
			ID:            approvalID,
			ActionID:      actionID,
			ActionVersion: 1,
			Decision:      "approved",
			OperatorID:    "operator-1",
			CreatedAt:     startedAt,
		})
		state.Outbox = append(state.Outbox, AutomationOutboxEntry{
			ID:                outboxID,
			ActionID:          actionID,
			IdempotencyKey:    outboxKey,
			ChatType:          ChatTypePrivate,
			PeerID:            "wxid_peer",
			Text:              "hello",
			Status:            AutomationOutboxSent,
			Attempt:           1,
			DispatchAttempted: true,
			CreatedAt:         createdAt,
			UpdatedAt:         completedAt,
			DispatchStartedAt: automationTimePointer(startedAt),
			CompletedAt:       automationTimePointer(completedAt),
		})
		state.Idempotency[runKey] = AutomationIdempotencyRecord{
			Kind:       "run",
			ResourceID: runID,
			CreatedAt:  createdAt,
		}
		state.Idempotency[outboxKey] = AutomationIdempotencyRecord{
			Kind:       "outbox",
			ResourceID: outboxID,
			CreatedAt:  createdAt,
		}
		state.PermissionRequests = append(state.PermissionRequests, AutomationPermissionRequest{
			ID:         requestID,
			RunID:      runID,
			ActionID:   actionID,
			Tool:       AutomationToolReplyPrivateText,
			Risk:       AutomationRiskR2,
			Status:     AutomationPermissionStatusAllowed,
			Version:    1,
			Decision:   AutomationPermissionAllowOnce,
			OperatorID: "operator-1",
			CreatedAt:  startedAt,
			UpdatedAt:  startedAt,
		})
	}
	return state
}

func automationStoreTestRunEvent(index int) MessageEvent {
	createdAt := automationStoreTestTime.Add(time.Duration(index) * time.Hour)
	event := MessageEvent{
		ID:           fmt.Sprintf("event_%08d", index),
		Source:       "wechat",
		Mid:          fmt.Sprintf("mid-%d", index),
		ChatType:     ChatTypePrivate,
		PeerID:       "wxid_peer",
		AuthorID:     "wxid_author",
		Text:         "hello",
		Kinds:        []string{"text"},
		Timestamp:    createdAt.Unix(),
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      1,
		CreatedAt:    createdAt,
		UpdatedAt:    createdAt,
	}
	event.MessageKey = MessageKeyForEvent(event)
	return event
}

func assertAutomationRetentionIDs[T any](
	t *testing.T,
	values []T,
	id func(T) string,
	want ...string,
) {
	t.Helper()
	got := make([]string, len(values))
	for i := range values {
		got[i] = id(values[i])
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("retained IDs = %#v, want %#v", got, want)
	}
}

func automationStoreWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := syncAutomationDirectory(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
}

func automationStoreRecoveryMarkerBytes(t *testing.T, existed bool) []byte {
	t.Helper()
	data, err := json.Marshal(automationRecoveryMarker{
		Version:         automationRecoveryMarkerVersion,
		SnapshotExisted: existed,
	})
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func automationStoreWriteRecoveryArtifacts(t *testing.T, path string, existed bool, rollback []byte) {
	t.Helper()
	rollbackPath, markerPath := automationRecoveryPaths(path)
	if err := writeAutomationDurableFileExclusive(rollbackPath, rollback); err != nil {
		t.Fatal(err)
	}
	if err := writeAutomationDurableFileExclusive(markerPath, automationStoreRecoveryMarkerBytes(t, existed)); err != nil {
		t.Fatal(err)
	}
	if err := syncAutomationDirectory(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	assertAutomationRecoveryArtifacts(t, path, existed, rollback)
}

func assertAutomationRecoveryArtifacts(t *testing.T, path string, existed bool, rollback []byte) {
	t.Helper()
	rollbackPath, markerPath := automationRecoveryPaths(path)
	assertAutomationMode(t, rollbackPath, 0o600)
	assertAutomationMode(t, markerPath, 0o600)
	gotRollback, err := os.ReadFile(rollbackPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotRollback, rollback) {
		t.Fatalf("rollback snapshot = %q, want %q", gotRollback, rollback)
	}
	markerBytes, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	var marker automationRecoveryMarker
	if err := decodeAutomationJSONObject(
		"recovery_marker",
		markerBytes,
		&marker,
		"version",
		"snapshot_existed",
	); err != nil {
		t.Fatal(err)
	}
	if marker.Version != automationRecoveryMarkerVersion || marker.SnapshotExisted != existed {
		t.Fatalf("recovery marker = %#v, want version %d existed %v", marker, automationRecoveryMarkerVersion, existed)
	}
}

func automationStoreJSONObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value = %#v, want JSON object", value)
	}
	return object
}

func automationStoreJSONArrayObject(t *testing.T, value any, index int) map[string]any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want JSON array", value)
	}
	if index < 0 || index >= len(array) {
		t.Fatalf("JSON array index %d out of range", index)
	}
	return automationStoreJSONObject(t, array[index])
}

func assertAutomationCollectionsNonNil(t *testing.T, state AutomationState) {
	t.Helper()
	switch {
	case state.Sessions == nil:
		t.Fatal("Sessions is nil")
	case state.Turns == nil:
		t.Fatal("Turns is nil")
	case state.TurnEvents == nil:
		t.Fatal("TurnEvents is nil")
	case state.Runs == nil:
		t.Fatal("Runs is nil")
	case state.Actions == nil:
		t.Fatal("Actions is nil")
	case state.Results == nil:
		t.Fatal("Results is nil")
	case state.Approvals == nil:
		t.Fatal("Approvals is nil")
	case state.Outbox == nil:
		t.Fatal("Outbox is nil")
	case state.Idempotency == nil:
		t.Fatal("Idempotency is nil")
	case state.PendingAudit == nil:
		t.Fatal("PendingAudit is nil")
	case state.PermissionRequests == nil:
		t.Fatal("PermissionRequests is nil")
	case state.PermissionRules == nil:
		t.Fatal("PermissionRules is nil")
	case state.Policy.R1AllowedTools == nil:
		t.Fatal("Policy.R1AllowedTools is nil")
	case state.Policy.R2AllowedIntents == nil:
		t.Fatal("Policy.R2AllowedIntents is nil")
	}
}

func assertAutomationMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %04o, want %04o", path, got, want)
	}
}

func assertAutomationStateAndDisk(
	t *testing.T,
	store *AutomationStore,
	path string,
	wantState AutomationState,
	wantDisk []byte,
) {
	t.Helper()
	if got := store.Snapshot(); !reflect.DeepEqual(got, wantState) {
		t.Fatalf("memory changed after failed update:\ngot  %#v\nwant %#v", got, wantState)
	}
	gotDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotDisk, wantDisk) {
		t.Fatal("persisted snapshot changed after failed update")
	}
}

func assertAutomationStatesEqual(t *testing.T, got, want AutomationState) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("automation states differ:\ngot  %s\nwant %s", gotJSON, wantJSON)
	}
}

func assertNoAutomationAtomicArtifacts(t *testing.T, parent, base string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "."+base+".tmp-") ||
			strings.HasPrefix(entry.Name(), "."+base+".restore-") ||
			entry.Name() == "."+base+".rollback" ||
			entry.Name() == "."+base+".recovery" {
			t.Fatalf("atomic artifact remained: %s", entry.Name())
		}
	}
}

func TestAutomationStoreReadNarrow(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatalf("OpenAutomationStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	if err := store.Read(nil); err == nil {
		t.Fatal("Read(nil) error = nil, want validation error")
	}

	// Read observes exactly the committed state without cloning it back.
	var session AutomationSession
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindSession)
		if err != nil {
			return err
		}
		session = AutomationSession{
			ID: id, Kind: AutomationSessionKindChat, Title: "narrow read",
			Status: AutomationSessionStatusActive, Version: 1,
			CreatedAt: automationStoreTestTime, UpdatedAt: automationStoreTestTime,
		}
		state.Sessions = append(state.Sessions, session)
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	var seen []AutomationSession
	if err := store.Read(func(state *AutomationState) error {
		for _, candidate := range state.Sessions {
			if candidate.ID == session.ID {
				seen = append(seen, candidate)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(seen) != 1 || !reflect.DeepEqual(seen[0], session) {
		t.Fatalf("Read() observed %#v, want exactly the committed session %#v", seen, session)
	}

	// Callback errors propagate, and the state is left untouched.
	probe := errors.New("read probe")
	if err := store.Read(func(*AutomationState) error { return probe }); !errors.Is(err, probe) {
		t.Fatalf("Read() callback error = %v, want %v", err, probe)
	}
	after := store.Snapshot()
	if len(after.Sessions) != 1 || !reflect.DeepEqual(after.Sessions[0], session) {
		t.Fatalf("Read() callback mutated or lost state: %#v", after.Sessions)
	}

	// A subsequent Update is visible to the next Read.
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindTurn)
		if err != nil {
			return err
		}
		state.Turns = append(state.Turns, AutomationTurn{
			ID: id, SessionID: session.ID, Status: AutomationTurnQueued,
			ProviderName: "test-provider",
			Version:      1, CreatedAt: automationStoreTestTime, UpdatedAt: automationStoreTestTime,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("second Update() error = %v", err)
	}
	turns := 0
	if err := store.Read(func(state *AutomationState) error {
		for _, turn := range state.Turns {
			if turn.SessionID == session.ID {
				turns++
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("second Read() error = %v", err)
	}
	if turns != 1 {
		t.Fatalf("Read() saw %d turns, want 1", turns)
	}
}

func TestAutomationStoreResolvesRenameRollbackWithoutMarker(t *testing.T) {
	automationStoreUnsetRetentionEnvironment(t)
	t.Run("renamed previous snapshot is restored", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		oldState := newAutomationStateAt(automationStoreTestTime)
		oldState.LastObservedCRMEventCounter = 7
		rollbackPath, _ := automationRecoveryPaths(path)
		automationStoreWriteFile(t, rollbackPath, automationStoreMarshalState(t, oldState))

		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore() error = %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		assertAutomationStatesEqual(t, store.Snapshot(), oldState)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
		assertAutomationMode(t, path, 0o600)
	})

	t.Run("empty first-write placeholder is discarded", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		rollbackPath, _ := automationRecoveryPaths(path)
		automationStoreWriteFile(t, rollbackPath, nil)

		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore() error = %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		state := store.Snapshot()
		if state.LastObservedCRMEventCounter != 0 {
			t.Fatalf("recovered watermark = %d, want fresh state", state.LastObservedCRMEventCounter)
		}
		assertAutomationCollectionsNonNil(t, state)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})

	t.Run("orphan rollback with live snapshot is cleaned up", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "state.json")
		live := newAutomationStateAt(automationStoreTestTime)
		live.LastObservedCRMEventCounter = 11
		automationStoreWriteFile(t, path, automationStoreMarshalState(t, live))
		rollbackPath, _ := automationRecoveryPaths(path)
		automationStoreWriteFile(t, rollbackPath, automationStoreMarshalState(t, live))

		store, err := OpenAutomationStore(parent)
		if err != nil {
			t.Fatalf("OpenAutomationStore() error = %v", err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})
		assertAutomationStatesEqual(t, store.Snapshot(), live)
		assertNoAutomationAtomicArtifacts(t, parent, filepath.Base(path))
	})
}
