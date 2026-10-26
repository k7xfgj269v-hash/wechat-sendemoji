package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const (
	AutomationAuditEventChatPreviewed = "chat.previewed"
	automationTestChatMaxTurns        = 32
	automationTestChatMaxTextRunes    = 4000
)

var automationTestChatCounter atomic.Uint64

type AutomationTestChatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	Text    string `json:"text,omitempty"`
}

type AutomationTestChatRequest struct {
	Text  string                   `json:"text"`
	Turns []AutomationTestChatTurn `json:"turns,omitempty"`
}

type AutomationTestChatToolPreview struct {
	CallID         string                         `json:"call_id"`
	Tool           AutomationToolName             `json:"tool"`
	Confidence     float64                        `json:"confidence"`
	Evidence       []AutomationEvidence           `json:"evidence"`
	Arguments      json.RawMessage                `json:"arguments"`
	Risk           AutomationRisk                 `json:"risk"`
	PolicyDecision AutomationPolicyDecision       `json:"policy_decision"`
	RiskReasons    []string                       `json:"risk_reasons"`
	Recipient      *AutomationRecipient           `json:"recipient,omitempty"`
	Preconditions  []AutomationEntityPrecondition `json:"preconditions"`
	DependsOnCalls []string                       `json:"depends_on_calls"`
}

type AutomationTestChatResponse struct {
	DryRun           bool                            `json:"dry_run"`
	ProviderName     string                          `json:"provider_name"`
	AssistantMessage string                          `json:"assistant_message"`
	ToolPreviews     []AutomationTestChatToolPreview `json:"tool_previews"`
}

type automationTestChatProviderEnvelope struct {
	SchemaVersion    string            `json:"schema_version"`
	AssistantMessage json.RawMessage   `json:"assistant_message"`
	Actions          []json.RawMessage `json:"actions"`
}

func (s *AutomationService) PreviewTestChat(
	ctx context.Context,
	provider AutomationProvider,
	request AutomationTestChatRequest,
) (AutomationTestChatResponse, error) {
	if ctx == nil {
		return AutomationTestChatResponse{}, NewAutomationValidationError("context", "is required")
	}
	if s == nil || s.store == nil || s.risk == nil {
		return AutomationTestChatResponse{}, NewAutomationValidationError("automation_service", "is unavailable")
	}
	if provider == nil {
		return AutomationTestChatResponse{}, NewAutomationValidationError("provider", "is unavailable")
	}

	text, err := automationTestChatContext(request)
	if err != nil {
		return AutomationTestChatResponse{}, err
	}
	now := time.Now().UTC()
	sequence := automationTestChatCounter.Add(1)
	runID := fmt.Sprintf("test_chat_%d_%d", now.UnixNano(), sequence)
	event := MessageEvent{
		ID:           runID + "_event",
		Source:       "automation_test_chat",
		ChatType:     ChatTypePrivate,
		PeerID:       "automation_test_chat_peer",
		AuthorID:     "automation_test_chat_user",
		Text:         text,
		Kinds:        []string{"text"},
		Timestamp:    now.Unix(),
		ReviewStatus: ReviewStatusUnreviewed,
		Version:      1,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	event.MessageKey = MessageKeyForEvent(event)

	input, err := BuildAutomationProviderInput(
		runID,
		event,
		newCRMState(),
		automationAllTools(),
	)
	if err != nil {
		return AutomationTestChatResponse{}, err
	}
	output, err := automationGenerateProviderOutput(ctx, provider, input)
	if err != nil {
		return AutomationTestChatResponse{}, err
	}
	assistantMessage := output.AssistantText
	envelope := AutomationToolEnvelope{
		SchemaVersion:    AutomationToolEnvelopeVersion,
		AssistantMessage: assistantMessage,
		Actions:          output.ToolCalls,
	}
	if err := ValidateAutomationEnvelopeEvidence(envelope, automationEvidenceResolver(input)); err != nil {
		return AutomationTestChatResponse{}, err
	}

	state := s.store.Snapshot()
	source := AutomationSourceMessageRef{
		EventID:         event.ID,
		MessageKey:      event.MessageKey,
		ExpectedVersion: event.Version,
		ChatType:        event.ChatType,
		PeerID:          event.PeerID,
	}
	previews := make([]AutomationTestChatToolPreview, 0, len(envelope.Actions))
	for _, call := range envelope.Actions {
		references, err := AutomationToolCallReferences(call)
		if err != nil {
			return AutomationTestChatResponse{}, err
		}
		preconditions := make([]AutomationEntityPrecondition, 0, len(references))
		dependencies := make([]string, 0, len(references))
		for _, reference := range references {
			if reference.Reference.ID != "" {
				preconditions = append(preconditions, AutomationEntityPrecondition{
					EntityType:      reference.EntityType,
					EntityID:        reference.Reference.ID,
					ExpectedVersion: reference.Reference.ExpectedVersion,
				})
				continue
			}
			dependencies = append(dependencies, reference.Reference.FromCall)
		}
		result := s.risk.Evaluate(state.Policy, AutomationPolicyInput{
			Tool:           call.Tool,
			Confidence:     call.Confidence,
			Payload:        call.Arguments,
			SourceMessage:  source,
			RunActionCount: len(envelope.Actions),
		})
		previews = append(previews, AutomationTestChatToolPreview{
			CallID:         call.CallID,
			Tool:           call.Tool,
			Confidence:     call.Confidence,
			Evidence:       append([]AutomationEvidence{}, call.Evidence...),
			Arguments:      append(json.RawMessage(nil), call.Arguments...),
			Risk:           result.Risk,
			PolicyDecision: result.Decision,
			RiskReasons:    append([]string{}, result.RiskReasons...),
			Recipient:      result.Recipient,
			Preconditions:  preconditions,
			DependsOnCalls: dependencies,
		})
	}
	response := AutomationTestChatResponse{
		DryRun:           true,
		ProviderName:     provider.Name(),
		AssistantMessage: assistantMessage,
		ToolPreviews:     previews,
	}
	auditData, _ := json.Marshal(map[string]any{
		"dry_run":              true,
		"provider_name":        response.ProviderName,
		"assistant_message":    response.AssistantMessage != "",
		"tool_preview_count":   len(response.ToolPreviews),
		"source_message_event": event.ID,
	})
	if err := s.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		_, err := AppendPendingAutomationAudit(state, allocator, AutomationAuditRecord{
			EventType: AutomationAuditEventChatPreviewed,
			ActorType: "operator",
			Data:      sanitizeAutomationJSON(auditData),
			CreatedAt: time.Now().UTC(),
		})
		return err
	}); err != nil {
		return AutomationTestChatResponse{}, err
	}
	eventData, _ := json.Marshal(response)
	s.publishRuntimeEvent(AutomationRuntimeEvent{
		Type: AutomationAuditEventChatPreviewed,
		Data: sanitizeAutomationJSON(eventData),
	})
	return response, nil
}

func automationTestChatContext(request AutomationTestChatRequest) (string, error) {
	text, err := normalizeAutomationString("text", request.Text, automationTestChatMaxTextRunes, true, true)
	if err != nil {
		return "", err
	}
	if len(request.Turns) > automationTestChatMaxTurns {
		return "", NewAutomationValidationError(
			"turns",
			fmt.Sprintf("must contain at most %d turns", automationTestChatMaxTurns),
		)
	}
	if len(request.Turns) == 0 {
		return text, nil
	}

	var context strings.Builder
	context.WriteString("Recent synthetic conversation:\n")
	for i, turn := range request.Turns {
		role := strings.TrimSpace(turn.Role)
		if role != "user" && role != "assistant" {
			return "", NewAutomationValidationError(
				fmt.Sprintf("turns[%d].role", i),
				"must be user or assistant",
			)
		}
		content := strings.TrimSpace(turn.Content)
		alias := strings.TrimSpace(turn.Text)
		if content != "" && alias != "" {
			return "", NewAutomationValidationError(
				fmt.Sprintf("turns[%d]", i),
				"must specify content or text, not both",
			)
		}
		if content == "" {
			content = alias
		}
		content, err = normalizeAutomationString(
			fmt.Sprintf("turns[%d].content", i),
			content,
			automationTestChatMaxTextRunes,
			true,
			true,
		)
		if err != nil {
			return "", err
		}
		context.WriteString(strings.ToUpper(role))
		context.WriteString(": ")
		context.WriteString(content)
		context.WriteByte('\n')
	}
	context.WriteString("CURRENT USER: ")
	context.WriteString(text)
	result := context.String()
	if utf8.RuneCountInString(result) > automationTestChatMaxTextRunes {
		return "", NewAutomationValidationError(
			"turns",
			fmt.Sprintf("combined chat context must be at most %d characters", automationTestChatMaxTextRunes),
		)
	}
	return result, nil
}

func decodeAutomationTestChatProviderEnvelope(data []byte) (string, AutomationToolEnvelope, error) {
	var raw automationTestChatProviderEnvelope
	if err := decodeAutomationJSONObject(
		"provider_response",
		data,
		&raw,
		"schema_version",
		"actions",
	); err != nil {
		return "", AutomationToolEnvelope{}, err
	}

	assistantMessage := ""
	if len(raw.AssistantMessage) > 0 {
		if strings.TrimSpace(string(raw.AssistantMessage)) == "null" {
			return "", AutomationToolEnvelope{}, NewAutomationValidationError(
				"assistant_message",
				"must be a string",
			)
		}
		if err := json.Unmarshal(raw.AssistantMessage, &assistantMessage); err != nil {
			return "", AutomationToolEnvelope{}, NewAutomationValidationError(
				"assistant_message",
				"must be a string",
			)
		}
		var err error
		assistantMessage, err = normalizeAutomationString(
			"assistant_message",
			assistantMessage,
			automationTestChatMaxTextRunes,
			false,
			true,
		)
		if err != nil {
			return "", AutomationToolEnvelope{}, err
		}
	}

	actionsData, err := json.Marshal(automationRawEnvelope{
		SchemaVersion: raw.SchemaVersion,
		Actions:       raw.Actions,
	})
	if err != nil {
		return "", AutomationToolEnvelope{}, err
	}
	envelope, err := DecodeAutomationToolEnvelope(actionsData)
	if err != nil {
		return "", AutomationToolEnvelope{}, err
	}
	return assistantMessage, envelope, nil
}
