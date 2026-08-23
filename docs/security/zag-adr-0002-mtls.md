# ADR-0002: ZAgentGateway mTLS

- **Status:** Proposed (M0 baseline)
- **Date:** 2026-08-23
- **Scope:** all mTLS-protected connections **originated by** ZAG (`pocketd` ← ZAG, `acc-go` ← ZAG, `redclaw` ← ZAG, `memora` ← ZAG, IDE connectors ← ZAG). This ADR is the outbound half; the inbound mTLS terminator on ZAG's own listener follows the same root CA and is exercised by the same tests.
- **Supersedes:** none.

---

## Context

ZAG sits between a mobile app (`OpenPocket`), an enterprise control surface (`RedClaw`), and a task orchestrator (`acc-go`). The ZAG security model (`docs/新架构v1/02-modules/zagent-gateway.md:594-595` and `:819-829`) calls out three independent layers:

1. mTLS — transport identity and channel confidentiality,
2. delegated token — caller identity, tenant, scope,
3. RBAC/ABAC (ADR-0003) — what the caller is allowed to do.

Without mTLS, the bearer token rides plaintext (or any TLS that the operator forgot to enable), and an attacker who can MITM the network can harvest long-replay tokens. The audit handed to this repo concluded that no mTLS enrollment, rotation, or revocation plan exists yet for ZAG.

We also need to be specific about what happens when the CA is unreachable. A control-plane gateway that "degrades gracefully" by skipping mTLS is not a control-plane gateway.

---

## Decision

### 1. Pick: **SPIFFE/SPIRE** for enrollment, with a managed intermediate CA.

We pick **SPIRE** as the enrollment authority. SPIRE is the only option that natively supports:

- **Workload identity (SVID):** every ZAG replica, every `redclaw` agent, every `acc-go` worker receives a short-lived X.509 SVID (default 1 hour, capped at 24 hours) bound to its SPIFFE ID `spiffe://<trust-domain>/<path>`.
- **Automatic rotation:** the agent renews SVIDs before expiry; rotation is a property of the agent, not an out-of-band script.
- **Revocation:** SPIRE's `svid-expiry` plus its federated-bundle revocation list feed our cache. The SVID is short enough that "just wait for expiry" is a 1-hour worst-case bound.
- **CA rollover:** SPIRE supports a dual-CA grace window (old + new roots both trusted) so we can rotate the upstream root without breaking in-flight connections.
- **Multi-environment (dev / staging / prod):** each environment has its own trust domain; SVIDs from `staging` are rejected by `prod` workloads.

We considered and rejected:

- **SCEP / EST** — These are device-cert protocols designed for routers and laptops, not for short-lived, automated service identity. Rotation is manual or scripted; revocation is per-CA-file with no cache; federation is non-existent. Adequate for fleet MDM, wrong shape for control-plane mTLS.
- **cert-manager + ACME (Let's Encrypt or internal ACME)** — Issues public-or-internal certs but binds them to a DNS name, not a workload identity. Revocation is `kubectl delete certificate` plus CRL/OCSP that has to be plumbed separately. No native SVID; rotation windows are configured by hand.
- **Manual `openssl` + cert file drop** — Acceptable for a 5-replica dev box, untenable for the 2-3 replicas we plan (`docs/新架构v1/02-modules/zagent-gateway.md:907-909`) once we account for blue/green rollouts and emergency replacement.

### 2. Trust model

- One **offline root CA** per environment (dev / staging / prod). The root is kept offline; only the issuing intermediate signs workload SVIDs.
- One or more **SPIRE server intermediates** (HA pair) issue SVIDs to SPIRE agents running on each ZAG node and each peer (`pocketd`, `acc-go`, `redclaw`).
- The trust bundle (`spiffe://<trust-domain>/...`) is published by the SPIRE server federation endpoint and refreshed every 5 minutes by every workload.

### 3. Certificate contents (X.509 / SVID)

| Field | Value |
| --- | --- |
| `Subject.CN` | not authoritative; SPIFFE ID is in SAN URI |
| `SAN.URI` | `spiffe://<trust-domain>/ns/<namespace>/sa/<service-account>` |
| `SAN.DNS` | optional, for legacy clients |
| `KeyUsage` | `digitalSignature`, `keyEncipherment` |
| `ExtKeyUsage` | `serverAuth`, `clientAuth` (we are both client and server) |
| `NotBefore` | now |
| `NotAfter` | now + 1h, capped at 24h |
| `SigAlg` | `ECDSA-with-SHA256` (P-256) or `Ed25519` |

### 4. Enrollment flow (one-time, per replica)

1. ZAG process starts and loads the SPIRE agent socket (`/run/spiffe/agent.sock`).
2. SPIRE agent performs its **node attestation** against the cloud provider's metadata service (or a bootstrap token in on-prem). The agent proves it is the same machine that was registered.
3. The agent calls `FetchX509SVID()` and receives an SVID + key + bundle + expiry.
4. ZAG supplies the SVID to its `tls.Config` (incoming listener and outgoing HTTP client).
5. ZAG also fetches the federated bundle for the peer trust domains (`pocketd`, `acc-go`, `redclaw`, `memora`) so it can verify their SVIDs.
6. After 30 minutes, the agent automatically renews; ZAG hot-reloads the `tls.Config` without dropping in-flight connections (the old certificate is retained until its natural expiry).

### 5. Rotation cadence

- **SVID rotation:** every 1 hour (configurable, default 1h; max 24h).
- **Bundle rotation:** every 5 minutes; new bundles are additive (old trusted until 24h after last seen).
- **Root CA rotation:** planned annual rotation with a 2-week dual-trust overlap. SPIRE's `ca_ttl` and `x509svid_ttl` knobs drive the schedule.

### 6. Revocation

Two layers:

1. **SVID expiry (automatic).** Because the SVID lifetime is 1 hour, an unrevoked but compromised SVID becomes inert in at most 60 minutes. This is the baseline.
2. **Active revocation.** When ZAG detects compromise (lost device, suspicious log), it calls SPIRE's revocation API:
   - `POST /v1/agents/<id>/revoke` — revokes the agent; all its SVIDs are added to the CRL.
   - `POST /v1/entries/<id>/revoke` — revokes a specific registration entry.
   - SPIRE republishes the CRL to the federation endpoint within 10 seconds. ZAG's next bundle refresh (≤ 5 minutes) sees the revoked entry.
   - For an in-flight emergency (the 5-minute bundle window is too long), ZAG also drops the SVID's public-key fingerprint into its local `zag_auth_revocation` (shared with ADR-0001 §7) so the immediate connection is rejected even before the bundle refresh.

### 7. CA rollover

When the offline root rotates:

1. Generate a new root key + cert in the offline CA. Sign the new intermediate(s).
2. Publish the new root's cert into SPIRE's "rotated root" slot. SPIRE now issues SVIDs that chain to **either** the old or the new root, distributing both bundles to agents.
3. Wait `max(SVID TTL) + grace` (24h + 1h) so all in-flight SVIDs that chain to the old root have expired.
4. Demote the old root to "retired"; remove from the bundle.
5. Destroy the old root key in a witnessed ceremony.

This is a standard SPIRE rollover and is exercised by the chaos tests in `docs/security/zag-test-matrix.md`.

### 8. Failure mode (fail-closed)

The CA is the trust root. If ZAG cannot complete mTLS — for any reason — it must **deny the connection**.

| Condition | Behavior | Error code |
| --- | --- | --- |
| SPIRE agent socket missing | process exits non-zero at startup; never accepts traffic | `ZAG_TLS_AGENT_MISSING` |
| Initial SVID fetch fails | process exits non-zero | `ZAG_TLS_SVID_UNAVAILABLE` |
| SVID renewal fails (transient, < 5 min) | process serves with the still-valid SVID; retries renewal; if expiry passes, process exits | `ZAG_TLS_SVID_EXPIRED` |
| SVID renewal fails and no valid SVID exists | process exits | `ZAG_TLS_NO_VALID_SVID` |
| Peer presents SVID outside the trust bundle | connection rejected | `ZAG_TLS_BAD_PEER` |
| Peer presents SVID with wrong SAN.URI (e.g. wrong workload) | connection rejected | `ZAG_TLS_WRONG_IDENTITY` |
| Bundle refresh fails | ZAG keeps the last known good bundle; when **all** bundles are older than 24h, process exits | `ZAG_TLS_BUNDLE_STALE` |
| CRL entry seen (peer revoked) | connection rejected | `ZAG_TLS_PEER_REVOKED` |
| Caller disables mTLS in config | refused at startup; production mode rejects `ZAG_TLS_MODE=insecure` outright | `ZAG_TLS_MODE_REJECTED` |

**There is no "fall back to plaintext" path. There is no "fall back to HMAC-only" path.** A control-plane gateway that downgrades its transport on CA failure is by definition no longer a control-plane gateway. This is asserted in the security model at `docs/新架构v1/02-modules/zagent-gateway.md:594-595`.

---

## Consequences

### Positive

- Workload identity, not DNS name, is the binding. A misconfigured DNS cannot impersonate a ZAG replica.
- Rotation is automatic and bounded (1h).
- Revocation is two-layered: short SVID TTL plus an active CRL.
- CA rollover is a documented ceremony with a 24h + 1h dual-trust window.

### Negative

- Operating SPIRE is a non-trivial dependency. Each new environment (dev, staging, prod) needs its own SPIRE server HA pair. We accept this — ZAG is the new control-plane entry point and deserves a real PKI.
- The first deploy takes longer because the SPIRE servers have to be brought up before ZAG can start.

### Neutral

- We do not introduce a new revocation primitive beyond SPIRE + local fingerprint cache; ADR-0001's `jti` revocation list and this ADR's CRL share the same `zag_auth_revocation` table.

---

## Acceptance criteria

1. A ZAG replica that has no SPIRE agent socket exits at startup with `ZAG_TLS_AGENT_MISSING` (verified in unit + chaos test).
2. A ZAG replica whose SPIRE agent returns a valid SVID accepts an mTLS connection from a peer presenting a SVID chained to the trust bundle.
3. A peer presenting a SVID with the wrong SPIFFE ID (e.g. `acc-go` SA talking to a ZAG endpoint that expects `redclaw`) is rejected with `ZAG_TLS_WRONG_IDENTITY`.
4. A peer whose SVID has been revoked via SPIRE's revoke API is rejected within 5 minutes (the next bundle refresh).
5. After the SVID TTL elapses with no successful renewal, the ZAG replica exits with `ZAG_TLS_SVID_EXPIRED`.
6. Production startup with `ZAG_TLS_MODE=insecure` is refused (`ZAG_TLS_MODE_REJECTED`).
