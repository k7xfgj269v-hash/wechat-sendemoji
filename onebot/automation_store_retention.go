package main

import (
	"errors"
	"fmt"
	"sort"
)

func applyAutomationRetention(state *AutomationState, limits automationRetentionLimits) bool {
	// Fast path: when every prunable collection is at or below its retention
	// window, the keep sets are necessarily complete (every entity is kept),
	// so nothing can change. Outbox entries are kept through their actions'
	// runs (and validated states never contain dangling outbox references),
	// so the outbox is covered by the runs window. This skips building the
	// per-collection indexes on every mutation while the system is well under
	// its limits.
	if len(state.Sessions) <= limits.Sessions &&
		len(state.Turns) <= limits.Turns &&
		len(state.Runs) <= limits.Runs {
		return false
	}
	sessions := make(map[string]*AutomationSession, len(state.Sessions))
	for i := range state.Sessions {
		sessions[state.Sessions[i].ID] = &state.Sessions[i]
	}
	turns := make(map[string]*AutomationTurn, len(state.Turns))
	for i := range state.Turns {
		turns[state.Turns[i].ID] = &state.Turns[i]
	}
	runs := make(map[string]*AutomationRun, len(state.Runs))
	for i := range state.Runs {
		runs[state.Runs[i].ID] = &state.Runs[i]
	}
	actions := make(map[string]*AutomationAction, len(state.Actions))
	for i := range state.Actions {
		actions[state.Actions[i].ID] = &state.Actions[i]
	}
	outbox := make(map[string]*AutomationOutboxEntry, len(state.Outbox))
	for i := range state.Outbox {
		outbox[state.Outbox[i].ID] = &state.Outbox[i]
	}

	keepSessions := make(map[string]struct{})
	keepTurns := make(map[string]struct{})
	keepRuns := make(map[string]struct{})
	keepOutbox := make(map[string]struct{})

	keepTurn := func(turnID string) {
		turn := turns[turnID]
		if turn == nil {
			return
		}
		keepTurns[turn.ID] = struct{}{}
		keepSessions[turn.SessionID] = struct{}{}
	}
	var keepRun func(string)
	keepRun = func(runID string) {
		run := runs[runID]
		if run == nil {
			return
		}
		if _, exists := keepRuns[run.ID]; exists {
			return
		}
		keepRuns[run.ID] = struct{}{}
		keepTurn(run.TurnID)
		keepSessions[run.SessionID] = struct{}{}
		if run.RetryOfRunID != "" {
			keepRun(run.RetryOfRunID)
		}
	}
	keepAction := func(actionID string) {
		if action := actions[actionID]; action != nil {
			keepRun(action.RunID)
		}
	}
	keepOutboxEntry := func(outboxID string) {
		entry := outbox[outboxID]
		if entry == nil {
			return
		}
		keepOutbox[entry.ID] = struct{}{}
		keepAction(entry.ActionID)
	}

	for i := range state.Sessions {
		if state.Sessions[i].Status == AutomationSessionStatusActive {
			keepSessions[state.Sessions[i].ID] = struct{}{}
		}
	}
	for i := range state.Turns {
		switch state.Turns[i].Status {
		case AutomationTurnQueued, AutomationTurnRunning, AutomationTurnAwaitingReview:
			keepTurn(state.Turns[i].ID)
		}
	}
	for i := range state.Runs {
		switch state.Runs[i].Status {
		case AutomationRunQueued, AutomationRunRunning:
			keepRun(state.Runs[i].ID)
		}
	}
	for i := range state.Actions {
		if !IsTerminalAutomationActionStatus(state.Actions[i].Status) {
			keepRun(state.Actions[i].RunID)
		}
	}
	for i := range state.PermissionRequests {
		if state.PermissionRequests[i].Status == AutomationPermissionStatusPending {
			keepRun(state.PermissionRequests[i].RunID)
		}
	}
	for i := range state.Outbox {
		switch state.Outbox[i].Status {
		case AutomationOutboxPending, AutomationOutboxDispatching:
			keepOutboxEntry(state.Outbox[i].ID)
		}
	}
	for i := range state.PendingAudit {
		record := &state.PendingAudit[i]
		keepRun(record.RunID)
		keepAction(record.ActionID)
		keepOutboxEntry(record.OutboxID)
	}

	sessionCandidates := make([]automationRetentionCandidate, 0, len(state.Sessions))
	for i := range state.Sessions {
		session := &state.Sessions[i]
		if session.Status != AutomationSessionStatusActive {
			sessionCandidates = append(sessionCandidates, automationRetentionCandidate{
				ID:        session.ID,
				Timestamp: session.UpdatedAt,
			})
		}
	}
	keepNewestAutomationRetention(sessionCandidates, limits.Sessions, keepSessions, func(id string) {
		keepSessions[id] = struct{}{}
	})
	for sessionID := range keepSessions {
		session := sessions[sessionID]
		if session != nil && session.ContextCheckpoint != nil {
			keepTurn(session.ContextCheckpoint.ThroughTurnID)
		}
	}

	turnCandidates := make([]automationRetentionCandidate, 0, len(state.Turns))
	for i := range state.Turns {
		turn := &state.Turns[i]
		switch turn.Status {
		case AutomationTurnCompleted, AutomationTurnFailed, AutomationTurnCancelled:
			turnCandidates = append(turnCandidates, automationRetentionCandidate{
				ID:        turn.ID,
				Timestamp: turn.UpdatedAt,
			})
		}
	}
	keepNewestAutomationRetention(turnCandidates, limits.Turns, keepTurns, keepTurn)

	runCandidates := make([]automationRetentionCandidate, 0, len(state.Runs))
	for i := range state.Runs {
		run := &state.Runs[i]
		switch run.Status {
		case AutomationRunSucceeded, AutomationRunFailed:
			runCandidates = append(runCandidates, automationRetentionCandidate{
				ID:        run.ID,
				Timestamp: run.CreatedAt,
			})
		}
	}
	keepNewestAutomationRetention(runCandidates, limits.Runs, keepRuns, keepRun)

	idempotencyCandidates := make([]automationRetentionCandidate, 0, len(state.Idempotency))
	for key, record := range state.Idempotency {
		idempotencyCandidates = append(idempotencyCandidates, automationRetentionCandidate{
			ID:        key,
			Timestamp: record.CreatedAt,
		})
	}
	keepIdempotency := make(map[string]struct{})
	keepNewestAutomationRetention(
		idempotencyCandidates,
		limits.Idempotency,
		keepIdempotency,
		func(key string) {
			keepIdempotency[key] = struct{}{}
			record := state.Idempotency[key]
			switch record.Kind {
			case "run":
				keepRun(record.ResourceID)
			case "outbox":
				keepOutboxEntry(record.ResourceID)
			}
		},
	)

	for i := range state.Outbox {
		entry := &state.Outbox[i]
		action := actions[entry.ActionID]
		if action == nil {
			continue
		}
		if _, exists := keepRuns[action.RunID]; exists {
			keepOutbox[entry.ID] = struct{}{}
		}
	}

	changed := len(keepSessions) != len(state.Sessions) ||
		len(keepTurns) != len(state.Turns) ||
		len(keepRuns) != len(state.Runs) ||
		len(keepOutbox) != len(state.Outbox)
	if !changed {
		return false
	}

	state.Sessions = filterAutomationRetention(state.Sessions, func(session AutomationSession) bool {
		_, exists := keepSessions[session.ID]
		return exists
	})
	state.Turns = filterAutomationRetention(state.Turns, func(turn AutomationTurn) bool {
		_, exists := keepTurns[turn.ID]
		return exists
	})
	state.TurnEvents = filterAutomationRetention(state.TurnEvents, func(event AutomationTurnEvent) bool {
		_, exists := keepTurns[event.TurnID]
		return exists
	})
	state.Runs = filterAutomationRetention(state.Runs, func(run AutomationRun) bool {
		_, exists := keepRuns[run.ID]
		return exists
	})
	state.Actions = filterAutomationRetention(state.Actions, func(action AutomationAction) bool {
		_, exists := keepRuns[action.RunID]
		return exists
	})
	state.Results = filterAutomationRetention(state.Results, func(result AutomationResult) bool {
		action := actions[result.ActionID]
		if action == nil {
			return false
		}
		_, exists := keepRuns[action.RunID]
		return exists
	})
	state.Approvals = filterAutomationRetention(state.Approvals, func(approval AutomationApprovalRecord) bool {
		action := actions[approval.ActionID]
		if action == nil {
			return false
		}
		_, exists := keepRuns[action.RunID]
		return exists
	})
	state.Outbox = filterAutomationRetention(state.Outbox, func(entry AutomationOutboxEntry) bool {
		_, exists := keepOutbox[entry.ID]
		return exists
	})
	idempotency := make(map[string]AutomationIdempotencyRecord)
	for key, record := range state.Idempotency {
		switch record.Kind {
		case "run":
			if _, exists := keepRuns[record.ResourceID]; exists {
				idempotency[key] = record
			}
		case "outbox":
			if _, exists := keepOutbox[record.ResourceID]; exists {
				idempotency[key] = record
			}
		}
	}
	state.Idempotency = idempotency
	state.PermissionRequests = filterAutomationRetention(
		state.PermissionRequests,
		func(request AutomationPermissionRequest) bool {
			_, exists := keepRuns[request.RunID]
			return exists
		},
	)
	return true
}

func keepNewestAutomationRetention(
	candidates []automationRetentionCandidate,
	limit int,
	kept map[string]struct{},
	keep func(string),
) {
	if len(kept) >= limit {
		return
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Timestamp.Equal(candidates[j].Timestamp) {
			return candidates[i].ID > candidates[j].ID
		}
		return candidates[i].Timestamp.After(candidates[j].Timestamp)
	})
	for _, candidate := range candidates {
		if len(kept) >= limit {
			return
		}
		if _, exists := kept[candidate.ID]; exists {
			continue
		}
		keep(candidate.ID)
	}
}

func filterAutomationRetention[T any](values []T, keep func(T) bool) []T {
	retained := make([]T, 0, len(values))
	for _, value := range values {
		if keep(value) {
			retained = append(retained, value)
		}
	}
	return retained
}

func (s *AutomationStore) blockPersistenceIfUncertain(err error) {
	if errors.Is(err, errAutomationPersistenceUncertain) {
		s.persistenceBlocked = err
	}
}

func (a *AutomationIDAllocator) Next(kind AutomationIDKind) (string, error) {
	if a == nil || !a.active {
		return "", NewAutomationValidationError("id_allocator", "is not active")
	}

	prefix, counter, err := a.counter(kind)
	if err != nil {
		return "", err
	}
	if *counter >= automationMaxIDCounter {
		return "", NewAutomationValidationError("counters."+string(kind), "ID counter is exhausted")
	}
	*counter = *counter + 1
	return fmt.Sprintf("%s_%08d", prefix, *counter), nil
}

func (a *AutomationIDAllocator) counter(kind AutomationIDKind) (string, *uint64, error) {
	switch kind {
	case AutomationIDKindSession:
		return "aisession", &a.counters.Session, nil
	case AutomationIDKindTurn:
		return "aiturn", &a.counters.Turn, nil
	case AutomationIDKindRun:
		return "airun", &a.counters.Run, nil
	case AutomationIDKindAction:
		return "aiact", &a.counters.Action, nil
	case AutomationIDKindResult:
		return "airesult", &a.counters.Result, nil
	case AutomationIDKindApproval:
		return "aiapproval", &a.counters.Approval, nil
	case AutomationIDKindOutbox:
		return "aiout", &a.counters.Outbox, nil
	case AutomationIDKindAudit:
		return "aiaudit", &a.counters.Audit, nil
	case AutomationIDKindPermissionRequest:
		return "aipermreq", &a.counters.PermissionRequest, nil
	case AutomationIDKindPermissionRule:
		return "aipermrule", &a.counters.PermissionRule, nil
	default:
		return "", nil, NewAutomationValidationError("id_kind", "is invalid")
	}
}

func (a *AutomationIDAllocator) NextTurnEventSequence() (uint64, error) {
	if a == nil || !a.active {
		return 0, NewAutomationValidationError("id_allocator", "is not active")
	}
	if a.counters.TurnEvent >= automationMaxIDCounter {
		return 0, NewAutomationValidationError("counters.turn_event", "sequence counter is exhausted")
	}
	a.counters.TurnEvent++
	return a.counters.TurnEvent, nil
}
