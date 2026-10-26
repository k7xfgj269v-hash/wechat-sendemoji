package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

func (e *defaultAutomationRunEngine) runChatStream(ctx context.Context, runID, turnID string) error {
	loopTimeout := automationChatLoopTimeout
	configuredTimeout := e.runTimeout()*automationChatMaxProviderRounds + automationChatQueryTotalTimeout
	if configuredTimeout > loopTimeout {
		loopTimeout = configuredTimeout
	}
	loopCtx, cancel := context.WithTimeout(ctx, loopTimeout)
	defer cancel()

	var run AutomationRun
	var turn AutomationTurn
	err := e.supervision.chat.withLock(func() error {
		return e.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			runIndex := automationRunIndex(state, runID)
			storedTurn := automationTurnByID(state, turnID)
			if runIndex < 0 || storedTurn == nil {
				return errors.New("automation chat run not found")
			}
			if storedTurn.Status == AutomationTurnCancelled {
				return context.Canceled
			}
			if state.Runs[runIndex].Status != AutomationRunQueued || storedTurn.Status != AutomationTurnQueued {
				return NewAutomationValidationError("turn", "is not queued")
			}
			now := time.Now().UTC()
			state.Runs[runIndex].Status = AutomationRunRunning
			state.Runs[runIndex].Version++
			state.Runs[runIndex].StartedAt = &now
			storedTurn.Status = AutomationTurnRunning
			storedTurn.Version++
			storedTurn.StartedAt = &now
			storedTurn.UpdatedAt = now
			run = state.Runs[runIndex]
			turn = *storedTurn
			_, err := appendAutomationTurnEvent(
				state, allocator, turn.SessionID, turn.ID, AutomationTurnEventStarted,
				map[string]any{"turn": turn, "run_id": run.ID, "actor_type": "system"}, now,
			)
			return err
		})
	})
	if err != nil {
		return err
	}
	e.supervision.chat.publishPersisted(turn.ID)

	provider, err := e.providerForRun(run)
	if err != nil {
		return wrapAutomationChatLoopError(
			"provider",
			"prompt_revision_unavailable",
			err,
		)
	}
	event, _ := e.chatMessageEventWithStats(turn, run)
	providerEvent := event
	providerEvent.MessageKey = MessageKeyForEvent(providerEvent)
	input, contextCompaction, err := buildAutomationChatProviderInputWithStats(
		run.ID,
		providerEvent,
		e.crm.Snapshot(),
	)
	if err != nil {
		return err
	}
	input, semanticCompaction, err := e.prepareAutomationChatContext(
		loopCtx,
		provider,
		run,
		turn,
		input,
	)
	if err != nil {
		return err
	}
	historyCompaction := automationChatHistoryCompactionStats{
		HistoricalTurns: semanticCompaction.EligibleTurns,
		IncludedTurns:   semanticCompaction.EligibleTurns,
		TargetRunes:     automationChatContextTargetRunes,
	}
	if semanticCompaction.Succeeded {
		historyCompaction.IncludedTurns = semanticCompaction.RecentTurns
		historyCompaction.OmittedTurns = semanticCompaction.CompactedTurns
		historyCompaction.Compacted = true
	}
	limits := automationChatLimits{actionCount: len(run.ActionIDs)}
	seenCallIDs := make(map[string]struct{})
	deniedQueryOperations := make(map[WorkbenchOperation]struct{})
	var continuation []AutomationProviderMessage
	var continuationState json.RawMessage
	var totalUsage AutomationProviderUsage
	sawUsage := false

	for providerRound := 1; providerRound <= automationChatMaxProviderRounds; providerRound++ {
		if err := loopCtx.Err(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return newAutomationChatLoopError(
				"provider",
				automationChatErrorLoopTimeout,
				"automation chat loop timed out",
			)
		}
		input.Continuation = append([]AutomationProviderMessage(nil), continuation...)
		input.ContinuationState = append(json.RawMessage(nil), continuationState...)
		roundCompaction, err := boundAutomationProviderInput(
			&input,
			len(input.Candidates) != 0,
		)
		if err != nil {
			return err
		}
		auditCounters := map[string]any{
			"provider_name":        run.ProviderName,
			"turn_id":              turn.ID,
			"round":                providerRound,
			"tool_count":           len(input.AllowedTools),
			"context_compaction":   contextCompaction,
			"history_compaction":   historyCompaction,
			"semantic_compaction":  semanticCompaction,
			"round_compaction":     roundCompaction,
			"continuation_count":   len(input.Continuation),
			"query_count":          limits.queryCalls,
			"query_result_bytes":   limits.queryResultBytes,
			"query_elapsed_millis": limits.queryElapsed.Milliseconds(),
		}
		if err := e.recordRunEvent(
			AutomationAuditEventProviderStarted,
			run,
			automationRuntimeRunData(run, auditCounters),
		); err != nil {
			return err
		}

		var output AutomationProviderOutput
		var textDeltas []string
		stream, streamErr := provider.Stream(loopCtx, input)
		if streamErr == nil {
			func() {
				defer stream.Close()
				for {
					providerEvent, recvErr := stream.Recv()
					if recvErr != nil {
						if errors.Is(recvErr, io.EOF) &&
							(output.RawSHA256 != "" ||
								output.AssistantText != "" ||
								output.ToolCalls != nil) {
							streamErr = nil
							return
						}
						streamErr = recvErr
						return
					}
					switch providerEvent.Type {
					case "text_delta":
						if providerEvent.TextDelta != "" {
							textDeltas = append(textDeltas, providerEvent.TextDelta)
						}
					case "tool_call_delta":
						// The provider sends cumulative previews. Persist the final
						// preview once after completion instead of rewriting the
						// full automation snapshot for every streamed fragment.
					case "completed":
						if providerEvent.Output == nil {
							streamErr = NewAutomationValidationError("provider", "completed without output")
							return
						}
						output = *providerEvent.Output
						return
					}
				}
			}()
		}
		if streamErr != nil {
			_ = e.recordRunEvent(
				AutomationAuditEventProviderCompleted,
				run,
				automationRuntimeRunData(run, map[string]any{
					"round": providerRound, "continuation_count": len(input.Continuation),
					"query_count": limits.queryCalls, "query_result_bytes": limits.queryResultBytes,
					"succeeded": false, "turn_id": turn.ID,
				}),
			)
			if loopCtx.Err() != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return newAutomationChatLoopError(
					"provider",
					automationChatErrorLoopTimeout,
					"automation chat loop timed out",
				)
			}
			return streamErr
		}
		if output.Usage != nil {
			addAutomationProviderUsage(&totalUsage, output.Usage)
			sawUsage = true
		}
		for _, call := range output.ToolCalls {
			if _, err := e.supervision.chat.appendEvent(
				turn.SessionID, turn.ID, AutomationTurnEventToolCallDelta,
				map[string]any{
					"call_id":            call.CallID,
					"tool":               call.Tool,
					"arguments_fragment": string(call.Arguments),
					"actor_type":         "provider", "actor_id": run.ProviderName,
				},
				time.Now().UTC(),
			); err != nil {
				return err
			}
		}

		responseData, err := json.Marshal(output)
		if err != nil {
			return err
		}
		responseHash := output.RawSHA256
		if responseHash == "" {
			digest := sha256.Sum256(responseData)
			responseHash = hex.EncodeToString(digest[:])
		}
		roundQueryCalls := 0
		for _, call := range output.ToolCalls {
			if call.Tool == AutomationToolWorkbenchQuery {
				roundQueryCalls++
			}
		}
		if err := e.recordRunEvent(
			AutomationAuditEventProviderCompleted,
			run,
			automationRuntimeRunData(run, map[string]any{
				"round": providerRound, "continuation_count": len(input.Continuation),
				"query_count":        limits.queryCalls + roundQueryCalls,
				"query_result_bytes": limits.queryResultBytes,
				"response_bytes":     len(responseData), "response_sha256": responseHash,
				"usage":     output.Usage,
				"succeeded": true, "turn_id": turn.ID,
			}),
		); err != nil {
			return err
		}

		round, err := canonicalizeAutomationChatRound(output.ToolCalls, seenCallIDs)
		if err != nil {
			return err
		}
		output.ToolCalls = round.calls
		output, err = NormalizeAutomationProviderOutput(output, input.AllowedTools)
		if err != nil {
			return wrapAutomationChatLoopError("validation", "provider_response_invalid", err)
		}
		round.calls = output.ToolCalls
		envelope := AutomationToolEnvelope{
			SchemaVersion:    AutomationToolEnvelopeVersion,
			AssistantMessage: output.AssistantText,
			Actions:          round.calls,
		}
		if err := ValidateAutomationEnvelopeEvidence(envelope, automationEvidenceResolver(input)); err != nil {
			return wrapAutomationChatLoopError("validation", "provider_response_invalid", err)
		}
		if round.kind == automationChatRoundQuery &&
			providerRound > automationChatMaxQueryRounds {
			return newAutomationChatLoopError(
				"provider",
				automationChatErrorProviderRoundLimit,
				fmt.Sprintf(
					"provider requested continuation after %d query rounds",
					automationChatMaxQueryRounds,
				),
			)
		}
		if err := limits.reserveRound(round); err != nil {
			return err
		}

		switch round.kind {
		case automationChatRoundFinal:
			var usage *AutomationProviderUsage
			if sawUsage {
				usage = &totalUsage
			}
			_, _, err = e.persistChatOutput(
				loopCtx, run, turn, event, envelope, responseHash, true, textDeltas, usage,
			)
			if err != nil {
				return err
			}
			return nil

		case automationChatRoundSideEffect:
			var usage *AutomationProviderUsage
			if sawUsage {
				usage = &totalUsage
			}
			executeIDs, actionIDs, err := e.persistChatOutput(
				loopCtx, run, turn, event, envelope, responseHash, true, textDeltas, usage,
			)
			if err != nil {
				return err
			}
			limits.actionCount += len(actionIDs)
			e.supervision.publishPersistedActions(actionIDs)
			for _, actionID := range executeIDs {
				if err := loopCtx.Err(); err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return newAutomationChatLoopError(
						"provider",
						automationChatErrorLoopTimeout,
						"automation chat loop timed out",
					)
				}
				if err := e.supervision.executeSupervisedAction(loopCtx, actionID); err != nil {
					return err
				}
			}
			e.supervision.reconcileChatTurnForRun(run.ID)
			return nil

		case automationChatRoundQuery:
			results := make([]WorkbenchToolResult, 0, len(round.calls))
			for _, call := range round.calls {
				queryEnvelope := AutomationToolEnvelope{
					SchemaVersion: AutomationToolEnvelopeVersion,
					Actions:       []AutomationToolCall{call},
				}
				executeIDs, actionIDs, err := e.persistChatOutput(
					loopCtx, run, turn, event, queryEnvelope, "", false, nil, nil,
				)
				if err != nil {
					return err
				}
				limits.actionCount += len(actionIDs)
				e.supervision.publishPersistedActions(actionIDs)
				if len(actionIDs) != 1 {
					return newAutomationChatLoopError(
						"continuation",
						automationChatErrorContinuationInvalid,
						"query action was not persisted as a single supervised action",
					)
				}
				if len(executeIDs) == 0 {
					// Policy denied this query action (no execution id). Surface the
					// denial to the provider as a forbidden tool result and continue,
					// instead of failing the whole turn.
					result, resultBytes, err := automationChatDeniedQueryResult(
						e.store.Snapshot(),
						actionIDs[0],
					)
					if err != nil {
						return err
					}
					if _, exists := deniedQueryOperations[result.Operation]; exists {
						return newAutomationChatLoopError(
							"continuation",
							automationChatErrorQueryDeniedRepeat,
							fmt.Sprintf("query operation %q was denied more than once", result.Operation),
						)
					}
					deniedQueryOperations[result.Operation] = struct{}{}
					results = append(results, result)
					if err := limits.addQueryResult(resultBytes); err != nil {
						return err
					}
					continue
				}
				if len(executeIDs) != 1 || actionIDs[0] != executeIDs[0] {
					return newAutomationChatLoopError(
						"continuation",
						automationChatErrorContinuationInvalid,
						"query action was not approved for supervised execution",
					)
				}

				queryCtx, queryCancel, queryStarted, err := limits.queryContext(loopCtx)
				if err != nil {
					return err
				}
				executeErr := e.supervision.executeSupervisedAction(queryCtx, executeIDs[0])
				queryContextErr := queryCtx.Err()
				queryCancel()
				if loopCtx.Err() != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					return newAutomationChatLoopError(
						"provider",
						automationChatErrorLoopTimeout,
						"automation chat loop timed out",
					)
				}
				if err := limits.finishQuery(queryStarted, queryContextErr); err != nil {
					return err
				}
				if executeErr != nil {
					return executeErr
				}
				result, resultBytes, err := automationChatQueryResult(
					e.store.Snapshot(),
					actionIDs[0],
				)
				if err != nil {
					return err
				}
				results = append(results, result)
				if err := limits.addQueryResult(resultBytes); err != nil {
					return err
				}
			}
			continuation, err = appendAutomationChatContinuation(
				continuation,
				output.AssistantText,
				round.calls,
				results,
			)
			if err != nil {
				return err
			}
			continuationState = append(json.RawMessage(nil), output.ContinuationState...)
		}
	}
	return newAutomationChatLoopError(
		"provider",
		automationChatErrorProviderRoundLimit,
		"provider round limit reached",
	)
}

func (e *defaultAutomationRunEngine) persistChatOutput(
	ctx context.Context,
	run AutomationRun,
	turn AutomationTurn,
	event MessageEvent,
	envelope AutomationToolEnvelope,
	responseHash string,
	finalize bool,
	textDeltas []string,
	usage *AutomationProviderUsage,
) ([]string, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	var executeIDs []string
	var actionIDs []string
	var persistedEvents []AutomationTurnEvent
	err := e.supervision.chat.withLock(func() error {
		return e.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
			runIndex := automationRunIndex(state, run.ID)
			storedTurn := automationTurnByID(state, turn.ID)
			if runIndex < 0 || storedTurn == nil {
				return errors.New("automation chat run not found")
			}
			if storedTurn.Status == AutomationTurnCancelled {
				return context.Canceled
			}
			if state.Runs[runIndex].Status != AutomationRunRunning {
				return NewAutomationValidationError("run", "is no longer running")
			}
			totalActionCount := len(state.Runs[runIndex].ActionIDs) + len(envelope.Actions)
			if totalActionCount > AutomationMaxActionsPerRun {
				return NewAutomationValidationError(
					"actions",
					fmt.Sprintf("must contain at most %d entries", AutomationMaxActionsPerRun),
				)
			}
			policy := state.Policy
			source := AutomationSourceMessageRef{
				EventID: event.ID, MessageKey: event.MessageKey, ExpectedVersion: event.Version,
				ChatType: event.ChatType, PeerID: event.PeerID,
			}
			callToAction := make(map[string]string, len(envelope.Actions))
			actionIDs = make([]string, len(envelope.Actions))
			for i := range envelope.Actions {
				id, err := allocator.Next(AutomationIDKindAction)
				if err != nil {
					return err
				}
				actionIDs[i] = id
				callToAction[envelope.Actions[i].CallID] = id
			}
			now := time.Now().UTC()
			for i, call := range envelope.Actions {
				operation := WorkbenchOperation("")
				payload := append(json.RawMessage(nil), call.Arguments...)
				if IsWorkbenchToolName(call.Tool) {
					decoded, err := DecodeWorkbenchToolRequest(call.Tool, call.Arguments)
					if err != nil {
						return err
					}
					operation = decoded.Request.Operation
					payload = append(json.RawMessage(nil), decoded.CanonicalRequest...)
					call.Arguments = payload
				}
				preconditions, dependencies, err := automationActionReferences(call, callToAction)
				if err != nil {
					return err
				}
				policyResult := e.risk.Evaluate(policy, AutomationPolicyInput{
					Tool: call.Tool, Confidence: call.Confidence, Payload: payload,
					SourceMessage: source, RunActionCount: totalActionCount,
				})
				status := AutomationActionAwaitingReview
				switch policyResult.Decision {
				case AutomationDecisionAutoComplete:
					status = AutomationActionSucceeded
				case AutomationDecisionAutoApprove:
					status = AutomationActionApproved
				case AutomationDecisionDeny:
					status = AutomationActionDenied
				}
				action := AutomationAction{
					ID: actionIDs[i], RunID: run.ID, ProviderCallID: call.CallID,
					Tool: call.Tool, Operation: operation, Status: status, Risk: policyResult.Risk,
					PolicyDecision: policyResult.Decision, PolicyVersion: policy.Version,
					RiskReasons: append([]string(nil), policyResult.RiskReasons...),
					Confidence:  call.Confidence, Evidence: append([]AutomationEvidence{}, call.Evidence...),
					SourceMessage: source, Preconditions: preconditions,
					DependsOnActionIDs: dependencies, Recipient: policyResult.Recipient,
					Payload: payload,
					Attempt: 1, Version: 1, CreatedAt: now, UpdatedAt: now,
				}
				if status == AutomationActionSucceeded {
					action.Result = append(json.RawMessage(nil), payload...)
				}
				ruleMatched := false
				if policyResult.Decision == AutomationDecisionReview {
					effect, _, matched := matchAutomationPermissionRule(state, action)
					if matched {
						ruleMatched = true
						if effect == AutomationPermissionEffectAllow {
							action.Status = AutomationActionApproved
						} else {
							action.Status = AutomationActionDenied
						}
					}
				}
				state.Actions = append(state.Actions, action)
				if _, err := appendAutomationSupervisionAudit(
					state, allocator, AutomationAuditEventActionProposed, action, "", "", now,
				); err != nil {
					return err
				}
				switch {
				case action.Status == AutomationActionApproved:
					executeIDs = append(executeIDs, action.ID)
					if ruleMatched {
						if _, err := appendAutomationSupervisionAudit(
							state, allocator, AutomationAuditEventPermissionAllowed, action, "", "", now,
						); err != nil {
							return err
						}
					}
				case action.PolicyDecision == AutomationDecisionReview &&
					action.Status == AutomationActionAwaitingReview:
					if _, err := PersistAutomationPermissionRequest(state, allocator, action, now); err != nil {
						return err
					}
				case action.Status == AutomationActionDenied:
					if _, err := appendAutomationSupervisionAudit(
						state, allocator, AutomationAuditEventPermissionDenied, action, "", "", now,
					); err != nil {
						return err
					}
				}
				persistedEvent, err := appendAutomationTurnEvent(
					state, allocator, turn.SessionID, turn.ID, AutomationTurnEventToolCallProposed,
					map[string]any{
						"action_id": action.ID, "provider_call_id": action.ProviderCallID,
						"tool": action.Tool, "status": action.Status, "risk": action.Risk,
						"action_version": action.Version,
						"actor_type":     "provider", "actor_id": run.ProviderName,
					}, now,
				)
				if err != nil {
					return err
				}
				persistedEvents = append(persistedEvents, persistedEvent)
			}
			state.Runs[runIndex].ActionIDs = append(
				state.Runs[runIndex].ActionIDs,
				actionIDs...,
			)
			state.Runs[runIndex].Version++
			if finalize {
				completed := time.Now().UTC()
				state.Runs[runIndex].Status = AutomationRunSucceeded
				state.Runs[runIndex].CompletedAt = &completed
				state.Runs[runIndex].ProviderResponseSHA256 = responseHash
				storedTurn.AssistantText = envelope.AssistantMessage
				if usage != nil {
					clonedUsage := *usage
					storedTurn.ProviderUsage = &clonedUsage
				}
				storedTurn.Version++
				storedTurn.UpdatedAt = completed
				if len(textDeltas) == 0 && envelope.AssistantMessage != "" {
					textDeltas = []string{envelope.AssistantMessage}
				}
				for _, delta := range textDeltas {
					if delta == "" {
						continue
					}
					persistedEvent, err := appendAutomationTurnEvent(
						state,
						allocator,
						storedTurn.SessionID,
						storedTurn.ID,
						AutomationTurnEventAssistantTextDelta,
						map[string]any{
							"text_delta": delta,
							"actor_type": "provider",
							"actor_id":   run.ProviderName,
						},
						completed,
					)
					if err != nil {
						return err
					}
					persistedEvents = append(persistedEvents, persistedEvent)
				}
				if len(actionIDs) == 0 {
					_, completedEvent, err := reconcileSucceededChatTurn(
						state,
						allocator,
						&state.Runs[runIndex],
						storedTurn,
					)
					if err != nil {
						return err
					}
					if completedEvent.Sequence != 0 {
						persistedEvents = append(persistedEvents, completedEvent)
					}
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, nil, err
	}
	e.supervision.chat.publishPersistedEvents(persistedEvents)
	return executeIDs, actionIDs, nil
}

func (e *defaultAutomationRunEngine) chatMessageEvent(turn AutomationTurn, run AutomationRun) MessageEvent {
	event, _ := e.chatMessageEventWithStats(turn, run)
	return event
}

func (e *defaultAutomationRunEngine) chatMessageEventWithStats(
	turn AutomationTurn,
	run AutomationRun,
) (MessageEvent, automationChatHistoryCompactionStats) {
	text := turn.UserText
	stats := automationChatHistoryCompactionStats{
		FinalRunes:  len([]rune(text)),
		TargetRunes: automationChatContextTargetRunes,
	}
	now := turn.CreatedAt
	event := MessageEvent{
		ID: turn.ID, Source: "automation_chat", ChatType: ChatTypePrivate,
		PeerID: "automation_chat", AuthorID: "automation_chat_user",
		Text: text, Kinds: []string{"text"}, Timestamp: now.Unix(),
		ReviewStatus: ReviewStatusUnreviewed, Version: run.SourceMessageVersion,
		CreatedAt: now, UpdatedAt: now,
	}
	event.MessageKey = run.SourceMessageKey
	return event, stats
}
