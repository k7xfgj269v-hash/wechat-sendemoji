package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type automationChatCompactingProvider struct {
	request AutomationContextCompactionRequest
	result  AutomationContextCompactionResult
}

func (p *automationChatCompactingProvider) Name() string {
	return "chat-compacting"
}

func (p *automationChatCompactingProvider) Generate(
	context.Context,
	AutomationProviderInput,
) ([]byte, error) {
	return nil, errors.New("Generate should not be called while preparing chat context")
}

func (p *automationChatCompactingProvider) Stream(
	context.Context,
	AutomationProviderInput,
) (AutomationProviderStream, error) {
	return nil, errors.New("Stream should not be called while preparing chat context")
}

func (p *automationChatCompactingProvider) CompactContext(
	_ context.Context,
	request AutomationContextCompactionRequest,
) (AutomationContextCompactionResult, error) {
	p.request = request
	return p.result, nil
}

func TestPersistAutomationChatCheckpointKeepsPublicSessionRevision(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crm.Close() })
	provider := &automationChatInputCaptureProvider{
		inputs: make(chan AutomationProviderInput, 2),
	}
	risk := NewServerAutomationRiskEvaluator()
	engine := NewDefaultAutomationRunEngine(store, crm, provider, risk, nil)
	defaultEngine := engine.(*defaultAutomationRunEngine)
	service := NewAutomationService(store, crm, engine, nil, risk, nil)
	t.Cleanup(service.closeChatCoordinator)

	session, err := service.CreateChatSession("checkpoint revision")
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.SubmitChatTurn(
		context.Background(),
		session.ID,
		session.Version,
		"first turn",
	)
	if err != nil {
		t.Fatal(err)
	}
	waitAutomationChatTurnTerminal(t, service, first.ID)

	before, turns, err := service.ChatSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 1 || turns[0].Status != AutomationTurnCompleted {
		t.Fatalf("completed turns = %+v", turns)
	}
	checkpoint := newAutomationChatCheckpoint(
		before,
		automationChatContextPlan{
			CompactTurns: []AutomationConversationTurn{{
				TurnID:        turns[0].ID,
				UserText:      turns[0].UserText,
				AssistantText: turns[0].AssistantText,
			}},
			ThroughTurnID: turns[0].ID,
		},
		turns[0].PromptRevisionID,
		automationChatTestContextSummary(),
		time.Now().UTC(),
	)
	if err := defaultEngine.persistAutomationChatCheckpoint(before, checkpoint); err != nil {
		t.Fatal(err)
	}

	after, _, err := service.ChatSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version {
		t.Fatalf("session version = %d, want unchanged %d", after.Version, before.Version)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("session updated_at = %s, want unchanged %s", after.UpdatedAt, before.UpdatedAt)
	}
	if after.ContextCheckpoint == nil ||
		after.ContextCheckpoint.ThroughTurnID != turns[0].ID {
		t.Fatalf("checkpoint = %+v", after.ContextCheckpoint)
	}

	next, err := service.SubmitChatTurn(
		context.Background(),
		session.ID,
		before.Version,
		"second turn",
	)
	if err != nil {
		t.Fatalf("submit with pre-checkpoint public version: %v", err)
	}
	waitAutomationChatTurnTerminal(t, service, next.ID)
}

func TestPrepareAutomationChatContextFallbackPreservesAllTurns(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crm.Close() })
	provider := &automationChatInputCaptureProvider{
		inputs: make(chan AutomationProviderInput, 1),
	}
	risk := NewServerAutomationRiskEvaluator()
	engine := NewDefaultAutomationRunEngine(store, crm, provider, risk, nil).(*defaultAutomationRunEngine)
	engine.chatContextPolicy = func() AutomationChatContextPolicy {
		return AutomationChatContextPolicy{
			ContextWindowTokens: 8192, AutoCompactPercent: 50,
			ContextReserveTokens: 1024, RecentTurnTokens: 1024,
		}
	}
	service := NewAutomationService(store, crm, engine, nil, risk, nil)
	t.Cleanup(service.closeChatCoordinator)
	session, err := service.CreateChatSession("fallback context")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	turns := make([]AutomationTurn, 0, 5)
	if err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for index := 0; index < 5; index++ {
			turnID, err := allocator.Next(AutomationIDKindTurn)
			if err != nil {
				return err
			}
			timestamp := now.Add(time.Duration(index) * time.Second)
			turn := AutomationTurn{
				ID: turnID, SessionID: session.ID,
				UserText:     strings.Repeat("问", 1800),
				Status:       AutomationTurnCompleted,
				ProviderName: "test", Version: 1,
				CreatedAt: timestamp, UpdatedAt: timestamp,
			}
			if index == 4 {
				turn.Status = AutomationTurnQueued
			} else {
				started := timestamp
				completed := timestamp
				turn.AssistantText = strings.Repeat("答", 1800)
				turn.StartedAt = &started
				turn.CompletedAt = &completed
			}
			turns = append(turns, turn)
			state.Turns = append(state.Turns, turn)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	input, stats, err := engine.prepareAutomationChatContext(
		context.Background(),
		provider,
		AutomationRun{},
		turns[4],
		AutomationProviderInput{
			SchemaVersion: AutomationProviderInputVersion,
			RunID:         "airun_context_fallback",
			EngineVersion: AutomationEngineVersion,
			Message: AutomationMessageContext{
				Source: "automation_chat", Text: turns[4].UserText, Kinds: []string{"text"},
			},
			AllowedTools: []AutomationToolName{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Triggered || !stats.Fallback ||
		stats.CompactedTurns == 0 ||
		input.Conversation == nil ||
		len(input.Conversation.RecentTurns) != 4 {
		recent := 0
		if input.Conversation != nil {
			recent = len(input.Conversation.RecentTurns)
		}
		t.Fatalf("fallback recent turns = %d, stats = %+v", recent, stats)
	}
	for index := range input.Conversation.RecentTurns {
		if input.Conversation.RecentTurns[index].TurnID != turns[index].ID {
			t.Fatalf("fallback turn[%d] = %+v", index, input.Conversation.RecentTurns[index])
		}
	}
}

func TestPrepareAutomationChatContextPersistsCheckpointAndKeepsRecentTurns(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	crm, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = crm.Close() })
	previousSummary := automationChatTestContextSummary()
	previousSummary.Objective = "continue the existing hiring workflow"
	compactedSummary := automationChatTestContextSummary()
	compactedSummary.Objective = "complete the active candidate workflow"
	compactedSummary.Facts = append(compactedSummary.Facts, "four more turns were reviewed")
	provider := &automationChatCompactingProvider{
		result: AutomationContextCompactionResult{
			Summary: compactedSummary,
			Usage: &AutomationProviderUsage{
				InputTokens: 2400, CachedInputTokens: 1600,
				OutputTokens: 180, TotalTokens: 2580,
			},
		},
	}
	risk := NewServerAutomationRiskEvaluator()
	engine := NewDefaultAutomationRunEngine(store, crm, provider, risk, nil).(*defaultAutomationRunEngine)
	engine.chatContextPolicy = func() AutomationChatContextPolicy {
		return AutomationChatContextPolicy{
			ContextWindowTokens: 8192, AutoCompactPercent: 50,
			ContextReserveTokens: 1024, RecentTurnTokens: 1024,
		}
	}
	service := NewAutomationService(store, crm, engine, nil, risk, nil)
	t.Cleanup(service.closeChatCoordinator)
	session, err := service.CreateChatSession("successful context compaction")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	var anchor AutomationTurn
	completedTurns := make([]AutomationTurn, 0, 4)
	var current AutomationTurn
	if err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		anchorID, err := allocator.Next(AutomationIDKindTurn)
		if err != nil {
			return err
		}
		anchorTime := now.Add(-time.Second)
		anchor = AutomationTurn{
			ID: anchorID, SessionID: session.ID,
			UserText: "existing checkpoint anchor", AssistantText: "checkpointed",
			Status: AutomationTurnCompleted, ProviderName: "test", Version: 1,
			CreatedAt: anchorTime, UpdatedAt: anchorTime,
			StartedAt: &anchorTime, CompletedAt: &anchorTime,
		}
		state.Turns = append(state.Turns, anchor)
		storedSession := automationSessionByID(state, session.ID)
		storedSession.ContextCheckpoint = &AutomationChatCheckpoint{
			SchemaVersion:    AutomationChatCheckpointVersion,
			ThroughTurnID:    anchor.ID,
			PromptRevisionID: "prompt_00000001",
			SourceTurnCount:  1,
			Summary:          previousSummary,
			CreatedAt:        anchorTime,
			UpdatedAt:        anchorTime,
		}
		for index := 0; index < 5; index++ {
			turnID, err := allocator.Next(AutomationIDKindTurn)
			if err != nil {
				return err
			}
			timestamp := now.Add(time.Duration(index) * time.Second)
			turn := AutomationTurn{
				ID: turnID, SessionID: session.ID,
				UserText:     strings.Repeat("问", 1800),
				Status:       AutomationTurnCompleted,
				ProviderName: "test", PromptRevisionID: "prompt_00000009",
				Version: 1, CreatedAt: timestamp, UpdatedAt: timestamp,
			}
			if index == 4 {
				turn.Status = AutomationTurnQueued
				current = turn
			} else {
				turn.AssistantText = strings.Repeat("答", 1800)
				turn.StartedAt = &timestamp
				turn.CompletedAt = &timestamp
				completedTurns = append(completedTurns, turn)
			}
			state.Turns = append(state.Turns, turn)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, _, err := service.ChatSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	input, stats, err := engine.prepareAutomationChatContext(
		context.Background(),
		provider,
		AutomationRun{PromptRevisionID: "prompt_00000009"},
		current,
		AutomationProviderInput{
			SchemaVersion: AutomationProviderInputVersion,
			RunID:         "airun_context_success",
			EngineVersion: AutomationEngineVersion,
			Message: AutomationMessageContext{
				Source: "automation_chat", Text: current.UserText, Kinds: []string{"text"},
			},
			AllowedTools: []AutomationToolName{},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.Triggered || !stats.Attempted || !stats.Succeeded || stats.Fallback ||
		stats.CompactedTurns != 3 || stats.RecentTurns != 1 ||
		stats.CompactionUsage == nil || stats.CompactionUsage.TotalTokens != 2580 {
		t.Fatalf("successful compaction stats = %+v", stats)
	}
	if provider.request.SchemaVersion != automationChatCompactionInputVersion ||
		provider.request.SessionID != session.ID ||
		provider.request.PromptRevisionID != "prompt_00000009" ||
		provider.request.PreviousSummary == nil ||
		!automationChatContextSummaryEqual(*provider.request.PreviousSummary, previousSummary) ||
		len(provider.request.Turns) != 3 {
		t.Fatalf("compaction request = %+v", provider.request)
	}
	for index := range provider.request.Turns {
		if provider.request.Turns[index].TurnID != completedTurns[index].ID {
			t.Fatalf("compaction request turn[%d] = %+v", index, provider.request.Turns[index])
		}
	}
	if input.Conversation == nil ||
		input.Conversation.Checkpoint == nil ||
		input.Conversation.Checkpoint.ThroughTurnID != completedTurns[2].ID ||
		!automationChatContextSummaryEqual(input.Conversation.Checkpoint.Summary, compactedSummary) ||
		len(input.Conversation.RecentTurns) != 1 ||
		input.Conversation.RecentTurns[0].TurnID != completedTurns[3].ID {
		t.Fatalf("provider conversation = %+v", input.Conversation)
	}

	after, persistedTurns, err := service.ChatSession(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Version != before.Version || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf(
			"session revision changed from version=%d updated_at=%s to version=%d updated_at=%s",
			before.Version,
			before.UpdatedAt,
			after.Version,
			after.UpdatedAt,
		)
	}
	if after.ContextCheckpoint == nil ||
		after.ContextCheckpoint.ThroughTurnID != completedTurns[2].ID ||
		after.ContextCheckpoint.SourceTurnCount != 4 ||
		after.ContextCheckpoint.PromptRevisionID != "prompt_00000009" ||
		!after.ContextCheckpoint.CreatedAt.Equal(anchor.CreatedAt) ||
		!automationChatContextSummaryEqual(after.ContextCheckpoint.Summary, compactedSummary) {
		t.Fatalf("persisted checkpoint = %+v", after.ContextCheckpoint)
	}
	if len(persistedTurns) != 6 {
		t.Fatalf("persisted turns = %d, want 6", len(persistedTurns))
	}
}

func TestAutomationChatLastInputTokensUsesLatestCompletedTurn(t *testing.T) {
	now := time.Now().UTC()
	current := AutomationTurn{ID: "aiturn_00000003"}
	got := automationChatLastInputTokens(current, []AutomationTurn{
		{
			ID: "aiturn_00000001", Status: AutomationTurnCompleted,
			CreatedAt: now, ProviderUsage: &AutomationProviderUsage{InputTokens: 50000},
		},
		{
			ID: "aiturn_00000002", Status: AutomationTurnCompleted,
			CreatedAt: now.Add(time.Second), ProviderUsage: &AutomationProviderUsage{InputTokens: 1200},
		},
		current,
	})
	if got != 1200 {
		t.Fatalf("last input tokens = %d, want 1200", got)
	}
}

func TestPersistAutomationChatCheckpointRejectsStaleCheckpointCAS(t *testing.T) {
	store, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine := &defaultAutomationRunEngine{store: store}
	now := time.Now().UTC()
	var session AutomationSession
	var turn AutomationTurn
	if err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		sessionID, err := allocator.Next(AutomationIDKindSession)
		if err != nil {
			return err
		}
		turnID, err := allocator.Next(AutomationIDKindTurn)
		if err != nil {
			return err
		}
		session = AutomationSession{
			ID: sessionID, Kind: AutomationSessionKindChat, Title: "checkpoint CAS",
			Status: AutomationSessionStatusActive, Version: 7,
			CreatedAt: now, UpdatedAt: now,
		}
		completed := now
		turn = AutomationTurn{
			ID: turnID, SessionID: sessionID, UserText: "completed",
			AssistantText: "done", Status: AutomationTurnCompleted,
			ProviderName: "test", Version: 2,
			CreatedAt: now, UpdatedAt: now, CompletedAt: &completed,
		}
		state.Sessions = append(state.Sessions, session)
		state.Turns = append(state.Turns, turn)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	plan := automationChatContextPlan{
		CompactTurns: []AutomationConversationTurn{{
			TurnID: turn.ID, UserText: turn.UserText, AssistantText: turn.AssistantText,
		}},
		ThroughTurnID: turn.ID,
	}
	first := newAutomationChatCheckpoint(
		session,
		plan,
		"",
		automationChatTestContextSummary(),
		now.Add(time.Second),
	)
	if err := engine.persistAutomationChatCheckpoint(session, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.Summary.Objective = "stale overwrite"
	second.UpdatedAt = now.Add(2 * time.Second)
	err = engine.persistAutomationChatCheckpoint(session, second)
	if err == nil || !strings.Contains(err.Error(), "context_checkpoint") {
		t.Fatalf("stale checkpoint CAS error = %v", err)
	}
	stored := store.Snapshot().Sessions[0]
	if stored.Version != session.Version ||
		stored.ContextCheckpoint == nil ||
		stored.ContextCheckpoint.Summary.Objective != first.Summary.Objective {
		t.Fatalf("stored session after stale CAS = %+v", stored)
	}
}

func TestAutomationStoreReopensCheckpointAndProviderUsage(t *testing.T) {
	dataDir := t.TempDir()
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		sessionID, err := allocator.Next(AutomationIDKindSession)
		if err != nil {
			return err
		}
		turnID, err := allocator.Next(AutomationIDKindTurn)
		if err != nil {
			return err
		}
		completed := now
		state.Sessions = append(state.Sessions, AutomationSession{
			ID: sessionID, Kind: AutomationSessionKindChat, Title: "reopen",
			Status: AutomationSessionStatusActive,
			ContextCheckpoint: &AutomationChatCheckpoint{
				SchemaVersion: AutomationChatCheckpointVersion,
				ThroughTurnID: turnID, SourceTurnCount: 1,
				Summary:   automationChatTestContextSummary(),
				CreatedAt: now, UpdatedAt: now,
			},
			Version: 1, CreatedAt: now, UpdatedAt: now,
		})
		state.Turns = append(state.Turns, AutomationTurn{
			ID: turnID, SessionID: sessionID, UserText: "reopen",
			AssistantText: "ok", Status: AutomationTurnCompleted,
			ProviderName: "test",
			ProviderUsage: &AutomationProviderUsage{
				InputTokens: 100, CachedInputTokens: 60,
				OutputTokens: 20, TotalTokens: 120,
			},
			Version: 1, CreatedAt: now, UpdatedAt: now,
			StartedAt: &completed, CompletedAt: &completed,
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAutomationStore(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	state := reopened.Snapshot()
	if len(state.Sessions) != 1 ||
		len(state.Turns) != 1 ||
		state.Sessions[0].ContextCheckpoint == nil ||
		state.Sessions[0].ContextCheckpoint.ThroughTurnID != state.Turns[0].ID ||
		state.Turns[0].ProviderUsage == nil ||
		state.Turns[0].ProviderUsage.CachedInputTokens != 60 {
		t.Fatalf("reopened state = %+v", state)
	}
}

func automationChatTestContextSummary() AutomationChatContextSummary {
	return AutomationChatContextSummary{
		Objective:       "continue the workflow",
		Facts:           []string{"first turn completed"},
		EntityRefs:      []AutomationChatContextEntityRef{},
		Decisions:       []string{},
		PendingItems:    []string{"handle the next message"},
		ToolResultRefs:  []AutomationChatContextToolResultRef{},
		UserConstraints: []string{},
	}
}

func waitAutomationChatTurnTerminal(
	t *testing.T,
	service *AutomationService,
	turnID string,
) AutomationTurn {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		state := service.store.Snapshot()
		turn := automationTurnByID(&state, turnID)
		if turn != nil &&
			(turn.Status == AutomationTurnCompleted ||
				turn.Status == AutomationTurnFailed ||
				turn.Status == AutomationTurnCancelled) {
			return *turn
		}
		if time.Now().After(deadline) {
			t.Fatalf("turn %s did not become terminal", turnID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
