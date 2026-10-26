package main

import (
	"errors"
	"sort"
	"strings"
	"time"
)

type ServiceRequestCreate struct {
	CandidateID      string                 `json:"candidate_id"`
	JobID            string                 `json:"job_id,omitempty"`
	OnboardingCaseID string                 `json:"onboarding_case_id,omitempty"`
	SourceEventID    string                 `json:"source_event_id,omitempty"`
	Category         ServiceRequestCategory `json:"category"`
	Priority         JobPriority            `json:"priority,omitempty"`
	Description      string                 `json:"description"`
	Owner            string                 `json:"owner,omitempty"`
	RequiresHuman    bool                   `json:"requires_human,omitempty"`
	DueAt            *time.Time             `json:"due_at,omitempty"`
	Actor            string                 `json:"-"`
}

type ServiceRequestUpdate struct {
	ExpectedVersion  uint64                  `json:"expected_version"`
	CandidateID      *string                 `json:"candidate_id,omitempty"`
	JobID            *string                 `json:"job_id,omitempty"`
	OnboardingCaseID *string                 `json:"onboarding_case_id,omitempty"`
	Category         *ServiceRequestCategory `json:"category,omitempty"`
	Priority         *JobPriority            `json:"priority,omitempty"`
	Description      *string                 `json:"description,omitempty"`
	Owner            *string                 `json:"owner,omitempty"`
	QueueID          *string                 `json:"queue_id,omitempty"`
	ClearQueue       bool                    `json:"clear_queue,omitempty"`
	RequiresHuman    *bool                   `json:"requires_human,omitempty"`
	DueAt            *time.Time              `json:"due_at,omitempty"`
	ClearDueAt       bool                    `json:"clear_due_at,omitempty"`
	Resolution       *string                 `json:"resolution,omitempty"`
	Actor            string                  `json:"-"`
}

type ServiceRequestTransition struct {
	ExpectedVersion uint64               `json:"expected_version"`
	FromStatus      ServiceRequestStatus `json:"from_status"`
	ToStatus        ServiceRequestStatus `json:"to_status"`
	Resolution      string               `json:"resolution,omitempty"`
	Actor           string               `json:"-"`
}

type ServiceQueueCreate struct {
	Name       string                   `json:"name"`
	Owner      string                   `json:"owner,omitempty"`
	Categories []ServiceRequestCategory `json:"categories"`
	SLASeconds int64                    `json:"sla_seconds"`
	Enabled    *bool                    `json:"enabled,omitempty"`
}

type ServiceQueueUpdate struct {
	ExpectedVersion uint64                    `json:"expected_version"`
	Name            *string                   `json:"name,omitempty"`
	Owner           *string                   `json:"owner,omitempty"`
	Categories      *[]ServiceRequestCategory `json:"categories,omitempty"`
	SLASeconds      *int64                    `json:"sla_seconds,omitempty"`
	Enabled         *bool                     `json:"enabled,omitempty"`
}

type ServiceRequestClaim struct {
	ExpectedVersion uint64 `json:"expected_version"`
}

type crmServiceRequestFilter struct {
	Query            string
	CandidateID      string
	JobID            string
	OnboardingCaseID string
	Category         ServiceRequestCategory
	Priority         JobPriority
	Status           ServiceRequestStatus
	Owner            *string
	RequiresHuman    *bool
	Overdue          *bool
	DueFrom          *time.Time
	DueTo            *time.Time
	Limit            int
	Offset           int
}

type crmServiceRequestNotFoundError struct {
	resource string
	id       string
}

func (e *crmServiceRequestNotFoundError) Error() string {
	if e.id == "" {
		return e.resource + " not found"
	}
	return e.resource + " " + e.id + " not found"
}

func crmServiceRequestCreate(store *CRMStore, create ServiceRequestCreate) (ServiceRequest, error) {
	if store == nil {
		return ServiceRequest{}, NewCRMValidationError("store", "is required")
	}
	normalized, err := crmServiceRequestNormalizeCreate(create)
	if err != nil {
		return ServiceRequest{}, err
	}

	var created ServiceRequest
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:         crmServiceRequestActor(normalized.Actor),
		Operation:     "service_request.create",
		EntityType:    "service_request",
		SourceEventID: normalized.SourceEventID,
	}, func(state *CRMState, allocator *IDAllocator) error {
		if err := crmServiceRequestValidateReferences(
			state,
			normalized.CandidateID,
			normalized.JobID,
			normalized.OnboardingCaseID,
			normalized.SourceEventID,
		); err != nil {
			return err
		}
		id, err := allocator.Next(IDKindServiceRequest)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created = ServiceRequest{
			ID:               id,
			CandidateID:      normalized.CandidateID,
			JobID:            normalized.JobID,
			OnboardingCaseID: normalized.OnboardingCaseID,
			SourceEventID:    normalized.SourceEventID,
			Category:         normalized.Category,
			Priority:         normalized.Priority,
			Description:      normalized.Description,
			Owner:            normalized.Owner,
			Status:           ServiceRequestStatusOpen,
			RequiresHuman:    normalized.RequiresHuman,
			DueAt:            cloneCRMTimePointer(normalized.DueAt),
			Version:          1,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		state.ServiceRequests = append(state.ServiceRequests, created)
		return nil
	})
	return created, err
}

func crmServiceRequestList(
	store *CRMStore,
	filter crmServiceRequestFilter,
) (int, []ServiceRequest, error) {
	if store == nil {
		return 0, nil, NewCRMValidationError("store", "is required")
	}
	normalized, err := crmServiceRequestNormalizeFilter(filter)
	if err != nil {
		return 0, nil, err
	}

	state := store.Snapshot()
	now := time.Now().UTC()
	requests := make([]ServiceRequest, 0, len(state.ServiceRequests))
	for _, request := range state.ServiceRequests {
		if crmServiceRequestMatches(request, normalized, now) {
			requests = append(requests, request)
		}
	}
	sort.Slice(requests, func(i, j int) bool {
		if requests[i].RequiresHuman != requests[j].RequiresHuman {
			return requests[i].RequiresHuman
		}
		leftPriority := crmServiceRequestPriorityRank(requests[i].Priority)
		rightPriority := crmServiceRequestPriorityRank(requests[j].Priority)
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		if (requests[i].DueAt == nil) != (requests[j].DueAt == nil) {
			return requests[i].DueAt != nil
		}
		if requests[i].DueAt != nil && !requests[i].DueAt.Equal(*requests[j].DueAt) {
			return requests[i].DueAt.Before(*requests[j].DueAt)
		}
		if !requests[i].UpdatedAt.Equal(requests[j].UpdatedAt) {
			return requests[i].UpdatedAt.After(requests[j].UpdatedAt)
		}
		return requests[i].ID < requests[j].ID
	})

	total := len(requests)
	return total, crmServiceRequestPage(requests, normalized.Offset, normalized.Limit), nil
}

func crmServiceRequestQueueMetrics(state CRMState, now time.Time) (int, time.Duration) {
	depth := 0
	var oldest time.Time
	for _, request := range state.ServiceRequests {
		if request.QueueID == "" || request.EnqueuedAt == nil || request.ClaimedAt != nil {
			continue
		}
		switch request.Status {
		case ServiceRequestStatusResolved,
			ServiceRequestStatusClosed,
			ServiceRequestStatusCancelled:
			continue
		}
		depth++
		if oldest.IsZero() || request.EnqueuedAt.Before(oldest) {
			oldest = *request.EnqueuedAt
		}
	}
	return operationalQueueAge(depth, oldest, now)
}

func crmServiceRequestGet(store *CRMStore, id string) (ServiceRequest, error) {
	if store == nil {
		return ServiceRequest{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return ServiceRequest{}, err
	}
	state := store.Snapshot()
	index := crmServiceRequestIndex(&state, id)
	if index < 0 {
		return ServiceRequest{}, &crmServiceRequestNotFoundError{resource: "service request", id: id}
	}
	return state.ServiceRequests[index], nil
}

func crmServiceRequestUpdate(
	store *CRMStore,
	id string,
	update ServiceRequestUpdate,
) (ServiceRequest, error) {
	if store == nil {
		return ServiceRequest{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return ServiceRequest{}, err
	}
	normalized, err := crmServiceRequestNormalizeUpdate(update)
	if err != nil {
		return ServiceRequest{}, err
	}

	var updated ServiceRequest
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      crmServiceRequestActor(normalized.Actor),
		Operation:  "service_request.update",
		EntityType: "service_request",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmServiceRequestIndex(state, id)
		if index < 0 {
			return &crmServiceRequestNotFoundError{resource: "service request", id: id}
		}
		current := state.ServiceRequests[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("service request version conflict")
		}

		candidateID := current.CandidateID
		jobID := current.JobID
		onboardingCaseID := current.OnboardingCaseID
		if normalized.CandidateID != nil {
			candidateID = *normalized.CandidateID
		}
		if normalized.JobID != nil {
			jobID = *normalized.JobID
		}
		if normalized.OnboardingCaseID != nil {
			onboardingCaseID = *normalized.OnboardingCaseID
		}
		if err := crmServiceRequestValidateReferences(
			state,
			candidateID,
			jobID,
			onboardingCaseID,
			current.SourceEventID,
		); err != nil {
			return err
		}

		current.CandidateID = candidateID
		current.JobID = jobID
		current.OnboardingCaseID = onboardingCaseID
		if normalized.Category != nil {
			current.Category = *normalized.Category
		}
		if normalized.Priority != nil {
			current.Priority = *normalized.Priority
		}
		if normalized.Description != nil {
			current.Description = *normalized.Description
		}
		if normalized.Owner != nil {
			current.Owner = *normalized.Owner
		}
		if normalized.QueueID != nil {
			queue, queueErr := crmServiceQueueForAssignment(state, *normalized.QueueID, current.Category)
			if queueErr != nil {
				return queueErr
			}
			now := crmServiceRequestMutationTime(current.UpdatedAt)
			current.QueueID = queue.ID
			current.EnqueuedAt = &now
			current.ClaimedBy = ""
			current.ClaimedAt = nil
		} else if normalized.ClearQueue {
			current.QueueID = ""
			current.EnqueuedAt = nil
			current.ClaimedBy = ""
			current.ClaimedAt = nil
		}
		if normalized.RequiresHuman != nil {
			current.RequiresHuman = *normalized.RequiresHuman
		}
		if crmServiceRequestCategoryRequiresHuman(current.Category) {
			current.RequiresHuman = true
		}
		if normalized.DueAt != nil {
			current.DueAt = cloneCRMTimePointer(normalized.DueAt)
		} else if normalized.ClearDueAt {
			current.DueAt = nil
		}
		if normalized.Resolution != nil {
			current.Resolution = *normalized.Resolution
		}
		current.Version++
		current.UpdatedAt = crmServiceRequestMutationTime(current.UpdatedAt)
		state.ServiceRequests[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmServiceQueueCreate(
	store *CRMStore,
	create ServiceQueueCreate,
	actor string,
) (ServiceQueue, error) {
	if store == nil {
		return ServiceQueue{}, NewCRMValidationError("store", "is required")
	}
	normalized, err := crmServiceQueueNormalizeCreate(create)
	if err != nil {
		return ServiceQueue{}, err
	}
	actor, err = crmNormalizeActor(actor)
	if err != nil {
		return ServiceQueue{}, err
	}

	var created ServiceQueue
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "service_queue.create",
		EntityType: "service_queue",
	}, func(state *CRMState, allocator *IDAllocator) error {
		if crmServiceQueueNameExists(state.ServiceQueues, normalized.Name, "") {
			return NewCRMConflictError("service queue name already exists")
		}
		id, allocateErr := allocator.Next(IDKindServiceQueue)
		if allocateErr != nil {
			return allocateErr
		}
		now := time.Now().UTC()
		enabled := true
		if normalized.Enabled != nil {
			enabled = *normalized.Enabled
		}
		created = ServiceQueue{
			ID:         id,
			Name:       normalized.Name,
			Owner:      normalized.Owner,
			Categories: append([]ServiceRequestCategory{}, normalized.Categories...),
			SLASeconds: normalized.SLASeconds,
			Enabled:    enabled,
			Version:    1,
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		state.ServiceQueues = append(state.ServiceQueues, created)
		return nil
	})
	return created, err
}

func crmServiceQueueList(store *CRMStore) ([]ServiceQueue, error) {
	if store == nil {
		return nil, NewCRMValidationError("store", "is required")
	}
	queues := append([]ServiceQueue{}, store.Snapshot().ServiceQueues...)
	sort.Slice(queues, func(i, j int) bool {
		if queues[i].Enabled != queues[j].Enabled {
			return queues[i].Enabled
		}
		if queues[i].UpdatedAt.Equal(queues[j].UpdatedAt) {
			return queues[i].ID < queues[j].ID
		}
		return queues[i].UpdatedAt.After(queues[j].UpdatedAt)
	})
	return queues, nil
}

func crmServiceQueueGet(store *CRMStore, id string) (ServiceQueue, error) {
	if store == nil {
		return ServiceQueue{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return ServiceQueue{}, err
	}
	state := store.Snapshot()
	index := crmServiceQueueIndex(state.ServiceQueues, id)
	if index < 0 {
		return ServiceQueue{}, &crmServiceRequestNotFoundError{resource: "service queue", id: id}
	}
	return state.ServiceQueues[index], nil
}

func crmServiceQueueUpdate(
	store *CRMStore,
	id string,
	update ServiceQueueUpdate,
	actor string,
) (ServiceQueue, error) {
	if store == nil {
		return ServiceQueue{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return ServiceQueue{}, err
	}
	normalized, err := crmServiceQueueNormalizeUpdate(update)
	if err != nil {
		return ServiceQueue{}, err
	}
	actor, err = crmNormalizeActor(actor)
	if err != nil {
		return ServiceQueue{}, err
	}

	var updated ServiceQueue
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "service_queue.update",
		EntityType: "service_queue",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmServiceQueueIndex(state.ServiceQueues, id)
		if index < 0 {
			return &crmServiceRequestNotFoundError{resource: "service queue", id: id}
		}
		current := state.ServiceQueues[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("service queue version conflict")
		}
		if normalized.Name != nil {
			if crmServiceQueueNameExists(state.ServiceQueues, *normalized.Name, current.ID) {
				return NewCRMConflictError("service queue name already exists")
			}
			current.Name = *normalized.Name
		}
		if normalized.Owner != nil {
			current.Owner = *normalized.Owner
		}
		if normalized.Categories != nil {
			current.Categories = append([]ServiceRequestCategory{}, (*normalized.Categories)...)
		}
		if normalized.SLASeconds != nil {
			current.SLASeconds = *normalized.SLASeconds
		}
		if normalized.Enabled != nil {
			current.Enabled = *normalized.Enabled
		}
		current.Version++
		current.UpdatedAt = crmServiceRequestMutationTime(current.UpdatedAt)
		state.ServiceQueues[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmServiceRequestClaim(
	store *CRMStore,
	id string,
	claim ServiceRequestClaim,
	actor string,
) (ServiceRequest, error) {
	if store == nil {
		return ServiceRequest{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return ServiceRequest{}, err
	}
	if err := validateCRMVersion("expected_version", claim.ExpectedVersion); err != nil {
		return ServiceRequest{}, err
	}
	actor, err = crmNormalizeActor(actor)
	if err != nil {
		return ServiceRequest{}, err
	}

	var claimed ServiceRequest
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "service_request.claim",
		EntityType: "service_request",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmServiceRequestIndex(state, id)
		if index < 0 {
			return &crmServiceRequestNotFoundError{resource: "service request", id: id}
		}
		current := state.ServiceRequests[index]
		if current.Version != claim.ExpectedVersion {
			return NewCRMConflictError("service request version conflict")
		}
		if current.QueueID == "" || current.EnqueuedAt == nil {
			return NewCRMConflictError("service request is not assigned to a queue")
		}
		queueIndex := crmServiceQueueIndex(state.ServiceQueues, current.QueueID)
		if queueIndex < 0 || !state.ServiceQueues[queueIndex].Enabled {
			return NewCRMConflictError("service queue is unavailable")
		}
		if current.ClaimedBy != "" || current.ClaimedAt != nil {
			return NewCRMConflictError("service request has already been claimed")
		}
		switch current.Status {
		case ServiceRequestStatusOpen, ServiceRequestStatusTriaged:
			current.Status = ServiceRequestStatusAssigned
		case ServiceRequestStatusAssigned:
		default:
			return NewCRMConflictError("service request cannot be claimed in its current status")
		}
		now := crmServiceRequestMutationTime(current.UpdatedAt)
		current.ClaimedBy = actor
		current.ClaimedAt = &now
		current.Owner = actor
		current.Version++
		current.UpdatedAt = now
		state.ServiceRequests[index] = current
		claimed = current
		return nil
	})
	return claimed, err
}

func crmServiceRequestTransition(
	store *CRMStore,
	id string,
	transition ServiceRequestTransition,
) (ServiceRequest, error) {
	if store == nil {
		return ServiceRequest{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return ServiceRequest{}, err
	}
	normalized, err := crmServiceRequestNormalizeTransition(transition)
	if err != nil {
		return ServiceRequest{}, err
	}

	var updated ServiceRequest
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      crmServiceRequestActor(normalized.Actor),
		Operation:  "service_request.transition",
		EntityType: "service_request",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmServiceRequestIndex(state, id)
		if index < 0 {
			return &crmServiceRequestNotFoundError{resource: "service request", id: id}
		}
		current := state.ServiceRequests[index]
		if current.Version != normalized.ExpectedVersion || current.Status != normalized.FromStatus {
			return NewCRMConflictError("service request version or status conflict")
		}
		if !crmServiceRequestTransitionAllowed(current.Status, normalized.ToStatus) {
			return NewCRMConflictError("service request status transition is not allowed")
		}

		now := crmServiceRequestMutationTime(current.UpdatedAt)
		if normalized.Resolution != "" {
			current.Resolution = normalized.Resolution
		}
		switch normalized.ToStatus {
		case ServiceRequestStatusResolved:
			if current.Resolution == "" {
				return NewCRMValidationError("resolution", "is required when resolving a service request")
			}
			resolvedAt := now
			current.ResolvedAt = &resolvedAt
		case ServiceRequestStatusClosed:
			if current.ResolvedAt == nil {
				return NewCRMConflictError("service request must be resolved before it can be closed")
			}
		default:
			if current.Status == ServiceRequestStatusResolved {
				current.ResolvedAt = nil
			}
		}
		current.Status = normalized.ToStatus
		current.Version++
		current.UpdatedAt = now
		state.ServiceRequests[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmServiceRequestNormalizeCreate(create ServiceRequestCreate) (ServiceRequestCreate, error) {
	var err error
	if create.CandidateID, err = normalizeCRMString("candidate_id", create.CandidateID, 80, true, false); err != nil {
		return ServiceRequestCreate{}, err
	}
	if create.JobID, err = normalizeCRMString("job_id", create.JobID, 80, false, false); err != nil {
		return ServiceRequestCreate{}, err
	}
	if create.OnboardingCaseID, err = normalizeCRMString("onboarding_case_id", create.OnboardingCaseID, 80, false, false); err != nil {
		return ServiceRequestCreate{}, err
	}
	if create.SourceEventID, err = normalizeCRMString("source_event_id", create.SourceEventID, 80, false, false); err != nil {
		return ServiceRequestCreate{}, err
	}
	if !IsValidServiceRequestCategory(create.Category) {
		return ServiceRequestCreate{}, NewCRMValidationError("category", "invalid service request category")
	}
	if create.Priority == "" {
		create.Priority = JobPriorityNormal
	}
	if !IsValidJobPriority(create.Priority) {
		return ServiceRequestCreate{}, NewCRMValidationError("priority", "invalid job priority")
	}
	if create.Description, err = normalizeCRMString("description", create.Description, 4000, true, true); err != nil {
		return ServiceRequestCreate{}, err
	}
	if create.Owner, err = normalizeCRMString("owner", create.Owner, 80, false, false); err != nil {
		return ServiceRequestCreate{}, err
	}
	if create.DueAt, err = normalizeCRMOptionalTime("due_at", create.DueAt); err != nil {
		return ServiceRequestCreate{}, err
	}
	if create.Actor, err = normalizeCRMString("actor", create.Actor, 80, false, false); err != nil {
		return ServiceRequestCreate{}, err
	}
	if crmServiceRequestCategoryRequiresHuman(create.Category) {
		create.RequiresHuman = true
	}
	return create, nil
}

func crmServiceRequestNormalizeUpdate(update ServiceRequestUpdate) (ServiceRequestUpdate, error) {
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return ServiceRequestUpdate{}, err
	}
	if update.CandidateID == nil && update.JobID == nil && update.OnboardingCaseID == nil &&
		update.Category == nil && update.Priority == nil && update.Description == nil &&
		update.Owner == nil && update.QueueID == nil && !update.ClearQueue &&
		update.RequiresHuman == nil && update.DueAt == nil &&
		!update.ClearDueAt && update.Resolution == nil {
		return ServiceRequestUpdate{}, NewCRMValidationError(
			"service_request",
			"at least one update field is required",
		)
	}
	if update.DueAt != nil && update.ClearDueAt {
		return ServiceRequestUpdate{}, NewCRMValidationError("due_at", "cannot be set and cleared together")
	}
	if update.QueueID != nil && update.ClearQueue {
		return ServiceRequestUpdate{}, NewCRMValidationError("queue_id", "cannot be set and cleared together")
	}

	var err error
	for _, item := range []struct {
		field     string
		value     **string
		limit     int
		required  bool
		multiline bool
	}{
		{field: "candidate_id", value: &update.CandidateID, limit: 80, required: true},
		{field: "job_id", value: &update.JobID, limit: 80},
		{field: "onboarding_case_id", value: &update.OnboardingCaseID, limit: 80},
		{field: "description", value: &update.Description, limit: 4000, required: true, multiline: true},
		{field: "owner", value: &update.Owner, limit: 80},
		{field: "queue_id", value: &update.QueueID, limit: 80, required: true},
		{field: "resolution", value: &update.Resolution, limit: 4000, multiline: true},
	} {
		if *item.value == nil {
			continue
		}
		value, normalizeErr := normalizeCRMString(
			item.field,
			**item.value,
			item.limit,
			item.required,
			item.multiline,
		)
		if normalizeErr != nil {
			return ServiceRequestUpdate{}, normalizeErr
		}
		*item.value = &value
	}
	if update.Category != nil && !IsValidServiceRequestCategory(*update.Category) {
		return ServiceRequestUpdate{}, NewCRMValidationError("category", "invalid service request category")
	}
	if update.Priority != nil && !IsValidJobPriority(*update.Priority) {
		return ServiceRequestUpdate{}, NewCRMValidationError("priority", "invalid job priority")
	}
	if update.DueAt, err = normalizeCRMOptionalTime("due_at", update.DueAt); err != nil {
		return ServiceRequestUpdate{}, err
	}
	if update.Actor, err = normalizeCRMString("actor", update.Actor, 80, false, false); err != nil {
		return ServiceRequestUpdate{}, err
	}
	return update, nil
}

func crmServiceQueueNormalizeCreate(create ServiceQueueCreate) (ServiceQueueCreate, error) {
	var err error
	if create.Name, err = normalizeCRMString("name", create.Name, 160, true, false); err != nil {
		return ServiceQueueCreate{}, err
	}
	if create.Owner, err = normalizeCRMString("owner", create.Owner, 80, false, false); err != nil {
		return ServiceQueueCreate{}, err
	}
	if create.Categories == nil {
		create.Categories = []ServiceRequestCategory{}
	}
	if create.Categories, err = crmServiceQueueNormalizeCategories(create.Categories); err != nil {
		return ServiceQueueCreate{}, err
	}
	if create.SLASeconds < 0 || create.SLASeconds > int64((365*24*time.Hour)/time.Second) {
		return ServiceQueueCreate{}, NewCRMValidationError("sla_seconds", "must be between 0 and 31536000")
	}
	return create, nil
}

func crmServiceQueueNormalizeUpdate(update ServiceQueueUpdate) (ServiceQueueUpdate, error) {
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return ServiceQueueUpdate{}, err
	}
	if update.Name == nil && update.Owner == nil && update.Categories == nil &&
		update.SLASeconds == nil && update.Enabled == nil {
		return ServiceQueueUpdate{}, NewCRMValidationError(
			"service_queue",
			"at least one update field is required",
		)
	}
	var err error
	if update.Name, err = crmServiceQueueNormalizeOptionalString("name", update.Name, 160, true); err != nil {
		return ServiceQueueUpdate{}, err
	}
	if update.Owner, err = crmServiceQueueNormalizeOptionalString("owner", update.Owner, 80, false); err != nil {
		return ServiceQueueUpdate{}, err
	}
	if update.Categories != nil {
		categories, normalizeErr := crmServiceQueueNormalizeCategories(*update.Categories)
		if normalizeErr != nil {
			return ServiceQueueUpdate{}, normalizeErr
		}
		update.Categories = &categories
	}
	if update.SLASeconds != nil &&
		(*update.SLASeconds < 0 || *update.SLASeconds > int64((365*24*time.Hour)/time.Second)) {
		return ServiceQueueUpdate{}, NewCRMValidationError("sla_seconds", "must be between 0 and 31536000")
	}
	return update, nil
}

func crmServiceQueueNormalizeOptionalString(
	field string,
	value *string,
	limit int,
	required bool,
) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeCRMString(field, *value, limit, required, false)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func crmServiceQueueNormalizeCategories(
	categories []ServiceRequestCategory,
) ([]ServiceRequestCategory, error) {
	if len(categories) > 32 {
		return nil, NewCRMValidationError("categories", "contains too many categories")
	}
	seen := make(map[ServiceRequestCategory]struct{}, len(categories))
	normalized := make([]ServiceRequestCategory, 0, len(categories))
	for _, category := range categories {
		if !IsValidServiceRequestCategory(category) {
			return nil, NewCRMValidationError("categories", "contains an invalid category")
		}
		if _, exists := seen[category]; exists {
			return nil, NewCRMValidationError("categories", "contains a duplicate category")
		}
		seen[category] = struct{}{}
		normalized = append(normalized, category)
	}
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	return normalized, nil
}

func crmServiceQueueForAssignment(
	state *CRMState,
	id string,
	category ServiceRequestCategory,
) (ServiceQueue, error) {
	index := crmServiceQueueIndex(state.ServiceQueues, id)
	if index < 0 {
		return ServiceQueue{}, &crmServiceRequestNotFoundError{resource: "service queue", id: id}
	}
	queue := state.ServiceQueues[index]
	if !queue.Enabled {
		return ServiceQueue{}, NewCRMConflictError("service queue is disabled")
	}
	if len(queue.Categories) != 0 {
		allowed := false
		for _, allowedCategory := range queue.Categories {
			if allowedCategory == category {
				allowed = true
				break
			}
		}
		if !allowed {
			return ServiceQueue{}, NewCRMConflictError("service queue does not accept this category")
		}
	}
	return queue, nil
}

func crmServiceQueueNameExists(queues []ServiceQueue, name, exceptID string) bool {
	for _, queue := range queues {
		if queue.ID != exceptID && strings.EqualFold(queue.Name, name) {
			return true
		}
	}
	return false
}

func crmServiceQueueIndex(queues []ServiceQueue, id string) int {
	for index := range queues {
		if queues[index].ID == id {
			return index
		}
	}
	return -1
}

func crmServiceRequestNormalizeTransition(
	transition ServiceRequestTransition,
) (ServiceRequestTransition, error) {
	if err := validateCRMVersion("expected_version", transition.ExpectedVersion); err != nil {
		return ServiceRequestTransition{}, err
	}
	if !IsValidServiceRequestStatus(transition.FromStatus) {
		return ServiceRequestTransition{}, NewCRMValidationError("from_status", "invalid service request status")
	}
	if !IsValidServiceRequestStatus(transition.ToStatus) {
		return ServiceRequestTransition{}, NewCRMValidationError("to_status", "invalid service request status")
	}
	var err error
	if transition.Resolution, err = normalizeCRMString(
		"resolution",
		transition.Resolution,
		4000,
		false,
		true,
	); err != nil {
		return ServiceRequestTransition{}, err
	}
	if transition.Actor, err = normalizeCRMString("actor", transition.Actor, 80, false, false); err != nil {
		return ServiceRequestTransition{}, err
	}
	return transition, nil
}

func crmServiceRequestNormalizeFilter(
	filter crmServiceRequestFilter,
) (crmServiceRequestFilter, error) {
	var err error
	if filter.Query, err = normalizeCRMString("q", filter.Query, 200, false, true); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.CandidateID, err = normalizeCRMString("candidate_id", filter.CandidateID, 80, false, false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.JobID, err = normalizeCRMString("job_id", filter.JobID, 80, false, false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.OnboardingCaseID, err = normalizeCRMString("onboarding_case_id", filter.OnboardingCaseID, 80, false, false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.Category != "" && !IsValidServiceRequestCategory(filter.Category) {
		return crmServiceRequestFilter{}, NewCRMValidationError("category", "invalid service request category")
	}
	if filter.Priority != "" && !IsValidJobPriority(filter.Priority) {
		return crmServiceRequestFilter{}, NewCRMValidationError("priority", "invalid job priority")
	}
	if filter.Status != "" && !IsValidServiceRequestStatus(filter.Status) {
		return crmServiceRequestFilter{}, NewCRMValidationError("status", "invalid service request status")
	}
	if filter.Owner != nil {
		owner, normalizeErr := normalizeCRMString("owner", *filter.Owner, 80, false, false)
		if normalizeErr != nil {
			return crmServiceRequestFilter{}, normalizeErr
		}
		filter.Owner = &owner
	}
	if filter.DueFrom != nil {
		if filter.DueFrom.IsZero() {
			return crmServiceRequestFilter{}, NewCRMValidationError("due_from", "must not be zero")
		}
		value := filter.DueFrom.UTC()
		filter.DueFrom = &value
	}
	if filter.DueTo != nil {
		if filter.DueTo.IsZero() {
			return crmServiceRequestFilter{}, NewCRMValidationError("due_to", "must not be zero")
		}
		value := filter.DueTo.UTC()
		filter.DueTo = &value
	}
	if filter.DueFrom != nil && filter.DueTo != nil && filter.DueFrom.After(*filter.DueTo) {
		return crmServiceRequestFilter{}, NewCRMValidationError(
			"due_range",
			"due_from must not be after due_to",
		)
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmServiceRequestFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmServiceRequestFilter{}, NewCRMValidationError("offset", "must not be negative")
	}
	return filter, nil
}

func crmServiceRequestValidateReferences(
	state *CRMState,
	candidateID string,
	jobID string,
	onboardingCaseID string,
	sourceEventID string,
) error {
	if crmServiceRequestCandidateIndex(state, candidateID) < 0 {
		return &crmServiceRequestNotFoundError{resource: "candidate", id: candidateID}
	}
	if jobID != "" && crmServiceRequestJobIndex(state, jobID) < 0 {
		return &crmServiceRequestNotFoundError{resource: "job", id: jobID}
	}
	if onboardingCaseID != "" {
		index := crmServiceRequestOnboardingIndex(state, onboardingCaseID)
		if index < 0 {
			return &crmServiceRequestNotFoundError{resource: "onboarding case", id: onboardingCaseID}
		}
		onboarding := state.OnboardingCases[index]
		if onboarding.CandidateID != candidateID {
			return NewCRMValidationError("candidate_id", "does not match the onboarding candidate")
		}
		if jobID != "" && onboarding.JobID != jobID {
			return NewCRMValidationError("job_id", "does not match the onboarding job")
		}
	}
	if sourceEventID != "" && crmServiceRequestEventIndex(state, sourceEventID) < 0 {
		return &crmServiceRequestNotFoundError{resource: "message event", id: sourceEventID}
	}
	return nil
}

func crmServiceRequestMatches(
	request ServiceRequest,
	filter crmServiceRequestFilter,
	now time.Time,
) bool {
	if filter.CandidateID != "" && request.CandidateID != filter.CandidateID {
		return false
	}
	if filter.JobID != "" && request.JobID != filter.JobID {
		return false
	}
	if filter.OnboardingCaseID != "" && request.OnboardingCaseID != filter.OnboardingCaseID {
		return false
	}
	if filter.Category != "" && request.Category != filter.Category {
		return false
	}
	if filter.Priority != "" && request.Priority != filter.Priority {
		return false
	}
	if filter.Status != "" && request.Status != filter.Status {
		return false
	}
	if filter.Owner != nil && request.Owner != *filter.Owner {
		return false
	}
	if filter.RequiresHuman != nil && request.RequiresHuman != *filter.RequiresHuman {
		return false
	}
	if filter.Overdue != nil {
		overdue := request.DueAt != nil &&
			request.DueAt.Before(now) &&
			request.Status != ServiceRequestStatusResolved &&
			request.Status != ServiceRequestStatusClosed &&
			request.Status != ServiceRequestStatusCancelled
		if overdue != *filter.Overdue {
			return false
		}
	}
	if filter.DueFrom != nil && (request.DueAt == nil || request.DueAt.Before(*filter.DueFrom)) {
		return false
	}
	if filter.DueTo != nil && (request.DueAt == nil || request.DueAt.After(*filter.DueTo)) {
		return false
	}
	if filter.Query != "" {
		query := strings.ToLower(filter.Query)
		haystack := strings.ToLower(strings.Join([]string{
			request.ID,
			request.CandidateID,
			request.JobID,
			request.OnboardingCaseID,
			request.Description,
			request.Owner,
			request.Resolution,
		}, "\n"))
		if !strings.Contains(haystack, query) {
			return false
		}
	}
	return true
}

func crmServiceRequestTransitionAllowed(from, to ServiceRequestStatus) bool {
	allowed := map[ServiceRequestStatus]map[ServiceRequestStatus]bool{
		ServiceRequestStatusOpen: {
			ServiceRequestStatusTriaged:    true,
			ServiceRequestStatusAssigned:   true,
			ServiceRequestStatusInProgress: true,
			ServiceRequestStatusEscalated:  true,
			ServiceRequestStatusCancelled:  true,
		},
		ServiceRequestStatusTriaged: {
			ServiceRequestStatusAssigned:   true,
			ServiceRequestStatusInProgress: true,
			ServiceRequestStatusEscalated:  true,
			ServiceRequestStatusCancelled:  true,
		},
		ServiceRequestStatusAssigned: {
			ServiceRequestStatusInProgress:        true,
			ServiceRequestStatusAwaitingRequester: true,
			ServiceRequestStatusResolved:          true,
			ServiceRequestStatusEscalated:         true,
			ServiceRequestStatusCancelled:         true,
		},
		ServiceRequestStatusInProgress: {
			ServiceRequestStatusAwaitingRequester: true,
			ServiceRequestStatusResolved:          true,
			ServiceRequestStatusEscalated:         true,
			ServiceRequestStatusCancelled:         true,
		},
		ServiceRequestStatusAwaitingRequester: {
			ServiceRequestStatusInProgress: true,
			ServiceRequestStatusResolved:   true,
			ServiceRequestStatusEscalated:  true,
			ServiceRequestStatusCancelled:  true,
		},
		ServiceRequestStatusEscalated: {
			ServiceRequestStatusTriaged:           true,
			ServiceRequestStatusAssigned:          true,
			ServiceRequestStatusInProgress:        true,
			ServiceRequestStatusAwaitingRequester: true,
			ServiceRequestStatusResolved:          true,
			ServiceRequestStatusCancelled:         true,
		},
		ServiceRequestStatusResolved: {
			ServiceRequestStatusClosed:     true,
			ServiceRequestStatusInProgress: true,
			ServiceRequestStatusEscalated:  true,
		},
	}
	return allowed[from][to]
}

func crmServiceRequestCategoryRequiresHuman(category ServiceRequestCategory) bool {
	return category == ServiceRequestCategorySafety ||
		category == ServiceRequestCategoryDispute ||
		category == ServiceRequestCategoryFund
}

func crmServiceRequestMutationTime(notBefore time.Time) time.Time {
	now := time.Now().UTC()
	if now.Before(notBefore) {
		return notBefore
	}
	return now
}

func crmServiceRequestActor(actor string) string {
	if actor == "" {
		return "system"
	}
	return actor
}

func crmServiceRequestPriorityRank(priority JobPriority) int {
	switch priority {
	case JobPriorityUrgent:
		return 4
	case JobPriorityHigh:
		return 3
	case JobPriorityNormal:
		return 2
	case JobPriorityLow:
		return 1
	default:
		return 0
	}
}

func crmServiceRequestIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.ServiceRequests {
		if state.ServiceRequests[index].ID == id {
			return index
		}
	}
	return -1
}

func crmServiceRequestCandidateIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.Candidates {
		if state.Candidates[index].ID == id {
			return index
		}
	}
	return -1
}

func crmServiceRequestJobIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.Jobs {
		if state.Jobs[index].ID == id {
			return index
		}
	}
	return -1
}

func crmServiceRequestOnboardingIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.OnboardingCases {
		if state.OnboardingCases[index].ID == id {
			return index
		}
	}
	return -1
}

func crmServiceRequestEventIndex(state *CRMState, id string) int {
	if state == nil {
		return -1
	}
	for index := range state.MessageEvents {
		if state.MessageEvents[index].ID == id {
			return index
		}
	}
	return -1
}

func crmServiceRequestPage[T any](values []T, offset, limit int) []T {
	if offset >= len(values) {
		return []T{}
	}
	end := offset + limit
	if end > len(values) {
		end = len(values)
	}
	result := make([]T, end-offset)
	copy(result, values[offset:end])
	return result
}

func crmServiceRequestIsNotFound(err error) bool {
	var target *crmServiceRequestNotFoundError
	return errors.As(err, &target)
}
