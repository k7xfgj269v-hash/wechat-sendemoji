package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type task003LifecycleAgentClient struct {
	task003AgentClient

	lifecycleMu               sync.Mutex
	subscriptionContext       context.Context
	subscriptionStarted       chan struct{}
	subscriptionStartedOnce   sync.Once
	closeObservedCancellation bool
}

func (c *task003LifecycleAgentClient) SubscribeEvents(
	ctx context.Context,
	afterCursor string,
) (<-chan AgentInboundEvent, <-chan error, error) {
	c.task003AgentClient.mu.Lock()
	c.task003AgentClient.calls = append(c.task003AgentClient.calls, "events.subscribe")
	c.task003AgentClient.subscribeFrom = append(
		c.task003AgentClient.subscribeFrom,
		afterCursor,
	)
	c.task003AgentClient.mu.Unlock()

	c.lifecycleMu.Lock()
	c.subscriptionContext = ctx
	c.lifecycleMu.Unlock()
	c.subscriptionStartedOnce.Do(func() {
		close(c.subscriptionStarted)
	})
	return make(chan AgentInboundEvent), make(chan error), nil
}

func (c *task003LifecycleAgentClient) Close() error {
	c.lifecycleMu.Lock()
	c.closeObservedCancellation =
		c.subscriptionContext != nil && c.subscriptionContext.Err() != nil
	c.lifecycleMu.Unlock()
	c.task003AgentClient.record("close")
	return c.task003AgentClient.closeErr
}

func TestRunHRWorkbenchCancellationNeverMutatesFridaOrWechat(t *testing.T) {
	client := &task003LifecycleAgentClient{
		subscriptionStarted: make(chan struct{}),
	}

	runDir, err := os.MkdirTemp("/tmp", "task003-hr-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runDir) })
	cfg := &Config{
		ReceiveHost: "127.0.0.1:0",
		CRMDataPath: filepath.Join(runDir, "crm.json"),
		RunDir:      runDir,
		AgentSocket: filepath.Join(runDir, "wechat-agent.sock"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- RunHRWorkbenchWithDependencies(ctx, cfg, HRWorkbenchProcessDependencies{
			Agent:  client,
			Listen: net.Listen,
			OpenCRM: func(path string) (*CRMStore, error) {
				return OpenCRMStore(path)
			},
			InitializeAutomation: func(*Config, *CRMStore) (*AutomationRuntime, error) {
				return nil, nil
			},
			OpenCursor: OpenHRWorkbenchAgentCursorStore,
		})
	}()

	select {
	case <-client.subscriptionStarted:
	case err := <-done:
		t.Fatalf("RunHRWorkbenchWithDependencies() returned before startup: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("HR workbench did not start its synthetic event subscription")
	}

	for _, call := range []string{
		"frida.initialize",
		"frida.rescan",
		"frida.detach",
		"message.send_controlled",
		"contacts.refresh",
	} {
		if got := client.callCount(call); got != 0 {
			t.Fatalf("%s calls during startup = %d, want 0", call, got)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunHRWorkbenchWithDependencies() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HR workbench did not stop after cancellation")
	}

	if client.callCount("events.subscribe") != 1 {
		t.Fatalf("events.subscribe calls = %d, want 1", client.callCount("events.subscribe"))
	}
	if client.callCount("close") != 1 {
		t.Fatalf("client Close calls = %d, want 1", client.callCount("close"))
	}
	client.lifecycleMu.Lock()
	closeObservedCancellation := client.closeObservedCancellation
	client.lifecycleMu.Unlock()
	if !closeObservedCancellation {
		t.Fatal("AgentClient.Close ran before the event subscription was canceled")
	}
	for _, call := range []string{
		"frida.initialize",
		"frida.rescan",
		"frida.detach",
		"message.send_controlled",
	} {
		if got := client.callCount(call); got != 0 {
			t.Fatalf("%s calls during cancellation = %d, want 0", call, got)
		}
	}
}

func TestRunHRWorkbenchRejectsNonLoopbackBeforeOpeningResources(t *testing.T) {
	client := &task003AgentClient{}
	cfg := &Config{
		ReceiveHost: "0.0.0.0:58080",
		RunDir:      t.TempDir(),
	}
	err := RunHRWorkbenchWithDependencies(
		context.Background(),
		cfg,
		HRWorkbenchProcessDependencies{Agent: client},
	)
	if err == nil {
		t.Fatal("RunHRWorkbenchWithDependencies() succeeded for a non-loopback listener")
	}
	if client.callCount("close") != 1 {
		t.Fatalf("client Close calls = %d, want 1", client.callCount("close"))
	}
	for _, call := range []string{
		"events.subscribe",
		"frida.initialize",
		"frida.rescan",
		"frida.detach",
		"message.send_controlled",
	} {
		if got := client.callCount(call); got != 0 {
			t.Fatalf("%s calls = %d, want 0", call, got)
		}
	}
}
