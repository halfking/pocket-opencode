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

## 8. 仍未验证 / 未完成（不得外推）

- **真实邮箱已接入（6/6）**，但只做了**只读同步 + 发票采集**。仍未在真实邮箱上验证的：
  - **垃圾邮件 MOVE**：清垃圾这一步会改动真实邮箱，全程没跑过（`cleanSpam` 只在夹具上验证过）。
    要不要在真邮箱上开这条，建议你确认后再开。
  - **163 的特殊头**（CLIENTID 等）这次没被触发，代码路径未被真实流量覆盖。
  - **发票开票日期**没从附件 PDF 里抽到，文件名日期退化为下载当天。
- **飞书推送未验证**：`POCKET_FEISHU_APP_ID/SECRET/INVOICE_CHAT_ID` 未配置，
  真实 `SendInvoiceFile` 与新建的电子表格接口都**只在 httptest 假服务器上跑过**，
  没有对着真实租户跑过一次（需要应用开通电子表格权限）。
- **「多次操作才能下载到发票」只做到跨轮重试**（`MaxInvoiceAttempts=8` + pending 重试），
  没有「打开邮件→点确认→再下载」这类交互式多步。
- 定时流水线**到点执行**没有等过一次真实 06:00（用注入时钟单测 + 启动排期日志代替）。
- 需求 6 的「委托服务端执行」：`delegatePipeline` 只是 HTTP 转发，**对端编排服务不在本仓**，
  默认 `local` 路径才是可验证的那条。
- 前端改动（3×3 入口、409 走行覆盖）只过了 typecheck，**未做真机 UI 验证**。
