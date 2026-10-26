package main

import (
	"net/http"
	"strings"
)

func registerAutomationProviderConfigRoutes(
	mux *http.ServeMux,
	store *AutomationProviderConfigStore,
	provider *DynamicAutomationProvider,
	operatorToken string,
) {
	mux.HandleFunc("/api/automation/provider", automationOperatorAuth(operatorToken, automationProviderConfigHandler(store, provider)))
	mux.HandleFunc("/api/automation/provider/test", automationOperatorAuth(operatorToken, automationProviderTestHandler(store)))
	mux.HandleFunc("/api/automation/audiences", automationOperatorAuth(operatorToken, automationAudiencesHandler(store, provider)))
	mux.HandleFunc("/api/automation/audiences/", automationOperatorAuth(operatorToken, automationAudienceHandler(store, provider)))
	registerAutomationPromptRoutes(mux, store, provider, operatorToken)
}

func automationProviderConfigHandler(store *AutomationProviderConfigStore, dynamic *DynamicAutomationProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			automationWriteJSON(w, http.StatusOK, store.Public())
		case http.MethodPut:
			var request AutomationProviderConfigUpdate
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			result, err := store.Update(request)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			if err := automationSwapConfiguredProvider(store, dynamic); err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, result)
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
	}
}

func automationProviderTestHandler(store *AutomationProviderConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request AutomationProviderTestRequest
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		if err := store.Test(r.Context(), request); err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func automationAudiencesHandler(store *AutomationProviderConfigStore, dynamic *DynamicAutomationProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			config := store.Public()
			automationWriteJSON(w, http.StatusOK, map[string]any{
				"provider_version": config.Version,
				"audiences":        config.Audiences,
			})
		case http.MethodPost:
			var request AutomationAudienceCreate
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			audience, config, err := store.CreateAudience(request)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			if err := automationSwapConfiguredProvider(store, dynamic); err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusCreated, map[string]any{"audience": audience, "provider": config})
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func automationAudienceHandler(store *AutomationProviderConfigStore, dynamic *DynamicAutomationProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/automation/audiences/"))
		if id == "" || strings.Contains(id, "/") {
			automationWriteError(w, http.StatusNotFound, "not_found", "automation audience not found")
			return
		}
		switch r.Method {
		case http.MethodPatch:
			var request AutomationAudienceUpdate
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			audience, config, err := store.UpdateAudience(id, request)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			if err := automationSwapConfiguredProvider(store, dynamic); err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, map[string]any{"audience": audience, "provider": config})
		case http.MethodDelete:
			var request AutomationAudienceDelete
			if err := automationDecodeObject(r, &request); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			config, err := store.DeleteAudience(id, request)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			if err := automationSwapConfiguredProvider(store, dynamic); err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, map[string]any{"provider": config})
		default:
			automationWriteMethodNotAllowed(w, http.MethodPatch, http.MethodDelete)
		}
	}
}

func automationSwapConfiguredProvider(store *AutomationProviderConfigStore, dynamic *DynamicAutomationProvider) error {
	if dynamic == nil {
		return nil
	}
	provider, err := store.Provider()
	if err != nil {
		return err
	}
	dynamic.Swap(provider)
	return nil
}
