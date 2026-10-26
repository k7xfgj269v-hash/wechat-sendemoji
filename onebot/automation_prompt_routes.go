package main

import (
	"net/http"
	"strings"
)

type automationPromptEvaluateBody struct {
	RevisionID string `json:"revision_id"`
	CreatedBy  string `json:"created_by"`
}

func registerAutomationPromptRoutes(
	mux *http.ServeMux,
	store *AutomationProviderConfigStore,
	provider *DynamicAutomationProvider,
	operatorToken string,
) {
	mux.HandleFunc(
		"/api/automation/prompts",
		automationOperatorAuth(operatorToken, automationPromptsHandler(store, provider)),
	)
	mux.HandleFunc(
		"/api/automation/prompts/compare",
		automationOperatorAuth(operatorToken, automationPromptCompareHandler(store)),
	)
	mux.HandleFunc(
		"/api/automation/prompts/evaluate",
		automationOperatorAuth(operatorToken, automationPromptEvaluateHandler(store)),
	)
	mux.HandleFunc(
		"/api/automation/prompts/",
		automationOperatorAuth(operatorToken, automationPromptItemHandler(store, provider)),
	)
}

func automationPromptsHandler(
	store *AutomationProviderConfigStore,
	dynamic *DynamicAutomationProvider,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			config := store.Public()
			automationWriteJSON(w, http.StatusOK, map[string]any{
				"provider_version":       config.Version,
				"active_revision_id":     config.ActivePromptRevisionID,
				"active_revision_number": config.ActivePromptRevisionNumber,
				"revisions":              store.PromptRevisions(),
			})
		case http.MethodPost:
			var request AutomationPromptSaveRequest
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			revision, config, err := store.SavePromptRevision(
				request.ExpectedVersion,
				request.Template,
				request.CreatedBy,
				request.Reason,
			)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			if err := automationSwapConfiguredProvider(store, dynamic); err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, map[string]any{
				"revision": revision,
				"provider": config,
			})
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func automationPromptCompareHandler(store *AutomationProviderConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		fromID := strings.TrimSpace(r.URL.Query().Get("from"))
		toID := strings.TrimSpace(r.URL.Query().Get("to"))
		if fromID == "" || toID == "" {
			automationWriteError(w, http.StatusBadRequest, "validation_error", "from and to prompt revisions are required")
			return
		}
		comparison, err := store.ComparePromptRevisions(fromID, toID)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, comparison)
	}
}

func automationPromptEvaluateHandler(store *AutomationProviderConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request automationPromptEvaluateBody
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		evaluation, err := store.EvaluatePromptRevision(r.Context(), request.RevisionID, request.CreatedBy)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, evaluation)
	}
}

func automationPromptItemHandler(
	store *AutomationProviderConfigStore,
	dynamic *DynamicAutomationProvider,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/automation/prompts/"), "/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 2 {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation prompt revision not found")
			return
		}
		revisionID := strings.TrimSpace(parts[0])
		if revisionID == "" {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation prompt revision not found")
			return
		}
		if len(parts) == 1 {
			if r.Method != http.MethodGet {
				automationWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			revision, ok := store.PromptRevision(revisionID)
			if !ok {
				automationWriteError(w, http.StatusNotFound, "not_found", "automation prompt revision not found")
				return
			}
			automationWriteJSON(w, http.StatusOK, revision)
			return
		}
		switch parts[1] {
		case "activate", "rollback":
			automationPromptActivationHandler(store, dynamic, revisionID, parts[1] == "rollback").ServeHTTP(w, r)
		case "evaluate":
			automationPromptRevisionEvaluationHandler(store, revisionID).ServeHTTP(w, r)
		default:
			automationWriteError(w, http.StatusNotFound, "not_found", "automation prompt operation not found")
		}
	}
}

func automationPromptActivationHandler(
	store *AutomationProviderConfigStore,
	dynamic *DynamicAutomationProvider,
	revisionID string,
	rollback bool,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request AutomationPromptActivationRequest
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if rollback && strings.TrimSpace(request.Reason) == "" {
			request.Reason = "prompt rollback"
		}
		config, err := store.ActivatePromptRevision(
			request.ExpectedVersion,
			revisionID,
			request.CreatedBy,
			request.Reason,
		)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		if err := automationSwapConfiguredProvider(store, dynamic); err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{"provider": config})
	}
}

func automationPromptRevisionEvaluationHandler(
	store *AutomationProviderConfigStore,
	revisionID string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request AutomationPromptEvaluationRequest
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		evaluation, err := store.EvaluatePromptRevision(r.Context(), revisionID, request.CreatedBy)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, evaluation)
	}
}
