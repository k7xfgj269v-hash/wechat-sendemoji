package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	errWorkbenchRecruitingNoMutation               = errors.New("workbench recruiting no mutation")
	errWorkbenchRecruitingUnsafeData               = errors.New("workbench recruiting projection is unsafe")
	errWorkbenchRecruitingAudienceBindingMissing   = errors.New("approved audience recipient binding is missing")
	errWorkbenchRecruitingAudienceVersionChanged   = errors.New("approved audience version changed")
	errWorkbenchRecruitingAudienceRecipientChanged = errors.New("approved audience recipient changed")
)

const (
	workbenchRecruitingBridgeAudience = "m3-recruiting-audience"
	workbenchRecruitingBridgeForward  = "m3-recruiting-forward"
	workbenchRecruitingBridgeDelete   = "m3-recruiting-delete"
)

type workbenchRecruitingProjectProjection struct {
	OrganizationName        string `json:"organization_name"`
	Version                 uint64 `json:"version"`
	RequireAuthoritativeJob bool   `json:"require_authoritative_job"`
	SourceGroupCount        int    `json:"source_group_count"`
}

type workbenchRecruitingSourceListData struct {
	Sources []RecruitingSourceGroup `json:"sources"`
}

type workbenchRecruitingSourceMutationResult struct {
	ProjectVersion uint64                `json:"project_version"`
	Source         RecruitingSourceGroup `json:"source"`
}

type workbenchRecruitingDeleteResult struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
	Version uint64 `json:"version,omitempty"`
}

type workbenchRecruitingForwardRuleListData struct {
	Rules []ForwardRule `json:"rules"`
}

type workbenchRecruitingAudienceSendResult struct {
	AudienceID      string                 `json:"audience_id"`
	AudienceVersion uint64                 `json:"audience_version"`
	OutboxID        string                 `json:"outbox_id"`
	Status          AutomationOutboxStatus `json:"status"`
}

type workbenchRecruitingForwardSendResult struct {
	EventID   string `json:"event_id"`
	RuleID    string `json:"rule_id"`
	Forwarded bool   `json:"forwarded"`
}

type workbenchRecruitingBridgeSpec struct {
	Kind       string
	Tool       AutomationToolName
	Operation  WorkbenchOperation
	Payload    json.RawMessage
	Recipient  *AutomationRecipient
	Audience   *AutomationAudience
	RiskReason string
}

func init() {
	bindWorkbenchOperationHandlers("m3-recruiting-forwarding",
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationRecruitingProjectGet,
			Handler:   workbenchRecruitingProjectGet,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationRecruitingSourcesList,
			Handler:   workbenchRecruitingSourcesList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationForwardRulesList,
			Handler:   workbenchRecruitingForwardRulesList,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationRecruitingProjectUpdate,
			Handler:   workbenchRecruitingProjectUpdate,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationRecruitingSourcesUpsert,
			Handler:   workbenchRecruitingSourcesUpsert,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationForwardRulesCreate,
			Handler:   workbenchRecruitingForwardRulesCreate,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationForwardRulesUpdate,
			Handler:   workbenchRecruitingForwardRulesUpdate,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationRecruitingSourcesDelete,
			Handler:   workbenchRecruitingSourcesDelete,
		},
		WorkbenchOperationBinding{
			Operation: WorkbenchOperationForwardRulesDelete,
			Handler:   workbenchRecruitingForwardRulesDelete,
		},
		WorkbenchOperationBinding{
			Operation:      WorkbenchOperationMessagesSendAudience,
			Handler:        workbenchRecruitingSendAudience,
			PolicyResolver: workbenchRecruitingAudiencePolicyResolver,
		},
		WorkbenchOperationBinding{
			Operation:      WorkbenchOperationForwardingSendEvent,
			Handler:        workbenchRecruitingForwardEvent,
			PolicyResolver: workbenchRecruitingForwardPolicyResolver,
		},
	)
	workbenchRecruitingSanitizeCatalogDefinition()
}

func workbenchRecruitingSanitizeCatalogDefinition() {
	workbenchOperationRegistry.mu.Lock()
	defer workbenchOperationRegistry.mu.Unlock()
	if workbenchOperationRegistry.finalizing || workbenchOperationRegistry.finalized {
		panic("M3-04 catalog metadata registered after finalization")
	}
	updated := false
	for i := range workbenchOperationRegistry.declarations {
		definition := &workbenchOperationRegistry.declarations[i].definition
		if definition.Operation != WorkbenchOperationRecruitingProjectUpdate {
			continue
		}
		definition.Description = "Update approved recruiting project fields."
		updated = true
	}
	if !updated {
		panic("M3-04 recruiting project definition is unavailable")
	}
}

func workbenchRecruitingProjectGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	_ any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRecruitingProjectGet
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	projection, err := workbenchRecruitingProjectResult(dispatcher.crm.RecruitingProject())
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, projection, nil)
}

func workbenchRecruitingSourcesList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRecruitingSourcesList
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchPageArguments)
	sources := append([]RecruitingSourceGroup(nil), dispatcher.crm.RecruitingProject().SourceGroups...)
	sort.SliceStable(sources, func(i, j int) bool {
		return sources[i].ID < sources[j].ID
	})
	for _, source := range sources {
		if !workbenchRecruitingSafeSource(source) {
			return workbenchRecruitingFailure(operation, errWorkbenchRecruitingUnsafeData)
		}
	}
	total := len(sources)
	sources = workbenchRecruitingPage(sources, arguments.Limit, arguments.Offset)
	return workbenchRecruitingBoundedPage(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		sources,
		func(page []RecruitingSourceGroup) any {
			return workbenchRecruitingSourceListData{Sources: page}
		},
	)
}

func workbenchRecruitingForwardRulesList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationForwardRulesList
	if unavailable := workbenchRecruitingRequireForwardRules(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchPageArguments)
	rules := dispatcher.forwardRules.List()
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].ID < rules[j].ID
	})
	for _, rule := range rules {
		if !workbenchRecruitingSafeForwardRule(rule) {
			return workbenchRecruitingFailure(operation, errWorkbenchRecruitingUnsafeData)
		}
	}
	total := len(rules)
	rules = workbenchRecruitingPage(rules, arguments.Limit, arguments.Offset)
	return workbenchRecruitingBoundedPage(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		rules,
		func(page []ForwardRule) any {
			return workbenchRecruitingForwardRuleListData{Rules: page}
		},
	)
}

func workbenchRecruitingProjectUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRecruitingProjectUpdate
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchRecruitingProjectUpdateArguments)
	project, err := dispatcher.crm.UpdateRecruitingProject(
		arguments.ExpectedVersion,
		arguments.Update,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	projection, err := workbenchRecruitingProjectResult(project)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, projection, nil)
}

func workbenchRecruitingSourcesUpsert(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRecruitingSourcesUpsert
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchRecruitingSourceUpsertArguments)
	project, err := dispatcher.crm.UpsertRecruitingSourceGroup(
		arguments.ExpectedProjectVersion,
		arguments.Source,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	source, found := workbenchRecruitingFindSource(project.SourceGroups, arguments.Source)
	if !found || !workbenchRecruitingSafeSource(source) {
		return workbenchRecruitingFailure(operation, errWorkbenchRecruitingUnsafeData)
	}
	return NewWorkbenchToolSuccess(operation, workbenchRecruitingSourceMutationResult{
		ProjectVersion: project.Version,
		Source:         source,
	}, nil)
}

func workbenchRecruitingForwardRulesCreate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationForwardRulesCreate
	if unavailable := workbenchRecruitingRequireForwardRules(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchForwardRuleCreateArguments)
	if err := workbenchRecruitingValidateForwardTarget(arguments.GroupID); err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if !workbenchRecruitingSafeKeywords(arguments.Keywords) {
		return workbenchRecruitingFailure(
			operation,
			NewCRMValidationError("keywords", "contains unsafe text"),
		)
	}
	rule, err := dispatcher.forwardRules.Create(
		arguments.GroupID,
		arguments.Keywords,
		arguments.CooldownSeconds,
		arguments.Enabled,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if !workbenchRecruitingSafeForwardRule(rule) {
		return workbenchRecruitingFailure(operation, errWorkbenchRecruitingUnsafeData)
	}
	return NewWorkbenchToolSuccess(operation, rule, nil)
}

func workbenchRecruitingForwardRulesUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationForwardRulesUpdate
	if unavailable := workbenchRecruitingRequireForwardRules(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchForwardRuleUpdateArguments)
	if arguments.Keywords != nil && !workbenchRecruitingSafeKeywords(*arguments.Keywords) {
		return workbenchRecruitingFailure(
			operation,
			NewCRMValidationError("keywords", "contains unsafe text"),
		)
	}
	rule, found, err := dispatcher.forwardRules.Update(arguments.RuleID, updateForwardRuleRequest{
		Keywords:        arguments.Keywords,
		CooldownSeconds: arguments.CooldownSeconds,
		Enabled:         arguments.Enabled,
	})
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if !found {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorNotFound, "forward rule was not found", false)
	}
	if !workbenchRecruitingSafeForwardRule(rule) {
		return workbenchRecruitingFailure(operation, errWorkbenchRecruitingUnsafeData)
	}
	return NewWorkbenchToolSuccess(operation, rule, nil)
}

func workbenchRecruitingSourcesDelete(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRecruitingSourcesDelete
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchRecruitingSourceDeleteArguments)
	return workbenchRecruitingExecuteDelete(
		ctx,
		dispatcher,
		execution,
		operation,
		arguments,
		func() (any, error) {
			project, err := dispatcher.crm.RemoveRecruitingSourceGroup(
				arguments.GroupID,
				arguments.ExpectedProjectVersion,
			)
			if err != nil {
				return nil, err
			}
			return workbenchRecruitingDeleteResult{
				ID:      arguments.GroupID,
				Deleted: true,
				Version: project.Version,
			}, nil
		},
	)
}

func workbenchRecruitingForwardRulesDelete(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationForwardRulesDelete
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	if unavailable := workbenchRecruitingRequireForwardRules(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchIDArguments)
	return workbenchRecruitingExecuteDelete(
		ctx,
		dispatcher,
		execution,
		operation,
		arguments,
		func() (any, error) {
			deleted, err := dispatcher.forwardRules.forwardRuleDelete(arguments.ID)
			if err != nil {
				return nil, err
			}
			if !deleted {
				return nil, errForwardRuleNotFound
			}
			return workbenchRecruitingDeleteResult{ID: arguments.ID, Deleted: true}, nil
		},
	)
}

func workbenchRecruitingSendAudience(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationMessagesSendAudience
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	if unavailable := workbenchRecruitingRequireOutbound(dispatcher, operation, true); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchAudienceSendArguments)
	parent, err := workbenchRecruitingApprovedParent(
		dispatcher.automation,
		execution,
		operation,
		arguments,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	return workbenchRecruitingSendApprovedAudience(
		ctx,
		dispatcher,
		execution,
		parent,
		arguments,
	)
}

func workbenchRecruitingSendApprovedAudience(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	parent AutomationAction,
	arguments *WorkbenchAudienceSendArguments,
) WorkbenchToolResult {
	operation := WorkbenchOperationMessagesSendAudience
	payload, err := json.Marshal(ReplyAudienceTextArgs{
		AudienceID: arguments.AudienceID,
		Intent:     arguments.Intent,
		Text:       arguments.Text,
	})
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if replay := workbenchRecruitingAudienceReplay(
		dispatcher.automation,
		parent.ID,
		payload,
	); replay != nil {
		return *replay
	}
	audience, err := workbenchRecruitingResolveAudience(
		dispatcher.audiences,
		arguments.AudienceID,
		arguments.Intent,
		arguments.Text,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	approvedRecipient, err := workbenchRecruitingApprovedAudienceRecipient(parent, audience)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if err := ctx.Err(); err != nil {
		return workbenchRecruitingFailure(operation, err)
	}

	bridge, _, err := workbenchRecruitingEnsureBridgeAction(
		ctx,
		dispatcher.automation,
		parent,
		execution,
		workbenchRecruitingBridgeSpec{
			Kind:       workbenchRecruitingBridgeAudience,
			Tool:       AutomationToolReplyAudienceText,
			Payload:    payload,
			Recipient:  approvedRecipient,
			Audience:   &audience,
			RiskReason: automationReasonR3BulkOutreach,
		},
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	bridge, claimed, err := workbenchRecruitingClaimBridgeAction(
		dispatcher.automation,
		bridge.ID,
		execution.OperatorID,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if !claimed {
		return workbenchRecruitingAudienceBridgeResult(dispatcher.automation, bridge)
	}

	executor := &defaultAutomationCommandExecutor{
		automation: dispatcher.automation,
		crm:        dispatcher.crm,
		sender:     dispatcher.sender,
		audiences:  dispatcher.audiences,
		audit:      dispatcher.audit,
		rules:      dispatcher.forwardRules,
	}
	delivery := executor.executeTextReply(ctx, bridge, arguments.Text)
	delivery = workbenchRecruitingContextExecution(ctx, delivery)
	delivery = workbenchRecruitingPublicExecution(delivery, "controlled audience send failed")
	bridge, finishErr := workbenchRecruitingFinishBridgeAction(
		dispatcher.automation,
		bridge.ID,
		delivery,
		execution.OperatorID,
	)
	if finishErr != nil {
		return workbenchRecruitingFailure(operation, finishErr)
	}
	return workbenchRecruitingAudienceBridgeResult(dispatcher.automation, bridge)
}

func workbenchRecruitingExecuteDelete(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	operation WorkbenchOperation,
	approvedArguments any,
	mutate func() (any, error),
) WorkbenchToolResult {
	if dispatcher == nil || dispatcher.automation == nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"automation store is unavailable",
			true,
		)
	}
	parent, err := workbenchRecruitingApprovedParent(
		dispatcher.automation,
		execution,
		operation,
		approvedArguments,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	bridge, _, err := workbenchRecruitingEnsureBridgeAction(
		ctx,
		dispatcher.automation,
		parent,
		execution,
		workbenchRecruitingBridgeSpec{
			Kind:       workbenchRecruitingBridgeDelete,
			Tool:       AutomationToolWorkbenchAllowOnce,
			Operation:  operation,
			Payload:    append(json.RawMessage(nil), parent.Payload...),
			RiskReason: automationReasonR3WorkbenchAllowOnce,
		},
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	bridge, claimed, err := workbenchRecruitingClaimBridgeAction(
		dispatcher.automation,
		bridge.ID,
		execution.OperatorID,
	)
	if err != nil {
		return workbenchRecruitingFailure(operation, err)
	}
	if !claimed {
		return workbenchRecruitingDeleteBridgeResult(operation, bridge)
	}
	data, mutationErr := mutate()
	result := AutomationExecutionResult{Status: AutomationActionSucceeded}
	if mutationErr != nil {
		result = workbenchRecruitingExecutionFromError(mutationErr)
	}
	result = workbenchRecruitingPublicExecution(result, "approved delete failed")
	var persistedResult json.RawMessage
	if data != nil {
		encoded, encodeErr := json.Marshal(data)
		if encodeErr != nil {
			result = automationExecutionFailure("result_invalid", "approved delete failed", false)
		} else {
			persistedResult = encoded
		}
	}
	if len(persistedResult) == 0 {
		encoded, encodeErr := json.Marshal(result.Result)
		if encodeErr != nil {
			return workbenchRecruitingFailure(operation, encodeErr)
		}
		persistedResult = encoded
	}
	bridge, finishErr := workbenchRecruitingFinishBridgeActionWithResult(
		dispatcher.automation,
		bridge.ID,
		result,
		persistedResult,
		execution.OperatorID,
	)
	if finishErr != nil {
		return workbenchRecruitingFailure(operation, finishErr)
	}
	return workbenchRecruitingDeleteBridgeResult(operation, bridge)
}

func workbenchRecruitingExecutionCode(action AutomationAction) WorkbenchErrorCode {
	if action.Status == AutomationActionFailed {
		switch strings.TrimSpace(action.ErrorCode) {
		case "cancelled", "context_canceled":
			return WorkbenchErrorCancelled
		case "deadline_exceeded":
			return WorkbenchErrorDeadlineExceeded
		}
	}
	switch action.Status {
	case AutomationActionStale:
		return WorkbenchErrorConflict
	case AutomationActionUnknown:
		return WorkbenchErrorConflict
	default:
		if action.Retryable || workbenchRecruitingUnavailableExecutionCode(action.ErrorCode) {
			return WorkbenchErrorUnavailable
		}
		return WorkbenchErrorInternal
	}
}

func workbenchRecruitingUnavailableExecutionCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "authorization_unavailable", "sender_unavailable":
		return true
	default:
		return false
	}
}

func workbenchRecruitingExecutionFromError(err error) AutomationExecutionResult {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return automationExecutionFailure("deadline_exceeded", "approved delete failed", false)
	case errors.Is(err, context.Canceled):
		return automationExecutionFailure("cancelled", "approved delete failed", false)
	case errors.Is(err, ErrCRMConflict):
		return AutomationExecutionResult{
			Status:       AutomationActionStale,
			ErrorCode:    "conflict",
			ErrorMessage: "approved delete failed",
		}
	case errors.Is(err, ErrCRMStoreClosed), errors.Is(err, ErrCRMPersistence),
		errors.Is(err, ErrAutomationStoreClosed), errors.Is(err, ErrAutomationPersistence):
		return automationExecutionFailure("dependency_unavailable", "approved delete failed", true)
	case workbenchRecruitingNotFound(err):
		return automationExecutionFailure("not_found", "approved delete failed", false)
	default:
		return automationExecutionFailure("delete_failed", "approved delete failed", false)
	}
}

func workbenchRecruitingProjectResult(
	project RecruitingProjectConfig,
) (workbenchRecruitingProjectProjection, error) {
	if workbenchRecruitingUnsafeValue(project.OrganizationName) {
		return workbenchRecruitingProjectProjection{}, errWorkbenchRecruitingUnsafeData
	}
	for _, source := range project.SourceGroups {
		if !workbenchRecruitingSafeSource(source) {
			return workbenchRecruitingProjectProjection{}, errWorkbenchRecruitingUnsafeData
		}
	}
	return workbenchRecruitingProjectProjection{
		OrganizationName:        project.OrganizationName,
		Version:                 project.Version,
		RequireAuthoritativeJob: project.RequireAuthoritativeJob,
		SourceGroupCount:        len(project.SourceGroups),
	}, nil
}

func workbenchRecruitingFindSource(
	sources []RecruitingSourceGroup,
	input RecruitingSourceGroupUpsert,
) (RecruitingSourceGroup, bool) {
	for _, source := range sources {
		if input.ID != "" && source.ID == input.ID {
			return source, true
		}
		if input.ID == "" && source.GroupID == strings.TrimSpace(input.GroupID) {
			return source, true
		}
	}
	return RecruitingSourceGroup{}, false
}

func workbenchRecruitingFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return workbenchContextFailure(operation, context.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return workbenchContextFailure(operation, context.Canceled)
	case workbenchRecruitingNotFound(err):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorNotFound, "requested recruiting resource was not found", false)
	case errors.Is(err, ErrCRMConflict),
		errors.Is(err, ErrAutomationOutboxConflict),
		errors.Is(err, errForwardRuleCooldown),
		errors.Is(err, errWorkbenchRecruitingAudienceVersionChanged):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorConflict, "operation conflicts with current state", false)
	case errors.Is(err, errWorkbenchRecruitingAudienceRecipientChanged):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorForbidden, "approved audience recipient changed", false)
	case errors.Is(err, errWorkbenchRecruitingAudienceBindingMissing):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorForbidden, "approved audience recipient binding is unavailable", false)
	case errors.Is(err, ErrCRMValidation), errors.Is(err, ErrAutomationValidation),
		errors.Is(err, errForwardRuleDisabled), errors.Is(err, errForwardRuleMismatch):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInvalidArguments, "operation arguments are invalid", false)
	case errors.Is(err, ErrCRMStoreClosed), errors.Is(err, ErrCRMPersistence),
		errors.Is(err, ErrAutomationStoreClosed), errors.Is(err, ErrAutomationPersistence):
		return NewWorkbenchToolFailure(operation, WorkbenchErrorUnavailable, "operation dependency is unavailable", true)
	default:
		return NewWorkbenchToolFailure(operation, WorkbenchErrorInternal, "recruiting operation failed", false)
	}
}

func workbenchRecruitingNotFound(err error) bool {
	var source *recruitingSourceGroupNotFoundError
	return errors.As(err, &source) ||
		errors.Is(err, errForwardRuleNotFound) ||
		errors.Is(err, ErrAutomationOutboxNotFound)
}

func workbenchRecruitingRequireForwardRules(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) *WorkbenchToolResult {
	if dispatcher != nil && dispatcher.forwardRules != nil {
		return nil
	}
	result := NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorUnavailable,
		"forward rule store is unavailable",
		true,
	)
	return &result
}

func workbenchRecruitingRequireOutbound(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
	requireSender bool,
) *WorkbenchToolResult {
	if dispatcher == nil ||
		dispatcher.crm == nil ||
		dispatcher.automation == nil ||
		dispatcher.forwardRules == nil && operation == WorkbenchOperationForwardingSendEvent ||
		dispatcher.audiences == nil && operation == WorkbenchOperationMessagesSendAudience ||
		requireSender && workbenchRecruitingSenderUnavailable(dispatcher.sender) {
		result := NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorUnavailable,
			"controlled outbound dependency is unavailable",
			true,
		)
		return &result
	}
	return nil
}

func workbenchRecruitingSenderUnavailable(sender AutomationTextSender) bool {
	if sender == nil {
		return true
	}
	agentSender, ok := sender.(*agentAutomationTextSender)
	if !ok {
		return false
	}
	if agentSender == nil {
		return true
	}
	if agentSender.client != nil {
		return false
	}
	if agentSender.clientProvider == nil {
		return true
	}
	return agentSender.clientProvider() == nil
}

func workbenchRecruitingBoundedPage[T any](
	operation WorkbenchOperation,
	limit int,
	offset int,
	total int,
	items []T,
	project func([]T) any,
) WorkbenchToolResult {
	for returned := len(items); returned >= 0; returned-- {
		result := NewWorkbenchToolSuccess(
			operation,
			project(items[:returned]),
			newWorkbenchPage(limit, offset, total, returned),
		)
		if result.OK {
			return result
		}
	}
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorInternal,
		"result could not be bounded",
		false,
	)
}

func workbenchRecruitingPage[T any](items []T, limit int, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return append([]T(nil), items[offset:end]...)
}

func workbenchRecruitingValidateForwardTarget(groupID string) error {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return nil
	}
	_, err := normalizeRecruitingGroupID(groupID)
	return err
}

func workbenchRecruitingValidateAudienceRecipient(audience AutomationAudience) error {
	peerID := strings.TrimSpace(audience.PeerID)
	if peerID == "" || workbenchRecruitingUnsafeValue(peerID) {
		return NewAutomationValidationError("audience_id", "configured audience recipient is invalid")
	}
	switch audience.ChatType {
	case ChatTypePrivate:
		if strings.Contains(peerID, "@chatroom") {
			return NewAutomationValidationError("audience_id", "configured private audience recipient is invalid")
		}
	case ChatTypeGroup:
		if _, err := normalizeRecruitingGroupID(peerID); err != nil {
			return NewAutomationValidationError("audience_id", "configured group audience recipient is invalid")
		}
	default:
		return NewAutomationValidationError("audience_id", "configured audience chat type is invalid")
	}
	return nil
}

func workbenchRecruitingSafeSource(source RecruitingSourceGroup) bool {
	if !workbenchResourceID(source.ID, "source") {
		return false
	}
	if _, err := normalizeRecruitingGroupID(source.GroupID); err != nil {
		return false
	}
	return !workbenchRecruitingUnsafeValue(source.DisplayName) &&
		!workbenchRecruitingUnsafeValue(source.GateMode)
}

func workbenchRecruitingSafeForwardRule(rule ForwardRule) bool {
	if !workbenchResourceID(rule.ID, "rule") ||
		workbenchRecruitingValidateForwardTarget(rule.GroupID) != nil ||
		!workbenchRecruitingSafeKeywords(rule.Keywords) {
		return false
	}
	return rule.CooldownSeconds >= 0 && rule.CooldownSeconds <= 86400
}

func workbenchRecruitingSafeKeywords(keywords []string) bool {
	if len(keywords) > 32 {
		return false
	}
	for _, keyword := range keywords {
		if keyword == "" || len([]rune(keyword)) > 120 ||
			workbenchRecruitingUnsafeValue(keyword) {
			return false
		}
	}
	return true
}

func workbenchRecruitingUnsafeValue(value string) bool {
	if value != strings.TrimSpace(value) || strings.ContainsRune(value, '\x00') {
		return true
	}
	lower := strings.ToLower(value)
	if automationForbiddenURLPattern.MatchString(value) ||
		automationForbiddenPathPattern.MatchString(value) {
		return true
	}
	for _, marker := range []string{
		"api_key", "apikey", "access_token", "accesstoken",
		"client_secret", "clientsecret", "private_key", "privatekey",
		"-----begin", "github_pat_", "ghp_", "glpat-", "sk-live-", "sk_live_",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func workbenchRecruitingAudienceRecipient(audience AutomationAudience) *AutomationRecipient {
	return &AutomationRecipient{
		AudienceID:      audience.ID,
		AudienceVersion: audience.Version,
		ChatType:        audience.ChatType,
		PeerID:          audience.PeerID,
	}
}

func cloneAutomationRecipient(recipient *AutomationRecipient) *AutomationRecipient {
	if recipient == nil {
		return nil
	}
	cloned := *recipient
	return &cloned
}

func cloneAutomationAction(action AutomationAction) AutomationAction {
	action.RiskReasons = append([]string{}, action.RiskReasons...)
	action.Evidence = append([]AutomationEvidence{}, action.Evidence...)
	action.Preconditions = append([]AutomationEntityPrecondition{}, action.Preconditions...)
	action.DependsOnActionIDs = append([]string{}, action.DependsOnActionIDs...)
	action.Recipient = cloneAutomationRecipient(action.Recipient)
	action.Payload = append(json.RawMessage(nil), action.Payload...)
	action.Result = append(json.RawMessage(nil), action.Result...)
	return action
}

func jsonEqual(left json.RawMessage, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return fmt.Sprint(leftValue) == fmt.Sprint(rightValue)
}
