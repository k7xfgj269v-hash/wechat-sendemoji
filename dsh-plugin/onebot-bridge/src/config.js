// 配置：默认值 + 读配置文件 + 归一化。
//
// 配置文件：$DSH_HOME/plugins-data/onebot-bridge/config.json（默认 ~/.dsh/plugins-data/onebot-bridge/config.json）
//
// ⚠ 这个文件里【没有】operatorToken 字段，也永远不会写进去：token 只从
//   process.env[service.tokenEnv] 或 DSH 凭据服务（ctx.credentials.resolve）读。
//   配置里出现 operatorToken/token 之类的键会被忽略并告警（防止有人把密钥贴进配置）。
//
// ⚠ dryRun 红线：默认 true。只有配置里【显式】写 `"dryRun": false` 才会真发；
//   缺失、null、字符串 "false"、"0"、0 等一律按 true 处理（见 effectiveDryRun）。

import { readFileSync, writeFileSync, mkdirSync, renameSync, existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { dirname, join } from 'node:path'

export const NAME = 'onebot-bridge'

export function dshHome() {
  return process.env.DSH_HOME || join(homedir(), '.dsh')
}

export function dataDir(...parts) {
  const base = process.env.ONEBOT_BRIDGE_DATA_DIR || join(dshHome(), 'plugins-data', NAME)
  return join(base, ...parts)
}

export const CONFIG_PATH = () => dataDir('config.json')

export const DEFAULTS = Object.freeze({
  // 总开关：关着就不连 onebot、不收不发。默认关。
  enabled: false,
  // 试运行：只记日志，不往微信发任何一个字。默认开，红线。
  dryRun: true,
  debug: false,
  service: {
    httpUrl: 'http://127.0.0.1:58080',
    wsPath: '/ws',
    // token 从哪个环境变量读（只放变量名，不放值）
    tokenEnv: 'ONEBOT_OPERATOR_TOKEN',
    // DSH 凭据服务里的引用名；留空则与 tokenEnv 相同
    credentialRef: '',
    sendTimeoutMs: 15000,
  },
  // 人格：注入到提示词变量 {{onebot_persona}}（preset 的 persona 行渲染它）。
  // 走「变量」而不是拼进每条消息，所以改了配置下一轮就生效，也不污染会话历史。
  persona: {
    // 直接用这段文本
    text: '你在微信上和一个人聊天。像本人一样说话：短句、口语、别用列表和小标题，别自报模型身份。',
    // 非空则从文件读（相对路径相对数据目录），优先于 text
    file: '',
    // 兜底长度上限，防止一份巨长的文件把提示词撑爆
    maxChars: 20000,
  },
  // 谁是「主人」：这些 wxid / 群用 onebot-owner preset（工具多），其余用 onebot-social（工具收敛）。
  // 留空 = 所有人都是 social（安全默认）。
  owner: {
    wxids: [],
    groups: [],
  },
  // agent preset（本插件自带的 cordis.patch.yml 里定义了这两个 id）。
  presets: {
    enabled: true,
    owner: 'onebot-owner',
    social: 'onebot-social',
  },
  session: {
    // 收到消息后是否投进 DSH 会话（关掉就只做「入向归一化 + dryRun 出向」）
    enabled: true,
    // 每条聊天对应会话的工作目录；留空 = <dataDir>/chats
    cwd: '',
    // 私聊/群聊各自的系统提示词补充片段（走 systemPrompt.context，不进 system 开头）
    systemPrivate: '你正在通过微信与对方私聊。你回复的文字会被直接发到微信。',
    systemGroup: '你正在一个微信群里。你回复的文字会被直接发到群里。',
    // 「今天聊过谁」这类每轮变化的信息，附加几条到运行时上下文
    recentChats: 5,
    // 单条消息最多交给模型多少字（防止一条长消息撑爆上下文）
    maxChars: 4000,
  },
  // 出向决策（v3，CONTRACT §7.3）：拆几条、间隔多久、合并窗口 —— 全在插件侧算。
  outbound: {
    // 拆条：长回复按段落/句末标点拆成多条（false = 整段一条发出去）
    split: true,
    // 单条上限（字）。超过就继续拆
    maxChars: 300,
    // 条间隔（毫秒）：像人一样一条一条发
    intervalMs: 600,
    // 合并窗口（毫秒）：同一会话在这个窗口内连续到达的回复合成一批再拆（0 = 不合并，立刻发）
    mergeWindowMs: 2000,
    // 回退开关：true = 用 v2 的 /send_private_msg、/send_group_msg（deprecated，仅兼容）
    legacyEndpoint: false,
  },
  // 表情决策（简单版）：命中表情库标签就直接配一张；否则按概率随机
  sticker: {
    enabled: true,
    probability: 0.25,
    // T2 写的表情库索引；留空 = ~/.wechat-chatter/stickers/index.json
    indexPath: '',
  },
  // 模块开关：关掉某个模块，它注册的东西（WS/tools/路由/监听器/定时器）会一起撤掉。
  // 四个模块串成一条流水线：connection → inbound → sessions → outbound
  modules: {
    connection: true,
    inbound: true,
    sessions: true,
    outbound: true,
  },
})

const TOKEN_KEYS = ['operatorToken', 'token', 'accessToken', 'access_token', 'password']

function isPlainObject(v) {
  return v !== null && typeof v === 'object' && !Array.isArray(v)
}

function deepMerge(base, patch) {
  if (!isPlainObject(patch)) return base
  const out = Array.isArray(base) ? [...base] : { ...base }
  for (const [k, v] of Object.entries(patch)) {
    if (isPlainObject(v) && isPlainObject(out[k])) out[k] = deepMerge(out[k], v)
    else out[k] = v
  }
  return out
}

function readJson(file) {
  try {
    let text = readFileSync(file, 'utf8')
    if (text.charCodeAt(0) === 0xfeff) text = text.slice(1)
    return JSON.parse(text)
  } catch {
    return null
  }
}

/** 名单字段：接受数组、或逗号/空格分隔的字符串；去重、去空、去重音空白。 */
export function normalizeIdList(raw) {
  const list = Array.isArray(raw) ? raw : String(raw ?? '').split(/[\s,，]+/)
  const out = []
  for (const item of list) {
    const v = String(item ?? '').trim()
    if (v && !out.includes(v)) out.push(v)
  }
  return out
}

/** 谁是主人（决定用哪个 preset）。留空名单 = 没有主人，全走 social。 */
export function isOwner(cfg, wxid) {
  const id = String(wxid ?? '').trim()
  return Boolean(id) && (cfg?.owner?.wxids || []).includes(id)
}

/** 这个群算不算主人的群（群聊里用 owner preset）。 */
export function isOwnerGroup(cfg, groupId) {
  const id = String(groupId ?? '').trim()
  return Boolean(id) && (cfg?.owner?.groups || []).includes(id)
}

/** 去掉末尾斜杠、去掉可能误贴进来的 /ws。 */
export function normalizeHttpUrl(raw) {
  let url = String(raw ?? '').trim()
  if (!url) return DEFAULTS.service.httpUrl
  url = url.replace(/\/+$/, '')
  if (url.endsWith('/ws')) url = url.slice(0, -3)
  return url
}

/** http://host:port → ws://host:port<wsPath>（wss 保持 wss）。 */
export function wsUrlOf(cfg) {
  const base = normalizeHttpUrl(cfg?.service?.httpUrl)
  const path = String(cfg?.service?.wsPath || '/ws')
  const p = path.startsWith('/') ? path : `/${path}`
  return base.replace(/^http/i, 'ws').replace(/\/+$/, '') + p
}

/**
 * 读配置。文件不存在就用默认值（不自动创建：让用户先看过 README 再决定）。
 * @returns {{ config: object, warnings: string[] }}
 */
export function loadConfig({ file = CONFIG_PATH() } = {}) {
  const warnings = []
  const raw = existsSync(file) ? readJson(file) : null
  if (existsSync(file) && raw === null) warnings.push(`配置文件不是合法 JSON，按默认值运行：${file}`)
  let cfg = deepMerge(JSON.parse(JSON.stringify(DEFAULTS)), raw || {})

  // 配置里绝不放密钥：出现就忽略 + 告警（不打印值本身）
  for (const k of TOKEN_KEYS) {
    if (raw && Object.prototype.hasOwnProperty.call(raw, k)) {
      delete cfg[k]
      warnings.push(`配置里出现 ${k} 字段，已忽略（token 只从环境变量/DSH 凭据读）`)
    }
  }
  if (raw && isPlainObject(raw.service)) {
    for (const k of TOKEN_KEYS) {
      if (Object.prototype.hasOwnProperty.call(raw.service, k)) {
        delete cfg.service[k]
        warnings.push(`配置里出现 service.${k} 字段，已忽略（token 只从环境变量/DSH 凭据读）`)
      }
    }
  }

  cfg.service.httpUrl = normalizeHttpUrl(cfg.service.httpUrl)

  // 主人名单：允许字符串（逗号/空格分隔）或数组，统一成去重后的字符串数组
  cfg.owner.wxids = normalizeIdList(cfg.owner.wxids)
  cfg.owner.groups = normalizeIdList(cfg.owner.groups)
  // preset 名：空串 = 不指定 preset（用 profile 默认 agent）
  cfg.presets.owner = String(cfg.presets.owner ?? '').trim() || DEFAULTS.presets.owner
  cfg.presets.social = String(cfg.presets.social ?? '').trim() || DEFAULTS.presets.social
  cfg.presets.enabled = cfg.presets.enabled !== false
  cfg.persona.text = String(cfg.persona.text ?? '') || DEFAULTS.persona.text
  cfg.persona.file = String(cfg.persona.file ?? '').trim()
  cfg.persona.maxChars = Number.isFinite(Number(cfg.persona.maxChars))
    ? Math.max(200, Math.min(200000, Number(cfg.persona.maxChars)))
    : DEFAULTS.persona.maxChars
  cfg.session.recentChats = Number.isFinite(Number(cfg.session.recentChats))
    ? Math.max(0, Math.min(20, Number(cfg.session.recentChats)))
    : DEFAULTS.session.recentChats
  // 模块开关：只认显式 false 关闭（缺省 = 开）
  cfg.modules = { ...DEFAULTS.modules, ...(isPlainObject(cfg.modules) ? cfg.modules : {}) }
  for (const k of Object.keys(DEFAULTS.modules)) cfg.modules[k] = cfg.modules[k] !== false

  // 出向决策：数字字段夹在合理区间；split/legacyEndpoint 只认显式 false/true
  const num = (v, min, max, dflt) => (Number.isFinite(Number(v)) ? Math.max(min, Math.min(max, Number(v))) : dflt)
  cfg.outbound = { ...DEFAULTS.outbound, ...(isPlainObject(cfg.outbound) ? cfg.outbound : {}) }
  cfg.outbound.split = cfg.outbound.split !== false
  cfg.outbound.maxChars = num(cfg.outbound.maxChars, 20, 5000, DEFAULTS.outbound.maxChars)
  cfg.outbound.intervalMs = num(cfg.outbound.intervalMs, 0, 60000, DEFAULTS.outbound.intervalMs)
  cfg.outbound.mergeWindowMs = num(cfg.outbound.mergeWindowMs, 0, 60000, DEFAULTS.outbound.mergeWindowMs)
  cfg.outbound.legacyEndpoint = cfg.outbound.legacyEndpoint === true

  // 表情决策：概率夹到 [0,1]（只认数字；缺省 0.25）
  cfg.sticker = { ...DEFAULTS.sticker, ...(isPlainObject(cfg.sticker) ? cfg.sticker : {}) }
  cfg.sticker.enabled = cfg.sticker.enabled !== false
  cfg.sticker.probability = num(cfg.sticker.probability, 0, 1, DEFAULTS.sticker.probability)
  cfg.sticker.indexPath = String(cfg.sticker.indexPath ?? '').trim()

  cfg.session.cwd = String(cfg.session.cwd || '').trim() || dataDir('chats')
  cfg.session.maxChars = Number.isFinite(Number(cfg.session.maxChars))
    ? Math.max(200, Math.min(20000, Number(cfg.session.maxChars)))
    : DEFAULTS.session.maxChars
  cfg.service.sendTimeoutMs = Number.isFinite(Number(cfg.service.sendTimeoutMs))
    ? Math.max(1000, Math.min(120000, Number(cfg.service.sendTimeoutMs)))
    : DEFAULTS.service.sendTimeoutMs
  return { config: cfg, warnings, path: file, exists: existsSync(file) }
}

export function saveConfig(cfg, { file = CONFIG_PATH() } = {}) {
  const out = JSON.parse(JSON.stringify(cfg))
  for (const k of TOKEN_KEYS) delete out[k]
  if (isPlainObject(out.service)) for (const k of TOKEN_KEYS) delete out.service[k]
  mkdirSync(dirname(file), { recursive: true })
  const tmp = `${file}.tmp-${process.pid}`
  writeFileSync(tmp, `${JSON.stringify(out, null, 2)}\n`, 'utf8')
  renameSync(tmp, file)
  return file
}

/**
 * 真发还是试运行。红线：只有显式 false 才真发。
 * @returns {boolean} true = 试运行（不真发）
 */
export function effectiveDryRun(cfg) {
  return !(cfg && cfg.dryRun === false)
}

export function effectiveEnabled(cfg) {
  return Boolean(cfg && cfg.enabled === true)
}

/**
 * 取 operatorToken。顺序：环境变量 → DSH 凭据服务。绝不读配置文件。
 * @param {object} cfg 已归一化的配置
 * @param {object} [deps]
 * @param {() => object|undefined} [deps.credentials] 返回 ctx.credentials（或 ctx.get('credentials')）
 * @returns {Promise<{ token: string, source: string }>}
 */
export async function resolveOperatorToken(cfg, { credentials } = {}) {
  const envName = String(cfg?.service?.tokenEnv || '').trim()
  if (envName && process.env[envName]) {
    return { token: String(process.env[envName]), source: `env:${envName}` }
  }
  const ref = String(cfg?.service?.credentialRef || '').trim() || envName
  let svc = null
  try {
    svc = typeof credentials === 'function' ? credentials() : credentials
  } catch {
    svc = null
  }
  if (ref && svc && typeof svc.resolve === 'function') {
    try {
      const r = await svc.resolve(ref)
      if (r && r.value) return { token: String(r.value), source: `credentials:${ref}` }
    } catch {
      // 凭据服务里没有这个 ref 不算错误，静默落到「没有 token」
    }
  }
  return { token: '', source: '' }
}
