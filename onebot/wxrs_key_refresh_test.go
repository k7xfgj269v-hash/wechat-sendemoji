//go:build !noFrida

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestWechatUseKeyRefreshArgumentsKeepSourceVersionDispatch(t *testing.T) {
	got := wechatUseKeyRefreshArguments(6095)
	want := []string{
		"init",
		"--force",
		"--attach-only",
		"--pid", "6095",
		"--json",
		"--skip-smoke",
		"--timeout", "300",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
	for _, argument := range got {
		if argument == "--scan" || argument == "--literal-scan" {
			t.Fatalf("arguments force source fallback path: %#v", got)
		}
	}
}

func TestValidateWechatUseExecutableRequiresSourceVersion(t *testing.T) {
	writeVersionBinary := func(name, version string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		content := "#!/bin/sh\nprintf '%s\\n' '" + version + "'\n"
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
		return path
	}

	if err := validateWechatUseExecutable(
		writeVersionBinary("current", wechatUseExpectedVersion),
	); err != nil {
		t.Fatalf("current version rejected: %v", err)
	}
	if err := validateWechatUseExecutable(
		writeVersionBinary("old", "wechat-use 1.16.7"),
	); err == nil {
		t.Fatal("old wechat-use version accepted")
	}
}

func TestShouldRefreshWXRSKeysOnlyForLocalWechat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request FridaInitializeRequest
		want    bool
	}{
		{
			name:    "local",
			request: FridaInitializeRequest{Type: "local"},
			want:    true,
		},
		{
			name: "loopback gadget",
			request: FridaInitializeRequest{
				Type:       "gadget",
				GadgetAddr: "127.0.0.1:27042",
			},
			want: true,
		},
		{
			name: "ipv6 loopback gadget",
			request: FridaInitializeRequest{
				Type:       "gadget",
				GadgetAddr: "[::1]:27042",
			},
			want: true,
		},
		{
			name: "remote gadget",
			request: FridaInitializeRequest{
				Type:       "gadget",
				GadgetAddr: "10.0.0.5:27042",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshWXRSKeysForAttachment(tc.request); got != tc.want {
				t.Fatalf("should refresh = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestFridaControllerRefreshesKeysThenContacts(t *testing.T) {
	controller := NewFridaController(nil, nil)
	controller.statusMu.Lock()
	controller.sessionGeneration = 7
	controller.status = FridaStatus{
		State:        FridaStateAttached,
		WechatPID:    6095,
		Attached:     true,
		ScriptLoaded: true,
	}
	controller.statusMu.Unlock()

	keyDone := make(chan struct{})
	contactsDone := make(chan struct{})
	controller.keyRefresher = func(_ context.Context, pid int) error {
		if pid != 6095 {
			t.Errorf("key refresh PID = %d, want 6095", pid)
		}
		close(keyDone)
		return nil
	}
	controller.contacts = func(_ context.Context, shouldPublish func() bool) error {
		select {
		case <-keyDone:
		default:
			t.Error("contacts refreshed before keys")
		}
		if !shouldPublish() {
			t.Error("current Frida generation was rejected")
		}
		close(contactsDone)
		return nil
	}

	controller.scheduleDBKeyRefresh(7, 6095)
	select {
	case <-contactsDone:
	case <-time.After(time.Second):
		t.Fatal("key/contact refresh did not complete")
	}
}

func TestRefreshContactsAfterDBKeyRetriesConcurrentRefresh(t *testing.T) {
	var calls atomic.Int32
	err := refreshContactsAfterDBKey(
		context.Background(),
		func(context.Context) error {
			if calls.Add(1) == 1 {
				return errContactCacheRefreshInProgress
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("contact refresh calls = %d, want 2", got)
	}
}

func TestFridaControllerCancelsSupersededKeyRefresh(t *testing.T) {
	controller := NewFridaController(nil, nil)
	controller.statusMu.Lock()
	controller.sessionGeneration = 2
	controller.status = FridaStatus{
		State:        FridaStateAttached,
		WechatPID:    222,
		Attached:     true,
		ScriptLoaded: true,
	}
	controller.statusMu.Unlock()

	firstStarted := make(chan struct{})
	firstCancelObserved := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstExited := make(chan struct{})
	secondStarted := make(chan struct{})
	secondDone := make(chan struct{})
	var calls atomic.Int32
	controller.keyRefresher = func(ctx context.Context, pid int) error {
		switch calls.Add(1) {
		case 1:
			close(firstStarted)
			<-ctx.Done()
			close(firstCancelObserved)
			<-releaseFirst
			close(firstExited)
			return ctx.Err()
		case 2:
			if pid != 222 {
				t.Errorf("second key refresh PID = %d, want 222", pid)
			}
			close(secondStarted)
			close(secondDone)
			return nil
		default:
			return errors.New("unexpected key refresh")
		}
	}
	var contactCalls atomic.Int32
	controller.contacts = func(context.Context, func() bool) error {
		contactCalls.Add(1)
		return nil
	}

	controller.scheduleDBKeyRefresh(1, 111)
	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first key refresh did not start")
	}
	controller.scheduleDBKeyRefresh(2, 222)
	select {
	case <-firstCancelObserved:
	case <-time.After(time.Second):
		t.Fatal("superseded key refresh was not canceled")
	}
	select {
	case <-secondStarted:
		t.Fatal("replacement key refresh started before the old process exited")
	case <-time.After(25 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case <-firstExited:
	case <-time.After(time.Second):
		t.Fatal("superseded key refresh did not exit")
	}
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("replacement key refresh did not complete")
	}

	deadline := time.Now().Add(time.Second)
	for contactCalls.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("contact refresh calls = %d, want 1", contactCalls.Load())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFridaControllerCancelWaitsForKeyRefreshExit(t *testing.T) {
	controller := NewFridaController(nil, nil)
	controller.statusMu.Lock()
	controller.sessionGeneration = 4
	controller.status = FridaStatus{
		State:        FridaStateAttached,
		WechatPID:    444,
		Attached:     true,
		ScriptLoaded: true,
	}
	controller.statusMu.Unlock()

	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	release := make(chan struct{})
	exited := make(chan struct{})
	controller.keyRefresher = func(ctx context.Context, _ int) error {
		close(started)
		<-ctx.Done()
		close(cancelObserved)
		<-release
		close(exited)
		return ctx.Err()
	}

	controller.scheduleDBKeyRefresh(4, 444)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("key refresh did not start")
	}

	cancelDone := make(chan error, 1)
	go func() {
		cancelDone <- controller.cancelDBKeyRefresh(context.Background())
	}()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("key refresh did not observe cancellation")
	}
	select {
	case err := <-cancelDone:
		t.Fatalf("cancel returned before key refresh exited: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("key refresh did not exit")
	}
	select {
	case err := <-cancelDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel did not return after key refresh exited")
	}
}

func TestFridaControllerKeyRefreshFailureDoesNotChangeAttachedState(t *testing.T) {
	controller := NewFridaController(nil, nil)
	controller.statusMu.Lock()
	controller.sessionGeneration = 3
	controller.status = FridaStatus{
		State:         FridaStateAttached,
		WechatPID:     333,
		Attached:      true,
		ScriptLoaded:  true,
		AutoReconnect: true,
	}
	controller.statusMu.Unlock()

	failed := make(chan struct{})
	controller.keyRefresher = func(context.Context, int) error {
		close(failed)
		return errors.New("capture failed")
	}
	var contactCalls atomic.Int32
	controller.contacts = func(context.Context, func() bool) error {
		contactCalls.Add(1)
		return nil
	}

	controller.scheduleDBKeyRefresh(3, 333)
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("key refresh did not run")
	}
	deadline := time.Now().Add(time.Second)
	for {
		controller.keyRefreshMu.Lock()
		active := controller.keyRefreshCancel != nil
		controller.keyRefreshMu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed key refresh did not finish")
		}
		time.Sleep(time.Millisecond)
	}

	status := controller.Status()
	if status.State != FridaStateAttached || !status.Attached ||
		!status.ScriptLoaded || !status.AutoReconnect {
		t.Fatalf("key refresh failure changed Frida status: %#v", status)
	}
	if contactCalls.Load() != 0 {
		t.Fatalf("contact refresh calls = %d, want 0", contactCalls.Load())
	}
}
