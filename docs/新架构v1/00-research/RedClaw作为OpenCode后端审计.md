# RedClaw 作为 OpenCode 后端服务的审计结论

> 审计日期：2026-08-23
> 审计范围：
> - OpenPocket：`/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket`
> - RedClaw：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw`
> - OpenCode：`/Users/xutaohuang/workspace/ai/opencode`
> - 现有 v3 设计：`docs/新架构v1/`
>
> 审计性质：源码与文档核验。ZAgentGateway 尚未实现，因此本文不把设计目标当作现有能力。

---

## 1. 最终判断

### 1.1 不能直接替换

**不能把当前 RedClaw platform-go 直接配置成 OpenCode 的 drop-in backend。**

原因不是部署方式，而是协议和职责不同：

| 维度 | OpenCode | RedClaw platform-go |
|---|---|---|
| 主要定位 | coding-agent runtime | 企业 AI 控制面、任务队列、OpenClaw runtime 包装 |
| 会话 API | `/session`、`/session/:id/message`、parts、snapshot、revert | `/api/v1/sessions` 或 façade `/api/v2/tasks`，字段更粗粒度 |
| 事件 | `/event` SSE + EventV2 | orchestrator WebSocket / façade run SSE，事件 envelope 不同 |
| 执行 runtime | OpenCode 自己的 agent loop、shell、edit、LSP、PTY、MCP | `agentcontainer` 启动 `/usr/local/bin/openclaw` 子进程 |
| 权限交互 | `/permission`、`/question` reply/reject | RedClaw approval、Plan A/E、control signal |
| 终端 | `/pty` + WebSocket | 当前未提供 OpenCode PTY 等价接口 |
| Agent 协议 | ACP service 可用 | 当前未发现 ACP server/provider |
| 认证 | Basic Auth 或 `auth_token` | Bearer JWT / service auth / 内部 token |
| IDE 集成 | OpenCode IDE extension / ACP | RedClaw generic connectors，尚未实现 OpenCode/IDE connector |

因此，直接替换会导致现有 OpenCode client、OpenPocket adapter、IDE extension、PTY、permission/question 和事件订阅无法工作。

### 1.2 可行的定位

推荐采用以下分层：

```text
RedClaw = 企业控制面
  - tenant / identity / policy / approval / audit / quota
  - task / run / notification / LLM gateway
  - OpenClaw runtime 的企业包装

OpenCode = 编程执行 runtime
  - session / message parts / agent loop
  - file / shell / git / LSP / PTY / MCP
  - `/session`、`/event`、`/permission`、`/question`、`/pty`

ZAgentGateway = 受控协议适配与 PC Agent 控制面
  - RedClaw task/run ↔ OpenCode session 映射
  - RedClaw policy/approval ↔ OpenCode permission/question 映射
  - 统一 PC Agent、IDE、OpenClaw、OpenCode 监测接口
  - 为 OpenPocket 和 MCP 客户端提供安全 API

OpenPocket = 移动端 BFF 与控制台
  - JWT、移动 UI、WebSocket、通知、离线同步
```

### 1.3 三种可选方案

| 方案 | 说明 | 结论 |
|---|---|---|
| A. RedClaw 直接替代 OpenCode | 把 RedClaw URL 当作 OpenCode server | **否，不可行** |
| B. RedClaw 控制面 + OpenCode runtime | RedClaw/ZAG 管任务、策略和审批；OpenCode 继续执行 | **推荐，M0-M3** |
| C. RedClaw OpenCode-compatible facade | RedClaw 新增完整 `/session`、`/event`、`/permission`、`/pty` 兼容层 | 可行但工作量大，作为 **M4+** |

---

## 2. 已核验的源码事实

### 2.1 OpenCode 真实接口

OpenCode session 路由位于：

- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/session.ts`
- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/event.ts`
- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/permission.ts`
- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/question.ts`
- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/groups/pty.ts`

关键接口包括：

```text
GET/POST       /session
GET/PATCH/DELETE /session/:sessionID
GET/POST        /session/:sessionID/message
POST            /session/:sessionID/prompt_async
POST            /session/:sessionID/abort
POST            /session/:sessionID/summarize
POST            /session/:sessionID/revert
GET             /event                         # SSE
GET/POST        /permission /permission/:id/reply
GET/POST        /question /question/:id/reply
POST/            question/:id/reject
GET/POST         /pty
GET              /pty/:id/connect               # WebSocket
```

OpenCode 的消息包含 `info` 和 `parts`，不是简单的 `{role, content}`。

### 2.2 OpenCode 认证

OpenCode 当前使用：

- `Authorization: Basic ...`；或
- query `auth_token`（仅兼容用途，不应成为新集成默认）；
- 用户名默认 `opencode`；密码来自 `OPENCODE_SERVER_PASSWORD`。

证据：

- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/routes/instance/httpapi/middleware/authorization.ts`
- `/Users/xutaohuang/workspace/ai/opencode/packages/opencode/src/server/auth.ts`

### 2.3 RedClaw façade 真实能力

RedClaw façade 路由位于：

- `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/facade/server.go`
- OpenAPI：`/Users/xutaohuang/workspace/ai-native-tools/RedClaw/docs/全面优化v1/openapi-facade-v1.yaml`

当前是 façade task/run API：

```text
GET/POST /api/v2/tasks
GET       /api/v2/tasks/:task_id
GET       /api/v2/runs/:run_id/events
POST      /api/v2/approvals/:gate_id/decision
GET/POST  /api/v2/notifications
POST      /api/v2/memory/search
```

它不是 OpenCode session API。façade 默认是 mock；real 模式主要代理 ACC/Memora，run event projection 仍可能返回 `projection_unavailable`。

### 2.4 RedClaw agentcontainer 真实执行模型

RedClaw agentcontainer 的 `/invoke` 会：

1. 做安全输入检查；
2. 组装 workspace / SOUL；
3. 注入权限 profile；
4. 启动 `/usr/local/bin/openclaw`；
5. 读取 CLI JSON 结果。

证据：

- `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/agentcontainer/handlers/handlers.go`
- `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go/internal/agentcontainer/agent/invoker.go`

这可以作为 ZAgentGateway 的执行适配起点，但不等于 OpenCode runtime。

### 2.5 RedClaw connectors 当前边界

`internal/connectors/` 当前定义的是通用外部连接模型：

- OAuth2 / API key / Basic / mTLS；
- ingest / execute / revoke；
- cursor / rate limit / idempotency key / side effect level。

当前内存实现仍含 stub receipt，未证明存在可用的 ZCode、VS Code、Cursor、OpenCode connector。因此 v3 的 IDE 控制必须标记为 planned，不得标成已接通。

### 2.6 OpenPocket 当前 OpenCode 适配

Pocket 的正式 Go adapter 已实现主要 OpenCode HTTP/SSE 能力：

- `/session` list/create/delete;
- `/session/:id/message`;
- messages/context;
- interrupt/compact/wait;
- permission/question;
- `/event` SSE;
- `/global/health`。

证据：

- `backend/internal/adapter/opencode_http.go`
- `backend/internal/agent/adapter_opencode.go`
- `backend/internal/opencode/event_stream.go`

但独立的 TypeScript plugin 和 Go manager 仍使用旧的 `/prompt`、`/api/health` 路径，不能作为当前 OpenCode 兼容证据：

- `opencode-plugin/src/index.ts`
- `opencode-manager/main.go`

---

## 3. 对 v3 方案的审计修正

### 3.1 必须删除的表述

以下表述在 v3 文档中不再成立，已按本审计修正：

- “RedClaw platform-go 可直接作为 OpenCode 后端”；
- “RedClaw connectors 已经可以控制 ZCode / VS Code / Cursor / OpenCode”；
- “ZAG 已注册为 acc-go worker”；
- “M0/M1 功能已完成”；
- “所有现有服务均已生产就绪”；
- “OpenPocket 可以在 ZAG 不可用时绕过安全控制直接访问 RedClaw/CLI”。

### 3.2 必须新增的边界

1. ZAG 是 **planned adapter/control service**，不是现有实现。
2. RedClaw 作为 OpenCode 后端时，必须分为：
   - 控制面集成；
   - OpenCode-compatible facade；
   - 真正 runtime 替代。
3. M0 只做 mock contract 与只读状态，不开放远程 IDE 命令和高危控制。
4. 真实 OpenCode runtime 保留到兼容性验证通过后再切换。
5. 所有声明标注证据等级：`implemented` / `contract-tested` / `source-inspected` / `mock-only` / `planned`。

---

## 4. 必须冻结的安全契约

### 4.1 身份链

禁止把裸 `X-Tenant-ID`、`X-User-ID` 或请求体中的 `fleetId/userId` 当作授权依据。

服务间 token 至少包含：

```json
{
  "iss": "pocketd",
  "aud": "zagent-gateway",
  "sub": "user-id",
  "tenant_id": "workspace-id",
  "actor_id": "device-or-service-id",
  "actor_type": "user|service|worker",
  "scope": ["agent:read", "task:create"],
  "delegated_by": "pocketd",
  "jti": "unique-token-id",
  "iat": 0,
  "exp": 0
}
```

- ZAG 必须验证签名、`iss`、`aud`、`exp`、`jti` 和 scope；
- 目标对象的 tenant 必须与 claims 一致；
- header 仅作为 trace hint，不可提升权限；
- ZAG → RedClaw / ACC 使用受众限定的短期 delegated token；
- 不使用 mTLS 失败后降级到 HMAC 的 fail-open 策略。

### 4.2 高危操作

M0/M1 就必须具备最小 RBAC/ABAC：

- `viewer`：只读状态和事件；
- `operator`：创建/取消自己的 task；
- `approver`：审批绑定到具体 task/command/diff 的请求；
- `admin`：Pod 控制、策略、密钥和 connector 管理。

`allow_always` 必须绑定：tenant、workspace、agent、工具、参数约束、有效期和可撤销策略，不得是全局永久放行。

### 4.3 命令执行

所有 IDE/Agent command 必须：

- 使用注册的 command schema；
- 使用 argv 而非任意 shell 字符串；
- 进行 workspace root、路径 canonicalization、symlink 和 TOCTOU 检查；
- 限制 cwd、环境变量、网络、资源、输出大小和超时；
- 在执行点二次校验授权；
- connector endpoint 必须经过 allowlist，禁止任意 SSRF。

### 4.4 事件和幂等

统一所有跨服务操作字段：

```json
{
  "operation_id": "op-uuid",
  "idempotency_key": "client-uuid",
  "trace_id": "trace-id",
  "tenant_id": "from-verified-claims",
  "source": "pocketd|acc|zag|redclaw",
  "event_id": "monotonic-or-uuid",
  "aggregate_id": "task-or-session-id",
  "aggregate_version": 7
}
```

超时后必须先 query/reconcile，再 retry，禁止无条件重新执行。

### 4.5 WS/SSE

- 不使用 query token；使用 Authorization header、受限 cookie 或一次性 WS ticket；
- subscribe 时执行对象级授权；
- 每个事件带 event_id、sequence、tenant_id 和 schema_version；
- 定义 Last-Event-ID、补偿、去重、背压、最大消息和断线重认证；
- 慢消费者不能阻塞其他租户。

### 4.6 审计和恢复

- 高危写操作的审计写入失败时 fail-closed；
- 使用持久 outbox + append-only/WORM 归档，不能只写可修改的 Memora namespace；
- 待签 command、审批、event cursor 必须持久化，不能依赖进程内 map；
- 明确 RPO/RTO、备份加密和 restore drill。

---

## 5. 推荐落地路径

### M0：兼容性与安全基线

- 固定一个 OpenCode 版本/commit，建立唯一 OpenCode contract；
- 建立 RedClaw facade 与 OpenCode 的差异矩阵；
- 实现 ZAG 只读 Pod/Agent/Session 聚合；
- 实现认证、授权、tenant binding、幂等、审计 outbox；
- 使用 mock RedClaw 和 mock OpenCode 做合同测试；
- **不开放** IDE command、shell、git push、Pod terminate、MCP 写工具。

### M1：控制面集成，保留 OpenCode runtime

- task/run/session 映射：`acc_task → zag_task → redclaw_run → opencode_session`；
- OpenCode `/event` → ZAG event envelope → OpenPocket WS；
- RedClaw approval → OpenCode permission/question；
- OpenPocket 移动审批；
- 只开放低风险只读 IDE command；
- 真实 OpenCode 集成测试通过后再扩大权限。

### M2：高危控制与 IDE 适配

- 先 ZCode，再 OpenCode，再 VS Code/Cursor；
- 命令 schema、workspace sandbox、connector allowlist；
- 独立审批服务/设备完成双签；
- 事件补偿、reconciliation、故障注入。

### M3：可选 OpenCode-compatible facade

只有在确实需要让现有 OpenCode client/IDE 无感连接 RedClaw 时，才实现：

```text
/session
/session/:id/message
/event
/permission
/question
/pty
```

这应作为独立兼容项目，不与 RedClaw task API 混用，也不替代 OpenCode runtime，除非完成完整的 parts/tool/PTY/ACP 兼容性验证。

---

## 6. 审计结论

**可以把 RedClaw 作为 OpenCode 的企业控制面和 LLM/策略后端；不能把当前 RedClaw 后端直接当作 OpenCode runtime 后端。**

正确架构是：

```text
OpenPocket
   ↓ mobile BFF
ZAgentGateway
   ↓ task/policy/approval adapter
RedClaw platform-go
   ↓ execution bridge
OpenCode runtime 或 OpenClaw runtime
   ↓
本地 workspace / IDE / shell / git / PTY
```

默认选择：**保留 OpenCode runtime，先做控制面适配；兼容 facade 仅作为后续可选项目。**
