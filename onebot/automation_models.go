package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	AutomationSnapshotSchemaVersion = 3
	AutomationToolEnvelopeVersion   = "automation.actions.v1"
	AutomationProviderInputVersion  = "automation.context.v1"
	AutomationEngineVersion         = "automation-engine.v1"
	AutomationChatCheckpointVersion = "automation.chat.checkpoint.v1"
	AutomationMaxProviderBytes      = 256 << 10
	AutomationMaxActionsPerRun      = 32
	AutomationMaxEvidencePerAction  = 16
	AutomationMaxActionPayloadBytes = 32 << 10
)

const (
	WorkbenchToolCatalogVersion    = "workbench.tools.v1"
	WorkbenchMaxArgumentsJSONBytes = 24 << 10
	WorkbenchMaxResultJSONBytes    = 32 << 10
	WorkbenchDefaultPageLimit      = 50
	WorkbenchMaxPageLimit          = 100
)

var ErrAutomationValidation = errors.New("automation validation error")

type AutomationValidationError struct {
	Field   string
	Message string
}

func (e *AutomationValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

func (e *AutomationValidationError) Unwrap() error {
	return ErrAutomationValidation
}

func NewAutomationValidationError(field, message string) error {
	return &AutomationValidationError{Field: field, Message: message}
}

type AutomationMode string

const (
	AutomationModeManual    AutomationMode = "manual"
	AutomationModeAutomatic AutomationMode = "automatic"
)

type AutomationRunStatus string

const (
	AutomationRunQueued    AutomationRunStatus = "queued"
	AutomationRunRunning   AutomationRunStatus = "running"
	AutomationRunSucceeded AutomationRunStatus = "succeeded"
	AutomationRunFailed    AutomationRunStatus = "failed"
)

type AutomationSessionKind string

const (
	AutomationSessionKindAutomation AutomationSessionKind = "automation"
	AutomationSessionKindChat       AutomationSessionKind = "chat"
	AutomationSessionKindLegacy     AutomationSessionKind = "legacy"
)

type AutomationSessionStatus string

const (
	AutomationSessionStatusActive    AutomationSessionStatus = "active"
	AutomationSessionStatusCompleted AutomationSessionStatus = "completed"
	AutomationSessionStatusArchived  AutomationSessionStatus = "archived"
)

type AutomationTurnStatus string

const (
	AutomationTurnQueued         AutomationTurnStatus = "queued"
	AutomationTurnRunning        AutomationTurnStatus = "running"
	AutomationTurnAwaitingReview AutomationTurnStatus = "awaiting_review"
	AutomationTurnCompleted      AutomationTurnStatus = "completed"
	AutomationTurnFailed         AutomationTurnStatus = "failed"
	AutomationTurnCancelled      AutomationTurnStatus = "cancelled"
)

type AutomationActionStatus string

const (
	AutomationActionProposed       AutomationActionStatus = "proposed"
	AutomationActionAwaitingReview AutomationActionStatus = "awaiting_review"
	AutomationActionApproved       AutomationActionStatus = "approved"
	AutomationActionExecuting      AutomationActionStatus = "executing"
	AutomationActionSucceeded      AutomationActionStatus = "succeeded"
	AutomationActionRejected       AutomationActionStatus = "rejected"
	AutomationActionStale          AutomationActionStatus = "stale"
	AutomationActionSuperseded     AutomationActionStatus = "superseded"
	AutomationActionFailed         AutomationActionStatus = "failed"
	AutomationActionDenied         AutomationActionStatus = "denied"
	AutomationActionUnknown        AutomationActionStatus = "unknown"
)

type AutomationRisk string

const (
	AutomationRiskR0 AutomationRisk = "R0"
	AutomationRiskR1 AutomationRisk = "R1"
	AutomationRiskR2 AutomationRisk = "R2"
	AutomationRiskR3 AutomationRisk = "R3"
	AutomationRiskR4 AutomationRisk = "R4"
)

type AutomationPolicyDecision string

const (
	AutomationDecisionAutoComplete AutomationPolicyDecision = "auto_complete"
	AutomationDecisionAutoApprove  AutomationPolicyDecision = "auto_approve"
	AutomationDecisionReview       AutomationPolicyDecision = "review"
	AutomationDecisionDeny         AutomationPolicyDecision = "deny"
)

type AutomationOutboxStatus string

const (
	AutomationOutboxPending     AutomationOutboxStatus = "pending"
	AutomationOutboxDispatching AutomationOutboxStatus = "dispatching"
	AutomationOutboxSent        AutomationOutboxStatus = "sent"
	AutomationOutboxFailed      AutomationOutboxStatus = "failed"
	AutomationOutboxUnknown     AutomationOutboxStatus = "unknown"
)

type AutomationToolName string

const (
	AutomationToolMessageClassify    AutomationToolName = "message.classify"
	AutomationToolCandidateCreate    AutomationToolName = "candidate.create"
	AutomationToolCandidateUpdate    AutomationToolName = "candidate.update"
	AutomationToolCandidateLink      AutomationToolName = "candidate.link"
	AutomationToolJobMatch           AutomationToolName = "job.match"
	AutomationToolJobCreate          AutomationToolName = "job.create"
	AutomationToolLeadCreate         AutomationToolName = "lead.create"
	AutomationToolLeadTransition     AutomationToolName = "lead.transition"
	AutomationToolTaskCreate         AutomationToolName = "task.create"
	AutomationToolNoteCreate         AutomationToolName = "note.create"
	AutomationToolJobUpdate          AutomationToolName = "job.update"
	AutomationToolOnboardingCreate   AutomationToolName = "onboarding.create"
	AutomationToolOnboardingUpdate   AutomationToolName = "onboarding.update"
	AutomationToolFleetUpdate        AutomationToolName = "onboarding.fleet_update"
	AutomationToolServiceCreate      AutomationToolName = "service_request.create"
	AutomationToolServiceUpdate      AutomationToolName = "service_request.update"
	AutomationToolFundCreate         AutomationToolName = "fund_callback.create"
	AutomationToolReplyPrivateText   AutomationToolName = "reply.private_text"
	AutomationToolReplyAudienceText  AutomationToolName = "reply.audience_text"
	AutomationToolForwardEvent       AutomationToolName = "forward.event"
	AutomationToolWorkbenchQuery     AutomationToolName = "workbench.query"
	AutomationToolWorkbenchMutate    AutomationToolName = "workbench.mutate"
	AutomationToolWorkbenchAllowOnce AutomationToolName = "workbench.allow_once"
)

type WorkbenchOperation string

type WorkbenchImplementationState string

const (
	WorkbenchOperationWired        WorkbenchImplementationState = "wired"
	WorkbenchOperationBackingReady WorkbenchImplementationState = "backing_ready"
	WorkbenchOperationNewRequired  WorkbenchImplementationState = "new_required"
)

type WorkbenchExposure string

const (
	WorkbenchExposureAllowed   WorkbenchExposure = "allowed"
	WorkbenchExposureForbidden WorkbenchExposure = "forbidden"
)

type WorkbenchSideEffect string

const (
	WorkbenchSideEffectReadOnly   WorkbenchSideEffect = "read_only"
	WorkbenchSideEffectReversible WorkbenchSideEffect = "reversible"
	WorkbenchSideEffectHighImpact WorkbenchSideEffect = "high_impact"
)

type WorkbenchAvailability string

const (
	WorkbenchAvailabilityAvailable   WorkbenchAvailability = "available"
	WorkbenchAvailabilityUnavailable WorkbenchAvailability = "unavailable"
)

type WorkbenchErrorCode string

const (
	WorkbenchErrorInvalidArguments     WorkbenchErrorCode = "invalid_arguments"
	WorkbenchErrorUnsupportedOperation WorkbenchErrorCode = "unsupported_operation"
	WorkbenchErrorPayloadTooLarge      WorkbenchErrorCode = "payload_too_large"
	WorkbenchErrorResultTooLarge       WorkbenchErrorCode = "result_too_large"
	WorkbenchErrorNotFound             WorkbenchErrorCode = "not_found"
	WorkbenchErrorConflict             WorkbenchErrorCode = "conflict"
	WorkbenchErrorUnavailable          WorkbenchErrorCode = "unavailable"
	WorkbenchErrorAllowOnceRequired    WorkbenchErrorCode = "allow_once_required"
	WorkbenchErrorForbidden            WorkbenchErrorCode = "forbidden"
	WorkbenchErrorDeadlineExceeded     WorkbenchErrorCode = "deadline_exceeded"
	WorkbenchErrorCancelled            WorkbenchErrorCode = "cancelled"
	WorkbenchErrorLoopLimit            WorkbenchErrorCode = "loop_limit"
	WorkbenchErrorInternal             WorkbenchErrorCode = "internal_error"
)

type WorkbenchToolRequest struct {
	Operation     WorkbenchOperation `json:"operation"`
	ArgumentsJSON string             `json:"arguments_json"`
}

type WorkbenchPage struct {
	Limit      int  `json:"limit"`
	Offset     int  `json:"offset"`
	Returned   int  `json:"returned"`
	Total      int  `json:"total,omitempty"`
	HasMore    bool `json:"has_more"`
	NextOffset int  `json:"next_offset,omitempty"`
}

type WorkbenchError struct {
	Code      WorkbenchErrorCode `json:"code"`
	Message   string             `json:"message"`
	Retryable bool               `json:"retryable,omitempty"`
}

type WorkbenchToolResult struct {
	Operation WorkbenchOperation `json:"operation"`
	OK        bool               `json:"ok"`
	Data      json.RawMessage    `json:"data,omitempty"`
	Page      *WorkbenchPage     `json:"page,omitempty"`
	Error     *WorkbenchError    `json:"error,omitempty"`
}

type AutomationPermissionDecision string

const (
	AutomationPermissionAllowOnce       AutomationPermissionDecision = "allow_once"
	AutomationPermissionAllowRun        AutomationPermissionDecision = "allow_run"
	AutomationPermissionAllowPersistent AutomationPermissionDecision = "allow_persistent"
	AutomationPermissionDenyOnce        AutomationPermissionDecision = "deny_once"
	AutomationPermissionDenyPersistent  AutomationPermissionDecision = "deny_persistent"
)

const (
	AutomationPermissionStatusPending = "pending"
	AutomationPermissionStatusAllowed = "allowed"
	AutomationPermissionStatusDenied  = "denied"
	AutomationPermissionStatusExpired = "expired"

	AutomationPermissionEffectAllow = "allow"
	AutomationPermissionEffectDeny  = "deny"
)

const (
	AutomationEntityCandidate      = "candidate"
	AutomationEntityJob            = "job"
	AutomationEntityLead           = "lead"
	AutomationEntityTask           = "task"
	AutomationEntityNote           = "note"
	AutomationEntityOnboarding     = "onboarding"
	AutomationEntityServiceRequest = "service_request"
	AutomationEntityFundCallback   = "fund_callback"
)

type AutomationEvidence struct {
	Source   string `json:"source"`
	EntityID string `json:"entity_id,omitempty"`
	Field    string `json:"field,omitempty"`
	Excerpt  string `json:"excerpt"`
}

type AutomationSourceMessageRef struct {
	EventID         string   `json:"event_id"`
	MessageKey      string   `json:"message_key"`
	ExpectedVersion uint64   `json:"expected_version"`
	ChatType        ChatType `json:"chat_type"`
	PeerID          string   `json:"peer_id"`
	Suspicious      bool     `json:"suspicious"`
}

type AutomationEntityPrecondition struct {
	EntityType      string `json:"entity_type"`
	EntityID        string `json:"entity_id"`
	ExpectedVersion uint64 `json:"expected_version"`
}

type AutomationRecipient struct {
	AudienceID      string   `json:"audience_id,omitempty"`
	AudienceVersion uint64   `json:"audience_version,omitempty"`
	ChatType        ChatType `json:"chat_type"`
	PeerID          string   `json:"peer_id"`
}

type AutomationProviderUsage struct {
	InputTokens       int `json:"input_tokens"`
	CachedInputTokens int `json:"cached_input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalTokens       int `json:"total_tokens"`
}

type AutomationChatContextEntityRef struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
}

type AutomationChatContextToolResultRef struct {
	ActionID  string `json:"action_id,omitempty"`
	ResultID  string `json:"result_id,omitempty"`
	Operation string `json:"operation,omitempty"`
	Summary   string `json:"summary"`
}

type AutomationChatContextSummary struct {
	Objective       string                               `json:"objective,omitempty"`
	Facts           []string                             `json:"facts"`
	EntityRefs      []AutomationChatContextEntityRef     `json:"entity_refs"`
	Decisions       []string                             `json:"decisions"`
	PendingItems    []string                             `json:"pending_items"`
	ToolResultRefs  []AutomationChatContextToolResultRef `json:"tool_result_refs"`
	UserConstraints []string                             `json:"user_constraints"`
}

type AutomationChatCheckpoint struct {
	SchemaVersion    string                       `json:"schema_version"`
	ThroughTurnID    string                       `json:"through_turn_id"`
	PromptRevisionID string                       `json:"prompt_revision_id,omitempty"`
	SourceTurnCount  int                          `json:"source_turn_count"`
	Summary          AutomationChatContextSummary `json:"summary"`
	CreatedAt        time.Time                    `json:"created_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
}

type AutomationSession struct {
	ID                string                    `json:"id"`
	Kind              AutomationSessionKind     `json:"kind"`
	Title             string                    `json:"title"`
	Status            AutomationSessionStatus   `json:"status"`
	ContextCheckpoint *AutomationChatCheckpoint `json:"context_checkpoint,omitempty"`
	Version           uint64                    `json:"version"`
	CreatedAt         time.Time                 `json:"created_at"`
	UpdatedAt         time.Time                 `json:"updated_at"`
}

type AutomationTurn struct {
	ID               string                   `json:"id"`
	SessionID        string                   `json:"session_id"`
	UserText         string                   `json:"user_text"`
	AssistantText    string                   `json:"assistant_text"`
	Status           AutomationTurnStatus     `json:"status"`
	ProviderName     string                   `json:"provider_name"`
	PromptRevisionID string                   `json:"prompt_revision_id,omitempty"`
	ProviderUsage    *AutomationProviderUsage `json:"provider_usage,omitempty"`
	Version          uint64                   `json:"version"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
	StartedAt        *time.Time               `json:"started_at,omitempty"`
	CompletedAt      *time.Time               `json:"completed_at,omitempty"`
}

type AutomationTurnEvent struct {
	Sequence  uint64          `json:"sequence"`
	SessionID string          `json:"session_id"`
	TurnID    string          `json:"turn_id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type AutomationResult struct {
	ID                  string                   `json:"id"`
	ActionID            string                   `json:"action_id"`
	PermissionRequestID string                   `json:"permission_request_id"`
	Status              AutomationDeliveryStatus `json:"status"`
	Data                json.RawMessage          `json:"data,omitempty"`
	ErrorCode           string                   `json:"error_code,omitempty"`
	ErrorMessage        string                   `json:"error_message,omitempty"`
	CreatedAt           time.Time                `json:"created_at"`
}

type AutomationJobCreateResult struct {
	Entity   *AutomationEntityResult `json:"entity"`
	Decision JobIngestionDecision    `json:"decision"`
}

type AutomationRun struct {
	ID                     string              `json:"id"`
	SessionID              string              `json:"session_id"`
	TurnID                 string              `json:"turn_id"`
	PromptRevisionID       string              `json:"prompt_revision_id,omitempty"`
	IdempotencyKey         string              `json:"idempotency_key"`
	EngineVersion          string              `json:"engine_version"`
	ProviderName           string              `json:"provider_name"`
	SourceMessageEventID   string              `json:"source_message_event_id"`
	SourceMessageKey       string              `json:"source_message_key"`
	SourceMessageVersion   uint64              `json:"source_message_version"`
	Status                 AutomationRunStatus `json:"status"`
	Version                uint64              `json:"version"`
	Attempt                uint32              `json:"attempt"`
	RetryOfRunID           string              `json:"retry_of_run_id,omitempty"`
	FailureStage           string              `json:"failure_stage,omitempty"`
	FailureCode            string              `json:"failure_code,omitempty"`
	FailureMessage         string              `json:"failure_message,omitempty"`
	ProviderResponseSHA256 string              `json:"provider_response_sha256,omitempty"`
	ActionIDs              []string            `json:"action_ids"`
	CreatedAt              time.Time           `json:"created_at"`
	StartedAt              *time.Time          `json:"started_at,omitempty"`
	CompletedAt            *time.Time          `json:"completed_at,omitempty"`
}

type AutomationAction struct {
	ID                 string                         `json:"id"`
	RunID              string                         `json:"run_id"`
	ProviderCallID     string                         `json:"provider_call_id"`
	Tool               AutomationToolName             `json:"tool"`
	Operation          WorkbenchOperation             `json:"operation,omitempty"`
	Status             AutomationActionStatus         `json:"status"`
	Risk               AutomationRisk                 `json:"risk"`
	PolicyDecision     AutomationPolicyDecision       `json:"policy_decision"`
	PolicyVersion      uint64                         `json:"policy_version"`
	RiskReasons        []string                       `json:"risk_reasons"`
	Confidence         float64                        `json:"confidence"`
	Evidence           []AutomationEvidence           `json:"evidence"`
	SourceMessage      AutomationSourceMessageRef     `json:"source_message"`
	Preconditions      []AutomationEntityPrecondition `json:"preconditions"`
	DependsOnActionIDs []string                       `json:"depends_on_action_ids"`
	Recipient          *AutomationRecipient           `json:"recipient,omitempty"`
	Payload            json.RawMessage                `json:"payload"`
	ResultID           string                         `json:"result_id,omitempty"`
	Result             json.RawMessage                `json:"result,omitempty"`
	ErrorCode          string                         `json:"error_code,omitempty"`
	ErrorMessage       string                         `json:"error_message,omitempty"`
	Retryable          bool                           `json:"retryable"`
	Attempt            uint32                         `json:"attempt"`
	RetryOfActionID    string                         `json:"retry_of_action_id,omitempty"`
	ReplacesActionID   string                         `json:"replaces_action_id,omitempty"`
	ReplacedByActionID string                         `json:"replaced_by_action_id,omitempty"`
	Version            uint64                         `json:"version"`
	CreatedAt          time.Time                      `json:"created_at"`
	UpdatedAt          time.Time                      `json:"updated_at"`
}

type AutomationApprovalRecord struct {
	ID                  string    `json:"id"`
	ActionID            string    `json:"action_id"`
	ActionVersion       uint64    `json:"action_version"`
	Decision            string    `json:"decision"`
	OperatorID          string    `json:"operator_id"`
	Reason              string    `json:"reason,omitempty"`
	ReplacementActionID string    `json:"replacement_action_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type AutomationOutboxEntry struct {
	ID                string                 `json:"id"`
	ActionID          string                 `json:"action_id"`
	IdempotencyKey    string                 `json:"idempotency_key"`
	ChatType          ChatType               `json:"chat_type"`
	PeerID            string                 `json:"peer_id"`
	Text              string                 `json:"text"`
	Status            AutomationOutboxStatus `json:"status"`
	Attempt           uint32                 `json:"attempt"`
	DispatchAttempted bool                   `json:"dispatch_attempted"`
	ErrorCode         string                 `json:"error_code,omitempty"`
	ErrorMessage      string                 `json:"error_message,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	DispatchStartedAt *time.Time             `json:"dispatch_started_at,omitempty"`
	CompletedAt       *time.Time             `json:"completed_at,omitempty"`
}

type AutomationPolicy struct {
	Version                   uint64               `json:"version"`
	Mode                      AutomationMode       `json:"mode"`
	R1AutoApprove             bool                 `json:"r1_auto_approve"`
	R1MinConfidence           float64              `json:"r1_min_confidence"`
	R1AllowedTools            []AutomationToolName `json:"r1_allowed_tools"`
	R2PrivateReplyAutoApprove bool                 `json:"r2_private_reply_auto_approve"`
	R2MinConfidence           float64              `json:"r2_min_confidence"`
	R2AllowedIntents          []string             `json:"r2_allowed_intents"`
	UpdatedAt                 time.Time            `json:"updated_at"`
}

type AutomationAuditRecord struct {
	ID        string          `json:"id"`
	EventType string          `json:"event_type"`
	RunID     string          `json:"run_id,omitempty"`
	ActionID  string          `json:"action_id,omitempty"`
	OutboxID  string          `json:"outbox_id,omitempty"`
	ActorType string          `json:"actor_type"`
	ActorID   string          `json:"actor_id,omitempty"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

type AutomationPermissionRequest struct {
	ID         string                       `json:"id"`
	RunID      string                       `json:"run_id"`
	ActionID   string                       `json:"action_id"`
	Tool       AutomationToolName           `json:"tool"`
	Operation  WorkbenchOperation           `json:"operation,omitempty"`
	Risk       AutomationRisk               `json:"risk"`
	Status     string                       `json:"status"`
	Version    uint64                       `json:"version"`
	Decision   AutomationPermissionDecision `json:"decision,omitempty"`
	OperatorID string                       `json:"operator_id,omitempty"`
	Reason     string                       `json:"reason,omitempty"`
	RuleID     string                       `json:"rule_id,omitempty"`
	CreatedAt  time.Time                    `json:"created_at"`
	UpdatedAt  time.Time                    `json:"updated_at"`
}

type AutomationPermissionRule struct {
	ID        string             `json:"id"`
	Effect    string             `json:"effect"`
	Tool      AutomationToolName `json:"tool"`
	Operation WorkbenchOperation `json:"operation,omitempty"`
	ScopeKey  string             `json:"scope_key"`
	Scope     json.RawMessage    `json:"scope"`
	Enabled   bool               `json:"enabled"`
	Version   uint64             `json:"version"`
	CreatedBy string             `json:"created_by"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type AutomationRuntimeEvent struct {
	Sequence            uint64          `json:"sequence"`
	Type                string          `json:"type"`
	RunID               string          `json:"run_id,omitempty"`
	ActionID            string          `json:"action_id,omitempty"`
	PermissionRequestID string          `json:"permission_request_id,omitempty"`
	Data                json.RawMessage `json:"data"`
	CreatedAt           time.Time       `json:"created_at"`
}

type AutomationIdempotencyRecord struct {
	Kind       string    `json:"kind"`
	ResourceID string    `json:"resource_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type AutomationCounters struct {
	Session           uint64 `json:"session"`
	Turn              uint64 `json:"turn"`
	TurnEvent         uint64 `json:"turn_event"`
	Run               uint64 `json:"run"`
	Action            uint64 `json:"action"`
	Result            uint64 `json:"result"`
	Approval          uint64 `json:"approval"`
	Outbox            uint64 `json:"outbox"`
	Audit             uint64 `json:"audit"`
	PermissionRequest uint64 `json:"permission_request"`
	PermissionRule    uint64 `json:"permission_rule"`
}

type AutomationState struct {
	SchemaVersion               int                                    `json:"schema_version"`
	Counters                    AutomationCounters                     `json:"counters"`
	Policy                      AutomationPolicy                       `json:"policy"`
	Sessions                    []AutomationSession                    `json:"sessions"`
	Turns                       []AutomationTurn                       `json:"turns"`
	TurnEvents                  []AutomationTurnEvent                  `json:"turn_events"`
	Runs                        []AutomationRun                        `json:"runs"`
	Actions                     []AutomationAction                     `json:"actions"`
	Results                     []AutomationResult                     `json:"results"`
	Approvals                   []AutomationApprovalRecord             `json:"approvals"`
	Outbox                      []AutomationOutboxEntry                `json:"outbox"`
	Idempotency                 map[string]AutomationIdempotencyRecord `json:"idempotency"`
	PendingAudit                []AutomationAuditRecord                `json:"pending_audit"`
	PermissionRequests          []AutomationPermissionRequest          `json:"permission_requests"`
	PermissionRules             []AutomationPermissionRule             `json:"permission_rules"`
	LastObservedCRMEventCounter uint64                                 `json:"last_observed_crm_event_counter"`
}

func IsValidAutomationMode(mode AutomationMode) bool {
	return mode == AutomationModeManual || mode == AutomationModeAutomatic
}

func IsValidAutomationRunStatus(status AutomationRunStatus) bool {
	switch status {
	case AutomationRunQueued, AutomationRunRunning, AutomationRunSucceeded, AutomationRunFailed:
		return true
	default:
		return false
	}
}

func IsValidAutomationSessionKind(kind AutomationSessionKind) bool {
	switch kind {
	case AutomationSessionKindAutomation, AutomationSessionKindChat, AutomationSessionKindLegacy:
		return true
	default:
		return false
	}
}

func IsValidAutomationSessionStatus(status AutomationSessionStatus) bool {
	switch status {
	case AutomationSessionStatusActive, AutomationSessionStatusCompleted, AutomationSessionStatusArchived:
		return true
	default:
		return false
	}
}

func IsValidAutomationTurnStatus(status AutomationTurnStatus) bool {
	switch status {
	case AutomationTurnQueued,
		AutomationTurnRunning,
		AutomationTurnAwaitingReview,
		AutomationTurnCompleted,
		AutomationTurnFailed,
		AutomationTurnCancelled:
		return true
	default:
		return false
	}
}

func CanTransitionAutomationRunStatus(from, to AutomationRunStatus) bool {
	switch from {
	case AutomationRunQueued:
		return to == AutomationRunRunning
	case AutomationRunRunning:
		return to == AutomationRunSucceeded || to == AutomationRunFailed
	default:
		return false
	}
}

func ValidateAutomationRunTransition(from, to AutomationRunStatus) error {
	if !IsValidAutomationRunStatus(from) {
		return NewAutomationValidationError("from_status", "invalid automation run status")
	}
	if !IsValidAutomationRunStatus(to) {
		return NewAutomationValidationError("status", "invalid automation run status")
	}
	if !CanTransitionAutomationRunStatus(from, to) {
		return NewAutomationValidationError("status", fmt.Sprintf("cannot transition from %q to %q", from, to))
	}
	return nil
}

func IsValidAutomationActionStatus(status AutomationActionStatus) bool {
	switch status {
	case AutomationActionProposed,
		AutomationActionAwaitingReview,
		AutomationActionApproved,
		AutomationActionExecuting,
		AutomationActionSucceeded,
		AutomationActionRejected,
		AutomationActionStale,
		AutomationActionSuperseded,
		AutomationActionFailed,
		AutomationActionDenied,
		AutomationActionUnknown:
		return true
	default:
		return false
	}
}

func CanTransitionAutomationActionStatus(from, to AutomationActionStatus) bool {
	switch from {
	case AutomationActionProposed:
		return to == AutomationActionSucceeded ||
			to == AutomationActionAwaitingReview ||
			to == AutomationActionApproved ||
			to == AutomationActionDenied
	case AutomationActionAwaitingReview:
		return to == AutomationActionApproved ||
			to == AutomationActionRejected ||
			to == AutomationActionDenied ||
			to == AutomationActionStale ||
			to == AutomationActionSuperseded
	case AutomationActionApproved:
		return to == AutomationActionExecuting
	case AutomationActionExecuting:
		return to == AutomationActionSucceeded ||
			to == AutomationActionFailed ||
			to == AutomationActionStale ||
			to == AutomationActionSuperseded ||
			to == AutomationActionUnknown
	default:
		return false
	}
}

func ValidateAutomationActionTransition(from, to AutomationActionStatus) error {
	if !IsValidAutomationActionStatus(from) {
		return NewAutomationValidationError("from_status", "invalid automation action status")
	}
	if !IsValidAutomationActionStatus(to) {
		return NewAutomationValidationError("status", "invalid automation action status")
	}
	if !CanTransitionAutomationActionStatus(from, to) {
		return NewAutomationValidationError("status", fmt.Sprintf("cannot transition from %q to %q", from, to))
	}
	return nil
}

func IsTerminalAutomationActionStatus(status AutomationActionStatus) bool {
	switch status {
	case AutomationActionSucceeded,
		AutomationActionRejected,
		AutomationActionStale,
		AutomationActionSuperseded,
		AutomationActionFailed,
		AutomationActionDenied,
		AutomationActionUnknown:
		return true
	default:
		return false
	}
}

func IsValidAutomationRisk(risk AutomationRisk) bool {
	switch risk {
	case AutomationRiskR0, AutomationRiskR1, AutomationRiskR2, AutomationRiskR3, AutomationRiskR4:
		return true
	default:
		return false
	}
}

func IsValidAutomationPolicyDecision(decision AutomationPolicyDecision) bool {
	switch decision {
	case AutomationDecisionAutoComplete, AutomationDecisionAutoApprove, AutomationDecisionReview, AutomationDecisionDeny:
		return true
	default:
		return false
	}
}

func IsValidAutomationOutboxStatus(status AutomationOutboxStatus) bool {
	switch status {
	case AutomationOutboxPending,
		AutomationOutboxDispatching,
		AutomationOutboxSent,
		AutomationOutboxFailed,
		AutomationOutboxUnknown:
		return true
	default:
		return false
	}
}

func CanTransitionAutomationOutboxStatus(from, to AutomationOutboxStatus) bool {
	switch from {
	case AutomationOutboxPending:
		return to == AutomationOutboxDispatching || to == AutomationOutboxFailed
	case AutomationOutboxDispatching:
		return to == AutomationOutboxSent || to == AutomationOutboxFailed || to == AutomationOutboxUnknown
	case AutomationOutboxFailed:
		return to == AutomationOutboxPending
	default:
		return false
	}
}

func ValidateAutomationOutboxTransition(from, to AutomationOutboxStatus) error {
	if !IsValidAutomationOutboxStatus(from) {
		return NewAutomationValidationError("from_status", "invalid automation outbox status")
	}
	if !IsValidAutomationOutboxStatus(to) {
		return NewAutomationValidationError("status", "invalid automation outbox status")
	}
	if !CanTransitionAutomationOutboxStatus(from, to) {
		return NewAutomationValidationError("status", fmt.Sprintf("cannot transition from %q to %q", from, to))
	}
	return nil
}

func IsValidAutomationToolName(tool AutomationToolName) bool {
	switch tool {
	case AutomationToolMessageClassify,
		AutomationToolCandidateCreate,
		AutomationToolCandidateUpdate,
		AutomationToolCandidateLink,
		AutomationToolJobMatch,
		AutomationToolJobCreate,
		AutomationToolLeadCreate,
		AutomationToolLeadTransition,
		AutomationToolTaskCreate,
		AutomationToolNoteCreate,
		AutomationToolJobUpdate,
		AutomationToolOnboardingCreate,
		AutomationToolOnboardingUpdate,
		AutomationToolFleetUpdate,
		AutomationToolServiceCreate,
		AutomationToolServiceUpdate,
		AutomationToolFundCreate,
		AutomationToolReplyPrivateText,
		AutomationToolReplyAudienceText,
		AutomationToolForwardEvent,
		AutomationToolWorkbenchQuery,
		AutomationToolWorkbenchMutate,
		AutomationToolWorkbenchAllowOnce:
		return true
	default:
		return false
	}
}

func IsWorkbenchToolName(tool AutomationToolName) bool {
	switch tool {
	case AutomationToolWorkbenchQuery,
		AutomationToolWorkbenchMutate,
		AutomationToolWorkbenchAllowOnce:
		return true
	default:
		return false
	}
}

func IsValidWorkbenchSideEffect(sideEffect WorkbenchSideEffect) bool {
	switch sideEffect {
	case WorkbenchSideEffectReadOnly, WorkbenchSideEffectReversible, WorkbenchSideEffectHighImpact:
		return true
	default:
		return false
	}
}

func IsValidWorkbenchAvailability(availability WorkbenchAvailability) bool {
	return availability == WorkbenchAvailabilityAvailable ||
		availability == WorkbenchAvailabilityUnavailable
}

func IsValidWorkbenchErrorCode(code WorkbenchErrorCode) bool {
	switch code {
	case WorkbenchErrorInvalidArguments,
		WorkbenchErrorUnsupportedOperation,
		WorkbenchErrorPayloadTooLarge,
		WorkbenchErrorResultTooLarge,
		WorkbenchErrorNotFound,
		WorkbenchErrorConflict,
		WorkbenchErrorUnavailable,
		WorkbenchErrorAllowOnceRequired,
		WorkbenchErrorForbidden,
		WorkbenchErrorDeadlineExceeded,
		WorkbenchErrorCancelled,
		WorkbenchErrorLoopLimit,
		WorkbenchErrorInternal:
		return true
	default:
		return false
	}
}

func AutomationToolOutputEntityType(tool AutomationToolName) string {
	switch tool {
	case AutomationToolCandidateCreate, AutomationToolCandidateUpdate, AutomationToolCandidateLink:
		return AutomationEntityCandidate
	case AutomationToolLeadCreate, AutomationToolLeadTransition:
		return AutomationEntityLead
	case AutomationToolJobCreate, AutomationToolJobUpdate:
		return AutomationEntityJob
	case AutomationToolTaskCreate:
		return AutomationEntityTask
	case AutomationToolNoteCreate:
		return AutomationEntityNote
	case AutomationToolOnboardingCreate, AutomationToolOnboardingUpdate, AutomationToolFleetUpdate:
		return AutomationEntityOnboarding
	case AutomationToolServiceCreate, AutomationToolServiceUpdate:
		return AutomationEntityServiceRequest
	case AutomationToolFundCreate:
		return AutomationEntityFundCallback
	default:
		return ""
	}
}

func IsValidAutomationPermissionDecision(decision AutomationPermissionDecision) bool {
	switch decision {
	case AutomationPermissionAllowOnce,
		AutomationPermissionAllowRun,
		AutomationPermissionAllowPersistent,
		AutomationPermissionDenyOnce,
		AutomationPermissionDenyPersistent:
		return true
	default:
		return false
	}
}
