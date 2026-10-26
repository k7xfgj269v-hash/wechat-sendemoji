package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type automationPermissionRequestProjection struct {
	AutomationPermissionRequest
	Action *automationActionProjection `json:"action,omitempty"`
}

func automationRuntimeEventsHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(r, "after"); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			automationWriteError(w, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable")
			return
		}
		after, err := automationRuntimeEventCursor(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		broker := service.RuntimeEventBroker()
		replay, events, cancel := broker.Subscribe(after)
		defer cancel()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		for _, event := range replay {
			if err := automationWriteSSEEvent(w, event); err != nil {
				return
			}
		}
		flusher.Flush()

		keepalive := time.NewTicker(15 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if err := automationWriteSSEEvent(w, event); err != nil {
					return
				}
				flusher.Flush()
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

func automationPermissionRequestsHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(r, "status", "limit", "offset"); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		limit, offset, err := automationPagination(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		total, requests, err := service.ListPermissionRequests(r.URL.Query().Get("status"), limit, offset)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		projected := automationProjectPermissionRequests(service.store.Snapshot(), requests)
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"total": total, "limit": limit, "offset": offset, "permission_requests": projected,
		})
	}
}

func automationProjectPermissionRequests(
	state AutomationState,
	requests []AutomationPermissionRequest,
) []automationPermissionRequestProjection {
	projected := make([]automationPermissionRequestProjection, len(requests))
	for i := range requests {
		projected[i].AutomationPermissionRequest = requests[i]
		actionIndex := automationActionIndex(&state, requests[i].ActionID)
		if actionIndex < 0 {
			continue
		}
		action := automationProjectAction(state, state.Actions[actionIndex])
		projected[i].Action = &action
	}
	return projected
}

func automationPermissionRequestHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/automation/permission_requests/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" || parts[1] != "decide" {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation permission request not found")
			return
		}
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request automationPermissionDecisionRequest
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		permissionRequest, action, err := service.DecidePermissionRequest(r.Context(), parts[0], request)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"permission_request": permissionRequest,
			"action":             automationProjectAction(service.store.Snapshot(), action),
		})
	}
}

func automationPermissionsHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(r, "limit", "offset"); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		limit, offset, err := automationPagination(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		total, rules, err := service.ListPermissionRules(limit, offset)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"total": total, "limit": limit, "offset": offset, "permissions": rules,
		})
	}
}

func automationPermissionHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/automation/permissions/"))
		if id == "" || strings.Contains(id, "/") {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation permission rule not found")
			return
		}
		if r.Method != http.MethodDelete {
			automationWriteMethodNotAllowed(w, http.MethodDelete)
			return
		}
		rule, err := service.DeletePermissionRule(id)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{"permission": rule})
	}
}

func automationRuntimeEventCursor(r *http.Request) (uint64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("after"))
	if raw == "" {
		raw = strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	}
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, NewAutomationValidationError("after", "is invalid")
	}
	return value, nil
}

func automationWriteSSEEvent(w http.ResponseWriter, event AutomationRuntimeEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	return err
}
