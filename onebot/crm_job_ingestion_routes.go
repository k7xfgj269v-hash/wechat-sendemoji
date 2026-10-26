package main

import (
	"net/http"
	"sort"
	"strings"
)

const (
	crmJobIngestionPath  = "/api/crm/recruiting/job_ingestions"
	crmJobProvenancePath = "/api/crm/job_provenance"
)

type crmJobIngestionDecisionTotals struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Duplicate int `json:"duplicate"`
	Noop      int `json:"noop"`
	Noise     int `json:"noise"`
	Failure   int `json:"failure"`
}

type crmJobIngestionSummaryResponse struct {
	Total           int                           `json:"total"`
	Totals          crmJobIngestionDecisionTotals `json:"totals"`
	Limit           int                           `json:"limit"`
	Offset          int                           `json:"offset"`
	RecentDecisions []JobProvenance               `json:"recent_decisions"`
}

type crmJobProvenanceResponse struct {
	Total      int             `json:"total"`
	Limit      int             `json:"limit"`
	Offset     int             `json:"offset"`
	Provenance []JobProvenance `json:"provenance"`
}

type crmJobProvenanceQuery struct {
	jobID    string
	eventID  string
	groupID  string
	decision JobIngestionDecision
	limit    int
	offset   int
}

func registerCRMJobIngestionRoutes(
	mux *http.ServeMux,
	store *CRMStore,
	operatorToken string,
) {
	mux.HandleFunc(
		crmJobIngestionPath,
		automationOperatorAuth(operatorToken, crmJobIngestionHandler(store)),
	)
	mux.HandleFunc(
		crmJobProvenancePath,
		automationOperatorAuth(operatorToken, crmJobProvenanceHandler(store)),
	)
}

func crmJobIngestionHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != crmJobIngestionPath {
			crmCandidateWriteError(
				w,
				http.StatusNotFound,
				crmCandidateCodeNotFound,
				"job ingestion endpoint not found",
			)
			return
		}
		if store == nil {
			crmCandidateWriteError(
				w,
				http.StatusInternalServerError,
				crmCandidateCodePersistenceError,
				"CRM persistence failed",
			)
			return
		}

		switch r.Method {
		case http.MethodGet:
			crmJobIngestionSummary(w, r, store)
		case http.MethodPost:
			if err := crmJobIngestionRejectUnknownQuery(r); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			var input SourceJobIngestion
			if err := crmCandidateDecodeRequest(w, r, &input); err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			result, err := store.IngestSourceJob(input)
			if err != nil {
				crmCandidateWriteMappedError(w, err)
				return
			}
			status := http.StatusOK
			if result.Decision == JobIngestionCreate && !result.Idempotent {
				status = http.StatusCreated
			}
			writeJSON(w, status, result)
		default:
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmJobIngestionSummary(w http.ResponseWriter, r *http.Request, store *CRMStore) {
	if err := crmJobIngestionRejectUnknownQuery(r, "limit", "offset"); err != nil {
		crmCandidateWriteMappedError(w, err)
		return
	}
	limit, offset, err := crmPipelinePagination(r)
	if err != nil {
		crmCandidateWriteMappedError(w, err)
		return
	}

	provenance := store.Snapshot().JobProvenance
	totals := crmJobIngestionTotals(provenance)
	crmJobIngestionSortProvenance(provenance)
	page := crmJobIngestionProvenancePage(provenance, limit, offset)
	writeJSON(w, http.StatusOK, crmJobIngestionSummaryResponse{
		Total:           len(provenance),
		Totals:          totals,
		Limit:           limit,
		Offset:          offset,
		RecentDecisions: page,
	})
}

func crmJobProvenanceHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != crmJobProvenancePath {
			crmCandidateWriteError(
				w,
				http.StatusNotFound,
				crmCandidateCodeNotFound,
				"job provenance endpoint not found",
			)
			return
		}
		if r.Method != http.MethodGet {
			crmCandidateWriteMethodNotAllowed(w, http.MethodGet)
			return
		}
		if store == nil {
			crmCandidateWriteError(
				w,
				http.StatusInternalServerError,
				crmCandidateCodePersistenceError,
				"CRM persistence failed",
			)
			return
		}

		query, err := crmJobProvenanceParseQuery(r)
		if err != nil {
			crmCandidateWriteMappedError(w, err)
			return
		}
		state := store.Snapshot()
		provenance := make([]JobProvenance, 0, len(state.JobProvenance))
		for _, item := range state.JobProvenance {
			if query.jobID != "" && item.JobID != query.jobID {
				continue
			}
			if query.eventID != "" && item.EventID != query.eventID {
				continue
			}
			if query.groupID != "" && item.GroupID != query.groupID {
				continue
			}
			if query.decision != "" && item.Decision != query.decision {
				continue
			}
			provenance = append(provenance, item)
		}
		crmJobIngestionSortProvenance(provenance)
		writeJSON(w, http.StatusOK, crmJobProvenanceResponse{
			Total:      len(provenance),
			Limit:      query.limit,
			Offset:     query.offset,
			Provenance: crmJobIngestionProvenancePage(provenance, query.limit, query.offset),
		})
	}
}

func crmJobProvenanceParseQuery(r *http.Request) (crmJobProvenanceQuery, error) {
	if err := crmJobIngestionRejectUnknownQuery(
		r,
		"job_id",
		"event_id",
		"group_id",
		"decision",
		"limit",
		"offset",
	); err != nil {
		return crmJobProvenanceQuery{}, err
	}
	jobID, err := crmPipelineSingleQueryValue(r, "job_id")
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	eventID, err := crmPipelineSingleQueryValue(r, "event_id")
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	groupID, err := crmPipelineSingleQueryValue(r, "group_id")
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	decisionText, err := crmPipelineSingleQueryValue(r, "decision")
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	limit, offset, err := crmPipelinePagination(r)
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}

	jobID, err = normalizeCRMString("job_id", jobID, 80, false, false)
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	eventID, err = normalizeCRMString("event_id", eventID, 80, false, false)
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	groupID, err = normalizeCRMString("group_id", groupID, 256, false, false)
	if err != nil {
		return crmJobProvenanceQuery{}, err
	}
	if groupID != "" && !strings.HasSuffix(groupID, "@chatroom") {
		return crmJobProvenanceQuery{}, NewCRMValidationError(
			"group_id",
			"must be a WeChat group identifier",
		)
	}
	decision := JobIngestionDecision(strings.TrimSpace(decisionText))
	if decision != "" && !IsValidJobIngestionDecision(decision) {
		return crmJobProvenanceQuery{}, NewCRMValidationError("decision", "is invalid")
	}
	return crmJobProvenanceQuery{
		jobID:    jobID,
		eventID:  eventID,
		groupID:  groupID,
		decision: decision,
		limit:    limit,
		offset:   offset,
	}, nil
}

func crmJobIngestionRejectUnknownQuery(r *http.Request, allowed ...string) error {
	if err := automationRejectUnknownQuery(r, allowed...); err != nil {
		return NewCRMValidationError("query", err.Error())
	}
	return nil
}

func crmJobIngestionTotals(provenance []JobProvenance) crmJobIngestionDecisionTotals {
	var totals crmJobIngestionDecisionTotals
	for _, item := range provenance {
		switch item.Decision {
		case JobIngestionCreate:
			totals.Create++
		case JobIngestionUpdate:
			totals.Update++
		case JobIngestionDuplicate:
			totals.Duplicate++
		case JobIngestionNoop:
			totals.Noop++
		case JobIngestionNoise:
			totals.Noise++
		case JobIngestionFailure:
			totals.Failure++
		}
	}
	return totals
}

func crmJobIngestionSortProvenance(provenance []JobProvenance) {
	sort.Slice(provenance, func(i, j int) bool {
		if !provenance[i].CreatedAt.Equal(provenance[j].CreatedAt) {
			return provenance[i].CreatedAt.After(provenance[j].CreatedAt)
		}
		if !provenance[i].SourceTimestamp.Equal(provenance[j].SourceTimestamp) {
			return provenance[i].SourceTimestamp.After(provenance[j].SourceTimestamp)
		}
		return provenance[i].ID > provenance[j].ID
	})
}

func crmJobIngestionProvenancePage(
	provenance []JobProvenance,
	limit, offset int,
) []JobProvenance {
	start := offset
	if start > len(provenance) {
		start = len(provenance)
	}
	end := start + limit
	if end > len(provenance) {
		end = len(provenance)
	}
	return append([]JobProvenance{}, provenance[start:end]...)
}
