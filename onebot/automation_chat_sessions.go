package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	automationChatMaxTextRunes        = 4000
	automationChatContextTargetRunes  = 3200
	automationChatAutoRetryLimit      = 3
	automationChatAutoRetryDelay      = 2 * time.Second
	automationChatRetryMessageRunes   = 800
	automationChatRetryTaskRunes      = 700
	automationChatRetryOperationLimit = 8
	automationChatRetrySnapshotRunes  = 1600
	automationChatRetryResultRunes    = 420
	automationChatRetryTotalRunes     = 3200

	AutomationTurnEventQueued              = "turn.queued"
	AutomationTurnEventStarted             = "turn.started"
	AutomationTurnEventAssistantTextDelta  = "assistant.text.delta"
	AutomationTurnEventToolCallDelta       = "tool.call.delta"
	AutomationTurnEventToolCallProposed    = "tool.call.proposed"
	AutomationTurnEventPermissionRequested = "permission.requested"
	AutomationTurnEventPermissionAllowed   = "permission.allowed"
	AutomationTurnEventPermissionDenied    = "permission.denied"
	AutomationTurnEventToolExecuting       = "tool.executing"
	AutomationTurnEventToolSucceeded       = "tool.succeeded"
	AutomationTurnEventToolFailed          = "tool.failed"
	AutomationTurnEventCompleted           = "turn.completed"
	AutomationTurnEventFailed              = "turn.failed"
	AutomationTurnEventCancelled           = "turn.cancelled"
)

type automationChatCoordinator struct {
	service     *AutomationService
	mu          sync.Mutex
	active      map[string]context.CancelFunc
	subscribers map[string]map[chan AutomationTurnEvent]struct{}
	closed      bool
}

type AutomationChatTrace struct {
	Session            AutomationSession             `json:"session"`
	Turns              []AutomationTurn              `json:"turns"`
	TurnEvents         []AutomationTurnEvent         `json:"turn_events"`
	Runs               []AutomationRun               `json:"runs"`
	Actions            []AutomationAction            `json:"actions"`
	PermissionRequests []AutomationPermissionRequest `json:"permission_requests"`
	Results            []AutomationResult            `json:"results"`
	AuditEvents        []AutomationAuditRecord       `json:"audit_events"`
}

type automationChatHistoryCompactionStats struct {
	HistoricalTurns int  `json:"historical_turns"`
	IncludedTurns   int  `json:"included_turns"`
	OmittedTurns    int  `json:"omitted_turns"`
	FinalRunes      int  `json:"final_runes"`
	TargetRunes     int  `json:"target_runes"`
	Compacted       bool `json:"compacted"`
}

type automationChatTurnSubmission struct {
	ActorType        string
	Attempt          uint32
	RetryOfRunID     string
	ProviderName     string
	PromptRevisionID string
}

func newAutomationChatCoordinator(service *AutomationService) *automationChatCoordinator {
	return &automationChatCoordinator{
		service:     service,
		active:      make(map[string]context.CancelFunc),
		subscribers: make(map[string]map[chan AutomationTurnEvent]struct{}),
	}
}

func (s *AutomationService) closeChatCoordinator() {
	if s == nil || s.chat == nil {
		return
	}
	activeTurnIDs := make(map[string]struct{})
	s.chat.mu.Lock()
	if !s.chat.closed {
		s.chat.closed = true
		for turnID, cancel := range s.chat.active {
			activeTurnIDs[turnID] = struct{}{}
			cancel()
		}
		for turnID, subscribers := range s.chat.subscribers {
			for subscriber := range subscribers {
				close(subscriber)
			}
			delete(s.chat.subscribers, turnID)
		}
	}
	s.chat.mu.Unlock()
	_ = s.failInterruptedChatTurns(
		activeTurnIDs,
		"shutdown_interrupted",
		"chat execution was interrupted by workbench shutdown",
	)
}

func (s *AutomationService) CreateChatSession(title string) (AutomationSession, error) {
	if s == nil || s.store == nil {
		return AutomationSession{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	var err error
	title, err = normalizeAutomationString("title", title, 256, false, true)
	if err != nil {
		return AutomationSession{}, err
	}
	if title == "" {
		title = "New conversation"
	}
	var session AutomationSession
	err = s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		id, err := allocator.Next(AutomationIDKindSession)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		session = AutomationSession{
			ID: id, Kind: AutomationSessionKindChat, Title: title,
			Status: AutomationSessionStatusActive, Version: 1,
			CreatedAt: now, UpdatedAt: now,
		}
		state.Sessions = append(state.Sessions, session)
		return nil
	})
	return session, err
}

func (s *AutomationService) ListChatSessions(limit, offset int) (int, []AutomationSession, error) {
	if s == nil || s.store == nil {
		return 0, nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	items := make([]AutomationSession, 0)
	if err := s.store.Read(func(state *AutomationState) error {
		for _, session := range state.Sessions {
			if session.Kind == AutomationSessionKindChat {
				items = append(items, session)
			}
		}
		return nil
	}); err != nil {
		return 0, nil, err
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	total := len(items)
	start, end := automationPage(total, limit, offset)
	return total, append([]AutomationSession(nil), items[start:end]...), nil
}

func (s *AutomationService) ChatSession(id string) (AutomationSession, []AutomationTurn, error) {
	if s == nil || s.store == nil {
		return AutomationSession{}, nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	var session AutomationSession
	var turns []AutomationTurn
	if err := s.store.Read(func(state *AutomationState) error {
		found := automationSessionByID(state, strings.TrimSpace(id))
		if found == nil || found.Kind != AutomationSessionKindChat {
			return errors.New("automation chat session not found")
		}
		session = *found
		turns = make([]AutomationTurn, 0)
		for _, turn := range state.Turns {
			if turn.SessionID == session.ID {
				turns = append(turns, turn)
			}
		}
		return nil
	}); err != nil {
		return AutomationSession{}, nil, err
	}
	sort.Slice(turns, func(i, j int) bool {
		if turns[i].CreatedAt.Equal(turns[j].CreatedAt) {
			return turns[i].ID < turns[j].ID
		}
		return turns[i].CreatedAt.Before(turns[j].CreatedAt)
	})
	return session, turns, nil
}

func (s *AutomationService) ChatSessionTrace(id string) (AutomationChatTrace, error) {
	session, turns, err := s.ChatSession(id)
	if err != nil {
		return AutomationChatTrace{}, err
	}
	trace := AutomationChatTrace{Session: session, Turns: turns}
	turnIDs := make(map[string]struct{}, len(turns))
	runIDs := make(map[string]struct{})
	actionIDs := make(map[string]struct{})
	for _, turn := range turns {
		turnIDs[turn.ID] = struct{}{}
	}
	if err := s.store.Read(func(state *AutomationState) error {
		for _, event := range state.TurnEvents {
			if _, ok := turnIDs[event.TurnID]; ok {
				trace.TurnEvents = append(trace.TurnEvents, event)
			}
		}
		for _, run := range state.Runs {
			if _, ok := turnIDs[run.TurnID]; ok {
				trace.Runs = append(trace.Runs, run)
				runIDs[run.ID] = struct{}{}
			}
		}
		for _, action := range state.Actions {
			if _, ok := runIDs[action.RunID]; ok {
				trace.Actions = append(trace.Actions, action)
				actionIDs[action.ID] = struct{}{}
			}
		}
		for _, request := range state.PermissionRequests {
			if _, ok := actionIDs[request.ActionID]; ok {
				trace.PermissionRequests = append(trace.PermissionRequests, request)
			}
		}
		for _, result := range state.Results {
			if _, ok := actionIDs[result.ActionID]; ok {
				trace.Results = append(trace.Results, result)
			}
		}
		return nil
	}); err != nil {
		return AutomationChatTrace{}, err
	}
	for _, record := range s.auditFor("", "") {
		if _, ok := runIDs[record.RunID]; ok {
			trace.AuditEvents = append(trace.AuditEvents, record)
		}
	}
	sort.Slice(trace.TurnEvents, func(i, j int) bool { return trace.TurnEvents[i].Sequence < trace.TurnEvents[j].Sequence })
	return trace, nil
}

func (s *AutomationService) SubmitChatTurn(
	ctx context.Context,
	sessionID string,
	expectedVersion uint64,
	text string,
) (AutomationTurn, error) {
	return s.submitChatTurn(
		ctx,
		sessionID,
		expectedVersion,
		text,
		automationChatTurnSubmission{ActorType: "operator", Attempt: 1},
	)
}

func (s *AutomationService) submitChatTurn(
	ctx context.Context,
	sessionID string,
	expectedVersion uint64,
	text string,
	submission automationChatTurnSubmission,
) (AutomationTurn, error) {
	if ctx == nil {
		return AutomationTurn{}, NewAutomationValidationError("context", "is required")
	}
	if err := ctx.Err(); err != nil {
		return AutomationTurn{}, err
	}
	if s == nil || s.store == nil || s.chat == nil {
		return AutomationTurn{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	engine, ok := s.engine.(*defaultAutomationRunEngine)
	if !ok || engine.provider == nil {
		return AutomationTurn{}, NewAutomationValidationError("provider", "is unavailable")
	}
	var err error
	text, err = normalizeAutomationString("text", text, automationChatMaxTextRunes, true, true)
	if err != nil {
		return AutomationTurn{}, err
	}
	submission.ActorType = strings.TrimSpace(submission.ActorType)
	if submission.ActorType == "" {
		submission.ActorType = "operator"
	}
	if submission.Attempt == 0 {
		submission.Attempt = 1
	}
	submission.ProviderName = strings.TrimSpace(submission.ProviderName)
	if submission.ProviderName == "" {
		submission.ProviderName = engine.provider.Name()
	}
	submission.PromptRevisionID = strings.TrimSpace(submission.PromptRevisionID)
	if submission.PromptRevisionID == "" {
		submission.PromptRevisionID = engine.currentPromptRevisionID()
	}

	var turn AutomationTurn
	var run AutomationRun
	err = s.chat.withLock(func() error {
		return s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			session := automationSessionByID(state, strings.TrimSpace(sessionID))
			if session == nil || session.Kind != AutomationSessionKindChat {
				return errors.New("automation chat session not found")
			}
			if session.Status != AutomationSessionStatusActive {
				return NewAutomationValidationError("session", "is not active")
			}
			if session.Version != expectedVersion {
				return NewAutomationValidationError("expected_version", "chat session version conflict")
			}
			for _, existing := range state.Turns {
				if existing.SessionID == session.ID &&
					(existing.Status == AutomationTurnQueued ||
						existing.Status == AutomationTurnRunning ||
						existing.Status == AutomationTurnAwaitingReview) {
					return NewAutomationValidationError("session", "already has an active turn")
				}
			}
			turnID, err := allocator.Next(AutomationIDKindTurn)
			if err != nil {
				return err
			}
			runID, err := allocator.Next(AutomationIDKindRun)
			if err != nil {
				return err
			}
			now := time.Now().UTC()
			turn = AutomationTurn{
				ID: turnID, SessionID: session.ID, UserText: text,
				Status: AutomationTurnQueued, ProviderName: submission.ProviderName,
				PromptRevisionID: submission.PromptRevisionID,
				Version:          1, CreatedAt: now, UpdatedAt: now,
			}
			sourceKey := "chat:" + session.ID + ":" + turnID
			run = AutomationRun{
				ID: runID, SessionID: session.ID, TurnID: turnID,
				PromptRevisionID: submission.PromptRevisionID,
				IdempotencyKey:   sourceKey, EngineVersion: AutomationEngineVersion,
				ProviderName: turn.ProviderName, SourceMessageEventID: turnID,
				SourceMessageKey: sourceKey, SourceMessageVersion: 1,
				Status: AutomationRunQueued, Version: 1, Attempt: submission.Attempt,
				RetryOfRunID: submission.RetryOfRunID,
				ActionIDs:    []string{}, CreatedAt: now,
			}
			state.Turns = append(state.Turns, turn)
			state.Runs = append(state.Runs, run)
			state.Idempotency[sourceKey] = AutomationIdempotencyRecord{
				Kind: "run", ResourceID: runID, CreatedAt: now,
			}
			session.Version++
			session.UpdatedAt = now
			eventData := map[string]any{
				"turn": turn, "run_id": run.ID, "actor_type": submission.ActorType,
			}
			if submission.RetryOfRunID != "" {
				eventData["retry_of_run_id"] = submission.RetryOfRunID
			}
			_, err = appendAutomationTurnEvent(
				state, allocator, session.ID, turn.ID, AutomationTurnEventQueued,
				eventData,
				now,
			)
			return err
		})
	})
	if err != nil {
		return AutomationTurn{}, err
	}
	s.chat.publishPersisted(turn.ID)
	s.chat.start(automationChatRunContext(s), turn.ID, func(runCtx context.Context) {
		engine.processChatRun(runCtx, run.ID, turn.ID)
	})
	return turn, nil
}

func (s *AutomationService) CancelChatTurn(
	turnID string,
	expectedVersion uint64,
	actorID string,
) (AutomationTurn, error) {
	if s == nil || s.store == nil || s.chat == nil {
		return AutomationTurn{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	actorID = strings.TrimSpace(actorID)
	if actorID == "" {
		return AutomationTurn{}, NewAutomationValidationError("operator_id", "is required")
	}
	turnID = strings.TrimSpace(turnID)
	var cancelled AutomationTurn
	err := s.chat.withLock(func() error {
		return s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			turn := automationTurnByID(state, turnID)
			if turn == nil {
				return errors.New("automation chat turn not found")
			}
			if turn.Version != expectedVersion {
				return NewAutomationValidationError("expected_version", "chat turn version conflict")
			}
			if turn.Status == AutomationTurnCompleted ||
				turn.Status == AutomationTurnFailed ||
				turn.Status == AutomationTurnCancelled {
				return NewAutomationValidationError("turn", "is already terminal")
			}
			now := automationMutationTime(turn.UpdatedAt)
			turn.Status = AutomationTurnCancelled
			turn.Version++
			turn.UpdatedAt = now
			turn.CompletedAt = &now
			cancelled = *turn
			if session := automationSessionByID(state, turn.SessionID); session != nil {
				session.Version++
				session.UpdatedAt = now
			}
			for i := range state.Runs {
				run := &state.Runs[i]
				if run.TurnID != turn.ID {
					continue
				}
				if run.Status == AutomationRunQueued || run.Status == AutomationRunRunning {
					run.Status = AutomationRunFailed
					run.Version++
					run.FailureStage = "cancelled"
					run.FailureCode = "cancelled"
					run.FailureMessage = "chat turn cancelled by operator"
					run.CompletedAt = &now
				}
				for j := range state.Actions {
					action := &state.Actions[j]
					if action.RunID != run.ID || IsTerminalAutomationActionStatus(action.Status) ||
						action.Status == AutomationActionExecuting {
						continue
					}
					action.Status = AutomationActionSuperseded
					action.Version++
					action.UpdatedAt = now
				}
				for j := range state.PermissionRequests {
					request := &state.PermissionRequests[j]
					if request.RunID == run.ID && request.Status == AutomationPermissionStatusPending {
						request.Status = AutomationPermissionStatusExpired
						request.OperatorID = actorID
						request.Reason = "chat turn cancelled"
						request.Version++
						request.UpdatedAt = now
					}
				}
			}
			_, err := appendAutomationTurnEvent(
				state, allocator, turn.SessionID, turn.ID, AutomationTurnEventCancelled,
				map[string]any{"turn": *turn, "actor_type": "operator", "actor_id": actorID}, now,
			)
			return err
		})
	})
	if err != nil {
		return AutomationTurn{}, err
	}
	s.chat.cancel(turnID)
	s.chat.publishPersisted(cancelled.ID)
	return cancelled, nil
}

func (s *AutomationService) recoverInterruptedChatTurns() error {
	return s.failInterruptedChatTurns(
		nil,
		"restart_interrupted",
		"chat execution was interrupted by workbench restart",
	)
}

func (s *AutomationService) failInterruptedChatTurns(
	turnIDs map[string]struct{},
	code string,
	message string,
) error {
	if s == nil || s.store == nil {
		return NewAutomationValidationError("automation_service", "is unavailable")
	}
	return s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for i := range state.Runs {
			run := &state.Runs[i]
			if turnIDs != nil {
				if _, ok := turnIDs[run.TurnID]; !ok {
					continue
				}
			}
			session := automationSessionByID(state, run.SessionID)
			turn := automationTurnByID(state, run.TurnID)
			if session == nil || session.Kind != AutomationSessionKindChat || turn == nil {
				continue
			}
			if run.Status == AutomationRunSucceeded {
				if _, _, err := reconcileSucceededChatTurn(state, allocator, run, turn); err != nil {
					return err
				}
				continue
			}
			if turn.Status != AutomationTurnQueued && turn.Status != AutomationTurnRunning {
				continue
			}
			if run.Status != AutomationRunQueued && run.Status != AutomationRunRunning {
				continue
			}
			now := automationMutationTime(turn.UpdatedAt)
			turn.Status = AutomationTurnFailed
			turn.Version++
			turn.UpdatedAt = now
			turn.CompletedAt = &now
			session.Version++
			session.UpdatedAt = now

			run.Status = AutomationRunFailed
			run.Version++
			run.FailureStage = "lifecycle"
			run.FailureCode = code
			run.FailureMessage = message
			if run.StartedAt == nil {
				run.StartedAt = &now
			}
			run.CompletedAt = &now
			if _, err := appendAutomationTurnEvent(
				state, allocator, turn.SessionID, turn.ID, AutomationTurnEventFailed,
				map[string]any{
					"turn": *turn, "run_id": run.ID, "actor_type": "system",
					"failure_code": code,
				},
				now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *AutomationService) SubscribeTurnEvents(
	turnID string,
	after uint64,
) ([]AutomationTurnEvent, <-chan AutomationTurnEvent, func(), error) {
	if s == nil || s.store == nil || s.chat == nil {
		return nil, nil, nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	return s.chat.subscribe(strings.TrimSpace(turnID), after)
}

func (c *automationChatCoordinator) withLock(fn func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return NewAutomationValidationError("automation_chat", "is closed")
	}
	return fn()
}

func automationChatRunContext(service *AutomationService) context.Context {
	if runtime := automationRuntimeForService(service); runtime != nil && runtime.ctx != nil {
		return runtime.ctx
	}
	return context.Background()
}

func (c *automationChatCoordinator) start(
	parent context.Context,
	turnID string,
	work func(context.Context),
) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		return
	}
	c.active[turnID] = cancel
	c.mu.Unlock()
	go func() {
		defer func() {
			c.mu.Lock()
			delete(c.active, turnID)
			c.mu.Unlock()
			cancel()
		}()
		work(ctx)
	}()
}

func (c *automationChatCoordinator) cancel(turnID string) {
	c.mu.Lock()
	cancel := c.active[turnID]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *automationChatCoordinator) subscribe(
	turnID string,
	after uint64,
) ([]AutomationTurnEvent, <-chan AutomationTurnEvent, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var replay []AutomationTurnEvent
	if err := c.service.store.Read(func(state *AutomationState) error {
		if automationTurnByID(state, turnID) == nil {
			return errors.New("automation chat turn not found")
		}
		replay = make([]AutomationTurnEvent, 0)
		for _, event := range state.TurnEvents {
			if event.TurnID == turnID && event.Sequence > after {
				replay = append(replay, event)
			}
		}
		return nil
	}); err != nil {
		return nil, nil, nil, err
	}
	channel := make(chan AutomationTurnEvent, 32)
	if c.closed {
		close(channel)
		return replay, channel, func() {}, nil
	}
	if c.subscribers[turnID] == nil {
		c.subscribers[turnID] = make(map[chan AutomationTurnEvent]struct{})
	}
	c.subscribers[turnID][channel] = struct{}{}
	cancel := func() {
		c.mu.Lock()
		if subscribers := c.subscribers[turnID]; subscribers != nil {
			if _, ok := subscribers[channel]; ok {
				delete(subscribers, channel)
				close(channel)
			}
			if len(subscribers) == 0 {
				delete(c.subscribers, turnID)
			}
		}
		c.mu.Unlock()
	}
	return replay, channel, cancel, nil
}

func (c *automationChatCoordinator) publishPersisted(turnID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var latest *AutomationTurnEvent
	_ = c.service.store.Read(func(state *AutomationState) error {
		var found *AutomationTurnEvent
		for i := range state.TurnEvents {
			event := &state.TurnEvents[i]
			if event.TurnID == turnID && (found == nil || event.Sequence > found.Sequence) {
				found = event
			}
		}
		if found != nil {
			value := *found
			latest = &value
		}
		return nil
	})
	if latest != nil {
		c.publishLocked(*latest)
	}
}

func (c *automationChatCoordinator) publishPersistedEvents(events []AutomationTurnEvent) {
	if c == nil || len(events) == 0 {
		return
	}
	ordered := append([]AutomationTurnEvent(nil), events...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Sequence < ordered[j].Sequence
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range ordered {
		c.publishLocked(event)
	}
}

func (c *automationChatCoordinator) appendEvent(
	sessionID, turnID, eventType string,
	data any,
	now time.Time,
) (AutomationTurnEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return AutomationTurnEvent{}, NewAutomationValidationError("automation_chat", "is closed")
	}
	var event AutomationTurnEvent
	err := c.service.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		var err error
		event, err = appendAutomationTurnEvent(state, allocator, sessionID, turnID, eventType, data, now)
		return err
	})
	if err == nil {
		c.publishLocked(event)
	}
	return event, err
}

func (c *automationChatCoordinator) publishLocked(event AutomationTurnEvent) {
	for subscriber := range c.subscribers[event.TurnID] {
		select {
		case subscriber <- event:
		default:
			delete(c.subscribers[event.TurnID], subscriber)
			close(subscriber)
		}
	}
}

func appendAutomationTurnEvent(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	sessionID, turnID, eventType string,
	data any,
	now time.Time,
) (AutomationTurnEvent, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return AutomationTurnEvent{}, err
	}
	sequence, err := allocator.NextTurnEventSequence()
	if err != nil {
		return AutomationTurnEvent{}, err
	}
	event := AutomationTurnEvent{
		Sequence: sequence, SessionID: sessionID, TurnID: turnID,
		Type: eventType, Data: sanitizeAutomationJSON(encoded), CreatedAt: now.UTC(),
	}
	state.TurnEvents = append(state.TurnEvents, event)
	return event, nil
}

func (e *defaultAutomationRunEngine) processChatRun(ctx context.Context, runID, turnID string) {
	if err := e.runChatStream(ctx, runID, turnID); err != nil && !errors.Is(err, context.Canceled) {
		stage, code := automationChatFailureDetails(err)
		if failErr := e.failChatTurn(runID, turnID, stage, code, err); failErr == nil {
			e.retryFailedChatTurn(ctx, runID, turnID, stage, code, err)
		}
	}
}

func (s *AutomationService) publishChatRuntimeEvent(event AutomationRuntimeEvent) {
	if s == nil || s.chat == nil || event.RunID == "" {
		return
	}
	var run AutomationRun
	found := false
	_ = s.store.Read(func(state *AutomationState) error {
		runIndex := automationRunIndex(state, event.RunID)
		if runIndex < 0 {
			return nil
		}
		session := automationSessionByID(state, state.Runs[runIndex].SessionID)
		if session == nil || session.Kind != AutomationSessionKindChat {
			return nil
		}
		run = state.Runs[runIndex]
		found = true
		return nil
	})
	if !found {
		return
	}
	eventType := event.Type
	switch event.Type {
	case AutomationAuditEventActionProposed:
		return
	case AutomationAuditEventActionExecuting:
		eventType = AutomationTurnEventToolExecuting
	case AutomationAuditEventActionSucceeded:
		eventType = AutomationTurnEventToolSucceeded
	case AutomationAuditEventActionFailed:
		eventType = AutomationTurnEventToolFailed
	}
	var data any = map[string]any{
		"run_id": event.RunID, "action_id": event.ActionID,
		"permission_request_id": event.PermissionRequestID,
		"event_type":            event.Type, "actor_type": "system",
	}
	_, _ = s.chat.appendEvent(run.SessionID, run.TurnID, eventType, data, event.CreatedAt)
}

func (s *AutomationService) reconcileChatTurnForRun(runID string) {
	if s == nil || s.chat == nil || runID == "" {
		return
	}
	var changed bool
	var persistedEvent AutomationTurnEvent
	err := s.chat.withLock(func() error {
		return s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			runIndex := automationRunIndex(state, runID)
			if runIndex < 0 {
				return nil
			}
			run := state.Runs[runIndex]
			session := automationSessionByID(state, run.SessionID)
			turn := automationTurnByID(state, run.TurnID)
			if session == nil || session.Kind != AutomationSessionKindChat || turn == nil ||
				turn.Status == AutomationTurnCancelled || turn.Status == AutomationTurnFailed ||
				turn.Status == AutomationTurnCompleted {
				return nil
			}
			if run.Status != AutomationRunSucceeded {
				return nil
			}
			var reconcileErr error
			changed, persistedEvent, reconcileErr = reconcileSucceededChatTurn(
				state,
				allocator,
				&run,
				turn,
			)
			return reconcileErr
		})
	})
	if err == nil && changed && persistedEvent.Sequence != 0 {
		s.chat.publishPersistedEvents([]AutomationTurnEvent{persistedEvent})
	}
}

func reconcileSucceededChatTurn(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	run *AutomationRun,
	turn *AutomationTurn,
) (bool, AutomationTurnEvent, error) {
	if state == nil || allocator == nil || run == nil || turn == nil ||
		run.Status != AutomationRunSucceeded ||
		turn.Status == AutomationTurnCancelled ||
		turn.Status == AutomationTurnFailed ||
		turn.Status == AutomationTurnCompleted {
		return false, AutomationTurnEvent{}, nil
	}
	awaiting := false
	active := false
	for _, action := range state.Actions {
		if action.RunID != run.ID {
			continue
		}
		if action.Status == AutomationActionAwaitingReview {
			awaiting = true
		}
		if action.Status == AutomationActionApproved ||
			action.Status == AutomationActionExecuting ||
			action.Status == AutomationActionProposed {
			active = true
		}
	}
	now := automationMutationTime(turn.UpdatedAt)
	if awaiting {
		if turn.Status == AutomationTurnAwaitingReview {
			return false, AutomationTurnEvent{}, nil
		}
		turn.Status = AutomationTurnAwaitingReview
		turn.Version++
		turn.UpdatedAt = now
		return true, AutomationTurnEvent{}, nil
	}
	if active {
		return false, AutomationTurnEvent{}, nil
	}
	turn.Status = AutomationTurnCompleted
	turn.Version++
	turn.UpdatedAt = now
	turn.CompletedAt = &now
	if session := automationSessionByID(state, turn.SessionID); session != nil {
		session.Version++
		session.UpdatedAt = now
	}
	event, err := appendAutomationTurnEvent(
		state,
		allocator,
		turn.SessionID,
		turn.ID,
		AutomationTurnEventCompleted,
		map[string]any{"turn": *turn, "actor_type": "system"},
		now,
	)
	return true, event, err
}
