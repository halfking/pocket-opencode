# Security Test Matrix

> **Source of truth**: Every attack category listed in the session handoff
> (ZAG security baseline subtask E) is mapped to one or more test names
> below, each tagged with status and file:line.
>
> **Status values**:
> - **planned** — test exists in the suite but is skipped or marked
>   `t.Skip("requires ZAG implementation")`.
> - **implemented** — test runs to completion against the real code
>   path. Marked PASS only after a green `go test` run.
>
> **Build tag**: All tests in `backend/security/` are gated by
> `//go:build security`. They are NOT in the default CI path. Use
> `go test -tags=security ./backend/security/...` to run them.
>
> **Date / owner**: 2026-08-23 / ZAG security working group.

---

## 1. Cross-tenant access

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Cross-tenant read | `TestCrossTenantReadReturns404` | Tenant A's GET for Tenant B's resource returns 404 (not 403 — no existence leak). | `backend/security/cross_tenant_test.go:46` | implemented |
| Cross-tenant write | `TestCrossTenantWriteReturns403` | Tenant A's POST/PUT/DELETE for Tenant B's resource returns 403. | `backend/security/cross_tenant_test.go:89` | implemented |
| Cross-tenant event subscribe | `TestCrossTenantSSESubscribeReturnsForbidden` | SSE subscribe to Tenant B's stream is closed with stable error. | `backend/security/cross_tenant_test.go:131` | implemented |
| Cross-tenant WS subscribe | `TestCrossTenantWSSubscribeReturnsForbidden` | WS upgrade succeeds but first subscribe message returns forbidden, then closes. | `backend/security/cross_tenant_test.go:170` | implemented |
| Cross-tenant task payload | `TestCrossTenantTaskBodyTenantMismatchRejected` | Submit task with `tenant_id` in body that differs from JWT `tenant_id`. | `backend/security/cross_tenant_test.go:209` | implemented |
| Header-vs-claims mismatch | `TestXTenantHeaderIgnoredWhenJWTPresent` | `X-Tenant-ID` header value is ignored when a valid JWT is present. | `backend/security/cross_tenant_test.go:243` | implemented |

## 2. Token — audience / issuer / expiry

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Wrong audience | `TestTokenAudienceMismatchRejected` | JWT with `aud=other-service` is rejected. | `backend/security/token_test.go:48` | implemented |
| Issuer not in allowlist | `TestTokenIssuerOutsideAllowlist` | JWT with `iss=unknown` is rejected. | `backend/security/token_test.go:75` | implemented |
| Expired token | `TestTokenExpired` | JWT with `exp` in the past → 401. | `backend/security/token_test.go:102` | implemented |
| Clock skew beyond 60s | `TestTokenClockSkewRejected` | JWT with `iat` 10 minutes in the future → 401. | `backend/security/token_test.go:128` | implemented |
| None alg | `TestTokenNoneAlgRejected` | `alg=none` token → 401 (no signing bypass). | `backend/security/token_test.go:155` | implemented |
| HMAC vs RSA confusion | `TestTokenWrongSigningMethodRejected` | RSA-signed token submitted to HMAC-only verifier → 401. | `backend/security/token_test.go:178` | implemented |

## 3. Token — revocation / nonce / replay

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Revoked token | `TestRevokedTokenRejected` | After `Revoke(jti)`, the same token returns 401. | `backend/security/token_test.go:204` | implemented |
| Nonce replay | `TestNonceReplayRejected` | Same `nonce` reused within the TTL window → 401 with `nonce_replayed`. | `backend/security/token_test.go:233` | implemented |
| Signature tampering | `TestSignatureTamperingRejected` | Flipping one byte in the signature segment → 401. | `backend/security/token_test.go:262` | implemented |
| Body tampering after sign | `TestBodyHashTamperingAfterSignatureRejected` | Body hash mismatch → request rejected. | `backend/security/token_test.go:288` | implemented |
| Short TTL enforced | `TestShortLivedDelegatedToken` | 10-minute TTL accepted; 24-hour TTL rejected at boundary. | `backend/security/token_test.go:315` | implemented |
| User token not passable as service | `TestUserTokenNotPassableAsServiceToken` | Audience mismatch for service scope → 401. | `backend/security/token_test.go:345` | implemented |

## 4. Idempotency

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Duplicate `Idempotency-Key` | `TestDuplicateIdempotencyKeyIsNoOp` | Second submit with same key returns the original result; no second execution. | `backend/security/idempotency_test.go:42` | implemented |
| Key + body mismatch | `TestIdempotencyKeyBodyMismatchRejected` | Same key, different body → 409 conflict. | `backend/security/idempotency_test.go:80` | implemented |
| Replay across replicas | `TestIdempotencyKeyReplayAcrossReplicas` | Two ZAG replicas receive the same key → only one side-effect. | `backend/security/idempotency_test.go:114` | implemented |
| Key TTL expired | `TestIdempotencyKeyTTLExpires` | After 24h, the same key is accepted again as a fresh request. | `backend/security/idempotency_test.go:148` | implemented |
| Missing key on write | `TestMissingIdempotencyKeyOnWriteRejected` | Write op without `Idempotency-Key` → 400. | `backend/security/idempotency_test.go:178` | implemented |

## 5. Signer independence (dual-signature)

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Dual-signer required | `TestDualSignerRequiredForCriticalOps` | `pod.terminate` with only one signer → 403. | `backend/security/cross_tenant_test.go:280` | implemented |
| Second key not in ZAG | `TestZAGDoesNotHoldSecondAdminKey` | ZAG config does NOT contain a second admin private key. | `backend/security/cross_tenant_test.go:315` | implemented |
| Same-device second signer | `TestSecondSignerFromSameDeviceRejected` | Both signers from same device id → 403. | `backend/security/cross_tenant_test.go:343` | implemented |
| Same-subject second signer | `TestSecondSignerSameSubjectRejected` | Both signers from same subject → 403. | `backend/security/cross_tenant_test.go:374` | implemented |

## 6. Path / shell / env safety

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Path traversal via `..` | `TestPathTraversalDotDotRejected` | Path containing `..` is rejected before any FS op. | `backend/security/path_traversal_test.go:42` | implemented |
| Absolute path outside workspace | `TestAbsolutePathOutsideWorkspaceRejected` | `/etc/passwd` is rejected. | `backend/security/path_traversal_test.go:72` | implemented |
| Symlink escape | `TestSymlinkEscapeBlocked` | Symlink inside workspace pointing outside → blocked. | `backend/security/path_traversal_test.go:104` | implemented |
| TOCTOU race | `TestTOCTOUPatternBlocked` | Validation + open done with the same FD; mid-flight rename is detected. | `backend/security/path_traversal_test.go:135` | implemented |
| Shell injection | `TestShellInjectionBlocked` | `; rm -rf /` style payload in argv gets argv-escaped or rejected. | `backend/security/path_traversal_test.go:170` | implemented |
| Env-var leakage | `TestEnvAndSSHKeysNeverLeaveWorkspace` | `SSH_AUTH_SOCK`, `*_KEY`, `*_TOKEN` are stripped before logging/audit. | `backend/security/path_traversal_test.go:202` | implemented |
| `~/.ssh/id_rsa` access | `TestSSHPEMPathRejected` | Reading `~/.ssh/id_rsa` is denied regardless of role. | `backend/security/path_traversal_test.go:233` | implemented |
| UNC / null byte | `TestUNCAndNullByteRejected` | `\\evil\share` and `\x00` are rejected. | `backend/security/path_traversal_test.go:262` | implemented |

## 7. SSRF / outbound network

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Provider URL allowlist | `TestProviderURLAllowlistEnforced` | URL outside the allowlist → 400. | `backend/security/ssrf_test.go:38` | implemented |
| DNS rebinding | `TestProviderDNSRebindRejected` | Hostname that resolves to private IP is rejected at dial time. | `backend/security/ssrf_test.go:72` | implemented |
| Redirect to internal | `TestRedirectToInternalBlocked` | HTTP 30x pointing to `127.0.0.1` is not followed. | `backend/security/ssrf_test.go:108` | implemented |
| Cloud metadata endpoint | `TestCloudMetadataEndpointBlocked` | `169.254.169.254` always blocked regardless of allow-private flag. | `backend/security/ssrf_test.go:142` | implemented |
| Connector URL allowlist | `TestConnectorURLAllowlist` | Connector endpoint not in pod registry → 400. | `backend/security/ssrf_test.go:175` | implemented |
| Loopback bypass | `TestLoopbackBypassRejected` | `127.0.0.1`, `::1`, `localhost` are blocked unless allow_private=true. | `backend/security/ssrf_test.go:208` | implemented |
| mTLS to upstream required | `TestMTLSRequiredForUpstream` | mTLS handshake failure is fail-closed, never falls back to HMAC. | `backend/security/ssrf_test.go:240` | implemented |

## 8. SSE / WebSocket reliability

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| WS Origin enforced | `TestWSOriginEnforced` | Request with disallowed `Origin` → 403 before upgrade. | `backend/security/sse_ws_reliability_test.go:42` | implemented |
| SSE reconnect via Last-Event-ID | `TestSSEReconnectLastEventID` | Client reconnects with `Last-Event-ID`; server replays missed events. | `backend/security/sse_ws_reliability_test.go:78` | implemented |
| Event deduplication | `TestEventDedupByEventID` | Re-delivered `event_id` is dropped on the client. | `backend/security/sse_ws_reliability_test.go:115` | implemented |
| Out-of-order events | `TestOutOfOrderEventsReorder` | Client reassembles based on `sequence` field. | `backend/security/sse_ws_reliability_test.go:150` | implemented |
| Slow consumer dropped | `TestSlowConsumerDroppedNotBlocking` | Full send buffer → connection closed; other tenants unaffected. | `backend/security/sse_ws_reliability_test.go:188` | implemented |
| WS subscribe ACL | `TestWSSubscribeCrossTenantReturnsForbidden` | Subscribe to other tenant's stream → server closes with stable code. | `backend/security/sse_ws_reliability_test.go:225` | implemented |
| Channel buffer bounded | `TestWSChannelBufferBounded` | Hub channel has hard upper bound; overflow → disconnect. | `backend/security/sse_ws_reliability_test.go:262` | implemented |
| WS query token not logged | `TestWSQueryTokenNotLogged` | Token in query string never appears in access log. | `backend/security/sse_ws_reliability_test.go:295` | implemented |
| WS heartbeat / ping | `TestWSHeartbeatPongs` | Server sends ping; client must pong within 30s. | `backend/security/sse_ws_reliability_test.go:328` | implemented |

## 9. Audit outbox

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Outbox written before action | `TestAuditWrittenBeforeUpstreamAction` | Durable row exists before upstream HTTP call. | `backend/security/audit_outbox_test.go:42` | implemented |
| Outbox replay on restart | `TestAuditOutboxReplayOnRestart` | After crash, outbox replays un-acked rows. | `backend/security/audit_outbox_test.go:78` | implemented |
| Integrity hash chain | `TestAuditChainContinuityAfterRestart` | Each row carries hash of previous + new content. | `backend/security/audit_outbox_test.go:115` | implemented |
| Redaction of detail | `TestAuditDetailRedactsProviderKeys` | `api_key=...` becomes `api_key=[REDACTED]`. | `backend/security/audit_outbox_test.go:152` | implemented |
| Code/secret not in detail | `TestAuditDetailNeverIncludesCodeOrSecret` | Even verbose error paths never carry source code or vault blobs. | `backend/security/audit_outbox_test.go:188` | implemented |
| Authorization header never logged | `TestAuthorizationHeaderNeverInLog` | slog / audit writer do not see `Authorization` value. | `backend/security/audit_outbox_test.go:222` | implemented |
| Append-only after close | `TestAuditImmutableAfterClose` | Once audit writer is closed, Record returns ErrImmutable. | `backend/security/audit_outbox_test.go:255` | implemented |

## 10. Reconciliation

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| Indeterminate classification | `TestIndeterminateOnWriteTimeout` | Timeout after request was sent → `ErrIndeterminate`, no retry. | `backend/security/reconcile_test.go:42` | implemented |
| No blind retry | `TestNoBlindRetryOnTimeout` | Caller does not auto-retry `ErrIndeterminate`. | `backend/security/reconcile_test.go:78` | implemented |
| RedClaw 504 | `TestRedClaw504TriggersIndeterminate` | 504 from RedClaw → caller classifies as indeterminate. | `backend/security/reconcile_test.go:115` | implemented |
| Projection unavailable | `TestProjectionUnavailableTriggersReconcile` | `projection_unavailable` code → reconciliation worker kicks in. | `backend/security/reconcile_test.go:150` | implemented |
| Event sequence gap | `TestEventSequenceGapDetected` | Missing `sequence` is detected; reconcile fills the gap. | `backend/security/reconcile_test.go:188` | implemented |

## 11. Failover

| Threat | Test name | Expected outcome | File:line | Status |
|--------|-----------|------------------|-----------|--------|
| OpenCode crash | `TestOpenCodeRuntimeCrashRecovered` | OpenCode process crash → ZAG reconnects with backoff. | `backend/security/failover_test.go:42` | implemented |
| RedClaw outage | `TestRedClawOutageTriggersCircuitBreak` | RedClaw 5xx burst → circuit opens; fallover to mock. | `backend/security/failover_test.go:80` | implemented |
| ACC outage | `TestACCOutageDoesNotBlockReads` | ACC down → reads succeed; writes return 503 with retry-after. | `backend/security/failover_test.go:118` | implemented |
| Memora outage | `TestMemoraOutageDoesNotBlockAudit` | Memora down → audit still durably written; Memora resync later. | `backend/security/failover_test.go:155` | implemented |
| LLM outage | `TestLLMOutageTriggersCircuitBreak` | LLM provider 5xx → circuit opens; secondary provider tried. | `backend/security/failover_test.go:192` | implemented |
| LLM fallback residency | `TestLLMFallbackRespectsResidency` | Fallback provider is in the same region/tenant residency. | `backend/security/failover_test.go:230` | implemented |
| Oversized payload | `TestZAGRejectsOversizedPayload` | Body > 1 MiB → 413 PayloadTooLarge. | `backend/security/failover_test.go:268` | implemented |
| Per-tenant quota | `TestAuditStorePerTenantQuota` | Tenant A filling quota does NOT block tenant B writes. | `backend/security/failover_test.go:300` | implemented |

---

## Summary

| Category | Tests | Implemented | Planned |
|----------|------:|------------:|--------:|
| Cross-tenant access | 6 | 6 | 0 |
| Token aud/iss/exp | 6 | 6 | 0 |
| Token revoke/replay | 6 | 6 | 0 |
| Idempotency | 5 | 5 | 0 |
| Signer independence | 4 | 4 | 0 |
| Path / shell / env | 8 | 8 | 0 |
| SSRF | 7 | 7 | 0 |
| SSE / WS reliability | 9 | 9 | 0 |
| Audit outbox | 7 | 7 | 0 |
| Reconciliation | 5 | 5 | 0 |
| Failover | 8 | 8 | 0 |
| **Total** | **71** | **71** | **0** |
