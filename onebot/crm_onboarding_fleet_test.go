package main

import (
	"errors"
	"sync"
	"testing"
)

func TestCRMOnboardingHiredHandoffAndFleetLifecycle(t *testing.T) {
	store := crmPipelineTestStore(t)
	lead := crmOnboardingTestHiredLead(t, store)

	onboarding, created, err := crmOnboardingEnsureForLead(store, lead.ID, "operator")
	if err != nil {
		t.Fatalf("ensure onboarding: %v", err)
	}
	if created {
		t.Fatal("hired transition should already create onboarding")
	}
	if onboarding.LeadID != lead.ID || onboarding.Status != OnboardingStatusPending ||
		onboarding.Fleet.Status != FleetStatusNotRequired {
		t.Fatalf("unexpected onboarding: %#v", onboarding)
	}

	required := true
	onboarding, err = crmFleetUpdate(store, onboarding.ID, FleetUpdate{
		ExpectedVersion: onboarding.Version,
		Required:        &required,
	})
	if err != nil {
		t.Fatalf("require fleet: %v", err)
	}
	if !onboarding.Fleet.Required || onboarding.Fleet.Status != FleetStatusPending {
		t.Fatalf("unexpected fleet update: %#v", onboarding.Fleet)
	}
	onboarding, err = crmFleetTransition(store, onboarding.ID, FleetTransition{
		ExpectedVersion: onboarding.Version,
		FromStatus:      FleetStatusPending,
		ToStatus:        FleetStatusRequested,
	})
	if err != nil {
		t.Fatalf("request fleet: %v", err)
	}
	if onboarding.Fleet.Status != FleetStatusRequested {
		t.Fatalf("unexpected fleet transition: %#v", onboarding.Fleet)
	}

	if _, err := crmOnboardingCreate(store, OnboardingCreate{LeadID: lead.ID}); !errors.Is(err, ErrCRMConflict) {
		t.Fatalf("expected duplicate onboarding conflict, got %v", err)
	}
}

func TestCRMOnboardingEnsureConcurrentSingleCreate(t *testing.T) {
	store := crmPipelineTestStore(t)
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, err := crmPipelineCreateJob(store, JobDraft{Title: "Onsite", Company: "Example"})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	err = store.Update(func(state *CRMState, _ *IDAllocator) error {
		index := crmPipelineLeadIndex(state.Leads, lead.ID)
		state.Leads[index].Stage = LeadStageHired
		state.Leads[index].Version++
		state.Leads[index].StageChangedAt = state.Leads[index].UpdatedAt
		return nil
	})
	if err != nil {
		t.Fatalf("prepare hired lead: %v", err)
	}

	type result struct {
		created bool
		err     error
	}
	results := make(chan result, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, created, ensureErr := crmOnboardingEnsureForLead(store, lead.ID, "operator")
			results <- result{created: created, err: ensureErr}
		}()
	}
	wait.Wait()
	close(results)

	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("ensure onboarding: %v", result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created count=%d, want 1", createdCount)
	}
	if got := store.Snapshot().OnboardingCases; len(got) != 1 || got[0].LeadID != lead.ID {
		t.Fatalf("unexpected onboarding cases: %#v", got)
	}
}

func crmOnboardingTestHiredLead(t *testing.T, store *CRMStore) Lead {
	t.Helper()
	candidate := crmPipelineTestCandidate(t, store, CandidateStatusActive)
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title: "Onsite", Company: "Example", Location: "Site A",
		RequiredDocuments: []string{"ID card"},
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	lead, err := crmPipelineCreateLead(store, LeadCreate{CandidateID: candidate.ID, JobID: job.ID})
	if err != nil {
		t.Fatalf("create lead: %v", err)
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
			Actor:           "operator",
		})
		if err != nil {
			t.Fatalf("transition lead to %s: %v", stage, err)
		}
	}
	return lead
}
