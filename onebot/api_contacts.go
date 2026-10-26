package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const contactsRefreshAPITimeout = contactCacheRefreshTimeout

var (
	errContactsUnavailable = errors.New("contacts unavailable")
	errContactNotFound     = errors.New("contact not found")
	errGroupNotFound       = errors.New("group not found")
)

type contactsAPIBackend interface {
	Search(context.Context, AgentContactSearchRequest) (AgentContactSearchResult, error)
	Groups(context.Context) ([]Group, error)
	GroupMembers(context.Context, string) ([]GroupMember, error)
	Contact(context.Context, string) (Contact, error)
	Refresh(context.Context) (AgentContactCounts, error)
}

type contactCacheAPIBackend struct {
	cache *ContactCache
}

func (b contactCacheAPIBackend) Search(
	_ context.Context,
	request AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	if b.cache == nil {
		return AgentContactSearchResult{}, errContactsUnavailable
	}
	total, contacts, loaded := b.cache.ListContacts(
		request.Keyword,
		request.LocalType,
		request.Limit,
		request.Offset,
	)
	if !loaded {
		return AgentContactSearchResult{}, errContactsUnavailable
	}
	return AgentContactSearchResult{
		Total:    total,
		Contacts: contacts,
		Limit:    request.Limit,
		Offset:   request.Offset,
	}, nil
}

func (b contactCacheAPIBackend) Groups(context.Context) ([]Group, error) {
	if b.cache == nil {
		return nil, errContactsUnavailable
	}
	groups, loaded := b.cache.ListGroups()
	if !loaded {
		return nil, errContactsUnavailable
	}
	return groups, nil
}

func (b contactCacheAPIBackend) GroupMembers(
	_ context.Context,
	groupID string,
) ([]GroupMember, error) {
	if b.cache == nil {
		return nil, errContactsUnavailable
	}
	members, loaded, found := b.cache.GroupMembers(groupID)
	if !loaded {
		return nil, errContactsUnavailable
	}
	if !found {
		return nil, errGroupNotFound
	}
	return members, nil
}

func (b contactCacheAPIBackend) Contact(_ context.Context, username string) (Contact, error) {
	if b.cache == nil {
		return Contact{}, errContactsUnavailable
	}
	contact, loaded, found := b.cache.ContactDetail(username)
	if !loaded {
		return Contact{}, errContactsUnavailable
	}
	if !found {
		return Contact{}, errContactNotFound
	}
	return contact, nil
}

func (b contactCacheAPIBackend) Refresh(ctx context.Context) (AgentContactCounts, error) {
	if b.cache == nil {
		return AgentContactCounts{}, errContactsUnavailable
	}
	if err := b.cache.RefreshContext(ctx); err != nil {
		return AgentContactCounts{}, err
	}
	contacts, groups, _ := b.cache.Counts()
	return AgentContactCounts{Contacts: contacts, Groups: groups}, nil
}

type agentContactsAPIBackend struct {
	client AgentClient
}

func (b agentContactsAPIBackend) Search(
	ctx context.Context,
	request AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	if b.client == nil {
		return AgentContactSearchResult{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
	return b.client.SearchContacts(ctx, request)
}

func (b agentContactsAPIBackend) Groups(ctx context.Context) ([]Group, error) {
	if b.client == nil {
		return nil, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
	return b.client.Groups(ctx)
}

func (b agentContactsAPIBackend) GroupMembers(
	ctx context.Context,
	groupID string,
) ([]GroupMember, error) {
	if b.client == nil {
		return nil, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
	return b.client.GroupMembers(ctx, groupID)
}

func (b agentContactsAPIBackend) Contact(ctx context.Context, username string) (Contact, error) {
	if b.client == nil {
		return Contact{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
	return b.client.Contact(ctx, username)
}

func (b agentContactsAPIBackend) Refresh(ctx context.Context) (AgentContactCounts, error) {
	if b.client == nil {
		return AgentContactCounts{}, &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
	return b.client.RefreshContacts(ctx)
}

func contactsBackend(source any) contactsAPIBackend {
	switch value := source.(type) {
	case AgentClient:
		return agentContactsAPIBackend{client: value}
	case *ContactCache:
		return contactCacheAPIBackend{cache: value}
	default:
		return agentContactsAPIBackend{}
	}
}

func contactsHandler(source any) http.HandlerFunc {
	backend := contactsBackend(source)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}

		limit, err := parseBoundedIntQuery(r, "limit", 200, 1, 1000)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
		offset, err := parseBoundedIntQuery(r, "offset", 0, 0, int(^uint(0)>>1))
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}

		var localType *int
		if values, ok := r.URL.Query()["local_type"]; ok {
			if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
				writeAPIError(w, http.StatusBadRequest, "local_type 非法")
				return
			}
			value, err := strconv.Atoi(values[0])
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, "local_type 非法")
				return
			}
			localType = &value
		}

		result, err := backend.Search(r.Context(), AgentContactSearchRequest{
			Keyword:   r.URL.Query().Get("keyword"),
			LocalType: localType,
			Limit:     limit,
			Offset:    offset,
		})
		if err != nil {
			writeContactsAPIError(w, err, "联系人缓存尚未加载")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"total":    result.Total,
			"contacts": result.Contacts,
		})
	}
}

func groupsHandler(source any) http.HandlerFunc {
	backend := contactsBackend(source)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		groups, err := backend.Groups(r.Context())
		if err != nil {
			writeContactsAPIError(w, err, "联系人缓存尚未加载")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
	}
}

func groupMembersHandler(source any) http.HandlerFunc {
	backend := contactsBackend(source)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		groupID := strings.TrimSpace(r.URL.Query().Get("group_id"))
		if groupID == "" {
			writeAPIError(w, http.StatusBadRequest, "缺少 group_id")
			return
		}
		members, err := backend.GroupMembers(r.Context(), groupID)
		if err != nil {
			writeContactsAPIError(w, err, "群聊不存在")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"group_id": groupID,
			"members":  members,
		})
	}
}

func contactDetailHandler(source any) http.HandlerFunc {
	backend := contactsBackend(source)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			writeMethodNotAllowed(w, http.MethodGet)
			return
		}
		username := strings.TrimSpace(r.URL.Query().Get("username"))
		if username == "" {
			writeAPIError(w, http.StatusBadRequest, "缺少 username")
			return
		}
		contact, err := backend.Contact(r.Context(), username)
		if err != nil {
			writeContactsAPIError(w, err, "联系人不存在")
			return
		}
		writeJSON(w, http.StatusOK, contact)
	}
}

func refreshContactsHandler(source any) http.HandlerFunc {
	backend := contactsBackend(source)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodPost {
			writeMethodNotAllowed(w, http.MethodPost)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), contactsRefreshAPITimeout)
		defer cancel()
		counts, err := backend.Refresh(ctx)
		if err != nil {
			Error("刷新联系人缓存失败", "err", err)
			switch {
			case errors.Is(err, errContactCacheRefreshInProgress):
				writeAPIError(w, http.StatusConflict, "联系人缓存正在刷新")
			case errors.Is(err, context.DeadlineExceeded):
				writeAPIError(w, http.StatusGatewayTimeout, "刷新联系人缓存超时")
			case errors.Is(err, context.Canceled):
				writeAPIError(w, http.StatusRequestTimeout, "刷新联系人缓存已取消")
			case isAgentDeadlineExceeded(err):
				writeAPIError(w, http.StatusGatewayTimeout, publicAgentError(err, "刷新联系人缓存超时"))
			case isAgentClientSource(source):
				writeContactsAPIError(w, err, "刷新联系人缓存失败")
			default:
				writeAPIError(w, http.StatusInternalServerError, "刷新联系人缓存失败")
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":       "ok",
			"contacts":     counts.Contacts,
			"groups":       counts.Groups,
			"members":      counts.Members,
			"refreshed_at": counts.RefreshedAt,
		})
	}
}

func isAgentClientSource(source any) bool {
	_, ok := source.(AgentClient)
	return ok
}

func isAgentDeadlineExceeded(err error) bool {
	var agentError *AgentError
	return errors.As(err, &agentError) && agentError.Code == AgentErrorCodeDeadlineExceeded
}

func writeContactsAPIError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, errContactsUnavailable):
		writeAPIError(w, http.StatusServiceUnavailable, fallback)
	case errors.Is(err, errContactNotFound), errors.Is(err, errGroupNotFound):
		writeAPIError(w, http.StatusNotFound, fallback)
	default:
		writeAPIError(w, agentHTTPStatus(err), publicAgentError(err, fallback))
	}
}

func parseBoundedIntQuery(r *http.Request, name string, defaultValue, minValue, maxValue int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minValue || value > maxValue {
		return 0, &queryParameterError{name: name}
	}
	return value, nil
}

type queryParameterError struct {
	name string
}

func (e *queryParameterError) Error() string {
	return e.name + " 非法"
}

func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeAPIError(w, http.StatusMethodNotAllowed, "请求方法不支持")
}

func writeAPIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if status != http.StatusNoContent {
		_ = json.NewEncoder(w).Encode(value)
	}
}
