package main

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWechatAgentServerDispatchesSyntheticRuntime(t *testing.T) {
	socketPath, runtime, events := startSyntheticWechatAgentServer(t)
	client := NewUnixAgentClient(socketPath)
	t.Cleanup(func() { _ = client.Close() })

	health, err := client.Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !health.Ready || health.PID != 123 || health.ProtocolVersion != AgentProtocolVersion {
		t.Fatalf("Health() = %#v", health)
	}
	search, err := client.SearchContacts(
		context.Background(),
		AgentContactSearchRequest{Keyword: "alice"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if search.Total != 1 || search.Contacts[0].Username != "wxid-alice" {
		t.Fatalf("SearchContacts() = %#v", search)
	}

	if _, _, err := events.Append(syntheticInboundEvent(1)); err != nil {
		t.Fatal(err)
	}
	subscriptionCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventCh, errorCh, err := client.SubscribeEvents(subscriptionCtx, "0")
	if err != nil {
		t.Fatal(err)
	}
	if event := <-eventCh; event.Cursor != "1" {
		t.Fatalf("replayed event = %#v", event)
	}
	if _, _, err := events.Append(syntheticInboundEvent(2)); err != nil {
		t.Fatal(err)
	}
	if event := <-eventCh; event.Cursor != "2" {
		t.Fatalf("live event = %#v", event)
	}
	select {
	case err := <-errorCh:
		if err != nil {
			t.Fatalf("subscription error = %v", err)
		}
	default:
	}
	if runtime == nil {
		t.Fatal("runtime is nil")
	}
}

func TestWechatAgentServerReturnsStableProtocolErrors(t *testing.T) {
	socketPath, _, _ := startSyntheticWechatAgentServer(t)
	tests := []struct {
		name string
		wire string
		code string
	}{
		{
			name: "unsupported version",
			wire: `{"version":"wechat-agent.v1","id":"req-version","op":"health.get","body":{}}`,
			code: AgentErrorCodeUnsupportedVersion,
		},
		{
			name: "unsupported operation",
			wire: `{"version":"wechat-agent.v2","id":"req-op","op":"shell.exec","body":{}}`,
			code: AgentErrorCodeUnsupportedOperation,
		},
		{
			name: "unknown body field",
			wire: `{"version":"wechat-agent.v2","id":"req-body","op":"health.get","body":{"extra":true}}`,
			code: AgentErrorCodeInvalidFrame,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if _, err := conn.Write([]byte(test.wire + "\n")); err != nil {
				t.Fatal(err)
			}
			response, err := ReadAgentResponseFrame(bufio.NewReader(conn))
			if err != nil {
				t.Fatal(err)
			}
			if response.OK || response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("response = %#v, want code %q", response, test.code)
			}
		})
	}
}

func startSyntheticWechatAgentServer(
	t *testing.T,
) (string, *WechatAgentRuntime, *WechatAgentEventStore) {
	t.Helper()
	parent, err := os.MkdirTemp("/tmp", "wechat-agent-server-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(parent, "agent.sock")
	listener, err := listenWechatAgentUnix(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime, events := newSyntheticWechatAgentRuntime(
		t,
		filepath.Join(parent, "deliveries.json"),
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			return AutomationDeliveryResult{
				Status: AutomationDeliverySent, DispatchAttempted: true,
			}
		}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewWechatAgentServer(runtime).Serve(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve() did not stop")
		}
		_ = runtime.Close()
		_ = os.RemoveAll(parent)
	})
	return socketPath, runtime, events
}
