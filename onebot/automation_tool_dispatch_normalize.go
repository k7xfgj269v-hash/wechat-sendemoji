package main

import (
	"fmt"
	"strings"
)

func normalizeWorkbenchOperationArguments(operation WorkbenchOperation, arguments any) error {
	switch value := arguments.(type) {
	case *WorkbenchEmptyArguments:
		return nil
	case *WorkbenchPageArguments:
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchSearchArguments:
		if err := normalizeWorkbenchPage(&value.Limit, &value.Offset); err != nil {
			return err
		}
		value.Query = strings.TrimSpace(value.Query)
	case *WorkbenchContactSearchArguments:
		if err := normalizeWorkbenchPage(&value.Limit, &value.Offset); err != nil {
			return err
		}
		value.Keyword = strings.TrimSpace(value.Keyword)
	case *WorkbenchContactGetArguments:
		return normalizeWorkbenchRequiredString("arguments.arguments_json.username", &value.Username, 256)
	case *WorkbenchGroupMembersArguments:
		if err := normalizeWorkbenchRequiredString("arguments.arguments_json.group_id", &value.GroupID, 256); err != nil {
			return err
		}
		if !strings.HasSuffix(value.GroupID, "@chatroom") {
			return NewAutomationValidationError("arguments.arguments_json.group_id", "must be a WeChat group identifier")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchIDArguments:
		if err := normalizeWorkbenchRequiredString("arguments.arguments_json.id", &value.ID, 80); err != nil {
			return err
		}
		return validateWorkbenchOperationID(operation, "arguments.arguments_json.id", value.ID)
	case *WorkbenchVersionedIDArguments:
		if err := normalizeWorkbenchRequiredString("arguments.arguments_json.id", &value.ID, 80); err != nil {
			return err
		}
		if value.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json.expected_version", "must be greater than zero")
		}
		return validateWorkbenchOperationID(operation, "arguments.arguments_json.id", value.ID)
	case *WorkbenchMessageListArguments:
		if value.ReviewStatus != "" && !IsValidReviewStatus(value.ReviewStatus) {
			return NewAutomationValidationError("arguments.arguments_json.review_status", "invalid review status")
		}
		if value.CandidateID != "" && !isAutomationEntityID(value.CandidateID, AutomationEntityCandidate) {
			return NewAutomationValidationError("arguments.arguments_json.candidate_id", "invalid candidate id")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchCandidateListArguments:
		if value.Status != "" && !IsValidCandidateStatus(value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid candidate status")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchJobListArguments:
		if value.Status != "" && !IsValidJobStatus(value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid job status")
		}
		if value.Priority != "" && !IsValidJobPriority(value.Priority) {
			return NewAutomationValidationError("arguments.arguments_json.priority", "invalid job priority")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchRevisionListArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) {
			return NewAutomationValidationError("arguments.arguments_json.job_id", "invalid job id")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchRevisionGetArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) || !workbenchResourceID(value.RevisionID, "demand") {
			return NewAutomationValidationError("arguments.arguments_json", "invalid job or revision id")
		}
	case *WorkbenchRevisionCompareArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) ||
			!workbenchResourceID(value.FromRevisionID, "demand") ||
			!workbenchResourceID(value.ToRevisionID, "demand") {
			return NewAutomationValidationError("arguments.arguments_json", "invalid job or revision id")
		}
	case *WorkbenchLeadListArguments:
		if value.CandidateID != "" && !isAutomationEntityID(value.CandidateID, AutomationEntityCandidate) {
			return NewAutomationValidationError("arguments.arguments_json.candidate_id", "invalid candidate id")
		}
		if value.JobID != "" && !isAutomationEntityID(value.JobID, AutomationEntityJob) {
			return NewAutomationValidationError("arguments.arguments_json.job_id", "invalid job id")
		}
		if value.Stage != "" && !IsValidLeadStage(value.Stage) {
			return NewAutomationValidationError("arguments.arguments_json.stage", "invalid lead stage")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchNoteListArguments:
		if !IsValidNoteTargetType(value.TargetType) {
			return NewAutomationValidationError("arguments.arguments_json.target_type", "invalid note target type")
		}
		if !isAutomationEntityID(value.TargetID, string(value.TargetType)) {
			return NewAutomationValidationError("arguments.arguments_json.target_id", "invalid note target id")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchTaskListArguments:
		if value.Status != "" && !IsValidFollowUpStatus(value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid task status")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchOnboardingListArguments:
		if value.Status != "" && !IsValidOnboardingStatus(value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid onboarding status")
		}
		if value.FleetStatus != "" && !IsValidFleetStatus(value.FleetStatus) {
			return NewAutomationValidationError("arguments.arguments_json.fleet_status", "invalid fleet status")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchServiceRequestListArguments:
		if value.Category != "" && !IsValidServiceRequestCategory(value.Category) {
			return NewAutomationValidationError("arguments.arguments_json.category", "invalid service request category")
		}
		if value.Status != "" && !IsValidServiceRequestStatus(value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid service request status")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchFundListArguments:
		if value.Status != "" && !IsValidFundCallbackStatus(value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid fund callback status")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchFundLedgerArguments:
		if err := normalizeWorkbenchRequiredString("arguments.arguments_json.id", &value.ID, 80); err != nil {
			return err
		}
		if err := validateWorkbenchOperationID(
			operation,
			"arguments.arguments_json.id",
			value.ID,
		); err != nil {
			return err
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchAutomationListArguments:
		if value.Tool != "" && !IsValidAutomationToolName(value.Tool) {
			return NewAutomationValidationError("arguments.arguments_json.tool", "invalid automation tool")
		}
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchAuditListArguments:
		return normalizeWorkbenchPage(&value.Limit, &value.Offset)
	case *WorkbenchMessageReviewArguments:
		if !workbenchResourceID(value.MessageID, "event") || value.ExpectedVersion == 0 || !IsValidReviewStatus(value.ReviewStatus) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid message review arguments")
		}
	case *WorkbenchCandidateCreateArguments:
		normalized, err := NormalizeCandidateDraft(value.Candidate)
		if err != nil {
			return asAutomationValidationError(err)
		}
		if normalized.Status == CandidateStatusArchived || normalized.Status == CandidateStatusPlaced {
			return NewAutomationValidationError("arguments.arguments_json.candidate.status", "terminal candidate status requires workbench.allow_once")
		}
		value.Candidate = normalized
	case *WorkbenchCandidateUpdateArguments:
		if !isAutomationEntityID(value.CandidateID, AutomationEntityCandidate) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid candidate id or expected version")
		}
		if value.Update.Status != nil && (*value.Update.Status == CandidateStatusArchived || *value.Update.Status == CandidateStatusPlaced) {
			return NewAutomationValidationError("arguments.arguments_json.update.status", "terminal candidate status requires workbench.allow_once")
		}
	case *WorkbenchCandidateLinkMessageArguments:
		if !workbenchResourceID(value.Request.MessageEventID, "event") || value.Request.ExpectedEventVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json.request", "invalid message linkage precondition")
		}
	case *WorkbenchJobCreateArguments:
		normalized, err := NormalizeJobDraft(value.Job)
		if err != nil {
			return asAutomationValidationError(err)
		}
		if normalized.Status == JobStatusClosed || normalized.Status == JobStatusArchived {
			return NewAutomationValidationError("arguments.arguments_json.job.status", "terminal job status requires workbench.allow_once")
		}
		if normalized.CompensationSummary != "" ||
			normalized.Accommodation != "" ||
			normalized.TransportRequirements != "" {
			return NewAutomationValidationError(
				"arguments.arguments_json.job",
				"compensation and commitment-bearing terms require workbench.allow_once",
			)
		}
		value.Job = normalized
	case *WorkbenchJobUpdateArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid job id or expected version")
		}
		if value.Update.Status != nil && (*value.Update.Status == JobStatusClosed || *value.Update.Status == JobStatusArchived) {
			return NewAutomationValidationError("arguments.arguments_json.update.status", "terminal job status requires workbench.allow_once")
		}
		if value.Update.CompensationSummary != nil ||
			value.Update.Accommodation != nil ||
			value.Update.TransportRequirements != nil {
			return NewAutomationValidationError(
				"arguments.arguments_json.update",
				"compensation and commitment-bearing terms require workbench.allow_once",
			)
		}
	case *WorkbenchLeadCreateArguments:
		if !isAutomationEntityID(value.Lead.CandidateID, AutomationEntityCandidate) ||
			!isAutomationEntityID(value.Lead.JobID, AutomationEntityJob) {
			return NewAutomationValidationError("arguments.arguments_json.lead", "invalid candidate or job id")
		}
	case *WorkbenchLeadUpdateArguments:
		if !isAutomationEntityID(value.LeadID, AutomationEntityLead) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid lead id or expected version")
		}
	case *WorkbenchLeadTransitionArguments:
		if !isAutomationEntityID(value.LeadID, AutomationEntityLead) || value.ExpectedVersion == 0 ||
			!IsValidLeadStage(value.FromStage) || !IsValidLeadStage(value.ToStage) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid lead transition")
		}
		if IsTerminalLeadStage(value.ToStage) {
			return NewAutomationValidationError("arguments.arguments_json.to_stage", "terminal lead transition requires workbench.allow_once")
		}
	case *WorkbenchNoteCreateArguments:
		if !IsValidNoteTargetType(value.TargetType) || !isAutomationEntityID(value.TargetID, string(value.TargetType)) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid note target")
		}
		return normalizeWorkbenchRequiredString("arguments.arguments_json.body", &value.Body, 4000)
	case *WorkbenchTaskCreateArguments:
		if value.Task.CandidateID == "" && value.Task.LeadID == "" {
			return NewAutomationValidationError("arguments.arguments_json.task", "candidate_id or lead_id is required")
		}
	case *WorkbenchTaskUpdateArguments:
		if !isAutomationEntityID(value.TaskID, AutomationEntityTask) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid task id or expected version")
		}
	case *WorkbenchTaskStatusArguments:
		if !isAutomationEntityID(value.TaskID, AutomationEntityTask) || value.Request.ExpectedVersion == 0 ||
			!IsValidFollowUpStatus(value.Request.Status) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid task status request")
		}
	case *WorkbenchOnboardingUpdateArguments:
		if !isAutomationEntityID(value.OnboardingID, AutomationEntityOnboarding) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid onboarding id or expected version")
		}
	case *WorkbenchFleetUpdateArguments:
		if !isAutomationEntityID(value.OnboardingID, AutomationEntityOnboarding) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid onboarding id or expected version")
		}
	case *WorkbenchServiceRequestCreateArguments:
		if !isAutomationEntityID(value.Request.CandidateID, AutomationEntityCandidate) ||
			!IsValidServiceRequestCategory(value.Request.Category) {
			return NewAutomationValidationError("arguments.arguments_json.request", "invalid service request")
		}
	case *WorkbenchServiceRequestUpdateArguments:
		if !isAutomationEntityID(value.RequestID, AutomationEntityServiceRequest) || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid service request id or expected version")
		}
	case *WorkbenchServiceQueueCreateArguments:
		if value.Queue.SLASeconds <= 0 {
			return NewAutomationValidationError("arguments.arguments_json.queue.sla_seconds", "must be greater than zero")
		}
	case *WorkbenchServiceQueueUpdateArguments:
		if !workbenchResourceID(value.QueueID, "queue") || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid service queue id or expected version")
		}
	case *WorkbenchOperationBatchCreateArguments:
		if !IsValidOperationBatchKind(value.Batch.Kind) {
			return NewAutomationValidationError("arguments.arguments_json.batch.kind", "invalid operation batch kind")
		}
	case *WorkbenchOperationBatchUpdateArguments:
		if !workbenchResourceID(value.BatchID, "batch") || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid operation batch id or expected version")
		}
	case *WorkbenchOperationBatchMembersArguments:
		if !workbenchResourceID(value.BatchID, "batch") || value.Update.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid operation batch id or expected version")
		}
	case *WorkbenchRecruitingProjectUpdateArguments:
		if value.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json.expected_version", "must be greater than zero")
		}
	case *WorkbenchRecruitingSourceUpsertArguments:
		if value.ExpectedProjectVersion == 0 || !strings.HasSuffix(strings.TrimSpace(value.Source.GroupID), "@chatroom") {
			return NewAutomationValidationError("arguments.arguments_json", "invalid recruiting source arguments")
		}
	case *WorkbenchForwardRuleCreateArguments:
		if value.CooldownSeconds < 0 || value.CooldownSeconds > 86400 || value.Keywords == nil {
			return NewAutomationValidationError("arguments.arguments_json", "invalid forward rule")
		}
	case *WorkbenchForwardRuleUpdateArguments:
		if !workbenchResourceID(value.RuleID, "rule") ||
			(value.Keywords == nil && value.CooldownSeconds == nil && value.Enabled == nil) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid forward rule update")
		}
	case *WorkbenchControlledSendArguments:
		if !workbenchResourceID(value.EventID, "event") || !IsValidAutomationReplyIntent(value.Intent) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid controlled send arguments")
		}
		return normalizeWorkbenchRequiredString("arguments.arguments_json.text", &value.Text, 4000)
	case *WorkbenchAudienceSendArguments:
		if !automationProviderIdentifier(value.AudienceID) || !IsValidAutomationReplyIntent(value.Intent) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid audience send arguments")
		}
		return normalizeWorkbenchRequiredString("arguments.arguments_json.text", &value.Text, 4000)
	case *WorkbenchForwardEventArguments:
		if !workbenchResourceID(value.EventID, "event") || !workbenchResourceID(value.RuleID, "rule") {
			return NewAutomationValidationError("arguments.arguments_json", "invalid persisted event or rule id")
		}
	case *WorkbenchCandidateTerminalArguments:
		if !isAutomationEntityID(value.CandidateID, AutomationEntityCandidate) || value.ExpectedVersion == 0 ||
			(value.Status != CandidateStatusPlaced && value.Status != CandidateStatusArchived) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid terminal candidate transition")
		}
	case *WorkbenchJobTerminalArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) || value.ExpectedVersion == 0 ||
			(value.Status != JobStatusClosed && value.Status != JobStatusArchived) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid terminal job transition")
		}
	case *WorkbenchJobCompensationArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) || value.ExpectedVersion == 0 ||
			(value.CompensationSummary == nil &&
				value.Accommodation == nil &&
				value.TransportRequirements == nil) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid job compensation update")
		}
	case *WorkbenchLeadTerminalArguments:
		if !isAutomationEntityID(value.LeadID, AutomationEntityLead) || value.ExpectedVersion == 0 ||
			!IsValidLeadStage(value.FromStage) || !IsTerminalLeadStage(value.ToStage) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid terminal lead transition")
		}
	case *WorkbenchRevisionActivateArguments:
		if !isAutomationEntityID(value.JobID, AutomationEntityJob) || !workbenchResourceID(value.RevisionID, "demand") ||
			value.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid revision activation")
		}
	case *WorkbenchOnboardingCreateArguments:
		if !isAutomationEntityID(value.Onboarding.LeadID, AutomationEntityLead) {
			return NewAutomationValidationError("arguments.arguments_json.onboarding.lead_id", "invalid lead id")
		}
	case *WorkbenchOnboardingTransitionArguments:
		if !isAutomationEntityID(value.OnboardingID, AutomationEntityOnboarding) || value.ExpectedVersion == 0 ||
			!IsValidOnboardingStatus(value.ToStatus) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid onboarding transition")
		}
	case *WorkbenchFleetTransitionArguments:
		if !isAutomationEntityID(value.OnboardingID, AutomationEntityOnboarding) || value.ExpectedVersion == 0 ||
			!IsValidFleetStatus(value.ToStatus) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid fleet transition")
		}
	case *WorkbenchServiceRequestTransitionArguments:
		if !isAutomationEntityID(value.RequestID, AutomationEntityServiceRequest) || value.ExpectedVersion == 0 ||
			!IsValidServiceRequestStatus(value.FromStatus) || !IsValidServiceRequestStatus(value.ToStatus) {
			return NewAutomationValidationError("arguments.arguments_json", "invalid service request transition")
		}
	case *WorkbenchFundCreateArguments:
		if !isAutomationEntityID(value.CandidateID, AutomationEntityCandidate) || value.AmountMinor <= 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid fund callback create arguments")
		}
	case *WorkbenchFundUpdateArguments:
		if !isAutomationEntityID(value.FundID, AutomationEntityFundCallback) || value.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid fund callback id or expected version")
		}
		if value.Status != nil && !IsValidFundCallbackStatus(*value.Status) {
			return NewAutomationValidationError("arguments.arguments_json.status", "invalid fund callback status")
		}
	case *WorkbenchFundFinalArguments:
		if !isAutomationEntityID(value.FundID, AutomationEntityFundCallback) || value.ExpectedVersion == 0 {
			return NewAutomationValidationError("arguments.arguments_json", "invalid fund callback id or expected version")
		}
		return normalizeWorkbenchRequiredString("arguments.arguments_json.final_result", &value.FinalResult, 4000)
	case *WorkbenchRecruitingSourceDeleteArguments:
		if value.ExpectedProjectVersion == 0 || !strings.HasSuffix(strings.TrimSpace(value.GroupID), "@chatroom") {
			return NewAutomationValidationError("arguments.arguments_json", "invalid recruiting source deletion")
		}
	default:
		return NewAutomationValidationError("arguments.arguments_json", "unsupported operation argument type")
	}
	return nil
}

func normalizeWorkbenchPage(limit, offset *int) error {
	if *limit == 0 {
		*limit = WorkbenchDefaultPageLimit
	}
	if *limit < 1 || *limit > WorkbenchMaxPageLimit {
		return NewAutomationValidationError(
			"arguments.arguments_json.limit",
			fmt.Sprintf("must be between 1 and %d", WorkbenchMaxPageLimit),
		)
	}
	if *offset < 0 {
		return NewAutomationValidationError("arguments.arguments_json.offset", "must be non-negative")
	}
	return nil
}
