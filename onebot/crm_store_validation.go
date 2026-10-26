package main

import (
	"fmt"
	"reflect"
	"strconv"
)

func ValidateCRMState(state *CRMState) error {
	return validateCRMState(state, false)
}

func validateCRMStateStrict(state *CRMState) error {
	return validateCRMState(state, true)
}

func validateCRMState(state *CRMState, requireDemandRevisions bool) error {
	if state == nil {
		return NewCRMValidationError("state", "is required")
	}
	if state.SchemaVersion != CRMSchemaVersion {
		return NewCRMValidationError("schema_version", fmt.Sprintf("unsupported schema version %d", state.SchemaVersion))
	}
	if err := validateNonNilStateSlices(state); err != nil {
		return err
	}
	if len(state.Candidates) > crmMaxEntityCount {
		return NewCRMValidationError("candidates", "entity limit exceeded")
	}
	if len(state.Jobs) > crmMaxEntityCount {
		return NewCRMValidationError("jobs", "entity limit exceeded")
	}
	if len(state.Leads) > crmMaxEntityCount {
		return NewCRMValidationError("leads", "entity limit exceeded")
	}
	if len(state.LeadStageEvents) > crmMaxEntityCount {
		return NewCRMValidationError("lead_stage_events", "entity limit exceeded")
	}
	if len(state.OnboardingCases) > crmMaxEntityCount {
		return NewCRMValidationError("onboarding_cases", "entity limit exceeded")
	}
	if len(state.ServiceRequests) > crmMaxEntityCount {
		return NewCRMValidationError("service_requests", "entity limit exceeded")
	}
	if len(state.FundCallbacks) > crmMaxEntityCount {
		return NewCRMValidationError("fund_callbacks", "entity limit exceeded")
	}
	if len(state.DemandRevisions) > crmMaxEntityCount {
		return NewCRMValidationError("demand_revisions", "entity limit exceeded")
	}
	if len(state.JobProvenance) > crmMaxEntityCount {
		return NewCRMValidationError("job_provenance", "entity limit exceeded")
	}
	if len(state.OperationBatches) > crmMaxEntityCount {
		return NewCRMValidationError("operation_batches", "entity limit exceeded")
	}
	if len(state.ServiceQueues) > crmMaxEntityCount {
		return NewCRMValidationError("service_queues", "entity limit exceeded")
	}
	if len(state.FundCallbackLedger) > crmMaxNoteCount {
		return NewCRMValidationError("fund_callback_ledger", "entity limit exceeded")
	}
	if len(state.FundCallbackApprovedActions) > crmMaxEntityCount {
		return NewCRMValidationError("fund_callback_approved_actions", "entity limit exceeded")
	}
	if len(state.AuditEvents) > crmMaxNoteCount {
		return NewCRMValidationError("audit_events", "entity limit exceeded")
	}
	if len(state.Tasks) > crmMaxEntityCount {
		return NewCRMValidationError("tasks", "entity limit exceeded")
	}
	if len(state.Notes) > crmMaxNoteCount {
		return NewCRMValidationError("notes", "entity limit exceeded")
	}
	if len(state.MessageEvents) > crmMaxEntityCount {
		return NewCRMValidationError("message_events", "entity limit exceeded")
	}
	if len(state.ForwardingRules) > crmMaxRuleCount {
		return NewCRMValidationError("forwarding_rules", "entity limit exceeded")
	}
	if err := validateCounterBounds(state.Counters); err != nil {
		return err
	}

	allIDs := make(map[string]string, totalCRMEntities(state))
	candidates := make(map[string]Candidate, len(state.Candidates))
	jobs := make(map[string]Job, len(state.Jobs))
	demandRevisions := make(map[string]DemandRevision, len(state.DemandRevisions))
	demandRevisionNumbers := make(map[string]string, len(state.DemandRevisions))
	leads := make(map[string]Lead, len(state.Leads))
	onboardingCases := make(map[string]OnboardingCase, len(state.OnboardingCases))
	serviceQueues := make(map[string]ServiceQueue, len(state.ServiceQueues))
	fundCallbacks := make(map[string]FundCallback, len(state.FundCallbacks))
	events := make(map[string]MessageEvent, len(state.MessageEvents))
	messageKeys := make(map[string]string, len(state.MessageEvents))
	activePairs := make(map[string]string)
	onboardingLeads := make(map[string]string, len(state.OnboardingCases))

	if err := validateRecruitingProjectConfig(&state.RecruitingProject, state.Counters.SourceGroup, allIDs); err != nil {
		return err
	}
	for i := range state.Candidates {
		candidate := &state.Candidates[i]
		field := indexedField("candidates", i)
		if err := validateEntityID(field+".id", candidate.ID, "cand", state.Counters.Candidate, allIDs); err != nil {
			return err
		}
		if err := validateCandidate(field, candidate); err != nil {
			return err
		}
		candidates[candidate.ID] = *candidate
	}
	for i := range state.Jobs {
		job := &state.Jobs[i]
		field := indexedField("jobs", i)
		if err := validateEntityID(field+".id", job.ID, "job", state.Counters.Job, allIDs); err != nil {
			return err
		}
		if err := validateJob(field, job); err != nil {
			return err
		}
		jobs[job.ID] = *job
	}
	for i := range state.DemandRevisions {
		revision := &state.DemandRevisions[i]
		field := indexedField("demand_revisions", i)
		if err := validateEntityID(field+".id", revision.ID, "demand", state.Counters.DemandRevision, allIDs); err != nil {
			return err
		}
		if err := validateDemandRevision(field, revision); err != nil {
			return err
		}
		job, ok := jobs[revision.JobID]
		if !ok {
			return NewCRMValidationError(field+".job_id", "references a missing job")
		}
		if revision.CreatedAt.Before(job.CreatedAt) || revision.CreatedAt.After(job.UpdatedAt) {
			return NewCRMValidationError(field+".created_at", "must be between the job created_at and updated_at")
		}
		numberKey := revision.JobID + "\x00" + strconv.FormatUint(revision.Number, 10)
		if previous, exists := demandRevisionNumbers[numberKey]; exists {
			return NewCRMValidationError(field+".number", "duplicates demand revision "+previous)
		}
		demandRevisionNumbers[numberKey] = revision.ID
		demandRevisions[revision.ID] = *revision
	}
	for i := range state.Jobs {
		job := &state.Jobs[i]
		field := indexedField("jobs", i)
		if job.ActiveDemandRevisionID == "" {
			if requireDemandRevisions || job.DemandRevisionNumber != 0 || len(state.DemandRevisions) != 0 {
				return NewCRMValidationError(field+".active_demand_revision_id", "is required")
			}
			continue
		}
		revision, ok := demandRevisions[job.ActiveDemandRevisionID]
		if !ok {
			return NewCRMValidationError(field+".active_demand_revision_id", "references a missing demand revision")
		}
		if revision.JobID != job.ID {
			return NewCRMValidationError(field+".active_demand_revision_id", "references another job")
		}
		if job.DemandRevisionNumber != revision.Number {
			return NewCRMValidationError(field+".demand_revision_number", "does not match the active demand revision")
		}
		if !reflect.DeepEqual(jobDemandSnapshot(*job), revision.Snapshot) {
			return NewCRMValidationError(field, "does not match the active demand revision snapshot")
		}
	}
	for i := range state.Leads {
		lead := &state.Leads[i]
		field := indexedField("leads", i)
		if err := validateEntityID(field+".id", lead.ID, "lead", state.Counters.Lead, allIDs); err != nil {
			return err
		}
		if err := validateLead(field, lead); err != nil {
			return err
		}
		if _, ok := candidates[lead.CandidateID]; !ok {
			return NewCRMValidationError(field+".candidate_id", "references a missing candidate")
		}
		if _, ok := jobs[lead.JobID]; !ok {
			return NewCRMValidationError(field+".job_id", "references a missing job")
		}
		if !IsTerminalLeadStage(lead.Stage) {
			pair := lead.CandidateID + "\x00" + lead.JobID
			if previous, exists := activePairs[pair]; exists {
				return NewCRMValidationError(field, "duplicates active candidate/job lead "+previous)
			}
			activePairs[pair] = lead.ID
		}
		leads[lead.ID] = *lead
	}
	for i := range state.LeadStageEvents {
		stageEvent := &state.LeadStageEvents[i]
		field := indexedField("lead_stage_events", i)
		if err := validateEntityID(field+".id", stageEvent.ID, "stage", state.Counters.StageEvent, allIDs); err != nil {
			return err
		}
		if err := validateLeadStageEvent(field, stageEvent); err != nil {
			return err
		}
		lead, ok := leads[stageEvent.LeadID]
		if !ok {
			return NewCRMValidationError(field+".lead_id", "references a missing lead")
		}
		if stageEvent.CreatedAt.Before(lead.CreatedAt) || stageEvent.CreatedAt.After(lead.UpdatedAt) {
			return NewCRMValidationError(field+".created_at", "must be between the lead created_at and updated_at")
		}
	}
	for i := range state.OnboardingCases {
		onboarding := &state.OnboardingCases[i]
		field := indexedField("onboarding_cases", i)
		if err := validateEntityID(field+".id", onboarding.ID, "onboard", state.Counters.Onboarding, allIDs); err != nil {
			return err
		}
		if err := validateOnboardingCase(field, onboarding); err != nil {
			return err
		}
		lead, ok := leads[onboarding.LeadID]
		if !ok {
			return NewCRMValidationError(field+".lead_id", "references a missing lead")
		}
		if _, ok := candidates[onboarding.CandidateID]; !ok {
			return NewCRMValidationError(field+".candidate_id", "references a missing candidate")
		}
		if _, ok := jobs[onboarding.JobID]; !ok {
			return NewCRMValidationError(field+".job_id", "references a missing job")
		}
		if onboarding.CandidateID != lead.CandidateID {
			return NewCRMValidationError(field+".candidate_id", "does not match the lead candidate")
		}
		if onboarding.JobID != lead.JobID {
			return NewCRMValidationError(field+".job_id", "does not match the lead job")
		}
		if previous, exists := onboardingLeads[onboarding.LeadID]; exists {
			return NewCRMValidationError(field+".lead_id", "duplicates onboarding case "+previous)
		}
		onboardingLeads[onboarding.LeadID] = onboarding.ID
		onboardingCases[onboarding.ID] = *onboarding
	}
	for i := range state.OperationBatches {
		batch := &state.OperationBatches[i]
		field := indexedField("operation_batches", i)
		if err := validateEntityID(field+".id", batch.ID, "batch", state.Counters.OperationBatch, allIDs); err != nil {
			return err
		}
		if err := validateOperationBatch(field, batch, onboardingCases); err != nil {
			return err
		}
	}
	for i := range state.ServiceQueues {
		queue := &state.ServiceQueues[i]
		field := indexedField("service_queues", i)
		if err := validateEntityID(field+".id", queue.ID, "queue", state.Counters.ServiceQueue, allIDs); err != nil {
			return err
		}
		if err := validateServiceQueue(field, queue); err != nil {
			return err
		}
		serviceQueues[queue.ID] = *queue
	}
	for i := range state.ServiceRequests {
		request := &state.ServiceRequests[i]
		field := indexedField("service_requests", i)
		if err := validateEntityID(field+".id", request.ID, "service", state.Counters.ServiceRequest, allIDs); err != nil {
			return err
		}
		if err := validateServiceRequest(field, request); err != nil {
			return err
		}
		if _, ok := candidates[request.CandidateID]; !ok {
			return NewCRMValidationError(field+".candidate_id", "references a missing candidate")
		}
		if request.JobID != "" {
			if _, ok := jobs[request.JobID]; !ok {
				return NewCRMValidationError(field+".job_id", "references a missing job")
			}
		}
		if request.OnboardingCaseID != "" {
			onboarding, ok := onboardingCases[request.OnboardingCaseID]
			if !ok {
				return NewCRMValidationError(field+".onboarding_case_id", "references a missing onboarding case")
			}
			if request.CandidateID != onboarding.CandidateID {
				return NewCRMValidationError(field+".candidate_id", "does not match the onboarding candidate")
			}
			if request.JobID != "" && request.JobID != onboarding.JobID {
				return NewCRMValidationError(field+".job_id", "does not match the onboarding job")
			}
		}
		if request.QueueID != "" {
			if _, ok := serviceQueues[request.QueueID]; !ok {
				return NewCRMValidationError(field+".queue_id", "references a missing service queue")
			}
		}
	}
	for i := range state.FundCallbacks {
		callback := &state.FundCallbacks[i]
		field := indexedField("fund_callbacks", i)
		if err := validateEntityID(field+".id", callback.ID, "fund", state.Counters.FundCallback, allIDs); err != nil {
			return err
		}
		if err := validateFundCallback(field, callback); err != nil {
			return err
		}
		if _, ok := candidates[callback.CandidateID]; !ok {
			return NewCRMValidationError(field+".candidate_id", "references a missing candidate")
		}
		if callback.JobID != "" {
			if _, ok := jobs[callback.JobID]; !ok {
				return NewCRMValidationError(field+".job_id", "references a missing job")
			}
		}
		if callback.LeadID != "" {
			lead, ok := leads[callback.LeadID]
			if !ok {
				return NewCRMValidationError(field+".lead_id", "references a missing lead")
			}
			if callback.CandidateID != lead.CandidateID {
				return NewCRMValidationError(field+".candidate_id", "does not match the lead candidate")
			}
			if callback.JobID != "" && callback.JobID != lead.JobID {
				return NewCRMValidationError(field+".job_id", "does not match the lead job")
			}
		}
		if callback.OnboardingCaseID != "" {
			onboarding, ok := onboardingCases[callback.OnboardingCaseID]
			if !ok {
				return NewCRMValidationError(field+".onboarding_case_id", "references a missing onboarding case")
			}
			if callback.CandidateID != onboarding.CandidateID {
				return NewCRMValidationError(field+".candidate_id", "does not match the onboarding candidate")
			}
			if callback.JobID != "" && callback.JobID != onboarding.JobID {
				return NewCRMValidationError(field+".job_id", "does not match the onboarding job")
			}
		}
		fundCallbacks[callback.ID] = *callback
	}
	approvedBindingsByFundCallbackID := make(
		map[string]*FundCallbackApprovedActionBinding,
		len(state.FundCallbackApprovedActions),
	)
	approvedBindingFields := make(map[string]string, len(state.FundCallbackApprovedActions))
	for i := range state.FundCallbackApprovedActions {
		binding := &state.FundCallbackApprovedActions[i]
		field := indexedField("fund_callback_approved_actions", i)
		if err := validateFundCallbackApprovedActionBinding(field, binding); err != nil {
			return err
		}
		if _, ok := fundCallbacks[binding.FundCallbackID]; !ok {
			return NewCRMValidationError(field+".fund_callback_id", "references a missing fund callback")
		}
		if previous, exists := approvedBindingFields[binding.FundCallbackID]; exists {
			return NewCRMValidationError(
				field+".fund_callback_id",
				"duplicates approved fund callback binding "+previous,
			)
		}
		approvedBindingsByFundCallbackID[binding.FundCallbackID] = binding
		approvedBindingFields[binding.FundCallbackID] = field
	}
	fundCallbackCreateLedger := make(map[string]FundCallbackLedgerEntry)
	for i := range state.FundCallbackLedger {
		entry := &state.FundCallbackLedger[i]
		field := indexedField("fund_callback_ledger", i)
		if err := validateEntityID(field+".id", entry.ID, "fundledger", state.Counters.FundLedger, allIDs); err != nil {
			return err
		}
		if entry.Type == "create" && entry.CandidateID == "" {
			if binding, ok := approvedBindingsByFundCallbackID[entry.FundCallbackID]; ok {
				entry.CandidateID = binding.OriginalResult.CandidateID
			}
		}
		if err := validateFundCallbackLedgerEntry(field, entry); err != nil {
			return err
		}
		callback, ok := fundCallbacks[entry.FundCallbackID]
		if !ok {
			return NewCRMValidationError(field+".fund_callback_id", "references a missing fund callback")
		}
		if entry.CreatedAt.Before(callback.CreatedAt) || entry.CreatedAt.After(callback.UpdatedAt) {
			return NewCRMValidationError(field+".created_at", "must be between the fund callback created_at and updated_at")
		}
		if entry.Type == "create" {
			if _, exists := fundCallbackCreateLedger[entry.FundCallbackID]; exists {
				return NewCRMValidationError(field+".type", "duplicates fund callback create ledger entry")
			}
			fundCallbackCreateLedger[entry.FundCallbackID] = *entry
		}
	}
	approvedActionIDs := make(map[string]string, len(state.FundCallbackApprovedActions))
	approvedPermissionRequestIDs := make(map[string]string, len(state.FundCallbackApprovedActions))
	approvedFundCallbackIDs := make(map[string]string, len(state.FundCallbackApprovedActions))
	for i := range state.FundCallbackApprovedActions {
		binding := &state.FundCallbackApprovedActions[i]
		field := indexedField("fund_callback_approved_actions", i)
		if err := validateFundCallbackApprovedActionBinding(field, binding); err != nil {
			return err
		}
		callback, ok := fundCallbacks[binding.FundCallbackID]
		if !ok {
			return NewCRMValidationError(field+".fund_callback_id", "references a missing fund callback")
		}
		if binding.OriginalResult != nil {
			result := binding.OriginalResult
			createLedger, ok := fundCallbackCreateLedger[binding.FundCallbackID]
			if !ok {
				return NewCRMValidationError(
					field+".original_result",
					"requires an immutable create ledger entry",
				)
			}
			if result.AmountMinor != createLedger.AmountMinor ||
				result.CandidateID != createLedger.CandidateID ||
				result.Currency != createLedger.Currency ||
				result.Status != createLedger.ToStatus ||
				!result.UpdatedAt.Equal(createLedger.CreatedAt) ||
				!result.UpdatedAt.Equal(callback.CreatedAt) {
				return NewCRMValidationError(
					field+".original_result",
					"does not match the immutable fund callback creation record",
				)
			}
		}
		if previous, exists := approvedActionIDs[binding.ActionID]; exists {
			return NewCRMValidationError(field+".action_id", "duplicates approved action binding "+previous)
		}
		if previous, exists := approvedPermissionRequestIDs[binding.PermissionRequestID]; exists {
			return NewCRMValidationError(
				field+".permission_request_id",
				"duplicates approved permission request binding "+previous,
			)
		}
		if previous, exists := approvedFundCallbackIDs[binding.FundCallbackID]; exists {
			return NewCRMValidationError(
				field+".fund_callback_id",
				"duplicates approved fund callback binding "+previous,
			)
		}
		approvedActionIDs[binding.ActionID] = binding.FundCallbackID
		approvedPermissionRequestIDs[binding.PermissionRequestID] = binding.FundCallbackID
		approvedFundCallbackIDs[binding.FundCallbackID] = binding.ActionID
	}
	for i := range state.Tasks {
		task := &state.Tasks[i]
		field := indexedField("tasks", i)
		if err := validateEntityID(field+".id", task.ID, "task", state.Counters.Task, allIDs); err != nil {
			return err
		}
		if err := validateTask(field, task); err != nil {
			return err
		}
		if task.CandidateID != "" {
			if _, ok := candidates[task.CandidateID]; !ok {
				return NewCRMValidationError(field+".candidate_id", "references a missing candidate")
			}
		}
		if task.LeadID != "" {
			lead, ok := leads[task.LeadID]
			if !ok {
				return NewCRMValidationError(field+".lead_id", "references a missing lead")
			}
			if task.CandidateID != "" && task.CandidateID != lead.CandidateID {
				return NewCRMValidationError(field+".candidate_id", "does not match the lead candidate")
			}
		}
	}
	for i := range state.Notes {
		note := &state.Notes[i]
		field := indexedField("notes", i)
		if err := validateEntityID(field+".id", note.ID, "note", state.Counters.Note, allIDs); err != nil {
			return err
		}
		if err := validateNote(field, note); err != nil {
			return err
		}
		switch note.TargetType {
		case NoteTargetCandidate:
			if _, ok := candidates[note.TargetID]; !ok {
				return NewCRMValidationError(field+".target_id", "references a missing candidate")
			}
		case NoteTargetLead:
			if _, ok := leads[note.TargetID]; !ok {
				return NewCRMValidationError(field+".target_id", "references a missing lead")
			}
		}
	}
	for i := range state.MessageEvents {
		event := &state.MessageEvents[i]
		field := indexedField("message_events", i)
		if err := validateEntityID(field+".id", event.ID, "event", state.Counters.Event, allIDs); err != nil {
			return err
		}
		if err := validateMessageEvent(field, event); err != nil {
			return err
		}
		if previous, exists := messageKeys[event.MessageKey]; exists {
			return NewCRMValidationError(field+".message_key", "duplicates message event "+previous)
		}
		messageKeys[event.MessageKey] = event.ID
		if event.CandidateID != "" {
			if _, ok := candidates[event.CandidateID]; !ok {
				return NewCRMValidationError(field+".candidate_id", "references a missing candidate")
			}
		}
		events[event.ID] = *event
	}
	provenanceEvents := make(map[string]string, len(state.JobProvenance))
	provenanceMessages := make(map[string]string, len(state.JobProvenance))
	for i := range state.JobProvenance {
		provenance := &state.JobProvenance[i]
		field := indexedField("job_provenance", i)
		if err := validateEntityID(field+".id", provenance.ID, "provenance", state.Counters.JobProvenance, allIDs); err != nil {
			return err
		}
		if err := validateJobProvenance(field, provenance); err != nil {
			return err
		}
		event, ok := events[provenance.EventID]
		if !ok {
			return NewCRMValidationError(field+".event_id", "references a missing message event")
		}
		if event.ChatType != ChatTypeGroup ||
			event.MessageKey != provenance.MessageKey ||
			event.GroupID != provenance.GroupID ||
			event.AuthorID != provenance.AuthorID {
			return NewCRMValidationError(field, "does not match the source message event")
		}
		if previous, exists := provenanceEvents[provenance.EventID]; exists {
			return NewCRMValidationError(field+".event_id", "duplicates job provenance "+previous)
		}
		if previous, exists := provenanceMessages[provenance.MessageKey]; exists {
			return NewCRMValidationError(field+".message_key", "duplicates job provenance "+previous)
		}
		provenanceEvents[provenance.EventID] = provenance.ID
		provenanceMessages[provenance.MessageKey] = provenance.ID
		if provenance.JobID == "" {
			if provenance.JobRevision != 0 ||
				(provenance.Decision != JobIngestionNoise && provenance.Decision != JobIngestionFailure) {
				return NewCRMValidationError(field+".job_id", "is required for this decision")
			}
			continue
		}
		if _, ok := jobs[provenance.JobID]; !ok {
			return NewCRMValidationError(field+".job_id", "references a missing job")
		}
		revisionKey := provenance.JobID + "\x00" + strconv.FormatUint(provenance.JobRevision, 10)
		if _, ok := demandRevisionNumbers[revisionKey]; !ok {
			return NewCRMValidationError(field+".job_revision", "references a missing demand revision")
		}
	}
	for i := range state.ForwardingRules {
		rule := &state.ForwardingRules[i]
		field := indexedField("forwarding_rules", i)
		if err := validateEntityID(field+".id", rule.ID, "rule", state.Counters.Rule, allIDs); err != nil {
			return err
		}
		if err := validateForwardRule(field, rule); err != nil {
			return err
		}
	}
	for i := range state.AuditEvents {
		auditEvent := &state.AuditEvents[i]
		field := indexedField("audit_events", i)
		if err := validateEntityID(field+".id", auditEvent.ID, "audit", state.Counters.Audit, allIDs); err != nil {
			return err
		}
		if err := validateCRMAuditEvent(field, auditEvent); err != nil {
			return err
		}
		if auditEvent.EntityID != "" {
			entityField, ok := allIDs[auditEvent.EntityID]
			if !ok {
				return NewCRMValidationError(field+".entity_id", "references a missing entity")
			}
			if !crmAuditEntityFieldMatches(auditEvent.EntityType, entityField) {
				return NewCRMValidationError(field+".entity_type", "does not match entity_id")
			}
		}
		if auditEvent.SourceEventID != "" {
			if _, ok := events[auditEvent.SourceEventID]; !ok {
				return NewCRMValidationError(field+".source_event_id", "references a missing message event")
			}
		}
	}
	for i := range state.Candidates {
		candidate := &state.Candidates[i]
		if candidate.SourceEventID == "" {
			continue
		}
		field := indexedField("candidates", i) + ".source_event_id"
		event, ok := events[candidate.SourceEventID]
		if !ok {
			return NewCRMValidationError(field, "references a missing message event")
		}
		if event.CandidateID != candidate.ID {
			return NewCRMValidationError(field, "does not agree with the message candidate_id")
		}
	}
	for i := range state.ServiceRequests {
		if state.ServiceRequests[i].SourceEventID == "" {
			continue
		}
		field := indexedField("service_requests", i) + ".source_event_id"
		if _, ok := events[state.ServiceRequests[i].SourceEventID]; !ok {
			return NewCRMValidationError(field, "references a missing message event")
		}
	}
	return nil
}
