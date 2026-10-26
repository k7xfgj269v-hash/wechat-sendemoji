package main

import (
	"net/http"
	"strings"
)

func registerCRMOnboardingFleetRoutes(mux *http.ServeMux, store *CRMStore, operatorTokens ...string) {
	operatorToken := ""
	if len(operatorTokens) != 0 {
		operatorToken = operatorTokens[0]
	}
	mux.HandleFunc("/api/ops/onboarding_cases", crmOnboardingCasesHandler(store))
	mux.HandleFunc("/api/ops/onboarding_cases/", crmOnboardingCaseHandler(store, operatorToken))
}

func crmOnboardingCasesHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/onboarding_cases" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "onboarding endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			filter, err := crmOnboardingParseFilter(r)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			total, cases, err := crmOnboardingList(store, filter)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"total": total, "onboarding_cases": cases})
		case http.MethodPost:
			var create OnboardingCreate
			if err := crmPipelineDecodeRequest(w, r, &create); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			onboarding, err := crmOnboardingCreate(store, create)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, onboarding)
		default:
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmOnboardingCaseHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/ops/onboarding_cases/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 3 {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "onboarding case not found")
			return
		}
		id := parts[0]
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				onboarding, err := crmOnboardingGet(store, id)
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, onboarding)
			case http.MethodPatch:
				var update OnboardingUpdate
				if err := crmPipelineDecodeRequest(w, r, &update); err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				onboarding, err := crmOnboardingUpdate(store, id, update)
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, onboarding)
			default:
				crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
			}
			return
		}
		if len(parts) == 2 && parts[1] == "transition" {
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var transition OnboardingTransition
			if err := crmPipelineDecodeRequest(w, r, &transition); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			handler := crmOnboardingTransitionHandler(store, id, transition, "system")
			if crmProtectedOnboardingStatus(transition.ToStatus) {
				handler = crmOperatorAuth(operatorToken, func(w http.ResponseWriter, r *http.Request) {
					actor, err := crmOperatorIDFromRequest(r)
					if err != nil {
						crmPipelineWriteMappedError(w, err)
						return
					}
					crmOnboardingTransitionHandler(store, id, transition, actor)(w, r)
				})
			}
			handler(w, r)
			return
		}
		if len(parts) == 2 && parts[1] == "fleet" {
			if r.Method != http.MethodPatch {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPatch)
				return
			}
			var update FleetUpdate
			if err := crmPipelineDecodeRequest(w, r, &update); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			onboarding, err := crmFleetUpdate(store, id, update)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, onboarding)
			return
		}
		if len(parts) == 3 && parts[1] == "fleet" && parts[2] == "transition" {
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var transition FleetTransition
			if err := crmPipelineDecodeRequest(w, r, &transition); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			handler := crmFleetTransitionHandler(store, id, transition, "system")
			if crmProtectedFleetStatus(transition.ToStatus) {
				handler = crmOperatorAuth(operatorToken, func(w http.ResponseWriter, r *http.Request) {
					actor, err := crmOperatorIDFromRequest(r)
					if err != nil {
						crmPipelineWriteMappedError(w, err)
						return
					}
					crmFleetTransitionHandler(store, id, transition, actor)(w, r)
				})
			}
			handler(w, r)
			return
		}
		crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "onboarding endpoint not found")
	}
}

func crmOnboardingTransitionHandler(
	store *CRMStore,
	id string,
	transition OnboardingTransition,
	actor string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		transition.Actor = actor
		onboarding, err := crmOnboardingTransition(store, id, transition)
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, onboarding)
	}
}

func crmFleetTransitionHandler(
	store *CRMStore,
	id string,
	transition FleetTransition,
	actor string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		transition.Actor = actor
		onboarding, err := crmFleetTransition(store, id, transition)
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, onboarding)
	}
}

func crmOnboardingParseFilter(r *http.Request) (crmOnboardingFilter, error) {
	leadID, err := crmPipelineSingleQueryValue(r, "lead_id")
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	candidateID, err := crmPipelineSingleQueryValue(r, "candidate_id")
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	jobID, err := crmPipelineSingleQueryValue(r, "job_id")
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	status, err := crmPipelineSingleQueryValue(r, "status")
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	fleetStatus, err := crmPipelineSingleQueryValue(r, "fleet_status")
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	rawFleetRequired, err := crmPipelineSingleQueryValue(r, "fleet_required")
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	fleetRequired, err := crmParseOptionalBoolean(rawFleetRequired)
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	limit, offset, err := crmPipelinePagination(r)
	if err != nil {
		return crmOnboardingFilter{}, err
	}
	return crmOnboardingFilter{
		LeadID:        leadID,
		CandidateID:   candidateID,
		JobID:         jobID,
		Status:        OnboardingStatus(strings.TrimSpace(status)),
		FleetStatus:   FleetStatus(strings.TrimSpace(fleetStatus)),
		FleetRequired: fleetRequired,
		Limit:         limit,
		Offset:        offset,
	}, nil
}
