package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{}
var conn *websocket.Conn

type OneBotWSMsg struct {
	Action string    `json:"action"`
	Echo   string    `json:"echo"`
	Params *WSParams `json:"params"`
}

type WSParams struct {
	Message interface{} `json:"message"`
	UserID  string      `json:"user_id"`
	GroupID string      `json:"group_id"`
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	handleWebSocketWithAgent(currentHRWorkbenchAgentClient())(w, r)
}

func handleWebSocketWithAgent(client AgentClient) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !automationLoopbackRequest(r) {
			http.Error(w, "仅允许本机访问", http.StatusForbidden)
			return
		}
		socket, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			Error("升级失败", "err", err)
			return
		}
		conn = socket
		defer socket.Close()

		for {
			message := new(OneBotWSMsg)
			_, raw, err := socket.ReadMessage()
			if err != nil {
				Error("读取失败", "err", err)
				return
			}
			Info("收到消息", "msg", string(raw))
			if err := json.Unmarshal(raw, message); err != nil {
				Error("解析失败", "err", err)
				continue
			}

			switch message.Action {
			case "get_login_info":
				err = socket.WriteJSON(map[string]any{
					"echo":   message.Echo,
					"status": "ok",
					"data": map[string]any{
						"user_id":  myWechatId,
						"nickname": myWechatId,
					},
				})
			case "get_group_member_info":
				if message.Params == nil {
					err = errors.New("params is required")
					break
				}
				nickname, lookupErr := websocketAgentGroupMemberName(
					r.Context(),
					client,
					message.Params.GroupID,
					message.Params.UserID,
				)
				if lookupErr != nil {
					err = lookupErr
					break
				}
				err = socket.WriteJSON(map[string]any{
					"echo":   message.Echo,
					"status": "ok",
					"data":   map[string]any{"nickname": nickname},
				})
			case "send_private_msg", "send_group_msg":
				err = SendWSWithAgent(r.Context(), client, message.Params)
				if err == nil {
					err = socket.WriteJSON(map[string]any{
						"echo":   message.Echo,
						"status": "ok",
					})
				}
			default:
				continue
			}
			if err != nil {
				Error("WebSocket 请求失败", "err", err)
				_ = socket.WriteJSON(map[string]any{
					"echo":   message.Echo,
					"status": "failed",
					"error":  err.Error(),
				})
			}
		}
	}
}

func websocketAgentGroupMemberName(
	ctx context.Context,
	client AgentClient,
	groupID string,
	userID string,
) (string, error) {
	if client == nil {
		return "", &AgentError{
			Code: AgentErrorCodeUnavailable, Message: "wechat agent client is unavailable", Retryable: true,
		}
	}
	members, err := client.GroupMembers(ctx, strings.TrimSpace(groupID))
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(userID)
	for _, member := range members {
		if member.Username != target {
			continue
		}
		if name := strings.TrimSpace(member.DisplayName); name != "" {
			return name, nil
		}
		if name := strings.TrimSpace(member.NickName); name != "" {
			return name, nil
		}
		return member.Username, nil
	}
	return "", &AgentError{
		Code: AgentErrorCodeNotFound, Message: "group member was not found",
	}
}

func SendWebSocketMsg(jsonData []byte) {
	defer func() {
		if r := recover(); r != nil {
			Error("ws panic", "err", r, "stack", string(debug.Stack()))
		}
	}()

	if conn == nil {
		Error("连接为空")
		return
	}

	time.Sleep(time.Duration(config.SendInterval) * time.Millisecond)

	jsonReq, err := HandleMsg(jsonData)
	if err != nil {
		Error("JSON 序列化失败", "err", err)
		return
	}
	if jsonReq == nil {
		return
	}

	crmMessageCaptureNormalizedPayload(jsonReq)
	Info("发送数据", "msg", string(jsonReq))
	if err := conn.WriteMessage(websocket.TextMessage, jsonReq); err != nil {
		Error("发送消息失败", "err", err)
	}
}

func SendWS(req *WSParams) error {
	return SendWSWithAgent(context.Background(), currentHRWorkbenchAgentClient(), req)
}

func SendWSWithAgent(ctx context.Context, client AgentClient, req *WSParams) error {
	if req == nil {
		return errors.New("参数缺失")
	}
	request := &SendRequest{UserID: req.UserID, GroupID: req.GroupID}
	if text, ok := req.Message.(string); ok {
		request.Message = []*Message{{
			Type: "text",
			Data: &SendRequestData{Text: text},
		}}
	} else {
		raw, err := json.Marshal(req.Message)
		if err != nil {
			return err
		}
		messages := make([]*Message, 0)
		if err := json.Unmarshal(raw, &messages); err != nil {
			return err
		}
		request.Message = messages
	}

	recipient, contents, err := legacyAgentSendContents(request)
	if err != nil {
		return err
	}
	result, err := deliverManualAgentContents(ctx, client, "ws", recipient, contents)
	if err != nil {
		return err
	}
	if result.Status != AutomationDeliverySent {
		if result.ErrorMessage != "" {
			return errors.New(result.ErrorMessage)
		}
		return errors.New("消息发送结果未知")
	}
	return nil
}

func testWebSocket(w http.ResponseWriter, r *http.Request) {
	jsonData, err := io.ReadAll(r.Body)
	if err != nil {
		Error("读取消息失败", "err", err)
		return
	}
	if conn == nil {
		http.Error(w, "WebSocket 未连接", http.StatusServiceUnavailable)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
		Error("发送消息失败", "err", err)
	}
}
