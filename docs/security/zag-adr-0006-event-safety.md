# ADR-0006: Event Safety Requirements for ZAgentGateway

- Status: Proposed
- Date: 2025-01-XX
- Authors: ZAG Security Working Group
- Relates to: ADR-0004 (Request Safety), ADR-0005 (State Replication),
  ADR-0007 (Audit)

## Context

The gateway emits events whenever a control-plane operation has
visible side effects (task created, role granted, key rotated, token
issued, audit decision made). These events are consumed by:

- The audit pipeline (ADR-0007) for append-only storage.
- Downstream services (e.g. worker dispatch, notifications) for
  follow-up work.
- Operator dashboards for live state inspection.

Consumers cannot tolerate:

- **Lost events** - a missing "task.created" event means the worker
  pool never picks the task up.
- **Duplicate events** delivered without a way to dedupe - downstream
  services double-charge or double-cancel.
- **Reordered events** that violate causality (a "task.completed"
  emitted before "task.started").
- **Forged events** that impersonate another tenant or principal.

This ADR defines the guarantees the gateway provides for every event
emitted from a write request.

## Decision

### 1. Event envelope (JSON, versioned)

Every event MUST be a JSON object with the following top-level fields:

| Field              | Type     | Description                                       |
|--------------------|----------|---------------------------------------------------|
| `schema`           | string   | Constant: `zag.event.v1`                          |
| `event_id`         | string   | UUIDv7, unique within the cluster                 |
| `event_type`       | string   | e.g. `task.created`, `token.issued`               |
| `tenant_id`        | string   | Tenant the event belongs to                       |
| `principal_id`     | string   | Subject that triggered the event                  |
| `operation_id`     | string   | From ADR-0004                                     |
| `request_id`       | string   | From ADR-0004                                     |
| `occurred_at`      | string   | RFC3339 timestamp (UTC) at mutation commit time   |
| `emitted_at`       | string   | RFC3339 timestamp at outbox append                |
| `payload`          | object   | Event-type-specific body                          |
| `prev_event_hash`  | string   | Hex SHA-256 of the previous event for this tenant |
| `signature`        | string   | Detached signature over the canonical body        |

The canonical body for signing/verification is the JSON object with
`signature` and `prev_event_hash` set to empty strings and the
remaining fields sorted lexicographically.

### 2. Hash chain (per tenant)

Each tenant has its own ordered log of events. The `prev_event_hash`
of event N is the SHA-256 of the canonical body of event N-1 for the
same tenant. The first event for a tenant uses
`prev_event_hash = "0000000000000000000000000000000000000000000000000000000000000000"`.

This chain lets consumers detect reordering or tampering of historical
events. The gateway signs the chain head on every commit so that a
downstream consumer can verify continuity.

### 3. Signatures

- Each event is signed with the gateway cluster's signing key
  (Ed25519). The public key is published via the JWKS endpoint
  (ADR-0001).
- Signatures cover the canonical body; verifiers MUST reject any
  modification to `signature`, `prev_event_hash`, or any signed field.
- The signing key is rotated every 30 days; the previous key remains
  published for 90 days to allow verification of in-flight events.

### 4. Delivery guarantees

- **At-least-once delivery.** Every event that the gateway committed
  to the Raft log (ADR-0005) is delivered to every consumer exactly
  the number of times the consumer's retry policy requires; the
  gateway never silently drops an event.
- **Per-tenant ordering.** Within a single tenant, events are
  delivered in the order they were committed. Across tenants, ordering
  is best-effort.
- **No loss after commit.** If the gateway cannot deliver an event
  (consumer unreachable, sink timeout), the gateway retries with
  exponential backoff up to 24 hours, then escalates to
  `severity=critical` audit and a `503 event_sink_unavailable` for
  any new writes from the affected tenant until the situation is
  resolved.
- **Idempotent consumers.** Every event carries `event_id`. Consumers
  MUST dedupe on it. The gateway also exposes a "since cursor" API
  so consumers can resume after a restart.

### 5. Outbox pattern

Events are not emitted directly from request handlers. The flow is:

1. Request handler commits the mutation to the Raft log (ADR-0005).
2. As part of the same log entry, an `outbox` record is appended.
3. A background dispatcher reads committed outbox records, builds the
   canonical event, signs it, and pushes to consumers.
4. The dispatcher marks the outbox record as `delivered` only after
   every consumer has acknowledged (or the retry budget is exhausted
   and the failure has been audited).

This guarantees that an event is never emitted for a mutation that
was rolled back, and that a mutation is never committed without its
event eventually being emitted.

### 6. Sensitive payload handling

- Secrets (private keys, raw tokens) MUST NOT appear in event
  payloads. References (key IDs, certificate fingerprints) are fine.
- PII in event payloads MUST be minimised. The contract defines a
  per-event-type payload schema; any field not in the schema is
  rejected at canonicalisation.
- Event payloads MUST be redaction-checked before signing. A unit
  test enforces that no event in the catalog contains a known
  sensitive pattern.

### 7. Required events

The gateway MUST emit events for at minimum:

- `token.issued`, `token.revoked`, `token.rotated`
- `iam.role.granted`, `iam.role.revoked`
- `task.created`, `task.started`, `task.completed`, `task.failed`,
  `task.cancelled`
- `approval.requested`, `approval.granted`, `approval.denied`
- `audit.decision.recorded`

Additional event types MAY be added; each MUST follow the envelope in
section 1 and be listed in `zag-contract.md`.

### 8. Failure modes (mandatory section)

- **Event sink unreachable.** The outbox dispatcher retries with
  backoff. After 24 hours, fail-closed: writes from the affected
  tenant return `503 event_sink_unavailable`; the gateway emits a
  `severity=critical` audit entry and an operator alert.
- **Signing key unavailable.** Fail-closed: the gateway refuses to
  commit any new event (and therefore any new mutation) until the
  signing key is restored. Reads are unaffected.
- **Hash chain break on read.** The gateway's chain verifier rejects
  the affected event and the entire subsequent chain. The gateway
  emits `severity=critical` audit and refuses to issue new events for
  the affected tenant until an operator investigates.
- **Outbox growth unbounded.** A configurable cap (default 1 million
  pending records per replica) triggers a `503 outbox_overflow`
  fail-closed response for new writes from any tenant whose records
  are queued. This prevents the gateway from accepting writes it
  cannot guarantee to deliver.
- **Duplicate delivery.** Not a failure mode: documented above.
  Consumers MUST dedupe on `event_id`.

### 9. Implementation notes

- The package `internal/event` defines the envelope, the canonical
  body builder, and the chain verifier.
- Unit tests cover: round-trip encoding, canonicalisation stability,
  signature verification (positive and negative cases), chain
  continuity, and redaction of known sensitive patterns.
- The skeleton ships an in-memory consumer for tests; production code
  uses an HTTP push interface behind a small abstraction.