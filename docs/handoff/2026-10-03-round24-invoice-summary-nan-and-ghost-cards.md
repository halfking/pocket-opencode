# 2026-10-03 round24 — 发票页合计金额 ¥NaN（真机实测修掉）+ 8 张打不开的幽灵卡片

## §0 这一轮做了什么

需求 3（发票整理页）的真机验收。上一轮把真机通道打通后，本轮第一次真正**打开**那个页面读数，
结果第一屏就是缺陷：**顶部合计金额显示 `¥NaN`**。同屏「共 4 张」正常。

顺带查清了「设备上 12 张卡片 / 服务端只有 4 行发票」的成因，两者根因不同，都写在这里。

---

## §1 P0：合计金额显示 ¥NaN（已修，未上真机）

### 1.1 逐层实测，不是推断

真机页面上下文里直接问服务端（`scripts/adb-cdp-eval.ps1` + `Runtime.evaluate`）：

```
GET /api/emails/invoices?limit=30  ->  200
{
  "total": 4, "filed": 0,
  "amount": 3500, "currency": "CNY",
  "amounts": [ { "Currency": "CNY", "Amount": 3500, "Count": 1 } ]   ← 首字母大写
}
```

注意 `amounts[]` 的键是 **PascalCase**，而同一层的 `amount` / `currency` / `total` 是小驼峰。
`amounts` 的元素类型是 `email.CurrencyTotal`（`ledger.go`），**它没有 json tag**，
`encoding/json` 就按字段名原样输出。

前端 `resolveSummaryGroups`（`invoice-money.ts`）读 `a.currency` / `a.amount`：
两个都读到 `undefined` → `round2(undefined)` = `NaN` → `summaryMoney` 渲染成 **`¥NaN`**。

证据截图：`logs/zz-invoice-nan.png`（`¥NaN` 与「共 4 张 · 已归档 0 · 文件 6」同屏）。

### 1.2 为什么仓库原有护栏全绿（这条值得单独记）

| 护栏 | 守的是什么 | 为什么没抓到 |
|---|---|---|
| `invoice_total_parity_test.go` | 三处实现的**数值**一致（SQL / LedgerRows / 判据） | 三处都在 Go 内部比对，**从不序列化**。数值对得上 ≠ 线上键名对得上 |
| `invoice-totals-chain.test.mjs`（12 条） | 「若服务端按 camelCase 发，前端会不会用」 | 夹具是**手写的 camelCase**。夹具与被测对象出自同一个假设，这个假设从未被检验 |

结论：整条链上**没有任何一条用例断言过响应体里的字段名**。这与本仓库已记录的
「测试构造器掩盖生产形状」是同一类（STT 那次是构造函数恒给值，这里是夹具恒给对值）。

### 1.3 改了什么

- `backend/internal/email/ledger.go`：`CurrencyTotal` 三个字段补 `json:"currency"/"amount"/"count"`。
  这是根因修复，且**不需要重装 APK**——设备上已装的 APK 本来就读小驼峰，换后端即生效。
- `frontend/src/features/email/invoice-money.ts`：新增 `normalizeAmounts`，`a.currency ?? a.Currency`、
  `a.amount ?? a.Amount` 两种拼写都读。理由是灰度期 APK 与后端不会同时换：
  先发后端时旧 APK 正常；先发 APK 时旧后端仍打大写键。
  形状不可信时**整组作废**（返回空数组）而不是逐项跳过——跳过会让合计少算一组却看起来正常，
  那是这个模块一路在消灭的东西；作废后落回 `amount`+`currency`、再落回本地分组，两条都是真实可算的数。
  兼容分支**不能替代**服务端那条判据，这一点写进了注释。

### 1.4 新增护栏 + 负控实测

**运行时那一半**（Go）：`backend/internal/server/server_email_invoice_wire_keys_test.go`
真 store（`newInvoiceScopedStore`，隔离 schema）+ 真 handler 出**线上字节**，两条用例：
`TestInvoiceListWire_SingleCurrency_AmountsKeysAreLowerCamel`、
`TestInvoiceListWire_MultiCurrency_EveryGroupIsLowerCamel`。
刻意解到 `map[string]any` 而不是 struct——解进 struct 会把「键名不对」直接吞掉。

**跨语言那一半**（TS）：`frontend/src/features/email/__tests__/invoice-totals-wire-keys.test.mjs`（7 条）
从 `ledger.go` 读 CurrencyTotal 的 tag、从 `server_email_invoice.go` 读 handler 写出的顶层键、
从 `invoice-money.ts` 读前端实际读的键，三方对齐。

负控（全部实测，转红的用例名如下）：

| 注入的缺陷 | 转红 | 保持绿（证明守的是不同边界） |
|---|---|---|
| 删掉三个 json tag（= 真实缺陷形状） | Go：`SingleCurrency` + `MultiCurrency`；TS：`tag 首字母小写` + `前端读的键必须在 tag 里` | `invoice_total_parity_test.go` 全绿；`invoice-totals-chain` 12/12 全绿 |
| 只改服务端：tag 写成 `Currency` | TS：`tag 首字母小写` + `前端读的键必须在 tag 里` | 其余 5 条 |
| 只改前端：`a.amount` 改成 `a.money` | TS：`前端读的键必须在 tag 里`（**仅此一条**，定位精准） | 其余 6 条 |

第一行那列「保持绿」是这个护栏存在的理由：缺陷在场时，原有两条主力护栏**一条都不红**。

### 1.5 为什么没有真进程复验

判据已经是「真 store + 真 handler + 线上字节」，且 `email` 全包（115.7s）与
`server -run Invoice` 都通过。再起一个真 pocketd 只能验同一件事，代价是**并发会话正在改
`backend/internal/server/llm_gateway_handler.go`**，新二进制会把别人未完成的 WIP 一起打进去。
不做。

### 1.6 真机生效需要你授权

18099 跑的是 `logs/pocketd-invoicecheck.exe`（2026-10-03 23:34:44 构建，早于本次修复），
所以**设备上现在仍是 `¥NaN`**（本轮截图即为此状态）。要让它消失需要：

1. 重新 build（会包含并发会话的 WIP）；
2. 用新二进制重启 18099。

两步都需要你点头。上一轮我为了不换代码而复用同一二进制，本轮同样不自行决定。

---

## §2 P1：设备上 12 张卡片，服务端只有 4 行发票（**未修，待产品决定**）

### 2.1 实测

- 真机渲染 **12 张卡片**（`Runtime.evaluate` 读 `document.body.innerText`，比截图可靠——
  上一轮我凭两张局部截图目测成 7 张，是错的）。
- `opencode_pocket.email_invoices` 全库只有 **4 行、1 个 user、1 个 workspace**。
- 汇总卡因此自相矛盾：`共 4 张`（服务端 total）与 `文件 6`（本地 12 行里数出来的）
  出自同一张卡的两个数据源。

### 2.2 幽灵卡片是可操作且必然失败的

在真机上点第 4 张卡（`name: ¥0.00` 失败行）的「归档」，抓到：

```
PATCH /api/emails/invoices/inv_1790789580385036500_1  ->  404
```

该 id 不在服务端 4 行里。切一次筛选（`待整理` → `全部`）触发两次 `load()`，同一个 PATCH 404
**又各出现一次**——`markFiled` 只在成功时 `clearDirty`，失败后 `dirty=1` 留在本地，
于是每次进页面都重推一次、静默失败一次。

同一次筛选还暴露了更多：

```
GET  .../inv_1790782486690433000_1/thumb  -> 404     ← 幽灵卡的缩略图
GET  .../inv_1790782486690433000_1/file   -> 404     ← 幽灵卡的「下载」按钮
GET  .../inv_1790884695419622800_1/thumb  -> 200     ← 唯一真实那行（3500）
```

请求是否发出与 `invoiceHasFile` 严格一致（无文件的腾讯/工行行不发请求），这条自洽性反过来
证明判据可信。**用户看到的是：一张标着「已下载」并显示 PDF 文件名的卡片，点下载 404。**

### 2.3 成因：邮件被 purge，发票行 CASCADE 消失，本地镜像不裁剪

- `email_invoices.email_id` 是 `UNIQUE`，提取走 `UpsertInvoice` 幂等 upsert，**服务端不会自己堆行**。
- `email_invoices.email_id ... ON DELETE CASCADE`（`invoice_store.go:18`）：邮件被删，发票行跟着消失。
- PG 查 `云服务开票中心` / `财务部` / 对账单 相关邮件：**只剩工行那封**，其余邮件本身已不在库里。
- 设备端 `invoices-store.ts` 开头就写着「同步只 upsert，**禁止整表 DELETE**」——
  这是有意设计（离线优先），但缺「按服务端权威集裁剪」这一步。
- 附带一条同源隐患：`local_email_invoices` **没有 workspace_id / user_id 列**（`schema.ts:244-269`
  与 `invoices-store.ts` 的 `INVOICE_COLS` 都没有），所以切账号/切空间后旧数据会直接混进列表。

### 2.4 为什么我没有直接修

「按服务端权威集裁剪本地行」会与离线优先正面冲突：本地可能有尚未推送的改动
（`dirty=1`）、有 `isLocalOnlyId` 的本地临时行。粗暴裁剪会**丢用户未同步的操作**。
这属于产品/架构取舍（要不要加「服务端已删除」墓碑行、要不要给本地表补 workspace 列），
不在本轮授权范围。

已知的自助出口：卡片上的「删除」会先删本地再打服务端（404 被吞掉），所以**逐张点删除可以自愈**。

---

## §3 本轮在真机上留下的状态（如实记录）

- 点过一次幽灵卡的「归档」→ 服务端 404，本地短暂变成 `filed`；已点「取消归档」复位，
  现在**没有任何卡片处于已归档**。但那一行的 `dirty=1` 留在设备本地（`clearDirty` 未被调用），
  继续每次进页面重推一次 404——这是 §2 的缺陷的一部分，清不掉。
- `adb reverse tcp:18099` 保持原样（全程无 workaround）；本轮临时加的 `tcp:9333` CDP 转发已移除。
- 设备 `/sdcard/zz-nan*.png` 已删。
- 未改任何生产数据、未触发流水线、未跑真实 IMAP MOVE。

## §4 顺带看到、但不在本轮范围的两条

- 邮件详情页 `/api/emails/{id}/body` 返回 **502**，而同页 `/api/llm/chat` 200。
- `name: ¥0.00` 那两张卡把内部错误原文直接渲染成卡片副标题：
  「失败：POP3-sourced email raw body cache miss (err=<nil>); refusing to IMAP-FETCH a positional index」。
  英文技术黑话 + 内部错误串泄漏到用户界面。

## §5 待你拍板

1. **授权重新 build + 重启 18099**（让 §1 的修复在真机上可见；会包含并发会话的 WIP）。
2. §2 幽灵卡片：是否要「按服务端权威集裁剪」，以及本地表要不要补 workspace_id/user_id。
3. 上一轮那 11 项待拍板项**保持等决定**，本轮未推进。
4. 推送授权仍未给（本地已是 fast-forward）。

---

## §6 需求 4：客户端这一侧已钉住，真机那一侧等 08:00 自然满足

需求 4 此前被记为「验不了」：真库只有 24 条通知，渲染 24 条正常恰恰证明不了任何东西。
本轮把**不需要授权**的那一半做完了。

### 6.1 08:00 会自动造出那个条件

`notifyImportant`（`pipeline.go:894-901`）**没有任何限流**（round19 §4），而未提醒的
high 有 32 封。08:00 定时流水线一跑，总数 24 + 32 = **56 > 50**。
所以真机验收的条件当天自然满足——**不手工触发流水线**（未授权），等它自己跑。

### 6.2 关键：32 条走的是 WS 突发，不是冷启动

真机首次加载发生在 08:00 **之前**，所以这批走 `notificationDispatcher` → `store.pushLocal`。
既有判据 `notification-first-load-limit.test.ts` 守的恰恰是**冷启动**那条
（首次加载取 200 = 后端硬上限，有负控）。突发这条路上原有三处**只活在注释里**：

| 性质 | 位置 | 破了会怎样 |
|---|---|---|
| 入账不截断 inbox | `pushLocal` / `subscribeWs` | `slice(0, 50)`「省内存」会把最旧的 6 条挤掉，只在真机上表现为「翻不到更早的提醒」，store 其余测试全绿 |
| 视图渲染整个 inbox | `NotificationsView.vue:16` | 切片 =「store 里 56 条、界面只画 50 条」，store 正确也救不了界面 |
| 水位线仍是 `max(created_at)` | `stores/notification.ts:62` | 服务端是 `created_at > since`，**同秒产生的 32 条不会被增量重拉捞回来**——它们能进 store 只因为 WS 推了。前两条一破就没有第二条路 |

第三条是这轮的真正发现：它把前两条从「优化」变成「唯一路径」。

### 6.3 新增判据（`055e5604`）

`frontend/src/stores/__tests__/notification-burst-no-eviction.test.mjs`（3 条）。
负控实测，三条各自**只**命中一条，且既有 `first-load-limit` 判据三次都保持全绿：

1. `pushLocal` 注入 `this.inbox = this.inbox.slice(0, 50)` → 转红「WS 入账不得截断 inbox」
2. 视图 items 改成 `store.inbox.slice(0, 50)` → 转红「视图必须渲染整个 inbox」
3. 水位线换成 `n.read_at` → 转红「增量水位线仍是 max(created_at)」

判据自身也踩了两次坑（都响亮地红了，没静默放行）：`\{4\}` 写成「一个含 4 的花括号」
而不是 4 个空格；方法体收尾是 `},` 而不是 `}`，而放宽成任意缩进又会先撞上内层
6 空格的 `}` 把范围切短——后者会让断言「因为扫不到而通过」，是判据最危险的失败方式。

回归：前端全量 1727 用例 0 失败，187/187 个测试文件全部被实际执行。

### 6.4 08:00 之后要做的那一步

已排提醒（08:00 流水线跑完后自动唤醒）。要量的是**真机实际渲染了多少条**，
而不是 API 返回多少条——空列表和被截断的列表在屏幕上一模一样，只量 API 会全绿：
用 `adb-cdp-eval.ps1` 读 `document.body.innerText` 数渲染条数，与
`/api/notifications?limit=200` 的条数对照，并确认最早的 6 条仍能翻到。

---

## §7 复查 json tag 影响面时挖出的第二现场（`05cc27df`）

`handleEmailInvoiceSummary` 的响应里也有 `"amounts": amounts`
（`server_email_pipeline.go:615`），序列化的是**同一个类型** `CurrencyTotal`，
修复前同样发 PascalCase。

**要说准现状**：前端 `emailApi.invoiceSummary()` 全仓**只有定义、没有任何调用方**
（`invoiceSummary` / `EmailInvoiceSummary` 只出现在 `api/email.ts` 与一条测试注释里），
所以这个现场的错键今天**不显示**。按「闸门逻辑上一直关着、目前没造成损失」记录，
不是「已造成错账」。

### 7.1 这个缺陷当初为什么能活下来（整轮最值得记住的一段）

`server_email_invoice_summary_test.go` 的 `summaryResponse.Amounts` 元素带
`json:"currency"` 这类小驼峰 tag，却在修复前一直从 PascalCase 载荷里
**读到真值并断言成功**（`byCur["USD"]==100`、`Amounts[0].Amount==75.5`、`Count==2`……）。

原因是 **Go 的 `json.Unmarshal` 对字段名做大小写不敏感匹配**：`"Currency"`
能落进标了 `json:"currency"` 的字段。

所以「Go 侧用例全绿」**不能**证明线上键名对——它只证明了 Go 能读懂自己。
真正发作的是大小写敏感的 TS，也就是 §1 那个 ¥NaN。
这也是 `server_email_invoice_wire_keys_test.go` 全部断言都解到 `map[string]any`
的原因：解进 struct 会把这件事藏起来。

负控实测（删掉三个 tag）：新 wire 用例三条全红，而 `TestInvoiceSummary_` 全部
5 条**照旧通过**——用例全绿与线上键名错误可以长期共存，这条对照就是证据。

### 7.2 顺带闭合的一个验证缺口

上一段报告说「改了生产 TS」，其实只跑了 `.mjs` 用例，**没跑类型检查**。
本轮补：`vue-tsc --noEmit` exit=0。

---

## §8 需求 2：把「真开 MOVE 只会移 2 封」这个依据刷新到今天（只读）

round19 §2 给「授权开真实 IMAP MOVE」这个待拍板项的依据是「真开只会移那 2 封」，
那是 **2026-10-02 20:42** 在 **85 封** 7 天窗口邮件上量的。现在库里 175 封、
7 天窗口 88 封——**依据已经过期**，拿着旧数字做决定是不行的。

用仓库自带的只读诊断重跑（`diag_spam_verdict_test.go`，跑**生产代码路径**
`Pipeline.cleanSpam` 本身而不是抄一遍判定；只读由数据库强制
`SET default_transaction_read_only = on`，且 `Fetcher: nil` 证明预演分支不碰 IMAP）：

```
spam dry-run: 2 mail(s) across 1 account(s) would be moved, 0 near-miss
窗口内邮件数: 88（round19 时 85）
判定为垃圾: 2
  账户 acct-1790870162047413500-2: 2 封  依据=退订特征:取消订阅
    [1] 【阿里云】云安全中心周报
    [2] 【阿里云】云安全中心周报
近门槛（有分但未判垃圾）: 0
SpamMoved=0（dry-run 恒为 0：真 MOVE 没发生）
```

**结论没变**：仍是 2 封、同一账户、同一依据，`SpamDryRun` 报的是邮件数不是账户数
（`dc372be1` 那个单位修复在今天的数据上仍然成立）。配套状态事实一并复查：

| 事实 | 值 |
|---|---|
| `email_action_intents` | **0 行**（从未发生过一次真实 MOVE） |
| `email_accounts` | 5 个账户，**0 个**有 rules（→ route-folder 意图仍无从产生） |
| 邮件总量 / 7 天窗口 | 175 / 88 |

### 8.1 决定前值得多看一眼的一点

那 2 封是**「【阿里云】云安全中心周报」**，而判定依据只有一条
「退订特征:取消订阅」——即发件人带退订链接。**安全周报是否属于「该移走的广告」**
是个产品判断：把它移进垃圾箱可能正好藏掉一条需要人看的安全通知。
这是 round19 已记的「该不该移是产品决定」的更具体版本，不是我能替你定的。

---

## §9 时敏：58000 那行会在 08:00 **第一次**被采集，而它是否进合计取决于一条分支

round19 §3 记的是「58000 那行 `status=new`，`InvoiceCountsTowardTotal` 要求
`status ∈ {downloaded, filed}` 且有文件，所以它**不进合计**，没污染金额」。
这句在当时是对的，但它描述的是一个**会过期的事实**，不是恒等式。

### 9.1 实测前提（只读）

```
inv_1790903383222583800_1 | status=new | file_name=(空) | attempts=0 | last_error=(空)
  created/updated 都是 10-02 09:09
  来源邮件 em-1298896144-… 「中国工商银行客户对账单(ICBC Peony Card Ba…」
  has_attachments = false
```

采集器按 `status IN (new, pending)` 挑活（`ListHarvestableInvoices`），
而 `attempts=0` 说明它**从没被尝试过**。08:00 流水线一跑，它会拿到**第一次**机会。

### 9.2 三条分支里有两条已经被排除

`invoice_harvest.go` 的产出顺序：

| 分支 | 对这封邮件 | 依据 |
|---|---|---|
| ① PDF/图片附件 | **不可能** | `has_attachments = false` |
| ② 正文链接 → 下载回来是 PDF/图片 | **唯一可能产出的分支** | `extractInvoiceURLs(HTMLBody+TextBody)`，对账单正文里带「查看详情/下载」链接很常见 |
| ③ XML 附件 → 重渲染 | **不可能** | 同 ①，��附件 |

所以结论收得很窄：**这行会不会变成 ¥58,000 的错账，只取决于分支 ②**
——即正文里有没有一个能下载回 PDF/图片的链接。

- 若有 → `saveInvoiceFile(..., "pdf-url")` → `status=downloaded` + 有文件 →
  **立刻进合计**（`amounts` 会从 CNY 3,500 变成 CNY 61,500，页面上那 8 张幽灵卡
  之外还要再加一条真实的错账）。
- 若有链接但拿回来不对 → `markRetry`，`last_error` 记下每个链接的失败原因，
  `status` 转 `pending`/`failed` → 仍然不进合计。
- 若正文里根本没有链接 → 走「邮件里没有 pdf/xml」那条，仍不进合计。

### 9.3 为什么这条要现在说

要确定分支 ② 走哪一边，必须真跑一次采集（IMAP FETCH + 落盘 + 改状态），
**那是未授权动作**（且会写生产数据）。所以现在只能给出上面这组前提，
而不能替 08:00 断言结果。

但它把 round19 待拍板清单里第 3 项（58000 误建档行怎么处理）从「历史遗留问题」
变成了**今天 08:00 就会自己往前走一步的问题**：在流水线跑之前决定，
和处理「它自己变成了 downloaded 之后要不要认这笔账」，是两个不同难度的决定。

当前合计口径可复核：真机实测的 `GET /api/emails/invoices` 返回
`"amounts":[{"currency":"CNY","amount":3500,"count":1}]`——58000 不在其中，与上表一致。

---

## §10 08:00 验收的基线已存档（以及一个并发会话挡路的事实）

### 10.1 基线（2026-10-03 01:06，存档 `logs/zz-8am-baseline-20261003-0106.txt`）

只做计数与时间戳，**不含任何判定**：

```
notifications_total       = 24
notifications_unread      = 23
notifications_newest      = 2026-10-02 05:38:33
invoices_counted_by_total = 1
invoices_counted_amount   = 3500.00
row_58000_state           = new | file=(none) | attempts=0 | updated=10-02 09:09
```

`invoices_counted_*` 那两行是 `InvoiceCountsTowardTotal` 判据的 SQL 转写，
**不是**独立判定，也不是权威数字——权威数字是真机实测的 API
`amounts: [{CNY, 3500, count:1}]`，两者一致。

08:00 之后跑同一条命令，差值就是归因：通知 24→?、合计 3500→?、
58000 那行的 status/attempts/last_error 各变成什么。

### 10.2 ⚠️ 并发会话的在制品让 `internal/email` 的测试二进制编译不过

```
internal\email\diag_qp_replay_test.go:227:3: unknown field legacy in struct literal of type qpOutcome
internal\email\diag_qp_replay_test.go:89:6:  looksLikeMIME redeclared in this block
```

- 该文件**未被 git 跟踪**，mtime 在 01:05–01:06 之间还在变，错误信息两次不同
  ⇒ 对方正在里边写边改。
- 影响面：`go test ./internal/email` 与 `go vet ./internal/email` 整个包都跑不了
  （测试二进制是整包编译的，`-run` 过滤救不了）。
- **不影响**生产代码：`go build ./...` exit=0；本轮新增的护栏在 `internal/server`，
  `TestInvoiceListWire_*` / `TestInvoiceSummaryWire_*` 照常通过。
- 因此本轮没能跑成 `TestDiagReminderBacklog`（提醒积压那条只读诊断）与
  `TestDiagInvoiceBacklog`——它们和上面那个文件同包。**没有绕过去**：
  不 stash、不 checkout、不改对方文件（并行会话的 `git stash -u` 卷走过未提交工作，
  那是已吃过的亏）；也没把那个包复制到别处跑，那会造成代码分叉。
  等它编译过就能跑，08:00 那时大概率已提交或已修好。

### 10.3 需求 7 遗留的 18 封乱码摘要：预登记「明早它不该变」

基线（`logs/zz-snippet-baseline-20261003-0108.txt`）：

```
emails_total            = 175
stale_ge8               = 18      ← 阈值 >= 8 处（单处会撞上「01:2」里的 =2 假阳性）
stale_1_to_7            = 19
stale_zero              = 138
stale_ge8_newest_mail   = 10-02 22:08
stale_1_to_7_newest_mail= 10-02 10:00
```

与真机 API 实测的「175 封里 18 封 snippet 含真转义」一致，两个独立口径互相对上了。

**明早的预期结果：仍然是 18。** 理由：自愈路径是入库时
`ON CONFLICT (id) DO UPDATE` 刷新 snippet，而**常规同步只拉 UID 更大的新邮件**，
不会回头重读这 18 封；而 10-02 22:08 之后入库的邮件里没有新的乱码行
（`stale_1_to_7` 的最新一封是 10-02 10:00，更早），说明现行二进制不再写转义摘要。

⇒ **明早若还是 18，那是符合预期，不是「修复没生效」**；反过来若突然变成 0，
才说明有东西真的重读了那批邮件（要查是谁触发的）。修它们仍需重置 `last_synced_uid`
重同步 = 手工触发流水线，未授权。

### 10.4 顺带记一次判据自己骗人

第一次跑这条统计时我得到 `emails_total = 2021`、`stale = 0`，与直接计数的 175 矛盾。
查下去是我的 SQL 有两个错：`regexp_matches` 是 set-returning 函数，不能出现在
`WHERE` 里（后一次直接报了 `set-returning functions are not allowed in WHERE`），
而先前那次是把命中数算在 CTE 的 SELECT 列里、外层过滤——写法合法但**输出串行了**。
结论：**175 是真的，2021 是我判据的产物。** 数据本身干净（date 无 0、无未来日期，
发件人是真人邮件而非夹具）。这与本轮已经吃过三次的坑同源：
统计数量前先看**被计数的到底有哪几行**。

---

## §11 ⚠️ 更正：08:00 会推的是 **34** 条，不是 32；且定时路径不做自动分类

### 11.1 先更正我自己

我在前几段报告里说「08:00 会一次性推 32 条积压提醒 → 总数 56 > 50」。**32 是 round19
（10-02 20:42）的数，已经过期。** 用生产判据的只读诊断
（`TestDiagReminderBacklog`，存档 `logs/zz-reminder-backlog-20261003.txt`）重跑：

```
扫描行数          = 175
importance 为空   = 0
窗口外永不提醒的  = 0
本轮将推送 RemindersPending = 34
```

high 从 round19 的 56 涨到 58（新邮件到达并被分类），所以待推从 32 变成 **34**。
⇒ 08:00 之后通知总数应是 **24 + 34 = 58**，仍然 > 50，需求 4 的真机验收条件依然会满足。

### 11.2 定时路径不做自动分类 —— 这让预测变稳，而不是变松

今天的启动日志：

```
POCKET_KXMEMORY_BASE_URL not set; AI classification/SSOT disabled
Email scheduler started (fetch_enabled=true, kxmemory=false, ...)
[email/scheduler] daily pipeline runner injected (hour=8)
[email/scheduler] pipeline scheduled at 2026-10-03T08:00:00+08:00
```

昨天 23:36 的旧实例把后果写得更直白：「同步后**不执行**自动分类……手动
`/api/emails/classify` 有 LLM 网关兜底，定时路径没有。后果：新到邮件的 importance 恒为空」。

⇒ 08:00 之前新到的邮件**不会被分类、也就不会被提醒**，34 就是全部。
（当前 `importance 为空 = 0` 是并发会话那三次手工 `/api/emails/classify` 的结果，
日志里能看到它们大量撞 llm-gateway `429 rate_limit_exceeded`。**需求 4 的前提是由
我不控制的活动满足的**——这也是 §11.4 那个归因风险的一部分。）

### 11.3 对「32 条要不要限流」这个待拍板项有用的分桶

诊断本身就印了分桶（它写着「限流该不该分层，看这行」）：

| 维度 | 分布 |
|---|---|
| category | notification 16 / work 14 / bill 4 |
| 账户 | kimmy.huang@163.com 15 / 56551681@qq.com 11 / feikemanager@163.com 8 |
| 账龄 | 0-2d 17 / 3-7d 8 / 8-30d 9 |

逐条清单里，**14 条 `work` 全是我自己仓库的 CI 失败通知**
（`[halfking/ai-native-gateway-core] Run failed: …`、`Trendaradar`、`ci - main`），
真正的「账单类」只有 4 条（含 ICBC 对账单、可用额度低于预警值）。
⇒ 若要限流，**按 category 分层**与**全局一刀切**的取舍差别很大：前者保住 4 条账单、
压掉 14 条 CI 噪声；后者会把账单一起压掉。这条决策现在有了可依据的分桶。

### 11.4 归因风险（明早验收时必须注意）

今晚 00:16–01:07 之间，`POST /api/emails/sync` 被触发 **12 次**（00:47 那 3 次间隔仅 3–12 秒），
`POST /api/emails/classify` 3 次，另有大量 `/api/llm/chat`。轮询器要求 900 秒静默，
所以这些是**手工/API 驱动**的，来自并发会话，不是调度器。

⇒ **明早若通知数或发票行发生变化，不能只看 DB 差值就归因给 08:00 那次流水线**；
必须同时看日志里那一轮的报告行（`RemindersPending` / `RemindersSent` / harvest 结果）。
另外 00:25:53 那次流水线只打印了两行配置、**没有打印报告行**，
但它确实写出了 `invoices-summary-20261003-002553.csv/.md`（845/635 字节，比昨天的 426/389 大），
说明它走到了汇总文档这一步。报告行缺失这件事本轮没有查清，**明早别把它当成常态**。

---

## §12 查清了 §11.4 那个疑问，并且更正我自己在 §11.4 里的担心

### 12.1 我一度判断「00:25:53 那轮至今还在执行，08:00 会排队」——错的

那条推理是：「`Run` 的报告行在 `defer` 里，返回必打；二进制里确实有 `done synced=` 字符串；
日志里没有 ⇒ 它没返回 ⇒ 还在跑 ⇒ `emailPipelineMu` 被占 ⇒ 08:00 那次会排队」。

把它推翻的三条实测（`logs/pd-18099-20261003-001508.err.log`，今晚整轮）：

```
step1 sync          0 条      ← 真跑一轮会有 5 条（每个账户一条）
step1.5 window=     0 条
step 1              0 条
spam dry-run:       0 条      ← 只有走 cleanSpam 才会打
done synced=        0 条
本轮将推送           0 条
reminders sent:     0 条
进程 CPU：8.90625s -> 8.90625s（8 秒内 delta = 0.000s，空闲而非空转）
```

⇒ **`Run` 根本没被调用**，今晚没有任何一轮流水线在跑，
`emailPipelineMu` 是**空的**，08:00 那次会正常执行，没有排队风险。

### 12.2 那两行 `[email/pipeline]` 是谁打的

`data/email-invoices/ws_user-admin/exports/default/invoices-summary-20261003-002553.csv/.md`
确实在 00:25:53 被写出（845/635 字节，比昨天的 426/389 大）。而**写这两个文件的只有发票汇总路径**。
配合下一条，它就是一次**发票汇总请求**，不是流水线。

### 12.3 一条会让人反复踩坑的可观测性限制

`middleware.go:60`：

```go
// 慢请求日志（超过 500ms）
if duration > 500*time.Millisecond {
    log.Printf("[SLOW] %s %s - %d (%v)", ...)
}
```

**只有超过 500ms 的请求才有日志行。** 所以「日志里没有 `GET /api/emails/invoices/summary`」
**不能推出「没人调它」**——它快到不需要被记。我今晚就在这里绕了远路：
先假设是流水线卡死，再假设是 HTTP 汇总，两次都靠「日志里没有」来推断，而那条推断根本不成立。

⇒ 归因时先问一句：**这个事件在这个日志里本来会不会被记录？** 阈值以下的请求、
以及只记 SLOW 的路径，都是「没记录」而非「没发生」。

### 12.4 一个对明早有利的确认

跑着的二进制（`logs/pocketd-invoicecheck.exe`，2026-10-02 23:34:44）里**确实含**这些字符串：
`done synced=`、`本轮将推送`、`reminders sent:`、`step1 sync`、`step1.5 window=`、
`spam dry-run:`（不含 `飞书推送被跳过`——那条是新加的，晚于这个构建）。

所以 08:00 那轮**会**打出 `本轮将推送 N 条` 与 `done … reminders=N`，
**§11.4 依赖日志报告行做归因的方案成立**。

（另注：那个旧二进制的日志文案与当前源码有出入——它在汇总路径上打的也是
`[email/pipeline]` 前缀，而当前源码里那条是「飞书推送被跳过」。**读昨晚/今晚的日志要以
二进制里的实际字符串为准，不能拿当前源码的文案去对**，否则会把「文案不同」误读成「跑的是别的东西」。）

---

## §13 更正 §4 第 2 条：那两行「英文技术黑话」是**幽灵卡上的陈旧记录**，不是活的失败

§4 我记的是：「`name: ¥0.00` 那两张卡把内部错误原文直接渲染成卡片副标题」。
查证之后要改写：**那两张卡在服务端根本不存在。**

```
select … from email_invoices where status='failed' or seller='name:'
  -> 0 行
```

服务端 `email_invoices` 总共就 4 行（3500 / 58000 / 126 / 328.5），没有 `failed`，
没有 `seller='name:'`。而真机点第 4 张卡的「归档」打出去的是
`PATCH /api/emails/invoices/inv_1790789580385036500_1` → **404**，该 id 不在那 4 行里。

⇒ 那两张 `name: ¥0.00 失败` 就是 §2 说的 12 张渲染 / 4 行服务端之间的**幽灵卡**之一。
卡片上那串
「POP3-sourced email raw body cache miss (err=<nil>); refusing to IMAP-FETCH a positional index」
是它们**当年还在服务端时**写进 `last_error` 的历史记录。

### 13.1 两处连带更正

1. **不是「错误文案泄漏」需要单独修**。它是「本地镜像不裁剪」的一个切面：
   幽灵卡照样渲染它当年存下的字段。单独去改文案等于治一个已经随根因消失的症状。
2. **那句文案用的是旧二进制**。跑着的二进制（23:34:44）里其实**有**自愈路径
   （`recoverPOP3SourcedRaw` / `and self-heal failed` / `sameEmailMessage` 全部命中），
   而卡上那句缺少 `and self-heal failed:` 段——说明它是**自愈加入之前**的二进制写下的。
   ⇒ 我一度据这句文案推断「跑着的后端没有自愈」，**那是错的**，已用字符串探测推翻。
   （字符串探测二进制是个便宜的手段：一步区分「它不打这行」「它没走到这行」「它有的是旧文案」。）

### 13.2 那两张真实 QQ Wallet 发票现在在哪

`server_email_summary_total_test.go` 的注释记着它们曾经存在：
「两张 QQ Wallet：`seller="name:"`、`invoiceNo="Issuance"`，字段是从邮件错误段落抽出来的」。
它们已被服务端删除，设备上留下两张 ¥0.00 的幽灵卡。

⇒ 真机上「需求 3 的两张真实发票」目前**既不在服务端、也无法从界面取到**
（那张卡点下载 / 归档 / 预览都是 404）。这不是采集失败，是**行被删了**。
所以修它们不在「让采集重试」这一层，而在 §2 那个待拍板项（幽灵卡怎么处理）。

---

## §14 两条给 08:00 接手者的硬约束

### 14.1 `/api/healthz` 404 不是缺陷，是我探错了路径

```
/healthz       -> 200  body='ok'
/api/healthz   -> 404  body='404 page not found'
```

端点注册在 `server.go:662` 的 **`/healthz`**（无鉴权，`handleHealthz` 只写 200 "ok"）。
我先前用 `/api/healthz` 探到 404 便记成「未解释的异常」，其实它压根没这个路由。
（`longlived_paths_test.go:120` 里那份 `"api/healthz"` 名单也是**不存在**的路径——
那份判据断言的是「它不该被加进慢请求白名单」，对一个不存在的路径恒真，所以从没被这个事实绊倒。
**又一个恒真判据的实例**：它通过不是因为路径对，而是因为没人加。）

### 14.2 ⚠️ 08:00 之前**不要**起第二个后端实例

上一轮我拒绝起真进程验 §1 的修复，理由是「怕触发未授权的 IMAP MOVE」。现在多一条**更强**的理由：

`[email/scheduler] daily pipeline runner injected (hour=8)` +
`pipeline scheduled at 2026-10-03T08:00:00+08:00` 是**每个进程各自**打的——
再起一个 pocketd 实例，它会**自己**排一次 08:00 的流水线。那意味着明早变成
两轮流水线同时抢 `emailPipelineMu`、并发 `MarkEmailsNotified`，
**把 §11 那次验收的归因彻底搅乱**。

⇒ 08:00 验收期间**只保留 18099 这一个实例**。要验 §1 的修复（`CurrencyTotal` 的 json tag），
等 08:00 验收结束后、或用户授权的同一次重启里做。

---

## §16 查清 §15.3 那三个实例各自**能做什么**——污染不是均匀的，但比想象的广

§15.3 只列出了「三个实例都排了 08:00」。这一节把每个实例的实际能力查实，
因为**三者的污染面完全不同**，明早读日志时不能一视同仁。

### 16.1 18077（wt-a20，pid 23256）：**完整的一轮**

```
data dir = C:\workspace\openpocket\data          <- 与 18099 同一个
Email credential self-check: all 5 enabled email account(s) decrypt with the current master key
Email scheduler started (fetch_enabled=true, kxmemory=false, ...)
```

同一把 master key ⇒ **5 个账户全部能解密、能同步**，会跑完整的
sync → 分类 → 垃圾清理 → 提醒 → 发票采集 → 飞书/汇总文档。
⇒ 它的 08:00 那轮与 18099 **等价**。

### 16.2 18100（.wt-e2e，pid 28160）：**同步全灭，但提醒步照样跑**

它用的是 `.wt-e2e\backend\data` 下**自己的** master key，与生产加密的凭据不匹配：

```
[email/sync] account acct-…-5 (kimmy.huang@163.com): decrypt credential: cipher: message authentication failed
[email/scheduler] sync acct-…-1/-2/-3/-4/-5 failed: decrypt credential: cipher: message authentication failed
        （00:09:49、01:22:49 ×5、01:38:49 每次调度同步都这样）
fetcher 日志行 = 0
```

**我一度据此推断「它推不了提醒，所以无害」——那是错的**，而且错在一个很容易犯的地方：
以为同步失败会中止流水线。`pipeline.go` 的 `Run` 不是这么写的：

- `syncAccounts` 的错误走 `rep.AddError(...)`，而 `AddError` 的注释明写「非致命错误（流水线继续跑完）」；
- `Run` 里唯一的提前返回是 `ListEnabledAccountsWithWorkspace` 本身失败（第 402–406 行）；
- 第 426 行 `p.notifyImportant(ctx, rep)` 是**无条件**执行的，不看 `AccountsSynced`。

⇒ 18100 会在同步全灭的情况下继续跑 1.5 发票候选、1.6 分类、2 垃圾清理、
**3 重要邮件提醒**、4 发票采集、5 推送+台账/汇总文档——
**其中 3 这一步读的是共享生产 schema 里的那 34 条积压，与它能不能同步无关。**

（`accounts` 来自数据库查询、不涉及解密，所以 `pipelineScopes(accounts)` 也是好的，
第 5 步的推送与汇总文档同样会照常生成。）

### 16.3 明早读日志时怎么区分这三轮

| | `synced=` | 特征 | 提醒 |
|---|---|---|---|
| 18099 | 正常（5） | 完整一轮 | 基准 |
| 18077 | 正常（5） | 与 18099 等价 | **第二个完整轮，是主要污染源** |
| 18100 | 0，且 errors 含 5 条解密失败 | 同步全灭但仍走到第 3 步 | 仍会推 34 条积压 |

⇒ 三轮都会调 `MarkEmailsNotified`（进程内锁不跨进程），
所以 34 条积压可能被推 1 次、2 次或 3 次，**结果不可预测**。
§11 的「24 + 34 = 58」只能当**下界**看，不能当预测。

### 16.4 顺带记一条我自己刚犯又刚纠正的错

「某实例解密失败 ⇒ 它不会推提醒」这个推断听起来很稳，但它依赖一个**我没验证的前提**：
同步失败会中止流水线。实际不会。正确顺序是先读 `Run` 的控制流，
再对每个实例的能力下结论——而不是从「解密失败」直接外推到「整轮无害」。

这与 §12.3 那条同源：**缺席/失败的证据要先证明它对后续步骤意味着什么。**

---

## §17 刷新 08:00 基线——01:06 那份**已经漂了**，并记一个 psql 陷阱

§11/§16 引用的基线取于 01:06，而并发会话整晚在触发 sync/classify。
不刷新的话，08:00 的差值就是从一个错数起算的。重取于 02:00：
`logs/zz-8am-baseline-refresh-20261003-0200.txt`（psql exit=0，stderr 为空）。

```
=== A notifications ===   total 24 | unread 0 | read 24 | newest 2026-10-02 05:38:33
=== B invoices ===        downloaded 1 / 3500.00   |   new 3 / 58454.50
=== C the 58000 row ===   inv_1790903383222583800_1  new  file=(none)  attempts=0
                          updated 2026-10-02 09:09:43
=== D importance ===      (empty) 4 | high 58 | medium 56 | low 61
=== E eligible ===        175
=== F emails total ===    179
```

### 17.1 与 01:06 基线的差异

| | 01:06 | 02:00 |
|---|---|---|
| `notifications_unread` | 23 | **0** |
| `emails_total` | 175 | **179** |
| `importance` 为空 | 0 | **4** |
| `notifications_newest` | 2026-10-02 05:38:33 | 同上（未变） |
| 发票 / 58000 行 | — | 未变 |

**未读从 23 掉到 0**：24 条全部被读过。不是我做的（我只做过只读 fetch 与 CDP 读文本），
最可能是并发会话的 e2e 自动化点了通知中心。**这会影响明早的观察**：
「未读数从 0 涨到 34」才是干净的信号；若从非零起算，就分不清是流水线推的还是别人点的。

### 17.2 那 4 封 importance 为空的邮件，正好就是新到的那 4 封

`179 - 4 = 175`，与 E 的 eligible 数吻合。**它们不会被 08:00 那轮提醒**：
`POCKET_KXMEMORY_BASE_URL` 未配 ⇒ 定时路径不做自动分类（§11.2），
新到邮件的 `importance` 恒为空 ⇒ 不进提醒窗口。
⇒ §11.1 的「本轮将推送 34」在 02:00 这个基线上**仍然成立**（eligible 仍是 175），
但前提是**明天那 4 封仍未被分类**——若并发会话明早又手工跑一次
`/api/emails/classify`，它就会被算进 34。

### 17.3 psql 陷阱：`to_char()` 在 bigint 列上报 "multiple decimal points"

这份 SQL 第一版连着两次死在同一句错误上：

```
ERROR:  multiple decimal points
```

出错的那一行**一个小数点都没有**。真实原因：`notifications.created_at` 与
`email_invoices.updated_at` 都是 **bigint epoch 秒**（实测 `pg_typeof` = bigint，
值 1790890713）。`to_char(bigint, text)` 会静默解析成 `to_char(numeric, text)`，
把 `'YYYY-MM-DD HH24:MI:SS'` 当作**数值格式化模板**去解析，于是抛
"多个小数点"——错误文本完全不指向真正的原因。

正确写法：

```sql
to_char(to_timestamp(max(created_at)), 'YYYY-MM-DD HH24:MI:SS')
```

⇒ 这类错误有个共同特征：**报错文本与出错行没有任何词面关联**。
遇到荒谬的解析错误，先 `SELECT pg_typeof(col)`，别在那一行里找小数点。

（另一个同场教训：先用中文注释写 `-f` 的 SQL 文件时我怀疑是编码，
实测 `server_encoding` 与 `client_encoding` **都是 UTF8**，编码不是原因——
把注释换成英文也没用，因为真正的错在查询里。两个假设都要用实测排除，不能只换一个就下结论。）

---

## §18 更正 §16.3 的框架：重复提醒是**顺序**问题，最可能只发生一次

§16.3 我写的是「34 条可能被推 1 次、2 次或 3 次，58 只能当下界」。
那是**上界**，没说清概率。这一节把机制查实，并给出更可能的结局——
**它可能让「要不要打断并发会话」这个问题不再紧急**。

### 18.1 落库层没有任何兜底（实测）

```
pg_constraint on opencode_pocket.notifications:
  notifications_pkey | p | PRIMARY KEY (id)      <- id 每次插入新生成，不是业务键
  （另有两个普通索引 idx_notif_ws_time / idx_notif_unread，都非唯一）
pg_constraint on opencode_pocket.emails:
  UNIQUE (account_id, message_id)                 <- 邮件去重，与提醒无关
  notified_at 上没有任何约束
```

⇒ 代码层的去重**只是**一句 `UPDATE emails SET notified_at=$1 WHERE id=ANY($2)`
（`store_pipeline.go:155-161`），配合读取时的 `COALESCE(notified_at,0)=0` 判断。
**没有唯一约束兜底**，所以竞态一旦发生，重复通知会真的落库。

### 18.2 但去重是「读—推—标记」，所以**后到者会跳过**

`notifyImportant`（`pipeline.go:998-1050`）的顺序是：

```
1. ListEmailsSince(ctx, since, 2000)   -> 拿到 (emails, notified) 快照
2. splitReminderCandidates(emails, notified)   -> 对着**那一刻的快照**判定
3. for 每个 candidate: Notifier.NotifyImportantEmail  -> 逐条 INSERT notifications
4. 循环结束后才 MarkEmailsNotified(ids)         -> UPDATE notified_at
```

**没有锁、没有事务、没有推送前复查。** 但正因为快照在第 1 步、标记在第 4 步，
**只要某个进程在另一个进程读完快照之后才读完快照，它就会看到 notified_at≠0 而整批跳过。**

### 18.3 谁会先到？——18100，因为它同步最快地**失败**

- **18100**：解密在本地就失败，**不产生任何 IMAP 往返**（fetcher 日志 0 行），
  所以第 1 步几乎瞬间结束，接着 1.5/1.6/2 都是轻量的，直接进第 3 步。
- **18077 / 18099**：要做真实的 5 账户 IMAP 同步（18099 的日志里单次
  `/api/emails/sync` 就要 2.5–3 秒，5 个账户更多），之后还有 1.5 发票候选
  （可能去下正文链接）和 1.6 分类，才到第 3 步。

⇒ **最可能的结果：18100 先读快照、推 34 条、标记；18077 与 18099 随后读到
快照时 `notified_at` 已非 0，`RemindersPending=0`，一条都不推。**
也就是**只推一次，总数 58**。

### 18.4 但这仍是**预测**，明早要这样判别

- 若三份日志里**只有一个**出现 `本轮将推送 34 条` + `reminders sent:` ⇒ 上面的顺序成立，58 成立。
- 若出现 2~3 份 ⇒ 三者快照落在同一秒内，重复发生，此时**总数会 >58**，
  且不能把多出来的部分算到任何一个进程头上。
- 判别用每份日志的 `RemindersSent` 之和与 `notifications` 实际增量对账，不要只看 DB 差值。

### 18.5 顺带记一个与 08:00 无关、但真实存在的健壮性缺口

即使**只有单进程**，这段「先 INSERT 后 UPDATE、中间无事务」的写法也有一个洞：
**进程在两者之间崩溃 / 被杀，下一轮会把这批邮件重推一遍。**
更普遍地说，只要将来出现多实例部署（这个仓库现在就有 4 个 pocketd 实例在跑），
重复就是结构性的。

正解是让数据库兜住，而不是靠应用层的列值判断：
`notifications` 上加一条以「来源邮件」为业务键的**部分唯一索引**，或
用 `pg_try_advisory_lock` 把「读快照—推送—标记」整段包起来。
**本轮没有动手改**——那是行为变更，涉及幂等语义与历史数据该不该回填，
该由你拍板，不是我顺手就能定的。

（`pending_high` 我用独立 SQL 复核过：`date >= now()-90d AND deleted_at=0
AND notified_at=0 AND importance='high'` = **34**，与 01:14 那份只读诊断的
`RemindersPending=34` 完全吻合。两个口径互相对上了。）

---

## §19 幽灵卡：把「怎么处理」这个待拍板项需要的账算清楚

§2/§13 记的是「设备渲染 12 张 / 服务端 4 行」，那是数按钮数出来的，粗且不准。
02:10 那次真机抓包给了完整页面文本（`logs/zz-invoice-ui-verified-20261003-0210.txt`，
末行是「没有更多了」，**确认未截断**），按 `hide_image` 卡片标记逐张解析，
再与服务端逐行对账，得到下面这张表。

### 19.1 设备 7 个实体 vs 服务端 4 行

服务端 `email_invoices` 实测（`logs/zz-ghost-count.out.txt`）：
`rows_total = 4`；`seller='name:'` 的行 **0**；`invoice_no='Issuance'` 的行 **0**。

| # | 设备上的实体 | 服务端对应行 |
|---|---|---|
| 1 | Tencent Cloud ¥328.50 待整理 `No.24317200000907012703` | ✓ `inv_1790957532673098600_2` |
| 2 | Tencent Cloud ¥126.00 待整理 `No.24317200000907012698` | ✓ `inv_1790957532670968300_1` |
| 3 | 中国工商银行 ¥58,000.00 待整理 | ✓ `inv_1790903383222583800_1` |
| 4 | 杭州创客家 ¥3,500.00 已下载（文件式行） | ✓ `inv_1790884695419622800_1` |
| 5 | **`name:` ¥0.00 已归档 `No.Issuance`** | ✗ **幽灵** |
| 6 | **`name:` ¥0.00 失败 `No.Issuance`** | ✗ **幽灵** |
| 7 | **云服务开票中心 ¥1,280.00 待下载 `No.25332000000123456789`** | ✗ **幽灵** |

⇒ **3 张幽灵卡**，不是 8 张。§2 那个「12」是数「删除」按钮得来的——页面上
除卡片外还有别的删除入口（文件列表/选择态），按按钮数会高估。
**卡片数要用 `hide_image` 标记数，不是按钮数。**

### 19.2 三张幽灵**不是同一类**，这会改变处理方式

- 第 5、6 张：`seller='name:'`、`invoice_no='Issuance'`、`amount=0`。
  它们是 §13.2 说的那两张 QQ Wallet——**当年就从服务端删掉了**，
  本地镜像把 `last_error` 里的旧文案（`POP3-sourced email raw body cache miss…`）留着当卡片副标题。
  其中一张状态是**失败**、一张是**已归档**。
- 第 7 张完全不同：`seller='云服务开票中心'`、`amount=1280`、状态**待下载**、
  单号 `25332000000123456789`。它**不是**删除残留，而是一条服务端从来就没有过的记录。

⇒ 值得先问的是第 7 张：**它是怎么进本地镜像的？** 如果是某条写入路径
（比如入账/手工添加）能把服务端不存在的行写进本地表，那问题就不只是"幽灵卡怎么清"，
而是"本地表可以被写成服务端不认的行"。**这是两件不同的事，本轮没有查。**

### 19.3 页面头部与列表自相矛盾（另一个可核查的证据）

```
¥3,500.00
共 4 张 · 已归档 0 · 文件 6
```

- 「共 4 张」= 服务端计数，与 `rows_total=4` 一致；
- 但设备列表有 **7** 个实体；
- 「已归档 0」与第 5 张卡片标着**已归档**直接矛盾（服务端 `downloaded=1 / new=3`，无 filed）。

⇒ 这三行不是"旧缓存"，是**服务端权威数**与**本地镜像状态**被并排渲染的结果。
这也是为什么 §2 那个待拍板项本质上是：**本地镜像要不要接受服务端裁剪**。

### 19.4 供拍板的三条路（我没有替你选）

1. **按服务端权威集裁剪本地镜像**：最省事，`local_email_invoices` 只保留服务端返回的行。
   代价：`待下载`（第 7 张）这种**纯本地状态**会被一起清掉——而它可能正代表一条
   尚未采集完成的真实工作。
2. **给本地表补 `workspace_id` / `user_id`**（`schema.ts:244-269` 现在两列都没有），
   先解决隔离与归属问题，裁剪另议。代价：这是**设备 DB 迁移**，真机从未验证过。
3. **不裁剪，只修呈现**：承认本地镜像可以有服务端没有的行，但不该把
   `last_error` 旧文案渲染成卡片副标题（§13.1 第 1 条）。
   最小改动，但 3 张幽灵卡仍然在。

**第 7 张的来源没查清之前，这三条我都没法评估代价。** 那是我建议的下一步。

---

## §20 查清了第 7 张的机制——并因此**否掉 §19.4 的第 1 条路**

### 20.1 本地表是**设计上**就允许存在服务端没有的行

不是 bug，是明确设计。证据链（全部是仓库里现成的代码与用例）：

```
native/list-sync/planner.ts
  isLocalOnlyId(id)         把 'local-' 前缀识别为「仅本地」
  newLocalId('inv')         生成 local-inv-<...> 临时 id
  planListSync(local, remote, { pushLocalOnly })
      planner.test.ts:34  'local-only row is pushed by default'
      planner.test.ts:39  'local-only row stays local when pushLocalOnly is false'
                            ← pushLocalOnly=false 时它就**永远留在本地**

features/email/invoice-list-sync.ts:9-29
  matchInvoiceForAlign(local, remote)
      注释：本地临时票对齐服务端：同邮件 + 票号，或同邮件 + 销售方 + 金额
      硬条件：r.emailId !== l.emailId 直接 return false   （第 19 行）

features/email/invoices-store.ts
  upsertFromServer()  要求 inv.id，无 id 的跳过
  remapLocalId()      把本地临时 id 换成服务端 id；目标已存在则删掉本地副本
  listDirty() / clearDirty() / setLocalStatus()   ← 未回推的本地改动的账
native/schema.ts:268  local_email_invoices.client_id TEXT DEFAULT ''
```

### 20.2 于是有两条路会让 `local-inv-*` 永久留在本地

1. **回推没成功**（`pushLocalOnly=false`、outbox 失败、或服务端拒绝）。
2. **remap 对不上**：`matchInvoiceForAlign` 硬要求 `r.emailId === l.emailId`。
   只要服务端那一行来自**另一封邮件**（同一张票被重新采集、或原邮件被删后换源），
   就永远匹配不上，本地行就一直以 `local-inv-*` 的身份渲染成卡片。

第 7 张（`云服务开票中心` ¥1,280 `待下载` `No.25332000000123456789`）的形态
与第 2 条完全吻合：它有一个**具体的发票号**、非零金额、真实商家、
状态是「待下载」而不是失败——这不像删除残留（那两张是 `name:` / ¥0.00），
更像一条**建了档、但上游始终没有对应行**的本地票。

### 20.3 这否掉了 §19.4 的第 1 条路

「按服务端权威集裁剪本地镜像」会**删掉尚未回推成功的真实发票**。
这不是"可能有损失"，而是与 `client_id` / `remapLocalId` / `listDirty` /
`pushLocalOnly` 这整套机制的设计意图**直接冲突**——那套机制的存在意义
就是让服务端暂时没有的行能留在设备上。

⇒ §19.4 的三条里，第 1 条**应当排除**，除非同时改成"只裁剪 `dirty=0`
且从未有过 `local-` id 的行"，而那已经是另一套逻辑了。
剩下第 2 条（补 `workspace_id`/`user_id`）与第 3 条（只修呈现）才是真正在桌上的。

### 20.4 但有一件事我**没有验证**，别替我补上

我**没有读到设备上的 `local_email_invoices` 表**（本地库在设备里，
读取需要 vault 解锁或 root），所以：

- 第 7 张的 id **是不是** `local-inv-*`，**未证实**；
- §20.2 那两条机制是**从代码读出来的**，不是从这台设备上观察到的。

要证实只需一件事：在设备上执行
`SELECT id, seller, amount, status, client_id, dirty FROM local_email_invoices`
（解锁后用 CDP 求值，或 `adb shell run-as` 读 SQLite）。
**一条查询就能把 §20 从"机制成立"变成"这张卡就是这个机制产生的"。**
在那之前，§20.3 的结论应读作「按设计就该排除第 1 条」，
而不是「这台设备上的第 7 张已经被证明是 `local-inv-*`」。

---

## §21 ⚠️ 更正 §19 与 §20：设备直接查询推翻了这两节，**结论反过来了**

§20.4 我自己写了"一条查询就能把 §20 从『机制成立』变成『这张卡就是这个机制产生的』"。
02:15 跑了那条查询（走 App 自己的 `CapacitorSQLite` 插件，只读 SELECT，App 已解锁）。
**结果否掉了 §20 的假设，也否掉了 §19 的计数。**

存档：`logs/zz-local-invoices-20261003-0215.json`

### 21.1 实测：本地 12 行 / 服务端 4 行 ⇒ **8 张幽灵卡**

```
count local rows          = 12
present in server         =  4
GHOSTS                    =  8     <- §19 说 3，错了
client_id non-empty       =  0     <- 关键
rows with id like local-* =  0     <- 关键
dirty = 1                 =  1
```

### 21.2 §20 的机制假设**被否掉**

§20 断定这些是 `local-inv-*` 临时票、等着回推。实测：

- **12 个 id 全部是 `inv_<epoch>_<n>`**（服务端生成格式），**没有一个是 `local-inv-*`**；
- **`client_id` 12 行全为空**。

⇒ 它们**不是**"本地建的、服务端还没有"的行。它们**曾经存在于服务端**，
后来被**服务端删掉了**，而本地镜像从来没有跟着删。
§20.2 列的两条机制（`pushLocalOnly=false` / `emailId` 对不上）在**这台设备上
一条都没发生**——那两条是从代码读出来的，读对了，但**不适用于这里**。

**§19 的「3 张」也是错的**：我当时只数了页面上 `hide_image` 卡片的可见部分，
而页面把 `已下载` 的行放在文件区（截图里那 6 个「已下载 / 下载」入口），
所以少算了 5 行。§2 最早记的「12」其实是对的——那本来就是本地行数，
我上一轮把它误当成"数按钮得来的高估值"了。

### 21.3 8 张幽灵**不是随机的**，是按发票号成组的

```
26332000000907012703  Tencent        328.5  new         -> 服务端有
24317200000907012698  Tencent        126    new         -> 服务端有
58000 中国工商银行                  58000  new         -> 服务端有
26332000008261110741  杭州创客家      3500  downloaded  -> 服务端有 1 条
26332000008261110741  杭州创客家      3500  downloaded  -> 幽灵（同号重复采集）
25332000000123456789  云服务开票中心   1280  pending     -> 幽灵
25332000000123456789  云服务开票中心发票抬头 1280 downloaded -> 幽灵
25332000000123456789  云服务开票中心   1280  downloaded  -> 幽灵
Issuance              name:          0      filed       -> 幽灵（且 dirty=1）
Issuance              name:          0      failed      -> 幽灵
（无单号）             财务部          0      downloaded  -> 幽灵
（无单号）             财务部          0      downloaded  -> 幽灵
```

三个发票号各自对应 2~3 条本地行，服务端只留 1 条或不留。
⇒ **同一封/同一张票被重复采集过多次**（采集侧没有按 `invoice_no` 去重），
服务端后来清掉了多余的，本地镜像留着。

### 21.4 所以 §20.3 的结论**反过来了**

§20.3 说「按服务端权威集裁剪本地镜像会删掉尚未回推成功的真实发票，应当排除」。
**在实测数据上这个顾虑不成立**：

- 没有任何一行在等回推（`client_id` 全空、没有 `local-*` id）；
- 这 8 行是服务端**主动删掉**的，保留它们只会让用户继续看到已经不存在的账。

⇒ **「按服务端权威集裁剪本地镜像」重新成为首选**，而且现在是**有实测支撑的**首选。
§19.4 的第 2 条（补 `workspace_id`/`user_id`）解决的是隔离问题，与裁剪不冲突，
可以叠加；第 3 条（只修呈现）仍然不解决 8 张幽灵卡本身。

**唯一要在裁剪时单独处理的**是那 1 行 `dirty=1`
（`inv_1790789580385036500_1`，`name:` / `filed`）：它是**唯一**有未回推本地改动的行。
但服务端已经没有这条 id 了，`listDirty` 推过去只会拿到 404——
**它本来就推不上去**，不是裁剪会造成的损失。裁剪时按
「服务端没有 + 非 dirty」筛的话，它会被留下而不是被删，这是对的。

### 21.5 顺带暴露一个采集侧的问题（独立于裁剪决策）

同一个 `invoice_no` 在本地出现 2~3 次，说明**发票采集没有按发票号去重**。
服务端目前靠事后清理压住（只剩 4 行），但只要采集继续跑，
重复行还会不断产生——**裁剪只是把症状擦掉，根因在采集侧**。

这需要单独查 `extractInvoiceCandidates` / `HarvestAll` 的写入路径有没有
`invoice_no` 维度的存在性检查。**本轮没查，也没改。**

### 21.6 这一节的教训

§19 我凭一张截图数出「3 张」，§20 我又在这个数上建了一整套机制推断，
两节都写得很确定。**推翻它们的是我自己在 §20.4 里点名的那条查询。**
⇒ **截图与页面文本只能证明"渲染出了什么"，不能证明"表里有什么"。**
表里有几行、id 是什么形态、哪些字段为空，只有查表才知道。

---

## §22 查清了 §21.5 的根因：**不是去重失效，是权威合计缺"票级身份"**

§21.5 说「同一 `invoice_no` 出现 2~3 次 ⇒ 采集没按发票号去重」。查完要**修正这个说法**。

### 22.1 去重完全按设计工作

`invoice_store.go:93-110`：

```sql
INSERT INTO email_invoices (...) VALUES (...)
ON CONFLICT (email_id) DO UPDATE SET ...
```

冲突目标是 **`email_id`**，即**每封邮件一行**。`invoice_dedup_test.go:42`
（`TestUpsertInvoice_SameEmailIsIdempotent`）就是在钉这一条。

设备那 12 行按 `invoice_no` 分组后，**每组的不同 `email_id` 数都等于行数**：

```
(empty)                  rows=3  distinct_email_id=3
26332000008261110741     rows=2  distinct_email_id=2
25332000000123456789     rows=3  distinct_email_id=3
24317200000907012703     rows=1  distinct_email_id=1
24317200000907012698     rows=1  distinct_email_id=1
Issuance                 rows=2  distinct_email_id=2
```

⇒ **没有一行是同一封邮件建了两次。** 重复的成因是**同一张票出现在不同邮件里**
（厂商首发一次、提醒一次、更正一次），这是真实世界常态，不是 bug。

### 22.2 真正的问题在**合计那一侧**

`LedgerRows`（飞书台账）与 `InvoiceListStats`（列表统计）都只共用
`InvoiceCountsTowardTotal`（status ∈ downloaded/filed 且 file_path 非空），
**两处都不看 `invoice_no`**。于是：

- 同一张票在 3 封邮件里建了 3 行；
- 若其中 2~3 行都走到 `downloaded` 且有文件；
- **权威合计就把这张票算 2~3 次。**

设备镜像里 `25332000000123456789` 正好是 **2 downloaded + 1 pending**（3 封邮件）。
那 2 行若还在服务端，合计会多算 1280——**这不是假设，形状已经摆在那里**。

服务端现状（`logs/zz-invoice-dup-extent.out.txt`）：

```
rows_total=4  distinct_email_ids=4
按 invoice_no 分组：每个号各 1 行
权威口径 counted_rows=1  amount_sum=3500.00
```

⇒ **当前没有错账**，这是**潜在**风险。服务端那 4 行是干净的，历史重复已被清掉。

### 22.3 它与历史那个 61,500 是同一族，但缺的那一环不同

61,500 那次（`InvoiceListStats` 注释原话）是「**完全没有过滤**，把整张表求和」。
这次过滤是**对的**，缺的是**票级身份**：合计知道"哪些行算"，不知道"它们是不是同一张票"。

建议（**是行为变更，涉及钱，不擅自动手**）：合计在累加前先按
`invoice_no`（非空时）去重，同号只取一行；`invoice_no` 为空时才退回逐行计。
落点是两处共用的那个口径，或在 `InvoiceCountsTowardTotal` 之上加一层
「票级唯一」的过滤——但**前端 `sumByCurrency` 必须同步改**，
否则同一张票在列表页会被算一次、在台账里又算一次。

### 22.4 需要你拍板的一个口径问题

**同号不同金额**时以哪一行为准？设备上 `25332000000123456789` 的三行金额都是 1280，
但现实里"更正邮件"可能改金额。选项：

1. 取 `updated_at` 最新的那行（更正邮件赢）；
2. 取 `created_at` 最早的那行（首发邮件赢，语义上更接近"这张票本来是多少"）；
3. 视为两张票分别计入（保持现状）。

**这三条的账都不一样**，且都会改变已核对的数，所以必须你定。
在此之前 §22.2 的结论应读作「有据可查的潜在风险」，不是「已经算错了」。

---

## §23 并发会话正在修 §18 那个跨进程竞态——但**今晚 08:00 不会生效**

02:20 发现工作区里出现了 `backend/internal/email/pipeline_lock.go`
（外加两个 `.negbak`——他们在跑负控）。只读查了一遍，结论如下。

### 23.1 实现是**跨进程**的，而且接线接对了

`pipeline_lock.go` 用 `pg_try_advisory_lock`（**会话级**，`DailyPipelineLockKey`
= `"email:daily-pipeline"`），正是 §18.5 建议的那条。三处做得比我预期的周全：

1. **三态而非两态**：`Busy`（别的实例在跑 → 跳过）与 `Unavailable`
   （取不到连接/查询报错 → **降级照常跑**）。这样一次数据库抖动不会让
   每日流水线永久静默，而日志里能区分「别人在跑」与「锁坏了」。
2. **会话级锁的归还陷阱**：解锁失败时**销毁连接**（`Hijack` + `Close`）而不是
   `Release` 回池子——否则下一个借用这条连接的查询会继承锁，
   每日流水线被永久锁死且无错误日志指向原因。
3. **测试里显式验证它没有退化成进程内状态**：`lockProbeOnFreshConn` 用同池的另一条
   连接去取锁，取到就 `t.Fatal("the lock is not session-scoped, so it cannot
   protect multiple pocketd instances")`。

接线在 `server_email_pipeline.go:256-275` 的 `RunEmailPipeline`——**只有定时路径取锁**，
手工路径 `handleEmailPipelineRun` 刻意绕过，用户显式点「跑一次」不会被另一轮挡住。
开关 `POCKET_EMAIL_PIPELINE_ADVISORY_LOCK` **默认 true**（`config.go:315`）。

⇒ 这份工作是对的，且比我 §18.5 写的建议更完整。

### 23.2 但**跑着的二进制里没有它**（字节探测，实测）

`logs/zz-probe-lock-in-binary-20261003.mjs`：

```
=== pocketd-invoicenan-fix.exe（18099 现在跑的就是它）===
  ABSENT   每日定时流水线跨进程锁已被其它实例持有
  ABSENT   POCKET_EMAIL_PIPELINE_ADVISORY_LOCK
  ABSENT   email:daily-pipeline
  ABSENT   pg_try_advisory_lock
  ABSENT   daily pipeline already running in another instance
  present  control: pocketd listening          <- 对照：扫描本身有效
  present  control: pipeline scheduled at
```

`pocketd-invoicecheck.exe` 同样全缺。**这是「这个构建没有这条代码路径」，
不是「这条路径没跑到」**——两者不能混。

⇒ **今晚 08:00 那三个实例不会互斥**，除非并发会话提交后**重建 18099**
（重建会带进他们的在制品，需要你授权；上次我重建时是带着授权做的）。

### 23.3 所以 08:00 有两种可能签名

| 条件 | 预期日志 |
|---|---|
| 18099 未重建（当前状态） | 靠**到达顺序**取胜：18100 同步全灭最先到第 3 步，推 34 并标记；18077/18099 随后 `RemindersPending=0`。**一份**日志出现「本轮将推送 34 条」+「reminders sent:」 |
| 18099 被重建且带锁 | **一份**出现「本轮将推送 34 条」，另**两份**明确打印「每日定时流水线跨进程锁已被其它实例持有，本轮跳过」 |

两种签名的结论相同（**只推一次，总数 58**），但要找的字符串不同。
⇒ **先看三份日志里有没有「跨进程锁…本轮跳过」这一行**，有就是走了锁，
没有就是走的顺序。08:00 的提醒里已写明这个判别顺序。









---

## §15 ¥NaN 修复已上 18099（前后字节对照）；但 08:00 归因已被 3 个实例搅了

### 15.1 修复已上线，同一端点前后两次抓包只差那三个键名

用户 01:29 授权 build + 重启。本轮实际做的：

```
go build -o ..\logs\pocketd-invoicenan-fix.exe ./cmd/pocketd   exit=0   （含并发会话对
                                                                    llm_gateway_handler.go 的 WIP）
go vet ./internal/server ./internal/email                     exit=0
scripts/restart-18099-aligned-secret.ps1 -Exe ...invoicenan-fix.exe
  -> 旧 pid 21404 已停；新 pid 8168 于 01:32:59 起来
  -> 三道闸门全过：Postgres pool initialized / Email credential self-check: all 5
                   / daily pipeline runner injected
  -> pipeline scheduled at 2026-10-03T08:00:00+08:00
/healthz -> 200 ok
```

**旧二进制 `logs/pocketd-invoicecheck.exe` 故意没覆盖**，留作回退路径与对照物。
为此给重启脚本加了 `-Exe` 参数（默认值仍是旧那个，裸调用行为不变）。

前后对照用的是**同一个端点 + 设备真实 session token**（WebView 里 `pocket_token`，
291 字符的裸 JWT），两次相隔约 4 分钟：

- 修复前：`"amounts":[{"Currency":"CNY","Amount":3500,"Count":1}]`
- 修复后：`"amounts":[{"currency":"CNY","amount":3500,"count":1}]`

存档：`logs/zz-invoice-wire-PREFIX-20261003-0130.txt`、
`logs/zz-invoice-wire-POSTFIX-20261003-0134.txt`。

**其余字段逐字节相同**：`amountTotal=3500`、`count=4`、`downloaded=1`、`pending=3`、
4 行 `rows` 完全一致、58000 那行仍是 `status=new`。
⇒ 两次之间唯一变化的变量就是 `CurrencyTotal` 的 json tag，与预期完全吻合。

顺带把「为什么破口只有一处」钉死：同一次响应里 `rows[].amount`、`rows[].currency`、
顶层 `amountTotal` / `count` / `currency` **全是小驼峰**，只有 `amounts[]` 里是大驼峰。
`CurrencyTotal` 是这一坨里唯一漏 tag 的结构——这也是当初它能一路活到真机的原因：
Go 侧解进 struct 时大小写不敏感，测试全绿。

### 15.2 UI 那一半**已补验**（2026-10-03 02:10，原文此节记的是"未验"）

原文写的是"未验"，因为当时真机停在本地加密库解锁页。**后来并发会话把 App 解锁了**，
02:10 重新验成——`pocket_api_base` 已被切回 `http://127.0.0.1:18099`（新二进制那个）：

```
¥3,500.00
共 4 张 · 已归档 0 · 文件 6
```

截图 `logs/zz-invoice-total-verified-20261003-0210.png`，页面文本
`logs/zz-invoice-ui-verified-20261003-0210.txt`。**`¥NaN` 这个症状消失了。**
与修复前的 `logs/zz-invoice-nan.png` 构成前后对照。

### 15.2.1 但这张截图**证明不了**它是走了哪条路（与测试侧同一个陷阱）

本仓库这批数据里，服务端标量 `amount=3500` 与 `amounts[]` 分组金额 **3500 是同一个数**。
所以这一屏无论走「amounts 路径」还是「落回标量路径」都显示 `¥3,500.00`，
**渲染结果区分不了两者**。

⇒ 「走的是 amounts 路径」这句结论的依据不是这张截图，而是：
① wire 抓包里 `amounts[0]` 的键名已经是小驼峰（§15.1）；
② `resolveSummaryGroups` 的优先级是 amounts > 标量 > 本地重算，且
   新加的第 8/9 条用例（`invoice-totals-wire-keys.test.mjs`）已把这条钉住。

这与本轮在测试侧发现的**同一个陷阱**（夹具让两条路径同值 ⇒ 判据装饰化），
只是这次它出现在**真机观测**上而不是测试夹具上。记在这里以免下一个人
把这张截图当成「amounts 链路在真机上被验证过」。

---

### 15.2 原文（保留：那为什么会验不成）

真机当时停在「检测到已有登录态，但本地加密库未解锁」：

```
pocket_crypto_cfg = {"fieldEncryption":"disabled","hasMasterPassword":true,...}
```

解锁只有两条路（`features/auth/unlock-auth.ts:35-43`）：生物识别，或主密码。
**我没有主密码，也不猜**，所以当时只能验到 wire 层。补验见上面的 15.2。

### 15.3 ⚠️⚠️ §14.2 已经不成立了：现在有 **3 个生产 schema 实例**各排了一次 08:00

§14.2 写的是「08:00 验收期间只保留 18099 这一个实例」。实测已经不成立，**而且都不是我起的**：

| pid | 端口 | data dir | PG schema | 启动时间 |
|---|---|---|---|---|
| 8168 | 18099 | `C:\workspace\openpocket\data` | `opencode_pocket` | 10-03 01:32:59（本轮重启） |
| 28160 | 18100 | `.wt-e2e\backend\data` | **`opencode_pocket`** | 10-02 22:26:49 |
| 23256 | 18077 | **`C:\workspace\openpocket\data`** | **`opencode_pocket`** | 10-03 00:02:13 |
| 12664 | 18101 | `.wt-e2e\backend\data-verify` | `opencode_pocket_verify` | 10-03 00:45:52 |

前三个的启动日志里都有 `pipeline scheduled at 2026-10-03T08:00:00+08:00`。

- `emailPipelineMu` 是**进程内**锁，跨进程完全不互斥
  ⇒ 08:00 会有**最多三轮**流水线同时打生产库。
- 更糟：18077 与 18099 **共用同一个 data dir** ⇒ 同一份 `email_master.key`、同一批 5 个账户。
  三轮会对同一批邮件各自跑 `MarkEmailsNotified` 并各自推提醒。
- 18101 走 `opencode_pocket_verify`，**不影响**生产归因。

⇒ §11 的「24 + 34 = 58」**不再能直接采信**：明早的差值是三轮叠加的结果
（同时跑则各看到 34 条 pending 各自推；串行跑则后两轮可能看到更少）。
连 58000 那行会不会被采集都变得不确定。

处置要用户拍板——这些进程不是我的，我没有停它们的授权：

- **a) 08:00 前停掉 18100 与 18077，只留 18099**。最干净，代价是打断并发会话正在做的 e2e。
- **b) 让它们跑，归因改为按进程分读日志**。可行：18100 的日志在
  `.wt-e2e\logs\pocketd-18100-*.err.log`，18077 的在
  `openpocket-wt-a20\logs\audit-18077-*.err.log`，各轮报告行落在各自文件里。
  但要带 §12.4 那条约束：**只能拿二进制里实际的字符串去对**，旧二进制的日志文案与当前源码不一致。
- **c) 推迟 08:00 验收**。

### 15.4 顺带记一笔：设备的 API base 在验收途中被切走了

```
01:30  pocket_api_base = http://127.0.0.1:18099
01:34  pocket_api_base = http://192.168.31.20:18101
```

⇒ 并发会话正在用这台真机跑它自己的 e2e（18101 就是它那个 verify-schema 实例）。

对我的验证的直接后果：**不能依赖 App 的默认 base**。15.1 的两次抓取都是在 WebView 里
**显式 fetch 18099** 完成的，`adb reverse tcp:18099` 至今完好。
我没有把 base 改回去——那是别人的配置。
