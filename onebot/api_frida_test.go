package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeFridaLifecycle struct {
	status        FridaStatus
	initialize    FridaInitializeRequest
	initializeErr error
	rescanErr     error
	detachErr     error
	rescans       int
	detaches      int
}

func (f *fakeFridaLifecycle) Status() FridaStatus {
	return f.status
}

func (f *fakeFridaLifecycle) Initialize(_ context.Context, request FridaInitializeRequest) (FridaStatus, error) {
	f.initialize = request
	return f.status, f.initializeErr
}

func (f *fakeFridaLifecycle) Rescan(context.Context) (FridaStatus, error) {
	f.rescans++
	return f.status, f.rescanErr
}

func (f *fakeFridaLifecycle) Detach(context.Context) (FridaStatus, error) {
	f.detaches++
	return f.status, f.detachErr
}

func TestFridaStatusAndInitializeHandlers(t *testing.T) {
	lifecycle := &fakeFridaLifecycle{
		status: FridaStatus{
			State:        "attached",
			Type:         "gadget",
			GadgetAddr:   "127.0.0.1:27042",
			Attached:     true,
			ScriptLoaded: true,
			UpdatedAt:    "2026-07-22T12:00:00Z",
		},
	}

	response := httptest.NewRecorder()
	request := newLoopbackFridaRequest(http.MethodGet, "/api/frida/status", "")
	fridaStatusHandler(lifecycle)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var status FridaStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Attached || status.GadgetAddr != "127.0.0.1:27042" {
		t.Fatalf("status = %#v", status)
	}

	response = httptest.NewRecorder()
	request = newLoopbackFridaRequest(http.MethodPost, "/api/frida/initialize", `{"type":"local","wechat_pid":1234}`)
	request.Header.Set(fridaWorkbenchHeader, "1")
	fridaInitializeHandler(lifecycle)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", response.Code, response.Body.String())
	}
	if lifecycle.initialize.Type != "local" || lifecycle.initialize.WechatPID != 1234 {
		t.Fatalf("initialize request = %#v", lifecycle.initialize)
	}
}

func TestFridaMutationHandlersEnforceBoundaries(t *testing.T) {
	lifecycle := &fakeFridaLifecycle{status: FridaStatus{State: "detached"}}

	response := httptest.NewRecorder()
	request := newLoopbackFridaRequest(http.MethodPost, "/api/frida/detach", "")
	fridaDetachHandler(lifecycle)(response, request)
	if response.Code != http.StatusForbidden || lifecycle.detaches != 0 {
		t.Fatalf("missing marker status=%d detaches=%d", response.Code, lifecycle.detaches)
	}

	response = httptest.NewRecorder()
	request = newLoopbackFridaRequest(http.MethodPost, "/api/frida/rescan", "")
	request.RemoteAddr = "203.0.113.9:4567"
	request.Header.Set(fridaWorkbenchHeader, "1")
	fridaRescanHandler(lifecycle)(response, request)
	if response.Code != http.StatusForbidden || lifecycle.rescans != 0 {
		t.Fatalf("remote status=%d rescans=%d", response.Code, lifecycle.rescans)
	}

	response = httptest.NewRecorder()
	request = newLoopbackFridaRequest(http.MethodGet, "/api/frida/detach", "")
	fridaDetachHandler(lifecycle)(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("method status=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestRegisteredFridaMutationsRequireOperatorBearer(t *testing.T) {
	lifecycle := &fakeFridaLifecycle{status: FridaStatus{State: "detached"}}
	mux := http.NewServeMux()
	registerFridaRoutes(mux, lifecycle, "operator-token")

	response := httptest.NewRecorder()
	request := newLoopbackFridaRequest(http.MethodPost, "/api/frida/detach", "")
	request.Header.Set(fridaWorkbenchHeader, "1")
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || lifecycle.detaches != 0 {
		t.Fatalf("missing bearer status=%d detaches=%d", response.Code, lifecycle.detaches)
	}

	response = httptest.NewRecorder()
	request = newLoopbackFridaRequest(http.MethodPost, "/api/frida/detach", "")
	request.Header.Set(fridaWorkbenchHeader, "1")
	request.Header.Set("Authorization", "Bearer operator-token")
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || lifecycle.detaches != 1 {
		t.Fatalf("authorized status=%d detaches=%d body=%s", response.Code, lifecycle.detaches, response.Body.String())
	}
}

func TestFridaInitializeRejectsMalformedAndReportsAttachFailure(t *testing.T) {
	lifecycle := &fakeFridaLifecycle{
		status:        FridaStatus{State: "error", LastError: "attach failed"},
		initializeErr: errors.New("attach failed"),
	}

	response := httptest.NewRecorder()
	request := newLoopbackFridaRequest(http.MethodPost, "/api/frida/initialize", `{"type":"local","unknown":true}`)
	request.Header.Set(fridaWorkbenchHeader, "1")
	fridaInitializeHandler(lifecycle)(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	request = newLoopbackFridaRequest(http.MethodPost, "/api/frida/initialize", `{"type":"gadget","gadget_addr":"127.0.0.1:1"}`)
	request.Header.Set(fridaWorkbenchHeader, "1")
	fridaInitializeHandler(lifecycle)(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("attach failure status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"status"`) || !strings.Contains(response.Body.String(), `"error":"attach failed"`) {
		t.Fatalf("failure body = %s", response.Body.String())
	}
}

func TestAgentFridaRoutesDelegateEveryOperation(t *testing.T) {
	client := &task003AgentClient{
		fridaResult: FridaStatus{
			State:        "attached",
			Type:         "local",
			WechatPID:    2468,
			Attached:     true,
			ScriptLoaded: true,
		},
	}
	mux := http.NewServeMux()
	registerAgentFridaRoutes(mux, client, "operator-token")

	tests := []struct {
		name   string
		method string
		target string
		body   string
		call   string
	}{
		{
			name:   "status",
			method: http.MethodGet,
			target: "/api/frida/status",
			call:   "frida.status",
		},
		{
			name:   "initialize",
			method: http.MethodPost,
			target: "/api/frida/initialize",
			body:   `{"type":"local","wechat_pid":2468}`,
			call:   "frida.initialize",
		},
		{
			name:   "rescan",
			method: http.MethodPost,
			target: "/api/frida/rescan",
			call:   "frida.rescan",
		},
		{
			name:   "detach",
			method: http.MethodPost,
			target: "/api/frida/detach",
			call:   "frida.detach",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := client.callCount(test.call)
			response := httptest.NewRecorder()
			request := newLoopbackFridaRequest(test.method, test.target, test.body)
			if test.method == http.MethodPost {
				request.Header.Set(fridaWorkbenchHeader, "1")
				request.Header.Set("Authorization", "Bearer operator-token")
			}
			mux.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if got := client.callCount(test.call); got != before+1 {
				t.Fatalf("%s calls = %d, want %d", test.call, got, before+1)
			}
		})
	}
	if client.fridaInit.Type != "local" || client.fridaInit.WechatPID != 2468 {
		t.Fatalf("initialize request = %#v", client.fridaInit)
	}
}

func TestAgentFridaUnavailableMapsToServiceUnavailable(t *testing.T) {
	client := &task003AgentClient{
		fridaStatusErr: &AgentError{
			Code:      AgentErrorCodeUnavailable,
			Message:   "agent is offline",
			Retryable: true,
		},
	}
	response := httptest.NewRecorder()
	fridaStatusSourceHandler(client)(
		response,
		newLoopbackFridaRequest(http.MethodGet, "/api/frida/status", ""),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "agent is offline") {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func newLoopbackFridaRequest(method, path, body string) *http.Request {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	request.RemoteAddr = "127.0.0.1:4567"
	return request
}
