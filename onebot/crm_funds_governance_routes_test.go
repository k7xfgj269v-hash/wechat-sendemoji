package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFundsGovernanceRoutesProtectHumanActions(t *testing.T) {
	store := openFundsTestStore(t)
	candidate := createFundsTestCandidate(t, store, "Route Candidate")
	mux := http.NewServeMux()
	registerCRMFundsGovernanceRoutes(mux, store, "operator-secret")

	response := fundsRouteRequest(t, mux, http.MethodPost, "/api/ops/fund_callbacks", `{
		"candidate_id":"`+candidate.ID+`",
		"amount_minor":10000,
		"currency":"CNY",
		"condition":"Arrival confirmed",
		"promised_by":"Customer"
	}`, "", "127.0.0.1:10000")
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", response.Code, response.Body.String())
	}
	var callback FundCallback
	if err := json.Unmarshal(response.Body.Bytes(), &callback); err != nil {
		t.Fatal(err)
	}
	awaiting := FundCallbackStatusAwaitingCommitment
	callback, err := crmFundCallbackUpdate(store, callback.ID, FundCallbackUpdate{
		ExpectedVersion: callback.Version,
		Status:          &awaiting,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"expected_version":` + jsonNumber(callback.Version) + `}`
	path := "/api/ops/fund_callbacks/" + callback.ID + "/confirm_commitment"

	response = fundsRouteRequest(t, mux, http.MethodPost, path, body, "", "127.0.0.1:10000")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, body = %s", response.Code, response.Body.String())
	}
	response = fundsRouteRequest(t, mux, http.MethodPost, path, body, "operator-secret", "10.0.0.2:10000")
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, body = %s", response.Code, response.Body.String())
	}
	response = fundsRouteRequest(t, mux, http.MethodPost, path, body, "operator-secret", "127.0.0.1:10000")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed status = %d, body = %s", response.Code, response.Body.String())
	}
	body = `{"expected_version":` + jsonNumber(callback.Version) + `,"confirmed":true,"operator_id":"client-forged"}`
	response = fundsRouteRequest(t, mux, http.MethodPost, path, body, "operator-secret", "127.0.0.1:10000")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("client operator_id status = %d, body = %s", response.Code, response.Body.String())
	}
	body = `{"expected_version":` + jsonNumber(callback.Version) + `,"confirmed":true}`
	response = fundsRouteRequest(t, mux, http.MethodPost, path, body, "operator-secret", "127.0.0.1:10000")
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, body = %s", response.Code, response.Body.String())
	}
	var confirmed FundCallback
	if err := json.Unmarshal(response.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.CommitmentBy != crmAuthenticatedOperator {
		t.Fatalf("commitment actor = %q, want authenticated operator", confirmed.CommitmentBy)
	}
	ledger, err := crmFundCallbackLedger(store, callback.ID)
	if err != nil {
		t.Fatal(err)
	}
	var commitment *FundCallbackLedgerEntry
	for i := range ledger {
		if ledger[i].Type == "commitment" {
			commitment = &ledger[i]
			break
		}
	}
	if commitment == nil || commitment.OperatorID != crmAuthenticatedOperator {
		t.Fatalf("commitment ledger = %+v, want authenticated operator", ledger)
	}
}

func TestFundsGovernanceRoutesStrictJSONAuditAndExports(t *testing.T) {
	store := openFundsTestStore(t)
	candidate := createFundsTestCandidate(t, store, "Governance Candidate")
	mux := http.NewServeMux()
	registerCRMFundsGovernanceRoutes(mux, store, "operator-secret")

	response := fundsRouteRequest(
		t, mux, http.MethodPost, "/api/ops/fund_callbacks",
		`{"candidate_id":"`+candidate.ID+`","amount_minor":1,"currency":"CNY","condition":"x","promised_by":"y","unknown":true}`,
		"", "127.0.0.1:10000",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, body = %s", response.Code, response.Body.String())
	}

	for _, path := range []string{
		"/api/ops/governance/audit?limit=20",
		"/api/ops/governance/candidates/" + candidate.ID + "/export",
		"/api/ops/reports/summary",
		"/api/ops/reports/export.csv",
	} {
		response = fundsRouteRequest(t, mux, http.MethodGet, path, "", "", "127.0.0.1:10000")
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}

	anonymizePath := "/api/ops/governance/candidates/" + candidate.ID + "/anonymize"
	response = fundsRouteRequest(
		t, mux, http.MethodPost, anonymizePath,
		`{"expected_version":1,"confirmed":true}`,
		"operator-secret", "127.0.0.1:10000",
	)
	if response.Code != http.StatusOK {
		t.Fatalf("anonymize status = %d, body = %s", response.Code, response.Body.String())
	}
}

func fundsRouteRequest(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	body string,
	token string,
	remoteAddr string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func jsonNumber(value uint64) string {
	data, _ := json.Marshal(value)
	return string(data)
}
