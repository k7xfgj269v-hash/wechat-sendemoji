package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AutomationAuditLedger struct {
	mu                sync.Mutex
	auditDir          string
	store             *AutomationStore
	committed         map[string]automationCommittedAudit
	pendingDurability map[string]automationAuditPendingDurability
	openAppendFile    func(string, int, os.FileMode) (automationAuditAppendFile, error)
	syncDirectory     func(string) error

	// Batch flush configuration (ONEBOT_AUDIT_FLUSH_BATCH and
	// ONEBOT_AUDIT_FLUSH_INTERVAL_MS). A zero batch or interval selects the
	// legacy per-record sync behavior as a safety fallback.
	flushBatch      int
	flushInterval   time.Duration
	unsyncedAppends int
	lastSyncTime    time.Time
	written         map[string]automationCommittedAudit
	dirSyncPending  map[string]bool
}

const (
	automationAuditFlushBatchSize = 512

	automationAuditFlushBatchEnv      = "ONEBOT_AUDIT_FLUSH_BATCH"
	automationAuditFlushIntervalMsEnv = "ONEBOT_AUDIT_FLUSH_INTERVAL_MS"

	defaultAutomationAuditFlushBatch    = 100
	defaultAutomationAuditFlushInterval = 1000 * time.Millisecond
	maxAutomationAuditFlushBatch        = 1000000
	maxAutomationAuditFlushIntervalMs   = 3600000
)

type automationAuditFlushConfig struct {
	batch    int
	interval time.Duration
}

// readAutomationAuditFlushConfig reads the batch flush configuration from the
// environment, mirroring readAutomationRetentionLimits in automation_store.go.
// Zero is accepted for both variables and selects the legacy per-record sync
// behavior (0 records between syncs / 0ms interval both mean "sync on every
// write"); negative values are rejected as misconfiguration.
func readAutomationAuditFlushConfig() (automationAuditFlushConfig, error) {
	config := automationAuditFlushConfig{
		batch:    defaultAutomationAuditFlushBatch,
		interval: defaultAutomationAuditFlushInterval,
	}
	if raw, exists := os.LookupEnv(automationAuditFlushBatchEnv); exists {
		raw = strings.TrimSpace(raw)
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return automationAuditFlushConfig{}, NewAutomationValidationError(
				automationAuditFlushBatchEnv,
				"must be a non-negative integer",
			)
		}
		if parsed > maxAutomationAuditFlushBatch {
			return automationAuditFlushConfig{}, NewAutomationValidationError(
				automationAuditFlushBatchEnv,
				fmt.Sprintf("must not exceed %d", maxAutomationAuditFlushBatch),
			)
		}
		config.batch = int(parsed)
	}
	if raw, exists := os.LookupEnv(automationAuditFlushIntervalMsEnv); exists {
		raw = strings.TrimSpace(raw)
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return automationAuditFlushConfig{}, NewAutomationValidationError(
				automationAuditFlushIntervalMsEnv,
				"must be a non-negative integer",
			)
		}
		if parsed > maxAutomationAuditFlushIntervalMs {
			return automationAuditFlushConfig{}, NewAutomationValidationError(
				automationAuditFlushIntervalMsEnv,
				fmt.Sprintf("must not exceed %d", maxAutomationAuditFlushIntervalMs),
			)
		}
		config.interval = time.Duration(parsed) * time.Millisecond
	}
	return config, nil
}

type automationAuditAppendFile interface {
	Chmod(os.FileMode) error
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

type automationCommittedAudit struct {
	record    AutomationAuditRecord
	canonical []byte
}

type automationAuditPendingDurability struct {
	date                 time.Time
	requireDirectorySync bool
}

func NewAutomationAuditLedger(dataDir string, store *AutomationStore) (*AutomationAuditLedger, error) {
	return OpenAutomationAuditLedger(dataDir, store)
}

func OpenAutomationAuditLedger(dataDir string, store *AutomationStore) (*AutomationAuditLedger, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, NewAutomationValidationError("data_dir", "is required")
	}
	if store == nil {
		return nil, NewAutomationValidationError("automation_store", "is required")
	}

	flushConfig, err := readAutomationAuditFlushConfig()
	if err != nil {
		return nil, err
	}

	auditDir := filepath.Join(filepath.Clean(dataDir), "audit")
	if err := ensureAutomationAuditDirectory(auditDir); err != nil {
		return nil, err
	}
	ledger := &AutomationAuditLedger{
		auditDir:          auditDir,
		store:             store,
		committed:         make(map[string]automationCommittedAudit),
		pendingDurability: make(map[string]automationAuditPendingDurability),
		flushBatch:        flushConfig.batch,
		flushInterval:     flushConfig.interval,
		openAppendFile: func(path string, flags int, mode os.FileMode) (automationAuditAppendFile, error) {
			return os.OpenFile(path, flags, mode)
		},
		syncDirectory: syncAutomationDirectory,
	}
	if err := ledger.scan(); err != nil {
		return nil, err
	}
	if err := ledger.markReopenedPendingDurability(); err != nil {
		return nil, err
	}
	return ledger, nil
}

func AppendPendingAutomationAudit(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	record AutomationAuditRecord,
) (AutomationAuditRecord, error) {
	if state == nil {
		return AutomationAuditRecord{}, NewAutomationValidationError("state", "is required")
	}
	if record.ID != "" {
		return AutomationAuditRecord{}, NewAutomationValidationError("audit.id", "must be assigned by the server")
	}
	id, err := allocator.Next(AutomationIDKindAudit)
	if err != nil {
		return AutomationAuditRecord{}, err
	}
	record.ID = id
	record.CreatedAt = record.CreatedAt.UTC()
	record.Data = cloneAutomationBytes(record.Data)
	if err := validateAutomationAuditRecord("audit", &record); err != nil {
		return AutomationAuditRecord{}, err
	}
	if err := rejectAutomationDuplicateJSONKeys("audit.data", record.Data); err != nil {
		return AutomationAuditRecord{}, err
	}
	state.PendingAudit = append(state.PendingAudit, record)
	return record, nil
}

func (l *AutomationAuditLedger) FlushPending(ctx context.Context) error {
	if l == nil || l.store == nil {
		return NewAutomationValidationError("audit_ledger", "is required")
	}
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var batch []automationCommittedAudit
		if err := l.store.Read(func(state *AutomationState) error {
			if len(state.PendingAudit) == 0 {
				return nil
			}
			batchSize := len(state.PendingAudit)
			if batchSize > automationAuditFlushBatchSize {
				batchSize = automationAuditFlushBatchSize
			}
			batch = make([]automationCommittedAudit, batchSize)
			for i := range batch {
				record := state.PendingAudit[i]
				canonical, err := canonicalAutomationAudit(record)
				if err != nil {
					return err
				}
				batch[i] = automationCommittedAudit{
					record:    cloneAutomationAuditRecord(record),
					canonical: canonical,
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if len(batch) == 0 {
			// Nothing left pending: opportunistically flush any written-but-
			// unsynced data from previous batches if the flush window closed.
			if _, err := l.flushBatchPaths(false); err != nil {
				return err
			}
			return nil
		}

		for _, item := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			path := l.auditPath(item.record)
			if _, ok := l.pendingDurability[path]; ok {
				if err := l.reconcilePendingDurability(path); err != nil {
					return err
				}
			}
			if existing, ok := l.committed[item.record.ID]; ok {
				if !bytes.Equal(existing.canonical, item.canonical) {
					return WrapAutomationPersistence(
						"flush audit",
						fmt.Errorf("audit ID %s has conflicting content", item.record.ID),
					)
				}
				continue
			}
			if existing, ok := l.written[item.record.ID]; ok {
				if !bytes.Equal(existing.canonical, item.canonical) {
					return WrapAutomationPersistence(
						"flush audit",
						fmt.Errorf("audit ID %s has conflicting content", item.record.ID),
					)
				}
				continue
			}
			if err := l.append(item.record, item.canonical); err != nil {
				return err
			}
			if l.written == nil {
				l.written = make(map[string]automationCommittedAudit)
			}
			l.written[item.record.ID] = automationCommittedAudit{
				record:    cloneAutomationAuditRecord(item.record),
				canonical: append([]byte(nil), item.canonical...),
			}
		}

		// Batched durability: append only writes to the page cache; the flush
		// loop calls Sync() once per file when the flush window (record count
		// or time since the last sync) closes. Records become readable
		// (committed) only after their sync barrier succeeded; a sync failure
		// keeps them out of committed and leaves them in PendingAudit for the
		// existing reconcile machinery, so durability barriers always precede
		// cleanup. When the window has not closed yet, the written records
		// simply wait for a later flush call.
		synced, err := l.flushBatchPaths(false)
		if err != nil {
			return err
		}
		if synced {
			l.promoteWritten()
		}

		if err := l.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
			if len(state.PendingAudit) < len(batch) {
				return WrapAutomationPersistence(
					"remove pending audit",
					fmt.Errorf("pending audit batch shrank from %d to %d records", len(batch), len(state.PendingAudit)),
				)
			}
			for i, item := range batch {
				current, currentErr := canonicalAutomationAudit(state.PendingAudit[i])
				if currentErr != nil {
					return currentErr
				}
				if !bytes.Equal(current, item.canonical) {
					return WrapAutomationPersistence(
						"remove pending audit",
						fmt.Errorf("audit ID %s changed after append", item.record.ID),
					)
				}
			}
			remaining := make([]AutomationAuditRecord, len(state.PendingAudit)-len(batch))
			copy(remaining, state.PendingAudit[len(batch):])
			state.PendingAudit = remaining
			return nil
		}); err != nil {
			return err
		}
	}
}

// FlushSync drains PendingAudit exactly like FlushPending and then forces a
// final Sync() of every written-but-unsynced file, bypassing the batch flush
// window. It is used by shutdown paths (AutomationRuntime.Close) so that no
// written audit tail is left unflushed before close (CONTRACT C3 #2: flush
// before close). The batch window semantics of FlushPending are unchanged; the
// forced pass simply closes any window that is still open. On success it
// returns nil; on failure the first error encountered is returned (the Close
// call site keeps discarding it, so the error propagation contract is
// unchanged).
func (l *AutomationAuditLedger) FlushSync(ctx context.Context) error {
	if l == nil || l.store == nil {
		return NewAutomationValidationError("audit_ledger", "is required")
	}
	flushErr := l.FlushPending(ctx)
	l.mu.Lock()
	synced, syncErr := l.flushBatchPaths(true)
	if syncErr == nil && synced {
		l.promoteWritten()
	}
	l.mu.Unlock()
	if flushErr != nil {
		return flushErr
	}
	return syncErr
}

// promoteWritten moves every written-but-now-synced record into committed,
// making it readable through ReadAll, and clears the written map. Callers must
// hold l.mu and must only call it after every written record's file has been
// synced (durability barrier passed).
func (l *AutomationAuditLedger) promoteWritten() {
	for id, item := range l.written {
		l.committed[id] = item
	}
	l.written = make(map[string]automationCommittedAudit)
}

func (l *AutomationAuditLedger) ReadAll() ([]AutomationAuditRecord, error) {
	if l == nil || l.store == nil {
		return nil, NewAutomationValidationError("audit_ledger", "is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	merged := make(map[string]automationCommittedAudit, len(l.committed))
	for id, record := range l.committed {
		merged[id] = record
	}
	if err := l.store.Read(func(state *AutomationState) error {
		for _, pending := range state.PendingAudit {
			canonical, err := canonicalAutomationAudit(pending)
			if err != nil {
				return err
			}
			if existing, ok := merged[pending.ID]; ok {
				if !bytes.Equal(existing.canonical, canonical) {
					return WrapAutomationPersistence("read audit", fmt.Errorf("audit ID %s has conflicting content", pending.ID))
				}
				continue
			}
			merged[pending.ID] = automationCommittedAudit{
				record:    cloneAutomationAuditRecord(pending),
				canonical: canonical,
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	records := make([]AutomationAuditRecord, 0, len(merged))
	for _, item := range merged {
		records = append(records, cloneAutomationAuditRecord(item.record))
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID < records[j].ID
		}
		return records[i].CreatedAt.Before(records[j].CreatedAt)
	})
	return records, nil
}

func (l *AutomationAuditLedger) Records() ([]AutomationAuditRecord, error) {
	return l.ReadAll()
}

func (l *AutomationAuditLedger) scan() error {
	entries, err := os.ReadDir(l.auditDir)
	if err != nil {
		return WrapAutomationPersistence("read audit directory", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		date, err := automationAuditDateFromName(entry.Name())
		if err != nil {
			return WrapAutomationPersistence("scan audit directory", err)
		}
		path := filepath.Join(l.auditDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return WrapAutomationPersistence("inspect audit file", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return WrapAutomationPersistence("inspect audit file", errors.New("audit ledger must be a regular file"))
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return WrapAutomationPersistence("set audit file permissions", err)
		}
		if err := l.scanFile(path, date); err != nil {
			return err
		}
	}
	return nil
}

func (l *AutomationAuditLedger) scanFile(path string, date time.Time) error {
	candidate := cloneAutomationCommittedAuditMap(l.committed)
	if err := l.scanFileCandidate(path, date, candidate); err != nil {
		return err
	}
	l.committed = candidate
	return nil
}

func (l *AutomationAuditLedger) scanFileCandidate(
	path string,
	date time.Time,
	candidate map[string]automationCommittedAudit,
) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return WrapAutomationPersistence("read audit file", err)
	}
	offset := 0
	for offset < len(data) {
		newline := bytes.IndexByte(data[offset:], '\n')
		if newline < 0 {
			line := data[offset:]
			if len(trimAutomationJSONWhitespace(line)) == 0 {
				return WrapAutomationPersistence("scan audit file", errors.New("audit ledger contains an empty line"))
			}
			record, canonical, decodeErr := decodeAutomationAuditLine(line)
			if decodeErr != nil {
				if automationAuditJSONIncomplete(line) {
					return truncateAutomationAuditFile(path, int64(offset))
				}
				return decodeErr
			}
			if err := addAutomationCommittedAudit(candidate, record, canonical, date); err != nil {
				return err
			}
			return l.repairAutomationAuditNewline(path)
		}
		line := data[offset : offset+newline]
		if len(trimAutomationJSONWhitespace(line)) == 0 {
			return WrapAutomationPersistence("scan audit file", errors.New("audit ledger contains an empty line"))
		}
		record, canonical, err := decodeAutomationAuditLine(line)
		if err != nil {
			return err
		}
		if err := addAutomationCommittedAudit(candidate, record, canonical, date); err != nil {
			return err
		}
		offset += newline + 1
	}
	return nil
}

func addAutomationCommittedAudit(
	committed map[string]automationCommittedAudit,
	record AutomationAuditRecord,
	canonical []byte,
	fileDate time.Time,
) error {
	recordDate := record.CreatedAt.UTC().Format("2006-01-02")
	if recordDate != fileDate.Format("2006-01-02") {
		return WrapAutomationPersistence("scan audit file", fmt.Errorf("audit ID %s is stored under the wrong UTC date", record.ID))
	}
	if existing, ok := committed[record.ID]; ok {
		if !bytes.Equal(existing.canonical, canonical) {
			return WrapAutomationPersistence("scan audit file", fmt.Errorf("audit ID %s has conflicting content", record.ID))
		}
		return nil
	}
	committed[record.ID] = automationCommittedAudit{
		record:    cloneAutomationAuditRecord(record),
		canonical: append([]byte(nil), canonical...),
	}
	return nil
}

func (l *AutomationAuditLedger) append(record AutomationAuditRecord, canonical []byte) error {
	path := l.auditPath(record)
	created := false
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		created = true
	case err != nil:
		return WrapAutomationPersistence("inspect audit file", err)
	case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
		return WrapAutomationPersistence("inspect audit file", errors.New("audit ledger must be a regular file"))
	}

	flags := os.O_WRONLY | os.O_APPEND
	if created {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := l.openAuditFile(path, flags, 0o600)
	if err != nil {
		return WrapAutomationPersistence("open audit file", err)
	}
	reconcileFailure := func(operation string, operationErr error) error {
		_ = file.Close()
		l.markPendingDurability(path, record.CreatedAt.UTC(), created)
		return l.reconcileAppendFailure(
			path,
			record,
			canonical,
			WrapAutomationPersistence(operation, operationErr),
		)
	}
	if err := file.Chmod(0o600); err != nil {
		return reconcileFailure("set audit file permissions", err)
	}
	line := append(append([]byte(nil), canonical...), '\n')
	written, err := file.Write(line)
	if err == nil && written != len(line) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return reconcileFailure("append audit record", err)
	}
	legacy := l.legacyAuditSync()

	// Durability policy: with the legacy configuration (batch <= 0 or
	// interval <= 0) every write is synced immediately, exactly as before this
	// optimization. In batched mode the record is only written to the page
	// cache; the flush loop (FlushPending) calls Sync() once per file when the
	// flush window (record count or time since the last sync) closes, and
	// records only become readable (committed) after that Sync() succeeds.
	// Newly created files defer their directory sync together with the file
	// sync via dirSyncPending, preserving the file-sync-before-directory-sync
	// order.
	if legacy {
		if err := file.Sync(); err != nil {
			return reconcileFailure("sync audit file", err)
		}
	} else {
		l.unsyncedAppends++
		if created {
			if l.dirSyncPending == nil {
				l.dirSyncPending = make(map[string]bool)
			}
			l.dirSyncPending[path] = true
		}
	}
	if err := file.Close(); err != nil {
		_ = file.Close()
		l.markPendingDurability(path, record.CreatedAt.UTC(), created)
		return l.reconcileAppendFailure(
			path,
			record,
			canonical,
			WrapAutomationPersistence("close audit file", err),
		)
	}
	if created && legacy {
		if err := l.syncAuditDirectory(); err != nil {
			l.markPendingDurability(path, record.CreatedAt.UTC(), true)
			return l.reconcileAppendFailure(
				path,
				record,
				canonical,
				WrapAutomationPersistence("sync audit directory", err),
			)
		}
	}
	return nil
}

// legacyAuditSync reports whether the ledger runs in the legacy per-record
// sync mode. A zero batch or zero interval selects this mode as a safety
// fallback (0 records between syncs / 0ms interval both mean "sync on every
// write").
func (l *AutomationAuditLedger) legacyAuditSync() bool {
	return l.flushBatch <= 0 || l.flushInterval <= 0
}

// auditFlushDue reports whether the batch flush window has closed: the
// accumulated unsynced appends reached the configured batch, or the time since
// the last sync exceeded the configured interval. A ledger that has never
// synced counts as due so the first flush drains immediately. The time window
// is evaluated at the flush decision points instead of a timer goroutine, so
// no extra lifecycle is introduced.
func (l *AutomationAuditLedger) auditFlushDue() bool {
	if l.flushBatch <= 0 || l.flushInterval <= 0 {
		return true
	}
	if l.unsyncedAppends >= l.flushBatch {
		return true
	}
	if l.lastSyncTime.IsZero() {
		return true
	}
	return time.Since(l.lastSyncTime) >= l.flushInterval
}

// flushBatchPaths syncs every file that still holds written-but-unsynced
// records (one Sync() call per file), then reports whether that data is now
// durable. When force is false it returns (false, nil) if the flush window has
// not closed, in which case the written records stay unpublished and wait for
// a later flush call; when force is true it syncs unconditionally (shutdown
// paths, so no written tail is left unflushed before close). Newly created
// files have their deferred directory sync performed right after the file
// sync; a directory sync failure is routed through the existing
// pendingDurability reconciliation so the next flush retries it.
func (l *AutomationAuditLedger) flushBatchPaths(force bool) (bool, error) {
	if l.legacyAuditSync() {
		return true, nil
	}
	if len(l.written) == 0 {
		return true, nil
	}
	if !force && !l.auditFlushDue() {
		return false, nil
	}
	paths := make(map[string]struct{}, len(l.written))
	for _, item := range l.written {
		paths[l.auditPath(item.record)] = struct{}{}
	}
	for path := range paths {
		if err := l.syncAuditFile(path); err != nil {
			// Retain the path's durability state so the existing reconcile
			// machinery (pendingDurability) re-syncs it on the next flush.
			if date, dateErr := automationAuditDateFromName(filepath.Base(path)); dateErr == nil {
				l.markPendingDurability(path, date, l.dirSyncPending[path])
			}
			return false, err
		}
		if l.dirSyncPending[path] {
			if err := l.syncAuditDirectory(); err != nil {
				if date, dateErr := automationAuditDateFromName(filepath.Base(path)); dateErr == nil {
					l.markPendingDurability(path, date, true)
				}
				return false, WrapAutomationPersistence("sync audit directory after flush", err)
			}
			delete(l.dirSyncPending, path)
		}
	}
	l.lastSyncTime = time.Now()
	l.unsyncedAppends = 0
	return true, nil
}

func (l *AutomationAuditLedger) reconcileAppendFailure(
	path string,
	record AutomationAuditRecord,
	canonical []byte,
	appendErr error,
) error {
	if err := l.reconcilePendingDurability(path); err != nil {
		return errors.Join(appendErr, err)
	}
	existing, committed := l.committed[record.ID]
	if committed && !bytes.Equal(existing.canonical, canonical) {
		return errors.Join(
			appendErr,
			WrapAutomationPersistence(
				"reconcile conflicting audit content",
				fmt.Errorf("audit ID %s has conflicting content", record.ID),
			),
		)
	}
	if committed {
		return nil
	}
	return appendErr
}

func (l *AutomationAuditLedger) reconcilePendingDurability(path string) error {
	pending, ok := l.pendingDurability[path]
	if !ok {
		return nil
	}
	candidate := cloneAutomationCommittedAuditMap(l.committed)
	if err := l.scanFileCandidate(path, pending.date, candidate); err != nil {
		return err
	}
	if err := l.syncAuditFile(path); err != nil {
		return err
	}
	if pending.requireDirectorySync {
		if err := l.syncAuditDirectory(); err != nil {
			return WrapAutomationPersistence("sync audit directory after reconciliation", err)
		}
	}
	l.committed = candidate
	delete(l.pendingDurability, path)
	return nil
}

func (l *AutomationAuditLedger) markPendingDurability(
	path string,
	date time.Time,
	requireDirectorySync bool,
) {
	if l.pendingDurability == nil {
		l.pendingDurability = make(map[string]automationAuditPendingDurability)
	}
	pending := l.pendingDurability[path]
	pending.date = date.UTC()
	pending.requireDirectorySync = pending.requireDirectorySync || requireDirectorySync
	l.pendingDurability[path] = pending
}

func (l *AutomationAuditLedger) markReopenedPendingDurability() error {
	return l.store.Read(func(state *AutomationState) error {
		for _, record := range state.PendingAudit {
			path := l.auditPath(record)
			if _, ok := l.pendingDurability[path]; ok {
				continue
			}
			info, err := os.Lstat(path)
			switch {
			case errors.Is(err, os.ErrNotExist):
				continue
			case err != nil:
				return WrapAutomationPersistence("inspect pending audit file", err)
			case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular():
				return WrapAutomationPersistence("inspect pending audit file", errors.New("audit ledger must be a regular file"))
			}
			l.markPendingDurability(path, record.CreatedAt.UTC(), true)
		}
		return nil
	})
}

func (l *AutomationAuditLedger) auditPath(record AutomationAuditRecord) string {
	date := record.CreatedAt.UTC().Format("2006-01-02")
	return filepath.Join(l.auditDir, date+".jsonl")
}

func (l *AutomationAuditLedger) openAuditFile(
	path string,
	flags int,
	mode os.FileMode,
) (automationAuditAppendFile, error) {
	if l.openAppendFile == nil {
		return os.OpenFile(path, flags, mode)
	}
	return l.openAppendFile(path, flags, mode)
}

func (l *AutomationAuditLedger) syncAuditFile(path string) error {
	file, err := l.openAuditFile(path, os.O_RDONLY, 0)
	if err != nil {
		return WrapAutomationPersistence("open audit file for reconciliation sync", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return WrapAutomationPersistence("sync reconciled audit file", err)
	}
	if err := file.Close(); err != nil {
		return WrapAutomationPersistence("close reconciled audit file", err)
	}
	return nil
}

func (l *AutomationAuditLedger) syncAuditDirectory() error {
	if l.syncDirectory == nil {
		return syncAutomationDirectory(l.auditDir)
	}
	return l.syncDirectory(l.auditDir)
}

func ensureAutomationAuditDirectory(path string) error {
	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(path, 0o700); err != nil {
			return WrapAutomationPersistence("create audit directory", err)
		}
		if err := syncAutomationDirectory(filepath.Dir(path)); err != nil {
			return WrapAutomationPersistence("sync audit parent directory", err)
		}
	case err != nil:
		return WrapAutomationPersistence("inspect audit directory", err)
	case info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
		return WrapAutomationPersistence("inspect audit directory", errors.New("audit path must be a directory"))
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return WrapAutomationPersistence("set audit directory permissions", err)
	}
	return nil
}

func automationAuditDateFromName(name string) (time.Time, error) {
	if len(name) != len("2006-01-02.jsonl") || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, fmt.Errorf("unexpected audit entry %q", name)
	}
	dateText := strings.TrimSuffix(name, ".jsonl")
	date, err := time.Parse("2006-01-02", dateText)
	if err != nil || date.Format("2006-01-02") != dateText {
		return time.Time{}, fmt.Errorf("invalid audit filename %q", name)
	}
	return date, nil
}

func decodeAutomationAuditLine(line []byte) (AutomationAuditRecord, []byte, error) {
	var record AutomationAuditRecord
	if err := decodeAutomationJSONObject(
		"audit_record",
		line,
		&record,
		"id",
		"event_type",
		"actor_type",
		"data",
		"created_at",
	); err != nil {
		return AutomationAuditRecord{}, nil, err
	}
	if !isAutomationAuditID(record.ID) {
		return AutomationAuditRecord{}, nil, NewAutomationValidationError("audit_record.id", "has an invalid format")
	}
	record.CreatedAt = record.CreatedAt.UTC()
	if err := validateAutomationAuditRecord("audit_record", &record); err != nil {
		return AutomationAuditRecord{}, nil, err
	}
	canonical, err := canonicalAutomationAudit(record)
	if err != nil {
		return AutomationAuditRecord{}, nil, err
	}
	return record, canonical, nil
}

func canonicalAutomationAudit(record AutomationAuditRecord) ([]byte, error) {
	record.CreatedAt = record.CreatedAt.UTC()
	if !isAutomationAuditID(record.ID) {
		return nil, NewAutomationValidationError("audit.id", "has an invalid format")
	}
	if err := validateAutomationAuditRecord("audit", &record); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(record)
	if err != nil {
		return nil, WrapAutomationPersistence("encode audit record", err)
	}
	return canonical, nil
}

func isAutomationAuditID(id string) bool {
	if len(id) != len("aiaudit_00000001") || !strings.HasPrefix(id, "aiaudit_") {
		return false
	}
	for _, character := range id[len("aiaudit_"):] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return id != "aiaudit_00000000"
}

func cloneAutomationAuditRecord(record AutomationAuditRecord) AutomationAuditRecord {
	record.Data = cloneAutomationBytes(record.Data)
	return record
}

func cloneAutomationCommittedAuditMap(
	source map[string]automationCommittedAudit,
) map[string]automationCommittedAudit {
	cloned := make(map[string]automationCommittedAudit, len(source))
	for id, item := range source {
		cloned[id] = item
	}
	return cloned
}

func truncateAutomationAuditFile(path string, size int64) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return WrapAutomationPersistence("open torn audit file", err)
	}
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		return WrapAutomationPersistence("truncate torn audit tail", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return WrapAutomationPersistence("sync truncated audit file", err)
	}
	if err := file.Close(); err != nil {
		return WrapAutomationPersistence("close truncated audit file", err)
	}
	return nil
}

func (l *AutomationAuditLedger) repairAutomationAuditNewline(path string) error {
	file, err := l.openAuditFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return WrapAutomationPersistence("open audit file for newline repair", err)
	}
	written, err := file.Write([]byte{'\n'})
	if err == nil && written != 1 {
		err = io.ErrShortWrite
	}
	if err != nil {
		_ = file.Close()
		return WrapAutomationPersistence("repair audit newline", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return WrapAutomationPersistence("sync repaired audit file", err)
	}
	if err := file.Close(); err != nil {
		return WrapAutomationPersistence("close repaired audit file", err)
	}
	return nil
}

func automationAuditJSONIncomplete(data []byte) bool {
	data = trimAutomationJSONWhitespace(data)
	if len(data) == 0 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	err := decoder.Decode(&value)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var syntaxError *json.SyntaxError
	if errors.As(err, &syntaxError) {
		return syntaxError.Offset >= int64(len(data)) &&
			strings.Contains(syntaxError.Error(), "unexpected end of JSON input")
	}
	return false
}
