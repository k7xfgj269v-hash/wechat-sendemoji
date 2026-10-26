package main

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"
)

const (
	defaultWechatAgentDirectoryName = ".wechat-chatter/run"
	defaultWechatAgentSocketName    = "wechat-agent.sock"
	defaultWechatAgentEventName     = "wechat-agent-events.json"
	defaultWechatAgentDeliveryName  = "wechat-agent-deliveries.json"
	defaultWechatAgentAttachmentDir = "attachments"
	maxWechatAgentSendAttachment    = 128 << 20
)

type WechatAgentContacts interface {
	SearchContacts(context.Context, AgentContactSearchRequest) (AgentContactSearchResult, error)
	Contact(context.Context, string) (Contact, error)
	RefreshContacts(context.Context) (AgentContactCounts, error)
	Groups(context.Context) ([]Group, error)
	GroupMembers(context.Context, string) ([]GroupMember, error)
	Counts(context.Context) (AgentContactCounts, error)
}

type WechatAgentAttachments interface {
	Describe(context.Context, string) (AgentAttachment, error)
	ReadChunk(context.Context, AgentAttachmentChunkRequest) (AgentAttachmentChunk, error)
	ReadAll(context.Context, string, int64) ([]byte, error)
	CaptureInbound(context.Context, *WechatMessage) ([]AgentAttachment, error)
	Close() error
}

type WechatAgentControlledDelivery interface {
	Deliver(context.Context, AgentControlledSendRequest, []byte) AutomationDeliveryResult
}

type WechatAgentSendAuthorizer interface {
	Authorize(context.Context, AgentControlledSendRequest) error
}

type WechatAgentSendAuthorizerFunc func(context.Context, AgentControlledSendRequest) error

func (f WechatAgentSendAuthorizerFunc) Authorize(
	ctx context.Context,
	request AgentControlledSendRequest,
) error {
	return f(ctx, request)
}

type WechatAgentRuntimeDependencies struct {
	Contacts           WechatAgentContacts
	Frida              FridaLifecycle
	Attachments        WechatAgentAttachments
	Delivery           WechatAgentControlledDelivery
	Authorizer         WechatAgentSendAuthorizer
	Events             *WechatAgentEventStore
	DeliveryLedgerPath string
	Clock              func() time.Time
	PID                int
}

type wechatAgentDeliveryRecord struct {
	Fingerprint    string                   `json:"fingerprint"`
	FrequencyScope AgentFrequencyScope      `json:"frequency_scope"`
	Result         AutomationDeliveryResult `json:"result"`
	CreatedAt      time.Time                `json:"created_at"`
}

type wechatAgentDeliveryLedgerState struct {
	Version int                                  `json:"version"`
	Records map[string]wechatAgentDeliveryRecord `json:"records"`
}

type wechatAgentPendingDelivery struct {
	fingerprint string
	result      AutomationDeliveryResult
	done        chan struct{}
}

type WechatAgentRuntime struct {
	contacts    WechatAgentContacts
	frida       FridaLifecycle
	attachments WechatAgentAttachments
	delivery    WechatAgentControlledDelivery
	authorizer  WechatAgentSendAuthorizer
	events      *WechatAgentEventStore
	clock       func() time.Time
	pid         int
	startedAt   time.Time

	deliveryLedgerPath string
	deliveryMu         sync.Mutex
	deliveries         map[string]wechatAgentDeliveryRecord
	pendingDeliveries  map[string]*wechatAgentPendingDelivery
	frequency          map[string][]time.Time

	closeOnce sync.Once
}

func NewWechatAgentRuntime(dependencies WechatAgentRuntimeDependencies) (*WechatAgentRuntime, error) {
	if dependencies.Contacts == nil || dependencies.Frida == nil ||
		dependencies.Attachments == nil || dependencies.Delivery == nil ||
		dependencies.Events == nil {
		return nil, errors.New("wechat agent runtime dependencies are incomplete")
	}
	if dependencies.Clock == nil {
		dependencies.Clock = time.Now
	}
	if dependencies.PID == 0 {
		dependencies.PID = os.Getpid()
	}
	if dependencies.Authorizer == nil {
		dependencies.Authorizer = WechatAgentSendAuthorizerFunc(
			func(_ context.Context, request AgentControlledSendRequest) error {
				return ValidateAgentControlledSendRequest(request)
			},
		)
	}
	deliveries, err := loadWechatAgentDeliveryLedger(dependencies.DeliveryLedgerPath)
	if err != nil {
		return nil, err
	}
	frequency := make(map[string][]time.Time)
	for _, record := range deliveries {
		frequency[record.FrequencyScope.ID] = append(
			frequency[record.FrequencyScope.ID],
			record.CreatedAt,
		)
	}
	return &WechatAgentRuntime{
		contacts:           dependencies.Contacts,
		frida:              dependencies.Frida,
		attachments:        dependencies.Attachments,
		delivery:           dependencies.Delivery,
		authorizer:         dependencies.Authorizer,
		events:             dependencies.Events,
		clock:              dependencies.Clock,
		pid:                dependencies.PID,
		startedAt:          dependencies.Clock().UTC(),
		deliveryLedgerPath: dependencies.DeliveryLedgerPath,
		deliveries:         deliveries,
		pendingDeliveries:  make(map[string]*wechatAgentPendingDelivery),
		frequency:          frequency,
	}, nil
}

func (r *WechatAgentRuntime) Health(ctx context.Context) (AgentHealth, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return AgentHealth{}, err
	}
	counts, countsErr := r.contacts.Counts(ctx)
	fridaStatus := r.frida.Status()
	ready := countsErr == nil && fridaStatus.Attached && fridaStatus.ScriptLoaded
	status := "degraded"
	if ready {
		status = "ok"
	}
	return AgentHealth{
		Status:          status,
		Ready:           ready,
		PID:             r.pid,
		ProtocolVersion: AgentProtocolVersion,
		StartedAt:       r.startedAt.Format(time.RFC3339),
		Frida:           fridaStatus,
		Contacts:        counts,
		LatestCursor:    r.events.LatestCursor(),
	}, nil
}

func (r *WechatAgentRuntime) SearchContacts(
	ctx context.Context,
	request AgentContactSearchRequest,
) (AgentContactSearchResult, error) {
	return r.contacts.SearchContacts(ctx, request)
}

func (r *WechatAgentRuntime) Contact(ctx context.Context, username string) (Contact, error) {
	return r.contacts.Contact(ctx, username)
}

func (r *WechatAgentRuntime) RefreshContacts(ctx context.Context) (AgentContactCounts, error) {
	return r.contacts.RefreshContacts(ctx)
}

func (r *WechatAgentRuntime) Groups(ctx context.Context) ([]Group, error) {
	return r.contacts.Groups(ctx)
}

func (r *WechatAgentRuntime) GroupMembers(ctx context.Context, groupID string) ([]GroupMember, error) {
	return r.contacts.GroupMembers(ctx, groupID)
}

func (r *WechatAgentRuntime) FridaStatus(ctx context.Context) (FridaStatus, error) {
	if err := requireWechatAgentContext(ctx); err != nil {
		return FridaStatus{}, err
	}
	return r.frida.Status(), nil
}

func (r *WechatAgentRuntime) InitializeFrida(
	ctx context.Context,
	request FridaInitializeRequest,
) (FridaStatus, error) {
	return r.frida.Initialize(ctx, request)
}

func (r *WechatAgentRuntime) RescanFrida(ctx context.Context) (FridaStatus, error) {
	return r.frida.Rescan(ctx)
}

func (r *WechatAgentRuntime) DetachFrida(ctx context.Context) (FridaStatus, error) {
	return r.frida.Detach(ctx)
}

func (r *WechatAgentRuntime) DescribeAttachment(
	ctx context.Context,
	attachmentID string,
) (AgentAttachment, error) {
	if err := validateAgentOpaqueID("attachment id", attachmentID, 256); err != nil {
		return AgentAttachment{}, err
	}
	attachment, err := r.attachments.Describe(ctx, attachmentID)
	if err != nil {
		return AgentAttachment{}, err
	}
	if err := validateWechatAgentPublicAttachment(attachment); err != nil {
		return AgentAttachment{}, &AgentError{
			Code: AgentErrorCodeInternal, Message: "attachment metadata is unavailable",
		}
	}
	return attachment, nil
}

func (r *WechatAgentRuntime) ReadAttachmentChunk(
	ctx context.Context,
	request AgentAttachmentChunkRequest,
) (AgentAttachmentChunk, error) {
	if err := validateAgentOpaqueID("attachment id", request.AttachmentID, 256); err != nil {
		return AgentAttachmentChunk{}, err
	}
	if request.Offset < 0 || request.Length <= 0 || request.Length > AgentMaxAttachmentChunkBytes {
		return AgentAttachmentChunk{}, &AgentError{
			Code: AgentErrorCodeValidation, Message: "attachment chunk range is invalid",
		}
	}
	chunk, err := r.attachments.ReadChunk(ctx, request)
	if err != nil {
		return AgentAttachmentChunk{}, err
	}
	if chunk.AttachmentID != request.AttachmentID ||
		chunk.Offset != request.Offset ||
		len(chunk.Data) > request.Length ||
		len(chunk.Data) > AgentMaxAttachmentChunkBytes {
		return AgentAttachmentChunk{}, &AgentError{
			Code: AgentErrorCodeInternal, Message: "attachment chunk is unavailable",
		}
	}
	return chunk, nil
}

func (r *WechatAgentRuntime) SubscribeEvents(
	ctx context.Context,
	afterCursor string,
) (*WechatAgentEventSubscription, error) {
	return r.events.Subscribe(ctx, afterCursor)
}

func (r *WechatAgentRuntime) CaptureInbound(record *ForwardRecord) error {
	event, err := agentInboundEventFromForwardRecord(record)
	if err != nil {
		return err
	}
	_, _, err = r.events.Append(event)
	return err
}

func (r *WechatAgentRuntime) CaptureInboundAttachments(
	ctx context.Context,
	message *WechatMessage,
) ([]AgentAttachment, error) {
	attachments, err := r.attachments.CaptureInbound(ctx, message)
	if err != nil {
		return nil, err
	}
	for _, attachment := range attachments {
		if err := validateWechatAgentPublicAttachment(attachment); err != nil {
			return nil, err
		}
	}
	return attachments, nil
}
