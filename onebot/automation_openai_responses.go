package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func automationOpenAIContinuation(
	messages []AutomationProviderMessage,
) ([]openAIContinuationTurn, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	turns := make([]openAIContinuationTurn, 0)
	seenCallIDs := make(map[string]struct{})
	for messageIndex := 0; messageIndex < len(messages); {
		messageField := indexedField("continuation", messageIndex)
		assistant := messages[messageIndex]
		if assistant.Role != AutomationProviderMessageAssistant {
			return nil, NewAutomationValidationError(messageField+".role", "must be assistant")
		}
		if assistant.ToolCallID != "" || assistant.Tool != "" || assistant.Result != nil {
			return nil, NewAutomationValidationError(messageField, "assistant messages contain invalid tool result fields")
		}
		if len(assistant.ToolCalls) == 0 {
			return nil, NewAutomationValidationError(messageField+".tool_calls", "must contain at least one tool call")
		}

		turn := openAIContinuationTurn{
			AssistantText: assistant.AssistantText,
			Calls:         make([]openAIContinuationCall, 0, len(assistant.ToolCalls)),
		}
		pending := make(map[string]int, len(assistant.ToolCalls))
		for callIndex, call := range assistant.ToolCalls {
			callField := indexedField(messageField+".tool_calls", callIndex)
			callID, err := automationOpenAIContinuationCallID(callField+".call_id", call.CallID)
			if err != nil {
				return nil, err
			}
			if _, exists := seenCallIDs[callID]; exists {
				return nil, NewAutomationValidationError(callField+".call_id", "duplicates an earlier call_id")
			}
			decoded, err := DecodeWorkbenchToolRequest(call.Tool, call.Arguments)
			if err != nil {
				return nil, prefixOpenAIAutomationValidationError(callField, err)
			}
			if !bytes.Equal(call.Arguments, decoded.CanonicalRequest) {
				return nil, NewAutomationValidationError(callField+".arguments", "must contain canonical workbench arguments")
			}
			wireTool, err := automationOpenAIWireToolName(call.Tool)
			if err != nil {
				return nil, prefixOpenAIAutomationValidationError(callField, err)
			}
			seenCallIDs[callID] = struct{}{}
			pending[callID] = len(turn.Calls)
			turn.Calls = append(turn.Calls, openAIContinuationCall{
				ID:        callID,
				Tool:      call.Tool,
				WireTool:  wireTool,
				Arguments: string(call.Arguments),
				Operation: decoded.Request.Operation,
			})
		}

		messageIndex++
		returned := make(map[string]struct{}, len(turn.Calls))
		for len(returned) < len(turn.Calls) {
			if messageIndex >= len(messages) {
				return nil, NewAutomationValidationError(messageField+".tool_calls", "are missing matching tool results")
			}
			toolField := indexedField("continuation", messageIndex)
			toolMessage := messages[messageIndex]
			if toolMessage.Role != AutomationProviderMessageTool {
				return nil, NewAutomationValidationError(toolField+".role", "must be tool until all calls have results")
			}
			if toolMessage.AssistantText != "" || len(toolMessage.ToolCalls) != 0 {
				return nil, NewAutomationValidationError(toolField, "tool messages contain invalid assistant fields")
			}
			callID, err := automationOpenAIContinuationCallID(toolField+".tool_call_id", toolMessage.ToolCallID)
			if err != nil {
				return nil, err
			}
			if _, exists := returned[callID]; exists {
				return nil, NewAutomationValidationError(toolField+".tool_call_id", "duplicates an earlier tool result")
			}
			callIndex, exists := pending[callID]
			if !exists {
				return nil, NewAutomationValidationError(toolField+".tool_call_id", "does not match a pending tool call")
			}
			call := &turn.Calls[callIndex]
			if toolMessage.Tool != call.Tool {
				return nil, NewAutomationValidationError(toolField+".tool", "does not match the tool call")
			}
			if toolMessage.Result == nil {
				return nil, NewAutomationValidationError(toolField+".result", "is required")
			}
			if err := ValidateWorkbenchToolResult(*toolMessage.Result); err != nil {
				return nil, prefixOpenAIAutomationValidationError(toolField, err)
			}
			if toolMessage.Result.Operation != call.Operation {
				return nil, NewAutomationValidationError(toolField+".result.operation", "does not match the tool call")
			}
			resultJSON, err := json.Marshal(toolMessage.Result)
			if err != nil {
				return nil, NewAutomationValidationError(toolField+".result", "could not be encoded")
			}
			call.Result = string(resultJSON)
			returned[callID] = struct{}{}
			messageIndex++
		}
		turns = append(turns, turn)
	}
	return turns, nil
}

func automationOpenAIContinuationCallID(field, value string) (string, error) {
	normalized, err := normalizeAutomationString(field, value, 80, true, false)
	if err != nil {
		return "", err
	}
	if normalized != value {
		return "", NewAutomationValidationError(field, "must not contain surrounding whitespace")
	}
	return value, nil
}

func automationOpenAIResponsesContinuationItems(
	raw json.RawMessage,
) ([]json.RawMessage, error) {
	if len(raw) > AutomationMaxProviderBytes {
		return nil, NewAutomationValidationError("continuation_state", "is too large")
	}
	var state openAIResponsesContinuationState
	if err := automationOpenAIStrictDecode("continuation_state", raw, &state); err != nil {
		return nil, err
	}
	if state.Version != openAIResponsesContinuationStateVersion {
		return nil, NewAutomationValidationError("continuation_state.version", "is unsupported")
	}
	if len(state.Items) == 0 || len(state.Items) > openAIMaxResponsesContinuationItems {
		return nil, NewAutomationValidationError(
			"continuation_state.items",
			fmt.Sprintf("must contain between 1 and %d entries", openAIMaxResponsesContinuationItems),
		)
	}
	items := make([]json.RawMessage, 0, len(state.Items))
	seenIDs := make(map[string]struct{}, len(state.Items))
	for i, rawItem := range state.Items {
		field := indexedField("continuation_state.items", i)
		item, canonical, err := automationOpenAIResponsesReasoningStateItem(field, rawItem)
		if err != nil {
			return nil, err
		}
		if _, exists := seenIDs[item.ID]; exists {
			return nil, NewAutomationValidationError(field+".id", "duplicates an earlier reasoning item")
		}
		seenIDs[item.ID] = struct{}{}
		items = append(items, canonical)
	}
	return items, nil
}

func automationOpenAIResponsesContinuationStateJSON(
	items []json.RawMessage,
) (json.RawMessage, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > openAIMaxResponsesContinuationItems {
		return nil, ErrOpenAIAutomationResponseTooLarge
	}
	state := openAIResponsesContinuationState{
		Version: openAIResponsesContinuationStateVersion,
		Items:   items,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, ErrOpenAIAutomationMalformedResponse
	}
	if len(data) > AutomationMaxProviderBytes {
		return nil, ErrOpenAIAutomationResponseTooLarge
	}
	return json.RawMessage(data), nil
}

func automationOpenAIResponsesReasoningStateItem(
	field string,
	raw json.RawMessage,
) (openAIResponsesReasoningItem, json.RawMessage, error) {
	if len(raw) > AutomationMaxProviderBytes {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(field, "is too large")
	}
	var item openAIResponsesReasoningItem
	if err := automationOpenAIStrictDecode(field, raw, &item); err != nil {
		return openAIResponsesReasoningItem{}, nil, err
	}
	if item.Type != "reasoning" {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(field+".type", "must be reasoning")
	}
	normalizedID, err := normalizeAutomationString(
		field+".id",
		item.ID,
		openAIMaxResponsesReasoningIDBytes,
		true,
		false,
	)
	if err != nil {
		return openAIResponsesReasoningItem{}, nil, err
	}
	if normalizedID != item.ID {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
			field+".id",
			"must not contain surrounding whitespace",
		)
	}
	if item.EncryptedContent == "" ||
		len(item.EncryptedContent) > AutomationMaxProviderBytes ||
		strings.TrimSpace(item.EncryptedContent) != item.EncryptedContent {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
			field+".encrypted_content",
			"must contain bounded encrypted reasoning content without surrounding whitespace",
		)
	}
	if len(item.Summary) > openAIMaxResponsesReasoningSummaryItems {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
			field+".summary",
			fmt.Sprintf("must contain at most %d entries", openAIMaxResponsesReasoningSummaryItems),
		)
	}
	if len(item.Content) != 0 {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
			field+".content",
			"must be empty",
		)
	}
	for i, part := range item.Summary {
		partField := indexedField(field+".summary", i)
		if part.Type != "summary_text" {
			return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
				partField+".type",
				"must be summary_text",
			)
		}
		if len(part.Text) > openAIMaxResponsesReasoningSummaryTextBytes {
			return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
				partField+".text",
				"is too large",
			)
		}
	}
	switch item.Status {
	case "", "in_progress", "completed", "incomplete":
	default:
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(
			field+".status",
			"is invalid",
		)
	}
	canonical, err := json.Marshal(item)
	if err != nil {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(field, "could not be encoded")
	}
	if len(canonical) > AutomationMaxProviderBytes {
		return openAIResponsesReasoningItem{}, nil, NewAutomationValidationError(field, "is too large")
	}
	return item, json.RawMessage(canonical), nil
}

func automationOpenAIStrictDecode(field string, data []byte, destination any) error {
	if err := rejectAutomationDuplicateJSONKeys(field, data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return NewAutomationValidationError(field, "must contain strict JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return NewAutomationValidationError(field, "must contain exactly one JSON value")
	}
	return nil
}

func automationOpenAIResponsesContinuationInput(
	inputJSON []byte,
	stateItems []json.RawMessage,
	turns []openAIContinuationTurn,
) []any {
	itemCount := 1 + len(stateItems)
	for _, turn := range turns {
		itemCount += 2 * len(turn.Calls)
	}
	items := make([]any, 0, itemCount)
	items = append(items, openAIResponsesInputMessage{
		Role:    "user",
		Content: string(inputJSON),
	})
	for _, item := range stateItems {
		items = append(items, append(json.RawMessage(nil), item...))
	}
	for _, turn := range turns {
		for _, call := range turn.Calls {
			items = append(items, openAIResponsesFunctionCall{
				Type:      "function_call",
				CallID:    call.ID,
				Name:      call.WireTool,
				Arguments: call.Arguments,
			})
		}
		for _, call := range turn.Calls {
			items = append(items, openAIResponsesFunctionCallOutput{
				Type:   "function_call_output",
				CallID: call.ID,
				Output: call.Result,
			})
		}
	}
	return items
}

func (p *OpenAICompatibleAutomationProvider) parseResponse(data []byte) (AutomationProviderOutput, error) {
	if p.protocol == AutomationProviderResponses {
		return automationOpenAIResponsesOutput(data)
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content   json.RawMessage      `json:"content"`
				ToolCalls []openAIChatToolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || len(envelope.Choices) == 0 {
		return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
	}
	assistantText, err := automationOpenAIContentText(envelope.Choices[0].Message.Content)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	toolCalls, err := automationOpenAIToolCalls(envelope.Choices[0].Message.ToolCalls)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	output, err := automationOpenAIChatOutput(assistantText, toolCalls)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	output.Usage, err = automationOpenAIProviderUsage(envelope.Usage)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	return output, nil
}

func automationOpenAIResponsesOutput(data []byte) (AutomationProviderOutput, error) {
	var response struct {
		Status string          `json:"status"`
		Output json.RawMessage `json:"output"`
		Usage  json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(data, &response); err != nil || len(response.Output) == 0 {
		return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
	}
	if response.Status != "" && response.Status != "completed" {
		return AutomationProviderOutput{}, ErrOpenAIAutomationRequestFailed
	}
	var outputItems []json.RawMessage
	if err := json.Unmarshal(response.Output, &outputItems); err != nil || outputItems == nil {
		return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
	}
	var assistant strings.Builder
	toolCalls := make([]AutomationToolCall, 0)
	seenCallIDs := make(map[string]struct{})
	reasoningItems := make([]json.RawMessage, 0)
	sawFunctionCall := false
	for _, rawItem := range outputItems {
		var item struct {
			Type       string             `json:"type"`
			Status     string             `json:"status"`
			CallID     string             `json:"call_id"`
			Name       openAIWireToolName `json:"name"`
			Arguments  *string            `json:"arguments"`
			Input      json.RawMessage    `json:"input"`
			Params     json.RawMessage    `json:"params"`
			Args       json.RawMessage    `json:"args"`
			Parameters json.RawMessage    `json:"parameters"`
			Content    []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
		}
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				switch part.Type {
				case "output_text":
					assistant.WriteString(part.Text)
				case "refusal":
				default:
					return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
				}
			}
		case "function_call":
			sawFunctionCall = true
			if item.Status != "" && item.Status != "completed" {
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			if automationOpenAIHasArgumentWrapper(item.Input, item.Params, item.Args, item.Parameters) ||
				item.Arguments == nil {
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			callID := strings.TrimSpace(item.CallID)
			if callID == "" {
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			if _, exists := seenCallIDs[callID]; exists {
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			arguments, err := automationOpenAIArguments(*item.Arguments, true)
			if err != nil {
				return AutomationProviderOutput{}, err
			}
			tool, err := automationOpenAIInternalToolName(item.Name)
			if err != nil {
				return AutomationProviderOutput{}, err
			}
			seenCallIDs[callID] = struct{}{}
			toolCalls = append(toolCalls, AutomationToolCall{
				CallID:     callID,
				Tool:       tool,
				Confidence: 1,
				Evidence:   []AutomationEvidence{},
				Arguments:  json.RawMessage(arguments),
			})
		case "reasoning":
			if sawFunctionCall || len(reasoningItems) >= openAIMaxResponsesContinuationItems {
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			reasoningItems = append(reasoningItems, append(json.RawMessage(nil), rawItem...))
		default:
			return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
		}
	}
	var continuationState json.RawMessage
	if len(toolCalls) != 0 && len(reasoningItems) != 0 {
		canonicalItems := make([]json.RawMessage, 0, len(reasoningItems))
		seenReasoningIDs := make(map[string]struct{}, len(reasoningItems))
		for i, rawItem := range reasoningItems {
			item, canonical, err := automationOpenAIResponsesReasoningStateItem(
				indexedField("response.output.reasoning", i),
				rawItem,
			)
			if err != nil {
				if len(rawItem) > AutomationMaxProviderBytes {
					return AutomationProviderOutput{}, ErrOpenAIAutomationResponseTooLarge
				}
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			if _, exists := seenReasoningIDs[item.ID]; exists {
				return AutomationProviderOutput{}, ErrOpenAIAutomationMalformedResponse
			}
			seenReasoningIDs[item.ID] = struct{}{}
			canonicalItems = append(canonicalItems, canonical)
		}
		var err error
		continuationState, err = automationOpenAIResponsesContinuationStateJSON(canonicalItems)
		if err != nil {
			return AutomationProviderOutput{}, err
		}
	}
	usage, err := automationOpenAIProviderUsage(response.Usage)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	return AutomationProviderOutput{
		AssistantText:     assistant.String(),
		ToolCalls:         toolCalls,
		ContinuationState: continuationState,
		Usage:             usage,
	}, nil
}

func automationOpenAIHasArgumentWrapper(values ...json.RawMessage) bool {
	for _, value := range values {
		if len(value) != 0 {
			return true
		}
	}
	return false
}
