package main

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type serviceRequestTestRefs struct {
	Candidate Candidate
	Job       Job
}

func openServiceRequestTestStore(t *testing.T) *CRMStore {
	t.Helper()
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedServiceRequestTestRefs(t *testing.T, store *CRMStore) serviceRequestTestRefs {
	t.Helper()
	candidate, err := crmCandidateCreate(store, CandidateDraft{
		FullName: "Ada",
		Skills:   []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := crmPipelineCreateJob(store, JobDraft{
		Title:             "Onsite engineer",
		Company:           "Example",
		Requirements:      []string{},
		RequiredDocuments: []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return serviceRequestTestRefs{Candidate: candidate, Job: job}
}

func TestCRMServiceRequestLifecycleRiskAndFilters(t *testing.T) {
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	dueAt := time.Now().UTC().Add(-time.Hour)

	request, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID:   refs.Candidate.ID,
		JobID:         refs.Job.ID,
		Category:      ServiceRequestCategorySafety,
		Priority:      JobPriorityUrgent,
		Description:   " Unsafe equipment ",
		RequiresHuman: false,
		DueAt:         &dueAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !request.RequiresHuman || request.Status != ServiceRequestStatusOpen ||
		request.Description != "Unsafe equipment" || request.Version != 1 {
		t.Fatalf("created request = %+v", request)
	}

	total, requests, err := crmServiceRequestList(store, crmServiceRequestFilter{
		RequiresHuman: boolPointer(true),
		Overdue:       boolPointer(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(requests) != 1 || requests[0].ID != request.ID {
		t.Fatalf("filtered requests = %d %+v", total, requests)
	}

	owner := "ops"
	category := ServiceRequestCategoryDispute
	requiresHuman := false
	request, err = crmServiceRequestUpdate(store, request.ID, ServiceRequestUpdate{
		ExpectedVersion: request.Version,
		Category:        &category,
		Owner:           &owner,
		RequiresHuman:   &requiresHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Owner != "ops" || !request.RequiresHuman || request.Version != 2 {
		t.Fatalf("updated request = %+v", request)
	}

	request, err = crmServiceRequestTransition(store, request.ID, ServiceRequestTransition{
		ExpectedVersion: request.Version,
		FromStatus:      request.Status,
		ToStatus:        ServiceRequestStatusAssigned,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err = crmServiceRequestTransition(store, request.ID, ServiceRequestTransition{
		ExpectedVersion: request.Version,
		FromStatus:      request.Status,
		ToStatus:        ServiceRequestStatusResolved,
		Resolution:      "Replaced equipment",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.ResolvedAt == nil || request.Resolution != "Replaced equipment" {
		t.Fatalf("resolved request = %+v", request)
	}
	request, err = crmServiceRequestTransition(store, request.ID, ServiceRequestTransition{
		ExpectedVersion: request.Version,
		FromStatus:      request.Status,
		ToStatus:        ServiceRequestStatusClosed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.Status != ServiceRequestStatusClosed || request.ResolvedAt == nil {
		t.Fatalf("closed request = %+v", request)
	}
}

func TestCRMServiceRequestOptimisticConcurrency(t *testing.T) {
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	request, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategoryGeneral,
		Description: "Need assistance",
	})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, value := range []string{"alice", "bob"} {
		wait.Add(1)
		go func(owner string) {
			defer wait.Done()
			<-start
			_, updateErr := crmServiceRequestUpdate(store, request.ID, ServiceRequestUpdate{
				ExpectedVersion: request.Version,
				Owner:           &owner,
			})
			results <- updateErr
		}(value)
	}
	close(start)
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrCRMConflict):
			conflicts++
		default:
			t.Fatalf("unexpected update error: %v", result)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	current, err := crmServiceRequestGet(store, request.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || (current.Owner != "alice" && current.Owner != "bob") {
		t.Fatalf("current request = %+v", current)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
