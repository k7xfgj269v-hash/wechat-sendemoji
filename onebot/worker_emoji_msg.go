package main

// sendemoji（表情消息）请求体构建。
//
// 依据：/private/tmp/wxre2/sendemoji-sample.md 与 sendemoji-sample.hex 的实捕样本
// （211 字节，落点 wechat.dylib + 0x3e58f14，sp+0x140 → holder）。
//
// EmojiSendRequest {
//   1: EnvelopeHeader {          // 64 字节
//        1: bytes  = 00
//        2: varint = sessionId
//        3: string = 16 字符 client proof
//        4: varint = deviceId (u64)
//        5: string = "UnifiedPCMac 26 arm64"
//        6: varint = 逐条递增的计数
//   }
//   2: varint = 1
//   3: EmojiMessage {            // 140 字节
//        1: string = 表情 md5（32 位小写 hex 字符串）
//        2: varint = 0
//        3: varint = 18585（含义未知，样本原样）
//        4: msg { 1: varint = 0, 2: string = "" }   // 恒为空对
//        5: varint = 2
//        6: string = 接收方
//        7: string = gameext XML
//        9: string = clientMsgId
//   }
// }
//
// 关键结论（见样本文档 §已确定的结论）：body 里没有 cdnurl / aeskey / 宽高 / 上传凭据，
// 服务端**按 md5 反查表情**。因此这里既不上传也不构造图片消息：拿不到可发的 md5 就报错。
//
// 手写 protobuf 编码的理由：样本显式编码了若干 0 值字段（f1.1 = 00、f3.2 = 0、
// f3.4 内层 {1:0, 2:""}），proto3 的库会把这些字段连同 tag 一起省掉，无法复现字节。

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"
)

// emojiSendMsgType 是 SendMsg.Type 与 script.js triggerSendEmojiMessage 共用的 kind 名。
const emojiSendMsgType = "emoji"

// emojiSendCgiPath 是 sendemoji 这条 cgi 的路径。payload 里的一个字节是它的长度
// （见 BuildEmojiSendPayload 的说明），因此必须与 script.js 里的串保持一致。
const emojiSendCgiPath = "/cgi-bin/micromsg-bin/sendemoji"

// emojiPlatformString 与 img/voice builder 写进信封的 platform 一致（同一个客户端版本串）。
const emojiPlatformString = "UnifiedPCMac 26 arm64"

// emojiGameExtXML 是样本里 f3.7 的固定值（41 字符，注意 content="0" 后面有一个空格）。
const emojiGameExtXML = `<gameext type="0" content="0" ></gameext>`

// emojiClientProofLength 是信封 f1.3 的固定长度（16 字符），样本为 16。
const emojiClientProofLength = 16

// 样本里含义未定的常量，按其原值透传（同一台机器上稳定）。
const (
	emojiMsgUnknownField3 = 18585 // 疑似与 md5 相关，未定
	emojiMsgUnknownField5 = 2     // 未定
	emojiRequestField2    = 1     // 顶层 f2，样本为 1，未定
)

// emojiClientMsgIDSuffix 沿用 img builder 的 `_xwechat_3` 尾巴。
// 注意：样本实际是 `..._82_xwechat_33`（35 字符），尾巴像客户端侧的自由编号；
// 若真机校验这一位，可用 EmojiSendParams.ClientMsgID 逐字覆盖。
const emojiClientMsgIDSuffix = "_xwechat_3"

// emojiEnvelopeCounter 是信封 f1.6。样本里同一台机器的两条请求分别是 1297 / 1303
// （monotonic，跨 cgi 递增），所以这里用进程内自增；1 起计数，与样本绝对数值不同但同形。
var emojiEnvelopeCounter atomic.Uint64

// EmojiSendParams 是 BuildEmojiMsgProto 的可选覆盖项。
// 零值表示"从进程全局取"，便于真机对照与字节级单测逐字复现样本。
type EmojiSendParams struct {
	Md5         string // 必填：32 位 hex（大小写不敏感，输出统一小写）
	ClientMsgID string // 可选：完整 clientMsgId；空则按 <to>_<unix>_<rand>_xwechat_3 生成
	SessionID   uint32 // 可选：覆盖 globalSessionId
	DeviceID    uint64 // 可选：覆盖 globalDeviceId
	ClientProof []byte // 可选：覆盖 globalClientProof（必须 16 字节）
	Sequence    uint64 // 可选：覆盖 f1.6 计数
}

// BuildEmojiMsgProto 构建 sendemoji 请求体，返回小写 hex 字符串。
//
// selfID 只做参数校验：实捕 body 里没有发送方字段（f3.6 只有接收方），
// 传空说明调用方拿不到自身 wxid，属调用错误，直接报错而不是猜。
func BuildEmojiMsgProto(selfID, toID string, p EmojiSendParams) (string, error) {
	if strings.TrimSpace(selfID) == "" {
		return "", errors.New("emoji: sender id is required")
	}
	receiver := strings.TrimSpace(toID)
	if receiver == "" {
		return "", errors.New("emoji: receiver is required")
	}
	md5Hex, err := normalizeEmojiMD5(p.Md5)
	if err != nil {
		return "", err
	}
	proof, err := emojiClientProof(p.ClientProof)
	if err != nil {
		return "", err
	}
	sessionID := p.SessionID
	if sessionID == 0 {
		sessionID = globalSessionId
	}
	if sessionID == 0 {
		return "", errors.New("emoji: session id is unavailable")
	}
	deviceID := p.DeviceID
	if deviceID == 0 {
		deviceID = globalDeviceId
	}
	if deviceID == 0 {
		return "", errors.New("emoji: device id is unavailable")
	}
	sequence := p.Sequence
	if sequence == 0 {
		sequence = emojiEnvelopeCounter.Add(1)
	}
	clientMsgID := strings.TrimSpace(p.ClientMsgID)
	if clientMsgID == "" {
		clientMsgID = buildEmojiClientMsgID(receiver)
	}

	header := make([]byte, 0, 64)
	header = appendBytesField(header, 1, []byte{0x00})
	header = appendVarintField(header, 2, uint64(sessionID))
	header = appendBytesField(header, 3, proof)
	header = appendVarintField(header, 4, deviceID)
	header = appendBytesField(header, 5, []byte(emojiPlatformString))
	header = appendVarintField(header, 6, sequence)

	message := make([]byte, 0, 140)
	message = appendBytesField(message, 1, []byte(md5Hex))
	message = appendVarintField(message, 2, 0)
	message = appendVarintField(message, 3, emojiMsgUnknownField3)
	// f3.4 恒为 {1: 0, 2: ""}：两个 0 值字段必须显式编码，proto3 会省掉。
	emptyPair := appendVarintField(nil, 1, 0)
	emptyPair = appendBytesField(emptyPair, 2, nil)
	message = appendBytesField(message, 4, emptyPair)
	message = appendVarintField(message, 5, emojiMsgUnknownField5)
	message = appendBytesField(message, 6, []byte(receiver))
	message = appendBytesField(message, 7, []byte(emojiGameExtXML))
	message = appendBytesField(message, 9, []byte(clientMsgID))

	body := make([]byte, 0, len(header)+len(message)+16)
	body = appendBytesField(body, 1, header)
	body = appendVarintField(body, 2, emojiRequestField2)
	body = appendBytesField(body, 3, message)
	return hex.EncodeToString(body), nil
}

// BuildEmojiSendPayload 构建 MMStartTask 的 payload（0x1A0 字节）hex。
//
// 复用 BuildSendPayload 的媒体类模板（[0]=0x6E / [16]=0x10 / [92]=0x6E，与 img/video/voice
// 相同：script.js 里 emoji 的 message 结构与它们逐字段同构），只把 "cgi 长度" 那个字节
// 改成 sendemoji 的长度。
//
// 该字节的证据：完整 payload 偏移 0x20（= taskId 4 字节 + payloadData[0x1C]）对每个现有
// kind 都恰好等于其 cgi 串长度 —— text 0x20(32, newsendmsg)、img 0x22(34, uploadmsgimg)、
// video 0x21(33)、voice 0x21(33)、reply 0x20(32)、file 0x20(32)、fileupload 0x27(39)、
// appattach 0x25(37)；sendemoji 长 31 = 0x1F，与 script.js 里 emoji 结构的 +0x20 注释一致。
func BuildEmojiSendPayload(taskID int64) (string, error) {
	if len(emojiSendCgiPath) > 0xff {
		return "", errors.New("emoji: cgi path is too long for the payload layout")
	}
	encoded, err := hex.DecodeString(BuildSendPayload(taskID, "img"))
	if err != nil {
		return "", fmt.Errorf("emoji: payload template is invalid: %w", err)
	}
	const cgiLengthOffset = 0x20 // taskId(4) + payloadData[0x1C]
	if len(encoded) <= cgiLengthOffset {
		return "", errors.New("emoji: payload template is too short")
	}
	encoded[cgiLengthOffset] = byte(len(emojiSendCgiPath))
	return hex.EncodeToString(encoded), nil
}

// buildEmojiClientMsgID 生成 <toID>_<unix>_<rand>_xwechat_3（与 img builder 同风格）。
func buildEmojiClientMsgID(receiver string) string {
	return strings.Join([]string{
		receiver,
		fmt.Sprintf("%d", time.Now().Unix()),
		fmt.Sprintf("%d", rand.Int63n(1000)),
		"xwechat_3",
	}, "_")
}

// normalizeEmojiMD5 校验并归一化表情 md5：必须是 32 位 hex，输出小写。
func normalizeEmojiMD5(value string) (string, error) {
	md5Hex := strings.ToLower(strings.TrimSpace(value))
	if md5Hex == "" {
		return "", errors.New("emoji: md5 is required")
	}
	if len(md5Hex) != 32 {
		return "", fmt.Errorf("emoji: md5 must be 32 hex characters, got %d", len(md5Hex))
	}
	if _, err := hex.DecodeString(md5Hex); err != nil {
		return "", errors.New("emoji: md5 is not hexadecimal")
	}
	return md5Hex, nil
}

// emojiClientProof 取信封 f1.3：进程全局的 client proof，长度必须恰为 16。
func emojiClientProof(override []byte) ([]byte, error) {
	proof := override
	if len(proof) == 0 {
		proof = globalClientProof
	}
	if len(proof) != emojiClientProofLength {
		return nil, fmt.Errorf(
			"emoji: client proof must be %d bytes, got %d",
			emojiClientProofLength, len(proof),
		)
	}
	return append([]byte(nil), proof...), nil
}

// appendVarint 追加一个 base-128 varint。
func appendVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

// appendTag 追加 field tag（wire type 0 = varint，2 = length-delimited）。
func appendTag(dst []byte, field int, wireType byte) []byte {
	return appendVarint(dst, uint64(field)<<3|uint64(wireType))
}

// appendVarintField 追加 varint 字段（含显式 0 值）。
func appendVarintField(dst []byte, field int, value uint64) []byte {
	dst = appendTag(dst, field, 0)
	return appendVarint(dst, value)
}

// appendBytesField 追加 length-delimited 字段（空切片也写出 tag + 长度 0）。
func appendBytesField(dst []byte, field int, value []byte) []byte {
	dst = appendTag(dst, field, 2)
	dst = appendVarint(dst, uint64(len(value)))
	return append(dst, value...)
}
