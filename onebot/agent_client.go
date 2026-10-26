package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type AgentClient interface {
	Health(context.Context) (AgentHealth, error)
	SearchContacts(context.Context, AgentContactSearchRequest) (AgentContactSearchResult, error)
	Contact(context.Context, string) (Contact, error)
	RefreshContacts(context.Context) (AgentContactCounts, error)
	Groups(context.Context) ([]Group, error)
	GroupMembers(context.Context, string) ([]GroupMember, error)
	FridaStatus(context.Context) (FridaStatus, error)
	InitializeFrida(context.Context, FridaInitializeRequest) (FridaStatus, error)
	RescanFrida(context.Context) (FridaStatus, error)
	DetachFrida(context.Context) (FridaStatus, error)
	SendControlled(context.Context, AgentControlledSendRequest) (AutomationDeliveryResult, error)
	DescribeAttachment(context.Context, string) (AgentAttachment, error)
	ReadAttachmentChunk(context.Context, AgentAttachmentChunkRequest) (AgentAttachmentChunk, error)
	SubscribeEvents(context.Context, string) (<-chan AgentInboundEvent, <-chan error, error)
	Close() error
}

type unixAgentClient struct {
	socketPath string
	pathErr    error
	sequence   atomic.Uint64

	mu          sync.Mutex
	closed      bool
	shutdown    chan struct{}
	connections map[*net.UnixConn]struct{}
}

func NewUnixAgentClient(socketPath string) AgentClient {
	return &unixAgentClient{
		socketPath:  socketPath,
		pathErr:     ValidateAgentSocketPath(socketPath),
		shutdown:    make(chan struct{}),
		connections: make(map[*net.UnixConn]struct{}),
	}
}

func (c *unixAgentClient) Health(ctx context.Context) (AgentHealth, error) {
	var result AgentHealth
	err := c.call(ctx, AgentOperationHealthGet, AgentEmpty{}, &result)
	return result, err
}

func (c *unixAgentClient) SearchContacts(
	ctx context.Context,
	request AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	var result AgentContactSearchResult
	err := c.call(ctx, AgentOperationContactsSearch, request, &result)
	return result, err
}

func (c *unixAgentClient) Contact(ctx context.Context, username string) (Contact, error) {
	var result Contact
	err := c.call(ctx, AgentOperationContactsGet, AgentContactGetRequest{Username: username}, &result)
	return result, err
}

func (c *unixAgentClient) RefreshContacts(ctx context.Context) (AgentContactCounts, error) {
	var result AgentContactCounts
	err := c.call(ctx, AgentOperationContactsRefresh, AgentEmpty{}, &result)
	return result, err
}

func (c *unixAgentClient) Groups(ctx context.Context) ([]Group, error) {
	var result []Group
	err := c.call(ctx, AgentOperationGroupsList, AgentEmpty{}, &result)
	return result, err
}

func (c *unixAgentClient) GroupMembers(ctx context.Context, groupID string) ([]GroupMember, error) {
	var result []GroupMember
	err := c.call(ctx, AgentOperationGroupsMembers, AgentGroupMembersRequest{GroupID: groupID}, &result)
	return result, err
}

func (c *unixAgentClient) FridaStatus(ctx context.Context) (FridaStatus, error) {
	var result FridaStatus
	err := c.call(ctx, AgentOperationFridaStatus, AgentEmpty{}, &result)
	return result, err
}

func (c *unixAgentClient) InitializeFrida(
	ctx context.Context,
	request FridaInitializeRequest,
) (FridaStatus, error) {
	var result FridaStatus
	err := c.call(ctx, AgentOperationFridaInitialize, request, &result)
	return result, err
}

func (c *unixAgentClient) RescanFrida(ctx context.Context) (FridaStatus, error) {
	var result FridaStatus
	err := c.call(ctx, AgentOperationFridaRescan, AgentEmpty{}, &result)
	return result, err
}

func (c *unixAgentClient) DetachFrida(ctx context.Context) (FridaStatus, error) {
	var result FridaStatus
	err := c.call(ctx, AgentOperationFridaDetach, AgentEmpty{}, &result)
	return result, err
}

func (c *unixAgentClient) SendControlled(
	ctx context.Context,
	request AgentControlledSendRequest,
) (AutomationDeliveryResult, error) {
	var result AutomationDeliveryResult
	if ctx == nil {
		return result, &AgentError{Code: AgentErrorCodeValidation, Message: "context is required"}
	}
	requestID := c.nextRequestID()
	frame, err := NewAgentRequest(requestID, AgentOperationMessageSendControlled, request)
	if err != nil {
		return result, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return result, err
	}
	stopWatch := watchAgentContext(ctx, conn)
	defer stopWatch()
	defer c.release(conn)

	if err := WriteAgentFrame(conn, frame); err != nil {
		return result, c.transportError(ctx, err)
	}
	response, err := ReadAgentResponseFrame(bufio.NewReaderSize(conn, 64<<10))
	if err != nil {
		return ambiguousAgentDelivery()
	}
	if response.ID != requestID {
		return ambiguousAgentDelivery()
	}
	if !response.OK {
		if response.Error.Code == AgentErrorCodeDeliveryAmbiguous {
			return ambiguousAgentDelivery()
		}
		return result, response.Error
	}
	if err := DecodeAgentBody(response.Body, &result); err != nil {
		return ambiguousAgentDelivery()
	}
	switch result.Status {
	case AutomationDeliverySent, AutomationDeliveryUnknown:
		if !result.DispatchAttempted {
			return ambiguousAgentDelivery()
		}
	case AutomationDeliveryFailed:
	default:
		return ambiguousAgentDelivery()
	}
	return result, nil
}

func (c *unixAgentClient) DescribeAttachment(ctx context.Context, attachmentID string) (AgentAttachment, error) {
	var result AgentAttachment
	err := c.call(
		ctx,
		AgentOperationAttachmentDescribe,
		AgentAttachmentDescribeRequest{AttachmentID: attachmentID},
		&result,
	)
	if err == nil && result.ID != attachmentID {
		err = &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "attachment response is invalid"}
	}
	if err == nil && ValidateAgentAttachment(result) != nil {
		err = &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "attachment response is invalid"}
	}
	return result, err
}

func (c *unixAgentClient) ReadAttachmentChunk(
	ctx context.Context,
	request AgentAttachmentChunkRequest,
) (AgentAttachmentChunk, error) {
	var result AgentAttachmentChunk
	err := c.call(ctx, AgentOperationAttachmentReadChunk, request, &result)
	if err == nil &&
		(result.AttachmentID != request.AttachmentID ||
			result.Offset != request.Offset ||
			len(result.Data) > request.Length ||
			len(result.Data) > AgentMaxAttachmentChunkBytes) {
		err = &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "attachment chunk response is invalid"}
	}
	return result, err
}

func (c *unixAgentClient) SubscribeEvents(
	ctx context.Context,
	afterCursor string,
) (<-chan AgentInboundEvent, <-chan error, error) {
	if ctx == nil {
		return nil, nil, &AgentError{Code: AgentErrorCodeValidation, Message: "context is required"}
	}
	requestID := c.nextRequestID()
	request, err := NewAgentRequest(
		requestID,
		AgentOperationEventsSubscribe,
		AgentEventsSubscribeRequest{AfterCursor: afterCursor},
	)
	if err != nil {
		return nil, nil, err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, nil, err
	}
	stopWatch := watchAgentContext(ctx, conn)
	fail := func(err error) (<-chan AgentInboundEvent, <-chan error, error) {
		stopWatch()
		c.release(conn)
		return nil, nil, err
	}
	if err := WriteAgentFrame(conn, request); err != nil {
		return fail(c.transportError(ctx, err))
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	response, err := ReadAgentResponseFrame(reader)
	if err != nil {
		return fail(c.transportError(ctx, err))
	}
	if response.ID != requestID {
		return fail(&AgentError{Code: AgentErrorCodeInvalidFrame, Message: "response id does not match request"})
	}
	if !response.OK {
		return fail(response.Error)
	}
	var subscription AgentEventSubscription
	if err := DecodeAgentBody(response.Body, &subscription); err != nil {
		return fail(err)
	}
	if err := validateAgentValue("subscription id", subscription.SubscriptionID, 512); err != nil {
		return fail(err)
	}
	if err := ValidateAgentCursor(subscription.Cursor, false); err != nil {
		return fail(err)
	}
	baseline := afterCursor
	if baseline == "" {
		baseline = "0"
	}
	if subscription.Cursor != baseline {
		return fail(&AgentError{Code: AgentErrorCodeInvalidFrame, Message: "subscription cursor does not match request"})
	}

	events := make(chan AgentInboundEvent, 32)
	errs := make(chan error, 1)
	go c.streamEvents(ctx, conn, stopWatch, reader, subscription, events, errs)
	return events, errs, nil
}

func (c *unixAgentClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.shutdown)
	connections := make([]*net.UnixConn, 0, len(c.connections))
	for conn := range c.connections {
		connections = append(connections, conn)
	}
	c.mu.Unlock()

	var closeErr error
	for _, conn := range connections {
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) && closeErr == nil {
			closeErr = err
		}
	}
	return closeErr
}

func (c *unixAgentClient) call(ctx context.Context, operation AgentOperation, body, destination any) error {
	if ctx == nil {
		return &AgentError{Code: AgentErrorCodeValidation, Message: "context is required"}
	}
	requestID := c.nextRequestID()
	request, err := NewAgentRequest(requestID, operation, body)
	if err != nil {
		return err
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	stopWatch := watchAgentContext(ctx, conn)
	defer stopWatch()
	defer c.release(conn)

	if err := WriteAgentFrame(conn, request); err != nil {
		return c.transportError(ctx, err)
	}
	response, err := ReadAgentResponseFrame(bufio.NewReaderSize(conn, 64<<10))
	if err != nil {
		return c.transportError(ctx, err)
	}
	if response.ID != requestID {
		return &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "response id does not match request"}
	}
	if !response.OK {
		return response.Error
	}
	return DecodeAgentBody(response.Body, destination)
}

func (c *unixAgentClient) dial(ctx context.Context) (*net.UnixConn, error) {
	if c == nil {
		return nil, &AgentError{Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable"}
	}
	if c.pathErr != nil {
		return nil, &AgentError{Code: AgentErrorCodeValidation, Message: "agent socket path is unsafe"}
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	uid := uint32(os.Getuid())
	if err := ValidateAgentSocketFile(c.socketPath, uid); err != nil {
		return nil, &AgentError{Code: AgentErrorCodeUnavailable, Message: "wechat agent socket is unavailable", Retryable: true}
	}
	rawConn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.socketPath)
	if err != nil {
		return nil, c.transportError(ctx, err)
	}
	conn, ok := rawConn.(*net.UnixConn)
	if !ok {
		rawConn.Close()
		return nil, &AgentError{Code: AgentErrorCodeUnavailable, Message: "wechat agent transport is unavailable"}
	}
	if err := ValidateAgentPeerUID(conn, uid); err != nil {
		conn.Close()
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			conn.Close()
			return nil, c.transportError(ctx, err)
		}
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		conn.Close()
		return nil, net.ErrClosed
	}
	c.connections[conn] = struct{}{}
	c.mu.Unlock()
	return conn, nil
}

func (c *unixAgentClient) release(conn *net.UnixConn) {
	if conn == nil {
		return
	}
	c.mu.Lock()
	delete(c.connections, conn)
	c.mu.Unlock()
	_ = conn.Close()
}

func (c *unixAgentClient) streamEvents(
	ctx context.Context,
	conn *net.UnixConn,
	stopWatch func(),
	reader *bufio.Reader,
	subscription AgentEventSubscription,
	events chan<- AgentInboundEvent,
	errs chan<- error,
) {
	defer close(events)
	defer close(errs)
	defer stopWatch()
	defer c.release(conn)

	lastCursor := subscription.Cursor
	for {
		frame, err := ReadAgentEventFrame(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = &AgentError{Code: AgentErrorCodeUnavailable, Message: "event subscription closed", Retryable: true}
			}
			if ctx.Err() == nil && !c.isClosed() {
				errs <- c.transportError(ctx, err)
			}
			return
		}
		if frame.SubscriptionID != subscription.SubscriptionID ||
			!isAgentCursorSuccessor(lastCursor, frame.Cursor) {
			errs <- &AgentError{Code: AgentErrorCodeInvalidFrame, Message: "event stream is not ordered"}
			return
		}
		lastCursor = frame.Cursor
		frame.Event.Cursor = frame.Cursor
		select {
		case events <- frame.Event:
		case <-ctx.Done():
			return
		case <-c.shutdown:
			return
		}
	}
}

func isAgentCursorSuccessor(previous, current string) bool {
	if ValidateAgentCursor(previous, false) != nil || ValidateAgentCursor(current, false) != nil {
		return false
	}
	next := []byte(previous)
	for index := len(next) - 1; index >= 0; index-- {
		if next[index] < '9' {
			next[index]++
			return string(next) == current
		}
		next[index] = '0'
	}
	return "1"+string(next) == current
}

func (c *unixAgentClient) nextRequestID() string {
	return "req-" + strconv.FormatUint(c.sequence.Add(1), 10)
}

func (c *unixAgentClient) transportError(ctx context.Context, err error) error {
	var agentError *AgentError
	if errors.As(err, &agentError) {
		return agentError
	}
	if ctx != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &AgentError{Code: AgentErrorCodeDeadlineExceeded, Message: "agent request deadline exceeded", Retryable: true}
		}
		return ctx.Err()
	}
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return &AgentError{Code: AgentErrorCodeDeadlineExceeded, Message: "agent request deadline exceeded", Retryable: true}
	}
	if c != nil && c.isClosed() {
		return net.ErrClosed
	}
	return &AgentError{Code: AgentErrorCodeUnavailable, Message: "wechat agent is unavailable", Retryable: true}
}

func (c *unixAgentClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func ambiguousAgentDelivery() (AutomationDeliveryResult, error) {
	const message = "controlled delivery acknowledgement is unavailable"
	return AutomationDeliveryResult{
			Status:            AutomationDeliveryUnknown,
			DispatchAttempted: true,
			ErrorCode:         AgentErrorCodeDeliveryAmbiguous,
			ErrorMessage:      message,
		},
		&AgentError{
			Code:      AgentErrorCodeDeliveryAmbiguous,
			Message:   message,
			Retryable: false,
		}
}

func watchAgentContext(ctx context.Context, conn *net.UnixConn) func() {
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
		})
	}
}
