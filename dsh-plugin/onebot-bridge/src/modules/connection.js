// 模块①：onebot 事件流（WS 订阅）。只管「连上、断了重连、把帧丢进总线」。
// 停用这个模块 = WS 关闭 + 重连定时器清掉（client.stop() 做这两件事）。

import { createOneBotClient } from '../client.js'
import { wsUrlOf } from '../config.js'

export function connectionModule() {
  return {
    id: 'connection',
    title: 'onebot 事件流（WS 订阅）',
    factory({ getConfig, log, bus }) {
      let lastDetail = ''
      const client = createOneBotClient({
        getWsUrl: () => wsUrlOf(getConfig()),
        log,
        onState: (state, detail) => {
          lastDetail = detail || ''
          if (state === 'connected') log.info(`事件流已连接${detail ? `（${detail}）` : ''}`)
          else if (state === 'stopped') log.info('事件流已停止')
          else log.warn(`事件流：${state}${detail ? `（${detail}）` : ''}`)
          bus.emit('connection/state', state, detail)
        },
        onEvent: (raw) => bus.emit('frame', raw),
      })
      return {
        client,
        start() {
          client.start()
        },
        stop() {
          client.stop()
        },
        stats: () => client.stats(),
        get state() {
          return client.state
        },
        get detail() {
          return lastDetail
        },
      }
    },
  }
}
