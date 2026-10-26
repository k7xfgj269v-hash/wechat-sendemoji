package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"time"
)

func (r *WechatAgentRuntime) SendControlled(
	ctx context.Context,
	request AgentControlledSendRequest,
) (AutomationDeliveryResult, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AutomationDeliveryResult{}, err
	}
	if err := validateAgentControlledSendRequestStructure(request); err != nil {
		return AutomationDeliveryResult{}, err
	}
	normalizedContent, err := normalizeAgentControlledContent(request.Content)
	if err != nil {
		return AutomationDeliveryResult{}, err
	}
	request.Content = normalizedContent
	fingerprint, err := fingerprintWechatAgentDelivery(request)
	if err != nil {
		return AutomationDeliveryResult{}, &AgentError{
			Code: AgentErrorCodeValidation, Message: "controlled delivery is invalid",
		}
	}

	pending, existing, err := r.lookupDelivery(request.IdempotencyKey, fingerprint)
	if err != nil {
		return AutomationDeliveryResult{}, err
	}
	if pending != nil || existing != nil {
		return awaitWechatAgentDeliveryReplay(ctx, pending, existing)
	}
	if err := validateAgentControlledSendFreshness(request, r.clock().UTC()); err != nil {
		return AutomationDeliveryResult{}, err
	}
	if err := r.authorizer.Authorize(ctx, request); err != nil {
		return AutomationDeliveryResult{}, normalizeWechatAgentAuthorizationError(err)
	}

	pending, existing, err = r.reserveDelivery(request, fingerprint)
	if err != nil {
		return AutomationDeliveryResult{}, err
	}
	if pending != nil || existing != nil {
		return awaitWechatAgentDeliveryReplay(ctx, pending, existing)
	}

	var attachmentData []byte
	if request.Content.Type == AgentControlledContentAttachment {
		attachmentData, err = r.attachments.ReadAll(ctx, request.Content.AttachmentID, maxWechatAgentSendAttachment)
		if err != nil {
			result := failedAgentDelivery("attachment_unavailable", "attachment is unavailable")
			_ = r.completeDelivery(request.IdempotencyKey, fingerprint, result)
			return result, nil
		}
	}

	result := deliverWechatAgentControlledSafely(r.delivery, ctx, request, attachmentData)
	if result.Status == AutomationDeliveryFailed && result.DispatchAttempted {
		result = unknownAgentDelivery(
			"delivery_ambiguous",
			"controlled delivery may have been dispatched",
		)
	}
	if !isValidAgentDeliveryResult(result) {
		result = unknownAgentDelivery("delivery_ambiguous", "controlled delivery result is ambiguous")
	}
	if err := r.completeDelivery(request.IdempotencyKey, fingerprint, result); err != nil {
		return unknownAgentDelivery(
			"delivery_ledger_error",
			"controlled delivery acknowledgement could not be persisted",
		), nil
	}
	return result, nil
}

func awaitWechatAgentDeliveryReplay(
	ctx context.Context,
	pending *wechatAgentPendingDelivery,
	existing *AutomationDeliveryResult,
) (AutomationDeliveryResult, error) {
	if existing != nil {
		return *existing, nil
	}
	select {
	case <-pending.done:
		return pending.result, nil
	case <-ctx.Done():
		return AutomationDeliveryResult{}, ctx.Err()
	}
}

func (r *WechatAgentRuntime) Close() error {
	var closeErr error
	r.closeOnce.Do(func() {
		if err := r.events.Close(); err != nil {
			closeErr = err
		}
		if err := r.attachments.Close(); err != nil && closeErr == nil {
			closeErr = err
		}
	})
	return closeErr
}

func (r *WechatAgentRuntime) lookupDelivery(
	idempotencyKey string,
	fingerprint string,
) (*wechatAgentPendingDelivery, *AutomationDeliveryResult, error) {
	r.deliveryMu.Lock()
	defer r.deliveryMu.Unlock()
	return r.lookupDeliveryLocked(idempotencyKey, fingerprint)
}

func (r *WechatAgentRuntime) lookupDeliveryLocked(
	idempotencyKey string,
	fingerprint string,
) (*wechatAgentPendingDelivery, *AutomationDeliveryResult, error) {
	if pending, exists := r.pendingDeliveries[idempotencyKey]; exists {
		if pending.fingerprint != fingerprint {
			return nil, nil, &AgentError{
				Code: AgentErrorCodeConflict, Message: "idempotency key is bound to another delivery",
			}
		}
		return pending, nil, nil
	}
	if record, exists := r.deliveries[idempotencyKey]; exists {
		if record.Fingerprint != fingerprint {
			return nil, nil, &AgentError{
				Code: AgentErrorCodeConflict, Message: "idempotency key is bound to another delivery",
			}
		}
		result := record.Result
		return nil, &result, nil
	}
	return nil, nil, nil
}

func (r *WechatAgentRuntime) reserveDelivery(
	request AgentControlledSendRequest,
	fingerprint string,
) (*wechatAgentPendingDelivery, *AutomationDeliveryResult, error) {
	r.deliveryMu.Lock()
	defer r.deliveryMu.Unlock()
	pending, existing, err := r.lookupDeliveryLocked(request.IdempotencyKey, fingerprint)
	if err != nil || pending != nil || existing != nil {
		return pending, existing, err
	}
	now := r.clock().UTC()
	scope := request.Authorization.FrequencyScope
	cutoff := now.Add(-time.Duration(scope.WindowSeconds) * time.Second)
	timestamps := r.frequency[scope.ID][:0]
	for _, timestamp := range r.frequency[scope.ID] {
		if timestamp.After(cutoff) {
			timestamps = append(timestamps, timestamp)
		}
	}
	if len(timestamps) >= scope.Limit {
		r.frequency[scope.ID] = timestamps
		return nil, nil, &AgentError{
			Code: AgentErrorCodeConflict, Message: "authorized frequency limit is exhausted",
		}
	}
	pending = &wechatAgentPendingDelivery{
		fingerprint: fingerprint,
		done:        make(chan struct{}),
	}
	r.pendingDeliveries[request.IdempotencyKey] = pending
	unknown := unknownAgentDelivery("delivery_ambiguous", "controlled delivery may have been dispatched")
	working := cloneWechatAgentDeliveries(r.deliveries)
	working[request.IdempotencyKey] = wechatAgentDeliveryRecord{
		Fingerprint:    fingerprint,
		FrequencyScope: scope,
		Result:         unknown,
		CreatedAt:      now,
	}
	if err := persistWechatAgentDeliveryLedger(r.deliveryLedgerPath, working); err != nil {
		delete(r.pendingDeliveries, request.IdempotencyKey)
		close(pending.done)
		return nil, nil, err
	}
	r.deliveries = working
	r.frequency[scope.ID] = append(timestamps, now)
	return nil, nil, nil
}

func (r *WechatAgentRuntime) completeDelivery(
	idempotencyKey string,
	fingerprint string,
	result AutomationDeliveryResult,
) error {
	r.deliveryMu.Lock()
	defer r.deliveryMu.Unlock()
	pending := r.pendingDeliveries[idempotencyKey]
	record, exists := r.deliveries[idempotencyKey]
	if !exists {
		record = wechatAgentDeliveryRecord{
			Fingerprint: fingerprint,
			CreatedAt:   r.clock().UTC(),
		}
	}
	working := cloneWechatAgentDeliveries(r.deliveries)
	retryableFailure := result.Status == AutomationDeliveryFailed && !result.DispatchAttempted
	if retryableFailure {
		delete(working, idempotencyKey)
	} else {
		record.Result = result
		working[idempotencyKey] = record
	}
	if err := persistWechatAgentDeliveryLedger(r.deliveryLedgerPath, working); err != nil {
		if pending != nil {
			pending.result = unknownAgentDelivery(
				"delivery_ledger_error",
				"controlled delivery acknowledgement could not be persisted",
			)
			close(pending.done)
			delete(r.pendingDeliveries, idempotencyKey)
		}
		return err
	}
	r.deliveries = working
	if retryableFailure {
		r.releaseFrequencyReservationLocked(record.FrequencyScope, record.CreatedAt)
	}
	if pending != nil {
		pending.result = result
		close(pending.done)
		delete(r.pendingDeliveries, idempotencyKey)
	}
	return nil
}

func (r *WechatAgentRuntime) releaseFrequencyReservationLocked(
	scope AgentFrequencyScope,
	reservedAt time.Time,
) {
	timestamps := r.frequency[scope.ID]
	for index, timestamp := range timestamps {
		if timestamp.Equal(reservedAt) {
			timestamps = append(timestamps[:index], timestamps[index+1:]...)
			break
		}
	}
	if len(timestamps) == 0 {
		delete(r.frequency, scope.ID)
		return
	}
	r.frequency[scope.ID] = timestamps
}

type queueWechatAgentDelivery struct {
	queue chan<- *SendMsg
}

func NewQueueWechatAgentDelivery(queue chan<- *SendMsg) WechatAgentControlledDelivery {
	return &queueWechatAgentDelivery{queue: queue}
}

func (d *queueWechatAgentDelivery) Deliver(
	ctx context.Context,
	request AgentControlledSendRequest,
	attachmentData []byte,
) (result AutomationDeliveryResult) {
	if d == nil || d.queue == nil {
		return failedAgentDelivery("sender_unavailable", "message delivery is unavailable")
	}
	message := &SendMsg{ResultChan: make(chan error, 1)}
	if request.Recipient.ChatType == ChatTypeGroup {
		message.GroupID = request.Recipient.PeerID
	} else {
		message.UserId = request.Recipient.PeerID
	}
	switch request.Content.Type {
	case AgentControlledContentText:
		message.Type = "text"
		message.Content = strings.TrimSpace(request.Content.Text)
	case AgentControlledContentAttachment:
		message.Type = "send_file_simple"
		message.Content = base64.StdEncoding.EncodeToString(attachmentData)
	case AgentControlledContentEmoji:
		// 表情分支：只把 md5 交给 worker 的 case "emoji"，由它构造 sendemoji 请求体。
		// Content 与 Md5 同填同一串，与 send_routes.go 的投递约定一致，避免读法漂移。
		// 绝不把 emoji 映射成 attachment/image —— 那会变成发文件/发图片。
		if !IsValidAgentEmojiMD5(request.Content.MD5) {
			return failedAgentDelivery("validation_error", "emoji md5 is invalid")
		}
		message.Type = emojiSendMsgType
		message.Md5 = request.Content.MD5
		message.Content = request.Content.MD5
	default:
		return failedAgentDelivery("validation_error", "controlled content type is invalid")
	}

	accepted := false
	defer func() {
		if recover() != nil {
			if accepted {
				result = unknownAgentDelivery("delivery_ambiguous", "message delivery became ambiguous")
			} else {
				result = failedAgentDelivery("queue_closed", "message delivery queue is closed")
			}
		}
	}()
	select {
	case <-ctx.Done():
		return failedAgentDelivery("context_canceled", "message delivery was canceled")
	case d.queue <- message:
		accepted = true
	}
	select {
	case err, ok := <-message.ResultChan:
		if !ok || err != nil {
			return unknownAgentDelivery("delivery_ambiguous", "message delivery acknowledgement is unavailable")
		}
		return AutomationDeliveryResult{
			Status: AutomationDeliverySent, DispatchAttempted: true,
		}
	case <-ctx.Done():
		return unknownAgentDelivery("delivery_ambiguous", "message delivery acknowledgement timed out")
	}
}

func fingerprintWechatAgentDelivery(request AgentControlledSendRequest) (string, error) {
	normalizedContent, err := normalizeAgentControlledContent(request.Content)
	if err != nil {
		return "", err
	}
	stable := struct {
		Recipient           AgentRecipient         `json:"recipient"`
		Content             AgentControlledContent `json:"content"`
		ActionID            string                 `json:"action_id"`
		PermissionRequestID string                 `json:"permission_request_id"`
		ContentSHA256       string                 `json:"content_sha256"`
		AudienceID          string                 `json:"audience_id"`
		AudienceVersion     uint64                 `json:"audience_version"`
		FrequencyScope      AgentFrequencyScope    `json:"frequency_scope"`
	}{
		Recipient:           request.Recipient,
		Content:             normalizedContent,
		ActionID:            request.Authorization.ActionID,
		PermissionRequestID: request.Authorization.PermissionRequestID,
		ContentSHA256:       request.Authorization.ContentSHA256,
		AudienceID:          request.Authorization.AudienceID,
		AudienceVersion:     request.Authorization.AudienceVersion,
		FrequencyScope:      request.Authorization.FrequencyScope,
	}
	data, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func loadWechatAgentDeliveryLedger(path string) (map[string]wechatAgentDeliveryRecord, error) {
	records := make(map[string]wechatAgentDeliveryRecord)
	if path == "" {
		return records, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := persistWechatAgentDeliveryLedger(path, records); err != nil {
			return nil, err
		}
		return records, nil
	}
	if err != nil {
		return nil, err
	}
	var state wechatAgentDeliveryLedgerState
	if err := decodeWechatAgentJSON(data, &state); err != nil ||
		state.Version != 1 || state.Records == nil {
		return nil, errors.New("wechat agent delivery ledger is invalid")
	}
	for key, record := range state.Records {
		if key == "" || record.Fingerprint == "" || record.CreatedAt.IsZero() ||
			record.FrequencyScope.ID == "" ||
			record.FrequencyScope.WindowSeconds <= 0 ||
			record.FrequencyScope.Limit <= 0 ||
			!isValidAgentDeliveryResult(record.Result) {
			return nil, errors.New("wechat agent delivery ledger is invalid")
		}
	}
	return state.Records, nil
}

func persistWechatAgentDeliveryLedger(
	path string,
	records map[string]wechatAgentDeliveryRecord,
) error {
	if path == "" {
		return nil
	}
	return writeWechatAgentJSONAtomic(path, wechatAgentDeliveryLedgerState{
		Version: 1,
		Records: records,
	})
}

func cloneWechatAgentDeliveries(
	records map[string]wechatAgentDeliveryRecord,
) map[string]wechatAgentDeliveryRecord {
	cloned := make(map[string]wechatAgentDeliveryRecord, len(records))
	for key, record := range records {
		cloned[key] = record
	}
	return cloned
}

func requireWechatAgentContext(ctx context.Context) error {
	if ctx == nil {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "context is required"}
	}
	return ctx.Err()
}

func normalizeWechatAgentAuthorizationError(err error) error {
	var agentError *AgentError
	if errors.As(err, &agentError) {
		return agentError
	}
	return &AgentError{Code: AgentErrorCodeValidation, Message: "controlled delivery is not authorized"}
}

func isValidAgentDeliveryResult(result AutomationDeliveryResult) bool {
	switch result.Status {
	case AutomationDeliverySent, AutomationDeliveryUnknown:
		return result.DispatchAttempted
	case AutomationDeliveryFailed:
		return true
	default:
		return false
	}
}

func failedAgentDelivery(code, message string) AutomationDeliveryResult {
	return AutomationDeliveryResult{
		Status: AutomationDeliveryFailed, ErrorCode: code, ErrorMessage: message,
	}
}

func unknownAgentDelivery(code, message string) AutomationDeliveryResult {
	return AutomationDeliveryResult{
		Status:            AutomationDeliveryUnknown,
		DispatchAttempted: true,
		ErrorCode:         code,
		ErrorMessage:      message,
	}
}

func deliverWechatAgentControlledSafely(
	delivery WechatAgentControlledDelivery,
	ctx context.Context,
	request AgentControlledSendRequest,
	attachmentData []byte,
) (result AutomationDeliveryResult) {
	defer func() {
		if recover() != nil {
			result = unknownAgentDelivery(
				"delivery_ambiguous",
				"controlled delivery adapter failed after dispatch reservation",
			)
		}
	}()
	return delivery.Deliver(ctx, request, attachmentData)
}
