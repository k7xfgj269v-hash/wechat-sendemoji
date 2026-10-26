// 表情库（只读）：插件需要「有哪些表情、叫什么、什么标签」才能决定配哪张。
//
// 库是 T2（Go）写的：`~/.wechat-chatter/stickers/index.json`，形如
//   { "items": [ { "md5": "...", "name": "无语", "tags": ["无语","白眼"], "uses": 3, ... } ] }
// 这里只读、只缓存（按 mtime），文件坏了就当空库，绝不让出向挂掉。
// 真的发哪张由 `/api/send/sticker` 按 name 在 Go 侧模糊匹配。
//
// 没有索引也能跑：那就只剩「按概率随机」也无从下手 → 表情决策直接返回 null（等价于关掉）。

import { readFileSync, statSync } from 'node:fs'
import { homedir } from 'node:os'
import { isAbsolute, join } from 'node:path'
import { pickSticker } from './reply.js'

export const DEFAULT_INDEX_PATH = '~/.wechat-chatter/stickers/index.json'

/** 展开 ~，相对路径相对项目数据目录。 */
export function resolveIndexPath(cfg) {
  const raw = String(cfg?.sticker?.indexPath ?? '').trim() || DEFAULT_INDEX_PATH
  let file = raw
  if (file === '~' || file.startsWith('~/')) file = join(homedir(), file.slice(1))
  if (!isAbsolute(file)) file = join(process.cwd(), file)
  return file
}

const cache = new Map() // file → { mtimeMs, items, error }

/**
 * 读索引（按 mtime 缓存；坏了返回空库 + error）。
 * @returns {{ items: object[], error: string, file: string }}
 */
export function loadStickerIndex(file, { log } = {}) {
  let mtimeMs = 0
  try {
    mtimeMs = statSync(file).mtimeMs
  } catch (error) {
    cache.set(file, { mtimeMs: -1, items: [], error: `索引不存在：${file}` })
    return { items: [], error: `索引不存在：${file}`, file }
  }
  const hit = cache.get(file)
  if (hit && hit.mtimeMs === mtimeMs) return { items: hit.items, error: hit.error, file }
  let items = []
  let error = ''
  try {
    const parsed = JSON.parse(readFileSync(file, 'utf8').replace(/^\ufeff/, ''))
    const raw = Array.isArray(parsed) ? parsed : parsed?.items
    if (Array.isArray(raw)) {
      items = raw
        .filter((it) => it && typeof it === 'object')
        .map((it) => ({
          name: String(it.name ?? '').trim(),
          tags: Array.isArray(it.tags) ? it.tags.map((t) => String(t).trim()).filter(Boolean) : [],
          md5: String(it.md5 ?? ''),
          uses: Number(it.uses) || 0,
        }))
        .filter((it) => it.name)
    } else {
      error = '索引里没有 items 数组'
    }
  } catch (cause) {
    error = `索引读不了：${cause?.message || cause}`
  }
  if (error && !items.length) log?.warn?.('表情库读不了，表情决策关掉：', error)
  cache.set(file, { mtimeMs, items, error })
  return { items, error, file }
}

/**
 * 造一个「给这段回复挑一张表情」的决策函数（outbound 会注入它）。
 * @param {object} o
 * @param {() => object} o.getConfig
 * @param {object} o.log
 * @param {() => number} [o.rng] 测试注入
 * @returns {(text: string, cfg: object) => {name: string, tags?: string[]}|null}
 */
export function createStickerPicker({ getConfig, log, rng = Math.random }) {
  return function pick(text, cfg = getConfig()) {
    if (!cfg?.sticker?.enabled) return null
    const file = resolveIndexPath(cfg)
    const { items } = loadStickerIndex(file, { log })
    if (!items.length) return null
    const item = pickSticker(text, items, { probability: cfg.sticker.probability, rng })
    return item ? { name: item.name, tags: item.tags } : null
  }
}
