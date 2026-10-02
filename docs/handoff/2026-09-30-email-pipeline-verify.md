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
| `check:icons` | ✅ 0 | **2026-10-02 已修**：`graphic_eq` 字形缺失，字体子集重建。详见 §7cw | ❌ 设置页图标 |

> **2026-10-02 更新**：本表是历史快照（当时 3 红）。`typecheck` / `build:gate`
> 的 `recording-voice-prompt` 缺失已在 §7bt 解决（从 `895d950` 恢复该文件），
> `check:icons` 已在 §7cw 解决。**现在整条 `gates` 首跑 exit=0**，
> 7 个步骤全绿，`test:email` 242 用例在门禁里真实执行。

**3 个红步骤全是既有问题，没有一个与邮件有关。**（历史结论）

### 必须说明的后果：`test:email` 目前是**不起作用**的

> **2026-10-02 已作废**：下面这段说的短路问题**已经不存在了**。
> `typecheck` / `build:gate` 的 `recording-voice-prompt` 缺失在 §7bt 解决后，
> `gates` 第一次完整跑完，**`test:email` 的 242 个用例在门禁里真实执行**
> （实测 `# pass 242 / # fail 0`），`a0266a4` 那次改动从此**真的有保护力**。
> 保留原文是为了留痕：当时说「不能说邮件测试已纳入门禁所以有保护」是对的，
> 现在才可以这么说。

`gates` 是 `&&` 串联，`typecheck` 一红就短路，**`test:email` 根本轮不到执行**。

所以 `a0266a4` 那次改动在本分支上的**实际保护力为零**——它只有在
`recording-voice-prompt` 那个缺失模块被解决之后才开始生效。
这一点不能含糊：不能说「邮件测试已纳入门禁所以有保护」。

`check:icons` 的修法脚本自己都写明了：
`node scripts/build-material-symbols-subset.mjs` 重建字体后提交产物。
~~属录音/设置页的事，**未擅自处理**。~~ **2026-10-02 已按此修完，见 §7cw。**

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

## §7bu 恢复途径逐一排除：重建只需重启，但账户配置要手工回填（2026-10-02）

§7bp 把时间窗钉住了，§7bo 停在了「不知道怎么办」。这一节把**能不能救**这件事
做完，全是只读检查，没有执行任何恢复动作。

### 一、数据库层面的恢复：确定不可能

```
archive_mode     = off
archive_command  = (disabled)
wal_level        = replica          （但没有归档，replica 级别没有意义）
data_directory   = C:/workspace/openpocket/logs/pg/data
```

- **没有 WAL 归档 = 没有 PITR**。`DROP SCHEMA ... CASCADE` 的记录和数据页都只在
  WAL 段里，段会被循环覆盖。
- `pg` 目录下**没有任何** `*.backup` / `base.tar*` / `*.dump`。
- 部署脚本 `rebuild-db-local.sh` 的默认备份目录
  `${POCKET_BACKUP_DIR:-~/Downloads/kaixuan/opp/backup}` —— **不存在**，
  连 `~/Downloads/kaixuan` 都没有。
- `pg_waldump.exe` 本身在，但如上所述没有可用的历史段。

**结论：数据层面的恢复是零。** 这不是「还没找」，是「不存在这条路」。

### 二、重建本身：不需要手工 SQL，重启 pocketd 就行

这一点很重要，因为它决定了处置的**代价**：

```
cmd/pocketd/main.go:73   db.New(ctx, cfg.PostgresDSN, cfg.PostgresSchema)
internal/db/pg.go:80     createSchemaOnce → CREATE SCHEMA IF NOT EXISTS
cmd/pocketd/main.go:126  email.NewStore(pool)   → 自己的 migrate
```

`db.New` 启动时就会 `CREATE SCHEMA IF NOT EXISTS`，之后各 store 的构造函数
各自跑 migration。所以 **pocketd 一重启，schema 和全部表结构就回来了（空的）**。

不需要我写任何 DDL，也不需要手工 `CREATE SCHEMA`。

### 三、但数据不会自己回来，分三类

**确定丢失（无任何副本）：**

- `email_accounts` 5 行 —— 凭据是加密的，加密密钥的分布问题另见 §7bf
- `emails` 120 行
- `email_invoices` 行
- **各账户的 `last_synced_uid` 水位** —— 丢了意味着重连 IMAP 后会**从头抓**，
  而不是增量。这既是坏消息（慢、可能撞 UNIQUE）也是好消息（不会漏邮件）

**确定没丢：**

- 磁盘上的发票 PDF：`data\email-invoices\ws_user-admin\` 5 个文件，
  `data\email-invoices\exports\ws_user-admin\` 一批 A4 导出与汇总
- 正文缓存：`data\email-bodies\` 41 个文件（加密）
- **凭据本身** —— 5 个邮箱的 IMAP/SMTP 参数与授权码都在你最初的需求文本里
- 代码（显然）

### 四、账户配置怎么回来：脚本在，但它的凭据源不在

`scripts/seed_email_accounts.sh` 就是为此存在的：它通过 HTTP API 把 5 个账户
种回 PG，**幂等**（同 emailAddress 已存在则 PUT 刷新），凭据只从
**仓库外**的 envs loader 读、不写进仓库。

但：

```
ENVS_LOADER 默认 = $HOME/workspace/ai-native-tools/envs/loader.sh
→ C:\Users\86133\workspace\ai-native-tools\envs\loader.sh
→ 不存在（C:\workspace 下也没有任何 envs 目录）
```

**所以这个脚本在当前机器上跑不起来**，缺的是 envs loader，不缺脚本。

可行的替代：按 API 直接重填 5 个账户，凭据从你最初的需求文本取。代价是手工，
不是不可行。

### 五、所以完整路径是这样（**未执行，等你点头**）

1. **先备份磁盘上还在的东西**：`data\email-invoices\` 与 `data\email-bodies\`。
   数据库可以重建，这些文件重建不出来。
2. 重启 pocketd → schema 与表结构自动回来（空）。
3. 手工回填 5 个邮箱账户（IMAP/SMTP/授权码）。
4. 触发一次同步 → 从 IMAP 从头抓，邮件回来。
5. 发票由 pipeline 重新 harvest（`email-invoices` 行会重建，
   PDF 重新下载；磁盘上已有的 5 个旧文件会变成「无 DB 记录的孤儿」）。

第 5 步的副作用正好是 §7az 里那条待决项（「4 个孤儿 PDF 是否删除」）——
到那时它们的意义会变，得重新看一遍。

### 六、我仍然什么都没做

没有 `CREATE SCHEMA`、没有重启 pocketd、没有回填账户、没有删任何文件。
第 1 步的备份尤其该由你决定**备份到哪里**——那是个写操作，而且是覆盖不了的。

---

## §7bv 给飞书出站客户端补测试，**当场抓出一个真 bug**：`content` 多包了一层（2026-10-02）

需求 3（发票文件发飞书）与需求 4（重要邮件提醒）这两条，线上都因为缺配置而
**从未真正执行过一次**（`POCKET_FEISHU_INVOICE_CHAT_ID` / `POCKET_KXMEMORY_BASE_URL`
未设）。我此前把它们记为「已实现、只是缺外部条件」——这个说法**过于乐观**：
实现存在 ≠ 实现正确。给它们补上测试之后，第一轮就红了。

### 缺口本身

`internal/feishu/client.go` 的三个出站方法此前**一个测试都没有**：

```
TenantAccessToken   client.go:64    → 需求 3/4 的前提
UploadFile          client.go:98    → 需求 3「发票文件发到飞书」
SendMessage/Text/File/InvoiceFile  client.go:147/175/181/192
```

而同包的 `sheet_test.go` **已经**用 `httptest.NewServer` 覆盖了表格 API，
`client.go:30-31` 也明写「BaseURL 默认 https://open.feishu.cn；**测试可覆盖**」——
基础设施是现成的，只是没人写。

### 抓到的 bug

```go
// 修前
payload, _ := json.Marshal(map[string]string{"content": contentText})
body, _ := json.Marshal(map[string]any{..., "content": string(payload)})
```

飞书 `im/v1/messages` 的 `content` 字段要的就是**消息体本身**（text 时是
`{"text":"..."}`，file 时是 `{"file_key":"..."}`），只不过它要求这个消息体以
**字符串**形式出现 —— 也就是**只双重编码一次**。原代码把它包成
`{"content": contentText}` 再塞进去，于是线上实际发出的是：

```json
{"receive_id":"...","msg_type":"text","content":"{\"content\":\"{\\\"text\\\":\\\"...\\\"}\"}"}
```

飞书解出来看到的是一个叫 `content` 的**未知字段**。后果：

- `SendText` → 消息正文为空（需求 4 的提醒等于没发）
- `SendFile` / `SendInvoiceFile` → 拿不到 `file_key`，文件消息发不出去（需求 3 直接废）

**`SendText` 和 `SendInvoiceFile` 对真实飞书 API 必然失败。**

### 为什么这个 bug 能活下来

只有一个原因：**这条路径从未被执行过**。需求 3/4 在线上都是关的，
`Available()` 返回 false，上层走共享文档兜底（§5b），
`UploadFile` / `SendMessage` 一次都没被调到。绿灯全来自与它无关的包。

这就是「单测全绿 ≠ 功能可用」的另一个实例：这次的盲区不在构造器，
而在**整条调用链从未被触发**。

### 修法与验证

一行：把 `content` 直接设为 `contentText`，不再包内层。

```
修后：go test ./internal/feishu/ -count=1  → ok 1.290s（12 个新用例全绿）
```

**负控**（把 `payload` 那行塞回去，其余不动）：

```
client_test.go:317: content.text = "", want 发票已归档
client_test.go:354: file_key not threaded into the message: map[content:{"file_key":"key_42"}]
FAIL
```

精确复现、且只有这两条红。恢复修复后 `payload` 在文件中出现 **0 次**。

### 新增用例锁住的行为

| 用例 | 锁住的语义 |
|---|---|
| `TenantAccessToken_PostsAppCredentialsAndParsesSnakeCase` | 路径 + app_id/secret + 飞书特有的 snake_case 响应 |
| `TenantAccessToken_CachesUntilExpiry` | 缓存真生效（3 次调用只打 1 次飞书） |
| `TenantAccessToken_UnconfiguredFailsWithoutRequest` | 无凭据直接失败，**且不发请求** |
| `UploadFile_SendsMultipartWithAuthAndReturnsFileKey` | `Bearer` 头 + `file`/`file_type=pdf`/`file_name` 三字段 + 字节原样 |
| `UploadFile_EmptyFileKeyIsAnError` | code≠0 与空 key 都必须报错 |
| `SendMessage_DoubleEncodesContentAndSetsReceiveIDType` | **本次 bug 的哨兵**：`content` 解码后仍须是合法 JSON |
| `SendFile_UploadsThenSendsFileMessage` | 先传后发，file_key 必须串进消息 |
| `SendInvoiceFile_UsesChatIDAndPassesNameThrough` | 需求 3 入口固定 `chat_id` |
| `SendMessage_HTTPErrorIncludesBody` | 非 200 必须把响应体带进 error |
| `TenantAccessToken_ConcurrentCallersHitEndpointOnce` | Client 声称并发安全，16 个 goroutine 只打 1 次 |

### 回归

```
go build ./...                 → 0
go vet ./...                   → 0
go test ./internal/feishu/...  → ok 1.427s
go test ./internal/email/...   → ok 81.549s / rules ok 0.536s
```

### 仍未验证（边界要说清楚）

mock server 能证明**协议层**——URL、鉴权头、multipart 结构、双重编码。
证明不了：真实群权限、真实文件大小限制、事件回调解密与验签的联调、
以及「发票真的出现在群里」。后者仍然需要
`POCKET_FEISHU_INVOICE_CHAT_ID` + 回调部署。**不能说「需求 3 已修好」。**

---

## §7bw 用覆盖率给「从未被执行」的路径定点（2026-10-02）

§7bv 抓到飞书那个 bug 之后，一个自然的问题是：**还有多少代码是「有实现、但一次都没
被执行过」的？** 绿灯可能全来自没碰到它的包。用覆盖率把这类路径系统性地找出来。

### 两组数字

```
go test ./internal/email/ -coverprofile                     → 50.4%   0% 函数 122
go test -tags=greenmail ./internal/email/ -coverprofile     → 54.3%   0% 函数 116
```

加上 `-tags=greenmail` 只多覆盖了 **6 个**函数。所以这 116 个**不是被 build tag
藏起来的**，是任何测试都碰不到的。

按文件分布（0% 函数数）：

```
store.go 31   scheduler.go 17   fetcher.go 11   pipeline.go 10
oauth_callback.go 9   invoice_store.go 7   invoice_harvest.go 5
store_inbox.go 4   oauth.go 4   pop3_fetcher.go 4   mime.go 3
cleanup.go 3   store_cleanup.go 3   junk.go 3   其余各 1
```

### 0% ≠ 有缺陷：两类要分开

**（一）接线式，0% 很正常**，不必当问题：

```
scheduler.go:142 SetKxmemory        :148 SetOAuthRefresher    :160 SetBroadcaster
             :165 SetVacationSender :221 SetTimezoneOffset   :226 timezoneOffset
             :266 LastTickUnix      :271 NextTickUnix
```

这些是 setter / 一行 getter，被 `main.go` 接线时用，测试不需要碰。

顺带澄清一个我自己差点搞错的点：看到 `LastTickUnix` / `NextTickUnix` 0% 时，
我以为 §7aw 说「需求 1 重排已在单元测试层验证过」是假的。**查了才发现那说的
是另一件事** —— `cc6753d` 是 **Android 侧**的修复，改的是
`AndroidManifest.xml` + `EmailFetchReceiver.java` + 一个 **JUnit 5** 用例
（开机重排闹钟），与这两个 Go 函数无关。**原说法准确，不用改。**

**（二）带逻辑的，0% 才是猎场**：

```
scheduler.go:281  pollLoop            主轮询循环          → 需求 1
scheduler.go:301  refreshLoop         账户刷新循环        → 需求 1
scheduler.go:319  refreshOnce         单次刷新            → 需求 1
scheduler.go:748  runDailySummary     每日摘要            → 需求 4
scheduler.go:796  summarizeUser       单用户摘要          → 需求 4
invoice_harvest.go:85   HarvestAll     采集入口            → 需求 3
invoice_harvest.go:396  savePDF        落盘                → 需求 3
invoice_harvest.go:435  downloadPDF    下载                → 需求 3
invoice_harvest.go:643  InvoiceContentHash  内容哈希去重   → 需求 3
```

`InvoiceContentHash` 值得单独点一下：它是发票**去重**的依据，
0% 意味着「按内容哈希去重」这条逻辑没有任何测试钉住。

### 结论与用法

**不宣称「这些都有 bug」** —— 覆盖率低本身不是缺陷，`pollLoop` 这类循环也确实
难以用单测覆盖（它跑在真实时钟 + 真实网络 + 真实 DB 上）。

但 §7bv 已经证明了一次：**0% 的路径里藏着一个让需求 3 直接失效的 bug**。
所以这份清单的正确用法是「**下一轮排查的优先级排序**」，不是缺陷列表：

- 优先级 1：`downloadPDF` / `savePDF` —— 需求 3 的成败点，
  且**可以用 mock HTTP server 覆盖**（和 §7bv 同样的手法，不需要真实邮箱）
- 优先级 2：`InvoiceContentHash` —— 纯函数，最容易写测试
- 优先级 3：`refreshOnce` —— 需求 1 的开机重排判定，可以注入假时钟
- 优先级 4：`pollLoop` / `runDailySummary` —— 循环体，适合用假 store + 假时钟

**未做**：这一节只做定位，没有补这些测试，也没有改任何代码。

---

## §7bx 照 §7bw 的优先级 1 动手：超限 PDF 静默截断，被记成「已下载」（2026-10-02）

§7bw 把 `downloadPDF` / `savePDF` 排在优先级 1，并写了理由：「需求 3 的成败点，
且可以用 mock HTTP server 覆盖」。这一节就是执行这条排序的结果 —— **又抓到一个
真缺陷**，形态比 §7bv 那个更隐蔽：它不报错。

### 缺陷

`invoice_harvest.go:435` `downloadPDF` 修前最后一行：

```go
return io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes))
```

`MaxInvoicePDFBytes = 20 << 20`（`invoice_harvest.go:36-37`）。
`io.LimitReader` 到上限就**停止读取并正常返回 nil error** —— 也就是说
「文件超过 20MB」和「文件恰好 20MB」在这里**完全无法区分**。

调用方 `invoice_harvest.go:348` 只做一件事：

```go
if !isPDFBytes(data) { ... }
```

而 `isPDFBytes` 只看头部 magic。被切掉尾巴的 PDF，**头部 `%PDF-` 完好无损**
→ 判定通过 → `savePDF` 落盘 → 状态写成 `downloaded`。

**净效果**：一个打不开的 PDF 被存进 `email-invoices/`，数据库里记着
「已下载成功」，`last_error` 是空的，日志里没有任何异常。需求 3 交给用户的
凭证附件是坏的，而且**没有任何地方会告诉你**。

为什么这在真实场景里会发生：开票平台返回的「PDF」偶尔是扫描件合集或
带完整附件流的响应，几十 MB 完全可能。

### 修法

```go
body, err := io.ReadAll(io.LimitReader(resp.Body, MaxInvoicePDFBytes+1))
if err != nil {
    return nil, err
}
if int64(len(body)) > MaxInvoicePDFBytes {
    return nil, fmt.Errorf("invoice file too large: exceeds %d bytes", MaxInvoicePDFBytes)
}
return body, nil
```

多读 1 字节再回头看长度 —— 这是判「超过上限」的最小代价写法（多 1 字节，
不是多读整个 body）。**必须返回 error 而不是返回截断内容**，否则调用方
照样会把它当成功。

### 负控（决定性）

把修复改回旧写法，跑同一个用例：

```
=== RUN   TestDownloadPDF_OversizeIsRejectedNotTruncated
    invoice_download_test.go:107: oversize download must fail; got 20971520 bytes silently
--- FAIL: TestDownloadPDF_OversizeIsRejectedNotTruncated (0.07s)
=== RUN   TestDownloadPDF_JustUnderLimitStillPasses
--- PASS: TestDownloadPDF_JustUnderLimitStillPasses (0.06s)
```

`got 20971520 bytes silently` 就是缺陷的原始形态：**正好 20MB、零错误**。
注意 `JustUnderLimit` 在负控下仍然 PASS —— 这证明断言卡的是「超限」，
不是「一律拒绝」。

### 顺带钉住「第二道防线为什么不够」

新增 `TestIsPDFBytes_CannotDetectTruncation`：构造修前代码会产出的那个字节串
（正好 20MB、头部合法），断言 `isPDFBytes` **对它返回 true**。

这不是缺陷测试，是**防退化**测试：它把「isPDFBytes 挡不住截断」这个事实写成
可执行断言，免得后来者以为「下游还有个 magic 检查」而去削弱 `downloadPDF`
的上限判定。

### 测试清单（`invoice_download_test.go`，8 个用例）

| 用例 | 锁住的行为 |
|---|---|
| `TestDownloadPDF_SendsUAAndAcceptHeaders` | UA 非空 + Accept 含 `application/pdf`（部分开票平台对空 UA 直接 403） |
| `TestDownloadPDF_Non200IsAnError` | 403 的 HTML 错误页不能被当 PDF 收下 |
| `TestDownloadPDF_OversizeIsRejectedNotTruncated` | **超限报错，且不把截断内容回传给调用方** |
| `TestDownloadPDF_JustUnderLimitStillPasses` | 负控对照：差 1KB 仍原样通过 |
| `TestIsPDFBytes_CannotDetectTruncation` | 防退化：截断的 PDF 过得了 magic 检查 |
| `TestSaveInvoiceFile_WritesCanonicalNameAndMarksDownloaded` | 规范名 `{费用类型}-{对方单位}-{金额}-{日期}.pdf`、落盘内容一致、状态回写、无 `.tmp` 残渣 |
| `TestSaveInvoiceFile_FillsDateFromBytesWhenMissing` | 缺日期时不生成带空日期的文件名 |
| `TestSaveInvoiceFile_UnwritableDirIsNotReportedAsDownloaded` | 目录不可写时不得报 `downloaded`，且必须留下原因 |

后三个需要 PG，用的是本包自己的 `newWorkspaceTestStore`（已钉 `search_path`
并在 cleanup 里 DROP 自己的 schema），不碰 `public`（§7bq 的规矩）。

### 回归

```
go build ./...                    build=0
go vet  ./...                    vet=0
go test       ./internal/email/...   ok 50.327s / rules ok 0.394s
go test -race ./internal/email/...   ok 48.200s / rules ok 1.401s   0 DATA RACE
```

### 这一节的元教训

§7bw 我写的是「**不宣称这些都有 bug**」，并把清单定性为「下一轮排查的
优先级排序」。这条纪律是对的，而且**正因为克制才有效** —— 如果当时直接写成
「这些路径有缺陷」，下一轮就会带着结论去找证据，而不是去看代码。

结果是优先级 1 的两条里，**两条都真的有缺陷**（`downloadPDF` 静默截断；
`savePDF` 本身没查出问题，是 `TestSaveInvoiceFile_WritesCanonicalNameAndMarksDownloaded`
顺带确认了它是对的）。

同时也要说清边界：`pollLoop` / `runDailySummary` 这类跑在真实时钟上的循环
**本节没有覆盖**，它们是不是也有问题**仍然未知**，不宣称。

---

## §7by schema 已经回来了，但**处理链路一行没跑过**（2026-10-02 00:3x）

§7bo 报告「`opencode_pocket` schema 不存在」并给出 5 步恢复路径，§7bu 逐一排除了
所有恢复途径。用户随后点头执行恢复。**动手前先查现状，发现前提已经不成立** ——
这一节记的是那个转折。

### 前提已变：schema 存在，数据也在

```
select nspname from pg_namespace ...  ->  opencode_pocket 存在
opencode_pocket 下 68 张表（含 email_accounts / emails / email_invoices）
```

行数：

| 表 | 行数 |
|---|---|
| `opencode_pocket.email_accounts` | **5** |
| `opencode_pocket.emails` | **120** |
| `opencode_pocket.email_invoices` | 0 |
| `opencode_pocket.users` / `workspaces` | 1 / 1 |
| `opencode_pocket.tasks` / `scheduled_tasks` | 0 / 0 |

5 个账户的 `created_at` 全是 **`2026-10-01 23:56:02`**（`updated_at` 同值），
即**在我上轮报告之后、用户点头之前**就有人完成了一次完整回填。
凭据密文长度全部 60，一致。

**所以「执行恢复」这件事不需要我做。** 备份仍做了
（`D:\temp\email-backup-20261002-0030`，`email-invoices` 132 文件 +
`email-bodies` 41 文件，逐目录文件数与源比对 MATCH；`exports` 在
`email-invoices\exports` 下，127 文件，已含在内）—— 备份的价值在于
后续任何写操作都有退路，不在于它是否必要。

### 但真正的结论是下一句

120 封邮件确实是真实数据（覆盖 4 个账户，时间跨 2026-09-05 → 2026-10-01），
可是：

```
select count(*) filter (where processed_at > 0)          -> 0
select count(*) filter (where category <> '')            -> 0
select count(*) filter (where importance <> '')          -> 0
email_invoices                                             -> 0 行
```

**采集回写、分类、重要性判定、发票解析，一样都没跑过。**

这把需求的定性彻底改了。§7bo/§7bu 的框架是「数据库没了，怎么恢复」，
而实际局面是「数据库好了，**处理链路一次都没被真实数据走过**」。
需求 1（定时/手工收信处理）、2（垃圾清理）、3（发票采集）、4（重要提醒）
的真实实现，在这份 120 封的数据上**零执行证据**。

### master key：四选一，不是「找不到」

用 `realprobe_test.go`（`-tags=realprobe`，第 1 步纯本地不发网络）逐把试：

```
真实库里启用账户 5 个
KEY C:\workspace\openpocket\data\email_master.key                    可解出 = 0
    AllDecryptable=false AllFailed=true
    -> MASTER KEY LOOKS WRONG: ... cipher: message authentication failed
      (first: account=acct-1790870162070526000-4 feikemanager1@163.com)
KEY C:\workspace\openpocket\wt3\backend\data\email_master.key        可解出 = 5
    AllDecryptable=true AllFailed=false
    -> all 5 enabled email account(s) decrypt with the current master key
KEY C:\workspace\openpocket\backend\data\email_master.key             可解出 = 0
KEY C:\workspace\openpocket-wt-maildeploy\backend\data\email_master.key 可解出 = 0
```

**只有 `wt3\backend\data\email_master.key` 一把能解开全部 5 个账户。**
这与 §7bf 的结论一致（真实库的 key 在 wt3），现在拿到了逐把对照的硬证据。

注意那三把错 key 的输出形态：`cipher: message authentication failed`。
**这就是 §7bg 那个启动自检在生产里会打印的那一行** —— 当初写它的时候
只是推演用户「应该」看到什么，这次是真实抓出来的。

### 因此下一步不是「恢复」而是「跑一次」

恢复已由别人完成；现在要回答的是需求 1-5 在真实数据上到底能不能跑通。
但这一步**不是纯读操作**，它会：

1. 写 `body_path` 正文缓存到磁盘
2. 调 LLM 做分类（走 `llm.kxpms.cn` 那把 key，**仍在计费可用**）
3. 对判为垃圾的邮件执行 **IMAP MOVE**（真实改动邮箱状态，13 封待决）
4. 下载发票 PDF 并尝试**发飞书**（`POCKET_FEISHU_INVOICE_CHAT_ID` 仍缺）

第 3 步不可逆，所以**先只跑 1+2（采集 + 分类，不 MOVE、不发飞书）**，
把 120 封的分类结果拿出来看，再单独决定要不要动 IMAP。

**未做**：上述分步尚未执行，等用户对「允许对真实邮箱跑采集+分类」点头。

---

## §7bz 更正 §7bw 的一个错误推断：发票「去重」根本没有接线（2026-10-02）

§7bw 把 `InvoiceContentHash`（`invoice_harvest.go:661`）列为优先级 2，并写：

> `InvoiceContentHash` 值得单独点一下：它是发票**去重**的依据，
> 0% 意味着「按内容哈希去重」这条逻辑没有任何测试钉住。

**这句话是错的。** 它是从函数上方那行注释「供测试与幂等校验」推出来的，
我没查调用点。全仓 grep 的实际结果是：

```
invoice_harvest.go:660  // InvoiceContentHash 供测试与幂等校验...
invoice_harvest.go:661  func InvoiceContentHash(b []byte) string {
docs/handoff/...         （本文件里的三处提及）
```

**零生产调用点、零测试调用点。** 这不是一个「覆盖不足的逻辑」，
是一个**根本没被使用的函数**。上一轮把它当既有功能来讨论，本身就错了。

### 去重实际靠什么

`email_invoices` 的索引（实测）：

```
email_invoices_pkey        UNIQUE (id)
email_invoices_email_id_key UNIQUE (email_id)
idx_email_invoices_ws       (workspace_id, user_id, created_at DESC)
idx_email_invoices_status   (workspace_id, status)
```

写入路径 `invoice_store.go:98`：

```sql
ON CONFLICT (email_id) DO UPDATE SET ...
```

所以去重语义是 **「按 email_id」**：同一封邮件重复采集幂等更新。
采集侧 `pipeline.go:455` 每个 email 候选只产出**一个** `c.inv`
（`ExtractInvoiceLoose` 返回单个 `Invoice`），所以这个 `ON CONFLICT`
在当前架构下**不是覆盖 bug**，是正常的幂等路径。

### 但顺着查下去发现两条真实的能力边界

**边界 1：一封邮件含多张发票 → 只记第一张。**
解析器返回单个 `Invoice`，没有「一张邮件产出 N 张」的形状。
需求 3 写的是「将发票文件进行整理…汇总金额」，若一封邮件里有多张，
其余的**静默丢失**，汇总金额相应少算。这是能力缺口，不是 bug ——
没有任何代码声称支持多张。

**边界 2：同一张发票出现在两封邮件 → 记两行，金额翻倍。**
`invoice_no` 列存在，但**没有任何唯一约束**（见上面 4 个索引）。
发票被转发、重发、或同一张票分别进两个邮箱时，`email_id` 不同 → 两行。

`InvoiceContentHash` 恰好是为边界 2 准备的 —— 但它没被接线。
接线需要三件事一起做：加内容哈希列、加唯一约束、决定冲突时保留哪行。
这是设计变更，**等用户拍板**，本节不做。

### 本节实际产出

`invoice_hash_test.go`，3 个用例（全部实测 PASS）：与 `sha256.Sum256`
逐字节一致、64 位 hex；`nil` 与空切片一致且等于空串的已知 sha256
（`e3b0c442…`），不返回空串——否则「没算出来」和「内容为空」无法区分；
差一个字节必然不同、相同输入必然稳定（它作为去重依据的唯一前提）。

**刻意没写的东西**：一个「断言它尚未被接线」的守卫用例只能写成 `t.Skip`，
而**永远跳过的测试是假守卫** —— 让人以为这事被盯住了，实际什么也没盯。
「零调用点」这个事实由 grep 给出，且会在有人接线时由那处改动本身暴露。

---

## §7ca 又一次按位置猜职责：`refreshOnce` 不是开机重排（2026-10-02）

§7bw 的清单里写：

> 优先级 3：`refreshOnce` —— 需求 1 的开机重排判定，可以注入假时钟

**这行是错的。** `scheduler.go:319 refreshOnce` 读完整实现是
**OAuth token 定时刷新**（`ListExpiredOAuthTokens` → `GetAccountByIDScoped` →
查 provider 配置 → `RefreshAccessToken` → 永久失败才撤销），
与「开机重排」毫无关系。

开机重排的真实位置是 **Android 原生层**：`cc6753d` 修的就是
`EmailFetchReceiver` 的 `BOOT_COMPLETED` / `MY_PACKAGE_REPLACED`
intent-filter，并抽出静态纯函数 `shouldReschedule(action)` 供 JUnit 覆盖 ——
**那部分已有 5 个用例 + 负控**（负控：让 `shouldReschedule` 恒 false → 2 个转红）。

### 这是同一个错误的第二次

§7bz 是第一次：`InvoiceContentHash` 被我当成「去重依据」，
实际零调用点。两次的共同形状是：**我按「文件在哪个模块 / 函数挨着什么」猜职责，
没读实现**。区别只在于这次我甚至没读就写进了优先级清单。

写覆盖率清单这类东西，最容易犯的错就是给每个 0% 函数配一句
「它大概是干什么的」—— 读起来像结论，其实只是位置推断。
**清单里凡是带「大概 / 应该是 / 用于 X」的，一律要回读实现才能留。**

### 实际补的测试（`scheduler_refresh_test.go`，7 个用例）

`refreshOnce` 值得测的理由在 `oauth_refresh.go` 的注释里写得很直白：

> 5xx and 429 are transient. Anything else falls into the transient bucket so
> the scheduler will retry on the next tick instead of **nuking the account**.

「nuking」= `RevokeOAuthTokenScoped`（`store.go:2139-2143`）：
`DELETE FROM email_oauth_tokens` + `UPDATE email_accounts SET auth_type='password',
enabled=FALSE`。**撤销之后这个账户不再收信，用户必须重新走一次 OAuth 授权。**

所以核心命题只有一条：**临时失败绝不能撤销账户**。

| 用例 | 锁住的语义 |
|---|---|
| `NilRefresherReturnsBeforeTouchingStore` | 早退在 store 之前 —— **故意把 store 设成 nil 指针**，越过早退就会 panic。「不 panic」比「mock 没被调用」更强，不可假绿 |
| `NilCryptoReturnsBeforeTouchingStore` | 同上，另一条早退 |
| `UnconfiguredProviderSkipsWithoutRevoking` | provider 没配好就跳过，且账户**保持启用** —— 还没试过刷新就禁掉是最糟的误伤 |
| `TransientFailureKeepsTokenAndAccount` | **核心断言**：临时失败 token 行仍在、账户仍启用；且 refresher 收到的是**解密后的明文** |
| `PermanentFailureRevokes` | 永久失败才撤销：token 行归零 + `auth_type='password'` + `enabled=false` |
| `SuccessPersistsNewTokenAndPushesExpiry` | 成功时 `expires_at` 被推到未来（否则每 5 分钟刷一次），且不禁用账户 |
| `UncoveredCodesAndUnknownStatuses` | 只补已有表格**确实没有**的两点：`invalid_request` / `invalid_scope` 两个永久码；**非标准 status**（418/499/599）必须落 transient |

最后一条是刻意收窄的。`oauth_refresh_test.go:183` 的
`TestClassifyRefreshStatus` 已经覆盖 5xx / 429 / invalid_grant /
invalid_client / unauthorized_client / 400-unknown / 410 / network / 200，
`TestIsPermanentRefreshError_TypeAssertions` 也已经测了非 `RefreshError`。
我第一版把那些**全部重抄了一遍**（还起名叫 `_5xxAnd429AreTransient`，
名字只提 5xx/429 实际测了 10 个 case）—— 那是维护负担，不是保障，已删。

### 负控（双向转红）

把 `scheduler.go:362` 的 `if !IsPermanentRefreshError(err)` 反转成
`if IsPermanentRefreshError(err)`：

```
TestRefreshOnce_TransientFailureKeepsTokenAndAccount
    token rows = 0, want 1: a transient failure must NOT revoke      FAIL
TestRefreshOnce_PermanentFailureRevokes
    token rows = 1, want 0: a permanent failure must revoke          FAIL
```

**两个方向都红了**，说明这两条断言都不是「怎么都绿」的那种。
（第一次注入我写成 `if !IsPermanentRefreshError(err) && true` ——
`&& true` 恒真，**逻辑根本没变**，差点据此判定护栏失效。
注入后必须打点确认「注入生效=True」再读结论。）

---

## §7cb 需求 6 走 A 路线的硬约束：Go 不能编到 WASM 去跑 IMAP（2026-10-02）

用户选了 A（设备本地执行）。动手前先验一件事 —— **不是「怎么写」，是「能不能」**。

### 硬约束：`js/wasm` 下所有 socket syscall 直接 ENOSYS

Go 1.27.1 源码 `src/syscall/net_js.go`（`//go:build js && wasm`）：

```go
func Socket(proto, sotype, unused int) (fd int, err error) { return 0, ENOSYS }
func Connect(fd int, sa Sockaddr) error                     { return ENOSYS }
func Sendto(fd int, p []byte, flags int, to Sockaddr) error { return ENOSYS }
func Recvfrom(fd int, p []byte, flags int) (n int, from Sockaddr, err error) { return 0, nil, ENOSYS }
```

`src/net/fd_js.go` 的包注释把话说明白了：

> **Fake** networking for js/wasm. It is intended to allow **tests** of other
> package to pass.

而且这解释了为什么**编译期完全看不出来**：我写了一个真实用例
（`net.DialTimeout("tcp","imap.163.com:993")` + `crypto/tls` 握手），
`GOOS=js GOARCH=wasm go build` **exit 0，产出 8,094,699 字节的 wasm**。
`net/tcpsock_posix.go` 的 build tag 明确写着 `unix || js || wasip1 || windows`。
**编译通过，运行时必挂** —— 只在真正建连时暴露。

所以「把 Go 编成 wasm 塞进 WebView 就完成了需求 6」这条路是**不通的**。
IMAP/POP3/SMTP 必须走真实 TCP，而 WebView 里的 Go 拿不到 socket。

设备端能执行真实 TCP 的只有 **Java 层**（`javax.net.ssl.SSLSocket`）。
现有 `EmailFetchRunner.java` 78 行里只有两个 HTTP POST，
类注释第一句就是「后台线程委托 pocketd 收信+归类；**设备不直连 IMAP**」。

### 但 A 路线的成本比 §7bs 估的低一个量级

关键在于把「触及 socket 的面」量出来。全包 grep
`net.Dial|tls.Client|imapclient.Dial|pop3.|smtp.Dial` 只命中 **3 个生产文件**：

| 文件 | 行数 | socket 触点 |
|---|---|---|
| `fetcher.go` | 1070 | 6 |
| `pop3_fetcher.go` | 661 | 8 |
| `mime.go` | 583 | 3 —— **只有 `fetchRawByTextproto` 一个函数**（178-260 行） |

全包非测试共 **40 文件 / 11502 行**（§7bs 记的 11777/41 是更早的快照）。

也就是说：

- **必须用 Java 重写**：IMAP 主路径 + POP3 + 那一个 textproto 通道
  ≈ **1700 行量级**，且里面最值钱的不是协议样板而是**已踩过的坑**：
  `net.Dialer.Timeout` 只管三次握手（`mime.go:188-190` 原注释）、
  建连后必须自己夹 deadline、POP3 回退要拿到同一份预算（`bb21c6d`/`6c78fbb`）、
  partial 与 literal 之间那个空格（`§7be`）
- **纯计算，可复用**（≈9800 行）：MIME/XML 解析、垃圾规则 1599、
  PDF 网格 670、分类飞书 323、发票提取/命名/汇总

纯计算那部分有两个去处：编成 wasm（设备端已有 `sql-wasm.wasm`，
说明 wasm 运行时可用），或直接移植成 TS。
**注意一个尚未量化的成本**：wasm 堆 ↔ SQLite 之间的数据 marshalling，
这一步的真实开销我**没有测**，不宣称。

### 本节的边界

这一节**只做了可行性判定与面的量测**，没有写任何 A 路线的实现。
分阶段方案、每阶段的验收口径、wasm/SQLite marshalling 的取舍都**还没定**。
文档里出现「≈1700 行」时，指的是**触及 socket 的文件行数**，
不是「重写工作量」—— 这两者的差额正是那些已修过的 deadline / 预算 / 协议细节。

---

## §7cc 补上 §7cb 留的口子：纯逻辑编成 wasm **可行**，但要付 4.85MB（2026-10-02）

§7cb 末尾我留了一句「wasm 那一半没有验证」。这一节就是那个验证。

### 为什么 `go build` exit 0 不能当判据

上一轮实测：`GOOS=js GOARCH=wasm go build` 对 `internal/email`（**含 socket**）
和 `internal/email/rules`（**零 socket**）**都** exit 0。判据毫无区分度 ——
因为 §7cb 那个 `ENOSYS` 是**运行时**错误，编译期完全看不见。

唯一有区分度的判据是：**编出可执行产物、在宿主里真跑一遍、逐字节比对结果**。

### 实测：8 个用例，native == wasm 逐字节相同

探针 `backend/cmd/wasmprobe/main.go`，样本选 `internal/email/rules`
（实测 **293 行**非测试，import 只有 `regexp/strings/sort/json/time/bytes/fmt`，
**零 net**）。它就是需求 2（垃圾清理）的规则引擎。

```
cd backend
GOOS=js GOARCH=wasm go build -o cmd/wasmprobe/rulesprobe.wasm ./cmd/wasmprobe/
node C:\tools\go\lib\wasm\wasm_exec_node.js cmd/wasmprobe/rulesprobe.wasm
```

| | |
|---|---|
| 原生 `windows/amd64` | 2247 字节 JSON |
| `js/wasm` + Node v22.23.2 | 2247 字节 JSON |
| 比对结果 | **IDENTICAL** |

覆盖的 8 个用例刻意跨越了每种形态：黑名单命中、主题关键词、
`label-category` 带副参数、`route-folder` 带 folder、importance 阈值、
**旧版 `{"blacklist":[...]}` 格式**、不命中（`actions: null`）、`SupportedActions`。

**结论**：纯计算那部分搬进 WebView 并**保持行为完全一致**是成立的。
A 路线第一阶段不是空中楼阁。

### 但代价是 4.85MB，而且 strip 压不动

```
未 strip      5,086,065 字节（4.85 MB）
-s -w strip   4,997,442 字节（4.77 MB）   只省 1.7%
```

strip 后重跑，结果仍 IDENTICAL。**体积几乎全是 Go runtime，不是你的代码** ——
293 行规则撑不起 4.8MB，wasm 把 runtime 一起带进去了。

所以 A 路线的成本模型要改写成两笔账：

- **代码账**（§7cb 算的）：必须 Java 化 ≈1700 行，可复用 ≈9800 行
- **包体账**（本节新增）：**+4.85MB**，且**只付一次**（所有纯逻辑编进
  **同一个** wasm 二进制，不是每个模块一份 runtime）

对移动端 APK 来说 4.85MB 不是小数目，但也不是否决项 ——
仓库里已有 `sql-wasm.wasm` 的先例，运行时可用。这笔账应当进决策，不该被藏起来。

### 更正 §7cb 里的三个数字（凭印象写的，实测不符）

| §7cb 写的 | 实测 |
|---|---|
| 垃圾规则 1599 行 | `junk.go` 149 + `rules/engine.go` 293 = **442** |
| PDF 网格 670 行 | `export_pdf.go` = **245** |
| 分类飞书 323 行（算在 email 包里） | 它在**独立包** `internal/feishu` = **770 行**（3 文件），根本不在 email 包的 40 文件内 |

email 包非测试的真实分布（前 12）：

```
store.go 2077   fetcher.go 1070   pipeline.go 975   scheduler.go 824
pop3_fetcher.go 661   invoice_harvest.go 635   mime.go 583   invoice.go 356
invoice_store.go 335   xmlinvoice.go 271   export_pdf.go 245   oauth_callback.go 244
```

「≈1700 行 socket 面 / ≈9800 行可复用」这两个数**仍然成立** ——
它们是由 socket 触点 grep 出来的，不是印象。而上面那三个具体行数是拍的。

**这是同一个毛病的第三次**（前两次：`InvoiceContentHash`、`refreshOnce` 的职责）。
区别在于这次错的是**行数**不是职责，但成因一样：先写数字再找证据。
纪律不变：**任何出现在方案里的数字，都要能指到一次实际量测。**

### 副产品：规则 JSON 的两种形态很容易写错

探针自己踩了两次，两次都是「格式猜错」而非逻辑错：

1. 我写 `[{"type":...,"values":[...]}]` → `json: cannot unmarshal array into
   Go value of type map[string]jsontext.Value`。真实形态是
   **对象** `{"rules":[{"type","pattern","actions"}]}`，字段叫 `pattern` 不叫 `values`
2. 我写 `{"action":"label-category","category":"work"}` → `action[0]: missing name`。
   对象形式的键是 **`name`**（`actionSpec.Name` 的 tag）

第 1 条的错误信息不指向真实原因（用户配置写错时也会看到这条），
第 2 条倒是够清楚。写进这里是因为：**A 路线迁移时这套 JSON 形态要跨语言复刻**，
形态含糊会成为迁移期的 bug 来源。

### 仍然没测的

- **wasm 堆 ↔ SQLite 的数据 marshalling 成本**（§7cb 提过，本节也没测）
- 其余 9800 行纯逻辑**整体**编进一个 wasm 的体积（只测了 293 行的样本，
  5MB 里 4.8MB 是 runtime，所以增量应该很小 —— 但这是推断，不是实测）
- 在真机 WebView 里跑（Node ≠ Android WebView，`wasm_exec.js` 的宿主 API 有差异）

---

## §7cd 把「wasm 那一半」测到底，并推翻我自己的一个结论（2026-10-02）

§7cc 留了两个口子：体积增量是**推断**没实测；「死代码被链接器剔除」只有间接证据。
这一节把两件事都测了，第二件还**推翻了我自己**。

### 先修一个我自己造的坏判据

第一版把两套探针塞进**同一个** `cmd/wasmprobe/main.go`，用 `-mode` 切换，
测出来：

```
mode=rules  8.78 MB
mode=email  8.78 MB      净增 0.00 MB
```

第一反应是「漂亮 —— 死代码确实被剔除了」。**这是错的。**
`main.go` 顶层同时 `import` 了 `internal/email` 和 `rules`，而 `probeEmail()`
在 switch 分支里可达，所以**无论 `-mode` 传什么，两个包的代码都进产物**。
两个 mode 是**同一个二进制**，差值恒 0，比对毫无意义。

教训与「判据要能失败」同源：**当两个样本的差值恰好是一个可疑的整齐值（0.00）时，
先怀疑测量装置，别急着得出漂亮结论。** 拆成三个独立 `main` 才有区分度。

### 三个独立产物的真实数字

```
cmd/wasmbaseline      空 main，只 fmt.Println      2.39 MB   ← Go runtime 底线
cmd/wasmprobe         rules 引擎（293 行）          4.85 MB
cmd/wasmprobe/email   email 包的纯逻辑              8.69 MB
cmd/wasmprobe/email-fetcher  额外调 email.NewFetcher  8.56 MB
```

三个有输出的探针，native 与 wasm **全部逐字节 IDENTICAL**
（rules 2131 chars / email 1699 chars / email-fetcher 84 chars）。

2.39 MB 的 runtime 底线是 §7cc 里那 4.85 MB 无法解释的部分 ——
它对应 `regexp` + `encoding/json` + `time` + reflect 等 stdlib 与 runtime 本身，
293 行规则代码几乎不占地方。

### 反向对照推翻了我自己的结论

`email-fetcher` 探针只做一件事：调 `email.NewFetcher(nil, nil)`。
它**不建连**（真正的 socket 在 `imapDialWithTimeout` 里），但足以把
`fetcher.go` 及其 `net` / `crypto/tls` / `go-imap` / `go-sasl`
**整个依赖闭包**拉进编译单元。

结果：**8.56 MB，比纯逻辑版的 8.69 MB 还小一点**（差值 0.12 MB 属探针调用面
不同的噪声范围）。也就是说 ——

> §7cd 一度写的「搬纯逻辑比搬全包省 3.84 MB」**是误导性结论**。
> 不是省了，是那些 socket 代码在 js/wasm 下**本来就不占地方**：
> §7cb 那个 `ENOSYS` 空壳意味着 `net`/`tls` 在 wasm 目标下几乎不产生代码。

所以对 A 路线的实际含义是：**包体不是决策依据**。
「搬纯逻辑」和「连协议层一起搬」在体积上等价，
真正的分界只有一条 —— wasm 里的 socket 一调用就是 `ENOSYS`。

这条比「省了 3.84MB」更有用：它把决策从「要不要为体积做取舍」变成
「哪部分必须用 Java」，而后者的答案是明确的（§7cb 那 3 个文件）。

### 附带的两个「我以为验证了其实没有」

- **探针 JSON 检查写死 `"name"` 字段** → `email-fetcher` 输出的是 `"probe"`，
  脚本判它「produced no JSON」而失败。判据应该问「输出像不像 JSON」
  （首字符是 `[` 或 `{`），而不是问「有没有某个特定字段」。
- **`go build` 不接受运行时 flag** → `go build -o x ./cmd/ -mode rules` 会把
  `-mode` 当包路径，报 `malformed import path "-mode": leading dash`。
  它是运行时 flag，得由 `wasm_exec_node.js` 通过 `go.argv` 传进 wasm 内部。

### 本节仍然没测

- **wasm 堆 ↔ SQLite 的数据 marshalling 成本**（§7cb、§7cc 都提了，仍未测）
- **真机 Android WebView**（本轮只在 Node 宿主验证；
  `wasm_exec.js` 的宿主 API 在 WebView 里不同）
- Java 侧重写 IMAP/POP3 的实际工作量（只量了 socket 面的行数，没估工时）

---

## §7ce 试图测「wasm 边界成本」—— **没测出来，如实记录**（2026-10-02）

这是 §7cb/§7cc/§7cd 反复提到的最后一个未知数：wasm 堆 ↔ 宿主数据结构的传递成本。
**结论是没测出来**，所以本节记录的是问题、已排除的原因、以及仍未定位的那个。

### 问题本身有效

§7cd 证明了「纯逻辑编到 wasm 后与原生行为完全一致」，但那只覆盖**计算**。
设备端 SQLite 在 JS 侧（仓库里已有 `frontend/public/assets/sql-wasm.wasm`
= sql.js 的先例），wasm 只做纯函数，于是**每封邮件都要穿过 wasm 边界**。

关键点：**AI 分类的 HTTP 不能在 wasm 里发**（js/wasm 没有 `fetch`，`net` 也不可用），
必须由 JS 侧发起。于是真实架构是：

```
JS(sql.js 取行) → wasm(判是否要分类) → JS(fetch LLM) → wasm(解析结果)
```

**每封邮件至少两次跨界**。bulk 形态（一次 JSON 串过边界）在这个架构里
根本不会出现 —— 它假设数据一次性进出，中途要调外部服务时这个假设就破了。
拿 bulk 的数字论证 A 路线会**系统性低估**边界成本。

所以要测的是三种形态：bulk（1 次跨界）、struct（N×M 次）、
permail（2N 次，**这个才是真实模型**）。

### 已排除的四个原因

调试过程中连续踩了四个坑，每一个的症状都长得像「wasm 没跑」：

1. **`require('wasm_exec_node.js')`** —— 它是**立即执行**的命令行包装器，
   模块被 require 的瞬间就 `WebAssembly.instantiate(fs.readFileSync(process.argv[2]))`。
   driver 的 argv[2] 指向的是它自己，于是**读它的源码当 wasm**，
   报 `expected magic word 00 61 73 6d, found 2f 2f 20 43`（`// C` 版权注释）。
2. **`go.exit = process.exit`** —— Go runtime 在 main 返回后调它，
   直接结束 Node 进程，driver 后续代码根本执行不到（stdout 空）。
3. **`readFileSync(0, 'utf8')` 读管道会截断** —— 实测 19036 字节的 payload
   只读到 1919 字符。后果是 wasm 侧 `json.Unmarshal` 失败 → 提前 `return` →
   stdout 全空，父进程报 `Unexpected end of JSON input`，**看起来像 wasm 没跑**。
4. **`js.ValueOf(int64)` panic** —— 我为了调试把 `map[string]any` 挂到
   `globalThis`，wasm 侧直接 `panic: ValueOf: invalid value`。

### 仍未定位的那一个 —— **已定位，见 §7cv**

`scripts/marshal-driver.mjs` 最终状态：**退出码 0、stderr 全空、stdout 0 字节**。

~~js/wasm 的 `os.Stdout` 在 `wasm_exec.js` 里被接到 **`console.log`**~~
~~（`fs.writeSync` polyfill 的实现是 `console.log(outputBuf.substring(0, nl))`）。~~
~~Node 的 `console.log` 对 pipe 是**异步**的，我据此判断「进程退出时缓冲没刷完」，~~
~~改成劫持 `console.log` 并用 `fs.writeSync(1, ...)` 同步转发 —— **现象不变**。~~

**这个推断是错的，而且方向就错了**：问题从来不在 stdout 捕获。
driver 原封不动就是好的 —— 真正的失败在 wasm 侧，而且是**静默**的。
根因、修法、两条负控与实测数字全部在 §7cv。

### 代码保留的意义

`backend/cmd/wasmprobe/marshal/` 这套代码本身是好的，已实测：

- `go run ./cmd/wasmprobe/marshal/` → 正常输出 JSON
- `go run ./cmd/wasmprobe/marshal/ -emit-corpus` → 正常输出 120 封语料
- `GOOS=js GOARCH=wasm go build` → 正常产出 wasm
- `go vet` → 干净

native 基线与三种 wasm 形态的 wasm 侧实现都已就位，驱动一旦修好即可直接产出数据。
而且上面那四个坑的结论本身就是可复用的知识。

### 一个副产品：减法在这里根本不成立

`go run` 的 native 基线输出 `elapsed_ns: 0` —— `classifyAll` 只做字符串归一化，
120 封装不满 1ms，计时器分辨率下就是 0。

第一版的判据是「wasm 总耗时 − native 总耗时」，**一旦计算耗时落到分辨率以下，
减法就变成 0 − 0，边界成本被彻底抹掉**。现在 wasm 侧改成 read / calc / write
三段各自 `time.Now()` 直接测绝对值，这是正确口径。**数字见 §7cv。**

---

## §7cv 【§7ce 收尾】driver 一直是对的，错的是 wasm 侧一个 JS 语义坑——边界成本数字终于拿到了（2026-10-02）

§7ce 留下的「最后一个仍未定位的原因」已定位。**driver 的 stdout 捕获从头到尾
都是好的**，四个被排除的假设方向也全错。

### 复现与定位

原封不动跑 `node scripts/marshal-driver.mjs <goroot> <wasm> bulk`，**一次就成功**
（stdout 269B、exit 0、合法 JSON）。于是问题必然在别处。逐个变量隔离：

| 试的东西 | 结果 |
|---|---|
| driver 直接跑（stdin 用文件重定向） | OK 269B |
| `execFileSync` + `input`（探针真正用的方式） | OK 269B |
| CWD 换到 worktree | OK 269B |
| 先跑 native `go run` 再跑 driver | OK 269B |
| **`mode=struct`** | **OK 但 `count=1`**（语料是 120） |
| **`mode=permail`** | **exit 0 / stdout 0B / stderr 空** ← 原症状 |

前四项排除了「stdin 类型 / 管道截断 / CWD / 执行顺序」，
**问题只可能出在模式分支上**。这就是 §7ce 缺的那一步：
当时只试过 bulk，而 bulk 恰好是三条分支里唯一没坏的。

### 根因：`new Array(x)` 在 x 非数字时不是拷贝

`main_wasm.go` 的 struct 与 permail 两条分支都写了：

```go
arr := js.Global().Get("Array").New(rows)   // 等价于 new Array(rows)
```

JS 语义：`new Array(x)` 在 `x` **不是数字**时，创建的是**长度为 1、唯一元素就是
`x` 本身**的数组。于是：

- `Length()` 恒为 **1**；
- `Index(0)` 拿到的是**整个 rows 数组**，不是第一行；
- 对它 `.String()` 得到 syscall/js 给 object 的字面量 `"<object Value>"`
  → `json.Unmarshal` 报 `invalid character '<' looking for beginning of value`。

两条分支的表现不同，但同源：

- **struct**：`row.Get("from")` 在数组上取不到 → `js.TypeUndefined.String()`
  返回 `"undefined"` → 造出 1 条全字段为 `"undefined"` 的假数据 →
  **count=1，且 digest 自洽**（它只含 1 条，所以也"一致"）。
- **permail**：`json.Unmarshal` 直接失败 → `Set("marshalError")` + **裸 `return`**，
  return 之前一个字节都没打 → 外部看到「exit 0、stderr 空、stdout 0 字节」。

**关键教训：struct 那条是「假绿」。** 探针本来就有「四条路径结果摘要一致」的
校验，而它**照样通过** —— 因为一致性校验证明的是「各路径算法相同」，
**不是「处理了正确数量的数据」**。这与 §7cr（护栏被注释骗过）、
§7cs（`InvoiceContentHash` 全树无调用者）是同一族：判据问错了问题。

修法：`js.Value` 本身就有 `Length()` / `Index()`，直接用，不必包一层。
（`main_wasm.go:183` 的 `New(len(out))` 传的是数字，**本来就是对的**，没动。）

### 两条负控（都实测转红）

1. **撤回 struct 修复** → `[marshal] mode=struct: count=1, want 120`，exit 1。
   命中的是本轮**新加**的条数断言，不是碰巧挂在别处。
2. **撤回 permail 修复** → `wasm reported marshalError: invalid character '<'`，exit 1。

### 顺带修掉的可观测性缺口

wasm 侧所有失败路径都是 `Set("marshalError") + return`，**不写 stderr、不写
stdout、退出码还是 0**。driver 现在在 `go.run()` resolve 之后读
`globalThis.marshalError` 并转储（实测此时仍可读 —— Node 侧读 globalThis
不需要 wasm 还活着）。判据取「有值就报错」而不是「stdout 为空才报错」，
后者在 bulk 正常时也会被 struct/permail 的空输出触发，噪声太大。

探针侧另加两处：`once()` 的 JSON 解析失败带上 `mode`（原来裸
`Unexpected end of JSON input` 完全看不出是哪个形态），以及条数必须等于
语料条数的断言。

### 实测数字（120 封语料 / 7 轮取最小值 / 两次独立运行）

```
  形态            读(JS→wasm)    算(纯计算)  写(wasm→JS)          合计   边界占比
  wasm-bulk     读   14.085  算    2.305  写    6.410   合计   22.799 ms   边界占比   89.9%
  wasm-struct   读    8.605  算    1.298  写    6.141   合计   16.044 ms   边界占比   91.9%
  wasm-permail  读   13.843  算    0.781  写    7.314   合计   21.938 ms   边界占比   96.4%   每封边界 0.1763 ms
```

第二轮：bulk 19.859 / struct 16.646 / permail 22.712 ms，每封边界 0.1826 ms。
两次抖动 ≤ 4%，四条路径（native + 三形态）result_digest 完全一致。

**能说的**（对齐 §7ce 的问题，不外推）：

- A 路线的真实模型（permail）在 120 封规模下，**跨界成本 ≈ 0.18 ms/封**，
  合计 22.7 ms，**边界占总耗时 96.5%** —— 纯计算只占 3.5%。
- bulk 形态反而**不是**最省的：19.9 ms vs permail 22.7 ms。差距主要在写回
  （bulk 4.4~6.4ms vs permail 7.3~7.5ms），因为 permail 每封都要单独
  `Set("marshalScratch")` 回写一次。**所以「bulk 最优」这个直觉是错的**，
  而它正是 §7ce 论证时默认的前提。
- struct 形态**最快**（16.0~16.6 ms），读回最省（8.6ms vs 13~14ms）——
  逐字段 `syscall/js` 属性访问比整串 JSON 反序列化便宜。

**不能说的**：这些数字是 **Node 宿主**上量的。真机 Android WebView 的
JS 引擎、sql.js 取行成本、以及真实邮件字段比语料更长的情况都没测（见下）。

### 本节仍未测

- 真机 Android WebView 跑同一探针（`wasm_exec.js` 的宿主 API 在 WebView 里不同）
- sql.js 取行 → wasm 的那一段（现在测的是「已有 JS 值 → wasm」）
- 语料是合成数据（8 种主题轮转），真实邮件的 snippet/HTML 长度分布未采样

---

## §7cw 【gates 最后红点】`graphic_eq` 缺字形：已提交的字体子集比源码旧 8.5 小时（2026-10-02）

`check:icons` 是 `gates` 七步里最后一个红的。修完这条，整条门禁**首跑 exit=0**。

### 现象与两个判据为什么打架

```
[icons]  ✅ 所有声明式图标都在字体子集内          ← check-icon-subset.mjs 绿
[icon-font] ❌ 1 个名字在字体里合不出连字：graphic_eq  ← check-icon-font.mjs 红
```

两个脚本问的不是同一个问题，这不是矛盾：

- `check-icon-subset.mjs` 比对**名字集合**（扫源码收集的名字 vs FALLBACK），
  **从没打开过字体**，所以字体陈旧它照样绿。
- `check-icon-font.mjs` 打开**已提交的 woff2**，用 harfbuzz 对每个名字做真实
  连字成形。它自带正反对照（`light_mode` 必须合 / `zzzznotanicon` 必须不合），
  判据是「`名字 + 空格` 成形后恰好 2 个字形」。

即 `graphic_eq` 在**上游完整字体**里是能合字的（所以被判为真图标名，不是误报），
只是**不在我们裁出来的子集里**。

### 因果链（git 实测，不是推测）

```
82233060  2026-09-30 20:07:10  feat(learning,task) ...   ← 字体子集最后一次重建
2340f3fd  2026-10-01 04:36:37  recover(stt): 恢复 STT 文件并补回四处前端接入
                                                ↑ graphic_eq 在这里引入
git merge-base --is-ancestor 2340f3fd 82233060 → exit 1（非祖先）
```

STT 功能在 `SettingsView.vue:60` 加了
`<span class="material-symbols-outlined">graphic_eq</span>`，
比字体最后一次重建**晚 8.5 小时**，而产物没跟着重建。门禁从那一刻起就一直红。

### 验证走的是非破坏性路径

`build-material-symbols-subset.mjs` 提供 `POCKET_ICON_FONT_OUT`、
`check-icon-font.mjs` 提供 `POCKET_ICON_FONT` —— 先重建到临时文件、
拿门禁测新字体，绿了才覆盖正式产物（避免每次试验都去动那个 3.5 MB 的已跟踪文件）。

先确认采集规则收得到它（`--list`，脚本注释说这是排查该类问题的唯一可靠入口）：

```
[subset] 工程用到 + 兜底共 138 个图标
graphic_eq          ← 在清单里
```

所以不是采集规则漏扫，**纯粹是产物没重建**。重建到临时文件后门禁绿，
再落到 `src/assets/fonts/material-symbols-outlined.woff2`
（3614020 字节 / 3.45 MB，比旧产物略小），`check:icons` exit=0。

### 整条 gates 首跑全绿

```
test:email   # pass 242  # fail 0        ← 在门禁里真实执行
check:vm-gaps ✅ 命中 0
check:i18n   ✅ 9 份语言文件 key 齐平
check:icons  ✅ 全部图标名在字体里都能合成连字
GATES exit=0
```

这一条的意义超出设置页：**`test:email` 从此在门禁里真的有保护力**。
在此之前 `gates` 因 `typecheck` 短路，242 个邮件用例根本没被执行过
（§7bt 解决 `recording-voice-prompt` 缺失后才有第一次完整执行，
本节是补上最后一个红点）。

### 一条可复用的教训

「同名门禁一个绿一个红」时，**先确认两个判据分别问的是什么**，
别急着认定其中一个坏了。这次是名字清单绿、字体实测红，
差值本身就是答案（清单里有、字体里没有 = 产物陈旧），
不需要猜、不需要改采集规则、也不需要改判据。

---

## §7cf 【真 bug】每日摘要的时区参数是**完全无效**的（2026-10-02）

需求 4「对其它重要邮件进行提醒」的产出链路此前 0% 覆盖。补测试时撞上一个
**真缺陷**：`ListEmailsByDayScoped` / `ListEmailsByDay` 的 `tzOffsetSec` 传什么都没用。

### 缺陷

```go
t, _ := time.Parse("2006-01-02", date)   // -> UTC 午夜
loc := time.FixedZone("user", tzOffsetSec)
t = t.In(loc)                            // 只改 Location 字段
startUnix := t.Unix()                    // 恒等于该日期的 UTC 午夜
```

`time.Time.In()` 只改变**显示用**的 Location，**底层时刻（Unix 值）不变**。
所以日界恒为 UTC 午夜。正确写法是 `time.ParseInLocation`——直接在目标时区
把 `"2026-10-02"` 解释成该时区的午夜。

`store.go` 的 `ListEmailsByDay`（705 行）与 `ListEmailsByDayScoped`（2021 行）
是**同款缺陷的两份拷贝**。唯一生产调用方是 `scheduler.go:798 summarizeUser`。

### 影响：需求 4 的提醒窗口错 8 小时

东八区（+8）用户认为的「今天 00:00」实际是 UTC 前一天 16:00。每天
**00:00–08:00 之间**触发的每日摘要，取到的是「当地昨天 08:00 到今天 08:00」
的邮件，而不是用户认知里的「今天」。邮件归属日整体偏一天。

### 实测证据（负控先红后绿）

修复前，断言写在「按正确语义应该包含的那封邮件」上，运行即红：

```
--- FAIL: TestListEmailsByDayScoped_HonorsTZOffset
    an email at local 2026-10-02T01:00:00+08:00 (= 2026-10-01T17:00:00Z)
    is missing from local day 2026-10-02 (tzOffsetSec=28800); got 2 rows
```

修复后 11 个新用例全绿。**双向负控**：把 `ParseInLocation` 反退回
`time.Parse + .In`，两条断言同时转红，且暴露出缺陷的**第二个**表现——
日期文本 round-trip 变成 `2026-10-01`（`west-5: round-tripped to 2026-10-01`）：

```
--- FAIL: TestListEmailsByDayScoped_HonorsTZOffset
--- FAIL: TestParseDayStart
    east+8: start.Unix() = 1790899200, want 1790870400
    west-5: start.Unix() = 1790899200, want 1790917200
    west-5: round-tripped to 2026-10-01, want 2026-10-02
    tzOffsetSec had no effect: both returned unix 1790899200
```

`tzOffsetSec had no effect` 这条是死穴断言：缺陷版本对任何时区都返回同一个
UTC 午夜值，不依赖任何具体数字。

### 顺带修掉一个我自己写的假失败隐患

新测试里三处原本用 `time.Now().Unix()` 当邮件时间戳。但 `time.Now().Format(...)`
给的是**本地**日期，而查库以 UTC 午夜为界——东八区本地 00:00–08:00 期间
本地日期比 UTC 快一天，此刻的 Unix 值落在日界**之前**，用例会在这 8 小时里
假失败。改为取该日**正午**（`atNoon` 辅助函数），两种口径都在窗内。

### 数字自查

`TestParseDayStart` 里的基准值 `1790899200`（= 2026-10-02T00:00Z）第一版我
凭印象写成 `1790918400`，**写完立刻用 PowerShell 独立算了一遍**才发现差
57600 秒。现值与失败日志里的 `1790913600`（= UTC 04:00，即本地 12:00）
交叉验证一致。

### 回归

- `go build ./...` exit 0 / `go vet ./...` exit 0
- `go test ./internal/email/` **76.3s 通过**
- `go test -race ./internal/email/` **66.7s 通过，0 DATA RACE**
  （gcc 取 `C:\tools\w64devkit\w64devkit\bin`）
- 注意：这些用例需要 `POCKET_TEST_POSTGRES_DSN`，否则 `newWorkspaceTestStore`
  会 `t.Skip` —— **DSN 缺失时是「跳过」不是「失败」**，别把 skip 读成绿。

---

## §7cg 补编排层覆盖率，一开就抓到**两个**真缺陷（2026-10-02）

§7cf 证明了「补覆盖率能抓出真 bug」，于是这次先**量**再动：
`go test -covermode=count` 出 `cov.email.out`，`internal/email` 总覆盖 **53.5%**。

### 先说我自己造的坏测量工具

`scripts/cov0.mjs` 第一版把 profile 行解析成 `[NaN]`
（末尾两个独立字段被我当成「一个空格包起来的串」），
于是 `count > 0` 恒 false —— 输出「3325 个函数全部 0 覆盖」。
而全包明明是 53.5%。

**这个输出看起来还挺合理**，没去核对总数就差点据此去补「最优先的 0% 区域」。
第二版加了自检（归并后语句总数必须等于原始总数）才敢用。

## 缺口定位

`pipeline.go` 的 `Run` 主干 5 个 `stepStart` 区块
（**298 / 302 / 306 / 310 / 314**）全部 0 覆盖 ——
「收信 → 清垃圾 → 提醒 → 采发票 → 推飞书」这条主链路
**从未被任何测试整体跑过**，之前每次只测了单步的纯函数。
单步纯函数绿 ≠ 编排正确。

## 缺陷一：`Fetcher.Sync` 缺 nil 接收者守卫 → 进程崩溃

测试一跑就 panic：

    panic: runtime error: invalid memory address or nil pointer dereference
    email.(*Fetcher).Sync(...) fetcher.go:583
    email.(*Pipeline).syncAccounts.func1.2() pipeline.go:140

`Sync` 里**已经有** `if f.store == nil` 的同类检查，却漏了接收者本身
（`f.syncHook` 解引用 nil 指针）。同包的 `FetchMessageRaw` 反而有
`if f == nil || ...` —— 守卫漏在哪不是随机的。

为什么这是**进程级**故障：`syncAccounts` 把 `p.Fetcher.Sync` 放在
**独立 goroutine** 里调（pipeline.go:139-142），goroutine 内 panic
直接崩掉整个进程，主流程的 `defer recover` 拦不住。

场景不是纯理论：需求 6 走设备本地执行时，客户端推送路径本来就不建 Fetcher。

## 缺陷二：`Run` 把汇总文档路径丢进 `_`

    if _, _, err := p.BuildInvoiceSummaryDocs(ctx, sc[0], sc[1]); err != nil {

文件**生成了**，路径却被丢弃 —— `PipelineReport.ShareDocCSV` /
`ShareDocMD` 恒为空（这两个字段和 JSON 标签都在，就是没人填）。

**为什么一直没人发现**：手动触发走的是另一条路 ——
`server_email_pipeline.go:497` 单独调一次并回填到 HTTP 响应。
所以**只有定时这一条路径受影响**。结果是需求 3 明确要的
「共享文档 + 列表 + 金额汇总」在无人值守场景下等于没交付：
文件在磁盘上，但日报里没有任何字段告诉你在哪。

多 scope 时这两个字段是**单值**，只能指一个 scope（最后一轮赢，
与 `ShareDocURL` 既有行为一致）；每个 scope 的文件都仍会落盘。

## 双向负控

两处修复各自回退后：

| 回退 | 转红断言数 |
|---|---|
| `if f == nil` 改成 `return 0, nil` | 2 个用例（含 `AccountsSynced = 1, want 0` —— nil 守卫去掉后 go-imap 路径被走进去了） |
| 回填改成 `else if false` | 5 个用例（全部是 `summary docs missing` / `md path empty`） |

失败信息都精确指向被破坏的行为，不是笼统的红。

## 附带测出的需求 4 核心判定

`notifyImportant` 此前**完全没测**。新增用例钉住：
只有 `importance='high'` 才提醒（medium/low 不提醒）；
提醒过的不重复提醒（否则每轮轰炸）；提醒**失败不得**标记已通知
（否则这封重要邮件被永久漏掉）；`RemindersUnclassified` 正确统计
未分类邮件 —— 这个计数是需求 4 能否排查的关键，
kxmemory 没配时它会告诉你「这批邮件根本没被分类过」，
而不是让你对着恒为 0 的 `RemindersSent` 猜。

## 我自己写错的两处断言（不是产品 bug）

1. 一开始断言 `RemindersScanned > 0` 证明第 3 步跑了。
   实际 `Notifier == nil` 时**按设计**整体早退（pipeline.go:788）。
   改成断言「早退而非 panic」，第 3 步的真正判定另立用例。
2. fixture 里没注入 Fetcher 却期望 `AccountsSynced == 1`。
   该字段只在 `r.err == nil` 时递增。改用 `Fetcher.syncHook` 注入
   假实现 —— **用钩子而不是改断言**：让 Fetcher 报错去断言「同步失败」
   测的是降级路径，不是这里要的「五步编排正确」。

## 回归

go build ./... = 0，go vet ./... = 0
go test ./internal/email/ 52.6s 通过，go test -race 63.6s 通过（0 DATA RACE）

### 覆盖率增量（重测，不是估算）

| 指标 | 改动前 | 改动后 |
|---|---:|---:|
| 总覆盖率 | 53.5% | **57.2%** |
| 总语句数 | 5123 | 5128 |
| 0 覆盖语句 | 2383 | **2194**（-189） |
| 0 覆盖函数 | 1659 | 1573 |

`pipeline.go` 原先最大的 5 个 9 语句 0% 区块（298/302/306/310/314）
在改动后**全部消失**，剩余最大 0% 区块只有 4 语句。

新增 `scripts/cov0.mjs`（带自检的覆盖率提取器）。

---

## §7ch 覆盖率定位到需求 1 的不可逆路径：Greenmail 从未执行（2026-10-02）

继续按 §7cg 的方法量剩余缺口（`cov.after.out`，逐文件聚合 0 覆盖语句）：

| 文件 | 0 覆盖语句 | 与需求的关系 |
|---|---:|---|
| store.go | 420 | 多为迁移/低频路径 |
| pop3_fetcher.go | 306 | 163 邮箱路径 |
| fetcher.go | 240 | 含真实 IMAP 协议面 |
| **junk.go** | **69** | **需求 1「移到垃圾邮件箱」——不可逆** |
| scheduler.go | 163 | 需求 4 触发 |

`junk.go` 69 条 0% 优先级最高：它是**唯一不可逆**的操作
（IMAP MOVE 一旦发出，邮件就离开收件箱）。

## 根因：两套 DSN 变量名并存，两个方向都报 ok

查 `junk.go` 零覆盖的原因，发现三个文件各自硬编码 `os.Getenv("PG_DSN")`：
`fetcher_greenmail_test.go` / `junk_greenmail_test.go` / `realprobe_test.go`，
而本包其余二十多处走 `testDSN()`（认 `POCKET_TEST_POSTGRES_DSN`）。

后果是双向的，而且**都报 ok**：

1. 只设标准变量 → 这三处 `t.Skip`。需求 1 那条不可逆链路在 CI 与本地
   **从未执行**，报告是绿的。
2. 设 `PG_DSN` → 它们真的连上去，而本仓库惯例是同一个 DSN 既喂服务也喂测试
   ⇒ `PG_DSN` 极可能就是**生产 schema**（这正是 `b3c057c` 修过的那类污染）。

## 处置：统一入口 + 护栏

新增 `greenmailDSN()`（`pgscope_test.go`），**只认 `POCKET_TEST_POSTGRES_DSN`**，
刻意**不**回退 `POCKET_POSTGRES_DSN` —— 这几个用例会写库（建账户、插邮件、
标 spam），让「忘了设测试变量」的后果是 skip 而不是连上生产库。

`realprobe_test.go` **保持读 `PG_DSN` 并显式豁免**：它是 `-tags=realprobe`
手动启用的**只读**探针，刻意连生产 schema（§7by 那 120 封真实邮件就在那里），
且另有 `POCKET_REAL_KEYS` 第二道门控。改它才是错的。

## 护栏的判据为什么是 AST 而不是正则

`pgisolation_guard_test.go` 挡的是**值被读**（`os.Getenv("PG_DSN")`），
不是某种写法。用 AST 是因为正则有两个已知的绕过面，换行/多空格就能躲开。

### 四种负控形态，逐个实测

| 注入形态 | 期望 | 实测 |
|---|---|---|
| 直接 `os.Getenv("PG_DSN")` | 红 | **红** |
| `os.Getenv(\n "PG_DSN",\n)` 换行+多空格 | 红 | **红** |
| 只写进注释（行注释 + 块注释） | **不红** | **不红** |
| `os.Getenv(\`PG_DSN\`)` 原始字符串 | 红 | 未单独跑（AST 的 `lit.Value` 两种引号同值，随第 1 条一并覆盖） |

「只写进注释必须不红」这一条是刻意设计的：我前几轮栽过「注释能满足任何
源码扫描断言」的坑（见 agent memory「变体三」）。所以这里不仅负控它、
还把 `stripGoComments` 本身单测了 6 个形态（行/块注释、字符串、原始字符串、
rune 字面量、`http://` 不误伤）。

护栏另有防空跑断言：`checked == 0` 直接 fail，并 `t.Logf` 报扫了几个文件
（实测 136 个）。

## build tag 掩盖的一个编译错误

改完 `-tags=greenmail` 才暴露：`fetcher_greenmail_test.go` 与
`junk_greenmail_test.go` 的 `os` 导入变成 unused。
**`go vet ./...` 默认不带 tag，编译不到这两个文件**，所以第一轮
`VET=0` 是假的。`go vet -tags=greenmail` 与 `-tags=realprobe` 各跑一遍才干净。

## 本机跑不了 Greenmail（诚实记录）

`docker version` 报 `open //./pipe/docker_engine: The system cannot find the
file specified` —— **Docker daemon 未运行**。所以需求 1 的真实 IMAP MOVE
链路在本机**依然未验证**，本节只修好了「让它能跑起来」的前置条件。
真实邮箱上跑 MOVE 仍需单独授权（不可逆）。

## 回归

go build ./... = 0
go vet ./... = 0；**go vet -tags=greenmail = 0；-tags=realprobe = 0**
go test ./internal/email/ 通过，-race 通过（0 DATA RACE）

---

## §7ci 绕开 Docker：用进程内 IMAP 服务器验证需求 1 的不可逆 MOVE（2026-10-02）

§7ch 定位到 `junk.go` 69 条 0%，并修好了「让它能跑起来」的 DSN 前置条件，
但 Docker daemon 未运行 ⇒ 需求 1 这条**唯一不可逆**的操作仍未执行。
本节把这条路在不依赖任何外部进程的前提下走通。

## 做法

`Fetcher.dialTLS`（`fetcher.go:30`）本来就是为本地 IMAP server 留的测试缝。
于是用 `net.Listen("127.0.0.1:0")` 在**进程内**起一个最小 IMAP 服务器，
走真实 TCP + 真实 go-imap 客户端，只实现 `junk.go` 真正调用到的命令：
CAPABILITY / LOGIN / LIST / SELECT / CREATE / UID MOVE / UID COPY / UID STORE。

**故意不实现删除原邮件的能力** —— 一旦代码路径偏离预期，测试会红，
而不是静悄悄通过。

## 关键：断言「服务器收到了什么」，不是「调用没报错」

MOVE 一旦发出，邮件就离开收件箱。`moved == 3` 只能证明循环跑完了。
服务器侧**记录每一条收到的命令**，据此断言：

1. 服务器确实收到了 `UID MOVE`；
2. 目标是 **LIST 里发现出来的**垃圾箱，不是硬编码的 `"Junk"`；
3. 三个 UID 一个不少都到了服务器；
4. **`\Seen` 没被置上**（移动不该改变邮件的已读状态）。

第 4 条是设计后才想到的：任何实现里只要混入一次 `UID STORE +FLAGS(\Seen)`，
邮件就会被标成已读 —— 那是不可逆的副作用。

## 9 个用例覆盖的分支

| 分支 | 为什么要有 |
|---|---|
| 正常：`\Junk` 属性命中 | 主路径 |
| 无 `\Junk` 属性，靠**名字**匹配 + 层级前缀 | 163/qq 都不发 `\Junk` 属性 |
| 完全没有垃圾箱 → `CREATE "Junk"` | 否则需求 1 在这些账户上永远不生效 |
| 没有垃圾箱且 `CREATE` 失败 → **必须报错** | 返回 nil error 会让调用方把「一封没移」当「移完了」 |
| 空 UID 列表 → 不建连接 | 用 `loginOK=false` 反证 |
| 账户 disabled → 拒绝 | 不可逆操作上没有这层 = 一次误配置搬空邮件 |
| UID ≤ 0 → 跳过但不拖累整批 | POP3 来源邮件没有真 UID |
| **LOGIN 失败 → MOVE 之前停住** | 认证没过就动邮件 = 搬走别人的邮件 |

## 负控

把 `MoveUIDsToJunk` 退化成「只发剥掉层级的短名」（`junkBox = baseMailboxName(junkBox)`）——
这正是把邮件送进**错误信箱**的典型写法：

    --- FAIL: TestMoveUIDsToJunk_FallsBackToNameMatching
        moved to [Junk]; must use the full hierarchical name returned by LIST

注入生效已打点（`NEGCTL=1`）。生产代码已恢复（`git diff junk.go` 为空）。

## 写这个测试时踩的三个坑（都不是产品 bug）

1. **BOM 污染中文信箱名**。我先用 PowerShell `.Replace()` 改文件，
   `Set-Content -Encoding UTF8` 写入 BOM（PS 5.1 陷阱），又压掉了换行。
   症状是 `in LIST: invalid UTF-8`。改用 `write` 工具 + 手工剥 BOM 解决。
2. **`CREATE "Junk"` 带引号**，我把判据写成 `HasPrefix(cmd, "CREATE Junk")` ——
   字符串前缀匹配，加了引号就永远匹配不上。改成 `sawCreate()` 解析后比较。
3. **IMAP mailbox 名是 modified UTF-7**。直接发 UTF-8 字节的信箱名会被
   客户端 `ExpectMailbox` 拒掉（同样报 `invalid UTF-8`）。该用例改用 ASCII
   名 `Other Folders/Junk` —— 这里要验的是**层级剥离**，不是编码。

第 3 个坑顺带暴露了我服务器实现的一个真 bug：用 `strings.Fields` 切参数，
把 `COPY 11 "Other Folders/Junk"` 的信箱名截成了 `Folders/Junk`。
改成按引号切分（`splitIMAPArgs`）后，这条断言才真正成立 ——
**它现在顺带验证了 go-imap 确实给含空格的信箱名加了引号**。

## 回归

go build ./... = 0，go vet ./... = 0
go test ./internal/email/ 69.6s 通过，-race 72.8s 通过（0 DATA RACE）

### 覆盖率增量（重测）

| 指标 | 改动前 | 改动后 |
|---|---:|---:|
| 总覆盖率 | 57.2% | **58.5%** |
| 0 覆盖语句 | 2194 | **2127**（-67） |

`junk.go` 的 0 覆盖语句从 **69 降到 6**；剩下 6 条全是错误分支
（148 MOVE 失败、104 账户取不到、111 解密失败、40 LIST 出错、58 LIST 收尾出错），
主路径已全覆盖。

## 仍然未验证的

Greenmail（`-tags=greenmail`）那两条用例**本机依旧跑不了**，需要 Docker。
本节覆盖的是**同一段 junk.go 逻辑**，但服务器是我自己写的最小实现，
不覆盖 Greenmail 的 quirks。真实邮箱上的 MOVE 仍需单独授权（不可逆）。

---

## §7cj 进程内 POP3 服务器：覆盖 163 路径 + **收回一个我自己的错误结论**（2026-10-02）

`pop3_fetcher.go` 有 **306 条语句 0 覆盖**，是 `internal/email` 里最大的
未覆盖面，而 163 的三个账户全走这条路（需求 1/2/3 都依赖它）。

POP3 比 IMAP 好注入得多：`FetchPOP3MailboxWithIdle` 收的是包级函数
`(host string, useTLS bool, ...)`，传 `useTLS=false` + 含端口的 host
就能明文连本进程内的服务器，不需要任何注入缝。

9 个用例覆盖：只取未见过的（断言**服务器收到的 RETR 序号**）、
byte-stuffing 还原、**绝不发 DELE**、认证失败不 RETR、单封 RETR 失败不拖垮整轮、
空邮箱、连不上必须报错、空 UIDL 列表不建连、按 UIDL 精确取回。

其中两条护住**不可逆的数据损失**：

- **不得发 DELE**。DELE 会真删服务器上的邮件，而 IMAP 端不会同步删 ——
  于是「POP3 拉过 = IMAP 也丢」。
- **LOGIN 失败必须在任何 RETR 之前停住**。认证没过就动邮件。

## 【更正】我一度断言 `readPOP3Message` 有真 bug，那是错的

### 我当时怎么想的

原实现是：

    if strings.HasPrefix(line, "..") {   // 只认恰好两个点
        line = line[1:]
    }

我推断它「只处理两个点、漏掉三个及以上」⇒ 静默数据损坏，
并把它当成**本轮第三个真 bug** 写进了代码注释和测试注释。

### 负控不转红，逼我回头查

把实现改回 `HasPrefix(line, "..")` 后，**那条断言不红**。
按惯例此时该怀疑判据 —— 但我先验了「注入是否生效」（`NEGCTL=1`，生效了），
所以问题在判据或结论本身。于是写了个 10 输入的对照程序实测：

    original      wire(stuffed)   old(HasPrefix..)  new(HasPrefix.)
    "."           ".."            "."               "."
    ".."          "..."           ".."              ".."
    "..."         "...."          "..."             "..."
    "....four"    ".....four"     "....four"        "....four"
    ".trailing"   "..trailing"    ".trailing"       ".trailing"
    "normal"      "normal"        "normal"          "normal"
    ""            ""              ""                ""
    （10 例中 0 例分歧）

**结论：原实现没有 bug。** RFC 1939 §5.1 要求服务器把行首点填充成
**至少两个**（原文一个点 → 线上两个点），所以线上数据**恒**以 `..` 开头，
`HasPrefix(line, "..")` 恒成立，剥掉一个点与剥掉「恰好一个」逐例一致。

### 修正后的处置

代码保持 `HasPrefix(line, ".")`（更贴规范、对畸形单点回包更稳），
但**注释、测试注释、提交说明全部改成「这不是 bug 修复」**，并写明
「负控不转红在这里是判据正确的证据」。用例从 1/2 个点扩到 **1/2/3/4 个点**，
覆盖真正该覆盖的语义：原文的 N 个点原样还原成 N 个。

### 为什么值得单独记一节

**「负控不转红」有三种可能**：判据太松、注入没生效、**结论本来就错**。
前两种我都有对应的排查动作，**第三种只有靠「注入确认生效 + 回头质疑结论」才暴露**。
我在前两轮刚写过「负控不转红先怀疑判据」的记忆条目，这次正好是它的镜像：
怀疑完判据和注入之后，还得怀疑**被测代码本来就是对的**。

## 写这个测试时踩的一个真 bug（在我自己的服务器里）

`strings.Split(msg.raw, "\n")` 对以 `\r\n` 结尾的 body 会产生**末尾空元素**，
多发一行空行 → 客户端把下一个命令（`QUIT`）当正文读走。
症状极有欺骗性：「服务器从没收到 QUIT」，看起来像**客户端没收尾**，
而真因是服务器多发了一行。**是我第一版断言 `HasPrefix(line,"..")` 写错
导致的连锁红**，差点让我去改本来正确的生产代码。

## 回归

go vet ./... = 0，go test ./internal/email/ 54.0s 通过，-race 通过

### 我自己写出的一条**间歇失败**断言

提交后跑覆盖率时它红了，但提交时是绿的。查下来是竞态：

生产代码是 `_ = writeLine("QUIT")`（尽力而为），紧接着 `conn.Close()`。
服务器读循环与客户端关闭之间没有同步，**实测约一半的运行里服务器
来不及读到 QUIT**。我那条 `client never sent QUIT` 断言要求了
**本就不该要求的事**。

改成断言 `USER`/`PASS`/`UIDL`（真正保证会发的命令），并连跑 5 次确认稳定。

> **教训**：加断言前先问「这个行为是**保证**的吗」。
> 竞态类断言的单次绿灯没有意义 —— 它可能下一次就红，
> 而红的时候会被当成回归去查。

### 覆盖率增量（重测）

| 指标 | 改动前 | 改动后 |
|---|---:|---:|
| 总覆盖率 | 58.5% | **60.0%** |
| 0 覆盖语句 | 2127 | **2053**（-74） |

`pop3_fetcher.go` 的 0 覆盖语句从 **306 降到 12**。

---

## §7ck 把「发票重复」从待查推断变成有证据的事实（2026-10-02）

§7be 起我一直把「一封多张只记首张 / 跨邮件同发票记两行」记成**待拍板项**。
本节把它变成**可复核的证据**——因为「要不要加唯一约束」是产品决策，
而决策该建立在事实上，不建立在推断上。

## 先用真实库确认约束（不靠读代码）

`opencode_pocket.email_invoices` 的全部索引（psql 实测）：

    email_invoices_pkey        UNIQUE (id)
    email_invoices_email_id_key UNIQUE (email_id)   <- 唯一的业务约束
    idx_email_invoices_ws      (workspace_id, user_id, created_at DESC)
    idx_email_invoices_status  (workspace_id, status)

**`invoice_no` 确实没有任何唯一约束。**

`email_invoices` 当前 **0 行** ⇒ 这个重复在真实数据上**尚未发生**。

## 代码侧：同一封邮件是幂等的

`invoice_store.go:98` 的 `ON CONFLICT (email_id) DO UPDATE` 保证
**同一封邮件**重复建档只落一行，且**复用旧行 ID**（不新生成 ID，
否则旧行变孤儿——`scheduler.go:848` 的注释记着这个坑）。

## 缺口：跨邮件同一发票号会重复计入

`invoice_dedup_test.go` 用三条用例把它固定下来：

1. `TestUpsertInvoice_SameEmailIsIdempotent` —— 同邮件重复 ⇒ 1 行、ID 复用。
2. `TestUpsertInvoice_SecondPassDoesNotWipeKnownFields` —— 第二轮提取只剩
   envelope 信息时，**不得**把第一轮解析出的发票号/日期/销售方/金额抹成空串
   （`ON CONFLICT` 里那 4 个 `CASE WHEN ... <> ''` 就是干这个的）。
3. `TestInvoiceNoHasNoUniqueConstraintAcrossEmails` —— **记录当前真实行为**：

       已证实：同一发票号跨两封邮件 -> 2 行、合计 1000（应为 500）

   这条断言的是**现状**而不是「应该不重复」。若将来决定加唯一约束，
   它会红，那时它就自动变成需求的守卫。

## 真实场景

供应商先发确认函、后发正式发票（两封不同邮件、同一个 invoice_no），
或同一张发票因网络重发被收两次 —— 需求 3 的「汇总金额」就会**多算一倍**。

**这是决策项不是缺陷**：跨账户的同一张发票（公司替员工垫付）也许**应当**分开记。
我替不了用户定这个。但现在它有了可复核的证据。

---

## §7cl `FetchBody` 的读取上限：写了用例、又用负控**推翻了我自己的结论**（2026-10-02）

`fetcher.go:407-418` 的 `FetchBody` 有**两道**截断：请求里带部分取
`<0.maxBytes>`（服务器少发），以及取回后客户端兜底 `body[:maxBytes]`
（`fetcher.go:433-435`）。这一段此前**完全没测**。它是 §7bw 那个真 bug 的
同一处失效模式：`downloadPDF` 原来 `io.LimitReader` 截到 20MB 时
**返回 nil error**，调用方只做 `isPDFBytes` 落盘 + 标记成功 + 零报错。
同样的形状落在 `FetchBody` 上，后果是发票 XML 解析失败却零报错。

新文件 `fetcher_inproc_test.go`（7 用例），复用 §7ci 的进程内 IMAP 服务器。

### 卡了两天的不是产品 bug，是我的服务器把 FETCH 响应写错了

三个用例一直红在 `uid 11 not found`，而打点显示服务器**确实发了响应**。
症状完全指错方向（看起来像被测代码没把 UID 取回来）。查 go-imap 源码
（`imapclient/fetch.go`）后确认三处**响应格式**错误，全是我的锅：

1. **部分取在请求和响应里语法不同。**
   请求 `BODY[TEXT]<0.64>`（`writeSectionPartial` 写 `offset.size`）；
   响应必须 `BODY[TEXT]<0>` —— **只有 offset**（`readPartialOffset` 是
   `ExpectNumber` + `ExpectSpecial('>')`）。而且它在 `]` **之后**，
   写进方括号里（`BODY[TEXT<0>]`）同样解码失败。
   更反直觉的是：`matchFetchItemBodySection` 拿
   `(cmd.Partial == nil) != (resp.Partial == nil)` 判不匹配，
   **offset 必回显、size 反倒不能回显**（源码注释：not echoed back by the server）。
2. **`* N FETCH` 里的 N 是序号不是 UID**，UID 只出现在数据项里。
3. **响应必须带 `UID <uid>` 取件项。** 这条最隐蔽：客户端
   `writeFetchItems` 会「Ensure we request UID as the first data item」，
   而回包路由靠 `FetchCommand.recvUID`；`recvSeqNum` 对 `UIDSet` 直接
   `set, ok := cmd.numSet.(imap.SeqSet)` 返回 false。
   所以一个不带 UID 的 FETCH 回包会被**静默丢弃** —— 不报错、
   `Collect()` 返回 0 条、调用方只看到 `uid not found`。

### 负控推翻了我自己的结论：客户端那一半原本**根本没被覆盖**

我原本写的是「maxBytes 双重截断已覆盖」。**这是错的。** 负控实测：
把 `body[:maxBytes]` 改成 `&& false`（注入生效已验证），6 个用例**仍然全绿** ——
因为我的服务器**老实按 `<0.64>` 截断了**，客户端兜底那条路径根本走不到。

这正是负控不转红时的**第三种原因**：前两种（判据太松 / 注入没生效）
我都会去排查，**只有「回头质疑结论」才暴露**。于是补
`TestFetchBody_ClampsWhenServerIgnoresPartial` + `imapServer.ignorePartial`：
让服务器**无视**部分取照发 5000 字节，这才逼出客户端兜底。
现实依据是各家服务器对部分取的实现确有差异，这不是人造边界。

### 第二个被推翻的判据：「没收到 STORE」判不出 PEEK

`TestFetchBody_UsesPeekSoMailIsNotMarkedRead` 原来只断言
「服务器没收到 `UID STORE`」。负控把 `Peek: true` 改成 `false`，**用例照样全绿** ——
因为非 PEEK 是**服务器**置 `\Seen`，客户端**根本不会**为它发 STORE。
判据查错了通道。现在改判取件项里有没有 `.PEEK`
（`imapServer.sawFetchPeek()`），服务器也相应建模
（收到非 PEEK 的 BODY 取件即置 `seenSet`）。

### 五条负控全部转红，且各自只染红对应的那一条

| 负控 | 注入 | 转红的用例 |
|---|---|---|
| NEGCTL-1 | 客户端 clamp 加 `&& false` | `ClampsWhenServerIgnoresPartial` |
| NEGCTL-2 | `Peek: true` → `false` | `UsesPeekSoMailIsNotMarkedRead` |
| NEGCTL-3 | 不再发部分取（`if false`） | `TruncatesToMaxBytes` + `ClampsWhenServerIgnoresPartial` |
| NEGCTL-4 | 取不到邮件返回 `nil, nil` | `MissingUIDIsAnError` |
| NEGCTL-5 | 忽略 LOGIN 错误 | `RejectsBadLogin` |

NEGCTL-1/2 转红前**曾经是绿的** —— 也就是说这一节的两个结论是被负控
抓出来的，不是被「用例通过」认证的。

### 顺带一个环境坑：PowerShell 5.1 会静默吞掉 `.ps1` 的一整行

`negctl.ps1` 里 NEGCTL-4 那次调用**从头到尾没执行过、且没有任何报错**。
原因：PS 5.1 读**无 BOM** 的 `.ps1` 按 ANSI/GBK 解码，UTF-8 的中文注释
被解出**行尾反斜杠**，于是变成续行符，把下一行整行并进注释。
现象是「脚本少跑一条、不报错」，与「判据失效」难以区分。
脚本改成纯 ASCII 注释后正常。**教训：临时脚本别放中文注释。**

### 回归

- `internal/email` 全量 66.5s 通过；`-race` 63.1s 通过、**0 DATA RACE**。
- 覆盖率（`go test -covermode=count`，两次同口径实测）：
  包总量 **60.0% → 60.7%**；
  `fetcher.go:370 FetchBody` **0% → 80.4%**（此前没有任何用例调用过它）。
  同区域 `findBodySection` 75.0%。

---

## §7cm 0 覆盖清单：把「没测过」和「是死代码」分开（2026-10-02）

`internal/email` 共 **96 个函数 0 覆盖**。这个数字本身没用——0 覆盖可能是
「有真实调用方但没测」，也可能是「根本没人调用」。两者的处置完全相反。
用覆盖率输出 + 全树引用计数把它们分开：

- **A 类：0 覆盖且全树零引用**（名字在所有非测试 .go 里只出现 1 次 = 声明本身）

      store.go:275   ListAccounts
      store.go:1372  RevokeOAuthToken

  两条都逐个 grep 复核过，全 `backend/` 树内确实只有声明行。
  - `ListAccounts` 是 `ListAccountsScoped` 的无 scope 版本，而 `*Scoped`
    才是这个代码库的约定（server 侧三处调用的全是 `ListAccountsScoped`）。
    它只按 `user_id` 过滤、**没有 workspace 条件** —— 留着就是个跨工作区
    越权的地雷。
  - `RevokeOAuthToken` 的文档写着「Called only after we've already validated
    that the failure is permanent」，但没人调。它的实际副作用是
    `auth_type='password', enabled=FALSE` —— 也就是说 OAuth 令牌被吊销后
    **没有任何代码禁用该账户**，只会一直失败下去。
  - **我没有删它们**：删除是不可逆动作，且与 `folder_name`/`processed_at`
    死列是同一类待决项。已列为新的待拍板项。

- **B 类：0 覆盖但生产代码有真实调用方** —— 94 个，是「真未测」。

## 顺带查了一个疑似越权，结论是**排除**

`invoice_harvest.go:89` 的 `HarvestAll` 用的是**无 scope** 的
`ListHarvestableInvoices`，而 `HarvestInvoices` 的注释明确写着要走 scoped
清单「避免又走一遍无 scope 的 ListHarvestableInvoices（那会把别的 workspace
的待采集发票也拉进来重试）」。看着像跨工作区泄露。

**实际不是**：全树只有两处调用——
- `pipeline.go:316`（调度器，本来就要处理所有账户，无 scope 正确）
- `server_email_invoice.go:329` 的手动入口走的是 `HarvestInvoices` + scoped 清单。

所以**没有**泄露。没有 bug，如实记录为「已排查、已排除」。

## §7cn 补 `HarvestAll`：流水线第 4 步此前是**零执行证据**（2026-10-02）

`HarvestAll` 是流水线第 4 步（发票采集，需求 2/3）的入口。它此前唯一的测试在
`fetcher_greenmail_test.go:181`，而那个文件顶部是 `//go:build greenmail`。
本机 Docker daemon 没起来 ⇒ **这一步在本机从未真正跑过**
（覆盖率表里 `invoice_harvest.go:85 HarvestAll 0.0%`）。

用 §7ci 的进程内 IMAP 服务器重写，绕开 Docker。新增
`invoice_harvest_all_test.go`，7 个用例：未配置早退、只捞 new/pending、
列库时截断的预算语义、重试耗尽收尾、Fetcher 缺失早退、空清单 no-op、
以及一条真走 IMAP 拉原文的。

**顺带把 IMAP 服务器的 section 回显改成原样回显**（`requestedSectionSpec`）：
之前写死 `BODY[TEXT]`，而 `FetchMessageRaw` 请求的是 `BODY[]`，
客户端 `matchFetchItemBodySection` 比对 Specifier，写死就永远匹配不上。

### 五条负控，第三条又一次抓到我自己的错

| 负控 | 注入 | 转红的用例 |
|---|---|---|
| NEGCTL-6 | 不在列库时截断 | `RoundBudgetTruncatesAtListTime` |
| NEGCTL-7 | 捞取条件放宽成 `status <> 'filed'` | `OnlyPicksNewAndPending` |
| NEGCTL-8 | 去掉空清单早退 | `HarvestInvoices_EmptyList`（既有） |
| NEGCTL-9 | 让重试收尾永不命中 | `ExhaustedRetriesBecomeFailed` |
| NEGCTL-10 | 捞取条件去掉 `pending` | `OnlyPicksNewAndPending` + `ExhaustedRetriesBecomeFailed` |

**NEGCTL-9 第一版不转红**，而且是我写的用例本身有问题：我直接把那条
「重试耗尽」的发票种在预算内，harvestOne 跑一遍就把它置成 failed 了
（uid<=0 走 "no IMAP uid" 分支），收尾逻辑有没有被调**根本观察不到**。
又是 §7cl 那个第三种原因。改法：先种满 20 张 new 占掉预算，再把那条
耗尽发票用更大的 `created_at` 排到最后 —— 它这轮轮不到，
状态变化就**只能**来自收尾逻辑。

### 回归

覆盖率（同口径三次实测）：包总量 **60.0% → 60.7% → 62.2%**。
`invoice_harvest.go:85 HarvestAll` **0% → 80.0%**，
`HarvestInvoices` 81.2%，`harvestOne` 55.9%。
`recoverPOP3SourcedRaw`（POP3 发票自愈）**仍 0%**，下一轮目标。

---

## §7co 【真 bug】`Email.MessageID` 在生产里恒为空——发票自愈的强身份闸门一直是关着的（2026-10-02）

补 `recoverPOP3SourcedRaw` 覆盖率时撞出来的。先是两个成功路径用例**红**在
「IMAP resolved uid=11 returned a DIFFERENT message」——明明取的正是同一封
（Message-ID 相等、主题发件人日期全同）。负控没有转红，查下去才发现是
**产品代码的缺陷**，不是测试写错。

### 现象与证据

`emails.message_id` 一直在**写**：`fetcher.go:821`（IMAP 同步）与
`fetcher.go:944`（POP3 落库）都填了。但**没有任何读路径把它读出来**——
`GetEmailByID`（原 store.go:400）与 `GetEmailByIDScoped`（原 store.go:1742）
的 SELECT 列表里没有这一列，Scan 也没有对应目标。

实测（先确认写进去了，免得把「没写」误判成「没读」）：

    DB column message_id = "REAL-MSG-ID@example.com"
    GetEmailByID        em.MessageID = ""      <- 修复前
    GetEmailByIDScoped  em.MessageID = ""      <- 修复前

全 `backend/` 树里所有 `SELECT ... message_id` 都在 `diag_*_test.go` 的诊断
工具里（裸 SQL，不经过 `Email` 结构体）。所以 `Email.MessageID` 在生产里
**恒为空串**。

### 后果不是「少一个字段」，是**静默废掉一道安全闸**

`harvestOne` → `recoverPOP3SourcedRaw` → `sameEmailMessage` 用 `em.MessageID`
做「真实 Message-ID 强确认 / 强否定」（代码注释里称之为「最强」）。
em.MessageID 恒空 ⇒ `emHasReal` 恒 false ⇒ **那条分支在生产里从不执行**，
只剩 `subject + from + 同一天` 的弱判据。

而弱判据在真实数据上区分不了同名邮件——`invoice_selfheal_test.go` 的
`TestSameEmailMessage_DifferentInvoiceRejected` 早就把这个边界写明了：
两张 QQ Wallet 发票**主题/发件人/日期全同**，只有正文发票号不同。
也就是说这道闸门恰恰在最需要它的场景下是失效的。

### 修法

两个 getter 的 SELECT + Scan 各加一处 `message_id`，共 4 个点。
`GetEmailByID_MessageIDIsOptional` 守住 `message_id` 可空（旧行/客户端推送
的邮件就是空的），不让修复顺手把 NULL 变成崩溃。

### 双向负控

| 步骤 | 结果 |
|---|---|
| 撤回修复（4 处都还原成不读该列） | **4 红**：`GetEmailByID_ReturnsMessageID`、`GetEmailByIDScoped_ReturnsMessageID`、`RecoverPOP3SourcedRaw_POP3UnavailableFallsBackToIMAP`、`RecoverPOP3SourcedRaw_UniqueHitWithMatchingMessageSucceeds` |
| 恢复修复 | 全绿 |

### 一个必须如实说明的限定：**当前真实数据上 blast radius 为 0**

psql 实测 `opencode_pocket`：

    emails 中 id LIKE 'em-pop3-%' 的行数 : 0
    120 封邮件的 message_id             : 全部是真实值（非 'pop3-%' 合成值）
    email_invoices                      : 仅 1 行，status=downloaded
    emails 中 body_path 非空            : 0

所以：修复本身是**经单测证实的真 bug**，但在**当前这份数据上不会被触发**——
`isPOP3SourcedEmail` 依赖 `em-pop3-` 前缀，而库里一封都没有。

**同时更正本文档早先的说法**：§7co 之前多处提到「两张真实 QQ Wallet 发票
（uid=134/135）永远 failed」「POP3 侧 279 封」。那些描述对应的是
**已被恢复掉的旧 schema 状态**（§7bo 记着 schema 被别人恢复），
与现在这 120 封的库**对不上**。不要拿旧描述推断当前数据。

### §7cp 顺带补上的自愈测试

`invoice_harvest_selfheal_test.go`，6 个用例：
POP3 腿不可用时降级到 IMAP、0 命中不猜、>1 命中不猜、
反查到的 UID 取回另一封必须丢弃（且不得回填缓存、不得存文件）、
唯一命中且内容相符则成功并回填缓存、空主题直接拒绝。
IMAP 服务器的 `UID SEARCH` 现在会真的回 UID 集合（原来恒回空）。

**POP3 那条腿仍然测不了**，原因是缺测试缝：`RefetchPOP3RawByIndex` 内部用
`pop3EndpointFor(acc)` 从**邮箱域名**推导出 POP3 主机，没有注入口。
对照 IMAP 的 `Fetcher.dialTLS`（fetcher.go:30，注释明写「仅为测试留缝」），
POP3 侧四个入口全部直接 `net.Dialer{}.DialContext`。要补这条腿得先给
pop3_fetcher 加 dial 缝——那是生产代码改动，已列为待决项，本轮未擅自做。

覆盖率（同口径四次实测）：包总量 **60.0% → 60.7% → 62.2% → 63.4%**；
`recoverPOP3SourcedRaw` **0% → 75.0%**，`ResolveRealUIDByHeader` 75.0%。

---

## §7cq 把 message_id 那一类缺陷**系统性**查一遍：又抓到第二个（2026-10-02）

§7co 那类缺陷的特点是**单个字段**既不报错也不告警，只在下游某个 `if` 上
悄悄失效。所以修一个不够，我把 `Email` / `Account` / `Invoice` 三个结构体的
**全部字段**做了一遍「写了但从不读回」的扫描：取每个字段在**非测试** Go 代码里
是否作为 `Scan` 目标或被赋过值，没命中的就是候选。

结果：修完 message_id 后只剩一个 —— **`Email.BodyPurged`**。

### 第二个同类缺陷：摘要的「禁止回源」守卫一直关着

`model.go:57` 写明 `BodyPurged` 的语义是「正文已清空且**禁止回源**」。
`server_email_summary.go:178` 的 `summarizeBody` 第一句就是拿它当守卫：

```go
if em.BodyPurged { return "" }
```

而这个 handler 的 `em` **只**来自 `GetEmailByIDScoped`（同文件 :69），
那个方法的 SELECT 列表里**没有** `body_purged` ⇒ `em.BodyPurged` 恒 false
⇒ 守卫**从不触发**。

守卫失效后的实际行为：用户软删除一封邮件（`SoftDeleteEmailsScoped` 会置
`body_purged=TRUE`、`body_path=NULL`、清空 snippet）之后，再对这封邮件点
「总结」——

1. 守卫不生效；
2. `readCachedEmailBody` 失败（body_path 已 NULL）；
3. 继续回落到 `FetchMessageRaw(em.AccountID, em.UID)`，**把用户已删除的
   正文从 IMAP 重新拉回来**；
4. 喂给 LLM；
5. `SetSummaryScoped` 把摘要**写回那行已删除的记录**——那个 UPDATE
   （store.go:518）也没有 `deleted_at=0` 过滤。

也就是说这不只是浪费一次 LLM 调用，而是把「已删除、禁止回源」的数据
重新取回并回写。

### 真实数据现状（如实记录）

psql 实测 `opencode_pocket.emails`：120 行，`body_purged=TRUE` **0 行**，
`deleted_at>0` **0 行**，有摘要的 19 行。

⇒ 这个缺陷**目前没有实际影响**。它是「闸门在逻辑上一直关着」，
不是「已经造成了损失」。与 §7co 一样，**不夸大**。

### 修法与负控

store 侧：`GetEmailByIDScoped` 的 SELECT + Scan 各加一处
`COALESCE(e.body_purged, FALSE)`。
消费端：`server_email_summary_purged_test.go` 守 `summarizeBody` 必须在
**任何读取动作之前**短路（用 `&Server{}` 不注入 store/fetcher，碰了就会暴露）。

负控：把该列硬编码成 `FALSE`（= 字段不被填充，等价于原缺陷）→
`TestGetEmailByIDScoped_ReturnsBodyPurged` 转红；恢复后转绿。

**API 面影响**：`BodyPurged` 带 `json:"bodyPurged,omitempty"`，所以修好之后
只有**确实被清空**的邮件才会多出这个字段，false 被 omitempty 吞掉——
正常邮件的响应体不变。

### 回归

`internal/email` 全量 73.0s 通过。`internal/server` 11.7s，
**仅剩那两个既有失败**（`TestTaskWriteGuardBlocksPlainMemberPatch` /
`Delete`，404/403 语义分歧，见 §7az，非本轮引入、不在邮件分支）；
本轮新增的 `TestSummarizeBody_*` 两条通过。

---

## §7cr 给这一类缺陷做静态护栏——**前两版是废的，负控把它们抓了出来**（2026-10-02）

两个同类缺陷都是偶然撞上的，所以想固化成护栏。**但护栏本身比它要防的 bug
更容易骗人**：第一版和第二版都「全绿」，而负控证明它们**根本抓不到**
自己声称要防的那个回归。

### 第一版：按字段名判「有没有被读过」——做不到

第一版：拿结构体字段清单，检查它在非测试代码里是否作为 `Scan` 目标
（`&x.Field`）或被赋过值（`x.Field =`）。

诊断实测（临时探针，打完就删）：

    &x.MessageID 形式的 Scan 目标：一个都没有
    x.MessageID = 的位置：fetcher.go:959、mime.go:438、store.go:423、store.go:1786

因为 `GetEmailByID` 那几处是**先**把列 Scan 进一个 `sql.NullString` 局部变量
（`&messageID`），**再**赋给结构体（`e.MessageID = messageID.String`）。
字段名级的分析根本看不到「DB → 结构体」这一跳。

而「只要被赋值过就算读过」的退路更糟：`fetcher.go:959` 与 `mime.go:438`
是**在内存里构造** Email 时赋的值，与 DB 无关，照样满足判据。
⇒ 第一版负控**不转红**，护栏形同虚设。

### 第二版：把范围锚到单个函数内——**还是被注释骗了**

第二版：若函数体里出现 `X.Field = ...`，则**同一个函数**的源码里必须出现
该字段对应的列名。逻辑上更紧，但负控**仍然不转红**。

原因很荒唐：我自己在 `GetEmailByID` 上方写的那段解释性注释里就有
「message_id 必须在这里读出来」这句话，而判据用
`strings.Contains(函数体源码, "message_id")` —— **注释满足了对 bug 的检查**。

`pgisolation_guard_test.go` 的文件头就写着「注释能满足任何源码扫描，
所以这个测试先 strip 注释再匹配」，我**在同一个包里又犯了一次**。

### 第三版（落地）：只认字符串字面量

SQL 一定在字符串字面量里，所以改成遍历 AST、只收集 `token.STRING` 字面量，
再在里面找列名，天然免疫注释。两条负控都转红：

| 负控 | 注入 | 结果 |
|---|---|---|
| NEGCTL-A | 从 `GetEmailByID` 的 SELECT 删掉 `message_id` | **红** |
| NEGCTL-B | 把 `GetEmailByIDScoped` 的 `body_purged` 硬编码成 `FALSE` | **红** |

### 顺带：护栏还抓出了第三个候选，结论是「死字段」不是 bug

扫 `Email.DeletedAt` 时命中。查证结论与前两个**不同**：

- 软删除完全由 **SQL 谓词**执行（`WHERE deleted_at = 0`、部分表用
  `deleted_at IS NULL`），没有任何生产代码读 `Email.DeletedAt`——
  `soft_delete_test.go:14` 那句读的是另一个结构体
  `soft_delete.SoftDeleteRecord` 的同名字段。
- 所以它是**死字段**，不是**失效的守卫**。两者处置完全不同：前者是清理项，
  后者是修 bug。已列入待拍板（留着是个陷阱：下一个人写 `if em.DeletedAt`
  会得到一个永不触发的守卫），**没有擅自删**。

### 护栏的取舍（如实记录，不要当它比实际更强）

- 只守**已经出过事**的两个字段。全字段扫会误报一堆（有些字段是纯入参载体、
  有些函数的 SQL 在 helper 里），天天误报的护栏最后没人看。宁可少守。
- 豁免必须写理由（与本包 `pgDSNGuardExempt` 同一条规矩）。当前两条：
  - `ParseMIMEMessage`：Message-ID 从 MIME 头解析，与 DB 无关；
  - `syncPOP3Fallback`：赋的是**合成** Message-ID（`"pop3-"+uidl`），
    这正是 `sameEmailMessage` 用 `HasPrefix(msgID,"pop3-")` 把它排除在
    强确认之外的原因。**这条是第二版护栏才抓出来的**。
- 遍历 358 个函数；若一个函数都没遍历到（checked==0）直接失败——
  解析器坏了就静默放行是最坏的失败方式（同 `pgisolation_guard_test.go`）。

回归：`internal/email` 全量 80.0s 通过，注入无残留。

---

## §7cs 【需求 3 的真问题】同一张发票在磁盘上有**两份**——文件名不是发票的函数（2026-10-02）

这不是读代码猜的，是**对真实数据目录做的哈希比对**。

### 证据：`data/email-invoices/ws_user-admin/` 6 个文件，3 份不同内容

    7AB3033721A51A78  其他-云服务开票中心-1280.00-2026-09-28.pdf
    7AB3033721A51A78  其他-云服务开票中心-发票抬头-1280.00-2026-09-28.pdf
    0E331BF151CBFEA9  其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf
    0E331BF151CBFEA9  其他-杭州创客家投资管理有限公司-3500.00-2026-10-01.pdf
    CFA3181C1EE36E8B  其他-财务部-0.00-2026-09-30.pdf
    CFA3181C1EE36E8B  其他-财务部-0.00-2026-10-01.pdf

三对**逐字节相同**（SHA-256 前 16 位一致），文件名各不相同。
⇒ 磁盘上一半的发票 PDF 是重复件。

而且 `email_invoices` 现在**只有 1 行**（`inv_1790884695419622800_1`，
指向 `...-3500.00-2026-09-24.pdf`）——另外 5 个文件**没有任何 DB 行引用**，
它们是孤儿。先前记的「4 个孤儿 PDF」现在是 6 个文件 / 3 份内容。

### 根因链（每一环都能指到行）

1. `saveInvoiceFile`（invoice_harvest.go:401-403）在落盘前调
   `ParseInvoiceDateFromBytes(data)` 补开票日期。
2. 该函数（invoice.go:247-255）把 PDF **前 64KB 当字符串正则扫**。
   PDF 正文在压缩流里 ⇒ 绝大多数情况扫不出来 ⇒ 返回 `""`。
   （这与早先记录的「PDF 压缩流扫不出」是同一件事。）
3. `InvoiceFileName`（invoice_harvest.go:601-604）在 `InvoiceDate == ""` 时
   兜底成 **`time.Now().Format("2006-01-02")`**。
   ⇒ 文件名里嵌的是「**这次采集发生在哪一天**」，而不是「这是哪张发票」。
4. `saveInvoiceFile` 只 `os.WriteFile(tmp)` + `os.Rename`，**从不删除旧文件**。
   只要名字变了（日期被补上、或某轮重新采集），磁盘上就多一份。

三对里有两对正是**只差日期段**（`2026-09-24` vs `2026-10-01`），
与第 3 步的 wall-clock 兜底完全吻合。第三对差在 `seller`
（`云服务开票中心` vs `云服务开票中心-发票抬头`）——说明**提取出来的字段
本身会在不同轮次之间变**，而它们直接进了文件名。

### 本来该防住它的东西是**死的**

`InvoiceContentHash`（invoice_harvest.go:660-664）就是为「同一文件重复下载
内容一致性」写的，但全树**没有任何生产代码调用它**，只有 `invoice_hash_test.go`
在测它自己的数学正确性。那份测试的注释自己都写了：
「本文件不写『它还没被接线』的守卫用例……一个永远跳过的测试是**假守卫**」。

⇒ 现状是：去重能力**已实现但未接线**，而重复件**已经发生在真实数据上**。

### 对需求 3 的影响

需求要求「将发票文件进行整理」「建立共享文档及文件，进行整理，需要整理一个
列表，记录必要信息并**汇总金额**」。同一张发票落两份 ⇒

- 飞书可能被推两次；
- **汇总金额会多算一倍**（与 §7ck 那个「跨邮件重复」是**另一条独立成因**：
  那条是 DB 里两行，这条是磁盘上两个文件、DB 只有一行）；
- 「整理」承诺被直接破坏。

### ⚠️ 对一个**待拍板项**的警告

待决项里有「已 `downloaded` 发票的 `invoice_date` 回填：是否建回填路径」。
**如果按「改 DB 里的 invoice_date，然后重新落盘」来实现，
就会把上面这个缺陷对每一张已下载发票再触发一遍**（名字从兜底日期变成真实日期
⇒ 新文件，旧文件留下）。所以这个回填路径必须与命名/去重方案**一起**定，
不能单独做。

### 我没有动手的原因

修法有多个合理选项，各自是**产品语义**而不是实现细节，我不替你定：

- 日期未知时文件名该用什么？（省略日期段 / 用邮件日期 / 用内容哈希前缀 / 用发票号）
- 重新采集到同名发票时，是覆盖、跳过（复用已有文件）、还是改名留档？
- 那 5 个孤儿文件怎么处理？

同样**没有**写一条「已知会红」的用例，也没有写 `t.Skip` 的假守卫——
本包自己的 `invoice_hash_test.go` 已经把这条规矩写在注释里了。

---

## §7ct 【需求 3】汇总 CSV 没有 BOM——Excel 打开是乱码（2026-10-02）

顺着 §7cs 继续查需求 3 的另一件交付物：**那个「列表」本身**。

### 先说结论：列表的**内容**是对的，**编码**是错的

真实产物 `invoices-summary-20261002-041951.csv`（426 字节）内容完全符合要求：

    费用类型,对方单位,金额,币种,发票号,日期,状态,文件名,来源邮件
    其他,杭州创客家投资管理有限公司,3500.00,CNY,26332000008261110741,2026-09-24,downloaded,
      其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf,您收到来自…的发票…
    合计,,,,,,,3500.00,

MD 版本也正常（标题、表格、合计 3500.00）。

顺带核实了两件**已经是对的**、不需要动的事：

- **合计按币种分组**已实现（pipeline.go:942-985，`centsByCur`/`curOrder`，
  多币种时每币种一行合计且币种列带标签），与飞书表格那条路径同口径。
- 列序稳定、金额列在索引 7，合计行沿用同一列位（避免下游解析分两套规则）。

但文件首 3 字节实测是：

    E8 B4 B9      <- "费" 的 UTF-8 前三字节，**没有 BOM**

中文 Windows 的 Excel 打开**无 BOM** 的 UTF-8 CSV 会按系统 ANSI 代码页（GBK）
解码，整表中文变乱码。这份 CSV 是需求 3 明确要交付的「整理一个列表」——
用户拿到打不开就等于没做。

### 修法与取舍

`BuildInvoiceSummaryDocs` 写 CSV 时前置 `EF BB BF`；**Markdown 不加**
（MD 不由 Excel 打开，BOM 只会在首行前留三个不可见字符）。

**无兼容风险**（查证过，不是假设）：全树没有任何代码解析这些 CSV——
它们只以**文件名**形式过 API（`shareDocCsv`），测试里也只有 `os.Stat`，
不读内容。

### 负控

把 BOM 前缀撤掉 → `TestBuildInvoiceSummaryDocs_CSVStartsWithUTF8BOM` 转红。
负控输出本身就复现了乱码：

    CSV 没有 UTF-8 BOM（首 3 字节 = E8 B4 B9）
    BOM 之后不是表头，实际开头 = "用类型,对方单位,金额,…"

「费用类型」变成「用类型」——首字被按错误偏移吃掉了，正是 Excel 那边
会发生的事。

### 同时记一个我自己的测试错误

`TestBuildInvoiceSummaryDocs_CSVCarriesRequiredColumns` 第一版把表头
**9 列写成了 8 列**（漏了末列「来源邮件」），测试红了。查下来是**我写错**，
产品代码是对的。列序与列数是给用户的契约，所以这条断言保留。

回归：`internal/email` 全量 71.7s 通过，注入无残留。

---

## §7cu 【需求 4 的真 bug】2 天扫描窗口会把「从未提醒过」的重要邮件**永久漏掉**（2026-10-02）

先说排查起点：真库里 `emails.notified_at > 0` 的有 **0 封**，而
`importance='high'` 的有 25 封 —— 需求 4 看起来完全没生效。查下来是**两件事
叠在一起**，其中一件是配置/时序，另一件是**代码缺陷**。

### 第一件（不是 bug）：这 120 封邮件从来没进过定时流水线

- 120 封的 `created_at` 全部落在 **2026-10-01 23:56:38 ~ 23:56:52**（14 秒内），
  是客户端一次性推入的，不是 IMAP 逐封同步。
- `notifications` 表**全表 0 行**（不只是 `source='email'` 为 0）→ `Dispatch`
  从来没成功写入过任何通知。
- `email_scheduler.pipelineLoop`（`scheduler.go:684-698`）用
  `nextTimeAt(now, pipelineHour, 0, 0)` 排下一次，**启动时不补跑**。当前
  pocketd 进程 01:36:00 启动，`POCKET_EMAIL_PIPELINE_HOUR` 默认 8 → 今天
  08:00 才是第一次。
- 数据目录里 `invoices-summary-*` + 两个发票 PDF 精确出现在 **10-01 08:00:01**
  —— 那是一次真实跑过的定时流水线，但发生在 23:56 推入**之前**，作用的是
  上一批行（当时的行数是 162，见 `store.go:511` 注释），那批行已被替换。
- 23:56 之后磁盘上只有 10-02 03:32 / 03:59 / 04:00 / 04:19 四个产物，对应
  我自己做需求 3/5 验证时打的**手动** harvest / export / summary 端点。

所以 `notified_at=0` 本身**不能**推出提醒逻辑有 bug —— 这批邮件压根没被扫过。

### 第二件（**是真 bug**）：窗口用时间过滤，而不是用「未提醒过」这个持久状态

`pipeline.go:791`：

```go
since := time.Now().AddDate(0, 0, -2).Unix()
emails, notified, err := p.Store.ListEmailsSince(ctx, since, 500)
```

`ListEmailsSince`（`store_pipeline.go:69`）是 `WHERE date >= $1`。于是
「已提醒」这个**持久状态**被降级成了一个**时间窗口**条件：一封邮件只要
`notified_at` 还是 0 且 `date` 超过 2 天，就再也不会被扫到，也**永远不会被
提醒**，而且没有任何日志、报告字段或告警能看出这件事发生过。

「没提醒过」和「很久以前」本该是两个独立条件，现在被绑在一起了。

### 实测 blast radius（不是推演，是数出来的）

`importance='high'` 共 25 封，`notified_at>0` 的 0 封。窗口边界按
`流水线触发时刻 - 2 天` 算（注意是 **08:00 再减 2 天 = 09-30 08:00**，
不是 08:00 本身；我第一次就是这么算错的，得出过「一封都进不去」的错误结论）：

| 场景 | cutoff epoch | 仍在窗口内 | 已被永久漏掉 |
|---|---|---|---|
| 今天 08:00 那一轮 | 1790726400 | **5** | **20** |
| 明天 08:00 那一轮 | 1790812800 | 0 | **25** |

25 封全是 2026-09-30 的 GitHub Actions 失败通知（`Run failed: ...`），
`date` 分布在 05:34 ~ 09:26。也就是说：**今天 08:00 只有 5 封能被提醒，
剩下 20 封从明天起彻底出窗**；且需求 4 **没有任何手动补发入口**
（`/api/email/*` 下没有 remind 类路由，只有 `POST /api/email/pipeline/run`
整条重跑，而重跑仍然过同一个 2 天窗口，救不回那 20 封）。

### 修法涉及产品语义，本轮**不擅自实现**

最小改动是把查询条件从时间窗口换成持久状态（`notified_at=0 AND
importance='high'`，时间窗口退化成扫描上限）。但这会一次性把 20 封旧邮件
全推出来，所以要你先拍板两件事：

1. **未提醒过的邮件要不要过期？** 需求原文只说「对其它重要邮件进行提醒」，
   没有有效期。按字面就是不过期（则本节这 20 封应该被补提醒）。
2. **如果要过期，过期多久？** 以及首次上线时是否只补最近 N 天，避免一次性
   20 条通知轰炸。

未定之前不改代码，也不写会红的常红用例（本包 `invoice_hash_test.go` 注释里
定的规矩）。另外 §7ct 那条已修的 CSV BOM 不受影响。

---

## §7cx 【需求 1】拿真实邮件离线跑了一遍垃圾判定——**规则会把安全告警判成垃圾**（2026-10-02）

需求 1 的规则（`LooksLikeSpam`）此前只有单元测试，**从没在真实数据上跑过**。
`Pipeline.cleanSpam` 的预演报告本该给这个答案，但跑流水线要连真实 IMAP、
调计费 LLM，所以一直没拿到。

### 新增的探针：`backend/cmd/spamdryprobe`

`LooksLikeSpam` / `InvoiceCandidate` 都是**纯函数**，不碰网络、不碰 IMAP、
不调 LLM。把 emails 表的行读进内存就能得到和 `cleanSpam` **完全相同**的判定
（同一份代码，不是复刻）。

只读保证是硬的：连接串强制 `default_transaction_read_only = on`，
并且启动时**主动做一次写尝试**来证明它生效（`CREATE TEMP TABLE`）——
写成功了反而说明保护没生效，此时程序直接退出。所以「不动生产数据」
不是承诺，是能被验证的事实。

```
POCKET_PROBE_POSTGRES_DSN=... POCKET_PROBE_PG_SCHEMA=opencode_pocket \
  go run ./cmd/spamdryprobe/                  # 全量报告
  go run ./cmd/spamdryprobe/ -dump-from monitor.aliyun.com   # 打印原文，人工核对
```

### 实测结果（opencode_pocket，120 封，deleted_at=0）

```
会判为垃圾（score>=100）        6 封
发票候选（判定时短路）          6 封
importance=high（判定时短路）   35 封
near-miss（未判垃圾但有分）      1 封（30 分）
```

**命中 6 封全部来自同一个账户** `acct-...-2`：

| 封数 | 发件人 | 分数 | Why |
|---|---|---|---|
| 4 | `monitor@monitor.aliyun.com` | 100 | 退订特征:取消订阅 |
| 2 | `InfoQChina@edm.infoq.com.cn` | 100 | 退订特征:取消订阅 |

### 【高危】4 封是**安全风险告警**，不是广告

`-dump-from` 打出的原文（截取）：

> 您的安全中心当前为免费版 **已发现 5 条安全风险**，您的安全评分为 80，
> 您的资产存在较多安全隐患，建议您及时加固安全防护体系。**立即处理**
> **待处理漏洞 91** 0 81 10　**待处理告警 36** 8 28 0
> …
> 立即登录 **取消订阅通知** …

得分 100 的唯一理由是「取消订阅」四个字，而它在**阿里云邮件模板的页脚
导航区**里，是个「管理通知订阅」的链接，与营销退订不是一回事。

也就是说：一旦把 `POCKET_EMAIL_SPAM_DRYRUN` 置 false，这 4 封
「已发现 5 条安全风险 / 待处理漏洞 91」会被 **IMAP MOVE 进垃圾箱**。
安全告警被藏起来的代价远高于几封 InfoQ 广告留在收件箱。

**修法建议**（属产品语义，**本轮未擅自改**）：把安全/告警类发件域加进
既有的 `spamDomainWhitelist`（`spam.go:62`，那张表本来就是为「误杀代价高」
准备的），或让退订特征不适用于系统通知类发件人。

### 顺带更正一条已经过期的注释

`spam.go` 里 `LooksLikeSpam` 的原注释断言：「补退订特征（查摘要）在这份
数据上无效——真实 emails.snippet 存的是**原始 MIME 头**（105 封形如
`------=_Part_... Content-Type: text/html`）」。

实测当前 120 封（psql 统计）：

| 指标 | 值 |
|---|---|
| snippet 以 `------=_Part_` 开头 | **1 封** |
| snippet 以 `Content-Type` 开头 | 0 封 |
| snippet 以 RFC822 邮件头开头 | 0 封 |
| snippet 为空 | 0 封 |
| snippet 平均长度 | 377 字符（最长 501） |

即 119/120 是**真实正文**（形如「极客时间 点击这里取消订阅 ------=_Part_…」——
正文在前、MIME 边界在尾部）。退订特征**现在有效**，而且是 6/6 命中的
决定性信号。那条注释描述的是上一批数据来源，留着会误导，已按实测数字改写
（只改注释，不改行为）。

### 另一个已量化的自洽性问题（同样未擅自改）

同一发件人判定不一致：`InfoQChina@edm.infoq.com.cn` 共 3 封、vol=3，
其中 2 封页脚带「点击这里取消订阅」→ 100 分判垃圾；第 3 封
（InfoQ 每周精要 No.940）snippet 开头是正文、没匹配到退订 → 只 30 分，
留在收件箱。差别只在某一封的邮件模板有没有那个页脚链接。

「列表推送就是列表推送」，按单封模板决定去留在语义上说不通。改成按发件人
整体判定同样属产品语义（会不会误杀同域真人邮件），等拍板。

### 分布形态

命中 6、near-miss 1，中间地带**为空**。这不是「阈值卡在边缘」，而是
「要么明显是列表推送、要么几乎没特征」。所以调阈值 100 解决不了上面两个
问题——它们都不是阈值问题。

### 回归

`internal/email` 全量 74.0s 通过（只改注释 + 新增 cmd，行为未变）。

---

## §7cy 【需求 2】真实数据取证：6 封发票候选只建档 1 封；顺带查出 `has_attachments` 在生产里恒为 false（2026-10-02）

需求 2/3 此前在真实数据上的执行证据是 0。新增只读探针
`backend/cmd/invoiceprobe`（与 §7cx 同一套只读保证），把
`InvoiceCandidate` / `ExtractInvoiceLoose` / `InvoiceFileName` 这些**纯函数**
在真库 120 封邮件上跑一遍。

> **2026-10-02 06:1x 更正**：本节初稿说探针结果「与流水线**完全相同**」，
> 这个说法**是错的**，下面已按实测改正。保留更正记录是因为错误的因果链比
> 没有结论更危险。

### 流水线其实是两趟，探针只测了第一趟

`extractInvoiceCandidates`（pipeline.go 步骤 1.5）分两趟：

| 趟 | 位置 | 输入 | 门控 |
|---|---|---|---|
| 第 1 趟 | `pipeline.go:412` | `ExtractInvoice(e, "")` —— 只有 envelope | 无 |
| 第 2 趟 | `pipeline.go:469` | `ExtractInvoiceLoose(e, 正文, HasInvoiceAttachment(附件))` —— IMAP 取回的完整正文 | `pipeline.go:414`：`p.Fetcher != nil && e.UID > 0` |

探针复现的是**第 1 趟**（`ExtractInvoiceLoose(e, "", false)` 与
`ExtractInvoice(e, "")` 是同一个函数）。第 2 趟要连真实 IMAP，离线做不了。

**所以本节的「会建档 N 封」是下界，不是需求 2 的最终答案。**

### 实测（测量时刻 06:12）

```
发票候选（命中关键词）          6 封
第 1 趟 ExtractInvoiceLoose(false) 命中 1 封
对照 ExtractInvoiceLoose(true)    命中 6 封   ← 这一列不是生产取值
差值（只因 hasInvoiceAttachment 为真才成立）  5 封
```

第 1 趟唯一能建档的那封，字段完整、命名正确：

```
您收到来自杭州创客家投资管理有限公司的发票，发票号码：26332000008261110741…
  kind=e-invoice  category=其他  seller=杭州创客家投资管理有限公司
  amount=3500.00 CNY  invoiceNo=26332000008261110741  invoiceDate=2026-09-24
  文件名: 其他-杭州创客家投资管理有限公司-3500.00-2026-09-24-26332000008261110741.pdf
金额汇总（按币种分组）: CNY 3500.00 (1 张)
```

即需求原文的 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 格式**在这封上是达标的**，
日期来自主题，而不是 §7cs 那个必然失败的 `ParseInvoiceDateFromBytes`。

### 差出来的那 5 封：不能说成「被丢弃」

```
Xiaomi MiMo API 开放平台扣款成功通知        amount=0.00  no=(空)
所需操作：AWS 账户提示                      amount=0.00  no=(空)
Amazon Web Services Account Alert           amount=0.00  no=(空)
AWS 账户提醒                                amount=0.00  no=(空)
来自 Apple 西湖商务团队的问候 - 杭州开轩…    amount=0.00  no=(空)
```

`amount=0.00` 只说明**正文里没有金额**。金额可能在附件 PDF 或门户页里 ——
那正是第 2 趟 + 采集器该干的事。初稿把这 5 封写成「会被丢弃」是**过度
解读**：第 1 趟建不了档 ≠ 整条链路建不了档。

### `has_attachments` 那一列：不参与发票判定（初稿的因果链错了）

初稿说「放宽路径因 `has_attachments` 恒 false 而从不执行，所以 5 封被丢」。
**这是错的。** 第 2 趟的附件判定是
`HasInvoiceAttachment(b.parsed.Attachments)` —— 从刚解析的 MIME **现场算**，
**根本不读 DB 这一列**。全树 `e.HasAttachments` 只被 store 的扫描器读进
结构体（store.go:351/412/773/1775/1970/2110），**没有任何业务逻辑消费它**。

这一列确实有真缺陷，但后果是另一件事：

| 环节 | 状态 |
|---|---|
| POP3 路径 `fetcher.go:974` `em.HasAttachments = len(parsed.Attachments) > 0` | ✅ 正确置位 |
| IMAP 插入时 | 只有 envelope，无从得知 |
| IMAP 事后唯一机会 `MarkEmailBodyCached`（`store.go:1925`） | ❌ `has_attachments = COALESCE(has_attachments, FALSE)` —— **恒等操作** |
| harvest 回填 `invoice_harvest.go:317` | 只 `BodyCache.Put`，不调它 |
| 全树 `HasAttachments: true` | 只出现在**测试文件**里 |

它唯一的消费者是**前端的 📎 标记**（`emails-store` → `EmailInboxView:130`
`v-if="m.hasAttachments"`）。实测 120 封为真的 0 封 → **IMAP/客户端推送的邮件
上 📎 恒不显示**。这是需求 7 的一个可见缺陷，但与发票建档无关。

### 数据是活的：本节所有数字都只在测量时刻成立

运行中的 pocketd 每分钟同步真实账户，库的内容在变。同一个查询两次实测：

| 时刻 | `uid IS NULL` | `uid > 0` |
|---|---|---|
| 05:33 | **120 / 120** | 0 |
| 06:11 | 0 | **120 / 120** |

我曾据 05:33 的数据推断「第 2 趟因 `uid > 0` 为假而永不执行」——到 06:11
这个前提已经不成立。**任何基于「当前库」的结论都必须带测量时刻**，
否则下一轮复现就会得到不一样的数字、还会以为自己记错了。

现在（第 2 趟门控已打开）要拿到需求 2 的最终答案，只差「授权跑一次真实
IMAP 取原文」这一件事。

### 顺带发现：现存那条发票记录的 `file_name` 与当前命名规则不一致

`email_invoices` 全表只有 1 行：

```
file_name     = 其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf
invoice_no    = 26332000008261110741
invoice_date  = 2026-09-24
status        = downloaded
attempts      = 1
```

`InvoiceFileName`（`invoice_harvest.go:597`）在发票号非空时会追加一段
`-<发票号>`（`:591` 注释：「发票号为空时不加这一段」）。库里 `invoice_no`
明明有值，文件名却没有这一段 —— 说明它是**旧版命名规则**的产物。

今天按同样输入重采会算出 `...-2026-09-24-26332000008261110741.pdf`，
与现存文件名**不同**。而 §7cs 已证实 `saveInvoiceFile` 从不删旧文件，
所以这是「同一张发票在磁盘上出现两份」的**又一条独立成因**：
不是日期缺失导致的重名，而是**命名规则随代码变过**。

该行状态是 `downloaded`，今天不会自动重采，所以还没发生；但只要它被重置
或人工重新采集就会触发。命名/去重方案（待拍板）必须把这一条一起覆盖。

### 回归

`go build ./...` 与 `go vet ./cmd/...` 干净（本节只新增 cmd，未改行为）。

---

## §7da 两个探针的负控：证明它们的数字确实来自生产函数（2026-10-02）

§7cx / §7cy 的结论全部建立在两个探针输出的数字上。**探针自己也要被验** ——
一个自己重算一遍规则的探针，输出再漂亮也是废话。

方法：临时改生产代码里**唯一决定那个数字的那一行**，看探针输出是否跟着变。
变 ⇒ 数字由生产函数驱动；不变 ⇒ 探针在自说自话，整节结论作废。

### 负控 A：spamdryprobe ← `LooksLikeSpam` 的阈值

`spam.go:198` 的 `if score >= 100` 临时改成 `>= 30`：

| | 会判为垃圾 |
|---|---|
| 原样 | 6 封 |
| 阈值 100 → 30 | **7 封**（那封 30 分的 near-miss 并入） |

数字跟着动了 ⇒ 探针走的是 `LooksLikeSpam` 本体。已还原。

### 负控 B：invoiceprobe ← `ExtractInvoiceLoose` 的 return

`invoice.go:374` 的硬门槛临时改成无条件返回
（`if true || (inv.Amount == 0 && ...)`）：

| | 第 1 趟(false) | 对照(true) | 差值 |
|---|---|---|---|
| 原样 | 1 封 | 6 封 | 5 |
| 无条件 return | **0 封** | **0 封** | **0** |

两个数字同时归零 ⇒ 6 和 1 都由这一个生产 return 决定。已还原。

### 顺带抓到探针自己在说谎

负控 A 暴露出：`spamdryprobe` 输出标签里的 **「score>=100」是硬编码字面量**，
而真正的阈值藏在 `LooksLikeSpam` 内部、探针读不到。阈值一改，标签立刻开始
说谎 —— 而数字还是对的。**一个只对了一半的工具比错的更危险**，因为人会只盯着
那个「对」的部分。

已改成不写死数字（「会判为垃圾（score 过线）」），并补上测量时刻输出。

### 还原是逐字节的

两个生产文件都动过，验还原不能只靠「记得改回去了」：

```
git diff --name-only backend/internal/email/   ->  空
```

与 HEAD 完全一致。

回归：`internal/email` 全量 74.4s 通过（动过 spam.go / invoice.go 又还原，
跑一遍确认还原没有副作用）。

---

## §7db 需求 6 的服务端委托腿 `delegatePipeline` 此前**零覆盖**（2026-10-02）

`execution_mode_test.go` 只测了**判定**（`shouldDelegatePipeline`：mode/URL
怎么算才算委托）。真正干活的 `delegatePipeline`
（`server_email_pipeline.go:273`）——**把带邮箱权限的整条流水线 POST 到远端
的那个函数** —— 全树零用例。

判定绿了不等于委托能跑：URL 校验、非 200、解码失败、请求到底发没发出去，
一条都没有守护。而这类代码的失败模式特别难看：配置写错时它**静默地把请求
发到别处**，或者返回一条与真实原因无关的 decode 报错。

新增 `backend/internal/server/delegate_pipeline_test.go`，5 条：

| 用例 | 断言 |
|---|---|
| `DecodesRemoteReport` | 远端报告被如实解码（定时任务只看 `len(rep.Errors)`，字段丢了只剩空报告） |
| `RejectsNonHTTPSchemes` | `file://` / `ftp://` / 无 scheme / `https://` / 空 / 纯空白 全部在**发请求之前**被挡；判据是「远端一次都没被敲」 |
| `Non200ReportsStatusNotDecodeError` | 5xx 错误页不得掉进 JSON decode |
| `MalformedJSONIsLabelledDecode` | 200 但非法 JSON 必须标成 `delegate decode`（与「远端挂了」区分） |
| `UnreachableRemoteSurfacesError` | 连不上必须报错，不能返回空报告骗过定时任务 |

### 两条负控（都实测转红）

1. **去掉 scheme 校验**（只留 `target.Host == ""`）→
   `RejectsNonHTTPSchemes` 红：`ftp://example.com/run` 一路走到
   `client.Do` 才炸，报的是 `unsupported protocol scheme` 而不是配置项名。
2. **去掉 non-200 分支** → `Non200ReportsStatusNotDecodeError` 红，且
   精确演示了那条分支存在的理由：502 的 HTML 错误页会变成
   `delegate decode: invalid character '<'` —— 排障的人会去查 JSON 格式，
   而真正的问题是远端挂了。

### 顺手把一条契约钉成断言

实测确认：委托请求**不带任何凭证、不带 body**（远端用自己的配置跑）。
这不是「应该这样」，是当前实现的事实。写死成断言是为了当 **tripwire**：
谁给它加了鉴权，用例会红，迫使他同时更新本节与部署文档，而不是悄悄改掉
「谁能触发带邮箱权限的流水线」这个事实。

**这也是一条待决项**：远端编排服务当前是无鉴权触发。是否需要共享密钥，
属部署语义，本轮不擅自加。

### 回归

`internal/server` 全量：除两个**既有**失败
（`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，即 §7az 记的 404/403
语义分歧，非邮件分支、非本轮引入）外全绿，11.0s。
`git diff --name-only backend/internal/server/server_email_pipeline.go` 为空 ——
生产文件逐字节还原。

---

## §7dc 【工具链】UTF-8 BOM 让 `internal/server` **整包无法做覆盖率插桩**（2026-10-02）

给 §7db 补完覆盖后顺手想给 `internal/server` 出一份覆盖率数字，结果：

```
internal\server\server_email_invoice.go:1:1: invalid BOM in the middle of the file
FAIL github.com/halfking/pocket-opencode/backend/internal/server [build failed]
```

而 `go vet ./internal/server/` **通过**、不带 `-coverprofile` 的 `go test` **通过**
（11.0s，只有那两个既有失败）。也就是说：

> **「这个包有没有测试」和「这个包能不能被测量」是两件事，
> 而后者坏了没有任何人会发现。**

后果直接：`internal/server` 在此之前**从来没有过覆盖率数字**，于是
`delegatePipeline`（需求 6 服务端委托腿）零覆盖能长期躺着（§7db）。

### 因果链是实测的，不是推断

只去掉 `server_email_invoice.go` 的 BOM 后重跑，错误**精确移动到下一个文件**：

```
internal\server\server_email_pipeline.go:1:1: invalid BOM in the middle of the file
```

两个都去掉才通过。两个文件各自只有一个 BOM、位于 offset 0
（Go 报的 "in the middle" 是它的通用措辞，不是真在中间）。

### BOM 分布：719 个 .go 文件里 8 个

| 文件 | 类型 | 备注 |
|---|---|---|
| `internal/server/server_email_invoice.go` | 非测试 | 阻断 `internal/server` 覆盖率 |
| `internal/server/server_email_pipeline.go` | 非测试 | 同上 |
| `internal/config/config.go` | 非测试 | 阻断任何 `-coverpkg=./...` |
| `internal/email/export_pdf_test.go` | 测试 | 不被插桩，无影响 |
| `internal/email/mime_header_decode_test.go` | 测试 | 同上 |
| `internal/email/pipeline_dryrun_test.go` | 测试 | 同上 |
| `internal/feishu/client_test.go` | 测试 | 同上 |
| `internal/server/server_vault_empty_blob_test.go` | 测试 | 同上 |

8 个全部处理：非测试文件阻断覆盖率，测试文件留着是定时炸弹（哪天被人挪成
非测试或被 `-coverpkg` 扫到就炸）。每个文件都断言**恰好少 3 字节**，
`git diff --numstat` 每个都是 `1 1`。

> **过程中我自己踩了一次坑并当场发现**：第一次用字节切片
> `$b[3..($b.Length-1)]` 剥 BOM，把 `server_email_invoice.go` 写坏了 ——
> 顺手删掉 5 行真实内容（`currency` / `amounts` 两个响应字段和它们的注释），
> 文件从 14921 变成 14635 字节。是 `git diff` 的 numstat 1/6 露出来的，
> 不是编译错误 —— **少两个 JSON 字段编译照过，接口只是悄悄变了**。
> 回滚后改用「ReadAllText + UTF8Encoding(false) 重写 + 断言字节差恰为 3」，
> 每次都验。写进这一节是因为那 5 行如果没被 numstat 抓到，就是一个上线后
> 才发现的响应字段缺失。

### 护栏：BOM 扫描器 + 扫描器自身的对照

新增 `backend/internal/server/bom_guard_test.go`：

- `TestNoGoFileHasUTF8BOM` — 从包目录向上找 `go.mod`，扫全模块所有 `.go`，
  报出以 `EF BB BF` 开头的文件，并给出可直接粘贴的修法命令。
  **找不到 go.mod 时 fail 而不是 skip**：Go module 里的包必然在模块根之下，
  找不到就说明结构不对，那种情况下静默跳过正好会放行一个已经坏掉的仓库。
- `TestFindBOMFilesDetectsBOM` — 临时目录里放带 BOM / 不带 BOM / 嵌套目录
  / 非 .go 四种文件，必须只报出该报的。**没有这条，上面那条护栏可能因为
  「什么都没扫到」而永远绿。**

负控：给 `server_email_pipeline.go` 塞回 BOM → 护栏转红并指名道姓报出该文件
→ 移除后恢复绿。

BOM 的来源是环境默认行为（PowerShell 5.1 的 `Set-Content -Encoding UTF8`
与 `>` 重定向都写 BOM），所以必须机器拦，靠人记不住。

### 顺带：需求 2/3/5 的服务端 HTTP 层覆盖近乎为零

覆盖率可测之后第一件事就是看邮件相关的 server 文件，结果：

| 文件 | 覆盖 | 未覆盖块 | 对应需求 |
|---|---|---|---|
| `server_email_invoice.go` | **0%** | 138/138 | 需求 2/3/5 的全部 HTTP 端点 |
| `server_email_classify.go` | **0%** | 38/38 | 分类端点 |
| `server_email_invoice_file.go` | **0%** | 38/38 | 发票文件服务 |
| `server_email_purge.go` | **0%** | 14/14 | 软删端点 |
| `server_email_pipeline.go` | **7.3%** | 152/164 | 需求 1/3/4/6 手动触发端点 |
| `server_email_summary.go` | 38.5% | 40/65 | 每日摘要 |
| `server_email_classify_gateway.go` | 46.8% | 25/47 | AI 分类网关 |

**`internal/server` 整体 43.7%**（这个数字在此之前不存在）。
`internal/email` 63.5%。

也就是说：业务逻辑（`internal/email`）测得不少，但**需求 2/3/5 的 HTTP 接线
层一行都没测过** —— 而「端点有没有接对 store、scope 有没有带上、字段有没有
漏」恰恰是这轮已经踩过两次的坑类型（`ShareDocCSV` 被丢进 `_`、
`a0266a4` 因为门禁短路而零保护）。这块已在 §7dd 补上。

### 回归

`go build ./...` 干净；`config` / `feishu` / `email`(80.9s) 全绿；
`server` 11.97s，只剩那两个既有失败（`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，
§7az 的 404/403 语义分歧，非邮件分支、非本轮引入）。

---

## §7dd 需求 2/3/5 的 HTTP 接线层：把「落到哪个 handler」从读代码变成可证（2026-10-02）

§7dc 末尾列的缺口已补：`backend/internal/server/server_email_invoice_dispatch_test.go`，
4 个测试函数（其中 nil-store 分派表是 18 条逐条列出的子用例）。

### 接线层真正要挡的是什么

`handleEmailInvoiceDispatch`（`server_email_invoice.go:80`）的 switch 有一条**顺序依赖**：

```go
case rest == "export":                    // 精确相等
...
case strings.HasPrefix(rest, "export/"):  // 前缀
```

把第一条从 `==` 改成 `HasPrefix`（一个看起来完全合理的重构），`export/download`
（下载端点）就会被前一条抢走、变成去**生成**新 PDF。这类 bug 在真实链路上表现为
「下载按钮点下去生成了一个新文件」—— 功能看起来还在跑，不会有人从日志里发现。

### 为什么必须分两部分测

`Server.emailStore` 是**具体类型** `*email.Store`，不是接口，所以只有 nil 和真库两种
状态。实测下来：

- **nil-store 部分**能钉住每道守卫的**顺序**，但**证明不了分派**。extract/harvest/
  export/push 四个都回 `405 "POST only"`；ops/file/thumb 三个都回
  `503 "email store not configured"`。同一个二元组 = nil 状态的表达力上限。
  （我第一版给 nil-store 用例加了一条「签名必须互不相同」的断言，结果 9 条直接转红
  —— 那条判据本身是错的：它要求一个真实且正确的实现去满足一个不成立的条件。已删。）
- **真 store 部分**（自建隔离 schema）给每个 handler 造**唯一**签名，这才是分派的证明：

| 子路径 | 响应 |
|---|---|
| `extract` | 404 `email not found` |
| `harvest` | 503 `email fetcher not configured (IMAP unavailable)` |
| `export` | 400 `no harvested invoice files in selection` |
| `export/download` | 404 `export not found` |
| `{id}`（不存在） | 404 `invoice not found` |
| `{id}`（存在） | 200 + 发票 JSON |
| `{id}/file` | 200 + PDF 字节 + 规范文件名 |
| `{id}/thumb` | 404 `thumbnail unavailable` |

最后三行用同一条记录、同一套 URL 形态，靠**响应体形态**分开，不是靠错误消息。

### 负控（实测）

`case rest == "export"` → `strings.HasPrefix(rest, "export")`：
两个测试函数**同时**转红，共 4 条断言报出 `export/download` 被劫持。
还原后 `git diff --numstat` 为空（逐字节验过，不是「记得改回去了」）。

### 顺带查出的三件事

1. **建档要串三层 FK**：`email_invoices.email_id` → `emails.id` → `email_accounts.id`。
   测试里不给真邮件行，`UpsertInvoice` 直接 23503。这是条隐含约束：发票不可能脱离
   真实邮件凭空存在。
2. **`UpsertInvoice` 刻意不写采集列**（`invoice_store.go:94` 的 INSERT 列表里没有
   `file_name`/`file_path`/...），所以它**返回的结构体里的 `FilePath` 是内存回显，
   不是读回来的值**。落盘路径必须另走 `UpdateInvoiceHarvest`。调用方若以为
   「upsert 完读回自己的对象就是库里的状态」，会拿到一个库中不存在的事实。
3. **守卫顺序在同族里不一致**（已全部钉进用例）：
   - `handleEmailInvoices` / `handleEmailInvoiceOps` / `loadScopedInvoiceFile`：
     **先查库**。于是无库实例上「用错方法」的 405 **永远出不来**，
     「缺 invoice id」的 400 也永远出不来。
   - `handleEmailInvoiceExport`：**先查 dataDir**。
   这不是 bug，但意味着「方法不对」这个错误在降级配置下会被静默换成一个
   看起来无关的 503。

### 【新发现·低危，未修】pdfcpu v0.11.0 在畸形 PDF 上 panic

`GET /api/emails/invoices/{id}/thumb` → `ExtractInvoiceThumb` → `firstPDFEmbeddedImage`
→ `api.ExtractImagesRaw` → pdfcpu `model.skipStringLit`（`pkg/pdfcpu/model/parse.go:1273`）
**`panic: slice bounds out of range [-1:]`**。

- 触发输入：结构不完整的 PDF（我最初手写的 `%PDF-1.4 ... %%EOF` 裸字节）。
  换成 gofpdf 生成的合法 PDF 就不炸。
- 影响面：`ExtractInvoiceThumb` 全仓只有一个调用点（thumb handler），生产上有
  `recoveryMiddleware`（`middleware.go:67`）兜底，所以是 **500 而不是 404
  `thumbnail unavailable`**，不会拖垮进程。
- **未修**：修法要么在 `firstPDFEmbeddedImage` 外面加 `recover`，要么给 pdfcpu
  上游提 issue。前者是一行防御，后者是依赖决策 —— 都不该我单方面定，故留待拍板。
  测试里用合法 PDF 绕开，不把这个 bug 钉成「预期行为」。

### 覆盖率变化

| 文件 | 之前 | 现在 |
|---|---|---|
| `server_email_invoice.go` | 0%（138/138） | 43/231 = **18.6%** |
| `server_email_invoice_file.go` | 0%（38/38） | 21/64 = **32.8%** |
| `server_email_pipeline.go` | — | 43/285 = **15.1%** |
| `internal/server` 整包 | 43.7% | **44.7%** |

函数级亮点：`handleEmailInvoiceDispatch` 100%、`atoiSafe` 100%、
`handleEmailInvoiceFile` 86.7%、`handleEmailInvoiceExportDownload` 78.6%、
`invoiceFileAbs` 71.4%。

### 仍然没覆盖（诚实记录）

- `handleEmailInvoiceExport` 的成功路径（真实多文件 2x2/3x3 拼版）—— 需要真实
  已采集文件 + pdfcpu 渲染，本轮没造。
- `handleEmailInvoiceHarvest` / `handleEmailInvoicePush` / `handleEmailInvoiceSummary`
  的**成功**路径 —— 分别需要 IMAP fetcher、飞书凭据、pipeline 实例，前置条件都未提供。
- `extractInvoicesAsync`（0%）—— fire-and-forget goroutine，需要 scheduler。
- `handleEmailInvoiceOps` 的 PATCH/DELETE 成功分支（41.9%）。

### 回归

`internal/server` 全包 18.0s，**只剩那两个既有失败**
（`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，§7az 的 404/403 语义分歧，
非邮件分支、非本轮引入），无新增失败。

---

## §7de 分类 / 清理两个写操作端点：0% → 可证，并查出两条会静默失效的语义（2026-10-02）

`backend/internal/server/server_email_write_ops_test.go`，8 个测试函数。

选这两个文件的原因：它们比发票那组更危险，因为**改数据**。classify 把分类结果
写回 `emails` 行，purge 软删邮件**并删掉磁盘上的正文缓存**。接线错一个字符的代价
是「邮件被误分类」或「正文文件被删」，而两者都不会报错。

### 覆盖率（本轮实测）

| 函数 | 之前 | 现在 |
|---|---|---|
| `handleEmailClassify` | 0.0% | **92.9%** |
| `classifyOneEmail` | 0.0% | **100.0%** |
| `classifyViaKxmemory` | 0.0% | **77.8%** |
| `classifyViaGateway` | 0.0% | **67.8%** |
| `emailClassifyModel` | 0.0% | **80.0%** |
| `firstNonEmptyStr` | 0.0% | **100.0%** |
| `handleEmailPurge` | 0.0% | **90.5%** |

文件级：`server_email_classify.go` 0% → **51.5%**（34/66）；
`server_email_classify_gateway.go` → **39.1%**（36/92）；
`server_email_purge.go` 0% → **61.9%**（13/21）。
`internal/server` 整包 44.7% → **45.4%**。

### 【待拍板】`cfg.LLMModel` 对邮件分类是**无效配置**

`emailClassifyModel`（`server_email_classify_gateway.go:107`）取网关
`PreferredModels` 的第一项，`cfg.LLMModel` 只是兜底。而
`defaultLLMGatewayState()` 在**没有任何运行时配置**时也会把
`opencode.DefaultLLMGatewayPreferredModels` 整份填进 `PreferredModels`，
那是**非空的硬编码默认**。于是：**默认部署下 `cfg.LLMModel` 这条兜底永远走不到**，
分类模型实际由网关 preferredModels 决定（当前是 `glm-5.2`）。

实测方式：我在回退用例里断言「模型 = cfg.LLMModel」直接转红，拿到的是 `glm-5.2`。
已由 `TestEmailClassifyModel_PrefersGatewayOverCfg` 钉住 —— 这条钉住很重要，
因为该列表同时是 auto 模式的降级链顺序（`config_writer.go:53` 的注释明说了），
**改列表会静默换掉邮件分类模型，且改之前不会红**。

是否要让 `cfg.LLMModel` 真正生效，是产品语义，不擅自改。

### 【待拍板】显式指定 ids 会被**静默截断**成「ids ∩ 最新 ≤20 页」

`handleEmailClassify` 的过滤是「先 `ListUnclassifiedScoped(limit)` 取**最新一页**
（`ORDER BY e.date DESC LIMIT ≤20`），再用 `CapClassifyIDs(ids, limit)` 建的
allow 集合去取交集」（`server_email_classify.go:55-72`）。后果：

- 指定 1 封**很老的**未分类邮件 → `classified: 0`、无错误、无提示。
- 不指定 ids → 正常分类最新 20 封，`remaining` 正确（实测 25 封 → 分类 20、剩 5）。

需求上 classify 是「自动归纳整理」的入口，不报错但没整理，运维很难发现。
已由 `TestClassify_ExplicitIDsAreIntersectedWithNewestPage` 把当前行为钉住
（并附「行为已变请更新本用例」的提示）。要不要改成真按 ids 取，是待拍板项。

### 负控（实测）

1. 把 ids 过滤关掉（`len(body.IDs) > 0 && false`）
   → `TestClassify_ExplicitIDsAreIntersectedWithNewestPage` 转红。**这一条顺带量出了
   blast radius**：指定 1 封邮件会变成**分类 20 封完全不相关的邮件**。
2. 把 purge 的 `if s.dataDir != ""` 反过来并换成空 dataDir
   → `TestPurge_DeletesCachedBodyFiles` 转红（正文缓存没删）。
3. 把 `classifyOneEmail` 的 `if s.kxmemory != nil` 换成 `if true`
   → 见下，这条**第一次没转红**。

### 负控 3 不转红的原因（第三种：结论本身错了）

我把负控 3 对准了 `TestClassify_FallsBackToGatewayWhenKxmemoryFails`，结果**全绿**。
排查后确认：该用例本来就把 `kxmemory` 设成了 fake，所以
`if s.kxmemory != nil` 本来就是 true，这个变异对它是 **no-op**。

改查 `TestClassify_ExplicitIDsAreIntersectedWithNewestPage`（那个 Server 只有
`llm`、`kxmemory` 为 nil）→ **直接 nil pointer panic**。所以 nil 守卫**是**被覆盖的，
只是我一开始挑错了用例。

这也是「不转红不等于判据太松」的又一例：先怀疑结论，比先改判据更省事。

### 顺带钉住的三件事

1. **kxmemory 优先于网关**（`TestClassify_PrefersKxmemoryOverGateway`）。重要是因为
   `POCKET_KXMEMORY_BASE_URL` 没配，生产上 kxmemory 这条腿**从未跑过**；只验
   「调不通时退回网关」不足以说明「调得通时它是对的」。
2. **分类结果的 `EmailID` 取自请求、忽略分类器响应里自带的 id**
   （`classifyViaKxmemory` 的 `out := classifyResultJSON{EmailID: it.ID}`）。
   我最初断言反了（以为取自响应），转红后读代码才发现 —— 这是**更安全**的行为：
   分类器认错了邮件也不会把结果写到别人的行上。fake 故意返回
   `"from-kxmemory"` 就是为了钉住这一点。
3. **purge 的越权不变量**：换成别的 user 的 claims 打同一端点，实测 `purged: 0`、
   正文缓存文件保留、原邮件未标记；再用本人 claims 打则 `purged: 1`。
   走的是真 handler（claims 注入 request context，与 `requireAuth` 一样），
   不是直接调 store —— 后者只能验 SQL 的 WHERE，漏掉「handler 传错 userID」
   这类更常见的接线错误。走真 handler 的代价是必须再验一次本人能删成功，
   否则「越权删不掉」可能只是「端点根本没在工作」。

### 仍然没覆盖（诚实记录）

- `classifyViaGateway` 的错误分支：LLM 报错、模型输出不可解析、`SetClassification`
  写库失败。写库失败需要构造 store 故障，本轮没做。
- `classifyViaKxmemory` 的「空结果」分支（`resp.Results` 为空）。
- purge 的 `SoftDeleteEmailsScoped` 报错分支。

### 回归

`internal/server` 全包 14.7s，**只剩那两个既有失败**
（`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`），无新增失败。
两个生产文件（`server_email_classify.go` / `server_email_purge.go`）在负控后
`git diff --numstat` 为空，逐字节验过。

---

## §7df 需求 1 的定时入口与三个外部适配器：0% → 可证，并钉住「预演开关不泄漏」这个安全不变量（2026-10-02）

`backend/internal/server/server_email_pipeline_adapters_test.go`，9 个测试函数。

### 为什么这块优先级高

`handleEmailPipelineRun` 是「每天定时或手工进行邮件接收，然后进行处理」里
**手工**那一半的入口，此前 0% 覆盖。而它身上挂着一个安全性不变量
（`server_email_pipeline.go:261-266`）：

```go
if spamOverride != nil && *spamOverride != p.SpamDryRun {
    prev := p.SpamDryRun
    p.SpamDryRun = *spamOverride
    defer func() { p.SpamDryRun = prev }()
}
```

**一次 `dryRunSpam:false` 就是对真实邮箱执行不可逆的 IMAP MOVE。**
若这个覆盖泄漏到下一轮，用户某次调试按了「真实执行」之后，每天 06:00 的定时
任务都会真的搬邮件，而没人会去看配置项是否被改过。这条此前只靠一行注释守着。

### 覆盖率（本轮实测）

| 函数 | 之前 | 现在 |
|---|---|---|
| `handleEmailPipelineRun` | 0.0% | **100.0%** |
| `RunEmailPipeline`（scheduler 入口） | 0.0% | **100.0%** |
| `runEmailPipeline` | 0.0% | **94.7%** |
| `ensurePipeline` | 18.2% | **95.5%** |
| `ensureInvoiceHarvester` | 0.0% | **80.0%** |
| `feishuInvoicePusher.Available` | 0.0% | **100.0%** |
| `feishuLedgerPublisher.Available` | 0.0% | **100.0%** |
| `feishuLedgerPublisher.PublishedURL` | 0.0% | **100.0%** |
| `feishuLedgerPublisher.RememberPublished` | 0.0% | **100.0%** |
| `notifycenterEmailNotifier.NotifyImportantEmail` | 0.0% | 18.2%（只钉 nil 守卫） |

`server_email_pipeline.go` 文件级 43/285 → **84/285 = 29.5%**；
`internal/server` 整包 45.4% → **45.9%**。

### 钉住的行为事实

1. **预演开关不泄漏**（核心）。`SpamDryRun=true` 起手，手动跑一轮
   `dryRunSpam:false`，之后必须恢复成 `true`；再跑一轮不带覆盖，仍是 `true`。
   `RunEmailPipeline`（定时入口）额外钉住它**不传覆盖** —— 定时路径不该有
   能力改预演开关。
2. **什么都没配时端点回 HTTP 200**，错误只在 body 的 `errors` 数组里
   （`"email pipeline not configured"`）。不是 bug，但只判状态码的调用方
   会以为跑成功了。已把「200 + errors 非空」这个组合钉住。
3. **台账发布器的 `Available` 不需要 chatID**（建表只用 app 凭据），而
   pusher 的 `Available` 需要 —— 三条件缺一不可，其中 chatID 最容易被忘
   （app_id/secret 都配了就是不发）。两条分开钉，免得重构时被「统一」掉。
4. **未配置时 `PublishLedger` 必须返回 error**，不能是空串+nil，否则调用方
   会把「没配」当成「发布成功但没链接」。
5. **台账链接记忆按 (workspace, user) 隔离**；空 URL 不得覆盖已有链接；
   nil 接收者安全。
6. **`ensureInvoiceHarvester` 缺任一依赖（store/fetcher/dataDir）都返回 nil**，
   四种缺法各测一遍。附带纠正一处我自己的误解：它**每次都新建实例**，
   被单例缓存的是 `Pipeline` 而不是 harvester；「共用」指共用构造函数与配置。

### 【已查清的行为，非缺陷】飞书未配置时，发票推送**静默跳过**，报告里看不出来

`internal/email/pipeline.go:836` 的 `pushInvoiceSet`：

```go
if p.Pusher == nil || !p.Pusher.Available() {
    return          // ← 无 error、无计数
}
```

于是报告是 `FeishuPushed=0 / FeishuFailed=0 / Errors=[]` —— 与「有发票但都推
成功了」的三个数字**完全一样**，唯一线索是启动时那一行 log。

**影响**：需求 3「发送到飞书」在当前部署（`POCKET_FEISHU_INVOICE_CHAT_ID`
未提供）里是**结构上跑不起来的**，而报告看不出来。验收时不能只看报告数字。

需求允许「发不出去就建共享文档兜底」，所以**降级本身是设计内的**；缺的是
「跳过」这件事在报告里可见。加一个 skipped 计数或 Reason 字段是产品语义，
留待拍板，这里只把现状钉住。

### 负控（实测）

把 `defer func() { p.SpamDryRun = prev }()` 改成恢复成 `*spamOverride`
→ `TestRunPipeline_DryRunOverrideDoesNotLeak` 转红（两条断言）。

**第一版负控直接删掉那一行，结果 `prev` 变成未使用变量、编译失败。**
负控必须能编译，否则「build failed」会被误当成测试通过或护栏问题。

### 一条**没有**负控的用例，及原因（不假装它有效）

`TestPipeline_FeishuSkipIsInvisibleInReport` 断言「飞书未配置时报告里三个 0」。
我原计划的负控是去掉 `!p.Pusher.Available()` 短路，但隔离 schema 里**没有任何
发票**，`pushInvoiceSet` 的循环体一次都不执行 —— 无论短路在不在，报告都是那
三个 0。**这条负控在当前夹具下不可能转红。** 要给它配负控，需先在隔离 schema
放一条 `status=downloaded`、`feishu_sent_at=0`、`FilePath` 指向真实文件的发票；
那会让流水线去真的发网络请求，属于另一轮的工作。

### 仍然没覆盖（诚实记录）

- `feishuInvoicePusher.PushInvoice` 0% —— 需要真实飞书凭据 + 网络。
- `feishuLedgerPublisher.PublishLedger` 16.7%（只覆盖了未配置那一支）。
- `notifycenterEmailNotifier.NotifyImportantEmail` 18.2% —— 只钉了 nil 守卫，
  真实 `Dispatch` 需要构造 notifycenter.Service。
- `handleEmailInvoicePush` 18.9% / `handleEmailInvoiceSummary` 21.2% 的成功路径。

### 回归

`internal/server` 全包 19.2s，**只剩那两个既有失败**
（`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`），无新增失败。
`server_email_pipeline.go` 在负控后 `git diff --numstat` 为空，逐字节验过。

---

## §7dg 【需求 8】LWW 链路逐段查清：实现是对的，但本地时间戳单位混存（2026-10-02）

需求 8「配置有最后修改时间，服务端与客户端以最后时间为准」是这批需求里**唯一
一条服务端与客户端都实现了的机制**，但此前从没有人把这条链路从头到尾验过一遍。
本轮逐段查了五跳，结论是**实现正确**，只在最后一跳发现一个单位混存。

### 五跳逐段核对（全部实测，不是读印象）

1. **契约**：`email.Account.UpdatedAt` 带 `json:"updatedAt"`（`model.go:29`，
   注释明写「配置的最后修改时间（Unix 秒）」）。客户端 `EmailAccount.updatedAt`
   声明为 Unix 秒（`api/email.ts:29-30`）。两侧单位声明一致。
2. **增量拉取**：`handleEmailAccounts` GET → `filterEmailAccountsSince`
   （`server_since.go:110`），`parseSinceQuery` 把毫秒输入归一成秒，
   `stampAfterSince` 容忍秒/毫秒混存。秒/毫秒混用在这里是**被处理掉的**。
3. **服务端 LWW 守卫**：`UpdateAccountLWTScoped`（`store.go:1583`）用
   `WHERE ... AND updated_at <= $base` 做乐观并发，并保证
   `next = max(now, base+1)` 单调递增（否则秒级时间戳可能与旧值相同，
   客户端下一轮会误判「没变过」）。`ErrStaleWrite` 与 `ErrNotFound` 分开返回。
4. **HTTP 映射**：`server_assistant.go:1226-1236` 把 `ErrStaleWrite` 映射成
   **409 + 当前 updatedAt**（客户端可直接覆盖本地镜像），`ErrNotFound` → 404。
5. **客户端上行**：`account-sync.ts:130` 显式用**本地**的 `l.updatedAt` 作基准，
   代码里还写了注释说明「用 target 的 updatedAt 会让守卫永远放行」；
   收到 409 走下行覆盖而**不进 outbox 干等**（`account-sync.ts:160-166`）。

**结论：这条链路没有「看起来是 LWW、实际是 last-arrival-wins」的问题。**
服务端守卫生效，客户端传对了版本号，409 的处理也是对的。

### 【已实测的缺陷】`local_email_accounts.updated_at` 单位混存：秒与毫秒

- `emails-store.ts:78` 的 `saveAccount`：`const now = Date.now()` → **毫秒**，
  写进 `created_at` 与 `updated_at`。
- `emails-store.ts:126` 的 `updateAccount`：`Math.floor(Date.now()/1000)` → **秒**。
- 下行写入（`account-mirror-write.ts:37/49`）用的是服务端值 → **秒**。
- 读回（`emails-store.ts:431` 的 `rowToAccount`）：`updatedAt: r.updated_at ?? 0`，
  **不做任何归一**。

于是同一列里既有秒也有毫秒。实测后果（用 `planAccountSync` 真实函数跑的，
不是推演）：

```
server updated_at (s) = 1789480000
client updated_at (ms)= 1789480000000
plan = {"pullIds":[],"pushIds":["acc-1"]}
push base sent to server = 1789480000000
guard accepts stale write? (stored<=base) = true      ← 守卫被架空
```

服务端守卫是 `updated_at <= base`。客户端用毫秒当基准时，基准比服务端现存值大
约 1000 倍，**无论服务端那份是不是更新的，这个写都会被接受**。这正是需求 8
要防的「旧的一方覆盖新的一方」。

**但影响面比第一眼看上去窄，必须说清楚**：`saveAccount` 不是主路径。
`EmailAccountSetup.vue:430-456` 里它是 `emailApi.addAccount` 失败
（404 或 ≥500）后的**回落**。所以触发条件是三条同时成立：
① 账户是在云端 API 不可用时于设备上创建的；
② 之后同一邮箱在服务端也存在了（否则 `planAccountSync` 配不上，`pushIds` 为空，
毫秒值没有出口）；
③ 之后发生一次上行。

**未修**，两个原因：一是修法涉及「本地已写入的历史行要不要回填」，
属于数据迁移决策；二是主路径正确，只补回落路径可以按最小改动做。
候选修法（两条都做才彻底，因为存量行已经在库里）：
写时 `saveAccount` 改用秒；读时 `rowToAccount` 对 `> 1e12` 的值除以 1000。

### 顺带记一条：`last_synced_at` 用毫秒是**对的**，不要一起「修」

`updateSyncState`（`emails-store.ts:334-338`）把 `Date.now()` 写进
`last_synced_at`，那列本来就是毫秒语义，且不参与 LWW 比较。
把单位混存当通病去全库统一，会把这一列改坏。

### 回归

`npm.cmd run test:email` 实测 **242 用例全绿、0 失败**（含
`account-lww-real.test.mjs` / `account-sync.test.mjs` /
`account-mirror-write.test.mjs` 三份需求 8 用例）。本节**未改任何生产代码**，
取证用的临时探针已删除（`git status` 干净）。

---

## §7dh 修掉 §7dg 的单位混存：写侧改秒 + 读侧归一，两侧都要（2026-10-02）

`frontend/src/features/email/account-lww.ts` 新增 `normalizeAccountStamp`，
`emails-store.ts` 的 `saveAccount` / `rowToAccount` 改用它，
新增 `__tests__/account-stamp-units.test.mjs`（6 用例）。

### 为什么放在 account-lww.ts 而不是就地写

这个模块存在的理由就是「判定逻辑必须能被纯 node 测试 import」——`emails-store.ts`
import 了 `native/local-db`，在 node 里根本解析不了。早年的教训正是：测试为了
能跑，**在文件里复制了一份判定逻辑**，测试全绿但测的不是产品代码。归一函数放进
同一个零依赖模块，就是为了让「守卫测的」和「生产跑的」是同一份实现。

### 为什么必须**两侧**都改

只改写侧（`saveAccount` 用秒）救不了**已经写进库的历史行**：它们仍是毫秒，
读路径照样把毫秒喂进 `planAccountSync`。只改读侧则新增行还会继续写脏。
所以：写侧挡住增量，读侧覆盖存量。

### 用与服务端同一个阈值

`1e12` 分界，与服务端 `server_since.go` 的 `parseSinceQuery` /
`stampAfterSince` 一致。两侧对「这是秒还是毫秒」的判断不能有分歧，
否则就会出现「服务端认为该过滤、客户端认为该推」的错配。

### 用例设计

关键那条是**端到端**的：把归一后的值喂进真实的 `planAccountSync`，断言
`pushIds` 为空（归一后本地不再比服务端「新」）。同一条用例里还留了一段
**对照**——不归一时 `pushIds === ['acct-1']` 且 `stored <= base` 恒成立——
这样这条断言不是恒真，将来有人改坏归一逻辑能立刻看到差异。
另有一条反向用例（本地确实更新时仍要能上行），防止「归一顺手把上行也废了」。

### 负控（实测）

把 `normalizeAccountStamp` 改成 passthrough（`return v`，即修复前的效果）
→ **3 条转红**：毫秒换算、端到端那条、幂等那条。还原后 `git diff --numstat`
只含预期的三个文件。

### 一处**没有**负控，如实说明

`rowToAccount` 里那一句 `normalizeAccountStamp(r.updated_at)` 的**调用点**
没做负控 —— `emails-store.ts` 无法在 node 里 import（正是上面那个原因），
所以从 node 侧点不到这一行。现有保障是 typecheck + 该调用是一处字面量替换。
若要真正守住调用点，需要把 `rowToAccount` 也抽成纯函数，那是另一轮的事。

### 顺手记一条防误伤

`rowToAccount` 里 `lastSyncedAt` / `createdAt` **没有**跟着归一，这是对的：
`updateSyncState` 往 `last_synced_at` 写的 `Date.now()` 本就是毫秒语义，
且不参与 LWW 比较。代码里已就地写了注释，防止后来者「顺手统一」把它改坏。

### 回归

`npm.cmd run test:email` **248 全绿、0 失败**（242 → 248，新增 6）；
`npm.cmd run typecheck` 干净。

---

## §7di 修掉 §7dd 的 pdfcpu panic：缩略图路径补上导出路径早就定过的规矩（2026-10-02）

`backend/internal/email/invoice_file.go` 的 `firstPDFEmbeddedImage` 加 recover，
新增 `invoice_file_malformed_test.go`（4 用例）。

### 这条为什么从「等拍板」变成「直接修」

§7dd 记下 pdfcpu panic 时我把它列成待拍板，理由是「加 recover vs 给上游提 issue
是依赖决策」。补做需求 5 的核查时找到了决定性的反证——**这个仓库自己早就为
同一件事定过规矩，并且做了两遍**：

- `export_pdf.go:55-59` 的注释白纸黑字写着：畸形 PDF（只有 Catalog、没有页树，
  69 字节）会让 pdfcpu 的合并直接 panic（`slice bounds out of range [-1:]`），
  「因此这里做两件事：1. 每个 PDF 先 Validate，坏文件跳过；2. 兜底 recover，
  把 pdfcpu 的 panic 转成普通 error，**绝不让它冒到 handler**」。
- `exportNUp`（:91-95）和 `pdfPageCountSafe`（:193-198）里各有一处 recover，
  配套用例是 `TestExportInvoiceGrid_SkipsMalformedPDFAndKeepsGoodOnes`
  与 `TestExportInvoiceGrid_AllMalformedReturnsError`。
- 连函数签名都是为这件事准备的：`exportNUp` 用**具名返回值** `(res *GridExport,
  err error)`，正因为 recover 里要改 `err`。

也就是说这不是「新设计决策」，而是**缩略图这条路径当时漏掉了仓库既有的防御**。
于是补它属于「修漏」而不是「改语义」，待拍板项作废。

### 改法

照抄 `pdfPageCountSafe` 的形状：`firstPDFEmbeddedImage` 改具名返回值
`(img []byte, fileType string, err error)`，函数头加 recover，把 panic 转成
`fmt.Errorf("unreadable invoice pdf: %v", r)`。`ExtractInvoiceThumb` 原本就有
`if err != nil || len(img) == 0 { return nil, "", false }`，于是畸形发票的
`GET /api/emails/invoices/{id}/thumb` 从 **500 变回 404 thumbnail unavailable**。

### 用例

四条，形状刻意与导出那两条对齐：畸形 PDF 不 panic、截断/空/非 PDF 不 panic、
`firstPDFEmbeddedImage` 必须返回**非 nil error**（只断言「没 panic」会让
「返回 nil,nil,nil」的写法蒙混过关）、合法 PDF 仍然不报错（防 recover 误伤）。

### 负控（实测）

去掉 recover → 2 条转红，报出的正是
`runtime error: slice bounds out of range [-1:]`。

**第一版负控没编译**：`fmt` 变成未使用导入。这已是本轮第二次撞上
「负控必须能编译」，改成 `_ = fmt.Sprint(...)` 保引用后才跑通。

`TestExtractInvoiceThumb_TruncatedPDFDoesNotPanic` 在负控下**仍绿**，
这是预期的：那些输入在 `DetectInvoiceMedia` 就被拒了（没有 `%PDF` 头），
根本进不到 pdfcpu。如实记下来，免得被当成「这条用例没守住什么」。

### 回归

`internal/email` 全包 **9.675s 全绿**，无新增失败。

---

## §7dj 需求 5 的 HTTP 成功路径：产物落盘 / url 可下载 / exported_at 只给真正入网格的票（2026-10-02）

`backend/internal/server/server_email_invoice_export_test.go`，5 个测试函数。

需求 5 的**核心算法**（`internal/email/export_pdf.go`）覆盖得相当扎实——页数取整、
多页源、图片发票混入、非法 grid 拒绝、畸形件跳过、裁切线用「不画线对照产物」
的字节差证明真被画进去。缺的是**接线层**：`handleEmailInvoiceExport` 此前 35.3%，
也就是「文件真落盘、返回的 url 真能下载、exported_at 真被记上」从来没端到端跑过。

### 覆盖率（本轮实测）

| 函数 | 之前 | 现在 |
|---|---|---|
| `handleEmailInvoiceExport` | 35.3% | **79.4%** |
| `handleEmailInvoiceExportDownload` | 78.6% | **100.0%** |

`internal/server` 整包 45.9% → **46.1%**。

### ★ 本轮守住的核心不变量

`server_email_pipeline.go:404-406` 写着：

> 记录导出时间 + 通知前端刷新。只给**真正进入网格**的票打时间戳：
> 被跳过的坏文件不能算「已导出」，否则发票页会显示一张根本没导出的票已归档。

这是「注释声明的不变量」。验法：混合清单塞两张合法 + 一张畸形 PDF（畸形件在
handler 的 stat 阶段能过——文件存在——要到 `pdfPageCountSafe` 才被跳过），
断言两张好票 `exported_at > 0`、坏票 `exported_at == 0`。

**顺带查清了它凭什么成立**：`skipped` 集合是按 `filepath.Base(f)` 建的
（`export_pdf.go:173` / `:181` 两条分支都是），而 handler 的过滤也是
`skipped[filepath.Base(f)]`，键一致才对得上。我原本怀疑这里键不匹配（若 `Skipped`
装的是归一后的临时名 `inv-*.pdf`，过滤就会全部落空、坏票照样被打上时间戳），
读代码确认不是——**这条注释是真的在生效**，不是碰巧。

### 另外四条

- `grid=3` 被接受，且**产物文件名里含 `3x3`** —— 证明排版参数真的传到了算法层。
- 非法 grid（1/4/-1）被拒 —— 核心算法层已有覆盖，这里钉的是接线层有没有透上来。
- **重复导出不互相覆盖**：连导 3 次，产物文件名 3 个不同。`export_pdf.go:142-143`
  的注释点名说要用纳秒就是为了防这个撞名，实测成立。
- 9 张 / 2x2 ⇒ **3 页**，用 `api.PageCountFile` 数（不是字节扫描——输出 PDF 里
  嵌着源发票的 Form XObject，扫出来的第一个页对象是内层的）。

### 负控（实测，两条）

1. 把 skipped 的按名过滤去掉（无条件 `MarkInvoiceExported`）
   → `TestInvoiceExport_SuccessPathMarksOnlyUsableInvoices` 转红，报出
   `被跳过的坏票被打上了 exported_at=1790897974`（带真实时间戳）。
2. 把 `body.Grid == 0 → 2` 的缺省改掉
   → 同一条转红，`export => (500, {"error":"grid must be 2 (2x2) or 3 (3x3), got 0"})`。

还原后 `git diff --numstat` 为空，`server_email_pipeline.go` 逐字节未改。

### 一处已知的低危边界（不单独立项，挂到命名/去重那一条下）

`skipped` 按**文件名**匹配，所以两张不同目录、但**同名**的发票里，
若其中一张畸形，另一张会被一并当成「已跳过」而**不**记 `exported_at`。
方向是保守的（少记，不会多记），不产生错误归档。
而发票命名规则 `{费用类型}-{对方单位}-{金额}-{日期}.pdf` 天然会撞名——
这正是「发票命名/去重方案」那条待拍板项的一部分，不重复立项。

### 回归

`internal/server` 全包 18.1s，**只剩那两个既有失败**
（`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`），无新增失败。

---

## §7dk 「跨币种求和不是金额」的第**四**处实现：汇总端点的 amountTotal（2026-10-02）

`internal/email/ledger.go` 新增 `SumByCurrency`，
`internal/server/server_email_pipeline.go` 的 `handleEmailInvoiceSummary` 改用它，
新增 `server_email_invoice_summary_test.go`（5 用例），
`frontend/src/api/email.ts` 的 `EmailInvoiceSummary` 补上 `currency` / `amounts`。

### 这条规则已经被修过三次，第四次在审计范围外

「跨币种的算术和不是金额」在仓库里有三处实现，且三处都配了用例：

1. `email.LedgerRows`（`ledger.go`）
2. `email.WriteInvoiceSummaryDocs`（`pipeline.go`）
3. `email.InvoiceListStats`（`invoice_list.go`，SQL 层聚合——最容易骗过人，
   `invoice_list_stats_test.go:9` 自己就写着「这是同一条规则的前两处已修，第三处」）

2026-10-01 修第三处时，**审计范围只在 `internal/email`**，
server 层那处手写求和没被看到：

```go
var total float64
for _, inv := range invoices {
    total += inv.Amount      // ← 100.00 USD + 50.00 CNY = 150
```

而且它是四处里**唯一**把标量直接交给前端的。另外三处要么返回 `[]CurrencyTotal`
（`LedgerRows` 的调用点还故意写成 `rows, _ :=` 丢弃它，逼下一个人必须自己按币种处理），
要么把多币种时的标量置 0。

### 实测的影响面（如实，不夸大）

**前端没有任何地方读 `amountTotal`。** `api/email.ts` 里只有类型声明；
发票页合计区走的是客户端自己的 `summaryMoney(sumByCurrency(list))`
（`use-invoice-list.ts:82`）。所以这个错数**当前不显示**——
按「闸门逻辑上一直关着、目前没造成损失」记，而不是「已造成错账」。

但它是个**已声明的 API 字段**（`amountTotal: number`），任何人接上它就会拿到
错的账，而且 150 这种数连币种标签都没有。实测负控还发现更糟的一点：
裸求和的 `currency` 会被赋成**最后遍历到的那一组**的币种，于是响应变成
`amountTotal=150, currency="USD"` —— 一个有明确币种标签的错数，
比没有标签的错数更容易让人信。

### 改法与为什么不用现成的 `InvoiceListStats`

`InvoiceListStats` 已按币种分组，但它返回**未导出**类型，且走 SQL 全表聚合。
汇总端点本来就持有发票切片（要输出 `rows`），按切片聚合的好处是
**`count` 与合计来自同一份数据**，不会一边被 `ListInvoicesScoped(..., 500)`
截断、一边是全量。

顺带记一个**已存在但不在本轮范围**的既有不一致：`count` 用 `len(invoices)`
（500 上限），若发票超过 500 张，`count` 会被截断而金额不会。单独立项价值低，
记在这里备查。

### 用例

五条：多币种时 `amountTotal` 必须为 0 且 `currency` 为空、`amounts` 两组齐全；
单币种时标量仍可用（否则前端全要改）；空币种归 CNY；零张发票不 panic；
以及**汇总端点真的生成了共享文档**（CSV + MD，文件存在且非空）——
这条在当前部署尤其重要，因为飞书没配，**这条路就是实际生效的那条**。

### 负控（实测）

把 `len(amounts) == 1` 的条件去掉，改成遍历分组裸求和
→ `TestInvoiceSummary_MultiCurrencyHasNoCrossCurrencyTotal` 转红，报出
`amountTotal = 150，多币种时它必须是 0（150 不是金额）` 与
`currency = "USD"`。

`TestInvoiceSummary_SingleCurrencyKeepsScalar` 在该负控下**仍绿**（单币种求和
与分组求和结果相同）——这正是「哪些取值躲过了变异」的典型，如实记下。

### 回归

`internal/email` 9.758s 全绿；`internal/server` 23.0s 只剩那两个既有失败；
`npm.cmd run typecheck` 干净。

---

## §7dl 需求 1 的高危待拍板项：把「要不要加白名单」变成带数字的选择题（2026-10-02）

`monitor.aliyun.com` 是否加进 `spamDomainWhitelist` 这条一直挂着，它挡住
`POCKET_EMAIL_SPAM_DRYRUN=false`。本轮把**决策所需的数字全部量出来了**，
未改任何生产代码（`git diff --numstat` 为空）。

### 现状（实测，测量时刻 07:50:55；运行中的 pocketd 每分钟在改库）

120 封邮件里 **6 封**会判垃圾，全部 100 分，理由都只有「退订特征:取消订阅」：

| 发件人 | 封数 | 分数 | 命中原因 |
|---|---|---|---|
| `monitor@monitor.aliyun.com`（【阿里云】云安全中心周报） | **4** | 100 | 页脚「取消订阅」 |
| `InfoQChina@edm.infoq.com.cn`（QCon 上海推广） | 2 | 100 | 页脚「取消订阅」 |

那 4 封阿里云周报的内容是**安全告警**（含「已发现 N 条安全风险 / 待处理漏洞 M」），
它们进垃圾箱的**唯一**理由是云厂商邮件模板页脚里的取消订阅链接。

### 两个选项的实测差值

把 `"monitor.aliyun.com"` 临时加进白名单重跑探针：

| | 判垃圾封数 | 结果 |
|---|---|---|
| **A. 不加**（现状） | **6** | 4 封安全告警 + 2 封 QCon 推广 被移入垃圾箱 |
| **B. 加 `monitor.aliyun.com`** | **2** | 只有 2 封 QCon 推广被移；4 封安全告警留在收件箱 |

加完还原后复测回到 6，确认测量本身没留下痕迹。

### 顺带查清的一件事：白名单是**子串**匹配，不是后缀

`spam.go:134` 是 `strings.Contains(domain, w)`。两个后果：

1. 加 `"aliyun.com"` 会**一并**豁免该域下所有其他发件人（当前库里没有，所以
   A/B 差值一样；但一旦阿里云别的地址开始发营销邮件，它们也会一起被豁免）。
   建议若选 B，**只加 `monitor.aliyun.com`** 而不是 `aliyun.com`。
2. 该匹配**可被利用**：`attacker@kxpms.cn.evil.com` 的域名是
   `kxpms.cn.evil.com`，`Contains("kxpms.cn.evil.com", "kxpms.cn")` 为真 ⇒ 命中
   白名单。影响有限（白名单只豁免垃圾评分，不授予任何信任或访问权，邮件照样
   进收件箱、照样走分类与提醒），但确实是个可被绕过的口子。
   收紧成 `HasSuffix`（或「完全相等，或以 `.` + w 结尾」）会改变少量真实域名的
   匹配结果，属于行为变更，**未改**，留待拍板。

### 另一个同批待决项的现状（`edm.infoq.com.cn`）

同发件人 **判定不一致**：3 封里 2 封页脚带「点击这里取消订阅」→ 100 分判垃圾，
第 3 封（snippet 以正文开头、没匹配到退订）→ 30 分留在收件箱。
差别只在某一封的模板有没有那个页脚链接。`spam.go:116-123` 已把这条记在注释里，
**按发件人整体判定还是按单封模板**仍未拍板（属产品语义：会不会因此误杀同域真人邮件）。

### 建议（供拍板，不代替决定）

选 B，且**只加 `monitor.aliyun.com`**。理由：安全告警的误杀代价明显高于多留几封
营销邮件在收件箱；而阿里云周报是**周期性、有实际信息价值**的运维输入，
且该账户近 120 封里阿里云只发了这 4 封、全部是安全内容，没有混入营销。


---

## §7cz 【需求 6/7】邮件的增量同步**永远退化成全量拉取**，而它的「正确」是靠这个 bug 换来的（2026-10-02）

需求 7「在邮件窗口查看各类邮件」的 UI 链路本身是完整的，我逐段验过：

```
INBOX_CATEGORY_CHIPS（9 个 chip，含 重要/未分类/垃圾 三个虚拟值）
  → EmailInboxView.setCategory → readInboxPage(activeCategory, offset)
  → inboxListFilter：__important→{importance:'high'}、__spam→{category:'spam'}、__none→{uncategorized:true}
  → emailsStore.listEmails：三个条件都正确落到本地 SQLite 的 WHERE
syncEmailsFromServer：category / importance 都带下来了
```

虚拟值没有漏成字面量，`listEmails` 三个条件也都实现了 —— 这里没有 bug。
**但往上游追一层，增量同步协议本身是坏的**，而且坏法很特别。

### 表里根本没有 `updated_at`

```
information_schema 查 emails 的时间列：
  created_at, date, deleted_at, notified_at, processed_at      ← 没有 updated_at
processed_at 非零的行：0 / 120                                  ← 就是那个死列
```

于是服务端 `ListEmailsScoped` 的增量过滤（`store.go:1673`）实际比的是：

```go
stamp := "GREATEST(e.date, COALESCE(e.processed_at, 0), e.created_at)"
```

`processed_at` 恒 0 → 退化成 `GREATEST(date, created_at)`。

而客户端游标来自 `MAX(updated_at) FROM local_emails`，服务端给过去的
`UpdatedAt` 是**从 `date` 凭空合成的**（`store.go:1700-1704`：
`if e.UpdatedAt == 0 { e.UpdatedAt = e.Date * 1000 }`，因为 SELECT 列表里
根本没有 updated_at 列）。

### 实测：游标永远追不上

客户端游标 = `max(date)`，服务端过滤 = `GREATEST(date, created_at) > max(date)`：

| 指标 | 实测值 |
|---|---|
| 游标取 `max(date)`=1790821420 时返回的行数 | **120 / 120（整箱）** |
| `created_at > max(date)` 的行 | **120 / 120** |
| `date > max(date)` 的行 | 0 |
| `created_at − date` 最小间隔 | 48792 s（≈13.5 小时） |
| 平均间隔 | 511744 s（≈5.9 天） |

`created_at`（入库时间）比 `date`（邮件原始时间）晚 13.5 小时到 5.9 天，
**每一行都是**。所以游标被钉死在 `max(date)`，每一轮增量拉取都把整个信箱
重发一遍，永远如此。协议文档写的「since 只回传 updated_at 更晚的行」
（docs/2026-09-09-list-sync-rules.md）在邮件这条链路上**没有兑现**。

### 关键：需求 7 现在「看起来对」，靠的正是这个 bug

因为整箱被重发，AI 分类写进去的 `category` / `importance` 每次都能覆盖到
设备本地库，所以分类 chip 在设备上是有效的。

但这个「有效」是**偶然**的：

- 没有任何列记录「分类是什么时候发生的」（表里没有 `updated_at`，
  `SetClassification*` 也不碰任何时间戳列）；
- 所以一旦有人把游标改成「拉取时刻」来修掉全量重发，**分类变更就再也
  不会下发给设备**，需求 7 的分类筛选会静默变成陈旧数据。

**修效率就会破正确性，除非先给 `emails` 加真正的 `updated_at` 并让
`SetClassification*` / `MarkEmailBodyCached` / 软删 / 标记已读都去 bump 它。**

这与需求 8 的 LWW 是同一件事的两面：需求 8 要求「以最后修改时间为准」，
而邮件这条链路上目前**根本没有一个可信的最后修改时间**。所以 8 的方案
不能只做账户表，必须把 `emails.updated_at` 一起纳入 —— 否则「最后修改
时间」在邮件上是空的。

### 本轮不改

这是 schema 级改动（加列 + 改所有写路径 + 迁移），且与需求 8 待拍板的
账户归属语义耦合。本轮只记录，不擅自实施。

---

## §7az 本轮仍未验证 / 仍是阻塞

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


---

## §7dm 【需求 7】邮件 HTML 净化边界首次可测：补 17 条钉子用例，查出 3 处「配置写了但不生效」+ 1 条独立远程请求通道（2026-10-03）

### 起因

需求 7 是「在邮件的窗口中可以查看收到的各类邮件」。详情页把**发件人完全可控的
HTML 正文**经 `v-html` 塞进 Capacitor WebView（`EmailDetailView.vue:76/:93/:96`），
而这个 WebView 同时持有 IMAP 凭证与 native bridge。所以
`sanitizeEmailHtml` 是整条链上**唯一**的信任边界。

查覆盖率时发现两个事实：

1. `sanitizeEmailHtml`（`email-detail-format.ts:18`）此前**一条测试都没有**；
2. 它当时**也测不了**——实测（Node 22.23.2，无 DOM 环境）：

   ```
   isSupported = false
   DOMPurify.sanitize is not a function   ← TypeError，不是「原样返回」
   ```

   也就是说，任何「给净化器补测试」的尝试都会先抛 `TypeError`。这大概率就是它
   一直没有护栏的原因：**不是没人写，是一写就炸。**

### 做法：把 DOM 装起来再动态 import

`npm install --save-dev jsdom@^30.1.1`（新增 35 个包；`dompurify` 本来就在
`dependencies`，不需要动生产依赖）。测试文件
`frontend/src/features/email/__tests__/email-detail-sanitize.test.mjs`：
先把 jsdom 的 `window/document/Node/…` 挂到 `globalThis`，**再用顶层 await 动态
import** 被测模块——因为 `dompurify` 在 import 期就要 window，静态 import 会早于
jsdom 安装。

### 三处「配置写了但不生效」（全部为实测，不是从配置推的）

| # | 配置意图 | 实测 | 后果 |
|---|---|---|---|
| 1 | `ALLOWED_TAGS` 显式列了 `'style'`（`email-detail-format.ts:26`），模块注释第 9/16 行专门论证「基准样式必须**在净化前**注入，否则会被剥掉」 | **`<style>` 照样被剥掉**。用 DOMPurify **自己的默认配置**（不含任何自定义 `ALLOWED_TAGS`）也一样剥 | `injectBaseStyle`（`email-body-style.ts:99`）/ `normalizeStyleBlocks`（`:92`）/ `baseEmailStyle`（`:72`）**整条链路是死的**。邮件正文拿不到 CJK 字体兜底与图片 `max-width` 约束 |
| 2 | `ALLOWED_URI_REGEXP`（`:32`）只放行 `https?:` 与 `data:image/…` | `data:text/html;base64,…` 能进 `<img src>` | DOMPurify 对 img/audio/video/source/track 另有一条「`data:` 一律放行」的内建豁免，配置意图没兑现。**现代引擎在 `<img>` 上下文不渲染 data:text/html，故不构成 XSS** |
| 3 | `stripRemoteFonts`（`email-body-style.ts:32`）只管 `@font-face` | `style="background:url(https://track.example.com/bg.png)"` **原样保留** | 这是一条**独立于 `<img>` 的远程请求通道**，见下 |

第 1 条是本节最实的一条：**一段被仔细论证过顺序的代码，整条是无效的**。用
`git grep` 量过 blast radius——`baseEmailStyle` 只被 `injectBaseStyle` 调，
`injectBaseStyle` 只被 `sanitizeEmailHtml` 调，所以死掉的是**整个样式归一化链路**，
没有别处兜底。

> **定性**：以上三条对用户目前都**没有可感知的损害**（第 1 条是外观；第 2、3 条
> 是「配置意图没兑现」与隐私面，不是崩溃、不是 XSS）。如实标注，不夸大成安全漏洞。

### 远程请求面：实测「打开一封邮件会向谁发请求」

实测链路（`EmailDetailView.vue:436` 附近 `renderBody()`）：

```
净化(不拦 http 图源) → 先把净化后的 HTML 塞进 v-html（文字立刻可见）
                    → 再 preloadRemoteImages(sanitized) 抓远程图转 data URI
```

于是对 `<img src="https://track.example.com/px.gif">` 这封邮件，**用户什么都没点，
详情页就已经向 track.example.com 发了一次请求**。用例
「实测：净化后 `<img>` 的远程图仍会随后被 `preloadRemoteImages` 抓走」用注入的
`fetchImpl` 断言了 `calls === ['https://track.example.com/px.gif']`。

对照组：`<a href="https://tracker.example/collect">` 净化后保留但**不会**被自动
请求（用例断言 `calls === []`）——自动追踪的洞目前只在图片这条通道上。

**重要限定**：只给 `<img>` 加「默认不加载远程图」**不构成完整修复**，因为上面第 3
条的 CSS `url()` 通道还在，且 `preloadRemoteImages` 的采集正则
（`email-image-preload.ts:43`）只认 `<img src=…>`，覆盖不到它。

**真实语料的暴露面未量化**：120 封真实邮件里有几封带远程图、来自哪些主机——
**本轮没有测出来**，因为正文存在 `data/email-bodies/`（41 个文件，AES-GCM 加密，
密钥在 `POCKET_EMAIL_MASTER_KEY` 里，不在本 worktree 环境）。不要拿这里的合成
样例数字冒充真实命中率。

### 负控：3 次，全部按预期转红

| 负控 | 变异 | 结果 |
|---|---|---|
| NEGCTL-1 | `sanitizeEmailHtml` 开头加 `if (raw) return raw`（关掉净化器） | **17 条里 14 条转红**，含哨兵本身 |
| NEGCTL-2 | 从 `ALLOWED_ATTR` 去掉 `'style'` | 精确 1 条转红（CSS `url()` 那条）——NEGCTL-1 漏掉的用例由它补上 |
| NEGCTL-3 | 从 `ALLOWED_URI_REGEXP` 去掉 `https?:` | 3 条转红，其中包含 NEGCTL-1、NEGCTL-2 都没碰到的「远程图被抓走」那条 |

三次变异全部用 `git checkout --` 还原，还原后 `git diff --numstat` 为空
（逐字节验过生产文件没被改）。

**哨兵的作用**：第 1 条用例「净化器确实在删东西」是为了防「jsdom 没装成功 /
dompurify 变不支持 → 净化器退化成原样返回 → 上面所有『危险标签被剥掉』的断言
全部变成永真、静悄悄全绿」。这是本仓库反复踩的失效模式（判据失效但退出码 0），
所以把它做成第一条、并且 NEGCTL-1 证明它会红。

### 数字

- `frontend/src/features/email/__tests__/email-detail-sanitize.test.mjs`：17 条，全绿
- `npm.cmd run test:email`：**248 → 265** 条全绿（0 fail / 0 skipped）
- `npm.cmd run typecheck`：exit 0
- `package.json` / `package-lock.json`：仅新增 `jsdom` devDependency

### 本轮**没有**做的事（如实列出）

- **没有改任何生产代码。** 三处「配置写了但不生效」都只写了「现状钉子」用例，
  没去修——修法涉及产品取舍（要不要远程图片默认加载、CSS `url()` 堵不堵），
  见下面待拍板项。
- **没有验证真机 WebView。** 上述 DOMPurify 行为是在 **jsdom** 下测的。
  DOMPurify 解析走 `DOMParser`/`createHTMLDocument`，`<style>` 落到 `<head>`
  而它只返回 `<body>` 子节点——这是 DOM 实现层面的机制，真机 WebView 预期一致，
  但**未经真机验证**，不下断言。
- **没有量化真实语料的远程图命中率**（原因见上）。

### 新增待拍板项

1. **远程图片要不要默认阻断**（像 Outlook/Gmail/Thunderbird 那样给一个
   「显示远程内容」开关）。当前是**默认全加载、无开关、无主机白名单**，且开一封
   邮件就等于向正文里所有主机发请求。这是我建议优先处理的一条——理由是
   「打开邮件」这个动作本身在隐私上等价于主动联系了对方所有第三方。
2. **CSS `url()` 通道要不要一起堵**。只堵 `<img>` 不完整。
3. **`injectBaseStyle` / `normalizeStyleBlocks` / `baseEmailStyle` / `stripRemoteFonts`
   这一整条无效链路怎么办**：删掉，还是改用「不走 `<style>` 标签、直接把基准样式
   拼到宿主页面的 CSS 里」把它救活（后者能真正解决邮件正文的 CJK 字体兜底）。
   归入既有待拍板项「是否删除死字段与已证死函数」，**不擅自删**。

---

## §7dn 【需求 1/7】归类分批循环在分类器故障时**没有出口** —— 一次点击换一场对故障网关的持续压测（2026-10-03）

### 症状与机制

`use-email-inbox.ts` 的归类循环原本是：

```ts
do {
  const report = await emailApi.classifyInbox(20, controller.signal)
  ...
  if (classifyCancel.value || remain <= 0) break
} while (!classifyCancel.value)
```

它**唯一的自然退出条件是 `remaining <= 0`**，且没有批次数上限。

而服务端 `POST /api/emails/classify`（`server_email_classify.go:88-92`）在
**逐封分类全部失败**时返回的是：

```json
{ "classified": 0, "remaining": 25, "results": [ {"error": "..."} × 20 ] }
```

`classified` 的定义是「`Category != "" && Error == ""` 的条数」
（`server_email_classify.go:82-87`），失败时为 0；而 `remaining` 数的是
`category IS NULL OR category=''`（`store_inbox.go:63`），失败时根本没写库，
**一封没少**。于是 `remaining <= 0` 永远不成立，循环**没有出口**：

> 用户点一次「归类」，前端无限次打这个端点；每次调用服务端都要逐封跑分类；
> 没有退避、没有上限。持续对一台**已经故障**的分类器施压。

### 「逐封全部失败」不是边角状态

新增 `backend/internal/server/server_email_classify_progress_test.go`，用两种
**真实运行故障**复现（kxmemory 未配的部署——也就是本部署，
`POCKET_KXMEMORY_BASE_URL` 未设——全靠网关这条腿）：

| 场景 | 复现方式 | 断言 |
|---|---|---|
| A 网关调用失败 | `fakeLLM{err: 503}` | `classified=0`、`remaining=3` 不变、逐封 `Error != ""` |
| B 输出无法解析 | `fakeLLM{reply: "这封邮件看起来是账单。"}` | 同上，`remaining=2` 不变 |
| **不收敛** | 两种场景各**连打三次** | 三次 `classified` 恒 0、`remaining` 恒 5 |

第三条是关键：只断言「一次返回 remaining>0」不够——有人会说「下一次就好了」。
连打三次证明**故障不变时它永不收敛**，客户端那个循环**没有任何出口**。

### 一条被否掉的假机制（留档以免有人重走）

初稿假设主因是「网关对象配了但没选模型」→ `emailClassifyModel` 返回 `""` →
`classifyViaGateway` 的 `model == ""` 早退（`:126-129`）。

**实测不成立**：`cfg.LLMModel` 为空时 `ResolveGatewayForUser` 仍给出非空
`PreferredModels`（`llm_gateway_resolve.go:122-128`），所以那条早退没被触发，
日志显示实际走的是「调用了模型但输出无法解析」。**机制说错了**。

结论本身不受影响——(A)(B) 两个场景由上面三条用例**独立**证明，不依赖那个假机制。
写在这里是因为「猜出来的因果链」正是本轮 §7cy 在 `has_attachments` 上吃过的亏
（初稿的因果链错了），这次是自己在同一小时内重犯，标注出来。

### 修法：把循环抽成零依赖纯逻辑，并补上「零进展就停」

新增 `frontend/src/features/email/email-classify-loop.ts`（与 `account-lww.ts`
同一思路：**生产与测试共用同一份实现**，而不是让测试正则刨源码——刨出来的片段
带类型注解，`new Function` 会 SyntaxError，这正是 `invoice-money.ts` 抽出前的
老问题）。

四条退出条件，顺序有讲究：

1. `cancelled` —— 用户主动中止，最高优先；
2. `drained` —— `remaining <= 0`，正常跑完；
3. **`no-progress`** —— 修复的核心，两种写法：
   - (a) `classified === 0`。**不需要上一轮的数就能判定，且可靠**：`classified`
     为 0 意味着一封都没写库，`remaining` 自然一封没少。**第一批同样适用**。
   - (b) `remaining` 没有比上一轮少。更隐蔽的形态：声称归类了几封但 remaining
     纹丝不动（写库没生效 / 统计口径不一致）。从第二轮起才有意义。
4. `batch-cap` —— 批次数硬上限（默认 200 批 = 4000 封/次点击），纯兜底。
   命中时 `stopped` 如实为 `batch-cap`，**不静默收工**。

`use-email-inbox.ts:99` 起改用它，`:117` 用 `classifyStopHint` 按终止原因
给出**四种可区分**的文案——尤其把「分类器没返回结果，所以我停了」和
「确实跑完了」分开，否则用户只会以为点了个寂寞（同一个坑：
`reminder_diag_test.go` 记录的 RemindersSent 恒为 0）。

### 实现里踩到的一个真坑（第一版是错的）

第一版用 `prevRemaining = Infinity` 起手，想「保证第一批不误判」。结果
`remaining >= Infinity` 恒假，**第一批的零进展根本判不出来**，要等到第二轮。
两条用例当场转红（`not ok 3` / `not ok 5`），改成上面的 (a) 才修好。

这个错误的形状值得记住：**用哨兵值规避误判，结果让真阳性漏掉了**。
哨兵能防「第一批被误判成零进展」，但代价是「第一批的真零进展也判不出」。

另外 `classified>0 但 remaining 不降` 这一形态**最早只能在第二批识破**——
`remaining` 是分类**之后**的数，服务端没告诉我们分类之前是多少。这是有下限的，
不是判据失效，用例里写明了 `calls === 2` 而不是 1。

### 负控

| 负控 | 变异 | 结果 |
|---|---|---|
| NEGCTL-1 | 把 `got <= 0` 与 `remaining >= prevRemaining` 两个守卫都改成 `&& batches > 1e9`（永不成立） | **2 条转红**（用例 3、5） |
| NEGCTL-2 | 把 `use-email-inbox.ts` 的旧判定原样搬进测试跑一遍 | 用例 4 断言它会打到人为上限 50 次——**把「不收敛」也变成可复现证据**，而不是一句断言 |

### 数字

- `backend/.../server_email_classify_progress_test.go`：3 个用例（含 2 个子测试）全绿
- `frontend/.../__tests__/email-classify-loop.test.mjs`：**16 条全绿**
- `npm.cmd run test:email`：**265 → 281** 全绿（0 fail / 0 skipped）
- `npm.cmd run typecheck`：exit 0
- `go test ./internal/server/`：只剩**两个既有失败**
  （`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，非邮件分支、非本轮引入，
  每轮回归都出现，见待拍板项「internal/server 越权 404/403 语义分歧」）

### 本轮**没有**做的事

- **没有改服务端任何生产代码。** 只加了复现用例。服务端返回
  `classified:0 / remaining:N` 本身是**诚实的**（它如实说了「一封都没成」），
  问题在于客户端拿着这个诚实的回答**没有停**。改客户端就够了。
- **没有给 `classifyInbox` 加退避/重试。** 分类器故障时立刻重试无意义；
  真要重试应带退避，那是另一项设计，不在本轮偷偷加。
- **没有真机验证。** 客户端循环的逻辑已抽成纯函数并测透，但「WebView 里点
  归类按钮」的端到端表现仍未在真机验过。

### 顺带确认（不改代码）

`has_attachments` 仍是「列存在、UI 读它、生产里恒为 false」。
`EmailCard.vue:35` 的 📎 标记因此**永不显示**。归入既有待拍板项「是否真置位」，
本轮不动。

---

## §7do 【需求 3/5】发票合计的币种字段在**客户端转发层**被吃掉 —— §7dk 那类错账在下面一层又长出来一次（2026-10-03）

### 链路与病灶

```
服务端 server_email_invoice.go:55-57
    "amount": page.Amount      // 仅单一币种有意义；多币种时为 0
    "currency": page.Currency  // 同上；多币种时为 ""
    "amounts": page.Amounts    // 按币种分组的全量合计，不受分页截断
      ↓
转发层 invoice-list-pull.ts:37   ←←← 病灶
      ↓
判定层 use-invoice-list.ts applySummary:59-67
      ↓
展示   summaryMoney(groups)
```

修复前转发层返回的是：

```ts
totals: { total: res.total, filed: res.filed, amount: res.amount }
```

`currency` 与 `amounts` **被静默吃掉**。而下游的优先级判定
（`applySummary`）写的是「`amounts` 优先 → 否则 `amount` + `currency`
→ 否则本地分组」——**它要求三个字段都在**。少一个就出两种错账：

| 场景 | 少了什么 | 后果 |
|---|---|---|
| **单一外币**（100 USD） | `currency` | `normalizeCurrency(undefined)` 兜底成 CNY → 合计渲染成 **「¥100.00」**。这正是 §7dk（`de8d76d1`）在服务端修掉的同一类错账 |
| **多币种** | `amounts` | 服务端此时 `amount=0` → 退回 `sumByCurrency(当前页)` → 合计从「全量」缩成「**这一页**」，翻页时数字还会跳 |

也就是说：**服务端修好了，客户端在它下面一层把修复抵消了**，而且完全静默。

### 为什么按「修漏」而不是「新增设计」

规则已经存在并已在一层之上落地（`applySummary` 的优先级判定、`invoice-money.ts`
的 `sumByCurrency`/`summaryMoney`、§7dk 的服务端实现）。缺的只是转发层没把字段
带下去。判据沿用本会话一贯的那条：**「仓库已有既定规则却被漏掉」按修漏处理**。

### 修法

`invoice-money.ts` 加两个纯函数（与 `account-lww.ts` / `email-classify-loop.ts`
同一思路：**生产与测试共用同一份实现**）：

- `invoiceTotalsFrom(res)` —— 转发层专用，**只做默认值兜底、不做任何裁剪**。
  刻意不给 `amount` 加「多币种就归零」之类的加工：那该由服务端负责。
- `resolveSummaryGroups(totals, list)` —— 把 `applySummary` 里那三行优先级
  内联判定收进来，避免它和转发层分叉出两套规则。

`invoice-list-pull.ts:37` 改用 `invoiceTotalsFrom(res)`；
`use-invoice-list.ts` 改用 `resolveSummaryGroups(totals, list)`。

改动很小：`use-invoice-list.ts` 净 -15/+11 行（见 `git diff`）。

### 判据：行为断言 + 一条结构断言

`__tests__/invoice-totals-chain.test.mjs`，11 条。

**为什么不能只写行为断言**：`invoice-list-pull.ts` 依赖 Capacitor（`invoiceStore`
→ native），没法在 `node --test` 里 import。所以行为断言只能覆盖
`invoiceTotalsFrom` **本身**——若有人把转发层改回字面量，11 条行为断言
**全部仍然全绿**，而那正是修复前的状态。

因此补了一条**结构断言**（用例 10/11）钉住「转发层必须走 `invoiceTotalsFrom`」
「`applySummary` 必须走 `resolveSummaryGroups`」。这里用源码匹配是刻意的取舍：
判据匹配的是**函数调用结构**（`totals: invoiceTotalsFrom(res)`）而不是任意文本，
且用例里明确说明了这层的边界。

> 顺带一个实测：**`npm run typecheck` 抓不到「导入未使用」**（NEGCTL-2 里把转发层
> 改回字面量后 typecheck 仍 exit 0），所以这条结构断言在做实事，不是冗余。

### 负控 2 次

| 负控 | 变异 | 结果 |
|---|---|---|
| NEGCTL-1 | `invoiceTotalsFrom` 不再返回 `currency`/`amounts`（模拟修复前的转发层） | **6 条转红**，含关键的「单一外币显示 $ 不是 ¥」 |
| NEGCTL-2 | 把 `invoice-list-pull.ts` 改回字面量 | **精确 1 条转红**（结构用例 10）；行为断言 1~9 全绿——正是上面说的覆盖边界，结构断言补上的 |

两次均已还原，`invoice-money.ts` / `invoice-list-pull.ts` 回到修复后状态。

### 数字

- `__tests__/invoice-totals-chain.test.mjs`：11 条全绿
- `npm.cmd run test:email`：**281 → 292** 全绿（0 fail / 0 skipped）
- `npm.cmd run typecheck`：exit 0
- 生产代码改动：3 个文件，净 +76 / -12

### 本轮**没有**做的事

- **没有真机/端到端验证。** 上面验的是「服务端响应 → groups → 展示字符串」这条
  纯逻辑链。真实列表页在真机上显示成什么样，仍未验。
- **没有动服务端。** 服务端本来就是对的（`server_email_invoice.go:55-57` 三个字段
  齐发），问题纯在客户端。
- **没有审其它转发层。** 本轮只查了发票列表这一条。
  `has_attachments`、`updated_at` 等字段是否也在某一层被静默丢弃，**未系统排查** ——
  这是「字段写了/读了但中途没人传」这一族的系统性风险，建议单独排一轮
  「DB 列 → API 类型 → 转发层 → 视图」四段的字段对账。

---

## §7dp 字段四段对账（DB 列 → Go → API 类型 → store → 视图）+ `has_attachments` 在 IMAP 路径上是**结构性不可得**（2026-10-03）

### 做了什么

§7do 结尾留了一项「字段四段对账」，本轮把它跑掉：脚本从 `information_schema`
取三张邮件表（`emails` / `email_invoices` / `email_accounts`，共 71 列）的权威列名，
对每一列检查它在四个位置是否出现——Go 生产代码（非测试）、`api/email.ts`、
`emails-store.ts` / `invoices-store.ts`、`features/**.vue`。

脚本跑完就删（`probe-field-audit.cjs`，用 mavis-trash），**没有留在仓库里**——
它是一次性量测工具，不是护栏。留下的是结论。

### 结论一：粗扫的 23 条「断链候选」绝大部分是**有意的**，不能当缺陷报

举三个我**逐一复核过**的：

| 候选 | 复核结果 |
|---|---|
| `accounts.smtp_host` / `smtp_port`：API 类型有、store 没接、但 `.vue` 在用 | **假阳性，有据可查**。`EmailAccountSetup.vue:207-209` 明写「本地 store 的 EmailAccount 没有 SMTP 字段（SMTP 只在服务端生效），所以编辑态预填必须拿云端那份」，`:293` 走的是 `cloudAccounts`（直接来自 API）而不是本地 store。**设计如此** |
| `accounts.credential_encrypted` / `smtp_credential_encrypted` | 凭证**本来就不该**进视图层。不接是正确 |
| `*.workspace_id` / `*.user_id` | 作用域列，不进视图层是正确 |

**这就是粗扫不能直接当结论的证据**——`smtp_host` 那条长得最像 bug（API 有、
store 无、视图在用，三段自相矛盾），实际是刻意设计并写了注释。

### 结论二：真正的问题——`has_attachments` 在 IMAP 路径上**没有任何数据来源**

`emails.has_attachments` 只有两处写：

- `store.go:560` INSERT（作为参数传 `e.HasAttachments`）
- `store.go:1925` `SET ... has_attachments = COALESCE(has_attachments, FALSE)`
  ——**恒等操作**，是 §7cy 记过的

而 `e.HasAttachments` 全仓库**只有一处被赋值**：

- `fetcher.go:974` `em.HasAttachments = len(parsed.Attachments) > 0`

它在 **`syncPOP3Fallback` 的 POP3 循环里**（`fetcher.go:936-983`），基于
`ParseMIMEMessage(raw)` 的解析结果——POP3 `RETR` 拿到的是完整 RFC 5322 原文，
附件当然看得到。**这条路径是正确的。**

而 IMAP 路径（`fetcher.go:731-738`）的 FETCH 只请求三项：

```go
fetchOpts := &imap.FetchOptions{
    Envelope:     true,
    UID:          true,
    InternalDate: true,
    // 部分 IMAP server（如 Greenmail）对 BODY[TEXT]<0.1024> 的响应缺
    // SP 分隔符导致 imapwire 解析失败，因此仅 envelope + UID 起步，…
}
```

`envelope` 里**没有附件信息**，代码也从未给 IMAP 路径的 `em.HasAttachments`
赋过值 → 恒为 Go 零值 `false`。**不是漏写一行，是那条路径上根本没有数据。**

### 实测（2026-10-03 08:25:22，schema `opencode_pocket`）

运行中的 pocketd 每分钟同步真实账户，所以任何「当前库」结论都必须带测量时刻。

```
total=120
pop3_sourced=0          ← 唯一会置位的那条路径，本库一封都没有
imap_sourced=120        ← 全部来自结构性不可得的那条路径
has_attachments_true=0
attachments_col_nonempty=0
```

**「库里一封都没置真」的根因到此确定**：不是 IMAP 路径忘了写，是
**120 封全部来自 IMAP 路径，而 IMAP 路径拿不到附件信息**；唯一能置位的 POP3 路径
在本部署里产出为 0（与 §7de 记的「QQ 上 284/444 走 POP3」不矛盾——那是当时的
另一批数据/另一轮配置，本库这 120 封没有一条 POP3 id）。

`attachments` JSON 列同样**从未写入**（`attachments_col_nonempty=0`）。

### 要修需要什么，以及它卡在哪

1. `fetchOpts` 加 `BodyStructure: &imap.FetchItemBodyStructure{}`（go-imap
   v2.0.0-beta.8 有这个 FetchItem，`imap.BodyStructure` 是
   `*BodyStructureSinglePart` / `*BodyStructureMultiPart` 的联合，提供
   `Walk()` / `Filename()` / `Disposition()`，判附件不难）。
2. **踩坑预警**：我一度以为「ENVELOPE 自带 part 结构、近乎免费」——**实测证伪**：
   v2 的 `imap.Envelope`（`fetch.go:85-95`）**没有 `Body` 字段**（v1 才有）。
   所以必须新增 BODYSTRUCTURE 数据项，不是零成本。
3. **真正的卡点**：`fetcher.go:735-737` 的注释明写这条 FETCH 是**被刻意裁剪过**
   的——加数据项曾经导致 Greenmail 响应解析失败。现在再往这条已知脆弱的
   FETCH 上加 BODYSTRUCTURE，**必须先在 Greenmail 上验**，
   而 Greenmail 是本机唯一未启动的依赖（Docker daemon 未运行，
   见待拍板项「启动 Docker daemon」）。
4. POP3 路径已正确，无需改动；但它在本部署产出为 0，**帮不上忙**。

所以这个待拍板项的形态变了：不是「要不要置位」（需求 7 的 📎 标记要它，显然要），
而是「**在 fetch 脆弱性未验证前动不动**」。这是一个带成本的取舍，不是我能替你定的。

### 数字与范围

- 71 列 × 4 位置全量粗扫；**逐一复核 3 条候选，1 条假阳性（有据），2 条有意的**
- 生产代码改动：**0**
- 新增测试：**0**（本节是量测与取证，不是修复）
- 仓库内无残留：探针脚本已删，`git status` 干净

### 本轮**没有**做的事（如实列出）

- **没有系统复核其余 20 条候选**。只抽查了 3 条。所以「其余候选也都无害」这句话
  我**没有证据**，不下断言。脚本已删，要复核需要重跑。
- **没有改任何生产代码**，也没有给 `has_attachments` 补测试。
- **没有验 Greenmail**，因此 BODYSTRUCTURE 方案的实际风险未量化。

---

## §7dq 【需求 7】修掉 `has_attachments` 在 IMAP 路径上恒为 false；过程中实测到「负控第四次不转红」的真因（2026-10-03）

### 缺陷（承接 §7dp）

`emails.has_attachments` 全仓库原本**只有一处**被赋值，在 POP3 降级路径里
（`em.HasAttachments = len(parsed.Attachments) > 0`）。IMAP 路径**从未**赋过值——
不是漏写一行，是那条路径上没有数据来源：FETCH 只请求 Envelope / UID /
InternalDate，而 go-imap v2 的 `imap.Envelope`（`fetch.go:85-95`）**没有 Body
字段**（v1 才有），「反正 envelope 里就有」被实测证伪。

§7dp 实测（08:25:22）：真实库 120 封全部 IMAP 来源、`has_attachments_true=0`，
唯一能置位的 POP3 路径产出为 0。于是 `EmailCard.vue` 的 📎 标记**永不显示**。

### 修法

- `fetcher.go` 的 `fetchOpts` 加 `BodyStructure: &imap.FetchItemBodyStructure{}`
- 新增 `fetcher_attachment.go`：`bodyStructureHasAttachment(bs imap.BodyStructure) bool`

口径：disposition=attachment → 是；带 filename（Disposition 的 filename 或
Content-Type 的 name）→ 是；**内联图不算附件**（`Content-Disposition: inline`
且无 filename 时不算，否则每封带签名图的邮件都会亮 📎）。
`bs == nil` 保守返回 false（不凭空置真）。

### 关键：不需要 Docker，仓库里已经有 in-process IMAP

`fetcher_pipeline_test.go` 早就用 go-imap 的 `imapserver/imapmemserver` 起了
一个 TLS 的进程内 IMAP server（随机 loopback 端口 + 自签证书 + `dialTLS` 注入）。
所以 IMAP 链路**可以**在 `go test` 里真跑，不需要 Greenmail、不需要 Docker、
不需要网络与真实凭证。

端到端用例 `fetcher_attachments_test.go` 3 条：带附件 / 纯文本 / 内联图，
跑 `Fetcher.Sync` 后从库里读回 `has_attachments`。

### 本轮最重要的一件事：负控**第四次**不转红，这次查到了真因

第一次负控是「把 `case "inline"` 的豁免删掉」，期望
`TestSyncDoesNotCountInlineImageAsAttachment` 转红。**没有转红。**

按惯例先怀疑三件事（判据太松 / 注入没生效 / 结论本身错了），这次都不是。
写了个临时探针 dump `imapmemserver` 真实返回的 BODYSTRUCTURE：

```
--- uid=1 subject="inline probe"
    path=[] <multipart> *imap.BodyStructureMultiPart
    path=[1] type=text/html    params={charset=utf-8} disp=<nil> extended=false
    path=[2] type=image/png    params={}              disp=<nil> extended=false
--- uid=2 subject="att probe"
    path=[] <multipart> *imap.BodyStructureMultiPart
    path=[1] type=text/plain         params={charset=utf-8}      disp=<nil> extended=false
    path=[2] type=application/pdf    params={name=invoice.pdf}   disp=<nil> extended=false
```

**`imapmemserver` 根本不填 BODYSTRUCTURE 的 extended 部分**——`Disposition()`
对任何 part 都返回 nil。于是：

- `case "inline"` 与 `case "attachment"` 两个分支在端到端用例里**从未被执行**；
- 内联那条用例是**碰巧**通过的：内联图没有 `name=` 参数，走 `default` 分支
  得到 `filename == ""` → false。它验证的是「没有 filename 就不是附件」，
  **不是**「inline 不是附件」。

所以第四种原因的确切形态是：**被测逻辑（disposition 分支）根本没被执行到**，
变异落在一条 no-op 路径上。这与本会话早先记录的 `if s.kxmemory != nil` →
`if true` 那次同源：**「全绿」的前提是被测逻辑真的跑过**。

修法：把两个端到端/单测层次**分工**写进注释，并补 10 条直接构造
`imap.BodyStructureSinglePart` 的单测（`fetcher_attachment_test.go`）逐分支钉：

| 层 | 钉什么 | 覆盖不到的 |
|---|---|---|
| 端到端（`fetcher_attachments_test.go`） | 「整条链路真的用上了这个函数」 | disposition 两个分支（server 不给） |
| 单测（`fetcher_attachment_test.go`） | 判定口径逐分支 | 链路是否真的接上 |

**两层不能互相替代**：只写端到端，用例会因为 no-op 变异而全绿；
只写单测，函数没被接进 Sync 也照样全绿。

### 负控 2 次（都按预期转红）

| 负控 | 变异 | 结果 |
|---|---|---|
| NEGCTL-1 | `case "inline"` 的 `found = filename != ""` 改成 `found = true` | **精确 1 条转红**（`TestBodyStructure_InlineWithoutFilenameIsNotAttachment`）。注意：**第一轮做过同一个变异，端到端用例全绿** |
| NEGCTL-2 | 从 `fetchOpts` 去掉 `BodyStructure` | 精确 1 条转红（`TestSyncSetsHasAttachments_AttachmentOnly`） |

两次均已还原；`git diff --stat` 为 `fetcher.go` 净 +14/-1，另加 3 个新文件。

### 数字

- `fetcher_attachment_test.go`（单测）10 条全绿
- `fetcher_attachments_test.go`（端到端）3 条全绿
- `go test ./internal/email/`：**全绿**（75.1s）
- `go test ./internal/server/`：只剩**两个既有失败**
  （`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，非邮件分支、非本轮引入）
- `gofmt -l` 三个新文件：无输出（干净）。**只对新增文件跑 gofmt**，
  `fetcher.go` 是既有文件，未格式化，diff 保持 14/-1

### 这个修复**证明了什么、没证明什么**（重要）

**证明了**：go-imap 客户端 ↔ 服务端这一对能正确协商并解析 BODYSTRUCTURE；
判定口径对纯文本 / 附件 / 内联图 / 嵌套 multipart / 大小写 disposition
都成立；整条 `Sync → InsertEmail → 读回` 链路真的把这个函数用上了。

**没证明**：真实第三方 IMAP server 不会因此出问题。`fetcher.go` 里那条注释
记录过「加过数据项导致部分 server 响应缺 SP 分隔符、imapwire 解析失败」的历史，
而 `imapmemserver` 是 go-imap 自己的实现，**不可能**复现那种畸形响应。
**那一半仍然未验**，仍需 Greenmail（卡在 Docker daemon 未运行）。

此外 `imapmemserver` 不填 extended 这件事本身也有生产含义：它说明
**至少有一类实现不返回 disposition**，此时判定会退到「只看 filename」。
这在真实 server 上是常见形态（不少服务器只给 Content-Type 的 name=），
所以 `default` 分支不是多余的——但反过来，**真实 server 上 inline 图若既无
filename 又无 disposition，本实现会判成「不是附件」，这与预期一致**。

### 本轮**没有**做的事

- **没有验真实 server**（如上，仍需 Greenmail）
- **没有回填存量 120 封**。新增的 `has_attachments` 只对**今后同步**的邮件生效；
  库里已有的 120 封仍是 false，📎 标记对它们依然不显示。要回填只能重同步
  （或清 `last_synced_uid` 强制重拉），那是**会动真实账号状态**的操作，未做。
- **没有碰 `attachments` JSON 列**。它同样从未写入（`attachments_col_nonempty=0`），
  本轮只做了布尔标记。
- **没有验 POP3 路径**。它本来就正确（`ParseMIMEMessage`），本轮未改动、未加测试。

---

## §7dr 【需求 1/2】首次同步时超过 50 封的老邮件被**永久跳过** —— 实测 60 封丢 10 封，第二轮补不回来（2026-10-03）

### 缺陷机制（三段代码叠出来的）

IMAP 同步主流程里：

1. `criteria.UID` **只在 `LastSyncedUID > 0` 时**才设
   （`fetcher.go` 的 `if acc.LastSyncedUID > 0`）。所以**首次同步**搜索**没有 UID
   过滤**，返回 INBOX 里**全部** N 封。
2. `if len(uids) > 50 { uids = uids[len(uids)-50:] }` —— 只保留**最新 50 封**。
3. 循环末尾 `UpdateSyncState(ctx, accountID, int64(highestUID), ...)`，而
   `highestUID` 是**已插入的最大 UID**（从 `acc.LastSyncedUID` 起只增不减）。

三段叠加的后果：

> 首次同步 N>50 封 → 只落库最新 50 封 → `last_synced_uid` 被推到那 50 封里最大的
> UID → 下一轮搜索条件变成 `UID last_synced_uid+1 .. UIDNEXT` → **更老的那 N-50 封
> 再也搜不到**。不是「这轮不处理」，是**从此不在搜索范围内**。

### 与仓库既有原则直接冲突

`fetcher.go` 里紧挨着这段的注释明写：

> 「无新邮件时不推进 LastSyncedUID：语义是『已拉到的最大 UID』。若写成 uidNext
> ……下轮从 uidNext+1 起搜会**永久跳过**恰好分到 uidNext 的那封新邮件（真实踩中）」

那次修的是 **uidNext 方向**的洞，**留下了 50 封截断这个方向的同一个洞**。
判据按本会话一贯做法定为**修漏**：原则已写在代码注释里，缺的是遵守。

### 实测（新增 `fetcher_backlog_test.go`，用仓库自带的 in-process IMAP）

造 60 封邮件（>50 的截断阈值），跑 `Fetcher.Sync`：

```
=== RUN   TestSyncDrainsBacklogInsteadOfSkippingOldest
    second sync saved 0 —— 首批之外的 10 封被**永久跳过**了
                         （last_synced_uid 被推到最新 UID，更老的再也搜不到）
--- FAIL
=== RUN   TestSyncBacklogKeepsNewestFirstOrdering
--- PASS
```

**60 封丢 10 封，第二轮返回 0，且永远补不上。**

判据为什么必须连打两轮：只断言「首轮只落 50 封」证明不了丢失——那也可能是
分批设计。关键是**第二轮**：watermark 正确推进时第二轮必须把剩下的补齐。
修复前第二轮是 0，这是不变式被破坏的直接证据。

### 修法：取最老的 50 封，不是最新的

```go
if len(uids) > 50 {
    uids = uids[:50]      // 原为 uids[len(uids)-50:]
}
```

不变式：**watermark 只能沿着已处理的连续前缀推进。** 取最老的一段恰好满足——
每轮处理完 watermark 正好是这段末尾，下轮从下一封接着走，既不跳也不重
（`InsertEmail` 幂等）。积压会在若干轮后排空。

**代价（如实列出）**：积压很重时，新邮件要排在老邮件后面等几轮才出现。但老邮件
往往正是发票（需求 2 明确写「有可能我们需要多次操作才能下载到发票文件」），
优先收它们与需求方向一致。`TestSyncBacklogKeepsNewestFirstOrdering` 钉住了
**每轮 50 封的上限本身还在**——修 bug 不等于删上限。

### 负控 1 次

| 变异 | 结果 |
|---|---|
| 把 `uids[:50]` 改回 `uids[len(uids)-50:]` | **1 条转红**（`TestSyncDrainsBacklogInsteadOfSkippingOldest`） |

注意 `TestSyncBacklogKeepsNewestFirstOrdering` 在负控下**仍然全绿**——它只断言
`saved == 50`，两个变体都满足。真正承重的是那条「连打两轮」的用例。这正是
`threshold-predicates-need-the-common-value-negative-control` 那条教训：
判据要问「覆盖了哪些取值」，不是「跑过了几个用例」。

### 对需求的实际影响

- **需求 2「收取发票邮件」**：落在被跳过那批里的发票邮件**永远不会被采集**。
- **需求 3「发到飞书」**：同理，且**没有任何报错**——报告上「这轮 0 封新邮件」
  和「这轮没东西可收」长得一模一样，属于本仓库反复吃过的那类可观测性缺口。
- **需求 1「定时收信」**：首轮同步一个 444 封的信箱（§7de 记的 QQ 实测值）会
  永久丢掉约 394 封。§7cy 记的「6 封发票候选只建档 1 封」与这条同族。

### 数字

- `fetcher_backlog_test.go`：2 条（60 封排空 + 50 封上限），全绿
- `go test ./internal/email/`：**全绿**（117.2s；比 §7dq 的 75.1s 慢，因为新增
  两条要同步 60/55 封）
- `go test ./internal/server/`：只剩**两个既有失败**
  （`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，非邮件分支、非本轮引入）
- `gofmt -l` 新文件：无输出（干净）
- 生产代码改动：`fetcher.go` 一行（`uids[len(uids)-50:]` → `uids[:50]`）+ 注释

### 本轮**没有**做的事

- **没有回填存量**。库里那 120 封是被旧逻辑选出来的那一批，**更老的邮件现在仍然
  不在库里、也搜不到**。要捞回来必须把 `last_synced_uid` 清零强制全量重扫，
  那会**动真实账号状态并触发大量 IMAP 拉取**，需要单独授权，未做。
- **没有改 POP3 路径**。POP3 那条没有 UID watermark，用的是 UIDL 幂等，
  不受此 bug 影响（本轮未验证该说法，只是读过代码未见同类截断）。
- **没有测 444 封规模下的耗时**。50 封/轮 → 约 9 轮排空，间隔取决于
  `sync_interval_min`；这个排空节奏是否可接受**未量化**。
- **没有验真实 IMAP server**（同 §7dq，imapmemserver 不等于第三方实现）。

---

## §7ds 【需求 1】把「同步 N 封打几次 IMAP 往返」变成可测量的数字：实测 FETCH = N+1，50 封一轮 = 51 次串行往返（2026-10-03）

### 为什么量这个

`fetcher.go` 里紧挨着 `fetchSnippetOnConnected` 的注释自己写着：

> 这里是 Sync 里最可疑的一段：同一连接上**逐封串行**发部分取回，没有并发也没有
> 单独预算。企业微信（imap.exmail.qq.com）实测在这一步会挂到分钟级，而外层只能
> 看到 90s 上界。单独打点。

「可疑」不等于「有多少」。而 §7dr 把「每轮处理 50 封」变成了常态（积压按最老
50 封排空），于是「50 封 = 多少个串行往返」成了需求 1 定时收信路径上的一个
**必须量的数字**，不是直觉。

### 怎么量的：`imapserver.Options.DebugWriter`

go-imap 的 `imapserver.Options` 带一个 `DebugWriter io.Writer`，注释明写
「Raw ingress and egress data will be written to this writer」。在本进程里挂一个
缓冲，把 server 收发的**明文 IMAP 流**抓下来，用正则数命令出现次数即可。

- 不需要 MITM、不需要 Docker、不需要真实 server、不需要网络；
- **本文件只统计次数，绝不打印内容**——明文流里含登录凭据（DebugWriter 的
  文档也这么警告）。

### 实测（新增 `fetcher_rounds_test.go`，`go test -v` 输出）

```
n= 1 saved= 1 FETCH=  2 SEARCH=1  (每封 2.00 次 FETCH)
n= 5 saved= 5 FETCH=  6 SEARCH=1  (每封 1.20 次 FETCH)
n=10 saved=10 FETCH= 11 SEARCH=1  (每封 1.10 次 FETCH)
n=20 saved=20 FETCH= 21 SEARCH=1  (每封 1.05 次 FETCH)
n=50 saved=50 FETCH= 51 SEARCH=1  (每封 1.02 次 FETCH)
```

**干净的线性关系：FETCH = N + 1，SEARCH 恒为 1。**

拆开看：1 次是批量取 envelope 的（一次 `UID FETCH 1:50 (ENVELOPE ...)`），
**另外 N 次全部是逐封补 snippet 的**——因为批量 fetch 根本没请求 `BodySection`
（为了绕开 Greenmail 的 SP 分隔符问题），所以每封邮件的 snippet 都是空，
必然走 `fetchSnippetOnConnected` 补一次。

所以在 §7dr 之后的**生产轮次大小（50 封）下，单轮固定 50 次串行往返**，
这与注释里「企业微信实测挂到分钟级」的症状是吻合的。

### 第一版量出 0 的坑

最初在每个 `Write` 回调里切行统计，得到 FETCH=0。两个原因：

1. IMAP 命令行带 tag 前缀（`a001 UID FETCH …`），不以 `UID FETCH` 开头；
2. **DebugWriter 的 Write 边界与 IMAP 的行边界无关**，一条命令完全可能被切成
   两次 Write，按 Write 切行必然漏。

正确做法是整段缓冲在 `stats()` 里统一用正则扫。**判据要匹配真实格式**，
不是「大概长什么样」——第一版的正则「看起来」是对的，拿到 0 才发现。

### 负控 1 次

| 变异 | 结果 |
|---|---|
| 把 `if snippet == ""` 分支里的 `fetchSnippetOnConnected` 调用删掉 | n=10 的 FETCH **11 → 1**，**2 条转红**（`TestSyncUsesOneBatchedFetchForEnvelopes` 与 `TestSyncSnippetFetchesAreStillPerMessage`） |

说明计数器确实在数真东西，基线用例确实承重。已还原。

`TestSyncSnippetFetchesAreStillPerMessage` 是**故意钉住现状并把数字写死**的：
它是「把逐封 snippet 改成批量」这件事的基线，改动会让它转红——写死数字是为了
让「往返次数从 O(n) 降到 O(1)」有一个可对比的起点，而不是拍脑袋。

### 本轮**没有**改 snippet 补拉路径（这是一个有取舍的待拍板项）

把 N 次逐封补拉改成 1 次批量，方向明确（N+1 → 2），但**不是零成本**，所以
本轮只量不改：

- **带宽换往返**：当前每次取**整封**正文（`FetchItemBodySection` 没设
  `Partial`）。批量 50 封 = 单次传输 50 封正文；对带大附件的邮件，单次响应
  可能很大，甚至触发服务端/中间设备的响应上限。
- **原有的「部分取回」已被放弃过**：`fetcher.go` 的注释记录了
  `BODY[TEXT]<partial>` 曾导致部分 server 响应缺 SP 分隔符、imapwire 解析失败。
  也就是说「小体积」这条路已经试过并退回，批量方案要面对同样的兼容性面。
- **可观测性会变好**：现在每封一次 `tr.step("snippet uid=%d")` 打点，批量后
  只能看到一次，排查时定位不到具体哪一封卡住。

所以这是一个**需要拍板的性能/兼容性取舍**，不是我该单方面改的。建议的折中方案
（若要实施）：先按 UID 分组批量、每批 10~20 封，保留每封的 `tr.step` 打点，
把往返从 50 降到 3~6，同时把单次响应体量控制住。

### 顺带记录一个未修复的隐患（读代码得出，未复现）

IMAP 阶段的硬截止是 `time.AfterFunc(imapStageBudget, client.Close())`，
而 `UpdateSyncState`（写 `last_synced_uid`）**只在整个循环结束后调用一次**。
若某一轮在中途被 `client.Close()` 掐断，则：

- 已经 InsertEmail 成功的邮件**留住了**（不丢数据）；
- 但 `last_synced_uid` **没推进** → 下一轮重新搜同一批 UID → `InsertEmail`
  幂等（`ON CONFLICT DO NOTHING`）→ saved=0 → **同一批反复重试**。

§7dr 之前也存在这个风险；§7dr 之后**风险变大**了：现在每轮取的是最老 50 封，
若这批里有系统性慢的一封，它会**永久堵住整个积压**（新邮件排在它后面）。

**我没有修，也没有复现它**——本机 harness 里 `imapmemserver` 太快，无法在循环
中途稳定触发 `Close`。修法方向很清楚（每插成功一封就推进 watermark，或每 N 封
推进一次），但**没有实测支撑就不动**，与本轮其余改动的标准一致。

### 数字

- `fetcher_rounds_test.go`：3 条，全绿
- `go test ./internal/email/`：**全绿**（121.4s）
- `gofmt -l` 新文件：无输出
- 生产代码：**本轮只加注释**，无逻辑改动（§7dr 的改动已在上一提交）

### 本轮**没有**做的事

- **没有改 snippet 补拉路径**（如上，有取舍）
- **没有测真实 server 的单次往返延迟**。本机 harness 是 loopback，往返几乎免费；
  企业微信的每次往返实际耗时**未量化**，所以「50 次串行 = 分钟级」这个推论
  **不由本次实测支撑**，只由代码注释里的线上观察支撑。
- **没有复现「中途 Close 导致 watermark 不推进」**，理由见上。

---

## §7dt 【需求 4】2 天扫描窗口让 27/28 封未提醒的重要邮件**永远不会被提醒**；本轮只补可见性，不动窗口（2026-10-03）

### 缺陷

`notifyImportant` 里 `since := time.Now().AddDate(0, 0, -2).Unix()` 是**硬编码**的，
扫描只覆盖最近 2 天。落在窗口之外、`importance=high`、且从未提醒过的邮件：

> **不是「这轮没轮到」，是「不在扫描范围里」**——之后每一轮都不会再看到它。

报告里原本有 `RemindersSent` / `RemindersScanned` / `RemindersUnclassified`，
但 0 这个数**分不清**下面两种情况：

- 「这批邮件里确实没有重要的」
- 「有 27 封重要的，但它们太老了，永远不会被提醒」

§7cu 在真实数据上量到过后者（当时 25 封 high 里 20 封已被永久漏掉），但报告上
只能看到一个没有解释的 0。

### 实测（2026-10-03 09:12:31，schema `opencode_pocket`）

运行中的 pocketd 每分钟同步真实账户，所以任何「当前库」结论都带测量时刻。

```
total=121
high_total=52
high_unnotified=28
OUT_OF_WINDOW(>2d, high, 未提醒)=27
```

**28 封未提醒的重要邮件里，27 封在 2 天窗口之外**，也就是说它们**永远不会被提醒**，
只有 1 封落在窗口内。（§7cu 当时量到 25/20；现在 52/27，因为分类器在持续回填
`importance`——这也说明那个比例不是偶然，是结构性的。）

### 本轮只补可见性，**没有**动窗口

「窗口该多长 / 要不要过期 / 首次上线要不要限流」是产品取舍（早就在待拍板项里），
本轮**不擅自改**。只把那个不可观测的缺口补上，让取舍能带着实时数字做。

这与 `reminder_diag_test.go` 记录的 `RemindersUnclassified` 是同一类问题的
**时间维度**版本——同一个坑换一个方向又出现了一次，所以沿用同一套做法：
加一个计数 + 报告字段 + 日志，不改变任何既有行为。

### 改动

- `Store.CountHighImportanceOutside(ctx, before, limit)`（`store_pipeline.go`）：
  直接在 SQL 里数 `date < before AND importance='high' AND notified_at=0`，
  带 limit 防全表扫。
- `PipelineReport.RemindersOutOfWindow`（`pipeline.go`）：`json:"remindersOutOfWindow,omitempty"`。
- `notifyImportant`：扫描前先数，>0 时写报告 + 打日志说明「RemindersSent=0 有一部分
  是这个原因」。

**纯增量**：`git diff --stat` = `pipeline.go` +21 / `store_pipeline.go` +34，
零删除、零重排。（`gofmt -l` 报 `pipeline.go` 是仓库既有状态——只用了 `-l` 没用
`-w`，既有文件一行没被格式化。）

### 负控 1 次

| 变异 | 结果 |
|---|---|
| 把 SQL 里的 `importance = 'high'` 判据去掉（改成 `= ''`） | **1 条转红**（`TestReminders_OutOfWindowHighIsCounted`，得 0 想要 1） |

已还原。

### 判据为什么这么写

3 条用例覆盖三种「不该计入」的情况，因为这个计数一旦宽了就没意义：

| 邮件 | 期望 | 原因 |
|---|---|---|
| `e-old-high`（9 天前 / high / 未提醒） | **计入** | 正是要抓的那一类 |
| `e-old-medium`（9 天前 / **medium**） | 不计入 | 重要性不是 high，本来就不该提醒 |
| `e-old-done`（9 天前 / high / **已提醒**） | 不计入 | 不是漏掉，是已经做过了 |
| `e-fresh-high`（1 天前 / high） | 不计入（在第一组） | 它在窗口内，会被正常提醒 |

另有 `TestReminders_OutOfWindowStillNotifiesTheFreshOne` 钉住**加这个计数没有
改变原有行为**（窗口内那封仍被提醒，`RemindersSent=1`），以及
`TestReminders_OutOfWindowIsZeroWhenNothingIsOld` 钉住**没有老邮件时必须是 0**——
否则这个计数本身就变成又一个「0 分不清两种情况」。

### 数字

- `reminder_window_test.go`：3 条全绿
- `go test ./internal/email/`：**全绿**（77.7s）
- `go test ./internal/server/`：只剩**两个既有失败**
  （`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`，非邮件分支、非本轮引入）
- 生产代码：+55 / -0

### 由此可以怎么拍板（给决策用，不是结论）

现在有了实时数字，取舍变得具体：

- **保持 2 天**：那 27 封就一直不提醒，报告上会一直显示
  `remindersOutOfWindow=27` —— 至少不再是「没解释的 0」。
- **放宽窗口 / 去掉窗口**：`notified` 过滤本来就在（`splitReminderCandidates`），
  500 条上限也还在，所以不会重复轰炸；但**首次会有 27 条提醒一次性涌进来**，
  是否需要限流（首轮 N 条、其余排队）是随之而来的第二个问题。
- **改成分批消化**（每轮多扫一些老的）：需要额外一个「扫描从哪开始」的游标，
  比放宽窗口复杂，但首轮冲击可控。

这三条我都不替你选——它决定的是「你的收件箱第一次会响几声」。

### 本轮**没有**做的事

- **没有改窗口长度、没有加限流、没有加扫描游标**（都是产品取舍）
- **没有改 `splitReminderCandidates`**（它的逻辑与本计数不重叠）
- **没有验证通知渠道**（飞书/本地通知都未配置，真实提醒未跑通）

---

## §7du 需求 1「定时」路径：跑批真的发生了，但四个处理步骤全部空转

### 起点

前面 17 轮验的都是**手动入口** `POST /api/email/pipeline/run`。
需求 1 写的是「每天**定时**或手工进行邮件接收，然后进行处理」——
定时这一半一直只有「排期日志」的证据（§2），**没有到点执行的证据**。

### 意外发现：真实实例的日志里有到点执行的完整记录

`Get-Process pocketd` 指向的运行中实例来自**另一个 worktree**
（`C:\workspace\openpocket-wt-maildeploy`，启动于 2026-10-02 01:36，
不是本 worktree 的构建）。它的 stderr 有 93 MB：

```
logs\pocketd-18099-20261002-013559.err.log
```

搜 `[email/scheduler] pipeline` 命中两处，一处排期、一处**执行**：

```
01:36:00 [email/scheduler] daily pipeline runner injected (hour=8)
01:36:00 [email/scheduler] pipeline scheduled at 2026-10-02T08:00:00+08:00
...
08:00:00 [email/pipeline] step 1/5 sync 5 account(s) (t+2ms)
08:00:00 [email/pipeline] step1 sync feikemanager1@163.com new=0 in 318ms
08:00:00 [email/pipeline] step1 sync 56551681@qq.com new=0 in 523ms
08:00:00 [email/pipeline] step1 sync kimmy.huang@163.com new=1 in 363ms
08:00:00 [email/pipeline] step1 sync feikemanager@163.com new=1 in 346ms
08:00:01 [email/pipeline] step1 sync huangxutao@kxpms.cn new=0 in 1.125s
08:00:01 [email/pipeline] step 1.5/5 invoice candidates (t+1.126s)
08:00:01 [email/pipeline] step1.5 scanned=1 rawBodyFetches=0 fetchFailed=0 autoCreated=0
08:00:01 [email/pipeline] step 2/5 spam clean (dryRun=true) (t+1.133s)
08:00:01 [email/pipeline] spam dry-run: 0 mail(s) would be moved, 0 near-miss
08:00:01 [email/pipeline] step 3/5 important reminders (t+1.135s)
08:00:01 [email/pipeline] step 4/5 invoice harvest (t+1.135s)
08:00:01 [email/pipeline] step 5/5 push+ledger over 1 scope(s) (t+1.136s)
08:00:01 [email/pipeline] done synced=5 new=2 spam=0(+0 local) reminders=0
          inv={Processed:0 Downloaded:0 Pending:0 Failed:0 Skipped:0} feishu=0/0 errors=0
08:00:01 [email/scheduler] pipeline scheduled at 2026-10-03T08:00:00+08:00
```

**结论（需求 1 的「定时」这一半）**：

- 定时触发**真的发生了**，在 `08:00:00` 整点，5 个真实账户全部同步
- 触发后**正确排下一天**（10-03 08:00），没有变成每分钟重复触发
- `POCKET_EMAIL_PIPELINE_HOUR` 默认 8 生效
- `SetPipelineRunner` 在 `Start` 之后调用（`main.go:763` vs `:491`）这条补起路径**在生产上真的被走过**

所以 §2 那个 BUG-AM 的修复在真实部署上确认有效，不再只是单测保证。

**但同一份日志也把另一半摆出来了**：收信之外的四步——
清垃圾 `spam=0`、重要提醒 `reminders=0`、发票采集
`inv={Processed:0 ...}`、飞书推送 `feishu=0/0`——**全部为 0**。

### 根因：`scanned=1`

`step1.5 scanned=1` 是唯一的线索。回到 `pipeline.go:357-358`：

```go
func (p *Pipeline) extractInvoiceCandidates(ctx context.Context, accounts []Account, rep *PipelineReport) {
	emails, _, err := p.Store.ListEmailsSince(ctx, rep.StartedAt-86400, 500)
```

`store_pipeline.go:69` 的 SQL：

```sql
SELECT ... FROM emails WHERE date >= $1 AND COALESCE(deleted_at,0)=0 ORDER BY date DESC LIMIT $2
```

**按 `date`（邮件头日期）筛最近 24 小时**。

### 三个步骤三个窗口，同一个判据

`ListEmailsSince` 全仓三个生产调用点，窗口各不相同：

| 步骤 | 位置 | 窗口 | 库内 121 封的覆盖 |
|---|---|---|---|
| 发票候选 | `pipeline.go:358` | **1 天**（`rep.StartedAt-86400`） | **2 封（1.7%）** |
| 重要提醒 | `pipeline.go:741` | **2 天** | **8 封（6.6%）** |
| 清垃圾 | `pipeline.go:670` | **7 天**（`SpamLookbackDays`） | **78 封（64.5%）** |

覆盖率数字测量时刻 **2026-10-02 09:26:20**，
`SELECT count(*) … FROM emails WHERE COALESCE(deleted_at,0)=0`。

### 探针：用真实代码量，不用手抄关键词表

直接 psql 做关键词匹配是**不可靠的**——库里 snippet 有非 UTF-8 字节
（`invalid byte sequence for encoding "UTF8": 0xb7` 实测三次），
而且手抄 `invoiceKeywordHit` 的关键词表容易与代码漂移。

改写一个临时探针（跑完即删），**直接调用真实的
`ExtractInvoice` / `invoiceBodyReason`**，只读生产库：

```
MEASURED_AT=2026-10-02 09:24:59  cutoff_24h=2026-10-01 09:24:59
COUNTS        total=121  in_24h=2  out_24h=119
ENVELOPE_HIT  total=2    in_24h=1  out_24h=1
NEEDS_RAWBODY total=5    in_24h=0  out_24h=5
INVOICE_ROWS=2
BLINDSPOT_SAMPLE (6 封，全部在 24h 窗外):
  2026-09-22 uid=10424 hit=false reason="candidate" subj="来自 Apple 西湖商务团队的问候 - 杭州开轩科技有限公司"
  2026-09-23 uid=10432 hit=false reason="candidate" subj="AWS 账户提醒"
  2026-09-24 uid=10435 hit=true  reason=""          subj="您收到来自杭州创客家投资管理有限公司的发票，发票号码：2633200000826…"
  2026-09-29 uid=10443 hit=false reason="candidate" subj="Amazon Web Services Account Alert"
  2026-09-29 uid=10444 hit=false reason="candidate" subj="所需操作：AWS 账户提示"
  2026-10-01 uid=1298896142 hit=false reason="candidate" subj="Xiaomi MiMo API 开放平台扣款成功通知"
```

### 这不是「历史遗留」，是当前活跃状态

最关键的一组对比（测量时刻 09:23:00 / 09:26:02）：

```
total=121   by_date_24h=2    by_created_24h=121
oldest=2026-09-05 15:37:15+08   newest=2026-10-02 09:07:39+08
```

- **121 封的 `created_at` 全部落在最近 24 小时内**——它们是**刚刚**批量入库的
- 但它们的 `date`（邮件头日期）**跨越 2026-09-05 ~ 10-02 共一个月**
- 于是这 119 封**刚进库就被 24h 窗口排除**

这不是「去年攒下的旧数据没处理」，而是**当下正在发生的漏**：
任何一次全量回填、任何一次服务器停机超过一天、
任何一次 `last_synced_uid` 被清零，补进来的邮件日期都在窗口外，
**从进库那一刻起就永远不会被定时跑批看到**。

这与 §7dr（首次同步 >50 封老邮件被永久跳过）是**完全同型**的缺陷：
`fetcher.go` 的注释早就写明「不能永久跳过」这条原则，
同步层（§7dr）修了，**发票候选层留着同一个洞**。

### 为什么没有任何东西把它救回来

`emails.processed_at` 这一列**121 封全部为 0**——它从未被写入过。
所以库里**没有任何「这封邮件已被发票候选扫过」的痕迹**：

- 窗口内的邮件：每轮**重复**扫（幂等，不算错，但也没有记忆）
- 窗口外的邮件：**没有任何机制**会在未来把它们带回来

唯一的另一条入口 `server_assistant.go:2171`（同步后的异步提取）
**同样是 24 小时窗口**（`time.Now().Unix()-86400`），不构成补偿。

### 影响分级（按真实数据，不按推测）

- **发票（需求 2/3）——最硬**：`uid=10435` 是 `hit=true` 的**确定真发票**
  （主题带发票号码，envelope 层直接命中，连原文都不用拉），
  却因为日期在窗外**永远不会建档**。另外 5 封 `candidate`
  需要拉原文二次判定，也全在窗外。
  发票是财务凭证，漏采不可逆。
- **重要提醒（需求 4）**：§7dt 已把这条盲区做成报告里的
  `remindersOutOfWindow=27`，**看得见**了，且窗口本身是有意设计（产品取舍）。
- **清垃圾（需求 1）**：7 天窗口 + `dryRun=true` 默认安全阀，
  过期邮件不再清理**符合直觉**，不视为缺陷。

所以本节**只把发票这一条定性为缺陷**，另两条是设计取舍。

### 由此可以怎么拍板

修法有三条，取舍不同：

- **A. 按入库时间兜底**（候选条件加 `OR created_at >= <上轮时间>`）：
  改动最小，不需要新列。代价是每轮可能重复扫一批刚入库的旧 `date` 邮件。
- **B. 游标 / watermark**（记录「扫到哪封了」）：
  与 §7dr 的修法同构，语义最干净。代价是新增一个持久化位置。
- **C. 复用 `processed_at` 死列**当「已扫描」标记：
  正好激活一个语义完全吻合的死列（§todo 记录的 `processed_at` 死列），
  零迁移。代价是把一个从未用过的列赋予新语义。

**共同代价**：一旦窗口放开，上面那 5 封 `candidate` 会被**真实拉取 IMAP 原文**
（每封一次完整 IMAP 会话）。这是只读 FETCH，不改邮箱状态，
但确实是此前从未在无人值守场景发生过的负载。

「先只处理未建档的存量、把 24h 窗口留给新邮件」这一折中我**没有**实现，
因为它同样要拍板「存量补采到什么程度算够」。

### 本轮**没有**做的事

- **没有改任何窗口、没有加游标、没有动 `processed_at`**（都是产品取舍）
- **没有跑真实 IMAP 拉原文**（那 5 封 `candidate` 的最终判定需要授权）
- **没有验证 08:00 那次跑批的飞书/通知渠道**（未配置，`feishu=0/0` 无法区分
  「没得推」和「推了但失败」——这与 todo 里「飞书未配置静默跳过」是同一条）
- 探针脚本 `zz_probe_blindspot_test.go` 跑完已删（`git clean`），
  `go.mod` 被 `-mod=mod` 顺带改动（`x/image` indirect→direct）已 `git checkout` 还原，
  提交前 `git status` 为空

---

## §7dv 收尾核实：并发会话已独立修复同一缺陷，已 cherry-pick 同步

### 怎么发现的

写完 §7du 准备收尾时，核实一条**必须说清的边界**：§7du 那些日志证据
来自 `openpocket-wt-maildeploy` 那个 worktree 构建的二进制，不是本 worktree
的代码。若两个 worktree 的 `pipeline.go` 不同，我对那次 08:00 跑批的根因
分析就对不上。

实测两个文件差 **357 行**（200 插 / 157 删）——
而且 `maildeploy` 的 step1.5 **没有**我读的那行硬编码：

```go
// openpocket-wt-maildeploy（对方）
emails, _, err := p.Store.ListEmailsSince(ctx,
    rep.StartedAt-int64(invoiceCandidateLookbackDays)*86400, invoiceCandidateScanLimit)
```

`invoiceCandidateLookbackDays = 90`、`invoiceCandidateScanLimit = 2000`。
**这个缺陷在对方分支上已经被修过了。**

### 时间线：两次测量指向同一结论

| 时刻 | 事件 |
|---|---|
| 2026-10-02 01:35:53 | `pocketd.exe` 构建（maildeploy） |
| 2026-10-02 01:36:00 | 进程启动 |
| 2026-10-02 03:57:27 | 修复提交 `46d9e779` 落地（**在构建之后**） |
| 2026-10-02 08:00:00 | 定时跑批，`scanned=1` |

二进制构建早于修复 2 小时 21 分，所以**那次 08:00 跑批跑的确实是修复前的
24h 硬编码版本**——§7du 对它的根因分析成立，没有张冠李戴。

更值得记的是**两份独立测量的数字完全吻合**：

| | 我（§7du，09:24:59） | 对方（46d9e779 的 commit message） |
|---|---|---|
| 库内邮件 | 121 封 | 120 封 |
| 24h 窗口内 | 2 封 | 2 封 |
| envelope 命中的真发票 | `uid=10435`，号码 `2633200000826…` | 号码 `26332000008261110741` |
| 抽出的销售方 | — | 杭州创客家投资管理有限公司 / 3500.00 / 2026-09-24 |

两个互不知情的会话，同一天、同一份真实库、同一张发票、同一个数字。
121 vs 120 的差异是测量时刻不同（对方测于 03:57 之前，我测于 09:24，
期间有同步）。**这是本次排查里最强的交叉印证**。

### 已 cherry-pick 到本分支

`5f52b146`（cherry-pick `46d9e779`）——不重复造轮子。

两处冲突，逐处比对语义合并，**没有整文件覆盖**：

1. **`PipelineReport` 字段块**：对方加 `InvoiceCandidatesScanned` /
   `Created` / `BodyFetchDeferred`，本分支已有 `RemindersOutOfWindow`。
   **两侧都保留**，四个字段现在都在。
2. **`stripGoComments` 重名**（编译失败）：本分支
   `pgisolation_guard_test.go` 已有同名**词法级**实现（识别字符串/字符/
   反引号字面量），对方带来一个简化版。两者**语义不等价**——对方那份
   依赖「块注释保留内部换行」，因为 `extractFuncBody` 靠 `"\n}\n"`
   切函数体，块注释被压成空格会截错。故把对方那份改名为
   `stripGoCommentsKeepLines` 并注明分工，**不动已验证的判据逻辑**
   （`48f5c1a1`）。

### 自验负控（不只信 cherry-pick 过来的 commit message）

把调用点退回 `rep.StartedAt-86400, 500`，实测**转红 3 条**：

```
--- FAIL: TestExtractInvoiceCandidates_SeesInvoiceOlderThan24h
--- FAIL: TestExtractInvoiceCandidates_IsIdempotent
--- FAIL: TestExtractInvoiceCandidates_UsesLookbackConstantAtCallSite
```

还原后 `git diff --numstat` 对 `pipeline.go` 为空。
全包 `go test ./internal/email/ -count=1` **ok 77.4s**；
两个文件 `gofmt -l` 均无输出（`pipeline.go` 差异 0 行）。

**一次假绿要记下来**：第一次跑负控时用 PowerShell 的
`[IO.File]::ReadAllText/WriteAllText` 改生产代码，`.NET` 用的是**进程级
CWD**，不跟随 PowerShell 的 `cd`——文件实际改在主仓 `C:\workspace\openpocket`
下，**worktree 里的代码一行没动**，测试于是「全绿」。改用 Node 重做，
并在跑测试**之前**回读文件确认 `MUTATED=true`。
判据：凡是「改了 A 再看 B 变没变」的负控，**必须先验证改动真的落在目标文件上**，
否则报告的绿/红都没有意义。

### 对 §7du 拍板项的影响

§7du 列的三个修法选项（created_at 兜底 / watermark 游标 / 复用
`processed_at` 死列）**已被并发会话用第四种方式回答**：
直接放宽到 90 天 + 扫描上限 2000。理由写在 `pipeline.go` 的常量注释里——
envelope 判定不碰 IMAP，放宽几乎零代价；贵的拉原文仍受
`maxInvoiceBodyFetches = 24` 预算限制。

这个选择**绕开了**我担心的「扫描上限 500 会被最近邮件占满」问题
（`ORDER BY date DESC` + LIMIT），并同步把上限提到 2000。

因此 §7du 的选项 3 作废，**不再需要你为「窗口定多宽」拍板**。
仍然需要你定的只剩一件：

> **存量补采要不要现在做。** 90 天窗口一开，下次定时跑批会为那 5 封
> `candidate` 各开一次完整 IMAP 会话拉原文（只读 FETCH，不改邮箱状态），
> 并把 `uid=10435` 那张真发票建档 + 下载附件 + 推飞书。
> 不开自动补采的话，可以改成手动 `POST /api/email/pipeline/run` 触发一次。

---

## §7dw 两条否定结论 + 又一次并发会话的独立修复

### 起点：查 §7ds 留下的待修项

§7ds 记了一条「**待修·未复现**：watermark 只在循环末尾写，中途 Close 会导致
同一批反复重试」。这条一直没动，本轮先把它查清楚。

### 否定结论 1：那条待修项的前提不成立

`fetcher.go` 的逐封循环体（781–928 行）**没有任何 `return` 或 `break`**，
只有两处 `continue`：

- `if m.Envelope == nil { continue }`（:782）
- `if err := f.store.InsertEmail(...); err != nil { continue }`（:920）

IMAP 阶段的兜底 `stopIMAPStage`（:675 `time.AfterFunc` → `client.Close()`）
即使中途把连接掐断，`fetchSnippetOnConnected` 也只是返回空串，循环照样跑完
到 :929 的 `UpdateSyncState`。

**所以「watermark 只在循环末尾写」是对的，不是缺陷**——「未复现」是它的
正确状态。§7ds 那条待修项应当撤下。

### 但读代码时发现了一个更精确的问题

`highestUID` 的推进方式是**取已插入邮件的最大 UID**（:925-927），
而不是 §7dr 注释里明确声明的「watermark 只能沿着**已处理的连续前缀**推进」。
UID 1,2,3,4,5 中若第 3 封插入失败，watermark 仍会跳到 5 —— 第 3 封从此
不在搜索范围内，与 §7dr 修掉的那个洞是同一类。

而且 `m.Envelope == nil` 那处 `continue` **一行日志都没有**，跳过是静默的。

### 否定结论 2：这个缺陷在真实数据上没发生过

在决定改之前先量 blast radius（93MB 真实日志，进程 01:36–09:21）：

| 模式 | 次数 |
|---|---|
| `insert email uid=` 失败 | **0** |
| `fetch:` 错误 | **0** |
| `search:` 错误 | **0** |
| `sync ... failed` | **72** |

`InsertEmail` 用 `ON CONFLICT (id)`，自然失败确实很难触发——真实日志里一次
都没有。**于是没有改**：把一条从未发生的路径改成新逻辑，代价是引入新的
watermark 语义，收益是零。这条留作「已知理论缺口，有日志可监控」。

### 那 72 次失败是什么

```
40 次  huangxutao@kxpms.cn
32 次  56551681@qq.com
全部同一个原因：imap failed and no time left for POP3 fallback
```

展开一条完整上下文：

```
09:33:01  huangxutao@kxpms.cn sync trace total 1.045s      ← 成功
09:34:00  feikemanager1@163.com sync trace total 292ms
09:35:00  feikemanager1@163.com sync trace total 288ms
09:35:01  56551681@qq.com    sync trace total 491ms
09:35:20  imap login huangxutao@kxpms.cn failed: i/o timeout
          — trying POP3 fallback (budget -10s left)
09:35:20  SLOW step login took 1m20.019s
09:35:20  [scheduler] sync acct-... failed
```

`budget -10s left` 与 `login 1m20.019s` 这两个数字，正是 `fetcher.go:231-240`
注释里写着「**已修**」的两个症状（idle/3=20s 的两次续期把 deadline 推到 80s，
连带把 70s 总预算吃穿）。第一反应是「修复没生效」——

### 否定结论 3：那是旧二进制，不能报成现存缺陷

核实构建时间线：

| 时刻 | 事件 |
|---|---|
| 2026-10-02 00:09:11 | 仓库 HEAD = `a4965052` |
| 2026-10-02 01:35:53 | **`pocketd.exe` 构建** |
| 2026-10-02 03:57:27 | `46d9e779` 发票窗口 90d 修复 |
| 2026-10-02 09:29:37 | `c459400a` last_synced_at 修复 |

二进制比当前 HEAD 落后 **8 小时**。而且 `stopIMAPStage`（IMAP 阶段 50s 兜底
Close）在 `feat/mail-config-deploy` 分支上**根本搜不到**——它只存在于本分支。

**结论：这 72 次失败是 8 小时前的旧代码的行为，当前代码里已有兜底。**
把它写成「生产仍在发生的缺陷」是错的。教训：拿到一份运行日志，
第一件事是确认它是**哪次构建**跑出来的。

### 但它暴露的真问题在当前代码里仍然成立

`09:33:01` 成功、`09:34:00` 起连续失败 80s、然后**每分钟再来一次**——
因为 `pollLoop` 的到期判据是

```go
if a.LastSyncedAt > 0 && now-a.LastSyncedAt < intervalSec { continue }
```

而 `UpdateSyncState` 只在**同步成功**时才写。失败 ⇒ `last_synced_at` 不变
⇒ 下一轮照判到期。配置 15 分钟的间隔保护，对故障账户**形同虚设**。

并发会话已在 `c459400a`（09:29:37）独立修掉，统计与我的量测同量级
（它数 1430 条 sync trace，我数 1494 条）：

```
feikemanager@163.com   40 次 → ~12.3 分钟  ✅
kimmy.huang@163.com    39 次 → ~12.4 分钟  ✅
56551681@qq.com       435 次 → ~64 秒     ❌
huangxutao@kxpms.cn   427 次 → ~65 秒     ❌
feikemanager1@163.com 489 次 → ~57 秒     ❌
```

### 已 cherry-pick（`2ee85dc0`），无冲突

- `fetcher.go`：IMAP 空结果路径写回 **UID 原值** + 刷新时间戳（两个 UID 与
  时间戳是不同的事，写 `uidNext` 会永久跳过恰好分到该 UID 的新邮件）；
  POP3 兜底两条成功出口都补上
- `scheduler.go`：内联判据抽成纯函数 `accountDueForSync`，测试与生产共用
  同一份判据（测试里抄一份的写法，漂移了也测不出来）

自验：`go build` 0、`go vet` 0、全包 `ok 87.19s`。
负控把 `accountDueForSync` 改成恒 true，实测转红
`TestSyncWithNoNewMailAdvancesLastSyncedAt` +
`TestAccountDueForSync` 的 5 个「未到期」子例，3 个「本就应到期」子例仍绿
（阈值判据无盲区）。还原后 `git diff --numstat` 为空。

### 又一次假绿，记下来

负控脚本第一版 needle 写的是 `\n`，而 `internal/email` 整个包是**纯 CRLF**，
于是 `includes()` 匹配不到、脚本按设计 `exit 2` 拒绝执行。

**但我还是接着跑了测试**，于是又拿到一个无意义的 `ok`。
第二次改对（`\r\n`）并加了 `if ($LASTEXITCODE -ne 0) { exit 1 }` 让脚本
失败即中止测试。

加上上一节记的 `.NET` 进程级 CWD 那次，这已经是**连续两次**因为
「变异没落到目标文件」而拿到假绿。固化成一条规矩：

> 负控脚本必须**自己校验改动已落盘**（回读目标文件、断言标志位），
> 并且**校验失败就不许跑测试**——否则「全绿」这个信号毫无意义，
> 比没有负控更危险。

---

## §7dx 补齐 Scheduler.Stop 的幂等性（仓库既定模式被漏掉的一处）

### 为什么做

上一轮 §7dw 收尾时把它列为「待定」。本轮查清后按**修漏**处理——
仓库里已有既定模式，email 这个是漏掉的：

```go
// internal/scheduledtask/scheduler.go:244
// Stop requests a graceful stop and waits for in-flight executions. It is
// idempotent and safe when Start was never called.
func (s *Scheduler) Stop() {
	if s == nil { return }
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}
```

另有 `redclaw/bridge_test.go` 里有显式的 `// Second stop should not panic` 用例。

email 侧原本是裸 `close(s.stop)`（:263）。**当前生产路径上二次 Stop 不可达**
（`main.go:492` 只有一处 `defer emailScheduler.Stop()`，`:491` Start 也只调一次），
所以这是**潜在健壮性缺口而非活跃 bug**——但「不可达」不是「安全」。

### 改了什么（+21 / -3）

```go
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stop) })
}
```

外加 struct 里一个 `stopOnce sync.Once`。

**刻意不做** `wg.Wait()`：这里的 loop（pollLoop / pipelineLoop / …）是裸
`go` 起的，没有 WaitGroup 可等；而 pipelineLoop 等待时持有一次最长 30 分钟的
`runner.RunEmailPipeline` 调用，让 `Stop` 阻塞那么久本身就是新问题。
优雅收尾是另一件事，不在本次范围内。

### 三条断言分三个层次

只断言「不 panic」是不够的——那只能证明 close 没被调第二次，**证明不了
loop 真的停了**。新增 `scheduler_stop_test.go`：

| 用例 | 断言的性质 |
|---|---|
| `TestStop_IsIdempotent` | 连续三次 Stop 不 panic |
| `TestStop_SafeWhenNeverStarted` | 未 Start 就 Stop 安全；nil 接收者安全 |
| `TestStop_EndsPipelineLoopBeforeTrigger` | **loop 真的退出** |

第三条用恒定冻结时钟把触发点固定在 400ms 后，在 50ms 处 Stop，
然后断言 750ms 后 `runner.calls == 0`。

### 写第三条时被自己的时钟打了一次

第一版复用了 `scheduler_pipeline_test.go` 的 `fakeClock`，**用例直接转红**：
```
Stop 之后 pipelineLoop 仍然触发了 1 次
```

查下来是**测试设计错了，不是实现错了**。`fakeClock` 第二次起返回
「已过触发点」的时间（它本来的用途是让 loop 第二轮把下次触发排到 24h 后），
而 `pipelineLoop` 一轮里要取两次 now：

```go
next  := nextTimeAt(s.now(), hour, 0, 0)   // 第 1 次：fakeNow  → next = triggerAt
delay := next.Sub(s.now())                 // 第 2 次：afterNow → delay = -200ms
if delay < 0 { delay = 0 }                 // → clamp 成 0，loop 立刻触发
```

于是 loop 在**启动瞬间**就跑完了，Stop 根本没来得及生效。改用恒定
`frozenClock` 后正常。

值得记的是：**这条用例是「红→修」而不是「红→改测试」**——
第一次转红先假设是实现有问题，读完 `pipelineLoop` 才发现是时钟语义不匹配。

### 负控 2 路，互补（各自覆盖不同性质）

| 负控 | 幂等 | nil 守卫 | loop 退出 |
|---|---|---|---|
| A：退回裸 `close(s.stop)` | 🔴 转红 | 🔴 转红 | 🟢 绿 |
| B：保留 `Once` 但不 `close` | 🟢 绿 | 🟢 绿 | 🔴 转红 |

第一路里第三条**仍绿**——它测的是另一个性质（Stop 有没有让 loop 退出），
不是本次修复的承重用例。**没有就此放过它**：单跑第二路负控（保留 Once
结构、只把 `close` 换成 `_ = s.stop`），确认第三条确实转红。

两路互补才说明三条断言没有冗余也没有缺口。这个「第一路只红一部分」不是
意外——它是「判据要问覆盖了哪些取值，不是跑过了几个用例」的又一个实例。

### 两个操作教训

1. **负控后不要用 `git checkout --` 还原**。它会连**自己未提交的正式修改**
   一起还原掉（本次把 `stopOnce` 的改动一起冲掉了，只能重做一遍）。
   改成让负控脚本自己带 `apply|restore` 两个模式，反向替换回去。
2. **一次测量方法本身出了错，不能当证据**：想量「`scheduler.go` 改动前的
   gofmt 基线」，用 `git show HEAD:… > file` 导出后再 gofmt，量出 2 行差异；
   但 PowerShell 5.1 的 `>` 重定向会改行尾，把基线自己污染了。改成直接看
   `gofmt -d` 里我改的那几行的形态——`-`/`+` **内容完全相同**，是纯
   CRLF/LF 差异，而 `scheduler_stop_test.go` 根本没被 `gofmt -l` 列出，
   才是能站得住的证据。

### 回归

`go build ./...` 0、`go vet ./internal/email/` 0、
`go test ./internal/email/ -count=1` 全包 ok。

---

## §7dy 迟来的 `-race`：本机一直能跑，之前 20 轮一次都没用

### 怎么发现的

改完 `Scheduler.Stop()`（加了 `sync.Once`）之后复核自己的验证链，
发现一件事：**这一整轮改的全是并发代码**（scheduler 的 loop、fetcher 的
goroutine、`sync.Once`），但从头到尾只跑过普通 `go test`，
一次都没带 `-race`。

于是去查本机到底能不能跑。

### 能跑，而且一直能跑

```
$ Get-ChildItem C:\tools -Recurse -Filter gcc.exe
C:\tools\w64devkit\w64devkit\bin\gcc.exe        # GCC 16.2.0, x86_64-w64-mingw32

$ go env CGO_ENABLED CC
                                                   # 两者都是空（= 未设置）

$env:PATH='C:\tools\w64devkit\w64devkit\bin;'+$env:PATH
$env:CC='C:\tools\w64devkit\w64devkit\bin\gcc.exe'
$env:CGO_ENABLED='1'
go test -race ./internal/email/ -count=1
```

**「本机跑不了 `-race`」是一个被沿用过的错误结论**——它只搜过 PATH 和几个
猜测目录，没搜 `C:\tools`。这个假阻塞还有个更坏的副作用：它会伪装成
「无法验证，只能请你贴日志」，把本该自己完成的工作推给用户。

### 实测结果

先验本轮改动直接相关的部分：

```
--- PASS: TestPipelineLoop_FiresWhenRunnerInjectedBeforeStart
--- PASS: TestPipelineLoop_FiresWhenRunnerInjectedAfterStart
--- PASS: TestPipelineLoop_NegativeHourDisablesSchedule
--- PASS: TestPipelineLoop_RepeatedInjectionStartsSingleLoop
--- PASS: TestStop_IsIdempotent
--- PASS: TestStop_SafeWhenNeverStarted
--- PASS: TestStop_EndsPipelineLoopBeforeTrigger
--- PASS: TestAccountDueForSync
ok  github.com/halfking/pocket-opencode/backend/internal/email  2.795s
```

**0 DATA RACE**。然后跑整包：

```
$ go test -race ./internal/email/ -count=1
ok  github.com/halfking/pocket-opencode/backend/internal/email  94.111s
```

**0 DATA RACE**（非 race 时同一包是 85.101s，race 开销约 +11%，
这个比例说明 race detector 确实在工作，而不是被静默跳过）。

注意口径：上面那句「全绿」必须带上 `-race` 才成立，
不带 `-race` 的全绿**不能**用来声称并发安全。

### 为什么要专门记一条

`sync.Once` 这类改动的正确性判据就是并发安全性，而 `-race` 是唯一能给出
「没有数据竞争」这个结论的工具。**只跑普通测试得到的「全绿」与并发安全
是两回事**——普通测试不会因为缺同步而失败，它只会**碰巧**没触发。

本轮 20 个提交里，涉及并发的那几处（`pipelineOnce` 补起 loop、
`SetPipelineRunner` 的 `startMu`、`accountDueForSync` 的抽取）
此前都只有非 race 的测试背书。从现在起，本分支的回归口径改为：

```
go test -race ./internal/email/ -count=1     # 涉及并发时必须带
go test ./internal/email/ -count=1           # 其余可用
```

### 顺带：`-race` 也不能替代负控

race detector 只能证明「这段并发代码没有数据竞争」，证不了「Stop 幂等」
（「二次 close 会不会 panic」是逻辑性质，不是数据竞争）。两者是互补的，
不是二选一。

---

## §7dz 两个红了一天的失败：不是「语义分歧」，是 11 分钟内两个提交造成的漏改

### 起点

上一轮发现本机能跑 `-race` 后，我说下一步用它重跑 `internal/server`——
那正是每轮回归都出同样两个失败、却被我记成「404/403 语义分歧、
非邮件分支、非本轮引入」的那包。

### `-race` 先给出一个否定结论

```
$ go test -race ./internal/server/ -count=1
--- FAIL: TestTaskWriteGuardBlocksPlainMemberPatch (0.44s)
--- FAIL: TestTaskWriteGuardBlocksPlainMemberDelete (0.39s)
FAIL  github.com/halfking/pocket-opencode/backend/internal/server  58.060s
```

**0 DATA RACE**，仍然只有这两个。并发层面没问题，失败是逻辑层面的。

### 我此前的定性是错的

拿失败详情：

```
task_write_guard_route_test.go:108: bob PATCH someone else's private work item
    = 404, want 403: task not found
task_write_guard_route_test.go:130: bob DELETE someone else's private work item
    = 404, want 403: task not found
```

**代码返回 404，测试期望 403**。我记的「语义分歧」暗示两边各有道理、
需要人来裁；实际不是——有一方是**过时的**。

而且失败指向 `:108`/`:130`，而我几轮前读同一文件时那个断言在 `:123`/`:145`，
内容还是「期望 404 + 一整段论证」。行号对不上，说明文件**已经变了**，
而 `git status` 是干净的——即这是**已提交**的改动。

### 两次提交，相隔 11 分钟

| 时刻 | 提交 | 做了什么 |
|---|---|---|
| 2026-10-01 **03:25:35** | `d8237d92` | 引入 `workItemWriteGuard`，读不到→404、**没权限→403**；同一提交写下测试，期望 **403** |
| 2026-10-01 **03:36:44** | `0c128c9a` | 在守卫里把 `CanReadWorkItem` 提到 `CanWriteWorkItem` **前面**，读不到就 404；**只改了 server.go（58 行），没动测试** |

第二个提交之后，**这个测试就一直是红的**。`0c128c9a` 的注释把理由写得很清楚：

```go
// id 仍然与「不存在」不可区分，GetTaskScoped 已保证这一点。
actor := s.userIDFromRequest(r)
if !task.CanReadWorkItem(current, parts, actor) {
    http.Error(w, "task not found", http.StatusNotFound)
    return nil, false
}
if !task.CanWriteWorkItem(current, parts, actor) {
    http.Error(w, "not the owner or a participant of this work item", http.StatusForbidden)
    return nil, false
}
```

403 会回答「这个 id 存在，你只是不能改」——那等于把**写路径**变成对不可见
id 的**存在性预言机**，而读路径已经防着这件事（`GetTaskScoped` 的注释：
"A cross-tenant ID is reported the same as a missing one"）。写路径一旦泄露，
读路径的保护就被抵消。

**所以：生产代码的当前行为是有意且有论证的，测试停在了 11 分钟前的中间状态。**
按「仓库已有既定政策被漏掉 = 修漏」处理，只改测试、不动生产代码。

### 顺带：403 分支不是死代码，缺的是能触发它的夹具

`internal/task/access.go`：

| | 条件 |
|---|---|
| `CanReadWorkItem` | owner / participant / **`Visibility == VisibilityWorkspace`** |
| `CanWriteWorkItem` | **只有** owner / participant |

所以「workspace 可见但非 owner/参与者」的任务 → **能读不能写 → 403**。
403 分支完全可达。

而原夹具里两个任务**都是 `VisibilityPrivate`**，于是 bob 两次都落在
「读不到」那一侧 → 全部 404。**`CanWriteWorkItem` 那条 return 在路由层
从未被执行过一次**——而那正是这次安全修复的主体逻辑。

### 改了什么（测试文件，+74/-6）

1. 夹具加 `wtg-shared`：`VisibilityWorkspace` + owner=carol + 参与者只有 carol
   ⇒ bob 能读、不能写 ⇒ 403 唯一入口。
2. 两条现有断言 403 → **404**，并把「存在性预言机」的论证写回注释
   （`d8237d92` 当时删掉的那段，内容仍然正确且必要）。
3. 新增 `TestTaskWriteGuardReadableButNotWritableIs403`：
   先断言 **bob 对该任务 GET = 200**（前置条件，防止它悄悄退化成又一条 404 用例），
   再断言 PATCH/DELETE 都是 403，最后回查标题未被改写。

### 负控 2 路，互补

| 负控 | 两条 404 用例 | 新增 403 用例 | 其余 4 条 |
|---|---|---|---|
| A：删掉 `CanReadWorkItem` 层 | 🔴 转红 | 🟢 绿 | 🟢 绿 |
| B：把 `CanWriteWorkItem` 的 403 改成 404 | 🟢 绿 | 🔴 转红 | 🟢 绿 |

A 还原的是 `d8237d92` 那个 03:25 的状态——**它精确复现了原始报错**，
说明修复方向对得上；B 证明新增那条不是凑数的。
两路都跑，才说明「404 钉住的是读权限层、403 钉住的是写权限层」，
没有冗余也没有缺口。

第一路 A 只红一部分**不是意外**：这正是「判据要问覆盖了哪些取值，
不是跑过了几个用例」——之前 §7dx 的 `TestStop_EndsPipelineLoopBeforeTrigger`
是同一个形态。

### 顺带一个更可靠的格式测量法

`gofmt -l` 把 `task_write_guard_route_test.go` 列出来了（266 行 CRLF）。
上一轮我用来量「基线」的方法（`git show HEAD:x > file`）会被 PowerShell 的
重定向改行尾，不可用。这次换成**先归一 CRLF 再 gofmt -d**：

```
task_write_guard_route_test.go   CRLF=266   归一后实质差异 = 0 行   ← 我改的
server.go                        CRLF=2333  归一后实质差异 = 8 行   ← 我没碰
```

`server.go` 那 8 行是 import 顺序与结构体对齐，**仓库既有状态**，与本次无关。
两个数字分开看才有意义——「某文件不在 `gofmt -l` 名单里」和「它符合 gofmt」
是两回事。

### 回归

```
$ go test -race ./internal/server/ -count=1
ok  github.com/halfking/pocket-opencode/backend/internal/server  86.521s
```

**0 失败、0 DATA RACE**——这是该包第一次整体全绿（此前每轮回归都固定带
那两个失败）。非 race 时同一包 58.060s。

本次**只改测试文件**（`git diff --numstat` 仅一项），生产代码零改动。

---

## §7ea 「learning 三条待改」的查清：一个 grep 参数引发的跨 worktree 误判

### 起点：一条我记了很多轮、却从没查过内容的待办

任务列表里挂着一条「learning 三条待改」。我记了它很多轮，
但**从来没查过它到底指哪三条**。本轮决定查。

### 第一次检索就出错了

`grep` 不带 `path` 参数时，检索的是 **workspace 根**
（`C:\workspace\openpocket`），**不是**我干活的 worktree
（`C:\workspace\openpocket-wt-email`）。

后果很具体：

- 我用 `read` 传 worktree 的**绝对路径**读 `learning/store.go`，
  看到的是 `if capturedAt > 0 { out = append(out, capturedAt) }`——**未修版本**
- 紧接着 `grep` **不带 path**，看到的是同一文件里有
  `sinceUnix is applied **per timestamp**, not per row` 的论证注释、
  以及一个 `store_pg_regression_test.go` 精确复现该缺陷的回归测试
- `git status` 对该文件是**干净**的，`LastWriteTime` 是 **10-01 10:26**（今天没动过）

「文件在我读之后变了」的第一反应应该是并发会话在写，但**文件今天根本没被改过**——
真因是两次检索看的**不是同一个 worktree**。

这个坑我在 §7dz 踩过一次（那时以为是并发会话改了
`task_write_guard_route_test.go`，行号对不上），现在知道真因了：
**主仓已修好（404 版本），worktree 还没跟上（403 版本）。**

### 同一个坑的两次表现，串起来才看得见

| | 主仓 `C:\workspace\openpocket` | 我的 worktree `openpocket-wt-email` |
|---|---|---|
| `task_write_guard_route_test.go` | 期望 **404** + 存在性预言机论证 | 期望 **403**（过时） |
| `learning/store.go` | 已修（`capturedAt >= sinceUnix`）+ 论证注释 + 回归测试 | **未修**（`if capturedAt > 0`） |

两边是**同一个仓库的不同 worktree**，各自 checkout 在不同提交上。
**worktree 的 HEAD SHA 只说明它 checkout 在哪，不代表文件内容。**

### 追溯：主仓的 `a1dd9013` 已经把 learning 两条都修了

`a1dd9013`（2026-10-01 11:10:55）`fix(learning): 延后提醒会往回拉、连续天数越过 since 边界虚增`：

1. **`SnoozeReminder` 用 `now+minutes` 覆盖 `next_due_at`** ——
   对一个 24 小时后才到期的提醒延后 120 分钟，提醒变成 `now+2h`，
   **用户说「晚点提醒我」反而提前 22 小时触发**。改用
   `GREATEST(next_due_at, now) + minutes*60`。
2. **`ActiveDayTimestamps` 的 since 只过滤行不过滤时间戳** ——
   正是我上面读到的那段。SQL 用 `(captured_at >= since OR updated_at >= since)`
   选中行后，Go 把两个时间戳都无条件 append，「很久以前采集、刚刚更新」的行
   会吐出久远的 `captured_at`；而这是**连续天数的输入**，多吐一个窗口外
   时间戳等于给用户记上未活跃的一天。

**第 1 条正是待办里「learning 三条待改」的根因 3** ——
而我此前记的是「产品语义、只有这一个测试钉住、无第二处证据、**不擅自改**」。
**主仓已经定了**：snooze 从「原排期再推」改成「从 max(原排期, 现在) 再推」，
并用 `RETURNING` 读回实际落库值。

**所以待办「learning 三条待改」作废**——不是被我改的，是主仓先定了并修了。

### 顺带一次全仓 `-race`，以及它带来的两条方法论更正

借 §7dy 刚打通的 `-race` 能力跑了一次全仓：

```
$ go test -race ./... -count=1
EXIT=1   ok=51  no-test=18  FAIL=1  build_failed=0  DATA RACE=0
```

**70 个包里只有 `internal/learning` 红**，失败用例正是上面两条。
`internal/server` 那两个 404/403 已在 §7dz 修掉，不在列表里。

**自查一（防 OOM 静默跳过）**：该提交的 message 警告「默认并发下 `go build` 会
OOM，随机若干包报 `[build failed]` 且集合每次不同」。所以不能只看 `go test` 的
输出就断言「其余都好了」：

```
build_failed_count = 0
ok(51) + no-test(18) + FAIL(1) = 70
go list ./... 独立核对          = 70     ← 两个独立方法对上
```

**自查二（防静默 SKIP）**：该提交还指出「此前几轮全量未设
`POCKET_TEST_POSTGRES_DSN`，依赖真 PG 的 4 个用例被**静默 SKIP** 而非 PASS，
『零 FAIL』因此比实际证据弱」。本轮两次全量都**显式设了该变量**，且单独统计
`--- SKIP` 计数。

**更正到本分支的回归口径**：

| 项 | 要求 |
|---|---|
| 退出码 | 用 `*> $log` 重定向后读 `$LASTEXITCODE`，**不要接管道**（`Select-String` 会顶掉退出码，把「全过」和「没查到 FAIL」变成同一个 (空, 0)） |
| 缓存 | `-count=1`，并确认 `cached=0` |
| DSN | 需要真 PG 的判定**必须**设 `POCKET_TEST_POSTGRES_DSN`，并查 `SKIP` 计数 |
| 并发 | `-p 2`（默认并发会 OOM；集合随机，不能复现） |
| 覆盖 | `ok + no-test + FAIL` 要与 `go list ./...` 的包数对上 |
| race | 涉及并发的包必须带 `-race` |

### 处置

`cherry-pick a1dd9013` → `47895e2c`，**无冲突**（4 个文件，含
`store_pg_regression_test.go` 与它的 handoff 文档）。

验证：

```
$ go test -race ./internal/learning/... -count=1 -v
EXIT=0   cached=0   skip=0   fail=0
--- PASS: TestSnoozeNeverPullsAReminderForward
--- PASS: TestSnoozeOfAnOverdueReminderStillLandsInTheFuture
--- PASS: TestActiveDayTimestampsNeverReturnsBeforeSince
--- PASS: TestActiveDayTimestamps
--- PASS: TestStoreAndStreakAgree
--- PASS: TestReminderLifecycle
```

`skip=0` 是这里最该看的一个数——它证明这些依赖真 PG 的用例**真的执行了**，
而不是「零 FAIL」掩盖下的静默跳过。

### 同步完 learning 后，全仓冒出**一个新失败**：`TestNoGoFileHasUTF8BOM`

```
bom_guard_test.go:110: 1 个 .go 文件以 UTF-8 BOM 开头，会让覆盖率插桩构建失败：
bom_guard_test.go:112:   internal\learning\store.go
```

不是我的改动引入的——**主仓那个 `learning/store.go` 本来就带 BOM**，
cherry-pick 只是原样同步（实测两边都是 BOM，而两边的 `server.go` 都无 BOM）。
`bom_guard_test.go` 向上找 `go.mod` 后扫**整个 module root**，
所以主仓跑同一个测试同样会红。

`a1dd9013` 改过这个文件（40 行），BOM 很可能就是那次编辑带进去的——
commit message 自己也点名了环境坑（PowerShell 的 `Set-Content -Encoding UTF8`
与 `>` 重定向都会写 BOM）。

**修法刻意只切 3 字节**：用 Node 读 Buffer、判前 3 字节、slice 掉再写回。
不重新编码、不碰行尾、不碰任何其他内容——直接 `WriteAllText` 重写整个文件
会把 CRLF 一起规范化，产生上百行与本次无关的 diff。

```
size_before=21018  had_BOM=true
size_after=21015   BOM_removed=true
```

**独立复核**（不只信那个守卫）：用一段自己的 Node 遍历全仓 `.go` 逐个判 BOM，
结果 `none`——确认没有第二个带 BOM 的文件。

**负控**：把 3 字节 BOM 加回去，`EXIT=1` 并准确报出那一个文件；再还原。

```
bom_guard_EXIT=0   learning_EXIT=0        # 修后
negctl_EXIT=1      --- FAIL: TestNoGoFileHasUTF8BOM   # 加回 BOM
```

### 终验：本分支第一次全仓全绿

```
$ go test -race ./... -count=1 -p 2
EXIT=0
ok=52   no-test=18   FAIL=0   合计=70
skip=0            ← 依赖真 PG 的用例没有被静默跳过
DATA RACE=0
go list ./...  = 70   ← 两个独立方法对上
```

**这是本分支第一次全仓全绿**，且绿得「有据」而不是「看起来绿」：
带 `-race`、显式设了 `POCKET_TEST_POSTGRES_DSN`、`skip=0` 证明那些依赖真
PG 的用例真的执行过、包数与 `go list` 对上证明没有包被 OOM 静默跳过。

对比本轮开头：同一个命令下 `internal/server` 2 红、`internal/learning` 2 红。
四处失败现在全部清零，且其中三处是并发会话在主仓修好、这边同步过来的。



### 教训（这一节最值钱的地方）

> **在多 worktree 仓库里，任何内容检索都必须显式给 `path`。**
> 默认路径是 workspace 根，而我干活的 worktree 在它之外。
> 两者的文件可以差好几个提交，而 `git status` 对两边**都是干净的**——
> 它不会告诉你「你看的是另一个 worktree」。

配套的判据（与 `attribute-by-reading-files-not-git-history` 同源）：

1. 「文件在我读之后变了」有三种可能，**按这个顺序排除**：
   ① 别人在写（查 `LastWriteTime`）→ ② 我看的是另一个 worktree
   （查检索的 `path`）→ ③ 我第一次就记错了（重读）。
   本次直接跳到了 ①，白绕一圈。
2. 看到「某个修复/文档/测试存在」时，**先确认它在哪个 worktree**，
   再下「已有/没有」的结论。

---

## §7eb 当前分支的完整基线（2026-10-02 10:45）

前几节的修复合完之后，把能跑的全跑了一遍，作为**可复核的基线**留档。
这一节没有新缺陷，只有数字——而数字的意义在于它能被下一个会话拿来对比。

### 后端：70 个包，`-race`，全绿

```
$ go test -race ./... -count=1 -p 2
EXIT=0
ok=52   no-test=18   FAIL=0   合计=70
skip=0                ← 依赖真 PG 的用例没有被静默跳过
DATA RACE=0
$ go list ./...  | Measure-Object      → 70     ← 两个独立方法对上
```

「绿」的四条依据，缺一条这个结论就比实际证据弱（详见 §7ea）：

| 依据 | 本轮实测 | 不做会怎样 |
|---|---|---|
| `-race` | `DATA RACE=0` | 普通 `go test` 不会因缺同步而失败，只会**碰巧**没触发 |
| `-p 2` | 无 `[build failed]` | 默认并发会 OOM，失败包集合每次不同、不可复现 |
| 显式 `POCKET_TEST_POSTGRES_DSN` | `skip=0` | 不设则依赖真 PG 的用例**静默 SKIP** 而非 PASS |
| `ok+no-test+FAIL` vs `go list` | 70 = 70 | OOM 静默跳过的包会让这个数对不上 |

### 前端：330 用例，全绿

```
$ npm.cmd run typecheck          EXIT=0   （输出含 "> vue-tsc --noEmit" 横幅）
$ npm.cmd run test:email         EXIT=0   tests 292 / pass 292 / fail 0 / skipped 0
$ npm.cmd run gates              EXIT=0   tests 38 + 292 = 330 / fail 0 / skipped 0
```

两处细节：

- **必须用 `npm.cmd`**：PowerShell 下 `npm` 先解析到 `npm.ps1` 并被执行策略拦截，
  而它**可能仍给出 exit 0**——脚本根本没跑，退出码来自 PowerShell 自身。
  判据是输出里有没有 `> <script>` 横幅（`test:email` 那次有，`gates` 那次有）。
- **gates 的退出码本身不可信**（node 匹配不到文件时不报错、退出码仍是 0），
  所以判据是 `test:pass` 的**实际计数**。这里两批分别 38 与 292，
  有计数就说明真的执行过；「空输出 + 0」不算数。

### 覆盖面与其边界

这份基线覆盖的是**自动化可判的部分**。它**不覆盖**下面这些，
所以「全绿」不能读成「需求已交付」：

| 未覆盖 | 原因 |
|---|---|
| 真机 Android WebView（wasm、DOMPurify 剥 `<style>`、UI 行为） | 无设备 |
| 真实 IMAP 取原文 / MOVE | 需授权（不可逆） |
| 飞书推送 | `POCKET_FEISHU_INVOICE_CHAT_ID` 未配置，回调未部署 |
| Greenmail / 真实第三方 IMAP 的 BODYSTRUCTURE 兼容性 | Docker daemon 未运行 |
| 生产实例 | 运行中的 pocketd 二进制构建于 01:35，落后当前 HEAD |

需求 1 的定时路径目前只有**日志实证**（2026-10-02 08:00:00 那次跑批），
而那份日志来自旧二进制；当前代码的定时行为由 in-process IMAP 单测保证，
**没有在生产二进制上复验过**——要复验得先重新构建并重启。

## §7ec — 普查「列写了从不读回」：查出 action_reason 被整条链路丢弃（2026-10-02 11:0x）

### 怎么开始的

上两轮修掉 `Email.MessageID` / `Email.BodyPurged` 两个「字段写了从不读回」的缺陷后，
它们其实是**同一类**。所以这一轮没有继续手翻代码，而是做了一次**全量普查**：
把每张表的列按「INSERT/UPDATE 写过」与「SELECT 读过」对账，找出写了却从不被读回的。

### 判据自己先错了三次（这一节比结论更值钱）

普查脚本 `.col-census2.js` 前后经历了三轮，每一轮的「发现」都是**分析器的假报**，
不是代码的问题。记下来是因为它们全都是同一族：判据没在判它声称在判的东西。

| 轮次 | 症状 | 真因 |
|---|---|---|
| v1 | 6 张表全部「no sql found」 | 只提取双引号字符串，而 Go 的 SQL 全在**反引号**原始字符串里 |
| v1.5 | `email_invoices` 整表 25 列 reads=0 | 一条巨型 `CREATE TABLE` 语句跨越 `;` 没被切开，于是**所有表共享同一份列清单** |
| v2 | 同上，修了一半 | SELECT 列表由 `const invoiceSelectCols = ` + 拼接而成；标识符在**两个字面量之间**，先替换再提取也看不到（替换后内容落在反引号**之外**） |
| v2.1 | `notified_at` 假报 | 内联后的列表残留 `` `+` ` + `` 拼接胶水，最后一列带着尾巴匹配不上 |

判据从「只认字符串字面量」（免疫注释）改成「字面量 + 源码级常量替换 + 清洗拼接胶水」之后，
`sel` 从 36 涨到 80，`email_invoices` 的 25 列假报全部清零。
**四次假报全部指向分析器，没有一次是代码的问题**——这正是「先怀疑判据，再怀疑代码」那条规矩的又一次兑现。

### 收敛后的真实结果

普查最终只剩 3 条 `WRITTEN-NEVER-READ`，逐条查证：

| 列 | 判定 |
|---|---|
| `emails.notified_at` | **假报**，只在 `pipelineEmailCols` 里被读到（判据胶水问题，已修） |
| `emails.created_at` | **假报**，`store.go:556-559` 有注释明写「目前没有任何读路径消费它」——是**有意的死列** |
| `emails.deleted_at` | **假报**，只做 `COALESCE(deleted_at,0)=0` 谓词，不进结构体；`Email.DeletedAt` 字段确实零使用（已单列为待拍板项） |
| `emails.action_reason` | **真缺陷**，见下 |

### 真缺陷：action_reason 在 122/122 封邮件上全为空

**现象**（真库实测 `opencode_pocket.emails`）：

    total=122  action_reason 非空=0  ai_summary 非空=122

两者来自**同一个分类响应**。这直接排除了「分类器没返回 action_reason」这个解释——
它返回了，只是在代码里被扔掉了。

**断点在第三处**，而前两处早已修过：

1. ✅ DTO 漏字段（`kxmemory/client.go:281`）——2026-10-01 已补
2. ✅ 写回 SQL 不带这一列（`email/store.go` 的 `SetClassificationScoped`）——2026-10-01 已补，
   并新增了 `SetClassificationWithReasonScoped`
3. ❌ **`ClassifyUnclassified` 构造 `RawClassifyResult` 时压根没有 `Reason` 字段可搬**

也就是说：前两处修完，`SetClassificationWithReasonScoped` 只有 `server_email_classify.go`
和 `server_assistant.go` 两个手工触发的调用点在用；**定时/自动分类这条主链路
（`ClassifyUnclassified`）从来没调用过它**，而它调的是那个签名里就没有 reason 的
`SetClassificationScoped`。编译通过、运行不报错、库里永远是空。

**修法**（`email/classify_run.go`，+19/-2）：

- `RawClassifyResult` 加 `Reason` 字段，并把成因写进字段注释
- `ClassifyUnclassified` 搬 `Reason: row.ActionReason`
- 改调 `SetClassificationWithReasonScoped(..., w.Reason)`

### 验证

新增 3 条断言 + 1 条端到端：

- `TestClassifyRunPersistsActionReason` —— **端到端**：fake kxmemory 返回带 `action_reason`
  的分类结果 → 打 `ClassifyUnclassified` → 回读真 PG 的 `emails` 表。
  前提断言 `classified==1` 与对照组 `ai_summary != ""` 都在，防止「夹具没生效」被误读成通过。
- `TestBuildClassifyWritesCarriesReason` —— 纯函数层的透传
- `TestRawClassifyResultHasReasonField` —— 编译期的「字段不许删」

**两路互补负控**（不是合起来跑，是分开各跑一次）：

| 变异 | 结果 |
|---|---|
| A：去掉 `Reason: row.ActionReason` 透传 | 转红 ✓ |
| B：**保留**透传，只把写库方法换成 `SetClassificationScoped` | 转红 ✓ |

B 单独转红说明断言钉的是**两处断点各自的正确那一半**，而不是「合在一起会红」。

### 负控脚本自己翻车的一次（记下来）

第一版还原脚本 `restore` 报 `RESTORE FAILED`：它读的「原始内容」是 **apply 之后**的文件，
于是把变异态当成了原始态。第二版改成 apply 时先落 snapshot。
更要紧的是——**脚本 exit 2 拒绝了那次变异之后，我仍然接着跑了测试**，
拿到一个「2 个受管字段、仍然 PASS」的绿灯。这正是
`negative-control-must-verify-mutation-landed` 记的「校验失败就不许跑测试」，
我又犯了一次。**校验失败后的任何绿灯都必须当作不存在。**

### 扩守 readback 护栏：一条**惰性**的条目（如实记）

把 `ActionReason` 加进 `readback_guard_test.go` 的受管字段时触发了一次真实误报
（`fetcher.go:924` 的 `Sync()`），加了豁免并写明理由。

随后做了一次「这条条目有没有承重」的负控：**把条目连同 Sync 豁免一起去掉，护栏仍然全绿**。
结论是这条扩守目前**是惰性的**——包里唯一给 `.ActionReason` 赋值的函数就是已豁免的 `Sync`。
它仍值得留着（豁免一旦被误删会立刻生效），但**不能**当成「AI 判定依据的读路径已被守住」的证据；
那一层由上面那条走真 PG 的端到端用例负责。这段说明已写进护栏文件本身。

### 仍然存在的读路径缺口（属产品语义，未擅自决定）

即使现在 reason 能落库了，**邮件列表接口也读不出它**：

- `store.go:309`（收件箱列表）的 SELECT 列表里没有 `action_reason`
- `pipelineEmailCols`（`store_pipeline.go:37`）也没有
- 前端 `frontend/src` 对 `actionReason` **零引用**

即：**落库了，但没有任何人显示它**。提醒卡片目前仍无法回答「为什么这封被判为重要」。
这属于产品语义（提醒卡片要不要展示判定依据、列表要不要带这一列），**留给用户拍板**，
本轮不动。同样地 `message_id` 也有一样的读路径缺口。

### 顺带记一条

`store.go:556-559` 的注释明写 `created_at`「目前没有任何读路径消费它」，
所以普查把它报成 `WRITTEN-NEVER-READ` 是**符合事实的**——它是有意的死列，
不是漏读。判据没错，分类要错。这类「代码里已经写明为什么」的列应当单独归类，
不要混进缺陷列表。

## §7ed — 三个处理窗口对真实积压的覆盖率（§7du 盲区的量化 + §7dt 的后果）

§7du 定位到「发票候选被 24h 窗口当场排除」，并发会话已把窗口改成 90 天并 cherry-pick 进来。
但**从没量过改完之后到底覆盖了多少**。这一节用只读 SQL 在真库上量。

### 探针（只读，无写入）

三个需求各有一道时间窗口，全部按**邮件头 `date`** 筛（不是 `created_at`）：

| 需求 | 窗口 | 代码位置 |
|---|---|---|
| 1 清理广告垃圾 | 7 天 | `cleanSpam` pipeline.go:665 |
| 2 发票候选 | 90 天 | `invoiceCandidateLookbackDays` pipeline.go:544 |
| 4 重要邮件提醒 | 2 天 | `notifyImportant` pipeline.go:741 |

### 覆盖率（2026-10-02 11:1x，schema `opencode_pocket`）

收件箱 123 封全部 `uid>0`、`deleted_at=0`，日期跨 2026-09-05 ~ 10-02。

**发票候选（待建档 121 封）：**

    visible_to_inv_90d   = 121 / 121  (100%)
    visible_to_spam_7d   =  79 / 121  ( 65%)
    visible_to_remind_2d =  17 / 121  ( 14%)

按「距今多久」分桶：

    <=1d   3 封
    1-7d  76 封
    7-30d 42 封

**结论**：90 天窗口把发票候选覆盖率从 24h 的 **3/123 = 2.4%** 拉到 **100%**。
修复是有效的，且是当前三个窗口里唯一没有盲区的那个。

### 一个之前没被注意到的交叉盲区

42 封（占待建档的 35%）**同时**对需求 1 的 7 天垃圾窗口失明。
它们不是发票专属——是整个收件箱里 7~30 天那一段。
换句话说：**垃圾清理和发票提取扫的是不同的时间段，中间有 42 封的缝。**
这不是新缺陷（7 天窗口是既定设计），但它是 §7du 那一类盲区的**第二个实例**，
且量级比 24h 那个小（42 vs 118）。

### §7dt 的选择题，现在有了具体后果

需求 4 的 2 天窗口：

    high_importance_total        = 55
    in_2d                        =  6   (会提醒)
    out_of_2d                    = 49   (永远不会提醒)
    out_of_2d_and_never_notified = 30   (没提醒过，且**永远不会有**)

那 30 封是「已被判为重要、从未提醒、且在现有 2 天窗口下**永远不会被提醒**」的邮件。
报告里一直显示 `remindersOutOfWindow` 就是这批。三个选项的实际后果：

1. **保持 2 天** —— 这 30 封永远收不到提醒，且 `RemindersSent` 的 0 永远无法区分
   「没有重要邮件」和「有 30 封被窗口挡着」。
2. **放宽到 90 天** —— 首轮会一次性涌入约 49 条提醒（按 `notified_at=0` 去重后
   实为 30 条），用户会被 30 条历史提醒淹没，但每条都有 `date`，可以看得出是旧邮件。
3. **分批消化** —— 需要游标（如按 `date` 升序每轮 N 条），改造成本最高，
   但既不漏也不淹没，且可重入。

**本轮不擅自决定**，留给用户拍板。数字在此，代价可算。

### 顺带确认的一件事

`email_invoices` 现有 2 行，`invoice_date` 是 **TEXT** 列（不是 bigint），
最早 2026-09-24。按发票日期算，90 天窗口能覆盖 2/2，24 小时窗口只能覆盖 1/2。
写任何按 `invoice_date` 过滤的代码时要注意类型——我第一次写探针就撞上了
`operator does not exist: text >= integer`。

## §7ee — 用真实判据函数量「补采到底会发生什么」，并纠正 §7du 的一处旧表述

§7ed 量了窗口覆盖率，但没回答用户真正要拍板的那个问题：**授权补采之后会发生什么**。
这一节写了一个一次性探针（用真实判据函数跑真库数据，不是 SQL 近似）来回答它。

### 探针口径

包内临时测试 `zz_probe_*_test.go`，直连真 schema（`search_path=opencode_pocket`）、
**严格只读**、跑完 `git clean` 掉不进提交。判据用**真实的** `InvoiceCandidate` /
`ExtractInvoice`，刻意不用 SQL 近似——要量的是代码的行为。

### 结果一：90 天窗口下真实会拉原文的是 5 封

    backlog_in_window      = 121
    NEEDS_RAWBODY_candidate =   5
    ENVELOPE_HIT_missing_date = 0
    NO_JOB                 = 116

这 5 封逐一看标题：

    em-1298896142-... | Xiaomi MiMo API 开放平台扣款成功通知
    em-10444-...      | 所需操作：AWS 账户提示
    em-10443-...      | Amazon Web Services Account Alert
    em-10432-...      | AWS 账户提醒
    em-10424-...      | 来自 Apple 西湖商务团队的问候 - 杭州开轩科技有限公司

**没有一封是真发票。** 它们命中关键词是因为「扣款」「账单」「支付成功」这类词。
所以补采的真实产出是：5 次真实 IMAP FETCH，换回 5 条**待人工判断**的账单类通知，
可能 0 条发票。

### 结果二：纠正 §7du 的一处旧表述（重要）

§7du 当时写「6 封待判定发票邮件全在窗外，**含 `uid=10435`**（envelope 直接命中的确定真发票）」。
这句在**当时的 24h 窗口语境下**成立，但现在不成立了，我上一轮照抄了它没有核实。

实测 `uid=10435`（发票号 26332000008261110741）：

    JUDGE InvoiceCandidate = true
    JUDGE ExtractInvoice hit=true
      seller=杭州创客家投资管理有限公司 amount=3500 CNY date=2026-09-24
      kind=e-invoice status=downloaded source=pdf-url

而且 `emails_already_archived=2`——**它已经被建档了**。
它没出现在那 5 封名单里，是因为探针用 `NOT EXISTS (SELECT 1 FROM email_invoices)`
过滤掉了已建档行（幂等跳过），**不是被漏掉**。

**教训**：§7du 的记录是**当时**的库状态。引用历史结论前必须重测，
否则会把一条「已解决」当成「待解决」，据此提出的授权请求也就名不副实了。

### 结果三：已建档的 2 行发票，完整度差异极大

    INV#1  amount=3500.00 CNY  date=2026-09-24  no=26332000008261110741
           status=downloaded  source=pdf-url  attempts=1
           file_name="其他-杭州创客家投资管理有限公司-3500.00-2026-09-24.pdf"
           exported_at=1790884828   feishu_sent_at=0

    INV#2  amount=58000.00 CNY date=2026-10-25  no=(空)
           status=new  source=(空)  attempts=0
           file_name=""  file_path=""   exported_at=0  feishu_sent_at=0

第 1 行是**需求 2 端到端跑通的证据**：从邮件到 PDF 下载、按
`{费用类型}-{对方单位}-{金额}-{日期}.pdf` 命名、落盘路径、导出时间戳都有。
唯一没做的是飞书推送（`feishu_sent_at=0`，因为 `FEISHU_INVOICE_CHAT_ID` 未配置）。

第 2 行是中国工商银行 58000.00，**发票号为空、文件没下载、状态还是 new**。
它占着 58000 的金额——如果有人按 `email_invoices` 汇总金额，
这 58000 会进合计，但**它背后没有文件**。这与 §7dk 记的
「汇总端点 count 有 500 上限、金额不受限」是同一族问题。

**注意 `invoice_date=2026-10-25` 是未来日期**（今天是 10-02），
说明这个日期是从正文里误抓的。`applyParsedBodyDate` 的注释说
「正文里的散落日期可能不是票面日期」——这里就是那个情况的实例。

### 需求 2 的真实完成度（据本节证据）

| 环节 | 状态 | 证据 |
|---|---|---|
| 邮件 → 识别发票 | 已跑通 | INV#1 从 uid=10435 到建档 |
| 解析字段（金额/销方/日期/号码） | 已跑通 | INV#1 四个字段全有值 |
| 下载 PDF | 已跑通 | `file_path` 指向 ws_user-admin 下的实际文件 |
| 规范命名 | 已跑通 | `其他-杭州创客家…-3500.00-2026-09-24.pdf` |
| 导出/汇总 | 已跑通 | `exported_at` 有值 |
| **飞书推送** | **未验** | `feishu_sent_at=0`，chat id 未配置 |
| **多封 A4 拼版** | **未验** | 库里无对应产物记录 |

即：**需求 2 的服务端链路除飞书外已被真实数据证明跑通过一次**，
剩下的不是「能不能」而是「在更多邮件上是否稳定」。

### 对「授权补采」这个问题的影响

原问题的措辞是「拉 5 封真实 IMAP 原文」。现在知道这 5 封是什么了：
它们是账单类通知，不是发票。所以这次补采的**预期产出应当被下调**——
它更像是「验证关键词门槛会不会误拉」的一次实测，而不是「补回漏掉的发票」。

如果目的是后者，那么**真正该做的是 `email_invoices` 里第 2 行那种
「有金额没文件」的重建**（INV#2 的 58000），以及排查
`has_attachments` 全库 123 封**全为 false** 这个事实——
真实发票邮件明明带 PDF/OFD 附件（uid=10435 的正文里就有
「PDF发票下载 / OFD发票下载 / XML发票下载」三个链接），
但 `has_attachments` 一封都没记上（§7dq 已记 attachments JSON 列从未写入，
与这条同源）。
