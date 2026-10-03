# 提醒类 7 个红测的定性：真 bug + 6 个时钟耦合测试

> 日期：2026-10-01 10:00 起，北京时间
> 分支：`audit/backend-red-baseline`（基线文档 `2026-10-01-backend-red-baseline.md` 的续篇）
> 涉及：`backend/internal/scheduledtask/executors/workitem_reminder.go`

## 结论

「main 上 11 个长期为红的测试」里的**提醒 7 个**，不是同一件事：

- **1 个是产品缺陷**：`quietResolver.window()` 把「已关闭静默」的零值窗口悄悄替换回
  默认的 22:30→07:30，使 `SetQuietWindow` 文档承诺的「零值 = 关闭顺延」这个合法配置
  **根本无法达成**。
- **6 个是测试与墙钟耦合**：它们断言「提醒应当触发」，却没有关闭静默，而
  `pinServerZone` 把 `time.Local` 钉成 UTC。于是整个套件在 22:30–07:30 UTC 之间跑就
  全红，其余时段全绿。

产品缺陷才是这批红测里唯一需要改生产代码的部分；6 个时钟耦合用例本身逻辑没错，
但它们**只在一天中的 10 个小时里通过**，这与同文件 `pinServerZone` 注释里自己写的
告诫（「without this they fail by clock rather than by logic」）正是同一类问题——
只是当初只给 deferral 一侧打了补丁（`windowAroundNow()`），firing 一侧漏了。

## 产品缺陷

`workitem_reminder.go` 的两条「关闭免打扰」路径结论相反：

| 路径 | 入口 | 结果 | 改前 |
|---|---|---|---|
| per-user 设置 | `QuietPreferences.Disabled` → `prefs.Window()` 返回零值 → `resolve()` 原样返回 | 不顺延 | ✅ 绿 |
| 执行器级设置 | `SetQuietWindow(零值)` → `q.fallback` 为零 → `window()` **替换成默认值** | 仍顺延 | ❌ 红 |

`SetQuietWindow` 的文档（L116-117）原文写着「A zero value disables deferral,
which is a legitimate configuration, not an error」——这个承诺是假的。

### 诊断脚本打脸了我自己的假设

先验假设是「纯时钟耦合，产品没问题」。诊断脚本 `fireScenario` 跑出的结果否掉了它：

```
A 默认静默窗口：fired=false  {"deferred":1,"due":1,"fired":0,...}
B 静默关闭    ：fired=false  {"deferred":1,"due":1,"fired":0,...}
```

B 组显式 `SetQuietWindow(零值)`，按文档应当直接触发，实际照样 `deferred:1`。
**对照组不生效，恰恰是缺陷本身。** 假设是错的，bug 是真的。

（该诊断脚本已删除——它的名字带 `TestDiag`，跑全量时需要 `-skip TestDiag` 才能
绕过一个 `select{}` 永久阻塞的旧文件，留着只会成为下一个踩坑点。结论已转写为下方的
正规护栏。）

### 修复

```go
 func (q *quietResolver) window() task.QuietWindow {
-	if q == nil || q.fallback == (task.QuietWindow{}) {
+	if q == nil {
 		return task.DefaultQuietWindow()
 	}
 	return q.fallback
 }
```

**为什么不会改变生产行为**：`cmd/pocketd/main.go:1032` 只用 `NewWorkItemReminderExecutor`
构造、**从不调用 `SetQuietWindow`**，22:30→07:30 由构造器装上（`workitem_reminder.go:83`）。
全仓 grep 确认唯一调用零值 `SetQuietWindow` 的地方是测试。默认值搬到了它本来的
归属处（构造器），解析器不再二次重建。

## 时钟耦合的 6 个用例

新增 `quietHoursOff(ex)` 助手，在 6 处「期待触发」的用例里显式关掉静默：
`FiresDueReminder`、`BatchSurvivesOneFailure`、`NotificationFailureIsSwallowed`、
`ToleratesNilNotifier`、`RetiresStaleReminders`、`StaleBoundIsConfigurable`。

deferral 一侧的用例**不动**，它们本来就用 `windowAroundNow()` 构造窗口，与时刻无关。

## 护栏

`workitem_reminder_quietoff_test.go`，两个用例 + 一个对照：

- `TestSetQuietWindowZeroDisablesDeferral` —— 同一时刻（固定 02:00 UTC，全年都在
  22:30–07:30 内）跑两遍，**唯一变量是窗口**：默认窗口 → `deferred:1`；零值窗口 →
  `fired=1` + 事件 1 条 + 通知 owner `alice` + `remind_at` 归零。
- `TestDefaultQuietWindowStillAppliesWithoutConfiguration` —— 钉住「没人配置时默认
  窗口仍然生效」这半边契约，也就是生产行为未被本修复改变的依据。

用固定 `02:00 UTC` 而非 `now`，护栏本身**与运行时刻无关**，不把时钟耦合换个地方
再犯一遍（这正是原测试的病根）。

## 负控

两轮，都确认转红：

**负控 A —— 改回产品代码**：把 `if q == nil || q.fallback == (task.QuietWindow{})` 还原。

```
--- FAIL: TestSetQuietWindowZeroDisablesDeferral/the_zero_window_fires_it_at_its_own_time
    quietoff_test.go:72: quiet hours are off, so nothing may be deferred:
    map[deferred:1 due:1 fired:0 notified:0 stale:0 workspaceId:ws-1]
```

只有钉住修复的那个子用例红，`the_default_window_defers_a_02:00_reminder` 与
`TestDefaultQuietWindowStillAppliesWithoutConfiguration` **仍绿** —— 证明三条断言
互不遮蔽，绿不是因为整体失效。

**负控 B —— 掏空测试助手**：把 `quietHoursOff` 改成空函数（等价于改动前）。

```
--- FAIL: TestWorkItemReminderFiresDueReminder
    expected 1 reminded event, got 0
--- FAIL: TestWorkItemReminderBatchSurvivesOneFailure
--- FAIL: TestWorkItemReminderNotificationFailureIsSwallowed
--- FAIL: TestWorkItemReminderToleratesNilNotifier
--- FAIL: TestWorkItemReminderRetiresStaleReminders
--- FAIL: TestWorkItemReminderStaleBoundIsConfigurable
```

恰好 6 个、报错与修复前逐字一致 → 绿是靠关静默换来的，不是断言被削弱。

## 验证

- `internal/scheduledtask/executors`：**整包 ok**（修复前 7 红）
- `go build ./...` exit 0；`go vet ./internal/scheduledtask/...` exit 0
- `go test ./... -p 2 -skip TestDiag`：**全仓零 FAIL**

### 一个需要记住的坑：全量跑会假报 build failed

默认并发跑全量时，`internal/adapter`、`internal/db`、`internal/facade`、
`internal/mcp`、`internal/migration`、`internal/notifycenter`、`internal/opencode`、
`internal/orchestrator` 会**随机**报 `[build failed]`，且每次集合不同。这些包单独跑
全部 `ok`，`go vet` 也 exit 0，`-p 2` 跑全量零 FAIL。

判定为 Windows 下并行编译的资源竞争，**不是代码错误，也不是本轮引入**。
判断「后端是不是红的」请用 `go test ./... -p 2`，否则会把编译抖动当成回归去追。

## 顺带澄清：学习 2 / 任务写守卫 2

本轮全量跑中 `internal/learning` 与 `internal/server` 均为 `ok`。基线文档记录的
那 4 个红测**未复现**。其中 `learning` 同样有 `nextAfterQuietHours` 静默机制，
高度疑似同源时钟耦合；`server` 的 2 个是任务写权限守卫 403/404 断言，属于产品决策
（是否泄露资源存在性），两种都安全。**本轮未对这两组做改动，状态维持「基线记录了、
本轮未复现」，不宣称已修。**
