package main

import (
	"crypto/sha256"
	"fmt"
	"reflect"
)

func reconcileCRMStateV3(previous, current *CRMState, meta CRMUpdateMeta, allocator *IDAllocator) error {
	if previous == nil || current == nil {
		return NewCRMValidationError("state", "is required")
	}
	if err := validateImmutableDemandRevisions(previous.DemandRevisions, current.DemandRevisions); err != nil {
		return err
	}
	if err := validateImmutableOperationBatchResults(previous.OperationBatches, current.OperationBatches); err != nil {
		return err
	}
	if err := validateImmutableFundLedger(previous.FundCallbackLedger, current.FundCallbackLedger); err != nil {
		return err
	}
	if err := reconcileJobDemandRevisions(previous, current, meta, allocator); err != nil {
		return err
	}
	return reconcileFundCallbackLedger(previous, current, meta, allocator)
}

func validateImmutableJobProvenance(previous, current []JobProvenance) error {
	currentByID := make(map[string]JobProvenance, len(current))
	for _, provenance := range current {
		currentByID[provenance.ID] = provenance
	}
	for _, provenance := range previous {
		candidate, ok := currentByID[provenance.ID]
		if !ok {
			return NewCRMConflictError("job provenance cannot be deleted")
		}
		if !reflect.DeepEqual(candidate, provenance) {
			return NewCRMConflictError("job provenance is immutable")
		}
	}
	return nil
}

func validateImmutableDemandRevisions(previous, current []DemandRevision) error {
	currentByID := make(map[string]DemandRevision, len(current))
	for _, revision := range current {
		currentByID[revision.ID] = revision
	}
	for _, revision := range previous {
		candidate, ok := currentByID[revision.ID]
		if !ok {
			return NewCRMConflictError("demand revisions cannot be deleted")
		}
		if !reflect.DeepEqual(candidate, revision) {
			return NewCRMConflictError("demand revisions are immutable")
		}
	}
	return nil
}

func validateImmutableOperationBatchResults(previous, current []OperationBatch) error {
	currentByID := make(map[string]OperationBatch, len(current))
	for _, batch := range current {
		currentByID[batch.ID] = batch
	}
	for _, batch := range previous {
		candidate, ok := currentByID[batch.ID]
		if !ok {
			if len(batch.Results) != 0 {
				return NewCRMConflictError("operation batch results cannot be deleted")
			}
			continue
		}
		if len(candidate.Results) < len(batch.Results) ||
			!reflect.DeepEqual(candidate.Results[:len(batch.Results)], batch.Results) {
			return NewCRMConflictError("operation batch results are immutable")
		}
	}
	return nil
}

func validateImmutableFundLedger(previous, current []FundCallbackLedgerEntry) error {
	currentByID := make(map[string]FundCallbackLedgerEntry, len(current))
	for _, entry := range current {
		currentByID[entry.ID] = entry
	}
	for _, entry := range previous {
		candidate, ok := currentByID[entry.ID]
		if !ok {
			return NewCRMConflictError("fund callback ledger entries cannot be deleted")
		}
		if !reflect.DeepEqual(candidate, entry) {
			return NewCRMConflictError("fund callback ledger entries are immutable")
		}
	}
	return nil
}

func reconcileJobDemandRevisions(
	previous, current *CRMState,
	meta CRMUpdateMeta,
	allocator *IDAllocator,
) error {
	previousJobs := make(map[string]Job, len(previous.Jobs))
	for _, job := range previous.Jobs {
		previousJobs[job.ID] = job
	}
	previousRevisions := make(map[string]DemandRevision, len(previous.DemandRevisions))
	for _, revision := range previous.DemandRevisions {
		previousRevisions[revision.ID] = revision
	}
	currentRevisions := make(map[string]DemandRevision, len(current.DemandRevisions))
	maxNumberByJob := make(map[string]uint64)
	for _, revision := range current.DemandRevisions {
		currentRevisions[revision.ID] = revision
		if revision.Number > maxNumberByJob[revision.JobID] {
			maxNumberByJob[revision.JobID] = revision.Number
		}
	}

	for i := range current.Jobs {
		job := &current.Jobs[i]
		previousJob, existed := previousJobs[job.ID]
		if !existed {
			if job.ActiveDemandRevisionID == "" {
				if err := appendDemandRevision(current, job, meta, allocator, maxNumberByJob); err != nil {
					return err
				}
				continue
			}
			revision, ok := currentRevisions[job.ActiveDemandRevisionID]
			if !ok || revision.JobID != job.ID {
				return NewCRMConflictError("active demand revision must belong to the job")
			}
			if !reflect.DeepEqual(jobDemandSnapshot(*job), revision.Snapshot) {
				return NewCRMConflictError("job projection must match the active demand revision")
			}
			job.DemandRevisionNumber = revision.Number
			continue
		}

		if job.ActiveDemandRevisionID == "" {
			if previousJob.ActiveDemandRevisionID != "" {
				return NewCRMConflictError("active demand revision cannot be cleared")
			}
			if err := appendDemandRevision(current, job, meta, allocator, maxNumberByJob); err != nil {
				return err
			}
			continue
		}
		if job.ActiveDemandRevisionID != previousJob.ActiveDemandRevisionID {
			revision, ok := previousRevisions[job.ActiveDemandRevisionID]
			if !ok || revision.JobID != job.ID {
				return NewCRMConflictError("rollback demand revision must already belong to the job")
			}
			if !reflect.DeepEqual(jobDemandSnapshot(*job), revision.Snapshot) {
				return NewCRMConflictError("job projection must exactly match the rollback demand revision")
			}
			job.DemandRevisionNumber = revision.Number
			continue
		}

		revision, ok := currentRevisions[job.ActiveDemandRevisionID]
		if !ok || revision.JobID != job.ID {
			return NewCRMConflictError("active demand revision must belong to the job")
		}
		if !reflect.DeepEqual(jobDemandSnapshot(*job), revision.Snapshot) {
			if err := appendDemandRevision(current, job, meta, allocator, maxNumberByJob); err != nil {
				return err
			}
			continue
		}
		job.DemandRevisionNumber = revision.Number
	}
	return nil
}

func appendDemandRevision(
	state *CRMState,
	job *Job,
	meta CRMUpdateMeta,
	allocator *IDAllocator,
	maxNumberByJob map[string]uint64,
) error {
	if maxNumberByJob[job.ID] >= crmMaxIDCounter {
		return NewCRMConflictError("demand revision number exhausted for job " + job.ID)
	}
	id, err := allocator.Next(IDKindDemandRevision)
	if err != nil {
		return err
	}
	number := maxNumberByJob[job.ID] + 1
	revision := DemandRevision{
		ID:        id,
		JobID:     job.ID,
		Number:    number,
		Snapshot:  jobDemandSnapshot(*job),
		CreatedBy: meta.Actor,
		Reason:    meta.Operation,
		CreatedAt: job.UpdatedAt,
	}
	state.DemandRevisions = append(state.DemandRevisions, revision)
	job.ActiveDemandRevisionID = revision.ID
	job.DemandRevisionNumber = revision.Number
	maxNumberByJob[job.ID] = number
	return nil
}

func reconcileFundCallbackLedger(
	previous, current *CRMState,
	meta CRMUpdateMeta,
	allocator *IDAllocator,
) error {
	previousByID := make(map[string]FundCallback, len(previous.FundCallbacks))
	for _, callback := range previous.FundCallbacks {
		previousByID[callback.ID] = callback
	}
	currentIDs := make(map[string]struct{}, len(current.FundCallbacks))
	for _, callback := range current.FundCallbacks {
		currentIDs[callback.ID] = struct{}{}
	}
	for id := range previousByID {
		if _, ok := currentIDs[id]; !ok {
			return NewCRMConflictError("fund callbacks cannot be deleted")
		}
	}
	for _, callback := range current.FundCallbacks {
		previousCallback, existed := previousByID[callback.ID]
		if existed && reflect.DeepEqual(previousCallback, callback) {
			continue
		}
		id, err := allocator.Next(IDKindFundLedger)
		if err != nil {
			return err
		}
		evidenceHash := sha256.Sum256([]byte(callback.Evidence))
		entry := FundCallbackLedgerEntry{
			ID:             id,
			FundCallbackID: callback.ID,
			Type:           fundCallbackLedgerType(meta.Operation, previousCallback, callback, existed),
			ToStatus:       callback.Status,
			AmountMinor:    callback.AmountMinor,
			Currency:       callback.Currency,
			OperatorID:     meta.Actor,
			EvidenceSHA256: fmt.Sprintf("%x", evidenceHash[:]),
			CreatedAt:      callback.UpdatedAt,
		}
		if existed {
			entry.FromStatus = previousCallback.Status
		}
		current.FundCallbackLedger = append(current.FundCallbackLedger, entry)
	}
	return nil
}

func fundCallbackLedgerType(
	operation string,
	previous, current FundCallback,
	existed bool,
) string {
	if !existed {
		return "create"
	}
	switch operation {
	case "fund_callback.confirm_commitment":
		return "commitment"
	case "fund_callback.confirm_final":
		return "final"
	}
	if previous.Status != current.Status {
		switch current.Status {
		case FundCallbackStatusDisputed:
			return "dispute"
		case FundCallbackStatusCancelled:
			return "cancel"
		default:
			return "status_change"
		}
	}
	return "update"
}

func (a *IDAllocator) Next(kind IDKind) (string, error) {
	if a == nil || !a.active {
		return "", NewCRMValidationError("id_allocator", "is not active")
	}

	prefix, counter, err := a.counter(kind)
	if err != nil {
		return "", err
	}
	if *counter >= crmMaxIDCounter {
		return "", NewCRMConflictError("id counter exhausted for " + string(kind))
	}
	*counter++
	return fmt.Sprintf("%s_%08d", prefix, *counter), nil
}

func (a *IDAllocator) counter(kind IDKind) (string, *uint64, error) {
	switch kind {
	case IDKindCandidate:
		return "cand", &a.counters.Candidate, nil
	case IDKindJob:
		return "job", &a.counters.Job, nil
	case IDKindLead:
		return "lead", &a.counters.Lead, nil
	case IDKindTask:
		return "task", &a.counters.Task, nil
	case IDKindNote:
		return "note", &a.counters.Note, nil
	case IDKindEvent:
		return "event", &a.counters.Event, nil
	case IDKindRule:
		return "rule", &a.counters.Rule, nil
	case IDKindStageEvent:
		return "stage", &a.counters.StageEvent, nil
	case IDKindOnboarding:
		return "onboard", &a.counters.Onboarding, nil
	case IDKindServiceRequest:
		return "service", &a.counters.ServiceRequest, nil
	case IDKindFundCallback:
		return "fund", &a.counters.FundCallback, nil
	case IDKindDemandRevision:
		return "demand", &a.counters.DemandRevision, nil
	case IDKindOperationBatch:
		return "batch", &a.counters.OperationBatch, nil
	case IDKindServiceQueue:
		return "queue", &a.counters.ServiceQueue, nil
	case IDKindFundLedger:
		return "fundledger", &a.counters.FundLedger, nil
	case IDKindAudit:
		return "audit", &a.counters.Audit, nil
	case IDKindSourceGroup:
		return "source", &a.counters.SourceGroup, nil
	case IDKindJobProvenance:
		return "provenance", &a.counters.JobProvenance, nil
	default:
		return "", nil, NewCRMValidationError("id_kind", "is invalid")
	}
}
