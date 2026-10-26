package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkbenchIntrospectionRegistryContract(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	operations := []WorkbenchOperation{
		WorkbenchOperationAutomationSessionsList,
		WorkbenchOperationAutomationSessionsGet,
		WorkbenchOperationAutomationRunsList,
		WorkbenchOperationAutomationRunsGet,
		WorkbenchOperationAutomationActionsList,
		WorkbenchOperationAutomationActionsGet,
		WorkbenchOperationAutomationAuditList,
	}
	for _, operation := range operations {
		definition, ok := WorkbenchOperationDefinitionFor(AutomationToolWorkbenchQuery, operation)
		if !ok ||
			definition.State != WorkbenchOperationWired ||
			definition.Exposure != WorkbenchExposureAllowed ||
			definition.Handler == nil ||
			!workbenchOperationEffectiveAvailable(definition) {
			t.Fatalf("introspection definition %s = %+v, found=%t", operation, definition, ok)
		}
	}

	stateCounts := map[WorkbenchImplementationState]int{}
	forbidden := 0
	dispatchable := 0
	for _, definition := range workbenchAllOperationDefinitions() {
		stateCounts[definition.State]++
		if definition.Exposure == WorkbenchExposureForbidden {
			forbidden++
		}
		if workbenchOperationEffectiveAvailable(definition) {
			dispatchable++
		}
	}
	if len(workbenchAllOperationDefinitions()) != 98 ||
		stateCounts[WorkbenchOperationWired] != 91 ||
		stateCounts[WorkbenchOperationBackingReady] != 7 ||
		stateCounts[WorkbenchOperationNewRequired] != 0 ||
		forbidden != 7 {
		t.Fatalf("final registry = states %#v forbidden=%d", stateCounts, forbidden)
	}
	available := 0
	for _, tool := range WorkbenchAvailableToolCatalog().Tools {
		available += len(tool.Operations)
	}
	if available != dispatchable || available != 91 {
		t.Fatalf("available=%d dispatchable=%d, want 91", available, dispatchable)
	}
}

func TestWorkbenchIntrospectionCanonicalActionID(t *testing.T) {
	state := m401BaseState()
	dispatcher := m401Dispatcher(m401Store(state), nil)

	decoded, err := DecodeWorkbenchToolRequest(
		AutomationToolWorkbenchQuery,
		workbenchTestWrapper(
			t,
			WorkbenchOperationAutomationActionsGet,
			`{"id":"aiact_00000001"}`,
		),
	)
	if err != nil {
		t.Fatalf("decode canonical action ID: %v", err)
	}
	result := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
	if !result.OK {
		t.Fatalf("canonical action dispatch = %+v", result)
	}
	var detail workbenchAutomationActionDetailProjection
	m401DecodeData(t, result, &detail)
	if detail.Action.ID != "aiact_00000001" {
		t.Fatalf("dispatched action = %q", detail.Action.ID)
	}

	_, err = DecodeWorkbenchToolRequest(
		AutomationToolWorkbenchQuery,
		workbenchTestWrapper(
			t,
			WorkbenchOperationAutomationActionsGet,
			`{"id":"aiaction_00000001"}`,
		),
	)
	if !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("legacy action ID error = %v", err)
	}
}

func TestWorkbenchIntrospectionListFiltersAndPagination(t *testing.T) {
	state := m401BaseState()
	dispatcher := m401Dispatcher(m401Store(state), nil)

	sessions := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 1, Offset: 0},
	)
	if !sessions.OK || sessions.Page == nil ||
		sessions.Page.Total != 3 || sessions.Page.Returned != 1 ||
		!sessions.Page.HasMore || sessions.Page.NextOffset != 1 {
		t.Fatalf("session page = %+v", sessions)
	}
	var sessionPage workbenchAutomationSessionListProjection
	m401DecodeData(t, sessions, &sessionPage)
	if len(sessionPage.Sessions) != 1 || sessionPage.Sessions[0].ID != "aisession_00000002" {
		t.Fatalf("session page order = %#v", sessionPage.Sessions)
	}
	second := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 1, Offset: 1},
	)
	m401DecodeData(t, second, &sessionPage)
	if len(sessionPage.Sessions) != 1 || sessionPage.Sessions[0].ID != "aisession_00000001" {
		t.Fatalf("second session page = %#v", sessionPage.Sessions)
	}
	beyond := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 5, Offset: 99},
	)
	m401DecodeData(t, beyond, &sessionPage)
	if !beyond.OK || beyond.Page == nil || beyond.Page.Total != 3 ||
		beyond.Page.Returned != 0 || beyond.Page.HasMore || len(sessionPage.Sessions) != 0 {
		t.Fatalf("beyond session page = %+v data=%#v", beyond, sessionPage)
	}

	activeCandidate := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{
			Status: string(AutomationSessionStatusActive),
			Tool:   AutomationToolCandidateCreate,
			Limit:  100,
		},
	)
	m401DecodeData(t, activeCandidate, &sessionPage)
	if got := m401SessionIDs(sessionPage.Sessions); !reflect.DeepEqual(got, []string{"aisession_00000001"}) {
		t.Fatalf("relational session filter = %#v", got)
	}
	empty := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Tool: AutomationToolNoteCreate, Limit: 100},
	)
	m401DecodeData(t, empty, &sessionPage)
	if empty.Page == nil || empty.Page.Total != 0 || len(sessionPage.Sessions) != 0 {
		t.Fatalf("empty session filter = %+v data=%#v", empty, sessionPage)
	}

	runs := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationRunsList,
		WorkbenchAutomationListArguments{Tool: AutomationToolCandidateCreate, Limit: 100},
	)
	var runPage workbenchAutomationRunListProjection
	m401DecodeData(t, runs, &runPage)
	if got := m401RunIDs(runPage.Runs); !reflect.DeepEqual(
		got,
		[]string{"airun_00000001", "airun_00000003"},
	) {
		t.Fatalf("relational run filter = %#v", got)
	}

	actions := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationActionsList,
		WorkbenchAutomationListArguments{
			Status: string(AutomationActionSucceeded),
			Tool:   AutomationToolJobCreate,
			Limit:  100,
		},
	)
	var actionPage workbenchAutomationActionListProjection
	m401DecodeData(t, actions, &actionPage)
	if got := m401ActionIDs(actionPage.Actions); !reflect.DeepEqual(got, []string{"aiact_00000002"}) {
		t.Fatalf("direct action filter = %#v", got)
	}

	for _, fixture := range []struct {
		operation WorkbenchOperation
		status    string
	}{
		{WorkbenchOperationAutomationSessionsList, string(AutomationRunSucceeded)},
		{WorkbenchOperationAutomationRunsList, string(AutomationSessionStatusActive)},
		{WorkbenchOperationAutomationActionsList, string(AutomationRunRunning)},
	} {
		result := m401Dispatch(
			t,
			dispatcher,
			fixture.operation,
			WorkbenchAutomationListArguments{Status: fixture.status, Limit: 1},
		)
		m401AssertFailure(t, result, WorkbenchErrorInvalidArguments, false)
	}
}

func TestWorkbenchIntrospectionDetailsAndNotFound(t *testing.T) {
	state := m401BaseState()
	dispatcher := m401Dispatcher(m401Store(state), nil)

	sessionResult := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsGet,
		WorkbenchIDArguments{ID: "aisession_00000001"},
	)
	var session workbenchAutomationSessionDetailProjection
	m401DecodeData(t, sessionResult, &session)
	if session.Session.ID != "aisession_00000001" ||
		session.TurnsTotal != 1 ||
		session.RunsTotal != 1 ||
		session.ActionsTotal != 1 ||
		len(session.Turns) != 1 ||
		!session.Turns[0].UserTextPresent ||
		!session.Turns[0].AssistantTextPresent {
		t.Fatalf("session detail = %+v", session)
	}

	runResult := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationRunsGet,
		WorkbenchIDArguments{ID: "airun_00000001"},
	)
	var run workbenchAutomationRunDetailProjection
	m401DecodeData(t, runResult, &run)
	if run.Run.ID != "airun_00000001" ||
		run.ActionsTotal != 1 ||
		len(run.Actions) != 1 ||
		run.Actions[0].ID != "aiact_00000001" {
		t.Fatalf("run detail = %+v", run)
	}

	actionResult := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationActionsGet,
		WorkbenchIDArguments{ID: "aiact_00000001"},
	)
	var action workbenchAutomationActionDetailProjection
	m401DecodeData(t, actionResult, &action)
	if action.Action.ID != "aiact_00000001" ||
		action.Result == nil ||
		action.Result.Entity == nil ||
		action.Result.Entity.EntityID != "cand_00000001" ||
		action.ApprovalsTotal != 1 ||
		action.PermissionRequestsTotal != 1 ||
		action.Outbox == nil ||
		action.Outbox.ID != "aiout_00000001" {
		t.Fatalf("action detail = %+v", action)
	}

	for _, fixture := range []struct {
		operation WorkbenchOperation
		id        string
	}{
		{WorkbenchOperationAutomationSessionsGet, "aisession_99999999"},
		{WorkbenchOperationAutomationRunsGet, "airun_99999999"},
		{WorkbenchOperationAutomationActionsGet, "aiact_99999999"},
	} {
		result := m401Dispatch(
			t,
			dispatcher,
			fixture.operation,
			WorkbenchIDArguments{ID: fixture.id},
		)
		m401AssertFailure(t, result, WorkbenchErrorNotFound, false)
	}
}

func TestWorkbenchIntrospectionRecursiveSanitization(t *testing.T) {
	const (
		titleCanary      = "sk-title-canary"
		userCanary       = "user-cookie-canary"
		assistantCanary  = "assistant-jwt-canary"
		eventCanary      = "event-raw-canary"
		runCanary        = "run-failure-canary"
		messageKeyCanary = "message-key-canary"
		peerCanary       = "peer-canary"
		payloadCanary    = "payload-canary"
		resultCanary     = "result-canary"
		evidenceCanary   = "evidence-canary"
		reasonCanary     = "reason-canary"
		outboxCanary     = "outbox-text-canary"
		auditCanary      = "audit-data-canary"
		windowsCanary    = `C:\Users\ludwig\secrets`
		pemCanary        = "-----BEGIN PRIVATE KEY-----"
		fileCanary       = "file:///private/tmp/audit"
		clientSecret     = "clientSecretSensitiveValue"
		accessToken      = "accessTokenSensitiveValue"
		rawAttachment    = "rawAttachmentSensitiveValue"
		providerPayload  = "providerPayloadSensitiveValue"
		apiKey           = "api_key_sensitive_value"
		privateKey       = "privateKeySensitiveValue"
		cookie           = "cookieSensitiveValue"
	)
	state := m401BaseState()
	state.Sessions[0].Title = titleCanary + " /Users/ludwig/private " + clientSecret
	state.Turns[0].UserText = userCanary + " cookie=session"
	state.Turns[0].AssistantText = assistantCanary + " eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature12"
	state.Turns[0].ProviderName = accessToken
	state.TurnEvents[0].Type = rawAttachment
	state.TurnEvents[0].Data = json.RawMessage(
		`{"api_key":"` + eventCanary + `","raw_attachment":{"body":"secret"}}`,
	)
	state.Runs[0].ProviderName = "/private/tmp/provider.sock " + providerPayload
	state.Runs[0].EngineVersion = apiKey
	state.Runs[0].FailureStage = windowsCanary
	state.Runs[0].FailureMessage = runCanary
	state.Actions[0].ProviderCallID = "provider-call-canary"
	state.Actions[0].RiskReasons = []string{reasonCanary}
	state.Actions[0].Evidence = []AutomationEvidence{{Excerpt: evidenceCanary}}
	state.Actions[0].SourceMessage.MessageKey = messageKeyCanary
	state.Actions[0].SourceMessage.PeerID = peerCanary
	state.Actions[0].Recipient = &AutomationRecipient{
		AudienceID: privateKey,
		ChatType:   ChatTypePrivate,
		PeerID:     peerCanary,
	}
	state.Actions[0].Payload = json.RawMessage(`{"password":"` + payloadCanary + `"}`)
	state.Actions[0].Result = json.RawMessage(`{"provider_payload":"` + resultCanary + `"}`)
	state.Actions[0].ErrorCode = pemCanary
	state.Results[0].Data = json.RawMessage(
		`{"entity":{"entity_type":"candidate","entity_id":"cand_00000001","version":1},"secret":"` +
			resultCanary + `"}`,
	)
	state.Approvals[0].Reason = reasonCanary
	state.PermissionRequests[0].Reason = reasonCanary
	state.Outbox[0].IdempotencyKey = "outbox-idempotency-canary"
	state.Outbox[0].PeerID = peerCanary
	state.Outbox[0].Text = outboxCanary
	state.PendingAudit[0].Data = json.RawMessage(`{"authorization":"Bearer ` + auditCanary + `"}`)
	state.PendingAudit[0].ActorID = fileCanary + " " + cookie

	before, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	store := m401Store(state)
	dispatcher := m401Dispatcher(store, nil)
	sessionResult := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsGet,
		WorkbenchIDArguments{ID: "aisession_00000001"},
	)
	results := []WorkbenchToolResult{
		sessionResult,
		m401Dispatch(
			t,
			dispatcher,
			WorkbenchOperationAutomationActionsGet,
			WorkbenchIDArguments{ID: "aiact_00000001"},
		),
		m401Dispatch(
			t,
			dispatcher,
			WorkbenchOperationAutomationAuditList,
			WorkbenchAuditListArguments{Limit: 100},
		),
	}
	var sanitizedSession workbenchAutomationSessionDetailProjection
	m401DecodeData(t, sessionResult, &sanitizedSession)
	if sanitizedSession.Session.Title != workbenchIntrospectionRedactedValue ||
		len(sanitizedSession.Turns) != 1 ||
		sanitizedSession.Turns[0].ProviderName != workbenchIntrospectionRedactedValue ||
		len(sanitizedSession.TurnEvents) != 1 ||
		sanitizedSession.TurnEvents[0].Type != workbenchIntrospectionRedactedValue ||
		len(sanitizedSession.Runs) != 1 ||
		sanitizedSession.Runs[0].ProviderName != workbenchIntrospectionRedactedValue ||
		sanitizedSession.Runs[0].EngineVersion != workbenchIntrospectionRedactedValue ||
		len(sanitizedSession.Actions) != 1 ||
		sanitizedSession.Actions[0].Recipient == nil ||
		sanitizedSession.Actions[0].Recipient.AudienceID != workbenchIntrospectionRedactedValue ||
		len(sanitizedSession.AuditEvents) != 1 ||
		sanitizedSession.AuditEvents[0].ActorID != workbenchIntrospectionRedactedValue {
		t.Fatalf("concatenated sensitive metadata was not redacted: %+v", sanitizedSession)
	}
	canaries := []string{
		titleCanary,
		userCanary,
		assistantCanary,
		eventCanary,
		runCanary,
		messageKeyCanary,
		peerCanary,
		payloadCanary,
		resultCanary,
		evidenceCanary,
		reasonCanary,
		outboxCanary,
		auditCanary,
		"/Users/ludwig/private",
		"/private/tmp/provider.sock",
		windowsCanary,
		pemCanary,
		fileCanary,
		clientSecret,
		accessToken,
		rawAttachment,
		providerPayload,
		apiKey,
		privateKey,
		cookie,
	}
	for _, result := range results {
		if !result.OK {
			t.Fatalf("sanitized result = %+v", result)
		}
		encoded := result.Data
		for _, canary := range canaries {
			if bytes.Contains(encoded, []byte(canary)) {
				t.Fatalf("%s leaked %q: %s", result.Operation, canary, encoded)
			}
		}
		for _, field := range []string{
			`"provider_call_id"`,
			`"message_key"`,
			`"peer_id"`,
			`"payload"`,
			`"risk_reasons"`,
			`"evidence"`,
			`"reason"`,
			`"idempotency_key"`,
			`"text"`,
			`"data"`,
			`"error_message"`,
		} {
			if bytes.Contains(encoded, []byte(field)) {
				t.Fatalf("%s leaked field %s: %s", result.Operation, field, encoded)
			}
		}
		m401AssertNoForbiddenProjectionKeys(t, result.Data)
	}
	after, err := json.Marshal(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("sanitized introspection mutated the source automation state")
	}
}

func TestWorkbenchIntrospectionAuditMerge(t *testing.T) {
	base := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	pending := AutomationAuditRecord{
		ID: "aiaudit_00000001", EventType: "pending",
		RunID: "airun_00000001", ActionID: "aiact_00000001",
		ActorType: "system", Data: json.RawMessage(`{"source":"pending"}`), CreatedAt: base,
	}
	duplicate := AutomationAuditRecord{
		ID: "aiaudit_00000002", EventType: "duplicate",
		RunID: "airun_00000001", ActionID: "aiact_00000001",
		ActorType: "operator", ActorID: "operator-1",
		Data: json.RawMessage(`{"source":"same"}`), CreatedAt: base.Add(time.Minute),
	}
	committed := AutomationAuditRecord{
		ID: "aiaudit_00000003", EventType: "committed",
		RunID: "airun_00000002", ActionID: "aiact_00000002",
		ActorType: "system", Data: json.RawMessage(`{"source":"committed"}`),
		CreatedAt: base.Add(2 * time.Minute),
	}
	state := newAutomationStateAt(base)
	state.PendingAudit = []AutomationAuditRecord{pending, duplicate}
	store := m401Store(state)
	ledger := m401Ledger(t, m401Store(newAutomationStateAt(base)), duplicate, committed)
	dispatcher := m401Dispatcher(store, ledger)

	result := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{Limit: 100},
	)
	var page workbenchAutomationAuditListProjection
	m401DecodeData(t, result, &page)
	if result.Page == nil || result.Page.Total != 3 ||
		!reflect.DeepEqual(
			m401AuditIDs(page.AuditEvents),
			[]string{"aiaudit_00000003", "aiaudit_00000002", "aiaudit_00000001"},
		) {
		t.Fatalf("merged audit = %+v data=%#v", result, page.AuditEvents)
	}

	filtered := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{ActionID: "aiact_00000001", RunID: "airun_00000001", Limit: 100},
	)
	m401DecodeData(t, filtered, &page)
	if !reflect.DeepEqual(
		m401AuditIDs(page.AuditEvents),
		[]string{"aiaudit_00000002", "aiaudit_00000001"},
	) {
		t.Fatalf("filtered audit = %#v", page.AuditEvents)
	}

	pendingOnly := m401Dispatch(
		t,
		m401Dispatcher(store, nil),
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{Limit: 100},
	)
	m401DecodeData(t, pendingOnly, &page)
	if pendingOnly.Page == nil || pendingOnly.Page.Total != 2 {
		t.Fatalf("pending-only audit = %+v", pendingOnly)
	}

	conflict := duplicate
	conflict.EventType = "conflicting"
	conflictLedger := m401Ledger(t, m401Store(newAutomationStateAt(base)), conflict)
	conflicted := m401Dispatch(
		t,
		m401Dispatcher(store, conflictLedger),
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{Limit: 100},
	)
	m401AssertFailure(t, conflicted, WorkbenchErrorUnavailable, true)

	unreadable := m401Dispatch(
		t,
		m401Dispatcher(store, &AutomationAuditLedger{}),
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{Limit: 100},
	)
	m401AssertFailure(t, unreadable, WorkbenchErrorUnavailable, true)
	if unreadable.Error.Message != "automation audit is unavailable" {
		t.Fatalf("unreadable audit message = %q", unreadable.Error.Message)
	}

	for _, arguments := range []WorkbenchAuditListArguments{
		{Operation: WorkbenchOperationCandidatesList, Limit: 1},
		{EntityType: "candidate", Limit: 1},
		{EntityID: "cand_00000001", Limit: 1},
		{SourceEventID: "event_00000001", Limit: 1},
		{RunID: "airun_bad", Limit: 1},
		{ActionID: "aiaction_00000001", Limit: 1},
	} {
		invalid := m401Dispatch(
			t,
			dispatcher,
			WorkbenchOperationAutomationAuditList,
			arguments,
		)
		m401AssertFailure(t, invalid, WorkbenchErrorInvalidArguments, false)
	}
}

func TestWorkbenchIntrospectionEnvelopeBounds(t *testing.T) {
	t.Run("detail reduction order", func(t *testing.T) {
		initial := workbenchIntrospectionDetailBudget{
			TurnEvents:         1,
			AuditEvents:        1,
			Actions:            1,
			Turns:              1,
			Runs:               1,
			PermissionRequests: 1,
			Approvals:          1,
		}
		var observed []workbenchIntrospectionDetailBudget
		result := workbenchIntrospectionBoundedDetailSuccess(
			WorkbenchOperationAutomationSessionsGet,
			initial,
			func(budget workbenchIntrospectionDetailBudget) any {
				observed = append(observed, budget)
				count := budget.TurnEvents +
					budget.AuditEvents +
					budget.Actions +
					budget.Turns +
					budget.Runs +
					budget.PermissionRequests +
					budget.Approvals
				values := make([]string, 0, count*70)
				for i := 0; i < count*70; i++ {
					values = append(values, strings.Repeat("z ", 256))
				}
				return struct {
					Values []string `json:"values"`
				}{Values: values}
			},
		)
		if !result.OK {
			t.Fatalf("bounded detail = %+v", result)
		}
		want := []workbenchIntrospectionDetailBudget{
			initial,
			{AuditEvents: 1, Actions: 1, Turns: 1, Runs: 1, PermissionRequests: 1, Approvals: 1},
			{Actions: 1, Turns: 1, Runs: 1, PermissionRequests: 1, Approvals: 1},
			{Turns: 1, Runs: 1, PermissionRequests: 1, Approvals: 1},
			{Runs: 1, PermissionRequests: 1, Approvals: 1},
			{PermissionRequests: 1, Approvals: 1},
			{Approvals: 1},
			{},
		}
		if !reflect.DeepEqual(observed, want) {
			t.Fatalf("detail reduction order = %#v, want %#v", observed, want)
		}
	})

	t.Run("large session detail", func(t *testing.T) {
		state := m401LargeState()
		dispatcher := m401Dispatcher(m401Store(state), nil)
		first := m401Dispatch(
			t,
			dispatcher,
			WorkbenchOperationAutomationSessionsGet,
			WorkbenchIDArguments{ID: "aisession_00000001"},
		)
		second := m401Dispatch(
			t,
			dispatcher,
			WorkbenchOperationAutomationSessionsGet,
			WorkbenchIDArguments{ID: "aisession_00000001"},
		)
		if !first.OK || ValidateWorkbenchToolResult(first) != nil {
			t.Fatalf("large detail = %+v", first)
		}
		firstJSON, err := json.Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		secondJSON, err := json.Marshal(second)
		if err != nil {
			t.Fatal(err)
		}
		if len(firstJSON) > WorkbenchMaxResultJSONBytes || !bytes.Equal(firstJSON, secondJSON) {
			t.Fatalf("large detail bytes=%d deterministic=%t", len(firstJSON), bytes.Equal(firstJSON, secondJSON))
		}
		var detail workbenchAutomationSessionDetailProjection
		m401DecodeData(t, first, &detail)
		if detail.TurnsTotal != 200 ||
			detail.TurnEventsTotal != 1200 ||
			detail.RunsTotal != 200 ||
			detail.ActionsTotal != 200 ||
			detail.AuditEventsTotal != 100 ||
			!detail.Truncated {
			t.Fatalf("large detail metadata = %+v", detail)
		}
	})

	t.Run("large run detail", func(t *testing.T) {
		state := m401LargeState()
		for i := range state.Actions {
			state.Actions[i].RunID = "airun_00000001"
		}
		for i := range state.PendingAudit {
			state.PendingAudit[i].RunID = "airun_00000001"
		}
		result := m401Dispatch(
			t,
			m401Dispatcher(m401Store(state), nil),
			WorkbenchOperationAutomationRunsGet,
			WorkbenchIDArguments{ID: "airun_00000001"},
		)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if !result.OK || ValidateWorkbenchToolResult(result) != nil ||
			len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf("large run detail = %+v bytes=%d", result, len(encoded))
		}
		var detail workbenchAutomationRunDetailProjection
		m401DecodeData(t, result, &detail)
		if detail.ActionsTotal != 200 ||
			detail.AuditEventsTotal != 100 ||
			!detail.Truncated ||
			len(detail.Actions) >= detail.ActionsTotal {
			t.Fatalf("large run detail metadata = %+v", detail)
		}
	})

	t.Run("large action detail", func(t *testing.T) {
		state := m401LargeState()
		actionID := "aiact_00000001"
		longOperator := strings.Repeat("operator ", 50)
		longDecision := strings.Repeat("decision ", 50)
		for i := 1; i <= 100; i++ {
			createdAt := state.Actions[0].CreatedAt.Add(time.Duration(i) * time.Millisecond)
			state.Approvals = append(state.Approvals, AutomationApprovalRecord{
				ID: fmt.Sprintf("aiapproval_%08d", i), ActionID: actionID,
				ActionVersion: 1, Decision: longDecision, OperatorID: longOperator,
				CreatedAt: createdAt,
			})
			state.PermissionRequests = append(state.PermissionRequests, AutomationPermissionRequest{
				ID: fmt.Sprintf("aiperm_%08d", i), RunID: "airun_00000001",
				ActionID: actionID, Tool: AutomationToolCandidateCreate,
				Risk: AutomationRiskR1, Status: AutomationPermissionStatusAllowed,
				Version: 1, Decision: AutomationPermissionAllowOnce,
				OperatorID: longOperator, RuleID: longDecision,
				CreatedAt: createdAt, UpdatedAt: createdAt,
			})
		}
		for i := range state.PendingAudit {
			state.PendingAudit[i].ActionID = actionID
		}
		result := m401Dispatch(
			t,
			m401Dispatcher(m401Store(state), nil),
			WorkbenchOperationAutomationActionsGet,
			WorkbenchIDArguments{ID: actionID},
		)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if !result.OK || ValidateWorkbenchToolResult(result) != nil ||
			len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf("large action detail = %+v bytes=%d", result, len(encoded))
		}
		var detail workbenchAutomationActionDetailProjection
		m401DecodeData(t, result, &detail)
		if detail.ApprovalsTotal != 100 ||
			detail.PermissionRequestsTotal != 100 ||
			detail.AuditEventsTotal != 100 ||
			!detail.Truncated {
			t.Fatalf("large action detail metadata = %+v", detail)
		}
	})

	t.Run("list keeps an advancing cursor", func(t *testing.T) {
		base := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
		state := newAutomationStateAt(base)
		for i := 1; i <= 100; i++ {
			state.Sessions = append(state.Sessions, AutomationSession{
				ID:        fmt.Sprintf("aisession_%08d", i),
				Kind:      AutomationSessionKindChat,
				Title:     strings.Repeat("title ", 85),
				Status:    AutomationSessionStatusActive,
				Version:   1,
				CreatedAt: base,
				UpdatedAt: base,
			})
		}
		result := m401Dispatch(
			t,
			m401Dispatcher(m401Store(state), nil),
			WorkbenchOperationAutomationSessionsList,
			WorkbenchAutomationListArguments{Limit: 100},
		)
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if !result.OK || result.Page == nil ||
			result.Page.Returned <= 0 ||
			result.Page.Returned >= 100 ||
			!result.Page.HasMore ||
			result.Page.NextOffset <= result.Page.Offset ||
			len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf("bounded list = %+v bytes=%d", result, len(encoded))
		}
	})
}

func TestWorkbenchIntrospectionNilDependencies(t *testing.T) {
	dispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{})
	fixtures := []struct {
		operation WorkbenchOperation
		arguments any
	}{
		{WorkbenchOperationAutomationSessionsList, WorkbenchAutomationListArguments{Limit: 1}},
		{WorkbenchOperationAutomationSessionsGet, WorkbenchIDArguments{ID: "aisession_00000001"}},
		{WorkbenchOperationAutomationRunsList, WorkbenchAutomationListArguments{Limit: 1}},
		{WorkbenchOperationAutomationRunsGet, WorkbenchIDArguments{ID: "airun_00000001"}},
		{WorkbenchOperationAutomationActionsList, WorkbenchAutomationListArguments{Limit: 1}},
		{WorkbenchOperationAutomationActionsGet, WorkbenchIDArguments{ID: "aiact_00000001"}},
		{WorkbenchOperationAutomationAuditList, WorkbenchAuditListArguments{Limit: 1}},
	}
	for _, fixture := range fixtures {
		result := m401Dispatch(t, dispatcher, fixture.operation, fixture.arguments)
		m401AssertFailure(t, result, WorkbenchErrorUnavailable, true)
		if result.Error.Message != "automation store is unavailable" {
			t.Fatalf("%s unavailable message = %q", fixture.operation, result.Error.Message)
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	decoded := m401Decoded(
		t,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 1},
	)
	result := m401Dispatcher(m401Store(m401BaseState()), nil).Dispatch(
		cancelled,
		WorkbenchExecutionContext{},
		decoded,
	)
	m401AssertFailure(t, result, WorkbenchErrorCancelled, false)
}

func TestWorkbenchIntrospectionDeterminismAndNoMutation(t *testing.T) {
	state := m401BaseState()
	store := m401Store(state)
	dispatcher := m401Dispatcher(store, nil)
	before := store.Snapshot()

	first := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsGet,
		WorkbenchIDArguments{ID: "aisession_00000001"},
	)
	second := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsGet,
		WorkbenchIDArguments{ID: "aisession_00000001"},
	)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated session detail differs:\n%+v\n%+v", first, second)
	}
	after := store.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("introspection changed automation state")
	}

	snapshot := store.Snapshot()
	snapshot.Sessions[0].Title = "mutated caller snapshot"
	snapshot.Actions[0].Payload[0] = '['
	if reflect.DeepEqual(snapshot, store.Snapshot()) {
		t.Fatal("AutomationStore.Snapshot did not return a defensive copy")
	}

	shuffled := cloneAutomationState(state)
	m401Reverse(shuffled.Sessions)
	m401Reverse(shuffled.Turns)
	m401Reverse(shuffled.TurnEvents)
	m401Reverse(shuffled.Runs)
	m401Reverse(shuffled.Actions)
	m401Reverse(shuffled.Approvals)
	m401Reverse(shuffled.PermissionRequests)
	m401Reverse(shuffled.PendingAudit)
	shuffledResult := m401Dispatch(
		t,
		m401Dispatcher(m401Store(shuffled), nil),
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 100},
	)
	baselineResult := m401Dispatch(
		t,
		dispatcher,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 100},
	)
	if !reflect.DeepEqual(shuffledResult, baselineResult) {
		t.Fatalf("shuffled storage changed list order:\n%+v\n%+v", baselineResult, shuffledResult)
	}
}

func TestWorkbenchIntrospectionConcurrentReads(t *testing.T) {
	stateA := m401BaseState()
	stateB := m401BaseState()
	stateB.Sessions[0].Title = "coherent alternate state"
	stateB.Sessions[0].Version = 2
	stateB.Sessions[0].UpdatedAt = stateB.Sessions[0].UpdatedAt.Add(time.Hour)
	store := m401Store(stateA)
	dispatcher := m401Dispatcher(store, nil)
	decoded := m401Decoded(
		t,
		WorkbenchOperationAutomationSessionsList,
		WorkbenchAutomationListArguments{Limit: 100},
	)
	wantA := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
	store.mu.Lock()
	store.state = cloneAutomationState(stateB)
	store.mu.Unlock()
	wantB := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)

	stop := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			store.mu.Lock()
			store.state = cloneAutomationState(stateA)
			store.mu.Unlock()
			runtime.Gosched()
			store.mu.Lock()
			store.state = cloneAutomationState(stateB)
			store.mu.Unlock()
			runtime.Gosched()
		}
	}()
	defer func() {
		close(stop)
		wait.Wait()
	}()

	for i := 0; i < 200; i++ {
		result := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
		if !reflect.DeepEqual(result, wantA) && !reflect.DeepEqual(result, wantB) {
			t.Fatalf("session query observed a hybrid snapshot on attempt %d: %+v", i, result)
		}
	}
}

func TestWorkbenchIntrospectionAuditSnapshotCoherence(t *testing.T) {
	base := time.Date(2026, time.August, 3, 14, 0, 0, 0, time.UTC)
	stateA := newAutomationStateAt(base)
	stateB := newAutomationStateAt(base)
	stateA.PendingAudit = []AutomationAuditRecord{{
		ID: "aiaudit_00000001", EventType: "first", ActorType: "system",
		Data: json.RawMessage(`{"state":"a"}`), CreatedAt: base,
	}}
	stateB.PendingAudit = []AutomationAuditRecord{{
		ID: "aiaudit_00000002", EventType: "second", ActorType: "system",
		Data: json.RawMessage(`{"state":"b"}`), CreatedAt: base.Add(time.Second),
	}}
	store := m401Store(stateA)
	ledger := m401Ledger(t, store)
	dispatcher := m401Dispatcher(store, ledger)
	decoded := m401Decoded(
		t,
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{Limit: 100},
	)

	ledger.mu.Lock()
	ledgerLocked := true
	defer func() {
		if ledgerLocked {
			ledger.mu.Unlock()
		}
	}()

	resultCh := make(chan WorkbenchToolResult, 1)
	go func() {
		resultCh <- dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
	}()

	m401WaitForAuditDispatchLock(t)
	store.mu.Lock()
	store.state = cloneAutomationState(stateB)
	store.mu.Unlock()
	ledger.mu.Unlock()
	ledgerLocked = false

	var result WorkbenchToolResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("audit dispatch did not resume after releasing ledger lock")
	}
	var page workbenchAutomationAuditListProjection
	m401DecodeData(t, result, &page)
	ids := m401AuditIDs(page.AuditEvents)
	if reflect.DeepEqual(ids, []string{"aiaudit_00000002", "aiaudit_00000001"}) {
		t.Fatalf("audit query merged disjoint snapshots A+B: %#v", ids)
	}
	if !reflect.DeepEqual(ids, []string{"aiaudit_00000002"}) {
		t.Fatalf("audit query observed incoherent snapshot: %#v", ids)
	}
}

func TestWorkbenchIntrospectionAuditConcurrentRead(t *testing.T) {
	base := time.Date(2026, time.August, 3, 14, 0, 0, 0, time.UTC)
	stateA := newAutomationStateAt(base)
	stateB := newAutomationStateAt(base)
	stateA.PendingAudit = []AutomationAuditRecord{{
		ID: "aiaudit_00000001", EventType: "first", ActorType: "system",
		Data: json.RawMessage(`{"state":"a"}`), CreatedAt: base,
	}}
	stateB.PendingAudit = []AutomationAuditRecord{{
		ID: "aiaudit_00000002", EventType: "second", ActorType: "system",
		Data: json.RawMessage(`{"state":"b"}`), CreatedAt: base.Add(time.Second),
	}}
	store := m401Store(stateA)
	dispatcher := m401Dispatcher(store, nil)
	decoded := m401Decoded(
		t,
		WorkbenchOperationAutomationAuditList,
		WorkbenchAuditListArguments{Limit: 100},
	)
	wantA := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
	store.mu.Lock()
	store.state = cloneAutomationState(stateB)
	store.mu.Unlock()
	wantB := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)

	stop := make(chan struct{})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			store.mu.Lock()
			store.state = cloneAutomationState(stateA)
			store.mu.Unlock()
			runtime.Gosched()
			store.mu.Lock()
			store.state = cloneAutomationState(stateB)
			store.mu.Unlock()
			runtime.Gosched()
		}
	}()
	defer func() {
		close(stop)
		wait.Wait()
	}()

	for i := 0; i < 200; i++ {
		result := dispatcher.Dispatch(context.Background(), WorkbenchExecutionContext{}, decoded)
		if !reflect.DeepEqual(result, wantA) && !reflect.DeepEqual(result, wantB) {
			t.Fatalf("audit query observed a hybrid snapshot on attempt %d: %+v", i, result)
		}
	}
}

func m401WaitForAuditDispatchLock(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	buffer := make([]byte, 1<<20)
	for {
		size := runtime.Stack(buffer, true)
		for _, stack := range strings.Split(string(buffer[:size]), "\n\n") {
			blockedOnAuditLock := strings.Contains(stack, "sync.(*Mutex).Lock") &&
				(strings.Contains(stack, "workbenchIntrospectionAuditSnapshot") ||
					strings.Contains(stack, "(*AutomationAuditLedger).ReadAll"))
			if blockedOnAuditLock {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("audit dispatch did not block on ledger mutex:\n%s", buffer[:size])
		}
		runtime.Gosched()
	}
}

func m401BaseState() AutomationState {
	base := time.Date(2026, time.August, 3, 8, 0, 0, 0, time.UTC)
	state := newAutomationStateAt(base)
	state.Sessions = []AutomationSession{
		{
			ID: "aisession_00000001", Kind: AutomationSessionKindChat, Title: "First",
			Status: AutomationSessionStatusActive, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(3 * time.Minute),
		},
		{
			ID: "aisession_00000002", Kind: AutomationSessionKindAutomation, Title: "Second",
			Status: AutomationSessionStatusActive, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(3 * time.Minute),
		},
		{
			ID: "aisession_00000003", Kind: AutomationSessionKindLegacy, Title: "Third",
			Status: AutomationSessionStatusArchived, Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(time.Minute),
		},
	}
	state.Turns = []AutomationTurn{
		{
			ID: "aiturn_00000001", SessionID: "aisession_00000001",
			UserText: "hello", AssistantText: "world",
			Status: AutomationTurnCompleted, ProviderName: "provider-one",
			PromptRevisionID: "prompt_00000001", Version: 1,
			CreatedAt: base, UpdatedAt: base.Add(time.Minute),
		},
		{
			ID: "aiturn_00000002", SessionID: "aisession_00000002",
			Status: AutomationTurnCompleted, ProviderName: "provider-two",
			Version: 1, CreatedAt: base, UpdatedAt: base.Add(time.Minute),
		},
		{
			ID: "aiturn_00000003", SessionID: "aisession_00000003",
			Status: AutomationTurnFailed, ProviderName: "provider-three",
			Version: 1, CreatedAt: base, UpdatedAt: base.Add(time.Minute),
		},
	}
	state.TurnEvents = []AutomationTurnEvent{
		{
			Sequence: 1, SessionID: "aisession_00000001", TurnID: "aiturn_00000001",
			Type: "created", Data: json.RawMessage(`{"raw":"hidden"}`), CreatedAt: base,
		},
	}
	state.Runs = []AutomationRun{
		{
			ID: "airun_00000001", SessionID: "aisession_00000001", TurnID: "aiturn_00000001",
			PromptRevisionID: "prompt_00000001", IdempotencyKey: "run-one",
			EngineVersion: "engine-v1", ProviderName: "provider-one",
			SourceMessageEventID: "event_00000001", SourceMessageKey: "message-one",
			SourceMessageVersion: 1, Status: AutomationRunRunning, Version: 1,
			Attempt: 1, ActionIDs: []string{"aiact_00000001"},
			CreatedAt: base.Add(2 * time.Minute),
		},
		{
			ID: "airun_00000002", SessionID: "aisession_00000002", TurnID: "aiturn_00000002",
			IdempotencyKey: "run-two", EngineVersion: "engine-v1", ProviderName: "provider-two",
			SourceMessageEventID: "event_00000002", SourceMessageKey: "message-two",
			SourceMessageVersion: 1, Status: AutomationRunSucceeded, Version: 1,
			Attempt: 1, ActionIDs: []string{"aiact_00000002"},
			CreatedAt: base.Add(2 * time.Minute),
		},
		{
			ID: "airun_00000003", SessionID: "aisession_00000003", TurnID: "aiturn_00000003",
			IdempotencyKey: "run-three", EngineVersion: "engine-v1", ProviderName: "provider-three",
			SourceMessageEventID: "event_00000003", SourceMessageKey: "message-three",
			SourceMessageVersion: 1, Status: AutomationRunFailed, Version: 1,
			Attempt: 1, ActionIDs: []string{"aiact_00000003"},
			CreatedAt: base.Add(time.Minute),
		},
	}
	state.Actions = []AutomationAction{
		m401Action(
			"aiact_00000001",
			"airun_00000001",
			AutomationToolCandidateCreate,
			AutomationActionProposed,
			base.Add(4*time.Minute),
		),
		m401Action(
			"aiact_00000002",
			"airun_00000002",
			AutomationToolJobCreate,
			AutomationActionSucceeded,
			base.Add(5*time.Minute),
		),
		m401Action(
			"aiact_00000003",
			"airun_00000003",
			AutomationToolCandidateCreate,
			AutomationActionFailed,
			base.Add(3*time.Minute),
		),
	}
	resultData, _ := json.Marshal(AutomationActionResult{
		Entity: &AutomationEntityResult{
			EntityType: AutomationEntityCandidate,
			EntityID:   "cand_00000001",
			Version:    1,
		},
	})
	state.Actions[0].ResultID = "airesult_00000001"
	state.Actions[0].Result = append(json.RawMessage(nil), resultData...)
	state.Results = []AutomationResult{{
		ID: "airesult_00000001", ActionID: "aiact_00000001",
		PermissionRequestID: "aiperm_00000001",
		Status:              AutomationDeliverySent, Data: resultData, CreatedAt: base.Add(6 * time.Minute),
	}}
	state.Approvals = []AutomationApprovalRecord{{
		ID: "aiapproval_00000001", ActionID: "aiact_00000001", ActionVersion: 1,
		Decision: "approved", OperatorID: "operator-1", CreatedAt: base.Add(5 * time.Minute),
	}}
	state.PermissionRequests = []AutomationPermissionRequest{{
		ID: "aiperm_00000001", RunID: "airun_00000001", ActionID: "aiact_00000001",
		Tool: AutomationToolCandidateCreate, Risk: AutomationRiskR1,
		Status: AutomationPermissionStatusAllowed, Version: 1,
		Decision: AutomationPermissionAllowOnce, OperatorID: "operator-1",
		CreatedAt: base.Add(4 * time.Minute), UpdatedAt: base.Add(5 * time.Minute),
	}}
	state.Outbox = []AutomationOutboxEntry{{
		ID: "aiout_00000001", ActionID: "aiact_00000001",
		IdempotencyKey: "outbox-one", ChatType: ChatTypePrivate, PeerID: "wxid-hidden",
		Text: "hidden text", Status: AutomationOutboxPending,
		CreatedAt: base.Add(5 * time.Minute), UpdatedAt: base.Add(5 * time.Minute),
	}}
	state.PendingAudit = []AutomationAuditRecord{{
		ID: "aiaudit_00000001", EventType: "action.created",
		RunID: "airun_00000001", ActionID: "aiact_00000001",
		ActorType: "system", Data: json.RawMessage(`{"detail":"hidden"}`),
		CreatedAt: base.Add(4 * time.Minute),
	}}
	return state
}

func m401Action(
	id string,
	runID string,
	tool AutomationToolName,
	status AutomationActionStatus,
	createdAt time.Time,
) AutomationAction {
	return AutomationAction{
		ID: id, RunID: runID, ProviderCallID: "call-" + id,
		Tool: tool, Status: status, Risk: AutomationRiskR1,
		PolicyDecision: AutomationDecisionAutoApprove, PolicyVersion: 1,
		RiskReasons: []string{"ordinary"}, Confidence: 0.9,
		Evidence: []AutomationEvidence{{Source: "fixture", Excerpt: "hidden"}},
		SourceMessage: AutomationSourceMessageRef{
			EventID: "event-" + id, MessageKey: "message-" + id,
			ExpectedVersion: 1, ChatType: ChatTypePrivate, PeerID: "wxid-hidden",
		},
		Preconditions:      []AutomationEntityPrecondition{},
		DependsOnActionIDs: []string{},
		Payload:            json.RawMessage(`{"fixture":true}`),
		Retryable:          false,
		Attempt:            1,
		Version:            1,
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
	}
}

func m401LargeState() AutomationState {
	base := time.Date(2026, time.August, 3, 11, 0, 0, 0, time.UTC)
	state := newAutomationStateAt(base)
	state.Sessions = []AutomationSession{{
		ID: "aisession_00000001", Kind: AutomationSessionKindChat,
		Title: strings.Repeat("s", 512), Status: AutomationSessionStatusActive,
		Version: 1, CreatedAt: base, UpdatedAt: base,
	}}
	for i := 1; i <= 200; i++ {
		turnID := fmt.Sprintf("aiturn_%08d", i)
		runID := fmt.Sprintf("airun_%08d", i)
		actionID := fmt.Sprintf("aiact_%08d", i)
		createdAt := base.Add(time.Duration(i) * time.Second)
		state.Turns = append(state.Turns, AutomationTurn{
			ID: turnID, SessionID: "aisession_00000001",
			UserText: "hidden", AssistantText: "hidden",
			Status:           AutomationTurnCompleted,
			ProviderName:     strings.Repeat("p", 512),
			PromptRevisionID: strings.Repeat("r", 512),
			Version:          1, CreatedAt: createdAt, UpdatedAt: createdAt,
		})
		state.Runs = append(state.Runs, AutomationRun{
			ID: runID, SessionID: "aisession_00000001", TurnID: turnID,
			PromptRevisionID: strings.Repeat("r", 512),
			IdempotencyKey:   "hidden", EngineVersion: strings.Repeat("e", 512),
			ProviderName:         strings.Repeat("p", 512),
			SourceMessageEventID: fmt.Sprintf("event_%08d", i),
			SourceMessageKey:     "hidden", SourceMessageVersion: 1,
			Status: AutomationRunSucceeded, Version: 1, Attempt: 1,
			ActionIDs: []string{actionID}, CreatedAt: createdAt,
		})
		action := m401Action(
			actionID,
			runID,
			AutomationToolCandidateCreate,
			AutomationActionSucceeded,
			createdAt,
		)
		for dependency := 1; dependency <= AutomationMaxActionsPerRun; dependency++ {
			action.Preconditions = append(
				action.Preconditions,
				AutomationEntityPrecondition{
					EntityType:      AutomationEntityCandidate,
					EntityID:        fmt.Sprintf("cand_%074d", dependency),
					ExpectedVersion: 1,
				},
			)
			action.DependsOnActionIDs = append(
				action.DependsOnActionIDs,
				fmt.Sprintf("aiact_%08d", dependency),
			)
		}
		state.Actions = append(state.Actions, action)
	}
	for i := 1; i <= 1200; i++ {
		state.TurnEvents = append(state.TurnEvents, AutomationTurnEvent{
			Sequence: uint64(i), SessionID: "aisession_00000001",
			TurnID: fmt.Sprintf("aiturn_%08d", ((i-1)%200)+1),
			Type:   "provider.event", Data: json.RawMessage(`{"raw":"hidden body"}`),
			CreatedAt: base.Add(time.Duration(i) * time.Millisecond),
		})
	}
	for i := 1; i <= 100; i++ {
		state.PendingAudit = append(state.PendingAudit, AutomationAuditRecord{
			ID: fmt.Sprintf("aiaudit_%08d", i), EventType: "trace.event",
			RunID:     fmt.Sprintf("airun_%08d", ((i-1)%200)+1),
			ActionID:  fmt.Sprintf("aiact_%08d", ((i-1)%200)+1),
			ActorType: "system", Data: json.RawMessage(`{"raw":"hidden audit"}`),
			CreatedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	return state
}

func m401Store(state AutomationState) *AutomationStore {
	return &AutomationStore{state: cloneAutomationState(state)}
}

func m401Dispatcher(
	store *AutomationStore,
	audit *AutomationAuditLedger,
) WorkbenchToolDispatcher {
	return NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		Automation: store,
		Audit:      audit,
	})
}

func m401Ledger(
	t *testing.T,
	store *AutomationStore,
	records ...AutomationAuditRecord,
) *AutomationAuditLedger {
	t.Helper()
	committed := make(map[string]automationCommittedAudit, len(records))
	for _, record := range records {
		canonical, err := canonicalAutomationAudit(record)
		if err != nil {
			t.Fatalf("canonical audit fixture: %v", err)
		}
		committed[record.ID] = automationCommittedAudit{
			record:    cloneAutomationAuditRecord(record),
			canonical: canonical,
		}
	}
	return &AutomationAuditLedger{
		store:     store,
		committed: committed,
	}
}

func m401Decoded(
	t *testing.T,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchDecodedToolRequest {
	t.Helper()
	encoded, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(
		AutomationToolWorkbenchQuery,
		workbenchTestWrapper(t, operation, string(encoded)),
	)
	if err != nil {
		t.Fatalf("decode %s: %v", operation, err)
	}
	return decoded
}

func m401Dispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	return dispatcher.Dispatch(
		context.Background(),
		WorkbenchExecutionContext{},
		m401Decoded(t, operation, arguments),
	)
}

func m401DecodeData(t *testing.T, result WorkbenchToolResult, target any) {
	t.Helper()
	if !result.OK {
		t.Fatalf("%s failed: %+v", result.Operation, result)
	}
	if err := json.Unmarshal(result.Data, target); err != nil {
		t.Fatalf("decode %s data: %v; data=%s", result.Operation, err, result.Data)
	}
}

func m401AssertFailure(
	t *testing.T,
	result WorkbenchToolResult,
	code WorkbenchErrorCode,
	retryable bool,
) {
	t.Helper()
	if result.OK || result.Error == nil ||
		result.Error.Code != code ||
		result.Error.Retryable != retryable ||
		len(result.Data) != 0 ||
		result.Page != nil {
		t.Fatalf("failure = %+v, want %s retryable=%t", result, code, retryable)
	}
}

func m401AssertNoForbiddenProjectionKeys(t *testing.T, data json.RawMessage) {
	t.Helper()
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	var inspect func(any)
	inspect = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if workbenchIntrospectionForbiddenKey(key) {
					t.Fatalf("projection contains forbidden key %q", key)
				}
				inspect(child)
			}
		case []any:
			for _, child := range typed {
				inspect(child)
			}
		}
	}
	inspect(value)
}

func m401SessionIDs(values []workbenchAutomationSessionProjection) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}

func m401RunIDs(values []workbenchAutomationRunProjection) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}

func m401ActionIDs(values []workbenchAutomationActionProjection) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}

func m401AuditIDs(values []workbenchAutomationAuditEventProjection) []string {
	ids := make([]string, len(values))
	for i := range values {
		ids[i] = values[i].ID
	}
	return ids
}

func m401Reverse[T any](values []T) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}
