# main 上的回归基线：11 个测试长期为红，分布在 3 个包（2026-10-01）

> 这份基线是在**隔离 worktree**（`git worktree add --detach ... HEAD`）上测的，
> 刻意绕开共享工作树 —— 测它的时候共享工作树正卡在一次未完成的合并上
> （`UU backend/internal/email/pipeline.go`，还留着 `>>>>>>> email-pipeline-snapshot-2026-10-01`），
> 那个状态下 `go build ./...` 直接语法错误，拿它测出来的红是假的。
>
> 命令：`POCKET_TEST_POSTGRES_DSN=... go test ./... -count=1 -skip TestDiag`
> （`-skip TestDiag` 是跳过并行会话留下的 `diag_min_test.go`，那里面有 `select{}`
> 永久阻塞，会让整包 15 分钟超时 —— 第一次跑就是被它顶到 900s 超时的。）

## 1. HEAD 上的失败清单（11 个）

| 包 | 失败用例 | 症状 |
|---|---|---|
| `internal/scheduledtask/executors` | 7 个 `TestWorkItemReminder*` | `expected 1 reminded event, got 0` —— 到期提醒不触发 |
| `internal/learning` | `TestActiveDayTimestamps`、`TestReminderLifecycle` | 见下方单独说明 |
| `internal/server` | `TestTaskWriteGuardBlocksPlainMemberPatch`、`…Delete` | 期望 403，实际 404 |

我本轮改动的区域（`internal/email`、`frontend/src/features/notifications`、
`frontend/src/features/email`）**全绿**：`internal/email` 50.9s ok、
前端 866/866。

## 2. 提醒类：从功能提交那一刻就是红的

`git worktree add --detach ... 8223306` 后重跑，同样失败：

```
--- FAIL: TestWorkItemReminderFiresDueReminder            expected 1 reminded event, got 0
--- FAIL: TestWorkItemReminderBatchSurvivesOneFailure
--- FAIL: TestWorkItemReminderNotificationFailureIsSwallowed
--- FAIL: TestWorkItemReminderToleratesNilNotifier
--- FAIL: TestWorkItemReminderRetiresStaleReminders
--- FAIL: TestWorkItemReminderStaleBoundIsConfigurable
--- FAIL: TestActiveDayTimestamps
--- FAIL: TestReminderLifecycle
```

**不是回归，是提交进来就没绿过。** 测试与实现在同一批落地
（测试 `backend/internal/scheduledtask/executors/workitem_reminder_test.go` 与实现
`workitem_reminder.go` 同属 `8223306`；随后 `88cb5a2 wip(snapshot)` 又动过实现）。

这意味着「到期提醒会触发」这条行为**目前没有任何通过的测试背书**，而提醒是用户可见
功能。是否真的不触发、还是测试夹具没搭对（时区/静默时段/`since` 边界），需要跑
`reminder_diag_test.go`（`88cb5a2` 刚加的）才能定。

## 3. 任务写权限守卫：不是越权，是状态码约定分歧

```
bob PATCH someone else's private work item = 404, want 403: task not found
bob DELETE someone else's private work item = 404, want 403: task not found
```

**写入确实被拦住了**（不是 200），所以这不是越权漏洞。分歧只在 403 vs 404：

- 测试 `task_write_guard_route_test.go:100-101` 写明这是刻意契约：
  「someone else's private work item and got 200. Now 403 … a guard that 403s but
  still writes would be worse than none」，并且另有
  `TestTaskWriteGuardUnknownTaskIs404` 要求「不存在」返回 404。
- 实现 `server.go:1758` 附近有明确注释：「id 仍然与『不存在』不可区分，
  GetTaskScoped 已保证这一点」——**刻意用 404**，与读路径保持一致的不可区分性。

而且 `server.go:1634` 那处 404 **在引入该测试的同一个 commit（d8237d9）里就已存在**，
即测试是「一出生就红」。

**建议**：把测试的期望改成 404（与刻意的不可区分设计一致），并保留
「行未被修改」这个安全关键断言；若产品上更希望用 403 区分「存在但无权限」，
则要改实现并同步改 `TestTaskWriteGuardUnknownTaskIs404` 的边界说明。
**两种都安全，差别只在是否向调用方泄露资源存在性。** 这是产品决策，不该由测试单方面钉死。

## 4. 怎么复现

```powershell
git worktree add --detach C:\workspace\openpocket-wt-head HEAD
$env:POCKET_TEST_POSTGRES_DSN='postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
go -C C:\workspace\openpocket-wt-head\backend test ./... -count=1 -timeout 900s -skip TestDiag
```

`-skip TestDiag` 不能省：`backend/internal/email/diag_min_test.go` 是并行会话留下的
未跟踪诊断文件，里面有 `select {}` 永久阻塞（`TestDiagDeadlineConnAlone`），
不跳过整包必然 15 分钟超时。**超时只说明没在窗口内跑完，不代表卡住**——
第一次跑我只看到 `panic: test timed out after 15m0s`，加 `-v` 打点才定位到具体是哪个用例。
