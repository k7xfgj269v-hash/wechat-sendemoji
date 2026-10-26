package main

import (
	"errors"
	"testing"
)

func TestCRMCandidateIdentityAliasNormalization(t *testing.T) {
	phoneCases := map[string]string{
		" +49 (170) 123-4567 ": "+491701234567",
		"0049 170 123 4567":    "+491701234567",
		"0170.123.4567":        "01701234567",
		"desk-extension":       "desk-extension",
	}
	for input, want := range phoneCases {
		if got := crmCandidateNormalizePhoneAlias(input); got != want {
			t.Errorf("crmCandidateNormalizePhoneAlias(%q) = %q, want %q", input, got, want)
		}
	}
	if got := crmCandidateNormalizeEmailAlias(" NOVA@SYNTHETIC.INVALID "); got != "nova@synthetic.invalid" {
		t.Fatalf("normalized email = %q", got)
	}
}

func TestCRMCandidateCreateReusesUnambiguousIdentity(t *testing.T) {
	store, _ := crmCandidateTestOpenStore(t)
	first, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Nova Vale",
		Phone:    "+49 (170) 123-4567",
		Email:    "nova@synthetic.invalid",
		Skills:   []string{},
	})
	if err != nil {
		t.Fatal(err)
	}

	reused, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Different Synthetic Name",
		Phone:    "0049 170 123 4567",
		Email:    "NOVA@SYNTHETIC.INVALID",
		Skills:   []string{"Go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reused.ID != first.ID || reused.FullName != first.FullName || reused.Version != first.Version {
		t.Fatalf("reused candidate = %+v, first = %+v", reused, first)
	}
	state := store.Snapshot()
	if state.Counters.Candidate != 1 || len(state.Candidates) != 1 {
		t.Fatalf("candidate identity reuse state = %+v", state.Candidates)
	}
}

func TestCRMCandidateIdentityConflictDoesNotMerge(t *testing.T) {
	store, _ := crmCandidateTestOpenStore(t)
	wechatCandidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Mira Sol",
		WeChatID: "wxid_synthetic_mira",
		Skills:   []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	emailCandidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Orin Vale",
		Email:    "orin@synthetic.invalid",
		Skills:   []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()

	_, err = crmCandidateCreate(store, CandidateDraft{
		FullName: "Conflicting Synthetic Identity",
		WeChatID: wechatCandidate.WeChatID,
		Email:    emailCandidate.Email,
		Skills:   []string{},
	})
	if !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("identity conflict error = %v, want conflict", err)
	}
	after := store.Snapshot()
	if after.Counters.Candidate != before.Counters.Candidate ||
		len(after.Candidates) != len(before.Candidates) ||
		after.Candidates[0].Version != before.Candidates[0].Version ||
		after.Candidates[1].Version != before.Candidates[1].Version {
		t.Fatalf("identity conflict merged candidates: before=%+v after=%+v", before.Candidates, after.Candidates)
	}
}
