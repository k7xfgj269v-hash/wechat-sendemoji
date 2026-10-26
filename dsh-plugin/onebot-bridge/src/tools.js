// wechat_reply：把一句话立刻发到当前微信会话。
//
// 为什么只加这一个工具：工具调用的正道是 T1 的 MCP 服务端（mcp__onebot__*），
// 但 MCP 那边没有「发消息」这个 op，所以补这一个原生工具，且只挂在「微信会话」的 agent 作用域里
// （agent.ctx.tools.register），其它 DSH 会话看不到它。

/**
 * @param {object} o
 * @param {{chatId:string, chatKind:string}} o.chat 这个 agent 对应的微信会话
 * @param {{replyNow:Function}} o.hub
 */
export function createReplyTool({ chat, hub }) {
  return {
    name: 'wechat_reply',
    description:
      '把一条消息立刻发到当前微信会话（私聊或群）。发出去之后，本轮结束时插件不会再自动发送你的最终回复；' +
      '想分几条发就用它。试运行（dryRun）时不会真的发，只返回一条试运行回执。',
    parameters: {
      type: 'object',
      properties: {
        text: { type: 'string', description: '要发到微信的文字（不要带 markdown 表格之类，微信不渲染）' },
      },
      required: ['text'],
      additionalProperties: false,
    },
    output: {
      schema: { type: 'object', additionalProperties: true, properties: {} },
      render: (_args, value) => [
        { type: 'text', text: value && value.ok === false ? `✗ ${value.message || '没发出去'}` : `✓ ${value?.message || '已发'}` },
      ],
    },
    async execute(args) {
      const text = String(args?.text ?? '').trim()
      if (!text) return { ok: false, message: '不发空消息' }
      const r = await hub.replyNow(chat.chatId, text)
      if (r?.dryRun) return { ok: true, dryRun: true, message: '试运行：没有真发（日志里能看到这段话）' }
      if (r?.ok) return { ok: true, message: `已发到 ${chat.chatId}` }
      if (r?.skipped) return { ok: false, message: `没发：${r.skipped}` }
      return { ok: false, message: `没发出去：${r?.error || '未知错误'}` }
    },
  }
}
