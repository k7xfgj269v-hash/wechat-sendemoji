# 微信 4.1.11 自定义表情（emoji / emoticon）发送链路 — 静态逆向规格（T3）

> 样本：`/Applications/WeChat.app` 4.1.11 (269109)，`Contents/Resources/wechat.dylib` 328,558,208 B
> arm64 slice 158,180,992 B（`lipo -thin arm64` 后与静态分析副本 `cmp` 完全一致）
> 方法：**纯静态**（otool / strings / numpy 指纹扫描 / rizin 定点反汇编）。
> 本机 `task_for_pid` 受限，本文所有地址均可离线复现，见 §11。
>
> 标注约定：**【事实】** = 有地址/字节证据；**【推断】** = 由事实推出但未验证；**【未命中】** = 扫不到。

---

## 0. 结论速览

| # | 问题 | 结论 | 证据强度 |
|---|---|---|---|
| 1 | 表情 xml 模板在 dylib 的位置 | `<msg><emoji`、`emojiattr`、`externurl` 字面量 **全部未命中**；只找到碎片 `<msg`（`0x7ce6200`，无 xref）与属性名字符池（`0x7eeb3a0..0x7eeb4f5`） | 【事实（未命中也是事实）】 |
| 2 | 是否复用同一条 mars 大包 | **是**：所有消息类型（text/img/video/voice/file/appattach/reply）都走 `MMStartTask 0x5116f30` + `Req2Buf 0x54f14f8`，差别只是 task 结构里塞的 **cgi 字符串** 和一个 **u32 命令域**（media 类 `0x6e`，text `0x20a`） | 【事实（script.js 现有 6 条流水线 + 锚点表）】 |
| 2b | 表情走哪条 cgi | **【运行时已证实】`/cgi-bin/micromsg-bin/sendemoji`**（reqid=68, nettype=3 长连）。用户发 3 张表情，抓到的出站任务里只有 `sendemoji`，无 `newsendmsg`/`sendappmsg`（§2.6） | 【事实·运行时】 |
| 3 | 表情图片 CDN 上传 | **复用同一套 mmcdn 任务**：CDN 任务统计日志函数 `0x5405ec0` 的字段里就有 `emojimd5`，与 `filemd5/thumburl/midimgmd5/mp4_identify_md5` 同级；cgi 表里有 `cdnuploadimgprepare/cdnuploadimgcommit/uploadmsgimg/mmuploadmedia` | 【事实】 |
| 4 | 必需字段 | 接收侧属性表（onebot 已在用、能跑通）为 24 个属性；其中 **md5 / len / aeskey / width / height 可本地算**，**cdnurl / thumburl / externurl / encrypturl 必须服务端回**（见 §7） | 字段名【事实】/ 发送必要性【推断】 |
| 4b | 发侧 pb 字段号/顺序 | **BLOCKED（三次确认）**：`autoBufferWrite 0x3E7FA8C`（§10.3）、task 结构（§2.8）、`0x3e58f14` out 参数（§2.10）三条路全部排除。`0x3d769a0` 经反汇编判定为写 bool 的状态查询虚函数，非序列化本体 | 【BLOCKED】 |
| 5 | 表情包(pack) vs 单张 | `emojipack` / `EmojiPack` / `packid` / `uploademoji` / `lensid` / `tpauthkey` **全部未命中**；存在的是 `upload_emoticon` / `operate_emoticon` 两个日志 tag 与 `MM_FAV_DATA_TYPE_EMOJI`（收藏域） | 【未命中 + 事实】 |

---

## 1. 扫描方法（与旧脚本的差别，必须看）

`docs/re/scripts/xref_scan.py` 与本地 `xc.py` 只认 **`adrp` + `add` 且 rd==rn** 的取址写法。
4.1.11 里大量字面量是 **"一次 adrp + 多条 `add Xd, Xn, #imm`"（rd≠rn）**，
这样扫会把整片字符串判成"无引用"——本轮的 emoji 属性池就是被旧写法误判成 `xref=NONE` 的。

本轮改用 `xref2.py`（`/private/tmp/wxre/xref2.py`，可拷进 `docs/re/scripts/`）：
记录 `adrp rd`，随后 24 条指令内凡 `add/ldr` 的 `rn==rd` 即命中，命中后不再要求 rd==rn。

```bash
# 建表（158 MB，约 4 s，产出 1.24 M 条 (pc,target)）
python3 xref2.py wechat.arm64 --build
# 关键词 → 字符串 vaddr → xref → 函数入口（自动回退到函数序言）
python3 xref2.py wechat.arm64 --str sendemoji emojimd5
# 段内所有被引用 vaddr + 解码串
python3 xref2.py wechat.arm64 --sect __const 0x7eeb300 0x7eeb600
# 某函数地址区间内引用到的所有字符串（本次最有用的一招）
python3 refs_in.py 0x5405ec0 0x5407000
```

段布局（本文所有地址可据此换算）：

```
__text        0x18000  .. 0x67b0214   (0x6798214 B)
__const       0x6bf7c80 .. 0x8c48dad  (0x205112d B)
__cstring     0x8c48db0 .. 0x8d447ff
__objc_methname 0x8d447ff .. 0x8d50862
__DATA.__data 0x9396200 ..
```

---

## 2. 事实表：字符串 → 地址 → 函数 → 结论

### 2.1 cgi 配置（唯一能证明"表情有专用 cgi"的证据）

| 字符串 | vaddr | 所在 blob | xref | 结论 |
|---|---|---|---|---|
| `<cgi reqid="68" respid="1000000068" nettype="3" netstrategy="0">sendemoji</cgi>` | `0x8c846ff` | `0x8c83dc8`（6,924 B 的 micromsg cgilist） | **无**（运行时 XML 解析） | 【事实】存在表情专用长连 cgi，reqid 68 |
| `<cgi reqid="69" respid="1000000069" nettype="1" netstrategy="0">receiveemoji</cgi>` | `0x8c84b74` | 同上 | 无 | 【事实】收表情走短连，reqid 69 |
| `sendemoji` | `0x8c86278` | `0x8c858d5`（23,144 B，`netproto="2"` 的第二份表） | 无 | 两份表都有 |
| `receiveemoji` | `0x8c86b1c` | 同上 | 无 | 同上 |
| `newsendmsg` / `sendmsg` / `sendappmsg` | `0x8c84567` / — / `0x8c8465f` | 同 blob | 无 | 普通聊天消息 cgi，reqid 237 / 2 / 107 |
| `uploadmsgimg` | `0x8c84c66` | 同 blob | 无 | 图片上传+发消息 cgi，reqid=9，**短连** |
| `uploadvoice` | `0x8c84750` | 同 blob | 无 | reqid=19，长连 |
| `mmuploadmedia` | `0x8c857fd` | 同 blob | 无 | 通用媒体上传 cgi，reqid=111，短连 |
| `cdnuploadimgprepare` / `cdnuploadimgcommit` | `0x8c8483b` | 同 blob | 无 | CDN 三段式（prepare/upload/commit），reqid=0，短连 |

**同一张表里逐条解析出的 reqid / nettype（`blob@0x8c83dc8`，53 条，这里是本议题相关的 18 条）**：

```
newinit        27   nettype=3      newsync       121  nettype=3
newsendmsg    237   nettype=3      sendmsg         2  nettype=3
sendappmsg    107   nettype=3      sendemoji      68  nettype=3   <-- 长连，与消息发送同域
heartbeat     238   nettype=3      uploadvoice    19  nettype=3
uploadvideo    39   nettype=1      uploadmsgimg   9  nettype=1
mmuploadmedia 111   nettype=1      uploadappattach 105 nettype=1
receiveemoji   69   nettype=1      getmsgimg      10  nettype=1
downloadvideo  40   nettype=1      cdnuploadimg{prepare,commit}  0  nettype=1
revokemsg       0   nettype=1
```

**这条 nettype 差异是 §4 的核心证据**：`sendemoji` 是 **nettype=3（长连接）**，与
`sendmsg`/`newsendmsg`/`sendappmsg` 同类；而 `receiveemoji`/`uploadmsgimg`/`mmuploadmedia`
是 **nettype=1（短连接）**。⇒ 表情**发送**在协议分类上属于"消息发送"，**不属于**"媒体上传"。

> **cgi 名字面量在代码段不存在**：`sendemoji`/`newsendmsg`/`uploadmsgimg`/`sendappmsg`
> 在整份 dylib 里各只出现 **2 次（= 两份 XML blob）**，`__text` 里 0 次。
> 这与 `docs/re/wechat-411-anchors.md` §4.1 对 `newsendmsg` 的结论一致：**名称是运行时从 XML 切出来的**。
> ⇒ 想静态确定"表情用哪条 cgi"这条路**走不通**，必须靠运行时断点或抓包（见 §10）。

### 2.2 表情/媒体 XML 属性名字符池

`0x7eeb3a0 .. 0x7eeb4f5` 是一片**无 NUL 分隔的碎片池**（16 字节槽 + `0xAA`/`00` 填充，
大量交叉重叠），被 `adrp`+多条 `add` 引用。逐字节 dump 出来含这些 ASCII 片段：

```
0x7eeb3b0  cdnurl  0x7eeb3b8  externur   0x7eeb3c0  externmd
0x7eeb3d0  thumbimg          0x7eeb3f0  tpurl      0x7eeb3f8  attached
0x7eeb410  product           0x7eeb490  encrypturl  0x7eeb4a0  uiemoticonwidth
0x7eeb4b0  uiemoticonheight 0x7eeb4c0  attachedtext  0x7eeb4d0  emoticonsvrdesc
0x7eeb4e0  srcmsg createtime
```

被引用到的精确起点（`--sect` 结果）：

| vaddr | n_ref | 首个 xref | 推定函数入口 |
|---|--:|---|---|
| `0x7eeb470` | 5 | `0x43f9cec` | `0x43f9890` |
| `0x7eeb480` | 2 | `0x43fad10` | `0x43fa170` |
| `0x7eeb490` (`encrypturl`) | 2 | `0x43fc9b0` | `0x43fc6d4` |
| `0x7eeb4a0` | 2 | `0x43fcdfc` | `0x43fc6d4` |
| `0x7eeb4b0` | 2 | `0x43fce4c` | `0x43fc6d4` |
| `0x7eeb4c0` (`attachedtext`) | 3 | `0x43fd030` | `0x43fc6d4` |
| `0x7eeb4d0` | 2 | `0x43fd124` | `0x43fc6d4` |
| `0x7eeb4e0` | 2 | `0x4401574` | `0x44009d4` |

**结论**：这是 4 个函数（`0x43f9890` / `0x43fa170` / `0x43fc6d4` / `0x44009d4`）共用的
"属性名 → 处理"字符池，同一函数 `0x43fc6d4` 连续取 `encrypturl / uiemoticon* / attachedtext /
emoticonsvrdesc` ⇒ 该函数是**表情/媒体 xml 的属性读写点**。
`0x43fc6d4` 内部还引用 `__FILE__ = str.…chromium/base/base_strings/string_piece.h`，
即用 `base::StringPiece(ptr, len)` 从池里取片段（这解释了为什么片段彼此重叠、无 NUL）。
【推断】= 表情 xml 的**解析/序列化**在此函数族；【未命中】= 找不到拼接 `<msg><emoji …` 的模板字面量。

### 2.3 CDN 层：表情复用同一套 mmcdn 任务

`0x5405ec0` 是 CDN 任务统计/日志函数，其引用到的完整字段串（`refs_in.py 0x5405ec0 0x5407000`）：

```
@0x8cb8227 'uin %_ dataid %_ '      @0x8cb8239 'allcost %_ netcost %_ '
@0x8cb8227 'usedgroup %_ '          @0x8cb825e 'svraddr(%_) %_ '
@0x8cb82a7 'recved fileid %_ '      @0x8cb82b9 'recved videofileid %_ '
@0x8cb82d0 'fileurl %_ '            @0x8cb82dc 'thumburl %_ '
@0x8cb82e9 'emojimd5 %_ '   <--     @0x8cb82f6 'filemd5 %_ '
@0x8cb8302 'midimgmd5 %_ '          @0x8cb8310 'thumbfilemd5 %_ '
@0x8cb8321 'midfilesize %_ '        @0x8cb8331 'midtotalsize %_ '
@0x8cb8342 'mp4_identify_md5_ %_ '  @0x8cb8358 'filesize %_ '
@0x8cb8365 'totalsize %_ '          @0x8cb8373 'cache_size %_ '
@0x8cb83a3 'usedaddr %_ '           @0x8cb83b0 'svr_error_count %_ '
```

**结论**【事实】：图片 / 视频 / 语音 / **表情** 共用同一个 CDN 任务对象，
表情只是在同一结构里多一个 `emojimd5` 字段（与 `filemd5`、`thumburl` 同级）。
⇒ **表情图片上传不需要另开 CDN 通道**，用现有 `triggerUploadImg` 那条 upload X1 即可；
表情与图片的差别在"上传后回填的字段名（emojimd5 vs filemd5）"，不在通道。

同族证据：

| 字符串 | vaddr | xref → 函数 | 结论 |
|---|---|---|---|
| `thumburl`（裸属性名） | `0x8cacee7` | `0x5345b88`→`0x534421c`；`0x53c3d04/2c`→`0x53c3564`；`0x53d0d04`→`0x53d0ce0`；`0x53d27bc`→`0x53d18d8`；`0x53e5100`→`0x53e4e00` | 5 个取值点，说明 thumburl 是被**解析读取**的属性名 |
| `cdntask %_ recved thumburl:%_` | `0x8cb43c5` | `0x53c3eec`→`0x53c3564` | CDN 收包时回填 thumburl |
| `cdnurl is exist` | `0x7ac8a10` | `0xdc5d14`→`0xdc5774`、`0xdcd02c`→`0xdcc94c`、`0xdf2564`→`0xdf1e84` | 三个检查"cdnurl 已存在"的分支（【推断】= 复用已上传结果的短路） |
| `productid` | `0x8cbfa5e` | `0x5454a44`→`0x545336c` | 商品/表情的 productid 读取点 |

### 2.4 表情模块源码锚点（`__FILE__` 字符串 → 函数）

微信大量日志用 `__FILE__`，源码文件名是极稳的锚点：

| 源文件串 | vaddr | xref → 函数入口 |
|---|---|---|
| `emoji_utility.cc`（紧邻 `~EmoticonConfig`） | `0x7bf2fe0` | `0x1cccf68`→`0x1cccecc`；`0x1ccd828`→`0x1ccd538`；`0x1ccdd1c`→`0x1ccdc84` |
| `get_emoticon.cc` | `0x7cc0f00` | `0x25f7300`→`0x25f728c`；`0x25f77c0`→`0x25f7758`；`0x25f7d3c`/`0x25f9630`→`0x25f7ca8`；`0x25fa43c`/`0x25fa8f8`→`0x25fa204` |
| `emoticon_key.cc` | `0x7c00ad0` | `0x1d4756c`→`0x1d474bc` |
| `upload_emoticon.StartUploadEmot…` | `0x7cc33f0` | `0x26162a4`→`0x26161b0`；`0x261689c…0x2617a44`（7 处）→`0x26165e0` |
| `operate_emoticon…` | `0x7cc2d30` | `0x2610374`→`0x261027c`；`0x26108a4…0x2611d08`（5 处）→`0x261072c` |
| `emojimd5 %_`（CDN 统计） | `0x8cb82e9` | `0x54065c0`→`0x5405ec0` |
| `ParseMessageXml`（日志 tag） | `0x7df2100` | `0x3717be0`→`0x371744c` |

**结论**【事实】：4.1.11 里表情的代码分层很清楚
`upload_emoticon`（上传/加表情，`0x26165e0`）→ `operate_emoticon`（表情操作，`0x261072c`）
→ `get_emoticon`（取表情，`0x25f7ca8`/`0x25fa204`）→ `emoticon_key`（`0x1d474bc`）
→ CDN 任务（`0x5405ec0`）；消息 xml 解析在 `ParseMessageXml 0x371744c`。
**但没有一个是"构造表情聊天消息"的函数**（`upload_emoticon` 是"我的表情"入库，不是发消息）。

### 2.5 `<msg` / `<appmsg` 片段

| 字符串 | vaddr | xref → 函数 | 说明 |
|---|---|---|---|
| `<msg` | `0x7ce6200` | **无** | 在 16 字节槽碎片池里（邻槽 `d tr` `thumbPat` `lf_d` …），无法归属到函数 |
| `<appmsg` | `0x7df20e0`（池内偏移） | `0x370f2c4`→`0x370f1c4`、`0x3742d88`→`0x3740c7c`、`0x37457d0`→`0x3745724` | appmsg 文案/解析侧 |
| `<img src="data:image/jpeg;base64,%@">` | `0x8d41fd3` | 无 | ObjC/UI 串，**不是**协议 xml |
| `<sysmsg type="%s">…`（格式化串） | `0x8c8…`（见 `strings` 129144 行） | — | 客户端确实会**自造**部分 xml（sysmsg） |

### 2.6 【运行时已证实】表情聊天发送走 `sendemoji`，不走 newsendmsg

只读 Frida 钩子（`/private/tmp/wxre/capture_sendemoji.js` + `capture_run.py`，见 §10.2）挂在
`MMStartTask.wrapper 0x5120fd8` / `MMStartTask.main 0x5116f30` 上，读任务结构：
`cgi = *(msg+0x18)`（C 串）、`cgiLen = *(msg+0x20)`、`cmd = *(msg+0x0c)`。
用户在微信表情面板给「文件传输助手」发一张表情时，抓到的出站任务序列（原文）：

```
SEND cgi=/cgi-bin/micromsg-bin/sendemoji      cmd=0x0 cgiLen=31   (三轮样本累计 22 条，含重试/多张)
SEND cgi=/cgi-bin/micromsg-bin/newsync        cmd=0x0 cgiLen=29
SEND cgi=/cgi-bin/micromsg-bin/heartbeat      cmd=0x0 cgiLen=31
SEND cgi=/cgi-bin/micromsg-bin/statusnotify   cmd=0x0 cgiLen=37
SEND cgi=/cgi-bin/micromsg-bin/predownload_check
SEND cgi=/cgi-bin/micromsg-bin/newreportkvcomm
```

**同一时段内没有 `newsendmsg` / `sendappmsg`**（用户先后发了 3 张表情，每次都是 `sendemoji`）。
⇒ 【事实】聊天表情发送 = **`/cgi-bin/micromsg-bin/sendemoji`（reqid 68，nettype 3 长连）**，
`triggerSendEmojiMessage` 用这条 cgi 是对的；§9.2 第 2 条"未验证"由本条关闭。

任务结构实测布局（`STRUCT` 行，0x300 B，与 `script.js` 伪造结构一致）：

```
msg+0x00 u32 = 0x35a           msg+0x04 u32 = 0xaf (=175，与 main(x0=msg, x1=0xaf) 的第二参一致
                                               —— 疑似请求体/pb 长度)
msg+0x0c u32 = 0x0 (cmd)       msg+0x10 u32 = 3
msg+0x18 ptr = cgi 字符串       msg+0x20 u64 = cgi 长度(31)
msg+0x28 u64 = 0x8000000000000030
msg+0x30 u64 = 0x0000000001010100
msg+0x50     = "default-longlink"（mars 通道名）
```

> **推论（对 T4 有用）**：真实 sendemoji 的 `cmd`（+0x0c）**是 0**，不是 `script.js` 里
> img/video/voice 用的 `0x6e`。要么 0x6e 只是那些 kind 的巧合/被忽略，要么 +0x0c 语义与我
> 读到的不同。**T4 落地时优先沿用现有可用值 0x6e（已验证能工作），不要为了"贴合真实值"改成 0。**

---

### 2.7 【运行时已证实】真实序列化点 = `autoBufferWrite 0x3E7FA8C`（文档锚点 0x54f14f8 是错的）

**关键发现：运行时地址不在 `wechat-411-anchors.md` 里，而在仓库自带的版本 profile**：
`wechat_version/4_1_11_53_mac.json`（onebot 打包 script.js 时就靠它填模板）：

```
sendFuncAddr       = 0x5120fd8     # MMStartTask.wrapper（运行时实测每次任务都中）
req2bufEnterAddr   = 0x3e58e8c     # ← 真实 Req2Buf 入口（不是 0x54f14f8！）
req2bufExitAddr    = 0x3E59DE0
blrX8Addr          = 0x3e58f0c     # blr x8 前一格
autoBufferWriteFunc= 0x3E7FA8C     # ← 序列化写缓冲：pb 明文在这里
buf2RespAddr       = 0x3e7e670
uploadImageAddr    = 0x529bd7c
startDownloadMedia = 0x529c9e4
downloadImagAddr   = 0x53c0004
downloadFileAddr   = 0x535ea98
downloadVideoAddr  = 0x5378a18
```

`autoBufferWrite 0x3E7FA8C` 实测签名（prologue `stp x22,x21,[sp,-0x30]!` + `mov x19,x2 / x20,x1 / x21,x0`）：

```c
int autoBufferWrite(AutoBuffer *buf /*x0*/, const void *data /*x1*/, uint32_t len /*x2*/);
```

钩它并把 `(x1, x2)` 原样 hex 出来，**就能拿到 mars 即将发出的明文 protobuf**。
20 秒自然流量自证：8 条，第一条 `len=404`，内容 `0a020800 1007 1af002 08ea02 12ea02 082f…`
（可被 protobuf 干净解析）。

> **陷阱（务必写进任何后续脚本）**：`blrX8Addr = 0x3e58f0c` 这条指令实际是
> `add x4, sp, 0x138`，真正的 `blr x8` 在 **`0x3e58f10`**。所以在 0x3e58f0c 处读
> `x2/x3` 拿到的是**栈临时指针**（`sp+0x14c` / `sp+0x13f`），不是 body；
> `script.js` 之所以能在那里塞 payload，是因为它读的是 `x1`（`sp+0x140`，栈上 AutoBuffer）
> 并用 `autoBufferWrite` 主动写入。**要"读"别人的 body，只能从 `0x3E7FA8C` 的 `(x1,x2)` 读。**

---

### 2.8 builder 定位（静态尝试，未命中；把死路和线索都留在这里）

**① `req2bufEnterAddr 0x3e58e8c` 不是分派器。** 反汇编开头即 `std::map` 式查找：

```
0x03e58e8c  ldr   x9, [x24, 0x60]!      ; x9 = task->field_0x60（就是 script.js 里的 insertMsg）
0x03e58e90  cbz   x9, 0x3e58ec8
0x03e58e98  ldr   w10, [x9, 0x20]       ; 取被比较字段
0x03e58e9c  cmp   w10, w20
0x03e58ea0  cset  w10, lo
0x03e58ea4  csel  x8, x8, x9, lo
0x03e58ea8  ldr   x9, [x9, w10, uxtw 3] ; 沿树下行
0x03e58eac  cbnz  x9, 0x3e58e98
```
⇒ 这是**按 key 找节点**，没有 `reqid→builder` 的 switch/跳转表（我扫了函数前 400 条指令的全部 `bl`：
主要落在 `operator new/delete`、`std::string` 赋值、`0x3ecb278`（单例 getter）上，无 pb 写入器）。

**② `sendemoji` 的 task 结构（0x300 B，运行时 `STRUCT` 行）关键字段**：

```
+0x000 u32 0x400 | u32 0xaf      ; 第二个 u32 = 175 = body 长度（与 main(x0=msg, x1=0xaf) 一致）
+0x010 u32 3 | u32 1
+0x018 ptr cgi 字符串("/cgi-bin/micromsg-bin/sendemoji")   +0x020 u64 31 (cgi 长度)
+0x028 u64 0x8000000000000030    +0x030 u64 0x0000000001010100
+0x060 u64 0xaf                  ; 又一处 175
+0x068.. "default-longlink"      ; mars 通道名
+0x2c8 ptr 0x121ffd40c           +0x2d0 u64 0xaf     ; (代码指针, 175) —— **不是缓冲区**
+0x2e0 u64 0xaf
```

其中 `0x121ffd40c`（在 `__text` 内）反汇编后是一个 **`strcmp` 比较器**：

```
0x029fd40c  ldr  x8, [x1, 8]
0x029fd410  adrp x9, 0x7d10000 ; add x9, x9, 0x182
0x029fd418  mov  x10, -0x8000000000000000
0x029fd420  cmp  x8, x10
...
0x029fd460  bl   sym.imp.strcmp
```

⇒ 这是 **protobuf 反射里的"按字段名查 FieldDescriptor"** 那一类助手（`FindFieldByName`）。
说明二进制里确实存在 **pb 反射/描述符**，但 `EmojiMsg`/`EmojiInfo`/`proto3` 等描述符名字扫不到
（§9.1），所以**不能靠描述符名反推字段号**。

**③ 结论**：`sendemoji` 的 175 B 请求体
(a) 不在 task 结构里（0x300 B 只找到长度、没有 body 指针）；
(b) 不经过 `autoBufferWrite 0x3E7FA8C`（同一时刻 0 条写事件，±8 s 窗口内也没有）；
⇒ **builder 静态未命中**，需要一次定向 hook（见下）。**这是 T4 唯一的阻塞项。**

**④ 下一步该 hook 谁（按性价比排序，都是"一行 dump"级别）**：

1. `operator_new`（`sym.imp.operator_new_unsigned_long`）：在 `sendemoji` 事件窗口内
   记录 `size==0xb0(176)`/`0xa8`/`0xc0` 的分配，拿返回指针，等 ~200 ms 后 dump 这块内存 →
   基本就是 pb（175 字节刚好卡在 176 的块里）。
2. `MMStartTask.main 0x5116f30` 的 `onLeave`：`x0` 就是 task 结构（见 ②），
   顺着 `+0x2c8` 附近再往外找一层（`0x175ad2860` 那个表里出现过 `{ptr, 0xaf}` 形状）。
3. 在 `0x3e58e8c` 的**上游**（`wrapper 0x5120fd8` 内部）找 vtable[0x10] 的实际目标：
   `0x3e58f0c` 处的调用是 `ldr x8,[x0]; ldr x8,[x8,0x10]; blr x8`，把 `x8` 打印出来即真实序列化函数地址。

---

### 2.9 operator_new 抓包法 + ring 回扫 + `bt` 栈证据（本轮结果）

**方法（下次直接照抄，脚本 `/private/tmp/wxre/capture_sendemoji.js` v10.1）**

1. 钩 `sym.imp.operator_new_unsigned_long` 的桩（**wechat.dylib `0x67b35b0`**，
   运行时 `module.base + 0x67b35b0`）：`adrp x16, …; ldr x16,[x16,0xaa8]; br x16`）。
2. **先按 size 过滤再判窗口**（否则全量开销会拖垮微信）：只记 `len ∈ [0xa0, 0xc8]`，
   实测这个尺寸带 ~13 次/秒、90 s 约 1210 次，属"真实且不空跑"的量级；
   过滤掉的数量单独计数（实测 20 s 内 `filtered_out≈2.4×10⁵`）。
3. 命中窗口（`|alloc_t - emojiAnchor| ≤ 2000 ms`）才 `Thread.backtrace(..., Backtracer.FUZZY).slice(0,3)`
   + `DebugSymbol.fromAddress()`，**延时 200 ms 再读内存**（此时 builder 已写完），
   单块 ≤256 B、最多 8 块/事件。
4. **必须有 ring 回扫**：pb 往往在 **mars task 开始之前**就分配好（用户点发送 → builder 先跑 →
   才起 task）。只按"锚点之后"判断会把它们永久标成 out 而永不 dump。所以保留最近 400 条
   带 size 过滤的分配记录（≈28 s 历史），**锚点建立的瞬间回扫 `[anchor−2s, anchor+2s]`**
   （日志 `RINGSCAN/RINGHIT/RINGHEX`），同时保留实时路径（`NEW WIN/NEWHEX`）。

**本轮结论（诚实记录）**

- 钩子本身自证通过：`NEWTICK` 20 s 一条，`total=270147 / filtered_out=268942`，
  五个钩子全部 attach 成功（`operator_new` / `main.onLeave` / `autoBufferWrite` /
  `0x3e58f0c` vtable probe / `ParseMessageXml`），无 attach 失败。
- **但没有拿到 `sendemoji` 的 body**：最后一张表情发出时，armed 会话已被回收（`capture_run.py`
  在重启/父作业被 SIGTERM 后死了，daemon 化进程启动晚了），样本落在无人监听的时间窗；
  上一张（样本 5）当时只挂了 `autoBufferWrite`，`operator_new` 还没上。
  ⇒ `bt` 栈证据也**没有**拿到（这是本轮最遗憾的一点，栈能直接指 builder）。
- 会话稳定性教训（写进脚本注释）：**别在同一 shell 里"先 kill 再起"**——`capture_run.py` 必须
  用 `--daemon`（双 fork + setsid）启动，并**独立确认** `dump-req.log` 已在增长再让用户发样本。

**唯一新增的地址级证据：要求串化入口的 vtable 目标 = `wechat.dylib!0x3d769a0`**

`0x3e58f0c` 处运行时打印（`VTBL` 行，只打一次）：

```
VTBL x8=0x12337a9a0 sym=wechat.dylib!0x3d769a0 (0x3d769a0) x0=0x9f78d4900
```

即 `ldr x8,[x0]; ldr x8,[x8,0x10]; blr x8`（`0x3e58f04..0x3e58f10`）实际调用的是 **`0x3d769a0`**。
静态反汇编：

```
0x03d769a0  sub  sp, sp, 0x50
0x03d769b4  mov  x19, x3        ; x3 = sp+0x13f（bool out）
0x03d769b8  mov  x20, x1        ; x1 = sp+0x140（out 结构）
0x03d769c0  mov  x1, x4
0x03d769c4  bl   0x3d76ac4
0x03d769c8  strb w0, [x19]      ; 把 bool 写回 out 参数
```

⇒ 它是**返回 bool 的虚函数**（往 out 参数写结果），不是"把 pb 写进 AutoBuffer"的写函数本体。
**但它揭示了 body 的正确落点**：`x1 = sp+0x140` 是出参结构，样本 5 的 `POSTBLR` 转储里
`sp+0x140` 处是 `0x9f11aca00`（堆指针）—— 这就是 body/AutoBuffer 的候选。
**下一步（1 条钩子即可闭合）**：钩 **`0x3e58f14`**（`blr x8` 的下一格），读
`x1'(=sp+0x140)` 与 `sp+0x14c`，按 `{ptr,len}` 取出即 body；配 `0x3d769a0` 的返回值可确认成败。

---

### 2.10 `0x3e58f14` out 参数探针：**负结果**（T4 因此永久 BLOCKED）

**做法**：daemon 不可用（Frida 在 `fork` 后会坏，见下），改用受管后台进程挂 `capture_run.py`；
在 `0x3e58f14`（`blr x8` 之后的下一格）`onEnter` 里读 4 种候选布局：

| 候选 | ptr 来源 | len 来源 |
|---|---|---|
| A | `sp+0x140` | `sp+0x148` |
| B | `sp+0x140` | `sp+0x14c`(u32) |
| C | `sp+0x148` | `sp+0x150` |
| D | `sp+0x138` | `sp+0x140` |

每种都要求：`ptr` 可读且保护属性非 `r-x`、`16 ≤ len ≤ 4096`、首字节是合法 protobuf tag。

**结果（6 次探针，全部来自 heartbeat/newsync 等自然流量）**：

```
PBWTICK probes=6 body_hit=0
sp+0x140 (u64) 恒为堆指针（0x9f6b15120 / 0x9f5f….）
sp+0x14c (u32) 恒为 0      ← 没有任何一次给出非零长度
```

⇒ **该 vtable 调用不产出 body**：`x1 = sp+0x140` 拿到的不是"请求体 AutoBuffer"，
`x2 = sp+0x14c`（u32 出参）恒 0。结合 §2.9 里 `0x3d769a0` 的 `strb w0,[x19]`（写 bool 到 `x3`），
这更像是 `Task::GetXxx(out_ptr, out_u32, out_bool, out_ptr2)` 一类的**状态查询虚函数**，
不是序列化本体。`req2bufEnterAddr 0x3e58e8c` 那个函数体（`std::map` 查找后）做的也只是查找/装配。

**结论（第三次也是最后一次确认）**：`sendemoji` 的 175 B 请求体既不在
`autoBufferWrite 0x3E7FA8C`（§10.3）、也不在 task 结构（§2.8）、也不在
`0x3e58f14` 的 out 参数（本节）。⇒ **静态 + 只读 hook 组合已经到顶**，
**T4 的表情 pb 字段表在本机样本上判定为 BLOCKED**。

**踩坑记录（节省后来者时间）**
1. `capture_run.py` **不能**用 `fork` 做 daemon：Frida 导入后进程已多线程，`os.fork()`
   会使子进程的 Frida 内部状态损坏（Python 3.14 直接给 `DeprecationWarning: multi-threaded … fork()`），
   实测 daemon 化后 agent 能 load 但 `File` 写日志失效（`dump-req.log` 根本不生成）。
   **正确做法**：用受管后台进程跑 `capture_run.py`（不 fork），并且**不要在同一 shell 里
   "先 kill 再起"**（父作业被 SIGTERM 会带走子进程，这是我第 2、3 次丢样本的直接原因）。
2. 探针自证的最小判据：`PBWTICK probes>0` 且 `body_hit>0`；只有 `probes>0` 说明"点在跑但偏移错"。

---

### 2.11 收藏表情（Fav ORM）——能拿到的与拿不到的

**① 收藏表的完整列名（静态解出，`__cstring 0x8c4d240–0x8c4d3fa`，41 项）**

```
reserved0 reserved1 reserved2 reserved3 user_id user_name profile_url user_storage_daily_detail
md5 caption product_id aes_key thumb_url tp_url auth_key cdn_url extern_url extern_md5 encrypt_url
tag_local_id tag_server_id fav_local_id fav_server_id op_code local_id server_id update_seq flag
update_time source_id sync_status upload_status upload_error_code trans_res_status
trans_res_error_code fromusr fromusr_id realchatname realchatname_id ext_bu
```

关键：**有 `caption`（备注/名字），没有 `width/height`**；`md5 / aes_key / cdn_url` 都在。
目录键 `_TYPE_MD_PID`（`0x7cc9e00`）说明 fav 的"目录/index 键"是 `type + md5 + product_id` 组合。

**② ORM 描述符构造函数 `0x2671798`（1448 B）** —— 反汇编开头即"逐字段建 `{name, offset}`"：

```
0x026717c4  mov  w0, 0x10 ; bl operator_new      ; 16 B = {char*, u64 offset}
0x026717d4  mov  w8, 0xd8                          ; ← 字段 "type" 的偏移 0xd8
0x026717d8  adrp x9, 0x9184000 ; add x9, x9, 0x1c0
0x026717e0  stp  x9, x8, [x20]                     ; {name_ptr, 0xd8}
0x026717ec  mov  w1, 0xd8
0x026717f4  bl   0x4e5bbf0                        ; 注册到 ORM 字段表
```

即该表就是一个 `{列名, 结构体偏移}` 数组，配合 `0x4e5bbf0`（register）与 `0x4e5ba18`（init）。
⇒ 静态能拿到**列序与偏移**，但拿不到"运行时有哪些行"。

**③ 数据库是加密的（因此不能离线读 caption）**

```
db_storage/favorite/favorite.db        e4dbedc0 47998213 e0b76aa8 accb59b7 …   ← 不是 "SQLite format 3"
db_storage/emoticon/emoticon.db        7ac6b190 fe438593 b5590182 837207b2 …   ← 同上
```

favorite.db（118 KB）+ -wal / emoticon.db（491 KB）+ -wal 都存在，但均为 SQLCipher，
**没有 "SQLite format 3" 头**，本机无 key 提取器（不走 `wechat-use` 那条）⇒ 离线读不了。
`business/favorite/` 目录只有空的 `temp/`，`db_storage/MMKV/` 里也没有表情相关键。

**④ 能拿到的：表情文件缓存（文件名 = md5）**

```
~/Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files/<wxid>/business/emoticon/
  Persist/<md5 前 2 位>/<32 位 hex>      255 个（内容加密，"发送只要 md5"故可用）
  Thumb/...                              258 个 .thumb
```

已生成 `/private/tmp/fav-emoji.json`：255 条 `{md5, cdn_url:"", aes_key:"", width:null, height:null, bytes, file}`。
**md5 足够发送**（服务端按 md5 反查）；`caption/aes_key/cdn_url` 仍需运行时 SQL 才能补。

**⑤ 运行时抓 caption 的钩法（已实现，缺"有流量"这一环）**

收藏数据在 SQLCipher 里，但 **sqlite3 层看到的是解密后明文**，所以无需解密 DB：

```javascript
// /private/tmp/wxre/fav_dump.js（只读；不改寄存器/内存/返回值）
// 注意 Frida 17：Module.findExportByName 已移除 → 用 Module.findGlobalExportByName
// 实测地址：sqlite3_prepare_v2 @0x192e739ac  prepare_v3 @0x192eaf250  step @0x192e4bd9c（系统 libsqlite3）
prepare(sql)  → 记 SQL；命中 /favorite|fav_|emoji|emoticon/i 的语句进关注集
step(stmt)==100(SQLITE_ROW) → 逐列打印 column_name / column_text / column_int64
finalize(stmt) → 出关注集
```

**两个必踩的坑**：
1. `sqlite3_step` 的 `stmt` 必须在 **onEnter 用 `args[0]` 捕获**；`onLeave` 里的 `context.x0`
   是**返回值**（100/101），照它当 stmt 会永远匹配不上。
2. WeChat 整包 141 个 Mach-O **没有一个自己定义 `sqlite3_*`**，全部走系统 `libsqlite3`
   ⇒ 按导出名挂钩即可，不需要按模块偏移。

**结果（已实测，含一次真实用户操作）：`prepares=0 / steps=0`，caption 拿不到。**

- 钩子 armed 后**连续在线约 4 分钟**，其中包含用户"点开表情面板 → 我的表情 → 滑动一遍"的完整动作；
- 期间 `TICK` 始终 `prepares=0 steps=0 rows=0`，**一次 sqlite3_prepare / step 调用都没有**；
- 结论：**微信的 SQL 根本不经过系统 `libsqlite3` 的这几个导出符号**。
  佐证：`/Applications/WeChat.app/Contents` 下 141 个 Mach-O，无论 `nm -gU`（导出）还是
  `nm -a`（含 local 符号）都**搜不到 `sqlite3_prepare_v2`** —— 说明 WCDB/SQLCipher 是
  **静态链接 + 符号重命名/剥离**进某个二进制的，按导出名挂钩必然落空（我第一次判断
  "整包没人定义 sqlite3_*，所以走系统库"是**错的**：`nm -a` 同样没有，只是符号被改名了）。

**⇒ caption / aes_key / cdn_url 在本机判定为不可得（不重复尝试）**，原因链：
fav 表在 SQLCipher（`favorite.db` 无 SQLite 头）→ 未走 DB 解密 → 运行时 sqlite 钩子因符号被剥离而无效
→ 三条来源全断。因此 `Persist/<md5>` 也**无法解密成可显示图片**（缺 `aes_key`，而 aes_key 只在 fav 表里）。

**要补这一块，只剩两条路（都不在本机范围）**：
1. 在 SQLCipher 的**内部函数**上下钩（需要先静态定位被重命名的 sqlite 符号，Ghidra 全量 328 MB 不现实）；
2. 提取 DB key（`wechat-use` 一类，本机没有）。
**T4 决策建议**：按"只有 md5"来做——发送用 md5 已足够（§7 的服务端反查），面板显示用
`Persist`/`Thumb` 的原始（加密）文件直接给前端也不可行，故预览就退化为"按 md5 占位 + 首字/序号"。

---

## 3. 发送链路：哪些是确定的

4.1.11 所有 u32/u64 消息发送 = **同一条 mars 大包**（`docs/re/wechat-411-anchors.md`）：

```
MMStartTask 0x5116f30  →  Req2Buf 0x54f14f8  →  LongLink 0x54d5080
```

onebot 的 `script.js` 用"伪造 task 结构 + 替换 cgi 字符串"复用了它，各 kind 的差异只有 3 个数：

| kind | cgi 字符串（script.js 内） | task+0x0c（u32 命令域） | task+0x20（cgi 长度） |
|---|---|---:|---:|
| text | `/cgi-bin/micromsg-bin/newsendmsg` | `0x20a` | `0x20` (=32) |
| img | `/cgi-bin/micromsg-bin/uploadmsgimg` | `0x6e` | `0x22` (=34) |
| video | `/cgi-bin/micromsg-bin/uploadvideo` | `0x6e` | `0x21` (=33) |
| voice | `/cgi-bin/micromsg-bin/uploadvoice` | `0x6e` | `0x21` (=33) |
| file | `/cgi-bin/micromsg-bin/sendappmsg` | `0x6e` | `0x20` (=32) |
| appattach | `/cgi-bin/micromsg-bin/uploadappattach` | `0x6e` | `0x25` (=37) |
| reply | `/cgi-bin/micromsg-bin/sendappmsg` | `0x6e` | `0x20` (=32) |
| **emoji（本次新增）** | `/cgi-bin/micromsg-bin/sendemoji`（**推断**，见 §9） | `0x6e`（**推断**） | `0x1f` (=31，`strlen` 确定) |

【事实】`+0x20` 与 cgi 字符串长度逐一吻合（32/34/33/33/32/37/32），故判定它是 `strlen(cgi)`。
`+0x0c` 只有 text 是 `0x20a`，其余全是 `0x6e` ⇒ 【推断】`0x6e` = "媒体/app 类命令"，
表情大概率同域，但这一个常数**没有静态证据**，是本次最大的未知量（见 §9 第 1 条）。

payload 侧（`triggerX1Payload`）：`+0x18` 写 cgi 指针、`+0x08/+0xb8/+0x190` 三条自引用回填，
`blr x8` 命中点（`script.js` `attachBlrX8Hook`）把 `autoBufferWrite` 的 protobuf 字节替换成 Go 传入的
`protoHex`——**即"发什么二进制由 Go 侧 protobuf builder 决定"，与 kind 无关**。

---

## 4. 与"发图片"的协议层差别（就是 T4 要回答的那几条）

| 维度 | 图片（img，已跑通） | 表情（emoji） | 证据 |
|---|---|---|---|
| mars 大包 | `MMStartTask 0x5116f30` | **同一入口** | 锚点表；script.js 共用 `triggerSendMediaMessage` |
| cgi | `uploadmsgimg`（reqid=9，短连 `nettype="1"`） | **`sendemoji` reqid=68 `nettype="3"`（长连）**，运行时已证实聊天表情走它（§2.6） | `0x8c846ff` / `0x8c84c66` |
| 消息类型字段 | 图片消息走 uploadmsgimg 一包完成"上传+发消息" | 表情是否也一包完成未知；表里另有 `newsendmsg`(237) 可能承载 type 47 | §2.1 |
| 媒体上传 | mmcdn：`cdnuploadimgprepare/commit` + `uploadmsgimg` | **同一套**，只是 CDN 任务里多 `emojimd5` 字段 | `0x5405ec0` 字段表 |
| xml 节点 | 收侧 `<img>`（onebot `param.go:245`） | 收侧 `<emoji>`（onebot `param.go:233`），发送侧模板**未命中** | §2.5 |
| 特有字段 | aeskey/md5/len/width/height | **多出** externurl / externmd5 / thumburl / encrypturl / productid / tpurl / tpauthkey / attachedtext / attachedtextcolor / emojiattr / lensid / designerid / idbuffer / desc | `param.go:284`（24 属性） |
| 会话内表现 | 可点开大图 | 直接当表情渲染；`emoticon.db` 单独一张表存表情索引 | `0x7eeb555 'emoticon'` / `0x7eeb55e 'emoticon.db'` |

---

## 5. 表情图片 CDN 上传（问题 3 的落地答案）

**走现有 upload 通道，不新增 cgi。** 理由（事实）：

1. CDN 任务统计函数 `0x5405ec0` 同时打印 `emojimd5` 与 `filemd5/thumburl/midimgmd5` ⇒ 同一任务对象。
2. 全局只有一套 `prepare/commit/uploadmsgimg/mmuploadmedia`，没有 `uploademoji`（未命中）。
3. 表情上传的**参数结构**与图片一致（`render/upload` X1 的 `+0x48 id / +0x68 receiver / +0xa8 md5 /
   +0xe8..+0x148 path / +0x200 aeskey`，见 `script.js fillUploadX1AndStart`），
   表情只需把 `md5` 换成**表情文件 md5**、`id` 前缀仍为 `receiver_ts_rand_1`。

【推断】上传返回里，服务端给回的正是 `cdnurl / thumburl / externurl / encrypturl`（也就是收侧那
4 个 url 属性名），`md5 / len / aeskey / width / height` 是客户端自己产生的（见 §7）。
没有静态证据表明存在"emoji 专用上传字段"——除 `emojimd5` 这个**统计字段名**。

---

## 6. 表情 xml（收侧）属性全集（直接来自可工作的解析器）

`onebot/param.go:283-317` 的 `Emoji` struct（接收侧已在生产使用，等价于
`ParseMessageXml 0x371744c` 认识的属性集合）：

```
fromusername tousername type idbuffer md5 len productid
androidmd5 androidlen s60v3md5 s60v3len s60v5md5 s60v5len
cdnurl designerid thumburl encrypturl aeskey externurl externmd5
width height tpurl tpauthkey attachedtext attachedtextcolor
lensid emojiattr linkid desc
```

其中在 dylib 里被独立证实的属性名字面量：`thumburl`(`0x8cacee7`)、`encrypturl`(`0x7eeb490`)、
`attachedtext`(`0x7eeb4c0`)、`tpurl`(`0x7eeb3f0`)、`productid`(`0x8cbfa5e`)、`cdnurl`(池 `0x7eeb3b0`)、
`externmd5`(池 `0x7eeb3c0` 片段 `externmd`)、`width`/`height`(池 `0x7eeb4a0/0x7eeb4b0`)。

---

## 7. 发送必需字段清单（问题 4）

按"能不能本地算"分三类。**左列是事实、右列"必须"是推断**（发送侧无静态证据，见 §9）。

### A. 本地可算（无服务端依赖）

| 字段 | 来源 / 算法 |
|---|---|
| `md5` | 文件内容 MD5（32 位小写 hex） |
| `len` | 文件字节数（十进制字符串） |
| `aeskey` | 32 位 hex 随机（`generateAESKey()` 同款），上传前生成、与 CDN 上传共用 |
| `width` / `height` | 图片尺寸（png/gif/jpeg/webp 头部解析，T2 的 `stickerImageSize`） |
| `fromusername` / `tousername` | 会话双方 wxid |
| `type` | 【推断】自定义表情类型常量，见 §9 第 3 条 |

### B. 必须服务端给（上传 / 会话协商的返回，本地造不出来）

| 字段 | 何时产生 |
|---|---|
| `cdnurl` | CDN prepare/commit 之后 |
| `thumburl` | 同上（服务端回缩略图地址） |
| `externurl` + `externmd5` | 表情专用外链（部分表情才有；收侧优先 cdnurl→thumburl→externurl 兜底） |
| `encrypturl` | AES 加密后的下载地址 |
| `productid` / `designerid` / `lensid` / `tpauthkey` / `emojiattr` | 表情商店/付费表情元数据，服务端下发 |
| `idbuffer` | 服务端给的表情唯一 id |

### C. 可选（有则更像"真表情"，无则由服务端/客户端补）

`attachedtext` / `attachedtextcolor`（表情附带文字）、`desc`、`linkid`、`s60v3*/s60v5*/android*` 兼容 md5。

> **对 T4 的硬结论**：一条**能发出去**的表情消息，`md5 + len + aeskey + width + height` 由本地产生后
> 走 CDN 上传，**必须拿到服务端回的 `cdnurl`（或 `externurl`）才能组成消息**。
> 若 T4 不能拦截上传返回，就只能退化成"降级发图片"（CONTRACT §T4 已允许）。
> 拦截上传返回的现成钩子已存在：`script.js` 的 `uploadOnCompleteAddr` / `cndOnCompleteAddr`
> （`uploadOnCompleteFuncAddr` 按 fileId 分流处，需再加一条 emoji fileId 分支）。

---

## 8. 表情包（pack / 商店表情）vs 单张自定义表情

**未命中**：`emojipack`、`EmojiPack`、`packid`、`packthumb`、`uploademoji`、`storeemoji`、`lensid`、
`tpauthkey` 在整份 dylib 里 0 次。

**命中但与"发消息"无关**：

| 字符串 | vaddr | 含义 |
|---|---|---|
| `MM_FAV_DATA_TYPE_EMOJI` | `0x7e74190` 一带（`__const`，与 `MM_FAV_DATA_TYPE_COLLECTION`/`…_FINDER_*` 同表） | 收藏域类型枚举（"收藏的表情"） |
| `EmojiToFav` / `AddEmojiToFav` | `0x93cd558` / `0x93c35f3`（`__DATA.__data`，Qt 元对象方法名） | UI 动作"添加到我的表情" |
| `EmoticonMsgInfo` / `SendEmoticon` | `0x7cc73d0` / `0x93ba699` | 内核侧表情消息/发送的类型名（Qt/RTTI 表） |
| `emoticon` / `emoticon.db` | `0x7eeb555` / `0x7eeb55e` | 独立 sqlite 表/库名（表情索引） |
| `operate_emoticon`（tag） | `0x7cc2d30` → `0x261072c` | "我的表情"增删操作（`sendemoji` cgi 的最可能调用者） |
| `upload_emoticon.StartUploadEmoticon`（tag） | `0x7cc33f0` → `0x26165e0` | 表情上传入口 |

**结论**：协议层**没有**给"表情包内单张"另开字段的证据；`sendemoji`(68)/`operate_emoticon` 这一组
更可能是**"把表情加入/同步到我的表情"**（收藏域），而不是聊天发送。
聊天侧已由运行时证实走 `sendemoji`（§2.6），与"表情入库"共用一个 cgi 家族，但**具体请求体
（pb）未拿到**，见 §10.3。

---

## 9. 未验证 / 未命中（必须单列的坑）

### 9.1 未命中（明确扫不到，禁止编造替代品）

| 目标 | 扫描方式 | 结果 |
|---|---|---|
| `<msg><emoji` | 全文件字节 find | 0 |
| `<emoji` | 全文件字节 find | 0 |
| `<msg>` / `<msg><` | 全文件字节 find | 0（只有碎片 `<msg` @`0x7ce6200`，无 xref） |
| `emojiattr` | 全文件 | 0 |
| `externurl`（连写、小写） | 全文件 | 0（池里只有 `externur`+`externmd` 片段） |
| `externmd5`、`androidmd5`、`designerid`、`lensid`、`tpauthkey` | 全文件 | 全 0 |
| `uploademoji` / `EmojiUpload` | 全文件 | 0 |
| `emojipack` / `EmojiPack` / `packid` / `packthumb` | 全文件 | 0 |
| `WxSendImgMsg` / `ImgMsgHeader` / `EmojiMsg` / `proto3` / `FieldDescriptorProto` | 全文件 | 全 0 ⇒ **无 protobuf descriptor 可静态恢复**（现有 `proto/*.proto` 是运行时抓的，不是从 dylib 读出来的） |
| `aeskey=` / `cdnurl=` / `md5=` 形式的 xml 片段 | 全文件 | `aeskey=`/`cdnurl=` 0；`md5=` 7 处但均非表情模板 |

### 9.2 未验证（有间接证据，但不足以定论）

1. **`task+0x0c` 常数**：`script.js` 里 media 类全用 `0x6e`、text 用 `0x20a`；
   **运行时实测真实任务的该字段是 `0x0`**（heartbeat/newsync/**sendemoji** 全部 `cmd=0x0`）。
   结论：`+0x0c` 不是"cgi 选择器"，填 `0x6e` 只是现有 6 条流水线的既有做法。
   ⇒ T4 **沿用 `0x6e`（已被现网验证能发出去）**，不要为了"贴合真实值"改成 0。
2. ~~表情聊天走哪条 cgi 未定~~ → **已关闭**：运行时证实走 `sendemoji`，见 §2.6。
3. **`type` 属性值**：`<emoji type="…">` 的取值（自定义/商店/包）无静态证据。
4. **发送是否需要 xml**：微信 4.x 的 `<img>`/`<emoji>` xml 很可能是**服务端生成**（收侧才解析）；
   客户端只发结构化 protobuf。若成立，则 T4 **不需要自己拼 xml**——但这是推断。
5. **`<msg` 碎片池（`0x7ce6200`）归属**：池被 `0xAA` 填充 + 交叉重叠，无法把 `<msg` 绑到具体函数。
6. **`<appmsg`/appmsg 侧 3 个函数（`0x3740c7c`/`0x370f1c4`/`0x3745724`）** 是解析还是构造，未反编译确认。

---

## 10. 要让 T4 真正跑通，还差哪些"一次运行时样本"

静态到 §9.2 就到顶了。缺的三件事，**任取其一即可闭环**：

1. **用户手动发一张表情**（唯一需要用户的操作；不要由 agent 代发），同时用只读 hook 记录：
   - `Req2Buf 0x54f14f8` 出口处 `payload+0x18` 指向的 cgi 字符串；
   - task 结构 `+0x0c` / `+0x20`；
   - `blr x8` 里 `autoBufferWrite` 收到的 **protobuf 原始字节**（`script.js` 已有该钩子，只需加
     `sendMsgType === "incoming"` 的只读分支，不替换 bytes）。
   拿到这三样，`BuildEmojiMsgProto` 的字段号/顺序与 cgi 就**全部确定**。
2. 或者：hook `upload_emoticon`（`0x26165e0`）/ `operate_emoticon`（`0x261072c`），在用户
   "添加表情到我的表情"时记录请求体 → 只能拿到 upload 侧字段，**拿不到聊天 cgi**。
3. 或者：hook `ParseMessageXml 0x371744c` 的入参，拿到**任意一条表情消息的完整 xml**（含全部属性名与
   取值形态）→ 能把 §6 字段表钉死（现在只有 onebot 的经验表），但仍给不出 cgi。

> 本机 `task_for_pid` 被禁，但 **FridaGadget 在 `127.0.0.1:27042`（进程名 `Gadget`）可连**，
> 上面 3 条都能做。**注意：禁止 reload 现有注入脚本**（会把用户正在跑的 agent 打断），
> 只能用 `frida -H 127.0.0.1:27042 -n Gadget -l <只读脚本>` 叠加。

### 10.2 只读抓包手册（本次实际用过的）

脚本（都在 `/private/tmp/wxre/`，可整体搬进 `docs/re/scripts/`）：

| 文件 | 作用 |
|---|---|
| `capture_sendemoji.js` | Frida agent：只读钩子（不改寄存器/内存/返回值）。挂 `MMStartTask.wrapper 0x5120fd8`、`MMStartTask.main 0x5116f30`、`Req2Buf 0x54f14f8`、`ParseMessageXml 0x371744c` |
| `capture_run.py` | 宿主：用 Frida **Python API** 守会话（CLI 在无 TTY 后台会加载完立刻退出） |
| `xref2.py` / `refs_in.py` | 静态：`adrp` + 任意 `add/ldr`（rd≠rn）引用扫描 / 函数内字符串清单 |

运行：

```bash
# 1) 起会话（后台常驻，Ctrl-C 或 kill 才 detach）
python3 /private/tmp/wxre/capture_run.py
# 2) 日志（每行一条，grep 友好）
tail -f /private/tmp/wxre/dump-req.log
#    事件类型：CHECK / SEND / WRAP / STRUCT / EMO / MD5CAND / TICK / PARSEXML
# 3) 让用户手动发一张表情（不要由 agent 代发）
grep 'sendemoji' /private/tmp/wxre/dump-req.log
```

**四个坑（都踩过，按此写脚本可省 1 小时）**：

1. **`Process.findModuleByName("wechat.dylib")` 会命中错模块**：
   `/Contents/Frameworks/wechat.dylib` 是 16 KiB 同名桩，真正的主逻辑是
   `/Contents/Resources/wechat.dylib`（运行时 base `0x11f604000`，size 150,126,592）。
   必须按 `path` + `size > 100 MB` 双条件选，否则 adrp 换算全错。
2. **Frida CLI 在后台/无 TTY 下加载完即退出**（`frida -H … -l x.js -q`）：必须用 Python API
   （`dev.attach()` + `script.load()` + 主循环 sleep）守会话，否则你以为挂着、其实钩子早就掉了。
3. **`readUtf8String()` 遇到字符串后的 `0xAA` 填充会抛异常**：cgi 名恒为空。
   必须按字节读到 `\0` 再 decode（脚本里 `readAsciiC()`）。
4. **`Req2Buf 0x54f14f8` 在 live 路径上 0 次调用**（见 §10.3），别把锚点表当"运行时必中"。

### 10.3 发侧 protobuf 顺序：**未拿到**（T4 的唯一硬风险）

试过且排除的路径：

| 尝试 | 结果 |
|---|---|
| 钩 `Req2Buf 0x54f14f8` 拿序列化结果 | **0 次调用** —— 真实入口是 profile JSON 里的 `0x3e58e8c`（§2.7），文档锚点那个地址在 live 路径上根本不执行 |
| 钩 `autoBufferWrite 0x3E7FA8C`（真实序列化写） | **有效**：20 秒自然流量抓到 8 条明文 pb（首条 `len=404`，可干净解析）；**但 `sendemoji` 那一次 ±8 s 内没有任何 `PBODY`** ⇒ 表情请求体不走这个写函数 |
| `Req2Buf` 出口扫 task 结构里的 `{ptr,len}` 候选 | 命中的是**代码段指针**（`0x121ffd40c` 落在 `r-x`，内容为 ARM64 指令），非请求体 |
| `MMStartTask.wrapper` 出口扫 `msg[0..0x400)` 全部可读指针 + 递归叶子 | 叶子全是代码/字符串池；**任务结构里不持有 pb 缓冲**（body 在调用期间写进 per-call AutoBuffer，调用后释放） |
| 事后扫驻留对象 256 KiB（0x175ad25f0 起）找 md5/pb | 0 命中（对象复用 + 缓冲已释放） |

**已拿到的替代证据**：同一 `sendemoji` 事件的 `msg+0x18` 关联字符串池里出现 4 个 32-hex
（`0302e5974743d8a463f64edfacda7fa2`、`7983913aa1eeb61e1147034a85cae41a`、
`36e7c83728a3956fae17ef31662f3eac`、`14e731bb7534277907aabfe3e91a160b`），
其中 `0302e5…` 在多次发送中重复出现 —— 与"用户同一张表情重发"吻合，
**判为表情 md5**【推断，未与服务端回显的 `<emoji md5=…>` 逐字比对过】。

**要闭合这块，只差一步**：拿到 `Req2Buf`/`autoBufferWrite` 的**实际运行时地址**（Go 侧配置里那个），
在它入口加一行 `log(hex(x2), x3)` 即可（`script.js` 的 `attachBlrX8Hook` 已经在同一个点上，
只需再加一个"只观察不替换"的分支）。**这不是新能力，是把现成钩子复用一次。**

### 10.4 `autoBufferWrite` 抓包手册（本轮实际用的）

```python
# capture_sendemoji.js 里（v9）新增的关键钩子；地址来自 wechat_version/4_1_11_53_mac.json
addr = Process.findModuleByName('wechat.dylib').base.add(0x3E7FA8C)
Interceptor.attach(addr, { onEnter: function () {
    var data = this.context.x1, len = this.context.x2.toUInt32();   // (AutoBuffer*, data, len)
    if (len < 16 || len > 4096) return;
    var r = Process.findRangeByAddress(data); if (!r || r.protection.indexOf('x') !== -1) return;
    log('PBODY t=' + Date.now() + ' len=' + len + ' hex=' + hex(data, len));
}});
```

```bash
python3 /private/tmp/wxre/capture_run.py        # 守会话（别用 frida CLI，见 §10.2 坑 2）
grep '^PBODY' /private/tmp/wxre/dump-req.log | awk -F'len=' '{print $2}' | awk '{print $1}' | sort -n | uniq -c
grep '^SEND'  /private/tmp/wxre/dump-req.log | grep sendemoji
python3 /private/tmp/wxre/pbparse.py --from-log /private/tmp/wxre/dump-req.log --len 175   # 字段树
```

**本轮结论**：`autoBufferWrite` 对 heartbeat/newsync/report 一类**有效**（抓到 50/288/330/404/416 等
多种长度），但 **`sendemoji` 的 175 字节请求体不经过它**——那条长连消息体由另一条路径组装。
下一步该 hook 谁（技术建议，按收益排序）：

1. **`sendemoji` 的 builder 本体**：在 `req2bufEnterAddr 0x3e58e8c` 附近按 `cgi 表下标/reqid 68`
   找分派（跳转表 `adrp+ldr+b` 三连），命中分支即 builder；
2. `MMStartTask.main 0x5116f30` 的 `x0`（msg 结构）所在的 **`AutoBuffer` 成员**：实测 `msg+0x04`
   就是 body 长度（`0xaf`=175），可顺着 `msg` 往上找持有该长度字段的容器（`wrapper` 的 `x0`
   单例 `0x9f3490b00` 是下一个候选，我扫过它内部 256 KiB 但没有 md5/pb 残留）；
3. 或者直接在 `wrapper` 出口对**整个 rw 堆**做「16 字节 raw md5」扫描（注意：pb 里 md5 大概率是
   **16 字节裸值**，不是 32 位 hex 字符串——本轮我先按 32-hex 找，方向错了）。

---

## 11. 复现命令（全部离线、不改 /Applications）

```bash
# 0) 取 arm64 切片（只读，产物放 /private/tmp）
lipo -thin arm64 -output /private/tmp/wxre/wechat.arm64 \
  /Applications/WeChat.app/Contents/Resources/wechat.dylib
ls -l /private/tmp/wxre/wechat.arm64     # 期望 158,180,992

# 1) cgi 表（本页 §2.1）
python3 /private/tmp/wxre/xref2.py /private/tmp/wxre/wechat.arm64 --str sendemoji receiveemoji

# 2) 表情属性池 + 引用它的函数（§2.2）
python3 /private/tmp/wxre/xref2.py /private/tmp/wxre/wechat.arm64 --sect __const 0x7eeb300 0x7eeb600

# 3) CDN 层 emojimd5 字段表（§2.3）
python3 /private/tmp/wxre/refs_in.py 0x5405ec0 0x5407000

# 4) 表情模块源码锚点（§2.4）
python3 /private/tmp/wxre/xref2.py /private/tmp/wxre/wechat.arm64 \
  --str emoji_utility.cc get_emoticon.cc emoticon_key.cc upload_emoticon

# 5) 未命中复核（§9.1）
python3 /private/tmp/wxre/scan2.py /private/tmp/wxre/wechat.arm64 \
  '<msg><emoji' '<emoji' emojiattr externurl uploademoji emojipack proto3

# 6) 定点反汇编（§2.2 的 0x43fc6d4）
rizin -q -N -e scr.color=0 -c 's 0x43fc6d4; af; pdf' /private/tmp/wxre/wechat.arm64
```

`xref2.py` / `refs_in.py` 建议并入 `docs/re/scripts/`（旧 `xref_scan.py` 的 rd==rn 限制会漏掉
本文的关键引用）。

---

## 12. 对 `script.js` 的改动（本次交付的另一半）

新增 `triggerSendEmojiMessage(taskId, sender, receiver, protoHex, payloadHex)`，注册进 `rpc.exports`；
实现方式与 `triggerSendVoiceMessage`/`triggerSendImgMessage` 完全同构（都转发到
`triggerSendMediaMessage(..., "emoji")`），另加：

- 全局：`emojiCgiAddr` / `sendEmojiMessageAddr` / `emojiMessageAddr` / `emojiProtoHexGlobal` /
  `emojiMessageCallbackFunc`；
- `setupSendImgMessageDynamic()` 内追加 emoji 段：cgi=`/cgi-bin/micromsg-bin/sendemoji`、
  `+0x0c = 0x6e`（假设，代码里已注明）、`+0x20 = 0x1f`（= `strlen`，确定）；
- `attachBlrX8Hook` / `attachReq2buf` / `msgAddrInfo` 各补一条 `emoji` 分支。

**假设全部写在脚本注释里**（`ASSUMPTION-1/2` 标记），并集中在 `EMOJI_CMD_DOMAIN` 一个常量上：

| 项 | 取值 | 依据 |
|---|---|---|
| cgi | `/cgi-bin/micromsg-bin/sendemoji` | 【运行时已证实】§2.6 |
| `+0x20`（cgi 长度） | `0x1f` = 31 | `strlen(cgi)`，与现有 6 条流水线逐一吻合 |
| `+0x0c`（`EMOJI_CMD_DOMAIN`） | `0x6e` | 沿用 img/video/voice/file/appattach/reply 的既验证值；**真实微信任务是 `0x0`**（§2.6），若发送失败先试改成 `0` |

**PB 未闭合**：`protoHex` 由 T4 的 `BuildEmojiMsgProto` 生成，字段号/顺序仍是 §10.3 的未解项。

---

## §2.12 收藏表情原图解密（外部方案，未在本机验证）

> 来源：[CN-Grace/Wechat-Emoticon-Parser](https://github.com/CN-Grace/Wechat-Emoticon-Parser) `v4.0-plus`（MIT）。
> 该项目目标是 **Windows 4.1.x**；下面是其中与 macOS 无关的**算法**部分，本机尚未跑通（原因见末尾）。

### 加密参数

`business/emoticon/` 下 `Persist` / `PersistStore` / `Thumb` / `ThumbStore` 四个目录
共用**同一把密钥**（文件名为内容 md5）：

```
算法: AES-128-CBC + PKCS7
key = IV = md5( f"{seed}{wxid}EMOTICON" ) 的 hex 解码后的前 16 字节
```

| 参数 | 来源 |
|---|---|
| `seed` | 微信进程内存里的账号级常量，**8~10 位十进制数字串**，位于主进程堆（wxid 字符串附近） |
| `wxid` | 数据目录名去掉 `_b487` 之类后缀 |

验证方式：拿任意表情文件的前 16 字节（C0）用候选 key 做 AES-128-CBC(key=IV) 解密，
首 4 字节命中魔数即确认 —— `89 50 4e 47`(PNG) / `47 49 46 38`(GIF) / `ff d8 ff`(JPEG) / `wxgf`。

### 与本文前述"aes_key 拿不到"的关系

之前判断"必须从 fav 表取 `aes_key` 才能解密"是**不完整**的：
fav 表的 `aes_key` 是**每条表情各自的**密钥；而磁盘缓存这一层用的是**账号级派生 key**。
所以不需要 fav 表、不需要 SQLCipher 解密，只要有 `seed` 就能批量还原全部收藏表情原图。

### 附带能力（同源方案）

- `PersistStore` 容器文件 = 多个表情按魔数连续拼接，需按 `PNG→IEND` / `GIF→0x3B` / `JPEG→EOI` 流式分割
- `wxgf` 文件 = 魔数后是裸 H.265 流（从 `00 00 00 01 40 01` VPS 处截取），需 ffmpeg 取首帧转 GIF
- 聊天图片 V2 `.dat`：`key = md5(f"{seed}{wxid}")[:16]`（16 字符 ASCII），AES-128-ECB + 明文区 + 单字节 XOR 尾部

### 本机未跑通的原因

`seed` 需要从**运行中的微信进程内存**里扫（该项目的 Windows 实现用 `ReadProcessMemory`）。
macOS 上等价做法是 Frida 扫 `rw-` 区段找数字串，但本次尝试后 **Frida gadget 的脚本注入卡死**
（进程与 27042 端口仍在，`create_script` 超时），需要重启微信恢复 —— 因此未取得 `seed`。

恢复后按此流程即可（本机已验证可用的工具链）：

```
1. Frida attach Gadget，enumerateRanges('rw-')，分块读（每块 ≤32MB，避免超时）
2. 正则 (?:^|[^0-9])([1-9][0-9]{8,9})(?:[^0-9]|$) 收集候选
3. 对每个候选算 key = md5(f"{seed}{wxid}EMOTICON")[:16]
4. 用某个 Persist 文件前 16 字节验证魔数
5. 命中后用这把 key 批量解密全部 Persist / Thumb
```

⚠️ 扫描时注意：**不要在 Frida 脚本里做长时间同步循环**（本次就是全量扫内存导致注入超时、会话卡死）。
必须用 `setInterval` 分块，或改成在 Python 侧逐块驱动。
