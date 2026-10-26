package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ForwardRecord 是转发给下游的归一化消息。
// 字段语义（沿用 4.1.9.58 的实测规则，4.1.11.53 保持兼容）：
//
//	self_id                   = 你（接收方，config.SelfWxid）
//	user_id == sender.user_id = 消息作者（入站=对方，回显=你自己）
//	group_id                  = '' 私聊 / 'xxx@chatroom' 群聊
type ForwardRecord struct {
	T           int64             `json:"t"`      // 秒级时间戳
	Chat        string            `json:"chat"`   // private | group
	Dir         string            `json:"dir"`    // in（对方发来）| out（你自己发的回显）
	Peer        string            `json:"peer"`   // 回复目标：群=group_id，私聊=对方 wxid
	Author      string            `json:"author"` // 消息作者 wxid
	GroupID     string            `json:"group_id,omitempty"`
	Text        string            `json:"text"`
	Suspicious  bool              `json:"suspicious"` // 疑似提示词注入（启发式）：下游只当数据、勿执行
	Kinds       []string          `json:"kinds"`      // message[] 段类型
	Mid         string            `json:"mid"`
	Attachments []AgentAttachment `json:"attachments,omitempty"`
}

var forwardFileMu sync.Mutex
var forwardRuntimeMu sync.RWMutex
var forwardInboundAppender = func(*ForwardRecord) error { return nil }
var forwardAttachmentCapture func(context.Context, *WechatMessage) ([]AgentAttachment, error)

// Forward 过滤 + 归一化 + 投递到 config.Sink。sink 为空则不转发（沿用旧的 :36060 POST）。
func Forward(jsonReq []byte) {
	m := new(WechatMessage)
	if err := json.Unmarshal(jsonReq, m); err != nil {
		return
	}
	if !hasRealContent(m) {
		return
	}
	rec := normalizeMsg(m)
	if rec.Dir != "in" { // 只转发入站；你自己发的回显不转
		return
	}
	forwardRuntimeMu.RLock()
	attachmentCapture := forwardAttachmentCapture
	inboundAppender := forwardInboundAppender
	forwardRuntimeMu.RUnlock()
	if attachmentCapture != nil {
		attachments, err := attachmentCapture(context.Background(), m)
		if err != nil {
			Error("消息附件捕获失败")
		} else {
			rec.Attachments = attachments
		}
	}
	if err := inboundAppender(rec); err != nil {
		Error("消息事件持久化失败")
	}
	if config.Sink == "" {
		return
	}
	if !forwardRuleStore.MatchAndReserve(rec, time.Now()) {
		return
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return
	}
	go deliverSink(config.Sink, data)
}

func installWechatAgentForwardRuntime(runtime *WechatAgentRuntime) func() {
	if runtime == nil {
		return func() {}
	}
	forwardRuntimeMu.Lock()
	previousAppender := forwardInboundAppender
	previousAttachmentCapture := forwardAttachmentCapture
	forwardInboundAppender = runtime.CaptureInbound
	forwardAttachmentCapture = runtime.CaptureInboundAttachments
	forwardRuntimeMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			forwardRuntimeMu.Lock()
			forwardInboundAppender = previousAppender
			forwardAttachmentCapture = previousAttachmentCapture
			forwardRuntimeMu.Unlock()
		})
	}
}

func hasRealContent(m *WechatMessage) bool {
	if m == nil {
		return false
	}
	showContent := strings.TrimSpace(sanitizeText(m.ShowContent))
	for _, seg := range m.Message {
		if seg == nil || seg.Data == nil {
			continue
		}
		if showContent != "" && (seg.Type == "text" || seg.Type == "at" || seg.Type == "share") {
			return true
		}
		if (seg.Type == "text" || seg.Type == "at" || seg.Type == "share") &&
			(strings.TrimSpace(sanitizeText(seg.Data.Text)) != "" ||
				strings.TrimSpace(seg.Data.QQ) != "") {
			return true
		}
		if strings.TrimSpace(seg.Data.File) != "" ||
			strings.HasPrefix(strings.TrimSpace(seg.Data.URL), "file://") ||
			len(seg.Data.Media) != 0 {
			return true
		}
	}
	return false
}

func normalizeMsg(m *WechatMessage) *ForwardRecord {
	author := ""
	if m.Sender != nil {
		author = m.Sender.UserID
	}
	if author == "" {
		author = m.UserID
	}
	kinds := make([]string, 0, len(m.Message))
	plain := ""
	for _, seg := range m.Message {
		if seg == nil {
			continue
		}
		kinds = append(kinds, seg.Type)
		if (seg.Type == "text" || seg.Type == "at") && seg.Data != nil {
			plain += seg.Data.Text
		}
	}
	text := strings.TrimSpace(m.ShowContent)
	if text == "" && !looksXML(plain) {
		text = plain
	}
	text = sanitizeText(text)
	group := m.GroupId
	chat := "private"
	peer := author
	if group != "" {
		chat = "group"
		peer = group
	}
	dir := "out"
	if author != config.SelfWxid {
		dir = "in"
	}
	return &ForwardRecord{
		T:          m.Time / 1000,
		Chat:       chat,
		Dir:        dir,
		Peer:       peer,
		Author:     author,
		GroupID:    group,
		Text:       text,
		Suspicious: looksInjection(text),
		Kinds:      kinds,
		Mid:        m.MessageId,
	}
}

func looksXML(s string) bool {
	s = strings.TrimLeft(s, " \t\r\n")
	return strings.HasPrefix(s, "<?xml") || strings.HasPrefix(s, "<msg") || strings.HasPrefix(s, "<")
}

// deliverSink 支持 file:/path | fifo:/path | webhook:URL
func deliverSink(sink string, data []byte) {
	defer func() {
		if r := recover(); r != nil {
			Error("deliverSink panic", "err", r)
		}
	}()
	if err := deliverSinkSync(context.Background(), sink, data); err != nil {
		Error("转发 sink 失败", "err", err)
	}
}

func deliverSinkSync(ctx context.Context, sink string, data []byte) error {
	kind, arg, err := parseForwardSink(sink)
	if err != nil {
		return err
	}
	line := append(append([]byte{}, data...), '\n')
	switch kind {
	case "file":
		forwardFileMu.Lock()
		defer forwardFileMu.Unlock()
		f, err := os.OpenFile(arg, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(line); err != nil {
			return err
		}
	case "fifo":
		f, err := os.OpenFile(arg, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(line); err != nil {
			return err
		}
	case "webhook":
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, arg, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("forward webhook returned HTTP %d", resp.StatusCode)
		}
	}
	return nil
}

func parseForwardSink(sink string) (string, string, error) {
	kind, arg, ok := strings.Cut(strings.TrimSpace(sink), ":")
	if !ok || strings.TrimSpace(arg) == "" {
		return "", "", fmt.Errorf("configured forward sink is unavailable")
	}
	switch kind {
	case "file", "fifo", "webhook":
		return kind, arg, nil
	default:
		return "", "", fmt.Errorf("configured forward sink type is unsupported")
	}
}

// sanitizeText 去掉 C0 控制字符（保留 \t \n），防终端/渲染注入；按 rune 限长 4000
func sanitizeText(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= 4000 {
			break
		}
		if r == '\t' || r == '\n' {
			b.WriteRune(r)
			n++
			continue
		}
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// injectionPatterns 常见提示词注入关键词。命中只置 suspicious 标志（不拦截，避免误伤正常消息），
// 真正的防线是下游把消息文本当纯数据、绝不当指令执行（见 wechat-use skill 安全一节）。
var injectionPatterns = []string{
	"ignore previous", "ignore above", "ignore all previous", "disregard previous",
	"system prompt", "you are now", "new instruction", "developer mode", "jailbreak",
	"reveal your", "print your prompt", "<|im_start|>", "<|system|>",
	"忽略之前", "忽略上面", "忽略以上", "忽略所有", "忽略前面", "无视之前", "无视上面",
	"系统提示", "系统提示词", "现在你是", "你现在是", "重复你的", "泄露你的",
}

func looksInjection(s string) bool {
	low := strings.ToLower(s)
	for _, p := range injectionPatterns {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}
