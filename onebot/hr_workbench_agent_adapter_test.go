package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type task003AgentClient struct {
	mu sync.Mutex

	calls []string

	searchRequest   AgentContactSearchRequest
	contactID       string
	groupID         string
	refreshDeadline time.Time
	fridaInit       FridaInitializeRequest
	sendRequests    []AgentControlledSendRequest
	subscribeFrom   []string

	healthResult       AgentHealth
	healthErr          error
	searchResult       AgentContactSearchResult
	searchErr          error
	contactResult      Contact
	contactErr         error
	refreshResult      AgentContactCounts
	refreshErr         error
	groupsResult       []Group
	groupsErr          error
	groupMembersResult []GroupMember
	groupMembersErr    error
	fridaResult        FridaStatus
	fridaStatusErr     error
	fridaInitializeErr error
	fridaRescanErr     error
	fridaDetachErr     error
	sendResult         AutomationDeliveryResult
	sendErr            error
	subscribeEvents    <-chan AgentInboundEvent
	subscribeErrors    <-chan error
	subscribeErr       error
	subscribe          func(
		context.Context,
		string,
	) (<-chan AgentInboundEvent, <-chan error, error)
	closeErr error
}

var _ AgentClient = (*task003AgentClient)(nil)

func (c *task003AgentClient) record(call string) {
	c.mu.Lock()
	c.calls = append(c.calls, call)
	c.mu.Unlock()
}

func (c *task003AgentClient) callCount(call string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, got := range c.calls {
		if got == call {
			count++
		}
	}
	return count
}

func (c *task003AgentClient) Health(context.Context) (AgentHealth, error) {
	c.record("health")
	return c.healthResult, c.healthErr
}

func (c *task003AgentClient) SearchContacts(
	_ context.Context,
	request AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "contacts.search")
	c.searchRequest = request
	c.mu.Unlock()
	return c.searchResult, c.searchErr
}

func (c *task003AgentClient) Contact(_ context.Context, username string) (Contact, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "contacts.get")
	c.contactID = username
	c.mu.Unlock()
	return c.contactResult, c.contactErr
}

func (c *task003AgentClient) RefreshContacts(ctx context.Context) (AgentContactCounts, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "contacts.refresh")
	c.refreshDeadline, _ = ctx.Deadline()
	c.mu.Unlock()
	return c.refreshResult, c.refreshErr
}

func (c *task003AgentClient) Groups(context.Context) ([]Group, error) {
	c.record("groups.list")
	return append([]Group(nil), c.groupsResult...), c.groupsErr
}

func (c *task003AgentClient) GroupMembers(
	_ context.Context,
	groupID string,
) ([]GroupMember, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "groups.members")
	c.groupID = groupID
	c.mu.Unlock()
	return append([]GroupMember(nil), c.groupMembersResult...), c.groupMembersErr
}

func (c *task003AgentClient) FridaStatus(context.Context) (FridaStatus, error) {
	c.record("frida.status")
	return c.fridaResult, c.fridaStatusErr
}

func (c *task003AgentClient) InitializeFrida(
	_ context.Context,
	request FridaInitializeRequest,
) (FridaStatus, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "frida.initialize")
	c.fridaInit = request
	c.mu.Unlock()
	return c.fridaResult, c.fridaInitializeErr
}

func (c *task003AgentClient) RescanFrida(context.Context) (FridaStatus, error) {
	c.record("frida.rescan")
	return c.fridaResult, c.fridaRescanErr
}

func (c *task003AgentClient) DetachFrida(context.Context) (FridaStatus, error) {
	c.record("frida.detach")
	return c.fridaResult, c.fridaDetachErr
}

func (c *task003AgentClient) SendControlled(
	_ context.Context,
	request AgentControlledSendRequest,
) (AutomationDeliveryResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "message.send_controlled")
	c.sendRequests = append(c.sendRequests, request)
	c.mu.Unlock()
	return c.sendResult, c.sendErr
}

func (c *task003AgentClient) DescribeAttachment(
	context.Context,
	string,
) (AgentAttachment, error) {
	c.record("attachment.describe")
	return AgentAttachment{}, nil
}

func (c *task003AgentClient) ReadAttachmentChunk(
	context.Context,
	AgentAttachmentChunkRequest,
) (AgentAttachmentChunk, error) {
	c.record("attachment.read_chunk")
	return AgentAttachmentChunk{}, nil
}

func (c *task003AgentClient) SubscribeEvents(
	ctx context.Context,
	afterCursor string,
) (<-chan AgentInboundEvent, <-chan error, error) {
	c.mu.Lock()
	c.calls = append(c.calls, "events.subscribe")
	c.subscribeFrom = append(c.subscribeFrom, afterCursor)
	subscribe := c.subscribe
	c.mu.Unlock()
	if subscribe != nil {
		return subscribe(ctx, afterCursor)
	}
	return c.subscribeEvents, c.subscribeErrors, c.subscribeErr
}

func (c *task003AgentClient) Close() error {
	c.record("close")
	return c.closeErr
}

func TestInstallHRWorkbenchAgentClientRestoresOnlyInstalledClient(t *testing.T) {
	original := &task003AgentClient{}
	restoreOriginal := installHRWorkbenchAgentClient(original)
	t.Cleanup(restoreOriginal)

	replacement := &task003AgentClient{}
	restoreReplacement := installHRWorkbenchAgentClient(replacement)
	if got := currentHRWorkbenchAgentClient(); got != replacement {
		t.Fatalf("current client = %T %p, want replacement %p", got, got, replacement)
	}

	restoreOriginal()
	if got := currentHRWorkbenchAgentClient(); got != replacement {
		t.Fatalf("out-of-order restore changed current client to %T %p", got, got)
	}

	restoreReplacement()
	if got := currentHRWorkbenchAgentClient(); got != original {
		t.Fatalf("restored client = %T %p, want original %p", got, got, original)
	}
}

func TestManualControlledSendRequestBindsAuthorizationToExactContent(t *testing.T) {
	now := time.Now().UTC()
	content := AgentControlledContent{Type: AgentControlledContentText, Text: "hello"}
	request, err := newManualAgentControlledSendRequest(
		"http",
		AgentRecipient{ChatType: ChatTypePrivate, PeerID: "wxid_peer"},
		content,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateAgentControlledSendRequest(request); err != nil {
		t.Fatalf("ValidateAgentControlledSendRequest() error = %v", err)
	}
	wantHash, err := AgentControlledContentSHA256(content)
	if err != nil {
		t.Fatal(err)
	}
	if request.Authorization.ContentSHA256 != wantHash {
		t.Fatalf("content hash = %q, want %q", request.Authorization.ContentSHA256, wantHash)
	}
	if request.Authorization.ActionID == "" ||
		request.Authorization.PermissionRequestID == "" ||
		request.Authorization.AudienceVersion != 1 ||
		request.Authorization.FrequencyScope.Limit != 1 {
		t.Fatalf("authorization metadata = %#v", request.Authorization)
	}
	if !request.Authorization.ExpiresAt.Equal(now.Add(manualAgentAuthorizationTTL)) {
		t.Fatalf("expires_at = %s, want %s", request.Authorization.ExpiresAt, now.Add(manualAgentAuthorizationTTL))
	}

	second, err := newManualAgentControlledSendRequest(
		"http",
		request.Recipient,
		content,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.IdempotencyKey == request.IdempotencyKey {
		t.Fatal("two manual sends reused an idempotency key")
	}
}

func TestAgentHTTPStatusKeepsCursorExpiredVisible(t *testing.T) {
	err := &AgentError{
		Code:    AgentErrorCodeCursorExpired,
		Message: "cursor is older than retained history",
	}
	if got := agentHTTPStatus(err); got != 409 {
		t.Fatalf("agentHTTPStatus(cursor_expired) = %d, want 409", got)
	}
	if got := publicAgentError(err, "fallback"); got != err.Message {
		t.Fatalf("publicAgentError() = %q, want %q", got, err.Message)
	}
	if got := agentHTTPStatus(errors.New("socket unavailable")); got != 503 {
		t.Fatalf("agentHTTPStatus(non-agent error) = %d, want 503", got)
	}
}
