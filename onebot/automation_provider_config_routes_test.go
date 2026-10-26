package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAutomationProviderConfigRoutesRedactAndTestOnce(t *testing.T) {
	t.Setenv("ONEBOT_AI_ENDPOINT", "")
	t.Setenv("ONEBOT_AI_MODEL", "")
	t.Setenv("ONEBOT_AI_API_KEY", "")
	var calls atomic.Int32
	client := &http.Client{Transport: openAIRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		var body openAIChatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 2 {
			t.Fatalf("synthetic request = %#v", body)
		}
		if body.Model == "route-gate-model" {
			if body.Messages[0].Content != "Synthetic gate prompt." ||
				!strings.Contains(body.Messages[1].Content, AutomationGateInputVersion) {
				t.Fatalf("synthetic gate request = %#v", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body: io.NopCloser(strings.NewReader(
					`{"choices":[{"message":{"content":"{\"schema_version\":\"automation.gate.v1\",\"allow\":true,\"reason\":\"synthetic gate accepted\",\"confidence\":1}"}}]}`,
				)),
				Header: make(http.Header),
			}, nil
		}
		if body.Model != "route-model" ||
			!strings.Contains(body.Messages[1].Content, `"allowed_tools":[]`) {
			t.Fatalf("synthetic planner request = %#v", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(
				`{"choices":[{"message":{"content":"{\"schema_version\":\"automation.actions.v1\",\"actions\":[]}"}}]}`,
			)),
			Header: make(http.Header),
		}, nil
	})}

	store, err := openAutomationProviderConfigStore(
		t.TempDir(),
		&automationProviderTestKeychainRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}
	store.testClient = client
	dynamic := NewDynamicAutomationProvider(noopAutomationProvider{})
	mux := http.NewServeMux()
	registerAutomationProviderConfigRoutes(mux, store, dynamic, "operator")

	key := "route-secret"
	put := automationProviderRouteRequest(t, mux, http.MethodPut, "/api/automation/provider", map[string]any{
		"expected_version":     0,
		"enabled":              true,
		"endpoint":             "https://api.example.test/v1/chat/completions",
		"model":                "route-model",
		"gate_model":           "route-gate-model",
		"timeout_seconds":      5,
		"prompt_template":      "Synthetic planner prompt.",
		"gate_prompt_template": "Synthetic gate prompt.",
		"api_key":              key,
	})
	if put.Code != http.StatusOK || strings.Contains(put.Body.String(), key) {
		t.Fatalf("PUT status/body = %d %s", put.Code, put.Body.String())
	}
	get := automationProviderRouteRequest(t, mux, http.MethodGet, "/api/automation/provider", nil)
	if get.Code != http.StatusOK || strings.Contains(get.Body.String(), key) ||
		!strings.Contains(get.Body.String(), `"api_key_configured":true`) ||
		!strings.Contains(get.Body.String(), `"gate_model":"route-gate-model"`) ||
		!strings.Contains(get.Body.String(), `"gate_prompt_template":"Synthetic gate prompt."`) {
		t.Fatalf("GET status/body = %d %s", get.Code, get.Body.String())
	}
	testResponse := automationProviderRouteRequest(t, mux, http.MethodPost, "/api/automation/provider/test", map[string]any{
		"endpoint":             "https://api.example.test/v1/chat/completions",
		"model":                "route-model",
		"gate_model":           "route-gate-model",
		"timeout_seconds":      5,
		"prompt_template":      "Synthetic planner prompt.",
		"gate_prompt_template": "Synthetic gate prompt.",
	})
	if testResponse.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("test status/calls = %d %d body=%s", testResponse.Code, calls.Load(), testResponse.Body.String())
	}
}

func automationProviderRouteRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var buffer bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buffer).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(method, path, &buffer)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("Authorization", "Bearer operator")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}
