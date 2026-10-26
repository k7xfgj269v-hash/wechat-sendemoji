package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestRecruitingConfigRoutesRequireLoopbackOperatorAuth(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mux := http.NewServeMux()
	registerCRMRecruitingConfigRoutes(mux, store, "operator-secret")

	response := recruitingConfigRouteRequest(
		t, mux, http.MethodGet, "/api/crm/recruiting/project", nil, "", "127.0.0.1:12000",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, body = %s", response.Code, response.Body.String())
	}
	response = recruitingConfigRouteRequest(
		t, mux, http.MethodGet, "/api/crm/recruiting/project", nil, "operator-secret", "10.0.0.2:12000",
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, body = %s", response.Code, response.Body.String())
	}
	response = recruitingConfigRouteRequest(
		t, mux, http.MethodGet, "/api/crm/recruiting/project", nil, "operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(CRMDefaultOrganizationName)) {
		t.Fatalf("authorized status/body = %d %s", response.Code, response.Body.String())
	}
}

func TestRecruitingConfigRoutesCRUDStaleRecoveryAndStrictJSON(t *testing.T) {
	store, err := OpenCRMStore(filepath.Join(t.TempDir(), "crm.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mux := http.NewServeMux()
	registerCRMRecruitingConfigRoutes(mux, store, "operator-secret")

	response := recruitingConfigRouteRequest(
		t, mux, http.MethodPatch, "/api/crm/recruiting/project",
		map[string]any{
			"expected_version":  1,
			"organization_name": "国爱人力测试组织",
			"unknown":           true,
		},
		"operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, body = %s", response.Code, response.Body.String())
	}

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodPatch, "/api/crm/recruiting/project",
		map[string]any{
			"expected_version":  1,
			"organization_name": "国爱人力测试组织",
		},
		"operator-secret", "127.0.0.1:12000",
	)
	project := decodeRecruitingProjectResponse(t, response, http.StatusOK)
	if project.Version != 2 || project.OrganizationName != "国爱人力测试组织" {
		t.Fatalf("updated project = %#v", project)
	}

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodPost, "/api/crm/recruiting/source_groups",
		map[string]any{
			"expected_project_version": project.Version,
			"group_id":                 "not-a-group",
			"display_name":             "非法",
		},
		"operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid group status = %d, body = %s", response.Code, response.Body.String())
	}

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodPost, "/api/crm/recruiting/source_groups",
		map[string]any{
			"expected_project_version": project.Version,
			"group_id":                 "synthetic_jobs@chatroom",
			"display_name":             "合成职位来源",
			"enabled":                  true,
			"gate_enabled":             true,
			"gate_mode":                RecruitingSourceGateModeJobRelevance,
		},
		"operator-secret", "127.0.0.1:12000",
	)
	project = decodeRecruitingProjectResponse(t, response, http.StatusCreated)
	if project.Version != 3 ||
		len(project.SourceGroups) != 1 ||
		!project.SourceGroups[0].GateEnabled ||
		project.SourceGroups[0].GateMode != RecruitingSourceGateModeJobRelevance {
		t.Fatalf("created project = %#v", project)
	}
	source := project.SourceGroups[0]

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodPost, "/api/crm/recruiting/source_groups",
		map[string]any{
			"expected_project_version": project.Version,
			"group_id":                 source.GroupID,
			"display_name":             "重复来源",
		},
		"operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, body = %s", response.Code, response.Body.String())
	}

	itemPath := "/api/crm/recruiting/source_groups/" + source.ID
	response = recruitingConfigRouteRequest(
		t, mux, http.MethodPatch, itemPath,
		map[string]any{
			"expected_project_version": 2,
			"display_name":             "过期编辑",
		},
		"operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale update status = %d, body = %s", response.Code, response.Body.String())
	}

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodPatch, itemPath,
		map[string]any{
			"expected_project_version": project.Version,
			"display_name":             "合成职位订单",
			"enabled":                  false,
			"gate_enabled":             false,
			"gate_mode":                RecruitingSourceGateModeJobRelevance,
		},
		"operator-secret", "127.0.0.1:12000",
	)
	project = decodeRecruitingProjectResponse(t, response, http.StatusOK)
	if project.Version != 4 ||
		project.SourceGroups[0].DisplayName != "合成职位订单" ||
		project.SourceGroups[0].Enabled ||
		project.SourceGroups[0].GateEnabled {
		t.Fatalf("patched project = %#v", project)
	}

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodDelete, itemPath,
		map[string]any{"expected_project_version": 3},
		"operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale delete status = %d, body = %s", response.Code, response.Body.String())
	}
	response = recruitingConfigRouteRequest(
		t, mux, http.MethodDelete, itemPath,
		map[string]any{"expected_project_version": project.Version},
		"operator-secret", "127.0.0.1:12000",
	)
	project = decodeRecruitingProjectResponse(t, response, http.StatusOK)
	if project.Version != 5 || len(project.SourceGroups) != 0 {
		t.Fatalf("deleted project = %#v", project)
	}

	response = recruitingConfigRouteRequest(
		t, mux, http.MethodGet, "/api/crm/recruiting/source_groups", nil,
		"operator-secret", "127.0.0.1:12000",
	)
	if response.Code != http.StatusOK ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"project_version":5`)) ||
		!bytes.Contains(response.Body.Bytes(), []byte(`"source_groups":[]`)) {
		t.Fatalf("list status/body = %d %s", response.Code, response.Body.String())
	}
}

func TestRecruitingConfigRoutesPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.json")
	store, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	registerCRMRecruitingConfigRoutes(mux, store, "operator-secret")

	response := recruitingConfigRouteRequest(
		t, mux, http.MethodPost, "/api/crm/recruiting/source_groups",
		map[string]any{
			"expected_project_version": 1,
			"group_id":                 "persisted_jobs@chatroom",
			"display_name":             "持久化职位来源",
			"enabled":                  true,
			"gate_enabled":             true,
			"gate_mode":                RecruitingSourceGateModeJobRelevance,
		},
		"operator-secret", "127.0.0.1:12000",
	)
	project := decodeRecruitingProjectResponse(t, response, http.StatusCreated)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenCRMStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	persisted := reopened.RecruitingProject()
	if persisted.Version != project.Version ||
		len(persisted.SourceGroups) != 1 ||
		persisted.SourceGroups[0].GroupID != "persisted_jobs@chatroom" ||
		!persisted.SourceGroups[0].GateEnabled ||
		persisted.SourceGroups[0].GateMode != RecruitingSourceGateModeJobRelevance ||
		len(reopened.Snapshot().AuditEvents) != 1 {
		t.Fatalf("persisted project/state = %#v / %#v", persisted, reopened.Snapshot())
	}
}

func recruitingConfigRouteRequest(
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
	request := httptest.NewRequest(method, path, &payload)
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeRecruitingProjectResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
) RecruitingProjectConfig {
	t.Helper()
	if response.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", response.Code, wantStatus, response.Body.String())
	}
	var project RecruitingProjectConfig
	if err := json.Unmarshal(response.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	return project
}
