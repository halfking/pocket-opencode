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

---

## §7ac 需求 3「汇总金额」的浮点精度缺陷（2026-10-01）

### 起因

需求原文：「需要整理一个列表，记录必要信息并**汇总金额**」。
`ledger.go` 是这条的实现（写飞书电子表格），但金额是财务数据，
值得实测累加精度——浮点加法是经典出错点。

### 实测证据（不靠推断）

用探针复刻 `LedgerRows` 的累加：

```
100 张 0.07 -> float64 累加 = 7.00000000000000888178
0.1 x 10                        = 0.99999999999999988898
126.00 + 328.50                 = 454.5        （这两张恰好没有误差）
```

关键的一步是**看它实际发出去什么**。金额经 `json.Marshal` 后原样
PUT 给飞书 `/open-apis/sheets/v2/spreadsheets/{token}/values`：

```json
{"values":[["合计","",7.000000000000009,"","","","","共 100 张",""]]}
```

也就是说**共享台账的合计格里会直接显示 `7.000000000000009`**。
这不是内部精度问题，是用户能在表格里看到的错账。

### 修复

`ledger.go` 改用**整数分累加**：

```go
var cents int64
for _, inv := range invs {
    cents += int64(math.Round(inv.Amount * 100))   // 四舍五入到分再累加
    rows = append(rows, []any{ ..., round2(inv.Amount), ... })  // 明细行同样规整
}
total = round2(float64(cents) / 100)
```

新增 `round2(v) = math.Round(v*100)/100`。整数分累加后
`float64(cents)/100` 的最短表示恰好是 `7`（而非 `7.000000000000009`）。

明细行也一并 `round2`：解析器可能给出 `126.005` 这类三位小数值
（§7ab 的金额清洗链路就接受任意精度输入），不规整会直接混进金额列。

### 负控对照（3 例转红）

改回 `total += inv.Amount` + 明细不 round2：

```
--- FAIL: TestLedgerRows_TotalIsExactInJSON
--- FAIL: TestLedgerRows_DetailAmountRoundedToCents
    detail amount must be rounded to cents, got [...,126.005,...]
--- FAIL: TestLedgerRows_RealInvoiceTotals/mixed_decimals
```

第二条的报错原文把缺陷值 `126.005` 直接打了出来。已还原。

### 证据

- `ledger_sum_test.go` 4 个顶层用例（含 4 个子用例）
- `ledger.go`：`LedgerRows` 累加逻辑 + 新增 `round2`
- `go build ./...` 通过；`go test ./internal/email/ -count=1` → `ok 4.496s`（清缓存后）

### 过程中的一处自身错误

改 `LedgerRows` 时多留了一个 `}`，`go vet` 报
`ledger.go:64:1: expected declaration, found '}'`，构建失败。已删。

### 未覆盖的相邻风险（本轮未动）

- `BuildInvoiceSummaryDocs` 写的**本地 CSV/MD** 走的是另一条金额格式化路径，
  本轮没查它是否有同样的浮点问题。
- 多币种混合时 `total` 把不同币种直接相加（USD + CNY），语义上不成立。
  需求没提多币种，属产品决策，未擅自改。

---

## §7ad 本地 CSV/MD 汇总的「账不平」缺陷（2026-10-01）

### 起因

§7ac 末尾自列的相邻风险之一：「`BuildInvoiceSummaryDocs` 写的**本地 CSV/MD**
走的是另一条金额格式化路径，本轮没查它是否有同样的浮点问题」。
`WriteInvoiceSummaryDocs`（`pipeline.go:870`）确实是独立实现，遂查。

### 实测结论：问题**不是**浮点噪声，是舍入口径不一致

先验证「本地路径有没有 §7ac 那个字面量问题」——**没有**：

```
100 x 0.07 -> 裸累加 = 7.000000000000009
           -> %.2f  = 7.00        ← 格式化把噪声掩盖了
```

这正是两条路径的差别：飞书表格（`LedgerRows`）把 float64 **原样 json.Marshal**
所以噪声裸露；本地 CSV/MD 走 `%.2f`，噪声被掩盖。

但换了个方向仍然有错账。真实缺陷是**明细行与合计行用了不同的舍入口径**：

| 位置 | 旧写法 |
|------|--------|
| 明细行 | `fmt.Sprintf("%.2f", inv.Amount)` —— 直接格式化**原始** float64 |
| 合计行 | `total += inv.Amount` 后 `%.2f` —— 对**未舍入的累加值**格式化 |

对 `.005` 结尾的金额，两者会给出不同的分位：

```
明细 1.005 / 2.675 / 8.615
  逐行 %.2f = 1.00 / 2.67 / 8.62  -> 相加 12.30
  裸累加 %.2f                    -> 12.29     ← 差 1 分
```

用户拿计算器把明细行加起来，发现和合计行差 1 分——**账不平**。

### 修复

合计改用整数分累加，且明细与合计共用同一个 `round2` 口径
（复用 §7ac 新增的 `round2`，`ledger.go:63`）：

```go
var cents int64
for _, inv := range invoices {
    amount := round2(inv.Amount)
    cents += int64(math.Round(amount * 100))
    rows = append(rows, []string{ ..., fmt.Sprintf("%.2f", amount), ... })
}
total := float64(cents) / 100
```

这样 `total ≡ sum(round2(每行))`，恒等。`pipeline.go` 补 `math` import。

### 负控对照（2 例转红，报错原文即缺陷）

明细退回不 round2、合计退回裸累加：

```
--- FAIL: TestWriteInvoiceSummaryDocs_DetailSumsToTotal
    detail rows sum to 12.29 but total row says 12.30 — the ledger does not balance
--- FAIL: TestWriteInvoiceSummaryDocs_DetailAmountRoundedToCents
```

已还原。`go clean -testcache` 后 email 包全量 **ok 4.700s**。

### 过程中修正的两处自身错误

1. `readCSVAmounts` 初版按索引 2 解析合计行，报
   `parse total "": invalid syntax`。实测列位后发现**两条行格式不同**：
   明细金额在索引 2，合计行 `合计,,,,,,,%.2f,` 的金额在**索引 7**。
   已按实测修正解析器——**是测试写错，生产代码无误**。
2. 改 `pipeline.go` 时用了 `math.Round` 但没加 `math` import，
   首次跑测试即 build failed，补 import 后通过。

### 证据

- `summary_docs_sum_test.go` 5 个顶层用例
  - `DetailSumsToTotal` —— 核心契约：明细逐行相加 == 合计行，且 MD 与 CSV 合计一致
  - `TwoDecimalHidesAccumulationNoise` —— 钉住「本地路径用 %.2f，噪声被掩盖」
    （与 §7ac 飞书路径形成对照，防止以后有人混淆两者）
  - `DetailAmountRoundedToCents` / `EmptyStillHasTotal` / `RealInvoiceTotals`
- `pipeline.go`：`WriteInvoiceSummaryDocs` 累加逻辑 + `math` import
- `go build ./...` 通过；`go test ./internal/email/ -count=1` → `ok 4.700s`

### 仍未处理

- **多币种合计**：`total` 仍把 USD 与 CNY 直接相加（两条路径都有此问题），
  财务上不成立。需求没提多币种，属产品决策。
- 合计行没有区分币种——若清单里混了两种币种，合计行的语义本身就不成立，
  即使数值上「加起来了」。

---

## §7ae 币种被硬编码成 CNY（多币种合计问题的真正根因）（2026-10-01）

### 起因

§7ac / §7ad 末尾自列的第二条相邻风险：「多币种合计语义」。当时判断是产品决策，
本轮先查**事实**再谈决策。

### 查证：真实数据里现在全是 CNY

```
SELECT currency, count(*), sum(amount) FROM email_invoices GROUP BY 1;
  CNY | 7 | 6060.00
```

只有 CNY，所以「多币种合计」在真实数据上**从未出问题**。但顺着查下去发现
根因比「合计语义」严重得多。

### 真正的缺陷：Currency 字段是硬编码的

`invoice.go:286`（修复前）：

```go
inv := &Invoice{
    ...
    Currency: "CNY",      // ← 硬编码
}
```

而 `reCurrency`（`invoice.go:82`）其实**早就能识别** ISO 4217 代码：

```go
reCurrency = `[¥￥$€£]?\s*(?:CNY|RMB|USD|EUR|GBP|HKD|JPY)?\s*`
```

也就是说：**能识别，但识别结果被丢掉了**。2026-09-30 修 QQ Wallet 英文发票时
（当时 `CNY 126.00` 抽不出金额）只把币种加进正则让它能匹配数字，币种本身
始终没被写进 `Invoice.Currency`。

后果链：

1. 一张 `Total tax-inclusive amount: USD 126.00` 的外币发票，
   `Currency` 被标成 **CNY**（错的）；
2. 共享台账/CSV 的「币种」列显示 CNY（错的）；
3. 合计把 USD 与 CNY **直接相加**——数值上「加起来了」，但没有财务意义。
   §7ac/§7ad 修的是**精度**，修不了**币种语义**：精度对了但币种错了，
   合计依然是错的。

### 修复

1. `reAmountTotal` / `reAnyAmount` 加捕获组取出币种标记；
   `reAnyAmount` 拆成两个分支（符号 / ISO 码），调用点按分支取不同索引。
2. 新增 `normalizeCurrency(mark, fallback)`：符号与代码都映射到 ISO 4217
   （`¥ ￥ 元 RMB → CNY`、`$ → USD`、`€ → EUR`、`£ → GBP` …），
   无法识别时回退 CNY。
3. 两条金额路径（主正则 + 兜底 `reAnyAmount`）都写入 `inv.Currency`。

### 负控对照（精确命中，CNY 用例仍绿）

`normalizeCurrency` 恒返回 `"CNY"`（还原旧行为）：

```
--- FAIL: TestNormalizeCurrency_SymbolsAndCodes
--- FAIL: TestExtractInvoice_ForeignCurrencyNotHardcodedCNY/usd_code      Currency = "CNY", want "USD"
--- FAIL: TestExtractInvoice_ForeignCurrencyNotHardcodedCNY/dollar_sign   Currency = "CNY", want "USD"
--- FAIL: TestExtractInvoice_ForeignCurrencyNotHardcodedCNY/euro_code     Currency = "CNY", want "EUR"
--- FAIL: TestExtractInvoice_ForeignCurrencyNotHardcodedCNY/hkd_code      Currency = "CNY", want "HKD"
--- FAIL: TestExtractInvoice_ForeignCurrencyViaFallbackPath               Currency = "CNY", want USD
```

4 个外币子用例 + 兜底路径全红，而 **cny_stays_cny / rmb_is_cny /
yuan_sign_is_cny 三个 CNY 用例仍绿**——说明改动没有把 CNY 场景弄坏。
已还原。

### 证据

- `invoice_currency_test.go` 4 个顶层用例（含 7 个子用例）
  - `NormalizeCurrency_SymbolsAndCodes` —— 17 个符号/代码映射
  - `ExtractInvoice_ForeignCurrencyNotHardcodedCNY` —— 7 个子用例（4 外币 + 3 CNY 回归）
  - `ExtractInvoice_ForeignCurrencyViaFallbackPath` —— 兜底路径也认币种
  - `ExtractInvoice_RealQQWalletStillParses` —— 真实 CNY 场景回归守卫
- `invoice.go`：`normalizeCurrency`（新增）、`reCurrency`/`reAmountTotal`/`reAnyAmount`、两个调用点
- `go build ./...` 通过；`go test ./internal/email/ -count=1` → `ok 4.707s`（清缓存后）

### 过程中修正的自身错误

1. `ExtractInvoice` 签名是 `(e Email, bodyText string)`——**按值传 Email + 必带 bodyText**。
   初版写成 `ExtractInvoice(e)` 编译报 `not enough arguments`，三处都改了。
2. 用 PowerShell + `[IO.File]::ReadAllText($p)` 做批量替换时，**相对路径基于进程 CWD
   而非 shell 的 `cd`**，报「未能找到路径 C:\workspace\openpocket\internal\...」。
   该脚本因 `$t` 为 null 全部失败且 `WriteAllText` 也未执行——**文件没有被破坏**
   （事后 Read 确认内容完好）。改用 Edit 工具逐处精确修改。
3. 注入负控时只改了函数签名行，残留了原 switch 体，导致 **build failed**。
   编译失败**不构成有效负控**（证明不了测试能捕获行为变化），
   删掉残留块重跑才拿到上面的 6 条 FAIL。

### 仍未处理（需要产品决策）

- **多币种合计语义**本身没动：现在 `Currency` 标对了，但合计行仍把所有币种
  直接相加。正确做法应是**按币种分组各出一个合计**。
  这需要你决定：分组显示 / 强制单币种 / 汇率换算。
- 存量数据 7 张发票的 `currency` 列已是 CNY 且本来就都是 CNY，无需回填。

---

## §7af 需求 4：AI 分类的 importance 未归一化 → 重要邮件静默漏提醒（2026-10-01）

### 起因

需求 4「对其它重要邮件进行提醒」目前卡在 `POCKET_KXMEMORY_BASE_URL` 未配置。
但「卡住」的是**部署配置**，不是代码正确性——代码路径仍可静态核实。
本轮查 `importance` 从上游到落库再到提醒判定的整条链。

### 核实过的部分（无问题）

- `splitReminderCandidates`（pipeline.go:717）是纯函数，
  `case "high"` / `case ""` 的语义清晰，未分类单独计数
- `ListEmailsSince`（store_pipeline.go:64）在**同一次 rows 循环内**同时 append
  `emails` 与 `notified`，两者索引严格对齐，`notified[i]` 对 `emails[i]` 安全
- kxmemory 声明的取值空间是 `high / medium / low`，与 switch 分支一致

### 找到的缺陷：BuildClassifyWrites 只归一化 Category，Importance 完全没管

`classify_run.go:18`（修复前）：

```go
for _, r := range in {
    r.Category = NormalizeCategory(r.Category)   // ← 有归一化
    if r.EmailID == "" || r.Category == "" { continue }
    out = append(out, r)                         // ← r.Importance 原样透传
}
```

后果链（需求 4 的核心失效模式）：

```
kxmemory 返回 "High" / "HIGH" / "高"（LLM 的大小写与写法偏差很常见）
  -> emails.importance 落成 "High"
  -> splitReminderCandidates 的 `case "high"` 匹配不上（pipeline.go:727）
  -> 重要邮件**静默漏提醒**
  -> 报告里 remindersSent=0，且 unclassified 也不涨 —— 看起来「一切正常」
```

最后一行是关键：脏值**既不提醒也不计数**，报告无法解释，
与「邮件确实不重要」在观测上完全一样。这正是需求 4 最难排查的失效。

**DB 层没有兜底**：实测
`SELECT conname FROM pg_constraint WHERE conrelid='...emails'::regclass AND contype='c'`
返回 **0 行**——`emails` 表没有任何 CHECK 约束，脏值会一直留着。

**真实数据现状**（所以从未暴露）：

```
importance 分布: (empty) 275 / medium 111 / high 56 / low 5
```

全是小写规范值，kxmemory 当前返回的取值恰好都是规范的。

### 修复

新增 `NormalizeImportance(raw string) string`，归一到 high/medium/low：

- 大小写（`High`/`HIGH`）、空白（`" high "`）
- 中文（`高`/`重要`/`紧急`、`中`/`普通`、`低`/`次要`）
- 数字档位（`1`/`2`/`3`）、常见同义（`urgent`/`critical`/`normal`）
- **无法识别返回空串**——语义是「未分类」，会被 `splitReminderCandidates`
  计入 `unclassified`，报告里看得见；这比落一个匹配不上的脏值好得多

并在 `BuildClassifyWrites` 里对 `r.Importance` 调用它（与 Category 同规则）。

### 负控对照（2 例转红，脏值形态被直接复现）

去掉 `r.Importance = NormalizeImportance(...)`：

```
--- FAIL: TestBuildClassifyWrites_NormalizesBothFields
    importance "High" was not canonicalized (row e1)
    importance "MEDIUM" was not canonicalized (row e2)
    importance "Low" was not canonicalized (row e3)
--- FAIL: TestBuildClassifyWrites_UnknownImportanceBecomesEmpty
    unknown importance must become empty, got "critical-ish"
```

已还原。`go clean -testcache` 后 email 包全量 **ok 4.551s**。

### 证据

- `classify_normalize_test.go` 4 个顶层用例（24 个归一化输入断言）
  - `NormalizeImportance_CanonicalizesUpstreamValues` —— 大小写/中文/数字/空白
  - `SplitReminderCandidates_UpstreamCaseDoesNotLoseReminders` —— 端到端：
    `high`/`High`/`HIGH` 三者归一后**都**进 toNotify（需求 4 的核心契约）
  - `BuildClassifyWrites_NormalizesBothFields` —— 两个字段都要过一遍
  - `BuildClassifyWrites_UnknownImportanceBecomesEmpty` —— 未知值变空且被计为 unclassified
- `classify_run.go`：`NormalizeImportance`（新增）、`BuildClassifyWrites` + `strings` import
- `go build ./...` 通过；`go test ./internal/email/ -count=1` → `ok 4.551s`（清缓存后）

### 首次运行时测试即 build failed

`NormalizeImportance` 尚不存在时，测试报
`undefined: normalizeImportanceForTest`——**编译失败**。
这只证明「函数缺失」，不构成对行为的负控证据；真正的负控是实现之后
把调用去掉再跑（上面的 2 条 FAIL）。

### 仍未验证

- kxmemory 未部署，**没有真实的上游返回值样本**。归一化表覆盖的是
  常见偏差形态，不是实测到的具体返回值。
- 建议给 `emails.importance` 加 CHECK 约束（`IN ('high','medium','low','')`），
  但这属于 schema 变更，未擅自做。
- 存量 447 封里 `importance` 全是规范值或空，**无需回填**。

## §7ag 前端构建验证（2026-10-01）

需求 7「邮件窗口可查看各类邮件」此前只有代码层证据。本轮装上依赖做了
真实构建验证。

**环境**：`frontend/node_modules` 原本缺失，`npm ci` 装入 293 个包（7s，
本机有缓存）。`node_modules/` 与 `dist/` 都在 `.gitignore:1-2`，不污染仓库。

**结果**：`vite build` 成功（`✓ built in 12.25s`，exit=0），
`EmailInboxView` / `EmailDetailView` / `InvoiceListView` 均产出 bundle。
邮件模块 188 例 `node --test` 全绿。

**两个既有问题（都不是本次邮件改动引入的）**：

1. `vue-tsc --noEmit` 报 3 个错，**全部**在 `src/native/recordingRuntime.ts`：
   `Cannot find module './recording-voice-prompt'` 及由此连带的两个
   TS7006 implicit any。邮件模块类型检查零错误。

2. `vite build` 同样卡在这个缺失模块上。根因：**该文件在本分支的 HEAD 树里
   根本不存在**，但在别的分支上存在——`git log --all --
   frontend/src/native/recording-voice-prompt.ts` 命中 `beffeae` /
   `895d950`，且主仓与另外三个 worktree（`.wt-pdf` / `wt3` / 主仓）都有
   这个文件。即本 worktree 的分支从未包含它，属既有分支分叉。

   为验证邮件代码本身可构建，临时从主仓复制该文件到本 worktree，构建通过后
   已移出工作区（留存于 `%TEMP%\recording-voice-prompt.ts.proof`）。
   **未提交**——把录音修复混进邮件分支是错的。修法应是 cherry-pick
   `895d950`，或 rebase 到包含该提交的基线。

**构建门禁**：首次 `vite build` 失败于「拒绝构建：VITE_API_BASE 为空」。
这是 `vite.config.ts` 里 `assertApiBaseForBuild` 的**有意设计**（防移动端
静默回落到同源，导致 /api 返回 index.html 而非 JSON），不是环境故障。
Web 同源部署确需空值时用 `MOBILE_ALLOW_EMPTY_API_BASE=1` 放行。

**PowerShell 环境**：`npm` / `npx` 会被执行策略拦（`npm.ps1` 不允许运行），
必须用 `npm.cmd` / `npx.cmd`。

**写文件教训**：用 PowerShell 的 `[IO.File]::AppendAllText` 追加中文到本
文档会把内容写成 GBK（整段变乱码）。已从 HEAD 还原。中文内容一律用
编辑工具直接写，或显式 `-Encoding UTF8` 且确认 `UTF8Encoding($false)` 无 BOM。

---

# 第三轮：静默错值类缺陷（2026-10-01 续）

上一轮（§7a~§7ag）修的是「做不出来的功能」。这一轮修的是一类更难发现的：
**代码照常运行、测试照常通过、界面上显示一个看起来完全正常的数字**。

贯穿全轮的方法论是 **负控必须能编译后断言红**。编译失败不算有效负控
（只证明函数缺失）；断言全绿则必须先分清是「断言无效」还是「注入位置错」——
本轮两者各发生一次，见 §7ai-4。

---

## §7ah 本地 CSV/MD 汇总把不同币种直接相加（`39c38fc`）

**起因**：§7ae 只发现 `Currency` 标对了、合计行仍是裸加。本轮查本地路径。

飞书路径（`WriteInvoiceSummaryDocs`）此前已改，但**本地** CSV/MD 汇总走的是
另一条路，没跟着改——典型的「修 A 漏 B」。

`LedgerRows` 原返回一个标量 `total`，内部对所有币种裸加：

```go
total += inv.Amount          // 624.50 + 40.00 EUR = 664.50  ← 不是任何金额
```

**后果**：一张 624.50 CNY + 一张 40.00 EUR 的发票，汇总里显示「合计 664.50」。
这个数在任何币种下都不成立，但它看起来完全正常。

**修复**：`LedgerRows` 改为按币种分组返回 `[]CurrencyTotal`（`ledger.go:34`、
`ledger.go:59`）。**直接删除误导性的标量返回值，而不是加注释**——
留着 `total` 字段早晚会有人再次拿来用。

**决策：单币种时输出形状逐字节不变**。对账习惯是一种真实资产，为了修多币种
而改变单币种的输出会让所有历史对账失效。

**负控**：还原裸加 → 2 例转红；额外返回跨币种标量 → 2 例转红（断言点名
`624.5`）；返回值与表格行对不上 → 转红（`EUR:40`）。

## §7ai 采集回写漏写 category / title（`1a1521f`）

**这是本轮最能说明问题的一个缺陷**，因为它同时具备三个隐蔽条件：

1. `invoiceSelectCols` 读 25 列，UPDATE 只写 11 列——**读得到但不写**；
2. 写入的 11 列看起来都正确，不缺列不报错；
3. 落库后数据「有值」，只是值是错的。

真库复现：采集回写后 `category` 变成默认值 `"其他"`，而邮件实际是 `"交通"`。

**修复**：`UpdateInvoiceHarvest`（`invoice_store.go:255`）补 `category` / `title`
两列，用 `CASE WHEN $14 <> ''` 守卫——空值不写，避免把已有值抹成空。

**判定修好的方法**：不能只看函数返回 nil，**必须从生产读路径
（`GetInvoiceByIDScoped`）读回核对**。这次就是这么做的：UPDATE 之后走一次
和前端一样的 SELECT，确认 `category` 真的落成了 `交通`。

**方法论教训**：「写进去了」和「读出来是对的」是两个断点。只验前者会漏掉
一半的字段丢失类缺陷。

### 负控的两次失败

1. **注入位置错**：我改了 `append` 之后的局部变量，测试全绿。当时判为
   「断言无效」，其实是注入打在了不参与返回值的地方。改正后转红。
2. **断言无效**（163 ID 头那轮）：第一版测试自己手搓 ID 命令，删掉生产代码
   全绿。改为抽出生产函数、由真实 go-imap 客户端驱动后才成为有效负控。

**结论：负控全绿时，先证明注入真的改变了被测行为，再怀疑断言。**

## §7aj AI 分类的 action_reason 被 DTO 静默丢弃（`3aaeaf0`）

契约 `docs/2026-07-02-kxmemory-api-contract.md` 的响应示例明写返回
`action_reason`，但客户端 DTO（`kxmemory/client.go:281`）**没有这个字段**。

Go 的 `encoding/json` 对未知字段**静默丢弃**——不报错、不警告。所以：

- 契约里有这个字段 ✓
- HTTP 响应里有 ✓
- DTO 里没有 → 静默丢弃
- 落库时永远是空串

**真库证据**：162 封已分类邮件，`ai_summary` 有值而 `action_reason`
**162/162 全为空**。这正是「契约里写了、代码里没了」的典型指纹——
上游一直在发，我们一直在丢。

**修复**：`EmailClassificationResult` 补 `ActionReason`（`client.go:281`），
新增 `SetClassificationWithReasonScoped`（`store.go:502`）带
`CASE WHEN $8 <> ''` 守卫。

**为什么新增方法而不改老方法**：老的 `SetClassification` 被规则引擎在
`InsertEmail` 时用来写命中依据。如果让它也覆盖 `action_reason`，AI 分类跑一次
就会把规则引擎写的依据抹掉——**修一个 bug 制造另一个**。老方法保持不写该列，
并有专门用例（`classification_reason_persist_test.go:95`）钉住这个约束。

**负控**：DTO 改 `json:"-"` → kxmemory 包转红；SQL 去掉 `action_reason` 赋值
→ email 包转红。

## §7ak 163 ID 头补自动化证据（`c7f2c26`）

163 邮箱（IMAP）访问要求带一个 ID 头，否则部分账号拒绝连接。原实现有该逻辑，
但**没有能真正驱动它的测试**——只有读代码确认。

**做法**：把拼接 ID 命令的逻辑抽成生产函数 `selectInboxWithClientID`
（`mime.go:66`），测试用**真实 go-imap 客户端**驱动它，而不是自己手搓命令。
这样删掉生产代码才会转红（见 §7ai 的「断言无效」对照）。

顺带修 `pop3_fetcher_test.go` 里 12 行 GBK 乱码注释。

**保留的反向夹具**：`mime_header_decode_test.go` 里的 `鍙戠エ` 是**故意**的
GBK 误解码样本（用来证明解码器能还原中文），不是乱码，已加注释说明，**未改**。

## §7al 发票列表合计与前端金额展示按币种（`b71b4c4`）

三个 `¥` 硬编码 + 一条 SQL 跨币种 `SUM`：

- `InvoiceListStats` 去掉裸 `SUM(amount)`，改 `GROUP BY 1` 按币种分组
  （`invoice_list.go:145`）。单币种保留标量 `Amount`+`Currency`（兼容旧前端），
  多币种时 `Amount` 置 0 并附 `Amounts`。
- 前端抽出 `invoice-money.ts`（`round2` / `normalizeCurrency` / `formatMoney` /
  `sumByCurrency` / `summaryMoney`），供生产代码与测试**共用**——避免测试里
  复制一份算法，两份实现悄悄分叉。
- CSV 导出加币种列。

**决策：跨币种场景下 `Amount` 置 0，而非跨币种之和。** 0 至少不会冒充成正确
金额；前端 `singleAmount` 用 `null` 是同一理由。

**负控**：卡片改回硬编码 `¥` → 1 例转红；SQL 去掉 `GROUP BY` → 4 例全红。

## §7am 外币发票入账按钮（`2233df7`）

财务模块没有 currency 概念，外币入账是**有意的正确保护**。但原实现让按钮
**凭空消失**——用户看不出「为什么不给我入账」。

**改为可见 + 禁用 + 显示原因**（`use-invoice-list.ts:106` 的 `bookBlockReason`），
并且 `book()` 自身也要守卫（前端保护不能替代后端保护）。

**负控**：改回 `v-if="canBook"` → 1 例转红；移除 `book()` 守卫 → 1 例转红。

## §7an 规范文件名补发票号（`5e16d4f`）

需求原文的格式 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` **不足以唯一标识一张票**。
真实后果：同额同日、同对方的两张发票生成**完全相同的文件名**，后者静默覆盖
前者——**凭证永久丢失，且没有任何报错**。

**修复**：`InvoiceFileName`（`invoice_harvest.go:579`）补发票号段：
`{费用类型}-{对方单位}-{金额}-{日期}[-{发票号}].pdf`。发票号是发票的唯一标识，
也符合「凭证可追溯」的意图。无发票号时不加该段（避免留下 `-` 空段）。

**存量文件为什么不用迁移**：`ListHarvestableInvoices`
（`invoice_store.go:196`）只取 `status IN ('new','pending')`，已 `downloaded`
的发票永不被重新采集，所以旧名文件不会被改名、不会被新名顶掉。
这个前提本轮用 `TestListHarvestableInvoices_ExcludesDownloadedAndFiled`
（提交 `3f4b23f`）显式钉住——**将来若加入「重新下载」功能，该测试立刻转红**。

**负控**：去掉发票号 → 2 例转红。

## §7ao 文件名限长防超 Windows MAX_PATH（`11004f6`）

补了发票号后文件名变长，可能超过 Windows `MAX_PATH`（260）导致落盘失败。
`InvoiceFileName` 整名限长 180 字节：保 UTF-8 合法、不丢扩展名、不留悬空分隔符。

**决策：超长时优先砍发票号（尾部）**，保住需求约定的可读部分。

**负控**：去掉长度上限 → 1 例转红（复现出 269 字节的超长名）。

---

## §7ap 本轮新增测试清单

| 文件 | 覆盖 |
|---|---|
| `summary_docs_sum_test.go` | 多币种分组合计、空清单补「合计 0.00」 |
| `invoice_meta_persist_test.go` | 采集回写 category/title（从生产读路径读回） |
| `classification_reason_persist_test.go` | action_reason 落库；老方法不得覆盖规则引擎写的依据 |
| `invoice_list_stats_test.go` | 按币种分组统计 |
| `invoice_filename_collision_test.go` | 撞名、限长、无发票号、已下载不再采集 |
| `imap_clientid_test.go` | 真实 go-imap 客户端驱动 ID 头协议 |
| `kxmemory/classify_action_reason_test.go` | 契约字段必须解出来 |
| `frontend/.../invoice-currency-ui.test.mjs` | 14 例金额/币种 UI |
| `invoice_harvest_test.go` | 扩充 |

**负控汇总（13 组，全部做到能编译 + 断言红）**：

1. 还原跨币种裸合计 → 2 例红
2. 去掉空清单守卫 → 2 例红
3. 额外返回跨币种标量 → 2 例红（点名 `624.5`）
4. 返回值与表格行不一致 → 红（`EUR:40`）
5. DTO 改 `json:"-"` → kxmemory 红
6. SQL 去掉 `action_reason` → email 红
7. 卡片改回硬编码 `¥` → 1 例红
8. SQL 去掉 `GROUP BY` → 4 例红
9. 按钮改回 `v-if="canBook"` → 1 例红
10. 移除 `book()` 守卫 → 1 例红
11. 去掉发票号 → 2 例红
12. 去掉长度上限 → 1 例红（复现 269 字节）
13. 采集器纳入 `downloaded` → 1 例红

**最终验证**：`go build ./...` exit=0；`go vet ./internal/email/` exit=0；
`go test ./internal/email/`（带真库 DSN）→ **ok 27.636s**；
前端邮件模块 **209/209** 全绿。

---

## §7aq 真实数据目录里的孤儿文件与 `invoice_date` 全空

本轮扫 `C:\workspace\openpocket\data\email-invoices\ws_user-admin\`，发现两组
同名内容重复文件：

| 文件 A | 文件 B | SHA256 前 12 位 | 字节 |
|---|---|---|---|
| `其他-云服务开票中心-1280.00-2026-09-28.pdf` | `其他-云服务开票中心-发票抬头-1280.00-2026-09-28.pdf` | `7AB3033721A5` | 1537 |
| `其他-财务部-0.00-2026-09-30.pdf` | `其他-财务部-0.00-2026-10-01.pdf` | `CFA3181C1EE3` | 69 |

**这 4 个文件在 `email_invoices` 表里都查不到（count=0）**——是早期迭代遗留的
**孤儿文件**，不是活跃发票。`email_invoices` 唯一有记录的是
`其他-杭州创客家投资管理有限公司-3500.00-2026-10-01.pdf`
（`inv_1790785758514563200_1`）。

第一组正是 `5e16d4f` 所修缺陷的历史残留：**同额同日的票互相覆盖留下了
带发票号的副本**。这为该修复提供了真实数据佐证。

**另一个发现（尚未评估影响）**：`email_invoices` **所有行的 `invoice_date`
都为空**。也就是说文件名里的日期实际来自**采集当天**（`time.Now()` 兜底），
而不是票面开票日期。需求格式要求日期字段，当前值语义存疑。

**这两项都待用户决定，未擅自删除或改动。**

---

## §7ar 开票日期回填链路：修了但测不到（`c6b5944`）

### 起因

§7aq 记录了「`email_invoices.invoice_date` 所有行为空」。本轮把根因查到底。

**先纠正一条我自己写进代码注释里的、未经证实的断言。**

原 `pipeline.go` 里有这么一段注释：

```go
case hit && inv != nil && inv.InvoiceDate == "":
    // 命中了但**没有开票日期**：IMAP 路径只落 envelope，正文里的
    // 「开票日期」看不到，于是规范文件名退化成下载当天（实测真发票
    // 「其他-杭州创客家…-3500.00-2026-10-01.pdf」，票面其实是 5 月开的）。
```

「票面其实是 5 月开的」——**`2026-05-01` 只出现在
`pop3_uid_test.go:172` 的测试夹具里**，那是我自己为了测 POP3 路径随手填的值。
没有任何真机证据支持这句话，它却以「实测」的口吻写进了生产代码注释。

已改成只陈述可验证的事实：文件名里的 `2026-10-01` 是采集当天、
该行 `invoice_date` 为空。至于票面日期到底是什么，**目前不知道**。

### 查证过程

真库只有 3 行发票，唯一 `downloaded` 的是
`inv_1790785758514563200_1`（`file_source=pdf-url`）：

```
 file_name    = 其他-杭州创客家投资管理有限公司-3500.00-2026-10-01.pdf
 invoice_date = ''            ← 空
 invoice_no   = 26332000008261110741   ← 解析到了
```

`UpdateInvoiceHarvest`（`invoice_store.go:264`）**确实**写 `invoice_date`
（`CASE WHEN $8 <> ''`），所以不是写入丢失。

两条取值来源都不通：

1. **PDF 字节扫描**：`ParseInvoiceDateFromBytes`（`invoice.go:247`）只是对
   原始字节做正则匹配。真实 PDF 的文字在压缩流里，扫不出来。函数注释自己
   就写着「图片旁路无效」。
2. **邮件正文**：正文缓存 `data/email-bodies/em-10435-…bin` 是**加密**的
   （11304 字节，密文），没有 store 的 crypto 密钥无法解析。
   DB 里的 `snippet` 只存了 500 字符，看不到日期。

所以「票面日期是什么」这个问题，**在当前证据下无法回答**。

### 真正的问题：这个修复从来测不到

`pipeline.go:406` 的 `date` 分支正是为这个症状加的修复。但整段逻辑
（`invoiceCandidate` 类型 + 判定 + 预算 + 回填）**全部写在 step1.5 函数的
函数体内**，`invoiceCandidate` 是函数内局部类型——从包外**完全无法构造
这个场景**。

查证：`grep "date" *_test.go` 只在 `pipeline_budget_test.go` 命中，
测的是 `limitInvoiceBodyJobs` 的**预算排序**（date 类优先），
**没有任何测试驱动「拉回正文 → 解析出日期 → 落到文件名」这条链**。

**修了等于没修。** 这与 §7ak 的 163 ID 头是同一类问题：修复存在，
但没有能真正驱动它的测试。

### 修复

抽出两个纯函数（与 163 ID 头同样处理）：

- `invoiceBodyReason(hit bool, inv *Invoice, e Email) string`
  （`pipeline.go:545`）——判定本轮要不要拉原文、拉为什么
- `applyParsedBodyDate(inv *Invoice, text string) string`
  （`pipeline.go:564`）——用正文补日期，**已有日期不覆盖**

后者顺带带来一个语义改进：原实现无条件 `c.inv.InvoiceDate = d`，
而 `date` 分支进入的前提正是「当前没有日期」，所以行为上等价；
但把「不覆盖已有日期」写进函数后，这个保证不再依赖调用点的巧合。

新增 `invoice_date_backfill_test.go`，9 个用例，含端到端断言：
补到的日期必须出现在 `InvoiceFileName()` 的结果里。

**负控（均能编译后断言红）**：

1. 改坏 `date` 分支条件 → `TestInvoiceBodyReason_DateWhenHitWithoutDate` 红
   ```
   invoiceBodyReason = "", want "date" —— 命中发票却缺日期时不拉原文，
   规范文件名会用采集当天冒充开票日期
   ```
2. `applyParsedBodyDate` 改成无条件覆盖 →
   `TestApplyParsedBodyDate_DoesNotOverwriteExisting` 红
   ```
   applyParsedBodyDate = "2026-05-01", want 2026-01-02（已有日期不得被正文覆盖）
   ```

验证：`go build` exit=0；`go vet` exit=0；`go test ./internal/email/`
→ **ok 30.053s**。

### 仍然遗留（需用户决策）

**已 `downloaded` 的行永远不会被回填**：`pipeline.go:385` 对已建档发票
直接 `continue`（幂等跳过）。所以：

- 存量文件 `其他-杭州创客家…-3500.00-2026-10-01.pdf` 的**文件名不会自愈**
- 该行 `invoice_date` 仍然是空

要让存量自愈，需要一条独立的回填路径（重新拉正文 → 补 `invoice_date`
→ 决定是否重命名文件）。这是**写操作**，且涉及重命名已交付给用户的凭证文件，
**未擅自做**。

---

## §7as 本轮的环境教训：三种读视图不一致

worktree 的 `.git` 是文件、索引在主仓且会被 `read-tree` 重新变陈旧。
本轮又遇到一次**更隐蔽**的版本：编辑后的文件在数秒内被不同读取方式
读出**三种不同内容**。

| 读取方式 | 观察到的结果 |
|---|---|
| `read` 工具 | 编辑前的旧内容（行号偏移 9 行） |
| `[IO.File]::ReadAllLines` / `ReadAllText` | 同样是旧内容，但 `mtime` 已是新的 |
| `Select-String` | **正确的新内容** |
| 独立 `GIT_INDEX_FILE` + `git diff` | 正确的改动统计 |

最险的一步是用 `[regex]::Matches(ReadAllText(...))` 搜新函数名，
返回 0 个，一度让我以为三次 `edit` 全部丢失并准备重做——
**而实际全部写入成功**。若照着那个假阴性重写，就会覆盖掉已生效的改动。

**判定纪律（本轮验证有效）**：确认文件内容一律用 `Select-String`，
不用 `[IO.File]::ReadAllText` + 正则；`mtime` 新鲜**不代表**内容是新的。

---

## §7at 需求 5 复核：差点把没问题的代码改坏（`adf622a`）

### 起因

需求 5「单个 PDF 含多张发票，按 A4 规范排版，2x2 或 3x3，打印后可剪裁」
是全部需求里最具体、最该能用产物验证的一条。本轮做一次复核。

### 一次被自己拦下的误判

读 `export_pdf.go` 的 NUp 配置时，我认定代码有 bug：

```go
PageDim: &types.Dim{Width: a4WidthPt / float64(grid), Height: a4HeightPt / float64(grid)}
```

推理链是：`RectsForGrid()`（pdfcpu `nup.go:126-130`）用
`maxX = PageDim.Width`、`gw = maxX/cols` 算目标矩形，矩形从 0 铺到
`PageDim.Width`——**说明 `PageDim` 是输出页尺寸**。那么 `grid=2` 时
`PageDim = 297.64 × 420.9`，输出页就是 **A5，不是 A4**，需求 5 没做到。

**这个推理是错的。** 完整读 pdfcpu 后发现两处相消：

| 位置 | 作用 |
|---|---|
| `nup.go:694-697` | `PageDim *= Grid.Width/Height`（**先把 PageDim 放大**） |
| `nup.go:800-803` | 输出 MediaBox = `PageDim × Grid` |
| `nup.go:126-130` | `RectsForGrid` 再按 cols/rows 把（已放大的）PageDim 切格 |

`RectsForGrid` 单独看确实像「PageDim 是整页」，但它读到的是**已经乘过
Grid 的** PageDim。净效果 = 输出页 = 原 PageDim × Grid = A4。
现有写法正确。

如果没停下来去读 pdfcpu 源码，而是直接按推理「修」，就会把本来正确的
A4 排版改成 A5——**制造一个需求 5 从未有过的缺陷**。
已在 `export_pdf.go` 注释里写清这个陷阱，注明具体行号。

### 真正补上的缺口：3x3 没有尺寸断言

`TestExportInvoiceGrid_DrawsCutLines` 断言了 2x2 的 A4 尺寸
（`absf(w-a4WidthPt) > 1.5`），但 **`TestExportInvoiceGrid_3x3AlsoDrawn`
只断言页数 = 1**。

`PageDim` 是按 `grid` 动态算的，3x3 一旦算错就会输出非 A4 页——
而**页数仍然是 1**。「1 页」看起来完全正常，这正是需求 5 最该被抓住
却最容易被漏掉的错误。已补上尺寸断言。

**负控**：`PageDim.Height` 改成 `a4HeightPt / (grid*grid)` → **4 例转红**，
其中新断言报出：

```
3x3 must still be A4: got 595.28x280.63 pt, want 595.28x841.89
```

验证：`go build` exit=0；`go vet` exit=0；`go test ./internal/email/`
→ **ok 29.991s**（12 个 ExportInvoiceGrid 用例全绿）。

### 顺带清理

`export_pdf.go` 原有两行重复的函数文档注释（`ExportInvoiceGrid` 的说明
被复制粘贴了两次），已删。

---

## §7au 需求 6 复核：只实现了「本地触发」，没有「本地执行」

### 需求原文

> 这些操作可以在设备本地进行，也可以委托服务端进行，**默认放在设备本地进行**。

### 实际实现

顺着执行链逐层查证：

| 层 | 文件 | 实际行为 |
|---|---|---|
| 调度触发 | `email-fetch-host.ts:21` | 本地：页面可见/应用回前台时 `kick()` |
| 原生桥 | `EmailFetchPlugin.java` / `EmailFetchRunner.java` | 本地：`BroadcastReceiver` + 周期调度（15 分钟） |
| **实际收信** | `EmailFetchRunner.java:34-35` | **服务端**：`POST {api}/api/emails/sync` + `/classify` |
| 归类/解析/下载 | `backend/internal/email` | **服务端**：唯一实现处 |
| 邮件缓存 | `lobster-init.ts` / `local-db.ts` | 本地：SQLCipher 加密库（只是缓存） |

`EmailFetchRunner.java:13` 的类注释写得很直白：

```java
/** 后台线程委托 pocketd 收信+归类；设备不直连 IMAP。 */
```

配套的 JS 侧也一致（`email-fetch-plan.ts:1`）：

```ts
/** WebView 不直连 IMAP；原生或 H5 只委托 pocketd。 */
```

**全仓核查**（均为 0 命中）：

- 后端 `backend/internal/email/*.go`：`RunMode` / `ExecMode` / `ExecutionMode` /
  `LocalRun` / `Offload` / `Delegate` 全部无
- 前端设置页 `EmailSettingsView.vue` / `EmailAccountSetup.vue`：无执行位置开关
- i18n 语言包：无「本地执行 / 委托服务端」相关文案
- 前端 `src/**/*.ts`：无任何 JS 侧 IMAP/POP3 直连实现
- 设备上无本地后端进程（「Lobster」是本地加密库，不是 pocketd）

### 结论

| 需求要素 | 状态 |
|---|---|
| 可以在设备本地进行 | ❌ 收信/解析/下载/归类的**执行**全部在服务端 |
| 也可以委托服务端进行 | ⚠️ 服务端是**唯一**方式，不是「也可以」 |
| 默认放在设备本地进行 | ❌ 没有本地执行实现，**无对象可默认** |
| （附带达成）本地触发/调度 | ✅ 后台线程 + 周期调度，不依赖 WebView 前台 |

代码里的**优先顺序**确实是「先试原生、再降级服务端」
（`email-fetch-run.ts:23-35`），但原生那条路也只是把 HTTP 请求换到后台线程发，
并不是把处理搬到本地。这个区别很容易被「本地优先」的措辞掩盖。

### 为什么这条要单独拎出来

前面几轮都只说「需求 6 已实现（本地优先）」。严格讲那句话**只对了一半**：
本地优先的是**触发**，不是**执行**。需求原文说的是「这些操作」可以在本地进行，
「这些操作」指的是收信与处理本身。

### 需要你定（不要自行开工）

真正的「本地执行」有三条路，成本差一个数量级：

1. **把邮件流水线移植到 Kotlin**（Android 原生做 IMAP/POP3 + 解析 + 下载）。
   工作量最大，但设备完全离线可用。
2. **让 pocketd 跑在设备上**（随 App 打包一个本地进程）。
   Go 代码可复用，但要处理进程生命周期、后台保活、凭证加密。
3. **把需求收窄为「本地触发」**，服务端仍是唯一处理方。
   改动最小，与当前实现一致——但这等于修改需求。

在你能拍板之前，**不擅自开工**。

---

## §7av 8 个邮件前端测试从未被执行（`a0266a4`）

### 起因

复核需求 7「邮件窗口可查看各类邮件」时，注意到 `features/email/` 下有一批
`*.test.ts`，而 `__tests__/` 下是 `*.test.mjs`。两种后缀会不会只有一种在跑？

### 结论：`.ts` 测试一个都没跑

`package.json` 的 `scripts` 里与测试相关的只有：

```
"test:native":      node --test src/native/__tests__/{4 个具名}.test.mjs
"test:native:all":  node --test src/native/__tests__/*.test.mjs src/native/__tests__/*.test.ts
"gates":            typecheck && build:gate && test:native && check:vm-gaps && check:i18n && check:icons
```

**没有任何脚本跑 `src/features/email/` 下的测试。** `gates` 只跑
`test:native` 的 4 个具名 `.mjs`。

实测（Node v22.23.2，原生支持跑 `.ts`，所以不是「跑不起来」，是「没人跑」）：

| 范围 | 用例数 | 结果 |
|---|---|---|
| `src/features/email/*.test.ts`（8 个文件） | **33** | 全过 |
| `src/features/email/__tests__/*.test.mjs` | **209** | 全过 |
| 合计 | **242** | 全过 |
| 全仓 `*.test.ts`（55 个文件） | 333 | 332 过 / **1 挂** |

那 33 例包括 `email-categories` / `email-classify-run` / `email-fetch-run` /
`email-inbox-page` / `email-inbox-search` / `email-inbox-select` /
`email-soft-delete` / `invoice-list`——**分类归一化、收信执行、收件箱分页、
搜索、软删除、发票列表**都在其中。它们看起来是覆盖，实际 CI 从不执行。

**顺带纠正我自己之前的说法**：前面几轮我报「前端邮件模块 209/209 全绿」
——这个数字对它所测量的东西是准确的，但**低估了 33 例**。真实覆盖是 242。

### 修复

新增 `test:email` 并接进 `gates`：

```json
"test:email": "node --test src/features/email/*.test.ts src/features/email/__tests__/*.test.mjs"
```

**负控**：把 `normalizeEmailCategory` 的未知值兜底从 `personal` 改成
`marketing` → **exit 1**，

```
not ok 210 - email categories
# tests 242  # pass 241  # fail 1
```

第 210 例正好是 209 个 `.mjs` 之后的第一个 `.ts` 用例，
**证明 `.ts` 确已被纳入**（而不是脚本写对了但 glob 没匹配上）。已还原，
复跑 **242/242 exit 0**。

### 为什么没有把全仓 `.ts` 都接进 gates

实测 55 个 `.ts` 文件 333 例里有 **1 例失败**：

```
not ok 21 - src/features/flashcards/utils/__tests__/flashcardIo.test.ts
```

闪卡模块，与邮件无关，且是既有问题。把它接进邮件分支的门禁会让
`npm run gates` 整体变红，掩盖邮件自己的真实状态。
**留给对应模块自己处理**，本分支只负责邮件。

---

## §7aw 需求 1：手机重启后定时收信静默停止（`cc6753d`）

### 起因

复核需求 1「每天定时或手工进行邮件接收」时，查调度侧。

### 缺陷

`EmailFetchReceiver.schedule()` 用的是：

```java
am.setInexactRepeating(
    AlarmManager.ELAPSED_REALTIME_WAKEUP,
    SystemClock.elapsedRealtime() + gap, gap, pi);
```

**`ELAPSED_REALTIME` 基准的闹钟在设备重启后会被系统清空。**

而原 manifest 里（`AndroidManifest.xml:46-48`）注册的是一个
**没有 intent-filter** 的 receiver：

```xml
<receiver
    android:name=".plugins.EmailFetchReceiver"
    android:exported="false" />
```

全仓 grep `BOOT_COMPLETED` 只命中**一条 `uses-permission` 声明**
（`AndroidManifest.xml:80`）——**没有任何 intent-filter，没有任何开机重排代码**。
那条权限是死的，说明有人想过这件事但没实现。

唯一会重排的地方是前端 `email-fetch-host.ts:11-19` 的 `bindNative()`，
只有**用户打开 App** 时才跑。

### 后果

**手机重启一次，后台定时收信就永久停止**，直到用户手动打开 App。
需求 1「每天定时」直接失效。现象是「偶发不收信」，
与「确实没有新邮件」在观测上完全一样——极难归因。

### 修复

1. manifest 给 `EmailFetchReceiver` 加 `BOOT_COMPLETED` +
   `MY_PACKAGE_REPLACED` intent-filter（`exported="false"` 仍可收系统广播）
2. `onReceive` 收到这两类广播时调 `schedule()` 重排，并顺手补一次收信
   （设备关着的那段时间收不到）
3. 抽出静态纯函数 `shouldReschedule(action)`——Robolectric 未接入，
   测不了 `AlarmManager` 的真实交互，但纯判定可以覆盖
4. `DEFAULT_INTERVAL_MS` 与前端 `GAP_MS` 对齐并断言

### 证据

**环境坑**（本仓库首次跑 JUnit 必踩）：`gradlew :app:testDebugUnitTest`
会挂在

```
Could not read script '.../capacitor-cordova-android-plugins/cordova.variables.gradle'
  as it does not exist.
```

该文件由 `npx cap sync android` 生成。worktree 里没同步过 Android 平台，
所以必须先跑一次 sync（生成目录在 `frontend/android/.gitignore:93` 被忽略，
不会污染仓库）。

**正向**：`:app:testDebugUnitTest --tests EmailFetchReceiverTest`
→ `tests=5 skipped=0 failures=0 errors=0`，**BUILD SUCCESSFUL**

**负控**：`shouldReschedule` 改成恒 `false`
→ `tests=5 failures=2 errors=0`（**能编译并运行**，`errors=0` 证明不是编译失败）：

```
java.lang.AssertionError: 开机广播必须重排闹钟，否则重启后定时收信永久停止
java.lang.AssertionError: 应用升级同样会清空 ELAPSED_REALTIME 闹钟
```

已还原并复跑 **BUILD SUCCESSFUL，5/5**。

### 仍未验证

- **真机行为未验证**：没有在真实 Android 设备上重启验证过闹钟确实重排。
  单元测试只覆盖了「判定逻辑」，没覆盖 `AlarmManager` 真的接受了这个请求、
  也没覆盖开机广播真的送达。
- Android 机型/ROM 差异（国产 ROM 的后台限制）未评估。

---

## §7ax 需求 2：垃圾箱定位的两个关键判定此前零测试（`038f560`）

### 起因

复核需求 2「清理广告与垃圾邮件，**将它们移到垃圾邮件箱**」。
重点是「移到」这个动作——本轮只做只读验证，不跑真实 IMAP MOVE。

### 现状（实现本身是扎实的）

`junk.go` 的真实 IMAP MOVE 链设计得相当完整：

- 定位垃圾箱：`\Junk` 特殊用途属性（RFC 6154）优先 → 7 种常见命名匹配
  → 都没有则 `CREATE "Junk"`
- 移动：优先 `UID MOVE` 扩展，服务器不支持时**自动回退
  COPY + `\Deleted` + EXPUNGE**（`junk.go:22`）
- 部分 UID 失败不影响其余（逐条 MOVE，幂等可重放）
- `cleanup.go` 的 `RunCleanup` 支持 `dryRun`，且有
  `TestCleanSpam_DryRunNeverMoves` 守住「dryRun 绝不移动」

### 缺口：定位链的纯函数一个测试都没有

`findJunkMailbox` 分两步，都已抽成纯函数，但零覆盖：

| 函数 | 位置 | 作用 |
|---|---|---|
| `hasMailboxAttr` | `junk.go:70` | 判断是否带 `\Junk` 属性 |
| `baseMailboxName` | `junk.go:80` | 剥掉层级前缀（`INBOX.Junk` → `Junk`） |

**判错的后果是静默降级**：`MoveUIDsToJunk` 返回 `ErrNoJunkMailbox`，
调用方就只做本地标记，**真实邮件不会被移进垃圾箱**，需求 2 失效且不报错。

### 为什么没有真 IMAP 验证

- `fetcher_greenmail_test.go` 挂在 `//go:build greenmail`，需要 Docker +
  `PG_DSN`，且**只测 `TestSyncGreenmail`（同步），不覆盖 MOVE 路径**
- 本机 Docker 未运行（`docker version` 报 pipe 不存在）
- 真实邮箱按既定约束**只做只读验证**

所以先把可测的判定部分钉住。

### 新增 6 例（`junk_mailbox_test.go`）

重点覆盖两个真实陷阱：

1. **属性是切片**：真实 LIST 响应里一个信箱常带多个属性。只查 `attrs[0]`
   的实现会在 `[NoInferiors, \Junk]` 这种形态上漏判。
2. **`.` 是 IMAP 标准层级分隔符**：`baseMailboxName` 靠它把
   `INBOX.Junk`（最常见形态）归一化成 `Junk`。去掉它就认不出来。

另外断言 `INBOX` / `INBOX.Sent` / `其他文件夹/已发送` **不得**被误判成
垃圾箱——误判会把正常邮件移走。

**负控（均能编译后断言红）**：

1. `hasMailboxAttr` 改成只查 `attrs[0]`
   → `TestHasMailboxAttr_ScansWholeSlice` 红：
   ```
   \Junk 不在首位就没认出来 —— 说明只查了 attrs[0]
   ```
2. `baseMailboxName` 去掉 `.` 分隔符 → **2 例红**：
   ```
   baseMailboxName("INBOX.Junk") = "INBOX.Junk", want "Junk"（IMAP 标准层级分隔符 '.'）
   baseMailboxName("INBOX.Junk")="INBOX.Junk" 未命中任何常见垃圾箱名
   ```

已还原并复跑。验证：`go build` exit=0；`go vet` exit=0；
`go test ./internal/email/` → **ok 33.812s**。

### 仍未验证

- **真实 IMAP MOVE 已端到端验证通过**（`4a06c28`，用 Greenmail standalone
  jar 跑的，**不需要 Docker**）。见 §7ba。
- 13 封判垃圾的真实 MOVE 是否执行，仍等你确认（写操作）。

---

## §7ba 需求 2 的真实 IMAP MOVE 用例已写出，但未跑过（`af63e2e`）

`junk_greenmail_test.go`（`-tags=greenmail`）证明的是纯函数证明不了的三件事：

1. `findJunkMailbox` 在**真实 LIST 响应**里能定位到垃圾箱
2. `MoveUIDsToJunk` 的逐条 MOVE 在真实连接上全部成功
3. **开新连接**复核邮件真的换地方了：
   - 被选的 uid 离开 INBOX、出现在垃圾箱
   - 未被选的仍在 INBOX（**部分移动不误伤**）
   - 移动前已在垃圾箱的没消失

**刻意不复用 `MoveUIDsToJunk` 内部的连接**——否则只能证明同一会话里的
内存状态，证明不了服务端真的变了。收件箱需 ≥3 封而只移动 2 封，
是为了让「部分移动」成为可判定的事实。

### 当前状态：**已真跑通过**（`4a06c28`）

> 本节最初只做到「编译已验证、没跑过」。本轮补上了运行时证据。

**关键发现：不需要 Docker。** Greenmail 有 standalone jar，`java -jar`
直接就能起。本机 Docker Desktop 根本没装（只有 `C:\tools\docker-cli`
的 CLI 壳，没有引擎/服务），但这条路完全绕开了它。

```
下载 com.icegreen:greenmail-standalone:2.1.14（10.47 MB，Maven Central 可达）

java -Dgreenmail.setup.test.all \
     -Dgreenmail.users=huangxutao@kxmail.local:h8pass \
     -jar greenmail-standalone-2.1.14.jar
# → smtp:3025 / imap:3143 / imaps:3993 / pop3:3110 全部监听

env PG_DSN=... go test -tags=greenmail ./internal/email/ -run TestMoveToJunkGreenmail -v
```

**结果：PASS**

```
已有垃圾箱 "Junk" —— 本次验证既有垃圾箱的移动路径
移动前: junkBox="Junk" inbox=15 封 junk=6 封
--- PASS: TestMoveToJunkGreenmail (0.13s)
```

**负控**：把 `MoveUIDsToJunk` 改成「只回报成功、实际不移动」→ **3 条断言红**：

```
uid 5  移动后仍在 INBOX —— 邮件没被移走
uid 16 移动后仍在 INBOX —— 邮件没被移走
垃圾箱邮件数 6，期望 8（移动前 6 + 移动 2）—— 邮件可能丢失或被误移
```

已还原并复跑 PASS。`junk.go` 与 HEAD 的 diff 为空。

### 用例自身修掉的 3 个错误（都是**测试的** bug，不是产品缺陷）

1. **跨信箱拿 UID 比对是错的**——最值得记的一条。IMAP 的 UID 是
   **按信箱独立编号**的，邮件从 INBOX 移入 Junk 会在 Junk 里拿到一个
   **全新的 UID**。我第一版据此断言，于是「邮件丢了」和「误伤既有垃圾箱」
   **同时误报**，而移动其实完全成功（收件箱确实少了 2 封）。

   修法：**INBOX 内部用 UID 比对**（UID 在同一信箱内稳定，可比），
   **跨信箱只用数量判定增量**。

2. `UIDSearch(nil, nil)` 在 `imapclient.searchCriteriaIsASCII` 里解引用 nil
   **直接 panic**，必须传 `&imap.SearchCriteria{}`。这是**运行时**才暴露的，
   编译期完全看不出来。

3. go-imap v2 beta.8 的 API 与直觉差得较远，踩了 4 次才对上：

   | 直觉写法 | 实际 |
   |---|---|
   | `client.Search("INBOX", SearchOptions{}, nil)` | `client.UIDSearch(&imap.SearchCriteria{}, nil)` |
   | `res.UIDs` | `res.All`（且必须 `UIDSearch` 才保证是 `UIDSet`） |
   | `range uidSet` | `uidSet.Nums()`（`UIDSet` 是 `[]UIDRange` 切片） |
   | `client.Fetch(uidSet, []FetchItem{...}, nil).Wait()` | `Fetch(uidSet, &imap.FetchOptions{...})`，`Next()` 两层迭代 + 类型断言 |

### 「没有垃圾箱」从 skip 改成正常场景

最初 Greenmail 默认没建垃圾箱，用例直接 skip 了。但那**正是**
`MoveUIDsToJunk` 走 `CREATE "Junk"` 兜底（`junk.go:127-133`）的路径，
是值得覆盖的真实分支。改成正常场景后，实测日志
`[email/junk] created junk mailbox for huangxutao@kxmail.local`
证实该分支可用。

**这条用例现在同时覆盖两条分支**：有垃圾箱时验证移动，没有时验证 CREATE 兜底。

### 顺带核实：junk.go 的回退注释是准确的

`junk.go:22` 声称「服务器不支持 MOVE 扩展时自动回退 COPY + `\Deleted` +
EXPUNGE」，而 `junk.go:147` 本身只有一句 `client.Move(...)`，**没有任何回退
分支**。一度以为是注释虚报。

查 go-imap v2 beta.8 源码确认**注释是对的**，回退在库里：

```
imapclient/move.go:16      if !c.Caps().Has(imap.CapMove) { cmdName = "COPY" }
imapclient/move.go:25-34   COPY 之后补 STORE \Deleted + (UID)EXPUNGE
```

**但有个更隐蔽的副作用**：`move.go:31` 只有在服务端支持 `UIDPLUS` 时才用
`UID EXPUNGE`；否则退回整箱 `Expunge()`——那会连带清掉 INBOX 里其它已被
标记 `\Deleted` 的邮件。UIDPLUS 支持广泛，暂不处理，仅记录。

**这是本会话第三次「怀疑代码有 bug → 查证后确认是对的」**（前两次：
需求 5 的 PageGrid 语义、需求 2 的 MOVE 回退）。教训一致：
*读到一个可疑点不等于发现了缺陷，必须查到依赖库/协议的原文再下结论。*

---

## §7ay 八条需求逐条核实结论（2026-10-01 收口）

本会话按需求逐条追到代码与产物，不采信「已实现」的声明。汇总如下。

| # | 需求 | 结论 | 关键证据 / 缺口 |
|---|---|---|---|
| 1 | 每天定时或手工收信 | ⚠️ **修了一个真缺陷，真机未验** | `cc6753d`：ELAPSED_REALTIME 闹钟重启后被清空且无开机重排 → 手机重启一次定时收信永久停止。已修 + JUnit 5 例；**真机重启未验证** |
| 2 | 清理广告垃圾邮件移到垃圾箱 | ✅ **已验证（含真实 IMAP）** | `junk.go` 完整（`\Junk` 属性 → 7 种命名 → CREATE；MOVE 扩展失败回退 COPY+\Deleted+EXPUNGE）；`dryRun` 有守卫。`038f560` 补定位判定覆盖。**`4a06c28` 用 Greenmail standalone jar 真跑通了 UID MOVE**（含 CREATE 兜底分支），负控 3 断言红 |
| 3 | 发票收取/解析/下载/飞书 | ⚠️ **除飞书外已通** | XML 重渲染**真跑通**（15 例非 skip，中文字体可找到），生产确实接上（`server_email_pipeline.go:182`）。**飞书投递卡 `POCKET_FEISHU_INVOICE_CHAT_ID` 未提供 + 回调未部署** |
| 4 | 其它重要邮件提醒 | ❌ **卡外部配置** | `POCKET_KXMEMORY_BASE_URL` 未配置 → 对**新邮件**不生效。162 封已分类是历史数据 |
| 5 | 发票导出 A4 2x2/3x3 可剪裁 | ✅ **已验证** | `adf622a`：3x3 补了 A4 尺寸断言（此前只查页数）。A4 排版经核实**本来就是对的**（见 §7at 的 PageGrid 陷阱）。裁切线有内容流级证据 |
| 6 | 本地执行/委托服务端，默认本地 | ❌ **唯一真正未实现** | 只实现了「**本地触发**」（BroadcastReceiver + 15min 周期），执行全在服务端 pocketd。无任何执行位置开关。见 §7au，**待你定方向** |
| 7 | 邮件窗口查看各类邮件 | ✅ **已验证** | 9 个分类 chip（全部/未分类/重要/工作/账单/私人/通知/广告/垃圾），后端 `Uncategorized`/`importance` 过滤均支持。`a0266a4` 把 8 个从未执行的 .ts 测试纳入门禁 |
| 8 | 配置存 DB 归 admin + LWW 同步 | ✅ **已验证，但归属语义待确认** | LWW 前后端都扎实：客户端 `planAccountSync` 纯函数（7 例含「本地 id 不同但同邮箱」），服务端 `UpdateAccountLWTScoped` 把守卫写进 SQL `WHERE updated_at <= $12`（原子）+ 409 `ErrStaleWrite` + 单调时间戳，5 例含跨 scope 攻击者。**但「归到 admin 名下」来自开发态身份回退**（`server_assistant.go:209/219`），生产有真实登录时归属登录用户 |

### 需求 8 的归属语义

需求写「配置到数据库中，**归到 admin 用户名下**」。实际机制不是邮件代码写死 admin，
而是开发态无登录时回退到 `devUser="admin"` / `id="user-admin"`
（`server_assistant.go:209`、`:219`）。真库现状：

```
 user_id   | workspace_id  | count
 user-admin | ws_user-admin |     5
```

生产环境有真实登录时，账户会归属**登录用户**而非 admin。这在多用户部署下更合理，
但与需求字面不完全一致。**未擅自改动**，等你确认按哪种。

### 汇总

- **真正未实现**：1 条（需求 6）
- **实现完成但缺真机验证**：1 条（需求 1）
- **卡外部配置**：2 条（需求 3 的飞书部分、需求 4）
- **已验证**：4 条（需求 2、5、7、8）

**整体不能称完成。**

---

## §7bb 整条 `gates` 门禁的实际状态（`a0266a4` 的连带发现）

§7av 把 `test:email` 接进了 `gates`，但当时**只单独验了新增那一步，没跑过整条链**。
本轮补跑，发现一个必须诚实说明的事。

### 逐步实测

```
npm run gates  →  ✗ 在第一步 typecheck 就断
```

逐个单跑的结果：

| 步骤 | exit | 原因 | 与邮件相关？ |
|---|---|---|---|
| `typecheck` | ✗ 1 | `recording-voice-prompt` 模块缺失（`recordingRuntime.ts` 3 处 TS2307/TS7006） | ❌ 录音模块 |
| `build:gate` | ✗ 1 | `Could not resolve "./recording-voice-prompt"`（同一根因） | ❌ 同上 |
| `test:native` | ✅ 0 | | |
| `test:email` | ✅ 0 | **本轮新增的步骤，绿** | ✅ |
| `check:vm-gaps` | ✅ 0 | | |
| `check:i18n` | ✅ 0 | | |
| `check:icons` | ✗ 1 | `graphic_eq` 在字体子集里合不出连字（`src/features/settings/SettingsView.vue`） | ❌ 设置页图标 |

**3 个红步骤全是既有问题，没有一个与邮件有关。**

### 必须说明的后果：`test:email` 目前是**不起作用**的

`gates` 是 `&&` 串联，`typecheck` 一红就短路，**`test:email` 根本轮不到执行**。

所以 `a0266a4` 那次改动在本分支上的**实际保护力为零**——它只有在
`recording-voice-prompt` 那个缺失模块被解决之后才开始生效。
这一点不能含糊：不能说「邮件测试已纳入门禁所以有保护」。

`check:icons` 的修法脚本自己都写明了：
`node scripts/build-material-symbols-subset.mjs` 重建字体后提交产物。
属录音/设置页的事，**未擅自处理**。

### 对「本分支能否合并」的影响

三条路：

1. 先解决 `recording-voice-prompt`（cherry-pick `895d950` 或 rebase），
   顺带重建字体子集 → 门禁恢复全绿，`test:email` 开始生效
2. 临时把 `test:email` 从 `gates` 摘掉，等基线修好再加回 → 门禁仍红，
   但不会给人「已纳入保护」的错觉
3. 维持现状 → 明确记录门禁在本分支不提供邮件保护

**等用户选**，未擅自改。

---

## §7bd 「硬编码日期 + TTL = 定时炸弹」排查：邮件包**不存在**该风险

有一类缺陷值得单独排查：**测试夹具把时间钉死在绝对日期，而生产代码存在
陈旧/过期上限**（且先判陈旧再判其它分支）。症状是「今天绿、某天永久红，
代码毫无改动」，极易被误判成回归。

### 排查方法与结果

先找生产侧的时间依赖判定：

| 位置 | 判定 | 是否时间炸弹 |
|---|---|---|
| `store_pipeline.go:98` `CleanupStalePendingInvoices` | 只看 `attempts >= maxAttempts`；`now` 仅用于写 `updated_at` | ❌ 否 |
| `invoice_harvest.go:585` `InvoiceFileName` 的日期兜底 | `time.Now().Format(...)`，仅在 `InvoiceDate` 为空时用 | ❌ 否（见下） |
| `fetcher.go:519/525` `syncTrace` | 只做慢步骤日志 | ❌ 否 |
| `fetcher.go:840` POP3 落库时间戳 | 仅写 `updated_at` | ❌ 否 |

再查测试侧的绝对日期夹具（`time.Date(20` 命中 7 处）：

| 文件 | 夹具 | 为什么安全 |
|---|---|---|
| `scheduler_pipeline_test.go:64/86/116/139` | `2026-09-30 09:00` | **注入了 `fakeClock`（`s.nowFn`）**，时钟完全由测试接管，硬编码日期只是锚点。这是正确写法的反面样本 |
| `ledger_test.go:120` | `now := time.Date(2026,9,30,...)` | `LedgerTitle(ws, now)` 把时间当**入参**，断言的是入参回显，纯函数 |
| `ledger_test.go:170` / `store_workspace_test.go:267` | UTC 锚点 | 纯夹具数据，不参与时间比较 |

**结论：邮件包不存在这类定时炸弹。** 调度测试尤其值得肯定——它没有
去赌真实时钟，而是注入了 `fakeClock`。

本轮新写的三个测试文件（`invoice_date_backfill_test.go`、
`junk_mailbox_test.go`、`EmailFetchReceiverTest.java`）也都不含
`time.Now()` 断言，属同一安全模式。

**这是一次「查了、结论是没有」的记录**——写下来是为了下次不必重查，
也为了标明风险已排除，而不是留白让人重新怀疑。

---

## §7be step1.5 取正文每封挂 150s：降级通道的两个真缺陷（`843b3c4`）

### 起因

`TestSyncGreenmail`（`fetcher_greenmail_test.go`）首次真正跑起来时，日志是：

```
step1.5 scanned=138 rawBodyFetches=4 fetchFailed=4
每封耗时 150.06 / 150.07 / 150.09 / 150.06 s
错误：imapwire: expected SP, got "{"; textproto fallback: read greeting: EOF
```

**每封恰好 150.0x 秒**这个数字本身就说明问题：不是随机网络抖动，是某个固定上界
在起作用。之前一直记为「可能是并发压 Greenmail 的假象，也可能是真实缺陷」，
这次用探针把它钉死了。

### 抓原始响应：一个 stdlib 探针就够

不解 go-imap，直接用 `crypto/tls` + `bufio` 手写一遍 `LOGIN / ID / SELECT /
UID FETCH <n> BODY.PEEK[]<0.8388608>`，把响应行原样打印：

```
"greeting <- * OK IMAP4rev1 Server GreenMail v2.1.14 ready"
"A2 ID   -> A2 BAD Invalid command."          <- 163 为什么要发 ID 的又一次印证
"FETCH  -> * 18 FETCH (UID 26 BODY[]<0>{1510}"   <- 注意这里
```

`BODY[]<0>` 与 `{1510}` 之间**没有空格**。RFC 3501 的 `msg-att` 里 literal 前的
SP 是可选项，常见服务器都会给；但 imapwire 在 section 之后强制期待 SP，于是
`expected SP, got "{"`。**主路径失败 → 掉进降级通道 → 降级通道又挂 150s。**

### 缺陷 A：建连之后零读超时（生产影响）

`fetchRawByTextproto` 原来只有 `net.Dialer{Timeout: 30 * time.Second}`，而
`Dialer.Timeout` **只管三次握手**。握手之后 `br.ReadString('\n')` 能挂多久完全看
服务器脸色，而且**不看 ctx** —— ctx 只喂给了 `DialContext`。

后果：任何「accept 了 TCP 但一句话不说」的服务器可以让单封邮件**永久**阻塞。
这直接违反同文件 `maxMessageBytes` 注释里自己写下的不变量：

> 也不能让一轮流水线没有上界。

讽刺的是 `fetcher.go:118-122` 已经把这个坑写得很清楚（「`net.Dialer.Timeout`
只管建连，建好之后的读操作没有任何时间上限」），并且为此造了 `deadlineConn`，
**只是 go-imap 主路径套了、降级路径漏了**。

修法：复用 `deadlineConn`（滚动空闲 60s + 绝对硬截止 45s），再加一个 ctx 取消
看门狗（ctx 一到期就把 deadline 钉到过去，正在阻塞的读立刻返回）。

### 缺陷 B：TLS 判定写死 `acc.IMAPPort == 993`

对 qq/163 恰好正确（生产全是 993），但对任何非 993 的**明文**端口是必然的协议
错配：客户端说 TLS、服务端等明文 greeting，双向互等，最后以 `read greeting: EOF`
收场。Greenmail 的 IMAPS 端口是 3993，正好落进这个坑。

改成与 `fetcher.dial` 走**同一条** `isPlainIMAPPort` 规则。**不把 993 放掉是
刻意的** —— 那等于把加密通道悄悄降级成明文，163 上会变成明文外发 LOGIN 密码。
取的是「与主路径一致」，不是「尽量猜」。

### 效果（同一个用例，前后对照）

| | 修复前 | 修复后 |
|---|---|---|
| 4 次取正文 | 各 **150.06s** 后失败 | 各 **~90ms** 成功 |
| `fetchFailed` | 4 | **0** |
| `email_invoices` 行 | 0 | **4** |
| `HarvestAll` | 无事可做，processed 恒 0 | Processed=4（2 downloaded / 2 pending）|
| 用例结果 | 8 分钟预算耗尽后失败 | **PASS 0.44s** |

顺带拿到需求 3 命名格式的**首次真实 IMAP 端到端**产物：

```
通信-开票中心-128.00-2026-09-24.pdf
其他-杭州创客家投资管理有限公司-3500.00-2026-09-24-26332000008261110741.pdf
```

末尾那串是发票号，来自撞名保护（`5e16d4f`）。

### 新增 3 例（不依赖 Greenmail / PG / Docker）

`mime_textproto_deadline_test.go`：

1. `TestUnblocksWhenServerNeverGreets` —— 服务器 accept 后一句话不说且**不关
   连接**，ctx 800ms，断言 5s 内返回且错误落在 `read greeting`。
2. `TestNonPlainPortGetsImplicitTLS` —— 随机端口的纯明文服务器，断言错误是
   `tls handshake`（而不是 `read greeting`）。
3. `TestPlainPortHappyPathFetchesBody` —— 1143 明文端口上完整跑一遍协议，正文
   逐字节相等。响应刻意模仿 Greenmail（`BODY[]<0>{30}`，partial 与 literal
   之间无空格）。

### 负控：三条都验证过「能红」，且都是编译通过后转红

| 注入 | 结果 |
|---|---|
| 摘掉整套 deadline 机制 | `ctx 已到期 800ms，实际耗时 20.0010313s 才返回` → FAIL |
| `!isPlainIMAPPort(addr)` 改回 `acc.IMAPPort == 993` | `期望 tls handshake 失败，实际 read greeting: ... i/o timeout` → FAIL |
| `parseBodyLiteralSize` 的 `return size` 改 `return 0` | `no BODY literal in UID FETCH response` → FAIL |

**两条过程中的自我纠正**（都是负控暴露出来的，不是事后想通的）：

- **第一次负控没红，是我注入不完整，不是断言无效。** 只把 `conn = dc` 摘掉、
  留着 ctx 看门狗，测试照样通过 —— 因为 `deadlineConn.SetDeadline` 会透传到被
  包装的底层 conn，看门狗照样能打断读。必须整套摘掉才暴露。这条正好印证
  「负控全绿先分清是断言无效还是注入位置错」。
- **第三次负控第一次是编译失败**（`syntax error` / `declared and not used`），
  按纪律**不算有效负控**，补 `_ = size` 后重跑才拿到真正的红。

### 用例自身修掉的 3 个 bug

1. **`count invoices` 用了 60s 的 `ctx` 而不是 `ctxCand`** —— 我自己新写的代码
   里的 ctx 复用错误，让后续断言因 deadline 假失败。
2. 假 IMAP 服务器用 `strings.Contains(line, "ID ")` 分派命令，结果
   `A2 UID FETCH ...` 里也含 `"ID "`，FETCH 被误判成 ID 命令 → 客户端在 FETCH
   循环里读到 `A0 BAD Invalid command.` 然后干等 20s。改成按 tag 前缀分派。
3. 假服务器写 literal 时漏了闭合 `}`（`{30` 而非 `{30}`）。`parseBodyLiteralSize`
   正确地拒绝猜测并报 `no BODY literal` —— 这条恰好成了上面第三个负控。

### 撤掉一条臆测的注释

`fetcher_greenmail_test.go` 原来写着「Greenmail 测试邮件的附件是占位 %PDF 内容，
harvester 会落到 failed 但 processed>0」。那是**写测试时臆测的、从未真跑过**的
说法。实测恰好相反：附件是真 PDF，2 封走 **downloaded**、2 封因无可用附件走
**pending**。已按实测改写。

---

## §7bf 真实邮箱只读探针：主路径是好的，但 master key 分布不一致（2026-10-01）

### 目的

§7be 留下一个无证据的问题：go-imap 主路径在 Greenmail 上必然失败
（`BODY[]<0>{n}` 少一个空格），那**真实 qq/163 呢**？若同样失败，主路径等于
长期闲置，每封发票正文都靠那条手搓的 textproto 通道兜底 —— 这决定了要不要
为主路径补一条「重试不带 partial」的中间路径。

### 只读是设计出来的，也是**测出来**的

新增 `realprobe_test.go`（`-tags=realprobe` 手动启用，默认不跑）：

- 只发 `LOGIN / ID / SELECT / UID SEARCH / UID FETCH BODY.PEEK[]`。
  不发 `STORE / MOVE / COPY / EXPUNGE / DELETE` 任何一条。代码里
  `FetchMessageRaw` 的 `FetchItemBodySection.Peek` 为 true，不置 `\Seen`。
- **不靠承诺，靠前后快照证明**：跑前记下 INBOX 的 UNSEEN 计数与目标邮件的
  flags，跑完再记一次，逐项必须相等，不等就 fail。刻意不比 UIDNEXT ——
  探针运行期间真邮箱可能收到新邮件，那不是探针造成的。
- 不写库：本文件只 `SELECT`。
- 凭据解密刻意**不**用 `EnsureMasterKey` —— 它找不到 key 时会**新建**一个，
  万一 dataDir 指错就会在真实目录里留垃圾。这里 `os.ReadFile` 显式读候选 key。

### 结果：真实账户主路径全部可用

```
KEY C:\workspace\openpocket\data\email_master.key:              可解出凭据的账户数 = 0
KEY C:\workspace\openpocket\backend\data\email_master.key:      可解出凭据的账户数 = 0
KEY C:\workspace\openpocket\wt3\backend\data\email_master.key: 可解出凭据的账户数 = 5

ACCT ...-2  56551681@qq.com        uid=10455      主路径可用  耗时=484ms  body=3300 字节   状态未变=true
ACCT ...-1  huangxutao@kxpms.cn    uid=11         主路径可用  耗时=1.101s body=10399 字节  状态未变=true
ACCT ...-3  feikemanager@163.com   uid=1669791329 主路径可用  耗时=268ms  body=12123 字节  状态未变=true
ACCT ...-5  kimmy.huang@163.com    uid=1298896143 主路径可用  耗时=357ms  body=41656 字节  状态未变=true
ACCT ...-4  feikemanager1@163.com  库里 0 封邮件，无 uid 可试
```

**结论：§7be 里「主路径可能长期闲置」的担心不成立。** Greenmail 是 quirk
server，真实 qq/163 都正常给了空格，主路径 268ms~1.1s 直接取回正文。
所以**不需要**为它补「重试不带 partial」的中间路径 —— 那会是为不存在的问题写代码。
降级通道仍然值得留着（它兜的就是这类解析器兼容问题），但它现在是**备份**，
不是主承重路径。

### 顺带查出的真问题：master key 分散在三处，且只有一把对得上

| 目录 | `email_master.key` | 发票文件 | 正文缓存 | 能解开真实库 5 个账户 |
|---|---|---|---|---|
| `openpocket\data` | 有 | 132 个 / 1.68 MB，**最新 20:41** | 41 个 | **0** |
| `openpocket\backend\data` | 有 | 无 | 无 | **0** |
| `openpocket\wt3\backend\data` | 有 | 10 个 / 3 KB，最新 19:56 | 无 | **5** |
| `openpocket-wt-maildeploy\backend\data`（**当前 pocketd.exe 所在目录**） | 有 | 无 | 无 | **0** |

也就是说：**当前活跃的数据目录（发票文件 20:41 还在写）和能解开凭据的 key
不在同一个目录**。`pocketd.exe`（PID 33948，来自 `wt-maildeploy\backend\
.verify-bin\`）那把 key 也解不开。

**但不能就此断定线上 IMAP 登录是坏的**，理由有两条，必须都摆出来：

1. §7b 记录 2026-10-01 凌晨真实邮箱接入 **6/6 账户连通**。若那把 key 解不开
   凭据，就不可能连通。所以线上进程多半是通过 `POCKET_EMAIL_MASTER_KEY`
   **环境变量**拿 key 的（`EnsureMasterKey` 是 env 优先），磁盘上那几把都是
   历史残留。
2. 我无法读取别人启动的进程的环境变量来证实第 1 条。**这一点是推断，不是实测。**

**严重性（已查证，不要高估也不要低估）**：邮件代码在解密失败时**只返回错误、
不回写凭据** —— `fetcher.go:338-343`、`fetcher.go:557-563`、
`imap_resolve.go:71-76` 三处都是 `return fmt.Errorf("decrypt credential: %w", err)`，
没有「自愈成默认值」的分支。所以这**不是**配置被毁的数据丢失事故，而是：

> 任何回退到 `<dataDir>/email_master.key` 的进程（脚本、诊断工具、换一个
> 工作目录启动的 pocketd）都会**一个账户都连不上**，而库里的配置看上去完好。

这正是我此前记过的教训（多个 data 目录 → 各自一把 key → 互解不开）的又一次
实证。**处置**：要么把 `POCKET_EMAIL_MASTER_KEY` 固定注入到所有启动方式，
要么把唯一正确的 key 复制到真正在用的 data 目录并在文档里写死位置。
**未擅自处置** —— 这需要知道线上到底怎么启动的。

### 过程中我自己犯的一个错（差点误报成数据事故）

探针第一版 SQL 写的是 `AND deleted_at IS NULL`，结果 5 个账户全部
`no rows in result set`。我看了一眼就差点下结论「真实邮件全被软删除了」。

实际：`deleted_at` 是 `bigint NOT NULL DEFAULT 0`，**0 才是未删除**
（`store_inbox.go:15` 的 `idx_emails_alive` 偏索引就是这么写的）。
真实分布是 138 行**全部 `deleted_at = 0`**，即一封都没删。

教训：**看到「查询返回 0 行」时，先怀疑自己的谓词，再怀疑数据**。

### 顺带确认：`test:email` 本身是全绿的

`npm run test:email` 单独跑：**242 tests / 242 pass / 0 fail**。

所以 §7bb 记的「`test:email` 目前保护力为零」需要精确化：不是它自己有问题，
而是 `gates` 用 `&&` 串联、在 typecheck 就短路，它**根本没机会跑**。这让三个
处置选项的利弊变清楚了 —— 「暂时从 gates 摘掉」今天不会损失任何东西（本来
就没在跑），只有「先修基线」能真正恢复保护力。

---

## §7bg master key 拿错是静默的：加一道启动自检（2026-10-01）

### 起因：§7bf 那个「推断」被代码证伪了一半

§7bf 说「线上多半是靠 `POCKET_EMAIL_MASTER_KEY` 环境变量拿 key 的」，
并注明那是推断。查完代码，结论要改写成**可证的事实**：

- `config.go:258`：`EmailFetchEnabled` **默认 true**。
- `config.go:441-443`：`EmailFetchEnabled && EmailMasterKey == ""` → 直接
  `return error`。
- `main.go:59-61`：`cfg.Validate()` 出错就 `log.Fatalf`，进程根本不启动。

所以**只要邮件抓取是开着的，`POCKET_EMAIL_MASTER_KEY` 就一定被设了**
（否则 pocketd 起不来）。`main.go:414-424` 那段「回落到磁盘 key 并打 WARN」
的代码，只有在 `EmailFetchEnabled=false` 时才可能走到。

但这引出一个更值得修的东西：**闸门只查「非空」，不查「对不对」。**
填一把**错**的 key，`Validate()` 照样通过，进程照常启动，调度器照常打印
`Email scheduler started`，然后每个账户每次同步都撞
`decrypt credential: …`（`fetcher.go:338`）。从外面看「服务在跑」，
实际一封新邮件都收不到，而且没有任何一行日志指向真正的原因。

这不是假想 —— §7bf 实测到本机 4 处 `email_master.key` 里 3 把是错的，
**用错的那把启动全程零报错**。

### 修法：启动时真解一次

新增 `credential_check.go` 的 `CheckCredentials(ctx, store, crypto)`，
在 `main.go` 构造好 `emailCrypto` 之后调用：

| 判定 | 触发条件 | 日志级别 |
|---|---|---|
| `AllFailed()` | 有凭据且**一把都**解不开 | **ERROR**，点名首个失败账户 + 原因 + 处置提示 |
| 部分可解 | 有一把解不开、一把能解开 | WARN |
| `AllDecryptable()` | 无失败 | INFO |
| 0 账户 / 全占位 | 全新部署 | INFO，`nothing to verify` |

边界写死在注释里，别过度承诺：

- 只验「能不能解密」，**不验**密码是否仍有效、IMAP 是否可达。
- **不修**任何东西，只把静默变成一行日志。
- 只扫 `enabled = TRUE` 的账户（停用账户的坏凭据不该报警）。

### 三个计数，不是一个

账户实际分三类，混在一起算会出现两种误判：

1. 能解开 → 成功
2. 空凭据 / `oauth-pending-no-credential` → **不是 key 错了，是还没配完**
3. 有密文却解不开 → key 错了

所以 `Decryptable` / `Skipped` / `Failed` 分开，且 `AllFailed()` 必须把
`Skipped` 排除掉，否则全新部署会被误报成「master key 拿错了」——那是会让
运维去追一个不存在的问题的假警报。

### 测试当场抓到一个真缺陷

第一版实现写成「先解密，再看解出来的明文是否为空」。写完用例一跑就红：

```
--- FAIL: TestCheckCredentials_PlaceholderCredentialsAreNotFailures
    两个占位凭据都该记为 Skipped，实际 {Accounts:2 Decryptable:0 Skipped:1
    Failed:1 FirstError:ciphertext too short}
```

**空密文根本走不到「解密成功」那一步**：`credential_encrypted` 是
`TEXT NOT NULL` 但值可以是 `''`，而 `DecryptString("")` 直接返回
`ciphertext too short`。于是「还没配凭据」被算成了「解密失败」。
判据必须在 `DecryptString` **之前**。

这是可达的生产状态（新建账户尚未配置凭据），不是假想边界。

### 负控

把 `if strings.TrimSpace(r.CredentialCipher) == "" { res.Skipped++ }`
改成 `res.Failed++` → `TestCheckCredentials_PlaceholderCredentialsAreNotFailures`
转红（`Skipped:1 Failed:1`）。6 例全绿在改回去后复验。

**过程中的一个操作失误**：负控注入后我用 `git checkout --` 还原，但
`credential_check.go` 是**未跟踪的新文件**，`git checkout` 对它无效
（`did not match any file(s) known to git`），而且我把错误输出吞掉了。
结果下一次全包测试莫名 FAIL（30.8s）——真因是负控代码还留在文件里，不是
新代码有问题。**教训：还原未跟踪文件不能用 `git checkout`，要么先
`git add`，要么留一份备份并核对哈希。**

### 真实库上的实际输出（不是想象的文案）

`realprobe` 拿 3 把真实存在的 key 跑同一个自检：

```
KEY ...\wt-maildeploy\backend\data\email_master.key: 可解出凭据的账户数 = 0
  AllDecryptable=false AllFailed=true -> MASTER KEY LOOKS WRONG: none of the 7
  configured email account(s) decrypt (first: account=acct-...-2
  56551681@qq.com: cipher: message authentication failed)

KEY C:\workspace\openpocket\data\email_master.key: 可解出凭据的账户数 = 0
  AllDecryptable=false AllFailed=true -> MASTER KEY LOOKS WRONG: …（同上）

KEY C:\workspace\openpocket\wt3\backend\data\email_master.key: 可解出凭据的账户数 = 5
  AllDecryptable=false AllFailed=false -> partial: 5/7 configured email
  account(s) decrypt with the current master key (first failure:
  account=acct-greenmail-junk …: cipher: message authentication failed)
```

第三行正是 `partial` 分支在真实数据上的验证：2 个 greenmail 测试账户是用
临时目录的 key 加密的，所以「5/7」，而自检**没有**把它误报成「key 拿错了」
—— 换成两计数实现这一格就会误报。

### 仍未处置

这把正确的 key 到底该固定注入到哪些启动方式、还是复制回真正在用的 data 目录，
仍然需要先知道线上怎么启动 pocketd。见 §7az 第 11 条。
本次只加「看得见」，不自动搬 key、不自动改配置。

---

## §7bh 读线上日志：master key 推断被证实，同时发现我的测试在污染生产库（2026-10-01）

### 起因

§7bf/§7bg 都在讨论「线上到底受不受 master key 影响」，但都停在推断。
这次直接读**正在跑的那个实例**的日志。

日志文件 `openpocket-wt-maildeploy\logs\pocketd-18099d.err.log`（PID 33948，
端口 18099，20:18:42 启动，正在实时写入）。读它要用允许共享读的句柄 ——
`[IO.File]::ReadAllBytes` 会报「正由另一进程使用」：

```powershell
$fs = New-Object System.IO.FileStream($f, 'Open', 'Read', 'ReadWrite')
$sr = New-Object System.IO.StreamReader($fs, [Text.Encoding]::UTF8); $t = $sr.ReadToEnd()
```

### 发现 A：master key 那条推断，现在有实据了

启动头部逐行是：

```
20:18:42 data dir = C:\workspace\openpocket\data
20:18:42 Postgres pool initialized (schema="opencode_pocket")
20:18:42 INFO: POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled
20:18:42 Email scheduler started (fetch_enabled=true, kxmemory=false, ...)
```

**没有** `WARN: POCKET_EMAIL_MASTER_KEY not set; using auto-generated key at …`
这一行。而那行在 `main.go:423`，位于 `main.go:472` 的
`Email scheduler started` **之前** —— 两者都出现/不出现的顺序是确定的。

所以：线上进程确实带着 `POCKET_EMAIL_MASTER_KEY` 环境变量启动，
`data dir` 指向 `C:\workspace\openpocket\data`（与 20:41 还在写发票的那个
目录一致），磁盘上那把解不开的 key **根本没被用上**。

§7bf 写的「线上多半是靠环境变量拿 key 的（推断，非实测）」——
**这条推断现在升级为实测**。同时它也说明 §7bg 那道自检的价值：它防的不是
「环境变量没设」（那种情况 `Validate()` 已经拦住了、进程根本起不来），
而是「环境变量设了但**值是错的**」。

顺带确认需求 4 在生产上确实是关的：
`POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled`。

### 发现 B（更刺眼）：我自己的测试在污染生产库

同一份日志里，`decrypt credential` 错误的来源是：

```
20:34:42 [email/scheduler] sync acct-greenmail-junk failed: decrypt credential: cipher: message authentication failed
21:03:42 [email/scheduler] sync acct-greenmail-realrun failed: ...
21:42:42 [email/scheduler] sync acct-greenmail-realrun failed: ...
```

**每 60 秒一次，从 20:34 连续报到 21:42，一个多小时几百行。**
`acct-greenmail-junk` / `acct-greenmail-realrun` 是 §7ba/§7bf 那两个
Greenmail 集成用例插进**生产 schema** 的账户，凭据用 `t.TempDir()` 里的
临时 master key 加密 —— 线上 pocketd 拿到的是另一把 key，于是每轮同步都失败。

而同一时刻 5 个真实账户全部正常：

```
[email/fetcher] feikemanager1@163.com sync trace total 292ms
[email/fetcher] 56551681@qq.com      sync trace total 549ms
[email/fetcher] huangxutao@kxpms.cn  sync trace total 1.121s
```

也就是说：**563 行日志里绝大多数是测试垃圾，真正的故障会被它淹没。**
这不是「顺手发现的小瑕疵」，是把自己的验证工作变成了生产噪声。

### 修法：跑完删掉自己的账户

两个用例各加 `t.Cleanup`（`t.Fatal` 也会执行），子表先删、父表后删。

只在开头清理是**不够**的：开头那次只能保证「重复跑幂等」，不能保证
「跑完不留」。

### 清理本身连踩两个坑，都是静默失败

用例照常 PASS、账户照样留在库里，日志里只有一行 `t.Logf` 级别的提示：

1. **复用测试里的连接池 → `closed pool`。**
   `defer pool.Close()` 在测试函数返回时**先于** `t.Cleanup` 执行，池已经关了。
   改成清理时用 DSN 自己开一条短连接。
2. **三张表统一写 `WHERE account_id=$1 OR id=$1` → 父表报错。**
   `email_accounts` 是父表，只有 `id`，没有 `account_id`，于是
   `ERROR: column "account_id" does not exist (SQLSTATE 42703)`。
   改成按表指定列。

两条都是**测试看着全绿、实际什么都没删**。这类「清理失败」比清理代码本身
更容易骗过人 —— 它不会让任何断言变红。

### 验证

两个用例 PASS 且**零 cleanup 错误**：

```
=== RUN   TestSyncGreenmail
--- PASS: TestSyncGreenmail (0.41s)
=== RUN   TestMoveToJunkGreenmail
--- PASS: TestMoveToJunkGreenmail (0.16s)
```

共享库现状：

```
email_accounts: 5 个，全部是真实邮箱（无 acct-greenmail%）
emails    where account_id like 'acct-greenmail%' : 0
invoices  where account_id like 'acct-greenmail%' : 0
emails    非测试账户                            : 120   ← 真实邮件一封没少
```

（顺带修正一个此前记录的数字：`emails` 表现共 138 行，其中 18 行是 greenmail
测试邮件，真实邮件是 **120** 行，不是之前文档里写的 416。）

**闭环验证在日志上**：清理前最后一次测试账户报错是 `21:45:42`，之后：

```
21:46:42 [email/fetcher] feikemanager1@163.com sync trace total 298ms
21:46:42 [email/fetcher] 56551681@qq.com      sync trace total 490ms
21:46:43 [email/fetcher] huangxutao@kxpms.cn  sync trace total 1.092s
21:47:42 [email/fetcher] feikemanager1@163.com sync trace total 259ms
21:47:43 [email/fetcher] 56551681@qq.com      sync trace total 534ms
21:47:43 [email/fetcher] huangxutao@kxpms.cn  sync trace total 1.19s
```

只剩真实账户的同步记录，**再没有一条 `decrypt credential`**。
即：测试删除 → 共享库清干净 → 线上调度器下一轮不再扫到 → 日志恢复干净，
三段都验过。这条链只看数据库是验不出来的 —— 数据没了但调度器还可能缓存，
必须回到日志确认。

### 顺带记一条偶发现象

```
21:38:02 [email/fetcher] imap login huangxutao@kxpms.cn failed:
        cannot read tag: read tcp 192.168.31.20:56534->36.158.243.217:993: i/o timeout
        — trying POP3 fallback (budget -10s left)
21:38:02 [email/fetcher] huangxutao@kxpms.cn SLOW step login took 1m20.001s
21:38:02 [email/scheduler] sync ...-1 failed: imap failed and no time left for POP3 fallback
```

单账户登录整整挂 80 秒后超时，POP3 回退因预算已耗尽而跳过，**该账户这一轮
没同步成功**；但下一个 60 秒轮次 1.121s 正常。属于 §7g/§7j 同族的瞬时网络
停顿，**已被 deadlineConn 兜住没有泄漏连接**（同一时刻没有出现连接堆积），
但「单账户 90s 上界耗尽后 POP3 回退拿不到预算」这一条值得单独记一笔：
回退路径在最需要它的时刻恰恰没有预算。

---

## §7bi 「绝对硬截止」在代码上从未成立（`51d04aa`）

### 起因：一行 80.001s 的日志

§7bh 顺带记了一条没解释的现象：

```
21:38:02 [email/fetcher] imap login huangxutao@kxpms.cn failed:
        in response: cannot read tag: read tcp 192.168.31.20:56534->36.158.243.217:993: i/o timeout
        — trying POP3 fallback (budget -10s left)
21:38:02 [email/fetcher] huangxutao@kxpms.cn SLOW step login took 1m20.001s
        (total 1m20.167s) — 该阶段以失败/提前返回结束
```

login 阶段整整 **80.001s**。而 `fetcher.go:138` 的 `imapHardTimeout` 写的是
`45 * time.Second`，注释把它称作「**绝对**寿命上限，不看有没有活动」。

两个时间的量级完全对不上：45s 的绝对上界，不可能跑出 80s。

> 顺带一个更正：`SLOW step` 那条带阶段署名的日志**不在本分支上**，它是并发
> 会话在 `feat/mail-config-deploy`（`openpocket-wt-maildeploy`）上加的 —
> 那边的 `syncTrace` 多一个 `cur` 字段，并在 `done()` 里补报「最后一个阶段」的
> 耗时。正因为有了它，线上这 80s 才有名字。两边的超时**常量完全相同**
> （10s / 60s / 45s / 70s），所以问题在这两条分支上都存在。

### 根因：滚动续期没有���硬截止夹住

`deadlineConn.start()` 的看门狗有三个分支：

```go
switch {
case !c.hard.IsZero() && now.After(c.hard):
    _ = c.Conn.SetDeadline(now.Add(-time.Second))   // 过了绝对截止，断开
case since >= c.idle:
    _ = c.Conn.SetDeadline(now.Add(-time.Second))   // 静默超时，断开
default:
    _ = c.Conn.SetDeadline(now.Add(c.idle))         // 最近有活动：续满
}
```

**第三条没有用 `hard` 夹。** 生产 `idle=60s` / `hard=45s`，tick = `idle/3` = 20s：

- T+20s 那次 tick：`now`(20s) **还没超过** `hard`(45s) → 走 default →
  `SetDeadline(T+80s)`。**一个比宣称绝对上界还晚 35 秒的时间点。**
- 此后 socket 上的 deadline 已经越界，能不能被拉回来完全取决于 T+40 / T+60
  两次 tick 是否**准时**跑（GC 停顿、调度饥饿、进程繁忙都会推迟）。

也就是说：`imapHardTimeout` 注释里写的「绝对寿命上限」**在代码上从来没有成立过**，
真正生效的是「`hard` + `idle/3`」，甚至更久。

### 修法

新增 `nextIdle()`，任何续期的 deadline 都夹在 `hard` 之内，`start()` 的初始
deadline 也走它：

```go
func (c *deadlineConn) nextIdle() time.Time {
    d := time.Now().Add(c.idle)
    if !c.hard.IsZero() && d.After(c.hard) {
        return c.hard
    }
    return d
}
```

socket 上的绝对上界从此**就是 `hard` 本身**，与 tick 是否准时无关。

### 实测两个数据点

黑洞服务器（accept 后不响应、也不关连接），读一个必然要靠 deadline 才返回的
`CAPABILITY`。`idle=6s` / `hard=3s` / tick=2s：

| | 初始 socket deadline | 实测返回时刻 |
|---|---|---|
| 不夹取 | `T+6`（**越过 hard=3s**） | **6.000s** |
| 夹取 | `min(T+6, T+3) = T+3` | **4.001s** |

线性对应到生产参数：不夹取时 T+20 那次续期把 deadline 推到 **T+80** ——
与线上观测到的 **80.001s 完全吻合**。

### 诚实标注残余缺口

夹取后仍在 **4.001s** 而不是精确的 3.0s。说明除 socket deadline 外，还有另一条
「tick 到点后才把 deadline 钉到过去」的路径在收尾 —— 即上界是
**`hard` + 一个 tick 量级**（生产约 `45 + 20 = 65s`），不是精确的 `hard`。

这一点我**没有去猜原因**（可能是 go-imap 读 goroutine 的时序，也可能是 tick
被推迟），但必须写下来：否则下一个人读代码会以为 `imapHardTimeout` 是精确值，
再按它去推算预算就会算错。

顺带一个可以确定的结论：不夹取时实测 6.000s **恰好是初始 socket deadline 的
到点时刻**，说明 socket deadline 本身是生效的 —— 问题只在「被续期推到了上界
之外」，不在「deadline 不管用」。

### 测试

新增 `deadline_clamp_test.go` 两条：

- `TestNextIdleIsClampedByHard` —— 纯函数断言：返回值不得超过 `hard`；`hard`
  为零值时**必须**退回纯 idle 续期（不能变成立刻超时，否则没有硬截止的调用方
  会被误伤）；`idle < hard` 时不该被提前夹到 `hard`（夹取只能收窄）。
- `TestHardDeadlineIsNotExceededByIdleRenewal` —— 时序断言，上界 5s（夹取 4.0s
  绿 / 不夹取 6.0s 红）。

**第一版时序参数选错过**：我把 `hard` 正好设成 tick 的整数倍，两种实现只差
0.5s，断言抓不住回归 —— 等于写了一条永远绿的测试。已重新标定（`hard=3s`、
tick=2s），并在注释里写明为什么必须避开 tick 边界。

原有三条 deadline 用例全部仍绿，其中
`TestIMAPIdleDeadlineAllowsSlowButActiveConnection` 尤其关键：夹取只收紧上限，
**不会误杀「慢但在动」的正常连接**。

### 仍然存在的一半问题（本次没修）

`syncBudget` 的注释说它是「单个账户的**总**墙钟预算，IMAP 与 POP3 降级共用」，
但代码里 **IMAP 路径从头到尾没有检查过它** —— `remaining()` 只被传给 POP3
分支。于是即使硬截止修好了，单账户同步仍可能超过 70s，POP3 回退照样拿不到
预算（线上那条 `budget -10s left` 就是这么来的）。

要真正兜住，得给 IMAP 阶段一个**独立于 deadline 机制**的上界（最可靠的是
到点直接 `client.Close()`，它是立即的，而 deadline 实测要等 tick）。这是下一步，
本次只把 deadline 机制本身修对，不顺手扩大改动面。

---

## §7bj 给 IMAP 阶段一个无条件上界：POP3 回退不再拿不到预算（`bb21c6d`）

### 起因：§7bi 只修了一半

`imapHardTimeout` 的滚动续期被夹住之后，还剩一半问题：
`syncBudget` 的注释说它是「单个账户的**总**墙钟预算，IMAP 与 POP3 降级共用」，
但代码上 **IMAP 路径从头到尾没有检查过它** —— `remaining()` 只被传给 POP3 分支。

于是：IMAP 一旦跑超 → `remaining()` 变负 → `syncPOP3Fallback` 第一行就是

```go
if budget <= 0 {
    return 0, fmt.Errorf("imap failed and no time left for POP3 fallback (%s)", ...)
}
```

降级路径**恰好在最需要它的时候没有预算**。线上原文（§7bh）：

```
21:38:02 imap login huangxutao@kxpms.cn failed: ... i/o timeout
        — trying POP3 fallback (budget -10s left)
```

### 修法：到点直接 Close

不是「给 IMAP 加检查」—— go-imap 不响应 ctx（`imapclient.Options` 没有
ReadTimeout 也没有 ctx），唯一的**立即**手段是 `Close`。`Close` 是同步的、
不依赖任何定时器，所以它和 §7bi 里那个「要等看门狗 tick」的 deadline 机制是
两种不同的兜底，缺一不可。

```go
const syncBudget = 70 * time.Second   // 生产
pop3Reserve     = 20 * time.Second   // 无条件留给 POP3
imapStageBudget = syncBudget - pop3Reserve   // 50s

stopIMAPStage := time.AfterFunc(imapStageBudget, func() {
    log.Printf("imap stage budget %s exhausted for %s — closing connection to leave %s for POP3 fallback", ...)
    _ = client.Close()
})
defer stopIMAPStage.Stop()
```

取 50s 而不是 `imapHardTimeout`(45s)：让 deadline 机制先按它自己的节奏收尾，
`Close` 只是兜底。正常同步实测 0.25~1.4s，远够不着这两个值。

**一个配置坑顺手堵掉**：若 `syncBudget <= pop3Reserve`，`imapStageBudget` 会算成
非正数，而 `time.AfterFunc` 拿到非正周期会**立刻触发** —— 每一轮都直接走降级，
比超预算更难排查。所以 `imapStageBudget <= 0` 时退回 `syncBudget / 2`。

### 为什么把两个 const 改成可注入字段

`syncBudget` / `pop3Reserve` 从 const 变成 `Fetcher` 的字段（零值仍取生产默认），
理由与 `imapDialWithIdle` 完全一样：**50s 的生产值测试等不了**，而「Sync 里到底
装没装那个 AfterFunc」这条契约只能在 Sync 的真实路径上验。

### 测试：第一版测的是测试自己

我第一版写的是「直接测 `time.AfterFunc` + `client.Close` 能断开挂住的读」——
那条**把 Sync 里那行删掉照样绿**，因为它根本没经过 Sync。这正是本项目反复
踩的「测测试自己」（见 `selectInboxWithClientID` 那次），所以重写了。

`sync_budget_test.go` 走完整 Sync：真 store（隔离 schema）+ 真 account +
`dialTLS` 指向黑洞 IMAP 服务器，预算缩到 `syncBudget=4s / pop3Reserve=2s`。

关键设计：把 `dialTLS` 的 idle 设成 60s、硬截止设 0，**保证在 2.5s 的测试窗口
里 deadline 机制完全不可能成为终结者** —— 唯一的终结者只能是 Sync 自己装的
那个 AfterFunc。IMAP 登录挂住后走 POP3 分支，而 POP3 端点解析不出来，所以返回
的错误**必然**是 `no POP3 endpoint`，而不是 `no time left for POP3 fallback`。

### 修复后

```
22:07:53 imap stage budget 2s exhausted for budget@example.com — closing connection
         to leave 2s for POP3 fallback
22:07:53 imap login budget@example.com failed: unexpected EOF — trying POP3 fallback (budget 2s left)
22:07:53 budget@example.com sync trace total 2.018s
--- PASS: TestSyncLeavesBudgetForPOP3Fallback (2.28s)
     IMAP stage closed at 2.019s, POP3 fallback entered with a positive budget:
     no POP3 endpoint for budget@example.com (imaphost=127.0.0.1)
```

`budget **2s left**` 是**正数**，而且真的走进了 POP3 分支（而不是停在「没预算」）。

### 负控：忠实复现了生产缺陷

把 AfterFunc 周期改成 1h（等于不设上界）：

```
22:09:07 imap login budget@example.com failed: ... i/o timeout
         — trying POP3 fallback (budget -56s left)
--- FAIL: TestSyncLeavesBudgetForPOP3Fallback (60.30s)
     sync_budget_test.go:112: POP3 回退仍然没拿到预算（elapsed=1m0.021s）：
     imap failed and no time left for POP3 fallback (budget@example.com)
```

`budget **-56s** left` 与线上那条 `budget -10s left` **同构** —— 缺陷被完整
复现（负控期间 `idle=60s` 的 deadline 兜底成了唯一终结者，耗时 60.3s，也正是
「deadline 机制不作为时 Sync 会跑多久」的实测值），而修复消除了它。

### 两条负控合起来说明的事

| 机制 | 负控（撤掉后） | 修复后 |
|---|---|---|
| §7bi 硬截止夹取 | 读在 6.000s 断（越过 hard=3s） | 4.001s |
| §7bj IMAP 阶段上界 | `budget -56s left`，POP3 直接放弃 | `budget 2s left`，走进 POP3 |

两者是**互补**的兜底：deadline 机制精细但依赖 tick 准时，`Close` 粗糙但立即。
只留后者，IMAP 阶段会硬切在 50s（正常同步无所谓，但长列表分页会被切）；只留
前者，就有了 §7bi 那个「上界可能越界」的问题。

---

## §7bk 用 race detector 验本轮新增的并发代码（2026-10-01）

### 为什么突然做这件事

§7bi/§7bj 往 fetcher 里加了三处**新的并发**：

1. `time.AfterFunc(imapStageBudget, client.Close)` —— 一个独立 goroutine，
   在主 goroutine 正卡在 `Login().Wait()` 的读上时去 `Close()` 客户端。
2. `deadlineConn` 的看门狗 goroutine 与 `touch()` 争 `c.mu`。
3. `inflight` 这个 `sync.Map`（原有的，但同属并发面）。

这三处正是 race detector 该管的区域。而在此之前，**本仓库的邮件测试从来没在
-race 下跑过**。

### 先确认工具链真的能跑 —— 别把「没检测到竞争」当成「没有竞争」

本机 `C:\tools\w64devkit\w64devkit\bin\gcc.exe`（GCC 16.2.0）存在，
`CGO_ENABLED=1` 时 `go test -race` 可用。

但**「没报 DATA RACE」有一个致命假阴性：检测器可能根本没启用**。所以先用一个
故意写错的临时包验工具链：

```
D:\temp\racetest\race_test.go   // 两个 goroutine 并发 x++
go test -race ./... -count=1
→ WARNING: DATA RACE
  Read at 0x... by goroutine 9:  racetest.TestDeliberateRace.func1() race_test.go:13
  Previous write at 0x... by goroutine 8: ...
```

检测器确实在工作。（顺带再踩一次 PowerShell 5.1 的坑：`Set-Content
-Encoding UTF8` 写出的 `go.mod` 带 BOM，go 直接报
`go.mod:1: unexpected input character '\ufeff'`。用
`[IO.File]::WriteAllText(path, text, New-Object UTF8Encoding($false))`。）

### 结果：全绿，0 DATA RACE

```
# 无 build tag（全部单测 + PG 集成）
$env:PATH='C:\tools\w64devkit\w64devkit\bin;'+$env:PATH
$env:CC='C:\tools\w64devkit\w64devkit\bin\gcc.exe'; $env:CGO_ENABLED='1'
go test -race ./internal/email/ -count=1 -timeout 1800s
→ ok  github.com/halfking/pocket-opencode/backend/internal/email  37.823s   exit=0

# 真实 IMAP 路径（Greenmail）
go test -race -tags=greenmail ./internal/email/ -run 'TestSyncGreenmail|TestMoveToJunkGreenmail' -count=1
→ --- PASS: TestSyncGreenmail (0.44s)
  --- PASS: TestMoveToJunkGreenmail (0.15s)
  ok  ... 1.767s   exit=0
```

`TestSyncLeavesBudgetForPOP3Fallback`（§7bj 那条）**确实在 -race 下跑到了
AfterFunc 触发的时刻** —— 它本身就是让 `client.Close()` 与挂住的读并发的用例，
所以那段竞争窗口是被真实覆盖的，不是「跑过了但没碰到」。

### 顺带：这次没有再污染生产库

按 §7bh 的教训验了共享库（greenmail 用例是要往生产 schema 插账户的）：

```
accounts_total     = 5      <- 只有 5 个真实邮箱
greenmail_left     = 0
emails_real        = 120
emails_greenmail   = 0
```

`t.Cleanup` 生效。§7bh 那次是我自己漏了收尾，这次修完就干净了。

### 这条改变了什么

不是「又通过了一次测试」，而是把**一类此前无法验证的风险**纳入了可验证范围：
本仓库此后的邮件并发改动（尤其是「从另一个 goroutine 强行打断一个阻塞中的
网络读」这种模式）可以在本机直接用 race detector 验，不必只靠代码审阅。

---

## §7bl 全后端 -race 回归：0 竞争，但挖出一个「定时炸弹」（`d6b4ebe`）

### 起因：把刚拿到的能力用起来

§7bk 确认本机可以跑 `-race` 之后，顺手做一次**全后端**回归，看邮件改动有没有
外溢。

```
$env:PATH='C:\tools\w64devkit\w64devkit\bin;'+$env:PATH
$env:CC='C:\tools\w64devkit\w64devkit\bin\gcc.exe'; $env:CGO_ENABLED='1'
go test -race ./... -count=1 -timeout 3600s
```

**DATA RACE 计数 = 0。** `internal/email` `ok 56.087s`，
`internal/email/rules` `ok`。

5 个包 FAIL：`chatagent`、`learning`、`quota`、`scheduledtask/executors`、`server`。

### 先做边界判定，别急着归因

我记过的纪律：**绿→红边界做 `git diff --stat`，相关目录零差异则二分方向错了**。

```
git diff --stat <merge-base> HEAD -- backend/internal/{chatagent,learning,quota,scheduledtask}
→ backend/internal/scheduledtask/executors/workitem_reminder_test.go | 39 ++++++++-----
```

只有 `executors` 被我的分支碰过，而且提交是
`0f3b4f4 email: A4 裁切线 + 多币种分组合计 + reminder 测试时钟夹具` ——
同一个提交里既有邮件改动也有 reminder 测试夹具改动。所以不能推给「无关」。

失败清单：

```
chatagent/store_test.go:130    ws-a should see 2 agents (custom-a + builtin), got 278
learning/store_pg_test.go:200  narrow window got 2 timestamps, want 1
learning/store_pg_test.go:547  snoozed until 1790871471, want later than ... 86400 offset
learning/store_pg_test.go:555  an acked reminder must never come due, got 1
learning/store_pg_test.go:562  pending = 1, want 0 after acking the only pending one
quota/pg_store_test.go:99      zero-period budget must always apply, got 2
executors/workitem_reminder_quiet_test.go:94  a user with no stored preferences must still get their reminder, got 0 events
server/task_write_guard_route_test.go:108,130  bob PATCH/DELETE ... = 404, want 403
```

`chatagent` 那条 `got 278` 很说明问题：它**期望隔离 schema，却读到了生产
schema 的 278 个 agent** —— 这正是我记忆里那条「PG 测试助手沿用 DSN 的
search_path」的形态。`server` 两条是 §7az 里早已记录的既有 404/403 分歧。

**真正值得挖的是 `executors` 那条**，它和记忆里的一类缺陷完全吻合。

### 确诊：钉死的绝对日期 + 24 小时陈旧上界 = 永久红

生产代码（`workitem_reminder.go`）：

```
:99   const DefaultStaleAfter = 24 * time.Hour
:172  now := time.Now().Unix()                 // 用的是墙上时钟
:195  // Staleness is checked before quiet hours: a three-day-old reminder
:197  if e.staleAfter > 0 && now-item.RemindAt > int64(e.staleAfter/time.Second) {
```

夹具（`workitem_reminder_quiet_test.go`）钉死 `time.Date(2026, time.September, 30, …)`。
写完当天绿，**跨过 24h 之后永久红**。

2026-10-01 22:12 实测：

```
--- FAIL: TestWorkItemReminderFallsBackWithoutPreferences
    a user with no stored preferences must still get their reminder, got 0 events
```

因为陈旧判定**排在免打扰之前**，夹具被 `ClearTaskRemindAt(…, 0)` 直接退役，
**根本没走到免打扰逻辑**。这个症状（「提醒没触发」）与「免打扰判定错了」
几乎一样，极易把排查方向带偏 —— 我第一反应也差点去找免打扰的 bug。

**为什么姊妹文件没红**：`workitem_reminder_test.go` 的夹具早就是相对时间
（`recent(...)`），而且验陈旧本事的用例在 `:468` 明确 `ex.SetStaleAfter(0)`。
只有 `quiet` 这个文件留着 4 个硬编码日期。

### 修法：两件事，缺一不可

1. `wallClockAt(t, loc, hour, minute)` —— 返回「loc 时区今天 hh:mm」，
   若那一刻**还没到**就退回昨天。退回昨天而不是往后推，是因为
   `DueTaskReminders` 只取 `remind_at <= now`，往后的夹件根本不会到期。
2. `freshExecutor(...)` —— 统一 `ex.SetStaleAfter(0)`。**陈旧不是这 4 条用例的
   主题**，它们测的是「免打扰窗口属于 owner」以及四条分支各自的判定。
   与 `workitem_reminder_test.go:468` 的既有约定对齐。

### 负控：精确复现原报错，且失败严格跟着 24h 上界走

把 12:00 那个夹具换回 `time.Date(2026, time.September, 30, 12, …)` 并去掉
`SetStaleAfter(0)`：

```
--- FAIL: TestWorkItemReminderFallsBackWithoutPreferences
    a user with no stored preferences must still get their reminder, got 0 events
```

同一条用例、同一句报错。

**只有一条红**，而这是对诊断最有力的印证：另外三个夹具当时**还没跨过 24h**
（23:50 上海 = 15:50 UTC，差 22.4h；23:50 UTC，差 14.4h），所以照常绿。
失败严格跟着陈旧上界走 —— 如果根因是别的，不可能这么整齐。

### 覆盖面没有被削弱

24 例全绿，其中三条专门测陈旧逻辑的
（`RetiresStaleReminders` / `StaleBeatsQuietHours` / `StaleBoundIsConfigurable`）
仍然全绿：只摘掉了与这些用例无关的耦合，陈旧行为本身仍由它们守着。

### 一条方法论

这是同一类缺陷的**第二次**命中（第一次是邮件包的排查，结论是「邮件包不存在
该风险」——那是对的，因为邮件包用的是 `time.Now()` 派生夹具）。
判据是同一条：**夹具的绝对日期 + 生产代码的时间上界 = 定时炸弹**。
所以每次新写夹具都该问一句「这个日期会过期吗」，而不是跑绿就收工。

---
## §7bm chatagent 测试助手写的是**应用正在用的表**（`99637e5`）

### 事实链（每条都有 file:line 或实测）

1. `internal/chatagent/store_test.go` 的 `setupTestStore` 原来是
   `pgxpool.New(ctx, dbURL)` —— **原样继承 DSN 的 `search_path`**，没有覆盖。
2. PG 的规则：当前 schema 找不到 unqualified 名字就**回落到 `public`**。
   本机 `search_path=opencode_pocket` 里**没有** `chat_agents` 表 → 落到
   `public.chat_agents`。
3. `cmd/pocketd/main.go:1473` `initChatAgentStores`：`pool != nil` 用 PG store，
   否则才用 `<dataDir>/chat_agents.sqlite`。线上日志
   `Postgres pool initialized (schema="opencode_pocket")` → 走 PG 分支。

   也就是说 **`public.chat_agents` 就是应用自己正在用的那张表**；
   `data/chat_agents.sqlite` 最后修改停在 2026-09-30 03:19，是更早 PG 不可用时
   的 fallback 遗留。
4. 于是这个 helper 每次跑都对**应用正在用的表**做两件事：

   ```go
   store.Init(ctx)   // CREATE TABLE IF NOT EXISTS —— 改的是生产表结构
   // 以及
   DELETE FROM chat_agents WHERE id LIKE 'test-%' OR id LIKE 'custom%'
      OR id IN ('builtin','builtin-agent','c1','c2')          // 删的是生产行
   ```

### 实测症状

`TestStore_List_WorkspaceIsolation` 报
`ws-a should see 2 agents (custom-a + builtin), got 278` —— 看见 278 行自己从没
建过的数据。`public.chat_agents` 现有 3 行：`builtin`（workspace 空）、`c1`、
`c2`（ws-1），`created_at` 正是测试运行的时刻。

### 修法

自己生成 `chatagent_test_<hex>` schema，`search_path` **只**指向它
（**不追加 public**）。这样既隔离写入，又让「引用一张不存在的表」变成**报错**
而不是静默落到 public —— **静默回落正是这个缺陷的根因**。收尾只 DROP 自己那个
schema。同一模式见 `internal/email/store_workspace_test.go:61`。

顺带把那条 DELETE 收窄成 `DELETE FROM chat_agents`：隔离 schema 是本轮自己建的，
没有历史数据，id 白名单只是历史包袱 —— 而白名单里的 `builtin`/`c1`/`c2`
**正是生产表里真实存在的 id**，留着它等于留着一把指向生产行的枪。

### 验证

```
go test ./internal/chatagent/ -count=1   -> ok 1.877s   （此前 FAIL: got 278）
select count(*) from public.chat_agents  -> 3           （本次运行后**未变**）
pg_namespace where nspname like 'chatagent_test_%' -> （空，cleanup 正常）
```

「public 计数未变」是这次修复最直接的证据：跑完整个包的测试，那张表一个字节
都没被碰。

### 刻意没有做的事

**没有**为了取证把旧写法再注入回去跑负控 —— 那条负控**本身就是那条 DELETE**，
会真的去删 `public.chat_agents` 里剩下的 3 行（其中 `builtin` 是生产 id）。
取证已经足够（修复前的 `got 278` 记录 + 修复后 public 未变），不值得为一个
已经能自证的结论去真的破坏数据。

> 这是一次**有意识地放弃负控**，并把理由写在这里 —— 不是忘了做。

### 仍未查清（不猜）

`public.chat_agents` 现在只有 3 行，而 `internal/chatagent/seed/agents.json`
里有 **277** 个内置角色。已确认：

- `ImportBuiltinAgents` 在启动路径上**没有任何调用者**（`cmd/pocketd`、
  `internal/server` 都没调），所以服务启动不会导入内置角色。
- 它本身幂等：表里已有 builtin 就跳过（`importer.go:115-121`）。
- `importer_test.go:166` 走的是 **NewSQLiteStore**（临时文件），不是 PG store。
- 全仓**没有**任何 `DROP TABLE` / `TRUNCATE chat_agents`。

所以那 277 行既不是服务启动写的，也不是那些测试写的，**来源与消失原因我都没
查清**。`ImportBuiltinAgents(ctx, repoPath)` 要的是一个 markdown 仓库路径
（不是那个 JSON），所以「重新导入」也不是一条现成的恢复路径。

这属于「应用数据现状」，需要你判断 `public.chat_agents` 对你是否有价值、
是否要恢复内置角色。**未擅自处理。**

---
## §7bn `learning` / `quota` 的失败定性：3 类根因，其中只有 1 个是真缺陷（2026-10-02）

### 起因与第一个坑：「全绿」本身可能是假的

待办里挂着一条「`learning` / `quota` 的 5 个失败定性（时间相关还是共享数据）」。
我第一件事是重跑：

```
$env:TEST_DATABASE_URL='postgres://...?search_path=opencode_pocket'
go test ./internal/learning/... ./internal/quota/... -count=1
→ ok internal/learning 0.249s / ok internal/quota 1.124s      # 「全绿」
```

**这个「全绿」是假的。** 这两个包的 `pgDSN()`（`learning/store_pg_test.go`、
`quota/pg_store_test.go:20`）只认 `POCKET_TEST_POSTGRES_DSN` 和
`POCKET_POSTGRES_DSN`，我给的是 `TEST_DATABASE_URL` → 走 `t.Skip` →
`go test` 照样打印 `ok`。

换成正确的变量名，失败立刻回来了，而且**比 §7bl 记的还多一条**：

```
$env:POCKET_TEST_POSTGRES_DSN='postgres://...?search_path=opencode_pocket'
--- FAIL: TestActiveDayTimestamps            store_pg_test.go:200
--- FAIL: TestReminderLifecycle              store_pg_test.go:547,555,562
--- FAIL: TestPGStore_BudgetsFor_FiltersByPeriod    pg_store_test.go:70
--- FAIL: TestPGStore_BudgetsFor_AcceptsZeroPeriod   pg_store_test.go:91
--- FAIL: TestPGStore_RejectsEmptyWorkspace          pg_store_test.go:104
```

> 记一条方法论：**`ok` 不等于跑过了。** DB 门控的包在 DSN 变量名写错时会**静默
> 跳过**并报 `ok`。任何「全后端全绿」的结论，都必须先确认目标包真的执行了
> （看 `ok <pkg> 0.0s` 后面有没有 `(cached)`/`[no test files]`，或临时把
> `t.Skip` 改成 `t.Fatal` 验一次）。

---

### 根因 1（`quota`，3 条）：测试助手把 `search_path` 拼成了两个参数

`quota/pg_store_test.go:49`：

```go
scopedPool, err := pgxpool.New(ctx, dsn+"&search_path="+schema)
```

字符串**追加**。本机 DSN 自带 `search_path=opencode_pocket`，拼出来就是
`...&search_path=opencode_pocket&search_path=quota_test_<hex>`。`&` 在 DSN 里是
参数分隔符，没有转义，于是 `search_path` 的值被解析成
`opencode_pocket&search_path=quota_test_<hex>` —— 一个**不存在的 schema 名**，
`CREATE TABLE` 无处可建：

```
NewPGStore: quota migrate: ERROR: no schema has been selected to create in (SQLSTATE 3F000)
```

**决定性对照**（只改 DSN，其它一切不变）：

```
$env:POCKET_TEST_POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
go test ./internal/quota/... -count=1        → ok  0.988s
```

不带 `search_path` 的干净 DSN 下拼接是正确的（`dsn` 末尾是 `?sslmode=disable`，
拼成 `?sslmode=disable&search_path=quota_test_x`），全绿。

**定性：纯测试助手缺陷，生产代码零影响。** CI 用的正是不带 `search_path` 的干净
DSN，所以「CI 绿、本地红」——**不是**「本地环境有问题」，是本地 DSN 多带了一个参数。
跑完 `pg_namespace` 无 `quota_test_*` 残留。

正确写法照抄 learning 即可：不要拼字符串，用
`pgxpool.ParseConfig(dsn)` 再改 `cfg.ConnConfig.RuntimeParams["search_path"]`。

---

### 根因 1b（`finance`，3 条）：**同一个错误，但形态更危险** —— 而且揭示了根因 1 之外的东西

`internal/finance` 之前从没在失败清单里出现过，因为它之前**一直被静默跳过**。
改对 DSN 变量名之后它才浮出来：

```
--- FAIL: TestPGStore_CreateScoped_Concurrent   pg_store_concurrent_test.go:46
--- FAIL: TestPGStore_ConflictRecovery          pg_store_conflict_test.go:30
--- FAIL: TestPGStore_ConcurrentConflictRetry   pg_store_conflict_test.go:93
    NewPGStore: finance migration failed: ERROR: no schema has been selected to create in (SQLSTATE 3F000)
```

**同一句 `3F000`，但 finance 的 harness 比 quota 危险得多**：

```go
// finance/pg_store_conflict_test.go:22
pool, err := pgxpool.New(ctx, dsn)      // 原始 DSN，一字不改
s, err := NewPGStore(ctx, pool)          // migrate: CREATE TABLE finance_transactions
pool.Exec(ctx, `DELETE FROM finance_transactions WHERE note_ref LIKE 'test-conflict-%'`)
```

它**根本不建隔离 schema**，直接吃调用方的 DSN，然后对
**`search_path` 解析到的表**建表 + 跑 `DELETE`。只把 DSN 换成不带
`search_path` 的版本就全绿（`ok 0.358s`）—— 也就是说它**成功地在 `public` 里
建了 `finance_transactions` 并写进了测试数据**。

我已经把自己留下的那 1 行删掉了（`note_ref='note:conc_1'`，`owner_id='user-conc'`，
`created_at 2026-10-01 22:47:06`），`finance_transactions` 回到 0 行。
**表本身我没有动** —— 它可能是生产表，删表是不可逆操作，留给你定。

`quota` 和 `finance` 的差别值得记下来：

| | 是否自建 schema | 失败时的落点 |
|---|---|---|
| `quota` | 建了 `quota_test_<hex>` | 拼串出错 → 3F000，**没写任何东西** |
| `finance` | **没有** | 用 DSN 的 search_path → **写进了 `public`** |

---

### 根因 1c：为什么本机 DSN 一带 `search_path` 就全盘 3F000

上面两条能成立，是因为**本机 DSN 里的那个 schema 压根不存在**：

```
select count(*) from pg_namespace where nspname='opencode_pocket'   →  0
```

而 `opencode_pocket` 是 `config.go:236` 里 `POCKET_PG_SCHEMA` 的**默认值**
（`getEnv("POCKET_PG_SCHEMA", "opencode_pocket")`）。于是：

- 任何**只读**的查询：PG 静默回落到 `public`，看起来一切正常；
- 任何**建表**：无处可建，3F000；
- 任何 `finance` 那类**没隔离的 harness**：安静地改建到 `public`。

这正是我在 §7bm 里写的那句话在更大范围内的重演：
**「静默回落让缺表变成看不见，而不是报错」。** 只是这次它把一整个 schema 藏了
整整两天。详见 §7bo。

---

### 根因 2（`learning`，1 条）：**真缺陷** —— `ActiveDayTimestamps` 漏过滤 `captured_at`

`learning/store.go:440-461`：

```go
rows, err := s.pool.Query(ctx, `
    SELECT captured_at, updated_at
    FROM learning_items
    WHERE workspace_id = $1 AND user_id = $2 AND deleted_at = 0
      AND (captured_at >= $3 OR updated_at >= $3)`,   // ← 行级过滤
...`)
for rows.Next() {
    if capturedAt > 0 { out = append(out, capturedAt) }   // ← 时间戳级不过滤
    if updatedAt  > 0 { out = append(out, updatedAt)  }
}
```

`since` 只在 SQL 里用来**筛行**。一行只要 `captured_at` 或 `updated_at` **任一**
过线就整行返回，然后 Go 侧把**两个时间戳都无条件 append**。

实测证据（就是失败输出本身）：

```
now     = 1790865684
since   = now-1800 = 1790863884
返回      [1790862084  1790865684]
                 ^^^^^^^^ 比 since 还早 1800 秒，照样被返回
```

`store.go:431-433` 的文档写明「returns every timestamp at which the user did
something **since sinceUnix**」——返回了窗口外的时间戳，是**契约违反**。

**影响面要说实话，不夸大**：生产调用的 `since` 是 730 天前
（`service.go:271,287` `streakWindowDays = 730`），所以泄漏的只能是**窗口外**的
时间戳。而 `ComputeStreak`（`streak.go:55-96`）只用这批 day 算两件事：
`Longest`（历史最长）和**锚定在 today / today-1 的 `Current`**。一个 730 天前的
day 不可能与 today 相邻，所以：

- **不会**虚增 `Current`（当前连续天数）；
- **可能**让 `Longest` 虚增一天 —— 当且仅当泄漏出来的那个窗口外 day 恰好紧贴
  窗口左边界、且那一段本来是连续的。

罕见、影响小，但确实是真缺陷，不是测试写错。

---

### 根因 3（`learning`，3 条）：**测试期望写错**，不是缺陷

三条同源，全在 `TestReminderLifecycle`：

**(a) `store_pg_test.go:546`** 期望 snooze 是「从原有排期再往后推」：

```go
s.MarkReminderSent(ctx, ..., due.ID, now+86400)   // 排到 now+86400
next, _ := s.SnoozeReminder(ctx, ..., due.ID, 120)
if next <= now+86400 { t.Errorf("want later than the previous 86400 offset") }
```

而实现（`store.go:399-400`）是**从当前时刻起算**：

```go
now := time.Now().Unix()
until := now + minutes*60
```

实测 `next - now = 7200` 秒 = 120 分钟，与实现**完全吻合**。测试的期望比实现多
要求了 86400 秒。

**(b)(c) `:555` / `:562`** 是 (a) 的连带，不是独立问题。测试 ack 的是
`future.ID`（另一条），却断言「due 队列为空、pending=0」。但 `due` 这时是
`snoozed` 状态，而两个查询都**明确把 snoozed 计入**：

```go
// DueReminders        store.go:356
AND state IN ('pending', 'snoozed', 'sent')
// CountPendingReminders store.go:514
WHERE ... AND state IN ('pending', 'snoozed')
```

所以 `due` 被 snooze 到 `now+7200`，在 `DueReminders(now+100000)` 的时间点
**本来就该被返回**，`pending=1` 也是对的。测试的前提（「acking the only
pending one」）本身不成立——`due` 一直健在。

**语义上到底是哪种对，不该由我定。** 我的判断是「实现更合理」：用户点
「2 小时后再提醒」，期望的就是 `now+2h`，而不是「24 小时后那次再往后推 2 小时」。
但这是**产品语义**，而且全仓**只有这一个测试**钉住 snooze 的基准点，没有第二处
证据可以交叉验证。**不擅自改，留给你定。**

---

### 顺带记下一个潜伏隐患（learning 助手）

`learning/store_pg_test.go` 的 `newTestStore` 用的是

```go
cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
```

**追加了 `public`。** 这正是 §7bm 里「静默回落」的形态：一旦它自己的表没建出来，
查询会**静悄悄**落到生产的 `public.learning_items` 上，而不是报错。现在没出事
只是因为 `EnsureSchema` 先跑了、而且 §7bm 之后 `public` 里也确实没有 email 之外的
影子表被测试碰到过 —— 但这是**运气，不是隔离**。修 quota 的时候顺手把它也改成
「只指向自己的 schema」更稳妥。**未擅自改。**

---

### 处置

三个根因**全部不在邮件分支**（`internal/learning`、`internal/quota` 本轮 diff
零改动），与 §7az 里 `internal/server` 那两条 404/403 是同一类问题：测试或产品
语义要一起定。按既有纪律**只定性、不改代码**，已列入待决清单。

---

## §7bo 停下来报告：`opencode_pocket` schema 不存在，本机库与文档记录对不上（2026-10-02）

**这一节不是结论，是一份「我查到了什么 / 我不知道什么」的移交。定性工作没有做完，
因为它撞上了一个比我原本任务大得多的问题。**

### 触发

§7bn 为了给 `quota` 做「换干净 DSN」的对照实验，我顺手看了一眼本机 PG 到底有哪些
schema —— 因为 `3F000` 只在 schema 不存在时才合理。结果：

```
select nspname from pg_namespace where nspname not like 'pg_%'
  and nspname <> 'information_schema';
→ meeting_test_9f53af8f624c
→ meeting_test_d3703153a1e1
→ public
```

**没有 `opencode_pocket`。** 只有 `public` 和两个残留的 `meeting_test_*` schema。

### 与文档记录的直接冲突

| 文档记录（§7bh，2026-10-01 21:46 实测） | 2026-10-02 现在的实测 |
|---|---|
| `email_accounts`: 5 个真实邮箱 | `public.email_accounts` = **0 行** |
| `emails` 非测试账户 = 120 | `public.emails` = **0 行**，且 `n_tup_ins = 0` |
| schema = `opencode_pocket` | 该 schema **不存在** |

`public` 一共只有 **11 张 base table**，全库只有 `chat_agents` 有数据（3 行）。
`public.emails` 的 `pg_stat_user_tables.n_tup_ins = 0`、`n_tup_del = 0`、
`last_vacuum` 为空 —— **这张表自统计重置以来从未进过一行**，所以那 138 行
（120 真实 + 18 greenmail）**从来就不在 `public`**。

同时，运行中的 pocketd（PID 33948，端口 18099）**确实连的就是这个本机实例** ——
它自己的查询语句出现在 `logs\pg\pg.err2.log` 里（`SELECT ... FROM tasks`、
`scheduled_tasks` 的 `relation does not exist`），而它的启动日志写着：

```
20:18:42 Postgres pool initialized (schema="opencode_pocket")
```

也就是说 20:18 启动时这个 schema 还是好的（能读出 5 个真实账户、能同步），
现在它不在了。

### 文件侧：**没有**发现对应损失

`C:\workspace\openpocket\data\email-invoices\ws_user-admin\` 现在 5 个文件，
其中 4 个正是待决清单里那 4 个「孤儿 PDF」。我此前记录的「132 个文件」经复核
**很可能是我把整棵 `email-invoices` 树（含 `exports\`）一起数了**，而不是
`ws_user-admin` 单目录 —— `exports\ws_user-admin\` 里确实有大量 A4 导出与汇总文件
（最新的 `invoices-summary-20261001-204124.md` 时间戳 20:41，与记录吻合）。
**所以文件侧我不宣称丢失。**

回收站也查过了（`mavis-trash` 走 PowerShell 回收站，390 条 `$I*` 元数据），
**没有任何一条路径指向 `email-invoices` / `email-bodies` / openpocket\data** ——
不是被 mavis-trash 移走的。

### 我已经排除的

- **不是本仓库的测试删的**：全仓 `grep 'DROP SCHEMA|DROP DATABASE'`（含非测试代码）
  **零命中**；所有 `DROP SCHEMA <name> CASCADE` 的测试用的都是自己随机生成的
  `<pkg>_test_<hex>` 名，没有任何一处从 DSN 的 `search_path` 取名字去 DROP。
- **不是 `finance` 那条**：`finance_transactions` 是 `public` 里一张独立表，
  与 `opencode_pocket` 无关；而且我把 DSN 换成干净的之后它才建的表，
  建表时间是 22:47，在 schema 消失之后。

### 我不知道的（**不猜**）

1. `opencode_pocket` 是在 21:47 ~ 22:47 之间被谁、以什么方式删掉的。
   我这一小时里跑的命令我都列得出来（两次 `go test ./...`、两次 `go test -race ./...`），
   但**没有任何一条能解释它**，所以我不敢把锅扣在测试上。
2. 也存在另一种可能：**我此前那些「实测」本来就打到了别的库**，
   文档里的 5 账户 / 120 邮件 / 138 行从一开始就记错了。这两种解释我目前**分不开**。
3. 部署脚本 `deploy-252.sh:73` 写着「openpocket 权威 PG 在 **252 本机 docker** 中」，
   而本机这个 PG 又是运行实例在连的 —— 到底哪个才是权威库，我没法从机器上判断。

### 为什么停在这里

接下来的动作（恢复、备份、从 WAL 捞、还是确认只是记录错误）**都是不可逆或
高风险的写操作**，而且**依赖一个只有你能回答的前提**：那个 schema 是不是本来就
该在这儿。我不擅自 DROP / 不擅自从 WAL 恢复 / 不擅自改 `POCKET_PG_SCHEMA`。

### 顺带一个已经能确定的产品问题

即使数据问题另有解释，**「`POCKET_PG_SCHEMA` 默认值指向一个不存在的 schema」**
这件事本身就该修：它让所有「缺表」都变成静默回落 `public`
（`internal/db/pg.go:53-54` 的注释明确说**故意不**把 `public` 加进 `search_path`，
就是为了避免这种混用 —— 但只要 schema 不存在，PG 就会回落到 `public`，
那条防线等于不存在）。

建议在 `db.New` 之后加一条启动断言：`search_path` 的第一个 schema 必须真实存在，
否则启动即失败并打印实际解析结果。**未擅自改** —— 它会改变启动行为，需要你点头。

---

## §7bp 把「什么时候没的」钉到 59 秒：22:17:43 – 22:18:42（2026-10-01）

§7bo 报的是现象。这一节把它收窄到一个可以被复查的时间窗 —— 因为**「不知道什么时候」
和「知道是哪 59 秒」是两种完全不同的处置**。

### 决定性证据：应用自己的日志

`pocketd-18099d.err.log` 里，邮件同步的成败是一条干净的线：

```
2026/10/01 22:17:42 [email/fetcher] 56551681@qq.com      sync trace total 526ms
2026/10/01 22:17:43 [email/fetcher] feikemanager1@163.com sync trace total 394ms
2026/10/01 22:17:43 [email/fetcher] huangxutao@kxpms.cn   sync trace total 1.132s   ← 最后一次成功
2026/10/01 22:18:42 [email/scheduler] list accounts for intents: ERROR: relation "email_accounts" does not exist (SQLSTATE 42P01)   ← 第一次失败
```

全日志 368 条 `sync trace total`，最后一条是 22:17:43；86 条
`relation "email_accounts" does not exist`，第一条是 22:18:42。
**中间隔 59 秒。** 之后到 23:01 仍在持续报错（`tasks` / `scheduled_tasks` /
`email_accounts`），也就是**至今没有恢复**。

### 同一时刻，PG 侧也在报

`logs\pg\pg.err2.log` 里 `relation "email_accounts" does not exist` 共 90 条，
首条 **19:47:12**、末条 **23:00:42**。

19:47 那批**不是**同一个事件 —— 那时应用还在正常同步（20:19 / 21:40 都有成功
记录），所以 19:47 的报错来自**另一条 `search_path` 不同的连接**（测试用 DSN）。
真正的事件以应用日志为准：**22:17:43 → 22:18:42**。

### 窗口里发生了什么

`~\.minimax\background-tasks` 按创建时间排，22:15–22:22 之间有 **24 个任务**，
关键三个：

```
22:15:43  accounts_total=5 | greenmail_left=0 | emails_real=120 | emails_greenmail=0
22:16:35  === every _test.go that opens a PG pool, and whether it pins search_path ===
          ** BARE **  sp=False create=False drop=False touched...
22:18:40  exit=1   ← 全量 go test -race ./... 启动（就是 §7bl 那轮）
22:19:02  --- DATA RACE 计数 --- 0 | FAIL internal/chatagent 11.063s
```

- **22:15:43**：库还是好的，5 个账户、120 封真实邮件、greenmail 残留 0。
- **22:16:35**：另一个并发会话正在做「哪些 PG 测试没有钉 search_path」的普查，
  把不隔离的标成 `BARE`。
- **22:18:40**：全量 `-race` 回归启动，**两秒后**（22:18:42）应用第一次报错。

### 我查到了什么，没查到什么

**查到了**：窗口、本机只有一个 PG 数据目录、应用连的确实是它、这一分钟内唯一
的写库动作是那轮全量测试、同时另一个会话正在普查同一类缺陷并且已经写出了
仓库级护栏（`pg_test_isolation_guard_test.go`，在 `openpocket-wt-font` worktree），
护栏的注释把机制写得很清楚：

> 本仓库的惯例是**同一个 DSN 既喂服务也喂测试**，所以
> `POCKET_TEST_POSTGRES_DSN` 的 search_path 完全可能就是生产 schema。

**没查到**：**具体是哪一条语句**执行了删除。全仓 `DROP SCHEMA|DROP DATABASE`
零命中，`internal/scheduledtask/maintenance_test.go`（直接吃
`POCKET_POSTGRES_DSN`）里也没有。所以我**不指认凶手** ——
窗口是事实，语句是推测，两者不能混。

### 一个必须说清楚的自我约束

22:18:40 那轮 `go test -race ./...` 是**我自己**跑的（§7bl）。它与事件时间
吻合到 2 秒。**但「吻合」不等于「是我」**：同一个窗口里另一个会话也在动这个库，
而我这一小时跑过的命令都列得出来。除了那轮全量测试，我在 22:17–22:19 之间
**没有执行过任何写库命令**。我既不撇清也不认领 —— 没有证据支持任何一个结论。

### 处置不变

仍然**没有做任何写操作**：未 `CREATE SCHEMA` 重建、未从 WAL 恢复、未改
`POCKET_PG_SCHEMA`、未重启 pocketd。

理由不是谨慎过度，而是：**重建一个空 schema 会让「表不见了」变成
「表空了」，两者在应用侧症状完全一样，但后者更难查**。要恢复必须先知道
原来的表结构和数据来源，而这两样现在都拿不准。

---

## §7bq 修掉本包自己的同款缺陷：greenmail 用例直连调用方 DSN（2026-10-02）

§7bp 把「什么时候没的」钉住了，但那只回答了**症状**。这一条是我顺着同一个缺陷类
回头审**我自己的包**查出来的 —— 结论是：`internal/email` 里两个 greenmail 用例
**就是 §7bh 那次污染生产库的元凶**，而且它们的形态和另一个会话普查出的 `BARE`
完全一致。

### 缺陷

`fetcher_greenmail_test.go:29` / `junk_greenmail_test.go:131`：

```go
dsn := os.Getenv("PG_DSN")
pool, err := pgxpool.New(ctx, dsn)     // 原样吃调用方 DSN，不建隔离 schema
```

本仓库的惯例是同一个 DSN 既喂服务也喂测试，所以 `PG_DSN` 的 `search_path`
**完全可能就是生产 schema**。写操作本身是收敛的（`DELETE ... WHERE account_id=$1`
/ `WHERE id=$1`），所以没删到别人的行；但「测试账户进了生产表」本身就是缺陷：
线上 pocketd 每 60 秒对那个「用临时 master key 加密的测试账户」报一次
`decrypt credential`，一小时几百行，把真实故障埋进日志 —— 这正是 §7bh 记录的
那起事故。

它**不是**删 schema 的凶手（全仓无 `DROP SCHEMA`，见 §7bp），但它属于同一族：
**测试的写操作落点由调用方的 DSN 决定**。

### 修法

新增 `internal/email/pgscope_test.go`，两个助手：

```go
func newScopedPool(ctx, dsn, schema)   // ParseConfig 后覆盖 RuntimeParams["search_path"]
func dropScopedSchema(ctx, dsn, schema) // 独立连接 + DROP SCHEMA ... CASCADE
```

两个用例各自 `CREATE SCHEMA email_greenmail_test_<hex>`，收尾改成 **DROP 掉自己
的 schema**，逐表 `DELETE` 整段删掉。

`search_path` **只**指向自己的 schema，**不追加 `public`** —— 让「引用一张不存在
的表」变成报错，而不是静默落到 `public`。这比包里 `store_workspace_test.go:61`
那份先例（`schema + ",public"`）更严；两者对各自用例都成立，新写的取严的。

逐表 `DELETE` 为什么可以整段删掉：账户只可能落在本测试自己的 schema 里，逐表删
反而是「**假设表在生产库里**」的做法。第一版需要它是因为当时没有隔离 —— 那次踩
的两个静默失败（复用已 `Close` 的 pool 拿到 `closed pool`；三张表统一写
`WHERE account_id=$1 OR id=$1` 而父表 `email_accounts` 没有 `account_id` 列）
已经写进注释留档，但不再属于这条路径。

### 验证：修后 vs 负控，两边都实测

DSN 故意用**不带 `search_path`** 的（于是解析到 `public`），跑完整的
`TestSyncGreenmail`（14 封同步、4 张发票建档并下载）：

```
修后： public.email_accounts / emails / email_invoices  n_tup_ins = 0 0 0
       残留 email_greenmail_test_* schema = 0
```

负控（把 `newScopedPool` 换回 `pgxpool.New(ctx, dsn)`，其余不动）：

```
ok  	github.com/halfking/pocket-opencode/backend/internal/email	0.476s      ← 报告 PASS
public.email_accounts | n_tup_ins=1 | n_live_tup=1
public.emails         | n_tup_ins=12
public.email_invoices | n_tup_ins=3
```

**负控才是这次的重点**：它证明这个绿**不是假绿** —— 旧写法在往生产表写数据的
同时**照样报 `ok`**。这正是 §7bp 那类事件能悄无声息发生的土壤。

负控造出来的 1 个账户 / 12 封邮件 / 3 张发票已按 `acct-greenmail%` 精确删除
（`DELETE 3 / DELETE 12 / DELETE 1`），复查 `public` 三张表 `count(*) = 0`、
无残留 schema。

### 回归

```
go vet -tags=greenmail ./internal/email/   → 0
go build ./...                              → 0
go test ./internal/email/ -count=1         → ok 35.873s
```

### 还剩的（未擅自改）

本包另有 10 个 PG 测试文件**不钉 `search_path`**，绝大多数是 `diag_*` 诊断探针，
靠 `POCKET_DIAG_*=1` + `POCKET_REAL_MAIL_DSN` 双重开关门控，`go test ./...`
不会执行。其中 `diag_merge_exec_test.go` 确实有写操作（合并重复邮件），
`realprobe_test.go` 靠 `-tags=realprobe`。**逐个审需要逐个判断它是不是
「有意指向真实库」**，与另一个会话正在做的仓库级护栏是同一件事，
**不重复做**。

---

## §7br 普查本包剩下的 PG 测试：9 个里 8 个只读，1 个是有授权的写（2026-10-02）

§7bq 修完 greenmail 两个用例之后，包里还剩 9 个测试会打开 PG 连接且**不钉
`search_path`**。逐个定性，不靠「看起来像只读」。

### 方法：两道扫描，缺一道会漏

第一道只扫 SQL 动词（`INSERT INTO` / `UPDATE ` / `DELETE FROM` / `TRUNCATE` /
`DROP ` / `CREATE TABLE` / `ALTER TABLE`）。**这道不够** —— 经 store API 的写入
（`store.UpsertXxx`、`store.MarkXxx`）一个都扫不到，而那正是本包最可能出问题的地方。
所以补了第二道：扫 `\.(\w+)\((` 形式的写方法调用。

（顺带记一个操作细节：第一版把模式写进 PowerShell 变量，字符串里的 `Delete`
触发了本地安全策略整条命令被拦。改用内容检索工具反而更直接 —— 这也正好印证
「批量内容判据用检索工具逐个看，别写临时脚本」。）

### 结果表

| 文件 | build tag | 门控开关 | SQL 写 | store 写 | 判定 |
|---|---|---|---|---|---|
| `diag_kxpms_test.go` | — | `POCKET_DIAG_ACCOUNT`+`POCKET_DIAG_ALLOW=1`+`POCKET_REAL_MAIL_DSN`+`POCKET_DIAG_DATA_DIR` | 无 | 无 | 只读探针 |
| `spam_realdata_test.go` | — | `POCKET_REAL_MAIL_DSN`+`POCKET_REAL_MAIL_SCHEMA` | 无 | 无 | 只读探针 |
| `diag_backfill_align_test.go` | — | `POCKET_DIAG_ALIGN=1`+`POCKET_REAL_MAIL_DSN`+…+`DATA_DIR` | 无 | 无 | 只读 |
| `diag_dup_report_test.go` | — | `POCKET_DIAG_DUP_REPORT=1`+… | 无 | 无 | 只读 |
| `diag_merge_plan_test.go` | — | `POCKET_DIAG_MERGE_PLAN=1`+… | 无 | 无 | 只读 |
| `diag_pop3_backfill_test.go` | — | `POCKET_DIAG_POP3_BACKFILL=1`+… | 无 | 无 | 只读 |
| `diag_pop3_invoice_test.go` | — | `POCKET_DIAG_POP3=1`+… | 无 | 无 | 只读 |
| `diag_rest_dupes_test.go` | — | `POCKET_DIAG_REST_DUPES=1`+… | 无 | 无 | 只读 |
| `realprobe_test.go` | `realprobe` | `PG_DSN`+`POCKET_REAL_KEYS` | 无 | 无 | 只读 |
| **`diag_merge_exec_test.go`** | — | `POCKET_DIAG_MERGE_EXEC=1`+… | **有** | 无 | **有意写** |

第二道扫描的命中全部是无害的：`imap.SeqSet.AddNum`、`uidSet.AddNum`、
`log.SetOutput` / `log.SetFlags`。**没有一处是 DB 写入。**

两个容易误判的点，都核过：

- `realprobe_test.go` 有一处 `UPDATE`，但它在**第 19 行的注释**里 ——
  `//  3. 不写库：本文件只 SELECT，不 INSERT / UPDATE / DELETE。`
  只扫字符串会把它当成写操作。
- `diag_kxpms_test.go` 的 `set.AddRange` / `uidSet.AddNum` 是 imap UID 集合操作，
  与持久化无关。

### `diag_merge_exec_test.go` 为什么不算缺陷

它是 2026-10-01 **经用户授权**执行的重复副本合并，属于一次性数据修复工具。
它**故意**指向真实 schema —— 那正是它的用途。安全设计是完整的：

```
:48   if os.Getenv("POCKET_DIAG_MERGE_EXEC") != "1" { t.Skip(...) }   // 硬门禁
:80   t.Fatal("backup table is empty — refusing to run a write without a rollback path")
:82   t.Logf("backup rows = %d (rollback path verified)", backupRows)
:218  t.Logf("rollback: UPDATE emails SET deleted_at=0 FROM emails_merge_backup_20261001 b ...")
```

**没有可验证的回滚路径就拒绝执行**，并在结束时把回滚语句原样打出来。这比
「自建隔离 schema」的要求更高，不是更低。

### 结论

**这 9 个里 0 个是缺陷。** 加上 §7bq 修掉的 2 个 greenmail，本包的 PG 测试现在
全部满足下面三条之一：

1. 自建隔离 schema（greenmail 两个 + `store_workspace_test.go`）
2. 只读，且门控开关 ≥ 2 个
3. 有意写真实库，但有硬门禁 + 可验证回滚路径

### 顺带一个给别人的交接项

另一个并发会话已经写出仓库级护栏 `pg_test_isolation_guard_test.go`
（在 `openpocket-wt-font` worktree），它维护一份「打开 PG 但不隔离仍然安全」的
allowlist。我读了它当前的 allowlist，**只列了 5 个文件**，本包还有 **6 个没列**：

```
diag_backfill_align_test.go / diag_dup_report_test.go / diag_merge_plan_test.go
diag_rest_dupes_test.go    / diag_merge_exec_test.go  / realprobe_test.go
```

按护栏现行规则，这 6 个会让它 FAIL。理由就是上表，逐条可核查。
**我没有去改那个文件**（不在我的 worktree，且对方正在写），只把清单记在这里，
合并时照抄即可。

---

## §7bs 需求 6：把三条路的成本差摆出来（更正「移植 Kotlin」这个说法）

需求 6 原文是「这些操作可以在设备本地进行，也可以委托服务端进行，**默认放在设备
本地进行**」。这是本轮**唯一一条真正没实现**的需求，§7az 里挂着等你定路线。
这一节只做一件事：把三条路的成本差**量化到 file:line**，让你不用凭印象选。

### 先更正一处我自己的说法

§7at 里我写的是「把邮件流水线移植到 **Kotlin**」。**这个说法是错的**，两处：

- `C:\workspace` 下**一个 `.kt` 文件都没有**（全树 depth 8 扫过）。原生层是
  **Java**：`frontend/android/app/src/main/java/com/kaixuan/opencode/pocket/plugins/`
  下是 `EmailFetchPlugin.java` / `EmailFetchRunner.java` / `EmailFetchReceiver.java`
  （外加一个 `EmailFetchReceiverTest.java`）。
- 更关键的是：**根本不存在「已存在的实现可以移植」这回事**。原生侧现在只有
  78 行的 HTTP 客户端，`EmailFetchRunner.java:13` 自己写着：

  > 后台线程委托 pocketd 收信+归类；**设备不直连 IMAP**。

  它的全部内容就是 `POST /api/emails/sync` 和 `POST /api/emails/classify`
  （`:34-35`）。所以「移植」不是搬运，是**在 Java 里重写一遍**。

### 设备本地执行要重写的东西有多大

`backend/internal/email` 的实际体量（非测试）：

```
MIME / XML 解析   5513 行   (32 个文件)
存储与 LWW        4140 行   (17 个文件)
IMAP / POP3 抓取  2792 行   ( 7 个文件)
垃圾规则          1599 行   ( 9 个文件)
PDF 网格导出       670 行   ( 3 个文件)
分类 / 飞书         323 行   ( 5 个文件)
────────────────────────────────────
包内非测试 Go 合计 11777 行 / 41 个文件（测试另有 12464 行 / 90 个文件）
```

而且**不是纯翻译**，每一块都有平台特有的硬骨头：

- 163 邮箱要求专用头信息（用户原始需求里点名了「163邮箱访问时，要有一个头信息」）
- XML 发票要**重新渲染**成 PDF（不是下载现成 PDF）
- 需求 5 的 A4 2x2 / 3x3 网格 + 裁切线，用的是 Go 的 `pdfcpu`
- 凭据加密用 `email_master.key`，设备侧要另建一套密钥存储
- 需求 8 的 LWW 要与客户端本地库对齐

粗算：**至少 1.2 万行非测试逻辑要在 Java 里重写并重新验证**，其中发票渲染和
PDF 网格是与 Go 版本**功能重复**的第二套实现（两套实现必然漂移）。

### 三条路的实际含义

| 路线 | 实际内容 | 代价 |
|---|---|---|
| **A. 设备本地执行** | 在 Java 里重写上表 1.2 万行 | 最大；且制造第二套发票/PDF 实现 |
| **B. pocketd 上设备** | 把 Go 服务端装进 Android | 要处理 SQLite↔PG 存储、后台保活、证书；架构改动大 |
| **C. 收窄为「本地触发」** | 保留现有 78 行 HTTP 客户端 + `EmailFetchReceiver` 周期调度，把需求 6 的措辞改成「触发入口在设备、处理在服务端」 | 最小；**但需求要改** |

C 的诚实之处：它**不是**把 A 做完了，而是**承认当前架构就是 B 的形态** ——
设备负责「什么时候收」，服务端负责「怎么收」。

### 我不替你选的理由

A 和 B 都是**架构级改动**，C 是**产品定义改动**。这三者要解决的问题不同
（技术能力 vs 部署形态 vs 需求边界），不该由我按「哪个看起来更省事」来定。

我能补的是：无论选哪条，上面那张表和那 1.2 万行的分解都成立，可以直接当
工作量估算的底稿用。

---

## §7bt `test:email` 的门禁阻塞：不是 cherry-pick，是**一个文件**（2026-10-02）

§7az 里我给的处置是「cherry-pick `895d950` 或 rebase」。这一节把它推翻并给出
准确成本 —— 因为**实测结果和当时的判断不一样**。

### 先纠正一处过时记录

§7az 写的阻塞是「`recording-voice-prompt.ts` 带 6 组 `<<<<<<< HEAD` 冲突标记」。
**现在不是这样了。** 并发会话已经动过这一块，当前真实状态是逐步实测出来的：

```
typecheck      exit=2   src/native/recordingRuntime.ts(49,8): TS2307 找不到模块
build:gate     exit=1   Could not resolve "./recording-voice-prompt"
test:native    exit=0
test:email     exit=0   # tests 242  # pass 242  # fail 0
check:vm-gaps  exit=0
check:i18n     exit=0
check:icons    exit=1   graphic_eq 在字体里合不出连字
```

**5 过 2 不过。** 另外确认一件之前只是推断的事：`.ts` 测试在
`node v22.23.2` 下**确实能跑**（该版本默认开启类型剥离），
`test:email` 的 242 个用例真实执行、真实通过。

### 两个失败是同一个根因

`typecheck` 报的 3 个 error，实际只有 1 个是根：

```
:49  TS2307  Cannot find module './recording-voice-prompt'      ← 根
:96  TS7006  Parameter 'engine' implicitly has an 'any' type   ← 连带
:96  TS7006  Parameter 'text'  implicitly has an 'any' type    ← 连带
```

两个 `TS7006` 的参数名 `engine` / `text` 正好对应缺失模块里的
`VoiceSpeaker = (engine: 'native'|'web', text: string) => Promise<void>` ——
模块解析不到，它的类型就是 `any`，参数于是「隐式 any」。

`build:gate` 的 rollup 报错是同一句话：`Could not resolve
"./recording-voice-prompt" from "src/native/recordingRuntime.ts"`。

**所以不是两个问题，是一个缺文件。**

### `895d950` 里的版本可以直接用，API 完全对得上

不是「cherry-pick 会不会冲突」的猜测，是逐个符号核对：

| 我分支 `recordingRuntime.ts:48-49` 导入 | `895d950` 版导出 |
|---|---|
| `RecordingVoicePrompt` | `export class RecordingVoicePrompt` ✓ |
| `makeWebSpeaker` | `export function makeWebSpeaker(` ✓ |
| `type MicTrackLike` | `export interface MicTrackLike` ✓ |
| `type VoicePromptDeps` | `export interface VoicePromptDeps` ✓ |

该文件在 `895d950` 里是 **324 行、0 个冲突标记**（`git show
895d950:frontend/src/native/recording-voice-prompt.ts`）。

### 实测：只恢复这一个文件，两个失败一起消失

```
git checkout 895d950 -- frontend/src/native/recording-voice-prompt.ts

npm run typecheck    → exit 0        （此前 exit 2）
npm run build:gate   → exit 0，✓ built in 22.06s   （此前 exit 1）
```

**整条 `gates` 因此第一次能跑到最后一步**，`test:native`（1.5s）与
`test:email`（242 个用例）**在门禁里真实执行** —— 这正是
「8 个邮件 .ts 测试接入 gates」当初想要的效果，此前一直是 0 保护。

剩下的唯一阻塞是 `check:icons`：

```
[icon-font] ❌ 1 个名字在字体里合不出连字：graphic_eq
             src\features\settings\SettingsView.vue 字面量
修法：node scripts/build-material-symbols-subset.mjs 重建字体后提交产物。
```

报错信息自带修法，是**独立于上面那件事**的一步。

### 我没有提交，理由

我自己给这个分支定过一条约束：**不擅自把录音修复混入邮件分支**。
这次虽然证据充分（一个文件、零 API 变更、两个门禁步骤转正），但它仍是
**录音/TTS 的修复，与邮件需求无关**，而且正确的落点其实在 `main`
（那边是冲突版本）。把邮件分支的形状改了，是你的决定。

所以我：临时恢复 → 实测两个失败转正 → **已用 mavis-trash 移回，工作区干净**
（HEAD 仍是 `44df48d`）。

### 成本对照（更新 §7az 的第 2 条）

| 处置 | 实际成本 | 实测结果 |
|---|---|---|
| cherry-pick `895d950` | 带进 8 个文件（含 4 个 diag 脚本、docs、测试） | 未做，收益与下同 |
| rebase | 分支形状变更 | 未做 |
| **只恢复 1 个文件** | `git checkout 895d950 -- frontend/src/native/recording-voice-prompt.ts` | **typecheck 0 / build:gate 0 / gates 跑到底** |
| 重建字体子集 | `node scripts/build-material-symbols-subset.mjs` + 提交产物 | 未做，`check:icons` 仍红 |

---

## §7az 本轮仍未验证 / 仍是阻塞

**阻塞（需要外部条件，非代码问题）**：

- `POCKET_FEISHU_INVOICE_CHAT_ID` 未提供；回调 `https://m.kxpms.cn/callback/feishu`
  未部署 → 验签/解密**只有单测证据，无真实 200 样本，不能称已修好**。
  微信回调完全未实现。
- `POCKET_KXMEMORY_BASE_URL` 未配置 → 需求 4 对**新邮件**无法生效
  （已分类的 162 封是历史数据）。

**待用户决策（写操作，不可逆）**：

1. 3 组 REAL_DUP 是否合并（阿里云周报、2 封 geopod 推送）
2. 13 封判垃圾是否跑真实 IMAP MOVE（真实邮箱本轮只做只读验证）
3. `folder_name` / `processed_at` 两个死列是否清理
4. 4 个孤儿 PDF 是否删除
5. `invoice_date` 回填：已 `downloaded` 的行永不自愈（`pipeline.go:385`
   对已建档发票直接 continue）。是否要建一条回填路径，见 §7ar。
   另需注意：**票面真实开票日期目前无从得知**（PDF 压缩流扫不出、
   正文缓存加密），所以「正确文件名」现在还定不下来。
6. **需求 6 走哪条路**（设备本地执行 / pocketd 上设备 / 收窄为「本地触发」），
   见 §7au 与 §7bs。这是**唯一一条真正未实现的需求**。
   **更正**：我此前写的「移植 Kotlin」是错的 —— 全树没有任何 `.kt` 文件，
   原生层是 Java，而且 `EmailFetchRunner.java:13` 明写「设备不直连 IMAP」，
   只有 78 行 HTTP 客户端。所以不是移植，是重写；成本量化见 §7bs
   （非测试 Go 11777 行 / 41 文件）。
7. 需求 1 的开机重排修复（`cc6753d`）**只在单元测试层验证过判定逻辑**，
   真机重启行为未验证，见 §7aw。要不要安排一次真机验证。
8. 需求 2 的**真实 IMAP MOVE 已跑通**（`4a06c28`，Greenmail standalone jar，
   不需要 Docker）。见 §7ba。
9. **step1.5 取正文每封挂 150s 的根因已查明并修掉**（`843b3c4`）：降级取正文
   通道建连后零读超时 + TLS 端口判定写死 993。用例从 8 分钟超时失败变为
   PASS 0.44s。见 §7be。
10. **「主路径是否对真实 qq/163 同样失效」已用只读探针实测：不失效**。
    4 个真实账户全部走 go-imap 主路径，268ms~1.1s 取回正文，且 UNSEEN 计数与
    目标邮件 flags 前后不变（只读被证明）。所以**不需要**为它补「重试不带
    partial」的中间路径。见 §7bf。
11. **新查出的真问题：master key 分散在三处，只有 `wt3\backend\data` 那把能
    解开真实库 5 个账户的凭据**；当前活跃的数据目录（发票文件 20:41 还在写）
    和当前 `pocketd.exe` 所在目录的 key 都解不开。已查证这**不会**导致凭据被
    自愈覆盖（邮件代码在解密失败时只返回错误），后果是「任何回退到
    `<dataDir>/email_master.key` 的进程一个账户都连不上，而库里的配置看着
    完好」。线上多半是靠 `POCKET_EMAIL_MASTER_KEY` 环境变量拿 key 的（推断，
    非实测）。**处置需先知道线上启动方式，未擅自处理**。见 §7bf。
12. **`public.chat_agents` 只剩 3 行、内置角色有 277 个 —— 来源与消失原因均未
    查清**。已排除「服务启动导入」（`ImportBuiltinAgents` 无调用者）、
    「测试写的」（`importer_test.go:166` 走 SQLite）、「被 DROP」（全仓无
    `DROP TABLE`/`TRUNCATE`）。且 `ImportBuiltinAgents(ctx, repoPath)` 要的是
    markdown 仓库路径，**不是一条现成的恢复路径**。已如实标注为「不猜」。
    见 §7bm。

13. **【需要你回答，优先级高于本分支其余所有待决项】本机 PG 里
    `opencode_pocket` schema 不存在，与 §7bh 记录的数据对不上**。
    现在 `public` 只有 11 张表、仅 `chat_agents` 有 3 行；`emails` /
    `email_accounts` 都是 0 行，且 `public.emails` 的 `n_tup_ins=0`
    （从没进过数据）。而运行中的 pocketd 确实连的就是这个本机实例，
    启动日志写着 `schema="opencode_pocket"`，现在却在报
    `relation "tasks" does not exist`。**原因未查清，我没有做任何写操作**
    （未 DROP、未从 WAL 恢复、未改 `POCKET_PG_SCHEMA`），也没找到任何
    本仓库的测试或代码会删这个 schema。详见 §7bo。

**环境问题**：

- **本分支 `internal/server` 有 2 个既有失败**（非邮件引入，本轮 diff 只碰
  `internal/email` 两个文件）：

  ```
  --- FAIL: TestTaskWriteGuardBlocksPlainMemberPatch
      task_write_guard_route_test.go:108: bob PATCH someone else's private
      work item = 404, want 403: task not found
  --- FAIL: TestTaskWriteGuardBlocksPlainMemberDelete
      task_write_guard_route_test.go:130: 同上（DELETE）
  ```

  守卫对「越权访问他人私有条目」返回 **404**，测试期望 **403**。
  这是授权语义的分歧（404 不泄露资源存在性，403 明说），与并发会话在
  `main` 上做的「404/403 区分」属于同一件事的两面。**不在邮件分支修**，
  修它需要先确定产品上要哪种语义。

- **`frontend/src/native/recording-voice-prompt.ts` 缺失（不是冲突标记了，2026-10-02 更新）**
  本分支里这个文件**根本不存在**，而 `recordingRuntime.ts:49` 导入它 →
  `typecheck` 3 个 error（1 个 TS2307 + 2 个连带 TS7006）与 `build:gate` 的
  `Could not resolve` 是**同一个根因**。实测：只从 `895d950` 恢复这**一个**文件
  （324 行、0 冲突标记、4 个导出符号与导入完全对应），`typecheck` exit 0、
  `build:gate` exit 0（22.06s），整条 `gates` 第一次跑到最后一步，
  `test:email` 的 242 个用例**在门禁里真实执行**。
  **未擅自做**（我自己定的约束：不把录音修复混入邮件分支），已移出工作区，
  工作区干净。完整实测见 §7bt。主仓那边的冲突版本仍需单独解决。
- PG 后端间歇崩溃 `0xC0000142`（非本轮引入，9-30 日志已有）。规避有效：
  绕开阻塞的 `pg_ctl`，用 `Start-Process` 直接起 `postgres.exe`。
- 两张真实 QQ Wallet 发票（uid=134/135）存量无法可靠救回。

