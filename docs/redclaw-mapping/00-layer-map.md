# 00 — RedClaw / OpenCode Layer Map

> Scope: Subtask C — explicit, testable mapping between every layer between
> `acc_task` (incoming mobile request) and `opencode_session` (real runtime).
> Every row cites source path + line range. "planned" rows have no source
> evidence yet and must never be marked implemented.

## 0. Identity and trust pre-conditions

| Concern | Owner (SSOT) | Source evidence |
|---|---|---|
| Tenant identity | JWT claims signed by `pocketd` (or the originating control plane). Never trusted from `X-Tenant-ID`, `X-User-Id`, or query parameters. | `RedClaw/services/platform-go/internal/facade/auth.go:46-94` (`authRequired` rejects `X-User-Id`); `RedClaw/services/platform-go/internal/orchestrator/server.go:55-61` (orchestrator middleware bound to JWT); `RedClaw/services/platform-go/internal/agentcontainer/server.go:57-62` (agentcontainer `/api/v1` group JWT-gated). |
| Auth modes | RedClaw façade uses HS256 JWT (`Authorization: Bearer …`). OpenCode uses `Authorization: Basic …` (or `auth_token` query). RedClaw and OpenCode are **not** interchangeable; the adapter must translate, not relay. | Façade: `RedClaw/services/platform-go/internal/facade/auth.go:14-42`; OpenCode: `opencode/packages/opencode/src/server/routes/instance/httpapi/middleware/authorization.ts` (referenced in `groups/session.ts:454`, `groups/permission.ts:53`). |
| Service identity | Short-lived delegated token + mTLS between ZAG ↔ RedClaw. mTLS failure must fail-closed. | Constraint documented in `docs/新架构v1/01-architecture/安全模型.md` and `docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md §4.1` (`/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md:202-242`). |

## 1. Layer table

| Layer | Process / Service | Base URL | Responsibility | Source evidence |
|---|---|---|---|---|
| **ACC** (mobile BFF) | `pocketd` (Go binary in `backend/pocketd`) | `/api/agent/*` (mobile surface) | Receives user intent, owns `acc_task` SSOT for task identity on the mobile side, enforces user RBAC and JWT. | `backend/internal/server/*` (HTTP routes), `backend/internal/agent/*` (domain managers). Inventory: `docs/新架构v1/00-research/现有服务能力盘点.md` (`/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/docs/新架构v1/00-research/现有服务能力盘点.md`). |
| **ZAG** (planned adapter) | `services/zagent-gateway/cmd/zag` (scaffold only; not yet shipped) | `/api/v1/*` (planned) | Single authority that owns `zag_operation` SSOT. Mints `operation_id`, propagates `idempotency_key`, enforces RBAC/ABAC on every request, decides whether to call RedClaw, translate, or fail-closed. | Scaffold: `services/zagent-gateway/internal/{identity,idempotency,outbox,audit,auth,config}/`. Design: `docs/新架构v1/02-modules/zagent-gateway.md`. No end-to-end implementation yet — must stay `planned`. |
| **RedClaw façade** | `services/platform-go/cmd/platform` → `internal/facade` | `/api/v2` (facade v1 contract) | Tenant-scoped `task / run / approval / notification / memory` API surface in front of ACC/Memora; JWT-validated; real mode delegates to ACC backend, mock mode is deterministic. Owns `redclaw_task` SSOT. | Server: `RedClaw/services/platform-go/internal/facade/server.go:159-183` (`v2.Group("/api/v2")`); model: `RedClaw/services/platform-go/internal/facade/model.go:15-44` (`taskStatuses`, `accStateToFacadeStatus`); auth: `RedClaw/services/platform-go/internal/facade/auth.go:46-94`. |
| **RedClaw orchestrator** | `services/platform-go/cmd/platform` → `internal/orchestrator` | `/api/v1` (legacy `v1` surface, **internal**) | Task queue + session store + WebSocket hub + audit + control dispatcher. Handles double-signature control-plane commands (`/api/v1/control`). **Internal** to platform-go; not the public façade. | Server: `RedClaw/services/platform-go/internal/orchestrator/server.go:55-61`; handlers: `RedClaw/services/platform-go/internal/orchestrator/handlers/handlers.go:38-64` (`Mount`); control: `RedClaw/services/platform-go/internal/orchestrator/control/control.go`. |
| **agentcontainer** | `services/platform-go/cmd/platform` → `internal/agentcontainer` | `/api/v1/invoke` (+ `/skills`, `/permissions/profiles`, `/tokens/issue`, `/tokens/verify`, `/health`) | Wraps the legacy OpenClaw CLI subprocess (`/usr/local/bin/openclaw`). Validates input, assembles a workspace (SOUL.md), injects permission profile, then invokes the CLI synchronously and returns JSON. | Server: `RedClaw/services/platform-go/internal/agentcontainer/server.go:57-62`; invoke handler: `RedClaw/services/platform-go/internal/agentcontainer/handlers/handlers.go:33-119`; invoker: `RedClaw/services/platform-go/internal/agentcontainer/agent/invoker.go`. |
| **RedClaw generic connectors** | `services/platform-go/internal/connectors` (library, not a process) | n/a — invoked by orchestrator/control plane | Generic external integration model: `ConnectorDefinition`, `Connection`, `Ingest`, `Execute` (idempotent), `Status`, `Revoke`. **Not** an IDE/ZCode/VS Code/Cursor/OpenCode adapter. No IDE connector is currently implemented; only stubbed `InMemoryService` and `PostgresConnectorsService`. | Types: `RedClaw/services/platform-go/internal/connectors/connectors.go:18-139`; in-memory stub: same file lines 145-279; PG: `RedClaw/services/platform-go/internal/connectors/postgres.go:38-340`. Audit flags: `docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md:158-161`. |
| **OpenClaw subprocess** | `/usr/local/bin/openclaw` (CLI binary started by `agentcontainer.Invoker`) | n/a — local subprocess | Legacy CLI runtime executed by agentcontainer. Reached only through the `Invoke` HTTP endpoint. Distinct from OpenCode — emits its own JSON shape, **not** OpenCode message parts. | Invoker: `RedClaw/services/platform-go/internal/agentcontainer/agent/invoker.go` (referenced from `handlers/handlers.go:96`). No first-class IDE/ACP surface. |
| **OpenCode runtime** | `opencode/packages/opencode` server | `/session`, `/session/:id/message`, `/event`, `/permission`, `/question`, `/pty`, `/global/{health,event,config,dispose,upgrade}` | Real coding-agent runtime: session list/create/fork/abort, message parts, prompt stream, permission prompts, user questions, PTY, global SSE event stream, Basic Auth (`opencode` / `OPENCODE_SERVER_PASSWORD`). | Session: `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:78-105` (`SessionPaths`); events: `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/event.ts:7-29`; permission: `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/permission.ts:11-62`; question: `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/question.ts:11-74`; PTY: `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/pty.ts:29-138`; global: `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/global.ts:67-138`. |

## 2. Status matrix

| ACC `acc_task.status` | ZAG `zag_operation.status` | RedClaw façade `task.status` (`internal/facade/model.go:15-18`) | OpenCode `session.status` (derived from `/session/status`, `/session/:id`) | Notes |
|---|---|---|---|---|
| `pending` | `queued` | `queued` | `idle` / absent | Task created but no session yet. ZAG mints `operation_id`; façade mints `task_id` + `run_id` and emits `task.created.v1`. |
| `dispatching` | `dispatching` | `queued` (still) | `running` (after prompt) | ZAG is calling façade `POST /api/v2/tasks` and waiting for the façade to accept. |
| `running` | `running` | `running` | `running` | Façade reflects ACC `in_progress`; OpenCode `/session/status` reports `busy`/`running`. |
| `paused` | `paused` | `blocked` (from `paused`) | `idle` | Pause emitted by user; ZAG holds the session open. See `internal/facade/model.go:36`. |
| `awaiting_approval` | `awaiting_approval` | `needs_approval` | `idle` (pending permission/question request) | Façade reflects ACC `review/approved/revision` per `internal/facade/model.go:37-39`. |
| `succeeded` | `succeeded` | `completed` | `completed` (terminal) | Final state. |
| `failed` | `failed` | `failed` | `completed` w/ error result | Terminal. |
| `cancelled` | `cancelled` | `cancelled` | `completed` w/ cancelled flag | Terminal. |
| `indeterminate` | `indeterminate` (ZAG-only; never sent up by façade) | *(not exposed)* | *(indeterminate)* | Only ZAG may mint this. See `01-flow.md` §5. |

## 3. Identifier ownership

| Identifier | Minted by | Re-emitted by | Persisted by | Source |
|---|---|---|---|---|
| `acc_task.id` | ACC | ACC only | ACC store | ACC internal (not in repo); design `docs/新架构v1/02-modules/chief-as-acc.md`. |
| `zag_operation.id` | ZAG | ZAG | ZAG outbox | Scaffold `services/zagent-gateway/internal/idempotency/`; planned. |
| `redclaw_task.task_id` | RedClaw façade | façade only | façade store / ACC mirror | `RedClaw/services/platform-go/internal/facade/store.go:156-182` (`CreateTask`); echo `handlers_tasks.go:108-158`. |
| `redclaw_task.run_id` | RedClaw façade | façade only | façade store + event stream | `RedClaw/services/platform-go/internal/facade/store.go:160-182`; event `store.go:386-407`. |
| `redclaw_task.operation_id` | RedClaw façade (writes) | façade + ZAG | façade idempotency store | `RedClaw/services/platform-go/internal/facade/store.go:184-190`; `handlers_tasks.go:151`. |
| `opencode_session.id` | OpenCode runtime | OpenCode only | OpenCode SQLite store | `opencode/packages/opencode/src/session/session` (referenced from `groups/session.ts:5,133`). |
| `opencode_session.message_id` | OpenCode runtime | OpenCode | OpenCode store | `opencode/packages/opencode/src/session/message-v2` (referenced from `groups/session.ts:6`). |
| `redclaw_event.event_id` | RedClaw façade | façade + orchestrator WS hub | façade store + orchestrator | `RedClaw/services/platform-go/internal/facade/store.go:386-407` (`appendEventLocked`); envelope `model.go:126-136`. |
| `opencode_event.event_id` (EventV2) | OpenCode runtime | OpenCode | OpenCode store | `opencode/packages/opencode/src/server/event` (referenced from `groups/global.ts:3`). |
| `redclaw_permission.gate_id` | ACC + façade | façade, ZAG, ACC | façade `gates` map | `RedClaw/services/platform-go/internal/facade/store.go:79-101`; decision `handlers_approvals.go:81-97`. |
| `opencode_permission.request_id` | OpenCode runtime | OpenCode | OpenCode store | `opencode/packages/opencode/src/server/routes/instance/httpapi/groups/permission.ts:31-43`. |

## 4. Items explicitly marked `planned` (no source evidence yet)

- **ZAG end-to-end implementation.** Only scaffolds exist under `services/zagent-gateway/internal/{identity,idempotency,outbox,audit,auth,config}/`; no `main.go`, no Redis/Memora binding, no real client to RedClaw. Cannot be treated as implemented.
- **ACC worker registration against ZAG.** Not yet wired in code; ACC + ZAG integration is design-only (`docs/新架构v1/02-modules/chief-as-acc.md`).
- **ZAG → RedClaw mock-real adapter.** No code under `backend/internal/redclaw_mapping/` for the client (this subtask provides types + tests only).
- **ZAG → OpenCode live session adapter.** No client implementation; this subtask defines the mapping boundary, not the client.
- **RedClaw façade real backend wiring with ACC + Memora.** `Backend=BackendReal` is partially implemented (`facade/backend.go`) but mock fallback is the default; do not treat as production.
- **Real OpenCode fixed-version contract.** `OpenCode` source is tracked at upstream HEAD; no pinned commit.
- **Real IDE / ZCode / VS Code / Cursor / OpenCode connector.** Not implemented. `RedClaw/services/platform-go/internal/connectors/connectors.go` defines the generic model only; no IDE binding.
- **`projection_unavailable` retry policy.** Failure path exists (`handlers_events.go:23-29`) but the consumer-side reconcile policy and retry-after contract are not codified.

## 5. What is **not** a layer (anti-pattern list)

| Apparent layer | Reality | Why it is wrong |
|---|---|---|
| "RedClaw as OpenCode drop-in backend" | Does not exist | See `docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md:22-33` — protocol and responsibility mismatch. |
| "ZAG can call OpenCode CLI directly" | Forbidden | `docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md §3.1` explicitly forbids skipping ZAG. |
| "Connector (`internal/connectors`) = IDE adapter" | Generic only | See §1 row "RedClaw generic connectors" above. |
| "Orchestrator `/api/v1` = public API" | Internal surface | Orchestrator is gated by `auth.Middleware` (`internal/orchestrator/server.go:55-61`) and exposes internal control commands (`handlers/handlers.go:54-56`). |

