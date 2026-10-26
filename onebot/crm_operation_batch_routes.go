package main

import (
	"context"
	"net/http"
	"strings"
)

type crmOperatorContextKey struct{}

const crmAuthenticatedOperator = "operator"

func registerCRMOperationBatchRoutes(
	mux *http.ServeMux,
	store *CRMStore,
	operatorToken string,
) {
	mux.HandleFunc("/api/ops/batches", crmOperationBatchesHandler(store))
	mux.HandleFunc("/api/ops/batches/", crmOperationBatchHandler(store, operatorToken))
}

func crmOperationBatchesHandler(store *CRMStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ops/batches" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "batch endpoint not found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if len(r.URL.Query()) != 0 {
				crmPipelineWriteMappedError(w, NewCRMValidationError("query", "is not supported"))
				return
			}
			batches, err := crmOperationBatchList(store)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"total":   len(batches),
				"batches": batches,
			})
		case http.MethodPost:
			var create OperationBatchCreate
			if err := crmPipelineDecodeRequest(w, r, &create); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			batch, err := crmOperationBatchCreate(store, create, "system")
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusCreated, batch)
		default:
			crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	}
}

func crmOperationBatchHandler(store *CRMStore, operatorToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/ops/batches/")
		parts := strings.Split(path, "/")
		if path == "" || len(parts) > 2 || strings.TrimSpace(parts[0]) == "" {
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "operation batch not found")
			return
		}
		id := parts[0]
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodGet:
				batch, err := crmOperationBatchGet(store, id)
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, batch)
			case http.MethodPatch:
				var update OperationBatchUpdate
				if err := crmPipelineDecodeRequest(w, r, &update); err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				batch, err := crmOperationBatchUpdate(store, id, update, "system")
				if err != nil {
					crmPipelineWriteMappedError(w, err)
					return
				}
				writeJSON(w, http.StatusOK, batch)
			default:
				crmPipelineWriteMethodNotAllowed(w, http.MethodGet, http.MethodPatch)
			}
			return
		}

		switch parts[1] {
		case "members":
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			var update OperationBatchMembersUpdate
			if err := crmPipelineDecodeRequest(w, r, &update); err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			batch, err := crmOperationBatchUpdateMembers(store, id, update, "system")
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, batch)
		case "preview":
			if r.Method != http.MethodGet {
				crmPipelineWriteMethodNotAllowed(w, http.MethodGet)
				return
			}
			if len(r.URL.Query()) != 0 {
				crmPipelineWriteMappedError(w, NewCRMValidationError("query", "is not supported"))
				return
			}
			preview, err := crmOperationBatchPreview(store, id)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, preview)
		case "execute":
			if r.Method != http.MethodPost {
				crmPipelineWriteMethodNotAllowed(w, http.MethodPost)
				return
			}
			batch, err := crmOperationBatchGet(store, id)
			if err != nil {
				crmPipelineWriteMappedError(w, err)
				return
			}
			handler := crmOperationBatchExecuteHandler(store, id, "system")
			if crmOperationBatchProtected(batch.Kind, batch.TargetStatus) {
				handler = crmOperatorAuth(operatorToken, crmOperationBatchAuthenticatedExecuteHandler(store, id))
			}
			handler(w, r)
		default:
			crmPipelineWriteError(w, http.StatusNotFound, crmPipelineCodeNotFound, "batch endpoint not found")
		}
	}
}

func crmOperationBatchExecuteHandler(
	store *CRMStore,
	id string,
	actor string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var execute OperationBatchExecute
		if err := crmPipelineDecodeRequest(w, r, &execute); err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		execute.Actor = actor
		batch, err := crmOperationBatchExecute(store, id, execute)
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, batch)
	}
}

func crmOperationBatchAuthenticatedExecuteHandler(
	store *CRMStore,
	id string,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, err := crmOperatorIDFromRequest(r)
		if err != nil {
			crmPipelineWriteMappedError(w, err)
			return
		}
		crmOperationBatchExecuteHandler(store, id, actor)(w, r)
	}
}

func crmOperatorAuth(token string, next http.HandlerFunc) http.HandlerFunc {
	return automationOperatorAuth(token, func(w http.ResponseWriter, r *http.Request) {
		contextWithOperator := context.WithValue(r.Context(), crmOperatorContextKey{}, crmAuthenticatedOperator)
		next(w, r.WithContext(contextWithOperator))
	})
}

func crmOperatorIDFromRequest(r *http.Request) (string, error) {
	if r == nil {
		return "", NewCRMValidationError("operator", "authenticated operator is required")
	}
	operatorID, _ := r.Context().Value(crmOperatorContextKey{}).(string)
	operatorID = strings.TrimSpace(operatorID)
	if operatorID == "" {
		return "", NewCRMValidationError("operator", "authenticated operator is required")
	}
	return operatorID, nil
}
