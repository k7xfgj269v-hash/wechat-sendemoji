package main

import (
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

const (
	OperationalProviderOutcomeSuccess   = "success"
	OperationalProviderOutcomeError     = "error"
	OperationalProviderOutcomeCancelled = "cancelled"

	OperationalCancellationProvider = "provider"
	OperationalCancellationChat     = "chat"

	OperationalQueueAutomation = "automation"
	OperationalQueuePermission = "permission"
	OperationalQueueBatch      = "batch"
	OperationalQueueService    = "service"
)

var operationalProviderLatencyBounds = [...]time.Duration{
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	2500 * time.Millisecond,
	5 * time.Second,
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

type OperationalMetrics interface {
	ObserveProvider(string, time.Duration, string)
	RecordAction(AutomationToolName, string)
	RecordCancellation(string)
	SetQueue(string, int, time.Duration)
	SetAgentConnected(bool)
	Snapshot(time.Time) OperationalMetricsSnapshot
}

type OperationalMetricsSnapshot struct {
	CollectedAt   time.Time                          `json:"collected_at"`
	Providers     []OperationalProviderMetric        `json:"providers"`
	Actions       []OperationalActionMetric          `json:"actions"`
	Cancellations []OperationalCancellationMetric    `json:"cancellations"`
	Queues        []OperationalQueueMetric           `json:"queues"`
	Agent         OperationalAgentConnectivityMetric `json:"agent"`
}

type OperationalProviderMetric struct {
	Provider       string                             `json:"provider"`
	Requests       uint64                             `json:"requests"`
	Errors         uint64                             `json:"errors"`
	Cancellations  uint64                             `json:"cancellations"`
	LatencyCount   uint64                             `json:"latency_count"`
	LatencySumMS   float64                            `json:"latency_sum_ms"`
	LatencyBuckets []OperationalProviderLatencyBucket `json:"latency_buckets"`
}

type OperationalProviderLatencyBucket struct {
	UpperBoundMS int64  `json:"upper_bound_ms"`
	Count        uint64 `json:"count"`
}

type OperationalActionMetric struct {
	Tool    AutomationToolName `json:"tool"`
	Outcome string             `json:"outcome"`
	Count   uint64             `json:"count"`
}

type OperationalCancellationMetric struct {
	Kind  string `json:"kind"`
	Count uint64 `json:"count"`
}

type OperationalQueueMetric struct {
	Kind             string  `json:"kind"`
	Depth            int     `json:"depth"`
	OldestAgeSeconds float64 `json:"oldest_age_seconds"`
}

type OperationalAgentConnectivityMetric struct {
	Connected bool `json:"connected"`
}

type operationalProviderAccumulator struct {
	requests      uint64
	errors        uint64
	cancellations uint64
	latencyCount  uint64
	latencySum    time.Duration
	buckets       [len(operationalProviderLatencyBounds) + 1]uint64
}

type operationalActionKey struct {
	tool    AutomationToolName
	outcome string
}

type operationalQueueGauge struct {
	depth     int
	oldestAge time.Duration
}

type operationalMetricsRegistry struct {
	mu             sync.RWMutex
	providers      map[string]operationalProviderAccumulator
	actions        map[operationalActionKey]uint64
	cancellations  map[string]uint64
	queues         map[string]operationalQueueGauge
	agentConnected bool
	automation     *AutomationStore
	crm            *CRMStore
}

func NewOperationalMetrics() OperationalMetrics {
	return &operationalMetricsRegistry{
		providers:     make(map[string]operationalProviderAccumulator),
		actions:       make(map[operationalActionKey]uint64),
		cancellations: make(map[string]uint64),
		queues:        make(map[string]operationalQueueGauge),
	}
}

func (m *operationalMetricsRegistry) ObserveProvider(
	provider string,
	latency time.Duration,
	outcome string,
) {
	if m == nil {
		return
	}
	provider = operationalProviderLabel(provider)
	outcome = operationalProviderOutcome(outcome)
	if latency < 0 {
		latency = 0
	}
	m.mu.Lock()
	current := m.providers[provider]
	current.requests++
	current.latencyCount++
	current.latencySum += latency
	switch outcome {
	case OperationalProviderOutcomeError:
		current.errors++
	case OperationalProviderOutcomeCancelled:
		current.cancellations++
	}
	bucket := len(operationalProviderLatencyBounds)
	for i, bound := range operationalProviderLatencyBounds {
		if latency <= bound {
			bucket = i
			break
		}
	}
	for i := bucket; i < len(current.buckets); i++ {
		current.buckets[i]++
	}
	m.providers[provider] = current
	m.mu.Unlock()
}

func (m *operationalMetricsRegistry) RecordAction(tool AutomationToolName, outcome string) {
	if m == nil || !IsValidAutomationToolName(tool) {
		return
	}
	outcome = operationalActionOutcome(outcome)
	if outcome == "" {
		return
	}
	m.mu.Lock()
	m.actions[operationalActionKey{tool: tool, outcome: outcome}]++
	m.mu.Unlock()
}

func (m *operationalMetricsRegistry) RecordCancellation(kind string) {
	if m == nil {
		return
	}
	kind = operationalCancellationKind(kind)
	if kind == "" {
		return
	}
	m.mu.Lock()
	m.cancellations[kind]++
	m.mu.Unlock()
}

func (m *operationalMetricsRegistry) SetQueue(kind string, depth int, oldestAge time.Duration) {
	if m == nil {
		return
	}
	kind = operationalQueueKind(kind)
	if kind == "" {
		return
	}
	if depth < 0 {
		depth = 0
	}
	if depth == 0 || oldestAge < 0 {
		oldestAge = 0
	}
	m.mu.Lock()
	m.queues[kind] = operationalQueueGauge{depth: depth, oldestAge: oldestAge}
	m.mu.Unlock()
}

func (m *operationalMetricsRegistry) SetAgentConnected(connected bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.agentConnected = connected
	m.mu.Unlock()
}

func (m *operationalMetricsRegistry) Snapshot(now time.Time) OperationalMetricsSnapshot {
	if m == nil {
		return emptyOperationalMetricsSnapshot(now)
	}
	now = now.UTC()
	m.refreshQueues(now)

	m.mu.RLock()
	providers := make(map[string]operationalProviderAccumulator, len(m.providers))
	for provider, current := range m.providers {
		providers[provider] = current
	}
	actions := make(map[operationalActionKey]uint64, len(m.actions))
	for key, count := range m.actions {
		actions[key] = count
	}
	cancellations := make(map[string]uint64, len(m.cancellations))
	for kind, count := range m.cancellations {
		cancellations[kind] = count
	}
	queues := make(map[string]operationalQueueGauge, len(m.queues))
	for kind, gauge := range m.queues {
		queues[kind] = gauge
	}
	agentConnected := m.agentConnected
	automation := m.automation
	m.mu.RUnlock()

	if automation != nil {
		state := automation.Snapshot()
		for key, count := range automationActionOutcomeMetrics(state) {
			actions[key] += count
		}
		cancellations[OperationalCancellationChat] += automationChatCancellationCount(state)
	}

	return OperationalMetricsSnapshot{
		CollectedAt:   now,
		Providers:     operationalProviderSnapshot(providers),
		Actions:       operationalActionSnapshot(actions),
		Cancellations: operationalCancellationSnapshot(cancellations),
		Queues:        operationalQueueSnapshot(queues),
		Agent:         OperationalAgentConnectivityMetric{Connected: agentConnected},
	}
}

func emptyOperationalMetricsSnapshot(now time.Time) OperationalMetricsSnapshot {
	return OperationalMetricsSnapshot{
		CollectedAt:   now.UTC(),
		Providers:     []OperationalProviderMetric{},
		Actions:       []OperationalActionMetric{},
		Cancellations: []OperationalCancellationMetric{},
		Queues:        []OperationalQueueMetric{},
	}
}

func bindOperationalMetricsSources(
	metrics OperationalMetrics,
	automation *AutomationStore,
	crm *CRMStore,
) {
	registry, ok := metrics.(*operationalMetricsRegistry)
	if !ok || registry == nil {
		return
	}
	registry.mu.Lock()
	registry.automation = automation
	registry.crm = crm
	registry.mu.Unlock()
}

func (m *operationalMetricsRegistry) refreshQueues(now time.Time) {
	m.mu.RLock()
	automation := m.automation
	crm := m.crm
	m.mu.RUnlock()
	if automation != nil {
		state := automation.Snapshot()
		depth, age := automationRunQueueMetrics(state, now)
		m.SetQueue(OperationalQueueAutomation, depth, age)
		depth, age = automationPermissionQueueMetrics(state, now)
		m.SetQueue(OperationalQueuePermission, depth, age)
	}
	if crm != nil {
		state := crm.Snapshot()
		depth, age := crmOperationBatchQueueMetrics(state, now)
		m.SetQueue(OperationalQueueBatch, depth, age)
		depth, age = crmServiceRequestQueueMetrics(state, now)
		m.SetQueue(OperationalQueueService, depth, age)
	}
}

func operationalProviderLabel(provider string) string {
	switch provider {
	case "openai-compatible", "disabled", "fake":
		return provider
	default:
		return "other"
	}
}

func operationalProviderOutcome(outcome string) string {
	switch outcome {
	case "", OperationalProviderOutcomeSuccess,
		OperationalProviderOutcomeError,
		OperationalProviderOutcomeCancelled:
		if outcome == "" {
			return OperationalProviderOutcomeSuccess
		}
		return outcome
	default:
		return OperationalProviderOutcomeError
	}
}

func operationalActionOutcome(outcome string) string {
	switch AutomationActionStatus(outcome) {
	case AutomationActionSucceeded,
		AutomationActionRejected,
		AutomationActionStale,
		AutomationActionSuperseded,
		AutomationActionFailed,
		AutomationActionDenied,
		AutomationActionUnknown:
		return outcome
	default:
		return ""
	}
}

func operationalCancellationKind(kind string) string {
	switch kind {
	case OperationalCancellationProvider, OperationalCancellationChat:
		return kind
	default:
		return ""
	}
}

func operationalQueueKind(kind string) string {
	switch kind {
	case OperationalQueueAutomation,
		OperationalQueuePermission,
		OperationalQueueBatch,
		OperationalQueueService:
		return kind
	default:
		return ""
	}
}

func operationalProviderSnapshot(
	providers map[string]operationalProviderAccumulator,
) []OperationalProviderMetric {
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]OperationalProviderMetric, 0, len(names))
	for _, name := range names {
		current := providers[name]
		buckets := make([]OperationalProviderLatencyBucket, 0, len(current.buckets))
		for i, count := range current.buckets {
			upperBound := int64(-1)
			if i < len(operationalProviderLatencyBounds) {
				upperBound = operationalProviderLatencyBounds[i].Milliseconds()
			}
			buckets = append(buckets, OperationalProviderLatencyBucket{
				UpperBoundMS: upperBound,
				Count:        count,
			})
		}
		result = append(result, OperationalProviderMetric{
			Provider:       name,
			Requests:       current.requests,
			Errors:         current.errors,
			Cancellations:  current.cancellations,
			LatencyCount:   current.latencyCount,
			LatencySumMS:   float64(current.latencySum) / float64(time.Millisecond),
			LatencyBuckets: buckets,
		})
	}
	return result
}

func operationalActionSnapshot(actions map[operationalActionKey]uint64) []OperationalActionMetric {
	result := make([]OperationalActionMetric, 0, len(actions))
	for key, count := range actions {
		if count == 0 {
			continue
		}
		result = append(result, OperationalActionMetric{
			Tool: key.tool, Outcome: key.outcome, Count: count,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Tool == result[j].Tool {
			return result[i].Outcome < result[j].Outcome
		}
		return result[i].Tool < result[j].Tool
	})
	return result
}

func operationalCancellationSnapshot(
	cancellations map[string]uint64,
) []OperationalCancellationMetric {
	kinds := []string{OperationalCancellationChat, OperationalCancellationProvider}
	result := make([]OperationalCancellationMetric, 0, len(kinds))
	for _, kind := range kinds {
		result = append(result, OperationalCancellationMetric{
			Kind: kind, Count: cancellations[kind],
		})
	}
	return result
}

func operationalQueueSnapshot(queues map[string]operationalQueueGauge) []OperationalQueueMetric {
	kinds := []string{
		OperationalQueueAutomation,
		OperationalQueuePermission,
		OperationalQueueBatch,
		OperationalQueueService,
	}
	result := make([]OperationalQueueMetric, 0, len(kinds))
	for _, kind := range kinds {
		gauge := queues[kind]
		result = append(result, OperationalQueueMetric{
			Kind:             kind,
			Depth:            gauge.depth,
			OldestAgeSeconds: gauge.oldestAge.Seconds(),
		})
	}
	return result
}

func automationPermissionQueueMetrics(
	state AutomationState,
	now time.Time,
) (int, time.Duration) {
	depth := 0
	var oldest time.Time
	for _, request := range state.PermissionRequests {
		if request.Status != AutomationPermissionStatusPending {
			continue
		}
		depth++
		if oldest.IsZero() || request.CreatedAt.Before(oldest) {
			oldest = request.CreatedAt
		}
	}
	return operationalQueueAge(depth, oldest, now)
}

func automationChatCancellationCount(state AutomationState) uint64 {
	var count uint64
	for _, turn := range state.Turns {
		if turn.Status == AutomationTurnCancelled {
			count++
		}
	}
	return count
}

func operationalQueueAge(depth int, oldest, now time.Time) (int, time.Duration) {
	if depth == 0 || oldest.IsZero() || !now.After(oldest) {
		return depth, 0
	}
	return depth, now.Sub(oldest)
}

type operationalMetricsProvider struct {
	provider AutomationProvider
	metrics  OperationalMetrics
}

func newOperationalMetricsProvider(
	provider AutomationProvider,
	metrics OperationalMetrics,
) AutomationProvider {
	if provider == nil || metrics == nil {
		return provider
	}
	return &operationalMetricsProvider{provider: provider, metrics: metrics}
}

func (p *operationalMetricsProvider) Name() string {
	if p == nil || p.provider == nil {
		return "disabled"
	}
	return p.provider.Name()
}

func (p *operationalMetricsProvider) Generate(
	ctx context.Context,
	input AutomationProviderInput,
) ([]byte, error) {
	started := time.Now()
	data, err := p.provider.Generate(ctx, input)
	p.observe(ctx, started, err)
	return data, err
}

func (p *operationalMetricsProvider) CompactContext(
	ctx context.Context,
	request AutomationContextCompactionRequest,
) (AutomationContextCompactionResult, error) {
	if p == nil || p.provider == nil {
		return AutomationContextCompactionResult{}, ErrAutomationContextCompactorUnavailable
	}
	compactor, ok := p.provider.(AutomationContextCompactor)
	if !ok {
		return AutomationContextCompactionResult{}, ErrAutomationContextCompactorUnavailable
	}
	started := time.Now()
	result, err := compactor.CompactContext(ctx, request)
	p.observe(ctx, started, err)
	return result, err
}

func (p *operationalMetricsProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderStream, error) {
	started := time.Now()
	stream, err := p.provider.Stream(ctx, input)
	if err != nil {
		p.observe(ctx, started, err)
		return nil, err
	}
	return &operationalMetricsProviderStream{
		stream: stream, ctx: ctx, provider: p.Name(),
		metrics: p.metrics, started: started,
	}, nil
}

func (p *operationalMetricsProvider) observe(
	ctx context.Context,
	started time.Time,
	err error,
) {
	outcome := OperationalProviderOutcomeSuccess
	if err != nil {
		outcome = OperationalProviderOutcomeError
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			(ctx != nil && ctx.Err() != nil) {
			outcome = OperationalProviderOutcomeCancelled
			p.metrics.RecordCancellation(OperationalCancellationProvider)
		}
	}
	p.metrics.ObserveProvider(p.Name(), time.Since(started), outcome)
}

type operationalMetricsProviderStream struct {
	stream   AutomationProviderStream
	ctx      context.Context
	provider string
	metrics  OperationalMetrics
	started  time.Time
	once     sync.Once
}

func (s *operationalMetricsProviderStream) Recv() (AutomationProviderEvent, error) {
	event, err := s.stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			s.finish(OperationalProviderOutcomeSuccess)
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			(s.ctx != nil && s.ctx.Err() != nil) {
			s.finish(OperationalProviderOutcomeCancelled)
		} else {
			s.finish(OperationalProviderOutcomeError)
		}
		return event, err
	}
	if event.Type == "completed" {
		s.finish(OperationalProviderOutcomeSuccess)
	}
	return event, nil
}

func (s *operationalMetricsProviderStream) Close() error {
	err := s.stream.Close()
	if s.ctx != nil && s.ctx.Err() != nil {
		s.finish(OperationalProviderOutcomeCancelled)
	} else if err != nil {
		s.finish(OperationalProviderOutcomeError)
	} else {
		s.finish(OperationalProviderOutcomeCancelled)
	}
	return err
}

func (s *operationalMetricsProviderStream) finish(outcome string) {
	s.once.Do(func() {
		if outcome == OperationalProviderOutcomeCancelled {
			s.metrics.RecordCancellation(OperationalCancellationProvider)
		}
		s.metrics.ObserveProvider(s.provider, time.Since(s.started), outcome)
	})
}
