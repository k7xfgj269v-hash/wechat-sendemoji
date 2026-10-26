package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	AutomationGateInputVersion    = "automation.gate.input.v1"
	AutomationGateDecisionVersion = "automation.gate.v1"
	automationGateMaxReasonBytes  = 1000
	automationGateMaxTimeout      = 30 * time.Second
)

type AutomationGateInput struct {
	SchemaVersion string                      `json:"schema_version"`
	RunID         string                      `json:"run_id"`
	Recruiting    AutomationRecruitingContext `json:"recruiting"`
	Message       AutomationMessageContext    `json:"message"`
	GateMode      string                      `json:"gate_mode"`
}

type AutomationGateDecision struct {
	SchemaVersion string  `json:"schema_version"`
	Allow         bool    `json:"allow"`
	Reason        string  `json:"reason"`
	Confidence    float64 `json:"confidence"`
	Model         string  `json:"-"`
	RawSHA256     string  `json:"-"`
}

type AutomationMessageGate interface {
	Evaluate(context.Context, AutomationGateInput) (AutomationGateDecision, error)
}

type providerStoreAutomationMessageGate struct {
	store  *AutomationProviderConfigStore
	client *http.Client
}

func newProviderStoreAutomationMessageGate(
	store *AutomationProviderConfigStore,
) AutomationMessageGate {
	return &providerStoreAutomationMessageGate{store: store}
}

func (g *providerStoreAutomationMessageGate) Evaluate(
	ctx context.Context,
	input AutomationGateInput,
) (AutomationGateDecision, error) {
	if ctx == nil {
		return AutomationGateDecision{}, NewAutomationValidationError("context", "is required")
	}
	if g == nil || g.store == nil {
		return AutomationGateDecision{}, NewAutomationValidationError("gate", "is unavailable")
	}
	g.store.mu.RLock()
	config := cloneAutomationProviderConfig(g.store.config)
	apiKey := automationProviderEffectiveAPIKey(g.store.apiKey)
	g.store.mu.RUnlock()
	if !config.Enabled {
		return AutomationGateDecision{}, NewAutomationValidationError("gate", "provider is disabled")
	}
	model := strings.TrimSpace(config.GateModel)
	if model == "" {
		model = config.Model
	}
	client := g.client
	if client == nil {
		timeout := time.Duration(config.TimeoutSeconds) * time.Second
		if timeout > automationGateMaxTimeout {
			timeout = automationGateMaxTimeout
		}
		client = &http.Client{Timeout: timeout}
	}
	provider, err := newOpenAICompatibleAutomationProviderWithProtocol(
		config.Protocol,
		config.Endpoint,
		model,
		apiKey,
		config.GatePromptTemplate,
		client,
	)
	if err != nil {
		return AutomationGateDecision{}, err
	}
	return provider.GenerateGateDecision(ctx, input)
}

func (p *OpenAICompatibleAutomationProvider) GenerateGateDecision(
	ctx context.Context,
	input AutomationGateInput,
) (AutomationGateDecision, error) {
	if ctx == nil {
		return AutomationGateDecision{}, NewAutomationValidationError("context", "is required")
	}
	if p == nil || p.client == nil {
		return AutomationGateDecision{}, ErrOpenAIAutomationConfiguration
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return AutomationGateDecision{}, err
	}
	if len(inputJSON) > AutomationMaxProviderBytes {
		return AutomationGateDecision{}, NewAutomationValidationError("gate_input", "is too large")
	}
	var body []byte
	switch p.protocol {
	case AutomationProviderResponses:
		body, err = json.Marshal(openAIResponsesRequest{
			Model:        p.model,
			Instructions: p.prompt,
			Input:        string(inputJSON),
			Stream:       false,
			Store:        false,
		})
	default:
		body, err = json.Marshal(openAIChatCompletionRequest{
			Model: p.model,
			Messages: []openAIChatMessage{
				{Role: "system", Content: p.prompt},
				{Role: "user", Content: string(inputJSON)},
			},
		})
	}
	if err != nil {
		return AutomationGateDecision{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return AutomationGateDecision{}, err
	}
	request.Header.Set("Authorization", "Bearer "+p.apiKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return AutomationGateDecision{}, ctx.Err()
		}
		return AutomationGateDecision{}, ErrOpenAIAutomationRequestFailed
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, openAIMaxResponseBodyBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return AutomationGateDecision{}, ctx.Err()
		}
		return AutomationGateDecision{}, ErrOpenAIAutomationRequestFailed
	}
	if len(data) > openAIMaxResponseBodyBytes {
		return AutomationGateDecision{}, ErrOpenAIAutomationResponseTooLarge
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AutomationGateDecision{}, &OpenAIAutomationHTTPError{StatusCode: response.StatusCode}
	}
	text, err := automationOpenAIGateText(p.protocol, data)
	if err != nil {
		return AutomationGateDecision{}, err
	}
	var decision AutomationGateDecision
	if err := automationOpenAIStrictDecode("gate_response", []byte(strings.TrimSpace(text)), &decision); err != nil {
		return AutomationGateDecision{}, err
	}
	if decision.SchemaVersion != AutomationGateDecisionVersion {
		return AutomationGateDecision{}, NewAutomationValidationError(
			"gate_response.schema_version",
			"is unsupported",
		)
	}
	decision.Reason = strings.TrimSpace(decision.Reason)
	if decision.Reason == "" || len(decision.Reason) > automationGateMaxReasonBytes {
		return AutomationGateDecision{}, NewAutomationValidationError(
			"gate_response.reason",
			"is required and must not exceed 1000 bytes",
		)
	}
	if decision.Confidence < 0 || decision.Confidence > 1 {
		return AutomationGateDecision{}, NewAutomationValidationError(
			"gate_response.confidence",
			"must be between 0 and 1",
		)
	}
	digest := sha256.Sum256(data)
	decision.Model = p.model
	decision.RawSHA256 = hex.EncodeToString(digest[:])
	return decision, nil
}

func automationOpenAIGateText(
	protocol AutomationProviderProtocol,
	data []byte,
) (string, error) {
	if protocol != AutomationProviderResponses {
		var response struct {
			Choices []struct {
				Message struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(data, &response); err != nil || len(response.Choices) == 0 {
			return "", ErrOpenAIAutomationMalformedResponse
		}
		return automationOpenAIContentText(response.Choices[0].Message.Content)
	}
	var response struct {
		Status string            `json:"status"`
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Output == nil {
		return "", ErrOpenAIAutomationMalformedResponse
	}
	if response.Status != "" && response.Status != "completed" {
		return "", ErrOpenAIAutomationRequestFailed
	}
	var text strings.Builder
	for _, raw := range response.Output {
		var item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return "", ErrOpenAIAutomationMalformedResponse
		}
		switch item.Type {
		case "reasoning":
			continue
		case "message":
			for _, part := range item.Content {
				if part.Type != "output_text" {
					return "", ErrOpenAIAutomationMalformedResponse
				}
				text.WriteString(part.Text)
			}
		default:
			return "", ErrOpenAIAutomationMalformedResponse
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", errors.New("automation gate returned no decision")
	}
	return text.String(), nil
}

func buildAutomationGateInput(
	runID string,
	event MessageEvent,
	route RecruitingAutomationRoute,
) AutomationGateInput {
	return AutomationGateInput{
		SchemaVersion: AutomationGateInputVersion,
		RunID:         runID,
		Recruiting:    automationRecruitingContext(route),
		Message:       automationMessageContext(event),
		GateMode:      route.SourceGroup.GateMode,
	}
}
