package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type automationSchedulerTestExecutor struct {
	mu      sync.Mutex
	calls   []string
	results map[string]AutomationExecutionResult
}

func (e *automationSchedulerTestExecutor) Execute(
	ctx context.Context,
	action AutomationAction,
) AutomationExecutionResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, action.ProviderCallID)
	if err := ctx.Err(); err != nil {
		return automationExecutionFailure("context_cancelled", err.Error(), false)
	}
	if result, ok := e.results[action.ProviderCallID]; ok {
		return result
	}
	return AutomationExecutionResult{Status: AutomationActionSucceeded}
}

func (e *automationSchedulerTestExecutor) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func TestAutomationActionSchedulerExecutesTopologicallyIndependentOfStoredOrder(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionApproved,
		nil,
	)
	executor := &automationSchedulerTestExecutor{
		results: map[string]AutomationExecutionResult{
			"parent": automationEntityExecutionSuccess(
				AutomationEntityCandidate,
				"cand_00000001",
				1,
			),
			"child": automationEntityExecutionSuccess(
				AutomationEntityCandidate,
				"cand_00000001",
				2,
			),
		},
	}
	service := automationSchedulerTestService(store, nil, executor)

	if err := service.ExecuteReadyActions(context.Background(), fixture.runID); err != nil {
		t.Fatal(err)
	}
	if got := executor.Calls(); !automationSchedulerTestStringsEqual(got, []string{"parent", "child"}) {
		t.Fatalf("execution order = %v, want [parent child]", got)
	}
	state := store.Snapshot()
	if action := automationSchedulerTestAction(t, state, fixture.parentID); action.Status != AutomationActionSucceeded {
		t.Fatalf("parent status = %s, want succeeded", action.Status)
	}
	if action := automationSchedulerTestAction(t, state, fixture.childID); action.Status != AutomationActionSucceeded {
		t.Fatalf("child status = %s, want succeeded", action.Status)
	}
	if got := automationSchedulerTestAuditSequence(state, fixture.runID); !automationSchedulerTestStringsEqual(
		got,
		[]string{
			AutomationAuditEventActionExecuting + ":parent",
			AutomationAuditEventActionSucceeded + ":parent",
			AutomationAuditEventActionExecuting + ":child",
			AutomationAuditEventActionSucceeded + ":child",
		},
	) {
		t.Fatalf("execution audit sequence = %v", got)
	}
}

func TestAutomationActionSchedulerApprovalResumesDescendants(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionAwaitingReview,
		nil,
	)
	executor := &automationSchedulerTestExecutor{
		results: map[string]AutomationExecutionResult{
			"parent": automationEntityExecutionSuccess(
				AutomationEntityCandidate,
				"cand_00000001",
				1,
			),
			"child": automationEntityExecutionSuccess(
				AutomationEntityCandidate,
				"cand_00000001",
				2,
			),
		},
	}
	service := automationSchedulerTestService(store, nil, executor)

	if err := service.ExecuteReadyActions(context.Background(), fixture.runID); err != nil {
		t.Fatal(err)
	}
	if got := executor.Calls(); len(got) != 0 {
		t.Fatalf("executor calls before approval = %v, want none", got)
	}
	state := store.Snapshot()
	plan, err := automationPlanRunSchedule(state, fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	childState := plan.Projection[fixture.childID]
	if childState.State != automationSchedulePending ||
		childState.NotReadyCode != "dependency_awaiting_review" {
		t.Fatalf("child schedule before approval = %#v", childState)
	}
	request := automationSchedulerTestPermissionRequest(t, state, fixture.parentID)
	parent := automationSchedulerTestAction(t, state, fixture.parentID)
	_, approved, err := service.DecidePermissionRequest(
		context.Background(),
		request.ID,
		automationPermissionDecisionRequest{
			ExpectedVersion:       request.Version,
			ExpectedActionVersion: parent.Version,
			Decision:              AutomationPermissionAllowOnce,
			OperatorID:            "synthetic-operator",
			Reason:                "synthetic approval",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != AutomationActionSucceeded {
		t.Fatalf("approved parent status = %s, want succeeded", approved.Status)
	}
	if got := executor.Calls(); !automationSchedulerTestStringsEqual(got, []string{"parent", "child"}) {
		t.Fatalf("execution order after approval = %v, want [parent child]", got)
	}
}

func TestAutomationActionSchedulerBlocksTerminalDependenciesWithAudit(t *testing.T) {
	tests := []struct {
		name     string
		status   AutomationActionStatus
		wantCode string
	}{
		{"denied", AutomationActionDenied, "dependency_denied"},
		{"failed", AutomationActionFailed, "dependency_failed"},
		{"stale", AutomationActionStale, "dependency_stale"},
		{"unknown", AutomationActionUnknown, "dependency_unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := automationSchedulerTestOpenStore(t, t.TempDir())
			fixture := automationSchedulerTestSeedPair(t, store, test.status, nil)
			executor := &automationSchedulerTestExecutor{}
			service := automationSchedulerTestService(store, nil, executor)

			if err := service.ExecuteReadyActions(context.Background(), fixture.runID); err != nil {
				t.Fatal(err)
			}
			if got := executor.Calls(); len(got) != 0 {
				t.Fatalf("executor calls = %v, want none", got)
			}
			state := store.Snapshot()
			child := automationSchedulerTestAction(t, state, fixture.childID)
			if child.Status != AutomationActionFailed || child.ErrorCode != test.wantCode {
				t.Fatalf("blocked child = %#v, want failed/%s", child, test.wantCode)
			}
			var result map[string]map[string]any
			if err := json.Unmarshal(child.Result, &result); err != nil {
				t.Fatalf("blocked result = %s: %v", child.Result, err)
			}
			if got := result["blocked"]["code"]; got != test.wantCode {
				t.Fatalf("blocked result code = %#v, want %q", got, test.wantCode)
			}
			if !automationSchedulerTestHasAudit(
				state,
				fixture.childID,
				AutomationAuditEventActionBlocked,
				test.wantCode,
			) {
				t.Fatalf("missing %s audit for %s", AutomationAuditEventActionBlocked, test.wantCode)
			}
		})
	}
}

func TestAutomationActionSchedulerBlocksDependencyEntityMismatch(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	wrongResult, err := json.Marshal(AutomationActionResult{
		Entity: &AutomationEntityResult{
			EntityType: AutomationEntityJob,
			EntityID:   "job_00000001",
			Version:    1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionSucceeded,
		wrongResult,
	)
	service := automationSchedulerTestService(store, nil, &automationSchedulerTestExecutor{})

	if err := service.ExecuteReadyActions(context.Background(), fixture.runID); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	child := automationSchedulerTestAction(t, state, fixture.childID)
	if child.Status != AutomationActionFailed ||
		child.ErrorCode != "dependency_entity_mismatch" ||
		!strings.Contains(child.ErrorMessage, "expected candidate") {
		t.Fatalf("entity-mismatch child = %#v", child)
	}
	projection := automationProjectAction(state, child)
	if projection.Scheduler.State != automationScheduleBlocked ||
		projection.NotReadyReason != child.ErrorMessage {
		t.Fatalf("blocked projection = %#v", projection.Scheduler)
	}
}

func TestAutomationActionSchedulerClassifiesInvalidGraphsDeterministically(t *testing.T) {
	now := time.Date(2026, time.July, 25, 10, 0, 0, 0, time.UTC)
	run := AutomationRun{ID: "airun_00000001"}
	otherRun := AutomationRun{ID: "airun_00000002"}
	base := automationSchedulerTestActionFixture(
		"aiact_00000001",
		run.ID,
		"child",
		AutomationToolCandidateUpdate,
		AutomationActionApproved,
		json.RawMessage(`{"candidate":{"from_call":"parent"},"patch":{"summary":"synthetic"}}`),
		[]string{"aiact_00000002"},
		now,
	)
	parent := automationSchedulerTestActionFixture(
		"aiact_00000002",
		run.ID,
		"parent",
		AutomationToolCandidateUpdate,
		AutomationActionApproved,
		json.RawMessage(`{"candidate":{"from_call":"child"},"patch":{"summary":"synthetic parent"}}`),
		[]string{"aiact_00000001"},
		now,
	)

	tests := []struct {
		name     string
		state    AutomationState
		actionID string
		wantCode string
	}{
		{
			name: "missing",
			state: AutomationState{
				Runs:    []AutomationRun{run},
				Actions: []AutomationAction{base},
			},
			actionID: base.ID,
			wantCode: "dependency_missing",
		},
		{
			name: "cross_run",
			state: AutomationState{
				Runs: []AutomationRun{run, otherRun},
				Actions: []AutomationAction{
					base,
					func() AutomationAction {
						value := parent
						value.RunID = otherRun.ID
						value.DependsOnActionIDs = []string{}
						value.Payload = json.RawMessage(`{"candidate":{"id":"cand_00000001","expected_version":1},"patch":{"summary":"other"}}`)
						return value
					}(),
				},
			},
			actionID: base.ID,
			wantCode: "dependency_cross_run",
		},
		{
			name: "cycle",
			state: AutomationState{
				Runs:    []AutomationRun{run},
				Actions: []AutomationAction{base, parent},
			},
			actionID: base.ID,
			wantCode: "dependency_cycle",
		},
		{
			name: "superseded",
			state: AutomationState{
				Runs: []AutomationRun{run},
				Actions: []AutomationAction{
					base,
					func() AutomationAction {
						value := parent
						value.Status = AutomationActionSuperseded
						value.DependsOnActionIDs = []string{}
						value.Payload = json.RawMessage(`{"candidate":{"id":"cand_00000001","expected_version":1},"patch":{"summary":"old"}}`)
						return value
					}(),
				},
			},
			actionID: base.ID,
			wantCode: "dependency_superseded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := automationPlanRunSchedule(test.state, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			projection := plan.Projection[test.actionID]
			if projection.State != automationScheduleBlocked ||
				projection.NotReadyCode != test.wantCode {
				t.Fatalf("projection = %#v, want blocked/%s", projection, test.wantCode)
			}
		})
	}
}

func TestAutomationActionSchedulerAPIProjectionIncludesDependencyState(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionAwaitingReview,
		nil,
	)
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = crm.Close()
	})
	service := automationSchedulerTestService(store, crm, &automationSchedulerTestExecutor{})

	detail, err := service.ActionDetail(fixture.childID)
	if err != nil {
		t.Fatal(err)
	}
	if got := detail["dependency_state"]; got != automationSchedulePending {
		t.Fatalf("dependency_state = %#v, want pending", got)
	}
	reason, _ := detail["not_ready_reason"].(string)
	if !strings.Contains(reason, "awaiting review") {
		t.Fatalf("not_ready_reason = %q", reason)
	}
	dependencies, ok := detail["dependencies"].([]automationDependencyProjection)
	if !ok || len(dependencies) != 1 {
		t.Fatalf("dependencies = %#v", detail["dependencies"])
	}
	if dependencies[0].Status != AutomationActionAwaitingReview ||
		dependencies[0].ExpectedEntityType != AutomationEntityCandidate {
		t.Fatalf("dependency projection = %#v", dependencies[0])
	}

	_, actions, err := service.ListActions("", "", "", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var child automationActionProjection
	for _, action := range actions {
		if action.ID == fixture.childID {
			child = action
			break
		}
	}
	if child.ID == "" || child.Scheduler.NotReadyCode != "dependency_awaiting_review" {
		t.Fatalf("list projection child = %#v", child)
	}
	_, runs, err := service.ListRuns("", "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 ||
		runs[0].ActionSummary.AwaitingReview != 1 ||
		runs[0].ActionSummary.Pending != 1 {
		t.Fatalf("run action summary = %#v", runs)
	}
}

type automationSchedulerTestPair struct {
	runID    string
	parentID string
	childID  string
}

func automationSchedulerTestOpenStore(t *testing.T, dataDir string) *AutomationStore {
	t.Helper()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func automationSchedulerTestSeedPair(
	t *testing.T,
	store *AutomationStore,
	parentStatus AutomationActionStatus,
	parentResult json.RawMessage,
) automationSchedulerTestPair {
	t.Helper()
	now := time.Date(2026, time.July, 25, 10, 0, 0, 0, time.UTC)
	var fixture automationSchedulerTestPair
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		runID, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		childID, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		parentID, err := allocator.Next(AutomationIDKindAction)
		if err != nil {
			return err
		}
		fixture = automationSchedulerTestPair{
			runID:    runID,
			parentID: parentID,
			childID:  childID,
		}
		source := AutomationSourceMessageRef{
			EventID:         "event_00000001",
			MessageKey:      "synthetic-message",
			ExpectedVersion: 1,
			ChatType:        ChatTypePrivate,
			PeerID:          "synthetic-peer",
		}
		started := now.Add(time.Second)
		completed := started.Add(time.Second)
		run := AutomationRun{
			ID:                   runID,
			IdempotencyKey:       "run:scheduler:" + runID,
			EngineVersion:        AutomationEngineVersion,
			ProviderName:         "synthetic-provider",
			SourceMessageEventID: source.EventID,
			SourceMessageKey:     source.MessageKey,
			SourceMessageVersion: source.ExpectedVersion,
			Status:               AutomationRunSucceeded,
			Version:              1,
			Attempt:              1,
			ActionIDs:            []string{childID, parentID},
			CreatedAt:            now,
			StartedAt:            &started,
			CompletedAt:          &completed,
		}
		child := automationSchedulerTestActionFixture(
			childID,
			runID,
			"child",
			AutomationToolCandidateUpdate,
			AutomationActionApproved,
			json.RawMessage(`{"candidate":{"from_call":"parent"},"patch":{"summary":"synthetic child"}}`),
			[]string{parentID},
			completed,
		)
		child.SourceMessage = source
		parent := automationSchedulerTestActionFixture(
			parentID,
			runID,
			"parent",
			AutomationToolCandidateCreate,
			parentStatus,
			json.RawMessage(`{"full_name":"Synthetic Candidate","skills":[]}`),
			[]string{},
			completed,
		)
		parent.SourceMessage = source
		parent.Result = append(json.RawMessage(nil), parentResult...)
		switch parentStatus {
		case AutomationActionAwaitingReview:
			parent.PolicyDecision = AutomationDecisionReview
		case AutomationActionDenied:
			parent.PolicyDecision = AutomationDecisionDeny
			parent.Risk = AutomationRiskR4
		case AutomationActionFailed, AutomationActionStale, AutomationActionUnknown:
			if len(parent.Result) == 0 {
				parent.Result = json.RawMessage(`{}`)
			}
			parent.ErrorCode = "synthetic_parent_" + string(parentStatus)
			parent.ErrorMessage = "synthetic parent ended with " + string(parentStatus)
		}
		state.Runs = append(state.Runs, run)
		state.Actions = append(state.Actions, child, parent)
		state.Idempotency[run.IdempotencyKey] = AutomationIdempotencyRecord{
			Kind:       "run",
			ResourceID: run.ID,
			CreatedAt:  now,
		}
		if parentStatus == AutomationActionAwaitingReview {
			if _, err := PersistAutomationPermissionRequest(state, allocator, parent, completed); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func automationSchedulerTestActionFixture(
	id string,
	runID string,
	callID string,
	tool AutomationToolName,
	status AutomationActionStatus,
	payload json.RawMessage,
	dependencies []string,
	now time.Time,
) AutomationAction {
	return AutomationAction{
		ID:                 id,
		RunID:              runID,
		ProviderCallID:     callID,
		Tool:               tool,
		Status:             status,
		Risk:               AutomationRiskR1,
		PolicyDecision:     AutomationDecisionAutoApprove,
		PolicyVersion:      1,
		RiskReasons:        []string{},
		Confidence:         1,
		Evidence:           []AutomationEvidence{},
		SourceMessage:      AutomationSourceMessageRef{},
		Preconditions:      []AutomationEntityPrecondition{},
		DependsOnActionIDs: append([]string{}, dependencies...),
		Payload:            append(json.RawMessage(nil), payload...),
		Attempt:            1,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func automationSchedulerTestService(
	store *AutomationStore,
	crm *CRMStore,
	executor AutomationCommandExecutor,
) *AutomationService {
	return &AutomationService{
		store:    store,
		crm:      crm,
		executor: executor,
		events:   NewAutomationRuntimeEventBroker(64),
	}
}

func automationSchedulerTestAction(
	t *testing.T,
	state AutomationState,
	actionID string,
) AutomationAction {
	t.Helper()
	index := automationActionIndex(&state, actionID)
	if index < 0 {
		t.Fatalf("action %s not found", actionID)
	}
	return state.Actions[index]
}

func automationSchedulerTestPermissionRequest(
	t *testing.T,
	state AutomationState,
	actionID string,
) AutomationPermissionRequest {
	t.Helper()
	for _, request := range state.PermissionRequests {
		if request.ActionID == actionID {
			return request
		}
	}
	t.Fatalf("permission request for %s not found", actionID)
	return AutomationPermissionRequest{}
}

func automationSchedulerTestAuditSequence(state AutomationState, runID string) []string {
	sequence := make([]string, 0)
	for _, record := range state.PendingAudit {
		if record.RunID != runID {
			continue
		}
		if record.EventType != AutomationAuditEventActionExecuting &&
			record.EventType != AutomationAuditEventActionSucceeded {
			continue
		}
		action := automationSchedulerTestActionByID(state, record.ActionID)
		sequence = append(sequence, record.EventType+":"+action.ProviderCallID)
	}
	return sequence
}

func automationSchedulerTestHasAudit(
	state AutomationState,
	actionID string,
	eventType string,
	code string,
) bool {
	for _, record := range state.PendingAudit {
		if record.ActionID != actionID || record.EventType != eventType {
			continue
		}
		var data map[string]any
		if json.Unmarshal(record.Data, &data) == nil && data["not_ready_code"] == code {
			return true
		}
	}
	return false
}

func automationSchedulerTestActionByID(state AutomationState, actionID string) AutomationAction {
	index := automationActionIndex(&state, actionID)
	if index < 0 {
		return AutomationAction{}
	}
	return state.Actions[index]
}

func automationSchedulerTestStringsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
