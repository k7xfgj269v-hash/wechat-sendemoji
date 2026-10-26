package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAutomationToolRouteRequiresOperatorAuth(t *testing.T) {
	mux := automationToolRouteTestMux()

	unauthorized := automationToolRouteTestRequest(mux, http.MethodGet, "", "/api/automation/tools")
	if unauthorized.Code != http.StatusUnauthorized ||
		unauthorized.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("unauthorized status/auth = %d %q", unauthorized.Code, unauthorized.Header().Get("WWW-Authenticate"))
	}
	agent := automationToolRouteTestRequest(mux, http.MethodGet, "agent-secret", "/api/automation/tools")
	if agent.Code != http.StatusUnauthorized {
		t.Fatalf("agent token status = %d, want %d", agent.Code, http.StatusUnauthorized)
	}
	operator := automationToolRouteTestRequest(mux, http.MethodGet, "operator-secret", "/api/automation/tools")
	if operator.Code != http.StatusOK {
		t.Fatalf("operator status/body = %d %s", operator.Code, operator.Body.String())
	}
}

func TestAutomationToolRouteReturnsDeterministicSanitizedCatalog(t *testing.T) {
	mux := automationToolRouteTestMux()
	first := automationToolRouteTestRequest(mux, http.MethodGet, "operator-secret", "/api/automation/tools")
	second := automationToolRouteTestRequest(mux, http.MethodGet, "operator-secret", "/api/automation/tools")
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("GET statuses = %d, %d", first.Code, second.Code)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("catalog route response is not deterministic")
	}
	if first.Header().Get("Content-Type") != "application/json; charset=utf-8" ||
		first.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("catalog headers = %#v", first.Header())
	}

	var got WorkbenchCatalog
	if err := json.Unmarshal(first.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := WorkbenchToolCatalog()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog response = %#v, want %#v", got, want)
	}
	if len(got.Tools) != 3 {
		t.Fatalf("catalog tools = %d, want 3", len(got.Tools))
	}
	for i, tool := range got.Tools {
		if tool.Name != WorkbenchProviderTools()[i] {
			t.Fatalf("catalog tool[%d] = %q, want %q", i, tool.Name, WorkbenchProviderTools()[i])
		}
		if !IsWorkbenchToolName(tool.Name) {
			t.Fatalf("catalog exposed legacy tool %q", tool.Name)
		}
	}
	wantExclusions := []string{
		"self_modification",
		"secrets",
		"exports",
		"raw_attachments",
		"arbitrary_execution",
		"runtime_targets",
		"payment_execution",
	}
	if len(got.Exclusions) != len(wantExclusions) {
		t.Fatalf("catalog exclusions = %d, want %d", len(got.Exclusions), len(wantExclusions))
	}
	for i, exclusion := range got.Exclusions {
		if exclusion.Class != wantExclusions[i] || exclusion.Description == "" {
			t.Fatalf("catalog exclusion[%d] = %#v", i, exclusion)
		}
	}
}

func TestAutomationToolRouteRejectsParametersAndPost(t *testing.T) {
	mux := automationToolRouteTestMux()

	query := automationToolRouteTestRequest(
		mux,
		http.MethodGet,
		"operator-secret",
		"/api/automation/tools?include=secrets",
	)
	if query.Code != http.StatusBadRequest ||
		!strings.Contains(query.Body.String(), `"code":"validation_error"`) {
		t.Fatalf("query status/body = %d %s", query.Code, query.Body.String())
	}
	post := automationToolRouteTestRequest(mux, http.MethodPost, "operator-secret", "/api/automation/tools")
	if post.Code != http.StatusMethodNotAllowed ||
		post.Header().Get("Allow") != http.MethodGet ||
		!strings.Contains(post.Body.String(), `"code":"method_not_allowed"`) {
		t.Fatalf("POST status/allow/body = %d %q %s", post.Code, post.Header().Get("Allow"), post.Body.String())
	}
}

func automationToolRouteTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerAutomationRoutes(mux, nil, "operator-secret", "agent-secret")
	return mux
}

func automationToolRouteTestRequest(
	handler http.Handler,
	method string,
	token string,
	target string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.RemoteAddr = "127.0.0.1:12345"
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
