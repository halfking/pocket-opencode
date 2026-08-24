# 02 — Forbidden Mappings

> This file enumerates mappings that look tempting but are **wrong**. Every
> anti-pattern below has been tempting enough that someone on the team will
> propose it. Each is paired with the failure mode it produces and the
> source evidence that proves the two layers are not interchangeable.
>
> If a future change tries to introduce any of these mappings, this file is
> the canonical reference to cite in review.

## ❌ 1. RedClaw `/api/v1/sessions` ⇔ OpenCode `/session`

**Failure mode.** Conflating the orchestrator's session store with OpenCode's
session API will silently drop message parts, permission prompts, and PTY
streams. OpenCode's `POST /session/:id/message` (`opencode/packages/opencode/src/server/routes/instance/httpapi/groups/session.ts:316-328`) returns a `SessionV1.WithParts` payload, which carries ordered parts of multiple kinds (text, tool, patch, sub-task, agent). RedClaw's `/api/v1/sessions/{id}` returns the in-process `session.Store` record (`RedClaw/services/platform-go/internal/orchestrator/session/store.go`), which has no concept of parts — only `tenant_id`, `user_id`, `agent_id`, timestamps. The two have **no payload overlap**.

**Additional failure.** Authentication is non-equivalent: RedClaw orchestrator uses platform JWT (`internal/orchestrator/server.go:55-61`); OpenCode uses Basic Auth (or `auth_token`) per `groups/session.ts:454` (`Authorization` middleware). Bridging the two would require ZAG to terminate JWT on one side and re-originate Basic Auth on the other, with no signal that the request is the same operation.

**Result.** Any code path that says "fetch the session from RedClaw and resume in OpenCode" will produce a session that exists in one system but not the other, leading to 404s, orphan OpenCode sessions, or duplicated LLM cost.

## ❌ 2. RedClaw generic connector ⇔ implemented IDE connector

**Failure mode.** `RedClaw/services/platform-go/internal/connectors/connectors.go:18-139` defines `ConnectorDefinition`, `Connection`, `Ingest`, `Execute`, `Status`, `Revoke`. The InMemory implementation at lines 145-279 returns a **stub** `CredentialHandle: uuid.NewString()` (line 179), a synthetic `ItemCount: 10` (line 204), and synthetic successful receipts (lines 230-235). Postgres implementation at `postgres.go:38-340` validates connections exist but still returns `ResponseBody: '{"status":"ok"}'` (line 224). There is no IDE/ZCode/VS Code/Cursor/OpenCode connector implementation.

**Additional failure.** The `auth_mode` enum at `connectors.go:24` lists `oauth2 | api_key | basic | mTLS` — none of these are how an OpenCode IDE extension would authenticate. An IDE connector would need WebSocket bidi + filesystem watch, which is outside the connector abstraction entirely.

**Result.** Anyone who wires "send a `ExecuteCommand` to the VS Code connector" will get a 200 OK with `{"status":"ok"}` and the IDE will not have done anything. This is the most dangerous of the five because the failure is silent and the audit log will show "success".

## ❌ 3. OpenClaw CLI stdout ⇔ OpenCode message parts

**Failure mode.** The agentcontainer `/invoke` endpoint
(`RedClaw/services/platform-go/internal/agentcontainer/handlers/handlers.go:55-119`)
starts `/usr/local/bin/openclaw` as a subprocess and reads its JSON output
(referenced via `internal/agentcontainer/agent/invoker.go`). That JSON has the
OpenClaw schema: a `result` field plus a workspace path. OpenCode message
parts (`opencode/packages/opencode/src/session/message-v2` referenced from
`groups/session.ts:6`) have a discriminated union with kinds like `text`,
`tool`, `patch`, `subtask`, `agent`, each with its own typed fields. There is
no safe structural mapping between them.

**Additional failure.** Streaming semantics differ. OpenCode `/session/:id/message` (`groups/session.ts:316-328`) streams incremental updates; OpenClaw returns one final blob. Treating the OpenClaw blob as a single OpenCode "text" part drops tool/patch/sub-task kinds entirely.

**Result.** Any code that "translates OpenClaw stdout into OpenCode message parts" will produce a session that looks like it ran but contains no tool calls, no diffs, and no sub-tasks. The OpenPocket mobile UI will show only a single text block and the audit log will record zero tool invocations.

## ❌ 4. Echo / mock response ⇔ real LLM success

**Failure mode.** The RedClaw façade mock mode (`internal/facade/server.go:46-49,118-145`) returns deterministic responses from an in-memory store. The mock creates tasks with `Status: "queued"` and immediately emits `task.created.v1` then `task.state.changed.v1` (`store.go:177-181`). There is no LLM call. If the mock is used as evidence that "the flow works end-to-end", the integration test is hollow — no tokens are spent, no permission prompts are generated, no tool calls are made.

**Additional failure.** The mock never enters the `needs_approval` lifecycle (it would require the store to seed a gate for the relevant task — see `store.go:88-101` for seed gates, but `CreateTask` at `store.go:156-182` does NOT auto-create gates for new tasks). So approval-flow tests against the mock will silently skip the approval round-trip.

**Result.** Green tests in CI but production fires for the first time on a real RedClaw → OpenCode → LLM path, where the missing approval handling, missing tool streaming, and missing rate limits all surface at once. This is the highest-impact failure mode and is why `real backend` mode (`facade/server.go:64-97`) must be explicitly opted into with a non-default JWT key.

## ❌ 5. RedClaw task API surface ⇔ OpenCode task API

**Failure mode.** RedClaw façade exposes `/api/v2/tasks` (list), `/api/v2/tasks/:task_id` (get), `/api/v2/runs/:run_id/events` (SSE), `/api/v2/approvals/:gate_id/decision` (decide). OpenCode does not have a "task" abstraction — it has "session" and "message". There is no `/task`, no `/run` (in OpenCode's vocabulary), and no `/approval` route.

**Additional failure.** The two systems use different event envelopes:
- RedClaw façade event: `evt-NNNNNN`, schema_version `1.0`, fixed types (`task.created.v1`, `task.state.changed.v1`, etc.) per `RedClaw/services/platform-go/internal/facade/model.go:126-136` and seeded events at `store.go:132-148`.
- OpenCode EventV2: `id`, `seq`, `aggregateID`, `properties` per `opencode/packages/opencode/src/server/event` referenced from `groups/global.ts:3,16`.

Trying to use RedClaw façade's `task_id` as if it were an OpenCode session id will produce 404s against `/session/:task_id`. Trying to use OpenCode's session id against `/api/v2/tasks/:session_id` will produce 404s.

**Result.** The "RedClaw task API surface ⇔ OpenCode task API" mapping is a category error: one is a control-plane task queue, the other is a coding-agent session. The correct mapping is documented in `01-flow.md` §4 and `00-layer-map.md` §3 — they live at different layers and need an explicit adapter (ZAG) to bridge.

---

## Cross-cutting anti-patterns (also forbidden)

| Anti-pattern | Why forbidden |
|---|---|
| ZAG calls OpenCode CLI directly | Bypasses ZAG's own RBAC/ABAC and audit trail. (`docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md §3.1`.) |
| ZAG trusts `X-Tenant-ID` / `X-User-Id` headers | Forged trivially. Auth must come from verified JWT claims. (`RedClaw/services/platform-go/internal/facade/auth.go:23-33`.) |
| ZAG retries on `unknown` timeout | Wastes LLM tokens and may re-execute side effects. Must query first. (`docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md §4.4` line 282-283.) |
| ACC approves high-risk operations without ZAG re-validation | High-risk gates require ZAG RBAC + ACC approver. (`01-flow.md §6.2`.) |
| mTLS fails → fallback to HMAC | Forbidden fail-open. (Design constraint in `docs/新架构v1/01-architecture/安全模型.md`.) |
| ZAG stores canonical task state | Forbidden — façade is the SSOT for `task_id`; OpenCode is the SSOT for `session_id`. ZAG stores references only. (`00-layer-map.md §3`.) |
| Echoing `redclaw_task.operation_id` as ZAG's own `operation_id` | Forbidden — ZAG must mint its own `operation_id`. Façade's `operation_id` is a façade-local field (`RedClaw/services/platform-go/internal/facade/store.go:184-190`). |

