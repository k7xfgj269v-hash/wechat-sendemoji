package main

// T4b：emoji 内容类型在 agent 协议上的契约 / 投递 / 消息段解析测试。
//
// 覆盖三件事：
//  1. 契约：type=emoji 只接受 32 位小写 hex 的 md5，且不携带 text/attachment_id；
//     未知 type 仍然拒绝（老行为不变）。
//  2. 内容摘要：text/attachment 的 sha256 与加入 md5 字段之前逐字节一致（向后兼容）。
//  3. 投递：queueWechatAgentDelivery 把 emoji 映射成 SendMsg{Type:"emoji", Md5:...}，
//     绝不映射成 attachment/image；非法 md5 不入队。
//  4. 消息段：/send_private_msg 的 {"type":"emoji"} 段解析。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

const (
	emojiTestMD5      = "0302e5974743d8a463f64edfacda7fa2"
	emojiTestMD5Other = "bb02e5974743d8a463f64edfacda7f11"
)

func TestAgentControlledContentEmojiNormalization(t *testing.T) {
	valid := AgentControlledContent{Type: AgentControlledContentEmoji, MD5: emojiTestMD5}
	normalized, err := normalizeAgentControlledContent(valid)
	if err != nil {
		t.Fatalf("normalize(emoji) error = %v", err)
	}
	if normalized != valid {
		t.Fatalf("normalize(emoji) = %+v, want unchanged", normalized)
	}

	rejected := map[string]AgentControlledContent{
		"missing md5":        {Type: AgentControlledContentEmoji},
		"uppercase md5":      {Type: AgentControlledContentEmoji, MD5: "0302E5974743D8A463F64EDFACDA7FA2"},
		"short md5":          {Type: AgentControlledContentEmoji, MD5: "0302e5974743d8a463f64edfacda7fa"},
		"long md5":           {Type: AgentControlledContentEmoji, MD5: emojiTestMD5 + "00"},
		"non hex md5":        {Type: AgentControlledContentEmoji, MD5: "zz02e5974743d8a463f64edfacda7fa2"},
		"padded md5":         {Type: AgentControlledContentEmoji, MD5: " " + emojiTestMD5},
		"emoji with text":    {Type: AgentControlledContentEmoji, MD5: emojiTestMD5, Text: "hi"},
		"emoji with attach":  {Type: AgentControlledContentEmoji, MD5: emojiTestMD5, AttachmentID: "a1"},
		"text with md5":      {Type: AgentControlledContentText, Text: "hi", MD5: emojiTestMD5},
		"attach with md5":    {Type: AgentControlledContentAttachment, AttachmentID: "a1", MD5: emojiTestMD5},
		"unknown type image": {Type: "image", MD5: emojiTestMD5},
		"unknown type empty": {},
	}
	for name, content := range rejected {
		t.Run(name, func(t *testing.T) {
			_, normalizeErr := normalizeAgentControlledContent(content)
			if normalizeErr == nil {
				t.Fatalf("normalize(%+v) succeeded, want rejection", content)
			}
			assertAgentErrorCode(t, normalizeErr, AgentErrorCodeValidation)
		})
	}

	// 老内容不受影响。
	for _, content := range []AgentControlledContent{
		{Type: AgentControlledContentText, Text: "hello"},
		{Type: AgentControlledContentAttachment, AttachmentID: "attachment-1"},
	} {
		if _, err := normalizeAgentControlledContent(content); err != nil {
			t.Fatalf("normalize(%+v) error = %v", content, err)
		}
	}
}

func TestAgentControlledContentSHA256WireShape(t *testing.T) {
	// 加入 md5 字段后，text / attachment 的摘要必须与旧实现逐字节一致：
	// 它们依赖 AgentControlledContent 的 JSON 字段顺序与 omitempty，这里直接钉住报文。
	cases := []struct {
		name    string
		content AgentControlledContent
		wire    string
	}{
		{
			name:    "text",
			content: AgentControlledContent{Type: AgentControlledContentText, Text: "hi"},
			wire:    `{"type":"text","text":"hi"}`,
		},
		{
			name:    "attachment",
			content: AgentControlledContent{Type: AgentControlledContentAttachment, AttachmentID: "a1"},
			wire:    `{"type":"attachment","attachment_id":"a1"}`,
		},
		{
			name:    "emoji",
			content: AgentControlledContent{Type: AgentControlledContentEmoji, MD5: emojiTestMD5},
			wire:    `{"type":"emoji","md5":"` + emojiTestMD5 + `"}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			want := sha256.Sum256([]byte(testCase.wire))
			got, err := AgentControlledContentSHA256(testCase.content)
			if err != nil {
				t.Fatalf("AgentControlledContentSHA256() error = %v", err)
			}
			if got != hex.EncodeToString(want[:]) {
				t.Fatalf("content hash = %s, want sha256(%s)", got, testCase.wire)
			}
		})
	}

	// 不同 md5 必须算出不同摘要（否则授权范围会串）。
	first := mustAgentContentHash(t, AgentControlledContent{Type: AgentControlledContentEmoji, MD5: emojiTestMD5})
	second := mustAgentContentHash(t, AgentControlledContent{Type: AgentControlledContentEmoji, MD5: emojiTestMD5Other})
	if first == second {
		t.Fatal("different emoji md5 produced the same content hash")
	}
}

func TestValidateAgentControlledSendRequestEmoji(t *testing.T) {
	request := validAgentControlledSendRequest(t)
	request.Content = AgentControlledContent{Type: AgentControlledContentEmoji, MD5: emojiTestMD5}
	request.Authorization.ContentSHA256 = mustAgentContentHash(t, request.Content)
	if err := ValidateAgentControlledSendRequest(request); err != nil {
		t.Fatalf("emoji controlled send request rejected: %v", err)
	}

	stale := request
	stale.Authorization.ContentSHA256 = mustAgentContentHash(t, validAgentControlledSendRequest(t).Content)
	assertAgentErrorCode(t, ValidateAgentControlledSendRequest(stale), AgentErrorCodeValidation)

	uppercase := request
	uppercase.Content = AgentControlledContent{Type: AgentControlledContentEmoji, MD5: "0302E5974743D8A463F64EDFACDA7FA2"}
	uppercase.Authorization.ContentSHA256 = mustAgentContentHash(t, AgentControlledContent{
		Type: AgentControlledContentEmoji, MD5: emojiTestMD5,
	})
	assertAgentErrorCode(t, ValidateAgentControlledSendRequest(uppercase), AgentErrorCodeValidation)
}

func TestQueueWechatAgentDeliveryEmojiMapping(t *testing.T) {
	cases := []struct {
		name      string
		recipient AgentRecipient
		wantUser  string
		wantRoom  string
	}{
		{
			name:      "private",
			recipient: AgentRecipient{ChatType: ChatTypePrivate, PeerID: "filehelper"},
			wantUser:  "filehelper",
		},
		{
			name:      "group",
			recipient: AgentRecipient{ChatType: ChatTypeGroup, PeerID: "team@chatroom"},
			wantRoom:  "team@chatroom",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			queue := make(chan *SendMsg, 1)
			delivery := NewQueueWechatAgentDelivery(queue)
			request := AgentControlledSendRequest{
				IdempotencyKey: "emoji-" + testCase.name,
				Recipient:      testCase.recipient,
				Content:        AgentControlledContent{Type: AgentControlledContentEmoji, MD5: emojiTestMD5},
			}

			resultCh := make(chan AutomationDeliveryResult, 1)
			go func() {
				resultCh <- delivery.Deliver(context.Background(), request, nil)
			}()

			var message *SendMsg
			select {
			case message = <-queue:
			case <-time.After(2 * time.Second):
				t.Fatal("emoji delivery did not enqueue a message")
			}
			if message.Type != "emoji" {
				t.Fatalf("SendMsg.Type = %q, want emoji (no attachment/image downgrade)", message.Type)
			}
			if message.Md5 != emojiTestMD5 || message.Content != emojiTestMD5 {
				t.Fatalf("SendMsg md5 fields = %q/%q", message.Md5, message.Content)
			}
			if message.UserId != testCase.wantUser || message.GroupID != testCase.wantRoom {
				t.Fatalf("SendMsg target = %q/%q, want %q/%q",
					message.UserId, message.GroupID, testCase.wantUser, testCase.wantRoom)
			}
			if message.ResultChan == nil {
				t.Fatal("SendMsg.ResultChan is nil: the delivery acknowledgement would hang")
			}
			message.ResultChan <- nil

			select {
			case result := <-resultCh:
				if result.Status != AutomationDeliverySent {
					t.Fatalf("delivery status = %q, want sent", result.Status)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("delivery did not return after the acknowledgement")
			}
		})
	}
}

func TestQueueWechatAgentDeliveryEmojiRejectsInvalidMD5(t *testing.T) {
	queue := make(chan *SendMsg, 1)
	delivery := NewQueueWechatAgentDelivery(queue)
	request := AgentControlledSendRequest{
		IdempotencyKey: "emoji-invalid",
		Recipient:      AgentRecipient{ChatType: ChatTypePrivate, PeerID: "filehelper"},
		Content:        AgentControlledContent{Type: AgentControlledContentEmoji, MD5: "not-a-md5"},
	}
	result := delivery.Deliver(context.Background(), request, nil)
	if result.Status != AutomationDeliveryFailed {
		t.Fatalf("delivery status = %q, want failed", result.Status)
	}
	if result.ErrorCode != "validation_error" {
		t.Fatalf("delivery error code = %q, want validation_error", result.ErrorCode)
	}
	select {
	case message := <-queue:
		t.Fatalf("invalid emoji md5 was queued: %+v", message)
	default:
	}
}

func TestLegacyAgentSendContentsEmojiSegment(t *testing.T) {
	contentsFor := func(t *testing.T, data *SendRequestData) (AgentRecipient, []AgentControlledContent, error) {
		t.Helper()
		return legacyAgentSendContents(&SendRequest{
			UserID:  "filehelper",
			Message: []*Message{{Type: "emoji", Data: data}},
		})
	}

	for name, data := range map[string]*SendRequestData{
		"file carrier": {File: emojiTestMD5},
		"text carrier": {Text: emojiTestMD5},
		"both agree":   {File: emojiTestMD5, Text: emojiTestMD5},
	} {
		t.Run(name, func(t *testing.T) {
			recipient, contents, err := contentsFor(t, data)
			if err != nil {
				t.Fatalf("legacyAgentSendContents() error = %v", err)
			}
			if recipient.ChatType != ChatTypePrivate || recipient.PeerID != "filehelper" {
				t.Fatalf("recipient = %+v", recipient)
			}
			if len(contents) != 1 {
				t.Fatalf("contents = %+v", contents)
			}
			if contents[0].Type != AgentControlledContentEmoji || contents[0].MD5 != emojiTestMD5 {
				t.Fatalf("content = %+v, want emoji/%s", contents[0], emojiTestMD5)
			}
			if contents[0].AttachmentID != "" {
				t.Fatalf("emoji segment became an attachment: %+v", contents[0])
			}
			if _, err := AgentControlledContentSHA256(contents[0]); err != nil {
				t.Fatalf("emoji content is not sendable: %v", err)
			}
		})
	}

	for name, data := range map[string]*SendRequestData{
		"missing data":      nil,
		"missing md5":       {},
		"invalid md5":       {File: "not-a-md5"},
		"uppercase md5":     {File: "0302E5974743D8A463F64EDFACDA7FA2"},
		"carriers disagree": {File: emojiTestMD5, Text: emojiTestMD5Other},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := contentsFor(t, data); err == nil {
				t.Fatalf("legacyAgentSendContents(%+v) succeeded, want error", data)
			}
		})
	}

	// text 与 emoji 混排仍各自成段，顺序与消息段顺序一致（emoji 就地追加，text 汇总后追加）。
	recipient, contents, err := legacyAgentSendContents(&SendRequest{
		UserID: "filehelper",
		Message: []*Message{
			{Type: "emoji", Data: &SendRequestData{File: emojiTestMD5}},
			{Type: "text", Data: &SendRequestData{Text: "hi"}},
		},
	})
	if err != nil {
		t.Fatalf("legacyAgentSendContents() error = %v", err)
	}
	if recipient.PeerID != "filehelper" || len(contents) != 2 {
		t.Fatalf("recipient/contents = %+v/%+v", recipient, contents)
	}
	if contents[0].Type != AgentControlledContentEmoji || contents[1].Type != AgentControlledContentText {
		t.Fatalf("content order = %+v", contents)
	}
}
