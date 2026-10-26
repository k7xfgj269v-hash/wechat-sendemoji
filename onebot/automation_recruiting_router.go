package main

import (
	"context"
	"errors"
)

const automationRecruitingToolJobCreate AutomationToolName = "job.create"

var errRecruitingRouteAlreadyFrozen = errors.New("recruiting route already frozen")

const (
	automationConversationWeChatPrivate     = "wechat_private"
	automationConversationWeChatSourceGroup = "wechat_source_group"
	automationConversationWeChatGroupNoise  = "wechat_group_noise"
	automationConversationOperatorChat      = "operator_chat"
	automationConversationTestChat          = "test_chat"
)

type RecruitingAutomationRoute struct {
	Event                   MessageEvent
	ConversationKind        string
	Disposition             MessageDisposition
	OrganizationName        string
	SourceGroup             *RecruitingSourceGroup
	RequireAuthoritativeJob bool
	AllowedTools            []AutomationToolName
}

type RecruitingAutomationRouter struct {
	crm *CRMStore
}

func NewRecruitingAutomationRouter(crm *CRMStore) *RecruitingAutomationRouter {
	return &RecruitingAutomationRouter{crm: crm}
}

func (r *RecruitingAutomationRouter) Route(
	event MessageEvent,
) (RecruitingAutomationRoute, error) {
	if r == nil || r.crm == nil {
		return RecruitingAutomationRoute{}, NewAutomationValidationError(
			"recruiting_router",
			"CRM store is unavailable",
		)
	}
	if err := validateMessageEvent("event", &event); err != nil {
		return RecruitingAutomationRoute{}, err
	}
	if event.Source != crmMessageSource {
		return RecruitingAutomationRoute{}, NewAutomationValidationError(
			"event.source",
			"must reference a persisted WeChat message",
		)
	}

	snapshot := r.crm.Snapshot()
	stored, ok := automationFindMessageEvent(snapshot, event.ID)
	if !ok {
		return RecruitingAutomationRoute{}, NewAutomationValidationError(
			"event.id",
			"references a missing persisted message event",
		)
	}
	if stored.MessageKey != event.MessageKey {
		return RecruitingAutomationRoute{}, NewAutomationValidationError(
			"event.message_key",
			"does not match the persisted message event",
		)
	}
	if stored.RecruitingProjectVersion != 0 {
		return frozenRecruitingAutomationRoute(stored), nil
	}
	route := buildRecruitingAutomationRoute(stored, snapshot.RecruitingProject)

	err := r.crm.UpdateWithMeta(CRMUpdateMeta{
		Actor:         "automation",
		Operation:     "automation.recruiting.route",
		EntityType:    "message_event",
		EntityID:      event.ID,
		SourceEventID: event.ID,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := sourceJobEventIndex(state.MessageEvents, event.ID)
		if index < 0 {
			return NewAutomationValidationError(
				"event.id",
				"references a missing persisted message event",
			)
		}
		stored := &state.MessageEvents[index]
		if stored.MessageKey != event.MessageKey {
			return NewAutomationValidationError(
				"event.message_key",
				"does not match the persisted message event",
			)
		}
		if stored.RecruitingProjectVersion != 0 {
			route = frozenRecruitingAutomationRoute(*stored)
			return errRecruitingRouteAlreadyFrozen
		}

		route = buildRecruitingAutomationRoute(*stored, state.RecruitingProject)
		freezeRecruitingAutomationRoute(stored, route, state.RecruitingProject.Version)
		stored.Version++
		stored.UpdatedAt = crmPipelineMutationTime(stored.UpdatedAt)
		route.Event = *stored
		return nil
	})
	if errors.Is(err, errRecruitingRouteAlreadyFrozen) {
		return route, nil
	}
	return route, err
}

func buildRecruitingAutomationRoute(
	event MessageEvent,
	project RecruitingProjectConfig,
) RecruitingAutomationRoute {
	route := RecruitingAutomationRoute{
		Event:                   event,
		OrganizationName:        project.OrganizationName,
		RequireAuthoritativeJob: project.RequireAuthoritativeJob,
	}

	switch event.Source {
	case "automation_chat":
		route.ConversationKind = automationConversationOperatorChat
		route.AllowedTools = recruitingInternalAutomationTools()
		return route
	case "automation_test_chat":
		route.ConversationKind = automationConversationTestChat
		route.AllowedTools = recruitingInternalAutomationTools()
		return route
	}

	if event.ChatType == ChatTypePrivate {
		route.ConversationKind = automationConversationWeChatPrivate
		route.Disposition = MessageDispositionPrivateCandidate
		route.AllowedTools = recruitingPrivateAutomationTools()
		return route
	}

	for i := range project.SourceGroups {
		group := project.SourceGroups[i]
		if group.GroupID != event.GroupID || !group.Enabled {
			continue
		}
		route.ConversationKind = automationConversationWeChatSourceGroup
		route.Disposition = MessageDispositionSourceGroupJob
		route.SourceGroup = &group
		route.AllowedTools = []AutomationToolName{
			AutomationToolMessageClassify,
			automationRecruitingToolJobCreate,
		}
		return route
	}

	route.ConversationKind = automationConversationWeChatGroupNoise
	route.Disposition = MessageDispositionGroupNoise
	route.AllowedTools = []AutomationToolName{AutomationToolMessageClassify}
	return route
}

func freezeRecruitingAutomationRoute(
	event *MessageEvent,
	route RecruitingAutomationRoute,
	projectVersion uint64,
) {
	event.Disposition = route.Disposition
	event.RecruitingProjectVersion = projectVersion
	event.RecruitingRouteSourceVersion = event.Version
	event.RecruitingConversationKind = route.ConversationKind
	event.RecruitingAllowedTools = append([]AutomationToolName(nil), route.AllowedTools...)
	event.RecruitingOrganizationName = route.OrganizationName
	event.RecruitingRequireAuthoritative = route.RequireAuthoritativeJob
	event.RecruitingSourceGroupID = ""
	event.RecruitingSourceGroupName = ""
	event.RecruitingSourceGroupVersion = 0
	event.RecruitingSourceGateEnabled = false
	event.RecruitingSourceGateMode = ""
	if route.SourceGroup != nil {
		event.RecruitingSourceGroupID = route.SourceGroup.ID
		event.RecruitingSourceGroupName = route.SourceGroup.DisplayName
		event.RecruitingSourceGroupVersion = route.SourceGroup.Version
		event.RecruitingSourceGateEnabled = route.SourceGroup.GateEnabled
		event.RecruitingSourceGateMode = route.SourceGroup.GateMode
	}
}

func frozenRecruitingAutomationRoute(event MessageEvent) RecruitingAutomationRoute {
	route := RecruitingAutomationRoute{
		Event:                   event,
		ConversationKind:        event.RecruitingConversationKind,
		Disposition:             event.Disposition,
		OrganizationName:        event.RecruitingOrganizationName,
		RequireAuthoritativeJob: event.RecruitingRequireAuthoritative,
		AllowedTools:            append([]AutomationToolName(nil), event.RecruitingAllowedTools...),
	}
	route.Event.RecruitingAllowedTools = append(
		[]AutomationToolName(nil),
		event.RecruitingAllowedTools...,
	)
	if event.RecruitingConversationKind == automationConversationWeChatSourceGroup {
		route.SourceGroup = &RecruitingSourceGroup{
			ID:          event.RecruitingSourceGroupID,
			GroupID:     event.GroupID,
			DisplayName: event.RecruitingSourceGroupName,
			Enabled:     true,
			GateEnabled: event.RecruitingSourceGateEnabled,
			GateMode:    event.RecruitingSourceGateMode,
			Version:     event.RecruitingSourceGroupVersion,
		}
	}
	return route
}

func (r *RecruitingAutomationRouter) applyCurrentGatePolicy(
	route RecruitingAutomationRoute,
) RecruitingAutomationRoute {
	if r == nil || r.crm == nil || route.SourceGroup == nil || route.SourceGroup.GateEnabled {
		return route
	}
	project := r.crm.Snapshot().RecruitingProject
	for i := range project.SourceGroups {
		current := project.SourceGroups[i]
		if current.ID != route.SourceGroup.ID ||
			current.GroupID != route.Event.GroupID ||
			!current.Enabled ||
			!current.GateEnabled {
			continue
		}
		source := *route.SourceGroup
		source.GateEnabled = true
		source.GateMode = current.GateMode
		route.SourceGroup = &source
		return route
	}
	return route
}

func recruitingInternalAutomationTools() []AutomationToolName {
	all := append(automationAllTools(), automationRecruitingToolJobCreate)
	tools := make([]AutomationToolName, 0, len(all)-3)
	for _, tool := range all {
		if tool == AutomationToolReplyPrivateText ||
			tool == AutomationToolReplyAudienceText ||
			tool == AutomationToolForwardEvent {
			continue
		}
		tools = append(tools, tool)
	}
	return tools
}

func recruitingPrivateAutomationTools() []AutomationToolName {
	return []AutomationToolName{
		AutomationToolMessageClassify,
		AutomationToolCandidateCreate,
		AutomationToolCandidateUpdate,
		AutomationToolCandidateLink,
		AutomationToolJobMatch,
		AutomationToolLeadCreate,
		AutomationToolLeadTransition,
		AutomationToolOnboardingCreate,
		AutomationToolOnboardingUpdate,
		AutomationToolFleetUpdate,
		AutomationToolServiceCreate,
		AutomationToolServiceUpdate,
		AutomationToolFundCreate,
		AutomationToolTaskCreate,
		AutomationToolNoteCreate,
		AutomationToolReplyPrivateText,
	}
}

type recruitingAutomationRunEngine struct {
	router   *RecruitingAutomationRouter
	delegate AutomationRunEngine
}

func newRecruitingAutomationRunEngine(
	router *RecruitingAutomationRouter,
	delegate AutomationRunEngine,
) AutomationRunEngine {
	if engine, ok := delegate.(*defaultAutomationRunEngine); ok {
		engine.recruitingRouter = router
		return engine
	}
	return &recruitingAutomationRunEngine{
		router:   router,
		delegate: delegate,
	}
}

func (e *recruitingAutomationRunEngine) EnsureRun(
	ctx context.Context,
	event MessageEvent,
	providerName string,
) (AutomationRun, bool, error) {
	if e == nil || e.router == nil || e.delegate == nil {
		return AutomationRun{}, false, NewAutomationValidationError(
			"recruiting_run_engine",
			"is unavailable",
		)
	}
	route, err := e.router.Route(event)
	if err != nil {
		return AutomationRun{}, false, err
	}
	return e.delegate.EnsureRun(ctx, route.Event, providerName)
}

func (e *recruitingAutomationRunEngine) RetryRun(
	ctx context.Context,
	runID string,
	expectedVersion uint64,
	operatorID string,
	reason string,
) (AutomationRun, error) {
	if e == nil || e.delegate == nil {
		return AutomationRun{}, NewAutomationValidationError(
			"recruiting_run_engine",
			"is unavailable",
		)
	}
	return e.delegate.RetryRun(ctx, runID, expectedVersion, operatorID, reason)
}

func (e *recruitingAutomationRunEngine) ProcessQueued(ctx context.Context) error {
	if e == nil || e.delegate == nil {
		return NewAutomationValidationError("recruiting_run_engine", "is unavailable")
	}
	return e.delegate.ProcessQueued(ctx)
}

func (e *recruitingAutomationRunEngine) ProcessRun(ctx context.Context, runID string) error {
	if e == nil || e.delegate == nil {
		return NewAutomationValidationError("recruiting_run_engine", "is unavailable")
	}
	return e.delegate.ProcessRun(ctx, runID)
}

func (e *recruitingAutomationRunEngine) RecoverScheduledActions(ctx context.Context) error {
	if e == nil || e.delegate == nil {
		return NewAutomationValidationError("recruiting_run_engine", "is unavailable")
	}
	return e.delegate.RecoverScheduledActions(ctx)
}
