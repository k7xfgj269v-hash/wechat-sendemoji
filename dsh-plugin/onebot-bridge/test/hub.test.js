// hub 的测试：用一套「形状与 DSH 一致」的桩来跑会话投递/回收路径。
//
// 说明：这里验证的是【本插件发出的调用形状】（create/resolveAgent/followup/session/event/
// tools.register/systemPrompt.context），不是 DSH 本身的行为。真宿主上的联调见 README「待验证」。

import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createHub } from '../src/hub.js'
import { createReplyTool } from '../src/tools.js'
import { createOutbound } from '../src/outbound.js'
import { loadConfig } from '../src/config.js'

process.env.ONEBOT_BRIDGE_DATA_DIR = mkdtempSync(join(tmpdir(), 'onebot-bridge-hub-'))

const silentLog = { info() {}, warn() {}, error() {}, debug() {} }

/** 一套最小 DSH 桩：sessionController + agents + 一个 agent（scoped tools / systemPrompt）。 */
function fakeDsh() {
  const agents = new Map()
  const calls = {
    create: [],
    rename: [],
    resolve: [],
    followup: [],
    registeredTools: [],
    contexts: [],
    archiving: [],
  }
  const sc = {
    async create(req) {
      calls.create.push(req)
      const sessionId = `session-${calls.create.length}`
      agents.set(sessionId, makeAgent(sessionId))
      return { sessionId }
    },
    async rename(req) {
      calls.rename.push(req)
    },
    async resolveAgent(sessionId) {
      calls.resolve.push(sessionId)
      const agent = agents.get(sessionId)
      if (!agent) return { error: { code: 'SESSION_NOT_FOUND', message: 'missing' } }
      return { agent }
    },
  }
  function makeAgent(sessionId) {
    const agent = {
      session: { id: sessionId },
      ctx: {
        tools: {
          register(tool) {
            calls.registeredTools.push({ sessionId, name: tool.name })
            return () => {}
          },
        },
        systemPrompt: {
          context(section) {
            calls.contexts.push({ sessionId, name: section.name, order: section.order, text: section.text })
            return () => {}
          },
        },
      },
      followup(message) {
        calls.followup.push({ sessionId, message })
      },
    }
    return agent
  }
  const ctx = {
    agents: { get: (id) => agents.get(id) },
    sessionController: sc,
    on() {
      return () => {}
    },
    effect(fn) {
      return fn()
    },
    get(name) {
      if (name === 'credentials') return { resolve: async () => undefined }
      return undefined
    },
  }
  return { ctx, calls, agents }
}

const config = (patch = {}) => {
  const { config: base } = loadConfig({ file: join(mkdtempSync(join(tmpdir(), 'onebot-bridge-c-')), 'none.json') })
  // 测试里不合并、不间隔（合并/间隔在 pacing.test.js 里单独测），避免每个用例都等 2s
  const outbound = { ...base.outbound, split: true, intervalMs: 0, mergeWindowMs: 0 }
  const merged = { ...base, outbound, ...patch }
  if (patch.outbound) merged.outbound = { ...outbound, ...patch.outbound }
  return merged
}

const privateMsg = { chatId: 'private:wxid_a', chatKind: 'private', sender: 'wxid_a', senderName: '张三', text: '在吗', msgType: 'text' }

function makeHub({ dsh, cfg, sent = [] }) {
  // 真的出向闸门 + 真的拆条/节奏（dryRun 判定也在里面），只把 HTTP 那一层换成记录器
  const transport = {
    async sendText({ target, text }) {
      sent.push({ chatId: target, text })
      return { ok: true }
    },
    async sendSticker({ target, name }) {
      sent.push({ chatId: target, sticker: name })
      return { ok: true }
    },
  }
  const outbound = createOutbound({ getConfig: () => cfg, transport, log: silentLog })
  const hub = createHub({
    ctx: dsh.ctx,
    getConfig: () => cfg,
    log: silentLog,
    outbound,
    buildReplyTool: (chat) => createReplyTool({ chat, hub }),
  })
  return { hub, sent, outbound }
}

test('第一条消息：建会话 → 挂 wechat_reply + 系统提示词 → followup 一条 user turn', async () => {
  const dsh = fakeDsh()
  const cfg = config()
  const { hub } = makeHub({ dsh, cfg })

  await hub.onMessage(privateMsg)

  assert.equal(dsh.calls.create.length, 1)
  assert.match(dsh.calls.create[0].cwd, /onebot-bridge|chats/)
  assert.equal(dsh.calls.rename.length, 1)
  assert.match(dsh.calls.rename[0].title, /微信/)
  assert.deepEqual(dsh.calls.registeredTools.map((t) => t.name), ['wechat_reply'])
  assert.equal(dsh.calls.contexts.length, 1)
  assert.equal(dsh.calls.contexts[0].name, 'onebot-bridge-chat')

  assert.equal(dsh.calls.followup.length, 1)
  const msg = dsh.calls.followup[0].message
  assert.equal(msg.role, 'user')
  assert.equal(msg.content[0].type, 'text')
  assert.match(msg.content[0].text, /【微信私聊】张三\(wxid_a\)：在吗/)
  assert.equal(msg.source.kind, 'onebot-bridge')
  assert.ok(msg.id)
})

test('同一个 chat 的第二条消息复用会话，不重复建会话/工具', async () => {
  const dsh = fakeDsh()
  const cfg = config()
  const { hub } = makeHub({ dsh, cfg })

  await hub.onMessage(privateMsg)
  // 第一轮还没结束：再来的消息会先排队
  await hub.onMessage({ ...privateMsg, text: '还在吗' })
  assert.equal(dsh.calls.followup.length, 1)

  // 第一轮结束 → 排队的第二条被投进去，用的还是同一个会话
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  assert.equal(dsh.calls.followup.length, 2)
  assert.equal(dsh.calls.followup[1].sessionId, 'session-1')
  assert.equal(dsh.calls.create.length, 1)
  assert.equal(dsh.calls.registeredTools.length, 1)
})

test('恢复不了旧会话：换一个新会话，不让消息卡死', async () => {
  const dsh = fakeDsh()
  const cfg = config()
  const { hub } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)

  // 模拟「DSH 重启后旧会话怎么都恢复不了」
  dsh.agents.clear()
  dsh.ctx.sessionController.resolveAgent = async () => ({ error: { code: 'CORRUPT', message: '坏了' } })
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  await hub.onMessage({ ...privateMsg, text: '新的一条' })
  assert.equal(dsh.calls.create.length, 2)
})

test('一轮正常结束 → 正文发回微信（dryRun 关、总开关开）', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true, dryRun: false })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)

  await hub.onSessionEvent({ id: 'session-1' }, {
    type: 'assistant/message',
    data: { turn: 1, message: { content: [{ type: 'text', text: '在的，怎么了？' }] } },
  })
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

  assert.deepEqual(sent, [{ chatId: 'private:wxid_a', text: '在的，怎么了？' }])
  assert.equal(hub.snapshot().stats.delivered, 1)
})

test('dryRun（默认值）→ 一个字都不发，只记账', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true }) // dryRun 缺省 = true
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  await hub.onSessionEvent({ id: 'session-1' }, {
    type: 'assistant/message',
    data: { turn: 1, message: { content: [{ type: 'text', text: '你好' }] } },
  })
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })

  assert.deepEqual(sent, [])
  assert.equal(hub.snapshot().stats.dryRun, 1)
})

test('总开关关着 → 不发（哪怕 dryRun 关掉了）', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: false, dryRun: false })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  const r = await hub.sendText('private:wxid_a', 'x')
  assert.equal(r.skipped, 'disabled')
  assert.deepEqual(sent, [])
})

test('这一轮没正常结束（aborted/error）→ 不自动发', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true, dryRun: false })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  await hub.onSessionEvent({ id: 'session-1' }, {
    type: 'assistant/message',
    data: { turn: 1, message: { content: [{ type: 'text', text: '半句话' }] } },
  })
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'aborted', reason: { kind: 'user' } } } })
  assert.deepEqual(sent, [])
})

test('中断的 assistant/message（interrupted）不进最终正文', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true, dryRun: false })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  await hub.onSessionEvent({ id: 'session-1' }, {
    type: 'assistant/message',
    data: { turn: 1, interrupted: true, message: { content: [{ type: 'text', text: '被打断的半句' }] } },
  })
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  assert.deepEqual(sent, [])
})

test('wechat_reply 发过就不再重发最终正文', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true, dryRun: false })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)

  const tool = createReplyTool({ chat: { chatId: 'private:wxid_a', chatKind: 'private' }, hub })
  const r = await tool.execute({ text: '我先回一句' })
  assert.equal(r.ok, true)
  assert.equal(tool.name, 'wechat_reply')
  assert.deepEqual(sent, [{ chatId: 'private:wxid_a', text: '我先回一句' }])

  await hub.onSessionEvent({ id: 'session-1' }, {
    type: 'assistant/message',
    data: { turn: 1, message: { content: [{ type: 'text', text: '我先回一句' }] } },
  })
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  assert.equal(sent.length, 1, '工具发过之后不该再自动重发')
})

test('wechat_reply 在 dryRun 下返回试运行回执，且不发', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  const tool = createReplyTool({ chat: { chatId: 'private:wxid_a', chatKind: 'private' }, hub })
  const r = await tool.execute({ text: 'hi' })
  assert.equal(r.ok, true)
  assert.equal(r.dryRun, true)
  assert.deepEqual(sent, [])
  assert.deepEqual(await tool.execute({ text: '   ' }), { ok: false, message: '不发空消息' })
})

test('宿主没有 sessionController：入向消息不炸宿主，只记错', async () => {
  const dsh = fakeDsh()
  delete dsh.ctx.sessionController
  const cfg = config({ enabled: true, dryRun: false })
  const { hub, sent } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  assert.equal(dsh.calls.followup.length, 0)
  assert.deepEqual(sent, [])
  assert.equal(hub.snapshot().stats.received, 1)
})

test('session.enabled=false：只归一化记日志，不碰 DSH', async () => {
  const dsh = fakeDsh()
  const cfg = config({ enabled: true, session: { ...config().session, enabled: false } })
  const { hub } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  assert.equal(dsh.calls.create.length, 0)
  assert.equal(dsh.calls.followup.length, 0)
})

test('连发多条：合成一段交给她（不是一人一轮）', async () => {
  const dsh = fakeDsh()
  const cfg = config()
  const { hub } = makeHub({ dsh, cfg })
  // 先占住这一轮
  const p = hub.onMessage(privateMsg)
  await hub.onMessage({ ...privateMsg, text: '第二句' })
  await hub.onMessage({ ...privateMsg, text: '第三句' })
  await p
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  const text = dsh.calls.followup[1].message.content[0].text
  assert.match(text, /第二句/)
  assert.match(text, /第三句/)
})

// ---------------------------------------------------------------------------
// 与 au_rabot 对齐的部分：preset 选择（主人 / 社交）+ 每轮变化的运行时上下文
// ---------------------------------------------------------------------------

const ownerCfg = (patch = {}) => config({ enabled: true, dryRun: false, owner: { wxids: ['wxid_a'], groups: [] }, ...patch })

test('preset 选择：主人私聊用 onebot-owner，陌生人私聊和群聊用 onebot-social', async () => {
  const dsh = fakeDsh()
  const cfg = ownerCfg()
  const { hub } = makeHub({ dsh, cfg })

  await hub.onMessage(privateMsg) // wxid_a 在主人名单里
  assert.equal(dsh.calls.create[0].agentPreset, 'onebot-owner')

  // 陌生人私聊
  await hub.onMessage({ ...privateMsg, chatId: 'private:wxid_stranger', sender: 'wxid_stranger', senderName: '路人' })
  assert.equal(dsh.calls.create[1].agentPreset, 'onebot-social')

  // 群聊（不在 owner.groups 里）
  await hub.onMessage({ ...privateMsg, chatId: 'group:g1', chatKind: 'group', sender: 'wxid_a' })
  assert.equal(dsh.calls.create[2].agentPreset, 'onebot-social')
})

test('主人名单里的群：群里也用 onebot-owner', async () => {
  const dsh = fakeDsh()
  const cfg = ownerCfg({ owner: { wxids: [], groups: ['g_owner'] } })
  const { hub } = makeHub({ dsh, cfg })
  await hub.onMessage({ ...privateMsg, chatId: 'group:g_owner', chatKind: 'group', sender: 'wxid_x' })
  assert.equal(dsh.calls.create[0].agentPreset, 'onebot-owner')
})

test('presets.enabled=false → create 不带 agentPreset（用 profile 默认 agent）', async () => {
  const dsh = fakeDsh()
  const cfg = ownerCfg({ presets: { enabled: false, owner: 'onebot-owner', social: 'onebot-social' } })
  const { hub } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  assert.equal(dsh.calls.create[0].agentPreset, undefined)
})

test('主人身份变了（加进 owner.wxids）→ 换一个新会话，旧映射被新 preset 覆盖', async () => {
  const dsh = fakeDsh()
  let cfg = config({ enabled: true, dryRun: false, owner: { wxids: [], groups: [] } })
  const sent = []
  const transport = {
    async sendText({ target, text }) { sent.push({ chatId: target, text }); return { ok: true } },
    async sendSticker({ target, name }) { sent.push({ chatId: target, sticker: name }); return { ok: true } },
  }
  const outbound = createOutbound({ getConfig: () => cfg, transport, log: silentLog })
  const hub = createHub({ ctx: dsh.ctx, getConfig: () => cfg, log: silentLog, outbound, buildReplyTool: (chat) => createReplyTool({ chat, hub }) })

  await hub.onMessage(privateMsg)
  assert.equal(dsh.calls.create[0].agentPreset, 'onebot-social')

  // 第一轮结束，把 wxid_a 加进主人名单
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  cfg = { ...cfg, owner: { wxids: ['wxid_a'], groups: [] } }

  await hub.onMessage({ ...privateMsg, text: '再来一句' })
  assert.equal(dsh.calls.create.length, 2, 'preset 变了要建新会话')
  assert.equal(dsh.calls.create[1].agentPreset, 'onebot-owner')
})

test('preset 建不起来（profile 没挂那层 patch）→ 退回默认 agent，且只告警一次', async () => {
  const dsh = fakeDsh()
  // 两个 wxid 都是主人：第二个 chat 也会想要 onebot-owner，此时应该「记过账了，不再试」
  const cfg = ownerCfg({ owner: { wxids: ['wxid_a', 'wxid_b'], groups: [] } })
  const seen = []
  const origCreate = dsh.ctx.sessionController.create
  dsh.ctx.sessionController.create = async (req) => {
    if (req.agentPreset) {
      seen.push(req.agentPreset)
      throw new Error(`unknown agent preset: ${req.agentPreset}`)
    }
    return origCreate(req)
  }
  const { hub } = makeHub({ dsh, cfg })

  await hub.onMessage(privateMsg)
  assert.deepEqual(seen, ['onebot-owner'], '第一次会试一下 preset')
  assert.equal(dsh.calls.create.length, 1)
  assert.equal(dsh.calls.create[0].agentPreset, undefined, '失败后退回不带 preset')
  assert.deepEqual(hub.snapshot().presetsBroken, ['onebot-owner'])

  // 第一轮结束、下一条消息：不该再试那个 preset
  await hub.onSessionEvent({ id: 'session-1' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  await hub.onMessage({ ...privateMsg, chatId: 'private:wxid_b', sender: 'wxid_b', senderName: '另一个主人' })
  assert.deepEqual(seen, ['onebot-owner'], '记过账就不再试')
  assert.equal(dsh.calls.create.length, 2)

  // 同一个 chat 再来消息：必须复用那个「没有 preset 的」会话，不能每条都新建
  await hub.onSessionEvent({ id: 'session-2' }, { type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  await hub.onMessage({ ...privateMsg, chatId: 'private:wxid_b', sender: 'wxid_b', senderName: '另一个主人', text: '还活着吗' })
  assert.equal(dsh.calls.create.length, 2, 'preset 建不起来之后不能每条消息都新建会话')
})

test('运行时上下文：当前对象 + 今天聊过谁（走 systemPrompt.context，不拼 system 开头）', async () => {
  const dsh = fakeDsh()
  const cfg = ownerCfg()
  const { hub } = makeHub({ dsh, cfg })

  await hub.onMessage(privateMsg)
  await hub.onMessage({ ...privateMsg, chatId: 'group:g1', chatKind: 'group', sender: 'wxid_b', senderName: '李四' })
  await hub.onMessage({ ...privateMsg, text: '再说一句' })

  const section = dsh.calls.contexts[0]
  assert.equal(section.name, 'onebot-bridge-chat')
  assert.equal(section.order, 900)
  const text = section.text()
  assert.match(text, /当前对象：微信私聊 · 张三\(wxid_a\)/)
  assert.match(text, /今天聊过：/)
  assert.match(text, /微信群 g1/)
  assert.match(text, /不会自动重发|发回微信/)
  // 人格不在这里（人格走 {{onebot_persona}} 提示词变量）
  assert.equal(text.includes('{{onebot_persona}}'), false)
})

test('session.recentChats=0 → 上下文里不出现「今天聊过」', async () => {
  const dsh = fakeDsh()
  const cfg = ownerCfg({ session: { ...config().session, recentChats: 0 } })
  const { hub } = makeHub({ dsh, cfg })
  await hub.onMessage(privateMsg)
  const text = dsh.calls.contexts[0].text()
  assert.equal(text.includes('今天聊过'), false)
  assert.match(text, /当前对象：/)
})
