package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	fridaAPIRequestLimit  = 4096
	fridaWorkbenchHeader  = "X-OneBot-Workbench"
	fridaStatusAPITimeout = 8 * time.Second
	fridaActionAPITimeout = 50 * time.Second
	fridaDetachAPITimeout = 10 * time.Second
)

func registerFridaRoutes(mux *http.ServeMux, lifecycle FridaLifecycle, operatorTokens ...string) {
	registerFridaRoutesForSource(mux, lifecycle, firstFridaOperatorToken(operatorTokens))
}

func registerAgentFridaRoutes(mux *http.ServeMux, client AgentClient, operatorTokens ...string) {
	registerFridaRoutesForSource(mux, client, firstFridaOperatorToken(operatorTokens))
}

func registerFridaRoutesForSource(mux *http.ServeMux, source any, operatorToken string) {
	mux.HandleFunc("/api/frida/status", fridaStatusSourceHandler(source))
	mux.HandleFunc(
		"/api/frida/initialize",
		fridaMutationAuth(operatorToken, fridaInitializeSourceHandler(source)),
	)
	mux.HandleFunc(
		"/api/frida/rescan",
		fridaMutationAuth(operatorToken, fridaRescanSourceHandler(source)),
	)
	mux.HandleFunc(
		"/api/frida/detach",
		fridaMutationAuth(operatorToken, fridaDetachSourceHandler(source)),
	)
}

func firstFridaOperatorToken(tokens []string) string {
	if len(tokens) == 0 {
		return ""
	}
	return strings.TrimSpace(tokens[0])
}

func fridaMutationAuth(operatorToken string, next http.HandlerFunc) http.HandlerFunc {
	if strings.TrimSpace(operatorToken) == "" {
		return next
	}
	return automationOperatorAuth(operatorToken, next)
}

func fridaStatusHandler(lifecycle FridaLifecycle) http.HandlerFunc {
	return fridaStatusSourceHandler(lifecycle)
}

func fridaStatusSourceHandler(source any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fridaRequestAllowed(w, r, http.MethodGet, false) {
			return
		}
		request, cancel := fridaRequestWithTimeout(r, fridaStatusAPITimeout)
		defer cancel()
		status, err := fridaStatusForSource(request, source)
		if err != nil {
			writeFridaError(w, agentHTTPStatus(err), errors.New(publicAgentError(err, "Frida 状态不可用")))
			return
		}
		writeFridaJSON(w, http.StatusOK, status)
	}
}

func fridaInitializeHandler(lifecycle FridaLifecycle) http.HandlerFunc {
	return fridaInitializeSourceHandler(lifecycle)
}

func fridaInitializeSourceHandler(source any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fridaRequestAllowed(w, r, http.MethodPost, true) {
			return
		}
		var request FridaInitializeRequest
		if err := decodeFridaRequest(w, r, &request); err != nil {
			writeFridaError(w, http.StatusBadRequest, err)
			return
		}
		timedRequest, cancel := fridaRequestWithTimeout(r, fridaActionAPITimeout)
		defer cancel()
		status, err := fridaInitializeForSource(timedRequest, source, request)
		if err != nil {
			statusCode := http.StatusConflict
			if _, ok := source.(AgentClient); ok {
				statusCode = agentHTTPStatus(err)
			}
			writeFridaJSON(w, statusCode, map[string]any{
				"error":  err.Error(),
				"status": status,
			})
			return
		}
		writeFridaJSON(w, http.StatusOK, status)
	}
}

func fridaRescanHandler(lifecycle FridaLifecycle) http.HandlerFunc {
	return fridaRescanSourceHandler(lifecycle)
}

func fridaRescanSourceHandler(source any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fridaRequestAllowed(w, r, http.MethodPost, true) {
			return
		}
		request, cancel := fridaRequestWithTimeout(r, fridaActionAPITimeout)
		defer cancel()
		status, err := fridaRescanForSource(request, source)
		if err != nil {
			statusCode := http.StatusConflict
			if _, ok := source.(AgentClient); ok {
				statusCode = agentHTTPStatus(err)
			}
			writeFridaJSON(w, statusCode, map[string]any{
				"error":  err.Error(),
				"status": status,
			})
			return
		}
		writeFridaJSON(w, http.StatusOK, status)
	}
}

func fridaDetachHandler(lifecycle FridaLifecycle) http.HandlerFunc {
	return fridaDetachSourceHandler(lifecycle)
}

func fridaDetachSourceHandler(source any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !fridaRequestAllowed(w, r, http.MethodPost, true) {
			return
		}
		request, cancel := fridaRequestWithTimeout(r, fridaDetachAPITimeout)
		defer cancel()
		status, err := fridaDetachForSource(request, source)
		if err != nil {
			statusCode := http.StatusInternalServerError
			if _, ok := source.(AgentClient); ok {
				statusCode = agentHTTPStatus(err)
			}
			writeFridaJSON(w, statusCode, map[string]any{
				"error":  err.Error(),
				"status": status,
			})
			return
		}
		writeFridaJSON(w, http.StatusOK, status)
	}
}

func fridaRequestWithTimeout(r *http.Request, timeout time.Duration) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	return r.Clone(ctx), cancel
}

func fridaStatusForSource(r *http.Request, source any) (FridaStatus, error) {
	switch value := source.(type) {
	case AgentClient:
		if value == nil {
			return FridaStatus{}, &AgentError{
				Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
			}
		}
		return value.FridaStatus(r.Context())
	case FridaLifecycle:
		if value == nil {
			return FridaStatus{}, errors.New("Frida lifecycle is unavailable")
		}
		return value.Status(), nil
	default:
		return FridaStatus{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
}

func fridaInitializeForSource(
	r *http.Request,
	source any,
	request FridaInitializeRequest,
) (FridaStatus, error) {
	switch value := source.(type) {
	case AgentClient:
		return value.InitializeFrida(r.Context(), request)
	case FridaLifecycle:
		return value.Initialize(r.Context(), request)
	default:
		return FridaStatus{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
}

func fridaRescanForSource(r *http.Request, source any) (FridaStatus, error) {
	switch value := source.(type) {
	case AgentClient:
		return value.RescanFrida(r.Context())
	case FridaLifecycle:
		return value.Rescan(r.Context())
	default:
		return FridaStatus{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
}

func fridaDetachForSource(r *http.Request, source any) (FridaStatus, error) {
	switch value := source.(type) {
	case AgentClient:
		return value.DetachFrida(r.Context())
	case FridaLifecycle:
		return value.Detach(r.Context())
	default:
		return FridaStatus{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
}

func fridaRequestAllowed(w http.ResponseWriter, r *http.Request, method string, mutation bool) bool {
	if r.Method != method {
		w.Header().Set("Allow", method)
		writeFridaError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
		return false
	}
	if !automationLoopbackRequest(r) {
		writeFridaError(w, http.StatusForbidden, errors.New("Frida API 仅允许本机访问"))
		return false
	}
	if mutation && r.Header.Get(fridaWorkbenchHeader) != "1" {
		writeFridaError(w, http.StatusForbidden, errors.New("缺少工作台请求标记"))
		return false
	}
	return true
}

func decodeFridaRequest(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.Body == nil {
		return errors.New("请求体不能为空")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, fridaAPIRequestLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func writeFridaError(w http.ResponseWriter, status int, err error) {
	writeFridaJSON(w, status, map[string]string{"error": err.Error()})
}

func writeFridaJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
