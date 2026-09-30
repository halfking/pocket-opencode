# P4 验证证据（2026-09-30）

**范围**：提醒落地里**不依赖部署证书**的部分 —— 工作项一次性提醒的生产者、
免打扰顺延，以及 ADR-002 的契约变更登记。
只记录**实际执行并看到输出**的验证；未做的一律标「未验证」。

---

## 1. 出发点：一个「有存储、无消费者」的字段

本轮开工前先查了 `tasks.remind_at` 的实际使用情况，结果是：

- 列存在（`store.go:171` 的 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`）；
- 写入与校验存在（POST/PATCH 校验 `remindAt` 不晚于 `dueAt`）；
- **没有任何东西消费它** —— `reminded` 事件类型、`work_item.reminder` 通知映射、
  免打扰顺延逻辑，全都是「有定义、无生产者」。

也就是说：用户在任务上设了「提前 1 小时提醒」，界面上存下来了，然后什么都不会发生。
这是比缺功能更糟的一种状态——它看起来是做了的。所以 P4 的第一件事是补生产者。

## 2. 改动清单

| 文件 | 性质 | 说明 |
|---|---|---|
| `backend/internal/task/quiet.go` | 新增 | 免打扰窗口纯函数 `QuietWindow`（`InWindow` / `Defer` / `DeferralMinutes`）+ `ReminderEventID` |
| `backend/internal/task/quiet_test.go` | 新增 | 窗口跨越午夜、顺延、幂等键（7 个用例） |
| `backend/internal/task/hierarchy_store.go` | 改动 | `DueTaskReminders` / `ClearTaskRemindAt` |
| `backend/internal/scheduledtask/executors/workitem_reminder.go` | 新增 | `WorkItemReminderExecutor` |
| `backend/internal/scheduledtask/executors/workitem_reminder_test.go` | 新增 | 执行器单测（10 个用例，全用假 client，不需要数据库） |
| `backend/internal/scheduledtask/types.go` | 改动 | 新增 `KindWorkItemReminder` 并**加入 `AllKinds()`** |
| `backend/cmd/pocketd/main.go` | 改动 | 注册 executor + 晚绑通知客户端 |
| `backend/internal/task/store_contract_test.go` | 新增 | 列 ↔ 扫描位错位守卫（4 个用例） |
| `docs/flashcards-contract.md` | 改动 | 增补「服务端调度为写入真相」条款（ADR-002 收口） |

## 1b. 第二轮：把自己引入的「存量积压」风险修掉

上一轮结尾我记了一条风险：「存量 `remind_at` 会在部署后集中触发，积压规模未估量」。
那是**我自己引入的**风险，不该只写进文档就放着。本轮回头量化并修掉了。

**先量化，别凭感觉。** 调度器 tick 默认 5s（`config.go:291`），执行器每 tick 限 50 条
→ 最坏 10 条/秒。所以「通知瞬时爆炸」这个担心是**夸大**的，我上一轮的表述不准。

但真正的风险是另一个，量级完全不同：

> 一万条积压会变成**一万条推送**，而且每一条都是几天前的旧提醒。

这正是我自己写在 `notify.go` 注释里的那句警告——「让人被通知淹没，是最快教会用户
静音一个来源的方式」。batch 限流只压住了速率，没有压住**总量**。

**修法：加陈旧度上限（`staleAfter`，默认 24h）。** 超过上限的提醒**静默退役**
（`remind_at` 归零、不写事件、不推送），并在结果里计入 `stale`。

三个刻意的选择：

1. **退役而不是丢弃。** `remind_at` 归零，所以它不会每个 tick 反复冒出来。
2. **可观测而非静默。** 结果里有 `stale` 计数，运维能看到「本次退役了多少」，
   而不是「什么都没发生且不知道为什么」。
3. **陈旧判定优先于免打扰。** 三天前的提醒不该被「顺延到明天早上 07:30」——
   那样只是把噪音推迟了。判定顺序是 stale → quiet → fire。
4. **可以关掉。** `SetStaleAfter(0)` 取消上限，供确实想把所有积压都推一遍的部署使用。

被退役的工作项本身仍会作为「逾期」出现在任务列表里——那才是过期提醒该待的地方。


## 3. 验证结果

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ 退出码 0 |
| `go test ./internal/task/ -run "Quiet\|ReminderEventID"` | ✅ 7/7 |
| `go test ./internal/scheduledtask/... -run "WorkItemReminder"` | ✅ **13/13**（第二轮新增 3 条陈旧上限用例） |
| `go test ./internal/task/ -run "TaskColumns\|ScanTask"` | ✅ 4/4 |
| `go test ./...`（全量） | ✅ 46 包通过；`internal/agent` / `internal/email` 18 条失败 —— 与基线同名同数（平台性） |

## 4. 三个关键设计点

### 4.1 幂等靠事件主键，不靠执行器自觉

`§4.2` 要求「每任务每提醒点只发一次」。实现方式是让 `event_id` 由 `remind_at` 派生
（`task.ReminderEventID` → `"reminder:<remind_at>"`），而 `work_item_events` 的主键正是
`(workspace_id, task_id, event_id)`。于是**去重由数据库保证**，而不是由执行器里一句
「我检查一下有没有发过」保证——后者在两个 pocketd 实例同时跑、或一次 tick 中途崩溃时都会漏。

单测里显式断言了 `EventID == ReminderEventID(item.RemindAt)`，就是为了钉住这一点。

### 4.2 顺延而不是丢弃，且状态分得清楚

落在免打扰窗口内的提醒**不触发、不通知**，而是把 `remind_at` 改写到窗口结束时刻。
执行器分别计 `fired` 与 `deferred`：一个被顺延的提醒仍然是「待发」，
如果混进 `fired` 里，统计就会骗人。

窗口默认 22:30→07:30，**跨越午夜**。朴素的 `start <= x && x < end` 在这里会全错，
所以 `InWindow` 显式分「同日窗口」与「跨午夜窗口」两条分支，各有单测。
`Defer` 还有一个容易被忽略的点：23:50 顺延到的是**次日** 07:30，
不是今天那个已经过去的 07:30——否则提醒会落到过去。

### 4.3 通知失败不让整个 run 失败

提醒事件已经落库了，此时 `Dispatch` 失败如果冒泡成 500，调度器会重试，
而重试的是一次**已经成功的写入**。所以投递失败只记日志、计入 `notified` 的实际值。
单测 `TestWorkItemReminderNotificationFailureIsSwallowed` 钉住了「事件仍然写入」。

## 5. 测试抓到的三类错误

### 5.1 我自己写错的断言（1 条）

「23:50 顺延到次日 07:30 应该跨过 86400 秒」——错的。23:50 到次日 07:30 是 7h40m。
**跨越午夜是钟面概念，不是时长概念**。改成用「分钟数 == 07:30 且 next > fireAt」
两个条件联合判定（后者排除了今天那个已过去的 07:30）。

### 5.2 `float64` 与 `int` 比较永远为假（3 条）

`json.Unmarshal` 出来的数字是 `float64`，`out["fired"] != 1` 恒为真。
最阴险的地方在于它**读起来像「执行器算错了计数」**，会把我引去查错的文件。
加了一个 `num(t, out, key) int` 辅助函数做显式转换。

### 5.3 测试命名撞车（1 条）

同包内闪卡测试已有 `fakeNotifier`，我新建的同名 → 编译失败。
改名为 `fakeWorkNotifier`，并顺手修正了 `Dispatch` 的返回签名
（真实接口返回 `*notifycenter.DispatchResult`，我一开始写成了 `string`）。

### 5.4 只在半夜才通过的测试（1 条，第二轮发现）

加完陈旧上限后，原来的静默时段测试开始失败——因为它们用的是「day 0 的 23:50」
这种固定时间戳，在新的 24h 上限面前全部成了陈旧。

改法不是把时间戳换成「现在」，而是**从 `now` 推导出一个必定包含当前时刻的窗口**
（`windowAroundNow()`：StartMin = now-30、EndMin = now+30）。硬编码 23:50 的话，
这类测试一天里只有一半时间是有效的——**只在半夜通过的测试比没有测试更糟**，
因为它会在 CI 的某个时刻悄悄变红，看起来像 flaky。

顺带把另几个用 `recent(60)` 之外固定时间戳的用例也统一了。

### 5.5 `int64` / `int` 比较（1 条）

`(next / 60) % 1440` 是 `int64`，`window.EndMin` 是 `int`，编译期就报出来。
这类错编译器能抓，和 §5.2 那种编译器抓不到的正好构成对照。

## 6. 顺带加的守卫：列 ↔ 扫描位错位

见 `store_contract_test.go`。这直接对应 P3 证据里那句
「这些 SQL 在本轮无法对真实 Postgres 验证，改错就是运行时扫描错位」——
当时我因为验不了库而**放弃了加 `created_by` 列**。现在把其中能离线验的那部分补上了：

- 用一个记录型 stub row 真正调用 `scanTask`，比对**列数 == 目标数**；
- 每个目标必须是**指针**（传值会编译通过但静默丢列）；
- `taskColumns` 不得有重复列（重复列会让计数正确但整体错位，是计数检查看不见的形状）；
- P0 新增的工作项列必须在列表里（缺了 UI 就渲染空白，正是旧 `category` 字段的老毛病）。

**并且验证了这个守卫本身抓得住**：往 `taskColumns` 注入一列 `injected_drift` 后，
测试如实报 `selects 26 columns but scanTask passes 25 destinations`；随后精确回滚。
这一步不能省——P2 的字体子集正则就栽在「脚本自己报的计数变大了」这种假绿上。

## 7. 明确未验证的部分

| 项 | 原因 |
|---|---|
| **真实 Postgres** | 仍然是那个跨阶段缺口。本轮新增的 `DueTaskReminders` / `ClearTaskRemindAt` 两条 SQL 未在真库跑过；提醒的**真实投递**同理。执行器单测全用假 client，**一条真实 SQL 都没执行** |
| **调度器真的按节奏调起这个 executor** | `Register` 与 `AllKinds()` 已有单测，但「调度器 tick → 到期扫描 → 通知」这条完整链路**未端到端验证** |
| 免打扰的时区 | `Defer` 用 UTC 分钟近似（沿用学习域既有的做法，`nextAfterQuietHours` 同款），提醒行只存 unix 秒。**跨时区用户会顺延到错误的钟点**，这一点沿用了既有实现的口径，没有解决 |
| `remind_at` 早于当前时间的存量数据 | 已在第二轮用 24h 陈旧上限解决：超龄的静默退役并计入 `stale`，不再推送。见 §1b |
| APNs / FCM 真实推送 | **非代码问题**，是部署前置（证书 + provider SDK）。仍是 `NoopPushSender` |
| 连续学习天数（streak） | P4 原列的第三项，**未做** |
| 真机 / 浏览器实测 | 始终未做 |

## 8. P4 状态

| 路线图条目 | 状态 |
|---|---|
| 工作项一次性提醒（`remind_at` 消费者） | ✅ 已实现（真库与真实投递未验） |
| 免打扰顺延 | ✅ 已实现（纯函数 + 单测；时区近似沿用既有口径） |
| APNs / FCM 真实 Sender | ❌ **部署前置**，非代码可解 |
| 连续学习天数（streak）+ 里程碑 | ❌ **未做** |
| 契约变更登记（ADR-002 收口） | ✅ 已做（`docs/flashcards-contract.md`） |
