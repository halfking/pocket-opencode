# Scheduled Task Implementation Audit — 2026-08-30

**Scope:** Scheduled-task backend, Server/bootstrap wiring, RedClaw/ACC/AI executors, and Settings automation UI.

## Result

**Rating: B+ after remediation**

No P0 findings remain in the reviewed implementation. The audit exercised data flow, process closure, state transitions, concurrency, schema compatibility, targeted unit tests, `go vet`, `go build`, frontend type checking, and frontend production build.

## Data Flow

- HTTP definitions are authenticated by the existing JWT middleware; `user_id` and `workspace_id` originate from claims, not request payloads.
- Definitions persist in `scheduled_tasks`; individual outcomes persist in `scheduled_task_runs`.
- Results flow to workspace WebSocket events and central audit records.
- Executor payloads are typed per executor and JSON-validated before dispatch.

## State Model

`Task`: enabled/disabled plus scheduled lease state. `Run`: `running → success|failed|skipped`.

- `ClaimDue` uses `FOR UPDATE SKIP LOCKED` and a lease to prevent duplicate concurrent claim.
- `ClaimTaskNow` applies the same lease and `max_runs` guard for manual execution.
- Completion records terminal run state, increments run count, clears lease, and calculates the next schedule.

## Findings Fixed During Audit

1. **Manual-run context cancellation** — `202 Accepted` work originally retained the HTTP request context, which cancels after a response. `Scheduler.TriggerNow` now dispatches through a scheduler-owned background context and still applies task-level timeout.
2. **ACC tenant boundary** — ACC executor now requires the task workspace to equal the configured MCP tenant before it invokes an MCP tool. This prevents a scheduled task from acting through a service client configured for another tenant.
3. **ACC tool schema safety** — removed executor-added `workspace_id`/`user_id` tool arguments that could conflict with ACC tool schemas. Tenant isolation remains at the signed MCP client boundary.
4. **Test data race** — race detector found unsynchronized read of the fake audit writer. Added a synchronized accessor and wait condition; `go test -race ./internal/scheduledtask/...` now passes.
5. **Claim recovery** — added `lease_until` migration and per-task lease duration (`max(5m, timeout_sec + 60s)`), so a process crash does not strand a claim indefinitely.
6. **API/UI contract** — runs endpoint now requires GET; manual trigger is asynchronous; UI has an immediate-run action, cooldown input, and route-param watches for reused Vue components.
7. **Unsupported option** — removed public `intent_forward` selection until an implementation exists.
8. **Semaphore initialization race** — first scheduler scan and first manual trigger could initialize the worker semaphore concurrently. Initialization now goes through a mutex-protected helper; the race suite passes after the change.

## Remaining Operational Constraints

- RedClaw and ACC clients are statically tenant-configured. A task whose workspace does not match the configured client tenant is rejected/fails closed. Multi-tenant fan-out requires a future tenant-scoped client factory.
- Live PostgreSQL, RedClaw, and ACC calls were not executed because this session had no approved deployment credentials or running integration endpoints. Unit, race, compile, and UI-build checks cover the in-process contract.
- A crashed process can leave an old run row in `running` while its task becomes eligible after lease expiry; a future maintenance pass can mark expired running rows as `failed` or `abandoned` for cleaner historical reporting.

## Verification

```text
PASS  go test -race ./internal/scheduledtask/...
PASS  go test ./internal/mcp ./internal/server ./cmd/pocketd
PASS  go vet ./internal/scheduledtask/... ./internal/server/...
PASS  go build ./...
PASS  npm run typecheck
PASS  npm run build
PASS  git diff --check
```

The generic audit scripts were also run. Their process-closure, state-machine, and concurrency checks passed. The data-flow output and migration checker traversed unrelated repository paths / assumed a missing `sql/migrations` directory, so their non-zero result is not a scheduled-task defect; the scoped manual audit above is authoritative.
