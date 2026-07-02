# 🎯 Phase 5 — ACC MCP 任务系统对接

**日期**: 2026-07-02
**状态**: 三源任务聚合已实现 + 后台同步已启动

> pocketd 现在通过 MCP 客户端调 ACC（Agent Control Center）系统的 `acc_get_tasks` 工具，并把任务按 `source=acc` 字段合并到统一任务视图。同时启动后台 scheduler 5 分钟拉一次，把 ACC 任务缓存到本地 PG store 让 UI 访问更快。

---

## 1. 任务三源聚合

pocketd 把任务来源分成三类，用 `task.Task.Source` 字段标识：

| Source | 来源 | 数据位置 | 更新方式 |
|--------|------|---------|---------|
| `acc` | ACC 系统 | 远程（`https://acc.kxpms.cn/mcp`）| 实时拉取 + 5min 后台同步到 PG |
| `opencode` | OpenCode 实例 | 远程（每个 instance 的 `/session`）| 实时拉取，不缓存 |
| `local` | 本地 PG store | 本地 PG（pocketd `tasks` 表）| 实时写入 + WebSocket 广播 |

### API 契约

```
GET /api/tasks?source=acc|opencode|local&instance_id=xxx&workstream_id=yyy&limit=100
```

| Query Param | 说明 |
|------------|------|
| `source` | 过滤源；省略=三源合并 |
| `instance_id` | 当 `source=opencode` 时必填；其他源可选 |
| `workstream_id` | 按 workstream 过滤（仅 local 源生效）|
| `limit` | 单源最大返回数（默认 100，最大 500）|

返回：

```json
{ "tasks": [
  { "id": "tsk-xxx", "title": "Q3 预算", "status": "in-progress",
    "priority": "normal", "workstreamId": null,
    "source": "acc", "createdAt": "...", "updatedAt": "..." }
] }
```

---

## 2. 后台同步（tasksync.Scheduler）

```
[mcpClient] --acc_get_tasks--> [Scheduler every 5min] --CreateTask--> [PG tasks 表]
                                                                 ↓
                                                            WebSocket: task_created
                                                                 ↓
                                                            客户端实时刷新
```

- **间隔**：5 分钟（`tasksync.New(client, store, interval)`）
- **去重**：靠 PG UNIQUE 约束（`source + id`）自动去重，重跑只更新时间戳
- **故障容错**：ACC 不可用时静默退避（错误日志节流到 1 分钟一条）
- **优雅退出**：收到 SIGINT 时 `scheduler.Stop()` 干净关闭

---

## 3. ACC MCP 端点配置

pocketd 启动时通过环境变量配置：

| 环境变量 | 默认 | 说明 |
|---------|------|------|
| `POCKET_MCP_BASE_URL` | `""` | ACC MCP 端点（如 `https://mcp.kxpms.cn/acc/mcp`）|
| `POCKET_MCP_API_KEY` | `""` | ACC MCP Bearer token |

若两者都为空，`mcpClient` 为 nil，所有依赖 MCP 的功能降级（任务三源聚合不会包含 acc，但其他源照常工作）。

---

## 4. 待 ACC 团队协作的事项

### 4.1 ACC MCP 端点需要的工具

pocketd 当前只用到 `acc_get_tasks`。如果想让任务系统更完整，ACC 还需暴露：

| 工具 | 用途 | 参数 | 响应 |
|------|------|------|------|
| `acc_get_tasks` ✅ 已实现 | 拉取任务列表 | `{status?, limit?}` | 文本行 `[status] id: title (owner: x)` |
| `acc_create_task` | 创建任务（暂未用，pocketd POST /api/tasks 写到本地 PG）| `{title, description, priority, owner}` | 任务 ID |
| `acc_update_task` | 更新任务状态/结果 | `{task_id, status, result}` | ok |
| `acc_claim_task` | 认领任务（OpenClaw agent）| `{task_id, agent_id}` | claimed_at |
| `acc_complete_task` | 完成任务并提交结果 | `{task_id, result, deliverables}` | completed_at |
| `acc_decompose_task` | L1-L4 层级拆分 | `{task_id, levels}` | 子任务列表 |
| `acc_get_task_tree` | 拉任务树 | `{task_id}` | L1-L4 任务树 |

### 4.2 acc_get_tasks 输出格式建议（标准化）

当前是文本行解析（`ParseToolTasks`），容易出错。建议改为结构化 JSON 返回：

**当前（文本）**：
```
[in-progress] ts-123: Q3 预算 (owner: alice)
[done] ts-124: 报表 (owner: bob)
```

**建议（结构化）**：
```json
{
  "tasks": [
    {
      "id": "ts-123",
      "title": "Q3 预算",
      "status": "in-progress",
      "owner": "alice",
      "priority": "high",
      "due_at": "2026-07-05",
      "tags": ["budget", "Q3"],
      "level": 1,
      "parent_id": null
    }
  ]
}
```

**实现提示词**（给 ACC 团队）：

```
请在 ACC MCP 服务端把 acc_get_tasks 工具的返回格式从文本行改为结构化 JSON：

1. 修改 MCP 工具定义，返回 {tasks: [...]} 而非 text
2. 每个 task 对象含: id, title, status, owner, priority, due_at, tags, level, parent_id, created_at, updated_at
3. level: 1-4 表示 L1 系统任务 → L4 操作任务层级
4. 保持向后兼容：客户端暂未升级，pocketd 的 ParseToolTasks 仍能用旧文本行，但新版应优先返回 JSON
5. 更新 schema 校验（如果用 Pydantic），加入新的 TaskResponse 模型
```

### 4.3 任务事件推送（WebSocket）

pocketd 现在只在本地任务变化时广播 WebSocket。ACC 任务的实时变化（其他 agent 更新状态）目前靠 5 分钟轮询。如需实时性，需 ACC 实现：

- WebSocket endpoint: `wss://acc.kxpms.cn/events`
- 事件格式: `{type: "task_updated"|"task_created"|"task_completed", task_id, payload}`
- pocketd 在 main.go 启动时建立 WS 连接，转发到内部 Hub

---

## 5. 验证清单

部署后验证：

- [ ] pocketd 启动日志显示 `[tasksync] started, interval=5m0s`（若 POCKET_MCP_BASE_URL 配置）
- [ ] `GET /api/tasks?source=acc` 返回 ACC 任务
- [ ] `GET /api/tasks?source=local&workstream_id=xxx` 返回本地任务
- [ ] `GET /api/tasks`（无 source）返回三源合并
- [ ] 5 分钟后 PG `tasks` 表有 `source='acc'` 的记录
- [ ] ACC 端故障时 logs 只每分钟一条错误（不刷屏）

### 端到端 curl 测试

```bash
# 仅 ACC 任务
curl http://localhost:8088/api/tasks?source=acc | jq '.tasks | length'

# 三源合并
curl http://localhost:8088/api/tasks | jq '.tasks | group_by(.source) | map({source: .[0].source, count: length})'
```

---

## 6. 演进路径

| 阶段 | 内容 |
|------|------|
| **当前** | ACC 任务拉取 + PG 缓存 + 三源聚合 |
| Phase 5.1 | ACC 任务事件 WebSocket 推送（实时更新） |
| Phase 5.2 | 双向同步（pocketd 创建任务 → ACC） |
| Phase 5.3 | L1-L4 任务树展示（acc_decompose/acc_get_task_tree） |
| Phase 5.4 | 任务执行回调（pocketd 接收 ACC agent 任务 → 派发到 OpenCode） |