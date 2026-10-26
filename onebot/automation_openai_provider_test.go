package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type openAIRoundTripper func(*http.Request) (*http.Response, error)

func (f openAIRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func openAIProviderTestInput(tools ...AutomationToolName) AutomationProviderInput {
	return AutomationProviderInput{
		SchemaVersion: AutomationProviderInputVersion,
		RunID:         "airun_00000001",
		EngineVersion: AutomationEngineVersion,
		AllowedTools:  append([]AutomationToolName{}, tools...),
	}
}

func openAIProviderTestResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func openAIProviderTestContinuation(
	t *testing.T,
) (json.RawMessage, WorkbenchToolResult, []AutomationProviderMessage) {
	t.Helper()
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     WorkbenchOperationCandidatesList,
		ArgumentsJSON: `{"limit":1,"offset":0}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw)
	if err != nil {
		t.Fatal(err)
	}
	result := NewWorkbenchToolSuccess(
		WorkbenchOperationCandidatesList,
		json.RawMessage(`{"items":[]}`),
		nil,
	)
	if err := ValidateWorkbenchToolResult(result); err != nil {
		t.Fatal(err)
	}
	continuation := []AutomationProviderMessage{
		{
			Role:          AutomationProviderMessageAssistant,
			AssistantText: "I checked the candidate list.",
			ToolCalls: []AutomationToolCall{
				{
					CallID:    "call_candidates",
					Tool:      AutomationToolWorkbenchQuery,
					Arguments: append(json.RawMessage(nil), decoded.CanonicalRequest...),
				},
			},
		},
		{
			Role:       AutomationProviderMessageTool,
			ToolCallID: "call_candidates",
			Tool:       AutomationToolWorkbenchQuery,
			Result:     &result,
		},
	}
	return decoded.CanonicalRequest, result, continuation
}

func openAIProviderTestRequestBody(
	t *testing.T,
	protocol AutomationProviderProtocol,
	input AutomationProviderInput,
) []byte {
	t.Helper()
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	})}
	provider, err := NewOpenAIAutomationProviderWithProtocol(
		protocol,
		"https://api.example.test/v1/provider",
		"test-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := provider.requestBody(input, false)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func openAIProviderTestRequestBodyWithPromptCacheKey(
	t *testing.T,
	protocol AutomationProviderProtocol,
	input AutomationProviderInput,
	promptCacheKeyEnabled bool,
) []byte {
	t.Helper()
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	})}
	provider, err := newOpenAICompatibleAutomationProviderWithProtocolAndPromptCacheKey(
		protocol,
		"https://api.example.test/v1/provider",
		"test-model",
		"secret-key",
		DefaultAutomationPromptTemplate,
		client,
		promptCacheKeyEnabled,
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := provider.requestBody(input, false)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestOpenAIAutomationProviderWireToolAliasesAreExactAndReversible(t *testing.T) {
	tests := []struct {
		internal AutomationToolName
		wire     openAIWireToolName
	}{
		{AutomationToolMessageClassify, "message_classify"},
		{AutomationToolCandidateCreate, "candidate_create"},
		{AutomationToolCandidateUpdate, "candidate_update"},
		{AutomationToolCandidateLink, "candidate_link"},
		{AutomationToolJobMatch, "job_match"},
		{AutomationToolJobCreate, "job_create"},
		{AutomationToolLeadCreate, "lead_create"},
		{AutomationToolLeadTransition, "lead_transition"},
		{AutomationToolTaskCreate, "task_create"},
		{AutomationToolNoteCreate, "note_create"},
		{AutomationToolJobUpdate, "job_update"},
		{AutomationToolOnboardingCreate, "onboarding_create"},
		{AutomationToolOnboardingUpdate, "onboarding_update"},
		{AutomationToolFleetUpdate, "onboarding_fleet_update"},
		{AutomationToolServiceCreate, "service_request_create"},
		{AutomationToolServiceUpdate, "service_request_update"},
		{AutomationToolFundCreate, "fund_callback_create"},
		{AutomationToolReplyPrivateText, "reply_private_text"},
		{AutomationToolReplyAudienceText, "reply_audience_text"},
		{AutomationToolForwardEvent, "forward_event"},
		{AutomationToolWorkbenchQuery, "workbench_query"},
		{AutomationToolWorkbenchMutate, "workbench_mutate"},
		{AutomationToolWorkbenchAllowOnce, "workbench_allow_once"},
	}
	seen := make(map[openAIWireToolName]AutomationToolName, len(tests))
	for _, test := range tests {
		wire, err := automationOpenAIWireToolName(test.internal)
		if err != nil || wire != test.wire {
			t.Fatalf("wire alias for %q = %q, %v; want %q", test.internal, wire, err, test.wire)
		}
		if strings.Contains(string(wire), ".") {
			t.Fatalf("wire alias %q contains a dot", wire)
		}
		if previous, exists := seen[wire]; exists {
			t.Fatalf("wire alias %q collides for %q and %q", wire, previous, test.internal)
		}
		seen[wire] = test.internal
		internal, err := automationOpenAIInternalToolName(wire)
		if err != nil || internal != test.internal {
			t.Fatalf("reverse alias for %q = %q, %v; want %q", wire, internal, err, test.internal)
		}
	}
	if _, err := automationOpenAIWireToolName("unknown.tool"); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("unknown internal tool error = %v", err)
	}
	if _, err := automationOpenAIInternalToolName("unknown_alias"); !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
		t.Fatalf("unknown wire alias error = %v", err)
	}
}

func TestOpenAIAutomationProviderRejectsUnknownWireAliases(t *testing.T) {
	chat := &OpenAICompatibleAutomationProvider{protocol: AutomationProviderChatCompletions}
	_, err := chat.parseResponse([]byte(
		`{"choices":[{"message":{"tool_calls":[{"id":"call_unknown","type":"function","function":{"name":"unknown_alias","arguments":"{}"}}]}}]}`,
	))
	if !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
		t.Fatalf("chat parse error = %v", err)
	}

	_, err = automationOpenAIResponsesOutput([]byte(
		`{"status":"completed","output":[{"type":"function_call","call_id":"call_unknown","name":"unknown_alias","arguments":"{}"}]}`,
	))
	if !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
		t.Fatalf("responses parse error = %v", err)
	}

	streamCall := &openAIStreamToolCall{}
	if err := automationOpenAISetStreamToolName(streamCall, "unknown_alias"); !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
		t.Fatalf("stream parse error = %v", err)
	}
}

func TestOpenAIAutomationProviderNoContinuationKeepsLegacyRequestShape(t *testing.T) {
	input := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	wantInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("chat completions", func(t *testing.T) {
		data := openAIProviderTestRequestBody(t, AutomationProviderChatCompletions, input)
		var body openAIChatCompletionRequest
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 2 ||
			body.Messages[0].Role != "system" ||
			body.Messages[1].Role != "user" ||
			body.Messages[1].Content != string(wantInput) ||
			len(body.Tools) != 1 ||
			body.Tools[0].Function.Name != openAIWireToolWorkbenchQuery ||
			body.PromptCacheKey != "" {
			t.Fatalf("messages = %#v", body.Messages)
		}
	})

	t.Run("responses", func(t *testing.T) {
		data := openAIProviderTestRequestBody(t, AutomationProviderResponses, input)
		var body openAIResponsesRequest
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		gotInput, ok := body.Input.(string)
		if !ok || gotInput != string(wantInput) ||
			len(body.Tools) != 1 ||
			body.Tools[0].Name != openAIWireToolWorkbenchQuery ||
			body.PromptCacheKey == "" ||
			len(body.Include) != 1 ||
			body.Include[0] != openAIResponsesEncryptedReasoningInclude {
			t.Fatalf("input = %#v", body.Input)
		}
	})
}

func TestOpenAIAutomationProviderPromptCacheKeyTracksOnlyStaticPrefix(t *testing.T) {
	first := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	first.RunID = "airun_00000001"
	first.Message.Text = "first dynamic message"
	second := first
	second.RunID = "airun_00000002"
	second.Message.Text = "second dynamic message"

	decodeKey := func(input AutomationProviderInput) string {
		t.Helper()
		data := openAIProviderTestRequestBody(t, AutomationProviderResponses, input)
		var body openAIResponsesRequest
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		return body.PromptCacheKey
	}

	firstKey := decodeKey(first)
	secondKey := decodeKey(second)
	if firstKey == "" || secondKey != firstKey {
		t.Fatalf("dynamic input changed cache key: first=%q second=%q", firstKey, secondKey)
	}

	withAnotherTool := second
	withAnotherTool.AllowedTools = []AutomationToolName{
		AutomationToolWorkbenchQuery,
		AutomationToolWorkbenchMutate,
	}
	if key := decodeKey(withAnotherTool); key == firstKey {
		t.Fatalf("tool schema change retained cache key %q", key)
	}

	definitions, err := AutomationToolDefinitions(first.AllowedTools)
	if err != nil {
		t.Fatal(err)
	}
	changedPromptKey, err := automationOpenAIPromptCacheKey(
		AutomationProviderChatCompletions,
		"test-model",
		DefaultAutomationPromptTemplate+" changed",
		definitions,
	)
	if err != nil {
		t.Fatal(err)
	}
	if changedPromptKey == firstKey {
		t.Fatalf("prompt change retained cache key %q", firstKey)
	}
}

func TestOpenAIAutomationProviderChatSerializesContinuation(t *testing.T) {
	arguments, result, continuation := openAIProviderTestContinuation(t)
	input := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	input.Continuation = continuation
	nativeInput := input
	nativeInput.Continuation = nil
	wantInput, err := json.Marshal(nativeInput)
	if err != nil {
		t.Fatal(err)
	}
	wantResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	data := openAIProviderTestRequestBody(t, AutomationProviderChatCompletions, input)
	var body openAIChatCompletionRequest
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 4 || body.Messages[1].Content != string(wantInput) {
		t.Fatalf("messages = %#v", body.Messages)
	}
	assistant := body.Messages[2]
	if assistant.Role != "assistant" ||
		assistant.Content != continuation[0].AssistantText ||
		len(assistant.ToolCalls) != 1 {
		t.Fatalf("assistant continuation = %#v", assistant)
	}
	call := assistant.ToolCalls[0]
	if call.ID != "call_candidates" ||
		call.Type != "function" ||
		call.Function.Name != openAIWireToolWorkbenchQuery ||
		call.Function.Arguments != string(arguments) {
		t.Fatalf("assistant tool call = %#v", call)
	}
	tool := body.Messages[3]
	if tool.Role != "tool" ||
		tool.ToolCallID != "call_candidates" ||
		tool.Content != string(wantResult) ||
		len(tool.ToolCalls) != 0 {
		t.Fatalf("tool continuation = %#v", tool)
	}
}

func TestOpenAIAutomationProviderResponsesSerializesContinuation(t *testing.T) {
	arguments, result, continuation := openAIProviderTestContinuation(t)
	input := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	input.Continuation = continuation
	nativeInput := input
	nativeInput.Continuation = nil
	wantInput, err := json.Marshal(nativeInput)
	if err != nil {
		t.Fatal(err)
	}
	wantResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}

	data := openAIProviderTestRequestBody(t, AutomationProviderResponses, input)
	var body struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) != 3 {
		t.Fatalf("input items = %s", body.Input)
	}
	var user openAIResponsesInputMessage
	if err := json.Unmarshal(body.Input[0], &user); err != nil {
		t.Fatal(err)
	}
	if user.Role != "user" || user.Content != string(wantInput) {
		t.Fatalf("user input = %#v", user)
	}
	var call openAIResponsesFunctionCall
	if err := json.Unmarshal(body.Input[1], &call); err != nil {
		t.Fatal(err)
	}
	if call.Type != "function_call" ||
		call.CallID != "call_candidates" ||
		call.Name != openAIWireToolWorkbenchQuery ||
		call.Arguments != string(arguments) {
		t.Fatalf("function call = %#v", call)
	}
	var output openAIResponsesFunctionCallOutput
	if err := json.Unmarshal(body.Input[2], &output); err != nil {
		t.Fatal(err)
	}
	if output.Type != "function_call_output" ||
		output.CallID != "call_candidates" ||
		output.Output != string(wantResult) {
		t.Fatalf("function call output = %#v", output)
	}
}

func TestOpenAIAutomationProviderResponsesReplaysEncryptedReasoningState(t *testing.T) {
	arguments, result, continuation := openAIProviderTestContinuation(t)
	quotedArguments, err := json.Marshal(string(arguments))
	if err != nil {
		t.Fatal(err)
	}
	responseBody := `{"status":"completed","output":[` +
		`{"id":"rs_candidates","type":"reasoning","encrypted_content":"encrypted-candidates",` +
		`"summary":[{"type":"summary_text","text":"checked candidates"}],"status":"completed"},` +
		`{"type":"function_call","status":"completed","call_id":"call_candidates",` +
		`"name":"workbench_query","arguments":` + string(quotedArguments) + `}]}`

	firstInput := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	client := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
		var body openAIResponsesRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Include) != 1 ||
			body.Include[0] != openAIResponsesEncryptedReasoningInclude ||
			len(body.Tools) != 1 ||
			body.Tools[0].Name != openAIWireToolWorkbenchQuery ||
			body.Store {
			t.Fatalf("responses request = %#v", body)
		}
		return openAIProviderTestResponse(http.StatusOK, responseBody), nil
	})}
	provider, err := NewOpenAIAutomationProviderWithProtocol(
		AutomationProviderResponses,
		"https://api.example.test/v1/responses",
		"responses-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	output, err := provider.GenerateOutput(context.Background(), firstInput)
	if err != nil {
		t.Fatal(err)
	}
	if len(output.ToolCalls) != 1 ||
		output.ToolCalls[0].Tool != AutomationToolWorkbenchQuery ||
		len(output.ContinuationState) == 0 {
		t.Fatalf("output = %#v", output)
	}
	stateItems, err := automationOpenAIResponsesContinuationItems(output.ContinuationState)
	if err != nil || len(stateItems) != 1 {
		t.Fatalf("continuation state = %s, %v", output.ContinuationState, err)
	}

	nextInput := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	nextInput.Continuation = continuation
	nextInput.ContinuationState = append(json.RawMessage(nil), output.ContinuationState...)
	nativeInput := nextInput
	nativeInput.Continuation = nil
	nativeInput.ContinuationState = nil
	wantInput, err := json.Marshal(nativeInput)
	if err != nil {
		t.Fatal(err)
	}
	wantResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	data := openAIProviderTestRequestBody(t, AutomationProviderResponses, nextInput)
	var body struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Input) != 4 {
		t.Fatalf("input items = %s", body.Input)
	}
	var user openAIResponsesInputMessage
	if err := json.Unmarshal(body.Input[0], &user); err != nil {
		t.Fatal(err)
	}
	if user.Role != "user" || user.Content != string(wantInput) {
		t.Fatalf("user input = %#v", user)
	}
	var reasoning openAIResponsesReasoningItem
	if err := json.Unmarshal(body.Input[1], &reasoning); err != nil {
		t.Fatal(err)
	}
	if reasoning.ID != "rs_candidates" ||
		reasoning.Type != "reasoning" ||
		reasoning.EncryptedContent != "encrypted-candidates" ||
		len(reasoning.Summary) != 1 ||
		reasoning.Summary[0].Text != "checked candidates" {
		t.Fatalf("reasoning replay = %#v", reasoning)
	}
	var call openAIResponsesFunctionCall
	if err := json.Unmarshal(body.Input[2], &call); err != nil {
		t.Fatal(err)
	}
	if call.Type != "function_call" ||
		call.CallID != "call_candidates" ||
		call.Name != openAIWireToolWorkbenchQuery ||
		call.Arguments != string(arguments) {
		t.Fatalf("function call = %#v", call)
	}
	var callOutput openAIResponsesFunctionCallOutput
	if err := json.Unmarshal(body.Input[3], &callOutput); err != nil {
		t.Fatal(err)
	}
	if callOutput.Type != "function_call_output" ||
		callOutput.CallID != "call_candidates" ||
		callOutput.Output != string(wantResult) {
		t.Fatalf("function call output = %#v", callOutput)
	}
}

func TestOpenAIAutomationProviderRejectsUnknownAndOversizedContinuationState(t *testing.T) {
	_, _, continuation := openAIProviderTestContinuation(t)
	tests := []struct {
		name  string
		state json.RawMessage
	}{
		{
			name: "unknown state field",
			state: json.RawMessage(
				`{"version":"openai.responses.continuation.v1","items":[],"provider_json":{}}`,
			),
		},
		{
			name: "unknown item type",
			state: json.RawMessage(
				`{"version":"openai.responses.continuation.v1","items":[{"id":"x","type":"message","encrypted_content":"enc"}]}`,
			),
		},
		{
			name: "unknown reasoning field",
			state: json.RawMessage(
				`{"version":"openai.responses.continuation.v1","items":[{"id":"x","type":"reasoning","encrypted_content":"enc","provider_json":{}}]}`,
			),
		},
		{
			name: "nonempty reasoning content",
			state: json.RawMessage(
				`{"version":"openai.responses.continuation.v1","items":[{"id":"x","type":"reasoning","encrypted_content":"enc","content":[{"type":"reasoning_text","text":"secret"}]}]}`,
			),
		},
		{
			name: "oversized",
			state: json.RawMessage(
				`{"version":"openai.responses.continuation.v1","items":[{"id":"x","type":"reasoning","encrypted_content":"` +
					strings.Repeat("x", AutomationMaxProviderBytes) +
					`"}]}`,
			),
		},
	}
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid continuation state reached HTTP dispatch")
		return nil, nil
	})}
	provider, err := NewOpenAIAutomationProviderWithProtocol(
		AutomationProviderResponses,
		"https://api.example.test/v1/responses",
		"responses-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := openAIProviderTestInput(AutomationToolWorkbenchQuery)
			input.Continuation = continuation
			input.ContinuationState = test.state
			if _, err := provider.requestBody(input, false); !errors.Is(err, ErrAutomationValidation) {
				t.Fatalf("requestBody() error = %v", err)
			}
		})
	}

	input := openAIProviderTestInput(AutomationToolWorkbenchQuery)
	input.Continuation = continuation
	input.ContinuationState = json.RawMessage(
		`{"version":"openai.responses.continuation.v1","items":[{"id":"x","type":"reasoning","encrypted_content":"enc"}]}`,
	)
	chatProvider, err := NewOpenAIAutomationProvider(
		"https://api.example.test/v1/chat/completions",
		"chat-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chatProvider.requestBody(input, false); !errors.Is(err, ErrAutomationValidation) {
		t.Fatalf("chat requestBody() error = %v", err)
	}
}

func TestOpenAIAutomationProviderRejectsInvalidContinuationBeforeDispatch(t *testing.T) {
	noncanonicalArguments, err := json.Marshal(WorkbenchToolRequest{
		Operation:     WorkbenchOperationCandidatesList,
		ArgumentsJSON: `{ "limit": 1, "offset": 0 }`,
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		wantField string
		mutate    func([]AutomationProviderMessage) []AutomationProviderMessage
	}{
		{
			name:      "tool before assistant",
			wantField: "continuation[0].role",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[0], messages[1] = messages[1], messages[0]
				return messages
			},
		},
		{
			name:      "missing call id",
			wantField: "continuation[0].tool_calls[0].call_id",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[0].ToolCalls[0].CallID = ""
				return messages
			},
		},
		{
			name:      "duplicate call id",
			wantField: "continuation[0].tool_calls[1].call_id",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[0].ToolCalls = append(messages[0].ToolCalls, messages[0].ToolCalls[0])
				return messages
			},
		},
		{
			name:      "noncanonical arguments",
			wantField: "continuation[0].tool_calls[0].arguments",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[0].ToolCalls[0].Arguments = noncanonicalArguments
				return messages
			},
		},
		{
			name:      "missing matching result",
			wantField: "continuation[0].tool_calls",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				return messages[:1]
			},
		},
		{
			name:      "mismatched result call id",
			wantField: "continuation[1].tool_call_id",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[1].ToolCallID = "call_other"
				return messages
			},
		},
		{
			name:      "duplicate tool result",
			wantField: "continuation[2].tool_call_id",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				second := messages[0].ToolCalls[0]
				second.CallID = "call_candidates_2"
				messages[0].ToolCalls = append(messages[0].ToolCalls, second)
				return append(messages, messages[1])
			},
		},
		{
			name:      "mismatched result tool",
			wantField: "continuation[1].tool",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[1].Tool = AutomationToolWorkbenchMutate
				return messages
			},
		},
		{
			name:      "nil result",
			wantField: "continuation[1].result",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				messages[1].Result = nil
				return messages
			},
		},
		{
			name:      "invalid result",
			wantField: "continuation[1].result.error",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				result := WorkbenchToolResult{
					Operation: WorkbenchOperationCandidatesList,
					OK:        false,
				}
				messages[1].Result = &result
				return messages
			},
		},
		{
			name:      "mismatched result operation",
			wantField: "continuation[1].result.operation",
			mutate: func(messages []AutomationProviderMessage) []AutomationProviderMessage {
				result := NewWorkbenchToolSuccess(
					WorkbenchOperationJobsList,
					json.RawMessage(`{"items":[]}`),
					nil,
				)
				messages[1].Result = &result
				return messages
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, continuation := openAIProviderTestContinuation(t)
			input := openAIProviderTestInput(AutomationToolWorkbenchQuery)
			input.Continuation = test.mutate(continuation)
			client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid continuation reached HTTP dispatch")
				return nil, nil
			})}
			provider, err := NewOpenAIAutomationProvider(
				"https://api.example.test/v1/chat/completions",
				"test-model",
				"secret-key",
				client,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.GenerateOutput(context.Background(), input)
			if !errors.Is(err, ErrAutomationValidation) || !strings.Contains(err.Error(), test.wantField) {
				t.Fatalf("GenerateOutput() error = %v, want validation field %q", err, test.wantField)
			}
		})
	}
}

func TestOpenAIAutomationProviderRequestShapeAndContentForms(t *testing.T) {
	input := openAIProviderTestInput()
	wantInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	wantEnvelope := `{"schema_version":"automation.actions.v1","actions":[]}`

	for _, response := range []string{
		`{"choices":[{"message":{"content":"{\"schema_version\":\"automation.actions.v1\",\"actions\":[]}"}}]}`,
		`{"choices":[{"message":{"content":[{"type":"text","text":"{\"schema_version\":\"automation.actions.v1\","},{"type":"text","text":"\"actions\":[]}"}]}}]}`,
	} {
		client := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost || req.URL.String() != "https://api.example.test/v1/chat/completions" {
				t.Fatalf("request = %s %s", req.Method, req.URL)
			}
			if got := req.Header.Get("Authorization"); got != "Bearer secret-key" {
				t.Fatalf("Authorization = %q", got)
			}
			var body openAIChatCompletionRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Model != "test-model" || len(body.Messages) != 2 {
				t.Fatalf("body = %+v", body)
			}
			if body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, AutomationToolEnvelopeVersion) {
				t.Fatalf("system message = %+v", body.Messages[0])
			}
			if body.Messages[1].Role != "user" || body.Messages[1].Content != string(wantInput) {
				t.Fatalf("user message = %q", body.Messages[1].Content)
			}
			return openAIProviderTestResponse(http.StatusOK, response), nil
		})}
		provider, err := NewOpenAIAutomationProvider("https://api.example.test/v1/chat/completions", "test-model", "secret-key", client)
		if err != nil {
			t.Fatal(err)
		}
		got, err := provider.Generate(context.Background(), input)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != wantEnvelope {
			t.Fatalf("Generate() = %q", got)
		}
	}
}

func TestOpenAIAutomationProviderChatMapsWireAliases(t *testing.T) {
	const arguments = `{"labels":["general"],"summary":"ok"}`
	t.Run("non-streaming", func(t *testing.T) {
		client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
			return openAIProviderTestResponse(
				http.StatusOK,
				`{"choices":[{"message":{"tool_calls":[{"id":"call_chat","type":"function","function":{"name":"message_classify","arguments":"`+
					strings.ReplaceAll(arguments, `"`, `\"`)+`"}}]}}]}`,
			), nil
		})}
		provider, err := NewOpenAIAutomationProvider(
			"https://api.example.test/v1/chat/completions",
			"chat-model",
			"secret-key",
			client,
		)
		if err != nil {
			t.Fatal(err)
		}
		output, err := provider.GenerateOutput(
			context.Background(),
			openAIProviderTestInput(AutomationToolMessageClassify),
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(output.ToolCalls) != 1 ||
			output.ToolCalls[0].CallID != "call_chat" ||
			output.ToolCalls[0].Tool != AutomationToolMessageClassify ||
			string(output.ToolCalls[0].Arguments) != arguments {
			t.Fatalf("output = %#v", output)
		}
	})

	t.Run("streaming", func(t *testing.T) {
		client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
			stream := strings.Join([]string{
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_chat_stream","type":"function","function":{"name":"message_classify","arguments":"` + strings.ReplaceAll(arguments, `"`, `\"`) + `"}}]}}]}`,
				``,
				`data: [DONE]`,
				``,
			}, "\n")
			return openAIProviderTestResponse(http.StatusOK, stream), nil
		})}
		provider, err := NewOpenAIAutomationProvider(
			"https://api.example.test/v1/chat/completions",
			"chat-model",
			"secret-key",
			client,
		)
		if err != nil {
			t.Fatal(err)
		}
		stream, err := provider.Stream(
			context.Background(),
			openAIProviderTestInput(AutomationToolMessageClassify),
		)
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		event, err := stream.Recv()
		if err != nil ||
			event.Type != "tool_call_delta" ||
			event.ToolCall == nil ||
			event.ToolCall.Tool != AutomationToolMessageClassify {
			t.Fatalf("tool call event = %#v, %v", event, err)
		}
		event, err = stream.Recv()
		if err != nil ||
			event.Type != "completed" ||
			event.Output == nil ||
			len(event.Output.ToolCalls) != 1 ||
			event.Output.ToolCalls[0].Tool != AutomationToolMessageClassify {
			t.Fatalf("completed event = %#v, %v", event, err)
		}
	})
}

func TestOpenAIAutomationProviderRejectsUnsafeEndpoint(t *testing.T) {
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected request")
		return nil, nil
	})}
	for _, endpoint := range []string{"", "http://example.com/v1/chat/completions", "ftp://127.0.0.1/x", "https://user:pass@example.com/x"} {
		if _, err := NewOpenAIAutomationProvider(endpoint, "model", "key", client); !errors.Is(err, ErrOpenAIAutomationEndpoint) {
			t.Fatalf("endpoint %q error = %v", endpoint, err)
		}
	}
	if _, err := NewOpenAIAutomationProvider("http://127.0.0.1/v1/chat/completions", "model", "key", client); err != nil {
		t.Fatalf("loopback endpoint: %v", err)
	}
}

func TestOpenAIAutomationProviderResponsesNonStreamingTextAndCalls(t *testing.T) {
	input := openAIProviderTestInput(AutomationToolMessageClassify)
	wantInput, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://api.example.test/v1/responses" {
			t.Fatalf("request = %s %s", req.Method, req.URL)
		}
		if got := req.Header.Get("Authorization"); got != "Bearer secret-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body openAIResponsesRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "responses-model" || body.Instructions != DefaultAutomationPromptTemplate ||
			body.Input != string(wantInput) || body.Stream || body.Store || len(body.Tools) != 1 ||
			body.Tools[0].Type != "function" || body.Tools[0].Name != openAIWireToolMessageClassify ||
			len(body.Include) != 1 || body.Include[0] != openAIResponsesEncryptedReasoningInclude ||
			!body.Tools[0].Strict {
			t.Fatalf("responses request = %#v", body)
		}
		return openAIProviderTestResponse(http.StatusOK, `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ready "} ]},{"type":"function_call","status":"completed","call_id":"call_classify","name":"message_classify","arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}"}]}`), nil
	})}
	provider, err := NewOpenAIAutomationProviderWithProtocol(
		AutomationProviderResponses,
		"https://api.example.test/v1/responses",
		"responses-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	output, err := provider.GenerateOutput(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if output.AssistantText != "ready" || output.RawSHA256 == "" || len(output.ToolCalls) != 1 {
		t.Fatalf("output = %#v", output)
	}
	call := output.ToolCalls[0]
	if call.CallID != "call_classify" || call.Tool != AutomationToolMessageClassify ||
		call.Confidence != 1 || string(call.Arguments) != `{"labels":["general"],"summary":"ok"}` {
		t.Fatalf("tool call = %#v", call)
	}
}

func TestOpenAIAutomationProviderResponsesSSETextAndCalls(t *testing.T) {
	input := openAIProviderTestInput(AutomationToolMessageClassify)
	arguments := `{"labels":["general"],"summary":"stream ok"}`
	client := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Accept"); got != "text/event-stream" {
			t.Fatalf("Accept = %q", got)
		}
		var body openAIResponsesRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Stream || body.Store || body.Model != "responses-model" ||
			len(body.Include) != 1 ||
			body.Include[0] != openAIResponsesEncryptedReasoningInclude {
			t.Fatalf("responses stream request = %#v", body)
		}
		stream := strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"plan "}`,
			``,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_stream"}}`,
			``,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_stream","encrypted_content":"encrypted-stream","summary":[],"status":"completed","content":[]}}`,
			``,
			`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_stream","name":"message_classify"}}`,
			``,
			`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"` + strings.ReplaceAll(arguments, `"`, `\"`) + `"}`,
			``,
			`data: {"type":"response.function_call_arguments.done","output_index":1,"name":"message_classify","arguments":"` + strings.ReplaceAll(arguments, `"`, `\"`) + `"}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		return openAIProviderTestResponse(http.StatusOK, stream), nil
	})}
	provider, err := NewOpenAIAutomationProviderWithProtocol(
		AutomationProviderResponses,
		"https://api.example.test/v1/responses",
		"responses-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	event, err := stream.Recv()
	if err != nil || event.Type != "text_delta" || event.TextDelta != "plan " {
		t.Fatalf("text event = %#v, %v", event, err)
	}
	event, err = stream.Recv()
	if err != nil || event.Type != "tool_call_delta" || event.ToolCall == nil ||
		event.ToolCall.CallID != "call_stream" || event.ToolCall.Tool != AutomationToolMessageClassify {
		t.Fatalf("added event = %#v, %v", event, err)
	}
	event, err = stream.Recv()
	if err != nil || event.Type != "tool_call_delta" || event.ToolCall == nil ||
		string(event.ToolCall.Arguments) != arguments {
		t.Fatalf("arguments event = %#v, %v", event, err)
	}
	event, err = stream.Recv()
	if err != nil || event.Type != "completed" || event.Output == nil {
		t.Fatalf("completed event = %#v, %v", event, err)
	}
	if event.Output.AssistantText != "plan" || event.Output.RawSHA256 == "" ||
		len(event.Output.ToolCalls) != 1 || event.Output.ToolCalls[0].CallID != "call_stream" ||
		event.Output.ToolCalls[0].Tool != AutomationToolMessageClassify ||
		string(event.Output.ToolCalls[0].Arguments) != arguments ||
		len(event.Output.ContinuationState) == 0 {
		t.Fatalf("completed output = %#v", event.Output)
	}
	stateItems, err := automationOpenAIResponsesContinuationItems(event.Output.ContinuationState)
	if err != nil || len(stateItems) != 1 {
		t.Fatalf("continuation state = %s, %v", event.Output.ContinuationState, err)
	}
}

func TestOpenAIAutomationProviderResponsesRequiresUniqueCallIDs(t *testing.T) {
	validArgs := `"{\"labels\":[\"general\"],\"summary\":\"ok\"}"`
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing",
			body: `{"status":"completed","output":[{"type":"function_call","name":"message_classify","arguments":` + validArgs + `}]}`,
		},
		{
			name: "duplicate",
			body: `{"status":"completed","output":[{"type":"function_call","call_id":"same","name":"message_classify","arguments":` + validArgs + `},{"type":"function_call","call_id":"same","name":"message_classify","arguments":` + validArgs + `}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
				return openAIProviderTestResponse(http.StatusOK, test.body), nil
			})}
			provider, err := NewOpenAIAutomationProviderWithProtocol(
				AutomationProviderResponses,
				"https://api.example.test/v1/responses",
				"responses-model",
				"secret-key",
				client,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.GenerateOutput(context.Background(), openAIProviderTestInput(AutomationToolMessageClassify))
			if !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
				t.Fatalf("GenerateOutput() error = %v", err)
			}
		})
	}
}

func TestOpenAIAutomationProviderRejectsArgumentWrappers(t *testing.T) {
	tests := []struct {
		name     string
		protocol AutomationProviderProtocol
		body     string
	}{
		{
			name:     "chat wrapper field",
			protocol: AutomationProviderChatCompletions,
			body:     `{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"message_classify","arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}","parameters":{}}}]}}]}`,
		},
		{
			name:     "responses wrapper field",
			protocol: AutomationProviderResponses,
			body:     `{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"message_classify","input":{},"arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}"}]}`,
		},
		{
			name:     "responses wrapper arguments",
			protocol: AutomationProviderResponses,
			body:     `{"status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"message_classify","arguments":"{\"input\":{\"labels\":[\"general\"],\"summary\":\"ok\"}}"}]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
				return openAIProviderTestResponse(http.StatusOK, test.body), nil
			})}
			provider, err := NewOpenAIAutomationProviderWithProtocol(
				test.protocol,
				"https://api.example.test/v1/provider",
				"model",
				"secret-key",
				client,
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.GenerateOutput(context.Background(), openAIProviderTestInput(AutomationToolMessageClassify))
			if !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
				t.Fatalf("GenerateOutput() error = %v", err)
			}
		})
	}
}

func TestOpenAIAutomationProviderChatRequiresProviderCallIDs(t *testing.T) {
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		return openAIProviderTestResponse(http.StatusOK, `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"message_classify","arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}"}}]}}]}`), nil
	})}
	provider, err := NewOpenAIAutomationProvider(
		"https://api.example.test/v1/chat/completions",
		"chat-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.GenerateOutput(context.Background(), openAIProviderTestInput(AutomationToolMessageClassify))
	if !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
		t.Fatalf("GenerateOutput() error = %v", err)
	}

	streamClient := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		stream := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function","function":{"name":"message_classify","arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}"}}]}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		return openAIProviderTestResponse(http.StatusOK, stream), nil
	})}
	streamProvider, err := NewOpenAIAutomationProvider(
		"https://api.example.test/v1/chat/completions",
		"chat-model",
		"secret-key",
		streamClient,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamProvider.Stream(context.Background(), openAIProviderTestInput(AutomationToolMessageClassify))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		event, err := stream.Recv()
		if errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
			return
		}
		if err != nil {
			t.Fatalf("Stream Recv() error = %v", err)
		}
		if event.Type == "completed" {
			t.Fatalf("stream completed with missing provider call ID: %#v", event.Output)
		}
	}
}

func TestOpenAIAutomationProviderChatSSERejectsArgumentWrappers(t *testing.T) {
	tests := []struct {
		name string
		call string
	}{
		{
			name: "top level wrapper",
			call: `{"index":0,"id":"call_1","type":"function","parameters":{},"function":{"name":"message_classify","arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}"}}`,
		},
		{
			name: "function wrapper",
			call: `{"index":0,"id":"call_1","type":"function","function":{"name":"message_classify","parameters":{},"arguments":"{\"labels\":[\"general\"],\"summary\":\"ok\"}"}}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
				stream := strings.Join([]string{
					`data: {"choices":[{"delta":{"tool_calls":[` + test.call + `]}}]}`,
					``,
					`data: [DONE]`,
					``,
				}, "\n")
				return openAIProviderTestResponse(http.StatusOK, stream), nil
			})}
			provider, err := NewOpenAIAutomationProvider(
				"https://api.example.test/v1/chat/completions",
				"chat-model",
				"secret-key",
				client,
			)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := provider.Stream(context.Background(), openAIProviderTestInput(AutomationToolMessageClassify))
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			_, err = stream.Recv()
			if !errors.Is(err, ErrOpenAIAutomationMalformedResponse) {
				t.Fatalf("Stream Recv() error = %v", err)
			}
		})
	}
}

func TestOpenAIAutomationProviderChatSSELegacyEnvelope(t *testing.T) {
	envelope := `{"schema_version":"automation.actions.v1","assistant_message":"ready","actions":[]}`
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		stream := strings.Join([]string{
			`data: {"choices":[{"delta":{"content":` + strconv.Quote(envelope) + `}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		return openAIProviderTestResponse(http.StatusOK, stream), nil
	})}
	provider, err := NewOpenAIAutomationProvider(
		"https://api.example.test/v1/chat/completions",
		"chat-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(context.Background(), openAIProviderTestInput())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, err := stream.Recv(); err != nil || event.Type != "text_delta" || event.TextDelta != envelope {
		t.Fatalf("text event = %#v, %v", event, err)
	}
	event, err := stream.Recv()
	if err != nil || event.Type != "completed" || event.Output == nil {
		t.Fatalf("completed event = %#v, %v", event, err)
	}
	if event.Output.AssistantText != "ready" || len(event.Output.ToolCalls) != 0 {
		t.Fatalf("completed output = %#v", event.Output)
	}
}

func TestOpenAIAutomationProviderChatSSEAcceptsUsageOnlyChunk(t *testing.T) {
	client := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
		var body openAIChatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.StreamOptions == nil || !body.StreamOptions.IncludeUsage {
			t.Fatalf("stream options = %+v", body.StreamOptions)
		}
		stream := strings.Join([]string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			``,
			`data: {"choices":[],"usage":{"prompt_tokens":17,"completion_tokens":6,"total_tokens":23,"prompt_tokens_details":{"cached_tokens":11}}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		return openAIProviderTestResponse(http.StatusOK, stream), nil
	})}
	provider, err := NewOpenAIAutomationProvider(
		"https://api.example.test/v1/chat/completions",
		"chat-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(context.Background(), openAIProviderTestInput())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if event, err := stream.Recv(); err != nil || event.Type != "text_delta" || event.TextDelta != "Hello" {
		t.Fatalf("text event = %#v, %v", event, err)
	}
	event, err := stream.Recv()
	if err != nil || event.Type != "completed" || event.Output == nil {
		t.Fatalf("completed event = %#v, %v", event, err)
	}
	if event.Output.AssistantText != "Hello" ||
		len(event.Output.ToolCalls) != 0 ||
		event.Output.Usage == nil ||
		event.Output.Usage.InputTokens != 17 ||
		event.Output.Usage.CachedInputTokens != 11 ||
		event.Output.Usage.OutputTokens != 6 ||
		event.Output.Usage.TotalTokens != 23 {
		t.Fatalf("completed output = %#v", event.Output)
	}
}

func TestOpenAIAutomationProviderChatSSEPreservesMultipleToolCallsInChunk(t *testing.T) {
	firstArguments := `{"operation":"candidates.list","arguments_json":"{}"}`
	secondArguments := `{"operation":"jobs.list","arguments_json":"{}"}`
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		stream := strings.Join([]string{
			`data: {"choices":[{"delta":{"tool_calls":[` +
				`{"index":0,"id":"call_candidates","type":"function","function":{"name":"workbench_query","arguments":` +
				strconv.Quote(firstArguments) + `}},` +
				`{"index":1,"id":"call_jobs","type":"function","function":{"name":"workbench_query","arguments":` +
				strconv.Quote(secondArguments) + `}}]}}]}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n")
		return openAIProviderTestResponse(http.StatusOK, stream), nil
	})}
	provider, err := NewOpenAIAutomationProvider(
		"https://api.example.test/v1/chat/completions",
		"chat-model",
		"secret-key",
		client,
	)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := provider.Stream(
		context.Background(),
		openAIProviderTestInput(AutomationToolWorkbenchQuery),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	for index, wantID := range []string{"call_candidates", "call_jobs"} {
		event, err := stream.Recv()
		if err != nil || event.Type != "tool_call_delta" || event.ToolCall == nil {
			t.Fatalf("tool event %d = %#v, %v", index, event, err)
		}
		if event.ToolCall.CallID != wantID {
			t.Fatalf("tool event %d call_id = %q, want %q", index, event.ToolCall.CallID, wantID)
		}
	}
	event, err := stream.Recv()
	if err != nil || event.Type != "completed" || event.Output == nil {
		t.Fatalf("completed event = %#v, %v", event, err)
	}
	if len(event.Output.ToolCalls) != 2 ||
		event.Output.ToolCalls[0].CallID != "call_candidates" ||
		event.Output.ToolCalls[1].CallID != "call_jobs" {
		t.Fatalf("completed tool calls = %#v", event.Output.ToolCalls)
	}
}

func TestOpenAIAutomationProviderErrorsAreBoundedAndRedacted(t *testing.T) {
	const secret = "do-not-leak-this-key"
	tests := []struct {
		name     string
		response *http.Response
		err      error
		wantErr  error
	}{
		{
			name: "status",
			response: &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(secret)),
				Header:     make(http.Header),
			},
			wantErr: ErrOpenAIAutomationHTTPStatus,
		},
		{
			name: "oversize",
			response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", openAIMaxResponseBodyBytes+1))),
				Header:     make(http.Header),
			},
			wantErr: ErrOpenAIAutomationResponseTooLarge,
		},
		{
			name: "malformed",
			response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"choices":[]}`)),
				Header:     make(http.Header),
			},
			wantErr: ErrOpenAIAutomationMalformedResponse,
		},
		{
			name:    "transport",
			err:     errors.New(secret),
			wantErr: ErrOpenAIAutomationRequestFailed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
				return test.response, test.err
			})}
			provider, err := NewOpenAIAutomationProvider("https://api.example.test/v1/chat/completions", "model", secret, client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Generate(context.Background(), AutomationProviderInput{})
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Generate() error = %v, want %v", err, test.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked API key: %v", err)
			}
		})
	}
}

func TestOpenAIAutomationProviderHonorsCancellation(t *testing.T) {
	client := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	provider, err := NewOpenAIAutomationProvider("https://api.example.test/v1/chat/completions", "model", "key", client)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := provider.Generate(ctx, AutomationProviderInput{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Generate() error = %v", err)
	}
}

func TestDynamicAutomationProviderSwapsPromptWithoutRetry(t *testing.T) {
	var firstCalls int
	firstClient := &http.Client{Transport: openAIRoundTripper(func(req *http.Request) (*http.Response, error) {
		firstCalls++
		var body openAIChatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Messages[0].Content != "first prompt" {
			t.Fatalf("first prompt = %q", body.Messages[0].Content)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"{}"}}]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	first, err := newOpenAICompatibleAutomationProvider(
		"https://api.example.test/v1/chat/completions", "model", "key", "first prompt", firstClient,
	)
	if err != nil {
		t.Fatal(err)
	}
	dynamic := NewDynamicAutomationProvider(first)
	if _, err := dynamic.Generate(context.Background(), AutomationProviderInput{}); err != nil {
		t.Fatal(err)
	}
	if firstCalls != 1 {
		t.Fatalf("first provider calls = %d", firstCalls)
	}
	dynamic.Swap(noopAutomationProvider{})
	if dynamic.Name() != "disabled" {
		t.Fatalf("dynamic name = %q", dynamic.Name())
	}
	if got, err := dynamic.Generate(context.Background(), AutomationProviderInput{}); err != nil ||
		string(got) != `{"schema_version":"automation.actions.v1","actions":[]}` {
		t.Fatalf("disabled Generate = %q, %v", got, err)
	}
}

func TestOpenAIAutomationProviderPromptCacheKeySwitchControlsRequestBody(t *testing.T) {
	input := openAIProviderTestInput(AutomationToolWorkbenchQuery)

	t.Run("chat disabled omits prompt_cache_key", func(t *testing.T) {
		data := openAIProviderTestRequestBodyWithPromptCacheKey(
			t, AutomationProviderChatCompletions, input, false,
		)
		if strings.Contains(string(data), "prompt_cache_key") {
			t.Fatalf("chat request body leaked prompt_cache_key: %s", data)
		}
		var body openAIChatCompletionRequest
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if body.PromptCacheKey != "" {
			t.Fatalf("chat PromptCacheKey = %q, want empty", body.PromptCacheKey)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" ||
			body.Messages[1].Role != "user" || len(body.Tools) != 1 {
			t.Fatalf("chat request shape = %#v", body)
		}
	})

	t.Run("chat enabled sends prompt_cache_key", func(t *testing.T) {
		data := openAIProviderTestRequestBodyWithPromptCacheKey(
			t, AutomationProviderChatCompletions, input, true,
		)
		var body openAIChatCompletionRequest
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if body.PromptCacheKey == "" {
			t.Fatalf("chat PromptCacheKey empty with switch enabled: %s", data)
		}
		if !strings.Contains(string(data), `"prompt_cache_key":`) {
			t.Fatalf("chat request body lacks prompt_cache_key field: %s", data)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" ||
			body.Messages[1].Role != "user" || len(body.Tools) != 1 {
			t.Fatalf("chat request shape = %#v", body)
		}
	})

	t.Run("responses always sends prompt_cache_key", func(t *testing.T) {
		disabled := openAIProviderTestRequestBodyWithPromptCacheKey(
			t, AutomationProviderResponses, input, false,
		)
		enabled := openAIProviderTestRequestBodyWithPromptCacheKey(
			t, AutomationProviderResponses, input, true,
		)
		var disabledBody openAIResponsesRequest
		if err := json.Unmarshal(disabled, &disabledBody); err != nil {
			t.Fatal(err)
		}
		var enabledBody openAIResponsesRequest
		if err := json.Unmarshal(enabled, &enabledBody); err != nil {
			t.Fatal(err)
		}
		if disabledBody.PromptCacheKey == "" || enabledBody.PromptCacheKey == "" {
			t.Fatalf("responses PromptCacheKey = %q / %q, want non-empty",
				disabledBody.PromptCacheKey, enabledBody.PromptCacheKey)
		}
		if disabledBody.PromptCacheKey != enabledBody.PromptCacheKey {
			t.Fatalf("responses PromptCacheKey differs by switch: %q vs %q",
				disabledBody.PromptCacheKey, enabledBody.PromptCacheKey)
		}
	})
}
