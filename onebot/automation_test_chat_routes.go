package main

import "net/http"

func registerAutomationTestChatRoutes(
	mux *http.ServeMux,
	service *AutomationService,
	provider *DynamicAutomationProvider,
	operatorToken string,
) {
	mux.HandleFunc(
		"/api/automation/chat",
		automationOperatorAuth(operatorToken, automationTestChatHandler(service, provider)),
	)
}

func automationTestChatHandler(
	service *AutomationService,
	provider AutomationProvider,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		if err := automationRejectUnknownQuery(r); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		var request AutomationTestChatRequest
		if err := automationDecodeObject(r, &request); err != nil {
			automationWriteError(w, http.StatusBadRequest, "invalid_json", err.Error())
			return
		}
		response, err := service.PreviewTestChat(r.Context(), provider, request)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		automationWriteJSON(w, http.StatusOK, response)
	}
}
