package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

func (e *defaultAutomationRunEngine) processRun(ctx context.Context, runID string) error {
	pending, ok := automationRunByID(e.store.Snapshot(), runID)
	if !ok {
		return errors.New("automation run not found")
	}
	event, ok := automationFindMessageEvent(e.crm.Snapshot(), pending.SourceMessageEventID)
	if !ok || event.MessageKey != pending.SourceMessageKey {
		return e.failQueuedRun(
			runID,
			"context",
			"source_stale",
			"source message changed before analysis",
		)
	}
	if e.recruitingRouter != nil {
		route, err := e.recruitingRouter.Route(event)
		if err != nil {
			return e.failQueuedRun(runID, "context", "route_snapshot_invalid", err.Error())
		}
		event = route.Event
	}

	var run AutomationRun
	err := e.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		for i := range state.Runs {
			if state.Runs[i].ID != runID {
				continue
			}
			if state.Runs[i].Status != AutomationRunQueued {
				return nil
			}
			if e.recruitingRouter != nil {
				if _, err := reconcileQueuedRunSourceVersion(&state.Runs[i], event); err != nil {
					return err
				}
			}
			now := time.Now().UTC()
			state.Runs[i].Status = AutomationRunRunning
			state.Runs[i].Version++
			state.Runs[i].StartedAt = &now
			run = state.Runs[i]
			return nil
		}
		return errors.New("automation run not found")
	})
	if err != nil {
		return e.failQueuedRun(runID, "context", "source_stale", err.Error())
	}
	if run.ID == "" {
		return err
	}

	event, ok = automationFindMessageEvent(e.crm.Snapshot(), run.SourceMessageEventID)
	if !ok || event.MessageKey != run.SourceMessageKey || event.Version != run.SourceMessageVersion {
		return e.failRun(run.ID, "context", "source_stale", "source message changed before analysis", "")
	}
	if e.recruitingRouter != nil && event.RecruitingProjectVersion == 0 {
		return e.failRun(run.ID, "context", "route_snapshot_missing", "source message has no recruiting route snapshot", "")
	}
	if e.recruitingRouter != nil {
		route := frozenRecruitingAutomationRoute(event)
		route = e.recruitingRouter.applyCurrentGatePolicy(route)
		if route.SourceGroup != nil && route.SourceGroup.GateEnabled {
			if err := e.runSourceGroupGate(ctx, run, event, route); err != nil {
				return err
			}
			current, ok := automationRunByID(e.store.Snapshot(), run.ID)
			if !ok {
				return errors.New("automation run not found after gate")
			}
			if current.Status == AutomationRunSucceeded {
				return nil
			}
		}
	}
	provider, err := e.providerForRun(run)
	if err != nil {
		return e.failRun(run.ID, "provider", "prompt_revision_unavailable", err.Error(), "")
	}
	input, inputCompaction, err := buildAutomationProviderInputWithStats(
		run.ID,
		event,
		e.crm.Snapshot(),
		automationAllTools(),
	)
	if err != nil {
		return e.failRun(run.ID, "context", "context_invalid", err.Error(), "")
	}
	if err := e.recordRunEvent(
		AutomationAuditEventProviderStarted,
		run,
		automationRuntimeRunData(run, map[string]any{
			"provider_name":    run.ProviderName,
			"tool_count":       len(input.AllowedTools),
			"input_compaction": inputCompaction,
		}),
	); err != nil {
		return e.failRun(run.ID, "persistence", "provider_started_audit_failed", err.Error(), "")
	}
	providerCtx, providerCancel := context.WithTimeout(ctx, e.runTimeout())
	output, err := automationGenerateProviderOutputBounded(providerCtx, provider, input)
	providerCancel()
	if err != nil {
		_ = e.recordRunEvent(
			AutomationAuditEventProviderCompleted,
			run,
			automationRuntimeRunData(run, map[string]any{"succeeded": false}),
		)
		if errors.Is(err, context.DeadlineExceeded) {
			return e.failRun(run.ID, "provider", "provider_timeout", "automation provider timed out", "")
		}
		return e.failRun(run.ID, "provider", "provider_failed", err.Error(), "")
	}
	responseData, err := json.Marshal(output)
	if err != nil {
		return e.failRun(run.ID, "validation", "provider_response_invalid", err.Error(), "")
	}
	responseHash := output.RawSHA256
	if responseHash == "" {
		digest := sha256.Sum256(responseData)
		responseHash = hex.EncodeToString(digest[:])
	}
	if err := e.recordRunEvent(
		AutomationAuditEventProviderCompleted,
		run,
		automationRuntimeRunData(run, map[string]any{
			"response_bytes":  len(responseData),
			"response_sha256": responseHash,
			"succeeded":       true,
		}),
	); err != nil {
		return e.failRun(run.ID, "persistence", "provider_completed_audit_failed", err.Error(), responseHash)
	}
	envelope := AutomationToolEnvelope{
		SchemaVersion:    AutomationToolEnvelopeVersion,
		AssistantMessage: output.AssistantText,
		Actions:          output.ToolCalls,
	}
	if err := ValidateAutomationEnvelopeEvidence(envelope, automationEvidenceResolver(input)); err != nil {
		return e.failRun(run.ID, "validation", "evidence_invalid", err.Error(), responseHash)
	}

	var actionIDs []string
	err = e.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		runIndex := automationRunIndex(state, run.ID)
		if runIndex < 0 || state.Runs[runIndex].Status != AutomationRunRunning {
			return NewAutomationValidationError("run", "is no longer running")
		}
		policy := state.Policy
		source := AutomationSourceMessageRef{
			EventID:         event.ID,
			MessageKey:      event.MessageKey,
			ExpectedVersion: event.Version,
			ChatType:        event.ChatType,
			PeerID:          event.PeerID,
			Suspicious:      event.Suspicious,
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
				Tool:           call.Tool,
				Confidence:     call.Confidence,
				Payload:        payload,
				SourceMessage:  source,
				RunActionCount: len(envelope.Actions),
			})
			status := AutomationActionAwaitingReview
			retryable := false
			switch policyResult.Decision {
			case AutomationDecisionAutoComplete:
				status = AutomationActionSucceeded
			case AutomationDecisionAutoApprove:
				status = AutomationActionApproved
			case AutomationDecisionDeny:
				status = AutomationActionDenied
			case AutomationDecisionReview:
				status = AutomationActionAwaitingReview
			}
			action := AutomationAction{
				ID:                 actionIDs[i],
				RunID:              run.ID,
				ProviderCallID:     call.CallID,
				Tool:               call.Tool,
				Operation:          operation,
				Status:             status,
				Risk:               policyResult.Risk,
				PolicyDecision:     policyResult.Decision,
				PolicyVersion:      policy.Version,
				RiskReasons:        append([]string{}, policyResult.RiskReasons...),
				Confidence:         call.Confidence,
				Evidence:           append([]AutomationEvidence{}, call.Evidence...),
				SourceMessage:      source,
				Preconditions:      preconditions,
				DependsOnActionIDs: dependencies,
				Recipient:          policyResult.Recipient,
				Payload:            payload,
				Retryable:          retryable,
				Attempt:            1,
				Version:            1,
				CreatedAt:          now,
				UpdatedAt:          now,
			}
			if status == AutomationActionSucceeded {
				action.Result = append(json.RawMessage(nil), call.Arguments...)
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
				state,
				allocator,
				AutomationAuditEventActionProposed,
				action,
				"",
				"",
				now,
			); err != nil {
				return err
			}
			switch {
			case action.Status == AutomationActionApproved:
				if ruleMatched {
					if _, err := appendAutomationSupervisionAudit(
						state,
						allocator,
						AutomationAuditEventPermissionAllowed,
						action,
						"",
						"",
						now,
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
					state,
					allocator,
					AutomationAuditEventPermissionDenied,
					action,
					"",
					"",
					now,
				); err != nil {
					return err
				}
			}
		}
		completed := time.Now().UTC()
		state.Runs[runIndex].Status = AutomationRunSucceeded
		state.Runs[runIndex].Version++
		state.Runs[runIndex].CompletedAt = &completed
		state.Runs[runIndex].ProviderResponseSHA256 = responseHash
		state.Runs[runIndex].ActionIDs = actionIDs
		return nil
	})
	if err != nil {
		return e.failRun(run.ID, "persistence", "proposal_persist_failed", err.Error(), responseHash)
	}

	if e.supervision != nil {
		e.supervision.publishPersistedActions(actionIDs)
	}
	return e.ExecuteReadyActions(ctx, run.ID)
}

func (e *defaultAutomationRunEngine) runSourceGroupGate(
	ctx context.Context,
	run AutomationRun,
	event MessageEvent,
	route RecruitingAutomationRoute,
) error {
	if e.gate == nil {
		return e.failRun(run.ID, "gate", "gate_unavailable", "automation gate is unavailable", "")
	}
	input := buildAutomationGateInput(run.ID, event, route)
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return e.failRun(run.ID, "gate", "gate_input_invalid", err.Error(), "")
	}
	inputDigest := sha256.Sum256(inputJSON)
	startedAt := time.Now().UTC()
	if err := e.recordRunEvent(
		AutomationAuditEventGateStarted,
		run,
		automationRuntimeRunData(run, map[string]any{
			"gate_mode":       input.GateMode,
			"source_group_id": route.SourceGroup.ID,
			"input_sha256":    hex.EncodeToString(inputDigest[:]),
		}),
	); err != nil {
		return e.failRun(run.ID, "persistence", "gate_started_audit_failed", err.Error(), "")
	}
	gateTimeout := e.runTimeout()
	if gateTimeout > automationGateMaxTimeout {
		gateTimeout = automationGateMaxTimeout
	}
	gateCtx, cancel := context.WithTimeout(ctx, gateTimeout)
	defer cancel()
	decision, err := e.gate.Evaluate(gateCtx, input)
	if err != nil {
		auditErr := e.recordRunEvent(
			AutomationAuditEventGateCompleted,
			run,
			automationRuntimeRunData(run, map[string]any{
				"gate_mode":   input.GateMode,
				"succeeded":   false,
				"duration_ms": time.Since(startedAt).Milliseconds(),
			}),
		)
		if auditErr != nil {
			return e.failRun(
				run.ID,
				"persistence",
				"gate_completed_audit_failed",
				auditErr.Error(),
				"",
			)
		}
		code := "gate_failed"
		message := err.Error()
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			code = "gate_timeout"
			message = "automation gate timed out"
		case errors.Is(err, context.Canceled):
			code = "gate_canceled"
		case errors.Is(err, ErrOpenAIAutomationConfiguration),
			errors.Is(err, ErrOpenAIAutomationRequestFailed),
			errors.Is(err, ErrOpenAIAutomationHTTPStatus):
			code = "gate_provider_failed"
		case errors.Is(err, ErrAutomationValidation),
			errors.Is(err, ErrOpenAIAutomationMalformedResponse),
			errors.Is(err, ErrOpenAIAutomationResponseTooLarge):
			code = "gate_response_invalid"
		}
		return e.failRun(run.ID, "gate", code, message, "")
	}
	if err := e.recordRunEvent(
		AutomationAuditEventGateCompleted,
		run,
		automationRuntimeRunData(run, map[string]any{
			"gate_mode":       input.GateMode,
			"model":           decision.Model,
			"allow":           decision.Allow,
			"reason":          decision.Reason,
			"confidence":      decision.Confidence,
			"response_sha256": decision.RawSHA256,
			"duration_ms":     time.Since(startedAt).Milliseconds(),
			"succeeded":       true,
		}),
	); err != nil {
		return e.failRun(run.ID, "persistence", "gate_completed_audit_failed", err.Error(), "")
	}
	if decision.Allow {
		return nil
	}
	return e.completeGatedRun(run.ID)
}

func (e *defaultAutomationRunEngine) completeGatedRun(runID string) error {
	return e.store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		index := automationRunIndex(state, runID)
		if index < 0 {
			return errors.New("automation run not found")
		}
		if state.Runs[index].Status != AutomationRunRunning {
			return nil
		}
		now := time.Now().UTC()
		state.Runs[index].Status = AutomationRunSucceeded
		state.Runs[index].Version++
		state.Runs[index].ActionIDs = []string{}
		state.Runs[index].CompletedAt = &now
		return nil
	})
}

func (e *defaultAutomationRunEngine) ExecuteReadyActions(ctx context.Context, runID string) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if e == nil || e.store == nil {
		return NewAutomationValidationError("run_engine", "is unavailable")
	}
	if e.supervision != nil {
		return e.supervision.ExecuteReadyActions(ctx, runID)
	}
	if e.executor == nil {
		return nil
	}
	service := &AutomationService{
		store:    e.store,
		executor: e.executor,
		events:   NewAutomationRuntimeEventBroker(1),
	}
	return service.ExecuteReadyActions(ctx, runID)
}

func (e *defaultAutomationRunEngine) resumeScheduledActions(ctx context.Context) error {
	state := e.store.Snapshot()
	runIDs := make([]string, 0)
	for _, run := range state.Runs {
		for _, action := range state.Actions {
			if action.RunID != run.ID || IsTerminalAutomationActionStatus(action.Status) {
				continue
			}
			runIDs = append(runIDs, run.ID)
			break
		}
	}
	sort.Strings(runIDs)
	for _, runID := range runIDs {
		if err := e.ExecuteReadyActions(ctx, runID); err != nil {
			return err
		}
	}
	return nil
}

func automationActionReferences(
	call AutomationToolCall,
	callToAction map[string]string,
) ([]AutomationEntityPrecondition, []string, error) {
	references, err := AutomationToolCallReferences(call)
	if err != nil {
		return nil, nil, err
	}
	preconditions := make([]AutomationEntityPrecondition, 0, len(references))
	dependencies := make([]string, 0, len(references))
	for _, reference := range references {
		if reference.Reference.ID != "" {
			preconditions = append(preconditions, AutomationEntityPrecondition{
				EntityType:      reference.EntityType,
				EntityID:        reference.Reference.ID,
				ExpectedVersion: reference.Reference.ExpectedVersion,
			})
			continue
		}
		id := callToAction[reference.Reference.FromCall]
		if id == "" {
			return nil, nil, NewAutomationValidationError("from_call", "references an unknown action")
		}
		dependencies = append(dependencies, id)
	}
	return preconditions, dependencies, nil
}

func automationEvidenceResolver(input AutomationProviderInput) AutomationEvidenceTextResolver {
	return func(evidence AutomationEvidence) (string, bool) {
		switch evidence.Source {
		case "source_message":
			return input.Message.Text, true
		case AutomationEntityCandidate:
			for _, item := range input.Candidates {
				if item.ID == evidence.EntityID {
					data, _ := json.Marshal(item)
					return string(data), true
				}
			}
		case AutomationEntityJob:
			for _, item := range input.Jobs {
				if item.ID == evidence.EntityID {
					data, _ := json.Marshal(item)
					return string(data), true
				}
			}
		case AutomationEntityLead:
			for _, item := range input.Leads {
				if item.ID == evidence.EntityID {
					data, _ := json.Marshal(item)
					return string(data), true
				}
			}
		}
		return "", false
	}
}

func automationAllTools() []AutomationToolName {
	return []AutomationToolName{
		AutomationToolMessageClassify,
		AutomationToolCandidateCreate,
		AutomationToolCandidateUpdate,
		AutomationToolCandidateLink,
		AutomationToolJobMatch,
		AutomationToolLeadCreate,
		AutomationToolLeadTransition,
		AutomationToolTaskCreate,
		AutomationToolNoteCreate,
		AutomationToolJobUpdate,
		AutomationToolOnboardingCreate,
		AutomationToolOnboardingUpdate,
		AutomationToolFleetUpdate,
		AutomationToolServiceCreate,
		AutomationToolServiceUpdate,
		AutomationToolFundCreate,
		AutomationToolReplyPrivateText,
		AutomationToolReplyAudienceText,
		AutomationToolForwardEvent,
	}
}
