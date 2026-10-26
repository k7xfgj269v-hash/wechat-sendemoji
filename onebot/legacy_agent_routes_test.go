package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type task003SequencedAgentClient struct {
	task003AgentClient
	results []AutomationDeliveryResult
	errs    []error
}

func (c *task003SequencedAgentClient) SendControlled(
	_ context.Context,
	request AgentControlledSendRequest,
) (AutomationDeliveryResult, error) {
	c.mu.Lock()
	index := len(c.sendRequests)
	c.calls = append(c.calls, "message.send_controlled")
	c.sendRequests = append(c.sendRequests, request)
	var result AutomationDeliveryResult
	var err error
	if index < len(c.results) {
		result = c.results[index]
	}
	if index < len(c.errs) {
		err = c.errs[index]
	}
	c.mu.Unlock()
	return result, err
}

func TestManualMultiSegmentFailureAfterSentIsUnknown(t *testing.T) {
	client := &task003SequencedAgentClient{
		results: []AutomationDeliveryResult{
			{
				Status:            AutomationDeliverySent,
				DispatchAttempted: true,
			},
			{
				Status:            AutomationDeliveryFailed,
				DispatchAttempted: false,
				ErrorCode:         AgentErrorCodeDeliveryFailed,
				ErrorMessage:      "second segment failed",
			},
		},
		errs: []error{
			nil,
			&AgentError{
				Code:    AgentErrorCodeDeliveryFailed,
				Message: "second segment failed",
			},
		},
	}
	result, err := deliverManualAgentContents(
		context.Background(),
		client,
		"http",
		AgentRecipient{ChatType: ChatTypePrivate, PeerID: "wxid_peer"},
		[]AgentControlledContent{
			{Type: AgentControlledContentText, Text: "first"},
			{Type: AgentControlledContentText, Text: "second"},
		},
	)
	var agentErr *AgentError
	if err == nil || !errors.As(err, &agentErr) {
		t.Fatalf("delivery error = %v, want AgentError", err)
	}
	if result.Status != AutomationDeliveryUnknown || !result.DispatchAttempted {
		t.Fatalf("delivery result = %#v, want unknown with dispatch attempted", result)
	}
	if result.ErrorCode != "partial_delivery_unknown" {
		t.Fatalf("error code = %q, want partial_delivery_unknown", result.ErrorCode)
	}
	if client.callCount("message.send_controlled") != 2 {
		t.Fatalf(
			"SendControlled calls = %d, want 2",
			client.callCount("message.send_controlled"),
		)
	}
}

func TestLegacySendRouteUsesControlledAgentDelivery(t *testing.T) {
	client := &task003AgentClient{
		sendResult: AutomationDeliveryResult{
			Status:            AutomationDeliverySent,
			DispatchAttempted: true,
		},
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/send_private_msg",
		strings.NewReader(`{
			"user_id":"wxid_peer",
			"message":[
				{"type":"text","data":{"text":"hello"}},
				{"type":"file","data":{"file":"attachment-opaque-1"}}
			]
		}`),
	)
	request.RemoteAddr = "127.0.0.1:4567"
	sendHandlerWithAgent(client)(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if client.callCount("message.send_controlled") != 2 {
		t.Fatalf("SendControlled calls = %d, want 2", client.callCount("message.send_controlled"))
	}
	if len(client.sendRequests) != 2 {
		t.Fatalf("requests = %#v", client.sendRequests)
	}
	attachment := client.sendRequests[0]
	text := client.sendRequests[1]
	if attachment.Content.Type != AgentControlledContentAttachment ||
		attachment.Content.AttachmentID != "attachment-opaque-1" {
		t.Fatalf("attachment request = %#v", attachment)
	}
	if text.Content.Type != AgentControlledContentText || text.Content.Text != "hello" {
		t.Fatalf("text request = %#v", text)
	}
	for index, controlled := range client.sendRequests {
		if err := ValidateAgentControlledSendRequest(controlled); err != nil {
			t.Fatalf("request %d validation error = %v", index, err)
		}
		if controlled.Recipient.ChatType != ChatTypePrivate ||
			controlled.Recipient.PeerID != "wxid_peer" {
			t.Fatalf("request %d recipient = %#v", index, controlled.Recipient)
		}
	}
}

func TestLegacySendRouteMakesAmbiguityVisibleWithoutRetry(t *testing.T) {
	client := &task003AgentClient{
		sendResult: AutomationDeliveryResult{
			Status:            AutomationDeliveryUnknown,
			DispatchAttempted: true,
			ErrorCode:         AgentErrorCodeDeliveryAmbiguous,
			ErrorMessage:      "dispatch acknowledgement was lost",
		},
		sendErr: &AgentError{
			Code:      AgentErrorCodeDeliveryAmbiguous,
			Message:   "dispatch acknowledgement was lost",
			Retryable: false,
		},
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/send_private_msg",
		strings.NewReader(`{
			"user_id":"wxid_peer",
			"message":[{"type":"text","data":{"text":"hello"}}]
		}`),
	)
	request.RemoteAddr = "127.0.0.1:4567"
	sendHandlerWithAgent(client)(response, request)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if client.callCount("message.send_controlled") != 1 {
		t.Fatalf("SendControlled calls = %d, want 1", client.callCount("message.send_controlled"))
	}
	if !strings.Contains(response.Body.String(), `"status":"unknown"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestLegacyContactSearchUsesAgentClient(t *testing.T) {
	client := &task003AgentClient{
		searchResult: AgentContactSearchResult{
			Total: 1,
			Contacts: []Contact{{
				Username: "wxid_agent",
				NickName: "Agent Alice",
				Alias:    "alice",
			}},
		},
	}
	response := httptest.NewRecorder()
	searchContactHandlerWithAgent(client)(
		response,
		httptest.NewRequest(
			http.MethodPost,
			"/search_contact",
			strings.NewReader(`{"keyword":" alice "}`),
		),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if client.callCount("contacts.search") != 1 {
		t.Fatalf("SearchContacts calls = %d, want 1", client.callCount("contacts.search"))
	}
	if client.searchRequest.Keyword != "alice" || client.searchRequest.Limit != 200 {
		t.Fatalf("search request = %#v", client.searchRequest)
	}
	if !strings.Contains(response.Body.String(), `"username":"wxid_agent"`) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestLegacyWebSocketSendUsesControlledAgentDelivery(t *testing.T) {
	client := &task003AgentClient{
		sendResult: AutomationDeliveryResult{
			Status:            AutomationDeliverySent,
			DispatchAttempted: true,
		},
	}
	err := SendWSWithAgent(context.Background(), client, &WSParams{
		Message: "hello from websocket",
		UserID:  "wxid_peer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.callCount("message.send_controlled") != 1 {
		t.Fatalf("SendControlled calls = %d, want 1", client.callCount("message.send_controlled"))
	}
	if len(client.sendRequests) != 1 ||
		client.sendRequests[0].Content.Type != AgentControlledContentText ||
		client.sendRequests[0].Content.Text != "hello from websocket" {
		t.Fatalf("requests = %#v", client.sendRequests)
	}
}

func TestLegacyAgentRoutesRejectRemoteOrMissingAgentBeforeDispatch(t *testing.T) {
	client := &task003AgentClient{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/send_private_msg",
		strings.NewReader(`{
			"user_id":"wxid_peer",
			"message":[{"type":"text","data":{"text":"hello"}}]
		}`),
	)
	request.RemoteAddr = "203.0.113.9:4567"
	sendHandlerWithAgent(client)(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("remote status = %d, body = %s", response.Code, response.Body.String())
	}
	if client.callCount("message.send_controlled") != 0 {
		t.Fatalf("remote SendControlled calls = %d", client.callCount("message.send_controlled"))
	}

	response = httptest.NewRecorder()
	request = httptest.NewRequest(
		http.MethodPost,
		"/send_private_msg",
		strings.NewReader(`{
			"user_id":"wxid_peer",
			"message":[{"type":"text","data":{"text":"hello"}}]
		}`),
	)
	request.RemoteAddr = "127.0.0.1:4567"
	sendHandlerWithAgent(nil)(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing agent status = %d, body = %s", response.Code, response.Body.String())
	}
}
