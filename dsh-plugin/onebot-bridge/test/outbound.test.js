import test from 'node:test'
import assert from 'node:assert/strict'
import { buildSendRequest, parseChatId, createSender } from '../src/outbound.js'
import { startFakeOneBot } from './fake-onebot.js'

test('chatId 解析', () => {
  assert.deepEqual(parseChatId('private:wxid_a'), { kind: 'private', id: 'wxid_a' })
  assert.deepEqual(parseChatId('group:123@chatroom'), { kind: 'group', id: '123@chatroom' })
  assert.equal(parseChatId('wxid_a'), null)
  assert.equal(parseChatId('private:'), null)
  assert.equal(parseChatId('system:x'), null)
})

test('请求形状对齐 onebot http.go:20 / param.go:110：id 是字符串，message 是段数组', () => {
  assert.deepEqual(buildSendRequest('private:wxid_a', ' 你好 '), {
    path: '/send_private_msg',
    body: { user_id: 'wxid_a', message: [{ type: 'text', data: { text: '你好' } }] },
  })
  assert.deepEqual(buildSendRequest('group:123@chatroom', '大家好'), {
    path: '/send_group_msg',
    body: { group_id: '123@chatroom', message: [{ type: 'text', data: { text: '大家好' } }] },
  })
  // 数字 id 也照样写成字符串（Go 侧是 string 字段，传数字会 400）
  assert.equal(typeof buildSendRequest('private:10086', 'x').body.user_id, 'string')
})

test('空消息 / 坏 chatId：直接报错，不浪费一次请求', () => {
  assert.throws(() => buildSendRequest('private:wxid_a', '   '), /空消息/)
  assert.throws(() => buildSendRequest('nope', 'x'), /chatId 不合法/)
})

test('真正打一次 HTTP：私聊/群聊两个路径、Bearer 头、错误处理', async () => {
  const bot = await startFakeOneBot()
  try {
    const sender = createSender({ getHttpUrl: () => bot.httpUrl, timeoutMs: 3000, getToken: () => 'tok-123' })
    const ok = await sender.send('private:wxid_a', '在吗')
    assert.equal(ok.ok, true)
    const okGroup = await sender.send('group:g1', '大家好')
    assert.equal(okGroup.ok, true)

    assert.equal(bot.requests.length, 2)
    assert.equal(bot.requests[0].url, '/send_private_msg')
    assert.deepEqual(bot.requests[0].body, { user_id: 'wxid_a', message: [{ type: 'text', data: { text: '在吗' } }] })
    assert.equal(bot.requests[0].headers.authorization, 'Bearer tok-123')
    assert.equal(bot.requests[1].url, '/send_group_msg')
    assert.deepEqual(bot.requests[1].body, { group_id: 'g1', message: [{ type: 'text', data: { text: '大家好' } }] })
  } finally {
    await bot.close()
  }
})

test('没有 token 就不带 Authorization（onebot 的 /send_* 只校验本机来源）', async () => {
  const bot = await startFakeOneBot()
  try {
    const sender = createSender({ getHttpUrl: () => bot.httpUrl, timeoutMs: 3000, getToken: () => '' })
    await sender.send('private:wxid_a', 'x')
    assert.equal(bot.requests[0].headers.authorization, undefined)
  } finally {
    await bot.close()
  }
})

test('onebot 回失败：不抛异常，返回 {ok:false,error}', async () => {
  const errors = []
  const bot = await startFakeOneBot({
    onSend: () => ({ status: 500, json: { status: 'failed', error: '微信代理不可用' } }),
  })
  try {
    const sender = createSender({
      getHttpUrl: () => bot.httpUrl,
      timeoutMs: 3000,
      getToken: () => '',
      onError: (m) => errors.push(m),
    })
    const r = await sender.send('private:wxid_a', 'x')
    assert.equal(r.ok, false)
    assert.equal(r.httpStatus, 500)
    assert.match(r.error, /微信代理不可用/)
    assert.equal(errors.length, 1)
    assert.match(errors[0], /发到 private:wxid_a 失败/)
  } finally {
    await bot.close()
  }
})

test('连不上（端口没人听）：返回错误而不是把宿主搞崩', async () => {
  const sender = createSender({ getHttpUrl: () => 'http://127.0.0.1:1', timeoutMs: 500, getToken: () => '' })
  const r = await sender.send('private:wxid_a', 'x')
  assert.equal(r.ok, false)
  assert.ok(r.error)
})
