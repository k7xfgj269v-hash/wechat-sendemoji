package main

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	workbenchIntrospectionMaxSafeStringRunes = 512

	workbenchIntrospectionSessionTurnsCap    = 16
	workbenchIntrospectionSessionEventsCap   = 32
	workbenchIntrospectionSessionRunsCap     = 16
	workbenchIntrospectionSessionActionsCap  = 32
	workbenchIntrospectionAuditEventsCap     = 32
	workbenchIntrospectionRunActionsCap      = 32
	workbenchIntrospectionActionApprovalsCap = 16
	workbenchIntrospectionPermissionReqsCap  = 16
	workbenchIntrospectionRedactedValue      = "[REDACTED]"
	workbenchIntrospectionProjectionFail     = "automation introspection projection failed"
	workbenchIntrospectionResultBoundingFail = "result exceeds the allowed size"
)

var (
	workbenchIntrospectionJWT = regexp.MustCompile(
		`(?i)(?:^|[^a-z0-9_-])[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}\.[a-z0-9_-]{8,}(?:$|[^a-z0-9_-])`,
	)
	workbenchIntrospectionWindowsPath = regexp.MustCompile(`(?i)(?:^|[\s"'=])[a-z]:[\\/][^\s]+`)
	workbenchIntrospectionHostPath    = regexp.MustCompile(
		`(?i)(?:^|[\s"'=])/(?:[a-z0-9._-]+/)+[^\s"'<>]+`,
	)
	workbenchIntrospectionUNCPath = regexp.MustCompile(`(?i)(?:^|[\s"'=])\\\\[^\\\s]+\\[^\s]+`)
)

type workbenchAutomationSessionProjection struct {
	ID          string                  `json:"id"`
	Kind        AutomationSessionKind   `json:"kind"`
	Title       string                  `json:"title"`
	Status      AutomationSessionStatus `json:"status"`
	Version     uint64                  `json:"version"`
	TurnCount   int                     `json:"turn_count"`
	RunCount    int                     `json:"run_count"`
	ActionCount int                     `json:"action_count"`
	CreatedAt   time.Time               `json:"created_at"`
	UpdatedAt   time.Time               `json:"updated_at"`
}

type workbenchAutomationTurnProjection struct {
	ID                   string               `json:"id"`
	SessionID            string               `json:"session_id"`
	Status               AutomationTurnStatus `json:"status"`
	ProviderName         string               `json:"provider_name"`
	PromptRevisionID     string               `json:"prompt_revision_id,omitempty"`
	Version              uint64               `json:"version"`
	UserTextPresent      bool                 `json:"user_text_present"`
	AssistantTextPresent bool                 `json:"assistant_text_present"`
	CreatedAt            time.Time            `json:"created_at"`
	UpdatedAt            time.Time            `json:"updated_at"`
	StartedAt            *time.Time           `json:"started_at,omitempty"`
	CompletedAt          *time.Time           `json:"completed_at,omitempty"`
}

type workbenchAutomationTurnEventProjection struct {
	Sequence  uint64    `json:"sequence"`
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"created_at"`
}

type workbenchAutomationRunProjection struct {
	ID                     string              `json:"id"`
	SessionID              string              `json:"session_id"`
	TurnID                 string              `json:"turn_id"`
	PromptRevisionID       string              `json:"prompt_revision_id,omitempty"`
	EngineVersion          string              `json:"engine_version"`
	ProviderName           string              `json:"provider_name"`
	SourceMessageEventID   string              `json:"source_message_event_id"`
	Status                 AutomationRunStatus `json:"status"`
	Version                uint64              `json:"version"`
	Attempt                uint32              `json:"attempt"`
	RetryOfRunID           string              `json:"retry_of_run_id,omitempty"`
	FailureStage           string              `json:"failure_stage,omitempty"`
	FailureCode            string              `json:"failure_code,omitempty"`
	ProviderResponseSHA256 string              `json:"provider_response_sha256,omitempty"`
	ActionCount            int                 `json:"action_count"`
	CreatedAt              time.Time           `json:"created_at"`
	StartedAt              *time.Time          `json:"started_at,omitempty"`
	CompletedAt            *time.Time          `json:"completed_at,omitempty"`
}

type workbenchAutomationEntityPreconditionProjection struct {
	EntityType      string `json:"entity_type"`
	EntityID        string `json:"entity_id"`
	ExpectedVersion uint64 `json:"expected_version"`
}

type workbenchAutomationRecipientProjection struct {
	AudienceID      string   `json:"audience_id,omitempty"`
	AudienceVersion uint64   `json:"audience_version,omitempty"`
	ChatType        ChatType `json:"chat_type"`
}

type workbenchAutomationActionProjection struct {
	ID                   string                                            `json:"id"`
	RunID                string                                            `json:"run_id"`
	Tool                 AutomationToolName                                `json:"tool"`
	Operation            WorkbenchOperation                                `json:"operation,omitempty"`
	Status               AutomationActionStatus                            `json:"status"`
	Risk                 AutomationRisk                                    `json:"risk"`
	PolicyDecision       AutomationPolicyDecision                          `json:"policy_decision"`
	PolicyVersion        uint64                                            `json:"policy_version"`
	Confidence           float64                                           `json:"confidence"`
	RiskReasonCount      int                                               `json:"risk_reason_count"`
	EvidenceCount        int                                               `json:"evidence_count"`
	SourceMessageEventID string                                            `json:"source_message_event_id"`
	Preconditions        []workbenchAutomationEntityPreconditionProjection `json:"preconditions"`
	DependsOnActionIDs   []string                                          `json:"depends_on_action_ids"`
	Recipient            *workbenchAutomationRecipientProjection           `json:"recipient,omitempty"`
	ResultID             string                                            `json:"result_id,omitempty"`
	ErrorCode            string                                            `json:"error_code,omitempty"`
	Retryable            bool                                              `json:"retryable"`
	Attempt              uint32                                            `json:"attempt"`
	RetryOfActionID      string                                            `json:"retry_of_action_id,omitempty"`
	ReplacesActionID     string                                            `json:"replaces_action_id,omitempty"`
	ReplacedByActionID   string                                            `json:"replaced_by_action_id,omitempty"`
	Version              uint64                                            `json:"version"`
	CreatedAt            time.Time                                         `json:"created_at"`
	UpdatedAt            time.Time                                         `json:"updated_at"`
}

type workbenchAutomationEntityResultProjection struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Version    uint64 `json:"version"`
}

type workbenchAutomationOutboxResultProjection struct {
	OutboxID string                 `json:"outbox_id"`
	Status   AutomationOutboxStatus `json:"status"`
}

type workbenchAutomationResultProjection struct {
	ID                  string                                     `json:"id"`
	ActionID            string                                     `json:"action_id"`
	PermissionRequestID string                                     `json:"permission_request_id"`
	Status              AutomationDeliveryStatus                   `json:"status"`
	Entity              *workbenchAutomationEntityResultProjection `json:"entity,omitempty"`
	Outbox              *workbenchAutomationOutboxResultProjection `json:"outbox,omitempty"`
	ErrorCode           string                                     `json:"error_code,omitempty"`
	CreatedAt           time.Time                                  `json:"created_at"`
}

type workbenchAutomationApprovalProjection struct {
	ID                  string    `json:"id"`
	ActionID            string    `json:"action_id"`
	ActionVersion       uint64    `json:"action_version"`
	Decision            string    `json:"decision"`
	OperatorID          string    `json:"operator_id"`
	ReplacementActionID string    `json:"replacement_action_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type workbenchAutomationPermissionRequestProjection struct {
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
	RuleID     string                       `json:"rule_id,omitempty"`
	CreatedAt  time.Time                    `json:"created_at"`
	UpdatedAt  time.Time                    `json:"updated_at"`
}

type workbenchAutomationOutboxProjection struct {
	ID                string                 `json:"id"`
	ActionID          string                 `json:"action_id"`
	ChatType          ChatType               `json:"chat_type"`
	Status            AutomationOutboxStatus `json:"status"`
	Attempt           uint32                 `json:"attempt"`
	DispatchAttempted bool                   `json:"dispatch_attempted"`
	ErrorCode         string                 `json:"error_code,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	DispatchStartedAt *time.Time             `json:"dispatch_started_at,omitempty"`
	CompletedAt       *time.Time             `json:"completed_at,omitempty"`
}

type workbenchAutomationAuditEventProjection struct {
	ID        string    `json:"id"`
	EventType string    `json:"event_type"`
	RunID     string    `json:"run_id,omitempty"`
	ActionID  string    `json:"action_id,omitempty"`
	OutboxID  string    `json:"outbox_id,omitempty"`
	ActorType string    `json:"actor_type"`
	ActorID   string    `json:"actor_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type workbenchAutomationSessionListProjection struct {
	Sessions []workbenchAutomationSessionProjection `json:"sessions"`
}

type workbenchAutomationRunListProjection struct {
	Runs []workbenchAutomationRunProjection `json:"runs"`
}

type workbenchAutomationActionListProjection struct {
	Actions []workbenchAutomationActionProjection `json:"actions"`
}

type workbenchAutomationAuditListProjection struct {
	AuditEvents []workbenchAutomationAuditEventProjection `json:"audit_events"`
}

type workbenchAutomationSessionDetailProjection struct {
	Session          workbenchAutomationSessionProjection      `json:"session"`
	TurnsTotal       int                                       `json:"turns_total"`
	Turns            []workbenchAutomationTurnProjection       `json:"turns"`
	TurnEventsTotal  int                                       `json:"turn_events_total"`
	TurnEvents       []workbenchAutomationTurnEventProjection  `json:"turn_events"`
	RunsTotal        int                                       `json:"runs_total"`
	Runs             []workbenchAutomationRunProjection        `json:"runs"`
	ActionsTotal     int                                       `json:"actions_total"`
	Actions          []workbenchAutomationActionProjection     `json:"actions"`
	AuditEventsTotal int                                       `json:"audit_events_total"`
	AuditEvents      []workbenchAutomationAuditEventProjection `json:"audit_events"`
	Truncated        bool                                      `json:"truncated"`
}

type workbenchAutomationRunDetailProjection struct {
	Run              workbenchAutomationRunProjection          `json:"run"`
	ActionsTotal     int                                       `json:"actions_total"`
	Actions          []workbenchAutomationActionProjection     `json:"actions"`
	AuditEventsTotal int                                       `json:"audit_events_total"`
	AuditEvents      []workbenchAutomationAuditEventProjection `json:"audit_events"`
	Truncated        bool                                      `json:"truncated"`
}

type workbenchAutomationActionDetailProjection struct {
	Action                  workbenchAutomationActionProjection              `json:"action"`
	Result                  *workbenchAutomationResultProjection             `json:"result,omitempty"`
	ApprovalsTotal          int                                              `json:"approvals_total"`
	Approvals               []workbenchAutomationApprovalProjection          `json:"approvals"`
	PermissionRequestsTotal int                                              `json:"permission_requests_total"`
	PermissionRequests      []workbenchAutomationPermissionRequestProjection `json:"permission_requests"`
	Outbox                  *workbenchAutomationOutboxProjection             `json:"outbox,omitempty"`
	AuditEventsTotal        int                                              `json:"audit_events_total"`
	AuditEvents             []workbenchAutomationAuditEventProjection        `json:"audit_events"`
	Truncated               bool                                             `json:"truncated"`
}

type workbenchIntrospectionDetailBudget struct {
	Turns              int
	TurnEvents         int
	Runs               int
	Actions            int
	AuditEvents        int
	PermissionRequests int
	Approvals          int
}

type workbenchAutomationRelationshipCounts struct {
	TurnsBySession   map[string]int
	RunsBySession    map[string]int
	ActionsBySession map[string]int
	ActionsByRun     map[string]int
}

func workbenchIntrospectionRelationshipCounts(
	state AutomationState,
) workbenchAutomationRelationshipCounts {
	counts := workbenchAutomationRelationshipCounts{
		TurnsBySession:   make(map[string]int),
		RunsBySession:    make(map[string]int),
		ActionsBySession: make(map[string]int),
		ActionsByRun:     make(map[string]int),
	}
	runSessions := make(map[string]string, len(state.Runs))
	for _, turn := range state.Turns {
		counts.TurnsBySession[turn.SessionID]++
	}
	for _, run := range state.Runs {
		counts.RunsBySession[run.SessionID]++
		runSessions[run.ID] = run.SessionID
	}
	for _, action := range state.Actions {
		counts.ActionsByRun[action.RunID]++
		if sessionID := runSessions[action.RunID]; sessionID != "" {
			counts.ActionsBySession[sessionID]++
		}
	}
	return counts
}

func workbenchProjectAutomationSession(
	state AutomationState,
	session AutomationSession,
) workbenchAutomationSessionProjection {
	return workbenchProjectAutomationSessionWithCounts(
		session,
		workbenchIntrospectionRelationshipCounts(state),
	)
}

func workbenchProjectAutomationSessionWithCounts(
	session AutomationSession,
	counts workbenchAutomationRelationshipCounts,
) workbenchAutomationSessionProjection {
	return workbenchAutomationSessionProjection{
		ID:          session.ID,
		Kind:        session.Kind,
		Title:       session.Title,
		Status:      session.Status,
		Version:     session.Version,
		TurnCount:   counts.TurnsBySession[session.ID],
		RunCount:    counts.RunsBySession[session.ID],
		ActionCount: counts.ActionsBySession[session.ID],
		CreatedAt:   session.CreatedAt,
		UpdatedAt:   session.UpdatedAt,
	}
}

func workbenchProjectAutomationTurn(turn AutomationTurn) workbenchAutomationTurnProjection {
	return workbenchAutomationTurnProjection{
		ID:                   turn.ID,
		SessionID:            turn.SessionID,
		Status:               turn.Status,
		ProviderName:         turn.ProviderName,
		PromptRevisionID:     turn.PromptRevisionID,
		Version:              turn.Version,
		UserTextPresent:      strings.TrimSpace(turn.UserText) != "",
		AssistantTextPresent: strings.TrimSpace(turn.AssistantText) != "",
		CreatedAt:            turn.CreatedAt,
		UpdatedAt:            turn.UpdatedAt,
		StartedAt:            cloneAutomationTimePointer(turn.StartedAt),
		CompletedAt:          cloneAutomationTimePointer(turn.CompletedAt),
	}
}

func workbenchProjectAutomationTurnEvent(
	event AutomationTurnEvent,
) workbenchAutomationTurnEventProjection {
	return workbenchAutomationTurnEventProjection{
		Sequence:  event.Sequence,
		SessionID: event.SessionID,
		TurnID:    event.TurnID,
		Type:      event.Type,
		CreatedAt: event.CreatedAt,
	}
}

func workbenchProjectAutomationRun(
	state AutomationState,
	run AutomationRun,
) workbenchAutomationRunProjection {
	return workbenchProjectAutomationRunWithCounts(
		run,
		workbenchIntrospectionRelationshipCounts(state),
	)
}

func workbenchProjectAutomationRunWithCounts(
	run AutomationRun,
	counts workbenchAutomationRelationshipCounts,
) workbenchAutomationRunProjection {
	return workbenchAutomationRunProjection{
		ID:                     run.ID,
		SessionID:              run.SessionID,
		TurnID:                 run.TurnID,
		PromptRevisionID:       run.PromptRevisionID,
		EngineVersion:          run.EngineVersion,
		ProviderName:           run.ProviderName,
		SourceMessageEventID:   run.SourceMessageEventID,
		Status:                 run.Status,
		Version:                run.Version,
		Attempt:                run.Attempt,
		RetryOfRunID:           run.RetryOfRunID,
		FailureStage:           run.FailureStage,
		FailureCode:            run.FailureCode,
		ProviderResponseSHA256: run.ProviderResponseSHA256,
		ActionCount:            counts.ActionsByRun[run.ID],
		CreatedAt:              run.CreatedAt,
		StartedAt:              cloneAutomationTimePointer(run.StartedAt),
		CompletedAt:            cloneAutomationTimePointer(run.CompletedAt),
	}
}

func workbenchProjectAutomationAction(
	action AutomationAction,
) workbenchAutomationActionProjection {
	preconditions := make([]workbenchAutomationEntityPreconditionProjection, len(action.Preconditions))
	for i, precondition := range action.Preconditions {
		preconditions[i] = workbenchAutomationEntityPreconditionProjection{
			EntityType:      precondition.EntityType,
			EntityID:        precondition.EntityID,
			ExpectedVersion: precondition.ExpectedVersion,
		}
	}
	dependencies := make([]string, len(action.DependsOnActionIDs))
	copy(dependencies, action.DependsOnActionIDs)
	var recipient *workbenchAutomationRecipientProjection
	if action.Recipient != nil {
		recipient = &workbenchAutomationRecipientProjection{
			AudienceID:      action.Recipient.AudienceID,
			AudienceVersion: action.Recipient.AudienceVersion,
			ChatType:        action.Recipient.ChatType,
		}
	}
	return workbenchAutomationActionProjection{
		ID:                   action.ID,
		RunID:                action.RunID,
		Tool:                 action.Tool,
		Operation:            action.Operation,
		Status:               action.Status,
		Risk:                 action.Risk,
		PolicyDecision:       action.PolicyDecision,
		PolicyVersion:        action.PolicyVersion,
		Confidence:           action.Confidence,
		RiskReasonCount:      len(action.RiskReasons),
		EvidenceCount:        len(action.Evidence),
		SourceMessageEventID: action.SourceMessage.EventID,
		Preconditions:        preconditions,
		DependsOnActionIDs:   dependencies,
		Recipient:            recipient,
		ResultID:             action.ResultID,
		ErrorCode:            action.ErrorCode,
		Retryable:            action.Retryable,
		Attempt:              action.Attempt,
		RetryOfActionID:      action.RetryOfActionID,
		ReplacesActionID:     action.ReplacesActionID,
		ReplacedByActionID:   action.ReplacedByActionID,
		Version:              action.Version,
		CreatedAt:            action.CreatedAt,
		UpdatedAt:            action.UpdatedAt,
	}
}

func workbenchProjectAutomationResult(
	result AutomationResult,
) workbenchAutomationResultProjection {
	projected := workbenchAutomationResultProjection{
		ID:                  result.ID,
		ActionID:            result.ActionID,
		PermissionRequestID: result.PermissionRequestID,
		Status:              result.Status,
		ErrorCode:           result.ErrorCode,
		CreatedAt:           result.CreatedAt,
	}
	var decoded AutomationActionResult
	decoder := json.NewDecoder(bytes.NewReader(result.Data))
	decoder.DisallowUnknownFields()
	if len(result.Data) == 0 || decoder.Decode(&decoded) != nil {
		return projected
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return projected
	}
	if decoded.Entity != nil {
		projected.Entity = &workbenchAutomationEntityResultProjection{
			EntityType: decoded.Entity.EntityType,
			EntityID:   decoded.Entity.EntityID,
			Version:    decoded.Entity.Version,
		}
	}
	if decoded.Outbox != nil {
		projected.Outbox = &workbenchAutomationOutboxResultProjection{
			OutboxID: decoded.Outbox.OutboxID,
			Status:   decoded.Outbox.Status,
		}
	}
	return projected
}

func workbenchProjectAutomationApproval(
	approval AutomationApprovalRecord,
) workbenchAutomationApprovalProjection {
	return workbenchAutomationApprovalProjection{
		ID:                  approval.ID,
		ActionID:            approval.ActionID,
		ActionVersion:       approval.ActionVersion,
		Decision:            approval.Decision,
		OperatorID:          approval.OperatorID,
		ReplacementActionID: approval.ReplacementActionID,
		CreatedAt:           approval.CreatedAt,
	}
}

func workbenchProjectAutomationPermissionRequest(
	request AutomationPermissionRequest,
) workbenchAutomationPermissionRequestProjection {
	return workbenchAutomationPermissionRequestProjection{
		ID:         request.ID,
		RunID:      request.RunID,
		ActionID:   request.ActionID,
		Tool:       request.Tool,
		Operation:  request.Operation,
		Risk:       request.Risk,
		Status:     request.Status,
		Version:    request.Version,
		Decision:   request.Decision,
		OperatorID: request.OperatorID,
		RuleID:     request.RuleID,
		CreatedAt:  request.CreatedAt,
		UpdatedAt:  request.UpdatedAt,
	}
}

func workbenchProjectAutomationOutbox(
	outbox AutomationOutboxEntry,
) workbenchAutomationOutboxProjection {
	return workbenchAutomationOutboxProjection{
		ID:                outbox.ID,
		ActionID:          outbox.ActionID,
		ChatType:          automationOutboxChatType(outbox),
		Status:            outbox.Status,
		Attempt:           outbox.Attempt,
		DispatchAttempted: outbox.DispatchAttempted,
		ErrorCode:         outbox.ErrorCode,
		CreatedAt:         outbox.CreatedAt,
		UpdatedAt:         outbox.UpdatedAt,
		DispatchStartedAt: cloneAutomationTimePointer(outbox.DispatchStartedAt),
		CompletedAt:       cloneAutomationTimePointer(outbox.CompletedAt),
	}
}

func workbenchProjectAutomationAudit(
	record AutomationAuditRecord,
) workbenchAutomationAuditEventProjection {
	return workbenchAutomationAuditEventProjection{
		ID:        record.ID,
		EventType: record.EventType,
		RunID:     record.RunID,
		ActionID:  record.ActionID,
		OutboxID:  record.OutboxID,
		ActorType: record.ActorType,
		ActorID:   record.ActorID,
		CreatedAt: record.CreatedAt,
	}
}

func workbenchIntrospectionSanitizedJSON(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	sanitized := workbenchIntrospectionSanitizeValue(decoded)
	if err := workbenchIntrospectionValidateSanitizedValue(sanitized); err != nil {
		return nil, err
	}
	return json.Marshal(sanitized)
}

func workbenchIntrospectionSanitizeValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		sanitized := make(map[string]any, len(typed))
		for key, item := range typed {
			if workbenchIntrospectionForbiddenKey(key) {
				continue
			}
			sanitized[key] = workbenchIntrospectionSanitizeValue(item)
		}
		return sanitized
	case []any:
		sanitized := make([]any, len(typed))
		for i := range typed {
			sanitized[i] = workbenchIntrospectionSanitizeValue(typed[i])
		}
		return sanitized
	case string:
		if workbenchIntrospectionUnsafeString(typed) {
			return workbenchIntrospectionRedactedValue
		}
		return typed
	default:
		return typed
	}
}

func workbenchIntrospectionValidateSanitizedValue(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			if workbenchIntrospectionForbiddenKey(key) {
				return NewAutomationValidationError("projection", "contains a prohibited field")
			}
			if err := workbenchIntrospectionValidateSanitizedValue(item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range typed {
			if err := workbenchIntrospectionValidateSanitizedValue(item); err != nil {
				return err
			}
		}
	case string:
		if typed != workbenchIntrospectionRedactedValue &&
			workbenchIntrospectionUnsafeString(typed) {
			return NewAutomationValidationError("projection", "contains a prohibited value")
		}
	}
	return nil
}

func workbenchIntrospectionForbiddenKey(key string) bool {
	switch key {
	case "user_text_present", "assistant_text_present":
		return false
	}
	value := workbenchIntrospectionAlphanumericLower(key)
	for _, fragment := range []string{
		"authorization",
		"apikey",
		"accesstoken",
		"cookie",
		"credential",
		"password",
		"passwd",
		"privatekey",
		"clientsecret",
		"secret",
		"token",
		"payload",
		"arguments",
		"rawattachment",
		"attachmentbody",
		"attachmentdata",
		"providerpayload",
		"rawdata",
		"filepath",
		"directorypath",
		"socketpath",
		"configpath",
		"hostpath",
		"idempotencykey",
		"sourcemessagekey",
		"peerid",
		"usertext",
		"assistanttext",
		"outboxtext",
	} {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func workbenchIntrospectionAlphanumericLower(value string) string {
	var normalized strings.Builder
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			normalized.WriteRune(character)
		}
	}
	return normalized.String()
}

func workbenchIntrospectionSensitiveValueMarker(value string) bool {
	normalized := workbenchIntrospectionAlphanumericLower(value)
	for _, fragment := range []string{
		"authorization",
		"apikey",
		"accesstoken",
		"cookie",
		"credential",
		"password",
		"passwd",
		"privatekey",
		"clientsecret",
		"secret",
		"token",
		"rawattachment",
		"providerpayload",
	} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func workbenchIntrospectionUnsafeString(value string) bool {
	if value == workbenchIntrospectionRedactedValue {
		return false
	}
	if len([]rune(value)) > workbenchIntrospectionMaxSafeStringRunes {
		return true
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	trimmed := strings.TrimSpace(value)
	lower := strings.ToLower(trimmed)
	if workbenchIntrospectionSensitiveValueMarker(trimmed) {
		return true
	}
	for _, marker := range []string{
		"sk-",
		"ghp_",
		"github_pat_",
		"glpat-",
		"hf_",
		"npm_",
		"rk_live_",
		"sk_live_",
		"xox",
		"akia",
		"asia",
		"aiza",
		"ya29.",
		"-----begin private key-----",
		"-----begin rsa private key-----",
		"-----begin ec private key-----",
		"bearer ",
		"basic ",
		"api_key=",
		"api_key:",
		"api-key=",
		"api-key:",
		"access_token=",
		"access_token:",
		"token=",
		"token:",
		"cookie=",
		"cookie:",
		"password=",
		"password:",
		"passwd=",
		"passwd:",
		"client_secret=",
		"client_secret:",
		"authorization:",
		"authorization=",
		"file://",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	if strings.HasPrefix(lower, "/") ||
		strings.HasPrefix(lower, "~/") ||
		strings.HasPrefix(lower, `\\`) ||
		workbenchIntrospectionWindowsPath.MatchString(trimmed) ||
		workbenchIntrospectionHostPath.MatchString(trimmed) ||
		workbenchIntrospectionUNCPath.MatchString(trimmed) ||
		workbenchIntrospectionJWT.MatchString(trimmed) {
		return true
	}
	return workbenchIntrospectionBase64Like(trimmed)
}

func workbenchIntrospectionBase64Like(value string) bool {
	if len(value) < 160 {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case character == '+', character == '/', character == '=', character == '-', character == '_':
		default:
			return false
		}
	}
	return true
}

func workbenchIntrospectionSuccess(
	operation WorkbenchOperation,
	data any,
	page *WorkbenchPage,
) WorkbenchToolResult {
	sanitized, err := workbenchIntrospectionSanitizedJSON(data)
	if err != nil {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorInternal,
			workbenchIntrospectionProjectionFail,
			false,
		)
	}
	return NewWorkbenchToolSuccess(operation, sanitized, page)
}

func workbenchIntrospectionBoundedListSuccess[T any](
	operation WorkbenchOperation,
	limit int,
	offset int,
	total int,
	items []T,
	project func([]T) any,
) WorkbenchToolResult {
	if len(items) == 0 {
		return workbenchIntrospectionSuccess(
			operation,
			project([]T{}),
			newWorkbenchPage(limit, offset, total, 0),
		)
	}
	var oversized WorkbenchToolResult
	for returned := len(items); returned > 0; returned-- {
		result := workbenchIntrospectionSuccess(
			operation,
			project(items[:returned]),
			newWorkbenchPage(limit, offset, total, returned),
		)
		if result.OK {
			return result
		}
		if result.Error == nil || result.Error.Code != WorkbenchErrorResultTooLarge {
			return result
		}
		oversized = result
	}
	if oversized.Error != nil {
		return oversized
	}
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorResultTooLarge,
		workbenchIntrospectionResultBoundingFail,
		false,
	)
}

func workbenchIntrospectionBoundedDetailSuccess(
	operation WorkbenchOperation,
	budget workbenchIntrospectionDetailBudget,
	project func(workbenchIntrospectionDetailBudget) any,
) WorkbenchToolResult {
	for {
		result := workbenchIntrospectionSuccess(operation, project(budget), nil)
		if result.OK {
			return result
		}
		if result.Error == nil || result.Error.Code != WorkbenchErrorResultTooLarge {
			return result
		}
		switch {
		case budget.TurnEvents > 0:
			budget.TurnEvents--
		case budget.AuditEvents > 0:
			budget.AuditEvents--
		case budget.Actions > 0:
			budget.Actions--
		case budget.Turns > 0:
			budget.Turns--
		case budget.Runs > 0:
			budget.Runs--
		case budget.PermissionRequests > 0:
			budget.PermissionRequests--
		case budget.Approvals > 0:
			budget.Approvals--
		default:
			return result
		}
	}
}

func workbenchIntrospectionPage[T any](items []T, limit int, offset int) []T {
	if offset >= len(items) {
		return []T{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return append([]T(nil), items[offset:end]...)
}

func workbenchIntrospectionNewest[T any](items []T, limit int) []T {
	if limit <= 0 || len(items) == 0 {
		return []T{}
	}
	if limit >= len(items) {
		return append([]T(nil), items...)
	}
	return append([]T(nil), items[len(items)-limit:]...)
}
