package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	AgentProtocolVersion         = "wechat-agent.v2"
	AgentMaxFrameBytes           = 1 << 20
	AgentMaxAttachmentChunkBytes = 64 << 10
	AgentMaxControlledTextRunes  = 4000
	agentMaxJSONDepth            = 64
	agentMaxUnixSocketPathBytes  = 103
)

var (
	ErrAgentInvalidFrame     = errors.New("invalid agent frame")
	ErrAgentFrameTooLarge    = errors.New("agent frame too large")
	ErrUnsafeAgentSocketPath = errors.New("unsafe agent socket path")
)

type AgentOperation string

const (
	AgentOperationHealthGet             AgentOperation = "health.get"
	AgentOperationContactsSearch        AgentOperation = "contacts.search"
	AgentOperationContactsGet           AgentOperation = "contacts.get"
	AgentOperationContactsRefresh       AgentOperation = "contacts.refresh"
	AgentOperationGroupsList            AgentOperation = "groups.list"
	AgentOperationGroupsMembers         AgentOperation = "groups.members"
	AgentOperationFridaStatus           AgentOperation = "frida.status"
	AgentOperationFridaInitialize       AgentOperation = "frida.initialize"
	AgentOperationFridaRescan           AgentOperation = "frida.rescan"
	AgentOperationFridaDetach           AgentOperation = "frida.detach"
	AgentOperationMessageSendControlled AgentOperation = "message.send_controlled"
	AgentOperationAttachmentDescribe    AgentOperation = "attachment.describe"
	AgentOperationAttachmentReadChunk   AgentOperation = "attachment.read_chunk"
	AgentOperationEventsSubscribe       AgentOperation = "events.subscribe"
)

const (
	AgentErrorCodeInvalidFrame         = "invalid_frame"
	AgentErrorCodeFrameTooLarge        = "frame_too_large"
	AgentErrorCodeUnsupportedVersion   = "unsupported_version"
	AgentErrorCodeUnsupportedOperation = "unsupported_operation"
	AgentErrorCodeUnauthorizedPeer     = "unauthorized_peer"
	AgentErrorCodeValidation           = "validation_error"
	AgentErrorCodeNotFound             = "not_found"
	AgentErrorCodeConflict             = "conflict"
	AgentErrorCodeUnavailable          = "unavailable"
	AgentErrorCodeCursorExpired        = "cursor_expired"
	AgentErrorCodeDeadlineExceeded     = "deadline_exceeded"
	AgentErrorCodeDeliveryFailed       = "delivery_failed"
	AgentErrorCodeDeliveryAmbiguous    = "delivery_ambiguous"
	AgentErrorCodeInternal             = "internal_error"
)

type AgentRequest struct {
	Version string          `json:"version"`
	ID      string          `json:"id"`
	Op      AgentOperation  `json:"op"`
	Body    json.RawMessage `json:"body"`
}

type AgentResponse struct {
	Version string          `json:"version"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Body    json.RawMessage `json:"body,omitempty"`
	Error   *AgentError     `json:"error,omitempty"`
}

type AgentEventFrame struct {
	Version        string            `json:"version"`
	SubscriptionID string            `json:"subscription_id"`
	Cursor         string            `json:"cursor"`
	Event          AgentInboundEvent `json:"event"`
}

type AgentError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *AgentError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

type AgentEmpty struct{}

type AgentHealth struct {
	Status          string             `json:"status"`
	Ready           bool               `json:"ready"`
	PID             int                `json:"pid,omitempty"`
	ProtocolVersion string             `json:"protocol_version"`
	StartedAt       string             `json:"started_at,omitempty"`
	Frida           FridaStatus        `json:"frida"`
	Contacts        AgentContactCounts `json:"contacts"`
	LatestCursor    string             `json:"latest_cursor,omitempty"`
}

type AgentContactSearchRequest struct {
	Keyword   string `json:"keyword,omitempty"`
	LocalType *int   `json:"local_type,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}

type AgentContactSearchResult struct {
	Total    int       `json:"total"`
	Contacts []Contact `json:"contacts"`
	Limit    int       `json:"limit,omitempty"`
	Offset   int       `json:"offset,omitempty"`
}

type AgentContactGetRequest struct {
	Username string `json:"username"`
}

type AgentContactCounts struct {
	Contacts    int    `json:"contacts"`
	Groups      int    `json:"groups"`
	Members     int    `json:"members"`
	RefreshedAt string `json:"refreshed_at,omitempty"`
}

type AgentGroupMembersRequest struct {
	GroupID string `json:"group_id"`
}

type AgentControlledContentType string

const (
	AgentControlledContentText       AgentControlledContentType = "text"
	AgentControlledContentAttachment AgentControlledContentType = "attachment"
	// AgentControlledContentEmoji 按表情 md5 发送一条表情消息（worker 的 "emoji" 分支）。
	// 老客户端不认识这个取值，所以只在显式请求时出现；未知 type 仍按原样拒绝。
	AgentControlledContentEmoji AgentControlledContentType = "emoji"
)

type AgentRecipient struct {
	ChatType ChatType `json:"chat_type"`
	PeerID   string   `json:"peer_id"`
}

type AgentControlledContent struct {
	Type         AgentControlledContentType `json:"type"`
	Text         string                     `json:"text,omitempty"`
	AttachmentID string                     `json:"attachment_id,omitempty"`
	// MD5 只在 type=emoji 时出现：32 位小写 hex 的表情 md5，服务端按它反查表情。
	// omitempty 保证 text/attachment 的内容摘要（AgentControlledContentSHA256）逐字节不变。
	MD5 string `json:"md5,omitempty"`
}

type AgentFrequencyScope struct {
	ID            string `json:"id"`
	WindowSeconds int64  `json:"window_seconds"`
	Limit         int    `json:"limit"`
}

type AgentSendAuthorization struct {
	ActionID            string              `json:"action_id"`
	PermissionRequestID string              `json:"permission_request_id"`
	ContentSHA256       string              `json:"content_sha256"`
	AudienceID          string              `json:"audience_id,omitempty"`
	AudienceVersion     uint64              `json:"audience_version"`
	ExpiresAt           time.Time           `json:"expires_at"`
	FrequencyScope      AgentFrequencyScope `json:"frequency_scope"`
}

type AgentControlledSendRequest struct {
	IdempotencyKey string                 `json:"idempotency_key"`
	Recipient      AgentRecipient         `json:"recipient"`
	Content        AgentControlledContent `json:"content"`
	Authorization  AgentSendAuthorization `json:"authorization"`
}

type AgentAttachment struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	MediaType string     `json:"media_type,omitempty"`
	Size      int64      `json:"size"`
	SHA256    string     `json:"sha256,omitempty"`
	CreatedAt time.Time  `json:"created_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type AgentAttachmentDescribeRequest struct {
	AttachmentID string `json:"attachment_id"`
}

type AgentAttachmentChunkRequest struct {
	AttachmentID string `json:"attachment_id"`
	Offset       int64  `json:"offset"`
	Length       int    `json:"length"`
}

type AgentAttachmentChunk struct {
	AttachmentID string `json:"attachment_id"`
	Offset       int64  `json:"offset"`
	Data         []byte `json:"data"`
	EOF          bool   `json:"eof"`
}

type AgentEventsSubscribeRequest struct {
	AfterCursor string `json:"after_cursor"`
}

type AgentEventSubscription struct {
	SubscriptionID string `json:"subscription_id"`
	Cursor         string `json:"cursor"`
}

type AgentInboundEvent struct {
	Cursor      string            `json:"cursor,omitempty"`
	MessageKey  string            `json:"message_key"`
	Source      string            `json:"source"`
	Mid         string            `json:"mid,omitempty"`
	ChatType    ChatType          `json:"chat_type"`
	PeerID      string            `json:"peer_id"`
	AuthorID    string            `json:"author_id"`
	GroupID     string            `json:"group_id,omitempty"`
	Text        string            `json:"text"`
	Kinds       []string          `json:"kinds"`
	Timestamp   int64             `json:"timestamp"`
	Suspicious  bool              `json:"suspicious"`
	Attachments []AgentAttachment `json:"attachments,omitempty"`
}

type AgentPeerCredentials struct {
	UID       uint32
	GID       uint32
	PID       int
	Available bool
}

func IsValidAgentOperation(operation AgentOperation) bool {
	switch operation {
	case AgentOperationHealthGet, AgentOperationContactsSearch, AgentOperationContactsGet,
		AgentOperationContactsRefresh, AgentOperationGroupsList, AgentOperationGroupsMembers,
		AgentOperationFridaStatus, AgentOperationFridaInitialize, AgentOperationFridaRescan,
		AgentOperationFridaDetach, AgentOperationMessageSendControlled,
		AgentOperationAttachmentDescribe, AgentOperationAttachmentReadChunk,
		AgentOperationEventsSubscribe:
		return true
	default:
		return false
	}
}

func IsValidAgentErrorCode(code string) bool {
	switch code {
	case AgentErrorCodeInvalidFrame, AgentErrorCodeFrameTooLarge,
		AgentErrorCodeUnsupportedVersion, AgentErrorCodeUnsupportedOperation,
		AgentErrorCodeUnauthorizedPeer, AgentErrorCodeValidation, AgentErrorCodeNotFound,
		AgentErrorCodeConflict, AgentErrorCodeUnavailable, AgentErrorCodeCursorExpired,
		AgentErrorCodeDeadlineExceeded, AgentErrorCodeDeliveryFailed,
		AgentErrorCodeDeliveryAmbiguous, AgentErrorCodeInternal:
		return true
	default:
		return false
	}
}

func NewAgentRequest(id string, operation AgentOperation, body any) (AgentRequest, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return AgentRequest{}, &AgentError{Code: AgentErrorCodeValidation, Message: "request body is invalid"}
	}
	request := AgentRequest{Version: AgentProtocolVersion, ID: id, Op: operation, Body: raw}
	if err := ValidateAgentRequest(request); err != nil {
		return AgentRequest{}, err
	}
	return request, nil
}

func ValidateAgentRequest(request AgentRequest) error {
	if request.Version != AgentProtocolVersion {
		return &AgentError{Code: AgentErrorCodeUnsupportedVersion, Message: "protocol version is unsupported"}
	}
	if !IsValidAgentOperation(request.Op) {
		return &AgentError{Code: AgentErrorCodeUnsupportedOperation, Message: "operation is unsupported"}
	}
	if err := validateAgentValue("request id", request.ID, 512); err != nil {
		return err
	}
	if len(request.Body) == 0 || bytes.Equal(bytes.TrimSpace(request.Body), []byte("null")) {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "request body is required"}
	}
	return validateAgentRequestBody(request.Op, request.Body)
}

func ValidateAgentResponse(response AgentResponse) error {
	if response.Version != AgentProtocolVersion {
		return &AgentError{Code: AgentErrorCodeUnsupportedVersion, Message: "protocol version is unsupported"}
	}
	if err := validateAgentValue("response id", response.ID, 512); err != nil {
		return err
	}
	if response.OK {
		if response.Error != nil || len(response.Body) == 0 {
			return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "successful response is invalid"}
		}
		return nil
	}
	if response.Error == nil || !IsValidAgentErrorCode(response.Error.Code) ||
		strings.TrimSpace(response.Error.Message) == "" || len(bytes.TrimSpace(response.Body)) != 0 {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "failed response is invalid"}
	}
	return nil
}

func ValidateAgentEventFrame(frame AgentEventFrame) error {
	if frame.Version != AgentProtocolVersion {
		return &AgentError{Code: AgentErrorCodeUnsupportedVersion, Message: "protocol version is unsupported"}
	}
	if err := validateAgentValue("subscription id", frame.SubscriptionID, 512); err != nil {
		return err
	}
	if err := ValidateAgentCursor(frame.Cursor, false); err != nil {
		return err
	}
	if frame.Event.MessageKey == "" {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "event message key is required"}
	}
	for _, attachment := range frame.Event.Attachments {
		if err := ValidateAgentAttachment(attachment); err != nil {
			return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "event attachment is invalid"}
		}
	}
	return nil
}

func DecodeAgentRequestFrame(frame []byte) (AgentRequest, error) {
	var request AgentRequest
	if err := decodeAgentFrame(frame, &request); err != nil {
		return AgentRequest{}, err
	}
	return request, ValidateAgentRequest(request)
}

func DecodeAgentResponseFrame(frame []byte) (AgentResponse, error) {
	var response AgentResponse
	if err := decodeAgentFrame(frame, &response); err != nil {
		return AgentResponse{}, err
	}
	return response, ValidateAgentResponse(response)
}

func DecodeAgentEventFrame(frame []byte) (AgentEventFrame, error) {
	var event AgentEventFrame
	if err := decodeAgentFrame(frame, &event); err != nil {
		return AgentEventFrame{}, err
	}
	return event, ValidateAgentEventFrame(event)
}

func DecodeAgentBody(body json.RawMessage, destination any) error {
	trimmedBody := bytes.TrimSpace(body)
	if destination == nil || len(trimmedBody) == 0 || bytes.Equal(trimmedBody, []byte("null")) {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "response body is invalid"}
	}
	if err := decodeAgentJSON(body, destination, false); err != nil {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "response body is invalid"}
	}
	return nil
}

func ReadAgentRequestFrame(reader *bufio.Reader) (AgentRequest, error) {
	frame, err := readAgentFrame(reader)
	if err != nil {
		return AgentRequest{}, err
	}
	return DecodeAgentRequestFrame(frame)
}

func ReadAgentResponseFrame(reader *bufio.Reader) (AgentResponse, error) {
	frame, err := readAgentFrame(reader)
	if err != nil {
		return AgentResponse{}, err
	}
	return DecodeAgentResponseFrame(frame)
}

func ReadAgentEventFrame(reader *bufio.Reader) (AgentEventFrame, error) {
	frame, err := readAgentFrame(reader)
	if err != nil {
		return AgentEventFrame{}, err
	}
	return DecodeAgentEventFrame(frame)
}

func WriteAgentFrame(writer io.Writer, value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "frame is invalid"}
	}
	if len(frame) > AgentMaxFrameBytes {
		return &AgentError{Code: AgentErrorCodeFrameTooLarge, Message: "frame exceeds the maximum size"}
	}
	frame = append(frame, '\n')
	for len(frame) > 0 {
		n, err := writer.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func ValidateAgentCursor(cursor string, allowEmpty bool) error {
	if cursor == "" && allowEmpty {
		return nil
	}
	if cursor == "" || len(cursor) > 128 || (len(cursor) > 1 && cursor[0] == '0') {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "cursor is invalid"}
	}
	for _, char := range cursor {
		if char < '0' || char > '9' {
			return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "cursor is invalid"}
		}
	}
	return nil
}

func CompareAgentCursors(left, right string) int {
	left = strings.TrimLeft(left, "0")
	right = strings.TrimLeft(right, "0")
	if left == "" {
		left = "0"
	}
	if right == "" {
		right = "0"
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return strings.Compare(left, right)
}

func ValidateAgentAttachment(attachment AgentAttachment) error {
	if err := validateAgentOpaqueID("attachment id", attachment.ID, 256); err != nil {
		return err
	}
	if attachment.Size < 0 {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "attachment size is invalid"}
	}
	if attachment.SHA256 != "" {
		if len(attachment.SHA256) != sha256.Size*2 ||
			strings.ToLower(attachment.SHA256) != attachment.SHA256 {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "attachment sha256 is invalid"}
		}
		if _, err := hex.DecodeString(attachment.SHA256); err != nil {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "attachment sha256 is invalid"}
		}
	}
	if attachment.ExpiresAt != nil {
		if attachment.ExpiresAt.IsZero() ||
			(!attachment.CreatedAt.IsZero() && !attachment.ExpiresAt.After(attachment.CreatedAt)) {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "attachment timestamps are invalid"}
		}
	}
	return nil
}

func AgentControlledContentSHA256(content AgentControlledContent) (string, error) {
	normalized, err := normalizeAgentControlledContent(content)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateAgentControlledSendRequest(request AgentControlledSendRequest) error {
	if err := validateAgentControlledSendRequestStructure(request); err != nil {
		return err
	}
	return validateAgentControlledSendFreshness(request, time.Now())
}

func validateAgentControlledSendRequestStructure(request AgentControlledSendRequest) error {
	if err := validateAgentIdempotencyKey(request.IdempotencyKey); err != nil {
		return err
	}
	if !IsValidChatType(request.Recipient.ChatType) {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "recipient chat type is invalid"}
	}
	if err := validateAgentOpaqueID("recipient peer id", request.Recipient.PeerID, 256); err != nil {
		return err
	}
	if request.Recipient.ChatType == ChatTypePrivate && strings.HasSuffix(request.Recipient.PeerID, "@chatroom") {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "private recipient is invalid"}
	}
	if request.Recipient.ChatType == ChatTypeGroup && !strings.HasSuffix(request.Recipient.PeerID, "@chatroom") {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "group recipient is invalid"}
	}
	contentHash, err := AgentControlledContentSHA256(request.Content)
	if err != nil {
		return err
	}
	auth := request.Authorization
	if err := validateAgentOpaqueID("action id", auth.ActionID, 512); err != nil {
		return err
	}
	if strings.Contains(request.IdempotencyKey, ":") &&
		request.IdempotencyKey != "send:"+auth.ActionID {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "idempotency key is invalid"}
	}
	if err := validateAgentOpaqueID("permission request id", auth.PermissionRequestID, 512); err != nil {
		return err
	}
	if auth.AudienceID != "" {
		if err := validateAgentOpaqueID("audience id", auth.AudienceID, 512); err != nil {
			return err
		}
	}
	if auth.ContentSHA256 != contentHash || auth.AudienceVersion == 0 || auth.ExpiresAt.IsZero() {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "authorization scope is invalid"}
	}
	if err := validateAgentOpaqueID("frequency scope id", auth.FrequencyScope.ID, 512); err != nil {
		return err
	}
	if auth.FrequencyScope.WindowSeconds <= 0 || auth.FrequencyScope.Limit <= 0 {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "frequency scope is invalid"}
	}
	return nil
}

func validateAgentControlledSendFreshness(request AgentControlledSendRequest, now time.Time) error {
	if !request.Authorization.ExpiresAt.After(now) {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "authorization scope is invalid"}
	}
	return nil
}

func ValidateAgentSocketPath(socketPath string) error {
	if socketPath == "" || strings.IndexByte(socketPath, 0) >= 0 ||
		!filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath ||
		len([]byte(socketPath)) > agentMaxUnixSocketPathBytes {
		return ErrUnsafeAgentSocketPath
	}
	return nil
}

func ValidateAgentSocketParent(socketPath string, uid uint32) error {
	if err := ValidateAgentSocketPath(socketPath); err != nil {
		return err
	}
	info, err := os.Lstat(filepath.Dir(socketPath))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0700 {
		return ErrUnsafeAgentSocketPath
	}
	owner, ok := agentFileOwner(info)
	if !ok || owner != uid {
		return ErrUnsafeAgentSocketPath
	}
	return nil
}

func ValidateAgentSocketFile(socketPath string, uid uint32) error {
	if err := ValidateAgentSocketParent(socketPath, uid); err != nil {
		return err
	}
	info, err := os.Lstat(socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return ErrUnsafeAgentSocketPath
	}
	owner, ok := agentFileOwner(info)
	if !ok || owner != uid {
		return ErrUnsafeAgentSocketPath
	}
	return nil
}

func ReadAgentPeerCredentials(conn *net.UnixConn) (AgentPeerCredentials, error) {
	if conn == nil {
		return AgentPeerCredentials{}, &AgentError{Code: AgentErrorCodeUnauthorizedPeer, Message: "peer connection is unavailable"}
	}
	return agentPeerCredentials(conn)
}

func ValidateAgentPeerUID(conn *net.UnixConn, expectedUID uint32) error {
	credentials, err := ReadAgentPeerCredentials(conn)
	if err != nil {
		return &AgentError{Code: AgentErrorCodeUnauthorizedPeer, Message: "peer credentials are unavailable"}
	}
	if credentials.Available && credentials.UID != expectedUID {
		return &AgentError{Code: AgentErrorCodeUnauthorizedPeer, Message: "peer uid is not authorized"}
	}
	return nil
}

func ValidateAgentPeerCredentials(conn *net.UnixConn, expectedUID uint32) error {
	return ValidateAgentPeerUID(conn, expectedUID)
}

func validateAgentRequestBody(operation AgentOperation, body json.RawMessage) error {
	var destination any
	switch operation {
	case AgentOperationHealthGet, AgentOperationContactsRefresh, AgentOperationGroupsList,
		AgentOperationFridaStatus, AgentOperationFridaRescan, AgentOperationFridaDetach:
		destination = &AgentEmpty{}
	case AgentOperationContactsSearch:
		destination = &AgentContactSearchRequest{}
	case AgentOperationContactsGet:
		destination = &AgentContactGetRequest{}
	case AgentOperationGroupsMembers:
		destination = &AgentGroupMembersRequest{}
	case AgentOperationFridaInitialize:
		destination = &FridaInitializeRequest{}
	case AgentOperationMessageSendControlled:
		destination = &AgentControlledSendRequest{}
	case AgentOperationAttachmentDescribe:
		destination = &AgentAttachmentDescribeRequest{}
	case AgentOperationAttachmentReadChunk:
		destination = &AgentAttachmentChunkRequest{}
	case AgentOperationEventsSubscribe:
		destination = &AgentEventsSubscribeRequest{}
	default:
		return &AgentError{Code: AgentErrorCodeUnsupportedOperation, Message: "operation is unsupported"}
	}
	if err := decodeAgentJSON(body, destination, false); err != nil {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "request body is invalid"}
	}
	switch value := destination.(type) {
	case *AgentContactSearchRequest:
		if utf8.RuneCountInString(value.Keyword) > 200 || value.Limit < 0 || value.Limit > 200 || value.Offset < 0 {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "contact search request is invalid"}
		}
	case *AgentContactGetRequest:
		return validateAgentOpaqueID("contact username", value.Username, 256)
	case *AgentGroupMembersRequest:
		return validateAgentOpaqueID("group id", value.GroupID, 256)
	case *AgentControlledSendRequest:
		return validateAgentControlledSendRequestStructure(*value)
	case *AgentAttachmentDescribeRequest:
		return validateAgentOpaqueID("attachment id", value.AttachmentID, 256)
	case *AgentAttachmentChunkRequest:
		if err := validateAgentOpaqueID("attachment id", value.AttachmentID, 256); err != nil {
			return err
		}
		if value.Offset < 0 || value.Length <= 0 || value.Length > AgentMaxAttachmentChunkBytes {
			return &AgentError{Code: AgentErrorCodeValidation, Message: "attachment chunk range is invalid"}
		}
	case *AgentEventsSubscribeRequest:
		return ValidateAgentCursor(value.AfterCursor, true)
	}
	return nil
}

func normalizeAgentControlledContent(content AgentControlledContent) (AgentControlledContent, error) {
	switch content.Type {
	case AgentControlledContentText:
		text := strings.TrimSpace(content.Text)
		if text == "" || utf8.RuneCountInString(text) > AgentMaxControlledTextRunes ||
			strings.IndexByte(text, 0) >= 0 || content.AttachmentID != "" || content.MD5 != "" {
			return AgentControlledContent{}, &AgentError{Code: AgentErrorCodeValidation, Message: "controlled text is invalid"}
		}
		content.Text = text
	case AgentControlledContentAttachment:
		if content.Text != "" || content.MD5 != "" {
			return AgentControlledContent{}, &AgentError{Code: AgentErrorCodeValidation, Message: "attachment content is invalid"}
		}
		if err := validateAgentOpaqueID("attachment id", content.AttachmentID, 256); err != nil {
			return AgentControlledContent{}, err
		}
	case AgentControlledContentEmoji:
		if content.Text != "" || content.AttachmentID != "" || !IsValidAgentEmojiMD5(content.MD5) {
			return AgentControlledContent{}, &AgentError{Code: AgentErrorCodeValidation, Message: "emoji content is invalid"}
		}
	default:
		return AgentControlledContent{}, &AgentError{Code: AgentErrorCodeValidation, Message: "controlled content type is invalid"}
	}
	return content, nil
}

// AgentEmojiMD5HexLength 是表情 md5 的固定长度（32 位小写 hex，与 sendemoji body 里
// 的 f3.1 完全一致）。
const AgentEmojiMD5HexLength = 32

// IsValidAgentEmojiMD5 判断 value 是否是可直接发给 sendemoji 的表情 md5。
// 只接受小写：内容摘要是对归一化内容做的 sha256，大小写两态会让同一张表情算出两个摘要。
func IsValidAgentEmojiMD5(value string) bool {
	if len(value) != AgentEmojiMD5HexLength || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateAgentValue(field, value string, maxBytes int) error {
	if value == "" || len(value) > maxBytes || strings.TrimSpace(value) != value {
		return &AgentError{Code: AgentErrorCodeValidation, Message: field + " is invalid"}
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return &AgentError{Code: AgentErrorCodeValidation, Message: field + " is invalid"}
		}
	}
	return nil
}

func validateAgentOpaqueID(field, value string, maxBytes int) error {
	if err := validateAgentValue(field, value, maxBytes); err != nil {
		return err
	}
	parsed, err := url.Parse(value)
	if err != nil ||
		parsed.IsAbs() ||
		parsed.Host != "" ||
		strings.ContainsAny(value, `/\`) ||
		strings.ContainsAny(parsed.Path, `/\`) ||
		value == "." ||
		value == ".." ||
		parsed.Path == "." ||
		parsed.Path == ".." {
		return &AgentError{Code: AgentErrorCodeValidation, Message: field + " is invalid"}
	}
	return nil
}

func validateAgentIdempotencyKey(value string) error {
	const (
		field     = "idempotency key"
		maxBytes  = 512
		namespace = "send:"
	)
	if err := validateAgentValue(field, value, maxBytes); err != nil {
		return err
	}
	if !strings.Contains(value, ":") {
		return validateAgentOpaqueID(field, value, maxBytes)
	}
	if !strings.HasPrefix(value, namespace) {
		return &AgentError{Code: AgentErrorCodeValidation, Message: field + " is invalid"}
	}
	suffix := strings.TrimPrefix(value, namespace)
	if err := validateAgentOpaqueID(field, suffix, maxBytes-len(namespace)); err != nil {
		return &AgentError{Code: AgentErrorCodeValidation, Message: field + " is invalid"}
	}
	return nil
}

func readAgentFrame(reader *bufio.Reader) ([]byte, error) {
	frame := make([]byte, 0, 4096)
	for {
		fragment, err := reader.ReadSlice('\n')
		hasNewline := len(fragment) > 0 && fragment[len(fragment)-1] == '\n'
		if hasNewline {
			fragment = fragment[:len(fragment)-1]
		}
		if len(frame)+len(fragment) > AgentMaxFrameBytes {
			if !hasNewline {
				for err == bufio.ErrBufferFull {
					fragment, err = reader.ReadSlice('\n')
					if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
						break
					}
				}
			}
			return nil, &AgentError{Code: AgentErrorCodeFrameTooLarge, Message: "frame exceeds the maximum size"}
		}
		frame = append(frame, fragment...)
		if hasNewline {
			if len(bytes.TrimSpace(frame)) == 0 {
				return nil, &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "frame is empty"}
			}
			return frame, nil
		}
		switch err {
		case nil, bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return nil, &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "frame is not newline terminated"}
		default:
			return nil, err
		}
	}
}

func decodeAgentFrame(frame []byte, destination any) error {
	frame = bytes.TrimSuffix(frame, []byte{'\n'})
	if len(frame) > AgentMaxFrameBytes {
		return &AgentError{Code: AgentErrorCodeFrameTooLarge, Message: "frame exceeds the maximum size"}
	}
	if err := decodeAgentJSON(frame, destination, true); err != nil {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "frame is invalid"}
	}
	return nil
}

func decodeAgentJSON(data []byte, destination any, requireObject bool) error {
	if err := validateAgentJSONTokens(data, requireObject); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrAgentInvalidFrame
	}
	return nil
}

func validateAgentJSONTokens(data []byte, requireObject bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if requireObject {
		delim, ok := token.(json.Delim)
		if !ok || delim != '{' {
			return ErrAgentInvalidFrame
		}
	}
	if err := consumeAgentJSONValue(decoder, token, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return ErrAgentInvalidFrame
	}
	return nil
}

func consumeAgentJSONValue(decoder *json.Decoder, token json.Token, depth int) error {
	if depth > agentMaxJSONDepth {
		return ErrAgentInvalidFrame
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrAgentInvalidFrame
			}
			if _, duplicate := keys[key]; duplicate {
				return ErrAgentInvalidFrame
			}
			keys[key] = struct{}{}
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := consumeAgentJSONValue(decoder, valueToken, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrAgentInvalidFrame
		}
	case '[':
		for decoder.More() {
			valueToken, err := decoder.Token()
			if err != nil {
				return err
			}
			if err := consumeAgentJSONValue(decoder, valueToken, depth+1); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrAgentInvalidFrame
		}
	default:
		return ErrAgentInvalidFrame
	}
	return nil
}

func agentFileOwner(info os.FileInfo) (uint32, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Uid, true
}
