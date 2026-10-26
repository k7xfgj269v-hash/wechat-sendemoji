package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomationSupervisionDenialBlocksDependentActions(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionAwaitingReview,
		nil,
	)
	service := automationSchedulerTestService(store, nil, &automationSchedulerTestExecutor{})
	state := store.Snapshot()
	request := automationSchedulerTestPermissionRequest(t, state, fixture.parentID)
	parent := automationSchedulerTestAction(t, state, fixture.parentID)

	decided, denied, err := service.DecidePermissionRequest(
		context.Background(),
		request.ID,
		automationPermissionDecisionRequest{
			ExpectedVersion:       request.Version,
			ExpectedActionVersion: parent.Version,
			Decision:              AutomationPermissionDenyOnce,
			OperatorID:            "synthetic-operator",
			Reason:                "synthetic denial",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Status != AutomationPermissionStatusDenied ||
		denied.Status != AutomationActionDenied {
		t.Fatalf("denial result = %#v / %#v", decided, denied)
	}
	state = store.Snapshot()
	child := automationSchedulerTestAction(t, state, fixture.childID)
	if child.Status != AutomationActionFailed || child.ErrorCode != "dependency_denied" {
		t.Fatalf("dependent action after denial = %#v", child)
	}
	if !automationSchedulerTestHasAudit(
		state,
		fixture.childID,
		AutomationAuditEventActionBlocked,
		"dependency_denied",
	) {
		t.Fatal("dependent action denial did not persist action.blocked audit")
	}
}

func TestAutomationPermissionRequestRouteProjectsSchedulerState(t *testing.T) {
	store := automationSchedulerTestOpenStore(t, t.TempDir())
	fixture := automationSchedulerTestSeedPair(
		t,
		store,
		AutomationActionAwaitingReview,
		nil,
	)
	service := automationSchedulerTestService(store, nil, &automationSchedulerTestExecutor{})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/automation/permission_requests?status=pending&limit=50&offset=0",
		nil,
	)
	automationPermissionRequestsHandler(service)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("permission request status = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		PermissionRequests []automationPermissionRequestProjection `json:"permission_requests"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.PermissionRequests) != 1 {
		t.Fatalf("permission request projection = %+v", envelope)
	}
	projected := envelope.PermissionRequests[0]
	if projected.ActionID != fixture.parentID ||
		projected.Action == nil ||
		projected.Action.Scheduler.State != automationScheduleAwaitingReview ||
		projected.Action.Scheduler.NotReadyReason == "" {
		t.Fatalf("permission action projection = %+v", projected)
	}
}

func TestPersistentHighRiskOutboundScopeMatchesPeerAcrossMessages(t *testing.T) {
	t.Setenv(automationPersistentHighRiskSendEnv, "1")
	action := AutomationAction{
		Tool:      AutomationToolWorkbenchAllowOnce,
		Operation: WorkbenchOperationMessagesSendPrivate,
		Payload:   json.RawMessage(`{"event_id":"event_1","text":"first"}`),
		Recipient: &AutomationRecipient{
			ChatType:   ChatTypePrivate,
			PeerID:     "wxid_alpha",
			AudienceID: "event_1",
		},
	}
	scope, key, err := automationPermissionScope(action, false)
	if err != nil {
		t.Fatal(err)
	}

	state := AutomationState{
		PermissionRules: []AutomationPermissionRule{{
			ID:        "aipermrule_test",
			Effect:    AutomationPermissionEffectAllow,
			Tool:      action.Tool,
			Operation: action.Operation,
			ScopeKey:  key,
			Scope:     scope,
			Enabled:   true,
		}},
	}
	next := action
	next.Payload = json.RawMessage(`{"event_id":"event_2","text":"second"}`)
	next.Recipient = &AutomationRecipient{
		ChatType:   ChatTypePrivate,
		PeerID:     "wxid_alpha",
		AudienceID: "event_2",
	}
	if effect, ruleID, matched := matchAutomationPermissionRule(&state, next); !matched ||
		effect != AutomationPermissionEffectAllow || ruleID != "aipermrule_test" {
		t.Fatalf("same-peer persistent rule match = %q, %q, %v", effect, ruleID, matched)
	}

	otherPeer := next
	otherPeer.Recipient = &AutomationRecipient{
		ChatType:   ChatTypePrivate,
		PeerID:     "wxid_other",
		AudienceID: "event_3",
	}
	if _, _, matched := matchAutomationPermissionRule(&state, otherPeer); matched {
		t.Fatal("persistent outbound rule matched a different peer")
	}
}

func TestPersistentHighRiskOutboundApprovalPersistsRule(t *testing.T) {
	t.Setenv(automationPersistentHighRiskSendEnv, "1")
	client, automation, _, service, _, turn := workbenchE2EOutboundFixture(
		t,
		ChatTypePrivate,
		"wxid_alpha",
		WorkbenchOperationMessagesSendPrivate,
	)
	request, action := workbenchE2EPendingPermission(t, automation, turn.ID)
	expectedScope, expectedScopeKey, err := automationPermissionScope(action, false)
	if err != nil {
		t.Fatal(err)
	}

	decided, executed, err := service.DecidePermissionRequest(
		context.Background(),
		request.ID,
		automationPermissionDecisionRequest{
			ExpectedVersion:       request.Version,
			ExpectedActionVersion: action.Version,
			Decision:              AutomationPermissionAllowPersistent,
			OperatorID:            "operator-persistent",
			Reason:                "approved for the synthetic outbound peer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Decision != AutomationPermissionAllowPersistent ||
		decided.Status != AutomationPermissionStatusAllowed ||
		decided.RuleID == "" ||
		executed.Status != AutomationActionSucceeded ||
		client.callCount("message.send_controlled") != 1 {
		t.Fatalf("persistent approval result = %+v / %+v, calls=%d",
			decided, executed, client.callCount("message.send_controlled"))
	}

	state := automation.Snapshot()
	if len(state.PermissionRules) != 1 {
		t.Fatalf("permission rules after approval = %+v", state.PermissionRules)
	}
	rule := state.PermissionRules[0]
	if rule.ID != decided.RuleID ||
		rule.Tool != AutomationToolWorkbenchAllowOnce ||
		rule.Operation != WorkbenchOperationMessagesSendPrivate ||
		rule.ScopeKey != expectedScopeKey ||
		string(rule.Scope) != string(expectedScope) ||
		rule.CreatedBy != "operator-persistent" ||
		!rule.Enabled {
		t.Fatalf("persisted permission rule = %+v", rule)
	}

	reopened, err := OpenAutomationStore(filepath.Dir(automation.path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedState := reopened.Snapshot()
	if len(reopenedState.PermissionRules) != 1 ||
		reopenedState.PermissionRules[0].ID != rule.ID ||
		reopenedState.PermissionRules[0].ScopeKey != rule.ScopeKey {
		t.Fatalf("reopened permission rules = %+v", reopenedState.PermissionRules)
	}
}

func TestPersistentHighRiskOutboundRuleAuthorizesLaterAction(t *testing.T) {
	t.Setenv(automationPersistentHighRiskSendEnv, "1")
	client, automation, _, service, _, turn := workbenchE2EOutboundFixture(
		t,
		ChatTypePrivate,
		"wxid_alpha",
		WorkbenchOperationMessagesSendPrivate,
	)
	request, action := workbenchE2EPendingPermission(t, automation, turn.ID)
	scope, scopeKey, err := automationPermissionScope(action, false)
	if err != nil {
		t.Fatal(err)
	}

	var ruleID string
	err = automation.Update(func(
		state *AutomationState,
		allocator *AutomationIDAllocator,
	) error {
		ruleID, err = allocator.Next(AutomationIDKindPermissionRule)
		if err != nil {
			return err
		}
		now := automationMutationTime(action.UpdatedAt)
		state.PermissionRules = append(state.PermissionRules, AutomationPermissionRule{
			ID:        ruleID,
			Effect:    AutomationPermissionEffectAllow,
			Tool:      action.Tool,
			Operation: action.Operation,
			ScopeKey:  scopeKey,
			Scope:     scope,
			Enabled:   true,
			Version:   1,
			CreatedBy: "operator-persistent",
			CreatedAt: now,
			UpdatedAt: now,
		})
		requests := state.PermissionRequests[:0]
		for _, current := range state.PermissionRequests {
			if current.ID != request.ID {
				requests = append(requests, current)
			}
		}
		state.PermissionRequests = requests
		index := automationActionIndex(state, action.ID)
		if index < 0 {
			t.Fatal("persistent outbound action is missing")
		}
		state.Actions[index].Status = AutomationActionApproved
		state.Actions[index].Version++
		state.Actions[index].UpdatedAt = now
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ExecuteReadyActions(context.Background(), action.RunID); err != nil {
		t.Fatal(err)
	}

	state := automation.Snapshot()
	index := automationActionIndex(&state, action.ID)
	if index < 0 ||
		state.Actions[index].Status != AutomationActionSucceeded ||
		client.callCount("message.send_controlled") != 1 {
		t.Fatalf("persistent rule execution = %+v, calls=%d",
			state.Actions, client.callCount("message.send_controlled"))
	}
	var authorization *AutomationPermissionRequest
	for i := range state.PermissionRequests {
		if state.PermissionRequests[i].ActionID == action.ID {
			authorization = &state.PermissionRequests[i]
			break
		}
	}
	if authorization == nil ||
		authorization.Status != AutomationPermissionStatusAllowed ||
		authorization.Decision != AutomationPermissionAllowPersistent ||
		authorization.RuleID != ruleID ||
		authorization.OperatorID != "operator-persistent" {
		t.Fatalf("persistent rule authorization = %+v", authorization)
	}
}

func TestPersistentHighRiskOutboundScopeRemainsDisabledByDefault(t *testing.T) {
	t.Setenv(automationPersistentHighRiskSendEnv, "")
	action := AutomationAction{
		Tool:      AutomationToolWorkbenchAllowOnce,
		Operation: WorkbenchOperationMessagesSendPrivate,
		Payload:   json.RawMessage(`{"event_id":"event_1","text":"first"}`),
		Recipient: &AutomationRecipient{
			ChatType:   ChatTypePrivate,
			PeerID:     "wxid_alpha",
			AudienceID: "event_1",
		},
	}
	scope, key, err := automationPermissionScope(action, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(scope), "arguments_sha256") {
		t.Fatalf("default outbound scope is not action-bound: %s", scope)
	}
	if _, _, matched := matchAutomationPermissionRule(&AutomationState{
		PermissionRules: []AutomationPermissionRule{{
			Effect:    AutomationPermissionEffectAllow,
			Tool:      action.Tool,
			Operation: action.Operation,
			Scope:     scope,
			ScopeKey:  key,
			Enabled:   true,
		}},
	}, action); matched {
		t.Fatal("disabled persistent outbound rule matched")
	}
}
