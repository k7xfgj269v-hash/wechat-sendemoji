// 出向的「决策」纯函数：拆条规则 + 表情挑选。
//
// v3（CONTRACT §7.3）：拆几条、怎么拆、要不要配表情，都在插件侧算；Go 只负责发。
// 这个文件不碰网络、不碰配置读写，只吃文本/列表，好测。

export const DEFAULT_MAX_CHARS = 300

/** 整段就是一个围栏代码块（``` / ~~~）—— 不拆。 */
export function isFencedOnly(text) {
  const s = String(text ?? '').trim()
  if (!s) return false
  const m = s.match(/^(`{3,}|~{3,})[\s\S]*\1\s*$/)
  return Boolean(m)
}

/** 整段就是一条链接（或一个 markdown 链接）—— 不拆。 */
export function isSingleUrl(text) {
  const s = String(text ?? '').trim()
  if (!s) return false
  if (/^https?:\/\/\S+$/i.test(s)) return true
  return /^\[[^\]\n]+\]\(https?:\/\/[^)\s]+\)$/i.test(s)
}

/** 含围栏代码块的段落整段保留（代码不该被句子标点拆开）。 */
function hasFence(text) {
  return /(`{3,}|~{3,})/.test(text)
}

/**
 * 把长回复拆成多条。
 * 规则（从上到下，命中就停）：
 *   1. 整段就是代码块 / 整段就是一条链接 → 原样一条
 *   2. 按空行分段（段落就是一条）
 *   3. 段落超过 maxChars 才继续拆：先按句末标点（。！？!?…；; 和换行），再拼到不超过 maxChars
 *   4. 单句仍然超过 maxChars → 硬切（保证不会有一条超长）
 * @param {string} text
 * @param {{maxChars?: number}} [opts]
 * @returns {string[]} 至少 0 条，永远不会有空串
 */
export function splitText(text, { maxChars = DEFAULT_MAX_CHARS } = {}) {
  const s = String(text ?? '').replace(/\r\n?/g, '\n').trim()
  if (!s) return []
  const limit = Math.max(1, Number(maxChars) || DEFAULT_MAX_CHARS)
  if (isFencedOnly(s) || isSingleUrl(s)) return [s]

  const pieces = []
  const blocks = s.split(/\n{2,}/).map((b) => b.trim()).filter(Boolean)
  for (const block of blocks) {
    if (block.length <= limit || hasFence(block)) {
      pieces.push(block)
      continue
    }
    // 按句末标点 + 换行切，再把短句拼回去
    const sentences = block.split(/(?<=[。！？!?…；;\n])/u).map((x) => x.trim()).filter(Boolean)
    let buf = ''
    for (const sentence of sentences) {
      if (sentence.length > limit) {
        if (buf) {
          pieces.push(buf)
          buf = ''
        }
        for (let i = 0; i < sentence.length; i += limit) pieces.push(sentence.slice(i, i + limit))
        continue
      }
      if (!buf) buf = sentence
      else if (buf.length + sentence.length <= limit) buf += sentence
      else {
        pieces.push(buf)
        buf = sentence
      }
    }
    if (buf) pieces.push(buf)
  }
  return pieces.map((p) => p.trim()).filter(Boolean)
}

/** 标签/名字命中：文本里出现这个标签就直接配这张（长度 <2 的标签太容易误命中，跳过）。 */
export function pickStickerByTag(text, items) {
  const s = String(text ?? '')
  if (!s || !Array.isArray(items)) return null
  for (const item of items) {
    const keys = [item?.name, ...(Array.isArray(item?.tags) ? item.tags : [])]
    for (const key of keys) {
      const k = String(key ?? '').trim()
      if (k.length >= 2 && s.includes(k)) return item
    }
  }
  return null
}

/**
 * 表情决策：标签命中优先，否则按概率随机。
 * @param {string} text 这轮回复的全文
 * @param {object[]} items 表情库条目（{name, tags}）
 * @param {{probability?: number, rng?: () => number}} [opts]
 * @returns {object|null}
 */
export function pickSticker(text, items, { probability = 0.25, rng = Math.random } = {}) {
  const usable = (Array.isArray(items) ? items : []).filter((it) => String(it?.name ?? '').trim())
  if (!usable.length) return null
  const hit = pickStickerByTag(text, usable)
  if (hit) return hit
  const p = Math.max(0, Math.min(1, Number(probability) || 0))
  if (p <= 0) return null
  if (rng() >= p) return null
  return usable[Math.floor(rng() * usable.length) % usable.length] || null
}
