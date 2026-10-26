package main

// 表情发送：从本地表情库取一条 → 构建 sendemoji 请求体 → 投递到发送队列。
//
// 关键设计（依据 /private/tmp/wxre2/sendemoji-sample.md）：
//   - sendemoji 的 body 只有表情 md5，没有 cdnurl / aeskey / 宽高 ⇒ 服务端按 md5 反查表情。
//   - 所以这里**不做上传**（不上 triggerUploadImg、不构造图片消息）：把一个本地图片上传后
//     再"发送"，出去的一定是图片而不是表情，属于契约明确禁止的降级。
//   - 拿不到 32 位 md5 / 目标 / 运行期信封参数时一律显式报错，绝不静默改发图片。
//
// 进程约束：真正执行 Frida 导出的是 wechat-agent 进程（msgChan + RunSendWorker 都在那边），
// 因此 sendSticker 必须在 wechat-agent 进程内调用；在 hr-workbench 进程里调用只会排进一个
// 没有消费者的队列并超时。

import (
	"context"
	"errors"
	"strings"
)

// emojiChatroomSuffix 是微信群聊 id 的后缀；其余目标视为私聊。
const emojiChatroomSuffix = "@chatroom"

// emojiSendParamsFromSticker 把表情库条目转成 sendemoji 参数。
//
// 与契约草稿的差别：草稿签名为 (item StickerItem, uploaded UploadResult)，但仓库里没有
// UploadResult 类型，且实捕 body 证明这条链路根本不存在上传步骤；故这里只吃 item。
func emojiSendParamsFromSticker(item StickerItem) (EmojiSendParams, error) {
	// 只有 md5 会进 body；本地文件路径不参与构建，故不在这里校验。
	md5Hex, err := normalizeEmojiMD5(item.MD5)
	if err != nil {
		return EmojiSendParams{}, err
	}
	return EmojiSendParams{Md5: md5Hex}, nil
}

// sendSticker 把一张表情库里的表情真正发出去。
//
// 成功 = triggerSendEmojiMessage 返回 1 且收到 buf2resp ack（由发送 worker 判定）；
// 失败原样返回错误，不做任何降级。成功后 MarkUsed 记一次使用。
func sendSticker(ctx context.Context, target string, item StickerItem) error {
	if ctx == nil {
		return errors.New("emoji: context is required")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return errors.New("emoji: target is required")
	}
	params, err := emojiSendParamsFromSticker(item)
	if err != nil {
		return err
	}
	// 先在入队前跑一次 builder：参数不全（如 client proof 长度不对）立即失败，
	// 而不是排进队列后由 worker 异步报错。
	if _, err := BuildEmojiMsgProto(myWechatId, target, params); err != nil {
		return err
	}
	if item.From != "" && item.From != stickerFromCollected {
		// 服务端只认识自己见过的 md5；手工/上传件多半不在其中，发出去会由服务端拒绝。
		Warn("表情不是接收收集来的，服务端可能不认识这个 md5",
			"md5", params.Md5, "from", item.From)
	}

	message := newEmojiSendMsg(target, params.Md5)
	select {
	case msgChan <- message:
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-message.ResultChan:
		if err != nil {
			return err
		}
	}
	if store := defaultStickerStore(); store != nil {
		if err := store.MarkUsed(item.MD5); err != nil {
			Warn("表情使用计数更新失败", "md5", item.MD5, "err", err)
		}
	}
	return nil
}

// newEmojiSendMsg 组装一条表情发送任务；结果通过 ResultChan 回传。
func newEmojiSendMsg(target, md5Hex string) *SendMsg {
	message := &SendMsg{
		Type:       emojiSendMsgType,
		Md5:        md5Hex,
		ResultChan: make(chan error, 1),
	}
	if strings.HasSuffix(target, emojiChatroomSuffix) {
		message.GroupID = target
	} else {
		message.UserId = target
	}
	return message
}

// sendStickerByMD5 是传输层窄入口：服务端只认 md5，所以只要能拿到 md5 就能发。
// 面板/HTTP 层将来接 /api/stickers/send 时直接调它即可（不需要先读本地文件）。
func sendStickerByMD5(ctx context.Context, target, md5Hex string) error {
	digest, err := normalizeEmojiMD5(md5Hex)
	if err != nil {
		return err
	}
	return sendSticker(ctx, target, StickerItem{MD5: digest})
}
