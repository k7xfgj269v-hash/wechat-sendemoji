// 模块②：入向归一化。onebot 帧 → { chatId, chatKind, sender, senderName, text, msgType }。
// 只做形状转换，不碰 DSH：这样停用它就等于「只收不投」。
// 它注册的唯一东西是总线监听器，走 scope，停用时自动摘掉。

import { normalizeEvent } from '../inbound.js'
import { brief } from '../log.js'

export function inboundModule() {
  return {
    id: 'inbound',
    title: '入向归一化',
    factory({ log, bus, scope }) {
      let selfId = ''
      let handled = 0
      let skipped = 0

      scope.add(
        bus.on('frame', (raw) => {
          const r = normalizeEvent(raw, { selfId })
          if (!r.ok) {
            skipped++
            log.debug(`跳过一帧（${r.reason}）`)
            return
          }
          const msg = r.message
          if (msg.selfId) selfId = msg.selfId
          handled++
          log.info(`← ${msg.chatId} ${msg.senderName}：${brief(msg.text || `[${msg.msgType}]`)}`)
          bus.emit('message', msg)
        }),
      )

      return {
        stats: () => ({ handled, skipped, selfId }),
        get selfId() {
          return selfId
        },
      }
    },
  }
}
