# Task acceptance evidence design

## Goal

Add a terminal `accepted` task status that records a verifiable acceptance verdict
with an evidence bundle, distinct from the existing `completed` gate. Completion
remains gated by the approval projection SSOT; acceptance captures the human or
system verdict that an already-completed task's outcome is accepted, who accepted
it, and on what evidence.

## Boundaries

- `completed` keeps its existing meaning: work executed and the approval-
  projection pending gate has linearized to zero. The approval projection spec
  (`2026-08-17-task-approval-projection-design.md`) remains the single source of
  truth for the completion gate. `accepted` does not replace or modify that gate.
- `accepted` is terminal: a later `pending` approval projection cannot reopen an
  accepted task (the existing "no reopening of completed" invariant is extended to
  cover accepted). A normal `PATCH /api/tasks/{id}` cannot move a task into or out
  of `accepted`.
- `workspace_id` is taken from authenticated ownership records, never a client
  request body, mirroring the projection spec's trust model.
- `accepted_by` is taken from authenticated claims, never the request body.
- The bundle stores references (URIs, labels, optional content hashes) — never
  uploaded blob bodies. The server stores references but does not fetch them. This
  keeps the task domain free of attachment storage.
- Re-acceptance of an already-accepted task is not a distinct transition: the
  accept endpoint rejects requests on a task that is already `accepted`
  (`409`) rather than overwriting. The terminal verdict is immutable in place.

## Data model

Four nullable columns on `tasks`, all backward-compatible (no NOT NULL default):

```sql
accepted_at          bigint    -- unix seconds of the accept verdict
accepted_by          text      -- user id from authenticated claims, never the body
acceptance_criteria  jsonb     -- reference-only criteria written before acceptance
evidence_bundle      jsonb     -- the evidence supplied at the accept moment
```

The Go model gains corresponding fields (pointer-friendly for optional JSON),
while `TaskUpdate` (the PATCH body) is unchanged — there is no way to set
`accepted`, the columns, or the bundle through the generic edit path.

`evidence_bundle` is a stable structured object, not an opaque blob:

```go
type EvidenceBundle struct {
    Note       string              `json:"note,omitempty"`
    References []EvidenceReference `json:"references"`
}

type EvidenceReference struct {
    Kind   string `json:"kind"`             // url | task | session | note | audit_event
    URI    string `json:"uri,omitempty"`     // opaque pointer; server does not fetch
    Label  string `json:"label,omitempty"`
    SHA256 string `json:"sha256,omitempty"` // optional client-computed digest
}
```

`acceptance_criteria` has the same shape as `evidence_bundle` but only
`References` is expected in practice; the server does not evaluate it. Criteria
are reference/visibility for the actor and UI, not a server-checked gate. The
authenticated actor IS the gate; the audit `audit.task.accepted` action and the
immutable `accepted_*` columns form the tamper-evident trail.

## HTTP surface

A dedicated sub-resource under the existing sub-route router:

```
POST /api/tasks/{id}/accept
{
  "evidenceBundle": { "note": "...", "references": [...] },
  "note": "..."   // optional; top-level note is folded into evidenceBundle.note
}
```

On success returns the updated `Task` (200, JSON). The handler is registered next
to `attach-session` and `sessions` sub-route handling.

Error contract, symmetric with the existing PATCH response path:

| Condition                                   | Status |
|---------------------------------------------|--------|
| Task not in `completed` (incl. already accepted, in-progress, etc.) | 409 |
| Task not found / wrong workspace            | 404 |
| Malformed or rejected `evidenceBundle`     | 400 |
| Unauthorized                                | 401 (handled by `requireAuth`) |

The audit action is `audit.task.accepted` — non-terminal in the audit
infrastructure sense (emitted via `s.Write`, like `task.status.changed`), with
`resource = task:<id>` and a `Detail` of `from=completed;to=accepted;actor=<userId>;references=<n>`.
The `accepted_*` columns are the authoritative record; the audit line is the
tamper-evident companion, matching how the existing audit is a companion to
status columns rather than their SSOT.

No corresponding `reject` or `revoke` endpoint. Acceptance is final by design
(approved terminality decision).

## Store method

`AcceptTaskScoped(ctx, id, wsID, actorUserID, bundle EvidenceBundle) (*Task, error)`
is added to `task.Store`. It linearizes under the **same task advisory lock**
already used by `CompleteTaskScoped`
(`pg_advisory_xact_lock(hashtextextended(wsID+":"+id))`) so acceptance and any
concurrent late-pending approval projection event serialize per-task. The
transaction:

1. lock task (advisory xact lock)
2. `SELECT status FROM tasks WHERE id = $1 AND workspace_id = $2 FOR UPDATE`
3. reject if status != `completed` → return `ErrTaskNotCompletable`/ sentinel
4. `UPDATE tasks SET status='accepted', accepted_at=$now, accepted_by=$actor,
   evidence_bundle=$bundle_json, updated_at=$now WHERE id=$1 AND workspace_id=$2`
5. commit; return `GetTaskScoped(id, ws)`

`ErrTaskNotCompletable` is a new typed error returned for non-completed states;
the handler maps it to `409`. The bundle is JSON-validated by Go decode of the
typed struct and marshalled for storage — no opaque `json.RawMessage`.

## Late-pending anti-regression (critical)

Two existing guards keep a late `pending` approval projection event from
reopening a completed task:

- `store.go:444`: `if status == "completed" && event.State == ApprovalStatePending { continue }`
- `applyObservedApprovalsForTask` `NOT EXISTS (... status = 'completed')` clause in
  the conditional projection INSERT (store.go:555)

Both are extended to cover `accepted`:

- the in-tx branch becomes `if status == "completed" || status == "accepted"`
  (when the projected state would be `pending`)
- the `NOT EXISTS` clause in `applyObservedApprovalsForTask` becomes
  `status NOT IN ('completed','accepted')`

These are careful two-line extensions of the projection body; they preserve the
existing "late pending does not reopen" invariant for the new terminal state,
which the approval projection spec explicitly flagged as work this increment
owns.

## Migration

A single new migration adds the four columns, nullable, no default. No new tables.
Evidence lives on `tasks` rather than creating a second immutable audit surface —
the projection spec already owns the durable projection row, and acceptance is a
verdict, not a log stream. Request handler and store gated on whether the columns
exist? No — the migration is additive, gated by deployment. Existing rows simply
have null `accepted_*`; they never become `accepted` unless an explicit
`POST /accept` arrives.

## Tests (TDD vertical slices)

PostgreSQL integration tests, skipped without `POCKET_TEST_POSTGRES_DSN`, mirroring
the approval projection spec's test discipline:

1. **Accept on a `completed` task transitions to `accepted`.** Fields
   `status`, `accepted_by`, `accepted_at`, `evidence_bundle` set; the returned
   task reflects them.  `RED → GREEN`
2. **Accept on a non-completed task is rejected with the sentinel error.** Task
   status is unchanged.  `RED → GREEN`
3. **Handler: malformed `evidenceBundle` body returns 400**, and the task is
   unchanged. Exercise the handler end-to-end, not only the store.  `RED → GREEN`
4. **Anti-regression: a late `pending` approval projection event does not reopen
   an accepted task.** Seed a completed→accepted task; call
   `ApplyApprovalProjectionEvent` with a `pending` event for an attached session;
   assert status stays `accepted` and no pending projection row is inserted for
   that task.  `RED → GREEN`
5. **Workspace isolation: accept via the handler does not succeed against a task
   owned by another workspace** even with the right task id.  `RED → GREEN`

Each test uses the real `AcceptTaskScoped` / handler, observes public behavior
through the store's public interface and HTTP responses, and survives refactor.

## Out of scope

- `POST /api/tasks` idempotency via `Idempotency-Key` + workspace unique key —
  a later increment (handoff §5 follow-on).
- Mobile outbox replay — later.
- ACC / RedClaw / Temporal envelope & control-plane wiring — `accepted` does not
  flow back to ACC in this increment; it is OpenPocket task-local, symmetric with
  the approval projection spec's local-domain boundary.
- Server-side evaluation of `acceptance_criteria`: the actor is the gate. Criteria
  are reference/visibility only.
- `reject`/`revoke` from `accepted`: not added; acceptance is terminal by decision.
