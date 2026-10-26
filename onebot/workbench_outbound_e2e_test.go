package main

import (
	"context"
	"testing"
	"time"
)

func TestWorkbenchApprovedControlledSends(t *testing.T) {
	tests := []struct {
		name      string
		chatType  ChatType
		peerID    string
		operation WorkbenchOperation
	}{
		{
			name:      "private",
			chatType:  ChatTypePrivate,
			peerID:    "wxid_offline_private",
			operation: WorkbenchOperationMessagesSendPrivate,
		},
		{
			name:      "group",
			chatType:  ChatTypeGroup,
			peerID:    "offline_group@chatroom",
			operation: WorkbenchOperationMessagesSendGroup,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, automation, _, service, event, turn := workbenchE2EOutboundFixture(
				t,
				test.chatType,
				test.peerID,
				test.operation,
			)
			if client.callCount("message.send_controlled") != 0 {
				t.Fatal("controlled send occurred before approval")
			}
			request, action := workbenchE2EPendingPermission(t, automation, turn.ID)
			if action.Recipient == nil ||
				action.Recipient.AudienceID != event.ID ||
				action.Recipient.AudienceVersion != event.Version ||
				action.Recipient.ChatType != test.chatType ||
				action.Recipient.PeerID != test.peerID {
				t.Fatalf("persisted recipient binding = %+v, event = %+v",
					action.Recipient, event)
			}

			decided, executed, err := service.DecidePermissionRequest(
				context.Background(),
				request.ID,
				automationPermissionDecisionRequest{
					ExpectedVersion:       request.Version,
					ExpectedActionVersion: action.Version,
					Decision:              AutomationPermissionAllowOnce,
					OperatorID:            "operator-outbound",
					Reason:                "approved offline controlled send",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if decided.Decision != AutomationPermissionAllowOnce ||
				decided.OperatorID != "operator-outbound" ||
				executed.Status != AutomationActionSucceeded {
				t.Fatalf("approved result = %+v / %+v", decided, executed)
			}
			if client.callCount("message.send_controlled") != 1 {
				t.Fatalf("SendControlled calls = %d, want 1",
					client.callCount("message.send_controlled"))
			}

			sent := workbenchE2ESendRequest(t, client)
			state := automation.Snapshot()
			if len(state.Outbox) != 1 ||
				sent.IdempotencyKey != state.Outbox[0].IdempotencyKey ||
				sent.Recipient.ChatType != test.chatType ||
				sent.Recipient.PeerID != test.peerID ||
				sent.Authorization.ActionID != action.ID ||
				sent.Authorization.PermissionRequestID != request.ID ||
				sent.Authorization.AudienceID != event.ID ||
				sent.Authorization.AudienceVersion != event.Version {
				t.Fatalf("controlled send/outbox binding = %+v / %+v",
					sent, state.Outbox)
			}
		})
	}
}

func TestWorkbenchRejectedOutboundStaysOffline(t *testing.T) {
	t.Run("deny_once", func(t *testing.T) {
		client, automation, _, service, _, turn := workbenchE2EOutboundFixture(
			t,
			ChatTypePrivate,
			"wxid_denied",
			WorkbenchOperationMessagesSendPrivate,
		)
		request, action := workbenchE2EPendingPermission(t, automation, turn.ID)
		decided, denied, err := service.DecidePermissionRequest(
			context.Background(),
			request.ID,
			automationPermissionDecisionRequest{
				ExpectedVersion:       request.Version,
				ExpectedActionVersion: action.Version,
				Decision:              AutomationPermissionDenyOnce,
				OperatorID:            "operator-deny",
				Reason:                "remain offline",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if decided.Status != AutomationPermissionStatusDenied ||
			denied.Status != AutomationActionDenied ||
			client.callCount("message.send_controlled") != 0 {
			t.Fatalf("denied outbound = %+v / %+v, calls=%d",
				decided, denied, client.callCount("message.send_controlled"))
		}
	})

	for _, decision := range []AutomationPermissionDecision{
		AutomationPermissionAllowRun,
		AutomationPermissionAllowPersistent,
	} {
		t.Run(string(decision), func(t *testing.T) {
			client, automation, _, service, _, turn := workbenchE2EOutboundFixture(
				t,
				ChatTypePrivate,
				"wxid_reusable_rejected",
				WorkbenchOperationMessagesSendPrivate,
			)
			request, action := workbenchE2EPendingPermission(t, automation, turn.ID)
			_, _, err := service.DecidePermissionRequest(
				context.Background(),
				request.ID,
				automationPermissionDecisionRequest{
					ExpectedVersion:       request.Version,
					ExpectedActionVersion: action.Version,
					Decision:              decision,
					OperatorID:            "operator-reusable",
				},
			)
			if err == nil {
				t.Fatalf("%s unexpectedly accepted for workbench.allow_once", decision)
			}
			if client.callCount("message.send_controlled") != 0 {
				t.Fatalf("SendControlled calls = %d, want 0",
					client.callCount("message.send_controlled"))
			}
		})
	}

	for _, test := range []struct {
		name      string
		chatType  ChatType
		peerID    string
		operation WorkbenchOperation
		mutate    func(*MessageEvent)
	}{
		{
			name:      "event_version_only",
			chatType:  ChatTypePrivate,
			peerID:    "wxid_version_original",
			operation: WorkbenchOperationMessagesSendPrivate,
			mutate: func(event *MessageEvent) {
				event.Version++
				event.UpdatedAt = event.UpdatedAt.Add(time.Second)
			},
		},
		{
			name:      "private_peer_id_only",
			chatType:  ChatTypePrivate,
			peerID:    "wxid_private_original",
			operation: WorkbenchOperationMessagesSendPrivate,
			mutate: func(event *MessageEvent) {
				event.PeerID = "wxid_private_replacement"
			},
		},
		{
			name:      "group_group_id_only",
			chatType:  ChatTypeGroup,
			peerID:    "group_original@chatroom",
			operation: WorkbenchOperationMessagesSendGroup,
			mutate: func(event *MessageEvent) {
				event.GroupID = "group_replacement@chatroom"
			},
		},
		{
			name:      "chat_type_mismatch",
			chatType:  ChatTypeGroup,
			peerID:    "group_chat_type@chatroom",
			operation: WorkbenchOperationMessagesSendGroup,
			mutate: func(event *MessageEvent) {
				event.ChatType = ChatTypePrivate
				event.Disposition = MessageDispositionPrivateCandidate
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, automation, crm, service, event, turn := workbenchE2EOutboundFixture(
				t,
				test.chatType,
				test.peerID,
				test.operation,
			)
			request, action := workbenchE2EPendingPermission(t, automation, turn.ID)
			err := crm.Update(func(state *CRMState, _ *IDAllocator) error {
				for i := range state.MessageEvents {
					if state.MessageEvents[i].ID == event.ID {
						test.mutate(&state.MessageEvents[i])
						state.MessageEvents[i].MessageKey =
							MessageKeyForEvent(state.MessageEvents[i])
						break
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			_, executed, err := service.DecidePermissionRequest(
				context.Background(),
				request.ID,
				automationPermissionDecisionRequest{
					ExpectedVersion:       request.Version,
					ExpectedActionVersion: action.Version,
					Decision:              AutomationPermissionAllowOnce,
					OperatorID:            "operator-stale",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if executed.Status == AutomationActionSucceeded ||
				client.callCount("message.send_controlled") != 0 {
				t.Fatalf("stale recipient executed = %+v, calls=%d",
					executed, client.callCount("message.send_controlled"))
			}
		})
	}

	t.Run("unavailable_frida_operations", func(t *testing.T) {
		client := &task003AgentClient{}
		restore := installHRWorkbenchAgentClient(client)
		t.Cleanup(restore)
		dispatcher := NewDefaultWorkbenchToolDispatcher(crmMessageOpenTestStore(t))
		for _, fixture := range []struct {
			tool      AutomationToolName
			operation WorkbenchOperation
		}{
			{AutomationToolWorkbenchQuery, WorkbenchOperationFridaStatus},
			{AutomationToolWorkbenchAllowOnce, WorkbenchOperationFridaInitialize},
			{AutomationToolWorkbenchAllowOnce, WorkbenchOperationFridaRescan},
			{AutomationToolWorkbenchAllowOnce, WorkbenchOperationFridaDetach},
		} {
			result := task001Dispatch(
				t,
				dispatcher,
				WorkbenchExecutionContext{},
				fixture.tool,
				fixture.operation,
				WorkbenchEmptyArguments{},
			)
			task001AssertFailure(t, result, WorkbenchErrorUnavailable)
		}
		for _, call := range []string{
			"frida.status",
			"frida.initialize",
			"frida.rescan",
			"frida.detach",
			"message.send_controlled",
		} {
			if client.callCount(call) != 0 {
				t.Fatalf("%s calls = %d, want 0", call, client.callCount(call))
			}
		}
	})
}

func workbenchE2EOutboundFixture(
	t *testing.T,
	chatType ChatType,
	peerID string,
	operation WorkbenchOperation,
) (
	*task003AgentClient,
	*AutomationStore,
	*CRMStore,
	*AutomationService,
	MessageEvent,
	AutomationTurn,
) {
	t.Helper()
	crm := crmMessageOpenTestStore(t)
	event := workbenchE2ESeedOutboundEvent(t, crm, chatType, peerID)
	const userText = "prepare controlled send"
	provider := workbenchE2ENewProvider(t,
		workbenchE2EStep(t, "Controlled send awaiting approval",
			workbenchE2EToolCall(t, userText, "controlled_send",
				AutomationToolWorkbenchAllowOnce,
				operation,
				WorkbenchControlledSendArguments{
					EventID: event.ID,
					Intent:  AutomationReplyIntentGeneral,
					Text:    "Offline controlled message",
				},
			),
		),
	)
	client := &task003AgentClient{
		sendResult: AutomationDeliveryResult{
			Status:            AutomationDeliverySent,
			DispatchAttempted: true,
		},
	}
	restoreAgent := installHRWorkbenchAgentClient(client)
	t.Cleanup(restoreAgent)
	sender := NewMsgChanAutomationTextSender(nil)
	automation, engine, service := workbenchE2EOpen(t, crm, provider, sender)
	workbenchE2EInstallRuntime(t, automation, service)
	turn := workbenchE2ERunChat(t, automation, crm, engine, userText)
	return client, automation, crm, service, event, turn
}

func workbenchE2ESeedOutboundEvent(
	t *testing.T,
	crm *CRMStore,
	chatType ChatType,
	peerID string,
) MessageEvent {
	t.Helper()
	record := &ForwardRecord{
		T:     time.Now().UTC().Unix(),
		Chat:  string(chatType),
		Dir:   "in",
		Peer:  peerID,
		Text:  "Offline outbound source",
		Kinds: []string{"text"},
		Mid:   "mid-" + string(chatType) + "-outbound",
	}
	if chatType == ChatTypePrivate {
		record.Author = peerID
	} else {
		record.Author = "wxid_group_author"
		record.GroupID = peerID
	}
	event, created, err := crmMessageCaptureNormalizedInboundToStore(
		crm,
		record,
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("outbound source event was not created")
	}
	if chatType == ChatTypeGroup {
		event.GroupID = peerID
		event.PeerID = peerID
		err = crm.Update(func(state *CRMState, _ *IDAllocator) error {
			for i := range state.MessageEvents {
				if state.MessageEvents[i].ID == event.ID {
					state.MessageEvents[i] = event
					break
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return event
}

func workbenchE2EInstallRuntime(
	t *testing.T,
	store *AutomationStore,
	service *AutomationService,
) {
	t.Helper()
	automationRuntimeMu.Lock()
	previous := automationRuntime
	automationRuntime = &AutomationRuntime{store: store, service: service}
	automationRuntimeMu.Unlock()
	t.Cleanup(func() {
		automationRuntimeMu.Lock()
		if automationRuntime != nil &&
			automationRuntime.store == store &&
			automationRuntime.service == service {
			automationRuntime = previous
		}
		automationRuntimeMu.Unlock()
	})
}

func workbenchE2ESendRequest(
	t *testing.T,
	client *task003AgentClient,
) AgentControlledSendRequest {
	t.Helper()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.sendRequests) != 1 {
		t.Fatalf("controlled requests = %+v", client.sendRequests)
	}
	return client.sendRequests[0]
}
