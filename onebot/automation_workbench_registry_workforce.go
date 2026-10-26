package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

const (
	workbenchWorkforceTextJSONBytes = 512
	workbenchWorkforceListItems     = 8
	workbenchWorkforceBatchItems    = 16
)

type workbenchOnboardingProjection struct {
	ID                          string                   `json:"id"`
	LeadID                      string                   `json:"lead_id"`
	CandidateID                 string                   `json:"candidate_id"`
	JobID                       string                   `json:"job_id"`
	Status                      OnboardingStatus         `json:"status"`
	EntryAt                     *time.Time               `json:"entry_at,omitempty"`
	EntryAddress                string                   `json:"entry_address"`
	EntryAddressTruncated       bool                     `json:"entry_address_truncated"`
	SiteContact                 string                   `json:"site_contact"`
	SiteContactTruncated        bool                     `json:"site_contact_truncated"`
	RequiredDocuments           []string                 `json:"required_documents"`
	RequiredDocumentsTotal      int                      `json:"required_documents_total"`
	RequiredDocumentsReturned   int                      `json:"required_documents_returned"`
	RequiredDocumentsTruncated  bool                     `json:"required_documents_truncated"`
	CompletedDocuments          []string                 `json:"completed_documents"`
	CompletedDocumentsTotal     int                      `json:"completed_documents_total"`
	CompletedDocumentsReturned  int                      `json:"completed_documents_returned"`
	CompletedDocumentsTruncated bool                     `json:"completed_documents_truncated"`
	MissingInformation          []string                 `json:"missing_information"`
	MissingInformationTotal     int                      `json:"missing_information_total"`
	MissingInformationReturned  int                      `json:"missing_information_returned"`
	MissingInformationTruncated bool                     `json:"missing_information_truncated"`
	Fleet                       workbenchFleetProjection `json:"fleet"`
	BlockReason                 string                   `json:"block_reason"`
	BlockReasonTruncated        bool                     `json:"block_reason_truncated"`
	Version                     uint64                   `json:"version"`
	CreatedAt                   time.Time                `json:"created_at"`
	UpdatedAt                   time.Time                `json:"updated_at"`
}

type workbenchFleetProjection struct {
	Required                    bool        `json:"required"`
	PickupAddress               string      `json:"pickup_address"`
	PickupAddressTruncated      bool        `json:"pickup_address_truncated"`
	PickupAt                    *time.Time  `json:"pickup_at,omitempty"`
	Destination                 string      `json:"destination"`
	DestinationTruncated        bool        `json:"destination_truncated"`
	LuggageNote                 string      `json:"luggage_note"`
	LuggageNoteTruncated        bool        `json:"luggage_note_truncated"`
	VehicleRequirement          string      `json:"vehicle_requirement"`
	VehicleRequirementTruncated bool        `json:"vehicle_requirement_truncated"`
	FleetContact                string      `json:"fleet_contact"`
	FleetContactTruncated       bool        `json:"fleet_contact_truncated"`
	DriverName                  string      `json:"driver_name"`
	DriverNameTruncated         bool        `json:"driver_name_truncated"`
	DriverPhone                 string      `json:"driver_phone"`
	DriverPhoneTruncated        bool        `json:"driver_phone_truncated"`
	VehiclePlate                string      `json:"vehicle_plate"`
	VehiclePlateTruncated       bool        `json:"vehicle_plate_truncated"`
	Status                      FleetStatus `json:"status"`
	UpdatedAt                   time.Time   `json:"updated_at"`
}

type workbenchOperationBatchSummary struct {
	ID             string               `json:"id"`
	Kind           OperationBatchKind   `json:"kind"`
	TargetStatus   string               `json:"target_status"`
	Status         OperationBatchStatus `json:"status"`
	Version        uint64               `json:"version"`
	MembersTotal   int                  `json:"members_total"`
	ResultsTotal   int                  `json:"results_total"`
	SucceededTotal int                  `json:"succeeded_total"`
	FailedTotal    int                  `json:"failed_total"`
	CreatedBy      string               `json:"created_by"`
	CreatedAt      time.Time            `json:"created_at"`
	UpdatedAt      time.Time            `json:"updated_at"`
}

type workbenchOperationBatchResultProjection struct {
	OnboardingCaseID      string                `json:"onboarding_case_id"`
	Outcome               OperationBatchOutcome `json:"outcome"`
	FromStatus            string                `json:"from_status"`
	ToStatus              string                `json:"to_status"`
	ErrorCode             string                `json:"error_code"`
	ErrorMessage          string                `json:"error_message"`
	ErrorMessageTruncated bool                  `json:"error_message_truncated"`
	CreatedAt             time.Time             `json:"created_at"`
}

type workbenchOperationBatchProjection struct {
	ID               string                                    `json:"id"`
	Kind             OperationBatchKind                        `json:"kind"`
	TargetStatus     string                                    `json:"target_status"`
	Status           OperationBatchStatus                      `json:"status"`
	Version          uint64                                    `json:"version"`
	Members          []OperationBatchMember                    `json:"members"`
	MembersTotal     int                                       `json:"members_total"`
	MembersReturned  int                                       `json:"members_returned"`
	MembersTruncated bool                                      `json:"members_truncated"`
	Results          []workbenchOperationBatchResultProjection `json:"results"`
	ResultsTotal     int                                       `json:"results_total"`
	ResultsReturned  int                                       `json:"results_returned"`
	ResultsTruncated bool                                      `json:"results_truncated"`
	SucceededTotal   int                                       `json:"succeeded_total"`
	FailedTotal      int                                       `json:"failed_total"`
	CreatedBy        string                                    `json:"created_by"`
	CreatedAt        time.Time                                 `json:"created_at"`
	UpdatedAt        time.Time                                 `json:"updated_at"`
}

type workbenchOperationBatchPreviewProjection struct {
	BatchID             string                      `json:"batch_id"`
	BatchVersion        uint64                      `json:"batch_version"`
	Kind                OperationBatchKind          `json:"kind"`
	TargetStatus        string                      `json:"target_status"`
	Protected           bool                        `json:"protected"`
	RequiresConfirm     bool                        `json:"requires_confirmation"`
	Items               []OperationBatchPreviewItem `json:"items"`
	ItemsTotal          int                         `json:"items_total"`
	ItemsReturned       int                         `json:"items_returned"`
	ItemsTruncated      bool                        `json:"items_truncated"`
	EligibleTotal       int                         `json:"eligible_total"`
	IneligibleTotal     int                         `json:"ineligible_total"`
	ProtectedItemsTotal int                         `json:"protected_items_total"`
}

type workbenchOperationBatchListPayload struct {
	OperationBatches []workbenchOperationBatchSummary `json:"operation_batches"`
}

func init() {
	bindWorkbenchOperationHandlers("m3-workforce-batches",
		WorkbenchOperationBinding{Operation: WorkbenchOperationOnboardingsCreate, Handler: workbenchWorkforceOnboardingsCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOnboardingsUpdate, Handler: workbenchWorkforceOnboardingsUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOnboardingsTransition, Handler: workbenchWorkforceOnboardingsTransition},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFleetUpdate, Handler: workbenchWorkforceFleetUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationFleetTransition, Handler: workbenchWorkforceFleetTransition},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesList, Handler: workbenchWorkforceOperationBatchesList},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesGet, Handler: workbenchWorkforceOperationBatchesGet},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesPreview, Handler: workbenchWorkforceOperationBatchesPreview},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesCreate, Handler: workbenchWorkforceOperationBatchesCreate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesUpdate, Handler: workbenchWorkforceOperationBatchesUpdate},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesMembers, Handler: workbenchWorkforceOperationBatchesMembers},
		WorkbenchOperationBinding{Operation: WorkbenchOperationOperationBatchesExecute, Handler: workbenchWorkforceOperationBatchesExecute},
	)
}

func workbenchWorkforceOnboardingsCreate(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationOnboardingsCreate
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchM0CRMResult(d, operation, func() (any, error) {
		create := raw.(*WorkbenchOnboardingCreateArguments).Onboarding
		create.Actor = strings.TrimSpace(execution.OperatorID)
		onboarding, err := crmOnboardingCreate(d.crm, create)
		if err != nil {
			return workbenchOnboardingProjection{}, err
		}
		return workbenchProjectOnboarding(onboarding), nil
	})
}

func workbenchWorkforceOnboardingsUpdate(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOnboardingsUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchOnboardingUpdateArguments)
		update := arguments.Update
		update.Actor = strings.TrimSpace(execution.OperatorID)
		onboarding, err := crmOnboardingUpdate(d.crm, arguments.OnboardingID, update)
		if err != nil {
			return workbenchOnboardingProjection{}, err
		}
		return workbenchProjectOnboarding(onboarding), nil
	})
}

func workbenchWorkforceOnboardingsTransition(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationOnboardingsTransition
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchM0CRMResult(d, operation, func() (any, error) {
		arguments := raw.(*WorkbenchOnboardingTransitionArguments)
		onboarding, err := crmOnboardingTransition(d.crm, arguments.OnboardingID, OnboardingTransition{
			ExpectedVersion: arguments.ExpectedVersion,
			FromStatus:      arguments.FromStatus,
			ToStatus:        arguments.ToStatus,
			BlockReason:     arguments.BlockReason,
			Confirmed:       true,
			Actor:           strings.TrimSpace(execution.OperatorID),
		})
		if err != nil {
			return workbenchOnboardingProjection{}, err
		}
		return workbenchProjectOnboarding(onboarding), nil
	})
}

func workbenchWorkforceFleetUpdate(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationFleetUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchFleetUpdateArguments)
		update := arguments.Update
		update.Actor = strings.TrimSpace(execution.OperatorID)
		onboarding, err := crmFleetUpdate(d.crm, arguments.OnboardingID, update)
		if err != nil {
			return workbenchOnboardingProjection{}, err
		}
		return workbenchProjectOnboarding(onboarding), nil
	})
}

func workbenchWorkforceFleetTransition(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationFleetTransition
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchM0CRMResult(d, operation, func() (any, error) {
		arguments := raw.(*WorkbenchFleetTransitionArguments)
		onboarding, err := crmFleetTransition(d.crm, arguments.OnboardingID, FleetTransition{
			ExpectedVersion: arguments.ExpectedVersion,
			FromStatus:      arguments.FromStatus,
			ToStatus:        arguments.ToStatus,
			Confirmed:       true,
			Actor:           strings.TrimSpace(execution.OperatorID),
		})
		if err != nil {
			return workbenchOnboardingProjection{}, err
		}
		return workbenchProjectOnboarding(onboarding), nil
	})
}

func workbenchWorkforceOperationBatchesList(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationOperationBatchesList
	if unavailable := workbenchM0RequireCRM(d, operation); unavailable != nil {
		return *unavailable
	}
	arguments := raw.(*WorkbenchPageArguments)
	batches, err := crmOperationBatchList(d.crm)
	if err != nil {
		return workbenchDomainFailure(operation, err)
	}
	if arguments.Offset >= len(batches) {
		return NewWorkbenchToolSuccess(
			operation,
			workbenchOperationBatchListPayload{OperationBatches: []workbenchOperationBatchSummary{}},
			newWorkbenchPage(arguments.Limit, arguments.Offset, len(batches), 0),
		)
	}
	end := arguments.Offset + arguments.Limit
	if end > len(batches) {
		end = len(batches)
	}
	items := make([]workbenchOperationBatchSummary, 0, end-arguments.Offset)
	var accepted WorkbenchToolResult
	for index := arguments.Offset; index < end; index++ {
		candidateItems := append(items, workbenchSummarizeOperationBatch(batches[index]))
		candidate := NewWorkbenchToolSuccess(
			operation,
			workbenchOperationBatchListPayload{OperationBatches: candidateItems},
			newWorkbenchPage(arguments.Limit, arguments.Offset, len(batches), len(candidateItems)),
		)
		if !candidate.OK {
			break
		}
		items = candidateItems
		accepted = candidate
	}
	if accepted.OK {
		return accepted
	}
	return NewWorkbenchToolFailure(
		operation,
		WorkbenchErrorInternal,
		"operation batch summary could not fit in the result envelope",
		false,
	)
}

func workbenchWorkforceOperationBatchesGet(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOperationBatchesGet, func() (any, error) {
		batch, err := crmOperationBatchGet(d.crm, raw.(*WorkbenchIDArguments).ID)
		if err != nil {
			return workbenchOperationBatchProjection{}, err
		}
		return workbenchProjectOperationBatch(batch), nil
	})
}

func workbenchWorkforceOperationBatchesPreview(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	_ WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOperationBatchesPreview, func() (any, error) {
		preview, err := crmOperationBatchPreview(d.crm, raw.(*WorkbenchIDArguments).ID)
		if err != nil {
			return OperationBatchPreview{}, err
		}
		if err := workbenchValidateOperationBatchTarget(preview.Kind, preview.TargetStatus); err != nil {
			return OperationBatchPreview{}, err
		}
		return workbenchProjectOperationBatchPreview(preview), nil
	})
}

func workbenchWorkforceOperationBatchesCreate(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOperationBatchesCreate, func() (any, error) {
		create := raw.(*WorkbenchOperationBatchCreateArguments).Batch
		if err := workbenchValidateOperationBatchTarget(create.Kind, create.TargetStatus); err != nil {
			return OperationBatch{}, err
		}
		batch, err := crmOperationBatchCreate(
			d.crm,
			create,
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return workbenchOperationBatchProjection{}, err
		}
		return workbenchProjectOperationBatch(batch), nil
	})
}

func workbenchWorkforceOperationBatchesUpdate(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOperationBatchesUpdate, func() (any, error) {
		arguments := raw.(*WorkbenchOperationBatchUpdateArguments)
		batch, err := crmOperationBatchGet(d.crm, arguments.BatchID)
		if err != nil {
			return OperationBatch{}, err
		}
		if arguments.Update.TargetStatus != nil {
			if err := workbenchValidateOperationBatchTarget(batch.Kind, *arguments.Update.TargetStatus); err != nil {
				return OperationBatch{}, err
			}
		}
		batch, err = crmOperationBatchUpdate(
			d.crm,
			arguments.BatchID,
			arguments.Update,
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return workbenchOperationBatchProjection{}, err
		}
		return workbenchProjectOperationBatch(batch), nil
	})
}

func workbenchWorkforceOperationBatchesMembers(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	return workbenchM0CRMResult(d, WorkbenchOperationOperationBatchesMembers, func() (any, error) {
		arguments := raw.(*WorkbenchOperationBatchMembersArguments)
		batch, err := crmOperationBatchUpdateMembers(
			d.crm,
			arguments.BatchID,
			arguments.Update,
			strings.TrimSpace(execution.OperatorID),
		)
		if err != nil {
			return workbenchOperationBatchProjection{}, err
		}
		return workbenchProjectOperationBatch(batch), nil
	})
}

func workbenchWorkforceOperationBatchesExecute(
	_ context.Context,
	d *defaultWorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	raw any,
) WorkbenchToolResult {
	operation := WorkbenchOperationOperationBatchesExecute
	if required := requireWorkbenchAllowOnce(operation, execution); required != nil {
		return *required
	}
	return workbenchM0CRMResult(d, operation, func() (any, error) {
		arguments := raw.(*WorkbenchVersionedIDArguments)
		batch, err := crmOperationBatchGet(d.crm, arguments.ID)
		if err != nil {
			return OperationBatch{}, err
		}
		if err := workbenchValidateOperationBatchTarget(batch.Kind, batch.TargetStatus); err != nil {
			return OperationBatch{}, err
		}
		batch, err = crmOperationBatchExecute(d.crm, arguments.ID, OperationBatchExecute{
			ExpectedVersion: arguments.ExpectedVersion,
			Confirmed:       true,
			Actor:           strings.TrimSpace(execution.OperatorID),
		})
		if err != nil {
			return workbenchOperationBatchProjection{}, err
		}
		return workbenchProjectOperationBatch(batch), nil
	})
}

func workbenchProjectOnboarding(onboarding OnboardingCase) workbenchOnboardingProjection {
	requiredDocuments, requiredTruncated := workbenchBoundedStringList(onboarding.RequiredDocuments)
	completedDocuments, completedTruncated := workbenchBoundedStringList(onboarding.CompletedDocuments)
	missingInformation, missingTruncated := workbenchBoundedStringList(onboarding.MissingInformation)
	entryAddress, entryAddressTruncated := workbenchBoundedJSONString(onboarding.EntryAddress)
	siteContact, siteContactTruncated := workbenchBoundedJSONString(onboarding.SiteContact)
	blockReason, blockReasonTruncated := workbenchBoundedJSONString(onboarding.BlockReason)
	return workbenchOnboardingProjection{
		ID:                          onboarding.ID,
		LeadID:                      onboarding.LeadID,
		CandidateID:                 onboarding.CandidateID,
		JobID:                       onboarding.JobID,
		Status:                      onboarding.Status,
		EntryAt:                     cloneCRMTimePointer(onboarding.EntryAt),
		EntryAddress:                entryAddress,
		EntryAddressTruncated:       entryAddressTruncated,
		SiteContact:                 siteContact,
		SiteContactTruncated:        siteContactTruncated,
		RequiredDocuments:           requiredDocuments,
		RequiredDocumentsTotal:      len(onboarding.RequiredDocuments),
		RequiredDocumentsReturned:   len(requiredDocuments),
		RequiredDocumentsTruncated:  requiredTruncated,
		CompletedDocuments:          completedDocuments,
		CompletedDocumentsTotal:     len(onboarding.CompletedDocuments),
		CompletedDocumentsReturned:  len(completedDocuments),
		CompletedDocumentsTruncated: completedTruncated,
		MissingInformation:          missingInformation,
		MissingInformationTotal:     len(onboarding.MissingInformation),
		MissingInformationReturned:  len(missingInformation),
		MissingInformationTruncated: missingTruncated,
		Fleet:                       workbenchProjectFleet(onboarding.Fleet),
		BlockReason:                 blockReason,
		BlockReasonTruncated:        blockReasonTruncated,
		Version:                     onboarding.Version,
		CreatedAt:                   onboarding.CreatedAt,
		UpdatedAt:                   onboarding.UpdatedAt,
	}
}

func workbenchProjectFleet(fleet FleetCoordination) workbenchFleetProjection {
	pickupAddress, pickupAddressTruncated := workbenchBoundedJSONString(fleet.PickupAddress)
	destination, destinationTruncated := workbenchBoundedJSONString(fleet.Destination)
	luggageNote, luggageNoteTruncated := workbenchBoundedJSONString(fleet.LuggageNote)
	vehicleRequirement, vehicleRequirementTruncated := workbenchBoundedJSONString(fleet.VehicleRequirement)
	fleetContact, fleetContactTruncated := workbenchBoundedJSONString(fleet.FleetContact)
	driverName, driverNameTruncated := workbenchBoundedJSONString(fleet.DriverName)
	driverPhone, driverPhoneTruncated := workbenchBoundedJSONString(fleet.DriverPhone)
	vehiclePlate, vehiclePlateTruncated := workbenchBoundedJSONString(fleet.VehiclePlate)
	return workbenchFleetProjection{
		Required:                    fleet.Required,
		PickupAddress:               pickupAddress,
		PickupAddressTruncated:      pickupAddressTruncated,
		PickupAt:                    cloneCRMTimePointer(fleet.PickupAt),
		Destination:                 destination,
		DestinationTruncated:        destinationTruncated,
		LuggageNote:                 luggageNote,
		LuggageNoteTruncated:        luggageNoteTruncated,
		VehicleRequirement:          vehicleRequirement,
		VehicleRequirementTruncated: vehicleRequirementTruncated,
		FleetContact:                fleetContact,
		FleetContactTruncated:       fleetContactTruncated,
		DriverName:                  driverName,
		DriverNameTruncated:         driverNameTruncated,
		DriverPhone:                 driverPhone,
		DriverPhoneTruncated:        driverPhoneTruncated,
		VehiclePlate:                vehiclePlate,
		VehiclePlateTruncated:       vehiclePlateTruncated,
		Status:                      fleet.Status,
		UpdatedAt:                   fleet.UpdatedAt,
	}
}

func workbenchBoundedStringList(values []string) ([]string, bool) {
	limit := len(values)
	if limit > workbenchWorkforceListItems {
		limit = workbenchWorkforceListItems
	}
	projected := make([]string, 0, limit)
	truncated := limit < len(values)
	for _, value := range values[:limit] {
		bounded, itemTruncated := workbenchBoundedJSONString(value)
		projected = append(projected, bounded)
		truncated = truncated || itemTruncated
	}
	return projected, truncated
}

func workbenchBoundedJSONString(value string) (string, bool) {
	encoded, err := json.Marshal(value)
	if err == nil && len(encoded) <= workbenchWorkforceTextJSONBytes {
		return value, false
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		middle := low + (high-low+1)/2
		candidate, marshalErr := json.Marshal(string(runes[:middle]))
		if marshalErr == nil && len(candidate) <= workbenchWorkforceTextJSONBytes {
			low = middle
			continue
		}
		high = middle - 1
	}
	return string(runes[:low]), true
}

func workbenchSummarizeOperationBatch(batch OperationBatch) workbenchOperationBatchSummary {
	succeeded, failed := workbenchOperationBatchOutcomeTotals(batch.Results)
	return workbenchOperationBatchSummary{
		ID:             batch.ID,
		Kind:           batch.Kind,
		TargetStatus:   batch.TargetStatus,
		Status:         batch.Status,
		Version:        batch.Version,
		MembersTotal:   len(batch.Members),
		ResultsTotal:   len(batch.Results),
		SucceededTotal: succeeded,
		FailedTotal:    failed,
		CreatedBy:      batch.CreatedBy,
		CreatedAt:      batch.CreatedAt,
		UpdatedAt:      batch.UpdatedAt,
	}
}

func workbenchProjectOperationBatch(batch OperationBatch) workbenchOperationBatchProjection {
	memberLimit := len(batch.Members)
	if memberLimit > workbenchWorkforceBatchItems {
		memberLimit = workbenchWorkforceBatchItems
	}
	resultLimit := len(batch.Results)
	if resultLimit > workbenchWorkforceBatchItems {
		resultLimit = workbenchWorkforceBatchItems
	}
	members := append([]OperationBatchMember{}, batch.Members[:memberLimit]...)
	results := make([]workbenchOperationBatchResultProjection, 0, resultLimit)
	resultsTruncated := resultLimit < len(batch.Results)
	for _, result := range batch.Results[:resultLimit] {
		errorMessage, errorMessageTruncated := workbenchBoundedJSONString(result.ErrorMessage)
		results = append(results, workbenchOperationBatchResultProjection{
			OnboardingCaseID:      result.OnboardingCaseID,
			Outcome:               result.Outcome,
			FromStatus:            result.FromStatus,
			ToStatus:              result.ToStatus,
			ErrorCode:             result.ErrorCode,
			ErrorMessage:          errorMessage,
			ErrorMessageTruncated: errorMessageTruncated,
			CreatedAt:             result.CreatedAt,
		})
		resultsTruncated = resultsTruncated || errorMessageTruncated
	}
	succeeded, failed := workbenchOperationBatchOutcomeTotals(batch.Results)
	return workbenchOperationBatchProjection{
		ID:               batch.ID,
		Kind:             batch.Kind,
		TargetStatus:     batch.TargetStatus,
		Status:           batch.Status,
		Version:          batch.Version,
		Members:          members,
		MembersTotal:     len(batch.Members),
		MembersReturned:  len(members),
		MembersTruncated: len(members) < len(batch.Members),
		Results:          results,
		ResultsTotal:     len(batch.Results),
		ResultsReturned:  len(results),
		ResultsTruncated: resultsTruncated,
		SucceededTotal:   succeeded,
		FailedTotal:      failed,
		CreatedBy:        batch.CreatedBy,
		CreatedAt:        batch.CreatedAt,
		UpdatedAt:        batch.UpdatedAt,
	}
}

func workbenchProjectOperationBatchPreview(preview OperationBatchPreview) workbenchOperationBatchPreviewProjection {
	itemLimit := len(preview.Items)
	if itemLimit > workbenchWorkforceBatchItems {
		itemLimit = workbenchWorkforceBatchItems
	}
	items := append([]OperationBatchPreviewItem{}, preview.Items[:itemLimit]...)
	eligible := 0
	protected := 0
	for _, item := range preview.Items {
		if item.Eligible {
			eligible++
		}
		if item.Protected {
			protected++
		}
	}
	return workbenchOperationBatchPreviewProjection{
		BatchID:             preview.BatchID,
		BatchVersion:        preview.BatchVersion,
		Kind:                preview.Kind,
		TargetStatus:        preview.TargetStatus,
		Protected:           preview.Protected,
		RequiresConfirm:     preview.RequiresConfirm,
		Items:               items,
		ItemsTotal:          len(preview.Items),
		ItemsReturned:       len(items),
		ItemsTruncated:      len(items) < len(preview.Items),
		EligibleTotal:       eligible,
		IneligibleTotal:     len(preview.Items) - eligible,
		ProtectedItemsTotal: protected,
	}
}

func workbenchOperationBatchOutcomeTotals(results []OperationBatchResult) (int, int) {
	succeeded := 0
	failed := 0
	for _, result := range results {
		switch result.Outcome {
		case OperationBatchOutcomeSucceeded:
			succeeded++
		case OperationBatchOutcomeFailed:
			failed++
		}
	}
	return succeeded, failed
}

func workbenchValidateOperationBatchTarget(kind OperationBatchKind, target string) error {
	if kind == OperationBatchKindOnboardingTransition &&
		OnboardingStatus(strings.TrimSpace(target)) == OnboardingStatusBlocked {
		return NewCRMValidationError(
			"target_status",
			"blocked onboarding batches are not supported",
		)
	}
	return nil
}
