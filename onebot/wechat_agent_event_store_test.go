package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestWechatAgentEventStorePersistsMonotonicCursorsAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	store, err := OpenWechatAgentEventStore(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 3; index++ {
		event, created, err := store.Append(syntheticInboundEvent(index))
		if err != nil {
			t.Fatal(err)
		}
		if !created || event.Cursor != strconv.Itoa(index) {
			t.Fatalf("append %d = %#v, created = %v", index, event, created)
		}
	}
	duplicate, created, err := store.Append(syntheticInboundEvent(1))
	if err != nil {
		t.Fatal(err)
	}
	if created || duplicate.Cursor != "1" || store.LatestCursor() != "3" {
		t.Fatalf("duplicate = %#v, created = %v, latest = %q", duplicate, created, store.LatestCursor())
	}
	assertAgentErrorCode(t, replayError(store, "0"), AgentErrorCodeCursorExpired)
	replay, err := store.ReplayAfter("1")
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 2 || replay[0].Cursor != "2" || replay[1].Cursor != "3" {
		t.Fatalf("replay = %#v", replay)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenWechatAgentEventStore(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	event, created, err := reopened.Append(syntheticInboundEvent(4))
	if err != nil || !created || event.Cursor != "4" {
		t.Fatalf("reopened append = %#v, created = %v, err = %v", event, created, err)
	}
}

func TestWechatAgentEventStoreSubscriptionReplaysThenStreams(t *testing.T) {
	store := NewWechatAgentEventStore(8)
	t.Cleanup(func() { _ = store.Close() })
	if _, _, err := store.Append(syntheticInboundEvent(1)); err != nil {
		t.Fatal(err)
	}
	subscription, err := store.Subscribe(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	if len(subscription.Replay) != 1 || subscription.Replay[0].Cursor != "1" {
		t.Fatalf("subscription replay = %#v", subscription.Replay)
	}
	if _, _, err := store.Append(syntheticInboundEvent(2)); err != nil {
		t.Fatal(err)
	}
	event := <-subscription.Events
	if event.Cursor != "2" || event.MessageKey != "message-2" {
		t.Fatalf("streamed event = %#v", event)
	}
}

func TestWechatAgentEventStoreOverflowAndConcurrentCloseAreSafe(t *testing.T) {
	store := NewWechatAgentEventStore(1)
	subscription, err := store.Subscribe(context.Background(), "0")
	if err != nil {
		t.Fatal(err)
	}
	for index := 1; index <= 40; index++ {
		if _, _, err := store.Append(syntheticInboundEvent(index)); err != nil {
			t.Fatal(err)
		}
	}
	err = <-subscription.Errors
	assertAgentErrorCode(t, err, AgentErrorCodeUnavailable)
	for range subscription.Events {
	}

	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		subscription.Close()
	}()
	go func() {
		defer wait.Done()
		_ = store.Close()
	}()
	go func() {
		defer wait.Done()
		subscription.Close()
	}()
	wait.Wait()
	if _, _, err := store.Append(syntheticInboundEvent(41)); !errors.Is(err, ErrWechatAgentEventStoreClosed) {
		t.Fatalf("Append() after close error = %v", err)
	}
}

func syntheticInboundEvent(index int) AgentInboundEvent {
	return AgentInboundEvent{
		MessageKey: "message-" + strconv.Itoa(index),
		Source:     "wechat",
		Mid:        "mid-" + strconv.Itoa(index),
		ChatType:   ChatTypePrivate,
		PeerID:     "wxid-alice",
		AuthorID:   "wxid-alice",
		Text:       "hello",
		Kinds:      []string{"text"},
		Timestamp:  int64(index),
	}
}

func replayError(store *WechatAgentEventStore, cursor string) error {
	_, err := store.ReplayAfter(cursor)
	return err
}
