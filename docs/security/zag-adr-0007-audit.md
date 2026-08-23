# ADR-0007: Audit Requirements (Append-Only, WORM, Fail-Closed)

- Status: Proposed
- Date: 2025-01-XX
- Authors: ZAG Security Working Group
- Relates to: ADR-0001 (Token Format), ADR-0004 (Request Safety),
  ADR-0006 (Event Safety)

## Context

The ZAgentGateway mediates every privileged operation in the ZAG
control plane. After the fact, we must be able to answer
unambiguously: who did what, when, against which resource, with what
decision. The audit trail is the last line of defense when token
forensics, dispute resolution, regulator inquiries, or internal
investigations are needed.

This ADR makes the audit subsystem a first-class, fail-closed
component. Losing or corrupting an audit record is treated as a
security incident, not an operational nuisance.

## Decision

### 1. Audit record shape

Every audit record is a JSON object with the following required
fields:

| Field              | Type     | Description                                  |
|--------------------|----------|----------------------------------------------|
| `schema`           | string   | Constant: `zag.audit.v1`                     |
| `audit_id`         | string   | UUIDv7, unique cluster-wide                  |
| `occurred_at`      | string   | RFC3339 timestamp at decision time           |
| `tenant_id`        | string   | Tenant the decision belongs to               |
| `principal_id`     | string   | Subject that triggered the action            |
| `actor_kind`       | string   | `user`, `service`, `system`                  |
| `action`           | string   | Verb, e.g. `task.cancel`                     |
| `resource`         | string   | Resource identifier, e.g. `task:<uuid>`      |
| `decision`         | string   | `allow` or `deny`                            |
| `decision_reason`  | string   | Short machine code, e.g. `rbac_role_missing` |
| `request_id`       | string   | From ADR-0004                                |
| `operation_id`     | string   | From ADR-0004                                |
| `idempotency_key`  | string   | From ADR-0004                                |
| `idempotency_replayed` | bool  | From ADR-0004                                |
| `source_ip`        | string   | Client source IP                             |
| `user_agent`       | string   | Client `User-Agent`                          |
| `payload`          | object   | Additional structured fields (redacted)      |
| `prev_audit_hash`  | string   | SHA-256 of the previous audit record         |
| `signature`        | string   | Detached Ed25519 signature                   |

Canonicalisation rules for the audit body are identical to the event
envelope in ADR-0006.

### 2. Append-only and WORM

- Audit records are written to an object-store backed WORM (Write
  Once Read Many) bucket. The bucket is configured to reject object
  overwrites and deletes for the gateway's IAM principal.
- Records are batched into immutable segments: one segment per minute
  per cluster, sealed at the minute boundary. Once sealed, a segment
  is hashed and the hash is signed and stored as the segment's
  manifest. The manifest is itself written to WORM storage.
- Operators cannot delete audit records via the gateway API; only
  out-of-band tooling with the dedicated `audit-retainer` role can
  expire records after the retention window. That action itself
  produces an audit record of kind `system` named `audit.expired`.

### 3. Retention

- Default retention: 7 years for decisions on IAM, token, and
  approval actions; 18 months for all other decisions.
- Retention is enforced by the bucket lifecycle policy, not by the
  gateway. The gateway has no code path that deletes an audit
  record.

### 4. Fail-closed guarantees

The gateway MUST NOT acknowledge a write request success unless the
corresponding audit record has been durably committed. Concretely:

1. The handler commits the mutation to the Raft log (ADR-0005). The
   log entry includes the audit record payload.
2. The audit dispatcher takes the committed entry, builds the
   canonical audit record, signs it, and writes the segment to WORM
   storage.
3. The handler returns success to the client only after the
   dispatcher has acknowledged the WORM write.

If any of the above steps fails:

- The handler returns `503 audit_unavailable` and rolls back the
  mutation if it was not yet visible.
- The gateway emits a `severity=critical` operator alert.
- New writes from the affected tenant are denied until the situation
  is resolved.

### 5. Hash chain

Each tenant has its own ordered chain of audit records. The
`prev_audit_hash` of record N is the SHA-256 of the canonical body
of record N-1 for the same tenant. The first record for a tenant
uses the zero hash.

This mirrors the event chain in ADR-0006 and lets investigators
detect reordering, deletion, or tampering of historical records.

### 6. Query API

The gateway exposes a read-only `audit.query` endpoint with the
following semantics:

- Requires `audit.read` permission (ADR-0003).
- Returns records that the principal's tenant is authorised to see.
- Pagination is cursor-based on `audit_id`.
- Results are returned with the signature and `prev_audit_hash` so
  consumers can verify chain integrity offline.
- The endpoint refuses to serve if chain integrity cannot be verified
  for the requested range; it returns
  `503 audit_chain_unverifiable` instead.

### 7. Tamper detection

A daily job walks the entire audit chain, recomputes hashes, and
verifies signatures. Any mismatch:

- Emits a `severity=critical` operator alert.
- Records an audit entry of kind `system` named `audit.tamper_detected`
  in the affected tenant's chain. This entry intentionally does NOT
  fix the chain; it flags the break for investigators.
- Suspends write access for the affected tenant until an operator
  acknowledges in an out-of-band channel.

### 8. Sensitive data in audit records

- Secrets (private keys, raw tokens, passwords) MUST NOT appear in
  audit payloads. The `payload` field is schema-validated per action;
  unknown fields are rejected.
- Free-text fields (e.g. `decision_reason`) are restricted to a
  closed enum of machine codes. Operators adding a new code must
  update the contract.
- PII minimisation: actions that touch user content (e.g. a message
  body) MUST store only identifiers, not the content itself.

### 9. Failure modes (mandatory section)

- **WORM storage unavailable.** Fail-closed: writes return
  `503 audit_unavailable`. Reads remain available. New writes from
  any tenant whose records cannot be written are blocked.
- **Audit dispatcher crash mid-write.** The outbox record (ADR-0005)
  keeps the audit payload; on restart the dispatcher replays the
  outbox, never losing a pending audit.
- **Chain integrity mismatch detected.** Fail-closed for the
  affected tenant: new writes blocked, daily job continues to
  report. Operations never resume silently.
- **Retention expiration while a record is still under dispute.**
  The WORM lifecycle is authoritative; the gateway cannot extend
  retention. Operators MUST export disputed records before
  expiration via the dedicated out-of-band tool.
- **Signing key unavailable.** Fail-closed: same as ADR-0006.

### 10. Implementation notes

- The package `internal/audit` owns the record builder, the
  WORM-write interface, the chain verifier, and the daily tamper
  walker.
- The skeleton provides an in-memory WORM stub for tests. The unit
  tests assert fail-closed behavior when the stub returns errors.
- Unit tests cover: missing fields, chain continuity, signature
  verification, redaction enforcement, and tamper-detection
  behaviour.