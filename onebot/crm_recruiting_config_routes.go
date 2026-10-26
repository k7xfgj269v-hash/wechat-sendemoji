package main

import (
	"errors"
	"net/http"
	"strings"
)

type recruitingProjectRouteUpdate struct {
	ExpectedVersion         uint64  `json:"expected_version"`
	OrganizationName        *string `json:"organization_name,omitempty"`
	RequireAuthoritativeJob *bool   `json:"require_authoritative_job,omitempty"`
}

type recruitingSourceCreateRequest struct {
	ExpectedProjectVersion uint64 `json:"expected_project_version"`
	GroupID                string `json:"group_id"`
	DisplayName            string `json:"display_name,omitempty"`
	Enabled                *bool  `json:"enabled,omitempty"`
	GateEnabled            *bool  `json:"gate_enabled,omitempty"`
	GateMode               string `json:"gate_mode,omitempty"`
}

type recruitingSourceUpdateRequest struct {
	ExpectedProjectVersion uint64  `json:"expected_project_version"`
	GroupID                *string `json:"group_id,omitempty"`
	DisplayName            *string `json:"display_name,omitempty"`
	Enabled                *bool   `json:"enabled,omitempty"`
	GateEnabled            *bool   `json:"gate_enabled,omitempty"`
	GateMode               *string `json:"gate_mode,omitempty"`
}

type recruitingSourceRemoveRequest struct {
	ExpectedProjectVersion uint64 `json:"expected_project_version"`
}

func registerCRMRecruitingConfigRoutes(
	mux *http.ServeMux,
	store *CRMStore,
	operatorToken string,
) {
	mux.HandleFunc(
		"/api/crm/recruiting/project",
		automationOperatorAuth(operatorToken, crmRecruitingProjectHandler(store)),
	)
	mux.HandleFunc(
		"/api/crm/recruiting/source_groups",
		automationOperatorAuth(operatorToken, crmRecruitingSourceCollectionHandler(store)),
	)
	mux.HandleFunc(
		"/api/crm/recruiting/source_groups/",
		automationOperatorAuth(operatorToken, crmRecruitingSourceItemHandler(store)),
	)
}

func crmRecruitingProjectHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/recruiting/project" {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "recruiting project endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, store.RecruitingProject())
		case http.MethodPatch:
			var request recruitingProjectRouteUpdate
			if err := crmCandidateDecodeRequest(w, r, &request); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			project, err := store.UpdateRecruitingProject(
				request.ExpectedVersion,
				RecruitingProjectUpdate{
					OrganizationName:        request.OrganizationName,
					RequireAuthoritativeJob: request.RequireAuthoritativeJob,
				},
			)
			if err != nil {
				crmRecruitingWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, project)
		default:
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmRecruitingSourceCollectionHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/recruiting/source_groups" {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "recruiting source endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			project := store.RecruitingProject()
			writeJSON(w, http.StatusOK, map[string]any{
				"project_version": project.Version,
				"source_groups":   project.SourceGroups,
			})
		case http.MethodPost:
			var request recruitingSourceCreateRequest
			if err := crmCandidateDecodeRequest(w, r, &request); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			enabled := true
			if request.Enabled != nil {
				enabled = *request.Enabled
			}
			gateEnabled := false
			if request.GateEnabled != nil {
				gateEnabled = *request.GateEnabled
			}
			project, err := store.UpsertRecruitingSourceGroup(
				request.ExpectedProjectVersion,
				RecruitingSourceGroupUpsert{
					GroupID:     request.GroupID,
					DisplayName: request.DisplayName,
					Enabled:     enabled,
					GateEnabled: gateEnabled,
					GateMode:    request.GateMode,
				},
			)
			if err != nil {
				crmRecruitingWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, project)
		default:
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmRecruitingSourceItemHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sourceID := strings.TrimPrefix(r.URL.Path, "/api/crm/recruiting/source_groups/")
		if sourceID == "" || strings.Contains(sourceID, "/") {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "recruiting source group not found")
			return
		}
		project := store.RecruitingProject()
		index := recruitingSourceGroupIndexByID(project.SourceGroups, sourceID)
		if index < 0 {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "recruiting source group not found")
			return
		}
		source := project.SourceGroups[index]

		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, source)
		case http.MethodPatch:
			var request recruitingSourceUpdateRequest
			if err := crmCandidateDecodeRequest(w, r, &request); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			if request.GroupID == nil &&
				request.DisplayName == nil &&
				request.Enabled == nil &&
				request.GateEnabled == nil &&
				request.GateMode == nil {
				crmCandidateWriteMappedError(w, NewCRMValidationError("update", "must contain a field"))
				return
			}
			input := RecruitingSourceGroupUpsert{
				ID:          source.ID,
				GroupID:     source.GroupID,
				DisplayName: source.DisplayName,
				Enabled:     source.Enabled,
				GateEnabled: source.GateEnabled,
				GateMode:    source.GateMode,
			}
			if request.GroupID != nil {
				input.GroupID = *request.GroupID
			}
			if request.DisplayName != nil {
				input.DisplayName = *request.DisplayName
			}
			if request.Enabled != nil {
				input.Enabled = *request.Enabled
			}
			if request.GateEnabled != nil {
				input.GateEnabled = *request.GateEnabled
			}
			if request.GateMode != nil {
				input.GateMode = *request.GateMode
			}
			updated, err := store.UpsertRecruitingSourceGroup(request.ExpectedProjectVersion, input)
			if err != nil {
				crmRecruitingWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, updated)
		case http.MethodDelete:
			var request recruitingSourceRemoveRequest
			if err := crmCandidateDecodeRequest(w, r, &request); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			updated, err := store.RemoveRecruitingSourceGroup(
				source.GroupID,
				request.ExpectedProjectVersion,
			)
			if err != nil {
				crmRecruitingWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, updated)
		default:
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodDelete)
		}
	}
}

func crmRecruitingWriteMappedError(w http.ResponseWriter, err error) {
	var notFound *recruitingSourceGroupNotFoundError
	if errors.As(err, &notFound) {
		crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, notFound.Error())
		return
	}
	crmCandidateWriteMappedError(w, err)
}
