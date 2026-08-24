# OpenCode Contract — single source of truth

> The single, pinned, testable contract between `opencode-pocket` and the
> OpenCode upstream. Every other doc that references OpenCode HTTP routes,
> event shapes, or versions MUST defer to this file.
>
> **Do NOT map any RedClaw / pocket-internal task API onto these routes.**
> The adapter here speaks **only** OpenCode. Pocket's `/api/agent/*` and
> `/api/opencode/*` facades are a separate layer above this contract.

---

## 1. Pinned OpenCode version

| Field | Value |
|-------|-------|
| Release package (`packages/opencode/package.json`) | `1.17.9` |
| Git commit SHA (full) | `f12ac6f234ebe31982ee78f3359e8170cb09ffc9` |
| Git commit short | `f12ac6f23` |
| Commit subject | `fix(tui): reduce noisy MCP autocomplete matches (#33176)` |
| Release date (commit date) | `2026-06-21` |
| Source repository (local) | `/Users/xutaohuang/workspace/ai/opencode` |

The same commit is surfaced in Go as:

```go
// backend/internal/adapter/opencode_contract.go
const (
    OpenCodePinnedCommit  = "f12ac6f234ebe31982ee78f3359e8170cb09ffc9"
    OpenCodePinnedRelease = "1.17.9"
)
```

> The repository had no annotated tags at this commit, so the "release tag"
> is the `packages/opencode/package.json` version (`1.17.9`). A previous
> commit (`e84d94d99`, `chore: sync release versions for v1.17.9`) is the
> nearest tagging-equivalent commit and is the source of the version string.

Any change to the contract requires re-pinning (bump the constants and
update the route tables in §3).

---

## 2. Base URL assumptions

The OpenCode HTTP server listens on `host:port` where:

| Variable | Default | Source |
|----------|---------|--------|
| `hostname` | `127.0.0.1` (cli) / LAN-IP (mDNS) | `packages/opencode/src/server/server.ts:155-167` |
| `port` | `4096` (fallback to any free port) | `packages/opencode/src/server/server.ts:117-121` |
| mDNS domain | `_opencode._tcp.local` | `packages/opencode/src/server/mdns.ts` (consumer in `server.ts:154-168`) |
| Auth | per-request `Authorization: Bearer <token>` issued at `/auth` | `packages/opencode/src/server/auth.ts` |
| Content-Type | `application/json` for bodies; `text/event-stream` for `/event` | `handlers/event.ts:79-84` |

**Pocket convention**: instance URL is `http://<host>:<port>` with no path
prefix. All routes below are relative to that root.

---

## 3. HTTP route inventory (pinned v1.17.9)

All routes live under `packages/opencode/src/server/routes/instance/httpapi/`
and are mounted on `HostHttpApi` (`api.ts:44-67`) or `InstanceHttpApi`
(`api.ts:51-67`). Paths and request/response schemas are quoted from the
`HttpApiEndpoint` declarations.

### 3.1 Global routes (`groups/global.ts:67-138`)

| Method | Path | Operation ID | Request | Response (success) | Notes |
|--------|------|--------------|---------|--------------------|-------|
| GET    | `/global/health` | `global.health` | — | `{ healthy: true, version: string }` | `groups/global.ts:11-14, 78-86` |
| GET    | `/global/event`  | `global.event`  | — | SSE (text/event-stream) — global events | `groups/global.ts:87-95`, `handlers/global.ts` |
| GET    | `/global/config` | `global.config.get` | — | `ConfigV1.Info` | `groups/global.ts:96-104` |
| PATCH  | `/global/config` | `global.config.update` | `ConfigV1.Info` | `ConfigV1.Info` | `groups/global.ts:105-115` |
| POST   | `/global/dispose` | `global.dispose` | — | `boolean` | `groups/global.ts:116-124` |
| POST   | `/global/upgrade` | `global.upgrade` | `GlobalUpgradeInput` | `GlobalUpgradeResult` | `groups/global.ts:125-135` |

### 3.2 Event stream (`groups/event.ts`)

| Method | Path | Operation ID | Response |
|--------|------|--------------|----------|
| GET    | `/event` | `event.subscribe` | SSE — instance-scoped events |

Wire format (handlers/event.ts:12-86, see also `effect/unstable/encoding/Sse`):

```
event: message
id: <evt_xxxx>
data: {"id":"evt_...","type":"<type>","properties":{...}}
```

* First emitted frame is `{ id, type: "server.connected", properties: {} }`.
* A keep-alive heartbeat is emitted every **10 seconds** as
  `{ id, type: "server.heartbeat", properties: {} }` (handlers/event.ts:63-66).
* Connection terminates after `{ type: "server.instance.disposed" }` arrives
  (handlers/event.ts:42-62).
* Response headers (handlers/event.ts:77-84):
  `Content-Type: text/event-stream`,
  `Cache-Control: no-cache, no-transform`,
  `X-Accel-Buffering: no`,
  `X-Content-Type-Options: nosniff`.

### 3.3 Session routes (`groups/session.ts:78-105, 107-454`)

| Method | Path | Operation ID | Request | Response |
|--------|------|--------------|---------|----------|
| GET    | `/session` | `session.list` | `ListQuery` (`scope?`, `path?`, `roots?`, `start?`, `search?`, `limit?`) | `Session.Info[]` |
| GET    | `/session/status` | `session.status` | `WorkspaceRoutingQuery` | `Record<SessionID, SessionStatus.Info>` |
| GET    | `/session/:sessionID` | `session.get` | — | `Session.Info` |
| GET    | `/session/:sessionID/children` | `session.children` | — | `Session.Info[]` |
| GET    | `/session/:sessionID/todo` | `session.todo` | — | `Todo.Info[]` |
| GET    | `/session/:sessionID/diff` | `session.diff` | `DiffQuery` | `Snapshot.FileDiff[]` |
| GET    | `/session/:sessionID/message` | `session.messages` | `MessagesQuery` (`limit?`, `before?`) | `SessionV1.WithParts[]` |
| GET    | `/session/:sessionID/message/:messageID` | `session.message` | — | `SessionV1.WithParts` |
| POST   | `/session` | `session.create` | `Session.CreateInput?` (or empty) | `Session.Info` |
| DELETE | `/session/:sessionID` | `session.remove` | — | `boolean` |
| PATCH  | `/session/:sessionID` | `session.update` | `UpdatePayload` (`title?`, `metadata?`, `permission?`, `time.archived?`) | `Session.Info` |
| POST   | `/session/:sessionID/fork` | `session.fork` | `ForkPayload` | `Session.Info` |
| POST   | `/session/:sessionID/abort` | `session.abort` | — | `boolean` |
| POST   | `/session/:sessionID/init` | `session.init` | `InitPayload` | `boolean` |
| POST   | `/session/:sessionID/share` | `session.share` | — | `Session.Info` |
| DELETE | `/session/:sessionID/share` | `session.unshare` | — | `Session.Info` |
| POST   | `/session/:sessionID/summarize` | `session.summarize` | `SummarizePayload` | `boolean` |
| POST   | `/session/:sessionID/message` | `session.prompt` | `PromptPayload` | `SessionV1.WithParts` |
| POST   | `/session/:sessionID/prompt_async` | `session.prompt_async` | `PromptPayload` | `NoContent (204)` |
| POST   | `/session/:sessionID/command` | `session.command` | `CommandPayload` | `SessionV1.WithParts` |
| POST   | `/session/:sessionID/shell` | `session.shell` | `ShellPayload` | `SessionV1.WithParts` |
| POST   | `/session/:sessionID/revert` | `session.revert` | `RevertPayload` | `Session.Info` |
| POST   | `/session/:sessionID/unrevert` | `session.unrevert` | — | `Session.Info` |
| POST   | `/session/:sessionID/permissions/:permissionID` | `permission.respond` (deprecated) | `PermissionResponsePayload` | `boolean` |
| DELETE | `/session/:sessionID/message/:messageID` | `session.deleteMessage` | — | `boolean` |
| DELETE | `/session/:sessionID/message/:messageID/part/:partID` | `part.delete` | — | `boolean` |
| PATCH  | `/session/:sessionID/message/:messageID/part/:partID` | `part.update` | `SessionV1.Part` | `SessionV1.Part` |

### 3.4 Permission routes (`groups/permission.ts:11-65`)

| Method | Path | Operation ID | Request | Response |
|--------|------|--------------|---------|----------|
| GET    | `/permission` | `permission.list` | `WorkspaceRoutingQuery` | `PermissionV1.Request[]` |
| POST   | `/permission/:requestID/reply` | `permission.reply` | `{ reply: "once"\|"always"\|"reject", message?: string }` | `boolean` |

### 3.5 Question routes (`groups/question.ts:11-60`)

| Method | Path | Operation ID | Request | Response |
|--------|------|--------------|---------|----------|
| GET    | `/question` | `question.list` | `WorkspaceRoutingQuery` | `Question.Request[]` |
| POST   | `/question/:requestID/reply` | `question.reply` | `{ answers: string[][] }` | `boolean` |
| POST   | `/question/:requestID/reject` | `question.reject` | — | `boolean` |

### 3.6 Other groups (relevant but not yet wired in pocket adapter)

These are present in the pinned source but the Pocket adapter does not yet
call them. They are tracked here so a future bump does not silently drift.

* `groups/project.ts`, `groups/workspace.ts`, `groups/file.ts`,
  `groups/provider.ts`, `groups/config.ts`, `groups/mcp.ts`,
  `groups/instance.ts`, `groups/control.ts`, `groups/control-plane.ts`,
  `groups/tui.ts`, `groups/pty.ts`, `groups/sync.ts`,
  `groups/project-copy.ts`, `groups/experimental.ts`, `groups/mobile.ts`,
  `groups/metadata.ts`, `groups/query.ts`.

---

## 4. Schema references

### 4.1 `Session.Info`
From `packages/opencode/src/session/session.ts:213-233`. Critical fields:

| Field | Type | Notes |
|-------|------|-------|
| `id` | branded `SessionID` (`ses_*`) | |
| `slug` | `string` | |
| `projectID` | `ProjectV2.ID` | |
| `workspaceID` | `WorkspaceV2.ID?` | |
| `directory` | `string` | |
| `parentID` | `SessionID?` | |
| `title` | `string` | |
| `agent` | `string?` | |
| `model` | `{ providerID, modelID }?` | |
| `version` | `string` | OpenCode session version |
| `metadata` | `Record<string, any>?` | |
| `time.created` | `number` (Unix ms) | |
| `time.updated` | `number` (Unix ms) | |
| `time.archived` | `number?` (Unix ms) | |

### 4.2 `SessionV1.WithParts`
From `packages/core/src/v1/session.ts:495-502`. Response shape for
`POST /session/:sessionID/message`:

```ts
{
  info:   User | Assistant,        // discriminated by `role`
  parts:  Part[]                   // see 4.3
}
```

`Assistant` (`v1/session.ts:455-490`) carries `cost`, `tokens`, `modelID`,
`providerID`, `mode`, `agent`, `path.cwd`, `path.root`, `summary`,
`structured`, `variant`, `finish`, plus optional `error`.

### 4.3 Message parts (the `parts[]` array)

Defined in `packages/core/src/v1/session.ts:309-377`, discriminated by
`type`:

* `text` — `{ id, sessionID, messageID, text, synthetic?, ignored?, time? }`
* `reasoning` — `{ id, sessionID, messageID, text, metadata?, time }`
* `file` — `{ id, sessionID, messageID, mime, filename?, url, source? }`
* `agent` — `{ id, sessionID, messageID, name, source? }`
* `subtask` — `{ id, sessionID, messageID, prompt, description, agent, model?, command? }`
* `tool` — `{ id, sessionID, messageID, callID, tool, state, metadata? }`
  where `state` is `pending | running | completed | error`
* `step-start` / `step-finish` / `compaction` / `retry`

### 4.4 Prompt request (`POST /session/:sessionID/message`)

Body is `PromptPayload` = `Omit<PromptInput, "sessionID">`
(`packages/opencode/src/session/prompt.ts:1579-1601`):

```ts
{
  messageID?: MessageID,
  model?:     { providerID, modelID },
  agent?:     string,
  noReply?:   boolean,
  format?:    OutputFormatText | OutputFormatJsonSchema,
  system?:    string,
  variant?:   string,
  parts: Array<
    | TextPartInput    // { type:"text",    text:string, ... }
    | FilePartInput    // { type:"file",    mime:string, url:string, source? }
    | AgentPartInput   // { type:"agent",   name:string, source? }
    | SubtaskPartInput // { type:"subtask", prompt, description, agent, ... }
  >
}
```

> The legacy `{ prompt: { text } }` shape used by the **plugin** and
> **manager** is NOT valid against the pinned upstream. They must be
> migrated to `parts: [{ type:"text", text }]`. See §6.

### 4.5 Event stream payload

The internal `EventV2.Payload` (`packages/core/src/event.ts:40-51`):

```ts
{
  id:       EventV2.ID,             // "evt_..."
  type:     string,                 // see registry
  data:     Record<string, unknown>,
  version?: number,
  location?: Location.Ref,           // { directory, workspaceID? }
  metadata?: Record<string, unknown>
}
```

The HTTP handler maps that to a public frame (`handlers/event.ts:40`):

```json
{ "id": "evt_...", "type": "<type>", "properties": { ... data ... } }
```

`type` values seen by the adapter include (non-exhaustive):
`server.connected`, `server.heartbeat`, `server.instance.disposed`,
`session.created`, `session.updated`, `session.deleted`,
`session.compacted`, `session.idle`, `session.error`,
`message.updated`, `message.part.updated`,
`permission.asked`, `permission.replied`,
`question.asked`, `question.replied`, `question.rejected`.

### 4.6 Permission (`PermissionV1.Request`) and Question (`Question.Request`)

`PermissionV1.Request` carries `id`, `sessionID`, `permission` (verb),
`patterns[]`, `metadata?`, `always?`, `tool?`. See `Permission` module
(`packages/opencode/src/permission/index.ts`).

`Question.Request` (`packages/opencode/src/question/index.ts:56-64`):

```ts
{
  id:        QuestionID,                   // "que_..."
  sessionID: SessionID,
  questions: Array<{
    question:  string,
    header:    string,
    options:   Array<{ label, description }>,
    multiple?: boolean,
    custom?:   boolean,
  }>,
  tool?: { messageID, callID }
}
```

Reply: `{ answers: string[][] }` — each outer element is the user's answer
to one question, as an array of selected labels (so `multiple=true` is
encoded by repeating labels).

---

## 5. Error model

All non-2xx responses follow Effect's HttpApi error envelope. Relevant
classes from `routes/instance/httpapi/errors.ts:1-193`:

| Class | HTTP status | Shape |
|-------|-------------|-------|
| `InvalidRequestError` | 400 | `{ message, kind?, field? }` |
| `UnauthorizedError` | 401 | `{ message }` |
| `ForbiddenError` | 403 | `{ message }` |
| `ConflictError` | 409 | `{ message, resource? }` |
| `UpstreamError` | 502 | `{ message, service?, status? }` |
| `ServiceUnavailableError` | 503 | `{ message, service? }` |
| `TimeoutError` | 504 | `{ message, operation? }` |
| `UnknownError` | 500 | `{ message, ref? }` |
| `ProviderNotFoundError` | 404 | `{ providerID, message }` |
| `ModelNotFoundError` | 404 | `{ providerID, modelID, suggestions[], message }` |
| `SessionNotFoundError` | 404 | `{ sessionID, message }` |
| `MessageNotFoundError` | 404 | `{ sessionID, messageID, message }` |
| `SessionBusyError` | 409 | `{ sessionID, message }` |
| `QuestionNotFoundError` | 404 | `{ requestID, message }` |
| `PermissionNotFoundError` | 404 | `{ requestID, message }` |
| `McpServerNotFoundError` | 404 | `{ name, message }` |
| `PtyNotFoundError` / `PtyForbiddenError` | 404 / 403 | |
| `ProjectNotFoundError` | 404 | `{ projectID, message }` |
| `ApiNotFoundError` | 404 | `{ name:"NotFoundError", data:{ message } }` |

When the upstream SSE closes, the adapter must treat the underlying
`EventV2.Service` shutdown as the end of stream and reconnect with
exponential backoff (the existing `internal/opencode/event_stream.go`
already implements this).

---

## 6. Status matrix — Pocket adapter vs pinned contract

> Status legend:
> * `implemented` — calls the pinned route, parses the pinned schema.
> * `source-inspected` — reads the same field names but does not call the
>   pinned route or does not parse the pinned schema.
> * `contract-tested` — covered by a real httptest harness in
>   `backend/internal/adapter/opencode_http_contract_test.go` behind the
>   `contract_opencode` build tag.
> * `drift-from-pinned` — calls a route or sends a body that the pinned
>   upstream rejects. Must be fixed before bumping the pinned commit.

### 6.1 Go HTTP adapter (`backend/internal/adapter/opencode_http.go`)

| Function | Pinned route | Status | Evidence in code |
|----------|--------------|--------|------------------|
| `HealthCheck` | `GET /global/health` | **implemented** | `opencode_http.go` `// status:` annotation TBD |
| `ListSessions` | `GET /session` | **implemented** | |
| `GetSessionDetail` | `GET /session/:sessionID` | **implemented** | |
| `CreateSession` | `POST /session` | **implemented** | |
| `DeleteSession` | `DELETE /session/:sessionID` | **implemented** | |
| `GetSessionMessages` | `GET /session/:sessionID/message` | **implemented** | |
| `GetSessionContext` | `GET /session/:sessionID/context` | **drift-from-pinned** — no such route in upstream; candidate removal |
| `InterruptSession` | `POST /session/:sessionID/abort` | **drift-from-pinned** — path is `abort`, not `interrupt` |
| `CompactSession` | `POST /session/:sessionID/compact` | **drift-from-pinned** — upstream is `summarize`, not `compact` |
| `WaitForSessionIdle` | `POST /session/:sessionID/wait` | **drift-from-pinned** — no such route in upstream |
| `GetPermissionRequests` | `GET /session/:sessionID/permission` | **drift-from-pinned** — upstream does not expose a per-session permission list; the global route is `/permission` |
| `GetAllPendingPermissionRequests` | `GET /permission` | **implemented** | |
| `ReplyPermission` | `POST /permission/:requestID/reply` | **implemented** | |
| `GetQuestionRequests` | `GET /session/:sessionID/question` | **drift-from-pinned** — global route is `/question` |
| `GetAllPendingQuestionRequests` | `GET /question` | **implemented** | |
| `ReplyQuestion` | `POST /question/:requestID/reply` | **implemented** | |
| `RejectQuestion` | `POST /question/:requestID/reject` | **implemented** | |
| `SendPrompt` | `POST /session/:sessionID/message` | **implemented** | |
| `SubscribeEvents` | `GET /event` | **implemented** | |

### 6.2 OpenCode Pocket Plugin (`opencode-plugin/src/index.ts`)

| Command | Pinned route | Status |
|---------|--------------|--------|
| `GET /session` (pollSessions) | matches | **source-inspected** — accepts bare array or `{ sessions }` / `{ data }`. Upstream returns bare `Session.Info[]` (`groups/session.ts:113`). |
| `POST /session` (createSession) | matches | **drift-from-pinned** — body uses legacy `{ id, agent, model, location }` shape; must use `Session.CreateInput` exactly |
| `POST /session/{id}/prompt` (sendPrompt) | WRONG | **drift-from-pinned** — sends `{ id, prompt: { text }, delivery }`. Upstream requires `{ parts: [...] }` and has no `delivery` field. |
| `POST /session/{id}/interrupt` (stopSession) | WRONG | **drift-from-pinned** — path is `interrupt`, upstream uses `abort` |
| `GET /api/health` (getOpenCodeVersion) | WRONG | **drift-from-pinned** — upstream exposes `GET /global/health`, not `/api/health` |

### 6.3 OpenCode Instance Manager (`opencode-manager/main.go`)

| Call | Pinned route | Status |
|------|--------------|--------|
| `GET /api/health` (waitForOpenCode, Check) | WRONG | **drift-from-pinned** — must be `/global/health` |
| `POST /session` (createOpenCodeSession) | matches | **source-inspected** — body uses `{ agent, model, location }`. Compatible with `Session.CreateInput`. |
| `POST /session/{id}/prompt` (sendOpenCodePrompt) | WRONG | **drift-from-pinned** — sends `{ id, prompt: { text }, delivery }`. Upstream requires `{ parts: [...] }`. |

---

## 7. Required follow-ups (drift-fix backlog)

These are the explicit contracts that must hold after this subtask lands:

1. **Adapter** — `InterruptSession` POST path → `/session/:sessionID/abort`.
2. **Adapter** — `CompactSession` → either remove or rename to `Summarize`
   with `SummarizePayload` body (`{ providerID, modelID, auto? }`).
3. **Adapter** — `GetSessionContext`, `WaitForSessionIdle` — remove or mark
   explicitly unsupported (no upstream route).
4. **Adapter** — `GetPermissionRequests` → use the global
   `GET /permission` and filter by `sessionID` in-process.
5. **Adapter** — `GetQuestionRequests` → same treatment with `GET /question`.
6. **Plugin** — switch `createSession` body to `Session.CreateInput`
   (drop `id?` client-supplied; let upstream assign unless explicitly
   providing a deterministic slug).
7. **Plugin** — switch `sendPrompt` body to `{ parts: [...] }` (drop
   `delivery`; drop legacy `prompt.text`).
8. **Plugin** — switch `stopSession` path to `/session/{id}/abort`.
9. **Plugin** — switch version probe to `GET /global/health`.
10. **Manager** — same fixes as plugin for `/global/health` and `/prompt`.
11. **All callers** — must NOT delete the `delivery` field semantics
    silently; if the upstream contract truly has no `delivery`, it must
    be dropped with a code comment and a test.
