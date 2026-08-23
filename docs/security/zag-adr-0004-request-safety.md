# ADR-0004: Request Safety Primitives (Idempotency, operation_id, X-Request-Id)

- Status: Proposed
- Date: 2025-01-XX
- Authors: ZAG Security Working Group
- Relates to: ADR-0001 (Token Format), ADR-0007 (Audit)

## Context

The ZAgentGateway exposes control-plane operations that are retried by
clients (mobile networks drop, IDEs reconnect, dashboards poll) and
that may be replayed by intermediaries. Without explicit safety
primitives we risk:

- Duplicate side effects (a `task.cancel` running twice on retry).
- Cross-request correlation gaps during incident triage.
- Ambiguous causality between upstream requests and downstream events.

We need three primitives, each with a defined contract, applied at the
gateway ingress before any handler runs.

## Decision

### 1. Required headers on every request

| Header                  | Direction     | Required          | Producer              |
|-------------------------|---------------|-------------------|-----------------------|
| `X-Request-Id`          | client+server | required (server) | client OR gateway    |
| `Idempotency-Key`       | client        | required on writes | client               |
| `operation_id` (body)   | client        | required on writes | client               |
| `X-Gateway-Decision-Id` | server        | always            | gateway               |

`X-Request-Id` MUST be a 128-bit value rendered as lowercase hex
(32 chars) or a UUIDv7 string. The gateway MUST accept either.
`Idempotency-Key` MUST be a string of length 16..128, restricted to
`[A-Za-z0-9_\-]`. `operation_id` MUST be a string of length 8..64,
restricted to `[A-Za-z0-9_\-\.]`.

### 2. X-Request-Id

- If the client supplies a valid `X-Request-Id`, the gateway adopts it.
- If absent or malformed, the gateway generates a new UUIDv7 and sets it
  in the response. The chosen value is exposed to downstream handlers via
  `context.RequestID`.
- The same value is propagated to every outbound call the gateway makes
  (e.g. to event emitters, audit log, downstream microservices) as both
  a header and a structured field on events.
- The gateway MUST reject a request whose `X-Request-Id` is syntactically
  invalid (wrong charset/length) using `400 invalid_request_id`.
- `X-Request-Id` is **not** authoritative for deduplication. Its sole
  purposes are correlation and log stitching.

### 3. Idempotency-Key

The `Idempotency-Key` is required on every write verb (`create`,
`update`, `delete`, `approve`, `rotate_key`, `manage_iam`, and any
custom verb that produces side effects).

Scope of an idempotency record is the tuple
`(tenant_id, principal_id, method, path, Idempotency-Key)`. The gateway
stores the result of the first successful execution (HTTP status, body
hash, completion timestamp) keyed by this tuple for **24 hours** from
the moment the request first began executing.

Behaviour on replay:

- **Same body as the original** (compared via SHA-256 of the raw
  request body): return the stored response verbatim. Set the
  `Idempotency-Replayed: true` response header.
- **Different body**: return `409 idempotency_key_collision` and DO NOT
  execute the operation. The original response is preserved unchanged.
- **Original still in flight**: return `409 idempotency_in_progress`
  with a `Retry-After: 1` header. Do not start a second execution.
- **Original failed with 5xx**: the idempotency record is NOT retained.
  The client may retry; the gateway will execute again.

Limits:

- Per `(tenant_id, principal_id)`: at most 1000 live idempotency records.
  Beyond that the gateway returns `429 idempotency_quota_exceeded`.
- Per-key body cap: 1 MiB. Larger bodies return `413 payload_too_large`
  with an `Idempotency-Key` error code.

Storage guarantees:

- The idempotency store is part of the gateway's state surface and
  inherits the durability/RPO guarantees from ADR-0005.
- On read from a follower, the gateway MAY serve the cached response if
  the record is at least 5 seconds old; otherwise it returns
  `409 idempotency_in_progress`.

### 4. operation_id

`operation_id` is a field in the JSON request body for write operations.
It MUST be unique per business operation from the client's perspective.
The gateway uses it to:

1. Link the request to its emitted events (events carry
   `operation_id` per ADR-0006).
2. Provide a stable identifier for client-side retries that survived an
   `Idempotency-Key` collision.

The gateway validates the format on ingress; a missing or malformed
`operation_id` on a write yields `400 missing_operation_id`.

`operation_id` MUST be recorded on every audit entry that corresponds
to the operation.

### 5. Ordering and retries

The gateway guarantees that for any single `(Idempotency-Key, body)`
pair:

- The body is executed **at most once**.
- The first response is the one returned to all replays.
- A 5xx followed by a retry may execute the body more than once; this
  is documented and accepted (clients SHOULD treat 5xx as ambiguous).

### 6. Error codes

| Code                         | HTTP | Meaning                                  |
|------------------------------|:----:|------------------------------------------|
| `invalid_request_id`         | 400  | `X-Request-Id` malformed                 |
| `missing_operation_id`       | 400  | Write body missing `operation_id`        |
| `idempotency_key_collision`  | 409  | Key reused with a different body         |
| `idempotency_in_progress`    | 409  | Original execution still in flight       |
| `idempotency_quota_exceeded` | 429  | Too many live records for the principal  |
| `payload_too_large`          | 413  | Request body exceeds 1 MiB cap           |

### 7. Audit integration

Per ADR-0007 the audit record for every write request MUST include:

- `X-Request-Id`
- `Idempotency-Key` (when present)
- `operation_id`
- `Idempotency-Replayed` flag

This makes post-incident reconstruction unambiguous.

### 8. Implementation notes

- The package `internal/reqsafety` owns parsing, validation, and the
  idempotency store interface.
- The idempotency store is pluggable; the skeleton provides an
  in-memory implementation behind a small interface so the persistence
  choice in ADR-0005 can be slotted in later.
- Table-driven unit tests cover: missing headers, malformed headers,
  in-progress replays, body-collision, quota exhaustion, and crash
  recovery semantics (durable record outliving a restart).

## Consequences

Positive:
- Side effects on writes are guaranteed at-most-once per
  `(key, body)`.
- Logs, traces, events, and audit entries share one identifier per
  request, simplifying incident triage.
- Clients get a stable error surface for retry-related failures.

Negative / Risks:
- The 24h idempotency retention window is shorter than some operator
  workflows. Clients needing longer replay safety MUST persist their
  own request/response pairs.
- The 1 MiB body cap forces large uploads onto a separate "blob +
  reference" flow, intentionally deferred.

## Failure modes (mandatory section)

- **Idempotency store unavailable.** Fail-closed: writes return
  `503 idempotency_unavailable`; reads are unaffected.
- **Body hash mismatch but key reused.** Fail-closed: return
  `409 idempotency_key_collision`. The first execution is never
  overwritten.
- **Quota exhausted.** Fail-closed: `429 idempotency_quota_exceeded`.
  No partial execution; no record created.
- **`X-Request-Id` collision across unrelated clients.** Not a safety
  hazard; correlation remains best-effort. Logged as `WARN` so an
  upstream misconfiguration is visible.
- **Crash between side-effect commit and idempotency record write.**
  Documented as a possible duplicate-execution window for 5xx-class
  failures. Compensated by clients treating 5xx as ambiguous.