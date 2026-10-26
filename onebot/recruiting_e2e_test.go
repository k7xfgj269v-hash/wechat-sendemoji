package main

import (
	"context"
	"testing"
	"time"
)

func TestSyntheticRecruitingLifecycleEndToEnd(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	const (
		sourceGroupID = "synthetic_jobs@chatroom"
		candidateID   = "wxid_synthetic_candidate"
	)
	crmJobIngestionSeedSourceGroup(t, store, sourceGroupID, true)

	draft := crmJobIngestionDraft("SYN-E2E-001")
	sourceEvent := crmJobIngestionSeedEvent(
		t,
		store,
		sourceGroupID,
		"mid-synthetic-job-create",
		"Synthetic operator demand version one",
	)
	created, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: sourceEvent.ID,
		GroupID: sourceGroupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("create source job: %v", err)
	}
	if created.Decision != JobIngestionCreate || created.Job.ID == "" {
		t.Fatalf("create source job result = %+v", created)
	}

	idempotent, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: sourceEvent.ID,
		GroupID: sourceGroupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("repeat source event: %v", err)
	}
	if !idempotent.Idempotent || idempotent.Job.ID != created.Job.ID {
		t.Fatalf("idempotent source job result = %+v", idempotent)
	}

	duplicateEvent := crmJobIngestionSeedEvent(
		t,
		store,
		sourceGroupID,
		"mid-synthetic-job-duplicate",
		sourceEvent.Text,
	)
	duplicate, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: duplicateEvent.ID,
		GroupID: sourceGroupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("duplicate source job: %v", err)
	}
	if duplicate.Decision != JobIngestionDuplicate || duplicate.Job.ID != created.Job.ID {
		t.Fatalf("duplicate source job result = %+v", duplicate)
	}

	noopEvent := crmJobIngestionSeedEvent(
		t,
		store,
		sourceGroupID,
		"mid-synthetic-job-noop",
		"Same synthetic demand with editorial wording",
	)
	noop, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: noopEvent.ID,
		GroupID: sourceGroupID,
		Job:     draft,
	})
	if err != nil {
		t.Fatalf("no-op source job: %v", err)
	}
	if noop.Decision != JobIngestionNoop || noop.Job.ID != created.Job.ID {
		t.Fatalf("no-op source job result = %+v", noop)
	}

	updatedDraft := draft
	updatedDraft.Headcount = draft.Headcount + 5
	updateEvent := crmJobIngestionSeedEvent(
		t,
		store,
		sourceGroupID,
		"mid-synthetic-job-update",
		"Synthetic operator demand version two",
	)
	updated, err := store.IngestSourceJob(SourceJobIngestion{
		EventID: updateEvent.ID,
		GroupID: sourceGroupID,
		Job:     updatedDraft,
	})
	if err != nil {
		t.Fatalf("update source job: %v", err)
	}
	if updated.Decision != JobIngestionUpdate ||
		updated.Job.ID != created.Job.ID ||
		updated.Job.Headcount != updatedDraft.Headcount {
		t.Fatalf("updated source job result = %+v", updated)
	}

	firstMessage := syntheticPrivateCandidateEvent(
		t,
		store,
		candidateID,
		"mid-synthetic-candidate-one",
		"Fictional candidate asks about the synthetic operator role",
		0,
	)
	firstLink, err := crmCandidateLinkMessage(
		context.Background(),
		store,
		ManualCandidateExtractor{},
		MessageCandidateLink{
			MessageEventID:       firstMessage.ID,
			ExpectedEventVersion: firstMessage.Version,
			Candidate: &CandidateDraft{
				FullName: "Fictional Candidate",
				Phone:    "+49 000 000000",
				Email:    "fictional.candidate@example.invalid",
				Skills:   []string{"assembly", "Go"},
			},
		},
	)
	if err != nil {
		t.Fatalf("link first private event: %v", err)
	}
	if !firstLink.Created || firstLink.Candidate.WeChatID != candidateID {
		t.Fatalf("first candidate link = %+v", firstLink)
	}

	secondMessage := syntheticPrivateCandidateEvent(
		t,
		store,
		candidateID,
		"mid-synthetic-candidate-two",
		"Fictional candidate provides a synthetic onboarding detail",
		time.Second,
	)
	secondLink, err := crmCandidateLinkMessage(
		context.Background(),
		store,
		ManualCandidateExtractor{},
		MessageCandidateLink{
			MessageEventID:       secondMessage.ID,
			ExpectedEventVersion: secondMessage.Version,
			Candidate: &CandidateDraft{
				FullName: "Fictional Candidate",
				Phone:    "+49 000 000000",
				Email:    "fictional.candidate@example.invalid",
				Skills:   []string{"assembly", "Go"},
			},
		},
	)
	if err != nil {
		t.Fatalf("link second private event: %v", err)
	}
	if secondLink.Created || secondLink.Candidate.ID != firstLink.Candidate.ID {
		t.Fatalf("candidate dedup result = %+v", secondLink)
	}

	lead, err := crmPipelineCreateLead(store, LeadCreate{
		CandidateID: firstLink.Candidate.ID,
		JobID:       updated.Job.ID,
		Owner:       "synthetic-recruiter",
	})
	if err != nil {
		t.Fatalf("create lead: %v", err)
	}
	for _, next := range []LeadStage{
		LeadStageScreening,
		LeadStageInterview,
		LeadStageOffer,
		LeadStageHired,
	} {
		lead, err = crmPipelineTransitionLead(store, lead.ID, LeadTransition{
			ExpectedVersion: lead.Version,
			FromStage:       lead.Stage,
			ToStage:         next,
			Reason:          "synthetic end-to-end fixture",
			Actor:           "synthetic-operator",
		})
		if err != nil {
			t.Fatalf("transition lead to %s: %v", next, err)
		}
	}

	onboardingTotal, onboardings, err := crmOnboardingList(
		store,
		crmOnboardingFilter{LeadID: lead.ID},
	)
	if err != nil {
		t.Fatalf("list onboarding: %v", err)
	}
	if onboardingTotal != 1 || len(onboardings) != 1 {
		t.Fatalf("onboarding projection = total %d, items %+v", onboardingTotal, onboardings)
	}
	onboarding := onboardings[0]
	fleetRequired := true
	pickupAddress := "Synthetic Pickup Point"
	destination := "Synthetic Site"
	onboarding, err = crmFleetUpdate(store, onboarding.ID, FleetUpdate{
		ExpectedVersion: onboarding.Version,
		Required:        &fleetRequired,
		PickupAddress:   &pickupAddress,
		Destination:     &destination,
		Actor:           "synthetic-operator",
	})
	if err != nil {
		t.Fatalf("configure fleet coordination: %v", err)
	}
	if !onboarding.Fleet.Required || onboarding.Fleet.Status != FleetStatusPending {
		t.Fatalf("fleet coordination = %+v", onboarding.Fleet)
	}

	serviceRequest, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID:      firstLink.Candidate.ID,
		JobID:            updated.Job.ID,
		OnboardingCaseID: onboarding.ID,
		SourceEventID:    secondMessage.ID,
		Category:         ServiceRequestCategoryTransport,
		Priority:         JobPriorityNormal,
		Description:      "Coordinate the fictional pickup request",
		Actor:            "synthetic-operator",
	})
	if err != nil {
		t.Fatalf("create service request: %v", err)
	}

	dueAt := time.Date(2027, time.January, 15, 9, 0, 0, 0, time.UTC)
	fundCallback, err := crmFundCallbackCreate(store, FundCallbackCreate{
		CandidateID:      firstLink.Candidate.ID,
		JobID:            updated.Job.ID,
		LeadID:           lead.ID,
		OnboardingCaseID: onboarding.ID,
		AmountMinor:      100,
		Currency:         "CNY",
		Condition:        "Synthetic callback draft only",
		PromisedBy:       "Fictional Customer",
		DueAt:            &dueAt,
		Evidence:         "No payment and no irreversible commitment",
		Actor:            "synthetic-operator",
	})
	if err != nil {
		t.Fatalf("create fund callback draft: %v", err)
	}
	if fundCallback.Status != FundCallbackStatusDraft {
		t.Fatalf("fund callback status = %q", fundCallback.Status)
	}

	activity := crmActivityNewService(store)
	task, err := activity.CreateTask(TaskCreate{
		Title:       "Review synthetic onboarding",
		Description: "Synthetic fixture task",
		Owner:       "synthetic-operator",
		DueAt:       dueAt,
		CandidateID: firstLink.Candidate.ID,
		LeadID:      lead.ID,
	})
	if err != nil {
		t.Fatalf("create follow-up task: %v", err)
	}
	note, err := activity.CreateNote(NoteCreate{
		TargetType: NoteTargetLead,
		TargetID:   lead.ID,
		Author:     "synthetic-operator",
		Body:       "Synthetic lifecycle reached onboarding and service handling.",
	})
	if err != nil {
		t.Fatalf("create lead note: %v", err)
	}

	state := store.Snapshot()
	if len(state.Candidates) != 1 ||
		len(state.Jobs) != 1 ||
		len(state.JobProvenance) != 4 ||
		len(state.DemandRevisions) != 2 ||
		len(state.Leads) != 1 ||
		len(state.OnboardingCases) != 1 ||
		len(state.ServiceRequests) != 1 ||
		len(state.FundCallbacks) != 1 ||
		len(state.Tasks) != 1 ||
		len(state.Notes) != 1 {
		t.Fatalf("synthetic lifecycle state = %+v", state.Counters)
	}
	if serviceRequest.CandidateID != firstLink.Candidate.ID ||
		task.LeadID != lead.ID ||
		note.TargetID != lead.ID {
		t.Fatalf(
			"downstream links = service %+v task %+v note %+v",
			serviceRequest,
			task,
			note,
		)
	}
}

func syntheticPrivateCandidateEvent(
	t *testing.T,
	store *CRMStore,
	wechatID string,
	mid string,
	text string,
	offset time.Duration,
) MessageEvent {
	t.Helper()
	now := crmJobIngestionTestTime.Add(offset)
	event, created, err := crmMessageCaptureNormalizedInboundToStore(
		store,
		&ForwardRecord{
			T:      now.Unix(),
			Chat:   string(ChatTypePrivate),
			Dir:    "in",
			Peer:   wechatID,
			Author: wechatID,
			Text:   text,
			Kinds:  []string{"text"},
			Mid:    mid,
		},
		now,
	)
	if err != nil {
		t.Fatalf("capture synthetic private event: %v", err)
	}
	if !created {
		t.Fatalf("synthetic private event %q was not created", mid)
	}
	return event
}
