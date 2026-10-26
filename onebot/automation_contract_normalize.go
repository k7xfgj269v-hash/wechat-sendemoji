package main

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

func normalizeAutomationEvidence(field string, evidence AutomationEvidence) (AutomationEvidence, error) {
	var err error
	if evidence.Source, err = normalizeAutomationString(field+".source", evidence.Source, 120, true, false); err != nil {
		return AutomationEvidence{}, err
	}
	if evidence.EntityID, err = normalizeAutomationString(field+".entity_id", evidence.EntityID, 80, false, false); err != nil {
		return AutomationEvidence{}, err
	}
	if evidence.Field, err = normalizeAutomationString(field+".field", evidence.Field, 120, false, false); err != nil {
		return AutomationEvidence{}, err
	}
	if evidence.Excerpt, err = normalizeAutomationString(field+".excerpt", evidence.Excerpt, 500, true, true); err != nil {
		return AutomationEvidence{}, err
	}
	return evidence, nil
}

func normalizeMessageClassifyArgs(field string, arguments MessageClassifyArgs) (MessageClassifyArgs, error) {
	if arguments.Labels == nil {
		return MessageClassifyArgs{}, NewAutomationValidationError(field+".labels", "must not be null")
	}
	labels, err := normalizeAutomationStringList(field+".labels", arguments.Labels, 5, 64)
	if err != nil {
		return MessageClassifyArgs{}, err
	}
	for i, label := range labels {
		if !IsValidAutomationClassificationLabel(label) {
			return MessageClassifyArgs{}, NewAutomationValidationError(indexedField(field+".labels", i), "unsupported classification label")
		}
	}
	summary, err := normalizeAutomationString(field+".summary", arguments.Summary, 4000, true, true)
	if err != nil {
		return MessageClassifyArgs{}, err
	}
	arguments.Labels = labels
	arguments.Summary = summary
	return arguments, nil
}

func normalizeCandidateCreateArgs(field string, arguments CandidateCreateArgs) (CandidateCreateArgs, error) {
	var err error
	if arguments.FullName, err = normalizeAutomationString(field+".full_name", arguments.FullName, 120, true, false); err != nil {
		return CandidateCreateArgs{}, err
	}
	if arguments.Phone, err = normalizeAutomationString(field+".phone", arguments.Phone, 40, false, false); err != nil {
		return CandidateCreateArgs{}, err
	}
	if arguments.Email, err = normalizeAutomationString(field+".email", arguments.Email, 254, false, false); err != nil {
		return CandidateCreateArgs{}, err
	}
	if arguments.Location, err = normalizeAutomationString(field+".location", arguments.Location, 160, false, false); err != nil {
		return CandidateCreateArgs{}, err
	}
	if arguments.Skills == nil {
		return CandidateCreateArgs{}, NewAutomationValidationError(field+".skills", "must not be null")
	}
	if arguments.Skills, err = normalizeAutomationStringList(field+".skills", arguments.Skills, 32, 64); err != nil {
		return CandidateCreateArgs{}, err
	}
	if arguments.Owner, err = normalizeAutomationString(field+".owner", arguments.Owner, 80, false, false); err != nil {
		return CandidateCreateArgs{}, err
	}
	if arguments.Summary, err = normalizeAutomationString(field+".summary", arguments.Summary, 4000, false, true); err != nil {
		return CandidateCreateArgs{}, err
	}
	return arguments, nil
}

func normalizeCandidateUpdateArgs(field string, arguments CandidateUpdateArgs) (CandidateUpdateArgs, error) {
	candidate, err := normalizeAutomationEntityRef(field+".candidate", arguments.Candidate, AutomationEntityCandidate)
	if err != nil {
		return CandidateUpdateArgs{}, err
	}
	patch, err := normalizeCandidateUpdatePatch(field+".patch", arguments.Patch)
	if err != nil {
		return CandidateUpdateArgs{}, err
	}
	arguments.Candidate = candidate
	arguments.Patch = patch
	return arguments, nil
}

func normalizeCandidateUpdatePatch(field string, patch CandidateUpdatePatch) (CandidateUpdatePatch, error) {
	if patch.FullName == nil &&
		patch.WeChatID == nil &&
		patch.Phone == nil &&
		patch.Email == nil &&
		patch.Location == nil &&
		patch.Source == nil &&
		patch.Skills == nil &&
		patch.Owner == nil &&
		patch.Status == nil &&
		patch.Summary == nil {
		return CandidateUpdatePatch{}, NewAutomationValidationError(field, "must contain at least one update")
	}

	var err error
	if patch.FullName, err = normalizeAutomationStringPointer(field+".full_name", patch.FullName, 120, true, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.WeChatID, err = normalizeAutomationStringPointer(field+".wechat_id", patch.WeChatID, 128, false, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.Phone, err = normalizeAutomationStringPointer(field+".phone", patch.Phone, 40, false, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.Email, err = normalizeAutomationStringPointer(field+".email", patch.Email, 254, false, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.Location, err = normalizeAutomationStringPointer(field+".location", patch.Location, 160, false, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.Source, err = normalizeAutomationStringPointer(field+".source", patch.Source, 120, false, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.Skills != nil {
		normalized, normalizeErr := normalizeAutomationStringList(field+".skills", *patch.Skills, 32, 64)
		if normalizeErr != nil {
			return CandidateUpdatePatch{}, normalizeErr
		}
		patch.Skills = &normalized
	}
	if patch.Owner, err = normalizeAutomationStringPointer(field+".owner", patch.Owner, 80, false, false); err != nil {
		return CandidateUpdatePatch{}, err
	}
	if patch.Status != nil && !IsValidCandidateStatus(*patch.Status) {
		return CandidateUpdatePatch{}, NewAutomationValidationError(field+".status", "invalid candidate status")
	}
	if patch.Summary, err = normalizeAutomationStringPointer(field+".summary", patch.Summary, 4000, false, true); err != nil {
		return CandidateUpdatePatch{}, err
	}
	return patch, nil
}

func normalizeCandidateLinkArgs(field string, arguments CandidateLinkArgs) (CandidateLinkArgs, error) {
	candidate, err := normalizeAutomationEntityRef(field+".candidate", arguments.Candidate, AutomationEntityCandidate)
	if err != nil {
		return CandidateLinkArgs{}, err
	}
	arguments.Candidate = candidate
	return arguments, nil
}

func normalizeJobMatchArgs(field string, arguments JobMatchArgs) (JobMatchArgs, []AutomationToolReference, error) {
	if arguments.Matches == nil {
		return JobMatchArgs{}, nil, NewAutomationValidationError(field+".matches", "must not be null")
	}
	if len(arguments.Matches) > 50 {
		return JobMatchArgs{}, nil, NewAutomationValidationError(field+".matches", "must contain at most 50 matches")
	}

	references := make([]AutomationToolReference, 0, len(arguments.Matches))
	seen := make(map[string]struct{}, len(arguments.Matches))
	for i := range arguments.Matches {
		matchField := indexedField(field+".matches", i)
		job, err := normalizeAutomationEntityRef(matchField+".job", arguments.Matches[i].Job, AutomationEntityJob)
		if err != nil {
			return JobMatchArgs{}, nil, err
		}
		key := job.ID
		if key == "" {
			key = "from_call:" + job.FromCall
		}
		if _, exists := seen[key]; exists {
			return JobMatchArgs{}, nil, NewAutomationValidationError(matchField+".job", "duplicates an earlier job")
		}
		seen[key] = struct{}{}
		if math.IsNaN(arguments.Matches[i].Score) ||
			math.IsInf(arguments.Matches[i].Score, 0) ||
			arguments.Matches[i].Score < 0 ||
			arguments.Matches[i].Score > 1 {
			return JobMatchArgs{}, nil, NewAutomationValidationError(matchField+".score", "must be finite and between 0 and 1")
		}
		rationale, err := normalizeAutomationString(matchField+".rationale", arguments.Matches[i].Rationale, 4000, true, true)
		if err != nil {
			return JobMatchArgs{}, nil, err
		}
		arguments.Matches[i].Job = job
		arguments.Matches[i].Rationale = rationale
		references = append(references, AutomationToolReference{
			Field:      "matches[" + strconv.Itoa(i) + "].job",
			EntityType: AutomationEntityJob,
			Reference:  job,
		})
	}
	return arguments, references, nil
}

func normalizeJobCreateArgs(field string, arguments JobCreateArgs) (JobCreateArgs, error) {
	var err error
	if arguments.EventID, err = normalizeAutomationString(field+".event_id", arguments.EventID, 80, true, false); err != nil {
		return JobCreateArgs{}, err
	}
	if !automationForwardResourceID(arguments.EventID, "event") {
		return JobCreateArgs{}, NewAutomationValidationError(field+".event_id", "is invalid")
	}
	if arguments.GroupID, err = normalizeAutomationString(field+".group_id", arguments.GroupID, 256, true, false); err != nil {
		return JobCreateArgs{}, err
	}
	if !strings.HasSuffix(arguments.GroupID, "@chatroom") {
		return JobCreateArgs{}, NewAutomationValidationError(
			field+".group_id",
			"must be a WeChat group identifier",
		)
	}
	normalized, err := sourceJobNormalizeDraft(arguments.Job)
	if err != nil {
		var validationError *CRMValidationError
		if errors.As(err, &validationError) {
			return JobCreateArgs{}, NewAutomationValidationError(
				field+".job."+validationError.Field,
				validationError.Message,
			)
		}
		return JobCreateArgs{}, err
	}
	if err := validateAutomationJobCreateContent(field+".job", normalized); err != nil {
		return JobCreateArgs{}, err
	}
	arguments.Job = normalized
	return arguments, nil
}

func validateAutomationJobCreateContent(field string, job JobDraft) error {
	values := []struct {
		field string
		value string
	}{
		{"title", job.Title},
		{"company", job.Company},
		{"location", job.Location},
		{"employment_type", job.EmploymentType},
		{"description", job.Description},
		{"demand_code", job.DemandCode},
		{"site_name", job.SiteName},
		{"onsite_contact", job.OnsiteContact},
		{"shift", job.Shift},
		{"work_schedule", job.WorkSchedule},
		{"compensation_summary", job.CompensationSummary},
		{"accommodation", job.Accommodation},
		{"transport_requirements", job.TransportRequirements},
		{"owner", job.Owner},
	}
	for _, item := range values {
		if isForbiddenAutomationJobContent(item.value) {
			return NewAutomationValidationError(field+"."+item.field, "contains forbidden automation content")
		}
	}
	for i, value := range job.Requirements {
		if isForbiddenAutomationJobContent(value) {
			return NewAutomationValidationError(
				indexedField(field+".requirements", i),
				"contains forbidden automation content",
			)
		}
	}
	for i, value := range job.RequiredDocuments {
		if isForbiddenAutomationJobContent(value) {
			return NewAutomationValidationError(
				indexedField(field+".required_documents", i),
				"contains forbidden automation content",
			)
		}
	}
	return nil
}

func isForbiddenAutomationJobContent(value string) bool {
	return automationForbiddenURLPattern.MatchString(value) ||
		automationForbiddenPathPattern.MatchString(value) ||
		automationForbiddenShellPattern.MatchString(value) ||
		automationForbiddenSQLPattern.MatchString(value) ||
		automationForbiddenCredentialPattern.MatchString(value) ||
		automationForbiddenCredentialValuePattern.MatchString(value) ||
		automationForbiddenPaymentPattern.MatchString(value) ||
		automationForbiddenPaymentValuePattern.MatchString(value)
}

func normalizeLeadCreateArgs(field string, arguments LeadCreateArgs) (LeadCreateArgs, error) {
	candidate, err := normalizeAutomationEntityRef(field+".candidate", arguments.Candidate, AutomationEntityCandidate)
	if err != nil {
		return LeadCreateArgs{}, err
	}
	job, err := normalizeAutomationEntityRef(field+".job", arguments.Job, AutomationEntityJob)
	if err != nil {
		return LeadCreateArgs{}, err
	}
	owner, err := normalizeAutomationString(field+".owner", arguments.Owner, 80, false, false)
	if err != nil {
		return LeadCreateArgs{}, err
	}
	arguments.Candidate = candidate
	arguments.Job = job
	arguments.Owner = owner
	return arguments, nil
}

func normalizeLeadTransitionArgs(field string, arguments LeadTransitionArgs) (LeadTransitionArgs, error) {
	lead, err := normalizeAutomationEntityRef(field+".lead", arguments.Lead, AutomationEntityLead)
	if err != nil {
		return LeadTransitionArgs{}, err
	}
	if !IsValidLeadStage(arguments.FromStage) {
		return LeadTransitionArgs{}, NewAutomationValidationError(field+".from_stage", "invalid lead stage")
	}
	if !IsValidLeadStage(arguments.ToStage) {
		return LeadTransitionArgs{}, NewAutomationValidationError(field+".to_stage", "invalid lead stage")
	}
	if !CanTransitionLeadStage(arguments.FromStage, arguments.ToStage) {
		return LeadTransitionArgs{}, NewAutomationValidationError(field+".to_stage", "lead stage transition is not allowed")
	}
	arguments.Lead = lead
	return arguments, nil
}

func normalizeTaskCreateArgs(field string, arguments TaskCreateArgs) (TaskCreateArgs, error) {
	var err error
	if arguments.Title, err = normalizeAutomationString(field+".title", arguments.Title, 200, true, false); err != nil {
		return TaskCreateArgs{}, err
	}
	if arguments.Description, err = normalizeAutomationString(field+".description", arguments.Description, 4000, false, true); err != nil {
		return TaskCreateArgs{}, err
	}
	if arguments.Owner, err = normalizeAutomationString(field+".owner", arguments.Owner, 80, false, false); err != nil {
		return TaskCreateArgs{}, err
	}
	if err := validateAutomationTime(field+".due_at", arguments.DueAt); err != nil {
		return TaskCreateArgs{}, err
	}
	switch arguments.TargetType {
	case AutomationEntityCandidate, AutomationEntityLead:
	default:
		return TaskCreateArgs{}, NewAutomationValidationError(field+".target_type", "must be candidate or lead")
	}
	target, err := normalizeAutomationEntityRef(field+".target", arguments.Target, arguments.TargetType)
	if err != nil {
		return TaskCreateArgs{}, err
	}
	arguments.DueAt = arguments.DueAt.UTC()
	arguments.Target = target
	return arguments, nil
}

func normalizeNoteCreateArgs(field string, arguments NoteCreateArgs) (NoteCreateArgs, error) {
	if !IsValidNoteTargetType(arguments.TargetType) {
		return NoteCreateArgs{}, NewAutomationValidationError(field+".target_type", "invalid note target type")
	}
	target, err := normalizeAutomationEntityRef(field+".target", arguments.Target, string(arguments.TargetType))
	if err != nil {
		return NoteCreateArgs{}, err
	}
	author, err := normalizeAutomationString(field+".author", arguments.Author, 80, false, false)
	if err != nil {
		return NoteCreateArgs{}, err
	}
	body, err := normalizeAutomationString(field+".body", arguments.Body, 4000, true, true)
	if err != nil {
		return NoteCreateArgs{}, err
	}
	arguments.Target = target
	arguments.Author = author
	arguments.Body = body
	return arguments, nil
}

func normalizeReplyPrivateTextArgs(field string, arguments ReplyPrivateTextArgs) (ReplyPrivateTextArgs, error) {
	intent, err := normalizeAutomationString(field+".intent", arguments.Intent, 80, true, false)
	if err != nil {
		return ReplyPrivateTextArgs{}, err
	}
	if !IsValidAutomationReplyIntent(intent) {
		return ReplyPrivateTextArgs{}, NewAutomationValidationError(field+".intent", "unsupported private reply intent")
	}
	text, err := normalizeAutomationString(field+".text", arguments.Text, 4000, true, true)
	if err != nil {
		return ReplyPrivateTextArgs{}, err
	}
	arguments.Intent = intent
	arguments.Text = text
	return arguments, nil
}

func normalizeJobUpdateArgs(field string, arguments JobUpdateArgs) (JobUpdateArgs, error) {
	job, err := normalizeAutomationEntityRef(field+".job", arguments.Job, AutomationEntityJob)
	if err != nil {
		return JobUpdateArgs{}, err
	}
	update, err := crmPipelineNormalizeJobUpdate(arguments.Patch.jobUpdate(1))
	if err != nil {
		return JobUpdateArgs{}, err
	}
	arguments.Job = job
	arguments.Patch = automationJobUpdatePatch(update)
	return arguments, nil
}

func normalizeOnboardingCreateArgs(field string, arguments OnboardingCreateArgs) (OnboardingCreateArgs, error) {
	lead, err := normalizeAutomationEntityRef(field+".lead", arguments.Lead, AutomationEntityLead)
	if err != nil {
		return OnboardingCreateArgs{}, err
	}
	create, err := crmNormalizeOnboardingCreate(OnboardingCreate{
		LeadID:             automationReferenceID(lead, "lead_00000001"),
		EntryAt:            arguments.EntryAt,
		EntryAddress:       arguments.EntryAddress,
		SiteContact:        arguments.SiteContact,
		RequiredDocuments:  arguments.RequiredDocuments,
		CompletedDocuments: arguments.CompletedDocuments,
		MissingInformation: arguments.MissingInformation,
		Actor:              "automation",
	})
	if err != nil {
		return OnboardingCreateArgs{}, err
	}
	arguments.Lead = lead
	arguments.EntryAt = create.EntryAt
	arguments.EntryAddress = create.EntryAddress
	arguments.SiteContact = create.SiteContact
	arguments.RequiredDocuments = create.RequiredDocuments
	arguments.CompletedDocuments = create.CompletedDocuments
	arguments.MissingInformation = create.MissingInformation
	return arguments, nil
}

func normalizeOnboardingUpdateArgs(field string, arguments OnboardingUpdateArgs) (OnboardingUpdateArgs, error) {
	onboarding, err := normalizeAutomationEntityRef(field+".onboarding", arguments.Onboarding, AutomationEntityOnboarding)
	if err != nil {
		return OnboardingUpdateArgs{}, err
	}
	update, err := crmNormalizeOnboardingUpdate(arguments.Patch.onboardingUpdate(1))
	if err != nil {
		return OnboardingUpdateArgs{}, err
	}
	arguments.Onboarding = onboarding
	arguments.Patch = automationOnboardingUpdatePatch(update)
	return arguments, nil
}

func normalizeFleetUpdateArgs(field string, arguments FleetUpdateArgs) (FleetUpdateArgs, error) {
	onboarding, err := normalizeAutomationEntityRef(field+".onboarding", arguments.Onboarding, AutomationEntityOnboarding)
	if err != nil {
		return FleetUpdateArgs{}, err
	}
	update, err := crmNormalizeFleetUpdate(arguments.Patch.fleetUpdate(1))
	if err != nil {
		return FleetUpdateArgs{}, err
	}
	arguments.Onboarding = onboarding
	arguments.Patch = automationFleetUpdatePatch(update)
	return arguments, nil
}
