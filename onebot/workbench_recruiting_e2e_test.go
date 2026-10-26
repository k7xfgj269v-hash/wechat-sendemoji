package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type workbenchE2EProvider struct {
	fake *FakeAutomationProvider

	mu           sync.Mutex
	inputs       []AutomationProviderInput
	beforeStream func(int, AutomationProviderInput)
}

func (p *workbenchE2EProvider) Name() string {
	return p.fake.Name()
}

func (p *workbenchE2EProvider) Generate(
	ctx context.Context,
	input AutomationProviderInput,
) ([]byte, error) {
	return p.fake.Generate(ctx, input)
}

func (p *workbenchE2EProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderStream, error) {
	p.mu.Lock()
	index := len(p.inputs)
	p.inputs = append(p.inputs, input)
	beforeStream := p.beforeStream
	p.mu.Unlock()
	if beforeStream != nil {
		beforeStream(index, input)
	}
	return p.fake.Stream(ctx, input)
}

func (p *workbenchE2EProvider) input(index int) AutomationProviderInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inputs[index]
}

func TestWorkbenchRecruitingWorkflowOffline(t *testing.T) {
	crm := crmMessageOpenTestStore(t)
	message := syntheticPrivateCandidateEvent(
		t,
		crm,
		"wxid_workbench_candidate",
		"mid-workbench-recruiting",
		"Candidate inquiry for the warehouse role",
		0,
	)

	const (
		candidateID       = "cand_00000001"
		jobID             = "job_00000001"
		leadID            = "lead_00000001"
		prepareRecruiting = "prepare recruiting"
		confirmHire       = "confirm hire"
		verifyOnboarding  = "verify onboarding"
	)
	provider := workbenchE2ENewProvider(t,
		workbenchE2EStep(t, "Checking messages",
			workbenchE2EToolCall(t, prepareRecruiting, "messages_list",
				AutomationToolWorkbenchQuery,
				WorkbenchOperationMessagesList,
				WorkbenchMessageListArguments{
					Query: "Candidate inquiry", Limit: 1, Offset: 0,
				},
			),
		),
		workbenchE2EStep(t, "Recruiting records prepared",
			workbenchE2EToolCall(t, prepareRecruiting, "candidate_create",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationCandidatesCreate,
				WorkbenchCandidateCreateArguments{Candidate: CandidateDraft{
					FullName: "Offline Candidate", Skills: []string{"Go"},
				}},
			),
			workbenchE2EToolCall(t, prepareRecruiting, "candidate_link",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationCandidatesLinkMessage,
				WorkbenchCandidateLinkMessageArguments{Request: MessageCandidateLink{
					MessageEventID:       message.ID,
					ExpectedEventVersion: message.Version,
					CandidateID:          candidateID,
				}},
			),
			workbenchE2EToolCall(t, prepareRecruiting, "job_create",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationJobsCreate,
				WorkbenchJobCreateArguments{Job: JobDraft{
					Title: "Warehouse Engineer", Company: "Offline GmbH",
					Requirements: []string{}, RequiredDocuments: []string{},
				}},
			),
			workbenchE2EToolCall(t, prepareRecruiting, "lead_create",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationLeadsCreate,
				WorkbenchLeadCreateArguments{Lead: LeadCreate{
					CandidateID: candidateID, JobID: jobID, Owner: "operator-recruiting",
				}},
			),
			workbenchE2EToolCall(t, prepareRecruiting, "lead_screening",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationLeadsTransition,
				WorkbenchLeadTransitionArguments{
					LeadID: leadID, ExpectedVersion: 1,
					FromStage: LeadStageSourced, ToStage: LeadStageScreening,
				},
			),
			workbenchE2EToolCall(t, prepareRecruiting, "lead_interview",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationLeadsTransition,
				WorkbenchLeadTransitionArguments{
					LeadID: leadID, ExpectedVersion: 2,
					FromStage: LeadStageScreening, ToStage: LeadStageInterview,
				},
			),
			workbenchE2EToolCall(t, prepareRecruiting, "lead_offer",
				AutomationToolWorkbenchMutate,
				WorkbenchOperationLeadsTransition,
				WorkbenchLeadTransitionArguments{
					LeadID: leadID, ExpectedVersion: 3,
					FromStage: LeadStageInterview, ToStage: LeadStageOffer,
				},
			),
		),
		workbenchE2EStep(t, "Awaiting terminal approval",
			workbenchE2EToolCall(t, confirmHire, "lead_hired",
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationLeadsTerminal,
				WorkbenchLeadTerminalArguments{
					LeadID: leadID, ExpectedVersion: 4,
					FromStage: LeadStageOffer, ToStage: LeadStageHired,
					Reason: "operator-confirmed hire",
				},
			),
		),
		workbenchE2EStep(t, "Checking onboarding",
			workbenchE2EToolCall(t, verifyOnboarding, "onboardings_list",
				AutomationToolWorkbenchQuery,
				WorkbenchOperationOnboardingsList,
				WorkbenchOnboardingListArguments{
					LeadID: leadID, Limit: 10, Offset: 0,
				},
			),
		),
		workbenchE2EStep(t, "Onboarding verified"),
	)
	automation, engine, service := workbenchE2EOpen(t, crm, provider, nil)

	firstTurn := workbenchE2ERunChat(t, automation, crm, engine, prepareRecruiting)
	if firstTurn.Status != AutomationTurnCompleted {
		t.Fatalf("recruiting turn = %+v", firstTurn)
	}
	state := crm.Snapshot()
	if len(state.Candidates) != 1 || len(state.Jobs) != 1 || len(state.Leads) != 1 {
		t.Fatalf("recruiting state = candidates:%d jobs:%d leads:%d",
			len(state.Candidates), len(state.Jobs), len(state.Leads))
	}
	if state.MessageEvents[0].CandidateID != candidateID ||
		state.Leads[0].Stage != LeadStageOffer {
		t.Fatalf("linked message/lead = %+v / %+v", state.MessageEvents[0], state.Leads[0])
	}
	workbenchE2EAssertContinuation(t, provider.input(1), WorkbenchOperationMessagesList)

	terminalTurn := workbenchE2ERunChat(t, automation, crm, engine, confirmHire)
	request, action := workbenchE2EPendingPermission(t, automation, terminalTurn.ID)
	if action.Operation != WorkbenchOperationLeadsTerminal ||
		action.Status != AutomationActionAwaitingReview {
		t.Fatalf("terminal action = %+v", action)
	}
	decided, executed, err := service.DecidePermissionRequest(
		context.Background(),
		request.ID,
		automationPermissionDecisionRequest{
			ExpectedVersion:       request.Version,
			ExpectedActionVersion: action.Version,
			Decision:              AutomationPermissionAllowOnce,
			OperatorID:            "operator-recruiting",
			Reason:                "confirmed offline fixture",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if decided.OperatorID != "operator-recruiting" ||
		decided.Decision != AutomationPermissionAllowOnce ||
		executed.Status != AutomationActionSucceeded {
		t.Fatalf("terminal decision = %+v / %+v", decided, executed)
	}
	state = crm.Snapshot()
	if len(state.OnboardingCases) != 1 ||
		state.OnboardingCases[0].LeadID != leadID ||
		state.Leads[0].Stage != LeadStageHired {
		t.Fatalf("hired onboarding projection = %+v / %+v",
			state.Leads, state.OnboardingCases)
	}

	provider.beforeStream = func(index int, _ AutomationProviderInput) {
		if index != 4 {
			return
		}
		snapshot := automation.Snapshot()
		turn := snapshot.Turns[len(snapshot.Turns)-1]
		if turn.AssistantText != "" {
			t.Fatalf("assistant text persisted before onboarding continuation: %+v", turn)
		}
	}
	finalTurn := workbenchE2ERunChat(t, automation, crm, engine, verifyOnboarding)
	workbenchE2EAssertContinuation(t, provider.input(4), WorkbenchOperationOnboardingsList)
	if finalTurn.Status != AutomationTurnCompleted ||
		finalTurn.AssistantText != "Onboarding verified" {
		t.Fatalf("final turn = %+v", finalTurn)
	}
}

func workbenchE2ENewProvider(
	t *testing.T,
	steps ...FakeAutomationProviderStep,
) *workbenchE2EProvider {
	t.Helper()
	return &workbenchE2EProvider{fake: NewFakeAutomationProvider(steps)}
}

func workbenchE2EToolCall(
	t *testing.T,
	userText string,
	callID string,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) AutomationToolCall {
	t.Helper()
	call := automationChatTestCall(t, callID, tool, operation, arguments)
	call.Evidence = []AutomationEvidence{{
		Source:  "source_message",
		Field:   "text",
		Excerpt: userText,
	}}
	return call
}

func workbenchE2EStep(
	t *testing.T,
	assistant string,
	calls ...AutomationToolCall,
) FakeAutomationProviderStep {
	t.Helper()
	response, err := json.Marshal(AutomationToolEnvelope{
		SchemaVersion:    AutomationToolEnvelopeVersion,
		AssistantMessage: assistant,
		Actions:          append([]AutomationToolCall{}, calls...),
	})
	if err != nil {
		t.Fatal(err)
	}
	return FakeAutomationProviderStep{Response: response}
}

func workbenchE2EOpen(
	t *testing.T,
	crm *CRMStore,
	provider AutomationProvider,
	sender AutomationTextSender,
) (*AutomationStore, *defaultAutomationRunEngine, *AutomationService) {
	t.Helper()
	automation, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = automation.Close() })
	err = automation.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		state.Policy = DefaultAutomationPolicy(time.Now().UTC(), AutomationModeAutomatic)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	risk := NewServerAutomationRiskEvaluator()
	executor := NewDefaultAutomationCommandExecutor(automation, crm, sender)
	engine := NewDefaultAutomationRunEngine(
		automation, crm, provider, risk, executor,
	).(*defaultAutomationRunEngine)
	service := NewAutomationService(automation, crm, engine, executor, risk, nil)
	t.Cleanup(service.closeChatCoordinator)
	return automation, engine, service
}

func workbenchE2ERunChat(
	t *testing.T,
	automation *AutomationStore,
	_ *CRMStore,
	engine *defaultAutomationRunEngine,
	userText string,
) AutomationTurn {
	t.Helper()
	var session AutomationSession
	var turn AutomationTurn
	var run AutomationRun
	now := time.Now().UTC()
	err := automation.Update(func(
		state *AutomationState,
		allocator *AutomationIDAllocator,
	) error {
		sessionID, err := allocator.Next(AutomationIDKindSession)
		if err != nil {
			return err
		}
		turnID, err := allocator.Next(AutomationIDKindTurn)
		if err != nil {
			return err
		}
		runID, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		session = AutomationSession{
			ID: sessionID, Kind: AutomationSessionKindChat, Title: userText,
			Status: AutomationSessionStatusActive, Version: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		turn = AutomationTurn{
			ID: turnID, SessionID: sessionID, UserText: userText,
			Status: AutomationTurnQueued, ProviderName: engine.provider.Name(),
			Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		sourceKey := "chat:" + sessionID + ":" + turnID
		run = AutomationRun{
			ID: runID, SessionID: sessionID, TurnID: turnID,
			IdempotencyKey: sourceKey, EngineVersion: AutomationEngineVersion,
			ProviderName: turn.ProviderName, SourceMessageEventID: turnID,
			SourceMessageKey: sourceKey, SourceMessageVersion: 1,
			Status: AutomationRunQueued, Version: 1, Attempt: 1,
			ActionIDs: []string{}, CreatedAt: now,
		}
		state.Sessions = append(state.Sessions, session)
		state.Turns = append(state.Turns, turn)
		state.Runs = append(state.Runs, run)
		state.Idempotency[sourceKey] = AutomationIdempotencyRecord{
			Kind: "run", ResourceID: runID, CreatedAt: now,
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.runChatStream(context.Background(), run.ID, turn.ID); err != nil {
		t.Fatal(err)
	}
	state := automation.Snapshot()
	stored := automationTurnByID(&state, turn.ID)
	if stored == nil {
		t.Fatalf("turn %q was not persisted", turn.ID)
	}
	return *stored
}

func workbenchE2EPendingPermission(
	t *testing.T,
	store *AutomationStore,
	turnID string,
) (AutomationPermissionRequest, AutomationAction) {
	t.Helper()
	state := store.Snapshot()
	for _, run := range state.Runs {
		if run.TurnID != turnID {
			continue
		}
		for _, action := range state.Actions {
			if action.RunID != run.ID {
				continue
			}
			for _, request := range state.PermissionRequests {
				if request.ActionID == action.ID &&
					request.Status == AutomationPermissionStatusPending {
					return request, action
				}
			}
		}
	}
	t.Fatalf("pending permission for turn %q not found", turnID)
	return AutomationPermissionRequest{}, AutomationAction{}
}

func workbenchE2EAssertContinuation(
	t *testing.T,
	input AutomationProviderInput,
	operation WorkbenchOperation,
) {
	t.Helper()
	for _, message := range input.Continuation {
		if message.Result != nil && message.Result.Operation == operation &&
			message.Result.OK {
			return
		}
	}
	t.Fatalf("continuation does not contain successful %s: %+v",
		operation, input.Continuation)
}
