package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCRMServiceRequestRoutesStrictJSONFiltersAndConflict(t *testing.T) {
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	mux := http.NewServeMux()
	registerCRMServiceRequestRoutes(mux, store)

	createBody := []byte(`{
		"candidate_id":"` + refs.Candidate.ID + `",
		"job_id":"` + refs.Job.ID + `",
		"category":"fund",
		"priority":"high",
		"description":"Callback missing"
	}`)
	createResponse := serviceRequestRouteCall(mux, http.MethodPost, "/api/ops/service_requests", createBody)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created ServiceRequest
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !created.RequiresHuman {
		t.Fatalf("created request = %+v", created)
	}

	listResponse := serviceRequestRouteCall(
		mux,
		http.MethodGet,
		"/api/ops/service_requests?requires_human=true&category=fund&limit=10&offset=0",
		nil,
	)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var list struct {
		Total    int              `json:"total"`
		Requests []ServiceRequest `json:"service_requests"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 1 || len(list.Requests) != 1 || list.Requests[0].ID != created.ID {
		t.Fatalf("list response = %+v", list)
	}

	patchPath := "/api/ops/service_requests/" + created.ID
	firstPatch := serviceRequestRouteCall(
		mux,
		http.MethodPatch,
		patchPath,
		[]byte(`{"expected_version":1,"owner":"ops"}`),
	)
	if firstPatch.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", firstPatch.Code, firstPatch.Body.String())
	}
	stalePatch := serviceRequestRouteCall(
		mux,
		http.MethodPatch,
		patchPath,
		[]byte(`{"expected_version":1,"owner":"stale"}`),
	)
	if stalePatch.Code != http.StatusConflict {
		t.Fatalf("stale patch status=%d body=%s", stalePatch.Code, stalePatch.Body.String())
	}
	unknownField := serviceRequestRouteCall(
		mux,
		http.MethodPatch,
		patchPath,
		[]byte(`{"expected_version":2,"owner":"ops","extra":true}`),
	)
	if unknownField.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", unknownField.Code, unknownField.Body.String())
	}
}

func TestCRMServiceRequestProtectedTransitionRequiresOperatorBearer(t *testing.T) {
	store := openServiceRequestTestStore(t)
	refs := seedServiceRequestTestRefs(t, store)
	created, err := crmServiceRequestCreate(store, ServiceRequestCreate{
		CandidateID: refs.Candidate.ID,
		Category:    ServiceRequestCategoryFund,
		Description: "Confirm callback outcome",
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerCRMServiceRequestRoutes(mux, store, "operator-token")
	target := "/api/ops/service_requests/" + created.ID + "/transition"
	body := []byte(`{"expected_version":1,"from_status":"open","to_status":"cancelled"}`)

	response := serviceRequestRouteCall(mux, http.MethodPost, target, body)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status=%d body=%s", response.Code, response.Body.String())
	}
	if current, err := crmServiceRequestGet(store, created.ID); err != nil ||
		current.Status != ServiceRequestStatusOpen {
		t.Fatalf("unauthorized transition changed request: %#v err=%v", current, err)
	}

	request := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:4567"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer operator-token")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", response.Code, response.Body.String())
	}
	current, err := crmServiceRequestGet(store, created.ID)
	if err != nil || current.Status != ServiceRequestStatusCancelled {
		t.Fatalf("authorized transition result=%#v err=%v", current, err)
	}
}

func serviceRequestRouteCall(
	handler http.Handler,
	method string,
	target string,
	body []byte,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.RemoteAddr = "127.0.0.1:4567"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
