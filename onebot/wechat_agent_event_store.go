package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	WechatAgentEventStoreVersion     = 1
	DefaultWechatAgentEventRetention = 10_000
)

var ErrWechatAgentEventStoreClosed = errors.New("wechat agent event store is closed")

type wechatAgentEventStoreState struct {
	Version        int                 `json:"version"`
	NextCursor     string              `json:"next_cursor"`
	Events         []AgentInboundEvent `json:"events"`
	MessageCursors map[string]string   `json:"message_cursors"`
}

type wechatAgentEventSubscriber struct {
	events chan AgentInboundEvent
	errs   chan error
	done   chan struct{}
	once   sync.Once
}

func (s *wechatAgentEventSubscriber) close(terminal error) {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if terminal != nil {
			select {
			case s.errs <- terminal:
			default:
			}
		}
		close(s.done)
		close(s.events)
		close(s.errs)
	})
}

type WechatAgentEventSubscription struct {
	ID     string
	Cursor string
	Replay []AgentInboundEvent
	Events <-chan AgentInboundEvent
	Errors <-chan error

	once   sync.Once
	cancel func()
}

func (s *WechatAgentEventSubscription) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
}

type WechatAgentEventStore struct {
	path      string
	retention int

	mu               sync.Mutex
	nextCursor       string
	events           []AgentInboundEvent
	messageCursors   map[string]string
	subscribers      map[uint64]*wechatAgentEventSubscriber
	nextSubscriberID uint64
	closed           bool
}

func NewWechatAgentEventStore(retention int) *WechatAgentEventStore {
	if retention <= 0 {
		retention = DefaultWechatAgentEventRetention
	}
	return &WechatAgentEventStore{
		retention:      retention,
		nextCursor:     "1",
		events:         []AgentInboundEvent{},
		messageCursors: make(map[string]string),
		subscribers:    make(map[uint64]*wechatAgentEventSubscriber),
	}
}

func OpenWechatAgentEventStore(path string, retention int) (*WechatAgentEventStore, error) {
	if filepath.Clean(path) != path || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("wechat agent event store path is unsafe")
	}
	store := NewWechatAgentEventStore(retention)
	store.path = path

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeWechatAgentJSONAtomic(path, store.snapshotLocked()); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read wechat agent event store: %w", err)
	}

	var state wechatAgentEventStoreState
	if err := decodeWechatAgentJSON(data, &state); err != nil {
		return nil, fmt.Errorf("decode wechat agent event store: %w", err)
	}
	if err := validateWechatAgentEventStoreState(state, store.retention); err != nil {
		return nil, err
	}
	store.nextCursor = state.NextCursor
	store.events = cloneAgentInboundEvents(state.Events)
	store.messageCursors = cloneStringMap(state.MessageCursors)
	return store, nil
}

func (s *WechatAgentEventStore) Append(event AgentInboundEvent) (AgentInboundEvent, bool, error) {
	if s == nil {
		return AgentInboundEvent{}, false, ErrWechatAgentEventStoreClosed
	}
	event.Cursor = ""
	if err := validateStoredAgentInboundEvent(event); err != nil {
		return AgentInboundEvent{}, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return AgentInboundEvent{}, false, ErrWechatAgentEventStoreClosed
	}
	if cursor, exists := s.messageCursors[event.MessageKey]; exists {
		for i := range s.events {
			if s.events[i].Cursor == cursor {
				return cloneAgentInboundEvent(s.events[i]), false, nil
			}
		}
		event.Cursor = cursor
		return cloneAgentInboundEvent(event), false, nil
	}

	event.Cursor = s.nextCursor
	nextCursor := incrementAgentDecimal(s.nextCursor)
	events := append(cloneAgentInboundEvents(s.events), cloneAgentInboundEvent(event))
	if len(events) > s.retention {
		events = events[len(events)-s.retention:]
	}
	messageCursors := cloneStringMap(s.messageCursors)
	messageCursors[event.MessageKey] = event.Cursor
	state := wechatAgentEventStoreState{
		Version:        WechatAgentEventStoreVersion,
		NextCursor:     nextCursor,
		Events:         events,
		MessageCursors: messageCursors,
	}
	if s.path != "" {
		if err := writeWechatAgentJSONAtomic(s.path, state); err != nil {
			return AgentInboundEvent{}, false, err
		}
	}

	s.nextCursor = nextCursor
	s.events = events
	s.messageCursors = messageCursors
	s.publishLocked(event)
	return cloneAgentInboundEvent(event), true, nil
}

func (s *WechatAgentEventStore) ReplayAfter(afterCursor string) ([]AgentInboundEvent, error) {
	if s == nil {
		return nil, ErrWechatAgentEventStoreClosed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrWechatAgentEventStoreClosed
	}
	return s.replayAfterLocked(afterCursor)
}

func (s *WechatAgentEventStore) Subscribe(
	ctx context.Context,
	afterCursor string,
) (*WechatAgentEventSubscription, error) {
	if ctx == nil {
		return nil, &AgentError{Code: AgentErrorCodeValidation, Message: "context is required"}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrWechatAgentEventStoreClosed
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrWechatAgentEventStoreClosed
	}
	replay, err := s.replayAfterLocked(afterCursor)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.nextSubscriberID++
	numericID := s.nextSubscriberID
	id := fmt.Sprintf("sub-%d", numericID)
	bufferSize := s.retention + 16
	if bufferSize < 32 {
		bufferSize = 32
	}
	subscriber := &wechatAgentEventSubscriber{
		events: make(chan AgentInboundEvent, bufferSize),
		errs:   make(chan error, 1),
		done:   make(chan struct{}),
	}
	s.subscribers[numericID] = subscriber
	s.mu.Unlock()

	cursor := afterCursor
	if cursor == "" {
		cursor = "0"
	}
	subscription := &WechatAgentEventSubscription{
		ID:     id,
		Cursor: cursor,
		Replay: replay,
		Events: subscriber.events,
		Errors: subscriber.errs,
		cancel: func() {
			s.removeSubscriber(numericID, nil)
		},
	}
	go func() {
		select {
		case <-ctx.Done():
			subscription.Close()
		case <-subscriber.done:
		}
	}()
	return subscription, nil
}

func (s *WechatAgentEventStore) LatestCursor() string {
	if s == nil {
		return "0"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return previousAgentDecimal(s.nextCursor)
}

func (s *WechatAgentEventStore) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	subscribers := s.subscribers
	s.subscribers = make(map[uint64]*wechatAgentEventSubscriber)
	s.mu.Unlock()

	for _, subscriber := range subscribers {
		subscriber.close(nil)
	}
	return nil
}

func (s *WechatAgentEventStore) replayAfterLocked(afterCursor string) ([]AgentInboundEvent, error) {
	if afterCursor == "" {
		afterCursor = "0"
	}
	if err := ValidateAgentCursor(afterCursor, false); err != nil {
		return nil, err
	}
	latest := previousAgentDecimal(s.nextCursor)
	if CompareAgentCursors(afterCursor, latest) > 0 {
		return nil, &AgentError{Code: AgentErrorCodeValidation, Message: "cursor is ahead of retained history"}
	}
	if len(s.events) > 0 {
		oldestPredecessor := previousAgentDecimal(s.events[0].Cursor)
		if CompareAgentCursors(afterCursor, oldestPredecessor) < 0 {
			return nil, &AgentError{
				Code:      AgentErrorCodeCursorExpired,
				Message:   "cursor is older than retained history",
				Retryable: false,
			}
		}
	}

	start := len(s.events)
	for i := range s.events {
		if CompareAgentCursors(s.events[i].Cursor, afterCursor) > 0 {
			start = i
			break
		}
	}
	return cloneAgentInboundEvents(s.events[start:]), nil
}

func (s *WechatAgentEventStore) publishLocked(event AgentInboundEvent) {
	for id, subscriber := range s.subscribers {
		select {
		case subscriber.events <- cloneAgentInboundEvent(event):
		default:
			delete(s.subscribers, id)
			subscriber.close(&AgentError{
				Code:      AgentErrorCodeUnavailable,
				Message:   "event subscriber could not keep up",
				Retryable: true,
			})
		}
	}
}

func (s *WechatAgentEventStore) removeSubscriber(id uint64, terminal error) {
	s.mu.Lock()
	subscriber, exists := s.subscribers[id]
	if exists {
		delete(s.subscribers, id)
	}
	s.mu.Unlock()
	if !exists {
		return
	}
	subscriber.close(terminal)
}

func (s *WechatAgentEventStore) snapshotLocked() wechatAgentEventStoreState {
	return wechatAgentEventStoreState{
		Version:        WechatAgentEventStoreVersion,
		NextCursor:     s.nextCursor,
		Events:         cloneAgentInboundEvents(s.events),
		MessageCursors: cloneStringMap(s.messageCursors),
	}
}

func validateWechatAgentEventStoreState(state wechatAgentEventStoreState, retention int) error {
	if state.Version != WechatAgentEventStoreVersion {
		return fmt.Errorf("wechat agent event store version is unsupported")
	}
	if err := ValidateAgentCursor(state.NextCursor, false); err != nil || state.NextCursor == "0" {
		return fmt.Errorf("wechat agent event store next cursor is invalid")
	}
	if state.Events == nil || state.MessageCursors == nil || len(state.Events) > retention {
		return fmt.Errorf("wechat agent event store state is invalid")
	}
	previous := ""
	retainedKeys := make(map[string]struct{}, len(state.Events))
	for i := range state.Events {
		event := state.Events[i]
		if err := ValidateAgentCursor(event.Cursor, false); err != nil ||
			CompareAgentCursors(event.Cursor, state.NextCursor) >= 0 ||
			(previous != "" && incrementAgentDecimal(previous) != event.Cursor) {
			return fmt.Errorf("wechat agent event cursor sequence is invalid")
		}
		if err := validateStoredAgentInboundEvent(event); err != nil {
			return fmt.Errorf("wechat agent event %d is invalid: %w", i, err)
		}
		if _, exists := retainedKeys[event.MessageKey]; exists {
			return fmt.Errorf("wechat agent retained message key is duplicated")
		}
		if state.MessageCursors[event.MessageKey] != event.Cursor {
			return fmt.Errorf("wechat agent message cursor index is inconsistent")
		}
		retainedKeys[event.MessageKey] = struct{}{}
		previous = event.Cursor
	}
	for messageKey, cursor := range state.MessageCursors {
		if messageKey == "" || ValidateAgentCursor(cursor, false) != nil ||
			CompareAgentCursors(cursor, state.NextCursor) >= 0 {
			return fmt.Errorf("wechat agent message cursor index is invalid")
		}
	}
	if len(state.Events) == 0 {
		if state.NextCursor != "1" {
			return fmt.Errorf("wechat agent event cursor sequence is invalid")
		}
	} else if incrementAgentDecimal(state.Events[len(state.Events)-1].Cursor) != state.NextCursor {
		return fmt.Errorf("wechat agent event cursor sequence is invalid")
	}
	return nil
}

func validateStoredAgentInboundEvent(event AgentInboundEvent) error {
	if event.MessageKey == "" || len(event.MessageKey) > 1024 {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "event message key is invalid"}
	}
	if event.Source == "" || len(event.Source) > 64 || !IsValidChatType(event.ChatType) {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "event source or chat type is invalid"}
	}
	if event.PeerID == "" || len(event.PeerID) > 256 ||
		event.AuthorID == "" || len(event.AuthorID) > 256 {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "event peer is invalid"}
	}
	if event.ChatType == ChatTypePrivate && event.GroupID != "" {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "private event group is invalid"}
	}
	if event.ChatType == ChatTypeGroup && event.GroupID != event.PeerID {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "group event peer is invalid"}
	}
	if len(event.Kinds) > 32 {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "event kinds are invalid"}
	}
	seenKinds := make(map[string]struct{}, len(event.Kinds))
	for _, kind := range event.Kinds {
		if kind == "" || len(kind) > 32 || strings.TrimSpace(kind) != kind {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "event kinds are invalid"}
		}
		if _, exists := seenKinds[kind]; exists {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "event kinds are invalid"}
		}
		seenKinds[kind] = struct{}{}
	}
	for _, attachment := range event.Attachments {
		if err := validateWechatAgentPublicAttachment(attachment); err != nil {
			return err
		}
	}
	return nil
}

func incrementAgentDecimal(value string) string {
	if value == "" {
		return "1"
	}
	result := []byte(value)
	for i := len(result) - 1; i >= 0; i-- {
		if result[i] < '9' {
			result[i]++
			return string(result)
		}
		result[i] = '0'
	}
	return "1" + string(result)
}

func previousAgentDecimal(value string) string {
	if value == "" || value == "0" || value == "1" {
		return "0"
	}
	result := []byte(value)
	for i := len(result) - 1; i >= 0; i-- {
		if result[i] > '0' {
			result[i]--
			break
		}
		result[i] = '9'
	}
	if len(result) > 1 && result[0] == '0' {
		result = result[1:]
	}
	return string(result)
}

func cloneAgentInboundEvent(event AgentInboundEvent) AgentInboundEvent {
	cloned := event
	cloned.Kinds = append([]string{}, event.Kinds...)
	cloned.Attachments = append([]AgentAttachment{}, event.Attachments...)
	return cloned
}

func cloneAgentInboundEvents(events []AgentInboundEvent) []AgentInboundEvent {
	cloned := make([]AgentInboundEvent, len(events))
	for i := range events {
		cloned[i] = cloneAgentInboundEvent(events[i])
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func decodeWechatAgentJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func writeWechatAgentJSONAtomic(path string, value any) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return fmt.Errorf("create wechat agent data directory: %w", err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		return fmt.Errorf("protect wechat agent data directory: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode wechat agent state: %w", err)
	}
	data = append(data, '\n')

	temp, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create wechat agent state: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("protect wechat agent state: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write wechat agent state: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync wechat agent state: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close wechat agent state: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace wechat agent state: %w", err)
	}
	directory, err := os.Open(parent)
	if err != nil {
		return fmt.Errorf("open wechat agent data directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync wechat agent data directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close wechat agent data directory: %w", err)
	}
	return nil
}
