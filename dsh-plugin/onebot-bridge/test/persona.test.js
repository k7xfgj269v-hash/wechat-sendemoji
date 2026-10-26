// 人格：persona.file > persona.text > 默认；坏文件/空文件退回而不是让整轮报错。
// 人格走提示词变量 {{onebot_persona}}（preset 的 persona 行渲染），所以这里只测「取文本」这一段。

import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, writeFileSync, mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { loadConfig, DEFAULTS } from '../src/config.js'
import { resolvePersona, personaFilePath, VARIABLE } from '../src/persona.js'

const dataDir = mkdtempSync(join(tmpdir(), 'onebot-bridge-persona-'))
process.env.ONEBOT_BRIDGE_DATA_DIR = dataDir

const warnings = []
const log = { info() {}, debug() {}, error() {}, warn: (...a) => warnings.push(a.join(' ')) }

const cfgWith = (persona) => ({ ...loadConfig({ file: join(dataDir, 'none.json') }).config, persona })

test('变量名固定是 onebot_persona（preset 里写的就是它）', () => {
  assert.equal(VARIABLE, 'onebot_persona')
})

test('没配 persona → 内置默认文本', () => {
  const r = resolvePersona(cfgWith({ text: '', file: '', maxChars: 20000 }), { log })
  assert.equal(r.text, DEFAULTS.persona.text)
  assert.equal(r.source, 'default')
})

test('persona.text 优先于默认', () => {
  const r = resolvePersona(cfgWith({ text: '你是猫娘。', file: '', maxChars: 20000 }), { log })
  assert.equal(r.text, '你是猫娘。')
  assert.equal(r.source, 'config:persona.text')
})

test('persona.file（相对路径按数据目录解析）优先于 text', () => {
  writeFileSync(join(dataDir, 'persona.md'), '\ufeff  文件里的人格。\n', 'utf8')
  const cfg = cfgWith({ text: '这段不该被用到', file: 'persona.md', maxChars: 20000 })
  assert.equal(personaFilePath(cfg), join(dataDir, 'persona.md'))
  const r = resolvePersona(cfg, { log })
  assert.equal(r.text, '文件里的人格。') // BOM 和首尾空白都去掉
  assert.match(r.source, /^file:/)
})

test('绝对路径原样用', () => {
  const abs = join(dataDir, 'persona.md')
  assert.equal(personaFilePath(cfgWith({ text: '', file: abs, maxChars: 20000 })), abs)
})

test('文件不存在 / 是空的 → 退回 persona.text 并告警，不抛异常', () => {
  warnings.length = 0
  const missing = resolvePersona(cfgWith({ text: '兜底人格', file: 'nope.md', maxChars: 20000 }), { log })
  assert.equal(missing.text, '兜底人格')
  assert.equal(missing.source, 'config:persona.text')
  assert.ok(warnings.some((w) => w.includes('不存在')))

  warnings.length = 0
  writeFileSync(join(dataDir, 'empty.md'), '   \n', 'utf8')
  const empty = resolvePersona(cfgWith({ text: '兜底人格', file: 'empty.md', maxChars: 20000 }), { log })
  assert.equal(empty.text, '兜底人格')
  assert.ok(warnings.some((w) => w.includes('空的')))
})

test('maxChars 截断（一份巨长的文件不能把提示词撑爆）', () => {
  const cfg = cfgWith({ text: 'x'.repeat(5000), file: '', maxChars: 1200 })
  const r = resolvePersona(cfg, { log })
  assert.equal(r.text.length, 1200)
})

test('配置归一化：persona/owner/presets/modules 的默认值与容错', () => {
  const { config } = loadConfig({ file: join(dataDir, 'none.json') })
  assert.equal(config.persona.file, '')
  assert.ok(config.persona.text.length > 0)
  assert.deepEqual(config.owner.wxids, [])
  assert.deepEqual(config.owner.groups, [])
  assert.equal(config.presets.enabled, true)
  assert.equal(config.presets.owner, 'onebot-owner')
  assert.equal(config.presets.social, 'onebot-social')
  assert.deepEqual(config.modules, { connection: true, inbound: true, sessions: true, outbound: true })
})

test('配置容错：owner 名单能写字符串，preset 空串退回默认，模块只认显式 false', () => {
  const file = join(dataDir, 'cfg.json')
  writeFileSync(
    file,
    JSON.stringify({
      owner: { wxids: 'wxid_a， wxid_b  wxid_a', groups: '' },
      presets: { owner: '  ', social: 'onebot-social' },
      modules: { connection: false, inbound: 'no', sessions: 1, outbound: true },
    }),
    'utf8',
  )
  const { config } = loadConfig({ file })
  assert.deepEqual(config.owner.wxids, ['wxid_a', 'wxid_b'])
  assert.deepEqual(config.owner.groups, [])
  assert.equal(config.presets.owner, 'onebot-owner')
  assert.equal(config.modules.connection, false)
  assert.equal(config.modules.inbound, true, '非 false 一律算开')
  assert.equal(config.modules.sessions, true)
})

test('isOwner / isOwnerGroup：名单空 = 没有主人', async () => {
  const { isOwner, isOwnerGroup } = await import('../src/config.js')
  const { config } = loadConfig({ file: join(dataDir, 'none.json') })
  assert.equal(isOwner(config, 'wxid_a'), false)
  assert.equal(isOwner(config, ''), false)
  assert.equal(isOwnerGroup(config, 'g1'), false)
  const cfg2 = { ...config, owner: { wxids: ['wxid_a'], groups: ['g1'] } }
  assert.equal(isOwner(cfg2, 'wxid_a'), true)
  assert.equal(isOwner(cfg2, 'wxid_b'), false)
  assert.equal(isOwnerGroup(cfg2, 'g1'), true)
})
