package main

import (
	"encoding/json"
	"time"
)

func writeSnapshotAtomic(path string, state CRMState) error {
	return writeSnapshotAtomicWithDirectorySync(path, state, syncCRMDirectory, false)
}

// writeSnapshotAtomicWithDirectorySync writes state to path atomically.
// skipPreValidate suppresses the redundant strict validation inside the
// write for callers that already validated the exact state they are
// persisting (CRMStore.UpdateWithMeta); external callers (OpenCRMStore and
// migrations via writeSnapshotAtomic) keep the default full validation.
func writeSnapshotAtomicWithDirectorySync(path string, state CRMState, syncDirectory func(string) error, skipPreValidate bool) error {
	normalizeCRMStateV2Defaults(&state)
	if !skipPreValidate {
		if err := validateCRMStateStrict(&state); err != nil {
			return err
		}
	}
	data, err := json.Marshal(state)
	if err != nil {
		return WrapCRMPersistence("encode snapshot", err)
	}
	data = append(data, '\n')
	return writeSnapshotBytesAtomic(path, data, syncDirectory)
}

func newCRMState() CRMState {
	return CRMState{
		SchemaVersion: CRMSchemaVersion,
		RecruitingProject: RecruitingProjectConfig{
			OrganizationName:        CRMDefaultOrganizationName,
			Version:                 1,
			RequireAuthoritativeJob: true,
			SourceGroups:            []RecruitingSourceGroup{},
		},
		Candidates:         []Candidate{},
		Jobs:               []Job{},
		Leads:              []Lead{},
		LeadStageEvents:    []LeadStageEvent{},
		OnboardingCases:    []OnboardingCase{},
		ServiceRequests:    []ServiceRequest{},
		FundCallbacks:      []FundCallback{},
		DemandRevisions:    []DemandRevision{},
		JobProvenance:      []JobProvenance{},
		OperationBatches:   []OperationBatch{},
		ServiceQueues:      []ServiceQueue{},
		FundCallbackLedger: []FundCallbackLedgerEntry{},
		AuditEvents:        []CRMAuditEvent{},
		Tasks:              []FollowUpTask{},
		Notes:              []Note{},
		MessageEvents:      []MessageEvent{},
		ForwardingRules:    []ForwardRule{},
	}
}

func cloneCRMState(state CRMState) CRMState {
	cloned := state
	cloned.RecruitingProject.SourceGroups = append(
		[]RecruitingSourceGroup{},
		state.RecruitingProject.SourceGroups...,
	)
	cloned.Candidates = make([]Candidate, len(state.Candidates))
	for i := range state.Candidates {
		cloned.Candidates[i] = state.Candidates[i]
		cloned.Candidates[i].Skills = append([]string{}, state.Candidates[i].Skills...)
	}
	cloned.Jobs = make([]Job, len(state.Jobs))
	for i := range state.Jobs {
		cloned.Jobs[i] = state.Jobs[i]
		cloned.Jobs[i].Requirements = append([]string{}, state.Jobs[i].Requirements...)
		cloned.Jobs[i].RequiredDocuments = append([]string{}, state.Jobs[i].RequiredDocuments...)
		cloned.Jobs[i].StartAt = cloneCRMTimePointer(state.Jobs[i].StartAt)
		cloned.Jobs[i].DeadlineAt = cloneCRMTimePointer(state.Jobs[i].DeadlineAt)
	}
	cloned.Leads = append([]Lead{}, state.Leads...)
	cloned.LeadStageEvents = append([]LeadStageEvent{}, state.LeadStageEvents...)
	cloned.OnboardingCases = make([]OnboardingCase, len(state.OnboardingCases))
	for i := range state.OnboardingCases {
		cloned.OnboardingCases[i] = state.OnboardingCases[i]
		cloned.OnboardingCases[i].EntryAt = cloneCRMTimePointer(state.OnboardingCases[i].EntryAt)
		cloned.OnboardingCases[i].RequiredDocuments = append([]string{}, state.OnboardingCases[i].RequiredDocuments...)
		cloned.OnboardingCases[i].CompletedDocuments = append([]string{}, state.OnboardingCases[i].CompletedDocuments...)
		cloned.OnboardingCases[i].MissingInformation = append([]string{}, state.OnboardingCases[i].MissingInformation...)
		cloned.OnboardingCases[i].Fleet.PickupAt = cloneCRMTimePointer(state.OnboardingCases[i].Fleet.PickupAt)
	}
	cloned.ServiceRequests = make([]ServiceRequest, len(state.ServiceRequests))
	for i := range state.ServiceRequests {
		cloned.ServiceRequests[i] = state.ServiceRequests[i]
		cloned.ServiceRequests[i].EnqueuedAt = cloneCRMTimePointer(state.ServiceRequests[i].EnqueuedAt)
		cloned.ServiceRequests[i].ClaimedAt = cloneCRMTimePointer(state.ServiceRequests[i].ClaimedAt)
		cloned.ServiceRequests[i].DueAt = cloneCRMTimePointer(state.ServiceRequests[i].DueAt)
		cloned.ServiceRequests[i].ResolvedAt = cloneCRMTimePointer(state.ServiceRequests[i].ResolvedAt)
	}
	cloned.FundCallbacks = make([]FundCallback, len(state.FundCallbacks))
	for i := range state.FundCallbacks {
		cloned.FundCallbacks[i] = state.FundCallbacks[i]
		cloned.FundCallbacks[i].DueAt = cloneCRMTimePointer(state.FundCallbacks[i].DueAt)
		cloned.FundCallbacks[i].CommitmentAt = cloneCRMTimePointer(state.FundCallbacks[i].CommitmentAt)
		cloned.FundCallbacks[i].FinalConfirmedAt = cloneCRMTimePointer(state.FundCallbacks[i].FinalConfirmedAt)
	}
	cloned.DemandRevisions = make([]DemandRevision, len(state.DemandRevisions))
	for i := range state.DemandRevisions {
		cloned.DemandRevisions[i] = state.DemandRevisions[i]
		cloned.DemandRevisions[i].Snapshot = cloneJobDemandSnapshot(state.DemandRevisions[i].Snapshot)
	}
	cloned.JobProvenance = append([]JobProvenance{}, state.JobProvenance...)
	cloned.OperationBatches = make([]OperationBatch, len(state.OperationBatches))
	for i := range state.OperationBatches {
		cloned.OperationBatches[i] = state.OperationBatches[i]
		cloned.OperationBatches[i].Members = append([]OperationBatchMember{}, state.OperationBatches[i].Members...)
		cloned.OperationBatches[i].Results = append([]OperationBatchResult{}, state.OperationBatches[i].Results...)
	}
	cloned.ServiceQueues = make([]ServiceQueue, len(state.ServiceQueues))
	for i := range state.ServiceQueues {
		cloned.ServiceQueues[i] = state.ServiceQueues[i]
		cloned.ServiceQueues[i].Categories = append([]ServiceRequestCategory{}, state.ServiceQueues[i].Categories...)
	}
	cloned.FundCallbackLedger = append([]FundCallbackLedgerEntry{}, state.FundCallbackLedger...)
	cloned.FundCallbackApprovedActions = cloneFundCallbackApprovedActionBindings(
		state.FundCallbackApprovedActions,
	)
	cloned.AuditEvents = append([]CRMAuditEvent{}, state.AuditEvents...)
	cloned.Tasks = make([]FollowUpTask, len(state.Tasks))
	for i := range state.Tasks {
		cloned.Tasks[i] = state.Tasks[i]
		if state.Tasks[i].CompletedAt != nil {
			completedAt := *state.Tasks[i].CompletedAt
			cloned.Tasks[i].CompletedAt = &completedAt
		}
	}
	cloned.Notes = append([]Note{}, state.Notes...)
	cloned.MessageEvents = make([]MessageEvent, len(state.MessageEvents))
	for i := range state.MessageEvents {
		cloned.MessageEvents[i] = state.MessageEvents[i]
		cloned.MessageEvents[i].Kinds = append([]string{}, state.MessageEvents[i].Kinds...)
		if state.MessageEvents[i].RecruitingAllowedTools != nil {
			cloned.MessageEvents[i].RecruitingAllowedTools = append(
				[]AutomationToolName{},
				state.MessageEvents[i].RecruitingAllowedTools...,
			)
		}
	}
	cloned.ForwardingRules = make([]ForwardRule, len(state.ForwardingRules))
	for i := range state.ForwardingRules {
		cloned.ForwardingRules[i] = cloneForwardRule(state.ForwardingRules[i])
		if cloned.ForwardingRules[i].Keywords == nil {
			cloned.ForwardingRules[i].Keywords = []string{}
		}
	}
	return cloned
}

func cloneFundCallbackApprovedActionBindings(
	bindings []FundCallbackApprovedActionBinding,
) []FundCallbackApprovedActionBinding {
	if bindings == nil {
		return nil
	}
	cloned := make([]FundCallbackApprovedActionBinding, len(bindings))
	for i := range bindings {
		cloned[i] = bindings[i]
		if bindings[i].OriginalResult != nil {
			result := *bindings[i].OriginalResult
			cloned[i].OriginalResult = &result
		}
	}
	return cloned
}

func cloneCRMTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneJobDemandSnapshot(snapshot JobDemandSnapshot) JobDemandSnapshot {
	cloned := snapshot
	cloned.Requirements = append([]string{}, snapshot.Requirements...)
	cloned.RequiredDocuments = append([]string{}, snapshot.RequiredDocuments...)
	cloned.StartAt = cloneCRMTimePointer(snapshot.StartAt)
	cloned.DeadlineAt = cloneCRMTimePointer(snapshot.DeadlineAt)
	return cloned
}
