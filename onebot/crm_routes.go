package main

func registerCRMBaseHTTPRoutes(context httpRouteRegistrationContext) {
	registerCRMMessageRoutes(context.mux, context.store)
	registerCRMCandidateRoutes(context.mux, context.store)
	registerCRMActivityRoutes(context.mux, context.store)
	registerForwardRuleRoutes(context.mux, context.rules)
}

func registerCRMWorkflowHTTPRoutes(context httpRouteRegistrationContext) {
	operatorToken := context.operatorToken()
	registerCRMPipelineRoutes(context.mux, context.store, operatorToken)
	registerCRMOnboardingFleetRoutes(context.mux, context.store, operatorToken)
	registerCRMServiceRequestRoutes(context.mux, context.store, operatorToken)
	registerCRMOperationBatchRoutes(context.mux, context.store, operatorToken)
	registerCRMFundsGovernanceRoutes(context.mux, context.store, operatorToken)
	registerCRMRecruitingConfigRoutes(context.mux, context.store, operatorToken)
	registerCRMJobIngestionRoutes(context.mux, context.store, operatorToken)
}
