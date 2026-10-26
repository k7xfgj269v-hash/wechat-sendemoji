package main

import (
	"time"
)

func validateOperationBatch(
	field string,
	batch *OperationBatch,
	onboardingCases map[string]OnboardingCase,
) error {
	if !IsValidOperationBatchKind(batch.Kind) {
		return NewCRMValidationError(field+".kind", "is invalid")
	}
	if err := validateStoredCRMString(field+".target_status", batch.TargetStatus, 80, true, false); err != nil {
		return err
	}
	switch batch.Kind {
	case OperationBatchKindOnboardingTransition:
		if !IsValidOnboardingStatus(OnboardingStatus(batch.TargetStatus)) {
			return NewCRMValidationError(field+".target_status", "is invalid for onboarding transitions")
		}
	case OperationBatchKindFleetTransition:
		if !IsValidFleetStatus(FleetStatus(batch.TargetStatus)) {
			return NewCRMValidationError(field+".target_status", "is invalid for fleet transitions")
		}
	}
	if !IsValidOperationBatchStatus(batch.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if err := validateCRMVersion(field+".version", batch.Version); err != nil {
		return err
	}
	if batch.Members == nil {
		return NewCRMValidationError(field+".members", "must not be null")
	}
	if batch.Results == nil {
		return NewCRMValidationError(field+".results", "must not be null")
	}
	if len(batch.Members) > crmMaxEntityCount || len(batch.Results) > crmMaxEntityCount {
		return NewCRMValidationError(field, "member or result limit exceeded")
	}
	members := make(map[string]struct{}, len(batch.Members))
	for i := range batch.Members {
		member := &batch.Members[i]
		memberField := indexedField(field+".members", i)
		if err := validateStoredCRMString(memberField+".onboarding_case_id", member.OnboardingCaseID, 80, true, false); err != nil {
			return err
		}
		if err := validateCRMVersion(memberField+".expected_version", member.ExpectedVersion); err != nil {
			return err
		}
		if _, ok := onboardingCases[member.OnboardingCaseID]; !ok {
			return NewCRMValidationError(memberField+".onboarding_case_id", "references a missing onboarding case")
		}
		if _, exists := members[member.OnboardingCaseID]; exists {
			return NewCRMValidationError(memberField+".onboarding_case_id", "duplicates a batch member")
		}
		members[member.OnboardingCaseID] = struct{}{}
	}
	results := make(map[string]struct{}, len(batch.Results))
	for i := range batch.Results {
		result := &batch.Results[i]
		resultField := indexedField(field+".results", i)
		if err := validateStoredCRMString(resultField+".onboarding_case_id", result.OnboardingCaseID, 80, true, false); err != nil {
			return err
		}
		if _, ok := members[result.OnboardingCaseID]; !ok {
			return NewCRMValidationError(resultField+".onboarding_case_id", "is not a batch member")
		}
		if _, exists := results[result.OnboardingCaseID]; exists {
			return NewCRMValidationError(resultField+".onboarding_case_id", "duplicates a batch result")
		}
		results[result.OnboardingCaseID] = struct{}{}
		if !IsValidOperationBatchOutcome(result.Outcome) {
			return NewCRMValidationError(resultField+".outcome", "is invalid")
		}
		if err := validateStoredCRMString(resultField+".from_status", result.FromStatus, 80, true, false); err != nil {
			return err
		}
		if err := validateStoredCRMString(resultField+".to_status", result.ToStatus, 80, true, false); err != nil {
			return err
		}
		if result.ToStatus != batch.TargetStatus {
			return NewCRMValidationError(resultField+".to_status", "does not match the batch target status")
		}
		if err := validateStoredCRMString(resultField+".error_code", result.ErrorCode, 120, false, false); err != nil {
			return err
		}
		if err := validateStoredCRMString(resultField+".error_message", result.ErrorMessage, 2000, false, true); err != nil {
			return err
		}
		if result.Outcome == OperationBatchOutcomeSucceeded && (result.ErrorCode != "" || result.ErrorMessage != "") {
			return NewCRMValidationError(resultField, "successful results cannot contain errors")
		}
		if result.Outcome == OperationBatchOutcomeFailed && result.ErrorCode == "" {
			return NewCRMValidationError(resultField+".error_code", "is required for failed results")
		}
		if err := validateCRMTime(resultField+".created_at", result.CreatedAt); err != nil {
			return err
		}
	}
	if batch.Status == OperationBatchStatusDraft && len(batch.Results) != 0 {
		return NewCRMValidationError(field+".results", "must be empty for draft batches")
	}
	if batch.Status == OperationBatchStatusCompleted && len(batch.Results) != len(batch.Members) {
		return NewCRMValidationError(field+".results", "must contain one result per member for completed batches")
	}
	if err := validateStoredCRMString(field+".created_by", batch.CreatedBy, 80, true, false); err != nil {
		return err
	}
	return validateVersionedTimes(field, batch.Version, batch.CreatedAt, batch.UpdatedAt)
}

func validateServiceQueue(field string, queue *ServiceQueue) error {
	if err := validateStoredCRMString(field+".name", queue.Name, 160, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".owner", queue.Owner, 80, false, false); err != nil {
		return err
	}
	if queue.Categories == nil {
		return NewCRMValidationError(field+".categories", "must not be null")
	}
	if len(queue.Categories) > 32 {
		return NewCRMValidationError(field+".categories", "contains too many categories")
	}
	seen := make(map[ServiceRequestCategory]struct{}, len(queue.Categories))
	for i, category := range queue.Categories {
		if !IsValidServiceRequestCategory(category) {
			return NewCRMValidationError(indexedField(field+".categories", i), "is invalid")
		}
		if _, exists := seen[category]; exists {
			return NewCRMValidationError(indexedField(field+".categories", i), "is duplicated")
		}
		seen[category] = struct{}{}
	}
	if queue.SLASeconds < 0 || queue.SLASeconds > int64((365*24*time.Hour)/time.Second) {
		return NewCRMValidationError(field+".sla_seconds", "must be between 0 and 31536000")
	}
	return validateVersionedTimes(field, queue.Version, queue.CreatedAt, queue.UpdatedAt)
}

func validateServiceRequest(field string, request *ServiceRequest) error {
	if err := validateStoredCRMString(field+".candidate_id", request.CandidateID, 80, true, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".job_id", request.JobID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".onboarding_case_id", request.OnboardingCaseID, 80, false, false); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".source_event_id", request.SourceEventID, 80, false, false); err != nil {
		return err
	}
	if !IsValidServiceRequestCategory(request.Category) {
		return NewCRMValidationError(field+".category", "is invalid")
	}
	if !IsValidJobPriority(request.Priority) {
		return NewCRMValidationError(field+".priority", "is invalid")
	}
	if err := validateStoredCRMString(field+".description", request.Description, 4000, true, true); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".owner", request.Owner, 80, false, false); err != nil {
		return err
	}
	if !IsValidServiceRequestStatus(request.Status) {
		return NewCRMValidationError(field+".status", "is invalid")
	}
	if request.Category == ServiceRequestCategorySafety ||
		request.Category == ServiceRequestCategoryDispute ||
		request.Category == ServiceRequestCategoryFund {
		if !request.RequiresHuman {
			return NewCRMValidationError(field+".requires_human", "must be true for safety, dispute, and fund requests")
		}
	}
	if err := validateStoredCRMString(field+".queue_id", request.QueueID, 80, false, false); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".enqueued_at", request.EnqueuedAt); err != nil {
		return err
	}
	if request.QueueID == "" {
		if request.EnqueuedAt != nil || request.ClaimedBy != "" || request.ClaimedAt != nil {
			return NewCRMValidationError(field+".queue_id", "is required for queue and claim metadata")
		}
	} else if request.EnqueuedAt == nil {
		return NewCRMValidationError(field+".enqueued_at", "is required when queue_id is set")
	}
	if err := validateStoredCRMString(field+".claimed_by", request.ClaimedBy, 80, false, false); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".claimed_at", request.ClaimedAt); err != nil {
		return err
	}
	if (request.ClaimedBy == "") != (request.ClaimedAt == nil) {
		return NewCRMValidationError(field+".claimed_at", "must be present together with claimed_by")
	}
	if err := validateCRMOptionalTime(field+".due_at", request.DueAt); err != nil {
		return err
	}
	if err := validateStoredCRMString(field+".resolution", request.Resolution, 4000, false, true); err != nil {
		return err
	}
	if err := validateVersionedTimes(field, request.Version, request.CreatedAt, request.UpdatedAt); err != nil {
		return err
	}
	if err := validateCRMOptionalTime(field+".resolved_at", request.ResolvedAt); err != nil {
		return err
	}
	if request.ResolvedAt != nil {
		if request.ResolvedAt.Before(request.CreatedAt) || request.ResolvedAt.After(request.UpdatedAt) {
			return NewCRMValidationError(field+".resolved_at", "must be between created_at and updated_at")
		}
	}
	for _, value := range []struct {
		field string
		time  *time.Time
	}{
		{field + ".enqueued_at", request.EnqueuedAt},
		{field + ".claimed_at", request.ClaimedAt},
	} {
		if value.time != nil && (value.time.Before(request.CreatedAt) || value.time.After(request.UpdatedAt)) {
			return NewCRMValidationError(value.field, "must be between created_at and updated_at")
		}
	}
	if request.EnqueuedAt != nil && request.ClaimedAt != nil && request.ClaimedAt.Before(*request.EnqueuedAt) {
		return NewCRMValidationError(field+".claimed_at", "must not precede enqueued_at")
	}
	if (request.Status == ServiceRequestStatusResolved || request.Status == ServiceRequestStatusClosed) &&
		request.ResolvedAt == nil {
		return NewCRMValidationError(field+".resolved_at", "is required for resolved or closed requests")
	}
	return nil
}
