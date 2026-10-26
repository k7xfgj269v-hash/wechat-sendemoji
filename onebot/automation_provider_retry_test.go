package main

import (
	"context"
	"net/http"
	"testing"
)

func TestAutomationProviderEffectiveAPIKeyFallsBackToEnv(t *testing.T) {
	t.Setenv("ONEBOT_AI_API_KEY", "env-key")
	if got := automationProviderEffectiveAPIKey("stored"); got != "stored" {
		t.Fatalf("stored key = %q, want stored", got)
	}
	if got := automationProviderEffectiveAPIKey("   "); got != "env-key" {
		t.Fatalf("blank stored fallback = %q, want env-key", got)
	}
	t.Setenv("ONEBOT_AI_API_KEY", "")
	if got := automationProviderEffectiveAPIKey(""); got != "" {
		t.Fatalf("empty both = %q, want empty", got)
	}
}

func TestAutomationProviderRetryableError(t *testing.T) {
	for _, code := range []int{401, 408, 429, 500, 502, 503, 504} {
		if !automationProviderRetryableError(&OpenAIAutomationHTTPError{StatusCode: code}) {
			t.Fatalf("status %d should be retryable", code)
		}
	}
	for _, code := range []int{400, 403, 404, 422} {
		if automationProviderRetryableError(&OpenAIAutomationHTTPError{StatusCode: code}) {
			t.Fatalf("status %d should not be retryable", code)
		}
	}
	if !automationProviderRetryableError(ErrOpenAIAutomationRequestFailed) {
		t.Fatal("network failure should be retryable")
	}
	if automationProviderRetryableError(nil) {
		t.Fatal("nil should not be retryable")
	}
}

func TestOpenAIAutomationProviderRetriesRetryableStatus(t *testing.T) {
	attempts := 0
	client := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		return openAIProviderTestResponse(http.StatusUnauthorized, "{}"), nil
	})}
	provider, err := NewOpenAIAutomationProviderWithProtocol(
		AutomationProviderChatCompletions,
		"https://api.example.test/v1/provider", "test-model", "secret-key", client,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, genErr := provider.Generate(context.Background(), openAIProviderTestInput()); genErr == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if attempts != automationProviderMaxAttempts {
		t.Fatalf("401 attempts = %d, want %d", attempts, automationProviderMaxAttempts)
	}

	attempts = 0
	clientNotFound := &http.Client{Transport: openAIRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		return openAIProviderTestResponse(http.StatusNotFound, "{}"), nil
	})}
	providerNotFound, err := NewOpenAIAutomationProviderWithProtocol(
		AutomationProviderChatCompletions,
		"https://api.example.test/v1/provider", "test-model", "secret-key", clientNotFound,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, genErr := providerNotFound.Generate(context.Background(), openAIProviderTestInput()); genErr == nil {
		t.Fatal("expected error for non-retryable 404")
	}
	if attempts != 1 {
		t.Fatalf("404 attempts = %d, want 1", attempts)
	}
}
