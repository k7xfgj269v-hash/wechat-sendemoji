package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

func sendHandler(w http.ResponseWriter, r *http.Request) {
	sendHandlerWithAgent(currentHRWorkbenchAgentClient())(w, r)
}

func sendHandlerWithAgent(client AgentClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "仅支持 POST", http.StatusMethodNotAllowed)
			return
		}
		if !automationLoopbackRequest(r) {
			writeJSON(w, http.StatusForbidden, map[string]any{
				"status": "failed",
				"error":  "仅允许本机访问",
			})
			return
		}
		if client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "failed",
				"error":  "微信代理不可用",
			})
			return
		}

		req := new(SendRequest)
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, AgentMaxFrameBytes))
		if err := decoder.Decode(req); err != nil {
			http.Error(w, "无效的 JSON", http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			http.Error(w, "无效的 JSON", http.StatusBadRequest)
			return
		}

		recipient, contents, err := legacyAgentSendContents(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := deliverManualAgentContents(r.Context(), client, "http", recipient, contents)
		if err != nil {
			status := agentHTTPStatus(err)
			if result.Status == AutomationDeliveryUnknown && result.DispatchAttempted {
				status = http.StatusAccepted
			}
			writeJSON(w, status, map[string]any{
				"status": string(result.Status),
				"error":  publicAgentError(err, "消息发送失败"),
			})
			return
		}
		switch result.Status {
		case AutomationDeliverySent:
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		case AutomationDeliveryUnknown:
			writeJSON(w, http.StatusAccepted, map[string]any{
				"status": "unknown",
				"error":  result.ErrorMessage,
			})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"status": "failed",
				"error":  result.ErrorMessage,
			})
		}
	}
}

func legacyAgentSendContents(
	req *SendRequest,
) (AgentRecipient, []AgentControlledContent, error) {
	if req == nil || len(req.Message) == 0 {
		return AgentRecipient{}, nil, errors.New("参数缺失")
	}
	for _, segment := range req.Message {
		if segment != nil && segment.Type == "reply" && segment.Data != nil &&
			segment.Data.ReplyMessage != nil && req.GroupID == "" {
			req.GroupID = segment.Data.ReplyMessage.GroupId
		}
	}
	userID := strings.TrimSpace(req.UserID)
	groupID := strings.TrimSpace(req.GroupID)
	if (userID == "") == (groupID == "") {
		return AgentRecipient{}, nil, errors.New("必须指定且只能指定一个接收方")
	}
	recipient := AgentRecipient{ChatType: ChatTypePrivate, PeerID: userID}
	if groupID != "" {
		recipient = AgentRecipient{ChatType: ChatTypeGroup, PeerID: groupID}
	}

	text := ""
	contents := make([]AgentControlledContent, 0, len(req.Message)+1)
	for _, segment := range req.Message {
		if segment == nil || segment.Data == nil {
			return AgentRecipient{}, nil, errors.New("消息段非法")
		}
		switch segment.Type {
		case "text":
			text += segment.Data.Text
		case "at":
			if recipient.ChatType == ChatTypeGroup {
				if nickname, ok := userID2NicknameMap.Load(groupID + "_" + segment.Data.QQ); ok {
					text += fmt.Sprintf("@%s\u2005", nickname.(string))
				}
			}
		case "image", "video", "record", "voice", "file":
			contents = append(contents, AgentControlledContent{
				Type:         AgentControlledContentAttachment,
				AttachmentID: strings.TrimSpace(segment.Data.File),
			})
		case "emoji":
			md5Hex, err := legacyAgentEmojiMD5(segment.Data)
			if err != nil {
				return AgentRecipient{}, nil, err
			}
			// 表情段只承载 md5：服务端按 md5 反查表情，绝不退化成 attachment/image。
			contents = append(contents, AgentControlledContent{
				Type: AgentControlledContentEmoji,
				MD5:  md5Hex,
			})
		case "reply":
			text += segment.Data.Text
		default:
			return AgentRecipient{}, nil, errors.New("消息类型不支持")
		}
	}
	if strings.TrimSpace(text) != "" {
		contents = append(contents, AgentControlledContent{
			Type: AgentControlledContentText,
			Text: text,
		})
	}
	if len(contents) == 0 {
		return AgentRecipient{}, nil, errors.New("消息内容为空")
	}
	return recipient, contents, nil
}

// legacyAgentEmojiMD5 从表情消息段里取 md5。
//
// 段形状（{type:"emoji", data:{...}}）沿用既有 text/attachment 段的写法，不发明新协议：
//
//	{"type":"emoji","data":{"file":"<32 位小写 hex>"}}   ← 推荐，OneBot 媒体段本来就用 file
//	{"type":"emoji","data":{"text":"<32 位小写 hex>"}}   ← 兼容只看 text 的调用方
//
// 两个都写时必须是同一个值，否则显式报错而不是猜。
func legacyAgentEmojiMD5(data *SendRequestData) (string, error) {
	if data == nil {
		return "", errors.New("emoji 消息段缺少 data")
	}
	fileValue := strings.TrimSpace(data.File)
	textValue := strings.TrimSpace(data.Text)
	if fileValue != "" && textValue != "" && fileValue != textValue {
		return "", errors.New("emoji 消息段的 file 与 text 不一致")
	}
	md5Hex := fileValue
	if md5Hex == "" {
		md5Hex = textValue
	}
	if !IsValidAgentEmojiMD5(md5Hex) {
		return "", errors.New("emoji 消息段缺少合法的 md5（需 32 位小写 hex）")
	}
	return md5Hex, nil
}

func deliverManualAgentContents(
	ctx context.Context,
	client AgentClient,
	scope string,
	recipient AgentRecipient,
	contents []AgentControlledContent,
) (AutomationDeliveryResult, error) {
	if ctx == nil {
		err := errors.New("context is required")
		return automationDeliveryFailed("invalid_context", err.Error()), err
	}
	if client == nil {
		err := &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
		return automationDeliveryFailed(err.Code, err.Message), err
	}
	final := AutomationDeliveryResult{Status: AutomationDeliverySent, DispatchAttempted: true}
	dispatchAttempted := false
	for _, content := range contents {
		request, err := newManualAgentControlledSendRequest(scope, recipient, content, time.Now().UTC())
		if err != nil {
			if dispatchAttempted {
				return automationDeliveryUnknown(
					"partial_delivery_unknown",
					"an earlier message segment may already have been delivered",
				), err
			}
			return automationDeliveryFailed("validation_error", "manual delivery is invalid"), err
		}
		result, err := client.SendControlled(ctx, request)
		if result.DispatchAttempted || result.Status == AutomationDeliverySent {
			dispatchAttempted = true
		}
		if err != nil {
			if dispatchAttempted {
				if result.Status == AutomationDeliveryUnknown && result.DispatchAttempted {
					return result, err
				}
				return automationDeliveryUnknown(
					"partial_delivery_unknown",
					"one or more message segments may already have been delivered",
				), err
			}
			return automationDeliveryFailed("delivery_failed", publicAgentError(err, "message delivery failed")), err
		}
		if result.Status != AutomationDeliverySent {
			if dispatchAttempted {
				if result.Status == AutomationDeliveryUnknown && result.DispatchAttempted {
					return result, nil
				}
				return automationDeliveryUnknown(
					"partial_delivery_unknown",
					"one or more message segments may already have been delivered",
				), nil
			}
			return result, nil
		}
		final = result
	}
	return final, nil
}

func SendHttpReq(jsonData []byte) {
	defer func() {
		if r := recover(); r != nil {
			Error("http panic", "err", r, "stack", string(debug.Stack()))
		}
	}()

	time.Sleep(time.Duration(config.SendInterval) * time.Millisecond)
	jsonReq, err := HandleMsg(jsonData)
	if err != nil {
		Error("JSON 序列化失败", "err", err)
		return
	}
	if jsonReq == nil {
		return
	}

	Info("发送数据", "msg", string(jsonReq))
	Forward(jsonReq)
	if config.Sink != "" {
		return
	}

	req, err := http.NewRequest("POST", config.SendURL, bytes.NewBuffer(jsonReq))
	if err != nil {
		Error("创建请求失败", "err", err)
		return
	}

	// 5. 设置 Header (OneBot 接口通常要求 application/json)
	h := hmac.New(sha1.New, []byte(config.OnebotToken))
	h.Write(jsonReq)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Signature", "sha1="+hex.EncodeToString(h.Sum(nil)))

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	// 6. 执行请求
	resp, err := client.Do(req)
	if err != nil {
		Error("请求执行失败", "err", err)
		return
	}
	defer resp.Body.Close()

	// 7. 读取返回结果
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		Error("读取响应失败", "err", err)
		return
	}

	Info("返回内容", "status", resp.StatusCode, "body", string(body))
}

// jsonUnescapeString 对双重JSON编码的字符串做unescape
// 例如: \\u003c → \u003c (第一次json.Unmarshal) → < (本函数)
func jsonUnescapeString(s string) string {
	if s == "" {
		return s
	}
	var result string
	if err := json.Unmarshal([]byte(`"`+s+`"`), &result); err != nil {
		return s
	}
	return result
}

// getFileExt 从文件名中提取扩展名(不含.)
func getFileExt(fileName string) string {
	idx := strings.LastIndex(fileName, ".")
	if idx == -1 || idx == len(fileName)-1 {
		return ""
	}
	return fileName[idx+1:]
}

// extractFileName 从路径或URL中提取文件名
func extractFileName(s string) string {
	if s == "" {
		return ""
	}
	// 跳过 base64 数据
	if strings.HasPrefix(s, "base64://") || strings.Contains(s, ";base64,") {
		return ""
	}
	// URL 路径
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "file://") {
		if u, err := url.Parse(s); err == nil {
			p := u.Path
			if idx := strings.LastIndex(p, "/"); idx != -1 {
				return p[idx+1:]
			}
			return p
		}
	}
	// 本地文件路径
	if strings.Contains(s, "/") || strings.Contains(s, "\\") {
		if idx := strings.LastIndexAny(s, "/\\"); idx != -1 {
			return s[idx+1:]
		}
	}
	return s
}
