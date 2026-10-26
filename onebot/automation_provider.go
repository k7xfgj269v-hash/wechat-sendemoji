package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"
)

const (
	automationProviderCandidateLimit = 25
	automationProviderJobLimit       = 50
	automationProviderLeadLimit      = 50
	automationProviderOpsLimit       = 50
	automationProviderTargetBytes    = 192 << 10
)

var ErrFakeAutomationProviderExhausted = errors.New("fake automation provider script exhausted")

type automationProviderCompactionStats struct {
	OriginalBytes          int  `json:"original_bytes"`
	FinalBytes             int  `json:"final_bytes"`
	TargetBytes            int  `json:"target_bytes"`
	Compacted              bool `json:"compacted"`
	RemovedCandidates      int  `json:"removed_candidates"`
	RemovedJobs            int  `json:"removed_jobs"`
	RemovedLeads           int  `json:"removed_leads"`
	RemovedOnboardings     int  `json:"removed_onboardings"`
	RemovedServiceRequests int  `json:"removed_service_requests"`
	RemovedFundCallbacks   int  `json:"removed_fund_callbacks"`
}

type FakeAutomationProviderStep struct {
	Response []byte
	Err      error
}

type FakeAutomationProvider struct {
	mu    sync.Mutex
	steps []FakeAutomationProviderStep
	next  int
}

func NewFakeAutomationProvider(steps []FakeAutomationProviderStep) *FakeAutomationProvider {
	copied := make([]FakeAutomationProviderStep, len(steps))
	for i := range steps {
		copied[i] = FakeAutomationProviderStep{
			Response: append([]byte(nil), steps[i].Response...),
			Err:      steps[i].Err,
		}
	}
	return &FakeAutomationProvider{steps: copied}
}

func (p *FakeAutomationProvider) Name() string {
	return "fake"
}

func (p *FakeAutomationProvider) Generate(ctx context.Context, _ AutomationProviderInput) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("fake automation provider requires a context")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.next >= len(p.steps) {
		return nil, ErrFakeAutomationProviderExhausted
	}
	step := p.steps[p.next]
	p.next++
	return append([]byte(nil), step.Response...), step.Err
}

func (p *FakeAutomationProvider) Stream(
	ctx context.Context,
	input AutomationProviderInput,
) (AutomationProviderStream, error) {
	output, err := automationGenerateProviderOutput(ctx, p, input)
	if err != nil {
		return nil, err
	}
	return &singleAutomationProviderStream{output: output}, nil
}

type automationStructuredProvider interface {
	GenerateOutput(context.Context, AutomationProviderInput) (AutomationProviderOutput, error)
}

func automationGenerateProviderOutput(
	ctx context.Context,
	provider AutomationProvider,
	input AutomationProviderInput,
) (AutomationProviderOutput, error) {
	if structured, ok := provider.(automationStructuredProvider); ok {
		output, err := structured.GenerateOutput(ctx, input)
		if err != nil {
			return AutomationProviderOutput{}, err
		}
		return NormalizeAutomationProviderOutput(output, input.AllowedTools)
	}
	data, err := provider.Generate(ctx, input)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	envelope, err := DecodeAutomationToolEnvelope(data)
	if err != nil {
		return AutomationProviderOutput{}, err
	}
	digest := sha256.Sum256(data)
	return NormalizeAutomationProviderOutput(AutomationProviderOutput{
		AssistantText: envelope.AssistantMessage,
		ToolCalls:     envelope.Actions,
		RawSHA256:     hex.EncodeToString(digest[:]),
	}, input.AllowedTools)
}

type singleAutomationProviderStream struct {
	output AutomationProviderOutput
	sent   bool
	closed bool
}

func (s *singleAutomationProviderStream) Recv() (AutomationProviderEvent, error) {
	if s == nil || s.closed || s.sent {
		return AutomationProviderEvent{}, io.EOF
	}
	s.sent = true
	output := s.output
	return AutomationProviderEvent{Type: "completed", Output: &output}, nil
}

func (s *singleAutomationProviderStream) Close() error {
	if s != nil {
		s.closed = true
	}
	return nil
}

func BuildAutomationProviderInput(
	runID string,
	event MessageEvent,
	crm CRMState,
	allowedTools []AutomationToolName,
) (AutomationProviderInput, error) {
	input, _, err := buildAutomationProviderInputWithStats(runID, event, crm, allowedTools)
	return input, err
}

func buildAutomationProviderInputWithStats(
	runID string,
	event MessageEvent,
	crm CRMState,
	allowedTools []AutomationToolName,
) (AutomationProviderInput, automationProviderCompactionStats, error) {
	normalizedRunID, err := normalizeAutomationString("run_id", runID, 80, true, false)
	if err != nil {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, err
	}
	if normalizedRunID != runID {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, NewAutomationValidationError("run_id", "must not contain surrounding whitespace")
	}
	normalizedEventID, err := normalizeAutomationString("event.id", event.ID, 80, true, false)
	if err != nil {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, err
	}
	if normalizedEventID != event.ID {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, NewAutomationValidationError("event.id", "must not contain surrounding whitespace")
	}
	if err := validateMessageEvent("event", &event); err != nil {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, err
	}
	if err := ValidateCRMState(&crm); err != nil {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, err
	}

	route := buildRecruitingAutomationRoute(event, crm.RecruitingProject)
	if event.RecruitingProjectVersion != 0 {
		route = frozenRecruitingAutomationRoute(event)
	}
	if route.Disposition != "" {
		event.Disposition = route.Disposition
	}
	tools, err := recruitingAutomationProviderTools(route, allowedTools)
	if err != nil {
		return AutomationProviderInput{}, automationProviderCompactionStats{}, err
	}
	candidates := []AutomationCandidateContext{}
	jobs := []AutomationJobContext{}
	leads := []AutomationLeadContext{}
	onboardings := []OnboardingCase{}
	serviceRequests := []ServiceRequest{}
	fundCallbacks := []FundCallback{}
	if route.ConversationKind == automationConversationWeChatPrivate {
		var candidateScores map[string]int
		candidates, candidateScores, err = buildAutomationCandidateContext(event, crm.Candidates)
		if err != nil {
			return AutomationProviderInput{}, automationProviderCompactionStats{}, err
		}
		availableJobs := recruitingAutomationJobs(crm, route.RequireAuthoritativeJob)
		var jobScores map[string]int
		jobs, jobScores = buildAutomationJobContext(event, candidates, availableJobs)
		leads = buildAutomationLeadContext(
			event,
			candidates,
			candidateScores,
			jobs,
			jobScores,
			crm.Leads,
		)
		candidateIDs := automationProviderCandidateIDs(candidates)
		for _, onboarding := range crm.OnboardingCases {
			if _, ok := candidateIDs[onboarding.CandidateID]; ok {
				onboardings = append(onboardings, onboarding)
			}
		}
		for _, request := range crm.ServiceRequests {
			if _, ok := candidateIDs[request.CandidateID]; ok {
				serviceRequests = append(serviceRequests, request)
			}
		}
		for _, callback := range crm.FundCallbacks {
			if _, ok := candidateIDs[callback.CandidateID]; ok {
				fundCallbacks = append(fundCallbacks, callback)
			}
		}
	}
	sort.Slice(onboardings, func(i, j int) bool { return onboardings[i].ID < onboardings[j].ID })
	sort.Slice(serviceRequests, func(i, j int) bool { return serviceRequests[i].ID < serviceRequests[j].ID })
	sort.Slice(fundCallbacks, func(i, j int) bool { return fundCallbacks[i].ID < fundCallbacks[j].ID })
	if len(onboardings) > automationProviderOpsLimit {
		onboardings = onboardings[:automationProviderOpsLimit]
	}
	if len(serviceRequests) > automationProviderOpsLimit {
		serviceRequests = serviceRequests[:automationProviderOpsLimit]
	}
	if len(fundCallbacks) > automationProviderOpsLimit {
		fundCallbacks = fundCallbacks[:automationProviderOpsLimit]
	}

	input := AutomationProviderInput{
		SchemaVersion:   AutomationProviderInputVersion,
		RunID:           runID,
		EngineVersion:   AutomationEngineVersion,
		Recruiting:      automationRecruitingContext(route),
		Message:         automationMessageContext(event),
		Candidates:      candidates,
		Jobs:            jobs,
		Leads:           leads,
		Onboardings:     onboardings,
		ServiceRequests: serviceRequests,
		FundCallbacks:   fundCallbacks,
		AllowedTools:    tools,
	}
	stats, err := boundAutomationProviderInput(&input, len(candidates) != 0)
	if err != nil {
		return AutomationProviderInput{}, stats, err
	}
	return input, stats, nil
}

func normalizeAutomationProviderTools(tools []AutomationToolName) ([]AutomationToolName, error) {
	result := append([]AutomationToolName(nil), tools...)
	seen := make(map[AutomationToolName]struct{}, len(result))
	for i, tool := range result {
		if !IsValidAutomationToolName(tool) && tool != automationRecruitingToolJobCreate {
			return nil, NewAutomationValidationError(
				fmt.Sprintf("allowed_tools[%d]", i),
				"unsupported automation tool",
			)
		}
		if _, exists := seen[tool]; exists {
			return nil, NewAutomationValidationError(
				fmt.Sprintf("allowed_tools[%d]", i),
				"duplicates an earlier tool",
			)
		}
		seen[tool] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i] < result[j]
	})
	if result == nil {
		result = []AutomationToolName{}
	}
	return result, nil
}

func recruitingAutomationProviderTools(
	route RecruitingAutomationRoute,
	requested []AutomationToolName,
) ([]AutomationToolName, error) {
	normalized, err := normalizeAutomationProviderTools(requested)
	if err != nil {
		return nil, err
	}
	requestedSet := make(map[AutomationToolName]struct{}, len(normalized))
	for _, tool := range normalized {
		requestedSet[tool] = struct{}{}
	}
	if recruitingAutomationRequestedAllTools(normalized) {
		requestedSet[automationRecruitingToolJobCreate] = struct{}{}
	}

	result := make([]AutomationToolName, 0, len(route.AllowedTools))
	for _, tool := range route.AllowedTools {
		if _, ok := requestedSet[tool]; ok {
			result = append(result, tool)
		}
	}
	return normalizeAutomationProviderTools(result)
}

func recruitingAutomationRequestedAllTools(tools []AutomationToolName) bool {
	all, err := normalizeAutomationProviderTools(automationAllTools())
	if err != nil || len(tools) != len(all) {
		return false
	}
	for i := range tools {
		if tools[i] != all[i] {
			return false
		}
	}
	return true
}

func recruitingAutomationJobs(crm CRMState, authoritativeOnly bool) []Job {
	if !authoritativeOnly {
		return append([]Job{}, crm.Jobs...)
	}
	authoritative := make(map[string]struct{}, len(crm.JobProvenance))
	for _, provenance := range crm.JobProvenance {
		if provenance.JobID == "" {
			continue
		}
		switch provenance.Decision {
		case JobIngestionCreate,
			JobIngestionUpdate,
			JobIngestionDuplicate,
			JobIngestionNoop:
			authoritative[provenance.JobID] = struct{}{}
		}
	}
	jobs := make([]Job, 0, len(authoritative))
	for _, job := range crm.Jobs {
		if _, ok := authoritative[job.ID]; ok {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

type automationRankedCandidate struct {
	context  AutomationCandidateContext
	priority int
	score    int
}

func buildAutomationCandidateContext(
	event MessageEvent,
	candidates []Candidate,
) ([]AutomationCandidateContext, map[string]int, error) {
	query := automationProviderTerms(event.Text)
	ranked := make([]automationRankedCandidate, 0, 1)
	linkedFound := event.CandidateID == ""

	for _, candidate := range candidates {
		if event.CandidateID != "" {
			if candidate.ID != event.CandidateID {
				continue
			}
			linkedFound = true
		} else if candidate.SourceEventID != event.ID &&
			(candidate.WeChatID == "" ||
				(candidate.WeChatID != event.AuthorID && candidate.WeChatID != event.PeerID)) {
			continue
		}
		ranked = append(ranked, automationRankedCandidate{
			context:  automationCandidateContext(candidate),
			priority: 0,
			score: automationProviderTermScore(query,
				candidate.FullName,
				candidate.Location,
				strings.Join(candidate.Skills, " "),
				candidate.Summary,
			),
		})
	}
	if !linkedFound {
		return nil, nil, NewAutomationValidationError("event.candidate_id", "references a missing candidate")
	}
	if event.CandidateID == "" && len(ranked) > 1 {
		return nil, nil, NewAutomationValidationError(
			"event.peer_id",
			"matches multiple candidate identities",
		)
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].priority != ranked[j].priority {
			return ranked[i].priority < ranked[j].priority
		}
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].context.ID < ranked[j].context.ID
	})
	if len(ranked) > automationProviderCandidateLimit {
		ranked = ranked[:automationProviderCandidateLimit]
	}

	result := make([]AutomationCandidateContext, len(ranked))
	scores := make(map[string]int, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].context
		scores[result[i].ID] = ranked[i].score
	}
	return result, scores, nil
}

func automationProviderCandidateIDs(candidates []AutomationCandidateContext) map[string]struct{} {
	result := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		result[candidate.ID] = struct{}{}
	}
	return result
}

type automationRankedJob struct {
	context AutomationJobContext
	score   int
}

func buildAutomationJobContext(
	event MessageEvent,
	candidates []AutomationCandidateContext,
	jobs []Job,
) ([]AutomationJobContext, map[string]int) {
	queryText := event.Text
	if len(candidates) > 0 && candidates[0].ID == event.CandidateID {
		queryText += " " + strings.Join(candidates[0].Skills, " ")
	}
	query := automationProviderTerms(queryText)
	ranked := make([]automationRankedJob, 0, len(jobs))
	for _, job := range jobs {
		if job.Status != JobStatusOpen {
			continue
		}
		ranked = append(ranked, automationRankedJob{
			context: automationJobContext(job),
			score: automationProviderTermScore(query,
				job.Title,
				job.Company,
				job.Location,
				job.Description,
				strings.Join(job.Requirements, " "),
			),
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].context.ID < ranked[j].context.ID
	})
	if len(ranked) > automationProviderJobLimit {
		ranked = ranked[:automationProviderJobLimit]
	}

	result := make([]AutomationJobContext, len(ranked))
	scores := make(map[string]int, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].context
		scores[result[i].ID] = ranked[i].score
	}
	return result, scores
}

type automationRankedLead struct {
	context  AutomationLeadContext
	priority int
	score    int
}

func buildAutomationLeadContext(
	event MessageEvent,
	candidates []AutomationCandidateContext,
	candidateScores map[string]int,
	jobs []AutomationJobContext,
	jobScores map[string]int,
	leads []Lead,
) []AutomationLeadContext {
	candidateIDs := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidateIDs[candidate.ID] = struct{}{}
	}
	jobIDs := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		jobIDs[job.ID] = struct{}{}
	}

	ranked := make([]automationRankedLead, 0, len(leads))
	for _, lead := range leads {
		if _, ok := candidateIDs[lead.CandidateID]; !ok {
			continue
		}
		if _, ok := jobIDs[lead.JobID]; !ok {
			continue
		}
		priority := 1
		if lead.CandidateID == event.CandidateID {
			priority = 0
		}
		ranked = append(ranked, automationRankedLead{
			context:  automationLeadContext(lead),
			priority: priority,
			score:    candidateScores[lead.CandidateID] + jobScores[lead.JobID],
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].priority != ranked[j].priority {
			return ranked[i].priority < ranked[j].priority
		}
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].context.ID < ranked[j].context.ID
	})
	if len(ranked) > automationProviderLeadLimit {
		ranked = ranked[:automationProviderLeadLimit]
	}

	result := make([]AutomationLeadContext, len(ranked))
	for i := range ranked {
		result[i] = ranked[i].context
	}
	return result
}

func boundAutomationProviderInput(
	input *AutomationProviderInput,
	preserveLinkedCandidate bool,
) (automationProviderCompactionStats, error) {
	stats := automationProviderCompactionStats{TargetBytes: automationProviderTargetBytes}
	for {
		data, err := json.Marshal(input)
		if err != nil {
			return stats, NewAutomationValidationError("provider_input", "could not be encoded")
		}
		if stats.OriginalBytes == 0 {
			stats.OriginalBytes = len(data)
		}
		stats.FinalBytes = len(data)
		if len(data) <= automationProviderTargetBytes {
			return stats, nil
		}
		switch {
		case len(input.FundCallbacks) > 0:
			input.FundCallbacks = input.FundCallbacks[:len(input.FundCallbacks)-1]
			stats.RemovedFundCallbacks++
		case len(input.ServiceRequests) > 0:
			input.ServiceRequests = input.ServiceRequests[:len(input.ServiceRequests)-1]
			stats.RemovedServiceRequests++
		case len(input.Onboardings) > 0:
			input.Onboardings = input.Onboardings[:len(input.Onboardings)-1]
			stats.RemovedOnboardings++
		case len(input.Leads) > 0:
			input.Leads = input.Leads[:len(input.Leads)-1]
			stats.RemovedLeads++
		case len(input.Jobs) > 0:
			input.Jobs = input.Jobs[:len(input.Jobs)-1]
			stats.RemovedJobs++
		case len(input.Candidates) > 1:
			input.Candidates = input.Candidates[:len(input.Candidates)-1]
			stats.RemovedCandidates++
		case len(input.Candidates) == 1 && !preserveLinkedCandidate:
			input.Candidates = input.Candidates[:0]
			stats.RemovedCandidates++
		default:
			if len(data) <= AutomationMaxProviderBytes {
				return stats, nil
			}
			return stats, NewAutomationValidationError(
				"provider_input",
				fmt.Sprintf("must be at most %d bytes", AutomationMaxProviderBytes),
			)
		}
		stats.Compacted = true
	}
}

func automationProviderTerms(text string) map[string]struct{} {
	terms := make(map[string]struct{})
	for _, term := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		if len([]rune(term)) >= 2 {
			terms[term] = struct{}{}
		}
	}
	return terms
}

func automationProviderTermScore(query map[string]struct{}, values ...string) int {
	if len(query) == 0 {
		return 0
	}
	seen := make(map[string]struct{})
	score := 0
	for _, value := range values {
		for term := range automationProviderTerms(value) {
			if _, wanted := query[term]; !wanted {
				continue
			}
			if _, counted := seen[term]; counted {
				continue
			}
			seen[term] = struct{}{}
			score++
		}
	}
	return score
}

func automationMessageContext(event MessageEvent) AutomationMessageContext {
	return AutomationMessageContext{
		ID:           event.ID,
		MessageKey:   event.MessageKey,
		Source:       event.Source,
		Mid:          event.Mid,
		ChatType:     event.ChatType,
		PeerID:       event.PeerID,
		AuthorID:     event.AuthorID,
		GroupID:      event.GroupID,
		Text:         event.Text,
		Kinds:        append([]string(nil), event.Kinds...),
		Timestamp:    event.Timestamp,
		Suspicious:   event.Suspicious,
		Disposition:  event.Disposition,
		ReviewStatus: event.ReviewStatus,
		CandidateID:  event.CandidateID,
		Version:      event.Version,
		CreatedAt:    event.CreatedAt,
		UpdatedAt:    event.UpdatedAt,
	}
}

func automationRecruitingContext(route RecruitingAutomationRoute) AutomationRecruitingContext {
	context := AutomationRecruitingContext{
		OrganizationName:        route.OrganizationName,
		ConversationKind:        route.ConversationKind,
		Disposition:             route.Disposition,
		RequireAuthoritativeJob: route.RequireAuthoritativeJob,
	}
	if route.SourceGroup != nil {
		context.SourceGroup = &AutomationRecruitingSourceContext{
			ID:          route.SourceGroup.ID,
			GroupID:     route.SourceGroup.GroupID,
			DisplayName: route.SourceGroup.DisplayName,
			Version:     route.SourceGroup.Version,
		}
	}
	return context
}

func automationCandidateContext(candidate Candidate) AutomationCandidateContext {
	return AutomationCandidateContext{
		ID:            candidate.ID,
		FullName:      candidate.FullName,
		WeChatID:      candidate.WeChatID,
		Phone:         candidate.Phone,
		Email:         candidate.Email,
		Location:      candidate.Location,
		Source:        candidate.Source,
		SourceEventID: candidate.SourceEventID,
		Skills:        append([]string(nil), candidate.Skills...),
		Owner:         candidate.Owner,
		Status:        candidate.Status,
		Summary:       candidate.Summary,
		Version:       candidate.Version,
		CreatedAt:     candidate.CreatedAt,
		UpdatedAt:     candidate.UpdatedAt,
	}
}

func automationJobContext(job Job) AutomationJobContext {
	return AutomationJobContext{
		ID:                    job.ID,
		Title:                 job.Title,
		Company:               job.Company,
		Location:              job.Location,
		EmploymentType:        job.EmploymentType,
		Description:           job.Description,
		Requirements:          append([]string(nil), job.Requirements...),
		DemandCode:            job.DemandCode,
		SiteName:              job.SiteName,
		OnsiteContact:         job.OnsiteContact,
		Headcount:             job.Headcount,
		Priority:              job.Priority,
		StartAt:               cloneAutomationTimePointer(job.StartAt),
		DeadlineAt:            cloneAutomationTimePointer(job.DeadlineAt),
		Shift:                 job.Shift,
		WorkSchedule:          job.WorkSchedule,
		CompensationSummary:   job.CompensationSummary,
		Accommodation:         job.Accommodation,
		TransportRequirements: job.TransportRequirements,
		RequiredDocuments:     append([]string(nil), job.RequiredDocuments...),
		Owner:                 job.Owner,
		Status:                job.Status,
		Version:               job.Version,
		CreatedAt:             job.CreatedAt,
		UpdatedAt:             job.UpdatedAt,
	}
}

func automationLeadContext(lead Lead) AutomationLeadContext {
	return AutomationLeadContext{
		ID:             lead.ID,
		CandidateID:    lead.CandidateID,
		JobID:          lead.JobID,
		Stage:          lead.Stage,
		Owner:          lead.Owner,
		Version:        lead.Version,
		CreatedAt:      lead.CreatedAt,
		UpdatedAt:      lead.UpdatedAt,
		StageChangedAt: lead.StageChangedAt,
	}
}
