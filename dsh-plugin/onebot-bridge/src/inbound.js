// 入向归一化：把 onebot 推到 WS 上的原始帧，变成插件内部统一的一条消息。
//
// onebot 侧的真实形状（protobuf_handler.go:233 构造、websocket.go:148 SendWebSocketMsg 下发，
// 注意它【不保证】是标准 OneBot v11，字段以 WechatMessage 结构体为准）：
//   {
//     "group_id": "", "self_id": "<自己>", "user_id": "<发送者 wxid>",
//     "sender": { "user_id": "...", "nickname": "..." },
//     "time": 1720000000000, "post_type": "message", "message_id": "...",
//     "message": [ { "type": "text", "data": { "text": "..." } } ],
//     "msgsource": "<xml>", "raw_message": "...", "show_content": "...",
//     "message_type": "private" | "group"
//   }
// message 段的 type 取值（protobuf_handler.go:278 classifyMessage）：
//   text / image / record / share / file / face / video / sys

const PLACEHOLDER = {
  image: '[图片]',
  record: '[语音]',
  video: '[视频]',
  face: '[表情]',
  share: '[链接]',
  sys: '',
}

/** 把消息段数组压成一行可读文本。找不到的段给个占位，别让模型以为什么都没有。 */
export function segmentsToText(segments = []) {
  const parts = []
  for (const seg of Array.isArray(segments) ? segments : []) {
    if (!seg || typeof seg !== 'object') continue
    const data = seg.data && typeof seg.data === 'object' ? seg.data : {}
    switch (seg.type) {
      case 'text':
        if (typeof data.text === 'string') parts.push(data.text)
        break
      case 'file':
        parts.push(`[文件 ${data.name || data.file || ''}]`.replace(/\s+\]/, ']'))
        break
      case 'at':
        parts.push(`@${data.name || data.qq || ''}`.trim())
        break
      default:
        parts.push(PLACEHOLDER[seg.type] !== undefined ? PLACEHOLDER[seg.type] : `[${seg.type}]`)
    }
  }
  return parts.join('').replace(/[ \t]+/g, ' ').replace(/\s*\n\s*/g, '\n').trim()
}

/** 内容类型：有文字就算 text，否则取第一个实质段的类型。 */
export function primaryType(segments = [], fallback = 'text') {
  const list = (Array.isArray(segments) ? segments : []).filter((s) => s && typeof s === 'object' && s.type !== 'sys')
  if (list.some((s) => s.type === 'text' && String(s?.data?.text ?? '').trim())) return 'text'
  return list[0]?.type || fallback
}

/**
 * 归一化一条原始事件。
 * @param {object} raw onebot 下发的一帧
 * @param {{ selfId?: string }} [opts] 已知的自己（用于过滤自己发的消息）
 * @returns {{ ok: true, message: object } | { ok: false, reason: string }}
 *   message = { chatId, chatKind, sender, senderName, text, msgType, messageId, at, selfId }
 */
export function normalizeEvent(raw, { selfId = '' } = {}) {
  if (!raw || typeof raw !== 'object') return { ok: false, reason: 'not-object' }
  // post_type 只在这个 onebot 的部分路径上存在；存在且不是 message 就跳过
  if (raw.post_type && raw.post_type !== 'message') return { ok: false, reason: `post_type:${raw.post_type}` }

  const segments = Array.isArray(raw.message) ? raw.message : []
  if (!segments.length) return { ok: false, reason: 'no-segments' }

  const groupId = String(raw.group_id ?? '').trim()
  const userId = String(raw.user_id ?? raw.sender?.user_id ?? '').trim()
  const self = String(selfId || raw.self_id || '').trim()
  if (!userId) return { ok: false, reason: 'no-user-id' }
  if (self && userId === self) return { ok: false, reason: 'self' }

  const chatKind = groupId ? 'group' : 'private'
  const chatId = `${chatKind}:${groupId || userId}`
  const text = segmentsToText(segments)
  const senderName = String(raw.sender?.nickname || raw.sender?.card || userId)

  return {
    ok: true,
    message: {
      chatId,
      chatKind,
      sender: userId,
      senderName,
      text,
      msgType: primaryType(segments),
      messageId: String(raw.message_id ?? ''),
      at: Number(raw.time) || Date.now(),
      selfId: self,
    },
  }
}

/** 交给模型的文案（私聊/群聊开头不一样，让模型知道自己站在哪儿）。 */
export function promptOf(msg) {
  if (msg.chatKind === 'group') return `【微信群 ${msg.chatId.slice('group:'.length)}】${msg.senderName}(${msg.sender})：${msg.text || '（没有文字）'}`
  return `【微信私聊】${msg.senderName}(${msg.sender})：${msg.text || '（没有文字）'}`
}
