package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	automationQueuedRunBatchDefault    = 10
	automationQueuedRunBatchMaximum    = 10
	automationQueuedRunCooldownDefault = 30 * time.Second
	automationQueuedRunCooldownMaximum = 5 * time.Minute
	automationRunAutoRetryLimit        = 3
	automationRunAutoRetryWindow       = time.Hour
)

type defaultAutomationRunEngine struct {
	store                     *AutomationStore
	crm                       *CRMStore
	provider                  AutomationProvider
	gate                      AutomationMessageGate
	risk                      AutomationRiskEvaluator
	executor                  AutomationCommandExecutor
	supervision               *AutomationService
	recruitingRouter          *RecruitingAutomationRouter
	providerTimeout           func() time.Duration
	chatContextPolicy         func() AutomationChatContextPolicy
	activePromptRevisionID    func() string
	providerForPromptRevision func(string) (AutomationProvider, error)

	queuedControlMu        sync.Mutex
	queuedBatchActive      bool
	queuedCooldownUntil    time.Time
	queuedBatchLimit       int
	queuedProviderCooldown time.Duration
	queuedClock            func() time.Time
}

func NewDefaultAutomationRunEngine(
	store *AutomationStore,
	crm *CRMStore,
	provider AutomationProvider,
	risk AutomationRiskEvaluator,
	executor AutomationCommandExecutor,
) AutomationRunEngine {
	if evaluator, ok := risk.(*ServerAutomationRiskEvaluator); ok {
		evaluator.crm = crm
	}
	return &defaultAutomationRunEngine{
		store:    store,
		crm:      crm,
		provider: provider,
		risk:     risk,
		executor: executor,
		providerTimeout: func() time.Duration {
			return time.Duration(AutomationProviderDefaultTimeoutSeconds) * time.Second
		},
		chatContextPolicy: defaultAutomationChatContextPolicy,
	}
}

func (e *defaultAutomationRunEngine) EnsureRun(
	ctx context.Context,
	event MessageEvent,
	providerName string,
) (AutomationRun, bool, error) {
	if ctx == nil {
		return AutomationRun{}, false, NewAutomationValidationError("context", "is required")
	}
	if e == nil || e.store == nil || e.crm == nil || e.provider == nil || e.risk == nil {
		return AutomationRun{}, false, NewAutomationValidationError("run_engine", "is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return AutomationRun{}, false, err
	}
	if e.recruitingRouter != nil {
		route, err := e.recruitingRouter.Route(event)
		if err != nil {
			return AutomationRun{}, false, err
		}
		event = route.Event
	}
	if err := validateMessageEvent("event", &event); err != nil {
		return AutomationRun{}, false, err
	}
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		providerName = e.provider.Name()
	}
	if providerName == "" {
		return AutomationRun{}, false, NewAutomationValidationError("provider_name", "is required")
	}
	promptRevisionID := e.currentPromptRevisionID()

	key := fmt.Sprintf("run:%s:%s", AutomationEngineVersion, event.MessageKey)
	var result AutomationRun
	created := false
	err := e.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		for i := range state.Runs {
			run := &state.Runs[i]
			if run.RetryOfRunID == "" &&
				run.EngineVersion == AutomationEngineVersion &&
				run.SourceMessageKey == event.MessageKey {
				if e.recruitingRouter != nil && run.Status == AutomationRunQueued {
					updated, err := reconcileQueuedRunSourceVersion(run, event)
					if err != nil {
						return err
					}
					if updated {
						run.Version++
					}
				}
				result = *run
				return nil
			}
		}
		if record, ok := state.Idempotency[key]; ok {
			for i := range state.Runs {
				run := &state.Runs[i]
				if record.Kind == "run" && run.ID == record.ResourceID {
					if e.recruitingRouter != nil && run.Status == AutomationRunQueued {
						updated, err := reconcileQueuedRunSourceVersion(run, event)
						if err != nil {
							return err
						}
						if updated {
							run.Version++
						}
					}
					result = *run
					return nil
				}
			}
			return NewAutomationValidationError("idempotency", "run mapping is inconsistent")
		}
		id, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		result = AutomationRun{
			ID:                   id,
			PromptRevisionID:     promptRevisionID,
			IdempotencyKey:       key,
			EngineVersion:        AutomationEngineVersion,
			ProviderName:         providerName,
			SourceMessageEventID: event.ID,
			SourceMessageKey:     event.MessageKey,
			SourceMessageVersion: event.Version,
			Status:               AutomationRunQueued,
			Version:              1,
			Attempt:              1,
			ActionIDs:            []string{},
			CreatedAt:            now,
		}
		state.Runs = append(state.Runs, result)
		state.Idempotency[key] = AutomationIdempotencyRecord{
			Kind:       "run",
			ResourceID: id,
			CreatedAt:  now,
		}
		created = true
		return nil
	})
	return result, created, err
}

func (e *defaultAutomationRunEngine) RetryRun(
	ctx context.Context,
	runID string,
	expectedVersion uint64,
	operatorID string,
	reason string,
) (AutomationRun, error) {
	if ctx == nil {
		return AutomationRun{}, NewAutomationValidationError("context", "is required")
	}
	if e == nil || e.store == nil {
		return AutomationRun{}, NewAutomationValidationError("run_engine", "is unavailable")
	}
	operatorID = strings.TrimSpace(operatorID)
	if operatorID == "" {
		return AutomationRun{}, NewAutomationValidationError("operator_id", "is required")
	}
	_ = reason

	retrySourceVersion := uint64(0)
	if e.recruitingRouter != nil {
		state := e.store.Snapshot()
		index := automationRunIndex(&state, runID)
		if index < 0 {
			return AutomationRun{}, errors.New("automation run not found")
		}
		source := state.Runs[index]
		if source.Version != expectedVersion {
			return AutomationRun{}, NewAutomationValidationError("expected_version", "run version conflict")
		}
		if source.Status != AutomationRunFailed || source.FailureStage == "execution" {
			return AutomationRun{}, NewAutomationValidationError("run", "is not retryable")
		}
		event, ok := automationFindMessageEvent(e.crm.Snapshot(), source.SourceMessageEventID)
		if !ok || event.MessageKey != source.SourceMessageKey {
			return AutomationRun{}, NewAutomationValidationError(
				"run.source_message",
				"references a missing or mismatched message event",
			)
		}
		route, err := e.recruitingRouter.Route(event)
		if err != nil {
			return AutomationRun{}, err
		}
		retrySourceVersion = route.Event.Version
		if source.SourceMessageVersion != retrySourceVersion &&
			!isSingleRecruitingRouteVersionIncrement(source.SourceMessageVersion, route.Event) {
			return AutomationRun{}, NewAutomationValidationError(
				"run.source_message_version",
				"cannot be reconciled with the frozen recruiting route",
			)
		}
	}

	var retry AutomationRun
	err := e.store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		var source *AutomationRun
		for i := range state.Runs {
			if state.Runs[i].ID == runID {
				source = &state.Runs[i]
				break
			}
		}
		if source == nil {
			return errors.New("automation run not found")
		}
		if source.Version != expectedVersion {
			return NewAutomationValidationError("expected_version", "run version conflict")
		}
		if source.Status != AutomationRunFailed || source.FailureStage == "execution" {
			return NewAutomationValidationError("run", "is not retryable")
		}
		id, err := allocator.Next(AutomationIDKindRun)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		key := fmt.Sprintf("retry:%s:%d", source.ID, source.Attempt+1)
		sourceMessageVersion := source.SourceMessageVersion
		if retrySourceVersion != 0 {
			sourceMessageVersion = retrySourceVersion
		}
		retry = AutomationRun{
			ID:                   id,
			PromptRevisionID:     source.PromptRevisionID,
			IdempotencyKey:       key,
			EngineVersion:        source.EngineVersion,
			ProviderName:         source.ProviderName,
			SourceMessageEventID: source.SourceMessageEventID,
			SourceMessageKey:     source.SourceMessageKey,
			SourceMessageVersion: sourceMessageVersion,
			Status:               AutomationRunQueued,
			Version:              1,
			Attempt:              source.Attempt + 1,
			RetryOfRunID:         source.ID,
			ActionIDs:            []string{},
			CreatedAt:            now,
		}
		state.Runs = append(state.Runs, retry)
		state.Idempotency[key] = AutomationIdempotencyRecord{
			Kind:       "run",
			ResourceID: id,
			CreatedAt:  now,
		}
		return nil
	})
	return retry, err
}

func (e *defaultAutomationRunEngine) ProcessRun(ctx context.Context, runID string) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if e == nil || e.store == nil || e.crm == nil || e.provider == nil || e.risk == nil {
		return NewAutomationValidationError("run_engine", "is unavailable")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return NewAutomationValidationError("run_id", "is required")
	}
	if err := e.prepareExplicitRunRecovery(runID); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, e.runLifecycleTimeout())
	defer cancel()
	return e.processRun(runCtx, runID)
}

func (e *defaultAutomationRunEngine) RecoverScheduledActions(ctx context.Context) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if e == nil || e.store == nil {
		return NewAutomationValidationError("run_engine", "is unavailable")
	}
	return e.resumeScheduledActions(ctx)
}

func (e *defaultAutomationRunEngine) ProcessQueued(ctx context.Context) error {
	if ctx == nil {
		return NewAutomationValidationError("context", "is required")
	}
	if e == nil || e.store == nil {
		return NewAutomationValidationError("run_engine", "is unavailable")
	}
	if !e.beginQueuedBatch() {
		return nil
	}
	defer e.endQueuedBatch()
	if err := e.RecoverScheduledActions(ctx); err != nil {
		return err
	}
	if err := e.queueAutomaticRetries(ctx); err != nil {
		return err
	}
	if e.queuedCooldownActive() {
		return nil
	}
	runIDs := make([]string, 0)
	for _, run := range e.store.Snapshot().Runs {
		if run.Status == AutomationRunQueued {
			runIDs = append(runIDs, run.ID)
		}
	}
	sort.Strings(runIDs)
	if limit := e.queuedRunBatchLimit(); len(runIDs) > limit {
		runIDs = runIDs[:limit]
	}
	for _, runID := range runIDs {
		err := e.processQueuedRun(ctx, runID)
		if err == nil {
			continue
		}
		run, ok := automationRunByID(e.store.Snapshot(), runID)
		if ok &&
			run.Status == AutomationRunFailed &&
			automationQueuedFailureStartsProviderCooldown(run) {
			e.startQueuedProviderCooldown()
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			break
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !ok || (run.Status != AutomationRunFailed && run.Status != AutomationRunSucceeded) {
			return err
		}
	}
	return nil
}

func (e *defaultAutomationRunEngine) queueAutomaticRetries(ctx context.Context) error {
	if e == nil || e.store == nil {
		return NewAutomationValidationError("run_engine", "is unavailable")
	}
	if e.crm == nil {
		return nil
	}
	state := e.store.Snapshot()
	crmState := e.crm.Snapshot()
	now := e.queuedNow()
	cooldown := e.queuedRunProviderCooldown()
	for _, run := range state.Runs {
		if !automationRunCanAutoRetry(state, crmState, run, now, cooldown) {
			continue
		}
		if _, err := e.RetryRun(
			ctx,
			run.ID,
			run.Version,
			"automation-runtime",
			"automatic retry after side-effect-free failure",
		); err != nil {
			return err
		}
	}
	return nil
}

func automationRunCanAutoRetry(
	state AutomationState,
	crmState CRMState,
	run AutomationRun,
	now time.Time,
	cooldown time.Duration,
) bool {
	attempt := run.Attempt
	if attempt == 0 {
		attempt = 1
	}
	if run.Status != AutomationRunFailed ||
		attempt > automationRunAutoRetryLimit ||
		run.CompletedAt == nil ||
		run.CompletedAt.Add(cooldown).After(now) ||
		now.Sub(*run.CompletedAt) > automationRunAutoRetryWindow {
		return false
	}
	session := automationSessionByID(&state, run.SessionID)
	if session == nil || session.Kind != AutomationSessionKindAutomation {
		return false
	}
	switch run.FailureStage {
	case "provider", "validation":
	default:
		return false
	}
	if len(run.ActionIDs) != 0 {
		return false
	}
	for _, action := range state.Actions {
		if action.RunID == run.ID {
			return false
		}
	}
	for _, candidate := range state.Runs {
		if candidate.RetryOfRunID == run.ID {
			return false
		}
	}
	source, ok := automationFindMessageEvent(crmState, run.SourceMessageEventID)
	if !ok || source.MessageKey != run.SourceMessageKey {
		return false
	}
	for _, event := range crmState.MessageEvents {
		if event.ID == source.ID ||
			event.ChatType != source.ChatType ||
			event.PeerID != source.PeerID {
			continue
		}
		if event.CreatedAt.After(source.CreatedAt) ||
			(event.CreatedAt.Equal(source.CreatedAt) && event.ID > source.ID) {
			return false
		}
	}
	return true
}

func (e *defaultAutomationRunEngine) processQueuedRun(ctx context.Context, runID string) error {
	if e == nil || e.store == nil || e.crm == nil || e.provider == nil || e.risk == nil {
		return NewAutomationValidationError("run_engine", "is unavailable")
	}
	runCtx, cancel := context.WithTimeout(ctx, e.runLifecycleTimeout())
	defer cancel()
	return e.processRun(runCtx, runID)
}

func (e *defaultAutomationRunEngine) beginQueuedBatch() bool {
	e.queuedControlMu.Lock()
	defer e.queuedControlMu.Unlock()
	if e.queuedBatchActive {
		return false
	}
	e.queuedBatchActive = true
	return true
}

func (e *defaultAutomationRunEngine) endQueuedBatch() {
	e.queuedControlMu.Lock()
	e.queuedBatchActive = false
	e.queuedControlMu.Unlock()
}

func (e *defaultAutomationRunEngine) queuedCooldownActive() bool {
	now := e.queuedNow()
	e.queuedControlMu.Lock()
	defer e.queuedControlMu.Unlock()
	return e.queuedCooldownUntil.After(now)
}

func (e *defaultAutomationRunEngine) startQueuedProviderCooldown() {
	until := e.queuedNow().Add(e.queuedRunProviderCooldown())
	e.queuedControlMu.Lock()
	if until.After(e.queuedCooldownUntil) {
		e.queuedCooldownUntil = until
	}
	e.queuedControlMu.Unlock()
}

func (e *defaultAutomationRunEngine) queuedRunBatchLimit() int {
	if e.queuedBatchLimit <= 0 {
		return automationQueuedRunBatchDefault
	}
	if e.queuedBatchLimit > automationQueuedRunBatchMaximum {
		return automationQueuedRunBatchMaximum
	}
	return e.queuedBatchLimit
}

func (e *defaultAutomationRunEngine) queuedRunProviderCooldown() time.Duration {
	if e.queuedProviderCooldown <= 0 {
		return automationQueuedRunCooldownDefault
	}
	if e.queuedProviderCooldown > automationQueuedRunCooldownMaximum {
		return automationQueuedRunCooldownMaximum
	}
	return e.queuedProviderCooldown
}

func (e *defaultAutomationRunEngine) queuedNow() time.Time {
	if e != nil && e.queuedClock != nil {
		return e.queuedClock().UTC()
	}
	return time.Now().UTC()
}

func (e *defaultAutomationRunEngine) runTimeout() time.Duration {
	timeout := time.Duration(AutomationProviderDefaultTimeoutSeconds) * time.Second
	if e != nil && e.providerTimeout != nil {
		timeout = e.providerTimeout()
	}
	maximum := time.Duration(AutomationProviderMaxTimeoutSeconds) * time.Second
	if timeout <= 0 || timeout > maximum {
		return time.Duration(AutomationProviderDefaultTimeoutSeconds) * time.Second
	}
	return timeout
}

func (e *defaultAutomationRunEngine) currentChatContextPolicy() AutomationChatContextPolicy {
	if e == nil || e.chatContextPolicy == nil {
		return defaultAutomationChatContextPolicy()
	}
	return normalizeAutomationChatContextPolicy(e.chatContextPolicy())
}

func (e *defaultAutomationRunEngine) runLifecycleTimeout() time.Duration {
	providerTimeout := e.runTimeout()
	gateTimeout := providerTimeout
	if gateTimeout > automationGateMaxTimeout {
		gateTimeout = automationGateMaxTimeout
	}
	return providerTimeout + gateTimeout
}

func (e *defaultAutomationRunEngine) currentPromptRevisionID() string {
	if e == nil || e.activePromptRevisionID == nil {
		return ""
	}
	return strings.TrimSpace(e.activePromptRevisionID())
}

func (e *defaultAutomationRunEngine) providerForRun(run AutomationRun) (AutomationProvider, error) {
	if e == nil || e.provider == nil {
		return nil, NewAutomationValidationError("provider", "is unavailable")
	}
	if e.providerForPromptRevision == nil || strings.TrimSpace(run.PromptRevisionID) == "" {
		return e.provider, nil
	}
	provider, err := e.providerForPromptRevision(run.PromptRevisionID)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return nil, NewAutomationValidationError("provider", "is unavailable")
	}
	return provider, nil
}

func automationQueuedFailureStartsProviderCooldown(run AutomationRun) bool {
	if run.FailureStage == "provider" {
		return true
	}
	if run.FailureStage != "gate" {
		return false
	}
	switch run.FailureCode {
	case "gate_unavailable", "gate_provider_failed", "gate_timeout":
		return true
	default:
		return false
	}
}

func reconcileQueuedRunSourceVersion(run *AutomationRun, event MessageEvent) (bool, error) {
	if run.SourceMessageEventID != event.ID || run.SourceMessageKey != event.MessageKey {
		return false, NewAutomationValidationError(
			"run.source_message",
			"does not match the frozen message event",
		)
	}
	if run.SourceMessageVersion == event.Version {
		return false, nil
	}
	if len(run.ActionIDs) != 0 ||
		run.StartedAt != nil ||
		run.CompletedAt != nil ||
		!isSingleRecruitingRouteVersionIncrement(run.SourceMessageVersion, event) {
		return false, NewAutomationValidationError(
			"run.source_message_version",
			"cannot be reconciled with the frozen recruiting route",
		)
	}
	run.SourceMessageVersion = event.Version
	return true, nil
}

func isSingleRecruitingRouteVersionIncrement(sourceVersion uint64, event MessageEvent) bool {
	return event.RecruitingProjectVersion != 0 &&
		event.RecruitingRouteSourceVersion == sourceVersion &&
		event.Version == sourceVersion+1
}

func automationRunByID(state AutomationState, id string) (AutomationRun, bool) {
	index := automationRunIndex(&state, id)
	if index < 0 {
		return AutomationRun{}, false
	}
	return state.Runs[index], true
}

type automationProviderOutputResult struct {
	output AutomationProviderOutput
	err    error
}

func automationGenerateProviderOutputBounded(
	ctx context.Context,
	provider AutomationProvider,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	result := make(chan automationProviderOutputResult, 1)
	go func() {
		output, err := automationGenerateProviderOutput(ctx, provider, input)
		result <- automationProviderOutputResult{output: output, err: err}
	}()
	select {
	case <-ctx.Done():
		return AutomationProviderOutput{}, ctx.Err()
	case generated := <-result:
		if err := ctx.Err(); err != nil {
			return AutomationProviderOutput{}, err
		}
		return generated.output, generated.err
	}
}

func automationActionOutcomeMetrics(state AutomationState) map[operationalActionKey]uint64 {
	counts := make(map[operationalActionKey]uint64)
	for _, action := range state.Actions {
		if !IsValidAutomationToolName(action.Tool) {
			continue
		}
		outcome := operationalActionOutcome(string(action.Status))
		if outcome == "" {
			continue
		}
		counts[operationalActionKey{tool: action.Tool, outcome: outcome}]++
	}
	return counts
}

func automationFindMessageEvent(state CRMState, id string) (MessageEvent, bool) {
	for _, event := range state.MessageEvents {
		if event.ID == id {
			return event, true
		}
	}
	return MessageEvent{}, false
}
