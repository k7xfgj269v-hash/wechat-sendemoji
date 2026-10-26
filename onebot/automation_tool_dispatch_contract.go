package main

import (
	"encoding/json"
	"time"
)

type WorkbenchExecutionContext struct {
	RunID               string                       `json:"run_id,omitempty"`
	TurnID              string                       `json:"turn_id,omitempty"`
	CallID              string                       `json:"call_id,omitempty"`
	ActionID            string                       `json:"action_id,omitempty"`
	PermissionRequestID string                       `json:"permission_request_id,omitempty"`
	PermissionDecision  AutomationPermissionDecision `json:"permission_decision,omitempty"`
	OperatorID          string                       `json:"operator_id,omitempty"`
}

type WorkbenchDecodedToolRequest struct {
	Tool               AutomationToolName
	Request            WorkbenchToolRequest
	Arguments          any
	CanonicalArguments json.RawMessage
	CanonicalRequest   json.RawMessage
}

type WorkbenchEmptyArguments struct{}

type WorkbenchPageArguments struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type WorkbenchIDArguments struct {
	ID string `json:"id"`
}

type WorkbenchVersionedIDArguments struct {
	ID              string `json:"id"`
	ExpectedVersion uint64 `json:"expected_version"`
}

type WorkbenchSearchArguments struct {
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type WorkbenchContactSearchArguments struct {
	Keyword   string `json:"keyword,omitempty"`
	LocalType *int   `json:"local_type,omitempty"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
}

type WorkbenchContactGetArguments struct {
	Username string `json:"username"`
}

type WorkbenchGroupMembersArguments struct {
	GroupID string `json:"group_id"`
	Limit   int    `json:"limit"`
	Offset  int    `json:"offset"`
}

type WorkbenchMessageListArguments struct {
	Query        string       `json:"query,omitempty"`
	ReviewStatus ReviewStatus `json:"review_status,omitempty"`
	CandidateID  string       `json:"candidate_id,omitempty"`
	Limit        int          `json:"limit"`
	Offset       int          `json:"offset"`
}

type WorkbenchCandidateListArguments struct {
	Query  string          `json:"query,omitempty"`
	Status CandidateStatus `json:"status,omitempty"`
	Owner  string          `json:"owner,omitempty"`
	Source string          `json:"source,omitempty"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type WorkbenchJobListArguments struct {
	Query    string      `json:"query,omitempty"`
	Status   JobStatus   `json:"status,omitempty"`
	Priority JobPriority `json:"priority,omitempty"`
	SiteName string      `json:"site_name,omitempty"`
	Owner    string      `json:"owner,omitempty"`
	Limit    int         `json:"limit"`
	Offset   int         `json:"offset"`
}

type WorkbenchRevisionListArguments struct {
	JobID  string `json:"job_id"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type WorkbenchRevisionGetArguments struct {
	JobID      string `json:"job_id"`
	RevisionID string `json:"revision_id"`
}

type WorkbenchRevisionCompareArguments struct {
	JobID          string `json:"job_id"`
	FromRevisionID string `json:"from_revision_id"`
	ToRevisionID   string `json:"to_revision_id"`
}

type WorkbenchLeadListArguments struct {
	CandidateID string    `json:"candidate_id,omitempty"`
	JobID       string    `json:"job_id,omitempty"`
	Stage       LeadStage `json:"stage,omitempty"`
	Owner       string    `json:"owner,omitempty"`
	Limit       int       `json:"limit"`
	Offset      int       `json:"offset"`
}

type WorkbenchNoteListArguments struct {
	TargetType NoteTargetType `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
}

type WorkbenchTaskListArguments struct {
	Owner       *string        `json:"owner,omitempty"`
	Status      FollowUpStatus `json:"status,omitempty"`
	Overdue     *bool          `json:"overdue,omitempty"`
	DueFrom     *time.Time     `json:"due_from,omitempty"`
	DueTo       *time.Time     `json:"due_to,omitempty"`
	CandidateID string         `json:"candidate_id,omitempty"`
	LeadID      string         `json:"lead_id,omitempty"`
	Limit       int            `json:"limit"`
	Offset      int            `json:"offset"`
}

type WorkbenchOnboardingListArguments struct {
	LeadID        string           `json:"lead_id,omitempty"`
	CandidateID   string           `json:"candidate_id,omitempty"`
	JobID         string           `json:"job_id,omitempty"`
	Status        OnboardingStatus `json:"status,omitempty"`
	FleetStatus   FleetStatus      `json:"fleet_status,omitempty"`
	FleetRequired *bool            `json:"fleet_required,omitempty"`
	Limit         int              `json:"limit"`
	Offset        int              `json:"offset"`
}

type WorkbenchServiceRequestListArguments struct {
	Query            string                 `json:"query,omitempty"`
	CandidateID      string                 `json:"candidate_id,omitempty"`
	JobID            string                 `json:"job_id,omitempty"`
	OnboardingCaseID string                 `json:"onboarding_case_id,omitempty"`
	Category         ServiceRequestCategory `json:"category,omitempty"`
	Priority         JobPriority            `json:"priority,omitempty"`
	Status           ServiceRequestStatus   `json:"status,omitempty"`
	Owner            *string                `json:"owner,omitempty"`
	RequiresHuman    *bool                  `json:"requires_human,omitempty"`
	Overdue          *bool                  `json:"overdue,omitempty"`
	Limit            int                    `json:"limit"`
	Offset           int                    `json:"offset"`
}

type WorkbenchFundListArguments struct {
	Query            string             `json:"query,omitempty"`
	CandidateID      string             `json:"candidate_id,omitempty"`
	JobID            string             `json:"job_id,omitempty"`
	LeadID           string             `json:"lead_id,omitempty"`
	OnboardingCaseID string             `json:"onboarding_case_id,omitempty"`
	Status           FundCallbackStatus `json:"status,omitempty"`
	Currency         string             `json:"currency,omitempty"`
	Limit            int                `json:"limit"`
	Offset           int                `json:"offset"`
}

type WorkbenchFundLedgerArguments struct {
	ID     string `json:"id"`
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
}

type WorkbenchAutomationListArguments struct {
	Status string             `json:"status,omitempty"`
	Tool   AutomationToolName `json:"tool,omitempty"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

type WorkbenchAuditListArguments struct {
	Operation     WorkbenchOperation `json:"operation,omitempty"`
	EntityType    string             `json:"entity_type,omitempty"`
	EntityID      string             `json:"entity_id,omitempty"`
	SourceEventID string             `json:"source_event_id,omitempty"`
	ActionID      string             `json:"action_id,omitempty"`
	RunID         string             `json:"run_id,omitempty"`
	Limit         int                `json:"limit"`
	Offset        int                `json:"offset"`
}

type WorkbenchMessageReviewArguments struct {
	MessageID       string       `json:"message_id"`
	ExpectedVersion uint64       `json:"expected_version"`
	ReviewStatus    ReviewStatus `json:"review_status"`
}

type WorkbenchCandidateCreateArguments struct {
	Candidate CandidateDraft `json:"candidate"`
}

type WorkbenchCandidateUpdateArguments struct {
	CandidateID string          `json:"candidate_id"`
	Update      CandidateUpdate `json:"update"`
}

type WorkbenchCandidateLinkMessageArguments struct {
	Request MessageCandidateLink `json:"request"`
}

type WorkbenchJobCreateArguments struct {
	Job JobDraft `json:"job"`
}

type WorkbenchJobUpdateArguments struct {
	JobID  string    `json:"job_id"`
	Update JobUpdate `json:"update"`
}

type WorkbenchLeadCreateArguments struct {
	Lead LeadCreate `json:"lead"`
}

type WorkbenchLeadUpdateArguments struct {
	LeadID string     `json:"lead_id"`
	Update LeadUpdate `json:"update"`
}

type WorkbenchLeadTransitionArguments struct {
	LeadID          string    `json:"lead_id"`
	ExpectedVersion uint64    `json:"expected_version"`
	FromStage       LeadStage `json:"from_stage"`
	ToStage         LeadStage `json:"to_stage"`
	Reason          string    `json:"reason,omitempty"`
}

type WorkbenchNoteCreateArguments struct {
	TargetType NoteTargetType `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Body       string         `json:"body"`
}

type WorkbenchTaskCreateArguments struct {
	Task TaskCreate `json:"task"`
}

type WorkbenchTaskUpdateArguments struct {
	TaskID string     `json:"task_id"`
	Update TaskUpdate `json:"update"`
}

type WorkbenchTaskStatusArguments struct {
	TaskID  string            `json:"task_id"`
	Request TaskStatusRequest `json:"request"`
}

type WorkbenchOnboardingUpdateArguments struct {
	OnboardingID string           `json:"onboarding_id"`
	Update       OnboardingUpdate `json:"update"`
}

type WorkbenchFleetUpdateArguments struct {
	OnboardingID string      `json:"onboarding_id"`
	Update       FleetUpdate `json:"update"`
}

type WorkbenchServiceRequestCreateArguments struct {
	Request ServiceRequestCreate `json:"request"`
}

type WorkbenchServiceRequestUpdateArguments struct {
	RequestID string               `json:"request_id"`
	Update    ServiceRequestUpdate `json:"update"`
}

type WorkbenchServiceQueueCreateArguments struct {
	Queue ServiceQueueCreate `json:"queue"`
}

type WorkbenchServiceQueueUpdateArguments struct {
	QueueID string             `json:"queue_id"`
	Update  ServiceQueueUpdate `json:"update"`
}

type WorkbenchOperationBatchCreateArguments struct {
	Batch OperationBatchCreate `json:"batch"`
}

type WorkbenchOperationBatchUpdateArguments struct {
	BatchID string               `json:"batch_id"`
	Update  OperationBatchUpdate `json:"update"`
}

type WorkbenchOperationBatchMembersArguments struct {
	BatchID string                      `json:"batch_id"`
	Update  OperationBatchMembersUpdate `json:"update"`
}

type WorkbenchRecruitingProjectUpdateArguments struct {
	ExpectedVersion uint64                  `json:"expected_version"`
	Update          RecruitingProjectUpdate `json:"update"`
}

type WorkbenchRecruitingSourceUpsertArguments struct {
	ExpectedProjectVersion uint64                      `json:"expected_project_version"`
	Source                 RecruitingSourceGroupUpsert `json:"source"`
}

type WorkbenchForwardRuleCreateArguments struct {
	GroupID         string   `json:"group_id,omitempty"`
	Keywords        []string `json:"keywords"`
	CooldownSeconds int      `json:"cooldown_seconds"`
	Enabled         bool     `json:"enabled"`
}

type WorkbenchForwardRuleUpdateArguments struct {
	RuleID          string    `json:"rule_id"`
	Keywords        *[]string `json:"keywords,omitempty"`
	CooldownSeconds *int      `json:"cooldown_seconds,omitempty"`
	Enabled         *bool     `json:"enabled,omitempty"`
}

type WorkbenchControlledSendArguments struct {
	EventID string `json:"event_id"`
	Intent  string `json:"intent"`
	Text    string `json:"text"`
}

type WorkbenchAudienceSendArguments struct {
	AudienceID string `json:"audience_id"`
	Intent     string `json:"intent"`
	Text       string `json:"text"`
}

type WorkbenchForwardEventArguments struct {
	EventID string `json:"event_id"`
	RuleID  string `json:"rule_id"`
	Reason  string `json:"reason,omitempty"`
}

type WorkbenchCandidateTerminalArguments struct {
	CandidateID     string          `json:"candidate_id"`
	ExpectedVersion uint64          `json:"expected_version"`
	Status          CandidateStatus `json:"status"`
}

type WorkbenchJobTerminalArguments struct {
	JobID           string    `json:"job_id"`
	ExpectedVersion uint64    `json:"expected_version"`
	Status          JobStatus `json:"status"`
}

type WorkbenchJobCompensationArguments struct {
	JobID                 string  `json:"job_id"`
	ExpectedVersion       uint64  `json:"expected_version"`
	CompensationSummary   *string `json:"compensation_summary,omitempty"`
	Accommodation         *string `json:"accommodation,omitempty"`
	TransportRequirements *string `json:"transport_requirements,omitempty"`
}

type WorkbenchLeadTerminalArguments struct {
	LeadID          string    `json:"lead_id"`
	ExpectedVersion uint64    `json:"expected_version"`
	FromStage       LeadStage `json:"from_stage"`
	ToStage         LeadStage `json:"to_stage"`
	Reason          string    `json:"reason,omitempty"`
}

type WorkbenchRevisionActivateArguments struct {
	JobID           string `json:"job_id"`
	RevisionID      string `json:"revision_id"`
	ExpectedVersion uint64 `json:"expected_version"`
}

type WorkbenchOnboardingCreateArguments struct {
	Onboarding OnboardingCreate `json:"onboarding"`
}

type WorkbenchOnboardingTransitionArguments struct {
	OnboardingID    string           `json:"onboarding_id"`
	ExpectedVersion uint64           `json:"expected_version"`
	FromStatus      OnboardingStatus `json:"from_status,omitempty"`
	ToStatus        OnboardingStatus `json:"to_status"`
	BlockReason     string           `json:"block_reason,omitempty"`
}

type WorkbenchFleetTransitionArguments struct {
	OnboardingID    string      `json:"onboarding_id"`
	ExpectedVersion uint64      `json:"expected_version"`
	FromStatus      FleetStatus `json:"from_status,omitempty"`
	ToStatus        FleetStatus `json:"to_status"`
}

type WorkbenchServiceRequestTransitionArguments struct {
	RequestID       string               `json:"request_id"`
	ExpectedVersion uint64               `json:"expected_version"`
	FromStatus      ServiceRequestStatus `json:"from_status"`
	ToStatus        ServiceRequestStatus `json:"to_status"`
	Resolution      string               `json:"resolution,omitempty"`
}

type WorkbenchFundCreateArguments struct {
	CandidateID      string     `json:"candidate_id"`
	JobID            string     `json:"job_id,omitempty"`
	LeadID           string     `json:"lead_id,omitempty"`
	OnboardingCaseID string     `json:"onboarding_case_id,omitempty"`
	AmountMinor      int64      `json:"amount_minor"`
	Currency         string     `json:"currency"`
	Condition        string     `json:"condition"`
	PromisedBy       string     `json:"promised_by"`
	DueAt            *time.Time `json:"due_at,omitempty"`
	Evidence         string     `json:"evidence,omitempty"`
}

type WorkbenchFundUpdateArguments struct {
	FundID          string              `json:"fund_id"`
	ExpectedVersion uint64              `json:"expected_version"`
	CandidateID     *string             `json:"candidate_id,omitempty"`
	JobID           *string             `json:"job_id,omitempty"`
	LeadID          *string             `json:"lead_id,omitempty"`
	OnboardingID    *string             `json:"onboarding_id,omitempty"`
	AmountMinor     *int64              `json:"amount_minor,omitempty"`
	Currency        *string             `json:"currency,omitempty"`
	Condition       *string             `json:"condition,omitempty"`
	PromisedBy      *string             `json:"promised_by,omitempty"`
	DueAt           *time.Time          `json:"due_at,omitempty"`
	ClearDueAt      bool                `json:"clear_due_at,omitempty"`
	Evidence        *string             `json:"evidence,omitempty"`
	Status          *FundCallbackStatus `json:"status,omitempty"`
}

type WorkbenchFundFinalArguments struct {
	FundID          string `json:"fund_id"`
	ExpectedVersion uint64 `json:"expected_version"`
	FinalResult     string `json:"final_result"`
}

type WorkbenchRecruitingSourceDeleteArguments struct {
	GroupID                string `json:"group_id"`
	ExpectedProjectVersion uint64 `json:"expected_project_version"`
}
