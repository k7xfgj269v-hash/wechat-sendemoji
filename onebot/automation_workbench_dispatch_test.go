package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDefaultWorkbenchDispatcherOfflineWorkflow(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	created := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationCandidatesCreate,
		WorkbenchCandidateCreateArguments{Candidate: CandidateDraft{
			FullName: "Ada", Skills: []string{"Go"},
		}})
	if !created.OK {
		t.Fatalf("candidates.create = %+v", created)
	}
	var candidate Candidate
	if err := json.Unmarshal(created.Data, &candidate); err != nil {
		t.Fatal(err)
	}

	queried := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationCandidatesGet,
		WorkbenchIDArguments{ID: candidate.ID})
	if !queried.OK {
		t.Fatalf("candidates.get = %+v", queried)
	}
	var detail CandidateDetail
	if err := json.Unmarshal(queried.Data, &detail); err != nil || detail.Candidate.ID != candidate.ID {
		t.Fatalf("candidate detail = %+v, err=%v", detail, err)
	}

	job, err := crmPipelineCreateJob(store, JobDraft{
		Title: "Engineer", Company: "Example",
		Requirements: []string{}, RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []LeadStage{LeadStageScreening, LeadStageInterview, LeadStageOffer} {
		lead, err = crmPipelineTransitionLead(store, lead.ID, LeadTransition{
			ExpectedVersion: lead.Version, FromStage: lead.Stage, ToStage: stage, Actor: "setup",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	terminalArgs := WorkbenchLeadTerminalArguments{
		LeadID: lead.ID, ExpectedVersion: lead.Version,
		FromStage: LeadStageOffer, ToStage: LeadStageHired,
	}
	required := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchAllowOnce, WorkbenchOperationLeadsTerminal, terminalArgs)
	task001AssertFailure(t, required, WorkbenchErrorAllowOnceRequired)

	allowed := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{
		PermissionDecision: AutomationPermissionAllowOnce,
		OperatorID:         "operator-1",
	}, AutomationToolWorkbenchAllowOnce, WorkbenchOperationLeadsTerminal, terminalArgs)
	if !allowed.OK {
		t.Fatalf("allowed terminal transition = %+v", allowed)
	}
	var hired Lead
	if err := json.Unmarshal(allowed.Data, &hired); err != nil || hired.Stage != LeadStageHired {
		t.Fatalf("hired lead = %+v, err=%v", hired, err)
	}
	if state := store.Snapshot(); len(state.OnboardingCases) != 1 ||
		state.OnboardingCases[0].LeadID != lead.ID {
		t.Fatalf("terminal transition onboarding = %#v", state.OnboardingCases)
	}
}

func TestDefaultWorkbenchDispatcherRejectsTerminalCandidateCreate(t *testing.T) {
	err := task001DecodeError(t,
		AutomationToolWorkbenchMutate, WorkbenchOperationCandidatesCreate,
		WorkbenchCandidateCreateArguments{Candidate: CandidateDraft{
			FullName: "Ada", Status: CandidateStatusPlaced, Skills: []string{},
		}})
	if !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("terminal candidates.create error = %v, want validation", err)
	}
}

func TestDefaultWorkbenchDispatcherRejectsJobCreateCommitments(t *testing.T) {
	err := task001DecodeError(t,
		AutomationToolWorkbenchMutate, WorkbenchOperationJobsCreate,
		WorkbenchJobCreateArguments{Job: JobDraft{
			Title: "Engineer", Company: "Example",
			Requirements: []string{}, RequiredDocuments: []string{},
			CompensationSummary:   "Guaranteed rate",
			Accommodation:         "Shared housing",
			TransportRequirements: "Shuttle required",
		}})
	if !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("commitment-bearing jobs.create error = %v, want validation", err)
	}
}

func TestDefaultWorkbenchDispatcherMapsStaleAndNotFound(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, _ := crmPipelineCreateJob(store, JobDraft{
		Title: "Engineer", Company: "Example",
		Requirements: []string{}, RequiredDocuments: []string{},
	})
	lead, _ := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	stale := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchMutate, WorkbenchOperationLeadsTransition,
		WorkbenchLeadTransitionArguments{
			LeadID: lead.ID, ExpectedVersion: lead.Version + 1,
			FromStage: LeadStageSourced, ToStage: LeadStageScreening,
		})
	task001AssertFailure(t, stale, WorkbenchErrorConflict)

	missing := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery, WorkbenchOperationJobsGet,
		WorkbenchIDArguments{ID: "job_99999999"})
	task001AssertFailure(t, missing, WorkbenchErrorNotFound)
}

func TestDefaultWorkbenchDispatcherKeepsFridaOffline(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	for _, fixture := range []struct {
		tool      AutomationToolName
		operation WorkbenchOperation
	}{
		{AutomationToolWorkbenchQuery, WorkbenchOperationFridaStatus},
		{AutomationToolWorkbenchAllowOnce, WorkbenchOperationFridaInitialize},
		{AutomationToolWorkbenchAllowOnce, WorkbenchOperationFridaRescan},
		{AutomationToolWorkbenchAllowOnce, WorkbenchOperationFridaDetach},
	} {
		result := task001Dispatch(t, dispatcher, WorkbenchExecutionContext{},
			fixture.tool, fixture.operation, WorkbenchEmptyArguments{})
		task001AssertFailure(t, result, WorkbenchErrorUnavailable)
	}
	state := store.Snapshot()
	if len(state.Candidates)+len(state.Jobs)+len(state.Leads)+len(state.MessageEvents) != 0 {
		t.Fatalf("unavailable Frida dispatch mutated CRM: %+v", state)
	}
}

func TestApprovedWorkbenchControlledSendHonorsLateOperationDeny(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	defer applyWorkbenchOperationDenySet(map[string]struct{}{})

	client, automation, _, service, _, turn := workbenchE2EOutboundFixture(
		t,
		ChatTypePrivate,
		"wxid_late_denied",
		WorkbenchOperationMessagesSendPrivate,
	)
	request, action := workbenchE2EPendingPermission(t, automation, turn.ID)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	decided, approved, err := service.DecidePermissionRequest(
		canceled,
		request.ID,
		automationPermissionDecisionRequest{
			ExpectedVersion:       request.Version,
			ExpectedActionVersion: action.Version,
			Decision:              AutomationPermissionAllowOnce,
			OperatorID:            "operator-late-deny",
			Reason:                "persist approval without executing",
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("approval interruption error = %v, want context canceled", err)
	}
	if decided.Status != AutomationPermissionStatusAllowed ||
		approved.Status != AutomationActionApproved {
		t.Fatalf("persisted approval = %+v / %+v", decided, approved)
	}
	if snapshot := automation.Snapshot(); len(snapshot.Outbox) != 0 {
		t.Fatalf("outbox before denied execution = %+v", snapshot.Outbox)
	}

	applyWorkbenchOperationDenySet(map[string]struct{}{
		workbenchOperationDenyKey(
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationMessagesSendPrivate,
		): {},
	})
	if err := service.ExecuteReadyActions(context.Background(), action.RunID); err != nil {
		t.Fatal(err)
	}

	state := automation.Snapshot()
	index := automationActionIndex(&state, action.ID)
	if index < 0 {
		t.Fatal("persisted controlled-send action is missing")
	}
	executed := state.Actions[index]
	if executed.Status != AutomationActionFailed ||
		executed.ErrorCode != string(WorkbenchErrorUnavailable) {
		t.Fatalf("late-denied action = %+v", executed)
	}
	if len(state.Outbox) != 0 {
		t.Fatalf("late-denied outbox = %+v, want none", state.Outbox)
	}
	if calls := client.callCount("message.send_controlled"); calls != 0 {
		t.Fatalf("late-denied SendControlled calls = %d, want 0", calls)
	}
}

func TestDefaultWorkbenchDispatcherSearchesContactsThroughAgent(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	localType := 3
	agent := &task003AgentClient{
		searchResult: AgentContactSearchResult{
			Total: 2,
			Contacts: []Contact{
				{Username: "wxid_ali", NickName: "阿狸"},
			},
			Limit:  1,
			Offset: 0,
		},
	}
	raw := json.RawMessage(`{
		"operation":"contacts.search",
		"arguments_json":"{\"query\":\"阿狸\",\"channel\":\"wechat\",\"local_type\":3,\"limit\":1,\"offset\":0}"
	}`)
	decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw)
	if err != nil {
		t.Fatalf("DecodeWorkbenchToolRequest() error = %v", err)
	}
	arguments := decoded.Arguments.(*WorkbenchContactSearchArguments)
	if arguments.Keyword != "阿狸" || arguments.LocalType == nil || *arguments.LocalType != localType {
		t.Fatalf("normalized contact search arguments = %+v", arguments)
	}
	if decoded.Request.ArgumentsJSON != `{"keyword":"阿狸","local_type":3,"limit":1,"offset":0}` {
		t.Fatalf("canonical contact search request = %s", decoded.Request.ArgumentsJSON)
	}

	result := NewDefaultWorkbenchToolDispatcher(store, agent).Dispatch(
		context.Background(),
		WorkbenchExecutionContext{},
		decoded,
	)
	if !result.OK {
		t.Fatalf("contacts.search = %+v", result)
	}
	if agent.searchRequest.Keyword != "阿狸" ||
		agent.searchRequest.LocalType == nil ||
		*agent.searchRequest.LocalType != localType ||
		agent.searchRequest.Limit != 1 ||
		agent.searchRequest.Offset != 0 {
		t.Fatalf("agent search request = %+v", agent.searchRequest)
	}
	var payload struct {
		Contacts []Contact `json:"contacts"`
	}
	if err := json.Unmarshal(result.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Contacts) != 1 || payload.Contacts[0].Username != "wxid_ali" {
		t.Fatalf("contacts.search payload = %+v", payload)
	}
	if result.Page == nil || result.Page.Total != 2 || !result.Page.HasMore || result.Page.NextOffset != 1 {
		t.Fatalf("contacts.search page = %+v", result.Page)
	}
}

func TestWorkbenchContactSearchRejectsForeignChannel(t *testing.T) {
	raw := json.RawMessage(`{
		"operation":"contacts.search",
		"arguments_json":"{\"query\":\"阿狸\",\"channel\":\"telegram\",\"limit\":1,\"offset\":0}"
	}`)
	if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw); err == nil {
		t.Fatal("contacts.search accepted a non-WeChat channel")
	}
}

func TestDefaultWorkbenchDispatcherMapsContactAgentErrors(t *testing.T) {
	store := crmMessageOpenTestStore(t)
	agent := &task003AgentClient{
		searchErr: &AgentError{
			Code:      AgentErrorCodeUnavailable,
			Message:   "contact cache is unavailable",
			Retryable: true,
		},
	}
	result := task001Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(store, agent),
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationContactsSearch,
		WorkbenchContactSearchArguments{Limit: 1, Offset: 0},
	)
	task001AssertFailure(t, result, WorkbenchErrorUnavailable)
	if result.Error.Message != "contact cache is unavailable" || !result.Error.Retryable {
		t.Fatalf("mapped agent error = %+v", result.Error)
	}

	agent.searchErr = errors.New("/private/wechat-agent.sock failed")
	result = task001Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(store, agent),
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationContactsSearch,
		WorkbenchContactSearchArguments{Limit: 1, Offset: 0},
	)
	task001AssertFailure(t, result, WorkbenchErrorUnavailable)
	if result.Error.Message != "WeChat agent operation failed" {
		t.Fatalf("transport error leaked details: %+v", result.Error)
	}
}

func task001Dispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation: operation, ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(tool, raw)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", tool, operation, err)
	}
	return dispatcher.Dispatch(context.Background(), execution, decoded)
}

func task001DecodeError(
	t *testing.T,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) error {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation: operation, ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = DecodeWorkbenchToolRequest(tool, raw)
	return err
}

func task001AssertFailure(t *testing.T, result WorkbenchToolResult, code WorkbenchErrorCode) {
	t.Helper()
	if result.OK || result.Error == nil || result.Error.Code != code {
		t.Fatalf("result = %+v, want failure %s", result, code)
	}
}
