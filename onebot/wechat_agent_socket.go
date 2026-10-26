package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
)

type WechatAgentServer struct {
	runtime     *WechatAgentRuntime
	expectedUID uint32

	mu       sync.Mutex
	listener net.Listener
	conns    map[net.Conn]struct{}
	serving  bool
	wg       sync.WaitGroup
}

func NewWechatAgentServer(runtime *WechatAgentRuntime) *WechatAgentServer {
	return &WechatAgentServer{
		runtime:     runtime,
		expectedUID: uint32(os.Getuid()),
		conns:       make(map[net.Conn]struct{}),
	}
}

func (s *WechatAgentServer) Serve(ctx context.Context, listener net.Listener) error {
	if ctx == nil || listener == nil || s == nil || s.runtime == nil {
		return errors.New("wechat agent server dependencies are incomplete")
	}
	s.mu.Lock()
	if s.serving {
		s.mu.Unlock()
		return errors.New("wechat agent server is already serving")
	}
	s.serving = true
	s.listener = listener
	s.mu.Unlock()

	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.closeTransport()
		case <-stop:
		}
	}()
	defer close(stop)
	defer func() {
		s.closeTransport()
		s.wg.Wait()
		s.mu.Lock()
		s.listener = nil
		s.serving = false
		s.mu.Unlock()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			var temporary interface{ Temporary() bool }
			if errors.As(err, &temporary) && temporary.Temporary() {
				continue
			}
			return err
		}
		s.trackConnection(conn, true)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.trackConnection(conn, false)
			defer conn.Close()
			s.handleConnection(ctx, conn)
		}()
	}
}

func (s *WechatAgentServer) handleConnection(ctx context.Context, conn net.Conn) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return
	}
	if err := ValidateAgentPeerUID(unixConn, s.expectedUID); err != nil {
		return
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	frame, err := readAgentFrame(reader)
	if err != nil {
		return
	}
	var request AgentRequest
	if err := decodeAgentFrame(frame, &request); err != nil {
		return
	}
	if err := ValidateAgentRequest(request); err != nil {
		if validateAgentValue("request id", request.ID, 512) == nil {
			_ = writeWechatAgentError(conn, request.ID, err)
		}
		return
	}
	if request.Op == AgentOperationEventsSubscribe {
		s.handleEventSubscription(ctx, conn, request)
		return
	}
	body, err := s.dispatch(ctx, request)
	if err != nil {
		_ = writeWechatAgentError(conn, request.ID, err)
		return
	}
	_ = writeWechatAgentSuccess(conn, request.ID, body)
}

func (s *WechatAgentServer) dispatch(ctx context.Context, request AgentRequest) (any, error) {
	switch request.Op {
	case AgentOperationHealthGet:
		return s.runtime.Health(ctx)
	case AgentOperationContactsSearch:
		var body AgentContactSearchRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.SearchContacts(ctx, body)
	case AgentOperationContactsGet:
		var body AgentContactGetRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.Contact(ctx, body.Username)
	case AgentOperationContactsRefresh:
		return s.runtime.RefreshContacts(ctx)
	case AgentOperationGroupsList:
		return s.runtime.Groups(ctx)
	case AgentOperationGroupsMembers:
		var body AgentGroupMembersRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.GroupMembers(ctx, body.GroupID)
	case AgentOperationFridaStatus:
		return s.runtime.FridaStatus(ctx)
	case AgentOperationFridaInitialize:
		var body FridaInitializeRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.InitializeFrida(ctx, body)
	case AgentOperationFridaRescan:
		return s.runtime.RescanFrida(ctx)
	case AgentOperationFridaDetach:
		return s.runtime.DetachFrida(ctx)
	case AgentOperationMessageSendControlled:
		var body AgentControlledSendRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.SendControlled(ctx, body)
	case AgentOperationAttachmentDescribe:
		var body AgentAttachmentDescribeRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.DescribeAttachment(ctx, body.AttachmentID)
	case AgentOperationAttachmentReadChunk:
		var body AgentAttachmentChunkRequest
		if err := DecodeAgentBody(request.Body, &body); err != nil {
			return nil, err
		}
		return s.runtime.ReadAttachmentChunk(ctx, body)
	default:
		return nil, &AgentError{
			Code: AgentErrorCodeUnsupportedOperation, Message: "operation is unsupported",
		}
	}
}

func (s *WechatAgentServer) handleEventSubscription(
	ctx context.Context,
	conn net.Conn,
	request AgentRequest,
) {
	var body AgentEventsSubscribeRequest
	if err := DecodeAgentBody(request.Body, &body); err != nil {
		_ = writeWechatAgentError(conn, request.ID, err)
		return
	}
	subscription, err := s.runtime.SubscribeEvents(ctx, body.AfterCursor)
	if err != nil {
		_ = writeWechatAgentError(conn, request.ID, err)
		return
	}
	defer subscription.Close()
	if err := writeWechatAgentSuccess(conn, request.ID, AgentEventSubscription{
		SubscriptionID: subscription.ID,
		Cursor:         subscription.Cursor,
	}); err != nil {
		return
	}
	for _, event := range subscription.Replay {
		if err := WriteAgentFrame(conn, AgentEventFrame{
			Version:        AgentProtocolVersion,
			SubscriptionID: subscription.ID,
			Cursor:         event.Cursor,
			Event:          event,
		}); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err, ok := <-subscription.Errors:
			if ok && err != nil {
				return
			}
			if !ok {
				return
			}
		case event, ok := <-subscription.Events:
			if !ok {
				return
			}
			if err := WriteAgentFrame(conn, AgentEventFrame{
				Version:        AgentProtocolVersion,
				SubscriptionID: subscription.ID,
				Cursor:         event.Cursor,
				Event:          event,
			}); err != nil {
				return
			}
		}
	}
}

func (s *WechatAgentServer) trackConnection(conn net.Conn, add bool) {
	s.mu.Lock()
	if add {
		s.conns[conn] = struct{}{}
	} else {
		delete(s.conns, conn)
	}
	s.mu.Unlock()
}

func (s *WechatAgentServer) closeTransport() {
	s.mu.Lock()
	listener := s.listener
	connections := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		connections = append(connections, conn)
	}
	s.mu.Unlock()
	if listener != nil {
		_ = listener.Close()
	}
	for _, conn := range connections {
		_ = conn.Close()
	}
}

func writeWechatAgentSuccess(writer io.Writer, requestID string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return WriteAgentFrame(writer, AgentResponse{
		Version: AgentProtocolVersion,
		ID:      requestID,
		OK:      true,
		Body:    raw,
	})
}

func writeWechatAgentError(writer io.Writer, requestID string, err error) error {
	agentError := normalizeWechatAgentServerError(err)
	return WriteAgentFrame(writer, AgentResponse{
		Version: AgentProtocolVersion,
		ID:      requestID,
		OK:      false,
		Error:   agentError,
	})
}

func normalizeWechatAgentServerError(err error) *AgentError {
	var agentError *AgentError
	if errors.As(err, &agentError) {
		return &AgentError{
			Code: agentError.Code, Message: agentError.Message, Retryable: agentError.Retryable,
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &AgentError{
			Code: AgentErrorCodeDeadlineExceeded, Message: "operation deadline exceeded", Retryable: true,
		}
	case errors.Is(err, context.Canceled):
		return &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "operation was canceled", Retryable: true,
		}
	case errors.Is(err, ErrWechatAgentEventStoreClosed), errors.Is(err, net.ErrClosed):
		return &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent resource is unavailable", Retryable: true,
		}
	default:
		return &AgentError{
			Code: AgentErrorCodeInternal, Message: "wechat agent operation failed",
		}
	}
}
