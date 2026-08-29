# Scheduled Task Automation

OpenCode Pocket provides tenant-scoped scheduled automations under **Settings → 定时自动化** and the `/api/scheduled-tasks` API.

## Supported executors

| Kind | Payload example | Backend |
| --- | --- | --- |
| `redclaw_chat` | `{"model":"...","messages":[{"role":"user","content":"..."}]}` | RedClaw LLM bridge |
| `redclaw_knowledge` | `{"query":"...","topK":5}` | RedClaw knowledge search |
| `agent_bridge` | `{"agentId":"...","prompt":"...","modelId":"..."}` | OpenCode Agent Bridge |
| `llmbff_summary` | `{"messages":[{"role":"user","content":"..."}]}` | Unified LLM BFF |
| `kxmemory_summary` | `{"date":"2026-08-30","emails":[]}` | kxmemory DailySummary |
| `acc_mcp` | `{"tool":"acc_get_tasks","status":"pending","limit":20}` | ACC MCP connector |
| `webhook` | `{"url":"https://example.com/hook","method":"POST","body":{}}` | Safe outbound HTTP |

`intent_forward` is intentionally not exposed until its executor is implemented.

## Schedule formats

- `cron`: five-field expression, for example `0 9 * * 1-5`.
- `interval`: Go duration, for example `30m` or `6h`.
- `at`: RFC3339 timestamp, for example `2026-09-01T09:00:00Z`.

Times are evaluated in the task's `timezone`, defaulting to `Asia/Shanghai`.

## API

- `GET /api/scheduled-tasks`
- `POST /api/scheduled-tasks`
- `GET /api/scheduled-tasks/{id}`
- `PATCH /api/scheduled-tasks/{id}`
- `DELETE /api/scheduled-tasks/{id}`
- `POST /api/scheduled-tasks/{id}/run`
- `GET /api/scheduled-tasks/{id}/runs`
- `POST /api/scheduled-tasks/preview`

Identity is always taken from the authenticated JWT (`user_id` and `workspace_id`). Payload fields cannot change task ownership. A manual run is queued and returns `202 Accepted`; run state is observable through the runs endpoint and WebSocket lifecycle events.

## Runtime configuration

- `POCKET_SCHEDULER_ENABLED` — enable/disable execution; defaults to `true`.
- `POCKET_SCHEDULER_TICK_INTERVAL` — polling interval; defaults to `5s`.
- `POCKET_SCHEDULER_MAX_PARALLEL` — worker limit; defaults to `4`.
- `POCKET_SCHEDULER_WEBHOOK_TIMEOUT` — outbound webhook timeout; defaults to `30s`.
- `POCKET_SCHEDULER_WEBHOOK_ALLOW_PRIVATE=true` — explicitly allow private webhook addresses for trusted internal deployments. Metadata endpoints remain blocked.

The scheduler requires PostgreSQL. Without `POCKET_POSTGRES_DSN`, the API returns a clear `503` for persistent task operations and the rest of Pocket remains in remote-only mode.

## Reliability and isolation

Definitions and run history are stored in `scheduled_tasks` and `scheduled_task_runs`. Due rows use PostgreSQL `FOR UPDATE SKIP LOCKED` and a bounded claim lease; a crashed process can be recovered after the lease expires. Manual and periodic execution use the same reservation path and worker bound. Every terminal run produces a central audit event (`scheduler.task.run`) and a workspace-targeted WebSocket event.

RedClaw and ACC tasks require a non-empty task workspace and user. RedClaw requests carry the workspace as `TenantID`; the configured RedClaw client must be tenant-compatible. ACC writes carry the task workspace/user attribution, while ACC still enforces its configured service tenant and MCP scopes.
