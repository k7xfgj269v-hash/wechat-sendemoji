import test from 'node:test'
import assert from 'node:assert/strict'
import { createOneBotClient } from '../src/client.js'
import { normalizeEvent } from '../src/inbound.js'
import { startFakeOneBot, frame } from './fake-onebot.js'

const silentLog = { info() {}, warn() {}, error() {}, debug() {} }

const waitFor = async (fn, { timeout = 4000, interval = 20 } = {}) => {
  const started = Date.now()
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() - started > timeout) throw new Error('等待超时')
    await new Promise((r) => setTimeout(r, interval))
  }
}

test('真 WS：连上假 onebot /ws，收到推送的消息帧并归一化', async () => {
  const bot = await startFakeOneBot()
  const events = []
  const states = []
  const client = createOneBotClient({
    getWsUrl: () => bot.wsUrl,
    onEvent: (raw) => events.push(normalizeEvent(raw, { selfId: 'wxid_self' })),
    onState: (s) => states.push(s),
    log: silentLog,
    reconnectMinMs: 20,
    reconnectMaxMs: 50,
  })
  try {
    client.start()
    await waitFor(() => client.state === 'connected')
    await waitFor(() => bot.wsClients === 1)
    assert.deepEqual(states.slice(0, 2), ['connecting', 'connected'])

    bot.push(frame({ userId: 'wxid_a', nickname: '张三', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    const got = await waitFor(() => events[0])
    assert.equal(got.ok, true)
    assert.equal(got.message.chatId, 'private:wxid_a')
    assert.equal(got.message.text, '在吗')

    bot.push(frame({ groupId: 'g1', userId: 'wxid_b', nickname: '李四', segments: [{ type: 'text', data: { text: '大家好' } }] }))
    await waitFor(() => events.length === 2)
    assert.equal(events[1].message.chatId, 'group:g1')
  } finally {
    client.stop()
    await bot.close()
  }
})

test('命令回包（{echo,status}）不进业务回调；坏 JSON 也不炸', async () => {
  const bot = await startFakeOneBot()
  const events = []
  const client = createOneBotClient({
    getWsUrl: () => bot.wsUrl,
    onEvent: (raw) => events.push(raw),
    log: silentLog,
    reconnectMinMs: 20,
    reconnectMaxMs: 50,
  })
  try {
    client.start()
    await waitFor(() => client.state === 'connected')
    await waitFor(() => bot.wsClients === 1)
    bot.push({ echo: 'ab-1', status: 'ok', data: { user_id: 'wxid_self' } })
    bot.push('{ not json')
    bot.push({ retcode: 0, status: 'ok' })
    await new Promise((r) => setTimeout(r, 120))
    assert.equal(events.length, 0)
    assert.ok(client.stats().ignored >= 2)
  } finally {
    client.stop()
    await bot.close()
  }
})

test('断线自动重连：服务端掐断连接后重新连上并继续收消息', async () => {
  const bot = await startFakeOneBot()
  const events = []
  const states = []
  const client = createOneBotClient({
    getWsUrl: () => bot.wsUrl,
    onEvent: (raw) => events.push(raw),
    onState: (s) => states.push(s),
    log: silentLog,
    reconnectMinMs: 20,
    reconnectMaxMs: 50,
  })
  try {
    client.start()
    await waitFor(() => client.state === 'connected')
    await waitFor(() => bot.wsClients === 1)
    assert.equal(client.stats().connects, 1)

    bot.drop()
    await waitFor(() => client.stats().connects >= 2)
    await waitFor(() => client.state === 'connected')
    assert.ok(states.includes('disconnected'))

    bot.push(frame({ userId: 'wxid_a', segments: [{ type: 'text', data: { text: '重连之后还能收' } }] }))
    const got = await waitFor(() => events[0] && normalizeEvent(events[0], { selfId: 'wxid_self' }))
    assert.equal(got.message.text, '重连之后还能收')
  } finally {
    client.stop()
    await bot.close()
  }
})

test('没有 WebSocket 实现时降级成 unavailable，不抛异常', async () => {
  const client = createOneBotClient({
    getWsUrl: () => 'ws://127.0.0.1:1/ws',
    onEvent: () => {},
    log: silentLog,
    WebSocketImpl: null,
  })
  client.start()
  assert.equal(client.state, 'unavailable')
  client.stop()
  assert.equal(client.state, 'stopped')
})

test('stop() 之后不再重连', async () => {
  const bot = await startFakeOneBot()
  const client = createOneBotClient({
    getWsUrl: () => bot.wsUrl,
    onEvent: () => {},
    log: silentLog,
    reconnectMinMs: 20,
    reconnectMaxMs: 50,
  })
  try {
    client.start()
    await waitFor(() => client.state === 'connected')
    client.stop()
    assert.equal(client.state, 'stopped')
    const connects = client.stats().connects
    await new Promise((r) => setTimeout(r, 150))
    assert.equal(client.stats().connects, connects)
  } finally {
    await bot.close()
  }
})
