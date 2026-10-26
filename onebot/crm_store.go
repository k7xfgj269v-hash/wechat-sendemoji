package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
)

const (
	crmMaxEntityCount = 100000
	crmMaxNoteCount   = 500000
	crmMaxRuleCount   = 1000
	crmMaxIDCounter   = 99999999
)

type CRMStore struct {
	mu            sync.RWMutex
	path          string
	state         CRMState
	closed        bool
	syncDirectory func(string) error
}

type IDAllocator struct {
	counters IDCounters
	active   bool
}

var crmOpenLocks sync.Map

func DefaultCRMDataPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", WrapCRMPersistence("resolve home directory", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", WrapCRMPersistence("resolve home directory", errors.New("home directory is empty"))
	}
	return filepath.Join(home, ".wechat-chatter", "crm.json"), nil
}

func OpenCRMStore(path string) (*CRMStore, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, NewCRMValidationError("path", "is required")
	}
	path = filepath.Clean(path)
	openLockValue, _ := crmOpenLocks.LoadOrStore(path, &sync.Mutex{})
	openLock := openLockValue.(*sync.Mutex)
	openLock.Lock()
	defer openLock.Unlock()
	if err := ensureCRMParent(path); err != nil {
		return nil, err
	}

	info, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// A write that crashed after renaming the previous snapshot aside
		// but before replacing it (see writeSnapshotBytesAtomic) leaves the
		// old data in .<base>.rollback with no main snapshot; recover it
		// before deciding whether this is a first-ever write.
		restored, rerr := recoverCRMRollbackSnapshot(path)
		if rerr != nil {
			return nil, rerr
		}
		if !restored {
			state := newCRMState()
			if err := writeSnapshotAtomic(path, state); err != nil {
				return nil, err
			}
			return &CRMStore{
				path:          path,
				state:         cloneCRMState(state),
				syncDirectory: syncCRMDirectory,
			}, nil
		}
	case err != nil:
		return nil, WrapCRMPersistence("inspect snapshot", err)
	case info.Mode()&os.ModeSymlink != 0:
		return nil, WrapCRMPersistence("inspect snapshot", errors.New("snapshot must not be a symbolic link"))
	case !info.Mode().IsRegular():
		return nil, WrapCRMPersistence("inspect snapshot", errors.New("snapshot must be a regular file"))
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return nil, WrapCRMPersistence("set snapshot permissions", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, WrapCRMPersistence("read snapshot", err)
	}
	state, migrated, err := decodeCRMStateWithMigration(data)
	if err != nil {
		return nil, err
	}
	if migrated {
		if err := writeSnapshotAtomic(path, state); err != nil {
			return nil, err
		}
	}
	return &CRMStore{
		path:          path,
		state:         cloneCRMState(state),
		syncDirectory: syncCRMDirectory,
	}, nil
}

func (s *CRMStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *CRMStore) Snapshot() CRMState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneCRMState(s.state)
}

func (s *CRMStore) Update(fn func(*CRMState, *IDAllocator) error) error {
	return s.UpdateWithMeta(CRMUpdateMeta{
		Actor:     "system",
		Operation: "update",
	}, fn)
}

func (s *CRMStore) UpdateWithMeta(meta CRMUpdateMeta, fn func(*CRMState, *IDAllocator) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return ErrCRMStoreClosed
	}
	if fn == nil {
		return NewCRMValidationError("update", "callback is required")
	}
	normalizedMeta, err := normalizeCRMUpdateMeta(meta)
	if err != nil {
		return err
	}

	working := cloneCRMState(s.state)
	allocator := &IDAllocator{counters: working.Counters, active: true}
	defer func() {
		allocator.active = false
	}()
	callbackErr := fn(&working, allocator)
	if callbackErr != nil {
		allocator.active = false
		return callbackErr
	}
	normalizeCRMStateV2Defaults(&working)
	if err := reconcileCRMStateV4(&s.state, &working, normalizedMeta, allocator); err != nil {
		allocator.active = false
		return err
	}
	auditID, err := allocator.Next(IDKindAudit)
	if err != nil {
		allocator.active = false
		return err
	}
	working.AuditEvents = append(working.AuditEvents, CRMAuditEvent{
		ID:            auditID,
		Actor:         normalizedMeta.Actor,
		Operation:     normalizedMeta.Operation,
		EntityType:    normalizedMeta.EntityType,
		EntityID:      normalizedMeta.EntityID,
		SourceEventID: normalizedMeta.SourceEventID,
		CreatedAt:     time.Now().UTC(),
	})
	allocator.active = false
	working.Counters = allocator.counters
	if err := validateCRMStateStrict(&working); err != nil {
		return err
	}
	syncDirectory := s.syncDirectory
	if syncDirectory == nil {
		syncDirectory = syncCRMDirectory
	}
	// working was already strict-validated above, so the write skips the
	// redundant full validation of the exact same state.
	if err := writeSnapshotAtomicWithDirectorySync(s.path, working, syncDirectory, true); err != nil {
		return err
	}
	s.state = cloneCRMState(working)
	return nil
}

func reconcileCRMStateV4(previous, current *CRMState, meta CRMUpdateMeta, allocator *IDAllocator) error {
	if previous == nil || current == nil {
		return NewCRMValidationError("state", "is required")
	}
	if err := validateImmutableFundCallbackApprovedActions(previous, current); err != nil {
		return err
	}
	if err := validateImmutableRecruitingRouteSnapshots(previous.MessageEvents, current.MessageEvents); err != nil {
		return err
	}
	if err := validateImmutableJobProvenance(previous.JobProvenance, current.JobProvenance); err != nil {
		return err
	}
	return reconcileCRMStateV3(previous, current, meta, allocator)
}

func validateImmutableFundCallbackApprovedActions(previous, current *CRMState) error {
	previousBindings := previous.FundCallbackApprovedActions
	currentBindings := current.FundCallbackApprovedActions
	if len(currentBindings) < len(previousBindings) {
		return NewCRMValidationError(
			"fund_callback_approved_actions",
			"existing approved action bindings cannot be deleted",
		)
	}
	for i := range previousBindings {
		if !reflect.DeepEqual(currentBindings[i], previousBindings[i]) {
			return NewCRMValidationError(
				indexedField("fund_callback_approved_actions", i),
				"existing approved action binding is immutable",
			)
		}
	}
	previousCallbackIDs := make(map[string]struct{}, len(previous.FundCallbacks))
	for _, callback := range previous.FundCallbacks {
		previousCallbackIDs[callback.ID] = struct{}{}
	}
	for i := len(previousBindings); i < len(currentBindings); i++ {
		if _, existed := previousCallbackIDs[currentBindings[i].FundCallbackID]; existed {
			return NewCRMValidationError(
				indexedField("fund_callback_approved_actions", i)+".fund_callback_id",
				"must reference a callback created in the same update",
			)
		}
	}
	return nil
}

type crmRecruitingRouteSnapshot struct {
	Disposition          MessageDisposition
	ProjectVersion       uint64
	RouteSourceVersion   uint64
	ConversationKind     string
	AllowedTools         []AutomationToolName
	OrganizationName     string
	RequireAuthoritative bool
	SourceGroupID        string
	SourceGroupName      string
	SourceGroupVersion   uint64
	SourceGateEnabled    bool
	SourceGateMode       string
}

func validateImmutableRecruitingRouteSnapshots(previous, current []MessageEvent) error {
	currentByID := make(map[string]MessageEvent, len(current))
	for _, event := range current {
		currentByID[event.ID] = event
	}
	for _, before := range previous {
		after, exists := currentByID[before.ID]
		if before.RecruitingProjectVersion != 0 {
			if !exists {
				return NewCRMConflictError("frozen recruiting route snapshots cannot be deleted")
			}
			if !reflect.DeepEqual(
				crmRecruitingSnapshot(before),
				crmRecruitingSnapshot(after),
			) {
				return NewCRMConflictError("frozen recruiting route snapshots are immutable")
			}
			continue
		}
		if !exists || after.RecruitingProjectVersion == 0 {
			continue
		}
		if after.RecruitingRouteSourceVersion != before.Version ||
			after.Version != before.Version+1 {
			return NewCRMConflictError("recruiting route freeze must increment the source version exactly once")
		}
		beforeBase := before
		afterBase := after
		clearCRMRecruitingSnapshot(&beforeBase)
		clearCRMRecruitingSnapshot(&afterBase)
		beforeBase.Version = 0
		beforeBase.UpdatedAt = time.Time{}
		afterBase.Version = 0
		afterBase.UpdatedAt = time.Time{}
		if !reflect.DeepEqual(beforeBase, afterBase) {
			return NewCRMConflictError("recruiting route freeze cannot change non-routing message fields")
		}
	}
	return nil
}

func crmRecruitingSnapshot(event MessageEvent) crmRecruitingRouteSnapshot {
	return crmRecruitingRouteSnapshot{
		Disposition:          event.Disposition,
		ProjectVersion:       event.RecruitingProjectVersion,
		RouteSourceVersion:   event.RecruitingRouteSourceVersion,
		ConversationKind:     event.RecruitingConversationKind,
		AllowedTools:         append([]AutomationToolName(nil), event.RecruitingAllowedTools...),
		OrganizationName:     event.RecruitingOrganizationName,
		RequireAuthoritative: event.RecruitingRequireAuthoritative,
		SourceGroupID:        event.RecruitingSourceGroupID,
		SourceGroupName:      event.RecruitingSourceGroupName,
		SourceGroupVersion:   event.RecruitingSourceGroupVersion,
		SourceGateEnabled:    event.RecruitingSourceGateEnabled,
		SourceGateMode:       event.RecruitingSourceGateMode,
	}
}

func clearCRMRecruitingSnapshot(event *MessageEvent) {
	event.Disposition = ""
	event.RecruitingProjectVersion = 0
	event.RecruitingRouteSourceVersion = 0
	event.RecruitingConversationKind = ""
	event.RecruitingAllowedTools = nil
	event.RecruitingOrganizationName = ""
	event.RecruitingRequireAuthoritative = false
	event.RecruitingSourceGroupID = ""
	event.RecruitingSourceGroupName = ""
	event.RecruitingSourceGroupVersion = 0
	event.RecruitingSourceGateEnabled = false
	event.RecruitingSourceGateMode = ""
}
