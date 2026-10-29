# Portal notes client (R6 slice)

This slice connects ordinary local notes, ENEX Markdown and PKM HTML notes to the portal v2
protocol. `main.ts` starts the runtime only after the policy runtime starts;
the runtime also waits for LocalDB readiness and a verified owner.

Saving a managed note atomically commits its source row, owner projection and
immutable outbox operation. Retries retain the original timestamp, payload and
operation ID and the verified reset epoch captured before the first business-operation await. Pulls atomically apply the portable projection and cursor, using
the original RFC3339 timestamp (including submillisecond precision), then the
greater operation ID. Remote changes preserve audio and attachment files.
The local encryption preference also protects stored outbox/projection bodies.

Only a pocketd workspace binding for the captured user/workspace is attached
to requests. Owner and policy leases guard network requests and SQLite writes.
Unknown historical rows are never assigned or uploaded. Known owner filters
run in SQL before pagination, same-title lookups and vector top-K selection.

The settings notice reports pending failures. Oversized content is retained
without truncation; clock skew and rejected operations remain pending until
the user corrects and saves the note. A fresh cloud-enable intent rekeys a
server `skipped_local` operation while retaining its original content/time.

Scope limits:

- RSS, mail metadata and meetings have separate adapters in their feature directories.
- New ENEX Markdown imports use an explicit format discriminator and stay in the
  Markdown editor. Unknown source times and oversized payloads remain blocked;
  attachment bytes stay local. See `docs/portal-enex-sync.md` for the boundaries.
- Native device/background-process survival and a deployed pocketd↔RedClaw
  environment were not exercised in this slice. The service binding is covered
  by separate backend tests; browser fixtures isolate account/data boundaries.
- Portal workspace-aware CRUD is a separate server/UI implementation. Local code
  validation does not establish the complete deployed four-module R6 result.
- Trusted v2 purge now has client wiring for all four portal module entries.
  Client API fixtures isolate network responses and backend PG/handler tests run
  separately; no deployed admin↔DAL↔pocketd end-to-end environment was available.
  This is not a claim of completed deployed purge acceptance.

Validation:

```sh
npm run typecheck
node --test src/native/portal-sync/__tests__/*.test.* src/features/notes/__tests__/notes-operation-leases.test.mjs
MOBILE_ALLOW_EMPTY_API_BASE=1 npm run build
node src/native/portal-sync/__tests__/notice-browser.mjs
node src/native/portal-sync/__tests__/views-browser.mjs
```

The two browser fixtures were exercised with `browser-use` in isolated sessions.
The actual settings notice showed the error, accepted retry, disabled retry
while running and cleared after resolution. The actual NoteListView and
PkmTodayView were mounted inside KeepAlive: cached account/workspace switches
removed old rows, remote updates/deletes refreshed active and restored lists,
and PKM retained its search query after a remote update. No global recorder or
unrelated route was destroyed.


Reset safety:

- `epoch` is part of each immutable operation. Unknown historical queue rows
  migrate with NULL epochs and stay blocked; a later pull never assigns its
  current epoch to those old operations.
- Trusted changes persist a monotonically increasing epoch and reset the cursor
  on change. Old-epoch operations remain stored and cannot upload or be cleared
  by a late acknowledgement. Source text, media references and files remain.
- A purge intent stores its canonical modules and stable `reset_id` before the
  mutation request. Lost responses reuse that id after restart. An uncertain
  intent blocks uploads until the trusted result is confirmed.
- The public purge API captures the original owner/session/DB/policy lease before
  dynamic import or any other await. All four module entry points use the
  binding-bearing v2 purge endpoint. Policy or identity changes abort completion.
- Failure, reset-blocked old operations and first-device initialization have
  separate visible notices. Clicking an already selected Local-only option can
  reopen the purge confirmation to retry an uncertain reset.

Additional validation commands:

```sh
node --test src/api/__tests__/purge-policy-wire.test.mjs
node src/native/portal-sync/__tests__/purge-browser.mjs
```
