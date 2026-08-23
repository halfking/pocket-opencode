# ADR-0005: Multi-Replica State Design for ZAgentGateway

- Status: Proposed
- Date: 2025-01-XX
- Authors: ZAG Security Working Group
- Relates to: ADR-0004 (Request Safety), ADR-0006 (Event Safety),
  ADR-0007 (Audit)

## Context

The ZAgentGateway is deployed as multiple replicas behind a load
balancer. Several security-critical pieces of state must be visible
cluster-wide:

- The idempotency store (ADR-0004) - replay safety depends on every
  replica seeing the same record for a given key.
- Revoked-token set and revoked-certificate fingerprints.
- Approval grants (issued, pending, consumed).
- Outbox of audit and event records awaiting durable emission.

A single-node design is unsafe: a replica crash mid-write would let a
replay through, defeating idempotency. Conversely, a synchronous
quorum on the hot path would make the gateway unusable under any
network hiccup. We need a design that is **fail-closed** at the
security boundary but **fast** on the happy path.

## Decision

### 1. Topology: leader for writes, followers for reads

- The gateway runs N replicas (N >= 3 in production). A leader is
  elected via Raft over a small dedicated cluster keyspace; followers
  replicate the log.
- All state-mutating operations are committed to the Raft log on the
  leader before the request returns success. Followers acknowledge the
  append; once a quorum (N/2 + 1) has persisted, the leader applies
  the entry to its state machine and responds.
- Read-only operations may be served by followers **only** with a
  `read_index` linearizable read against the leader's current commit
  index, or from a follower that can prove it is still up to date
  (within `staleness_budget`). The `staleness_budget` defaults to
  500 ms; replicas older than that MUST forward the read to the leader.

### 2. State surfaces and their durability

| Surface                  | Storage                  | RPO    | RTO    |
|--------------------------|--------------------------|--------|--------|
| Idempotency store        | Raft log + LSM           | 0      | < 5 s  |
| Revoked tokens / certs   | Raft log + LSM           | 0      | < 5 s  |
| Approval grants          | Raft log + LSM           | 0      | < 5 s  |
| Audit outbox             | Raft log + WAL           | 0      | < 5 s  |
| Event outbox             | Raft log + WAL           | 0      | < 5 s  |
| Hot in-memory caches     | Local replica            | n/a    | n/a    |

RPO = 0 means a committed write is durable against any single-replica
crash. We accept that an asynchronous fsync may lose the last few
hundred milliseconds of in-flight writes **only** if those writes never
reached the leader's commit quorum; such writes are treated as having
never happened by the client (idempotency guarantees hold).

### 3. Fail-closed behavior on the write path

A request that needs to mutate state MUST NOT be acknowledged success
unless the mutation is durable on a quorum. Concretely:

1. Leader accepts the request, appends an entry to its Raft log, and
   replicates to followers.
2. Leader waits up to `commit_timeout` (default 1500 ms) for a quorum
   acknowledgement.
3. If the quorum is reached: apply, respond `2xx`, propagate to
   downstream sinks (audit, events).
4. If the timeout expires: respond `503 leader_unavailable`. The
   client may retry; if the entry reached quorum silently later, the
   retry will see the idempotency record and return the stored
   response per ADR-0004.
5. If the leader steps down mid-flight, the new leader rejects any
   uncommitted entries; clients receive `503 leadership_lost` and
   retry.

### 4. Fail-closed behavior on the read path

- A read that participates in a security decision (e.g. "is this
  token revoked?") MUST go through a linearizable read. Local caches
  may not be used.
- A read that is purely informational (e.g. listing cached
  capabilities) MAY use a local cache with a bounded staleness budget.
  The budget is recorded in the response header
  `X-Gateway-Cache-Age-Ms`.

### 5. Replication transport

- mTLS is mandatory between replicas, using the same PKI as the
  ingress plane (ADR-0002). Each replica has a unique certificate
  with a SAN matching its role (`replica.<cluster>.<domain>`).
- Raft traffic runs over a dedicated listener that is not exposed
  publicly.
- Replicas MUST refuse Raft connections from peers whose certificate
  is not in the configured trust set.

### 6. Crash recovery

- On restart, a replica MUST rejoin the cluster as a follower, refuse
  to serve state-mutating requests until it has caught up to the
  leader's commit index, and refuse to serve linearizable reads until
  its log is within `staleness_budget` of the leader.
- The leader treats any follower that is more than
  `catchup_timeout` (default 30 s) behind as suspect and triggers a
  snapshot transfer. During the catch-up window, the lagging replica
  returns `503 warming_up` for state-mutating requests.

### 7. Backups and disaster recovery

- A periodic snapshot (default every 5 minutes) of the state machine
  is shipped to object storage, encrypted at rest with a KMS key.
- Snapshots are signed with the gateway's release key. Restoration
  MUST verify the signature before applying.
- Restoring from snapshot creates a **new** cluster identity (new
  cluster UUID, new Raft peer set). Tokens issued before the restore
  are considered revoked by default; operators MUST reissue tokens
  after a restore.

### 8. Observability

Each replica exports:

- `rag_commit_index` - latest applied index.
- `rag_is_leader` - 1 if leader, 0 otherwise.
- `rag_replication_lag_ms` - lag behind the leader.
- `rag_idempotency_records` - live record count.

A replica whose `rag_is_leader = 0` and `rag_replication_lag_ms > 5000`
fires a `critical` alert. Operators MUST investigate before that
replica is allowed to serve linearizable reads.

### 9. Implementation notes

- The package `internal/state` owns the Raft client wrapper, the
  state-machine interface, and snapshot management.
- The skeleton ships a single-node in-memory implementation behind
  the same interface so unit tests can run without a real Raft
  cluster. The interface boundary is the only thing production code
  depends on.
- All fail-closed branches are unit tested: leader down, quorum loss,
  follower lag, snapshot restore, and certificate mismatch on the
  replication listener.

## Consequences

Positive:
- State mutations are durable and consistent across replicas.
- A single replica crash does not violate any security guarantee.
- Read-heavy endpoints remain fast on the happy path.

Negative / Risks:
- Raft adds operational complexity (snapshot timing, peer set
  management, leader election tuning).
- The default `commit_timeout` of 1500 ms sets a ceiling on tail
  latency for state-mutating endpoints; if we observe tail latency
  regressions in production we will revisit the trade-off.

## Failure modes (mandatory section)

- **No leader elected.** Fail-closed: every replica returns
  `503 no_leader` for state-mutating requests. Reads are denied.
- **Quorum lost mid-write.** Fail-closed: `503 leader_unavailable`.
  The client retries; idempotency keys ensure at-most-once execution.
- **Follower lag exceeds staleness budget.** Fail-closed for security
  reads: forward to leader or `503 stale_follower`.
- **Snapshot signature mismatch on restore.** Fail-closed: refuse to
  boot the new cluster until an operator with the release key
  acknowledges in an out-of-band channel.
- **Replication peer presents an untrusted certificate.** Fail-closed:
  drop the connection and emit `severity=critical` audit.
- **Snapshot is older than 24 hours.** Fail-closed: refuse automatic
  restore; require an operator decision.