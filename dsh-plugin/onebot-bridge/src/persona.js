// 人格：一段文本，注册成提示词变量 {{onebot_persona}}。
//
// 为什么用「变量」而不是把人格拼进每条 user 消息（au_rabot 的做法也是变量）：
//   - preset 里的 @deepseek-ai/dsh-persona 行渲染 {{onebot_persona}}，人格进 system 前缀；
//   - 改了人格，下一轮渲染就生效，不用重建会话；
//   - 会话历史里不留人格文本，前缀缓存也不会被每轮改写打穿。
//
// 读法：persona.file（相对路径按插件数据目录解析）> persona.text > 内置默认。

import { readFileSync, existsSync } from 'node:fs'
import { isAbsolute, join } from 'node:path'
import { dataDir, DEFAULTS } from './config.js'

export const VARIABLE = 'onebot_persona'

/** 把 persona.file 解析成绝对路径（相对路径相对插件数据目录）。 */
export function personaFilePath(cfg) {
  const file = String(cfg?.persona?.file ?? '').trim()
  if (!file) return ''
  return isAbsolute(file) ? file : join(dataDir(), file)
}

/**
 * 现取人格文本。每轮渲染都会调，所以文件改动/配置改动下一轮就生效。
 * @returns {{ text: string, source: string }}
 */
export function resolvePersona(cfg, { log } = {}) {
  const max = Number(cfg?.persona?.maxChars) || DEFAULTS.persona.maxChars
  const file = personaFilePath(cfg)
  if (file) {
    try {
      if (existsSync(file)) {
        const text = readFileSync(file, 'utf8').replace(/^\ufeff/, '').trim()
        if (text) return { text: text.slice(0, max), source: `file:${file}` }
        log?.warn?.(`人格文件是空的，退回配置里的 persona.text：${file}`)
      } else {
        log?.warn?.(`人格文件不存在，退回配置里的 persona.text：${file}`)
      }
    } catch (error) {
      log?.warn?.('读人格文件失败，退回配置里的 persona.text：', error?.message || error)
    }
  }
  const text = String(cfg?.persona?.text ?? '').trim()
  if (text) return { text: text.slice(0, max), source: 'config:persona.text' }
  return { text: DEFAULTS.persona.text, source: 'default' }
}
