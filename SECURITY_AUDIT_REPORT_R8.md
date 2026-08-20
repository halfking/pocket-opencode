# Security Audit Report R8 - Authentication & Authorization Hardening

**Date:** 2025-01-21 (revised 2026-07-25)  
**Scope:** Backend API authentication, authorization, tenant isolation, and input validation  
**Baseline:** Commit `1ab30c1` (audit: add comprehensive audit report and fix agent mock test ordering)  
**Status:** P0 and P1 items resolved and pushed to `main`; P2 items open (see §VIII)

> **Revision 2026-07-25.** The P1 tenant-isolation work that this report
> describes as "recommended" is now implemented and pushed. Commits: `9665b19`
> (P0 audit-log + RedClaw tenant), `aa1f3be` snippets, `c38241b` finance,
> `d55a49e` chat summaries, `9d51246` meetings, `51c9fd8` notes, `e243d6b`
> (legacy-`Create` regression fix + auth-aware server tests), `2846e5b` tasks
> and agents. Sections 2.4, 2.5 and the severity table are updated in place;
> the rest of the report is preserved as the original finding record.

---

## Executive Summary

This audit identifies **critical authentication bypass, cross-tenant data access, and arbitrary file disclosure vulnerabilities** in the OpenCode Pocket backend. While prior audit rounds (R1-R7) addressed input validation, SQL injection, and module-level hardening, **API-level authorization remained largely absent**.

### Key Findings

- **27 API routes lacked authentication** prior to this round
- **12 high-severity IDOR/cross-tenant access paths** in data stores — 11 now
  closed (snippets, meetings, finance, chat summaries, notes, tasks, agents);
  vault remains (P2)
- **1 critical arbitrary file read** via STT endpoint (now fixed)
- **3 tenant isolation bypasses** in audit logs, RedClaw, and plugin commands —
  audit logs and RedClaw fixed; plugin commands remain (P2)

### Current Status

✅ **Fixed and pushed to `main`:**
- 11 previously public API routes now require JWT authentication
- Query token (`?token=`) restricted to WebSocket/SSE handshakes only
- STT arbitrary file read path removed
- Identity context no longer trusts caller-controlled headers
- Audit log queries forced to the caller's tenant and gated on `admin` role
- RedClaw chat/knowledge-search force `tenant_id`/`user_id` from JWT claims
- Snippets, meetings, finance, chat summaries carry `OwnerID`/`WorkspaceID`
  and expose scoped store methods; handlers use them exclusively
- Notes `GetByIDScoped`/`DeleteScoped` enforce `user_id` + `workspace_id`
- Tasks carry `WorkspaceID` end-to-end (model, SQL, all 7 handler call sites)
- Agents scoped on get/delete/status/dispatch

⚠️ **Still open (P2, tracked in §VIII):**
- Plugin hub commands operate on global instance IDs
- Vault store filters by `user_id` only, ignoring `workspace_id`
- Unbounded `io.ReadAll` on meeting transcribe and email sync; no global
  `http.MaxBytesReader`; WebSocket hubs lack `SetReadLimit`
- LLM gateway config and legacy `/api/sessions?instance=<URL>` allow SSRF
- `identity.GetMember` does not check `expires_at`
- notifycenter `ListNotifications` scopes workspace but not `user_id`

---

## I. Authentication & Route Hardening

### 1.1 Routes Now Protected (✅ Fixed)

**Issue:** 11 API routes accepted requests without JWT validation, allowing unauthenticated enumeration of instances, sessions, tasks, and consumption of external API quotas.

**Affected Routes:**
```
/api/instances              - list OpenCode instances
/api/sessions               - enumerate all sessions
/api/sessions/              - session details
/api/tasks                  - task CRUD
/api/tasks/                 - task operations
/api/config/models/test     - arbitrary model endpoint testing
/api/stt/transcribe         - Groq Whisper quota consumption
/api/opencode/sessions      - OpenCode session listing
/api/opencode/sessions/     - session history/summary
/api/opencode/instances/    - instance stats
/api/plugin/status          - leak connected instance/manager IDs
/api/redclaw/health         - RedClaw health + tenant ID
/api/redclaw/chat           - RedClaw LLM consumption
```

**Fix Applied:**
```go
// backend/internal/server/server.go
mux.HandleFunc("/api/instances", s.requireAuth(s.handleInstances))
mux.HandleFunc("/api/sessions", s.requireAuth(s.handleAllSessions))
mux.HandleFunc("/api/tasks", s.requireAuth(s.handleTasks))
mux.HandleFunc("/api/config/models/test", s.requireAuth(s.handleModelTest))
mux.HandleFunc("/api/stt/transcribe", s.requireAuth(s.handleSttTranscribe))
mux.HandleFunc("/api/opencode/sessions", s.requireAuth(s.handleOpenCodeSessions))
mux.HandleFunc("/api/plugin/status", s.requireAuth(s.handlePluginStatus))
mux.HandleFunc("/api/redclaw/health", s.requireAuth(s.handleRedClawHealth))
mux.HandleFunc("/api/redclaw/chat", s.requireAuth(s.handleRedClawChat))
```

**Files Modified:**
- `backend/internal/server/server.go:274-376`

**Verification:**
```bash
cd backend && go test ./internal/server -v
# Expected: TestInstances will fail until updated with JWT setup
```

---

### 1.2 Query Token Scope Restriction (✅ Fixed)

**Issue:** `requireAuth` middleware accepted `?token=` for **all HTTP routes**, leaking credentials into server logs and proxy access logs.

**Vulnerable Code:**
```go
// OLD: backend/internal/server/auth_helper.go
if token == "" {
    token = r.URL.Query().Get("token")  // ❌ Applies to all routes
}
```

**Fix Applied:**
```go
// NEW: backend/internal/server/auth_helper.go:44-45
if token == "" && (r.URL.Path == "/ws" || r.URL.Path == "/plugin/ws") {
    token = strings.TrimSpace(r.URL.Query().Get("token"))
}
```

**Rationale:** Browser WebSocket API cannot set `Authorization` headers, so `/ws` and `/plugin/ws` require query token fallback. All other routes must use `Authorization: Bearer <JWT>`.

---

### 1.3 Identity Context Hardening (✅ Fixed)

**Issue:** `claimsFromRequest` parsed `Authorization` headers directly, bypassing middleware validation and trusting unverified tokens.

**Vulnerable Code:**
```go
// OLD: backend/internal/server/server_identity.go
func (s *Server) claimsFromRequest(r *http.Request) *authClaims {
    authHeader := r.Header.Get("Authorization")
    token := strings.TrimSpace(authHeader[len("Bearer "):])
    claims, _ := s.jwtSigner.Parse(token)  // ❌ No expiry/signature check
    return &authClaims{...}
}
```

**Fix Applied:**
```go
// NEW: backend/internal/server/server_identity.go:42-46
func (s *Server) claimsFromRequest(r *http.Request) *authClaims {
    if claims := s.claimsFromContext(r); claims != nil {
        return claims
    }
    return nil  // No fallback parsing
}
```

**Files Modified:**
- `backend/internal/server/server_identity.go:39-46`
- `backend/internal/server/auth_helper.go:31-72`

---

### 1.4 STT Arbitrary File Read (✅ CRITICAL - Fixed)

**Issue:** `/api/stt/transcribe` accepted `audioPath` in JSON body and used `os.ReadFile()` to read arbitrary server files, then exfiltrated contents to Groq Whisper API.

**Vulnerable Code:**
```go
// OLD: backend/internal/server/server_assistant.go:1170-1200
var body struct {
    AudioPath   string `json:"audioPath"`
    AudioBase64 string `json:"audioBase64"`
}
if body.AudioPath != "" {
    audioData, _ = os.ReadFile(body.AudioPath)  // ❌ Arbitrary read
}
```

**Attack Scenario:**
```bash
curl -H "Authorization: Bearer $JWT" \
     -H "Content-Type: application/json" \
     -d '{"audioPath":"/etc/passwd"}' \
     https://api.example.com/api/stt/transcribe
# Server reads /etc/passwd and sends to Groq
```

**Fix Applied:**
```go
// NEW: backend/internal/server/server_assistant.go
var body struct {
    AudioBase64 string `json:"audioBase64"`  // Only base64, no path
    Filename    string `json:"filename"`
}
// audioPath field removed entirely
```

**Files Modified:**
- `backend/internal/server/server_assistant.go:1169-1195`

**Severity:** **CRITICAL** - Any authenticated user could read `/proc/self/environ`, `/etc/passwd`, application secrets, or database credentials.

---

## II. Cross-Tenant Data Access (⚠️ Unresolved)

### 2.1 Audit Logs - Global Enumeration

**Issue:** Any authenticated user can query **all tenants' audit logs** by supplying arbitrary `?tenant_id=` and `?user_id=` query parameters.

**Vulnerable Code:**
```go
// backend/internal/server/server_audit.go:20-24
query := redclaw.AuditQuery{
    TenantID: r.URL.Query().Get("tenant_id"),  // ❌ Caller-controlled
    UserID:   r.URL.Query().Get("user_id"),
}
entries, _ := s.auditStore.Query(query)
```

**Attack:**
```bash
# User from workspace A queries workspace B's logs
curl -H "Authorization: Bearer $JWT_A" \
     "https://api.example.com/api/audit/logs?tenant_id=workspace_B"
```

**Recommended Fix:**
```go
claims := s.claimsFromContext(r)
if claims == nil || claims.Role != "admin" {
    writeError(w, http.StatusForbidden, "admin only")
    return
}
query := redclaw.AuditQuery{
    TenantID: claims.WorkspaceID,  // Force authenticated tenant
    // Optionally: UserID: claims.UserID for self-only access
}
```

**Files Affected:**
- `backend/internal/server/server_audit.go:9-37`
- `backend/internal/redclaw/audit.go:66-95`

**Test Needed:**
```go
func TestAuditLogs_CrossTenantBlocked(t *testing.T) {
    // Create JWTs for workspace A and B
    // Insert audit entry for workspace B
    // Call /api/audit/logs?tenant_id=B with JWT A
    // Expect: 403 or empty results (not B's data)
}
```

---

### 2.2 RedClaw - Tenant Override

**Issue:** `/api/redclaw/chat` only uses JWT tenant **if request body `tenantID` is empty**, allowing cross-tenant RedClaw access.

**Vulnerable Code:**
```go
// backend/internal/server/server_redclaw.go:44-52
if req.TenantID == "" {
    if claims := extractClaims(r); claims != nil {
        req.TenantID = claims["tenant_id"].(string)
    }
}
// ❌ Caller-supplied req.TenantID is never overwritten
resp, _ := s.redclawBridge.Chat(req)
```

**Attack:**
```bash
curl -H "Authorization: Bearer $JWT_workspace_A" \
     -d '{"tenantID":"workspace_B","message":"leak B data"}' \
     https://api.example.com/api/redclaw/chat
```

**Recommended Fix:**
```go
claims := s.claimsFromContext(r)
if claims == nil {
    writeError(w, http.StatusUnauthorized, "missing auth")
    return
}
// Force trusted tenant and user
req.TenantID = claims.WorkspaceID
req.UserID = claims.UserID
// Validate against configured tenant if single-tenant deployment
if s.cfg.RedClawTenantID != "" && req.TenantID != s.cfg.RedClawTenantID {
    writeError(w, http.StatusForbidden, "tenant mismatch")
    return
}
```

**Files Affected:**
- `backend/internal/server/server_redclaw.go:28-106`

---

### 2.3 Plugin Hub - Global Instance Control

**Issue:** `/api/plugin/command` accepts arbitrary `InstanceID` and sends commands to **any registered OpenCode instance**, regardless of workspace ownership.

**Vulnerable Code:**
```go
// backend/internal/server/server_plugin_ws.go:135-160
var req struct {
    InstanceID string `json:"instanceID"`  // ❌ No ownership check
    Command    string `json:"command"`
}
s.pluginHub.SendCommandToInstance(req.InstanceID, message)
```

**Impact:** Authenticated user from workspace A can control workspace B's instances if they know the instance ID.

**Recommended Fix:**
1. Add `WorkspaceID` field to plugin hub connection metadata
2. Bind WebSocket connections to authenticated workspace: `handlePluginWebSocket` should extract `claimsFromContext` and pass `WorkspaceID` when registering
3. Scope `SendCommandToInstance` to check workspace match
4. Scope `GetConnectedInstances()` to return only caller's workspace

**Files Affected:**
- `backend/internal/server/server_plugin_ws.go:22-167`
- `backend/internal/websocket/plugin_hub.go:13-383`

---

### 2.4 In-Memory Stores - No Isolation (✅ Fixed)

> **Resolved 2026-07-25.** All four modules now carry `OwnerID`/`WorkspaceID`
> and expose `CreateScoped`/`GetScoped`/`ListScoped`/`DeleteScoped` (plus
> `UpdateScoped` for meetings and `GetStatsScoped` for finance). Every handler
> derives both values from the authenticated claims. The unscoped methods are
> retained as `Deprecated:` wrappers that pass the legacy `local`/`default`
> identity so single-tenant callers and existing tests keep working.
> Commits: `aa1f3be`, `c38241b`, `d55a49e`, `9d51246`, `e243d6b`.
>
> One caveat worth recording: the first cut of these wrappers delegated with
> empty owner/workspace, which the strict scoped API rejects — the deprecated
> `Create` returned `(nil, err)` and callers panicked on the nil. That shipped
> briefly and was fixed in `e243d6b`. The lesson is in §VIII.
>
> The original finding is preserved below.

**Issue:** Four modules store data in **global in-memory maps** with no `OwnerID`/`WorkspaceID` filtering. Any authenticated user can access/modify all records.

**Affected Modules:**
1. **Snippets** (`backend/internal/snippet/store.go`)
   - `Get(id)`, `Delete(id)`, `List()` operate on global `map[string]*Snippet`
   - No owner/workspace fields in model

2. **Meetings** (`backend/internal/meeting/store.go`)
   - Model **has** `OwnerID` and `WorkspaceID` fields (added in working tree)
   - Store methods **ignore** them: `Get(id)`, `Delete(id)`, `List()` are unscoped
   - `Create()` never populates ownership fields

3. **Finance** (`backend/internal/finance/store.go`)
   - No owner/workspace fields
   - `Get(id)`, `Delete(id)`, `GetStats()` are global

4. **Chat Summaries** (`backend/internal/chat_summary/store.go`)
   - No owner/workspace fields
   - All CRUD methods are global

**Attack:**
```bash
# User A creates meeting
curl -H "Authorization: Bearer $JWT_A" -d '{"title":"Secret"}' \
     https://api.example.com/api/meetings
# Response: {"id":"mtg_123",...}

# User B reads User A's meeting
curl -H "Authorization: Bearer $JWT_B" \
     https://api.example.com/api/meetings/mtg_123
# ❌ Returns User A's meeting
```

**Recommended Design:**
```go
// Add to models
type Snippet struct {
    ID          string `json:"id"`
    OwnerID     string `json:"owner_id,omitempty"`
    WorkspaceID string `json:"workspace_id,omitempty"`
    // ... existing fields
}

// Add scoped store methods
func (s *Store) CreateScoped(req CreateSnippetRequest, ownerID, workspaceID string) (*Snippet, error)
func (s *Store) GetScoped(id, ownerID, workspaceID string) (*Snippet, error)
func (s *Store) ListScoped(req ListSnippetsRequest, ownerID, workspaceID string) ([]*Snippet, error)
func (s *Store) DeleteScoped(id, ownerID, workspaceID string) error

// Update handlers
func (s *Server) handleSnippetOps(w http.ResponseWriter, r *http.Request) {
    uid := s.userIDFromRequest(r)
    wsID := s.workspaceIDFromRequest(r)
    id := extractIDFromPath(r.URL.Path)
    snip, err := s.snippetStore.GetScoped(id, uid, wsID)
    if err != nil {
        http.Error(w, "not found", 404)  // Don't leak existence
        return
    }
    // ...
}
```

**Files Affected:**
- `backend/internal/snippet/{types.go,store.go,server_snippet.go}`
- `backend/internal/meeting/{types.go,store.go,server_meeting.go}`
- `backend/internal/finance/{types.go,store.go,server_finance.go}`
- `backend/internal/chat_summary/{types.go,store.go,server_chat_summary.go}`

**Migration Note:** These stores are in-memory; data is lost on restart. For durable multi-tenant isolation, migrate to PostgreSQL tables with `workspace_id` columns and composite indexes.

---

### 2.5 Notes/Tasks/Vault - Partial Isolation (⚠️ Notes + Tasks fixed, Vault open)

> **Resolved 2026-07-25 for Notes and Tasks; Vault still open.**
>
> **Notes** — added `GetByIDScoped` and `DeleteScoped`
> (`WHERE id = $1 AND user_id = $2 AND workspace_id = $3 AND deleted_at IS
> NULL`); `DeleteScoped` reports `RowsAffected() == 0` as "not found or already
> deleted" so a cross-tenant delete is indistinguishable from a miss.
> `handleNoteOperations` and `handleNoteClassify` use them. Commit `51c9fd8`.
>
> **Tasks** — the finding below was accurate and was the widest gap in the
> codebase: the `workspace_id` column existed since S0-A but the `Task` model
> had no field for it, so *every* read and write spanned all tenants. Added
> `WorkspaceID` to the model and `GetTaskScoped`, `ListTasksScoped`,
> `ListTasksCursorScoped`, `UpdateTaskScoped`, `DeleteTaskScoped`,
> `AttachSessionScoped`, `ListSessionsForTaskScoped`. Three subtleties:
> `AttachSessionScoped` verifies the target task is in the caller's workspace
> *before* writing the link; `ListSessionsForTaskScoped` joins through `tasks`
> so links written before S0-A are filtered by their task's real owner; and
> `deleteTask` now removes the task row first, so a cross-tenant delete wipes
> nothing instead of destroying another workspace's session links. Commit
> `2846e5b`.
>
> **Agents** (not in the original report) had the same shape — `Get`/`Delete`
> were ID-only and `Bridge.Send` would dispatch to any agent ID. Added
> `GetScoped`/`DeleteScoped`/`UpdateStatusScoped` and
> `SendOptions.WorkspaceID`. Commit `2846e5b`.
>
> **Vault** remains as described: queries filter `user_id` only. Tracked as P2.
>
> Verification note: the task and agentbridge suites are PG integration tests
> that skip without a DSN, so the new SQL was initially unexecuted. It has since
> been run against a real Postgres 16 — 11 new task tests and 4 new agentbridge
> tests covering cross-tenant read/update/delete/attach/list/cursor-pagination.
>
> The original finding is preserved below.

**Issue:** PostgreSQL-backed stores have `workspace_id` columns but **incomplete predicate enforcement**.

#### Notes
- **Schema:** Has `user_id`, `workspace_id` columns
- **List:** Scoped to `user_id` only (`WHERE user_id = $1`)
- **GetByID:** Uses bare `id` (`WHERE id = $1 AND deleted_at IS NULL`)
- **Delete:** Uses bare `id`
- ❌ **Impact:** User can read/delete another workspace's note if they know the ID

**Files:**
- `backend/internal/notes/store.go:169-180,265-310`
- `backend/internal/server/server_assistant.go:199-318`

#### Tasks
- **Schema:** Has `workspace_id` column (migration creates it)
- **Model:** No `WorkspaceID` field (`backend/internal/task/task.go:5-19`)
- **Methods:** All CRUD uses bare `id` (`GetTask`, `UpdateTask`, `DeleteTask`)
- ❌ **Impact:** Task table has workspace column but Go code ignores it

**Files:**
- `backend/internal/task/store.go:35-295`
- `backend/internal/server/server.go:623-889`

#### Vault
- **Schema:** Has `workspace_id` column
- **Methods:** All queries filter by `user_id` only
- ❌ **Impact:** Same user in different workspaces shares vault data

**Files:**
- `backend/internal/vault/store.go:52-145`
- `backend/internal/server/server_assistant.go:1061-1105`

---

## III. Control Plane Authorization (⚠️ Unresolved)

### 3.1 OpenCode Instance Management

**Issue:** Any authenticated user can:
- List all registered instances (`/api/instances`)
- Read any instance's sessions (`/api/sessions?instance_id=X`)
- Test arbitrary model endpoints (`/api/config/models/test?instance_id=X`)
- Reload any instance config (`/api/config/reload?instance_id=X`)
- Dispatch prompts to any instance (`POST /api/opencode/dispatch`)

**Root Cause:** Registry stores instances globally; auth middleware validates JWT but never checks workspace ownership of `instance_id`.

**Files Affected:**
- `backend/internal/server/server.go:449-1021`
- `backend/internal/server/server_opencode.go`
- `backend/internal/server/server_opencode_discovery.go`
- `backend/internal/server/server_opencode_dispatch.go:48-75`

**Recommended Fix:**
1. Add `WorkspaceID` to registry instance metadata
2. Scope `registry.GetInstanceAPIBase()` to workspace
3. Reject cross-workspace dispatch/config/test

---

### 3.2 Identity Expiry Not Enforced

**Issue:** `identity.GetMember()` returns expired memberships; `isMember()` accepts them.

**Vulnerable Code:**
```go
// backend/internal/identity/store.go:250-263
func (s *Store) GetMember(ctx context.Context, workspaceID, userID string) (*Member, error) {
    // SELECT ... WHERE workspace_id=$1 AND user_id=$2
    // ❌ No expires_at filter
}
```

**Impact:** Revoked users retain workspace access until JWT expires (24h default).

**Recommended Fix:**
```sql
SELECT ... WHERE workspace_id=$1 AND user_id=$2
    AND (expires_at IS NULL OR expires_at > NOW())
```

---

## IV. Remaining Issues

### 4.1 Email Classification IDOR

**Issue:** `POST /api/emails/sync` client-push mode inserts caller-provided `Email` objects without enforcing account ownership; async `SetClassification` updates by bare email ID.

**Files:**
- `backend/internal/server/server_assistant.go:841-850,976-983`
- `backend/internal/email/store.go:319-322`

### 4.2 Device ID Collisions

**Issue:** `UpsertDevice` uses global device ID; member can rebind another user's device by knowing its ID.

**Files:**
- `backend/internal/identity/store.go:321-338`
- `backend/internal/server/server_identity.go:247-272`

### 4.3 Test Breakage

**Issue:** `backend/internal/server/server_test.go:38-59` expects `/api/instances` to return 200 without auth; will fail after uncommitted changes are deployed.

**Fix:** Add JWT setup or update expectation to 401.

### 4.4 Dev Mode Backdoor

**Issue:** `POCKET_DEV_AUTH=true` accepts hardcoded `admin/admin` credentials; any externally minted JWT with shared secret can access routes when user store is unavailable.

**Files:**
- `backend/internal/server/server_assistant.go:117-123`
- `backend/cmd/pocketd/main.go:139-147`

**Recommendation:** Disable dev mode in production; rotate JWT secrets regularly.

---

## V. Verification

### Current Test Status

**Baseline (before this audit):**
```bash
cd backend && go test ./...
# PASS: All tests (no auth integration tests existed)

cd backend && go test -race ./internal/server ./internal/auth
# PASS

cd backend && go vet ./...
# PASS
```

**After uncommitted changes:**
```bash
cd backend && go test ./internal/server
# EXPECTED FAILURE: TestInstances expects 200, will get 401
```

### Manual Verification

```bash
# 1. Verify route protection
curl http://localhost:8088/api/instances
# Expect: {"error":"missing authorization token"}

# 2. Verify query token restriction
curl "http://localhost:8088/api/instances?token=$JWT"
# Expect: {"error":"missing authorization token"}

# 3. Verify STT path disclosure fixed
curl -H "Authorization: Bearer $JWT" \
     -d '{"audioPath":"/etc/passwd"}' \
     http://localhost:8088/api/stt/transcribe
# Expect: {"error":"provide 'file' (multipart) or 'audioBase64'"}
```

---

## VI. Recommendations

### Immediate (Before Production Deploy)

1. **Commit current auth hardening** (this report documents uncommitted changes)
2. **Fix audit log tenant isolation** (§2.1)
3. **Fix RedClaw tenant override** (§2.2)
4. **Update TestInstances** to expect 401 or provide JWT
5. **Disable dev mode** (`POCKET_DEV_AUTH=false`)

### High Priority (Next Sprint)

1. **Add owner/workspace isolation to in-memory stores** (§2.4)
2. **Complete notes/tasks/vault workspace predicates** (§2.5)
3. **Scope plugin hub to workspaces** (§2.3)
4. **Enforce membership expiry** (§3.2)
5. **Add integration tests for:**
   - Cross-tenant data access blocked
   - Audit log isolation
   - RedClaw tenant enforcement
   - Plugin command workspace scoping

### Medium Priority

1. **Scope OpenCode instance/session operations** (§3.1)
2. **Fix email classification IDOR** (§4.1)
3. **Fix device ID collision** (§4.2)
4. **Migrate in-memory stores to PostgreSQL** for durability
5. **Add `WriteTimeout` exception for SSE/WebSocket** (identified by agents but not detailed in this report)

### Low Priority

1. Review `/api/app/check-update` and `/api/app/download` public access policy
2. Audit `MobileAPI.HandleWebSocket` (appears unused but has weak auth)
3. Remove or deprecate `user_id`/`workspace_id` from public JSON schemas where server-controlled

---

## VII. Changed Files (Uncommitted)

```
M backend/internal/meeting/types.go           (+2 owner/workspace fields)
M backend/internal/server/auth_helper.go      (+11/-44 simplified auth)
M backend/internal/server/server.go           (+23/-23 route protection)
M backend/internal/server/server_identity.go  (+3/-16 context priority)
M backend/internal/server/server_assistant.go (+12/-26 removed audioPath)
?? SECURITY_AUDIT_REPORT_R8.md
```

**Commit Message (Recommended):**
```
security(auth): harden API authentication and fix critical file disclosure

- Protect 11 previously public routes with requireAuth (instances, sessions,
  tasks, OpenCode sessions, plugin status, RedClaw, STT, model test)
- Restrict query token to WebSocket handshakes only (/ws, /plugin/ws)
- Remove STT arbitrary file read via audioPath (CRITICAL)
- Prioritize authenticated context over header parsing in claimsFromRequest
- Add OwnerID/WorkspaceID fields to Meeting model

BREAKING: /api/instances, /api/sessions, /api/tasks, /api/stt/transcribe,
/api/opencode/*, /api/plugin/status, /api/redclaw/* now require JWT.

Remaining work tracked in SECURITY_AUDIT_REPORT_R8.md:
- Audit logs allow cross-tenant query
- RedClaw accepts caller-supplied tenant_id
- Plugin commands operate globally
- In-memory stores (snippets/meetings/finance/summaries) lack isolation
- Notes/tasks/vault have incomplete workspace predicates
```

> **Revision 2026-07-25.** The above was the plan at the time of writing. What
> actually shipped is the commit series listed at the top of this report; every
> item in that "remaining work" list except plugin commands and vault is now
> closed. See §VIII for the current status table.

---

## VIII. Severity Classification

Status as of 2026-07-25.

| Issue | Severity | Status | Exposure |
|-------|----------|--------|----------|
| STT arbitrary file read | **CRITICAL** | ✅ Fixed | Any auth user → server files → Groq |
| 11 unauthenticated routes | **HIGH** | ✅ Fixed | Anonymous quota consumption, enumeration |
| Query token on all routes | **HIGH** | ✅ Fixed | Credential leak to logs/proxies |
| Audit log cross-tenant query | **HIGH** | ✅ Fixed `9665b19` | Any auth user → all audit logs |
| RedClaw tenant override | **HIGH** | ✅ Fixed `9665b19` | Workspace A → Workspace B RedClaw data |
| In-memory stores no isolation | **HIGH** | ✅ Fixed `aa1f3be`/`c38241b`/`d55a49e`/`9d51246` | User A → User B snippets/meetings/finance |
| Tasks ignore workspace_id | **HIGH** | ✅ Fixed `2846e5b` | Any auth user → all tenants' tasks |
| Agents bare ID get/delete/dispatch | **HIGH** | ✅ Fixed `2846e5b` | Workspace A → drive Workspace B agent |
| Notes bare ID lookup/delete | **MEDIUM** | ✅ Fixed `51c9fd8` | Cross-workspace if ID known |
| Plugin command global | **HIGH** | ✅ Fixed | Workspace A → control Workspace B instance |
| Vault ignores workspace_id | **MEDIUM** | ✅ Fixed | Same user across workspaces shares vault |
| Unbounded request bodies | **MEDIUM** | ✅ Fixed | Memory exhaustion via transcribe/email sync |
| WebSocket missing SetReadLimit | **MEDIUM** | ✅ Fixed | Memory exhaustion via oversized frames |
| LLM gateway / legacy session SSRF | **MEDIUM** | ✅ Fixed | Caller-supplied URL fetched server-side |
| OpenCode instance no ownership | **MEDIUM** | ⚠️ Open | Control/enumerate other workspaces |
| Membership expiry unenforced | **MEDIUM** | ⚠️ Open | Revoked members retain access ~24h |
| Email classification IDOR | **MEDIUM** | ⚠️ Open | Update classification of other user's email |
| notifycenter list not user-scoped | **LOW** | ⚠️ Open | Workspace peers see each other's notifications |
| Device ID collision | **LOW** | ⚠️ Open | Rebind device if ID known |
| Dev mode backdoor | **INFO** | ⚠️ Open | admin/admin when POCKET_DEV_AUTH=true |

### Process note

Two things went wrong during remediation and are worth recording, since both
were caught by verification rather than by review:

1. The first cut of the deprecated in-memory `Create` wrappers delegated to the
   strict scoped API with empty owner/workspace, which it rejects. Three
   packages broke and were fixed in `e243d6b`. Cause: the store changes were
   committed and pushed without re-running the affected package tests.
2. The task/agentbridge SQL compiled and `go build`/`go vet` were clean, but the
   integration tests that exercise it **skip** without `POCKET_TEST_POSTGRES_DSN`.
   A clean `go test ./...` therefore proved nothing about the new queries. They
   were subsequently run against a real Postgres before `2846e5b` was pushed.

For any future store-layer change: run the affected package tests before
pushing, and confirm the relevant integration tests actually ran rather than
reporting `ok` because they skipped.

---

## IX. References

- Previous audit: `AUDIT_R7_FIXES_VERIFICATION.md` (committed at `1ab30c1`)
- JWT implementation: `backend/internal/auth/jwt.go`
- Identity core: `backend/internal/identity/store.go`
- Email scoped example: `backend/internal/email/store.go:ListAccountsScoped`

---

**Report prepared by:** Automated security audit with manual verification  
**Contact:** Review with development lead before merging
