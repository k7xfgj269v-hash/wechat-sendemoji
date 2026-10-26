package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newAPITestContactCache(t *testing.T) *ContactCache {
	t.Helper()
	cache := NewContactCache(func() (ContactSnapshot, error) {
		return ContactSnapshot{
			Contacts: []Contact{
				{Username: "wxid_1", NickName: "Alice", LocalType: 3},
				{Username: "wxid_2", Remark: "Builder", LocalType: 4},
			},
			Groups:  []Group{{Username: "group@chatroom", NickName: "Test group", MemberCount: 1}},
			Members: []GroupMember{{GroupID: "group@chatroom", Username: "wxid_1", NickName: "Alice"}},
		}, nil
	})
	if err := cache.Refresh(); err != nil {
		t.Fatal(err)
	}
	return cache
}

func TestContactsHandlerFiltersAndValidates(t *testing.T) {
	cache := newAPITestContactCache(t)

	request := httptest.NewRequest(http.MethodGet, "/api/contacts?keyword=ali&local_type=3&limit=1", nil)
	response := httptest.NewRecorder()
	contactsHandler(cache)(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result struct {
		Total    int       `json:"total"`
		Contacts []Contact `json:"contacts"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Contacts) != 1 || result.Contacts[0].Username != "wxid_1" {
		t.Fatalf("result = %#v", result)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/contacts?limit=0", nil)
	response = httptest.NewRecorder()
	contactsHandler(cache)(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/contacts", nil)
	response = httptest.NewRecorder()
	contactsHandler(cache)(response, request)
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestContactAndGroupHandlersReturnExpectedStatuses(t *testing.T) {
	cache := newAPITestContactCache(t)

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
		status  int
	}{
		{"group members", groupMembersHandler(cache), "/api/group_members?group_id=group@chatroom", http.StatusOK},
		{"missing group", groupMembersHandler(cache), "/api/group_members?group_id=missing", http.StatusNotFound},
		{"detail", contactDetailHandler(cache), "/api/contact_detail?username=wxid_1", http.StatusOK},
		{"missing detail", contactDetailHandler(cache), "/api/contact_detail?username=missing", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			tc.handler(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if response.Code != tc.status {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}

	unloaded := NewContactCache(nil)
	response := httptest.NewRecorder()
	contactsHandler(unloaded)(response, httptest.NewRequest(http.MethodGet, "/api/contacts", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unloaded status = %d", response.Code)
	}

	failing := NewContactCache(func() (ContactSnapshot, error) {
		return ContactSnapshot{}, errors.New("/private/contact.db unavailable")
	})
	response = httptest.NewRecorder()
	refreshContactsHandler(failing)(response, httptest.NewRequest(http.MethodPost, "/api/contacts/refresh", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("refresh failure status = %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "/private/contact.db") {
		t.Fatalf("refresh response leaked internal error: %s", response.Body.String())
	}
}

func TestRefreshContactsHandlerBoundsAndRejectsConcurrentRefresh(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		cache := NewContactCache(func() (ContactSnapshot, error) {
			<-release
			return ContactSnapshot{}, nil
		})
		request := httptest.NewRequest(http.MethodPost, "/api/contacts/refresh", nil)
		ctx, cancel := context.WithTimeout(request.Context(), 20*time.Millisecond)
		defer cancel()
		response := httptest.NewRecorder()
		refreshContactsHandler(cache)(response, request.WithContext(ctx))
		if response.Code != http.StatusGatewayTimeout {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	})

	t.Run("in progress", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		cache := NewContactCache(func() (ContactSnapshot, error) {
			close(started)
			<-release
			return ContactSnapshot{}, nil
		})
		firstDone := make(chan error, 1)
		go func() {
			firstDone <- cache.RefreshContext(context.Background())
		}()
		<-started

		response := httptest.NewRecorder()
		refreshContactsHandler(cache)(
			response,
			httptest.NewRequest(http.MethodPost, "/api/contacts/refresh", nil),
		)
		if response.Code != http.StatusConflict {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}

		close(release)
		if err := <-firstDone; err != nil {
			t.Fatalf("first refresh error = %v", err)
		}
	})
}

func TestContactHandlersDelegateToAgentClient(t *testing.T) {
	localType := 3
	client := &task003AgentClient{
		searchResult: AgentContactSearchResult{
			Total:    1,
			Contacts: []Contact{{Username: "wxid_agent", NickName: "Agent Alice"}},
		},
		groupsResult:       []Group{{Username: "team@chatroom", MemberCount: 2}},
		groupMembersResult: []GroupMember{{GroupID: "team@chatroom", Username: "wxid_agent"}},
		contactResult:      Contact{Username: "wxid_agent", NickName: "Agent Alice"},
		refreshResult: AgentContactCounts{
			Contacts: 4, Groups: 2, Members: 9, RefreshedAt: "2026-07-27T18:00:00Z",
		},
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		target  string
		call    string
	}{
		{
			name:    "search",
			handler: contactsHandler(client),
			method:  http.MethodGet,
			target:  "/api/contacts?keyword=alice&local_type=3&limit=25&offset=5",
			call:    "contacts.search",
		},
		{
			name:    "groups",
			handler: groupsHandler(client),
			method:  http.MethodGet,
			target:  "/api/groups",
			call:    "groups.list",
		},
		{
			name:    "group members",
			handler: groupMembersHandler(client),
			method:  http.MethodGet,
			target:  "/api/group_members?group_id=team@chatroom",
			call:    "groups.members",
		},
		{
			name:    "contact",
			handler: contactDetailHandler(client),
			method:  http.MethodGet,
			target:  "/api/contact_detail?username=wxid_agent",
			call:    "contacts.get",
		},
		{
			name:    "refresh",
			handler: refreshContactsHandler(client),
			method:  http.MethodPost,
			target:  "/api/contacts/refresh",
			call:    "contacts.refresh",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := client.callCount(test.call)
			response := httptest.NewRecorder()
			test.handler(response, httptest.NewRequest(test.method, test.target, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
			}
			if got := client.callCount(test.call); got != before+1 {
				t.Fatalf("%s calls = %d, want %d", test.call, got, before+1)
			}
			if test.name == "refresh" &&
				(!strings.Contains(response.Body.String(), `"members":9`) ||
					!strings.Contains(response.Body.String(), `"refreshed_at":"2026-07-27T18:00:00Z"`)) {
				t.Fatalf("refresh body = %s", response.Body.String())
			}
		})
	}

	if client.searchRequest.Keyword != "alice" ||
		client.searchRequest.LocalType == nil ||
		*client.searchRequest.LocalType != localType ||
		client.searchRequest.Limit != 25 ||
		client.searchRequest.Offset != 5 {
		t.Fatalf("search request = %#v", client.searchRequest)
	}
	if client.groupID != "team@chatroom" {
		t.Fatalf("group ID = %q", client.groupID)
	}
	if client.contactID != "wxid_agent" {
		t.Fatalf("contact ID = %q", client.contactID)
	}
	if client.refreshDeadline.IsZero() {
		t.Fatal("refresh request has no deadline")
	}
	if remaining := time.Until(client.refreshDeadline); remaining <= 0 || remaining > contactsRefreshAPITimeout {
		t.Fatalf("refresh deadline remaining = %v", remaining)
	}
}

func TestContactAgentErrorsRemainStableAndDoNotLeakPaths(t *testing.T) {
	client := &task003AgentClient{
		searchErr: &AgentError{
			Code:      AgentErrorCodeUnavailable,
			Message:   "agent unavailable",
			Retryable: true,
		},
		contactErr: &AgentError{
			Code:    AgentErrorCodeNotFound,
			Message: "contact not found",
		},
		refreshErr: errors.New("/private/contact.db unavailable"),
	}

	response := httptest.NewRecorder()
	contactsHandler(client)(
		response,
		httptest.NewRequest(http.MethodGet, "/api/contacts", nil),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("search status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	contactDetailHandler(client)(
		response,
		httptest.NewRequest(http.MethodGet, "/api/contact_detail?username=missing", nil),
	)
	if response.Code != http.StatusNotFound {
		t.Fatalf("detail status = %d, body = %s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	refreshContactsHandler(client)(
		response,
		httptest.NewRequest(http.MethodPost, "/api/contacts/refresh", nil),
	)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("refresh status = %d, body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "/private/contact.db") {
		t.Fatalf("refresh response leaked path: %s", response.Body.String())
	}
}
