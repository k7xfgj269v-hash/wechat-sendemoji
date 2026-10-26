package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type createForwardRuleRequest struct {
	GroupID         string   `json:"group_id"`
	Keywords        []string `json:"keywords"`
	CooldownSeconds int      `json:"cooldown_seconds"`
	Enabled         *bool    `json:"enabled"`
}

type updateForwardRuleRequest struct {
	Keywords        *[]string `json:"keywords"`
	CooldownSeconds *int      `json:"cooldown_seconds"`
	Enabled         *bool     `json:"enabled"`
}

func registerForwardRuleRoutes(mux *http.ServeMux, store *ForwardRuleStore) {
	mux.HandleFunc("/api/forward_rules", forwardRulesHandler(store))
}

func forwardRulesHandler(store *ForwardRuleStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, store.List())
		case http.MethodPost:
			handleCreateForwardRule(w, r, store)
		case http.MethodPut:
			handleUpdateForwardRule(w, r, store)
		case http.MethodDelete:
			handleDeleteForwardRule(w, r, store)
		default:
			writeMethodNotAllowed(w, http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete)
		}
	}
}

func handleCreateForwardRule(w http.ResponseWriter, r *http.Request, store *ForwardRuleStore) {
	var req createForwardRuleRequest
	if err := decodeJSONObject(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rule, err := store.Create(req.GroupID, req.Keywords, req.CooldownSeconds, enabled)
	if err != nil {
		forwardRuleWriteMutationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func handleUpdateForwardRule(w http.ResponseWriter, r *http.Request, store *ForwardRuleStore) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "缺少 id")
		return
	}
	var req updateForwardRuleRequest
	if err := decodeJSONObject(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule, found, err := store.Update(id, req)
	if err != nil {
		forwardRuleWriteMutationError(w, err)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "规则不存在")
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func handleDeleteForwardRule(w http.ResponseWriter, r *http.Request, store *ForwardRuleStore) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeAPIError(w, http.StatusBadRequest, "缺少 id")
		return
	}
	deleted, err := store.forwardRuleDelete(id)
	if err != nil {
		forwardRuleWriteMutationError(w, err)
		return
	}
	if !deleted {
		writeAPIError(w, http.StatusNotFound, "规则不存在")
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func forwardRuleWriteMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrCRMPersistence) || errors.Is(err, ErrCRMStoreClosed) {
		writeAPIError(w, http.StatusInternalServerError, "保存转发规则失败")
		return
	}
	writeAPIError(w, http.StatusBadRequest, err.Error())
}

func decodeJSONObject(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("无效的 JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return fmt.Errorf("无效的 JSON: 只能提交一个对象")
	} else if err != io.EOF {
		return fmt.Errorf("无效的 JSON: %w", err)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return fmt.Errorf("无效的 JSON: 请求体必须是对象")
	}
	objectDecoder := json.NewDecoder(bytes.NewReader(raw))
	objectDecoder.DisallowUnknownFields()
	if err := objectDecoder.Decode(dst); err != nil {
		return fmt.Errorf("无效的 JSON: %w", err)
	}
	return nil
}
