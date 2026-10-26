package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type automationRunRouteTestEngine struct {
	runID           string
	expectedVersion uint64
	operatorID      string
	reason          string
}

func (*automationRunRouteTestEngine) EnsureRun(
	context.Context,
	MessageEvent,
	string,
) (AutomationRun, bool, error) {
	return AutomationRun{}, false, nil
}

func (e *automationRunRouteTestEngine) RetryRun(
	_ context.Context,
	runID string,
	expectedVersion uint64,
	operatorID string,
	reason string,
) (AutomationRun, error) {
	e.runID = runID
	e.expectedVersion = expectedVersion
	e.operatorID = operatorID
	e.reason = reason
	return AutomationRun{
		ID: "airun_00000002", RetryOfRunID: runID,
		Status: AutomationRunQueued, Version: 1, Attempt: 2,
	}, nil
}

func (*automationRunRouteTestEngine) ProcessRun(context.Context, string) error {
	return nil
}

func (*automationRunRouteTestEngine) ProcessQueued(context.Context) error {
	return nil
}

func (*automationRunRouteTestEngine) RecoverScheduledActions(context.Context) error {
	return nil
}

func TestAutomationRunRetryRoute(t *testing.T) {
	engine := &automationRunRouteTestEngine{}
	handler := automationOperatorAuth(
		"operator-secret",
		automationRunHandler(&AutomationService{engine: engine}),
	)
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1/api/automation/runs/airun_00000001/retry",
		bytes.NewBufferString(
			`{"expected_version":3,"operator_id":"operator-1","reason":"provider recovered"}`,
		),
	)
	request.Header.Set("Authorization", "Bearer operator-secret")
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if engine.runID != "airun_00000001" ||
		engine.expectedVersion != 3 ||
		engine.operatorID != "operator-1" ||
		engine.reason != "provider recovered" {
		t.Fatalf("retry request = %#v", engine)
	}
}
