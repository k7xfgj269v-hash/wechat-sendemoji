package main

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

func registerAutomationRoutes(
	mux *http.ServeMux,
	service *AutomationService,
	operatorToken string,
	agentToken string,
) {
	mux.HandleFunc("/api/automation/actions", automationOperatorAuth(operatorToken, automationActionsHandler(service)))
	mux.HandleFunc("/api/automation/actions/", automationOperatorAuth(operatorToken, automationActionHandler(service)))
	mux.HandleFunc("/api/automation/policy", automationOperatorAuth(operatorToken, automationPolicyHandler(service)))
	mux.HandleFunc("/api/automation/runs", automationScopedAuth(operatorToken, agentToken, automationRunsHandler(service)))
	mux.HandleFunc("/api/automation/runs/", automationOperatorAuth(operatorToken, automationRunHandler(service)))
	mux.HandleFunc("/api/automation/runtime/events", automationOperatorAuth(operatorToken, automationRuntimeEventsHandler(service)))
	mux.HandleFunc("/api/automation/permission_requests", automationOperatorAuth(operatorToken, automationPermissionRequestsHandler(service)))
	mux.HandleFunc("/api/automation/permission_requests/", automationOperatorAuth(operatorToken, automationPermissionRequestHandler(service)))
	mux.HandleFunc("/api/automation/permissions", automationOperatorAuth(operatorToken, automationPermissionsHandler(service)))
	mux.HandleFunc("/api/automation/permissions/", automationOperatorAuth(operatorToken, automationPermissionHandler(service)))
	mux.HandleFunc("/api/automation/audit", automationOperatorAuth(operatorToken, automationAuditHandler(service)))
	mux.HandleFunc("/api/automation/audit/export.jsonl", automationOperatorAuth(operatorToken, automationAuditExportHandler(service)))
	mux.HandleFunc("/api/automation/tools", automationOperatorAuth(operatorToken, automationToolsHandler()))
	registerAutomationChatRoutes(mux, service, operatorToken)
	if runtime := automationRuntimeForService(service); runtime != nil {
		registerAutomationMetricsRoutes(mux, runtime.metrics, operatorToken)
		if runtime.providerConfigStore == nil {
			return
		}
		registerAutomationProviderConfigRoutes(
			mux,
			runtime.providerConfigStore,
			runtime.provider,
			operatorToken,
		)
		if runtime.operationPermissionStore != nil {
			registerAutomationOperationPermissionRoutes(
				mux,
				runtime.operationPermissionStore,
				operatorToken,
			)
		}
		registerAutomationTestChatRoutes(mux, service, runtime.provider, operatorToken)
	}
}

func registerDataManagementRoutes(
	mux *http.ServeMux,
	service *DataManagementService,
	operatorToken string,
) {
	mux.Handle(
		"/api/system/data/status",
		automationOperatorAuth(operatorToken, dataManagementStatusHandler(service)),
	)
	mux.Handle(
		"/api/system/data/export",
		automationOperatorAuth(operatorToken, dataManagementExportHandler(service)),
	)
	mux.Handle(
		"/api/system/data/validate",
		automationOperatorAuth(operatorToken, dataManagementValidateHandler()),
	)
}

func automationActionsHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(r, "status", "tool", "risk", "source_message_event_id", "limit", "offset"); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		status := AutomationActionStatus(r.URL.Query().Get("status"))
		if status != "" && !IsValidAutomationActionStatus(status) {
			automationWriteError(w, http.StatusBadRequest, "validation_error", "status is invalid")
			return
		}
		tool := AutomationToolName(r.URL.Query().Get("tool"))
		if tool != "" && !IsValidAutomationToolName(tool) {
			automationWriteError(w, http.StatusBadRequest, "validation_error", "tool is invalid")
			return
		}
		risk := AutomationRisk(r.URL.Query().Get("risk"))
		if risk != "" && !IsValidAutomationRisk(risk) {
			automationWriteError(w, http.StatusBadRequest, "validation_error", "risk is invalid")
			return
		}
		limit, offset, err := automationPagination(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		total, actions, err := service.ListActions(
			status,
			tool,
			risk,
			r.URL.Query().Get("source_message_event_id"),
			limit,
			offset,
		)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"total": total, "limit": limit, "offset": offset, "actions": actions,
		})
	}
}

func automationActionHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/automation/actions/")
		parts := strings.Split(path, "/")
		if len(parts) < 1 || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation action not found")
			return
		}
		actionID := parts[0]
		if len(parts) == 1 {
			if r.Method != http.MethodGet {
				automationWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			detail, err := service.ActionDetail(actionID)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, detail)
			return
		}
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request automationActionMutationRequest
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		var (
			action AutomationAction
			err    error
		)
		switch parts[1] {
		case "approve":
			action, err = service.ApproveAction(r.Context(), actionID, request)
		case "edit_approve":
			action, err = service.EditApproveAction(r.Context(), actionID, request)
		case "reject":
			action, err = service.RejectAction(actionID, request)
		case "retry":
			action, err = service.RetryAction(r.Context(), actionID, request)
		default:
			automationWriteError(w, http.StatusNotFound, "not_found", "automation operation not found")
			return
		}
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{"action": action})
	}
}

func automationPolicyHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			automationWriteJSON(w, http.StatusOK, service.Policy())
		case http.MethodPut:
			var request automationPolicyUpdateRequest
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			result, err := service.UpdatePolicy(request)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, result)
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
	}
}

func automationRunsHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if err := automationRejectUnknownQuery(r, "status", "source_message_event_id", "limit", "offset"); err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			status := AutomationRunStatus(r.URL.Query().Get("status"))
			if status != "" && !IsValidAutomationRunStatus(status) {
				automationWriteError(w, http.StatusBadRequest, "validation_error", "status is invalid")
				return
			}
			limit, offset, err := automationPagination(r)
			if err != nil {
				automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
				return
			}
			total, runs, err := service.ListRuns(status, r.URL.Query().Get("source_message_event_id"), limit, offset)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, map[string]any{
				"total": total, "limit": limit, "offset": offset, "runs": runs,
			})
		case http.MethodPost:
			var request struct {
				EventID string `json:"event_id"`
			}
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			event, ok := automationFindMessageEvent(service.crm.Snapshot(), strings.TrimSpace(request.EventID))
			if !ok {
				automationWriteError(w, http.StatusNotFound, "not_found", "message event not found")
				return
			}
			run, err := service.ProcessEvent(r.Context(), event)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusAccepted, map[string]any{"run": run})
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func automationRunHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/automation/runs/")
		parts := strings.Split(path, "/")
		if len(parts) < 1 || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation run not found")
			return
		}
		runID := parts[0]
		if len(parts) == 2 {
			if parts[1] != "retry" {
				automationWriteError(w, http.StatusNotFound, "not_found", "automation operation not found")
				return
			}
			if r.Method != http.MethodPost {
				automationWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var request struct {
				ExpectedVersion uint64 `json:"expected_version"`
				OperatorID      string `json:"operator_id"`
				Reason          string `json:"reason,omitempty"`
			}
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			if service == nil || service.engine == nil {
				automationWriteServiceError(
					w,
					NewAutomationValidationError("automation_service", "run engine is unavailable"),
				)
				return
			}
			run, err := service.engine.RetryRun(
				r.Context(),
				runID,
				request.ExpectedVersion,
				request.OperatorID,
				request.Reason,
			)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusAccepted, map[string]any{"run": run})
			return
		}
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if strings.Contains(runID, "/") {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation run not found")
			return
		}
		detail, err := service.RunDetail(runID)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, detail)
	}
}

func automationOperatorAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return automationAuth([]string{token}, next)
}

func automationScopedAuth(operatorToken, agentToken string, next http.HandlerFunc) http.HandlerFunc {
	return automationAuth([]string{operatorToken, agentToken}, next)
}

func automationAuth(tokens []string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !automationLoopbackRequest(r) {
			automationWriteError(w, http.StatusForbidden, "loopback_required", "automation API is loopback-only")
			return
		}
		authorization := strings.TrimSpace(r.Header.Get("Authorization"))
		valid := false
		for _, token := range tokens {
			if token != "" && authorization == "Bearer "+token {
				valid = true
				break
			}
		}
		if !valid {
			w.Header().Set("WWW-Authenticate", "Bearer")
			automationWriteError(w, http.StatusUnauthorized, "unauthorized", "automation token is invalid")
			return
		}
		next(w, r)
	}
}

func automationLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if host == "" {
		return true
	}
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func automationDecodeObject(r *http.Request, destination any) error {
	if r.Body == nil {
		return errors.New("request body is required")
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, CRMMaxRequestBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func automationRejectUnknownQuery(r *http.Request, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key, values := range r.URL.Query() {
		if _, ok := allowedSet[key]; !ok {
			return errors.New(key + " is not supported")
		}
		if len(values) != 1 {
			return errors.New(key + " must be specified once")
		}
	}
	return nil
}

func automationPagination(r *http.Request) (int, int, error) {
	limit := 50
	offset := 0
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, errors.New("limit is invalid")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("offset is invalid")
		}
	}
	return limit, offset, nil
}

func automationWriteServiceError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "operation_failed"
	switch {
	case errors.Is(err, ErrAutomationValidation), errors.Is(err, ErrCRMValidation):
		status = http.StatusBadRequest
		code = "validation_error"
	case errors.Is(err, ErrCRMConflict),
		errors.Is(err, ErrAutomationOutboxConflict),
		errors.Is(err, ErrAutomationProviderConfigConflict):
		status = http.StatusConflict
		code = "conflict"
	case strings.Contains(strings.ToLower(err.Error()), "not found"):
		status = http.StatusNotFound
		code = "not_found"
	}
	automationWriteError(w, status, code, err.Error())
}

func automationWriteError(w http.ResponseWriter, status int, code, message string) {
	automationWriteJSON(w, status, map[string]any{"code": code, "error": message})
}

func automationWriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func automationWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	automationWriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}
