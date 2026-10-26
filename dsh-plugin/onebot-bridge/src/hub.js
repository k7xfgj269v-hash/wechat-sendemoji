// 会话中枢：一个微信 chat ↔ 一个持久 DSH 会话。
//   收：归一化后的消息 → 同一 chat 的排队 → 作为一条 user turn 投进去
//   发：那个会话一轮结束 → 取最终正文 → 走 sendText（dryRun 闸门在 sendText 里）发回去
//
// 与 au_rabot 的分工照抄：人格走提示词变量（preset 的 persona 行），
// 「当前在跟谁说话 / 今天聊过谁」这种每轮变化的信息走 agent.ctx.systemPrompt.context()，
// 绝不拼进 system 开头（那样每轮都变，前缀缓存全废）。
//
// DSH 侧用到的接口（与 dsh-aurabot-qq/src/chat/hub.js 同款写法）：
//   ctx.agents.get(sessionId)                     拿活着的 agent
//   ctx.sessionController.create({ cwd })         建会话
//   ctx.sessionController.resolveAgent(sessionId) 恢复会话 → { agent } | { error }
//   ctx.sessionController.rename({ sessionId, title })
//   agent.followup({ id, role: 'user', content, source })
//   ctx.on('session/event', (session, event) => …)   事件：assistant/message、turn/end
//   agent.ctx.tools.register(tool) / agent.ctx.systemPrompt.context({...})

import { randomUUID } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync, renameSync } from 'node:fs'
import { dirname } from 'node:path'
import { dataDir, isOwner, isOwnerGroup } from './config.js'
import { promptOf } from './inbound.js'
import { brief } from './log.js'

export const SOURCE_KIND = 'onebot-bridge'

const sessionKey = (id) => {
  const s = String(id ?? '')
  return s.startsWith('session-') ? s.slice(8) : s
}

function textOf(message) {
  if (!message || !Array.isArray(message.content)) return ''
  return message.content
    .filter((b) => b && b.type === 'text' && typeof b.text === 'string')
    .map((b) => b.text)
    .join('\n')
    .trim()
}

function readJson(file, fallback) {
  try {
    let text = readFileSync(file, 'utf8')
    if (text.charCodeAt(0) === 0xfeff) text = text.slice(1)
    return JSON.parse(text)
  } catch {
    return fallback
  }
}

function writeJson(file, value) {
  mkdirSync(dirname(file), { recursive: true })
  const tmp = `${file}.tmp-${process.pid}`
  writeFileSync(tmp, `${JSON.stringify(value, null, 2)}\n`, 'utf8')
  renameSync(tmp, file)
}

/**
 * @param {object} o
 * @param {object} o.ctx DSH 宿主 ctx
 * @param {() => object} o.getConfig 现取配置（改了配置不用重启）
 * @param {{info:Function,warn:Function,error:Function,debug:Function}} o.log
 * @param {{send:Function, stats:Function}} o.outbound 出向闸门（outbound.js 的 createOutbound）
 * @param {(chat: object) => object|null} [o.buildReplyTool] 返回这个会话要挂的 wechat_reply 工具
 */
export function createHub({ ctx, getConfig, log, outbound, buildReplyTool }) {
  if (!outbound) throw new Error('createHub 需要 outbound（出向闸门）')
  const mapFile = () => dataDir('sessions.json')
  const queues = new Map() // chatId → [msg]
  const busy = new Map() // chatId → 这一轮开始的时间（还没结束）
  // 卡住保护：一轮超过这么久就当它已经结束了（turn/end 丢了也不能让这个 chat 永远哑掉）
  const BUSY_MAX_MS = 5 * 60_000
  const states = new Map() // sessionKey → { lastTurn, text, toolReplied, chatId }
  const mounted = new WeakMap() // agent → { chatId }
  const sessionToChat = new Map() // sessionKey → chatId
  const stats = { received: 0, delivered: 0, sent: 0, dryRun: 0, failed: 0, skipped: 0 }
  const lastSeen = new Map() // chatId → { label, at, count }（「今天聊过谁」用）
  const presetsBroken = new Set() // 建不起来的 preset 名（记住，别每条消息都试一次）
  let sessionsUnavailable = false

  const map = () => readJson(mapFile(), { chats: {} })
  const saveMap = (m) => writeJson(mapFile(), m)

  function stateByKey(key, chatId) {
    let st = states.get(key)
    if (!st) {
      st = { lastTurn: -1, text: '', toolReplied: false, chatId: chatId || '' }
      states.set(key, st)
    }
    if (chatId && !st.chatId) st.chatId = chatId
    return st
  }

  const stateFor = (sessionId, chatId) => stateByKey(sessionKey(sessionId), chatId)
  const isBusy = (chatId) => busy.has(chatId) && Date.now() - busy.get(chatId) < BUSY_MAX_MS

  // ---------- 发 ----------
  /**
   * 唯一的出向闸门：总开关 + dryRun 都在这里判。
   * @returns {Promise<{ok:boolean, dryRun?:boolean, skipped?:string, error?:string}>}
   */
  async function sendText(chatId, text, { via = 'auto' } = {}) {
    // 判定（总开关 / dryRun / 模块开关）全在 outbound 闸门里，这里只记账
    const r = await outbound.send(chatId, text, { via })
    if (r?.dryRun) stats.dryRun++
    else if (r?.ok) stats.sent++
    else if (r?.skipped) stats.skipped++
    else stats.failed++
    return r
  }

  // ---------- 会话 ----------
  function controller() {
    const c = ctx?.sessionController || (typeof ctx?.get === 'function' ? ctx.get('sessionController') : undefined)
    if (!c) {
      if (!sessionsUnavailable) {
        sessionsUnavailable = true
        log.error('宿主没有 sessionController：入向消息只会归一化并记日志，不会进 DSH 会话（需要 DSH 的 web/api 组合）')
      }
      throw new Error('no-session-controller')
    }
    return c
  }

  /** 这个 chat 用哪个 preset：主人 → owner（工具多），其余 → social（收敛）。 */
  function presetFor(cfg, chat) {
    if (!cfg?.presets?.enabled) return ''
    const owner = chat.chatKind === 'group'
      ? isOwnerGroup(cfg, String(chat.groupId || chat.chatId.slice('group:'.length)))
      : isOwner(cfg, chat.sender)
    return owner ? String(cfg.presets.owner || '') : String(cfg.presets.social || '')
  }

  /** 一行话说明「现在在跟谁说话」。 */
  function chatLabel(chat) {
    if (chat.chatKind === 'group') return `微信群 ${String(chat.groupId || chat.chatId.slice('group:'.length))}`
    return `微信私聊 · ${chat.senderName || ''}${chat.sender ? `(${chat.sender})` : ''}`
  }

  const hhmm = (at) => {
    const d = new Date(at)
    const p = (n) => String(n).padStart(2, '0')
    return `${p(d.getHours())}:${p(d.getMinutes())}`
  }

  /** 「今天聊过谁」—— 运行时上下文用，每轮重新算。 */
  function recentChatsText(limit) {
    if (!(limit > 0)) return ''
    const rows = [...lastSeen.entries()]
      .sort((a, b) => b[1].at - a[1].at)
      .slice(0, limit)
      .map(([, v]) => `${v.label}（${v.count} 条，最近 ${hhmm(v.at)}）`)
    return rows.length ? rows.join('；') : ''
  }

  function titleFor(chat) {
    const name = chat.senderName || chat.sender || ''
    return chat.chatKind === 'group' ? `微信 · 群 ${chat.chatId.slice(6)}` : `微信 · ${name || chat.chatId.slice(8)}`
  }

  async function createSession(chat, wantPreset = '') {
    const sc = controller()
    const cfg = getConfig()
    const cwd = String(cfg?.session?.cwd || '').trim() || dataDir('chats')
    mkdirSync(cwd, { recursive: true })
    let preset = presetsBroken.has(wantPreset) ? '' : wantPreset
    let created
    try {
      created = await sc.create(preset ? { cwd, agentPreset: preset } : { cwd })
    } catch (error) {
      if (!preset) throw error
      // preset 没注册（profile 没加我们那层 patch）→ 退回默认 agent，只告警一次
      presetsBroken.add(preset)
      log.warn(`preset ${preset} 建不起来（${error?.message || error}）；先用 profile 默认 agent。想要 owner/social 分工，把 cordis.patch.yml 里那两个 preset 一起挂上`)
      preset = ''
      created = await sc.create({ cwd })
    }
    const sessionId = String(created?.sessionId ?? '')
    if (!sessionId) throw new Error('create 没返回 sessionId')
    try {
      await sc.rename?.({ sessionId, title: titleFor(chat) })
    } catch (error) {
      log.debug('改会话标题失败：', error?.message || error)
    }
    const m = map()
    m.chats[chat.chatId] = { sessionId, preset, createdAt: Date.now(), title: titleFor(chat) }
    saveMap(m)
    log.info(`为 ${chat.chatId} 新建了 DSH 会话 ${sessionKey(sessionId).slice(-8)}${preset ? `（preset ${preset}）` : ''}`)
    return sessionId
  }

  async function resumeAgent(sessionId) {
    const sc = controller()
    const r = await sc.resolveAgent(sessionId)
    if (r && r.agent) return r.agent
    if (r && r.error) throw new Error(r.error.message || String(r.error))
    throw new Error('resolveAgent 没返回 agent')
  }

  function liveAgent(sessionId) {
    const reg = ctx?.agents
    const get = reg && typeof reg.get === 'function' ? (id) => reg.get(id) : null
    if (!get) return null
    try {
      return get(sessionId) || get(`session-${sessionKey(sessionId)}`) || null
    } catch {
      return null
    }
  }

  /** 拿到这个 chat 对应的活 agent（没有就建；preset 变了就换一个新的）。 */
  async function agentFor(chat) {
    const cfg = getConfig()
    const wantPreset = presetFor(cfg, chat)
    // 已经确认建不起来的 preset 就别再提 —— 否则「想要的 preset」和「存下来的空 preset」
    // 永远对不上，每条消息都会新建一个会话。
    const preset = wantPreset && presetsBroken.has(wantPreset) ? '' : wantPreset
    const entry = map().chats[chat.chatId]
    const samePreset = (entry?.preset ?? '') === preset
    let sessionId = samePreset ? String(entry?.sessionId ?? '') : ''
    let agent = null
    if (entry && !samePreset) {
      log.info(`${chat.chatId} 的 preset 变了（${entry?.preset || '默认'} → ${preset || '默认'}），换一个新会话`)
    }
    if (sessionId) {
      agent = liveAgent(sessionId)
      if (!agent) {
        try {
          agent = await resumeAgent(sessionId)
        } catch (error) {
          log.warn(`会话 ${sessionKey(sessionId).slice(-8)} 恢复不了（${error?.message || error}），换一个新的`)
          sessionId = ''
          agent = null
        }
      }
    }
    if (!sessionId) {
      sessionId = await createSession(chat, preset)
      agent = liveAgent(sessionId) || (await resumeAgent(sessionId))
    }
    sessionToChat.set(sessionKey(sessionId), chat.chatId)
    stateFor(sessionId, chat.chatId)
    mount(agent, chat)
    return { agent, sessionId, preset }
  }

  /** 给这个 agent 挂上「回微信」工具和这段聊天的系统提示词补充（每个 agent 只挂一次）。 */
  function mount(agent, chat) {
    if (!agent || mounted.has(agent)) return
    const offs = []
    try {
      const tool = buildReplyTool ? buildReplyTool(chat) : null
      if (tool && agent.ctx?.tools?.register) offs.push(agent.ctx.tools.register(tool))
    } catch (error) {
      log.warn('挂 wechat_reply 工具失败：', error?.message || error)
    }
    try {
      if (agent.ctx?.systemPrompt?.context) {
        offs.push(
          agent.ctx.systemPrompt.context({
            name: 'onebot-bridge-chat',
            order: 900,
            // 这段是运行时上下文，别当成提示词模板去插值（群名/昵称里可能有 {{ ）
            interpolate: false,
            text: () => {
              const cfg = getConfig()
              const base = chat.chatKind === 'group' ? cfg?.session?.systemGroup : cfg?.session?.systemPrivate
              const lines = [String(base || '')]
              // 每轮变化的信息放这里（运行时上下文），不进 system 开头
              lines.push(`当前对象：${chatLabel(chat)}`)
              const recent = recentChatsText(Number(cfg?.session?.recentChats) || 0)
              if (recent) lines.push(`今天聊过：${recent}`)
              lines.push('（你的回复文字会由插件发回微信；不要提“插件”“系统提示词”。）')
              return lines.filter(Boolean).join('\n')
            },
          }),
        )
      }
    } catch (error) {
      log.warn('挂系统提示词补充失败：', error?.message || error)
    }
    mounted.set(agent, { chatId: chat.chatId, offs })
  }

  // ---------- 入 ----------
  async function onMessage(msg) {
    stats.received++
    const cfg = getConfig()
    const max = Number(cfg?.session?.maxChars) || 4000
    const text = String(msg.text ?? '').slice(0, max)
    const chat = {
      chatId: msg.chatId,
      chatKind: msg.chatKind,
      sender: msg.sender,
      senderName: msg.senderName,
      groupId: msg.chatKind === 'group' ? msg.chatId.slice('group:'.length) : '',
    }
    const seen = lastSeen.get(msg.chatId) || { label: chatLabel(chat), at: 0, count: 0 }
    seen.at = Date.now()
    seen.count++
    seen.label = chatLabel(chat)
    lastSeen.set(msg.chatId, seen)
    const q = queues.get(msg.chatId) || []
    q.push({ ...msg, text })
    queues.set(msg.chatId, q)
    if (!cfg?.session?.enabled) {
      log.info(`收到 ${msg.chatId}（会话转发关着，只记日志）：${brief(text)}`)
      queues.delete(msg.chatId)
      return
    }
    await pump(chat)
  }

  async function pump(chat) {
    if (isBusy(chat.chatId)) return
    const q = queues.get(chat.chatId) || []
    if (!q.length) return
    const batch = q.splice(0, q.length)
    const text = batch.map((m) => promptOf(m)).join('\n')
    busy.set(chat.chatId, Date.now())
    try {
      const { agent, sessionId } = await agentFor(chat)
      const st = stateFor(sessionId, chat.chatId)
      st.toolReplied = false
      log.info(`${chat.chatId}：交给她 ${batch.length} 条（会话 ${sessionKey(sessionId).slice(-8)}）`)
      await agent.followup({
        id: randomUUID(),
        role: 'user',
        content: [{ type: 'text', text }],
        source: { kind: SOURCE_KIND },
      })
    } catch (error) {
      busy.delete(chat.chatId)
      stats.failed++
      log.error(`转进 DSH 失败（${chat.chatId}）：`, error?.message || error)
    }
  }

  // ---------- 一轮结束 ----------
  async function onSessionEvent(session, event) {
    if (!event || typeof event !== 'object') return
    const key = sessionKey(session?.id ?? session?.sessionId)
    if (!key) return
    const known = states.get(key) || sessionToChat.has(key)
    if (!known) return
    const chatId = sessionToChat.get(key) || states.get(key)?.chatId || ''
    if (!chatId) return
    const st = stateByKey(key, chatId)
    st.chatId = chatId

    if (event.type === 'assistant/message') {
      const d = event.data || {}
      if (d.interrupted) return
      const t = textOf(d.message)
      if (t) {
        st.lastTurn = d.turn
        st.text = t
      }
      return
    }

    if (event.type !== 'turn/end') return
    const d = event.data || {}
    const completed = d?.reason?.kind === 'completed'
    const text = st.lastTurn === d.turn ? st.text : ''
    const usedTool = st.toolReplied
    st.text = ''
    st.lastTurn = -1
    st.toolReplied = false

    try {
      if (!completed) {
        log.warn(`${chatId} 这一轮没正常结束（${d?.reason?.kind || '?'}），不自动发`)
      } else if (usedTool) {
        log.info(`${chatId}：这一轮已经用 wechat_reply 回过了，不再重发最终正文`)
      } else if (!text) {
        log.debug(`${chatId}：这一轮没有正文，不发`)
      } else {
        const r = await sendText(chatId, text, { via: 'reply' })
        if (r?.ok) stats.delivered++
      }
    } catch (error) {
      log.error(`发回 ${chatId} 出错：`, error?.message || error)
    } finally {
      busy.delete(chatId)
      if (!sessionsUnavailable) {
        const next = queues.get(chatId)
        if (next && next.length) {
          const chat = { chatId, chatKind: chatId.startsWith('group:') ? 'group' : 'private', senderName: next[0].senderName, sender: next[0].sender }
          pump(chat).catch((error) => log.error('继续投递失败：', error?.message || error))
        }
      }
    }
  }

  /** wechat_reply 工具用：立即发一条，并标记这一轮不再自动重发正文。 */
  async function replyNow(chatId, text, sessionIdHint = '') {
    const r = await sendText(chatId, text, { via: 'tool' })
    if (sessionIdHint) stateFor(sessionIdHint, chatId).toolReplied = true
    else {
      for (const st of states.values()) if (st.chatId === chatId) st.toolReplied = true
    }
    return r
  }

  return {
    onMessage,
    onSessionEvent,
    sendText,
    replyNow,
    chatOfSession: (sessionId) => sessionToChat.get(sessionKey(sessionId)) || '',
    sessions: () => map().chats,
    snapshot: () => ({
      stats: { ...stats },
      chats: map().chats,
      busy: [...busy.keys()].filter((id) => isBusy(id)),
      presetsBroken: [...presetsBroken],
      queued: [...queues.entries()].map(([id, q]) => [id, q.length]),
    }),
  }
}
