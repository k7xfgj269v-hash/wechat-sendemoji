package main

import (
	"errors"
	"sort"
	"strings"
	"time"
)

type OperationBatchCreate struct {
	Kind         OperationBatchKind `json:"kind"`
	TargetStatus string             `json:"target_status"`
}

type OperationBatchUpdate struct {
	ExpectedVersion uint64  `json:"expected_version"`
	TargetStatus    *string `json:"target_status,omitempty"`
}

type OperationBatchMembersUpdate struct {
	ExpectedVersion   uint64                 `json:"expected_version"`
	Members           []OperationBatchMember `json:"members,omitempty"`
	OnboardingCaseIDs []string               `json:"onboarding_case_ids,omitempty"`
	Replace           bool                   `json:"replace,omitempty"`
}

type OperationBatchExecute struct {
	ExpectedVersion uint64 `json:"expected_version"`
	Confirmed       bool   `json:"confirmed"`
	Actor           string `json:"-"`
}

type OperationBatchItemState struct {
	Version          uint64           `json:"version"`
	OnboardingStatus OnboardingStatus `json:"onboarding_status"`
	FleetStatus      FleetStatus      `json:"fleet_status"`
}

type OperationBatchPreviewItem struct {
	OnboardingCaseID string                  `json:"onboarding_case_id"`
	ExpectedVersion  uint64                  `json:"expected_version"`
	CurrentVersion   uint64                  `json:"current_version,omitempty"`
	Eligible         bool                    `json:"eligible"`
	Protected        bool                    `json:"protected"`
	Before           OperationBatchItemState `json:"before"`
	After            OperationBatchItemState `json:"after"`
	ErrorCode        string                  `json:"error_code,omitempty"`
	ErrorMessage     string                  `json:"error_message,omitempty"`
}

type OperationBatchPreview struct {
	BatchID         string                      `json:"batch_id"`
	BatchVersion    uint64                      `json:"batch_version"`
	Kind            OperationBatchKind          `json:"kind"`
	TargetStatus    string                      `json:"target_status"`
	Protected       bool                        `json:"protected"`
	RequiresConfirm bool                        `json:"requires_confirmation"`
	Items           []OperationBatchPreviewItem `json:"items"`
}

func crmOperationBatchCreate(
	store *CRMStore,
	create OperationBatchCreate,
	actor string,
) (OperationBatch, error) {
	if store == nil {
		return OperationBatch{}, NewCRMValidationError("store", "is required")
	}
	normalized, err := crmNormalizeOperationBatchCreate(create)
	if err != nil {
		return OperationBatch{}, err
	}
	actor, err = crmNormalizeActor(actor)
	if err != nil {
		return OperationBatch{}, err
	}

	var created OperationBatch
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "operation_batch.create",
		EntityType: "operation_batch",
	}, func(state *CRMState, allocator *IDAllocator) error {
		id, allocateErr := allocator.Next(IDKindOperationBatch)
		if allocateErr != nil {
			return allocateErr
		}
		now := time.Now().UTC()
		created = OperationBatch{
			ID:           id,
			Kind:         normalized.Kind,
			TargetStatus: normalized.TargetStatus,
			Status:       OperationBatchStatusDraft,
			Version:      1,
			Members:      []OperationBatchMember{},
			Results:      []OperationBatchResult{},
			CreatedBy:    actor,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		state.OperationBatches = append(state.OperationBatches, created)
		return nil
	})
	return created, err
}

func crmOperationBatchList(store *CRMStore) ([]OperationBatch, error) {
	if store == nil {
		return nil, NewCRMValidationError("store", "is required")
	}
	batches := append([]OperationBatch{}, store.Snapshot().OperationBatches...)
	sort.Slice(batches, func(i, j int) bool {
		if batches[i].UpdatedAt.Equal(batches[j].UpdatedAt) {
			return batches[i].ID < batches[j].ID
		}
		return batches[i].UpdatedAt.After(batches[j].UpdatedAt)
	})
	return batches, nil
}

func crmOperationBatchQueueMetrics(state CRMState, now time.Time) (int, time.Duration) {
	depth := 0
	var oldest time.Time
	for _, batch := range state.OperationBatches {
		if batch.Status != OperationBatchStatusDraft {
			continue
		}
		depth++
		if oldest.IsZero() || batch.CreatedAt.Before(oldest) {
			oldest = batch.CreatedAt
		}
	}
	return operationalQueueAge(depth, oldest, now)
}

func crmOperationBatchGet(store *CRMStore, id string) (OperationBatch, error) {
	if store == nil {
		return OperationBatch{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return OperationBatch{}, err
	}
	state := store.Snapshot()
	index := crmOperationBatchIndex(state.OperationBatches, id)
	if index < 0 {
		return OperationBatch{}, &crmPipelineNotFoundError{resource: "operation batch"}
	}
	return state.OperationBatches[index], nil
}

func crmOperationBatchUpdate(
	store *CRMStore,
	id string,
	update OperationBatchUpdate,
	actor string,
) (OperationBatch, error) {
	if store == nil {
		return OperationBatch{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return OperationBatch{}, err
	}
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return OperationBatch{}, err
	}
	if update.TargetStatus == nil {
		return OperationBatch{}, NewCRMValidationError("operation_batch", "at least one update field is required")
	}
	targetStatus := strings.TrimSpace(*update.TargetStatus)
	actor, err = crmNormalizeActor(actor)
	if err != nil {
		return OperationBatch{}, err
	}

	var updated OperationBatch
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "operation_batch.update",
		EntityType: "operation_batch",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOperationBatchIndex(state.OperationBatches, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "operation batch"}
		}
		current := state.OperationBatches[index]
		if current.Version != update.ExpectedVersion {
			return NewCRMConflictError("operation batch version conflict")
		}
		if current.Status != OperationBatchStatusDraft {
			return NewCRMConflictError("completed operation batches are immutable")
		}
		if err := crmValidateOperationBatchTarget(current.Kind, targetStatus); err != nil {
			return err
		}
		current.TargetStatus = targetStatus
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.OperationBatches[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmOperationBatchUpdateMembers(
	store *CRMStore,
	id string,
	update OperationBatchMembersUpdate,
	actor string,
) (OperationBatch, error) {
	if store == nil {
		return OperationBatch{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return OperationBatch{}, err
	}
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return OperationBatch{}, err
	}
	if len(update.Members) == 0 && len(update.OnboardingCaseIDs) == 0 {
		return OperationBatch{}, NewCRMValidationError("members", "at least one member is required")
	}
	actor, err = crmNormalizeActor(actor)
	if err != nil {
		return OperationBatch{}, err
	}

	var updated OperationBatch
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "operation_batch.members",
		EntityType: "operation_batch",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOperationBatchIndex(state.OperationBatches, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "operation batch"}
		}
		current := state.OperationBatches[index]
		if current.Version != update.ExpectedVersion {
			return NewCRMConflictError("operation batch version conflict")
		}
		if current.Status != OperationBatchStatusDraft {
			return NewCRMConflictError("completed operation batches are immutable")
		}
		members := append([]OperationBatchMember{}, current.Members...)
		if update.Replace {
			members = []OperationBatchMember{}
		}
		for _, member := range update.Members {
			normalizedMember, normalizeErr := crmNormalizeOperationBatchMember(state, member)
			if normalizeErr != nil {
				return normalizeErr
			}
			members = crmUpsertOperationBatchMember(members, normalizedMember)
		}
		for _, onboardingID := range update.OnboardingCaseIDs {
			onboardingID, normalizeErr := normalizeCRMString(
				"onboarding_case_id",
				onboardingID,
				80,
				true,
				false,
			)
			if normalizeErr != nil {
				return normalizeErr
			}
			onboardingIndex := crmOnboardingIndex(state.OnboardingCases, onboardingID)
			if onboardingIndex < 0 {
				return &crmPipelineNotFoundError{resource: "onboarding case"}
			}
			members = crmUpsertOperationBatchMember(members, OperationBatchMember{
				OnboardingCaseID: onboardingID,
				ExpectedVersion:  state.OnboardingCases[onboardingIndex].Version,
			})
		}
		sort.Slice(members, func(i, j int) bool {
			return members[i].OnboardingCaseID < members[j].OnboardingCaseID
		})
		current.Members = members
		current.Version++
		current.UpdatedAt = crmPipelineMutationTime(current.UpdatedAt)
		state.OperationBatches[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmOperationBatchPreview(store *CRMStore, id string) (OperationBatchPreview, error) {
	batch, err := crmOperationBatchGet(store, id)
	if err != nil {
		return OperationBatchPreview{}, err
	}
	state := store.Snapshot()
	preview := OperationBatchPreview{
		BatchID:         batch.ID,
		BatchVersion:    batch.Version,
		Kind:            batch.Kind,
		TargetStatus:    batch.TargetStatus,
		Protected:       crmOperationBatchProtected(batch.Kind, batch.TargetStatus),
		RequiresConfirm: crmOperationBatchProtected(batch.Kind, batch.TargetStatus),
		Items:           make([]OperationBatchPreviewItem, 0, len(batch.Members)),
	}
	for _, member := range batch.Members {
		preview.Items = append(preview.Items, crmOperationBatchPreviewMember(&state, batch, member))
	}
	return preview, nil
}

func crmOperationBatchExecute(
	store *CRMStore,
	id string,
	execute OperationBatchExecute,
) (OperationBatch, error) {
	if store == nil {
		return OperationBatch{}, NewCRMValidationError("store", "is required")
	}
	id, err := normalizeCRMString("id", id, 80, true, false)
	if err != nil {
		return OperationBatch{}, err
	}
	if err := validateCRMVersion("expected_version", execute.ExpectedVersion); err != nil {
		return OperationBatch{}, err
	}
	execute.Actor, err = crmNormalizeActor(execute.Actor)
	if err != nil {
		return OperationBatch{}, err
	}

	var completed OperationBatch
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      execute.Actor,
		Operation:  "operation_batch.execute",
		EntityType: "operation_batch",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOperationBatchIndex(state.OperationBatches, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "operation batch"}
		}
		batch := state.OperationBatches[index]
		if batch.Version != execute.ExpectedVersion {
			return NewCRMConflictError("operation batch version conflict")
		}
		if batch.Status != OperationBatchStatusDraft {
			return NewCRMConflictError("operation batch has already executed")
		}
		if len(batch.Members) == 0 {
			return NewCRMValidationError("members", "operation batch has no members")
		}
		if crmOperationBatchProtected(batch.Kind, batch.TargetStatus) && !execute.Confirmed {
			return NewCRMValidationError("confirmed", "explicit confirmation is required")
		}

		now := crmPipelineMutationTime(batch.UpdatedAt)
		results := make([]OperationBatchResult, 0, len(batch.Members))
		for _, member := range batch.Members {
			item := crmOperationBatchPreviewMember(state, batch, member)
			result := OperationBatchResult{
				OnboardingCaseID: member.OnboardingCaseID,
				FromStatus:       crmOperationBatchResultFromStatus(batch.Kind, item.Before),
				ToStatus:         batch.TargetStatus,
				CreatedAt:        now,
			}
			if !item.Eligible {
				result.Outcome = OperationBatchOutcomeFailed
				result.ErrorCode = item.ErrorCode
				result.ErrorMessage = item.ErrorMessage
				results = append(results, result)
				continue
			}
			onboardingIndex := crmOnboardingIndex(state.OnboardingCases, member.OnboardingCaseID)
			current := state.OnboardingCases[onboardingIndex]
			crmApplyOperationBatchTransition(&current, batch, now)
			state.OnboardingCases[onboardingIndex] = current
			result.Outcome = OperationBatchOutcomeSucceeded
			results = append(results, result)
		}
		batch.Results = results
		batch.Status = OperationBatchStatusCompleted
		batch.Version++
		batch.UpdatedAt = now
		state.OperationBatches[index] = batch
		completed = batch
		return nil
	})
	return completed, err
}

func crmNormalizeOperationBatchCreate(create OperationBatchCreate) (OperationBatchCreate, error) {
	if !IsValidOperationBatchKind(create.Kind) {
		return OperationBatchCreate{}, NewCRMValidationError("kind", "invalid operation batch kind")
	}
	create.TargetStatus = strings.TrimSpace(create.TargetStatus)
	if err := crmValidateOperationBatchTarget(create.Kind, create.TargetStatus); err != nil {
		return OperationBatchCreate{}, err
	}
	return create, nil
}

func crmValidateOperationBatchTarget(kind OperationBatchKind, target string) error {
	switch kind {
	case OperationBatchKindOnboardingTransition:
		if !IsValidOnboardingStatus(OnboardingStatus(target)) {
			return NewCRMValidationError("target_status", "invalid onboarding status")
		}
	case OperationBatchKindFleetTransition:
		if !IsValidFleetStatus(FleetStatus(target)) || FleetStatus(target) == FleetStatusNotRequired {
			return NewCRMValidationError("target_status", "invalid fleet status")
		}
	default:
		return NewCRMValidationError("kind", "invalid operation batch kind")
	}
	return nil
}

func crmNormalizeOperationBatchMember(
	state *CRMState,
	member OperationBatchMember,
) (OperationBatchMember, error) {
	var err error
	member.OnboardingCaseID, err = normalizeCRMString(
		"onboarding_case_id",
		member.OnboardingCaseID,
		80,
		true,
		false,
	)
	if err != nil {
		return OperationBatchMember{}, err
	}
	index := crmOnboardingIndex(state.OnboardingCases, member.OnboardingCaseID)
	if index < 0 {
		return OperationBatchMember{}, &crmPipelineNotFoundError{resource: "onboarding case"}
	}
	if member.ExpectedVersion == 0 {
		member.ExpectedVersion = state.OnboardingCases[index].Version
	}
	if err := validateCRMVersion("expected_version", member.ExpectedVersion); err != nil {
		return OperationBatchMember{}, err
	}
	return member, nil
}

func crmUpsertOperationBatchMember(
	members []OperationBatchMember,
	member OperationBatchMember,
) []OperationBatchMember {
	for index := range members {
		if members[index].OnboardingCaseID == member.OnboardingCaseID {
			members[index] = member
			return members
		}
	}
	return append(members, member)
}

func crmOperationBatchPreviewMember(
	state *CRMState,
	batch OperationBatch,
	member OperationBatchMember,
) OperationBatchPreviewItem {
	item := OperationBatchPreviewItem{
		OnboardingCaseID: member.OnboardingCaseID,
		ExpectedVersion:  member.ExpectedVersion,
		Protected:        crmOperationBatchProtected(batch.Kind, batch.TargetStatus),
	}
	index := crmOnboardingIndex(state.OnboardingCases, member.OnboardingCaseID)
	if index < 0 {
		item.ErrorCode = "not_found"
		item.ErrorMessage = "onboarding case not found"
		return item
	}
	current := state.OnboardingCases[index]
	item.CurrentVersion = current.Version
	item.Before = crmOperationBatchState(current)
	item.After = item.Before
	item.After.Version++
	switch batch.Kind {
	case OperationBatchKindOnboardingTransition:
		item.After.OnboardingStatus = OnboardingStatus(batch.TargetStatus)
	case OperationBatchKindFleetTransition:
		item.After.FleetStatus = FleetStatus(batch.TargetStatus)
	}
	if current.Version != member.ExpectedVersion {
		item.ErrorCode = "version_conflict"
		item.ErrorMessage = "onboarding version conflict"
		return item
	}
	if err := crmValidateOperationBatchTransition(current, batch); err != nil {
		item.ErrorCode, item.ErrorMessage = crmOperationBatchError(err)
		return item
	}
	item.Eligible = true
	return item
}

func crmValidateOperationBatchTransition(current OnboardingCase, batch OperationBatch) error {
	switch batch.Kind {
	case OperationBatchKindOnboardingTransition:
		target := OnboardingStatus(batch.TargetStatus)
		if !crmCanTransitionOnboarding(current.Status, target) {
			return NewCRMConflictError("onboarding status transition is not allowed")
		}
		if (target == OnboardingStatusReady || target == OnboardingStatusCompleted) &&
			!crmOnboardingReady(current) {
			return NewCRMConflictError("onboarding still has missing information or incomplete required documents")
		}
		if target == OnboardingStatusTransportScheduled &&
			(!current.Fleet.Required ||
				(current.Fleet.Status != FleetStatusConfirmed &&
					current.Fleet.Status != FleetStatusDispatched &&
					current.Fleet.Status != FleetStatusPickedUp &&
					current.Fleet.Status != FleetStatusDelivered)) {
			return NewCRMConflictError("fleet must be confirmed before transport is scheduled")
		}
	case OperationBatchKindFleetTransition:
		if !current.Fleet.Required {
			return NewCRMConflictError("fleet is not required")
		}
		if !crmCanTransitionFleet(current.Fleet.Status, FleetStatus(batch.TargetStatus)) {
			return NewCRMConflictError("fleet status transition is not allowed")
		}
	default:
		return NewCRMValidationError("kind", "invalid operation batch kind")
	}
	return nil
}

func crmApplyOperationBatchTransition(
	current *OnboardingCase,
	batch OperationBatch,
	now time.Time,
) {
	switch batch.Kind {
	case OperationBatchKindOnboardingTransition:
		current.Status = OnboardingStatus(batch.TargetStatus)
		current.BlockReason = ""
	case OperationBatchKindFleetTransition:
		current.Fleet.Status = FleetStatus(batch.TargetStatus)
		current.Fleet.UpdatedAt = now
	}
	current.Version++
	current.UpdatedAt = now
}

func crmOperationBatchProtected(kind OperationBatchKind, target string) bool {
	switch kind {
	case OperationBatchKindOnboardingTransition:
		status := OnboardingStatus(target)
		return status == OnboardingStatusCompleted || status == OnboardingStatusCancelled
	case OperationBatchKindFleetTransition:
		status := FleetStatus(target)
		return status == FleetStatusConfirmed ||
			status == FleetStatusDispatched ||
			status == FleetStatusDelivered ||
			status == FleetStatusCancelled
	default:
		return false
	}
}

func crmOperationBatchState(onboarding OnboardingCase) OperationBatchItemState {
	return OperationBatchItemState{
		Version:          onboarding.Version,
		OnboardingStatus: onboarding.Status,
		FleetStatus:      onboarding.Fleet.Status,
	}
}

func crmOperationBatchResultFromStatus(
	kind OperationBatchKind,
	state OperationBatchItemState,
) string {
	if kind == OperationBatchKindFleetTransition {
		return string(state.FleetStatus)
	}
	return string(state.OnboardingStatus)
}

func crmOperationBatchError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrCRMConflict):
		return "conflict", err.Error()
	case errors.Is(err, ErrCRMValidation):
		return "validation_error", err.Error()
	default:
		return "operation_failed", "operation failed"
	}
}

func crmOperationBatchIndex(batches []OperationBatch, id string) int {
	for index := range batches {
		if batches[index].ID == id {
			return index
		}
	}
	return -1
}
