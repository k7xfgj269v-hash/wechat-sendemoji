package main

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
)

type FridaLifecycleState string

const (
	FridaStateDetached     FridaLifecycleState = "detached"
	FridaStateAttaching    FridaLifecycleState = "attaching"
	FridaStateAttached     FridaLifecycleState = "attached"
	FridaStateReconnecting FridaLifecycleState = "reconnecting"
	FridaStateDetaching    FridaLifecycleState = "detaching"
	FridaStateError        FridaLifecycleState = "error"
)

type FridaErrorClass string

const (
	FridaErrorClassInvalidRequest    FridaErrorClass = "invalid_request"
	FridaErrorClassDeviceUnavailable FridaErrorClass = "device_unavailable"
	FridaErrorClassTargetNotFound    FridaErrorClass = "target_not_found"
	FridaErrorClassAttachFailure     FridaErrorClass = "attach_failure"
	FridaErrorClassScriptLoadFailure FridaErrorClass = "script_load_failure"
	FridaErrorClassUnexpectedDetach  FridaErrorClass = "unexpected_detach"
	FridaErrorClassDetachFailure     FridaErrorClass = "detach_failure"
	FridaErrorClassCancellation      FridaErrorClass = "cancellation"
)

type FridaLifecycleError struct {
	Class   FridaErrorClass
	Message string
	Cause   error
}

func (e *FridaLifecycleError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *FridaLifecycleError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func newFridaLifecycleError(class FridaErrorClass, message string, cause error) error {
	if cause == nil && message == "" {
		return nil
	}
	return &FridaLifecycleError{
		Class:   class,
		Message: message,
		Cause:   cause,
	}
}

func classifyFridaLifecycleError(err error, fallback FridaErrorClass, message string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return newFridaLifecycleError(
			FridaErrorClassCancellation,
			"Frida 生命周期操作已取消",
			err,
		)
	}
	var lifecycleErr *FridaLifecycleError
	if errors.As(err, &lifecycleErr) {
		return err
	}
	return newFridaLifecycleError(fallback, message, err)
}

func FridaErrorClassOf(err error) FridaErrorClass {
	var lifecycleErr *FridaLifecycleError
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Class
	}
	return ""
}

func setFridaStatusError(status *FridaStatus, err error) {
	if status == nil {
		return
	}
	status.ErrorClass = FridaErrorClassOf(err)
	if err == nil {
		status.LastError = ""
		return
	}
	status.LastError = err.Error()
}

type fridaLifecycleOperationKind string

const (
	fridaOperationInitialize fridaLifecycleOperationKind = "initialize"
	fridaOperationRescan     fridaLifecycleOperationKind = "rescan"
	fridaOperationReconnect  fridaLifecycleOperationKind = "reconnect"
	fridaOperationDetach     fridaLifecycleOperationKind = "detach"
)

type fridaLifecycleOperation struct {
	kind    fridaLifecycleOperationKind
	request FridaInitializeRequest
	context context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	status  FridaStatus
	err     error
}

func (o *fridaLifecycleOperation) equivalent(
	kind fridaLifecycleOperationKind,
	request FridaInitializeRequest,
) bool {
	return o != nil && o.kind == kind && o.request == request
}

type fridaLifecycleCoordinator struct {
	mu     sync.Mutex
	active *fridaLifecycleOperation
}

func (c *fridaLifecycleCoordinator) begin(
	ctx context.Context,
	kind fridaLifecycleOperationKind,
	request FridaInitializeRequest,
) (*fridaLifecycleOperation, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, classifyFridaLifecycleError(
				err,
				FridaErrorClassCancellation,
				"Frida 生命周期操作已取消",
			)
		}

		c.mu.Lock()
		active := c.active
		if active == nil {
			operationContext, cancel := context.WithCancel(ctx)
			operation := &fridaLifecycleOperation{
				kind:    kind,
				request: request,
				context: operationContext,
				cancel:  cancel,
				done:    make(chan struct{}),
			}
			c.active = operation
			c.mu.Unlock()
			return operation, true, nil
		}
		if kind == fridaOperationDetach && active.kind != fridaOperationDetach {
			active.cancel()
		} else if kind != fridaOperationReconnect && active.kind == fridaOperationReconnect {
			active.cancel()
		}
		equivalent := active.equivalent(kind, request)
		c.mu.Unlock()

		select {
		case <-active.done:
			if equivalent {
				return active, false, nil
			}
		case <-ctx.Done():
			return nil, false, classifyFridaLifecycleError(
				ctx.Err(),
				FridaErrorClassCancellation,
				"Frida 生命周期操作已取消",
			)
		}
	}
}

func (c *fridaLifecycleCoordinator) finish(
	operation *fridaLifecycleOperation,
	status FridaStatus,
	err error,
) {
	if operation == nil {
		return
	}
	c.mu.Lock()
	operation.status = status
	operation.err = err
	if c.active == operation {
		c.active = nil
	}
	operation.cancel()
	close(operation.done)
	c.mu.Unlock()
}

func (c *fridaLifecycleCoordinator) cancelActiveExcept(kind fridaLifecycleOperationKind) {
	c.mu.Lock()
	active := c.active
	if active != nil && active.kind != kind {
		active.cancel()
	}
	c.mu.Unlock()
}

func normalizeFridaInitializeRequest(request FridaInitializeRequest) (FridaInitializeRequest, error) {
	request.Type = strings.ToLower(strings.TrimSpace(request.Type))
	request.GadgetAddr = strings.TrimSpace(request.GadgetAddr)
	switch request.Type {
	case "local":
		if request.WechatPID < 0 {
			return request, newFridaLifecycleError(
				FridaErrorClassInvalidRequest,
				"wechat_pid 不能为负数",
				nil,
			)
		}
		request.GadgetAddr = ""
	case "gadget":
		if request.GadgetAddr == "" {
			request.GadgetAddr = "127.0.0.1:27042"
		}
		if len(request.GadgetAddr) > 255 {
			return request, newFridaLifecycleError(
				FridaErrorClassInvalidRequest,
				"gadget_addr 过长",
				nil,
			)
		}
		_, portText, err := net.SplitHostPort(request.GadgetAddr)
		if err != nil {
			return request, newFridaLifecycleError(
				FridaErrorClassInvalidRequest,
				"gadget_addr 必须使用 host:port 格式",
				err,
			)
		}
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return request, newFridaLifecycleError(
				FridaErrorClassInvalidRequest,
				"gadget_addr 端口无效",
				err,
			)
		}
		request.WechatPID = 0
	default:
		return request, newFridaLifecycleError(
			FridaErrorClassInvalidRequest,
			"type 仅支持 local 或 gadget",
			nil,
		)
	}
	return request, nil
}
