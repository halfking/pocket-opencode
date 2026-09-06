# OpenCode Pocket — Deployment & Test Plan (2026-09-07)

**Audience:** engineers running end-to-end verification of the `opencode-pocket` mobile control plane (Go backend + iOS/Android emulator clients + optional `agent-companion` runtime).

**Read time target:** < 10 minutes. **Executable end-to-end:** yes.

**Companion file:** `CHECKLIST.md` (same dir) — flat, tickable items grouped by phase.

---

## 1. Scope and Out-of-Scope

### In Scope
- Bring up the local backend (`pocketd`) on port `8088` against a local PostgreSQL store.
- Bring up the iOS Simulator and Android Emulator clients (Capacitor shells wrapping the same web frontend).
- Verify the **mobile control plane** endpoints under `/api/mobile/...`:
  - Sessions CRUD + SSE event stream + messages + prompt + interrupt (`mobile_session_handler.go`).
  - Approval reply / question reply/reject (`mobile_approval_handler.go`).
  - Events snapshot (`mobile_events_handler.go`).
- Auth + workspace scoping (`requireAuth` + `requireMobileWorkspace`, fail-closed).
- Data closure: confirm DB rows after each flow.
- Audit closure: confirm `audit_entries` rows after each flow.
- Interactive UI smoke + screenshot evidence per client.

### Out of Scope
- Production deployment (`deploy-154.sh`, `deploy-245.sh`, `deploy-252.sh` — not used here).
- RedClaw Admin / OIDC SSO integration (configured but disabled when `POCKET_AUTH_LEGACY_ONLY=true`).
- LLM gateway E2E (only the local mock `mock-llm-gateway.js` is used for chat; we are not validating upstream provider correctness).
- Email, Feishu, vault, scheduled tasks — listed in the larger surface but not part of this plan.
- Performance / load testing.

---

## 2. Environment Prerequisites

### 2.1 Binaries / Tooling

| Tool | Min version | Used for | Detection |
|---|---|---|---|
| Go | 1.22+ | (only if rebuilding `pocketd`; pre-built `backend/pocketd` exists) | `go version` |
| Docker Desktop | 4.x | Hosts the local PG container via `deploy-local.sh` | `docker --version` |
| `docker compose` | v2 | Same | `docker compose version` |
| `openssl` | any | Generates JWT secret in `deploy-local.sh` | `openssl version` |
| `psql` | 14+ | Direct DB verification | `psql --version` |
| Node.js | 18+ | Build frontend + run mock LLM gateway | `node --version` |
| Xcode | 16+ | iOS Simulator runtime | `xcodebuild -version` |
| Android SDK / `adb` | API 34 | Android Emulator + adb reverse | `adb --version` |
| `jq` | any | Parsing JSON in shell | `jq --version` |
| `curl` | 7+ | HTTP probes | `curl --version` |

### 2.2 Ports (local)

| Port | Service | Source |
|---|---|---|
| `8088` | `pocketd` HTTP (set by `POCKET_HTTP_PORT` in repo-root `.env`) | `services/opencode-pocket/.env:1` |
| `8089` | local mock LLM gateway (`mock-llm-gateway.js`) | `POCKET_LLM_GATEWAY_URL` in repo-root `.env` |
| `15432` | local PostgreSQL via `deploy-local.sh` (`OPP_PG_PORT` override; default `5432` host) | `deploy-local.sh:36-37` |
| `4175` | Vite dev frontend (only used by web/desktop tests) | `deploy/bin/env.sh:225` |
| iOS sim UDID | device-specific | `xcrun simctl list devices booted` |
| Android emulator | `5554`-default | `adb devices` |

### 2.3 Environment Variables (set before `pocketd` boots)

Loaded automatically by `scripts/start-dev.sh`. **For repeatable runs, prefer `scripts/start-dev.sh` over exporting by hand.** Key vars:

| Var | Local value | Effect | Source |
|---|---|---|---|
| `POCKET_HTTP_PORT` | `8088` | Backend listen port | repo-root `.env:1` |
| `POCKET_DB_PATH` | `./data/pocket.sqlite` | SQLite (legacy; data dir locator only) | repo-root `.env:2` |
| `POCKET_DEV_AUTH` | `true` | Auto-seed `admin/admin` | repo-root `.env` |
| `POCKET_AUTH_USER` | `admin` | Dev user | repo-root `.env` |
| `POCKET_AUTH_PASS` | `admin123` (or `admin` per `backend/.env.test`) | Dev password | repo-root `.env` / `backend/.env.test:5` |
| `POCKET_AUTH_LEGACY_ONLY` | `true` | Force local JWT, ignore RedClaw Admin | repo-root `.env` |
| `POCKET_OPENCODE_INSTANCES` | JSON catalog of writable instances | Required for session create / prompt / interrupt (writable) | `deploy-local.sh:148-152` |
| `POCKET_OPENCODE_TIMEOUT_MS` | `10000` | Upstream timeout | `backend/.env.test:8` |
| `POCKET_LLM_GATEWAY_URL` | `http://localhost:8089/v1` | LLM BFF target | repo-root `.env` |
| `POCKET_LLM_GATEWAY_API_KEY` | `mock-key` | LLM BFF mock bearer | repo-root `.env` |
| `POCKET_JWT_SECRET` / `JWT_SECRET` | `test-secret-key-for-phase7-validation` | HS256 signing key | `backend/.env.test:1` |

`POCKET_POSTGRES_DSN` is **commented out** in repo-root `.env`. The local PG via `deploy-local.sh` writes the DSN into `${POCKET_CONFIG_DIR}/.env.local` and `pocketd` picks it up from there if launched with that env file. When launching via `scripts/start-dev.sh`, SQLite is used and **PG-backed tables (`audit_entries`, `opencode_sessions`, etc.) will NOT be created** — see "Data Closure" caveats below.

### 2.4 Test data setup

- A seeded dev user `admin` / `admin123` is created on first `pocketd` boot when `POCKET_DEV_AUTH=true`. No seed script is required.
- A fake "demo-main" `instance_id` is seeded via `POCKET_OPENCODE_INSTANCES` (see `scripts/start-dev.sh`). Writes (`POST /api/mobile/sessions`, `/prompt`, `/interrupt`) will hit a fake `apiBaseURL` and return 502 unless a real OpenCode is reachable.

---

## 3. Deployment Procedure

Execute in order from repo root `/Users/xutaohuang/workspace/official-deploy/services/opencode-pocket`.

### 3.1 Backend (preferred: `scripts/start-dev.sh`)
```bash
# Kills any prior pocketd, exports env, launches background
bash scripts/start-dev.sh
# Health check
curl -fsS http://localhost:8088/healthz   # → "ok"
```
The script writes logs to `logs/pocketd-dev.log`.

### 3.2 Backend (alternative: `deploy-local.sh`)
Use only if you need PostgreSQL-backed verification. Generates `.env.local` with `POCKET_POSTGRES_DSN` set; `pocketd` must then be started with that env file:
```bash
./deploy-local.sh                              # init-dirs + ensure-databases + start
# When the script reports "container started", pocketd is up; verify:
curl -fsS http://localhost:8088/healthz
```

### 3.3 Local mock LLM gateway (required for chat flows)
```bash
node mock-llm-gateway.js &
curl -fsS http://localhost:8089/v1/models | jq '.[0].id'
```
The mock answers any `/v1/chat/completions` request with a streaming response.

### 3.4 `agent-companion` (OpenCode upstream)
**Caveat — see Audit Questions #4 below.** The string `agent-companion` appears only in code comments in this repo (e.g. `backend/internal/agent/adapter_pi_test.go:495`). No `agent-companion` binary, Docker image, or process definition lives under `services/opencode-pocket/`. To exercise write paths against a real OpenCode, point `POCKET_OPENCODE_INSTANCES` at an existing OpenCode HTTP service (e.g. `http://host.docker.internal:4096`).

### 3.5 iOS Simulator (Capacitor shell)
```bash
# Boot iPhone 16 / iOS 18 (use whatever the local Xcode provides)
xcrun simctl list devices booted | grep -q Booted || \
  xcrun simctl boot "iPhone 16"
# Build + install + launch (Capacitor wraps the Vite-built web bundle)
cd frontend
npm ci
npm run build
npx cap sync ios
npx cap run ios --target="iPhone 16"
```

### 3.6 Android Emulator (Capacitor shell)
```bash
# Start an AVD (Capacitor default) if none is running
$ANDROID_HOME/emulator/emulator -avd pocket_test -no-snapshot -no-audio &
adb wait-for-device
adb reverse tcp:8088 tcp:8088   # critical so the app can reach host:8088
cd frontend
npx cap sync android
npx cap run android
```

### 3.7 Frontend (web/desktop) — only for screenshot baselines
```bash
cd frontend
npm run dev   # Vite serves http://localhost:5173 by default; configure per .env
```

### 3.8 Health check matrix (must all return 200 / "ok")

| URL | Expected |
|---|---|
| `http://localhost:8088/healthz` | `ok` |
| `curl -X POST http://localhost:8088/api/auth/login -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin123"}'` | JSON with `token` (and `refreshToken`) |
| `http://localhost:8089/v1/models` | JSON array |

---

## 4. Auth & Test User Setup

### 4.1 Auth scheme (verified from code)
- **Header:** `Authorization: Bearer <JWT>` — see `backend/internal/server/auth_helper.go:45-87`.
- **Token issuance:** `POST /api/auth/login` returns `{ token, refreshToken, ... }`. Verified handler at `backend/internal/server/server_auth_extended.go`.
- **WebSocket / SSE note:** `/api/mobile/sessions/.../event` accepts `?token=...` query string **only if** `Authorization` header is empty (`auth_helper.go:53-55`). For our flows we always use the `Authorization` header.
- **Workspace scoping:** every mobile handler calls `requireMobileWorkspace` (`backend/internal/server/mobile_endpoint_scope.go:236-249`), which fails closed (`401`) if claims are missing or `WorkspaceID` is empty (`400`).

### 4.2 Obtain a token
```bash
# Dev mode login (POCKET_DEV_AUTH=true seeds admin/admin123)
LOGIN_RES=$(curl -s -X POST http://localhost:8088/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin123"}')
TOKEN=$(echo "$LOGIN_RES" | jq -r .token)
echo "Bearer $TOKEN" | head -c 40
```
If the response includes `workspaceId`, capture it as `WORKSPACE_ID` (it becomes the required `tenant_id` / `workspace_id` for all mobile calls).

### 4.3 Refresh
```bash
curl -s -X POST http://localhost:8088/api/auth/refresh \
  -H "Authorization: Bearer $TOKEN" | jq -r .token
```
Endpoint registered at `server.go:598`.

### 4.4 Auth header format used throughout the plan
```bash
AUTH="Authorization: Bearer $TOKEN"
```
For SSE paths, you may equivalently use `?token=$TOKEN` query string.

---

## 5. API Inventory Table

> All paths below are mounted on `http://localhost:8088`. All require `Authorization: Bearer <JWT>`. Mobile routes additionally fail-closed on missing `WorkspaceID` in claims.

### 5.1 Mobile Session routes (`backend/internal/server/mobile_session_handler.go`)

| # | Method | Path | Handler ref | Auth | Request body / query | Response shape | Side effects (DB) | Test assertion |
|---|---|---|---|---|---|---|---|---|
| 1 | POST | `/api/mobile/sessions` | `handleMobileSessionCreate` (:141) | JWT + WS | query `instance_id=<id>`; optional `Idempotency-Key` header; body `{title?,parentID?,agent?,model?}` | `{id, title?, parentID?, ...}` upstream session JSON (200) | optional cache write to in-memory `mobileCreates` | status 200; response has `id`; replay with same `Idempotency-Key` returns `Idempotency-Replayed: true` |
| 2 | GET | `/api/mobile/sessions` | `handleMobileSessionList` (:194) | JWT + WS | query `instance_id`, optional `since=<unix_ms>` | `{data:[{id,title,status,timeUpdatedMs}], total, sinceMs, serverTimeMs}` (200) | read-only (upstream list) | status 200; `data` array |
| 3 | GET | `/api/mobile/sessions/search` | `handleMobileSessionSearch` (:244) | JWT + WS | query `q`, `instance_id` | `{data:[session...], query, total}` (200) | read-only | status 200; `total >= 0` |
| 4 | DELETE | `/api/mobile/sessions/{id}` | `handleMobileSessionDelete` (:679) | JWT + WS | query `instance_id` | `204 No Content` | `opencodeManager.InvalidateCache(instanceID)` (in-memory) | status 204; subsequent list should not contain `{id}` |
| 5 | GET | `/api/mobile/sessions/{id}/event` | `handleMobileSessionEvent` (:357) | JWT + WS (+ query `?token=` accepted) | query `instance_id`, optional `directory` | SSE stream: `event: server.connected` first, then per-event `event: <type>` + `data: <json>`, `:ping\n\n` heartbeat every 15s, `event: upstream.closed` on end | writes nothing persistent (broadcast stream) | content-type `text/event-stream`; first event name is `server.connected`; at least one downstream event observed within the prompt roundtrip |
| 6 | GET | `/api/mobile/sessions/{id}/messages` | `handleMobileSessionMessages` (:563) | JWT + WS | query `instance_id`, `limit=1..100`, `order=asc\|desc` | `{sessionId, messages:[MobileMessage], total}` (200) | read-only (upstream fetch) | status 200; messages normalized to `{id,role,text,content}` shape |
| 7 | GET | `/api/mobile/sessions/{id}/summary` | `handleMobileSessionSummary` (:286) | JWT + WS | query `instance_id` | `{sessionID, title, summary, messageCount}` (200) | read-only | status 200; `summary` non-empty |
| 8 | POST | `/api/mobile/sessions/{id}/prompt` | `handleMobileSessionPrompt` (:616) | JWT + WS | query `instance_id`; body `{text, agent?, model?}` | `{messageID, sessionID}` (202 Accepted) | upstream OpenCode `SendPrompt` call | status 202; `messageID` non-empty |
| 9 | POST | `/api/mobile/sessions/{id}/interrupt` | `handleMobileSessionInterrupt` (:659) | JWT + WS | query `instance_id` | `204 No Content` | upstream OpenCode `InterruptSession` | status 204 |

### 5.2 Mobile Approval routes (`backend/internal/server/mobile_approval_handler.go`)

| # | Method | Path | Handler ref | Auth | Request body | Response shape | Side effects (DB) | Test assertion |
|---|---|---|---|---|---|---|---|---|
| 10 | GET | `/api/mobile/approvals` | `listMobileApprovals` (:59) | JWT + WS | query `instance_id`, optional `session_id` | `{permissions:[], questions:[]}` (200) | read-only (in-memory pending sets) | status 200; both keys present |
| 11 | POST | `/api/mobile/approvals/permission/{request_id}/reply` | `replyMobilePermission` (:71) | JWT + WS | `{instance_id, session_id, decision: once\|always\|reject, message?}` | `{request_id, decision, confirmed:true, correlation_id}` (200) | `permMgr.ReplyForWorkspace` (in-memory) + audit `mobile.approval.permission_<decision>` | status 200; `confirmed:true`; 1 audit row |
| 12 | POST | `/api/mobile/approvals/question/{request_id}/reply` | `replyMobileQuestion` (:101) | JWT + WS | `{instance_id, session_id, answers:[[string,...],...]}` | `{request_id, decision:"answer", confirmed:true, correlation_id}` (200) | `quesMgr.ReplyForWorkspace` + audit `mobile.approval.question_answer` | status 200; `confirmed:true`; 1 audit row |
| 13 | POST | `/api/mobile/approvals/question/{request_id}/reject` | `rejectMobileQuestion` (:125) | JWT + WS | `{instance_id, session_id}` | `{request_id, decision:"reject", confirmed:true, correlation_id}` (200) | `quesMgr.RejectForWorkspace` + audit `mobile.approval.question_reject` | status 200; `confirmed:true`; 1 audit row |

### 5.3 Mobile Events route (`backend/internal/server/mobile_events_handler.go`)

| # | Method | Path | Handler ref | Auth | Request body | Response shape | Side effects (DB) | Test assertion |
|---|---|---|---|---|---|---|---|---|
| 14 | GET | `/api/mobile/events/snapshot` | `handleMobileEventsSnapshot` (:36) | JWT + WS | none | in-memory `EventsSnapshot` JSON; 503 if broadcaster not configured | read-only | status 200; payload has `generated_at`; workspace-scoped rows only |

### 5.4 Adjacent routes used for setup / verification

| # | Method | Path | Notes |
|---|---|---|---|
| 15 | GET | `/api/audit/logs` | Used in §8 to fetch the audit trail. Handler at `server_audit.go` (registered `server.go:758`). Supports `?action=mobile.approval.*&limit=50&since=<rfc3339>`. |
| 16 | GET | `/api/instances` | Returns registered OpenCode instances for current workspace. Used to discover `instance_id` (see §6.2). |
| 17 | GET | `/healthz` | Plain text `ok`. |

---

## 6. Flow Closure Tests

> Conventions:
> - `BASE=http://localhost:8088`
> - `AUTH="Authorization: Bearer $TOKEN"`
> - Capture `INSTANCE_ID=$(curl ... | jq -r '.[0].id')` first.
> - Replace `<sid>` / `<rid>` / `<mid>` placeholders with values from prior steps.
> - All commands assume `psql` and `jq` are on PATH.

### 6.0 Pre-loaded test data
- Backend reachable on `:8088`, health = `ok`.
- Login completed (`$TOKEN`).
- A writable `instance_id` known (see §6.2 step 0).
- For PG-backed verification: `PG_DSN` set in `${POCKET_CONFIG_DIR}/.env.local` and `pocketd` was launched with it; otherwise SQLite + in-memory only — adjust §7/§8 SQL accordingly or use the file-backed PG.
- For audit verification: confirm `s.auditStore` was constructed at boot (`backend/internal/server/server.go:250-265`).

### 6.1 Pre-flight: discover an instance_id
```bash
# Endpoint registered at server.go:566
INSTANCES=$(curl -s $BASE/api/instances -H "$AUTH")
INSTANCE_ID=$(echo "$INSTANCES" | jq -r '.[0].id')
[ -n "$INSTANCE_ID" ] && [ "$INSTANCE_ID" != "null" ] || {
  echo "no instance — set POCKET_OPENCODE_INSTANCES"; exit 2; }
```

### 6.2 Flow A — Session Create → Prompt → Event Stream → Messages → Interrupt

**Step 0.** Discover the instance id (above).

**Step 1.** Create a session (`mobile_session_handler.go:141`).
```bash
CREATE=$(curl -s -X POST "$BASE/api/mobile/sessions?instance_id=$INSTANCE_ID" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -H "Idempotency-Key: it-$(uuidgen)" \
  -d '{"title":"plan run","model":{}}')
SID=$(echo "$CREATE" | jq -r .id)
[ "$SID" != "null" ] || { echo "create failed: $CREATE"; exit 2; }
```
Assert: 200, `id` present. With `POCKET_OPENCODE_INSTANCES` pointing at a real upstream, also `opencode_sessions` row inserted.

**Step 2.** Open the SSE event stream in the background (collect first 60s).
```bash
( curl -N -s "$BASE/api/mobile/sessions/$SID/event?instance_id=$INSTANCE_ID" \
    -H "$AUTH" > /tmp/pocket-sse-$SID.log 2>&1 & echo $! > /tmp/pocket-sse.pid )
sleep 1
head -1 /tmp/pocket-sse-$SID.log | grep -q '^event: server.connected' || {
  echo "SSE did not emit server.connected"; exit 2; }
```

**Step 3.** Send a prompt (`mobile_session_handler.go:616`).
```bash
PROMPT=$(curl -s -X POST "$BASE/api/mobile/sessions/$SID/prompt?instance_id=$INSTANCE_ID" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"text":"echo hello"}')
MID=$(echo "$PROMPT" | jq -r .messageID)
[ "$MID" != "null" ] || { echo "prompt failed: $PROMPT"; exit 2; }
```
Assert: status 202, `messageID` non-empty.

**Step 4.** Wait, then verify the event stream produced per-session events.
```bash
sleep 8
grep -E "^event: (message\.|session\.)" /tmp/pocket-sse-$SID.log | head -5
# If upstream is mock'd, you may see only server.connected / upstream.closed.
# Failure mode to flag: stream emits events not bound to $SID (eventBelongsToSession).
```

**Step 5.** Fetch messages (`mobile_session_handler.go:563`).
```bash
MSGS=$(curl -s "$BASE/api/mobile/sessions/$SID/messages?instance_id=$INSTANCE_ID&limit=50&order=asc" \
  -H "$AUTH")
echo "$MSGS" | jq '.messages | length'
```
Assert: status 200; `messages` array; rows normalized to `{id,role,text,content}`.

**Step 6.** Interrupt (`mobile_session_handler.go:659`).
```bash
curl -s -o /dev/null -w "%{http_code}\n" \
  -X POST "$BASE/api/mobile/sessions/$SID/interrupt?instance_id=$INSTANCE_ID" \
  -H "$AUTH"     # expect 204
```
Assert: status 204.

**Step 7.** Tear down the SSE consumer.
```bash
kill $(cat /tmp/pocket-sse.pid) 2>/dev/null || true
```

**Post-conditions**
- When PG-backed: 1 row in `opencode_sessions` (with the new `id`), ≥1 row in `opencode_session_history` per event.
- `mobile.approval.*` rows: none for this flow.
- Audit: `mobile.approval.*` rows: none. (Session create/prompt/interrupt do NOT call `s.Write(...)` — see audit-closure note in §11.)

### 6.3 Flow B — Approval Request → Reply

**Step 0.** Seed a permission request. Two options:
- (a) If the upstream OpenCode is running, issue a prompt that triggers a real permission.
- (b) Use the test binary `backend/test_acp_adapter` (in-tree) to push a synthetic permission into the registry via `permMgr`. The repo ships `test_acp_stdio` / `test_acp_adapter` binaries — exercise via `go run ./cmd/test_acp_adapter` from `backend/`.

**Step 1.** List pending approvals.
```bash
PEND=$(curl -s "$BASE/api/mobile/approvals?instance_id=$INSTANCE_ID&session_id=$SID" -H "$AUTH")
RID=$(echo "$PEND" | jq -r '.permissions[0].id // .questions[0].id')
[ -n "$RID" ] && [ "$RID" != "null" ] || { echo "no pending approvals"; exit 2; }
```
Assert: 200; at least one of `permissions` / `questions` non-empty.

**Step 2.** Reply `once`.
```bash
curl -s -X POST "$BASE/api/mobile/approvals/permission/$RID/reply" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d "{\"instance_id\":\"$INSTANCE_ID\",\"session_id\":\"$SID\",\"decision\":\"once\",\"message\":\"\"}"
# expect {"request_id":"<rid>","decision":"once","confirmed":true,"correlation_id":"..."}
```
Assert: 200, `confirmed:true`, decision echoes input.

**Post-conditions**
- `permission_manager.pending` map no longer contains `$RID` (in-memory only — see §7 caveat).
- Audit row in `audit_entries` with `action='mobile.approval.permission_once'` (see `mobile_approval_handler.go:97`).

### 6.4 Flow C — Question Request → Answer / Reject

**Step 1.** Answer.
```bash
curl -s -X POST "$BASE/api/mobile/approvals/question/$RID/reply" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d "{\"instance_id\":\"$INSTANCE_ID\",\"session_id\":\"$SID\",\"answers\":[[\"yes\"]]}"
# expect {"request_id":"...","decision":"answer","confirmed":true,...}
```
Assert: 200, `confirmed:true`. Audit row `mobile.approval.question_answer`.

**Step 2.** Reject (separate request id).
```bash
curl -s -X POST "$BASE/api/mobile/approvals/question/$RID2/reject" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d "{\"instance_id\":\"$INSTANCE_ID\",\"session_id\":\"$SID\"}"
```
Assert: 200, `confirmed:true`. Audit row `mobile.approval.question_reject`.

### 6.5 Flow D — Events snapshot

**Step 0.** Requires `mobileEventsBroadcaster` to be wired at boot (see `mobile_events_handler.go:27`). If `pocketd` was started via `cmd/pocketd` and `SetMobileEventsBroadcaster` ran, this returns 200. Otherwise 503 — flag in evidence.

```bash
curl -s "$BASE/api/mobile/events/snapshot" -H "$AUTH" | jq '.generated_at'
```
Assert: 200; payload includes `generated_at` (RFC3339 string) and is scoped to `$WORKSPACE_ID`.

### 6.6 Flow E — Idempotency replay

Re-run step 1 of Flow A with the **same** `Idempotency-Key`. Expect:
- status 200,
- `Idempotency-Replayed: true` header present,
- response body identical to the original create.

---

## 7. Data-Closure Verification (psql)

> Only meaningful when `pocketd` is connected to PostgreSQL (`POCKET_POSTGRES_DSN` set, e.g. via `deploy-local.sh`). Otherwise data lives in SQLite (legacy) and the `audit_entries` / `opencode_sessions` PG tables simply do not exist.

```bash
PG_DSN=$(grep -E '^POCKET_POSTGRES_DSN=' ${POCKET_CONFIG_DIR:-${HOME}/kaixuan/openpocket}/.env.local \
  | cut -d= -f2-)
export PGPASSWORD=<pg password>
psql "$PG_DSN" <<'SQL'
-- 7.1 Sessions: should contain $SID, status reflects upstream
SELECT id, instance_id, status, created_at
FROM opencode_sessions
WHERE id = :'SID';

-- 7.2 History: per-event rows bound to $SID
SELECT count(*), event_type
FROM opencode_session_history
WHERE session_id = :'SID'
GROUP BY event_type;

-- 7.3 Audit entries from the Flow B/C approval actions
SELECT action, resource, success, detail, timestamp
FROM audit_entries
WHERE action LIKE 'mobile.approval.%'
ORDER BY timestamp DESC
LIMIT 10;

-- 7.4 Audit entries from Flow E idempotency
SELECT action, success, detail
FROM audit_entries
WHERE detail ILIKE '%upstream_confirmed%'
ORDER BY timestamp DESC LIMIT 5;
SQL
```

If `psql` cannot connect (SQLite-only run), substitute:
```bash
sqlite3 backend/data/pocket.sqlite "SELECT name FROM sqlite_master WHERE type='table';"
```

### Per-flow expected DB state

| Flow | `opencode_sessions` | `opencode_session_history` | `audit_entries` |
|---|---|---|---|
| A — session/prompt/interrupt | 1 row for `$SID` | ≥1 row per SSE event | none (session/prompt/interrupt do not call `s.Write`) |
| B — permission reply | unchanged | unchanged | 1 row: `action='mobile.approval.permission_once'`, `success=true`, `detail='upstream_confirmed'` |
| C — question reply/reject | unchanged | unchanged | 1 row each: `mobile.approval.question_answer`, `mobile.approval.question_reject` |
| D — events snapshot | unchanged | unchanged | none (handler does not write audit) |
| E — idempotency replay | unchanged | unchanged | none (cache hit, no upstream call) |

---

## 8. Audit-Closure Verification

### 8.1 Pull the audit trail via the public endpoint
```bash
# handler at server_audit.go; registered server.go:758
curl -s "$BASE/api/audit/logs?action=mobile.approval.&limit=50&since=$(date -u -v-30M +%Y-%m-%dT%H:%M:%SZ)" \
  -H "$AUTH" | jq '.entries[] | {action, resource, success, detail, timestamp}'
```
Expected after running §6.3 + §6.4:
- `action="mobile.approval.permission_once"`, `resource="instance:<id>/session:<sid>/request:<rid>"`, `success=true`.
- `action="mobile.approval.question_answer"`, similar resource.
- `action="mobile.approval.question_reject"`, similar resource.

### 8.2 Direct DB read (PG)
```sql
-- Use the same PGPASSWORD / DSN as §7
SELECT action, count(*), bool_or(success) AS any_success
FROM audit_entries
WHERE action LIKE 'mobile.approval.%'
GROUP BY action;
```
Should produce 3 groups, each with `count = 1` and `any_success = true`.

### 8.3 Direct DB read (SQLite fallback)
```bash
sqlite3 backend/data/pocket.sqlite \
  "SELECT action, count(*) FROM audit_entries WHERE action LIKE 'mobile.approval.%' GROUP BY action;"
```

### 8.4 Notes
- The audit table is `audit_entries` (PG) — schema at `backend/internal/redclaw/audit_pg.go:46-63`. `tenant_id` = `claims.WorkspaceID` (auth_helper.go:76-81).
- Only the approval handlers call `s.Write(...)`; sessions/prompt/interrupt do **not** write audit rows. This is by design but should be acknowledged in any audit completeness report.

---

## 9. Interactive UI Tests (iOS, Android, web/desktop)

The mobile clients are Capacitor wrappers around the Vite-built web frontend. Build artifacts live in `frontend/dist` and are loaded by both shells via `capacitor.config.ts` (`frontend/capacitor.config.ts`).

### 9.1 Common screenshot evidence directory
```bash
mkdir -p test-evidence/2026-09-07-{ios,android,web}
```

### 9.2 iOS (SwiftUI Capacitor shell)
**Launch**
```bash
xcrun simctl boot "iPhone 16" || true
npx cap run ios --target="iPhone 16"
# Adb-equivalent: simctl push and openurl for deep links if any.
```

**Critical screens / assertions**
- Launch screen → login (`POCKET_AUTH_USER` / `POCKET_AUTH_PASS` form). Screenshot `test-evidence/2026-09-07-ios/01-login.png`.
- Session list. Verify: navigation bar translucent style, list scrolls under it (do **not** cover content), tab bar at the bottom does not overlap system tab bar (Dynamic Island devices only show safe-area inset).
- Session detail. Right-side action button visible (refresh / interrupt), back button respects safe-area.
- Approvals sheet. Verify swipe-to-dismiss respects bottom safe-area inset; action buttons reachable with one thumb.
- Events snapshot list rendered when broadcaster is configured.

**Multi-window / iPad**
- On iPad simulator: switch to multi-scene (`xcrun simctl spawn booted launchctl ...` is overkill; use the simulator's `View → Enter Multiple Windows`). Verify layout reflows (CSS `@media (min-width: 768px)` triggers if implemented).
- Background the app and return — SSE stream must reconnect (look for a fresh `event: server.connected`).

**Fold / mode switching**
- Capture screenshots in light and dark mode (`xcrun simctl ui booted appearance dark`).
- Capture in landscape (`xcrun simctl ui booted rotate`); verify content-area scrolling is preserved.

### 9.3 Android (Capacitor shell)
**Launch**
```bash
adb wait-for-device
adb reverse tcp:8088 tcp:8088
npx cap run android
# App id: com.kaixuan.opencode.pocket (see POCKET_ANDROID_APP_ID)
```

**Critical screens / assertions**
- Same flow as iOS. Screenshot to `test-evidence/2026-09-07-android/01-login.png`.
- Verify Android system back button (`adb shell input keyevent KEYCODE_BACK`) pops the navigation stack and does not exit the app until the root is reached.
- Verify multi-mic detection is irrelevant here (no voice input wired) — but if added, ensure the app does not request `RECORD_AUDIO` unless the user enables voice mode.
- Verify tablet layout (Android emulator `7" WSVGA` tablet skin) — multi-window mode (`adb shell cmd activity supports-multi-window`).

### 9.4 Web / Desktop (Vite dev)
**Launch**
```bash
cd frontend && npm run dev   # http://localhost:5173
```
**Critical screens / assertions**
- Same flows as mobile; screenshots to `test-evidence/2026-09-07-web/`.
- Resize browser to: 375×667 (iPhone SE), 768×1024 (iPad), 1280×800 (desktop). Verify breakpoints trigger expected layout changes.

### 9.5 Responsive / multi-device checks (consolidated)

| Check | iOS sim | Android emu | Web |
|---|---|---|---|
| Login renders + submits | ✓ | ✓ | ✓ |
| Session list scrolls under nav bar | ✓ | ✓ | ✓ |
| Tab bar does not overlap system tab bar | ✓ (notch devices) | ✓ (gesture nav) | n/a |
| Right-side action button reachable | ✓ | ✓ | ✓ |
| Multi-window / split view | ✓ (iPad sim) | ✓ (tablet emu) | ✓ (resize) |
| Light/dark mode | ✓ (`simctl ui booted appearance`) | ✓ (`adb shell cmd uimode night`) | ✓ (DevTools) |
| Landscape | ✓ | ✓ | ✓ |
| SSE reconnect after backgrounding | ✓ | ✓ | n/a |

---

## 10. Regression Matrix

| Test ID | Flow | Client | Priority | Pass criteria |
|---|---|---|---|---|
| API-AUTH-01 | Login | backend | P0 | 200, JWT in `.token` |
| API-AUTH-02 | Login bad password | backend | P1 | 401 |
| API-AUTH-03 | No Authorization header on mobile route | backend | P0 | 401 `CodeUnauthenticated` |
| API-MOB-01 | Session create | backend | P0 | 200 + `id` |
| API-MOB-02 | Session create w/ Idempotency-Key (replay) | backend | P1 | `Idempotency-Replayed: true`, same body |
| API-MOB-03 | Session list (with `since`) | backend | P0 | 200, `data[]` |
| API-MOB-04 | Session search | backend | P2 | 200, `total>=0` |
| API-MOB-05 | Session delete | backend | P1 | 204 |
| API-MOB-06 | Session event (SSE) | backend | P0 | `text/event-stream`, `event: server.connected` first |
| API-MOB-07 | Session messages | backend | P0 | 200, normalized rows |
| API-MOB-08 | Session summary | backend | P1 | 200, non-empty `summary` |
| API-MOB-09 | Session prompt | backend | P0 | 202, `messageID` |
| API-MOB-10 | Session interrupt | backend | P0 | 204 |
| API-MOB-11 | Approval list | backend | P0 | 200, both keys present |
| API-MOB-12 | Permission reply (once/always/reject) | backend | P0 | 200, audit row |
| API-MOB-13 | Question answer | backend | P0 | 200, audit row |
| API-MOB-14 | Question reject | backend | P0 | 200, audit row |
| API-MOB-15 | Approval against other workspace's request_id | backend | P0 | 400/401 (scope fails closed) |
| API-MOB-16 | Events snapshot | backend | P1 | 200 or 503 (document) |
| DATA-01 | After Flow A | DB | P0 | `opencode_sessions` row |
| DATA-02 | After Flow A | DB | P1 | `opencode_session_history` rows |
| DATA-03 | After Flow B/C | DB | P0 | `audit_entries` row per action |
| AUDIT-01 | `/api/audit/logs?action=mobile.approval.` | DB | P0 | entries present |
| AUDIT-02 | `tenant_id` matches `claims.WorkspaceID` | DB | P1 | exact match |
| UI-IOS-01 | Login submit | iOS sim | P0 | token stored, list loads |
| UI-IOS-02 | Session SSE live | iOS sim | P0 | events appear w/o manual refresh |
| UI-IOS-03 | Approval sheet responds | iOS sim | P0 | pending → resolved |
| UI-IOS-04 | Light/dark | iOS sim | P2 | matches system |
| UI-AND-01 | Login submit | Android emu | P0 | as iOS-01 |
| UI-AND-02 | Back button stack | Android emu | P0 | exits on root only |
| UI-AND-03 | SSE live | Android emu | P0 | as iOS-02 |
| UI-WEB-01 | Breakpoint 375/768/1280 | Vite dev | P1 | layout changes |
| UI-WEB-02 | SSE reconnect | Vite dev | P2 | reconnect after `online`/`offline` toggle |

---

## 11. Risk Register

1. **`agent-companion` is not a binary in this repo.** References are comment-only (`backend/internal/agent/adapter_pi_test.go:495`). Writes (`POST /prompt`, `DELETE`, `interrupt`) will return 502 unless an OpenCode upstream is reachable. Mitigation: point `POCKET_OPENCODE_INSTANCES` at an external OpenCode service, or accept 502 and document.
2. **Auth scheme nuance:** `requireAuth` accepts `Authorization: Bearer ...` OR `?token=...` for paths containing `/event` (`auth_helper.go:53`). Tests using SSE should prefer header but can fall back to query.
3. **PG vs SQLite at boot.** `scripts/start-dev.sh` does not set `POCKET_POSTGRES_DSN`, so audit + session tables are absent. For §7 / §8 to be meaningful, run via `deploy-local.sh` (which writes the DSN into `${POCKET_CONFIG_DIR}/.env.local`) and start `pocketd` with that env.
4. **Idempotency cache is in-memory** (`mobileCreates`). Restarting `pocketd` clears it; replays after restart will create new upstream sessions — known behavior, do not flag.
5. **Audit writes are limited to approval handlers** (`mobile_approval_handler.go:97,121,143`). Sessions/prompt/interrupt/interrupt/delete/interrupt write no audit row. The matrix in §10 reflects this.
6. **`events/snapshot` returns 503** if `mobileEventsBroadcaster` was never wired at boot (`mobile_events_handler.go:46-50`). Treat 503 as "broadcaster not configured" not "test failed".
7. **`permMgr`/`quesMgr` state is in-memory** (`permission_manager.go:48`). Restarting `pocketd` drops pending approvals. For repeatable tests, do not restart between Flow B and §7 verification.
8. **Capacitor shells require a built `frontend/dist`.** Running `npx cap run ios|android` without `npm run build` first will serve a stale bundle. Always run `npm ci && npm run build && npx cap sync ...` before emulator launches.
9. **Mobile `adb reverse` is required** for the Android emulator to reach the host backend. The Capacitor config (`frontend/capacitor.config.ts`) typically uses `http://10.0.2.2:8088` which avoids this; verify which is in use before the run.
10. **`X-User-ID` / `X-Tenant-ID` headers set in `requireAuth`** (`auth_helper.go:82-84`) are derived from JWT claims — treat them as authoritative for any downstream audit row.

---

## Appendix A — File references

| Topic | File:Line |
|---|---|
| Mobile route mounts | `backend/internal/server/server.go:741-745` |
| Session create | `backend/internal/server/mobile_session_handler.go:141` |
| Session list / since filter | `backend/internal/server/mobile_session_handler.go:194` |
| Session SSE (manager path) | `backend/internal/server/mobile_session_handler.go:379` |
| Session event filter | `backend/internal/server/mobile_session_handler.go:527` |
| Prompt | `backend/internal/server/mobile_session_handler.go:616` |
| Interrupt | `backend/internal/server/mobile_session_handler.go:659` |
| Approval list | `backend/internal/server/mobile_approval_handler.go:59` |
| Permission reply + audit | `backend/internal/server/mobile_approval_handler.go:71-98` |
| Question reply | `backend/internal/server/mobile_approval_handler.go:101` |
| Question reject | `backend/internal/server/mobile_approval_handler.go:125` |
| Events snapshot | `backend/internal/server/mobile_events_handler.go:36` |
| `requireMobileWorkspace` | `backend/internal/server/mobile_endpoint_scope.go:236-249` |
| `requireAuth` (header + token-fallback) | `backend/internal/server/auth_helper.go:45-87` |
| Legacy auth.Middleware | `backend/internal/auth/middleware.go:16-41` |
| Audit entry schema | `backend/internal/redclaw/audit_pg.go:46-63` |
| OpenCode sessions table | `backend/internal/opencode/store.go:282-302` |
| Audit writer | `backend/internal/server/audit_writer.go:70-135` |
| Long-lived SSE middleware | `backend/internal/server/server.go:804-826` |
| Start script (dev) | `scripts/start-dev.sh` |
| Local deploy script | `deploy-local.sh` |
| Top-level .env | `.env` |
| Backend .env.example | `backend/.env.example` |
| Backend .env.test | `backend/.env.test` |
| iOS Capacitor config | `frontend/capacitor.config.ts` |
| Android Capacitor config | `frontend/capacitor.config.ts` |
| Mock LLM gateway | `mock-llm-gateway.js` |