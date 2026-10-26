// 拆条 + 表情挑选的纯规则（没有网络、没有时间）。
//
// 这些规则决定「发几条、每条多长、要不要配表情」—— v3 把这件事从 Go 挪到插件侧
// （CONTRACT §7.3），所以它们值得单独钉住。

import test from 'node:test'
import assert from 'node:assert/strict'
import { splitText, isFencedOnly, isSingleUrl, pickSticker, pickStickerByTag, DEFAULT_MAX_CHARS } from '../src/reply.js'

test('默认上限 300 字', () => {
  assert.equal(DEFAULT_MAX_CHARS, 300)
})

test('短回复不拆：一条就是一条', () => {
  assert.deepEqual(splitText('在的，怎么了？'), ['在的，怎么了？'])
})

test('空 / 空白 → 不产出任何条（不会发空消息）', () => {
  assert.deepEqual(splitText(''), [])
  assert.deepEqual(splitText('   \n\n  '), [])
  assert.deepEqual(splitText(null), [])
})

test('按段落拆：空行分段，每段一条', () => {
  const text = '第一段。\n\n第二段。\n\n第三段。'
  assert.deepEqual(splitText(text), ['第一段。', '第二段。', '第三段。'])
})

test('单段落太长 → 按句末标点拆，尽量拼到不超过上限', () => {
  const s = '一'.repeat(40) + '。' + '二'.repeat(40) + '。' + '三'.repeat(40) + '。'
  const out = splitText(s, { maxChars: 50 })
  assert.equal(out.length, 3)
  for (const p of out) assert.ok(p.length <= 50, `超长了：${p.length}`)
  assert.ok(out[0].endsWith('。'))
})

test('单个句子本身超上限 → 硬切，保证没有超长条', () => {
  const long = '啊'.repeat(1000)
  const out = splitText(long, { maxChars: 300 })
  assert.equal(out.length, 4)
  for (const p of out) assert.ok(p.length <= 300)
  assert.equal(out.join('').length, 1000)
})

test('整段就是一个代码块 → 不拆（哪怕超过上限）', () => {
  const code = '```js\n' + 'const a = 1\n'.repeat(80) + '```'
  assert.equal(isFencedOnly(code), true)
  assert.deepEqual(splitText(code, { maxChars: 100 }), [code])
})

test('含代码块的段落整段保留，不被句号拆开', () => {
  const block = '看这个：\n```js\nfoo.bar()\n```\n好。'
  const out = splitText(block, { maxChars: 20 })
  assert.equal(out.length, 1, `代码块不该被拆：${JSON.stringify(out)}`)
  assert.match(out[0], /```js/)
})

test('整段是一条链接 → 不拆', () => {
  const url = 'https://example.com/' + 'a'.repeat(400)
  assert.equal(isSingleUrl(url), true)
  assert.deepEqual(splitText(url, { maxChars: 50 }), [url])
  const md = `[文档](${url})`
  assert.equal(isSingleUrl(md), true)
  assert.deepEqual(splitText(md, { maxChars: 50 }), [md])
})

test('markdown 列表按行拆（换行也算句末），不会把一条塞爆', () => {
  const list = ['- 第一件事：改配置', '- 第二件事：重启', '- 第三件事：验证'].join('\n')
  const out = splitText(list, { maxChars: 12 })
  assert.ok(out.length >= 3)
  for (const p of out) assert.ok(p.length <= 12 || p.includes('件事'))
})

test('表情：标签命中就直接选它（不看概率）', () => {
  const items = [
    { name: '无语', tags: ['无语', '白眼'] },
    { name: '开心', tags: ['哈哈', '开心'] },
  ]
  const hit = pickSticker('我真是服了，白眼了', items, { probability: 0, rng: () => 0.99 })
  assert.equal(hit.name, '无语')
  assert.equal(pickStickerByTag('今天好开心', items).name, '开心')
})

test('表情：没命中标签时按概率（rng 由测试固定）', () => {
  const items = [{ name: 'a', tags: ['xx'] }, { name: 'b', tags: ['yy'] }]
  assert.equal(pickSticker('随便说点什么', items, { probability: 0.25, rng: () => 0.9 }), null, '概率没中就不发')
  const picked = pickSticker('随便说点什么', items, { probability: 0.25, rng: () => 0.1 })
  assert.ok(picked && ['a', 'b'].includes(picked.name))
})

test('表情：概率 0 / 空库 / 没有名字的条目 → null（不发）', () => {
  const items = [{ name: 'a', tags: [] }]
  assert.equal(pickSticker('随便', items, { probability: 0, rng: () => 0 }), null)
  assert.equal(pickSticker('随便', [], { probability: 1, rng: () => 0 }), null)
  assert.equal(pickSticker('随便', [{ name: '', tags: [] }], { probability: 1, rng: () => 0 }), null)
})

test('表情：单字标签不命中（太容易误中）', () => {
  const items = [{ name: '单', tags: ['好'] }]
  assert.equal(pickStickerByTag('今天真好', items), null)
})
