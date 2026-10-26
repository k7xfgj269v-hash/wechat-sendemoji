package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

const (
	automationAgentAuthorizationTTL = 2 * time.Minute
	automationAgentFrequencyWindow  = time.Hour
)

type AgentAutomationControlledSendResolver interface {
	ResolveAgentControlledSend(
		context.Context,
		ChatType,
		string,
		string,
	) (AgentControlledSendRequest, error)
}

type AgentAutomationControlledSendResolverFunc func(
	context.Context,
	ChatType,
	string,
	string,
) (AgentControlledSendRequest, error)

func (f AgentAutomationControlledSendResolverFunc) ResolveAgentControlledSend(
	ctx context.Context,
	chatType ChatType,
	peerID string,
	text string,
) (AgentControlledSendRequest, error) {
	return f(ctx, chatType, peerID, text)
}

type agentAutomationTextSender struct {
	client         AgentClient
	clientProvider func() AgentClient
	resolver       AgentAutomationControlledSendResolver
}

func NewAgentAutomationTextSender(
	client AgentClient,
	resolver AgentAutomationControlledSendResolver,
) AutomationTextSender {
	return &agentAutomationTextSender{client: client, resolver: resolver}
}

func NewMsgChanAutomationTextSender(_ chan<- *SendMsg) AutomationTextSender {
	return &agentAutomationTextSender{
		clientProvider: currentHRWorkbenchAgentClient,
		resolver:       automationRuntimeAgentSendResolver{},
	}
}

func (s *agentAutomationTextSender) SendPrivateText(
	ctx context.Context,
	peerID string,
	text string,
) (result AutomationDeliveryResult) {
	return s.SendText(ctx, ChatTypePrivate, peerID, text)
}

func (s *agentAutomationTextSender) SendText(
	ctx context.Context,
	chatType ChatType,
	peerID string,
	text string,
) AutomationDeliveryResult {
	if ctx == nil {
		return automationDeliveryFailed("invalid_context", "context is required")
	}
	if !IsValidChatType(chatType) {
		return automationDeliveryFailed("validation_error", "chat_type: is invalid")
	}

	normalizedPeerID, err := normalizeAutomationString("peer_id", peerID, 256, true, false)
	if err != nil {
		return automationDeliveryFailed("validation_error", err.Error())
	}
	if chatType == ChatTypePrivate && strings.Contains(normalizedPeerID, "@chatroom") {
		return automationDeliveryFailed("validation_error", "peer_id: must identify a private peer")
	}
	if chatType == ChatTypeGroup && !strings.HasSuffix(normalizedPeerID, "@chatroom") {
		return automationDeliveryFailed("validation_error", "peer_id: must identify a group")
	}
	if parsed, parseErr := url.Parse(normalizedPeerID); parseErr == nil &&
		(parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(normalizedPeerID, "//")) {
		return automationDeliveryFailed("validation_error", "peer_id: URL targets are not allowed")
	}

	normalizedText, err := normalizeAutomationString("text", text, 4000, true, true)
	if err != nil {
		return automationDeliveryFailed("validation_error", err.Error())
	}
	if err := ctx.Err(); err != nil {
		return automationDeliveryFailed(automationContextErrorCode(err), err.Error())
	}

	if s == nil || s.resolver == nil {
		return automationDeliveryFailed("authorization_unavailable", "automation authorization is unavailable")
	}
	client := s.client
	if client == nil && s.clientProvider != nil {
		client = s.clientProvider()
	}
	if client == nil {
		return automationDeliveryFailed("sender_unavailable", "wechat agent client is unavailable")
	}

	request, err := s.resolver.ResolveAgentControlledSend(
		ctx,
		chatType,
		normalizedPeerID,
		normalizedText,
	)
	if err != nil {
		return automationDeliveryFailed("authorization_failed", err.Error())
	}
	if err := ValidateAgentControlledSendRequest(request); err != nil {
		return automationDeliveryFailed("authorization_failed", "automation authorization is invalid")
	}

	result, err := client.SendControlled(ctx, request)
	if err == nil {
		return result
	}
	if result.Status == AutomationDeliveryUnknown && result.DispatchAttempted {
		return result
	}
	var agentErr *AgentError
	if errors.As(err, &agentErr) {
		if agentErr.Code == AgentErrorCodeDeliveryAmbiguous {
			return automationDeliveryUnknown(agentErr.Code, agentErr.Message)
		}
		return automationDeliveryFailed(agentErr.Code, agentErr.Message)
	}
	if ctx.Err() != nil {
		return automationDeliveryFailed(automationContextErrorCode(ctx.Err()), ctx.Err().Error())
	}
	return automationDeliveryFailed("sender_unavailable", "wechat agent delivery is unavailable")
}

func automationDeliveryFailed(code, message string) AutomationDeliveryResult {
	return AutomationDeliveryResult{
		Status:       AutomationDeliveryFailed,
		ErrorCode:    code,
		ErrorMessage: message,
	}
}

func automationDeliveryUnknown(code, message string) AutomationDeliveryResult {
	return AutomationDeliveryResult{
		Status:            AutomationDeliveryUnknown,
		DispatchAttempted: true,
		ErrorCode:         code,
		ErrorMessage:      message,
	}
}

func automationContextErrorCode(err error) string {
	if err == context.DeadlineExceeded {
		return "deadline_exceeded"
	}
	return "context_canceled"
}

type automationRuntimeAgentSendResolver struct{}

func (automationRuntimeAgentSendResolver) ResolveAgentControlledSend(
	ctx context.Context,
	chatType ChatType,
	peerID string,
	text string,
) (AgentControlledSendRequest, error) {
	if ctx == nil {
		return AgentControlledSendRequest{}, errors.New("context is required")
	}
	if err := ctx.Err(); err != nil {
		return AgentControlledSendRequest{}, err
	}
	automationRuntimeMu.RLock()
	runtime := automationRuntime
	automationRuntimeMu.RUnlock()
	if runtime == nil || runtime.store == nil {
		return AgentControlledSendRequest{}, errors.New("automation store is unavailable")
	}

	state := runtime.store.Snapshot()
	var outbox *AutomationOutboxEntry
	for i := range state.Outbox {
		entry := &state.Outbox[i]
		if entry.Status != AutomationOutboxDispatching ||
			automationOutboxChatType(*entry) != chatType ||
			entry.PeerID != peerID ||
			entry.Text != text {
			continue
		}
		if outbox != nil {
			return AgentControlledSendRequest{}, errors.New("automation dispatch authorization is ambiguous")
		}
		outbox = entry
	}
	if outbox == nil {
		return AgentControlledSendRequest{}, errors.New("automation dispatch authorization was not found")
	}

	var action *AutomationAction
	for i := range state.Actions {
		if state.Actions[i].ID == outbox.ActionID {
			action = &state.Actions[i]
			break
		}
	}
	if action == nil || action.Recipient == nil ||
		action.Recipient.ChatType != chatType ||
		action.Recipient.PeerID != peerID ||
		action.Recipient.AudienceVersion == 0 {
		return AgentControlledSendRequest{}, errors.New("automation action recipient authorization is invalid")
	}
	workbenchSend := action.Tool == AutomationToolWorkbenchAllowOnce &&
		(action.Operation == WorkbenchOperationMessagesSendPrivate ||
			action.Operation == WorkbenchOperationMessagesSendGroup)
	if workbenchSend {
		expectedChatType := ChatTypePrivate
		if action.Operation == WorkbenchOperationMessagesSendGroup {
			expectedChatType = ChatTypeGroup
		}
		if action.Status != AutomationActionExecuting ||
			action.Recipient.ChatType != expectedChatType ||
			action.Recipient.AudienceID == "" {
			return AgentControlledSendRequest{}, errors.New("workbench send action authorization is invalid")
		}
		if runtime.service == nil || runtime.service.crm == nil {
			return AgentControlledSendRequest{}, errors.New("workbench send CRM authorization is unavailable")
		}
		event, ok := automationFindMessageEvent(runtime.service.crm.Snapshot(), action.Recipient.AudienceID)
		eventPeerID := event.PeerID
		if ok && expectedChatType == ChatTypeGroup && strings.TrimSpace(event.GroupID) != "" {
			eventPeerID = event.GroupID
		}
		if !ok || event.Version != action.Recipient.AudienceVersion ||
			event.ChatType != expectedChatType || eventPeerID != action.Recipient.PeerID {
			return AgentControlledSendRequest{}, errors.New("workbench send message event authorization is stale")
		}
	} else if action.Tool != AutomationToolReplyPrivateText &&
		action.Tool != AutomationToolReplyAudienceText {
		return AgentControlledSendRequest{}, errors.New("automation action is not a controlled text send")
	}

	var permission *AutomationPermissionRequest
	for i := range state.PermissionRequests {
		request := &state.PermissionRequests[i]
		if request.ActionID != action.ID {
			continue
		}
		if permission != nil {
			return AgentControlledSendRequest{}, errors.New("automation permission authorization is ambiguous")
		}
		permission = request
	}
	if permission == nil || permission.Status != AutomationPermissionStatusAllowed ||
		permission.Tool != action.Tool || permission.Operation != action.Operation {
		return AgentControlledSendRequest{}, errors.New("automation permission is not approved")
	}
	if workbenchSend {
		allowOnce := permission.Decision == AutomationPermissionAllowOnce &&
			permission.RuleID == ""
		persistent := permission.Decision == AutomationPermissionAllowPersistent &&
			persistentHighRiskSendEnabled() &&
			isPersistentHighRiskSendOperation(action.Operation) &&
			permission.RuleID != ""
		if (!allowOnce && !persistent) ||
			strings.TrimSpace(permission.OperatorID) == "" ||
			permission.OperatorID == automationPermissionOperatorAuto {
			return AgentControlledSendRequest{}, errors.New(
				"workbench allow_once or enabled persistent outbound authorization is required",
			)
		}
	}

	content := AgentControlledContent{
		Type: AgentControlledContentText,
		Text: text,
	}
	contentHash, err := AgentControlledContentSHA256(content)
	if err != nil {
		return AgentControlledSendRequest{}, err
	}
	request := AgentControlledSendRequest{
		IdempotencyKey: outbox.IdempotencyKey,
		Recipient: AgentRecipient{
			ChatType: chatType,
			PeerID:   peerID,
		},
		Content: content,
		Authorization: AgentSendAuthorization{
			ActionID:            action.ID,
			PermissionRequestID: permission.ID,
			ContentSHA256:       contentHash,
			AudienceID:          action.Recipient.AudienceID,
			AudienceVersion:     action.Recipient.AudienceVersion,
			ExpiresAt:           time.Now().UTC().Add(automationAgentAuthorizationTTL),
			FrequencyScope: AgentFrequencyScope{
				ID:            "permission-" + permission.ID,
				WindowSeconds: int64(automationAgentFrequencyWindow / time.Second),
				Limit:         1,
			},
		},
	}
	if err := ValidateAgentControlledSendRequest(request); err != nil {
		return AgentControlledSendRequest{}, errors.New("automation dispatch authorization is invalid")
	}
	return request, nil
}
