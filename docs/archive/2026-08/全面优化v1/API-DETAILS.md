# OpenPocket v1 API 接入细化方案

> 状态：接入设计草案。当前 Pocket 已有本地 API、ACC MCP、Memora `/v1/*`、LLM gateway/直连、RedClaw bridge 多链路；本文定义迁移到平台 façade 的 API 适配方案。

## 1. 当前本地 API 基线

| 能力 | 当前路径/方式 | 状态 | 说明 |
| --- | --- | --- | --- |
| 用户 HTTP | `Authorization: Bearer <Pocket JWT>` | CURRENT | pocketd 校验用户 token |
| WS | `/ws?token=...` | CURRENT | query token，用于本地实时消息 |
| OpenCode SSE | `/api/mobile/sessions/{session_id}/event?instance_id=&after=` | CURRENT | session event stream |
| 任务 | `/api/tasks?source=local|acc|all` | CURRENT/PARTIAL | 本地 store + ACC MCP 缓存 |
| 通知 | `/api/notifications`, `/api/notifications/rules` | CURRENT | 本地通知中心 |
| ACC | MCP `acc_get_tasks` | CURRENT | `POCKET_MCP_BASE_URL` + API key |
| Memora | `/v1/notes/classify`, `/v1/emails/classify`, `/v1/emails/daily-summary` | CURRENT | 分类/总结兼容路径 |
| RedClaw bridge | `/health`, `/api/chat`, `/api/knowledge/search` | STUB/PARTIAL | 与 RedClaw 当前路由未闭环 |
| LLM | `POCKET_LLM_GATEWAY_URL` 优先，否则直连 | PARTIAL | 生产需禁直连 |

## 2. 目标接入拓扑

客户端只与 pocketd 通信；pocketd 作为 BFF/agent host 调 RedClaw façade 和本地 OpenCode/ACP。service-to-service 使用 service JWT，不把用户 token 直接透给 ACC/Memora/gateway。

```text
Mobile UI → pocketd → RedClaw façade → ACC/Memora/gateway/SM
              └→ local OpenCode/ACP runtime
```

## 3. pocketd → RedClaw façade client

### 通用 headers

```http
Authorization: Bearer <pocket-service-jwt>
X-Correlation-ID: <corr>
Traceparent: <trace>
Idempotency-Key: <required for writes>
```

JWT claims：`aud=redclaw-facade`、`sub=service:openpocket`、`tenant_id`、`actor_id`、`actor_type=user|service`、`scope`。

## 4. 任务 API 迁移

### 当前本地任务模型需补字段

```yaml
pocket_task:
  pocket_task_id: string
  acc_task_id: string optional
  redclaw_task_url: string optional
  project_id: string optional
  source: local|acc|redclaw
  status: local_pending|queued|running|blocked|needs_approval|completed|failed|cancelled|unknown
  status_source: pocket|acc|redclaw
  resource_version: integer optional
  opencode_instance_id: string optional
  opencode_session_id: string optional
  correlation_id: string
  last_synced_at: timestamp
```

### List

当前：`GET /api/tasks?source=local|acc|all`。

目标内部调用：`GET {redclaw}/api/v2/tasks?project_id=&status=&limit=&cursor=`。

合并规则：

- `acc_task_id` 存在时，ACC/RedClaw 状态覆盖本地缓存。
- 本地离线任务状态只可为 `local_pending`，同步成功后写 `acc_task_id`。
- 未知状态映射为 `unknown`，UI 不崩溃。

### Create

Pocket local endpoint 可继续为 `POST /api/tasks`，但当用户选择“平台任务”时由 pocketd 调：

`POST {redclaw}/api/v2/tasks`

Request body：`project_id/title/description/task_contract/client_ref`。

Idempotency：`tenant_id + user_id + client_ref`。

Response 写回本地 mapping。

### Dispatch to local OpenCode

当 ACC/RedClaw 选择 Pocket runtime 执行时，需要字段：

```yaml
runtime_dispatch:
  acc_task_id: string
  run_id: string
  dispatch_id: string
  opencode_instance_id: string
  prompt: string optional
  context_snapshot_ref: string optional
  fencing_token: string
  attempt: integer
```

Pocket 回调状态时必须带 `dispatch_id/fencing_token/attempt/resource_version`，避免旧回调覆盖新任务。

## 5. 审批 API

> **当前实现范围（2026-08-15）**：移动端审批 bottom sheet（`approval.bottom_sheet_v1` flag）
> 目前仅覆盖 **pocketd 本地审批**（OpenCode permission/question 请求，走 pocketd WS 推送）。
> 本节描述的 RedClaw façade 审批（gate/candidate_decisions）尚未接入 UI；接入时需在
> `usePendingApprovals` 之前增加一层 façade 审批适配层（gate_id ↔ 本地 approval_id 映射、
> SSE cursor 与 WS 推送去重），不改现有本地审批契约。

### Pull/list approvals

目标：`GET {redclaw}/api/v2/tasks?status=needs_approval` 或后续 `GET /api/v2/approvals`。

UI item 字段：

```yaml
gate_id: string
task_id: string
run_id: string
approval_type: command|memory_candidate|permission|deployment
summary: string
risk_level: low|medium|high
candidate_decisions: array optional
expires_at: timestamp optional
resource_version: integer
```

### Decision

`POST {redclaw}/api/v2/approvals/{gate_id}/decision`

Request:

```json
{
  "decision": "approve|reject",
  "reason": "...",
  "expected_gate_version": 3,
  "candidate_decisions": [
    {"candidate_id": "cand-123", "decision": "promote|reject|defer", "reason": "..."}
  ]
}
```

Pocket 本地需记录 `operation_id` 与 `correlation_id`，断网后可用相同 `Idempotency-Key` 重试。

## 6. 记忆 API

### 当前分类兼容

继续使用 Memora `/v1/*`，直到 Memora v2 service JWT 与 ingest/search 完成。

### ingest queue

新增本地失败重试队列字段：

```yaml
memory_ingest_queue:
  id: string
  source_type: email|note|meeting|session
  source_id: string
  payload_hash: string
  idempotency_key: string
  status: pending|sent|failed|dlq
  attempts: integer
  next_retry_at: timestamp
  correlation_id: string
  last_error: string optional
```

目标调用：`POST {redclaw}/api/v2/memory/ingest`（若 façade 不提供，则 pocketd 直接调 Memora `/api/v2/memories/ingest`，但需 service JWT）。

### search UI

目标调用：`POST {redclaw}/api/v2/memory/search`。

Request fields：`query`、`scope_chain`、`top_k`、`token_budget`、`filters`。

Result card：`memory_id`、`source`、`title/snippet`、`score`、`provenance.source_system`、`created_at`、`policy_decision`。

## 7. 通知 API

当前 `/api/notifications` 保留本地通知。v1 目标是合并平台通知：

- 平台来源：`GET {redclaw}/api/v2/notifications`。
- 本地来源：OpenCode session、设备状态、离线重试。
- 合并键：`notification_id`；如果来源无全局 ID，用 `source + source_resource_id + type`。

Ack：本地通知写本地；平台通知调用 `POST /api/v2/notifications/{id}/ack`。

## 8. 实时与断线续传

- 本地 WS `/ws` 继续用于设备内实时事件。
- OpenCode SSE 继续用于 session stream。
- 平台任务/审批事件使用 RedClaw façade SSE，cursor 存本地。

本地 cursor：

```yaml
stream_cursor:
  stream: redclaw_run_events|opencode_session_events
  stream_id: string
  cursor: string
  updated_at: timestamp
```

## 9. 验收

- 用 RedClaw mock 完成 task create/list、approval decision、notification ack、memory search、event reconnect。
- 网络断开后同一 `Idempotency-Key` 不产生重复任务/审批。
- 生产 profile 抓包证明 LLM 0 直连。
- Pocket 任一页面展示的数据都可追溯：source system、resource id、correlation id。
