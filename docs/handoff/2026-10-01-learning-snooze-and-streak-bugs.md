# 学习域 2 个红测：查出 2 个真缺陷 + 2 条写错的测试期望

> 日期：2026-10-01 10:35 起，北京时间
> 基线：`2026-10-01-backend-red-baseline.md` 第 1 节记录的 `internal/learning` 两条
> 涉及：`backend/internal/learning/store.go`、`store_pg_test.go`

## 先更正一个方法论错误

基线文档给的复现命令是**带 DSN** 的：

```
POCKET_TEST_POSTGRES_DSN=postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable \
  go test ./... -count=1 -skip TestDiag
```

我在排查提醒问题时跑的全量**没带这个变量**。于是 `TestActiveDayTimestamps`、
`TestReminderLifecycle`、`TestTaskWriteGuardBlocksPlainMemberPatch/Delete`
这 4 个是 **SKIP 而不是 PASS**——它们打的是真 PostgreSQL，没 DSN 直接跳过：

```
--- SKIP: TestActiveDayTimestamps
    store_pg_test.go:167: POCKET_TEST_POSTGRES_DSN not set; skipping learning integration test
```

我当时报的是「全仓零 FAIL」，字面为真，但比实际证据弱：它把「跳过」混进了「通过」。
**任何需要真 PG 的判定，都必须带 `POCKET_TEST_POSTGRES_DSN`。** 带 DSN 重跑，
4 个立刻全红。

## 三条性质不同的失败，不是一回事

`TestReminderLifecycle` 报三条，按性质拆开是**两回事**：

| 报错 | 性质 | 依据 |
|---|---|---|
| L547 `snoozed until …, want later than` | **产品缺陷** | 延后把提醒往回拉 |
| L555 `an acked reminder must never come due, got 1` | **测试期望写错** | 返回的是 `r-due`，不是被 ack 的 `r-once` |
| L562 `pending = 1, want 0` | **测试期望写错** | `r-due` 仍 `snoozed`，本就应计入 pending |

`TestActiveDayTimestamps` 的 1 条是**产品缺陷**（见下）。

### 缺陷一：SnoozeReminder 把提醒往回拉

`store.go` 原来用 `now + minutes*60` 覆盖 `next_due_at`。真库实测：

```
【命题1】snooze 前 next_due_at=1790908965（24 小时后）
【命题1】snooze 120 分钟后 next_due_at=1790829765（2 小时后）
【命题1】确认：延后 2 小时反而把提醒从 24 小时后提前到 2 小时后 —— 往回拉了
```

对一个每天 08:00 的提醒，用户说「晚点提醒我」，结果提前了 22 小时触发，且此后
`RuleValue` 与实际排期不再一致。函数自己的注释写着 "pushes a reminder into the
future"，测试注释写着 "pushes it further out"——两处都描述了本该有的行为。

**这个 bug 对「已过期」的提醒是隐形的**：那时 `now` 恰好是正确的基准，所以常见路径
看起来没问题，只有 `next_due_at` 在未来时才发作——这正是它活下来的原因。

修法：

```sql
snoozed_until = GREATEST(next_due_at, $1) + $2 * 60,
next_due_at   = GREATEST(next_due_at, $1) + $2 * 60
```

`GREATEST` 的下限不能省，见下面的负控。顺带把返回值改成 `RETURNING next_due_at`
读回，避免调用方与库里各说各话。

### 缺陷二：ActiveDayTimestamps 的 since 边界不逐时间戳生效

SQL 用 `(captured_at >= $3 OR updated_at >= $3)` 过滤**行**，Go 循环却把
`capturedAt` 和 `updatedAt` **无条件都 append**。于是「很久以前采集、刚刚更新」的
那一行会同时吐出那个久远的 `captured_at`。实测：

```
【命题2】since=now-30m(1790820765) → [1790818965 1790822565]（2 条，期望 1）
【命题2】确认：返回了 1790818965，早于 since 边界 1790820765（差 1800 秒）
```

`ActiveDayTimestamps` 是**连续天数（streak）的输入**。多吐一个窗口外的时间戳
不是「多返回一个数」，而是**给用户记上他没有活跃的那一天**——连续天数被虚增。
函数文档写的 "since sinceUnix" 从来没有真正兑现。

## 两条测试期望为什么是写错的（不是迁就实现）

原测试先 ack 掉 `r-future`，然后断言「到期队列为空」和「pending 为 0」。但
`r-due` 还在：它被 `MarkReminderSent` 置回 `pending`、又被 `SnoozeReminder` 置为
`snoozed`，而 `CountPendingReminders` 统计的正是 `state IN ('pending','snoozed')`。

实测确认 ack 本身是对的：

```
【命题3】ack 掉 r-once 后 CountPendingReminders=1（r-daily 仍是 snoozed，合法计入）
【命题3】在 now+100000 处 DueReminders 返回 id=[r-daily]（被 ack 的 r-once 必须在其中缺席）
```

被 ack 的 `r-once` **正确地缺席了**。测试把「被 ack 的那一条不再到期」写成了
「一条都不到期」，把「仍然有效的 snoozed 提醒」当成了不该计入 pending。

改法不是放宽断言，而是让它**真正钉住 Ack 的语义**：

- 改成按 id 检查「被 ack 的那条不在结果里」，而不是断言整个队列为空；
- pending 先断言为 1（只有仍 snoozed 的 `r-due`），再把 `r-due` 也 ack 掉，
  断言归零、到期队列为空。

这比原版更强：原版的两条断言在任何「队列恰好为空」的情况下都会通过，根本拦不住
ack 失效。

## 护栏

`store_pg_regression_test.go`，三个用例（需真 PG）：

- `TestSnoozeNeverPullsAReminderForward` —— 对**未来**到期的提醒延后，结果必须仍更晚；
  并读回库值与返回值比对一致。
- `TestSnoozeOfAnOverdueReminderStillLandsInTheFuture` —— 对**已过期**提醒做短延后，
  结果必须落在未来且不再到期。
- `TestActiveDayTimestampsNeverReturnsBeforeSince` —— 任何返回的时间戳都不得早于 since。

## 负控（三轮）

**A —— 回退延后修复**（改回 `now + minutes`）：

```
--- FAIL: TestReminderLifecycle
    snoozed until 1790830109, want later than the previous 86400 offset
```

只有 `TestReminderLifecycle` 红，`TestActiveDayTimestamps` 仍绿 → 两个修复互不遮蔽。

**B —— 回退 since 修复**：

```
--- FAIL: TestActiveDayTimestamps
    narrow window got 2 timestamps, want 1: [1790819437 1790823037]
```

只有它红，`TestReminderLifecycle` 仍绿 → 同上。

**C —— 修成「看似合理但缺下限」的版本**（`next_due_at + minutes`，无 `GREATEST`）：

```
--- PASS: TestSnoozeNeverPullsAReminderForward          ← 误判为通过
--- FAIL: TestSnoozeOfAnOverdueReminderStillLandsInTheFuture
    snoozed to 1790816596, want later than now (1790823496)
    a snoozed reminder must not be due, got [{ID:r-overdue ... NextDueAt:1790816596}]
```

这一轮最有价值：**只修一半的修法会被第一条护栏放过**，是第二条抓住的——延后 5 分钟
后提醒**仍然过期**，等于延后完全没生效。两条护栏互补，缺一不可。

（也正因如此，`TestSnoozeOfAnOverdueReminderStillLandsInTheFuture` 在**原始 bug 代码
上是通过的**——它检测不出原缺陷，职责是防「修过头」。这点如实记录，不虚报护栏覆盖面。）

## 验证

- `internal/learning` 整包 **ok 4.846s**（带真 PG）
- `go build -p 1 ./...` exit 0
- 全量 `go test ./... -p 2 -skip TestDiag`（带 DSN）：**只剩任务写守卫 2 个红**，
  email 40.6s ok、executors ok、task ok、tasksync ok

## 环境坑：内存不足导致的假 build failed

`go build ./...`（默认并发）在本机会 `fatal error: runtime: cannot allocate memory`，
或让随机若干包报 `[build failed]`，且每次集合不同。机器 14.8 GB 内存只剩约 3.0 GB
可用，Go 编译器高峰吃不下。

**判定「后端是否红」请用 `go build -p 1 ./...` 与 `go test ./... -p 2`**，
否则会把内存抖动当成代码回归去追。这解释了基线文档第 4 节之外的一类假红。
