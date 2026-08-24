# 合同状态矩阵（Status Matrix）

> 本文件追踪新架构 v1 中所有"合同草案 → 实现"的状态。
> 每条目包含：合同位置、当前状态（`draft` / `reserved` / `implemented` / `contract-tested` / `source-inspected` / `mock-only` / `planned` / `blocked`）、责任人、阻塞项、最近一次变更。
>
> 本节是子任务 D（OpenPocket Adapter 边界矩阵）和子任务 C（RedClaw ↔ OpenCode 映射矩阵）的状态快照。

---

## Pocket Adapter（子任务 D）

- 关联合同：`docs/新架构v1/04-contracts/pocket-adapter-matrix.md`、`pocket-zag-incremental.md`
- 关联代码：`backend/internal/zagclient/{client.go,noop.go,client_test.go}`
- 关联现状：legacy redclaw（`implemented`）、facade client（`contract-tested`）、mcp client（`implemented`）、opencode_http adapter（`implemented`）、目标 ZAG client（`reserved`）

| 适配器 | 接口契约 | 鉴权 | tenant 隔离 | 幂等 | 超时 | 重试 | outbox | 当前状态 | 阻塞项 |
|---|---|---|---|---|---|---|---|---|---|
| legacy redclaw client | `/api/v1/pocket/*` | 静态 `Bearer + X-Tenant-ID` | 客户端覆盖 | 无 | 30s | 无 | 无 | `implemented`（仅 legacy bridge） | 安全模型 v3 §3.1 不允许作为新授权路径 |
| facade client | `/api/v2/{tasks,approvals,notifications,memory}` | 服务 JWT | JWT claim | 自动 Idempotency-Key | 30s / SSE 独立 | 由调用方 reconcile | 无 | `contract-tested` | — |
| mcp client | JSON-RPC 2.0 + MCP `2024-11-05` | HS256 内部 JWT | JWT claim + ACC RLS | JSON-RPC `id` | 30s / init TTL 5min | `sync.Once` 缓存失败 | 无 | `implemented` | — |
| opencode_http adapter | OpenCode runtime HTTP | 无（受信网络） | 本地目录边界 | runtime 端保证 | 实例化时配置 | 无 | 无 | `implemented` | 不感知 tenant，需在更高层兜底 |
| 目标 ZAG client | `Client` 接口（`backend/internal/zagclient/client.go`） | mTLS **或** short-lived delegated JWT | JWT claim + mTLS SAN 双向校验 | 写路径强制 `Idempotency-Key` | 30s / SSE 独立 | 调用方 reconcile + `indeterminate` | 先 outbox 再执行；高危 fail-closed | `reserved`（仅接口 + Noop 实现 + 单测；运行时未注册路由） | 1) ZAG 协议合同冻结；2) mTLS / delegated JWT 签发；3) outbox + reconciliation worker；4) M0/M1 门禁通过 |

### 状态值定义

- `draft`：合同草案，未进入代码层。
- `reserved`：代码层接口或路由已留位（接口/常量/路由占位），运行时未启用。
- `implemented`：运行时已接入，被调用方依赖。
- `contract-tested`：行为由自动化测试覆盖。
- `mock-only`：仅 mock / fake 实现存在。
- `blocked`：因前置依赖显式禁止启用。

### 子任务 D 交付清单

| 类别 | 文件 | 状态 | 行数 |
|---|---|---|---|
| 边界矩阵 | `docs/新架构v1/04-contracts/pocket-adapter-matrix.md` | `draft` | 见 wc -l |
| 增量接口设计 | `docs/新架构v1/04-contracts/pocket-zag-incremental.md` | `draft` | 见 wc -l |
| 状态矩阵（本文件） | `docs/新架构v1/04-contracts/_status.md` | `draft` | 见 wc -l |
| ZAG client 接口契约 | `backend/internal/zagclient/client.go` | `reserved` | 见 wc -l |
| ZAG noop 实现 | `backend/internal/zagclient/noop.go` | `reserved` | 见 wc -l |
| ZAG client 单测 | `backend/internal/zagclient/client_test.go` | `contract-tested` | 见 wc -l |

### 最近一次变更

- 2026-08-23：子任务 D 首版（接口预留 + 文档冻结）。

---

## RedClaw ↔ OpenCode 映射（子任务 C）

- 关联合同：`docs/新架构v1/04-contracts/redclaw-to-opencode.md`
- 关联源码：
  - RedClaw：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/{facade,orchestrator,agentcontainer,authagent,connectors}/`
  - OpenCode：`/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/{session,event,permission,question,pty,control}.ts`
- 状态分布（截至 2026-08-23）：端点矩阵 **45 行**，其中 `source-inspected` = **41** 行、`mock-only` = **5** 行、`planned` = **0** 行、`blocked` = **13** 行（blocked 是路由级别受限项，可能与 mock-only 在同一端点重复计）。

### 端点映射状态（节选自 `redclaw-to-opencode.md`）

| 映射项 | RedClaw 路径 + handler | OpenCode 路径 + handler | 状态 |
|---|---|---|---|
| 任务提交 | `POST /api/v1/tasks` → `handlers.go:68 SubmitTask` | `POST /session/:sessionID/message` → `session.ts:316 prompt` | source-inspected（路径） / mock-only（默认 mock） |
| 任务查询 | `GET /api/v1/tasks/:task_id` → `handlers.go:102 GetTask` | `GET /session/:sessionID` → `session.ts:132 get` | source-inspected |
| 任务列表 | `GET /api/v1/tasks` → `handlers.go:120 ListTasks` | `GET /session` → `session.ts:111 list` | source-inspected |
| 任务结果 | `GET /api/v1/tasks/:task_id/result` → `handlers.go:136 GetTaskResult` | `GET /session/:sessionID/message` → `session.ts:179 messages` | source-inspected |
| 任务取消 | **不存在** | `POST /session/:sessionID/abort` → `session.ts:253 abort` | blocked（RedClaw 缺 cancel path） |
| 创建 session | `POST /api/v1/sessions` → `handlers.go:160 CreateSession` | `POST /session` → `session.ts:203 create` | source-inspected |
| session 详情 | `GET /api/v1/sessions/:session_id` → `handlers.go:185 GetSession` | `GET /session/:sessionID` → `session.ts:132 get` | source-inspected |
| session 投递消息 | **不存在** | `POST /session/:sessionID/message` → `session.ts:316 prompt` | blocked（RedClaw session 不接受消息） |
| session share | **不存在** | `POST /session/:sessionID/share` → `session.ts:279 share` | blocked（RedClaw 无 share API） |
| session abort | **不存在** | `POST /session/:sessionID/abort` → `session.ts:253 abort` | blocked |
| 控制信号 create | `POST /api/v1/control` → `handlers.go:225 CreateControl`（Role≥admin/manager） | **不存在** | source-inspected（仅 RedClaw） |
| 控制信号 sign | `POST /api/v1/control/:command_id/signature` → `handlers.go:257 SignControl` | **不存在** | source-inspected（仅 RedClaw） |
| 控制信号 execute | `POST /api/v1/control/:command_id/execute` → `handlers.go:313 ExecuteControl` | **不存在** | source-inspected（仅 RedClaw） |
| 审计查询 | `GET /api/v1/audit` → `handlers.go:374 QueryAudit` | **不存在** | source-inspected（仅 RedClaw） |
| 监控 cluster | `GET /api/v1/monitor/cluster` → `handlers.go:405 MonitorCluster` | **不存在** | source-inspected（仅 RedClaw） |
| 监控 workflow | `GET /api/v1/monitor/workflow/:session_id` → `handlers.go:420 MonitorWorkflow` | **不存在** | source-inspected（仅 RedClaw） |
| WS hub | `GET /api/v1/ws` → `handlers.go:443 WebSocket` | **不存在** | source-inspected（仅 RedClaw） |
| agent invoke | `POST /api/v1/invoke` → `agentcontainer/handlers.go:55 Invoke` | **不存在** | source-inspected（OpenClaw 子进程执行） |
| skills list | `GET /api/v1/skills` → `agentcontainer/handlers.go:129 ListSkills` | **不存在** | source-inspected |
| skill 详情 | `GET /api/v1/skills/:skill_id` → `agentcontainer/handlers.go:135 GetSkill` | **不存在** | source-inspected |
| 权限 profile upsert | `POST /api/v1/permissions/profiles` → `agentcontainer/handlers.go:178 UpsertProfile` | **不存在**（OpenCode 用 `PermissionV1.Ruleset` per-session） | source-inspected |
| 权限 profile 查询 | `GET /api/v1/permissions/profiles/:position_id` → `agentcontainer/handlers.go:204 GetProfile` | **不存在** | source-inspected |
| approval token issue | `POST /api/v1/tokens/issue` → `agentcontainer/handlers.go:228 IssueToken` | **不存在** | source-inspected |
| approval token verify | `POST /api/v1/tokens/verify` → `agentcontainer/handlers.go:257 VerifyToken` | **不存在** | source-inspected |
| SSO login | `GET /api/v1/sso/login` → `sso/handlers.go:16`（mgr nil → 503） | **不存在** | source-inspected |
| SSO callback | `GET /api/v1/sso/callback` → `sso/handlers.go:31`（`exchangeCode` stub） | **不存在** | source-inspected（含 stub 标记） |
| SSO logout | `POST /api/v1/sso/logout` → `sso/handlers.go:55`（占位） | **不存在** | source-inspected（占位） |
| approval submit | `POST /api/v1/requests` → `approval/executor.go:70 Submit` | **不存在** | source-inspected |
| approval list pending | `GET /api/v1/requests` → `approval/executor.go:104 ListPending` | **不存在** | source-inspected |
| approval get | `GET /api/v1/requests/:request_id` → `approval/executor.go:115 Get` | **不存在** | source-inspected |
| approval approve | `POST /api/v1/requests/:request_id/approve` → `approval/executor.go:130 Approve` | **不存在** | source-inspected |
| approval reject | `POST /api/v1/requests/:request_id/reject` → `approval/executor.go:135 Reject` | **不存在** | source-inspected |
| grant verify | `POST /api/v1/grants/verify` → `approval/executor.go:186 VerifyGrant` | **不存在** | source-inspected |
| permission list | `POST /api/v1/requests` → RedClaw `approval` queue（不同模型） | `GET /permission` → `permission.ts:21 list` | source-inspected（双源） |
| permission reply | `POST /api/v1/requests/:request_id/approve` 或 `/reject` | `POST /permission/:requestID/reply` → `permission.ts:31 reply` | source-inspected（双源） |
| question list | **不存在**（RedClaw 无 question API） | `GET /question` → `question.ts:22 list` | source-inspected（仅 OpenCode） |
| question reply | **不存在** | `POST /question/:requestID/reply` → `question.ts:32 reply` | source-inspected（仅 OpenCode） |
| question reject | **不存在** | `POST /question/:requestID/reject` → `question.ts:45 reject` | source-inspected（仅 OpenCode） |
| PTY 全套 | **不存在**（RedClaw 无 PTY） | `GET/POST/PUT/DELETE /pty[/...]` → `pty.ts:44/54/64/76/88/101/113/144` | blocked（RedClaw 端缺失） |
| 事件 SSE | `GET /api/v2/runs/:run_id/events` → `handlers_events.go:22`（real 模式 503） | `GET /event` → `event.ts:14 subscribe`（SSE） | source-inspected / mock-only |
| 通知 list/ack | `GET /api/v2/notifications` + `/ack`（real 模式 503） | **不存在** | mock-only（real 模式 503） |
| façade capabilities | `GET /api/v2/capabilities` → `server.go:190 handleCapabilities` | **不存在** | source-inspected |
| façade tasks CRUD | `POST/GET /api/v2/tasks` + `/api/v2/tasks/:id` → `handlers_tasks.go:108/186/242` | （重复于 §1） | source-inspected / mock-only |
| façade approvals | `POST /api/v2/approvals/:gate_id/decision` → `handlers_approvals.go:27` | **不存在** | source-inspected / mock-only |
| façade memory search | `POST /api/v2/memory/search` → `handlers_memory.go:139` | **不存在** | source-inspected / mock-only |
| OpenCode control plane | **不存在**（RedClaw 无） | `PUT/DELETE /auth/:providerID` + `POST /log` → `control.ts:39/51/62` | source-inspected（仅 OpenCode） |

### 子任务 C 状态值定义

- `source-inspected`：端点路径、method、handler、schema 已逐行从源码确认。
- `mock-only`：端点存在但默认 mock（如 `BackendMock` 的 façade、real 模式固定 503 的 runs/events/notifications）。
- `planned`：v3 文档规划但源码未实现（如 `/api/v1/devices`、`/api/v1/pods`、`/api/v1/agents`）。
- `blocked`：因 RedClaw 端不存在或前置依赖未满足，禁止启用，必须降级为 unavailable 响应。

### 子任务 C 证据数（截至 2026-08-23）

| 类别 | 数量 |
|---|---|
| RedClaw platform-go 端点（source-inspected） | **35** |
| OpenCode runtime 端点（source-inspected） | **35** |
| Mapping matrix 总行数 | **45** |
| 其中 `source-inspected` | **41** |
| 其中 `mock-only` | **5** |
| 其中 `planned` | **0** |
| 其中 `blocked`（路由级） | **13** |

### 子任务 C 交付清单

| 类别 | 文件 | 状态 |
|---|---|---|
| 映射矩阵 | `docs/新架构v1/04-contracts/redclaw-to-opencode.md` | source-inspected（路径 + handler） |
| Connector vs IDE Adapter 边界 | `redclaw-to-opencode.md §5` | source-inspected（基于 `connectors.go` 真实范围） |
| 不可用端点清单 + 降级响应模板 | `redclaw-to-opencode.md §4` | source-inspected（含 503/projection_unavailable 实际代码路径） |
| 状态矩阵（本文件 §C） | `docs/新架构v1/04-contracts/_status.md` | source-inspected |

### 最近一次变更

- 2026-08-23：子任务 C 首版（RedClaw ↔ OpenCode 映射矩阵 + 状态标签）。
