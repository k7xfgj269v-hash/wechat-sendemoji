package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	crmCandidateCodeInvalidJSON      = "invalid_json"
	crmCandidateCodeValidationError  = "validation_error"
	crmCandidateCodeNotFound         = "not_found"
	crmCandidateCodeConflict         = "conflict"
	crmCandidateCodePersistenceError = "persistence_error"
	crmCandidateCodeMethodNotAllowed = "method_not_allowed"
	crmCandidateCodeBodyTooLarge     = "body_too_large"
)

type crmCandidateRequestError struct {
	status  int
	code    string
	message string
}

func (e *crmCandidateRequestError) Error() string {
	return e.message
}

func registerCRMCandidateRoutes(mux *http.ServeMux, store *CRMStore) {
	mux.HandleFunc("/api/crm/candidates", crmCandidateCollectionHandler(store))
	mux.HandleFunc("/api/crm/candidates/", crmCandidateDetailHandler(store))
	mux.HandleFunc("/api/crm/message_candidate_links", crmCandidateMessageLinkHandler(store, ManualCandidateExtractor{}))
}

func crmCandidateCollectionHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/candidates" {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "candidate endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			filter, err := crmCandidateParseListFilter(r)
			if err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			total, candidates, err := crmCandidateList(store, filter)
			if err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"total":      total,
				"candidates": candidates,
			})
		case http.MethodPost:
			var draft CandidateDraft
			if err := crmCandidateDecodeRequest(w, r, &draft); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			candidate, err := crmCandidateCreate(store, draft)
			if err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, candidate)
		default:
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmCandidateDetailHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/crm/candidates/")
		if id == "" || strings.Contains(id, "/") {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "candidate not found")
			return
		}

		switch r.Method {
		case http.MethodGet:
			detail, err := crmCandidateGetDetail(store, id)
			if err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, detail)
		case http.MethodPatch:
			var update CandidateUpdate
			if err := crmCandidateDecodeRequest(w, r, &update); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			candidate, err := crmCandidateUpdate(store, id, update)
			if err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, candidate)
		default:
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmCandidateMessageLinkHandler(store *CRMStore, extractor CandidateExtractor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/crm/message_candidate_links" {
			crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, "candidate link endpoint not found")
			return
		}
		if r.Method != http.MethodPost {
			crmCandidateWriteMethodNotAllowed(w, http.MethodPost)
			return
		}

		var request MessageCandidateLink
		if err := crmCandidateDecodeRequest(w, r, &request); err != nil {
			crmCandidateWriteMappedError(w, err)
			return
		}
		result, err := crmCandidateLinkMessage(r.Context(), store, extractor, request)
		if err != nil {
			crmCandidateWriteMappedError(w, err)
			return
		}
		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		writeJSON(w, status, map[string]any{
			"candidate": result.Candidate,
			"event":     result.Event,
		})
	}
}

func crmCandidateParseListFilter(r *http.Request) (crmCandidateListFilter, error) {
	query, err := crmCandidateSingleQueryValue(r, "q")
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	statusText, err := crmCandidateSingleQueryValue(r, "status")
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	owner, err := crmCandidateSingleQueryValue(r, "owner")
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	source, err := crmCandidateSingleQueryValue(r, "source")
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	limitText, err := crmCandidateSingleQueryValue(r, "limit")
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	offsetText, err := crmCandidateSingleQueryValue(r, "offset")
	if err != nil {
		return crmCandidateListFilter{}, err
	}

	limit, err := crmCandidateParseListInteger("limit", limitText, CRMDefaultListLimit, 1, CRMMaxListLimit)
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	offset, err := crmCandidateParseListInteger("offset", offsetText, 0, 0, int(^uint(0)>>1))
	if err != nil {
		return crmCandidateListFilter{}, err
	}
	return crmCandidateNormalizeListFilter(crmCandidateListFilter{
		Query:  query,
		Status: CandidateStatus(strings.TrimSpace(statusText)),
		Owner:  owner,
		Source: source,
		Limit:  limit,
		Offset: offset,
	})
}

func crmCandidateSingleQueryValue(r *http.Request, name string) (string, error) {
	values, ok := r.URL.Query()[name]
	if !ok {
		return "", nil
	}
	if len(values) != 1 {
		return "", NewCRMValidationError(name, "must be provided once")
	}
	return values[0], nil
}

func crmCandidateParseListInteger(name, raw string, defaultValue, minValue, maxValue int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		return 0, NewCRMValidationError(name, "is invalid")
	}
	return value, nil
}

func crmCandidateDecodeRequest(w http.ResponseWriter, r *http.Request, destination any) error {
	if r.ContentLength > CRMMaxRequestBodyBytes {
		return &crmCandidateRequestError{
			status:  http.StatusRequestEntityTooLarge,
			code:    crmCandidateCodeBodyTooLarge,
			message: "request body is too large",
		}
	}

	body := http.MaxBytesReader(w, r.Body, CRMMaxRequestBodyBytes)
	decoder := json.NewDecoder(body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return crmCandidateDecodeError(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &crmCandidateRequestError{
				status:  http.StatusBadRequest,
				code:    crmCandidateCodeInvalidJSON,
				message: "request body must contain one JSON object",
			}
		}
		return crmCandidateDecodeError(err)
	}

	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return &crmCandidateRequestError{
			status:  http.StatusBadRequest,
			code:    crmCandidateCodeInvalidJSON,
			message: "request body must be a JSON object",
		}
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(destination); err != nil {
		return &crmCandidateRequestError{
			status:  http.StatusBadRequest,
			code:    crmCandidateCodeInvalidJSON,
			message: "invalid JSON request body",
		}
	}
	return nil
}

func crmCandidateDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &crmCandidateRequestError{
			status:  http.StatusRequestEntityTooLarge,
			code:    crmCandidateCodeBodyTooLarge,
			message: "request body is too large",
		}
	}
	return &crmCandidateRequestError{
		status:  http.StatusBadRequest,
		code:    crmCandidateCodeInvalidJSON,
		message: "invalid JSON request body",
	}
}

func crmCandidateWriteMappedError(w http.ResponseWriter, err error) {
	var requestError *crmCandidateRequestError
	if errors.As(err, &requestError) {
		crmCandidateWriteError(w, requestError.status, requestError.code, requestError.message)
		return
	}
	var notFoundError *crmCandidateNotFoundError
	if errors.As(err, &notFoundError) {
		crmCandidateWriteError(w, http.StatusNotFound, crmCandidateCodeNotFound, notFoundError.Error())
		return
	}
	switch {
	case errors.Is(err, ErrCRMValidation):
		crmCandidateWriteError(w, http.StatusBadRequest, crmCandidateCodeValidationError, err.Error())
	case errors.Is(err, ErrCRMConflict):
		crmCandidateWriteError(w, http.StatusConflict, crmCandidateCodeConflict, err.Error())
	case errors.Is(err, ErrCRMPersistence), errors.Is(err, ErrCRMStoreClosed):
		crmCandidateWriteError(w, http.StatusInternalServerError, crmCandidateCodePersistenceError, "CRM persistence failed")
	default:
		crmCandidateWriteError(w, http.StatusInternalServerError, crmCandidateCodePersistenceError, "CRM operation failed")
	}
}

func crmCandidateWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	crmCandidateWriteError(w, http.StatusMethodNotAllowed, crmCandidateCodeMethodNotAllowed, "method not allowed")
}

func crmCandidateWriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"code":  code,
		"error": message,
	})
}
