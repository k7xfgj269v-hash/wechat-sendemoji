package main

import (
	"errors"
)

const (
	CRMSchemaVersion                  = 4
	CRMDefaultOrganizationName        = "国爱人力"
	DefaultRecruitingOrganizationName = CRMDefaultOrganizationName
	CRMDefaultListLimit               = 50
	CRMMaxListLimit                   = 200
	CRMMaxSearchRunes                 = 200
	CRMMaxRequestBodyBytes            = 1 << 20
)

var (
	ErrCRMValidation  = errors.New("crm validation error")
	ErrCRMConflict    = errors.New("crm conflict")
	ErrCRMStoreClosed = errors.New("crm store closed")
	ErrCRMPersistence = errors.New("crm persistence error")
)

type CRMValidationError struct {
	Field   string
	Message string
}

func (e *CRMValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func (e *CRMValidationError) Unwrap() error {
	return ErrCRMValidation
}

type ValidationError = CRMValidationError

type CRMConflictError struct {
	Message string
}

func (e *CRMConflictError) Error() string {
	return e.Message
}

func (e *CRMConflictError) Unwrap() error {
	return ErrCRMConflict
}

type ConflictError = CRMConflictError

type CRMPersistenceError struct {
	Operation string
	Err       error
}

func (e *CRMPersistenceError) Error() string {
	if e.Operation == "" {
		return ErrCRMPersistence.Error()
	}
	return "crm persistence failed during " + e.Operation
}

func (e *CRMPersistenceError) Unwrap() error {
	return e.Err
}

func (e *CRMPersistenceError) Is(target error) bool {
	return target == ErrCRMPersistence || errors.Is(e.Err, target)
}

type PersistenceError = CRMPersistenceError

func NewCRMValidationError(field, message string) error {
	return &CRMValidationError{Field: field, Message: message}
}

func NewCRMConflictError(message string) error {
	return &CRMConflictError{Message: message}
}

func WrapCRMPersistence(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &CRMPersistenceError{Operation: operation, Err: err}
}

type CandidateStatus string

const (
	CandidateStatusActive   CandidateStatus = "active"
	CandidateStatusPaused   CandidateStatus = "paused"
	CandidateStatusPlaced   CandidateStatus = "placed"
	CandidateStatusArchived CandidateStatus = "archived"
)

type JobStatus string

const (
	JobStatusOpen     JobStatus = "open"
	JobStatusClosed   JobStatus = "closed"
	JobStatusArchived JobStatus = "archived"
)

type JobPriority string

const (
	JobPriorityLow    JobPriority = "low"
	JobPriorityNormal JobPriority = "normal"
	JobPriorityHigh   JobPriority = "high"
	JobPriorityUrgent JobPriority = "urgent"
)

type LeadStage string

const (
	LeadStageSourced   LeadStage = "sourced"
	LeadStageScreening LeadStage = "screening"
	LeadStageInterview LeadStage = "interview"
	LeadStageOffer     LeadStage = "offer"
	LeadStageHired     LeadStage = "hired"
	LeadStageRejected  LeadStage = "rejected"
	LeadStageWithdrawn LeadStage = "withdrawn"
)

var leadStageOrder = [...]LeadStage{
	LeadStageSourced,
	LeadStageScreening,
	LeadStageInterview,
	LeadStageOffer,
	LeadStageHired,
	LeadStageRejected,
	LeadStageWithdrawn,
}

type OnboardingStatus string

const (
	OnboardingStatusPending            OnboardingStatus = "pending"
	OnboardingStatusCollecting         OnboardingStatus = "collecting"
	OnboardingStatusReady              OnboardingStatus = "ready"
	OnboardingStatusTransportScheduled OnboardingStatus = "transport_scheduled"
	OnboardingStatusEnRoute            OnboardingStatus = "en_route"
	OnboardingStatusArrived            OnboardingStatus = "arrived"
	OnboardingStatusCheckedIn          OnboardingStatus = "checked_in"
	OnboardingStatusCompleted          OnboardingStatus = "completed"
	OnboardingStatusBlocked            OnboardingStatus = "blocked"
	OnboardingStatusCancelled          OnboardingStatus = "cancelled"
)

type FleetStatus string

const (
	FleetStatusNotRequired FleetStatus = "not_required"
	FleetStatusPending     FleetStatus = "pending"
	FleetStatusRequested   FleetStatus = "requested"
	FleetStatusConfirmed   FleetStatus = "confirmed"
	FleetStatusDispatched  FleetStatus = "dispatched"
	FleetStatusPickedUp    FleetStatus = "picked_up"
	FleetStatusDelivered   FleetStatus = "delivered"
	FleetStatusCancelled   FleetStatus = "cancelled"
)

type ServiceRequestCategory string

const (
	ServiceRequestCategoryGeneral       ServiceRequestCategory = "general"
	ServiceRequestCategoryTransport     ServiceRequestCategory = "transport"
	ServiceRequestCategoryAccommodation ServiceRequestCategory = "accommodation"
	ServiceRequestCategoryEquipment     ServiceRequestCategory = "equipment"
	ServiceRequestCategorySchedule      ServiceRequestCategory = "schedule"
	ServiceRequestCategoryDocument      ServiceRequestCategory = "document"
	ServiceRequestCategorySafety        ServiceRequestCategory = "safety"
	ServiceRequestCategoryDispute       ServiceRequestCategory = "dispute"
	ServiceRequestCategoryFund          ServiceRequestCategory = "fund"
	ServiceRequestCategoryOther         ServiceRequestCategory = "other"
)

type ServiceRequestStatus string

const (
	ServiceRequestStatusOpen              ServiceRequestStatus = "open"
	ServiceRequestStatusTriaged           ServiceRequestStatus = "triaged"
	ServiceRequestStatusAssigned          ServiceRequestStatus = "assigned"
	ServiceRequestStatusInProgress        ServiceRequestStatus = "in_progress"
	ServiceRequestStatusAwaitingRequester ServiceRequestStatus = "awaiting_requester"
	ServiceRequestStatusResolved          ServiceRequestStatus = "resolved"
	ServiceRequestStatusClosed            ServiceRequestStatus = "closed"
	ServiceRequestStatusEscalated         ServiceRequestStatus = "escalated"
	ServiceRequestStatusCancelled         ServiceRequestStatus = "cancelled"
)

type FundCallbackStatus string

const (
	FundCallbackStatusDraft              FundCallbackStatus = "draft"
	FundCallbackStatusAwaitingCommitment FundCallbackStatus = "awaiting_commitment"
	FundCallbackStatusCommitted          FundCallbackStatus = "committed"
	FundCallbackStatusCallbackDue        FundCallbackStatus = "callback_due"
	FundCallbackStatusAwaitingFinal      FundCallbackStatus = "awaiting_final"
	FundCallbackStatusCompleted          FundCallbackStatus = "completed"
	FundCallbackStatusOverdue            FundCallbackStatus = "overdue"
	FundCallbackStatusDisputed           FundCallbackStatus = "disputed"
	FundCallbackStatusCancelled          FundCallbackStatus = "cancelled"
)

type OperationBatchKind string

const (
	OperationBatchKindOnboardingTransition OperationBatchKind = "onboarding_transition"
	OperationBatchKindFleetTransition      OperationBatchKind = "fleet_transition"
)

type OperationBatchStatus string

const (
	OperationBatchStatusDraft     OperationBatchStatus = "draft"
	OperationBatchStatusCompleted OperationBatchStatus = "completed"
)

type OperationBatchOutcome string

const (
	OperationBatchOutcomeSucceeded OperationBatchOutcome = "succeeded"
	OperationBatchOutcomeFailed    OperationBatchOutcome = "failed"
)

type FollowUpStatus string

const (
	FollowUpStatusOpen      FollowUpStatus = "open"
	FollowUpStatusCompleted FollowUpStatus = "completed"
)

type ReviewStatus string

const (
	ReviewStatusUnreviewed ReviewStatus = "unreviewed"
	ReviewStatusReviewed   ReviewStatus = "reviewed"
)

type ChatType string

const (
	ChatTypePrivate ChatType = "private"
	ChatTypeGroup   ChatType = "group"
)

type MessageDisposition string

const (
	MessageDispositionPrivateCandidate MessageDisposition = "private_candidate"
	MessageDispositionSourceGroupJob   MessageDisposition = "source_group_job"
	MessageDispositionGroupNoise       MessageDisposition = "group_noise"
)

type JobIngestionDecision string

const (
	JobIngestionCreate    JobIngestionDecision = "create"
	JobIngestionUpdate    JobIngestionDecision = "update"
	JobIngestionDuplicate JobIngestionDecision = "duplicate"
	JobIngestionNoop      JobIngestionDecision = "noop"
	JobIngestionNoise     JobIngestionDecision = "noise"
	JobIngestionFailure   JobIngestionDecision = "failure"
)

type NoteTargetType string

const (
	NoteTargetCandidate NoteTargetType = "candidate"
	NoteTargetLead      NoteTargetType = "lead"
)

type IDKind string

const (
	IDKindCandidate      IDKind = "candidate"
	IDKindJob            IDKind = "job"
	IDKindLead           IDKind = "lead"
	IDKindTask           IDKind = "task"
	IDKindNote           IDKind = "note"
	IDKindEvent          IDKind = "event"
	IDKindRule           IDKind = "rule"
	IDKindStageEvent     IDKind = "stage_event"
	IDKindOnboarding     IDKind = "onboarding"
	IDKindServiceRequest IDKind = "service_request"
	IDKindFundCallback   IDKind = "fund_callback"
	IDKindDemandRevision IDKind = "demand_revision"
	IDKindOperationBatch IDKind = "operation_batch"
	IDKindServiceQueue   IDKind = "service_queue"
	IDKindFundLedger     IDKind = "fund_ledger"
	IDKindAudit          IDKind = "audit"
	IDKindSourceGroup    IDKind = "recruiting_source_group"
	IDKindJobProvenance  IDKind = "job_provenance"
)

const IDKindRecruitingSourceGroup = IDKindSourceGroup
