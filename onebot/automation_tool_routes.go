package main

import (
	"encoding/json"
	"net/http"
)

func automationToolsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(r); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		encoded, err := json.Marshal(WorkbenchToolCatalog())
		if err != nil {
			automationWriteError(w, http.StatusInternalServerError, "operation_failed", "tool catalog could not be encoded")
			return
		}
		automationWriteJSON(w, http.StatusOK, json.RawMessage(sanitizeAutomationJSON(encoded)))
	}
}
