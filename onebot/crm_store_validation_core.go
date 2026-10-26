package main

import (
	"crypto/sha256"
	"strconv"
	"strings"
	"time"
)

func validateNonNilStateSlices(state *CRMState) error {
	switch {
	case state.RecruitingProject.SourceGroups == nil:
		return NewCRMValidationError("recruiting_project.source_groups", "must not be null")
	case state.Candidates == nil:
		return NewCRMValidationError("candidates", "must not be null")
	case state.Jobs == nil:
		return NewCRMValidationError("jobs", "must not be null")
	case state.Leads == nil:
		return NewCRMValidationError("leads", "must not be null")
	case state.LeadStageEvents == nil:
		return NewCRMValidationError("lead_stage_events", "must not be null")
	case state.OnboardingCases == nil:
		return NewCRMValidationError("onboarding_cases", "must not be null")
	case state.ServiceRequests == nil:
		return NewCRMValidationError("service_requests", "must not be null")
	case state.FundCallbacks == nil:
		return NewCRMValidationError("fund_callbacks", "must not be null")
	case state.JobProvenance == nil:
		return NewCRMValidationError("job_provenance", "must not be null")
	case state.AuditEvents == nil:
		return NewCRMValidationError("audit_events", "must not be null")
	case state.Tasks == nil:
		return NewCRMValidationError("tasks", "must not be null")
	case state.Notes == nil:
		return NewCRMValidationError("notes", "must not be null")
	case state.MessageEvents == nil:
		return NewCRMValidationError("message_events", "must not be null")
	case state.ForwardingRules == nil:
		return NewCRMValidationError("forwarding_rules", "must not be null")
	default:
		return nil
	}
}

func validateCounterBounds(counters IDCounters) error {
	values := []struct {
		name  string
		value uint64
	}{
		{"candidate", counters.Candidate},
		{"job", counters.Job},
		{"lead", counters.Lead},
		{"task", counters.Task},
		{"note", counters.Note},
		{"event", counters.Event},
		{"rule", counters.Rule},
		{"stage_event", counters.StageEvent},
		{"onboarding", counters.Onboarding},
		{"service_request", counters.ServiceRequest},
		{"fund_callback", counters.FundCallback},
		{"demand_revision", counters.DemandRevision},
		{"operation_batch", counters.OperationBatch},
		{"service_queue", counters.ServiceQueue},
		{"fund_ledger", counters.FundLedger},
		{"audit", counters.Audit},
		{"source_group", counters.SourceGroup},
		{"job_provenance", counters.JobProvenance},
	}
	for _, counter := range values {
		if counter.value > crmMaxIDCounter {
			return NewCRMValidationError("counters."+counter.name, "exceeds the ID range")
		}
	}
	return nil
}

func validateEntityID(field, id, prefix string, counter uint64, allIDs map[string]string) error {
	if err := validateStoredCRMString(field, id, 80, true, false); err != nil {
		return err
	}
	expectedLength := len(prefix) + 1 + 8
	if len(id) != expectedLength || !strings.HasPrefix(id, prefix+"_") {
		return NewCRMValidationError(field, "has an invalid format")
	}
	numberText := id[len(prefix)+1:]
	for _, character := range numberText {
		if character < '0' || character > '9' {
			return NewCRMValidationError(field, "has an invalid format")
		}
	}
	number, err := strconv.ParseUint(numberText, 10, 64)
	if err != nil || number == 0 || number > crmMaxIDCounter {
		return NewCRMValidationError(field, "has an invalid counter")
	}
	if number > counter {
		return NewCRMValidationError(field, "is ahead of its counter")
	}
	if previous, exists := allIDs[id]; exists {
		return NewCRMValidationError(field, "duplicates "+previous)
	}
	allIDs[id] = field
	return nil
}

func validateRecruitingProjectConfig(
	project *RecruitingProjectConfig,
	sourceGroupCounter uint64,
	allIDs map[string]string,
) error {
	if project == nil {
		return NewCRMValidationError("recruiting_project", "is required")
	}
	if err := validateStoredCRMString("recruiting_project.organization_name", project.OrganizationName, 160, true, false); err != nil {
		return err
	}
	if err := validateCRMVersion("recruiting_project.version", project.Version); err != nil {
		return err
	}
	if project.SourceGroups == nil {
		return NewCRMValidationError("recruiting_project.source_groups", "must not be null")
	}
	if len(project.SourceGroups) > crmMaxRuleCount {
		return NewCRMValidationError("recruiting_project.source_groups", "entity limit exceeded")
	}
	groupIDs := make(map[string]string, len(project.SourceGroups))
	for i := range project.SourceGroups {
		group := &project.SourceGroups[i]
		field := indexedField("recruiting_project.source_groups", i)
		if err := validateEntityID(field+".id", group.ID, "source", sourceGroupCounter, allIDs); err != nil {
			return err
		}
		if err := validateStoredCRMString(field+".group_id", group.GroupID, 256, true, false); err != nil {
			return err
		}
		if !strings.HasSuffix(group.GroupID, "@chatroom") {
			return NewCRMValidationError(field+".group_id", "must be a WeChat group identifier")
		}
		if previous, exists := groupIDs[group.GroupID]; exists {
			return NewCRMValidationError(field+".group_id", "duplicates source group "+previous)
		}
		groupIDs[group.GroupID] = group.ID
		if err := validateStoredCRMString(field+".display_name", group.DisplayName, 160, false, false); err != nil {
			return err
		}
		if err := validateStoredCRMString(field+".gate_mode", group.GateMode, 80, false, false); err != nil {
			return err
		}
		if group.GateMode != "" && group.GateMode != RecruitingSourceGateModeJobRelevance {
			return NewCRMValidationError(field+".gate_mode", "must be job_relevance")
		}
		if group.GateEnabled && group.GateMode == "" {
			return NewCRMValidationError(field+".gate_mode", "is required when gate is enabled")
		}
		if err := validateVersionedTimes(field, group.Version, group.CreatedAt, group.UpdatedAt); err != nil {
			return err
		}
	}
	return nil
}

func validateJobProvenance(field string, provenance *JobProvenance) error {
	if err := validateStoredCRMString(field+".job_id", provenance.JobID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".event_id", provenance.EventID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".message_key", provenance.MessageKey, 1024, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".group_id", provenance.GroupID, 256, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".author_id", provenance.AuthorID, 256, true, false); err != nil {
		return err
	}
	if err := validateCRMTime(field+".source_timestamp", provenance.SourceTimestamp); err != nil {
		return err
	}
	if err := validateSHA256Hex(field+".content_sha256", provenance.ContentSHA256, true); err != nil {
		return err
	}
	fingerprintRequired := provenance.Decision != JobIngestionNoise && provenance.Decision != JobIngestionFailure
	if err := validateSHA256Hex(field+".canonical_fingerprint", provenance.CanonicalFingerprint, fingerprintRequired); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".demand_code", provenance.DemandCode, 120, false, false); err != nil {
		return err
	}
	if !IsValidJobIngestionDecision(provenance.Decision) {
		return NewCRMValidationError(field+".decision", "is invalid")
	}
	if provenance.JobRevision > crmMaxIDCounter {
		return NewCRMValidationError(field+".job_revision", "exceeds the revision range")
	}
	return validateCRMTime(field+".created_at", provenance.CreatedAt)
}

func validateSHA256Hex(field, value string, required bool) error {
	if value == "" && !required {
		return nil
	}
	if len(value) != sha256.Size*2 {
		return NewCRMValidationError(field, "must be a SHA-256 hex digest")
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return NewCRMValidationError(field, "must be a lowercase SHA-256 hex digest")
		}
	}
	return nil
}

func validateCandidate(field string, candidate *Candidate) error {
	if err := validateStoredCRMString(field+".full_name", candidate.FullName, 120, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".wechat_id", candidate.WeChatID, 128, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".phone", candidate.Phone, 40, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".email", candidate.Email, 254, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".location", candidate.Location, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".source", candidate.Source, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".source_event_id", candidate.SourceEventID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMStringList(field+".skills", candidate.Skills, 32, 64); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".owner", candidate.Owner, 80, false, false); err != nil {
		return err
	}
	if !IsValidCandidateStatus(candidate.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if err := validateStoredCRMString(field+".summary", candidate.Summary, 4000, false, true); err != nil {
		return err
	}
	return validateVersionedTimes(field, candidate.Version, candidate.CreatedAt, candidate.UpdatedAt)
}

func validateJob(field string, job *Job) error {
	if err := validateStoredCRMString(field+".title", job.Title, 160, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".company", job.Company, 160, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".location", job.Location, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".employment_type", job.EmploymentType, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".description", job.Description, 8000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMStringList(field+".requirements", job.Requirements, 64, 1000); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".demand_code", job.DemandCode, 120, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".site_name", job.SiteName, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".onsite_contact", job.OnsiteContact, 160, false, false); err != nil {
		return err
	}
	if job.Headcount < 0 || job.Headcount > 1000000 {
		return NewCRMValidationError(field+".headcount", "must be between 0 and 1000000")
	}
	if job.Priority != "" && !IsValidJobPriority(job.Priority) {
		return NewCRMValidationError(field+".priority", "is invalid")
	}
	if err := validateCRMOptionalTime(field+".start_at", job.StartAt); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".deadline_at", job.DeadlineAt); err != nil {
		return err
	}
	if job.StartAt != nil && job.DeadlineAt != nil && job.DeadlineAt.Before(*job.StartAt) {
		return NewCRMValidationError(field+".deadline_at", "must not precede start_at")
	}
	if err := validateStoredCRMString(field+".shift", job.Shift, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".work_schedule", job.WorkSchedule, 2000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".compensation_summary", job.CompensationSummary, 2000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".accommodation", job.Accommodation, 2000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".transport_requirements", job.TransportRequirements, 2000, false, true); err != nil {
		return err
	}
	if job.RequiredDocuments != nil {
		if err := validateStoredCRMStringList(field+".required_documents", job.RequiredDocuments, 64, 200); err != nil {
			return err
		}
	}
	if err := validateStoredCRMString(field+".owner", job.Owner, 80, false, false); err != nil {
		return err
	}
	if !IsValidJobStatus(job.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if err := validateStoredCRMString(field+".active_demand_revision_id", job.ActiveDemandRevisionID, 80, false, false); err != nil {
		return err
	}
	if job.DemandRevisionNumber > crmMaxIDCounter {
		return NewCRMValidationError(field+".demand_revision_number", "exceeds the revision range")
	}
	return validateVersionedTimes(field, job.Version, job.CreatedAt, job.UpdatedAt)
}

func jobDemandSnapshot(job Job) JobDemandSnapshot {
	return JobDemandSnapshot{
		Title:                 job.Title,
		Company:               job.Company,
		Location:              job.Location,
		EmploymentType:        job.EmploymentType,
		Description:           job.Description,
		Requirements:          append([]string{}, job.Requirements...),
		DemandCode:            job.DemandCode,
		SiteName:              job.SiteName,
		OnsiteContact:         job.OnsiteContact,
		Headcount:             job.Headcount,
		Priority:              job.Priority,
		StartAt:               cloneCRMTimePointer(job.StartAt),
		DeadlineAt:            cloneCRMTimePointer(job.DeadlineAt),
		Shift:                 job.Shift,
		WorkSchedule:          job.WorkSchedule,
		CompensationSummary:   job.CompensationSummary,
		Accommodation:         job.Accommodation,
		TransportRequirements: job.TransportRequirements,
		RequiredDocuments:     append([]string{}, job.RequiredDocuments...),
	}
}

func validateJobDemandSnapshot(field string, snapshot *JobDemandSnapshot) error {
	job := Job{
		Title:                 snapshot.Title,
		Company:               snapshot.Company,
		Location:              snapshot.Location,
		EmploymentType:        snapshot.EmploymentType,
		Description:           snapshot.Description,
		Requirements:          snapshot.Requirements,
		DemandCode:            snapshot.DemandCode,
		SiteName:              snapshot.SiteName,
		OnsiteContact:         snapshot.OnsiteContact,
		Headcount:             snapshot.Headcount,
		Priority:              snapshot.Priority,
		StartAt:               snapshot.StartAt,
		DeadlineAt:            snapshot.DeadlineAt,
		Shift:                 snapshot.Shift,
		WorkSchedule:          snapshot.WorkSchedule,
		CompensationSummary:   snapshot.CompensationSummary,
		Accommodation:         snapshot.Accommodation,
		TransportRequirements: snapshot.TransportRequirements,
		RequiredDocuments:     snapshot.RequiredDocuments,
		Status:                JobStatusOpen,
		Version:               1,
		CreatedAt:             time.Unix(1, 0).UTC(),
		UpdatedAt:             time.Unix(1, 0).UTC(),
	}
	return validateJob(field, &job)
}

func validateDemandRevision(field string, revision *DemandRevision) error {
	if err := validateStoredCRMString(field+".job_id", revision.JobID, 80, true, false); err != nil {
		return err
	}
	if revision.Number == 0 || revision.Number > crmMaxIDCounter {
		return NewCRMValidationError(field+".number", "must be within the revision range")
	}
	if err := validateJobDemandSnapshot(field+".snapshot", &revision.Snapshot); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".created_by", revision.CreatedBy, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".reason", revision.Reason, 120, true, false); err != nil {
		return err
	}
	return validateCRMTime(field+".created_at", revision.CreatedAt)
}

func validateLead(field string, lead *Lead) error {
	if err := validateStoredCRMString(field+".candidate_id", lead.CandidateID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".job_id", lead.JobID, 80, true, false); err != nil {
		return err
	}
	if !IsValidLeadStage(lead.Stage) {
		return NewCRMValidationError(field+".stage", "is invalid")
	}
	if err := validateStoredCRMString(field+".owner", lead.Owner, 80, false, false); err != nil {
		return err
	}
	if err := validateVersionedTimes(field, lead.Version, lead.CreatedAt, lead.UpdatedAt); err != nil {
		return err
	}
	if err := validateCRMTime(field+".stage_changed_at", lead.StageChangedAt); err != nil {
		return err
	}
	if lead.StageChangedAt.Before(lead.CreatedAt) || lead.StageChangedAt.After(lead.UpdatedAt) {
		return NewCRMValidationError(field+".stage_changed_at", "must be between created_at and updated_at")
	}
	return nil
}

func validateLeadStageEvent(field string, event *LeadStageEvent) error {
	if err := validateStoredCRMString(field+".lead_id", event.LeadID, 80, true, false); err != nil {
		return err
	}
	if !IsValidLeadStage(event.FromStage) {
		return NewCRMValidationError(field+".from_stage", "is invalid")
	}
	if !IsValidLeadStage(event.ToStage) {
		return NewCRMValidationError(field+".to_stage", "is invalid")
	}
	if !CanTransitionLeadStage(event.FromStage, event.ToStage) {
		return NewCRMValidationError(field, "contains an invalid lead stage transition")
	}
	if err := validateStoredCRMString(field+".reason", event.Reason, 2000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".actor", event.Actor, 80, true, false); err != nil {
		return err
	}
	return validateCRMTime(field+".created_at", event.CreatedAt)
}

func validateOnboardingCase(field string, onboarding *OnboardingCase) error {
	if err := validateStoredCRMString(field+".lead_id", onboarding.LeadID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".candidate_id", onboarding.CandidateID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".job_id", onboarding.JobID, 80, true, false); err != nil {
		return err
	}
	if !IsValidOnboardingStatus(onboarding.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if err := validateCRMOptionalTime(field+".entry_at", onboarding.EntryAt); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".entry_address", onboarding.EntryAddress, 500, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".site_contact", onboarding.SiteContact, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMStringList(field+".required_documents", onboarding.RequiredDocuments, 64, 200); err != nil {
		return err
	}
	if err := validateStoredCRMStringList(field+".completed_documents", onboarding.CompletedDocuments, 64, 200); err != nil {
		return err
	}
	required := make(map[string]struct{}, len(onboarding.RequiredDocuments))
	for _, document := range onboarding.RequiredDocuments {
		required[document] = struct{}{}
	}
	for i, document := range onboarding.CompletedDocuments {
		if _, ok := required[document]; !ok {
			return NewCRMValidationError(indexedField(field+".completed_documents", i), "is not required")
		}
	}
	if err := validateStoredCRMStringList(field+".missing_information", onboarding.MissingInformation, 64, 500); err != nil {
		return err
	}
	if err := validateFleetCoordination(field+".fleet", &onboarding.Fleet); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".block_reason", onboarding.BlockReason, 2000, false, true); err != nil {
		return err
	}
	if err := validateVersionedTimes(field, onboarding.Version, onboarding.CreatedAt, onboarding.UpdatedAt); err != nil {
		return err
	}
	if onboarding.Fleet.UpdatedAt.Before(onboarding.CreatedAt) || onboarding.Fleet.UpdatedAt.After(onboarding.UpdatedAt) {
		return NewCRMValidationError(field+".fleet.updated_at", "must be between created_at and updated_at")
	}
	return nil
}

func validateFleetCoordination(field string, fleet *FleetCoordination) error {
	if err := validateStoredCRMString(field+".pickup_address", fleet.PickupAddress, 500, false, true); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".pickup_at", fleet.PickupAt); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".destination", fleet.Destination, 500, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".luggage_note", fleet.LuggageNote, 1000, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".vehicle_requirement", fleet.VehicleRequirement, 500, false, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".fleet_contact", fleet.FleetContact, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".driver_name", fleet.DriverName, 160, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".driver_phone", fleet.DriverPhone, 40, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".vehicle_plate", fleet.VehiclePlate, 40, false, false); err != nil {
		return err
	}
	if !IsValidFleetStatus(fleet.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if fleet.Required && fleet.Status == FleetStatusNotRequired {
		return NewCRMValidationError(field+".status", "cannot be not_required when fleet is required")
	}
	if !fleet.Required && fleet.Status != FleetStatusNotRequired {
		return NewCRMValidationError(field+".status", "must be not_required when fleet is not required")
	}
	return validateCRMTime(field+".updated_at", fleet.UpdatedAt)
}
