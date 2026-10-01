# 邮件需求：2026-09-30 夜 这一轮的实测与修复

> 目标：定时/手工收信 → 清垃圾 → 发票采集整理下载 → 飞书推送/共享汇总 → 重要邮件提醒 → A4 网格导出 → 账户配置 LWW 同步。
> 本轮不写「已完成」，只写**有对照证据**的部分；未验证项单列在 §5。

## 1. 起点：把目标逐条对到代码

先用只读盘点把 8 条需求映射到 `file:line`，结论里有三条是「看着有、其实没接上」：

| 需求 | 盘点结论 |
|---|---|
| 1 每天定时处理 | **`SetPipelineRunner` 全仓零调用点**，`POCKET_EMAIL_PIPELINE_HOUR` 读进来没人用 → 每日流水线从未启动 |
| 3 发票采集 | 链路齐全，但「金额只印在附件里」的邮件会被规则层提前丢弃（§3.2） |
| 5 A4 网格导出 | 只吃 PDF；采集端却允许 jpg/png/webp；畸形附件直接 panic |
| 8 配置 LWW | 服务端 `UpdateAccountScoped` 无条件覆盖，离线客户端会冲掉服务端新配置 |

## 2. BUG-AM：每日定时流水线是死代码（需求 1 的硬缺口）

**证据**：`grep SetPipelineRunner` 只有定义没有调用点；`EmailPipelineHour` 只有一个消费者都没有。
后果：清垃圾 / 重要提醒 / 发票采集 / 飞书推送**全都只能手动** `POST /api/email/pipeline/run`。

**根因**：`cmd/pocketd` 里 `emailScheduler.Start()` 在 L458，而 `srv`（流水线依赖的持有者）在 L723 之后才构造完。
`Start()` 只在「调用 SetPipelineRunner 时 runner 已注入」的情况下起 loop，顺序反了就永远不起。

**修复**：
- `email.Scheduler` 记录 `started/startCtx`，`SetPipelineRunner` 在 Start 之后被调用时**补起** loop；
  `pipelineOnce` 保证重复注入不重复跑（否则一天两轮、飞书重复推送）。
- `pipelineLoop` 改用可注入时钟 `s.now()`，单测能把触发点拉到几百毫秒内。
- `cmd/pocketd/main.go:723` 注入 `srv`（`*server.Server` 实现 `email.PipelineRunner`）。

**验证**
- 单测 `TestPipelineLoop_*` 4 例 ×3 轮：Start 前/后注入都到点真跑、hour<0 不排期、重复注入只跑一次。
- 真实进程日志（:8099 实例）：

  ```
  [email/scheduler] daily pipeline runner injected (hour=6)
  [email/scheduler] pipeline scheduled at 2026-10-01T06:00:00+08:00
  ```

  `POCKET_EMAIL_PIPELINE_HOUR=6` 生效（默认值 8 也验证过）。**到点真的执行**由注入时钟的单测保证，
  没有等到凌晨 6 点。

## 3. 发票链路的两个真缺陷

### 3.1 BUG-AN：中文字体探测漏了 Windows/Android

`FindChineseFont` 候选表只有 macOS/Linux，Windows 开发机与 Android 设备一律探测失败 →
`RenderInvoiceXMLPDF` 报错 → **XML 发票分支恒为 failed**。补 `systemFontCandidates()`：
`C:\Windows\Fonts\{simhei,Deng,simfang,simkai,NotoSansSC-VF}.ttf`（走 `SystemRoot`，不硬编码盘符）
+ `/system/fonts/DroidSansFallback.ttf`。
单测 `TestSystemFontCandidates_*` / `TestFindChineseFont_ThenRenderXMLInvoice` 本机真跑通（探到 simhei）。

### 3.2 BUG-AO：金额只在附件里的账单邮件被提前丢弃

实测（IMAP 夹具，主题「9 月度对账单（附件 + 内嵌图表）」、正文只有一句「见附件」）：

```
POST /api/email/pipeline/run → 200  invoices={"Processed":0,...}   发票列表 0 条
POST /api/emails/invoices/extract → {"matched": false}
```

根因：`ExtractInvoice` 末尾「金额=0 且发票号为空 → 丢弃」这道防误伤门槛，在**采集器看到附件之前**
就执行了，于是 `harvestOne` 的 PDF 附件分支永远没机会跑。

修复：`ExtractInvoiceLoose(e, bodyText, hasInvoiceAttachment)` —— 只有在**确实带着** PDF/图片/XML 附件
时才破例建档；无附件的营销「账单提醒」依旧丢弃（`TestExtractInvoiceLoose_PureMarketingMailStillDropped`）。
流水线二次提取时把 `HasInvoiceAttachment(parsed.Attachments)` 一起传进去。

### 3.3 BUG-AP：畸形 PDF 让导出接口 panic 成 500

69 字节的退化 PDF（有 Catalog、无页树）走到 pdfcpu 合并会 panic
`slice bounds out of range [-1:]`，HTTP 日志里直接是 `[PANIC] POST /api/emails/invoices/export`。
注意 `api.ValidateFile` 对这种文件是**放行**的，所以修复不是加 Validate，而是「确认至少有 1 页」
（`pdfPageCountSafe`，自带 recover）。坏文件跳过、好文件照常导出，全坏时回 **400**（`ErrNoUsableInvoiceFile`）
而不是 500，并在响应里如实回报 `count`（真正入网格的张数）与 `skipped`。

## 4. A4 网格导出（需求 5）

- **图片发票现在也占格**：jpg/png/gif 直转单页 PDF，webp 用 `x/image/webp` 解码再转 PNG
  （`normalizeInvoiceFilesToPDF` / `imageFileToA4PDF`）。以前采集端收图片、导出端只吃 PDF，选 3 张含 1 张图片就只剩 2 张。
- `ExportInvoiceGridDetailed` 返回 `GridExport{Path, Count, Skipped}`，handler 按名字回填 `MarkInvoiceExported`——
  跳过的坏文件不会被标成「已导出」。
- 前端 `InvoiceListView` 补 2×2 / 3×3 可选（原来硬编码 2×2）。
- 单测断言的是**产物**不是「没报错」：2×2/4 张 = 1 页 A4、5 张 = 2 页、3×3/5 张 = 1 页、
  12 格 = 3 页、页尺寸 = 595.28×841.89pt、PDF+png+jpg 混合进网格、畸形件跳过。

## 5. 配置 LWW（需求 8）

`UpdateAccountLWTScoped(ctx, a, uid, ws, cred, updateCred, baseUpdatedAt)`：
- `baseUpdatedAt > 0`：`WHERE ... AND updated_at <= $base`，写入值取 `max(now, base+1)` 保证单调；
- 0 行时区分 **ErrNotFound**（不存在/越权）与 **ErrStaleWrite**（存在但版本过期）；
- `baseUpdatedAt <= 0`：退化为旧行为，老客户端不受影响；
- HTTP 层 `PUT` 新增可选 `updatedAt`；409 响应带上服务端当前 `updatedAt`。
- 前端上行时带的是**本地**那份的 `updatedAt`（原来带的是服务端的，等于没有守卫）；
  收到 409 走行覆盖，不进 outbox 干等。

真库验证（`POCKET_TEST_POSTGRES_DSN` 指向本机 PG，隔离 schema）4/4：
新基准写入成功且时间戳递增 / 旧基准被拒且不动那一行 / 越权仍是 404（不被守卫变成 409）/ 无版本号保持旧行为。
HTTP 侧 `verify-email-pipeline-run.mjs` 9/9（409 → 200 → 再用旧基准又 409）。

## 5b. 共享台账：从「本地文件」升级为飞书电子表格

需求原文：「这些文件如果没有办法发送，可以建立共享文档及文件，进行整理，
需要整理一个列表，记录必要信息并汇总金额。」

**原状**：`BuildInvoiceSummaryDocs` 只在设备本地写 CSV/MD。那是**本机文件**，
不是共享文档——别人拿不到，也不算「建立共享文档」。

**新增**（端点先查官方文档核实，不凭记忆写）：
- `feishu.CreateSpreadsheet` → `POST /open-apis/sheets/v3/spreadsheets`（元数据走 v3）
- `feishu.FirstSheetID` → `GET  /open-apis/sheets/v3/spreadsheets/{token}/sheets/query`
- `feishu.WriteValues` → `PUT  /open-apis/sheets/v2/spreadsheets/{token}/values`（单元格读写走 v2）
  请求体 `{"valueRange":{"range":"<sheetId>!A1:I<n>","values":[[...]]}}`
  —— 元数据 v3、数据 v2，两版字段风格不同，混用会 400。
- `email.LedgerPublisher` 接口 + `Pipeline.PublishLedgerScoped`：
  表头 + 每张票一行 + **独立合计行**（合计单独占行，能在表里直接求和，不只是文字里提一句）。
- 接线：`Pipeline.Run` 每轮收尾发布一次；`GET /api/emails/invoices/summary` 与
  `POST /api/emails/invoices/push` 返回 `shareDocUrl`；前端发票页在有链接时显示「共享台账」入口。
- 本地 CSV/MD **始终生成**（离线兜底 + 对账留存），飞书不可用不会让流水线记 error。
- 新配置 `POCKET_FEISHU_INVOICE_FOLDER_TOKEN`（台账建在哪个云空间目录，空=根目录）。

**验证**：`feishu` 包 httptest 6/6（端点、方法、请求体形状、错误冒泡、空值不发请求）；
`email.Ledger*` + `TestPublishLedgerScoped_*` 真库 6/6（表头列、合计行、范围换算、
发布成功回传 URL、发布失败冒泡、空台账不建表、不可用不报错）。

**⚠️ 未在真实租户验证**：本地没有 `POCKET_FEISHU_APP_ID/SECRET`，
所以这套表格接口只在假服务器上跑过。首次接入真实飞书时需要人工确认一次
（应用需开通「查看、评论、编辑和管理电子表格」权限，否则 1310213 Permission Fail）。

## 6. 端到端实测（真实进程 + IMAP 夹具）

`node scripts/verify-email-invoice-e2e.mjs` → **12 PASS / 0 FAIL**：

```
流水线 → 200：synced=2 newEmails=14 invoices={"Processed":2,"Downloaded":2}
发票 4 条
  其他-云服务开票中心-1280.00-2026-09-28.pdf   amount=1280  src=attachment
  其他-财务部-0.00-2026-09-30.pdf               amount=0     src=attachment
单张原件可下载 200 / 1537B / %PDF
A4 2x2 → 200 count=2 skipped=[2 个畸形件] 下载 5104B %PDF
A4 3x3 → 200 count=2 skipped=[2 个畸形件] 下载 5105B %PDF
汇总：count=4 合计=2560，共享文档 CSV/MD 已生成
```

命名 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 由真实采集产物证明，不是推断。

**夹具的两个坑（都属于测试基础设施，不是产品缺陷，但要记）**
1. IMAP 的 UID 一旦分配不可变。新邮件**只能追加到夹具末尾**；插在中间会让后面邮件 UID 位移，
   客户端按 `last_synced_uid` 增量拉取时会把新邮件正文写进旧行（实测出现「主题是入职材料清单、
   正文是发票邮件」的错位）。
2. 每个验证轮次用**独立账户**（`invoice-fixture@example.com`）：`last_synced_uid` 按账户记，
   复用旧账户会带上历史 UID 状态。
3. 原先那份 69 字节退化 PDF 附件是上一轮留下的，它让「发票导出」在夹具里永远验证不到真实产物；
   现已换成真格式 PDF（`gen_fixture_invoice_test.go` 生成 base64，`inject-fixture-invoice-pdf.mjs` 注入）。

## 7. 测试基线对照

本机 Windows 上 email 包有两个**改动前就存在**的失败，已用独立 worktree 跑 HEAD（bf38264）对照确认与本轮无关：

- `TestFetchPOP3MailboxAuthRejected`：stub 写 `-ERR` 时连接被本机中止（`wsasend: ... aborted`）。
- `TestWriteKeyAtomic_CreatesFileWithCorrectMode`：文件权限位语义在 Windows 上不成立。

排除这两条后 `go test ./internal/email/...` 全绿；`go build ./...` 通过；前端 `npm run typecheck` 无错。

## 7b. 真实邮箱接入（2026-10-01 凌晨补做，6/6 账户连通）

之前所有证据都来自夹具。这一轮用目标里给的 6 个真实账户各做了一次**只读**同步
（`scripts/verify-real-mailbox-readonly.mjs`，只调 `POST /api/emails/sync`，
**不跑流水线**——流水线第 2 步会把广告邮件 MOVE 进真实邮箱的垃圾箱）。

| 账户 | 服务商 | 结果 |
|---|---|---|
| huangxutao@kxpms.cn | imap.exmail.qq.com:993 | 200，5 封 |
| feikemanager@163.com | imap.163.com:993 | 200，14 封 |
| feikemanager1@163.com | imap.163.com:993 | 200，0 封（空信箱） |
| kimmy.huang@163.com | imap.163.com:993 | 200，50 封 |
| 56551681@qq.com | imap.qq.com:993 | 200，50 封 |
| invoice-fixture（夹具） | 127.0.0.1:1143 | 200，7 封 |

6/6 连通并落库，中文主题全部正常显示。QQ/163 都没有触发「登录需特殊头」的限制
（163 的 CLIENTID 头逻辑在代码里有，但这次没被触发，**不能据此说它是对的**）。

### 7b.1 BUG-AQ：主题没做 MIME 解码（真实数据才暴露）

第一次连真实邮箱，5 封邮件主题全是 `=?GBK?B?ob6/qtD5xvS556G...?=` 原文。
两层原因：
1. `fetcher.go` 直接存 `m.Envelope.Subject`——go-imap **不解码** RFC 2047 编码字；
2. `decodeMIMEWord` 用的是 `new(mime.WordDecoder)`，没挂 `CharsetReader`，
   默认只认 UTF-8/ISO-8859-1，遇到 `=?GBK?B?` 直接报错 → 整体返回原文。

影响不止显示：发票关键词匹配是**在主题上做的**，不解码 ⇒ 命中率为 0。

修复：fetcher 落库前解码 Subject 与 From 个人名；`decodeMIMEWord` 挂
`CharsetReader` 走 `decodeCharset`（GBK/GB2312/GB18030）。修复后真实主题：
「【开轩启圭】SMTP 配置测试邮件」「企业微信邮箱登录提醒」「[API VibeCoding] 余额充值成功」。
单测 `TestDecodeMIMEWord_*` 8/8（含「解不动必须原样返回、不能变空串」）。

> 这里我第一版测试夹具写错了：把 UTF-8 字节当成 GBK 塞进 Q 编码，断言失败后
> 一度以为是代码 bug。实际 `发票` 的 GBK 字节是 `B7 EE B7 A8`，用
> `=E5=8F=91=E7=A5=A8` 会解出经典乱码「鍙戠エ」。**是夹具错，不是代码错。**

### 7b.2 BUG-AR：真发票邮件的金额与销售方抽错

QQ 私人邮箱里有一封真发票：
「您收到来自〔真实销售方〕的发票，发票号码：〔20 位真实号，2026-10-01 合成化移除〕，**金额：3500.00元**，请注意查收！」

线上解析结果：`amount=0`、`seller=noreply@service.dzfp.com`。
- 金额：关键词表里只有「价税合计/合计金额/合计/总额/Amount」，**没有「金额」**；
- 销售方：正文没拉下来时（IMAP 路径只落 envelope）就退化成发件地址。

两处都会直接毁掉需求里的「汇总金额」和文件名里的「对方单位」。

修复：`reAmountTotal` 加「金额」并容忍尾随「元」（数值是必需的，所以
「您本月的金额已超出额度」这种散文不会误伤，有单测）；新增
`reSellerFromSubject` 从「来自XX的发票」里取开票方（中文发票邮件最常见形态）。
修复后在真实邮件上：amount=3500、seller=〔真实销售方，已合成化〕。

### 7b.3 BUG-AS：没有「只下载发票文件」的入口

下载逻辑一直只挂在 `Pipeline.Run` 里，而流水线会 MOVE 真实邮件。于是
「我现在只想把这张发票的文件拿到手」在真实邮箱上只能整条流水线跑一遍——
为了一个文件去改动真实邮箱，不是可接受的入口。

新增 `POST /api/emails/invoices/harvest {ids?}`：只下载（附件 / 正文链接 / XML 重渲染），
**不做任何邮箱写操作**；`ids` 省略 = 该 scope 下全部待下载。
配套把 `HarvestAll` 拆出 `HarvestInvoices(ctx, invoices)`——手动入口已经按
user/workspace 取好清单，不该再走一遍无 scope 的 `ListHarvestableInvoices`
（那会把别的 workspace 的待采集发票也拉进来重试）。

第一版实测把 HTTP 请求挂死 >280s（真 IMAP 慢 + 端点没有超时），已加 5 分钟上限；
加超时后同一封发票 **1.0–1.4s** 完成。

### 7b.4 真实发票端到端（`scripts/verify-real-invoice-e2e.mjs`，12 PASS / 0 FAIL）

```
解析：amount=3500  seller=〔真实销售方，已合成化〕  invoiceNo=〔已合成化移除〕
采集：200，1.0s  Downloaded=1  来源=pdf-url（正文里的阿里云 OSS 发票链接）
落盘：其他-〔真实销售方〕-3500.00-2026-10-01.pdf
汇总：count=4 合计=6060，共享 CSV 已生成，飞书未配置 ⇒ shareDocUrl 为空（不编假链接）
导出：A4 2x2 → 200 count=2 skipped=[1 个畸形件] 158458B %PDF
      A4 3x3 → 200 count=2 skipped=[1 个畸形件] 158463B %PDF
```

即需求 3 的「{费用类型}-{对方单位}-{金额}-{日期}.pdf」与需求 5 的 A4 网格，
在**真实发票邮件**上跑通了（此前只有夹具证据）。

> 文件名里的日期是 `2026-10-01`（下载当天）而不是开票日期：附件 PDF 里
> 没抽到文本日期，`ParseInvoiceDateFromBytes` 拿不到。信封里也没有开票日期字段。
> 这是**已知不足**，不是静默通过。

### 7b.5 踩坑：master key 跟着进程 CWD 跑

启动验证实例时我 `cd backend` 后再 `Start-Process`，于是它按相对 `DBPath`
在 `backend\data\email_master.key` **又生成了一把密钥**，所有账户凭证
`cipher: message authentication failed`。两个实例日志打的都是
`data/email_master.key`，肉眼看不出区别。

加固：启动日志改成打**绝对路径**（`filepath.Abs`）。另外仓库里现在有两把 key
（`data/` 与 `backend/data/`），后者是误建，可删。

## 7c. BUG-AU：整轮「20 分钟跑不完」的定位

症状是「每天定时收信」跑不完，没有进度、没有日志。定位手段：

1. `Get-NetTCPConnection` 看连接 —— 只剩**一条** `Established ...:993` 挂着，进程 CPU 累计才 1.67s。**不是网络慢，是代码在原地等**。
2. 逐账户打点：前 4 个账户各 0.4~1.3s，第 5 个 QQ 账户单独连挂 **4m11s**。

根因：第 1 步（逐账户同步）是 `for` **串行**循环，且整个流程**零日志**。

修（`pipeline.go`）：加 `stepStart()` 分步日志 + 逐账户耗时；改为**有界并发** `syncConcurrency = 3`。实测第 1 步从「20 分钟未结束」变成 **1.465s**。

第 1.5 步（拉原文做发票二次提取）同样毛病：6 分钟未完。改为并发 + `maxInvoiceBodyFetches = 24` 的预算后变成 **0.75s**。预算分配规则是 `date` 类（已确定是发票、只差日期）**优先**于 `candidate` 类（推测性扫描），见 `pipeline_budget_test.go`。

## 7d. BUG-AT：163 报 `NO SELECT Unsafe Login`

`FetchMessageRaw` 没发 RFC 2971 要求的 `ID` 客户端标识头，163 直接拒绝（`FAILED after 267ms`）。修（`mime.go`）：发 `ID` 头。同一封邮件变成 `in 552ms`。

同批修的还有 textproto 降级路径：它用**明文 TCP** 连 993，必然 `read greeting: EOF`。改成先做 TLS 握手；并且**主路径「匹配 0 条 / 无 body section」不再被降级路径的错误覆盖**。

## 7e. 清垃圾规则的真实数据量化

在 6 个真实账户、443 封邮件上跑 `LooksLikeSpam`：`spamHits=0`。这个 0 有两种解释，看数字本身分不开：(a) 真实信箱确实没广告；(b) 规则在真实数据上形同虚设。

诊断过程先排掉**两个我自己的错误**（都不是实现问题）：诊断 SQL 写成 `deleted_at IS NULL`（实际列是 `bigint NOT NULL DEFAULT 0`）；`nearMiss=0` 是因为 `LooksLikeSpam` 未达阈值时**返回零值**，Score/Why 被丢弃 —— 这才是真正的可诊断性缺口：预演报告只有命中/未命中两态，阈值没法基于真实数据校准。

修好后拿到 **17 条 near-miss**（全在 QQ，score=30：InfoQ 每周精要 11、阿里云产品月刊 4、ecloudrover 1），已在预演报告的 `spamNearMiss` 区块里露出。

**没有把阈值降到 30。** 订阅 newsletter 该不该算垃圾是产品判断，不是技术判断。

> **本轮未能完成的一项**：弱词按数量分级（≥4→100 / 3→70 / 2→40）这套改动在 06:17 的并发事故后与另一个会话在 `spam.go` / `invoice_harvest_test.go` 上反复互相覆盖，无法在共享工作区稳定落地，相关测试断言已撤回。**当前 `spam.go` 仍是旧规则**（弱词 ≥2 固定加 40 分、未达阈值返回零值），因此 `spamHits` 在真实数据上仍为 0、near-miss 仍只有发件人特征那 30 分。要落地需要独占这两个文件。

## 7f. BUG-AV：POP3 来源邮件会下载到**完全错误**的发票

QQ 上 IMAP 不可用时走 **POP3 降级路径**（实测 444 封里 **284 封**是 POP3 来的，它是主路径不是异常路径）。该路径落库时把 `UID` 写成 **POP3 位置序号** `i+1`，而不是 IMAP UID。采集器拿这个序号去 `UID FETCH` ——

> 取到的是**另一封毫不相干的邮件**，会被当成这封发票的原文解析、存成错误的发票 PDF。

同一封邮件存两份的根因也在这里：POP3 合成 `message_id` 时无视真实 Message-ID 头，`UNIQUE(account_id, message_id)` 拦不住 —— 实测 **47 组重复副本**。

修：`mime.go` 暴露真实 `MessageID`；`invoice_harvest.go` 加 `isPOP3SourcedEmail` 守卫（判据用 `em-pop3-` 前缀），**POP3 来源一律不走 IMAP**，缓存没命中就明确失败 —— 宁可失败也不下载错文件；新增 `email/body_cache.go` 在 POP3 同步那一刻把原文加密落盘（独立子目录 `email-bodies-raw`，与 server 层 `email-bodies` 分开：两边 UID 语义根本不同，混一个文件必然互相误命中），采集器改读缓存。

功能侧实测产出：`其他-杭州创客家投资管理有限公司-3500.00-2026-05-01.pdf`。

## 7g. 整轮上界：单账户 90s 放弃等待

`DefaultAccountSyncTimeout = 90s`（`Pipeline.AccountSyncTimeout` 可覆盖）。要点是**到期不打断、只放弃等待**：go-imap 不响应 context 取消，强行关连接会让账户状态半写。Sync 继续在后台跑完（落库幂等），结果用带缓冲 channel 回传。

## 7h. BUG-AW：「服务端 200 / 客户端 0 字节」= WriteTimeout 30s

服务端日志记 `POST /api/email/pipeline/run - 200`，客户端拿到 `UND_ERR_SOCKET: other side closed` + `bytesRead: 0`，**一个字节都没有**。

根因不是网络抖动：`http.Server.WriteTimeout: 30s`（`cmd/pocketd/main.go`），`longLivedPaths` 白名单里只有 SSE 路由，**邮件同步端点不在其中**。连接在 30s 处写 deadline 到期、连接已废，handler 在 1m30s 才 `writeJSON`。logging 中间件在 handler 返回后才打 200，与客户端是否真收到无关 —— 这正是难查的原因。

修：把 `/api/email/pipeline/run` 与 `/api/emails/invoices/harvest` 加进 `longLivedPaths`。

验证（`long_lived_middleware_test.go`，**含对照组与负控**）：

- `TestLongLivedPathSurvivesServerWriteTimeout`：把 30s 缩到 300ms、handler 缩到 600ms，真实 TCP 上白名单端点必须拿到完整响应体。
- `TestNonLongLivedPathStillCutOffByWriteTimeout`：对照组，断言非白名单**必须**拿不到响应。它没按预期失败的话，上面那个绿就是假绿。
- **负控**：把白名单两条摘掉重跑，用例立刻转红，报错正是 `连接在 300ms 处被掐断：EOF`。

真实进程复验：`POST /api/email/pipeline/run → 200，耗时 90787ms`，**这一轮本身就超过 30s，客户端完整收到整份报告**（13 PASS / 0 FAIL）。这补上了此前只能靠单测证明的缺口。

## 7j. BUG-AX：IMAP 卡死导致**连接泄漏**（§7g/§7h 之外的真正根因）

§7g 的 90s 上界和 §7h 的白名单都只解决了表象。`huangxutao@kxpms.cn` 每一轮都稳定卡满 90s，根因在更下面一层。

### 定位（三步，每步排除一个方向）

1. **网络层排除**：逐 IP 实测 `imap.exmail.qq.com` 四个地址 —— TCP 建连 41~191ms、TLS 110~187ms、greeting 34~90ms，服务端回 `* OK [CAPABILITY IMAP4 IMAP4rev1 ID AUTH=PLAIN AUTH=LOGIN NAMESPACE] QQMail IMAP4Server ready`。服务端完全正常。DNS 连续 12 次解析，IP 池稳定 4 个，无黑洞。
2. **协议层排除**：写只读诊断（`diag_kxpms_test.go`，逐条命令打点 + 每条独立超时），跑出来 CAPABILITY 102ms / LOGIN 593ms / ID 112ms / SELECT 57ms / UID SEARCH 51ms，**总计 1.4 秒**；且 `UIDNext=12` vs `LastSyncedUID=11` = **无新邮件**，按代码应当秒回。
3. **进程层命中**：决定性对照 —— **重启 pocketd 后第一次同步只要 1.261s**，而重启前进程里已经攒到 **11 条**到 993 的 Established 连接（`120.226.165.33` 一台独占 7 条）。

### 根因链

1. `net.Dialer.Timeout` 只管**建连**，建好之后的读操作没有任何时间上限。`imapclient.Options` 压根**没有** `ReadTimeout`/`WriteTimeout` 字段。`fetcher.go` 里那句注释「给 read deadline 一个上限，防止 server 不规范致连接挂死」**是说了但代码里没做**。
2. 卡在读上 → Sync 不返回 → `defer client.Close()` 永远执行不到 → **连接泄漏**。§7g 的 90s 只是「不再等待」，Sync 还在后台挂着，连接也没释放。
3. `scheduler.pollLoop` 每 60s 对「`LastSyncedAt` 没更新」的账户再起一个 goroutine 调 Sync，**无互斥** → 泄漏正反馈。

### 修（四道，缺一不可）

1. **滚动空闲 deadline**（`fetcher.go` 的 `deadlineConn`）：自己 `net.Dial` 拿到 conn（`imapclient.New(conn, opts)` 接受现成连接），每 `idle/3` 判断一次「距上次**活动**」，有活动才续期。
   > 这里踩了个典型坑：**用定时器无条件续期是错的**。第一版写成 `ticker 每 idle/3 就 SetDeadline(now+idle)`，实际效果是**静默连接也被无限续命** —— 服务端一个字节都不发，deadline 被一次次推后，Read 永远不返回。实测 `SetDeadline` 明确返回 `nil`（成功）而 Read 仍挂满 60s 整。测试当场把它打红。
2. **绝对硬截止**（`imapHardTimeout = 45s`）：兜的是「连接活着、有活动，但某条命令迟迟不返回」那一类。实测 `56551681@qq.com` 的 imap login 整整挂了 100s（`sync trace total 1m40.128s`），空闲 deadline 一次都没触发。这类只有硬截止能治。
3. **per-account 互斥**（`Fetcher.inflight` + 哨兵错误 `ErrSyncInFlight`）：第二个并发 Sync 立刻返回，pipeline 与 scheduler 都识别它 —— **跳过不是失败**，计进 errors 只会每轮挂一条假警。重复同步的危害不只是浪费连接：QQ 上 POP3 是**主路径**，重复同步等于把同一批邮件反复拉一遍，正是那 47 组重复副本的来源之一。
4. **POP3 共用剩余预算**（`syncBudget = 70s`）：原先 POP3 侧是固定 120s deadline，比 pipeline 的 90s 上界还长，于是「IMAP 挂 60s → 转 POP3 → 再挂 120s」整轮必然超时（实测 100.1s）。现在 IMAP 与降级共用一份预算 —— IMAP 慢通常意味着同一个服务商整体慢。

顺带补了 `syncTrace` 分步打点（只打慢阶段）—— 之前 Sync 里一条日志都没有，只能看到 pipeline 外层的 `TIMED OUT after 1m30s`，完全不知道卡在哪。上面「真凶是 QQ 私人账户而不是企业微信」这个结论，就是靠它一次定位的。

### 验证

`imap_deadline_test.go`，5 个用例，**全部做过负控**：

- `TestIMAPIdleDeadlineBreaksHungRead`：黑洞服务器（接受连接后永不发送任何字节），idle=400ms 拨号，断言 `Capability()` 必须在 idle 量级内返回错误（实测 534ms）。**负控**：摘掉 `dc.start()` → 立刻以 `读在 1m0.002s 才返回` 转红。
- `TestIMAPIdleDeadlineAllowsSlowButActiveConnection`（对照组）：服务器每 idle/2 发 1 字节，连续读 5 次、累计 753ms > idle 300ms，**必须全部成功** —— 证明滚动 deadline 只杀静默、不误杀「慢但在动」的正常同步。
- `TestIMAPHardDeadlineBreaksBusyButStuckConnection`：服务器持续发数据制造「活跃」假象，但 `idle=5s` 远大于 `hard=600ms`，仍必须被硬截止断开（实测 1.667s）。**负控**：把 `case !c.hard.IsZero() && now.After(c.hard)` 改成 `case false:` → 转红。
- `TestSyncSkipsAccountAlreadyInFlight` / `TestSyncReleasesInflightOnReturn`。

> 对照组还抓出过一次**测试自身的 bug**：它用 `conn.Read` 直连底层连接，绕过了 `deadlineConn`，`touch()` 从没被调用，看门狗正确地判定「静默」并钉死 deadline —— 那个失败是真的，但不是被测语义。

### 真实进程复验

同一条命令连跑多轮，对比修复前后：

| | 修复前 | 修复后 |
|---|---|---|
| 整轮耗时 | 90787ms / 90810ms（连续两轮都超时） | 多数轮 1.0~2.0s |
| kxpms 单账户 | `TIMED OUT after 1m30s` | `new=0 in 1.03~1.31s` |
| 残留 993 连接 | **11 条**（且持续累积） | **0 条**（超时轮最多 1 条，60s 后自行归零） |

## 7i. 共享工作树事故：十次 `git stash -u`，以及快照策略的一个致命缺陷

04:04、~04:28、~04:38、~06:0x、~06:17、06:26、~06:48、~06:52、~07:0x 共**十次**被
并发会话用 `git stash -u` / `git clean` 卷走未提交工作。其中两次造成了实质损害：

- **06:26（第 8 次）**：BUG-AV 的修复被整体清空 —— `invoice_harvest.go` 的
  `isPOP3SourcedEmail` 守卫、`body_cache.go`、`pop3_uid_test.go` 全部消失。
  工作区当时处于**危险状态**：`harvestOne` 正在拿 POP3 的**合成位置序号**去
  `UID FETCH`，也就是 BUG-AV 的原始 bug 复现，会把别人的邮件当成发票存下来。
- **~06:52（第 9 次）**：刚恢复的成果再次被清空。这一次是**新增的逐符号核验
  拦住的** —— 它在建快照前发现 16 个关键符号缺失，从而避免把坏状态固化。
  之后又发生两次，其中一次把 §7c~§7h 整段文档清空（靠快照 bce36f7 取回）。

### 快照策略的致命缺陷（比事故本身更值得记）

快照用 `git add` 读**当前工作区**。所以一旦工作区已被破坏，之后建的每一个快照都会
把**破坏后的状态**一起固化 —— 分支上看起来「有十几个提交、很安全」，实际上内容是坏的。

实证：快照 #9（`9f7082a`）和 #10（`bce36f7`）就是污染源，它们建在 06:26 事故**之后**，
都不含 BUG-AV 修复。真正干净的最后一个是 #8（`f923938`）。如果当时只按
「取最新快照」恢复，就会从一个坏快照恢复，越还原越坏。

修正：**建快照前必须逐符号核验**，一个都不缺才允许提交。当前核验清单（11 项）：

```
invoice_harvest.go      : isPOP3SourcedEmail, BodyCache
fetcher.go              : deadlineConn, imapHardTimeout, ErrSyncInFlight,
                          syncTrace, FetchPOP3MailboxWithIdle, MessageID
pipeline.go             : SpamNearMiss, RemindersUnclassified, splitReminderCandidates
pop3_fetcher.go         : pop3IdleTimeout, FetchPOP3MailboxWithIdle
scheduler.go            : ErrSyncInFlight
body_cache.go           : FileBodyCache
server.go               : api/email/pipeline/run
server_email_pipeline.go / main.go : BodyCache
```

恢复源的选择也因此变了：**不能盲取最新快照**，要扫一遍所有快照找出最后一个含
关键符号的（`git show <c>:path | Select-String -Quiet '<符号>'`），再叠加更早快照里
独有的部分。#13 就是「#11 提供 email 包 + f923938 提供 server 侧」双源拼出来的。

### 处置与恢复

- **独立快照分支** `email-pipeline-snapshot-2026-10-01`（`GIT_INDEX_FILE` +
  `read-tree`/`write-tree`/`commit-tree`/`update-ref`，**全程不动共享 HEAD、不动共享
  index**）。今天共推进 15 个快照提交。
- 仓库外备份 `~/Documents/openpocket-email-backup-20261001/`。

三个恢复坑：

1. stash 索引会随并发会话**整体移位**，不能写死 `stash@{0}`，要循环
   `git ls-tree -r --name-only "stash@{$i}^3" | Select-String -Quiet <文件名>` 动态定位；
2. `git show "stash@{$i}^3:<path>"` 对未跟踪文件**静默返回 0 字节**，必须用 `git checkout`；
3. 恢复**非一次性**，恢复完要 build + 测试 + 逐符号 grep 复核。

> **教训**：
> - 至少有 2 次不是「被清空」而是**两个会话同时编辑同一文件在互相覆盖**
>   （`spam.go` 的白名单一度出现重复条目）。这种情况继续重试只会拉锯，
>   正确做法是停手、如实记录哪一项没落地。
> - 我自己的恢复清单漏过 server 侧文件（只列了 `backend/internal/email`），
>   导致 BodyCache 装配没恢复，错误信息从 `cache miss` 退化成
>   `no raw body cache configured` 才被发现。**恢复清单要按符号列，不按目录列。**
> - **这个工作区已经不适合继续做增量开发**：一次恢复要 10 分钟以上，期间还可能
>   再被清空；快照链也会被污染。已向用户提出改用独立 worktree。

## 7k. `remindersSent=0`：不是规则失灵，是**看不见**（需求 4）

连续多轮报告都是 `remindersSent=0`，而邮件表里明明有 58 封 `importance='high'`。
只读 SQL 查下来是两件事叠加：

1. **提醒链路是通的**。通知中心有 **53 条** `email.important` 记录，`notified_at`
   集中在 01:40（42 封）和 04:04（9 封）。已提醒过的不重复提醒，符合设计。
2. **但新邮件的 importance 全是空**。04:44 之后入库的邮件 importance 一律为空，
   包括「企业微信邮箱授权码使用提醒」这种显然重要的。启动日志明写
   `POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled` ——
   importance 是 AI 分类（kxmemory）写进去的，没配就永远不会有 high。

判定依据：58 封 high **全部带 `ai_summary`**，说明它们是 AI 分类产物而非规则标注；
而 7 个账户的 `rules` 列**全是空**（`<no rules>`），所以「账户规则标重要」这条路径
根本没在用。

结论：`remindersSent=0` 是两件事叠加 —— 已提醒过的不再提醒（正确），新邮件等不到
high（缺 kxmemory）。**但报告上只有一个 0，分不清这两种情况**，需求 4 于是看起来
像没实现。

修（可观测性，不是改判定）：`PipelineReport` 增 `remindersScanned` /
`remindersUnclassified`，并把判定抽成纯函数 `splitReminderCandidates` 以便脱离
数据库验证。判定逻辑本身**没动** —— 重要邮件的召回率要不要靠启发式规则补，是产品
判断，不该由我替你决定。

> 这里有个容易写错的点，值得单独钉住：`Notifier == nil` 时必须**什么都不做**，
> 尤其是不能把邮件标记成已提醒。一旦标了，等 Notifier 装好之后这些邮件就再也不会
> 被提醒，而报告里依然显示 0。

验证 `reminder_diag_test.go`，3 个纯函数用例，**含负控**：

- `TestSplitReminderCandidates_SeparatesUnclassified`：三种状态（该提醒 / 已提醒过 /
  未分类）必须分开，且「已分类为 medium」和「垃圾」都**不能**算进未分类。
- `TestSplitReminderCandidates_AllUnclassifiedYieldsZeroButExplained`：复刻 kxmemory
  没配的场景 —— 提醒为 0，但 unclassified 必须等于总数。
- `TestSplitReminderCandidates_HandlesShortNotifiedSlice`：两个切片长度不一致时不越界。
- **负控**：把 `unclassified++` 注释掉 → 前两个用例立刻转红
  （`unclassified = 0, want 1` / `want 3`）。恢复后全绿。

## 7l. POP3 发票为什么救不回来（一个尚未修的盲区）

两张 QQ 发票至今 `failed`，错误是 `raw body cache miss`。查下来是**一个设计盲区**，
不是数据坏了：

`data/email-bodies-raw/` 目录**根本不存在** —— 原文缓存是 BUG-AV 的修复加的，
「POP3 同步那一刻把原文落盘」。但 POP3 路径**只在 IMAP 失败时才走**，而 IMAP 现在
一直正常，所以 POP3 同步从未被触发过，原文自然从未落盘。

结果是一个死结：**存量 POP3 邮件的原文永远拿不到** —— 不会 IMAP FETCH（守卫不让，
宁可失败也不下载错的），也不会有 POP3 缓存（POP3 不跑）。

真实进程复验（`POST /api/emails/invoices/harvest`，`pocketd-v23`）：

```
{"processed":3,"result":{"Processed":3,"Downloaded":0,"Pending":0,"Failed":3,"Skipped":0}}
inv_1790789580385036500_1 | failed | <nofile> | POP3-sourced email and no raw body cache configured; refusing to IMAP-FETCH
```

**守卫确实生效**：没有新落盘任何发票文件（目录里最新的仍是 0:29 那次成功的），
即「宁可不下载，也不下载错邮件」在真实进程里成立。

但这次复验还暴露了**我自己的一个恢复遗漏**：`no raw body cache configured` 说明
BodyCache 当时是 nil —— 第 8 次事故把 `server_email_pipeline.go` / `main.go` 里的
装配也清掉了，而我第一次恢复时只列了 `backend/internal/email`，没列 server 侧。
补回后错误信息变成 `raw body cache miss (err=<nil>)`，说明它真的去查缓存了。

> **自愈方案（已于 §7m 落地，但推翻过一次）**：最初的方案是「采集器在缓存未命中时，
> 用 IMAP SEARCH 反查真实 UID」。那个方案对真实场景**无效** —— 详见 §7m 的两条硬证据。
> 最终落地的是**回到 POP3 用位置序号 RETR**（`FetchPOP3MessageByIndex`）。

## 7m. 我推翻了自己：「IMAP SEARCH 反查真实 UID」对真实场景无效

§7l 的自愈方案先做成了 IMAP SEARCH 反查（`imap_resolve.go`），代码正确、测试扎实
（3 用例含负控）。**下一轮自查时用真实数据推翻了自己**，两条硬证据：

1. **两张 QQ Wallet 发票无法按头部区分** —— 主题、主题、发件人、日期**全同**
   （`2026-10-01 01:24:12`，同一批落库），只有正文里的发票号不同
   （`…7012698`/CNY 126.00 vs `…7012703`/CNY 328.50）。`HEADER Subject + From +
   SINCE/BEFORE` 必然同时命中 134 和 135 → 落入自己写的 ambiguous 拒绝 → 白做。
2. **邮件在 IMAP 侧根本不存在** —— QQ 账户 IMAP 侧 50 封里 Wallet/Invoice 主题
   **零命中**，发票只存在于 POP3 路径的 279 封里。SEARCH 必然 0 命中。

> **教训**：上轮只核了「守卫逻辑正确、测试扎实」，没核「这两封真实邮件在 IMAP
> 侧到底在不在」。典型的局部证据支撑全局结论。真实数据诊断不是可选项。

## 7n. 正确的自愈：回到 POP3 用位置序号 RETR

位置序号（134/135）在 **POP3 侧是有效的**（它就是 POP3 自己的编号），拿它去 IMAP
盲 FETCH 才危险。三处新增：

- `FetchPOP3MessageByIndex`（`pop3_fetcher.go`）— 按位置序号 `RETR` 单封，
  可选 UIDL 交叉校验防位置漂移。
- `RefetchPOP3RawByIndex`（`fetcher.go`）— 用账户凭据走 `pop3EndpointFor`
  解析出的 POP3 端点补取。
- `recoverPOP3SourcedRaw`（`invoice_harvest.go`）— **POP3 补取优先**，
  SEARCH 反查降为次选；拿到的原文回填缓存。

两条路都过 `sameEmailMessage` 闸门：真实 Message-ID 相等=强确认，**不等=强否定
直接拒绝**，否则要求主题+发件人相等且同一天。

**测试抓到一个我自己没想到的缺口**：`TestSameEmailMessage_RealMessageIDMismatchRejected`
一开始就红了 —— 我把「真实 Message-ID 不匹配」当成「无强确认」而继续走头部比对，
于是放行。真实 Message-ID 明确不等就是另一封，必须立即拒绝。已修正为强否定。

**负控**：`sameEmailMessage` 改成永远 `true` → 3 个「拒绝」用例全红、3 个「接受」
仍绿，精确证明在测拒绝逻辑。

> **仍未验证**：自愈路径**没有在真实 QQ 邮箱上跑过**（只读约束）。要生效前提是
> 那两封还在 QQ 的 POP3 收件箱（位置 134/135 未漂移）。需要一次只读 RETR 授权。
>
> **已由 §7s 实测证伪** —— 见下。位置序号已漂移，方案本身不成立；但
> `sameEmailMessage` 闸门按设计拦住了错误邮件。

## 7o. 需求 3 的规范文件名在真实 QQ 发票上是废的

翻库时发现两张真实发票的提取结果三字段全错：

| 字段 | 库里的值 | 应该是什么 | 根因 |
|---|---|---|---|
| `invoice_no` | `Issuance` | `24317200000907012698` | 主题 `Invoice Issuance Notice` 里 `Invoice`+空格把纯字母 `Issuance`（8 字符过长度门槛）当发票号 |
| `seller` | `name:` | `Tencent Cloud Computing Co Ltd` | snippet `Seller name:` 的标签词被当值 |
| `amount` | `0.00` | `126.00` | `CNY 126.00` 的 ISO 货币代码不在 `[¥￥$€£]` 白名单 |

后果：`{费用类型}-{对方单位}-{金额}-{日期}.pdf` 产出 `其他-name--0.00-2026-10-01.pdf`，
**金额是空的，对账不可用**。

修 `invoice.go` 四个共享正则：发票号值必须**含数字**（不用数字打头 —— 那会误杀
`INV-TEST-0001`）、销售方支持 `Seller name` 复合标签、金额支持 ISO 货币代码、
全部加 `(?i)`（真实英文标签是小写）。

**两次负控都给了实质信息**：
1. 撤 `reCurrency` 的 CNY，金额用例**没转红** —— 金额实际靠 `reAnyAmount` 兜底
   路径，我改了两处却只撤了一处。撤对后 3 个用例全红。
2. 全量测试抓到**修过头**：首版用 `[0-9]` 打头，`TestRealInvoice_Amount3500`
   立刻回归（`INV-TEST-0001` 被误杀），当场修正。

## 7p. 真实数据诊断抓出我上一轮引入的 seller 跨行缺陷

§7o 的修复里，`reSeller` 值组改成 `(?:\s+…)*` 支持多词公司名，但 `\s` **含
`\r\n`** —— 贪婪匹配把销售方后面那行一起吞了：

```
Seller="Tencent Cloud Computing Co Ltd\r\nInvoice details please see attachment (PDF)."
```

文件名退化成 `其他-Tencent-…-Invoice-details-please-see-a-126.00-….pdf`。

**为什么单测没抓到**：我写的 snippet 恰好没触发这个边界。是把真实库 snippet 喂进
提取器才暴露的。改为 `[^\S\r\n]+`（非换行空白）并限制最多 6 个词，负控换回 `\s`
后断言立即转红。

真实数据验证（两封各自正确）：

```
其他-Tencent-Cloud-Computing-Co-Ltd-126.00-2026-10-01.pdf
其他-Tencent-Cloud-Computing-Co-Ltd-328.50-2026-10-01.pdf
```

> **边界**：字段提取修好了，但这两张发票的 **PDF 仍下载不了** —— 原文拿不到，
> `status` 仍是 `failed`。字段提取与原文获取是两条独立线，后者依赖 §7n 的
> 未验证自愈路径。存量 failed 记录需走 `/api/emails/invoices/extract` 单封重提取
> 才会用上新正则（**没有批量入口**）。

## 7q. 需求 8 客户端 LWW 一直在被「假测试」覆盖

`account-sync.test.mjs` 里的 `planAccountSync` 是**测试文件自己复制的一份判定
逻辑**，不是生产实现。生产判定埋在 `account-sync.ts`，而该模块 import 了
`emailApi`/`localDB`/`vue`，node 测试环境整条依赖链解析不了 —— 这正是当初选择
复制一份的原因。

后果：**生产 LWW 判定怎么改，测试都全绿**。

修法：抽出无任何 import 的纯模块 `account-lww.ts`，生产与测试共用同一份实现。
tsc 当场抓到 `export { x } from` **不会把名字引入本模块作用域**（`TS2304`），
改为 import + export 双向。

**负控**：生产的 `>` 改成 `>=` → `equal timestamps do nothing` 立即转红；旧版
假测试对同一改动完全无感。

> **我的一次误判（已撤回）**：中途判定 `buildMirrorAccountWrite` 无条件 UPDATE
> 覆盖、违反需求 8。**不成立** —— LWW 判断在调用方 `emails-store.ts:151`
> （`if (local && local.updatedAt >= acc.updatedAt) return false`），
> `buildMirrorAccountWrite` 只是 SQL 构造器。

## 7r. 需求 6「默认本地执行」此前零测试守护

判定埋在 `runEmailPipeline` 里，无测试。风险很实：把 `mode == "server"` 改成
`mode != ""` 就让**默认**变成委托，而被委托的是带邮箱权限的整条流水线（POST 到
远端编排服务）—— 不会让任何现有测试变红。

抽出 `shouldDelegatePipeline` 纯函数 + 4 用例。负控用的正是那个危险改动，3 个
用例转红。顺带固化一个隐含行为：配了 `server` 却没给 URL 时**落回本地**，
而不是委托进一个必然报错的分支（`delegatePipeline` 遇空 URL 直接返回错误，
等于那轮什么都没跑）。

## 7s. 真实 POP3 只读实测：位置补取被证伪，闸门按设计生效

用户授权「只读 RETR」后在**独立 worktree**（见 §7t）跑真实 QQ POP3
（`pop.qq.com:995`），只发 STAT/UIDL/RETR/QUIT，**不发 DELE、不发任何 IMAP 写命令**。

**连接与取回都成功**：`FetchPOP3MessageByIndex` 连上 `pop.qq.com:995`，
位置 134 取回 2870 字节、135 取回 3345 字节。**但两封都不是目标邮件**：

```
index=134  db:   from="56551681@qq.com" subj="[QQ Wallet] Electronic Invoice Issuance Notice"
         pop3: from="速云U站API <u1@syapi.cn>" subj="您的额度即将用尽"
index=135  db:   from="56551681@qq.com" subj="[QQ Wallet] Electronic Invoice Issuance Notice"
         pop3: from="API VibeCoding <695562094@qq.com>" subj="[API VibeCoding] 余额充值成功"
```

**结论：§7n 的位置补取方案被证伪。** POP3 位置序号（第几封）会随邮件
增删而漂移——这两封发票是后来才落库的，现在 134/135 位置上坐的是别的邮件。
位置序号只在**当次同步那一刻**有效，事后回取不可靠。

> **闸门按设计生效**：`sameEmailMessage` 把两封都判为「不是那一封」并**拒绝**。
> 若没有这道闸门，这两封无关邮件的附件会被当成 QQ Wallet 发票存成错误的
> PDF —— 这正是 §7n 反复强调的「绝不拿不确定的原文」。**安全底线在真实数据上
> 第一次得到验证。**

**根因（比方案失效更值得记）**：库里 `message_id` 是合成的
`pop3-ZL0007_…`，而真实 Message-ID 形如 `1788749656…@syapi.cn`。
**POP3 落库时没解析出真实 Message-ID**（POP3 协议本身不返回它，只能从
RETR 后的邮件头里解析，而当时那条路径没做）。因此：
- 无法按 Message-ID 精确回取；
- `email_pop3_seen` 的 UIDL 去重与 `UNIQUE(account_id, message_id)` 都只能靠
  合成值，这也是 §重复副本 47 组脏数据的机制来源。

**要真正救回这两张发票，可行的只有**：下次这两封被重新同步时，在 POP3 落库
环节**解析并保存真实 Message-ID + 原文缓存**（两件事一起做），此后才有精确
回取的可能。存量这两封——**没有可靠路径能救回**，只能人工从 QQ 邮箱下载。

## 7t. 独立 worktree（用户决策：改用）

共享工作区累计 10 次 `git stash -u` 后，用户选择改用独立 worktree：

```
C:/workspace/openpocket-wt-email   90b6d36 [email-pipeline-snapshot-2026-10-01]
```

要点：
- 建在**仓库外**（`C:\workspace\openpocket-wt-email`），不与主工作区共享
  未跟踪文件，`git stash -u` 影响不到它；
- 检出的是快照分支（不是我改动的产物），**建后 16 项符号逐条核验全中**，
  `go build ./...` 通过、email 包全量 `ok(37.5s)`；
- 本仓已有 4 个其它 worktree（`wt3` / `wt-822f` / `wt-head` / `wt-stt`），
  并发会话各自占用，**不要动它们**；
- worktree 里的 `.git` 是**文件**不是目录 —— `psql -f .git/xxx.sql` 这类
  写法会失败，临时文件放 `%TEMP%`。

## 7u. 回填按 UIDL 取回原文（已实测 216/279 成功），根治重复副本的钥匙

§7s 证伪了「按位置序号补取」。**正确**的入口是**按 UIDL**：UIDL 跨轮稳定
（服务器重排也不变），所以先 `UIDL`（无参）拿「UIDL → 当前序号」映射，
再 `RETR` 那个序号。新增 `FetchPOP3MessagesByUIDLs`（单连接批量）。

> **踩到的坑 1**：逐封调用（每封重连 + 拉全量 UIDL）必然超时。QQ 账户 285 封
> 时全量 UIDL 响应本身接近 45s 预算，实测每封都 `i/o timeout`。必须**一次
> 登录、一次 UIDL、循环 RETR**。
>
> **踩到的坑 2**：`uidlFromEmailID` 一开始按第一个 `-` 切分 accountID，而
> accountID 本身含 `-`（`acct-1790784184824054300-1`），把整段当成 UIDL，
> 回填全找不到。必须**传入已知 accountID** 精确剥前缀。

**真实只读实测**（`POCKET_DIAG_POP3_BACKFILL=1`，不发 DELE）：

| 账户 | 取回 | 说明 |
|---|---|---|
| huangxutao@kxpms.cn | **6/6** | 全部成功 |
| 56551681@qq.com | **216/279** | 63 封 MISS = 已被删除，不在收件箱 |

取回内容与库记录**主题完全对应**，并拿到了**真实 Message-ID**，例如
`mis_201A73A…@exmail.weixin.qq.com`、`tencent_E35ABE…@qq.com`。

**为什么这是根治重复副本的钥匙**（实测库内 43 组疑似重复，模式统一为）：

```
em-10432-acct-…-3                    REAL  uid=10432  "AWS 账户提醒"
em-pop3-acct-…-3-ZC0023_7u_NgG…10    SYNTH uid=260    "AWS 账户提醒"
```

同一封邮件，IMAP 侧用真实 IMAP UID + 真实 message_id 落一条，POP3 侧用
**位置序号** + **合成 message_id** 再落一条；`UNIQUE(account_id, message_id)`
因为两个值都不同而拦不住。回填真实 Message-ID 后，这两条就能按 message_id
对齐并合并，从根上消掉重复。

**尚未做**（诊断只读验证，未写库）：把 216 条真实 message_id 写回
`emails.message_id`、把原文写入 BodyCache、再按 message_id 合并重复组。
写库前需人工确认合并策略（保留 IMAP 侧还是 POP3 侧、发票/垃圾状态如何继承）。

## 7v. 重复副本 dry-run 报表：**43 组里只有 40 组是真副本**（含一处自我更正）

`diag_dup_report_test.go`（只读，只 SELECT）按
`(account_id, from_address, subject, date)` 聚类出 43 组，结论如下。

**自我更正**：我此前说「43 组都是 IMAP+POP3 配对」。**不准确**。报表显示第 6 组
`[API VibeCoding] 余额充值成功` 下有 **22 条**同主题同发件人同时间的记录，我一度
判为「同封落库 22 次」。查证后是**误判**：

```
subject                          recs  distinct_msgid  distinct_uid
您的订阅额度即将用尽               90        90             90
[API VibeCoding] 余额充值成功      60        60             60
```

`message_id` **各不相同**（90/90、60/60）——这些是**真实的不同邮件**（订阅提醒本
就是按天重复推送），**不是**副本。若按「同主题同时刻」去重，会误删 200+ 封真实邮件。

**严格判据下的真实构成**：

| 构成 | 组数 | 性质 |
|---|---|---|
| IMAP 1 条 + POP3 1 条 | **40** | **真重复副本**（可合并） |
| 仅 POP3 多条 | 2 | 真实重复投递（**不可动**） |
| IMAP 2 条 + POP3 0 条 | 1 | IMAP 侧重复（另案） |

**合并仍需 message_id 对齐**：即使「IMAP 1 + POP3 1」，同一时刻同主题也**可能是
两封不同的邮件**（比如确实同时收到两封 AWS 提醒）。所以真正的确认条件是
**回填后的真实 Message-ID 相等**——§7u 已实测该能力可行（216/279 取回）。

**状态继承风险（报表实测）**：
- 42 组的 POP3 侧 `notified_at` 非空 → 只保留 IMAP 侧会**丢提醒记录**
- 1 组的 POP3 侧挂着发票（`email_invoices.email_id` 指向它）→ 删 POP3 侧会**丢发票关联**
- 3 组不是 IMAP+POP3 配对 → 需人工判定

**另一个发现**：`emails.deleted_at` 实际存的是**空字符串**而非 NULL（447 封全是
`''`）。生产代码用 `COALESCE(deleted_at, 0) = 0` 判断，正确；我最初的诊断用
`IS NULL` 导致查出 0 行。**记一笔**：任何自己写的查询必须跟生产用同一判据。

## 7w. 写库前预演：**40 组候选里只有 29 组是真副本**（message_id 才是判据）

`diag_backfill_align_test.go`（只读）把 §7u 的回填能力接上 §7v 的分组，逐组用
**真实 Message-ID** 比对两侧（不是同主题同时刻）。真实 POP3 实测结果：

| 判定 | 数量 | 处置 |
|---|---|---|
| **CONFIRMED** 两侧真实 message_id 相同 = 同一封 | **29** | 可安全合并 |
| **NOT-DUP** 两侧 message_id 不同 = 两封不同邮件 | **2** | **绝不能合并** |
| UNRESOLVED POP3 侧邮件已删除、取不回来 | 9 | 需人工判定 |

取回情况：QQ 账户 30/35、kxpms 5/5（已删除的在收件箱里不存在）。

**NOT-DUP 这 2 组是本节最有价值的部分**：`【network-switch】邮件通道测试` 在库里
IMAP 侧是 `uid-10405` / `uid-10406` 两条、POP3 侧真实 message_id 分别是
`mis_2DD98EE7…` / `mis_0055C0BF…` —— **它们是不同的邮件**。按「同主题 + 同发件人
+ 同时刻」合并就会把两封不同的邮件当成一封删掉。

> **实证结论**：§7v 的严格判据（IMAP1+POP31）**仍然不够**——它只是把 43 组
> 收敛到 40 组候选，真正的确认条件只能是**回填后的真实 Message-ID 相等**。
> 这 2 组如果没做预演而被直接合并，就是一次真实的数据损失。

**同时可回填原文缓存 31 封**（POP3 侧取回成功的那些），这是发票采集与重复判定的
共同前提。

**待用户定**：29 组合并时保留哪一侧、42 组 `notified_at` 与 1 组 `email_invoices`
关联如何继承。9 组 UNRESOLVED 与 2 组 NOT-DUP 都不动。

## 7x. 合并迁移预演：17 组**零字段需迁移**（更正 §7v 的风险高估）

`diag_merge_plan_test.go`（只读）按建议方案（保留 IMAP 侧 + 迁移 POP3 侧可继承
状态）算出实际影响：

```
将合并的组数:      17
  迁移 notified_at:  0
  改指发票关联:      0 条 invoice 行
  补上 importance:   0
  补上 category:     0
  补上 is_read:      0
  补上 is_starred:   0
```

17 组的 IMAP 侧状态**已经完整**，合并是「纯删重复行」，无字段冲突。

> **更正 §7v**：我在 §7v 写「42 组 POP3 侧 `notified_at` 非空 → 只保留 IMAP 侧
> 会**丢提醒记录**」。**这个推断是错的**——当时只统计了 POP3 侧非空，没核对
> IMAP 侧。实测：
>
> ```
> imap_has  pop3_has  both_have  only_pop3  total
>       40        40         40          0     40
> ```
>
> **两侧都有**，`only_pop3=0`。提醒处理按账户维度进行，两条记录都写入了
> notified_at。合并**不会丢任何提醒记录**，风险显著低于我此前的描述。
>
> **教训**：报告「A 侧有 X，合并会丢 X」之前，必须先确认另一侧**没有** X。
> 只看一侧就推断「会丢」，是又一次局部证据支撑全局结论。

**注意**：本预演的 JOIN 条件更严（`lower(from_address)` 精确相等 + 同 `date`），
候选是 21 组（§7w 是 40 组）；其中经真实 message_id 确认后 **17 组**判定为同一封。
§7w 的 29 组与本节的 17 组差在 JOIN 口径，**以本节为准**（它同时校验了
message_id 与状态可迁移性）。

## 8. 仍未验证 / 未完成（不得外推）

- **真实邮箱已接入（6/6）**，但只做了**只读同步 + 发票采集**。仍未在真实邮箱上验证的：
  - **垃圾邮件 MOVE**：清垃圾这一步会改动真实邮箱，全程没跑过（`cleanSpam` 只在夹具上验证过）。
    要不要在真邮箱上开这条，建议你确认后再开。
  - **163 的特殊头**（CLIENTID 等）这次没被触发，代码路径未被真实流量覆盖。
  - **发票开票日期**没从附件 PDF 里抽到，文件名日期退化为下载当天。
    （另注：**主题/摘要里的英文字段**提取曾整体失效，已由 §7o/§7p 修复并有真实
    数据验证；**附件 PDF 内的字段**仍未抽，二者是两回事。）
- **飞书推送未验证**：`POCKET_FEISHU_INVOICE_CHAT_ID` 未配置，**「发消息到指定群」
  从未真实跑过**（上传文件 / 电子表格部分已验证，见上）。
  事件回调 `https://m.kxpms.cn/callback/feishu` 的部署与飞书格式响应亦未验。
  已验证的只是无凭证也成立的那部分：`PublishLedgerScoped` 5 用例全绿，其中
  `SkipsWhenUnavailable` 守住「未配置时返回空 URL 且不报错、不编造 `shareDocUrl`」；
  推送失败保留 `feishu_sent_at=0`，由共享汇总文档兜底。
- **POP3 自愈路径（§7n）已在真实邮箱被证伪**（§7s）：位置序号漂移，取回的
  是无关邮件，闸门拦住了。两张发票**存量无法可靠救回**，只能人工下载。
  真正的修法是让 POP3 落库时保存真实 Message-ID + 原文缓存（**未做**）。
- **飞书真实租户链路已验证**（2026-10-01）：`tenant_access_token` 获取、
  建电子表格、写 4 行 20 单元格（含两笔发票 + 合计 454.50）、读回确认，
  全部 `code=0`。**未验证的**是「发送消息到指定群」——缺
  `POCKET_FEISHU_INVOICE_CHAT_ID`（真实 chat_id），以及事件回调
  `https://m.kxpms.cn/callback/feishu` 的部署与飞书格式响应未验。
- 需求 7 前端 UI：**用户选择跳过**真机/浏览器 UI 验证。已确认的是
  构建通过（`build-gate` 绿、三个 email 视图独立 chunk）、viewmodel 缺口 0、
  i18n / 裸文案门禁全绿、以及 `/api/emails` 与 `/api/emails/invoices`
  在真实后端返回真实数据。**未验证**的是实际渲染与交互。
- **「多次操作才能下载到发票」只做到跨轮重试**（`MaxInvoiceAttempts=8` + pending 重试），
  没有「打开邮件→点确认→再下载」这类交互式多步。
- 定时流水线**到点执行**没有等过一次真实 06:00（用注入时钟单测 + 启动排期日志代替）。
- 需求 6 的「委托服务端执行」：`delegatePipeline` 只是 HTTP 转发，**对端编排服务不在本仓**；
  默认 `local` 判定已由 §7r 的纯函数 + 4 用例（含负控）守护。
- 前端改动（3×3 入口、409 走行覆盖）只过了 typecheck，**未做真机 UI 验证**。

---

## §7y 飞书事件回调验签：官方算法核实与实现修正（2026-10-01）

### 起因

`verify_signature_test.go` 首次运行即 FAIL（`AcceptsFeishuOfficialSignature` 红），
暴露 `handler.go` 验签实现与飞书官方算法不符。**真实回调会恒返回 401**。

### 取证过程（三次，含两次自我推翻）

1. `web_search`「X-Lark-Signature 验签」→ 结果**全部是自定义机器人 Webhook** 的签名
   （`HMAC(key=timestamp+secret, data=空)`）。**不能**作为事件订阅回调的证据，未采信。
2. `web_fetch` 飞书官方「请求地址配置」文档 → URL 被重定向到「使用长连接接收事件」页面全文，
   签名算法原文仍缺失。
3. 改从搜索结果外置 artifact 中定位到官方原文（`Signature verification example code`
   段落，中英文各含五语言示例）：

   ```
   b1 = (timestamp + nonce + encrypt_key).encode('utf-8')
   b  = b1 + body
   s  = sha256(b)        # 裸 SHA-256
   校验 s == X-Lark-Signature
   ```

   官方英文描述：*"Concatenate the request headers X-Lark-Request-Timestamp,
   X-Lark-Request-Nonce with encrypt_key, encode ... and then concatenate with the
   original request body ... Use the sha256 algorithm to hash b"*。
   PHP 示例：`hash('sha256', $timestamp . $nonce . $encrypt_key . $body)`。

### 旧实现三处错误（均已确认，非推测）

| # | 项 | 旧实现 | 官方 |
|---|-----|--------|------|
| 1 | 摘要算法 | `hmac.New(sha256.New, key)` | **裸 `sha256.Sum256`** |
| 2 | body 位置 | 作为 HMAC 的 data，密钥串不含 body | 拼进**被哈希的明文**，与 ts/nonce/key 同级 |
| 3 | 输出编码 | `base64.StdEncoding` | **小写 `hex`** |

**自我更正**：本轮开始时我先入为主判断官方是 `HMAC(key=ts+nonce+key, data=空)`，
并据此写了「body 不参与签名」的测试。读到官方原文后**证伪了自己**——
body 明确参与哈希，且根本不是 HMAC。测试已按原文重写。

### 密钥字段修正

官方用的是「事件与回调 > 加密策略」中的 **Encrypt Key**，即 `POCKET_FEISHU_ENCRYPT_KEY`。
本仓历史字段 `POCKET_FEISHU_VERIFY_SECRET` 保留为**回退**（新增 `signatureKey()` 纯函数，
Encrypt Key 优先，两者皆空才是 dev 模式跳过验签）。原先只用 VerifySecret 配验签，
若用户按 `config.go` 注释理解只配 EncryptKey，会**静默跳过验签**——该隐患一并消除。

### 证据

- `handler.go:165-187`（`verifySignature` 官方实现）、`handler.go:193-198`（`signatureKey`）
- `verify_signature_test.go` 8 用例，其中 `RejectsLegacyHmacBase64Variant` 直接用旧算法
  构造签名断言必须被拒（把旧算法钉成反例）
- `go build ./...` 通过；`go test ./internal/feishu/...` → `ok 1.037s`

### 负控对照

把实现改回 `HMAC + base64 + body 作 data`：

```
--- FAIL: TestVerifySignature_AcceptsFeishuOfficialSignature
--- FAIL: TestVerifySignature_BodyParticipatesInSignature
--- FAIL: TestVerifySignature_RejectsLegacyHmacBase64Variant
```

3 例转红、另 5 例仍绿（它们守的是时间戳窗口/dev 模式/密钥选取等**别的**语义，
不受该负控影响）→ 测试确实在测算法本身，不是摆设。已还原。

**负控过程中的一次失败记录**：第一次注入负控时只改算法、没补 import，
`go test` 报的是 `encoding/hex imported and not used` **编译失败**，
不是断言转红。这**不构成有效负控**（证明不了测试能捕获算法错误），
补 `_ = hex.EncodeToString` 占位让包能编译后才拿到上面的 3 例红。

### 仍未验证

- 回调端点 `https://m.kxpms.cn/callback/feishu` **未实际部署联调**，无真实回调样本。
  本条修正的依据是官方文档 + 单测，**不是**真实 200 响应。
- 官方文档说明：配置 Encrypt Key 后事件体本身会被加密（AES-256-CBC），**本仓未实现解密**。
  即配了 EncryptKey 虽能过验签，但 `body` 仍是密文 → 事件解析会失败。
  这是**独立的第二个缺口**，本轮未修，需单独处理。

---

## §7z 飞书加密事件体解密（AES-256-CBC）——§7y 遗留缺口的修复（2026-10-01）

### 起因

§7y 修了验签，但留下一个**独立缺口**：飞书官方文档明确说明，配了 Encrypt Key 后
事件体本身会被加密（`{"encrypt":"<base64>"}`），而本仓**没有实现解密**。
后果：配了 Encrypt Key 虽能过验签，`body` 仍是密文 → 事件解析必然失败。
这是「按 §7y 配置上线就会踩」的坑，属本轮主动补齐。

### 取证

从 §7y 已取证的官方文档 artifact 中直接定位到「事件解密 / Event decryption」段原文
（含 Python/Java/Golang/Node/PHP 五语言示例）：

```
key  = sha256(encrypt_key)                        # 32 字节
raw  = base64decode(encrypt)
iv   = raw[:16]                                   # IV 内嵌密文头部，非配置项
body = AES-256-CBC-decrypt(key, iv, raw[16:]) → PKCS#7 unpad
```

关键细节（易错点）：

- key 是 `sha256(encrypt_key)` 的 **32 字节**摘要，**不是** encrypt_key 本身
- **IV 内嵌在密文前 16 字节**，不需要单独配置
- 官方 Python 示例用 `AES.MODE_CBC, iv`；Java 示例写死 `AES/CBC/NOPADDING`
  再手工去 padding——两者等价，本仓用 Go 标准 `cipher.NewCBCDecrypter` + 显式去 padding

### 真值锚点：官方自带测试向量

**没有自己造密文**，直接用官方文档 Python/Java 示例里的同一组数据：

| 项 | 值 |
|----|-----|
| `encrypt_key` | `test key` |
| `encrypt` | `P37w+VZImNgPEO1RBhJ6RtKl7n6zymIbEG1pReEzghk=` |
| 明文 | `hello world` |

该向量来源可追溯（官方示例代码中逐字出现），比自造 round-trip 更有说服力。
`TestDecryptEvent_OfficialTestVector` 直接锁它。

### 实现

- `decryptEvent(encryptKey, encrypted string) ([]byte, error)` — `handler.go`
- `unpadPKCS7(b []byte, blockSize int)` — 显式校验填充字节范围与一致性，
  **不**用「信任密文」的写法，避免畸形输入被静默截断成看似合法的明文
- handler 新增**分支 0**（`env.Encrypt != ""`），顺序严格是：
  **验签（对原始加密 body）→ 解密 → 解析明文 → dispatch**
  验签用原始 body 而非明文，否则签名对不上

### 证据

- `decrypt_event_test.go` 9 个顶层用例（含 5 个畸形子用例）全绿
- `go build ./...` 通过
- `go test`：feishu / email / email-rules / config / server 五包全绿（server 5.453s）

覆盖的用例：

- 官方向量真值锚点
- round-trip（自造密文，验证加解密对称）
- 畸形输入 5 例：空 key / 非 base64 / 过短 / 只有 IV 无密文 / 密文非块对齐
- PKCS#7 非法填充 5 例：填充字节 0 / 超块长 / 超数据长 / 内容不一致 / 空输入
- 端到端 handler：加密 body → 200 且正确派发
- 加密事件 + 错误签名 → 401（**解密分支不能成为验签旁路**）
- 加密事件 + 未配 key → 401（不能静默当明文解析）

### 负控对照（两次，均 3 例转红）

1. **IV 取错**（改成取后 16 字节）：
   ```
   --- FAIL: TestDecryptEvent_OfficialTestVector   invalid PKCS#7 padding byte 172
   --- FAIL: TestDecryptEvent_RoundTrip            invalid PKCS#7 padding byte 118
   --- FAIL: TestHandler_EncryptedEventEndToEnd    400 decrypt failed
   ```
2. **key 不做 sha256 派生**（直接用 encrypt_key 当 AES key）：
   ```
   --- FAIL: TestDecryptEvent_OfficialTestVector   invalid key size 8
   --- FAIL: TestDecryptEvent_RoundTrip            invalid key size 5
   --- FAIL: TestHandler_EncryptedEventEndToEnd    400 decrypt failed
   ```

两次都是 3 例红、其余 6 例绿（它们守的是畸形输入/验签旁路等别的语义）。
两个负控分别独立证明了「IV 位置」和「key 派生」这两个易错点被真正锁住。
均已还原并复跑全绿。

### 过程中抓到的一个测试自身 bug

`TestHandler_EncryptedEventEndToEnd` 首次 FAIL，期望 `dispatched == "im.message.receive_v1"`。
查 `dispatch` → `handleMessageEvent` 后确认：**是我测试写错了**——`broadcast` 收到的是
转换后的 `"feishu.message"`，不是原始 event type。生产代码行为正确，已改测试期望。
这与 §7v/§7w/§7x「只查一侧就下结论」是同一类毛病，测试期望同样需要核到实现。

### 仍未验证

- 端点 `https://m.kxpms.cn/callback/feishu` **未实际部署联调**，无真实回调样本。
  验签（§7y）与解密（本节）都只有「官方文档 + 单测」依据，**没有真实 200 响应**。
- 官方向量只覆盖 16 字节密文（1 个块）的最短路径；多块密文靠 round-trip 用例覆盖，
  但**同样不是真实飞书密文**。
- 长连接模式（长连接事件订阅）本仓未实现，本节只覆盖「发送至开发者服务器」模式。

---

## §7aa 需求 5（A4 网格导出）复核 + 那 7 个 FAIL 的根因定性（2026-10-01）

### A. 需求 5 不是「已完成」，是「已复核为真」

之前只在待验证清单里记了 `export_pdf.go` 存在，没验过它测的是不是生产代码
（同 §7u 需求 8「假测试」的教训）。本轮实测：

- `export_pdf_test.go` 直接调 `ExportInvoiceGrid`（生产函数），**没有自带副本**
- 断言很硬：真开产物数页 `api.PageCountFile`，并用 `api.PageDimsFile` 验每页
  **是 A4 尺寸**（容差 1.5pt），不是「函数没报错」式空测
- 9 个用例全绿（`-count=1` 强制重跑 4.906s，非缓存）

覆盖到的需求要点：

| 需求原文 | 对应用例 | 结果 |
|---------|---------|------|
| 4 张 2x2 ⇒ 1 页 A4 | `2x2FitsOneA4Page` | PASS（含 A4 尺寸断言） |
| 页数 = ceil(n/grid²) | `PageCountFollowsCeiling` | PASS（5 张：2x2⇒2 页 / 3x3⇒1 页） |
| 多页发票按格数算 | `MultiPageInvoiceFollowsCellCount` | PASS（12 格⇒3 页） |
| 多张发票合成一个 PDF | 同上（单文件多页） | PASS |
| 打印后剪裁 | — | **未覆盖，见下** |

**负控**：把 `PageDim` 改大 1.3 倍 ⇒
`--- FAIL: 2x2FitsOneA4Page  page is not A4: 773.86x1094.46 pt`。
证明 A4 断言真的在量产物、不是在验常量。已还原并 `-count=1` 复跑确认真绿。

### 发现的真实缺口：无裁切线

需求原文「打印后可直接剪裁」。当前实现 `export_pdf.go:123` 是 `Border: false`，
输出网格**没有任何裁切标记**。用户打印 A4 后面对 4/9 张发票，没有可对齐的裁切线，
「直接剪裁」这一步实际不可执行（只能凭发票白边目测）。

这**不是 bug 而是缺功能**——代码从一开始就没做。判断：属产品决策（要不要默认加线、
线条粗细、是否做成可配置），未擅自改。若加，默认方案是在每格边框画 0.25pt 细线。

### B. 那 7 个 `TestWorkItemReminder*` FAIL 的根因（纠正上一轮结论）

**上一轮我说这是「真实功能缺陷」，是错的，本轮纠正。**

根因：测试自身的时钟耦合，不是执行器逻辑问题。

- `workitem_reminder_test.go:194-199` `pinServerZone` 把 `time.Local` 钉成 UTC
- 默认免打扰窗口 `task.DefaultQuietWindow()` = 22:30→07:30（`task/quiet.go:106-108`）
- 现在是 **CST 11:59 = UTC 03:59**，落在窗口内 ⇒ `quiet.Defer()` 把提醒改期
  ⇒ `fired=0`、事件未写、`remind_at` 被改写而非清空 ⇒ 7 个断言全挂

**双向对照实测**（临时探针，已删）：

| 条件 | minute-of-day | `Defer` 改期? | 结果 |
|------|---------------|--------------|------|
| 钉 UTC（现状） | 239 = 03:59 | 是 | 7 FAIL |
| 固定 UTC+8（反控） | 719 = 11:59 | 否 | 探针 PASS |

`workitem_reminder.go:211-217` 的「免打扰内改期而非丢弃」正是它文档承诺的行为
（`workitem_reminder.go:17-19`），**逻辑是对的**。

讽刺点：`pinServerZone` 的注释（`:185-193`）自称要消除「隐藏的 UTC 假设」，
但它只固定了**时区**、没固定**时刻**——只解决了一半，于是换了个形式重新引入
「依赖真实时钟」的假设。这批测试**只在 UTC 07:30–22:30 绿**，是
flaky-by-time，不是 flaky-by-change。

排除本轮回归的证据：父提交 `a23c5a9` 单独开探测 worktree 跑同一包，**7 FAIL
报错逐字相同**；该包源文件哈希与 HEAD 完全一致（`4701F053…`）。
探测 worktree 已 `git worktree remove` 清理。

**与邮件需求无关**：这批是**任务提醒**（`task.Task.remind_at`），
不是需求 4 的邮件重要提醒。邮件提醒走 `pipeline.go` 的 `splitReminderCandidates`，
该链路本轮全绿。

修法（未擅自做）：在测试夹具里增加「把当前时刻推到非免打扰窗口」，
让断言与真实时钟解耦，只改 `_test.go` 不碰生产代码。

---

## §7ab 需求 3 的 XML 路径：零覆盖 → 13 用例，并修掉 2 个真缺陷（2026-10-01）

### 起因

复核需求 3 时发现一个**此前完全没人碰过的盲区**：需求原文明确要求
「原邮件中有 PDF 下载地址（可直接下载已有 PDF），也有 XML 数据格式
（可解析后重新渲染）」。PDF 那条路径本轮验过（`invoice_qqwallet_test.go`
用的是真实 QQ Wallet 邮件），**XML 那条从来没被验证过**。

证据：`grep 'ParseInvoiceXML|mergeXMLFields' **/*_test.go` → **零命中**。
全仓唯一提到 XML 的 `invoice_font_test.go` 里也没有这两个函数的调用
（只有字体查找 + PDF 渲染）。也就是说 XML 解析器可能早就坏了，
而因为没人测过所以一直没人知道。

### 新增 13 个用例（`xmlinvoice_test.go`）

夹具按**真实**全电票（数电票）结构写，不自造简化格式：

| 用例 | 覆盖 |
|------|------|
| `RealChineseFullEInvoice` | 中文标签嵌套 `<发票><发票号码>` + 价税合计 `￥126.00` |
| `EnglishKeyValue` | 英文清单 `InvoiceNo` / `TotalAmount` / `SellerName` |
| `AttributeStyle` | 属性形式 `AmountTotal="454.50"`（`xmlinvoice.go:80-84` 显式支持） |
| `StripsUTF8BOM` | Windows 工具导出带 BOM，解析前须剥 |
| `HandlesNamespacePrefix` | `xsi:` 命名空间前缀 |
| `UnrelatedXMLReturnsNil` ×4 | 空 / 畸形 / 无关 / 只有卖方（无票号金额）→ 必须 nil |
| `OnlyFillsEmptyFields` | mergeXMLFields 的优先级边界 |
| `AmountCleaning` ×4 | `￥` / `元` / `RMB` / `CNY` + 千分位 |
| `TrimsMaskingAsterks` | 票号带 `**` 掩码 |
| `LabelMatch_WideSubstringBehaviour` | 词典边界（含 `date`/`number` 这类宽泛子串） |

### 首次运行：2 个 FAIL —— 都是生产代码的真缺陷

#### 缺陷 1：XML 无条件覆盖主题提取值

`xmlinvoice.go:154-155` 旧写法：

```go
if f.Seller != "" {
    inv.Seller = firstNonEmpty(f.Seller, inv.Seller)   // ← XML 排在前面
}
```

`firstNonEmpty` 第一个非空即返回，所以 **XML 值永远赢**。这与该函数自己的
文档注释「只在原字段为空/为零时覆盖，**保持邮件主题提取值的优先级**」正好相反——
注释和实现是矛盾的，实现赢。其它 5 个字段（InvoiceNo/Date/Amount/Title/Category）
都正确地写成 `if inv.X == ""`，**只有 Seller 这一处不一致**，像是漏改。

已修为 `if inv.Seller == "" { inv.Seller = f.Seller }`，与同函数其它字段统一。

#### 缺陷 2：`<Seller>` 把税号拼进单位名

真实数电票结构：

```xml
<Seller>
  <销售方名称>腾讯科技（深圳）有限公司</销售方名称>
  <销售方纳税人识别号>9144030071526726XG</销售方纳税人识别号>
</Seller>
```

`labelMatch("Seller")` 命中 seller 类别，于是对整个 `<Seller>` 调 `deepText`——
而 `deepText`（`xmlinvoice.go:190-201`）**无条件拼接所有后代的 chardata**，
得到 `腾讯科技（深圳）有限公司9144030071526726XG`。

这个值会直接进需求 3 的规范文件名 `{费用类型}-{对方单位}-{金额}-{日期}.pdf`，
产出畸形文件名（例如 `其他-腾讯科技（深圳）有限公司9144030071526726XG-126.00-….pdf`）。

修法：新增 `nodeText()` / `findNameLeaf()`——父节点命中时**广度优先下钻到
「名称」类叶子**（`名称` / `name`），只有找不到名称叶子才退回 `deepText`。
`walk` 改调 `nodeText`。这样纯文本结构（`<销售方名称>X</销售方名称>` 直接命中，
无子节点）行为不变，`<Seller><Name>X</Name></Seller>` 也能拿到干净的 X。

### 负控对照（两次，各自精确命中 1 例）

1. 还原 `firstNonEmpty(f.Seller, inv.Seller)`：
   `--- FAIL: TestMergeXMLFields_OnlyFillsEmptyFields`（其余 12 例绿）
2. `nodeText` 退回直接 `return deepText(n)`：
   ```
   --- FAIL: TestParseInvoiceXML_RealChineseFullEInvoice
       Seller = "腾讯科技（深圳）有限公司9144030071526726XG", want 腾讯科技（深圳）有限公司
   ```
   报错原文就是那个畸形拼接值，缺陷 2 被直接复现。

两次均已还原，`go clean -testcache` 后 email 包全量 **ok 4.612s**（非缓存）。

### 过程中修正的一处自身错误

`TestMergeXMLFields_OnlyFillsEmptyFields` 初版写 `inv.BuyerTitle`，编译报
`type *Invoice has no field or method BuyerTitle`。查 `invoice.go:19-40` 发现
字段叫 **`Title`**（注释「发票抬头」= 购方），已改。属测试写错，生产代码无误。

### 证据

- `xmlinvoice_test.go` 13 个顶层用例（含 4 个子用例）
- `xmlinvoice.go`：`nodeText` / `findNameLeaf` / `leafNameKeys`（新增）、
  `mergeXMLFields` Seller 分支、`walk` 调用点
- `go build ./...` 通过；`go test ./internal/email/ -count=1` → `ok 4.612s`

### 仍未验证

- 夹具是按**已知真实结构**写的，但**没有真实数电票 XML 原文样本**。
  本轮所有发票实证（126.00 / 328.50 / 454.50）都来自 PDF 附件，
  没拿到过真实 XML 附件。真实 XML 的标签组合可能还有本文未覆盖的写法。
- `FileSource=xml-render` 这条重渲染链路端到端未跑（需要真实 XML 附件）。
