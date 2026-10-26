package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	openAIMaxResponseBodyBytes                  = 2*AutomationMaxProviderBytes + 64<<10
	openAIPromptCacheKeyVersion                 = "lingxi.prompt-cache.v1"
	openAIResponsesContinuationStateVersion     = "openai.responses.continuation.v1"
	openAIResponsesEncryptedReasoningInclude    = "reasoning.encrypted_content"
	openAIMaxResponsesContinuationItems         = AutomationMaxActionsPerRun
	openAIMaxResponsesReasoningIDBytes          = 200
	openAIMaxResponsesReasoningSummaryItems     = 16
	openAIMaxResponsesReasoningSummaryTextBytes = 4000
)

var (
	ErrOpenAIAutomationEndpoint          = errors.New("openai automation provider endpoint must use https or loopback http")
	ErrOpenAIAutomationConfiguration     = errors.New("openai automation provider configuration is invalid")
	ErrOpenAIAutomationRequestFailed     = errors.New("openai automation provider request failed")
	ErrOpenAIAutomationHTTPStatus        = errors.New("openai automation provider returned a non-success status")
	ErrOpenAIAutomationResponseTooLarge  = errors.New("openai automation provider response is too large")
	ErrOpenAIAutomationMalformedResponse = errors.New("openai automation provider returned a malformed response")
)

type openAIChatCompletionRequest struct {
	Model          string                   `json:"model"`
	Messages       []openAIChatMessage      `json:"messages"`
	Tools          []openAIChatTool         `json:"tools,omitempty"`
	PromptCacheKey string                   `json:"prompt_cache_key,omitempty"`
	Stream         bool                     `json:"stream,omitempty"`
	StreamOptions  *openAIChatStreamOptions `json:"stream_options,omitempty"`
}

type openAIChatStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIChatMessage struct {
	Role       string                      `json:"role"`
	Content    string                      `json:"content,omitempty"`
	ToolCalls  []openAIChatRequestToolCall `json:"tool_calls,omitempty"`
	ToolCallID string                      `json:"tool_call_id,omitempty"`
}

type openAIChatRequestToolCall struct {
	ID       string                        `json:"id"`
	Type     string                        `json:"type"`
	Function openAIChatRequestToolFunction `json:"function"`
}

type openAIChatRequestToolFunction struct {
	Name      openAIWireToolName `json:"name"`
	Arguments string             `json:"arguments"`
}

type openAIChatTool struct {
	Type     string                 `json:"type"`
	Function openAIChatToolFunction `json:"function"`
}

type openAIChatToolFunction struct {
	Name        openAIWireToolName `json:"name"`
	Description string             `json:"description"`
	Parameters  json.RawMessage    `json:"parameters"`
	Strict      bool               `json:"strict"`
}

type openAIChatToolCall struct {
	Index      int             `json:"index,omitempty"`
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Input      json.RawMessage `json:"input"`
	Params     json.RawMessage `json:"params"`
	Args       json.RawMessage `json:"args"`
	Parameters json.RawMessage `json:"parameters"`
	Function   struct {
		Name       openAIWireToolName `json:"name"`
		Arguments  string             `json:"arguments"`
		Input      json.RawMessage    `json:"input"`
		Params     json.RawMessage    `json:"params"`
		Args       json.RawMessage    `json:"args"`
		Parameters json.RawMessage    `json:"parameters"`
	} `json:"function"`
}

type openAIResponsesRequest struct {
	Model          string                `json:"model"`
	Instructions   string                `json:"instructions"`
	Input          any                   `json:"input"`
	Tools          []openAIResponsesTool `json:"tools,omitempty"`
	PromptCacheKey string                `json:"prompt_cache_key,omitempty"`
	Include        []string              `json:"include,omitempty"`
	Stream         bool                  `json:"stream"`
	Store          bool                  `json:"store"`
}

type openAIResponsesInputMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIResponsesFunctionCall struct {
	Type      string             `json:"type"`
	CallID    string             `json:"call_id"`
	Name      openAIWireToolName `json:"name"`
	Arguments string             `json:"arguments"`
}

type openAIResponsesFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type openAIResponsesTool struct {
	Type        string             `json:"type"`
	Name        openAIWireToolName `json:"name"`
	Description string             `json:"description"`
	Parameters  json.RawMessage    `json:"parameters"`
	Strict      bool               `json:"strict"`
}

type openAIResponsesContinuationState struct {
	Version string            `json:"version"`
	Items   []json.RawMessage `json:"items"`
}

type openAIProviderUsageWire struct {
	PromptTokens       int `json:"prompt_tokens"`
	CompletionTokens   int `json:"completion_tokens"`
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	PromptTokenDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	InputTokenDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type openAIResponsesReasoningItem struct {
	ID               string                                `json:"id"`
	Type             string                                `json:"type"`
	EncryptedContent string                                `json:"encrypted_content"`
	Summary          []openAIResponsesReasoningSummaryPart `json:"summary,omitempty"`
	Status           string                                `json:"status,omitempty"`
	Content          []json.RawMessage                     `json:"content,omitempty"`
}

type openAIResponsesReasoningSummaryPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIContinuationTurn struct {
	AssistantText string
	Calls         []openAIContinuationCall
}

type openAIContinuationCall struct {
	ID        string
	Tool      AutomationToolName
	WireTool  openAIWireToolName
	Arguments string
	Operation WorkbenchOperation
	Result    string
}

type OpenAIAutomationHTTPError struct {
	StatusCode int
}

func (e *OpenAIAutomationHTTPError) Error() string {
	return fmt.Sprintf("openai automation provider returned HTTP status %d", e.StatusCode)
}

func (e *OpenAIAutomationHTTPError) Unwrap() error {
	return ErrOpenAIAutomationHTTPStatus
}

type OpenAICompatibleAutomationProvider struct {
	endpoint                      string
	model                         string
	apiKey                        string
	prompt                        string
	protocol                      AutomationProviderProtocol
	client                        *http.Client
	chatCompletionsPromptCacheKey bool
}

func NewOpenAICompatibleAutomationProvider(
	endpoint string,
	model string,
	apiKey string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	return newOpenAICompatibleAutomationProvider(endpoint, model, apiKey, DefaultAutomationPromptTemplate, client)
}

func NewOpenAICompatibleAutomationProviderWithProtocol(
	protocol AutomationProviderProtocol,
	endpoint string,
	model string,
	apiKey string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	return newOpenAICompatibleAutomationProviderWithProtocol(
		protocol,
		endpoint,
		model,
		apiKey,
		DefaultAutomationPromptTemplate,
		client,
	)
}

func newOpenAICompatibleAutomationProvider(
	endpoint string,
	model string,
	apiKey string,
	prompt string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	return newOpenAICompatibleAutomationProviderWithProtocol(
		AutomationProviderChatCompletions,
		endpoint,
		model,
		apiKey,
		prompt,
		client,
	)
}

func newOpenAICompatibleAutomationProviderWithProtocol(
	protocol AutomationProviderProtocol,
	endpoint string,
	model string,
	apiKey string,
	prompt string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	return newOpenAICompatibleAutomationProviderWithProtocolAndPromptCacheKey(
		protocol,
		endpoint,
		model,
		apiKey,
		prompt,
		client,
		false,
	)
}

func newOpenAICompatibleAutomationProviderWithProtocolAndPromptCacheKey(
	protocol AutomationProviderProtocol,
	endpoint string,
	model string,
	apiKey string,
	prompt string,
	client *http.Client,
	chatCompletionsPromptCacheKey bool,
) (*OpenAICompatibleAutomationProvider, error) {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	apiKey = strings.TrimSpace(apiKey)
	prompt = strings.TrimSpace(prompt)
	protocol, err := normalizeAutomationProviderProtocol(protocol)
	if err != nil {
		return nil, err
	}
	normalizedEndpoint, err := normalizeOpenAIAutomationEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if model == "" || apiKey == "" || prompt == "" {
		return nil, ErrOpenAIAutomationConfiguration
	}
	if client == nil {
		client = &http.Client{Timeout: 90 * time.Second}
	}
	return &OpenAICompatibleAutomationProvider{
		endpoint:                      normalizedEndpoint,
		model:                         model,
		apiKey:                        apiKey,
		prompt:                        prompt,
		protocol:                      protocol,
		client:                        client,
		chatCompletionsPromptCacheKey: chatCompletionsPromptCacheKey,
	}, nil
}

func NewOpenAIAutomationProvider(
	endpoint string,
	model string,
	apiKey string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	if client == nil {
		return nil, ErrOpenAIAutomationConfiguration
	}
	return NewOpenAICompatibleAutomationProvider(endpoint, model, apiKey, client)
}

func NewOpenAIAutomationProviderWithProtocol(
	protocol AutomationProviderProtocol,
	endpoint string,
	model string,
	apiKey string,
	client *http.Client,
) (*OpenAICompatibleAutomationProvider, error) {
	if client == nil {
		return nil, ErrOpenAIAutomationConfiguration
	}
	return newOpenAICompatibleAutomationProviderWithProtocol(
		protocol,
		endpoint,
		model,
		apiKey,
		DefaultAutomationPromptTemplate,
		client,
	)
}

func (p *OpenAICompatibleAutomationProvider) Name() string {
	return "openai-compatible"
}

func (p *OpenAICompatibleAutomationProvider) CompactContext(
	ctx context.Context,
	request AutomationContextCompactionRequest,
) (AutomationContextCompactionResult, error) {
	if ctx == nil {
		return AutomationContextCompactionResult{}, NewAutomationValidationError("context", "is required")
	}
	if p == nil {
		return AutomationContextCompactionResult{}, ErrAutomationContextCompactorUnavailable
	}
	if request.SchemaVersion != automationChatCompactionInputVersion {
		return AutomationContextCompactionResult{}, NewAutomationValidationError(
			"compaction.schema_version",
			"is unsupported",
		)
	}
	if strings.TrimSpace(request.SessionID) == "" || len(request.Turns) == 0 {
		return AutomationContextCompactionResult{}, NewAutomationValidationError(
			"compaction",
			"requires a session and at least one completed turn",
		)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return AutomationContextCompactionResult{}, err
	}
	if len(data) > AutomationMaxProviderBytes {
		return AutomationContextCompactionResult{}, NewAutomationValidationError("compaction", "is too large")
	}
	compactor := *p
	compactor.prompt = DefaultAutomationContextCompactionPrompt
	output, err := compactor.GenerateOutput(ctx, AutomationProviderInput{
		SchemaVersion: AutomationProviderInputVersion,
		RunID:         "compact:" + request.SessionID,
		EngineVersion: AutomationEngineVersion,
		Message: AutomationMessageContext{
			Source: "automation_chat_compaction",
			Text:   string(data),
			Kinds:  []string{"text"},
		},
		AllowedTools: []AutomationToolName{},
	})
	if err != nil {
		return AutomationContextCompactionResult{}, err
	}
	summary, err := decodeAutomationChatCompactionResult(output.AssistantText)
	if err != nil {
		return AutomationContextCompactionResult{}, err
	}
	var usage *AutomationProviderUsage
	if output.Usage != nil {
		cloned := *output.Usage
		usage = &cloned
	}
	return AutomationContextCompactionResult{Summary: summary, Usage: usage}, nil
}

func (p *OpenAICompatibleAutomationProvider) Generate(
	ctx context.Context,
	input AutomationProviderInput,
) ([]byte, error) {
	output, err := p.GenerateOutput(ctx, input)
	if err != nil {
		return nil, err
	}
	return json.Marshal(AutomationToolEnvelope{
		SchemaVersion:    AutomationToolEnvelopeVersion,
		AssistantMessage: output.AssistantText,
		Actions:          output.ToolCalls,
	})
}

func (p *OpenAICompatibleAutomationProvider) GenerateOutput(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	if ctx == nil {
		return AutomationProviderOutput{}, NewAutomationValidationError("context", "is required")
	}
	body, err := p.requestBody(input, false)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	var lastErr error
	for attempt := 0; attempt < automationProviderMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return AutomationProviderOutput{}, ctx.Err()
			case <-time.After(automationProviderRetryBackoff(attempt)):
			}
		}
		output, genErr := p.generateOnce(ctx, body, input)
		if genErr == nil {
			return output, nil
		}
		if ctx.Err() != nil {
			return AutomationProviderOutput{}, ctx.Err()
		}
		lastErr = genErr
		if !automationProviderRetryableError(genErr) {
			return AutomationProviderOutput{}, genErr
		}
	}
	return AutomationProviderOutput{}, lastErr
}

// openAIAutomationEndpointHost returns the host portion of an endpoint URL for
// structured observability logs. The endpoint carries no credentials, but only
// the host is recorded to keep the log line compact. If the endpoint somehow
// fails to parse (it is validated at construction), the raw value is returned
// as a best-effort fallback.
func openAIAutomationEndpointHost(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return endpoint
	}
	return parsed.Host
}

// logOpenAIAutomationProviderCall records one structured provider call
// observation (event=automation_provider_call, see CONTRACT phase 4 P2).
// Success is logged at info level; failure at error level with the sanitized
// error message. Credentials (apiKey / Authorization header) are never
// included. usage may be nil; token fields then stay 0 (no fabrication).
func logOpenAIAutomationProviderCall(
	ctx context.Context,
	protocol AutomationProviderProtocol,
	model string,
	endpointHost string,
	started time.Time,
	usage *AutomationProviderUsage,
	err error,
) {
	fields := []interface{}{
		"event", "automation_provider_call",
		"protocol", string(protocol),
		"model", model,
		"endpoint", endpointHost,
		"duration_ms", time.Since(started).Milliseconds(),
	}
	promptTokens, completionTokens := 0, 0
	if usage != nil {
		promptTokens = usage.InputTokens
		completionTokens = usage.OutputTokens
	}
	fields = append(fields, "prompt_tokens", promptTokens, "completion_tokens", completionTokens)
	if err != nil {
		fields = append(fields, "error", err.Error())
		ErrorCtx(ctx, "automation provider call failed", fields...)
		return
	}
	InfoCtx(ctx, "automation provider call succeeded", fields...)
}

func (p *OpenAICompatibleAutomationProvider) generateOnce(
	ctx context.Context,
	body []byte,
	input AutomationProviderInput,
) (output AutomationProviderOutput, err error) {
	started := time.Now()
	defer func() {
		logOpenAIAutomationProviderCall(
			ctx,
			p.protocol,
			p.model,
			openAIAutomationEndpointHost(p.endpoint),
			started,
			output.Usage,
			err,
		)
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return AutomationProviderOutput{}, ctx.Err()
		}
		return AutomationProviderOutput{}, ErrOpenAIAutomationRequestFailed
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, openAIMaxResponseBodyBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return AutomationProviderOutput{}, ctx.Err()
		}
		return AutomationProviderOutput{}, ErrOpenAIAutomationRequestFailed
	}
	if len(data) > openAIMaxResponseBodyBytes {
		return AutomationProviderOutput{}, ErrOpenAIAutomationResponseTooLarge
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AutomationProviderOutput{}, &OpenAIAutomationHTTPError{StatusCode: response.StatusCode}
	}
	output, err = p.parseResponse(data)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	digest := sha256.Sum256(data)
	output.RawSHA256 = fmt.Sprintf("%x", digest[:])
	return NormalizeAutomationProviderOutput(output, input.AllowedTools)
}

// automationProviderMaxAttempts bounds Generate to one initial call plus retries.
const automationProviderMaxAttempts = 3

func automationProviderRetryBackoff(attempt int) time.Duration {
	backoff := 400 * time.Millisecond * time.Duration(attempt)
	if backoff > 2*time.Second {
		backoff = 2 * time.Second
	}
	return backoff
}

// automationProviderRetryableError reports whether Generate should retry. 401 is
// retried because the upstream relay load-balances across channels and a retry
// can land on a healthy one; transient network and 408/429/5xx failures retry too.
func automationProviderRetryableError(err error) bool {
	if errors.Is(err, ErrOpenAIAutomationRequestFailed) {
		return true
	}
	var httpErr *OpenAIAutomationHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case http.StatusUnauthorized,
			http.StatusRequestTimeout,
			http.StatusTooManyRequests,
			http.StatusInternalServerError,
			http.StatusBadGateway,
			http.StatusServiceUnavailable,
			http.StatusGatewayTimeout:
			return true
		}
	}
	return false
}

func (p *OpenAICompatibleAutomationProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (stream AutomationProviderStream, err error) {
	started := time.Now()
	// Setup failures (request build / HTTP / non-2xx) are observed here; the
	// streaming call itself is observed once at completion in completeOutput
	// (success) or via fail (error interruption), so no per-chunk logging.
	defer func() {
		if err != nil {
			logOpenAIAutomationProviderCall(
				ctx,
				p.protocol,
				p.model,
				openAIAutomationEndpointHost(p.endpoint),
				started,
				nil,
				err,
			)
		}
	}()
	if ctx == nil {
		return nil, NewAutomationValidationError("context", "is required")
	}
	body, err := p.requestBody(input, true)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	response, err := p.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrOpenAIAutomationRequestFailed
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = response.Body.Close()
		return nil, &OpenAIAutomationHTTPError{StatusCode: response.StatusCode}
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 32<<10), openAIMaxResponseBodyBytes)
	return &openAIAutomationProviderStream{
		protocol:           p.protocol,
		model:              p.model,
		endpointHost:       openAIAutomationEndpointHost(p.endpoint),
		started:            started,
		ctx:                ctx,
		body:               response.Body,
		scanner:            scanner,
		allowedTools:       append([]AutomationToolName(nil), input.AllowedTools...),
		toolCalls:          make(map[int]*openAIStreamToolCall),
		responsesReasoning: make(map[int]json.RawMessage),
	}, nil
}

func (p *OpenAICompatibleAutomationProvider) requestBody(
	input AutomationProviderInput,
	stream bool,
) ([]byte, error) {
	continuation, err := automationOpenAIContinuation(input.Continuation)
	if err != nil {
		return nil, err
	}
	var continuationStateItems []json.RawMessage
	if len(input.ContinuationState) != 0 {
		if p.protocol != AutomationProviderResponses {
			return nil, NewAutomationValidationError(
				"continuation_state",
				"is only valid for the Responses protocol",
			)
		}
		if len(continuation) == 0 {
			return nil, NewAutomationValidationError(
				"continuation_state",
				"requires continuation tool messages",
			)
		}
		continuationStateItems, err = automationOpenAIResponsesContinuationItems(input.ContinuationState)
		if err != nil {
			return nil, err
		}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if len(inputJSON) > AutomationMaxProviderBytes {
		return nil, NewAutomationValidationError("provider_input", "is too large")
	}
	nativeInputJSON := inputJSON
	if len(continuation) != 0 || len(input.ContinuationState) != 0 {
		nativeInput := input
		nativeInput.Continuation = nil
		nativeInput.ContinuationState = nil
		nativeInputJSON, err = json.Marshal(nativeInput)
		if err != nil {
			return nil, err
		}
	}
	definitions, err := AutomationToolDefinitions(input.AllowedTools)
	if err != nil {
		return nil, err
	}
	promptCacheKey, err := automationOpenAIPromptCacheKey(
		p.protocol,
		p.model,
		p.prompt,
		definitions,
	)
	if err != nil {
		return nil, err
	}
	switch p.protocol {
	case AutomationProviderResponses:
		tools := make([]openAIResponsesTool, 0, len(definitions))
		for _, definition := range definitions {
			wireName, err := automationOpenAIWireToolName(definition.Name)
			if err != nil {
				return nil, err
			}
			tools = append(tools, openAIResponsesTool{
				Type:        "function",
				Name:        wireName,
				Description: definition.Description,
				Parameters:  definition.Parameters,
				Strict:      definition.Strict,
			})
		}
		responsesInput := any(string(nativeInputJSON))
		if len(continuation) != 0 {
			responsesInput = automationOpenAIResponsesContinuationInput(
				nativeInputJSON,
				continuationStateItems,
				continuation,
			)
		}
		return json.Marshal(openAIResponsesRequest{
			Model:          p.model,
			Instructions:   p.prompt,
			Input:          responsesInput,
			Tools:          tools,
			PromptCacheKey: promptCacheKey,
			Include:        []string{openAIResponsesEncryptedReasoningInclude},
			Stream:         stream,
			Store:          false,
		})
	default:
		tools := make([]openAIChatTool, 0, len(definitions))
		for _, definition := range definitions {
			wireName, err := automationOpenAIWireToolName(definition.Name)
			if err != nil {
				return nil, err
			}
			tools = append(tools, openAIChatTool{
				Type: "function",
				Function: openAIChatToolFunction{
					Name:        wireName,
					Description: definition.Description,
					Parameters:  definition.Parameters,
					Strict:      definition.Strict,
				},
			})
		}
		messages := []openAIChatMessage{
			{Role: "system", Content: p.prompt},
			{Role: "user", Content: string(nativeInputJSON)},
		}
		messages = append(messages, automationOpenAIChatContinuationMessages(continuation)...)
		request := openAIChatCompletionRequest{
			Model:    p.model,
			Messages: messages,
			Tools:    tools,
			// prompt_cache_key is only sent on the chat completions
			// protocol when the operator opts in via the
			// chat_completions_prompt_cache_key config switch: new-api
			// style gateways (DeepSeek channel) reject unknown parameters
			// with HTTP 400. The responses protocol keeps sending it, where
			// OpenAI compatible servers support prompt caching natively.
			Stream: stream,
		}
		if p.chatCompletionsPromptCacheKey {
			request.PromptCacheKey = promptCacheKey
		}
		if stream {
			request.StreamOptions = &openAIChatStreamOptions{IncludeUsage: true}
		}
		return json.Marshal(request)
	}
}

func automationOpenAIPromptCacheKey(
	protocol AutomationProviderProtocol,
	model string,
	prompt string,
	definitions []AutomationToolDefinition,
) (string, error) {
	data, err := json.Marshal(struct {
		Version     string                     `json:"version"`
		Protocol    AutomationProviderProtocol `json:"protocol"`
		Model       string                     `json:"model"`
		Prompt      string                     `json:"prompt"`
		Definitions []AutomationToolDefinition `json:"definitions"`
	}{
		Version:     openAIPromptCacheKeyVersion,
		Protocol:    protocol,
		Model:       model,
		Prompt:      prompt,
		Definitions: definitions,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return fmt.Sprintf("lingxi-hr-v1:%x", digest[:20]), nil
}

type noopAutomationProvider struct{}

func (noopAutomationProvider) Name() string {
	return "disabled"
}

func (noopAutomationProvider) Generate(context.Context, AutomationProviderInput) ([]byte, error) {
	return []byte(`{"schema_version":"automation.actions.v1","actions":[]}`), nil
}

func (noopAutomationProvider) GenerateOutput(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	if ctx == nil {
		return AutomationProviderOutput{}, NewAutomationValidationError("context", "is required")
	}
	if err := ctx.Err(); err != nil {
		return AutomationProviderOutput{}, err
	}
	return NormalizeAutomationProviderOutput(AutomationProviderOutput{
		ToolCalls: []AutomationToolCall{},
	}, input.AllowedTools)
}

func (p noopAutomationProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderStream, error) {
	output, err := p.GenerateOutput(ctx, input)
	if err != nil {
		return nil, err
	}
	return &singleAutomationProviderStream{output: output}, nil
}

type DynamicAutomationProvider struct {
	mu       sync.RWMutex
	provider AutomationProvider
}

func NewDynamicAutomationProvider(provider AutomationProvider) *DynamicAutomationProvider {
	p := &DynamicAutomationProvider{}
	p.Swap(provider)
	return p
}

func (p *DynamicAutomationProvider) Swap(provider AutomationProvider) {
	if provider == nil {
		provider = noopAutomationProvider{}
	}
	p.mu.Lock()
	p.provider = provider
	p.mu.Unlock()
}

func (p *DynamicAutomationProvider) Name() string {
	if p == nil {
		return "disabled"
	}
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	if provider == nil {
		return "disabled"
	}
	return provider.Name()
}

func (p *DynamicAutomationProvider) Generate(ctx context.Context, input AutomationProviderInput) ([]byte, error) {
	if p == nil {
		return noopAutomationProvider{}.Generate(ctx, input)
	}
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	if provider == nil {
		provider = noopAutomationProvider{}
	}
	return provider.Generate(ctx, input)
}

func (p *DynamicAutomationProvider) GenerateOutput(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	if p == nil {
		return noopAutomationProvider{}.GenerateOutput(ctx, input)
	}
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	if provider == nil {
		provider = noopAutomationProvider{}
	}
	return automationGenerateProviderOutput(ctx, provider, input)
}

func (p *DynamicAutomationProvider) CompactContext(
	ctx context.Context,
	request AutomationContextCompactionRequest,
) (AutomationContextCompactionResult, error) {
	if p == nil {
		return AutomationContextCompactionResult{}, ErrAutomationContextCompactorUnavailable
	}
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	compactor, ok := provider.(AutomationContextCompactor)
	if !ok {
		return AutomationContextCompactionResult{}, ErrAutomationContextCompactorUnavailable
	}
	return compactor.CompactContext(ctx, request)
}

func (p *DynamicAutomationProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderStream, error) {
	if p == nil {
		return noopAutomationProvider{}.Stream(ctx, input)
	}
	p.mu.RLock()
	provider := p.provider
	p.mu.RUnlock()
	if provider == nil {
		provider = noopAutomationProvider{}
	}
	return provider.Stream(ctx, input)
}

type openAIStreamToolCall struct {
	id        string
	name      AutomationToolName
	arguments strings.Builder
}

type openAIAutomationProviderStream struct {
	protocol           AutomationProviderProtocol
	model              string
	endpointHost       string
	started            time.Time
	ctx                context.Context
	body               io.ReadCloser
	scanner            *bufio.Scanner
	allowedTools       []AutomationToolName
	assistant          strings.Builder
	toolCalls          map[int]*openAIStreamToolCall
	responsesReasoning map[int]json.RawMessage
	usage              *AutomationProviderUsage
	pendingEvents      []AutomationProviderEvent
	raw                strings.Builder
	completed          bool
	failed             bool
	closed             bool
}

// fail terminates the stream with a provider call error observation. The
// failure is logged at error level (once per stream), the underlying body is
// closed (idempotent), and err is returned unchanged. It replaces the plain
// `_ = s.Close()` on error paths without changing behavior.
func (s *openAIAutomationProviderStream) fail(err error) error {
	if !s.failed {
		s.failed = true
		logOpenAIAutomationProviderCall(s.ctx, s.protocol, s.model, s.endpointHost, s.started, s.usage, err)
	}
	_ = s.Close()
	return err
}

func (s *openAIAutomationProviderStream) Recv() (AutomationProviderEvent, error) {
	if s == nil || s.closed {
		return AutomationProviderEvent{}, io.EOF
	}
	if len(s.pendingEvents) != 0 {
		event := s.pendingEvents[0]
		s.pendingEvents = s.pendingEvents[1:]
		return event, nil
	}
	if s.completed {
		return AutomationProviderEvent{}, io.EOF
	}
	for s.scanner.Scan() {
		if err := s.ctx.Err(); err != nil {
			return AutomationProviderEvent{}, s.fail(err)
		}
		line := s.scanner.Text()
		if len(line)+1 > openAIMaxResponseBodyBytes-s.raw.Len() {
			return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationResponseTooLarge)
		}
		s.raw.WriteString(line)
		s.raw.WriteByte('\n')
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			return s.complete()
		}
		if s.protocol == AutomationProviderResponses {
			event, emit, err := s.recvResponsesPayload([]byte(payload))
			if err != nil {
				return AutomationProviderEvent{}, s.fail(err)
			}
			if emit {
				return event, nil
			}
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   json.RawMessage      `json:"content"`
					ToolCalls []openAIChatToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage json.RawMessage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationMalformedResponse)
		}
		if len(chunk.Usage) != 0 {
			usage, err := automationOpenAIProviderUsage(chunk.Usage)
			if err != nil {
				return AutomationProviderEvent{}, s.fail(err)
			}
			if usage != nil {
				s.usage = usage
			}
		}
		if len(chunk.Choices) == 0 {
			if s.usage == nil {
				return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationMalformedResponse)
			}
			continue
		}
		delta := chunk.Choices[0].Delta
		text, err := automationOpenAIContentText(delta.Content)
		if err != nil {
			return AutomationProviderEvent{}, s.fail(err)
		}
		if text != "" {
			if len(text) > AutomationMaxProviderBytes-s.assistant.Len() {
				return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationResponseTooLarge)
			}
			s.assistant.WriteString(text)
			s.pendingEvents = append(s.pendingEvents, AutomationProviderEvent{
				Type: "text_delta", TextDelta: text,
			})
		}
		for _, deltaCall := range delta.ToolCalls {
			if automationOpenAIHasArgumentWrapper(
				deltaCall.Input,
				deltaCall.Params,
				deltaCall.Args,
				deltaCall.Parameters,
				deltaCall.Function.Input,
				deltaCall.Function.Params,
				deltaCall.Function.Args,
				deltaCall.Function.Parameters,
			) {
				return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationMalformedResponse)
			}
			accumulated := s.toolCalls[deltaCall.Index]
			if accumulated == nil {
				if len(s.toolCalls) >= AutomationMaxActionsPerRun {
					return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationResponseTooLarge)
				}
				accumulated = &openAIStreamToolCall{}
				s.toolCalls[deltaCall.Index] = accumulated
			}
			if deltaCall.ID != "" {
				accumulated.id += deltaCall.ID
			}
			if deltaCall.Function.Name != "" {
				if err := automationOpenAISetStreamToolName(accumulated, deltaCall.Function.Name); err != nil {
					return AutomationProviderEvent{}, s.fail(err)
				}
			}
			if deltaCall.Function.Arguments != "" {
				if len(deltaCall.Function.Arguments) > AutomationMaxActionPayloadBytes-accumulated.arguments.Len() {
					return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationResponseTooLarge)
				}
				accumulated.arguments.WriteString(deltaCall.Function.Arguments)
			}
			preview := AutomationToolCall{
				CallID:     accumulated.id,
				Tool:       accumulated.name,
				Confidence: 1,
				Evidence:   []AutomationEvidence{},
				Arguments:  json.RawMessage(accumulated.arguments.String()),
			}
			s.pendingEvents = append(s.pendingEvents, AutomationProviderEvent{
				Type: "tool_call_delta", ToolCall: &preview,
			})
		}
		if len(s.pendingEvents) != 0 {
			event := s.pendingEvents[0]
			s.pendingEvents = s.pendingEvents[1:]
			return event, nil
		}
	}
	if err := s.scanner.Err(); err != nil {
		if ctxErr := s.ctx.Err(); ctxErr != nil {
			return AutomationProviderEvent{}, s.fail(ctxErr)
		}
		return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationRequestFailed)
	}
	return s.complete()
}

func (s *openAIAutomationProviderStream) recvResponsesPayload(
	payload []byte,
) (AutomationProviderEvent, bool, error) {
	var event struct {
		Type        string             `json:"type"`
		OutputIndex int                `json:"output_index"`
		Delta       string             `json:"delta"`
		Arguments   string             `json:"arguments"`
		Name        openAIWireToolName `json:"name"`
		Item        json.RawMessage    `json:"item"`
		Response    json.RawMessage    `json:"response"`
	}
	if err := json.Unmarshal(payload, &event); err != nil || event.Type == "" {
		return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
	}
	switch event.Type {
	case "response.output_text.delta":
		if len(event.Delta) > AutomationMaxProviderBytes-s.assistant.Len() {
			return AutomationProviderEvent{}, false, ErrOpenAIAutomationResponseTooLarge
		}
		s.assistant.WriteString(event.Delta)
		return AutomationProviderEvent{Type: "text_delta", TextDelta: event.Delta}, event.Delta != "", nil
	case "response.output_text.done":
		return AutomationProviderEvent{}, false, nil
	case "response.output_item.added", "response.output_item.done":
		if len(event.Item) == 0 {
			return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
		}
		preview, isFunction, err := s.applyResponsesFunctionCall(event.OutputIndex, event.Item)
		if err != nil {
			return AutomationProviderEvent{}, false, err
		}
		if isFunction {
			return AutomationProviderEvent{Type: "tool_call_delta", ToolCall: &preview}, true, nil
		}
		if event.Type == "response.output_item.done" {
			var marker struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(event.Item, &marker); err != nil {
				return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
			}
			if marker.Type == "reasoning" {
				if len(s.responsesReasoning) >= openAIMaxResponsesContinuationItems {
					return AutomationProviderEvent{}, false, ErrOpenAIAutomationResponseTooLarge
				}
				_, canonical, err := automationOpenAIResponsesReasoningStateItem(
					indexedField("response.output", event.OutputIndex),
					event.Item,
				)
				if err != nil {
					if len(event.Item) > AutomationMaxProviderBytes {
						return AutomationProviderEvent{}, false, ErrOpenAIAutomationResponseTooLarge
					}
					return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
				}
				if _, exists := s.responsesReasoning[event.OutputIndex]; exists {
					return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
				}
				s.responsesReasoning[event.OutputIndex] = canonical
			}
		}
		return AutomationProviderEvent{}, false, nil
	case "response.function_call_arguments.delta":
		call := s.toolCalls[event.OutputIndex]
		if call == nil {
			return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
		}
		if len(event.Delta) > AutomationMaxActionPayloadBytes-call.arguments.Len() {
			return AutomationProviderEvent{}, false, ErrOpenAIAutomationResponseTooLarge
		}
		call.arguments.WriteString(event.Delta)
		preview := automationOpenAIStreamToolCallPreview(call)
		return AutomationProviderEvent{Type: "tool_call_delta", ToolCall: &preview}, event.Delta != "", nil
	case "response.function_call_arguments.done":
		call := s.toolCalls[event.OutputIndex]
		if call == nil {
			return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
		}
		if event.Name != "" {
			if err := automationOpenAISetStreamToolName(call, event.Name); err != nil {
				return AutomationProviderEvent{}, false, err
			}
		}
		if call.arguments.Len() == 0 {
			if len(event.Arguments) > AutomationMaxActionPayloadBytes {
				return AutomationProviderEvent{}, false, ErrOpenAIAutomationResponseTooLarge
			}
			call.arguments.WriteString(event.Arguments)
		} else if event.Arguments != "" && call.arguments.String() != event.Arguments {
			return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
		}
		return AutomationProviderEvent{}, false, nil
	case "response.completed":
		if len(event.Response) != 0 && strings.TrimSpace(string(event.Response)) != "null" {
			output, err := automationOpenAIResponsesOutput(event.Response)
			if err != nil {
				return AutomationProviderEvent{}, false, err
			}
			completed, err := s.completeOutput(output)
			return completed, err == nil, err
		}
		completed, err := s.complete()
		return completed, err == nil, err
	case "error", "response.failed", "response.incomplete", "response.cancelled":
		return AutomationProviderEvent{}, false, ErrOpenAIAutomationRequestFailed
	case "response.created",
		"response.queued",
		"response.in_progress",
		"response.content_part.added",
		"response.content_part.done":
		return AutomationProviderEvent{}, false, nil
	default:
		return AutomationProviderEvent{}, false, ErrOpenAIAutomationMalformedResponse
	}
}

func (s *openAIAutomationProviderStream) applyResponsesFunctionCall(
	index int,
	raw json.RawMessage,
) (AutomationToolCall, bool, error) {
	var item struct {
		Type       string             `json:"type"`
		CallID     string             `json:"call_id"`
		Name       openAIWireToolName `json:"name"`
		Arguments  *string            `json:"arguments"`
		Input      json.RawMessage    `json:"input"`
		Params     json.RawMessage    `json:"params"`
		Args       json.RawMessage    `json:"args"`
		Parameters json.RawMessage    `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return AutomationToolCall{}, false, ErrOpenAIAutomationMalformedResponse
	}
	if item.Type != "function_call" {
		if item.Type == "message" || item.Type == "reasoning" {
			return AutomationToolCall{}, false, nil
		}
		return AutomationToolCall{}, false, ErrOpenAIAutomationMalformedResponse
	}
	if automationOpenAIHasArgumentWrapper(item.Input, item.Params, item.Args, item.Parameters) {
		return AutomationToolCall{}, false, ErrOpenAIAutomationMalformedResponse
	}
	call := s.toolCalls[index]
	if call == nil {
		if len(s.toolCalls) >= AutomationMaxActionsPerRun {
			return AutomationToolCall{}, false, ErrOpenAIAutomationResponseTooLarge
		}
		call = &openAIStreamToolCall{}
		s.toolCalls[index] = call
	}
	if item.CallID != "" {
		call.id = item.CallID
	}
	if item.Name != "" {
		if err := automationOpenAISetStreamToolName(call, item.Name); err != nil {
			return AutomationToolCall{}, false, err
		}
	}
	if item.Arguments != nil && *item.Arguments != "" {
		if call.arguments.Len() == 0 {
			if len(*item.Arguments) > AutomationMaxActionPayloadBytes {
				return AutomationToolCall{}, false, ErrOpenAIAutomationResponseTooLarge
			}
			call.arguments.WriteString(*item.Arguments)
		} else if call.arguments.String() != *item.Arguments {
			return AutomationToolCall{}, false, ErrOpenAIAutomationMalformedResponse
		}
	}
	return automationOpenAIStreamToolCallPreview(call), true, nil
}

func automationOpenAIStreamToolCallPreview(call *openAIStreamToolCall) AutomationToolCall {
	return AutomationToolCall{
		CallID:     call.id,
		Tool:       call.name,
		Confidence: 1,
		Evidence:   []AutomationEvidence{},
		Arguments:  json.RawMessage(call.arguments.String()),
	}
}

func automationOpenAISetStreamToolName(
	call *openAIStreamToolCall,
	name openAIWireToolName,
) error {
	tool, err := automationOpenAIInternalToolName(name)
	if err != nil {
		return err
	}
	if call.name != "" && call.name != tool {
		return ErrOpenAIAutomationMalformedResponse
	}
	call.name = tool
	return nil
}

func (s *openAIAutomationProviderStream) complete() (AutomationProviderEvent, error) {
	if s.completed {
		return AutomationProviderEvent{}, io.EOF
	}
	indices := make([]int, 0, len(s.toolCalls))
	for index := range s.toolCalls {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	native := make([]openAIChatToolCall, 0, len(indices))
	for _, index := range indices {
		call := s.toolCalls[index]
		item := openAIChatToolCall{Index: index, ID: call.id, Type: "function"}
		wireName, err := automationOpenAIWireToolName(call.name)
		if err != nil {
			return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationMalformedResponse)
		}
		item.Function.Name = wireName
		item.Function.Arguments = call.arguments.String()
		native = append(native, item)
	}
	toolCalls, err := automationOpenAIToolCalls(native)
	if err != nil {
		return AutomationProviderEvent{}, s.fail(err)
	}
	output := AutomationProviderOutput{
		AssistantText: s.assistant.String(),
		ToolCalls:     toolCalls,
	}
	if s.protocol == AutomationProviderResponses {
		if len(toolCalls) != 0 && len(s.responsesReasoning) != 0 {
			reasoningIndices := make([]int, 0, len(s.responsesReasoning))
			for index := range s.responsesReasoning {
				reasoningIndices = append(reasoningIndices, index)
			}
			sort.Ints(reasoningIndices)
			if len(indices) != 0 && reasoningIndices[len(reasoningIndices)-1] >= indices[0] {
				return AutomationProviderEvent{}, s.fail(ErrOpenAIAutomationMalformedResponse)
			}
			reasoningItems := make([]json.RawMessage, 0, len(reasoningIndices))
			for _, index := range reasoningIndices {
				reasoningItems = append(reasoningItems, s.responsesReasoning[index])
			}
			output.ContinuationState, err = automationOpenAIResponsesContinuationStateJSON(reasoningItems)
			if err != nil {
				return AutomationProviderEvent{}, s.fail(err)
			}
		}
	} else {
		output, err = automationOpenAIChatOutput(output.AssistantText, output.ToolCalls)
		if err != nil {
			return AutomationProviderEvent{}, s.fail(err)
		}
	}
	return s.completeOutput(output)
}

func (s *openAIAutomationProviderStream) completeOutput(
	output AutomationProviderOutput,
) (AutomationProviderEvent, error) {
	digest := sha256.Sum256([]byte(s.raw.String()))
	output.RawSHA256 = fmt.Sprintf("%x", digest[:])
	if output.Usage == nil && s.usage != nil {
		usage := *s.usage
		output.Usage = &usage
	}
	output, err := NormalizeAutomationProviderOutput(output, s.allowedTools)
	if err != nil {
		return AutomationProviderEvent{}, s.fail(err)
	}
	s.completed = true
	_ = s.Close()
	logOpenAIAutomationProviderCall(s.ctx, s.protocol, s.model, s.endpointHost, s.started, output.Usage, nil)
	return AutomationProviderEvent{Type: "completed", Output: &output}, nil
}

func automationOpenAIRequireStableCallIDs(calls []openAIChatToolCall) error {
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		callID := strings.TrimSpace(call.ID)
		if callID == "" {
			return ErrOpenAIAutomationMalformedResponse
		}
		if _, exists := seen[callID]; exists {
			return ErrOpenAIAutomationMalformedResponse
		}
		seen[callID] = struct{}{}
	}
	return nil
}

func (s *openAIAutomationProviderStream) Close() error {
	if s == nil || s.closed {
		return nil
	}
	s.closed = true
	if s.body == nil {
		return nil
	}
	return s.body.Close()
}
