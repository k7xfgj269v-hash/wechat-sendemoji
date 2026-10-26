package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	crmMessageSource                      = "wechat"
	HRWorkbenchAgentCursorStateVersion    = 1
	defaultHRWorkbenchAgentCursorFileName = "hr-workbench-agent-cursor.json"
	defaultHRWorkbenchAgentReconnectDelay = time.Second
	maxHRWorkbenchAgentReconnectDelay     = 30 * time.Second
)

var (
	crmMessageStoreMu sync.RWMutex
	crmMessageStore   *CRMStore
)

type crmMessageListFilter struct {
	Query        string
	ReviewStatus ReviewStatus
	CandidateID  string
	Limit        int
	Offset       int
}

func crmMessageSetStore(store *CRMStore) {
	crmMessageStoreMu.Lock()
	crmMessageStore = store
	crmMessageStoreMu.Unlock()
}

func crmMessageCurrentStore() *CRMStore {
	crmMessageStoreMu.RLock()
	store := crmMessageStore
	crmMessageStoreMu.RUnlock()
	return store
}

func crmMessageList(
	store *CRMStore,
	filter crmMessageListFilter,
) (int, []crmMessageEventProjection, error) {
	if store == nil {
		return 0, nil, NewCRMValidationError("store", "is required")
	}
	query, err := NormalizeCRMSearch(filter.Query)
	if err != nil {
		return 0, nil, err
	}
	if filter.ReviewStatus != "" && !IsValidReviewStatus(filter.ReviewStatus) {
		return 0, nil, NewCRMValidationError("review_status", "is invalid")
	}
	candidateID, err := normalizeCRMString(
		"candidate_id",
		filter.CandidateID,
		80,
		false,
		false,
	)
	if err != nil {
		return 0, nil, err
	}
	if filter.Limit < 1 || filter.Limit > WorkbenchMaxPageLimit {
		return 0, nil, NewCRMValidationError("limit", "is invalid")
	}
	if filter.Offset < 0 {
		return 0, nil, NewCRMValidationError("offset", "is invalid")
	}

	state := store.Snapshot()
	query = strings.ToLower(query)
	events := make([]MessageEvent, 0, len(state.MessageEvents))
	for _, event := range state.MessageEvents {
		if filter.ReviewStatus != "" && event.ReviewStatus != filter.ReviewStatus {
			continue
		}
		if candidateID != "" && event.CandidateID != candidateID {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(event.Text), query) {
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].Timestamp != events[j].Timestamp {
			return events[i].Timestamp > events[j].Timestamp
		}
		if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].CreatedAt.After(events[j].CreatedAt)
		}
		return events[i].ID > events[j].ID
	})

	total := len(events)
	start := filter.Offset
	if start > total {
		start = total
	}
	end := start + filter.Limit
	if end > total {
		end = total
	}
	return total, crmMessageProjectEvents(state, events[start:end]), nil
}

func crmMessageGet(store *CRMStore, id string) (crmMessageEventProjection, error) {
	if store == nil {
		return crmMessageEventProjection{}, NewCRMValidationError("store", "is required")
	}
	normalizedID, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return crmMessageEventProjection{}, err
	}
	state := store.Snapshot()
	event, found := crmMessageFindEvent(state, normalizedID)
	if !found {
		return crmMessageEventProjection{}, crmMessageErrNotFound
	}
	return crmMessageProjectEvent(state, event), nil
}

func captureNormalizedInbound(rec *ForwardRecord) error {
	store := crmMessageCurrentStore()
	if store == nil {
		return nil
	}
	_, _, err := crmMessageCaptureNormalizedInboundToStore(store, rec, time.Now().UTC())
	return err
}

func crmMessageCaptureNormalizedInboundToStore(store *CRMStore, rec *ForwardRecord, now time.Time) (MessageEvent, bool, error) {
	if store == nil {
		return MessageEvent{}, false, NewCRMValidationError("store", "is required")
	}
	event, err := crmMessageEventFromRecord(rec, now)
	if err != nil {
		return MessageEvent{}, false, err
	}
	captured, created, err := crmMessagePersistEventToStore(store, event)
	if err != nil {
		return MessageEvent{}, false, err
	}
	if created {
		automationEnqueueEvent(captured)
	}
	return captured, created, nil
}

func crmMessagePersistEventToStore(
	store *CRMStore,
	event MessageEvent,
) (MessageEvent, bool, error) {
	var captured MessageEvent
	created := false
	err := store.Update(func(state *CRMState, allocator *IDAllocator) error {
		for i := range state.MessageEvents {
			if state.MessageEvents[i].MessageKey == event.MessageKey {
				captured = state.MessageEvents[i]
				return nil
			}
		}

		id, err := allocator.Next(IDKindEvent)
		if err != nil {
			return err
		}
		event.ID = id
		state.MessageEvents = append(state.MessageEvents, event)
		captured = event
		created = true
		return nil
	})
	if err != nil {
		return MessageEvent{}, false, err
	}
	return captured, created, nil
}

func crmMessageCaptureAgentInboundToStore(
	store *CRMStore,
	inbound AgentInboundEvent,
	now time.Time,
) (MessageEvent, bool, error) {
	if err := validateStoredAgentInboundEvent(inbound); err != nil {
		return MessageEvent{}, false, err
	}
	record := &ForwardRecord{
		T:           inbound.Timestamp,
		Chat:        string(inbound.ChatType),
		Dir:         "in",
		Peer:        inbound.PeerID,
		Author:      inbound.AuthorID,
		GroupID:     inbound.GroupID,
		Text:        inbound.Text,
		Suspicious:  inbound.Suspicious,
		Kinds:       append([]string{}, inbound.Kinds...),
		Mid:         inbound.Mid,
		Attachments: append([]AgentAttachment{}, inbound.Attachments...),
	}
	event, err := crmMessageEventFromRecord(record, now)
	if err != nil {
		return MessageEvent{}, false, err
	}
	if inbound.MessageKey != event.MessageKey {
		return MessageEvent{}, false, NewCRMValidationError(
			"message_key",
			"does not match canonical CRM message key",
		)
	}
	event.MessageKey = inbound.MessageKey
	return crmMessagePersistEventToStore(store, event)
}

func crmMessageEventFromRecord(rec *ForwardRecord, now time.Time) (MessageEvent, error) {
	if rec == nil {
		return MessageEvent{}, NewCRMValidationError("message", "is required")
	}
	if rec.Dir != "in" {
		return MessageEvent{}, NewCRMValidationError("dir", "must be inbound")
	}

	chatType := ChatType(rec.Chat)
	if !IsValidChatType(chatType) {
		return MessageEvent{}, NewCRMValidationError("chat_type", "is invalid")
	}

	peerID, err := normalizeCRMString("peer_id", rec.Peer, 256, true, false)
	if err != nil {
		return MessageEvent{}, err
	}
	authorID, err := normalizeCRMString("author_id", rec.Author, 256, true, false)
	if err != nil {
		return MessageEvent{}, err
	}
	groupID, err := normalizeCRMString("group_id", rec.GroupID, 256, chatType == ChatTypeGroup, false)
	if err != nil {
		return MessageEvent{}, err
	}
	if chatType == ChatTypePrivate && groupID != "" {
		return MessageEvent{}, NewCRMValidationError("group_id", "must be empty for private messages")
	}
	mid, err := normalizeCRMString("mid", rec.Mid, 256, false, false)
	if err != nil {
		return MessageEvent{}, err
	}
	text, err := normalizeCRMString("text", rec.Text, 4000, false, true)
	if err != nil {
		return MessageEvent{}, err
	}
	kinds, err := crmMessageNormalizeKinds(rec.Kinds)
	if err != nil {
		return MessageEvent{}, err
	}
	if now.IsZero() {
		return MessageEvent{}, NewCRMValidationError("created_at", "is required")
	}
	now = now.UTC()

	event := MessageEvent{
		Source:       crmMessageSource,
		Mid:          mid,
		ChatType:     chatType,
		PeerID:       peerID,
		AuthorID:     authorID,
		GroupID:      groupID,
		Text:         text,
		Kinds:        kinds,
		Timestamp:    rec.T,
		Suspicious:   rec.Suspicious,
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	event.MessageKey = MessageKeyForEvent(event)
	return event, nil
}

func crmMessageNormalizeKinds(kinds []string) ([]string, error) {
	result := make([]string, 0, len(kinds))
	seen := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		normalized, err := normalizeCRMString("kinds", kind, 32, true, false)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		if len(result) >= 32 {
			return nil, NewCRMValidationError("kinds", "must contain at most 32 values")
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func crmMessageCaptureNormalizedPayload(jsonData []byte) {
	message := new(WechatMessage)
	if err := json.Unmarshal(jsonData, message); err != nil {
		return
	}
	if !hasRealContent(message) {
		return
	}
	rec := normalizeMsg(message)
	if rec.Dir != "in" {
		return
	}
	if err := captureNormalizedInbound(rec); err != nil {
		Error("消息事件持久化失败")
	}
}

type HRWorkbenchAgentCursorState struct {
	Version             int       `json:"version"`
	Cursor              string    `json:"cursor"`
	Connected           bool      `json:"connected"`
	LastError           string    `json:"last_error,omitempty"`
	ReconciliationError string    `json:"reconciliation_error,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type HRWorkbenchAgentCursorStore struct {
	mu    sync.RWMutex
	path  string
	state HRWorkbenchAgentCursorState
}

func OpenHRWorkbenchAgentCursorStore(path string) (*HRWorkbenchAgentCursorStore, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("hr workbench cursor path is unsafe")
	}
	store := &HRWorkbenchAgentCursorStore{
		path: path,
		state: HRWorkbenchAgentCursorState{
			Version:   HRWorkbenchAgentCursorStateVersion,
			Cursor:    "0",
			UpdatedAt: time.Now().UTC(),
		},
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := store.persistLocked(store.state); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read hr workbench cursor: %w", err)
	}
	var state HRWorkbenchAgentCursorState
	if err := decodeWechatAgentJSON(data, &state); err != nil {
		return nil, fmt.Errorf("decode hr workbench cursor: %w", err)
	}
	if err := validateHRWorkbenchAgentCursorState(state); err != nil {
		return nil, err
	}
	store.state = state
	store.state.Connected = false
	return store, nil
}

func (s *HRWorkbenchAgentCursorStore) Snapshot() HRWorkbenchAgentCursorState {
	if s == nil {
		return HRWorkbenchAgentCursorState{
			Version: HRWorkbenchAgentCursorStateVersion,
			Cursor:  "0",
		}
	}
	s.mu.RLock()
	state := s.state
	s.mu.RUnlock()
	return state
}

func (s *HRWorkbenchAgentCursorStore) Checkpoint(cursor string) error {
	if s == nil {
		return errors.New("hr workbench cursor store is unavailable")
	}
	if err := ValidateAgentCursor(cursor, false); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if CompareAgentCursors(cursor, s.state.Cursor) < 0 {
		return errors.New("hr workbench cursor cannot move backwards")
	}
	next := s.state
	next.Cursor = cursor
	next.Connected = true
	next.LastError = ""
	next.UpdatedAt = time.Now().UTC()
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *HRWorkbenchAgentCursorStore) SetConnectionState(connected bool, err error) error {
	if s == nil {
		return errors.New("hr workbench cursor store is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lastError := ""
	if err != nil {
		lastError = publicAgentError(err, "wechat agent event stream is unavailable")
	}
	if s.state.Connected == connected && s.state.LastError == lastError {
		return nil
	}
	next := s.state
	next.Connected = connected
	next.LastError = lastError
	next.UpdatedAt = time.Now().UTC()
	if persistErr := s.persistLocked(next); persistErr != nil {
		return persistErr
	}
	s.state = next
	return nil
}

func (s *HRWorkbenchAgentCursorStore) RecordReconciliationError(err error) error {
	if s == nil {
		return errors.New("hr workbench cursor store is unavailable")
	}
	message := publicAgentError(err, "agent event cursor requires reconciliation")
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	next.Connected = false
	next.LastError = message
	next.ReconciliationError = message
	next.UpdatedAt = time.Now().UTC()
	if persistErr := s.persistLocked(next); persistErr != nil {
		return persistErr
	}
	s.state = next
	return nil
}

func (s *HRWorkbenchAgentCursorStore) persistLocked(state HRWorkbenchAgentCursorState) error {
	if err := validateHRWorkbenchAgentCursorState(state); err != nil {
		return err
	}
	if err := writeWechatAgentJSONAtomic(s.path, state); err != nil {
		return fmt.Errorf("persist hr workbench cursor: %w", err)
	}
	return nil
}

func validateHRWorkbenchAgentCursorState(state HRWorkbenchAgentCursorState) error {
	if state.Version != HRWorkbenchAgentCursorStateVersion {
		return errors.New("hr workbench cursor version is unsupported")
	}
	if err := ValidateAgentCursor(state.Cursor, false); err != nil {
		return errors.New("hr workbench cursor is invalid")
	}
	if state.UpdatedAt.IsZero() {
		return errors.New("hr workbench cursor timestamp is invalid")
	}
	return nil
}

type HRWorkbenchInboundSubscription struct {
	client     AgentClient
	crm        *CRMStore
	cursor     *HRWorkbenchAgentCursorStore
	ensureRun  func(context.Context, MessageEvent) (AutomationRun, bool, error)
	retryDelay time.Duration
	clock      func() time.Time
}

func NewHRWorkbenchInboundSubscription(
	client AgentClient,
	crm *CRMStore,
	cursor *HRWorkbenchAgentCursorStore,
) *HRWorkbenchInboundSubscription {
	return &HRWorkbenchInboundSubscription{
		client:     client,
		crm:        crm,
		cursor:     cursor,
		ensureRun:  ensureHRWorkbenchAutomationRun,
		retryDelay: defaultHRWorkbenchAgentReconnectDelay,
		clock:      time.Now,
	}
}

func ensureHRWorkbenchAutomationRun(
	ctx context.Context,
	event MessageEvent,
) (AutomationRun, bool, error) {
	automationRuntimeMu.RLock()
	runtime := automationRuntime
	automationRuntimeMu.RUnlock()
	if runtime == nil {
		return AutomationRun{}, false, nil
	}
	if runtime.service == nil || runtime.service.engine == nil {
		return AutomationRun{}, false, NewAutomationValidationError(
			"automation_runtime",
			"run engine is unavailable",
		)
	}
	return runtime.service.engine.EnsureRun(ctx, event, "")
}

func (s *HRWorkbenchInboundSubscription) State() HRWorkbenchAgentCursorState {
	if s == nil || s.cursor == nil {
		return HRWorkbenchAgentCursorState{
			Version: HRWorkbenchAgentCursorStateVersion,
			Cursor:  "0",
		}
	}
	return s.cursor.Snapshot()
}

func (s *HRWorkbenchInboundSubscription) Run(ctx context.Context) error {
	if ctx == nil || s == nil || s.client == nil || s.crm == nil || s.cursor == nil {
		return errors.New("hr workbench inbound subscription dependencies are incomplete")
	}
	if s.retryDelay <= 0 {
		s.retryDelay = defaultHRWorkbenchAgentReconnectDelay
	}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.ensureRun == nil {
		s.ensureRun = ensureHRWorkbenchAutomationRun
	}

	retryDelay := s.retryDelay
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		afterCursor := s.cursor.Snapshot().Cursor
		events, errs, err := s.client.SubscribeEvents(ctx, afterCursor)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return nil
			}
			if isAgentCursorExpired(err) {
				if recordErr := s.cursor.RecordReconciliationError(err); recordErr != nil {
					return recordErr
				}
				<-ctx.Done()
				return nil
			}
			if stateErr := s.cursor.SetConnectionState(false, err); stateErr != nil {
				return stateErr
			}
			if !shouldRetryHRWorkbenchSubscriptionError(err) {
				return err
			}
			if !waitHRWorkbenchRetry(ctx, retryDelay) {
				return nil
			}
			retryDelay = nextHRWorkbenchRetryDelay(retryDelay)
			continue
		}
		if events == nil || errs == nil {
			err = &AgentError{
				Code: AgentErrorCodeUnavailable, Message: "wechat agent event stream is unavailable", Retryable: true,
			}
			if stateErr := s.cursor.SetConnectionState(false, err); stateErr != nil {
				return stateErr
			}
			if !waitHRWorkbenchRetry(ctx, retryDelay) {
				return nil
			}
			retryDelay = nextHRWorkbenchRetryDelay(retryDelay)
			continue
		}
		if err := s.cursor.SetConnectionState(true, nil); err != nil {
			return err
		}
		retryDelay = s.retryDelay

		reconnect, err := s.consume(ctx, events, errs)
		if err != nil {
			if isAgentCursorExpired(err) {
				if recordErr := s.cursor.RecordReconciliationError(err); recordErr != nil {
					return recordErr
				}
				<-ctx.Done()
				return nil
			}
			return err
		}
		if !reconnect || !waitHRWorkbenchRetry(ctx, retryDelay) {
			return nil
		}
		retryDelay = nextHRWorkbenchRetryDelay(retryDelay)
	}
}

func (s *HRWorkbenchInboundSubscription) consume(
	ctx context.Context,
	events <-chan AgentInboundEvent,
	errs <-chan error,
) (bool, error) {
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return false, nil
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			current := s.cursor.Snapshot().Cursor
			if err := ValidateAgentCursor(event.Cursor, false); err != nil ||
				CompareAgentCursors(event.Cursor, current) <= 0 {
				return false, errors.New("agent event cursor is not monotonic")
			}
			captured, _, err := crmMessageCaptureAgentInboundToStore(
				s.crm,
				event,
				s.clock().UTC(),
			)
			if err != nil {
				return false, err
			}
			run, _, err := s.ensureRun(ctx, captured)
			if err != nil {
				return false, err
			}
			if run.Status == AutomationRunQueued {
				automationEnqueueEvent(captured)
			}
			if err := s.cursor.Checkpoint(event.Cursor); err != nil {
				return false, err
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				if errors.Is(err, context.Canceled) || ctx.Err() != nil {
					return false, nil
				}
				if isAgentCursorExpired(err) {
					return false, err
				}
				if stateErr := s.cursor.SetConnectionState(false, err); stateErr != nil {
					return false, stateErr
				}
				if !shouldRetryHRWorkbenchSubscriptionError(err) {
					return false, err
				}
				return true, nil
			}
		}
	}
	if err := s.cursor.SetConnectionState(false, nil); err != nil {
		return false, err
	}
	return true, nil
}

func isAgentCursorExpired(err error) bool {
	var agentErr *AgentError
	return errors.As(err, &agentErr) && agentErr.Code == AgentErrorCodeCursorExpired
}

func shouldRetryHRWorkbenchSubscriptionError(err error) bool {
	var agentErr *AgentError
	return !errors.As(err, &agentErr) || agentErr.Retryable
}

func waitHRWorkbenchRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func nextHRWorkbenchRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return defaultHRWorkbenchAgentReconnectDelay
	}
	if delay >= maxHRWorkbenchAgentReconnectDelay {
		return maxHRWorkbenchAgentReconnectDelay
	}
	next := delay * 2
	if next > maxHRWorkbenchAgentReconnectDelay {
		return maxHRWorkbenchAgentReconnectDelay
	}
	return next
}
