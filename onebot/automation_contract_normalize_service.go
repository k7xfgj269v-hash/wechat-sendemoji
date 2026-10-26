package main

import (
	"strconv"
	"strings"
)

func normalizeServiceRequestCreateArgs(field string, arguments ServiceRequestCreateArgs) (ServiceRequestCreateArgs, []AutomationToolReference, error) {
	candidate, err := normalizeAutomationEntityRef(field+".candidate", arguments.Candidate, AutomationEntityCandidate)
	if err != nil {
		return ServiceRequestCreateArgs{}, nil, err
	}
	job, err := normalizeOptionalAutomationEntityRef(field+".job", arguments.Job, AutomationEntityJob)
	if err != nil {
		return ServiceRequestCreateArgs{}, nil, err
	}
	onboarding, err := normalizeOptionalAutomationEntityRef(field+".onboarding", arguments.Onboarding, AutomationEntityOnboarding)
	if err != nil {
		return ServiceRequestCreateArgs{}, nil, err
	}
	create, err := crmServiceRequestNormalizeCreate(ServiceRequestCreate{
		CandidateID:      automationReferenceID(candidate, "cand_00000001"),
		JobID:            automationOptionalReferenceID(job, "job_00000001"),
		OnboardingCaseID: automationOptionalReferenceID(onboarding, "onboard_00000001"),
		Category:         arguments.Category,
		Priority:         arguments.Priority,
		Description:      arguments.Description,
		Owner:            arguments.Owner,
		RequiresHuman:    arguments.RequiresHuman,
		DueAt:            arguments.DueAt,
		Actor:            "automation",
	})
	if err != nil {
		return ServiceRequestCreateArgs{}, nil, err
	}
	arguments.Candidate = candidate
	arguments.Job = job
	arguments.Onboarding = onboarding
	arguments.Category = create.Category
	arguments.Priority = create.Priority
	arguments.Description = create.Description
	arguments.Owner = create.Owner
	arguments.RequiresHuman = create.RequiresHuman
	arguments.DueAt = create.DueAt
	references := automationSingleToolReference("candidate", AutomationEntityCandidate, candidate)
	references = appendOptionalAutomationReference(references, "job", AutomationEntityJob, job)
	references = appendOptionalAutomationReference(references, "onboarding", AutomationEntityOnboarding, onboarding)
	return arguments, references, nil
}

func normalizeServiceRequestUpdateArgs(field string, arguments ServiceRequestUpdateArgs) (ServiceRequestUpdateArgs, []AutomationToolReference, error) {
	request, err := normalizeAutomationEntityRef(field+".service_request", arguments.ServiceRequest, AutomationEntityServiceRequest)
	if err != nil {
		return ServiceRequestUpdateArgs{}, nil, err
	}
	candidate, err := normalizeOptionalAutomationEntityRef(field+".patch.candidate", arguments.Patch.Candidate, AutomationEntityCandidate)
	if err != nil {
		return ServiceRequestUpdateArgs{}, nil, err
	}
	job, err := normalizeOptionalAutomationEntityRef(field+".patch.job", arguments.Patch.Job, AutomationEntityJob)
	if err != nil {
		return ServiceRequestUpdateArgs{}, nil, err
	}
	onboarding, err := normalizeOptionalAutomationEntityRef(field+".patch.onboarding", arguments.Patch.Onboarding, AutomationEntityOnboarding)
	if err != nil {
		return ServiceRequestUpdateArgs{}, nil, err
	}
	update, err := crmServiceRequestNormalizeUpdate(arguments.Patch.serviceRequestUpdate(1, candidate, job, onboarding))
	if err != nil {
		return ServiceRequestUpdateArgs{}, nil, err
	}
	arguments.ServiceRequest = request
	arguments.Patch = automationServiceRequestUpdatePatch(update, candidate, job, onboarding)
	references := automationSingleToolReference("service_request", AutomationEntityServiceRequest, request)
	references = appendOptionalAutomationReference(references, "patch.candidate", AutomationEntityCandidate, candidate)
	references = appendOptionalAutomationReference(references, "patch.job", AutomationEntityJob, job)
	references = appendOptionalAutomationReference(references, "patch.onboarding", AutomationEntityOnboarding, onboarding)
	return arguments, references, nil
}

func normalizeFundCallbackCreateArgs(field string, arguments FundCallbackCreateArgs) (FundCallbackCreateArgs, []AutomationToolReference, error) {
	candidate, err := normalizeAutomationEntityRef(field+".candidate", arguments.Candidate, AutomationEntityCandidate)
	if err != nil {
		return FundCallbackCreateArgs{}, nil, err
	}
	job, err := normalizeOptionalAutomationEntityRef(field+".job", arguments.Job, AutomationEntityJob)
	if err != nil {
		return FundCallbackCreateArgs{}, nil, err
	}
	lead, err := normalizeOptionalAutomationEntityRef(field+".lead", arguments.Lead, AutomationEntityLead)
	if err != nil {
		return FundCallbackCreateArgs{}, nil, err
	}
	onboarding, err := normalizeOptionalAutomationEntityRef(field+".onboarding", arguments.Onboarding, AutomationEntityOnboarding)
	if err != nil {
		return FundCallbackCreateArgs{}, nil, err
	}
	create, err := crmNormalizeFundCallbackCreate(FundCallbackCreate{
		CandidateID:      automationReferenceID(candidate, "cand_00000001"),
		JobID:            automationOptionalReferenceID(job, "job_00000001"),
		LeadID:           automationOptionalReferenceID(lead, "lead_00000001"),
		OnboardingCaseID: automationOptionalReferenceID(onboarding, "onboard_00000001"),
		AmountMinor:      arguments.AmountMinor,
		Currency:         arguments.Currency,
		Condition:        arguments.Condition,
		PromisedBy:       arguments.PromisedBy,
		DueAt:            arguments.DueAt,
		Evidence:         arguments.Evidence,
	})
	if err != nil {
		return FundCallbackCreateArgs{}, nil, err
	}
	arguments.Candidate = candidate
	arguments.Job = job
	arguments.Lead = lead
	arguments.Onboarding = onboarding
	arguments.AmountMinor = create.AmountMinor
	arguments.Currency = create.Currency
	arguments.Condition = create.Condition
	arguments.PromisedBy = create.PromisedBy
	arguments.DueAt = create.DueAt
	arguments.Evidence = create.Evidence
	references := automationSingleToolReference("candidate", AutomationEntityCandidate, candidate)
	references = appendOptionalAutomationReference(references, "job", AutomationEntityJob, job)
	references = appendOptionalAutomationReference(references, "lead", AutomationEntityLead, lead)
	references = appendOptionalAutomationReference(references, "onboarding", AutomationEntityOnboarding, onboarding)
	return arguments, references, nil
}

func normalizeReplyAudienceTextArgs(field string, arguments ReplyAudienceTextArgs) (ReplyAudienceTextArgs, error) {
	audienceID, err := normalizeAutomationString(field+".audience_id", arguments.AudienceID, 80, true, false)
	if err != nil || !automationProviderIdentifier(audienceID) {
		return ReplyAudienceTextArgs{}, NewAutomationValidationError(field+".audience_id", "is invalid")
	}
	reply, err := normalizeReplyPrivateTextArgs(field, ReplyPrivateTextArgs{Intent: arguments.Intent, Text: arguments.Text})
	if err != nil {
		return ReplyAudienceTextArgs{}, err
	}
	arguments.AudienceID = audienceID
	arguments.Intent = reply.Intent
	arguments.Text = reply.Text
	return arguments, nil
}

func normalizeForwardEventArgs(field string, arguments ForwardEventArgs) (ForwardEventArgs, error) {
	var err error
	if arguments.EventID, err = normalizeAutomationString(field+".event_id", arguments.EventID, 80, true, false); err != nil {
		return ForwardEventArgs{}, err
	}
	if !automationForwardResourceID(arguments.EventID, "event") {
		return ForwardEventArgs{}, NewAutomationValidationError(field+".event_id", "is invalid")
	}
	if arguments.RuleID, err = normalizeAutomationString(field+".rule_id", arguments.RuleID, 80, true, false); err != nil {
		return ForwardEventArgs{}, err
	}
	if !automationForwardResourceID(arguments.RuleID, "rule") {
		return ForwardEventArgs{}, NewAutomationValidationError(field+".rule_id", "is invalid")
	}
	if arguments.Reason, err = normalizeAutomationString(field+".reason", arguments.Reason, 500, false, true); err != nil {
		return ForwardEventArgs{}, err
	}
	return arguments, nil
}

func automationForwardResourceID(value, prefix string) bool {
	if len(value) != len(prefix)+9 || !strings.HasPrefix(value, prefix+"_") {
		return false
	}
	counter, err := strconv.ParseUint(value[len(prefix)+1:], 10, 64)
	return err == nil && counter > 0
}

func normalizeAutomationEntityRef(field string, reference AutomationEntityRef, entityType string) (AutomationEntityRef, error) {
	var err error
	if reference.ID, err = normalizeAutomationString(field+".id", reference.ID, 80, false, false); err != nil {
		return AutomationEntityRef{}, err
	}
	if reference.FromCall, err = normalizeAutomationString(field+".from_call", reference.FromCall, 80, false, false); err != nil {
		return AutomationEntityRef{}, err
	}

	switch {
	case reference.ID != "":
		if reference.FromCall != "" {
			return AutomationEntityRef{}, NewAutomationValidationError(field, "must use exactly one reference mode")
		}
		if reference.ExpectedVersion == 0 {
			return AutomationEntityRef{}, NewAutomationValidationError(field+".expected_version", "must be greater than zero for an existing entity")
		}
		if !isAutomationEntityID(reference.ID, entityType) {
			return AutomationEntityRef{}, NewAutomationValidationError(field+".id", "does not match the expected entity type")
		}
	case reference.FromCall != "":
		if reference.ExpectedVersion != 0 {
			return AutomationEntityRef{}, NewAutomationValidationError(field+".expected_version", "must be absent for a call reference")
		}
	default:
		return AutomationEntityRef{}, NewAutomationValidationError(field, "must reference an existing entity or a tool call")
	}
	return reference, nil
}

func normalizeOptionalAutomationEntityRef(
	field string,
	reference *AutomationEntityRef,
	entityType string,
) (*AutomationEntityRef, error) {
	if reference == nil {
		return nil, nil
	}
	normalized, err := normalizeAutomationEntityRef(field, *reference, entityType)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func automationSingleToolReference(field, entityType string, reference AutomationEntityRef) []AutomationToolReference {
	return []AutomationToolReference{{
		Field:      field,
		EntityType: entityType,
		Reference:  reference,
	}}
}

func appendOptionalAutomationReference(
	references []AutomationToolReference,
	field string,
	entityType string,
	reference *AutomationEntityRef,
) []AutomationToolReference {
	if reference == nil {
		return references
	}
	return append(references, AutomationToolReference{
		Field:      field,
		EntityType: entityType,
		Reference:  *reference,
	})
}

func automationReferenceID(reference AutomationEntityRef, fallback string) string {
	if reference.ID != "" {
		return reference.ID
	}
	return fallback
}

func automationOptionalReferenceID(reference *AutomationEntityRef, fallback string) string {
	if reference == nil {
		return ""
	}
	return automationReferenceID(*reference, fallback)
}

func (patch AutomationJobUpdatePatch) jobUpdate(version uint64) JobUpdate {
	return JobUpdate{
		ExpectedVersion:       version,
		Title:                 patch.Title,
		Company:               patch.Company,
		Location:              patch.Location,
		EmploymentType:        patch.EmploymentType,
		Description:           patch.Description,
		Requirements:          patch.Requirements,
		DemandCode:            patch.DemandCode,
		SiteName:              patch.SiteName,
		OnsiteContact:         patch.OnsiteContact,
		Headcount:             patch.Headcount,
		Priority:              patch.Priority,
		StartAt:               patch.StartAt,
		DeadlineAt:            patch.DeadlineAt,
		ClearStartAt:          patch.ClearStartAt,
		ClearDeadlineAt:       patch.ClearDeadlineAt,
		Shift:                 patch.Shift,
		WorkSchedule:          patch.WorkSchedule,
		CompensationSummary:   patch.CompensationSummary,
		Accommodation:         patch.Accommodation,
		TransportRequirements: patch.TransportRequirements,
		RequiredDocuments:     patch.RequiredDocuments,
		Owner:                 patch.Owner,
		Status:                patch.Status,
	}
}

func automationJobUpdatePatch(update JobUpdate) AutomationJobUpdatePatch {
	return AutomationJobUpdatePatch{
		Title:                 update.Title,
		Company:               update.Company,
		Location:              update.Location,
		EmploymentType:        update.EmploymentType,
		Description:           update.Description,
		Requirements:          update.Requirements,
		DemandCode:            update.DemandCode,
		SiteName:              update.SiteName,
		OnsiteContact:         update.OnsiteContact,
		Headcount:             update.Headcount,
		Priority:              update.Priority,
		StartAt:               update.StartAt,
		DeadlineAt:            update.DeadlineAt,
		ClearStartAt:          update.ClearStartAt,
		ClearDeadlineAt:       update.ClearDeadlineAt,
		Shift:                 update.Shift,
		WorkSchedule:          update.WorkSchedule,
		CompensationSummary:   update.CompensationSummary,
		Accommodation:         update.Accommodation,
		TransportRequirements: update.TransportRequirements,
		RequiredDocuments:     update.RequiredDocuments,
		Owner:                 update.Owner,
		Status:                update.Status,
	}
}

func (patch AutomationOnboardingUpdatePatch) onboardingUpdate(version uint64) OnboardingUpdate {
	return OnboardingUpdate{
		ExpectedVersion:    version,
		EntryAt:            patch.EntryAt,
		ClearEntryAt:       patch.ClearEntryAt,
		EntryAddress:       patch.EntryAddress,
		SiteContact:        patch.SiteContact,
		RequiredDocuments:  patch.RequiredDocuments,
		CompletedDocuments: patch.CompletedDocuments,
		MissingInformation: patch.MissingInformation,
		BlockReason:        patch.BlockReason,
		Actor:              "automation",
	}
}

func automationOnboardingUpdatePatch(update OnboardingUpdate) AutomationOnboardingUpdatePatch {
	return AutomationOnboardingUpdatePatch{
		EntryAt:            update.EntryAt,
		ClearEntryAt:       update.ClearEntryAt,
		EntryAddress:       update.EntryAddress,
		SiteContact:        update.SiteContact,
		RequiredDocuments:  update.RequiredDocuments,
		CompletedDocuments: update.CompletedDocuments,
		MissingInformation: update.MissingInformation,
		BlockReason:        update.BlockReason,
	}
}

func (patch AutomationFleetUpdatePatch) fleetUpdate(version uint64) FleetUpdate {
	return FleetUpdate{
		ExpectedVersion:    version,
		Required:           patch.Required,
		PickupAddress:      patch.PickupAddress,
		PickupAt:           patch.PickupAt,
		ClearPickupAt:      patch.ClearPickupAt,
		Destination:        patch.Destination,
		LuggageNote:        patch.LuggageNote,
		VehicleRequirement: patch.VehicleRequirement,
		FleetContact:       patch.FleetContact,
		DriverName:         patch.DriverName,
		DriverPhone:        patch.DriverPhone,
		VehiclePlate:       patch.VehiclePlate,
		Actor:              "automation",
	}
}

func automationFleetUpdatePatch(update FleetUpdate) AutomationFleetUpdatePatch {
	return AutomationFleetUpdatePatch{
		Required:           update.Required,
		PickupAddress:      update.PickupAddress,
		PickupAt:           update.PickupAt,
		ClearPickupAt:      update.ClearPickupAt,
		Destination:        update.Destination,
		LuggageNote:        update.LuggageNote,
		VehicleRequirement: update.VehicleRequirement,
		FleetContact:       update.FleetContact,
		DriverName:         update.DriverName,
		DriverPhone:        update.DriverPhone,
		VehiclePlate:       update.VehiclePlate,
	}
}

func (patch AutomationServiceRequestUpdatePatch) serviceRequestUpdate(
	version uint64,
	candidate *AutomationEntityRef,
	job *AutomationEntityRef,
	onboarding *AutomationEntityRef,
) ServiceRequestUpdate {
	var candidateID, jobID, onboardingID *string
	if candidate != nil {
		value := automationReferenceID(*candidate, "cand_00000001")
		candidateID = &value
	}
	if job != nil {
		value := automationReferenceID(*job, "job_00000001")
		jobID = &value
	}
	if onboarding != nil {
		value := automationReferenceID(*onboarding, "onboard_00000001")
		onboardingID = &value
	}
	return ServiceRequestUpdate{
		ExpectedVersion:  version,
		CandidateID:      candidateID,
		JobID:            jobID,
		OnboardingCaseID: onboardingID,
		Category:         patch.Category,
		Priority:         patch.Priority,
		Description:      patch.Description,
		Owner:            patch.Owner,
		RequiresHuman:    patch.RequiresHuman,
		DueAt:            patch.DueAt,
		ClearDueAt:       patch.ClearDueAt,
		Resolution:       patch.Resolution,
		Actor:            "automation",
	}
}

func automationServiceRequestUpdatePatch(
	update ServiceRequestUpdate,
	candidate *AutomationEntityRef,
	job *AutomationEntityRef,
	onboarding *AutomationEntityRef,
) AutomationServiceRequestUpdatePatch {
	return AutomationServiceRequestUpdatePatch{
		Candidate:     candidate,
		Job:           job,
		Onboarding:    onboarding,
		Category:      update.Category,
		Priority:      update.Priority,
		Description:   update.Description,
		Owner:         update.Owner,
		RequiresHuman: update.RequiresHuman,
		DueAt:         update.DueAt,
		ClearDueAt:    update.ClearDueAt,
		Resolution:    update.Resolution,
	}
}

func isAutomationEntityID(id, entityType string) bool {
	prefix := ""
	switch entityType {
	case AutomationEntityCandidate:
		prefix = "cand"
	case AutomationEntityJob:
		prefix = "job"
	case AutomationEntityLead:
		prefix = "lead"
	case AutomationEntityTask:
		prefix = "task"
	case AutomationEntityNote:
		prefix = "note"
	case AutomationEntityOnboarding:
		prefix = "onboard"
	case AutomationEntityServiceRequest:
		prefix = "service"
	case AutomationEntityFundCallback:
		prefix = "fund"
	default:
		return false
	}
	if len(id) != len(prefix)+9 || !strings.HasPrefix(id, prefix+"_") {
		return false
	}
	counterText := id[len(prefix)+1:]
	counter, err := strconv.ParseUint(counterText, 10, 64)
	return err == nil && counter > 0
}
