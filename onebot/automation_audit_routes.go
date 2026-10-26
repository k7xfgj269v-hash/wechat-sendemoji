package main

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

type automationAuditFilter struct {
	EventType     string
	RunID         string
	ActionID      string
	ActorType     string
	ActorID       string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

func automationAuditHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(
			r,
			"event_type",
			"run_id",
			"action_id",
			"actor_type",
			"actor_id",
			"created_after",
			"created_before",
			"limit",
			"offset",
		); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		limit, offset, err := automationPagination(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		filter, err := automationParseAuditFilter(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		records, err := automationFilteredAuditRecords(service, filter)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		total := len(records)
		start, end := automationPage(total, limit, offset)
		automationWriteJSON(w, http.StatusOK, map[string]any{
			"total":        total,
			"limit":        limit,
			"offset":       offset,
			"audit_events": records[start:end],
		})
	}
}

func automationAuditExportHandler(service *AutomationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			automationWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if err := automationRejectUnknownQuery(
			r,
			"event_type",
			"run_id",
			"action_id",
			"actor_type",
			"actor_id",
			"created_after",
			"created_before",
		); err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		filter, err := automationParseAuditFilter(r)
		if err != nil {
			automationWriteError(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		records, err := automationFilteredAuditRecords(service, filter)
		if err != nil {
			automationWriteServiceError(w, err)
			return
		}
		sort.Slice(records, func(i, j int) bool {
			if records[i].CreatedAt.Equal(records[j].CreatedAt) {
				return records[i].ID < records[j].ID
			}
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		})

		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="automation-audit.jsonl"`)
		w.Header().Set("Cache-Control", "no-store")
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				return
			}
		}
	}
}

func automationParseAuditFilter(r *http.Request) (automationAuditFilter, error) {
	createdAfter, err := automationAuditQueryTime(r, "created_after")
	if err != nil {
		return automationAuditFilter{}, err
	}
	createdBefore, err := automationAuditQueryTime(r, "created_before")
	if err != nil {
		return automationAuditFilter{}, err
	}
	if createdAfter != nil && createdBefore != nil && createdBefore.Before(*createdAfter) {
		return automationAuditFilter{}, NewAutomationValidationError(
			"created_before",
			"must not precede created_after",
		)
	}
	return automationAuditFilter{
		EventType:     strings.TrimSpace(r.URL.Query().Get("event_type")),
		RunID:         strings.TrimSpace(r.URL.Query().Get("run_id")),
		ActionID:      strings.TrimSpace(r.URL.Query().Get("action_id")),
		ActorType:     strings.TrimSpace(r.URL.Query().Get("actor_type")),
		ActorID:       strings.TrimSpace(r.URL.Query().Get("actor_id")),
		CreatedAfter:  createdAfter,
		CreatedBefore: createdBefore,
	}, nil
}

func automationAuditQueryTime(r *http.Request, name string) (*time.Time, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, NewAutomationValidationError(name, "must be RFC3339")
	}
	value = value.UTC()
	return &value, nil
}

func automationFilteredAuditRecords(
	service *AutomationService,
	filter automationAuditFilter,
) ([]AutomationAuditRecord, error) {
	if service == nil || service.store == nil {
		return nil, NewAutomationValidationError("automation_service", "is unavailable")
	}
	var (
		records []AutomationAuditRecord
		err     error
	)
	if service.audit != nil {
		records, err = service.audit.ReadAll()
		if err != nil {
			return nil, err
		}
	} else {
		err = service.store.Read(func(state *AutomationState) error {
			records = append([]AutomationAuditRecord(nil), state.PendingAudit...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	filtered := make([]AutomationAuditRecord, 0, len(records))
	for _, record := range records {
		if filter.EventType != "" && record.EventType != filter.EventType {
			continue
		}
		if filter.RunID != "" && record.RunID != filter.RunID {
			continue
		}
		if filter.ActionID != "" && record.ActionID != filter.ActionID {
			continue
		}
		if filter.ActorType != "" && record.ActorType != filter.ActorType {
			continue
		}
		if filter.ActorID != "" && record.ActorID != filter.ActorID {
			continue
		}
		if filter.CreatedAfter != nil && record.CreatedAt.Before(*filter.CreatedAfter) {
			continue
		}
		if filter.CreatedBefore != nil && record.CreatedAt.After(*filter.CreatedBefore) {
			continue
		}
		filtered = append(filtered, cloneAutomationAuditRecord(record))
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].CreatedAt.Equal(filtered[j].CreatedAt) {
			return filtered[i].ID > filtered[j].ID
		}
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})
	return filtered, nil
}
