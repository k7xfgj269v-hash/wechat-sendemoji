package main

import (
	"encoding/json"
	"errors"
	"strings"
)

func prefixOpenAIAutomationValidationError(field string, err error) error {
	var validationError *AutomationValidationError
	if !errors.As(err, &validationError) {
		return err
	}
	if validationError.Field != "" {
		field += "." + validationError.Field
	}
	return NewAutomationValidationError(field, validationError.Message)
}

func automationOpenAIChatContinuationMessages(
	turns []openAIContinuationTurn,
) []openAIChatMessage {
	messageCount := 0
	for _, turn := range turns {
		messageCount += 1 + len(turn.Calls)
	}
	messages := make([]openAIChatMessage, 0, messageCount)
	for _, turn := range turns {
		toolCalls := make([]openAIChatRequestToolCall, 0, len(turn.Calls))
		for _, call := range turn.Calls {
			toolCalls = append(toolCalls, openAIChatRequestToolCall{
				ID:   call.ID,
				Type: "function",
				Function: openAIChatRequestToolFunction{
					Name:      call.WireTool,
					Arguments: call.Arguments,
				},
			})
		}
		messages = append(messages, openAIChatMessage{
			Role:      "assistant",
			Content:   turn.AssistantText,
			ToolCalls: toolCalls,
		})
		for _, call := range turn.Calls {
			messages = append(messages, openAIChatMessage{
				Role:       "tool",
				Content:    call.Result,
				ToolCallID: call.ID,
			})
		}
	}
	return messages
}

func automationOpenAIChatOutput(
	assistantText string,
	toolCalls []AutomationToolCall,
) (AutomationProviderOutput, error) {
	legacyEnvelope, claimed, err := automationOpenAILegacyEnvelope(assistantText)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	if claimed {
		if len(toolCalls) != 0 {
			return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
		}
		return AutomationProviderOutput{
			AssistantText: legacyEnvelope.AssistantMessage,
			ToolCalls:     legacyEnvelope.Actions,
		}, nil
	}
	return AutomationProviderOutput{AssistantText: assistantText, ToolCalls: toolCalls}, nil
}

func automationOpenAIContentText(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", ErrOpenAIAutomationMalformedResponse
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "output_text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String(), nil
}

func automationOpenAILegacyEnvelope(text string) (AutomationToolEnvelope, bool, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return AutomationToolEnvelope{}, false, nil
	}
	var marker struct {
		SchemaVersion json.RawMessage `json:"schema_version"`
	}
	if err := json.Unmarshal([]byte(text), &marker); err != nil || len(marker.SchemaVersion) == 0 {
		return AutomationToolEnvelope{}, false, nil
	}
	var schemaVersion string
	if err := json.Unmarshal(marker.SchemaVersion, &schemaVersion); err != nil ||
		schemaVersion != AutomationToolEnvelopeVersion {
		return AutomationToolEnvelope{}, true, ErrOpenAIAutomationMalformedResponse
	}
	envelope, err := DecodeAutomationToolEnvelope([]byte(text))
	if err != nil {
		return AutomationToolEnvelope{}, true, ErrOpenAIAutomationMalformedResponse
	}
	return envelope, true, nil
}

func automationOpenAIToolCalls(calls []openAIChatToolCall) ([]AutomationToolCall, error) {
	if len(calls) > AutomationMaxActionsPerRun {
		return nil, ErrOpenAIAutomationResponseTooLarge
	}
	if err := automationOpenAIRequireStableCallIDs(calls); err != nil {
		return nil, err
	}
	result := make([]AutomationToolCall, 0, len(calls))
	for _, call := range calls {
		if call.Type != "" && call.Type != "function" {
			return nil, ErrOpenAIAutomationMalformedResponse
		}
		if automationOpenAIHasArgumentWrapper(
			call.Input,
			call.Params,
			call.Args,
			call.Parameters,
			call.Function.Input,
			call.Function.Params,
			call.Function.Args,
			call.Function.Parameters,
		) {
			return nil, ErrOpenAIAutomationMalformedResponse
		}
		callID := strings.TrimSpace(call.ID)
		arguments, err := automationOpenAIArguments(call.Function.Arguments, false)
		if err != nil {
			return nil, err
		}
		tool, err := automationOpenAIInternalToolName(call.Function.Name)
		if err != nil {
			return nil, err
		}
		result = append(result, AutomationToolCall{
			CallID:     callID,
			Tool:       tool,
			Confidence: 1,
			Evidence:   []AutomationEvidence{},
			Arguments:  json.RawMessage(arguments),
		})
	}
	return result, nil
}

func automationOpenAIArguments(arguments string, required bool) (string, error) {
	arguments = strings.TrimSpace(arguments)
	if len(arguments) > AutomationMaxActionPayloadBytes {
		return "", ErrOpenAIAutomationResponseTooLarge
	}
	if arguments == "" {
		if required {
			return "", ErrOpenAIAutomationMalformedResponse
		}
		return "{}", nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(arguments), &object); err != nil || object == nil {
		return "", ErrOpenAIAutomationMalformedResponse
	}
	for _, wrapper := range []string{"input", "params", "args", "parameters"} {
		if _, exists := object[wrapper]; exists {
			return "", ErrOpenAIAutomationMalformedResponse
		}
	}
	return arguments, nil
}
