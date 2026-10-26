package main

import (
	"net/http"
	"testing"
)

func TestCRMOnboardingFleetRoutes(t *testing.T) {
	store := crmPipelineTestStore(t)
	lead := crmOnboardingTestHiredLead(t, store)
	mux := http.NewServeMux()
	registerCRMOnboardingFleetRoutes(mux, store)

	list := crmPipelineRequest(
		t,
		mux,
		http.MethodGet,
		"/api/ops/onboarding_cases?lead_id="+lead.ID,
		"",
	)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var response struct {
		Total           int              `json:"total"`
		OnboardingCases []OnboardingCase `json:"onboarding_cases"`
	}
	crmPipelineDecodeResponse(t, list, &response)
	if response.Total != 1 || len(response.OnboardingCases) != 1 {
		t.Fatalf("unexpected list: %#v", response)
	}
	onboarding := response.OnboardingCases[0]

	update := crmPipelineRequest(
		t,
		mux,
		http.MethodPatch,
		"/api/ops/onboarding_cases/"+onboarding.ID+"/fleet",
		`{"expected_version":1,"required":true,"pickup_address":"Station","destination":"Site A"}`,
	)
	if update.Code != http.StatusOK {
		t.Fatalf("fleet update status=%d body=%s", update.Code, update.Body.String())
	}
	crmPipelineDecodeResponse(t, update, &onboarding)
	if onboarding.Fleet.Status != FleetStatusPending || !onboarding.Fleet.Required {
		t.Fatalf("unexpected fleet: %#v", onboarding.Fleet)
	}

	transition := crmPipelineRequest(
		t,
		mux,
		http.MethodPost,
		"/api/ops/onboarding_cases/"+onboarding.ID+"/fleet/transition",
		`{"expected_version":2,"from_status":"pending","to_status":"requested"}`,
	)
	if transition.Code != http.StatusOK {
		t.Fatalf("fleet transition status=%d body=%s", transition.Code, transition.Body.String())
	}

	unknown := crmPipelineRequest(
		t,
		mux,
		http.MethodPatch,
		"/api/ops/onboarding_cases/"+onboarding.ID,
		`{"expected_version":3,"unknown":true}`,
	)
	if unknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknown.Code, unknown.Body.String())
	}
}
