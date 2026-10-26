package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var m303TestTime = time.Date(2026, time.July, 30, 9, 0, 0, 0, time.UTC)

func TestWorkbenchGovernanceFundLedgerDTOAndRegistry(t *testing.T) {
	definition, ok := WorkbenchOperationDefinitionFor(
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsLedger,
	)
	if !ok {
		t.Fatal("funds.ledger registry definition is missing")
	}
	if _, ok := definition.NewArguments().(*WorkbenchFundLedgerArguments); !ok {
		t.Fatalf("funds.ledger arguments type = %T", definition.NewArguments())
	}
	if !reflect.DeepEqual(definition.RequiredFields, []string{"id", "limit", "offset"}) {
		t.Fatalf("funds.ledger required fields = %v", definition.RequiredFields)
	}
	decoded := m303Decoded(
		t,
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsLedger,
		WorkbenchFundLedgerArguments{ID: "fund_00000001", Limit: 1, Offset: 0},
	)
	arguments, ok := decoded.Arguments.(*WorkbenchFundLedgerArguments)
	if !ok || *arguments != (WorkbenchFundLedgerArguments{
		ID: "fund_00000001", Limit: 1, Offset: 0,
	}) {
		t.Fatalf("funds.ledger decoded arguments = %#v", decoded.Arguments)
	}
}

func TestWorkbenchGovernanceFundOperationsExactSchemasAndNoPayment(t *testing.T) {
	fixture := m303FundsFixture(t)
	execution := m303Allowed("funds-operator")
	maxText := strings.Repeat("x", 4000)
	dueAt := m303TestTime.Add(24 * time.Hour)

	create := m303Dispatch(
		t,
		fixture.dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		WorkbenchFundCreateArguments{
			CandidateID:      fixture.candidate.ID,
			JobID:            fixture.job.ID,
			LeadID:           fixture.lead.ID,
			OnboardingCaseID: fixture.onboarding.ID,
			AmountMinor:      12500,
			Currency:         "cny",
			Condition:        maxText,
			PromisedBy:       "Customer A",
			DueAt:            &dueAt,
			Evidence:         maxText,
		},
	)
	m303AssertSuccessEnvelope(t, create, false)
	m303AssertJSONKeys(t, create.Data, m303FundCreateResultKeys()...)
	m303AssertNoPaymentFields(t, create.Data)
	var createResult workbenchGovernanceFundCreateResult
	m303Decode(t, create.Data, &createResult)
	if createResult.Currency != "CNY" ||
		createResult.Status != FundCallbackStatusDraft ||
		createResult.Version != 1 {
		t.Fatalf("funds.create payload = %+v", createResult)
	}
	fund, err := crmFundCallbackGet(fixture.store, createResult.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fund.Condition != maxText || fund.Evidence != maxText {
		t.Fatalf("persisted funds.create callback = %+v", fund)
	}
	m303AssertLastAudit(t, fixture.store, "fund_callback.create", "funds-operator")

	awaitingCommitment := FundCallbackStatusAwaitingCommitment
	updatedCondition := strings.Repeat("y", 4000)
	updateArguments := WorkbenchFundUpdateArguments{
		FundID:          fund.ID,
		ExpectedVersion: fund.Version,
		Condition:       &updatedCondition,
		Status:          &awaitingCommitment,
	}
	update := m303Dispatch(
		t,
		fixture.dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsUpdate,
		updateArguments,
	)
	m303AssertSuccessEnvelope(t, update, false)
	m303AssertJSONKeys(t, update.Data, m303FundUpdateResultKeys()...)
	var updateResult workbenchGovernanceFundUpdateResult
	m303Decode(t, update.Data, &updateResult)
	if updateResult.Status != FundCallbackStatusAwaitingCommitment ||
		updateResult.Version != 2 {
		t.Fatalf("funds.update payload = %+v", updateResult)
	}
	fund, err = crmFundCallbackGet(fixture.store, updateResult.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fund.Condition != updatedCondition {
		t.Fatalf("persisted funds.update callback = %+v", fund)
	}
	m303AssertRejectedWithoutStateChange(t, fixture.store, func() WorkbenchToolResult {
		return m303Dispatch(
			t,
			fixture.dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsUpdate,
			updateArguments,
		)
	}, WorkbenchErrorConflict)

	commitArguments := WorkbenchVersionedIDArguments{
		ID:              fund.ID,
		ExpectedVersion: fund.Version,
	}
	commitment := m303Dispatch(
		t,
		fixture.dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsConfirmCommitment,
		commitArguments,
	)
	m303AssertSuccessEnvelope(t, commitment, false)
	m303AssertJSONKeys(t, commitment.Data, m303FundCommitmentResultKeys()...)
	var commitmentResult workbenchGovernanceFundCommitmentResult
	m303Decode(t, commitment.Data, &commitmentResult)
	if commitmentResult.Status != FundCallbackStatusCommitted ||
		commitmentResult.CommitmentBy != "funds-operator" ||
		commitmentResult.CommitmentAt == nil ||
		commitmentResult.Version != 3 {
		t.Fatalf("funds.confirm_commitment payload = %+v", commitmentResult)
	}
	fund, err = crmFundCallbackGet(fixture.store, commitmentResult.ID)
	if err != nil {
		t.Fatal(err)
	}
	m303AssertRejectedWithoutStateChange(t, fixture.store, func() WorkbenchToolResult {
		return m303Dispatch(
			t,
			fixture.dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmCommitment,
			WorkbenchVersionedIDArguments{ID: fund.ID, ExpectedVersion: fund.Version},
		)
	}, WorkbenchErrorConflict)

	get := m303Dispatch(
		t,
		fixture.dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsGet,
		WorkbenchIDArguments{ID: fund.ID},
	)
	m303AssertSuccessEnvelope(t, get, false)
	m303AssertJSONKeys(t, get.Data, m303FundReadKeys()...)

	list := m303Dispatch(
		t,
		fixture.dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsList,
		WorkbenchFundListArguments{
			Query:            "customer a",
			CandidateID:      fixture.candidate.ID,
			JobID:            fixture.job.ID,
			LeadID:           fixture.lead.ID,
			OnboardingCaseID: fixture.onboarding.ID,
			Status:           FundCallbackStatusCommitted,
			Currency:         "cny",
			Limit:            1,
			Offset:           0,
		},
	)
	m303AssertSuccessEnvelope(t, list, true)
	listObject := m303AssertJSONKeys(t, list.Data, "fund_callbacks")
	var listed []json.RawMessage
	m303Decode(t, listObject["fund_callbacks"], &listed)
	if len(listed) != 1 {
		t.Fatalf("funds.list returned %d callbacks, want 1", len(listed))
	}
	m303AssertJSONKeys(t, listed[0], m303FundReadKeys()...)
	if list.Page == nil || *list.Page != (WorkbenchPage{
		Limit: 1, Offset: 0, Returned: 1, Total: 1, HasMore: false,
	}) {
		t.Fatalf("funds.list page = %+v", list.Page)
	}

	awaitingFinal := FundCallbackStatusAwaitingFinal
	awaitingFinalResult := m303Dispatch(
		t,
		fixture.dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsUpdate,
		WorkbenchFundUpdateArguments{
			FundID:          fund.ID,
			ExpectedVersion: fund.Version,
			Status:          &awaitingFinal,
		},
	)
	m303AssertSuccessEnvelope(t, awaitingFinalResult, false)
	m303AssertJSONKeys(t, awaitingFinalResult.Data, m303FundUpdateResultKeys()...)
	m303Decode(t, awaitingFinalResult.Data, &updateResult)
	if updateResult.Status != FundCallbackStatusAwaitingFinal || updateResult.Version != 4 {
		t.Fatalf("funds.update awaiting_final payload = %+v", updateResult)
	}
	fund, err = crmFundCallbackGet(fixture.store, updateResult.ID)
	if err != nil {
		t.Fatal(err)
	}

	finalArguments := WorkbenchFundFinalArguments{
		FundID:          fund.ID,
		ExpectedVersion: fund.Version,
		FinalResult:     maxText,
	}
	final := m303Dispatch(
		t,
		fixture.dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsConfirmFinal,
		finalArguments,
	)
	m303AssertSuccessEnvelope(t, final, false)
	m303AssertJSONKeys(t, final.Data, m303FundFinalResultKeys()...)
	m303AssertNoPaymentFields(t, final.Data)
	var finalResult workbenchGovernanceFundFinalResult
	m303Decode(t, final.Data, &finalResult)
	if finalResult.Status != FundCallbackStatusCompleted ||
		finalResult.FinalConfirmedBy != "funds-operator" ||
		finalResult.FinalConfirmedAt == nil ||
		finalResult.Version != 5 {
		t.Fatalf("funds.confirm_final payload = %+v", finalResult)
	}
	fund, err = crmFundCallbackGet(fixture.store, finalResult.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fund.FinalResult != maxText {
		t.Fatalf("persisted funds.confirm_final callback = %+v", fund)
	}
	m303AssertRejectedWithoutStateChange(t, fixture.store, func() WorkbenchToolResult {
		return m303Dispatch(
			t,
			fixture.dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmFinal,
			WorkbenchFundFinalArguments{
				FundID:          fund.ID,
				ExpectedVersion: fund.Version,
				FinalResult:     "duplicate",
			},
		)
	}, WorkbenchErrorConflict)

	beforeLedger := fixture.store.Snapshot()
	ledger := m303Dispatch(
		t,
		fixture.dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsLedger,
		WorkbenchFundLedgerArguments{
			ID: fund.ID, Limit: WorkbenchMaxPageLimit, Offset: 0,
		},
	)
	m303AssertSuccessEnvelope(t, ledger, true)
	ledgerObject := m303AssertJSONKeys(t, ledger.Data, "ledger", "total")
	var entries []json.RawMessage
	m303Decode(t, ledgerObject["ledger"], &entries)
	if len(entries) != 5 {
		t.Fatalf("funds.ledger entries = %d, want 5", len(entries))
	}
	m303AssertJSONKeys(t, entries[0], m303LedgerKeys(false)...)
	for _, entry := range entries[1:] {
		m303AssertJSONKeys(t, entry, m303LedgerKeys(true)...)
	}
	if ledger.Page == nil || *ledger.Page != (WorkbenchPage{
		Limit: WorkbenchMaxPageLimit, Offset: 0, Returned: 5, Total: 5, HasMore: false,
	}) {
		t.Fatalf("funds.ledger page = %+v", ledger.Page)
	}
	if afterLedger := fixture.store.Snapshot(); !reflect.DeepEqual(afterLedger, beforeLedger) {
		t.Fatal("funds.ledger mutated CRM state")
	}

	fixture.agent.mu.Lock()
	agentCalls := append([]string(nil), fixture.agent.calls...)
	fixture.agent.mu.Unlock()
	if len(agentCalls) != 0 {
		t.Fatalf("fund operations called external agent paths: %v", agentCalls)
	}
	for _, event := range fixture.store.Snapshot().AuditEvents {
		if strings.Contains(strings.ToLower(event.Operation), "payment") {
			t.Fatalf("fund operation recorded payment execution audit: %+v", event)
		}
	}
}

func TestWorkbenchGovernanceTerminalOperationsExactSchemasAndDuplicateConflicts(t *testing.T) {
	t.Run("candidate terminal", func(t *testing.T) {
		store := m303OpenStore(t)
		candidate := m303Candidate(t, store)
		dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
		arguments := WorkbenchCandidateTerminalArguments{
			CandidateID: candidate.ID, ExpectedVersion: candidate.Version, Status: CandidateStatusPlaced,
		}
		result := m303Dispatch(
			t, dispatcher, m303Allowed("candidate-operator"),
			AutomationToolWorkbenchAllowOnce, WorkbenchOperationCandidatesTerminal, arguments,
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertJSONKeys(t, result.Data, m303CandidateTerminalKeys()...)
		var updated workbenchGovernanceCandidateTerminalResult
		m303Decode(t, result.Data, &updated)
		if updated.Status != CandidateStatusPlaced ||
			updated.Version != candidate.Version+1 ||
			updated.Summary != candidate.Summary ||
			updated.SummaryLength != len([]rune(candidate.Summary)) ||
			updated.SummaryTruncated {
			t.Fatalf("candidate terminal payload = %+v", updated)
		}
		m303AssertLastAudit(t, store, "candidate.transition_terminal", "candidate-operator")
		m303AssertRejectedWithoutStateChange(t, store, func() WorkbenchToolResult {
			return m303Dispatch(
				t, dispatcher, m303Allowed("candidate-operator"),
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationCandidatesTerminal,
				WorkbenchCandidateTerminalArguments{
					CandidateID:     updated.ID,
					ExpectedVersion: updated.Version,
					Status:          CandidateStatusPlaced,
				},
			)
		}, WorkbenchErrorConflict)
	})

	t.Run("candidate anonymize", func(t *testing.T) {
		store := m303OpenStore(t)
		candidate := m303Candidate(t, store)
		dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
		result := m303Dispatch(
			t, dispatcher, m303Allowed("privacy-operator"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationCandidatesAnonymize,
			WorkbenchVersionedIDArguments{ID: candidate.ID, ExpectedVersion: candidate.Version},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertJSONKeys(t, result.Data, m303CandidateKeys(true)...)
		var anonymized Candidate
		m303Decode(t, result.Data, &anonymized)
		if !workbenchGovernanceCandidateAnonymized(anonymized) {
			t.Fatalf("candidate anonymize payload = %+v", anonymized)
		}
		m303AssertLastAudit(t, store, "candidate.anonymize", "privacy-operator")
		m303AssertRejectedWithoutStateChange(t, store, func() WorkbenchToolResult {
			return m303Dispatch(
				t, dispatcher, m303Allowed("privacy-operator"),
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationCandidatesAnonymize,
				WorkbenchVersionedIDArguments{ID: anonymized.ID, ExpectedVersion: anonymized.Version},
			)
		}, WorkbenchErrorConflict)
	})

	t.Run("job terminal", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303Job(t, store)
		dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
		result := m303Dispatch(
			t, dispatcher, m303Allowed("job-operator"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsTerminal,
			WorkbenchJobTerminalArguments{
				JobID: job.ID, ExpectedVersion: job.Version, Status: JobStatusClosed,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertJSONKeys(t, result.Data, "id", "status", "updated_at", "version")
		var updated workbenchGovernanceJobTerminalResult
		m303Decode(t, result.Data, &updated)
		if updated.Status != JobStatusClosed || updated.Version != job.Version+1 {
			t.Fatalf("job terminal payload = %+v", updated)
		}
		m303AssertLastAudit(t, store, "job.transition_terminal", "job-operator")
		m303AssertRejectedWithoutStateChange(t, store, func() WorkbenchToolResult {
			return m303Dispatch(
				t, dispatcher, m303Allowed("job-operator"),
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationJobsTerminal,
				WorkbenchJobTerminalArguments{
					JobID: updated.ID, ExpectedVersion: updated.Version, Status: JobStatusClosed,
				},
			)
		}, WorkbenchErrorConflict)
	})

	t.Run("job compensation", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303Job(t, store)
		dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
		summary := strings.Repeat("c", 2000)
		accommodation := "Updated accommodation"
		transport := "Updated transport"
		result := m303Dispatch(
			t, dispatcher, m303Allowed("compensation-operator"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsUpdateCompensation,
			WorkbenchJobCompensationArguments{
				JobID:                 job.ID,
				ExpectedVersion:       job.Version,
				CompensationSummary:   &summary,
				Accommodation:         &accommodation,
				TransportRequirements: &transport,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertJSONKeys(
			t,
			result.Data,
			m303JobCompensationKeys()...,
		)
		var updated workbenchGovernanceJobCompensationResult
		m303Decode(t, result.Data, &updated)
		if updated.CompensationSummary != summary ||
			updated.Accommodation != accommodation ||
			updated.TransportRequirements != transport ||
			updated.CompensationSummaryLength != len([]rune(summary)) ||
			updated.AccommodationLength != len([]rune(accommodation)) ||
			updated.TransportRequirementsLength != len([]rune(transport)) ||
			updated.CompensationSummaryTruncated ||
			updated.AccommodationTruncated ||
			updated.TransportRequirementsTruncated ||
			updated.Version != job.Version+1 {
			t.Fatalf("job compensation payload = %+v", updated)
		}
		m303AssertLastAudit(t, store, "job.update_compensation", "compensation-operator")
		m303AssertRejectedWithoutStateChange(t, store, func() WorkbenchToolResult {
			return m303Dispatch(
				t, dispatcher, m303Allowed("compensation-operator"),
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationJobsUpdateCompensation,
				WorkbenchJobCompensationArguments{
					JobID:               updated.ID,
					ExpectedVersion:     updated.Version,
					CompensationSummary: &summary,
				},
			)
		}, WorkbenchErrorConflict)
	})

	t.Run("revision activate", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303Job(t, store)
		firstRevisionID := job.ActiveDemandRevisionID
		title := "Updated Role"
		updated, err := crmPipelineUpdateJob(store, job.ID, JobUpdate{
			ExpectedVersion: job.Version,
			Title:           &title,
		})
		if err != nil {
			t.Fatal(err)
		}
		dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
		result := m303Dispatch(
			t, dispatcher, m303Allowed("revision-operator"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationRevisionsActivate,
			WorkbenchRevisionActivateArguments{
				JobID:           updated.ID,
				RevisionID:      firstRevisionID,
				ExpectedVersion: updated.Version,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertJSONKeys(
			t,
			result.Data,
			"active_demand_revision_id",
			"demand_revision_number",
			"id",
			"updated_at",
			"version",
		)
		var activated workbenchGovernanceRevisionActivationResult
		m303Decode(t, result.Data, &activated)
		stored, err := crmPipelineStoredJob(store, activated.ID)
		if err != nil {
			t.Fatal(err)
		}
		if activated.ActiveDemandRevisionID != firstRevisionID ||
			stored.Title != job.Title ||
			activated.Version != updated.Version+1 {
			t.Fatalf("revision activation payload = %+v", activated)
		}
		m303AssertLastAudit(t, store, "job.demand_revision.activate", "revision-operator")
		m303AssertRejectedWithoutStateChange(t, store, func() WorkbenchToolResult {
			return m303Dispatch(
				t, dispatcher, m303Allowed("revision-operator"),
				AutomationToolWorkbenchAllowOnce,
				WorkbenchOperationRevisionsActivate,
				WorkbenchRevisionActivateArguments{
					JobID:           activated.ID,
					RevisionID:      firstRevisionID,
					ExpectedVersion: activated.Version,
				},
			)
		}, WorkbenchErrorConflict)
	})
}

func TestWorkbenchGovernanceListAndLedgerStableBoundedReadOnly(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	var funds []FundCallback
	for index := 0; index < 3; index++ {
		fund, err := crmFundCallbackCreate(store, FundCallbackCreate{
			CandidateID: candidate.ID,
			AmountMinor: int64(1000 + index),
			Currency:    "CNY",
			Condition:   "stable list",
			PromisedBy:  "fixture",
			Actor:       "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		funds = append(funds, fund)
	}
	sameTime := time.Now().UTC().Add(time.Second)
	if err := store.UpdateWithMeta(CRMUpdateMeta{
		Actor: "fixture", Operation: "fund_callback.update",
	}, func(state *CRMState, _ *IDAllocator) error {
		for index := range state.FundCallbacks {
			state.FundCallbacks[index].UpdatedAt = sameTime
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := m303AppendLedgerEntries(store, funds[0].ID, 130); err != nil {
		t.Fatal(err)
	}

	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	before := store.Snapshot()
	list := m303Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsList,
		WorkbenchFundListArguments{Limit: 1, Offset: 1},
	)
	m303AssertSuccessEnvelope(t, list, true)
	var listPayload struct {
		FundCallbacks []workbenchGovernanceFundReadResult `json:"fund_callbacks"`
	}
	m303Decode(t, list.Data, &listPayload)
	if len(listPayload.FundCallbacks) != 1 ||
		listPayload.FundCallbacks[0].ID != funds[1].ID ||
		list.Page == nil ||
		*list.Page != (WorkbenchPage{
			Limit: 1, Offset: 1, Returned: 1, Total: 3, HasMore: true, NextOffset: 2,
		}) {
		t.Fatalf("stable paged funds.list = %+v page=%+v", listPayload, list.Page)
	}
	maxPage := m303Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsList,
		WorkbenchFundListArguments{Limit: WorkbenchMaxPageLimit, Offset: 0},
	)
	if maxPage.Page == nil || maxPage.Page.Limit != WorkbenchMaxPageLimit ||
		maxPage.Page.Returned != 3 || maxPage.Page.Total != 3 {
		t.Fatalf("maximum funds.list page = %+v", maxPage.Page)
	}

	allEntries, err := crmFundCallbackLedger(store, funds[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	ledgerArguments := WorkbenchFundLedgerArguments{
		ID: funds[0].ID, Limit: WorkbenchMaxPageLimit, Offset: 0,
	}
	ledger := m303Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsLedger,
		ledgerArguments,
	)
	m303AssertSuccessEnvelope(t, ledger, true)
	again := m303Dispatch(
		t,
		dispatcher,
		WorkbenchExecutionContext{},
		AutomationToolWorkbenchQuery,
		WorkbenchOperationFundsLedger,
		ledgerArguments,
	)
	if !reflect.DeepEqual(again, ledger) {
		t.Fatalf("funds.ledger output is unstable:\nfirst=%+v\nagain=%+v", ledger, again)
	}

	collected := make([]FundCallbackLedgerEntry, 0, len(allEntries))
	offset := 0
	for {
		pageResult := m303Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsLedger,
			WorkbenchFundLedgerArguments{
				ID: funds[0].ID, Limit: WorkbenchMaxPageLimit, Offset: offset,
			},
		)
		m303AssertSuccessEnvelope(t, pageResult, true)
		encoded, err := json.Marshal(pageResult)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) > WorkbenchMaxResultJSONBytes {
			t.Fatalf("funds.ledger page encoded to %d bytes", len(encoded))
		}
		var pagePayload workbenchGovernanceFundLedgerData
		m303Decode(t, pageResult.Data, &pagePayload)
		if pageResult.Page == nil ||
			pageResult.Page.Limit != WorkbenchMaxPageLimit ||
			pageResult.Page.Offset != offset ||
			pageResult.Page.Total != len(allEntries) ||
			pageResult.Page.Returned != len(pagePayload.Ledger) ||
			pagePayload.Total != len(allEntries) {
			t.Fatalf("funds.ledger payload=%+v page=%+v", pagePayload, pageResult.Page)
		}
		collected = append(collected, pagePayload.Ledger...)
		if !pageResult.Page.HasMore {
			if pageResult.Page.NextOffset != 0 {
				t.Fatalf("final funds.ledger next_offset = %d", pageResult.Page.NextOffset)
			}
			break
		}
		if pageResult.Page.NextOffset <= offset {
			t.Fatalf("funds.ledger page did not advance: %+v", pageResult.Page)
		}
		offset = pageResult.Page.NextOffset
	}
	if !reflect.DeepEqual(collected, allEntries) {
		t.Fatalf("funds.ledger collected %d entries, want %d", len(collected), len(allEntries))
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("funds.list or funds.ledger mutated CRM state")
	}
}

func TestWorkbenchGovernanceMaximumEscapedFundReadsStayWithinEnvelope(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	maxText := strings.Repeat("<", 4000)
	maxPromisedBy := strings.Repeat("<", 160)
	for index := 0; index < 6; index++ {
		fund, err := crmFundCallbackCreate(store, FundCallbackCreate{
			CandidateID: candidate.ID,
			AmountMinor: int64(index + 1),
			Currency:    "CNY",
			Condition:   maxText,
			PromisedBy:  maxPromisedBy,
			Evidence:    maxText,
			Actor:       "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		if index != 0 {
			continue
		}
		awaitingCommitment := FundCallbackStatusAwaitingCommitment
		fund, err = crmFundCallbackUpdate(store, fund.ID, FundCallbackUpdate{
			ExpectedVersion: fund.Version,
			Status:          &awaitingCommitment,
			Actor:           "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		fund, err = crmFundCallbackConfirmCommitment(store, fund.ID, FundCommitmentConfirmation{
			ExpectedVersion: fund.Version,
			Confirmed:       true,
			OperatorID:      "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		awaitingFinal := FundCallbackStatusAwaitingFinal
		fund, err = crmFundCallbackUpdate(store, fund.ID, FundCallbackUpdate{
			ExpectedVersion: fund.Version,
			Status:          &awaitingFinal,
			Actor:           "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := crmFundCallbackConfirmFinal(store, fund.ID, FundFinalConfirmation{
			ExpectedVersion: fund.Version,
			Confirmed:       true,
			OperatorID:      "fixture",
			FinalResult:     maxText,
		}); err != nil {
			t.Fatal(err)
		}
	}
	total, expected, err := crmFundCallbackList(store, crmFundCallbackFilter{
		Limit: WorkbenchMaxPageLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	before := store.Snapshot()

	for _, fund := range expected {
		result := m303Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsGet,
			WorkbenchIDArguments{ID: fund.ID},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertEnvelopeSize(t, result)
		m303AssertJSONKeys(t, result.Data, m303FundReadKeys()...)
		var projected workbenchGovernanceFundReadResult
		m303Decode(t, result.Data, &projected)
		m303AssertFundReadProjection(t, fund, projected)
	}

	collected := make([]workbenchGovernanceFundReadResult, 0, total)
	offset := 0
	envelopeLimited := false
	for {
		result := m303Dispatch(
			t,
			dispatcher,
			WorkbenchExecutionContext{},
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsList,
			WorkbenchFundListArguments{
				Limit: WorkbenchMaxPageLimit, Offset: offset,
			},
		)
		m303AssertSuccessEnvelope(t, result, true)
		m303AssertEnvelopeSize(t, result)
		var payload workbenchGovernanceFundListData
		m303Decode(t, result.Data, &payload)
		if result.Page == nil ||
			result.Page.Offset != offset ||
			result.Page.Total != total ||
			result.Page.Returned != len(payload.FundCallbacks) {
			t.Fatalf("funds.list payload=%+v page=%+v", payload, result.Page)
		}
		if result.Page.Returned < total-offset && result.Page.HasMore {
			envelopeLimited = true
		}
		if result.Page.Returned == 0 && result.Page.HasMore {
			t.Fatalf("funds.list page made no progress: %+v", result.Page)
		}
		for index := range payload.FundCallbacks {
			m303AssertFundReadProjection(t, expected[offset+index], payload.FundCallbacks[index])
		}
		collected = append(collected, payload.FundCallbacks...)
		if !result.Page.HasMore {
			break
		}
		if result.Page.NextOffset <= offset {
			t.Fatalf("funds.list page did not advance: %+v", result.Page)
		}
		offset = result.Page.NextOffset
	}
	if !envelopeLimited {
		t.Fatal("funds.list did not exercise aggregate encoded-byte pagination")
	}
	expectedProjected := make([]workbenchGovernanceFundReadResult, len(expected))
	for index := range expected {
		expectedProjected[index] = workbenchGovernanceFundReadProjection(expected[index])
	}
	if !reflect.DeepEqual(collected, expectedProjected) {
		t.Fatalf("funds.list collected %d callbacks, want %d", len(collected), len(expected))
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("funds.list mutated CRM state")
	}
}

func TestWorkbenchGovernanceMaximumMutationResultsStayWithinEnvelope(t *testing.T) {
	t.Run("job terminal", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303MaximumJob(t, store)
		result := m303Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
			m303Allowed("max-job-terminal"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsTerminal,
			WorkbenchJobTerminalArguments{
				JobID: job.ID, ExpectedVersion: job.Version, Status: JobStatusClosed,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertEnvelopeSize(t, result)
		stored, err := crmPipelineStoredJob(store, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != JobStatusClosed || stored.Version != job.Version+1 {
			t.Fatalf("maximum job terminal state = %+v", stored)
		}
	})

	t.Run("job compensation", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303MaximumJob(t, store)
		summary := strings.Repeat("n", 2000)
		result := m303Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
			m303Allowed("max-job-compensation"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsUpdateCompensation,
			WorkbenchJobCompensationArguments{
				JobID:               job.ID,
				ExpectedVersion:     job.Version,
				CompensationSummary: &summary,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertEnvelopeSize(t, result)
		stored, err := crmPipelineStoredJob(store, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.CompensationSummary != summary || stored.Version != job.Version+1 {
			t.Fatalf("maximum job compensation state = %+v", stored)
		}
	})

	t.Run("revision activation", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303MaximumJob(t, store)
		firstRevisionID := job.ActiveDemandRevisionID
		title := strings.Repeat("z", 160)
		updated, err := crmPipelineUpdateJob(store, job.ID, JobUpdate{
			ExpectedVersion: job.Version,
			Title:           &title,
		})
		if err != nil {
			t.Fatal(err)
		}
		result := m303Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
			m303Allowed("max-revision-activation"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationRevisionsActivate,
			WorkbenchRevisionActivateArguments{
				JobID:           updated.ID,
				RevisionID:      firstRevisionID,
				ExpectedVersion: updated.Version,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertEnvelopeSize(t, result)
		stored, err := crmPipelineStoredJob(store, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.ActiveDemandRevisionID != firstRevisionID ||
			stored.Title != job.Title ||
			stored.Version != updated.Version+1 {
			t.Fatalf("maximum revision activation state = %+v", stored)
		}
	})

	t.Run("fund lifecycle", func(t *testing.T) {
		fixture := m303FundsFixture(t)
		dispatcher := fixture.dispatcher
		execution := m303Allowed("max-fund-lifecycle")
		maxText := strings.Repeat("界", 4000)
		create := m303Dispatch(
			t,
			dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsCreate,
			WorkbenchFundCreateArguments{
				CandidateID: fixture.candidate.ID,
				AmountMinor: 1,
				Currency:    "CNY",
				Condition:   maxText,
				PromisedBy:  strings.Repeat("p", 160),
				Evidence:    maxText,
			},
		)
		m303AssertSuccessEnvelope(t, create, false)
		m303AssertEnvelopeSize(t, create)
		var created workbenchGovernanceFundCreateResult
		m303Decode(t, create.Data, &created)

		awaitingCommitment := FundCallbackStatusAwaitingCommitment
		changedText := strings.Repeat("改", 4000)
		update := m303Dispatch(
			t,
			dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsUpdate,
			WorkbenchFundUpdateArguments{
				FundID:          created.ID,
				ExpectedVersion: created.Version,
				Condition:       &changedText,
				Evidence:        &changedText,
				Status:          &awaitingCommitment,
			},
		)
		m303AssertSuccessEnvelope(t, update, false)
		m303AssertEnvelopeSize(t, update)
		var updated workbenchGovernanceFundUpdateResult
		m303Decode(t, update.Data, &updated)

		commitment := m303Dispatch(
			t,
			dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmCommitment,
			WorkbenchVersionedIDArguments{
				ID: updated.ID, ExpectedVersion: updated.Version,
			},
		)
		m303AssertSuccessEnvelope(t, commitment, false)
		m303AssertEnvelopeSize(t, commitment)
		var committed workbenchGovernanceFundCommitmentResult
		m303Decode(t, commitment.Data, &committed)

		awaitingFinal := FundCallbackStatusAwaitingFinal
		update = m303Dispatch(
			t,
			dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsUpdate,
			WorkbenchFundUpdateArguments{
				FundID:          committed.ID,
				ExpectedVersion: committed.Version,
				Status:          &awaitingFinal,
			},
		)
		m303AssertSuccessEnvelope(t, update, false)
		m303AssertEnvelopeSize(t, update)
		m303Decode(t, update.Data, &updated)

		final := m303Dispatch(
			t,
			dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmFinal,
			WorkbenchFundFinalArguments{
				FundID:          updated.ID,
				ExpectedVersion: updated.Version,
				FinalResult:     maxText,
			},
		)
		m303AssertSuccessEnvelope(t, final, false)
		m303AssertEnvelopeSize(t, final)
		stored, err := crmFundCallbackGet(fixture.store, updated.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != FundCallbackStatusCompleted ||
			stored.FinalResult != maxText ||
			stored.Version != updated.Version+1 {
			t.Fatalf("maximum fund lifecycle state = %+v", stored)
		}
	})
}

func TestWorkbenchGovernanceMaximumEscapedMutationResultsStayWithinEnvelope(t *testing.T) {
	t.Run("candidate terminal", func(t *testing.T) {
		store := m303OpenStore(t)
		candidate := m303MaximumEscapedCandidate(t, store)
		before := store.Snapshot()
		result := m303Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
			m303Allowed("max-escaped-candidate"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationCandidatesTerminal,
			WorkbenchCandidateTerminalArguments{
				CandidateID: candidate.ID, ExpectedVersion: candidate.Version, Status: CandidateStatusPlaced,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertEnvelopeSize(t, result)
		m303AssertJSONKeys(t, result.Data, m303CandidateTerminalKeys()...)

		var projected workbenchGovernanceCandidateTerminalResult
		m303Decode(t, result.Data, &projected)
		detail, err := crmCandidateGetDetail(store, candidate.ID)
		if err != nil {
			t.Fatal(err)
		}
		stored := detail.Candidate
		if projected.ID != stored.ID ||
			projected.Status != stored.Status ||
			projected.Version != stored.Version ||
			!projected.UpdatedAt.Equal(stored.UpdatedAt) ||
			stored.Status != CandidateStatusPlaced ||
			stored.Version != candidate.Version+1 {
			t.Fatalf("candidate commit/result mismatch:\nstored=%+v\nprojected=%+v", stored, projected)
		}
		if !reflect.DeepEqual(projected.Skills, candidate.Skills) ||
			!reflect.DeepEqual(stored.Skills, candidate.Skills) ||
			stored.Summary != candidate.Summary ||
			projected.SummaryLength != len([]rune(candidate.Summary)) ||
			!projected.SummaryTruncated ||
			projected.Summary == candidate.Summary ||
			!strings.HasPrefix(candidate.Summary, projected.Summary) {
			t.Fatalf("candidate bounded projection mismatch:\nstored=%+v\nprojected=%+v", stored, projected)
		}
		after := store.Snapshot()
		if m303AuditOperationCount(after, "candidate.transition_terminal") !=
			m303AuditOperationCount(before, "candidate.transition_terminal")+1 {
			t.Fatal("candidate terminal audit was not committed exactly once")
		}
	})

	t.Run("job compensation", func(t *testing.T) {
		store := m303OpenStore(t)
		job := m303MaximumJob(t, store)
		originalSummary := m303EscapedText(2000, 40)
		accommodation := m303EscapedText(2000, 41)
		transport := m303EscapedText(2000, 42)
		seeded, err := crmPipelineUpdateJob(store, job.ID, JobUpdate{
			ExpectedVersion:       job.Version,
			CompensationSummary:   &originalSummary,
			Accommodation:         &accommodation,
			TransportRequirements: &transport,
		})
		if err != nil {
			t.Fatal(err)
		}
		before := store.Snapshot()
		updatedSummary := m303EscapedText(2000, 43)
		result := m303Dispatch(
			t,
			NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
			m303Allowed("max-escaped-compensation"),
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsUpdateCompensation,
			WorkbenchJobCompensationArguments{
				JobID:               seeded.ID,
				ExpectedVersion:     seeded.Version,
				CompensationSummary: &updatedSummary,
			},
		)
		m303AssertSuccessEnvelope(t, result, false)
		m303AssertEnvelopeSize(t, result)
		m303AssertJSONKeys(t, result.Data, m303JobCompensationKeys()...)

		var projected workbenchGovernanceJobCompensationResult
		m303Decode(t, result.Data, &projected)
		stored, err := crmPipelineStoredJob(store, seeded.ID)
		if err != nil {
			t.Fatal(err)
		}
		if projected.ID != stored.ID ||
			projected.Version != stored.Version ||
			!projected.UpdatedAt.Equal(stored.UpdatedAt) ||
			stored.CompensationSummary != updatedSummary ||
			stored.Accommodation != accommodation ||
			stored.TransportRequirements != transport ||
			stored.Version != seeded.Version+1 {
			t.Fatalf("compensation commit/result mismatch:\nstored=%+v\nprojected=%+v", stored, projected)
		}
		m303AssertBoundedMutationText(
			t,
			"compensation_summary",
			updatedSummary,
			projected.CompensationSummary,
			projected.CompensationSummaryLength,
			projected.CompensationSummaryTruncated,
		)
		m303AssertBoundedMutationText(
			t,
			"accommodation",
			accommodation,
			projected.Accommodation,
			projected.AccommodationLength,
			projected.AccommodationTruncated,
		)
		m303AssertBoundedMutationText(
			t,
			"transport_requirements",
			transport,
			projected.TransportRequirements,
			projected.TransportRequirementsLength,
			projected.TransportRequirementsTruncated,
		)
		after := store.Snapshot()
		if m303AuditOperationCount(after, "job.update_compensation") !=
			m303AuditOperationCount(before, "job.update_compensation")+1 {
			t.Fatal("job compensation audit was not committed exactly once")
		}
	})
}

func TestWorkbenchGovernanceR3RequiresFreshHumanAllowOnce(t *testing.T) {
	compensation := "hourly"
	condition := "updated condition"
	operations := []struct {
		operation WorkbenchOperation
		arguments any
	}{
		{
			WorkbenchOperationCandidatesTerminal,
			WorkbenchCandidateTerminalArguments{
				CandidateID: "cand_00000001", ExpectedVersion: 1, Status: CandidateStatusPlaced,
			},
		},
		{
			WorkbenchOperationCandidatesAnonymize,
			WorkbenchVersionedIDArguments{ID: "cand_00000001", ExpectedVersion: 1},
		},
		{
			WorkbenchOperationJobsTerminal,
			WorkbenchJobTerminalArguments{
				JobID: "job_00000001", ExpectedVersion: 1, Status: JobStatusClosed,
			},
		},
		{
			WorkbenchOperationJobsUpdateCompensation,
			WorkbenchJobCompensationArguments{
				JobID: "job_00000001", ExpectedVersion: 1, CompensationSummary: &compensation,
			},
		},
		{
			WorkbenchOperationRevisionsActivate,
			WorkbenchRevisionActivateArguments{
				JobID: "job_00000001", RevisionID: "demand_00000001", ExpectedVersion: 1,
			},
		},
		{
			WorkbenchOperationFundsCreate,
			WorkbenchFundCreateArguments{
				CandidateID: "cand_00000001",
				AmountMinor: 1,
				Currency:    "CNY",
				Condition:   "condition",
				PromisedBy:  "customer",
			},
		},
		{
			WorkbenchOperationFundsUpdate,
			WorkbenchFundUpdateArguments{
				FundID: "fund_00000001", ExpectedVersion: 1, Condition: &condition,
			},
		},
		{
			WorkbenchOperationFundsConfirmCommitment,
			WorkbenchVersionedIDArguments{ID: "fund_00000001", ExpectedVersion: 1},
		},
		{
			WorkbenchOperationFundsConfirmFinal,
			WorkbenchFundFinalArguments{
				FundID: "fund_00000001", ExpectedVersion: 1, FinalResult: "done",
			},
		},
	}
	contexts := []struct {
		name      string
		execution WorkbenchExecutionContext
		code      WorkbenchErrorCode
	}{
		{"missing approval", WorkbenchExecutionContext{OperatorID: "operator"}, WorkbenchErrorAllowOnceRequired},
		{
			"empty operator",
			WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowOnce,
				OperatorID:         " ",
			},
			WorkbenchErrorForbidden,
		},
		{
			"deny once",
			WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionDenyOnce,
				OperatorID:         "operator",
			},
			WorkbenchErrorForbidden,
		},
		{
			"allow run",
			WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowRun,
				OperatorID:         "operator",
			},
			WorkbenchErrorAllowOnceRequired,
		},
		{
			"allow persistent",
			WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionAllowPersistent,
				OperatorID:         "operator",
			},
			WorkbenchErrorAllowOnceRequired,
		},
		{
			"deny persistent",
			WorkbenchExecutionContext{
				PermissionDecision: AutomationPermissionDenyPersistent,
				OperatorID:         "operator",
			},
			WorkbenchErrorAllowOnceRequired,
		},
	}

	for _, operation := range operations {
		t.Run(string(operation.operation), func(t *testing.T) {
			store := m303OpenStore(t)
			agent := &task003AgentClient{}
			dispatcher := NewDefaultWorkbenchToolDispatcher(store, agent)
			before := store.Snapshot()
			wantDecisions := []AutomationPermissionDecision{
				AutomationPermissionAllowOnce,
				AutomationPermissionDenyOnce,
			}
			if decisions := WorkbenchValidPermissionDecisions(
				AutomationToolWorkbenchAllowOnce,
				operation.operation,
			); !reflect.DeepEqual(decisions, wantDecisions) {
				t.Fatalf("valid decisions = %v, want %v", decisions, wantDecisions)
			}
			for _, permission := range contexts {
				result := m303Dispatch(
					t,
					dispatcher,
					permission.execution,
					AutomationToolWorkbenchAllowOnce,
					operation.operation,
					operation.arguments,
				)
				m303AssertFailure(t, result, permission.code)
				if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
					t.Fatalf("%s mutated CRM state for %s", permission.name, operation.operation)
				}
			}
			agent.mu.Lock()
			calls := append([]string(nil), agent.calls...)
			agent.mu.Unlock()
			if len(calls) != 0 {
				t.Fatalf("rejected %s called agent: %v", operation.operation, calls)
			}
		})
	}
}

func TestWorkbenchGovernancePermissionDenyStopsMutation(t *testing.T) {
	applyWorkbenchOperationDenySet(map[string]struct{}{})
	t.Cleanup(func() { applyWorkbenchOperationDenySet(map[string]struct{}{}) })

	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	agent := &task003AgentClient{}
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, agent)
	before := store.Snapshot()
	applyWorkbenchOperationDenySet(map[string]struct{}{
		workbenchOperationDenyKey(
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsCreate,
		): {},
	})
	result := m303Dispatch(
		t,
		dispatcher,
		m303Allowed("operator"),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		WorkbenchFundCreateArguments{
			CandidateID: candidate.ID,
			AmountMinor: 1,
			Currency:    "CNY",
			Condition:   "condition",
			PromisedBy:  "customer",
		},
	)
	m303AssertFailure(t, result, WorkbenchErrorUnavailable)
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("operator permission deny mutated CRM state")
	}
	agent.mu.Lock()
	calls := append([]string(nil), agent.calls...)
	agent.mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("operator permission deny called agent: %v", calls)
	}
}

func TestWorkbenchGovernanceNilCRMValidationNotFoundAndConflict(t *testing.T) {
	compensation := "hourly"
	condition := "updated"
	operations := []struct {
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments any
		execution WorkbenchExecutionContext
	}{
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsList,
			WorkbenchFundListArguments{Limit: 1, Offset: 0},
			WorkbenchExecutionContext{},
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsGet,
			WorkbenchIDArguments{ID: "fund_00000001"},
			WorkbenchExecutionContext{},
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsLedger,
			WorkbenchFundLedgerArguments{ID: "fund_00000001", Limit: 1, Offset: 0},
			WorkbenchExecutionContext{},
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationCandidatesTerminal,
			WorkbenchCandidateTerminalArguments{
				CandidateID: "cand_00000001", ExpectedVersion: 1, Status: CandidateStatusPlaced,
			},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationCandidatesAnonymize,
			WorkbenchVersionedIDArguments{ID: "cand_00000001", ExpectedVersion: 1},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsTerminal,
			WorkbenchJobTerminalArguments{
				JobID: "job_00000001", ExpectedVersion: 1, Status: JobStatusClosed,
			},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsUpdateCompensation,
			WorkbenchJobCompensationArguments{
				JobID: "job_00000001", ExpectedVersion: 1, CompensationSummary: &compensation,
			},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationRevisionsActivate,
			WorkbenchRevisionActivateArguments{
				JobID: "job_00000001", RevisionID: "demand_00000001", ExpectedVersion: 1,
			},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsCreate,
			WorkbenchFundCreateArguments{
				CandidateID: "cand_00000001",
				AmountMinor: 1,
				Currency:    "CNY",
				Condition:   "condition",
				PromisedBy:  "customer",
			},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsUpdate,
			WorkbenchFundUpdateArguments{
				FundID: "fund_00000001", ExpectedVersion: 1, Condition: &condition,
			},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmCommitment,
			WorkbenchVersionedIDArguments{ID: "fund_00000001", ExpectedVersion: 1},
			m303Allowed("operator"),
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmFinal,
			WorkbenchFundFinalArguments{
				FundID: "fund_00000001", ExpectedVersion: 1, FinalResult: "done",
			},
			m303Allowed("operator"),
		},
	}

	nilDispatcher := NewDefaultWorkbenchToolDispatcherWithDependencies(WorkbenchToolDependencies{})
	for _, operation := range operations {
		t.Run("nil CRM "+string(operation.operation), func(t *testing.T) {
			result := m303Dispatch(
				t,
				nilDispatcher,
				operation.execution,
				operation.tool,
				operation.operation,
				operation.arguments,
			)
			m303AssertFailure(t, result, WorkbenchErrorUnavailable)
			if !result.Error.Retryable {
				t.Fatalf("nil CRM result is not retryable: %+v", result)
			}
		})
	}

	store := m303OpenStore(t)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	for _, operation := range operations {
		if operation.operation == WorkbenchOperationFundsList {
			continue
		}
		t.Run("empty CRM "+string(operation.operation), func(t *testing.T) {
			result := m303Dispatch(
				t,
				dispatcher,
				operation.execution,
				operation.tool,
				operation.operation,
				operation.arguments,
			)
			if operation.operation == WorkbenchOperationFundsCreate {
				m303AssertFailure(t, result, WorkbenchErrorInvalidArguments)
				return
			}
			m303AssertFailure(t, result, WorkbenchErrorNotFound)
		})
	}

	candidate := m303Candidate(t, store)
	before := store.Snapshot()
	stale := m303Dispatch(
		t,
		dispatcher,
		m303Allowed("operator"),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationCandidatesTerminal,
		WorkbenchCandidateTerminalArguments{
			CandidateID:     candidate.ID,
			ExpectedVersion: candidate.Version + 1,
			Status:          CandidateStatusPlaced,
		},
	)
	m303AssertFailure(t, stale, WorkbenchErrorConflict)
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("stale candidate terminal action mutated state or audit")
	}
}

func TestWorkbenchGovernanceRejectsUnknownFieldsAndBoundaryValidation(t *testing.T) {
	tests := []struct {
		tool      AutomationToolName
		operation WorkbenchOperation
		arguments string
	}{
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsList,
			`{"limit":1,"offset":0,"unexpected":true}`,
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsGet,
			`{"id":"fund_00000001","unexpected":true}`,
		},
		{
			AutomationToolWorkbenchQuery,
			WorkbenchOperationFundsLedger,
			`{"id":"fund_00000001","limit":1,"offset":0,"unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationCandidatesTerminal,
			`{"candidate_id":"cand_00000001","expected_version":1,"status":"placed","unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationCandidatesAnonymize,
			`{"id":"cand_00000001","expected_version":1,"unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsTerminal,
			`{"job_id":"job_00000001","expected_version":1,"status":"closed","unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationJobsUpdateCompensation,
			`{"job_id":"job_00000001","expected_version":1,"compensation_summary":"hourly","unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationRevisionsActivate,
			`{"job_id":"job_00000001","revision_id":"demand_00000001","expected_version":1,"unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsCreate,
			`{"candidate_id":"cand_00000001","amount_minor":1,"currency":"CNY","condition":"c","promised_by":"p","unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsUpdate,
			`{"fund_id":"fund_00000001","expected_version":1,"condition":"c","unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmCommitment,
			`{"id":"fund_00000001","expected_version":1,"unexpected":true}`,
		},
		{
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsConfirmFinal,
			`{"fund_id":"fund_00000001","expected_version":1,"final_result":"done","unexpected":true}`,
		},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			raw, err := json.Marshal(WorkbenchToolRequest{
				Operation: test.operation, ArgumentsJSON: test.arguments,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeWorkbenchToolRequest(test.tool, raw); err == nil {
				t.Fatal("decoder accepted an unknown argument field")
			}
		})
	}

	for _, arguments := range []string{
		`{"limit":101,"offset":0}`,
		`{"limit":1,"offset":-1}`,
		`{"limit":1,"offset":0,"status":"unknown"}`,
	} {
		raw, err := json.Marshal(WorkbenchToolRequest{
			Operation: WorkbenchOperationFundsList, ArgumentsJSON: arguments,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw); err == nil {
			t.Fatalf("funds.list accepted invalid boundary arguments %s", arguments)
		}
	}
	for _, arguments := range []string{
		`{"id":"fund_00000001","limit":101,"offset":0}`,
		`{"id":"fund_00000001","limit":1,"offset":-1}`,
		`{"id":"","limit":1,"offset":0}`,
	} {
		raw, err := json.Marshal(WorkbenchToolRequest{
			Operation: WorkbenchOperationFundsLedger, ArgumentsJSON: arguments,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeWorkbenchToolRequest(AutomationToolWorkbenchQuery, raw); err == nil {
			t.Fatalf("funds.ledger accepted invalid boundary arguments %s", arguments)
		}
	}

	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	invalidCurrency := m303Dispatch(
		t,
		dispatcher,
		m303Allowed("operator"),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		WorkbenchFundCreateArguments{
			CandidateID: candidate.ID,
			AmountMinor: 1,
			Currency:    "US",
			Condition:   "condition",
			PromisedBy:  "customer",
		},
	)
	m303AssertFailure(t, invalidCurrency, WorkbenchErrorInvalidArguments)
	if len(store.Snapshot().FundCallbacks) != 0 {
		t.Fatal("invalid funds.create persisted a callback")
	}
}

func TestWorkbenchGovernanceConcurrentDuplicateExecutionAppliesOnce(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	decoded := m303Decoded(
		t,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationCandidatesTerminal,
		WorkbenchCandidateTerminalArguments{
			CandidateID:     candidate.ID,
			ExpectedVersion: candidate.Version,
			Status:          CandidateStatusPlaced,
		},
	)
	before := store.Snapshot()
	start := make(chan struct{})
	results := make(chan WorkbenchToolResult, 2)
	var workers sync.WaitGroup
	for index := 0; index < 2; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- dispatcher.Dispatch(
				context.Background(),
				m303Allowed("concurrent-operator"),
				decoded,
			)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for result := range results {
		switch {
		case result.OK:
			successes++
		case result.Error != nil && result.Error.Code == WorkbenchErrorConflict:
			conflicts++
		default:
			t.Fatalf("concurrent duplicate result = %+v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent results successes=%d conflicts=%d", successes, conflicts)
	}
	after := store.Snapshot()
	if len(after.Candidates) != 1 ||
		after.Candidates[0].Status != CandidateStatusPlaced ||
		after.Candidates[0].Version != candidate.Version+1 ||
		len(after.AuditEvents) != len(before.AuditEvents)+1 {
		t.Fatalf(
			"concurrent duplicate state candidate=%+v audits before=%d after=%d",
			after.Candidates,
			len(before.AuditEvents),
			len(after.AuditEvents),
		)
	}
	lastAudit := after.AuditEvents[len(after.AuditEvents)-1]
	if lastAudit.Operation != "candidate.transition_terminal" ||
		lastAudit.Actor != "concurrent-operator" {
		t.Fatalf("concurrent terminal audit = %+v", lastAudit)
	}
}

func TestWorkbenchGovernanceFundsCreateRequiresTrustedApprovedActionBinding(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	arguments := WorkbenchFundCreateArguments{
		CandidateID: candidate.ID,
		AmountMinor: 1,
		Currency:    "CNY",
		Condition:   "condition",
		PromisedBy:  "customer",
	}
	contexts := []WorkbenchExecutionContext{
		{
			PermissionDecision: AutomationPermissionAllowOnce,
			OperatorID:         "operator",
		},
		{
			ActionID:           "aiact_00000001",
			PermissionDecision: AutomationPermissionAllowOnce,
			OperatorID:         "operator",
		},
		{
			PermissionRequestID: "aipermreq_00000001",
			PermissionDecision:  AutomationPermissionAllowOnce,
			OperatorID:          "operator",
		},
	}
	for index, execution := range contexts {
		before := store.Snapshot()
		result := m303Dispatch(
			t,
			dispatcher,
			execution,
			AutomationToolWorkbenchAllowOnce,
			WorkbenchOperationFundsCreate,
			arguments,
		)
		m303AssertFailure(t, result, WorkbenchErrorForbidden)
		if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Fatalf("missing approved action binding case %d mutated CRM state", index)
		}
	}
}

func TestWorkbenchGovernanceFundsCreateSequentialReplaySurvivesReopen(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	execution := m303Allowed("fund-replay")
	arguments := WorkbenchFundCreateArguments{
		CandidateID: candidate.ID,
		AmountMinor: 12500,
		Currency:    "CNY",
		Condition:   "approved callback",
		PromisedBy:  "customer",
	}
	before := store.Snapshot()
	first := m303Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertSuccessEnvelope(t, first, false)
	afterFirst := store.Snapshot()
	m303AssertSingleFundCreateDelta(t, before, afterFirst, execution)

	second := m303Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("sequential funds.create replay changed result:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if afterSecond := store.Snapshot(); !reflect.DeepEqual(afterSecond, afterFirst) {
		t.Fatal("sequential funds.create replay mutated CRM state")
	}

	path := store.path
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	beforeReopenReplay := reopened.Snapshot()
	third := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(reopened, &task003AgentClient{}),
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	if !reflect.DeepEqual(third, first) {
		t.Fatalf("reopened funds.create replay changed result:\nfirst=%+v\nthird=%+v", first, third)
	}
	if afterReopenReplay := reopened.Snapshot(); !reflect.DeepEqual(afterReopenReplay, beforeReopenReplay) {
		t.Fatal("reopened funds.create replay mutated CRM state")
	}
}

func TestWorkbenchGovernanceFundsCreateUpdateReplayReturnsOriginalResult(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	execution := m303Allowed("fund-update-replay")
	arguments := WorkbenchFundCreateArguments{
		CandidateID: candidate.ID,
		AmountMinor: 12500,
		Currency:    "CNY",
		Condition:   "approved callback",
		PromisedBy:  "customer",
	}
	first := m303Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertSuccessEnvelope(t, first, false)
	m303UpdateCreatedFund(t, store, first)
	beforeReplay := store.Snapshot()

	replayed := m303Dispatch(
		t,
		dispatcher,
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertByteEquivalentToolResult(t, first, replayed)
	m303AssertFundCreateReplayNoSideEffects(t, beforeReplay, store.Snapshot())
}

func TestWorkbenchGovernanceFundsCreateUpdateReopenReplayReturnsOriginalResult(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	execution := m303Allowed("fund-update-reopen-replay")
	arguments := WorkbenchFundCreateArguments{
		CandidateID: candidate.ID,
		AmountMinor: 12500,
		Currency:    "CNY",
		Condition:   "approved callback",
		PromisedBy:  "customer",
	}
	first := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertSuccessEnvelope(t, first, false)
	m303UpdateCreatedFund(t, store, first)

	path := store.path
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	beforeReplay := reopened.Snapshot()
	replayed := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(reopened, &task003AgentClient{}),
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertByteEquivalentToolResult(t, first, replayed)
	m303AssertFundCreateReplayNoSideEffects(t, beforeReplay, reopened.Snapshot())
}

func TestWorkbenchGovernanceFundsCreateBindingSnapshotCannotDrift(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	execution := m303Allowed("fund-binding-snapshot")
	result := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		WorkbenchFundCreateArguments{
			CandidateID: candidate.ID,
			AmountMinor: 12500,
			Currency:    "CNY",
			Condition:   "approved callback",
			PromisedBy:  "customer",
		},
	)
	m303AssertSuccessEnvelope(t, result, false)
	reassigned, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Candidate Drift",
		WeChatID: "wxid_m303_candidate_drift",
		Skills:   []string{},
		Status:   CandidateStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()

	err = store.UpdateWithMeta(CRMUpdateMeta{
		Actor:     "snapshot-drift",
		Operation: "fund_callback.update",
	}, func(state *CRMState, _ *IDAllocator) error {
		binding := &state.FundCallbackApprovedActions[0]
		binding.OriginalResult.CandidateID = reassigned.ID
		digest, err := crmFundCallbackApprovedActionResultSHA256(*binding.OriginalResult)
		if err != nil {
			return err
		}
		binding.OriginalResultSHA256 = digest
		return nil
	})
	if err == nil || !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("binding snapshot drift error = %v, want validation error", err)
	}
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatal("rejected binding snapshot drift mutated CRM state")
	}
}

func TestWorkbenchGovernanceFundsCreateBindingRequiresOriginalResultAndDigest(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	result := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
		m303Allowed("fund-binding-required"),
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		WorkbenchFundCreateArguments{
			CandidateID: candidate.ID,
			AmountMinor: 12500,
			Currency:    "CNY",
			Condition:   "approved callback",
			PromisedBy:  "customer",
		},
	)
	m303AssertSuccessEnvelope(t, result, false)
	valid := store.Snapshot()

	tests := []struct {
		name   string
		mutate func(*FundCallbackApprovedActionBinding)
	}{
		{
			name: "missing original result",
			mutate: func(binding *FundCallbackApprovedActionBinding) {
				binding.OriginalResult = nil
			},
		},
		{
			name: "missing original result digest",
			mutate: func(binding *FundCallbackApprovedActionBinding) {
				binding.OriginalResultSHA256 = ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := cloneCRMState(valid)
			test.mutate(&state.FundCallbackApprovedActions[0])
			if err := ValidateCRMState(&state); err == nil || !errors.Is(err, ErrCRMValidation) {
				t.Fatalf("missing binding snapshot validation error = %v", err)
			}
		})
	}
}

func TestWorkbenchGovernanceFundsCreateBindingsAreAppendOnlyImmutableAndAtomic(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	execution := m303Allowed("fund-binding-immutable")
	arguments := WorkbenchFundCreateArguments{
		CandidateID: candidate.ID,
		AmountMinor: 12500,
		Currency:    "CNY",
		Condition:   "approved callback",
		PromisedBy:  "customer",
	}
	beforeCreate := store.Snapshot()
	first := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{}),
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertSuccessEnvelope(t, first, false)
	m303AssertSingleFundCreateDelta(t, beforeCreate, store.Snapshot(), execution)

	unbound, err := crmFundCallbackCreate(store, FundCallbackCreate{
		CandidateID: candidate.ID,
		AmountMinor: 22000,
		Currency:    "EUR",
		Condition:   "unbound callback",
		PromisedBy:  "customer",
		Actor:       "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	unboundResult := crmFundCallbackApprovedActionResult(unbound)
	unboundDigest, err := crmFundCallbackApprovedActionResultSHA256(unboundResult)
	if err != nil {
		t.Fatal(err)
	}

	path := store.path
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	tests := []struct {
		name   string
		mutate func(*CRMState) error
	}{
		{
			name: "delete binding",
			mutate: func(state *CRMState) error {
				state.FundCallbackApprovedActions = state.FundCallbackApprovedActions[:0]
				return nil
			},
		},
		{
			name: "change action id",
			mutate: func(state *CRMState) error {
				state.FundCallbackApprovedActions[0].ActionID = "aiact_m303_changed"
				return nil
			},
		},
		{
			name: "change permission request id",
			mutate: func(state *CRMState) error {
				state.FundCallbackApprovedActions[0].PermissionRequestID = "aipermreq_m303_changed"
				return nil
			},
		},
		{
			name: "change fund callback id",
			mutate: func(state *CRMState) error {
				result := unboundResult
				binding := &state.FundCallbackApprovedActions[0]
				binding.FundCallbackID = unbound.ID
				binding.OriginalResult = &result
				binding.OriginalResultSHA256 = unboundDigest
				return nil
			},
		},
		{
			name: "change original result",
			mutate: func(state *CRMState) error {
				binding := &state.FundCallbackApprovedActions[0]
				binding.OriginalResult.AmountMinor++
				digest, err := crmFundCallbackApprovedActionResultSHA256(*binding.OriginalResult)
				if err != nil {
					return err
				}
				binding.OriginalResultSHA256 = digest
				return nil
			},
		},
		{
			name: "change original result digest",
			mutate: func(state *CRMState) error {
				state.FundCallbackApprovedActions[0].OriginalResultSHA256 = strings.Repeat("0", 64)
				return nil
			},
		},
		{
			name: "append binding for existing callback",
			mutate: func(state *CRMState) error {
				result := unboundResult
				state.FundCallbackApprovedActions = append(
					state.FundCallbackApprovedActions,
					FundCallbackApprovedActionBinding{
						ActionID:             "aiact_m303_existing_callback",
						PermissionRequestID:  "aipermreq_m303_existing_callback",
						FundCallbackID:       unbound.ID,
						OriginalResult:       &result,
						OriginalResultSHA256: unboundDigest,
					},
				)
				return nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := reopened.Snapshot()
			err := reopened.UpdateWithMeta(CRMUpdateMeta{
				Actor:     "binding-mutator",
				Operation: "fund_callback.binding_mutation",
			}, func(state *CRMState, _ *IDAllocator) error {
				return test.mutate(state)
			})
			if err == nil || !errors.Is(err, ErrCRMValidation) {
				t.Fatalf("binding mutation error = %v, want validation error", err)
			}
			if after := reopened.Snapshot(); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s changed CRM state", test.name)
			}
		})
	}

	beforeReplay := reopened.Snapshot()
	replayed := m303Dispatch(
		t,
		NewDefaultWorkbenchToolDispatcher(reopened, &task003AgentClient{}),
		execution,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)
	m303AssertByteEquivalentToolResult(t, first, replayed)
	m303AssertFundCreateReplayNoSideEffects(t, beforeReplay, reopened.Snapshot())
}

func TestWorkbenchGovernanceFundsCreateConcurrentReplayAppliesOnce(t *testing.T) {
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	dispatcher := NewDefaultWorkbenchToolDispatcher(store, &task003AgentClient{})
	execution := m303Allowed("fund-concurrent-replay")
	decoded := m303Decoded(
		t,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		WorkbenchFundCreateArguments{
			CandidateID: candidate.ID,
			AmountMinor: 12500,
			Currency:    "CNY",
			Condition:   "approved callback",
			PromisedBy:  "customer",
		},
	)
	before := store.Snapshot()
	start := make(chan struct{})
	results := make(chan WorkbenchToolResult, 2)
	var workers sync.WaitGroup
	for index := 0; index < 2; index++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- dispatcher.Dispatch(context.Background(), execution, decoded)
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var replayed []WorkbenchToolResult
	for result := range results {
		m303AssertSuccessEnvelope(t, result, false)
		replayed = append(replayed, result)
	}
	if len(replayed) != 2 || !reflect.DeepEqual(replayed[0], replayed[1]) {
		t.Fatalf("concurrent funds.create replay results = %+v", replayed)
	}
	m303AssertSingleFundCreateDelta(t, before, store.Snapshot(), execution)
}

func TestWorkbenchGovernanceFundsCreateExecutorReplayAppliesOnce(t *testing.T) {
	crm := m303OpenStore(t)
	candidate := m303Candidate(t, crm)
	event := crmCandidateTestAddPrivateEvent(
		t,
		crm,
		"wxid_m303_executor",
		"wxid_m303_executor",
	)
	arguments := WorkbenchFundCreateArguments{
		CandidateID: candidate.ID,
		AmountMinor: 12500,
		Currency:    "CNY",
		Condition:   "executor approved callback",
		PromisedBy:  "customer",
	}
	decoded := m303Decoded(
		t,
		AutomationToolWorkbenchAllowOnce,
		WorkbenchOperationFundsCreate,
		arguments,
	)

	automation, err := OpenAutomationStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = automation.Close() })
	state := automationStoreTestStateWithoutOutbox()
	state.Runs[0].SourceMessageEventID = event.ID
	state.Runs[0].SourceMessageKey = event.MessageKey
	state.Runs[0].SourceMessageVersion = event.Version
	action := &state.Actions[0]
	action.Tool = AutomationToolWorkbenchAllowOnce
	action.Operation = WorkbenchOperationFundsCreate
	action.Risk = AutomationRiskR3
	action.PolicyDecision = AutomationDecisionReview
	action.Recipient = nil
	action.Payload = append(json.RawMessage(nil), decoded.CanonicalRequest...)
	action.SourceMessage = AutomationSourceMessageRef{
		EventID:         event.ID,
		MessageKey:      event.MessageKey,
		ExpectedVersion: event.Version,
		ChatType:        event.ChatType,
		PeerID:          event.PeerID,
	}
	action.Result = json.RawMessage(`{"replayed":true}`)
	state.Results[0].Data = append(json.RawMessage(nil), action.Result...)
	request := &state.PermissionRequests[0]
	request.Tool = action.Tool
	request.Operation = action.Operation
	request.Risk = action.Risk
	request.Status = AutomationPermissionStatusAllowed
	request.Decision = AutomationPermissionAllowOnce
	request.OperatorID = "executor-operator"
	request.RuleID = ""
	if err := automation.Update(func(current *AutomationState, _ *AutomationIDAllocator) error {
		*current = state
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	persisted := automation.Snapshot().Actions[0]
	executor, ok := NewDefaultAutomationCommandExecutor(
		automation,
		crm,
		nil,
	).(*defaultAutomationCommandExecutor)
	if !ok {
		t.Fatal("default automation executor has unexpected type")
	}
	before := crm.Snapshot()
	firstExecution, firstRaw, err := executor.executeWorkbenchAction(context.Background(), persisted)
	if err != nil {
		t.Fatal(err)
	}
	secondExecution, secondRaw, err := executor.executeWorkbenchAction(context.Background(), persisted)
	if err != nil {
		t.Fatal(err)
	}
	if firstExecution.Status != AutomationActionSucceeded ||
		secondExecution.Status != AutomationActionSucceeded ||
		!reflect.DeepEqual(firstRaw, secondRaw) {
		t.Fatalf(
			"executor replay first=%+v second=%+v raw_equal=%v",
			firstExecution,
			secondExecution,
			reflect.DeepEqual(firstRaw, secondRaw),
		)
	}
	var result WorkbenchToolResult
	if err := json.Unmarshal(firstRaw, &result); err != nil {
		t.Fatal(err)
	}
	m303AssertSuccessEnvelope(t, result, false)
	execution := WorkbenchExecutionContext{
		ActionID:            persisted.ID,
		PermissionRequestID: request.ID,
		PermissionDecision:  AutomationPermissionAllowOnce,
		OperatorID:          request.OperatorID,
	}
	m303AssertSingleFundCreateDelta(t, before, crm.Snapshot(), execution)
}

type m303FundFixture struct {
	store      *CRMStore
	dispatcher WorkbenchToolDispatcher
	agent      *task003AgentClient
	candidate  Candidate
	job        Job
	lead       Lead
	onboarding OnboardingCase
}

func m303FundsFixture(t *testing.T) m303FundFixture {
	t.Helper()
	store := m303OpenStore(t)
	candidate := m303Candidate(t, store)
	job := m303Job(t, store)
	lead, err := crmPipelineCreateLead(store, LeadCreate{
		CandidateID: candidate.ID,
		JobID:       job.ID,
		Owner:       "fixture-owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []LeadStage{
		LeadStageScreening,
		LeadStageInterview,
		LeadStageOffer,
		LeadStageHired,
	} {
		lead, err = crmPipelineTransitionLead(store, lead.ID, LeadTransition{
			ExpectedVersion: lead.Version,
			FromStage:       lead.Stage,
			ToStage:         stage,
			Actor:           "fixture",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	onboardings := store.Snapshot().OnboardingCases
	if len(onboardings) != 1 || onboardings[0].LeadID != lead.ID {
		t.Fatalf("hired lead onboarding = %+v", onboardings)
	}
	onboarding := onboardings[0]
	agent := &task003AgentClient{}
	return m303FundFixture{
		store:      store,
		dispatcher: NewDefaultWorkbenchToolDispatcher(store, agent),
		agent:      agent,
		candidate:  candidate,
		job:        job,
		lead:       lead,
		onboarding: onboarding,
	}
}

func m303OpenStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(t.TempDir() + "/crm.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func m303Candidate(t *testing.T, store *CRMStore) Candidate {
	t.Helper()
	candidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Ada Lovelace",
		WeChatID: "wxid_m303",
		Phone:    "123456789",
		Email:    "ada@example.com",
		Location: "Berlin",
		Source:   "referral",
		Skills:   []string{"Go", "C"},
		Owner:    "fixture-owner",
		Status:   CandidateStatusActive,
		Summary:  "Fixture candidate",
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func m303MaximumEscapedCandidate(t *testing.T, store *CRMStore) Candidate {
	t.Helper()
	skills := make([]string, 32)
	for index := range skills {
		skills[index] = m303EscapedText(64, index+1)
	}
	candidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: m303EscapedText(120, 32),
		WeChatID: m303EscapedText(128, 33),
		Phone:    m303EscapedText(40, 34),
		Email:    m303EscapedText(254, 35),
		Location: m303EscapedText(160, 36),
		Source:   m303EscapedText(120, 37),
		Skills:   skills,
		Owner:    m303EscapedText(80, 38),
		Status:   CandidateStatusActive,
		Summary:  m303EscapedText(4000, 39),
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func m303Job(t *testing.T, store *CRMStore) Job {
	t.Helper()
	startAt := m303TestTime.Add(72 * time.Hour)
	deadlineAt := startAt.Add(7 * 24 * time.Hour)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:                 "Backend Engineer",
		Company:               "Example Manufacturing",
		Location:              "Berlin",
		EmploymentType:        "contract",
		Description:           "Fixture job",
		Requirements:          []string{"Go", "C"},
		DemandCode:            "M3-03",
		SiteName:              "Fixture site",
		OnsiteContact:         "Fixture contact",
		Headcount:             2,
		Priority:              JobPriorityHigh,
		StartAt:               &startAt,
		DeadlineAt:            &deadlineAt,
		Shift:                 "day",
		WorkSchedule:          "five days",
		CompensationSummary:   "hourly rate",
		Accommodation:         "shared housing",
		TransportRequirements: "shuttle",
		RequiredDocuments:     []string{"ID", "Passport"},
		Owner:                 "fixture-owner",
		Status:                JobStatusOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func m303MaximumJob(t *testing.T, store *CRMStore) Job {
	t.Helper()
	requirements := make([]string, 64)
	documents := make([]string, 64)
	for index := range requirements {
		requirements[index] = fmt.Sprintf("%03d:", index) + strings.Repeat("r", 996)
		documents[index] = fmt.Sprintf("%03d:", index) + strings.Repeat("d", 196)
	}
	startAt := m303TestTime.Add(72 * time.Hour)
	deadlineAt := startAt.Add(7 * 24 * time.Hour)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:                 strings.Repeat("t", 160),
		Company:               strings.Repeat("c", 160),
		Location:              strings.Repeat("l", 160),
		EmploymentType:        strings.Repeat("e", 80),
		Description:           strings.Repeat("d", 8000),
		Requirements:          requirements,
		DemandCode:            strings.Repeat("m", 120),
		SiteName:              strings.Repeat("s", 160),
		OnsiteContact:         strings.Repeat("o", 160),
		Headcount:             1000000,
		Priority:              JobPriorityHigh,
		StartAt:               &startAt,
		DeadlineAt:            &deadlineAt,
		Shift:                 strings.Repeat("h", 160),
		WorkSchedule:          strings.Repeat("w", 2000),
		CompensationSummary:   strings.Repeat("c", 2000),
		Accommodation:         strings.Repeat("a", 2000),
		TransportRequirements: strings.Repeat("v", 2000),
		RequiredDocuments:     documents,
		Owner:                 strings.Repeat("o", 80),
		Status:                JobStatusOpen,
	})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func m303EscapedText(length, seed int) string {
	characters := []rune{'<', '>', '&'}
	runes := make([]rune, length)
	for index := range runes {
		runes[index] = '<'
	}
	for index := 0; index < len(runes) && (index < 8 || seed > 0); index++ {
		runes[index] = characters[seed%len(characters)]
		seed /= len(characters)
	}
	return string(runes)
}

func m303AppendLedgerEntries(store *CRMStore, fundID string, count int) error {
	return store.UpdateWithMeta(CRMUpdateMeta{
		Actor: "fixture", Operation: "fund_callback.ledger_fixture",
	}, func(state *CRMState, allocator *IDAllocator) error {
		fundIndex := crmFundCallbackIndex(state.FundCallbacks, fundID)
		fund := state.FundCallbacks[fundIndex]
		for index := 0; index < count; index++ {
			id, err := allocator.Next(IDKindFundLedger)
			if err != nil {
				return err
			}
			state.FundCallbackLedger = append(state.FundCallbackLedger, FundCallbackLedgerEntry{
				ID:             id,
				FundCallbackID: fundID,
				Type:           "update",
				FromStatus:     fund.Status,
				ToStatus:       fund.Status,
				AmountMinor:    fund.AmountMinor,
				Currency:       fund.Currency,
				OperatorID:     strings.Repeat("o", 80),
				EvidenceSHA256: strings.Repeat("0", 64),
				CreatedAt:      fund.CreatedAt.Add(time.Duration(index+1) * time.Nanosecond),
			})
		}
		return nil
	})
}

func m303AssertEnvelopeSize(t *testing.T, result WorkbenchToolResult) {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > WorkbenchMaxResultJSONBytes {
		t.Fatalf("workbench result encoded to %d bytes", len(encoded))
	}
	if err := ValidateWorkbenchToolResult(result); err != nil {
		t.Fatalf("workbench result validation error = %v", err)
	}
}

func m303AssertBoundedMutationText(
	t *testing.T,
	field string,
	original string,
	projected string,
	length int,
	truncated bool,
) {
	t.Helper()
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if length != len([]rune(original)) ||
		!truncated ||
		projected == original ||
		!strings.HasPrefix(original, projected) ||
		len(encoded) > workbenchGovernanceMutationTextJSONBytes {
		t.Fatalf(
			"%s bounded projection length=%d truncated=%v encoded=%d projected_runes=%d original_runes=%d",
			field,
			length,
			truncated,
			len(encoded),
			len([]rune(projected)),
			len([]rune(original)),
		)
	}
}

func m303AssertSingleFundCreateDelta(
	t *testing.T,
	before CRMState,
	after CRMState,
	execution WorkbenchExecutionContext,
) {
	t.Helper()
	if len(after.FundCallbacks) != len(before.FundCallbacks)+1 ||
		len(after.FundCallbackLedger) != len(before.FundCallbackLedger)+1 ||
		len(after.FundCallbackApprovedActions) != len(before.FundCallbackApprovedActions)+1 ||
		m303AuditOperationCount(after, "fund_callback.create") !=
			m303AuditOperationCount(before, "fund_callback.create")+1 {
		t.Fatalf(
			"funds.create delta callbacks=%d/%d ledger=%d/%d bindings=%d/%d create_audits=%d/%d",
			len(after.FundCallbacks),
			len(before.FundCallbacks),
			len(after.FundCallbackLedger),
			len(before.FundCallbackLedger),
			len(after.FundCallbackApprovedActions),
			len(before.FundCallbackApprovedActions),
			m303AuditOperationCount(after, "fund_callback.create"),
			m303AuditOperationCount(before, "fund_callback.create"),
		)
	}
	binding := after.FundCallbackApprovedActions[len(after.FundCallbackApprovedActions)-1]
	callback := after.FundCallbacks[len(after.FundCallbacks)-1]
	ledger := after.FundCallbackLedger[len(after.FundCallbackLedger)-1]
	if binding.ActionID != execution.ActionID ||
		binding.PermissionRequestID != execution.PermissionRequestID ||
		binding.FundCallbackID != callback.ID ||
		binding.OriginalResult == nil ||
		ledger.FundCallbackID != callback.ID ||
		ledger.CandidateID != callback.CandidateID ||
		ledger.Type != "create" {
		t.Fatalf("funds.create binding=%+v callback=%+v ledger=%+v", binding, callback, ledger)
	}
	expectedResult := crmFundCallbackApprovedActionResult(callback)
	expectedSHA256, err := crmFundCallbackApprovedActionResultSHA256(expectedResult)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*binding.OriginalResult, expectedResult) ||
		binding.OriginalResultSHA256 != expectedSHA256 {
		t.Fatalf("funds.create original result binding = %+v", binding)
	}
}

func m303UpdateCreatedFund(t *testing.T, store *CRMStore, created WorkbenchToolResult) {
	t.Helper()
	var payload workbenchGovernanceFundCreateResult
	m303Decode(t, created.Data, &payload)
	reassigned, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Grace Hopper",
		WeChatID: "wxid_m303_reassigned_" + payload.ID,
		Skills:   []string{"Go"},
		Status:   CandidateStatusActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidateID := reassigned.ID
	amountMinor := payload.AmountMinor + 7500
	currency := "USD"
	status := FundCallbackStatusAwaitingCommitment
	updated, err := crmFundCallbackUpdate(store, payload.ID, FundCallbackUpdate{
		ExpectedVersion: payload.Version,
		CandidateID:     &candidateID,
		AmountMinor:     &amountMinor,
		Currency:        &currency,
		Status:          &status,
		Actor:           "fund-update",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AmountMinor != amountMinor ||
		updated.CandidateID != candidateID ||
		updated.CandidateID == payload.CandidateID ||
		updated.Currency != currency ||
		updated.Status != status ||
		updated.Version != payload.Version+1 ||
		updated.UpdatedAt.Equal(payload.UpdatedAt) {
		t.Fatalf("fund update did not change compact create fields: %+v", updated)
	}
}

func m303AssertByteEquivalentToolResult(
	t *testing.T,
	expected WorkbenchToolResult,
	actual WorkbenchToolResult,
) {
	t.Helper()
	expectedJSON, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualJSON, expectedJSON) {
		t.Fatalf(
			"tool results are not byte-equivalent:\nexpected=%s\nactual=%s",
			expectedJSON,
			actualJSON,
		)
	}
}

func m303AssertFundCreateReplayNoSideEffects(t *testing.T, before, after CRMState) {
	t.Helper()
	if len(after.FundCallbacks) != len(before.FundCallbacks) ||
		len(after.FundCallbackLedger) != len(before.FundCallbackLedger) ||
		len(after.FundCallbackApprovedActions) != len(before.FundCallbackApprovedActions) ||
		len(after.AuditEvents) != len(before.AuditEvents) {
		t.Fatalf(
			"funds.create replay side effects callbacks=%d/%d ledger=%d/%d bindings=%d/%d audits=%d/%d",
			len(after.FundCallbacks),
			len(before.FundCallbacks),
			len(after.FundCallbackLedger),
			len(before.FundCallbackLedger),
			len(after.FundCallbackApprovedActions),
			len(before.FundCallbackApprovedActions),
			len(after.AuditEvents),
			len(before.AuditEvents),
		)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("funds.create replay mutated CRM state")
	}
}

func m303AuditOperationCount(state CRMState, operation string) int {
	count := 0
	for _, event := range state.AuditEvents {
		if event.Operation == operation {
			count++
		}
	}
	return count
}

func m303Allowed(operator string) WorkbenchExecutionContext {
	binding := strings.ReplaceAll(operator, " ", "_")
	return WorkbenchExecutionContext{
		ActionID:            "aiact_m303_" + binding,
		PermissionRequestID: "aipermreq_m303_" + binding,
		PermissionDecision:  AutomationPermissionAllowOnce,
		OperatorID:          operator,
	}
}

func m303Dispatch(
	t *testing.T,
	dispatcher WorkbenchToolDispatcher,
	execution WorkbenchExecutionContext,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchToolResult {
	t.Helper()
	return dispatcher.Dispatch(
		context.Background(),
		execution,
		m303Decoded(t, tool, operation, arguments),
	)
}

func m303Decoded(
	t *testing.T,
	tool AutomationToolName,
	operation WorkbenchOperation,
	arguments any,
) WorkbenchDecodedToolRequest {
	t.Helper()
	argumentsJSON, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(WorkbenchToolRequest{
		Operation: operation, ArgumentsJSON: string(argumentsJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeWorkbenchToolRequest(tool, raw)
	if err != nil {
		t.Fatalf("decode %s/%s: %v", tool, operation, err)
	}
	return decoded
}

func m303AssertSuccessEnvelope(t *testing.T, result WorkbenchToolResult, paged bool) {
	t.Helper()
	if !result.OK || result.Error != nil || len(result.Data) == 0 {
		t.Fatalf("result = %+v, want success", result)
	}
	want := []string{"data", "ok", "operation"}
	if paged {
		if result.Page == nil {
			t.Fatalf("result = %+v, want page", result)
		}
		want = append(want, "page")
	} else if result.Page != nil {
		t.Fatalf("result = %+v, want no page", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	m303AssertJSONKeys(t, encoded, want...)
}

func m303AssertFailure(t *testing.T, result WorkbenchToolResult, code WorkbenchErrorCode) {
	t.Helper()
	if result.OK || result.Error == nil || result.Error.Code != code ||
		len(result.Data) != 0 || result.Page != nil {
		t.Fatalf("result = %+v, want failure %s", result, code)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	m303AssertJSONKeys(t, encoded, "error", "ok", "operation")
}

func m303AssertRejectedWithoutStateChange(
	t *testing.T,
	store *CRMStore,
	call func() WorkbenchToolResult,
	code WorkbenchErrorCode,
) {
	t.Helper()
	before := store.Snapshot()
	result := call()
	m303AssertFailure(t, result, code)
	if after := store.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected operation %s mutated state or audit", result.Operation)
	}
}

func m303AssertLastAudit(t *testing.T, store *CRMStore, operation, actor string) {
	t.Helper()
	audits := store.Snapshot().AuditEvents
	if len(audits) == 0 {
		t.Fatal("expected CRM audit")
	}
	last := audits[len(audits)-1]
	if last.Operation != operation || last.Actor != actor {
		t.Fatalf("last audit = %+v, want operation=%s actor=%s", last, operation, actor)
	}
}

func m303AssertJSONKeys(
	t *testing.T,
	raw json.RawMessage,
	want ...string,
) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode object %s: %v", raw, err)
	}
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys = %v, want %v; payload=%s", got, want, raw)
	}
	return object
}

func m303AssertNoPaymentFields(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var value any
	m303Decode(t, raw, &value)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				switch strings.ToLower(key) {
				case "payment", "payments", "payment_execution", "bank_account",
					"wallet_address", "recipient", "dispatch_attempted":
					t.Fatalf("fund result exposed payment execution field %q", key)
				}
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
}

func m303Decode(t *testing.T, raw json.RawMessage, target any) {
	t.Helper()
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}

func m303FundReadKeys() []string {
	return []string{
		"amount_minor",
		"candidate_id",
		"commitment_at",
		"commitment_by",
		"condition",
		"condition_length",
		"condition_truncated",
		"created_at",
		"currency",
		"due_at",
		"evidence",
		"evidence_length",
		"evidence_truncated",
		"final_confirmed_at",
		"final_confirmed_by",
		"final_result",
		"final_result_length",
		"final_result_truncated",
		"id",
		"job_id",
		"lead_id",
		"onboarding_case_id",
		"promised_by",
		"promised_by_length",
		"promised_by_truncated",
		"status",
		"updated_at",
		"version",
	}
}

func m303AssertFundReadProjection(
	t *testing.T,
	fund FundCallback,
	projected workbenchGovernanceFundReadResult,
) {
	t.Helper()
	if projected.ID != fund.ID ||
		projected.CandidateID != fund.CandidateID ||
		projected.JobID != fund.JobID ||
		projected.LeadID != fund.LeadID ||
		projected.OnboardingCaseID != fund.OnboardingCaseID ||
		projected.AmountMinor != fund.AmountMinor ||
		projected.Currency != fund.Currency ||
		projected.Status != fund.Status ||
		projected.CommitmentBy != fund.CommitmentBy ||
		!reflect.DeepEqual(projected.CommitmentAt, fund.CommitmentAt) ||
		projected.FinalConfirmedBy != fund.FinalConfirmedBy ||
		!reflect.DeepEqual(projected.FinalConfirmedAt, fund.FinalConfirmedAt) ||
		!reflect.DeepEqual(projected.DueAt, fund.DueAt) ||
		projected.Version != fund.Version ||
		projected.CreatedAt != fund.CreatedAt ||
		projected.UpdatedAt != fund.UpdatedAt {
		t.Fatalf("fund read projection lost exact fields:\nfund=%+v\nprojection=%+v", fund, projected)
	}
	m303AssertBoundedFundText(
		t,
		"condition",
		fund.Condition,
		projected.Condition,
		projected.ConditionLength,
		projected.ConditionTruncated,
	)
	m303AssertBoundedFundText(
		t,
		"promised_by",
		fund.PromisedBy,
		projected.PromisedBy,
		projected.PromisedByLength,
		projected.PromisedByTruncated,
	)
	m303AssertBoundedFundText(
		t,
		"evidence",
		fund.Evidence,
		projected.Evidence,
		projected.EvidenceLength,
		projected.EvidenceTruncated,
	)
	m303AssertBoundedFundText(
		t,
		"final_result",
		fund.FinalResult,
		projected.FinalResult,
		projected.FinalResultLength,
		projected.FinalResultTruncated,
	)
}

func m303AssertBoundedFundText(
	t *testing.T,
	field string,
	original string,
	projected string,
	length int,
	truncated bool,
) {
	t.Helper()
	runes := []rune(original)
	want := original
	wantTruncated := false
	if len(runes) > workbenchGovernanceFundTextPreviewRunes {
		want = string(runes[:workbenchGovernanceFundTextPreviewRunes])
		wantTruncated = true
	}
	if projected != want || length != len(runes) || truncated != wantTruncated {
		t.Fatalf(
			"%s projection length=%d truncated=%v preview_runes=%d, want length=%d truncated=%v preview_runes=%d",
			field,
			length,
			truncated,
			len([]rune(projected)),
			len(runes),
			wantTruncated,
			len([]rune(want)),
		)
	}
}

func m303FundCreateResultKeys() []string {
	return []string{
		"amount_minor",
		"candidate_id",
		"currency",
		"id",
		"status",
		"updated_at",
		"version",
	}
}

func m303FundUpdateResultKeys() []string {
	return []string{"id", "status", "updated_at", "version"}
}

func m303FundCommitmentResultKeys() []string {
	return []string{
		"commitment_at",
		"commitment_by",
		"id",
		"status",
		"updated_at",
		"version",
	}
}

func m303FundFinalResultKeys() []string {
	return []string{
		"final_confirmed_at",
		"final_confirmed_by",
		"id",
		"status",
		"updated_at",
		"version",
	}
}

func m303LedgerKeys(fromStatus bool) []string {
	keys := []string{
		"amount_minor",
		"created_at",
		"currency",
		"evidence_sha256",
		"fund_callback_id",
		"id",
		"operator_id",
		"to_status",
		"type",
	}
	if fromStatus {
		keys = append(keys, "from_status")
	} else {
		keys = append(keys, "candidate_id")
	}
	return keys
}

func m303CandidateKeys(anonymized bool) []string {
	if anonymized {
		return []string{
			"created_at",
			"full_name",
			"id",
			"skills",
			"source",
			"status",
			"updated_at",
			"version",
		}
	}
	return []string{
		"created_at",
		"email",
		"full_name",
		"id",
		"location",
		"owner",
		"phone",
		"skills",
		"source",
		"status",
		"summary",
		"updated_at",
		"version",
		"wechat_id",
	}
}

func m303CandidateTerminalKeys() []string {
	return append(
		m303CandidateKeys(false),
		"summary_length",
		"summary_truncated",
	)
}

func m303JobCompensationKeys() []string {
	return []string{
		"accommodation",
		"accommodation_length",
		"accommodation_truncated",
		"compensation_summary",
		"compensation_summary_length",
		"compensation_summary_truncated",
		"id",
		"transport_requirements",
		"transport_requirements_length",
		"transport_requirements_truncated",
		"updated_at",
		"version",
	}
}
