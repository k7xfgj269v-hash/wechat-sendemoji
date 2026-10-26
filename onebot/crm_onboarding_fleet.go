package main

import (
	"sort"
	"strings"
	"time"
)

type OnboardingCreate struct {
	LeadID             string     `json:"lead_id"`
	EntryAt            *time.Time `json:"entry_at,omitempty"`
	EntryAddress       string     `json:"entry_address,omitempty"`
	SiteContact        string     `json:"site_contact,omitempty"`
	RequiredDocuments  []string   `json:"required_documents,omitempty"`
	CompletedDocuments []string   `json:"completed_documents,omitempty"`
	MissingInformation []string   `json:"missing_information,omitempty"`
	Actor              string     `json:"-"`
}

type OnboardingUpdate struct {
	ExpectedVersion    uint64     `json:"expected_version"`
	EntryAt            *time.Time `json:"entry_at,omitempty"`
	ClearEntryAt       bool       `json:"clear_entry_at,omitempty"`
	EntryAddress       *string    `json:"entry_address,omitempty"`
	SiteContact        *string    `json:"site_contact,omitempty"`
	RequiredDocuments  *[]string  `json:"required_documents,omitempty"`
	CompletedDocuments *[]string  `json:"completed_documents,omitempty"`
	MissingInformation *[]string  `json:"missing_information,omitempty"`
	BlockReason        *string    `json:"block_reason,omitempty"`
	Actor              string     `json:"-"`
}

type OnboardingTransition struct {
	ExpectedVersion uint64           `json:"expected_version"`
	FromStatus      OnboardingStatus `json:"from_status,omitempty"`
	ToStatus        OnboardingStatus `json:"to_status"`
	BlockReason     string           `json:"block_reason,omitempty"`
	Confirmed       bool             `json:"confirmed,omitempty"`
	Actor           string           `json:"-"`
}

type FleetUpdate struct {
	ExpectedVersion    uint64     `json:"expected_version"`
	Required           *bool      `json:"required,omitempty"`
	PickupAddress      *string    `json:"pickup_address,omitempty"`
	PickupAt           *time.Time `json:"pickup_at,omitempty"`
	ClearPickupAt      bool       `json:"clear_pickup_at,omitempty"`
	Destination        *string    `json:"destination,omitempty"`
	LuggageNote        *string    `json:"luggage_note,omitempty"`
	VehicleRequirement *string    `json:"vehicle_requirement,omitempty"`
	FleetContact       *string    `json:"fleet_contact,omitempty"`
	DriverName         *string    `json:"driver_name,omitempty"`
	DriverPhone        *string    `json:"driver_phone,omitempty"`
	VehiclePlate       *string    `json:"vehicle_plate,omitempty"`
	Actor              string     `json:"-"`
}

type FleetTransition struct {
	ExpectedVersion uint64      `json:"expected_version"`
	FromStatus      FleetStatus `json:"from_status,omitempty"`
	ToStatus        FleetStatus `json:"to_status"`
	Confirmed       bool        `json:"confirmed,omitempty"`
	Actor           string      `json:"-"`
}

type crmOnboardingFilter struct {
	LeadID        string
	CandidateID   string
	JobID         string
	Status        OnboardingStatus
	FleetStatus   FleetStatus
	FleetRequired *bool
	Limit         int
	Offset        int
}

func crmOnboardingCreate(store *CRMStore, create OnboardingCreate) (OnboardingCase, error) {
	normalized, err := crmNormalizeOnboardingCreate(create)
	if err != nil {
		return OnboardingCase{}, err
	}

	var created OnboardingCase
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      normalized.Actor,
		Operation:  "onboarding.create",
		EntityType: "onboarding_case",
	}, func(state *CRMState, allocator *IDAllocator) error {
		var createErr error
		created, createErr = crmCreateOnboardingInState(state, allocator, normalized)
		return createErr
	})
	return created, err
}

func crmOnboardingEnsureForLead(store *CRMStore, leadID, actor string) (OnboardingCase, bool, error) {
	var err error
	if leadID, err = normalizeCRMString("lead_id", leadID, 80, true, false); err != nil {
		return OnboardingCase{}, false, err
	}
	if actor, err = crmNormalizeActor(actor); err != nil {
		return OnboardingCase{}, false, err
	}

	var onboarding OnboardingCase
	var created bool
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      actor,
		Operation:  "onboarding.ensure",
		EntityType: "lead",
		EntityID:   leadID,
	}, func(state *CRMState, allocator *IDAllocator) error {
		var ensureErr error
		onboarding, created, ensureErr = crmEnsureOnboardingForLeadInState(state, allocator, leadID, actor)
		return ensureErr
	})
	return onboarding, created, err
}

func crmOnboardingList(store *CRMStore, filter crmOnboardingFilter) (int, []OnboardingCase, error) {
	normalized, err := crmNormalizeOnboardingFilter(filter)
	if err != nil {
		return 0, nil, err
	}
	state := store.Snapshot()
	items := make([]OnboardingCase, 0, len(state.OnboardingCases))
	for _, onboarding := range state.OnboardingCases {
		if normalized.LeadID != "" && onboarding.LeadID != normalized.LeadID {
			continue
		}
		if normalized.CandidateID != "" && onboarding.CandidateID != normalized.CandidateID {
			continue
		}
		if normalized.JobID != "" && onboarding.JobID != normalized.JobID {
			continue
		}
		if normalized.Status != "" && onboarding.Status != normalized.Status {
			continue
		}
		if normalized.FleetStatus != "" && onboarding.Fleet.Status != normalized.FleetStatus {
			continue
		}
		if normalized.FleetRequired != nil && onboarding.Fleet.Required != *normalized.FleetRequired {
			continue
		}
		items = append(items, onboarding)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	total := len(items)
	return total, crmPipelinePage(items, normalized.Offset, normalized.Limit), nil
}

func crmOnboardingGet(store *CRMStore, id string) (OnboardingCase, error) {
	var err error
	if id, err = normalizeCRMString("id", id, 80, true, false); err != nil {
		return OnboardingCase{}, err
	}
	state := store.Snapshot()
	index := crmOnboardingIndex(state.OnboardingCases, id)
	if index < 0 {
		return OnboardingCase{}, &crmPipelineNotFoundError{resource: "onboarding case"}
	}
	return state.OnboardingCases[index], nil
}

func crmOnboardingUpdate(store *CRMStore, id string, update OnboardingUpdate) (OnboardingCase, error) {
	var err error
	if id, err = normalizeCRMString("id", id, 80, true, false); err != nil {
		return OnboardingCase{}, err
	}
	normalized, err := crmNormalizeOnboardingUpdate(update)
	if err != nil {
		return OnboardingCase{}, err
	}

	var updated OnboardingCase
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      normalized.Actor,
		Operation:  "onboarding.update",
		EntityType: "onboarding_case",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOnboardingIndex(state.OnboardingCases, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "onboarding case"}
		}
		current := state.OnboardingCases[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("onboarding version conflict")
		}
		if normalized.EntryAt != nil {
			current.EntryAt = cloneCRMTimePointer(normalized.EntryAt)
		} else if normalized.ClearEntryAt {
			current.EntryAt = nil
		}
		if normalized.EntryAddress != nil {
			current.EntryAddress = *normalized.EntryAddress
		}
		if normalized.SiteContact != nil {
			current.SiteContact = *normalized.SiteContact
		}
		if normalized.RequiredDocuments != nil {
			current.RequiredDocuments = append([]string{}, (*normalized.RequiredDocuments)...)
		}
		if normalized.CompletedDocuments != nil {
			current.CompletedDocuments = append([]string{}, (*normalized.CompletedDocuments)...)
		}
		if normalized.MissingInformation != nil {
			current.MissingInformation = append([]string{}, (*normalized.MissingInformation)...)
		}
		if normalized.BlockReason != nil {
			current.BlockReason = *normalized.BlockReason
		}
		if err := crmValidateCompletedDocuments(current.RequiredDocuments, current.CompletedDocuments); err != nil {
			return err
		}
		if current.Status == OnboardingStatusCompleted && !crmOnboardingReady(current) {
			return NewCRMConflictError("completed onboarding must retain all required documents and no missing information")
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		current.Version++
		current.UpdatedAt = now
		state.OnboardingCases[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmOnboardingTransition(store *CRMStore, id string, transition OnboardingTransition) (OnboardingCase, error) {
	var err error
	if id, err = normalizeCRMString("id", id, 80, true, false); err != nil {
		return OnboardingCase{}, err
	}
	normalized, err := crmNormalizeOnboardingTransition(transition)
	if err != nil {
		return OnboardingCase{}, err
	}
	if crmProtectedOnboardingStatus(normalized.ToStatus) && !normalized.Confirmed {
		return OnboardingCase{}, NewCRMValidationError("confirmed", "explicit confirmation is required")
	}

	var updated OnboardingCase
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      normalized.Actor,
		Operation:  "onboarding.transition",
		EntityType: "onboarding_case",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOnboardingIndex(state.OnboardingCases, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "onboarding case"}
		}
		current := state.OnboardingCases[index]
		if current.Version != normalized.ExpectedVersion ||
			(normalized.FromStatus != "" && current.Status != normalized.FromStatus) {
			return NewCRMConflictError("onboarding version or status conflict")
		}
		if !crmCanTransitionOnboarding(current.Status, normalized.ToStatus) {
			return NewCRMConflictError("onboarding status transition is not allowed")
		}
		if normalized.ToStatus == OnboardingStatusReady || normalized.ToStatus == OnboardingStatusCompleted {
			if !crmOnboardingReady(current) {
				return NewCRMConflictError("onboarding still has missing information or incomplete required documents")
			}
		}
		if normalized.ToStatus == OnboardingStatusTransportScheduled {
			if !current.Fleet.Required ||
				(current.Fleet.Status != FleetStatusConfirmed &&
					current.Fleet.Status != FleetStatusDispatched &&
					current.Fleet.Status != FleetStatusPickedUp &&
					current.Fleet.Status != FleetStatusDelivered) {
				return NewCRMConflictError("fleet must be confirmed before transport is scheduled")
			}
		}
		if normalized.ToStatus == OnboardingStatusBlocked {
			if normalized.BlockReason == "" {
				return NewCRMValidationError("block_reason", "is required when blocking onboarding")
			}
			current.BlockReason = normalized.BlockReason
		} else {
			current.BlockReason = ""
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		current.Status = normalized.ToStatus
		current.Version++
		current.UpdatedAt = now
		state.OnboardingCases[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmFleetUpdate(store *CRMStore, id string, update FleetUpdate) (OnboardingCase, error) {
	var err error
	if id, err = normalizeCRMString("id", id, 80, true, false); err != nil {
		return OnboardingCase{}, err
	}
	normalized, err := crmNormalizeFleetUpdate(update)
	if err != nil {
		return OnboardingCase{}, err
	}

	var updated OnboardingCase
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      normalized.Actor,
		Operation:  "onboarding.fleet_update",
		EntityType: "onboarding_case",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOnboardingIndex(state.OnboardingCases, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "onboarding case"}
		}
		current := state.OnboardingCases[index]
		if current.Version != normalized.ExpectedVersion {
			return NewCRMConflictError("onboarding version conflict")
		}
		fleet := current.Fleet
		if normalized.Required != nil && fleet.Required != *normalized.Required {
			fleet.Required = *normalized.Required
			if fleet.Required {
				fleet.Status = FleetStatusPending
			} else {
				fleet.Status = FleetStatusNotRequired
				fleet.DriverName = ""
				fleet.DriverPhone = ""
				fleet.VehiclePlate = ""
			}
		}
		if normalized.PickupAddress != nil {
			fleet.PickupAddress = *normalized.PickupAddress
		}
		if normalized.PickupAt != nil {
			fleet.PickupAt = cloneCRMTimePointer(normalized.PickupAt)
		} else if normalized.ClearPickupAt {
			fleet.PickupAt = nil
		}
		if normalized.Destination != nil {
			fleet.Destination = *normalized.Destination
		}
		if normalized.LuggageNote != nil {
			fleet.LuggageNote = *normalized.LuggageNote
		}
		if normalized.VehicleRequirement != nil {
			fleet.VehicleRequirement = *normalized.VehicleRequirement
		}
		if normalized.FleetContact != nil {
			fleet.FleetContact = *normalized.FleetContact
		}
		if normalized.DriverName != nil {
			fleet.DriverName = *normalized.DriverName
		}
		if normalized.DriverPhone != nil {
			fleet.DriverPhone = *normalized.DriverPhone
		}
		if normalized.VehiclePlate != nil {
			fleet.VehiclePlate = *normalized.VehiclePlate
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		fleet.UpdatedAt = now
		current.Fleet = fleet
		current.Version++
		current.UpdatedAt = now
		state.OnboardingCases[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmFleetTransition(store *CRMStore, id string, transition FleetTransition) (OnboardingCase, error) {
	var err error
	if id, err = normalizeCRMString("id", id, 80, true, false); err != nil {
		return OnboardingCase{}, err
	}
	normalized, err := crmNormalizeFleetTransition(transition)
	if err != nil {
		return OnboardingCase{}, err
	}
	if crmProtectedFleetStatus(normalized.ToStatus) && !normalized.Confirmed {
		return OnboardingCase{}, NewCRMValidationError("confirmed", "explicit confirmation is required")
	}

	var updated OnboardingCase
	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:      normalized.Actor,
		Operation:  "onboarding.fleet_transition",
		EntityType: "onboarding_case",
		EntityID:   id,
	}, func(state *CRMState, _ *IDAllocator) error {
		index := crmOnboardingIndex(state.OnboardingCases, id)
		if index < 0 {
			return &crmPipelineNotFoundError{resource: "onboarding case"}
		}
		current := state.OnboardingCases[index]
		if current.Version != normalized.ExpectedVersion ||
			(normalized.FromStatus != "" && current.Fleet.Status != normalized.FromStatus) {
			return NewCRMConflictError("onboarding version or fleet status conflict")
		}
		if !current.Fleet.Required {
			return NewCRMConflictError("fleet is not required")
		}
		if !crmCanTransitionFleet(current.Fleet.Status, normalized.ToStatus) {
			return NewCRMConflictError("fleet status transition is not allowed")
		}
		now := crmPipelineMutationTime(current.UpdatedAt)
		current.Fleet.Status = normalized.ToStatus
		current.Fleet.UpdatedAt = now
		current.Version++
		current.UpdatedAt = now
		state.OnboardingCases[index] = current
		updated = current
		return nil
	})
	return updated, err
}

func crmEnsureOnboardingForLeadInState(
	state *CRMState,
	allocator *IDAllocator,
	leadID string,
	actor string,
) (OnboardingCase, bool, error) {
	for _, onboarding := range state.OnboardingCases {
		if onboarding.LeadID == leadID {
			return onboarding, false, nil
		}
	}
	created, err := crmCreateOnboardingInState(state, allocator, OnboardingCreate{
		LeadID: leadID,
		Actor:  actor,
	})
	return created, err == nil, err
}

func crmCreateOnboardingInState(
	state *CRMState,
	allocator *IDAllocator,
	create OnboardingCreate,
) (OnboardingCase, error) {
	leadIndex := crmPipelineLeadIndex(state.Leads, create.LeadID)
	if leadIndex < 0 {
		return OnboardingCase{}, &crmPipelineNotFoundError{resource: "lead"}
	}
	lead := state.Leads[leadIndex]
	if lead.Stage != LeadStageHired {
		return OnboardingCase{}, NewCRMConflictError("lead must be hired before onboarding")
	}
	for _, onboarding := range state.OnboardingCases {
		if onboarding.LeadID == create.LeadID {
			return OnboardingCase{}, NewCRMConflictError("onboarding case already exists for lead")
		}
	}
	jobIndex := crmPipelineJobIndex(state.Jobs, lead.JobID)
	if jobIndex < 0 {
		return OnboardingCase{}, &crmPipelineNotFoundError{resource: "job"}
	}
	job := state.Jobs[jobIndex]
	requiredDocuments := create.RequiredDocuments
	if requiredDocuments == nil {
		requiredDocuments = append([]string{}, job.RequiredDocuments...)
	}
	if err := crmValidateCompletedDocuments(requiredDocuments, create.CompletedDocuments); err != nil {
		return OnboardingCase{}, err
	}
	id, err := allocator.Next(IDKindOnboarding)
	if err != nil {
		return OnboardingCase{}, err
	}
	now := time.Now().UTC()
	entryAddress := create.EntryAddress
	if entryAddress == "" {
		entryAddress = job.Location
	}
	siteContact := create.SiteContact
	if siteContact == "" {
		siteContact = job.OnsiteContact
	}
	onboarding := OnboardingCase{
		ID:                 id,
		LeadID:             lead.ID,
		CandidateID:        lead.CandidateID,
		JobID:              lead.JobID,
		Status:             OnboardingStatusPending,
		EntryAt:            cloneCRMTimePointer(create.EntryAt),
		EntryAddress:       entryAddress,
		SiteContact:        siteContact,
		RequiredDocuments:  append([]string{}, requiredDocuments...),
		CompletedDocuments: append([]string{}, create.CompletedDocuments...),
		MissingInformation: append([]string{}, create.MissingInformation...),
		Fleet: FleetCoordination{
			Required:    false,
			Destination: entryAddress,
			Status:      FleetStatusNotRequired,
			UpdatedAt:   now,
		},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	state.OnboardingCases = append(state.OnboardingCases, onboarding)
	return onboarding, nil
}

func crmNormalizeOnboardingCreate(create OnboardingCreate) (OnboardingCreate, error) {
	var err error
	if create.LeadID, err = normalizeCRMString("lead_id", create.LeadID, 80, true, false); err != nil {
		return OnboardingCreate{}, err
	}
	if create.EntryAt, err = normalizeCRMOptionalTime("entry_at", create.EntryAt); err != nil {
		return OnboardingCreate{}, err
	}
	if create.EntryAddress, err = normalizeCRMString("entry_address", create.EntryAddress, 500, false, true); err != nil {
		return OnboardingCreate{}, err
	}
	if create.SiteContact, err = normalizeCRMString("site_contact", create.SiteContact, 160, false, false); err != nil {
		return OnboardingCreate{}, err
	}
	if create.RequiredDocuments != nil {
		if create.RequiredDocuments, err = normalizeCRMStringList("required_documents", create.RequiredDocuments, 64, 200); err != nil {
			return OnboardingCreate{}, err
		}
	}
	if create.CompletedDocuments, err = normalizeCRMStringList("completed_documents", create.CompletedDocuments, 64, 200); err != nil {
		return OnboardingCreate{}, err
	}
	if create.MissingInformation, err = normalizeCRMStringList("missing_information", create.MissingInformation, 64, 500); err != nil {
		return OnboardingCreate{}, err
	}
	if create.Actor, err = crmNormalizeActor(create.Actor); err != nil {
		return OnboardingCreate{}, err
	}
	return create, nil
}

func crmNormalizeOnboardingUpdate(update OnboardingUpdate) (OnboardingUpdate, error) {
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.EntryAt == nil && !update.ClearEntryAt &&
		update.EntryAddress == nil && update.SiteContact == nil &&
		update.RequiredDocuments == nil && update.CompletedDocuments == nil &&
		update.MissingInformation == nil && update.BlockReason == nil {
		return OnboardingUpdate{}, NewCRMValidationError("onboarding", "at least one update field is required")
	}
	if update.EntryAt != nil && update.ClearEntryAt {
		return OnboardingUpdate{}, NewCRMValidationError("entry_at", "cannot be set and cleared together")
	}
	var err error
	if update.EntryAt, err = normalizeCRMOptionalTime("entry_at", update.EntryAt); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.EntryAddress, err = crmNormalizeOnboardingOptionalString("entry_address", update.EntryAddress, 500, true); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.SiteContact, err = crmNormalizeOnboardingOptionalString("site_contact", update.SiteContact, 160, false); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.RequiredDocuments, err = crmNormalizeOptionalList("required_documents", update.RequiredDocuments, 64, 200); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.CompletedDocuments, err = crmNormalizeOptionalList("completed_documents", update.CompletedDocuments, 64, 200); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.MissingInformation, err = crmNormalizeOptionalList("missing_information", update.MissingInformation, 64, 500); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.BlockReason, err = crmNormalizeOnboardingOptionalString("block_reason", update.BlockReason, 2000, true); err != nil {
		return OnboardingUpdate{}, err
	}
	if update.Actor, err = crmNormalizeActor(update.Actor); err != nil {
		return OnboardingUpdate{}, err
	}
	return update, nil
}

func crmNormalizeOnboardingTransition(transition OnboardingTransition) (OnboardingTransition, error) {
	if err := validateCRMVersion("expected_version", transition.ExpectedVersion); err != nil {
		return OnboardingTransition{}, err
	}
	if transition.FromStatus != "" && !IsValidOnboardingStatus(transition.FromStatus) {
		return OnboardingTransition{}, NewCRMValidationError("from_status", "invalid onboarding status")
	}
	if !IsValidOnboardingStatus(transition.ToStatus) {
		return OnboardingTransition{}, NewCRMValidationError("to_status", "invalid onboarding status")
	}
	var err error
	if transition.BlockReason, err = normalizeCRMString("block_reason", transition.BlockReason, 2000, false, true); err != nil {
		return OnboardingTransition{}, err
	}
	if transition.Actor, err = crmNormalizeActor(transition.Actor); err != nil {
		return OnboardingTransition{}, err
	}
	return transition, nil
}

func crmNormalizeFleetUpdate(update FleetUpdate) (FleetUpdate, error) {
	if err := validateCRMVersion("expected_version", update.ExpectedVersion); err != nil {
		return FleetUpdate{}, err
	}
	if update.Required == nil && update.PickupAddress == nil &&
		update.PickupAt == nil && !update.ClearPickupAt &&
		update.Destination == nil && update.LuggageNote == nil &&
		update.VehicleRequirement == nil && update.FleetContact == nil &&
		update.DriverName == nil && update.DriverPhone == nil &&
		update.VehiclePlate == nil {
		return FleetUpdate{}, NewCRMValidationError("fleet", "at least one update field is required")
	}
	if update.PickupAt != nil && update.ClearPickupAt {
		return FleetUpdate{}, NewCRMValidationError("pickup_at", "cannot be set and cleared together")
	}
	var err error
	if update.PickupAt, err = normalizeCRMOptionalTime("pickup_at", update.PickupAt); err != nil {
		return FleetUpdate{}, err
	}
	fields := []struct {
		name      string
		value     **string
		maxRunes  int
		multiline bool
	}{
		{"pickup_address", &update.PickupAddress, 500, true},
		{"destination", &update.Destination, 500, true},
		{"luggage_note", &update.LuggageNote, 1000, true},
		{"vehicle_requirement", &update.VehicleRequirement, 500, true},
		{"fleet_contact", &update.FleetContact, 160, false},
		{"driver_name", &update.DriverName, 160, false},
		{"driver_phone", &update.DriverPhone, 40, false},
		{"vehicle_plate", &update.VehiclePlate, 40, false},
	}
	for _, field := range fields {
		*field.value, err = crmNormalizeOnboardingOptionalString(field.name, *field.value, field.maxRunes, field.multiline)
		if err != nil {
			return FleetUpdate{}, err
		}
	}
	if update.Actor, err = crmNormalizeActor(update.Actor); err != nil {
		return FleetUpdate{}, err
	}
	return update, nil
}

func crmNormalizeFleetTransition(transition FleetTransition) (FleetTransition, error) {
	if err := validateCRMVersion("expected_version", transition.ExpectedVersion); err != nil {
		return FleetTransition{}, err
	}
	if transition.FromStatus != "" && !IsValidFleetStatus(transition.FromStatus) {
		return FleetTransition{}, NewCRMValidationError("from_status", "invalid fleet status")
	}
	if !IsValidFleetStatus(transition.ToStatus) || transition.ToStatus == FleetStatusNotRequired {
		return FleetTransition{}, NewCRMValidationError("to_status", "invalid fleet status")
	}
	var err error
	if transition.Actor, err = crmNormalizeActor(transition.Actor); err != nil {
		return FleetTransition{}, err
	}
	return transition, nil
}

func crmNormalizeOnboardingFilter(filter crmOnboardingFilter) (crmOnboardingFilter, error) {
	var err error
	if filter.LeadID, err = normalizeCRMString("lead_id", filter.LeadID, 80, false, false); err != nil {
		return crmOnboardingFilter{}, err
	}
	if filter.CandidateID, err = normalizeCRMString("candidate_id", filter.CandidateID, 80, false, false); err != nil {
		return crmOnboardingFilter{}, err
	}
	if filter.JobID, err = normalizeCRMString("job_id", filter.JobID, 80, false, false); err != nil {
		return crmOnboardingFilter{}, err
	}
	if filter.Status != "" && !IsValidOnboardingStatus(filter.Status) {
		return crmOnboardingFilter{}, NewCRMValidationError("status", "invalid onboarding status")
	}
	if filter.FleetStatus != "" && !IsValidFleetStatus(filter.FleetStatus) {
		return crmOnboardingFilter{}, NewCRMValidationError("fleet_status", "invalid fleet status")
	}
	if filter.Limit == 0 {
		filter.Limit = CRMDefaultListLimit
	}
	if filter.Limit < 1 || filter.Limit > CRMMaxListLimit {
		return crmOnboardingFilter{}, NewCRMValidationError("limit", "must be between 1 and 200")
	}
	if filter.Offset < 0 {
		return crmOnboardingFilter{}, NewCRMValidationError("offset", "must be zero or greater")
	}
	return filter, nil
}

func crmCanTransitionOnboarding(from, to OnboardingStatus) bool {
	if from == to || from == OnboardingStatusCompleted || from == OnboardingStatusCancelled {
		return false
	}
	if to == OnboardingStatusBlocked || to == OnboardingStatusCancelled {
		return true
	}
	switch from {
	case OnboardingStatusPending:
		return to == OnboardingStatusCollecting || to == OnboardingStatusReady
	case OnboardingStatusCollecting:
		return to == OnboardingStatusPending || to == OnboardingStatusReady
	case OnboardingStatusReady:
		return to == OnboardingStatusCollecting || to == OnboardingStatusTransportScheduled ||
			to == OnboardingStatusEnRoute || to == OnboardingStatusArrived || to == OnboardingStatusCheckedIn
	case OnboardingStatusTransportScheduled:
		return to == OnboardingStatusReady || to == OnboardingStatusEnRoute
	case OnboardingStatusEnRoute:
		return to == OnboardingStatusArrived
	case OnboardingStatusArrived:
		return to == OnboardingStatusCheckedIn
	case OnboardingStatusCheckedIn:
		return to == OnboardingStatusCompleted
	case OnboardingStatusBlocked:
		return to == OnboardingStatusPending || to == OnboardingStatusCollecting ||
			to == OnboardingStatusReady || to == OnboardingStatusTransportScheduled
	default:
		return false
	}
}

func crmCanTransitionFleet(from, to FleetStatus) bool {
	if from == to || from == FleetStatusNotRequired || from == FleetStatusDelivered {
		return false
	}
	if to == FleetStatusCancelled && from != FleetStatusCancelled {
		return true
	}
	switch from {
	case FleetStatusPending:
		return to == FleetStatusRequested
	case FleetStatusRequested:
		return to == FleetStatusConfirmed
	case FleetStatusConfirmed:
		return to == FleetStatusDispatched
	case FleetStatusDispatched:
		return to == FleetStatusPickedUp
	case FleetStatusPickedUp:
		return to == FleetStatusDelivered
	case FleetStatusCancelled:
		return to == FleetStatusPending
	default:
		return false
	}
}

func crmProtectedOnboardingStatus(status OnboardingStatus) bool {
	return status == OnboardingStatusCompleted || status == OnboardingStatusCancelled
}

func crmProtectedFleetStatus(status FleetStatus) bool {
	return status == FleetStatusConfirmed ||
		status == FleetStatusDispatched ||
		status == FleetStatusDelivered ||
		status == FleetStatusCancelled
}

func crmOnboardingReady(onboarding OnboardingCase) bool {
	return len(onboarding.MissingInformation) == 0 &&
		crmDocumentsComplete(onboarding.RequiredDocuments, onboarding.CompletedDocuments)
}

func crmDocumentsComplete(required, completed []string) bool {
	if len(required) != len(completed) {
		return false
	}
	completedSet := make(map[string]struct{}, len(completed))
	for _, document := range completed {
		completedSet[document] = struct{}{}
	}
	for _, document := range required {
		if _, ok := completedSet[document]; !ok {
			return false
		}
	}
	return true
}

func crmValidateCompletedDocuments(required, completed []string) error {
	requiredSet := make(map[string]struct{}, len(required))
	for _, document := range required {
		requiredSet[document] = struct{}{}
	}
	for _, document := range completed {
		if _, ok := requiredSet[document]; !ok {
			return NewCRMValidationError("completed_documents", "contains a document that is not required")
		}
	}
	return nil
}

func crmNormalizeOnboardingOptionalString(
	field string,
	value *string,
	maxRunes int,
	multiline bool,
) (*string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeCRMString(field, *value, maxRunes, false, multiline)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func crmNormalizeOptionalList(
	field string,
	value *[]string,
	maxCount int,
	maxRunes int,
) (*[]string, error) {
	if value == nil {
		return nil, nil
	}
	normalized, err := normalizeCRMStringList(field, *value, maxCount, maxRunes)
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func crmNormalizeActor(actor string) (string, error) {
	normalized, err := normalizeCRMString("actor", actor, 80, false, false)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		normalized = "system"
	}
	return normalized, nil
}

func crmOnboardingIndex(cases []OnboardingCase, id string) int {
	for i := range cases {
		if cases[i].ID == id {
			return i
		}
	}
	return -1
}

func crmParseOptionalBoolean(value string) (*bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return nil, nil
	case "true", "1":
		result := true
		return &result, nil
	case "false", "0":
		result := false
		return &result, nil
	default:
		return nil, NewCRMValidationError("fleet_required", "is invalid")
	}
}
