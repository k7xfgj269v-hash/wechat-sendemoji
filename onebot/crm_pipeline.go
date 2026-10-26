package main

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
)

type crmPipelineNotFoundError struct {
	resource string
}

func (e *crmPipelineNotFoundError) Error() string {
	return e.resource + " not found"
}

type crmPipelineJobFilter struct {
	Query    string
	Status   JobStatus
	Priority JobPriority
	SiteName string
	Owner    string
	Limit    int
	Offset   int
}

type crmPipelineLeadFilter struct {
	CandidateID string
	JobID       string
	Stage       LeadStage
	Owner       string
	Limit       int
	Offset      int
}

type crmPipelineJobStatusRequest struct {
	ExpectedVersion uint64    `json:"expected_version"`
	Status          JobStatus `json:"status"`
}

type crmPipelineDemandRevisionActivation struct {
	ExpectedVersion uint64 `json:"expected_version"`
}

type crmPipelineDemandRevisionChange struct {
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type crmPipelineDemandRevisionComparison struct {
	JobID         string                            `json:"job_id"`
	From          DemandRevision                    `json:"from"`
	To            DemandRevision                    `json:"to"`
	ChangedFields []string                          `json:"changed_fields"`
	Changes       []crmPipelineDemandRevisionChange `json:"changes"`
}

func crmPipelineCreateJob(store *CRMStore, draft JobDraft) (Job, error) {
	normalized, err := NormalizeJobDraft(draft)
	if err != nil {
		return Job{}, err
	}

	var created Job
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "system",
		Operation:  "job.create",
		EntityType: "job",
	}, func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindJob)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created = Job{
			ID:                    id,
			Title:                 normalized.Title,
			Company:               normalized.Company,
			Location:              normalized.Location,
			EmploymentType:        normalized.EmploymentType,
			Description:           normalized.Description,
			Requirements:          append([]string{}, normalized.Requirements...),
			DemandCode:            normalized.DemandCode,
			SiteName:              normalized.SiteName,
			OnsiteContact:         normalized.OnsiteContact,
			Headcount:             normalized.Headcount,
			Priority:              normalized.Priority,
			StartAt:               cloneCRMTimePointer(normalized.StartAt),
			DeadlineAt:            cloneCRMTimePointer(normalized.DeadlineAt),
			Shift:                 normalized.Shift,
			WorkSchedule:          normalized.WorkSchedule,
			CompensationSummary:   normalized.CompensationSummary,
			Accommodation:         normalized.Accommodation,
			TransportRequirements: normalized.TransportRequirements,
			RequiredDocuments:     append([]string{}, normalized.RequiredDocuments...),
			Owner:                 normalized.Owner,
			Status:                normalized.Status,
			Version:               1,
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		state.Jobs = append(state.Jobs, created)
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return crmPipelineStoredJob(store, created.ID)
}

func crmPipelineListJobs(store *CRMStore, filter crmPipelineJobFilter) (int, []JobListItem, error) {
	normalized, err := crmPipelineNormalizeJobFilter(filter)
	if err != nil {
		return 0, nil, err
	}
	state := store.Snapshot()
	leadCounts := make(map[string]int, len(state.Jobs))
	for _, lead := range state.Leads {
		leadCounts[lead.JobID]++
	}
	provenanceCounts := make(map[string]int, len(state.Jobs))
	latestProvenance := make(map[string]JobProvenance, len(state.Jobs))
	for _, provenance := range state.JobProvenance {
		if provenance.JobID == "" {
			continue
		}
		provenanceCounts[provenance.JobID]++
		latest, ok := latestProvenance[provenance.JobID]
		if !ok || crmPipelineProvenanceNewer(provenance, latest) {
			latestProvenance[provenance.JobID] = provenance
		}
	}
	items := make([]JobListItem, 0, len(state.Jobs))
	for _, job := range state.Jobs {
		if !crmPipelineJobMatches(job, normalized) {
			continue
		}
		items = append(items, JobListItem{
			Job:                     job,
			LeadCount:               leadCounts[job.ID],
			LatestIngestionDecision: latestProvenance[job.ID].Decision,
			ProvenanceCount:         provenanceCounts[job.ID],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Job.UpdatedAt.Equal(items[j].Job.UpdatedAt) {
			return items[i].Job.ID < items[j].Job.ID
		}
		return items[i].Job.UpdatedAt.After(items[j].Job.UpdatedAt)
	})
	total := len(items)
	return total, crmPipelinePage(items, normalized.Offset, normalized.Limit), nil
}

func crmPipelineGetJob(store *CRMStore, id string) (JobDetail, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return JobDetail{}, err
	}
	state := store.Snapshot()
	index := crmPipelineJobIndex(state.Jobs, id)
	if index < 0 {
		return JobDetail{}, &crmPipelineNotFoundError{resource: "job"}
	}
	detail := JobDetail{
		Job:        state.Jobs[index],
		LeadIDs:    []string{},
		Provenance: []JobProvenance{},
		Revisions:  []DemandRevision{},
	}
	for _, lead := range state.Leads {
		if lead.JobID == id {
			detail.LeadIDs = append(detail.LeadIDs, lead.ID)
		}
	}
	for _, provenance := range state.JobProvenance {
		if provenance.JobID == id {
			detail.Provenance = append(detail.Provenance, provenance)
		}
	}
	for _, revision := range state.DemandRevisions {
		if revision.JobID == id {
			detail.Revisions = append(detail.Revisions, revision)
		}
	}
	sort.Strings(detail.LeadIDs)
	sort.Slice(detail.Provenance, func(i, j int) bool {
		return crmPipelineProvenanceNewer(detail.Provenance[i], detail.Provenance[j])
	})
	sort.Slice(detail.Revisions, func(i, j int) bool {
		if detail.Revisions[i].Number == detail.Revisions[j].Number {
			return detail.Revisions[i].ID < detail.Revisions[j].ID
		}
		return detail.Revisions[i].Number > detail.Revisions[j].Number
	})
	detail.LeadCount = len(detail.LeadIDs)
	return detail, nil
}

func crmPipelineProvenanceNewer(left, right JobProvenance) bool {
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}
	if !left.SourceTimestamp.Equal(right.SourceTimestamp) {
		return left.SourceTimestamp.After(right.SourceTimestamp)
	}
	return left.ID > right.ID
}

func crmPipelineUpdateJob(store *CRMStore, id string, update JobUpdate) (Job, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Job{}, err
	}
	normalized, err := crmPipelineNormalizeJobUpdate(update)
	if err != nil {
		return Job{}, err
	}

	var updated Job
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "system",
		Operation:  "job.update",
		EntityType: "job",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineJobIndex(state.Jobs, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "job"}
		}
		current := state.Jobs[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("job version conflict")
		}
		previous := current
		if normalized.Title != nil {
			current.Title = *normalized.Title
		}
		if normalized.Company != nil {
			current.Company = *normalized.Company
		}
		if normalized.Location != nil {
			current.Location = *normalized.Location
		}
		if normalized.EmploymentType != nil {
			current.EmploymentType = *normalized.EmploymentType
		}
		if normalized.Description != nil {
			current.Description = *normalized.Description
		}
		if normalized.Requirements != nil {
			current.Requirements = append([]string{}, (*normalized.Requirements)...)
		}
		if normalized.DemandCode != nil {
			current.DemandCode = *normalized.DemandCode
		}
		if normalized.SiteName != nil {
			current.SiteName = *normalized.SiteName
		}
		if normalized.OnsiteContact != nil {
			current.OnsiteContact = *normalized.OnsiteContact
		}
		if normalized.Headcount != nil {
			current.Headcount = *normalized.Headcount
		}
		if normalized.Priority != nil {
			current.Priority = *normalized.Priority
		}
		if normalized.StartAt != nil {
			current.StartAt = cloneCRMTimePointer(normalized.StartAt)
		} else if normalized.ClearStartAt {
			current.StartAt = nil
		}
		if normalized.DeadlineAt != nil {
			current.DeadlineAt = cloneCRMTimePointer(normalized.DeadlineAt)
		} else if normalized.ClearDeadlineAt {
			current.DeadlineAt = nil
		}
		if normalized.Shift != nil {
			current.Shift = *normalized.Shift
		}
		if normalized.WorkSchedule != nil {
			current.WorkSchedule = *normalized.WorkSchedule
		}
		if normalized.CompensationSummary != nil {
			current.CompensationSummary = *normalized.CompensationSummary
		}
		if normalized.Accommodation != nil {
			current.Accommodation = *normalized.Accommodation
		}
		if normalized.TransportRequirements != nil {
			current.TransportRequirements = *normalized.TransportRequirements
		}
		if normalized.RequiredDocuments != nil {
			current.RequiredDocuments = append([]string{}, (*normalized.RequiredDocuments)...)
		}
		if normalized.Owner != nil {
			current.Owner = *normalized.Owner
		}
		if normalized.Status != nil && current.Status != *normalized.Status {
			if err := ValidateJobStatusTransition(current.Status, *normalized.Status); err != nil {
				return err
			}
			current.Status = *normalized.Status
		}
		if reflect.DeepEqual(current, previous) {
			updated = current
			return nil
		}
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.Jobs[index] = current
		updated = current
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return crmPipelineStoredJob(store, updated.ID)
}

func crmPipelineListDemandRevisions(store *CRMStore, jobID string) ([]DemandRevision, error) {
	jobID, err := normalizeCRMString("id", jobID, 80, true, false)
	if err != nil {
		return nil, err
	}
	state := store.Snapshot()
	if crmPipelineJobIndex(state.Jobs, jobID) < 0 {
		return nil, &crmPipelineNotFoundError{resource: "job"}
	}
	revisions := make([]DemandRevision, 0)
	for _, revision := range state.DemandRevisions {
		if revision.JobID == jobID {
			revisions = append(revisions, revision)
		}
	}
	sort.Slice(revisions, func(i, j int) bool {
		if revisions[i].Number == revisions[j].Number {
			return revisions[i].ID < revisions[j].ID
		}
		return revisions[i].Number > revisions[j].Number
	})
	return revisions, nil
}

func crmPipelineGetDemandRevision(store *CRMStore, jobID, revisionID string) (DemandRevision, error) {
	jobID, err := normalizeCRMString("id", jobID, 80, true, false)
	if err != nil {
		return DemandRevision{}, err
	}
	revisionID, err = normalizeCRMString("revision_id", revisionID, 80, true, false)
	if err != nil {
		return DemandRevision{}, err
	}
	state := store.Snapshot()
	if crmPipelineJobIndex(state.Jobs, jobID) < 0 {
		return DemandRevision{}, &crmPipelineNotFoundError{resource: "job"}
	}
	index := crmPipelineDemandRevisionIndex(state.DemandRevisions, revisionID)
	if index < 0 || state.DemandRevisions[index].JobID != jobID {
		return DemandRevision{}, &crmPipelineNotFoundError{resource: "demand revision"}
	}
	return state.DemandRevisions[index], nil
}

func crmPipelineCompareDemandRevisions(
	store *CRMStore,
	jobID, fromRevisionID, toRevisionID string,
) (crmPipelineDemandRevisionComparison, error) {
	from, err := crmPipelineGetDemandRevision(store, jobID, fromRevisionID)
	if err != nil {
		return crmPipelineDemandRevisionComparison{}, err
	}
	to, err := crmPipelineGetDemandRevision(store, jobID, toRevisionID)
	if err != nil {
		return crmPipelineDemandRevisionComparison{}, err
	}
	comparison := crmPipelineDemandRevisionComparison{
		JobID:         from.JobID,
		From:          from,
		To:            to,
		ChangedFields: []string{},
		Changes:       []crmPipelineDemandRevisionChange{},
	}
	fields := []struct {
		name   string
		before any
		after  any
	}{
		{"title", from.Snapshot.Title, to.Snapshot.Title},
		{"company", from.Snapshot.Company, to.Snapshot.Company},
		{"location", from.Snapshot.Location, to.Snapshot.Location},
		{"employment_type", from.Snapshot.EmploymentType, to.Snapshot.EmploymentType},
		{"description", from.Snapshot.Description, to.Snapshot.Description},
		{"requirements", from.Snapshot.Requirements, to.Snapshot.Requirements},
		{"demand_code", from.Snapshot.DemandCode, to.Snapshot.DemandCode},
		{"site_name", from.Snapshot.SiteName, to.Snapshot.SiteName},
		{"onsite_contact", from.Snapshot.OnsiteContact, to.Snapshot.OnsiteContact},
		{"headcount", from.Snapshot.Headcount, to.Snapshot.Headcount},
		{"priority", from.Snapshot.Priority, to.Snapshot.Priority},
		{"start_at", from.Snapshot.StartAt, to.Snapshot.StartAt},
		{"deadline_at", from.Snapshot.DeadlineAt, to.Snapshot.DeadlineAt},
		{"shift", from.Snapshot.Shift, to.Snapshot.Shift},
		{"work_schedule", from.Snapshot.WorkSchedule, to.Snapshot.WorkSchedule},
		{"compensation_summary", from.Snapshot.CompensationSummary, to.Snapshot.CompensationSummary},
		{"accommodation", from.Snapshot.Accommodation, to.Snapshot.Accommodation},
		{"transport_requirements", from.Snapshot.TransportRequirements, to.Snapshot.TransportRequirements},
		{"required_documents", from.Snapshot.RequiredDocuments, to.Snapshot.RequiredDocuments},
	}
	for _, field := range fields {
		if reflect.DeepEqual(field.before, field.after) {
			continue
		}
		comparison.ChangedFields = append(comparison.ChangedFields, field.name)
		comparison.Changes = append(comparison.Changes, crmPipelineDemandRevisionChange{
			Field: field.name, Before: field.before, After: field.after,
		})
	}
	return comparison, nil
}

func crmPipelineActivateDemandRevision(
	store *CRMStore,
	jobID, revisionID string,
	request crmPipelineDemandRevisionActivation,
	operatorID string,
) (Job, error) {
	jobID, err := normalizeCRMString("id", jobID, 80, true, false)
	if err != nil {
		return Job{}, err
	}
	revisionID, err = normalizeCRMString("revision_id", revisionID, 80, true, false)
	if err != nil {
		return Job{}, err
	}
	if err := validateCRMVersion("expected_version", request.ExpectedVersion); err != nil {
		return Job{}, err
	}
	operatorID, err = normalizeCRMString("operator_id", operatorID, 80, true, false)
	if err != nil {
		return Job{}, err
	}

	var updated Job
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      operatorID,
		Operation:  "job.demand_revision.activate",
		EntityType: "job",
		EntityID:   jobID,
	}, func(state *CRMState, _ *IDAllocator) error {
		jobIndex := crmPipelineJobIndex(state.Jobs, jobID)
		if jobIndex < 0 {
			return &crmPipelineNotFoundError{resource: "job"}
		}
		current := state.Jobs[jobIndex]
		if current.Version != request.ExpectedVersion {
			return NewCRMConflictError("job version conflict")
		}
		revisionIndex := crmPipelineDemandRevisionIndex(state.DemandRevisions, revisionID)
		if revisionIndex < 0 || state.DemandRevisions[revisionIndex].JobID != jobID {
			return &crmPipelineNotFoundError{resource: "demand revision"}
		}
		revision := state.DemandRevisions[revisionIndex]
		if current.ActiveDemandRevisionID == revision.ID {
			updated = current
			return nil
		}
		crmPipelineApplyDemandSnapshot(&current, revision.Snapshot)
		current.ActiveDemandRevisionID = revision.ID
		current.DemandRevisionNumber = revision.Number
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.Jobs[jobIndex] = current
		updated = current
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return crmPipelineStoredJob(store, updated.ID)
}

func crmPipelineSetJobStatus(store *CRMStore, id string, request crmPipelineJobStatusRequest) (Job, error) {
	if err := validateCRMVersion("expected_version", request.ExpectedVersion); err != nil {
		return Job{}, err
	}
	if !IsValidJobStatus(request.Status) {
		return Job{}, NewCRMValidationError("status", "invalid job status")
	}
	status := request.Status
	return crmPipelineUpdateJob(store, id, JobUpdate{
		ExpectedVersion: request.ExpectedVersion,
		Status:          &status,
	})
}

func crmPipelineCreateLead(store *CRMStore, create LeadCreate) (Lead, error) {
	normalized, err := NormalizeLeadCreate(create)
	if err != nil {
		return Lead{}, err
	}

	var created Lead
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "system",
		Operation:  "lead.create",
		EntityType: "lead",
	}, func(state *CRMState, allocator *IDAllocator) error {
		candidateIndex := crmPipelineCandidateIndex(state.Candidates, normalized.CandidateID)
		if candidateIndex < 0 {
			return &crmPipelineNotFoundError{resource: "candidate"}
		}
		if state.Candidates[candidateIndex].Status == CandidateStatusArchived {
			return NewCRMConflictError("archived candidate cannot enter a pipeline")
		}
		jobIndex := crmPipelineJobIndex(state.Jobs, normalized.JobID)
		if jobIndex < 0 {
			return &crmPipelineNotFoundError{resource: "job"}
		}
		if state.Jobs[jobIndex].Status != JobStatusOpen {
			return NewCRMConflictError("job must be open")
		}
		for _, lead := range state.Leads {
			if lead.CandidateID == normalized.CandidateID &&
				lead.JobID == normalized.JobID &&
				!IsTerminalLeadStage(lead.Stage) {
				return NewCRMConflictError("active candidate/job lead already exists")
			}
		}
		id, err := allocator.Next(IDKindLead)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created = Lead{
			ID:             id,
			CandidateID:    normalized.CandidateID,
			JobID:          normalized.JobID,
			Stage:          LeadStageSourced,
			Owner:          normalized.Owner,
			Version:        1,
			CreatedAt:      now,
			UpdatedAt:      now,
			StageChangedAt: now,
		}
		state.Leads = append(state.Leads, created)
		return nil
	})
	return created, err
}

func crmPipelineListLeads(store *CRMStore, filter crmPipelineLeadFilter) (int, []LeadListItem, error) {
	normalized, err := crmPipelineNormalizeLeadFilter(filter)
	if err != nil {
		return 0, nil, err
	}
	state := store.Snapshot()
	items := make([]LeadListItem, 0, len(state.Leads))
	for _, lead := range state.Leads {
		if normalized.CandidateID != "" && lead.CandidateID != normalized.CandidateID {
			continue
		}
		if normalized.JobID != "" && lead.JobID != normalized.JobID {
			continue
		}
		if normalized.Stage != "" && lead.Stage != normalized.Stage {
			continue
		}
		if normalized.Owner != "" && lead.Owner != normalized.Owner {
			continue
		}
		items = append(items, crmPipelineProjectLead(state, lead))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Lead.UpdatedAt.Equal(items[j].Lead.UpdatedAt) {
			return items[i].Lead.ID < items[j].Lead.ID
		}
		return items[i].Lead.UpdatedAt.After(items[j].Lead.UpdatedAt)
	})
	total := len(items)
	return total, crmPipelinePage(items, normalized.Offset, normalized.Limit), nil
}

func crmPipelineGetLead(store *CRMStore, id string) (LeadListItem, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return LeadListItem{}, err
	}
	state := store.Snapshot()
	index := crmPipelineLeadIndex(state.Leads, id)
	if index < 0 {
		return LeadListItem{}, &crmPipelineNotFoundError{resource: "lead"}
	}
	return crmPipelineProjectLead(state, state.Leads[index]), nil
}

func crmPipelineUpdateLead(store *CRMStore, id string, update LeadUpdate) (Lead, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Lead{}, err
	}
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return Lead{}, err
	}
	if update.Owner == nil {
		return Lead{}, NewCRMValidationError("lead", "owner update is required")
	}
	owner, err := normalizeCRMString("owner", *update.Owner, 80, false, false)
	if err != nil {
		return Lead{}, err
	}

	var updated Lead
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      "system",
		Operation:  "lead.update",
		EntityType: "lead",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineLeadIndex(state.Leads, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "lead"}
		}
		current := state.Leads[index]
		if current.Version != update.ExpectedVersion {
			return NewCRMConflictError("lead version conflict")
		}
		current.Owner = owner
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.Leads[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmPipelineTransitionLead(store *CRMStore, id string, transition LeadTransition) (Lead, error) {
	operatorID := transition.Actor
	confirmed := !IsTerminalLeadStage(transition.ToStage) || strings.TrimSpace(operatorID) != ""
	return crmPipelineTransitionLeadConfirmed(store, id, transition, confirmed, operatorID)
}

func crmPipelineTransitionLeadConfirmed(
	store *CRMStore,
	id string,
	transition LeadTransition,
	confirmed bool,
	operatorID string,
) (Lead, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Lead{}, err
	}
	if err := validateCRMVersion("expected_version", transition.ExpectedVersion); err != nil {
		return Lead{}, err
	}
	if !IsValidLeadStage(transition.FromStage) {
		return Lead{}, NewCRMValidationError("from_stage", "invalid lead stage")
	}
	if !IsValidLeadStage(transition.ToStage) {
		return Lead{}, NewCRMValidationError("to_stage", "invalid lead stage")
	}
	transition.Reason, err = normalizeCRMString("reason", transition.Reason, 2000, false, true)
	if err != nil {
		return Lead{}, err
	}
	operatorID, err = normalizeCRMString("operator_id", operatorID, 80, false, false)
	if err != nil {
		return Lead{}, err
	}

	var updated Lead
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      crmPipelineTransitionActor(operatorID),
		Operation:  "lead.transition",
		EntityType: "lead",
		EntityID:   id,
	}, func(state *CRMState, allocator *IDAllocator) error {
		index := crmPipelineLeadIndex(state.Leads, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "lead"}
		}
		current := state.Leads[index]
		if current.Version != transition.ExpectedVersion || current.Stage != transition.FromStage {
			return NewCRMConflictError("lead version or stage conflict")
		}
		if err := ValidateLeadStageTransition(current.Stage, transition.ToStage); err != nil {
			return err
		}
		if IsTerminalLeadStage(transition.ToStage) {
			if !confirmed {
				return NewCRMValidationError("confirmed", "explicit confirmation is required")
			}
			if operatorID == "" {
				return NewCRMValidationError("operator_id", "authenticated operator is required")
			}
		}
		stageEventID, err := allocator.Next(IDKindStageEvent)
		if err != nil {
			return err
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		state.LeadStageEvents = append(state.LeadStageEvents, LeadStageEvent{
			ID:        stageEventID,
			LeadID:    current.ID,
			FromStage: current.Stage,
			ToStage:   transition.ToStage,
			Reason:    transition.Reason,
			Actor:     crmPipelineTransitionActor(operatorID),
			CreatedAt: now,
		})
		current.Stage = transition.ToStage
		current.StageChangedAt = now
		current.UpdatedAt = now
		current.Version++
		state.Leads[index] = current
		if current.Stage == LeadStageHired {
			if _, _, err := crmEnsureOnboardingForLeadInState(
				state,
				allocator,
				current.ID,
				operatorID,
			); err != nil {
				return err
			}
		}
		updated = current
		return nil
	})
	return updated, err
}

func crmPipelineAllowedTransitions(stage LeadStage) []LeadStage {
	allowed := make([]LeadStage, 0, 3)
	for _, candidate := range LeadStages() {
		if CanTransitionLeadStage(stage, candidate) {
			allowed = append(allowed, candidate)
		}
	}
	return allowed
}

func crmPipelineTransitionActor(operatorID string) string {
	if operatorID == "" {
		return "system"
	}
	return operatorID
}

func crmPipelineBoard(store *CRMStore, jobID, owner string) (PipelineResponse, error) {
	var err error
	if jobID, err = normalizeCRMString("job_id", jobID, 80, false, false); err != nil {
		return PipelineResponse{}, err
	}
	if owner, err = normalizeCRMString("owner", owner, 80, false, false); err != nil {
		return PipelineResponse{}, err
	}
	state := store.Snapshot()
	if jobID != "" && crmPipelineJobIndex(state.Jobs, jobID) < 0 {
		return PipelineResponse{}, &crmPipelineNotFoundError{resource: "job"}
	}
	response := PipelineResponse{Stages: make([]PipelineStage, 0, len(LeadStages()))}
	stageIndexes := make(map[LeadStage]int)
	for _, stage := range LeadStages() {
		stageIndexes[stage] = len(response.Stages)
		response.Stages = append(response.Stages, PipelineStage{Stage: stage, Leads: []LeadListItem{}})
	}
	for _, lead := range state.Leads {
		if jobID != "" && lead.JobID != jobID {
			continue
		}
		if owner != "" && lead.Owner != owner {
			continue
		}
		index := stageIndexes[lead.Stage]
		response.Stages[index].Leads = append(response.Stages[index].Leads, crmPipelineProjectLead(state, lead))
		response.Total++
	}
	for i := range response.Stages {
		sort.Slice(response.Stages[i].Leads, func(a, b int) bool {
			left := response.Stages[i].Leads[a].Lead
			right := response.Stages[i].Leads[b].Lead
			if left.UpdatedAt.Equal(right.UpdatedAt) {
				return left.ID < right.ID
			}
			return left.UpdatedAt.After(right.UpdatedAt)
		})
	}
	return response, nil
}

func crmPipelineNormalizeJobFilter(filter crmPipelineJobFilter) (crmPipelineJobFilter, error) {
	var err error
	if filter.Query, err = NormalizeCRMSearch(filter.Query); err != nil {
		return crmPipelineJobFilter{}, err
	}
	if filter.Owner, err = normalizeCRMString("owner", filter.Owner, 80, false, false); err != nil {
		return crmPipelineJobFilter{}, err
	}
	if filter.SiteName, err = normalizeCRMString("site_name", filter.SiteName, 160, false, false); err != nil {
		return crmPipelineJobFilter{}, err
	}
	if filter.Status != "" && !IsValidJobStatus(filter.Status) {
		return crmPipelineJobFilter{}, NewCRMValidationError("status", "invalid job status")
	}
	if filter.Priority != "" && !IsValidJobPriority(filter.Priority) {
		return crmPipelineJobFilter{}, NewCRMValidationError("priority", "invalid job priority")
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmPipelineJobFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmPipelineJobFilter{}, NewCRMValidationError("offset", "must be zero or greater")
	}
	return filter, nil
}

func crmPipelineNormalizeLeadFilter(filter crmPipelineLeadFilter) (crmPipelineLeadFilter, error) {
	var err error
	if filter.CandidateID, err = normalizeCRMString("candidate_id", filter.CandidateID, 80, false, false); err != nil {
		return crmPipelineLeadFilter{}, err
	}
	if filter.JobID, err = normalizeCRMString("job_id", filter.JobID, 80, false, false); err != nil {
		return crmPipelineLeadFilter{}, err
	}
	if filter.Owner, err = normalizeCRMString("owner", filter.Owner, 80, false, false); err != nil {
		return crmPipelineLeadFilter{}, err
	}
	if filter.Stage != "" && !IsValidLeadStage(filter.Stage) {
		return crmPipelineLeadFilter{}, NewCRMValidationError("stage", "invalid lead stage")
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmPipelineLeadFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmPipelineLeadFilter{}, NewCRMValidationError("offset", "must be zero or greater")
	}
	return filter, nil
}

func crmPipelineNormalizeJobUpdate(update JobUpdate) (JobUpdate, error) {
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return JobUpdate{}, err
	}
	if update.Title == nil && update.Company == nil && update.Location == nil &&
		update.EmploymentType == nil && update.Description == nil &&
		update.Requirements == nil && update.DemandCode == nil &&
		update.SiteName == nil && update.OnsiteContact == nil &&
		update.Headcount == nil && update.Priority == nil &&
		update.StartAt == nil && update.DeadlineAt == nil &&
		!update.ClearStartAt && !update.ClearDeadlineAt &&
		update.Shift == nil && update.WorkSchedule == nil &&
		update.CompensationSummary == nil && update.Accommodation == nil &&
		update.TransportRequirements == nil && update.RequiredDocuments == nil &&
		update.Owner == nil && update.Status == nil {
		return JobUpdate{}, NewCRMValidationError("job", "at least one update field is required")
	}
	var err error
	if update.Title != nil {
		value, normalizeErr := normalizeCRMString("title", *update.Title, 160, true, false)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.Title = &value
	}
	if update.Company != nil {
		value, normalizeErr := normalizeCRMString("company", *update.Company, 160, true, false)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.Company = &value
	}
	if update.Location != nil {
		value, normalizeErr := normalizeCRMString("location", *update.Location, 160, false, false)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.Location = &value
	}
	if update.EmploymentType != nil {
		value, normalizeErr := normalizeCRMString("employment_type", *update.EmploymentType, 80, false, false)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.EmploymentType = &value
	}
	if update.Description != nil {
		value, normalizeErr := normalizeCRMString("description", *update.Description, 8000, false, true)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.Description = &value
	}
	if update.Requirements != nil {
		values, normalizeErr := normalizeCRMStringList("requirements", *update.Requirements, 64, 1000)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.Requirements = &values
	}
	for _, item := range []struct {
		field string
		value **string
		limit int
		multi bool
	}{
		{"demand_code", &update.DemandCode, 120, false},
		{"site_name", &update.SiteName, 160, false},
		{"onsite_contact", &update.OnsiteContact, 160, false},
		{"shift", &update.Shift, 160, false},
		{"work_schedule", &update.WorkSchedule, 2000, true},
		{"compensation_summary", &update.CompensationSummary, 2000, true},
		{"accommodation", &update.Accommodation, 2000, true},
		{"transport_requirements", &update.TransportRequirements, 2000, true},
	} {
		if *item.value == nil {
			continue
		}
		value, normalizeErr := normalizeCRMString(item.field, **item.value, item.limit, false, item.multi)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		*item.value = &value
	}
	if update.Headcount != nil && (*update.Headcount < 0 || *update.Headcount > 1000000) {
		return JobUpdate{}, NewCRMValidationError("headcount", "must be between 0 and 1000000")
	}
	if update.Priority != nil && !IsValidJobPriority(*update.Priority) {
		return JobUpdate{}, NewCRMValidationError("priority", "invalid job priority")
	}
	if update.StartAt != nil {
		value, normalizeErr := normalizeCRMOptionalTime("start_at", update.StartAt)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.StartAt = value
	}
	if update.StartAt != nil && update.ClearStartAt {
		return JobUpdate{}, NewCRMValidationError("clear_start_at", "cannot be true when start_at is set")
	}
	if update.DeadlineAt != nil {
		value, normalizeErr := normalizeCRMOptionalTime("deadline_at", update.DeadlineAt)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.DeadlineAt = value
	}
	if update.DeadlineAt != nil && update.ClearDeadlineAt {
		return JobUpdate{}, NewCRMValidationError("clear_deadline_at", "cannot be true when deadline_at is set")
	}
	if update.StartAt != nil && update.DeadlineAt != nil && update.DeadlineAt.Before(*update.StartAt) {
		return JobUpdate{}, NewCRMValidationError("deadline_at", "must not precede start_at")
	}
	if update.RequiredDocuments != nil {
		values, normalizeErr := normalizeCRMStringList("required_documents", *update.RequiredDocuments, 64, 200)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.RequiredDocuments = &values
	}
	if update.Owner != nil {
		value, normalizeErr := normalizeCRMString("owner", *update.Owner, 80, false, false)
		if normalizeErr != nil {
			return JobUpdate{}, normalizeErr
		}
		update.Owner = &value
	}
	if update.Status != nil && !IsValidJobStatus(*update.Status) {
		return JobUpdate{}, NewCRMValidationError("status", "invalid job status")
	}
	return update, err
}

func crmPipelineJobMatches(job Job, filter crmPipelineJobFilter) bool {
	if filter.Status != "" && job.Status != filter.Status {
		return false
	}
	if filter.Owner != "" && job.Owner != filter.Owner {
		return false
	}
	if filter.Priority != "" && job.Priority != filter.Priority {
		return false
	}
	if filter.SiteName != "" && !strings.Contains(strings.ToLower(job.SiteName), strings.ToLower(filter.SiteName)) {
		return false
	}
	if filter.Query == "" {
		return true
	}
	query := strings.ToLower(filter.Query)
	values := []string{
		job.ID, job.Title, job.Company, job.Location, job.EmploymentType,
		job.Description, job.DemandCode, job.SiteName, job.OnsiteContact,
		job.Shift, job.WorkSchedule, job.CompensationSummary, job.Accommodation,
		job.TransportRequirements, job.Owner, string(job.Status), string(job.Priority),
		strconv.Itoa(job.Headcount),
	}
	values = append(values, job.Requirements...)
	values = append(values, job.RequiredDocuments...)
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func crmPipelineProjectLead(state CRMState, lead Lead) LeadListItem {
	item := LeadListItem{Lead: lead, StageEvents: []LeadStageEvent{}}
	if index := crmPipelineCandidateIndex(state.Candidates, lead.CandidateID); index >= 0 {
		item.CandidateName = state.Candidates[index].FullName
	}
	if index := crmPipelineJobIndex(state.Jobs, lead.JobID); index >= 0 {
		item.JobTitle = state.Jobs[index].Title
	}
	for _, stageEvent := range state.LeadStageEvents {
		if stageEvent.LeadID == lead.ID {
			item.StageEvents = append(item.StageEvents, stageEvent)
		}
	}
	sort.Slice(item.StageEvents, func(i, j int) bool {
		if item.StageEvents[i].CreatedAt.Equal(item.StageEvents[j].CreatedAt) {
			return item.StageEvents[i].ID < item.StageEvents[j].ID
		}
		return item.StageEvents[i].CreatedAt.Before(item.StageEvents[j].CreatedAt)
	})
	return item
}

func crmPipelineJobIndex(jobs []Job, id string) int {
	for i := range jobs {
		if jobs[i].ID == id {
			return i
		}
	}
	return -1
}

func crmPipelineCandidateIndex(candidates []Candidate, id string) int {
	for i := range candidates {
		if candidates[i].ID == id {
			return i
		}
	}
	return -1
}

func crmPipelineLeadIndex(leads []Lead, id string) int {
	for i := range leads {
		if leads[i].ID == id {
			return i
		}
	}
	return -1
}

func crmPipelineDemandRevisionIndex(revisions []DemandRevision, id string) int {
	for i := range revisions {
		if revisions[i].ID == id {
			return i
		}
	}
	return -1
}

func crmPipelineStoredJob(store *CRMStore, id string) (Job, error) {
	state := store.Snapshot()
	index := crmPipelineJobIndex(state.Jobs, id)
	if index < 0 {
		return Job{}, &crmPipelineNotFoundError{resource: "job"}
	}
	return state.Jobs[index], nil
}

func crmPipelineApplyDemandSnapshot(job *Job, snapshot JobDemandSnapshot) {
	job.Title = snapshot.Title
	job.Company = snapshot.Company
	job.Location = snapshot.Location
	job.EmploymentType = snapshot.EmploymentType
	job.Description = snapshot.Description
	job.Requirements = append([]string{}, snapshot.Requirements...)
	job.DemandCode = snapshot.DemandCode
	job.SiteName = snapshot.SiteName
	job.OnsiteContact = snapshot.OnsiteContact
	job.Headcount = snapshot.Headcount
	job.Priority = snapshot.Priority
	job.StartAt = cloneCRMTimePointer(snapshot.StartAt)
	job.DeadlineAt = cloneCRMTimePointer(snapshot.DeadlineAt)
	job.Shift = snapshot.Shift
	job.WorkSchedule = snapshot.WorkSchedule
	job.CompensationSummary = snapshot.CompensationSummary
	job.Accommodation = snapshot.Accommodation
	job.TransportRequirements = snapshot.TransportRequirements
	job.RequiredDocuments = append([]string{}, snapshot.RequiredDocuments...)
}

func crmPipelineMutationTime(previous time.Time) time.Time {
	now := time.Now().UTC()
	if now.Before(previous) {
		return previous
	}
	return now
}

func crmPipelinePage[T any](values []T, offset, limit int) []T {
	if offset >= len(values) {
		return []T{}
	}
	end := offset + limit
	if end > len(values) {
		end = len(values)
	}
	return values[offset:end]
}
