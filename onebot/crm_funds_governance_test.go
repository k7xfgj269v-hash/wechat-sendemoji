package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFundCallbackLifecycleRequiresProtectedConfirmations(t *testing.T) {
	store := openFundsTestStore(t)
	candidate := createFundsTestCandidate(t, store, "Alice")
	dueAt := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)

	callback, err := crmFundCallbackCreate(store, FundCallbackCreate{
		CandidateID: candidate.ID,
		AmountMinor: 12500,
		Currency:    "cny",
		Condition:   "Complete onsite check-in",
		PromisedBy:  "Customer A",
		DueAt:       &dueAt,
		Evidence:    "Signed callback note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if callback.Status != FundCallbackStatusDraft || callback.Currency != "CNY" || callback.Version != 1 {
		t.Fatalf("created callback = %#v", callback)
	}

	awaiting := FundCallbackStatusAwaitingCommitment
	callback, err = crmFundCallbackUpdate(store, callback.ID, FundCallbackUpdate{
		ExpectedVersion: callback.Version,
		Status:          &awaiting,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crmFundCallbackUpdate(store, callback.ID, FundCallbackUpdate{
		ExpectedVersion: callback.Version,
		Status:          fundStatusPointer(FundCallbackStatusCommitted),
	}); !errors.Is(err, ErrCRMValidation) {
		t.Fatalf("ordinary committed update error = %v", err)
	}

	callback, err = crmFundCallbackConfirmCommitment(store, callback.ID, FundCommitmentConfirmation{
		ExpectedVersion: callback.Version,
		Confirmed:       true,
		OperatorID:      "operator-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if callback.Status != FundCallbackStatusCommitted ||
		callback.CommitmentBy != "operator-1" ||
		callback.CommitmentAt == nil {
		t.Fatalf("committed callback = %#v", callback)
	}
	changedAmount := int64(13000)
	if _, err := crmFundCallbackUpdate(store, callback.ID, FundCallbackUpdate{
		ExpectedVersion: callback.Version,
		AmountMinor:     &changedAmount,
	}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("confirmed amount update error = %v", err)
	}

	awaitingFinal := FundCallbackStatusAwaitingFinal
	callback, err = crmFundCallbackUpdate(store, callback.ID, FundCallbackUpdate{
		ExpectedVersion: callback.Version,
		Status:          &awaitingFinal,
	})
	if err != nil {
		t.Fatal(err)
	}
	callback, err = crmFundCallbackConfirmFinal(store, callback.ID, FundFinalConfirmation{
		ExpectedVersion: callback.Version,
		Confirmed:       true,
		OperatorID:      "operator-2",
		FinalResult:     "Candidate confirmed receipt.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if callback.Status != FundCallbackStatusCompleted ||
		callback.FinalConfirmedBy != "operator-2" ||
		callback.FinalConfirmedAt == nil {
		t.Fatalf("completed callback = %#v", callback)
	}

	state := store.Snapshot()
	var commitmentAudit, finalAudit bool
	for _, event := range state.AuditEvents {
		if event.Operation == "fund_callback.confirm_commitment" && event.Actor == "operator-1" {
			commitmentAudit = true
		}
		if event.Operation == "fund_callback.confirm_final" && event.Actor == "operator-2" {
			finalAudit = true
		}
	}
	if !commitmentAudit || !finalAudit {
		t.Fatalf("missing protected audit events: %#v", state.AuditEvents)
	}
}

func TestFundsGovernanceExportAnonymizeAndReports(t *testing.T) {
	store := openFundsTestStore(t)
	candidate := createFundsTestCandidate(t, store, "=Alice")
	callback, err := crmFundCallbackCreate(store, FundCallbackCreate{
		CandidateID: candidate.ID,
		AmountMinor: 5000,
		Currency:    "USD",
		Condition:   "Callback due",
		PromisedBy:  "@customer",
	})
	if err != nil {
		t.Fatal(err)
	}

	archive, err := crmCandidateExport(store.Snapshot(), candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Candidate.ID != candidate.ID || len(archive.FundCallbacks) != 1 {
		t.Fatalf("archive = %#v", archive)
	}

	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	summary := crmReportSummary(store.Snapshot(), now)
	if summary.CandidateCounts["active"] != 1 ||
		summary.FundCallbackCounts["draft"] != 1 ||
		summary.AuditEventCount < 2 {
		t.Fatalf("summary = %#v", summary)
	}
	data, err := crmReportCSV(store.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "fund_callback,"+callback.ID) ||
		!strings.Contains(string(data), "'@customer") {
		t.Fatalf("CSV = %q", data)
	}

	anonymized, err := crmCandidateAnonymize(store, candidate.ID, CandidateAnonymizeRequest{
		ExpectedVersion: candidate.Version,
		Confirmed:       true,
		OperatorID:      "privacy-operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if anonymized.Status != CandidateStatusArchived ||
		anonymized.WeChatID != "" ||
		anonymized.Phone != "" ||
		anonymized.Email != "" ||
		anonymized.FullName != "Anonymized "+candidate.ID {
		t.Fatalf("anonymized candidate = %#v", anonymized)
	}
}

func openFundsTestStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(t.TempDir() + "/crm.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func createFundsTestCandidate(t *testing.T, store *CRMStore, name string) Candidate {
	t.Helper()
	candidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: name,
		WeChatID: "wxid-funds",
		Phone:    "123",
		Email:    "candidate@example.com",
		Skills:   []string{"onsite"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

func fundStatusPointer(status FundCallbackStatus) *FundCallbackStatus {
	return &status
}
