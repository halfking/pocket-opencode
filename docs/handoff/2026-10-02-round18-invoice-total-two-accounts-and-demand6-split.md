# round18 —— 发票合计两套口径已修；两条误报撤回；需求 6 改走拆分路线

> 日期：2026-10-02　提交：`bcd27ebd`（已推 origin/main）
> 本轮最重要的三件事：**一条真发现被我撤销过又被还回来**、**两条「服务/账户故障」结论建立在别人的进程上**、
> **需求 6 的落地路线与既有 `2026-10-02-demand6-ondevice-plan.md` 不是同一条**。

---

## 0. 先读这一节：三条会让下一轮重新踩错的更正

### 0.1 「台账合计虚高 17.6 倍」是**真发现**，我撤销过又还回来了

我先报「发票页显示 61,500、虚高 17.6 倍，先别推飞书」，随后**撤销**了它，
理由是「`ledger.go:120` 只计 downloaded/filed 且有文件，线上报的就是 3,500」。

**撤销是错的。** 我只核了一条消费路径就宣布平反。真实库实跑（2 行发票：
3500.00 downloaded+有文件、58000.00 new+无文件）：

| 消费方 | 判据 | 修之前 |
|---|---|---|
| `LedgerRows`（ledger.go）→ 飞书表格 + CSV | `status IN (downloaded,filed) AND file_path<>''` | CNY 3,500 |
| `InvoiceListStats`（invoice_list.go）→ **发票列表 API** | **无任何过滤** | CNY **61,500** |
| 前端 `resolveSummaryGroups`（invoice-money.ts） | 优先读 API 的 `amounts` | 页面显示 **¥61,500** |
| 前端 `sumByCurrency(list)` 兜底重算 | **无任何过滤** | 61,500 |

**教训（已写进 agent memory）**：撤销一条发现所需的证据**多于**发现它。
撤销前必须 `grep` 出所有读该字段的地方（后端聚合 + SQL 层聚合 + 前端兜底重算），
逐条实跑口径，而不是找到一条「看起来对」的就宣布平反。
前端有 `sumByCurrency(list)` 这类兜底重算时，它与服务端 totals 是两条独立口径。

### 0.2 「服务已恢复：PID 81664，端口 18099」是**误报**

我 `Start-Process` 起进程，拿回 PID，`Invoke-WebRequest /healthz` 返回 `ok`，
就写了「已恢复」。该进程其实**启动即退出**：

```
19:11:50 pocketd listening on :18099
19:11:50 listen tcp :18099: bind: Only one usage of each socket address...
```

那个 `ok` 和随后所有 API 响应都来自 **PID 75084**——并发会话 19:03:31 自己启的
`C:\workspace\openpocket\backend\.verify-bin2\pocketd.exe`，它先抢到了 18099。

**教训**：`Start-Process -PassThru` 的返回值和「进程还活着」是两件事；
`Invoke-WebRequest` 不区分是谁在应答。启动后必须重新 `Get-Process`，
并用 `Get-NetTCPConnection` 查端口的 `OwningProcess` 对上号。
**最好让 `/healthz` 回显 pid/启动时间**，让日志与应答能互证。

### 0.3 「5 个账户里 3 个同步失败」**结论不成立**

建立在那次误报之上的推论全部作废：正确配置的实例（金丝雀 78488）
全量同步返回 `{"new":2,"synced":5}` 且**无 `failed` 字段**，5 个账户全部成功。
那 3 个账户的失败是 75084 那份 env 的属性，**不是代码缺陷**。
这条线索归并发会话，不该由我下代码结论。

### 0.4 「只有 2 个账户在按周期跑」也是**误报**

我做了两次相隔 100 秒的快照，水位没动，就下了结论。但 `sync_interval_min` 全是 15，
调度器每 60s 一轮——**100 秒的窗口里大多数账户根本不该到期**。

26 分钟连续快照（`logs/watch_watermark.log`）的真实情况：

| 账户 | 当时结论 | 实测 |
|---|---|---|
| feikemanager@163 | 落后 | 18:29 → 18:44，正好 15 分钟一轮 |
| kimmy.huang@163 | 落后 | 18:40:01 → 18:56:01，准点 |
| 56551681@qq | 落后 25 周期 | 12:03:01 → **18:58:01**（7 小时空窗后恢复，uid 10458→10459） |
| huangxutao@kxpms | 落后 66 周期 | 确实卡在 01:37:22 |

**教训**：采样窗口必须先和被观测的周期比对，至少覆盖 2 个完整周期再下结论。
找因果链（`pg_stat_activity.backend_start` 与 `last_synced_at` 对齐）比找静止可靠。

---

## 1. 真实缺陷已修：`bcd27ebd`

病根是**同一条合计规则被手写了三遍**，前两处逐字符相同、第三处漏了，
而三处各自只测自己。修复：

1. 判据收敛到唯一函数 `InvoiceCountsTowardTotal`（`ledger.go:44`），
   导出 `InvoiceVerifiedLabel` 供展示；两处内联表达式改为调用它。
2. `InvoiceListStats` 的 SQL 补上对应过滤（无法复用 Go 函数，故用护栏钉住相等）。
3. 前端新增 `invoiceCountsTowardTotal`，`sumByCurrency` 的 `status` 设为**必填**——
   给默认值等于把「构造不出凭证的行」悄悄放行，那正是要消灭的歧义。
4. 明细行与 CSV/飞书表头新增**「核验」列**（未核验的行仍列出，只是不计入）；
   合计行张数改写为「**计入 N 张 / 共 M 张**」。
5. `LedgerCellRange` 列宽 9 → 10。

**为什么合计行要改张数**：只写「共 2 张」会让读者以为 3,500 是那两行的合计——
这正是本次缺陷在纸面上的表现。多币种时每行的「共」也是本币种张数，
三行各写「共 5 张」同样是误导。

### 防漂移护栏与其负控

`invoice_total_parity_test.go` 用**同一组夹具同时跑 SQL 与 Go 判据**并断言相等，
夹具覆盖四种「不计入」的理由（new/无文件、downloaded/无文件、failed/有文件、pending）。

负控实测：摘掉 SQL 的 `WHERE` 过滤后 `EXIT=1`，报
`CNY 组 SQL=72331/6 张，判据=4000/2 张`；撤销后 `EXIT=0`。
**另外两条用例在该变异下仍绿是 no-op（它们不碰 SQL），不计入验证。**

### 顺带修的夹具（值得记住的一类陷阱）

`invoice_list_stats_test.go` / `invoice_dedup_test.go` 里有一批
`status='downloaded'` 却 `file_path` 为空的行——**「下载完了但没有凭证」**，
正是新的「未核验」形态。改法是显式传 `filePath` 参数，**不留默认值**：
默认值会把这次的歧义重新藏起来，而夹具不真实时测试会在
「夹具错了」和「判据错了」之间二选一地变红，两者的修法完全相反。

另：`UpsertInvoice` 按设计**不写** `file_path`（那几列归 `UpdateInvoiceHarvest`），
所以在结构体里填 `FilePath` 不会落库——需要时用直接 UPDATE。

---

## 2. 需求 6：拍板结果与既有方案**不是同一条路线**

`2026-10-02-demand6-ondevice-plan.md` 写的是**路线 A：全在设备端**
（Java 重写 IMAP ≈1700 行 + Go 编 wasm ≈9800 行 + 设备本地 SQLite）。
该文档状态仍是「待用户拍板」，**现已拍板，但选的不是 A**：

> **拆分**：收信 / 分类留服务端，MOVE / 渲染 / 导出 / 推送上设备。

### 拆分解掉了路线 A 最硬的那条约束

路线 A 的关键技术结论是（该文档 §1，**有实测依据，本轮不推翻**）：

> Go 编到 WASM 后 `syscall/net_js.go` 的 `Socket`/`Connect`/`Sendto` 全部 `ENOSYS`，
> 编译期完全看不出来（`GOOS=js GOARCH=wasm go build` exit 0 产出 8MB wasm），运行时必挂。
> 因此设备端唯一能跑真实 TCP 的是 Java，`fetcher.go` + `pop3_fetcher.go` 必须重写。

**拆分方案里设备端永远不需要裸 IMAP**（收信在服务端），于是：
Java IMAP 重写、那三个必须照搬的坑（`mime.go:187-190` 的自夹 deadline、
POP3 预算共用、IMAP partial 与 literal 的空格）**全部不适用**。
这是拆分相对路线 A 最大的范围优势，应当明确记下来，别让后来者按 A 的成本估算。

### 拆分方案的接缝已确认存在

| 组件 | 位置 | 状态 |
|---|---|---|
| 意图表 | `email_action_intents`（store.go:294） | 已建：id/email_id/account_id/workspace_id/user_id/action/folder/reason/idempotency_key/status/error/时间戳 |
| 幂等 | `idx_action_intents_idem` UNIQUE(idempotency_key) | 已有 |
| 待处理索引 | `idx_action_intents_pending` 部分索引 | 已有 |
| 消费接口 | `IntentExecutor`（scheduler.go:45）`Execute(ctx, ActionIntent) error` | 已有 |
| 注入点 | `main.go:500` `SetIntentExecutor(NewIntentExecutor(store, crypto, emailFetcher))` | **已装配**，`emailFetcher` 就是真实 IMAP MOVE 执行器 |
| 消费循环 | `intentLoop`（scheduler.go:473）每分钟一轮 → `runIntents` → `ClaimActionIntents` | 已有 |

**设备侧执行器只要实现同一个 `IntentExecutor` 接口。**

### 拆分方案缺的那一块（下一步该做的）

`runIntents`（`scheduler.go:488`）目前**由服务端无差别 claim 全部 pending 意图**。
要让 MOVE 落到设备上执行，必须先给 action 加**归属方路由**，否则服务端会把
设备该做的意图抢走并按自己的方式执行掉。

需要定的三件事（属产品/架构语义，**不擅自决定**）：
1. 哪些 action 归设备（route-folder？xml-render？export？push-feishu？）
2. 服务端 `processScopedIntents` 如何跳过不属于自己的 action
   （`ClaimActionIntents` 加过滤条件，还是 claim 后判归属再放回）
3. 设备不可达时意图的滞留与重试语义（现有注释已写明
   「pending→failed/skipped 都是终态；**本期没有重试退避**」，
   设备侧会把这个终态假设彻底打破）

---

## 3. 需求 2：代码链路完整，只差流水线没排到

不需要授权「补实现」，实现已经在了：

- `intent_executor.go:52` `executeRouteFolder` 做**真实 IMAP UID MOVE**
  （服务器不支持 MOVE 扩展时 go-imap 自动回退 COPY+\Deleted+EXPUNGE）
- `main.go:500` 已把 `emailFetcher` 作为 `mover` 注入
- `mover` 为 nil 时才退化为 `ErrSkipIntent`（留行可观测）

**它没在真实数据上跑过的原因**：定时流水线排在每天 08:00
（日志：`pipeline scheduled at 2026-10-03T08:00:00+08:00`），
且 `email_folders` 表**至今 0 行**——目录子系统从未在真实数据上执行过。

**若要现在验证**：需手动触发一次流水线。这会**真实移动邮件**（改变真实邮箱状态），
属需单独授权的动作，不要在「重启服务」这类授权下顺带做掉。

---

## 4. 环境事实（省得下一轮重新查一遍）

### 4.1 主密钥在哪、怎么确认是对的

盘上四把 32 字节 key，**只有一把能解开真实库那 5 个账户的 `credential_encrypted`**：

| 路径 | 结果 |
|---|---|
| `C:\workspace\openpocket\data\email_master.key` | **5/5 解密成功** |
| `C:\workspace\openpocket\backend\data\email_master.key` | 5/5 `cipher: message authentication failed` |
| `C:\workspace\openpocket\.scratch-sttdev\data\email_master.key` | 5/5 失败 |
| `C:\workspace\openpocket-wt-maildeploy\backend\data\email_master.key` | 5/5 失败 |

**判据不是「文件存在」，是「能否解开真实库里那 5 个账户」**。
复核命令：`go test -count=1 -run TestDiagCredentialHealthRealDB ./internal/email/ -v`，
需设 `POCKET_REAL_MAIL_DSN` / `POCKET_REAL_MAIL_SCHEMA` / `POCKET_REAL_DATA_DIR`。
三负控全红、唯一正控通过——这个判据有承重能力，别退化成「文件在就算对」。

注入方式（`EnsureMasterKey` 认 base64 或 32 原始字节，`crypto.go:69`）：

```powershell
$env:POCKET_EMAIL_MASTER_KEY = [Convert]::ToBase64String(
  [IO.File]::ReadAllBytes('C:\workspace\openpocket\data\email_master.key'))
```

### 4.2 端口 18099 的归属（本轮结束时）

跑着的是 **PID 75084 / 启动 19:03:31 / `backend\.verify-bin2\pocketd.exe`**，
属**并发会话**。它的 env 不可读（父进程已退出，WMI 取不到）。

**在本机有并发会话时，端口是最容易被抢的资源**：起服务前先查
`Get-NetTCPConnection -State Listen` 确认端口空闲，起完再查 `OwningProcess`。

### 4.3 本轮停掉的进程

- PID 66160（`.scratch-sttdev`，端口 18111）：**我停的**。依据是
  `pg_stat_activity` 证明它 18:18 之后无任何新连接，是个摆设。
- PID 78488 / 81664：我自己起的，已随实验结束退出。
- PID 45872（`.verify-bin`，端口 18099）：**不是我停的**，19:06 还在、19:10 消失，
  Windows 事件日志无崩溃记录（= 干净退出）。时间上与 75084 启动吻合，
  但**没有直接证据**，记为「疑似并发会话所停，不确定」。

---

## 5. 仍未验证 / 未实现

- **需求 6 拆分**：一行未实现。归属路由见 §2。
- **新增「核验」列的真机与飞书呈现**：飞书 chat_id 未配置，真机未跑。
- **发票页显示值未在设备上确认**（`¥61,500 → ¥3,500` 只在库与单测层面验过）。
- **需求 2 的真实 MOVE**：代码完整，未在真实数据上执行过（见 §3）。
- **Docker daemon 未运行** → Greenmail 跑不了 → 真实 BODYSTRUCTURE 未验。
- 按「暂按不动」处理中：LLM 兜底接 Scheduler / 需求 4 推送限流 / 分类失败退避 /
  未来日期清理 / 阿里云白名单 / `feikemanager1@163.com` 的真实 IMAP 登录。
