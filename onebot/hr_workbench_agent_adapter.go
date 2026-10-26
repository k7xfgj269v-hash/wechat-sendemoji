package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	manualAgentAuthorizationTTL   = 2 * time.Minute
	manualAgentFrequencyWindow    = 5 * time.Minute
	operationalAgentHealthTimeout = time.Second
)

var (
	hrWorkbenchAgentClientMu sync.RWMutex
	hrWorkbenchAgentClient   AgentClient
	manualAgentSequence      atomic.Uint64
)

func installHRWorkbenchAgentClient(client AgentClient) func() {
	hrWorkbenchAgentClientMu.Lock()
	previous := hrWorkbenchAgentClient
	hrWorkbenchAgentClient = client
	hrWorkbenchAgentClientMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			hrWorkbenchAgentClientMu.Lock()
			if hrWorkbenchAgentClient == client {
				hrWorkbenchAgentClient = previous
			}
			hrWorkbenchAgentClientMu.Unlock()
		})
	}
}

func currentHRWorkbenchAgentClient() AgentClient {
	hrWorkbenchAgentClientMu.RLock()
	client := hrWorkbenchAgentClient
	hrWorkbenchAgentClientMu.RUnlock()
	return client
}

func observeHRWorkbenchAgentConnectivity(ctx context.Context, metrics OperationalMetrics) {
	if metrics == nil {
		return
	}
	client := currentHRWorkbenchAgentClient()
	if client == nil {
		metrics.SetAgentConnected(false)
		return
	}
	healthCtx, cancel := context.WithTimeout(ctx, operationalAgentHealthTimeout)
	_, err := client.Health(healthCtx)
	cancel()
	metrics.SetAgentConnected(err == nil)
}

func agentHTTPStatus(err error) int {
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		return http.StatusServiceUnavailable
	}
	switch agentErr.Code {
	case AgentErrorCodeValidation, AgentErrorCodeInvalidFrame,
		AgentErrorCodeUnsupportedVersion, AgentErrorCodeUnsupportedOperation:
		return http.StatusBadRequest
	case AgentErrorCodeUnauthorizedPeer:
		return http.StatusUnauthorized
	case AgentErrorCodeNotFound:
		return http.StatusNotFound
	case AgentErrorCodeConflict, AgentErrorCodeCursorExpired:
		return http.StatusConflict
	case AgentErrorCodeFrameTooLarge:
		return http.StatusRequestEntityTooLarge
	case AgentErrorCodeUnavailable, AgentErrorCodeDeadlineExceeded:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func publicAgentError(err error, fallback string) string {
	var agentErr *AgentError
	if errors.As(err, &agentErr) && strings.TrimSpace(agentErr.Message) != "" {
		return agentErr.Message
	}
	return fallback
}

func newManualAgentControlledSendRequest(
	scope string,
	recipient AgentRecipient,
	content AgentControlledContent,
	now time.Time,
) (AgentControlledSendRequest, error) {
	switch scope {
	case "http", "ws":
	default:
		return AgentControlledSendRequest{}, errors.New("manual delivery scope is invalid")
	}
	contentHash, err := AgentControlledContentSHA256(content)
	if err != nil {
		return AgentControlledSendRequest{}, err
	}
	sequence := manualAgentSequence.Add(1)
	seed := strings.Join([]string{
		scope,
		string(recipient.ChatType),
		recipient.PeerID,
		contentHash,
		time.Now().UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	token := hex.EncodeToString(sum[:12])
	prefix := "manual-" + scope + "-"
	actionID := prefix + "action-" + token
	permissionID := prefix + "permission-" + token
	idempotencyKey := prefix + "send-" + token + "-" + uint64Decimal(sequence)

	request := AgentControlledSendRequest{
		IdempotencyKey: idempotencyKey,
		Recipient:      recipient,
		Content:        content,
		Authorization: AgentSendAuthorization{
			ActionID:            actionID,
			PermissionRequestID: permissionID,
			ContentSHA256:       contentHash,
			AudienceID:          prefix + "recipient-" + token,
			AudienceVersion:     1,
			ExpiresAt:           now.UTC().Add(manualAgentAuthorizationTTL),
			FrequencyScope: AgentFrequencyScope{
				ID:            permissionID,
				WindowSeconds: int64(manualAgentFrequencyWindow / time.Second),
				Limit:         1,
			},
		},
	}
	if err := ValidateAgentControlledSendRequest(request); err != nil {
		return AgentControlledSendRequest{}, err
	}
	return request, nil
}

func uint64Decimal(value uint64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}
