package main

// 出向 API（CONTRACT v3 §7.2 / T8a）：Go 侧只做「把这段内容发给这个会话」的机械执行。
//
// 四个端点（鉴权 automationOperatorAuth：仅 loopback + Authorization: Bearer <operatorToken>，
// 与 /api/stickers、/mcp 同族）：
//
//	POST /api/send/text     {target, text, at?: []string}
//	POST /api/send/image    {target, image_base64 | path | attachment_id}
//	POST /api/send/sticker  {target, name}     ← 表情库模糊匹配，不命中不降级
//	POST /api/send/emoji    {target, md5}      ← 直接投递 emoji 分支
//
// 语义边界（刻意保持不变）：
//   - text 原样发出：不解释、不改写、不拆条、不重试、不做节奏控制（那些是 JS 侧 T8b 的事）；
//   - 单次调用只投递一条；image 的「上传 → 发送」两段流水由既有链路承担（见下）；
//   - 失败显式：任何分支都不吞异常，响应体永远带 ok/reason/mode。
//
// # 两条出向通道（T8a2）
//
//   - mode=local：投 param.go 的 msgChan，由 worker.go 的 SendWechatMsg 消费。只在
//     wechat-agent 进程里可用（wechat_agent_runner.go 起 RunSendWorker），支持 text /
//     image(字节) / sticker / emoji 全部内容。
//   - mode=agent：走 AgentClient.SendControlled（wechat_agent_delivery.go），即
//     httpRouteRegistrationContext.backend == httpRouteBackendAgent（或注入了 agent 客户端）
//     时使用。hr-workbench 进程只能走这条：该进程没有 Frida 通道，msgChan 也没有消费者。
//
//     agent 协议支持的内容（agent_contract.go）：text / attachment / emoji。因此 agent 模式下：
//     text            → 支持（at 不支持，协议无 atusrelist；给了 at 就显式失败，不篡改 text）
//     image           → 仅支持 {attachment_id}；{image_base64|path} 显式失败并说明
//                       （附件只能来自入站消息，wechat_agent_attachments.go CaptureInbound，
//                       协议里没有上传操作 —— 这条限制是真的）
//     sticker / emoji → 支持：Content{type:"emoji", md5:<32 位小写 hex>}，由 wechat-agent 进程
//                       的 worker.go `case "emoji"`（T4）发 sendemoji 请求；绝不降级成图片。
//                       md5 非法按请求非法处理（400），不做静默兜底。
//
// # HTTP 状态码约定
//
//	200 {ok:true}          已投递（local 已入队 / agent 已确认 Sent）
//	200 {ok:false,reason}  请求合法但这次发不了（表情没命中、emoji 分支未就绪、
//	                       agent 通道表达不了这类内容）
//	400 {ok:false,reason}  请求非法（JSON / 字段 / base64 等）
//	502 {ok:false,reason}  agent 投递失败或结果未知（上游网关问题）
//	503 {ok:false,reason}  agent 客户端不可用 / 本地发送队列不可用或已满 / 表情库打不开
//
// # emoji 字段约定（已与 T4 的实现对齐）
//
// worker.go 的 `case "emoji"` 读 m.Md5 去构建 sendemoji 请求体（服务端按 md5 反查表情），
// 因此投递时 Content 与 Md5 都放 md5（两个字段同时带，避免任一侧读法漂移）；FilePath 只是
// 表情库里的本地路径提示，T4 的 emoji 链路不需要本地文件，可忽略。

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	sendTextPath    = "/api/send/text"
	sendImagePath   = "/api/send/image"
	sendStickerPath = "/api/send/sticker"
	sendEmojiPath   = "/api/send/emoji"

	// sendBodyMaxBytes 是四个端点共用的请求体上限（含 image_base64）。
	sendBodyMaxBytes = 4 << 20
	// sendImageFileMaxBytes 是 {target, path} 形式读文件的体积上限。
	sendImageFileMaxBytes = 4 << 20

	sendTypeText  = "text"
	sendTypeImage = "image" // 第一段：worker 保存图片 + CDN 上传；上传完成后 Frida 回调自行投递 send_image
	sendTypeEmoji = "emoji" // T4

	// sendModeLocal / sendModeAgent 会回显在响应里，方便上层与 T7 排查。
	sendModeLocal = "local"
	sendModeAgent = "agent"

	sendAgentScope = "http" // newManualAgentControlledSendRequest 只接受 http/ws

	// sendEmojiUnavailableReason 说明 local 通道为什么现在不能发表情。
	sendEmojiUnavailableReason = "emoji send branch is not implemented yet (T4); sticker not queued rather than downgraded to an image"
	// sendAgentImageBytesUnsupportedReason 说明 agent 通道为什么发不了图片字节。
	sendAgentImageBytesUnsupportedReason = "agent backend only supports image via attachment_id: the agent protocol has no attachment upload, so image_base64/path cannot be delivered in agent mode; pass the attachment_id of an inbound attachment or use the local backend"
	// sendStickerMD5InvalidReason 说明表情库里的 md5 不可用（agent 协议要求 32 位小写 hex）。
	sendStickerMD5InvalidReason = "matched sticker md5 is invalid: the agent protocol requires a 32 character lowercase hex md5"
	// sendAgentAtUnsupportedReason 说明 agent 通道为什么不支持 at。
	sendAgentAtUnsupportedReason = "agent backend cannot express at-users: the agent protocol has no atusrelist; drop at or use the local backend"
)

// emojiSendBranchReady 表示发送侧（worker.go 的 switch）是否已有 case "emoji"。
// T4 的 emoji 分支已落地（worker.go 的 `case "emoji"` + worker_emoji_msg.go），故默认 true；
// 一旦那侧被回退或暂不可用，把它置 false，/api/send/sticker 就会显式失败而不是降级发图片。
// 只影响 local 通道（agent 通道根本表达不了 emoji）；用 atomic 是为了让测试/灰度能改。
var emojiSendBranchReady = newEmojiSendBranchFlag()

func newEmojiSendBranchFlag() *atomic.Bool {
	flag := new(atomic.Bool)
	flag.Store(true)
	return flag
}

// sendQueueProvider 提供本地发送队列（测试可注入假 channel，避免真的外发）。
type sendQueueProvider func() chan *SendMsg

// defaultSendQueue 返回进程内的发送队列。
func defaultSendQueue() chan *SendMsg {
	return msgChan
}

type sendTextRequest struct {
	Target string   `json:"target"`
	Text   string   `json:"text"`
	At     []string `json:"at"`
}

type sendImageRequest struct {
	Target string `json:"target"`
	// ImageBase64 / Path 走 local 通道；AttachmentID 走 agent 通道（引用入站附件）。
	ImageBase64  string `json:"image_base64"`
	Path         string `json:"path"`
	AttachmentID string `json:"attachment_id"`
}

type sendStickerRequest struct {
	Target string `json:"target"`
	Name   string `json:"name"`
}

type sendEmojiRequest struct {
	Target string `json:"target"`
	MD5    string `json:"md5"`
}

// sendResponse 是四个端点统一的响应信封。
type sendResponse struct {
	OK       bool   `json:"ok"`
	TaskID   int64  `json:"task_id,omitempty"`
	QueuedAt int64  `json:"queued_at,omitempty"`
	Type     string `json:"type,omitempty"`
	Target   string `json:"target,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// sendDelivery 是一次成功投递的元信息。
type sendDelivery struct {
	TaskID   int64
	QueuedAt int64
}

// sendFailure 描述一次失败：status==0 表示业务失败（HTTP 200 + ok:false），
// 否则用该 HTTP 状态码（502 agent 失败 / 503 通道不可用）。
type sendFailure struct {
	status int
	reason string
}

func (f *sendFailure) Error() string {
	return f.reason
}

func sendBusinessFailure(reason string) error {
	return &sendFailure{reason: reason}
}

func sendGatewayFailure(reason string) error {
	return &sendFailure{status: http.StatusBadGateway, reason: reason}
}

func sendChannelFailure(reason string) error {
	return &sendFailure{status: http.StatusServiceUnavailable, reason: reason}
}

// sendDispatcher 是两条出向通道的统一门面。
type sendDispatcher struct {
	mode  string
	queue sendQueueProvider
	agent AgentClient
}

// newSendDispatcher 按注册上下文选通道：agent 后端（或注入了 agent 客户端）走 SendControlled，
// 否则走进程内 msgChan。
func newSendDispatcher(backend httpRouteBackend, agent AgentClient, queue sendQueueProvider) *sendDispatcher {
	if queue == nil {
		queue = defaultSendQueue
	}
	if backend == httpRouteBackendAgent || agent != nil {
		return &sendDispatcher{mode: sendModeAgent, queue: queue, agent: agent}
	}
	return &sendDispatcher{mode: sendModeLocal, queue: queue}
}

// registerSendHTTPRoutes 供 httpRouteRegistrars 使用（挂法与 sticker_routes.go 一致）。
func registerSendHTTPRoutes(context httpRouteRegistrationContext) {
	registerSendRoutesWithDispatcher(
		context.mux,
		context.operatorToken(),
		newSendDispatcher(context.backend, context.agent, defaultSendQueue),
		defaultStickerStore,
	)
}

// registerSendRoutes 挂载出向 API（local 通道 + 进程级表情库）。
func registerSendRoutes(mux *http.ServeMux, operatorToken string) {
	registerSendRoutesWithDispatcher(
		mux,
		operatorToken,
		newSendDispatcher(httpRouteBackendLegacy, nil, defaultSendQueue),
		defaultStickerStore,
	)
}

// registerSendRoutesWithDeps 注入本地发送队列与表情库（测试 / 嵌入式场景）。
func registerSendRoutesWithDeps(
	mux *http.ServeMux,
	operatorToken string,
	queue sendQueueProvider,
	stickers stickerStoreProvider,
) {
	registerSendRoutesWithDispatcher(
		mux,
		operatorToken,
		newSendDispatcher(httpRouteBackendLegacy, nil, queue),
		stickers,
	)
}

// registerSendRoutesWithAgent 强制 agent 通道（测试 / 嵌入式场景）。
func registerSendRoutesWithAgent(
	mux *http.ServeMux,
	operatorToken string,
	agent AgentClient,
	stickers stickerStoreProvider,
) {
	registerSendRoutesWithDispatcher(
		mux,
		operatorToken,
		newSendDispatcher(httpRouteBackendAgent, agent, defaultSendQueue),
		stickers,
	)
}

func registerSendRoutesWithDispatcher(
	mux *http.ServeMux,
	operatorToken string,
	dispatcher *sendDispatcher,
	stickers stickerStoreProvider,
) {
	if mux == nil {
		return
	}
	if dispatcher == nil {
		dispatcher = newSendDispatcher(httpRouteBackendLegacy, nil, defaultSendQueue)
	}
	if stickers == nil {
		stickers = defaultStickerStore
	}
	mux.HandleFunc(sendTextPath, automationOperatorAuth(operatorToken, sendTextHandler(dispatcher)))
	mux.HandleFunc(sendImagePath, automationOperatorAuth(operatorToken, sendImageHandler(dispatcher)))
	mux.HandleFunc(
		sendStickerPath,
		automationOperatorAuth(operatorToken, sendStickerHandler(dispatcher, stickers)),
	)
	mux.HandleFunc(
		sendEmojiPath,
		automationOperatorAuth(operatorToken, sendEmojiHandler(dispatcher, stickers)),
	)
}

func sendTextHandler(dispatcher *sendDispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request sendTextRequest
		if !sendDecodeJSONBody(w, r, &request) {
			return
		}
		target := strings.TrimSpace(request.Target)
		if target == "" {
			sendWriteBadRequest(w, "target is required")
			return
		}
		// text 原样发出：这里只拒空串，不做任何改写。
		if request.Text == "" {
			sendWriteBadRequest(w, "text is required")
			return
		}
		delivery, err := dispatcher.DeliverText(r.Context(), target, request.Text, request.At)
		sendWriteDelivery(w, dispatcher, delivery, err, sendTypeText, target)
	}
}

func sendImageHandler(dispatcher *sendDispatcher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request sendImageRequest
		if !sendDecodeJSONBody(w, r, &request) {
			return
		}
		target := strings.TrimSpace(request.Target)
		if target == "" {
			sendWriteBadRequest(w, "target is required")
			return
		}
		delivery, err := dispatcher.DeliverImage(r.Context(), target, request)
		sendWriteDelivery(w, dispatcher, delivery, err, sendTypeImage, target)
	}
}

func sendStickerHandler(dispatcher *sendDispatcher, stickers stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request sendStickerRequest
		if !sendDecodeJSONBody(w, r, &request) {
			return
		}
		target := strings.TrimSpace(request.Target)
		if target == "" {
			sendWriteBadRequest(w, "target is required")
			return
		}
		name := strings.TrimSpace(request.Name)
		if name == "" {
			sendWriteBadRequest(w, "name is required")
			return
		}
		library := stickers()
		if library == nil {
			sendChannelFailureStatus(w, dispatcher.mode, "sticker library is unavailable")
			return
		}
		item, matched := library.Match(name)
		if !matched {
			// 不命中就报错，绝不退化成发图片。
			sendWriteFailure(w, dispatcher.mode, "no sticker matched: "+name)
			return
		}
		if item.MD5 == "" {
			sendWriteFailure(w, dispatcher.mode, "matched sticker has no md5: "+name)
			return
		}
		if _, valid := sendNormalizeEmojiMD5(item.MD5); !valid {
			// 请求本身没问题，但库里的 md5 不能用来发表情；显式拒绝而不是退回发图片。
			sendWriteBadRequest(w, sendStickerMD5InvalidReason)
			return
		}
		delivery, err := dispatcher.DeliverSticker(r.Context(), target, item, library)
		sendWriteDelivery(w, dispatcher, delivery, err, sendTypeEmoji, target)
	}
}

func sendEmojiHandler(dispatcher *sendDispatcher, stickers stickerStoreProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			automationWriteMethodNotAllowed(w, http.MethodPost)
			return
		}
		var request sendEmojiRequest
		if !sendDecodeJSONBody(w, r, &request) {
			return
		}
		target := strings.TrimSpace(request.Target)
		if target == "" {
			sendWriteBadRequest(w, "target is required")
			return
		}
		digest, valid := sendNormalizeEmojiMD5(request.MD5)
		if !valid {
			sendWriteBadRequest(w, "md5 must be 32 hexadecimal characters")
			return
		}
		// 原始 emoji 通道：不做模糊匹配，直接把 md5 投给发送侧。
		var library *StickerStore
		if stickers != nil {
			library = stickers()
		}
		delivery, err := dispatcher.DeliverEmoji(r.Context(), target, digest, library)
		sendWriteDelivery(w, dispatcher, delivery, err, sendTypeEmoji, target)
	}
}

// DeliverText 投递一条文本；agent 通道不支持 at（协议无 atusrelist），也不改写 text。
func (d *sendDispatcher) DeliverText(
	ctx context.Context,
	target string,
	text string,
	at []string,
) (sendDelivery, error) {
	if d.mode == sendModeAgent {
		if strings.TrimSpace(sendJoinAtUsers(at)) != "" {
			return sendDelivery{}, sendBusinessFailure(sendAgentAtUnsupportedReason)
		}
		return d.deliverAgent(ctx, target, AgentControlledContent{
			Type: AgentControlledContentText,
			Text: text,
		})
	}
	message := &SendMsg{
		Type:    sendTypeText,
		Content: text,
		AtUser:  sendJoinAtUsers(at),
	}
	sendAssignLocalTarget(message, target)
	return d.enqueueLocal(message)
}

// DeliverImage 投递一张图片：local 走 worker 的上传→发送流水；agent 只认 attachment_id。
func (d *sendDispatcher) DeliverImage(
	ctx context.Context,
	target string,
	request sendImageRequest,
) (sendDelivery, error) {
	attachmentID := strings.TrimSpace(request.AttachmentID)
	if d.mode == sendModeAgent {
		if attachmentID == "" {
			if strings.TrimSpace(request.ImageBase64) != "" || strings.TrimSpace(request.Path) != "" {
				return sendDelivery{}, sendBusinessFailure(sendAgentImageBytesUnsupportedReason)
			}
			return sendDelivery{}, sendBusinessFailure("attachment_id is required by the agent backend")
		}
		if err := validateAgentOpaqueID("attachment id", attachmentID, 256); err != nil {
			return sendDelivery{}, sendBusinessFailure("attachment_id is invalid")
		}
		return d.deliverAgent(ctx, target, AgentControlledContent{
			Type:         AgentControlledContentAttachment,
			AttachmentID: attachmentID,
		})
	}
	if attachmentID != "" {
		return sendDelivery{}, sendBusinessFailure(
			"attachment_id is only supported by the agent backend; the local backend needs image_base64 or path",
		)
	}
	payload, err := sendImagePayload(request)
	if err != nil {
		return sendDelivery{}, sendBusinessFailure(err.Error())
	}
	// 第一段：worker 的 "image" 分支保存图片并触发 CDN 上传；
	// 上传完成由 Frida 回调（frida_script_loader.go decodeFridaUploadMessage）投递 "send_image"。
	message := &SendMsg{Type: sendTypeImage, Content: payload}
	sendAssignLocalTarget(message, target)
	return d.enqueueLocal(message)
}

// DeliverSticker 投递一张表情；两条通道都不允许降级成图片。
func (d *sendDispatcher) DeliverSticker(
	ctx context.Context,
	target string,
	item StickerItem,
	library *StickerStore,
) (sendDelivery, error) {
	digest, valid := sendNormalizeEmojiMD5(item.MD5)
	if !valid {
		return sendDelivery{}, sendBusinessFailure(sendStickerMD5InvalidReason)
	}
	if d.mode == sendModeAgent {
		return d.deliverAgent(ctx, target, sendEmojiContent(digest))
	}
	if !emojiSendBranchReady.Load() {
		return sendDelivery{}, sendBusinessFailure(sendEmojiUnavailableReason)
	}
	return d.enqueueLocal(sendEmojiMessage(library, digest, target))
}

// DeliverEmoji 直接把 md5 投给 emoji 分支（不做库存校验）。
func (d *sendDispatcher) DeliverEmoji(
	ctx context.Context,
	target string,
	digest string,
	library *StickerStore,
) (sendDelivery, error) {
	normalized, valid := sendNormalizeEmojiMD5(digest)
	if !valid {
		return sendDelivery{}, sendBusinessFailure("emoji md5 is invalid: 32 character lowercase hex is required")
	}
	if d.mode == sendModeAgent {
		return d.deliverAgent(ctx, target, sendEmojiContent(normalized))
	}
	return d.enqueueLocal(sendEmojiMessage(library, normalized, target))
}

// deliverAgent 通过 AgentClient.SendControlled 投递一条内容（复用 http.go 的手工发送通道：
// 幂等键 / 授权 / 结果归一化都在 deliverManualAgentContents 里）。
func (d *sendDispatcher) deliverAgent(
	ctx context.Context,
	target string,
	content AgentControlledContent,
) (sendDelivery, error) {
	if ctx == nil {
		return sendDelivery{}, sendChannelFailure("request context is unavailable")
	}
	if d.agent == nil {
		return sendDelivery{}, sendChannelFailure("agent backend is configured but the agent client is unavailable")
	}
	recipient, err := sendAgentRecipient(target)
	if err != nil {
		return sendDelivery{}, sendBusinessFailure(err.Error())
	}
	result, err := deliverManualAgentContents(
		ctx,
		d.agent,
		sendAgentScope,
		recipient,
		[]AgentControlledContent{content},
	)
	if err != nil {
		return sendDelivery{}, sendGatewayFailure(
			"agent delivery failed: " + publicAgentError(err, "agent delivery failed"),
		)
	}
	switch result.Status {
	case AutomationDeliverySent:
		return sendDelivery{TaskID: sendNextAgentTaskID(), QueuedAt: time.Now().UnixMilli()}, nil
	case AutomationDeliveryUnknown:
		return sendDelivery{}, sendGatewayFailure(
			"agent delivery outcome is unknown: " + sendNonEmpty(result.ErrorMessage, "no detail"),
		)
	default:
		return sendDelivery{}, sendGatewayFailure(
			"agent delivery failed: " + sendNonEmpty(result.ErrorMessage, string(result.Status)),
		)
	}
}

// sendAgentTaskIDSequence 给 agent 通道生成关联 id（agent 协议不返回任务 id，
// 这里自增本地 id 并在响应里标 mode:"agent"，便于上层与 T7 排查）。
var sendAgentTaskIDSequence atomic.Int64

func sendNextAgentTaskID() int64 {
	return sendAgentTaskIDSequence.Add(1)
}

// enqueueLocal 把一条 SendMsg 放进进程内队列。
func (d *sendDispatcher) enqueueLocal(message *SendMsg) (sendDelivery, error) {
	var queue chan *SendMsg
	if d.queue != nil {
		queue = d.queue()
	}
	taskID, queuedAt, err := sendEnqueue(queue, message)
	if err != nil {
		return sendDelivery{}, sendChannelFailure(err.Error())
	}
	return sendDelivery{TaskID: taskID, QueuedAt: queuedAt}, nil
}

// sendEmojiContent 构造 agent 协议的 emoji 内容（md5 必须是 32 位小写 hex）。
func sendEmojiContent(digest string) AgentControlledContent {
	return AgentControlledContent{Type: AgentControlledContentEmoji, MD5: digest}
}

// sendNormalizeEmojiMD5 归一化并校验表情 md5：先按约定转小写，再交给 agent 侧的
// IsValidAgentEmojiMD5（它要求 32 位小写 hex）。两条通道共用同一套校验。
func sendNormalizeEmojiMD5(value string) (string, bool) {
	digest := strings.ToLower(strings.TrimSpace(value))
	if !IsValidAgentEmojiMD5(digest) {
		return "", false
	}
	return digest, true
}

// sendEmojiMessage 构造 emoji 发送任务。Content/Md5 都放 md5（见文件头的字段约定），
// FilePath 是表情库里的本地路径提示（库里没有这张时留空）。
func sendEmojiMessage(library *StickerStore, digest, target string) *SendMsg {
	message := &SendMsg{
		Type:    sendTypeEmoji,
		Content: digest,
		Md5:     digest,
	}
	if library != nil {
		if path, err := library.ImagePath(digest); err == nil {
			message.FilePath = path
		}
	}
	sendAssignLocalTarget(message, target)
	return message
}

// sendTargetIsGroup 判定 target 是否群聊：与入站解析同一口径
// （protobuf_handler.go 用 strings.Contains(sender, "@chatroom") 判群）。
// 其余一律当私聊，避免 filehelper / gh_ 公众号这类非群 id 被误判成群。
func sendTargetIsGroup(target string) bool {
	return strings.Contains(target, "@chatroom")
}

// sendAssignLocalTarget 把 target 落到本地 SendMsg 的 UserId / GroupID。
// 两个字段在 SendWechatMsg 里都归到 targetId，协议上没有区别。
func sendAssignLocalTarget(message *SendMsg, target string) {
	if message == nil {
		return
	}
	if sendTargetIsGroup(target) {
		message.GroupID = target
		return
	}
	message.UserId = target
}

// sendAgentRecipient 把 target 转成 agent 协议的收件人。
func sendAgentRecipient(target string) (AgentRecipient, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return AgentRecipient{}, errors.New("target is required")
	}
	if sendTargetIsGroup(target) {
		return AgentRecipient{ChatType: ChatTypeGroup, PeerID: target}, nil
	}
	return AgentRecipient{ChatType: ChatTypePrivate, PeerID: target}, nil
}

// sendJoinAtUsers 把 at 列表拼成 atuserlist（既有实现按逗号分隔解析，见 protobuf_handler.go）。
func sendJoinAtUsers(users []string) string {
	cleaned := make([]string, 0, len(users))
	for _, user := range users {
		user = strings.TrimSpace(user)
		if user == "" {
			continue
		}
		cleaned = append(cleaned, user)
	}
	return strings.Join(cleaned, ",")
}

// sendImagePayload 解析 image_base64 / path 两种入参，返回交给 SendMsg.Content 的载荷。
func sendImagePayload(request sendImageRequest) (string, error) {
	encoded := strings.TrimSpace(request.ImageBase64)
	path := strings.TrimSpace(request.Path)
	switch {
	case strings.TrimSpace(request.AttachmentID) != "":
		return "", errors.New("attachment_id is only supported by the agent backend")
	case encoded != "" && path != "":
		return "", errors.New("image_base64 and path are mutually exclusive")
	case encoded != "":
		if _, err := base64.StdEncoding.DecodeString(sendStripBase64Prefix(encoded)); err != nil {
			return "", errors.New("image_base64 is not valid base64")
		}
		// 原样交给 worker 的 SaveBase64Image（它接受 base64:// / data URL / 裸 base64）。
		return encoded, nil
	case path != "":
		data, err := sendReadImageFile(path)
		if err != nil {
			return "", err
		}
		return "base64://" + base64.StdEncoding.EncodeToString(data), nil
	default:
		return "", errors.New("image_base64 or path is required")
	}
}

func sendStripBase64Prefix(value string) string {
	if strings.HasPrefix(value, "base64://") {
		return strings.TrimSpace(strings.TrimPrefix(value, "base64://"))
	}
	if index := strings.Index(value, "base64,"); index >= 0 {
		return strings.TrimSpace(value[index+len("base64,"):])
	}
	return value
}

func sendReadImageFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("path is not readable")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("path must be a regular file")
	}
	if info.Size() <= 0 {
		return nil, errors.New("path points at an empty file")
	}
	if info.Size() > sendImageFileMaxBytes {
		return nil, errors.New("image file is too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("path is not readable")
	}
	return data, nil
}

// sendEnqueueMu 让「预估 task_id + 入队」在 API 侧串行，响应里的 task_id 严格递增。
var sendEnqueueMu sync.Mutex

// sendEnqueue 把一条消息放进发送队列，返回预估的 task_id 与入队时间。
// SendWechatMsg 取任务号时才自增（worker.go），所以这里是按当前计数器预报的下一号；
// API 侧串行只是为了让连续两次调用拿到递增的值，便于把响应与发送日志对上。
func sendEnqueue(queue chan *SendMsg, message *SendMsg) (int64, int64, error) {
	if queue == nil {
		return 0, 0, errors.New("send queue is unavailable")
	}
	if message == nil {
		return 0, 0, errors.New("send message is empty")
	}
	sendEnqueueMu.Lock()
	defer sendEnqueueMu.Unlock()
	taskID := atomic.LoadInt64(&taskId) + 1
	queuedAt := time.Now().UnixMilli()
	select {
	case queue <- message:
	default:
		return 0, 0, errors.New("send queue is full")
	}
	return taskID, queuedAt, nil
}

// sendWriteDelivery 统一把投递结果写成响应；错误按 sendFailure 的类型选状态码。
func sendWriteDelivery(
	w http.ResponseWriter,
	dispatcher *sendDispatcher,
	delivery sendDelivery,
	err error,
	messageType string,
	target string,
) {
	if err != nil {
		var failure *sendFailure
		if errors.As(err, &failure) {
			if failure.status == 0 {
				sendWriteFailure(w, dispatcher.mode, failure.reason)
				return
			}
			automationWriteJSON(w, failure.status, sendResponse{
				OK:     false,
				Mode:   dispatcher.mode,
				Type:   messageType,
				Target: target,
				Reason: failure.reason,
			})
			return
		}
		// 兜底：未归类的错误也算上游失败，绝不静默。
		automationWriteJSON(w, http.StatusBadGateway, sendResponse{
			OK:     false,
			Mode:   dispatcher.mode,
			Type:   messageType,
			Target: target,
			Reason: err.Error(),
		})
		return
	}
	automationWriteJSON(w, http.StatusOK, sendResponse{
		OK:       true,
		TaskID:   delivery.TaskID,
		QueuedAt: delivery.QueuedAt,
		Type:     messageType,
		Target:   target,
		Mode:     dispatcher.mode,
	})
}

func sendDecodeJSONBody(w http.ResponseWriter, r *http.Request, destination any) bool {
	return stickerDecodeJSONBodyLimited(w, r, destination, sendBodyMaxBytes)
}

func sendWriteBadRequest(w http.ResponseWriter, reason string) {
	automationWriteError(w, http.StatusBadRequest, "invalid_request", reason)
}

// sendWriteFailure 表示「请求本身合法，但这次发不了」（表情没命中 / emoji 分支未就绪 /
// agent 通道表达不了这类内容）。调用方只看 ok 字段；HTTP 仍是 200，方便上层统一解析。
func sendWriteFailure(w http.ResponseWriter, mode, reason string) {
	automationWriteJSON(w, http.StatusOK, sendResponse{OK: false, Mode: mode, Reason: reason})
}

// sendChannelFailureStatus 用于通道级不可用（表情库打不开等），503。
func sendChannelFailureStatus(w http.ResponseWriter, mode, reason string) {
	automationWriteJSON(w, http.StatusServiceUnavailable, sendResponse{OK: false, Mode: mode, Reason: reason})
}

func sendNonEmpty(value, fallback string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return fallback
}
