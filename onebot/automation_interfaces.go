package main

import (
	"context"
	"encoding/json"
	"time"
)

type AutomationMessageContext struct {
	ID           string             `json:"id"`
	MessageKey   string             `json:"message_key"`
	Source       string             `json:"source"`
	Mid          string             `json:"mid,omitempty"`
	ChatType     ChatType           `json:"chat_type"`
	PeerID       string             `json:"peer_id"`
	AuthorID     string             `json:"author_id"`
	GroupID      string             `json:"group_id,omitempty"`
	Text         string             `json:"text"`
	Kinds        []string           `json:"kinds"`
	Timestamp    int64              `json:"timestamp"`
	Suspicious   bool               `json:"suspicious"`
	Disposition  MessageDisposition `json:"disposition"`
	ReviewStatus ReviewStatus       `json:"review_status"`
	CandidateID  string             `json:"candidate_id,omitempty"`
	Version      uint64             `json:"version"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

type AutomationRecruitingContext struct {
	OrganizationName        string                             `json:"organization_name"`
	ConversationKind        string                             `json:"conversation_kind"`
	Disposition             MessageDisposition                 `json:"disposition"`
	SourceGroup             *AutomationRecruitingSourceContext `json:"source_group,omitempty"`
	RequireAuthoritativeJob bool                               `json:"require_authoritative_job"`
}

type AutomationRecruitingSourceContext struct {
	ID          string `json:"id"`
	GroupID     string `json:"group_id"`
	DisplayName string `json:"display_name,omitempty"`
	Version     uint64 `json:"version"`
}

type AutomationCandidateContext struct {
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

type AutomationJobContext struct {
	ID                    string      `json:"id"`
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
	Owner                 string      `json:"owner,omitempty"`
	Status                JobStatus   `json:"status"`
	Version               uint64      `json:"version"`
	CreatedAt             time.Time   `json:"created_at"`
	UpdatedAt             time.Time   `json:"updated_at"`
}

type AutomationLeadContext struct {
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

type AutomationProviderInput struct {
	SchemaVersion     string                         `json:"schema_version"`
	RunID             string                         `json:"run_id"`
	EngineVersion     string                         `json:"engine_version"`
	Recruiting        AutomationRecruitingContext    `json:"recruiting"`
	Message           AutomationMessageContext       `json:"message"`
	Conversation      *AutomationConversationContext `json:"conversation,omitempty"`
	Candidates        []AutomationCandidateContext   `json:"candidates"`
	Jobs              []AutomationJobContext         `json:"jobs"`
	Leads             []AutomationLeadContext        `json:"leads"`
	Onboardings       []OnboardingCase               `json:"onboardings"`
	ServiceRequests   []ServiceRequest               `json:"service_requests"`
	FundCallbacks     []FundCallback                 `json:"fund_callbacks"`
	AllowedTools      []AutomationToolName           `json:"allowed_tools"`
	Continuation      []AutomationProviderMessage    `json:"continuation,omitempty"`
	ContinuationState json.RawMessage                `json:"continuation_state,omitempty"`
}

type AutomationConversationTurn struct {
	TurnID        string `json:"turn_id"`
	UserText      string `json:"user_text"`
	AssistantText string `json:"assistant_text"`
}

type AutomationConversationContext struct {
	Checkpoint  *AutomationChatCheckpoint    `json:"checkpoint,omitempty"`
	RecentTurns []AutomationConversationTurn `json:"recent_turns"`
}

type AutomationContextCompactionRequest struct {
	SchemaVersion    string                        `json:"schema_version"`
	SessionID        string                        `json:"session_id"`
	PromptRevisionID string                        `json:"prompt_revision_id,omitempty"`
	PreviousSummary  *AutomationChatContextSummary `json:"previous_summary,omitempty"`
	Turns            []AutomationConversationTurn  `json:"turns"`
}

type AutomationContextCompactionResult struct {
	Summary AutomationChatContextSummary `json:"summary"`
	Usage   *AutomationProviderUsage     `json:"usage,omitempty"`
}

type AutomationProviderMessageRole string

const (
	AutomationProviderMessageAssistant AutomationProviderMessageRole = "assistant"
	AutomationProviderMessageTool      AutomationProviderMessageRole = "tool"
)

type AutomationProviderMessage struct {
	Role          AutomationProviderMessageRole `json:"role"`
	AssistantText string                        `json:"assistant_text,omitempty"`
	ToolCalls     []AutomationToolCall          `json:"tool_calls,omitempty"`
	ToolCallID    string                        `json:"tool_call_id,omitempty"`
	Tool          AutomationToolName            `json:"tool,omitempty"`
	Result        *WorkbenchToolResult          `json:"result,omitempty"`
}

type AutomationToolDefinition struct {
	Name        AutomationToolName `json:"name"`
	Description string             `json:"description"`
	Parameters  json.RawMessage    `json:"parameters"`
	Strict      bool               `json:"strict"`
}

type AutomationProviderOutput struct {
	AssistantText     string                   `json:"assistant_text"`
	ToolCalls         []AutomationToolCall     `json:"tool_calls"`
	ContinuationState json.RawMessage          `json:"continuation_state,omitempty"`
	Usage             *AutomationProviderUsage `json:"usage,omitempty"`
	RawSHA256         string                   `json:"raw_sha256"`
}

type AutomationProviderEvent struct {
	Type      string                    `json:"type"`
	TextDelta string                    `json:"text_delta,omitempty"`
	ToolCall  *AutomationToolCall       `json:"tool_call,omitempty"`
	Output    *AutomationProviderOutput `json:"output,omitempty"`
}

type AutomationProviderStream interface {
	Recv() (AutomationProviderEvent, error)
	Close() error
}

type AutomationProvider interface {
	Name() string
	Generate(context.Context, AutomationProviderInput) ([]byte, error)
	Stream(context.Context, AutomationProviderInput) (AutomationProviderStream, error)
}

type AutomationContextCompactor interface {
	CompactContext(context.Context, AutomationContextCompactionRequest) (AutomationContextCompactionResult, error)
}

type AutomationRunEngine interface {
	EnsureRun(context.Context, MessageEvent, string) (AutomationRun, bool, error)
	RetryRun(context.Context, string, uint64, string, string) (AutomationRun, error)
	ProcessRun(context.Context, string) error
	RecoverScheduledActions(context.Context) error
	ProcessQueued(context.Context) error
}

type AutomationPolicyInput struct {
	Tool           AutomationToolName         `json:"tool"`
	Confidence     float64                    `json:"confidence"`
	Payload        json.RawMessage            `json:"payload"`
	SourceMessage  AutomationSourceMessageRef `json:"source_message"`
	RunActionCount int                        `json:"run_action_count"`
}

type AutomationPolicyResult struct {
	Risk        AutomationRisk           `json:"risk"`
	Decision    AutomationPolicyDecision `json:"decision"`
	RiskReasons []string                 `json:"risk_reasons"`
	Recipient   *AutomationRecipient     `json:"recipient,omitempty"`
}

type AutomationRiskEvaluator interface {
	Evaluate(AutomationPolicy, AutomationPolicyInput) AutomationPolicyResult
}

type AutomationDeliveryStatus string

const (
	AutomationDeliverySent    AutomationDeliveryStatus = "sent"
	AutomationDeliveryFailed  AutomationDeliveryStatus = "failed"
	AutomationDeliveryUnknown AutomationDeliveryStatus = "unknown"
)

type AutomationDeliveryResult struct {
	Status            AutomationDeliveryStatus `json:"status"`
	DispatchAttempted bool                     `json:"dispatch_attempted"`
	ErrorCode         string                   `json:"error_code,omitempty"`
	ErrorMessage      string                   `json:"error_message,omitempty"`
}

type AutomationEntityResult struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Version    uint64 `json:"version"`
}

type AutomationOutboxResult struct {
	OutboxID string                 `json:"outbox_id"`
	Status   AutomationOutboxStatus `json:"status"`
}

type AutomationActionResult struct {
	Entity *AutomationEntityResult `json:"entity,omitempty"`
	Outbox *AutomationOutboxResult `json:"outbox,omitempty"`
}

type AutomationExecutionResult struct {
	Status       AutomationActionStatus `json:"status"`
	Result       AutomationActionResult `json:"result"`
	ErrorCode    string                 `json:"error_code,omitempty"`
	ErrorMessage string                 `json:"error_message,omitempty"`
	Retryable    bool                   `json:"retryable"`
}

type AutomationCommandExecutor interface {
	Execute(context.Context, AutomationAction) AutomationExecutionResult
}

type WorkbenchToolDispatcher interface {
	Dispatch(context.Context, WorkbenchExecutionContext, WorkbenchDecodedToolRequest) WorkbenchToolResult
}

type AutomationTextSender interface {
	SendPrivateText(context.Context, string, string) AutomationDeliveryResult
	SendText(context.Context, ChatType, string, string) AutomationDeliveryResult
}

type AutomationAudienceResolver interface {
	ResolveAutomationAudience(string) (AutomationAudience, bool)
}

type AutomationOutboxLedger interface {
	Ensure(context.Context, AutomationAction, string, string) (AutomationOutboxEntry, bool, error)
	ClaimPending(context.Context, string) (AutomationOutboxEntry, bool, error)
	CompleteDispatch(context.Context, string, AutomationDeliveryResult) (AutomationOutboxEntry, error)
}

type AutomationCRM interface {
	Snapshot() CRMState
	LinkMessageCandidate(context.Context, MessageCandidateLink) (crmCandidateLinkResult, error)
	UpdateCandidate(string, CandidateUpdate) (Candidate, error)
	CreateLead(LeadCreate) (Lead, error)
	TransitionLead(string, LeadTransition) (Lead, error)
	CreateTask(TaskCreate) (FollowUpTask, error)
	CreateNote(NoteCreate) (Note, error)
}
