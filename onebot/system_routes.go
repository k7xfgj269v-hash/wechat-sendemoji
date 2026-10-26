package main

func registerSystemHTTPRoutes(context httpRouteRegistrationContext) {
	if context.backend == httpRouteBackendAgent {
		registerSystemRuntimeRoutes(context.mux, context.agent, context.operatorToken())
		return
	}
	registerSystemRuntimeRoutes(context.mux, nil, context.operatorToken())
}
