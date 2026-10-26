package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	crmMessageCollectionPath = "/api/crm/message_events"
	crmMessageItemPrefix     = "/api/crm/message_events/"
)

var crmMessageErrNotFound = errors.New("crm message event not found")

type crmMessageListQuery struct {
	reviewStatus *ReviewStatus
	chatType     *ChatType
	candidateID  string
	linked       *bool
	search       string
	limit        int
	offset       int
}

type crmMessageIngestionProjection struct {
	Decision   JobIngestionDecision `json:"decision"`
	Provenance JobProvenance        `json:"provenance"`
}

type crmMessageEventProjection struct {
	MessageEvent
	JobIngestion *crmMessageIngestionProjection `json:"job_ingestion,omitempty"`
}

func registerCRMMessageRoutes(mux *http.ServeMux, store *CRMStore) {
	crmMessageSetStore(store)
	mux.HandleFunc(crmMessageCollectionPath, crmMessageCollectionHandler(store))
	mux.HandleFunc(crmMessageItemPrefix, crmMessageItemHandler(store))
}

func crmMessageCollectionHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			crmMessageWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if store == nil {
			crmMessageWriteError(w, http.StatusInternalServerError, "persistence_error", "message store is unavailable")
			return
		}

		query, err := crmMessageParseListQuery(r.URL.Query())
		if err != nil {
			crmMessageWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		state := store.Snapshot()
		events := make([]MessageEvent, 0, len(state.MessageEvents))
		search := strings.ToLower(query.search)
		for _, event := range state.MessageEvents {
			if query.reviewStatus != nil && event.ReviewStatus != *query.reviewStatus {
				continue
			}
			if query.chatType != nil && event.ChatType != *query.chatType {
				continue
			}
			if query.candidateID != "" && event.CandidateID != query.candidateID {
				continue
			}
			if query.linked != nil && (event.CandidateID != "") != *query.linked {
				continue
			}
			if search != "" && !strings.Contains(strings.ToLower(event.Text), search) {
				continue
			}
			events = append(events, event)
		}
		sort.Slice(events, func(i, j int) bool {
			if events[i].Timestamp != events[j].Timestamp {
				return events[i].Timestamp > events[j].Timestamp
			}
			if !events[i].CreatedAt.Equal(events[j].CreatedAt) {
				return events[i].CreatedAt.After(events[j].CreatedAt)
			}
			return events[i].ID > events[j].ID
		})

		total := len(events)
		start := query.offset
		if start > total {
			start = total
		}
		end := start + query.limit
		if end > total {
			end = total
		}
		page := crmMessageProjectEvents(state, events[start:end])
		provenance := append([]JobProvenance{}, state.JobProvenance...)
		crmJobIngestionSortProvenance(provenance)
		writeJSON(w, http.StatusOK, map[string]any{
			"total":            total,
			"events":           page,
			"ingestion_totals": crmJobIngestionTotals(provenance),
			"recent_decisions": crmJobIngestionProvenancePage(provenance, 10, 0),
		})
	}
}

func crmMessageItemHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, crmMessageItemPrefix)
		if id == "" || strings.Contains(id, "/") {
			crmMessageWriteError(w, http.StatusNotFound, "not_found", "message event not found")
			return
		}
		normalizedID, err := normalizeCRMString("id", id, 80, true, false)
		if err != nil {
			crmMessageWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		if store == nil {
			crmMessageWriteError(w, http.StatusInternalServerError, "persistence_error", "message store is unavailable")
			return
		}

		switch r.Method {
		case http.MethodGet:
			state := store.Snapshot()
			event, found := crmMessageFindEvent(state, normalizedID)
			if !found {
				crmMessageWriteError(w, http.StatusNotFound, "not_found", "message event not found")
				return
			}
			writeJSON(w, http.StatusOK, crmMessageProjectEvent(state, event))
		case http.MethodPatch:
			crmMessagePatchEvent(w, r, store, normalizedID)
		default:
			crmMessageWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
		}
	}
}

func crmMessagePatchEvent(w http.ResponseWriter, r *http.Request, store *CRMStore, id string) {
	var request ReviewUpdate
	code, err := crmMessageDecodeObject(r, &request)
	if err != nil {
		status := http.StatusBadRequest
		if code == "body_too_large" {
			status = http.StatusRequestEntityTooLarge
		}
		crmMessageWriteError(w, status, code, err.Error())
		return
	}
	if err := validateCRMVersion("expected_version", request.ExpectedVersion); err != nil {
		crmMessageWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if !IsValidReviewStatus(request.ReviewStatus) {
		crmMessageWriteError(w, http.StatusBadRequest, "validation_error", "review_status is invalid")
		return
	}

	now := time.Now().UTC()
	var updated MessageEvent
	err = store.Update(func(state *CRMState, _ *IDAllocator) error {
		for i := range state.MessageEvents {
			event := &state.MessageEvents[i]
			if event.ID != id {
				continue
			}
			if event.Version != request.ExpectedVersion {
				return NewCRMConflictError("message event version conflict")
			}
			if event.CandidateID != "" && request.ReviewStatus != ReviewStatusReviewed {
				return NewCRMValidationError("review_status", "linked messages must remain reviewed")
			}
			event.ReviewStatus = request.ReviewStatus
			event.Version++
			event.UpdatedAt = now
			updated = *event
			updated.Kinds = append([]string{}, event.Kinds...)
			return nil
		}
		return crmMessageErrNotFound
	})
	if err != nil {
		crmMessageWriteStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, crmMessageProjectEvent(store.Snapshot(), updated))
}

func crmMessageParseListQuery(values url.Values) (crmMessageListQuery, error) {
	query := crmMessageListQuery{
		limit: CRMDefaultListLimit,
	}
	for key := range values {
		switch key {
		case "review_status", "chat_type", "candidate_id", "linked", "q", "limit", "offset":
		default:
			return crmMessageListQuery{}, NewCRMValidationError(key, "is not supported")
		}
	}

	reviewStatus, err := crmMessageQueryValue(values, "review_status")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	if reviewStatus != "" {
		status := ReviewStatus(reviewStatus)
		if !IsValidReviewStatus(status) {
			return crmMessageListQuery{}, NewCRMValidationError("review_status", "is invalid")
		}
		query.reviewStatus = &status
	}

	chatType, err := crmMessageQueryValue(values, "chat_type")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	if chatType != "" {
		value := ChatType(chatType)
		if !IsValidChatType(value) {
			return crmMessageListQuery{}, NewCRMValidationError("chat_type", "is invalid")
		}
		query.chatType = &value
	}

	candidateID, err := crmMessageQueryValue(values, "candidate_id")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	if candidateID != "" {
		query.candidateID, err = normalizeCRMString("candidate_id", candidateID, 80, true, false)
		if err != nil {
			return crmMessageListQuery{}, err
		}
	}

	linked, err := crmMessageQueryValue(values, "linked")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	if linked != "" {
		var value bool
		switch linked {
		case "true":
			value = true
		case "false":
			value = false
		default:
			return crmMessageListQuery{}, NewCRMValidationError("linked", "must be true or false")
		}
		query.linked = &value
	}

	search, err := crmMessageQueryValue(values, "q")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	query.search, err = NormalizeCRMSearch(search)
	if err != nil {
		return crmMessageListQuery{}, err
	}

	limit, err := crmMessageQueryValue(values, "limit")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	if limit != "" {
		query.limit, err = crmMessageParseInteger("limit", limit, 1, CRMMaxListLimit)
		if err != nil {
			return crmMessageListQuery{}, err
		}
	}

	offset, err := crmMessageQueryValue(values, "offset")
	if err != nil {
		return crmMessageListQuery{}, err
	}
	if offset != "" {
		query.offset, err = crmMessageParseInteger("offset", offset, 0, int(^uint(0)>>1))
		if err != nil {
			return crmMessageListQuery{}, err
		}
	}
	return query, nil
}

func crmMessageQueryValue(values url.Values, name string) (string, error) {
	items, exists := values[name]
	if !exists {
		return "", nil
	}
	if len(items) != 1 {
		return "", NewCRMValidationError(name, "must be specified once")
	}
	return items[0], nil
}

func crmMessageParseInteger(field, raw string, minimum, maximum int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, NewCRMValidationError(field, "is invalid")
	}
	return value, nil
}

func crmMessageDecodeObject(r *http.Request, destination any) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r.Body, CRMMaxRequestBodyBytes+1))
	if err != nil {
		return "invalid_json", errors.New("could not read request body")
	}
	if len(data) > CRMMaxRequestBodyBytes {
		return "body_too_large", errors.New("request body is too large")
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return "invalid_json", errors.New("request body must contain one JSON object")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return "invalid_json", errors.New("request body contains invalid JSON")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "invalid_json", errors.New("request body contains trailing JSON")
	}
	return "", nil
}

func crmMessageFindEvent(state CRMState, id string) (MessageEvent, bool) {
	for i := range state.MessageEvents {
		if state.MessageEvents[i].ID == id {
			return state.MessageEvents[i], true
		}
	}
	return MessageEvent{}, false
}

func crmMessageProjectEvents(state CRMState, events []MessageEvent) []crmMessageEventProjection {
	projected := make([]crmMessageEventProjection, len(events))
	for i := range events {
		projected[i] = crmMessageProjectEvent(state, events[i])
	}
	return projected
}

func crmMessageProjectEvent(state CRMState, event MessageEvent) crmMessageEventProjection {
	projected := crmMessageEventProjection{MessageEvent: event}
	for i := len(state.JobProvenance) - 1; i >= 0; i-- {
		provenance := state.JobProvenance[i]
		if provenance.EventID != event.ID {
			continue
		}
		projected.JobIngestion = &crmMessageIngestionProjection{
			Decision:   provenance.Decision,
			Provenance: provenance,
		}
		break
	}
	return projected
}

func crmMessageWriteStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, crmMessageErrNotFound):
		crmMessageWriteError(w, http.StatusNotFound, "not_found", "message event not found")
	case errors.Is(err, ErrCRMConflict):
		crmMessageWriteError(w, http.StatusConflict, "conflict", err.Error())
	case errors.Is(err, ErrCRMValidation):
		crmMessageWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
	default:
		crmMessageWriteError(w, http.StatusInternalServerError, "persistence_error", "message event could not be saved")
	}
}

func crmMessageWriteMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	crmMessageWriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "request method is not supported")
}

func crmMessageWriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{
		"code":  code,
		"error": message,
	})
}
