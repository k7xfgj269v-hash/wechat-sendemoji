// 出向决策层：合并窗口、拆条后逐条发、条间隔、dryRun 逐块拦截、表情静默失败、老出口回退。
//
// 这里全部用注入的假传输 + 假 sleep，不碰网络也不真的等待，断言「发了什么、发了几条、间隔多少」。

import test from 'node:test'
import assert from 'node:assert/strict'
import { createOutbound, apiTargetOf, createApiTransport } from '../src/outbound.js'
import { loadConfig } from '../src/config.js'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const silentLog = { info() {}, warn() {}, debug() {}, error() {} }
const warnings = []
const noisyLog = { ...silentLog, warn: (...a) => warnings.push(a.join(' ')) }

const baseConfig = (patch = {}) => {
  const { config } = loadConfig({ file: join(mkdtempSync(join(tmpdir(), 'onebot-bridge-out-')), 'none.json') })
  // 默认：开真发 + 不合并/不间隔（要测合并/间隔的用例自己传）；要测 dryRun 的传 dryRun: true
  const fast = { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false }
  const merged = { ...config, enabled: true, dryRun: false, outbound: fast, ...patch }
  if (patch.outbound) merged.outbound = { ...fast, ...patch.outbound }
  return merged
}

/** 记录调用的假传输层。 */
function fakeTransport({ failSticker = false, failTextAt = -1 } = {}) {
  const calls = []
  return {
    calls,
    async sendText({ target, text }) {
      calls.push({ kind: 'text', target, text })
      const index = calls.filter((c) => c.kind === 'text').length - 1
      if (index === failTextAt) return { ok: false, reason: 'boom' }
      return { ok: true, task_id: `t${calls.length}` }
    },
    async sendSticker({ target, name }) {
      calls.push({ kind: 'sticker', target, name })
      if (failSticker) return { ok: false, reason: 'sticker-not-found' }
      return { ok: true, task_id: `s${calls.length}` }
    },
  }
}

function makeGate({ cfg, transport, pickSticker = null, sleeps = [] }) {
  const sleep = async (ms) => {
    sleeps.push(ms)
  }
  const outbound = createOutbound({
    getConfig: () => cfg,
    transport,
    log: silentLog,
    pickSticker,
    sleep,
  })
  return { outbound, sleeps }
}

test('chatId → target：唯一翻译点，带 kind/id 提示', () => {
  assert.deepEqual(apiTargetOf('private:wxid_a'), { target: 'private:wxid_a', target_kind: 'private', target_id: 'wxid_a' })
  assert.deepEqual(apiTargetOf('group:123@chatroom'), { target: 'group:123@chatroom', target_kind: 'group', target_id: '123@chatroom' })
  assert.equal(apiTargetOf('wxid_a'), null)
})

test('拆条后逐条发：三段文本 → 三次 sendText，内容各自正确', async () => {
  const cfg = baseConfig({ outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound, sleeps } = makeGate({ cfg, transport })

  const r = await outbound.send('private:wxid_a', '第一段。\n\n第二段。\n\n第三段。')
  assert.equal(r.ok, true)
  assert.equal(r.pieces, 3)
  assert.deepEqual(transport.calls.map((c) => c.text), ['第一段。', '第二段。', '第三段。'])
  assert.deepEqual(transport.calls.map((c) => c.target), ['private:wxid_a', 'private:wxid_a', 'private:wxid_a'])
  assert.deepEqual(sleeps, [], 'intervalMs=0 不该 sleep')
  assert.equal(outbound.stats().pieces, 3)
  assert.equal(outbound.stats().sent, 3)
})

test('条间隔：每两条之间 sleep 一次 intervalMs（最后一条之后不等）', async () => {
  const cfg = baseConfig({ outbound: { split: true, maxChars: 300, intervalMs: 600, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound, sleeps } = makeGate({ cfg, transport })
  await outbound.send('private:wxid_a', '一。\n\n二。\n\n三。')
  assert.deepEqual(sleeps, [600, 600])
})

test('split=false：整段一条发出去（不拆）', async () => {
  const cfg = baseConfig({ outbound: { split: false, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({ cfg, transport })
  const r = await outbound.send('private:wxid_a', '一。\n\n二。')
  assert.equal(r.pieces, 1)
  assert.deepEqual(transport.calls.map((c) => c.text), ['一。\n\n二。'])
})

test('合并窗口：窗口内到达的两段回复合成一批，一起拆一起发', async () => {
  const cfg = baseConfig({ outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 5, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({ cfg, transport })

  const p1 = outbound.send('private:wxid_a', '先说一句。')
  const p2 = outbound.send('private:wxid_a', '又想起一句。')
  assert.deepEqual(outbound.pending(), ['private:wxid_a'], '两段都应该在同一个窗口里')
  const [r1, r2] = await Promise.all([p1, p2])

  assert.equal(outbound.stats().merged, 1)
  assert.equal(r1, r2, '两个调用方拿到同一个结果')
  assert.equal(r1.pieces, 2)
  assert.deepEqual(transport.calls.map((c) => c.text), ['先说一句。', '又想起一句。'])
})

test('合并窗口：不同会话互不合并', async () => {
  const cfg = baseConfig({ outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 5, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({ cfg, transport })
  await Promise.all([outbound.send('private:a', '给 a'), outbound.send('private:b', '给 b')])
  assert.equal(transport.calls.length, 2)
  assert.deepEqual(transport.calls.map((c) => c.target).sort(), ['private:a', 'private:b'])
})

test('dryRun：拆成多条也一条都不发（零传输调用，每块都记 dryRun）', async () => {
  const cfg = baseConfig({ dryRun: true, outbound: { split: true, maxChars: 10, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({ cfg, transport })
  const r = await outbound.send('private:wxid_a', '一二三四五六七八九十。\n\n再来一段很长很长的话。')
  assert.equal(r.dryRun, true)
  assert.equal(transport.calls.length, 0, 'dryRun 下一个请求都不许发')
  assert.ok(outbound.stats().dryRun >= 2)
  assert.equal(outbound.stats().sent, 0)
})

test('dryRun：表情也只记日志，不发', async () => {
  const cfg = baseConfig({ dryRun: true, sticker: { enabled: true, probability: 1, indexPath: '' }, outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({ cfg, transport, pickSticker: () => ({ name: '无语' }) })
  const r = await outbound.send('private:wxid_a', '随便说说')
  assert.equal(r.sticker, '无语')
  assert.equal(transport.calls.length, 0)
})

test('总开关关着 / 模块关着：不发，返回 skipped', async () => {
  const transport = fakeTransport()
  const offCfg = baseConfig({ enabled: false })
  const g1 = makeGate({ cfg: offCfg, transport })
  assert.equal((await g1.outbound.send('private:a', 'hi')).skipped, 'disabled')

  const offModule = createOutbound({ getConfig: () => baseConfig(), transport, log: silentLog, isEnabled: () => false })
  assert.equal((await offModule.send('private:a', 'hi')).skipped, 'outbound-module-off')
  assert.equal(transport.calls.length, 0)
})

test('表情：命中就发一张，且排在文本之后', async () => {
  const cfg = baseConfig({ sticker: { enabled: true, probability: 0, indexPath: '' }, outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({ cfg, transport, pickSticker: (text) => (text.includes('无语') ? { name: '无语' } : null) })
  await outbound.send('private:wxid_a', '我真的很无语')
  assert.deepEqual(transport.calls.map((c) => c.kind), ['text', 'sticker'])
  assert.equal(transport.calls[1].name, '无语')
  assert.equal(outbound.stats().stickers, 1)
})

test('表情：发送失败静默跳过，文本仍算成功', async () => {
  const cfg = baseConfig({ sticker: { enabled: true, probability: 1, indexPath: '' }, outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport({ failSticker: true })
  const { outbound } = makeGate({ cfg, transport, pickSticker: () => ({ name: '不存在的表情' }) })
  const r = await outbound.send('private:wxid_a', '一句话')
  assert.equal(r.ok, true, '表情失败不影响文本结果')
  assert.equal(outbound.stats().sent, 1)
  assert.equal(outbound.stats().stickers, 0, '没发出去就不算发出')
})

test('表情决策抛错：静默跳过，不影响文本', async () => {
  const cfg = baseConfig({ sticker: { enabled: true, probability: 1, indexPath: '' }, outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport()
  const { outbound } = makeGate({
    cfg,
    transport,
    pickSticker: () => {
      throw new Error('索引炸了')
    },
  })
  const r = await outbound.send('private:wxid_a', '一句话')
  assert.equal(r.ok, true)
  assert.equal(r.sticker, '')
  assert.deepEqual(transport.calls.map((c) => c.kind), ['text'])
})

test('表情关掉 / 概率 0：不挑也不发', async () => {
  const transport = fakeTransport()
  let asked = 0
  const pick = () => {
    asked++
    return null
  }
  const cfgOff = baseConfig({ sticker: { enabled: false, probability: 1, indexPath: '' }, outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const { outbound } = makeGate({ cfg: cfgOff, transport, pickSticker: pick })
  await outbound.send('private:a', 'hi')
  assert.equal(asked, 0, 'sticker.enabled=false 时连挑都不挑')
  assert.equal(transport.calls.length, 1)
})

test('文本某一条失败：记为 failed，其余照发，整体 ok=false', async () => {
  const cfg = baseConfig({ outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const transport = fakeTransport({ failTextAt: 1 })
  const { outbound } = makeGate({ cfg, transport })
  const r = await outbound.send('private:a', '一。\n\n二。\n\n三。')
  assert.equal(r.ok, false)
  assert.equal(r.failed, 1)
  assert.equal(outbound.stats().sent, 2)
  assert.equal(outbound.stats().failed, 1)
})

test('传输层不可用（模块装错/被替换）：显式失败，不抛异常', async () => {
  const cfg = baseConfig({ outbound: { split: true, maxChars: 300, intervalMs: 0, mergeWindowMs: 0, legacyEndpoint: false } })
  const outbound = createOutbound({ getConfig: () => cfg, transport: () => null, log: noisyLog })
  const r = await outbound.send('private:a', 'hi')
  assert.equal(r.ok, false)
  assert.equal(r.error, 'no-transport')
})

test('新传输层：/api/send/text 的 body 形状 + ok:false 当失败', async () => {
  const seen = []
  const transport = createApiTransport({
    getHttpUrl: () => 'http://127.0.0.1:1',
    getToken: () => '',
  })
  // 直接替换 fetch，断言请求形状（不真的起服务）
  const orig = globalThis.fetch
  globalThis.fetch = async (url, init) => {
    seen.push({ url, body: JSON.parse(init.body), headers: init.headers })
    const json = url.endsWith('/api/send/text') ? { ok: true, task_id: 'task-1', queued_at: 123 } : { ok: false, reason: 'nope' }
    return { ok: true, status: 200, text: async () => JSON.stringify(json) }
  }
  try {
    const ok = await transport.sendText({ target: 'private:wxid_a', text: ' 你好 ' })
    assert.equal(ok.ok, true)
    assert.equal(ok.task_id, 'task-1')
    assert.equal(seen[0].url, 'http://127.0.0.1:1/api/send/text')
    assert.deepEqual(seen[0].body, { target: 'private:wxid_a', text: '你好' })

    const bad = await transport.sendSticker({ target: 'private:wxid_a', name: '无语' })
    assert.equal(bad.ok, false)
    assert.equal(bad.reason, 'nope')
    assert.deepEqual(seen[1].body, { target: 'private:wxid_a', name: '无语' })
  } finally {
    globalThis.fetch = orig
  }
})
