package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"strconv"
	"strings"
)

func agentInboundEventFromForwardRecord(record *ForwardRecord) (AgentInboundEvent, error) {
	if record == nil || record.Dir != "in" {
		return AgentInboundEvent{}, &AgentError{
			Code: AgentErrorCodeValidation, Message: "inbound message is invalid",
		}
	}
	chatType := ChatType(record.Chat)
	if !IsValidChatType(chatType) || record.Peer == "" || record.Author == "" {
		return AgentInboundEvent{}, &AgentError{
			Code: AgentErrorCodeValidation, Message: "inbound message is invalid",
		}
	}
	kinds := make([]string, 0, len(record.Kinds))
	seenKinds := make(map[string]struct{}, len(record.Kinds))
	for _, kind := range record.Kinds {
		kind = strings.TrimSpace(kind)
		if kind == "" || len(kind) > 32 {
			return AgentInboundEvent{}, &AgentError{
				Code: AgentErrorCodeValidation, Message: "inbound message kind is invalid",
			}
		}
		if _, exists := seenKinds[kind]; !exists {
			seenKinds[kind] = struct{}{}
			kinds = append(kinds, kind)
		}
	}
	event := AgentInboundEvent{
		Source:      "wechat",
		Mid:         strings.TrimSpace(record.Mid),
		ChatType:    chatType,
		PeerID:      record.Peer,
		AuthorID:    record.Author,
		GroupID:     record.GroupID,
		Text:        record.Text,
		Kinds:       kinds,
		Timestamp:   record.T,
		Suspicious:  record.Suspicious,
		Attachments: append([]AgentAttachment{}, record.Attachments...),
	}
	event.MessageKey = buildWechatAgentMessageKey(
		event.ChatType,
		event.PeerID,
		event.Mid,
		event.AuthorID,
		event.GroupID,
		event.Timestamp,
		event.Text,
		event.Kinds,
	)
	return event, validateStoredAgentInboundEvent(event)
}

func buildWechatAgentMessageKey(
	chatType ChatType,
	peerID string,
	mid string,
	authorID string,
	groupID string,
	timestamp int64,
	text string,
	kinds []string,
) string {
	if mid != "" {
		return "wechat:" + string(chatType) + ":" + peerID + ":" + mid
	}
	hasher := sha256.New()
	writeWechatAgentCanonicalString(hasher, string(chatType))
	writeWechatAgentCanonicalString(hasher, peerID)
	writeWechatAgentCanonicalString(hasher, authorID)
	writeWechatAgentCanonicalString(hasher, groupID)
	writeWechatAgentCanonicalString(hasher, strconv.FormatInt(timestamp, 10))
	writeWechatAgentCanonicalString(hasher, text)
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(kinds)))
	_, _ = hasher.Write(count[:])
	for _, kind := range kinds {
		writeWechatAgentCanonicalString(hasher, kind)
	}
	return "wechat:sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func writeWechatAgentCanonicalString(writer io.Writer, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = writer.Write(length[:])
	_, _ = writer.Write([]byte(value))
}
