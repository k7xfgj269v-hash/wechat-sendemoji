package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type crmServiceRequestAPIError struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

type crmServiceRequestRequestError struct {
	status  int
	code    string
	message string
}

func (e *crmServiceRequestRequestError) Error() string {
	return e.message
}

func registerCRMServiceRequestRoutes(mux *http.ServeMux, store *CRMStore, operatorTokens ...string) {
	operatorToken := ""
	if len(operatorTokens) != 0 {
		operatorToken = operatorTokens[0]
	}
	mux.HandleFunc("/api/ops/service_requests", crmServiceRequestsHandler(store))
	mux.HandleFunc("/api/ops/service_requests/", crmServiceRequestItemHandler(store, operatorToken))
	mux.HandleFunc("/api/ops/service_queues", crmServiceQueuesHandler(store))
	mux.HandleFunc("/api/ops/service_queues/", crmServiceQueueItemHandler(store))
}

func crmServiceRequestsHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/service_requests" {
			crmServiceRequestWriteError(w, http.StatusNotFound, "not_found", "service request endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			filter, err := crmServiceRequestParseFilter(r.URL.Query())
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			total, requests, err := crmServiceRequestList(store, filter)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"total":            total,
				"service_requests": requests,
			})
		case http.MethodPost:
			var create ServiceRequestCreate
			if err := crmServiceRequestDecodeRequest(w, r, &create); err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			request, err := crmServiceRequestCreate(store, create)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, request)
		default:
			crmServiceRequestWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmServiceRequestItemHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/ops/service_requests/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 2 || parts[0] == "" {
			crmServiceRequestWriteError(w, http.StatusNotFound, "not_found", "service request not found")
			return
		}
		id := parts[0]
		if len(parts) == 2 {
			if parts[1] == "claim" {
				if r.Method != http.MethodPost {
					crmServiceRequestWriteMethodNotAllowed(w, http.MethodPost)
					return
				}
				crmOperatorAuth(operatorToken, crmServiceRequestClaimHandler(store, id))(w, r)
				return
			}
			if parts[1] != "transition" {
				crmServiceRequestWriteError(w, http.StatusNotFound, "not_found", "service request endpoint not found")
				return
			}
			if r.Method != http.MethodPost {
				crmServiceRequestWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var transition ServiceRequestTransition
			if err := crmServiceRequestDecodeRequest(w, r, &transition); err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			handle := func(w http.ResponseWriter, _ *http.Request) {
				request, err := crmServiceRequestTransition(store, id, transition)
				if err != nil {
					crmServiceRequestWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, request)
			}
			current, err := crmServiceRequestGet(store, id)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			if crmProtectedServiceRequestTransition(current, transition.ToStatus) {
				crmOperatorAuth(operatorToken, func(w http.ResponseWriter, r *http.Request) {
					actor, err := crmOperatorIDFromRequest(r)
					if err != nil {
						crmServiceRequestWriteMappedError(w, err)
						return
					}
					transition.Actor = actor
					handle(w, r)
				})(w, r)
				return
			}
			handle(w, r)
			return
		}

		switch r.Method {
		case http.MethodGet:
			request, err := crmServiceRequestGet(store, id)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, request)
		case http.MethodPatch:
			var update ServiceRequestUpdate
			if err := crmServiceRequestDecodeRequest(w, r, &update); err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			request, err := crmServiceRequestUpdate(store, id, update)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, request)
		default:
			crmServiceRequestWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmProtectedServiceRequestTransition(
	current ServiceRequest,
	toStatus ServiceRequestStatus,
) bool {
	return current.RequiresHuman ||
		toStatus == ServiceRequestStatusClosed ||
		toStatus == ServiceRequestStatusCancelled
}

func crmServiceQueuesHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/service_queues" {
			crmServiceRequestWriteError(w, http.StatusNotFound, "not_found", "service queue endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if len(r.URL.Query()) != 0 {
				crmServiceRequestWriteMappedError(w, NewCRMValidationError("query", "is not supported"))
				return
			}
			queues, err := crmServiceQueueList(store)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"total":          len(queues),
				"service_queues": queues,
			})
		case http.MethodPost:
			var create ServiceQueueCreate
			if err := crmServiceRequestDecodeRequest(w, r, &create); err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			queue, err := crmServiceQueueCreate(store, create, "system")
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, queue)
		default:
			crmServiceRequestWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmServiceQueueItemHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/ops/service_queues/")
		if id == "" || strings.Contains(id, "/") {
			crmServiceRequestWriteError(w, http.StatusNotFound, "not_found", "service queue not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			queue, err := crmServiceQueueGet(store, id)
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, queue)
		case http.MethodPatch:
			var update ServiceQueueUpdate
			if err := crmServiceRequestDecodeRequest(w, r, &update); err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			queue, err := crmServiceQueueUpdate(store, id, update, "system")
			if err != nil {
				crmServiceRequestWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, queue)
		default:
			crmServiceRequestWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmServiceRequestClaimHandler(store *CRMStore, id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := crmOperatorIDFromRequest(r)
		if err != nil {
			crmServiceRequestWriteMappedError(w, err)
			return
		}
		var claim ServiceRequestClaim
		if err := crmServiceRequestDecodeRequest(w, r, &claim); err != nil {
			crmServiceRequestWriteMappedError(w, err)
			return
		}
		request, err := crmServiceRequestClaim(store, id, claim, actor)
		if err != nil {
			crmServiceRequestWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, request)
	}
}

func crmServiceRequestParseFilter(query url.Values) (crmServiceRequestFilter, error) {
	allowed := map[string]bool{
		"q": true, "candidate_id": true, "job_id": true, "onboarding_case_id": true,
		"category": true, "priority": true, "status": true, "owner": true,
		"requires_human": true, "overdue": true, "due_from": true, "due_to": true,
		"limit": true, "offset": true,
	}
	for name := range query {
		if !allowed[name] {
			return crmServiceRequestFilter{}, NewCRMValidationError(name, "unknown query parameter")
		}
	}

	var filter crmServiceRequestFilter
	var err error
	if filter.Query, _, err = crmServiceRequestQueryValue(query, "q", false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.CandidateID, _, err = crmServiceRequestQueryValue(query, "candidate_id", false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.JobID, _, err = crmServiceRequestQueryValue(query, "job_id", false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.OnboardingCaseID, _, err = crmServiceRequestQueryValue(query, "onboarding_case_id", false); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if value, ok, queryErr := crmServiceRequestQueryValue(query, "category", false); queryErr != nil {
		return crmServiceRequestFilter{}, queryErr
	} else if ok {
		filter.Category = ServiceRequestCategory(value)
	}
	if value, ok, queryErr := crmServiceRequestQueryValue(query, "priority", false); queryErr != nil {
		return crmServiceRequestFilter{}, queryErr
	} else if ok {
		filter.Priority = JobPriority(value)
	}
	if value, ok, queryErr := crmServiceRequestQueryValue(query, "status", false); queryErr != nil {
		return crmServiceRequestFilter{}, queryErr
	} else if ok {
		filter.Status = ServiceRequestStatus(value)
	}
	if value, ok, queryErr := crmServiceRequestQueryValue(query, "owner", true); queryErr != nil {
		return crmServiceRequestFilter{}, queryErr
	} else if ok {
		filter.Owner = &value
	}
	if filter.RequiresHuman, err = crmServiceRequestBoolQuery(query, "requires_human"); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.Overdue, err = crmServiceRequestBoolQuery(query, "overdue"); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.DueFrom, err = crmServiceRequestTimeQuery(query, "due_from"); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.DueTo, err = crmServiceRequestTimeQuery(query, "due_to"); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.Limit, err = crmServiceRequestIntQuery(
		query,
		"limit",
		CRMDefaultListLimit,
		1,
		CRMMaxListLimit,
	); err != nil {
		return crmServiceRequestFilter{}, err
	}
	if filter.Offset, err = crmServiceRequestIntQuery(query, "offset", 0, 0, int(^uint(0)>>1)); err != nil {
		return crmServiceRequestFilter{}, err
	}
	return filter, nil
}

func crmServiceRequestQueryValue(
	query url.Values,
	name string,
	allowEmpty bool,
) (string, bool, error) {
	values, ok := query[name]
	if !ok {
		return "", false, nil
	}
	if len(values) != 1 {
		return "", false, NewCRMValidationError(name, "must be specified once")
	}
	value := strings.TrimSpace(values[0])
	if value == "" && !allowEmpty {
		return "", false, NewCRMValidationError(name, "is required")
	}
	return value, true, nil
}

func crmServiceRequestBoolQuery(query url.Values, name string) (*bool, error) {
	raw, ok, err := crmServiceRequestQueryValue(query, name, false)
	if err != nil || !ok {
		return nil, err
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return nil, NewCRMValidationError(name, "must be true or false")
	}
	return &value, nil
}

func crmServiceRequestTimeQuery(query url.Values, name string) (*time.Time, error) {
	raw, ok, err := crmServiceRequestQueryValue(query, name, false)
	if err != nil || !ok {
		return nil, err
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || value.IsZero() {
		return nil, NewCRMValidationError(name, "must be an RFC3339 timestamp")
	}
	value = value.UTC()
	return &value, nil
}

func crmServiceRequestIntQuery(
	query url.Values,
	name string,
	defaultValue int,
	minimum int,
	maximum int,
) (int, error) {
	raw, ok, err := crmServiceRequestQueryValue(query, name, false)
	if err != nil {
		return 0, err
	}
	if !ok {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, NewCRMValidationError(
			name,
			fmt.Sprintf("must be between %d and %d", minimum, maximum),
		)
	}
	return value, nil
}

func crmServiceRequestDecodeRequest(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, CRMMaxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return crmServiceRequestDecodeError(err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &crmServiceRequestRequestError{
				status:  http.StatusBadRequest,
				code:    "invalid_json",
				message: "request body must contain one JSON object",
			}
		}
		return crmServiceRequestDecodeError(err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return &crmServiceRequestRequestError{
			status:  http.StatusBadRequest,
			code:    "invalid_json",
			message: "request body must be a JSON object",
		}
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(destination); err != nil {
		return crmServiceRequestDecodeError(err)
	}
	return nil
}

func crmServiceRequestDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &crmServiceRequestRequestError{
			status:  http.StatusRequestEntityTooLarge,
			code:    "body_too_large",
			message: "request body exceeds 1 MiB",
		}
	}
	return &crmServiceRequestRequestError{
		status:  http.StatusBadRequest,
		code:    "invalid_json",
		message: "invalid JSON: " + err.Error(),
	}
}

func crmServiceRequestWriteMappedError(w http.ResponseWriter, err error) {
	var requestError *crmServiceRequestRequestError
	switch {
	case errors.As(err, &requestError):
		crmServiceRequestWriteError(w, requestError.status, requestError.code, requestError.message)
	case errors.Is(err, ErrCRMValidation):
		crmServiceRequestWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
	case crmServiceRequestIsNotFound(err):
		crmServiceRequestWriteError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrCRMConflict):
		crmServiceRequestWriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrCRMPersistence), errors.Is(err, ErrCRMStoreClosed):
		crmServiceRequestWriteError(w, http.StatusInternalServerError, "persistence_error", "CRM persistence failed")
	default:
		crmServiceRequestWriteError(w, http.StatusInternalServerError, "persistence_error", "CRM persistence failed")
	}
}

func crmServiceRequestWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	crmServiceRequestWriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func crmServiceRequestWriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, crmServiceRequestAPIError{Code: code, Error: message})
}
