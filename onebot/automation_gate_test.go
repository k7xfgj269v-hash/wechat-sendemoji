package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOpenAIAutomationGateUsesDedicatedModelPromptAndStrictDecision(t *testing.T) {
	client := &http.Client{Transport: openAIRoundTripper(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer gate-key" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		var body openAIChatCompletionRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "gate-model" ||
			len(body.Messages) != 2 ||
			body.Messages[0].Content != "gate prompt" ||
			!strings.Contains(body.Messages[1].Content, AutomationGateInputVersion) ||
			len(body.Tools) != 0 {
			t.Fatalf("gate request = %#v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"message":{"content":"{\"schema_version\":\"automation.gate.v1\",\"allow\":true,\"reason\":\"contains a concrete staffing order\",\"confidence\":0.96}"}}]}`,
			)),
		}, nil
	})}
	provider, err := newOpenAICompatibleAutomationProviderWithProtocol(
		AutomationProviderChatCompletions,
		"https://api.example.test/v1/chat/completions",
		"gate-model",
		"gate-key",
		"gate prompt",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := provider.GenerateGateDecision(context.Background(), AutomationGateInput{
		SchemaVersion: AutomationGateInputVersion,
		RunID:         "run_00000001",
		GateMode:      RecruitingSourceGateModeJobRelevance,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allow ||
		decision.Model != "gate-model" ||
		decision.Reason != "contains a concrete staffing order" ||
		decision.RawSHA256 == "" {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestOpenAIAutomationGateRejectsUnknownDecisionFields(t *testing.T) {
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"message":{"content":"{\"schema_version\":\"automation.gate.v1\",\"allow\":false,\"reason\":\"noise\",\"confidence\":1,\"extra\":true}"}}]}`,
			)),
		}, nil
	})}
	provider, err := newOpenAICompatibleAutomationProviderWithProtocol(
		AutomationProviderChatCompletions,
		"https://api.example.test/v1/chat/completions",
		"gate-model",
		"gate-key",
		"gate prompt",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.GenerateGateDecision(
		context.Background(),
		AutomationGateInput{SchemaVersion: AutomationGateInputVersion},
	); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("GenerateGateDecision() error = %v, want strict validation", err)
	}
}

func TestAutomationRunEngineSourceGroupGateAllowBlockAndFailClosed(t *testing.T) {
	tests := []struct {
		name             string
		decision         AutomationGateDecision
		gateErr          error
		wantRunStatus    AutomationRunStatus
		wantFailureStage string
		wantMainCalls    int
		wantAudit        []string
	}{
		{
			name: "allow",
			decision: AutomationGateDecision{
				SchemaVersion: AutomationGateDecisionVersion,
				Allow:         true,
				Reason:        "concrete job order",
				Confidence:    0.98,
				Model:         "gate-model",
				RawSHA256:     strings.Repeat("a", 64),
			},
			wantRunStatus: AutomationRunSucceeded,
			wantMainCalls: 1,
			wantAudit: []string{
				AutomationAuditEventGateStarted,
				AutomationAuditEventGateCompleted,
				AutomationAuditEventProviderStarted,
				AutomationAuditEventProviderCompleted,
			},
		},
		{
			name: "block",
			decision: AutomationGateDecision{
				SchemaVersion: AutomationGateDecisionVersion,
				Allow:         false,
				Reason:        "group chatter",
				Confidence:    0.94,
				Model:         "gate-model",
				RawSHA256:     strings.Repeat("b", 64),
			},
			wantRunStatus: AutomationRunSucceeded,
			wantMainCalls: 0,
			wantAudit: []string{
				AutomationAuditEventGateStarted,
				AutomationAuditEventGateCompleted,
			},
		},
		{
			name:             "failure",
			gateErr:          errors.New("synthetic gate failure"),
			wantRunStatus:    AutomationRunFailed,
			wantFailureStage: "gate",
			wantMainCalls:    0,
			wantAudit: []string{
				AutomationAuditEventGateStarted,
				AutomationAuditEventGateCompleted,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, crm, engine, mainProvider, gate := automationGateTestEngine(t)
			gate.decision = test.decision
			gate.err = test.gateErr
			event := automationRecruitingTestEvent(
				ChatTypeGroup,
				"synthetic_jobs@chatroom",
				false,
			)
			event.Text = "synthetic source group payload"
			event.MessageKey = MessageKeyForEvent(event)
			persisted, created, err := crmMessagePersistEventToStore(crm, event)
			if err != nil || !created {
				t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
			}
			run, created, err := engine.EnsureRun(context.Background(), persisted, "")
			if err != nil || !created {
				t.Fatalf("EnsureRun() = %#v, created=%t, err=%v", run, created, err)
			}
			err = engine.ProcessRun(context.Background(), run.ID)
			if test.gateErr == nil && err != nil {
				t.Fatal(err)
			}
			if test.gateErr != nil && err == nil {
				t.Fatal("ProcessRun() succeeded after gate failure")
			}
			state := store.Snapshot()
			completed := automationRunEngineTestRun(t, state, run.ID)
			if completed.Status != test.wantRunStatus ||
				completed.FailureStage != test.wantFailureStage ||
				len(completed.ActionIDs) != 0 {
				t.Fatalf("completed run = %#v", completed)
			}
			turn := automationTurnByID(&state, completed.TurnID)
			session := automationSessionByID(&state, completed.SessionID)
			if turn == nil || session == nil {
				t.Fatalf("run trace is missing: turn=%#v session=%#v", turn, session)
			}
			wantTurnStatus := AutomationTurnCompleted
			if test.wantRunStatus == AutomationRunFailed {
				wantTurnStatus = AutomationTurnFailed
			}
			if turn.Status != wantTurnStatus ||
				session.Status != AutomationSessionStatusCompleted ||
				turn.CompletedAt == nil {
				t.Fatalf("run trace status = turn:%#v session:%#v", turn, session)
			}
			mainProvider.mu.Lock()
			mainCalls := len(mainProvider.calls)
			mainProvider.mu.Unlock()
			if mainCalls != test.wantMainCalls {
				t.Fatalf("main provider calls = %d, want %d", mainCalls, test.wantMainCalls)
			}
			if gate.callCount() != 1 {
				t.Fatalf("gate calls = %d, want 1", gate.callCount())
			}
			gotAudit := make([]string, 0, len(state.PendingAudit))
			for _, record := range state.PendingAudit {
				if record.RunID == run.ID {
					gotAudit = append(gotAudit, record.EventType)
				}
			}
			if !automationSchedulerTestStringsEqual(gotAudit, test.wantAudit) {
				t.Fatalf("audit order = %v, want %v", gotAudit, test.wantAudit)
			}
		})
	}
}

func TestAutomationRunEngineAppliesGateEnabledAfterRouteFreeze(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	crm := automationRecruitingOpenTestStore(t)
	project := automationRecruitingConfigureProject(t, crm)
	mainProvider := &automationRunEngineRecordingProvider{}
	base := NewDefaultAutomationRunEngine(
		store,
		crm,
		mainProvider,
		NewServerAutomationRiskEvaluator(),
		nil,
	).(*defaultAutomationRunEngine)
	gate := &automationGateTestGate{decision: AutomationGateDecision{
		SchemaVersion: AutomationGateDecisionVersion,
		Allow:         false,
		Reason:        "old queued message is not a staffing order",
		Confidence:    0.99,
		Model:         "gate-model",
		RawSHA256:     strings.Repeat("c", 64),
	}}
	base.gate = gate
	engine := newRecruitingAutomationRunEngine(NewRecruitingAutomationRouter(crm), base)

	event := automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false)
	event.Mid = "frozen-before-gate"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}
	run, created, err := engine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created {
		t.Fatalf("EnsureRun() = %#v, created=%t, err=%v", run, created, err)
	}
	frozen, ok := automationFindMessageEvent(crm.Snapshot(), persisted.ID)
	if !ok || frozen.RecruitingSourceGateEnabled {
		t.Fatalf("event was not frozen with gate disabled: %#v", frozen)
	}

	source := project.SourceGroups[0]
	if _, err := crm.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		ID:          source.ID,
		GroupID:     source.GroupID,
		DisplayName: source.DisplayName,
		Enabled:     true,
		GateEnabled: true,
		GateMode:    RecruitingSourceGateModeJobRelevance,
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.ProcessRun(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	completed := automationRunEngineTestRun(t, store.Snapshot(), run.ID)
	if completed.Status != AutomationRunSucceeded || gate.callCount() != 1 ||
		len(mainProvider.Calls()) != 0 {
		t.Fatalf(
			"retroactive gate result = run:%#v gate_calls:%d provider_calls:%v",
			completed,
			gate.callCount(),
			mainProvider.Calls(),
		)
	}
}

func TestAutomationRunEngineGateAndProviderUseIndependentTimeouts(t *testing.T) {
	store, crm, engine, mainProvider, gate := automationGateTestEngine(t)
	base := engine.(*defaultAutomationRunEngine)
	base.providerTimeout = func() time.Duration { return 500 * time.Millisecond }
	gate.delay = 150 * time.Millisecond
	gate.decision = AutomationGateDecision{
		SchemaVersion: AutomationGateDecisionVersion,
		Allow:         true,
		Reason:        "concrete order",
		Confidence:    0.99,
		Model:         "gate-model",
		RawSHA256:     strings.Repeat("d", 64),
	}
	event := automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false)
	event.Mid = "independent-timeouts"
	event.MessageKey = MessageKeyForEvent(event)
	persisted, created, err := crmMessagePersistEventToStore(crm, event)
	if err != nil || !created {
		t.Fatalf("persist event = %#v, created=%t, err=%v", persisted, created, err)
	}
	run, created, err := engine.EnsureRun(context.Background(), persisted, "")
	if err != nil || !created {
		t.Fatalf("EnsureRun() = %#v, created=%t, err=%v", run, created, err)
	}
	if err := engine.ProcessRun(context.Background(), run.ID); err != nil {
		t.Fatal(err)
	}
	if completed := automationRunEngineTestRun(t, store.Snapshot(), run.ID); completed.Status != AutomationRunSucceeded {
		t.Fatalf("completed run = %#v", completed)
	}
	gate.mu.Lock()
	gateRemaining := gate.deadlineRemaining
	gate.mu.Unlock()
	mainProvider.mu.Lock()
	providerRemaining := mainProvider.deadlineRemaining
	mainProvider.mu.Unlock()
	if gateRemaining < 400*time.Millisecond || providerRemaining < 400*time.Millisecond {
		t.Fatalf(
			"independent deadlines = gate:%v provider:%v, want both near 500ms",
			gateRemaining,
			providerRemaining,
		)
	}
}

func TestAutomationRunEngineProcessQueuedContinuesAfterInvalidGateResponse(t *testing.T) {
	store, crm, engine, mainProvider, gate := automationGateTestEngine(t)
	gate.sequence = []automationGateTestResult{
		{err: NewAutomationValidationError("gate_response", "is malformed")},
		{decision: AutomationGateDecision{
			SchemaVersion: AutomationGateDecisionVersion,
			Allow:         true,
			Reason:        "concrete order",
			Confidence:    0.98,
			Model:         "gate-model",
			RawSHA256:     strings.Repeat("e", 64),
		}},
	}
	var runs []AutomationRun
	for i, mid := range []string{"invalid-gate", "valid-gate"} {
		event := automationRecruitingTestEvent(ChatTypeGroup, "synthetic_jobs@chatroom", false)
		event.Mid = mid
		event.Timestamp += int64(i)
		event.MessageKey = MessageKeyForEvent(event)
		persisted, created, err := crmMessagePersistEventToStore(crm, event)
		if err != nil || !created {
			t.Fatalf("persist event %d = %#v, created=%t, err=%v", i, persisted, created, err)
		}
		run, created, err := engine.EnsureRun(context.Background(), persisted, "")
		if err != nil || !created {
			t.Fatalf("EnsureRun(%d) = %#v, created=%t, err=%v", i, run, created, err)
		}
		runs = append(runs, run)
	}
	if err := engine.ProcessQueued(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := store.Snapshot()
	first := automationRunEngineTestRun(t, state, runs[0].ID)
	second := automationRunEngineTestRun(t, state, runs[1].ID)
	if first.Status != AutomationRunFailed ||
		first.FailureCode != "gate_response_invalid" ||
		second.Status != AutomationRunSucceeded ||
		!automationSchedulerTestStringsEqual(mainProvider.Calls(), []string{runs[1].ID}) {
		t.Fatalf("queued gate results = first:%#v second:%#v calls:%v", first, second, mainProvider.Calls())
	}
}

type automationGateTestResult struct {
	decision AutomationGateDecision
	err      error
}

type automationGateTestGate struct {
	mu       sync.Mutex
	calls    []AutomationGateInput
	decision AutomationGateDecision
	err      error
	delay    time.Duration
	sequence []automationGateTestResult

	deadlineRemaining time.Duration
}

func (g *automationGateTestGate) Evaluate(
	ctx context.Context,
	input AutomationGateInput,
) (AutomationGateDecision, error) {
	g.mu.Lock()
	g.calls = append(g.calls, input)
	delay := g.delay
	if deadline, ok := ctx.Deadline(); ok {
		g.deadlineRemaining = time.Until(deadline)
	}
	result := automationGateTestResult{decision: g.decision, err: g.err}
	if len(g.sequence) != 0 {
		result = g.sequence[0]
		g.sequence = g.sequence[1:]
	}
	g.mu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return AutomationGateDecision{}, ctx.Err()
		case <-timer.C:
		}
	}
	return result.decision, result.err
}

func (g *automationGateTestGate) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.calls)
}

func automationGateTestEngine(
	t *testing.T,
) (*AutomationStore, *CRMStore, AutomationRunEngine, *automationRunEngineRecordingProvider, *automationGateTestGate) {
	t.Helper()
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	crm := automationRecruitingOpenTestStore(t)
	project := automationRecruitingConfigureProject(t, crm)
	source := project.SourceGroups[0]
	project, err = crm.UpsertRecruitingSourceGroup(project.Version, RecruitingSourceGroupUpsert{
		ID:          source.ID,
		GroupID:     source.GroupID,
		DisplayName: source.DisplayName,
		Enabled:     source.Enabled,
		GateEnabled: true,
		GateMode:    RecruitingSourceGateModeJobRelevance,
	})
	if err != nil {
		t.Fatal(err)
	}
	mainProvider := &automationRunEngineRecordingProvider{}
	base := NewDefaultAutomationRunEngine(
		store,
		crm,
		mainProvider,
		NewServerAutomationRiskEvaluator(),
		nil,
	).(*defaultAutomationRunEngine)
	gate := &automationGateTestGate{}
	base.gate = gate
	engine := newRecruitingAutomationRunEngine(NewRecruitingAutomationRouter(crm), base)
	return store, crm, engine, mainProvider, gate
}
