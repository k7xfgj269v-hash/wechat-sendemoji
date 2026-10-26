package main

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func (e *defaultAutomationRunEngine) prepareExplicitRunRecovery(runID string) error {
	timeout := e.runLifecycleTimeout()
	return e.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationRunIndex(state, runID)
		if index < 0 {
			return errors.New("automation run not found")
		}
		run := &state.Runs[index]
		switch run.Status {
		case AutomationRunQueued:
			return nil
		case AutomationRunRunning:
			if run.StartedAt == nil || time.Since(*run.StartedAt) < timeout {
				return NewAutomationValidationError("run", "is still within its execution timeout")
			}
			run.Status = AutomationRunQueued
			run.Version++
			run.StartedAt = nil
			return nil
		default:
			return NewAutomationValidationError("run", "is not recoverable")
		}
	})
}

func (e *defaultAutomationRunEngine) recordRunEvent(
	eventType string,
	run AutomationRun,
	data json.RawMessage,
) error {
	err := e.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: eventType,
			RunID:     run.ID,
			ActorType: "system",
			Data:      sanitizeAutomationJSON(data),
			CreatedAt: time.Now().UTC(),
		})
		return err
	})
	if err != nil {
		return err
	}
	if e.supervision != nil {
		e.supervision.publishRunEvent(eventType, run, data)
	}
	return nil
}

func (e *defaultAutomationRunEngine) failRun(runID, stage, code, message, responseHash string) error {
	message = strings.TrimSpace(message)
	if len([]rune(message)) > 4000 {
		message = string([]rune(message)[:4000])
	}
	err := e.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationRunIndex(state, runID)
		if index < 0 {
			return errors.New("automation run not found")
		}
		if state.Runs[index].Status != AutomationRunRunning {
			return nil
		}
		now := time.Now().UTC()
		state.Runs[index].Status = AutomationRunFailed
		state.Runs[index].Version++
		state.Runs[index].FailureStage = stage
		state.Runs[index].FailureCode = code
		state.Runs[index].FailureMessage = message
		state.Runs[index].ProviderResponseSHA256 = responseHash
		state.Runs[index].CompletedAt = &now
		return nil
	})
	if err != nil {
		return err
	}
	return errors.New(message)
}

func (e *defaultAutomationRunEngine) failQueuedRun(runID, stage, code, message string) error {
	message = strings.TrimSpace(message)
	if len([]rune(message)) > 4000 {
		message = string([]rune(message)[:4000])
	}
	err := e.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationRunIndex(state, runID)
		if index < 0 {
			return errors.New("automation run not found")
		}
		if state.Runs[index].Status != AutomationRunQueued {
			return nil
		}
		now := time.Now().UTC()
		state.Runs[index].Status = AutomationRunFailed
		state.Runs[index].Version++
		state.Runs[index].FailureStage = stage
		state.Runs[index].FailureCode = code
		state.Runs[index].FailureMessage = message
		state.Runs[index].StartedAt = &now
		state.Runs[index].CompletedAt = &now
		return nil
	})
	if err != nil {
		return err
	}
	return errors.New(message)
}

func automationRunIndex(state *AutomationState, id string) int {
	for i := range state.Runs {
		if state.Runs[i].ID == id {
			return i
		}
	}
	return -1
}

func automationRunQueueMetrics(state AutomationState, now time.Time) (int, time.Duration) {
	depth := 0
	var oldest time.Time
	for _, run := range state.Runs {
		if run.Status != AutomationRunQueued {
			continue
		}
		depth++
		if oldest.IsZero() || run.CreatedAt.Before(oldest) {
			oldest = run.CreatedAt
		}
	}
	return operationalQueueAge(depth, oldest, now)
}
