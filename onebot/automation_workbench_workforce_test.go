package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkbenchWorkforceAllOperationsAndExactSchemas(t *testing.T) {
	store := crmPipelineTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	lead := m302HiredLeadWithoutOnboarding(t, store)
	approved := WorkbenchExecutionContext{
		PermissionDecision: AutomationPermissionAllowOnce,
		OperatorID:         "human-operator",
	}

	createdResult := task001Dispatch(t, dispatcher, approved,
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOnboardingsCreate,
		WorkbenchOnboardingCreateArguments{Onboarding: OnboardingCreate{
			LeadID:             lead.ID,
			EntryAddress:       "Site A",
			SiteContact:        "Alice",
			RequiredDocuments:  []string{"ID"},
			CompletedDocuments: []string{},
			MissingInformation: []string{"bank account"},
		}})
	created := m302OnboardingResult(t, createdResult)
	m302AssertOnboardingSchema(t, createdResult.Data)

	entryAddress := "Site B"
	updatedResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "r1-operator"},
		AutomationToolWorkbenchMutate, WorkbenchOperationOnboardingsUpdate,
		WorkbenchOnboardingUpdateArguments{
			OnboardingID: created.ID,
			Update: OnboardingUpdate{
				ExpectedVersion: created.Version,
				EntryAddress:    &entryAddress,
			},
		})
	updated := m302OnboardingResult(t, updatedResult)
	if updated.EntryAddress != entryAddress || updated.Version != created.Version+1 {
		t.Fatalf("onboardings.update = %+v", updated)
	}
	m302AssertOnboardingSchema(t, updatedResult.Data)

	transitionResult := task001Dispatch(t, dispatcher, approved,
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOnboardingsTransition,
		WorkbenchOnboardingTransitionArguments{
			OnboardingID:    updated.ID,
			ExpectedVersion: updated.Version,
			FromStatus:      OnboardingStatusPending,
			ToStatus:        OnboardingStatusCollecting,
		})
	transitioned := m302OnboardingResult(t, transitionResult)
	if transitioned.Status != OnboardingStatusCollecting {
		t.Fatalf("onboardings.transition status = %q", transitioned.Status)
	}
	m302AssertOnboardingSchema(t, transitionResult.Data)

	required := true
	fleetUpdateResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "r1-operator"},
		AutomationToolWorkbenchMutate, WorkbenchOperationFleetUpdate,
		WorkbenchFleetUpdateArguments{
			OnboardingID: transitioned.ID,
			Update: FleetUpdate{
				ExpectedVersion: transitioned.Version,
				Required:        &required,
			},
		})
	fleetUpdated := m302OnboardingResult(t, fleetUpdateResult)
	if !fleetUpdated.Fleet.Required || fleetUpdated.Fleet.Status != FleetStatusPending {
		t.Fatalf("fleet.update = %+v", fleetUpdated.Fleet)
	}
	m302AssertOnboardingSchema(t, fleetUpdateResult.Data)

	fleetTransitionResult := task001Dispatch(t, dispatcher, approved,
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationFleetTransition,
		WorkbenchFleetTransitionArguments{
			OnboardingID:    fleetUpdated.ID,
			ExpectedVersion: fleetUpdated.Version,
			FromStatus:      FleetStatusPending,
			ToStatus:        FleetStatusRequested,
		})
	fleetTransitioned := m302OnboardingResult(t, fleetTransitionResult)
	if fleetTransitioned.Fleet.Status != FleetStatusRequested {
		t.Fatalf("fleet.transition = %+v", fleetTransitioned.Fleet)
	}
	m302AssertOnboardingSchema(t, fleetTransitionResult.Data)

	batchCreateResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "batch-owner"},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesCreate,
		WorkbenchOperationBatchCreateArguments{Batch: OperationBatchCreate{
			Kind:         OperationBatchKindOnboardingTransition,
			TargetStatus: string(OnboardingStatusCollecting),
		}})
	batch := m302BatchResult(t, batchCreateResult)
	if batch.Status != OperationBatchStatusDraft || batch.CreatedBy != "batch-owner" {
		t.Fatalf("operation_batches.create = %+v", batch)
	}
	m302AssertBatchProjectionSchema(t, batchCreateResult.Data)

	target := string(OnboardingStatusReady)
	batchUpdateResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "batch-editor"},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesUpdate,
		WorkbenchOperationBatchUpdateArguments{
			BatchID: batch.ID,
			Update: OperationBatchUpdate{
				ExpectedVersion: batch.Version,
				TargetStatus:    &target,
			},
		})
	batch = m302BatchResult(t, batchUpdateResult)
	if batch.TargetStatus != target {
		t.Fatalf("operation_batches.update = %+v", batch)
	}
	m302AssertBatchProjectionSchema(t, batchUpdateResult.Data)

	membersResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "batch-editor"},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesMembers,
		WorkbenchOperationBatchMembersArguments{
			BatchID: batch.ID,
			Update: OperationBatchMembersUpdate{
				ExpectedVersion:   batch.Version,
				OnboardingCaseIDs: []string{fleetTransitioned.ID},
			},
		})
	batch = m302BatchResult(t, membersResult)
	if len(batch.Members) != 1 || batch.Members[0].OnboardingCaseID != fleetTransitioned.ID {
		t.Fatalf("operation_batches.members = %+v", batch.Members)
	}
	m302AssertBatchProjectionSchema(t, membersResult.Data)

	beforePreview := store.Snapshot()
	previewResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesPreview,
		WorkbenchIDArguments{ID: batch.ID})
	if !previewResult.OK {
		t.Fatalf("operation_batches.preview = %+v", previewResult)
	}
	var preview workbenchOperationBatchPreviewProjection
	if err := json.Unmarshal(previewResult.Data, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.BatchID != batch.ID || len(preview.Items) != 1 {
		t.Fatalf("operation_batches.preview payload = %+v", preview)
	}
	m302AssertJSONKeys(t, previewResult.Data,
		"batch_id", "batch_version", "eligible_total", "ineligible_total", "items",
		"items_returned", "items_total", "items_truncated", "kind", "protected",
		"protected_items_total", "requires_confirmation", "target_status")
	m302AssertJSONKeys(t, m302JSONArrayItem(t, previewResult.Data, "items", 0),
		"after", "before", "current_version", "eligible", "expected_version",
		"error_code", "error_message", "onboarding_case_id", "protected")
	previewItem := m302JSONArrayItem(t, previewResult.Data, "items", 0)
	m302AssertJSONKeys(t, m302JSONObjectField(t, previewItem, "before"),
		"fleet_status", "onboarding_status", "version")
	m302AssertJSONKeys(t, m302JSONObjectField(t, previewItem, "after"),
		"fleet_status", "onboarding_status", "version")
	if afterPreview := store.Snapshot(); !reflect.DeepEqual(afterPreview, beforePreview) {
		t.Fatal("operation_batches.preview mutated CRM or audit state")
	}

	getResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesGet,
		WorkbenchIDArguments{ID: batch.ID})
	gotBatch := m302BatchResult(t, getResult)
	if gotBatch.ID != batch.ID {
		t.Fatalf("operation_batches.get = %+v", gotBatch)
	}
	m302AssertBatchProjectionSchema(t, getResult.Data)

	listResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesList,
		WorkbenchPageArguments{Limit: 1, Offset: 0})
	if !listResult.OK {
		t.Fatalf("operation_batches.list = %+v", listResult)
	}
	m302AssertJSONKeys(t, listResult.Data, "operation_batches")
	m302AssertBatchSummarySchema(t, m302JSONArrayItem(t, listResult.Data, "operation_batches", 0))
	if listResult.Page == nil || listResult.Page.Limit != 1 || listResult.Page.Offset != 0 ||
		listResult.Page.Returned != 1 || listResult.Page.Total != 1 ||
		listResult.Page.HasMore || listResult.Page.NextOffset != 0 {
		t.Fatalf("operation_batches.list page = %+v", listResult.Page)
	}

	executeResult := task001Dispatch(t, dispatcher, approved,
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute,
		WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: batch.Version})
	executed := m302BatchResult(t, executeResult)
	if executed.Status != OperationBatchStatusCompleted || len(executed.Results) != 1 ||
		executed.Results[0].Outcome != OperationBatchOutcomeFailed {
		t.Fatalf("operation_batches.execute = %+v", executed)
	}
	m302AssertBatchProjectionSchema(t, executeResult.Data)
	m302AssertJSONKeys(t, m302JSONArrayItem(t, executeResult.Data, "members", 0),
		"expected_version", "onboarding_case_id")
	m302AssertJSONKeys(t, m302JSONArrayItem(t, executeResult.Data, "results", 0),
		"created_at", "error_code", "error_message", "error_message_truncated",
		"from_status", "onboarding_case_id", "outcome", "to_status")
}

func TestWorkbenchWorkforceBatchListPaginationAndStableOrder(t *testing.T) {
	store := crmPipelineTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	var created []workbenchOperationBatchProjection
	for _, target := range []OnboardingStatus{
		OnboardingStatusCollecting,
		OnboardingStatusReady,
		OnboardingStatusArrived,
	} {
		result := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "operator"},
			AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesCreate,
			WorkbenchOperationBatchCreateArguments{Batch: OperationBatchCreate{
				Kind:         OperationBatchKindOnboardingTransition,
				TargetStatus: string(target),
			}})
		created = append(created, m302BatchResult(t, result))
	}

	result := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesList,
		WorkbenchPageArguments{Limit: 1, Offset: 1})
	if !result.OK {
		t.Fatalf("operation_batches.list = %+v", result)
	}
	var payload struct {
		OperationBatches []workbenchOperationBatchSummary `json:"operation_batches"`
	}
	if err := json.Unmarshal(result.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.OperationBatches) != 1 || payload.OperationBatches[0].ID != created[1].ID {
		t.Fatalf("stable ordered page = %+v, created = %+v", payload.OperationBatches, created)
	}
	if result.Page == nil || result.Page.Limit != 1 || result.Page.Offset != 1 ||
		result.Page.Returned != 1 || result.Page.Total != 3 ||
		!result.Page.HasMore || result.Page.NextOffset != 2 {
		t.Fatalf("page = %+v", result.Page)
	}

	empty := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesList,
		WorkbenchPageArguments{Limit: 100, Offset: 3})
	if !empty.OK || empty.Page == nil || empty.Page.Returned != 0 ||
		empty.Page.Total != 3 || empty.Page.HasMore {
		t.Fatalf("empty terminal page = %+v", empty)
	}
	m302AssertJSONKeys(t, empty.Data, "operation_batches")
}

func TestWorkbenchWorkforceR3ApprovalAndZeroSideEffects(t *testing.T) {
	tests := []struct {
		name      string
		operation WorkbenchOperation
		prepare   func(*testing.T, *CRMStore) any
	}{
		{
			name:      "onboarding create",
			operation: WorkbenchOperationOnboardingsCreate,
			prepare: func(t *testing.T, store *CRMStore) any {
				return WorkbenchOnboardingCreateArguments{Onboarding: OnboardingCreate{
					LeadID: m302HiredLeadWithoutOnboarding(t, store).ID,
				}}
			},
		},
		{
			name:      "onboarding transition",
			operation: WorkbenchOperationOnboardingsTransition,
			prepare: func(t *testing.T, store *CRMStore) any {
				onboarding := m302ExistingOnboarding(t, store)
				return WorkbenchOnboardingTransitionArguments{
					OnboardingID: onboarding.ID, ExpectedVersion: onboarding.Version,
					FromStatus: onboarding.Status, ToStatus: OnboardingStatusCollecting,
				}
			},
		},
		{
			name:      "fleet transition",
			operation: WorkbenchOperationFleetTransition,
			prepare: func(t *testing.T, store *CRMStore) any {
				onboarding := m302ExistingOnboarding(t, store)
				required := true
				onboarding, err := crmFleetUpdate(store, onboarding.ID, FleetUpdate{
					ExpectedVersion: onboarding.Version, Required: &required, Actor: "setup",
				})
				if err != nil {
					t.Fatal(err)
				}
				return WorkbenchFleetTransitionArguments{
					OnboardingID: onboarding.ID, ExpectedVersion: onboarding.Version,
					FromStatus: FleetStatusPending, ToStatus: FleetStatusRequested,
				}
			},
		},
		{
			name:      "batch execute",
			operation: WorkbenchOperationOperationBatchesExecute,
			prepare: func(t *testing.T, store *CRMStore) any {
				onboarding := m302ExistingOnboarding(t, store)
				batch, err := crmOperationBatchCreate(store, OperationBatchCreate{
					Kind: OperationBatchKindOnboardingTransition, TargetStatus: string(OnboardingStatusCollecting),
				}, "setup")
				if err != nil {
					t.Fatal(err)
				}
				batch, err = crmOperationBatchUpdateMembers(store, batch.ID, OperationBatchMembersUpdate{
					ExpectedVersion:   batch.Version,
					OnboardingCaseIDs: []string{onboarding.ID},
				}, "setup")
				if err != nil {
					t.Fatal(err)
				}
				return WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: batch.Version}
			},
		},
	}
	contexts := []struct {
		name string
		ctx  WorkbenchExecutionContext
		code WorkbenchErrorCode
	}{
		{name: "missing", ctx: WorkbenchExecutionContext{}, code: WorkbenchErrorAllowOnceRequired},
		{name: "reusable", ctx: WorkbenchExecutionContext{PermissionDecision: AutomationPermissionAllowRun, OperatorID: "human"}, code: WorkbenchErrorAllowOnceRequired},
		{name: "persistent", ctx: WorkbenchExecutionContext{PermissionDecision: AutomationPermissionAllowPersistent, OperatorID: "human"}, code: WorkbenchErrorAllowOnceRequired},
		{name: "denied", ctx: WorkbenchExecutionContext{PermissionDecision: AutomationPermissionDenyOnce, OperatorID: "human"}, code: WorkbenchErrorForbidden},
		{name: "missing human", ctx: WorkbenchExecutionContext{PermissionDecision: AutomationPermissionAllowOnce}, code: WorkbenchErrorForbidden},
	}

	for _, test := range tests {
		for _, execution := range contexts {
			t.Run(test.name+"/"+execution.name, func(t *testing.T) {
				store := crmPipelineTestStore(t)
				arguments := test.prepare(t, store)
				before := store.Snapshot()
				result := task001Dispatch(t, NewDefaultWorkbenchToolDispatcher(store), execution.ctx,
					AutomationToolWorkbenchAllowOnce, test.operation, arguments)
				task001AssertFailure(t, result, execution.code)
				if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s mutated CRM or audit without fresh human allow_once", test.operation)
				}
			})
		}
	}
}

func TestWorkbenchWorkforceErrorsAndConflicts(t *testing.T) {
	valid := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
		execution WorkbenchExecutionContext
	}{
		{"onboarding create", AutomationToolWorkbenchAllowOnce, WorkbenchOperationOnboardingsCreate, WorkbenchOnboardingCreateArguments{Onboarding: OnboardingCreate{LeadID: "lead_00000001"}}, m302Approved()},
		{"onboarding update", AutomationToolWorkbenchMutate, WorkbenchOperationOnboardingsUpdate, WorkbenchOnboardingUpdateArguments{OnboardingID: "onboard_00000001", Update: OnboardingUpdate{ExpectedVersion: 1, ClearEntryAt: true}}, WorkbenchExecutionContext{}},
		{"onboarding transition", AutomationToolWorkbenchAllowOnce, WorkbenchOperationOnboardingsTransition, WorkbenchOnboardingTransitionArguments{OnboardingID: "onboard_00000001", ExpectedVersion: 1, ToStatus: OnboardingStatusCollecting}, m302Approved()},
		{"fleet update", AutomationToolWorkbenchMutate, WorkbenchOperationFleetUpdate, WorkbenchFleetUpdateArguments{OnboardingID: "onboard_00000001", Update: FleetUpdate{ExpectedVersion: 1, ClearPickupAt: true}}, WorkbenchExecutionContext{}},
		{"fleet transition", AutomationToolWorkbenchAllowOnce, WorkbenchOperationFleetTransition, WorkbenchFleetTransitionArguments{OnboardingID: "onboard_00000001", ExpectedVersion: 1, ToStatus: FleetStatusRequested}, m302Approved()},
		{"batch list", AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesList, WorkbenchPageArguments{Limit: 1}, WorkbenchExecutionContext{}},
		{"batch get", AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesGet, WorkbenchIDArguments{ID: "batch_00000001"}, WorkbenchExecutionContext{}},
		{"batch preview", AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesPreview, WorkbenchIDArguments{ID: "batch_00000001"}, WorkbenchExecutionContext{}},
		{"batch create", AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesCreate, WorkbenchOperationBatchCreateArguments{Batch: OperationBatchCreate{Kind: OperationBatchKindOnboardingTransition, TargetStatus: string(OnboardingStatusCollecting)}}, WorkbenchExecutionContext{}},
		{"batch update", AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesUpdate, WorkbenchOperationBatchUpdateArguments{BatchID: "batch_00000001", Update: OperationBatchUpdate{ExpectedVersion: 1, TargetStatus: m302String(string(OnboardingStatusReady))}}, WorkbenchExecutionContext{}},
		{"batch members", AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesMembers, WorkbenchOperationBatchMembersArguments{BatchID: "batch_00000001", Update: OperationBatchMembersUpdate{ExpectedVersion: 1, OnboardingCaseIDs: []string{"onboard_00000001"}}}, WorkbenchExecutionContext{}},
		{"batch execute", AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute, WorkbenchVersionedIDArguments{ID: "batch_00000001", ExpectedVersion: 1}, m302Approved()},
	}
	nilDispatcher := NewDefaultWorkbenchToolDispatcher(nil)
	for _, test := range valid {
		t.Run("nil CRM/"+test.name, func(t *testing.T) {
			result := task001Dispatch(t, nilDispatcher, test.execution, test.tool, test.operation, test.arguments)
			task001AssertFailure(t, result, WorkbenchErrorUnavailable)
			if !result.Error.Retryable {
				t.Fatalf("%s unavailable error is not retryable", test.operation)
			}
		})
	}

	store := crmPipelineTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	for _, test := range valid {
		if test.operation == WorkbenchOperationOperationBatchesList ||
			test.operation == WorkbenchOperationOperationBatchesCreate ||
			test.operation == WorkbenchOperationOnboardingsCreate {
			continue
		}
		t.Run("not found/"+test.name, func(t *testing.T) {
			result := task001Dispatch(t, dispatcher, test.execution, test.tool, test.operation, test.arguments)
			task001AssertFailure(t, result, WorkbenchErrorNotFound)
		})
	}

	onboarding := m302ExistingOnboarding(t, store)
	staleAddress := "stale"
	staleOnboarding := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationOnboardingsUpdate,
		WorkbenchOnboardingUpdateArguments{OnboardingID: onboarding.ID, Update: OnboardingUpdate{
			ExpectedVersion: onboarding.Version + 1, EntryAddress: &staleAddress,
		}})
	task001AssertFailure(t, staleOnboarding, WorkbenchErrorConflict)
	staleFleet := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationFleetUpdate,
		WorkbenchFleetUpdateArguments{OnboardingID: onboarding.ID, Update: FleetUpdate{
			ExpectedVersion: onboarding.Version + 1, ClearPickupAt: true,
		}})
	task001AssertFailure(t, staleFleet, WorkbenchErrorConflict)
	staleTransition := task001Dispatch(t, dispatcher, m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOnboardingsTransition,
		WorkbenchOnboardingTransitionArguments{
			OnboardingID: onboarding.ID, ExpectedVersion: onboarding.Version + 1,
			FromStatus: onboarding.Status, ToStatus: OnboardingStatusCollecting,
		})
	task001AssertFailure(t, staleTransition, WorkbenchErrorConflict)

	required := true
	fleetReady, err := crmFleetUpdate(store, onboarding.ID, FleetUpdate{
		ExpectedVersion: onboarding.Version, Required: &required, Actor: "setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	staleFleetTransition := task001Dispatch(t, dispatcher, m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationFleetTransition,
		WorkbenchFleetTransitionArguments{
			OnboardingID: fleetReady.ID, ExpectedVersion: fleetReady.Version + 1,
			FromStatus: FleetStatusPending, ToStatus: FleetStatusRequested,
		})
	task001AssertFailure(t, staleFleetTransition, WorkbenchErrorConflict)

	batch, err := crmOperationBatchCreate(store, OperationBatchCreate{
		Kind: OperationBatchKindOnboardingTransition, TargetStatus: string(OnboardingStatusCollecting),
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	target := string(OnboardingStatusReady)
	staleBatch := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesUpdate,
		WorkbenchOperationBatchUpdateArguments{BatchID: batch.ID, Update: OperationBatchUpdate{
			ExpectedVersion: batch.Version + 1, TargetStatus: &target,
		}})
	task001AssertFailure(t, staleBatch, WorkbenchErrorConflict)
	staleMembers := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesMembers,
		WorkbenchOperationBatchMembersArguments{BatchID: batch.ID, Update: OperationBatchMembersUpdate{
			ExpectedVersion: batch.Version + 1, OnboardingCaseIDs: []string{onboarding.ID},
		}})
	task001AssertFailure(t, staleMembers, WorkbenchErrorConflict)

	batch, err = crmOperationBatchUpdateMembers(store, batch.ID, OperationBatchMembersUpdate{
		ExpectedVersion: batch.Version, OnboardingCaseIDs: []string{onboarding.ID},
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	staleExecute := task001Dispatch(t, dispatcher, m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute,
		WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: batch.Version + 1})
	task001AssertFailure(t, staleExecute, WorkbenchErrorConflict)

	invalid := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesCreate,
		WorkbenchOperationBatchCreateArguments{Batch: OperationBatchCreate{
			Kind: OperationBatchKindOnboardingTransition, TargetStatus: "not-a-status",
		}})
	task001AssertFailure(t, invalid, WorkbenchErrorInvalidArguments)

	malformed := json.RawMessage(`{"operation":"operation_batches.list","arguments_json":"{\"limit\":1,\"offset\":0,\"extra\":true}"}`)
	if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, malformed); err == nil {
		t.Fatal("operation_batches.list accepted an unknown field")
	}
	broken := json.RawMessage(`{"operation":"operation_batches.list","arguments_json":"{"}`)
	if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, broken); err == nil {
		t.Fatal("operation_batches.list accepted malformed arguments JSON")
	}

	missingLead := task001Dispatch(t, dispatcher, m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOnboardingsCreate,
		WorkbenchOnboardingCreateArguments{Onboarding: OnboardingCreate{LeadID: "lead_99999999"}})
	task001AssertFailure(t, missingLead, WorkbenchErrorNotFound)
}

func TestWorkbenchWorkforceRejectsBlockedOnboardingBatches(t *testing.T) {
	store := crmPipelineTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	beforeCreate := store.Snapshot()
	createResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "batch-owner"},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesCreate,
		WorkbenchOperationBatchCreateArguments{Batch: OperationBatchCreate{
			Kind:         OperationBatchKindOnboardingTransition,
			TargetStatus: string(OnboardingStatusBlocked),
		}})
	task001AssertFailure(t, createResult, WorkbenchErrorInvalidArguments)
	if len(createResult.Data) != 0 {
		t.Fatalf("blocked batch create returned data: %s", createResult.Data)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, beforeCreate) {
		t.Fatal("blocked batch create persisted CRM or audit state")
	}

	allowedBatch, err := crmOperationBatchCreate(store, OperationBatchCreate{
		Kind:         OperationBatchKindOnboardingTransition,
		TargetStatus: string(OnboardingStatusCollecting),
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	blocked := string(OnboardingStatusBlocked)
	beforeUpdate := store.Snapshot()
	updateResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{OperatorID: "batch-editor"},
		AutomationToolWorkbenchMutate, WorkbenchOperationOperationBatchesUpdate,
		WorkbenchOperationBatchUpdateArguments{
			BatchID: allowedBatch.ID,
			Update: OperationBatchUpdate{
				ExpectedVersion: allowedBatch.Version,
				TargetStatus:    &blocked,
			},
		})
	task001AssertFailure(t, updateResult, WorkbenchErrorInvalidArguments)
	if len(updateResult.Data) != 0 {
		t.Fatalf("blocked batch update returned data: %s", updateResult.Data)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, beforeUpdate) {
		t.Fatal("blocked batch update persisted CRM or audit state")
	}

	onboarding := m302ExistingOnboarding(t, store)
	preexisting, err := crmOperationBatchCreate(store, OperationBatchCreate{
		Kind:         OperationBatchKindOnboardingTransition,
		TargetStatus: string(OnboardingStatusBlocked),
	}, "legacy")
	if err != nil {
		t.Fatal(err)
	}
	preexisting, err = crmOperationBatchUpdateMembers(store, preexisting.ID, OperationBatchMembersUpdate{
		ExpectedVersion:   preexisting.Version,
		OnboardingCaseIDs: []string{onboarding.ID},
	}, "legacy")
	if err != nil {
		t.Fatal(err)
	}

	beforePreview := store.Snapshot()
	previewResult := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationOperationBatchesPreview,
		WorkbenchIDArguments{ID: preexisting.ID})
	task001AssertFailure(t, previewResult, WorkbenchErrorInvalidArguments)
	if len(previewResult.Data) != 0 {
		t.Fatalf("blocked batch preview reported data: %s", previewResult.Data)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, beforePreview) {
		t.Fatal("blocked batch preview mutated CRM or audit state")
	}

	executeResult := task001Dispatch(t, dispatcher, m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute,
		WorkbenchVersionedIDArguments{ID: preexisting.ID, ExpectedVersion: preexisting.Version})
	task001AssertFailure(t, executeResult, WorkbenchErrorInvalidArguments)
	if len(executeResult.Data) != 0 {
		t.Fatalf("blocked batch execute returned data: %s", executeResult.Data)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, beforePreview) {
		t.Fatal("blocked batch execute mutated CRM or audit state")
	}
}

func TestWorkbenchWorkforceBatchPartialFailure(t *testing.T) {
	store := crmPipelineTestStore(t)
	first := m302ExistingOnboarding(t, store)
	second := m302ExistingOnboarding(t, store)
	batch, err := crmOperationBatchCreate(store, OperationBatchCreate{
		Kind: OperationBatchKindOnboardingTransition, TargetStatus: string(OnboardingStatusCollecting),
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	batch, err = crmOperationBatchUpdateMembers(store, batch.ID, OperationBatchMembersUpdate{
		ExpectedVersion: batch.Version,
		Members: []OperationBatchMember{
			{OnboardingCaseID: first.ID, ExpectedVersion: first.Version},
			{OnboardingCaseID: second.ID, ExpectedVersion: second.Version + 1},
		},
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}

	result := task001Dispatch(t, NewDefaultWorkbenchToolDispatcher(store), m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute,
		WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: batch.Version})
	executed := m302BatchResult(t, result)
	if len(executed.Results) != 2 ||
		executed.Results[0].Outcome != OperationBatchOutcomeSucceeded ||
		executed.Results[1].Outcome != OperationBatchOutcomeFailed ||
		executed.Results[1].ErrorCode != "version_conflict" {
		t.Fatalf("partial batch results = %+v", executed.Results)
	}
	state := store.Snapshot()
	firstAfter := state.OnboardingCases[crmOnboardingIndex(state.OnboardingCases, first.ID)]
	secondAfter := state.OnboardingCases[crmOnboardingIndex(state.OnboardingCases, second.ID)]
	if firstAfter.Status != OnboardingStatusCollecting || firstAfter.Version != first.Version+1 {
		t.Fatalf("eligible member = %+v", firstAfter)
	}
	if secondAfter.Status != second.Status || secondAfter.Version != second.Version {
		t.Fatalf("failed member mutated = %+v", secondAfter)
	}
}

func TestWorkbenchWorkforceBatchExecuteExactlyOnceConcurrent(t *testing.T) {
	store := crmPipelineTestStore(t)
	onboarding := m302ExistingOnboarding(t, store)
	batch, err := crmOperationBatchCreate(store, OperationBatchCreate{
		Kind: OperationBatchKindOnboardingTransition, TargetStatus: string(OnboardingStatusCollecting),
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	batch, err = crmOperationBatchUpdateMembers(store, batch.ID, OperationBatchMembersUpdate{
		ExpectedVersion:   batch.Version,
		OnboardingCaseIDs: []string{onboarding.ID},
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	beforeExecuteAudits := m302AuditCount(before, "operation_batch.execute")
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	const workers = 12
	results := make(chan WorkbenchToolResult, workers)
	var wait sync.WaitGroup
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- task001Dispatch(t, dispatcher, m302Approved(),
				AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute,
				WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: batch.Version})
		}()
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for result := range results {
		if result.OK {
			successes++
			continue
		}
		if result.Error != nil && result.Error.Code == WorkbenchErrorConflict {
			conflicts++
			continue
		}
		t.Fatalf("unexpected concurrent execute result = %+v", result)
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("concurrent execute successes=%d conflicts=%d", successes, conflicts)
	}

	after := store.Snapshot()
	gotBatch := after.OperationBatches[crmOperationBatchIndex(after.OperationBatches, batch.ID)]
	gotOnboarding := after.OnboardingCases[crmOnboardingIndex(after.OnboardingCases, onboarding.ID)]
	if gotBatch.Status != OperationBatchStatusCompleted || gotBatch.Version != batch.Version+1 ||
		len(gotBatch.Results) != 1 || gotBatch.Results[0].Outcome != OperationBatchOutcomeSucceeded {
		t.Fatalf("completed batch = %+v", gotBatch)
	}
	if gotOnboarding.Status != OnboardingStatusCollecting ||
		gotOnboarding.Version != onboarding.Version+1 {
		t.Fatalf("onboarding mutation = %+v", gotOnboarding)
	}
	if got := m302AuditCount(after, "operation_batch.execute"); got != beforeExecuteAudits+1 {
		t.Fatalf("execute audit count = %d, want %d", got, beforeExecuteAudits+1)
	}

	repeated := task001Dispatch(t, dispatcher, m302Approved(),
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationOperationBatchesExecute,
		WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: gotBatch.Version})
	task001AssertFailure(t, repeated, WorkbenchErrorConflict)
	final := store.Snapshot()
	if !reflect.DeepEqual(after, final) {
		t.Fatal("repeated terminal execute changed CRM or audit state")
	}
}

func TestWorkbenchWorkforceMaximumOnboardingMutationsStayBounded(t *testing.T) {
	tests := []struct {
		name           string
		tool           AutomationToolName
		operation      WorkbenchOperation
		auditOperation string
		execution      WorkbenchExecutionContext
		arguments      func(OnboardingCase) any
		assertState    func(*testing.T, OnboardingCase)
	}{
		{
			name:           "onboarding update",
			tool:           AutomationToolWorkbenchMutate,
			operation:      WorkbenchOperationOnboardingsUpdate,
			auditOperation: "onboarding.update",
			execution:      WorkbenchExecutionContext{OperatorID: "operator"},
			arguments: func(onboarding OnboardingCase) any {
				entryAddress := m302SizedString("updated-", 500)
				return WorkbenchOnboardingUpdateArguments{
					OnboardingID: onboarding.ID,
					Update: OnboardingUpdate{
						ExpectedVersion: onboarding.Version,
						EntryAddress:    &entryAddress,
					},
				}
			},
			assertState: func(t *testing.T, onboarding OnboardingCase) {
				if onboarding.Status != OnboardingStatusPending {
					t.Fatalf("onboarding status = %q", onboarding.Status)
				}
			},
		},
		{
			name:           "onboarding transition",
			tool:           AutomationToolWorkbenchAllowOnce,
			operation:      WorkbenchOperationOnboardingsTransition,
			auditOperation: "onboarding.transition",
			execution:      m302Approved(),
			arguments: func(onboarding OnboardingCase) any {
				return WorkbenchOnboardingTransitionArguments{
					OnboardingID: onboarding.ID, ExpectedVersion: onboarding.Version,
					FromStatus: onboarding.Status, ToStatus: OnboardingStatusCollecting,
				}
			},
			assertState: func(t *testing.T, onboarding OnboardingCase) {
				if onboarding.Status != OnboardingStatusCollecting {
					t.Fatalf("onboarding status = %q", onboarding.Status)
				}
			},
		},
		{
			name:           "fleet update",
			tool:           AutomationToolWorkbenchMutate,
			operation:      WorkbenchOperationFleetUpdate,
			auditOperation: "onboarding.fleet_update",
			execution:      WorkbenchExecutionContext{OperatorID: "operator"},
			arguments: func(onboarding OnboardingCase) any {
				destination := m302SizedString("updated-", 500)
				return WorkbenchFleetUpdateArguments{
					OnboardingID: onboarding.ID,
					Update: FleetUpdate{
						ExpectedVersion: onboarding.Version,
						Destination:     &destination,
					},
				}
			},
			assertState: func(t *testing.T, onboarding OnboardingCase) {
				if onboarding.Fleet.Status != FleetStatusPending {
					t.Fatalf("fleet status = %q", onboarding.Fleet.Status)
				}
			},
		},
		{
			name:           "fleet transition",
			tool:           AutomationToolWorkbenchAllowOnce,
			operation:      WorkbenchOperationFleetTransition,
			auditOperation: "onboarding.fleet_transition",
			execution:      m302Approved(),
			arguments: func(onboarding OnboardingCase) any {
				return WorkbenchFleetTransitionArguments{
					OnboardingID: onboarding.ID, ExpectedVersion: onboarding.Version,
					FromStatus: onboarding.Fleet.Status, ToStatus: FleetStatusRequested,
				}
			},
			assertState: func(t *testing.T, onboarding OnboardingCase) {
				if onboarding.Fleet.Status != FleetStatusRequested {
					t.Fatalf("fleet status = %q", onboarding.Fleet.Status)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, onboarding := m302MaximumOnboarding(t)
			before := store.Snapshot()
			beforeAudits := m302AuditCount(before, test.auditOperation)
			arguments := test.arguments(onboarding)
			dispatcher := NewDefaultWorkbenchToolDispatcher(store)

			result := task001Dispatch(
				t,
				dispatcher,
				test.execution,
				test.tool,
				test.operation,
				arguments,
			)
			m302AssertBoundedResult(t, result)
			projected := m302OnboardingResult(t, result)
			if projected.Version != onboarding.Version+1 ||
				projected.RequiredDocumentsTotal != 64 ||
				projected.RequiredDocumentsReturned != workbenchWorkforceListItems ||
				!projected.RequiredDocumentsTruncated ||
				projected.MissingInformationTotal != 64 ||
				!projected.MissingInformationTruncated ||
				!projected.EntryAddressTruncated ||
				!projected.Fleet.LuggageNoteTruncated {
				t.Fatalf("maximum onboarding projection = %+v", projected)
			}

			after := store.Snapshot()
			got := after.OnboardingCases[crmOnboardingIndex(after.OnboardingCases, onboarding.ID)]
			if got.Version != onboarding.Version+1 {
				t.Fatalf("persisted onboarding version = %d, want %d", got.Version, onboarding.Version+1)
			}
			test.assertState(t, got)
			if gotAudits := m302AuditCount(after, test.auditOperation); gotAudits != beforeAudits+1 {
				t.Fatalf("%s audit count = %d, want %d", test.auditOperation, gotAudits, beforeAudits+1)
			}

			stale := task001Dispatch(
				t,
				dispatcher,
				test.execution,
				test.tool,
				test.operation,
				arguments,
			)
			task001AssertFailure(t, stale, WorkbenchErrorConflict)
			if final := store.Snapshot(); !reflect.DeepEqual(final, after) {
				t.Fatal("stale onboarding replay changed CRM or audit state")
			}
		})
	}
}

func TestWorkbenchWorkforceBatchListEnvelopePaginationTraversesLargeSet(t *testing.T) {
	store := crmPipelineTestStore(t)
	actor := strings.Repeat("\\", 80)
	for index := 0; index < 100; index++ {
		target := []OnboardingStatus{
			OnboardingStatusCollecting,
			OnboardingStatusReady,
			OnboardingStatusArrived,
		}[index%3]
		if _, err := crmOperationBatchCreate(store, OperationBatchCreate{
			Kind:         OperationBatchKindOnboardingTransition,
			TargetStatus: string(target),
		}, actor); err != nil {
			t.Fatal(err)
		}
	}
	batches, err := crmOperationBatchList(store)
	if err != nil {
		t.Fatal(err)
	}
	allSummaries := make([]workbenchOperationBatchSummary, 0, len(batches))
	expectedIDs := make([]string, 0, len(batches))
	for _, batch := range batches {
		allSummaries = append(allSummaries, workbenchSummarizeOperationBatch(batch))
		expectedIDs = append(expectedIDs, batch.ID)
	}
	oversized := NewWorkbenchToolSuccess(
		WorkbenchOperationOperationBatchesList,
		workbenchOperationBatchListPayload{OperationBatches: allSummaries},
		newWorkbenchPage(100, 0, len(batches), len(batches)),
	)
	task001AssertFailure(t, oversized, WorkbenchErrorResultTooLarge)

	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	before := store.Snapshot()
	var seen []string
	offset := 0
	pages := 0
	for {
		arguments := WorkbenchPageArguments{Limit: 100, Offset: offset}
		result := task001Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationOperationBatchesList,
			arguments,
		)
		m302AssertBoundedResult(t, result)
		if result.Page == nil || result.Page.Returned < 1 ||
			result.Page.Offset != offset || result.Page.Total != len(batches) {
			t.Fatalf("list page = %+v", result.Page)
		}
		replayed := task001Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationOperationBatchesList,
			arguments,
		)
		if !reflect.DeepEqual(replayed, result) {
			t.Fatal("operation batch page replay was not stable")
		}
		var payload workbenchOperationBatchListPayload
		if err := json.Unmarshal(result.Data, &payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.OperationBatches) != result.Page.Returned {
			t.Fatalf("returned summaries = %d, page = %+v", len(payload.OperationBatches), result.Page)
		}
		for _, summary := range payload.OperationBatches {
			seen = append(seen, summary.ID)
		}
		pages++
		if !result.Page.HasMore {
			break
		}
		if result.Page.NextOffset <= offset {
			t.Fatalf("next offset did not advance: %+v", result.Page)
		}
		offset = result.Page.NextOffset
	}
	if pages < 2 {
		t.Fatalf("envelope-aware traversal used %d page, want at least 2", pages)
	}
	if !reflect.DeepEqual(seen, expectedIDs) {
		t.Fatalf("traversed IDs differ: got %d, want %d", len(seen), len(expectedIDs))
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("operation batch list mutated CRM or audit state")
	}
}

func TestWorkbenchWorkforceBatchGetAndPreviewBoundedAt700Members(t *testing.T) {
	store, batch, _ := m302OperationBatchWithMembers(t, 700, 700)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	before := store.Snapshot()

	getResult := task001Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationOperationBatchesGet,
		WorkbenchIDArguments{ID: batch.ID},
	)
	m302AssertBoundedResult(t, getResult)
	got := m302BatchResult(t, getResult)
	if got.MembersTotal != 700 ||
		got.MembersReturned != workbenchWorkforceBatchItems ||
		!got.MembersTruncated ||
		got.ResultsTotal != 0 ||
		got.ResultsTruncated {
		t.Fatalf("700-member batch detail = %+v", got)
	}

	previewResult := task001Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationOperationBatchesPreview,
		WorkbenchIDArguments{ID: batch.ID},
	)
	m302AssertBoundedResult(t, previewResult)
	var preview workbenchOperationBatchPreviewProjection
	if err := json.Unmarshal(previewResult.Data, &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ItemsTotal != 700 ||
		preview.ItemsReturned != workbenchWorkforceBatchItems ||
		!preview.ItemsTruncated ||
		preview.EligibleTotal != 700 ||
		preview.IneligibleTotal != 0 {
		t.Fatalf("700-member batch preview = %+v", preview)
	}

	replayedGet := task001Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationOperationBatchesGet,
		WorkbenchIDArguments{ID: batch.ID},
	)
	replayedPreview := task001Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationOperationBatchesPreview,
		WorkbenchIDArguments{ID: batch.ID},
	)
	if !reflect.DeepEqual(replayedGet, getResult) || !reflect.DeepEqual(replayedPreview, previewResult) {
		t.Fatal("bounded batch read replay was not stable")
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("bounded batch reads mutated CRM or audit state")
	}
}

func TestWorkbenchWorkforceLargeBatchMutationsStayBoundedAndRejectStaleReplay(t *testing.T) {
	t.Run("update", func(t *testing.T) {
		store, batch, _ := m302OperationBatchWithMembers(t, 700, 700)
		before := store.Snapshot()
		beforeAudits := m302AuditCount(before, "operation_batch.update")
		target := string(OnboardingStatusReady)
		arguments := WorkbenchOperationBatchUpdateArguments{
			BatchID: batch.ID,
			Update: OperationBatchUpdate{
				ExpectedVersion: batch.Version,
				TargetStatus:    &target,
			},
		}
		dispatcher := NewDefaultWorkbenchToolDispatcher(store)
		result := task001Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{OperatorID: "operator"},
			AutomationToolWorkbenchMutate,
			WorkbenchOperationOperationBatchesUpdate,
			arguments,
		)
		m302AssertBoundedResult(t, result)
		projected := m302BatchResult(t, result)
		if projected.Version != batch.Version+1 ||
			projected.MembersTotal != 700 ||
			!projected.MembersTruncated ||
			projected.TargetStatus != target {
			t.Fatalf("large batch update projection = %+v", projected)
		}
		after := store.Snapshot()
		if got := m302AuditCount(after, "operation_batch.update"); got != beforeAudits+1 {
			t.Fatalf("update audit count = %d, want %d", got, beforeAudits+1)
		}
		stale := task001Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{OperatorID: "operator"},
			AutomationToolWorkbenchMutate,
			WorkbenchOperationOperationBatchesUpdate,
			arguments,
		)
		task001AssertFailure(t, stale, WorkbenchErrorConflict)
		if final := store.Snapshot(); !reflect.DeepEqual(final, after) {
			t.Fatal("stale batch update replay changed CRM or audit state")
		}
	})

	t.Run("members", func(t *testing.T) {
		store, batch, onboardings := m302OperationBatchWithMembers(t, 701, 700)
		before := store.Snapshot()
		beforeAudits := m302AuditCount(before, "operation_batch.members")
		arguments := WorkbenchOperationBatchMembersArguments{
			BatchID: batch.ID,
			Update: OperationBatchMembersUpdate{
				ExpectedVersion:   batch.Version,
				OnboardingCaseIDs: []string{onboardings[700].ID},
			},
		}
		dispatcher := NewDefaultWorkbenchToolDispatcher(store)
		result := task001Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{OperatorID: "operator"},
			AutomationToolWorkbenchMutate,
			WorkbenchOperationOperationBatchesMembers,
			arguments,
		)
		m302AssertBoundedResult(t, result)
		projected := m302BatchResult(t, result)
		if projected.Version != batch.Version+1 ||
			projected.MembersTotal != 701 ||
			!projected.MembersTruncated {
			t.Fatalf("large batch members projection = %+v", projected)
		}
		after := store.Snapshot()
		if got := m302AuditCount(after, "operation_batch.members"); got != beforeAudits+1 {
			t.Fatalf("members audit count = %d, want %d", got, beforeAudits+1)
		}
		stale := task001Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{OperatorID: "operator"},
			AutomationToolWorkbenchMutate,
			WorkbenchOperationOperationBatchesMembers,
			arguments,
		)
		task001AssertFailure(t, stale, WorkbenchErrorConflict)
		if final := store.Snapshot(); !reflect.DeepEqual(final, after) {
			t.Fatal("stale batch members replay changed CRM or audit state")
		}
	})
}

func TestWorkbenchWorkforceExecute500MembersReturnsBoundedExactlyOnceSummary(t *testing.T) {
	store, batch, onboardings := m302OperationBatchWithMembers(t, 500, 500)
	before := store.Snapshot()
	beforeAudits := m302AuditCount(before, "operation_batch.execute")
	arguments := WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: batch.Version}
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	result := task001Dispatch(
		t,
		dispatcher,
		m302Approved(),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationOperationBatchesExecute,
		arguments,
	)
	m302AssertBoundedResult(t, result)
	projected := m302BatchResult(t, result)
	if projected.Status != OperationBatchStatusCompleted ||
		projected.Version != batch.Version+1 ||
		projected.MembersTotal != 500 ||
		projected.ResultsTotal != 500 ||
		projected.ResultsReturned != workbenchWorkforceBatchItems ||
		!projected.ResultsTruncated ||
		projected.SucceededTotal != 500 ||
		projected.FailedTotal != 0 {
		t.Fatalf("500-member execute projection = %+v", projected)
	}

	after := store.Snapshot()
	persistedBatch := after.OperationBatches[crmOperationBatchIndex(after.OperationBatches, batch.ID)]
	if persistedBatch.Status != OperationBatchStatusCompleted ||
		persistedBatch.Version != batch.Version+1 ||
		len(persistedBatch.Results) != 500 {
		t.Fatalf("persisted 500-member batch = %+v", persistedBatch)
	}
	for _, onboarding := range onboardings {
		persisted := after.OnboardingCases[crmOnboardingIndex(after.OnboardingCases, onboarding.ID)]
		if persisted.Status != OnboardingStatusCollecting ||
			persisted.Version != onboarding.Version+1 {
			t.Fatalf("executed onboarding %s = %+v", onboarding.ID, persisted)
		}
	}
	if got := m302AuditCount(after, "operation_batch.execute"); got != beforeAudits+1 {
		t.Fatalf("execute audit count = %d, want %d", got, beforeAudits+1)
	}

	stale := task001Dispatch(
		t,
		dispatcher,
		m302Approved(),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationOperationBatchesExecute,
		arguments,
	)
	task001AssertFailure(t, stale, WorkbenchErrorConflict)
	terminal := task001Dispatch(
		t,
		dispatcher,
		m302Approved(),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationOperationBatchesExecute,
		WorkbenchVersionedIDArguments{ID: batch.ID, ExpectedVersion: persistedBatch.Version},
	)
	task001AssertFailure(t, terminal, WorkbenchErrorConflict)
	if final := store.Snapshot(); !reflect.DeepEqual(final, after) {
		t.Fatal("execute replay changed CRM or audit state")
	}
}

func m302MaximumOnboarding(t *testing.T) (*CRMStore, OnboardingCase) {
	t.Helper()
	store := crmPipelineTestStore(t)
	onboarding := m302ExistingOnboarding(t, store)
	requiredDocuments := make([]string, 64)
	missingInformation := make([]string, 64)
	for index := range requiredDocuments {
		requiredDocuments[index] = m302SizedString(fmt.Sprintf("required-%02d-", index), 200)
		missingInformation[index] = m302SizedString(fmt.Sprintf("missing-%02d-", index), 500)
	}
	if err := store.UpdateWithMeta(CRMUpdateMeta{
		Actor: "setup", Operation: "test.maximum_onboarding",
		EntityType: "onboarding_case", EntityID: onboarding.ID,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOnboardingIndex(state.OnboardingCases, onboarding.ID)
		now := crmPipelineMutationTime(state.OnboardingCases[index].UpdatedAt)
		current := state.OnboardingCases[index]
		current.EntryAddress = m302SizedString("entry-", 500)
		current.SiteContact = m302SizedString("site-", 160)
		current.RequiredDocuments = append([]string{}, requiredDocuments...)
		current.CompletedDocuments = append([]string{}, requiredDocuments...)
		current.MissingInformation = append([]string{}, missingInformation...)
		current.BlockReason = m302SizedString("block-", 2000)
		current.Fleet = FleetCoordination{
			Required:           true,
			PickupAddress:      m302SizedString("pickup-", 500),
			Destination:        m302SizedString("destination-", 500),
			LuggageNote:        m302SizedString("luggage-", 1000),
			VehicleRequirement: m302SizedString("vehicle-", 500),
			FleetContact:       m302SizedString("contact-", 160),
			DriverName:         m302SizedString("driver-", 160),
			DriverPhone:        m302SizedString("phone-", 40),
			VehiclePlate:       m302SizedString("plate-", 40),
			Status:             FleetStatusPending,
			UpdatedAt:          now,
		}
		current.Version++
		current.UpdatedAt = now
		state.OnboardingCases[index] = current
		onboarding = current
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return store, onboarding
}

func m302OperationBatchWithMembers(
	t *testing.T,
	onboardingCount int,
	memberCount int,
) (*CRMStore, OperationBatch, []OnboardingCase) {
	t.Helper()
	if onboardingCount < 1 || memberCount < 1 || memberCount > onboardingCount {
		t.Fatalf("invalid onboarding/member counts %d/%d", onboardingCount, memberCount)
	}
	store := crmPipelineTestStore(t)
	first := m302ExistingOnboarding(t, store)
	onboardings := []OnboardingCase{first}
	if onboardingCount > 1 {
		if err := store.UpdateWithMeta(CRMUpdateMeta{
			Actor: "setup", Operation: "test.bulk_onboardings",
			EntityType: "onboarding_case", EntityID: first.ID,
		}, func(state *CRMState, allocator *IDAllocator) error {
			lead := state.Leads[crmPipelineLeadIndex(state.Leads, first.LeadID)]
			template := state.OnboardingCases[crmOnboardingIndex(state.OnboardingCases, first.ID)]
			for len(onboardings) < onboardingCount {
				leadID, err := allocator.Next(IDKindLead)
				if err != nil {
					return err
				}
				onboardingID, err := allocator.Next(IDKindOnboarding)
				if err != nil {
					return err
				}
				clonedLead := lead
				clonedLead.ID = leadID
				state.Leads = append(state.Leads, clonedLead)

				cloned := template
				cloned.ID = onboardingID
				cloned.LeadID = leadID
				cloned.RequiredDocuments = append([]string{}, template.RequiredDocuments...)
				cloned.CompletedDocuments = append([]string{}, template.CompletedDocuments...)
				cloned.MissingInformation = append([]string{}, template.MissingInformation...)
				state.OnboardingCases = append(state.OnboardingCases, cloned)
				onboardings = append(onboardings, cloned)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := crmOperationBatchCreate(store, OperationBatchCreate{
		Kind: OperationBatchKindOnboardingTransition, TargetStatus: string(OnboardingStatusCollecting),
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, memberCount)
	for _, onboarding := range onboardings[:memberCount] {
		ids = append(ids, onboarding.ID)
	}
	batch, err = crmOperationBatchUpdateMembers(store, batch.ID, OperationBatchMembersUpdate{
		ExpectedVersion: batch.Version, OnboardingCaseIDs: ids,
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	return store, batch, onboardings
}

func m302SizedString(prefix string, runes int) string {
	if len(prefix) >= runes {
		return prefix[:runes]
	}
	return prefix + strings.Repeat("\\", runes-len(prefix))
}

func m302AssertBoundedResult(t *testing.T, result WorkbenchToolResult) {
	t.Helper()
	if !result.OK {
		t.Fatalf("%s = %+v", result.Operation, result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		t.Fatalf("%s result is %d bytes, max %d", result.Operation, len(encoded), WorkbenchMaxResultJSONBytes)
	}
}

func m302Approved() WorkbenchExecutionContext {
	return WorkbenchExecutionContext{
		PermissionDecision: AutomationPermissionAllowOnce,
		OperatorID:         "human-operator",
	}
}

func m302HiredLeadWithoutOnboarding(t *testing.T, store *CRMStore) Lead {
	t.Helper()
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title: "Onsite", Company: "Example", Location: "Site A",
		RequiredDocuments: []string{"ID"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWithMeta(CRMUpdateMeta{
		Actor: "setup", Operation: "test.hire", EntityType: "lead", EntityID: lead.ID,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineLeadIndex(state.Leads, lead.ID)
		now := time.Now().UTC()
		state.Leads[index].Stage = LeadStageHired
		state.Leads[index].Version++
		state.Leads[index].UpdatedAt = now
		state.Leads[index].StageChangedAt = now
		lead = state.Leads[index]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return lead
}

func m302ExistingOnboarding(t *testing.T, store *CRMStore) OnboardingCase {
	t.Helper()
	lead := m302HiredLeadWithoutOnboarding(t, store)
	onboarding, err := crmOnboardingCreate(store, OnboardingCreate{
		LeadID: lead.ID, RequiredDocuments: []string{}, CompletedDocuments: []string{}, Actor: "setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	return onboarding
}

func m302OnboardingResult(t *testing.T, result WorkbenchToolResult) workbenchOnboardingProjection {
	t.Helper()
	if !result.OK {
		t.Fatalf("%s = %+v", result.Operation, result)
	}
	var onboarding workbenchOnboardingProjection
	if err := json.Unmarshal(result.Data, &onboarding); err != nil {
		t.Fatal(err)
	}
	return onboarding
}

func m302BatchResult(t *testing.T, result WorkbenchToolResult) workbenchOperationBatchProjection {
	t.Helper()
	if !result.OK {
		t.Fatalf("%s = %+v", result.Operation, result)
	}
	var batch workbenchOperationBatchProjection
	if err := json.Unmarshal(result.Data, &batch); err != nil {
		t.Fatal(err)
	}
	return batch
}

func m302AssertOnboardingSchema(t *testing.T, raw json.RawMessage) {
	t.Helper()
	m302AssertJSONKeys(t, raw,
		"block_reason", "block_reason_truncated", "candidate_id",
		"completed_documents", "completed_documents_returned", "completed_documents_total",
		"completed_documents_truncated", "created_at", "entry_address",
		"entry_address_truncated", "fleet", "id", "job_id", "lead_id",
		"missing_information", "missing_information_returned", "missing_information_total",
		"missing_information_truncated", "required_documents",
		"required_documents_returned", "required_documents_total",
		"required_documents_truncated", "site_contact", "site_contact_truncated",
		"status", "updated_at", "version")
	m302AssertJSONKeys(t, m302JSONObjectField(t, raw, "fleet"),
		"destination", "destination_truncated", "driver_name", "driver_name_truncated",
		"driver_phone", "driver_phone_truncated", "fleet_contact",
		"fleet_contact_truncated", "luggage_note", "luggage_note_truncated",
		"pickup_address", "pickup_address_truncated", "required", "status",
		"updated_at", "vehicle_plate", "vehicle_plate_truncated",
		"vehicle_requirement", "vehicle_requirement_truncated")
}

func m302AssertBatchProjectionSchema(t *testing.T, raw json.RawMessage) {
	t.Helper()
	m302AssertJSONKeys(t, raw,
		"created_at", "created_by", "id", "kind", "members",
		"members_returned", "members_total", "members_truncated", "results",
		"results_returned", "results_total", "results_truncated", "status",
		"succeeded_total", "failed_total", "target_status", "updated_at", "version")
}

func m302AssertBatchSummarySchema(t *testing.T, raw json.RawMessage) {
	t.Helper()
	m302AssertJSONKeys(t, raw,
		"created_at", "created_by", "failed_total", "id", "kind",
		"members_total", "results_total", "status", "succeeded_total",
		"target_status", "updated_at", "version")
}

func m302AssertJSONKeys(t *testing.T, raw json.RawMessage, expected ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	actual := make([]string, 0, len(object))
	for key := range object {
		actual = append(actual, key)
	}
	sort.Strings(actual)
	sort.Strings(expected)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("JSON keys = %v, want %v; raw=%s", actual, expected, raw)
	}
}

func m302JSONArrayItem(t *testing.T, raw json.RawMessage, field string, index int) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(object[field], &items); err != nil {
		t.Fatal(err)
	}
	if index < 0 || index >= len(items) {
		t.Fatalf("%s[%d] is missing in %s", field, index, raw)
	}
	return items[index]
}

func m302JSONObjectField(t *testing.T, raw json.RawMessage, field string) json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	value, ok := object[field]
	if !ok {
		t.Fatalf("%s is missing in %s", field, raw)
	}
	return value
}

func m302AuditCount(state CRMState, operation string) int {
	count := 0
	for _, event := range state.AuditEvents {
		if event.Operation == operation {
			count++
		}
	}
	return count
}

func m302String(value string) *string {
	return &value
}
