# sendemoji 请求体 —— 实捕样本与解码（T4 实现依据）

> 采集：2026-10-26 /private/tmp/wxre2/body3.log（用户从表情面板发一张表情给 filehelper）
> 采集点：`wechat.dylib + 0x3e58f14`（`blr x8` 之后），`sp+0x140` → holder；`holder+0x00`=数据指针，`holder+0x0c` 和 `holder+0x10` = 长度（两处相同）；`holder+0x20`=16 字节摘要。
> 捕获到的心跳 body（len=404）以 `0a0208001007...` 开头、可正常解析 → 该落点已验证有效。

## 原始字节（211 字节）

```
0a400a010010cac8fbda081a106d31333361616136386561656565393720b5b69093ffffffffff012a15556e696669656450434d61632032362061726d363430910a1001
1a8c010a2030333032653539373437343364386134363366363465646661636461376661321000189991012204080012002802320a66696c6568656c706572
3a293c67616d6565787420747970653d22302220636f6e74656e743d223022203e3c2f67616d656578743e4a2366696c6568656c7065725f313739333031323930315f
38325f787765636861745f3333
```
（实际是上面三行拼起来、无换行；文件 `/private/tmp/wxre2/sendemoji-sample.hex`）

## 结构

```
EmojiSendRequest {
  1: EnvelopeHeader {            // 64 B
       1: bytes  = 00
       2: varint = 2338251850                  // sessionId（同其它 cgi，取 onebot globalSessionId）
       3: string = "m133aaa68eaeee97"          // 16 字符，本样本固定值（疑似 client proof 的字符串形态）
       4: varint = 18446744073481231157         // 0xffffffffb5b69093 型 u64（deviceId 域，未定）
       5: string = "UnifiedPCMac 26 arm64"
       6: varint = 1297                         // 每条消息都不同（1297/1303…）→ 顺序号/计数，不是版本号
     }
  2: varint = 1
  3: EmojiMessage {              // 140 B
       1: string = "0302e5974743d8a463f64edfacda7fa2"   // ★ 表情 md5（32 hex，字符串形态）
       2: varint = 0
       3: varint = 18585                                // 0x4899，含义未知（与 md5 相关？未定）
       4: msg { 1: varint = 0, 2: string = "" }         // 恒为空对
       5: varint = 2
       6: string = "filehelper"                         // ★ 接收方
       7: string = "<gameext type=\"0\" content=\"0\" ></gameext>"  // 41 字符
       9: string = "filehelper_1793012901_82_xwechat_33" // ★ clientMsgId：<to>_<ts>_<rand>_xwechat_<n>
     }
}
```

## 已确定的结论

1. **body 里没有 cdnurl / aeskey / width / height / 任何上传凭据** ⇒ 服务端**按 md5 反查**表情。
   推论：只能发「微信服务端已知」的表情（收藏/接收过的自定义表情、商店表情）；本地库里服务端没见过的图片需要先走收藏/上传（那是另一条链路）。
2. 之前认为「`task+0x04 = 0xaf(175)` 是 body 长度」是**错的**：实捕 body 是 211 字节。175 是别的字段。
3. 与 `WxSendImgMsg` 的信封不同（img 的 header 是 `{flag, sessionId, clientProof(bytes), deviceId, platform, version}`），
   表情这条 header 的 field 3 是 **16 字符字符串**、field 6 是逐条递增的计数 ⇒ **不能直接复用 img 的 builder**。
4. `f3.3 = 18585`、`f3.5 = 2` 含义未定；`f1.4` 是否为 deviceId 未定。第一次实现先按样本原样透传（这些值在同一台机器上是稳定/可取的）。

## 给 T4 的实现要求

- `BuildEmojiMsgProto(selfID, toID, md5 string) (hexBody string, err error)`：手写最小 protobuf 编码器（**不新增依赖**，不要改 `proto/*.proto`）。
- 需要运行期才知道的三个值（`f1.2` sessionId、`f1.3` 16 字符、`f1.4` u64、`f1.6` 计数）必须从现有 Go 全局（如 `globalSessionId` / `globalClientProof` / `globalDeviceId`）或配置取，
  **不许把本样本的常量硬编码成唯一来源**；取不到就报错而不是瞎填。
- `f3.9` clientMsgId 按 `<toID>_<unix>_<rand>_xwechat_3` 生成（与 img builder 同风格）。
- 发送：`worker.go` 加 `case "emoji":` → `script.ExportsCall("triggerSendEmojiMessage", taskId, selfID, toID, bodyHex, payloadHex)`。
  `payloadHex` 先照 `BuildSendPayload(taskId, "<kind>")` 里其它 kind 的形状推，跑不通再用抓到的 `STRUCT` 布局修正。
- **验收**：真发到 filehelper，且**回显为表情（sub_type=1 / `<msg><emoji`）**；只出现图片或报错都算没成。
- 复检脚本（我留下的）：`/private/tmp/wxre2/hook3.js` + `run2.py`（后台 job 方式启动，**不要 fork 守护**）可再次抓 body 做对照。
