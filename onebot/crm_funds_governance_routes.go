package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	crmFundsCodeInvalidJSON      = "invalid_json"
	crmFundsCodeValidationError  = "validation_error"
	crmFundsCodeNotFound         = "not_found"
	crmFundsCodeConflict         = "conflict"
	crmFundsCodePersistenceError = "persistence_error"
	crmFundsCodeMethodNotAllowed = "method_not_allowed"
	crmFundsCodeBodyTooLarge     = "body_too_large"
)

type crmFundsRequestError struct {
	status  int
	code    string
	message string
}

func (e *crmFundsRequestError) Error() string {
	return e.message
}

func registerCRMFundsGovernanceRoutes(mux *http.ServeMux, store *CRMStore, operatorToken string) {
	mux.HandleFunc("/api/ops/fund_callbacks", crmFundCallbacksHandler(store))
	mux.HandleFunc("/api/ops/fund_callbacks/", crmFundCallbackHandler(store, operatorToken))
	mux.HandleFunc("/api/ops/governance/audit", crmGovernanceAuditHandler(store))
	mux.HandleFunc("/api/ops/governance/candidates/", crmGovernanceCandidateHandler(store, operatorToken))
	mux.HandleFunc("/api/ops/reports/summary", crmReportSummaryHandler(store))
	mux.HandleFunc("/api/ops/reports/export.csv", crmReportCSVHandler(store))
}

func crmFundCallbacksHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/fund_callbacks" {
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "fund callback endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			filter, err := crmFundsParseCallbackFilter(r)
			if err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			total, callbacks, err := crmFundCallbackList(store, filter)
			if err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"total": total, "limit": filter.Limit, "offset": filter.Offset,
				"fund_callbacks": callbacks,
			})
		case http.MethodPost:
			var create FundCallbackCreate
			if err := crmFundsDecodeRequest(w, r, &create); err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			callback, err := crmFundCallbackCreate(store, create)
			if err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, callback)
		default:
			crmFundsWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmFundCallbackHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/ops/fund_callbacks/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "fund callback not found")
			return
		}
		id := parts[0]
		if len(parts) == 2 {
			if parts[1] == "ledger" {
				if r.Method != http.MethodGet {
					crmFundsWriteMethodNotAllowed(w, http.MethodGet)
					return
				}
				entries, err := crmFundCallbackLedger(store, id)
				if err != nil {
					crmFundsWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"total":  len(entries),
					"ledger": entries,
				})
				return
			}
			if r.Method != http.MethodPost {
				crmFundsWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var protected http.HandlerFunc
			switch parts[1] {
			case "confirm_commitment":
				protected = crmFundCommitmentConfirmationHandler(store, id)
			case "confirm_final":
				protected = crmFundFinalConfirmationHandler(store, id)
			default:
				crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "fund callback endpoint not found")
				return
			}
			crmOperatorAuth(operatorToken, protected)(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			callback, err := crmFundCallbackGet(store, id)
			if err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, callback)
		case http.MethodPatch:
			var update FundCallbackUpdate
			if err := crmFundsDecodeRequest(w, r, &update); err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			callback, err := crmFundCallbackUpdate(store, id, update)
			if err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, callback)
		default:
			crmFundsWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmFundCommitmentConfirmationHandler(store *CRMStore, id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := crmOperatorIDFromRequest(r)
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		var confirmation FundCommitmentConfirmation
		if err := crmFundsDecodeRequest(w, r, &confirmation); err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		confirmation.OperatorID = actor
		callback, err := crmFundCallbackConfirmCommitment(store, id, confirmation)
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, callback)
	}
}

func crmFundFinalConfirmationHandler(store *CRMStore, id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := crmOperatorIDFromRequest(r)
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		var confirmation FundFinalConfirmation
		if err := crmFundsDecodeRequest(w, r, &confirmation); err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		confirmation.OperatorID = actor
		callback, err := crmFundCallbackConfirmFinal(store, id, confirmation)
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, callback)
	}
}

func crmGovernanceAuditHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/governance/audit" {
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "audit endpoint not found")
			return
		}
		if r.Method != http.MethodGet {
			crmFundsWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		filter, err := crmFundsParseAuditFilter(r)
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		total, events, err := crmAuditList(store.Snapshot(), filter)
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"total": total, "limit": filter.Limit, "offset": filter.Offset,
			"audit_events": events,
		})
	}
}

func crmGovernanceCandidateHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/ops/governance/candidates/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "candidate governance endpoint not found")
			return
		}
		id := parts[0]
		switch parts[1] {
		case "export":
			if r.Method != http.MethodGet {
				crmFundsWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			export, err := crmCandidateExport(store.Snapshot(), id)
			if err != nil {
				crmFundsWriteMappedError(w, err)
				return
			}
			w.Header().Set("Content-Disposition", `attachment; filename="candidate-`+id+`.json"`)
			writeJSON(w, http.StatusOK, export)
		case "anonymize":
			if r.Method != http.MethodPost {
				crmFundsWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			crmOperatorAuth(operatorToken, func(w http.ResponseWriter, r *http.Request) {
				actor, err := crmOperatorIDFromRequest(r)
				if err != nil {
					crmFundsWriteMappedError(w, err)
					return
				}
				var request CandidateAnonymizeRequest
				if err := crmFundsDecodeRequest(w, r, &request); err != nil {
					crmFundsWriteMappedError(w, err)
					return
				}
				request.OperatorID = actor
				candidate, err := crmCandidateAnonymize(store, id, request)
				if err != nil {
					crmFundsWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, candidate)
			})(w, r)
		default:
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "candidate governance endpoint not found")
		}
	}
}

func crmReportSummaryHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/reports/summary" {
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "report endpoint not found")
			return
		}
		if r.Method != http.MethodGet {
			crmFundsWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := crmFundsRejectUnknownQuery(r); err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, crmReportSummary(store.Snapshot(), time.Now().UTC()))
	}
}

func crmReportCSVHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/reports/export.csv" {
			crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, "report endpoint not found")
			return
		}
		if r.Method != http.MethodGet {
			crmFundsWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := crmFundsRejectUnknownQuery(r); err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		data, err := crmReportCSV(store.Snapshot())
		if err != nil {
			crmFundsWriteMappedError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="crm-operations.csv"`)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

func crmFundsParseCallbackFilter(r *http.Request) (crmFundCallbackFilter, error) {
	if err := crmFundsRejectUnknownQuery(
		r, "q", "candidate_id", "job_id", "lead_id", "onboarding_case_id",
		"status", "currency", "due_before", "due_after", "limit", "offset",
	); err != nil {
		return crmFundCallbackFilter{}, err
	}
	limit, offset, err := crmFundsPagination(r)
	if err != nil {
		return crmFundCallbackFilter{}, err
	}
	dueBefore, err := crmFundsQueryTime(r, "due_before")
	if err != nil {
		return crmFundCallbackFilter{}, err
	}
	dueAfter, err := crmFundsQueryTime(r, "due_after")
	if err != nil {
		return crmFundCallbackFilter{}, err
	}
	return crmFundCallbackFilter{
		Query:            r.URL.Query().Get("q"),
		CandidateID:      r.URL.Query().Get("candidate_id"),
		JobID:            r.URL.Query().Get("job_id"),
		LeadID:           r.URL.Query().Get("lead_id"),
		OnboardingCaseID: r.URL.Query().Get("onboarding_case_id"),
		Status:           FundCallbackStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
		Currency:         r.URL.Query().Get("currency"),
		DueBefore:        dueBefore,
		DueAfter:         dueAfter,
		Limit:            limit,
		Offset:           offset,
	}, nil
}

func crmFundsParseAuditFilter(r *http.Request) (crmAuditFilter, error) {
	if err := crmFundsRejectUnknownQuery(
		r, "actor", "operation", "entity_type", "entity_id", "source_event_id",
		"created_after", "created_before", "limit", "offset",
	); err != nil {
		return crmAuditFilter{}, err
	}
	limit, offset, err := crmFundsPagination(r)
	if err != nil {
		return crmAuditFilter{}, err
	}
	createdAfter, err := crmFundsQueryTime(r, "created_after")
	if err != nil {
		return crmAuditFilter{}, err
	}
	createdBefore, err := crmFundsQueryTime(r, "created_before")
	if err != nil {
		return crmAuditFilter{}, err
	}
	return crmAuditFilter{
		Actor:         r.URL.Query().Get("actor"),
		Operation:     r.URL.Query().Get("operation"),
		EntityType:    r.URL.Query().Get("entity_type"),
		EntityID:      r.URL.Query().Get("entity_id"),
		SourceEventID: r.URL.Query().Get("source_event_id"),
		CreatedAfter:  createdAfter,
		CreatedBefore: createdBefore,
		Limit:         limit,
		Offset:        offset,
	}, nil
}

func crmFundsRejectUnknownQuery(r *http.Request, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name, values := range r.URL.Query() {
		if _, ok := allowedSet[name]; !ok {
			return NewCRMValidationError(name, "is not supported")
		}
		if len(values) != 1 {
			return NewCRMValidationError(name, "must be provided once")
		}
	}
	return nil
}

func crmFundsPagination(r *http.Request) (int, int, error) {
	limit := CRMDefaultListLimit
	offset := 0
	var err error
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > CRMMaxListLimit {
			return 0, 0, NewCRMValidationError("limit", "must be between 1 and 200")
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, NewCRMValidationError("offset", "must be zero or greater")
		}
	}
	return limit, offset, nil
}

func crmFundsQueryTime(r *http.Request, name string) (*time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, NewCRMValidationError(name, "must use RFC3339")
	}
	value = value.UTC()
	return &value, nil
}

func crmFundsDecodeRequest(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.ContentLength > CRMMaxRequestBodyBytes {
		return &crmFundsRequestError{
			status: http.StatusRequestEntityTooLarge, code: crmFundsCodeBodyTooLarge,
			message: "request body is too large",
		}
	}
	body := http.MaxBytesReader(w, r.Body, CRMMaxRequestBodyBytes)
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return crmFundsDecodeError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &crmFundsRequestError{
				status: http.StatusBadRequest, code: crmFundsCodeInvalidJSON,
				message: "request body must contain one JSON object",
			}
		}
		return crmFundsDecodeError(err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return &crmFundsRequestError{
			status: http.StatusBadRequest, code: crmFundsCodeInvalidJSON,
			message: "request body must be a JSON object",
		}
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(destination); err != nil {
		return &crmFundsRequestError{
			status: http.StatusBadRequest, code: crmFundsCodeInvalidJSON,
			message: "invalid JSON request body",
		}
	}
	return nil
}

func crmFundsDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &crmFundsRequestError{
			status: http.StatusRequestEntityTooLarge, code: crmFundsCodeBodyTooLarge,
			message: "request body is too large",
		}
	}
	return &crmFundsRequestError{
		status: http.StatusBadRequest, code: crmFundsCodeInvalidJSON,
		message: "invalid JSON request body",
	}
}

func crmFundsWriteMappedError(w http.ResponseWriter, err error) {
	var requestError *crmFundsRequestError
	if errors.As(err, &requestError) {
		crmFundsWriteError(w, requestError.status, requestError.code, requestError.message)
		return
	}
	var notFoundError *crmFundsNotFoundError
	if errors.As(err, &notFoundError) {
		crmFundsWriteError(w, http.StatusNotFound, crmFundsCodeNotFound, notFoundError.Error())
		return
	}
	switch {
	case errors.Is(err, ErrCRMValidation):
		crmFundsWriteError(w, http.StatusBadRequest, crmFundsCodeValidationError, err.Error())
	case errors.Is(err, ErrCRMConflict):
		crmFundsWriteError(w, http.StatusConflict, crmFundsCodeConflict, err.Error())
	case errors.Is(err, ErrCRMPersistence), errors.Is(err, ErrCRMStoreClosed):
		crmFundsWriteError(w, http.StatusInternalServerError, crmFundsCodePersistenceError, "CRM persistence failed")
	default:
		crmFundsWriteError(w, http.StatusInternalServerError, crmFundsCodePersistenceError, "CRM operation failed")
	}
}

func crmFundsWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	crmFundsWriteError(w, http.StatusMethodNotAllowed, crmFundsCodeMethodNotAllowed, "method not allowed")
}

func crmFundsWriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "error": message})
}
