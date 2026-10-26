//go:build !noFrida

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/frida/frida-go/frida"
)

func loadFridaScript(currentSession *frida.Session) (*frida.Script, error) {
	return loadFridaScriptWithContext(context.Background(), currentSession)
}

func loadFridaScriptWithContext(
	ctx context.Context,
	currentSession *frida.Session,
) (*frida.Script, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	jsonData, err := os.ReadFile(config.WechatConf)
	if err != nil {
		return nil, fmt.Errorf("读取微信 Hook 配置失败")
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	codeTemplate, err := os.ReadFile("./script.js")
	if err != nil {
		return nil, fmt.Errorf("读取 Frida 脚本失败")
	}

	renderedScript, err := renderFridaScriptTemplate(codeTemplate, jsonData)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	script, err := currentSession.CreateScript(renderedScript)
	if err != nil {
		return nil, fmt.Errorf("创建 Frida 脚本失败: %w", err)
	}

	script.On("message", handleFridaMessage)

	if err := ctx.Err(); err != nil {
		script.Clean()
		return nil, err
	}
	if err := script.Load(); err != nil {
		script.Clean()
		return nil, fmt.Errorf("加载 Frida 脚本失败: %w", err)
	}

	Info("✅ Frida 已就绪，微信控制通道已打通")
	return script, nil
}

func handleFridaMessage(rawMessage string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			Error("忽略触发 panic 的 Frida 消息", "err", recovered)
		}
	}()

	message, err := decodeFridaMessage(rawMessage)
	if err != nil {
		Error("忽略畸形 Frida 消息", "err", err)
		return
	}

	switch message.Type {
	case "send":
		if err := handleFridaSendMessage(message.Payload); err != nil {
			Error("忽略畸形 Frida send 消息", "err", err)
		}
	case "log":
		var payload interface{}
		if len(message.Payload) > 0 && json.Unmarshal(message.Payload, &payload) == nil {
			Info("[JS日志]", "payload", payload)
		}
	case "error":
		Error("[JS日志报错]", "err", message.Description, "stack", message.Stack)
	}
}

func handleFridaSendMessage(rawPayload json.RawMessage) error {
	payload, err := decodeFridaSendPayload(rawPayload)
	if err != nil {
		return err
	}
	payloadType, err := requiredFridaPayloadString(payload, "type")
	if err != nil {
		return err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码 Frida payload 失败: %w", err)
	}

	switch payloadType {
	case "protobuf_msg":
		go HandleProtobufMsgAndSend(payload)
	case "send":
		if config.ConnType == "http" {
			go SendHttpReq(payloadJSON)
		} else {
			go SendWebSocketMsg(payloadJSON)
		}
	case "buf2resp":
		messageType, fieldErr := optionalFridaPayloadString(payload, "msg_type")
		if fieldErr != nil {
			return fieldErr
		}
		data, fieldErr := fridaPayloadBytes(payload, "data")
		if fieldErr != nil {
			return fieldErr
		}
		go HandleBuf2Resp(messageType, data)
	case "upload_image_finish", "upload_video_finish", "upload_voice_finish":
		message, fieldErr := decodeFridaUploadMessage(payloadType, payload)
		if fieldErr != nil {
			return fieldErr
		}
		msgChan <- message
	case "download":
		if err := Download(payloadJSON); err != nil {
			return fmt.Errorf("下载失败: %w", err)
		}
	}
	return nil
}

func decodeFridaUploadMessage(
	payloadType string,
	payload map[string]interface{},
) (*SendMsg, error) {
	targetID, err := requiredFridaPayloadString(payload, "target_id")
	if err != nil {
		return nil, err
	}
	cdnKey, err := optionalFridaPayloadString(payload, "cdn_key")
	if err != nil {
		return nil, err
	}
	aesKey, err := optionalFridaPayloadString(payload, "aes_key")
	if err != nil {
		return nil, err
	}
	md5Key, err := optionalFridaPayloadString(payload, "md5_key")
	if err != nil {
		return nil, err
	}

	message := &SendMsg{
		CdnKey: cdnKey,
		AesKey: aesKey,
		Md5Key: md5Key,
	}
	if strings.Contains(targetID, "wxid_") {
		message.UserId = targetID
	} else {
		message.GroupID = targetID
	}

	switch payloadType {
	case "upload_image_finish":
		message.Type = "send_image"
	case "upload_video_finish":
		message.Type = "send_video"
		message.VideoId, err = optionalFridaPayloadString(payload, "video_id")
	case "upload_voice_finish":
		message.Type = "send_voice"
		message.VoiceDuration, err = optionalFridaPayloadInt32(payload, "voice_duration")
		if err == nil {
			message.SilkDataLen, err = optionalFridaPayloadInt32(payload, "silk_data_len")
		}
	}
	if err != nil {
		return nil, err
	}
	if result, ok := pendingResultMap.LoadAndDelete(targetID); ok {
		if resultChannel, valid := result.(chan error); valid {
			message.ResultChan = resultChannel
		}
	}
	return message, nil
}

// newHookController 构造真实的 Frida 控制器（wechat-agent 角色）。
func newHookController(cfg *Config) FridaRuntime {
	return newFridaController(cfg, loadFridaScriptWithContext)
}
