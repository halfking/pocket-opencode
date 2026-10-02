# round28 — 修掉 08:00 多实例竞态：每日定时流水线的跨进程互斥锁

日期：2026-10-03 02:0x — 02:3x
上一轮：round27（`a4692a94`）把竞态查实并定量，**但没修**。本轮修掉。

---

## §1 一句话

round27 定位的重复推送根因（进程内互斥挡不住多进程）已修：给 `RunEmailPipeline`
加了 PostgreSQL 会话级 advisory lock，**只作用于定时触发**，手工触发不受影响。
默认开启，`POCKET_EMAIL_PIPELINE_ADVISORY_LOCK=false` 可关。

**但 08:00 的止血仍需人工决定**——见 §7。代码修的是"以后"，
18077 / 18100 上跑着的旧二进制不会因为这次提交而改变行为。

---

## §2 为什么之前会重复推送（round27 的结论，本轮未重算）

三个 pocketd（18099 / 18077 / 18100）共享 `opencode_pocket` schema，
各自在**本进程内**排了同一点的每日流水线：

- `emailPipelineMu`（`server_email_pipeline.go:274`）—— 进程内
- `sync.Once`（`scheduler.go:208`）—— 进程内
- `time.After` 调度循环 —— 每进程一份

三者都跨不了进程。伤害不是"慢一点"：

- `MarkEmailsNotified` 写在**整个推送循环跑完之后**（`pipeline.go:1036` 循环 / `:1044` 标记）
- `notifycenter.InsertNotification` 是裸 INSERT，唯一约束只有主键

⇒ 24 行基线最多变 **126 行**（24 封 × 3 实例 + 原有）。

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

## §6 顺带查实：`POCKET_SCHEDULER_ADVISORY_LOCK` 是死配置

`config.go:195` 声明、`config.go:340` 赋值，**全仓零消费方**
（`Select-String` 全 `.go/.mjs/.md/.ts/.sql` 扫描，命中仅这两行）。

也就是说 scheduledtask 的 dispatcher **同样没有跨进程锁**——
`store.go:329-332` 的注释自己写着"callers should also wrap the scheduler tick
in a single pg_advisory_lock"，但没人包。

**本轮没有改 scheduledtask**：那不是邮件需求，且 08:00 前不该动无关的调度器。
已加 `TestSchedulerAdvisoryLock_IsStillUnconsumed` 把"它当前确实是死的"
记成一个会主动失败的事实——哪天有人接上了（那是好事），用例会转红提醒更新本文档。

---

## §7 仍然需要人工决定的事

### 7.1 08:00 的止血（最紧急）

代码修的是"以后"。**18077 / 18100 上跑着的旧二进制不会因此改变行为**，
它们仍会在 08:00 各跑一轮 ⇒ 最多 102 条重复推送。

零代码止血只有一条路：**在 08:00 前停掉 18077 和 18100**。
它们是并发会话的验证环境，我不能擅自停。

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
| `internal/server/server_email_pipeline_lock_test.go` | 4 条接线护栏（真 PG） |
| `internal/config/config_email_pipeline_lock_test.go` | 4 条配置护栏 + 死配置审计 |

改动既有文件：`config.go`（+2 字段）、`server_email_pipeline.go`（接线）、
`pg_test_isolation_guard_test.go`（两条 allowlist）。
