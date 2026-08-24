# Threat Model — OpenPocket + ZAG Stack (STRIDE per element)

> **Scope**: This document covers the security threats across all trust
> boundaries in the OpenPocket + ZAgentGateway (ZAG) stack. It is the
> authoritative reference for the threat-facing surface that the automated
> test suite in `backend/security/` and the release gates in
> `docs/security/02-release-gates.md` are designed to enforce.
>
> **Status**: Living document. Updated each time a new boundary, threat,
> or mitigation is identified. See the matching `evidence-ledger.md` for
> evidence provenance per mitigation.
>
> **Trust domains**: see `docs/新架构v1/01-architecture/安全模型.md` for
> the canonical trust-domain diagram.
>
> **Date / owner**: 2026-08-23 / ZAG security working group.

---

## 0. How to read this document

For each element (boundary / component) we walk STRIDE:

| Letter | Threat |
|--------|--------|
| **S** | Spoofing — an attacker impersonates an actor (user, service, tenant). |
| **T** | Tampering — unauthorized modification of state in transit or at rest. |
| **R** | Repudiation — an actor denies an action that took place. |
| **I** | Information disclosure — exposure of secrets, PII, or metadata. |
| **D** | Denial of service — resource exhaustion that breaks availability. |
| **E** | Elevation of privilege — lower-privileged actor gains higher capability. |

For each cell we record: **Mitigation** (what we already do or will do),
and **Test** (which file/line in `backend/security/` enforces it). Tests
are gate-enforced via the `security` build tag — they are not on the hot
default CI path so they can do heavier work (fuzzing, integration).

---

## 1. Element: ACC → ZAG boundary (token / mTLS)

> ACC (Agent Control Center, `acc-go`) dispatches canonical tasks to ZAG.
> Both sides trust each other as service principals, but **never as users**.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | ACC instance spoofs another ACC instance to push tasks as another tenant. | (a) mTLS with leaf cert issued by managed CA, SAN pinned to instance ID. (b) delegated JWT carries `iss=acc` and `tenant_id` from the ACC principal record. (c) ZAG verifies `iss ∈ allowlist` and `aud=zagent-gateway`. | `cross_tenant_test.go::TestACCIssNotInAllowlist`, `token_test.go::TestTokenAudienceMismatchRejected`, `token_test.go::TestTokenIssuerOutsideAllowlist` |
| **T** | Task payload tampered in flight (e.g. goal text or pinned-pod-id changed). | (a) request signature bound to `method\|path\|body-hash\|tenant\|subject\|issued_at\|expires_at\|nonce\|key_id`. (b) ZAG stores nonce/jti, rejects replay. (c) canonical JSON for high-risk ops. | `token_test.go::TestNonceReplayRejected`, `idempotency_test.go::TestIdempotencyKeyReplayAcrossReplicas`, `cross_tenant_test.go::TestSignedPayloadTamperRejected` |
| **R** | ACC denies pushing a task (forensic dispute about what got dispatched). | (a) ZAG emits `task.received` audit event with `request_id`, `tenant_id`, body digest. (b) ACC mirrors the request_id in its own audit table. (c) clock skew bounded. | `audit_outbox_test.go::TestAuditOutboxAppendRecovery`, `audit_outbox_test.go::TestAuditReplayIntegrity` |
| **I** | Body contains provider API keys, SSH keys, vault blobs. | (a) redactor (`server/audit_writer.go::redactDetail`) masks known sensitive keys. (b) log allowlist at logger level. (c) no echo of `Authorization` header in error envelopes. | `audit_outbox_test.go::TestAuditDetailRedactsProviderKeys`, `path_traversal_test.go::TestEnvAndSSHKeysNeverLeaveWorkspace` |
| **D** | ACC burst-floods ZAG with duplicate task submissions. | (a) write ops require `Idempotency-Key`. (b) rate-limit per tenant in ZAG. (c) bounded request body (1 MiB by default). | `idempotency_test.go::TestDuplicateIdempotencyKeyIsNoOp`, `failover_test.go::TestZAGRejectsOversizedPayload` |
| **E** | ACC pushes a high-risk control command (`pod.terminate`, `rollback`) that needs a second approver. | (a) ZAG `RiskClass(OpControlPod) = RiskHigh/Critical`. (b) ZAG must reject when only one signer is present; canonical payload must include second signer's pubkey id. (c) ZAG does NOT hold the second admin private key. | `cross_tenant_test.go::TestDualSignerRequiredForCriticalOps`, `cross_tenant_test.go::TestZAGDoesNotHoldSecondAdminKey` |

---

## 2. Element: ZAG → RedClaw boundary

> ZAG forwards operations into RedClaw platform-go's task/run/approval
> surface. RedClaw holds the source-of-truth state for tasks/runs in
> enterprise installations.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Rogue ZAG instance spoofs a legitimate ZAG instance. | (a) mTLS leaf cert with short TTL (≤24h). (b) cert SAN pinned to ZAG instance id. (c) RedClaw trust-store is allowlist-managed, no auto-trust of unknown CA. | `cross_tenant_test.go::TestMTLSRevokedCertRejected`, `cross_tenant_test.go::TestMTLSUnknownCARefused` |
| **T** | Task state tampered between ZAG and RedClaw (e.g. status forced to `done`). | (a) signed request as in §1. (b) RedClaw enforces aggregate version on updates; ZAG must read-then-write through reconcile. (c) every state change has an event_id + sequence. | `reconcile_test.go::TestRedClaw504TriggersIndeterminate`, `reconcile_test.go::TestProjectionUnavailableTriggersReconcile` |
| **R** | Either side denies taking an action. | (a) durable outbox in ZAG (see §4). (b) RedClaw audit table with append-only WORM archive. | `audit_outbox_test.go::TestAuditOutboxReplayOnRestart`, `audit_outbox_test.go::TestAuditChainContinuityAfterRestart` |
| **I** | Tenant A's task spec leaked to Tenant B. | (a) every payload carries `tenant_id` claim; RedClaw routes by tenant. (b) ZAG never accepts cross-tenant forwarding. (c) RedClaw reject for `X-Tenant-ID != token.tenant_id`. | `cross_tenant_test.go::TestCrossTenantReadReturns404`, `cross_tenant_test.go::TestCrossTenantWriteReturns403`, `cross_tenant_test.go::TestBodyTenantMismatchRejected` |
| **D** | RedClaw unreachable / slow → ZAG retries aggressively and amplifies load. | (a) timeout 30s default; retry only on idempotent ops. (b) `indeterminate` result triggers query/reconcile, NOT retry. (c) circuit breaker per upstream. | `reconcile_test.go::TestNoBlindRetryOnTimeout`, `failover_test.go::TestRedClawOutageTriggersCircuitBreak` |
| **E** | ZAG user (lower privileged) uses ZAG → RedClaw path to escalate. | (a) ZAG does not pass through end-user tokens; it always re-mints a delegated token with the minimal scope needed. (b) RedClaw verifies delegated token, not the original user JWT. | `token_test.go::TestUserTokenNotPassableAsServiceToken`, `cross_tenant_test.go::TestServiceScopeNarrowing` |

---

## 3. Element: ZAG → OpenCode boundary

> ZAG mediates between the control plane and the OpenCode runtime on the
> user PC. OpenCode holds the workspace, shell, git, and IDE handles.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Attacker spoofs the user (mobile) to invoke ZAG, which then commands OpenCode. | (a) mobile request must carry short-lived JWT (≤5min). (b) JWT is bound to device id (fingerprint) and refresh requires re-auth. (c) OpenCode never trusts the inbound user; it trusts the ZAG signed command. | `token_test.go::TestShortLivedDelegatedToken`, `cross_tenant_test.go::TestWSOriginEnforced` |
| **T** | OpenCode session state (messages, file edits) tampered. | (a) OpenCode session id is opaque and not user-guessable; canonical payload signed by ZAG. (b) `event_id + sequence + aggregate_version` on every event. (c) `Last-Event-ID` server-side validation on reconnect. | `sse_ws_reliability_test.go::TestSSEReconnectLastEventID`, `sse_ws_reliability_test.go::TestEventDedupByEventID` |
| **R** | User denies sending a particular prompt or tool-call. | (a) audit trail in ZAG outbox includes `actor`, `delegator`, `device`, `prompt-hash`, `ip`, `latency`, `result`. (b) immutable archive in Memora. | `audit_outbox_test.go::TestAuditOutboxCapturesActorAndDelegator` |
| **I** | Workspace contents (source code, secrets, prompt text) leaked via OpenCode events. | (a) data classification: code/diff/secret never default-log. (b) redactor on audit detail. (c) SSE events carry `summary` only; full body requires explicit `?expand=1` and is gated by scope. | `audit_outbox_test.go::TestAuditDetailNeverIncludesCodeOrSecret`, `path_traversal_test.go::TestWorkspaceSecretsNotExposed` |
| **D** | Mobile client connects many sockets and floods OpenCode session events. | (a) per-WS ticket is single-purpose and short-lived. (b) channel buffer size bounded (`make(chan []byte, 256)` in `internal/websocket`). (c) slow consumer is dropped, not blocking other tenants. | `sse_ws_reliability_test.go::TestSlowConsumerDroppedNotBlocking`, `sse_ws_reliability_test.go::TestWSChannelBufferBounded` |
| **E** | Mobile viewer (read-only) pushes a high-risk IDE command. | (a) scope check at ZAG (must hold `ide:write`). (b) IDE connector allowlist at OpenCode. (c) command schema, not raw strings (per `IDECommand` type). | `cross_tenant_test.go::TestIDECommandWithoutScopeRejected`, `path_traversal_test.go::TestIDEShellInjectionBlocked` |

---

## 4. Element: ZAG → Memora boundary

> Memora is the long-term memory layer; ZAG writes audit summaries,
> Charter entries, and Skill blobs there. It is NOT a substitute for
> the durable audit storage (per ADR-0007).

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Spoofed Memora namespace claiming a tenant it doesn't own. | (a) Memora namespace key = `pocketfleet/{tenant}/...` derived from JWT `tenant_id`. (b) write requires tenant-claim in delegated token. | `cross_tenant_test.go::TestMemoraNamespacePinnedToTenant` |
| **T** | Memora entry tampered (Skill content rewritten). | (a) Memora stores immutable events; updates create new revisions. (b) ZAG computes content hash and pins it in the outbox row. | `audit_outbox_test.go::TestMemoraRevisionChainDetectsTamper` |
| **R** | Audit loss between ZAG and Memora. | (a) audit must land in **durable append-only outbox first**, THEN be forwarded to Memora. (b) Memora downtime ≠ audit loss. | `audit_outbox_test.go::TestAuditWrittenBeforeUpstreamAction` |
| **I** | Skill blob leaks another tenant's data. | (a) Skill content is hashed + cross-tenant scoped at write time. (b) read ACL on the namespace. | `cross_tenant_test.go::TestSkillReadCrossTenantDenied` |
| **D** | Memora outage → ZAG retries and fills local buffer. | (a) bounded buffer + circuit breaker. (b) high-priority audit writes never block on Memora. | `failover_test.go::TestMemoraOutageDoesNotBlockAudit` |
| **E** | Memora namespace admin scope escalation. | (a) Memora scopes are pinned to tenant at token-mint time; cannot be re-scoped from request body. | `cross_tenant_test.go::TestMemoraScopeNotEscalatable` |

---

## 5. Element: ZAG → LLM providers

> ZAG (via llm-gateway-go) is the only path to LLM providers. The
> provider endpoint, key, and tenant residency must all be tightly
> controlled.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Spoofed provider endpoint (LLM traffic redirected to attacker). | (a) provider URL pinned in config + tenant residency allowlist. (b) certificate pinning at TLS layer for known providers. | `ssrf_test.go::TestProviderURLAllowlistEnforced`, `ssrf_test.go::TestProviderDNSRebindRejected` |
| **T** | Provider API key leaked via logs / audit detail. | (a) redactor (`audit_writer.go::redactDetail`) masks `api_key`, `apikey`, `authorization`, `token`, `access_token`, `refresh_token`, `private_key`. (b) provider keys never enter audit outbox. | `audit_outbox_test.go::TestProviderKeyNotInAuditDetail`, `path_traversal_test.go::TestEnvAndSSHKeysNeverLeaveWorkspace` |
| **R** | Provider denies receiving a particular prompt. | (a) provider request id propagated as `X-Correlation-ID`; mirrored in audit. (b) idempotency-key propagated for write-capable calls. | `idempotency_test.go::TestIdempotencyKeyPropagatedToProvider` |
| **I** | Provider response includes other tenant data (because of shared proxy). | (a) per-tenant routing in llm-gateway. (b) response is bound to request id; mismatched response is dropped. | `cross_tenant_test.go::TestProviderResponseBoundToTenant` |
| **D** | Provider outage / slow → ZAG exhausts local pool. | (a) per-tenant concurrency budget. (b) global circuit breaker; fallover to secondary provider within tenant residency. | `failover_test.go::TestLLMOutageTriggersCircuitBreak`, `failover_test.go::TestLLMFallbackRespectsResidency` |
| **E** | User-side prompt attempts to escalate model (e.g. GPT-5 via internal path). | (a) model allowlist per tenant. (b) the prompt is not authoritative for routing; routing comes from request headers / config. | `cross_tenant_test.go::TestModelEscalationBlocked` |

---

## 6. Element: Pocket browser ↔ ZAG (WebSocket + SSE)

> The mobile web client (Capacitor/Vue) talks to ZAG via WebSocket
> (events) and SSE (long-poll fallback). All auth material must be
> non-leaky and the boundary must reject cross-tenant subscriptions.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Forged Origin / Origin-less CSWSH. | (a) server Upgrader has `CheckOrigin` enforced (`buildOriginChecker` in `server.go:232`). (b) mobile fallback WS in `mobile_api.go:529` MUST be removed before GA (currently `return true`). | `cross_tenant_test.go::TestWSOriginEnforced`, `cross_tenant_test.go::TestWSUnknownOriginRejected` |
| **T** | WS messages tampered with proxy / MITM. | (a) `wss://` only; HSTS preload. (b) message integrity enforced via per-message signature on critical events. | `sse_ws_reliability_test.go::TestWSMessageIntegrityCheck` |
| **R** | WS client denies sending a particular event. | (a) client→server WS messages are limited to a small set (subscribe/ack/ping); no free-form control. (b) server-side audit on every control msg. | `sse_ws_reliability_test.go::TestWSControlChannelACL` |
| **I** | Query-string JWT leaked via proxy logs. | (a) no long-lived JWT in query string; use Authorization header OR one-shot ticket. (b) ticket exchange endpoint short-lived + single use. | `sse_ws_reliability_test.go::TestWSQueryTokenNotLogged`, `token_test.go::TestQueryJWTRejectedByDefault` |
| **D** | Slow consumer exhausts the hub channel. | (a) channel buffer 256; full buffer triggers unregister, not backpressure across tenants. (b) per-tenant subscription cap. | `sse_ws_reliability_test.go::TestSlowConsumerDroppedNotBlocking` |
| **E** | WS client subscribes to arbitrary `task_id` / `pod_id` outside its tenant. | (a) every subscribe is checked against JWT `workspace_id` and resource ACL. (b) reject with stable error code, no info leak. | `cross_tenant_test.go::TestWSSubscribeCrossTenantReturnsForbidden` |

---

## 7. Element: IDE connector ↔ ZAG

> The IDE connector is the bridge between ZAG and the user's local
> IDE (ZCode/VS Code/Cursor/OpenCode-compatible facade). It must obey
> the command schema and the workspace sandbox.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Spoofed IDE connector command (fake plugin claims to be a real IDE). | (a) connector endpoint pinned in ZAG instance config (no arbitrary URL). (b) mTLS to connector. (c) per-Pod enrollment token. | `ssrf_test.go::TestConnectorURLAllowlist`, `cross_tenant_test.go::TestConnectorMTLSRequired` |
| **T** | File content / shell output tampered in the connector. | (a) argv-only command execution (no shell strings). (b) workspace canonicalize + symlink resolution before write. (c) idempotency-key on every IDE write. | `path_traversal_test.go::TestIDEShellInjectionBlocked`, `path_traversal_test.go::TestSymlinkEscapeBlocked` |
| **R** | IDE denies applying a diff. | (a) every IDE write is auditable with input/output digests. (b) connector echoes `operation_id` for ack. | `audit_outbox_test.go::TestIDEWriteAuditHasInputDigest` |
| **I** | Workspace secrets (`~/.ssh/id_rsa`, `~/.aws/credentials`) read by IDE connector. | (a) workspace path allowlist excludes home-dir secrets. (b) command args schema rejects `~/.ssh/...` / `~/.aws/...` paths. | `path_traversal_test.go::TestSSHPEMPathRejected` |
| **D** | Connector offline / slow. | (a) timeout 10s; circuit breaker; high-risk ops block on outbox ack. | `failover_test.go::TestIDEConnectorOfflineTriggersBackoff` |
| **E** | `viewer` role pushes `shell.run` via IDE connector. | (a) capability check at ZAG (`permission:approve`, `ide:write`). (b) connector must verify scope on each call. | `cross_tenant_test.go::TestIDERequiresIDEWriteScope` |

---

## 8. Element: Multi-tenant data plane

> Covers the persistent stores (Postgres, Redis, object storage,
> audit archive). Every store operation must carry tenant + actor and
> be filtered by `workspace_id`.

| STRIDE | Threat | Mitigation | Test |
|--------|--------|-----------|------|
| **S** | Spoofed tenant by setting `X-Tenant-ID` header. | (a) header MUST be ignored when JWT is present; tenant comes from claims only. (b) handler-level workspace binding in `auth/approval_scope.go::ValidateScope`. | `cross_tenant_test.go::TestXTenantHeaderIgnoredWhenJWTPresent`, `cross_tenant_test.go::TestBodyTenantMismatchRejected` |
| **T** | Tenant-A query that uses Tenant-B's `user_id` (because of weak natural keys). | (a) every table has `(workspace_id, ...)` composite predicates. (b) helper `tenantFromContext` centralizes the rule. | `cross_tenant_test.go::TestCrossTenantReadReturns404`, `cross_tenant_test.go::TestCrossTenantWriteReturns403` |
| **R** | Audit log mutability: tenant could erase evidence. | (a) audit store is append-only / WORM. (b) audit chain continuity check (hash of prev + new). | `audit_outbox_test.go::TestAuditChainContinuityAfterRestart`, `audit_outbox_test.go::TestAuditImmutableAfterClose` |
| **I** | PII / secret fields accidentally logged. | (a) `audit_writer.go::redactDetail` runs at write boundary. (b) logger field allowlist at `log/slog`. (c) data-classification register maps each field to retention. | `audit_outbox_test.go::TestAuditDetailRedactsProviderKeys` |
| **D** | One tenant floods the audit store → other tenants blocked. | (a) per-tenant quota. (b) bounded buffer + backpressure. (c) tiered retention. | `failover_test.go::TestAuditStorePerTenantQuota` |
| **E** | Shared admin key from a ZAG operator could re-sign a high-risk op. | (a) second signer must come from a **separate identity** (independent device / approver service). (b) ZAG MUST NOT hold the second admin private key (asserted in test). | `cross_tenant_test.go::TestZAGDoesNotHoldSecondAdminKey`, `cross_tenant_test.go::TestDualSignerRequiredForCriticalOps` |

---

## 9. Cross-cutting requirements (apply to every element above)

| Topic | Rule | Test reference |
|------|------|---------------|
| Idempotency | Every write op MUST carry `Idempotency-Key`. ZAG persists it for ≥24h. | `idempotency_test.go::*` |
| Replay / nonce | Every signed request MUST carry `nonce`; server persists `jti+nonce` for ≥1h. | `token_test.go::TestNonceReplayRejected` |
| Clock skew | Reject `|iat - now| > 60s`. | `token_test.go::TestTokenExpiredAndClockSkew` |
| Body size | All inbound bodies ≤ 1 MiB unless explicitly allowed (LLM streaming). | `failover_test.go::TestZAGRejectsOversizedPayload` |
| Logging | Never log raw Authorization header / cookies / JWT. | `audit_outbox_test.go::TestAuthorizationHeaderNeverInLog` |
| Build tag | All security tests gated by `//go:build security`. | n/a — enforced by Go build. |

---

## 10. Test count per element (planned + implemented)

| Element | Implemented | Planned | Total |
|---------|-------------|---------|-------|
| ACC → ZAG | 0 | 8 | 8 |
| ZAG → RedClaw | 0 | 9 | 9 |
| ZAG → OpenCode | 0 | 8 | 8 |
| ZAG → Memora | 0 | 6 | 6 |
| ZAG → LLM providers | 0 | 7 | 7 |
| Pocket ↔ ZAG (WS+SSE) | 0 | 9 | 9 |
| IDE connector ↔ ZAG | 0 | 8 | 8 |
| Multi-tenant data plane | 0 | 9 | 9 |
| Cross-cutting | 0 | 6 | 6 |
| **Total** | **0** | **70** | **70** |

(Test names appear in `docs/security/01-test-matrix.md`.)

---

## 11. When this threat model must be re-reviewed

- Any time a new external integration is added (e.g. new LLM provider).
- Any time a new high-risk command (shell, git push, pod control) is added to the command schema.
- Any time `docs/新架构v1/01-architecture/安全模型.md` changes the trust boundary topology.
- After every security incident report (regardless of severity).
- Quarterly, as part of the standard review cycle.
