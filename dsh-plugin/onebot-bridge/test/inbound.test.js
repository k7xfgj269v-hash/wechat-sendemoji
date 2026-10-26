import test from 'node:test'
import assert from 'node:assert/strict'
import { normalizeEvent, segmentsToText, primaryType, promptOf } from '../src/inbound.js'

const SELF = 'wxid_self'

test('私聊：归一化成 chatId/chatKind/sender/senderName/text/msgType', () => {
  const r = normalizeEvent(
    {
      group_id: '',
      self_id: SELF,
      user_id: 'wxid_a',
      sender: { user_id: 'wxid_a', nickname: '张三' },
      post_type: 'message',
      message_type: 'private',
      message_id: '1',
      message: [{ type: 'text', data: { text: '在吗' } }],
    },
    { selfId: SELF },
  )
  assert.equal(r.ok, true)
  assert.equal(r.message.chatId, 'private:wxid_a')
  assert.equal(r.message.chatKind, 'private')
  assert.equal(r.message.sender, 'wxid_a')
  assert.equal(r.message.senderName, '张三')
  assert.equal(r.message.text, '在吗')
  assert.equal(r.message.msgType, 'text')
})

test('群聊：chatId 用 group_id，msgType 跟着内容类型走', () => {
  const r = normalizeEvent(
    {
      group_id: '123@chatroom',
      self_id: SELF,
      user_id: 'wxid_b',
      sender: { user_id: 'wxid_b', nickname: '李四' },
      message_type: 'group',
      message: [
        { type: 'text', data: { text: '看这个' } },
        { type: 'image', data: { text: '<xml />' } },
      ],
    },
    { selfId: SELF },
  )
  assert.equal(r.ok, true)
  assert.equal(r.message.chatId, 'group:123@chatroom')
  assert.equal(r.message.chatKind, 'group')
  assert.equal(r.message.text, '看这个[图片]')
  assert.equal(r.message.msgType, 'text')
})

test('纯图片消息：msgType=image，text 给占位（不假装没收到）', () => {
  const r = normalizeEvent(
    { user_id: 'wxid_a', sender: { nickname: '张三' }, message_type: 'private', message: [{ type: 'image', data: { text: '<xml />' } }] },
    { selfId: SELF },
  )
  assert.equal(r.ok, true)
  assert.equal(r.message.msgType, 'image')
  assert.equal(r.message.text, '[图片]')
})

test('自己发的消息跳过（self_id 比对）', () => {
  const r = normalizeEvent({ self_id: SELF, user_id: SELF, message: [{ type: 'text', data: { text: '我' } }] })
  assert.equal(r.ok, false)
  assert.equal(r.reason, 'self')
})

test('post_type 不是 message 的帧跳过（notice 之类）', () => {
  const r = normalizeEvent({ post_type: 'notice', user_id: 'wxid_a', message: [{ type: 'text', data: { text: 'x' } }] })
  assert.equal(r.ok, false)
  assert.match(r.reason, /post_type/)
})

test('没有消息段 / 没有 user_id：跳过而不是造一条空消息', () => {
  assert.equal(normalizeEvent({ user_id: 'wxid_a', message: [] }).ok, false)
  assert.equal(normalizeEvent({ message: [{ type: 'text', data: { text: 'x' } }] }).ok, false)
  assert.equal(normalizeEvent(null).ok, false)
  assert.equal(normalizeEvent('nope').ok, false)
})

test('消息段：text/file/at/record/video/share/未知类型都有交代', () => {
  assert.equal(
    segmentsToText([
      { type: 'file', data: { name: '报告.pdf' } },
      { type: 'record', data: {} },
      { type: 'video', data: {} },
      { type: 'share', data: {} },
      { type: 'at', data: { qq: 'wxid_x' } },
      { type: 'weird', data: {} },
      { type: 'sys', data: {} },
    ]),
    '[文件 报告.pdf][语音][视频][链接]@wxid_x[weird]',
  )
  assert.equal(primaryType([{ type: 'record', data: {} }]), 'record')
  assert.equal(primaryType([{ type: 'sys', data: {} }]), 'text')
})

test('交给模型的文案：私聊/群聊开头不同，空文本有明确说明', () => {
  const priv = normalizeEvent({ user_id: 'wxid_a', sender: { nickname: '张三' }, message: [{ type: 'text', data: { text: '在吗' } }] }).message
  assert.equal(promptOf(priv), '【微信私聊】张三(wxid_a)：在吗')
  const grp = normalizeEvent({ group_id: 'g1', user_id: 'wxid_b', sender: { nickname: '李四' }, message: [{ type: 'image', data: {} }] }).message
  assert.equal(promptOf(grp), '【微信群 g1】李四(wxid_b)：[图片]')
  const empty = normalizeEvent({ user_id: 'wxid_a', sender: {}, message: [{ type: 'sys', data: {} }] }).message
  assert.match(promptOf(empty), /（没有文字）$/)
})
