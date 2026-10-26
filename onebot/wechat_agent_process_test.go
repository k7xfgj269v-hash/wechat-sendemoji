package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type syntheticWechatAgentContacts struct{}

func (syntheticWechatAgentContacts) SearchContacts(
	context.Context,
	AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	return AgentContactSearchResult{
		Total:    1,
		Contacts: []Contact{{Username: "wxid-alice", NickName: "Alice"}},
		Limit:    50,
	}, nil
}

func (syntheticWechatAgentContacts) Contact(context.Context, string) (Contact, error) {
	return Contact{Username: "wxid-alice", NickName: "Alice"}, nil
}

func (syntheticWechatAgentContacts) RefreshContacts(context.Context) (AgentContactCounts, error) {
	return AgentContactCounts{Contacts: 1, Groups: 1, Members: 2}, nil
}

func (syntheticWechatAgentContacts) Groups(context.Context) ([]Group, error) {
	return []Group{{Username: "team@chatroom", MemberCount: 2}}, nil
}

func (syntheticWechatAgentContacts) GroupMembers(context.Context, string) ([]GroupMember, error) {
	return []GroupMember{{Username: "wxid-alice", DisplayName: "Alice"}}, nil
}

func (syntheticWechatAgentContacts) Counts(context.Context) (AgentContactCounts, error) {
	return AgentContactCounts{Contacts: 1, Groups: 1, Members: 2}, nil
}

type syntheticWechatAgentFrida struct {
	status FridaStatus
}

func (f *syntheticWechatAgentFrida) Status() FridaStatus {
	return f.status
}

func (f *syntheticWechatAgentFrida) Initialize(
	_ context.Context,
	request FridaInitializeRequest,
) (FridaStatus, error) {
	f.status = FridaStatus{
		State:        "attached",
		Type:         request.Type,
		WechatPID:    request.WechatPID,
		Attached:     true,
		ScriptLoaded: true,
	}
	return f.status, nil
}

func (f *syntheticWechatAgentFrida) Rescan(context.Context) (FridaStatus, error) {
	return f.status, nil
}

func (f *syntheticWechatAgentFrida) Detach(context.Context) (FridaStatus, error) {
	f.status.Attached = false
	f.status.ScriptLoaded = false
	f.status.State = "detached"
	return f.status, nil
}

type syntheticWechatAgentAttachments struct {
	data map[string][]byte
}

func (a *syntheticWechatAgentAttachments) Describe(
	_ context.Context,
	id string,
) (AgentAttachment, error) {
	data, ok := a.data[id]
	if !ok {
		return AgentAttachment{}, &AgentError{
			Code: AgentErrorCodeNotFound, Message: "attachment was not found",
		}
	}
	return AgentAttachment{ID: id, Size: int64(len(data))}, nil
}

func (a *syntheticWechatAgentAttachments) ReadChunk(
	_ context.Context,
	request AgentAttachmentChunkRequest,
) (AgentAttachmentChunk, error) {
	data := a.data[request.AttachmentID]
	end := request.Offset + int64(request.Length)
	if end > int64(len(data)) {
		end = int64(len(data))
	}
	return AgentAttachmentChunk{
		AttachmentID: request.AttachmentID,
		Offset:       request.Offset,
		Data:         append([]byte(nil), data[request.Offset:end]...),
		EOF:          end == int64(len(data)),
	}, nil
}

func (a *syntheticWechatAgentAttachments) ReadAll(
	_ context.Context,
	id string,
	_ int64,
) ([]byte, error) {
	data, ok := a.data[id]
	if !ok {
		return nil, errors.New("missing attachment")
	}
	return append([]byte(nil), data...), nil
}

func (a *syntheticWechatAgentAttachments) CaptureInbound(
	context.Context,
	*WechatMessage,
) ([]AgentAttachment, error) {
	return nil, nil
}

func (*syntheticWechatAgentAttachments) Close() error {
	return nil
}

type syntheticWechatAgentDeliveryFunc func(
	context.Context,
	AgentControlledSendRequest,
	[]byte,
) AutomationDeliveryResult

func (f syntheticWechatAgentDeliveryFunc) Deliver(
	ctx context.Context,
	request AgentControlledSendRequest,
	data []byte,
) AutomationDeliveryResult {
	return f(ctx, request, data)
}

func newSyntheticWechatAgentRuntime(
	t *testing.T,
	ledgerPath string,
	delivery WechatAgentControlledDelivery,
) (*WechatAgentRuntime, *WechatAgentEventStore) {
	return newSyntheticWechatAgentRuntimeWithAuthorization(
		t,
		ledgerPath,
		delivery,
		nil,
	)
}

func newSyntheticWechatAgentRuntimeWithAuthorization(
	t *testing.T,
	ledgerPath string,
	delivery WechatAgentControlledDelivery,
	authorizer WechatAgentSendAuthorizer,
) (*WechatAgentRuntime, *WechatAgentEventStore) {
	t.Helper()
	events := NewWechatAgentEventStore(16)
	runtime, err := NewWechatAgentRuntime(WechatAgentRuntimeDependencies{
		Contacts: syntheticWechatAgentContacts{},
		Frida: &syntheticWechatAgentFrida{status: FridaStatus{
			State:        "attached",
			Type:         "local",
			Attached:     true,
			ScriptLoaded: true,
		}},
		Attachments:        &syntheticWechatAgentAttachments{data: map[string][]byte{}},
		Delivery:           delivery,
		Authorizer:         authorizer,
		Events:             events,
		DeliveryLedgerPath: ledgerPath,
		PID:                123,
	})
	if err != nil {
		t.Fatalf("NewWechatAgentRuntime() error = %v", err)
	}
	return runtime, events
}

func TestWechatAgentControlledSendReplaysExpiredRequestAcrossPendingAndRestart(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "deliveries.json")
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseDelivery := func() {
		releaseOnce.Do(func() { close(release) })
	}
	t.Cleanup(releaseDelivery)

	var deliveryCalls atomic.Int32
	delivery := syntheticWechatAgentDeliveryFunc(func(
		context.Context,
		AgentControlledSendRequest,
		[]byte,
	) AutomationDeliveryResult {
		if deliveryCalls.Add(1) == 1 {
			close(started)
		}
		<-release
		return AutomationDeliveryResult{
			Status: AutomationDeliverySent, DispatchAttempted: true,
		}
	})
	var authorizationCalls atomic.Int32
	authorizer := WechatAgentSendAuthorizerFunc(func(
		_ context.Context,
		request AgentControlledSendRequest,
	) error {
		authorizationCalls.Add(1)
		return ValidateAgentControlledSendRequest(request)
	})
	runtime, _ := newSyntheticWechatAgentRuntimeWithAuthorization(
		t,
		ledgerPath,
		delivery,
		authorizer,
	)

	request := validAgentControlledSendRequest(t)
	request.IdempotencyKey = "send:" + request.Authorization.ActionID
	type sendOutcome struct {
		result AutomationDeliveryResult
		err    error
	}
	firstOutcome := make(chan sendOutcome, 1)
	go func() {
		result, err := runtime.SendControlled(context.Background(), request)
		firstOutcome <- sendOutcome{result: result, err: err}
	}()
	<-started

	runtime.deliveryMu.Lock()
	pending := runtime.pendingDeliveries[request.IdempotencyKey]
	runtime.deliveryMu.Unlock()
	if pending == nil {
		t.Fatal("controlled send did not create a pending reservation after authorization")
	}

	replay := request
	replay.Authorization.ExpiresAt = time.Now().Add(-time.Hour)
	replayEntered := make(chan struct{})
	replayOutcome := make(chan sendOutcome, 1)
	go func() {
		close(replayEntered)
		result, err := runtime.SendControlled(context.Background(), replay)
		replayOutcome <- sendOutcome{result: result, err: err}
	}()
	<-replayEntered
	select {
	case outcome := <-replayOutcome:
		t.Fatalf("pending replay completed before original delivery: %#v", outcome)
	case <-time.After(100 * time.Millisecond):
	}
	if got := authorizationCalls.Load(); got != 1 {
		t.Fatalf("authorization calls while replay pending = %d, want 1", got)
	}

	releaseDelivery()
	for name, outcomes := range map[string]<-chan sendOutcome{
		"original": firstOutcome,
		"replay":   replayOutcome,
	} {
		outcome := <-outcomes
		if outcome.err != nil {
			t.Fatalf("%s SendControlled() error = %v", name, outcome.err)
		}
		if outcome.result.Status != AutomationDeliverySent || !outcome.result.DispatchAttempted {
			t.Fatalf("%s SendControlled() = %#v, want sent", name, outcome.result)
		}
	}
	if got := deliveryCalls.Load(); got != 1 {
		t.Fatalf("delivery calls = %d, want 1", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	var reopenedDeliveryCalls atomic.Int32
	var reopenedAuthorizationCalls atomic.Int32
	reopened, _ := newSyntheticWechatAgentRuntimeWithAuthorization(
		t,
		ledgerPath,
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			reopenedDeliveryCalls.Add(1)
			return failedAgentDelivery("unexpected_delivery", "replay was redispatched")
		}),
		WechatAgentSendAuthorizerFunc(func(
			context.Context,
			AgentControlledSendRequest,
		) error {
			reopenedAuthorizationCalls.Add(1)
			return errors.New("expired replay was reauthorized")
		}),
	)
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedResult, err := reopened.SendControlled(context.Background(), replay)
	if err != nil {
		t.Fatalf("reopened SendControlled() error = %v", err)
	}
	if reopenedResult.Status != AutomationDeliverySent || !reopenedResult.DispatchAttempted {
		t.Fatalf("reopened SendControlled() = %#v, want sent", reopenedResult)
	}
	if reopenedDeliveryCalls.Load() != 0 || reopenedAuthorizationCalls.Load() != 0 {
		t.Fatalf(
			"reopened delivery calls = %d, authorization calls = %d, want 0/0",
			reopenedDeliveryCalls.Load(),
			reopenedAuthorizationCalls.Load(),
		)
	}
}

func TestWechatAgentControlledSendRejectsExpiredNewRequestWithoutReservation(t *testing.T) {
	var deliveryCalls atomic.Int32
	var authorizationCalls atomic.Int32
	runtime, _ := newSyntheticWechatAgentRuntimeWithAuthorization(
		t,
		filepath.Join(t.TempDir(), "deliveries.json"),
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			deliveryCalls.Add(1)
			return AutomationDeliveryResult{
				Status: AutomationDeliverySent, DispatchAttempted: true,
			}
		}),
		WechatAgentSendAuthorizerFunc(func(
			context.Context,
			AgentControlledSendRequest,
		) error {
			authorizationCalls.Add(1)
			return nil
		}),
	)
	t.Cleanup(func() { _ = runtime.Close() })

	request := validAgentControlledSendRequest(t)
	request.IdempotencyKey = "send:" + request.Authorization.ActionID
	request.Authorization.ExpiresAt = time.Now().Add(-time.Hour)
	_, err := runtime.SendControlled(context.Background(), request)
	assertAgentErrorCode(t, err, AgentErrorCodeValidation)
	if deliveryCalls.Load() != 0 || authorizationCalls.Load() != 0 {
		t.Fatalf(
			"delivery calls = %d, authorization calls = %d, want 0/0",
			deliveryCalls.Load(),
			authorizationCalls.Load(),
		)
	}
	runtime.deliveryMu.Lock()
	defer runtime.deliveryMu.Unlock()
	if len(runtime.pendingDeliveries) != 0 || len(runtime.deliveries) != 0 {
		t.Fatalf(
			"pending deliveries = %d, ledger records = %d, want 0/0",
			len(runtime.pendingDeliveries),
			len(runtime.deliveries),
		)
	}
}

func TestWechatAgentControlledSendAuthorizesBeforeReservation(t *testing.T) {
	var deliveryCalls atomic.Int32
	runtime, _ := newSyntheticWechatAgentRuntimeWithAuthorization(
		t,
		filepath.Join(t.TempDir(), "deliveries.json"),
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			deliveryCalls.Add(1)
			return AutomationDeliveryResult{
				Status: AutomationDeliverySent, DispatchAttempted: true,
			}
		}),
		WechatAgentSendAuthorizerFunc(func(
			context.Context,
			AgentControlledSendRequest,
		) error {
			return errors.New("synthetic authorization rejection")
		}),
	)
	t.Cleanup(func() { _ = runtime.Close() })

	request := validAgentControlledSendRequest(t)
	_, err := runtime.SendControlled(context.Background(), request)
	assertAgentErrorCode(t, err, AgentErrorCodeValidation)
	if deliveryCalls.Load() != 0 {
		t.Fatalf("delivery calls = %d, want 0", deliveryCalls.Load())
	}
	runtime.deliveryMu.Lock()
	defer runtime.deliveryMu.Unlock()
	if len(runtime.pendingDeliveries) != 0 || len(runtime.deliveries) != 0 {
		t.Fatalf(
			"pending deliveries = %d, ledger records = %d, want 0/0",
			len(runtime.pendingDeliveries),
			len(runtime.deliveries),
		)
	}
}

func TestWechatAgentControlledSendStableScopeChangesConflict(t *testing.T) {
	mutations := map[string]func(*AgentControlledSendRequest){
		"recipient peer": func(request *AgentControlledSendRequest) {
			request.Recipient.PeerID = "wxid-bob"
		},
		"recipient chat scope": func(request *AgentControlledSendRequest) {
			request.Recipient = AgentRecipient{
				ChatType: ChatTypeGroup,
				PeerID:   "team@chatroom",
			}
		},
		"normalized content and hash": func(request *AgentControlledSendRequest) {
			request.Content.Text = "goodbye"
			request.Authorization.ContentSHA256 = mustAgentContentHash(t, request.Content)
		},
		"action id": func(request *AgentControlledSendRequest) {
			request.Authorization.ActionID = "action-2"
		},
		"permission request id": func(request *AgentControlledSendRequest) {
			request.Authorization.PermissionRequestID = "permission-2"
		},
		"audience id": func(request *AgentControlledSendRequest) {
			request.Authorization.AudienceID = "audience-2"
		},
		"audience version": func(request *AgentControlledSendRequest) {
			request.Authorization.AudienceVersion++
		},
		"frequency scope id": func(request *AgentControlledSendRequest) {
			request.Authorization.FrequencyScope.ID = "frequency-2"
		},
		"frequency window": func(request *AgentControlledSendRequest) {
			request.Authorization.FrequencyScope.WindowSeconds++
		},
		"frequency limit": func(request *AgentControlledSendRequest) {
			request.Authorization.FrequencyScope.Limit++
		},
	}

	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			var deliveryCalls atomic.Int32
			runtime, _ := newSyntheticWechatAgentRuntime(
				t,
				filepath.Join(t.TempDir(), "deliveries.json"),
				syntheticWechatAgentDeliveryFunc(func(
					context.Context,
					AgentControlledSendRequest,
					[]byte,
				) AutomationDeliveryResult {
					deliveryCalls.Add(1)
					return AutomationDeliveryResult{
						Status: AutomationDeliverySent, DispatchAttempted: true,
					}
				}),
			)
			t.Cleanup(func() { _ = runtime.Close() })

			request := validAgentControlledSendRequest(t)
			if _, err := runtime.SendControlled(context.Background(), request); err != nil {
				t.Fatalf("initial SendControlled() error = %v", err)
			}
			replay := request
			mutate(&replay)
			replay.Authorization.ExpiresAt = time.Now().Add(-time.Hour)
			_, err := runtime.SendControlled(context.Background(), replay)
			assertAgentErrorCode(t, err, AgentErrorCodeConflict)
			if got := deliveryCalls.Load(); got != 1 {
				t.Fatalf("delivery calls = %d, want 1", got)
			}
		})
	}
}

func TestWechatAgentControlledSendNeverRedispatchesSameKey(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "deliveries.json")
	var calls atomic.Int32
	delivery := syntheticWechatAgentDeliveryFunc(func(
		context.Context,
		AgentControlledSendRequest,
		[]byte,
	) AutomationDeliveryResult {
		calls.Add(1)
		return unknownAgentDelivery("worker_error", "worker acknowledgement is ambiguous")
	})
	runtime, _ := newSyntheticWechatAgentRuntime(t, ledgerPath, delivery)
	request := validAgentControlledSendRequest(t)

	for index := 0; index < 2; index++ {
		result, err := runtime.SendControlled(context.Background(), request)
		if err != nil {
			t.Fatalf("SendControlled() error = %v", err)
		}
		if result.Status != AutomationDeliveryUnknown || !result.DispatchAttempted {
			t.Fatalf("SendControlled() = %#v, want unknown attempted", result)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("delivery calls = %d, want 1", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedCalls := atomic.Int32{}
	reopened, _ := newSyntheticWechatAgentRuntime(
		t,
		ledgerPath,
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			reopenedCalls.Add(1)
			return AutomationDeliveryResult{
				Status: AutomationDeliverySent, DispatchAttempted: true,
			}
		}),
	)
	t.Cleanup(func() { _ = reopened.Close() })
	result, err := reopened.SendControlled(context.Background(), request)
	if err != nil {
		t.Fatalf("reopened SendControlled() error = %v", err)
	}
	if result.Status != AutomationDeliveryUnknown || reopenedCalls.Load() != 0 {
		t.Fatalf("reopened result = %#v, calls = %d", result, reopenedCalls.Load())
	}
}

func TestWechatAgentControlledSendConcurrentDuplicateDispatchesOnce(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	runtime, _ := newSyntheticWechatAgentRuntime(
		t,
		filepath.Join(t.TempDir(), "deliveries.json"),
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			if calls.Add(1) == 1 {
				close(started)
			}
			<-release
			return AutomationDeliveryResult{
				Status: AutomationDeliverySent, DispatchAttempted: true,
			}
		}),
	)
	t.Cleanup(func() { _ = runtime.Close() })
	request := validAgentControlledSendRequest(t)

	results := make(chan AutomationDeliveryResult, 2)
	errs := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func() {
			result, err := runtime.SendControlled(context.Background(), request)
			results <- result
			errs <- err
		}()
	}
	<-started
	close(release)
	for index := 0; index < 2; index++ {
		if err := <-errs; err != nil {
			t.Fatalf("SendControlled() error = %v", err)
		}
		if result := <-results; result.Status != AutomationDeliverySent {
			t.Fatalf("SendControlled() = %#v, want sent", result)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("delivery calls = %d, want 1", got)
	}
}

func TestWechatAgentControlledSendRetriesKnownFailure(t *testing.T) {
	var calls atomic.Int32
	runtime, _ := newSyntheticWechatAgentRuntime(
		t,
		filepath.Join(t.TempDir(), "deliveries.json"),
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			if calls.Add(1) == 1 {
				return failedAgentDelivery("sender_unavailable", "delivery was not dispatched")
			}
			return AutomationDeliveryResult{
				Status: AutomationDeliverySent, DispatchAttempted: true,
			}
		}),
	)
	t.Cleanup(func() { _ = runtime.Close() })
	request := validAgentControlledSendRequest(t)
	request.Authorization.FrequencyScope.Limit = 1

	first, err := runtime.SendControlled(context.Background(), request)
	if err != nil {
		t.Fatalf("first SendControlled() error = %v", err)
	}
	if first.Status != AutomationDeliveryFailed || first.DispatchAttempted {
		t.Fatalf("first SendControlled() = %#v, want known failure", first)
	}
	second, err := runtime.SendControlled(context.Background(), request)
	if err != nil {
		t.Fatalf("second SendControlled() error = %v", err)
	}
	if second.Status != AutomationDeliverySent || !second.DispatchAttempted {
		t.Fatalf("second SendControlled() = %#v, want sent", second)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("delivery calls = %d, want 2", got)
	}
}

func TestQueueWechatAgentDeliveryTreatsAcceptedWorkerErrorAsUnknown(t *testing.T) {
	queue := make(chan *SendMsg)
	delivery := NewQueueWechatAgentDelivery(queue)
	request := validAgentControlledSendRequest(t)
	resultCh := make(chan AutomationDeliveryResult, 1)
	go func() {
		resultCh <- delivery.Deliver(context.Background(), request, nil)
	}()
	message := <-queue
	message.ResultChan <- errors.New("synthetic worker failure")
	result := <-resultCh
	if result.Status != AutomationDeliveryUnknown || !result.DispatchAttempted {
		t.Fatalf("Deliver() = %#v, want unknown attempted", result)
	}
}

func TestRunSendWorkerUsesContextBoundShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := make(chan *SendMsg, 1)
	queue <- &SendMsg{Type: "text"}
	var calls atomic.Int32
	err := runSendWorker(ctx, queue, func(*SendMsg) {
		calls.Add(1)
		cancel()
	})
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("runSendWorker() error = %v, calls = %d", err, calls.Load())
	}

	panicErr := runSendWorker(context.Background(), closedSendMsgQueue(), func(*SendMsg) {
		panic("synthetic panic")
	})
	if panicErr != nil {
		t.Fatalf("closed queue error = %v", panicErr)
	}
}

func closedSendMsgQueue() <-chan *SendMsg {
	queue := make(chan *SendMsg)
	close(queue)
	return queue
}

func TestFileWechatAgentAttachmentCaptureUsesInlineMediaOnly(t *testing.T) {
	store, err := OpenFileWechatAgentAttachmentStore(filepath.Join(t.TempDir(), "attachments"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secretPath := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secretPath, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	message := &WechatMessage{Message: []*Message{
		{Type: "file", Data: &SendRequestData{File: secretPath}},
		{Type: "file", Data: &SendRequestData{URL: "file://" + secretPath}},
		{Type: "image", Data: &SendRequestData{Media: []byte("inline")}},
	}}
	attachments, err := store.CaptureInbound(context.Background(), message)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].Size != int64(len("inline")) {
		t.Fatalf("captured attachments = %#v", attachments)
	}
	data, err := store.ReadAll(context.Background(), attachments[0].ID, 64)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "inline" {
		t.Fatalf("attachment data = %q", data)
	}
}

func TestWechatAgentMessageKeyMatchesLegacyCanonicalKey(t *testing.T) {
	got := buildWechatAgentMessageKey(
		ChatTypeGroup,
		"team@chatroom",
		"",
		"wxid-alice",
		"team@chatroom",
		1700000000,
		"hello",
		[]string{"text", "at"},
	)
	want := BuildMessageKey(
		ChatTypeGroup,
		"team@chatroom",
		"",
		"wxid-alice",
		"team@chatroom",
		1700000000,
		"hello",
		[]string{"text", "at"},
	)
	if got != want {
		t.Fatalf("message key = %q, want %q", got, want)
	}
}

func TestWechatAgentForwardRuntimeRestoreIsIdempotent(t *testing.T) {
	runtime, _ := newSyntheticWechatAgentRuntime(
		t,
		filepath.Join(t.TempDir(), "deliveries.json"),
		syntheticWechatAgentDeliveryFunc(func(
			context.Context,
			AgentControlledSendRequest,
			[]byte,
		) AutomationDeliveryResult {
			return failedAgentDelivery("unused", "unused")
		}),
	)
	defer runtime.Close()
	restore := installWechatAgentForwardRuntime(runtime)
	var wait sync.WaitGroup
	wait.Add(2)
	for index := 0; index < 2; index++ {
		go func() {
			defer wait.Done()
			restore()
		}()
	}
	wait.Wait()
}
