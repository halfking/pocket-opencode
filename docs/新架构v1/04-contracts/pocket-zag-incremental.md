# Pocket → ZAG 增量接口设计（Incremental Interface Design）

> 目标：在不破坏现有 `/api/agent/*`、`/api/opencode/*` 行为的前提下，为未来 ZAG client 接入预留独立路由空间与授权通道。**本文档是合同草案，不是已实现功能。**
>
> 安全底线：与 `01-architecture/安全模型.md` v3 §3（身份与委托链）、§5（mTLS、密钥和签名）、§6（WebSocket/SSE 安全）、§7（SSOT、幂等和故障恢复）保持一致。

---

## 1. 现有路由承诺（不破坏）

下列路由的请求体、响应体、状态码、错误码、鉴权方式在本文档中**显式冻结**：

| 路由前缀 | 鉴权 | 授权源 | 不可修改项 |
|---|---|---|---|
| `/api/agent/*` | 用户 JWT（cookie 或 `Authorization: Bearer`） | JWT claims → `identity.FromContext` | 响应体字段、HTTP 状态码、错误信封 |
| `/api/opencode/*` | 用户 JWT | JWT claims + 本地 OpenCode runtime 直连（受信网络） | SSE 帧格式、permission/question 双格式兼容 |
| `/api/sessions/*`、`/api/tasks/*`、`/api/instances/*` 等 | 用户 JWT | JWT claims | 与上同 |

**禁止**：

- 修改以上路由的鉴权或授权源（即便提供"兼容开关"也不允许）；
- 在以上路由的 query string 中加入 JWT（违反安全模型 v3 §6）；
- 把裸 `X-Tenant-ID`、`X-User-ID` header 加入以上路由作为授权依据（违反 §3.1）；
- 通过以上路由透传至 ZAG（这些路由仅服务于"pocket → 本地 OpenCode"或"pocket → RedClaw façade"）。

---

## 2. 新增路由：`/api/zag/v1/*`

新增路由前缀**仅**供未来 ZAG client 调用，独立命名空间、独立中间件链、独立错误信封。

### 2.1 路由表（草案）

| 方法 | 路径 | 说明 | 鉴权 | 幂等键 |
|---|---|---|---|---|
| GET | `/api/zag/v1/health` | ZAG 上游探活（轻量） | mTLS **或** delegated JWT | — |
| GET | `/api/zag/v1/pods` | 列出 pod | delegated JWT | — |
| GET | `/api/zag/v1/pods/:podId` | pod 详情 | delegated JWT | — |
| POST | `/api/zag/v1/pods/:podId/control` | pod 控制（pause/resume/restart/upgrade/rollback/terminate） | delegated JWT + 二次审批回执 | 必须 |
| GET | `/api/zag/v1/agents` | 列出 agent | delegated JWT | — |
| GET | `/api/zag/v1/agents/:agentId` | agent 详情 | delegated JWT | — |
| POST | `/api/zag/v1/agents/:agentId/invoke` | 触发 agent 调用 | delegated JWT | 必须 |
| GET | `/api/zag/v1/ide` | 列出 IDE 实例 | delegated JWT | — |
| GET | `/api/zag/v1/ide/:name/status` | IDE 状态 | delegated JWT | — |
| POST | `/api/zag/v1/ide/:name/command` | 执行 IDE 命令（仅 schema 内命令） | delegated JWT + 命令注册表校验 | 必须 |
| POST | `/api/zag/v1/tasks` | 提交 task | delegated JWT | 必须 |
| GET | `/api/zag/v1/tasks/:taskId` | task 详情 | delegated JWT | — |
| POST | `/api/zag/v1/tasks/:taskId/cancel` | 取消 task | delegated JWT | 必须 |
| POST | `/api/zag/v1/permissions/:permId/reply` | 回复权限请求 | delegated JWT | 必须 |
| GET | `/api/zag/v1/tasks/:taskId/events` | 订阅 task 事件（SSE） | delegated JWT | — |

> 所有"必须"列表示写路径。客户端必须发 `Idempotency-Key`；缺失则 400。

### 2.2 错误信封（草案）

与现有 `facade.APIError` 同形，便于上层重试逻辑复用：

```json
{
  "error": {
    "code": "tenant_mismatch",
    "message": "delegated token tenant_id does not match mTLS SAN",
    "retryable": false
  },
  "request_id": "req_xxx",
  "correlation_id": "corr_xxx"
}
```

`code` 取值至少包括：`bad_request`、`unauthorized`、`forbidden`（含 `tenant_mismatch`、`scope_insufficient`、`subject_mismatch`）、`not_found`、`conflict`、`rate_limited`、`upstream_unavailable`、`idempotency_conflict`、`internal`。

---

## 3. 鉴权与授权（明确禁止与强制项）

### 3.1 禁止项（写在评审 checklist 顶部）

- **禁止**：把任何长期 JWT（>5 分钟 TTL）放在 query string 中作为 `/api/zag/v1/*` 的鉴权依据。
- **禁止**：以裸 `X-Tenant-ID` 或 `X-User-ID` header 作为新授权路径的**唯一**来源；二者只能作为一致性 trace hint，必须与 JWT claim 或 mTLS SAN/CN 同时校验。
- **禁止**：复用现有 `/api/agent/*`、`/api/opencode/*` 的鉴权中间件直接服务 `/api/zag/v1/*`；必须新建中间件。
- **禁止**：ZAG 不可用时静默降级为"只发 HMAC 头"绕过 mTLS。
- **禁止**：审计不可写时执行高危写（pod.terminate、git.push、shell.run 等）。

### 3.2 强制项

新授权必须满足以下**任一**路径（不可省略其一）：

#### 路径 A：mTLS

1. pocketd 加载受管 CA bundle（`POCKET_ZAG_MTLS_CA`），不接受运行时新增未知 CA；
2. ZAG client 必须出示由该 CA 签发的 leaf cert，证书 SAN/CN 与配置的 ZAG 实例名一致；
3. leaf cert TTL ≤ 24h，支持重叠轮换；
4. cert 撤销列表本地缓存并按 TTL 刷新。

#### 路径 B：short-lived ZAG delegated token

1. pocketd 签发内部 JWT，TTL ≤ 5 分钟；
2. claims 必须包含：

   ```json
   {
     "iss": "pocketd",
     "aud": "zagent-gateway",
     "sub": "<user-id>",
     "tenant_id": "<workspace-id>",
     "actor_id": "<device-or-service-id>",
     "actor_type": "user|service|worker",
     "delegated_by": "pocketd",
     "scope": ["agent:read", "task:create"],
     "jti": "<unique-token-id>",
     "iat": 0,
     "exp": 0
   }
   ```
3. ZAG 必须验证签名、`iss`、`aud`、`exp`、`jti`、scope 与对象 tenant；任一失败 401/403；
4. 支持 `kid`/JWKS 或等价轮换机制；
5. 同一 `jti` 不得重复消费。

#### 路径 A+B 组合（推荐）

mTLS 用于"是哪个 ZAG 实例"，delegated JWT 用于"是谁、用什么 scope"。两侧任何不一致（`aud` ≠ 期望 ZAG、tenant_id ≠ mTLS SAN 中的 workspace、`scope` 不足）必须 403 且写审计。

---

## 4. SSE / WebSocket 入口（待定）

`/api/zag/v1/tasks/:taskId/events` 使用 SSE 时必须：

- Authorization header / 受限 HttpOnly cookie / 一次性短期 WS ticket 三选一；
- 校验 Origin、tenant、client scope；
- 每个事件含 `event_id`、`sequence`、`schema_version`、`tenant_id`、`aggregate_id`；
- 支持 `Last-Event-ID` 补偿、去重、out-of-order 处理；
- 慢消费者不阻塞其他租户。

短期方案：先禁止在该路径上使用 query JWT；正式接入由后续任务在 `02-modules/fleetbridge/ws_bridge.go` 中实现。

---

## 5. 不破坏性约束 checklist

任何 PR 在接入 ZAG 时必须逐项确认：

- [ ] `/api/agent/*`、`/api/opencode/*` 响应体快照测试无 diff；
- [ ] 新路由注册在独立中间件链上（不复用 `requireAuth`）；
- [ ] 新增的中间件不接受 query JWT，不接受裸 `X-Tenant-ID` 唯一鉴权；
- [ ] 新增的 `Idempotency-Key` 校验是写路径必经；
- [ ] 写操作先写 outbox，再执行；失败时入 `indeterminate`；
- [ ] `client_jwt_test.go`、`server_opencode*.go` 既有测试全部通过；
- [ ] `mobile_api_isolation_test.go`、`mobile_session_security_test.go` 既有测试全部通过。

---

## 6. 代码接入步骤（运行时未启用）

本任务**仅**做接口与文档预留，**不**接入运行时。后续接入步骤：

1. 在 `backend/internal/zagclient/` 内替换 `NoopClient` 为真实实现（保留 `Client` 接口不变）；
2. 在 `backend/internal/server/router.go`（或 `server.go` 的 `mux`）注册 `/api/zag/v1/*` 中间件链；
3. 启用 `POCKET_ZAG_BASE_URL`、`POCKET_ZAG_AUDIENCE`、`POCKET_ZAG_MTLS_CA` 配置（见 `pocketd-fleet-bridge.md` §9）；
4. 通过 M0/M1 门禁后再向真实租户开放远程写。

---

## 7. 与其他文档的关系

- 上游：`01-architecture/安全模型.md` v3 §3、§5、§6、§7、§10；
- 上游：`02-modules/pocketd-fleet-bridge.md` §2、§4、§5、§6、§9；
- 现状：本文档是合同草案，所有"实现"项实际状态见 `_status.md`。
