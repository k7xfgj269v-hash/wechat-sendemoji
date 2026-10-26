// 测试用的假 onebot：一个真 HTTP + 真 WebSocket 服务端。
//
// 为什么手写 WS 握手：Node 22 只有客户端 WebSocket，没有服务端；而「一根真 socket」才
// 能证明 src/client.js 是真的能连上 onebot 的 /ws，而不是只跟假对象打过招呼。
//
// 行为对齐真 onebot：
//   GET /ws（Upgrade）→ 101，之后服务端主动推 WechatMessage JSON（websocket.go:148 SendWebSocketMsg）
//   POST /send_private_msg、/send_group_msg → { "status": "ok" }（http.go:30 sendHandler）
//   另外记录每一个收到的 HTTP 请求，测试里断言 body 形状

import { createServer } from 'node:http'
import { createHash } from 'node:crypto'

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'

function encodeFrame(text) {
  const payload = Buffer.from(text, 'utf8')
  if (payload.length < 126) return Buffer.concat([Buffer.from([0x81, payload.length]), payload])
  if (payload.length < 65536) {
    const header = Buffer.alloc(4)
    header[0] = 0x81
    header[1] = 126
    header.writeUInt16BE(payload.length, 2)
    return Buffer.concat([header, payload])
  }
  const header = Buffer.alloc(10)
  header[0] = 0x81
  header[1] = 127
  header.writeBigUInt64BE(BigInt(payload.length), 2)
  return Buffer.concat([header, payload])
}

/** 解客户端帧（客户端发的帧一定带 mask）。返回 { frames: string[], closed: boolean }。 */
function decodeFrames(buffer, { masked = true } = {}) {
  const out = []
  let offset = 0
  while (offset + 2 <= buffer.length) {
    const b0 = buffer[offset]
    const b1 = buffer[offset + 1]
    const opcode = b0 & 0x0f
    const hasMask = Boolean(b1 & 0x80)
    let len = b1 & 0x7f
    let p = offset + 2
    if (len === 126) {
      if (p + 2 > buffer.length) break
      len = buffer.readUInt16BE(p)
      p += 2
    } else if (len === 127) {
      if (p + 8 > buffer.length) break
      len = Number(buffer.readBigUInt64BE(p))
      p += 8
    }
    let mask = null
    if (hasMask) {
      if (p + 4 > buffer.length) break
      mask = buffer.subarray(p, p + 4)
      p += 4
    }
    if (p + len > buffer.length) break
    const data = Buffer.from(buffer.subarray(p, p + len))
    if (mask && masked) for (let i = 0; i < data.length; i++) data[i] ^= mask[i % 4]
    if (opcode === 1) out.push(data.toString('utf8'))
    if (opcode === 8) out.push('__close__')
    offset = p + len
  }
  return { frames: out, rest: buffer.subarray(offset) }
}

/**
 * @param {object} [o]
 * @param {(payload: object) => object|undefined|Promise<object|undefined>} [o.onSend] 自定义 /send_* 的响应
 */
export async function startFakeOneBot({ onSend } = {}) {
  const sockets = new Set()
  const requests = []
  const received = [] // 客户端（插件）发上来的 WS 帧
  const wsClients = new Set()

  const server = createServer(async (req, res) => {
    const chunks = []
    for await (const c of req) chunks.push(c)
    const raw = Buffer.concat(chunks).toString('utf8')
    let body = null
    try {
      body = raw ? JSON.parse(raw) : null
    } catch {
      body = null
    }
    const entry = { method: req.method, url: req.url, headers: req.headers, body, raw }
    requests.push(entry)

    const legacySend = req.url === '/send_private_msg' || req.url === '/send_group_msg'
    const apiSend = typeof req.url === 'string' && req.url.startsWith('/api/send/')

    const reply = (status, json) => {
      res.statusCode = status
      res.setHeader('content-type', 'application/json')
      res.end(JSON.stringify(json))
    }

    if (req.method === 'POST' && (legacySend || apiSend)) {
      // onSend 优先：测试里用它造失败/自定义响应
      const custom = onSend ? await onSend(body, entry) : undefined
      if (custom) {
        res.statusCode = custom.status ?? 200
        res.setHeader('content-type', 'application/json')
        res.end(JSON.stringify(custom.json ?? (apiSend ? { ok: true } : { status: 'ok' })))
        return
      }
      if (apiSend) {
        // §7.2 薄接口：{ ok, task_id, queued_at }；缺 target 就显式失败
        if (!body || !body.target) return reply(400, { ok: false, reason: 'missing target' })
        if (req.url === '/api/send/text' && !String(body.text ?? '').trim()) return reply(400, { ok: false, reason: 'empty text' })
        if (req.url === '/api/send/sticker' && !String(body.name ?? '').trim()) return reply(400, { ok: false, reason: 'missing name' })
        return reply(200, { ok: true, task_id: `task-${requests.length}`, queued_at: Date.now() })
      }
      // 老出口（deprecated）
      if (!body || (!body.user_id && !body.group_id)) {
        res.statusCode = 400
        res.setHeader('content-type', 'text/plain')
        res.end('必须指定且只能指定一个接收方')
        return
      }
      return reply(200, { status: 'ok' })
    }
    res.statusCode = 404
    res.end('not found')
  })

  server.on('upgrade', (req, socket) => {
    const key = req.headers['sec-websocket-key'] || ''
    const accept = createHash('sha1').update(key + GUID).digest('base64')
    socket.write(
      ['HTTP/1.1 101 Switching Protocols', 'Upgrade: websocket', 'Connection: Upgrade', `Sec-WebSocket-Accept: ${accept}`, '', ''].join('\r\n'),
    )
    sockets.add(socket)
    wsClients.add(socket)
    let buffer = Buffer.alloc(0)
    socket.on('data', (chunk) => {
      buffer = Buffer.concat([buffer, chunk])
      const { frames, rest } = decodeFrames(buffer)
      buffer = rest
      for (const f of frames) {
        if (f === '__close__') {
          socket.end()
          return
        }
        received.push(f)
        // 插件不该发 action；万一发了，也照 onebot 的形状回一个 ok，免得测试卡住
        try {
          const msg = JSON.parse(f)
          if (msg && msg.echo) socket.write(encodeFrame(JSON.stringify({ echo: msg.echo, status: 'ok' })))
        } catch {
          // 非 JSON 帧直接忽略
        }
      }
    })
    const drop = () => {
      sockets.delete(socket)
      wsClients.delete(socket)
    }
    socket.on('close', drop)
    socket.on('error', drop)
  })

  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()

  return {
    port,
    httpUrl: `http://127.0.0.1:${port}`,
    wsUrl: `ws://127.0.0.1:${port}/ws`,
    requests,
    received,
    /** 往所有连上来的客户端推一帧（模拟 onebot 的 SendWebSocketMsg）。 */
    push(payload) {
      const text = typeof payload === 'string' ? payload : JSON.stringify(payload)
      for (const s of wsClients) s.write(encodeFrame(text))
      return wsClients.size
    },
    get wsClients() {
      return wsClients.size
    },
    /** 掐断所有 WS 连接（服务端还在听），用来测重连。 */
    drop() {
      for (const s of wsClients) {
        wsClients.delete(s)
        sockets.delete(s)
        try {
          s.destroy()
        } catch {
          // 已断开
        }
      }
      return sockets.size
    },
    async close() {
      for (const s of sockets) {
        try {
          s.destroy()
        } catch {
          // 已断开
        }
      }
      await new Promise((resolve) => server.close(resolve))
    },
  }
}

/** 一条最普通的 onebot 入向帧（形状照 protobuf_handler.go:233）。 */
export function frame({ groupId = '', selfId = 'wxid_self', userId = 'wxid_a', nickname = '张三', segments = [{ type: 'text', data: { text: '在吗' } }], messageId = '1001', time = 1720000000000 } = {}) {
  return {
    group_id: groupId,
    self_id: selfId,
    user_id: userId,
    sender: { user_id: userId, nickname },
    time,
    post_type: 'message',
    message_id: messageId,
    message: segments,
    msgsource: '<msgsource />',
    raw_message: '在吗',
    show_content: '在吗',
    message_type: groupId ? 'group' : 'private',
  }
}
