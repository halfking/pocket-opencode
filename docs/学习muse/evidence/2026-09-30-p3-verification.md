# P3 验证证据（2026-09-30）

**范围**：协作闭环 —— 参与者、委派、活动流、事件→通知映射，以及任务详情页的协作面板。
只记录**实际执行并看到输出**的验证；未做的一律标「未验证」。

环境：Windows / PowerShell，Node v22.23.2 / Go（`go build` 通过），仓库 `C:\workspace\openpocket`。

---

## 1. 改动清单

### 后端

| 文件 | 性质 | 说明 |
|---|---|---|
| `backend/internal/task/notify.go` | 新增 | §4.2 事件→通知映射的**纯函数**：`NotificationKind` / `NotifyRecipients` / `NotificationTitle` |
| `backend/internal/task/notify_test.go` | 新增 | 收件人规则单测（10 个用例） |
| `backend/internal/task/access.go` | 新增 | `CanReadWorkItem` / `CanWriteWorkItem` 纯函数 |
| `backend/internal/task/access_test.go` | 新增 | 读/写权限单测（15 个用例） |
| `backend/internal/server/task_collaboration_handler.go` | 新增 | 5 个协作端点 + 通知分发 |
| `backend/internal/server/task_collaboration_route_test.go` | 新增 | 路由注册锁 + `normalizeParticipants` 单测 |
| `backend/internal/server/server.go` | 改动 | 子资源分发新增 3 条路径；`POST /api/tasks` 把创建者落为 `owner_id` 并写入参与者表 |

### 前端

| 文件 | 性质 | 说明 |
|---|---|---|
| `frontend/src/api/client.ts` | 改动 | 5 个协作方法 + `TaskParticipant` / `TaskParticipantRole` / `WorkItemEvent` 类型 |
| `frontend/src/features/tasks/TaskCollaborationPanel.vue` | 新增 | 参与者名单 + 委派表单 + 活动流 + 评论框 |
| `frontend/src/features/tasks/TaskDetailView.vue` | 改动 | 挂载协作面板 |
| `frontend/src/assets/fonts/material-symbols-outlined.woff2` | 改动 | 新增 `group` / `comment` 字形，106 → **108** |

---

## 2. 验证结果

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ 退出码 0 |
| `go test ./internal/task/ -run "Notif\|CanRead\|CanWrite"` | ✅ 12/12 通过 |
| `go test ./internal/server/ -run "Collaboration\|RunEventsPath\|NormalizeParticipants"` | ✅ 2 顶层 + 3 子用例通过 |
| `go test ./...`（全量） | ✅ 46 包通过；`internal/agent`(16) / `internal/email`(2) 失败 |
| `gofmt -l`（本轮新建文件） | ✅ 0（**注意**：仓库整体有 539 个文件未格式化，属既有状态，见 §5） |
| `npx.cmd vue-tsc --noEmit` | ✅ 退出码 0 |
| `node scripts/build-gate.mjs` | ✅ `✓ built in 13.31s`，退出码 0 |
| `node --test src/native/__tests__/*.test.mjs` | ✅ **101 tests / 101 pass / 0 fail** |
| `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 = 阈值 |
| `node scripts/report-locale-gaps.mjs` | ✅ 8 个非 en-US 语言缺 0 / 多 0；en-US **356 key**（本阶段未新增键） |
| `node scripts/verify-i18n.js` | ✅ 退出码 0 |

### 基线对照（18 条失败不是本轮引入）

用 `git worktree add .baseline-check HEAD` 拉出未改动的 HEAD，在**同一台机器同一命令**下
重跑 `go test -count=1 ./internal/agent/ ./internal/email/`，与本分支失败清单**逐条同名比对**：

| | 失败数 | 清单 |
|---|---|---|
| HEAD 基线 | 18 | `TestACPStdioAdapter_SubscribeEvents`、`TestPiAdapter_*`(7)、`TestStdioTransport_*`(8)、`TestWriteKeyAtomic_CreatesFileWithCorrectMode`、`TestFetchPOP3MailboxAuthRejected` |
| 本分支 | 18 | **完全相同的 18 条** |

失败原因全部是 Windows 平台性的，与本轮改动无关：
`%1 is not a valid Win32 application`（在 Windows 上跑 `.sh` 假二进制）、
`TestWriteKeyAtomic_CreatesFileWithCorrectMode`（POSIX 文件权限位）。

---

## 3. 本轮做出的三个设计判断

### 3.1 路径名是被既有路由逼出来的，不是随手起的

架构方案原文写的是 `GET/POST /api/tasks/{id}/events` 走活动流。实现时发现这条路**已经被占用**：

- `GET /api/tasks/{id}/events` → `handleTaskRunEvents`，是 **ACC 运行事件**投影；
- `POST /api/tasks/delegate`（无 id）→ `handleDelegateTask`，是「经 ACC 建任务」。

照文档写会把 ACC 的运行历史顶掉，或直接劫持已有契约。所以：

- 协作活动流 → `/api/tasks/{id}/activity`
- 委派到人 → `POST /api/tasks/{id}/delegate`

`TestRunEventsPathIsNotHijackedByActivityStream` 专门锁住第一条，防止后人「按文档改回去」。

### 3.2 `due_soon` 与 `reminded` 重复，已对账折叠

§4.2 原表有 `due_soon → work_item.due_soon → 参与者`，但提醒中枢在 `remind_at` 到点时
写入的事件类型是 `reminded`，而表里 `reminded` 那行已经负责发给 owner。两行并存会让
owner 在同一次触发里收到两条通知。已把 `due_soon` 折叠进 `reminded`，并把
`created` / `due_changed` 明确为**不发通知**。架构文档 §4.2 已同步改成以
`notify.go` 为准的版本。

`completed` 是唯一**保留操作者**的事件：关闭任务的人要收到回执。
其余广播类事件一律排除操作者——让人被自己的动作通知，是最快教会用户静音一个来源的方式。

### 3.3 创建者落 `owner_id`，而不是加 `created_by` 列

`tasks` 表没有 `created_by`。若不管，**一条无 owner 的私有任务创建者本人也读不到**（403），
这是个真实的死角。两条路：

- (a) 加 `created_by` 列 → 要动 `taskColumns` / `scanTask` / `CreateTask` / `PutTask` 四处，
  而这些 SQL **在本轮无法对真实 Postgres 验证**，改错就是运行时扫描错位；
- (b) 创建时把 `owner_id` 默认为 JWT 身份 → 复用已有的列和已有的写路径。

选 (b)。它同时让「委派落 owner」有了落点：委派就是换 owner。
副作用是 `owner_id` 现在同时表示「谁建的」和「现在归谁」，已写进文档。

---

## 4. 明确未验证的部分

| 项 | 原因 |
|---|---|
| **真实 Postgres 端到端** | 最关键的缺口。`work_item_participants` / `work_item_events` 的读写、唯一索引、`SetParticipants` 的事务与 `ON CONFLICT` 全部**没有在真库跑过**。DDL 沿用既有 `CREATE TABLE IF NOT EXISTS` 幂等范式，但没验证过 |
| **通知真的发出去** | `dispatchWorkItemNotification` 调用 `notifySvc.Dispatch`，`internal/server` 测试里 `notifySvc` 为 nil，走不到。收件人规则有纯函数单测，但**端到端投递、免打扰时段、inbox 留存全部未实测** |
| **403 的真实响应** | `CanReadWorkItem` / `CanWriteWorkItem` 是纯函数且有单测，但「非参与者 GET 私有工作项真的返回 403」需要真库，**未端到端验证** |
| **委派的原子性** | participants → `owner_id` → 事件 → 通知是四步顺序写，**不是事务**。第二步失败时参与者表已改、`owner_id` 未改，handler 会返回 500 但留下不一致状态。已刻意让它显式报错而不是吞掉，但**这个不一致窗口未在真库复现验证** |
| 真机 / 浏览器实测 | 协作面板只有门禁级验证。`available=false` 的隐藏分支、评论发送、活动流渲染**均未目视确认** |
| `TaskCollaborationPanel` 的 i18n | 沿用本 feature 既有约定（硬编码中文），**未本地化**。`tasks` 整个目录（TasksView / TaskDetailView）本来就没接 i18n，这里跟着走而不是半个 feature 两种风格；但这是一个**已知的不一致**，不是本轮的成果 |
| 协作审批 / 目标→任务层级 | 路线图 P3 还列了这两项，**本轮未做**。`parentId` 列已存在但没有目标实体与进度聚合 |
| e2e 套件 | 未跑；面板带 `data-testid`（`task-collaboration` / `collab-participants` / `collab-activity` / `collab-delegate-input` / `collab-delegate-role` / `collab-comment-input`）供后续补测 |

---

## 5. 顺带发现（未修，仅记录）

**仓库整体 gofmt 不干净**：`gofmt -l ./internal ./cmd` 报出 **539 个文件**，
包含大量与本项目无关的既有文件（`llm_gateway_*.go`、`mobile_*.go` 等）。
本轮只对自己新建/新建后修改的 8 个文件跑了 `gofmt -w`。
把全仓库格式化会制造一个巨大的、与学习模块无关的 diff，因此未做——
如需清理，应作为独立的一次提交。
