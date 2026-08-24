# 01 — Task / Operation Flow

> End-to-end flow:
>
> ```text
> acc_task  →  zag_operation  →  redclaw_task/run  →  opencode_session
> ```
>
> Each hop is documented with SSOT ownership, state mapping, identifier
> propagation, idempotency, failure handling, event mapping, and approval
> mapping. Every claim cites source path + line range.

## 1. Hop 1: ACC → ZAG (`acc_task` → `zag_operation`)

### 1.1 SSOT

- **Canonical state of `task_id`**: ACC (`acc_task`). The mobile user is anchored to ACC. ZAG does **not** redefine task identity — it creates a parallel `operation_id` that anchors the adapter-side workflow.
- ZAG must never store the canonical `acc_task` row; it stores `zag_operation` (its own row, with `external_task_ref = acc_task.id` for lookup).

### 1.2 State mapping

| ACC `acc_task.status` | ZAG `zag_operation.status` |
|---|---|
| `pending` | `queued` |
| `dispatching` | `dispatching` |
| `running` | `running` |
| `paused` | `paused` |
| `awaiting_approval` | `awaiting_approval` |
| `succeeded` | `succeeded` |
| `failed` | `failed` |
| `cancelled` | `cancelled` |
| `indeterminate` | `indeterminate` (ZAG only — see §5) |

### 1.3 `operation_id` propagation

- ZAG **mints** `operation_id` (UUIDv4 or `op-NNN` per façade convention; either form is acceptable but ZAG must mint).
- ZAG **re-emits** `operation_id` in every subsequent log line, audit entry, and downstream call to RedClaw. It must never be silently changed.
- If ZAG spawns a child operation (e.g. for an approval retry), the child must carry `parent_operation_id` explicitly; identity lineage must be reconstructible.

### 1.4 Idempotency

- `idempotency_key` is **scoped by tenant** at this hop. The header is `Idempotency-Key` (see façade `envelope.go:14-17`).
- ZAG must mirror the façade convention: keys are stored as `tenant_id + ":" + idempotency_key` (`RedClaw/services/platform-go/internal/facade/store.go:419-432`).
- If two ZAG replicas receive the same key, one wins and the other returns the stored response. ZAG's outbox must persist the response before acknowledging.

### 1.5 Timeout & reconciliation

- ACC → ZAG is bounded by ZAG's outbound HTTP timeout (default 15s per `RedClaw/services/platform-go/internal/facade/server.go:51`). On 504/timeout, ZAG must **not** blindly retry; it must mark `zag_operation.status = indeterminate` and enqueue a reconcile worker.

## 2. Hop 2: ZAG → RedClaw façade (`zag_operation` → `redclaw_task`)

### 2.1 SSOT

- **Canonical state of `task_id` after this hop**: RedClaw façade (`redclaw_task`). ZAG must keep a foreign reference (`redclaw_task_ref`) but must not rewrite or "canonicalize" the row.
- The façade mints both `task_id` (`acc-task-NNN`) and `run_id` (`run-NNN`) deterministically per `RedClaw/services/platform-go/internal/facade/store.go:156-182`.

### 2.2 State mapping

ZAG maps its own state into the façade's `task_contract` (`RedClaw/services/platform-go/internal/facade/handlers_tasks.go:16-23`). The façade's 7-state surface (`taskStatuses` in `internal/facade/model.go:15-18`) collapses ACC's 12-state `BusinessState` via `accStateToFacadeStatus` (`model.go:31-44`). ZAG must map at the ZAG boundary, **not** assume the façade will do it.

| ZAG façade input (`task_contract.type`) | Façade `task.status` lifecycle |
|---|---|
| `agent_task` | queued → running → completed/failed/cancelled |
| `workflow` | queued → running → needs_approval → completed/failed |
| `manual` | queued → needs_approval → completed |

### 2.3 `operation_id` propagation

- ZAG sends `operation_id` as a **trace hint** only. The façade echoes the ZAG `operation_id` if present, but if absent it mints its own (`RedClaw/services/platform-go/internal/facade/store.go:184-190` + `handlers_tasks.go:151`).
- ZAG must not rely on the façade's `operation_id` for its own SSOT; it must keep its own `operation_id` independently.

### 2.4 Idempotency

- Façade uses `tenant_id + ":" + idempotency_key` (`store.go:419-432`). ZAG re-emits the same key from ACC and the façade will dedupe identical replays for the same tenant.
- Cross-tenant key collision is impossible because the key is tenant-prefixed.

### 2.5 Timeout & reconciliation

- Façade default backend timeout = 15s (`server.go:51`). On timeout / 504, ZAG returns `indeterminate` to caller and enqueues reconcile. Reconcile uses the façade's `GET /api/v2/tasks/{task_id}` (handler at `handlers_tasks.go:242-269`) which is a cheap read against the in-memory store.

## 3. Hop 3: RedClaw façade → OpenCode runtime (`redclaw_run` → `opencode_session`)

### 3.1 SSOT

- **Canonical state of `session_id`**: OpenCode runtime. Neither ZAG nor façade may persist OpenCode session state authoritatively; they may cache it (see OpenPocket's `backend/internal/opencode/store.go` for the cache schema) but the live SSOT is OpenCode.

### 3.2 State mapping

Façade `task.status` → ZAG `zag_operation.status` → OpenCode session state. OpenCode's `SessionStatus` is defined in `opencode/packages/opencode/src/session/status` (referenced from `groups/session.ts:9`). The mapping is directional:

| Façade `task.status` | OpenCode equivalent observable via | OpenCode actual session state |
|---|---|---|
| `queued` | nothing yet | session not yet created (ZAG will call `POST /session` to materialize it) |
| `running` | `GET /session/status` reports the session as busy | `running` |
| `blocked` | session exists, no current run | `idle` |
| `needs_approval` | `GET /permission` returns pending request, `GET /question` returns pending request | `idle` w/ pending |
| `completed` | `GET /session/:id` returns terminal state | terminal |
| `failed` | `GET /session/:id` returns terminal state with error | terminal |
| `cancelled` | `POST /session/:id/abort` then terminal | terminal w/ aborted |

### 3.3 `operation_id` propagation

- ZAG must pass `operation_id` as a metadata tag on the session-create body (e.g. inside `Session.CreateInput.metadata` referenced from `groups/session.ts:51-58`). It must not attempt to set OpenCode's internal fields.

### 3.4 Idempotency

- OpenCode has no first-class idempotency key for session creation. ZAG must therefore mint `operation_id` upstream and not rely on OpenCode to dedupe. If a retry is unavoidable, ZAG must **query** OpenCode first via `GET /session` to detect a prior create (see audit §4.4: "超时后必须先 query/reconcile，再 retry，禁止无条件重新执行" — `docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md:282-283`).

### 3.5 Timeout & reconciliation

- OpenCode `POST /session/:id/message` and `POST /session/:id/prompt_async` (`groups/session.ts:316-342`) are streaming or async; ZAG must track the `operation_id` and `session_id` so a `GET /session/:id` (`groups/session.ts:132-143`) reconcile can recover state.
- For any unknown timeout, ZAG must NOT re-issue the prompt. Re-issue risk: duplicating LLM cost and side effects.

## 4. Event mapping

### 4.1 Source-side envelopes

- **RedClaw façade event** (`RedClaw/services/platform-go/internal/facade/model.go:126-136`):
  ```json
  {
    "event_id": "evt-NNNNNN",
    "correlation_id": "...",
    "schema_version": "1.0",
    "type": "task.created.v1 | task.state.changed.v1 | task.progress.v1 | approval.requested.v1 | approval.decided.v1",
    "run_id": "...",
    "task_id": "...",
    "sequence": <int>,
    "occurred_at": "<RFC3339>",
    "payload": { ... }
  }
  ```
- **RedClaw orchestrator WS event** (`RedClaw/services/platform-go/internal/orchestrator/ws/hub.go`):
  ```json
  {
    "event_type": "task.submitted | control.created | control.executed",
    "tenant_id": "...",
    "agent_id": "...",
    "session_id": "...",
    "task_id": "...",
    "payload": <bytes>
  }
  ```
- **OpenCode EventV2** (`opencode/packages/opencode/src/server/event` referenced from `groups/global.ts:3,16`):
  ```json
  {
    "type": "session.created | session.updated | message.updated | permission.asked | question.asked | ...",
    "id": <EventV2.ID>,
    "aggregateID": "...",
    "seq": <finite>,
    "properties": { ... }
  }
  ```
- **OpenCode `/global/event`** stream emits EventV2; **`/event`** instance stream emits text/event-stream of session events.

### 4.2 Mapping rules

| RedClaw façade `type` | RedClaw orchestrator `event_type` | OpenCode EventV2 `type` | ZAG canonical event name | ACC event emitted |
|---|---|---|---|---|
| `task.created.v1` | `task.submitted` | `session.created` | `task.created` | `task.created` |
| `task.state.changed.v1` (`to: running`) | — | `session.updated` (busy) | `task.started` | `task.started` |
| `task.progress.v1` | — | `message.updated` (incremental) | `task.progress` | `task.progress` |
| `task.state.changed.v1` (`to: needs_approval`) | — | `permission.asked` or `question.asked` | `task.awaiting_approval` | `task.awaiting_approval` |
| `approval.requested.v1` | — | `permission.asked` or `question.asked` | `approval.requested` | `approval.requested` |
| `approval.decided.v1` | `control.executed` (if via double-sig) | `permission.replied` or `question.replied` | `approval.decided` | `approval.decided` |
| `task.state.changed.v1` (`to: completed`) | — | `session.updated` (idle terminal) | `task.completed` | `task.completed` |
| `task.state.changed.v1` (`to: failed`) | — | `session.updated` (idle terminal) | `task.failed` | `task.failed` |
| `task.state.changed.v1` (`to: cancelled`) | — | `session.updated` (idle terminal) | `task.cancelled` | `task.cancelled` |

### 4.3 Worked example: `task.progress.v1`

Source event from RedClaw façade (`RedClaw/services/platform-go/internal/facade/store.go:136-145` example payload):
```json
{
  "event_id": "evt-000003",
  "correlation_id": "corr-seed-run-2001",
  "schema_version": "1.0",
  "type": "task.progress.v1",
  "run_id": "run-2001",
  "task_id": "acc-task-1001",
  "sequence": 3,
  "occurred_at": "2026-08-14T00:03:00Z",
  "payload": { "percent": 40, "resource_version": 5 }
}
```

ZAG receives it via SSE `GET /api/v2/runs/run-2001/events` (`RedClaw/services/platform-go/internal/facade/handlers_events.go:22-127`). ZAG fans out to:

- ACC WebSocket as `{type: "task.progress", taskId: "acc-task-1001", percent: 40, sequence: 3}`.
- ZAG audit outbox with `event_id = evt-000003` and `correlation_id` preserved.
- OpenPocket mobile WS with the same payload shape, preserving `operation_id` from the original ZAG state.

ZAG MUST NOT synthesize the event; it is a pure re-emit with `operation_id` enrichment. ZAG MUST persist `last_event_id = evt-000003` so reconnects can resume via `Last-Event-ID` header (see `handlers_events.go:32-35`).

## 5. `indeterminate` status semantics

### 5.1 When it applies

- RedClaw returns 504 (timeout) or `projection_unavailable` and ZAG cannot confirm the task state via `GET /api/v2/tasks/:task_id`.
- The façade emits an in-flight `task.state.changed.v1` event but the corresponding `task.status` reflects a transient state (e.g. `running`) that ZAG cannot verify is still current.
- An OpenCode `prompt_async` (`groups/session.ts:329-342`) succeeds but the SSE stream disconnects before any `message.updated` arrives.

### 5.2 Who can resolve it

- Only **ZAG reconcile worker** can resolve `indeterminate`.
- ACC MUST NOT auto-resolve `indeterminate` to `succeeded` or `failed`.
- OpenCode and façade cannot mint or resolve `indeterminate`; they have no concept of it.

### 5.3 Required evidence to clear `indeterminate`

1. A successful `GET /api/v2/tasks/:task_id` that returns a terminal status (`completed`, `failed`, `cancelled`), OR
2. A successful `GET /session/:id` against OpenCode that returns a terminal status AND the latest event sequence in the façade event log matches the OpenCode session's last-known sequence, OR
3. An explicit ACC-side cancel authorized by the original `actor_id` from the JWT claims.

### 5.4 SLA

- `indeterminate` must be cleared within the **reconcile worker SLA** (default: 30s after the first 504; retry budget: 5 attempts with exponential backoff capped at 5 minutes).
- After the budget is exhausted, ZAG surfaces `indeterminate` to the caller with a stable `reconcile_token` so the caller can re-poll.

## 6. Approval mapping

### 6.1 Three approval surfaces

1. **RedClaw façade gate** (`RedClaw/services/platform-go/internal/facade/handlers_approvals.go`):
   - `POST /api/v2/approvals/:gate_id/decision` with body `{decision, reason, expected_gate_version, candidate_decisions[]}`.
   - Decision is `approve` or `reject`; candidates may be `promote | reject | defer`.
   - Optimistic concurrency on `gate_version` (`store.go:240-262`).
2. **OpenCode permission** (`opencode/packages/opencode/src/server/routes/instance/httpapi/groups/permission.ts`):
   - `POST /permission/:requestID/reply` with body `{reply: "allow"|"deny"|"always", message?}`.
3. **OpenCode question** (`opencode/packages/opencode/src/server/routes/instance/httpapi/groups/question.ts`):
   - `POST /question/:requestID/reply` with body `{answers: []}` or `POST /question/:requestID/reject`.

### 6.2 Which approvals are honored / re-evaluated / re-approved

| Origin | Honored as-is by ZAG? | Re-evaluated by ZAG? | Requires ACC re-approval? |
|---|---|---|---|
| RedClaw façade gate (low/medium risk) | yes — ZAG relays `POST /api/v2/approvals/:gate_id/decision` and updates `zag_operation.status` | no | no |
| RedClaw façade gate (high risk) | yes — but ZAG re-validates RBAC (`approver` role) and tenant binding | yes | **yes** — ZAG must require ACC approver for high-risk gates; gate response alone is not sufficient |
| OpenCode permission (low risk) | yes — ZAG relays `POST /permission/:requestID/reply` with `reply: "allow"` once | no | no |
| OpenCode permission (high risk: write outside workspace, network, git push, shell exec) | NO — ZAG must translate to a façade gate and require ACC approval | yes | **yes** |
| OpenCode question | yes — relayed as `POST /question/:requestID/reply` | no | no (question answers are content, not authorization) |

### 6.3 Permission → ZAG pending-approval round trip

1. OpenCode emits `permission.asked` via `/global/event` (`groups/global.ts:87-95`).
2. ZAG maps `permission.asked` → internal `pending-approval {kind: "permission", session_id, request_id, risk_level}`.
3. ZAG publishes to ACC as `awaiting_approval`; ACC surfaces in OpenPocket mobile.
4. User approves in OpenPocket → ACC calls ZAG → ZAG calls `POST /permission/:requestID/reply` with `reply: "allow"`.
5. OpenCode emits `permission.replied`; ZAG clears `pending-approval`.

### 6.4 Risk classification

ZAG must classify every permission prompt as `low | medium | high` based on the tool name (see `opencode/packages/opencode/src/permission` referenced from `groups/permission.ts:2`). Defaults:

- `low`: read, list, search, glob.
- `medium`: edit (single file in workspace), write (single file in workspace).
- `high`: shell, bash, git push, network, delete, webfetch, install, anything with a path outside the workspace root.

## 7. `projection_unavailable` behavior

- Façade returns `503 Service Unavailable` with code `projection_unavailable` when the real ACC backend is not configured (`handlers_events.go:23-29`, `handlers_notifications.go:42-48`, `handlers_notifications.go:82-89`).
- ZAG surfaces this as an **error class** `ProjectionUnavailable` (carries the original 503 status and a `retry_after_ms` hint derived from the response headers; default 1000 ms when not present).
- ZAG MUST NOT retry inline. The error is returned to the caller (ACC + OpenPocket) which decides whether to retry or surface the degraded state to the user.
- ZAG's reconcile worker keeps polling `GET /api/v2/runs/:run_id/events` until projection recovers, at which point it resumes event streaming.

## 8. Failure reconciliation summary

| Symptom | ZAG action |
|---|---|
| Façade 5xx (other than `projection_unavailable`) | retry with backoff up to 3 times; on persistent failure, mark `indeterminate` |
| Façade 504 | mark `indeterminate`; enqueue reconcile worker immediately |
| Façade 409 (`gate_version_conflict`) | refetch gate via `GET /api/v2/tasks/:task_id`; re-decide based on current version |
| Façade 401/403 | fail-closed; do NOT fall back to anonymous auth |
| Façade `projection_unavailable` | return `ProjectionUnavailable` error class; reconcile worker keeps polling |
| OpenCode network error | retry with backoff up to 3 times; on persistent failure, query `GET /session/:id` for state, then mark `indeterminate` if terminal state cannot be confirmed |
| OpenCode SSE disconnect | reconnect with `Last-Event-ID` (`groups/event.ts:14-23`); if reconnect fails, mark session `paused` and surface to ACC |
| OpenCode `/permission` rejects (user denied) | emit `task.cancelled` to façade, then `cancelled` upstream |
