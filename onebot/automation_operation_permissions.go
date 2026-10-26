package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Operator-managed allow/deny overrides for implemented workbench operations.
//
// Only operations whose hardcoded catalog availability is "available" (i.e. the
// dispatcher actually implements them) can be denied; unimplemented operations
// stay unavailable regardless. The runtime deny set is consulted by
// WorkbenchOperationCatalogEntryFor, so both the policy gate and the dispatcher
// gate honor it from a single point.

const automationOperationPermissionsFileName = "operation_permissions.json"

var (
	workbenchOperationDenyMu  sync.RWMutex
	workbenchOperationDenySet = map[string]struct{}{}
)

func workbenchOperationDenyKey(tool AutomationToolName, operation WorkbenchOperation) string {
	return string(tool) + "|" + string(operation)
}

// workbenchOperationOverrideDenied reports whether the operator denied this
// operation. Safe for concurrent use.
func workbenchOperationOverrideDenied(tool AutomationToolName, operation WorkbenchOperation) bool {
	workbenchOperationDenyMu.RLock()
	defer workbenchOperationDenyMu.RUnlock()
	if len(workbenchOperationDenySet) == 0 {
		return false
	}
	_, denied := workbenchOperationDenySet[workbenchOperationDenyKey(tool, operation)]
	return denied
}

func applyWorkbenchOperationDenySet(denied map[string]struct{}) {
	workbenchOperationDenyMu.Lock()
	workbenchOperationDenySet = denied
	workbenchOperationDenyMu.Unlock()
}

// AutomationOperationPermissionRef identifies one operation.
type AutomationOperationPermissionRef struct {
	Tool      AutomationToolName `json:"tool"`
	Operation WorkbenchOperation `json:"operation"`
}

// AutomationOperationPermissionEntry is one row in the operator view.
type AutomationOperationPermissionEntry struct {
	Tool                 AutomationToolName           `json:"tool"`
	Operation            WorkbenchOperation           `json:"operation"`
	Label                string                       `json:"label"`
	Description          string                       `json:"description"`
	Risk                 AutomationRisk               `json:"risk"`
	ImplementationStatus WorkbenchImplementationState `json:"implementation_status"`
	Exposure             WorkbenchExposure            `json:"exposure"`
	Implemented          bool                         `json:"implemented"`
	Denied               bool                         `json:"denied"`
}

// AutomationOperationPermissionsView is the GET payload.
type AutomationOperationPermissionsView struct {
	Version    uint64                               `json:"version"`
	Operations []AutomationOperationPermissionEntry `json:"operations"`
}

// AutomationOperationPermissionsUpdate is the PUT payload.
type AutomationOperationPermissionsUpdate struct {
	ExpectedVersion uint64                             `json:"expected_version"`
	Denied          []AutomationOperationPermissionRef `json:"denied"`
}

type automationOperationPermissionsFile struct {
	Version uint64   `json:"version"`
	Denied  []string `json:"denied_operations"`
}

// AutomationOperationPermissionStore persists operator overrides and keeps the
// runtime deny set in sync.
type AutomationOperationPermissionStore struct {
	mu      sync.Mutex
	path    string
	version uint64
	denied  map[string]struct{}
}

// NewAutomationOperationPermissionStore loads any persisted overrides from
// dataDir and applies them to the runtime.
func NewAutomationOperationPermissionStore(dataDir string) (*AutomationOperationPermissionStore, error) {
	store := &AutomationOperationPermissionStore{
		path:   filepath.Join(strings.TrimSpace(dataDir), automationOperationPermissionsFileName),
		denied: map[string]struct{}{},
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	store.apply()
	return store, nil
}

func (s *AutomationOperationPermissionStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read operation permissions: %w", err)
	}
	var file automationOperationPermissionsFile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return fmt.Errorf("decode operation permissions: %w", err)
	}
	s.version = file.Version
	s.denied = map[string]struct{}{}
	for _, key := range file.Denied {
		key = strings.TrimSpace(key)
		if key != "" {
			s.denied[key] = struct{}{}
		}
	}
	return nil
}

// apply copies the current deny set into the process-wide runtime override.
func (s *AutomationOperationPermissionStore) apply() {
	snapshot := make(map[string]struct{}, len(s.denied))
	for key := range s.denied {
		snapshot[key] = struct{}{}
	}
	applyWorkbenchOperationDenySet(snapshot)
}

func (s *AutomationOperationPermissionStore) persistLocked() error {
	keys := make([]string, 0, len(s.denied))
	for key := range s.denied {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	payload, err := json.MarshalIndent(automationOperationPermissionsFile{
		Version: s.version,
		Denied:  keys,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode operation permissions: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write operation permissions: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("commit operation permissions: %w", err)
	}
	return nil
}

// buildAutomationOperationPermissionsView renders every catalog operation with
// its implemented/denied state. It takes no locks, so callers must pass a
// consistent snapshot of version and denied.
func buildAutomationOperationPermissionsView(
	version uint64,
	denied map[string]struct{},
) AutomationOperationPermissionsView {
	var entries []AutomationOperationPermissionEntry
	for _, definition := range workbenchAllOperationDefinitions() {
		risk, _, _, _, _, _ := workbenchToolMetadata(definition.Tool)
		key := workbenchOperationDenyKey(definition.Tool, definition.Operation)
		implemented := workbenchOperationBaseAvailable(definition)
		_, isDenied := denied[key]
		entries = append(entries, AutomationOperationPermissionEntry{
			Tool:                 definition.Tool,
			Operation:            definition.Operation,
			Label:                definition.Label,
			Description:          definition.Description,
			Risk:                 risk,
			ImplementationStatus: definition.State,
			Exposure:             definition.Exposure,
			Implemented:          implemented,
			Denied:               implemented && isDenied,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Tool != entries[j].Tool {
			return entries[i].Tool < entries[j].Tool
		}
		return entries[i].Operation < entries[j].Operation
	})
	return AutomationOperationPermissionsView{Version: version, Operations: entries}
}

// View returns the full operator-facing operation list with implemented/denied
// state for every catalog operation.
func (s *AutomationOperationPermissionStore) View() AutomationOperationPermissionsView {
	s.mu.Lock()
	version := s.version
	denied := make(map[string]struct{}, len(s.denied))
	for key := range s.denied {
		denied[key] = struct{}{}
	}
	s.mu.Unlock()
	return buildAutomationOperationPermissionsView(version, denied)
}

// Update replaces the deny set with the requested operations. Only implemented
// operations may be denied; unknown or unimplemented operations are rejected.
func (s *AutomationOperationPermissionStore) Update(
	update AutomationOperationPermissionsUpdate,
) (AutomationOperationPermissionsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if update.ExpectedVersion != s.version {
		return AutomationOperationPermissionsView{}, ErrAutomationProviderConfigConflict
	}
	next := map[string]struct{}{}
	for _, ref := range update.Denied {
		if !IsValidWorkbenchOperation(ref.Tool, ref.Operation) {
			return AutomationOperationPermissionsView{}, NewAutomationValidationError(
				"denied.operation",
				fmt.Sprintf("unknown operation %q for tool %q", ref.Operation, ref.Tool),
			)
		}
		definition, _ := WorkbenchOperationDefinitionFor(ref.Tool, ref.Operation)
		if !workbenchOperationBaseAvailable(definition) {
			return AutomationOperationPermissionsView{}, NewAutomationValidationError(
				"denied.operation",
				fmt.Sprintf("operation %q is not implemented and cannot be toggled", ref.Operation),
			)
		}
		next[workbenchOperationDenyKey(ref.Tool, ref.Operation)] = struct{}{}
	}

	s.version++
	s.denied = next
	if err := s.persistLocked(); err != nil {
		s.version--
		return AutomationOperationPermissionsView{}, err
	}
	s.apply()
	// buildAutomationOperationPermissionsView takes no locks; safe under s.mu.
	return buildAutomationOperationPermissionsView(s.version, s.denied), nil
}
