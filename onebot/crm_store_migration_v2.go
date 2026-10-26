package main

import (
	"bytes"
	"encoding/json"
	"io"
)

func migrateCRMStateV3(legacy crmStateV3) (CRMState, error) {
	if legacy.SchemaVersion != 3 {
		return CRMState{}, NewCRMValidationError("schema_version", "schema 3 snapshot is required")
	}
	if err := validateCRMDecodedV3Shape(&legacy); err != nil {
		return CRMState{}, err
	}
	state := crmStateFromV3(legacy)
	normalizeCRMStateV2Defaults(&state)
	if err := validateCRMStateStrict(&state); err != nil {
		return CRMState{}, err
	}
	return state, nil
}

func crmStateFromV3(legacy crmStateV3) CRMState {
	state := CRMState{
		SchemaVersion: CRMSchemaVersion,
		Counters: IDCounters{
			Candidate:      legacy.Counters.Candidate,
			Job:            legacy.Counters.Job,
			Lead:           legacy.Counters.Lead,
			Task:           legacy.Counters.Task,
			Note:           legacy.Counters.Note,
			Event:          legacy.Counters.Event,
			Rule:           legacy.Counters.Rule,
			StageEvent:     legacy.Counters.StageEvent,
			Onboarding:     legacy.Counters.Onboarding,
			ServiceRequest: legacy.Counters.ServiceRequest,
			FundCallback:   legacy.Counters.FundCallback,
			DemandRevision: legacy.Counters.DemandRevision,
			OperationBatch: legacy.Counters.OperationBatch,
			ServiceQueue:   legacy.Counters.ServiceQueue,
			FundLedger:     legacy.Counters.FundLedger,
			Audit:          legacy.Counters.Audit,
		},
		RecruitingProject: RecruitingProjectConfig{
			OrganizationName:        CRMDefaultOrganizationName,
			Version:                 1,
			RequireAuthoritativeJob: true,
			SourceGroups:            []RecruitingSourceGroup{},
		},
		Candidates:         legacy.Candidates,
		Jobs:               legacy.Jobs,
		Leads:              legacy.Leads,
		LeadStageEvents:    legacy.LeadStageEvents,
		OnboardingCases:    legacy.OnboardingCases,
		ServiceRequests:    legacy.ServiceRequests,
		FundCallbacks:      legacy.FundCallbacks,
		DemandRevisions:    legacy.DemandRevisions,
		JobProvenance:      []JobProvenance{},
		OperationBatches:   legacy.OperationBatches,
		ServiceQueues:      legacy.ServiceQueues,
		FundCallbackLedger: legacy.FundCallbackLedger,
		AuditEvents:        legacy.AuditEvents,
		Tasks:              legacy.Tasks,
		Notes:              legacy.Notes,
		ForwardingRules:    legacy.ForwardingRules,
	}
	if legacy.MessageEvents != nil {
		state.MessageEvents = make([]MessageEvent, len(legacy.MessageEvents))
		for i, event := range legacy.MessageEvents {
			state.MessageEvents[i] = MessageEvent{
				ID:           event.ID,
				MessageKey:   event.MessageKey,
				Source:       event.Source,
				Mid:          event.Mid,
				ChatType:     event.ChatType,
				PeerID:       event.PeerID,
				AuthorID:     event.AuthorID,
				GroupID:      event.GroupID,
				Text:         event.Text,
				Kinds:        event.Kinds,
				Timestamp:    event.Timestamp,
				Suspicious:   event.Suspicious,
				ReviewStatus: event.ReviewStatus,
				CandidateID:  event.CandidateID,
				Version:      event.Version,
				CreatedAt:    event.CreatedAt,
				UpdatedAt:    event.UpdatedAt,
			}
		}
	}
	return state
}

func crmStateFromV2(legacy crmStateV2) CRMState {
	state := CRMState{
		SchemaVersion: CRMSchemaVersion,
		Counters: IDCounters{
			Candidate:      legacy.Counters.Candidate,
			Job:            legacy.Counters.Job,
			Lead:           legacy.Counters.Lead,
			Task:           legacy.Counters.Task,
			Note:           legacy.Counters.Note,
			Event:          legacy.Counters.Event,
			Rule:           legacy.Counters.Rule,
			StageEvent:     legacy.Counters.StageEvent,
			Onboarding:     legacy.Counters.Onboarding,
			ServiceRequest: legacy.Counters.ServiceRequest,
			FundCallback:   legacy.Counters.FundCallback,
			Audit:          legacy.Counters.Audit,
		},
		RecruitingProject: RecruitingProjectConfig{
			OrganizationName:        CRMDefaultOrganizationName,
			Version:                 1,
			RequireAuthoritativeJob: true,
			SourceGroups:            []RecruitingSourceGroup{},
		},
		Candidates:         legacy.Candidates,
		Leads:              legacy.Leads,
		LeadStageEvents:    legacy.LeadStageEvents,
		OnboardingCases:    legacy.OnboardingCases,
		FundCallbacks:      legacy.FundCallbacks,
		DemandRevisions:    []DemandRevision{},
		JobProvenance:      []JobProvenance{},
		OperationBatches:   []OperationBatch{},
		ServiceQueues:      []ServiceQueue{},
		FundCallbackLedger: []FundCallbackLedgerEntry{},
		AuditEvents:        legacy.AuditEvents,
		Tasks:              legacy.Tasks,
		Notes:              legacy.Notes,
		MessageEvents:      legacy.MessageEvents,
		ForwardingRules:    legacy.ForwardingRules,
	}
	if legacy.Jobs != nil {
		state.Jobs = make([]Job, len(legacy.Jobs))
		for i, job := range legacy.Jobs {
			state.Jobs[i] = Job{
				ID:                    job.ID,
				Title:                 job.Title,
				Company:               job.Company,
				Location:              job.Location,
				EmploymentType:        job.EmploymentType,
				Description:           job.Description,
				Requirements:          job.Requirements,
				DemandCode:            job.DemandCode,
				SiteName:              job.SiteName,
				OnsiteContact:         job.OnsiteContact,
				Headcount:             job.Headcount,
				Priority:              job.Priority,
				StartAt:               job.StartAt,
				DeadlineAt:            job.DeadlineAt,
				Shift:                 job.Shift,
				WorkSchedule:          job.WorkSchedule,
				CompensationSummary:   job.CompensationSummary,
				Accommodation:         job.Accommodation,
				TransportRequirements: job.TransportRequirements,
				RequiredDocuments:     job.RequiredDocuments,
				Owner:                 job.Owner,
				Status:                job.Status,
				Version:               job.Version,
				CreatedAt:             job.CreatedAt,
				UpdatedAt:             job.UpdatedAt,
			}
		}
	}
	if legacy.ServiceRequests != nil {
		state.ServiceRequests = make([]ServiceRequest, len(legacy.ServiceRequests))
		for i, request := range legacy.ServiceRequests {
			state.ServiceRequests[i] = ServiceRequest{
				ID:               request.ID,
				CandidateID:      request.CandidateID,
				JobID:            request.JobID,
				OnboardingCaseID: request.OnboardingCaseID,
				SourceEventID:    request.SourceEventID,
				Category:         request.Category,
				Priority:         request.Priority,
				Description:      request.Description,
				Owner:            request.Owner,
				Status:           request.Status,
				RequiresHuman:    request.RequiresHuman,
				DueAt:            request.DueAt,
				Resolution:       request.Resolution,
				Version:          request.Version,
				CreatedAt:        request.CreatedAt,
				UpdatedAt:        request.UpdatedAt,
				ResolvedAt:       request.ResolvedAt,
			}
		}
	}
	return state
}

func crmStateV2FromCRMStateWithoutV3(state CRMState) crmStateV2 {
	legacy := crmStateV2{
		SchemaVersion: 2,
		Counters: crmIDCountersV2{
			Candidate:      state.Counters.Candidate,
			Job:            state.Counters.Job,
			Lead:           state.Counters.Lead,
			Task:           state.Counters.Task,
			Note:           state.Counters.Note,
			Event:          state.Counters.Event,
			Rule:           state.Counters.Rule,
			StageEvent:     state.Counters.StageEvent,
			Onboarding:     state.Counters.Onboarding,
			ServiceRequest: state.Counters.ServiceRequest,
			FundCallback:   state.Counters.FundCallback,
			Audit:          state.Counters.Audit,
		},
		Candidates:      state.Candidates,
		Leads:           state.Leads,
		LeadStageEvents: state.LeadStageEvents,
		OnboardingCases: state.OnboardingCases,
		FundCallbacks:   state.FundCallbacks,
		AuditEvents:     state.AuditEvents,
		Tasks:           state.Tasks,
		Notes:           state.Notes,
		MessageEvents:   state.MessageEvents,
		ForwardingRules: state.ForwardingRules,
	}
	if state.Jobs != nil {
		legacy.Jobs = make([]crmJobV2, len(state.Jobs))
		for i, job := range state.Jobs {
			legacy.Jobs[i] = crmJobV2{
				ID:                    job.ID,
				Title:                 job.Title,
				Company:               job.Company,
				Location:              job.Location,
				EmploymentType:        job.EmploymentType,
				Description:           job.Description,
				Requirements:          job.Requirements,
				DemandCode:            job.DemandCode,
				SiteName:              job.SiteName,
				OnsiteContact:         job.OnsiteContact,
				Headcount:             job.Headcount,
				Priority:              job.Priority,
				StartAt:               job.StartAt,
				DeadlineAt:            job.DeadlineAt,
				Shift:                 job.Shift,
				WorkSchedule:          job.WorkSchedule,
				CompensationSummary:   job.CompensationSummary,
				Accommodation:         job.Accommodation,
				TransportRequirements: job.TransportRequirements,
				RequiredDocuments:     job.RequiredDocuments,
				Owner:                 job.Owner,
				Status:                job.Status,
				Version:               job.Version,
				CreatedAt:             job.CreatedAt,
				UpdatedAt:             job.UpdatedAt,
			}
		}
	}
	if state.ServiceRequests != nil {
		legacy.ServiceRequests = make([]crmServiceRequestV2, len(state.ServiceRequests))
		for i, request := range state.ServiceRequests {
			legacy.ServiceRequests[i] = crmServiceRequestV2{
				ID:               request.ID,
				CandidateID:      request.CandidateID,
				JobID:            request.JobID,
				OnboardingCaseID: request.OnboardingCaseID,
				SourceEventID:    request.SourceEventID,
				Category:         request.Category,
				Priority:         request.Priority,
				Description:      request.Description,
				Owner:            request.Owner,
				Status:           request.Status,
				RequiresHuman:    request.RequiresHuman,
				DueAt:            request.DueAt,
				Resolution:       request.Resolution,
				Version:          request.Version,
				CreatedAt:        request.CreatedAt,
				UpdatedAt:        request.UpdatedAt,
				ResolvedAt:       request.ResolvedAt,
			}
		}
	}
	return legacy
}

func validateCRMStateV1Slices(state *CRMState) error {
	switch {
	case state.Candidates == nil:
		return NewCRMValidationError("candidates", "must not be null")
	case state.Jobs == nil:
		return NewCRMValidationError("jobs", "must not be null")
	case state.Leads == nil:
		return NewCRMValidationError("leads", "must not be null")
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

func crmJobHasV2Data(job Job) bool {
	return job.DemandCode != "" ||
		job.SiteName != "" ||
		job.OnsiteContact != "" ||
		job.Headcount != 0 ||
		job.Priority != "" ||
		job.StartAt != nil ||
		job.DeadlineAt != nil ||
		job.Shift != "" ||
		job.WorkSchedule != "" ||
		job.CompensationSummary != "" ||
		job.Accommodation != "" ||
		job.TransportRequirements != "" ||
		len(job.RequiredDocuments) != 0 ||
		job.ActiveDemandRevisionID != "" ||
		job.DemandRevisionNumber != 0
}

func normalizeCRMStateV2Defaults(state *CRMState) {
	if state == nil || state.SchemaVersion != CRMSchemaVersion {
		return
	}
	if state.RecruitingProject.OrganizationName == "" &&
		state.RecruitingProject.Version == 0 &&
		state.RecruitingProject.SourceGroups == nil {
		state.RecruitingProject = RecruitingProjectConfig{
			OrganizationName:        CRMDefaultOrganizationName,
			Version:                 1,
			RequireAuthoritativeJob: true,
			SourceGroups:            []RecruitingSourceGroup{},
		}
	}
	for i := range state.Jobs {
		if state.Jobs[i].Priority == "" {
			state.Jobs[i].Priority = JobPriorityNormal
		}
		if state.Jobs[i].RequiredDocuments == nil {
			state.Jobs[i].RequiredDocuments = []string{}
		}
	}
	for i := range state.MessageEvents {
		if state.MessageEvents[i].Disposition == "" {
			state.MessageEvents[i].Disposition = defaultMessageDisposition(state.MessageEvents[i].ChatType)
		}
	}
}

func validateCRMDecodedV2Shape(state *crmStateV2) error {
	switch {
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
	}
	for i := range state.Jobs {
		field := indexedField("jobs", i)
		if state.Jobs[i].Priority == "" {
			return NewCRMValidationError(field+".priority", "is required")
		}
		if state.Jobs[i].RequiredDocuments == nil {
			return NewCRMValidationError(field+".required_documents", "must not be null")
		}
	}
	return nil
}

func validateCRMDecodedV3Shape(legacy *crmStateV3) error {
	if legacy == nil {
		return NewCRMValidationError("state", "is required")
	}
	state := crmStateFromV3(*legacy)
	if err := validateNonNilStateSlices(&state); err != nil {
		return err
	}
	for i := range state.Jobs {
		field := indexedField("jobs", i)
		if state.Jobs[i].Priority == "" {
			return NewCRMValidationError(field+".priority", "is required")
		}
		if state.Jobs[i].RequiredDocuments == nil {
			return NewCRMValidationError(field+".required_documents", "must not be null")
		}
	}
	return nil
}

func validateCRMDecodedV4Shape(state *CRMState) error {
	if err := validateNonNilStateSlices(state); err != nil {
		return err
	}
	if state.RecruitingProject.SourceGroups == nil {
		return NewCRMValidationError("recruiting_project.source_groups", "must not be null")
	}
	for i := range state.Jobs {
		field := indexedField("jobs", i)
		if state.Jobs[i].Priority == "" {
			return NewCRMValidationError(field+".priority", "is required")
		}
		if state.Jobs[i].RequiredDocuments == nil {
			return NewCRMValidationError(field+".required_documents", "must not be null")
		}
	}
	for i := range state.MessageEvents {
		if state.MessageEvents[i].Disposition == "" {
			return NewCRMValidationError(indexedField("message_events", i)+".disposition", "is required")
		}
	}
	return nil
}

func decodeCRMJSON(data []byte, destination any) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return NewCRMValidationError("snapshot", "is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return NewCRMValidationError("snapshot", "contains invalid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return NewCRMValidationError("snapshot", "contains trailing JSON")
		}
		return NewCRMValidationError("snapshot", "contains invalid trailing data")
	}
	return nil
}
