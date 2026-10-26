package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var crmStoreTestTime = time.Date(2026, time.July, 21, 6, 0, 0, 123, time.UTC)

func TestCRMStoreDefaultStateAndPermissions(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "private")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "crm.json")

	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	state := store.Snapshot()
	if state.SchemaVersion != CRMSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", state.SchemaVersion, CRMSchemaVersion)
	}
	assertCRMStateSlicesNonNil(t, state)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("decode persisted snapshot: %v", err)
	}
	for _, field := range []string{
		"candidates",
		"jobs",
		"leads",
		"lead_stage_events",
		"onboarding_cases",
		"service_requests",
		"fund_callbacks",
		"audit_events",
		"tasks",
		"notes",
		"message_events",
		"forwarding_rules",
	} {
		value, ok := persisted[field].([]any)
		if !ok || len(value) != 0 {
			t.Errorf("persisted %s = %#v, want []", field, persisted[field])
		}
	}

	assertCRMMode(t, parent, 0o700)
	assertCRMMode(t, path, 0o600)
}

func TestCRMStoreV4DefaultsAndRecruitingSourceReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}

	state := store.Snapshot()
	if state.SchemaVersion != 4 ||
		state.RecruitingProject.OrganizationName != CRMDefaultOrganizationName ||
		state.RecruitingProject.Version != 1 ||
		!state.RecruitingProject.RequireAuthoritativeJob ||
		state.RecruitingProject.SourceGroups == nil ||
		len(state.RecruitingProject.SourceGroups) != 0 ||
		state.JobProvenance == nil ||
		len(state.JobProvenance) != 0 {
		t.Fatalf("unexpected v4 defaults: %#v", state)
	}

	cloned := store.Snapshot()
	cloned.RecruitingProject.SourceGroups = append(cloned.RecruitingProject.SourceGroups, RecruitingSourceGroup{
		ID: "source_99999999", GroupID: "mutated@chatroom", Version: 1, CreatedAt: crmStoreTestTime, UpdatedAt: crmStoreTestTime,
	})
	if got := store.Snapshot().RecruitingProject.SourceGroups; len(got) != 0 {
		t.Fatalf("snapshot clone mutation changed store source groups: %#v", got)
	}

	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:     "contract-test",
		Operation: "recruiting.source.persist",
	}, func(state *CRMState, allocator *IDAllocator) error {
		sourceID, err := allocator.Next(IDKindSourceGroup)
		if err != nil {
			return err
		}
		eventID, err := allocator.Next(IDKindEvent)
		if err != nil {
			return err
		}
		jobID, err := allocator.Next(IDKindJob)
		if err != nil {
			return err
		}
		provenanceID, err := allocator.Next(IDKindJobProvenance)
		if err != nil {
			return err
		}

		state.RecruitingProject.Version = 2
		state.RecruitingProject.SourceGroups = append(state.RecruitingProject.SourceGroups, RecruitingSourceGroup{
			ID:          sourceID,
			GroupID:     "synthetic_jobs@chatroom",
			DisplayName: "Synthetic Jobs",
			Enabled:     true,
			Version:     1,
			CreatedAt:   crmStoreTestTime,
			UpdatedAt:   crmStoreTestTime,
		})
		job := crmStoreTestJob(jobID)
		job.Title = "Fictional Line Operator"
		job.DemandCode = "SYN-LINE-001"
		state.Jobs = append(state.Jobs, job)
		event := crmStoreTestGroupEvent(eventID, "mid-v4-default", "synthetic_jobs@chatroom", MessageDispositionSourceGroupJob)
		state.MessageEvents = append(state.MessageEvents, event)
		provenance := crmStoreTestJobProvenance(provenanceID, jobID, event, JobIngestionCreate)
		provenance.DemandCode = job.DemandCode
		state.JobProvenance = append(state.JobProvenance, provenance)
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateWithMeta() error = %v", err)
	}

	want := store.Snapshot()
	if len(want.RecruitingProject.SourceGroups) != 1 ||
		want.RecruitingProject.SourceGroups[0].ID != "source_00000001" ||
		want.Counters.JobProvenance != 1 ||
		len(want.JobProvenance) != 1 ||
		want.JobProvenance[0].ID != "provenance_00000001" ||
		want.JobProvenance[0].Decision != JobIngestionCreate ||
		want.JobProvenance[0].JobRevision != 1 {
		t.Fatalf("unexpected persisted v4 recruiting state: %#v", want)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if got := reopened.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened snapshot = %#v, want %#v", got, want)
	}
}

func TestCRMStoreDeterministicIDsAndFailureRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	callbackErr := errors.New("callback failed")
	var leaked *IDAllocator
	err = store.Update(func(_ *CRMState, allocator *IDAllocator) error {
		leaked = allocator
		id, nextErr := allocator.Next(IDKindCandidate)
		if nextErr != nil {
			t.Fatalf("Next(candidate) error = %v", nextErr)
		}
		if id != "cand_00000001" {
			t.Fatalf("Next(candidate) = %q, want cand_00000001", id)
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("Update(callback failure) error = %v, want %v", err, callbackErr)
	}
	if _, err := leaked.Next(IDKindCandidate); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("Next() after callback error = %v, want validation error", err)
	}
	if got := store.Snapshot().Counters.Candidate; got != 0 {
		t.Fatalf("candidate counter after callback failure = %d, want 0", got)
	}

	originalPath := store.path
	originalDisk, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.path = filepath.Join(blockingFile, "crm.json")
	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, nextErr := allocator.Next(IDKindCandidate)
		if nextErr != nil {
			return nextErr
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "failed"))
		return nil
	})
	store.path = originalPath
	if !errors.Is(err, ErrCRMPersistence) {
		t.Fatalf("Update(persistence failure) error = %v, want persistence error", err)
	}
	if got := store.Snapshot(); got.Counters.Candidate != 0 || len(got.Candidates) != 0 {
		t.Fatalf("memory changed after persistence failure: %+v", got)
	}
	afterFailureDisk, err := os.ReadFile(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterFailureDisk, originalDisk) {
		t.Fatal("persisted snapshot changed after persistence failure")
	}

	kinds := []struct {
		kind IDKind
		want string
	}{
		{IDKindCandidate, "cand_00000001"},
		{IDKindJob, "job_00000001"},
		{IDKindLead, "lead_00000001"},
		{IDKindTask, "task_00000001"},
		{IDKindNote, "note_00000001"},
		{IDKindEvent, "event_00000001"},
		{IDKindRule, "rule_00000001"},
		{IDKindStageEvent, "stage_00000001"},
		{IDKindOnboarding, "onboard_00000001"},
		{IDKindServiceRequest, "service_00000001"},
		{IDKindFundCallback, "fund_00000001"},
	}
	err = store.Update(func(_ *CRMState, allocator *IDAllocator) error {
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
	if _, err := leaked.Next(IDKindCandidate); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("leaked allocator Next() error = %v, want validation error", err)
	}
	state := store.Snapshot()
	if state.Counters.Audit != 1 || len(state.AuditEvents) != 1 {
		t.Fatalf("automatic audit state = %d/%d, want 1/1", state.Counters.Audit, len(state.AuditEvents))
	}
}

func TestCRMStorePostRenameDirectorySyncRollback(t *testing.T) {
	syncErr := errors.New("injected directory sync failure")

	t.Run("existing snapshot", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "crm.json")
		store, err := OpenCRMStore(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
			return nil
		})
		if err != nil {
			t.Fatalf("seed Update() error = %v", err)
		}

		beforeState := store.Snapshot()
		beforeBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		beforeInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}

		syncCalls := 0
		store.syncDirectory = func(directory string) error {
			syncCalls++
			if syncCalls == 1 {
				return syncErr
			}
			return syncCRMDirectory(directory)
		}
		err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			if id != "cand_00000002" {
				return fmt.Errorf("Next(candidate) = %q, want cand_00000002", id)
			}
			state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Grace"))
			return nil
		})
		if !errors.Is(err, syncErr) || !errors.Is(err, ErrCRMPersistence) {
			t.Fatalf("Update() error = %v, want injected persistence error", err)
		}
		if syncCalls != 2 {
			t.Fatalf("directory sync calls = %d, want 2", syncCalls)
		}

		afterBytes, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(afterBytes, beforeBytes) {
			t.Fatal("rollback did not restore prior snapshot bytes")
		}
		afterInfo, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
			t.Fatalf("snapshot mode after rollback = %04o, want prior mode %04o", afterInfo.Mode().Perm(), beforeInfo.Mode().Perm())
		}
		if got := store.Snapshot(); !reflect.DeepEqual(got, beforeState) {
			t.Fatalf("memory after rollback = %#v, want %#v", got, beforeState)
		}
		if got := store.Snapshot().Counters.Candidate; got != 1 {
			t.Fatalf("candidate counter after rollback = %d, want 1", got)
		}
		assertNoCRMAtomicArtifacts(t, parent, filepath.Base(path))

		store.syncDirectory = syncCRMDirectory
		err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			if id != "cand_00000002" {
				return fmt.Errorf("Next(candidate) after rollback = %q, want cand_00000002", id)
			}
			state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Grace"))
			return nil
		})
		if err != nil {
			t.Fatalf("Update() after rollback error = %v", err)
		}
	})

	t.Run("initially missing snapshot", func(t *testing.T) {
		parent := t.TempDir()
		path := filepath.Join(parent, "crm.json")
		beforeState := newCRMState()
		syncCalls := 0
		store := &CRMStore{
			path:  path,
			state: cloneCRMState(beforeState),
			syncDirectory: func(directory string) error {
				syncCalls++
				if syncCalls == 1 {
					return syncErr
				}
				return syncCRMDirectory(directory)
			},
		}
		t.Cleanup(func() {
			_ = store.Close()
		})

		err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
			return nil
		})
		if !errors.Is(err, syncErr) || !errors.Is(err, ErrCRMPersistence) {
			t.Fatalf("Update() error = %v, want injected persistence error", err)
		}
		if syncCalls != 2 {
			t.Fatalf("directory sync calls = %d, want 2", syncCalls)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("snapshot after rollback stat error = %v, want not exist", err)
		}
		if got := store.Snapshot(); !reflect.DeepEqual(got, beforeState) {
			t.Fatalf("memory after rollback = %#v, want %#v", got, beforeState)
		}
		if got := store.Snapshot().Counters.Candidate; got != 0 {
			t.Fatalf("candidate counter after rollback = %d, want 0", got)
		}
		assertNoCRMAtomicArtifacts(t, parent, filepath.Base(path))

		store.syncDirectory = syncCRMDirectory
		err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			if id != "cand_00000001" {
				return fmt.Errorf("Next(candidate) after rollback = %q, want cand_00000001", id)
			}
			state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
			return nil
		})
		if err != nil {
			t.Fatalf("Update() after rollback error = %v", err)
		}
	})
}

func TestCRMStoreCallbackPanicDeactivatesAllocator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
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
	var leaked *IDAllocator
	var allocatedID string
	var allocatedErr error
	var recovered any

	func() {
		defer func() {
			recovered = recover()
		}()
		_ = store.Update(func(_ *CRMState, allocator *IDAllocator) error {
			leaked = allocator
			allocatedID, allocatedErr = allocator.Next(IDKindCandidate)
			panic(panicValue)
		})
		t.Fatal("Update() returned instead of propagating callback panic")
	}()

	if recovered != panicValue {
		t.Fatalf("recovered panic = %#v, want original panic %#v", recovered, panicValue)
	}
	if allocatedErr != nil || allocatedID != "cand_00000001" {
		t.Fatalf("allocation before panic = %q, %v; want cand_00000001, nil", allocatedID, allocatedErr)
	}
	if leaked == nil {
		t.Fatal("callback did not retain allocator")
	}
	if _, err := leaked.Next(IDKindCandidate); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("leaked allocator Next() error = %v, want inactive allocator validation error", err)
	}
	if got := store.Snapshot(); !reflect.DeepEqual(got, beforeState) {
		t.Fatalf("memory after panic = %#v, want %#v", got, beforeState)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(afterBytes, beforeBytes) {
		t.Fatal("snapshot bytes changed after callback panic")
	}

	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		if id != "cand_00000001" {
			return fmt.Errorf("Next(candidate) after panic = %q, want cand_00000001", id)
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
		return nil
	})
	if err != nil {
		t.Fatalf("Update() after panic error = %v", err)
	}
}

func TestCRMStoreRestartRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}

	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	want := store.Snapshot()
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	if got := reopened.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened snapshot = %#v, want %#v", got, want)
	}

	err = reopened.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		if id != "cand_00000002" {
			return fmt.Errorf("recovered Next(candidate) = %q, want cand_00000002", id)
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Grace"))
		return nil
	})
	if err != nil {
		t.Fatalf("Update() after restart error = %v", err)
	}
}

// TestCRMStoreRecoverRollbackSnapshotOnOpen covers F1-FIX: a write that
// crashed after renaming the previous snapshot aside (writeSnapshotBytesAtomic)
// but before replacing it leaves the old data in .<base>.rollback with no main
// snapshot. OpenCRMStore must recover that data instead of creating an empty
// store.
func TestCRMStoreRecoverRollbackSnapshotOnOpen(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "crm.json")
	base := filepath.Base(path)
	rollbackPath := filepath.Join(parent, "."+base+".rollback")

	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
		return nil
	})
	if err != nil {
		t.Fatalf("seed Update() error = %v", err)
	}
	want := store.Snapshot()
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Simulate the crash window: the previous snapshot is renamed aside and
	// the main file is gone, only .<base>.rollback remains.
	if err := os.Rename(path, rollbackPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("main snapshot exists after rename-aside: %v", err)
	}
	if _, err := os.Lstat(rollbackPath); err != nil {
		t.Fatalf("rollback snapshot missing after rename-aside: %v", err)
	}

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore() after rename-aside error = %v, want recovery", err)
	}
	t.Cleanup(func() {
		_ = reopened.Close()
	})
	if got := reopened.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered snapshot = %#v, want %#v", got, want)
	}
	if got := reopened.Snapshot().Counters.Candidate; got != 1 {
		t.Fatalf("recovered candidate counter = %d, want 1", got)
	}
	// Recovery consumes the rollback (renames it back to the main path).
	if _, err := os.Lstat(rollbackPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback snapshot remains after recovery: %v", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		t.Fatalf("recovered snapshot mode = %v, want regular file", info.Mode())
	}
	assertCRMMode(t, path, 0o600)

	// The recovered store must continue allocating from the recovered
	// counters instead of restarting from an empty store.
	err = reopened.Update(func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		if id != "cand_00000002" {
			return fmt.Errorf("Next(candidate) after recovery = %q, want cand_00000002", id)
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Grace"))
		return nil
	})
	if err != nil {
		t.Fatalf("Update() after recovery error = %v", err)
	}
	assertNoCRMAtomicArtifacts(t, parent, base)
}

// TestCRMStoreOpenCorruptRollbackReturnsErrorWithoutOverwrite covers F1-FIX
// error path: when only .<base>.rollback exists and its content is corrupt,
// OpenCRMStore must surface the decode failure and must not silently create an
// empty store that would hide the crash-window data.
func TestCRMStoreOpenCorruptRollbackReturnsErrorWithoutOverwrite(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "crm.json")
	base := filepath.Base(path)
	rollbackPath := filepath.Join(parent, "."+base+".rollback")

	corrupt := []byte(`{"schema_version":`)
	if err := os.WriteFile(rollbackPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("main snapshot unexpectedly exists: %v", err)
	}

	if store, err := OpenCRMStore(path); err == nil {
		_ = store.Close()
		t.Fatal("OpenCRMStore() succeeded with corrupt rollback, want error")
	}

	// Recovery must not lose or overwrite the crash-window data: the corrupt
	// bytes stay at the main path (no empty store replaced them).
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, corrupt) {
		t.Fatalf("snapshot after corrupt rollback = %q, want preserved corrupt bytes %q", got, corrupt)
	}
	// The rollback was consumed by recovery (renamed to the main path).
	if _, err := os.Lstat(rollbackPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback snapshot remains after failed open: %v", err)
	}
}

// TestCRMStoreOpenMissingPathWithoutRollbackCreatesEmptyStore guards the
// first-write path (F1-FIX must not change it): with no snapshot and no
// rollback, OpenCRMStore still creates a fresh empty store.
func TestCRMStoreOpenMissingPathWithoutRollbackCreatesEmptyStore(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "crm.json")
	base := filepath.Base(path)
	rollbackPath := filepath.Join(parent, "."+base+".rollback")

	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("main snapshot unexpectedly exists: %v", err)
	}
	if _, err := os.Lstat(rollbackPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback snapshot unexpectedly exists: %v", err)
	}

	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatalf("OpenCRMStore() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	state := store.Snapshot()
	if state.SchemaVersion != CRMSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", state.SchemaVersion, CRMSchemaVersion)
	}
	if len(state.Candidates) != 0 || state.Counters.Candidate != 0 {
		t.Fatalf("new store candidate state = %d/%d, want 0/0", len(state.Candidates), state.Counters.Candidate)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("main snapshot not created: %v", err)
	}
	assertNoCRMAtomicArtifacts(t, parent, base)
	if _, err := os.Lstat(rollbackPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback snapshot created by first open: %v", err)
	}
}

// TestCRMStoreSuccessfulWriteLeavesNoRollbackArtifacts guards the F1-FIX
// write path: after every successful update neither the fixed-name rollback
// (.<base>.rollback, not covered by assertNoCRMAtomicArtifacts' dash-suffixed
// patterns) nor any temporary snapshot may remain.
func TestCRMStoreSuccessfulWriteLeavesNoRollbackArtifacts(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "crm.json")
	base := filepath.Base(path)
	rollbackPath := filepath.Join(parent, "."+base+".rollback")

	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	for i := 0; i < 3; i++ {
		err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
			id, err := allocator.Next(IDKindCandidate)
			if err != nil {
				return err
			}
			state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, fmt.Sprintf("Candidate %d", i+1)))
			return nil
		})
		if err != nil {
			t.Fatalf("Update() #%d error = %v", i+1, err)
		}
		if _, err := os.Lstat(rollbackPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollback snapshot remains after successful write #%d: %v", i+1, err)
		}
	}
	assertNoCRMAtomicArtifacts(t, parent, base)
}

func TestCRMStoreRejectsInvalidSnapshotsWithoutRewrite(t *testing.T) {
	unsupported := newCRMState()
	unsupported.SchemaVersion = CRMSchemaVersion + 1
	unsupportedJSON, err := json.Marshal(unsupported)
	if err != nil {
		t.Fatal(err)
	}
	invalidReference := newCRMState()
	invalidReference.Counters.Lead = 1
	invalidReference.Leads = append(invalidReference.Leads, crmStoreTestLead("lead_00000001", "cand_00000001", "job_00000001"))
	invalidReferenceJSON, err := json.Marshal(invalidReference)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: []byte{}},
		{name: "whitespace", data: []byte(" \n\t")},
		{name: "corrupt", data: []byte(`{"schema_version":`)},
		{name: "unsupported schema", data: unsupportedJSON},
		{name: "invalid reference", data: invalidReferenceJSON},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "crm.json")
			if err := os.WriteFile(path, test.data, 0o644); err != nil {
				t.Fatal(err)
			}

			if store, err := OpenCRMStore(path); err == nil {
				_ = store.Close()
				t.Fatal("OpenCRMStore() succeeded, want error")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.data) {
				t.Fatalf("snapshot was rewritten: got %q, want %q", got, test.data)
			}
		})
	}
}

func TestCRMValidateStateRejectsInvalidRelationshipsAndCounters(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CRMState)
	}{
		{
			name: "nil top-level slice",
			mutate: func(state *CRMState) {
				state.Candidates = nil
			},
		},
		{
			name: "missing lead candidate",
			mutate: func(state *CRMState) {
				state.Counters.Lead = 1
				state.Leads = append(state.Leads, crmStoreTestLead("lead_00000001", "cand_00000001", "job_00000001"))
			},
		},
		{
			name: "duplicate entity ID",
			mutate: func(state *CRMState) {
				state.Counters.Candidate = 1
				candidate := crmStoreTestCandidate("cand_00000001", "Ada")
				state.Candidates = append(state.Candidates, candidate, candidate)
			},
		},
		{
			name: "duplicate message key",
			mutate: func(state *CRMState) {
				state.Counters.Event = 2
				first := crmStoreTestEvent("event_00000001", "mid-1")
				second := crmStoreTestEvent("event_00000002", "mid-1")
				state.MessageEvents = append(state.MessageEvents, first, second)
			},
		},
		{
			name: "duplicate active candidate job pair",
			mutate: func(state *CRMState) {
				crmStoreTestAddCandidateAndJob(state)
				state.Counters.Lead = 2
				state.Leads = append(
					state.Leads,
					crmStoreTestLead("lead_00000001", "cand_00000001", "job_00000001"),
					crmStoreTestLead("lead_00000002", "cand_00000001", "job_00000001"),
				)
			},
		},
		{
			name: "task candidate disagrees with lead",
			mutate: func(state *CRMState) {
				crmStoreTestAddCandidateAndJob(state)
				state.Counters.Candidate = 2
				state.Candidates = append(state.Candidates, crmStoreTestCandidate("cand_00000002", "Grace"))
				state.Counters.Lead = 1
				state.Leads = append(state.Leads, crmStoreTestLead("lead_00000001", "cand_00000001", "job_00000001"))
				state.Counters.Task = 1
				state.Tasks = append(state.Tasks, crmStoreTestTask("task_00000001", "cand_00000002", "lead_00000001"))
			},
		},
		{
			name: "note target missing",
			mutate: func(state *CRMState) {
				state.Counters.Note = 1
				state.Notes = append(state.Notes, Note{
					ID:         "note_00000001",
					TargetType: NoteTargetCandidate,
					TargetID:   "cand_00000001",
					Body:       "missing",
					CreatedAt:  crmStoreTestTime,
				})
			},
		},
		{
			name: "message candidate missing",
			mutate: func(state *CRMState) {
				state.Counters.Event = 1
				event := crmStoreTestEvent("event_00000001", "mid-1")
				event.CandidateID = "cand_00000001"
				event.ReviewStatus = ReviewStatusReviewed
				state.MessageEvents = append(state.MessageEvents, event)
			},
		},
		{
			name: "source event candidate disagreement",
			mutate: func(state *CRMState) {
				state.Counters.Candidate = 2
				first := crmStoreTestCandidate("cand_00000001", "Ada")
				first.SourceEventID = "event_00000001"
				state.Candidates = append(state.Candidates, first, crmStoreTestCandidate("cand_00000002", "Grace"))
				state.Counters.Event = 1
				event := crmStoreTestEvent("event_00000001", "mid-1")
				event.CandidateID = "cand_00000002"
				event.ReviewStatus = ReviewStatusReviewed
				state.MessageEvents = append(state.MessageEvents, event)
			},
		},
		{
			name: "counter behind allocated ID",
			mutate: func(state *CRMState) {
				state.Candidates = append(state.Candidates, crmStoreTestCandidate("cand_00000001", "Ada"))
			},
		},
		{
			name: "counter exceeds ID range",
			mutate: func(state *CRMState) {
				state.Counters.Rule = crmMaxIDCounter + 1
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newCRMState()
			test.mutate(&state)
			if err := ValidateCRMState(&state); !errors.Is(err, ErrCRMValidation) {
				t.Fatalf("ValidateCRMState() error = %v, want validation error", err)
			}
		})
	}
}

func TestCRMTransitionValidation(t *testing.T) {
	tests := []struct {
		name     string
		validate func() error
		wantErr  error
	}{
		{
			name:     "candidate active to paused",
			validate: func() error { return ValidateCandidateStatusTransition(CandidateStatusActive, CandidateStatusPaused) },
		},
		{
			name:     "candidate archived to active",
			validate: func() error { return ValidateCandidateStatusTransition(CandidateStatusArchived, CandidateStatusActive) },
			wantErr:  ErrCRMConflict,
		},
		{
			name:     "job closed to open",
			validate: func() error { return ValidateJobStatusTransition(JobStatusClosed, JobStatusOpen) },
		},
		{
			name:     "job archived to closed",
			validate: func() error { return ValidateJobStatusTransition(JobStatusArchived, JobStatusClosed) },
			wantErr:  ErrCRMConflict,
		},
		{
			name:     "lead interview to offer",
			validate: func() error { return ValidateLeadStageTransition(LeadStageInterview, LeadStageOffer) },
		},
		{
			name:     "lead sourced to offer",
			validate: func() error { return ValidateLeadStageTransition(LeadStageSourced, LeadStageOffer) },
			wantErr:  ErrCRMConflict,
		},
		{
			name:     "lead terminal has no outgoing transition",
			validate: func() error { return ValidateLeadStageTransition(LeadStageHired, LeadStageRejected) },
			wantErr:  ErrCRMConflict,
		},
		{
			name:     "task open to completed",
			validate: func() error { return ValidateFollowUpStatusTransition(FollowUpStatusOpen, FollowUpStatusCompleted) },
		},
		{
			name:     "task cannot remain open",
			validate: func() error { return ValidateFollowUpStatusTransition(FollowUpStatusOpen, FollowUpStatusOpen) },
			wantErr:  ErrCRMConflict,
		},
		{
			name: "invalid candidate status",
			validate: func() error {
				return ValidateCandidateStatusTransition(CandidateStatus("invalid"), CandidateStatusActive)
			},
			wantErr: ErrCRMValidation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.validate()
			if test.wantErr == nil && err != nil {
				t.Fatalf("transition error = %v, want nil", err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("transition error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestCRMStoreSnapshotDeepCopyIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	completedAt := crmStoreTestTime.Add(time.Hour)
	err = store.Update(func(state *CRMState, allocator *IDAllocator) error {
		for _, kind := range []IDKind{
			IDKindCandidate,
			IDKindJob,
			IDKindLead,
			IDKindTask,
			IDKindEvent,
			IDKindRule,
		} {
			if _, err := allocator.Next(kind); err != nil {
				return err
			}
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate("cand_00000001", "Ada"))
		state.Jobs = append(state.Jobs, crmStoreTestJob("job_00000001"))
		state.Leads = append(state.Leads, crmStoreTestLead("lead_00000001", "cand_00000001", "job_00000001"))
		task := crmStoreTestTask("task_00000001", "cand_00000001", "lead_00000001")
		task.Status = FollowUpStatusCompleted
		task.UpdatedAt = completedAt
		task.CompletedAt = &completedAt
		state.Tasks = append(state.Tasks, task)
		state.MessageEvents = append(state.MessageEvents, crmStoreTestEvent("event_00000001", "mid-1"))
		state.ForwardingRules = append(state.ForwardingRules, ForwardRule{
			ID:       "rule_00000001",
			GroupID:  "group",
			Keywords: []string{"golang"},
			Enabled:  true,
		})
		return nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	snapshot := store.Snapshot()
	snapshot.Candidates[0].FullName = "mutated"
	snapshot.Candidates[0].Skills[0] = "mutated"
	snapshot.Jobs[0].Requirements[0] = "mutated"
	snapshot.Tasks[0].CompletedAt = nil
	snapshot.MessageEvents[0].Kinds[0] = "mutated"
	snapshot.ForwardingRules[0].Keywords[0] = "mutated"
	snapshot.Candidates = append(snapshot.Candidates, crmStoreTestCandidate("cand_00000002", "extra"))

	got := store.Snapshot()
	if got.Candidates[0].FullName != "Ada" || got.Candidates[0].Skills[0] != "go" {
		t.Fatalf("candidate leaked through snapshot clone: %+v", got.Candidates[0])
	}
	if got.Jobs[0].Requirements[0] != "standard library" {
		t.Fatalf("job requirements leaked through snapshot clone: %+v", got.Jobs[0].Requirements)
	}
	if got.Tasks[0].CompletedAt == nil || !got.Tasks[0].CompletedAt.Equal(completedAt) {
		t.Fatalf("task completion leaked through snapshot clone: %+v", got.Tasks[0].CompletedAt)
	}
	if got.MessageEvents[0].Kinds[0] != "text" {
		t.Fatalf("message kinds leaked through snapshot clone: %+v", got.MessageEvents[0].Kinds)
	}
	if got.ForwardingRules[0].Keywords[0] != "golang" {
		t.Fatalf("forwarding keywords leaked through snapshot clone: %+v", got.ForwardingRules[0].Keywords)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidate slice length = %d, want 1", len(got.Candidates))
	}
}

func TestCRMStoreCloseRejectsUpdates(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
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
	err = store.Update(func(_ *CRMState, _ *IDAllocator) error {
		called.Store(true)
		return nil
	})
	if !errors.Is(err, ErrCRMStoreClosed) {
		t.Fatalf("Update() after Close error = %v, want ErrCRMStoreClosed", err)
	}
	if called.Load() {
		t.Fatal("Update callback ran after Close")
	}
	assertCRMStateSlicesNonNil(t, store.Snapshot())
}

func TestCRMStoreConcurrentUpdates(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	const updates = 24
	start := make(chan struct{})
	errs := make(chan error, updates)
	var writers sync.WaitGroup
	for i := 0; i < updates; i++ {
		writers.Add(1)
		go func(index int) {
			defer writers.Done()
			<-start
			errs <- store.Update(func(state *CRMState, allocator *IDAllocator) error {
				id, err := allocator.Next(IDKindCandidate)
				if err != nil {
					return err
				}
				state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, fmt.Sprintf("candidate-%02d", index)))
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
					if snapshot.Candidates == nil {
						errs <- errors.New("Snapshot returned nil candidates")
						return
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
	if state.Counters.Candidate != updates {
		t.Fatalf("candidate counter = %d, want %d", state.Counters.Candidate, updates)
	}
	if len(state.Candidates) != updates {
		t.Fatalf("candidate count = %d, want %d", len(state.Candidates), updates)
	}
	ids := make([]string, 0, updates)
	for _, candidate := range state.Candidates {
		ids = append(ids, candidate.ID)
	}
	sort.Strings(ids)
	for i, id := range ids {
		want := fmt.Sprintf("cand_%08d", i+1)
		if id != want {
			t.Fatalf("sorted candidate ID %d = %q, want %q", i, id, want)
		}
	}
}

func TestCRMStoreUpdateWithMetaWritesAtomicAudit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "operator",
		Operation:  "candidate.create",
		EntityType: "candidate",
		EntityID:   "cand_00000001",
	}, func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindCandidate)
		if err != nil {
			return err
		}
		state.Candidates = append(state.Candidates, crmStoreTestCandidate(id, "Ada"))
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateWithMeta() error = %v", err)
	}

	state := store.Snapshot()
	if state.Counters.Candidate != 1 || state.Counters.Audit != 1 {
		t.Fatalf("counters = %+v, want candidate=1 audit=1", state.Counters)
	}
	if len(state.AuditEvents) != 1 {
		t.Fatalf("audit events = %d, want 1", len(state.AuditEvents))
	}
	audit := state.AuditEvents[0]
	if audit.ID != "audit_00000001" ||
		audit.Actor != "operator" ||
		audit.Operation != "candidate.create" ||
		audit.EntityType != "candidate" ||
		audit.EntityID != "cand_00000001" {
		t.Fatalf("unexpected audit event: %#v", audit)
	}

	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, migrated, err := decodeCRMStateWithMigration(persisted)
	if err != nil {
		t.Fatalf("decode persisted state: %v", err)
	}
	if migrated || !reflect.DeepEqual(decoded, state) {
		t.Fatalf("persisted state mismatch migrated=%v got=%#v want=%#v", migrated, decoded, state)
	}

	before := store.Snapshot()
	callbackErr := errors.New("no mutation")
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor: "operator", Operation: "candidate.update",
	}, func(*CRMState, *IDAllocator) error {
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("failed UpdateWithMeta() error = %v, want %v", err, callbackErr)
	}
	if got := store.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed update changed state: %#v", got)
	}
}

func TestCRMStoreV2CloneIsolation(t *testing.T) {
	startAt := crmStoreTestTime.Add(time.Hour)
	deadlineAt := startAt.Add(8 * time.Hour)
	entryAt := startAt.Add(24 * time.Hour)
	pickupAt := entryAt.Add(-time.Hour)
	dueAt := entryAt.Add(2 * time.Hour)
	resolvedAt := dueAt.Add(time.Hour)
	commitmentAt := crmStoreTestTime.Add(time.Hour)
	finalAt := commitmentAt.Add(time.Hour)

	state := newCRMState()
	state.Jobs = append(state.Jobs, Job{
		StartAt:           &startAt,
		DeadlineAt:        &deadlineAt,
		Requirements:      []string{"Go"},
		RequiredDocuments: []string{"ID"},
	})
	state.OnboardingCases = append(state.OnboardingCases, OnboardingCase{
		EntryAt:            &entryAt,
		RequiredDocuments:  []string{"ID"},
		CompletedDocuments: []string{"ID"},
		MissingInformation: []string{"phone"},
		Fleet: FleetCoordination{
			PickupAt: &pickupAt,
		},
	})
	state.ServiceRequests = append(state.ServiceRequests, ServiceRequest{
		DueAt:      &dueAt,
		ResolvedAt: &resolvedAt,
	})
	state.FundCallbacks = append(state.FundCallbacks, FundCallback{
		DueAt:            &dueAt,
		CommitmentAt:     &commitmentAt,
		FinalConfirmedAt: &finalAt,
	})

	cloned := cloneCRMState(state)
	cloned.Jobs[0].Requirements[0] = "Rust"
	cloned.Jobs[0].RequiredDocuments[0] = "Passport"
	*cloned.Jobs[0].StartAt = startAt.Add(time.Hour)
	cloned.OnboardingCases[0].CompletedDocuments[0] = "Passport"
	*cloned.OnboardingCases[0].Fleet.PickupAt = pickupAt.Add(time.Hour)
	*cloned.ServiceRequests[0].ResolvedAt = resolvedAt.Add(time.Hour)
	*cloned.FundCallbacks[0].FinalConfirmedAt = finalAt.Add(time.Hour)

	if state.Jobs[0].Requirements[0] != "Go" ||
		state.Jobs[0].RequiredDocuments[0] != "ID" ||
		!state.Jobs[0].StartAt.Equal(startAt) ||
		state.OnboardingCases[0].CompletedDocuments[0] != "ID" ||
		!state.OnboardingCases[0].Fleet.PickupAt.Equal(pickupAt) ||
		!state.ServiceRequests[0].ResolvedAt.Equal(resolvedAt) ||
		!state.FundCallbacks[0].FinalConfirmedAt.Equal(finalAt) {
		t.Fatal("v2 nested data leaked through cloneCRMState")
	}
}

func crmStoreTestCandidate(id, name string) Candidate {
	return Candidate{
		ID:        id,
		FullName:  name,
		Skills:    []string{"go"},
		Status:    CandidateStatusActive,
		Version:   1,
		CreatedAt: crmStoreTestTime,
		UpdatedAt: crmStoreTestTime,
	}
}

func crmStoreTestJob(id string) Job {
	return Job{
		ID:                id,
		Title:             "Backend Engineer",
		Company:           "Example",
		Requirements:      []string{"standard library"},
		Priority:          JobPriorityNormal,
		RequiredDocuments: []string{},
		Status:            JobStatusOpen,
		Version:           1,
		CreatedAt:         crmStoreTestTime,
		UpdatedAt:         crmStoreTestTime,
	}
}

func crmStoreTestLead(id, candidateID, jobID string) Lead {
	return Lead{
		ID:             id,
		CandidateID:    candidateID,
		JobID:          jobID,
		Stage:          LeadStageSourced,
		Version:        1,
		CreatedAt:      crmStoreTestTime,
		UpdatedAt:      crmStoreTestTime,
		StageChangedAt: crmStoreTestTime,
	}
}

func crmStoreTestTask(id, candidateID, leadID string) FollowUpTask {
	return FollowUpTask{
		ID:          id,
		Title:       "Follow up",
		Status:      FollowUpStatusOpen,
		DueAt:       crmStoreTestTime.Add(24 * time.Hour),
		CandidateID: candidateID,
		LeadID:      leadID,
		Version:     1,
		CreatedAt:   crmStoreTestTime,
		UpdatedAt:   crmStoreTestTime,
	}
}

func crmStoreTestEvent(id, mid string) MessageEvent {
	event := MessageEvent{
		ID:           id,
		Source:       "wechat",
		Mid:          mid,
		ChatType:     ChatTypePrivate,
		PeerID:       "peer",
		AuthorID:     "author",
		Text:         "hello",
		Kinds:        []string{"text"},
		Timestamp:    1,
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      1,
		CreatedAt:    crmStoreTestTime,
		UpdatedAt:    crmStoreTestTime,
	}
	event.MessageKey = MessageKeyForEvent(event)
	return event
}

func crmStoreTestGroupEvent(id, mid, groupID string, disposition MessageDisposition) MessageEvent {
	event := crmStoreTestEvent(id, mid)
	event.ChatType = ChatTypeGroup
	event.PeerID = groupID
	event.AuthorID = "fictional_author"
	event.GroupID = groupID
	event.Text = "fictional job demand"
	event.Disposition = disposition
	event.MessageKey = MessageKeyForEvent(event)
	return event
}

func crmStoreTestJobProvenance(id, jobID string, event MessageEvent, decision JobIngestionDecision) JobProvenance {
	return JobProvenance{
		ID:                   id,
		JobID:                jobID,
		EventID:              event.ID,
		MessageKey:           event.MessageKey,
		GroupID:              event.GroupID,
		AuthorID:             event.AuthorID,
		SourceTimestamp:      crmStoreTestTime,
		ContentSHA256:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		CanonicalFingerprint: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Decision:             decision,
		JobRevision:          1,
		CreatedAt:            crmStoreTestTime,
	}
}

func crmStoreTestAddCandidateAndJob(state *CRMState) {
	state.Counters.Candidate = 1
	state.Candidates = append(state.Candidates, crmStoreTestCandidate("cand_00000001", "Ada"))
	state.Counters.Job = 1
	state.Jobs = append(state.Jobs, crmStoreTestJob("job_00000001"))
}

func assertCRMStateSlicesNonNil(t *testing.T, state CRMState) {
	t.Helper()
	values := []struct {
		name  string
		value any
	}{
		{"Candidates", state.Candidates},
		{"Jobs", state.Jobs},
		{"Leads", state.Leads},
		{"LeadStageEvents", state.LeadStageEvents},
		{"OnboardingCases", state.OnboardingCases},
		{"ServiceRequests", state.ServiceRequests},
		{"FundCallbacks", state.FundCallbacks},
		{"AuditEvents", state.AuditEvents},
		{"Tasks", state.Tasks},
		{"Notes", state.Notes},
		{"MessageEvents", state.MessageEvents},
		{"ForwardingRules", state.ForwardingRules},
	}
	for _, value := range values {
		if reflect.ValueOf(value.value).IsNil() {
			t.Errorf("%s is nil", value.name)
		}
	}
}

func assertCRMMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %04o, want %04o", path, got, want)
	}
}

func assertNoCRMAtomicArtifacts(t *testing.T, parent, base string) {
	t.Helper()
	for _, pattern := range []string{
		filepath.Join(parent, "."+base+".tmp-*"),
		filepath.Join(parent, "."+base+".rollback-*"),
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			t.Errorf("atomic snapshot artifacts remain for %q: %v", pattern, matches)
		}
	}
}
