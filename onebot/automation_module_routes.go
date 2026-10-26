package main

func registerAutomationModuleHTTPRoutes(context httpRouteRegistrationContext) {
	if context.automation == nil {
		return
	}
	registerAutomationRoutes(
		context.mux,
		context.automation.service,
		context.automation.operatorToken,
		context.automation.agentToken,
	)
}

func registerDataManagementModuleHTTPRoutes(context httpRouteRegistrationContext) {
	if context.automation == nil {
		return
	}
	registerDataManagementRoutes(
		context.mux,
		NewDataManagementService(
			context.store,
			context.automation.store,
			context.automation.providerConfigStore,
			context.automation.audit,
		),
		context.operatorToken(),
	)
}
