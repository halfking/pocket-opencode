# round28 — 修掉 08:00 多实例竞态：每日定时流水线的跨进程互斥锁

日期：2026-10-03 02:0x — 02:3x
上一轮：round27（`a4692a94`）把竞态查实并定量，**但没修**。本轮修掉。

---

## §1 一句话

round27 定位的重复推送根因已修：email 每日定时流水线的"认领"此前只在**进程内**
做（`emailPipelineMu` / `sync.Once`），共享同一个 PG 的多个 pocketd 各自都会跑一轮。
现给 `RunEmailPipeline` 加了 PostgreSQL 会话级 advisory lock，**只作用于定时触发**，
手工触发不受影响。默认开启，`POCKET_EMAIL_PIPELINE_ADVISORY_LOCK=false` 可关。

**但 08:00 的止血仍需人工决定**——见 §7。代码修的是"以后"，
18077 / 18100 上跑着的旧二进制不会因为这次提交而改变行为。

⚠ **别把"多实例"本身当缺陷**。scheduledtask 的 dispatcher 同样是每进程一份
tick 循环，但它在**数据库内**做租约式认领（`ClaimDue` 的
`FOR UPDATE SKIP LOCKED` + 立即改期），实测多进程并发 scan 从不重复认领（§6）。
**"认领只在进程内做"才是缺陷**——这正是本轮这把锁补的那一层。

---

## §2 为什么之前会重复推送（round27 的结论，本轮未重算）

三个 pocketd（18099 / 18077 / 18100）共享 `opencode_pocket` schema，
各自在**本进程内**排了同一点的每日流水线。**认领这一步只发生在进程内**：

- `emailPipelineMu`（`server_email_pipeline.go:274`）—— 进程内
- `sync.Once`（`scheduler.go:208`）—— 进程内
- `time.After` 调度循环 —— 每进程一份

对比：scheduledtask 把同样的认领做在了数据库里，所以它没这个问题（§6）。
伤害不是"慢一点"：

- `MarkEmailsNotified` 写在**整个推送循环跑完之后**（`pipeline.go:1036` 循环 / `:1044` 标记）
- `notifycenter.InsertNotification` 是裸 INSERT，唯一约束只有主键

⇒ 24 行基线变 129 行（24 + 35×3；`pending_high=35` 见 §7.1 的重算）。
按 24 算的初稿数字（126 / 102）已作废。

---

## §3 实现

### 3.1 锁本身 —— `internal/email/pipeline_lock.go`（新增）

`Store.TryLockDailyPipeline(ctx) (release, state, err)`：

| state | 含义 | 调用方应当 |
|---|---|---|
| `Acquired` | 拿到锁 | 跑流水线，结束后 `defer release()` |
| `Busy` | **别的 pocketd 正在跑这一轮** | **跳过**（这就是修复意图） |
| `Unavailable` | 没有连接池 / 取连接失败 / 查询报错 | **降级照跑** |

**三态而不是两态是刻意的。** `Busy` 与 `Unavailable` 对调用方的含义完全相反，
合成一个 bool 就会出现：一次数据库抖动 → 打印"跳过" → 每日流水线**永久静默**，
而且日志里没有任何线索指向真实原因。

### 3.2 为什么用 `pg_try_advisory_lock`（会话级）而不是 `pg_advisory_xact_lock`（事务级）

一轮流水线最长 30 分钟。事务级锁要开一个 30 分钟的事务把连接钉住，
而流水线自身正用**同一个连接池**跑几十条查询——有把池子耗尽、把自己死锁的风险。
会话级锁只占**一条**独占连接（`pool.Acquire`），且用 Try 语义：取不到立刻返回，不排队。

### 3.3 最关键的一处：解锁失败必须销毁连接

```go
raw := conn.Hijack()
_ = raw.Close(context.Background())
```

advisory lock 是**会话级**的。若带着锁把连接 `Release` 回池子，下一个借用者
（可能是几小时后的另一轮流水线）会继承这把锁 ⇒ 每日流水线**永久死锁**，
且没有任何错误日志指向原因。所以解锁失败时宁可销毁连接，
让 PG 端随会话结束自动释放锁。

### 3.4 接线 —— `internal/server/server_email_pipeline.go:256-283`

锁加在 `RunEmailPipeline`（scheduler 定时入口）**而不是** `runEmailPipeline`：

- 定时路径（每日 8 点）会从多实例同时进来 → 需要锁
- 手工路径（`handleEmailPipelineRun` → `runEmailPipeline`）是用户显式要求 → 不该被挡

`TestManualPathIgnoresTheLock` 钉住这条边界：有人"顺手"把锁挪进
`runEmailPipeline` 会转红。

### 3.5 配置 —— `POCKET_EMAIL_PIPELINE_ADVISORY_LOCK`（默认 **true**）

```
internal/config/config.go:130   EmailPipelineAdvisoryLock bool
internal/config/config.go:316   getEnv("POCKET_EMAIL_PIPELINE_ADVISORY_LOCK", "true") == "true"
```

逃生门用途：无 PG 的纯本地部署、手工重跑定时轮次。

⚠ **解析规则的风险（未擅自改动，如实记录）**：与 `POCKET_EMAIL_SPAM_DRYRUN`
同一套 `getEnv` 语义——**只有精确的 `"true"` 才开**，其余一切值都关。
由于默认值是 true（安全方向），任何拼错的取值（`"TRUE"`、`"1"`、`" yes"`、
`" true "`）都会**关掉**多实例保护，而代码路径不变、其它测试全绿。
这是既有惯例，改它属于产品语义决定，留给拍板。

---

## §4 判据：11 条，全部有负控

### 4.1 `internal/email/pipeline_lock_test.go`（7 条，真 PG）

| 用例 | 守什么 |
|---|---|
| `SecondAcquireIsBusy` | 同 pool 两条连接互斥（否则锁退化成进程内 map） |
| `ReleaseMakesItReacquirable` | 释放后能再拿（否则一轮跑完锁死当天） |
| `ReleaseDoesNotLeakIntoPool` | **带锁连接不得归还池子**（唯一会造成永久故障的写法） |
| `IndependentPoolsAreMutuallyExclusive` | 跨 pool 互斥（= 两个进程） |
| `NoPoolIsUnavailableNotBusy` | 无连接池是 `Unavailable` 不是 `Busy` |
| `StateStringIsDistinct` | 三个 state 的日志文案互不相同 |
| `TestHarnessIsActuallyIsolated` | **判据自检**：schema 必须是隔离的，不是 public |

### 4.2 `internal/server/server_email_pipeline_lock_test.go`（4 条，真 PG）

`SkipsWhenAnotherInstanceHoldsLock` / `LockUnavailableStillRuns` /
`AdvisoryLockDisabledDoesNotSkip` / `ManualPathIgnoresTheLock`

### 4.3 `internal/config/config_email_pipeline_lock_test.go`（4 条）

默认值 / 解析规则 / 逃生门 / `SchedulerAdvisoryLock` 死配置审计（§6）

### 4.4 负控实测

| 改法 | 结果 |
|---|---|
| `releaseDailyPipelineLock` 跳过 unlock 直接 `conn.Release()` | `ReleaseDoesNotLeakIntoPool` **转红**（其余 6 条仍绿） |
| `case DailyPipelineLockBusy` → `case Busy, Unavailable` | `LockUnavailableStillRuns` **转红**（其余 3 条仍绿） |

两次负控都只让**该转红的那一条**红，其余保持绿——判据是有牙齿的，
不是恰好被实现细节顺带满足。

### 4.5 真进程验收（2026-10-03 03:00 实跑）——这一节是护栏替代不了的

上面所有护栏都在**一个测试进程里用两个 `pgxpool`** 模拟多实例。那证明的是
PostgreSQL 侧的锁语义，但证不了三件事：

- 真实 `pipelineLoop` 真的走到 `RunEmailPipeline`
- `EmailPipelineAdvisoryLock` 默认 true 真的在 `Load()` 里生效
- 两个**进程**（不是两个连接池）真的抢同一把锁

任一件断了，15 条护栏都会全绿，而 08:00 照样重复推送。

**做法**：用 HEAD 构建 `pocketd-lock.exe`，起两个真实实例——**同一个隔离 schema**
`pocket_locktest_20261003`（不碰 `opencode_pocket`）、不同端口、都排到
`2026-10-03T03:00:00+08:00`。这是 08:00 三实例竞态的最小复现。
脚本 `.scratch-locktest/start-detached.ps1` + `check-lock.sql` / `hold-lock.sql`。

**结果**（同一刻 `03:00:00`）：

```
inst-a: [email/pipeline] 每日定时流水线跨进程锁已被其它实例持有，本轮跳过
inst-b: [email/pipeline] step 1/5 … step 5/5
        [email/pipeline] done synced=0 new=0 spam=0(+0 local) reminders=0 … errors=0
```

⇒ **只有一个执行，另一个跳过。** 三条附加判据也都过：

1. `pg_locks` 里该 advisory lock **0 行**（已干净释放）——泄漏的表现是
   明天起永久不再触发而日志一片正常。
2. 判据自身双向验证：先用一个持锁 45 秒的会话证明 `check-lock.sql`
   **看得见**被持有的锁（pid 32036，classid 1903246281 / objid 2785338973，
   与 Go 诊断读到的完全一致），释放后回到 0 行。否则那个「0 行」
   分不清是「干净释放」还是「判据看不见锁」。
3. **两个实例都重新排到了 `2026-10-04T03:00:00+08:00`**——被跳过的那一个也排了。
   若实现让跳过者 `return` 出 `pipelineLoop`，明天就只剩一个实例武装，
   等于用另一种方式悄悄坏掉。这条不看日志会漏。

### 4.6 搭这个真实环境时踩的两个坑（都属于「静默失效」型）

1. **master key 必须精确 32 字节**（`email/crypto.go:22-24`）。第一次给了 45 字符：
   `NewCrypto` 报错 → `emailCrypto` 为 nil → `main.go:439` 的 else 分支不进 →
   **整个 email 块（fetcher/scheduler/SetPipelineRunner）被跳过**。进程照常启动、
   `/healthz` 照常 200，**日志里连一行 WARN 都没有**。我一度以为是接线问题，
   去查 `main.go` 的 `SetPipelineRunner`。真正的判据是日志里必须出现子系统
   自己的启动行：`Email scheduler started (...)` +
   `[email/scheduler] daily pipeline runner injected (hour=N)` +
   `[email/scheduler] pipeline scheduled at <ISO>`。三行缺一 ⇒ 子系统没起来。
2. **长跑实例不能用 `& exe | Tee-Object`**。工具/会话超时杀掉宿主 shell 时
   管道一起死、子进程变孤儿、日志停在 14 行——我差点把「日志里没有
   pipeline scheduled」误读成「scheduler 没启动」。真相是日志管道断了、
   实例其实在服务。必须 `Start-Process -RedirectStandardOutput/-RedirectStandardError`。
   孤儿判据：进程还在 + 端口在 listen + 日志不再增长 ⇒ 是日志坏了不是程序坏了。

（顺带：PowerShell 的 `param(...)` 必须是 .ps1 的**第一条语句**，放在
`$ErrorActionPreference` 之后会被当普通命令执行。）

---

## §5 本轮判据自己坏掉的两次（都记下来，因为方法比结论更耐用）

### 5.1 「探针问锁还在不在」被 PG 的可重入语义打败

第一版 `ReleaseDoesNotLeakIntoPool` 的探针是：release 之后从池里取一条连接，
`pg_try_advisory_lock` 看能不能拿到。**负控下全绿。**

`diag_advisory_reentrant_test.go`（新增，门控 `POCKET_DIAG_ADVISORY_REENTRANT=1`）实测：

```
SAME SESSION: first=true second=true        ← 同会话连 lock 两次都成功
after ONE unlock, re-lock=true               ← unlock 一次后第三次仍成功
OTHER SESSION while conn still holds: got=true  ← 归还后再 Acquire 拿回同一会话
pg_locks matching rows = 1 (lock 3 次 / unlock 1 次)
```

PG 的**会话级** advisory lock 是**可重入**的：同会话重复 `lock()` 成功且计数 +1。
于是「持锁连接已归还」时，探针从池里拿回**同一条物理连接**、同会话再 lock 自然成功，
"有没有泄漏"这个问题它**答不了**。

`pg_locks` 是唯一能穿透的判据：**行数不随可重入计数增长**
（lock 3 次 / unlock 1 次仍是 1 行），所以「0 行」≡「没有任何会话持有它」。
key 编码：`hashtextextended` 返回 bigint，PG 拆成 `classid`(高 32) / `objid`(低 32)：

```sql
WHERE locktype='advisory' AND granted
  AND classid = ((hashtextextended($1,0) >> 32) & 4294967295)
  AND objid   =  (hashtextextended($1,0) &  4294967295)
  AND objsubid = 1
```

**通用教训**：验一个带"已持有"状态的资源，不能用"再要一次看给不给"当判据——
先问它是不是可重入。要问系统表（`pg_locks` / `lsof` / OS 句柄表）。

### 5.2 判据抓到了**我自己实现里的真 bug**

`TestRunEmailPipeline_LockUnavailableStillRuns` 第一次跑直接 panic：

```
panic: nil pointer dereference
  server.(*Server).RunEmailPipeline  server_email_pipeline.go:274
```

我第一版接线写的是 `switch { case err != nil: ...; case state == Busy: ...; default: defer release() }`。
`Unavailable` 时 `err == nil`（不是故障）且非 Busy ⇒ 落进 `default` ⇒
`release` 是 **nil** ⇒ `defer release()` 在函数返回时 panic。

**这个 goroutine 是 scheduler 的**，Go 里未捕获的 panic 直接终止整个 pocketd 进程。
即"无 PG 的部署会在 08:00 崩掉整个后端"。

改成按 `state` 分派（`case Acquired: defer release()` / `default: 降级照跑`）后修复。
这正是"nil 依赖类缺陷要靠真实进程路径的判据才看得见"的又一次实例。

### 5.3 config 判据的两处自我误判

- 第一版把期望写成"只有精确 false 才关"（我发明的规则），实际实现是"只有精确 true 才开"。
  报错的 6 个 case 全是**我的期望**错，不是代码错。已改为锁住真实契约 + 注释写明风险（§3.5）。
- 死配置审计文件自己包含 `SchedulerAdvisoryLock` 这个标识符 ⇒ 自己扫到自己 ⇒ 永远判红。
  已按文件名跳过自身。

---

## §6 `POCKET_SCHEDULER_ADVISORY_LOCK` 是死配置 —— 但**不是**因为 scheduledtask 缺锁

`config.go:195` 声明、`config.go:340` 赋值，**全仓零消费方**
（`Select-String` 全 `.go/.mjs/.md/.ts/.sql` 扫描，命中仅这两行）。
这部分是事实。

### 6.1 【更正】本文档初稿写下的"scheduledtask 同样没有跨进程锁"是**错的**

初稿从两件事推出结论：① 那个开关没人用；② `store.go:329-332` 的注释写着
"callers should also wrap the scheduler tick in a single pg_advisory_lock"。
**这是推断，不是实测。** 而且推错了。

实测（`internal/scheduledtask/diag_claimdue_race_test.go`，门控
`POCKET_DIAG_CLAIM_RACE=1`）：两个**独立 pool**（= 两个进程）同时 `ClaimDue`，
只造 1 条此刻到期的任务。

```
instance A claimed: [t-race-1]  window=2202us
instance B claimed: []          window=5485us
the two ClaimDue windows DID overlap — concurrency was real
```

**20 次 + 5 次重跑全部一致：从不重叠。**

⇒ **scheduledtask 并不缺跨进程保护。** `ClaimDue` 的
`FOR UPDATE SKIP LOCKED` + 在同一条 `UPDATE ... RETURNING` 里立刻把
`next_run_at` 推到 `$1 + GREATEST(300, timeout_sec + 60)`，本身就是**租约式**的
跨进程认领：第二个实例的候选集里那行已经被改期，不再满足 `next_run_at <= $1`。

### 6.2 判据里那句关键的自我设防

第一版这个诊断**没有**记录两个 `ClaimDue` 的执行窗口，只看"有没有重复"。
那样的话，"从不重复"可能仅仅是因为它们**恰好串行执行**了（pool 懒建连接，
第一个调用建连接的几毫秒里第二个还在建）——它证明的会是"串行时不重复"，
不是"并发时不重复"。

补上窗口断言后每次都打印 `the two ClaimDue windows DID overlap`，
并发是真的。若哪天这个断言不成立，用例会 `t.Fatalf` 而不是给出一个假安全结论。

### 6.3 两个调度器的对照：同样要防多实例，答案却相反

| | email 每日流水线 | scheduledtask dispatcher |
|---|---|---|
| 认领发生在哪里 | **进程内**（`emailPipelineMu`、`sync.Once`） | **数据库内**（`UPDATE...RETURNING` + `SKIP LOCKED`） |
| 多实例下会重复吗 | **会**（实测 24 行 → 最多 126 行） | **不会**（实测 20+5 次零重叠） |
| 修法 | 加会话级 advisory lock | 不需要 |

所以"多实例"本身不是缺陷，**"认领只在进程内做"才是**。
这也从反面印证了本轮 email 那把锁是加在正确位置的：
它补的正是 scheduledtask 早就有的那一层（数据库级认领）。

### 6.4 那个死配置怎么处理

**本轮没有动它。** 它现在的状态是"配置存在但无人消费"，危害仅为
误导下一个人以为 scheduledtask 需要加锁（我本轮就被它误导过，见 6.1）。
两条可选出路，都属产品/配置语义决定，留给拍板：

- 删掉 `config.go:195` + `:340`（承认它不需要）
- 保留并在注释里写明"scheduledtask 用租约式认领，不需要这把锁"

`TestSchedulerAdvisoryLock_IsStillUnconsumed`（`internal/config`）把
"它当前确实是死的"记成一个会主动失败的事实——哪天有人真接上了，
用例会转红提醒更新本文档。

---

## §7.4 【新发现·比 58000 更该修】需求 2 台账静默漏掉两封真发票

2026-10-03 03:1x 只读实测（`logs/zz-invoice-ledger-20261003.{sql,txt}`）：

`email_invoices` 现有 4 行，合计口径护栏是对的
（`all_rows=4 / sum_all=61,954.50` 与 `counts_toward_total=1 / sum_counted=3,500.00`
——58000 那行 `status=new` 被正确排除，**没污染金额**）。

但 `M4` 段 LEFT JOIN 揭出漏报：

| 邮件 | 收到 | 金额 | `email_invoices` 里 |
|---|---|---:|---|
| 通行费电子发票（浙AB59453） | 2026-09-14 13:44 | **19.00** | **无对应行** |
| 通行费电子发票（浙AB59453） | 2026-09-14 13:41 | **5.61** | **无对应行** |

⇒ **需求 2 的台账漏了 24.61 元真发票**，且是静默的。

### 7.4.1 根因（已实测验证，不是推断）

`diag_amount_gap_test.go`（纯正则、无需数据库）实跑 `reAmountTotal`
（`invoice.go:101`）：

```
MISS  发票金额共计19元          ← 真实写法
MISS  票根成功开具了1张发票，发票金额共计5.61元。
HIT   金额：19.00               （对照组）
HIT   价税合计（小写）¥1280.00  （对照组）
MISS  信用额度 58,000.00
```

正则结构是「标签 + `[:：\s]*` + 币种 + 数字」，标签与数字之间**只**允许冒号、
空白和「小写」。而票根的写法是「发票金额**共计**19元」——「共计」不在允许的
分隔符集合里 ⇒ `Amount=0`；又无发票号、无附件凭证 ⇒ 命中 `invoice.go:617`
的丢弃门槛 `Amount == 0 && InvoiceNo == "" && !hasInvoiceAttachment` ⇒ 不建档。

（`信用额度 58,000.00` 也是 MISS，说明 58000 那行**不是**走 `reAmountTotal`，
而是走了「取全文最大值」兜底——与 round19 的记录一致。）

### 7.4.2 为什么本轮不直接改

1. **这是误报/漏报方向相反的两个问题，成因不同**。58000 是**误建档**
   （对账单通过了准入门 + 金额兜底取全文最大值）；通行费是**漏建档**
   （金额正则太严）。只修 58000 不解决漏报，反之亦然。
2. **改 `reAmountTotal` 是产品决定，且有反向风险**：放宽分隔符会不会让
   对账单的「合计人民币(本位币)12,838.93」也命中？——那会把一个一眼荒谬的
   5.8 万换成一个貌似合理的 1.28 万，而它其实是「应还款额」，**仍然不是发票金额**。
   round21 的注释已就同一点给出警告：「错误数字改对了一点，比错误数字更危险」。
3. **时敏**：08:00 今晚会跑流水线，此时改发票提取路径等于在验收前动被测对象。
   即便要改，也应在 08:00 之后单独一轮做，且必须带负控。

### 7.4.3 可选出路（供拍板）

| 方案 | 效果 | 风险 |
|---|---|---|
| A 放宽 `reAmountTotal` 分隔符（加「共计/总共/合计」） | 通行费 24.61 进台账 | 可能让对账单命中「合计人民币…」；需配套负控 |
| B 保持不动，靠附件下载后再建档 | 无 | 票根的发票在附件里，现路径拿不到 → 仍然漏 |
| C 单独加一条「电子发票 + 金额标签 + 无对账单词」的旁路 | 精确命中票根类 | 新增一条判定路径，本身要负控 |

---

## §7.5 【更正·比外观问题严重】脏摘要是 **19** 条，且有 3 条写在修复落地**之后**

round26 的结论是「18 封是历史脏数据，新摘要干净了 ⇒ 活 bug 排除」。
本轮用只读 SQL 复核，**这条定性需要收窄**。

### 7.5.1 判据与自证

判据用 `diag_qp_replay_test.go` 的 `qpThreshold=20`（`regexp_matches(snippet,
'=[0-9A-Fa-f]{2}','g')` 计数，与 Go 版 `qpHits` 同义）。
**自证**：阈 10/15/20 都给出 19，阈 25 才降到 16 ⇒ 不是刀锋上的数字。

`logs/zz-qp-dirty-20261003.txt` 给出 19，**不是 18**。差值已解释
（`logs/zz-qp-timeline-20261003.txt` M3）：

```
dirty_before_1003 = 18      dirty_on_1003 = 1
oldest_dirty = 2026-09-15 00:23:47     newest_dirty = 2026-10-03 02:00:23
```

⇒ 记录里的 18 是**今天 00:00 之前**取的数；02:00 又新到一封。判据有效。

### 7.5.2 决定性判据：`created_at` vs `date`（`logs/zz-qp-created-20261003.txt` M2）

`date` 是邮件头的 Date，不是入库时刻。单看「最新脏行是今天」会误判。

| 插入时刻（created_at） | 条数 | 入库滞后 | 说明 |
|---|---:|---|---|
| 2026-10-01 23:56:42–52 | **16** | 84–407 小时 | **一个 10 秒内的批量回填** |
| 2026-10-02 22:21:31 | 2 | 0.2 小时 | 实时插入 |
| 2026-10-03 02:09:00 | 1 | 0.1 小时 | 实时插入（最新） |

⇒ **约 84% 是一次回填批处理的产物**，但**有 3 条是实时插入**。

### 7.5.3 关键对照（`...-created...txt` M4）

```
插入小时        行数  脏
2026-10-03 02      1   1     ← 实时插入，脏
2026-10-03 01      4   0
2026-10-03 00     45   0     ← 一次回填，45 行全干净
2026-10-01 23    120  16     ← 另一次回填，16 行脏
```

**10-01 23:56 那次回填脏、10-03 00:00 那次回填干净**，形状相同、结果不同。
所以「脏」不是「回填」这个动作本身决定的。

### 7.5.4 账户分布（`logs/zz-qp-protocol-20261003.txt` M2/M3）

| 账户 | 总数 | 脏 | 脏率 | uid 形态 |
|---|---:|---:|---:|---|
| acct-…-2 | 98 | 4 | 4.1% | IMAP（1~10459） |
| acct-…-5 | 58 | 4 | 6.9% | POP3（1.3e9） |
| acct-…-3 | 14 | **11** | **78.6%** | POP3（1.7e9） |
| acct-…-1 | 10 | 0 | 0% | IMAP（2~11） |

按 uid 形态：POP3 20.8%（72 行 15 脏）vs IMAP 5.5%（108 行 4 脏）。
`acct-…-3`（OpenAI 验证码/登录码）几乎全是脏的。
但 `snippet_test.go:221-222` 钉住 fetcher.go + backfill.go 共有 3 个
`DeriveSnippet` 调用点，其中一个就是「POP3 HTML 回退」——
**POP3 与 IMAP 走的是同一条摘要管线**，所以形态差异不能直接当因果。

### 7.5.5 那 3 条实时脏行是谁写的 —— 查到了关键排除，但**留下一个真张力**

`SnippetFromParsed` 是 `07910a32`（**2026-10-02 21:46**）引入的
（`git log -S 'em.Snippet = SnippetFromParsed(parsed, 500)'`）。
而 3 条实时脏行插入于 10-02 22:21 与 10-03 02:09，**在它之后**。

#### (1) 三个候选实例，二进制符号检查（`go tool nm`）

Go 二进制保留函数符号，可以直接问「这个 exe 里有没有这段代码」：

| 实例 | 二进制构建时间 | `SnippetFromParsed` 符号 |
|---|---|---|
| 18099 | 10-03 01:32 | **有** |
| 18077 | 10-02 23:42 | **有** |
| 18100 | 10-02 21:03 | **无**（比 `07910a32` 早 43 分钟） |

⇒ 18100 跑的是修复前的二进制。（启动日志里的
`Loaded version config: v1.2.0 build 2` 是**应用版本**，不是 git sha，
回答不了这个问题；符号检查才是有效的判据。）

#### (2) 但 18100 也被排除——它根本解不开那个账户的凭据

`zz-account-map-20261003.txt` M3：`acct-…-5` = **`kimmy.huang@163.com`**。
而 18100 的日志对 `acct-1790870162079171800-5 (kimmy.huang@163.com)` 反复报
`decrypt credential:` 失败 ⇒ **它从未成功同步过该账户**，不可能写这行。

#### (3) 时间对上了：只有 18099 在 02:09:00 动了这个账户

```
18099: 02:08:59 feikemanager1@163.com / feikemanager@163.com / 02:09:00 kimmy.huang@163.com sync trace total 487ms
18077: kimmy.huang 最近两次是 01:38:14 与 02:24:14 —— 不是 02:09
```

而脏行的 `created_at` 正是 **2026-10-03 02:09:00**。
**写它的是含 `SnippetFromParsed` 的 18099。**

#### (4) 但重放同一封邮件，当前代码是干净的 —— 【张力，未解】

缓存里有那封邮件的原文：`em-1298896151-acct-1790870162079171800-5.bin`
（58,637 字节，mtime 03:03:58）。用 `diag_qp_replay_test.go` 重放：

```
clean  em-1298896151-…-5.bin  fmt=0x01 plain=43941B  html=791c(qp=0) text=0c(qp=0) -> 791c(qp=0)
```

**同一封邮件、同一账户：库里 02:09 写入的行 qp=89，重放出来 qp=0。**

补充一条已验证的机制事实（`diag_pop3_html_fallback_test.go`，纯函数）：
**`SnippetFromParsed` 自己不做 QP 解码，它信任上游解析器**——喂进带 `=XX` 的
`HTMLBody`，它原样透传（qpHits=12，业务不可读）；而 `DeriveSnippet` 喂
**原始 MIME 字节**时解码正确（`中文春报`）。同时该测试**排除了**我原本的
假设：`fetcher.go:958-962` 的 POP3 回落分支在这个输入下**不会执行**
（主路径返回非空），所以脏行不是从回落分支来的。
而 `ParseMIMEMessage`（`mime.go:450/502`）在赋给 `HTMLBody` 前确实调了
`decodePartBody(part, …, Content-Transfer-Encoding)`。

#### (5) 因此仍未解的分叉

| 解释 | 需要什么才能判别 |
|---|---|
| (a) 02:09 那次 fetch 拿到的**字节与缓存里的不同**（缓存是 03:03:58 落的，晚了 55 分钟） | 复现一次同 UID 的 fetch，比对两次字节 |
| (b) 同一批字节下，02:09 走的**代码路径**与重放不同 | 需要在写库处打点（改动生产代码） |
| (c) 传输编码头不可信/缺失（头说 8bit、实际是 QP），`decodePartBody` 放行 | 拿到 02:09 那批原始字节看 `Content-Transfer-Encoding` |

**本轮不猜。** 三条都各自需要一次不同的取证，其中 (b)(c) 要改生产代码。
这是 08:00 之后单独一轮的活。

### 7.5.6 因此，待拍板项的定性


round26 把它记作「存量脏数据、修不修是外观问题」——**这个定性要收窄**：
现在有证据表明至少 3 行写在修复落地之后。需求 7 是「在邮件的窗口中可以查看
收到的各类邮件」，摘要乱码直接影响这一条的可读性。

§7.6 的修复方案（离线 QP 重算）**仍需写库授权**，但优先级应从「可选清理」
上调到「先判别 §7.5.5，再决定是只清存量还是还要改代码」。

---

## §7 仍然需要人工决定的事

### 7.0 08:00 会**推什么**进去（2026-10-03 03:0x 只读实测，规则可复算）

`splitReminderCandidates`（`pipeline.go:944-963`）只排除三样：
`notified_at > 0`、`category='spam'`、`importance != 'high'`。
它**不**排除测试产物、CI 失败潮、过期验证码。而
`importantReminderLookbackDays = 90`（`pipeline.go:986`），最老的候选才 25 天 ⇒
**35 封全部会进**。

分类规则写死在 `logs/zz-8am-junk-breakdown-20261003.sql` 里（可审阅、可复算）：

| 桶 | 条数 | 判据 |
|---|---:|---|
| A 测试产物 | **2** | 主题含 `[urgent-e2e]`，发件人 `56551681@qq.com`（用户自己的 qq 邮箱） |
| B CI 失败潮 | **14** | `notifications@github.com` + 主题 `Run failed` |
| C 过期验证码 | **4** | 主题含「验证码」或 `login code`（openai×3 + bigmodel×1） |
| D 其余（真实/待判断） | **15** | AWS 2、阿里云额度 1、工行对账单 1、github runner 1、cursor 登录 1、syapi 额度 3、小米合规 1、大地保单 1、华为 2、openai 新登录 2 |

**⇒ 20/35（57%）按显式规则就是不该推的。** 而这是需求 4 的**首次真实定时执行**，
它的第一印象就是这 35 条。

`pipeline.go:983-985` 的注释其实早已预见了这件事（「首次上线时会把积压的老 high
一次性全部推送出来……是产品取舍，已单列待拍板，不在本轮擅自决定」）。本节把
那个取舍从「约 30 封」更新成带规则的**确切 35 封**。

### 7.0.1 因此，08:00 前后可行动的三条（都不含写操作）

| 动作 | 08:00 后 notifications 期望值 | 需不需要写库授权 |
|---|---:|---|
| 什么都不做（修复生效、只跑 1 实例） | **59** | 否 |
| 先清掉 A 桶 2 封测试产物 | **57** | **是**（软删 2 行） |
| 只跑 1 实例但 18077/18100 未停 | **129** | 否（但见 §7.1） |

B 桶 14 条 CI 失败潮**不是**一条一封的清理能解决的——它需要一条规则
（按发件人或 `Run failed` 前缀降级 importance，或给 CI 类邮件单独一类），
属产品语义，不在本轮范围。

（A 桶 2 封与上一轮独立统计的「16 封测试产物中仅 2 封 importance=high」一致，
两轮独立复核相符。）

### 7.1 08:00 的止血（最紧急）

代码修的是"以后"。**18077 / 18100 上跑着的旧二进制不会因此改变行为**，
它们仍会在 08:00 各跑一轮。

零代码止血只有一条路：**在 08:00 前停掉 18077 和 18100**。
它们是并发会话的验证环境，我不能擅自停。

**【本节数字已重算·2026-10-03 02:56】** 本文初稿写的「24 行 → 最多 126 行
（最多 102 条重复推送）」**两个数都是错的**，来源是过期的 `pending_high=24`
基线，以及一处算术错误。重算证据：`logs/zz-8am-pending-take4-20261003.{sql,txt}`
（psql exit=0 / stderr 空，只读，全查询显式限定 `opencode_pocket.`）：

```
M0  notified_at / deleted_at : bigint, column_default=0, is_nullable=NO   ← 0 才是"空"
M2  total_emails=180, high_total=59
M3  notifications_now = 24
M4  notified_at 分布：0 → 156 封（其中 high 35 封）
                    1790890713 (2026-10-02 05:38:33) → 24 封（全部 high）
M5  pending_high = 35
```

正确的算术：

| 情形 | notifications 总行数 |
|---|---|
| 现在 | 24 |
| 08:00 只跑 1 个实例（修复后应有结果） | 24 + 35 = **59** |
| 08:00 跑 3 个实例（未修复） | 24 + 35×3 = **129** |
| ⇒ **重复推送多出来的行** | **70** |

08:00 之后唯一该断言的数字是 **59**；若是 129，说明修复没生效（或旧实例未停）。
（并发会话在 §26 独立算出了同样的 35 / 59，证据文件
`logs/zz-8am-pending-take3-20261003.*`；两轮独立复核一致。）

### 7.2 推送

本轮 + 上一轮共 8 个提交在本地，**未推**。`origin/main` 持续前进
（本轮开始时 behind 7 / ahead 13），需先 merge 再推。合并路径此前验证过零冲突。

### 7.3 其余待拍板（沿用上一轮清单，无变化）

- 34 条积压提醒的处置
- 汇总文档保留策略（95% 重复、无当前版本标记）
- 清理 16 封测试产物（需写库授权）
- `POCKET_EMAIL_CLASSIFY_VIA_GATEWAY=true` 是否写进 `.env`（会产生真实 LLM 费用）
- 飞书凭据四项（需求 3 主交付物）
- 18 条存量乱码摘要修复（需写库授权）
- 58000 库存行（工行对账单被当发票）
- 真机屏幕级验收（设备被并发会话占用）

---

## §8 回归

| 包 | 结果 |
|---|---|
| `go build ./...` | EXIT=0 |
| `go vet` 三包 | EXIT=0 |
| `internal/email`（带真 PG DSN） | ok |
| `internal/server` | ok |
| `internal/config` | ok |
| `gofmt -l`（新增 5 文件） | 干净 |

### 8.1 顺手修掉的一个回归：上一轮的提交让 `internal/server` 变红了

`TestPGTestsNeverTargetTheProductionSchema` 判红，指向
`internal/email/diag_spam_preview_test.go`——**round27 提交 `4de72306` 时漏的**。
当时只跑了 `internal/email`（它自己绿），没跑 `internal/server`。

两个文件已登记进 `pgSafeWithoutIsolation`，理由逐条写明（`pg_test_isolation_guard_test.go:255-272`）：

- `diag_spam_preview_test.go`：只读（仅一条 SELECT），门控 `POCKET_DIAG_SPAM_PREVIEW=1`，
  隔离靠 SQL 里显式限定 `FROM opencode_pocket.emails`（`:77`）而非 search_path
- `pipeline_lock_test.go`：确实隔离（走 `newWorkspaceTestStore`），
  但文件里没有 `"*_test_` 字面量（schema 名由 helper 现场生成），
  另有自检用例断言 `current_schema()` 非 public

**教训**：上一轮"提交干净"的印象是假的——只跑了被改动所在的那个包。
新增测试文件可能让**别的包**的架构守卫转红。

### 8.2 行尾

本仓库既有 Go 文件是纯 CRLF（`server_email_pipeline.go` / `config.go` 修改后仍
`bareLF=0`，说明 edit 工具保住了）。本轮 5 个新文件写完后显式转 CRLF，
字节增量恰好等于行数（`pipeline_lock.go` 4964→5077，CRLF=113 bareLF=0）。

---

## §9 新增文件清单

| 文件 | 作用 |
|---|---|
| `internal/email/pipeline_lock.go` | 跨进程锁实现 + 三态语义 |
| `internal/email/pipeline_lock_test.go` | 7 条护栏（真 PG） |
| `internal/email/diag_advisory_reentrant_test.go` | 可重入语义诊断（门控 env） |
| `internal/scheduledtask/diag_claimdue_race_test.go` | 并发 ClaimDue 诊断（门控 env）—— §6 证伪用 |
| `internal/server/server_email_pipeline_lock_test.go` | 4 条接线护栏（真 PG） |
| `internal/config/config_email_pipeline_lock_test.go` | 4 条配置护栏 + 死配置审计 |

改动既有文件：`config.go`（+2 字段）、`server_email_pipeline.go`（接线）、
`pg_test_isolation_guard_test.go`（allowlist 登记）。
