package main

import (
	"encoding/json"
	"strings"
)

func automationOpenAIProviderUsage(raw json.RawMessage) (*AutomationProviderUsage, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var wire openAIProviderUsageWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, ErrOpenAIAutomationMalformedResponse
	}
	input := wire.InputTokens
	if input == 0 {
		input = wire.PromptTokens
	}
	output := wire.OutputTokens
	if output == 0 {
		output = wire.CompletionTokens
	}
	cached := wire.InputTokenDetails.CachedTokens
	if cached == 0 {
		cached = wire.PromptTokenDetails.CachedTokens
	}
	total := wire.TotalTokens
	if total == 0 {
		total = input + output
	}
	usage := &AutomationProviderUsage{
		InputTokens:       input,
		CachedInputTokens: cached,
		OutputTokens:      output,
		TotalTokens:       total,
	}
	if err := validateAutomationProviderUsage("usage", usage); err != nil {
		return nil, ErrOpenAIAutomationMalformedResponse
	}
	return usage, nil
}
