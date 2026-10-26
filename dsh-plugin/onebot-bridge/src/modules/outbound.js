// 模块④：出向发送。它拥有传输层 + dryRun/总开关闸门 + 拆条/节奏/表情决策。
//
// v3（CONTRACT §7.3）：发什么、发几条、怎么拆、间隔多久、配不配表情 —— 都在这里算，
// Go 侧只收 `POST /api/send/text|sticker|image` 的薄指令。
//
// 停用这个模块 = 一个字节都不会发到微信（句柄没了，会话模块拿到 null），
// 但入向/会话照常跑（方便「只听不发」地观察模型会说什么）。
//
// token 由入口注入（getToken 每次现取），模块自己不读环境变量、不碰凭据服务。

import { createApiTransport, createSender, createOutbound } from '../outbound.js'
import { createStickerPicker } from '../stickers.js'
import { brief } from '../log.js'

export function outboundModule({ getToken = () => '' } = {}) {
  return {
    id: 'outbound',
    title: '出向发送（拆条/节奏/表情 + dryRun 闸门）',
    factory({ getConfig, log }) {
      const http = () => getConfig()?.service?.httpUrl
      const onError = (message) => log.warn(message)

      const transport = createApiTransport({
        getHttpUrl: http,
        getToken,
        timeoutMs: Number(getConfig()?.service?.sendTimeoutMs) || 15000,
        onError,
      })

      // 表情决策：读 T2 的表情库索引（只读），命中标签优先，否则按概率
      const pickSticker = createStickerPicker({ getConfig, log })

      // 老出口（deprecated）：只给 outbound.legacyEndpoint 回退用
      const legacy = createSender({
        getHttpUrl: http,
        timeoutMs: Number(getConfig()?.service?.sendTimeoutMs) || 15000,
        getToken,
        onError,
      })
      const legacyTransport = {
        async sendText({ target, text }) {
          const r = await legacy.send(target, text)
          return r.ok ? { ok: true } : { ok: false, reason: r.error || r.status || 'legacy-send-failed' }
        },
        // 老出口没有表情/图片（§7.3 之前表情发送没接）；显式失败，好让上层「静默跳过」
        async sendSticker() {
          return { ok: false, reason: 'legacy-endpoint-no-sticker' }
        },
        async sendImage() {
          return { ok: false, reason: 'legacy-endpoint-no-image' }
        },
      }

      const gate = createOutbound({
        getConfig,
        // 每次发送现取：改 legacyEndpoint 不用重启模块
        transport: () => (getConfig()?.outbound?.legacyEndpoint ? legacyTransport : transport),
        log,
        brief,
        pickSticker,
      })

      return {
        send: gate.send,
        flush: gate.flush,
        flushAll: gate.flushAll,
        deliver: gate.deliver,
        pending: gate.pending,
        stats: gate.stats,
        endpoint: () => (getConfig()?.outbound?.legacyEndpoint ? 'legacy(/send_*)' : 'api(/api/send/*)'),
      }
    },
  }
}
