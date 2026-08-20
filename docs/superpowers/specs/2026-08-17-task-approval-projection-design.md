# Task approval projection design

## Goal

Make task completion depend on a durable, versioned projection of OpenCode permission and question lifecycle events rather than on a request-time read of in-memory managers. The projection is local to OpenPocket's task domain; it is not the long-term ACC/Temporal control-plane record or the future acceptance-evidence model.

## Boundaries

- The projection is derived only for sessions attached to a task in the authenticated workspace.
- `workspace_id` comes from server-side ownership records, never a client request.
- The current `tasks.pending_approvals` column remains a backward-compatible derived counter. It is not an independent source of truth.
- A projection state is terminal once it leaves `pending`; replayed or late `pending` events cannot reopen it, even if their locally assigned version is higher after a process restart.
- `pending` blocks completion. `approved`, `rejected`, `answered`, `expired`, and `failed` do not.
- The existing mobile approval API and WebSocket contract stay compatible in this increment.

## Data model

`task_approval_projections` is keyed by:

```
(workspace_id, task_id, instance_id, session_id, request_id, kind)
```

It stores the current `state`, a monotonic `version`, optional decision metadata, and creation/update timestamps. A lookup index supports resolving all tasks linked to an `(instance_id, session_id)` pair and counting pending rows for one task.

The stable state is a materialized current view, not an append-only audit log. The future acceptance-evidence work owns immutable audit evidence.

## Event application

OpenCode's normalized permission/question manager events are translated to a small task-domain input:

```
workspace, instance, session, request, kind, state, version, decision
```

The manager persists the normalized event synchronously before publishing it to subscribers. `ApprovalBroadcaster` then forwards only the already-durable event to the existing WebSocket contract. For each input, the task store finds every matching `task_session_links` row in the same workspace. It serializes each affected task with a transaction-scoped PostgreSQL advisory lock, then:

1. inserts the first projection row, or conditionally replaces it only when the incoming version is greater than the stored version;
2. recomputes that task's `pending_approvals` from projection rows with `state = 'pending'`;
3. commits before any WebSocket notification is emitted.

A normalized manager event has no upstream sequence today, so the adapter assigns a monotonically increasing local version at event observation. Future upstream event versions can replace this source without changing task-store semantics.

An instance without a workspace has no task projection target and is safely ignored; it is never broadcast across tenants.

## Completion linearization

`CompleteTaskScoped` runs in one transaction under the same task advisory lock. It locks and reads the task in its workspace, counts `pending` projection rows, writes the derived counter, and changes status to `completed` only when the count is zero. If pending rows exist it returns `ErrPendingApprovals` and leaves status unchanged.

This establishes a per-task ordering between an event application and a completion request. Completion no longer queries `PermissionManager` or `QuestionManager`. If the projection store is unavailable, the completion API fails closed.

## Event and process wiring

`ApprovalBroadcaster` injects the projection sink into both managers. A manager changes its in-memory pending cache only after the projection write succeeds; a persistence failure leaves the state retryable and suppresses WebSocket publication.

`pocketd` constructs the task store before the managers, injects it into the broadcaster, and starts both permission and question managers. Starting managers is required so upstream events can reach the durable projection.

## Tests

PostgreSQL integration tests (skipped without `POCKET_TEST_POSTGRES_DSN`) cover:

- a pending event projects to every same-workspace attached task and increments the derived count;
- resolution with a newer version clears the gate, while replays and lower versions do not regress state;
- another workspace's task is never projected;
- completion is rejected while a pending row exists and succeeds after resolution;
- concurrent application and completion serialize on the task lock, so the final completed task has no pending projection.

OpenCode unit tests cover event-to-projection mapping and ensure a persistence failure is not broadcast as success.
