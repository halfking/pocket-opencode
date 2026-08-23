# Pocket Adapter 边界矩阵（Boundary Matrix）

> 目的：在不破坏现有 `/api/agent/*`、`/api/opencode/*` 行为的前提下，明确 OpenPocket 当前所有"出 pocketd 边界"的适配器对协议、鉴权、隔离、幂等、超时、重试、outbox 的实现差异，并锁定未来 ZAG（ZAgentGateway）适配器必须满足的边界条件。
>
> 标记说明：
> - `implemented` —— 当前代码已实现并被调用方依赖，行为固定，禁止静默修改。
> - `contract-tested` —— 行为由自动化测试覆盖，修改必须同步测试。
> - `mock-only` —— 仅 mock / fake 实现存在，未接入运行时。
> - `planned` —— 已写设计但未实现。
> - `blocked` —— 因前置依赖（ZAG 控制面合同未冻结、安全门禁未通过）显式禁止启用。

---

## 1. 适配器列定义

| 列 | 路径 | 当前角色 |
|---|---|---|
| legacy redclaw client | `backend/internal/redclaw/client.go` | 老 RedClaw bridge，仅发送 `Bearer + X-Tenant-ID`，与 `facade client` 并存 |
| facade client | `backend/internal/facade/client.go` | RedClaw façade v2（`/api/v2/tasks|approvals|notifications|memory`），Bearer JWT |
| mcp client | `backend/internal/mcp/client.go` | JSON-RPC 2.0 over HTTP+SSE，对接 ACC MCP server |
| opencode_http adapter | `backend/internal/adapter/opencode_http.go` | 调用本地/远端 OpenCode runtime 的 `/session`、`/event` SSE、permission、question |
| 目标 ZAG client | `backend/internal/zagclient/`（本任务新增） | 仅预留接口与 noop 实现；运行时未接入 |

---

## 2. 边界矩阵（每行一个边界契约维度）

| 维度 | legacy redclaw | facade client | mcp client | opencode_http adapter | 目标 ZAG client |
|---|---|---|---|---|---|
| 协议版本 | `/api/v1/pocket/llm/chat`、`/api/v1/pocket/knowledge/search`（legacy，未冻结） | `/api/v2/tasks|approvals|notifications|memory`（contract-tested） | JSON-RPC 2.0 + MCP `2024-11-05`（implemented） | OpenCode runtime HTTP（裸 OpenAPI，无 version 头）（implemented） | `/api/v1/{pods,agents,ide,tasks,permissions}`（planned） |
| auth 方式 | 静态 `Bearer <Secret>`（由 `ClientConfig.Secret` 配置，immutable token）（implemented） | 服务 JWT `Bearer <Token>`，token claims 自带 `tenant_id`（implemented） | HMAC HS256 内部 JWT，TTL 15 分钟，每次请求重新签发，`sub=pocketd`（implemented） | 无 auth header —— 仅用于同机/受信网络调用本地 OpenCode 端口（implemented） | mTLS（leaf cert + CA bundle）**或** short-lived delegated JWT（TTL ≤ 5 分钟，含 `aud=zagent-gateway`、`scope`），二选一（planned） |
| tenant 隔离机制 | 客户端 `cfg.TenantID` 强覆盖请求 `X-Tenant-ID` header；不一致直接 400（implemented） | 仅从 JWT claim 派生 `tenant_id`；禁止在请求体/header 传 tenant（contract-tested） | JWT `tenant_id` claim + ACC 侧 RLS；pocketd 不重复送 tenant header（implemented） | 不感知 tenant；通过 `workspaceID` query 与本地 runtime 文件目录边界隔离（implemented） | JWT `tenant_id` claim 与 mTLS SAN/CN 双向一致；服务端必须拒绝 claim/SAN 不匹配（planned） |
| idempotency | 不发送 `Idempotency-Key`，靠客户端去重（implemented） | 写操作自动生成 `Idempotency-Key`（16-byte random），可通过 `WithIdempotencyKey` 复用（contract-tested） | 由 MCP 协议层 `id` 字段保证 JSON-RPC 唯一性，无业务幂等键（implemented） | 不发 `Idempotency-Key`；写操作幂等性由 OpenCode runtime 端保证（implemented） | 必须强制发送 `Idempotency-Key`（写路径），且与 `operation_id`、`trace_id`、`event_id`、`aggregate_version` 绑定（planned） |
| 错误码 | 自定义 `ErrorResponse{Code, Message}` + HTTP 状态；非 2xx 返回 Go error（implemented） | 统一错误信封 `ErrorEnvelope` + `*APIError{Status, Code, Message, Retryable, RequestID, CorrelationID}`（contract-tested） | JSON-RPC `error{Code, Message, Data}` + MCP `result.isError`（双层）（implemented） | 原始 HTTP 状态码，错误信息从 body 截断 512B（implemented） | 与 façade 一致：标准 `ErrorEnvelope` + `code` 取自 ZAG 错误码表（planned） |
| 超时 | `cfg.TimeoutSec`，默认 30s（implemented） | `cfg.TimeoutSec`，默认 30s；SSE 走独立 lifecycle（contract-tested） | HTTP 30s + MCP session init TTL 5 分钟（implemented） | `timeoutMS`，默认实例化时传入；SSE `Timeout=0` 由 ctx 控制（implemented） | HTTP 30s；SSE/SSE-events 走独立 lifecycle；批量操作可配（planned） |
| 重试 | 客户端无内置重试，调用方自行重试（implemented） | 由 facade 客户端返回 `Retryable` 字段，**不**自动重试；调用方负责 reconcile（contract-tested） | 客户端无重试；`initialize` 用 `sync.Once` 缓存失败以避免抖动（implemented） | 无重试；`HealthCheck`/`SubscribeEvents` 失败直接冒泡（implemented） | 写路径：超时后必须 query/reconcile，再 retry；未知结果进入 `indeterminate`，禁止直接重放写（planned） |
| outbox | 无 outbox；调用方各自负责（implemented） | 无 outbox；调用方各自负责（contract-tested） | 无 outbox；调用方各自负责（implemented） | 无 outbox；调用方各自负责（implemented） | 必须先写 durable append-only outbox（`aggregate_id` + `operation_id`），再执行；高危操作 fail-closed（planned） |

---

## 3. 适配器 × 维度交叉摘要（用于评审）

### 3.1 legacy redclaw

- 协议：未冻结的 `/api/v1/pocket/*`，保留仅用于历史兼容。
- 风险：静态 `Secret` + 裸 `X-Tenant-ID` 头违反安全模型 v3 §3.1。
- 处置：**仅作为 legacy bridge 保留**，不得用于新能力；接入路径见 §5。

### 3.2 facade client

- 协议：`/api/v2/*`，是当前 ACC 控制面契约。
- 强项：自动 idempotency、统一错误信封、`Retryable` 标记。
- 限制：依赖上游 JWT 的 `tenant_id` claim，禁止业务侧覆盖。

### 3.3 mcp client

- 协议：JSON-RPC 2.0 + MCP `2024-11-05`，面向 acc-go MCP server。
- 强项：内部 HS256 JWT 自动签发、tenant 来自 claim、`initOnce` 保证单次握手。
- 限制：工具 allowlist 与 scope 由 ACC 侧定义；pocketd 仅透传。

### 3.4 opencode_http adapter

- 协议：OpenCode runtime HTTP（`/session`、`/event`、permission、question）。
- 强项：双格式兼容（裸数组 vs `{data:[]}`）、SSE reader 内置缓冲。
- 限制：不感知 tenant，依赖底层 runtime 的目录边界。

### 3.5 目标 ZAG client（本任务新增）

- 接口契约：`backend/internal/zagclient/client.go`
- 默认实现：`backend/internal/zagclient/noop.go`（所有方法返回 `ErrNotConfigured`）
- 测试：`backend/internal/zagclient/client_test.go`
- 运行时：**未接入**。`server.go` / `router.go` 不注册 `/api/zag/*` 路由。

---

## 4. 显式禁止（与安全模型 v3 对齐）

下列行为在所有适配器上一律禁止：

1. 使用 query string JWT / long-lived JWT 鉴权（违反 §6）。
2. 用裸 `X-Tenant-ID`、`X-User-ID` header 作为授权依据（违反 §3.1；legacy redclaw 仅作为历史兼容保留，新代码禁止模仿）。
3. 上游故障后跳过授权直接绕过 ZAG/RedClaw（违反 §1.2）。
4. 审计不可写时执行高危写操作（违反 §7.4）。
5. 超时后不 query/reconcile 就直接重放写（违反 §7.2）。

---

## 5. 迁移路线（与 `pocketd-fleet-bridge.md` 对齐）

1. **冻结 ZAG 协议合同**（v3 草案 → 实施版），同步更新本文档 `协议版本` 行；
2. **接入 mTLS 或 delegated JWT 签发**（`identity.TokenIssuer` 或 KMS/HSM），替换 query JWT；
3. **实现 `*zag.Client`**（替代本任务中的 `NoopClient`），保留 `Client` 接口；
4. **逐路由灰度**：先 `/api/fleet/pods`/`/agents`/`/ide` 只读路径，再放开写；
5. **开启 outbox + reconciliation worker**，验证 fail-closed 行为；
6. **关闭 legacy redclaw 写路径**（保留只读降级文档）。

任何步骤在未通过 M0/M1 门禁（见安全模型 v3 §10）之前，禁止向真实租户开放远程写。
