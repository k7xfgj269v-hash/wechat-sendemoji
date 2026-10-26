// 日志：先脱敏再输出。operatorToken 只以「有没有」的形式出现，绝不进日志。
// 只用 console（DSH 会接住 stdout/stderr 到自己的日志），不额外写文件。

const PATTERNS = [
  // Authorization: Bearer xxx / token=xxx / password: xxx
  [/(Authorization["']?\s*[:=]\s*["']?)(?:Bearer\s+)?[A-Za-z0-9._~+/=-]{6,}/gi, '$1***'],
  [/((?:access[_-]?token|operator[_-]?token|token|password|passwd|pwd|secret|api[_-]?key)["']?\s*[:=]\s*["']?)([^\s"',;&]{4,})/gi, '$1***'],
  [/(Bearer\s+)[A-Za-z0-9._~+/=-]{6,}/g, '$1***'],
  // access_token=... 查询参数（WS 地址里可能出现）
  [/([?&](?:access_token|token|key)=)[^&\s]+/gi, '$1***'],
]

/** 已知的敏感值：出现就整段替换成 ***（registerSecret 由 config.js 在解出 token 后调用）。 */
const known = new Set()

export function registerSecret(value) {
  const v = String(value ?? '')
  if (v.length >= 6) known.add(v)
}

export function redact(text) {
  let s = String(text ?? '')
  for (const v of known) if (s.includes(v)) s = s.split(v).join('***')
  for (const [re, rep] of PATTERNS) s = s.replace(re, rep)
  return s
}

const fmt = (v) => {
  if (v instanceof Error) return v.message
  if (typeof v === 'string') return v
  try {
    return JSON.stringify(v)
  } catch {
    return String(v)
  }
}

export const PREFIX = '[onebot-bridge]'

export function createLogger({ debug = false } = {}) {
  const write = (level, args) => {
    const text = redact(args.map(fmt).join(' '))
    const line = `${PREFIX} ${text}`
    if (level === 'error') console.error(line)
    else if (level === 'warn') console.warn(line)
    else if (level === 'debug') {
      if (debug) console.log(line)
    } else console.log(line)
  }
  return {
    debug: (...a) => write('debug', a),
    info: (...a) => write('info', a),
    warn: (...a) => write('warn', a),
    error: (...a) => write('error', a),
  }
}

/** 一句话摘要用：截断 + 去掉换行，日志里看得清又不刷屏。 */
export function brief(text, max = 120) {
  const s = String(text ?? '').replace(/\s+/g, ' ').trim()
  return s.length > max ? `${s.slice(0, max)}…` : s
}
