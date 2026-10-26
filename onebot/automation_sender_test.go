package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAgentAutomationTextSenderForwardsValidatedControlledRequest(t *testing.T) {
	client := &task003AgentClient{
		sendResult: AutomationDeliveryResult{
			Status:            AutomationDeliverySent,
			DispatchAttempted: true,
		},
	}
	resolved := validAgentControlledSendRequest(t)
	resolver := AgentAutomationControlledSendResolverFunc(func(
		_ context.Context,
		chatType ChatType,
		peerID string,
		text string,
	) (AgentControlledSendRequest, error) {
		if chatType != ChatTypePrivate || peerID != "wxid_peer" || text != "hello" {
			t.Fatalf("resolver input = %q %q %q", chatType, peerID, text)
		}
		resolved.Recipient = AgentRecipient{ChatType: chatType, PeerID: peerID}
		resolved.Content = AgentControlledContent{Type: AgentControlledContentText, Text: text}
		hash, err := AgentControlledContentSHA256(resolved.Content)
		if err != nil {
			return AgentControlledSendRequest{}, err
		}
		resolved.Authorization.ContentSHA256 = hash
		return resolved, nil
	})

	result := NewAgentAutomationTextSender(client, resolver).SendPrivateText(
		context.Background(),
		" wxid_peer ",
		" hello ",
	)
	assertAutomationDelivery(t, result, AutomationDeliverySent, true)
	if client.callCount("message.send_controlled") != 1 {
		t.Fatalf("SendControlled calls = %d, want 1", client.callCount("message.send_controlled"))
	}
	if len(client.sendRequests) != 1 || client.sendRequests[0].IdempotencyKey != resolved.IdempotencyKey {
		t.Fatalf("controlled requests = %#v", client.sendRequests)
	}
}

func TestAgentAutomationTextSenderFailsClosedBeforeDispatch(t *testing.T) {
	tests := []struct {
		name     string
		resolver AgentAutomationControlledSendResolver
		wantCode string
	}{
		{
			name: "resolver rejects ambiguous authorization",
			resolver: AgentAutomationControlledSendResolverFunc(func(
				context.Context,
				ChatType,
				string,
				string,
			) (AgentControlledSendRequest, error) {
				return AgentControlledSendRequest{}, errors.New("automation dispatch authorization is ambiguous")
			}),
			wantCode: "authorization_failed",
		},
		{
			name: "resolver returns invalid authorization",
			resolver: AgentAutomationControlledSendResolverFunc(func(
				context.Context,
				ChatType,
				string,
				string,
			) (AgentControlledSendRequest, error) {
				return AgentControlledSendRequest{}, nil
			}),
			wantCode: "authorization_failed",
		},
		{
			name:     "resolver unavailable",
			resolver: nil,
			wantCode: "authorization_unavailable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &task003AgentClient{}
			result := NewAgentAutomationTextSender(client, test.resolver).SendPrivateText(
				context.Background(),
				"wxid_peer",
				"hello",
			)
			assertAutomationDelivery(t, result, AutomationDeliveryFailed, false)
			if result.ErrorCode != test.wantCode {
				t.Fatalf("error code = %q, want %q", result.ErrorCode, test.wantCode)
			}
			if client.callCount("message.send_controlled") != 0 {
				t.Fatalf("SendControlled calls = %d, want 0", client.callCount("message.send_controlled"))
			}
		})
	}
}

func TestAgentAutomationTextSenderPreservesDeliveryAmbiguity(t *testing.T) {
	client := &task003AgentClient{
		sendErr: &AgentError{
			Code:      AgentErrorCodeDeliveryAmbiguous,
			Message:   "dispatch acknowledgement was lost",
			Retryable: false,
		},
	}
	request := validAgentControlledSendRequest(t)
	resolver := AgentAutomationControlledSendResolverFunc(func(
		context.Context,
		ChatType,
		string,
		string,
	) (AgentControlledSendRequest, error) {
		return request, nil
	})

	result := NewAgentAutomationTextSender(client, resolver).SendPrivateText(
		context.Background(),
		"wxid-alice",
		"hello",
	)
	assertAutomationDelivery(t, result, AutomationDeliveryUnknown, true)
	if result.ErrorCode != AgentErrorCodeDeliveryAmbiguous {
		t.Fatalf("error code = %q, want %q", result.ErrorCode, AgentErrorCodeDeliveryAmbiguous)
	}
}

func TestAutomationRuntimeResolverBindsPersistedOutboxActionAndPermission(t *testing.T) {
	store := automationOutboxTestStore(t)
	action, permission := task003AuthorizeAutomationAction(t, store, 7)
	entry, created, err := store.EnsureText(
		context.Background(),
		action,
		action.Recipient.ChatType,
		action.Recipient.PeerID,
		"hello",
	)
	if err != nil || !created {
		t.Fatalf("EnsureText() = %#v, %v, %v", entry, created, err)
	}
	entry, claimed, err := store.ClaimPending(context.Background(), entry.ID)
	if err != nil || !claimed {
		t.Fatalf("ClaimPending() = %#v, %v, %v", entry, claimed, err)
	}

	restore := task003InstallAutomationStore(store)
	t.Cleanup(restore)
	request, err := (automationRuntimeAgentSendResolver{}).ResolveAgentControlledSend(
		context.Background(),
		entry.ChatType,
		entry.PeerID,
		entry.Text,
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.IdempotencyKey != entry.IdempotencyKey ||
		request.Authorization.ActionID != action.ID ||
		request.Authorization.PermissionRequestID != permission.ID {
		t.Fatalf("controlled request metadata = %#v", request)
	}
	if request.Authorization.AudienceID != action.Recipient.AudienceID ||
		request.Authorization.AudienceVersion != action.Recipient.AudienceVersion {
		t.Fatalf("audience metadata = %#v, action recipient = %#v", request.Authorization, action.Recipient)
	}
	wantHash, err := AgentControlledContentSHA256(request.Content)
	if err != nil {
		t.Fatal(err)
	}
	if request.Authorization.ContentSHA256 != wantHash ||
		request.Authorization.FrequencyScope.ID != "permission-"+permission.ID ||
		request.Authorization.FrequencyScope.Limit != 1 {
		t.Fatalf("authorization binding = %#v", request.Authorization)
	}
	if err := ValidateAgentControlledSendRequest(request); err != nil {
		t.Fatalf("ValidateAgentControlledSendRequest() error = %v", err)
	}
}

func TestAutomationRuntimeResolverFailsClosedWhenDispatchIsAmbiguous(t *testing.T) {
	store := automationOutboxTestStore(t)
	first, _ := task003AuthorizeAutomationAction(t, store, 1)
	firstEntry, created, err := store.EnsureText(
		context.Background(),
		first,
		first.Recipient.ChatType,
		first.Recipient.PeerID,
		"same text",
	)
	if err != nil || !created {
		t.Fatalf("EnsureText(first) = %#v, %v, %v", firstEntry, created, err)
	}
	if _, claimed, err := store.ClaimPending(context.Background(), firstEntry.ID); err != nil || !claimed {
		t.Fatalf("ClaimPending(first) = %v, %v", claimed, err)
	}

	second := automationOutboxTestAddReplacementAction(t, store)
	_, secondPermission := task003AuthorizeStoredAction(t, store, second.ID, 2)
	if secondPermission.ActionID != second.ID {
		t.Fatalf("second permission = %#v", secondPermission)
	}
	second = task003FindAutomationAction(t, store.Snapshot(), second.ID)
	secondEntry, created, err := store.EnsureText(
		context.Background(),
		second,
		second.Recipient.ChatType,
		second.Recipient.PeerID,
		"same text",
	)
	if err != nil || !created {
		t.Fatalf("EnsureText(second) = %#v, %v, %v", secondEntry, created, err)
	}
	if _, claimed, err := store.ClaimPending(context.Background(), secondEntry.ID); err != nil || !claimed {
		t.Fatalf("ClaimPending(second) = %v, %v", claimed, err)
	}

	restore := task003InstallAutomationStore(store)
	t.Cleanup(restore)
	_, err = (automationRuntimeAgentSendResolver{}).ResolveAgentControlledSend(
		context.Background(),
		ChatTypePrivate,
		"wxid_peer",
		"same text",
	)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ResolveAgentControlledSend() error = %v, want ambiguous", err)
	}
}

func TestAgentAutomationTextSenderValidationAndCancellation(t *testing.T) {
	longPeer := strings.Repeat("p", 257)
	longText := strings.Repeat("t", 4001)
	tests := []struct {
		name   string
		ctx    context.Context
		peerID string
		text   string
	}{
		{name: "nil context", peerID: "wxid_peer", text: "hello"},
		{name: "empty peer", ctx: context.Background(), text: "hello"},
		{name: "peer control character", ctx: context.Background(), peerID: "wxid_\npeer", text: "hello"},
		{name: "peer too long", ctx: context.Background(), peerID: longPeer, text: "hello"},
		{name: "group peer", ctx: context.Background(), peerID: "group@chatroom", text: "hello"},
		{name: "URL peer", ctx: context.Background(), peerID: "https://example.com/send", text: "hello"},
		{name: "empty text", ctx: context.Background(), peerID: "wxid_peer"},
		{name: "text control character", ctx: context.Background(), peerID: "wxid_peer", text: "hello\x00"},
		{name: "text too long", ctx: context.Background(), peerID: "wxid_peer", text: longText},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &task003AgentClient{}
			result := NewAgentAutomationTextSender(
				client,
				AgentAutomationControlledSendResolverFunc(func(
					context.Context,
					ChatType,
					string,
					string,
				) (AgentControlledSendRequest, error) {
					t.Fatal("resolver called for invalid input")
					return AgentControlledSendRequest{}, nil
				}),
			).SendPrivateText(test.ctx, test.peerID, test.text)
			assertAutomationDelivery(t, result, AutomationDeliveryFailed, false)
			if client.callCount("message.send_controlled") != 0 {
				t.Fatalf("SendControlled calls = %d, want 0", client.callCount("message.send_controlled"))
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &task003AgentClient{}
	result := NewAgentAutomationTextSender(
		client,
		AgentAutomationControlledSendResolverFunc(func(
			context.Context,
			ChatType,
			string,
			string,
		) (AgentControlledSendRequest, error) {
			t.Fatal("resolver called after cancellation")
			return AgentControlledSendRequest{}, nil
		}),
	).SendPrivateText(ctx, "wxid_peer", "hello")
	assertAutomationDelivery(t, result, AutomationDeliveryFailed, false)
	if result.ErrorCode != "context_canceled" {
		t.Fatalf("error code = %q, want context_canceled", result.ErrorCode)
	}
}

func task003AuthorizeAutomationAction(
	t *testing.T,
	store *AutomationStore,
	audienceVersion uint64,
) (AutomationAction, AutomationPermissionRequest) {
	t.Helper()
	actionID := store.Snapshot().Actions[0].ID
	return task003AuthorizeStoredAction(t, store, actionID, audienceVersion)
}

func task003AuthorizeStoredAction(
	t *testing.T,
	store *AutomationStore,
	actionID string,
	audienceVersion uint64,
) (AutomationAction, AutomationPermissionRequest) {
	t.Helper()
	var action AutomationAction
	var permission AutomationPermissionRequest
	err := store.Update(func(state *AutomationState, allocator *AutomationIDAllocator) error {
		index := automationActionIndex(state, actionID)
		if index < 0 {
			return errors.New("test action not found")
		}
		current := &state.Actions[index]
		current.Tool = AutomationToolReplyAudienceText
		current.Recipient.AudienceID = "audience-task003"
		current.Recipient.AudienceVersion = audienceVersion
		current.Payload = []byte(`{
			"audience_id":"audience-task003",
			"intent":"general",
			"text":"hello"
		}`)
		action = *current
		action.Recipient = &AutomationRecipient{
			AudienceID:      current.Recipient.AudienceID,
			AudienceVersion: current.Recipient.AudienceVersion,
			ChatType:        current.Recipient.ChatType,
			PeerID:          current.Recipient.PeerID,
		}

		now := time.Now().UTC()
		for i := range state.PermissionRequests {
			request := &state.PermissionRequests[i]
			if request.ActionID != current.ID {
				continue
			}
			if request.OperatorID != automationPermissionOperatorAuto {
				request.Tool = current.Tool
				request.Risk = current.Risk
				request.Status = AutomationPermissionStatusAllowed
				request.Version++
				request.Decision = AutomationPermissionAllowOnce
				request.OperatorID = "operator-test"
				request.UpdatedAt = now
			}
			permission = *request
			return nil
		}
		id, err := allocator.Next(AutomationIDKindPermissionRequest)
		if err != nil {
			return err
		}
		permission = AutomationPermissionRequest{
			ID: id, RunID: current.RunID, ActionID: current.ID,
			Tool: current.Tool, Risk: current.Risk,
			Status: AutomationPermissionStatusAllowed, Version: 1,
			Decision: AutomationPermissionAllowOnce, OperatorID: "operator-test",
			CreatedAt: now, UpdatedAt: now,
		}
		state.PermissionRequests = append(state.PermissionRequests, permission)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return action, permission
}

func task003FindAutomationAction(
	t *testing.T,
	state AutomationState,
	actionID string,
) AutomationAction {
	t.Helper()
	for _, action := range state.Actions {
		if action.ID == actionID {
			return action
		}
	}
	t.Fatalf("action %q not found", actionID)
	return AutomationAction{}
}

func task003InstallAutomationStore(store *AutomationStore) func() {
	automationRuntimeMu.Lock()
	previous := automationRuntime
	automationRuntime = &AutomationRuntime{store: store}
	automationRuntimeMu.Unlock()
	return func() {
		automationRuntimeMu.Lock()
		if automationRuntime != nil && automationRuntime.store == store {
			automationRuntime = previous
		}
		automationRuntimeMu.Unlock()
	}
}

func assertAutomationDelivery(
	t *testing.T,
	result AutomationDeliveryResult,
	status AutomationDeliveryStatus,
	dispatchAttempted bool,
) {
	t.Helper()
	if result.Status != status || result.DispatchAttempted != dispatchAttempted {
		t.Fatalf(
			"delivery = %+v, want status %q dispatch_attempted=%t",
			result,
			status,
			dispatchAttempted,
		)
	}
}
