package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	AutomationAuditEventOutboxPending          = "outbox.pending"
	AutomationAuditEventOutboxDispatching      = "outbox.dispatching"
	AutomationAuditEventOutboxDispatchAccepted = "outbox.dispatch_accepted"
	AutomationAuditEventOutboxSent             = "outbox.sent"
	AutomationAuditEventOutboxFailed           = "outbox.failed"
	AutomationAuditEventOutboxUnknown          = "outbox.unknown"
)

var (
	ErrAutomationOutboxConflict = errors.New("automation outbox conflict")
	ErrAutomationOutboxNotFound = errors.New("automation outbox not found")

	errAutomationOutboxNoMutation = errors.New("automation outbox no mutation")
)

type AutomationOutboxConflictError struct {
	OutboxID string
	ActionID string
	Status   AutomationOutboxStatus
	Message  string
}

func (e *AutomationOutboxConflictError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.OutboxID != "" {
		return fmt.Sprintf("outbox %s is in status %q", e.OutboxID, e.Status)
	}
	return "automation outbox conflict"
}

func (e *AutomationOutboxConflictError) Unwrap() error {
	return ErrAutomationOutboxConflict
}

type AutomationOutboxNotFoundError struct {
	OutboxID string
	ActionID string
}

func (e *AutomationOutboxNotFoundError) Error() string {
	if e.OutboxID != "" {
		return "automation outbox entry not found: " + e.OutboxID
	}
	if e.ActionID != "" {
		return "automation outbox entry not found for action: " + e.ActionID
	}
	return ErrAutomationOutboxNotFound.Error()
}

func (e *AutomationOutboxNotFoundError) Unwrap() error {
	return ErrAutomationOutboxNotFound
}

type automationOutboxAuditData struct {
	PreviousStatus    AutomationOutboxStatus `json:"previous_status,omitempty"`
	Status            AutomationOutboxStatus `json:"status"`
	Attempt           uint32                 `json:"attempt"`
	DispatchAttempted bool                   `json:"dispatch_attempted"`
	ErrorCode         string                 `json:"error_code,omitempty"`
	Recovery          bool                   `json:"recovery,omitempty"`
}

var _ AutomationOutboxLedger = (*AutomationStore)(nil)

func (s *AutomationStore) Ensure(
	ctx context.Context,
	action AutomationAction,
	peerID string,
	text string,
) (AutomationOutboxEntry, bool, error) {
	return s.EnsurePending(ctx, action.ID, peerID, text)
}

func (s *AutomationStore) EnsurePending(
	ctx context.Context,
	actionID string,
	peerID string,
	text string,
) (AutomationOutboxEntry, bool, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	if err := validateStoredAutomationString("action_id", actionID, 80, true, false); err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	if err := validateStoredAutomationString("peer_id", peerID, 256, true, false); err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	if err := validateStoredAutomationString("text", text, 4000, true, true); err != nil {
		return AutomationOutboxEntry{}, false, err
	}

	var result AutomationOutboxEntry
	created := false
	err := s.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if err := automationOutboxValidateContext(ctx); err != nil {
			return err
		}
		action := automationOutboxFindAction(state, actionID)
		if action == nil {
			return &AutomationOutboxNotFoundError{ActionID: actionID}
		}

		key := "send:" + actionID
		if record, exists := state.Idempotency[key]; exists {
			entry := automationOutboxFindByID(state, record.ResourceID)
			if record.Kind != "outbox" || entry == nil || entry.ActionID != actionID {
				return &AutomationOutboxConflictError{
					ActionID: actionID,
					Message:  "canonical outbox idempotency key is inconsistent",
				}
			}
			result = cloneAutomationOutboxEntry(*entry)
			return errAutomationOutboxNoMutation
		}
		if existing := automationOutboxFindByActionID(state, actionID); existing != nil {
			return &AutomationOutboxConflictError{
				OutboxID: existing.ID,
				ActionID: actionID,
				Status:   existing.Status,
				Message:  "action already has an outbox entry without its canonical idempotency key",
			}
		}
		if action.Tool != AutomationToolReplyPrivateText ||
			action.Recipient == nil ||
			action.Recipient.ChatType != ChatTypePrivate ||
			action.Recipient.PeerID != peerID {
			return &AutomationOutboxConflictError{
				ActionID: actionID,
				Message:  "action is not linked to the requested private recipient",
			}
		}

		outboxID, err := allocator.Next(AutomationIDKindOutbox)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		entry := AutomationOutboxEntry{
			ID:                outboxID,
			ActionID:          actionID,
			IdempotencyKey:    key,
			PeerID:            peerID,
			Text:              text,
			Status:            AutomationOutboxPending,
			Attempt:           0,
			DispatchAttempted: false,
			CreatedAt:         now,
			UpdatedAt:         now,
		}
		state.Outbox = append(state.Outbox, entry)
		state.Idempotency[key] = AutomationIdempotencyRecord{
			Kind:       "outbox",
			ResourceID: outboxID,
			CreatedAt:  now,
		}
		if err := automationOutboxAppendAudit(
			state,
			allocator,
			entry,
			"",
			AutomationAuditEventOutboxPending,
			false,
			now,
		); err != nil {
			return err
		}
		result = cloneAutomationOutboxEntry(entry)
		created = true
		return nil
	})
	if errors.Is(err, errAutomationOutboxNoMutation) {
		return result, false, nil
	}
	if err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	return result, created, nil
}

func (s *AutomationStore) ClaimPending(
	ctx context.Context,
	outboxID string,
) (AutomationOutboxEntry, bool, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	if err := validateStoredAutomationString("outbox_id", outboxID, 80, true, false); err != nil {
		return AutomationOutboxEntry{}, false, err
	}

	var result AutomationOutboxEntry
	err := s.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if err := automationOutboxValidateContext(ctx); err != nil {
			return err
		}
		entry := automationOutboxFindByID(state, outboxID)
		if entry == nil {
			return &AutomationOutboxNotFoundError{OutboxID: outboxID}
		}
		result = cloneAutomationOutboxEntry(*entry)
		if entry.Status != AutomationOutboxPending {
			return errAutomationOutboxNoMutation
		}

		now := time.Now().UTC()
		entry.Status = AutomationOutboxDispatching
		entry.Attempt++
		entry.DispatchAttempted = false
		entry.ErrorCode = ""
		entry.ErrorMessage = ""
		entry.UpdatedAt = now
		entry.DispatchStartedAt = automationOutboxTimePointer(now)
		entry.CompletedAt = nil
		if err := automationOutboxAppendAudit(
			state,
			allocator,
			*entry,
			AutomationOutboxPending,
			AutomationAuditEventOutboxDispatching,
			false,
			now,
		); err != nil {
			return err
		}
		result = cloneAutomationOutboxEntry(*entry)
		return nil
	})
	if errors.Is(err, errAutomationOutboxNoMutation) {
		return result, false, nil
	}
	if err != nil {
		return AutomationOutboxEntry{}, false, err
	}
	return result, true, nil
}

func (s *AutomationStore) MarkDispatchAccepted(
	ctx context.Context,
	outboxID string,
) (AutomationOutboxEntry, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, err
	}
	if err := validateStoredAutomationString("outbox_id", outboxID, 80, true, false); err != nil {
		return AutomationOutboxEntry{}, err
	}

	var result AutomationOutboxEntry
	err := s.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if err := automationOutboxValidateContext(ctx); err != nil {
			return err
		}
		entry := automationOutboxFindByID(state, outboxID)
		if entry == nil {
			return &AutomationOutboxNotFoundError{OutboxID: outboxID}
		}
		if entry.Status != AutomationOutboxDispatching {
			return automationOutboxConflict(*entry, "dispatch acceptance requires dispatching status")
		}
		result = cloneAutomationOutboxEntry(*entry)
		if entry.DispatchAttempted {
			return errAutomationOutboxNoMutation
		}

		now := time.Now().UTC()
		entry.DispatchAttempted = true
		entry.UpdatedAt = now
		if err := automationOutboxAppendAudit(
			state,
			allocator,
			*entry,
			AutomationOutboxDispatching,
			AutomationAuditEventOutboxDispatchAccepted,
			false,
			now,
		); err != nil {
			return err
		}
		result = cloneAutomationOutboxEntry(*entry)
		return nil
	})
	if errors.Is(err, errAutomationOutboxNoMutation) {
		return result, nil
	}
	if err != nil {
		return AutomationOutboxEntry{}, err
	}
	return result, nil
}

func (s *AutomationStore) CompleteDispatch(
	ctx context.Context,
	outboxID string,
	delivery AutomationDeliveryResult,
) (AutomationOutboxEntry, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, err
	}
	if err := validateStoredAutomationString("outbox_id", outboxID, 80, true, false); err != nil {
		return AutomationOutboxEntry{}, err
	}
	if err := automationOutboxValidateDelivery(delivery); err != nil {
		return AutomationOutboxEntry{}, err
	}

	var result AutomationOutboxEntry
	err := s.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if err := automationOutboxValidateContext(ctx); err != nil {
			return err
		}
		entry := automationOutboxFindByID(state, outboxID)
		if entry == nil {
			return &AutomationOutboxNotFoundError{OutboxID: outboxID}
		}
		if entry.Status != AutomationOutboxDispatching {
			return automationOutboxConflict(*entry, "dispatch completion requires dispatching status")
		}

		accepted := entry.DispatchAttempted || delivery.DispatchAttempted
		target := AutomationOutboxUnknown
		switch {
		case !accepted && delivery.Status == AutomationDeliveryFailed:
			target = AutomationOutboxFailed
		case !accepted:
			return automationOutboxConflict(*entry, "sent or unknown delivery requires dispatch acceptance")
		case delivery.Status == AutomationDeliverySent:
			target = AutomationOutboxSent
		default:
			target = AutomationOutboxUnknown
		}

		now := time.Now().UTC()
		entry.Status = target
		entry.DispatchAttempted = accepted
		entry.ErrorCode = delivery.ErrorCode
		entry.ErrorMessage = delivery.ErrorMessage
		if target == AutomationOutboxSent {
			entry.ErrorCode = ""
			entry.ErrorMessage = ""
		}
		entry.UpdatedAt = now
		entry.CompletedAt = automationOutboxTimePointer(now)
		if err := automationOutboxAppendAudit(
			state,
			allocator,
			*entry,
			AutomationOutboxDispatching,
			automationOutboxTerminalAuditEvent(target),
			false,
			now,
		); err != nil {
			return err
		}
		result = cloneAutomationOutboxEntry(*entry)
		return nil
	})
	if err != nil {
		return AutomationOutboxEntry{}, err
	}
	return result, nil
}

func (s *AutomationStore) RecoverDispatchingOutbox(
	ctx context.Context,
) ([]AutomationOutboxEntry, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return nil, err
	}

	var recovered []AutomationOutboxEntry
	err := s.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		if err := automationOutboxValidateContext(ctx); err != nil {
			return err
		}
		now := time.Now().UTC()
		for i := range state.Outbox {
			entry := &state.Outbox[i]
			if entry.Status != AutomationOutboxDispatching {
				continue
			}
			entry.Status = AutomationOutboxUnknown
			entry.DispatchAttempted = true
			entry.ErrorCode = "restart_interrupted"
			entry.ErrorMessage = "dispatch outcome is unknown after restart"
			entry.UpdatedAt = now
			entry.CompletedAt = automationOutboxTimePointer(now)
			if err := automationOutboxAppendAudit(
				state,
				allocator,
				*entry,
				AutomationOutboxDispatching,
				AutomationAuditEventOutboxUnknown,
				true,
				now,
			); err != nil {
				return err
			}
			recovered = append(recovered, cloneAutomationOutboxEntry(*entry))
		}
		if len(recovered) == 0 {
			return errAutomationOutboxNoMutation
		}
		return nil
	})
	if errors.Is(err, errAutomationOutboxNoMutation) {
		return []AutomationOutboxEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	return recovered, nil
}

func (s *AutomationStore) RecoverOutbox(ctx context.Context) ([]AutomationOutboxEntry, error) {
	return s.RecoverDispatchingOutbox(ctx)
}

func (s *AutomationStore) GetOutboxByID(
	ctx context.Context,
	outboxID string,
) (AutomationOutboxEntry, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, err
	}
	if err := validateStoredAutomationString("outbox_id", outboxID, 80, true, false); err != nil {
		return AutomationOutboxEntry{}, err
	}
	state := s.Snapshot()
	entry := automationOutboxFindByID(&state, outboxID)
	if entry == nil {
		return AutomationOutboxEntry{}, &AutomationOutboxNotFoundError{OutboxID: outboxID}
	}
	return cloneAutomationOutboxEntry(*entry), nil
}

func (s *AutomationStore) GetOutboxByActionID(
	ctx context.Context,
	actionID string,
) (AutomationOutboxEntry, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return AutomationOutboxEntry{}, err
	}
	if err := validateStoredAutomationString("action_id", actionID, 80, true, false); err != nil {
		return AutomationOutboxEntry{}, err
	}
	state := s.Snapshot()
	entry := automationOutboxFindByActionID(&state, actionID)
	if entry == nil {
		return AutomationOutboxEntry{}, &AutomationOutboxNotFoundError{ActionID: actionID}
	}
	return cloneAutomationOutboxEntry(*entry), nil
}

func (s *AutomationStore) ListPending(
	ctx context.Context,
) ([]AutomationOutboxEntry, error) {
	if err := automationOutboxValidateContext(ctx); err != nil {
		return nil, err
	}
	state := s.Snapshot()
	pending := make([]AutomationOutboxEntry, 0)
	for _, entry := range state.Outbox {
		if entry.Status == AutomationOutboxPending {
			pending = append(pending, cloneAutomationOutboxEntry(entry))
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].CreatedAt.Equal(pending[j].CreatedAt) {
			return pending[i].ID < pending[j].ID
		}
		return pending[i].CreatedAt.Before(pending[j].CreatedAt)
	})
	return pending, nil
}

func (s *AutomationStore) ListPendingOutbox(ctx context.Context) ([]AutomationOutboxEntry, error) {
	return s.ListPending(ctx)
}

func automationOutboxAppendAudit(
	state *AutomationState,
	allocator *AutomationIDAllocator,
	entry AutomationOutboxEntry,
	previous AutomationOutboxStatus,
	eventType string,
	recovery bool,
	now time.Time,
) error {
	id, err := allocator.Next(AutomationIDKindAudit)
	if err != nil {
		return err
	}
	data, err := json.Marshal(automationOutboxAuditData{
		PreviousStatus:    previous,
		Status:            entry.Status,
		Attempt:           entry.Attempt,
		DispatchAttempted: entry.DispatchAttempted,
		ErrorCode:         entry.ErrorCode,
		Recovery:          recovery,
	})
	if err != nil {
		return err
	}
	runID := ""
	if action := automationOutboxFindAction(state, entry.ActionID); action != nil {
		runID = action.RunID
	}
	state.PendingAudit = append(state.PendingAudit, AutomationAuditRecord{
		ID:        id,
		EventType: eventType,
		RunID:     runID,
		ActionID:  entry.ActionID,
		OutboxID:  entry.ID,
		ActorType: "system",
		Data:      data,
		CreatedAt: now,
	})
	return nil
}

func automationOutboxValidateContext(ctx context.Context) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	return ctx.Err()
}

func automationOutboxValidateDelivery(delivery AutomationDeliveryResult) error {
	switch delivery.Status {
	case AutomationDeliverySent, AutomationDeliveryFailed, AutomationDeliveryUnknown:
	default:
		return NewAutomationValidationError("delivery.status", "is invalid")
	}
	if err := validateStoredAutomationString("delivery.error_code", delivery.ErrorCode, 120, false, false); err != nil {
		return err
	}
	return validateStoredAutomationString("delivery.error_message", delivery.ErrorMessage, 4000, false, true)
}

func automationOutboxFindAction(state *AutomationState, actionID string) *AutomationAction {
	for i := range state.Actions {
		if state.Actions[i].ID == actionID {
			return &state.Actions[i]
		}
	}
	return nil
}

func automationOutboxFindByID(state *AutomationState, outboxID string) *AutomationOutboxEntry {
	for i := range state.Outbox {
		if state.Outbox[i].ID == outboxID {
			return &state.Outbox[i]
		}
	}
	return nil
}

func automationOutboxFindByActionID(state *AutomationState, actionID string) *AutomationOutboxEntry {
	for i := range state.Outbox {
		if state.Outbox[i].ActionID == actionID {
			return &state.Outbox[i]
		}
	}
	return nil
}

func automationOutboxConflict(entry AutomationOutboxEntry, message string) error {
	return &AutomationOutboxConflictError{
		OutboxID: entry.ID,
		ActionID: entry.ActionID,
		Status:   entry.Status,
		Message:  message,
	}
}

func automationOutboxTerminalAuditEvent(status AutomationOutboxStatus) string {
	switch status {
	case AutomationOutboxSent:
		return AutomationAuditEventOutboxSent
	case AutomationOutboxFailed:
		return AutomationAuditEventOutboxFailed
	default:
		return AutomationAuditEventOutboxUnknown
	}
}

func cloneAutomationOutboxEntry(entry AutomationOutboxEntry) AutomationOutboxEntry {
	entry.DispatchStartedAt = cloneAutomationTimePointer(entry.DispatchStartedAt)
	entry.CompletedAt = cloneAutomationTimePointer(entry.CompletedAt)
	return entry
}

func automationOutboxTimePointer(value time.Time) *time.Time {
	return &value
}
