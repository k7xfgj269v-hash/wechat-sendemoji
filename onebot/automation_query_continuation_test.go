package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func automationChatTestCall(
	t *testing.T,
	callID string,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) AutomationToolCall {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation:     operation,
		ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	return AutomationToolCall{
		CallID:     callID,
		Tool:       tool,
		Confidence: 1,
		Evidence:   []AutomationEvidence{},
		Arguments:  raw,
	}
}

func automationChatTestErrorCode(t *testing.T, err error) string {
	t.Helper()
	var loopErr *automationChatLoopError
	if !errors.As(err, &loopErr) {
		t.Fatalf("error = %v, want automationChatLoopError", err)
	}
	return loopErr.code
}

func TestCanonicalizeAutomationChatRoundClassifiesAndRejectsMixedCalls(t *testing.T) {
	query := automationChatTestCall(
		t,
		"call_query",
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCandidatesList,
		WorkbenchCandidateListArguments{Limit: 1, Offset: 0},
	)
	query.Arguments = json.RawMessage(
		`{"arguments_json":"{\"offset\":0,\"limit\":1}","operation":"candidates.list"}`,
	)
	mutate := automationChatTestCall(
		t,
		"call_mutate",
		AutomationToolWorkbenchMutate,
		WorkbenchOperationCandidatesCreate,
		WorkbenchCandidateCreateArguments{
			Candidate: CandidateDraft{FullName: "Ada", Skills: []string{"Go"}},
		},
	)

	finalRound, err := canonicalizeAutomationChatRound(nil, map[string]struct{}{})
	if err != nil || finalRound.kind != automationChatRoundFinal {
		t.Fatalf("final round = %+v, %v", finalRound, err)
	}
	queryRound, err := canonicalizeAutomationChatRound(
		[]AutomationToolCall{query},
		map[string]struct{}{},
	)
	if err != nil || queryRound.kind != automationChatRoundQuery ||
		string(queryRound.calls[0].Arguments) == string(query.Arguments) {
		t.Fatalf("query round = %+v, %v", queryRound, err)
	}
	sideEffectRound, err := canonicalizeAutomationChatRound(
		[]AutomationToolCall{mutate},
		map[string]struct{}{},
	)
	if err != nil || sideEffectRound.kind != automationChatRoundSideEffect {
		t.Fatalf("side-effect round = %+v, %v", sideEffectRound, err)
	}
	_, err = canonicalizeAutomationChatRound(
		[]AutomationToolCall{query, mutate},
		map[string]struct{}{},
	)
	if got := automationChatTestErrorCode(t, err); got != automationChatErrorMixedToolRound {
		t.Fatalf("mixed error code = %q", got)
	}
}

func TestCanonicalizeAutomationChatRoundRejectsDuplicateCallIDAcrossRounds(t *testing.T) {
	seen := map[string]struct{}{}
	call := automationChatTestCall(
		t,
		"call_reused",
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCandidatesList,
		WorkbenchCandidateListArguments{Limit: 1, Offset: 0},
	)
	if _, err := canonicalizeAutomationChatRound([]AutomationToolCall{call}, seen); err != nil {
		t.Fatal(err)
	}
	_, err := canonicalizeAutomationChatRound([]AutomationToolCall{call}, seen)
	if got := automationChatTestErrorCode(t, err); got != automationChatErrorDuplicateProviderCallID {
		t.Fatalf("duplicate error code = %q", got)
	}
}

func TestAutomationChatLimitsEnforceQueryAndResultCaps(t *testing.T) {
	limits := automationChatLimits{}
	err := limits.reserveRound(automationChatRound{
		kind:       automationChatRoundQuery,
		queryCalls: automationChatMaxQueryCalls + 1,
	})
	if got := automationChatTestErrorCode(t, err); got != automationChatErrorQueryCallLimit {
		t.Fatalf("query limit error code = %q", got)
	}

	limits = automationChatLimits{}
	if err := limits.addQueryResult(automationChatMaxQueryResultBytes); err != nil {
		t.Fatalf("exact result limit rejected: %v", err)
	}
	err = limits.addQueryResult(1)
	if got := automationChatTestErrorCode(t, err); got != automationChatErrorQueryResultLimit {
		t.Fatalf("result limit error code = %q", got)
	}
}

func TestAppendAutomationChatContinuationIncludesFailedQueryResult(t *testing.T) {
	call := automationChatTestCall(
		t,
		"call_missing",
		AutomationToolWorkbenchQuery,
		WorkbenchOperationCandidatesGet,
		WorkbenchIDArguments{ID: "cand_99999999"},
	)
	round, err := canonicalizeAutomationChatRound(
		[]AutomationToolCall{call},
		map[string]struct{}{},
	)
	if err != nil {
		t.Fatal(err)
	}
	result := NewWorkbenchToolFailure(
		WorkbenchOperationCandidatesGet,
		WorkbenchErrorNotFound,
		"candidate not found",
		false,
	)
	continuation, err := appendAutomationChatContinuation(
		nil,
		"I checked the candidate.",
		round.calls,
		[]WorkbenchToolResult{result},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(continuation) != 2 ||
		continuation[0].Role != AutomationProviderMessageAssistant ||
		continuation[1].Role != AutomationProviderMessageTool ||
		continuation[1].Result == nil ||
		continuation[1].Result.OK {
		t.Fatalf("continuation = %+v", continuation)
	}
}

func TestAutomationChatDeniedQueryResultSurfacesForbidden(t *testing.T) {
	state := AutomationState{
		Actions: []AutomationAction{{
			ID:          "aiact_denied",
			Tool:        AutomationToolWorkbenchQuery,
			Operation:   WorkbenchOperationSystemHealth,
			Status:      AutomationActionDenied,
			RiskReasons: []string{"r4_forbidden_capability"},
		}},
	}

	result, size, err := automationChatDeniedQueryResult(state, "aiact_denied")
	if err != nil {
		t.Fatal(err)
	}
	if result.OK || result.Operation != WorkbenchOperationSystemHealth ||
		result.Error == nil || result.Error.Code != WorkbenchErrorForbidden {
		t.Fatalf("denied result = %+v", result)
	}
	if !strings.Contains(result.Error.Message, "r4_forbidden_capability") {
		t.Fatalf("denied message = %q", result.Error.Message)
	}
	if size <= 0 {
		t.Fatalf("denied size = %d", size)
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		t.Fatalf("denied result invalid: %v", err)
	}

	if _, _, err := automationChatDeniedQueryResult(state, "aiact_missing"); err == nil ||
		automationChatTestErrorCode(t, err) != automationChatErrorContinuationInvalid {
		t.Fatalf("missing action error = %v", err)
	}
}
