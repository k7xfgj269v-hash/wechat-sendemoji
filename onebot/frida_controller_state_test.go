package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestFridaLifecycleStateAndErrorContracts(t *testing.T) {
	states := []struct {
		got  FridaLifecycleState
		want FridaLifecycleState
	}{
		{FridaStateDetached, "detached"},
		{FridaStateAttaching, "attaching"},
		{FridaStateAttached, "attached"},
		{FridaStateReconnecting, "reconnecting"},
		{FridaStateDetaching, "detaching"},
		{FridaStateError, "error"},
	}
	for _, state := range states {
		if state.got != state.want {
			t.Errorf("state = %q, want %q", state.got, state.want)
		}
	}

	classes := []struct {
		got  FridaErrorClass
		want FridaErrorClass
	}{
		{FridaErrorClassInvalidRequest, "invalid_request"},
		{FridaErrorClassDeviceUnavailable, "device_unavailable"},
		{FridaErrorClassTargetNotFound, "target_not_found"},
		{FridaErrorClassAttachFailure, "attach_failure"},
		{FridaErrorClassScriptLoadFailure, "script_load_failure"},
		{FridaErrorClassUnexpectedDetach, "unexpected_detach"},
		{FridaErrorClassDetachFailure, "detach_failure"},
		{FridaErrorClassCancellation, "cancellation"},
	}
	for _, class := range classes {
		if class.got != class.want {
			t.Errorf("error class = %q, want %q", class.got, class.want)
		}
	}
}

func TestFridaLifecycleErrorClassificationAndStatus(t *testing.T) {
	cause := errors.New("attach failed")
	attachErr := newFridaLifecycleError(
		FridaErrorClassAttachFailure,
		"附加失败",
		cause,
	)
	if got := FridaErrorClassOf(attachErr); got != FridaErrorClassAttachFailure {
		t.Fatalf("error class = %q, want %q", got, FridaErrorClassAttachFailure)
	}
	if !errors.Is(attachErr, cause) {
		t.Fatal("lifecycle error did not retain its cause")
	}
	if got := classifyFridaLifecycleError(
		attachErr,
		FridaErrorClassDeviceUnavailable,
		"fallback",
	); got != attachErr {
		t.Fatalf("classified lifecycle error = %v, want original %v", got, attachErr)
	}

	for _, canceled := range []error{context.Canceled, context.DeadlineExceeded} {
		err := classifyFridaLifecycleError(
			canceled,
			FridaErrorClassAttachFailure,
			"attach failed",
		)
		if got := FridaErrorClassOf(err); got != FridaErrorClassCancellation {
			t.Errorf("cancellation class = %q, want %q", got, FridaErrorClassCancellation)
		}
		if !errors.Is(err, canceled) {
			t.Errorf("classified cancellation does not wrap %v", canceled)
		}
	}

	fallbackErr := classifyFridaLifecycleError(
		cause,
		FridaErrorClassScriptLoadFailure,
		"script load failed",
	)
	if got := FridaErrorClassOf(fallbackErr); got != FridaErrorClassScriptLoadFailure {
		t.Fatalf("fallback class = %q, want %q", got, FridaErrorClassScriptLoadFailure)
	}

	status := FridaStatus{}
	setFridaStatusError(&status, attachErr)
	if status.ErrorClass != FridaErrorClassAttachFailure || status.LastError != "附加失败" {
		t.Fatalf("status error = %#v", status)
	}
	setFridaStatusError(&status, nil)
	if status.ErrorClass != "" || status.LastError != "" {
		t.Fatalf("cleared status error = %#v", status)
	}
}

func TestFridaLifecycleCoordinatorSuppressesEquivalentOperation(t *testing.T) {
	var coordinator fridaLifecycleCoordinator
	request := FridaInitializeRequest{Type: "local", WechatPID: 42}
	first, execute, err := coordinator.begin(
		context.Background(),
		fridaOperationInitialize,
		request,
	)
	if err != nil || !execute {
		t.Fatalf("first begin = (%#v, %t, %v)", first, execute, err)
	}

	type beginResult struct {
		operation *fridaLifecycleOperation
		execute   bool
		err       error
	}
	secondResult := make(chan beginResult, 1)
	go func() {
		operation, shouldExecute, beginErr := coordinator.begin(
			context.Background(),
			fridaOperationInitialize,
			request,
		)
		secondResult <- beginResult{operation, shouldExecute, beginErr}
	}()

	select {
	case result := <-secondResult:
		t.Fatalf("equivalent operation returned before active operation finished: %#v", result)
	case <-time.After(25 * time.Millisecond):
	}

	wantStatus := FridaStatus{State: FridaStateAttached, Attached: true, ScriptLoaded: true}
	wantErr := newFridaLifecycleError(
		FridaErrorClassUnexpectedDetach,
		"shared result",
		errors.New("sentinel"),
	)
	coordinator.finish(first, wantStatus, wantErr)

	select {
	case result := <-secondResult:
		if result.err != nil {
			t.Fatalf("equivalent begin error = %v", result.err)
		}
		if result.execute {
			t.Fatal("equivalent operation was allowed to execute twice")
		}
		if result.operation != first {
			t.Fatal("equivalent operation did not reuse the active operation result")
		}
		if result.operation.status != wantStatus || result.operation.err != wantErr {
			t.Fatalf("shared operation result = (%#v, %v)", result.operation.status, result.operation.err)
		}
	case <-time.After(time.Second):
		t.Fatal("equivalent operation did not receive the completed result")
	}
}

func TestFridaLifecycleCoordinatorSerializesDistinctOperations(t *testing.T) {
	var coordinator fridaLifecycleCoordinator
	first, execute, err := coordinator.begin(
		context.Background(),
		fridaOperationInitialize,
		FridaInitializeRequest{Type: "local", WechatPID: 42},
	)
	if err != nil || !execute {
		t.Fatalf("first begin = (%#v, %t, %v)", first, execute, err)
	}

	type beginResult struct {
		operation *fridaLifecycleOperation
		execute   bool
		err       error
	}
	rescanResult := make(chan beginResult, 1)
	go func() {
		operation, shouldExecute, beginErr := coordinator.begin(
			context.Background(),
			fridaOperationRescan,
			FridaInitializeRequest{Type: "local"},
		)
		rescanResult <- beginResult{operation, shouldExecute, beginErr}
	}()

	select {
	case result := <-rescanResult:
		t.Fatalf("distinct operation ran concurrently: %#v", result)
	case <-time.After(25 * time.Millisecond):
	}

	coordinator.finish(first, FridaStatus{State: FridaStateAttached}, nil)
	select {
	case result := <-rescanResult:
		if result.err != nil || !result.execute {
			t.Fatalf("serialized begin = (%#v, %t, %v)", result.operation, result.execute, result.err)
		}
		if result.operation == first {
			t.Fatal("distinct operation reused the completed operation")
		}
		coordinator.finish(result.operation, FridaStatus{State: FridaStateAttached}, nil)
	case <-time.After(time.Second):
		t.Fatal("distinct operation did not start after active operation finished")
	}
}

func TestFridaLifecycleCoordinatorDetachCancelsReconnect(t *testing.T) {
	var coordinator fridaLifecycleCoordinator
	reconnect, execute, err := coordinator.begin(
		context.Background(),
		fridaOperationReconnect,
		FridaInitializeRequest{Type: "local", WechatPID: 42},
	)
	if err != nil || !execute {
		t.Fatalf("reconnect begin = (%#v, %t, %v)", reconnect, execute, err)
	}

	type beginResult struct {
		operation *fridaLifecycleOperation
		execute   bool
		err       error
	}
	detachResult := make(chan beginResult, 1)
	go func() {
		operation, shouldExecute, beginErr := coordinator.begin(
			context.Background(),
			fridaOperationDetach,
			FridaInitializeRequest{},
		)
		detachResult <- beginResult{operation, shouldExecute, beginErr}
	}()

	select {
	case <-reconnect.context.Done():
	case <-time.After(time.Second):
		t.Fatal("detach did not cancel the active reconnect operation")
	}
	select {
	case result := <-detachResult:
		t.Fatalf("detach started before reconnect cleanup completed: %#v", result)
	case <-time.After(25 * time.Millisecond):
	}

	reconnectErr := classifyFridaLifecycleError(
		reconnect.context.Err(),
		FridaErrorClassCancellation,
		"reconnect canceled",
	)
	coordinator.finish(reconnect, FridaStatus{State: FridaStateReconnecting}, reconnectErr)

	select {
	case result := <-detachResult:
		if result.err != nil || !result.execute {
			t.Fatalf("detach begin = (%#v, %t, %v)", result.operation, result.execute, result.err)
		}
		coordinator.finish(result.operation, FridaStatus{State: FridaStateDetached}, nil)
	case <-time.After(time.Second):
		t.Fatal("detach did not start after reconnect cancellation completed")
	}
}

func TestFridaLifecycleCoordinatorCanceledWaiter(t *testing.T) {
	var coordinator fridaLifecycleCoordinator
	active, execute, err := coordinator.begin(
		context.Background(),
		fridaOperationInitialize,
		FridaInitializeRequest{Type: "local", WechatPID: 42},
	)
	if err != nil || !execute {
		t.Fatalf("active begin = (%#v, %t, %v)", active, execute, err)
	}
	defer coordinator.finish(active, FridaStatus{State: FridaStateDetached}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	operation, shouldExecute, err := coordinator.begin(
		ctx,
		fridaOperationRescan,
		FridaInitializeRequest{Type: "local"},
	)
	if operation != nil || shouldExecute {
		t.Fatalf("canceled begin = (%#v, %t, %v)", operation, shouldExecute, err)
	}
	if got := FridaErrorClassOf(err); got != FridaErrorClassCancellation {
		t.Fatalf("canceled waiter class = %q, want %q", got, FridaErrorClassCancellation)
	}
}
