package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	crmPipelineCodeInvalidJSON      = "invalid_json"
	crmPipelineCodeValidationError  = "validation_error"
	crmPipelineCodeNotFound         = "not_found"
	crmPipelineCodeConflict         = "conflict"
	crmPipelineCodePersistenceError = "persistence_error"
	crmPipelineCodeMethodNotAllowed = "method_not_allowed"
	crmPipelineCodeBodyTooLarge     = "body_too_large"
)

type crmPipelineRequestError struct {
	status  int
	code    string
	message string
}

type crmPipelineLeadTransitionRequest struct {
	ExpectedVersion uint64    `json:"expected_version"`
	FromStage       LeadStage `json:"from_stage"`
	ToStage         LeadStage `json:"to_stage"`
	Reason          string    `json:"reason,omitempty"`
	Confirmed       bool      `json:"confirmed,omitempty"`
}

type crmPipelineLeadResponse struct {
	Lead               Lead             `json:"lead"`
	CandidateName      string           `json:"candidate_name"`
	JobTitle           string           `json:"job_title"`
	StageEvents        []LeadStageEvent `json:"stage_events"`
	AllowedTransitions []LeadStage      `json:"allowed_transitions"`
}

type crmPipelineStageResponse struct {
	Stage LeadStage                 `json:"stage"`
	Leads []crmPipelineLeadResponse `json:"leads"`
}

type crmPipelineBoardResponse struct {
	Stages []crmPipelineStageResponse `json:"stages"`
	Total  int                        `json:"total"`
}

type crmPipelineOperatorContextKey struct{}

func (e *crmPipelineRequestError) Error() string {
	return e.message
}

func registerCRMPipelineRoutes(mux *http.ServeMux, store *CRMStore, operatorTokens ...string) {
	operatorToken := ""
	if len(operatorTokens) != 0 {
		operatorToken = strings.TrimSpace(operatorTokens[0])
	}
	mux.HandleFunc("/api/crm/jobs", crmPipelineJobsHandler(store))
	mux.HandleFunc("/api/crm/jobs/", crmPipelineJobHandler(store, operatorToken))
	mux.HandleFunc("/api/crm/leads", crmPipelineLeadsHandler(store))
	mux.HandleFunc("/api/crm/leads/", crmPipelineLeadHandler(store, operatorToken))
	mux.HandleFunc("/api/crm/pipeline", crmPipelineBoardHandler(store))
}

func crmPipelineJobsHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/jobs" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "job endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			filter, err := crmPipelineParseJobFilter(r)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			total, jobs, err := crmPipelineListJobs(store, filter)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"total": total, "jobs": jobs})
		case http.MethodPost:
			var draft JobDraft
			if err := crmPipelineDecodeRequest(w, r, &draft); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			job, err := crmPipelineCreateJob(store, draft)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, job)
		default:
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmPipelineJobHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/crm/jobs/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 4 || strings.TrimSpace(parts[0]) == "" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "job not found")
			return
		}
		id := parts[0]
		if len(parts) >= 2 && parts[1] == "revisions" {
			crmPipelineJobRevisionsHandler(store, operatorToken, id, parts[2:])(w, r)
			return
		}
		if len(parts) == 2 {
			if parts[1] != "status" {
				crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "job endpoint not found")
				return
			}
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var request crmPipelineJobStatusRequest
			if err := crmPipelineDecodeRequest(w, r, &request); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			job, err := crmPipelineSetJobStatus(store, id, request)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, job)
			return
		}
		if len(parts) != 1 {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "job endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			job, err := crmPipelineGetJob(store, id)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, job)
		case http.MethodPatch:
			var update JobUpdate
			if err := crmPipelineDecodeRequest(w, r, &update); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			job, err := crmPipelineUpdateJob(store, id, update)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, job)
		default:
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmPipelineJobRevisionsHandler(
	store *CRMStore,
	operatorToken, jobID string,
	tail []string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch len(tail) {
		case 0:
			if r.Method != http.MethodGet {
				crmPipelineWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			revisions, err := crmPipelineListDemandRevisions(store, jobID)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			job, err := crmPipelineStoredJob(store, jobID)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"job_id":             job.ID,
				"active_revision_id": job.ActiveDemandRevisionID,
				"total":              len(revisions),
				"revisions":          revisions,
			})
		case 1:
			if tail[0] == "" {
				crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "demand revision not found")
				return
			}
			if tail[0] == "compare" {
				if r.Method != http.MethodGet {
					crmPipelineWriteMethodNotAllowed(w, http.MethodGet)
					return
				}
				from, err := crmPipelineSingleQueryValue(r, "from")
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				to, err := crmPipelineSingleQueryValue(r, "to")
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				comparison, err := crmPipelineCompareDemandRevisions(store, jobID, from, to)
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, comparison)
				return
			}
			if r.Method != http.MethodGet {
				crmPipelineWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			revision, err := crmPipelineGetDemandRevision(store, jobID, tail[0])
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, revision)
		case 2:
			if tail[0] == "" || tail[1] != "activate" {
				crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "demand revision endpoint not found")
				return
			}
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			crmPipelineOperatorAuth(operatorToken, func(w http.ResponseWriter, r *http.Request) {
				var request crmPipelineDemandRevisionActivation
				if err := crmPipelineDecodeRequest(w, r, &request); err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				job, err := crmPipelineActivateDemandRevision(
					store,
					jobID,
					tail[0],
					request,
					crmPipelineOperatorIdentity(r),
				)
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, job)
			})(w, r)
		default:
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "demand revision endpoint not found")
		}
	}
}

func crmPipelineLeadsHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/leads" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "lead endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			filter, err := crmPipelineParseLeadFilter(r)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			total, leads, err := crmPipelineListLeads(store, filter)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"total": total,
				"leads": crmPipelineLeadResponses(leads),
			})
		case http.MethodPost:
			var create LeadCreate
			if err := crmPipelineDecodeRequest(w, r, &create); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			lead, err := crmPipelineCreateLead(store, create)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, lead)
		default:
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmPipelineLeadHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/crm/leads/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 2 {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "lead not found")
			return
		}
		id := parts[0]
		if len(parts) == 2 {
			if parts[1] != "transition" {
				crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "lead endpoint not found")
				return
			}
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var request crmPipelineLeadTransitionRequest
			if err := crmPipelineDecodeRequest(w, r, &request); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			transition := LeadTransition{
				ExpectedVersion: request.ExpectedVersion,
				FromStage:       request.FromStage,
				ToStage:         request.ToStage,
				Reason:          request.Reason,
			}
			handle := func(w http.ResponseWriter, r *http.Request) {
				lead, err := crmPipelineTransitionLeadConfirmed(
					store,
					id,
					transition,
					request.Confirmed,
					crmPipelineOperatorIdentity(r),
				)
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, lead)
			}
			if IsTerminalLeadStage(request.ToStage) {
				crmPipelineOperatorAuth(operatorToken, handle)(w, r)
				return
			}
			handle(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			lead, err := crmPipelineGetLead(store, id)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, crmPipelineLeadResponseFromItem(lead))
		case http.MethodPatch:
			var update LeadUpdate
			if err := crmPipelineDecodeRequest(w, r, &update); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			lead, err := crmPipelineUpdateLead(store, id, update)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, lead)
		default:
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmPipelineBoardHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/pipeline" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "pipeline endpoint not found")
			return
		}
		if r.Method != http.MethodGet {
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		jobID, err := crmPipelineSingleQueryValue(r, "job_id")
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		owner, err := crmPipelineSingleQueryValue(r, "owner")
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		board, err := crmPipelineBoard(store, jobID, owner)
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, crmPipelineBoardResponseFromBoard(board))
	}
}

func crmPipelineOperatorAuth(operatorToken string, next http.HandlerFunc) http.HandlerFunc {
	return automationOperatorAuth(operatorToken, func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), crmPipelineOperatorContextKey{}, "operator")
		next(w, r.WithContext(ctx))
	})
}

func crmPipelineOperatorIdentity(r *http.Request) string {
	operatorID, _ := r.Context().Value(crmPipelineOperatorContextKey{}).(string)
	return operatorID
}

func crmPipelineLeadResponseFromItem(item LeadListItem) crmPipelineLeadResponse {
	return crmPipelineLeadResponse{
		Lead:               item.Lead,
		CandidateName:      item.CandidateName,
		JobTitle:           item.JobTitle,
		StageEvents:        item.StageEvents,
		AllowedTransitions: crmPipelineAllowedTransitions(item.Lead.Stage),
	}
}

func crmPipelineLeadResponses(items []LeadListItem) []crmPipelineLeadResponse {
	responses := make([]crmPipelineLeadResponse, len(items))
	for i := range items {
		responses[i] = crmPipelineLeadResponseFromItem(items[i])
	}
	return responses
}

func crmPipelineBoardResponseFromBoard(board PipelineResponse) crmPipelineBoardResponse {
	response := crmPipelineBoardResponse{
		Stages: make([]crmPipelineStageResponse, len(board.Stages)),
		Total:  board.Total,
	}
	for i := range board.Stages {
		response.Stages[i] = crmPipelineStageResponse{
			Stage: board.Stages[i].Stage,
			Leads: crmPipelineLeadResponses(board.Stages[i].Leads),
		}
	}
	return response
}

func crmPipelineParseJobFilter(r *http.Request) (crmPipelineJobFilter, error) {
	query, err := crmPipelineSingleQueryValue(r, "q")
	if err != nil {
		return crmPipelineJobFilter{}, err
	}
	status, err := crmPipelineSingleQueryValue(r, "status")
	if err != nil {
		return crmPipelineJobFilter{}, err
	}
	priority, err := crmPipelineSingleQueryValue(r, "priority")
	if err != nil {
		return crmPipelineJobFilter{}, err
	}
	siteName, err := crmPipelineSingleQueryValue(r, "site_name")
	if err != nil {
		return crmPipelineJobFilter{}, err
	}
	owner, err := crmPipelineSingleQueryValue(r, "owner")
	if err != nil {
		return crmPipelineJobFilter{}, err
	}
	limit, offset, err := crmPipelinePagination(r)
	if err != nil {
		return crmPipelineJobFilter{}, err
	}
	return crmPipelineJobFilter{
		Query: query, Status: JobStatus(strings.TrimSpace(status)),
		Priority: JobPriority(strings.TrimSpace(priority)), SiteName: siteName, Owner: owner,
		Limit: limit, Offset: offset,
	}, nil
}

func crmPipelineParseLeadFilter(r *http.Request) (crmPipelineLeadFilter, error) {
	candidateID, err := crmPipelineSingleQueryValue(r, "candidate_id")
	if err != nil {
		return crmPipelineLeadFilter{}, err
	}
	jobID, err := crmPipelineSingleQueryValue(r, "job_id")
	if err != nil {
		return crmPipelineLeadFilter{}, err
	}
	stage, err := crmPipelineSingleQueryValue(r, "stage")
	if err != nil {
		return crmPipelineLeadFilter{}, err
	}
	owner, err := crmPipelineSingleQueryValue(r, "owner")
	if err != nil {
		return crmPipelineLeadFilter{}, err
	}
	limit, offset, err := crmPipelinePagination(r)
	if err != nil {
		return crmPipelineLeadFilter{}, err
	}
	return crmPipelineLeadFilter{
		CandidateID: candidateID, JobID: jobID, Stage: LeadStage(strings.TrimSpace(stage)),
		Owner: owner, Limit: limit, Offset: offset,
	}, nil
}

func crmPipelinePagination(r *http.Request) (int, int, error) {
	rawLimit, err := crmPipelineSingleQueryValue(r, "limit")
	if err != nil {
		return 0, 0, err
	}
	rawOffset, err := crmPipelineSingleQueryValue(r, "offset")
	if err != nil {
		return 0, 0, err
	}
	limit, err := crmPipelineParseInteger("limit", rawLimit, CRMDefaultListLimit, 1, CRMMaxListLimit)
	if err != nil {
		return 0, 0, err
	}
	offset, err := crmPipelineParseInteger("offset", rawOffset, 0, 0, int(^uint(0)>>1))
	return limit, offset, err
}

func crmPipelineSingleQueryValue(r *http.Request, name string) (string, error) {
	values, ok := r.URL.Query()[name]
	if !ok {
		return "", nil
	}
	if len(values) != 1 {
		return "", NewCRMValidationError(name, "must be provided once")
	}
	return values[0], nil
}

func crmPipelineParseInteger(name, raw string, fallback, minimum, maximum int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, NewCRMValidationError(name, "is invalid")
	}
	return value, nil
}

func crmPipelineDecodeRequest(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.ContentLength > CRMMaxRequestBodyBytes {
		return &crmPipelineRequestError{
			status: http.StatusRequestEntityTooLarge, code: crmPipelineCodeBodyTooLarge,
			message: "request body is too large",
		}
	}
	body := http.MaxBytesReader(w, r.Body, CRMMaxRequestBodyBytes)
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return crmPipelineDecodeError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &crmPipelineRequestError{
				status: http.StatusBadRequest, code: crmPipelineCodeInvalidJSON,
				message: "request body must contain one JSON object",
			}
		}
		return crmPipelineDecodeError(err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return &crmPipelineRequestError{
			status: http.StatusBadRequest, code: crmPipelineCodeInvalidJSON,
			message: "request body must be a JSON object",
		}
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(destination); err != nil {
		return &crmPipelineRequestError{
			status: http.StatusBadRequest, code: crmPipelineCodeInvalidJSON,
			message: "invalid JSON request body",
		}
	}
	return nil
}

func crmPipelineDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &crmPipelineRequestError{
			status: http.StatusRequestEntityTooLarge, code: crmPipelineCodeBodyTooLarge,
			message: "request body is too large",
		}
	}
	return &crmPipelineRequestError{
		status: http.StatusBadRequest, code: crmPipelineCodeInvalidJSON,
		message: "invalid JSON request body",
	}
}

func crmPipelineWriteMappedError(w http.ResponseWriter, err error) {
	var requestError *crmPipelineRequestError
	if errors.As(err, &requestError) {
		crmPipelineWriteError(w, requestError.status, requestError.code, requestError.message)
		return
	}
	var notFoundError *crmPipelineNotFoundError
	if errors.As(err, &notFoundError) {
		crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, notFoundError.Error())
		return
	}
	switch {
	case errors.Is(err, ErrCRMValidation):
		crmPipelineWriteError(w, http.StatusBadRequest, crmPipelineCodeValidationError, err.Error())
	case errors.Is(err, ErrCRMConflict):
		crmPipelineWriteError(w, http.StatusConflict, crmPipelineCodeConflict, err.Error())
	case errors.Is(err, ErrCRMPersistence), errors.Is(err, ErrCRMStoreClosed):
		crmPipelineWriteError(w, http.StatusInternalServerError, crmPipelineCodePersistenceError, "CRM persistence failed")
	default:
		crmPipelineWriteError(w, http.StatusInternalServerError, crmPipelineCodePersistenceError, "CRM operation failed")
	}
}

func crmPipelineWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	crmPipelineWriteError(w, http.StatusMethodNotAllowed, crmPipelineCodeMethodNotAllowed, "method not allowed")
}

func crmPipelineWriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}
