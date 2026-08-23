# ADR-0001: ZAgentGateway Delegated Token Format

- **Status:** Proposed (M0 baseline)
- **Date:** 2026-08-23
- **Scope:** `services/zagent-gateway/` and every service that issues or verifies a delegated token for ZAG (RedClaw `redclaw`, `acc-go`, `memora`, `pocketd`, llm-gateway-go).
- **Supersedes:** none — this ADR is the first token-format decision for ZAG. The pre-existing fleet-wide HS256 model is documented separately in `docs/adr/2026-08-20-jwks-migration.md`; ZAG does **not** inherit that symmetric-key trust shape.

---

## Context

ZAgentGateway (ZAG) is the new PC-agent control plane sitting between RedClaw (the enterprise control surface), OpenPocket (mobile), and `acc-go` (the task orchestrator). See `docs/新架构v1/02-modules/zagent-gateway.md:1-156` for the module layout, and `docs/新架构v1/02-modules/zagent-gateway.md:817-829` for the security model this ADR is part of.

Every inbound REST, MCP, and WebSocket request to ZAG must carry a **short-lived delegated token**. Bare `X-Tenant-ID`, `X-User-ID`, or `fleetId` fields on the wire do **not** constitute authorization (see `docs/新架构v1/02-modules/zagent-gateway.md:594-598` and `:818`). The token carries the subject identity, the originating tenant, the audience, and the scope; ZAG verifies the token cryptographically, not by header trust.

### What this ADR fixes

The audit handed to this repo concluded that ZAG did not yet have a delegated token contract. Without one:

- A request could be authenticated but not authorized for the tenant it claims.
- A token could be replayed across replicas or across services because there was no shared `jti`/revocation channel.
- The signing key could be substituted (`alg=none`, `alg=HS256` confusion against an EdDSA key) without an enforced algorithm.
- Signing-key downtime could force ZAG to "degrade gracefully" — which for control-plane actions must not happen.

This ADR sets the token **format** and the **failure-mode policy**. Other ADRs in this series cover the matching pieces:

- ADR-0002 — mTLS transport that carries the bearer token.
- ADR-0003 — object-level authorization (RBAC + ABAC) applied after the token validates.
- ADR-0004 — request safety (`Idempotency-Key`, `operation_id`, `X-Request-Id`).
- ADR-0005 — multi-replica state including the revocation channel.
- ADR-0007 — audit writes that the control plane must produce for every write that this token authorizes.

---

## Decision

### 1. Token format: JWS (JSON Web Signature), compact serialization

The token is a **JWS in compact serialization** (three base64url segments separated by `.`), per RFC 7515. We do not use JWE — the token is opaque to the wire, but the audience is the ZAG gateway and the transport is mTLS (ADR-0002), so confidentiality is provided by the channel.

The JOSE header carries:

| Field | Value | Required |
| --- | --- | --- |
| `alg` | `EdDSA` (Ed25519) — see §2 | yes |
| `typ` | `at+jwt` (RFC 9068 access token) | yes |
| `kid` | key ID of the signing key in the issuer's JWKS | yes |
| `cty` | absent (we do not embed a nested JWT) | n/a |

The payload is a JWT claim set with the following **required** claims:

| Claim | Type | Meaning |
| --- | --- | --- |
| `iss` | string | Token issuer. Must be in ZAG's issuer allowlist (`ZAG_TOKEN_ALLOWED_ISSUERS`). Examples: `pocket`, `redclaw`, `acc`, `memora`. |
| `aud` | string or string array | Must contain `zagent-gateway` (or the deployment-specific value `ZAG_EXPECTED_AUDIENCE`). |
| `sub` | string | Subject — the principal (`u_<uuid>` for user, `svc_<name>` for service-to-service, `dev_<id>` for an enrolled device). |
| `tenant_id` | string | Tenant (workspace) the principal is bound to. Must equal the resource's tenant_id (ADR-0003 §3). |
| `scope` | space-separated string | OAuth2-style scope, e.g. `agents.read agents.invoke pods.read pods.control sessions.write tasks.write permissions.read permissions.reply audit.read`. |
| `iat` | integer (Unix seconds) | Issued-at. |
| `exp` | integer (Unix seconds) | Expiry. Reject if `exp <= now`. |
| `jti` | string (UUIDv4) | Unique token ID. Used by the revocation channel (ADR-0005 §3) and by replay-protection (this ADR §6). |

Optional but supported:

| Claim | Type | Meaning |
| --- | --- | --- |
| `act` | object | RFC 8693 actor claim when a service acts on behalf of a user (`{"sub": "u_..."}`). |
| `client_id` | string | MCP / OAuth client identifier. |
| `device_id` | string | Bound enrolled device (when the subject is a pod or IDE connector). |
| `delegated_by` | string | Original issuer when this token is re-issued; absent on first-issuance tokens. |

Example payload:

```json
{
  "iss": "pocket",
  "aud": ["zagent-gateway"],
  "sub": "u_7c1f0d2e-3a5b-4d7a-8c0e-1a2b3c4d5e6f",
  "tenant_id": "ws_001",
  "scope": "agents.read agents.invoke sessions.write",
  "iat": 1724390400,
  "exp": 1724390760,
  "jti": "9d2e8c7a-1b3f-4a5e-9c0d-2e3f4a5b6c7d",
  "client_id": "openpocket-mobile",
  "act": { "sub": "u_7c1f0d2e-3a5b-4d7a-8c0e-1a2b3c4d5e6f" }
}
```

### 2. Signing algorithm: EdDSA (Ed25519)

**Choice:** EdDSA with curve `Ed25519` (RFC 8037).

**Why not HS256 / RS256 / ES256:**

- **HS256 (symmetric)** — Same key signs and verifies. A single compromised service signs for every other issuer. This is exactly the failure mode called out in `docs/adr/2026-08-20-jwks-migration.md:39-42`; ZAG will not introduce it. Rejected.
- **RS256 (RSA-PKCS1v1.5)** — Large signatures (~256 bytes), slow verification. Acceptable as a fallback but not as the primary choice; we keep it as an allowed secondary `alg` only when an issuer is pinned to it for backwards compatibility.
- **ES256 (ECDSA P-256)** — Acceptable and supported. We use EdDSA in preference because verification is constant-time by construction, signatures are 64 bytes, and we already require Ed25519 for the approval double-sign path (`docs/新架构v1/02-modules/zagent-gateway.md:595-598`), so a single primitive serves both.

**Why Ed25519 specifically:**

- Deterministic nonce (no `k` reuse failure mode).
- Side-channel resistant in well-reviewed libraries (`golang.org/x/crypto/ed25519`, `crypto/ed25519` in stdlib).
- Short signatures keep MCP-over-HTTP payloads small (see §7).
- Public-key distribution is trivial (32 bytes; we still wrap it in JWKS for tooling compatibility).

### 3. Algorithm-substitution defence (explicit)

ZAG must reject — at parse time, before any claim is read — any token whose `alg` is not in the allowlist `[EdDSA, RS256]`. We never allow `alg=none`, `alg=HS256`, or any other symmetric algorithm, even in test environments. This mirrors the rejection already present in `identity-go` per `docs/adr/2026-08-20-jwks-migration.md:29`.

### 4. Audience and issuer checks

- `iss` must be in `ZAG_TOKEN_ALLOWED_ISSUERS`. An empty or missing `iss` is rejected.
- `aud` must contain `ZAG_EXPECTED_AUDIENCE` (default `zagent-gateway`). A token whose audience is `redclaw` or `pocket` is rejected here even if it is otherwise well-formed — the audience is what stops a pocket-issued user token from being replayed against ZAG and vice versa.
- The check is exact-match on the audience string; we do not accept wildcards or suffix matching.

### 5. Expiry and clock skew

- `exp` is required. `exp <= now()` is rejected.
- `iat` is required. `iat > now() + 60s` is rejected (clock skew window is bounded to 60 seconds; wider skew is a configuration error).
- Maximum token lifetime (enforced by the issuer, verified defensively here): **15 minutes** (`exp - iat <= 900s`). Longer-lived tokens must be exchanged for a fresh token via the `pocket` re-issue path; ZAG does not accept refresh tokens.

### 6. Replay window and `jti`

- ZAG keeps a `jti` cache (in-memory + Postgres, see ADR-0005) of every token it has accepted in the last **15 minutes + 60s clock-skew**.
- A second presentation of the same `jti` within the replay window is rejected with error `ZAG_AUTH_TOKEN_REPLAY`.
- The cache key is `(iss, jti, exp)`; the cache entry is created on first accept and removed lazily or by background sweep after `exp`.

### 7. Revocation

Two channels:

1. **Local revoke list (ZAG-internal):** when ZAG's own policy revokes a token (logout, role change, suspicious activity), it inserts `(iss, jti)` into `zag_auth_revocation` and rejects all subsequent presentations until `exp`.
2. **Cross-service revoke (issuer-side):** each issuer publishes its own revocation set at `https://<issuer>/.well-known/revocation.jsonl` (one JSON object per line, `{jti, exp_unix}`). ZAG polls this endpoint every 60 seconds and on a `Cache-Control: max-age` boundary. The poll is best-effort; the binding decision is "fail-closed if the signing key is unavailable" (see §9), **not** "fail-closed if the revocation feed is unavailable." We accept that a revoked token can survive up to 60s; this is documented and bounded.

### 8. Rotation

- Signing keys are rotated by the **issuer**, never by ZAG. ZAG verifies via JWKS fetched from `${ISSUER}/.well-known/jwks.json` and cached for `max-age` from `Cache-Control` (default 300s).
- When an issuer publishes a new key in JWKS, ZAG picks it up within one cache window.
- Old keys are retained until the latest token they could have signed has expired (i.e. `max(token_exp) + replay_window`).
- `kid` is mandatory in the JOSE header; a token whose `kid` is not in the issuer's JWKS is rejected (`ZAG_AUTH_UNKNOWN_KID`).

### 9. Failure mode (fail-closed)

The signing key is the trust root. If ZAG cannot verify a token — for any reason — it must **deny the request**. Specifically:

| Condition | Behavior | Error code |
| --- | --- | --- |
| JWKS fetch fails (issuer unreachable) | deny | `ZAG_AUTH_ISSUER_UNREACHABLE` |
| JWKS fetch returns 200 but parsing fails | deny | `ZAG_AUTH_JWKS_INVALID` |
| `kid` not present in JWKS | deny | `ZAG_AUTH_UNKNOWN_KID` |
| Signature verification fails | deny | `ZAG_AUTH_BAD_SIGNATURE` |
| `alg` not in allowlist | deny | `ZAG_AUTH_ALG_REJECTED` |
| `iss` not in allowlist | deny | `ZAG_AUTH_BAD_ISSUER` |
| `aud` missing expected audience | deny | `ZAG_AUTH_BAD_AUDIENCE` |
| `exp <= now()` | deny | `ZAG_AUTH_EXPIRED` |
| `iat > now + 60s` | deny | `ZAG_AUTH_IAT_FUTURE` |
| `jti` already seen in replay window | deny | `ZAG_AUTH_TOKEN_REPLAY` |
| `jti` present in local revocation list | deny | `ZAG_AUTH_TOKEN_REVOKED` |

**There is no "degrade gracefully" path for token verification.** Every control-plane action that this token authorizes must be preceded by a successful verification; if verification cannot complete, the request is rejected with HTTP 503 (so clients retry on a different replica or after the issuer recovers). We do **not** silently allow the request on an alternative path, and we do **not** fall back to HMAC, a static allowlist, or an "outage token." This is consistent with the ZAG security model at `docs/新架构v1/02-modules/zagent-gateway.md:819-829`.

### 10. Token exchange

For service-to-service callers (`pocketd`, `acc-go`, `memora`), the caller mints its own token with its own `iss`, signs with its own Ed25519 key, and presents it directly. There is no token-exchange endpoint on ZAG; the audience check (§4) and the mTLS check (ADR-0002) jointly determine trust.

---

## Consequences

### Positive

- Single, well-defined format that every issuer and every verifier agrees on.
- Replay protection (`jti`) and revocation (`jti` cache + revocation list) work without changing the wire format.
- Algorithm-substitution attacks are closed at parse time.
- A revoked or compromised key on one issuer does not let the attacker forge tokens for any other issuer.

### Negative

- The issuer allowlist and JWKS caching become operational dependencies: ZAG cannot start in `production` mode unless at least one JWKS endpoint is reachable. We accept this — see §9.
- 15-minute token lifetime means callers must refresh; mobile clients cache a refresh path through `pocketd` (out of scope for this ADR).

### Neutral

- `identity-go` already supports EdDSA per `docs/adr/2026-08-20-jwks-migration.md`. This ADR makes EdDSA mandatory for ZAG, regardless of the fleet-wide rollout schedule.

---

## Acceptance criteria

1. A token with `alg=EdDSA`, valid `iss/aud/exp/jti/tenant_id/scope`, signed with an Ed25519 key published in the issuer's JWKS, is accepted.
2. The same token with a flipped signature byte is rejected with `ZAG_AUTH_BAD_SIGNATURE`.
3. The same token presented twice within 60 seconds is rejected with `ZAG_AUTH_TOKEN_REPLAY` on the second call.
4. A token with `alg=HS256` is rejected with `ZAG_AUTH_ALG_REJECTED` even if signed with the corresponding shared secret.
5. A token with `aud=pocket` is rejected with `ZAG_AUTH_BAD_AUDIENCE`.
6. A token whose `iss` is not in `ZAG_TOKEN_ALLOWED_ISSUERS` is rejected with `ZAG_AUTH_BAD_ISSUER`.
7. A token whose `kid` is not in the issuer JWKS is rejected with `ZAG_AUTH_UNKNOWN_KID`.
8. If the issuer's JWKS endpoint returns 5xx, every request is rejected with HTTP 503 and error `ZAG_AUTH_ISSUER_UNREACHABLE` — verified by killing the upstream in a chaos test (see `docs/security/zag-test-matrix.md`).
