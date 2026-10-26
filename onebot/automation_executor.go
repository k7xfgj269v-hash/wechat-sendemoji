package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

type defaultAutomationCommandExecutor struct {
	automation *AutomationStore
	crm        *CRMStore
	sender     AutomationTextSender
	audiences  AutomationAudienceResolver
	audit      *AutomationAuditLedger
	agent      AgentClient
	rules      *ForwardRuleStore
}

func NewDefaultAutomationCommandExecutor(
	automation *AutomationStore,
	crm *CRMStore,
	sender AutomationTextSender,
	audiences ...AutomationAudienceResolver,
) AutomationCommandExecutor {
	executor := &defaultAutomationCommandExecutor{
		automation: automation,
		crm:        crm,
		sender:     sender,
	}
	if len(audiences) > 0 {
		executor.audiences = audiences[0]
	}
	executor.agent = currentHRWorkbenchAgentClient()
	executor.rules = forwardRuleStore
	return executor
}

func (e *defaultAutomationCommandExecutor) Execute(
	ctx context.Context,
	action AutomationAction,
) AutomationExecutionResult {
	if ctx == nil {
		return automationExecutionFailure("invalid_context", "context is required", false)
	}
	if e == nil || e.automation == nil || e.crm == nil {
		return automationExecutionFailure("executor_unavailable", "automation executor is unavailable", true)
	}
	if stale := e.checkPreconditions(action); stale != "" {
		return AutomationExecutionResult{
			Status:       AutomationActionStale,
			ErrorCode:    "stale_precondition",
			ErrorMessage: stale,
		}
	}
	if IsWorkbenchToolName(action.Tool) {
		result, _, err := e.executeWorkbenchAction(ctx, action)
		if err != nil {
			return automationExecutionFailure("result_invalid", err.Error(), false)
		}
		return result
	}

	arguments, err := DecodeAutomationToolArguments(action.Tool, action.Payload)
	if err != nil {
		return automationExecutionFailure("payload_invalid", err.Error(), false)
	}

	switch action.Tool {
	case AutomationToolMessageClassify, AutomationToolJobMatch:
		return AutomationExecutionResult{
			Status: AutomationActionSucceeded,
			Result: AutomationActionResult{},
		}

	case AutomationToolJobCreate:
		args := arguments.(JobCreateArgs)
		if action.Recipient != nil ||
			action.SourceMessage.EventID != args.EventID ||
			action.SourceMessage.ChatType != ChatTypeGroup ||
			action.SourceMessage.PeerID != args.GroupID {
			return automationExecutionFailure(
				"source_identity_invalid",
				"job source event and group must match the automation action source",
				false,
			)
		}
		result, err := e.crm.IngestSourceJob(SourceJobIngestion{
			EventID: args.EventID,
			GroupID: args.GroupID,
			Job:     args.Job,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(
			AutomationEntityJob,
			result.Job.ID,
			result.Job.Version,
		)

	case AutomationToolCandidateCreate:
		args := arguments.(CandidateCreateArgs)
		result, err := crmCandidateLinkMessage(ctx, e.crm, ManualCandidateExtractor{}, MessageCandidateLink{
			MessageEventID:       action.SourceMessage.EventID,
			ExpectedEventVersion: action.SourceMessage.ExpectedVersion,
			Candidate: &CandidateDraft{
				FullName: args.FullName,
				Phone:    args.Phone,
				Email:    args.Email,
				Location: args.Location,
				Skills:   append([]string{}, args.Skills...),
				Owner:    args.Owner,
				Summary:  args.Summary,
			},
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityCandidate, result.Candidate.ID, result.Candidate.Version)

	case AutomationToolCandidateLink:
		args := arguments.(CandidateLinkArgs)
		candidateID, _, err := e.resolveEntityRef(action, args.Candidate, AutomationEntityCandidate)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		result, err := crmCandidateLinkMessage(ctx, e.crm, ManualCandidateExtractor{}, MessageCandidateLink{
			MessageEventID:       action.SourceMessage.EventID,
			ExpectedEventVersion: action.SourceMessage.ExpectedVersion,
			CandidateID:          candidateID,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityCandidate, result.Candidate.ID, result.Candidate.Version)

	case AutomationToolCandidateUpdate:
		args := arguments.(CandidateUpdateArgs)
		candidateID, version, err := e.resolveEntityRef(action, args.Candidate, AutomationEntityCandidate)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		updated, err := crmCandidateUpdate(e.crm, candidateID, CandidateUpdate{
			ExpectedVersion: version,
			FullName:        args.Patch.FullName,
			WeChatID:        args.Patch.WeChatID,
			Phone:           args.Patch.Phone,
			Email:           args.Patch.Email,
			Location:        args.Patch.Location,
			Source:          args.Patch.Source,
			Skills:          args.Patch.Skills,
			Owner:           args.Patch.Owner,
			Status:          args.Patch.Status,
			Summary:         args.Patch.Summary,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityCandidate, updated.ID, updated.Version)

	case AutomationToolLeadCreate:
		args := arguments.(LeadCreateArgs)
		candidateID, _, err := e.resolveEntityRef(action, args.Candidate, AutomationEntityCandidate)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		jobID, _, err := e.resolveEntityRef(action, args.Job, AutomationEntityJob)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		lead, err := crmPipelineCreateLead(e.crm, LeadCreate{
			CandidateID: candidateID,
			JobID:       jobID,
			Owner:       args.Owner,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityLead, lead.ID, lead.Version)

	case AutomationToolLeadTransition:
		args := arguments.(LeadTransitionArgs)
		leadID, version, err := e.resolveEntityRef(action, args.Lead, AutomationEntityLead)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		lead, err := crmPipelineTransitionLead(e.crm, leadID, LeadTransition{
			ExpectedVersion: version,
			FromStage:       args.FromStage,
			ToStage:         args.ToStage,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityLead, lead.ID, lead.Version)

	case AutomationToolJobUpdate:
		args := arguments.(JobUpdateArgs)
		jobID, version, err := e.resolveEntityRef(action, args.Job, AutomationEntityJob)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		job, err := crmPipelineUpdateJob(e.crm, jobID, args.Patch.jobUpdate(version))
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityJob, job.ID, job.Version)

	case AutomationToolOnboardingCreate:
		args := arguments.(OnboardingCreateArgs)
		leadID, _, err := e.resolveEntityRef(action, args.Lead, AutomationEntityLead)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		onboarding, err := crmOnboardingCreate(e.crm, OnboardingCreate{
			LeadID:             leadID,
			EntryAt:            args.EntryAt,
			EntryAddress:       args.EntryAddress,
			SiteContact:        args.SiteContact,
			RequiredDocuments:  args.RequiredDocuments,
			CompletedDocuments: args.CompletedDocuments,
			MissingInformation: args.MissingInformation,
			Actor:              "automation",
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityOnboarding, onboarding.ID, onboarding.Version)

	case AutomationToolOnboardingUpdate:
		args := arguments.(OnboardingUpdateArgs)
		onboardingID, version, err := e.resolveEntityRef(action, args.Onboarding, AutomationEntityOnboarding)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		onboarding, err := crmOnboardingUpdate(e.crm, onboardingID, args.Patch.onboardingUpdate(version))
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityOnboarding, onboarding.ID, onboarding.Version)

	case AutomationToolFleetUpdate:
		args := arguments.(FleetUpdateArgs)
		onboardingID, version, err := e.resolveEntityRef(action, args.Onboarding, AutomationEntityOnboarding)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		onboarding, err := crmFleetUpdate(e.crm, onboardingID, args.Patch.fleetUpdate(version))
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityOnboarding, onboarding.ID, onboarding.Version)

	case AutomationToolServiceCreate:
		args := arguments.(ServiceRequestCreateArgs)
		candidateID, _, err := e.resolveEntityRef(action, args.Candidate, AutomationEntityCandidate)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		jobID, err := e.resolveOptionalEntityRef(action, args.Job, AutomationEntityJob)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		onboardingID, err := e.resolveOptionalEntityRef(action, args.Onboarding, AutomationEntityOnboarding)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		request, err := crmServiceRequestCreate(e.crm, ServiceRequestCreate{
			CandidateID:      candidateID,
			JobID:            jobID,
			OnboardingCaseID: onboardingID,
			SourceEventID:    action.SourceMessage.EventID,
			Category:         args.Category,
			Priority:         args.Priority,
			Description:      args.Description,
			Owner:            args.Owner,
			RequiresHuman:    args.RequiresHuman,
			DueAt:            args.DueAt,
			Actor:            "automation",
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityServiceRequest, request.ID, request.Version)

	case AutomationToolServiceUpdate:
		args := arguments.(ServiceRequestUpdateArgs)
		requestID, version, err := e.resolveEntityRef(action, args.ServiceRequest, AutomationEntityServiceRequest)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		candidateID, err := e.resolveOptionalEntityRefPointer(action, args.Patch.Candidate, AutomationEntityCandidate)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		jobID, err := e.resolveOptionalEntityRefPointer(action, args.Patch.Job, AutomationEntityJob)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		onboardingID, err := e.resolveOptionalEntityRefPointer(action, args.Patch.Onboarding, AutomationEntityOnboarding)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		update := args.Patch.serviceRequestUpdate(version, nil, nil, nil)
		update.CandidateID = candidateID
		update.JobID = jobID
		update.OnboardingCaseID = onboardingID
		request, err := crmServiceRequestUpdate(e.crm, requestID, update)
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityServiceRequest, request.ID, request.Version)

	case AutomationToolFundCreate:
		args := arguments.(FundCallbackCreateArgs)
		candidateID, _, err := e.resolveEntityRef(action, args.Candidate, AutomationEntityCandidate)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		jobID, err := e.resolveOptionalEntityRef(action, args.Job, AutomationEntityJob)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		leadID, err := e.resolveOptionalEntityRef(action, args.Lead, AutomationEntityLead)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		onboardingID, err := e.resolveOptionalEntityRef(action, args.Onboarding, AutomationEntityOnboarding)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		callback, err := crmFundCallbackCreate(e.crm, FundCallbackCreate{
			CandidateID:      candidateID,
			JobID:            jobID,
			LeadID:           leadID,
			OnboardingCaseID: onboardingID,
			AmountMinor:      args.AmountMinor,
			Currency:         args.Currency,
			Condition:        args.Condition,
			PromisedBy:       args.PromisedBy,
			DueAt:            args.DueAt,
			Evidence:         args.Evidence,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityFundCallback, callback.ID, callback.Version)

	case AutomationToolTaskCreate:
		args := arguments.(TaskCreateArgs)
		targetID, _, err := e.resolveEntityRef(action, args.Target, args.TargetType)
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		create := TaskCreate{
			Title:       args.Title,
			Description: args.Description,
			Owner:       args.Owner,
			DueAt:       args.DueAt,
		}
		if args.TargetType == AutomationEntityCandidate {
			create.CandidateID = targetID
		} else {
			create.LeadID = targetID
		}
		task, err := crmActivityNewService(e.crm).CreateTask(create)
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityTask, task.ID, task.Version)

	case AutomationToolNoteCreate:
		args := arguments.(NoteCreateArgs)
		targetID, _, err := e.resolveEntityRef(action, args.Target, string(args.TargetType))
		if err != nil {
			return automationDependencyExecutionError(err)
		}
		note, err := crmActivityNewService(e.crm).CreateNote(NoteCreate{
			TargetType: args.TargetType,
			TargetID:   targetID,
			Author:     args.Author,
			Body:       args.Body,
		})
		if err != nil {
			return automationCRMExecutionError(err)
		}
		return automationEntityExecutionSuccess(AutomationEntityNote, note.ID, 0)

	case AutomationToolReplyPrivateText:
		args := arguments.(ReplyPrivateTextArgs)
		if action.Recipient == nil ||
			action.Recipient.ChatType != ChatTypePrivate ||
			action.Recipient.PeerID != action.SourceMessage.PeerID {
			return automationExecutionFailure("recipient_invalid", "reply recipient must match the original private peer", false)
		}
		if e.sender == nil {
			return automationExecutionFailure("sender_unavailable", "private text sender is unavailable", true)
		}
		return e.executeTextReply(ctx, action, args.Text)

	case AutomationToolReplyAudienceText:
		args := arguments.(ReplyAudienceTextArgs)
		if action.Recipient == nil || action.Recipient.AudienceID != args.AudienceID ||
			e.audiences == nil {
			return automationExecutionFailure("recipient_invalid", "configured audience recipient is unavailable", false)
		}
		audience, ok := e.audiences.ResolveAutomationAudience(args.AudienceID)
		if !ok || !audience.Enabled || !containsAutomationIntent(audience.AllowedIntents, args.Intent) ||
			audience.Version != action.Recipient.AudienceVersion ||
			audience.ChatType != action.Recipient.ChatType ||
			audience.PeerID != action.Recipient.PeerID {
			return automationExecutionFailure("recipient_stale", "configured audience changed before execution", false)
		}
		return e.executeTextReply(ctx, action, args.Text)

	case AutomationToolForwardEvent:
		return e.executeForwardEvent(ctx, action, arguments.(ForwardEventArgs))
	}

	return automationExecutionFailure("tool_unsupported", "automation tool is not executable", false)
}

func (e *defaultAutomationCommandExecutor) executeWorkbenchAction(
	ctx context.Context,
	action AutomationAction,
) (AutomationExecutionResult, json.RawMessage, error) {
	if ctx == nil {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionFailed,
			WorkbenchErrorInvalidArguments,
			"invalid_context",
			"context is required",
			false,
		)
	}
	if e == nil || e.automation == nil || e.crm == nil {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionFailed,
			WorkbenchErrorUnavailable,
			"executor_unavailable",
			"automation executor is unavailable",
			true,
		)
	}
	if stale := e.checkPreconditions(action); stale != "" {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionStale,
			WorkbenchErrorConflict,
			"stale_precondition",
			stale,
			false,
		)
	}
	decoded, err := DecodeWorkbenchToolRequest(action.Tool, action.Payload)
	if err != nil {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionFailed,
			WorkbenchErrorInvalidArguments,
			"payload_invalid",
			err.Error(),
			false,
		)
	}
	if action.Operation != decoded.Request.Operation {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionFailed,
			WorkbenchErrorInvalidArguments,
			"operation_invalid",
			"persisted workbench operation does not match the request",
			false,
		)
	}
	execution, err := e.workbenchExecutionContext(action)
	if err != nil {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionFailed,
			WorkbenchErrorForbidden,
			"permission_invalid",
			err.Error(),
			false,
		)
	}
	definition, ok := WorkbenchOperationDefinitionFor(action.Tool, action.Operation)
	if !ok || !workbenchOperationEffectiveAvailable(definition) {
		return automationWorkbenchExecutionFailure(
			action.Operation,
			AutomationActionFailed,
			WorkbenchErrorUnavailable,
			string(WorkbenchErrorUnavailable),
			workbenchUnavailableMessage,
			false,
		)
	}

	var workbenchResult WorkbenchToolResult
	switch action.Tool {
	case AutomationToolWorkbenchQuery, AutomationToolWorkbenchMutate:
		workbenchResult = e.workbenchDispatcher().Dispatch(ctx, execution, decoded)
	case AutomationToolWorkbenchAllowOnce:
		switch action.Operation {
		case WorkbenchOperationMessagesSendPrivate, WorkbenchOperationMessagesSendGroup:
			arguments := decoded.Arguments.(*WorkbenchControlledSendArguments)
			if err := e.validateWorkbenchSendRecipient(action, arguments.EventID); err != nil {
				workbenchResult = NewWorkbenchToolFailure(
					action.Operation,
					WorkbenchErrorConflict,
					err.Error(),
					false,
				)
				break
			}
			delivery := e.executeTextReply(ctx, action, arguments.Text)
			workbenchResult = automationWorkbenchSendResult(action.Operation, delivery)
			raw, marshalErr := json.Marshal(workbenchResult)
			return delivery, raw, marshalErr
		default:
			workbenchResult = e.workbenchDispatcher().Dispatch(ctx, execution, decoded)
		}
	default:
		workbenchResult = NewWorkbenchToolFailure(
			action.Operation,
			WorkbenchErrorUnsupportedOperation,
			"automation tool is not executable",
			false,
		)
	}
	if err := ValidateWorkbenchToolResult(workbenchResult); err != nil {
		return AutomationExecutionResult{}, nil, err
	}
	raw, err := json.Marshal(workbenchResult)
	if err != nil {
		return AutomationExecutionResult{}, nil, err
	}
	return automationExecutionFromWorkbench(workbenchResult), raw, nil
}

func (e *defaultAutomationCommandExecutor) workbenchDispatcher() WorkbenchToolDispatcher {
	return NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{
		CRM:          e.crm,
		Agent:        e.agent,
		Automation:   e.automation,
		Audit:        e.audit,
		Audiences:    e.audiences,
		Sender:       e.sender,
		ForwardRules: e.rules,
	})
}

func (e *defaultAutomationCommandExecutor) persistMatchedPersistentOutboundPermission(
	action AutomationAction,
) (AutomationPermissionRequest, error) {
	var result AutomationPermissionRequest
	err := e.automation.Update(func(
		state *AutomationState,
		allocator *AutomationIDAllocator,
	) error {
		for _, request := range state.PermissionRequests {
			if request.ActionID != action.ID {
				continue
			}
			if result.ID != "" {
				return errors.New("workbench permission authorization is ambiguous")
			}
			result = request
		}
		if result.ID != "" {
			return nil
		}
		actionIndex := automationActionIndex(state, action.ID)
		if actionIndex < 0 {
			return errors.New("workbench action authorization target is missing")
		}
		current := &state.Actions[actionIndex]
		if current.Tool != AutomationToolWorkbenchAllowOnce ||
			current.Operation != action.Operation ||
			current.Status != AutomationActionExecuting {
			return errors.New("workbench action is not eligible for persistent outbound authorization")
		}
		effect, ruleID, matched := matchAutomationPermissionRule(state, *current)
		if !matched || effect != AutomationPermissionEffectAllow {
			return errors.New("enabled persistent outbound permission rule is required")
		}
		var rule *AutomationPermissionRule
		for i := range state.PermissionRules {
			if state.PermissionRules[i].ID == ruleID {
				rule = &state.PermissionRules[i]
				break
			}
		}
		if rule == nil || !rule.Enabled ||
			rule.Tool != current.Tool ||
			rule.Operation != current.Operation ||
			strings.TrimSpace(rule.CreatedBy) == "" {
			return errors.New("persistent outbound permission rule is invalid")
		}
		id, err := allocator.Next(AutomationIDKindPermissionRequest)
		if err != nil {
			return err
		}
		now := automationMutationTime(current.UpdatedAt)
		result = AutomationPermissionRequest{
			ID:         id,
			RunID:      current.RunID,
			ActionID:   current.ID,
			Tool:       current.Tool,
			Operation:  current.Operation,
			Risk:       current.Risk,
			Status:     AutomationPermissionStatusAllowed,
			Version:    1,
			Decision:   AutomationPermissionAllowPersistent,
			OperatorID: rule.CreatedBy,
			Reason:     "authorized by persistent outbound permission rule",
			RuleID:     rule.ID,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		state.PermissionRequests = append(state.PermissionRequests, result)
		return nil
	})
	return result, err
}

func (e *defaultAutomationCommandExecutor) workbenchExecutionContext(
	action AutomationAction,
) (WorkbenchExecutionContext, error) {
	var execution WorkbenchExecutionContext
	var permission *AutomationPermissionRequest
	if err := e.automation.Read(func(state *AutomationState) error {
		execution = WorkbenchExecutionContext{
			RunID:    action.RunID,
			CallID:   action.ProviderCallID,
			ActionID: action.ID,
		}
		for _, run := range state.Runs {
			if run.ID == action.RunID {
				execution.TurnID = run.TurnID
				break
			}
		}
		for i := range state.PermissionRequests {
			request := &state.PermissionRequests[i]
			if request.ActionID != action.ID {
				continue
			}
			if permission != nil {
				return errors.New("workbench permission authorization is ambiguous")
			}
			value := *request
			permission = &value
		}
		return nil
	}); err != nil {
		return WorkbenchExecutionContext{}, err
	}
	if action.Tool == AutomationToolWorkbenchAllowOnce {
		if permission == nil &&
			persistentHighRiskSendEnabled() &&
			isPersistentHighRiskSendOperation(action.Operation) {
			persisted, err := e.persistMatchedPersistentOutboundPermission(action)
			if err != nil {
				return WorkbenchExecutionContext{}, err
			}
			permission = &persisted
		}
		if permission == nil ||
			permission.Tool != action.Tool ||
			permission.Operation != action.Operation ||
			permission.Status != AutomationPermissionStatusAllowed ||
			strings.TrimSpace(permission.OperatorID) == "" ||
			permission.OperatorID == automationPermissionOperatorAuto {
			return WorkbenchExecutionContext{}, errors.New(
				"allow_once or enabled persistent outbound operator approval is required",
			)
		}
		switch permission.Decision {
		case AutomationPermissionAllowOnce:
			if permission.RuleID != "" {
				return WorkbenchExecutionContext{}, errors.New(
					"allow_once approval must not reference a reusable permission rule",
				)
			}
		case AutomationPermissionAllowPersistent:
			if !persistentHighRiskSendEnabled() ||
				!isPersistentHighRiskSendOperation(action.Operation) ||
				permission.RuleID == "" {
				return WorkbenchExecutionContext{}, errors.New(
					"persistent approval is limited to enabled high-risk outbound operations",
				)
			}
		default:
			return WorkbenchExecutionContext{}, errors.New(
				"allow_once or enabled persistent outbound operator approval is required",
			)
		}
		execution.PermissionRequestID = permission.ID
		execution.PermissionDecision = permission.Decision
		execution.OperatorID = permission.OperatorID
		return execution, nil
	}
	if permission != nil {
		if permission.Tool != action.Tool || permission.Operation != action.Operation {
			return WorkbenchExecutionContext{}, errors.New("workbench permission authorization is invalid")
		}
		if permission.Decision != "" {
			execution.PermissionRequestID = permission.ID
			execution.PermissionDecision = permission.Decision
			execution.OperatorID = permission.OperatorID
		}
	}
	return execution, nil
}

func (e *defaultAutomationCommandExecutor) validateWorkbenchSendRecipient(
	action AutomationAction,
	eventID string,
) error {
	if action.Recipient == nil || action.Recipient.AudienceID != eventID ||
		action.Recipient.AudienceVersion == 0 {
		return errors.New("controlled send recipient binding is invalid")
	}
	event, ok := automationFindMessageEvent(e.crm.Snapshot(), eventID)
	if !ok || event.ID != action.Recipient.AudienceID ||
		event.Version != action.Recipient.AudienceVersion {
		return errors.New("controlled send message event changed before execution")
	}
	expectedChatType := ChatTypePrivate
	peerID := event.PeerID
	if action.Operation == WorkbenchOperationMessagesSendGroup {
		expectedChatType = ChatTypeGroup
		if strings.TrimSpace(event.GroupID) != "" {
			peerID = event.GroupID
		}
	}
	if event.ChatType != expectedChatType ||
		action.Recipient.ChatType != expectedChatType ||
		action.Recipient.PeerID != peerID {
		return errors.New("controlled send message recipient changed before execution")
	}
	return nil
}

func (e *defaultAutomationCommandExecutor) executeTextReply(
	ctx context.Context,
	action AutomationAction,
	text string,
) AutomationExecutionResult {
	if action.Recipient == nil {
		return automationExecutionFailure("recipient_invalid", "reply recipient is required", false)
	}
	if e.sender == nil {
		return automationExecutionFailure("sender_unavailable", "text sender is unavailable", true)
	}
	entry, _, err := e.automation.EnsureText(
		ctx,
		action,
		action.Recipient.ChatType,
		action.Recipient.PeerID,
		text,
	)
	if err != nil {
		return automationExecutionFailure("outbox_failed", err.Error(), true)
	}
	claimed, ok, err := e.automation.ClaimPending(ctx, entry.ID)
	if err != nil {
		return automationExecutionFailure("outbox_claim_failed", err.Error(), true)
	}
	if !ok {
		return automationExecutionFromOutbox(claimed)
	}
	delivery := e.sender.SendText(ctx, claimed.ChatType, claimed.PeerID, claimed.Text)
	if delivery.DispatchAttempted {
		if _, err := e.automation.MarkDispatchAccepted(context.Background(), claimed.ID); err != nil {
			return AutomationExecutionResult{
				Status:       AutomationActionUnknown,
				ErrorCode:    "dispatch_acceptance_unknown",
				ErrorMessage: err.Error(),
			}
		}
	}
	completed, err := e.automation.CompleteDispatch(context.Background(), claimed.ID, delivery)
	if err != nil {
		return AutomationExecutionResult{
			Status:       AutomationActionUnknown,
			ErrorCode:    "dispatch_completion_unknown",
			ErrorMessage: err.Error(),
		}
	}
	return automationExecutionFromOutbox(completed)
}

func (e *defaultAutomationCommandExecutor) checkPreconditions(action AutomationAction) string {
	state := e.crm.Snapshot()
	event, ok := automationFindMessageEvent(state, action.SourceMessage.EventID)
	if ok {
		if event.MessageKey != action.SourceMessage.MessageKey {
			return "source message version changed"
		}
		if event.Version != action.SourceMessage.ExpectedVersion && len(action.DependsOnActionIDs) == 0 {
			return "source message version changed"
		}
	} else if !e.ownsSyntheticChatSource(action) {
		return "source message version changed"
	}
	for _, check := range action.Preconditions {
		version, ok := automationCRMEntityVersion(state, check.EntityType, check.EntityID)
		if !ok || version != check.ExpectedVersion {
			return check.EntityType + " " + check.EntityID + " version changed"
		}
	}
	return ""
}

func (e *defaultAutomationCommandExecutor) ownsSyntheticChatSource(action AutomationAction) bool {
	if e == nil || e.automation == nil || !IsWorkbenchToolName(action.Tool) {
		return false
	}
	owned := false
	_ = e.automation.Read(func(state *AutomationState) error {
		run, ok := automationRunByID(*state, action.RunID)
		if !ok {
			return nil
		}
		turn := automationTurnByID(state, run.TurnID)
		session := automationSessionByID(state, run.SessionID)
		if turn == nil || session == nil || session.Kind != AutomationSessionKindChat {
			return nil
		}
		sourceKey := "chat:" + session.ID + ":" + turn.ID
		owned = run.ID == action.RunID &&
			run.TurnID == turn.ID &&
			run.SessionID == session.ID &&
			turn.SessionID == session.ID &&
			run.SourceMessageEventID == turn.ID &&
			run.SourceMessageKey == sourceKey &&
			run.IdempotencyKey == sourceKey &&
			action.SourceMessage.EventID == run.SourceMessageEventID &&
			action.SourceMessage.MessageKey == run.SourceMessageKey &&
			action.SourceMessage.ExpectedVersion == run.SourceMessageVersion &&
			action.SourceMessage.ChatType == ChatTypePrivate &&
			action.SourceMessage.PeerID == "automation_chat"
		return nil
	})
	return owned
}

func (e *defaultAutomationCommandExecutor) resolveEntityRef(
	action AutomationAction,
	ref AutomationEntityRef,
	entityType string,
) (string, uint64, error) {
	if ref.ID != "" {
		return ref.ID, ref.ExpectedVersion, nil
	}
	var entityID string
	var entityVersion uint64
	err := e.automation.Read(func(state *AutomationState) error {
		for _, dependency := range state.Actions {
			if dependency.RunID != action.RunID || dependency.ProviderCallID != ref.FromCall {
				continue
			}
			if dependency.Status != AutomationActionSucceeded {
				return errors.New("dependency action has not succeeded")
			}
			var result AutomationActionResult
			if err := json.Unmarshal(dependency.Result, &result); err != nil ||
				result.Entity == nil ||
				result.Entity.EntityType != entityType {
				return errors.New("dependency result does not contain the expected entity")
			}
			entityID = result.Entity.EntityID
			entityVersion = result.Entity.Version
			return nil
		}
		return errors.New("dependency action was not found")
	})
	if err != nil {
		return "", 0, err
	}
	return entityID, entityVersion, nil
}

func (e *defaultAutomationCommandExecutor) resolveOptionalEntityRef(
	action AutomationAction,
	ref *AutomationEntityRef,
	entityType string,
) (string, error) {
	if ref == nil {
		return "", nil
	}
	id, _, err := e.resolveEntityRef(action, *ref, entityType)
	return id, err
}

func (e *defaultAutomationCommandExecutor) resolveOptionalEntityRefPointer(
	action AutomationAction,
	ref *AutomationEntityRef,
	entityType string,
) (*string, error) {
	if ref == nil {
		return nil, nil
	}
	id, _, err := e.resolveEntityRef(action, *ref, entityType)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func executeAndPersistAutomationAction(
	ctx context.Context,
	store *AutomationStore,
	executor AutomationCommandExecutor,
	actionID string,
) error {
	if store == nil || executor == nil {
		return NewAutomationValidationError("executor", "is unavailable")
	}
	var action AutomationAction
	err := store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		if state.Actions[index].Status != AutomationActionApproved {
			return NewAutomationValidationError("action", "is not approved")
		}
		state.Actions[index].Status = AutomationActionExecuting
		state.Actions[index].Version++
		state.Actions[index].UpdatedAt = automationMutationTime(state.Actions[index].UpdatedAt)
		action = state.Actions[index]
		return nil
	})
	if err != nil {
		return err
	}

	result, rawResult, err := executeAutomationActionForPersistence(ctx, executor, action)
	if err != nil {
		return err
	}
	return store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("automation action not found")
		}
		if state.Actions[index].Status != AutomationActionExecuting {
			return NewAutomationValidationError("action", "is no longer executing")
		}
		state.Actions[index].Status = result.Status
		state.Actions[index].Result = rawResult
		state.Actions[index].ErrorCode = strings.TrimSpace(result.ErrorCode)
		state.Actions[index].ErrorMessage = strings.TrimSpace(result.ErrorMessage)
		state.Actions[index].Retryable = result.Retryable
		state.Actions[index].Version++
		state.Actions[index].UpdatedAt = automationMutationTime(state.Actions[index].UpdatedAt)
		return nil
	})
}

func executeAutomationActionForPersistence(
	ctx context.Context,
	executor AutomationCommandExecutor,
	action AutomationAction,
) (AutomationExecutionResult, json.RawMessage, error) {
	if defaultExecutor, ok := executor.(*defaultAutomationCommandExecutor); ok &&
		IsWorkbenchToolName(action.Tool) {
		return defaultExecutor.executeWorkbenchAction(ctx, action)
	}
	result := executor.Execute(ctx, action)
	rawResult, err := automationMarshalExecutionResult(executor, action, result)
	return result, rawResult, err
}

func automationMarshalExecutionResult(
	executor AutomationCommandExecutor,
	action AutomationAction,
	result AutomationExecutionResult,
) ([]byte, error) {
	if action.Tool != AutomationToolJobCreate ||
		result.Status != AutomationActionSucceeded ||
		result.Result.Entity == nil {
		return json.Marshal(result.Result)
	}
	defaultExecutor, ok := executor.(*defaultAutomationCommandExecutor)
	if !ok || defaultExecutor == nil || defaultExecutor.crm == nil {
		return nil, errors.New("job.create result decision is unavailable")
	}
	entity := result.Result.Entity
	for _, provenance := range defaultExecutor.crm.Snapshot().JobProvenance {
		if provenance.EventID == action.SourceMessage.EventID &&
			provenance.JobID == entity.EntityID &&
			IsValidJobIngestionDecision(provenance.Decision) {
			return json.Marshal(AutomationJobCreateResult{
				Entity:   entity,
				Decision: provenance.Decision,
			})
		}
	}
	return nil, errors.New("job.create result decision is unavailable")
}

func automationCRMEntityVersion(state CRMState, entityType, id string) (uint64, bool) {
	switch entityType {
	case AutomationEntityCandidate:
		for _, item := range state.Candidates {
			if item.ID == id {
				return item.Version, true
			}
		}
	case AutomationEntityJob:
		for _, item := range state.Jobs {
			if item.ID == id {
				return item.Version, true
			}
		}
	case AutomationEntityLead:
		for _, item := range state.Leads {
			if item.ID == id {
				return item.Version, true
			}
		}
	case AutomationEntityTask:
		for _, item := range state.Tasks {
			if item.ID == id {
				return item.Version, true
			}
		}
	case AutomationEntityOnboarding:
		for _, item := range state.OnboardingCases {
			if item.ID == id {
				return item.Version, true
			}
		}
	case AutomationEntityServiceRequest:
		for _, item := range state.ServiceRequests {
			if item.ID == id {
				return item.Version, true
			}
		}
	case AutomationEntityFundCallback:
		for _, item := range state.FundCallbacks {
			if item.ID == id {
				return item.Version, true
			}
		}
	}
	return 0, false
}

func automationEntityExecutionSuccess(entityType, id string, version uint64) AutomationExecutionResult {
	return AutomationExecutionResult{
		Status: AutomationActionSucceeded,
		Result: AutomationActionResult{
			Entity: &AutomationEntityResult{
				EntityType: entityType,
				EntityID:   id,
				Version:    version,
			},
		},
	}
}

func automationExecutionFailure(code, message string, retryable bool) AutomationExecutionResult {
	return AutomationExecutionResult{
		Status:       AutomationActionFailed,
		ErrorCode:    code,
		ErrorMessage: message,
		Retryable:    retryable,
	}
}

func automationExecutionFromWorkbench(result WorkbenchToolResult) AutomationExecutionResult {
	if result.OK {
		return AutomationExecutionResult{Status: AutomationActionSucceeded}
	}
	execution := automationExecutionFailure(
		string(result.Error.Code),
		result.Error.Message,
		result.Error.Retryable,
	)
	if result.Error.Code == WorkbenchErrorConflict {
		execution.Status = AutomationActionStale
	}
	return execution
}

func automationWorkbenchSendResult(
	operation WorkbenchOperation,
	execution AutomationExecutionResult,
) WorkbenchToolResult {
	if execution.Status == AutomationActionSucceeded {
		return NewWorkbenchToolSuccess(operation, execution.Result.Outbox, nil)
	}
	code := WorkbenchErrorInternal
	if execution.Retryable {
		code = WorkbenchErrorUnavailable
	}
	message := strings.TrimSpace(execution.ErrorMessage)
	if message == "" {
		message = "controlled send failed"
	}
	return NewWorkbenchToolFailure(operation, code, message, execution.Retryable)
}

func automationWorkbenchExecutionFailure(
	operation WorkbenchOperation,
	status AutomationActionStatus,
	workbenchCode WorkbenchErrorCode,
	errorCode string,
	message string,
	retryable bool,
) (AutomationExecutionResult, json.RawMessage, error) {
	workbenchResult := NewWorkbenchToolFailure(operation, workbenchCode, message, retryable)
	if err := ValidateWorkbenchToolResult(workbenchResult); err != nil {
		return AutomationExecutionResult{}, nil, err
	}
	raw, err := json.Marshal(workbenchResult)
	if err != nil {
		return AutomationExecutionResult{}, nil, err
	}
	return AutomationExecutionResult{
		Status:       status,
		ErrorCode:    errorCode,
		ErrorMessage: message,
		Retryable:    retryable,
	}, raw, nil
}

func automationCRMExecutionError(err error) AutomationExecutionResult {
	if errors.Is(err, ErrCRMConflict) {
		return AutomationExecutionResult{
			Status:       AutomationActionStale,
			ErrorCode:    "crm_conflict",
			ErrorMessage: err.Error(),
		}
	}
	return automationExecutionFailure("crm_failed", err.Error(), false)
}

func automationDependencyExecutionError(err error) AutomationExecutionResult {
	return AutomationExecutionResult{
		Status:       AutomationActionStale,
		ErrorCode:    "dependency_unavailable",
		ErrorMessage: err.Error(),
	}
}

func automationExecutionFromOutbox(entry AutomationOutboxEntry) AutomationExecutionResult {
	result := AutomationActionResult{
		Outbox: &AutomationOutboxResult{
			OutboxID: entry.ID,
			Status:   entry.Status,
		},
	}
	switch entry.Status {
	case AutomationOutboxSent:
		return AutomationExecutionResult{Status: AutomationActionSucceeded, Result: result}
	case AutomationOutboxFailed:
		return AutomationExecutionResult{
			Status:       AutomationActionFailed,
			Result:       result,
			ErrorCode:    entry.ErrorCode,
			ErrorMessage: entry.ErrorMessage,
			Retryable:    !entry.DispatchAttempted,
		}
	default:
		return AutomationExecutionResult{
			Status:       AutomationActionUnknown,
			Result:       result,
			ErrorCode:    entry.ErrorCode,
			ErrorMessage: entry.ErrorMessage,
		}
	}
}

func automationActionIndex(state *AutomationState, id string) int {
	for i := range state.Actions {
		if state.Actions[i].ID == id {
			return i
		}
	}
	return -1
}

func automationMutationTime(previous time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(previous) {
		return previous.Add(time.Nanosecond)
	}
	return now
}
