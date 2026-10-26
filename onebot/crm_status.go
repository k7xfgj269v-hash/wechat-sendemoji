package main

func LeadStages() []LeadStage {
	stages := make([]LeadStage, len(leadStageOrder))
	copy(stages, leadStageOrder[:])
	return stages
}

func IsValidCandidateStatus(status CandidateStatus) bool {
	switch status {
	case CandidateStatusActive, CandidateStatusPaused, CandidateStatusPlaced, CandidateStatusArchived:
		return true
	default:
		return false
	}
}

func CanTransitionCandidateStatus(from, to CandidateStatus) bool {
	switch from {
	case CandidateStatusActive:
		return to == CandidateStatusPaused || to == CandidateStatusPlaced || to == CandidateStatusArchived
	case CandidateStatusPaused:
		return to == CandidateStatusActive || to == CandidateStatusPlaced || to == CandidateStatusArchived
	case CandidateStatusPlaced:
		return to == CandidateStatusArchived
	default:
		return false
	}
}

func ValidateCandidateStatusTransition(from, to CandidateStatus) error {
	if !IsValidCandidateStatus(from) {
		return NewCRMValidationError("from_status", "invalid candidate status")
	}
	if !IsValidCandidateStatus(to) {
		return NewCRMValidationError("status", "invalid candidate status")
	}
	if !CanTransitionCandidateStatus(from, to) {
		return NewCRMConflictError("candidate status transition is not allowed")
	}
	return nil
}

func IsValidJobStatus(status JobStatus) bool {
	switch status {
	case JobStatusOpen, JobStatusClosed, JobStatusArchived:
		return true
	default:
		return false
	}
}

func IsValidJobPriority(priority JobPriority) bool {
	switch priority {
	case JobPriorityLow, JobPriorityNormal, JobPriorityHigh, JobPriorityUrgent:
		return true
	default:
		return false
	}
}

func IsValidOnboardingStatus(status OnboardingStatus) bool {
	switch status {
	case OnboardingStatusPending,
		OnboardingStatusCollecting,
		OnboardingStatusReady,
		OnboardingStatusTransportScheduled,
		OnboardingStatusEnRoute,
		OnboardingStatusArrived,
		OnboardingStatusCheckedIn,
		OnboardingStatusCompleted,
		OnboardingStatusBlocked,
		OnboardingStatusCancelled:
		return true
	default:
		return false
	}
}

func IsValidFleetStatus(status FleetStatus) bool {
	switch status {
	case FleetStatusNotRequired,
		FleetStatusPending,
		FleetStatusRequested,
		FleetStatusConfirmed,
		FleetStatusDispatched,
		FleetStatusPickedUp,
		FleetStatusDelivered,
		FleetStatusCancelled:
		return true
	default:
		return false
	}
}

func IsValidServiceRequestCategory(category ServiceRequestCategory) bool {
	switch category {
	case ServiceRequestCategoryGeneral,
		ServiceRequestCategoryTransport,
		ServiceRequestCategoryAccommodation,
		ServiceRequestCategoryEquipment,
		ServiceRequestCategorySchedule,
		ServiceRequestCategoryDocument,
		ServiceRequestCategorySafety,
		ServiceRequestCategoryDispute,
		ServiceRequestCategoryFund,
		ServiceRequestCategoryOther:
		return true
	default:
		return false
	}
}

func IsValidServiceRequestStatus(status ServiceRequestStatus) bool {
	switch status {
	case ServiceRequestStatusOpen,
		ServiceRequestStatusTriaged,
		ServiceRequestStatusAssigned,
		ServiceRequestStatusInProgress,
		ServiceRequestStatusAwaitingRequester,
		ServiceRequestStatusResolved,
		ServiceRequestStatusClosed,
		ServiceRequestStatusEscalated,
		ServiceRequestStatusCancelled:
		return true
	default:
		return false
	}
}

func IsValidFundCallbackStatus(status FundCallbackStatus) bool {
	switch status {
	case FundCallbackStatusDraft,
		FundCallbackStatusAwaitingCommitment,
		FundCallbackStatusCommitted,
		FundCallbackStatusCallbackDue,
		FundCallbackStatusAwaitingFinal,
		FundCallbackStatusCompleted,
		FundCallbackStatusOverdue,
		FundCallbackStatusDisputed,
		FundCallbackStatusCancelled:
		return true
	default:
		return false
	}
}

func IsValidOperationBatchKind(kind OperationBatchKind) bool {
	switch kind {
	case OperationBatchKindOnboardingTransition, OperationBatchKindFleetTransition:
		return true
	default:
		return false
	}
}

func IsValidOperationBatchStatus(status OperationBatchStatus) bool {
	switch status {
	case OperationBatchStatusDraft, OperationBatchStatusCompleted:
		return true
	default:
		return false
	}
}

func IsValidOperationBatchOutcome(outcome OperationBatchOutcome) bool {
	switch outcome {
	case OperationBatchOutcomeSucceeded, OperationBatchOutcomeFailed:
		return true
	default:
		return false
	}
}

func CanTransitionJobStatus(from, to JobStatus) bool {
	switch from {
	case JobStatusOpen:
		return to == JobStatusClosed || to == JobStatusArchived
	case JobStatusClosed:
		return to == JobStatusOpen || to == JobStatusArchived
	default:
		return false
	}
}

func ValidateJobStatusTransition(from, to JobStatus) error {
	if !IsValidJobStatus(from) {
		return NewCRMValidationError("from_status", "invalid job status")
	}
	if !IsValidJobStatus(to) {
		return NewCRMValidationError("status", "invalid job status")
	}
	if !CanTransitionJobStatus(from, to) {
		return NewCRMConflictError("job status transition is not allowed")
	}
	return nil
}

func IsValidLeadStage(stage LeadStage) bool {
	for _, candidate := range leadStageOrder {
		if stage == candidate {
			return true
		}
	}
	return false
}

func IsTerminalLeadStage(stage LeadStage) bool {
	return stage == LeadStageHired || stage == LeadStageRejected || stage == LeadStageWithdrawn
}

func CanTransitionLeadStage(from, to LeadStage) bool {
	switch from {
	case LeadStageSourced:
		return to == LeadStageScreening || to == LeadStageRejected || to == LeadStageWithdrawn
	case LeadStageScreening:
		return to == LeadStageInterview || to == LeadStageRejected || to == LeadStageWithdrawn
	case LeadStageInterview:
		return to == LeadStageOffer || to == LeadStageRejected || to == LeadStageWithdrawn
	case LeadStageOffer:
		return to == LeadStageHired || to == LeadStageRejected || to == LeadStageWithdrawn
	default:
		return false
	}
}

func ValidateLeadStageTransition(from, to LeadStage) error {
	if !IsValidLeadStage(from) {
		return NewCRMValidationError("from_stage", "invalid lead stage")
	}
	if !IsValidLeadStage(to) {
		return NewCRMValidationError("to_stage", "invalid lead stage")
	}
	if !CanTransitionLeadStage(from, to) {
		return NewCRMConflictError("lead stage transition is not allowed")
	}
	return nil
}

func IsValidFollowUpStatus(status FollowUpStatus) bool {
	return status == FollowUpStatusOpen || status == FollowUpStatusCompleted
}

func CanTransitionFollowUpStatus(from, to FollowUpStatus) bool {
	return IsValidFollowUpStatus(from) && IsValidFollowUpStatus(to) && from != to
}

func ValidateFollowUpStatusTransition(from, to FollowUpStatus) error {
	if !IsValidFollowUpStatus(from) {
		return NewCRMValidationError("from_status", "invalid follow-up status")
	}
	if !IsValidFollowUpStatus(to) {
		return NewCRMValidationError("status", "invalid follow-up status")
	}
	if !CanTransitionFollowUpStatus(from, to) {
		return NewCRMConflictError("follow-up status transition is not allowed")
	}
	return nil
}

func IsValidReviewStatus(status ReviewStatus) bool {
	return status == ReviewStatusUnreviewed || status == ReviewStatusReviewed
}

func IsValidChatType(chatType ChatType) bool {
	return chatType == ChatTypePrivate || chatType == ChatTypeGroup
}

func IsValidMessageDisposition(disposition MessageDisposition) bool {
	switch disposition {
	case MessageDispositionPrivateCandidate,
		MessageDispositionSourceGroupJob,
		MessageDispositionGroupNoise:
		return true
	default:
		return false
	}
}

func IsValidJobIngestionDecision(decision JobIngestionDecision) bool {
	switch decision {
	case JobIngestionCreate,
		JobIngestionUpdate,
		JobIngestionDuplicate,
		JobIngestionNoop,
		JobIngestionNoise,
		JobIngestionFailure:
		return true
	default:
		return false
	}
}

func IsValidNoteTargetType(targetType NoteTargetType) bool {
	return targetType == NoteTargetCandidate || targetType == NoteTargetLead
}
