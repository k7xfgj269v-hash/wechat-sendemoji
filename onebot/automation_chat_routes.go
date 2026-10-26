package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func registerAutomationChatRoutes(
	mux *http.ServeMux,
	service *AutomationService,
	operatorToken string,
) {
	mux.HandleFunc(
		"/api/automation/chat/sessions",
		automationOperatorAuth(operatorToken, automationChatSessionsHandler(service)),
	)
	mux.HandleFunc(
		"/api/automation/chat/sessions/",
		automationOperatorAuth(operatorToken, automationChatSessionHandler(service)),
	)
	mux.HandleFunc(
		"/api/automation/chat/turns/",
		automationOperatorAuth(operatorToken, automationChatTurnHandler(service)),
	)
}

func automationChatSessionsHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if err := automationRejectUnknownQuery(r, "limit", "offset"); err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			limit, offset, err := automationPagination(r)
			if err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			total, sessions, err := service.ListChatSessions(limit, offset)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, map[string]any{
				"total": total, "limit": limit, "offset": offset, "sessions": sessions,
			})
		case http.MethodPost:
			if err := automationRejectUnknownQuery(r); err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			var request struct {
				Title string `json:"title"`
			}
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			session, err := service.CreateChatSession(request.Title)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusCreated, map[string]any{"session": session})
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func automationChatSessionHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/automation/chat/sessions/"), "/")
		parts := strings.Split(path, "/")
		if len(parts) < 1 || len(parts) > 2 || parts[0] == "" {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation chat session not found")
			return
		}
		switch {
		case len(parts) == 1 && r.Method == http.MethodGet:
			if err := automationRejectUnknownQuery(r); err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			trace, err := service.ChatSessionTrace(parts[0])
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, trace)
		case len(parts) == 2 && parts[1] == "turns" && r.Method == http.MethodPost:
			if err := automationRejectUnknownQuery(r); err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			var request struct {
				ExpectedVersion uint64 `json:"expected_version"`
				Text            string `json:"text"`
			}
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			turn, err := service.SubmitChatTurn(r.Context(), parts[0], request.ExpectedVersion, request.Text)
			if err != nil {
				automationWriteChatServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusAccepted, map[string]any{"turn": turn})
		default:
			if len(parts) == 2 && parts[1] == "turns" {
				automationWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			automationWriteError(w, http.StatusNotFound, "not_found", "automation chat operation not found")
		}
	}
}

func automationChatTurnHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/automation/chat/turns/"), "/")
		parts := strings.Split(path, "/")
		if len(parts) != 2 || parts[0] == "" {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation chat turn not found")
			return
		}
		switch parts[1] {
		case "events":
			if r.Method != http.MethodGet {
				automationWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			automationChatTurnEventsHandler(service, parts[0])(w, r)
		case "cancel":
			if r.Method != http.MethodPost {
				automationWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			if err := automationRejectUnknownQuery(r); err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			var request struct {
				ExpectedVersion uint64 `json:"expected_version"`
				OperatorID      string `json:"operator_id"`
			}
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			turn, err := service.CancelChatTurn(parts[0], request.ExpectedVersion, request.OperatorID)
			if err != nil {
				automationWriteChatServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, map[string]any{"turn": turn})
		default:
			automationWriteError(w, http.StatusNotFound, "not_found", "automation chat operation not found")
		}
	}
}

func automationChatTurnEventsHandler(service *AutomationService, turnID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := automationRejectUnknownQuery(r, "after"); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			automationWriteError(w, http.StatusInternalServerError, "stream_unavailable", "streaming is unavailable")
			return
		}
		after, err := automationTurnEventCursor(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		replay, events, cancel, err := service.SubscribeTurnEvents(turnID, after)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		defer cancel()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		for _, event := range replay {
			if err := automationWriteTurnSSEEvent(w, event); err != nil {
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
				if err := automationWriteTurnSSEEvent(w, event); err != nil {
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

func automationTurnEventCursor(r *http.Request) (uint64, error) {
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

func automationWriteTurnSSEEvent(w http.ResponseWriter, event AutomationTurnEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	return err
}

func automationWriteChatServiceError(w http.ResponseWriter, err error) {
	if strings.Contains(strings.ToLower(err.Error()), "version conflict") ||
		strings.Contains(strings.ToLower(err.Error()), "active turn") ||
		strings.Contains(strings.ToLower(err.Error()), "already terminal") {
		automationWriteError(w, http.StatusConflict, "conflict", err.Error())
		return
	}
	automationWriteServiceError(w, err)
}
