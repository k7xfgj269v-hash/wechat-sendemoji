import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, writeFileSync, readFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  DEFAULTS,
  loadConfig,
  saveConfig,
  effectiveDryRun,
  effectiveEnabled,
  normalizeHttpUrl,
  wsUrlOf,
  resolveOperatorToken,
} from '../src/config.js'

const tmp = () => mkdtempSync(join(tmpdir(), 'onebot-bridge-cfg-'))

test('默认值：enabled=false、dryRun=true、服务地址默认本机 58080', () => {
  const file = join(tmp(), 'config.json')
  const { config } = loadConfig({ file })
  assert.equal(config.enabled, false)
  assert.equal(config.dryRun, true)
  assert.equal(config.service.httpUrl, 'http://127.0.0.1:58080')
  assert.equal(config.service.tokenEnv, 'ONEBOT_OPERATOR_TOKEN')
})

test('dryRun 红线：只有显式 false 才真发', () => {
  assert.equal(effectiveDryRun({ dryRun: true }), true)
  assert.equal(effectiveDryRun({ dryRun: false }), false)
  // 这些都不许把 dryRun 打开
  for (const v of [undefined, null, 0, 1, '', 'false', '0', 'no']) assert.equal(effectiveDryRun({ dryRun: v }), true, `dryRun=${String(v)}`)
  assert.equal(effectiveDryRun({}), true)
  assert.equal(effectiveDryRun(null), true)
})

test('enabled 同样只认显式 true', () => {
  assert.equal(effectiveEnabled({ enabled: true }), true)
  for (const v of [undefined, null, 'true', 1, 'yes']) assert.equal(effectiveEnabled({ enabled: v }), false)
})

test('配置文件里写 operatorToken 会被忽略并告警（密钥不进配置）', () => {
  const dir = tmp()
  const file = join(dir, 'config.json')
  writeFileSync(file, JSON.stringify({ enabled: true, operatorToken: 'sekret-value-123456', service: { token: 'another-secret-123456' } }))
  const { config, warnings } = loadConfig({ file })
  assert.equal(config.operatorToken, undefined)
  assert.equal(config.service.token, undefined)
  assert.ok(warnings.some((w) => w.includes('operatorToken')))
  assert.ok(warnings.some((w) => w.includes('service.token')))
  // 保存也不会把密钥写回去
  saveConfig(config, { file })
  const text = readFileSync(file, 'utf8')
  assert.equal(text.includes('sekret-value-123456'), false)
  assert.equal(text.includes('another-secret-123456'), false)
})

test('坏 JSON：按默认值跑，不炸', () => {
  const file = join(tmp(), 'config.json')
  writeFileSync(file, '{ this is not json')
  const { config, warnings } = loadConfig({ file })
  assert.equal(config.dryRun, true)
  assert.ok(warnings.length >= 1)
})

test('httpUrl 归一化：去尾斜杠、误贴的 /ws 去掉；ws 地址跟着走', () => {
  assert.equal(normalizeHttpUrl('http://127.0.0.1:58080/'), 'http://127.0.0.1:58080')
  assert.equal(normalizeHttpUrl('http://127.0.0.1:58080/ws'), 'http://127.0.0.1:58080')
  assert.equal(normalizeHttpUrl(''), DEFAULTS.service.httpUrl)

  const { config } = loadConfig({ file: join(tmp(), 'none.json') })
  assert.equal(wsUrlOf(config), 'ws://127.0.0.1:58080/ws')
  assert.equal(wsUrlOf({ service: { httpUrl: 'https://h:1', wsPath: 'ws' } }), 'wss://h:1/ws')
})

test('session.cwd 默认落在插件数据目录，session.maxChars 夹在合理区间', () => {
  const { config } = loadConfig({ file: join(tmp(), 'none.json') })
  assert.match(config.session.cwd, /onebot-bridge.*chats|chats$/)
  const { config: c2 } = loadConfig({ file: join(tmp(), 'none.json') })
  assert.equal(c2.session.maxChars, DEFAULTS.session.maxChars)
})

test('token：先环境变量，再 DSH 凭据，最后空', async () => {
  const saved = process.env.ONEBOT_OPERATOR_TOKEN
  try {
    process.env.ONEBOT_OPERATOR_TOKEN = 'from-env-987654'
    const cfg = { service: { tokenEnv: 'ONEBOT_OPERATOR_TOKEN', credentialRef: '' } }
    assert.deepEqual(await resolveOperatorToken(cfg), { token: 'from-env-987654', source: 'env:ONEBOT_OPERATOR_TOKEN' })

    delete process.env.ONEBOT_OPERATOR_TOKEN
    const credentials = { resolve: async (ref) => (ref === 'ONEBOT_OPERATOR_TOKEN' ? { value: 'from-cred-123456', source: 'file' } : undefined) }
    assert.deepEqual(await resolveOperatorToken(cfg, { credentials }), { token: 'from-cred-123456', source: 'credentials:ONEBOT_OPERATOR_TOKEN' })

    const throwing = { resolve: async () => { throw new Error('nope') } }
    assert.deepEqual(await resolveOperatorToken(cfg, { credentials: throwing }), { token: '', source: '' })
    assert.deepEqual(await resolveOperatorToken(cfg), { token: '', source: '' })
  } finally {
    if (saved === undefined) delete process.env.ONEBOT_OPERATOR_TOKEN
    else process.env.ONEBOT_OPERATOR_TOKEN = saved
  }
})

// ---- v3 出向决策（CONTRACT §7.3）的默认值与夹取 ----

test('出向默认值：拆条开、单条 300 字、条间隔 600ms、合并窗口 2s、不用老出口', () => {
  const { config } = loadConfig({ file: join(tmp(), 'none.json') })
  assert.deepEqual(config.outbound, {
    split: true,
    maxChars: 300,
    intervalMs: 600,
    mergeWindowMs: 2000,
    legacyEndpoint: false,
  })
  assert.deepEqual(config.sticker, { enabled: true, probability: 0.25, indexPath: '' })
})

test('出向开关只认显式值：split 只有 false 才关，legacyEndpoint 只有 true 才开', () => {
  const dir = tmp()
  const file = join(dir, 'c.json')
  writeFileSync(file, JSON.stringify({ outbound: { split: 'false', legacyEndpoint: 'true' } }))
  const { config } = loadConfig({ file })
  assert.equal(config.outbound.split, true, '字符串 "false" 不算关')
  assert.equal(config.outbound.legacyEndpoint, false, '字符串 "true" 不算开')

  writeFileSync(file, JSON.stringify({ outbound: { split: false, legacyEndpoint: true }, sticker: { enabled: false } }))
  const { config: c2 } = loadConfig({ file })
  assert.equal(c2.outbound.split, false)
  assert.equal(c2.outbound.legacyEndpoint, true)
  assert.equal(c2.sticker.enabled, false)
})

test('出向数字字段夹在合理区间（写 0 / 负 / 超大都不出事）', () => {
  const dir = tmp()
  const file = join(dir, 'c.json')
  writeFileSync(
    file,
    JSON.stringify({
      outbound: { maxChars: 999999, intervalMs: -5, mergeWindowMs: 10 ** 9 },
      sticker: { probability: 5 },
    }),
  )
  const { config } = loadConfig({ file })
  assert.equal(config.outbound.maxChars, 5000)
  assert.equal(config.outbound.intervalMs, 0)
  assert.equal(config.outbound.mergeWindowMs, 60000)
  assert.equal(config.sticker.probability, 1)

  writeFileSync(file, JSON.stringify({ outbound: { maxChars: 1, mergeWindowMs: 'abc' }, sticker: { probability: -1 } }))
  const { config: c2 } = loadConfig({ file })
  assert.equal(c2.outbound.maxChars, 20)
  assert.equal(c2.outbound.mergeWindowMs, 2000, '非数字回到默认')
  assert.equal(c2.sticker.probability, 0)
})

test('mergeWindowMs=0 是合法配置（不合并，立刻发）', () => {
  const dir = tmp()
  const file = join(dir, 'c.json')
  writeFileSync(file, JSON.stringify({ outbound: { mergeWindowMs: 0 } }))
  const { config } = loadConfig({ file })
  assert.equal(config.outbound.mergeWindowMs, 0)
})
