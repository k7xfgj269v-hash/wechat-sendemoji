package main

type openAIWireToolName string

const (
	openAIWireToolMessageClassify    openAIWireToolName = "message_classify"
	openAIWireToolCandidateCreate    openAIWireToolName = "candidate_create"
	openAIWireToolCandidateUpdate    openAIWireToolName = "candidate_update"
	openAIWireToolCandidateLink      openAIWireToolName = "candidate_link"
	openAIWireToolJobMatch           openAIWireToolName = "job_match"
	openAIWireToolJobCreate          openAIWireToolName = "job_create"
	openAIWireToolLeadCreate         openAIWireToolName = "lead_create"
	openAIWireToolLeadTransition     openAIWireToolName = "lead_transition"
	openAIWireToolTaskCreate         openAIWireToolName = "task_create"
	openAIWireToolNoteCreate         openAIWireToolName = "note_create"
	openAIWireToolJobUpdate          openAIWireToolName = "job_update"
	openAIWireToolOnboardingCreate   openAIWireToolName = "onboarding_create"
	openAIWireToolOnboardingUpdate   openAIWireToolName = "onboarding_update"
	openAIWireToolFleetUpdate        openAIWireToolName = "onboarding_fleet_update"
	openAIWireToolServiceCreate      openAIWireToolName = "service_request_create"
	openAIWireToolServiceUpdate      openAIWireToolName = "service_request_update"
	openAIWireToolFundCreate         openAIWireToolName = "fund_callback_create"
	openAIWireToolReplyPrivateText   openAIWireToolName = "reply_private_text"
	openAIWireToolReplyAudienceText  openAIWireToolName = "reply_audience_text"
	openAIWireToolForwardEvent       openAIWireToolName = "forward_event"
	openAIWireToolWorkbenchQuery     openAIWireToolName = "workbench_query"
	openAIWireToolWorkbenchMutate    openAIWireToolName = "workbench_mutate"
	openAIWireToolWorkbenchAllowOnce openAIWireToolName = "workbench_allow_once"
)

func automationOpenAIWireToolName(tool AutomationToolName) (openAIWireToolName, error) {
	switch tool {
	case AutomationToolMessageClassify:
		return openAIWireToolMessageClassify, nil
	case AutomationToolCandidateCreate:
		return openAIWireToolCandidateCreate, nil
	case AutomationToolCandidateUpdate:
		return openAIWireToolCandidateUpdate, nil
	case AutomationToolCandidateLink:
		return openAIWireToolCandidateLink, nil
	case AutomationToolJobMatch:
		return openAIWireToolJobMatch, nil
	case AutomationToolJobCreate:
		return openAIWireToolJobCreate, nil
	case AutomationToolLeadCreate:
		return openAIWireToolLeadCreate, nil
	case AutomationToolLeadTransition:
		return openAIWireToolLeadTransition, nil
	case AutomationToolTaskCreate:
		return openAIWireToolTaskCreate, nil
	case AutomationToolNoteCreate:
		return openAIWireToolNoteCreate, nil
	case AutomationToolJobUpdate:
		return openAIWireToolJobUpdate, nil
	case AutomationToolOnboardingCreate:
		return openAIWireToolOnboardingCreate, nil
	case AutomationToolOnboardingUpdate:
		return openAIWireToolOnboardingUpdate, nil
	case AutomationToolFleetUpdate:
		return openAIWireToolFleetUpdate, nil
	case AutomationToolServiceCreate:
		return openAIWireToolServiceCreate, nil
	case AutomationToolServiceUpdate:
		return openAIWireToolServiceUpdate, nil
	case AutomationToolFundCreate:
		return openAIWireToolFundCreate, nil
	case AutomationToolReplyPrivateText:
		return openAIWireToolReplyPrivateText, nil
	case AutomationToolReplyAudienceText:
		return openAIWireToolReplyAudienceText, nil
	case AutomationToolForwardEvent:
		return openAIWireToolForwardEvent, nil
	case AutomationToolWorkbenchQuery:
		return openAIWireToolWorkbenchQuery, nil
	case AutomationToolWorkbenchMutate:
		return openAIWireToolWorkbenchMutate, nil
	case AutomationToolWorkbenchAllowOnce:
		return openAIWireToolWorkbenchAllowOnce, nil
	default:
		return "", NewAutomationValidationError("tool", "has no OpenAI wire alias")
	}
}

func automationOpenAIInternalToolName(name openAIWireToolName) (AutomationToolName, error) {
	switch name {
	case openAIWireToolMessageClassify:
		return AutomationToolMessageClassify, nil
	case openAIWireToolCandidateCreate:
		return AutomationToolCandidateCreate, nil
	case openAIWireToolCandidateUpdate:
		return AutomationToolCandidateUpdate, nil
	case openAIWireToolCandidateLink:
		return AutomationToolCandidateLink, nil
	case openAIWireToolJobMatch:
		return AutomationToolJobMatch, nil
	case openAIWireToolJobCreate:
		return AutomationToolJobCreate, nil
	case openAIWireToolLeadCreate:
		return AutomationToolLeadCreate, nil
	case openAIWireToolLeadTransition:
		return AutomationToolLeadTransition, nil
	case openAIWireToolTaskCreate:
		return AutomationToolTaskCreate, nil
	case openAIWireToolNoteCreate:
		return AutomationToolNoteCreate, nil
	case openAIWireToolJobUpdate:
		return AutomationToolJobUpdate, nil
	case openAIWireToolOnboardingCreate:
		return AutomationToolOnboardingCreate, nil
	case openAIWireToolOnboardingUpdate:
		return AutomationToolOnboardingUpdate, nil
	case openAIWireToolFleetUpdate:
		return AutomationToolFleetUpdate, nil
	case openAIWireToolServiceCreate:
		return AutomationToolServiceCreate, nil
	case openAIWireToolServiceUpdate:
		return AutomationToolServiceUpdate, nil
	case openAIWireToolFundCreate:
		return AutomationToolFundCreate, nil
	case openAIWireToolReplyPrivateText:
		return AutomationToolReplyPrivateText, nil
	case openAIWireToolReplyAudienceText:
		return AutomationToolReplyAudienceText, nil
	case openAIWireToolForwardEvent:
		return AutomationToolForwardEvent, nil
	case openAIWireToolWorkbenchQuery:
		return AutomationToolWorkbenchQuery, nil
	case openAIWireToolWorkbenchMutate:
		return AutomationToolWorkbenchMutate, nil
	case openAIWireToolWorkbenchAllowOnce:
		return AutomationToolWorkbenchAllowOnce, nil
	default:
		return "", ErrOpenAIAutomationMalformedResponse
	}
}
