package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const (
	workbenchGovernanceFundTextPreviewRunes  = 1024
	workbenchGovernanceMutationTextJSONBytes = 8192
)

type workbenchGovernanceFundListData struct {
	FundCallbacks []workbenchGovernanceFundReadResult `json:"fund_callbacks"`
}

type workbenchGovernanceFundLedgerData struct {
	Total  int                       `json:"total"`
	Ledger []FundCallbackLedgerEntry `json:"ledger"`
}

type workbenchGovernanceFundReadResult struct {
	ID                   string             `json:"id"`
	CandidateID          string             `json:"candidate_id"`
	JobID                string             `json:"job_id"`
	LeadID               string             `json:"lead_id"`
	OnboardingCaseID     string             `json:"onboarding_case_id"`
	AmountMinor          int64              `json:"amount_minor"`
	Currency             string             `json:"currency"`
	Condition            string             `json:"condition"`
	ConditionLength      int                `json:"condition_length"`
	ConditionTruncated   bool               `json:"condition_truncated"`
	PromisedBy           string             `json:"promised_by"`
	PromisedByLength     int                `json:"promised_by_length"`
	PromisedByTruncated  bool               `json:"promised_by_truncated"`
	DueAt                *time.Time         `json:"due_at"`
	Evidence             string             `json:"evidence"`
	EvidenceLength       int                `json:"evidence_length"`
	EvidenceTruncated    bool               `json:"evidence_truncated"`
	Status               FundCallbackStatus `json:"status"`
	CommitmentBy         string             `json:"commitment_by"`
	CommitmentAt         *time.Time         `json:"commitment_at"`
	FinalResult          string             `json:"final_result"`
	FinalResultLength    int                `json:"final_result_length"`
	FinalResultTruncated bool               `json:"final_result_truncated"`
	FinalConfirmedBy     string             `json:"final_confirmed_by"`
	FinalConfirmedAt     *time.Time         `json:"final_confirmed_at"`
	Version              uint64             `json:"version"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
}

type workbenchGovernanceJobTerminalResult struct {
	ID        string    `json:"id"`
	Status    JobStatus `json:"status"`
	Version   uint64    `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type workbenchGovernanceCandidateTerminalResult struct {
	ID               string          `json:"id"`
	FullName         string          `json:"full_name"`
	WeChatID         string          `json:"wechat_id,omitempty"`
	Phone            string          `json:"phone,omitempty"`
	Email            string          `json:"email,omitempty"`
	Location         string          `json:"location,omitempty"`
	Source           string          `json:"source,omitempty"`
	SourceEventID    string          `json:"source_event_id,omitempty"`
	Skills           []string        `json:"skills"`
	Owner            string          `json:"owner,omitempty"`
	Status           CandidateStatus `json:"status"`
	Summary          string          `json:"summary,omitempty"`
	SummaryLength    int             `json:"summary_length"`
	SummaryTruncated bool            `json:"summary_truncated"`
	Version          uint64          `json:"version"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type workbenchGovernanceJobCompensationResult struct {
	ID                             string    `json:"id"`
	CompensationSummary            string    `json:"compensation_summary"`
	CompensationSummaryLength      int       `json:"compensation_summary_length"`
	CompensationSummaryTruncated   bool      `json:"compensation_summary_truncated"`
	Accommodation                  string    `json:"accommodation"`
	AccommodationLength            int       `json:"accommodation_length"`
	AccommodationTruncated         bool      `json:"accommodation_truncated"`
	TransportRequirements          string    `json:"transport_requirements"`
	TransportRequirementsLength    int       `json:"transport_requirements_length"`
	TransportRequirementsTruncated bool      `json:"transport_requirements_truncated"`
	Version                        uint64    `json:"version"`
	UpdatedAt                      time.Time `json:"updated_at"`
}

type workbenchGovernanceRevisionActivationResult struct {
	ID                     string    `json:"id"`
	ActiveDemandRevisionID string    `json:"active_demand_revision_id"`
	DemandRevisionNumber   uint64    `json:"demand_revision_number"`
	Version                uint64    `json:"version"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type workbenchGovernanceFundCreateResult struct {
	ID          string             `json:"id"`
	CandidateID string             `json:"candidate_id"`
	AmountMinor int64              `json:"amount_minor"`
	Currency    string             `json:"currency"`
	Status      FundCallbackStatus `json:"status"`
	Version     uint64             `json:"version"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type workbenchGovernanceFundUpdateResult struct {
	ID        string             `json:"id"`
	Status    FundCallbackStatus `json:"status"`
	Version   uint64             `json:"version"`
	UpdatedAt time.Time          `json:"updated_at"`
}

type workbenchGovernanceFundCommitmentResult struct {
	ID           string             `json:"id"`
	Status       FundCallbackStatus `json:"status"`
	CommitmentBy string             `json:"commitment_by"`
	CommitmentAt *time.Time         `json:"commitment_at"`
	Version      uint64             `json:"version"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

type workbenchGovernanceFundFinalResult struct {
	ID               string             `json:"id"`
	Status           FundCallbackStatus `json:"status"`
	FinalConfirmedBy string             `json:"final_confirmed_by"`
	FinalConfirmedAt *time.Time         `json:"final_confirmed_at"`
	Version          uint64             `json:"version"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

func init() {
	bindWorkbenchOperationHandlers("m3-governance-terminal",
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsList, Handler: workbenchGovernanceFundsList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsGet, Handler: workbenchGovernanceFundsGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsLedger, Handler: workbenchGovernanceFundsLedger},
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesTerminal, Handler: workbenchGovernanceCandidatesTerminal},
		WorkbenchOperationBinding{Operation: WorkbenchOperationCandidatesAnonymize, Handler: workbenchGovernanceCandidatesAnonymize},
		WorkbenchOperationBinding{Operation: WorkbenchOperationJobsTerminal, Handler: workbenchGovernanceJobsTerminal},
		WorkbenchOperationBinding{Operation: WorkbenchOperationJobsUpdateCompensation, Handler: workbenchGovernanceJobsUpdateCompensation},
		WorkbenchOperationBinding{Operation: WorkbenchOperationRevisionsActivate, Handler: workbenchGovernanceRevisionsActivate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsCreate, Handler: workbenchGovernanceFundsCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsUpdate, Handler: workbenchGovernanceFundsUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsConfirmCommitment, Handler: workbenchGovernanceFundsConfirmCommitment},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFundsConfirmFinal, Handler: workbenchGovernanceFundsConfirmFinal},
	)
}

func workbenchGovernanceFundsList(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFundsList
	if unavailable := workbenchGovernanceRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchFundListArguments)
	total, callbacks, err := crmFundCallbackList(dispatcher.crm, crmFundCallbackFilter{
		Query:            arguments.Query,
		CandidateID:      arguments.CandidateID,
		JobID:            arguments.JobID,
		LeadID:           arguments.LeadID,
		OnboardingCaseID: arguments.OnboardingCaseID,
		Status:           arguments.Status,
		Currency:         arguments.Currency,
		Limit:            arguments.Limit,
		Offset:           arguments.Offset,
	})
	if err != nil {
		return workbenchGovernanceFailure(operation, err)
	}
	projected := make([]workbenchGovernanceFundReadResult, len(callbacks))
	for index := range callbacks {
		projected[index] = workbenchGovernanceFundReadProjection(callbacks[index])
	}
	return workbenchGovernanceBoundedPage(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		projected,
		func(page []workbenchGovernanceFundReadResult) any {
			return workbenchGovernanceFundListData{FundCallbacks: page}
		},
	)
}

func workbenchGovernanceFundsGet(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchGovernanceCRMResult(dispatcher, WorkbenchOperationFundsGet, func() (any, error) {
		callback, err := crmFundCallbackGet(dispatcher.crm, raw.(*WorkbenchIDArguments).ID)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceFundReadProjection(callback), nil
	})
}

func workbenchGovernanceFundsLedger(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFundsLedger
	if unavailable := workbenchGovernanceRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchFundLedgerArguments)
	entries, err := crmFundCallbackLedger(dispatcher.crm, arguments.ID)
	if err != nil {
		return workbenchGovernanceFailure(operation, err)
	}
	total := len(entries)
	entries = crmPipelinePage(entries, arguments.Offset, arguments.Limit)
	return workbenchGovernanceBoundedPage(
		operation,
		arguments.Limit,
		arguments.Offset,
		total,
		entries,
		func(page []FundCallbackLedgerEntry) any {
			return workbenchGovernanceFundLedgerData{Total: total, Ledger: page}
		},
	)
}

func workbenchGovernanceCandidatesTerminal(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationCandidatesTerminal
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchCandidateTerminalArguments)
		updated, err := workbenchGovernanceTransitionCandidate(
			dispatcher.crm,
			arguments.CandidateID,
			arguments.ExpectedVersion,
			arguments.Status,
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceCandidateTerminalProjection(updated), nil
	})
}

func workbenchGovernanceCandidatesAnonymize(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationCandidatesAnonymize
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchVersionedIDArguments)
		detail, err := crmCandidateGetDetail(dispatcher.crm, arguments.ID)
		if err != nil {
			return nil, err
		}
		if detail.Candidate.Version != arguments.ExpectedVersion {
			return nil, NewCRMConflictError("candidate version conflict")
		}
		if workbenchGovernanceCandidateAnonymized(detail.Candidate) {
			return nil, NewCRMConflictError("candidate is already anonymized")
		}
		return crmCandidateAnonymize(dispatcher.crm, arguments.ID, CandidateAnonymizeRequest{
			ExpectedVersion: arguments.ExpectedVersion,
			Confirmed:       true,
			OperatorID:      strings.TrimSpace(execution.OperatorID),
		})
	})
}

func workbenchGovernanceJobsTerminal(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationJobsTerminal
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchJobTerminalArguments)
		updated, err := workbenchGovernanceTransitionJob(
			dispatcher.crm,
			arguments.JobID,
			arguments.ExpectedVersion,
			arguments.Status,
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceJobTerminalResult{
			ID:        updated.ID,
			Status:    updated.Status,
			Version:   updated.Version,
			UpdatedAt: updated.UpdatedAt,
		}, nil
	})
}

func workbenchGovernanceJobsUpdateCompensation(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationJobsUpdateCompensation
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchJobCompensationArguments)
		updated, err := workbenchGovernanceUpdateJobCompensation(
			dispatcher.crm,
			arguments.JobID,
			arguments.DomainUpdate(),
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceJobCompensationProjection(updated), nil
	})
}

func workbenchGovernanceRevisionsActivate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationRevisionsActivate
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchRevisionActivateArguments)
		detail, err := crmPipelineGetJob(dispatcher.crm, arguments.JobID)
		if err != nil {
			return nil, err
		}
		if detail.Job.Version != arguments.ExpectedVersion {
			return nil, NewCRMConflictError("job version conflict")
		}
		if detail.Job.ActiveDemandRevisionID == arguments.RevisionID {
			return nil, NewCRMConflictError("demand revision is already active")
		}
		updated, err := crmPipelineActivateDemandRevision(
			dispatcher.crm,
			arguments.JobID,
			arguments.RevisionID,
			crmPipelineDemandRevisionActivation{ExpectedVersion: arguments.ExpectedVersion},
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceRevisionActivationResult{
			ID:                     updated.ID,
			ActiveDemandRevisionID: updated.ActiveDemandRevisionID,
			DemandRevisionNumber:   updated.DemandRevisionNumber,
			Version:                updated.Version,
			UpdatedAt:              updated.UpdatedAt,
		}, nil
	})
}

func workbenchGovernanceFundsCreate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFundsCreate
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	if strings.TrimSpace(execution.ActionID) == "" ||
		strings.TrimSpace(execution.PermissionRequestID) == "" {
		return NewWorkbenchToolFailure(
			operation,
			WorkbenchErrorForbidden,
			"trusted approved action binding is required",
			false,
		)
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchFundCreateArguments)
		created, err := crmFundCallbackCreateApprovedAction(
			dispatcher.crm,
			execution.ActionID,
			execution.PermissionRequestID,
			FundCallbackCreate{
				CandidateID:      arguments.CandidateID,
				JobID:            arguments.JobID,
				LeadID:           arguments.LeadID,
				OnboardingCaseID: arguments.OnboardingCaseID,
				AmountMinor:      arguments.AmountMinor,
				Currency:         arguments.Currency,
				Condition:        arguments.Condition,
				PromisedBy:       arguments.PromisedBy,
				DueAt:            arguments.DueAt,
				Evidence:         arguments.Evidence,
				Actor:            strings.TrimSpace(execution.OperatorID),
			},
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceFundCreateResult{
			ID:          created.ID,
			CandidateID: created.CandidateID,
			AmountMinor: created.AmountMinor,
			Currency:    created.Currency,
			Status:      created.Status,
			Version:     created.Version,
			UpdatedAt:   created.UpdatedAt,
		}, nil
	})
}

func workbenchGovernanceFundsUpdate(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFundsUpdate
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchFundUpdateArguments)
		updated, err := crmFundCallbackUpdate(dispatcher.crm, arguments.FundID, FundCallbackUpdate{
			ExpectedVersion:  arguments.ExpectedVersion,
			CandidateID:      arguments.CandidateID,
			JobID:            arguments.JobID,
			LeadID:           arguments.LeadID,
			OnboardingCaseID: arguments.OnboardingID,
			AmountMinor:      arguments.AmountMinor,
			Currency:         arguments.Currency,
			Condition:        arguments.Condition,
			PromisedBy:       arguments.PromisedBy,
			DueAt:            arguments.DueAt,
			ClearDueAt:       arguments.ClearDueAt,
			Evidence:         arguments.Evidence,
			Status:           arguments.Status,
			Actor:            strings.TrimSpace(execution.OperatorID),
		})
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceFundUpdateResult{
			ID:        updated.ID,
			Status:    updated.Status,
			Version:   updated.Version,
			UpdatedAt: updated.UpdatedAt,
		}, nil
	})
}

func workbenchGovernanceFundsConfirmCommitment(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFundsConfirmCommitment
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchVersionedIDArguments)
		updated, err := crmFundCallbackConfirmCommitment(
			dispatcher.crm,
			arguments.ID,
			FundCommitmentConfirmation{
				ExpectedVersion: arguments.ExpectedVersion,
				Confirmed:       true,
				OperatorID:      strings.TrimSpace(execution.OperatorID),
			},
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceFundCommitmentResult{
			ID:           updated.ID,
			Status:       updated.Status,
			CommitmentBy: updated.CommitmentBy,
			CommitmentAt: cloneCRMTimePointer(updated.CommitmentAt),
			Version:      updated.Version,
			UpdatedAt:    updated.UpdatedAt,
		}, nil
	})
}

func workbenchGovernanceFundsConfirmFinal(
	_ context.Context,
	dispatcher *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFundsConfirmFinal
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchGovernanceCRMResult(dispatcher, operation, func() (any, error) {
		arguments := raw.(*WorkbenchFundFinalArguments)
		updated, err := crmFundCallbackConfirmFinal(
			dispatcher.crm,
			arguments.FundID,
			FundFinalConfirmation{
				ExpectedVersion: arguments.ExpectedVersion,
				Confirmed:       true,
				OperatorID:      strings.TrimSpace(execution.OperatorID),
				FinalResult:     arguments.FinalResult,
			},
		)
		if err != nil {
			return nil, err
		}
		return workbenchGovernanceFundFinalResult{
			ID:               updated.ID,
			Status:           updated.Status,
			FinalConfirmedBy: updated.FinalConfirmedBy,
			FinalConfirmedAt: cloneCRMTimePointer(updated.FinalConfirmedAt),
			Version:          updated.Version,
			UpdatedAt:        updated.UpdatedAt,
		}, nil
	})
}

func workbenchGovernanceTransitionCandidate(
	store *CRMStore,
	id string,
	expectedVersion uint64,
	status CandidateStatus,
	operatorID string,
) (Candidate, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Candidate{}, err
	}
	if err := validateCRMVersion("expected_version", expectedVersion); err != nil {
		return Candidate{}, err
	}
	if status != CandidateStatusPlaced && status != CandidateStatusArchived {
		return Candidate{}, NewCRMValidationError("status", "must be a terminal candidate status")
	}
	operatorID, err = normalizeCRMString("operator_id", operatorID, 80, true, false)
	if err != nil {
		return Candidate{}, err
	}

	var updated Candidate
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      operatorID,
		Operation:  "candidate.transition_terminal",
		EntityType: "candidate",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmCandidateIndexByID(state.Candidates, id)
		if index < 0 {
			return &crmCandidateNotFoundError{resource: "candidate"}
		}
		current := state.Candidates[index]
		if current.Version != expectedVersion {
			return NewCRMConflictError("candidate version conflict")
		}
		if current.Status == status {
			return NewCRMConflictError("candidate is already in the requested terminal status")
		}
		if err := ValidateCandidateStatusTransition(current.Status, status); err != nil {
			return err
		}
		current.Status = status
		current.Version++
		current.UpdatedAt = crmCandidateMutationTime(current.UpdatedAt)
		state.Candidates[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func workbenchGovernanceTransitionJob(
	store *CRMStore,
	id string,
	expectedVersion uint64,
	status JobStatus,
	operatorID string,
) (Job, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Job{}, err
	}
	if err := validateCRMVersion("expected_version", expectedVersion); err != nil {
		return Job{}, err
	}
	if status != JobStatusClosed && status != JobStatusArchived {
		return Job{}, NewCRMValidationError("status", "must be a terminal job status")
	}
	operatorID, err = normalizeCRMString("operator_id", operatorID, 80, true, false)
	if err != nil {
		return Job{}, err
	}

	var updated Job
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      operatorID,
		Operation:  "job.transition_terminal",
		EntityType: "job",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineJobIndex(state.Jobs, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "job"}
		}
		current := state.Jobs[index]
		if current.Version != expectedVersion {
			return NewCRMConflictError("job version conflict")
		}
		if current.Status == status {
			return NewCRMConflictError("job is already in the requested terminal status")
		}
		if err := ValidateJobStatusTransition(current.Status, status); err != nil {
			return err
		}
		current.Status = status
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.Jobs[index] = current
		updated = current
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return crmPipelineStoredJob(store, updated.ID)
}

func workbenchGovernanceUpdateJobCompensation(
	store *CRMStore,
	id string,
	update JobUpdate,
	operatorID string,
) (Job, error) {
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return Job{}, err
	}
	update, err = crmPipelineNormalizeJobUpdate(update)
	if err != nil {
		return Job{}, err
	}
	operatorID, err = normalizeCRMString("operator_id", operatorID, 80, true, false)
	if err != nil {
		return Job{}, err
	}

	var updated Job
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      operatorID,
		Operation:  "job.update_compensation",
		EntityType: "job",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineJobIndex(state.Jobs, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "job"}
		}
		current := state.Jobs[index]
		if current.Version != update.ExpectedVersion {
			return NewCRMConflictError("job version conflict")
		}
		changed := false
		if update.CompensationSummary != nil && current.CompensationSummary != *update.CompensationSummary {
			current.CompensationSummary = *update.CompensationSummary
			changed = true
		}
		if update.Accommodation != nil && current.Accommodation != *update.Accommodation {
			current.Accommodation = *update.Accommodation
			changed = true
		}
		if update.TransportRequirements != nil && current.TransportRequirements != *update.TransportRequirements {
			current.TransportRequirements = *update.TransportRequirements
			changed = true
		}
		if !changed {
			return NewCRMConflictError("job compensation is unchanged")
		}
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.Jobs[index] = current
		updated = current
		return nil
	})
	if err != nil {
		return Job{}, err
	}
	return crmPipelineStoredJob(store, updated.ID)
}

func workbenchGovernanceCandidateAnonymized(candidate Candidate) bool {
	return candidate.Status == CandidateStatusArchived &&
		candidate.FullName == "Anonymized "+candidate.ID &&
		candidate.WeChatID == "" &&
		candidate.Phone == "" &&
		candidate.Email == "" &&
		candidate.Location == "" &&
		candidate.Source == "anonymized" &&
		len(candidate.Skills) == 0 &&
		candidate.Owner == "" &&
		candidate.Summary == ""
}

func workbenchGovernanceRequireCRM(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
) *WorkbenchToolResult {
	if dispatcher != nil && dispatcher.crm != nil {
		return nil
	}
	result := NewWorkbenchToolFailure(operation, WorkbenchErrorUnavailable, "CRM store is unavailable", true)
	return &result
}

func workbenchGovernanceCRMResult(
	dispatcher *defaultWorkbenchToolDispatcher,
	operation WorkbenchOperation,
	call func() (any, error),
) WorkbenchToolResult {
	if unavailable := workbenchGovernanceRequireCRM(dispatcher, operation); unavailable != nil {
		return *unavailable
	}
	data, err := call()
	if err != nil {
		return workbenchGovernanceFailure(operation, err)
	}
	return NewWorkbenchToolSuccess(operation, data, nil)
}

func workbenchGovernanceFailure(
	operation WorkbenchOperation,
	err error,
) WorkbenchToolResult {
	var notFound *crmFundsNotFoundError
	if errors.As(err, &notFound) {
		return NewWorkbenchToolFailure(operation, WorkbenchErrorNotFound, err.Error(), false)
	}
	return workbenchDomainFailure(operation, err)
}

func workbenchGovernanceFundReadProjection(
	callback FundCallback,
) workbenchGovernanceFundReadResult {
	condition, conditionLength, conditionTruncated := workbenchGovernanceBoundedFundText(callback.Condition)
	promisedBy, promisedByLength, promisedByTruncated := workbenchGovernanceBoundedFundText(callback.PromisedBy)
	evidence, evidenceLength, evidenceTruncated := workbenchGovernanceBoundedFundText(callback.Evidence)
	finalResult, finalResultLength, finalResultTruncated := workbenchGovernanceBoundedFundText(
		callback.FinalResult,
	)
	return workbenchGovernanceFundReadResult{
		ID:                   callback.ID,
		CandidateID:          callback.CandidateID,
		JobID:                callback.JobID,
		LeadID:               callback.LeadID,
		OnboardingCaseID:     callback.OnboardingCaseID,
		AmountMinor:          callback.AmountMinor,
		Currency:             callback.Currency,
		Condition:            condition,
		ConditionLength:      conditionLength,
		ConditionTruncated:   conditionTruncated,
		PromisedBy:           promisedBy,
		PromisedByLength:     promisedByLength,
		PromisedByTruncated:  promisedByTruncated,
		DueAt:                cloneCRMTimePointer(callback.DueAt),
		Evidence:             evidence,
		EvidenceLength:       evidenceLength,
		EvidenceTruncated:    evidenceTruncated,
		Status:               callback.Status,
		CommitmentBy:         callback.CommitmentBy,
		CommitmentAt:         cloneCRMTimePointer(callback.CommitmentAt),
		FinalResult:          finalResult,
		FinalResultLength:    finalResultLength,
		FinalResultTruncated: finalResultTruncated,
		FinalConfirmedBy:     callback.FinalConfirmedBy,
		FinalConfirmedAt:     cloneCRMTimePointer(callback.FinalConfirmedAt),
		Version:              callback.Version,
		CreatedAt:            callback.CreatedAt,
		UpdatedAt:            callback.UpdatedAt,
	}
}

func workbenchGovernanceCandidateTerminalProjection(
	candidate Candidate,
) workbenchGovernanceCandidateTerminalResult {
	summary, summaryLength, summaryTruncated := workbenchGovernanceBoundedMutationText(
		candidate.Summary,
	)
	return workbenchGovernanceCandidateTerminalResult{
		ID:               candidate.ID,
		FullName:         candidate.FullName,
		WeChatID:         candidate.WeChatID,
		Phone:            candidate.Phone,
		Email:            candidate.Email,
		Location:         candidate.Location,
		Source:           candidate.Source,
		SourceEventID:    candidate.SourceEventID,
		Skills:           append([]string{}, candidate.Skills...),
		Owner:            candidate.Owner,
		Status:           candidate.Status,
		Summary:          summary,
		SummaryLength:    summaryLength,
		SummaryTruncated: summaryTruncated,
		Version:          candidate.Version,
		CreatedAt:        candidate.CreatedAt,
		UpdatedAt:        candidate.UpdatedAt,
	}
}

func workbenchGovernanceJobCompensationProjection(
	job Job,
) workbenchGovernanceJobCompensationResult {
	compensationSummary, compensationSummaryLength, compensationSummaryTruncated :=
		workbenchGovernanceBoundedMutationText(job.CompensationSummary)
	accommodation, accommodationLength, accommodationTruncated :=
		workbenchGovernanceBoundedMutationText(job.Accommodation)
	transportRequirements, transportRequirementsLength, transportRequirementsTruncated :=
		workbenchGovernanceBoundedMutationText(job.TransportRequirements)
	return workbenchGovernanceJobCompensationResult{
		ID:                             job.ID,
		CompensationSummary:            compensationSummary,
		CompensationSummaryLength:      compensationSummaryLength,
		CompensationSummaryTruncated:   compensationSummaryTruncated,
		Accommodation:                  accommodation,
		AccommodationLength:            accommodationLength,
		AccommodationTruncated:         accommodationTruncated,
		TransportRequirements:          transportRequirements,
		TransportRequirementsLength:    transportRequirementsLength,
		TransportRequirementsTruncated: transportRequirementsTruncated,
		Version:                        job.Version,
		UpdatedAt:                      job.UpdatedAt,
	}
}

func workbenchGovernanceBoundedMutationText(value string) (string, int, bool) {
	runes := []rune(value)
	encoded, _ := json.Marshal(value)
	if len(encoded) <= workbenchGovernanceMutationTextJSONBytes {
		return value, len(runes), false
	}

	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		candidate, _ := json.Marshal(string(runes[:middle]))
		if len(candidate) <= workbenchGovernanceMutationTextJSONBytes {
			low = middle
		} else {
			high = middle - 1
		}
	}
	return string(runes[:low]), len(runes), true
}

func workbenchGovernanceBoundedFundText(value string) (string, int, bool) {
	runes := []rune(value)
	if len(runes) <= workbenchGovernanceFundTextPreviewRunes {
		return value, len(runes), false
	}
	return string(runes[:workbenchGovernanceFundTextPreviewRunes]), len(runes), true
}

func workbenchGovernanceBoundedPage[T any](
	operation WorkbenchOperation,
	limit int,
	offset int,
	total int,
	values []T,
	data func([]T) any,
) WorkbenchToolResult {
	if len(values) == 0 {
		return NewWorkbenchToolSuccess(
			operation,
			data([]T{}),
			newWorkbenchPage(limit, offset, total, 0),
		)
	}

	bounded := make([]T, 0, len(values))
	var result WorkbenchToolResult
	for _, value := range values {
		candidate := append(bounded, value)
		candidateResult := NewWorkbenchToolSuccess(
			operation,
			data(candidate),
			newWorkbenchPage(limit, offset, total, len(candidate)),
		)
		if !candidateResult.OK {
			if len(bounded) == 0 {
				return candidateResult
			}
			break
		}
		bounded = candidate
		result = candidateResult
	}
	return result
}
