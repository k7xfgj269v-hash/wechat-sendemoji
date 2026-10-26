# WeChat sendemoji — 微信表情消息的逆向实现 + DSH/OneBot 桥

把 **微信 4.1.11 的自定义表情发送（`sendemoji`）** 完整逆出来并落地成可运行的 Go 服务 + DSH 插件：
真机验证过 —— 表情能发出去，接收端显示为**表情**（不是图片）。

> 架构与工具链**借鉴** [accomplish07zrh-eng/dsh-aurabot-qq](https://github.com/accomplish07zrh-eng/dsh-aurabot-qq)：
> 它把「模型与工具循环」交给 DSH 宿主、插件只声明工具与人格的做法非常干净。
> 本项目沿用这个思路（见 [§架构](#架构)），但**目标是微信而不是 QQ**，所以多了一层协议逆向。

---

## 这是什么

三块东西，一次解决一个具体问题：

| 目录 | 是什么 | 关键点 |
|---|---|---|
| [`docs/re/`](docs/re/) | 微信表情发送链路的逆向规格 | `sendemoji` cgi、请求体 211 字节字段级解出、收藏表情 255 张的来源 |
| [`onebot/`](onebot/) | Go 服务（Frida 注入 + MCP 服务端 + 表情库 + 发送 API） | 91 个 MCP 工具、真发 `sendemoji`、表情库去重/匹配/面板 |
| [`dsh-plugin/onebot-bridge/`](dsh-plugin/onebot-bridge/) | DSH 插件（微信 ⇄ DSH 会话） | 拆条、发送节奏、表情决策、人格 preset、模块化 |

---

## 核心成果：`sendemoji` 请求体

微信里「发表情」走的是**独立 cgi**，不是普通消息那条路：

```
POST /cgi-bin/micromsg-bin/sendemoji     reqid=68  nettype=3（长连接）
```

抓到的真实请求体（211 字节，用户从表情面板发一张给文件传输助手）：

```
f1  header 64B  { sessionId, 16 字符 clientProof, deviceId, "UnifiedPCMac 26 arm64", 计数 }
f2  1
f3  表情体 140B {
      1: md5 "0302e5974743d8a463f64edfacda7fa2"
      2: 0   3: 18585   4: {0, ""}   5: 2
      6: 接收方 "filehelper"
      7: "<gameext type=\"0\" content=\"0\" ></gameext>"
      9: clientMsgId "filehelper_1793012901_82_xwechat_33"
    }
```

**三个反直觉的事实**（都是踩过坑才确认的）：

1. **请求体里没有 cdnurl / aeskey / 宽高 / 任何上传凭据** —— 服务端**按 md5 反查**表情。
   推论：只能发**服务端已认识**的表情。从网上下载的图发出去，服务端收下但不显示。
2. **抓包落点是 `wechat.dylib + 0x3e58f14`**，`sp+0x140` 指向 holder（`+0x00`=数据指针，`+0x0c`=长度）。
   此前判定的 `Req2Buf 0x54f14f8` 在 live 路径 **0 次调用**，`autoBufferWrite 0x3E7FA8C` 对 `sendemoji` 也无效。
3. **收藏表情不用解密 DB**：微信把表情原文件按 **md5 命名**缓存着：
   `business/emoticon/Persist/<前2位>/<32位hex>` —— 目录名就是发送需要的 md5。

完整地址/字段/排除过程见 [`docs/re/wechat-411-emoji.md`](docs/re/wechat-411-emoji.md)（含所有"未命中"的负结果）。

---

## 架构

```
微信进程 (WeChat 4.1.11)
   │  Frida Gadget 127.0.0.1:27042   ← script.js 注入，mars 层构造 + 发送
   ▼
wechat-agent 进程（Go）              ← Frida 控制、worker 队列、sendemoji 组包
   │  Unix socket（受控发送协议：text / attachment / emoji）
   ▼
hr-workbench 进程（Go）              ← HTTP + MCP，无 Frida
   │  POST /mcp   （MCP Streamable HTTP，91 个工具）
   │  POST /api/send/{text,image,sticker,emoji}
   │  GET  /api/stickers
   ▼
DSH（DeepSeek Harness）              ← 模型 + 工具循环 + 会话 + 人格
   ▲
   └── dsh-plugin/onebot-bridge      ← 微信事件 ⇄ DSH 会话；拆条/节奏/表情决策
```

**借鉴 au_rabot 的四个点**（也是它循环做得好的原因，不是"因为它用 JS"）：

1. **循环交给宿主** —— 插件从不自己拼 provider 请求，工具用 JSON Schema 声明一次，由 DSH 调度。
2. **人格是提示词变量**（`{{onebot_persona}}`）—— 改人格不重建会话。
3. **每轮变化的内容走 `systemPrompt.context()`** —— 不拼 system 开头，前缀缓存不破。
4. **能力模块化** —— 连接/入向/会话/出向各自可开关，停用时注册的东西一起撤。

Go 侧只做"手脚"（协议、组包、Frida、队列），**不再替上层决定发什么**。

---

## 快速开始

### 1. 起 wechat-agent（需要 Frida，注入微信）

```bash
# 需要 frida-core devkit 头文件与静态库（本项目不随仓库分发）
export CGO_CFLAGS="-I/path/to/frida-core-devkit -w"
export CGO_LDFLAGS="-L/path/to/frida-core-devkit -lfrida-core -lbsm -framework IOKit \
  -framework Foundation -framework AppKit -framework Security -lpthread -ldl -lm"
go build -o onebot-frida .

./onebot-frida -mode wechat-agent \
  -type gadget -gadget_addr 127.0.0.1:27042 \
  -wechat_conf ../wechat_version/4_1_11_53_mac.json \
  -self_wxid <你的 wxid> -run_dir /tmp/onebot-run
```

不加 Frida 时用 `go build -tags noFrida`（该 tag 下 Frida 是空实现，只适合跑测试）。

### 2. 起 hr-workbench

```bash
export ONEBOT_AUTOMATION_OPERATOR_TOKEN=$(openssl rand -hex 24)
export ONEBOT_AUTOMATION_AGENT_TOKEN=$(openssl rand -hex 24)
./onebot-frida -mode hr-workbench -receive_host 127.0.0.1:58080 \
  -run_dir /tmp/onebot-run
```

> `-token` 在 `hr-workbench` 模式下**非法**（`process_mode.go` 的 forbidden 列表）；
> `/mcp` 的 Bearer 来自环境变量 `ONEBOT_AUTOMATION_OPERATOR_TOKEN`。

### 3. 发表情

```bash
T=$ONEBOT_AUTOMATION_OPERATOR_TOKEN

# 按 md5（收藏表情的 md5 可从 business/emoticon/Persist 目录名取）
curl -X POST http://127.0.0.1:58080/api/send/emoji \
  -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"target":"filehelper","md5":"0302e5974743d8a463f64edfacda7fa2"}'

# 按名字（表情库模糊匹配）
curl -X POST http://127.0.0.1:58080/api/send/sticker \
  -H "Authorization: Bearer $T" -H 'Content-Type: application/json' \
  -d '{"target":"filehelper","name":"委屈"}'
```

### 4. 接进 DSH

profile patch 里加 MCP 条目：

```yaml
- id: mcp-onebot
  name: '@deepseek-ai/dsh-mcp-client'
  config:
    serverName: onebot
    transport: streamable-http
    url: http://127.0.0.1:58080/mcp
    headers:
      Authorization: !!js '`Bearer ${process.env.ONEBOT_AUTOMATION_OPERATOR_TOKEN}`'
    toolCallTimeoutMs: 30000
```

> ⚠️ **工具名带哈希后缀**。operation id 含 `.`，DSH 归一化后必然追加 `_sha256(server\0raw)[:12]`：
> `catalog.list` → `mcp__onebot__catalog_list_85cbf33839aa`。**不要手写清单**，从 `tools/list` 现查。

---

## 表情库

`~/.wechat-chatter/stickers/`：`index.json` + 图片文件。

- **md5 去重**、尺寸门（宽高 ≤400、≤1.5 MiB、长短边比 <3）、PNG/GIF/JPEG/WebP 手写头解析（零依赖）
- **收藏表情来源**：`business/emoticon/Persist/<前2位>/<md5>` 的文件名即 md5，可直接批量入库
- 名字/标签模糊匹配（完全等于 10 分、包含 6 分、字符重合按比例）、`Trim` 只淘汰自动收集的、手工件永不淘汰
- 面板：`web/stickers.js`（`AI 中心` 分组），HTTP：`GET /api/stickers` + `rename/delete/upload`

> 收藏表情的原图在磁盘上是**加密**的（缺 `aes_key`），所以面板只能显示"md5 + 序号"占位。
> **发送不受影响**（发送只要 md5）。

---

## 踩过的坑（改代码前先看）

| 坑 | 现象 | 解法 |
|---|---|---|
| 同名模块 | `findModuleByName("wechat.dylib")` 命中 `Contents/Frameworks/wechat.dylib`（16 KB 桩） | 按 path 含 `/Resources/wechat.dylib` + size>50MB 选 |
| `req2buf` 关键字扫描 | 4.1.11 内存里已扫不到，`baseAddr` 永远 null → **一个 hook 都装不上** | 扫描失败回退到主模块基址 |
| `AttachSendFunc` 偏移 | 挂在 `sendFuncAddr+0x10`（老版本偏移），4.1.11 钩不到 → 发送上下文永远为空 | 直接挂 wrapper 入口 `0x5120fd8` |
| `-self_wxid` | agent 刚启动、还没收到入站消息时 `myWechatId` 为空 → `sender id is required` | 回退到 `config.SelfWxid` |
| Frida CLI | 无 TTY 后台会加载完立即退出 | 用 Python API 守会话，别 fork 守护 |
| 二进制搜索 | 全量 auto-analysis 328 MB 不现实 | 定点反编译 / 只读 hook |

---

## 测试

```bash
# Go（Frida 用空实现）
go build -tags noFrida ./... && go test -tags noFrida ./...

# DSH 插件
cd dsh-plugin/onebot-bridge && node --test
```

---

## 边界与已知缺口

### 已解决

- **入向双循环**：入向消息不再同时触发 onebot 自己的 AI 对话循环（那是 DSH 的事）。
  默认关闭，需要时置 `ONEBOT_INBOUND_AUTOMATION=1` 恢复；CRM/审计落库不受影响。
- **插件与真实 profile 联调**：已在独立 profile 里加载并跑通 —— 四个模块启动、WS 连上真实 onebot。
  过程中修掉一个真 bug：`inject` 少了 `systemPrompt`，导致人格变量注册必然失败。
- **收藏表情原图解密算法**（见下）：不需要 fav 表的 `aes_key`，账号级派生 key 就够。

### 收藏表情原图的解密算法（未在本机验证）

来源 [CN-Grace/Wechat-Emoticon-Parser](https://github.com/CN-Grace/Wechat-Emoticon-Parser) `v4.0-plus`（MIT）：

```
AES-128-CBC + PKCS7，key = IV = md5(f"{seed}{wxid}EMOTICON") 的前 16 字节
seed = 微信进程内存里的账号级常量（8~10 位十进制数字串）
验证 = 表情文件前 16 字节解密后首 4 字节命中 89504e47 / GIF8 / ffd8ff / wxgf
```

这澄清了一个此前的误判：fav 表里的 `aes_key` 是**每条表情各自的**密钥；
磁盘缓存这一层用的是**账号级派生 key** —— 所以不用解 SQLCipher，只要有 `seed` 就能批量还原。

**但本机实测这条规则在 macOS 上不成立**（已完整验证，不打算再猜）：

| 步骤 | 结果 |
|---|---|
| Frida 扫 `rw-`（206 MB）找 8~10 位数字串 | 35 个候选 → **0 命中** |
| 扩扫 `r--`+`rw-`，放宽到 6~12 位 | 30,449 个候选 → **0 命中** |
| 8 种拼接变体 × 3 种 wxid 形式 | 730,776 次 → **0 命中** |

样本 C0 = `a6 61 44 33 57 5d dc 4a`。这说明 `seed` 不是内存里能正则捞出的纯数字常量，
或 macOS 的派生公式与 Windows 不同（也可能在 keychain / 别的进程）。
该开源方案在 Windows 有实现与用户群，**macOS 不可直接套用**。

另一个教训：**不要在 Frida 脚本里做长时间同步循环** —— 第一次全量扫描把 gadget 注入卡死了，
必须分块驱动（Python 侧逐块调 RPC）。

### 仍未做

- **只能发服务端已认识的表情**（按 md5 反查）。本地新图需要先走收藏/上传链路 —— **未逆向**。
- **收藏表情的 caption**：fav 表在 SQLCipher 里；WCDB/SQLCipher 静态链接且符号剥离，
  按导出名钩 `sqlite3_*` 必然落空（实测 4 分钟零流量）。面板只能显示"md5 + 序号"占位。
- **群聊/私聊分流**、**raw 图片直发**（agent 模式只支持 `attachment_id`）。
- 本仓库只做**表情这一条链路**的逆向；其余 mars/协议细节未展开。

---

## 免责

仅供**自有账号**的技术研究与自动化使用。逆向与自动化违反微信用户协议，可能导致账号受限。
请勿用于骚扰、群发、营销或任何未经授权的大规模操作。作者不承担使用者行为的任何后果。

## License

MIT(见 [LICENSE](LICENSE))。
