package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type automationAuditTestAppendFile struct {
	*os.File
	writeFunc func([]byte) (int, error)
	syncFunc  func() error
	closeFunc func() error
}

func (f *automationAuditTestAppendFile) Write(data []byte) (int, error) {
	if f.writeFunc != nil {
		return f.writeFunc(data)
	}
	return f.File.Write(data)
}

func (f *automationAuditTestAppendFile) Sync() error {
	if f.syncFunc != nil {
		return f.syncFunc()
	}
	return f.File.Sync()
}

func (f *automationAuditTestAppendFile) Close() error {
	if f.closeFunc != nil {
		return f.closeFunc()
	}
	return f.File.Close()
}

func TestAutomationAuditTornTailAndNewlineRepair(t *testing.T) {
	t.Run("torn final line truncates", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		auditDir := filepath.Join(dataDir, "audit")
		if err := os.Mkdir(auditDir, 0o755); err != nil {
			t.Fatal(err)
		}
		first := automationAuditTestRecord("aiaudit_00000001", time.Date(2026, 7, 21, 2, 0, 0, 0, time.UTC), 1)
		canonical := automationAuditTestCanonical(t, first)
		path := filepath.Join(auditDir, "2026-07-21.jsonl")
		input := append(append(append([]byte(nil), canonical...), '\n'), []byte(`{"id":"aiaudit_00000002"`)...)
		if err := os.WriteFile(path, input, 0o644); err != nil {
			t.Fatal(err)
		}

		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatalf("OpenAutomationAuditLedger() error = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := append(append([]byte(nil), canonical...), '\n')
		if !bytes.Equal(got, want) {
			t.Fatalf("truncated file = %q, want %q", got, want)
		}
		records, err := ledger.ReadAll()
		if err != nil || len(records) != 1 || records[0].ID != first.ID {
			t.Fatalf("ReadAll() = %#v, %v", records, err)
		}
		automationAuditAssertMode(t, auditDir, 0o700)
		automationAuditAssertMode(t, path, 0o600)
	})

	t.Run("complete final line is preserved and repaired", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		auditDir := filepath.Join(dataDir, "audit")
		if err := os.Mkdir(auditDir, 0o700); err != nil {
			t.Fatal(err)
		}
		record := automationAuditTestRecord("aiaudit_00000001", time.Date(2026, 7, 21, 3, 0, 0, 0, time.UTC), 1)
		canonical := automationAuditTestCanonical(t, record)
		path := filepath.Join(auditDir, "2026-07-21.jsonl")
		if err := os.WriteFile(path, canonical, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenAutomationAuditLedger(dataDir, store); err != nil {
			t.Fatalf("OpenAutomationAuditLedger() error = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, append(append([]byte(nil), canonical...), '\n')) {
			t.Fatalf("repaired file = %q", got)
		}
	})
}

func TestAutomationAuditRejectsCompleteCorruptionAndUnsafeFiles(t *testing.T) {
	valid := automationAuditTestRecord("aiaudit_00000001", time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC), 1)
	validJSON := automationAuditTestCanonical(t, valid)
	cases := []struct {
		name string
		data []byte
	}{
		{name: "bad full line", data: []byte(`{"id":]`)},
		{name: "invalid unterminated line", data: []byte(`{"id":truX`)},
		{name: "empty line", data: append(append(append([]byte(nil), validJSON...), '\n', '\n'), validJSON...)},
		{name: "unknown field", data: bytes.Replace(validJSON, []byte(`"event_type"`), []byte(`"unknown":1,"event_type"`), 1)},
		{name: "duplicate field", data: bytes.Replace(validJSON, []byte(`"event_type"`), []byte(`"id":"aiaudit_00000001","event_type"`), 1)},
		{name: "duplicate data key", data: bytes.Replace(validJSON, []byte(`"index":1`), []byte(`"index":1,"index":1`), 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			dataDir, store := automationAuditTestStore(t)
			auditDir := filepath.Join(dataDir, "audit")
			if err := os.Mkdir(auditDir, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(auditDir, "2026-07-21.jsonl")
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenAutomationAuditLedger(dataDir, store); err == nil {
				t.Fatal("OpenAutomationAuditLedger() succeeded, want corruption error")
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, test.data) {
				t.Fatal("complete corrupt line was modified")
			}
		})
	}

	t.Run("symlink ledger", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		auditDir := filepath.Join(dataDir, "audit")
		if err := os.Mkdir(auditDir, 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, append(validJSON, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(auditDir, "2026-07-21.jsonl")); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenAutomationAuditLedger(dataDir, store); err == nil {
			t.Fatal("OpenAutomationAuditLedger() accepted a symlink ledger")
		}
	})

	t.Run("nonregular ledger", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenAutomationAuditLedger(dataDir, store); err == nil {
			t.Fatal("OpenAutomationAuditLedger() accepted a directory ledger")
		}
	})
}

func TestAppendPendingAutomationAuditStrictData(t *testing.T) {
	for _, test := range []struct {
		name string
		data json.RawMessage
	}{
		{name: "top-level duplicate key", data: json.RawMessage(`{"index":1,"index":2}`)},
		{name: "nested duplicate key", data: json.RawMessage(`{"nested":{"index":1,"index":2}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, store := automationAuditTestStore(t)
			err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
				_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
					EventType: "test.event",
					ActorType: "system",
					Data:      test.data,
					CreatedAt: time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC),
				})
				return err
			})
			if err == nil {
				t.Fatal("AppendPendingAutomationAudit() accepted duplicate Data keys")
			}
			if got := len(store.Snapshot().PendingAudit); got != 0 {
				t.Fatalf("pending audit count = %d, want 0", got)
			}
		})
	}

	t.Run("valid canonical data", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		data := json.RawMessage(`{"index":1,"nested":{"enabled":true}}`)
		err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
				EventType: "test.event",
				ActorType: "system",
				Data:      data,
				CreatedAt: time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC),
			})
			return err
		})
		if err != nil {
			t.Fatalf("AppendPendingAutomationAudit() error = %v", err)
		}
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending() error = %v", err)
		}
		records, err := ledger.ReadAll()
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if len(records) != 1 || !bytes.Equal(records[0].Data, data) {
			t.Fatalf("ReadAll() = %#v, want canonical Data %s", records, data)
		}
	})
}

func TestAutomationAuditDuplicateIDAndCrossDate(t *testing.T) {
	t.Run("same canonical duplicate is deduplicated", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		auditDir := filepath.Join(dataDir, "audit")
		if err := os.Mkdir(auditDir, 0o700); err != nil {
			t.Fatal(err)
		}
		record := automationAuditTestRecord("aiaudit_00000001", time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC), 1)
		line := automationAuditTestCanonical(t, record)
		if err := os.WriteFile(
			filepath.Join(auditDir, "2026-07-21.jsonl"),
			append(append(append([]byte(nil), line...), '\n'), append(line, '\n')...),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		records, err := ledger.ReadAll()
		if err != nil || len(records) != 1 {
			t.Fatalf("ReadAll() count = %d, err = %v", len(records), err)
		}
	})

	t.Run("conflicting duplicate is corruption", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		auditDir := filepath.Join(dataDir, "audit")
		if err := os.Mkdir(auditDir, 0o700); err != nil {
			t.Fatal(err)
		}
		first := automationAuditTestCanonical(t, automationAuditTestRecord("aiaudit_00000001", time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC), 1))
		second := automationAuditTestCanonical(t, automationAuditTestRecord("aiaudit_00000001", time.Date(2026, 7, 21, 4, 0, 0, 0, time.UTC), 2))
		data := append(append(append([]byte(nil), first...), '\n'), append(second, '\n')...)
		if err := os.WriteFile(filepath.Join(auditDir, "2026-07-21.jsonl"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenAutomationAuditLedger(dataDir, store); err == nil {
			t.Fatal("OpenAutomationAuditLedger() accepted conflicting duplicate ID")
		}
	})

	t.Run("UTC date selects separate ledgers", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		location := time.FixedZone("west", -7*60*60)
		err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
				EventType: "day.one",
				ActorType: "system",
				Data:      json.RawMessage(`{"index":1}`),
				CreatedAt: time.Date(2026, 7, 21, 18, 0, 0, 0, location),
			})
			if err != nil {
				return err
			}
			_, err = AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
				EventType: "day.two",
				ActorType: "system",
				Data:      json.RawMessage(`{"index":2}`),
				CreatedAt: time.Date(2026, 7, 22, 18, 0, 0, 0, location),
			})
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, date := range []string{"2026-07-22", "2026-07-23"} {
			if _, err := os.Stat(filepath.Join(dataDir, "audit", date+".jsonl")); err != nil {
				t.Fatalf("missing UTC ledger %s: %v", date, err)
			}
		}
	})
}

func TestAutomationAuditFlushesPendingIn512RecordBatches(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	const count = automationAuditFlushBatchSize + 1
	records := automationAuditAppendPendingBatch(
		t,
		store,
		count,
		time.Date(2026, 7, 21, 7, 0, 0, 0, time.UTC),
	)

	rewrites := 0
	store.beforePersistenceStep = func(step string) error {
		if step == automationPersistenceStepReplaceSnapshot {
			rewrites++
		}
		return nil
	}
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending() error = %v", err)
	}
	store.beforePersistenceStep = nil

	if rewrites != 2 {
		t.Fatalf("snapshot rewrites = %d, want 2", rewrites)
	}
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "audit", "2026-07-21.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	if len(lines) != count {
		t.Fatalf("ledger line count = %d, want %d", len(lines), count)
	}
	for i, line := range lines {
		record, _, err := decodeAutomationAuditLine(line)
		if err != nil {
			t.Fatalf("decode ledger line %d: %v", i, err)
		}
		if record.ID != records[i].ID {
			t.Fatalf("ledger line %d ID = %q, want %q", i, record.ID, records[i].ID)
		}
	}
}

func TestAutomationAuditPendingCommittedDedupeAndCleanupFailure(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	const count = 3
	records := automationAuditAppendPendingBatch(
		t,
		store,
		count,
		time.Date(2026, 7, 21, 8, 0, 0, 0, time.UTC),
	)

	cleanupErr := errors.New("injected cleanup failure")
	cleanupFailed := false
	store.beforePersistenceStep = func(step string) error {
		if step == automationPersistenceStepPrepareRollback && !cleanupFailed {
			cleanupFailed = true
			return cleanupErr
		}
		return nil
	}
	if err := ledger.FlushPending(context.Background()); !errors.Is(err, cleanupErr) {
		t.Fatalf("FlushPending(cleanup failure) error = %v, want %v", err, cleanupErr)
	}
	store.beforePersistenceStep = nil

	gotRecords, err := ledger.ReadAll()
	if err != nil || len(gotRecords) != count {
		t.Fatalf("ReadAll() = %#v, %v", gotRecords, err)
	}
	for i := range records {
		if gotRecords[i].ID != records[i].ID {
			t.Fatalf("ReadAll()[%d].ID = %q, want %q", i, gotRecords[i].ID, records[i].ID)
		}
	}
	path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(before, []byte{'\n'}) != count {
		t.Fatalf("ledger after cleanup failure = %q", before)
	}

	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(retry) error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("cleanup retry appended a duplicate ledger line")
	}
	if len(store.Snapshot().PendingAudit) != 0 {
		t.Fatal("pending audit was not removed")
	}
}

func TestAutomationAuditRequiresDurabilityBarriersBeforeCleanup(t *testing.T) {
	t.Run("file sync", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		record := automationAuditAppendPending(
			t,
			store,
			time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC),
			1,
		)
		injectedErr := errors.New("injected unsynced file failure")
		blockSync := true
		fullWrites := 0
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if len(data) > 1 {
						fullWrites++
					}
					return file.Write(data)
				},
				syncFunc: func() error {
					if blockSync {
						return injectedErr
					}
					return file.Sync()
				},
			}, nil
		}

		if err := ledger.FlushPending(context.Background()); !errors.Is(err, injectedErr) {
			t.Fatalf("FlushPending(unsynced file) error = %v, want %v", err, injectedErr)
		}
		path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
		automationAuditAssertPendingDurability(t, ledger, store, record, path)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(before, []byte{'\n'}) != 1 {
			t.Fatalf("readable unsynced ledger = %q", before)
		}
		if fullWrites != 1 {
			t.Fatalf("full append writes = %d, want 1", fullWrites)
		}

		blockSync = false
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(sync retry) error = %v", err)
		}
		automationAuditAssertDurabilityResolved(t, ledger, store, path)
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("file-sync retry appended a duplicate ledger line")
		}
		if fullWrites != 1 {
			t.Fatalf("full append writes after retry = %d, want 1", fullWrites)
		}
	})

	t.Run("directory sync", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		record := automationAuditAppendPending(
			t,
			store,
			time.Date(2026, 7, 21, 10, 30, 0, 0, time.UTC),
			1,
		)
		injectedErr := errors.New("injected unsynced directory failure")
		blockDirectorySync := true
		fullWrites := 0
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if len(data) > 1 {
						fullWrites++
					}
					return file.Write(data)
				},
			}, nil
		}
		ledger.syncDirectory = func(path string) error {
			if blockDirectorySync {
				return injectedErr
			}
			return syncAutomationDirectory(path)
		}

		if err := ledger.FlushPending(context.Background()); !errors.Is(err, injectedErr) {
			t.Fatalf("FlushPending(unsynced directory) error = %v, want %v", err, injectedErr)
		}
		path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
		automationAuditAssertPendingDurability(t, ledger, store, record, path)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(before, []byte{'\n'}) != 1 {
			t.Fatalf("ledger before directory-sync retry = %q", before)
		}

		blockDirectorySync = false
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(directory-sync retry) error = %v", err)
		}
		automationAuditAssertDurabilityResolved(t, ledger, store, path)
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatal("directory-sync retry appended a duplicate ledger line")
		}
		if fullWrites != 1 {
			t.Fatalf("full append writes = %d, want 1", fullWrites)
		}
	})
}

func TestAutomationAuditReconcilesAppendWriteErrors(t *testing.T) {
	t.Run("complete write error is synchronized without duplicate append", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		record := automationAuditAppendPending(
			t,
			store,
			time.Date(2026, 7, 21, 10, 45, 0, 0, time.UTC),
			1,
		)
		injectedErr := errors.New("injected complete write failure")
		injected := false
		fullWrites := 0
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if len(data) > 1 {
						fullWrites++
					}
					written, writeErr := file.Write(data)
					if writeErr != nil {
						return written, writeErr
					}
					if len(data) > 1 && !injected {
						injected = true
						return written, injectedErr
					}
					return written, nil
				},
			}, nil
		}

		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(complete write error) error = %v", err)
		}
		path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
		automationAuditAssertDurabilityResolved(t, ledger, store, path)
		if fullWrites != 1 {
			t.Fatalf("full append writes = %d, want 1", fullWrites)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(data, []byte{'\n'}) != 1 {
			t.Fatalf("ledger after complete write error = %q", data)
		}
		existing, ok := ledger.committed[record.ID]
		if !ok || !bytes.Equal(existing.canonical, automationAuditTestCanonical(t, record)) {
			t.Fatal("complete write reconciliation did not publish the canonical record")
		}
	})

	t.Run("close error is reconciled through a new descriptor", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		record := automationAuditAppendPending(
			t,
			store,
			time.Date(2026, 7, 21, 10, 47, 0, 0, time.UTC),
			1,
		)
		injectedErr := errors.New("injected close failure")
		failClose := true
		fullWrites := 0
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if len(data) > 1 {
						fullWrites++
					}
					return file.Write(data)
				},
				closeFunc: func() error {
					if flags&os.O_APPEND != 0 && failClose {
						failClose = false
						return injectedErr
					}
					return file.Close()
				},
			}, nil
		}

		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(close error) error = %v", err)
		}
		path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
		automationAuditAssertDurabilityResolved(t, ledger, store, path)
		if fullWrites != 1 {
			t.Fatalf("full append writes = %d, want 1", fullWrites)
		}
		existing, ok := ledger.committed[record.ID]
		if !ok || !bytes.Equal(existing.canonical, automationAuditTestCanonical(t, record)) {
			t.Fatal("close-error reconciliation did not publish canonical content")
		}
	})

	t.Run("torn tail is truncated before retry", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		automationAuditAppendPending(t, store, time.Date(2026, 7, 21, 11, 0, 0, 0, time.UTC), 1)
		injectedErr := errors.New("injected partial write failure")
		failed := false
		fullWrites := 0
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if len(data) > 1 {
						fullWrites++
					}
					if len(data) > 1 && !failed {
						failed = true
						written, writeErr := file.Write(data[:len(data)/2])
						if writeErr != nil {
							return written, writeErr
						}
						return written, injectedErr
					}
					return file.Write(data)
				},
			}, nil
		}

		if err := ledger.FlushPending(context.Background()); !errors.Is(err, injectedErr) {
			t.Fatalf("FlushPending(partial write) error = %v, want %v", err, injectedErr)
		}
		path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 {
			t.Fatalf("torn ledger tail was not truncated: %q", data)
		}
		if got := len(store.Snapshot().PendingAudit); got != 1 {
			t.Fatalf("pending audit count = %d, want 1", got)
		}
		if len(ledger.pendingDurability) != 0 {
			t.Fatal("resolved torn tail retained pending durability state")
		}
		if len(ledger.committed) != 0 {
			t.Fatal("torn tail was published to the committed cache")
		}

		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(retry) error = %v", err)
		}
		data, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(data, []byte{'\n'}) != 1 {
			t.Fatalf("ledger after retry = %q", data)
		}
		if fullWrites != 2 {
			t.Fatalf("full append attempts = %d, want 2", fullWrites)
		}
	})

	t.Run("newline repair failure rolls back candidate cache", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		auditDir := filepath.Join(dataDir, "audit")
		if err := os.Mkdir(auditDir, 0o700); err != nil {
			t.Fatal(err)
		}
		existingRecord := automationAuditTestRecord(
			"aiaudit_00000099",
			time.Date(2026, 7, 21, 10, 50, 0, 0, time.UTC),
			1,
		)
		existingCanonical := automationAuditTestCanonical(t, existingRecord)
		path := filepath.Join(auditDir, "2026-07-21.jsonl")
		if err := os.WriteFile(path, append(append([]byte(nil), existingCanonical...), '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		record := automationAuditAppendPending(
			t,
			store,
			time.Date(2026, 7, 21, 10, 55, 0, 0, time.UTC),
			2,
		)
		appendErr := errors.New("injected canonical short write")
		repairErr := errors.New("injected newline repair failure")
		blockRepair := true
		fullWrites := 0
		repairAttempts := 0
		injectedAppend := false
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if len(data) == 1 {
						repairAttempts++
						if blockRepair {
							return 0, repairErr
						}
						return file.Write(data)
					}
					fullWrites++
					if !injectedAppend {
						injectedAppend = true
						written, writeErr := file.Write(data[:len(data)-1])
						if writeErr != nil {
							return written, writeErr
						}
						return written, appendErr
					}
					return file.Write(data)
				},
			}, nil
		}

		err = ledger.FlushPending(context.Background())
		if !errors.Is(err, appendErr) || !errors.Is(err, repairErr) {
			t.Fatalf("FlushPending(newline repair failure) error = %v", err)
		}
		if got := len(store.Snapshot().PendingAudit); got != 1 {
			t.Fatalf("pending audit count = %d, want 1", got)
		}
		if len(ledger.committed) != 1 {
			t.Fatalf("committed cache count = %d, want 1", len(ledger.committed))
		}
		if _, ok := ledger.committed[existingRecord.ID]; !ok {
			t.Fatal("newline repair failure removed the preexisting committed record")
		}
		if _, ok := ledger.committed[record.ID]; ok {
			t.Fatal("newline repair failure polluted the committed cache")
		}
		if _, ok := ledger.pendingDurability[path]; !ok {
			t.Fatal("newline repair failure did not retain pending durability state")
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.HasSuffix(before, []byte{'\n'}) {
			t.Fatalf("newline repair unexpectedly changed the file: %q", before)
		}

		blockRepair = false
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(newline repair retry) error = %v", err)
		}
		automationAuditAssertDurabilityResolved(t, ledger, store, path)
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(after, []byte{'\n'}) || bytes.Count(after, []byte{'\n'}) != 2 {
			t.Fatalf("ledger after newline repair retry = %q", after)
		}
		if fullWrites != 1 {
			t.Fatalf("full append writes = %d, want 1", fullWrites)
		}
		if repairAttempts != 2 {
			t.Fatalf("newline repair attempts = %d, want 2", repairAttempts)
		}
	})

	t.Run("conflicting same ID fails", func(t *testing.T) {
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		record := automationAuditAppendPending(
			t,
			store,
			time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
			1,
		)
		conflicting := record
		conflicting.Data = json.RawMessage(`{"index":2}`)
		conflictingCanonical := automationAuditTestCanonical(t, conflicting)
		injectedErr := errors.New("injected conflicting write failure")
		ledger.openAppendFile = func(
			path string,
			flags int,
			mode os.FileMode,
		) (automationAuditAppendFile, error) {
			file, err := os.OpenFile(path, flags, mode)
			if err != nil {
				return nil, err
			}
			return &automationAuditTestAppendFile{
				File: file,
				writeFunc: func(data []byte) (int, error) {
					if _, err := file.Write(append(append([]byte(nil), conflictingCanonical...), '\n')); err != nil {
						return 0, err
					}
					return len(data), injectedErr
				},
			}, nil
		}

		err = ledger.FlushPending(context.Background())
		if err == nil || !strings.Contains(err.Error(), "conflicting audit content") {
			t.Fatalf("FlushPending(conflict) error = %v, want conflicting content", err)
		}
		if got := len(store.Snapshot().PendingAudit); got != 1 {
			t.Fatalf("pending audit count = %d, want 1", got)
		}
		_, err = ledger.ReadAll()
		var persistenceErr *AutomationPersistenceError
		if err == nil ||
			!errors.As(err, &persistenceErr) ||
			!strings.Contains(persistenceErr.Err.Error(), "conflicting content") {
			t.Fatalf("ReadAll(conflict) error = %v, want conflicting content", err)
		}
	})
}

func TestAutomationAuditReopenRetriesPendingDurability(t *testing.T) {
	for _, test := range []struct {
		name      string
		blockFile bool
	}{
		{name: "file sync", blockFile: true},
		{name: "directory sync"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dataDir, store := automationAuditTestStore(t)
			ledger, err := OpenAutomationAuditLedger(dataDir, store)
			if err != nil {
				t.Fatal(err)
			}
			record := automationAuditAppendPending(
				t,
				store,
				time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC),
				1,
			)
			injectedErr := errors.New("injected reopen " + test.name + " failure")
			blockFirst := true
			if test.blockFile {
				ledger.openAppendFile = automationAuditBlockingSyncOpener(&blockFirst, injectedErr)
			} else {
				ledger.syncDirectory = func(path string) error {
					if blockFirst {
						return injectedErr
					}
					return syncAutomationDirectory(path)
				}
			}

			if err := ledger.FlushPending(context.Background()); !errors.Is(err, injectedErr) {
				t.Fatalf("FlushPending(initial %s failure) error = %v", test.name, err)
			}
			path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(before, []byte{'\n'}) != 1 {
				t.Fatalf("ledger before reopen = %q", before)
			}

			reopened, err := OpenAutomationAuditLedger(dataDir, store)
			if err != nil {
				t.Fatalf("OpenAutomationAuditLedger(reopen) error = %v", err)
			}
			if _, ok := reopened.pendingDurability[path]; !ok {
				t.Fatal("reopened ledger did not conservatively retain path durability")
			}
			blockReopened := true
			if test.blockFile {
				reopened.openAppendFile = automationAuditBlockingSyncOpener(&blockReopened, injectedErr)
			} else {
				reopened.syncDirectory = func(path string) error {
					if blockReopened {
						return injectedErr
					}
					return syncAutomationDirectory(path)
				}
			}
			if err := reopened.FlushPending(context.Background()); !errors.Is(err, injectedErr) {
				t.Fatalf("FlushPending(reopened blocked %s) error = %v", test.name, err)
			}
			if got := len(store.Snapshot().PendingAudit); got != 1 {
				t.Fatalf("pending audit count after reopened failure = %d, want 1", got)
			}
			if _, ok := reopened.pendingDurability[path]; !ok {
				t.Fatal("reopened barrier failure cleared pending durability")
			}

			blockReopened = false
			if err := reopened.FlushPending(context.Background()); err != nil {
				t.Fatalf("FlushPending(reopened retry) error = %v", err)
			}
			automationAuditAssertDurabilityResolved(t, reopened, store, path)
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatalf("%s reopen retry duplicated the ledger record", test.name)
			}
			existing, ok := reopened.committed[record.ID]
			if !ok || !bytes.Equal(existing.canonical, automationAuditTestCanonical(t, record)) {
				t.Fatal("reopened retry did not retain canonical committed content")
			}
		})
	}
}

func TestAutomationAuditPartialBatchAppendIsRetrySafe(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]AutomationAuditRecord, 0, 2)
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for _, createdAt := range []time.Time{
			time.Date(2026, 7, 21, 23, 59, 0, 0, time.UTC),
			time.Date(2026, 7, 22, 0, 1, 0, 0, time.UTC),
		} {
			record, appendErr := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
				EventType: "partial.flush",
				ActorType: "system",
				Data:      json.RawMessage(`{"ok":true}`),
				CreatedAt: createdAt,
			})
			if appendErr != nil {
				return appendErr
			}
			records = append(records, record)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	blockedPath := filepath.Join(dataDir, "audit", "2026-07-22.jsonl")
	if err := os.Mkdir(blockedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ledger.FlushPending(context.Background()); !errors.Is(err, ErrAutomationPersistence) {
		t.Fatalf("FlushPending(partial append) error = %v, want persistence error", err)
	}
	if got := len(store.Snapshot().PendingAudit); got != len(records) {
		t.Fatalf("pending audit count after partial append = %d, want %d", got, len(records))
	}
	firstPath := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
	firstBefore, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(firstBefore, []byte{'\n'}) != 1 {
		t.Fatalf("first ledger after partial append = %q", firstBefore)
	}

	if err := os.Remove(blockedPath); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatalf("OpenAutomationAuditLedger(retry) error = %v", err)
	}
	if err := reopened.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(retry) error = %v", err)
	}
	firstAfter, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBefore, firstAfter) {
		t.Fatal("partial append retry duplicated the committed first record")
	}
	second, err := os.ReadFile(blockedPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(second, []byte{'\n'}) != 1 {
		t.Fatalf("second ledger after retry = %q", second)
	}
	if len(store.Snapshot().PendingAudit) != 0 {
		t.Fatal("partial append retry did not remove the batch")
	}
}

func TestAutomationAuditMidBatchCancellationIsRetrySafe(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	const count = 3
	automationAuditAppendPendingBatch(
		t,
		store,
		count,
		time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC),
	)
	ctx, cancel := context.WithCancel(context.Background())
	closeCount := 0
	ledger.openAppendFile = func(
		path string,
		flags int,
		mode os.FileMode,
	) (automationAuditAppendFile, error) {
		file, err := os.OpenFile(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &automationAuditTestAppendFile{
			File: file,
			closeFunc: func() error {
				err := file.Close()
				closeCount++
				if closeCount == 1 {
					cancel()
				}
				return err
			},
		}, nil
	}

	if err := ledger.FlushPending(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("FlushPending(canceled) error = %v, want context.Canceled", err)
	}
	if closeCount != 1 {
		t.Fatalf("append close count before cancellation = %d, want 1", closeCount)
	}
	if got := len(store.Snapshot().PendingAudit); got != count {
		t.Fatalf("pending audit count after cancellation = %d, want %d", got, count)
	}
	path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(before, []byte{'\n'}) != 1 {
		t.Fatalf("ledger after cancellation = %q", before)
	}

	ledger.openAppendFile = nil
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(retry) error = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(after, []byte{'\n'}) != count {
		t.Fatalf("ledger line count after retry = %d, want %d", bytes.Count(after, []byte{'\n'}), count)
	}
}

func TestAutomationAuditConcurrentPendingTailIsPreserved(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	initial := automationAuditAppendPendingBatch(
		t,
		store,
		2,
		time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC),
	)
	firstClosed := make(chan struct{})
	tailStored := make(chan struct{})
	var blockOnce sync.Once
	ledger.openAppendFile = func(
		path string,
		flags int,
		mode os.FileMode,
	) (automationAuditAppendFile, error) {
		file, err := os.OpenFile(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &automationAuditTestAppendFile{
			File: file,
			closeFunc: func() error {
				err := file.Close()
				blockOnce.Do(func() {
					close(firstClosed)
					<-tailStored
				})
				return err
			},
		}, nil
	}

	flushErr := make(chan error, 1)
	go func() {
		flushErr <- ledger.FlushPending(context.Background())
	}()
	select {
	case <-firstClosed:
	case <-time.After(time.Second):
		close(tailStored)
		t.Fatal("first batch append did not reach the deterministic barrier")
	}

	var tail AutomationAuditRecord
	updateErr := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		var err error
		tail, err = AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: "concurrent.tail",
			ActorType: "system",
			Data:      json.RawMessage(`{"tail":true}`),
			CreatedAt: time.Date(2026, 7, 21, 14, 1, 0, 0, time.UTC),
		})
		return err
	})
	close(tailStored)
	if updateErr != nil {
		t.Fatal(updateErr)
	}
	select {
	case err := <-flushErr:
		if err != nil {
			t.Fatalf("FlushPending() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FlushPending() did not finish after storing the pending tail")
	}

	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "audit", "2026-07-21.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSuffix(data, []byte{'\n'}), []byte{'\n'})
	wantIDs := []string{initial[0].ID, initial[1].ID, tail.ID}
	if len(lines) != len(wantIDs) {
		t.Fatalf("ledger line count = %d, want %d", len(lines), len(wantIDs))
	}
	for i, line := range lines {
		record, _, err := decodeAutomationAuditLine(line)
		if err != nil {
			t.Fatalf("decode ledger line %d: %v", i, err)
		}
		if record.ID != wantIDs[i] {
			t.Fatalf("ledger line %d ID = %q, want %q", i, record.ID, wantIDs[i])
		}
	}
}

func TestAutomationAuditConcurrentFlush(t *testing.T) {
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	const count = 32
	err = store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for i := 0; i < count; i++ {
			_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
				EventType: "concurrent.flush",
				ActorType: "system",
				Data:      json.RawMessage(`{"ok":true}`),
				CreatedAt: time.Date(2026, 7, 21, 9, i, 0, 0, time.UTC),
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	const workers = 12
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errs <- ledger.FlushPending(context.Background())
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent FlushPending() error = %v", err)
		}
	}
	if len(store.Snapshot().PendingAudit) != 0 {
		t.Fatal("concurrent flush left pending records")
	}
	records, err := ledger.ReadAll()
	if err != nil || len(records) != count {
		t.Fatalf("ReadAll() count = %d, err = %v", len(records), err)
	}
	data, err := os.ReadFile(filepath.Join(dataDir, "audit", "2026-07-21.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte{'\n'}) != count {
		t.Fatalf("ledger line count = %d, want %d", bytes.Count(data, []byte{'\n'}), count)
	}
}

func TestAutomationAuditBatchFlushCountWindow(t *testing.T) {
	// BATCH=2 closes the flush window by record count; the 1h interval never
	// closes it by time, isolating the count-window semantics.
	automationAuditSetFlushEnvironment(t, "2", "3600000")
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	syncCount := 0
	ledger.openAppendFile = automationAuditCountingSyncOpener(&syncCount)

	day := time.Date(2026, 7, 21, 11, 0, 0, 0, time.UTC)
	path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")

	// Two appends close the count window: exactly one Sync covers both.
	first := automationAuditAppendPendingBatch(t, store, 2, day)
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(batch of 2) error = %v", err)
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 for 2 batched appends", syncCount)
	}
	automationAuditAssertBatchCommitted(t, ledger, store, first)
	if records, err := ledger.ReadAll(); err != nil || len(records) != 2 {
		t.Fatalf("ReadAll() = %d records, %v; want 2", len(records), err)
	}

	// A single append inside the open window writes the file (page cache) but
	// is not synced or published yet.
	third := automationAuditAppendPending(t, store, day.Add(time.Hour), 1)
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(single append) error = %v", err)
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 while the window is open", syncCount)
	}
	if _, ok := ledger.committed[third.ID]; ok {
		t.Fatal("append inside the open window was published before its sync")
	}
	if _, ok := ledger.written[third.ID]; !ok {
		t.Fatal("append inside the open window was not retained as written")
	}
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte{'\n'}); got != 3 {
		t.Fatalf("ledger line count = %d, want 3 (write precedes sync)", got)
	}
	if records, err := ledger.ReadAll(); err != nil || len(records) != 2 {
		t.Fatalf("ReadAll() = %d records, %v; want 2 (unsynced record must not be readable)", len(records), err)
	}

	// The second append closes the window: one Sync publishes both.
	fourth := automationAuditAppendPending(t, store, day.Add(2*time.Hour), 2)
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(closing append) error = %v", err)
	}
	if syncCount != 2 {
		t.Fatalf("sync calls = %d, want 2 after the count window closed", syncCount)
	}
	automationAuditAssertBatchCommitted(t, ledger, store, []AutomationAuditRecord{third, fourth})
	if records, err := ledger.ReadAll(); err != nil || len(records) != 4 {
		t.Fatalf("ReadAll() = %d records, %v; want 4", len(records), err)
	}
	if syncCount >= 4 {
		t.Fatalf("sync calls = %d, want fewer than the 4 appends", syncCount)
	}
}

func TestAutomationAuditBatchFlushTimeWindow(t *testing.T) {
	t.Run("far apart flushes each sync", func(t *testing.T) {
		// A 1ms interval with a count window that never closes: the time
		// window is the only trigger.
		automationAuditSetFlushEnvironment(t, "1000000", "1")
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		syncCount := 0
		ledger.openAppendFile = automationAuditCountingSyncOpener(&syncCount)

		day := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
		first := automationAuditAppendPending(t, store, day, 1)
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(first) error = %v", err)
		}
		if syncCount != 1 {
			t.Fatalf("sync calls = %d, want 1 for the first flush", syncCount)
		}
		automationAuditAssertBatchCommitted(t, ledger, store, []AutomationAuditRecord{first})

		time.Sleep(50 * time.Millisecond) // well past the 1ms interval

		second := automationAuditAppendPending(t, store, day.Add(time.Minute), 2)
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(second) error = %v", err)
		}
		if syncCount != 2 {
			t.Fatalf("sync calls = %d, want 2 (each far-apart flush syncs)", syncCount)
		}
		automationAuditAssertBatchCommitted(t, ledger, store, []AutomationAuditRecord{first, second})
	})

	t.Run("open time window defers sync", func(t *testing.T) {
		automationAuditSetFlushEnvironment(t, "1000000", "200")
		dataDir, store := automationAuditTestStore(t)
		ledger, err := OpenAutomationAuditLedger(dataDir, store)
		if err != nil {
			t.Fatal(err)
		}
		syncCount := 0
		ledger.openAppendFile = automationAuditCountingSyncOpener(&syncCount)

		day := time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC)
		first := automationAuditAppendPending(t, store, day, 1)
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(first) error = %v", err)
		}
		if syncCount != 1 {
			t.Fatalf("sync calls = %d, want 1 for the first flush", syncCount)
		}
		if _, ok := ledger.committed[first.ID]; !ok {
			t.Fatal("first flush did not publish its record")
		}

		// Deterministically reopen the time window so the elapsed interval
		// cannot race the 200ms threshold.
		ledger.lastSyncTime = time.Now()
		second := automationAuditAppendPending(t, store, day.Add(time.Minute), 2)
		if err := ledger.FlushPending(context.Background()); err != nil {
			t.Fatalf("FlushPending(immediate) error = %v", err)
		}
		if syncCount != 1 {
			t.Fatalf("sync calls = %d, want 1 while the time window is open", syncCount)
		}
		if _, ok := ledger.committed[second.ID]; ok {
			t.Fatal("append inside the open time window was published early")
		}
		if _, ok := ledger.written[second.ID]; !ok {
			t.Fatal("append inside the open time window was not retained as written")
		}
	})
}

func TestAutomationAuditLegacyFlushSyncsEveryAppend(t *testing.T) {
	day := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		batch      string
		intervalMs string
	}{
		{name: "batch zero", batch: "0", intervalMs: "1000"},
		{name: "interval zero", batch: "100", intervalMs: "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			automationAuditSetFlushEnvironment(t, test.batch, test.intervalMs)
			dataDir, store := automationAuditTestStore(t)
			ledger, err := OpenAutomationAuditLedger(dataDir, store)
			if err != nil {
				t.Fatal(err)
			}
			syncCount := 0
			ledger.openAppendFile = automationAuditCountingSyncOpener(&syncCount)

			const count = 3
			records := automationAuditAppendPendingBatch(t, store, count, day)
			if err := ledger.FlushPending(context.Background()); err != nil {
				t.Fatalf("FlushPending() error = %v", err)
			}
			if syncCount != count {
				t.Fatalf("sync calls = %d, want %d (every append synced)", syncCount, count)
			}
			automationAuditAssertBatchCommitted(t, ledger, store, records)
			if got, err := ledger.ReadAll(); err != nil || len(got) != count {
				t.Fatalf("ReadAll() = %d records, %v; want %d", len(got), err, count)
			}
		})
	}
}

func TestAutomationAuditBatchFlushSyncBarrierFailure(t *testing.T) {
	automationAuditSetFlushEnvironment(t, "2", "3600000")
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	injectedErr := errors.New("injected sync barrier failure")
	blockSync := true
	syncCount := 0
	ledger.openAppendFile = func(
		path string,
		flags int,
		mode os.FileMode,
	) (automationAuditAppendFile, error) {
		file, err := os.OpenFile(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &automationAuditTestAppendFile{
			File: file,
			syncFunc: func() error {
				syncCount++
				if blockSync {
					return injectedErr
				}
				return file.Sync()
			},
		}, nil
	}

	day := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	records := automationAuditAppendPendingBatch(t, store, 2, day)
	path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")

	// The sync barrier fails: the error propagates and the durability barrier
	// precedes cleanup — records stay pending and unpublished.
	if err := ledger.FlushPending(context.Background()); !errors.Is(err, injectedErr) {
		t.Fatalf("FlushPending(sync failure) error = %v, want %v", err, injectedErr)
	}
	if got := len(store.Snapshot().PendingAudit); got != 2 {
		t.Fatalf("pending audit count = %d, want 2 (barrier failure must not clean up)", got)
	}
	if got := len(ledger.committed); got != 0 {
		t.Fatalf("committed count = %d, want 0", got)
	}
	if got := len(ledger.written); got != 2 {
		t.Fatalf("written count = %d, want 2", got)
	}
	if _, ok := ledger.pendingDurability[path]; !ok {
		t.Fatal("sync barrier failure did not retain pending durability state")
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 (one failed barrier attempt)", syncCount)
	}

	// Unblock the barrier: the next flush reconciles and publishes the batch.
	blockSync = false
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(retry) error = %v", err)
	}
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0 after retry", got)
	}
	automationAuditAssertBatchCommitted(t, ledger, store, records)
	if _, ok := ledger.pendingDurability[path]; ok {
		t.Fatal("successful retry did not clear pending durability state")
	}
	if syncCount < 2 {
		t.Fatalf("sync calls = %d, want more than 1 after the retry re-synced", syncCount)
	}
}

func TestAutomationAuditFlushSyncForcesOpenWindow(t *testing.T) {
	// BATCH=100 with a 1h interval: a single append never closes the flush
	// window by count or time, so FlushPending alone defers the sync and
	// FlushSync must force it (Close-path guarantee, CONTRACT C3 #2).
	automationAuditSetFlushEnvironment(t, "100", "3600000")
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	syncCount := 0
	ledger.openAppendFile = automationAuditCountingSyncOpener(&syncCount)

	day := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	path := filepath.Join(dataDir, "audit", "2026-07-21.jsonl")

	// The first flush drains immediately: a never-synced ledger counts as due.
	first := automationAuditAppendPending(t, store, day, 1)
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(first) error = %v", err)
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 for the first flush", syncCount)
	}
	if _, ok := ledger.committed[first.ID]; !ok {
		t.Fatal("first flush did not publish its record")
	}

	// A single append inside the open window: FlushPending writes the file
	// (page cache) but defers the sync, keeping the record unpublished.
	second := automationAuditAppendPending(t, store, day.Add(time.Minute), 2)
	if err := ledger.FlushPending(context.Background()); err != nil {
		t.Fatalf("FlushPending(open window) error = %v", err)
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 while the window is open", syncCount)
	}
	if _, ok := ledger.committed[second.ID]; ok {
		t.Fatal("append inside the open window was published before FlushSync")
	}
	if _, ok := ledger.written[second.ID]; !ok {
		t.Fatal("append inside the open window was not retained as written")
	}

	// FlushSync forces the tail: the window is still open, but the forced pass
	// syncs, publishes into committed, and makes the record readable.
	if err := ledger.FlushSync(context.Background()); err != nil {
		t.Fatalf("FlushSync() error = %v", err)
	}
	if syncCount != 2 {
		t.Fatalf("sync calls = %d, want 2 (forced sync despite the open window)", syncCount)
	}
	if _, ok := ledger.committed[second.ID]; !ok {
		t.Fatal("FlushSync did not publish the written tail into committed")
	}
	if got := len(ledger.written); got != 0 {
		t.Fatalf("written count = %d, want 0 after FlushSync", got)
	}
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}
	records, err := ledger.ReadAll()
	if err != nil || len(records) != 2 {
		t.Fatalf("ReadAll() = %d records, %v; want 2 (both records visible)", len(records), err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(data, []byte{'\n'}) != 2 {
		t.Fatalf("ledger line count = %d, want 2", bytes.Count(data, []byte{'\n'}))
	}
}

func TestAutomationAuditFlushSyncEmptyAndIdempotent(t *testing.T) {
	automationAuditSetFlushEnvironment(t, "100", "3600000")
	dataDir, store := automationAuditTestStore(t)
	ledger, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		t.Fatal(err)
	}
	syncCount := 0
	ledger.openAppendFile = automationAuditCountingSyncOpener(&syncCount)

	// No pending audit and no written tail: FlushSync is a no-op returning nil.
	if err := ledger.FlushSync(context.Background()); err != nil {
		t.Fatalf("FlushSync(empty) error = %v, want nil", err)
	}
	if syncCount != 0 {
		t.Fatalf("sync calls = %d, want 0 for an empty ledger", syncCount)
	}

	// After a real flush the written map is empty again; a second FlushSync
	// must not re-sync anything and must still return nil (idempotent).
	day := time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)
	record := automationAuditAppendPending(t, store, day, 1)
	if err := ledger.FlushSync(context.Background()); err != nil {
		t.Fatalf("FlushSync(one record) error = %v", err)
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 after flushing one record", syncCount)
	}
	if _, ok := ledger.committed[record.ID]; !ok {
		t.Fatal("FlushSync did not publish the flushed record")
	}
	if err := ledger.FlushSync(context.Background()); err != nil {
		t.Fatalf("FlushSync(idempotent) error = %v, want nil", err)
	}
	if syncCount != 1 {
		t.Fatalf("sync calls = %d, want 1 (idempotent FlushSync must not re-sync)", syncCount)
	}
	records, err := ledger.ReadAll()
	if err != nil || len(records) != 1 {
		t.Fatalf("ReadAll() = %d records, %v; want 1", len(records), err)
	}
}

func automationAuditTestStore(t *testing.T) (string, *AutomationStore) {
	t.Helper()
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return dataDir, store
}

func automationAuditAppendPending(
	t *testing.T,
	store *AutomationStore,
	createdAt time.Time,
	index int,
) AutomationAuditRecord {
	t.Helper()
	var record AutomationAuditRecord
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		var err error
		record, err = AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: "test.event",
			ActorType: "system",
			Data:      json.RawMessage(`{"index":1}`),
			CreatedAt: createdAt,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func automationAuditAppendPendingBatch(
	t *testing.T,
	store *AutomationStore,
	count int,
	createdAt time.Time,
) []AutomationAuditRecord {
	t.Helper()
	records := make([]AutomationAuditRecord, 0, count)
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for i := 0; i < count; i++ {
			data, err := json.Marshal(map[string]int{"index": i})
			if err != nil {
				return err
			}
			record, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
				EventType: "batch.flush",
				ActorType: "system",
				Data:      data,
				CreatedAt: createdAt,
			})
			if err != nil {
				return err
			}
			records = append(records, record)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return records
}

func automationAuditBlockingSyncOpener(
	block *bool,
	injectedErr error,
) func(string, int, os.FileMode) (automationAuditAppendFile, error) {
	return func(path string, flags int, mode os.FileMode) (automationAuditAppendFile, error) {
		file, err := os.OpenFile(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &automationAuditTestAppendFile{
			File: file,
			syncFunc: func() error {
				if *block {
					return injectedErr
				}
				return file.Sync()
			},
		}, nil
	}
}

func automationAuditAssertPendingDurability(
	t *testing.T,
	ledger *AutomationAuditLedger,
	store *AutomationStore,
	record AutomationAuditRecord,
	path string,
) {
	t.Helper()
	if got := len(store.Snapshot().PendingAudit); got != 1 {
		t.Fatalf("pending audit count = %d, want 1", got)
	}
	if _, ok := ledger.committed[record.ID]; ok {
		t.Fatal("readable audit record was published before durability barriers")
	}
	if _, ok := ledger.pendingDurability[path]; !ok {
		t.Fatal("failed durability barrier did not retain path state")
	}
}

func automationAuditAssertDurabilityResolved(
	t *testing.T,
	ledger *AutomationAuditLedger,
	store *AutomationStore,
	path string,
) {
	t.Helper()
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}
	if _, ok := ledger.pendingDurability[path]; ok {
		t.Fatal("successful durability barriers did not clear path state")
	}
}

func automationAuditTestRecord(id string, createdAt time.Time, index int) AutomationAuditRecord {
	data, _ := json.Marshal(map[string]int{"index": index})
	return AutomationAuditRecord{
		ID:        id,
		EventType: "test.event",
		ActorType: "system",
		Data:      data,
		CreatedAt: createdAt,
	}
}

func automationAuditTestCanonical(t *testing.T, record AutomationAuditRecord) []byte {
	t.Helper()
	data, err := canonicalAutomationAudit(record)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func automationAuditAssertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %#o, want %#o", path, got, want)
	}
}

func automationAuditUnsetFlushEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		automationAuditFlushBatchEnv,
		automationAuditFlushIntervalMsEnv,
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

// automationAuditSetFlushEnvironment pins both batch-flush environment
// variables for the test and restores the previous values on cleanup, keeping
// the batch-window tests isolated from each other and from the ambient
// environment.
func automationAuditSetFlushEnvironment(t *testing.T, batch string, intervalMs string) {
	t.Helper()
	automationAuditUnsetFlushEnvironment(t)
	t.Setenv(automationAuditFlushBatchEnv, batch)
	t.Setenv(automationAuditFlushIntervalMsEnv, intervalMs)
}

// automationAuditCountingSyncOpener wraps every ledger file open so each
// Sync() call (append-path in legacy mode, flush-path via syncAuditFile in
// batch mode) is counted through the returned pointer.
func automationAuditCountingSyncOpener(
	syncCount *int,
) func(string, int, os.FileMode) (automationAuditAppendFile, error) {
	return func(path string, flags int, mode os.FileMode) (automationAuditAppendFile, error) {
		file, err := os.OpenFile(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &automationAuditTestAppendFile{
			File: file,
			syncFunc: func() error {
				*syncCount++
				return file.Sync()
			},
		}, nil
	}
}

func automationAuditAssertBatchCommitted(
	t *testing.T,
	ledger *AutomationAuditLedger,
	store *AutomationStore,
	records []AutomationAuditRecord,
) {
	t.Helper()
	if got := len(store.Snapshot().PendingAudit); got != 0 {
		t.Fatalf("pending audit count = %d, want 0", got)
	}
	for _, record := range records {
		if _, ok := ledger.committed[record.ID]; !ok {
			t.Fatalf("record %s was not published to committed", record.ID)
		}
	}
}
