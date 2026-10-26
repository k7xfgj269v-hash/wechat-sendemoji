// 端到端：假 onebot（真 HTTP + 真 WS）⇄ 插件本体 ⇄ DSH 桩
//
// 这条链路里「真的」的部分：真 WebSocket 连接与帧收发、真 HTTP POST 到 /send_private_msg、
// 真的配置加载与 dryRun 判定、真的入向归一化。
// 「桩」的部分：DSH 的 sessionController / agents / agent.followup / session/event
// ——它们只能证明本插件调用形状前后一致，真宿主上的联调见 README「待验证」。

import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, writeFileSync, mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { startFakeOneBot, frame } from './fake-onebot.js'

const dataDir = mkdtempSync(join(tmpdir(), 'onebot-bridge-e2e-'))
process.env.ONEBOT_BRIDGE_DATA_DIR = dataDir
process.env.ONEBOT_OPERATOR_TOKEN = 'test-token-should-never-be-logged'

const waitFor = async (fn, { timeout = 5000, interval = 20 } = {}) => {
  const started = Date.now()
  for (;;) {
    const v = await fn()
    if (v) return v
    if (Date.now() - started > timeout) throw new Error('等待超时')
    await new Promise((r) => setTimeout(r, interval))
  }
}

function writeConfig(patch) {
  mkdirSync(dataDir, { recursive: true })
  writeFileSync(
    join(dataDir, 'config.json'),
    JSON.stringify(
      {
        // 测试里不合并/不间隔，emoji 默认关（这台机器上真有一份表情库索引，
        // 不关掉会让断言随机多出一个 /api/send/sticker 请求）
        outbound: { intervalMs: 0, mergeWindowMs: 0 },
        sticker: { enabled: false },
        ...patch,
      },
      null,
      2,
    ),
  )
}

/** 最小 DSH 桩；返回 { ctx, calls, emit(sessionId, event) }。 */
function stubDsh() {
  const agents = new Map()
  const listeners = []
  const disposers = []
  const calls = { followup: [], create: [], variables: [] }
  const sc = {
    async create() {
      const sessionId = `session-${calls.create.length + 1}`
      calls.create.push({ sessionId })
      agents.set(sessionId, makeAgent(sessionId))
      return { sessionId }
    },
    async rename() {},
    async resolveAgent(sessionId) {
      const agent = agents.get(sessionId)
      return agent ? { agent } : { error: { code: 'NOT_FOUND', message: 'missing' } }
    },
  }
  function makeAgent(sessionId) {
    return {
      session: { id: sessionId },
      ctx: { tools: { register: () => () => {} }, systemPrompt: { context: () => () => {} } },
      followup(message) {
        calls.followup.push({ sessionId, message })
      },
    }
  }
  const ctx = {
    agents: { get: (id) => agents.get(id) },
    sessionController: sc,
    on(name, fn) {
      listeners.push({ name, fn })
      return () => {}
    },
    effect(fn) {
      const off = fn()
      if (typeof off === 'function') disposers.push(off)
      return off
    },
    interval(fn, ms) {
      const t = setInterval(fn, ms)
      t.unref?.()
      return () => clearInterval(t)
    },
    systemPrompt: {
      variable(name, fn) {
        calls.variables.push({ name, fn })
        return () => {}
      },
    },
    get(name) {
      if (name === 'credentials') return { resolve: async () => undefined }
      return undefined
    },
  }
  const emit = (sessionId, event) => {
    for (const l of listeners) if (l.name === 'session/event') l.fn({ id: sessionId }, event)
  }
  const dispose = () => {
    for (const off of disposers.splice(0)) {
      try {
        off()
      } catch {
        // 收尾失败不影响测试结论
      }
    }
  }
  return { ctx, calls, emit, dispose }
}

test('全链路 dryRun（默认）：WS 收到消息 → 进 DSH 会话 → 一轮结束 → 只记日志，一个 HTTP 请求都不发', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, service: { httpUrl: bot.httpUrl }, session: { enabled: true } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')

    const st = plugin.state()
    assert.equal(st.enabled, true)
    assert.equal(st.dryRun, true, '默认必须是试运行')
    assert.equal(st.token.present, true, 'token 从环境变量读到了')

    bot.push(frame({ userId: 'wxid_a', nickname: '张三', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    assert.match(dsh.calls.followup[0].message.content[0].text, /【微信私聊】张三\(wxid_a\)：在吗/)

    dsh.emit('session-1', { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '在的' }] } } })
    dsh.emit('session-1', { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await waitFor(() => plugin.state().session.stats.dryRun === 1)
    assert.equal(bot.requests.length, 0, 'dryRun 下不该有任何出向请求')
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('全链路真发：dryRun:false + enabled:true → 真的 POST /api/send/text（target 带会话前缀）', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')
    assert.equal(plugin.state().dryRun, false)

    bot.push(frame({ groupId: '', userId: 'wxid_b', nickname: '李四', segments: [{ type: 'text', data: { text: '你好' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId

    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '你好呀' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await waitFor(() => bot.requests.length === 1)
    const req = bot.requests[0]
    assert.equal(req.method, 'POST')
    assert.equal(req.url, '/api/send/text')
    assert.equal(req.body.target, 'private:wxid_b')
    assert.equal(req.body.text, '你好呀')
    // token 只走 Bearer 头，不进 body
    assert.equal(req.headers.authorization, 'Bearer test-token-should-never-be-logged')
    assert.equal(JSON.stringify(req.body).includes('test-token'), false)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('群聊：chatId 带 group: 前缀 → target 是 group:<群id>', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')

    bot.push(frame({ groupId: '123@chatroom', userId: 'wxid_c', nickname: '王五', segments: [{ type: 'text', data: { text: '大家好' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId
    assert.match(dsh.calls.followup[0].message.content[0].text, /【微信群 123@chatroom】/)

    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '欢迎' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await waitFor(() => bot.requests.length === 1)
    assert.equal(bot.requests[0].url, '/api/send/text')
    assert.equal(bot.requests[0].body.target, 'group:123@chatroom')
    assert.equal(bot.requests[0].body.text, '欢迎')
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('总开关关着（默认）：连都不连，也不收消息', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: false, service: { httpUrl: bot.httpUrl } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await new Promise((r) => setTimeout(r, 150))
    assert.equal(plugin.state().ws.state, 'stopped')
    assert.equal(bot.wsClients, 0)
    assert.equal(plugin.state().dryRun, true)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

// ---------------------------------------------------------------------------
// 对齐 au_rabot 的三件事：人格变量 / preset 与模块开关 / 收尾不留野连接
// ---------------------------------------------------------------------------

test('人格走提示词变量 {{onebot_persona}}（不是拼进消息），文本来自配置', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, service: { httpUrl: bot.httpUrl }, persona: { text: '你是微信里的猫娘。', file: '', maxChars: 20000 } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')

    const variable = dsh.calls.variables.find((v) => v.name === 'onebot_persona')
    assert.ok(variable, '必须注册 onebot_persona 变量（preset 的 persona 行渲染它）')
    assert.equal(variable.fn(), '你是微信里的猫娘。')
    assert.equal(plugin.state().persona.chars, '你是微信里的猫娘。'.length)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('模块开关：关掉 outbound → 会话照跑，但一个 HTTP 请求都不发', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl }, modules: { outbound: false } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')

    const mods = plugin.state().modules
    assert.equal(mods.find((m) => m.id === 'outbound').enabled, false)
    assert.equal(mods.find((m) => m.id === 'sessions').enabled, true)

    bot.push(frame({ userId: 'wxid_m', nickname: '小马', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '在的' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await new Promise((r) => setTimeout(r, 200))
    assert.equal(bot.requests.length, 0, '出向模块关着就不该有请求')
    assert.equal(plugin.state().session.stats.skipped >= 1, true)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('模块开关：关掉 sessions → 不建会话、不投递（只留在入向日志里）', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, service: { httpUrl: bot.httpUrl }, modules: { sessions: false } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')
    bot.push(frame({ userId: 'wxid_n', nickname: '小牛', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => plugin.state().inbound.handled === 1)
    await new Promise((r) => setTimeout(r, 100))
    assert.equal(dsh.calls.followup.length, 0)
    assert.equal(dsh.calls.create.length, 0)
    // 入向模块还在，说明是「只收不投」而不是整个断掉
    assert.equal(plugin.state().inbound.handled, 1)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('收尾：dispose 之后模块全停、WS 断开（HMR / 卸载不留野连接）', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, service: { httpUrl: bot.httpUrl } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')
    assert.equal(bot.wsClients, 1)

    dsh.dispose() // 走 ctx.effect 的收尾函数（= 宿主卸载 / HMR 重载）
    await waitFor(() => plugin.state().ws.state === 'stopped')
    assert.deepEqual(plugin.state().modules.map((m) => m.enabled), [false, false, false, false])
    await waitFor(() => bot.wsClients === 0)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

// ---------------------------------------------------------------------------
// v3（CONTRACT §7.3）：出向决策权在插件侧 —— 拆条 / 节奏 / 表情 / 新出口 / 老出口回退
// ---------------------------------------------------------------------------

const stickerDir = mkdtempSync(join(tmpdir(), 'onebot-bridge-stickers-'))
const stickerIndex = join(stickerDir, 'index.json')
writeFileSync(
  stickerIndex,
  JSON.stringify({
    items: [
      { md5: 'aaa', name: '无语', tags: ['无语', '白眼'], uses: 3 },
      { md5: 'bbb', name: '开心', tags: ['哈哈'], uses: 1 },
    ],
  }),
  'utf8',
)

const stickerCfg = (patch = {}) => ({
  sticker: { enabled: true, probability: 0, indexPath: stickerIndex },
  ...patch,
})

test('dryRun + 拆条 + 表情：一条回复拆成多条、还挑中表情，但零 HTTP', async () => {
  const bot = await startFakeOneBot()
  writeConfig(stickerCfg({ enabled: true, service: { httpUrl: bot.httpUrl } })) // dryRun 缺省 = true
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')

    bot.push(frame({ userId: 'wxid_d', nickname: '小丁', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId
    const reply = '第一段，先说这个。\n\n第二段，我真的很无语。\n\n第三段，就这样。'
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: reply }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    // 拆条/表情的计数在出向闸门（session.outbound）上；session.stats 是会话层的调用次数
    await waitFor(() => (plugin.state().session.outbound.dryRun || 0) >= 3)
    assert.equal(bot.requests.length, 0, 'dryRun 下拆成几条都不许发请求')
    const st = plugin.state().session.outbound
    assert.equal(st.sent, 0)
    assert.ok(st.dryRun >= 4, `3 条文本 + 1 张表情都要记 dryRun，实际 ${st.dryRun}`)
    assert.equal(st.pieces, 3)
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('真发 + 拆条 + 表情：3 个 /api/send/text + 1 个 /api/send/sticker，按顺序', async () => {
  const bot = await startFakeOneBot()
  writeConfig(stickerCfg({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl } }))
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')

    bot.push(frame({ userId: 'wxid_e', nickname: '小戊', segments: [{ type: 'text', data: { text: '说点什么' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId
    const reply = '第一段。\n\n第二段，有点无语。\n\n第三段。'
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: reply }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await waitFor(() => bot.requests.length >= 4)
    const urls = bot.requests.map((r) => r.url)
    assert.deepEqual(urls, ['/api/send/text', '/api/send/text', '/api/send/text', '/api/send/sticker'])
    assert.deepEqual(bot.requests.slice(0, 3).map((r) => r.body.text), ['第一段。', '第二段，有点无语。', '第三段。'])
    assert.deepEqual(bot.requests[3].body, { target: 'private:wxid_e', name: '无语' })
    for (const r of bot.requests) assert.equal(r.body.target, 'private:wxid_e')
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('表情接口失败：静默跳过，不影响已经发出的文本', async () => {
  const bot = await startFakeOneBot({
    onSend: (body, req) => (req.url === '/api/send/sticker' ? { status: 200, json: { ok: false, reason: '表情库里没这张' } } : undefined),
  })
  writeConfig(stickerCfg({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl } }))
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')
    bot.push(frame({ userId: 'wxid_f', nickname: '小己', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '好的，真无语。' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await waitFor(() => bot.requests.some((r) => r.url === '/api/send/sticker'))
    await new Promise((r) => setTimeout(r, 50))
    const st = plugin.state().session.outbound
    assert.equal(st.sent, 1, '文本算发出去了')
    assert.equal(st.stickers, 0, '表情没发出去就不计')
    assert.equal(st.failed, 0, '表情失败不算文本失败')
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('合并窗口：同一会话两轮回复在窗口内 → 合成一批（仍然按段落逐条发）', async () => {
  const bot = await startFakeOneBot()
  // 窗口开得很大 + 末尾手动 flush：不靠 sleep 抢时间，测试才稳定
  writeConfig({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl }, outbound: { intervalMs: 0, mergeWindowMs: 60000 } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')
    bot.push(frame({ userId: 'wxid_g', nickname: '小庚', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId

    // 两轮回复先后落到同一个会话（都在同一个合并窗口里）
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '第一句。' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
    await new Promise((r) => setTimeout(r, 20))
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 2, message: { content: [{ type: 'text', text: '第二句。' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 2, reason: { kind: 'completed' } } })
    await new Promise((r) => setTimeout(r, 20))

    const outbound = plugin.modules.service('outbound')
    assert.deepEqual(outbound.pending(), ['private:wxid_g'], '两段应攒在同一批里')
    await outbound.flush('private:wxid_g')

    await waitFor(() => bot.requests.length >= 2)
    assert.deepEqual(bot.requests.map((r) => r.body.text), ['第一句。', '第二句。'])
    assert.equal(outbound.stats().merged, 1, '第二段被合并进同一批')
  } finally {
    dsh.dispose()
    await bot.close()
  }
})

test('老出口回退（deprecated）：outbound.legacyEndpoint=true 时打 /send_private_msg', async () => {
  const bot = await startFakeOneBot()
  writeConfig({ enabled: true, dryRun: false, service: { httpUrl: bot.httpUrl }, outbound: { intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: true } })
  const dsh = stubDsh()
  const { apply } = await import('../src/index.js')
  try {
    const plugin = apply(dsh.ctx)
    await plugin.ready
    await waitFor(() => plugin.state().ws.state === 'connected')
    bot.push(frame({ userId: 'wxid_h', nickname: '小辛', segments: [{ type: 'text', data: { text: '在吗' } }] }))
    await waitFor(() => dsh.calls.followup.length === 1)
    const sessionId = dsh.calls.followup[0].sessionId
    dsh.emit(sessionId, { type: 'assistant/message', data: { turn: 1, message: { content: [{ type: 'text', text: '老路子。' }] } } })
    dsh.emit(sessionId, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

    await waitFor(() => bot.requests.length === 1)
    assert.equal(bot.requests[0].url, '/send_private_msg')
    assert.equal(bot.requests[0].body.user_id, 'wxid_h')
    assert.equal(plugin.modules.service('outbound').endpoint(), 'legacy(/send_*)')
  } finally {
    dsh.dispose()
    await bot.close()
  }
})
