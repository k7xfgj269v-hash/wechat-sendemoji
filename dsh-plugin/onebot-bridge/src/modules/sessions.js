// 模块③：会话映射（一个微信 chat ↔ 一个持久 DSH 会话）。
// 它注册两样东西，都进 scope：
//   - 总线监听器（收到归一化消息 → 投进会话）
//   - ctx.on('session/event')（一轮结束 → 取正文 → 找出向模块发回去）
// 所以停用这个模块 = 不再建会话、不再监听会话事件、不再往 DSH 投任何东西。

import { createHub } from '../hub.js'
import { createReplyTool } from '../tools.js'

export function sessionsModule() {
  return {
    id: 'sessions',
    title: '会话映射（chat ↔ DSH 会话）',
    factory({ ctx, getConfig, log, bus, scope, modules }) {
      // 出向在调用时现查：outbound 模块关着 / 起不来，这里拿到 null，那条回复就只记日志。
      const outbound = {
        async send(chatId, text, opts) {
          const m = modules.service('outbound')
          if (!m) return { ok: false, skipped: 'outbound-module-off' }
          return m.send(chatId, text, opts)
        },
        stats: () => modules.service('outbound')?.stats?.() || {},
      }

      const hub = createHub({
        ctx,
        getConfig,
        log,
        outbound,
        buildReplyTool: (chat) => createReplyTool({ chat, hub }),
      })

      scope.add(
        bus.on('message', (msg) => {
          hub.onMessage(msg).catch((error) => log.error('处理入向消息出错：', error?.message || error))
        }),
      )

      if (typeof ctx?.on === 'function') {
        try {
          const off = ctx.on('session/event', (session, event) => {
            hub.onSessionEvent(session, event).catch((error) => log.warn('会话事件处理出错：', error?.message || error))
          })
          scope.add(off)
        } catch (error) {
          log.warn('挂 session/event 失败（会话回复不会自动发回微信）：', error?.message || error)
        }
      } else {
        log.warn('宿主没有 ctx.on：会话回复不会自动发回微信')
      }

      return { hub }
    },
  }
}
