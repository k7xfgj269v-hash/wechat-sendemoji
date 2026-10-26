//go:build !noFrida

package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/frida/frida-go/frida"
)

func TestNormalizeFridaInitializeRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request FridaInitializeRequest
		want    FridaInitializeRequest
		wantErr bool
	}{
		{
			name:    "local automatic pid",
			request: FridaInitializeRequest{Type: " LOCAL ", GadgetAddr: "ignored", WechatPID: 0},
			want:    FridaInitializeRequest{Type: "local"},
		},
		{
			name:    "gadget default",
			request: FridaInitializeRequest{Type: "gadget"},
			want:    FridaInitializeRequest{Type: "gadget", GadgetAddr: "127.0.0.1:27042"},
		},
		{
			name:    "gadget address",
			request: FridaInitializeRequest{Type: "gadget", GadgetAddr: " 10.0.0.5:27042 ", WechatPID: 99},
			want:    FridaInitializeRequest{Type: "gadget", GadgetAddr: "10.0.0.5:27042"},
		},
		{
			name:    "invalid type",
			request: FridaInitializeRequest{Type: "usb"},
			wantErr: true,
		},
		{
			name:    "invalid gadget port",
			request: FridaInitializeRequest{Type: "gadget", GadgetAddr: "127.0.0.1:70000"},
			wantErr: true,
		},
		{
			name:    "negative pid",
			request: FridaInitializeRequest{Type: "local", WechatPID: -1},
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeFridaInitializeRequest(tc.request)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %#v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestFridaControllerDetachedStateAndSafeDetach(t *testing.T) {
	cfg := &Config{
		FridaType:       "gadget",
		FridaGadgetAddr: "127.0.0.1:27042",
	}
	controller := NewFridaController(cfg, nil)
	if status := controller.Status(); status.State != "detached" || status.Type != "gadget" {
		t.Fatalf("initial status = %#v", status)
	}
	if _, _, err := controller.AcquireScript(); err != ErrFridaUnavailable {
		t.Fatalf("AcquireScript error = %v", err)
	}

	status, err := controller.Detach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "detached" || status.Attached || status.AutoReconnect {
		t.Fatalf("detached status = %#v", status)
	}
}

func TestNextFridaReconnectDelay(t *testing.T) {
	delay := fridaReconnectDelay
	for i, want := range []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		30 * time.Second,
		30 * time.Second,
	} {
		if delay != want {
			t.Fatalf("delay[%d] = %s, want %s", i, delay, want)
		}
		delay = nextFridaReconnectDelay(delay)
	}
	if got := nextFridaReconnectDelay(0); got != fridaReconnectDelay {
		t.Fatalf("reset delay = %s, want %s", got, fridaReconnectDelay)
	}
}

func TestWaitFridaReconnectCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := make(chan bool, 1)
	go func() {
		result <- waitFridaReconnect(ctx, time.Second)
	}()

	select {
	case waited := <-result:
		if waited {
			t.Fatal("canceled reconnect wait reported timer completion")
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("cancellation did not interrupt reconnect wait")
	}
}

func TestIsFridaReconnectCancellation(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{
			name: "canceled context",
			ctx:  canceledCtx,
			err:  errors.New("attach failed"),
			want: true,
		},
		{
			name: "wrapped frida cancellation",
			ctx:  context.Background(),
			err:  fmt.Errorf("attach failed: %w", frida.ErrContextCancelled),
			want: true,
		},
		{
			name: "wrapped standard cancellation",
			ctx:  context.Background(),
			err:  fmt.Errorf("attach failed: %w", context.Canceled),
			want: true,
		},
		{
			name: "ordinary failure",
			ctx:  context.Background(),
			err:  errors.New("attach failed"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFridaReconnectCancellation(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("isFridaReconnectCancellation() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestFridaControllerSuppressesEquivalentLifecycleRequests(t *testing.T) {
	controller := NewFridaController(nil, nil)
	request := FridaInitializeRequest{Type: "local", WechatPID: 42}
	started := make(chan struct{})
	release := make(chan struct{})
	firstResult := make(chan FridaStatus, 1)
	secondResult := make(chan FridaStatus, 1)
	runCount := 0

	go func() {
		status, err := controller.runLifecycleOperation(
			context.Background(),
			fridaOperationInitialize,
			request,
			func(context.Context) (FridaStatus, error) {
				runCount++
				close(started)
				<-release
				return FridaStatus{
					State:        FridaStateAttached,
					WechatPID:    42,
					Attached:     true,
					ScriptLoaded: true,
				}, nil
			},
		)
		if err != nil {
			t.Errorf("first lifecycle request: %v", err)
		}
		firstResult <- status
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first lifecycle request did not start")
	}

	go func() {
		status, err := controller.runLifecycleOperation(
			context.Background(),
			fridaOperationInitialize,
			request,
			func(context.Context) (FridaStatus, error) {
				runCount++
				return FridaStatus{State: FridaStateError}, errors.New("duplicate execution")
			},
		)
		if err != nil {
			t.Errorf("equivalent lifecycle request: %v", err)
		}
		secondResult <- status
	}()

	select {
	case status := <-secondResult:
		t.Fatalf("equivalent request completed before the active request: %#v", status)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)

	var results []FridaStatus
	for _, result := range []chan FridaStatus{firstResult, secondResult} {
		select {
		case status := <-result:
			results = append(results, status)
		case <-time.After(time.Second):
			t.Fatal("lifecycle request did not complete")
		}
	}
	if runCount != 1 {
		t.Fatalf("lifecycle run count = %d, want 1", runCount)
	}
	for _, status := range results {
		if status.State != FridaStateAttached || !status.Attached || !status.ScriptLoaded {
			t.Fatalf("shared lifecycle status = %#v", status)
		}
	}
}

func TestFridaControllerSingleReconnectLoopAndDetachCancellation(t *testing.T) {
	controller := NewFridaController(nil, nil)
	controller.statusMu.Lock()
	controller.status.State = FridaStateReconnecting
	controller.status.AutoReconnect = true
	controller.status.ErrorClass = FridaErrorClassUnexpectedDetach
	controller.status.LastError = "Frida 会话意外断开"
	controller.statusMu.Unlock()

	controller.startReconnect()
	controller.reconnectMu.Lock()
	firstID := controller.reconnectID
	firstActive := controller.reconnectCancel != nil
	controller.reconnectMu.Unlock()
	if !firstActive {
		t.Fatal("reconnect loop was not started")
	}

	controller.startReconnect()
	controller.reconnectMu.Lock()
	secondID := controller.reconnectID
	controller.reconnectMu.Unlock()
	if secondID != firstID {
		t.Fatalf("duplicate reconnect loop id = %d, want %d", secondID, firstID)
	}

	status, err := controller.Detach(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.State != FridaStateDetached || status.AutoReconnect ||
		status.Attached || status.ScriptLoaded || status.ErrorClass != "" || status.LastError != "" {
		t.Fatalf("status after detach = %#v", status)
	}

	deadline := time.Now().Add(time.Second)
	for {
		controller.reconnectMu.Lock()
		active := controller.reconnectCancel != nil
		reconnectID := controller.reconnectID
		controller.reconnectMu.Unlock()
		if !active {
			if reconnectID != firstID {
				t.Fatalf("reconnect restarted after detach: id = %d, want %d", reconnectID, firstID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detach did not stop the reconnect loop")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestFridaControllerEquivalentInitializePreservesAutoReconnect(t *testing.T) {
	controller := NewFridaController(nil, nil)
	request := FridaInitializeRequest{Type: "local", WechatPID: 42}
	controller.statusMu.Lock()
	controller.desired = request
	controller.status.State = FridaStateAttached
	controller.status.Attached = true
	controller.status.ScriptLoaded = true
	controller.status.AutoReconnect = true
	controller.statusMu.Unlock()

	attachCalled := false
	status, err := controller.runAttachWith(
		context.Background(),
		fridaOperationInitialize,
		request,
		false,
		false,
		func(got FridaInitializeRequest) bool {
			if got != request {
				t.Fatalf("equivalence request = %#v, want %#v", got, request)
			}
			return true
		},
		func(context.Context, FridaInitializeRequest, bool) (FridaStatus, error) {
			attachCalled = true
			return FridaStatus{}, errors.New("equivalent initialize attached again")
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if attachCalled {
		t.Fatal("equivalent initialize executed a new attach")
	}
	if !status.AutoReconnect || !controller.Status().AutoReconnect {
		t.Fatalf("equivalent initialize disabled auto reconnect: %#v", status)
	}
}

func TestFridaControllerConcurrentInitializeAndRescanUsesLatestDesiredTarget(t *testing.T) {
	controller := NewFridaController(nil, nil)
	oldRequest := FridaInitializeRequest{
		Type:       "gadget",
		GadgetAddr: "127.0.0.1:27042",
	}
	newRequest := FridaInitializeRequest{
		Type:       "gadget",
		GadgetAddr: "127.0.0.1:28042",
	}
	controller.statusMu.Lock()
	controller.desired = oldRequest
	controller.statusMu.Unlock()

	initializeStarted := make(chan struct{})
	releaseInitialize := make(chan struct{})
	rescanRequest := make(chan FridaInitializeRequest, 1)
	var callsMu sync.Mutex
	callCount := 0
	attach := func(
		_ context.Context,
		request FridaInitializeRequest,
		_ bool,
	) (FridaStatus, error) {
		callsMu.Lock()
		callCount++
		currentCall := callCount
		callsMu.Unlock()

		if currentCall == 1 {
			close(initializeStarted)
			<-releaseInitialize
			controller.statusMu.Lock()
			controller.desired = request
			controller.status.State = FridaStateAttached
			controller.status.Type = request.Type
			controller.status.GadgetAddr = request.GadgetAddr
			controller.status.Attached = true
			controller.status.ScriptLoaded = true
			controller.status.AutoReconnect = true
			status := controller.status
			controller.statusMu.Unlock()
			return status, nil
		}
		rescanRequest <- request
		return controller.Status(), nil
	}

	initializeResult := make(chan error, 1)
	go func() {
		_, err := controller.runAttachWith(
			context.Background(),
			fridaOperationInitialize,
			newRequest,
			false,
			false,
			func(FridaInitializeRequest) bool { return false },
			attach,
		)
		initializeResult <- err
	}()
	select {
	case <-initializeStarted:
	case <-time.After(time.Second):
		t.Fatal("initialize did not start")
	}

	rescanResult := make(chan error, 1)
	go func() {
		_, err := controller.rescanWith(context.Background(), attach)
		rescanResult <- err
	}()
	select {
	case request := <-rescanRequest:
		t.Fatalf("rescan ran before initialize completed: %#v", request)
	case <-time.After(25 * time.Millisecond):
	}

	close(releaseInitialize)
	if err := <-initializeResult; err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := <-rescanResult; err != nil {
		t.Fatalf("rescan: %v", err)
	}
	select {
	case request := <-rescanRequest:
		if request != newRequest {
			t.Fatalf("rescan request = %#v, want latest desired %#v", request, newRequest)
		}
	case <-time.After(time.Second):
		t.Fatal("rescan did not execute")
	}
}

func TestRunTrackedOperationWaitsForCanceledWorkToExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	release := make(chan struct{})
	type operationResult struct {
		value string
		err   error
	}
	result := make(chan operationResult, 1)

	go func() {
		value, err := runTrackedOperation(
			ctx,
			func() {
				close(cancelObserved)
			},
			func() (string, error) {
				close(started)
				<-release
				return "finished", nil
			},
		)
		result <- operationResult{value: value, err: err}
	}()

	<-started
	cancel()
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("operation cancellation was not propagated")
	}
	select {
	case got := <-result:
		t.Fatalf("tracked operation returned before work exited: %#v", got)
	case <-time.After(25 * time.Millisecond):
	}

	close(release)
	select {
	case got := <-result:
		if got.value != "finished" || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("tracked operation result = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("tracked operation did not return after work exited")
	}
}

func TestFridaControllerDetachCancelsPublishedScriptLoad(t *testing.T) {
	loaderStarted := make(chan struct{})
	loaderExited := make(chan struct{})
	controller := newFridaController(
		nil,
		func(ctx context.Context, _ *frida.Session) (*frida.Script, error) {
			close(loaderStarted)
			<-ctx.Done()
			close(loaderExited)
			return nil, ctx.Err()
		},
	)
	controller.statusMu.Lock()
	controller.status.State = FridaStateReconnecting
	controller.status.AutoReconnect = true
	controller.statusMu.Unlock()

	reconnectResult := make(chan error, 1)
	go func() {
		_, err := controller.runLifecycleOperation(
			context.Background(),
			fridaOperationReconnect,
			FridaInitializeRequest{Type: "gadget", GadgetAddr: "partial.invalid:27042"},
			func(operationContext context.Context) (FridaStatus, error) {
				_, loadErr := controller.loadPublishedScript(
					operationContext,
					fridaResources{remoteAddr: "partial.invalid:27042"},
				)
				controller.takeResources()
				return controller.Status(), loadErr
			},
		)
		reconnectResult <- err
	}()

	select {
	case <-loaderStarted:
	case <-time.After(time.Second):
		t.Fatal("script loader did not start")
	}
	controller.resourcesMu.RLock()
	publishedAddress := controller.resources.remoteAddr
	controller.resourcesMu.RUnlock()
	if publishedAddress != "partial.invalid:27042" {
		t.Fatalf("partial resources were not published: %q", publishedAddress)
	}

	type detachResult struct {
		status FridaStatus
		err    error
	}
	detached := make(chan detachResult, 1)
	go func() {
		status, err := controller.Detach(context.Background())
		detached <- detachResult{status: status, err: err}
	}()

	select {
	case <-loaderExited:
	case <-time.After(time.Second):
		t.Fatal("detach did not cancel the in-progress script loader")
	}
	if err := <-reconnectResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("reconnect error = %v, want context cancellation", err)
	}
	select {
	case result := <-detached:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.status.State != FridaStateDetached || result.status.AutoReconnect {
			t.Fatalf("detach status = %#v", result.status)
		}
	case <-time.After(time.Second):
		t.Fatal("detach did not finish after script loader cleanup")
	}
	controller.resourcesMu.RLock()
	resources := controller.resources
	controller.resourcesMu.RUnlock()
	if resources != (fridaResources{}) {
		t.Fatalf("partial resources remain after detach: %#v", resources)
	}
}
