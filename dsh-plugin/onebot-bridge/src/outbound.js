// 出向：决定「发几条、怎么拆、间隔多久、要不要配表情」，以及唯一的 dryRun 闸门。
//
// v3（CONTRACT §7.3）之前：插件把整段回复丢给 onebot，发几条是 Go 的事。
// v3 之后：
//   文本 → 合并窗口（同会话连续回复合成一批）→ 拆条 → 逐条过闸门 → 条间隔 → 表情
// Go 侧只收到 `POST /api/send/text|sticker|image` 这样的薄指令，不再有「AI 回复」概念。
//
// 拆条/表情挑选的纯规则在 reply.js；HTTP 在 transport 层（createApiTransport）；
// 这里只负责时间线 + 计数 + 闸门。

import { effectiveDryRun, effectiveEnabled } from './config.js'
import { splitText, DEFAULT_MAX_CHARS } from './reply.js'

/** chatId：`private:<id>` / `group:<id>`。 */
export function parseChatId(chatId) {
  const s = String(chatId ?? '')
  const idx = s.indexOf(':')
  if (idx <= 0) return null
  const kind = s.slice(0, idx)
  const id = s.slice(idx + 1)
  if (!id) return null
  if (kind !== 'private' && kind !== 'group') return null
  return { kind, id }
}

/**
 * chatId → §7.2 的 target。
 *
 * ⚠ 这是「chatId 语义」和「target 语义」之间**唯一**的翻译点：目前 target 原样用
 * `private:<wxid>` / `group:<gid>`（Go 侧按前缀分派最省事）。T8a 若改成「target 只放 id，
 * 另加 kind」，**只改这一个函数**。
 * 额外带 target_kind / target_id 当提示：Go 的 json 解码忽略未知字段，不影响解析。
 */
export function apiTargetOf(chatId) {
  const t = parseChatId(chatId)
  if (!t) return null
  return { target: `${t.kind}:${t.id}`, target_kind: t.kind, target_id: t.id }
}

// ---------------------------------------------------------------------------
// 新出口（§7.2）：薄、单条、失败显式
// ---------------------------------------------------------------------------

/**
 * @param {object} o
 * @param {() => string} o.getHttpUrl 每次现取（改配置不用重启）
 * @param {() => string} [o.getToken]
 * @param {number} [o.timeoutMs]
 * @param {(msg:string)=>void} [o.onError]
 */
export function createApiTransport({ getHttpUrl, getToken = () => '', timeoutMs = 15000, onError }) {
  const base = () => String(getHttpUrl?.() ?? '').replace(/\/+$/, '')

  async function post(path, body) {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    try {
      const headers = { 'content-type': 'application/json' }
      const token = String(getToken() || '')
      if (token) headers.authorization = `Bearer ${token}`
      const res = await fetch(`${base()}${path}`, {
        method: 'POST',
        headers,
        body: JSON.stringify(body),
        signal: controller.signal,
      })
      const raw = await res.text()
      let json = null
      try {
        json = raw ? JSON.parse(raw) : null
      } catch {
        json = null
      }
      if (!res.ok) {
        const reason = String(json?.reason || json?.error || json?.message || raw || '').slice(0, 300) || `HTTP ${res.status}`
        onError?.(`${path} 失败：${reason}`)
        return { ok: false, reason, httpStatus: res.status }
      }
      if (!json || json.ok !== true) {
        // 200 但 ok:false —— 按失败处理，不许当成功（§7.2「失败必须显式」）
        const reason = String(json?.reason || json?.error || json?.message || 'ok:false').slice(0, 300)
        onError?.(`${path} 失败：${reason}`)
        return { ok: false, reason, httpStatus: res.status, task_id: json?.task_id }
      }
      return { ok: true, task_id: json?.task_id, queued_at: json?.queued_at, httpStatus: res.status }
    } catch (error) {
      const reason = error?.name === 'AbortError' ? `超时（${timeoutMs}ms）` : String(error?.message || error)
      onError?.(`${path} 出错：${reason}`)
      return { ok: false, reason }
    } finally {
      clearTimeout(timer)
    }
  }

  return {
    async sendText({ target, text, at } = {}) {
      const body = String(text ?? '').trim()
      if (!body) return { ok: false, reason: 'empty-text' }
      if (!target) return { ok: false, reason: 'missing-target' }
      const payload = { target, text: body }
      if (Array.isArray(at) && at.length) payload.at = at.map(String)
      return post('/api/send/text', payload)
    },
    async sendSticker({ target, name } = {}) {
      const sticker = String(name ?? '').trim()
      if (!sticker) return { ok: false, reason: 'missing-sticker-name' }
      if (!target) return { ok: false, reason: 'missing-target' }
      return post('/api/send/sticker', { target, name: sticker })
    },
    async sendImage({ target, image_base64, path } = {}) {
      if (!target) return { ok: false, reason: 'missing-target' }
      const payload = { target }
      if (image_base64) payload.image_base64 = String(image_base64)
      else if (path) payload.path = String(path)
      else return { ok: false, reason: 'missing-image' }
      return post('/api/send/image', payload)
    },
  }
}

// ---------------------------------------------------------------------------
// DEPRECATED：OneBot 语义的老出口，只保留兼容（老 profile 的 outbound.legacyEndpoint）
// ---------------------------------------------------------------------------

/** @deprecated 用 apiTargetOf + createApiTransport。 */
export function buildSendRequest(chatId, text) {
  const target = parseChatId(chatId)
  if (!target) throw new Error(`chatId 不合法：${chatId}`)
  const body = String(text ?? '').trim()
  if (!body) throw new Error('不发空消息')
  const message = [{ type: 'text', data: { text: body } }]
  return target.kind === 'group'
    ? { path: '/send_group_msg', body: { group_id: target.id, message } }
    : { path: '/send_private_msg', body: { user_id: target.id, message } }
}

/**
 * @deprecated v2 的整段发送器（`/send_private_msg` / `/send_group_msg`）。
 * 仅用于 `outbound.legacyEndpoint: true`（回退）和 T8a 上线前的对照，新代码别用。
 */
export function createSender({ getHttpUrl, timeoutMs = 15000, getToken = () => '', onError }) {
  const base = () => String(getHttpUrl?.() ?? '').replace(/\/+$/, '')

  async function send(chatId, text) {
    let req
    try {
      req = buildSendRequest(chatId, text)
    } catch (error) {
      return { ok: false, error: error.message }
    }
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    try {
      const headers = { 'content-type': 'application/json' }
      const token = String(getToken() || '')
      if (token) headers.authorization = `Bearer ${token}`
      const res = await fetch(`${base()}${req.path}`, {
        method: 'POST',
        headers,
        body: JSON.stringify(req.body),
        signal: controller.signal,
      })
      const raw = await res.text()
      let json = null
      try {
        json = raw ? JSON.parse(raw) : null
      } catch {
        json = null
      }
      const status = String(json?.status ?? '')
      const ok = res.ok && (status === 'ok' || status === '' || status === 'async')
      if (ok) return { ok: true, httpStatus: res.status, status: status || 'ok' }
      const error = String(json?.error || json?.message || raw || `HTTP ${res.status}`).slice(0, 300)
      onError?.(`发到 ${chatId} 失败：HTTP ${res.status} ${error}`)
      return { ok: false, httpStatus: res.status, status, error }
    } catch (error) {
      const message = error?.name === 'AbortError' ? `超时（${timeoutMs}ms）` : String(error?.message || error)
      onError?.(`发到 ${chatId} 出错：${message}`)
      return { ok: false, error: message }
    } finally {
      clearTimeout(timer)
    }
  }

  return { send }
}

// ---------------------------------------------------------------------------
// 出向闸门 + 节奏（outbound 模块拥有它）
//   - 总开关 / dryRun / 模块开关：唯一判定点，拆条之后**每一块**都过这里
//   - 合并窗口：同一会话窗口内连续到达的回复合成一批
//   - 拆条 / 条间隔 / 表情：reply.js + 注入的 pickSticker
// ---------------------------------------------------------------------------

/**
 * @param {object} o
 * @param {() => object} o.getConfig
 * @param {object|(() => object)} o.transport createApiTransport() 的产物（给函数就每次现取，便于配置切换）
 * @param {{info:Function,warn:Function,debug:Function,error:Function}} o.log
 * @param {(text:string, max?:number)=>string} [o.brief]
 * @param {() => boolean} [o.isEnabled] 模块开关
 * @param {(text:string, cfg:object) => {name?:string}|null} [o.pickSticker] 表情决策
 * @param {(ms:number) => Promise<void>} [o.sleep] 测试注入（默认 setTimeout）
 */
export function createOutbound({
  getConfig,
  transport,
  log,
  brief = (t, m = 120) => String(t ?? '').slice(0, m),
  isEnabled = () => true,
  pickSticker = null,
  sleep = null,
}) {
  const stats = { sent: 0, dryRun: 0, failed: 0, skipped: 0, pieces: 0, stickers: 0, merged: 0 }
  const wait = sleep || ((ms) => new Promise((r) => setTimeout(r, Math.max(0, ms))))
  const bursts = new Map() // chatId → { texts, timer, resolve[], via }

  const windowOf = (cfg) => {
    const n = Number(cfg?.outbound?.mergeWindowMs)
    return Number.isFinite(n) && n > 0 ? n : 0
  }

  /**
   * 发一条（文本）。先进合并窗口，窗口结束（或 flush）时拆条逐条发。
   * @returns {Promise<{ok:boolean, dryRun?:boolean, skipped?:string, error?:string, pieces?:number, sticker?:string}>}
   */
  function send(chatId, text, { via = 'auto' } = {}) {
    const body = String(text ?? '').trim()
    if (!body) return Promise.resolve({ ok: false, error: 'empty' })
    return new Promise((resolve) => {
      const pending = bursts.get(chatId)
      if (pending) {
        pending.texts.push(body)
        pending.resolve.push(resolve)
        pending.via = via
        stats.merged++
        return
      }
      const burst = { texts: [body], timer: null, resolve: [resolve], via }
      bursts.set(chatId, burst)
      const windowMs = windowOf(getConfig())
      if (windowMs > 0) {
        // 注意：这里**不** unref —— 攒着的那批回复必须真的被冲出去，
        // 不能被「进程没有别的活」顺手丢掉（那等于静默吞消息）。
        burst.timer = setTimeout(() => void flush(chatId), windowMs)
      } else {
        queueMicrotask(() => void flush(chatId))
      }
    })
  }

  /** 把某个会话攒着的回复发出去（也用于测试里的确定性收尾）。 */
  async function flush(chatId) {
    const burst = bursts.get(chatId)
    if (!burst) return null
    bursts.delete(chatId)
    if (burst.timer) clearTimeout(burst.timer)
    // 用空行接：合并的是「一批」（一次节奏），每段回复仍然是独立的一条消息（拆条按段落切）
    const result = await deliver(chatId, burst.texts.join('\n\n'), { via: burst.via, count: burst.texts.length })
    for (const resolve of burst.resolve) resolve(result)
    return result
  }

  async function flushAll() {
    const out = []
    for (const chatId of [...bursts.keys()]) out.push(await flush(chatId))
    return out
  }

  /** 真正发一批：拆条 → 逐条过闸门 → 条间隔 → 表情。 */
  async function deliver(chatId, text, { via = 'auto', count = 1 } = {}) {
    const cfg = getConfig()
    if (!isEnabled()) {
      stats.skipped++
      log.info(`出向模块关着，没发（${via}）→ ${chatId}：${brief(text)}`)
      return { ok: false, skipped: 'outbound-module-off' }
    }
    if (!effectiveEnabled(cfg)) {
      stats.skipped++
      log.info(`总开关关着，没发（${via}）→ ${chatId}：${brief(text)}`)
      return { ok: false, skipped: 'disabled' }
    }
    const target = apiTargetOf(chatId)
    if (!target) {
      stats.failed++
      log.warn(`chatId 不合法，没发：${chatId}`)
      return { ok: false, error: `bad-chat-id:${chatId}` }
    }

    const tp = typeof transport === 'function' ? transport() : transport
    if (!tp || typeof tp.sendText !== 'function') {
      stats.failed++
      log.warn(`出向传输层不可用，没发 → ${chatId}`)
      return { ok: false, error: 'no-transport' }
    }
    const pieces = splitForConfig(text, cfg)
    if (count > 1) log.info(`${chatId}：合并了 ${count} 段回复，拆成 ${pieces.length} 条`)
    else if (pieces.length > 1) log.debug(`${chatId}：拆成 ${pieces.length} 条`)

    // dryRun：每一块都过闸门（只记日志，一个请求都不发）
    if (effectiveDryRun(cfg)) {
      for (let i = 0; i < pieces.length; i++) {
        stats.dryRun++
        stats.pieces++
        log.info(`[dry-run] 不真发（${via}，第 ${i + 1}/${pieces.length} 条）→ ${chatId}：${brief(pieces[i])}`)
      }
      const sticker = decideSticker(cfg, text)
      if (sticker) {
        stats.dryRun++
        log.info(`[dry-run] 不真发（表情）→ ${chatId}：${sticker.name}`)
      }
      return { ok: true, dryRun: true, pieces: pieces.length, sticker: sticker?.name || '' }
    }

    const intervalMs = Math.max(0, Number(cfg?.outbound?.intervalMs) || 0)
    const results = []
    for (let i = 0; i < pieces.length; i++) {
      if (i > 0 && intervalMs > 0) await wait(intervalMs)
      const r = await tp.sendText({ target: target.target, text: pieces[i] })
      stats.pieces++
      if (r.ok) {
        stats.sent++
        log.info(`已发（${via}，第 ${i + 1}/${pieces.length} 条）→ ${chatId}：${brief(pieces[i])}`)
      } else {
        stats.failed++
        log.warn(`没发出去（${via}，第 ${i + 1}/${pieces.length} 条）→ ${chatId}：${r.reason || '未知原因'}`)
      }
      results.push(r)
    }

    // 表情：失败静默跳过，绝不打断文本
    const sticker = decideSticker(cfg, text)
    if (sticker) {
      if (intervalMs > 0) await wait(intervalMs)
      try {
        const r = await tp.sendSticker({ target: target.target, name: sticker.name })
        if (r.ok) {
          stats.sent++
          stats.stickers++
          log.info(`已发（表情）→ ${chatId}：${sticker.name}`)
        } else {
          log.debug(`表情没发出去（跳过，不影响文本）→ ${chatId}：${r.reason || '未知原因'}`)
        }
      } catch (error) {
        log.debug(`表情发送抛错（跳过，不影响文本）→ ${chatId}：`, error?.message || error)
      }
    }

    const okCount = results.filter((r) => r.ok).length
    return { ok: okCount === results.length, pieces: pieces.length, sticker: sticker?.name || '', failed: results.length - okCount }
  }

  function splitForConfig(text, cfg) {
    if (cfg?.outbound?.split === false) {
      const whole = String(text ?? '').trim()
      return whole ? [whole] : []
    }
    const maxChars = Number(cfg?.outbound?.maxChars) || DEFAULT_MAX_CHARS
    return splitText(text, { maxChars })
  }

  function decideSticker(cfg, text) {
    if (!cfg?.sticker?.enabled || !pickSticker) return null
    try {
      return pickSticker(text, cfg) || null
    } catch (error) {
      log.debug('表情决策出错（跳过）：', error?.message || error)
      return null
    }
  }

  return {
    send,
    flush,
    flushAll,
    deliver,
    stats: () => ({ ...stats }),
    pending: () => [...bursts.keys()],
  }
}
