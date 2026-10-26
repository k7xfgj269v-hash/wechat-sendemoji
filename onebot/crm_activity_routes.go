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

type crmActivityAPIError struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

type crmActivityRequestError struct {
	code    string
	status  int
	message string
}

func (e *crmActivityRequestError) Error() string {
	return e.message
}

func registerCRMActivityRoutes(mux *http.ServeMux, store *CRMStore) {
	service := crmActivityNewService(store)
	mux.HandleFunc("/api/crm/notes", crmActivityNotesHandler(service))
	mux.HandleFunc("/api/crm/tasks", crmActivityTasksHandler(service))
	mux.HandleFunc("/api/crm/tasks/", crmActivityTaskItemHandler(service))
}

func crmActivityNotesHandler(service *crmActivityService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			filter, err := crmActivityParseNoteFilter(r.URL.Query())
			if err != nil {
				crmActivityWriteDomainError(w, err)
				return
			}
			response, err := service.ListNotes(filter)
			if err != nil {
				crmActivityWriteDomainError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
		case http.MethodPost:
			var request NoteCreate
			if err := crmActivityDecodeJSONObject(w, r, &request); err != nil {
				crmActivityWriteRequestError(w, err)
				return
			}
			note, err := service.CreateNote(request)
			if err != nil {
				crmActivityWriteDomainError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, note)
		default:
			crmActivityWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmActivityTasksHandler(service *crmActivityService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			filter, err := crmActivityParseTaskFilter(r.URL.Query())
			if err != nil {
				crmActivityWriteDomainError(w, err)
				return
			}
			response, err := service.ListTasks(filter)
			if err != nil {
				crmActivityWriteDomainError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, response)
		case http.MethodPost:
			var request TaskCreate
			if err := crmActivityDecodeJSONObject(w, r, &request); err != nil {
				crmActivityWriteRequestError(w, err)
				return
			}
			task, err := service.CreateTask(request)
			if err != nil {
				crmActivityWriteDomainError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, task)
		default:
			crmActivityWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmActivityTaskItemHandler(service *crmActivityService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		remainder := strings.TrimPrefix(r.URL.Path, "/api/crm/tasks/")
		parts := strings.Split(remainder, "/")
		if len(parts) == 1 && parts[0] != "" {
			crmActivityHandleTaskItem(w, r, service, parts[0])
			return
		}
		if len(parts) == 2 && parts[0] != "" && parts[1] == "status" {
			crmActivityHandleTaskStatus(w, r, service, parts[0])
			return
		}
		crmActivityWriteAPIError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func crmActivityHandleTaskItem(w http.ResponseWriter, r *http.Request, service *crmActivityService, id string) {
	switch r.Method {
	case http.MethodGet:
		task, err := service.GetTask(id)
		if err != nil {
			crmActivityWriteDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	case http.MethodPatch:
		var request TaskUpdate
		if err := crmActivityDecodeJSONObject(w, r, &request); err != nil {
			crmActivityWriteRequestError(w, err)
			return
		}
		task, err := service.UpdateTask(id, request)
		if err != nil {
			crmActivityWriteDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, task)
	default:
		crmActivityWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
	}
}

func crmActivityHandleTaskStatus(w http.ResponseWriter, r *http.Request, service *crmActivityService, id string) {
	if r.Method != http.MethodPost {
		crmActivityWriteMethodNotAllowed(w, http.MethodPost)
		return
	}
	var request TaskStatusRequest
	if err := crmActivityDecodeJSONObject(w, r, &request); err != nil {
		crmActivityWriteRequestError(w, err)
		return
	}
	task, err := service.SetTaskStatus(id, request)
	if err != nil {
		crmActivityWriteDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func crmActivityParseNoteFilter(query url.Values) (crmActivityNoteFilter, error) {
	if err := crmActivityRejectUnknownQuery(query, "target_type", "target_id", "limit", "offset"); err != nil {
		return crmActivityNoteFilter{}, err
	}
	targetType, err := crmActivityRequiredQuery(query, "target_type")
	if err != nil {
		return crmActivityNoteFilter{}, err
	}
	targetID, err := crmActivityRequiredQuery(query, "target_id")
	if err != nil {
		return crmActivityNoteFilter{}, err
	}
	limit, err := crmActivityIntegerQuery(query, "limit", CRMDefaultListLimit, 1, CRMMaxListLimit)
	if err != nil {
		return crmActivityNoteFilter{}, err
	}
	offset, err := crmActivityIntegerQuery(query, "offset", 0, 0, int(^uint(0)>>1))
	if err != nil {
		return crmActivityNoteFilter{}, err
	}
	return crmActivityNoteFilter{
		TargetType: NoteTargetType(targetType),
		TargetID:   targetID,
		Limit:      limit,
		Offset:     offset,
	}, nil
}

func crmActivityParseTaskFilter(query url.Values) (crmActivityTaskFilter, error) {
	if err := crmActivityRejectUnknownQuery(
		query,
		"owner",
		"status",
		"overdue",
		"due_from",
		"due_to",
		"candidate_id",
		"lead_id",
		"limit",
		"offset",
	); err != nil {
		return crmActivityTaskFilter{}, err
	}

	var filter crmActivityTaskFilter
	if owner, ok, err := crmActivityOptionalQuery(query, "owner", true); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		filter.Owner = &owner
	}
	if status, ok, err := crmActivityOptionalQuery(query, "status", false); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		filter.Status = FollowUpStatus(status)
	}
	if raw, ok, err := crmActivityOptionalQuery(query, "overdue", false); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		var value bool
		switch raw {
		case "true":
			value = true
		case "false":
			value = false
		default:
			return crmActivityTaskFilter{}, NewCRMValidationError("overdue", "must be true or false")
		}
		filter.Overdue = &value
	}
	if raw, ok, err := crmActivityOptionalQuery(query, "due_from", false); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		value, parseErr := crmActivityParseTimeQuery("due_from", raw)
		if parseErr != nil {
			return crmActivityTaskFilter{}, parseErr
		}
		filter.DueFrom = &value
	}
	if raw, ok, err := crmActivityOptionalQuery(query, "due_to", false); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		value, parseErr := crmActivityParseTimeQuery("due_to", raw)
		if parseErr != nil {
			return crmActivityTaskFilter{}, parseErr
		}
		filter.DueTo = &value
	}
	if value, ok, err := crmActivityOptionalQuery(query, "candidate_id", false); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		filter.CandidateID = value
	}
	if value, ok, err := crmActivityOptionalQuery(query, "lead_id", false); err != nil {
		return crmActivityTaskFilter{}, err
	} else if ok {
		filter.LeadID = value
	}
	limit, err := crmActivityIntegerQuery(query, "limit", CRMDefaultListLimit, 1, CRMMaxListLimit)
	if err != nil {
		return crmActivityTaskFilter{}, err
	}
	offset, err := crmActivityIntegerQuery(query, "offset", 0, 0, int(^uint(0)>>1))
	if err != nil {
		return crmActivityTaskFilter{}, err
	}
	filter.Limit = limit
	filter.Offset = offset
	return filter, nil
}

func crmActivityDecodeJSONObject(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, CRMMaxRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return crmActivityJSONDecodeError(err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return &crmActivityRequestError{
				code:    "invalid_json",
				status:  http.StatusBadRequest,
				message: "request body must contain one JSON object",
			}
		}
		return crmActivityJSONDecodeError(err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return &crmActivityRequestError{
			code:    "invalid_json",
			status:  http.StatusBadRequest,
			message: "request body must be a JSON object",
		}
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(destination); err != nil {
		return crmActivityJSONDecodeError(err)
	}
	return nil
}

func crmActivityJSONDecodeError(err error) error {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return &crmActivityRequestError{
			code:    "body_too_large",
			status:  http.StatusRequestEntityTooLarge,
			message: "request body exceeds 1 MiB",
		}
	}
	return &crmActivityRequestError{
		code:    "invalid_json",
		status:  http.StatusBadRequest,
		message: "invalid JSON: " + err.Error(),
	}
}

func crmActivityRejectUnknownQuery(query url.Values, allowed ...string) error {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	for name := range query {
		if _, ok := allowedSet[name]; !ok {
			return NewCRMValidationError(name, "unknown query parameter")
		}
	}
	return nil
}

func crmActivityRequiredQuery(query url.Values, name string) (string, error) {
	value, ok, err := crmActivityOptionalQuery(query, name, false)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", NewCRMValidationError(name, "is required")
	}
	return value, nil
}

func crmActivityOptionalQuery(query url.Values, name string, allowEmpty bool) (string, bool, error) {
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

func crmActivityIntegerQuery(query url.Values, name string, defaultValue, minimum, maximum int) (int, error) {
	raw, ok, err := crmActivityOptionalQuery(query, name, false)
	if err != nil {
		return 0, err
	}
	if !ok {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, NewCRMValidationError(name, fmt.Sprintf("must be between %d and %d", minimum, maximum))
	}
	return value, nil
}

func crmActivityParseTimeQuery(field, raw string) (time.Time, error) {
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil || value.IsZero() {
		return time.Time{}, NewCRMValidationError(field, "must be an RFC3339 timestamp")
	}
	return value.UTC(), nil
}

func crmActivityWriteRequestError(w http.ResponseWriter, err error) {
	var requestError *crmActivityRequestError
	if errors.As(err, &requestError) {
		crmActivityWriteAPIError(w, requestError.status, requestError.code, requestError.message)
		return
	}
	crmActivityWriteAPIError(w, http.StatusBadRequest, "invalid_json", "invalid JSON")
}

func crmActivityWriteDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrCRMValidation):
		crmActivityWriteAPIError(w, http.StatusBadRequest, "validation_error", err.Error())
	case crmActivityIsNotFound(err):
		crmActivityWriteAPIError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrCRMConflict):
		crmActivityWriteAPIError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrCRMPersistence), errors.Is(err, ErrCRMStoreClosed):
		crmActivityWriteAPIError(w, http.StatusInternalServerError, "persistence_error", "CRM persistence failed")
	default:
		crmActivityWriteAPIError(w, http.StatusInternalServerError, "persistence_error", "CRM persistence failed")
	}
}

func crmActivityWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	crmActivityWriteAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func crmActivityWriteAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, crmActivityAPIError{Code: code, Error: message})
}
