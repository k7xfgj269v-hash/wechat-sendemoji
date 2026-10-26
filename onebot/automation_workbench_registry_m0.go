package main

import (
	"context"
	"strings"
)

func init() {
	bindWorkbenchOperationHandlers("workbench-m0",
		WorkbenchOperationBinding{Operation: WorkbenchOperationCatalogList, Handler: workbenchM0CatalogList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationContactsSearch, Handler: workbenchM0ContactsSearch},
		WorkbenchOperationBinding{Operation: WorkbenchOperationMessagesList, Handler: workbenchM0MessagesList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationMessagesGet, Handler: workbenchM0MessagesGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesList, Handler: workbenchM0CandidatesList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesGet, Handler: workbenchM0CandidatesGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationJobsList, Handler: workbenchM0JobsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationJobsGet, Handler: workbenchM0JobsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationLeadsList, Handler: workbenchM0LeadsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationLeadsGet, Handler: workbenchM0LeadsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOnboardingsList, Handler: workbenchM0OnboardingsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOnboardingsGet, Handler: workbenchM0OnboardingsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesCreate, Handler: workbenchM0CandidatesCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesLinkMessage, Handler: workbenchM0CandidatesLinkMessage},
		WorkbenchOperationBinding{Operation: WorkbenchOperationJobsCreate, Handler: workbenchM0JobsCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationLeadsCreate, Handler: workbenchM0LeadsCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationLeadsTransition, Handler: workbenchM0LeadsTransition},
		WorkbenchOperationBinding{Operation: WorkbenchOperationNotesCreate, Handler: workbenchM0NotesCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationTasksCreate, Handler: workbenchM0TasksCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationTasksSetStatus, Handler: workbenchM0TasksSetStatus},
		WorkbenchOperationBinding{Operation: WorkbenchOperationLeadsTerminal, Handler: workbenchM0LeadsTerminal},
		WorkbenchOperationBinding{Operation: WorkbenchOperationMessagesSendPrivate, Handler: workbenchM0PrivateSend},
		WorkbenchOperationBinding{Operation: WorkbenchOperationMessagesSendGroup, Handler: workbenchM0GroupSend},
	)
}

func workbenchM0CatalogList(
	_ context.Context,
	_ *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	arguments := raw.(*WorkbenchPageArguments)
	return workbenchCatalogListResult(WorkbenchAvailableToolCatalog(), *arguments)
}

func workbenchCatalogListResult(
	catalog WorkbenchAvailableCatalog,
	arguments WorkbenchPageArguments,
) WorkbenchToolResult {
	total := workbenchAvailableCatalogOperationCount(catalog)
	remaining := total - arguments.Offset
	if remaining <= 0 {
		return NewWorkbenchToolSuccess(
			WorkbenchOperationCatalogList,
			workbenchAvailableCatalogSlice(catalog, arguments.Offset, 0),
			newWorkbenchPage(arguments.Limit, arguments.Offset, total, 0),
		)
	}
	maxReturned := arguments.Limit
	if maxReturned > remaining {
		maxReturned = remaining
	}
	resultFor := func(returned int) WorkbenchToolResult {
		return NewWorkbenchToolSuccess(
			WorkbenchOperationCatalogList,
			workbenchAvailableCatalogSlice(catalog, arguments.Offset, returned),
			newWorkbenchPage(arguments.Limit, arguments.Offset, total, returned),
		)
	}

	best := resultFor(1)
	if !best.OK {
		return best
	}
	for low, high := 2, maxReturned; low <= high; {
		middle := low + (high-low)/2
		candidate := resultFor(middle)
		if candidate.OK {
			best = candidate
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	return best
}

func workbenchM0ContactsSearch(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationContactsSearch
	if dispatcher.agent == nil {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorUnavailable, "WeChat agent is unavailable", true)
	}
	arguments := raw.(*WorkbenchContactSearchArguments)
	result, err := dispatcher.agent.SearchContacts(ctx, AgentContactSearchRequest{
		Keyword: arguments.Keyword, LocalType: arguments.LocalType,
		Limit: arguments.Limit, Offset: arguments.Offset,
	})
	if err != nil {
		return workbenchAgentFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		struct {
			Contacts []Contact `json:"contacts"`
		}{Contacts: result.Contacts},
		newWorkbenchPage(arguments.Limit, arguments.Offset, result.Total, len(result.Contacts)),
	)
}

func workbenchM0MessagesList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationMessagesList
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchMessageListArguments)
	total, events, err := crmMessageList(dispatcher.crm, crmMessageListFilter{
		Query: arguments.Query, ReviewStatus: arguments.ReviewStatus,
		CandidateID: arguments.CandidateID, Limit: arguments.Limit, Offset: arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		struct {
			Events []crmMessageEventProjection `json:"events"`
		}{Events: events},
		newWorkbenchPage(arguments.Limit, arguments.Offset, total, len(events)),
	)
}

func workbenchM0MessagesGet(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationMessagesGet, func() (any, error) {
		return crmMessageGet(d.crm, raw.(*WorkbenchIDArguments).ID)
	})
}

func workbenchM0CandidatesList(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationCandidatesList
	if unavailable := workbenchM0RequireCRM(d, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchCandidateListArguments)
	total, candidates, err := crmCandidateList(d.crm, crmCandidateListFilter{
		Query: arguments.Query, Status: arguments.Status, Owner: arguments.Owner,
		Source: arguments.Source, Limit: arguments.Limit, Offset: arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		struct {
			Candidates []CandidateListItem `json:"candidates"`
		}{Candidates: candidates},
		newWorkbenchPage(arguments.Limit, arguments.Offset, total, len(candidates)),
	)
}

func workbenchM0CandidatesGet(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationCandidatesGet, func() (any, error) {
		return crmCandidateGetDetail(d.crm, raw.(*WorkbenchIDArguments).ID)
	})
}

func workbenchM0JobsList(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationJobsList
	if unavailable := workbenchM0RequireCRM(d, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchJobListArguments)
	total, jobs, err := crmPipelineListJobs(d.crm, crmPipelineJobFilter{
		Query: arguments.Query, Status: arguments.Status, Priority: arguments.Priority,
		SiteName: arguments.SiteName, Owner: arguments.Owner,
		Limit: arguments.Limit, Offset: arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		struct {
			Jobs []JobListItem `json:"jobs"`
		}{Jobs: jobs},
		newWorkbenchPage(arguments.Limit, arguments.Offset, total, len(jobs)),
	)
}

func workbenchM0JobsGet(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationJobsGet, func() (any, error) {
		return crmPipelineGetJob(d.crm, raw.(*WorkbenchIDArguments).ID)
	})
}

func workbenchM0LeadsList(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationLeadsList
	if unavailable := workbenchM0RequireCRM(d, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchLeadListArguments)
	total, leads, err := crmPipelineListLeads(d.crm, crmPipelineLeadFilter{
		CandidateID: arguments.CandidateID, JobID: arguments.JobID,
		Stage: arguments.Stage, Owner: arguments.Owner,
		Limit: arguments.Limit, Offset: arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		struct {
			Leads []LeadListItem `json:"leads"`
		}{Leads: leads},
		newWorkbenchPage(arguments.Limit, arguments.Offset, total, len(leads)),
	)
}

func workbenchM0LeadsGet(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationLeadsGet, func() (any, error) {
		return crmPipelineGetLead(d.crm, raw.(*WorkbenchIDArguments).ID)
	})
}

func workbenchM0OnboardingsList(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationOnboardingsList
	if unavailable := workbenchM0RequireCRM(d, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchOnboardingListArguments)
	total, onboardings, err := crmOnboardingList(d.crm, crmOnboardingFilter{
		LeadID: arguments.LeadID, CandidateID: arguments.CandidateID,
		JobID: arguments.JobID, Status: arguments.Status,
		FleetStatus: arguments.FleetStatus, FleetRequired: arguments.FleetRequired,
		Limit: arguments.Limit, Offset: arguments.Offset,
	})
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(
		operation,
		struct {
			OnboardingCases []OnboardingCase `json:"onboarding_cases"`
		}{OnboardingCases: onboardings},
		newWorkbenchPage(arguments.Limit, arguments.Offset, total, len(onboardings)),
	)
}

func workbenchM0OnboardingsGet(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOnboardingsGet, func() (any, error) {
		return crmOnboardingGet(d.crm, raw.(*WorkbenchIDArguments).ID)
	})
}

func workbenchM0CandidatesCreate(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationCandidatesCreate, func() (any, error) {
		return crmCandidateCreate(d.crm, raw.(*WorkbenchCandidateCreateArguments).Candidate)
	})
}

func workbenchM0CandidatesLinkMessage(ctx context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationCandidatesLinkMessage, func() (any, error) {
		return crmCandidateLinkMessage(ctx, d.crm, ManualCandidateExtractor{}, raw.(*WorkbenchCandidateLinkMessageArguments).Request)
	})
}

func workbenchM0JobsCreate(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationJobsCreate, func() (any, error) {
		return crmPipelineCreateJob(d.crm, raw.(*WorkbenchJobCreateArguments).Job)
	})
}

func workbenchM0LeadsCreate(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationLeadsCreate, func() (any, error) {
		return crmPipelineCreateLead(d.crm, raw.(*WorkbenchLeadCreateArguments).Lead)
	})
}

func workbenchM0LeadsTransition(_ context.Context, d *defaultWorkbenchToolDispatcher, execution WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationLeadsTransition, func() (any, error) {
		arguments := raw.(*WorkbenchLeadTransitionArguments)
		return crmPipelineTransitionLead(d.crm, arguments.LeadID, arguments.DomainTransition(strings.TrimSpace(execution.OperatorID)))
	})
}

func workbenchM0NotesCreate(_ context.Context, d *defaultWorkbenchToolDispatcher, execution WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationNotesCreate, func() (any, error) {
		return crmActivityNewService(d.crm).CreateNote(raw.(*WorkbenchNoteCreateArguments).DomainNote(strings.TrimSpace(execution.OperatorID)))
	})
}

func workbenchM0TasksCreate(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationTasksCreate, func() (any, error) {
		return crmActivityNewService(d.crm).CreateTask(raw.(*WorkbenchTaskCreateArguments).Task)
	})
}

func workbenchM0TasksSetStatus(_ context.Context, d *defaultWorkbenchToolDispatcher, _ WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationTasksSetStatus, func() (any, error) {
		arguments := raw.(*WorkbenchTaskStatusArguments)
		return crmActivityNewService(d.crm).SetTaskStatus(arguments.TaskID, arguments.Request)
	})
}

func workbenchM0LeadsTerminal(_ context.Context, d *defaultWorkbenchToolDispatcher, execution WorkbenchExecutionContext, raw any) WorkbenchToolResult {
	operation := WorkbenchOperationLeadsTerminal
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchM0CRMResult(d, operation, func() (any, error) {
		arguments := raw.(*WorkbenchLeadTerminalArguments)
		operatorID := strings.TrimSpace(execution.OperatorID)
		return crmPipelineTransitionLeadConfirmed(
			d.crm,
			arguments.LeadID,
			LeadTransition{
				ExpectedVersion: arguments.ExpectedVersion,
				FromStage:       arguments.FromStage, ToStage: arguments.ToStage,
				Reason: arguments.Reason, Actor: operatorID,
			},
			true,
			operatorID,
		)
	})
}

func workbenchM0PrivateSend(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0ControlledSend(
		ctx,
		dispatcher,
		execution,
		raw,
		WorkbenchOperationMessagesSendPrivate,
	)
}

func workbenchM0GroupSend(
	ctx context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0ControlledSend(
		ctx,
		dispatcher,
		execution,
		raw,
		WorkbenchOperationMessagesSendGroup,
	)
}

func workbenchM0ControlledSend(
	_ context.Context,
	_ *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	_ any,
	operation WorkbenchOperation,
) WorkbenchToolResult {
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorForbidden,
		"approved controlled sends must execute through the automation outbox",
		false,
	)
}

func workbenchM0RequireCRM(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) *WorkbenchToolResult {
	if dispatcher != nil && dispatcher.crm != nil {
		return nil
	}
	result := NewWorkbenchToolFailure(operation, WorkbenchErrorUnavailable, "CRM store is unavailable", true)
	return &result
}

func workbenchM0CRMResult(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
	call func() (any, error),
) WorkbenchToolResult {
	if unavailable := workbenchM0RequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	data, err := call()
	return workbenchResult(operation, data, err)
}
