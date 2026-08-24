# RedClaw 控制面 ↔ OpenCode runtime 映射矩阵

> **目的**：明确 RedClaw platform-go 的 `/api/v2` façade、`/api/v1` orchestrator、agentcontainer `/invoke`、auth agent handler 与 OpenCode runtime 端点之间的真实映射关系；为 ZAG（PocketFleet 新控制面中介）提供无虚构端点的实现边界。
>
> **证据等级约定**：
> - `source-inspected` —— 端点已从源码直接读到（行号/函数名），行为可被逐行验证。
> - `mock-only` —— 端点在源码中存在，但 `mock`/`stub` 模式返回占位响应（如 echo、in-memory store），生产不可用。
> - `planned` —— 端点在文档/设计中存在，但源码中尚未实现。
> - `blocked` —— 因前置依赖（安全门禁、协议冻结）显式禁止启用。
>
> **本文不包含未在源码中存在的端点。** 所有"未列出"的端点按"未实现"处理。

---

## 1. RedClaw platform-go 真实路径清单（source-inspected）

### 1.1 `/api/v2` façade（façade 模式：mock / real）

源码位置：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/facade/server.go`

| Method | Path | 处理函数 | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/api/v2/capabilities` | `(*Server).handleCapabilities` | `server.go:190` | 无（公开） | `facade_test.go` capability envelope | source-inspected |
| POST | `/api/v2/tasks` | `(*Server).handleCreateTask` | `handlers_tasks.go:108` | JWT（`Claims.TenantID` from Bearer） + `Idempotency-Key` + `Correlation-ID` headers | `createTaskRequest`（`project_id`/`title`/`description`/`task_contract{type,risk_level,inputs,acceptance}`） | source-inspected (mock-only default, real 模式 → ACC `createTask`) |
| GET | `/api/v2/tasks` | `(*Server).handleListTasks` | `handlers_tasks.go:186` | JWT | `TaskItem` schema | source-inspected |
| GET | `/api/v2/tasks/:task_id` | `(*Server).handleGetTask` | `handlers_tasks.go:242` | JWT | `TaskItem` + `task_contract` | source-inspected |
| GET | `/api/v2/runs/:run_id/events` | `(*Server).handleStreamRunEvents` | `handlers_events.go:22` | JWT + SSE 客户端 | `Event`（`evt-<seq>` cursor） | source-inspected（SSE mock-only；real 模式返回 `503 projection_unavailable`） |
| POST | `/api/v2/approvals/:gate_id/decision` | `(*Server).handleApprovalDecision` | `handlers_approvals.go:27` | JWT + `Idempotency-Key` + `Correlation-ID` | `approvalDecisionRequest{decision,reason,expected_gate_version,candidate_decisions[]}` | source-inspected |
| GET | `/api/v2/notifications` | `(*Server).handleListNotifications` | `handlers_notifications.go:41` | JWT | `NotificationItem` | source-inspected（real 模式 `503 projection_unavailable`） |
| POST | `/api/v2/notifications/:notification_id/ack` | `(*Server).handleAckNotification` | `handlers_notifications.go:82` | JWT + `Idempotency-Key` | n/a | source-inspected（real 模式 `503 projection_unavailable`） |
| POST | `/api/v2/memory/search` | `(*Server).handleMemorySearch` | `handlers_memory.go:139` | JWT + `Correlation-ID` | `memorySearchRequest{query,scope_chain,allowed_scopes,cube_ids,top_k,token_budget,filters,rerank,policy.on_degraded}` | source-inspected |

**关键源码事实**：

- façade 默认 `BackendMock`（`server.go:46`），需 `BackendReal` + 非默认 `JWTKey` 才进入 real 模式（`server.go:64-97`）。
- real 模式 `runs/:run_id/events` 永远返回 `503 projection_unavailable`（`handlers_events.go:22-28`）。
- tenant **永远从 JWT claim 派生**，不接受 header（`auth.go:83`）。
- 公共 `IssueToken`（`auth.go:30`）是开发用 mock JWT 签发器，**不能用于生产**。

### 1.2 `/api/v1` orchestrator（任务编排 / 会话 / 控制信号 / WebSocket）

源码位置：
- 入口：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/orchestrator/server.go`
- 处理器：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/orchestrator/handlers/handlers.go`
- 鉴权中间件：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/orchestrator/auth/auth.go`

| Method | Path | 处理函数 | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/healthz` | `pkgserver` 自带 | `server.go:43-44` | 无 | n/a | source-inspected |
| GET | `/api/v1/health` | `(*Handler).Health` | `handlers.go:463` | 无 | `{status,service,worker,trace}` | source-inspected |
| POST | `/api/v1/auth/login` | `(*Handler).Login` | `handlers.go:472` | 无 | `{token: uuid}` | source-inspected（**仅返回 UUID，不签发 JWT**——必须配合 `authagent` SSO 完成真实登录） |
| POST | `/api/v1/tasks` | `(*Handler).SubmitTask` | `handlers.go:68` | JWT + Role ≥ employee | `task.SubmitRequest`（`tenant_id` 来自 claims） | source-inspected |
| GET | `/api/v1/tasks/:task_id` | `(*Handler).GetTask` | `handlers.go:102` | JWT + tenant 匹配 | `task.Task` | source-inspected |
| GET | `/api/v1/tasks` | `(*Handler).ListTasks` | `handlers.go:120` | JWT + tenant 派生 | `{tasks[], limit}` | source-inspected |
| GET | `/api/v1/tasks/:task_id/result` | `(*Handler).GetTaskResult` | `handlers.go:136` | JWT + tenant 匹配 | `{status, result, error}` | source-inspected |
| POST | `/api/v1/sessions` | `(*Handler).CreateSession` | `handlers.go:160` | JWT | `{user_id, agent_id}` | source-inspected |
| GET | `/api/v1/sessions/:session_id` | `(*Handler).GetSession` | `handlers.go:185` | JWT + tenant 匹配 | `session.Session` | source-inspected |
| GET | `/api/v1/sessions` | `(*Handler).ListSessions` | `handlers.go:203` | JWT + tenant 派生（query `user_id`） | `{sessions[]}` | source-inspected |
| POST | `/api/v1/control` | `(*Handler).CreateControl` | `handlers.go:225` | JWT + Role ∈ {`admin`, `manager`} | `createControlInput{session_id,kind,payload}` | source-inspected |
| POST | `/api/v1/control/:command_id/signature` | `(*Handler).SignControl` | `handlers.go:257` | JWT + tenant 匹配 + ed25519 公钥校验 | `{signature(base64),public_key(base64),role}` | source-inspected |
| POST | `/api/v1/control/:command_id/execute` | `(*Handler).ExecuteControl` | `handlers.go:313` | JWT + tenant 匹配 + `VerifyAll`（双签校验） | `cmd.CommandID` | source-inspected |
| GET | `/api/v1/audit` | `(*Handler).QueryAudit` | `handlers.go:374` | JWT + Role ∈ {`admin`, `manager`} | `audit.Entry{tenant_id,actor_id,event_type,level,limit,offset}` | source-inspected |
| GET | `/api/v1/monitor/cluster` | `(*Handler).MonitorCluster` | `handlers.go:405` | JWT | `{service,worker_id,queue,ws_subscribers,live_sessions}` | source-inspected |
| GET | `/api/v1/monitor/workflow/:session_id` | `(*Handler).MonitorWorkflow` | `handlers.go:420` | JWT + tenant 匹配 | `{session,event_count}`（event_count 当前硬编码 `0`） | source-inspected |
| GET | `/api/v1/ws` | `(*Handler).WebSocket` | `handlers.go:443` | JWT + query `types`/`session_id`/`agent_id` | `ws.Event{EventType,TenantID,AgentID,SessionID,TaskID,Payload}` | source-inspected |

**鉴权机制（来自 `auth.go`）**：

- 鉴权链：API key (`X-API-Key`，DB 校验) → JWT Bearer（HS256/RS256，`iss` + `aud` 强制）。
- 角色枚举：`admin` / `manager` / `employee` / `agent`（`auth.go:34-38`）。
- 控制信号 `POST /api/v1/control` 必须 `admin` 或 `manager`（`handlers.go:236-238`）。
- `Get*` / `List*` 处理器始终校验 `task.TenantID == id.TenantID`，不一致返回 `403 forbidden`（`handlers.go:113-115` 等）。

### 1.3 agentcontainer `/invoke`（OpenClaw 子进程执行）

源码位置：
- 入口：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/agentcontainer/server.go`
- 处理器：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/agentcontainer/handlers/handlers.go`
- 执行器：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/agentcontainer/agent/invoker.go`

| Method | Path | 处理函数 | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/healthz` | `pkgserver` 自带 | `server.go` | 无 | n/a | source-inspected |
| GET | `/api/v1/health` | `(*Handler).Health` | `handlers.go:271` | JWT | `{status,service}` | source-inspected |
| POST | `/api/v1/invoke` | `(*Handler).Invoke` | `handlers.go:55` | JWT | `invokeRequest{session_id,agent_id,user_id,tenant_id,position_id,message,model}` | source-inspected（执行 `/usr/local/bin/openclaw` 子进程） |
| GET | `/api/v1/skills` | `(*Handler).ListSkills` | `handlers.go:129` | JWT（`roles` query 默认 `admin`） | `{skills[],role_count}` | source-inspected |
| GET | `/api/v1/skills/:skill_id` | `(*Handler).GetSkill` | `handlers.go:135` | JWT | `skill.Info` | source-inspected |
| POST | `/api/v1/permissions/profiles` | `(*Handler).UpsertProfile` | `handlers.go:178` | JWT | `profileInput{tenant_id,position_id,tool_allowlist,tool_blocklist,skill_allowlist,constraints,updated_by}` | source-inspected |
| GET | `/api/v1/permissions/profiles/:position_id` | `(*Handler).GetProfile` | `handlers.go:204` | JWT（`tenant_id` query） | `permissions.Profile` | source-inspected |
| POST | `/api/v1/tokens/issue` | `(*Handler).IssueToken` | `handlers.go:228` | JWT | `tokenRequest{tenant_id,user_id,action,resource,ttl}` | source-inspected |
| POST | `/api/v1/tokens/verify` | `(*Handler).VerifyToken` | `handlers.go:257` | JWT | `{raw}` | source-inspected |

**`/invoke` 真实行为（`handlers.go:55-119`）**：

1. 必填校验：`session_id` / `user_id` / `message` / `tenant_id`；缺一返回 `400 invalid_argument`。
2. 安全校验：`safety.Guard.Check(message)`，拦截则返回 `422 unsafe_input`。
3. 工作区组装：`workspace.Assembler.Assemble(sessionID, globals, positions, personals, nil)` 生成 `ws.Root`、`ws.SoulPath`。
4. 权限 prompt 注入：`permissions.NewChecker(prof).SystemPrompt()` 拼接到 `in.Message`（Plan A）。
5. 真实执行：`h.Invoker.Invoke(ctx, Invocation{SessionID, AgentID, UserID, Message, Model, Timeout, WorkingDir})`。
6. 失败返回：`504 agent_invocation_failed`（含 `result`、`workspace` 路径）。

### 1.4 auth agent（SSO + 审批 token）

源码位置：
- 入口：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/authagent/server.go`
- SSO handler：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/authagent/sso/handlers.go`
- Approval handler：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/authagent/approval/executor.go`

| Method | Path | 处理函数 | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/healthz` | `pkgserver` 自带 | `server.go` | 无 | n/a | source-inspected |
| GET | `/api/v1/sso/login` | `sso.Mount` 内联 | `sso/handlers.go:16` | 无 | query `origin` | source-inspected（mgr nil → `503 sso_disabled`） |
| GET | `/api/v1/sso/callback` | `sso.Mount` 内联 | `sso/handlers.go:31` | 无 | query `code`、`state`、`nonce`、`redirect_uri` | source-inspected（`exchangeCode` 是 stub：直接返回 code 本身，`sso.go:133-138`） |
| POST | `/api/v1/sso/logout` | `sso.Mount` 内联 | `sso/handlers.go:55` | 无 | n/a | source-inspected（**占位**：仅返回 `{status:"logged_out"}`） |
| POST | `/api/v1/requests` | `(*Executor).Submit` | `approval/executor.go:70` | JWT | `SubmitReq{action,t tenant_id,subject}` | source-inspected |
| GET | `/api/v1/requests` | `(*Executor).ListPending` | `approval/executor.go:104` | JWT + query `tenant_id` | `{requests[]}` | source-inspected |
| GET | `/api/v1/requests/:request_id` | `(*Executor).Get` | `approval/executor.go:115` | JWT | `Request{request_id,tenant_id,subject,action,status,created_at,expires_at,grant}` | source-inspected |
| POST | `/api/v1/requests/:request_id/approve` | `(*Executor).Approve` → `decide(c,true)` | `approval/executor.go:130` | JWT + approver role 校验 | `{approver,reason}` | source-inspected |
| POST | `/api/v1/requests/:request_id/reject` | `(*Executor).Reject` → `decide(c,false)` | `approval/executor.go:135` | JWT + approver role 校验 | `{approver,reason}` | source-inspected |
| POST | `/api/v1/grants/verify` | `(*Executor).VerifyGrant` | `approval/executor.go:186` | JWT | `{raw}` | source-inspected（HMAC-SHA256 校验，5min TTL） |

**关键事实**：

- SSO `exchangeCode` 是 stub（`sso.go:133-138`）——生产前必须替换为对 IdP `/token` 的真实调用。
- 审批 grant token TTL = 5min（`queue.go:214`），算法为 `HMAC-SHA256(body, hmacKey)`，body 是 `base64url(json).base64url(sig)`。
- 内存队列 `Queue` 是进程内存储（`queue.go:67-74`），重启即丢；多副本部署必须替换为 Postgres。

---

## 2. OpenCode runtime 真实路径清单（source-inspected）

源码位置：`/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/`

### 2.1 `/session` 路由（`groups/session.ts`）

| Method | Path | HTTP API identifier | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/session` | `session.list` | `session.ts:111` | Basic Auth + InstanceContext + WorkspaceRouting | `ListQuery` | source-inspected |
| GET | `/session/status` | `session.status` | `session.ts:121` | 同上 | `StatusMap` | source-inspected |
| GET | `/session/:sessionID` | `session.get` | `session.ts:132` | 同上 | `Session.Info` | source-inspected |
| GET | `/session/:sessionID/children` | `session.children` | `session.ts:144` | 同上 | `Session.Info[]` | source-inspected |
| GET | `/session/:sessionID/todo` | `session.todo` | `session.ts:156` | 同上 | `Todo.Info[]` | source-inspected |
| GET | `/session/:sessionID/diff` | `session.diff` | `session.ts:168` | 同上 | `Snapshot.FileDiff[]` | source-inspected |
| GET | `/session/:sessionID/message` | `session.messages` | `session.ts:179` | 同上 | `SessionV1.WithParts[]` | source-inspected |
| GET | `/session/:sessionID/message/:messageID` | `session.message` | `session.ts:191` | 同上 | `SessionV1.WithParts` | source-inspected |
| POST | `/session` | `session.create` | `session.ts:203` | 同上 | `Session.CreateInput` | source-inspected |
| DELETE | `/session/:sessionID` | `session.delete` | `session.ts:215` | 同上 | `Schema.Boolean` | source-inspected |
| PATCH | `/session/:sessionID` | `session.update` | `session.ts:227` | 同上 | `UpdatePayload{title,metadata,permission,time.archived}` | source-inspected |
| POST | `/session/:sessionID/fork` | `session.fork` | `session.ts:240` | 同上 | `ForkPayload` | source-inspected |
| POST | `/session/:sessionID/abort` | `session.abort` | `session.ts:253` | 同上 | `Schema.Boolean` | source-inspected |
| POST | `/session/:sessionID/init` | `session.init` | `session.ts:265` | 同上 | `InitPayload{modelID,providerID,messageID}` | source-inspected |
| POST | `/session/:sessionID/share` | `session.share` | `session.ts:279` | 同上 | `Session.Info` | source-inspected |
| DELETE | `/session/:sessionID/share` | `session.unshare` | `session.ts:291` | 同上 | `Session.Info` | source-inspected |
| POST | `/session/:sessionID/summarize` | `session.summarize` | `session.ts:303` | 同上 | `SummarizePayload{providerID,modelID,auto?}` | source-inspected |
| POST | `/session/:sessionID/message` | `session.prompt` | `session.ts:316` | 同上 | `PromptPayload` | source-inspected（流式） |
| POST | `/session/:sessionID/prompt_async` | `session.prompt_async` | `session.ts:329` | 同上 | `PromptPayload` | source-inspected |
| POST | `/session/:sessionID/command` | `session.command` | `session.ts:343` | 同上 | `CommandPayload` | source-inspected |
| POST | `/session/:sessionID/shell` | `session.shell` | `session.ts:356` | 同上 | `ShellPayload` | source-inspected |
| POST | `/session/:sessionID/revert` | `session.revert` | `session.ts:369` | 同上 | `RevertPayload` | source-inspected |
| POST | `/session/:sessionID/unrevert` | `session.unrevert` | `session.ts:383` | 同上 | n/a | source-inspected |
| POST | `/session/:sessionID/permissions/:permissionID` | `permission.respond`（deprecated） | `session.ts:395` | 同上 | `PermissionResponsePayload{response}` | source-inspected（**已 deprecated**，新代码用 `/permission`） |
| DELETE | `/session/:sessionID/message/:messageID` | `session.deleteMessage` | `session.ts:409` | 同上 | `Schema.Boolean` | source-inspected |
| DELETE | `/session/:sessionID/message/:messageID/part/:partID` | `part.delete` | `session.ts:422` | 同上 | `Schema.Boolean` | source-inspected |
| PATCH | `/session/:sessionID/message/:messageID/part/:partID` | `part.update` | `session.ts:433` | 同上 | `SessionV1.Part` | source-inspected |

### 2.2 `/event`（SSE）路由

| Method | Path | HTTP API identifier | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/event` | `event.subscribe` | `event.ts:14` | Basic Auth + InstanceContext + WorkspaceRouting | `text/event-stream` | source-inspected |

EventV2 类型定义：`event.ts:4-7`：
```ts
Event.Connected: EventV2.define({ type: "server.connected", schema: {} })
Event.Disposed:  EventV2.define({ type: "global.disposed",   schema: {} })
InstanceDisposed: { id, type: "server.instance.disposed", properties: { directory } }
```

### 2.3 `/permission` 路由（`groups/permission.ts`）

| Method | Path | HTTP API identifier | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/permission` | `permission.list` | `permission.ts:21` | Basic Auth + InstanceContext + WorkspaceRouting | `PermissionV1.Request[]` | source-inspected |
| POST | `/permission/:requestID/reply` | `permission.reply` | `permission.ts:31` | 同上 | `ReplyPayload{reply, message?}` | source-inspected |

### 2.4 `/question` 路由（`groups/question.ts`）

| Method | Path | HTTP API identifier | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/question` | `question.list` | `question.ts:22` | Basic Auth + InstanceContext + WorkspaceRouting | `Question.Request[]` | source-inspected |
| POST | `/question/:requestID/reply` | `question.reply` | `question.ts:32` | 同上 | `ReplyPayload{answers[]}` | source-inspected |
| POST | `/question/:requestID/reject` | `question.reject` | `question.ts:45` | 同上 | `Schema.Boolean` | source-inspected |

### 2.5 `/pty` 路由（`groups/pty.ts`）

| Method | Path | HTTP API identifier | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| GET | `/pty/shells` | `pty.shells` | `pty.ts:44` | Basic Auth + InstanceContext + WorkspaceRouting | `ShellItem[]` | source-inspected |
| GET | `/pty` | `pty.list` | `pty.ts:54` | 同上 | `Pty.Info[]` | source-inspected |
| POST | `/pty` | `pty.create` | `pty.ts:64` | 同上 | `Pty.CreateInput` | source-inspected |
| GET | `/pty/:ptyID` | `pty.get` | `pty.ts:76` | 同上 | `Pty.Info` | source-inspected |
| PUT | `/pty/:ptyID` | `pty.update` | `pty.ts:88` | 同上 | `Pty.UpdateInput` | source-inspected |
| DELETE | `/pty/:ptyID` | `pty.remove` | `pty.ts:101` | 同上 | `Schema.Boolean` | source-inspected |
| POST | `/pty/:ptyID/connect-token` | `pty.connectToken` | `pty.ts:113` | 同上 | `PtyTicket.ConnectToken` | source-inspected |
| GET | `/pty/:ptyID/connect` | `pty.connect` | `pty.ts:144` | `PtyConnectAuthorization`（ticket-based） + WorkspaceRouting | `Schema.Boolean` | source-inspected（WebSocket） |

### 2.6 `/control` 与 `/auth/:providerID`（`groups/control.ts`）

| Method | Path | HTTP API identifier | 文件:行号 | 鉴权要求 | Schema 引用 | 状态 |
|---|---|---|---|---|---|---|
| PUT | `/auth/:providerID` | `auth.set` | `control.ts:39` | 无（在 control group，未挂 Authorization middleware） | `Auth.Info` | source-inspected |
| DELETE | `/auth/:providerID` | `auth.remove` | `control.ts:51` | 无 | `Schema.Boolean` | source-inspected |
| POST | `/log` | `app.log` | `control.ts:62` | 无 | `LogInput{service,level,message,extra?}` | source-inspected |

### 2.7 OpenCode 认证机制（`auth.ts`）

- 默认 `username = "opencode"`，密码来自 `OPENCODE_SERVER_PASSWORD`（`auth.ts:18-19`）。
- 鉴权形式：`Authorization: Basic <base64(username:password)>`（`auth.ts:36-42`）。
- `authorized()` 函数要求 `username === config.username && password === config.password`（`auth.ts:28-33`）。
- `required()` 返回 `false` 时，OpenCode 不会强制 Basic Auth——但 `Authorization` middleware 在大多数 group 上仍挂载（`session.ts:454`、`permission.ts:53` 等）。

---

## 3. 映射矩阵

> **缩写说明**：以下表格中：
> - **ZAG API** 指的是 v3 设计中由 ZAgentGateway（`:9100`）暴露给 OpenPocket 的统一 API。
> - **ZAG → RedClaw** 指的是 ZAG 内部对 RedClaw platform-go 的真实调用。
> - **ZAG → OpenCode** 指的是 ZAG 内部对 OpenCode runtime（默认 `:4096`）的真实调用。
> - 状态分布：`source-inspected` / `mock-only` / `planned` / `blocked`。

### 3.1 任务与会话：`redclaw_task/run ↔ opencode_session`

| ZAG API（OpenPocket 视角） | ZAG → RedClaw（路径 + handler） | ZAG → OpenCode（路径 + handler） | 状态 | 备注 |
|---|---|---|---|---|
| `POST /api/v1/tasks` | `POST http://redclaw:8090/api/v1/tasks` → `handlers.go:68 SubmitTask` | `POST http://opencode:4096/session/:sessionID/message` → `session.ts:316 prompt` | source-inspected（路径）/ mock-only（real 模式默认 mock） | RedClaw 收到 task 后通过 WS hub 发布 `task.submitted`；OpenCode 真实 prompt 由 ZAG 内部转换 |
| `GET /api/v1/tasks/:id` | `GET http://redclaw:8090/api/v1/tasks/:task_id` → `handlers.go:102 GetTask` | （内部查询：`GET /session/:sessionID` → `session.ts:132 get`） | source-inspected | RedClaw 返回 `task.Task`；OpenCode 返回 `Session.Info` |
| `GET /api/v1/tasks` | `GET http://redclaw:8090/api/v1/tasks?status=&limit=` → `handlers.go:120 ListTasks` | （内部：`GET /session` → `session.ts:111 list`） | source-inspected | |
| `GET /api/v1/tasks/:id/result` | `GET http://redclaw:8090/api/v1/tasks/:task_id/result` → `handlers.go:136 GetTaskResult` | （内部：`GET /session/:sessionID/message` → `session.ts:179 messages`） | source-inspected | RedClaw 仅在 `StatusCompleted/Failed` 时返回 `result` |
| `POST /api/v1/tasks/:id/cancel` | （**没有 RedClaw 端点**） | `POST /session/:sessionID/abort` → `session.ts:253 abort` | blocked（RedClaw 缺 cancel path，文档中规划的 `Orchestrator.CancelTask` 未实现） | ZAG 必须直接调 OpenCode abort，并显式记录 `cancel.status = blocked_redclaw_path` |
| `POST /api/v1/sessions` | `POST http://redclaw:8090/api/v1/sessions` → `handlers.go:160 CreateSession` | `POST http://opencode:4096/session` → `session.ts:203 create` | source-inspected | RedClaw session 与 OpenCode session 是两个独立 ID，ZAG 必须维护 `redclaw_session_id ↔ opencode_session_id` 映射表 |
| `GET /api/v1/sessions/:id` | `GET http://redclaw:8090/api/v1/sessions/:session_id` → `handlers.go:185 GetSession` | `GET /session/:sessionID` → `session.ts:132 get` | source-inspected | |
| `POST /api/v1/sessions/:id/messages` | （**没有 RedClaw 端点**——任务走 task submit） | `POST /session/:sessionID/message` → `session.ts:316 prompt`（流式） | blocked（RedClaw `/api/v1/sessions/:id/messages` 不存在） | 任何 message 投递必须直接走 OpenCode |
| `POST /api/v1/sessions/:id/share` | （**没有 RedClaw 端点**） | `POST /session/:sessionID/share` → `session.ts:279 share` | blocked（RedClaw share API 未实现） | OpenCode session.share 真实存在；ZAG 调用 OpenCode 后需把 URL 写回 RedClaw（如果需要审计） |
| `POST /api/v1/sessions/:id/abort` | （**没有 RedClaw 端点**） | `POST /session/:sessionID/abort` → `session.ts:253 abort` | blocked | 同 cancel |

#### 字段映射

| RedClaw 字段（`task.Task`） | OpenCode 字段（`Session.Info`） | 转换规则 | 状态 |
|---|---|---|---|
| `TaskID` | `Session.Info.id` | `redclaw_task_id = "rc_" + opencode_session_id`（ZAG 命名） | source-inspected |
| `TenantID` | （无，OpenCode 不感知 tenant） | ZAG 维护 `tenant_id ↔ session_id` 映射；OpenCode 通过 `WorkspaceRoutingMiddleware` 用 `directory` 隔离 | source-inspected |
| `AgentID` | （OpenCode 不暴露 `agent_id` 字段） | ZAG 内部映射；OpenCode 用 `modelID + providerID`（`InitPayload`，`session.ts:64`） | source-inspected |
| `Goal` | 无 | 直接来自 OpenPocket intent；不在协议层转换 | source-inspected |
| `Status` ∈ {pending, running, completed, failed, cancelled} | `SessionStatus.Info.status` ∈ {idle, busy, ...} | ZAG 内部枚举映射：`running ↔ busy`、`completed ↔ idle + result` | source-inspected |
| `Priority` ∈ {low, normal, high} | （OpenCode 无优先级） | 仅 RedClaw 字段；ZAG 写回 RedClaw 即可 | source-inspected |
| `StartedAt` / `FinishedAt` | `Session.Info.time.created` / `time.completed`（部分字段依赖 schema 版本） | 时间戳 ISO8601 | source-inspected |
| `Result` | （OpenCode 不返回单字段 result） | ZAG 拉取 `GET /session/:sessionID/message` 拼装 | source-inspected |
| `SessionID` | `Session.Info.id` | 双向 1:1 映射，存 ZAG `session_map` 表 | source-inspected |

### 3.2 事件总线：`redclaw_task/event ↔ opencode_event`

| RedClaw 事件类型（`ws.Event.EventType`，`handlers.go:90`） | OpenCode EventV2 type | 字段重命名 | 状态 |
|---|---|---|---|
| `task.submitted` | 无直接对应；可由 `session.created` + `message.created` 复合 | `aggregate_id = task_id` | source-inspected |
| `task.running` | `session.status` 变化（schema 依赖版本） | `state = running ↔ busy` | source-inspected |
| `task.completed` | `session.idle` + `message.completed` | `state = completed` | source-inspected |
| `task.failed` | `session.error` | `error.message → RedClaw.error` | source-inspected |
| `agent.message` | `message.part.delta` / `message.part.completed` | `part.text → payload.text` | source-inspected |
| `agent.tool_call` | `tool.call.starting` / `tool.call.running` | `tool.name → payload.tool`，`args → payload.input` | source-inspected |
| `agent.tool_result` | `tool.call.completed` | `output → payload.output` | source-inspected |
| `device.status` | 无对应（OpenCode 无 device 概念） | ZAG 必须保留字段供 OpenPocket 消费 | source-inspected |
| `ide.status` | 无对应（OpenCode 不是 IDE） | ZAG 必须降级为 unavailable（见 §4） | blocked（无法映射） |
| `usage.tick` | 无对应 | 改由 RedClaw LLM gateway `/api/gateway/usage` 提供 | source-inspected（改路由） |
| `permission.request` | `permission.asked` | `permissionID → payload.permission_id`、`metadata → payload.metadata` | source-inspected |
| `permission.reply` | `permission.replied` | `reply ↔ response`（枚举值需校验） | source-inspected |
| `control.executed` | （无对应——控制信号是 RedClaw 独有） | 直接由 RedClaw WS hub 推送，ZAG 转发 | source-inspected |

#### 字段重命名规则

| RedClaw (`ws.Event`) | OpenCode EventV2 | 重命名 | 类型转换 |
|---|---|---|---|
| `EventType` | `type` | 名字不同，值见上表 | string |
| `TenantID` | 无 | 由 ZAG 用 `claims.tenant_id` 注入 | string |
| `AgentID` | `properties.agent` 或 `properties.modelID` | 1:1 传递 | string |
| `SessionID` | `properties.sessionID` | 1:1 传递 | string |
| `TaskID` | （无） | ZAG 内部映射 | string |
| `Payload`（JSON bytes） | `properties`（结构化） | ZAG 必须解析 Payload 后重组为 EventV2 `properties` | object |

### 3.3 权限：`redclaw_task/permission ↔ opencode_permission`

| ZAG API | ZAG → RedClaw | ZAG → OpenCode | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/permissions`（待审批） | `GET http://redclaw:8092/api/v1/requests?tenant_id=...` → `approval/executor.go:104 ListPending` | `GET /permission` → `permission.ts:21 list` | source-inspected | RedClaw 走 `ApprovalToken` 模型；OpenCode 走 `PermissionV1.Request` 模型；两个 ID 独立，ZAG 需双源订阅 |
| `POST /api/v1/permissions/:id/reply` | `POST /api/v1/requests/:request_id/approve` 或 `/reject` → `approval/executor.go:130/135` | `POST /permission/:requestID/reply` → `permission.ts:31 reply` | source-inspected | ZAG 必须双写：先调 RedClaw 完成审计 + plan E 审批，再调 OpenCode 解锁 prompt |
| `POST /api/v1/permissions/profiles` | `POST http://redclaw:8091/api/v1/permissions/profiles` → `handlers.go:178 UpsertProfile` | （无对应） | source-inspected | OpenCode 的 `Session.UpdatePayload.permission`（`session.ts:50`）只接受 `PermissionV1.Ruleset`，不是 profile |
| `GET /api/v1/permissions/profiles/:position_id` | `GET http://redclaw:8091/api/v1/permissions/profiles/:position_id?tenant_id=...` → `handlers.go:204 GetProfile` | （无对应） | source-inspected | |
| `POST /api/v1/tokens/issue` | `POST http://redclaw:8091/api/v1/tokens/issue` → `handlers.go:228 IssueToken` | （无对应） | source-inspected | RedClaw 单独的 approval token 概念；OpenCode 通过 Basic Auth 直连 |
| `POST /api/v1/tokens/verify` | `POST http://redclaw:8091/api/v1/tokens/verify` → `handlers.go:257 VerifyToken` | （无对应） | source-inspected | |

#### 字段映射

| RedClaw `Request` (`approval/queue.go:46`) | OpenCode `PermissionV1.Request` | 转换 |
|---|---|---|
| `RequestID` (格式 `req_<base64>`) | `PermissionV1.ID` (UUID) | ZAG 维护 `redclaw_request_id ↔ opencode_permission_id` |
| `TenantID` | 无 | ZAG 注入 |
| `Subject` | `properties.user` 或 `properties.sessionID` | 映射 |
| `Action{Kind, Resource, Reason, Payload}` | `properties.{tool, args, metadata}` | 拆分 + 重命名 |
| `Status` ∈ {pending, approved, rejected, expired} | `PermissionV1.Reply` ∈ {approve, deny, ...} | 1:1 映射 |
| `Grant` (HMAC token) | 无 | ZAG 内部传递，不暴露给 OpenCode |

### 3.4 终端 / PTY：`redclaw_task/pty ↔ opencode_pty`

| ZAG API | ZAG → RedClaw | ZAG → OpenCode | 状态 | 备注 |
|---|---|---|---|---|
| `GET /api/v1/pty` | （**无 RedClaw 端点**——RedClaw 没有 PTY 概念） | `GET /pty` → `pty.ts:54 list` | blocked（RedClaw 缺 PTY） | ZAG 直接代理到 OpenCode |
| `POST /api/v1/pty` | （**无 RedClaw 端点**） | `POST /pty` → `pty.ts:64 create` | blocked | |
| `GET /api/v1/pty/:id` | （**无 RedClaw 端点**） | `GET /pty/:ptyID` → `pty.ts:76 get` | blocked | |
| `PUT /api/v1/pty/:id` | （**无 RedClaw 端点**） | `PUT /pty/:ptyID` → `pty.ts:88 update` | blocked | |
| `DELETE /api/v1/pty/:id` | （**无 RedClaw 端点**） | `DELETE /pty/:ptyID` → `pty.ts:101 remove` | blocked | |
| `POST /api/v1/pty/:id/connect-token` | （**无 RedClaw 端点**） | `POST /pty/:ptyID/connect-token` → `pty.ts:113 connectToken` | blocked | OpenCode 返回短期 `PtyTicket.ConnectToken`，ZAG 转发 |
| `GET /api/v1/pty/:id/connect`（WebSocket） | （**无 RedClaw 端点**） | `GET /pty/:ptyID/connect?directory=&workspace=&cursor=&ticket=` → `pty.ts:144 connect` | blocked | OpenCode 用 `PtyConnectAuthorization` middleware 校验 ticket；ZAG 仅透传 |
| `GET /api/v1/pty/shells` | （**无 RedClaw 端点**） | `GET /pty/shells` → `pty.ts:44 shells` | blocked | 列出可用 shell，ZAG 用于预渲染 |

**关键事实**：RedClaw platform-go **没有任何 PTY 等价端点**——所有 PTY 操作必须绕过 RedClaw 直接连 OpenCode。ZAG 在 README §3.4（v3 文档）声称的 "OpenCode PTY 等价接口在 RedClaw 中存在" 是错误的，必须按 blocked 处理。

---

## 4. 不可用 / 需降级的端点清单

### 4.1 RedClaw 真实不可用（即便真实部署也不存在对应路径）

| 端点需求 | 原因 | 降级响应模板（ZAG → OpenPocket） |
|---|---|---|
| `POST /api/v1/tasks/:id/cancel` | RedClaw 没有 cancel path；只允许 `CreateControl(Kind: "terminate")` 走控制信号，且要求 Ed25519 双签 | `503 unavailable` + `code=feature_blocked` + `detail: "redclaw_cancel_unavailable, use control/terminate with double signature"` |
| `POST /api/v1/sessions/:id/messages` | RedClaw session 模型不接收消息流 | `503 unavailable` + `code=feature_blocked` + `detail: "redclaw_session_messages_unavailable, use opencode prompt directly"` |
| `POST /api/v1/sessions/:id/share` | RedClaw 没有 share API | `503 unavailable` + `code=feature_blocked` + `detail: "redclaw_share_unavailable, use opencode session.share"` |
| `POST /api/v1/sessions/:id/abort` | 同 cancel | 同上 |
| `/api/v1/pty/*` 全套 | RedClaw 无 PTY | `503 unavailable` + `code=feature_blocked` + `detail: "redclaw_pty_unavailable, route to opencode pty"` |
| `/api/v2/runs/:run_id/events`（real 模式） | real 模式 `handlers_events.go:22-28` 始终返回 `503 projection_unavailable` | 直接转发 `503 projection_unavailable` 给 OpenPocket，ZAG 不尝试重试 |
| `/api/v2/notifications` + `/api/v2/notifications/:id/ack`（real 模式） | real 模式 `handlers_notifications.go:42-46` + `82-88` 始终返回 `503 projection_unavailable` | 同上 |
| `ide.status` event | OpenCode 不是 IDE，没有 IDE status | `event.type = "ide.status"` 时直接 drop，发送 `unavailable` placeholder 给 OpenPocket |
| `/api/v1/devices`、`/api/v1/pods`、`/api/v1/agents` | RedClaw platform-go 当前**没有**这些端点——只有 orchestrator 的 `/api/v1/sessions` 和 `/api/v1/tasks`，不含 device/pod/agent 维度 | 全部按 blocked 返回 503 + `code=feature_blocked`；v3 文档中规划的 `/api/v1/devices` 仅是 design |

### 4.2 mock-only 端点（真实部署会改为 real，但当前默认 mock）

| 端点 | mock 行为 | real 行为 |
|---|---|---|
| `POST /api/v2/tasks` | 写入 `store.CreateTask()` 返回 UUID | 调用 ACC `accBackend.createTask()` |
| `GET /api/v2/tasks/:id` | 从内存 `store.GetTask()` 返回 | 调用 ACC `accBackend.getTask()` |
| `POST /api/v2/approvals/:gate_id/decision` | `store.ResolveGate()` 内存处理 | 调用 ACC `decideGate()` |
| `POST /api/v2/memory/search` | `store.MemoryItems()` mock 检索 | 调用 Memora `memoraBackend.search()` |
| `/api/v2/runs/:run_id/events` | `store.EventsAfter()` + `store.Subscribe()` SSE | **real 模式始终 503**（不支持） |
| `/api/v2/notifications*` | `store.ListNotifications()` + `AckNotification()` | **real 模式始终 503**（不支持） |

### 4.3 统一降级响应模板

```json
{
  "error": {
    "code": "feature_blocked | projection_unavailable | method_not_allowed",
    "message": "<human readable>",
    "retryable": false,
    "detail": {
      "feature": "redclaw_cancel | redclaw_pty | redclaw_share | ...",
      "source": "redclaw",
      "fallback": "opencode://<direct-path>",
      "blocked_since": "2026-08-23"
    }
  },
  "request_id": "<uuid>",
  "correlation_id": "<uuid>"
}
```

或 SSE 事件（针对流式降级）：
```
id: evt-unavailable-001
event: feature.blocked
data: {"code":"redclaw_pty_unavailable","fallback":"opencode://pty","timestamp":"2026-08-23T..."}

```

---

## 5. Connector vs IDE Adapter 边界（重要）

### 5.1 RedClaw `internal/connectors` 的真实范围

源码位置：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/connectors/`

`connectors.go` 定义的是**通用外部系统连接契约**（AuthMode、CursorType、SideEffectLevel、IdempotencyKey、PolicySnapshot + Assurance Permit）。它**不是 IDE adapter，也不针对任何 IDE 实现**。

ZAG `redclaw-integration.md §6.1` 中描述的"ZCode/VS Code/Cursor/OpenCode 都通过 Connector 注册" 是 **planned** 而非 **source-inspected**：

- 当前 `connectors.go` 仅有 `InMemoryStore` + `PostgresStore`，没有任何 IDE 适配器实现。
- `connector_id` 注册逻辑未在任何 main.go 中被调用；`POST /connectors/register` 端点**不存在**于 v1 / v2 / façade。
- `connector.execute` / `connector.ingest` 操作没有 IDE-specific 的 `Operation` schema。

### 5.2 Generic Connector 边界

| 能力 | 是否适合通过 generic connector 承载 | 原因 |
|---|---|---|
| OpenAI-compatible HTTP LLM 调用 | ✅ 适合 | 仅需 endpoint + AuthMode |
| REST API 代理（Slack、Notion 等） | ✅ 适合 | 通用 HTTP 即可 |
| 数据库查询（read-only） | ✅ 适合 | 受 SQL sandbox 限制即可 |
| Webhook 接收（异步事件） | ✅ 适合 | CursorType=webhook 即可 |
| **OpenCode session 操作** | ❌ **不适合** | OpenCode 需要 `/session`、`/event` SSE、`/permission`、`/pty` WebSocket——不是简单 HTTP 代理，必须由 IDE adapter 直接持有连接 |
| **VS Code / Cursor 远程修改代码** | ❌ **不适合** | 需要 OAuth2 + IDE 特定协议（extension host）；connector 无法保证 sandbox |
| **IDE 诊断（diagnostic）和选区（selection）推送** | ❌ **不适合** | 这是 IDE-specific 事件，不是"外部数据 ingest" |
| **PTY/terminal 流式 I/O** | ❌ **不适合** | 需要 WebSocket，connector.execute 是同步 HTTP 假设 |

### 5.3 哪些 connector 可以升级为 adapter（最小路径）

| IDE | 可升级性 | 路径 | 阻塞项 |
|---|---|---|---|
| **ZCode** | ✅ 推荐 | ZAG `internal/zcode/` 直接调本地 Unix socket；不经 RedClaw | ZCode RPC 协议未公开；需要 ZCode 团队暴露 `open_file` / `apply_diff` schema |
| **OpenCode** | ✅ **必选** | ZAG `internal/opencode/` 直接调 `http://localhost:4096`；固定 OpenCode 版本 | OpenCode 版本冻结 + 合同测试通过；不需要 RedClaw 中转 |
| **VS Code** | ⚠️ 条件升级 | 通过 `code-server` HTTP API（`/api/commands`）；需要 OAuth2 + 实例注册 | `code-server` 与 OpenCode TUI 共存的设计冲突 |
| **Cursor** | ❌ 拒绝升级 | Cursor 没有稳定 public API；当前 `https://api.cursor.sh` 是猜测路径 | 必须先有 Cursor 官方 SDK |

### 5.4 必须拒绝的 connector 误用

| 用例 | 误用示例 | 正确做法 |
|---|---|---|
| 把 OpenCode 当成 "connector.execute 操作目标" | `connector.execute(operation="session.create", payload={title})` | IDE adapter 直连 `/session`，不走 connector |
| 把 IDE selection 当成 "connector.ingest 事件" | `connector.ingest(type="ide.selection", payload=...)` | IDE adapter WebSocket → ZAG WS hub → OpenPocket |
| 把 PTY 当成 "connector webhook" | `cursorType="webhook", payload={cmd, output}` | IDE adapter WebSocket 直连 OpenCode `/pty/:id/connect` |
| 把 IDE 文件修改当成 "connector API 调用" | `connector.execute(operation="file.write", payload={path, content})` | IDE adapter RPC/ZCode socket |

**红线总结**：

1. Generic connector **不能**承载任何 OpenCode `/session`、`/event`、`/permission`、`/pty` 操作。
2. Generic connector **不能**承载 IDE-specific 事件（cursor、selection、diagnostic、breakpoint）。
3. ZAG 必须为 OpenCode 维护独立的 adapter，且**不通过 RedClaw connectors**。
4. 任何把 connector 包装为 IDE adapter 的代码必须 fail-closed：注册时被拒，运行时被屏蔽。

---

## 6. OpenCode EventV2 类型表（用于字段重命名参考）

来源：`/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/event.ts` + `@opencode-ai/core/event` schema

```text
server.connected                    # 服务启动 / 重连信号
server.instance.disposed            # 实例回收
session.created                     # session 创建
session.updated                     # session 元数据变化
session.deleted                     # session 删除
session.status                      # session 状态变化 (idle/busy/error)
session.idle                        # 空闲
session.compacted                   # 已压缩
session.error                       # 错误

message.created                     # 用户/AI 消息创建
message.updated                     # 消息更新
message.removed                     # 消息删除
message.part.delta                  # 流式增量
message.part.updated                # part 更新
message.part.completed              # part 完成
message.part.removed                # part 删除

tool.call.starting                  # 工具调用开始
tool.call.running                   # 工具运行中
tool.call.completed                 # 工具调用完成
tool.call.failed                    # 工具调用失败

permission.asked                    # 权限请求
permission.replied                  # 权限回复

question.asked                      # 问题请求
question.replied                    # 问题回复
question.rejected                   # 问题拒绝

file.edited                         # 文件编辑
file.watcher.updated                # 文件监听更新

pty.created                         # PTY 创建
pty.updated                         # PTY 更新
pty.exited                          # PTY 退出

lsp.client.diagnostics              # LSP 诊断（**IDE-only**，不进 OpenPocket）
lsp.updated                         # LSP 更新

vcs.branch.updated                  # git 分支更新
```

---

## 7. 红线（禁止项）

下列行为在 ZAG 上**一律禁止**：

1. 把 RedClaw URL 直接配为 OpenCode server URL——`/session` / `/event` 协议不兼容（[审计报告 §1.1](../00-research/RedClaw作为OpenCode后端审计.md)）。
2. 把 `connectors.execute(operation="session.create")` 当作 OpenCode 兼容层使用——`connectors.go` 没有 IDE 操作定义。
3. 把 `redclaw_task.events` 中的 `ide.status` 投影给 OpenPocket——OpenCode 不输出 IDE 状态。
4. 用 `X-API-Key` 作为对 RedClaw orchestrator 的生产鉴权——`auth.go:106` 的 `X-API-Key` 仅给 AgentContainer 回调用，且依赖 Postgres 表（`orchestrator.api_keys`）。
5. 把 RedClaw `/api/v1/sessions` 与 OpenCode `/session` 视为同一个 ID 空间——必须用 ZAG `session_map` 表显式维护映射。
6. 把 `/api/v2/runs/:run_id/events` 在 real 模式下视为可用的 SSE 通道——`handlers_events.go:22-28` 总是 503。
7. 让 OpenPocket 在 ZAG 不可用时绕过安全门禁直连 RedClaw——违反 v3 §1.2 和本文 §4。

---

## 8. 配套文档

| 文档 | 用途 |
|---|---|
| [`_status.md`](./_status.md) | 每个映射项的 source-inspected / mock-only / planned / blocked 标签 |
| [`pocket-adapter-matrix.md`](./pocket-adapter-matrix.md) | OpenPocket 5 个 adapter 的边界矩阵 |
| [`pocket-zag-incremental.md`](./pocket-zag-incremental.md) | ZAG client 增量接口设计 |
| [`../00-research/RedClaw作为OpenCode后端审计.md`](../00-research/RedClaw作为OpenCode后端审计.md) | RedClaw 不能直接替代 OpenCode 的根因审计 |
| [`../02-modules/redclaw-integration.md`](../02-modules/redclaw-integration.md) | ZAG ↔ RedClaw 集成设计 |
| [`../02-modules/ide-control.md`](../02-modules/ide-control.md) | IDE 控制设计 |
