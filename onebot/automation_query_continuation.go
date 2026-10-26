package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	automationChatMaxQueryRounds      = 3
	automationChatMaxProviderRounds   = automationChatMaxQueryRounds + 1
	automationChatMaxQueryCalls       = 8
	automationChatMaxQueryResultBytes = 128 << 10
	automationChatQueryTimeout        = 90 * time.Second
	automationChatQueryTotalTimeout   = 180 * time.Second
	automationChatLoopTimeout         = 90 * time.Second
)

const (
	automationChatErrorMixedToolRound          = "mixed_tool_round"
	automationChatErrorProviderRoundLimit      = "provider_round_limit"
	automationChatErrorQueryCallLimit          = "query_call_limit"
	automationChatErrorQueryResultLimit        = "query_result_limit"
	automationChatErrorQueryTimeLimit          = "query_time_limit"
	automationChatErrorLoopTimeout             = "chat_loop_timeout"
	automationChatErrorDuplicateProviderCallID = "duplicate_provider_call_id"
	automationChatErrorQueryDeniedRepeat       = "query_denied_repeat"
	automationChatErrorContinuationInvalid     = "continuation_result_invalid"
)

type automationChatLoopError struct {
	stage string
	code  string
	err   error
}

func (e *automationChatLoopError) Error() string {
	if e == nil || e.err == nil {
		return "automation chat loop failed"
	}
	return e.err.Error()
}

func (e *automationChatLoopError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func newAutomationChatLoopError(stage, code, message string) error {
	return &automationChatLoopError{
		stage: strings.TrimSpace(stage),
		code:  strings.TrimSpace(code),
		err:   errors.New(strings.TrimSpace(message)),
	}
}

func wrapAutomationChatLoopError(stage, code string, err error) error {
	if err == nil {
		return nil
	}
	return &automationChatLoopError{
		stage: strings.TrimSpace(stage),
		code:  strings.TrimSpace(code),
		err:   err,
	}
}

func automationChatFailureDetails(err error) (string, string) {
	var loopErr *automationChatLoopError
	if errors.As(err, &loopErr) {
		return loopErr.stage, loopErr.code
	}
	return "provider", "provider_failed"
}

type automationChatRoundKind uint8

const (
	automationChatRoundFinal automationChatRoundKind = iota
	automationChatRoundQuery
	automationChatRoundSideEffect
)

type automationChatRound struct {
	kind            automationChatRoundKind
	calls           []AutomationToolCall
	queryCalls      int
	sideEffectCalls int
}

func canonicalizeAutomationChatRound(
	calls []AutomationToolCall,
	seenCallIDs map[string]struct{},
) (automationChatRound, error) {
	round := automationChatRound{
		kind:  automationChatRoundFinal,
		calls: make([]AutomationToolCall, len(calls)),
	}
	for i, call := range calls {
		callID := strings.TrimSpace(call.CallID)
		if callID == "" || callID != call.CallID {
			return automationChatRound{}, newAutomationChatLoopError(
				"validation",
				"provider_response_invalid",
				fmt.Sprintf("provider call_id at index %d is invalid", i),
			)
		}
		if _, exists := seenCallIDs[callID]; exists {
			return automationChatRound{}, newAutomationChatLoopError(
				"validation",
				automationChatErrorDuplicateProviderCallID,
				fmt.Sprintf("provider call_id %q was already used", callID),
			)
		}
		seenCallIDs[callID] = struct{}{}

		decoded, err := DecodeWorkbenchToolRequest(call.Tool, call.Arguments)
		if err != nil {
			return automationChatRound{}, wrapAutomationChatLoopError(
				"validation",
				"provider_response_invalid",
				err,
			)
		}
		call.Arguments = append(json.RawMessage(nil), decoded.CanonicalRequest...)
		call.Evidence = append([]AutomationEvidence{}, call.Evidence...)
		round.calls[i] = call
		switch call.Tool {
		case AutomationToolWorkbenchQuery:
			round.queryCalls++
		case AutomationToolWorkbenchMutate, AutomationToolWorkbenchAllowOnce:
			round.sideEffectCalls++
		default:
			return automationChatRound{}, newAutomationChatLoopError(
				"validation",
				"provider_response_invalid",
				fmt.Sprintf("unsupported workbench tool %q", call.Tool),
			)
		}
	}
	if round.queryCalls != 0 && round.sideEffectCalls != 0 {
		return automationChatRound{}, newAutomationChatLoopError(
			"validation",
			automationChatErrorMixedToolRound,
			"provider round mixed query and side-effect calls",
		)
	}
	if round.queryCalls != 0 {
		round.kind = automationChatRoundQuery
	}
	if round.sideEffectCalls != 0 {
		round.kind = automationChatRoundSideEffect
	}
	return round, nil
}

type automationChatLimits struct {
	queryCalls       int
	queryResultBytes int
	queryElapsed     time.Duration
	actionCount      int
}

func (l *automationChatLimits) reserveRound(round automationChatRound) error {
	if l.actionCount+len(round.calls) > AutomationMaxActionsPerRun {
		return newAutomationChatLoopError(
			"validation",
			"provider_response_invalid",
			fmt.Sprintf("run actions exceed the limit of %d", AutomationMaxActionsPerRun),
		)
	}
	if round.kind == automationChatRoundQuery &&
		l.queryCalls+round.queryCalls > automationChatMaxQueryCalls {
		return newAutomationChatLoopError(
			"query",
			automationChatErrorQueryCallLimit,
			fmt.Sprintf("query calls exceed the limit of %d", automationChatMaxQueryCalls),
		)
	}
	l.queryCalls += round.queryCalls
	return nil
}

func (l *automationChatLimits) queryContext(
	parent context.Context,
) (context.Context, context.CancelFunc, time.Time, error) {
	remaining := automationChatQueryTotalTimeout - l.queryElapsed
	if remaining <= 0 {
		return nil, nil, time.Time{}, newAutomationChatLoopError(
			"query",
			automationChatErrorQueryTimeLimit,
			"aggregate query execution time limit reached",
		)
	}
	timeout := automationChatQueryTimeout
	if remaining < timeout {
		timeout = remaining
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	return ctx, cancel, time.Now(), nil
}

func (l *automationChatLimits) finishQuery(started time.Time, queryErr error) error {
	l.queryElapsed += time.Since(started)
	if errors.Is(queryErr, context.DeadlineExceeded) ||
		l.queryElapsed >= automationChatQueryTotalTimeout {
		return newAutomationChatLoopError(
			"query",
			automationChatErrorQueryTimeLimit,
			"query execution time limit reached",
		)
	}
	return nil
}

func (l *automationChatLimits) addQueryResult(size int) error {
	l.queryResultBytes += size
	if l.queryResultBytes > automationChatMaxQueryResultBytes {
		return newAutomationChatLoopError(
			"query",
			automationChatErrorQueryResultLimit,
			fmt.Sprintf(
				"query results reached the limit of %d bytes",
				automationChatMaxQueryResultBytes,
			),
		)
	}
	return nil
}

func automationChatQueryResult(
	state AutomationState,
	actionID string,
) (WorkbenchToolResult, int, error) {
	index := automationActionIndex(&state, actionID)
	if index < 0 {
		return WorkbenchToolResult{}, 0, newAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			"persisted query action was not found",
		)
	}
	action := state.Actions[index]
	result, err := DecodeWorkbenchToolResult(action.Result)
	if err != nil {
		return WorkbenchToolResult{}, 0, wrapAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			err,
		)
	}
	if action.Tool != AutomationToolWorkbenchQuery ||
		action.Operation == "" ||
		result.Operation != action.Operation {
		return WorkbenchToolResult{}, 0, newAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			"persisted query result does not match its action",
		)
	}
	return result, len(action.Result), nil
}

// automationChatDeniedQueryResult synthesizes a forbidden tool result for a
// query action that policy denied (no execution id). Surfacing the denial back
// to the provider lets it choose another approach instead of failing the turn.
func automationChatDeniedQueryResult(
	state AutomationState,
	actionID string,
) (WorkbenchToolResult, int, error) {
	index := automationActionIndex(&state, actionID)
	if index < 0 {
		return WorkbenchToolResult{}, 0, newAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			"denied query action was not found",
		)
	}
	action := state.Actions[index]
	if action.Tool != AutomationToolWorkbenchQuery || action.Operation == "" {
		return WorkbenchToolResult{}, 0, newAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			"denied query action is not a workbench query",
		)
	}
	message := "operation denied by workbench policy"
	if len(action.RiskReasons) != 0 {
		message = fmt.Sprintf(
			"operation denied by workbench policy: %s",
			strings.Join(action.RiskReasons, ", "),
		)
	}
	result := NewWorkbenchToolFailure(
		action.Operation,
		WorkbenchErrorForbidden,
		message,
		false,
	)
	if err := ValidateWorkbenchToolResult(result); err != nil {
		return WorkbenchToolResult{}, 0, wrapAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			err,
		)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return WorkbenchToolResult{}, 0, wrapAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			err,
		)
	}
	return result, len(encoded), nil
}

func appendAutomationChatContinuation(
	continuation []AutomationProviderMessage,
	assistantText string,
	calls []AutomationToolCall,
	results []WorkbenchToolResult,
) ([]AutomationProviderMessage, error) {
	if len(calls) == 0 || len(calls) != len(results) {
		return nil, newAutomationChatLoopError(
			"continuation",
			automationChatErrorContinuationInvalid,
			"query continuation calls and results do not match",
		)
	}
	assistantCalls := make([]AutomationToolCall, len(calls))
	for i, call := range calls {
		call.Arguments = append(json.RawMessage(nil), call.Arguments...)
		call.Evidence = append([]AutomationEvidence{}, call.Evidence...)
		assistantCalls[i] = call
	}
	next := append([]AutomationProviderMessage(nil), continuation...)
	next = append(next, AutomationProviderMessage{
		Role:          AutomationProviderMessageAssistant,
		AssistantText: assistantText,
		ToolCalls:     assistantCalls,
	})
	for i, call := range assistantCalls {
		result := results[i]
		if err := ValidateWorkbenchToolResult(result); err != nil {
			return nil, wrapAutomationChatLoopError(
				"continuation",
				automationChatErrorContinuationInvalid,
				err,
			)
		}
		next = append(next, AutomationProviderMessage{
			Role:       AutomationProviderMessageTool,
			ToolCallID: call.CallID,
			Tool:       call.Tool,
			Result:     &result,
		})
	}
	return next, nil
}
