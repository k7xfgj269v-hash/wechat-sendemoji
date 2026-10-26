package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const crmJobIngestionRouteToken = "operator-secret"

func TestCRMJobIngestionRoutesRequireLoopbackOperatorAuth(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	mux := http.NewServeMux()
	registerCRMJobIngestionRoutes(mux, store, crmJobIngestionRouteToken)

	response := crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobIngestionPath,
		nil,
		"",
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, body = %s", response.Code, response.Body.String())
	}
	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobIngestionPath,
		nil,
		crmJobIngestionRouteToken,
		"10.0.0.2:12000",
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, body = %s", response.Code, response.Body.String())
	}
	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobProvenancePath,
		nil,
		"",
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("provenance unauthenticated status = %d, body = %s",
			response.Code,
			response.Body.String(),
		)
	}
	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobIngestionPath,
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestCRMJobIngestionRoutesLifecycleTotalsAndProvenance(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	groupID := "synthetic_routes@chatroom"
	crmJobIngestionSeedSourceGroup(t, store, groupID, true)
	mux := http.NewServeMux()
	registerCRMJobIngestionRoutes(mux, store, crmJobIngestionRouteToken)

	draft := crmJobIngestionDraft("SYN-ROUTE-001")
	createEvent := crmJobIngestionSeedEvent(t, store, groupID, "mid-route-create", "route job")
	created := crmJobIngestionRoutePost(t, mux, SourceJobIngestion{
		EventID: createEvent.ID,
		GroupID: groupID,
		Job:     draft,
	}, http.StatusCreated)
	if created.Decision != JobIngestionCreate ||
		created.Idempotent ||
		created.Job.DemandRevisionNumber != 1 {
		t.Fatalf("created result = %+v", created)
	}

	idempotent := crmJobIngestionRoutePost(t, mux, SourceJobIngestion{
		EventID: createEvent.ID,
		GroupID: groupID,
		Job:     draft,
	}, http.StatusOK)
	if !idempotent.Idempotent ||
		idempotent.Decision != JobIngestionCreate ||
		idempotent.Provenance.ID != created.Provenance.ID {
		t.Fatalf("idempotent result = %+v", idempotent)
	}

	duplicateEvent := crmJobIngestionSeedEvent(
		t,
		store,
		groupID,
		"mid-route-duplicate",
		"route job",
	)
	duplicate := crmJobIngestionRoutePost(t, mux, SourceJobIngestion{
		EventID: duplicateEvent.ID,
		GroupID: groupID,
		Job:     draft,
	}, http.StatusOK)
	if duplicate.Decision != JobIngestionDuplicate || duplicate.Job.ID != created.Job.ID {
		t.Fatalf("duplicate result = %+v", duplicate)
	}

	noopDraft := draft
	noopDraft.Title = "  Synthetic Operator  "
	noopDraft.Requirements = []string{"Go", "assembly"}
	noopEvent := crmJobIngestionSeedEvent(
		t,
		store,
		groupID,
		"mid-route-noop",
		"equivalent route job",
	)
	noop := crmJobIngestionRoutePost(t, mux, SourceJobIngestion{
		EventID: noopEvent.ID,
		GroupID: groupID,
		Job:     noopDraft,
	}, http.StatusOK)
	if noop.Decision != JobIngestionNoop || noop.Job.ID != created.Job.ID {
		t.Fatalf("noop result = %+v", noop)
	}

	updateDraft := draft
	updateDraft.Headcount = 18
	updateEvent := crmJobIngestionSeedEvent(
		t,
		store,
		groupID,
		"mid-route-update",
		"updated route job",
	)
	updated := crmJobIngestionRoutePost(t, mux, SourceJobIngestion{
		EventID: updateEvent.ID,
		GroupID: groupID,
		Job:     updateDraft,
	}, http.StatusOK)
	if updated.Decision != JobIngestionUpdate ||
		updated.Job.ID != created.Job.ID ||
		updated.Job.Headcount != 18 ||
		updated.Job.DemandRevisionNumber != 2 {
		t.Fatalf("updated result = %+v", updated)
	}

	beforeReads := store.Snapshot()
	response := crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobIngestionPath+"?limit=2&offset=0",
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("summary status = %d, body = %s", response.Code, response.Body.String())
	}
	var summary crmJobIngestionSummaryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Total != 4 ||
		summary.Totals.Create != 1 ||
		summary.Totals.Update != 1 ||
		summary.Totals.Duplicate != 1 ||
		summary.Totals.Noop != 1 ||
		summary.Totals.Noise != 0 ||
		summary.Totals.Failure != 0 ||
		len(summary.RecentDecisions) != 2 ||
		summary.RecentDecisions[0].Decision != JobIngestionUpdate ||
		summary.RecentDecisions[1].Decision != JobIngestionNoop {
		t.Fatalf("summary = %+v", summary)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte(`"noise":0`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"failure":0`)) {
		t.Fatalf("summary omits zero-valued decision totals: %s", response.Body.String())
	}

	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobProvenancePath+"?job_id="+created.Job.ID+"&limit=2&offset=1",
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	provenance := crmJobIngestionDecodeProvenance(t, response, http.StatusOK)
	if provenance.Total != 4 ||
		len(provenance.Provenance) != 2 ||
		provenance.Provenance[0].Decision != JobIngestionNoop ||
		provenance.Provenance[1].Decision != JobIngestionDuplicate {
		t.Fatalf("job provenance = %+v", provenance)
	}

	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobProvenancePath+"?event_id="+updateEvent.ID,
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	provenance = crmJobIngestionDecodeProvenance(t, response, http.StatusOK)
	if provenance.Total != 1 ||
		len(provenance.Provenance) != 1 ||
		provenance.Provenance[0] != updated.Provenance {
		t.Fatalf("event provenance = %+v", provenance)
	}

	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodGet,
		crmJobProvenancePath+"?group_id="+groupID+"&decision=duplicate",
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	provenance = crmJobIngestionDecodeProvenance(t, response, http.StatusOK)
	if provenance.Total != 1 ||
		len(provenance.Provenance) != 1 ||
		provenance.Provenance[0].ID != duplicate.Provenance.ID {
		t.Fatalf("filtered provenance = %+v", provenance)
	}
	if afterReads := store.Snapshot(); !reflect.DeepEqual(afterReads, beforeReads) {
		t.Fatalf("read routes mutated state: before=%+v after=%+v", beforeReads, afterReads)
	}
}

func TestCRMJobIngestionRoutesRejectInvalidSourceFieldsMethodsAndBounds(t *testing.T) {
	store := crmJobIngestionOpenStore(t)
	groupID := "synthetic_invalid_routes@chatroom"
	event := crmJobIngestionSeedEvent(
		t,
		store,
		groupID,
		"mid-route-invalid",
		"invalid source route job",
	)
	mux := http.NewServeMux()
	registerCRMJobIngestionRoutes(mux, store, crmJobIngestionRouteToken)

	response := crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodPost,
		crmJobIngestionPath,
		SourceJobIngestion{
			EventID: event.ID,
			GroupID: groupID,
			Job:     crmJobIngestionDraft("SYN-INVALID-ROUTE"),
		},
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid source status = %d, body = %s", response.Code, response.Body.String())
	}
	if state := store.Snapshot(); len(state.Jobs) != 0 || len(state.JobProvenance) != 0 {
		t.Fatalf("invalid source mutated state: jobs=%d provenance=%d",
			len(state.Jobs),
			len(state.JobProvenance),
		)
	}

	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodPost,
		crmJobIngestionPath,
		map[string]any{
			"event_id":       event.ID,
			"group_id":       groupID,
			"job":            crmJobIngestionDraft("SYN-STRICT-ROUTE"),
			"operator_token": "must-not-be-accepted",
			"source_url":     "https://example.invalid",
			"shell":          "echo forbidden",
		},
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusBadRequest ||
		strings.Contains(response.Body.String(), "must-not-be-accepted") {
		t.Fatalf("strict JSON status/body = %d %s", response.Code, response.Body.String())
	}

	oversizedBody := `{"event_id":"` + strings.Repeat("x", CRMMaxRequestBodyBytes) + `"}`
	response = crmJobIngestionRouteRawRequest(
		mux,
		http.MethodPost,
		crmJobIngestionPath,
		oversizedBody,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, path := range []string{
		crmJobIngestionPath + "?limit=201",
		crmJobIngestionPath + "?offset=-1",
		crmJobIngestionPath + "?unknown=1",
		crmJobProvenancePath + "?limit=201",
		crmJobProvenancePath + "?offset=-1",
		crmJobProvenancePath + "?decision=invalid",
		crmJobProvenancePath + "?group_id=not-a-group",
		crmJobProvenancePath + "?job_id=one&job_id=two",
		crmJobProvenancePath + "?unknown=1",
	} {
		response = crmJobIngestionRouteRequest(
			t,
			mux,
			http.MethodGet,
			path,
			nil,
			crmJobIngestionRouteToken,
			"127.0.0.1:12000",
		)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}

	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodPut,
		crmJobIngestionPath,
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusMethodNotAllowed ||
		response.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("ingestion method status/allow = %d %q",
			response.Code,
			response.Header().Get("Allow"),
		)
	}
	response = crmJobIngestionRouteRequest(
		t,
		mux,
		http.MethodPost,
		crmJobProvenancePath,
		nil,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != http.StatusMethodNotAllowed ||
		response.Header().Get("Allow") != "GET" {
		t.Fatalf("provenance method status/allow = %d %q",
			response.Code,
			response.Header().Get("Allow"),
		)
	}
}

func crmJobIngestionRoutePost(
	t *testing.T,
	handler http.Handler,
	input SourceJobIngestion,
	wantStatus int,
) JobIngestionResult {
	t.Helper()
	response := crmJobIngestionRouteRequest(
		t,
		handler,
		http.MethodPost,
		crmJobIngestionPath,
		input,
		crmJobIngestionRouteToken,
		"127.0.0.1:12000",
	)
	if response.Code != wantStatus {
		t.Fatalf("ingestion status = %d, want %d, body = %s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	var result JobIngestionResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func crmJobIngestionDecodeProvenance(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
) crmJobProvenanceResponse {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("provenance status = %d, want %d, body = %s",
			response.Code,
			wantStatus,
			response.Body.String(),
		)
	}
	var result crmJobProvenanceResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func crmJobIngestionRouteRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body any,
	token string,
	remoteAddr string,
) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&payload).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	return crmJobIngestionRouteRawRequest(
		handler,
		method,
		path,
		payload.String(),
		token,
		remoteAddr,
	)
}

func crmJobIngestionRouteRawRequest(
	handler http.Handler,
	method string,
	path string,
	body string,
	token string,
	remoteAddr string,
) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
