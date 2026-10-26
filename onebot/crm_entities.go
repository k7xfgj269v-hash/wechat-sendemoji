package main

import (
	"time"
)

type RecruitingProjectConfig struct {
	OrganizationName        string                  `json:"organization_name"`
	Version                 uint64                  `json:"version"`
	RequireAuthoritativeJob bool                    `json:"require_authoritative_job"`
	SourceGroups            []RecruitingSourceGroup `json:"source_groups"`
}

type RecruitingSourceGroup struct {
	ID          string    `json:"id"`
	GroupID     string    `json:"group_id"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	GateEnabled bool      `json:"gate_enabled"`
	GateMode    string    `json:"gate_mode,omitempty"`
	Version     uint64    `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type JobProvenance struct {
	ID                   string               `json:"id"`
	JobID                string               `json:"job_id,omitempty"`
	EventID              string               `json:"event_id"`
	MessageKey           string               `json:"message_key"`
	GroupID              string               `json:"group_id"`
	AuthorID             string               `json:"author_id"`
	SourceTimestamp      time.Time            `json:"source_timestamp"`
	ContentSHA256        string               `json:"content_sha256"`
	CanonicalFingerprint string               `json:"canonical_fingerprint,omitempty"`
	DemandCode           string               `json:"demand_code,omitempty"`
	Decision             JobIngestionDecision `json:"decision"`
	JobRevision          uint64               `json:"job_revision,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
}

type Candidate struct {
	ID            string          `json:"id"`
	FullName      string          `json:"full_name"`
	WeChatID      string          `json:"wechat_id,omitempty"`
	Phone         string          `json:"phone,omitempty"`
	Email         string          `json:"email,omitempty"`
	Location      string          `json:"location,omitempty"`
	Source        string          `json:"source,omitempty"`
	SourceEventID string          `json:"source_event_id,omitempty"`
	Skills        []string        `json:"skills"`
	Owner         string          `json:"owner,omitempty"`
	Status        CandidateStatus `json:"status"`
	Summary       string          `json:"summary,omitempty"`
	Version       uint64          `json:"version"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type Job struct {
	ID                     string      `json:"id"`
	Title                  string      `json:"title"`
	Company                string      `json:"company"`
	Location               string      `json:"location,omitempty"`
	EmploymentType         string      `json:"employment_type,omitempty"`
	Description            string      `json:"description,omitempty"`
	Requirements           []string    `json:"requirements"`
	DemandCode             string      `json:"demand_code,omitempty"`
	SiteName               string      `json:"site_name,omitempty"`
	OnsiteContact          string      `json:"onsite_contact,omitempty"`
	Headcount              int         `json:"headcount"`
	Priority               JobPriority `json:"priority"`
	StartAt                *time.Time  `json:"start_at,omitempty"`
	DeadlineAt             *time.Time  `json:"deadline_at,omitempty"`
	Shift                  string      `json:"shift,omitempty"`
	WorkSchedule           string      `json:"work_schedule,omitempty"`
	CompensationSummary    string      `json:"compensation_summary,omitempty"`
	Accommodation          string      `json:"accommodation,omitempty"`
	TransportRequirements  string      `json:"transport_requirements,omitempty"`
	RequiredDocuments      []string    `json:"required_documents"`
	Owner                  string      `json:"owner,omitempty"`
	Status                 JobStatus   `json:"status"`
	ActiveDemandRevisionID string      `json:"active_demand_revision_id,omitempty"`
	DemandRevisionNumber   uint64      `json:"demand_revision_number,omitempty"`
	Version                uint64      `json:"version"`
	CreatedAt              time.Time   `json:"created_at"`
	UpdatedAt              time.Time   `json:"updated_at"`
}

type JobDemandSnapshot struct {
	Title                 string      `json:"title"`
	Company               string      `json:"company"`
	Location              string      `json:"location,omitempty"`
	EmploymentType        string      `json:"employment_type,omitempty"`
	Description           string      `json:"description,omitempty"`
	Requirements          []string    `json:"requirements"`
	DemandCode            string      `json:"demand_code,omitempty"`
	SiteName              string      `json:"site_name,omitempty"`
	OnsiteContact         string      `json:"onsite_contact,omitempty"`
	Headcount             int         `json:"headcount"`
	Priority              JobPriority `json:"priority"`
	StartAt               *time.Time  `json:"start_at,omitempty"`
	DeadlineAt            *time.Time  `json:"deadline_at,omitempty"`
	Shift                 string      `json:"shift,omitempty"`
	WorkSchedule          string      `json:"work_schedule,omitempty"`
	CompensationSummary   string      `json:"compensation_summary,omitempty"`
	Accommodation         string      `json:"accommodation,omitempty"`
	TransportRequirements string      `json:"transport_requirements,omitempty"`
	RequiredDocuments     []string    `json:"required_documents"`
}

type DemandRevision struct {
	ID        string            `json:"id"`
	JobID     string            `json:"job_id"`
	Number    uint64            `json:"number"`
	Snapshot  JobDemandSnapshot `json:"snapshot"`
	CreatedBy string            `json:"created_by"`
	Reason    string            `json:"reason"`
	CreatedAt time.Time         `json:"created_at"`
}

type Lead struct {
	ID             string    `json:"id"`
	CandidateID    string    `json:"candidate_id"`
	JobID          string    `json:"job_id"`
	Stage          LeadStage `json:"stage"`
	Owner          string    `json:"owner,omitempty"`
	Version        uint64    `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	StageChangedAt time.Time `json:"stage_changed_at"`
}

type LeadStageEvent struct {
	ID        string    `json:"id"`
	LeadID    string    `json:"lead_id"`
	FromStage LeadStage `json:"from_stage"`
	ToStage   LeadStage `json:"to_stage"`
	Reason    string    `json:"reason,omitempty"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

type FleetCoordination struct {
	Required           bool        `json:"required"`
	PickupAddress      string      `json:"pickup_address,omitempty"`
	PickupAt           *time.Time  `json:"pickup_at,omitempty"`
	Destination        string      `json:"destination,omitempty"`
	LuggageNote        string      `json:"luggage_note,omitempty"`
	VehicleRequirement string      `json:"vehicle_requirement,omitempty"`
	FleetContact       string      `json:"fleet_contact,omitempty"`
	DriverName         string      `json:"driver_name,omitempty"`
	DriverPhone        string      `json:"driver_phone,omitempty"`
	VehiclePlate       string      `json:"vehicle_plate,omitempty"`
	Status             FleetStatus `json:"status"`
	UpdatedAt          time.Time   `json:"updated_at"`
}

type OnboardingCase struct {
	ID                 string            `json:"id"`
	LeadID             string            `json:"lead_id"`
	CandidateID        string            `json:"candidate_id"`
	JobID              string            `json:"job_id"`
	Status             OnboardingStatus  `json:"status"`
	EntryAt            *time.Time        `json:"entry_at,omitempty"`
	EntryAddress       string            `json:"entry_address,omitempty"`
	SiteContact        string            `json:"site_contact,omitempty"`
	RequiredDocuments  []string          `json:"required_documents"`
	CompletedDocuments []string          `json:"completed_documents"`
	MissingInformation []string          `json:"missing_information"`
	Fleet              FleetCoordination `json:"fleet"`
	BlockReason        string            `json:"block_reason,omitempty"`
	Version            uint64            `json:"version"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type ServiceRequest struct {
	ID               string                 `json:"id"`
	CandidateID      string                 `json:"candidate_id"`
	JobID            string                 `json:"job_id,omitempty"`
	OnboardingCaseID string                 `json:"onboarding_case_id,omitempty"`
	SourceEventID    string                 `json:"source_event_id,omitempty"`
	Category         ServiceRequestCategory `json:"category"`
	Priority         JobPriority            `json:"priority"`
	Description      string                 `json:"description"`
	Owner            string                 `json:"owner,omitempty"`
	Status           ServiceRequestStatus   `json:"status"`
	RequiresHuman    bool                   `json:"requires_human"`
	QueueID          string                 `json:"queue_id,omitempty"`
	EnqueuedAt       *time.Time             `json:"enqueued_at,omitempty"`
	ClaimedBy        string                 `json:"claimed_by,omitempty"`
	ClaimedAt        *time.Time             `json:"claimed_at,omitempty"`
	DueAt            *time.Time             `json:"due_at,omitempty"`
	Resolution       string                 `json:"resolution,omitempty"`
	Version          uint64                 `json:"version"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
	ResolvedAt       *time.Time             `json:"resolved_at,omitempty"`
}

type OperationBatchMember struct {
	OnboardingCaseID string `json:"onboarding_case_id"`
	ExpectedVersion  uint64 `json:"expected_version"`
}

type OperationBatchResult struct {
	OnboardingCaseID string                `json:"onboarding_case_id"`
	Outcome          OperationBatchOutcome `json:"outcome"`
	FromStatus       string                `json:"from_status"`
	ToStatus         string                `json:"to_status"`
	ErrorCode        string                `json:"error_code,omitempty"`
	ErrorMessage     string                `json:"error_message,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
}

type OperationBatch struct {
	ID           string                 `json:"id"`
	Kind         OperationBatchKind     `json:"kind"`
	TargetStatus string                 `json:"target_status"`
	Status       OperationBatchStatus   `json:"status"`
	Version      uint64                 `json:"version"`
	Members      []OperationBatchMember `json:"members"`
	Results      []OperationBatchResult `json:"results"`
	CreatedBy    string                 `json:"created_by"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

type ServiceQueue struct {
	ID         string                   `json:"id"`
	Name       string                   `json:"name"`
	Owner      string                   `json:"owner,omitempty"`
	Categories []ServiceRequestCategory `json:"categories"`
	SLASeconds int64                    `json:"sla_seconds"`
	Enabled    bool                     `json:"enabled"`
	Version    uint64                   `json:"version"`
	CreatedAt  time.Time                `json:"created_at"`
	UpdatedAt  time.Time                `json:"updated_at"`
}

type FundCallback struct {
	ID               string             `json:"id"`
	CandidateID      string             `json:"candidate_id"`
	JobID            string             `json:"job_id,omitempty"`
	LeadID           string             `json:"lead_id,omitempty"`
	OnboardingCaseID string             `json:"onboarding_case_id,omitempty"`
	AmountMinor      int64              `json:"amount_minor"`
	Currency         string             `json:"currency"`
	Condition        string             `json:"condition"`
	PromisedBy       string             `json:"promised_by"`
	DueAt            *time.Time         `json:"due_at,omitempty"`
	Evidence         string             `json:"evidence,omitempty"`
	Status           FundCallbackStatus `json:"status"`
	CommitmentBy     string             `json:"commitment_by,omitempty"`
	CommitmentAt     *time.Time         `json:"commitment_at,omitempty"`
	FinalResult      string             `json:"final_result,omitempty"`
	FinalConfirmedBy string             `json:"final_confirmed_by,omitempty"`
	FinalConfirmedAt *time.Time         `json:"final_confirmed_at,omitempty"`
	Version          uint64             `json:"version"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type FundCallbackLedgerEntry struct {
	ID             string             `json:"id"`
	FundCallbackID string             `json:"fund_callback_id"`
	CandidateID    string             `json:"candidate_id,omitempty"`
	Type           string             `json:"type"`
	FromStatus     FundCallbackStatus `json:"from_status,omitempty"`
	ToStatus       FundCallbackStatus `json:"to_status"`
	AmountMinor    int64              `json:"amount_minor"`
	Currency       string             `json:"currency"`
	OperatorID     string             `json:"operator_id"`
	EvidenceSHA256 string             `json:"evidence_sha256"`
	CreatedAt      time.Time          `json:"created_at"`
}

type FundCallbackApprovedActionResult struct {
	ID          string             `json:"id"`
	CandidateID string             `json:"candidate_id"`
	AmountMinor int64              `json:"amount_minor"`
	Currency    string             `json:"currency"`
	Status      FundCallbackStatus `json:"status"`
	Version     uint64             `json:"version"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type FundCallbackApprovedActionBinding struct {
	ActionID             string                            `json:"action_id"`
	PermissionRequestID  string                            `json:"permission_request_id"`
	FundCallbackID       string                            `json:"fund_callback_id"`
	OriginalResult       *FundCallbackApprovedActionResult `json:"original_result,omitempty"`
	OriginalResultSHA256 string                            `json:"original_result_sha256,omitempty"`
}

type CRMAuditEvent struct {
	ID            string    `json:"id"`
	Actor         string    `json:"actor"`
	Operation     string    `json:"operation"`
	EntityType    string    `json:"entity_type,omitempty"`
	EntityID      string    `json:"entity_id,omitempty"`
	SourceEventID string    `json:"source_event_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type FollowUpTask struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Owner       string         `json:"owner,omitempty"`
	Status      FollowUpStatus `json:"status"`
	DueAt       time.Time      `json:"due_at"`
	CandidateID string         `json:"candidate_id,omitempty"`
	LeadID      string         `json:"lead_id,omitempty"`
	Version     uint64         `json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
}

type Note struct {
	ID         string         `json:"id"`
	TargetType NoteTargetType `json:"target_type"`
	TargetID   string         `json:"target_id"`
	Author     string         `json:"author,omitempty"`
	Body       string         `json:"body"`
	CreatedAt  time.Time      `json:"created_at"`
}

type MessageEvent struct {
	ID                             string               `json:"id"`
	MessageKey                     string               `json:"message_key"`
	Source                         string               `json:"source"`
	Mid                            string               `json:"mid,omitempty"`
	ChatType                       ChatType             `json:"chat_type"`
	PeerID                         string               `json:"peer_id"`
	AuthorID                       string               `json:"author_id"`
	GroupID                        string               `json:"group_id,omitempty"`
	Text                           string               `json:"text"`
	Kinds                          []string             `json:"kinds"`
	Timestamp                      int64                `json:"timestamp"`
	Suspicious                     bool                 `json:"suspicious"`
	Disposition                    MessageDisposition   `json:"disposition"`
	RecruitingProjectVersion       uint64               `json:"recruiting_project_version,omitempty"`
	RecruitingRouteSourceVersion   uint64               `json:"recruiting_route_source_version,omitempty"`
	RecruitingConversationKind     string               `json:"recruiting_conversation_kind,omitempty"`
	RecruitingAllowedTools         []AutomationToolName `json:"recruiting_allowed_tools,omitempty"`
	RecruitingOrganizationName     string               `json:"recruiting_organization_name,omitempty"`
	RecruitingRequireAuthoritative bool                 `json:"recruiting_require_authoritative_job,omitempty"`
	RecruitingSourceGroupID        string               `json:"recruiting_source_group_id,omitempty"`
	RecruitingSourceGroupName      string               `json:"recruiting_source_group_name,omitempty"`
	RecruitingSourceGroupVersion   uint64               `json:"recruiting_source_group_version,omitempty"`
	RecruitingSourceGateEnabled    bool                 `json:"recruiting_source_gate_enabled,omitempty"`
	RecruitingSourceGateMode       string               `json:"recruiting_source_gate_mode,omitempty"`
	ReviewStatus                   ReviewStatus         `json:"review_status"`
	CandidateID                    string               `json:"candidate_id,omitempty"`
	Version                        uint64               `json:"version"`
	CreatedAt                      time.Time            `json:"created_at"`
	UpdatedAt                      time.Time            `json:"updated_at"`
}

type IDCounters struct {
	Candidate      uint64 `json:"candidate"`
	Job            uint64 `json:"job"`
	Lead           uint64 `json:"lead"`
	Task           uint64 `json:"task"`
	Note           uint64 `json:"note"`
	Event          uint64 `json:"event"`
	Rule           uint64 `json:"rule"`
	StageEvent     uint64 `json:"stage_event"`
	Onboarding     uint64 `json:"onboarding"`
	ServiceRequest uint64 `json:"service_request"`
	FundCallback   uint64 `json:"fund_callback"`
	DemandRevision uint64 `json:"demand_revision"`
	OperationBatch uint64 `json:"operation_batch"`
	ServiceQueue   uint64 `json:"service_queue"`
	FundLedger     uint64 `json:"fund_ledger"`
	Audit          uint64 `json:"audit"`
	SourceGroup    uint64 `json:"source_group"`
	JobProvenance  uint64 `json:"job_provenance"`
}

type CRMState struct {
	SchemaVersion               int                                 `json:"schema_version"`
	Counters                    IDCounters                          `json:"counters"`
	RecruitingProject           RecruitingProjectConfig             `json:"recruiting_project"`
	Candidates                  []Candidate                         `json:"candidates"`
	Jobs                        []Job                               `json:"jobs"`
	Leads                       []Lead                              `json:"leads"`
	LeadStageEvents             []LeadStageEvent                    `json:"lead_stage_events"`
	OnboardingCases             []OnboardingCase                    `json:"onboarding_cases"`
	ServiceRequests             []ServiceRequest                    `json:"service_requests"`
	FundCallbacks               []FundCallback                      `json:"fund_callbacks"`
	DemandRevisions             []DemandRevision                    `json:"demand_revisions"`
	JobProvenance               []JobProvenance                     `json:"job_provenance"`
	OperationBatches            []OperationBatch                    `json:"operation_batches"`
	ServiceQueues               []ServiceQueue                      `json:"service_queues"`
	FundCallbackLedger          []FundCallbackLedgerEntry           `json:"fund_callback_ledger"`
	FundCallbackApprovedActions []FundCallbackApprovedActionBinding `json:"fund_callback_approved_actions,omitempty"`
	AuditEvents                 []CRMAuditEvent                     `json:"audit_events"`
	Tasks                       []FollowUpTask                      `json:"tasks"`
	Notes                       []Note                              `json:"notes"`
	MessageEvents               []MessageEvent                      `json:"message_events"`
	ForwardingRules             []ForwardRule                       `json:"forwarding_rules"`
}
