package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestUnixAgentClientUnaryRoundTripAndResponseMatching(t *testing.T) {
	t.Run("successful response", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			if request.Op != AgentOperationHealthGet {
				return fmt.Errorf("operation = %q, want %q", request.Op, AgentOperationHealthGet)
			}
			var empty AgentEmpty
			if err := DecodeAgentBody(request.Body, &empty); err != nil {
				return fmt.Errorf("decode request body: %w", err)
			}
			return WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      true,
				Body: mustAgentJSON(t, AgentHealth{
					Status:          "ready",
					Ready:           true,
					PID:             123,
					ProtocolVersion: AgentProtocolVersion,
				}),
			})
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		health, err := client.Health(ctx)
		if err != nil {
			t.Fatalf("Health() error = %v", err)
		}
		if health.Status != "ready" || !health.Ready || health.PID != 123 {
			t.Fatalf("Health() = %#v", health)
		}
		peer.wait(t)
	})

	t.Run("null success body", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			return WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      true,
				Body:    mustAgentJSON(t, nil),
			})
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		_, err := client.Health(context.Background())
		assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
		peer.wait(t)
	})

	t.Run("mismatched response id", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			return WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID + "-other",
				OK:      true,
				Body:    mustAgentJSON(t, AgentHealth{Status: "ready"}),
			})
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		_, err := client.Health(context.Background())
		assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
		peer.wait(t)
	})

	t.Run("closed remote error", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			return WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      false,
				Error: &AgentError{
					Code:      AgentErrorCodeUnavailable,
					Message:   "temporarily unavailable",
					Retryable: true,
				},
			})
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		_, err := client.Health(context.Background())
		assertAgentErrorCode(t, err, AgentErrorCodeUnavailable)
		peer.wait(t)
	})

	t.Run("oversized attachment response", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			return WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      true,
				Body: mustAgentJSON(t, AgentAttachmentChunk{
					AttachmentID: "attachment-1",
					Data:         make([]byte, AgentMaxAttachmentChunkBytes+1),
				}),
			})
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		_, err := client.ReadAttachmentChunk(context.Background(), AgentAttachmentChunkRequest{
			AttachmentID: "attachment-1",
			Length:       AgentMaxAttachmentChunkBytes,
		})
		assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
		peer.wait(t)
	})
}

func TestUnixAgentClientTypedUnaryOperations(t *testing.T) {
	controlled := validAgentControlledSendRequest(t)
	frida := FridaStatus{State: "attached", Type: "local", Attached: true}
	tests := []struct {
		name      string
		operation AgentOperation
		response  any
		validate  func(AgentRequest) error
		invoke    func(AgentClient) (any, error)
	}{
		{
			name:      "search contacts",
			operation: AgentOperationContactsSearch,
			response: AgentContactSearchResult{
				Total: 1, Contacts: []Contact{{Username: "wxid-a"}}, Limit: 10,
			},
			validate: func(request AgentRequest) error {
				var body AgentContactSearchRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.Keyword != "alice" || body.Limit != 10 {
					return fmt.Errorf("search body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.SearchContacts(context.Background(), AgentContactSearchRequest{
					Keyword: "alice",
					Limit:   10,
				})
			},
		},
		{
			name:      "get contact",
			operation: AgentOperationContactsGet,
			response:  Contact{Username: "wxid-a", NickName: "Alice"},
			validate: func(request AgentRequest) error {
				var body AgentContactGetRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.Username != "wxid-a" {
					return fmt.Errorf("contact body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.Contact(context.Background(), "wxid-a")
			},
		},
		{
			name:      "refresh contacts",
			operation: AgentOperationContactsRefresh,
			response:  AgentContactCounts{Contacts: 2, Groups: 1, Members: 3},
			invoke: func(client AgentClient) (any, error) {
				return client.RefreshContacts(context.Background())
			},
		},
		{
			name:      "list groups",
			operation: AgentOperationGroupsList,
			response:  []Group{{Username: "group@chatroom", MemberCount: 2}},
			invoke: func(client AgentClient) (any, error) {
				return client.Groups(context.Background())
			},
		},
		{
			name:      "group members",
			operation: AgentOperationGroupsMembers,
			response:  []GroupMember{{Username: "wxid-a", DisplayName: "Alice"}},
			validate: func(request AgentRequest) error {
				var body AgentGroupMembersRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.GroupID != "group@chatroom" {
					return fmt.Errorf("group member body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.GroupMembers(context.Background(), "group@chatroom")
			},
		},
		{
			name:      "frida status",
			operation: AgentOperationFridaStatus,
			response:  frida,
			invoke: func(client AgentClient) (any, error) {
				return client.FridaStatus(context.Background())
			},
		},
		{
			name:      "frida initialize",
			operation: AgentOperationFridaInitialize,
			response:  frida,
			validate: func(request AgentRequest) error {
				var body FridaInitializeRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.Type != "local" || body.WechatPID != 123 {
					return fmt.Errorf("frida initialize body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.InitializeFrida(context.Background(), FridaInitializeRequest{
					Type:      "local",
					WechatPID: 123,
				})
			},
		},
		{
			name:      "frida rescan",
			operation: AgentOperationFridaRescan,
			response:  frida,
			invoke: func(client AgentClient) (any, error) {
				return client.RescanFrida(context.Background())
			},
		},
		{
			name:      "frida detach",
			operation: AgentOperationFridaDetach,
			response:  FridaStatus{State: "detached", Type: "local"},
			invoke: func(client AgentClient) (any, error) {
				return client.DetachFrida(context.Background())
			},
		},
		{
			name:      "controlled send",
			operation: AgentOperationMessageSendControlled,
			response: AutomationDeliveryResult{
				Status:            AutomationDeliverySent,
				DispatchAttempted: true,
			},
			validate: func(request AgentRequest) error {
				var body AgentControlledSendRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.IdempotencyKey != controlled.IdempotencyKey ||
					body.Authorization.ContentSHA256 != controlled.Authorization.ContentSHA256 {
					return fmt.Errorf("controlled send body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.SendControlled(context.Background(), controlled)
			},
		},
		{
			name:      "describe attachment",
			operation: AgentOperationAttachmentDescribe,
			response:  AgentAttachment{ID: "attachment-1", Size: 3},
			validate: func(request AgentRequest) error {
				var body AgentAttachmentDescribeRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.AttachmentID != "attachment-1" {
					return fmt.Errorf("attachment describe body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.DescribeAttachment(context.Background(), "attachment-1")
			},
		},
		{
			name:      "read attachment chunk",
			operation: AgentOperationAttachmentReadChunk,
			response: AgentAttachmentChunk{
				AttachmentID: "attachment-1", Offset: 2, Data: []byte("abc"), EOF: true,
			},
			validate: func(request AgentRequest) error {
				var body AgentAttachmentChunkRequest
				if err := DecodeAgentBody(request.Body, &body); err != nil {
					return err
				}
				if body.AttachmentID != "attachment-1" || body.Offset != 2 || body.Length != 3 {
					return fmt.Errorf("attachment chunk body = %#v", body)
				}
				return nil
			},
			invoke: func(client AgentClient) (any, error) {
				return client.ReadAttachmentChunk(context.Background(), AgentAttachmentChunkRequest{
					AttachmentID: "attachment-1",
					Offset:       2,
					Length:       3,
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
				request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
				if err != nil {
					return err
				}
				if request.Op != test.operation {
					return fmt.Errorf("operation = %q, want %q", request.Op, test.operation)
				}
				if test.validate != nil {
					if err := test.validate(request); err != nil {
						return err
					}
				}
				return WriteAgentFrame(conn, AgentResponse{
					Version: AgentProtocolVersion,
					ID:      request.ID,
					OK:      true,
					Body:    mustAgentJSON(t, test.response),
				})
			})
			client := NewUnixAgentClient(peer.socketPath)
			t.Cleanup(func() { _ = client.Close() })

			result, err := test.invoke(client)
			if err != nil {
				t.Fatalf("typed unary call error = %v", err)
			}
			if !reflect.DeepEqual(result, test.response) {
				t.Fatalf("typed unary result = %#v, want %#v", result, test.response)
			}
			peer.wait(t)
		})
	}
}

func TestUnixAgentClientControlledSendResponseValidation(t *testing.T) {
	tests := []struct {
		name              string
		status            AutomationDeliveryStatus
		dispatchAttempted bool
		wantError         bool
	}{
		{"unknown status", AutomationDeliveryStatus("queued"), false, true},
		{"sent without dispatch", AutomationDeliverySent, false, true},
		{"unknown without dispatch", AutomationDeliveryUnknown, false, true},
		{"failed without dispatch", AutomationDeliveryFailed, false, false},
		{"failed after dispatch", AutomationDeliveryFailed, true, false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
				request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
				if err != nil {
					return err
				}
				if request.Op != AgentOperationMessageSendControlled {
					return fmt.Errorf("operation = %q, want message.send_controlled", request.Op)
				}
				return WriteAgentFrame(conn, AgentResponse{
					Version: AgentProtocolVersion,
					ID:      request.ID,
					OK:      true,
					Body: mustAgentJSON(t, AutomationDeliveryResult{
						Status:            test.status,
						DispatchAttempted: test.dispatchAttempted,
					}),
				})
			})
			client := NewUnixAgentClient(peer.socketPath)
			t.Cleanup(func() { _ = client.Close() })

			result, err := client.SendControlled(context.Background(), validAgentControlledSendRequest(t))
			if test.wantError {
				assertAmbiguousAgentDelivery(t, result, err)
			} else {
				if err != nil {
					t.Fatalf("SendControlled() error = %v", err)
				}
				if result.Status != test.status || result.DispatchAttempted != test.dispatchAttempted {
					t.Fatalf("SendControlled() = %#v", result)
				}
			}
			peer.wait(t)
		})
	}
}

func TestUnixAgentClientControlledSendPostWriteFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler func(*net.UnixConn, AgentRequest) error
	}{
		{
			name: "peer disconnects after complete request",
			handler: func(_ *net.UnixConn, _ AgentRequest) error {
				return nil
			},
		},
		{
			name: "response id mismatch",
			handler: func(conn *net.UnixConn, request AgentRequest) error {
				return WriteAgentFrame(conn, AgentResponse{
					Version: AgentProtocolVersion,
					ID:      request.ID + "-other",
					OK:      true,
					Body: mustAgentJSON(t, AutomationDeliveryResult{
						Status:            AutomationDeliverySent,
						DispatchAttempted: true,
					}),
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
				request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
				if err != nil {
					return err
				}
				if request.Op != AgentOperationMessageSendControlled {
					return fmt.Errorf("operation = %q, want message.send_controlled", request.Op)
				}
				return test.handler(conn, request)
			})
			client := NewUnixAgentClient(peer.socketPath)
			t.Cleanup(func() { _ = client.Close() })

			result, err := client.SendControlled(context.Background(), validAgentControlledSendRequest(t))
			assertAmbiguousAgentDelivery(t, result, err)
			peer.wait(t)
		})
	}
}

func TestUnixAgentClientDescribeAttachmentResponseValidation(t *testing.T) {
	createdAt := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	zeroTime := time.Time{}
	beforeCreated := createdAt.Add(-time.Second)
	tests := []struct {
		name     string
		response AgentAttachment
	}{
		{"mismatched id", AgentAttachment{ID: "attachment-2", Size: 3}},
		{"empty id", AgentAttachment{Size: 3}},
		{"path id", AgentAttachment{ID: "../attachment-1", Size: 3}},
		{"URI id", AgentAttachment{ID: "mailto:attachment-1", Size: 3}},
		{"negative size", AgentAttachment{ID: "attachment-1", Size: -1}},
		{"invalid sha256", AgentAttachment{ID: "attachment-1", Size: 3, SHA256: "abc"}},
		{"zero expiry", AgentAttachment{ID: "attachment-1", Size: 3, ExpiresAt: &zeroTime}},
		{"expiry before creation", AgentAttachment{
			ID: "attachment-1", Size: 3, CreatedAt: createdAt, ExpiresAt: &beforeCreated,
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
				request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
				if err != nil {
					return err
				}
				return WriteAgentFrame(conn, AgentResponse{
					Version: AgentProtocolVersion,
					ID:      request.ID,
					OK:      true,
					Body:    mustAgentJSON(t, test.response),
				})
			})
			client := NewUnixAgentClient(peer.socketPath)
			t.Cleanup(func() { _ = client.Close() })

			_, err := client.DescribeAttachment(context.Background(), "attachment-1")
			assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
			peer.wait(t)
		})
	}
}

func TestUnixAgentClientAttachmentChunkResponseValidation(t *testing.T) {
	request := AgentAttachmentChunkRequest{
		AttachmentID: "attachment-1",
		Offset:       4,
		Length:       2,
	}
	tests := []struct {
		name     string
		response AgentAttachmentChunk
	}{
		{
			name: "mismatched attachment id",
			response: AgentAttachmentChunk{
				AttachmentID: "attachment-2",
				Offset:       request.Offset,
				Data:         []byte("ab"),
			},
		},
		{
			name: "mismatched offset",
			response: AgentAttachmentChunk{
				AttachmentID: request.AttachmentID,
				Offset:       request.Offset + 1,
				Data:         []byte("ab"),
			},
		},
		{
			name: "data exceeds requested length",
			response: AgentAttachmentChunk{
				AttachmentID: request.AttachmentID,
				Offset:       request.Offset,
				Data:         []byte("abc"),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
				agentRequest, err := ReadAgentRequestFrame(bufio.NewReader(conn))
				if err != nil {
					return err
				}
				return WriteAgentFrame(conn, AgentResponse{
					Version: AgentProtocolVersion,
					ID:      agentRequest.ID,
					OK:      true,
					Body:    mustAgentJSON(t, test.response),
				})
			})
			client := NewUnixAgentClient(peer.socketPath)
			t.Cleanup(func() { _ = client.Close() })

			_, err := client.ReadAttachmentChunk(context.Background(), request)
			assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
			peer.wait(t)
		})
	}
}

func TestUnixAgentClientContextCancellation(t *testing.T) {
	requestSeen := make(chan struct{})
	releasePeer := make(chan struct{})
	peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
		if _, err := ReadAgentRequestFrame(bufio.NewReader(conn)); err != nil {
			return err
		}
		close(requestSeen)
		<-releasePeer
		return nil
	})
	client := NewUnixAgentClient(peer.socketPath)
	t.Cleanup(func() { _ = client.Close() })

	if _, err := client.Health(nil); err == nil {
		t.Fatal("Health(nil) did not reject a nil context")
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.Health(ctx)
		result <- err
	}()

	select {
	case <-requestSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("fake peer did not receive request")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Health() cancellation error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Health() did not return after context cancellation")
	}
	close(releasePeer)
	peer.wait(t)
}

func TestUnixAgentClientEventSubscriptionReplayAndCursorErrors(t *testing.T) {
	t.Run("replay remains ordered", func(t *testing.T) {
		eventsWritten := make(chan struct{})
		releasePeer := make(chan struct{})
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			if request.Op != AgentOperationEventsSubscribe {
				return fmt.Errorf("operation = %q, want events.subscribe", request.Op)
			}
			var subscribe AgentEventsSubscribeRequest
			if err := DecodeAgentBody(request.Body, &subscribe); err != nil {
				return err
			}
			if subscribe.AfterCursor != "5" {
				return fmt.Errorf("after_cursor = %q, want 5", subscribe.AfterCursor)
			}
			if err := WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      true,
				Body: mustAgentJSON(t, AgentEventSubscription{
					SubscriptionID: "sub-1",
					Cursor:         "5",
				}),
			}); err != nil {
				return err
			}
			for _, cursor := range []string{"6", "7"} {
				if err := WriteAgentFrame(conn, agentTestEventFrame("sub-1", cursor)); err != nil {
					return err
				}
			}
			close(eventsWritten)
			<-releasePeer
			return nil
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })
		ctx, cancel := context.WithCancel(context.Background())

		events, errs, err := client.SubscribeEvents(ctx, "5")
		if err != nil {
			t.Fatalf("SubscribeEvents() error = %v", err)
		}
		<-eventsWritten
		for _, wantCursor := range []string{"6", "7"} {
			select {
			case event, ok := <-events:
				if !ok {
					t.Fatalf("events closed before cursor %s", wantCursor)
				}
				if event.Cursor != wantCursor || event.MessageKey != "message-"+wantCursor {
					t.Fatalf("event = %#v, want cursor %s", event, wantCursor)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for cursor %s", wantCursor)
			}
		}
		cancel()
		close(releasePeer)
		assertAgentChannelClosesWithoutError(t, events, errs)
		peer.wait(t)
	})

	t.Run("cursor expired handshake", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			return WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      false,
				Error: &AgentError{
					Code:    AgentErrorCodeCursorExpired,
					Message: "cursor is older than retained history",
				},
			})
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		events, errs, err := client.SubscribeEvents(context.Background(), "1")
		if events != nil || errs != nil {
			t.Fatal("SubscribeEvents() returned channels for a rejected cursor")
		}
		assertAgentErrorCode(t, err, AgentErrorCodeCursorExpired)
		peer.wait(t)
	})

	t.Run("invalid cursor is rejected before dialing", func(t *testing.T) {
		client := NewUnixAgentClient("/tmp/unused-agent-contract.sock")
		t.Cleanup(func() { _ = client.Close() })
		events, errs, err := client.SubscribeEvents(context.Background(), "01")
		if events != nil || errs != nil {
			t.Fatal("SubscribeEvents() returned channels for an invalid cursor")
		}
		assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
	})

	t.Run("handshake cursor matches requested baseline", func(t *testing.T) {
		tests := []struct {
			name           string
			afterCursor    string
			returnedCursor string
			wantError      bool
		}{
			{"requested 5 returned 10", "5", "10", true},
			{"empty requested returned nonzero", "", "1", true},
			{"empty requested returned zero", "", "0", false},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				releasePeer := make(chan struct{})
				peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
					request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
					if err != nil {
						return err
					}
					if err := WriteAgentFrame(conn, AgentResponse{
						Version: AgentProtocolVersion,
						ID:      request.ID,
						OK:      true,
						Body: mustAgentJSON(t, AgentEventSubscription{
							SubscriptionID: "sub-1",
							Cursor:         test.returnedCursor,
						}),
					}); err != nil {
						return err
					}
					if !test.wantError {
						<-releasePeer
					}
					return nil
				})
				client := NewUnixAgentClient(peer.socketPath)
				t.Cleanup(func() { _ = client.Close() })
				ctx, cancel := context.WithCancel(context.Background())

				events, errs, err := client.SubscribeEvents(ctx, test.afterCursor)
				if test.wantError {
					cancel()
					if events != nil || errs != nil {
						t.Fatal("SubscribeEvents() returned channels for a mismatched handshake cursor")
					}
					assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
				} else {
					if err != nil {
						t.Fatalf("SubscribeEvents() error = %v", err)
					}
					cancel()
					close(releasePeer)
					assertAgentChannelClosesWithoutError(t, events, errs)
				}
				peer.wait(t)
			})
		}
	})

	t.Run("duplicate stream cursor is rejected", func(t *testing.T) {
		peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
			request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
			if err != nil {
				return err
			}
			if err := WriteAgentFrame(conn, AgentResponse{
				Version: AgentProtocolVersion,
				ID:      request.ID,
				OK:      true,
				Body: mustAgentJSON(t, AgentEventSubscription{
					SubscriptionID: "sub-1",
					Cursor:         "5",
				}),
			}); err != nil {
				return err
			}
			if err := WriteAgentFrame(conn, agentTestEventFrame("sub-1", "6")); err != nil {
				return err
			}
			return WriteAgentFrame(conn, agentTestEventFrame("sub-1", "6"))
		})
		client := NewUnixAgentClient(peer.socketPath)
		t.Cleanup(func() { _ = client.Close() })

		events, errs, err := client.SubscribeEvents(context.Background(), "5")
		if err != nil {
			t.Fatalf("SubscribeEvents() error = %v", err)
		}
		select {
		case event := <-events:
			if event.Cursor != "6" {
				t.Fatalf("first cursor = %q, want 6", event.Cursor)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for first event")
		}
		select {
		case err := <-errs:
			assertAgentErrorCode(t, err, AgentErrorCodeInvalidFrame)
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for ordering error")
		}
		peer.wait(t)
	})
}

func TestAgentCursorSuccessorRejectsGapsAndAcceptsCarry(t *testing.T) {
	tests := []struct {
		previous string
		current  string
		want     bool
	}{
		{"8", "9", true},
		{"9", "10", true},
		{"9", "11", false},
		{"10", "12", false},
	}
	for _, test := range tests {
		if got := isAgentCursorSuccessor(test.previous, test.current); got != test.want {
			t.Errorf("isAgentCursorSuccessor(%q, %q) = %t, want %t", test.previous, test.current, got, test.want)
		}
	}
}

func TestUnixAgentClientCloseUnblocksFullEventChannel(t *testing.T) {
	client := NewUnixAgentClient("/tmp/unused-agent-full-events.sock").(*unixAgentClient)
	events := make(chan AgentInboundEvent, 1)
	events <- AgentInboundEvent{MessageKey: "already-buffered"}
	errs := make(chan error, 1)

	var wire bytes.Buffer
	if err := WriteAgentFrame(&wire, agentTestEventFrame("sub-1", "1")); err != nil {
		t.Fatalf("WriteAgentFrame() error = %v", err)
	}
	go client.streamEvents(
		context.Background(),
		nil,
		func() {},
		bufio.NewReader(&wire),
		AgentEventSubscription{SubscriptionID: "sub-1", Cursor: "0"},
		events,
		errs,
	)

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	select {
	case err, ok := <-errs:
		if ok {
			t.Fatalf("errors channel returned %v, want closed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close() did not unblock the full event stream")
	}
	if event := <-events; event.MessageKey != "already-buffered" {
		t.Fatalf("buffered event = %#v", event)
	}
	select {
	case event, ok := <-events:
		if ok {
			t.Fatalf("events channel returned %#v after Close()", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("events channel did not close after Close()")
	}
}

func TestUnixAgentClientCloseIsIdempotentAndStopsActiveCalls(t *testing.T) {
	idle := NewUnixAgentClient("/tmp/unused-agent-close.sock")
	if err := idle.Close(); err != nil {
		t.Fatalf("first idle Close() error = %v", err)
	}
	if err := idle.Close(); err != nil {
		t.Fatalf("second idle Close() error = %v", err)
	}

	requestSeen := make(chan struct{})
	releasePeer := make(chan struct{})
	peer := newAgentTestPeer(t, 1, func(_ int, conn *net.UnixConn) error {
		if _, err := ReadAgentRequestFrame(bufio.NewReader(conn)); err != nil {
			return err
		}
		close(requestSeen)
		<-releasePeer
		return nil
	})
	client := NewUnixAgentClient(peer.socketPath)
	result := make(chan error, 1)
	go func() {
		_, err := client.Health(context.Background())
		result <- err
	}()

	select {
	case <-requestSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("fake peer did not receive active request")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("first active Close() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second active Close() error = %v", err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("active call error = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("active call did not stop after Close()")
	}
	close(releasePeer)
	peer.wait(t)

	if _, err := client.Health(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Health() after Close() error = %v, want net.ErrClosed", err)
	}
}

func TestUnixAgentClientConcurrentCalls(t *testing.T) {
	const callCount = 24
	var requestIDs sync.Map
	peer := newAgentTestPeer(t, callCount, func(index int, conn *net.UnixConn) error {
		request, err := ReadAgentRequestFrame(bufio.NewReader(conn))
		if err != nil {
			return err
		}
		if request.Op != AgentOperationHealthGet {
			return fmt.Errorf("operation = %q, want health.get", request.Op)
		}
		if _, loaded := requestIDs.LoadOrStore(request.ID, struct{}{}); loaded {
			return fmt.Errorf("duplicate request id %q", request.ID)
		}
		return WriteAgentFrame(conn, AgentResponse{
			Version: AgentProtocolVersion,
			ID:      request.ID,
			OK:      true,
			Body: mustAgentJSON(t, AgentHealth{
				Status:          "ready",
				Ready:           true,
				PID:             index + 1,
				ProtocolVersion: AgentProtocolVersion,
			}),
		})
	})
	client := NewUnixAgentClient(peer.socketPath)
	t.Cleanup(func() { _ = client.Close() })

	var wg sync.WaitGroup
	errs := make(chan error, callCount)
	for range callCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			health, err := client.Health(ctx)
			if err != nil {
				errs <- err
				return
			}
			if health.Status != "ready" || !health.Ready {
				errs <- fmt.Errorf("unexpected health response %#v", health)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Health() error = %v", err)
	}
	peer.wait(t)

	unique := 0
	requestIDs.Range(func(_, _ any) bool {
		unique++
		return true
	})
	if unique != callCount {
		t.Fatalf("unique request IDs = %d, want %d", unique, callCount)
	}
}

type agentTestPeer struct {
	socketPath string
	listener   *net.UnixListener
	done       chan error
	waitOnce   sync.Once
	waitErr    error
}

func newAgentTestPeer(
	t *testing.T,
	connectionCount int,
	handler func(int, *net.UnixConn) error,
) *agentTestPeer {
	t.Helper()
	parent, err := os.MkdirTemp("/tmp", "agent-contract-peer-")
	if err != nil {
		t.Fatalf("os.MkdirTemp() error = %v", err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		os.RemoveAll(parent)
		t.Fatalf("chmod fake peer parent: %v", err)
	}
	socketPath := filepath.Join(parent, "agent.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		os.RemoveAll(parent)
		t.Fatalf("net.ListenUnix() error = %v", err)
	}
	if err := os.Chmod(socketPath, 0600); err != nil {
		listener.Close()
		os.RemoveAll(parent)
		t.Fatalf("chmod fake peer socket: %v", err)
	}
	if err := listener.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		listener.Close()
		os.RemoveAll(parent)
		t.Fatalf("set fake peer deadline: %v", err)
	}

	peer := &agentTestPeer{
		socketPath: socketPath,
		listener:   listener,
		done:       make(chan error, 1),
	}
	go func() {
		handlerErrors := make(chan error, connectionCount)
		var handlers sync.WaitGroup
		var acceptErr error
		for index := 0; index < connectionCount; index++ {
			conn, err := listener.AcceptUnix()
			if err != nil {
				acceptErr = err
				break
			}
			handlers.Add(1)
			go func(index int, conn *net.UnixConn) {
				defer handlers.Done()
				defer conn.Close()
				handlerErrors <- handler(index, conn)
			}(index, conn)
		}
		handlers.Wait()
		close(handlerErrors)
		if acceptErr != nil {
			peer.done <- acceptErr
			return
		}
		for err := range handlerErrors {
			if err != nil {
				peer.done <- err
				return
			}
		}
		peer.done <- nil
	}()

	t.Cleanup(func() {
		_ = listener.Close()
		peer.waitNoFail()
		_ = os.RemoveAll(parent)
	})
	return peer
}

func (p *agentTestPeer) wait(t *testing.T) {
	t.Helper()
	p.waitNoFail()
	if p.waitErr != nil {
		t.Fatalf("fake peer error = %v", p.waitErr)
	}
}

func (p *agentTestPeer) waitNoFail() {
	p.waitOnce.Do(func() {
		p.waitErr = <-p.done
		_ = p.listener.Close()
	})
}

func agentTestEventFrame(subscriptionID, cursor string) AgentEventFrame {
	return AgentEventFrame{
		Version:        AgentProtocolVersion,
		SubscriptionID: subscriptionID,
		Cursor:         cursor,
		Event: AgentInboundEvent{
			MessageKey: "message-" + cursor,
			ChatType:   ChatTypePrivate,
			PeerID:     "wxid-peer",
			AuthorID:   "wxid-peer",
			Text:       "event " + cursor,
		},
	}
}

func assertAgentChannelClosesWithoutError(
	t *testing.T,
	events <-chan AgentInboundEvent,
	errs <-chan error,
) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for events != nil || errs != nil {
		select {
		case _, ok := <-events:
			if !ok {
				events = nil
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if err != nil {
				t.Fatalf("event stream cancellation error = %v", err)
			}
		case <-deadline:
			t.Fatal("event subscription channels did not close")
		}
	}
}

func assertAmbiguousAgentDelivery(t *testing.T, result AutomationDeliveryResult, err error) {
	t.Helper()
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("SendControlled() error = %v, want *AgentError", err)
	}
	if agentErr.Code != AgentErrorCodeDeliveryAmbiguous {
		t.Fatalf("AgentError.Code = %q, want %q", agentErr.Code, AgentErrorCodeDeliveryAmbiguous)
	}
	if agentErr.Retryable {
		t.Fatal("delivery_ambiguous error is retryable")
	}
	if result.Status != AutomationDeliveryUnknown || !result.DispatchAttempted {
		t.Fatalf("SendControlled() = %#v, want unknown dispatch-attempted result", result)
	}
}
