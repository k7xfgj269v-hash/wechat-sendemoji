package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAgentFrameAndBodyRoundTrips(t *testing.T) {
	request, err := NewAgentRequest(
		"req-1",
		AgentOperationContactsSearch,
		AgentContactSearchRequest{Keyword: "alice", Limit: 20, Offset: 2},
	)
	if err != nil {
		t.Fatalf("NewAgentRequest() error = %v", err)
	}

	var wire bytes.Buffer
	if err := WriteAgentFrame(&wire, request); err != nil {
		t.Fatalf("WriteAgentFrame(request) error = %v", err)
	}
	if !bytes.HasSuffix(wire.Bytes(), []byte{'\n'}) {
		t.Fatal("request frame is not newline terminated")
	}
	decodedRequest, err := ReadAgentRequestFrame(bufio.NewReader(&wire))
	if err != nil {
		t.Fatalf("ReadAgentRequestFrame() error = %v", err)
	}
	if decodedRequest.Version != AgentProtocolVersion ||
		decodedRequest.ID != request.ID ||
		decodedRequest.Op != request.Op {
		t.Fatalf("decoded request = %#v, want version/id/op from %#v", decodedRequest, request)
	}
	var search AgentContactSearchRequest
	if err := DecodeAgentBody(decodedRequest.Body, &search); err != nil {
		t.Fatalf("DecodeAgentBody(request) error = %v", err)
	}
	if search.Keyword != "alice" || search.Limit != 20 || search.Offset != 2 {
		t.Fatalf("decoded request body = %#v", search)
	}

	response := AgentResponse{
		Version: AgentProtocolVersion,
		ID:      request.ID,
		OK:      true,
		Body: mustAgentJSON(t, AgentContactSearchResult{
			Total: 1,
			Contacts: []Contact{{
				Username: "wxid_alice",
				NickName: "Alice",
			}},
			Limit:  20,
			Offset: 2,
		}),
	}
	wire.Reset()
	if err := WriteAgentFrame(&wire, response); err != nil {
		t.Fatalf("WriteAgentFrame(response) error = %v", err)
	}
	decodedResponse, err := ReadAgentResponseFrame(bufio.NewReader(&wire))
	if err != nil {
		t.Fatalf("ReadAgentResponseFrame() error = %v", err)
	}
	var result AgentContactSearchResult
	if err := DecodeAgentBody(decodedResponse.Body, &result); err != nil {
		t.Fatalf("DecodeAgentBody(response) error = %v", err)
	}
	if result.Total != 1 || len(result.Contacts) != 1 || result.Contacts[0].Username != "wxid_alice" {
		t.Fatalf("decoded response body = %#v", result)
	}

	event := AgentEventFrame{
		Version:        AgentProtocolVersion,
		SubscriptionID: "sub-1",
		Cursor:         "42",
		Event: AgentInboundEvent{
			MessageKey: "message-42",
			ChatType:   ChatTypePrivate,
			PeerID:     "wxid_alice",
			AuthorID:   "wxid_alice",
			Text:       "hello",
		},
	}
	wire.Reset()
	if err := WriteAgentFrame(&wire, event); err != nil {
		t.Fatalf("WriteAgentFrame(event) error = %v", err)
	}
	decodedEvent, err := ReadAgentEventFrame(bufio.NewReader(&wire))
	if err != nil {
		t.Fatalf("ReadAgentEventFrame() error = %v", err)
	}
	if decodedEvent.SubscriptionID != event.SubscriptionID ||
		decodedEvent.Cursor != event.Cursor ||
		decodedEvent.Event.MessageKey != event.Event.MessageKey {
		t.Fatalf("decoded event = %#v, want %#v", decodedEvent, event)
	}
}

func TestAgentFrameSizeBoundary(t *testing.T) {
	exact := json.RawMessage(`{"value":"` + strings.Repeat("x", AgentMaxFrameBytes-len(`{"value":""}`)) + `"}`)
	if len(exact) != AgentMaxFrameBytes {
		t.Fatalf("exact frame size = %d, want %d", len(exact), AgentMaxFrameBytes)
	}

	var wire bytes.Buffer
	if err := WriteAgentFrame(&wire, exact); err != nil {
		t.Fatalf("WriteAgentFrame(exact limit) error = %v", err)
	}
	if wire.Len() != AgentMaxFrameBytes+1 {
		t.Fatalf("written frame size = %d, want %d including newline", wire.Len(), AgentMaxFrameBytes+1)
	}
	read, err := readAgentFrame(bufio.NewReaderSize(bytes.NewReader(wire.Bytes()), 4096))
	if err != nil {
		t.Fatalf("readAgentFrame(exact limit) error = %v", err)
	}
	if !bytes.Equal(read, exact) {
		t.Fatal("readAgentFrame(exact limit) changed frame bytes")
	}

	over := json.RawMessage(`{"value":"` + strings.Repeat("x", AgentMaxFrameBytes-len(`{"value":""}`)+1) + `"}`)
	if len(over) != AgentMaxFrameBytes+1 {
		t.Fatalf("over-limit frame size = %d, want %d", len(over), AgentMaxFrameBytes+1)
	}
	wire.Reset()
	assertAgentErrorCode(t, WriteAgentFrame(&wire, over), AgentErrorCodeFrameTooLarge)

	overWire := append(append([]byte(nil), over...), '\n')
	_, err = readAgentFrame(bufio.NewReaderSize(bytes.NewReader(overWire), 4096))
	assertAgentErrorCode(t, err, AgentErrorCodeFrameTooLarge)
}

func TestAgentStrictJSONRejection(t *testing.T) {
	requestCases := map[string][]byte{
		"unknown frame field":   []byte(`{"version":"wechat-agent.v2","id":"req-1","op":"health.get","body":{},"extra":true}`),
		"duplicate frame field": []byte(`{"version":"wechat-agent.v2","id":"req-1","id":"req-2","op":"health.get","body":{}}`),
		"trailing object":       []byte(`{"version":"wechat-agent.v2","id":"req-1","op":"health.get","body":{}} {}`),
		"non-object frame":      []byte(`[]`),
		"unknown body field":    []byte(`{"version":"wechat-agent.v2","id":"req-1","op":"health.get","body":{"extra":true}}`),
		"duplicate body field":  []byte(`{"version":"wechat-agent.v2","id":"req-1","op":"contacts.search","body":{"keyword":"a","keyword":"b"}}`),
	}
	for name, frame := range requestCases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeAgentRequestFrame(frame)
			assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
		})
	}

	request := AgentRequest{
		Version: AgentProtocolVersion,
		ID:      "req-1",
		Op:      AgentOperationHealthGet,
		Body:    json.RawMessage(`{} {}`),
	}
	assertAgentErrorCode(t, ValidateAgentRequest(request), AgentErrorCodeInvalidFrame)

	responseCases := map[string][]byte{
		"unknown response field":   []byte(`{"version":"wechat-agent.v2","id":"req-1","ok":true,"body":{},"extra":true}`),
		"duplicate response field": []byte(`{"version":"wechat-agent.v2","id":"req-1","ok":true,"ok":false,"body":{}}`),
		"trailing response object": []byte(`{"version":"wechat-agent.v2","id":"req-1","ok":true,"body":{}} {}`),
	}
	for name, frame := range responseCases {
		t.Run(name, func(t *testing.T) {
			_, err := DecodeAgentResponseFrame(frame)
			assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
		})
	}

	for name, body := range map[string]json.RawMessage{
		"unknown body field":   json.RawMessage(`{"status":"ready","extra":true}`),
		"duplicate body field": json.RawMessage(`{"status":"ready","status":"other"}`),
		"trailing body object": json.RawMessage(`{"status":"ready"} {}`),
	} {
		t.Run(name, func(t *testing.T) {
			var health AgentHealth
			assertAgentErrorCode(t, DecodeAgentBody(body, &health), AgentErrorCodeInvalidFrame)
		})
	}
}

func TestDecodeAgentBodyRejectsJSONNull(t *testing.T) {
	for name, destination := range map[string]any{
		"struct": new(AgentHealth),
		"slice":  new([]Group),
	} {
		t.Run(name, func(t *testing.T) {
			assertAgentErrorCode(
				t,
				DecodeAgentBody(json.RawMessage(`null`), destination),
				AgentErrorCodeInvalidFrame,
			)
		})
	}
}

func TestAgentClosedVersionOperationAndErrorCodes(t *testing.T) {
	if AgentProtocolVersion != "wechat-agent.v2" {
		t.Fatalf("AgentProtocolVersion = %q, want wechat-agent.v2", AgentProtocolVersion)
	}

	operations := []AgentOperation{
		AgentOperationHealthGet,
		AgentOperationContactsSearch,
		AgentOperationContactsGet,
		AgentOperationContactsRefresh,
		AgentOperationGroupsList,
		AgentOperationGroupsMembers,
		AgentOperationFridaStatus,
		AgentOperationFridaInitialize,
		AgentOperationFridaRescan,
		AgentOperationFridaDetach,
		AgentOperationMessageSendControlled,
		AgentOperationAttachmentDescribe,
		AgentOperationAttachmentReadChunk,
		AgentOperationEventsSubscribe,
	}
	for _, operation := range operations {
		if !IsValidAgentOperation(operation) {
			t.Errorf("IsValidAgentOperation(%q) = false", operation)
		}
	}
	if IsValidAgentOperation("shell.exec") || IsValidAgentOperation("") {
		t.Fatal("operation allowlist accepted an unsupported operation")
	}

	errorCodes := []string{
		AgentErrorCodeInvalidFrame,
		AgentErrorCodeFrameTooLarge,
		AgentErrorCodeUnsupportedVersion,
		AgentErrorCodeUnsupportedOperation,
		AgentErrorCodeUnauthorizedPeer,
		AgentErrorCodeValidation,
		AgentErrorCodeNotFound,
		AgentErrorCodeConflict,
		AgentErrorCodeUnavailable,
		AgentErrorCodeCursorExpired,
		AgentErrorCodeDeadlineExceeded,
		AgentErrorCodeDeliveryFailed,
		AgentErrorCodeDeliveryAmbiguous,
		AgentErrorCodeInternal,
	}
	for _, code := range errorCodes {
		if !IsValidAgentErrorCode(code) {
			t.Errorf("IsValidAgentErrorCode(%q) = false", code)
		}
	}
	if IsValidAgentErrorCode("arbitrary_error") || IsValidAgentErrorCode("") {
		t.Fatal("error-code allowlist accepted an unsupported code")
	}

	_, err := DecodeAgentRequestFrame([]byte(`{"version":"wechat-agent.v1","id":"req-1","op":"health.get","body":{}}`))
	assertAgentErrorCode(t, err, AgentErrorCodeUnsupportedVersion)
	_, err = DecodeAgentRequestFrame([]byte(`{"version":"wechat-agent.v2","id":"req-1","op":"shell.exec","body":{}}`))
	assertAgentErrorCode(t, err, AgentErrorCodeUnsupportedOperation)
	_, err = DecodeAgentResponseFrame([]byte(`{"version":"wechat-agent.v2","id":"req-1","ok":false,"error":{"code":"arbitrary_error","message":"no","retryable":false}}`))
	assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
}

func TestAgentControlledSendValidation(t *testing.T) {
	valid := validAgentControlledSendRequest(t)
	if err := ValidateAgentControlledSendRequest(valid); err != nil {
		t.Fatalf("ValidateAgentControlledSendRequest(valid) error = %v", err)
	}

	canonicalOutbox := valid
	canonicalOutbox.IdempotencyKey = "send:" + canonicalOutbox.Authorization.ActionID
	if err := ValidateAgentControlledSendRequest(canonicalOutbox); err != nil {
		t.Fatalf("ValidateAgentControlledSendRequest(canonical outbox) error = %v", err)
	}

	group := valid
	group.Recipient = AgentRecipient{ChatType: ChatTypeGroup, PeerID: "group-1@chatroom"}
	if err := ValidateAgentControlledSendRequest(group); err != nil {
		t.Fatalf("ValidateAgentControlledSendRequest(valid group) error = %v", err)
	}

	normalizedHash, err := AgentControlledContentSHA256(AgentControlledContent{
		Type: AgentControlledContentText,
		Text: "  hello  ",
	})
	if err != nil {
		t.Fatalf("AgentControlledContentSHA256(normalized text) error = %v", err)
	}
	if normalizedHash != valid.Authorization.ContentSHA256 {
		t.Fatal("content hash is not bound to normalized text")
	}

	mutations := map[string]func(*AgentControlledSendRequest){
		"empty idempotency key": func(request *AgentControlledSendRequest) {
			request.IdempotencyKey = ""
		},
		"idempotency key URL": func(request *AgentControlledSendRequest) {
			request.IdempotencyKey = "https://example.invalid/key"
		},
		"idempotency key unknown namespace": func(request *AgentControlledSendRequest) {
			request.IdempotencyKey = "http:key"
		},
		"idempotency key nested URL": func(request *AgentControlledSendRequest) {
			request.IdempotencyKey = "send:https://example.invalid/key"
		},
		"idempotency key empty namespace value": func(request *AgentControlledSendRequest) {
			request.IdempotencyKey = "send:"
		},
		"canonical action mismatch": func(request *AgentControlledSendRequest) {
			request.IdempotencyKey = "send:another-action"
		},
		"unknown recipient type": func(request *AgentControlledSendRequest) {
			request.Recipient.ChatType = "broadcast"
		},
		"private recipient group id": func(request *AgentControlledSendRequest) {
			request.Recipient.PeerID = "group@chatroom"
		},
		"group recipient private id": func(request *AgentControlledSendRequest) {
			request.Recipient.ChatType = ChatTypeGroup
		},
		"recipient path": func(request *AgentControlledSendRequest) {
			request.Recipient.PeerID = "../wxid-peer"
		},
		"action path": func(request *AgentControlledSendRequest) {
			request.Authorization.ActionID = "/tmp/action"
		},
		"permission URL": func(request *AgentControlledSendRequest) {
			request.Authorization.PermissionRequestID = "https://example.invalid/permission"
		},
		"content hash mismatch": func(request *AgentControlledSendRequest) {
			request.Authorization.ContentSHA256 = strings.Repeat("0", 64)
		},
		"audience URL": func(request *AgentControlledSendRequest) {
			request.Authorization.AudienceID = "https://example.invalid/audience"
		},
		"zero audience version": func(request *AgentControlledSendRequest) {
			request.Authorization.AudienceVersion = 0
		},
		"zero expiry": func(request *AgentControlledSendRequest) {
			request.Authorization.ExpiresAt = time.Time{}
		},
		"expired authorization": func(request *AgentControlledSendRequest) {
			request.Authorization.ExpiresAt = time.Now().Add(-time.Second)
		},
		"frequency path": func(request *AgentControlledSendRequest) {
			request.Authorization.FrequencyScope.ID = "../frequency"
		},
		"zero frequency window": func(request *AgentControlledSendRequest) {
			request.Authorization.FrequencyScope.WindowSeconds = 0
		},
		"zero frequency limit": func(request *AgentControlledSendRequest) {
			request.Authorization.FrequencyScope.Limit = 0
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			assertAgentErrorCode(t, ValidateAgentControlledSendRequest(request), AgentErrorCodeValidation)
		})
	}
}

func TestAgentControlledSendFrameAllowsExpiredAuthorizationForReplay(t *testing.T) {
	request := validAgentControlledSendRequest(t)
	request.IdempotencyKey = "send:" + request.Authorization.ActionID
	request.Authorization.ExpiresAt = time.Now().Add(-time.Hour)

	if _, err := NewAgentRequest(
		"req-expired-replay",
		AgentOperationMessageSendControlled,
		request,
	); err != nil {
		t.Fatalf("NewAgentRequest(expired controlled send) error = %v", err)
	}
	assertAgentErrorCode(
		t,
		ValidateAgentControlledSendRequest(request),
		AgentErrorCodeValidation,
	)
}

func TestAgentControlledContentBoundaries(t *testing.T) {
	valid := validAgentControlledSendRequest(t)
	valid.Content.Text = strings.Repeat("界", AgentMaxControlledTextRunes)
	valid.Authorization.ContentSHA256 = mustAgentContentHash(t, valid.Content)
	if err := ValidateAgentControlledSendRequest(valid); err != nil {
		t.Fatalf("maximum text validation error = %v", err)
	}

	contentCases := map[string]AgentControlledContent{
		"one rune over": {
			Type: AgentControlledContentText,
			Text: strings.Repeat("界", AgentMaxControlledTextRunes+1),
		},
		"empty text": {
			Type: AgentControlledContentText,
		},
		"text with NUL": {
			Type: AgentControlledContentText,
			Text: "hello\x00world",
		},
		"text with attachment": {
			Type:         AgentControlledContentText,
			Text:         "hello",
			AttachmentID: "attachment-1",
		},
		"attachment with text": {
			Type:         AgentControlledContentAttachment,
			Text:         "hello",
			AttachmentID: "attachment-1",
		},
		"attachment path": {
			Type:         AgentControlledContentAttachment,
			AttachmentID: "/tmp/file",
		},
		"attachment URL": {
			Type:         AgentControlledContentAttachment,
			AttachmentID: "https://example.invalid/file",
		},
		"unknown content type": {
			Type: "raw",
			Text: "hello",
		},
	}
	for name, content := range contentCases {
		t.Run(name, func(t *testing.T) {
			_, err := AgentControlledContentSHA256(content)
			assertAgentErrorCode(t, err, AgentErrorCodeValidation)
		})
	}
}

func TestAgentAttachmentAndCursorValidation(t *testing.T) {
	validBodies := []struct {
		operation AgentOperation
		body      any
	}{
		{AgentOperationAttachmentDescribe, AgentAttachmentDescribeRequest{AttachmentID: "attachment-1"}},
		{AgentOperationAttachmentReadChunk, AgentAttachmentChunkRequest{
			AttachmentID: "attachment-1",
			Offset:       0,
			Length:       AgentMaxAttachmentChunkBytes,
		}},
		{AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: ""}},
		{AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: "0"}},
		{AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: strings.Repeat("9", 128)}},
	}
	for _, test := range validBodies {
		if _, err := NewAgentRequest("req-1", test.operation, test.body); err != nil {
			t.Errorf("NewAgentRequest(%q, %#v) error = %v", test.operation, test.body, err)
		}
	}

	invalidBodies := []struct {
		name      string
		operation AgentOperation
		body      any
	}{
		{"empty attachment id", AgentOperationAttachmentDescribe, AgentAttachmentDescribeRequest{}},
		{"attachment path", AgentOperationAttachmentDescribe, AgentAttachmentDescribeRequest{AttachmentID: "../file"}},
		{"attachment URL", AgentOperationAttachmentDescribe, AgentAttachmentDescribeRequest{AttachmentID: "https://example.invalid/file"}},
		{"negative offset", AgentOperationAttachmentReadChunk, AgentAttachmentChunkRequest{AttachmentID: "attachment-1", Offset: -1, Length: 1}},
		{"zero length", AgentOperationAttachmentReadChunk, AgentAttachmentChunkRequest{AttachmentID: "attachment-1", Length: 0}},
		{"one byte over chunk", AgentOperationAttachmentReadChunk, AgentAttachmentChunkRequest{AttachmentID: "attachment-1", Length: AgentMaxAttachmentChunkBytes + 1}},
		{"leading zero cursor", AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: "01"}},
		{"negative cursor", AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: "-1"}},
		{"non-decimal cursor", AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: "1a"}},
		{"cursor too long", AgentOperationEventsSubscribe, AgentEventsSubscribeRequest{AfterCursor: strings.Repeat("9", 129)}},
	}
	for _, test := range invalidBodies {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewAgentRequest("req-1", test.operation, test.body)
			if err == nil {
				t.Fatal("NewAgentRequest() accepted invalid body")
			}
		})
	}

	if CompareAgentCursors("9", "10") >= 0 ||
		CompareAgentCursors("100", "99") <= 0 ||
		CompareAgentCursors("42", "42") != 0 {
		t.Fatal("CompareAgentCursors() does not preserve decimal ordering")
	}
}

func TestAgentInboundEventAttachmentValidation(t *testing.T) {
	createdAt := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	zeroTime := time.Time{}
	beforeCreated := createdAt.Add(-time.Second)
	tests := []struct {
		name       string
		attachment AgentAttachment
	}{
		{"invalid id", AgentAttachment{ID: "../attachment-1", Size: 3}},
		{"invalid sha256", AgentAttachment{ID: "attachment-1", Size: 3, SHA256: "abc"}},
		{"negative size", AgentAttachment{ID: "attachment-1", Size: -1}},
		{"zero expiry", AgentAttachment{ID: "attachment-1", Size: 3, ExpiresAt: &zeroTime}},
		{"expiry before creation", AgentAttachment{
			ID: "attachment-1", Size: 3, CreatedAt: createdAt, ExpiresAt: &beforeCreated,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			frame := AgentEventFrame{
				Version:        AgentProtocolVersion,
				SubscriptionID: "sub-1",
				Cursor:         "1",
				Event: AgentInboundEvent{
					MessageKey:  "message-1",
					Attachments: []AgentAttachment{test.attachment},
				},
			}
			assertAgentErrorCode(t, ValidateAgentEventFrame(frame), AgentErrorCodeInvalidFrame)
		})
	}
}

func TestAgentOpaqueIDValidation(t *testing.T) {
	for _, value := range []string{
		"wxid_alice",
		"group-1@chatroom",
		"gh_service_account",
	} {
		t.Run("accept "+value, func(t *testing.T) {
			if err := validateAgentOpaqueID("test id", value, 256); err != nil {
				t.Fatalf("validateAgentOpaqueID(%q) error = %v", value, err)
			}
		})
	}

	for _, value := range []string{
		"mailto:alice@example.com",
		"data:text/plain,hello",
		"urn:uuid:1234",
		"//host",
		".",
		"..",
	} {
		t.Run("reject "+value, func(t *testing.T) {
			assertAgentErrorCode(
				t,
				validateAgentOpaqueID("test id", value, 256),
				AgentErrorCodeValidation,
			)
		})
	}
}

func TestAgentSocketPathAndFileValidation(t *testing.T) {
	exactPath := "/" + strings.Repeat("a", agentMaxUnixSocketPathBytes-1)
	if err := ValidateAgentSocketPath(exactPath); err != nil {
		t.Fatalf("ValidateAgentSocketPath(exact limit) error = %v", err)
	}
	for name, path := range map[string]string{
		"empty":         "",
		"relative":      "agent.sock",
		"unclean":       "/tmp/../tmp/agent.sock",
		"NUL":           "/tmp/agent\x00.sock",
		"one byte over": "/" + strings.Repeat("a", agentMaxUnixSocketPathBytes),
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(ValidateAgentSocketPath(path), ErrUnsafeAgentSocketPath) {
				t.Fatalf("ValidateAgentSocketPath(%q) did not reject unsafe path", path)
			}
		})
	}

	parent, err := os.MkdirTemp("/tmp", "agent-socket-")
	if err != nil {
		t.Fatalf("os.MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatalf("chmod parent: %v", err)
	}
	socketPath := filepath.Join(parent, "agent.sock")
	uid := uint32(os.Getuid())
	if err := ValidateAgentSocketParent(socketPath, uid); err != nil {
		t.Fatalf("ValidateAgentSocketParent(valid) error = %v", err)
	}
	if !errors.Is(ValidateAgentSocketParent(socketPath, uid+1), ErrUnsafeAgentSocketPath) {
		t.Fatal("ValidateAgentSocketParent() accepted a different owner")
	}
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatalf("chmod unsafe parent: %v", err)
	}
	if !errors.Is(ValidateAgentSocketParent(socketPath, uid), ErrUnsafeAgentSocketPath) {
		t.Fatal("ValidateAgentSocketParent() accepted mode 0755")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatalf("restore parent mode: %v", err)
	}

	symlinkTarget, err := os.MkdirTemp("/tmp", "agent-socket-target-")
	if err != nil {
		t.Fatalf("os.MkdirTemp(symlink target) error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(symlinkTarget) })
	if err := os.Chmod(symlinkTarget, 0700); err != nil {
		t.Fatalf("chmod symlink target: %v", err)
	}
	symlinkParent := filepath.Join(parent, "linked")
	if err := os.Symlink(symlinkTarget, symlinkParent); err != nil {
		t.Fatalf("create parent symlink: %v", err)
	}
	if !errors.Is(
		ValidateAgentSocketParent(filepath.Join(symlinkParent, "agent.sock"), uid),
		ErrUnsafeAgentSocketPath,
	) {
		t.Fatal("ValidateAgentSocketParent() accepted a symlink parent")
	}

	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatalf("net.ListenUnix() error = %v", err)
	}
	defer listener.Close()
	if err := os.Chmod(socketPath, 0600); err != nil {
		t.Fatalf("chmod socket: %v", err)
	}
	if err := ValidateAgentSocketFile(socketPath, uid); err != nil {
		t.Fatalf("ValidateAgentSocketFile(valid) error = %v", err)
	}
	if !errors.Is(ValidateAgentSocketFile(socketPath, uid+1), ErrUnsafeAgentSocketPath) {
		t.Fatal("ValidateAgentSocketFile() accepted a different owner")
	}
	if err := os.Chmod(socketPath, 0660); err != nil {
		t.Fatalf("chmod unsafe socket: %v", err)
	}
	if !errors.Is(ValidateAgentSocketFile(socketPath, uid), ErrUnsafeAgentSocketPath) {
		t.Fatal("ValidateAgentSocketFile() accepted mode 0660")
	}
}

func TestAgentDarwinPeerUIDValidation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Darwin LOCAL_PEERCRED contract")
	}

	parent, err := os.MkdirTemp("/tmp", "agent-peercred-")
	if err != nil {
		t.Fatalf("os.MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	socketPath := filepath.Join(parent, "agent.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatalf("net.ListenUnix() error = %v", err)
	}
	defer listener.Close()

	accepted := make(chan *net.UnixConn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatalf("net.DialUnix() error = %v", err)
	}
	defer client.Close()

	var server *net.UnixConn
	select {
	case server = <-accepted:
		defer server.Close()
	case err := <-acceptErr:
		t.Fatalf("AcceptUnix() error = %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out accepting Unix peer")
	}

	credentials, err := ReadAgentPeerCredentials(server)
	if err != nil {
		t.Fatalf("ReadAgentPeerCredentials() error = %v", err)
	}
	if !credentials.Available || credentials.UID != uint32(os.Getuid()) {
		t.Fatalf("peer credentials = %#v, want current uid", credentials)
	}
	if err := ValidateAgentPeerUID(server, uint32(os.Getuid())); err != nil {
		t.Fatalf("ValidateAgentPeerUID(current uid) error = %v", err)
	}
	assertAgentErrorCode(t, ValidateAgentPeerUID(server, uint32(os.Getuid()+1)), AgentErrorCodeUnauthorizedPeer)
}

func validAgentControlledSendRequest(t *testing.T) AgentControlledSendRequest {
	t.Helper()
	content := AgentControlledContent{Type: AgentControlledContentText, Text: "hello"}
	return AgentControlledSendRequest{
		IdempotencyKey: "idempotency-1",
		Recipient: AgentRecipient{
			ChatType: ChatTypePrivate,
			PeerID:   "wxid-alice",
		},
		Content: content,
		Authorization: AgentSendAuthorization{
			ActionID:            "action-1",
			PermissionRequestID: "permission-1",
			ContentSHA256:       mustAgentContentHash(t, content),
			AudienceID:          "audience-1",
			AudienceVersion:     1,
			ExpiresAt:           time.Now().Add(time.Hour),
			FrequencyScope: AgentFrequencyScope{
				ID:            "frequency-1",
				WindowSeconds: 3600,
				Limit:         1,
			},
		},
	}
}

func mustAgentContentHash(t *testing.T, content AgentControlledContent) string {
	t.Helper()
	hash, err := AgentControlledContentSHA256(content)
	if err != nil {
		t.Fatalf("AgentControlledContentSHA256() error = %v", err)
	}
	return hash
}

func mustAgentJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return data
}

func assertAgentErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error = %v, want *AgentError with code %q", err, code)
	}
	if agentErr.Code != code {
		t.Fatalf("AgentError.Code = %q, want %q (error %v)", agentErr.Code, code, err)
	}
}
