package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const workbenchServiceRequestTextJSONBytes = 2048

type workbenchServiceRequestTextProjection struct {
	Excerpt            string `json:"excerpt"`
	OriginalCharacters int    `json:"original_characters"`
	Truncated          bool   `json:"truncated"`
}

type workbenchServiceRequestProjection struct {
	ID               string                                `json:"id"`
	CandidateID      string                                `json:"candidate_id"`
	JobID            string                                `json:"job_id,omitempty"`
	OnboardingCaseID string                                `json:"onboarding_case_id,omitempty"`
	SourceEventID    string                                `json:"source_event_id,omitempty"`
	Category         ServiceRequestCategory                `json:"category"`
	Priority         JobPriority                           `json:"priority"`
	Description      workbenchServiceRequestTextProjection `json:"description"`
	Owner            string                                `json:"owner,omitempty"`
	Status           ServiceRequestStatus                  `json:"status"`
	RequiresHuman    bool                                  `json:"requires_human"`
	QueueID          string                                `json:"queue_id,omitempty"`
	EnqueuedAt       *time.Time                            `json:"enqueued_at,omitempty"`
	ClaimedBy        string                                `json:"claimed_by,omitempty"`
	ClaimedAt        *time.Time                            `json:"claimed_at,omitempty"`
	DueAt            *time.Time                            `json:"due_at,omitempty"`
	Resolution       workbenchServiceRequestTextProjection `json:"resolution"`
	Version          uint64                                `json:"version"`
	CreatedAt        time.Time                             `json:"created_at"`
	UpdatedAt        time.Time                             `json:"updated_at"`
	ResolvedAt       *time.Time                            `json:"resolved_at,omitempty"`
}

type workbenchServiceRequestListData struct {
	ServiceRequests []workbenchServiceRequestProjection `json:"service_requests"`
}

type workbenchServiceQueueListData struct {
	ServiceQueues []ServiceQueue `json:"service_queues"`
}

func init() {
	bindWorkbenchOperationHandlers("workbench-service-requests",
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceRequestsList, Handler: workbenchServiceRequestsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceRequestsGet, Handler: workbenchServiceRequestsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceRequestsCreate, Handler: workbenchServiceRequestsCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceRequestsUpdate, Handler: workbenchServiceRequestsUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceRequestsTransition, Handler: workbenchServiceRequestsTransition},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceQueuesList, Handler: workbenchServiceQueuesList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceQueuesGet, Handler: workbenchServiceQueuesGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceQueuesCreate, Handler: workbenchServiceQueuesCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationServiceQueuesUpdate, Handler: workbenchServiceQueuesUpdate},
	)
}

func workbenchServiceRequestsList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationServiceRequestsList
	if unavailable := workbenchServiceRequestsRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchServiceRequestListArguments)
	total, requests, err := crmServiceRequestList(dispatcher.crm, crmServiceRequestFilter{
		Query:            arguments.Query,
		CandidateID:      arguments.CandidateID,
		JobID:            arguments.JobID,
		OnboardingCaseID: arguments.OnboardingCaseID,
		Category:         arguments.Category,
		Priority:         arguments.Priority,
		Status:           arguments.Status,
		Owner:            arguments.Owner,
		RequiresHuman:    arguments.RequiresHuman,
		Overdue:          arguments.Overdue,
		Limit:            arguments.Limit,
		Offset:           arguments.Offset,
	})
	if err != nil {
		return workbenchServiceRequestsFailure(operation, err)
	}
	projected := make([]workbenchServiceRequestProjection, len(requests))
	for index := range requests {
		projected[index] = workbenchServiceRequestProject(requests[index])
	}
	return workbenchServiceListResult(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(values []workbenchServiceRequestProjection) any {
			return workbenchServiceRequestListData{ServiceRequests: values}
		},
	)
}

func workbenchServiceRequestsGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchServiceRequestResult(
		dispatcher,
		WorkbenchOperationServiceRequestsGet,
		func() (ServiceRequest, error) {
			return crmServiceRequestGet(dispatcher.crm, raw.(*WorkbenchIDArguments).ID)
		},
	)
}

func workbenchServiceRequestsCreate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchServiceRequestResult(
		dispatcher,
		WorkbenchOperationServiceRequestsCreate,
		func() (ServiceRequest, error) {
			create := raw.(*WorkbenchServiceRequestCreateArguments).Request
			create.Actor = strings.TrimSpace(execution.OperatorID)
			return crmServiceRequestCreate(dispatcher.crm, create)
		},
	)
}

func workbenchServiceRequestsUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchServiceRequestResult(
		dispatcher,
		WorkbenchOperationServiceRequestsUpdate,
		func() (ServiceRequest, error) {
			arguments := raw.(*WorkbenchServiceRequestUpdateArguments)
			update := arguments.Update
			update.Actor = strings.TrimSpace(execution.OperatorID)
			return crmServiceRequestUpdate(dispatcher.crm, arguments.RequestID, update)
		},
	)
}

func workbenchServiceRequestsTransition(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationServiceRequestsTransition
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchServiceRequestResult(dispatcher, operation, func() (ServiceRequest, error) {
		arguments := raw.(*WorkbenchServiceRequestTransitionArguments)
		return workbenchServiceRequestTransitionConfirmed(
			dispatcher.crm,
			arguments.RequestID,
			ServiceRequestTransition{
				ExpectedVersion: arguments.ExpectedVersion,
				FromStatus:      arguments.FromStatus,
				ToStatus:        arguments.ToStatus,
				Resolution:      arguments.Resolution,
			},
			true,
			strings.TrimSpace(execution.OperatorID),
		)
	})
}

func workbenchServiceQueuesList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationServiceQueuesList
	if unavailable := workbenchServiceRequestsRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchPageArguments)
	queues, err := crmServiceQueueList(dispatcher.crm)
	if err != nil {
		return workbenchServiceRequestsFailure(operation, err)
	}
	total := len(queues)
	queues = crmServiceRequestPage(queues, arguments.Offset, arguments.Limit)
	return workbenchServiceListResult(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		queues,
		func(values []ServiceQueue) any {
			return workbenchServiceQueueListData{ServiceQueues: values}
		},
	)
}

func workbenchServiceQueuesGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchServiceRequestsCRMResult(
		dispatcher,
		WorkbenchOperationServiceQueuesGet,
		func() (any, error) {
			return crmServiceQueueGet(dispatcher.crm, raw.(*WorkbenchIDArguments).ID)
		},
	)
}

func workbenchServiceQueuesCreate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchServiceRequestsCRMResult(
		dispatcher,
		WorkbenchOperationServiceQueuesCreate,
		func() (any, error) {
			return crmServiceQueueCreate(
				dispatcher.crm,
				raw.(*WorkbenchServiceQueueCreateArguments).Queue,
				strings.TrimSpace(execution.OperatorID),
			)
		},
	)
}

func workbenchServiceQueuesUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchServiceRequestsCRMResult(
		dispatcher,
		WorkbenchOperationServiceQueuesUpdate,
		func() (any, error) {
			arguments := raw.(*WorkbenchServiceQueueUpdateArguments)
			return crmServiceQueueUpdate(
				dispatcher.crm,
				arguments.QueueID,
				arguments.Update,
				strings.TrimSpace(execution.OperatorID),
			)
		},
	)
}

func workbenchServiceRequestTransitionConfirmed(
	store *CRMStore,
	id string,
	transition ServiceRequestTransition,
	confirmed bool,
	operatorID string,
) (ServiceRequest, error) {
	if !confirmed {
		return ServiceRequest{}, NewCRMValidationError("confirmed", "explicit confirmation is required")
	}
	operatorID, err := normalizeCRMString("operator_id", operatorID, 80, true, false)
	if err != nil {
		return ServiceRequest{}, err
	}
	transition.Actor = operatorID
	return crmServiceRequestTransition(store, id, transition)
}

func workbenchServiceRequestsRequireCRM(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) *WorkbenchToolResult {
	if dispatcher != nil && dispatcher.crm != nil {
		return nil
	}
	result := NewWorkbenchToolFailure(operation, WorkbenchErrorUnavailable, "CRM store is unavailable", true)
	return &result
}

func workbenchServiceRequestsCRMResult(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
	call func() (any, error),
) WorkbenchToolResult {
	if unavailable := workbenchServiceRequestsRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	data, err := call()
	if err != nil {
		return workbenchServiceRequestsFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, data, nil)
}

func workbenchServiceRequestResult(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
	call func() (ServiceRequest, error),
) WorkbenchToolResult {
	if unavailable := workbenchServiceRequestsRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	request, err := call()
	if err != nil {
		return workbenchServiceRequestsFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, workbenchServiceRequestProject(request), nil)
}

func workbenchServiceRequestsFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	if crmServiceRequestIsNotFound(err) {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorNotFound, err.Error(), false)
	}
	return workbenchDomainFailure(operation, err)
}

func workbenchServiceListResult[T any](
	operation WorkbenchOperation,
	limit int,
	offset int,
	total int,
	values []T,
	data func([]T) any,
) WorkbenchToolResult {
	if len(values) == 0 {
		return NewWorkbenchToolSuccess(
			operation,
			data([]T{}),
			newWorkbenchPage(limit, offset, total, 0),
		)
	}

	accepted := make([]T, 0, len(values))
	var result WorkbenchToolResult
	for _, value := range values {
		candidate := append(accepted, value)
		candidateResult := NewWorkbenchToolSuccess(
			operation,
			data(candidate),
			newWorkbenchPage(limit, offset, total, len(candidate)),
		)
		if !candidateResult.OK {
			if len(accepted) == 0 {
				return candidateResult
			}
			return result
		}
		accepted = candidate
		result = candidateResult
	}
	return result
}

func workbenchServiceRequestProject(
	request ServiceRequest,
) workbenchServiceRequestProjection {
	return workbenchServiceRequestProjection{
		ID:               request.ID,
		CandidateID:      request.CandidateID,
		JobID:            request.JobID,
		OnboardingCaseID: request.OnboardingCaseID,
		SourceEventID:    request.SourceEventID,
		Category:         request.Category,
		Priority:         request.Priority,
		Description:      workbenchServiceRequestProjectText(request.Description),
		Owner:            request.Owner,
		Status:           request.Status,
		RequiresHuman:    request.RequiresHuman,
		QueueID:          request.QueueID,
		EnqueuedAt:       cloneCRMTimePointer(request.EnqueuedAt),
		ClaimedBy:        request.ClaimedBy,
		ClaimedAt:        cloneCRMTimePointer(request.ClaimedAt),
		DueAt:            cloneCRMTimePointer(request.DueAt),
		Resolution:       workbenchServiceRequestProjectText(request.Resolution),
		Version:          request.Version,
		CreatedAt:        request.CreatedAt,
		UpdatedAt:        request.UpdatedAt,
		ResolvedAt:       cloneCRMTimePointer(request.ResolvedAt),
	}
}

func workbenchServiceRequestProjectText(
	value string,
) workbenchServiceRequestTextProjection {
	runes := []rune(value)
	encoded, _ := json.Marshal(value)
	if len(encoded) <= workbenchServiceRequestTextJSONBytes {
		return workbenchServiceRequestTextProjection{
			Excerpt:            value,
			OriginalCharacters: len(runes),
		}
	}

	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		candidate, _ := json.Marshal(string(runes[:middle]))
		if len(candidate) <= workbenchServiceRequestTextJSONBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return workbenchServiceRequestTextProjection{
		Excerpt:            string(runes[:low]),
		OriginalCharacters: len(runes),
		Truncated:          true,
	}
}
