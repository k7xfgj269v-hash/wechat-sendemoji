// 入向订阅：连 onebot 的 ws://<host>/ws，把消息帧回调出去。
//
// onebot 的 /ws（websocket.go:24 handleWebSocketWithAgent）本来是给「WS 命令通道」用的：
// 客户端发 {action, echo, params}，服务端回 {echo, status, data}；
// 同时服务端会把收到的微信消息用 SendWebSocketMsg（websocket.go:148）往同一个 conn 推。
// 所以本插件只订阅、只读，不发任何 action；出向一律走 HTTP（见 outbound.js）。
// 注意：onebot 只保存最后一个 conn（全局变量 conn），别的 WS 客户端连上来会把它顶掉。

const RECONNECT_MIN = 2000
const RECONNECT_MAX = 30000

/** 是不是一条「消息」帧（而不是 {echo,status} 这种命令回包）。 */
export function looksLikeMessageFrame(data) {
  if (!data || typeof data !== 'object') return false
  if (data.echo !== undefined || data.retcode !== undefined) return false
  if (Array.isArray(data.message)) return true
  return Boolean(data.user_id || data.message_type || data.post_type === 'message')
}

/**
 * @param {object} o
 * @param {() => string} o.getWsUrl 每次连接现取
 * @param {(frame: object) => void} o.onEvent 收到消息帧
 * @param {(state: string, detail?: string) => void} [o.onState]
 * @param {{info:Function,warn:Function,error:Function,debug:Function}} o.log
 * @param {typeof WebSocket} [o.WebSocketImpl] 测试时注入
 */
export function createOneBotClient({
  getWsUrl,
  onEvent,
  onState,
  log,
  WebSocketImpl = globalThis.WebSocket,
  reconnectMinMs = RECONNECT_MIN,
  reconnectMaxMs = RECONNECT_MAX,
}) {
  let socket = null
  let stopped = true
  let timer = null
  let backoff = reconnectMinMs
  let state = 'stopped'
  const stats = { frames: 0, ignored: 0, connects: 0, closes: 0 }

  const setState = (next, detail) => {
    if (state === next) return
    state = next
    try {
      onState?.(next, detail)
    } catch {
      // 状态回调出错不影响连接
    }
  }

  function schedule() {
    clearTimeout(timer)
    if (stopped) return
    timer = setTimeout(connect, backoff)
    timer.unref?.()
    backoff = Math.min(reconnectMaxMs, backoff * 2)
  }

  function connect() {
    if (stopped) return
    if (typeof WebSocketImpl !== 'function') {
      setState('unavailable', '当前 Node 没有 WebSocket（需要 Node 22+）')
      return
    }
    let url
    try {
      url = String(getWsUrl() || '')
    } catch (error) {
      log.error('取 WS 地址失败：', error)
      schedule()
      return
    }
    if (!url) {
      setState('unconfigured', '没有 WS 地址')
      schedule()
      return
    }
    setState('connecting', url)
    let sock
    try {
      sock = new WebSocketImpl(url)
    } catch (error) {
      log.warn('WS 连接失败：', error)
      setState('disconnected', String(error?.message || error))
      schedule()
      return
    }
    socket = sock

    sock.onopen = () => {
      backoff = reconnectMinMs
      stats.connects++
      setState('connected', url)
      log.info(`已连上 onebot 事件流：${url}`)
    }

    sock.onmessage = (event) => {
      const raw = typeof event?.data === 'string' ? event.data : Buffer.from(event?.data ?? '').toString('utf8')
      if (!raw) return
      let data
      try {
        data = JSON.parse(raw)
      } catch {
        stats.ignored++
        log.debug('收到非 JSON 帧，忽略')
        return
      }
      if (!looksLikeMessageFrame(data)) {
        stats.ignored++
        return
      }
      stats.frames++
      try {
        onEvent?.(data)
      } catch (error) {
        log.error('处理消息帧出错：', error)
      }
    }

    sock.onclose = (event) => {
      if (socket === sock) socket = null
      if (stopped) return
      stats.closes++
      setState('disconnected', `code ${event?.code ?? ''}`)
      schedule()
    }

    sock.onerror = (event) => {
      log.debug('WS 出错：', event?.message || event?.error?.message || 'unknown')
    }
  }

  return {
    start() {
      if (!stopped) return
      stopped = false
      backoff = reconnectMinMs
      connect()
    },
    stop() {
      stopped = true
      clearTimeout(timer)
      timer = null
      const sock = socket
      socket = null
      setState('stopped')
      try {
        sock?.close?.()
      } catch {
        // 关连接失败不影响收尾
      }
    },
    restart() {
      this.stop()
      this.start()
    },
    get state() {
      return state
    },
    stats() {
      return { ...stats }
    },
  }
}
