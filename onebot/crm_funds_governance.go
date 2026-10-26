package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type crmFundsNotFoundError struct {
	resource string
}

var errCRMFundCallbackApprovedActionReplay = errors.New("fund callback approved action replay")

func (e *crmFundsNotFoundError) Error() string {
	return e.resource + " not found"
}

type FundCallbackCreate struct {
	CandidateID      string             `json:"candidate_id"`
	JobID            string             `json:"job_id,omitempty"`
	LeadID           string             `json:"lead_id,omitempty"`
	OnboardingCaseID string             `json:"onboarding_case_id,omitempty"`
	AmountMinor      int64              `json:"amount_minor"`
	Currency         string             `json:"currency"`
	Condition        string             `json:"condition"`
	PromisedBy       string             `json:"promised_by"`
	DueAt            *time.Time         `json:"due_at,omitempty"`
	Evidence         string             `json:"evidence,omitempty"`
	Status           FundCallbackStatus `json:"status,omitempty"`
	Actor            string             `json:"-"`
}

type FundCallbackUpdate struct {
	ExpectedVersion  uint64              `json:"expected_version"`
	CandidateID      *string             `json:"candidate_id,omitempty"`
	JobID            *string             `json:"job_id,omitempty"`
	LeadID           *string             `json:"lead_id,omitempty"`
	OnboardingCaseID *string             `json:"onboarding_case_id,omitempty"`
	AmountMinor      *int64              `json:"amount_minor,omitempty"`
	Currency         *string             `json:"currency,omitempty"`
	Condition        *string             `json:"condition,omitempty"`
	PromisedBy       *string             `json:"promised_by,omitempty"`
	DueAt            *time.Time          `json:"due_at,omitempty"`
	ClearDueAt       bool                `json:"clear_due_at,omitempty"`
	Evidence         *string             `json:"evidence,omitempty"`
	Status           *FundCallbackStatus `json:"status,omitempty"`
	Actor            string              `json:"-"`
}

type FundCommitmentConfirmation struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Confirmed       bool   `json:"confirmed"`
	OperatorID      string `json:"-"`
}

type FundFinalConfirmation struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Confirmed       bool   `json:"confirmed"`
	OperatorID      string `json:"-"`
	FinalResult     string `json:"final_result"`
}

type CandidateAnonymizeRequest struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Confirmed       bool   `json:"confirmed"`
	OperatorID      string `json:"-"`
}

type crmFundCallbackFilter struct {
	Query            string
	CandidateID      string
	JobID            string
	LeadID           string
	OnboardingCaseID string
	Status           FundCallbackStatus
	Currency         string
	DueBefore        *time.Time
	DueAfter         *time.Time
	Limit            int
	Offset           int
}

type crmAuditFilter struct {
	Actor         string
	Operation     string
	EntityType    string
	EntityID      string
	SourceEventID string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
	Limit         int
	Offset        int
}

type CandidateArchiveExport struct {
	SchemaVersion   int              `json:"schema_version"`
	Candidate       Candidate        `json:"candidate"`
	Leads           []Lead           `json:"leads"`
	LeadStageEvents []LeadStageEvent `json:"lead_stage_events"`
	OnboardingCases []OnboardingCase `json:"onboarding_cases"`
	ServiceRequests []ServiceRequest `json:"service_requests"`
	FundCallbacks   []FundCallback   `json:"fund_callbacks"`
	Tasks           []FollowUpTask   `json:"tasks"`
	Notes           []Note           `json:"notes"`
	MessageEvents   []MessageEvent   `json:"message_events"`
	AuditEvents     []CRMAuditEvent  `json:"audit_events"`
}

type CRMReportSummary struct {
	GeneratedAt          time.Time        `json:"generated_at"`
	CandidateCounts      map[string]int   `json:"candidate_counts"`
	JobCounts            map[string]int   `json:"job_counts"`
	LeadCounts           map[string]int   `json:"lead_counts"`
	OnboardingCounts     map[string]int   `json:"onboarding_counts"`
	ServiceRequestCounts map[string]int   `json:"service_request_counts"`
	FundCallbackCounts   map[string]int   `json:"fund_callback_counts"`
	OpenServiceRequests  int              `json:"open_service_requests"`
	DueFundCallbacks     int              `json:"due_fund_callbacks"`
	OverdueFundCallbacks int              `json:"overdue_fund_callbacks"`
	CommittedAmountMinor map[string]int64 `json:"committed_amount_minor"`
	AuditEventCount      int              `json:"audit_event_count"`
}

func crmFundCallbackCreate(store *CRMStore, create FundCallbackCreate) (FundCallback, error) {
	normalized, err := crmNormalizeFundCallbackCreate(create)
	if err != nil {
		return FundCallback{}, err
	}

	var created FundCallback
	actor, err := crmNormalizeActor(normalized.Actor)
	if err != nil {
		return FundCallback{}, err
	}
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "fund_callback.create",
		EntityType: "fund_callback",
	}, func(state *CRMState, allocator *IDAllocator) error {
		id, err := allocator.Next(IDKindFundCallback)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created = FundCallback{
			ID:               id,
			CandidateID:      normalized.CandidateID,
			JobID:            normalized.JobID,
			LeadID:           normalized.LeadID,
			OnboardingCaseID: normalized.OnboardingCaseID,
			AmountMinor:      normalized.AmountMinor,
			Currency:         normalized.Currency,
			Condition:        normalized.Condition,
			PromisedBy:       normalized.PromisedBy,
			DueAt:            cloneCRMTimePointer(normalized.DueAt),
			Evidence:         normalized.Evidence,
			Status:           FundCallbackStatusDraft,
			Version:          1,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		state.FundCallbacks = append(state.FundCallbacks, created)
		return nil
	})
	return created, err
}

func crmFundCallbackCreateApprovedAction(
	store *CRMStore,
	actionID string,
	permissionRequestID string,
	create FundCallbackCreate,
) (FundCallbackApprovedActionResult, error) {
	if store == nil {
		return FundCallbackApprovedActionResult{}, NewCRMValidationError("store", "is required")
	}
	actionID, err := normalizeCRMString("action_id", actionID, 80, true, false)
	if err != nil {
		return FundCallbackApprovedActionResult{}, err
	}
	permissionRequestID, err = normalizeCRMString(
		"permission_request_id",
		permissionRequestID,
		80,
		true,
		false,
	)
	if err != nil {
		return FundCallbackApprovedActionResult{}, err
	}
	normalized, err := crmNormalizeFundCallbackCreate(create)
	if err != nil {
		return FundCallbackApprovedActionResult{}, err
	}
	actor, err := crmNormalizeActor(normalized.Actor)
	if err != nil {
		return FundCallbackApprovedActionResult{}, err
	}

	var result FundCallbackApprovedActionResult
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "fund_callback.create",
		EntityType: "fund_callback",
	}, func(state *CRMState, allocator *IDAllocator) error {
		for _, binding := range state.FundCallbackApprovedActions {
			if binding.ActionID != actionID {
				continue
			}
			if binding.PermissionRequestID != permissionRequestID {
				return NewCRMConflictError("approved action is bound to another permission request")
			}
			index := crmFundCallbackIndex(state.FundCallbacks, binding.FundCallbackID)
			if index < 0 {
				return NewCRMConflictError("approved action fund callback binding is invalid")
			}
			if err := validateFundCallbackApprovedActionBinding("approved_action", &binding); err != nil {
				return NewCRMConflictError("approved action original result is invalid")
			}
			result = *binding.OriginalResult
			return errCRMFundCallbackApprovedActionReplay
		}

		id, err := allocator.Next(IDKindFundCallback)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		callback := FundCallback{
			ID:               id,
			CandidateID:      normalized.CandidateID,
			JobID:            normalized.JobID,
			LeadID:           normalized.LeadID,
			OnboardingCaseID: normalized.OnboardingCaseID,
			AmountMinor:      normalized.AmountMinor,
			Currency:         normalized.Currency,
			Condition:        normalized.Condition,
			PromisedBy:       normalized.PromisedBy,
			DueAt:            cloneCRMTimePointer(normalized.DueAt),
			Evidence:         normalized.Evidence,
			Status:           FundCallbackStatusDraft,
			Version:          1,
			CreatedAt:        now,
			UpdatedAt:        now,
		}
		state.FundCallbacks = append(state.FundCallbacks, callback)
		result = crmFundCallbackApprovedActionResult(callback)
		resultSHA256, err := crmFundCallbackApprovedActionResultSHA256(result)
		if err != nil {
			return err
		}
		originalResult := result
		state.FundCallbackApprovedActions = append(
			state.FundCallbackApprovedActions,
			FundCallbackApprovedActionBinding{
				ActionID:             actionID,
				PermissionRequestID:  permissionRequestID,
				FundCallbackID:       callback.ID,
				OriginalResult:       &originalResult,
				OriginalResultSHA256: resultSHA256,
			},
		)
		return nil
	})
	if errors.Is(err, errCRMFundCallbackApprovedActionReplay) {
		return result, nil
	}
	return result, err
}

func crmFundCallbackApprovedActionResult(callback FundCallback) FundCallbackApprovedActionResult {
	return FundCallbackApprovedActionResult{
		ID:          callback.ID,
		CandidateID: callback.CandidateID,
		AmountMinor: callback.AmountMinor,
		Currency:    callback.Currency,
		Status:      callback.Status,
		Version:     callback.Version,
		UpdatedAt:   callback.UpdatedAt,
	}
}

func crmFundCallbackApprovedActionResultSHA256(result FundCallbackApprovedActionResult) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode fund callback approved action result: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
}

func crmFundCallbackList(store *CRMStore, filter crmFundCallbackFilter) (int, []FundCallback, error) {
	normalized, err := crmNormalizeFundCallbackFilter(filter)
	if err != nil {
		return 0, nil, err
	}
	state := store.Snapshot()
	callbacks := make([]FundCallback, 0, len(state.FundCallbacks))
	for _, callback := range state.FundCallbacks {
		if crmFundCallbackMatches(callback, normalized) {
			callbacks = append(callbacks, callback)
		}
	}
	sort.Slice(callbacks, func(i, j int) bool {
		if callbacks[i].UpdatedAt.Equal(callbacks[j].UpdatedAt) {
			return callbacks[i].ID < callbacks[j].ID
		}
		return callbacks[i].UpdatedAt.After(callbacks[j].UpdatedAt)
	})
	total := len(callbacks)
	return total, crmPipelinePage(callbacks, normalized.Offset, normalized.Limit), nil
}

func crmFundCallbackGet(store *CRMStore, id string) (FundCallback, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return FundCallback{}, err
	}
	state := store.Snapshot()
	index := crmFundCallbackIndex(state.FundCallbacks, id)
	if index < 0 {
		return FundCallback{}, &crmFundsNotFoundError{resource: "fund callback"}
	}
	return state.FundCallbacks[index], nil
}

func crmFundCallbackUpdate(store *CRMStore, id string, update FundCallbackUpdate) (FundCallback, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return FundCallback{}, err
	}
	normalized, err := crmNormalizeFundCallbackUpdate(update)
	if err != nil {
		return FundCallback{}, err
	}

	var updated FundCallback
	actor, err := crmNormalizeActor(normalized.Actor)
	if err != nil {
		return FundCallback{}, err
	}
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "fund_callback.update",
		EntityType: "fund_callback",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmFundCallbackIndex(state.FundCallbacks, id)
		if index < 0 {
			return &crmFundsNotFoundError{resource: "fund callback"}
		}
		current := state.FundCallbacks[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("fund callback version conflict")
		}
		if current.Status == FundCallbackStatusCompleted || current.Status == FundCallbackStatusCancelled {
			return NewCRMConflictError("fund callback is terminal")
		}
		if current.CommitmentAt != nil && crmFundUpdateChangesCommitment(normalized) {
			return NewCRMConflictError("confirmed commitment fields cannot be changed")
		}
		if normalized.Status != nil && current.Status != *normalized.Status {
			if !crmCanTransitionFundCallback(current, *normalized.Status) {
				return NewCRMConflictError("fund callback status transition is not allowed")
			}
			current.Status = *normalized.Status
		}
		crmApplyFundCallbackUpdate(&current, normalized)
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.FundCallbacks[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmFundCallbackConfirmCommitment(
	store *CRMStore,
	id string,
	confirmation FundCommitmentConfirmation,
) (FundCallback, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return FundCallback{}, err
	}
	if err := validateCRMVersion("expected_version", confirmation.ExpectedVersion); err != nil {
		return FundCallback{}, err
	}
	if !confirmation.Confirmed {
		return FundCallback{}, NewCRMValidationError("confirmed", "explicit confirmation is required")
	}
	confirmation.OperatorID, err = normalizeCRMString("operator_id", confirmation.OperatorID, 80, true, false)
	if err != nil {
		return FundCallback{}, err
	}

	var updated FundCallback
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      confirmation.OperatorID,
		Operation:  "fund_callback.confirm_commitment",
		EntityType: "fund_callback",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmFundCallbackIndex(state.FundCallbacks, id)
		if index < 0 {
			return &crmFundsNotFoundError{resource: "fund callback"}
		}
		current := state.FundCallbacks[index]
		if current.Version != confirmation.ExpectedVersion {
			return NewCRMConflictError("fund callback version conflict")
		}
		if current.Status != FundCallbackStatusAwaitingCommitment || current.CommitmentAt != nil {
			return NewCRMConflictError("fund callback is not awaiting commitment")
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		current.CommitmentBy = confirmation.OperatorID
		current.CommitmentAt = &now
		current.Status = FundCallbackStatusCommitted
		current.Version++
		current.UpdatedAt = now
		state.FundCallbacks[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmFundCallbackConfirmFinal(
	store *CRMStore,
	id string,
	confirmation FundFinalConfirmation,
) (FundCallback, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return FundCallback{}, err
	}
	if err := validateCRMVersion("expected_version", confirmation.ExpectedVersion); err != nil {
		return FundCallback{}, err
	}
	if !confirmation.Confirmed {
		return FundCallback{}, NewCRMValidationError("confirmed", "explicit confirmation is required")
	}
	confirmation.OperatorID, err = normalizeCRMString("operator_id", confirmation.OperatorID, 80, true, false)
	if err != nil {
		return FundCallback{}, err
	}
	confirmation.FinalResult, err = normalizeCRMString("final_result", confirmation.FinalResult, 4000, true, true)
	if err != nil {
		return FundCallback{}, err
	}

	var updated FundCallback
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      confirmation.OperatorID,
		Operation:  "fund_callback.confirm_final",
		EntityType: "fund_callback",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmFundCallbackIndex(state.FundCallbacks, id)
		if index < 0 {
			return &crmFundsNotFoundError{resource: "fund callback"}
		}
		current := state.FundCallbacks[index]
		if current.Version != confirmation.ExpectedVersion {
			return NewCRMConflictError("fund callback version conflict")
		}
		if current.Status != FundCallbackStatusAwaitingFinal ||
			current.CommitmentAt == nil ||
			current.FinalConfirmedAt != nil {
			return NewCRMConflictError("fund callback is not awaiting final confirmation")
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		current.FinalResult = confirmation.FinalResult
		current.FinalConfirmedBy = confirmation.OperatorID
		current.FinalConfirmedAt = &now
		current.Status = FundCallbackStatusCompleted
		current.Version++
		current.UpdatedAt = now
		state.FundCallbacks[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmAuditList(state CRMState, filter crmAuditFilter) (int, []CRMAuditEvent, error) {
	normalized, err := crmNormalizeAuditFilter(filter)
	if err != nil {
		return 0, nil, err
	}
	events := make([]CRMAuditEvent, 0, len(state.AuditEvents))
	for _, event := range state.AuditEvents {
		if normalized.Actor != "" && event.Actor != normalized.Actor {
			continue
		}
		if normalized.Operation != "" && event.Operation != normalized.Operation {
			continue
		}
		if normalized.EntityType != "" && event.EntityType != normalized.EntityType {
			continue
		}
		if normalized.EntityID != "" && event.EntityID != normalized.EntityID {
			continue
		}
		if normalized.SourceEventID != "" && event.SourceEventID != normalized.SourceEventID {
			continue
		}
		if normalized.CreatedAfter != nil && event.CreatedAt.Before(*normalized.CreatedAfter) {
			continue
		}
		if normalized.CreatedBefore != nil && event.CreatedAt.After(*normalized.CreatedBefore) {
			continue
		}
		events = append(events, event)
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].CreatedAt.Equal(events[j].CreatedAt) {
			return events[i].ID < events[j].ID
		}
		return events[i].CreatedAt.After(events[j].CreatedAt)
	})
	total := len(events)
	return total, crmPipelinePage(events, normalized.Offset, normalized.Limit), nil
}

func crmFundCallbackLedger(
	store *CRMStore,
	id string,
) ([]FundCallbackLedgerEntry, error) {
	if store == nil {
		return nil, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return nil, err
	}
	state := store.Snapshot()
	if crmFundCallbackIndex(state.FundCallbacks, id) < 0 {
		return nil, &crmFundsNotFoundError{resource: "fund callback"}
	}
	entries := make([]FundCallbackLedgerEntry, 0)
	for _, entry := range state.FundCallbackLedger {
		if entry.FundCallbackID == id {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].CreatedAt.Equal(entries[j].CreatedAt) {
			return entries[i].ID < entries[j].ID
		}
		return entries[i].CreatedAt.Before(entries[j].CreatedAt)
	})
	return entries, nil
}

func crmCandidateExport(state CRMState, id string) (CandidateArchiveExport, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return CandidateArchiveExport{}, err
	}
	index := crmPipelineCandidateIndex(state.Candidates, id)
	if index < 0 {
		return CandidateArchiveExport{}, &crmFundsNotFoundError{resource: "candidate"}
	}
	export := CandidateArchiveExport{
		SchemaVersion:   state.SchemaVersion,
		Candidate:       state.Candidates[index],
		Leads:           []Lead{},
		LeadStageEvents: []LeadStageEvent{},
		OnboardingCases: []OnboardingCase{},
		ServiceRequests: []ServiceRequest{},
		FundCallbacks:   []FundCallback{},
		Tasks:           []FollowUpTask{},
		Notes:           []Note{},
		MessageEvents:   []MessageEvent{},
		AuditEvents:     []CRMAuditEvent{},
	}
	leadIDs := map[string]struct{}{}
	entityIDs := map[string]struct{}{id: {}}
	eventIDs := map[string]struct{}{}
	for _, lead := range state.Leads {
		if lead.CandidateID == id {
			export.Leads = append(export.Leads, lead)
			leadIDs[lead.ID] = struct{}{}
			entityIDs[lead.ID] = struct{}{}
		}
	}
	for _, event := range state.LeadStageEvents {
		if _, ok := leadIDs[event.LeadID]; ok {
			export.LeadStageEvents = append(export.LeadStageEvents, event)
			entityIDs[event.ID] = struct{}{}
		}
	}
	for _, onboarding := range state.OnboardingCases {
		if onboarding.CandidateID == id {
			export.OnboardingCases = append(export.OnboardingCases, onboarding)
			entityIDs[onboarding.ID] = struct{}{}
		}
	}
	for _, request := range state.ServiceRequests {
		if request.CandidateID == id {
			export.ServiceRequests = append(export.ServiceRequests, request)
			entityIDs[request.ID] = struct{}{}
		}
	}
	for _, callback := range state.FundCallbacks {
		if callback.CandidateID == id {
			export.FundCallbacks = append(export.FundCallbacks, callback)
			entityIDs[callback.ID] = struct{}{}
		}
	}
	for _, task := range state.Tasks {
		_, linkedLead := leadIDs[task.LeadID]
		if task.CandidateID == id || linkedLead {
			export.Tasks = append(export.Tasks, task)
			entityIDs[task.ID] = struct{}{}
		}
	}
	for _, note := range state.Notes {
		_, linkedLead := leadIDs[note.TargetID]
		if note.TargetType == NoteTargetCandidate && note.TargetID == id || linkedLead {
			export.Notes = append(export.Notes, note)
			entityIDs[note.ID] = struct{}{}
		}
	}
	for _, event := range state.MessageEvents {
		if event.CandidateID == id {
			export.MessageEvents = append(export.MessageEvents, event)
			eventIDs[event.ID] = struct{}{}
			entityIDs[event.ID] = struct{}{}
		}
	}
	for _, event := range state.AuditEvents {
		_, entityMatch := entityIDs[event.EntityID]
		_, sourceMatch := eventIDs[event.SourceEventID]
		if entityMatch || sourceMatch {
			export.AuditEvents = append(export.AuditEvents, event)
		}
	}
	crmSortCandidateArchive(&export)
	return export, nil
}

func crmCandidateAnonymize(
	store *CRMStore,
	id string,
	request CandidateAnonymizeRequest,
) (Candidate, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Candidate{}, err
	}
	if err := validateCRMVersion("expected_version", request.ExpectedVersion); err != nil {
		return Candidate{}, err
	}
	if !request.Confirmed {
		return Candidate{}, NewCRMValidationError("confirmed", "explicit confirmation is required")
	}
	request.OperatorID, err = normalizeCRMString("operator_id", request.OperatorID, 80, true, false)
	if err != nil {
		return Candidate{}, err
	}

	var anonymized Candidate
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      request.OperatorID,
		Operation:  "candidate.anonymize",
		EntityType: "candidate",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineCandidateIndex(state.Candidates, id)
		if index < 0 {
			return &crmFundsNotFoundError{resource: "candidate"}
		}
		current := state.Candidates[index]
		if current.Version != request.ExpectedVersion {
			return NewCRMConflictError("candidate version conflict")
		}
		current.FullName = "Anonymized " + current.ID
		current.WeChatID = ""
		current.Phone = ""
		current.Email = ""
		current.Location = ""
		current.Source = "anonymized"
		current.Skills = []string{}
		current.Owner = ""
		current.Status = CandidateStatusArchived
		current.Summary = ""
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.Candidates[index] = current
		anonymized = current
		return nil
	})
	return anonymized, err
}

func crmReportSummary(state CRMState, now time.Time) CRMReportSummary {
	now = now.UTC()
	summary := CRMReportSummary{
		GeneratedAt:          now,
		CandidateCounts:      crmStringCountMap("active", "paused", "placed", "archived"),
		JobCounts:            crmStringCountMap("open", "closed", "archived"),
		LeadCounts:           crmStringCountMap("sourced", "screening", "interview", "offer", "hired", "rejected", "withdrawn"),
		OnboardingCounts:     crmStringCountMap("pending", "collecting", "ready", "transport_scheduled", "en_route", "arrived", "checked_in", "completed", "blocked", "cancelled"),
		ServiceRequestCounts: crmStringCountMap("open", "triaged", "assigned", "in_progress", "awaiting_requester", "resolved", "closed", "escalated", "cancelled"),
		FundCallbackCounts:   crmStringCountMap("draft", "awaiting_commitment", "committed", "callback_due", "awaiting_final", "completed", "overdue", "disputed", "cancelled"),
		CommittedAmountMinor: map[string]int64{},
		AuditEventCount:      len(state.AuditEvents),
	}
	for _, candidate := range state.Candidates {
		summary.CandidateCounts[string(candidate.Status)]++
	}
	for _, job := range state.Jobs {
		summary.JobCounts[string(job.Status)]++
	}
	for _, lead := range state.Leads {
		summary.LeadCounts[string(lead.Stage)]++
	}
	for _, onboarding := range state.OnboardingCases {
		summary.OnboardingCounts[string(onboarding.Status)]++
	}
	for _, request := range state.ServiceRequests {
		summary.ServiceRequestCounts[string(request.Status)]++
		if request.Status != ServiceRequestStatusResolved &&
			request.Status != ServiceRequestStatusClosed &&
			request.Status != ServiceRequestStatusCancelled {
			summary.OpenServiceRequests++
		}
	}
	for _, callback := range state.FundCallbacks {
		summary.FundCallbackCounts[string(callback.Status)]++
		if callback.CommitmentAt != nil && callback.Status != FundCallbackStatusCancelled {
			summary.CommittedAmountMinor[callback.Currency] += callback.AmountMinor
		}
		if callback.DueAt == nil ||
			callback.Status == FundCallbackStatusCompleted ||
			callback.Status == FundCallbackStatusCancelled {
			continue
		}
		if !callback.DueAt.After(now) {
			summary.DueFundCallbacks++
		}
		if callback.DueAt.Before(now) {
			summary.OverdueFundCallbacks++
		}
	}
	return summary
}

func crmReportCSV(state CRMState) ([]byte, error) {
	rows := [][]string{{
		"entity_type", "entity_id", "candidate_id", "job_id", "status",
		"owner", "due_at", "amount_minor", "currency", "updated_at",
	}}
	for _, candidate := range state.Candidates {
		rows = append(rows, []string{
			"candidate", candidate.ID, candidate.ID, "", string(candidate.Status),
			candidate.Owner, "", "", "", crmReportTime(candidate.UpdatedAt),
		})
	}
	for _, job := range state.Jobs {
		rows = append(rows, []string{
			"job", job.ID, "", job.ID, string(job.Status),
			job.Owner, crmReportOptionalTime(job.DeadlineAt), "", "", crmReportTime(job.UpdatedAt),
		})
	}
	for _, lead := range state.Leads {
		rows = append(rows, []string{
			"lead", lead.ID, lead.CandidateID, lead.JobID, string(lead.Stage),
			lead.Owner, "", "", "", crmReportTime(lead.UpdatedAt),
		})
	}
	for _, onboarding := range state.OnboardingCases {
		rows = append(rows, []string{
			"onboarding_case", onboarding.ID, onboarding.CandidateID, onboarding.JobID,
			string(onboarding.Status), "", crmReportOptionalTime(onboarding.EntryAt),
			"", "", crmReportTime(onboarding.UpdatedAt),
		})
	}
	for _, request := range state.ServiceRequests {
		rows = append(rows, []string{
			"service_request", request.ID, request.CandidateID, request.JobID,
			string(request.Status), request.Owner, crmReportOptionalTime(request.DueAt),
			"", "", crmReportTime(request.UpdatedAt),
		})
	}
	for _, callback := range state.FundCallbacks {
		rows = append(rows, []string{
			"fund_callback", callback.ID, callback.CandidateID, callback.JobID,
			string(callback.Status), callback.PromisedBy, crmReportOptionalTime(callback.DueAt),
			strconv.FormatInt(callback.AmountMinor, 10), callback.Currency, crmReportTime(callback.UpdatedAt),
		})
	}
	sort.Slice(rows[1:], func(i, j int) bool {
		left := rows[i+1]
		right := rows[j+1]
		if left[0] == right[0] {
			return left[1] < right[1]
		}
		return left[0] < right[0]
	})
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	for _, row := range rows {
		for index := range row {
			row[index] = crmSafeCSVCell(row[index])
		}
		if err := writer.Write(row); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func crmNormalizeFundCallbackCreate(create FundCallbackCreate) (FundCallbackCreate, error) {
	var err error
	if create.CandidateID, err = normalizeCRMString("candidate_id", create.CandidateID, 80, true, false); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.JobID, err = normalizeCRMString("job_id", create.JobID, 80, false, false); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.LeadID, err = normalizeCRMString("lead_id", create.LeadID, 80, false, false); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.OnboardingCaseID, err = normalizeCRMString("onboarding_case_id", create.OnboardingCaseID, 80, false, false); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.AmountMinor <= 0 {
		return FundCallbackCreate{}, NewCRMValidationError("amount_minor", "must be greater than zero")
	}
	create.Currency = strings.ToUpper(strings.TrimSpace(create.Currency))
	if !isValidCRMCurrency(create.Currency) {
		return FundCallbackCreate{}, NewCRMValidationError("currency", "must be a three-letter currency code")
	}
	if create.Condition, err = normalizeCRMString("condition", create.Condition, 4000, true, true); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.PromisedBy, err = normalizeCRMString("promised_by", create.PromisedBy, 160, true, false); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.DueAt, err = normalizeCRMOptionalTime("due_at", create.DueAt); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.Evidence, err = normalizeCRMString("evidence", create.Evidence, 4000, false, true); err != nil {
		return FundCallbackCreate{}, err
	}
	if create.Status != "" && create.Status != FundCallbackStatusDraft {
		return FundCallbackCreate{}, NewCRMValidationError("status", "new fund callbacks must be drafts")
	}
	create.Status = FundCallbackStatusDraft
	return create, nil
}

func crmNormalizeFundCallbackUpdate(update FundCallbackUpdate) (FundCallbackUpdate, error) {
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return FundCallbackUpdate{}, err
	}
	if !crmFundCallbackUpdateHasFields(update) {
		return FundCallbackUpdate{}, NewCRMValidationError("fund_callback", "at least one update field is required")
	}
	var err error
	if update.CandidateID, err = crmNormalizeOptionalString("candidate_id", update.CandidateID, 80, true, false); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.JobID, err = crmNormalizeOptionalString("job_id", update.JobID, 80, false, false); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.LeadID, err = crmNormalizeOptionalString("lead_id", update.LeadID, 80, false, false); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.OnboardingCaseID, err = crmNormalizeOptionalString("onboarding_case_id", update.OnboardingCaseID, 80, false, false); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.AmountMinor != nil && *update.AmountMinor <= 0 {
		return FundCallbackUpdate{}, NewCRMValidationError("amount_minor", "must be greater than zero")
	}
	if update.Currency != nil {
		value := strings.ToUpper(strings.TrimSpace(*update.Currency))
		if !isValidCRMCurrency(value) {
			return FundCallbackUpdate{}, NewCRMValidationError("currency", "must be a three-letter currency code")
		}
		update.Currency = &value
	}
	if update.Condition, err = crmNormalizeOptionalString("condition", update.Condition, 4000, true, true); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.PromisedBy, err = crmNormalizeOptionalString("promised_by", update.PromisedBy, 160, true, false); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.DueAt != nil && update.ClearDueAt {
		return FundCallbackUpdate{}, NewCRMValidationError("due_at", "cannot be set and cleared together")
	}
	if update.DueAt, err = normalizeCRMOptionalTime("due_at", update.DueAt); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.Evidence, err = crmNormalizeOptionalString("evidence", update.Evidence, 4000, false, true); err != nil {
		return FundCallbackUpdate{}, err
	}
	if update.Status != nil {
		if !IsValidFundCallbackStatus(*update.Status) {
			return FundCallbackUpdate{}, NewCRMValidationError("status", "invalid fund callback status")
		}
		if *update.Status == FundCallbackStatusCommitted || *update.Status == FundCallbackStatusCompleted {
			return FundCallbackUpdate{}, NewCRMValidationError("status", "requires protected human confirmation")
		}
	}
	return update, nil
}

func crmNormalizeFundCallbackFilter(filter crmFundCallbackFilter) (crmFundCallbackFilter, error) {
	var err error
	if filter.Query, err = NormalizeCRMSearch(filter.Query); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.CandidateID, err = normalizeCRMString("candidate_id", filter.CandidateID, 80, false, false); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.JobID, err = normalizeCRMString("job_id", filter.JobID, 80, false, false); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.LeadID, err = normalizeCRMString("lead_id", filter.LeadID, 80, false, false); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.OnboardingCaseID, err = normalizeCRMString("onboarding_case_id", filter.OnboardingCaseID, 80, false, false); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.Status != "" && !IsValidFundCallbackStatus(filter.Status) {
		return crmFundCallbackFilter{}, NewCRMValidationError("status", "invalid fund callback status")
	}
	filter.Currency = strings.ToUpper(strings.TrimSpace(filter.Currency))
	if filter.Currency != "" && !isValidCRMCurrency(filter.Currency) {
		return crmFundCallbackFilter{}, NewCRMValidationError("currency", "must be a three-letter currency code")
	}
	if filter.DueBefore, err = normalizeCRMOptionalTime("due_before", filter.DueBefore); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.DueAfter, err = normalizeCRMOptionalTime("due_after", filter.DueAfter); err != nil {
		return crmFundCallbackFilter{}, err
	}
	if filter.DueBefore != nil && filter.DueAfter != nil && filter.DueBefore.Before(*filter.DueAfter) {
		return crmFundCallbackFilter{}, NewCRMValidationError("due_before", "must not precede due_after")
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmFundCallbackFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmFundCallbackFilter{}, NewCRMValidationError("offset", "must be zero or greater")
	}
	return filter, nil
}

func crmNormalizeAuditFilter(filter crmAuditFilter) (crmAuditFilter, error) {
	var err error
	if filter.Actor, err = normalizeCRMString("actor", filter.Actor, 80, false, false); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.Operation, err = normalizeCRMString("operation", filter.Operation, 120, false, false); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.EntityType, err = normalizeCRMString("entity_type", filter.EntityType, 80, false, false); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.EntityType != "" && !isValidCRMAuditEntityType(filter.EntityType) {
		return crmAuditFilter{}, NewCRMValidationError("entity_type", "is invalid")
	}
	if filter.EntityID, err = normalizeCRMString("entity_id", filter.EntityID, 80, false, false); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.SourceEventID, err = normalizeCRMString("source_event_id", filter.SourceEventID, 80, false, false); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.CreatedAfter, err = normalizeCRMOptionalTime("created_after", filter.CreatedAfter); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.CreatedBefore, err = normalizeCRMOptionalTime("created_before", filter.CreatedBefore); err != nil {
		return crmAuditFilter{}, err
	}
	if filter.CreatedAfter != nil && filter.CreatedBefore != nil && filter.CreatedBefore.Before(*filter.CreatedAfter) {
		return crmAuditFilter{}, NewCRMValidationError("created_before", "must not precede created_after")
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmAuditFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmAuditFilter{}, NewCRMValidationError("offset", "must be zero or greater")
	}
	return filter, nil
}

func crmNormalizeOptionalString(
	field string,
	value *string,
	maxRunes int,
	required bool,
	multiline bool,
) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeCRMString(field, *value, maxRunes, required, multiline)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func crmFundCallbackUpdateHasFields(update FundCallbackUpdate) bool {
	return update.CandidateID != nil ||
		update.JobID != nil ||
		update.LeadID != nil ||
		update.OnboardingCaseID != nil ||
		update.AmountMinor != nil ||
		update.Currency != nil ||
		update.Condition != nil ||
		update.PromisedBy != nil ||
		update.DueAt != nil ||
		update.ClearDueAt ||
		update.Evidence != nil ||
		update.Status != nil
}

func crmFundUpdateChangesCommitment(update FundCallbackUpdate) bool {
	return update.CandidateID != nil ||
		update.JobID != nil ||
		update.LeadID != nil ||
		update.OnboardingCaseID != nil ||
		update.AmountMinor != nil ||
		update.Currency != nil ||
		update.Condition != nil ||
		update.PromisedBy != nil
}

func crmApplyFundCallbackUpdate(callback *FundCallback, update FundCallbackUpdate) {
	if update.CandidateID != nil {
		callback.CandidateID = *update.CandidateID
	}
	if update.JobID != nil {
		callback.JobID = *update.JobID
	}
	if update.LeadID != nil {
		callback.LeadID = *update.LeadID
	}
	if update.OnboardingCaseID != nil {
		callback.OnboardingCaseID = *update.OnboardingCaseID
	}
	if update.AmountMinor != nil {
		callback.AmountMinor = *update.AmountMinor
	}
	if update.Currency != nil {
		callback.Currency = *update.Currency
	}
	if update.Condition != nil {
		callback.Condition = *update.Condition
	}
	if update.PromisedBy != nil {
		callback.PromisedBy = *update.PromisedBy
	}
	if update.DueAt != nil {
		callback.DueAt = cloneCRMTimePointer(update.DueAt)
	} else if update.ClearDueAt {
		callback.DueAt = nil
	}
	if update.Evidence != nil {
		callback.Evidence = *update.Evidence
	}
}

func crmCanTransitionFundCallback(callback FundCallback, to FundCallbackStatus) bool {
	if callback.Status == to ||
		to == FundCallbackStatusCommitted ||
		to == FundCallbackStatusCompleted {
		return false
	}
	switch callback.Status {
	case FundCallbackStatusDraft:
		return to == FundCallbackStatusAwaitingCommitment ||
			to == FundCallbackStatusDisputed ||
			to == FundCallbackStatusCancelled
	case FundCallbackStatusAwaitingCommitment:
		return to == FundCallbackStatusDraft ||
			to == FundCallbackStatusOverdue ||
			to == FundCallbackStatusDisputed ||
			to == FundCallbackStatusCancelled
	case FundCallbackStatusCommitted:
		return to == FundCallbackStatusCallbackDue ||
			to == FundCallbackStatusAwaitingFinal ||
			to == FundCallbackStatusOverdue ||
			to == FundCallbackStatusDisputed ||
			to == FundCallbackStatusCancelled
	case FundCallbackStatusCallbackDue:
		return to == FundCallbackStatusAwaitingFinal ||
			to == FundCallbackStatusOverdue ||
			to == FundCallbackStatusDisputed ||
			to == FundCallbackStatusCancelled
	case FundCallbackStatusAwaitingFinal:
		return to == FundCallbackStatusCallbackDue ||
			to == FundCallbackStatusOverdue ||
			to == FundCallbackStatusDisputed ||
			to == FundCallbackStatusCancelled
	case FundCallbackStatusOverdue:
		if callback.CommitmentAt == nil {
			return to == FundCallbackStatusAwaitingCommitment ||
				to == FundCallbackStatusDisputed ||
				to == FundCallbackStatusCancelled
		}
		return to == FundCallbackStatusCallbackDue ||
			to == FundCallbackStatusAwaitingFinal ||
			to == FundCallbackStatusDisputed ||
			to == FundCallbackStatusCancelled
	case FundCallbackStatusDisputed:
		if callback.CommitmentAt == nil {
			return to == FundCallbackStatusAwaitingCommitment ||
				to == FundCallbackStatusCancelled
		}
		return to == FundCallbackStatusCallbackDue ||
			to == FundCallbackStatusAwaitingFinal ||
			to == FundCallbackStatusCancelled
	default:
		return false
	}
}

func crmFundCallbackMatches(callback FundCallback, filter crmFundCallbackFilter) bool {
	if filter.CandidateID != "" && callback.CandidateID != filter.CandidateID {
		return false
	}
	if filter.JobID != "" && callback.JobID != filter.JobID {
		return false
	}
	if filter.LeadID != "" && callback.LeadID != filter.LeadID {
		return false
	}
	if filter.OnboardingCaseID != "" && callback.OnboardingCaseID != filter.OnboardingCaseID {
		return false
	}
	if filter.Status != "" && callback.Status != filter.Status {
		return false
	}
	if filter.Currency != "" && callback.Currency != filter.Currency {
		return false
	}
	if filter.DueBefore != nil && (callback.DueAt == nil || callback.DueAt.After(*filter.DueBefore)) {
		return false
	}
	if filter.DueAfter != nil && (callback.DueAt == nil || callback.DueAt.Before(*filter.DueAfter)) {
		return false
	}
	if filter.Query == "" {
		return true
	}
	query := strings.ToLower(filter.Query)
	for _, value := range []string{
		callback.ID, callback.CandidateID, callback.JobID, callback.LeadID,
		callback.OnboardingCaseID, callback.Currency, callback.Condition,
		callback.PromisedBy, callback.Evidence, string(callback.Status),
	} {
		if strings.Contains(strings.ToLower(value), query) {
			return true
		}
	}
	return false
}

func crmFundCallbackIndex(callbacks []FundCallback, id string) int {
	for index := range callbacks {
		if callbacks[index].ID == id {
			return index
		}
	}
	return -1
}

func crmSortCandidateArchive(export *CandidateArchiveExport) {
	sort.Slice(export.Leads, func(i, j int) bool { return export.Leads[i].ID < export.Leads[j].ID })
	sort.Slice(export.LeadStageEvents, func(i, j int) bool {
		return export.LeadStageEvents[i].ID < export.LeadStageEvents[j].ID
	})
	sort.Slice(export.OnboardingCases, func(i, j int) bool {
		return export.OnboardingCases[i].ID < export.OnboardingCases[j].ID
	})
	sort.Slice(export.ServiceRequests, func(i, j int) bool {
		return export.ServiceRequests[i].ID < export.ServiceRequests[j].ID
	})
	sort.Slice(export.FundCallbacks, func(i, j int) bool {
		return export.FundCallbacks[i].ID < export.FundCallbacks[j].ID
	})
	sort.Slice(export.Tasks, func(i, j int) bool { return export.Tasks[i].ID < export.Tasks[j].ID })
	sort.Slice(export.Notes, func(i, j int) bool { return export.Notes[i].ID < export.Notes[j].ID })
	sort.Slice(export.MessageEvents, func(i, j int) bool {
		return export.MessageEvents[i].ID < export.MessageEvents[j].ID
	})
	sort.Slice(export.AuditEvents, func(i, j int) bool {
		return export.AuditEvents[i].ID < export.AuditEvents[j].ID
	})
}

func crmStringCountMap(values ...string) map[string]int {
	result := make(map[string]int, len(values))
	for _, value := range values {
		result[value] = 0
	}
	return result
}

func crmReportTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func crmReportOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return crmReportTime(*value)
}

func crmSafeCSVCell(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}

func crmFundAmountLabel(callback FundCallback) string {
	return fmt.Sprintf("%s %d", callback.Currency, callback.AmountMinor)
}
