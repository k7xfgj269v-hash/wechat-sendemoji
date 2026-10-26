package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWorkbenchServiceOperationsSchemasAndOrdinaryAudit(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	execution := WorkbenchExecutionContext{OperatorID: "operator-r1"}

	before := store.Snapshot()
	queueResult := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceQueuesCreate,
		WorkbenchServiceQueueCreateArguments{Queue: ServiceQueueCreate{
			Name:       "Safety response",
			Owner:      "service-ops",
			Categories: []ServiceRequestCategory{ServiceRequestCategoryGeneral, ServiceRequestCategorySafety},
			SLASeconds: int64((365 * 24 * time.Hour) / time.Second),
		}},
	)
	m301AssertSuccess(t, queueResult, WorkbenchOperationServiceQueuesCreate, false)
	m301AssertJSONKeys(t, queueResult.Data,
		"id", "name", "owner", "categories", "sla_seconds", "enabled",
		"version", "created_at", "updated_at",
	)
	var queue ServiceQueue
	m301DecodeData(t, queueResult, &queue)
	if queue.ID == "" || queue.Version != 1 || !queue.Enabled ||
		queue.SLASeconds != int64((365*24*time.Hour)/time.Second) {
		t.Fatalf("created service queue = %+v", queue)
	}
	m301AssertAuditDelta(t, store, before, "service_queue.create", "service_queue", "", "operator-r1")

	updatedQueueName := "Safety response priority"
	updatedQueueOwner := "human-service-ops"
	updatedQueueSLA := int64(900)
	before = store.Snapshot()
	queueResult = m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceQueuesUpdate,
		WorkbenchServiceQueueUpdateArguments{
			QueueID: queue.ID,
			Update: ServiceQueueUpdate{
				ExpectedVersion: queue.Version,
				Name:            &updatedQueueName,
				Owner:           &updatedQueueOwner,
				SLASeconds:      &updatedQueueSLA,
			},
		},
	)
	m301AssertSuccess(t, queueResult, WorkbenchOperationServiceQueuesUpdate, false)
	m301AssertJSONKeys(t, queueResult.Data,
		"id", "name", "owner", "categories", "sla_seconds", "enabled",
		"version", "created_at", "updated_at",
	)
	m301DecodeData(t, queueResult, &queue)
	if queue.Name != updatedQueueName || queue.Owner != updatedQueueOwner ||
		queue.SLASeconds != updatedQueueSLA || queue.Version != 2 {
		t.Fatalf("updated service queue = %+v", queue)
	}
	m301AssertAuditDelta(t, store, before, "service_queue.update", "service_queue", queue.ID, "operator-r1")

	beforeReads := store.Snapshot()
	queueGet := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceQueuesGet,
		WorkbenchIDArguments{ID: queue.ID},
	)
	m301AssertSuccess(t, queueGet, WorkbenchOperationServiceQueuesGet, false)
	m301AssertJSONKeys(t, queueGet.Data,
		"id", "name", "owner", "categories", "sla_seconds", "enabled",
		"version", "created_at", "updated_at",
	)
	var queriedQueue ServiceQueue
	m301DecodeData(t, queueGet, &queriedQueue)
	if !reflect.DeepEqual(queriedQueue, queue) {
		t.Fatalf("service_queues.get = %+v, want %+v", queriedQueue, queue)
	}

	queueList := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceQueuesList,
		WorkbenchPageArguments{Limit: 1, Offset: 0},
	)
	m301AssertSuccess(t, queueList, WorkbenchOperationServiceQueuesList, true)
	m301AssertJSONKeys(t, queueList.Data, "service_queues")
	m301AssertPage(t, queueList.Page, 1, 0, 1, 1, false, 0)
	var queuePage struct {
		ServiceQueues []ServiceQueue `json:"service_queues"`
	}
	m301DecodeData(t, queueList, &queuePage)
	if len(queuePage.ServiceQueues) != 1 || !reflect.DeepEqual(queuePage.ServiceQueues[0], queue) {
		t.Fatalf("service_queues.list = %+v", queuePage.ServiceQueues)
	}
	if afterReads := store.Snapshot(); !reflect.DeepEqual(afterReads, beforeReads) {
		t.Fatal("service queue reads changed CRM state or audit")
	}

	maxDescription := strings.Repeat("x", 4000)
	dueAt := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	before = store.Snapshot()
	requestResult := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceRequestsCreate,
		WorkbenchServiceRequestCreateArguments{Request: ServiceRequestCreate{
			CandidateID:   refs.Candidate.ID,
			JobID:         refs.Job.ID,
			Category:      ServiceRequestCategorySafety,
			Priority:      JobPriorityUrgent,
			Description:   maxDescription,
			Owner:         "initial-owner",
			RequiresHuman: false,
			DueAt:         &dueAt,
		}},
	)
	m301AssertSuccess(t, requestResult, WorkbenchOperationServiceRequestsCreate, false)
	m301AssertJSONKeys(t, requestResult.Data,
		"id", "candidate_id", "job_id", "category", "priority", "description",
		"owner", "status", "requires_human", "due_at", "resolution", "version",
		"created_at", "updated_at",
	)
	var createdProjection workbenchServiceRequestProjection
	m301DecodeData(t, requestResult, &createdProjection)
	request, err := crmServiceRequestGet(store, createdProjection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.ID == "" || request.Version != 1 || request.Status != ServiceRequestStatusOpen ||
		!request.RequiresHuman || request.Description != maxDescription {
		t.Fatalf("created service request = %+v", request)
	}
	m301AssertServiceRequestProjection(t, requestResult, request)
	m301AssertAuditDelta(t, store, before, "service_request.create", "service_request", "", "operator-r1")

	updatedDescription := strings.Repeat("y", 4000)
	updatedRequestOwner := "assigned-owner"
	before = store.Snapshot()
	requestResult = m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceRequestsUpdate,
		WorkbenchServiceRequestUpdateArguments{
			RequestID: request.ID,
			Update: ServiceRequestUpdate{
				ExpectedVersion: request.Version,
				Description:     &updatedDescription,
				Owner:           &updatedRequestOwner,
				QueueID:         &queue.ID,
			},
		},
	)
	m301AssertSuccess(t, requestResult, WorkbenchOperationServiceRequestsUpdate, false)
	m301AssertJSONKeys(t, requestResult.Data,
		"id", "candidate_id", "job_id", "category", "priority", "description",
		"owner", "status", "requires_human", "queue_id", "enqueued_at",
		"due_at", "resolution", "version", "created_at", "updated_at",
	)
	request, err = crmServiceRequestGet(store, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if request.Description != updatedDescription || request.Owner != updatedRequestOwner ||
		request.QueueID != queue.ID || request.EnqueuedAt == nil || request.Version != 2 {
		t.Fatalf("updated service request = %+v", request)
	}
	m301AssertServiceRequestProjection(t, requestResult, request)
	m301AssertAuditDelta(t, store, before, "service_request.update", "service_request", request.ID, "operator-r1")

	beforeReads = store.Snapshot()
	requestGet := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsGet,
		WorkbenchIDArguments{ID: request.ID},
	)
	m301AssertSuccess(t, requestGet, WorkbenchOperationServiceRequestsGet, false)
	m301AssertJSONKeys(t, requestGet.Data,
		"id", "candidate_id", "job_id", "category", "priority", "description",
		"owner", "status", "requires_human", "queue_id", "enqueued_at",
		"due_at", "resolution", "version", "created_at", "updated_at",
	)
	m301AssertServiceRequestProjection(t, requestGet, request)

	requestList := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{
			CandidateID:   refs.Candidate.ID,
			RequiresHuman: m301Bool(true),
			Limit:         1,
			Offset:        0,
		},
	)
	m301AssertSuccess(t, requestList, WorkbenchOperationServiceRequestsList, true)
	m301AssertJSONKeys(t, requestList.Data, "service_requests")
	m301AssertPage(t, requestList.Page, 1, 0, 1, 1, false, 0)
	var requestPage workbenchServiceRequestListData
	m301DecodeData(t, requestList, &requestPage)
	if len(requestPage.ServiceRequests) != 1 ||
		!reflect.DeepEqual(requestPage.ServiceRequests[0], workbenchServiceRequestProject(request)) {
		t.Fatalf("service_requests.list = %+v", requestPage.ServiceRequests)
	}
	if afterReads := store.Snapshot(); !reflect.DeepEqual(afterReads, beforeReads) {
		t.Fatal("service request reads changed CRM state or audit")
	}
}

func TestWorkbenchServiceListsStableBoundedPagination(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	firstQueue, err := crmServiceQueueCreate(store, ServiceQueueCreate{
		Name: "General queue", Categories: []ServiceRequestCategory{ServiceRequestCategoryGeneral},
		SLASeconds: 600,
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	secondQueue, err := crmServiceQueueCreate(store, ServiceQueueCreate{
		Name: "Disabled queue", Categories: []ServiceRequestCategory{},
		SLASeconds: 600, Enabled: &disabled,
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	thirdQueue, err := crmServiceQueueCreate(store, ServiceQueueCreate{
		Name: "Priority queue", Categories: []ServiceRequestCategory{ServiceRequestCategorySafety},
		SLASeconds: 600,
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}
	priorityOwner := "priority-owner"
	thirdQueue, err = crmServiceQueueUpdate(store, thirdQueue.ID, ServiceQueueUpdate{
		ExpectedVersion: thirdQueue.Version,
		Owner:           &priorityOwner,
	}, "setup")
	if err != nil {
		t.Fatal(err)
	}

	humanRequest, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategorySafety,
		Priority:    JobPriorityLow,
		Description: "human first",
	})
	if err != nil {
		t.Fatal(err)
	}
	urgentRequest, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategoryGeneral,
		Priority:    JobPriorityUrgent,
		Description: "urgent second",
	})
	if err != nil {
		t.Fatal(err)
	}
	normalRequest, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategoryGeneral,
		Priority:    JobPriorityNormal,
		Description: "normal third",
	})
	if err != nil {
		t.Fatal(err)
	}

	beforeReads := store.Snapshot()
	firstPage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{Limit: 2, Offset: 0},
	)
	repeatedPage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{Limit: 2, Offset: 0},
	)
	m301AssertSuccess(t, firstPage, WorkbenchOperationServiceRequestsList, true)
	m301AssertSuccess(t, repeatedPage, WorkbenchOperationServiceRequestsList, true)
	m301AssertPage(t, firstPage.Page, 2, 0, 2, 3, true, 2)
	if !reflect.DeepEqual(firstPage.Data, repeatedPage.Data) ||
		!reflect.DeepEqual(firstPage.Page, repeatedPage.Page) {
		t.Fatal("repeated service request list pagination was not stable")
	}
	var firstRequests workbenchServiceRequestListData
	m301DecodeData(t, firstPage, &firstRequests)
	m301AssertIDs(
		t,
		[]string{humanRequest.ID, urgentRequest.ID},
		m301RequestProjectionIDs(firstRequests.ServiceRequests),
	)

	finalPage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{Limit: 2, Offset: 2},
	)
	m301AssertSuccess(t, finalPage, WorkbenchOperationServiceRequestsList, true)
	m301AssertPage(t, finalPage.Page, 2, 2, 1, 3, false, 0)
	var finalRequests workbenchServiceRequestListData
	m301DecodeData(t, finalPage, &finalRequests)
	m301AssertIDs(
		t,
		[]string{normalRequest.ID},
		m301RequestProjectionIDs(finalRequests.ServiceRequests),
	)

	defaultPage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{},
	)
	m301AssertPage(t, defaultPage.Page, WorkbenchDefaultPageLimit, 0, 3, 3, false, 0)

	filtered := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{
			RequiresHuman: m301Bool(true),
			Limit:         WorkbenchMaxPageLimit,
		},
	)
	m301AssertPage(t, filtered.Page, WorkbenchMaxPageLimit, 0, 1, 1, false, 0)
	var filteredRequests workbenchServiceRequestListData
	m301DecodeData(t, filtered, &filteredRequests)
	m301AssertIDs(
		t,
		[]string{humanRequest.ID},
		m301RequestProjectionIDs(filteredRequests.ServiceRequests),
	)

	queuePage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceQueuesList,
		WorkbenchPageArguments{Limit: 2, Offset: 0},
	)
	m301AssertSuccess(t, queuePage, WorkbenchOperationServiceQueuesList, true)
	m301AssertPage(t, queuePage.Page, 2, 0, 2, 3, true, 2)
	var firstQueues struct {
		ServiceQueues []ServiceQueue `json:"service_queues"`
	}
	m301DecodeData(t, queuePage, &firstQueues)
	m301AssertIDs(t, []string{thirdQueue.ID, firstQueue.ID}, m301QueueIDs(firstQueues.ServiceQueues))

	finalQueuePage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceQueuesList,
		WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: 2},
	)
	m301AssertSuccess(t, finalQueuePage, WorkbenchOperationServiceQueuesList, true)
	m301AssertPage(t, finalQueuePage.Page, WorkbenchMaxPageLimit, 2, 1, 3, false, 0)
	var finalQueues struct {
		ServiceQueues []ServiceQueue `json:"service_queues"`
	}
	m301DecodeData(t, finalQueuePage, &finalQueues)
	m301AssertIDs(t, []string{secondQueue.ID}, m301QueueIDs(finalQueues.ServiceQueues))

	emptyQueuePage := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceQueuesList,
		WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: 3},
	)
	m301AssertPage(t, emptyQueuePage.Page, WorkbenchMaxPageLimit, 3, 0, 3, false, 0)
	if afterReads := store.Snapshot(); !reflect.DeepEqual(afterReads, beforeReads) {
		t.Fatal("paginated service reads changed CRM state or audit")
	}
}

func TestWorkbenchServiceRequestListMaximumEnvelopePagination(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	m301SeedMaximumServiceRequests(t, store, refs, WorkbenchMaxPageLimit)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	total, requests, err := crmServiceRequestList(store, crmServiceRequestFilter{
		Limit: WorkbenchMaxPageLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != WorkbenchMaxPageLimit || len(requests) != WorkbenchMaxPageLimit {
		t.Fatalf("maximum service requests = %d/%d, want %d", total, len(requests), WorkbenchMaxPageLimit)
	}
	getResult := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsGet,
		WorkbenchIDArguments{ID: requests[0].ID},
	)
	m301AssertSuccess(t, getResult, WorkbenchOperationServiceRequestsGet, false)
	m301AssertServiceRequestProjection(t, getResult, requests[0])
	m301AssertMaximumServiceRequestTextProjection(t, workbenchServiceRequestProject(requests[0]))

	offset := 0
	collected := make([]string, 0, total)
	for offset < total {
		arguments := WorkbenchServiceRequestListArguments{
			Limit:  WorkbenchMaxPageLimit,
			Offset: offset,
		}
		result := m301Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationServiceRequestsList,
			arguments,
		)
		repeated := m301Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationServiceRequestsList,
			arguments,
		)
		m301AssertSuccess(t, result, WorkbenchOperationServiceRequestsList, true)
		if !reflect.DeepEqual(result, repeated) {
			t.Fatalf("service request page at offset %d was not stable", offset)
		}
		m301AssertResultWithinEnvelope(t, result)

		var data workbenchServiceRequestListData
		m301DecodeData(t, result, &data)
		if len(data.ServiceRequests) == 0 {
			t.Fatalf("service request page at offset %d made no progress", offset)
		}
		for _, projected := range data.ServiceRequests {
			m301AssertMaximumServiceRequestTextProjection(t, projected)
		}
		returned := len(data.ServiceRequests)
		m301AssertPage(
			t,
			result.Page,
			WorkbenchMaxPageLimit,
			offset,
			returned,
			total,
			offset+returned < total,
			m301NextOffset(offset, returned, total),
		)
		m301AssertIDs(
			t,
			m301RequestIDs(requests[offset:offset+returned]),
			m301RequestProjectionIDs(data.ServiceRequests),
		)
		collected = append(collected, m301RequestProjectionIDs(data.ServiceRequests)...)

		if offset+returned < total {
			candidate := append(
				append([]workbenchServiceRequestProjection{}, data.ServiceRequests...),
				workbenchServiceRequestProject(requests[offset+returned]),
			)
			m301AssertListExtensionOverflows(
				t,
				WorkbenchOperationServiceRequestsList,
				workbenchServiceRequestListData{ServiceRequests: candidate},
				newWorkbenchPage(
					WorkbenchMaxPageLimit,
					offset,
					total,
					len(candidate),
				),
			)
			offset = result.Page.NextOffset
		} else {
			offset += returned
		}
	}
	m301AssertIDs(t, m301RequestIDs(requests), collected)
	m301AssertTerminalServiceRequestPage(t, dispatcher, total)
}

func TestWorkbenchServiceRequestMaximumEscapedUpdateCommitsAndReturnsBounded(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	execution := WorkbenchExecutionContext{OperatorID: "operator-r1"}
	maximumText := strings.Repeat("<", 4000)
	request := m301CreateMaximumServiceRequest(t, store, refs)

	updatedOwner := "updated-owner"
	arguments := WorkbenchServiceRequestUpdateArguments{
		RequestID: request.ID,
		Update: ServiceRequestUpdate{
			ExpectedVersion: request.Version,
			Owner:           &updatedOwner,
		},
	}
	beforeUpdate := store.Snapshot()
	updateResult := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceRequestsUpdate,
		arguments,
	)
	m301AssertSuccess(t, updateResult, WorkbenchOperationServiceRequestsUpdate, false)
	updated, err := crmServiceRequestGet(store, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Description != maximumText ||
		updated.Resolution != maximumText ||
		updated.Owner != updatedOwner ||
		updated.Version != request.Version+1 {
		t.Fatalf("maximum updated service request = %+v", updated)
	}
	m301AssertServiceRequestProjection(t, updateResult, updated)
	m301AssertMaximumServiceRequestTextProjection(t, workbenchServiceRequestProject(updated))
	m301AssertAuditDelta(
		t,
		store,
		beforeUpdate,
		"service_request.update",
		"service_request",
		request.ID,
		"operator-r1",
	)

	afterUpdate := store.Snapshot()
	repeated := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceRequestsUpdate,
		arguments,
	)
	m301AssertFailure(t, repeated, WorkbenchOperationServiceRequestsUpdate, WorkbenchErrorConflict)
	if afterRepeated := store.Snapshot(); !reflect.DeepEqual(afterRepeated, afterUpdate) {
		t.Fatal("stale maximum service request update changed CRM state or audit")
	}
}

func TestWorkbenchServiceRequestMaximumEscapedTransitionCommitsOnceAndReturnsBounded(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	maximumText := strings.Repeat("<", 4000)
	request := m301CreateMaximumServiceRequest(t, store, refs)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	execution := WorkbenchExecutionContext{
		PermissionRequestID: "permission_maximum_transition",
		PermissionDecision:  AutomationPermissionAllowOnce,
		OperatorID:          "human-operator",
	}
	arguments := WorkbenchServiceRequestTransitionArguments{
		RequestID:       request.ID,
		ExpectedVersion: request.Version,
		FromStatus:      request.Status,
		ToStatus:        ServiceRequestStatusAssigned,
	}

	beforeTransition := store.Snapshot()
	result := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationServiceRequestsTransition,
		arguments,
	)
	m301AssertSuccess(t, result, WorkbenchOperationServiceRequestsTransition, false)
	transitioned, err := crmServiceRequestGet(store, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if transitioned.Description != maximumText ||
		transitioned.Resolution != maximumText ||
		transitioned.Status != ServiceRequestStatusAssigned ||
		transitioned.Version != request.Version+1 {
		t.Fatalf("maximum transitioned service request = %+v", transitioned)
	}
	m301AssertServiceRequestProjection(t, result, transitioned)
	m301AssertMaximumServiceRequestTextProjection(t, workbenchServiceRequestProject(transitioned))
	m301AssertAuditDelta(
		t,
		store,
		beforeTransition,
		"service_request.transition",
		"service_request",
		request.ID,
		"human-operator",
	)

	afterTransition := store.Snapshot()
	repeated := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationServiceRequestsTransition,
		arguments,
	)
	m301AssertFailure(
		t,
		repeated,
		WorkbenchOperationServiceRequestsTransition,
		WorkbenchErrorConflict,
	)
	if afterRepeated := store.Snapshot(); !reflect.DeepEqual(afterRepeated, afterTransition) {
		t.Fatal("stale maximum service request transition changed CRM state or audit")
	}
}

func TestWorkbenchServiceQueueListMaximumEnvelopePagination(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	m301SeedMaximumServiceQueues(t, store, WorkbenchMaxPageLimit)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)

	queues, err := crmServiceQueueList(store)
	if err != nil {
		t.Fatal(err)
	}
	total := len(queues)
	if total != WorkbenchMaxPageLimit {
		t.Fatalf("maximum service queues = %d, want %d", total, WorkbenchMaxPageLimit)
	}

	offset := 0
	collected := make([]string, 0, total)
	for offset < total {
		arguments := WorkbenchPageArguments{
			Limit:  WorkbenchMaxPageLimit,
			Offset: offset,
		}
		result := m301Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationServiceQueuesList,
			arguments,
		)
		repeated := m301Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationServiceQueuesList,
			arguments,
		)
		m301AssertSuccess(t, result, WorkbenchOperationServiceQueuesList, true)
		if !reflect.DeepEqual(result, repeated) {
			t.Fatalf("service queue page at offset %d was not stable", offset)
		}
		m301AssertResultWithinEnvelope(t, result)

		var data workbenchServiceQueueListData
		m301DecodeData(t, result, &data)
		if len(data.ServiceQueues) == 0 {
			t.Fatalf("service queue page at offset %d made no progress", offset)
		}
		returned := len(data.ServiceQueues)
		m301AssertPage(
			t,
			result.Page,
			WorkbenchMaxPageLimit,
			offset,
			returned,
			total,
			offset+returned < total,
			m301NextOffset(offset, returned, total),
		)
		m301AssertIDs(
			t,
			m301QueueIDs(queues[offset:offset+returned]),
			m301QueueIDs(data.ServiceQueues),
		)
		collected = append(collected, m301QueueIDs(data.ServiceQueues)...)

		if offset+returned < total {
			candidate := append(
				append([]ServiceQueue{}, data.ServiceQueues...),
				queues[offset+returned],
			)
			m301AssertListExtensionOverflows(
				t,
				WorkbenchOperationServiceQueuesList,
				workbenchServiceQueueListData{ServiceQueues: candidate},
				newWorkbenchPage(
					WorkbenchMaxPageLimit,
					offset,
					total,
					len(candidate),
				),
			)
			offset = result.Page.NextOffset
		} else {
			offset += returned
		}
	}
	m301AssertIDs(t, m301QueueIDs(queues), collected)
	m301AssertTerminalServiceQueuePage(t, dispatcher, total)
}

func TestWorkbenchServiceOperationsRequireCRM(t *testing.T) {
	m301ResetOperationDeny(t)
	owner := "operator"
	queueName := "queue"
	tests := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		execution WorkbenchExecutionContext
		arguments any
	}{
		{
			name: "service requests list", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceRequestsList,
			arguments: WorkbenchServiceRequestListArguments{Limit: 1},
		},
		{
			name: "service requests get", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceRequestsGet,
			arguments: WorkbenchIDArguments{ID: "service_00000001"},
		},
		{
			name: "service requests create", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsCreate,
			arguments: WorkbenchServiceRequestCreateArguments{Request: ServiceRequestCreate{
				CandidateID: "cand_00000001", Category: ServiceRequestCategoryGeneral,
				Description: "help",
			}},
		},
		{
			name: "service requests update", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsUpdate,
			arguments: WorkbenchServiceRequestUpdateArguments{
				RequestID: "service_00000001",
				Update:    ServiceRequestUpdate{ExpectedVersion: 1, Owner: &owner},
			},
		},
		{
			name: "service requests transition", tool: AutomationToolWorkbenchAllowOnce,
			operation: WorkbenchOperationServiceRequestsTransition,
			execution: WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowOnce,
				OperatorID:         "human-operator",
			},
			arguments: WorkbenchServiceRequestTransitionArguments{
				RequestID: "service_00000001", ExpectedVersion: 1,
				FromStatus: ServiceRequestStatusOpen, ToStatus: ServiceRequestStatusAssigned,
			},
		},
		{
			name: "service queues list", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceQueuesList,
			arguments: WorkbenchPageArguments{Limit: 1},
		},
		{
			name: "service queues get", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceQueuesGet,
			arguments: WorkbenchIDArguments{ID: "queue_00000001"},
		},
		{
			name: "service queues create", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesCreate,
			arguments: WorkbenchServiceQueueCreateArguments{Queue: ServiceQueueCreate{
				Name: "queue", Categories: []ServiceRequestCategory{}, SLASeconds: 60,
			}},
		},
		{
			name: "service queues update", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesUpdate,
			arguments: WorkbenchServiceQueueUpdateArguments{
				QueueID: "queue_00000001",
				Update:  ServiceQueueUpdate{ExpectedVersion: 1, Name: &queueName},
			},
		},
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(nil)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := m301Dispatch(
				t,
				dispatcher,
				test.execution,
				test.tool,
				test.operation,
				test.arguments,
			)
			m301AssertFailure(t, result, test.operation, WorkbenchErrorUnavailable)
			if !result.Error.Retryable || result.Error.Message != "CRM store is unavailable" {
				t.Fatalf("nil CRM result = %+v", result)
			}
		})
	}
}

func TestWorkbenchServiceValidationNotFoundAndConflict(t *testing.T) {
	m301ResetOperationDeny(t)
	for _, test := range []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
	}{
		{
			name: "request page too large", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceRequestsList,
			arguments: `{"limit":101,"offset":0}`,
		},
		{
			name: "queue negative offset", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceQueuesList,
			arguments: `{"limit":1,"offset":-1}`,
		},
		{
			name: "invalid request category", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsCreate,
			arguments: `{"request":{"candidate_id":"cand_00000001","category":"unknown","description":"help"}}`,
		},
		{
			name: "trusted request actor", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsCreate,
			arguments: `{"request":{"candidate_id":"cand_00000001","category":"general","description":"help","actor":"model"}}`,
		},
		{
			name: "zero queue SLA", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesCreate,
			arguments: `{"queue":{"name":"queue","categories":[],"sla_seconds":0}}`,
		},
		{
			name: "zero transition version", tool: AutomationToolWorkbenchAllowOnce,
			operation: WorkbenchOperationServiceRequestsTransition,
			arguments: `{"request_id":"service_00000001","expected_version":0,"from_status":"open","to_status":"assigned"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := m301DecodeRaw(test.tool, test.operation, test.arguments); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("decode error = %v, want automation validation", err)
			}
		})
	}

	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	execution := WorkbenchExecutionContext{OperatorID: "operator-r1"}
	queueResult := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceQueuesCreate,
		WorkbenchServiceQueueCreateArguments{Queue: ServiceQueueCreate{
			Name: "General queue", Categories: []ServiceRequestCategory{ServiceRequestCategoryGeneral},
			SLASeconds: 600,
		}},
	)
	var queue ServiceQueue
	m301DecodeData(t, queueResult, &queue)
	requestResult := m301Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchMutate,
		WorkbenchOperationServiceRequestsCreate,
		WorkbenchServiceRequestCreateArguments{Request: ServiceRequestCreate{
			CandidateID: refs.Candidate.ID, Category: ServiceRequestCategoryGeneral,
			Description: "help",
		}},
	)
	var requestProjection workbenchServiceRequestProjection
	m301DecodeData(t, requestResult, &requestProjection)
	request, err := crmServiceRequestGet(store, requestProjection.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeErrors := store.Snapshot()

	owner := "owner"
	queueName := "renamed"
	errorCases := []struct {
		name      string
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
		code      WorkbenchErrorCode
	}{
		{
			name: "request get not found", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceRequestsGet,
			arguments: WorkbenchIDArguments{ID: "service_99999999"},
			code:      WorkbenchErrorNotFound,
		},
		{
			name: "queue get not found", tool: AutomationToolWorkbenchQuery,
			operation: WorkbenchOperationServiceQueuesGet,
			arguments: WorkbenchIDArguments{ID: "queue_99999999"},
			code:      WorkbenchErrorNotFound,
		},
		{
			name: "request create reference not found", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsCreate,
			arguments: WorkbenchServiceRequestCreateArguments{Request: ServiceRequestCreate{
				CandidateID: "cand_99999999", Category: ServiceRequestCategoryGeneral,
				Description: "help",
			}},
			code: WorkbenchErrorNotFound,
		},
		{
			name: "request update not found", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsUpdate,
			arguments: WorkbenchServiceRequestUpdateArguments{
				RequestID: "service_99999999",
				Update:    ServiceRequestUpdate{ExpectedVersion: 1, Owner: &owner},
			},
			code: WorkbenchErrorNotFound,
		},
		{
			name: "queue update not found", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesUpdate,
			arguments: WorkbenchServiceQueueUpdateArguments{
				QueueID: "queue_99999999",
				Update:  ServiceQueueUpdate{ExpectedVersion: 1, Name: &queueName},
			},
			code: WorkbenchErrorNotFound,
		},
		{
			name: "empty request update", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsUpdate,
			arguments: WorkbenchServiceRequestUpdateArguments{
				RequestID: request.ID,
				Update:    ServiceRequestUpdate{ExpectedVersion: request.Version},
			},
			code: WorkbenchErrorInvalidArguments,
		},
		{
			name: "empty queue update", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesUpdate,
			arguments: WorkbenchServiceQueueUpdateArguments{
				QueueID: queue.ID,
				Update:  ServiceQueueUpdate{ExpectedVersion: queue.Version},
			},
			code: WorkbenchErrorInvalidArguments,
		},
		{
			name: "request text too large", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsCreate,
			arguments: WorkbenchServiceRequestCreateArguments{Request: ServiceRequestCreate{
				CandidateID: refs.Candidate.ID, Category: ServiceRequestCategoryGeneral,
				Description: strings.Repeat("x", 4001),
			}},
			code: WorkbenchErrorInvalidArguments,
		},
		{
			name: "duplicate queue category", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesCreate,
			arguments: WorkbenchServiceQueueCreateArguments{Queue: ServiceQueueCreate{
				Name: "Duplicate category",
				Categories: []ServiceRequestCategory{
					ServiceRequestCategoryGeneral,
					ServiceRequestCategoryGeneral,
				},
				SLASeconds: 600,
			}},
			code: WorkbenchErrorInvalidArguments,
		},
		{
			name: "duplicate queue name", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesCreate,
			arguments: WorkbenchServiceQueueCreateArguments{Queue: ServiceQueueCreate{
				Name: queue.Name, Categories: []ServiceRequestCategory{}, SLASeconds: 600,
			}},
			code: WorkbenchErrorConflict,
		},
		{
			name: "stale request update", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceRequestsUpdate,
			arguments: WorkbenchServiceRequestUpdateArguments{
				RequestID: request.ID,
				Update:    ServiceRequestUpdate{ExpectedVersion: request.Version + 1, Owner: &owner},
			},
			code: WorkbenchErrorConflict,
		},
		{
			name: "stale queue update", tool: AutomationToolWorkbenchMutate,
			operation: WorkbenchOperationServiceQueuesUpdate,
			arguments: WorkbenchServiceQueueUpdateArguments{
				QueueID: queue.ID,
				Update:  ServiceQueueUpdate{ExpectedVersion: queue.Version + 1, Name: &queueName},
			},
			code: WorkbenchErrorConflict,
		},
	}
	for _, test := range errorCases {
		t.Run(test.name, func(t *testing.T) {
			result := m301Dispatch(
				t,
				dispatcher,
				execution,
				test.tool,
				test.operation,
				test.arguments,
			)
			m301AssertFailure(t, result, test.operation, test.code)
		})
	}
	if afterErrors := store.Snapshot(); !reflect.DeepEqual(afterErrors, beforeErrors) {
		t.Fatal("failed service operations changed CRM state or audit")
	}
}

func TestWorkbenchServiceOperationOverrideDenyDoesNotMutate(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	owner := "owner"
	name := "queue"
	tests := []struct {
		tool      AutomationToolName
		operation WorkbenchOperation
		execution WorkbenchExecutionContext
		arguments any
	}{
		{
			tool: AutomationToolWorkbenchQuery, operation: WorkbenchOperationServiceRequestsList,
			arguments: WorkbenchServiceRequestListArguments{Limit: 1},
		},
		{
			tool: AutomationToolWorkbenchQuery, operation: WorkbenchOperationServiceRequestsGet,
			arguments: WorkbenchIDArguments{ID: "service_00000001"},
		},
		{
			tool: AutomationToolWorkbenchMutate, operation: WorkbenchOperationServiceRequestsCreate,
			execution: WorkbenchExecutionContext{OperatorID: "operator"},
			arguments: WorkbenchServiceRequestCreateArguments{Request: ServiceRequestCreate{
				CandidateID: refs.Candidate.ID, Category: ServiceRequestCategoryGeneral,
				Description: "help",
			}},
		},
		{
			tool: AutomationToolWorkbenchMutate, operation: WorkbenchOperationServiceRequestsUpdate,
			execution: WorkbenchExecutionContext{OperatorID: "operator"},
			arguments: WorkbenchServiceRequestUpdateArguments{
				RequestID: "service_00000001",
				Update:    ServiceRequestUpdate{ExpectedVersion: 1, Owner: &owner},
			},
		},
		{
			tool: AutomationToolWorkbenchAllowOnce, operation: WorkbenchOperationServiceRequestsTransition,
			execution: WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowOnce,
				OperatorID:         "human-operator",
			},
			arguments: WorkbenchServiceRequestTransitionArguments{
				RequestID: "service_00000001", ExpectedVersion: 1,
				FromStatus: ServiceRequestStatusOpen, ToStatus: ServiceRequestStatusAssigned,
			},
		},
		{
			tool: AutomationToolWorkbenchQuery, operation: WorkbenchOperationServiceQueuesList,
			arguments: WorkbenchPageArguments{Limit: 1},
		},
		{
			tool: AutomationToolWorkbenchQuery, operation: WorkbenchOperationServiceQueuesGet,
			arguments: WorkbenchIDArguments{ID: "queue_00000001"},
		},
		{
			tool: AutomationToolWorkbenchMutate, operation: WorkbenchOperationServiceQueuesCreate,
			execution: WorkbenchExecutionContext{OperatorID: "operator"},
			arguments: WorkbenchServiceQueueCreateArguments{Queue: ServiceQueueCreate{
				Name: "queue", Categories: []ServiceRequestCategory{}, SLASeconds: 60,
			}},
		},
		{
			tool: AutomationToolWorkbenchMutate, operation: WorkbenchOperationServiceQueuesUpdate,
			execution: WorkbenchExecutionContext{OperatorID: "operator"},
			arguments: WorkbenchServiceQueueUpdateArguments{
				QueueID: "queue_00000001",
				Update:  ServiceQueueUpdate{ExpectedVersion: 1, Name: &name},
			},
		},
	}
	denied := make(map[string]struct{}, len(tests))
	for _, test := range tests {
		denied[workbenchOperationDenyKey(test.tool, test.operation)] = struct{}{}
	}
	applyWorkbenchOperationDenySet(denied)

	before := store.Snapshot()
	for _, test := range tests {
		result := m301Dispatch(
			t,
			dispatcher,
			test.execution,
			test.tool,
			test.operation,
			test.arguments,
		)
		m301AssertFailure(t, result, test.operation, WorkbenchErrorUnavailable)
		if result.Error.Retryable || result.Error.Message != workbenchUnavailableMessage {
			t.Fatalf("denied %s/%s result = %+v", test.tool, test.operation, result)
		}
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("operator-denied service operations changed CRM state or audit")
	}
}

func TestWorkbenchServiceRequestTransitionRequiresFreshHumanApprovalAndAppliesOnce(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	request, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategoryGeneral,
		Description: "assign this request",
		Actor:       "setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	arguments := WorkbenchServiceRequestTransitionArguments{
		RequestID: request.ID, ExpectedVersion: request.Version,
		FromStatus: request.Status, ToStatus: ServiceRequestStatusAssigned,
	}
	beforeApproval := store.Snapshot()

	for _, test := range []struct {
		name      string
		execution WorkbenchExecutionContext
		code      WorkbenchErrorCode
	}{
		{name: "missing approval", code: WorkbenchErrorAllowOnceRequired},
		{
			name: "reusable run approval",
			execution: WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowRun,
				OperatorID:         "human-operator",
			},
			code: WorkbenchErrorAllowOnceRequired,
		},
		{
			name: "explicit denial",
			execution: WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionDenyOnce,
				OperatorID:         "human-operator",
			},
			code: WorkbenchErrorForbidden,
		},
		{
			name: "empty human operator",
			execution: WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowOnce,
				OperatorID:         "   ",
			},
			code: WorkbenchErrorForbidden,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := m301Dispatch(
				t,
				dispatcher,
				test.execution,
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationServiceRequestsTransition,
				arguments,
			)
			m301AssertFailure(t, result, WorkbenchOperationServiceRequestsTransition, test.code)
			if after := store.Snapshot(); !reflect.DeepEqual(after, beforeApproval) {
				t.Fatal("service request changed before fresh human allow_once approval")
			}
		})
	}

	approved := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{
			PermissionRequestID: "permission_00000001",
			PermissionDecision:  AutomationPermissionAllowOnce,
			OperatorID:          "  human-operator  ",
		},
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationServiceRequestsTransition,
		arguments,
	)
	m301AssertSuccess(t, approved, WorkbenchOperationServiceRequestsTransition, false)
	m301AssertJSONKeys(t, approved.Data,
		"id", "candidate_id", "category", "priority", "description",
		"status", "requires_human", "resolution", "version", "created_at", "updated_at",
	)
	transitioned, err := crmServiceRequestGet(store, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if transitioned.Status != ServiceRequestStatusAssigned ||
		transitioned.Version != request.Version+1 {
		t.Fatalf("approved transition = %+v", transitioned)
	}
	m301AssertServiceRequestProjection(t, approved, transitioned)
	afterApproval := store.Snapshot()
	if len(afterApproval.ServiceRequests) != len(beforeApproval.ServiceRequests) ||
		len(afterApproval.AuditEvents) != len(beforeApproval.AuditEvents)+1 {
		t.Fatalf(
			"approved transition state/audit delta = requests %d->%d audit %d->%d",
			len(beforeApproval.ServiceRequests),
			len(afterApproval.ServiceRequests),
			len(beforeApproval.AuditEvents),
			len(afterApproval.AuditEvents),
		)
	}
	m301AssertLastAudit(
		t,
		afterApproval,
		"service_request.transition",
		"service_request",
		request.ID,
		"human-operator",
	)

	repeated := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{
			PermissionDecision: AutomationPermissionAllowOnce,
			OperatorID:         "human-operator",
		},
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationServiceRequestsTransition,
		arguments,
	)
	m301AssertFailure(t, repeated, WorkbenchOperationServiceRequestsTransition, WorkbenchErrorConflict)
	if afterRepeated := store.Snapshot(); !reflect.DeepEqual(afterRepeated, afterApproval) {
		t.Fatal("repeated approved transition changed CRM state or audit")
	}

	staleArguments := WorkbenchServiceRequestTransitionArguments{
		RequestID: request.ID, ExpectedVersion: request.Version,
		FromStatus: ServiceRequestStatusAssigned, ToStatus: ServiceRequestStatusInProgress,
	}
	stale := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{
			PermissionDecision: AutomationPermissionAllowOnce,
			OperatorID:         "human-operator",
		},
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationServiceRequestsTransition,
		staleArguments,
	)
	m301AssertFailure(t, stale, WorkbenchOperationServiceRequestsTransition, WorkbenchErrorConflict)
	if afterStale := store.Snapshot(); !reflect.DeepEqual(afterStale, afterApproval) {
		t.Fatal("stale approved transition changed CRM state or audit")
	}
}

func TestWorkbenchServiceRequestTransitionConcurrentDuplicateAppliesOnce(t *testing.T) {
	m301ResetOperationDeny(t)
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	request, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategoryGeneral,
		Description: "concurrent assignment",
		Actor:       "setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := m301Decode(
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationServiceRequestsTransition,
		WorkbenchServiceRequestTransitionArguments{
			RequestID: request.ID, ExpectedVersion: request.Version,
			FromStatus: request.Status, ToStatus: ServiceRequestStatusAssigned,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(store)
	execution := WorkbenchExecutionContext{
		PermissionDecision: AutomationPermissionAllowOnce,
		OperatorID:         "human-operator",
	}
	before := store.Snapshot()
	start := make(chan struct{})
	results := make(chan WorkbenchToolResult, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			results <- dispatcher.Dispatch(context.Background(), execution, decoded)
		}()
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for result := range results {
		switch {
		case result.OK:
			successes++
		case result.Error != nil && result.Error.Code == WorkbenchErrorConflict:
			conflicts++
		default:
			t.Fatalf("concurrent transition result = %+v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent transition successes=%d conflicts=%d", successes, conflicts)
	}
	after := store.Snapshot()
	if len(after.AuditEvents) != len(before.AuditEvents)+1 ||
		len(after.ServiceRequests) != len(before.ServiceRequests) {
		t.Fatalf(
			"concurrent transition state/audit delta = requests %d->%d audit %d->%d",
			len(before.ServiceRequests),
			len(after.ServiceRequests),
			len(before.AuditEvents),
			len(after.AuditEvents),
		)
	}
	current, err := crmServiceRequestGet(store, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != ServiceRequestStatusAssigned || current.Version != request.Version+1 {
		t.Fatalf("concurrent transition current request = %+v", current)
	}
	m301AssertLastAudit(
		t,
		after,
		"service_request.transition",
		"service_request",
		request.ID,
		"human-operator",
	)
}

func m301ResetOperationDeny(t *testing.T) {
	t.Helper()
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	t.Cleanup(func() {
		applyWorkbenchOperationDenySet(map[string]struct{}{})
	})
}

func m301Dispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	decoded, err := m301Decode(tool, operation, arguments)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", tool, operation, err)
	}
	return dispatcher.Dispatch(context.Background(), execution, decoded)
}

func m301Decode(
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) (WorkbenchDecodedToolRequest, error) {
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	return m301DecodeRaw(tool, operation, string(argumentsJSON))
}

func m301DecodeRaw(
	tool AutomationToolName,
	operation WorkbenchOperation,
	argumentsJSON string,
) (WorkbenchDecodedToolRequest, error) {
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: argumentsJSON,
	})
	if err != nil {
		return WorkbenchDecodedToolRequest{}, err
	}
	return DecodeWorkbenchToolRequest(tool, raw)
}

func m301AssertSuccess(
	t *testing.T,
	result WorkbenchToolResult,
	operation WorkbenchOperation,
	withPage bool,
) {
	t.Helper()
	if !result.OK || result.Operation != operation || result.Error != nil || len(result.Data) == 0 {
		t.Fatalf("%s result = %+v, want success", operation, result)
	}
	if withPage != (result.Page != nil) {
		t.Fatalf("%s page = %+v, withPage=%t", operation, result.Page, withPage)
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		t.Fatalf("%s result validation = %v", operation, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"operation", "ok", "data"}
	if withPage {
		wantKeys = append(wantKeys, "page")
	}
	m301AssertJSONKeys(t, encoded, wantKeys...)
}

func m301AssertFailure(
	t *testing.T,
	result WorkbenchToolResult,
	operation WorkbenchOperation,
	code WorkbenchErrorCode,
) {
	t.Helper()
	if result.OK || result.Operation != operation || result.Error == nil ||
		result.Error.Code != code || len(result.Data) != 0 || result.Page != nil {
		t.Fatalf("%s result = %+v, want failure %s", operation, result, code)
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		t.Fatalf("%s failure validation = %v", operation, err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	m301AssertJSONKeys(t, encoded, "operation", "ok", "error")
	errorJSON, err := json.Marshal(result.Error)
	if err != nil {
		t.Fatal(err)
	}
	errorKeys := []string{"code", "message"}
	if result.Error.Retryable {
		errorKeys = append(errorKeys, "retryable")
	}
	m301AssertJSONKeys(t, errorJSON, errorKeys...)
}

func m301AssertJSONKeys(t *testing.T, raw []byte, want ...string) {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode JSON object: %v; data=%s", err, raw)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	want = append([]string{}, want...)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v; data=%s", got, want, raw)
	}
}

func m301DecodeData(t *testing.T, result WorkbenchToolResult, target any) {
	t.Helper()
	if err := json.Unmarshal(result.Data, target); err != nil {
		t.Fatalf("decode %s data: %v; data=%s", result.Operation, err, result.Data)
	}
}

func m301AssertPage(
	t *testing.T,
	page *WorkbenchPage,
	limit int,
	offset int,
	returned int,
	total int,
	hasMore bool,
	nextOffset int,
) {
	t.Helper()
	want := &WorkbenchPage{
		Limit:      limit,
		Offset:     offset,
		Returned:   returned,
		Total:      total,
		HasMore:    hasMore,
		NextOffset: nextOffset,
	}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("page = %+v, want %+v", page, want)
	}
}

func m301AssertAuditDelta(
	t *testing.T,
	store *CRMStore,
	before CRMState,
	operation string,
	entityType string,
	entityID string,
	actor string,
) {
	t.Helper()
	after := store.Snapshot()
	if len(after.AuditEvents) != len(before.AuditEvents)+1 {
		t.Fatalf("audit count = %d, want %d", len(after.AuditEvents), len(before.AuditEvents)+1)
	}
	m301AssertLastAudit(t, after, operation, entityType, entityID, actor)
}

func m301AssertLastAudit(
	t *testing.T,
	state CRMState,
	operation string,
	entityType string,
	entityID string,
	actor string,
) {
	t.Helper()
	if len(state.AuditEvents) == 0 {
		t.Fatal("CRM audit is empty")
	}
	audit := state.AuditEvents[len(state.AuditEvents)-1]
	if audit.Operation != operation || audit.EntityType != entityType ||
		audit.EntityID != entityID || audit.Actor != actor {
		t.Fatalf(
			"last audit = %+v, want operation=%q entity_type=%q entity_id=%q actor=%q",
			audit,
			operation,
			entityType,
			entityID,
			actor,
		)
	}
}

func m301AssertIDs(t *testing.T, want, got []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs = %v, want %v", got, want)
	}
}

func m301RequestIDs(requests []ServiceRequest) []string {
	ids := make([]string, len(requests))
	for index := range requests {
		ids[index] = requests[index].ID
	}
	return ids
}

func m301RequestProjectionIDs(requests []workbenchServiceRequestProjection) []string {
	ids := make([]string, len(requests))
	for index := range requests {
		ids[index] = requests[index].ID
	}
	return ids
}

func m301QueueIDs(queues []ServiceQueue) []string {
	ids := make([]string, len(queues))
	for index := range queues {
		ids[index] = queues[index].ID
	}
	return ids
}

func m301Bool(value bool) *bool {
	return &value
}

func m301CreateMaximumServiceRequest(
	t *testing.T,
	store *CRMStore,
	refs serviceRequestTestRefs,
) ServiceRequest {
	t.Helper()
	maximumText := strings.Repeat("<", 4000)
	request, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		JobID:       refs.Job.ID,
		Category:    ServiceRequestCategoryGeneral,
		Priority:    JobPriorityUrgent,
		Description: maximumText,
		Owner:       strings.Repeat("<", 80),
		Actor:       "setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err = crmServiceRequestUpdate(store, request.ID, ServiceRequestUpdate{
		ExpectedVersion: request.Version,
		Resolution:      &maximumText,
		Actor:           "setup",
	})
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func m301SeedMaximumServiceRequests(
	t *testing.T,
	store *CRMStore,
	refs serviceRequestTestRefs,
	count int,
) {
	t.Helper()
	description := strings.Repeat("<", 4000)
	owner := strings.Repeat("<", 80)
	base := time.Now().UTC().Add(-time.Duration(count+1) * time.Second)
	err := store.UpdateWithMeta(CRMUpdateMeta{
		Actor:     "setup",
		Operation: "service_request.maximum_page_seed",
	}, func(state *CRMState, allocator *IDAllocator) error {
		for index := range count {
			id, err := allocator.Next(IDKindServiceRequest)
			if err != nil {
				return err
			}
			createdAt := base.Add(time.Duration(index) * time.Second)
			dueAt := createdAt.Add(24 * time.Hour)
			state.ServiceRequests = append(state.ServiceRequests, ServiceRequest{
				ID:            id,
				CandidateID:   refs.Candidate.ID,
				JobID:         refs.Job.ID,
				Category:      ServiceRequestCategorySafety,
				Priority:      JobPriorityUrgent,
				Description:   description,
				Owner:         owner,
				Status:        ServiceRequestStatusOpen,
				RequiresHuman: true,
				DueAt:         &dueAt,
				Resolution:    description,
				Version:       1,
				CreatedAt:     createdAt,
				UpdatedAt:     createdAt,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func m301SeedMaximumServiceQueues(t *testing.T, store *CRMStore, count int) {
	t.Helper()
	owner := strings.Repeat("<", 80)
	categories := []ServiceRequestCategory{
		ServiceRequestCategoryGeneral,
		ServiceRequestCategoryTransport,
		ServiceRequestCategoryAccommodation,
		ServiceRequestCategoryEquipment,
		ServiceRequestCategorySchedule,
		ServiceRequestCategoryDocument,
		ServiceRequestCategorySafety,
		ServiceRequestCategoryDispute,
		ServiceRequestCategoryFund,
		ServiceRequestCategoryOther,
	}
	base := time.Now().UTC().Add(-time.Duration(count+1) * time.Second)
	err := store.UpdateWithMeta(CRMUpdateMeta{
		Actor:     "setup",
		Operation: "service_queue.maximum_page_seed",
	}, func(state *CRMState, allocator *IDAllocator) error {
		for index := range count {
			id, err := allocator.Next(IDKindServiceQueue)
			if err != nil {
				return err
			}
			createdAt := base.Add(time.Duration(index) * time.Second)
			state.ServiceQueues = append(state.ServiceQueues, ServiceQueue{
				ID:         id,
				Name:       strings.Repeat("<", 150) + fmt.Sprintf("queue%05d", index),
				Owner:      owner,
				Categories: append([]ServiceRequestCategory{}, categories...),
				SLASeconds: int64((365 * 24 * time.Hour) / time.Second),
				Enabled:    true,
				Version:    1,
				CreatedAt:  createdAt,
				UpdatedAt:  createdAt,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func m301AssertResultWithinEnvelope(t *testing.T, result WorkbenchToolResult) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		t.Fatalf("encoded result bytes = %d, want <= %d", len(encoded), WorkbenchMaxResultJSONBytes)
	}
}

func m301AssertServiceRequestProjection(
	t *testing.T,
	result WorkbenchToolResult,
	request ServiceRequest,
) {
	t.Helper()
	var projected workbenchServiceRequestProjection
	m301DecodeData(t, result, &projected)
	want := workbenchServiceRequestProject(request)
	if !reflect.DeepEqual(projected, want) {
		t.Fatalf("service request projection = %+v, want %+v", projected, want)
	}
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != string(expected) {
		t.Fatalf("service request projection JSON = %s, want %s", result.Data, expected)
	}
	m301AssertResultWithinEnvelope(t, result)
}

func m301AssertMaximumServiceRequestTextProjection(
	t *testing.T,
	projected workbenchServiceRequestProjection,
) {
	t.Helper()
	for _, item := range []struct {
		name string
		text workbenchServiceRequestTextProjection
	}{
		{name: "description", text: projected.Description},
		{name: "resolution", text: projected.Resolution},
	} {
		if item.text.OriginalCharacters != 4000 ||
			!item.text.Truncated ||
			item.text.Excerpt == "" ||
			len([]rune(item.text.Excerpt)) >= item.text.OriginalCharacters {
			t.Fatalf("maximum %s projection = %+v", item.name, item.text)
		}
		encoded, err := json.Marshal(item.text.Excerpt)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > workbenchServiceRequestTextJSONBytes {
			t.Fatalf(
				"maximum %s excerpt bytes = %d, want <= %d",
				item.name,
				len(encoded),
				workbenchServiceRequestTextJSONBytes,
			)
		}
	}
}

func m301AssertListExtensionOverflows(
	t *testing.T,
	operation WorkbenchOperation,
	data any,
	page *WorkbenchPage,
) {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(WorkbenchToolResult{
		Operation: operation,
		OK:        true,
		Data:      raw,
		Page:      page,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) <= WorkbenchMaxResultJSONBytes {
		t.Fatalf(
			"%s extended result bytes = %d, want > %d",
			operation,
			len(encoded),
			WorkbenchMaxResultJSONBytes,
		)
	}
}

func m301NextOffset(offset, returned, total int) int {
	if offset+returned >= total {
		return 0
	}
	return offset + returned
}

func m301AssertTerminalServiceRequestPage(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	total int,
) {
	t.Helper()
	result := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceRequestsList,
		WorkbenchServiceRequestListArguments{Limit: WorkbenchMaxPageLimit, Offset: total},
	)
	m301AssertSuccess(t, result, WorkbenchOperationServiceRequestsList, true)
	m301AssertPage(t, result.Page, WorkbenchMaxPageLimit, total, 0, total, false, 0)
	var data workbenchServiceRequestListData
	m301DecodeData(t, result, &data)
	if data.ServiceRequests == nil || len(data.ServiceRequests) != 0 {
		t.Fatalf("terminal service request page = %+v, want empty non-nil slice", data.ServiceRequests)
	}
	m301AssertResultWithinEnvelope(t, result)
}

func m301AssertTerminalServiceQueuePage(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	total int,
) {
	t.Helper()
	result := m301Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationServiceQueuesList,
		WorkbenchPageArguments{Limit: WorkbenchMaxPageLimit, Offset: total},
	)
	m301AssertSuccess(t, result, WorkbenchOperationServiceQueuesList, true)
	m301AssertPage(t, result.Page, WorkbenchMaxPageLimit, total, 0, total, false, 0)
	var data workbenchServiceQueueListData
	m301DecodeData(t, result, &data)
	if data.ServiceQueues == nil || len(data.ServiceQueues) != 0 {
		t.Fatalf("terminal service queue page = %+v, want empty non-nil slice", data.ServiceQueues)
	}
	m301AssertResultWithinEnvelope(t, result)
}
