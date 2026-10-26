// onebot-bridge · DSH 插件入口（生命周期形态对齐 dsh-aurabot-qq/src/host.js）
//
// 微信（onebot）⇄ DSH 会话，一条四段流水线，每段都是可单独开关的模块：
//
//   connection ──frame──> inbound ──message──> sessions ──reply──> outbound
//   （WS 订阅）           （归一化）           （chat ↔ 会话）        （dryRun 闸门 + HTTP）
//
// 生命周期与 au_rabot 同构：
//   · 一切注册都进 ctx.effect(...)（宿主卸载 / HMR 热重载时自动撤）
//   · 看门狗走 ctx.interval（同上）
//   · 收尾统一：模块全停 → WS 关 → 定时器清，不留野 socket / 野 listener
//
// ⚠ 两条红线不许被静默改掉：
//   enabled = false（不手动打开就什么都不做）
//   dryRun  = true （只记日志，不发任何一个字；只有配置里显式 "dryRun": false 才真发）
//
// 工具：不自己注册一堆，工具走 T1 的 MCP 服务端（mcp__onebot__*，名字带 12 位哈希后缀，见 README §5）。
// 只补一个原生工具 wechat_reply（MCP 那边没有「发消息」op），且只挂在微信会话的 agent 作用域里。

import { readFileSync } from 'node:fs'
import {
  loadConfig,
  dataDir,
  effectiveDryRun,
  effectiveEnabled,
  resolveOperatorToken,
  wsUrlOf,
} from './config.js'
import { createLogger, registerSecret } from './log.js'
import { createModules } from './modules.js'
import { connectionModule } from './modules/connection.js'
import { inboundModule } from './modules/inbound.js'
import { sessionsModule } from './modules/sessions.js'
import { outboundModule } from './modules/outbound.js'
import { resolvePersona, VARIABLE as PERSONA_VARIABLE } from './persona.js'

export const name = 'onebot-bridge'
// 看门狗用 ctx.interval（au_rabot 同款）。没有 timer 就没法定时巡检，宁可不启动也别野跑。
export const inject = ['timer']

const WATCHDOG_MS = 30_000
const STATE_PATH = '/onebot-bridge/api/state'

const VERSION = (() => {
  try {
    return JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')).version || '0.0.0'
  } catch {
    return '0.0.0'
  }
})()

export function apply(ctx) {
  let loaded = loadConfig()
  let cfg = loaded.config
  let log = createLogger({ debug: Boolean(cfg.debug) })
  let token = ''
  let tokenSource = ''
  let stopping = false

  const getConfig = () => cfg
  const safe = (fn, label) => () =>
    Promise.resolve()
      .then(fn)
      .catch((error) => log.warn(`${label}出错：`, error?.message || error))

  for (const w of loaded.warnings) log.warn(w)
  logStartup()

  function logStartup() {
    const dry = effectiveDryRun(cfg)
    const on = effectiveEnabled(cfg)
    log.info(
      `配置：${loaded.exists ? loaded.path : '（没有配置文件，用默认值；示例见 README）'}｜` +
        `${on ? '总开关开' : '总开关关'}｜${dry ? 'dryRun 开（只记日志，不真发）' : 'dryRun 关（会真的发到微信！）'}`,
    )
    log.info(`onebot：${cfg.service.httpUrl}（WS ${wsUrlOf(cfg)}）｜会话转发 ${cfg.session.enabled ? '开' : '关'}`)
    const persona = resolvePersona(cfg, { log })
    log.info(
      `人格：${persona.source}（${persona.text.length} 字）｜preset ${cfg.presets.enabled ? `${cfg.presets.owner} / ${cfg.presets.social}` : '关（用 profile 默认 agent）'}`,
    )
    if (!cfg.owner.wxids.length) log.info('主人名单空着：所有人都会用 social preset（想给自己全工具，把 wxid 加进 owner.wxids）')
  }

  // ---------- token：只从环境变量 / DSH 凭据服务读 ----------
  async function loadToken() {
    try {
      const r = await resolveOperatorToken(cfg, {
        credentials: () => {
          try {
            return ctx.get?.('credentials')
          } catch {
            return undefined
          }
        },
      })
      token = r.token
      tokenSource = r.source
      if (token) registerSecret(token)
      log.info(
        token
          ? `operatorToken：已读取（来源 ${tokenSource}，不打印值）`
          : `operatorToken：没有（找的是 ${cfg.service.tokenEnv || 'env'} / DSH 凭据）。onebot 的 /send_* 只校验本机来源，发消息通常不需要它`,
      )
    } catch (error) {
      log.warn('读 operatorToken 出错：', error?.message || error)
    }
  }

  // ---------- 模块（四段流水线，各自可开关） ----------
  const mods = createModules({ ctx, log })
  mods.register(connectionModule())
  mods.register(inboundModule())
  mods.register(sessionsModule())
  mods.register(outboundModule({ getToken: () => token }))

  // ---------- 人格：提示词变量（preset 的 persona 行渲染它） ----------
  try {
    const sp = ctx.systemPrompt || ctx.get?.('systemPrompt')
    if (sp && typeof sp.variable === 'function') {
      ctx.effect(
        () => sp.variable(PERSONA_VARIABLE, () => resolvePersona(getConfig(), { log }).text),
        'onebot-bridge: 人格变量',
      )
    } else {
      log.warn(
        `宿主没有 systemPrompt.variable：{{${PERSONA_VARIABLE}}} 不会被替换（preset 的人格会原样出现），检查 profile 是否挂了 dsh-system-prompt`,
      )
    }
  } catch (error) {
    log.warn('注册人格变量失败：', error?.message || error)
  }

  // ---------- 看门狗：重读配置 → 启停模块 → 连接参数变了就重连 ----------
  function connectionKey(c) {
    return JSON.stringify({
      httpUrl: c.service.httpUrl,
      wsPath: c.service.wsPath,
      tokenEnv: c.service.tokenEnv,
      credentialRef: c.service.credentialRef,
    })
  }
  let lastConnKey = connectionKey(cfg)

  async function watchdog() {
    if (stopping) return
    let next
    try {
      next = loadConfig()
    } catch (error) {
      log.warn('重读配置失败（继续用旧的）：', error?.message || error)
      return
    }
    const before = cfg
    const wasDry = effectiveDryRun(before)
    const wasEnabled = effectiveEnabled(before)
    loaded = next
    cfg = next.config
    for (const w of next.warnings) log.warn(w)
    log = createLogger({ debug: Boolean(cfg.debug) })

    const nowDry = effectiveDryRun(cfg)
    const nowEnabled = effectiveEnabled(cfg)
    if (wasDry !== nowDry) {
      if (nowDry) log.info('dryRun 切回「开」：之后只记日志')
      else log.warn('⚠ dryRun 被关掉了：之后真的会往微信发消息！')
    }
    if (wasEnabled !== nowEnabled) log.info(`${nowEnabled ? '总开关打开' : '总开关关闭'}（配置已重读）`)
    if (cfg.service.tokenEnv !== before.service.tokenEnv || cfg.service.credentialRef !== before.service.credentialRef) {
      await loadToken()
    }

    if (!nowEnabled) {
      // 总开关关着：所有模块停掉（注册的东西一起撤），一个连接都不留
      if (mods.ids().some((id) => mods.isRunning(id))) log.info('总开关关着，停掉所有模块')
      await mods.stopAll()
      return
    }

    const key = connectionKey(cfg)
    if (key !== lastConnKey) {
      lastConnKey = key
      log.info('连接参数变了，重连事件流')
      await mods.restart('connection', getConfig)
    }
    await mods.sync(getConfig)
  }

  // ---------- 只读状态（不含任何密钥） ----------
  function state() {
    const conn = mods.service('connection')
    const sessions = mods.service('sessions')
    const outbound = mods.service('outbound')
    const inbound = mods.service('inbound')
    const snap = sessions?.hub?.snapshot?.() || { stats: {}, chats: {}, busy: [], queued: [] }
    const persona = resolvePersona(cfg, { log })
    return {
      ok: true,
      version: VERSION,
      enabled: effectiveEnabled(cfg),
      dryRun: effectiveDryRun(cfg),
      service: { httpUrl: cfg.service.httpUrl, wsUrl: wsUrlOf(cfg) },
      ws: { state: conn?.state || 'stopped', stats: conn?.stats?.() || {} },
      token: { present: Boolean(token), source: tokenSource || '' },
      persona: { source: persona.source, chars: persona.text.length, variable: PERSONA_VARIABLE },
      presets: {
        enabled: Boolean(cfg.presets.enabled),
        owner: cfg.presets.owner,
        social: cfg.presets.social,
        broken: snap.presetsBroken || [],
      },
      owner: { wxids: cfg.owner.wxids.length, groups: cfg.owner.groups.length },
      inbound: inbound?.stats?.() || {},
      session: {
        enabled: Boolean(cfg.session.enabled),
        cwd: cfg.session.cwd,
        chats: snap.chats,
        stats: snap.stats,
        busy: snap.busy,
        queued: snap.queued,
        outbound: { ...(outbound?.stats?.() || {}), endpoint: outbound?.endpoint?.() || 'api(/api/send/*)', pending: outbound?.pending?.()?.length || 0 },
      },
      modules: mods.status(),
      dataDir: dataDir(),
    }
  }

  try {
    const webServer = ctx.get?.('webServer')
    if (webServer && typeof webServer.register === 'function') {
      ctx.effect(
        () =>
          webServer.register({
            kind: 'exact',
            path: STATE_PATH,
            handler: (req, res) => {
              let body
              try {
                body = JSON.stringify(state())
              } catch (error) {
                body = JSON.stringify({ ok: false, message: String(error?.message || error) })
              }
              try {
                res.statusCode = 200
                res.setHeader?.('content-type', 'application/json; charset=utf-8')
                res.setHeader?.('cache-control', 'no-store')
                res.end?.(body)
              } catch {
                // 客户端断开之类，忽略
              }
            },
          }),
        'onebot-bridge: 状态接口',
      )
    }
  } catch (error) {
    log.debug('注册状态接口失败（不影响收发）：', error?.message || error)
  }

  // ---------- 启动 ----------
  const boot = (async () => {
    await loadToken()
    if (!effectiveEnabled(cfg)) {
      log.info('总开关关着：不连 onebot、不收。想看效果把 config.json 的 enabled 改成 true')
      return
    }
    await mods.sync(getConfig)
  })().catch((error) => log.error('启动失败：', error?.message || error))

  // 看门狗：au_rabot 同样是 ctx.effect(() => ctx.interval(...))，卸载/HMR 自动清
  try {
    ctx.effect(() => ctx.interval(safe(watchdog, '看门狗'), WATCHDOG_MS), 'onebot-bridge: 看门狗')
  } catch {
    // 没有 ctx.interval 的宿主（单测桩）就不开看门狗
  }

  // ---------- 收尾 ----------
  const dispose = async () => {
    if (stopping) return
    stopping = true
    try {
      await mods.dispose()
    } catch (error) {
      log.warn('收尾时停模块出错：', error?.message || error)
    }
    log.info('onebot-bridge 已停止')
  }
  try {
    ctx.effect(() => () => void dispose(), 'onebot-bridge: 收尾')
  } catch {
    // 桩宿主没有 effect
  }

  return {
    name,
    ready: boot,
    state,
    modules: mods,
    get hub() {
      return mods.service('sessions')?.hub
    },
    get client() {
      return mods.service('connection')?.client
    },
    getConfig,
    dispose,
  }
}
