package main

import "net/http"

type httpRouteBackend uint8

const (
	httpRouteBackendLegacy httpRouteBackend = iota
	httpRouteBackendAgent
)

type httpRouteRegistrationContext struct {
	mux          *http.ServeMux
	store        *CRMStore
	rules        *ForwardRuleStore
	automation   *AutomationRuntime
	backend      httpRouteBackend
	agent        AgentClient
	subscription *HRWorkbenchInboundSubscription
}

func (c httpRouteRegistrationContext) operatorToken() string {
	if c.automation == nil {
		return ""
	}
	return c.automation.operatorToken
}

type httpRouteRegistrar struct {
	name     string
	register func(httpRouteRegistrationContext)
}

var httpRouteRegistrars = [...]httpRouteRegistrar{
	{name: "core", register: registerCoreHTTPRoutes},
	{name: "crm_base", register: registerCRMBaseHTTPRoutes},
	{name: "automation", register: registerAutomationModuleHTTPRoutes},
	{name: "stickers", register: registerStickerHTTPRoutes},
	{name: "send", register: registerSendHTTPRoutes},
	{name: "crm_workflows", register: registerCRMWorkflowHTTPRoutes},
	{name: "system", register: registerSystemHTTPRoutes},
	{name: "data_management", register: registerDataManagementModuleHTTPRoutes},
	{name: "mcp", register: func(context httpRouteRegistrationContext) { registerMCPRoutes(context.mux, context) }},
}

func registerHTTPRouteModules(context httpRouteRegistrationContext) {
	for _, registrar := range httpRouteRegistrars {
		registrar.register(context)
	}
}
