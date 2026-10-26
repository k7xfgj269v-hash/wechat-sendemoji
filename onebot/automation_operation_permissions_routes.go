package main

import "net/http"

func registerAutomationOperationPermissionRoutes(
	mux *http.ServeMux,
	store *AutomationOperationPermissionStore,
	operatorToken string,
) {
	mux.HandleFunc(
		"/api/automation/operation-permissions",
		automationOperatorAuth(operatorToken, automationOperationPermissionsHandler(store)),
	)
}

func automationOperationPermissionsHandler(store *AutomationOperationPermissionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			automationWriteError(w, http.StatusServiceUnavailable, "unavailable", "operation permissions are unavailable")
			return
		}
		switch r.Method {
		case http.MethodGet:
			automationWriteJSON(w, http.StatusOK, store.View())
		case http.MethodPut:
			var update AutomationOperationPermissionsUpdate
			if err := automationDecodeObject(r, &update); err != nil {
				automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
				return
			}
			view, err := store.Update(update)
			if err != nil {
				automationWriteServiceError(w, err)
				return
			}
			automationWriteJSON(w, http.StatusOK, view)
		default:
			automationWriteMethodNotAllowed(w, http.MethodGet, http.MethodPut)
		}
	}
}
