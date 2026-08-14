# PR11 — P0 Mobile Workflow + Fault Injection Fixtures

**Version**: optimization v4
**Date**: 2026-08-14
**PR theme**: `test(qa): add p0 mobile workflow and fault fixtures`
**Boundary** (per `docs/优化v4/14-首批PR与执行顺序.md` row 11 / E2-S5, E6-S2):
- Add or maintain tests, fixtures, fault-injection harness, and acceptance
  reports. **Do not modify product logic to make a test pass.**
- Each fixture records: command, environment, device, commit, expected vs
  observed, defect class (if any). Anything not executed must be listed
  as `not executed` with a reason.

This document is the QA delivery for PR11. The actual fault-injection
harness is a Node script (`fault-injection.mjs`) so a QA agent can run
it in CI without additional dependencies.

---

## 1. Device matrix (P0-C gate)

| Device | OS | Resolution | Locale | Notes |
|---|---|---|---|---|
| Pixel 6 emulator (Android 14, API 34) | Android 14 | 1080x2400 | en-US | PR1 baseline; `frontend/android` Gradle build verified |
| Pixel 8a (Android 15, API 35) — planned | Android 15 | 1080x2400 | zh-CN | Real device; queued behind device fleet access |
| Samsung Galaxy A55 (Android 14, API 34) — planned | Android 14 | 1080x2340 | zh-CN | Real device; secondary fleet |

> Per docs/优化v4/02 §5 P0-C gate, at least one emulator + one real
> device must complete the core flow before declaring P0 shippable.
> The emulator path is automated; the real-device path is recorded as
> `not executed` until device access is granted by QA.

---

## 2. P0 core flow (20 attempts per device)

### 2.1 Scenario

1. Cold start → LoginView (anonymous)
2. Submit credentials → home `/ai`
3. Open instance list → select first non-shared instance
4. Open first session in the list
5. Wait for streaming chunk
6. Trigger a permission request via fixture (see §4)
7. Reject via ApprovalBottomSheet → confirm UI returns to running
8. Trigger a second permission request
9. Approve "本会话允许" → confirm server_confirmed banner appears
10. Send a follow-up prompt → wait for completion
11. Background app for 10s → return → confirm state preserved
12. Cold kill app → re-open → confirm last route is restored

### 2.2 Acceptance

| Metric | Target | Measurement |
|---|---|---|
| Success rate per device | ≥ 95% (≥ 20/20 attempts) | Recorded per device row below |
| Cold-start first screen P50 | ≤ 2.5 s | `frontend/android` logcat (real device only) |
| Cold-start first screen P95 | ≤ 4 s | Same |
| Tap-to-visual-feedback P95 | ≤ 150 ms | Manual stopwatch on the 20 attempts |
| Approval "已批准" without server confirm | 0 | Each approval attempt |
| Duplicate prompt after WS reconnect | 0 | Each prompt attempt |

### 2.3 Recording template

```
device: <name>
os: <version>
build_hash: <sha>
attempts: <N>
successes: <N>
failures: <list of failure modes>
defects: <links or none>
notes: <free form>
```

> Real-device rows are appended here as PR11.1 evidence runs complete.

---

## 3. Network fault matrix

Each row records: command, expected phase transition, expected UI text.

| Fault | Endpoint | Expected server response | Expected UI |
|---|---|---|---|
| 401 unauthenticated | any | 401 + code=token_expired | Redirect to `/login?returnTo=...` |
| 403 capability_denied | approval reply | 403 + code=capability_denied | BottomSheet stays open; error banner |
| 404 not_found (resource) | session detail | 404 + code=not_found | Empty state with "不可用" |
| 409 conflict (CAS) | asset push | 409 + code=conflict | Conflict UI with both versions |
| 429 rate_limited | session prompt | 429 + code=rate_limited | Backoff banner; retry-after header |
| 500 upstream_unavailable | session stream | 500 + retryable=true | Toast + retry button; preserve draft |
| 502 SMTP probe | email test | 502 + retryable=false | "凭据无效" banner |
| Network drop mid-stream | SSE | connection closed | Reconnect within 30s; gap event |
| Replay same event id | WS bus | — | Only first event fires |
| Out-of-order events | WS bus | — | `cause.action_id` latest wins |
| Stale token (mid-session) | any | 401 + code=token_expired | Silent refresh once, then login |

---

## 4. Fault-injection harness

The script `test-evidence/PR11/fault-injection.mjs` provides:

- `simulateServerResponse(status, code)` — produces a structured JSON
  matching the PR1 §10 envelope.
- `runNetworkMatrix(wsBus, asyncState)` — iterates the rows in §3 and
  asserts the AsyncState + WS bus end up in the right phase.
- `recordResult(label, expected, actual)` — appends a JSON line to
  `test-evidence/PR11/results.jsonl` for QA review.

### 4.1 Running

```bash
node test-evidence/PR11/fault-injection.mjs \
  --bus frontend/src/services/idempotentWsBus.ts \
  --state frontend/src/utils/asyncState.ts
```

The harness imports the pure helpers from PR3 + PR5 and exercises them
against the §3 matrix without a live backend.

### 4.2 Exit codes

- `0` — every expected transition matched
- `1` — at least one unexpected transition
- `2` — harness error (missing import, syntax)

---

## 5. WS fault matrix

The idempotent WS bus from PR5 is exercised by replaying envelopes
with the same id, out-of-order `cause.action_id` updates, and unknown
event types. See `fault-injection.mjs::runWsMatrix`.

| Scenario | Expected behaviour |
|---|---|
| Replay event with same id | Subscriber fires once |
| Two events with same `cause.action_id` | Latest id wins; subscribers see only the latest |
| Unknown event type | Bus logs + ignores; subscriber is NOT invoked |
| Wildcard subscriber | Receives every accepted envelope |
| LRU trim | Log size stays bounded at 1024 entries |

---

## 6. What was actually executed in this commit

- The harness script (`fault-injection.mjs`) is shipped.
- A smoke run of `runNetworkMatrix` against the pure helpers (no live
  backend) is executed in the QA agent's session and recorded to
  `test-evidence/PR11/results.jsonl`. CI integration happens in PR12.
- Real device runs are recorded as `not executed` (see §1) until
  device fleet access is granted.

## 7. Defect classes

The harness categorises any failure as:

- `auth` — JWT/cookie/refresh issues
- `scope` — workspace/instance/session cross-tenant
- `approval` — server_confirmed premature flip
- `state` — AsyncState regression
- `ws` — idempotency / out-of-order / replay
- `ui` — BottomSheet / Toast / accessibility regression
- `infra` — build / typecheck / lint

PR11 does **not** fix defects; it only reports them. Fixes land in
subsequent PRs that match the corresponding boundary in 14.
