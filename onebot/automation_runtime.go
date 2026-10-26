package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	automationQueuedScanInterval  = 5 * time.Second
	automationRuntimeCloseTimeout = 10 * time.Second
)

type AutomationRuntime struct {
	store                    *AutomationStore
	providerConfigStore      *AutomationProviderConfigStore
	operationPermissionStore *AutomationOperationPermissionStore
	provider                 *DynamicAutomationProvider
	recruitingRouter         *RecruitingAutomationRouter
	audit                    *AutomationAuditLedger
	service                  *AutomationService
	metrics                  OperationalMetrics
	operatorToken            string
	agentToken               string
	ctx                      context.Context
	cancel                   context.CancelFunc
	done                     <-chan struct{}
	events                   chan MessageEvent
	queueScanInterval        time.Duration
	closeTimeout             time.Duration
	wg                       sync.WaitGroup
	closeOnce                sync.Once
	closeErr                 error
}

var (
	automationRuntimeMu sync.RWMutex
	automationRuntime   *AutomationRuntime
)

func initializeAutomationRuntime(cfg *Config, crm *CRMStore) (*AutomationRuntime, error) {
	if cfg == nil || crm == nil {
		return nil, NewAutomationValidationError("automation_runtime", "config and CRM store are required")
	}
	dataDir := strings.TrimSpace(cfg.AutomationDataDir)
	if dataDir == "" {
		var err error
		dataDir, err = DefaultAutomationDataDir()
		if err != nil {
			return nil, err
		}
	}
	operatorToken := strings.TrimSpace(os.Getenv("ONEBOT_AUTOMATION_OPERATOR_TOKEN"))
	agentToken := strings.TrimSpace(os.Getenv("ONEBOT_AUTOMATION_AGENT_TOKEN"))
	if operatorToken == "" || agentToken == "" {
		return nil, errors.New("ONEBOT_AUTOMATION_OPERATOR_TOKEN and ONEBOT_AUTOMATION_AGENT_TOKEN are required")
	}
	store, err := OpenAutomationStore(dataDir)
	if err != nil {
		return nil, err
	}
	audit, err := OpenAutomationAuditLedger(dataDir, store)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	if _, err := store.RecoverOutbox(context.Background()); err != nil {
		_ = store.Close()
		return nil, err
	}
	providerConfigStore, err := OpenAutomationProviderConfigStore(dataDir)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	operationPermissionStore, err := NewAutomationOperationPermissionStore(dataDir)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	configuredProvider, err := providerConfigStore.Provider()
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	provider := NewDynamicAutomationProvider(configuredProvider)
	metrics := NewOperationalMetrics()
	risk := NewServerAutomationRiskEvaluator(providerConfigStore)
	sender := NewMsgChanAutomationTextSender(msgChan)
	executor := NewDefaultAutomationCommandExecutor(store, crm, sender, providerConfigStore)
	if defaultExecutor, ok := executor.(*defaultAutomationCommandExecutor); ok {
		defaultExecutor.audit = audit
		defaultExecutor.agent = currentHRWorkbenchAgentClient()
		defaultExecutor.rules = forwardRuleStore
	}
	engine := NewDefaultAutomationRunEngine(
		store,
		crm,
		newOperationalMetricsProvider(provider, metrics),
		risk,
		executor,
	)
	if defaultEngine, ok := engine.(*defaultAutomationRunEngine); ok {
		defaultEngine.providerTimeout = func() time.Duration {
			return time.Duration(providerConfigStore.Public().TimeoutSeconds) * time.Second
		}
		defaultEngine.chatContextPolicy = providerConfigStore.ChatContextPolicy
		defaultEngine.activePromptRevisionID = providerConfigStore.ActivePromptRevisionID
		defaultEngine.providerForPromptRevision = func(revisionID string) (AutomationProvider, error) {
			revisionProvider, err := providerConfigStore.ProviderForPromptRevision(revisionID)
			if err != nil {
				return nil, err
			}
			return newOperationalMetricsProvider(revisionProvider, metrics), nil
		}
		defaultEngine.gate = newProviderStoreAutomationMessageGate(providerConfigStore)
	}
	service := NewAutomationService(store, crm, engine, executor, risk, audit)
	if err := service.recoverInterruptedExecutingActions(); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := service.recoverInterruptedChatTurns(); err != nil {
		_ = store.Close()
		return nil, err
	}
	if err := recoverInterruptedAutomationRuns(store); err != nil {
		_ = store.Close()
		return nil, err
	}
	recruitingRouter := NewRecruitingAutomationRouter(crm)
	service.engine = newRecruitingAutomationRunEngine(recruitingRouter, engine)
	bindOperationalMetricsSources(metrics, store, crm)
	ctx, cancel := context.WithCancel(context.Background())
	runtime := &AutomationRuntime{
		store:                    store,
		providerConfigStore:      providerConfigStore,
		operationPermissionStore: operationPermissionStore,
		provider:                 provider,
		recruitingRouter:         recruitingRouter,
		audit:                    audit,
		service:                  service,
		metrics:                  metrics,
		operatorToken:            operatorToken,
		agentToken:               agentToken,
		ctx:                      ctx,
		cancel:                   cancel,
		done:                     ctx.Done(),
		events:                   make(chan MessageEvent, 256),
	}
	runtime.wg.Add(1)
	go runtime.worker(ctx)
	automationRuntimeMu.Lock()
	automationRuntime = runtime
	automationRuntimeMu.Unlock()
	cfg.AutomationDataDir = dataDir
	return runtime, nil
}

func automationRuntimeProvider() (AutomationProvider, error) {
	endpoint := strings.TrimSpace(os.Getenv("ONEBOT_AI_ENDPOINT"))
	model := strings.TrimSpace(os.Getenv("ONEBOT_AI_MODEL"))
	apiKey := strings.TrimSpace(os.Getenv("ONEBOT_AI_API_KEY"))
	if endpoint == "" && model == "" && apiKey == "" {
		return noopAutomationProvider{}, nil
	}
	if endpoint == "" || model == "" || apiKey == "" {
		return nil, errors.New("ONEBOT_AI_ENDPOINT, ONEBOT_AI_MODEL, and ONEBOT_AI_API_KEY must be configured together")
	}
	return NewOpenAICompatibleAutomationProvider(endpoint, model, apiKey, nil)
}

func (r *AutomationRuntime) worker(ctx context.Context) {
	defer r.wg.Done()
	if err := r.processQueuedUntilIdle(ctx, true); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		Error("AI 自动化恢复未完成任务失败", "err", err)
	}
	if r.audit != nil {
		if err := r.audit.FlushPending(ctx); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			Error("AI 自动化启动审计写入失败", "err", err)
		}
	}
	ticker := time.NewTicker(r.queuedScanInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.processQueuedUntilIdle(ctx, false); err != nil &&
				!errors.Is(err, context.Canceled) &&
				!errors.Is(err, context.DeadlineExceeded) {
				Error("AI 自动化恢复未完成任务失败", "err", err)
			}
		case event := <-r.events:
			if _, err := r.service.ProcessEvent(ctx, event); err != nil && ctx.Err() == nil {
				Error("AI 自动化处理失败", "event_id", event.ID, "err", err)
			}
			if r.audit != nil {
				if err := r.audit.FlushPending(context.Background()); err != nil {
					Error("AI 自动化审计写入失败", "err", err)
				}
			}
		}
	}
}

func (r *AutomationRuntime) processQueuedUntilIdle(ctx context.Context, force bool) error {
	if r == nil || r.service == nil || r.service.engine == nil {
		return NewAutomationValidationError("automation_runtime", "run engine is unavailable")
	}
	if r.store == nil {
		if !force {
			return nil
		}
		return r.service.engine.ProcessQueued(ctx)
	}
	for {
		before := automationQueuedRunCount(r.store.Snapshot())
		if err := r.service.engine.ProcessQueued(ctx); err != nil {
			return err
		}
		after := automationQueuedRunCount(r.store.Snapshot())
		if after == 0 || after >= before {
			return nil
		}
		force = false
	}
}

func (r *AutomationRuntime) queuedScanInterval() time.Duration {
	if r != nil && r.queueScanInterval > 0 {
		return r.queueScanInterval
	}
	return automationQueuedScanInterval
}

func automationQueuedRunCount(state AutomationState) int {
	count := 0
	for _, run := range state.Runs {
		if run.Status == AutomationRunQueued {
			count++
		}
	}
	return count
}

func recoverInterruptedAutomationRuns(store *AutomationStore) error {
	if store == nil {
		return NewAutomationValidationError("automation_store", "is unavailable")
	}
	return store.Update(func(state *AutomationState, _ *AutomationIDAllocator) error {
		for i := range state.Runs {
			run := &state.Runs[i]
			if run.Status != AutomationRunRunning ||
				run.CompletedAt != nil ||
				len(run.ActionIDs) != 0 {
				continue
			}
			session := automationSessionByID(state, run.SessionID)
			if session != nil && session.Kind == AutomationSessionKindChat {
				continue
			}
			run.Status = AutomationRunQueued
			run.Version++
			run.StartedAt = nil
			run.FailureStage = ""
			run.FailureCode = ""
			run.FailureMessage = ""
			run.ProviderResponseSHA256 = ""
		}
		return nil
	})
}

func (r *AutomationRuntime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		automationRuntimeMu.Lock()
		if automationRuntime == r {
			automationRuntime = nil
		}
		automationRuntimeMu.Unlock()
		if r.cancel != nil {
			r.cancel()
		}
		if r.service != nil {
			r.service.closeChatCoordinator()
			r.service.RuntimeEventBroker().Close()
		}
		timeout := r.closeTimeout
		if timeout <= 0 {
			timeout = automationRuntimeCloseTimeout
		}
		deadline := time.Now().Add(timeout)
		waitDone := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(waitDone)
		}()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			r.closeErr = errors.New("automation runtime shutdown timeout")
		} else {
			timer := time.NewTimer(remaining)
			select {
			case <-waitDone:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
				r.closeErr = errors.New("automation runtime shutdown timeout")
			}
		}
		if r.audit != nil {
			remaining = time.Until(deadline)
			if remaining > 0 {
				flushCtx, flushCancel := context.WithTimeout(context.Background(), remaining)
				_ = r.audit.FlushSync(flushCtx)
				flushCancel()
			}
		}
		if r.store != nil {
			if err := r.store.Close(); err != nil && r.closeErr == nil {
				r.closeErr = err
			}
		}
	})
	return r.closeErr
}

func automationEnqueueEvent(event MessageEvent) {
	automationRuntimeMu.RLock()
	runtime := automationRuntime
	automationRuntimeMu.RUnlock()
	if runtime == nil {
		return
	}
	select {
	case runtime.events <- event:
	case <-runtime.done:
	}
}

func automationRuntimeForService(service *AutomationService) *AutomationRuntime {
	automationRuntimeMu.RLock()
	defer automationRuntimeMu.RUnlock()
	if automationRuntime != nil && automationRuntime.service == service {
		return automationRuntime
	}
	return nil
}
