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
