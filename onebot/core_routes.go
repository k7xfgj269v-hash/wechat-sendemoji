package main

func registerCoreHTTPRoutes(context httpRouteRegistrationContext) {
	registerWebRoutes(context.mux)
	if context.backend == httpRouteBackendAgent {
		registerAgentCoreHTTPRoutes(context)
		return
	}
	registerLegacyCoreHTTPRoutes(context)
}

func registerLegacyCoreHTTPRoutes(context httpRouteRegistrationContext) {
	context.mux.HandleFunc("/send_private_msg", sendHandler)
	context.mux.HandleFunc("/send_group_msg", sendHandler)
	context.mux.HandleFunc("/search_contact", searchContactHandler)
	context.mux.HandleFunc("/ws", handleWebSocket)
	context.mux.HandleFunc("/test_ws", testWebSocket)

	context.mux.HandleFunc("/api/contacts", contactsHandler(globalContactCache))
	context.mux.HandleFunc("/api/groups", groupsHandler(globalContactCache))
	context.mux.HandleFunc("/api/group_members", groupMembersHandler(globalContactCache))
	context.mux.HandleFunc("/api/contact_detail", contactDetailHandler(globalContactCache))
	context.mux.HandleFunc("/api/contacts/refresh", refreshContactsHandler(globalContactCache))
	registerFridaRoutes(context.mux, hookController, context.operatorToken())
}

func registerAgentCoreHTTPRoutes(context httpRouteRegistrationContext) {
	context.mux.HandleFunc("/send_private_msg", sendHandlerWithAgent(context.agent))
	context.mux.HandleFunc("/send_group_msg", sendHandlerWithAgent(context.agent))
	context.mux.HandleFunc("/search_contact", searchContactHandlerWithAgent(context.agent))
	context.mux.HandleFunc("/ws", handleWebSocketWithAgent(context.agent))
	context.mux.HandleFunc("/test_ws", testWebSocket)

	context.mux.HandleFunc("/api/contacts", contactsHandler(context.agent))
	context.mux.HandleFunc("/api/groups", groupsHandler(context.agent))
	context.mux.HandleFunc("/api/group_members", groupMembersHandler(context.agent))
	context.mux.HandleFunc("/api/contact_detail", contactDetailHandler(context.agent))
	context.mux.HandleFunc("/api/contacts/refresh", refreshContactsHandler(context.agent))
	registerAgentFridaRoutes(context.mux, context.agent, context.operatorToken())
	context.mux.HandleFunc(
		"/api/agent/events/status",
		hrWorkbenchAgentEventStatusHandler(context.subscription),
	)
}
