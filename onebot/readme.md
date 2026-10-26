# onebot（Go 服务）

这个目录是完整的服务端，但它原本是个更大的内部工作台（招聘/CRM/履约等业务占多数）。
**本仓库关心的是表情链路**，相关文件在这几处：

## 表情发送（本仓库的核心）
| 文件 | 作用 |
|---|---|
| `worker_emoji_msg.go` | `sendemoji` 请求体构建（手写 protobuf，逐字节复现实捕样本） |
| `sticker_send.go` | 从表情库取图 → 校验 → 入队 → 等待结果 → `MarkUsed` |
| `worker.go` | `case "emoji":` 分支（调 Frida export `triggerSendEmojiMessage`） |
| `script.js` | 注入微信的 Frida agent，含 `triggerSendEmojiMessage` |

## 表情库
| 文件 | 作用 |
|---|---|
| `sticker_store.go` | 存储/去重/尺寸门/图片头解析/模糊匹配/淘汰 |
| `sticker_routes.go` | `GET /api/stickers`、`image`、`rename`、`delete`、`upload` |
| `web/stickers.js` | 面板「表情库」页 |

## 出向 API（给 DSH 插件用）
| 文件 | 作用 |
|---|---|
| `send_routes.go` | `POST /api/send/{text,image,sticker,emoji}`，local/agent 双通道 |
| `agent_contract.go` | 受控发送协议（text / attachment / emoji） |
| `wechat_agent_delivery.go` | 协议 → `SendMsg` 映射 |

## MCP 服务端（把工具暴露给 DSH）
| 文件 | 作用 |
|---|---|
| `mcp_server.go` | `POST /mcp`，JSON-RPC 2.0，Streamable HTTP |
| `mcp_registry.go` | 工具目录（与内部 registry 同源，无手抄清单） |
| `mcp_tools.go` | 线格式与错误文本卫生 |

## 构建

```bash
# 不带 Frida（仅测试，Frida 是空实现）
go build -tags noFrida ./...
go test  -tags noFrida ./...

# 带 Frida（需要 frida-core devkit 的 frida-core.h 与 libfrida-core.a）
export CGO_CFLAGS="-I/path/to/devkit -w"
export CGO_LDFLAGS="-L/path/to/devkit -lfrida-core -lbsm -framework IOKit \
  -framework Foundation -framework AppKit -framework Security -lpthread -ldl -lm"
go build -o onebot-frida .
```

依赖用 `go mod download`（`vendor/` 未入库，体积过大）。

## 注意

- Frida 逆向与自动化**违反微信用户协议**，仅限自有账号技术研究。
- 微信版本强绑定：`wechat_version/4_1_11_53_mac.json` 里的偏移只对 **4.1.11 (269109)** 有效。
  换版本需要重新定位（见 `docs/re/`）。
