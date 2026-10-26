package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	automationMaxEntityCount = 100000
	automationMaxAuditCount  = 500000
	automationMaxIDCounter   = 99999999

	automationRetentionSessionsEnv     = "ONEBOT_AUTOMATION_RETENTION_SESSIONS"
	automationRetentionTurnsEnv        = "ONEBOT_AUTOMATION_RETENTION_TURNS"
	automationRetentionRunsEnv         = "ONEBOT_AUTOMATION_RETENTION_RUNS"
	automationRetentionIdempotencyEnv  = "ONEBOT_AUTOMATION_RETENTION_IDEMPOTENCY"
	automationRetentionPendingAuditEnv = "ONEBOT_AUTOMATION_RETENTION_PENDING_AUDIT"

	automationRecoveryMarkerVersion  = 1
	automationPermissionOperatorAuto = "automation-policy"

	automationPersistenceStepPrepareRollback       = "prepare rollback snapshot"
	automationPersistenceStepPrepareMarker         = "prepare recovery marker"
	automationPersistenceStepSyncRecovery          = "sync recovery preparation"
	automationPersistenceStepReplaceSnapshot       = "replace snapshot"
	automationPersistenceStepSyncSnapshot          = "sync snapshot directory"
	automationPersistenceStepRemoveRecoveryMarker  = "remove recovery marker"
	automationPersistenceStepSyncMarkerRemoval     = "sync recovery marker removal"
	automationPersistenceStepRemoveRollback        = "remove rollback snapshot"
	automationPersistenceStepSyncRollbackRemoval   = "sync rollback removal"
	automationPersistenceStepRestoreSnapshot       = "restore previous snapshot"
	automationPersistenceStepSyncRestoredSnapshot  = "sync restored snapshot"
	automationPersistenceStepCleanupRecovery       = "clean recovery artifacts"
	automationPersistenceStepSyncRecoveryCleanup   = "sync recovery cleanup"
	automationPersistenceStepCleanupOrphanRollback = "clean orphan rollback snapshot"
)

var (
	ErrAutomationStoreClosed = errors.New("automation store closed")
	ErrAutomationPersistence = errors.New("automation persistence error")

	errAutomationPersistenceUncertain = errors.New("automation persistence coherence is uncertain")
	automationOpenMigrationMu         sync.Mutex
)

type AutomationPersistenceError struct {
	Operation string
	Err       error
}

func (e *AutomationPersistenceError) Error() string {
	if e.Operation == "" {
		return ErrAutomationPersistence.Error()
	}
	return "automation persistence failed during " + e.Operation
}

func (e *AutomationPersistenceError) Unwrap() error {
	return e.Err
}

func (e *AutomationPersistenceError) Is(target error) bool {
	return target == ErrAutomationPersistence || errors.Is(e.Err, target)
}

func WrapAutomationPersistence(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &AutomationPersistenceError{Operation: operation, Err: err}
}

type AutomationIDKind string

const (
	AutomationIDKindSession           AutomationIDKind = "session"
	AutomationIDKindTurn              AutomationIDKind = "turn"
	AutomationIDKindRun               AutomationIDKind = "run"
	AutomationIDKindAction            AutomationIDKind = "action"
	AutomationIDKindResult            AutomationIDKind = "result"
	AutomationIDKindApproval          AutomationIDKind = "approval"
	AutomationIDKindOutbox            AutomationIDKind = "outbox"
	AutomationIDKindAudit             AutomationIDKind = "audit"
	AutomationIDKindPermissionRequest AutomationIDKind = "permission_request"
	AutomationIDKindPermissionRule    AutomationIDKind = "permission_rule"
)

type AutomationStore struct {
	mu                    sync.RWMutex
	path                  string
	state                 AutomationState
	retention             automationRetentionLimits
	closed                bool
	persistenceBlocked    error
	syncDirectory         func(string) error
	beforePersistenceStep func(string) error
}

type automationRetentionLimits struct {
	Sessions     int
	Turns        int
	Runs         int
	Idempotency  int
	PendingAudit int
}

type automationRetentionCandidate struct {
	ID        string
	Timestamp time.Time
}

type AutomationIDAllocator struct {
	counters AutomationCounters
	active   bool
}

type automationPersistenceOps struct {
	syncDirectory func(string) error
	beforeStep    func(string) error
	// skipPreValidate suppresses the redundant full validation inside
	// writeAutomationSnapshotAtomicWithOps for callers that already validated
	// the exact state they are persisting (AutomationStore.Update).
	skipPreValidate bool
}

type automationRecoveryMarker struct {
	Version         int  `json:"version"`
	SnapshotExisted bool `json:"snapshot_existed"`
}

type automationRecoveryTransaction struct {
	markerBytes      []byte
	rollbackBytes    []byte
	rollbackByRename bool
	snapshotExisted  bool
}

func DefaultAutomationDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", WrapAutomationPersistence("resolve home directory", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", WrapAutomationPersistence("resolve home directory", errors.New("home directory is empty"))
	}
	return filepath.Join(home, ".wechat-chatter", "automation"), nil
}

func DefaultAutomationDataPath() (string, error) {
	dataDir, err := DefaultAutomationDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataDir, "state.json"), nil
}

func OpenAutomationStore(dataDir string) (*AutomationStore, error) {
	return openAutomationStoreWithOps(
		dataDir,
		automationPersistenceOps{syncDirectory: syncAutomationDirectory},
	)
}

func openAutomationStoreWithOps(dataDir string, ops automationPersistenceOps) (*AutomationStore, error) {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return nil, NewAutomationValidationError("data_dir", "is required")
	}
	retention, err := readAutomationRetentionLimits()
	if err != nil {
		return nil, err
	}
	dataDir = filepath.Clean(dataDir)
	ops = ops.normalized()
	automationOpenMigrationMu.Lock()
	defer automationOpenMigrationMu.Unlock()
	if err := ensureAutomationDirectory(dataDir); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "state.json")
	if err := resolveAutomationRecovery(path, ops); err != nil {
		return nil, err
	}

	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		state := newAutomationState()
		if err := writeAutomationSnapshotAtomicWithOps(path, state, ops); err != nil {
			return nil, err
		}
		return &AutomationStore{
			path:                  path,
			state:                 cloneAutomationState(state),
			retention:             retention,
			syncDirectory:         ops.syncDirectory,
			beforePersistenceStep: ops.beforeStep,
		}, nil
	case err != nil:
		return nil, WrapAutomationPersistence("inspect snapshot", err)
	case info.Mode()&os.ModeSymlink != 0:
		return nil, WrapAutomationPersistence("inspect snapshot", errors.New("snapshot must not be a symbolic link"))
	case !info.Mode().IsRegular():
		return nil, WrapAutomationPersistence("inspect snapshot", errors.New("snapshot must be a regular file"))
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return nil, WrapAutomationPersistence("set snapshot permissions", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, WrapAutomationPersistence("read snapshot", err)
	}
	state, sourceVersion, err := decodeAutomationStateVersioned(data)
	if err != nil {
		return nil, err
	}
	traceRepaired := reconcileAutomationRunTraceStatuses(&state)
	pruned := applyAutomationRetention(&state, retention)
	if sourceVersion != AutomationSnapshotSchemaVersion || traceRepaired || pruned {
		if err := writeAutomationSnapshotAtomicWithOps(path, state, ops); err != nil {
			return nil, err
		}
	}
	return &AutomationStore{
		path:                  path,
		state:                 cloneAutomationState(state),
		retention:             retention,
		syncDirectory:         ops.syncDirectory,
		beforePersistenceStep: ops.beforeStep,
	}, nil
}

func (s *AutomationStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *AutomationStore) Snapshot() AutomationState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneAutomationState(s.state)
}

// Read provides read-only access to the in-memory state under the store's
// read lock without cloning it, for callers that only need a small subset of
// the state (audit pending records, chat sessions, a single run, ...).
// The callback must not retain any reference into the state and must not
// mutate it; copies of observed values may be taken and used after the
// callback returns. Read must not be called from within an Update callback
// (the store lock is not re-entrant).
func (s *AutomationStore) Read(fn func(*AutomationState) error) error {
	if fn == nil {
		return NewAutomationValidationError("read", "callback is required")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return fn(&s.state)
}

func (s *AutomationStore) EnsureText(
	ctx context.Context,
	action AutomationAction,
	chatType ChatType,
	peerID string,
	text string,
) (AutomationOutboxEntry, bool, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	if !IsValidChatType(chatType) {
		return AutomationOutboxEntry{}, false, NewAutomationValidationError("chat_type", "is invalid")
	}
	if err := validateStoredAutomationString("peer_id", peerID, 256, true, false); err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	if err := validateStoredAutomationString("text", text, 4000, true, true); err != nil {
		return AutomationOutboxEntry{}, false, err
	}

	var result AutomationOutboxEntry
	created := false
	err := s.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		stored := automationOutboxFindAction(state, action.ID)
		if stored == nil {
			return &AutomationOutboxNotFoundError{ActionID: action.ID}
		}
		key := "send:" + action.ID
		if record, exists := state.Idempotency[key]; exists {
			entry := automationOutboxFindByID(state, record.ResourceID)
			if record.Kind != "outbox" || entry == nil || entry.ActionID != action.ID {
				return &AutomationOutboxConflictError{ActionID: action.ID, Message: "canonical outbox idempotency key is inconsistent"}
			}
			result = cloneAutomationOutboxEntry(*entry)
			return errAutomationOutboxNoMutation
		}
		if stored.Recipient == nil ||
			!automationActionSupportsTextOutbox(*stored) ||
			stored.Recipient.ChatType != chatType ||
			stored.Recipient.PeerID != peerID {
			return &AutomationOutboxConflictError{ActionID: action.ID, Message: "action is not linked to the requested recipient"}
		}
		outboxID, err := allocator.Next(AutomationIDKindOutbox)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		entry := AutomationOutboxEntry{
			ID:             outboxID,
			ActionID:       action.ID,
			IdempotencyKey: key,
			ChatType:       chatType,
			PeerID:         peerID,
			Text:           text,
			Status:         AutomationOutboxPending,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		state.Outbox = append(state.Outbox, entry)
		state.Idempotency[key] = AutomationIdempotencyRecord{
			Kind:       "outbox",
			ResourceID: outboxID,
			CreatedAt:  now,
		}
		if err := automationOutboxAppendAudit(
			state,
			allocator,
			entry,
			"",
			AutomationAuditEventOutboxPending,
			false,
			now,
		); err != nil {
			return err
		}
		result = cloneAutomationOutboxEntry(entry)
		created = true
		return nil
	})
	if errors.Is(err, errAutomationOutboxNoMutation) {
		return result, false, nil
	}
	if err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	return result, created, nil
}

func (s *AutomationStore) Update(fn func(*AutomationState, *AutomationIDAllocator) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrAutomationStoreClosed
	}
	if s.persistenceBlocked != nil {
		return s.persistenceBlocked
	}
	if fn == nil {
		return NewAutomationValidationError("update", "callback is required")
	}

	ops := automationPersistenceOps{
		syncDirectory:   s.syncDirectory,
		beforeStep:      s.beforePersistenceStep,
		skipPreValidate: true,
	}
	if err := resolveAutomationRecovery(s.path, ops); err != nil {
		s.blockPersistenceIfUncertain(err)
		return err
	}

	working := cloneAutomationState(s.state)
	allocator := &AutomationIDAllocator{counters: working.Counters, active: true}
	defer func() {
		allocator.active = false
	}()
	if err := fn(&working, allocator); err != nil {
		return err
	}
	allocator.counters = maxAutomationCounters(allocator.counters, working.Counters)
	if err := validateAutomationImmutableTrace(&s.state, &working); err != nil {
		return err
	}
	if err := reconcileAutomationTrace(&working, &s.state, allocator); err != nil {
		return err
	}
	allocator.active = false
	working.Counters = allocator.counters
	normalizeAutomationOutboxChatTypes(&working)
	if err := ValidateAutomationState(&working); err != nil {
		return err
	}
	applyAutomationRetention(&working, s.retention)
	if err := validateAutomationStateLight(&working); err != nil {
		return err
	}
	if err := writeAutomationSnapshotAtomicWithOps(s.path, working, ops); err != nil {
		s.blockPersistenceIfUncertain(err)
		return err
	}
	// working is already a private deep copy produced above and is not
	// referenced by any caller after fn returns, so it can become the live
	// state without a second full clone.
	s.state = working
	return nil
}

func maxAutomationCounters(left, right AutomationCounters) AutomationCounters {
	result := left
	values := []struct {
		target *uint64
		value  uint64
	}{
		{&result.Session, right.Session},
		{&result.Turn, right.Turn},
		{&result.TurnEvent, right.TurnEvent},
		{&result.Run, right.Run},
		{&result.Action, right.Action},
		{&result.Result, right.Result},
		{&result.Approval, right.Approval},
		{&result.Outbox, right.Outbox},
		{&result.Audit, right.Audit},
		{&result.PermissionRequest, right.PermissionRequest},
		{&result.PermissionRule, right.PermissionRule},
	}
	for _, item := range values {
		if item.value > *item.target {
			*item.target = item.value
		}
	}
	return result
}

func readAutomationRetentionLimits() (automationRetentionLimits, error) {
	limits := automationRetentionLimits{}
	values := []struct {
		name         string
		defaultValue int
		maximum      int
		target       *int
	}{
		{automationRetentionSessionsEnv, automationMaxEntityCount, automationMaxEntityCount, &limits.Sessions},
		{automationRetentionTurnsEnv, automationMaxEntityCount, automationMaxEntityCount, &limits.Turns},
		{automationRetentionRunsEnv, automationMaxEntityCount, automationMaxEntityCount, &limits.Runs},
		{
			automationRetentionIdempotencyEnv,
			automationMaxEntityCount * 2,
			automationMaxEntityCount * 2,
			&limits.Idempotency,
		},
		{automationRetentionPendingAuditEnv, automationMaxAuditCount, automationMaxAuditCount, &limits.PendingAudit},
	}
	for _, value := range values {
		raw, exists := os.LookupEnv(value.name)
		if !exists {
			*value.target = value.defaultValue
			continue
		}
		raw = strings.TrimSpace(raw)
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			return automationRetentionLimits{}, NewAutomationValidationError(value.name, "must be a non-negative integer")
		}
		if parsed > uint64(value.maximum) {
			return automationRetentionLimits{}, NewAutomationValidationError(
				value.name,
				fmt.Sprintf("must not exceed %d", value.maximum),
			)
		}
		*value.target = int(parsed)
	}
	return limits, nil
}
