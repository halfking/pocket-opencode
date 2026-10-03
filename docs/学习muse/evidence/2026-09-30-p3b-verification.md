# P3 剩余项验证证据（2026-09-30，续）

**范围**：P3 路线图里剩下的两项 —— 协作审批（`task_approval_projections` 读模型）
与目标→任务层级（`parent_id` 子任务 + 派生进度）。
只记录**实际执行并看到输出**的验证；未做的一律标「未验证」。

承接 [2026-09-30-p3-verification.md](2026-09-30-p3-verification.md)（P3 主体）。

---

## 1. 改动清单

### 后端

| 文件 | 性质 | 说明 |
|---|---|---|
| `backend/internal/task/hierarchy.go` | 新增 | 纯函数 `RollUpProgress`（进度聚合）与 `WouldCreateCycle`（环检测） |
| `backend/internal/task/hierarchy_test.go` | 新增 | 进度 6 例 + 环检测 9 例 |
| `backend/internal/task/hierarchy_store.go` | 新增 | `ListChildren` / `ChildProgress` / `ParentMap` / `ListTaskApprovals` + `TaskApproval` |
| `backend/internal/task/workitem.go` | 改动 | 新增事件类型 `child_added` |
| `backend/internal/task/notify.go` | 改动 | `child_added` 明确映射为**不发通知**；`EventPayload` 增 `ChildID` / `ChildTitle` |
| `backend/internal/server/task_hierarchy_handler.go` | 新增 | 3 个端点 + `validateReparent` |
| `backend/internal/server/server.go` | 改动 | 3 条子路由；`PATCH /api/tasks/{id}` 写前校验 `parentId` |

### 前端

| 文件 | 性质 | 说明 |
|---|---|---|
| `frontend/src/api/client.ts` | 改动 | `getTaskChildren` / `createSubtask` / `getTaskApprovals` + `GoalProgress` / `TaskApproval` 类型 |
| `frontend/src/features/tasks/TaskCollaborationPanel.vue` | 改动 | 审批徽标、子任务列表、进度条、新增子任务表单 |

---

## 2. 验证结果

| 命令 | 结果 |
|---|---|
| `go build ./...` | ✅ 退出码 0 |
| `go test ./internal/task/ ./internal/server/ -run "RollUp\|WouldCreateCycle\|Notif\|CanRead\|CanWrite\|Collaboration\|..."` | ✅ 全绿 |
| `go test ./...`（全量） | ✅ 46 包通过；`internal/agent` / `internal/email` 失败 —— 与本轮开始前**同名同数**（18 条），平台性原因 |
| `gofmt -l`（本轮新建文件） | ✅ 0 |
| `npx.cmd vue-tsc --noEmit` | ✅ 退出码 0 |
| `node scripts/build-gate.mjs` | ✅ `✓ built in 22.89s` |
| `node --test src/native/__tests__/*.test.mjs` | ✅ **101 / 101 / 0 fail** |
| `node scripts/check-viewmodel-gaps.mjs` | ✅ 命中 0 = 阈值 |
| `node scripts/report-locale-gaps.mjs` | ✅ 缺 0 / 多 0；en-US 356 key（本阶段未新增键） |

> 字体子集未重建：新引入的图标只有 `add`，它在子集脚本的 `FALLBACK` 名单里
> （P2 证据 §3 已确认）。当前子集 108 个图标，`add` 在内。

---

## 3. 测试真的抓到了一个错误 —— 在我自己身上

第一版 `TestWouldCreateCycle` 的三条断言是错的，**函数是对的**。逐条重推才确认：

树是 `a→b→c`（c 为根）。

- 我断言「把 c 的父设成 b 没问题」——**错**。b 的父是 c，c 的父再设成 b 就闭合成
  `b→c→b`。函数返回 `true` 正确。一个只问「b 在不在 c 下面」的校验器会完全漏掉这种情况。
- 我断言「a 的父设成 b 是环」——**错**。a→b→c 本来就是合法链，重复设置不成环。
  函数返回 `false` 正确。

我把断言改成了上面两条的真实语义，而不是去改函数。这条记下来是因为
**「测试红了 → 改实现」是个很自然的反射动作，而这里该改的是测试**。

另外 500 层深链的断言也写反了：`maxDepth=64` 的预算耗尽时函数返回 `true`，
这是**刻意设计的 fail-closed**（走不完就不能声称安全），已在 `hierarchy.go` 与
测试注释里写明。实际效果是目标树深度上限 64 层，没有任何真实场景会接近。

---

## 4. 设计判断

### 4.1 「目标」不是新实体

没有建 goal 表。目标就是一条普通工作项，别的条目用 `parent_id` 指着它，
进度**派生不落库**。这样「工作 = 任务」那条规则不被破坏：目标只是「可以往下面挂东西的工作项」。

`RollUpProgress` 的两个取值判断是刻意的：
- **无子任务 → 0%，不是 100%**。没有子任务意味着「这不是目标」，画一条满进度条是撒谎。
- **百分比向下取整**。2/3 显示 66 而不是 67 —— 向上取整会让一个还差一项的目标
  看起来快完成了，诱导过早收口。
- `blocked` 单独统计而非算作进度：「完成 50% 但其中 3 个卡死」和「完成 50%」需要不同的处理。

### 4.2 新增子任务不发通知，但留活动流

第一版我用 `comment` 事件 + 「added sub-task: X」的合成文案来记这件事——这是糊弄：
活动流里会显示成一个人发了一条评论，而实际上没人评论。改为新增真事件类型
`child_added`，并让它在 `NotificationKind` 里映射为**不发通知**。
理由：子任务出现这件事在父任务的进度条上已经看得见，再广播一遍正是
「让人把自己的通知源整个静音」的那类噪音。

### 4.3 审批是只读模型

回复审批的路径留在上游（agent 拥有那个请求）。`GET /api/tasks/{id}/approvals`
只回答「这条工作项在等什么」，并返回 pending 计数。已有的
`/api/mobile/approvals/*` 是 agent 视角（instance_id + session_id），
两者不冲突：一个是会话视角的上游操作，一个是任务视角的等待状态。

---

## 5. 明确未验证的部分

| 项 | 原因 |
|---|---|
| **真实 Postgres 端到端** | 仍是最大缺口。本轮新增的 `ListChildren` / `ChildProgress` / `ParentMap` / `ListTaskApprovals` 四条 SQL **全部没有在真库跑过**。`task_approval_projections` 的读取、`parent_id` 的查询与索引都没有实测 |
| **环检测的端到端拦截** | `WouldCreateCycle` 是纯函数且有 9 条单测，但「PATCH 一个会成环的 parentId 真的返回 400」需要真库（要读 `ParentMap`），**未端到端验证** |
| **进度百分比的实际渲染** | 算术有单测，但进度条在实际数据下的观感、以及子任务状态变化后父任务是否刷新，**未在浏览器确认** |
| **审批徽标** | 端点未跑过真库；`pending` 计数与徽标显示**未实测** |
| 真机 / 浏览器实测 | 协作面板本轮新增的三块（审批 / 子任务 / 进度条）**全部只有门禁级验证** |
| `child_added` 的活动流展示 | 后端会写该事件、前端有文案分支，但**未端到端确认**会真的出现在流里 |
| 跨层级的深层聚合 | 只聚合**直接子任务**。孙任务的进度不会自动折进父任务（进度是「直接子任务的完成比例」，不是递归 roll-up）——这是设计取舍，不是缺陷，但**文档里写明了这一点才不至于被误用** |
| 协作审批的**回复** | 未做，也**本轮不打算做**：回复路径属于 agent 上游，任务域只提供读模型 |

---

## 6. P3 收口状态

| 路线图条目 | 状态 |
|---|---|
| 参与者名单 | ✅ 已实现（真库未验） |
| 活动流 | ✅ 已实现（真库未验） |
| 委派落 owner | ✅ 已实现（真库未验） |
| 协作审批 | ✅ 只读投影已实现；回复路径按设计归上游 |
| 目标→任务层级 | ✅ 子任务 + 派生进度 + 环检测已实现 |
| 事件→通知映射 | ✅ 纯函数 + 单测（真实投递未验） |

**P3 代码层面已全部落地。** 但请注意：**整条链的端到端正确性仍然没有在真实
Postgres 上验证过一次**——这是贯穿 P0–P3 的同一个缺口，不是 P3 独有的。
