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
