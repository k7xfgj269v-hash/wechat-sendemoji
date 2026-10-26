# onebot-bridge —— 让 DSH 会话跟微信聊天

DSH 插件（**窄而真**的最小可用版）：

```
微信 ⇄ onebot（hr-workbench，本仓库的 Go 程序）⇄ 本插件 ⇄ 一个持久 DSH 会话
                                                    ↑
                        tools/list 走 T1 的 MCP 服务端（mcp__onebot__*，独立条目）
```

- **入向**：连 onebot 的 `ws://<host>/ws`，收 `SendWebSocketMsg`（`websocket.go:148`）推下来的消息帧 →
  归一化成 `{ chatId, chatKind, sender, senderName, text, msgType }` → 交给那个 chat 对应的 DSH 会话。
- **出向**：会话一轮正常结束 → 插件决定「拆几条、间隔多久、配不配表情」→ 逐条 `POST /api/send/text|sticker`
  （v3，CONTRACT §7.3；旧的 `/send_private_msg` 只作回退保留，见 §4.4）。
- **工具**：**不自己注册一堆工具**。工具调用的正道是 T1 的 MCP 服务端（`mcp__onebot__*`，见[§5](#5-mcp-条目必挂)）。
  只补一个原生工具 `wechat_reply`，因为 MCP 那边没有「发消息」这个 op。
- **安全默认**：`enabled: false`（不打开就什么都不做）+ `dryRun: true`（只记日志，一个字节都不发）。

---

## 1. 现在到什么程度（诚实清单）

**已经用 `node --test` 真跑过（110 个用例全绿，不需要真微信）：**

| 链路 | 怎么验的 |
|---|---|
| 入向 WS 收帧 | 手写了一个真 WebSocket 服务端（`test/fake-onebot.js`，真握手/真帧），插件用 Node 全局 `WebSocket` 真连上去，断线后真重连 |
| 入向归一化 | private/group、纯图片、自己发的、notice 帧、缺字段、`post_type` 缺失（见下面 §7 的坑）全部覆盖 |
| 出向 HTTP | 真 `POST /api/send/text` / `/api/send/sticker` 打到假 onebot，断言 `target`/`text` 形状、Bearer 头、`ok:false` 当失败、失败/超时/端口无人听的降级；旧 `/send_*` 只测回退那一条 |
| dryRun 闸门 | 默认（含 `null`/`"false"`/`0` 等写法）一律不发；只有配置里显式 `"dryRun": false` 才真发 |
| 模块骨架 | 启停/开关/热重载/出错隔离/作用域回收单独测；关掉 `outbound` 一个请求都不发、关掉 `sessions` 不建会话、`dispose` 之后 WS 真断开 |
| preset + 人格 | 主人/陌生人/群三条路各自选对 preset；preset 建不起来退回默认 agent 且只告警一次；`{{onebot_persona}}` 变量注册且文本来自配置 |
| 出向决策（v3） | 拆条规则（段落/句号/代码块/链接/硬切）、合并窗口、条间隔、表情标签命中与概率、`/api/send/*` body 形状、`ok:false` 当失败、表情失败静默跳过、旧出口回退 |
| dryRun × 拆条 | 一条回复拆成 3 条 + 1 张表情，**零 HTTP**（e2e 断言 `bot.requests.length === 0`） |
| 配置 | 默认值、坏 JSON、`httpUrl` 归一化、token 只从 env/凭据读、配置里写了 token 会被忽略并告警 |
| 会话投递 | 用一套**形状照抄 DSH** 的桩验证：`sessionController.create/resolveAgent/rename`、`agent.followup`、`session/event` 的 `assistant/message` / `turn/end`、`agent.ctx.tools.register`、`agent.ctx.systemPrompt.context` |
| 全链路 | 假 onebot（WS+HTTP）→ 插件本体（`apply()`）→ DSH 桩 → 一轮结束 → 真 HTTP 发回（dryRun 开时零请求） |

**还没在真 DSH 里跑过**（我没有去动你正在用的 `web` profile，也没新建 profile）：
`test/hub.test.js` 与 `test/e2e.test.js` 里的 `sessionController` / `agents` / `followup` / `session/event`
是**桩**，形状按 `dsh-aurabot-qq/src/chat/hub.js`（真实在跑的插件）和 DSH 包的类型声明抄的。
真宿主联调请按 [§8](#8-待验证清单-todo) 的清单做一遍 —— 那是最值得先跑的一条。

---

## 2. 安装

本目录就是插件包（`package.json` 里 `dsh.bundle.patch` 指向自带的 `cordis.patch.yml`，
所以 `dsh plugin add` 会**自动**把它并进 profile 的 `dsh.profile.bundles`）。

**⚠ 用一个独立 profile，别动正在跑的那个。** 下面用 `onebot` 当例子名（随便起）：

```bash
# 1) 建/装（--profile 指到新名字时 DSH 会按默认模板建一个）
dsh plugin --profile onebot add <repo>/dsh-plugin/onebot-bridge

# 2) 确认 bundle 里有了
grep -A6 '"bundles"' ~/.dsh/profiles/onebot/package.json

# 3) 起（web 形态；端口别跟正在跑的 3080 撞）
dsh --profile onebot web --port 3099
```

> `dsh plugin add <本地路径>` 是 pnpm 装包；插件自带的 `cordis.patch.yml` 会被当成 profile 的一层
> —— 如果 `bundles` 里没出现 `onebot-bridge`，手动加进 `~/.dsh/profiles/onebot/cordis.patch.yml`（见 §6）。

不需要装任何 npm 依赖：插件只用 Node 22 自带的东西（`fetch`、全局 `WebSocket`、`node:fs`、`node:crypto`）。

---

## 3. 配置

配置文件：`~/.dsh/plugins-data/onebot-bridge/config.json`
（`$DSH_HOME/plugins-data/onebot-bridge/config.json`；`ONEBOT_BRIDGE_DATA_DIR` 可以整段改到别处）

**文件不存在时用默认值，不会自动创建**。想动配置就先 `mkdir -p` 再写一个：

```json
{
  "enabled": true,
  "dryRun": true,
  "service": {
    "httpUrl": "http://127.0.0.1:58080",
    "wsPath": "/ws",
    "tokenEnv": "ONEBOT_AUTOMATION_OPERATOR_TOKEN",
    "credentialRef": "",
    "sendTimeoutMs": 15000
  },
  "persona": {
    "text": "你在微信上和一个人聊天。像本人一样说话：短句、口语、别用列表和小标题。",
    "file": "",
    "maxChars": 20000
  },
  "owner": { "wxids": ["wxid_你自己的号"], "groups": [] },
  "presets": { "enabled": true, "owner": "onebot-owner", "social": "onebot-social" },
  "session": {
    "enabled": true,
    "cwd": "",
    "systemPrivate": "你正在通过微信与对方私聊。",
    "systemGroup": "你正在一个微信群里。",
    "recentChats": 5,
    "maxChars": 4000
  },
  "outbound": {
    "split": true,
    "maxChars": 300,
    "intervalMs": 600,
    "mergeWindowMs": 2000,
    "legacyEndpoint": false
  },
  "sticker": { "enabled": true, "probability": 0.25, "indexPath": "" },
  "modules": { "connection": true, "inbound": true, "sessions": true, "outbound": true }
}
```

| 字段 | 默认 | 说明 |
|---|---|---|
| `enabled` | `false` | 总开关。**只认显式 `true`**；关着不连 onebot、不收不发 |
| `dryRun` | `true` | 试运行。**只有显式 `"dryRun": false` 才真发**；`null`/`0`/`"false"` 一律按 true |
| `service.httpUrl` | `http://127.0.0.1:58080` | onebot HTTP 地址（`-receive_host`）。误贴 `/ws` 或尾斜杠会被自动去掉 |
| `service.wsPath` | `/ws` | 事件流路径（`core_routes.go:16` 的 `/ws`） |
| `service.tokenEnv` | `ONEBOT_OPERATOR_TOKEN` | **只放变量名，不放值**。onebot 侧实际用的是 `ONEBOT_AUTOMATION_OPERATOR_TOKEN`，请按上面示例改 |
| `service.credentialRef` | `""` | DSH 凭据服务里的 ref，留空 = 与 `tokenEnv` 同名 |
| `persona.text` | 一段中性默认人格 | 人格文本，注入提示词变量 `{{onebot_persona}}`（preset 的 persona 行渲染它） |
| `persona.file` | `""` | 非空则读这个文件当人格（相对路径相对数据目录），优先于 `text`；改了下一条消息就生效 |
| `owner.wxids` | `[]` | 主人 wxid 列表（数组，或逗号/空格分隔的字符串）。**留空 = 所有人都是 social** |
| `owner.groups` | `[]` | 主人群列表：这些群里的消息也用 owner preset |
| `presets.enabled` | `true` | 关掉就不给会话指定 preset（用 profile 默认 agent） |
| `presets.owner` / `presets.social` | `onebot-owner` / `onebot-social` | 两个 preset 的 id，与 `cordis.patch.yml` 里一致 |
| `session.enabled` | `true` | 关掉就只做「入向归一化 + dryRun 出向」，不碰 DSH 会话 |
| `session.recentChats` | `5` | 运行时上下文里列几条「今天聊过谁」（0 = 不列） |
| `outbound.split` | `true` | 长回复拆成多条（只有显式 `false` 才关 = 整段一条发）。拆条规则见 §4b |
| `outbound.maxChars` | `300` | 单条上限（20–5000 字） |
| `outbound.intervalMs` | `600` | 条间隔（0–60000ms）。像人一样一条一条发 |
| `outbound.mergeWindowMs` | `2000` | 合并窗口（0–60000ms）：同一会话窗口内连续到达的回复合成一批；`0` = 不合并 |
| `outbound.legacyEndpoint` | `false` | **回退开关**：`true` 时打旧的 `/send_private_msg`（deprecated），拿不到表情/图片 |
| `sticker.enabled` | `true` | 表情决策总开关（关掉就纯文字） |
| `sticker.probability` | `0.25` | 没命中标签时随机配一张的概率（0–1） |
| `sticker.indexPath` | `""` | 表情库索引，留空 = `~/.wechat-chatter/stickers/index.json`（T2 写的） |
| `modules.*` | 全 `true` | 四个模块的开关，**只有显式 `false` 才关**；关掉会把该模块注册的东西一起撤掉 |
| `session.cwd` | `<数据目录>/chats` | 微信会话的工作目录（建 session 时用 `cwd`，不依赖 `workspaceRegistry`） |
| `session.systemPrivate` / `systemGroup` | 见上 | 每组会话追加的系统提示词片段 |
| `session.maxChars` | `4000` | 单条消息交给模型的最大字数 |

### operatorToken 从哪来（绝不进代码、进配置、进日志）

顺序：

1. `process.env[service.tokenEnv]`（**onebot 的实际名字是 `ONEBOT_AUTOMATION_OPERATOR_TOKEN`**；插件默认值 `ONEBOT_OPERATOR_TOKEN` 只是占位，请在配置里改成前者）
2. DSH 凭据服务：`ctx.credentials.resolve(credentialRef || tokenEnv)`
3. 都没有 → 空 token（照样能跑，见下）

```bash
export ONEBOT_AUTOMATION_OPERATOR_TOKEN='…'   # 起 DSH 之前；onebot 进程也要有这个同名变量
dsh --profile onebot web --port 3099
```

- 配置里如果写了 `operatorToken` / `service.token` / `password` 之类的键：**被忽略 + 告警**，保存时也不会写回去。
- 日志里只出现「有没有 + 来源」，不出现值；出向请求只把这个值放进 `Authorization: Bearer` 头。
- **鉴权现状**：v3 的出向端点 `/api/send/*` 与 `/mcp`、`/api/stickers` 同族，走 `automationOperatorAuth`
  （仅 loopback + Bearer），**所以出向现在真的需要 token**（和 v2 的 `/send_private_msg` 不同 ——
  那个只校验本机来源）。token 读不到时出向会 401，日志里能看到 `reason`。

---

## 4. 出向协议（v3：插件决定「发什么」，Go 只执行）

CONTRACT §7.2/§7.3：Go 侧不再有「AI 回复」这个概念，只接受薄指令；**发几条、怎么拆、要不要配表情由插件算**。

### 4.1 现在用的端点（`/api/send/*`）

```jsonc
// POST /api/send/text      { "target": "private:wxid_a", "text": "…", "at": ["wxid_b"]? }
// POST /api/send/sticker   { "target": "private:wxid_a", "name": "无语" }
// POST /api/send/image     { "target": "private:wxid_a", "image_base64": "…" } | { "path": "/abs/x.png" }
// 响应：{ "ok": true, "task_id": "…", "queued_at": 1720000000000 }
// 失败：{ "ok": false, "reason": "…" } —— 非 200 **或** 200+ok:false 都按失败处理
```

- 鉴权：`automationOperatorAuth`（loopback + Bearer），有 token 就带 `Authorization: Bearer …`。
- **单次调用只发一条**；拆条/节奏/表情都在插件侧（见 §4b）。
- `target` 的语义只有一个翻译点：`outbound.js` 的 `apiTargetOf(chatId)`，目前原样用
  `private:<wxid>` / `group:<gid>`（另带 `target_kind`/`target_id` 当提示）。T8a 若改语义，**只改那一个函数**。
- `ok:false` → 记 failed + reason；表情失败**静默跳过**，不影响文本。

### 4.2 拆条 / 节奏 / 表情（都在插件侧，全部可配可关）

| 规则 | 默认 | 行为 |
|---|---|---|
| 拆条 | `split: true` / `maxChars: 300` | 先按**空行分段**（一段一条）；段落超上限才按**句末标点**（`。！？!?…；;` 和换行）拆并拼回；单句还超就硬切。**整段是代码块 / 整段是一条链接 → 不拆**；含代码块的段落整段保留 |
| 条间隔 | `intervalMs: 600` | 两条之间等 600ms（最后一条之后不等）；dryRun 下不等 |
| 合并窗口 | `mergeWindowMs: 2000` | 同一会话窗口内连续到达的回复合成**一批**（一次节奏序列），批内每段回复仍是独立消息；`0` = 立刻发 |
| 表情 | `sticker.enabled: true` / `probability: 0.25` | 回复里出现表情库**标签/名字** → 直接配那张（不看概率）；否则以 25% 概率随机一张；只发 1 张，失败静默跳过 |

表情库是 T2 的 `~/.wechat-chatter/stickers/index.json`（只读、按 mtime 缓存、坏了当空库）。
没有索引 = 没有候选 = 表情决策自动失效（只发文字），不会报错。

### 4.3 dryRun 与拆条

`dryRun` 是**逐块**判定的：拆成 5 条就打 5 行 `[dry-run]` 日志、一张表情再打 1 行，**一个 HTTP 请求都不发**
（回归测试 `test/e2e.test.js` 里断言了 `bot.requests.length === 0`）。

### 4.4 迁移：旧的 `/send_*` 已 deprecated

| 旧（v2，deprecated） | 新（v3） |
|---|---|
| `POST /send_private_msg` `{user_id, message:[{type:'text',data:{text}}]}` | `POST /api/send/text` `{target:"private:<wxid>", text}` |
| `POST /send_group_msg` `{group_id, …}` | `POST /api/send/text` `{target:"group:<gid>", text}` |
| （没有表情出口） | `POST /api/send/sticker` `{target, name}` |
| （没有图片出口） | `POST /api/send/image` `{target, image_base64|path}` |

- 旧的 `createSender()` / `buildSendRequest()` 还留着，但标了 `@deprecated`；只有
  `config.outbound.legacyEndpoint: true` 才会走它们（回退用，且拿不到表情/图片）。
- 迁移只需三步：确认 onebot 是带 `/api/send/*` 的版本 → 去掉 `legacyEndpoint`（保持默认 `false`）→ 重启 profile。

---

## 5. MCP 条目（必挂）

工具由 **T1 的 MCP 服务端**提供。**名字不是 `mcp__onebot__<operationId>`** —— 真实名字带一串
12 位哈希后缀，而且**不可手写**。

onebot 的 operation id **全部含 `.`**（`automation_tool_catalog.go`，98 个 id，如 `catalog.list`），
而 DSH 的工具名只允许 `[A-Za-z0-9_-]`，所以 `@deepseek-ai/dsh-mcp-client` 的 `publicToolName()`
（`lib/index.js:96`）**一定**走「字符替换 → 归一化名不再是原名 → 追加 12 位 sha256」这条分支：

```js
joined     = `mcp__${serverName}__${rawName}`          // mcp__onebot__catalog.list
normalized = joined.replace(/[^A-Za-z0-9_-]/g, '_')     // mcp__onebot__catalog_list
hash       = sha256(`${serverName}\0${rawName}`).hex.slice(0, 12)
public     = `${normalized.slice(0, 51)}_${hash}`        // 64 - 12 - 1 = 51
```

T7a 真机实测 + 按源码复算，98/98 全中（三个真实样例）：

| operation id（wire 上的原名） | 模型看到的工具名 |
|---|---|
| `catalog.list` | `mcp__onebot__catalog_list_85cbf33839aa` |
| `automation_runs.list` | `mcp__onebot__automation_runs_list_43686b4d8365` |
| `candidates.create` | `mcp__onebot__candidates_create_d2a04bfe1589` |

> onebot 目前最长的 operation id 是 `service_requests.transition_sensitive`，归一化后 63 字符，
> 还没碰到 51 字符截断那条分支；但规则是「归一化名截到 51 + `_` + 12 位 hex」，**换 `serverName`
> 后端后缀全部跟着变**，所以别在任何地方抄死一个名字。

要拿真实工具名的三种办法（推荐第一种）：

1. **让模型自己看**：问它「列出 `mcp__onebot__` 开头的工具」——它看到的就是注册好的名字。
2. **现查 `tools/list`**（拿到 wire 上的原名，后缀按上面的公式算）：
   ```bash
   curl -s -X POST http://127.0.0.1:58080/mcp \
     -H "content-type: application/json" \
     -H "accept: application/json" \
     -H "authorization: Bearer $ONEBOT_AUTOMATION_OPERATOR_TOKEN" \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
   ```
3. **现算**（把 operation id 丢进去）：
   ```bash
   node -e 'const{createHash}=require("node:crypto");const[s,r]=[process.argv[1],process.argv[2]];
   const j=`mcp__${s}__${r}`,n=j.replace(/[^A-Za-z0-9_-]/g,"_");const h=createHash("sha256").update(`${s}\0${r}`).digest("hex").slice(0,12);
   console.log(`${n.slice(0,51)}_${h}`)' onebot catalog.list
   # → mcp__onebot__catalog_list_85cbf33839aa
   ```

往**新 profile** 的 `~/.dsh/profiles/onebot/cordis.patch.yml` 里加（或你在用的那个 profile 的 patch）：

```yaml
# onebot MCP 服务端：工具名形如 mcp__onebot__catalog_list_85cbf33839aa
# （归一化 + 12 位哈希后缀，别手写；见上面 §5 开头）
# serverName 只允许 [A-Za-z0-9_-]{1,32}，这里用 onebot
- insert:
    - id: mcp-onebot
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: onebot
        transport: streamable-http
        url: http://127.0.0.1:58080/mcp
        headers:
          # 唯一允许的 !!js：只从 process.env 取密钥，不做别的表达式
          # 变量名与 onebot 侧一致：ONEBOT_AUTOMATION_OPERATOR_TOKEN
          Authorization: !!js '`Bearer ${process.env.ONEBOT_AUTOMATION_OPERATOR_TOKEN ?? ""}`'
        toolCallTimeoutMs: 60000
        failOnStartupError: false
```

要点（对着 T1 已落的代码核过一遍）：

- 端点 `POST /mcp`（`mcp_server.go:23 mcpEndpointPath`），挂法 `registerMCPRoutes`（`mcp_server.go:106`）：
  `ctx.automationOperatorAuth(...)` = **仅 loopback + `Authorization: Bearer <operatorToken>`**，和上面 headers 对得上。
- 远端 `initialize.serverInfo.name` 就是 `"onebot"`（`mcp_server.go:25 mcpServerName`）。DSH 的 `serverName` 是**本地命名空间**
  （不读远端名字），这里同样取 `onebot` → 工具名 `mcp__onebot__*`。
- 只在 `-mode hr-workbench`（有 automation runtime）时才会挂上；那个模式下才有 `/mcp`。
- **Bearer 的值来自环境变量 `ONEBOT_AUTOMATION_OPERATOR_TOKEN`**（`automation_runtime.go:56`）。
  注意三件事：
  1. `hr-workbench` 模式下 **`-token` 是非法参数**（`process_mode.go:68` 的 forbidden 列表），
     启动时给 `-token` 会直接报错 —— token 只能走环境变量。
  2. 那个模式启动时 `ONEBOT_AUTOMATION_OPERATOR_TOKEN` 和 `ONEBOT_AUTOMATION_AGENT_TOKEN`
     **两个都必须有**（`automation_runtime.go:56-58`，缺一个 automation runtime 建不起来 → `/mcp` 不存在）。
  3. DSH 这一侧要读到**同一个值**：插件配置里 `service.tokenEnv` 写成
     `ONEBOT_AUTOMATION_OPERATOR_TOKEN`（见 §3），profile patch 里的 `!!js` 也读这个名字。
- `failOnStartupError: false`：onebot 没起的时候 DSH 照常启动，只是没有这些工具（日志里有一行报错）。
- **这里只允许用 `!!js` 读密钥**，别在 profile 里写别的 `!!js` 表达式（这是 T5 的硬约束）。
- 具体有哪些 `mcp__onebot__*` 由 T1 的 operation 目录决定（`WorkbenchOperationDefinition` 同源），
  插件不重复声明；想知道清单就把真实工具列表打出来看（§7）。

---

## 6. profile patch：插件本体 + MCP 一起

正常安装（`dsh plugin add`）**不用手写这段**：本包的 `cordis.patch.yml` 已经被当成一层 patch 并进去，
里面除了插件本体还有两个 agent preset（见 §12.2）。下面只是「没自动并进来时」的兜底：

`~/.dsh/profiles/onebot/cordis.patch.yml`：

```yaml
# 1) 插件本体 + 两个 preset —— 直接把本包 cordis.patch.yml 的内容整段粘过来
#    （内含 onebot-bridge / preset-onebot-owner / preset-onebot-social 三条 insert）
- insert:
    - id: onebot-bridge
      name: onebot-bridge
    # 两个 preset 见 <本目录>/cordis.patch.yml；想按主人/陌生人分档就必须一起挂上，
    # 否则会话会退回 profile 默认 agent（插件会自动降级，但日志里会有告警）。

# 2) MCP 服务端（见 §5）
- insert:
    - id: mcp-onebot
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: onebot
        transport: streamable-http
        url: http://127.0.0.1:58080/mcp
        headers:
          Authorization: !!js '`Bearer ${process.env.ONEBOT_AUTOMATION_OPERATOR_TOKEN ?? ""}`'
        toolCallTimeoutMs: 60000
        failOnStartupError: false
```

`name: onebot-bridge` 由 profile 的 `node_modules` 解析，所以要先 `dsh plugin --profile onebot add <本目录>`。
挂 preset 时注意：**插件本体和 preset 要同进同退** —— preset 里写的 `{{onebot_persona}}` 由插件注册，
只剩 preset 没有插件，那个变量就没人定义（人格模板会渲染失败）。

---

## 7. dry-run 验证（先做这一步，别急着真发）

1. **确认 onebot 在跑**（`-mode hr-workbench`，默认 `127.0.0.1:58080`）：
   ```bash
   curl -s http://127.0.0.1:58080/api/contacts | head -c 200
   ```
2. 写配置：`enabled: true`、**`dryRun: true`**（或干脆不写 dryRun，缺省就是 true）。
3. 起 profile：`dsh --profile onebot web --port 3099`，看日志里这几行：
   ```
   [onebot-bridge] 配置：…｜总开关开｜dryRun 开（只记日志，不真发）
   [onebot-bridge] onebot：http://127.0.0.1:58080（WS ws://127.0.0.1:58080/ws）｜会话转发 开
   [onebot-bridge] operatorToken：已读取（来源 env:ONEBOT_AUTOMATION_OPERATOR_TOKEN，不打印值）
   [onebot-bridge] 已连上 onebot 事件流：ws://127.0.0.1:58080/ws
   ```
4. 用手机另一个号给自己发一条「在吗」（或让别人在群里说一句），日志应出现：
   ```
   [onebot-bridge] ← private:wxid_xxx 张三：在吗
   [onebot-bridge] 为 private:wxid_xxx 新建了 DSH 会话 xxxxxxxx（preset onebot-social）
   [onebot-bridge] private:wxid_xxx：交给她 1 条（会话 xxxxxxxx）
   [onebot-bridge] [dry-run] 不真发（reply，第 1/2 条）→ private:wxid_xxx：第一段…
   [onebot-bridge] [dry-run] 不真发（reply，第 2/2 条）→ private:wxid_xxx：第二段…
   [onebot-bridge] [dry-run] 不真发（表情）→ private:wxid_xxx：无语
   ```
   **`第 i/N 条` 一行一行出现 = 拆条在工作**；最后一行出现 = 整条链路通了，只是没真发。
   一行都没有 = 链路没通（看上面有没有 `事件流已连接`）。
5. 想看结构化状态（不含密钥）：
   ```bash
   curl -s http://127.0.0.1:3099/onebot-bridge/api/state | python3 -m json.tool
   ```
   重点看 `enabled` / `dryRun` / `ws.state` / `token.present` /
   `session.outbound`（`endpoint` 是 `api(/api/send/*)`、`pieces`/`dryRun`/`stickers` 在涨就对了）。
6. 确认工具挂上了：在 DSH 那个会话里问「你有哪些 mcp__onebot__ 开头的工具」（名字带 12 位哈希后缀，
   见 §5），或看启动日志里 MCP 同步那一行。

**真发**：把 `config.json` 的 `"dryRun"` 显式改成 `false`（30 秒内自动生效，日志会有一条
`⚠ dryRun 被关掉了：之后真的会往微信发消息！`）。**先只对自己的一个号试。**

---

## 8. 待验证清单 (TODO)

下面每一条都是「代码已按 A 写了，但只在桩上验过」，真宿主里请确认：

1. **`ctx.sessionController.create({ cwd })` 是否接受只给 `cwd`**（不给 `workspaceId`）。
   类型声明里两者互斥、都可选（`SessionCreateRequest`），但没有实跑过。若报错 → 改成给 `workspaceId`
   （需要 `ctx.workspaceRegistry` 建工作区，au_rabot 的做法）。
2. **`session/event` 的 `session.id` 前缀**：代码按 `session-` 前缀剥不剥都能对上（`sessionKey()`），
   待确认事件里给的 id 与 `create()` 返回的 `sessionId` 完全一致。
3. **`agent.followup({ id, role: 'user', content, source })`**：字段名照 au_rabot 的写法
   （DSH 内部另有 `createUserMessage()` 工厂，见 `dsh-schedule`）。若宿主校验更严，改成用官方工厂。
4. **`turn/end` 的 `reason.kind`**：只有 `completed` 才自动发回；`aborted`/`error`/`blocked` 不发（已实现）。
5. **preset 是否真的按 `config.id` 命中**：0.2 起会按主人/社交给 `create({ agentPreset })` 传
   `onebot-owner` / `onebot-social`（定义在本包 `cordis.patch.yml`）。没在真 DSH 里跑过——
   第一次起来请确认日志里有 `新建了 DSH 会话 xxx（preset onebot-social）`，且 `state().presets.broken` 是空的。
   如果 preset 报「unknown agent preset」，说明 profile 只装了包、没并那层 patch（见 §6），
   插件会自动退回默认 agent（会告警一次）。
6. **`wechat_reply` 与「不自动重发」的配合**：现在只要这一轮调过 `wechat_reply` 就不再自动发最终正文；
   如果模型的最终正文和工具发的内容不一样，会丢掉最终正文。要不要改成「只对完全相同的去重」，待定。
7. **群聊策略**：没有「@我才回 / 插话概率 / 复读保护」。现在群里**每条**消息都会进会话（会话内部靠
   `busy` 排队合并）。要在群里用，先想清楚是不是真的要每条都回。
8. **图片/语音/表情**：入向只给占位文本（`[图片]`/`[语音]`/`[表情]`），不下载、不进附件；
   出向只发文本。表情出向留给 T4（`POST /api/stickers/send`），**本插件没接**。
9. **白名单**：`owner.wxids` / `owner.groups` 只决定**用哪个 preset**（工具档位），**不决定回不回**。
   `enabled: true` = 谁发都进会话。要「只跟特定人聊」，得自己加一层入口过滤（au_rabot 有 `allow` 白名单）。
10. **onebot 只有一个全局 `conn`**：别的 WS 客户端连上会把它顶掉；本插件被顶掉后会自己重连（2s→30s 退避），
    但它们会互相顶。**别同时挂两个**。
11. **`post_type` 不保证存在**：`WechatMessage` 只有 `protobuf_handler.go:233` 那条路径填了 `post_type`，
    Frida `send` 路径（`frida_script_loader.go` → `SendWebSocketMsg`）不填。归一化已按「缺失就按消息处理」写。
12. **`automationLoopbackRequest`**：`/send_*` 只认本机。插件假装在远端跑会 403。
12b. **`/api/send/*`（T8a）还没上线时**：出向会 404/401，日志里有 `reason`（不会被吞）。
    等 T8a 落地后第一件事是核对 **`target` 语义**（我按 `private:<wxid>` / `group:<gid>` 传，
    只影响 `outbound.js` 的 `apiTargetOf()` 一个函数）；对不上就改那一个函数，别的地方不动。
13. **`{{onebot_persona}}` 的注册时机**：变量在 `apply()` 里注册（不依赖 `enabled`），preset 渲染时才解析。
    但**插件本体和 preset 必须同进同退**：只挂 preset 不挂插件，模板里的变量没人定义 → 那一轮 system 渲染会失败。
13b. **表情依赖 T2 的表情库**：`~/.wechat-chatter/stickers/index.json` 不存在时表情决策自动失效（只发文字，不报错）。
    索引里必须有非空 `name`（`/api/send/sticker` 按名字匹配）；`tags` 用来做「标签命中」。
14. **`onebot-social` 用了 `complete: true`**：人格独占系统提示词（不带 DSH 的编程助手身份和工具指引）。
    这是照 au_rabot 的实测（私聊一轮快很多）；如果发现模型行为「太不助手」，把那条 `complete: true` 删掉即可。

---

## 9. 关掉

按需要选一种（从轻到重）：

1. **只停收发**：`config.json` 里 `"enabled": false` → 30 秒内事件流断开、消息不再处理（进程不重启）。
2. **只停真发**：`"dryRun": true` → 照常收，只记日志。
3. **只停会话转发**：`"session.enabled": false` → 只归一化 + 记日志，不建会话、不打扰模型。
3b. **只停某一环（推荐，改完 30 秒内生效且不留残留）**：`modules` 里把那一环设成 `false` ——
   `{"modules": {"outbound": false}}` 就是「照常收、照常让模型想，但一个字节都不发到微信」；
   `{"modules": {"sessions": false}}` 是「只收不投」；`{"modules": {"connection": false}}` 是「断开事件流」。
   停掉的模块会把自己的监听器/连接/定时器一起撤掉（不是挂着不干活）。
4. **整块摘掉**：从 profile 的 `cordis.patch.yml` 删掉 `onebot-bridge` 那条（§6 的第 1 段），重启 profile。
5. **连工具一起摘**：再把 `mcp-onebot` 那条删掉（MCP 服务端本身在 onebot 进程里，不受影响）。
6. **卸载包**：`dsh plugin --profile onebot remove onebot-bridge`。

撤掉插件后，`~/.dsh/plugins-data/onebot-bridge/`（`config.json`、`sessions.json`、`chats/`）
留着不会影响任何东西，想清干净就自己 `rm -rf`（**注意里面 `sessions.json` 记着 chat→DSH 会话的映射**）。

---

## 10. 代码结构

```
onebot-bridge/
  package.json            # dsh.id/name/bundle.patch → cordis.patch.yml（自带挂载声明）
  cordis.patch.yml        # 插件本体 + 两个 agent preset（onebot-owner / onebot-social）
  src/index.js            # 插件入口 apply(ctx)：配置、模块装配、人格变量、状态接口、看门狗、收尾
  src/modules.js          # 迷你模块宿主：注册/启停/热重载/出错隔离/作用域回收
  src/modules/connection.js # 模块①：onebot 事件流（WS 订阅 + 重连）
  src/modules/inbound.js    # 模块②：入向归一化
  src/modules/sessions.js   # 模块③：chat ↔ 持久 DSH 会话（+ session/event 监听）
  src/modules/outbound.js   # 模块④：出向发送（dryRun 闸门 + HTTP）
  src/persona.js          # 人格：persona.file/text → 提示词变量 {{onebot_persona}}
  src/reply.js            # 纯规则：拆条（段落/句号/代码块/链接）+ 表情挑选（标签命中/概率）
  src/stickers.js         # 只读表情库索引（T2 的 index.json）→ 挑一张
  src/outbound.js         # 传输层（/api/send/*）+ 合并窗口 + 条间隔 + 唯一的 dryRun 闸门
  src/config.js           # 默认值 + 归一化 + dryRun/enabled 红线 + token 读取 + owner 判定
  src/log.js              # 脱敏日志（token 只以「有没有」出现）
  src/client.js           # WS 客户端（模块①用）：订阅 + 断线重连（2s→30s 退避）
  src/inbound.js          # 纯函数：onebot 帧 → { chatId, chatKind, sender, senderName, text, msgType }
  src/hub.js              # 会话中枢：chat ↔ DSH 会话、preset 选择、运行时上下文、一轮结束发回
  src/tools.js            # wechat_reply（只挂在微信会话的 agent 作用域里）
  test/                   # node --test；test/fake-onebot.js 是手写真 WS 服务端
```

几条设计上的取舍：

- **WS 只订阅、不发 action**：onebot 的 `/ws` 本来是命令通道（`{action,echo,params}`），
  但出向走 HTTP 更好排查（curl 就能测），所以 WS 只读。
- **每个 chat 一个持久会话**：`sessions.json` 记 `chatId → sessionId`，进程重启后 `resolveAgent` 复活；
  复活不了就换新会话（不会把消息卡死）。
- **一轮没正常结束就不发**：避免把半句话或者报错前的残文发到微信。
- **同一 chat 串行**：一轮没结束期间来的消息排队合并成一段再投，不会一人一轮。
- **所有发送只过一个函数**（`hub.sendText` → `outbound.send`）：`enabled`/`dryRun` 的判定只有一处，
  而且**拆条之后每一块**都要再过一次（不存在「整段过闸、拆开乱发」）。
- **出向决策全在 JS**：拆几条、间隔多久、配不配表情是插件算的；Go 只收「把这段发给这个会话」。
  这也是 v3（CONTRACT §7.3）与 au_rabot 对齐的关键一环。

---

## 11. 测试

```bash
cd dsh-plugin/onebot-bridge
node --test "test/*.test.js"      # Node 22+，不需要真微信/真 onebot
```

> 测试里默认把 `sticker.enabled` 关掉、`mergeWindowMs` 设 0：本机如果已经有真的表情库
> （`~/.wechat-chatter/stickers/index.json`，T2 写的），不关掉会让断言随机多出一个
> `/api/send/sticker` 请求。手动 dry-run 时同理 —— 日志里会看到随机配表情，那是**正常行为**。

110 个用例，覆盖：入向归一化、真 WS 收发与重连、出向 HTTP 形状与失败降级、dryRun 红线（含拆条后逐块拦截）、
拆条/合并窗口/条间隔/表情决策、`/api/send/*` 新出口与 `/send_*` 老出口、配置与 token 来源、
会话投递/回收/去重、preset 选择与降级、人格变量、模块启停与作用域回收、
全链路（假 onebot ⇄ 插件本体 ⇄ DSH 桩）。

---

## 12. 与 au_rabot 的技术栈对齐

对照源：`/private/tmp/dsh-aurabot-qq/`（同一套 DSH 插件 API，真实在跑的插件）。目标不是「做成 au_rabot 那么大」，
而是**把技术选型和骨架对齐**，功能面保持窄。

### 12.1 对齐前的差距（0.1 → 0.2）

| 维度 | au_rabot | onebot-bridge 0.1（对齐前） | 差在哪 |
|---|---|---|---|
| 插件声明 | `package.json` 里 `dsh.bundle.patch` + `exports`；`cordis.patch.yml` **同时**声明插件本体和两个 agent preset | 只有 `dsh.bundle.patch` + 一行 insert，没有 preset | 会话一律跑 profile 默认 agent（工具全套），没法按「主人/陌生人」分档 |
| 人格 | `{{aurabot_persona}}` 提示词变量 + `@deepseek-ai/dsh-persona`，面板可切换 | 无（只能在 systemPrivate/systemGroup 里写死一句话） | 没有身份，模型默认是「编程助手」 |
| 每轮变化的信息 | `agent.ctx.systemPrompt.context()` 按 order 追加 | 只用 context 追加了一句固定话 | 模型不知道「现在在跟谁说话」 |
| 能力组织 | `src/core/modules.js`：每个能力是模块，开关/替换/热重载/出错隔离 | 全写死在 `index.js` 里 | 想「只听不发」「只收不投」得改代码 |
| 生命周期 | 一切注册进 `ctx.effect`、看门狗 `ctx.interval`、收尾统一 dispose | 部分进 effect，收尾靠单个 effect | HMR 重载后可能留野 socket |
| 面板（网页半边） | `client/client.js` + `/aurabot/api/*` | 无 | 见 12.3「刻意不做」 |

### 12.2 落地的三项

**① 插件形态（对齐）**

- `package.json`：`dsh.id` / `dsh.name` / `dsh.bundle.patch` 齐备，`main`/`exports`/`files` 与 au_rabot 同构，`node --test` 脚本。
- `cordis.patch.yml`：一个 `insert` 列表里依次是插件本体 + `preset-onebot-owner` + `preset-onebot-social`，
  写法（`name: '@deepseek-ai/dsh-agent-preset'` + `config.id/name/description/order/plugins`）与 au_rabot 一致。
- `src/index.js` 生命周期与 `aurabot/src/host.js` 同构：`export const name` + `export const inject`（`timer`）+
  `apply(ctx)`；所有注册走 `ctx.effect(...)`；看门狗 `ctx.effect(() => ctx.interval(...))`；收尾统一 `dispose()`。

**② 会话与人格（对齐）**

- preset：`onebot-owner`（`persona` + `time-context` + `tool-fs`/`tool-fs-search`/`tool-web` + compaction）、
  `onebot-social`（`persona(complete: true)` + `time-context` + compaction，**没有任何本地工具**）。
- 人格：`src/persona.js` 注册提示词变量 `{{onebot_persona}}`（`persona.file` > `persona.text` > 内置默认），
  每轮渲染现读 → 改配置/改文件，下一条消息就生效，**不用重建会话**，也不打穿前缀缓存。
- 每轮变化的信息：`hub.js` 的 `agent.ctx.systemPrompt.context({name:'onebot-bridge-chat', order:900})`
  里拼「当前对象：微信私聊 · 张三(wxid_a)」「今天聊过：…（N 条，最近 HH:MM）」，**不拼进 system 开头**。
- preset 选择：`owner.wxids` / `owner.groups` 命中 → `onebot-owner`，其余（含所有群聊）→ `onebot-social`；
  preset 变了会给那个 chat 换新会话；preset 没注册（profile 没挂这层 patch）→ 退回默认 agent 并只告警一次。
- 升级提醒：`sessions.json` 里老记录没有 `preset` 字段，0.2 起来后**每个 chat 的第一条消息会重建会话**
  （人格/preset 才算真正生效）。想让旧会话继续用，手动补 `"preset": ""` 到那条记录里。

**③ 模块骨架（对齐到够用）**

四段流水线，每段一个模块（`src/modules/*.js`），由 `src/modules.js` 托管：

```
connection ──frame──> inbound ──message──> sessions ──reply──> outbound
（WS 订阅）           （归一化）           （chat ↔ 会话）        （dryRun 闸门 + HTTP）
```

- 开关：`config.modules.connection/inbound/sessions/outbound`（只有显式 `false` 才关）。
- 作用域回收：模块注册的一切（总线监听器、`ctx.on('session/event')`、定时器、连接）都进它自己的 `scope`，
  `stop()` 时倒序撤销 —— 关掉 `outbound` 是真的一点都不发，关掉 `connection` 是真的断开，而不是「挂着不干活」。
- 出错隔离：某个模块 factory/start 抛错只记在它自己头上（`state().modules[].error`），其它模块照跑。
- 热重载：连接参数变了只 `restart('connection')`；`dispose()` 全停（测试里断言了 `dispose` 之后 WS 真断开）。

### 12.3 对照表：au_rabot 有什么 / 我们有什么 / 刻意不做

| 能力 | au_rabot | onebot-bridge 0.2 | 说明 |
|---|---|---|---|
| 传输 | SnowLuma（QQ，OneBot v11）+ 进程管理 | onebot（微信）：WS 只读订阅 + HTTP 发送 | **刻意**：只管微信，不做多协议 |
| 插件形态 | bundle patch + preset + 网页半边 | bundle patch + preset（**无网页半边**） | 刻意不做面板 |
| 人格 | 多份人格文件、面板切换 | 单份人格（`persona.text` / `persona.file`） | 够用；要切换就改配置 |
| preset 分档 | `aurabot-private` / `aurabot-social` | `onebot-owner` / `onebot-social` | 对齐 |
| 工具 | 每个模块注册一批（表情、语音、空间、B 站…） | **只加 `wechat_reply`**，其余走 T1 的 `mcp__onebot__*` | 刻意：工具归宿主/MCP，插件不当第二套工具层 |
| 会话中枢 | 合并连发、群聊策略、@判定、命令、暂停、心情影响 | 合并窗口 + 拆条 + 条间隔 + 串行 + `busy` 超时保护 | 刻意不做群聊策略/命令 |
| 出向决策（发几条/怎么拆/隔多久） | 插件侧（`chat/hub.js` + `chat/reply.js`） | **插件侧**（`outbound.js` + `reply.js`，v3） | 对齐（CONTRACT §7.3） |
| 每轮上下文 | 记忆检索、群上下文、活跃度 | 当前对象 + 今天聊过谁 | 没有记忆系统 |
| 图片/语音/表情 | 收图、转写、TTS、表情库 | 只有占位文本（`[图片]`/`[语音]`） | 表情发送留给 T4（`/api/stickers/send`，没接） |
| 模块系统 | 从磁盘加载模块、可写、可替换、面板热重载 | 静态注册 4 个内置模块 + 配置开关 | 刻意：不做「用户可写模块」 |
| 面板/API | `/aurabot/api/*` 一大套 | 只一个只读 `/onebot-bridge/api/state` | 排查用，够 |
| 本地接口/桥接 | `bridge`（HTTP 开放接口 + DSH 工具联动） | 无 | 刻意不做 |
| 出向安全 | 总开关 + dryRun + 白名单 + 群管命令 | 总开关 + dryRun + 模块开关 + owner 分档 | 红线保持 |
| 测试 | `node --test` | `node --test`（75 个） | 对齐 |

### 12.4 刻意没做，以及为什么

1. **网页面板**（`client/`）：本插件是「把微信接进 DSH 会话」，看会话就看 DSH 自己的 UI；
   面板只会多一份要维护的前端和一套 `/api/*`。排查用 `/onebot-bridge/api/state` 足够。
2. **磁盘可加载模块**（au_rabot 的 `src/modules/*` + 面板热重载）：那套系统的价值在「用户能写模块」，
   代价是契约、沙箱、版本兼容。这里只把四个内置能力模块化，开关走配置 —— 结构对了，功能没膨胀。
3. **群聊策略/命令/记忆/语音/表情**：这些是 au_rabot 的主场，搬过来会让这个插件变成第二个 au_rabot；
   需要时按模块加（骨架已经留好：加一个 `modules/xxx.js` + 一个配置开关）。
4. **shell 工具**（owner preset）：DSH 的 base 按平台只挂一套 shell（macOS/Linux = bash，Windows = pwsh），
   在 preset 里再写一套跨平台的会把 profile 加载搞挂；owner 档给 `fs`/`fs-search`/`web`，要 shell 自己往 preset 里加。
5. **`agentPreset` 之外的收窄**：`onebot-social` 用 `complete: true` 让模型只看到人格（不带 DSH 的编程助手身份），
   工具由 preset 决定；**没有**做按人的工具白名单（工具已经收敛到一个工具）。

> 注意：v3 的「出向决策权上收」是**做了**的（不是刻意不做），见下一节。

### 12.5 v3：出向决策权上收（T8b）

| | v2（对齐前） | v3（现在） |
|---|---|---|
| 发什么、发几条、怎么拆 | 插件把**整段**回复丢给 onebot，发几条是 Go 的事 | 插件侧：合并窗口 → 拆条 → 逐条 → 条间隔 → 表情 |
| 出口 | `/send_private_msg` / `/send_group_msg`（OneBot 语义） | `/api/send/text|sticker|image`（薄指令，`{ok,task_id,queued_at}`） |
| 表情 | 没接 | 标签命中优先 + 概率随机（默认 0.25），失败静默跳过 |
| dryRun | 整段过闸 | **逐块**过闸（拆 5 条打 5 行日志，零请求） |

这就是 au_rabot「循环好」的另一半：**它自己决定发几条、隔多久、配不配表情**。
代价也照抄了：拆条/节奏都在插件侧，所以「发得慢」这类问题要看 `outbound.intervalMs`/`mergeWindowMs`。
