package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	AutomationLabelCandidateInterest = "candidate_interest"
	AutomationLabelJobInquiry        = "job_inquiry"
	AutomationLabelAvailability      = "availability"
	AutomationLabelCompensation      = "compensation"
	AutomationLabelInterview         = "interview"
	AutomationLabelOffer             = "offer"
	AutomationLabelRejection         = "rejection"
	AutomationLabelOnboarding        = "onboarding"
	AutomationLabelGeneral           = "general"
	AutomationLabelSpam              = "spam"
	AutomationLabelSuspicious        = "suspicious"
)

const (
	AutomationReplyIntentGeneral                = "general"
	AutomationReplyIntentScheduling             = "scheduling"
	AutomationReplyIntentInformation            = "information"
	AutomationReplyIntentOffer                  = "offer"
	AutomationReplyIntentRejection              = "rejection"
	AutomationReplyIntentHiring                 = "hiring"
	AutomationReplyIntentOnboarding             = "onboarding"
	AutomationReplyIntentCompensationCommitment = "compensation_commitment"
	AutomationReplyIntentLegalCommitment        = "legal_commitment"
	AutomationReplyIntentPaymentRequest         = "payment_request"
	AutomationReplyIntentCredentialRequest      = "credential_request"
)

type AutomationToolEnvelope struct {
	SchemaVersion    string               `json:"schema_version"`
	AssistantMessage string               `json:"assistant_message,omitempty"`
	Actions          []AutomationToolCall `json:"actions"`
}

type AutomationToolCall struct {
	CallID     string               `json:"call_id"`
	Tool       AutomationToolName   `json:"tool"`
	Confidence float64              `json:"confidence"`
	Evidence   []AutomationEvidence `json:"evidence"`
	Arguments  json.RawMessage      `json:"arguments"`
}

type AutomationEntityRef struct {
	ID              string `json:"id,omitempty"`
	ExpectedVersion uint64 `json:"expected_version,omitempty"`
	FromCall        string `json:"from_call,omitempty"`
}

type MessageClassifyArgs struct {
	Labels  []string `json:"labels"`
	Summary string   `json:"summary"`
}

type CandidateCreateArgs struct {
	FullName string   `json:"full_name"`
	Phone    string   `json:"phone,omitempty"`
	Email    string   `json:"email,omitempty"`
	Location string   `json:"location,omitempty"`
	Skills   []string `json:"skills"`
	Owner    string   `json:"owner,omitempty"`
	Summary  string   `json:"summary,omitempty"`
}

type CandidateUpdateArgs struct {
	Candidate AutomationEntityRef  `json:"candidate"`
	Patch     CandidateUpdatePatch `json:"patch"`
}

type CandidateUpdatePatch struct {
	FullName *string          `json:"full_name,omitempty"`
	WeChatID *string          `json:"wechat_id,omitempty"`
	Phone    *string          `json:"phone,omitempty"`
	Email    *string          `json:"email,omitempty"`
	Location *string          `json:"location,omitempty"`
	Source   *string          `json:"source,omitempty"`
	Skills   *[]string        `json:"skills,omitempty"`
	Owner    *string          `json:"owner,omitempty"`
	Status   *CandidateStatus `json:"status,omitempty"`
	Summary  *string          `json:"summary,omitempty"`
}

type CandidateLinkArgs struct {
	Candidate AutomationEntityRef `json:"candidate"`
}

type JobMatchArgs struct {
	Matches []AutomationJobMatch `json:"matches"`
}

type JobCreateArgs struct {
	EventID string   `json:"event_id"`
	GroupID string   `json:"group_id"`
	Job     JobDraft `json:"job"`
}

type AutomationJobMatch struct {
	Job       AutomationEntityRef `json:"job"`
	Score     float64             `json:"score"`
	Rationale string              `json:"rationale"`
}

type LeadCreateArgs struct {
	Candidate AutomationEntityRef `json:"candidate"`
	Job       AutomationEntityRef `json:"job"`
	Owner     string              `json:"owner,omitempty"`
}

type LeadTransitionArgs struct {
	Lead      AutomationEntityRef `json:"lead"`
	FromStage LeadStage           `json:"from_stage"`
	ToStage   LeadStage           `json:"to_stage"`
}

type TaskCreateArgs struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Owner       string              `json:"owner,omitempty"`
	DueAt       time.Time           `json:"due_at"`
	TargetType  string              `json:"target_type"`
	Target      AutomationEntityRef `json:"target"`
}

type NoteCreateArgs struct {
	TargetType NoteTargetType      `json:"target_type"`
	Target     AutomationEntityRef `json:"target"`
	Author     string              `json:"author,omitempty"`
	Body       string              `json:"body"`
}

type ReplyPrivateTextArgs struct {
	Intent string `json:"intent"`
	Text   string `json:"text"`
}

type JobUpdateArgs struct {
	Job   AutomationEntityRef      `json:"job"`
	Patch AutomationJobUpdatePatch `json:"patch"`
}

type AutomationJobUpdatePatch struct {
	Title                 *string      `json:"title,omitempty"`
	Company               *string      `json:"company,omitempty"`
	Location              *string      `json:"location,omitempty"`
	EmploymentType        *string      `json:"employment_type,omitempty"`
	Description           *string      `json:"description,omitempty"`
	Requirements          *[]string    `json:"requirements,omitempty"`
	DemandCode            *string      `json:"demand_code,omitempty"`
	SiteName              *string      `json:"site_name,omitempty"`
	OnsiteContact         *string      `json:"onsite_contact,omitempty"`
	Headcount             *int         `json:"headcount,omitempty"`
	Priority              *JobPriority `json:"priority,omitempty"`
	StartAt               *time.Time   `json:"start_at,omitempty"`
	DeadlineAt            *time.Time   `json:"deadline_at,omitempty"`
	ClearStartAt          bool         `json:"clear_start_at,omitempty"`
	ClearDeadlineAt       bool         `json:"clear_deadline_at,omitempty"`
	Shift                 *string      `json:"shift,omitempty"`
	WorkSchedule          *string      `json:"work_schedule,omitempty"`
	CompensationSummary   *string      `json:"compensation_summary,omitempty"`
	Accommodation         *string      `json:"accommodation,omitempty"`
	TransportRequirements *string      `json:"transport_requirements,omitempty"`
	RequiredDocuments     *[]string    `json:"required_documents,omitempty"`
	Owner                 *string      `json:"owner,omitempty"`
	Status                *JobStatus   `json:"status,omitempty"`
}

type OnboardingCreateArgs struct {
	Lead               AutomationEntityRef `json:"lead"`
	EntryAt            *time.Time          `json:"entry_at,omitempty"`
	EntryAddress       string              `json:"entry_address,omitempty"`
	SiteContact        string              `json:"site_contact,omitempty"`
	RequiredDocuments  []string            `json:"required_documents,omitempty"`
	CompletedDocuments []string            `json:"completed_documents,omitempty"`
	MissingInformation []string            `json:"missing_information,omitempty"`
}

type OnboardingUpdateArgs struct {
	Onboarding AutomationEntityRef             `json:"onboarding"`
	Patch      AutomationOnboardingUpdatePatch `json:"patch"`
}

type AutomationOnboardingUpdatePatch struct {
	EntryAt            *time.Time `json:"entry_at,omitempty"`
	ClearEntryAt       bool       `json:"clear_entry_at,omitempty"`
	EntryAddress       *string    `json:"entry_address,omitempty"`
	SiteContact        *string    `json:"site_contact,omitempty"`
	RequiredDocuments  *[]string  `json:"required_documents,omitempty"`
	CompletedDocuments *[]string  `json:"completed_documents,omitempty"`
	MissingInformation *[]string  `json:"missing_information,omitempty"`
	BlockReason        *string    `json:"block_reason,omitempty"`
}

type FleetUpdateArgs struct {
	Onboarding AutomationEntityRef        `json:"onboarding"`
	Patch      AutomationFleetUpdatePatch `json:"patch"`
}

type AutomationFleetUpdatePatch struct {
	Required           *bool      `json:"required,omitempty"`
	PickupAddress      *string    `json:"pickup_address,omitempty"`
	PickupAt           *time.Time `json:"pickup_at,omitempty"`
	ClearPickupAt      bool       `json:"clear_pickup_at,omitempty"`
	Destination        *string    `json:"destination,omitempty"`
	LuggageNote        *string    `json:"luggage_note,omitempty"`
	VehicleRequirement *string    `json:"vehicle_requirement,omitempty"`
	FleetContact       *string    `json:"fleet_contact,omitempty"`
	DriverName         *string    `json:"driver_name,omitempty"`
	DriverPhone        *string    `json:"driver_phone,omitempty"`
	VehiclePlate       *string    `json:"vehicle_plate,omitempty"`
}

type ServiceRequestCreateArgs struct {
	Candidate     AutomationEntityRef    `json:"candidate"`
	Job           *AutomationEntityRef   `json:"job,omitempty"`
	Onboarding    *AutomationEntityRef   `json:"onboarding,omitempty"`
	Category      ServiceRequestCategory `json:"category"`
	Priority      JobPriority            `json:"priority,omitempty"`
	Description   string                 `json:"description"`
	Owner         string                 `json:"owner,omitempty"`
	RequiresHuman bool                   `json:"requires_human,omitempty"`
	DueAt         *time.Time             `json:"due_at,omitempty"`
}

type ServiceRequestUpdateArgs struct {
	ServiceRequest AutomationEntityRef                 `json:"service_request"`
	Patch          AutomationServiceRequestUpdatePatch `json:"patch"`
}

type AutomationServiceRequestUpdatePatch struct {
	Candidate     *AutomationEntityRef    `json:"candidate,omitempty"`
	Job           *AutomationEntityRef    `json:"job,omitempty"`
	Onboarding    *AutomationEntityRef    `json:"onboarding,omitempty"`
	Category      *ServiceRequestCategory `json:"category,omitempty"`
	Priority      *JobPriority            `json:"priority,omitempty"`
	Description   *string                 `json:"description,omitempty"`
	Owner         *string                 `json:"owner,omitempty"`
	RequiresHuman *bool                   `json:"requires_human,omitempty"`
	DueAt         *time.Time              `json:"due_at,omitempty"`
	ClearDueAt    bool                    `json:"clear_due_at,omitempty"`
	Resolution    *string                 `json:"resolution,omitempty"`
}

type FundCallbackCreateArgs struct {
	Candidate   AutomationEntityRef  `json:"candidate"`
	Job         *AutomationEntityRef `json:"job,omitempty"`
	Lead        *AutomationEntityRef `json:"lead,omitempty"`
	Onboarding  *AutomationEntityRef `json:"onboarding,omitempty"`
	AmountMinor int64                `json:"amount_minor"`
	Currency    string               `json:"currency"`
	Condition   string               `json:"condition"`
	PromisedBy  string               `json:"promised_by"`
	DueAt       *time.Time           `json:"due_at,omitempty"`
	Evidence    string               `json:"evidence,omitempty"`
}

type ReplyAudienceTextArgs struct {
	AudienceID string `json:"audience_id"`
	Intent     string `json:"intent"`
	Text       string `json:"text"`
}

type ForwardEventArgs struct {
	EventID string `json:"event_id"`
	RuleID  string `json:"rule_id"`
	Reason  string `json:"reason,omitempty"`
}

type AutomationToolReference struct {
	Field      string
	EntityType string
	Reference  AutomationEntityRef
}

type AutomationEvidenceTextResolver func(AutomationEvidence) (string, bool)

type automationRawEnvelope struct {
	SchemaVersion    string            `json:"schema_version"`
	AssistantMessage string            `json:"assistant_message,omitempty"`
	Actions          []json.RawMessage `json:"actions"`
}

type automationDecodedToolCall struct {
	field      string
	call       AutomationToolCall
	references []AutomationToolReference
}

type automationToolCallDependency struct {
	callID string
	field  string
}

func DecodeAutomationToolEnvelope(data []byte) (AutomationToolEnvelope, error) {
	if len(data) > AutomationMaxProviderBytes {
		return AutomationToolEnvelope{}, NewAutomationValidationError(
			"provider_response",
			fmt.Sprintf("must be at most %d bytes", AutomationMaxProviderBytes),
		)
	}

	var rawEnvelope automationRawEnvelope
	if err := decodeAutomationJSONObject(
		"provider_response",
		data,
		&rawEnvelope,
		"schema_version",
		"actions",
	); err != nil {
		return AutomationToolEnvelope{}, err
	}
	if rawEnvelope.SchemaVersion != AutomationToolEnvelopeVersion {
		return AutomationToolEnvelope{}, NewAutomationValidationError("schema_version", "unsupported automation tool envelope version")
	}
	if rawEnvelope.Actions == nil {
		return AutomationToolEnvelope{}, NewAutomationValidationError("actions", "must not be null")
	}
	if len(rawEnvelope.Actions) > AutomationMaxActionsPerRun {
		return AutomationToolEnvelope{}, NewAutomationValidationError(
			"actions",
			fmt.Sprintf("must contain at most %d actions", AutomationMaxActionsPerRun),
		)
	}

	envelope := AutomationToolEnvelope{
		SchemaVersion: rawEnvelope.SchemaVersion,
		Actions:       make([]AutomationToolCall, 0, len(rawEnvelope.Actions)),
	}
	assistantMessage, err := normalizeAutomationString(
		"assistant_message",
		rawEnvelope.AssistantMessage,
		4000,
		false,
		true,
	)
	if err != nil {
		return AutomationToolEnvelope{}, err
	}
	envelope.AssistantMessage = assistantMessage
	decodedCalls := make([]automationDecodedToolCall, 0, len(rawEnvelope.Actions))
	for i, rawCall := range rawEnvelope.Actions {
		field := indexedField("actions", i)
		rawCall = automationToolCallWithGeneratedID(rawCall, i)
		call, references, err := decodeAutomationToolCall(field, rawCall)
		if err != nil {
			return AutomationToolEnvelope{}, err
		}
		decodedCalls = append(decodedCalls, automationDecodedToolCall{
			field:      field,
			call:       call,
			references: references,
		})
		envelope.Actions = append(envelope.Actions, call)
	}
	if err := validateAutomationToolCallReferences(decodedCalls); err != nil {
		return AutomationToolEnvelope{}, err
	}
	return envelope, nil
}

func automationToolCallWithGeneratedID(raw json.RawMessage, index int) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return raw
	}
	var marker struct {
		CallID json.RawMessage `json:"call_id"`
	}
	if err := json.Unmarshal(trimmed, &marker); err != nil || len(marker.CallID) != 0 {
		return raw
	}
	callID, err := json.Marshal(fmt.Sprintf("call_%d", index+1))
	if err != nil {
		return raw
	}
	body := bytes.TrimSpace(trimmed[1 : len(trimmed)-1])
	result := make([]byte, 0, len(trimmed)+len(callID)+12)
	result = append(result, `{"call_id":`...)
	result = append(result, callID...)
	if len(body) != 0 {
		result = append(result, ',')
		result = append(result, body...)
	}
	result = append(result, '}')
	return result
}

func NormalizeAutomationProviderOutput(
	output AutomationProviderOutput,
	allowedTools []AutomationToolName,
) (AutomationProviderOutput, error) {
	allowed, err := normalizeAutomationProviderTools(allowedTools)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	allowedSet := make(map[AutomationToolName]struct{}, len(allowed))
	for _, tool := range allowed {
		allowedSet[tool] = struct{}{}
	}
	if len(output.ToolCalls) > AutomationMaxActionsPerRun {
		return AutomationProviderOutput{}, NewAutomationValidationError(
			"tool_calls",
			fmt.Sprintf("must contain at most %d entries", AutomationMaxActionsPerRun),
		)
	}
	output.AssistantText, err = normalizeAutomationString(
		"assistant_text",
		output.AssistantText,
		4000,
		false,
		true,
	)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	if err := validateAutomationProviderUsage("usage", output.Usage); err != nil {
		return AutomationProviderOutput{}, err
	}
	if output.Usage != nil {
		usage := *output.Usage
		if usage.TotalTokens == 0 {
			usage.TotalTokens = usage.InputTokens + usage.OutputTokens
		}
		output.Usage = &usage
	}
	normalized := make([]AutomationToolCall, 0, len(output.ToolCalls))
	decodedCalls := make([]automationDecodedToolCall, 0, len(output.ToolCalls))
	for i, original := range output.ToolCalls {
		if strings.TrimSpace(original.CallID) == "" {
			original.CallID = fmt.Sprintf("call_%d", i+1)
		}
		if original.Evidence == nil {
			original.Evidence = []AutomationEvidence{}
		}
		if _, ok := allowedSet[original.Tool]; !ok {
			return AutomationProviderOutput{}, NewAutomationValidationError(
				indexedField("tool_calls", i)+".tool",
				"is not allowed for this provider request",
			)
		}
		raw, err := json.Marshal(original)
		if err != nil {
			return AutomationProviderOutput{}, NewAutomationValidationError(
				indexedField("tool_calls", i),
				"could not be encoded",
			)
		}
		field := indexedField("tool_calls", i)
		call, references, err := decodeAutomationToolCall(field, raw)
		if err != nil {
			return AutomationProviderOutput{}, err
		}
		decodedCalls = append(decodedCalls, automationDecodedToolCall{
			field:      field,
			call:       call,
			references: references,
		})
		normalized = append(normalized, call)
	}
	if err := validateAutomationToolCallReferences(decodedCalls); err != nil {
		return AutomationProviderOutput{}, err
	}
	output.ToolCalls = normalized
	if output.ToolCalls == nil {
		output.ToolCalls = []AutomationToolCall{}
	}
	return output, nil
}

func validateAutomationToolCallReferences(calls []automationDecodedToolCall) error {
	callTypes := make(map[string]string, len(calls))
	for _, decoded := range calls {
		if _, exists := callTypes[decoded.call.CallID]; exists {
			return NewAutomationValidationError(
				decoded.field+".call_id",
				"duplicates an earlier call_id",
			)
		}
		callTypes[decoded.call.CallID] = AutomationToolOutputEntityType(decoded.call.Tool)
	}

	dependencies := make(map[string][]automationToolCallDependency, len(calls))
	for _, decoded := range calls {
		for _, reference := range decoded.references {
			fromCall := reference.Reference.FromCall
			if fromCall == "" {
				continue
			}
			field := decoded.field + "." + reference.Field + ".from_call"
			outputType, exists := callTypes[fromCall]
			if !exists {
				return NewAutomationValidationError(
					field,
					"must reference a call in the same provider response",
				)
			}
			if outputType != reference.EntityType {
				return NewAutomationValidationError(
					field,
					fmt.Sprintf("does not produce a %s entity", reference.EntityType),
				)
			}
			if fromCall == decoded.call.CallID {
				return NewAutomationValidationError(field, "must not reference its own call_id")
			}
			dependencies[decoded.call.CallID] = append(
				dependencies[decoded.call.CallID],
				automationToolCallDependency{callID: fromCall, field: field},
			)
		}
	}

	states := make(map[string]uint8, len(calls))
	var visit func(string) error
	visit = func(callID string) error {
		states[callID] = 1
		for _, dependency := range dependencies[callID] {
			switch states[dependency.callID] {
			case 1:
				return NewAutomationValidationError(
					dependency.field,
					"must not create a dependency cycle",
				)
			case 0:
				if err := visit(dependency.callID); err != nil {
					return err
				}
			}
		}
		states[callID] = 2
		return nil
	}
	for _, decoded := range calls {
		if states[decoded.call.CallID] == 0 {
			if err := visit(decoded.call.CallID); err != nil {
				return err
			}
		}
	}
	return nil
}
