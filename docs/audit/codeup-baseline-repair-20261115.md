# Codeup 5805020b baseline repair — checkpoint after SSE follow-up

Scope: only this publication clone, branch `codex/maestro-codeup-audit-20261115`,
HEAD `5805020b0a893efb137bfbeaa875114868a4200e`. No commit, push, device operation,
or change to the original source tree. The user's seven pre-existing repair files
retain their initial SHA-256 values. The main agent owns notes-crud.yaml,
.maestro-flows.json, pkm-test-fixture.mjs and their fixture regression; none was edited here.

## Repair blocks and evidence

| Block | Necessary diff | Actual validation |
|---|---|---|
| OIDC response budget | server_oidc.go: preserve the existing 10-second timeout through outbound.NewClient; consolidate two unbounded decoders into one 1-MiB capped reader, reject an oversized valid JSON prefix | TestOIDCResponseBodyBudget (valid, malformed, oversized); outbound/bounded-read gates pass; full Go suite passes |
| Go baseline hygiene | logging fixture receives the existing per-line synthetic-value annotation; memora hash uses rune slicing; length registry retains the same 16-character/O-class rule under the new expression | full repohygiene passes; full server and Go suites pass |
| Audio/subtitle | import the canonical blobToBase64; retain the store assertions while adapting Vitest/extensionless imports to the repository's Node runner and isolated storage injection; explicit object style binding, real accent tokens, existing font tokens | latest targeted run: 82 tests, 82 pass, including the two new overlay layout tests |
| ACC board | normalize caught exceptions with useApiError, retain diagnostics; keyboard inset and scrollable cards; z-dialog/font-mono/font-size tokens; descriptive address placeholder | 45 existing error/style/audit checks pass; Chromium verifies both overlay actions above the simulated keyboard |
| Email browser fixture | add the missing @ alias and decryptString export; wait for the initial authenticated fixture owner before seeding | email-owner-routes-browser.test.mjs passes, retaining identity-switch, private-row clearing and unsaved-draft assertions |
| ACC SSE lifecycle follow-up | move SSE and fallback/mock polling into a process-owned runtime; scope by auth generation, workspace and captured ACC address/credential; LogStream only subscribes/detaches; accepted user cancellation closes the captured run/owner | 16 real loopback HTTP behavior tests and two real Chromium component tests pass; existing unmount gate unchanged and green; latest targeted group 32/32; typecheck and full frontend suite pass |

The original baseline logs show 12 frontend files with failure events. The first repaired
full frontend run executed **433 files** and reported **only**
`src/native/__tests__/unmount-must-not-abort-streams.test.mjs` as failing. Its two
failure events described one component violation; they were not two independent
product defects. The user then authorized that lifecycle follow-up. The latest
full run exits **0**, with **435/435** files actually executed and **6079** execution
events (including suites/subtests, not a case count), and no failure events.
That run finished before the later parallel Board/subtitle fixes were authorized;
it is a pre-fix snapshot, not their unified publication validation. This agent will
not start another full run; the main agent owns it after both agents freeze.

## Ledger reconciliation — no new unbounded exception

* Promise census: initially 101 -> 103, now **102**. TaskBoardView.registerOfflineSw's
  optional service-worker registration retains `console.warn` evidence and its
  individual **A** entry. The former LogStream.startMock handler is actually gone:
  runtime.poll handles mock/live failures with a stateful try/catch, diagnostics,
  bounded retries and an unavailable state. Its old A entry was removed, not
  reclassified or exempted. The exact census and file/category equality gate passes.
* Read census: observed 279 total / 276 bounded / **the same three** existing
  unbounded entries. Counts by form: 57 readall, 151 decode, 3 copy, 16 sizedall,
  17 sizedsnp, 35 sizedlim; sources: 129 response, 119 request, 8 memory, 23 other.
  The six sites relative to the pinned 273 are memora.do's bounded snippet and
  JSON response, two meeting-translation request decoders, one portal-resource
  token request decoder, and the new bounded decodeOIDCResponse reader. Comments
  next to the pinned assertions enumerate these changes. Positive-cap and exact
  unbounded-ledger checks remain active and pass.
* memora length ledger: `len(hash) > 16` -> `len(runes) > 16`; threshold 16 and
  classification O (internal derived hash, not a rejected client request) remain.
* CSS injection, raw-error, keyboard, z-index and stream-unmount gates were not
  relaxed or skipped. In particular, no new entry was added to the unmount ALLOW map.

## SSE ownership and bounds — frozen follow-up

* The runtime follows aiStreamRuntime's process-owned subscribe/replay pattern,
  with a separate ACC protocol implementation. acc-log-session observes auth in
  a detached Vue effect scope; same-tab ACC setters notify it, and storage events
  cover other tabs. Revocation erases replay/visible rows and closes old IO.
* Maximum eight registered runs, 500 retained events and 256 Ki UTF-16 replay
  characters per run; incomplete SSE frames cap at 64 Ki characters. Poll bodies
  cap at 1 MiB. Sequence cursors deduplicate historical overlap, and retained event
  IDs deduplicate repeats with a newer sequence. Cross-run events are rejected.
* No subscribers: fixed 30-second grace, unaffected by heartbeat traffic, then
  connection/timer/replay/cursor eviction. SSE silence: 45 seconds before fallback.
  Handshake: 15 seconds; polling has a nonrenewable 15-second whole-response deadline
  and stops after three failures. A terminal event or accepted explicit cancellation
  closes IO while retaining bounded replay until the last subscriber's grace expires.
* The runtime owns both controller and reader: fetchWithTimeout disconnects its
  caller-signal listener after headers, so controller-only cancellation is insufficient.
  HTTP tests observe socket close, rather than asserting only a signal flag.
* Real Chromium tests mount the actual LogStream and TaskDetail components. They
  exercise unmount/remount, offscreen replay/dedup, workspace/account changes,
  offscreen credential replacement, server replacement and terminal replay. A
  cancel 404 keeps SSE alive; a successful fixture response closes the captured run.
* This is isolated protocol/component validation, not production ACC or device
  acceptance. The existing SOP-run cancel endpoint remains documented as unsupported;
  no production cancellation success is inferred from the successful-response fixture.
* Replay is process-local and bounded, not durable history. A remount after grace
  opens a fresh server replay. SSE dedup assumes ACC's documented run-local monotonic
  sequence order. Arbitrary same-tab localStorage mutations bypassing the ACC setters
  are checked before data/replay and by the one-second authority watchdog; supported
  setters invalidate immediately. OS suspension can delay JavaScript timers.

The initial eight source/test paths and SHA-256 values were saved in
`/tmp/openpocket-acc-sse-frozen-20261115.json` (historical, superseded below). The initial follow-up changed nine paths including
this checkpoint: accApi.ts, LogStream.vue, TaskDetailView.vue, acc-log-runtime.ts,
acc-log-session.ts, their two new tests, the promise census and this document.

## Final S1/S2 review fixes — frozen

Only five source/test paths changed in this final delta, plus this checkpoint:
TaskDetailView.vue, acc-log-runtime.ts, acc-log-session.ts, accApi.ts and
acc-log-browser.test.mjs. TaskBoardView.vue/liveSubtitle.ts and their Lagrange-owned
helpers/tests were not edited. No full suite, build, native/device operation,
commit or push was run in this final phase.

* S1: detail reads capture owner, run ID, view generation and actual request
  generation. Success, catch and finally all check admission. Scope changes and
  unmount revoke old results without aborting IO. Stable-owner poll ticks reuse a
  single flight and do not increment its generation, so slow reads can finish.
  Successful cancellation fences the pre-cancel read, waits for its flight, then
  refreshes; stale cancel success/error/finally cannot update a newer view or flag.
* Approval/handoff submitted and close callbacks are closures over the ticket
  captured when opening the modal, not lookups of the latest ticket. Both named
  completion handlers check that ticket before writing notes or loading. The old
  comment claiming SSE was not connected was corrected. These callback guards
  were checked by source review/typecheck; no extra modal HTTP matrix was added.
* Detail GET uses no-store. Before that change the real Chromium ABA fixture
  issued no fresh run-1 server request while an identical old request remained
  pending; after no-store the same assertion passed without changing its deadline.
  This supports browser in-flight/cache reuse as the cause. An optional synchronous
  admission callback also runs before the API's local registry side effect, so a
  fenced response cannot register stale private detail metadata.
* S2: runtime exposes a synchronous authority observer and the session ref follows
  it. Internal 401/403/watchdog revocation now reaches all consumers, not only log
  subscribers. Denied response headers revoke admission before body cleanup. The
  detail watcher immediately clears run/nodes, log view, notes, errors and modals;
  a null owner does not start another request with the same credential.
* Effective Chromium/HTTP RED: 12 tests, 3 pass / 9 fail, exit 1, 14.142 seconds.
  Failures include late detail success/error, stale cancellation state, ABA and
  SSE/poll 401/403 reactive clearing. The earlier missing-browser launch failure
  is environmental evidence only and is not counted as RED. The lockfile-matched
  headless browser was installed before the effective RED run.
* Final targeted GREEN: 42 tests / 42 pass / zero failures, exit 0, 5.454 seconds;
  includes 12 actual Chromium component/HTTP tests, 16 runtime HTTP tests and the
  unchanged unmount, raw-error and exact catch-ledger gates. Typecheck exits 0
  after the final source change. No ALLOW, test skip or gate relaxation was added.

Final artifacts (these supersede the previous freeze hashes):

* `/tmp/openpocket-acc-sse-fence-frozen-20261115.json` and matching `.sha256` cover
  all eight SSE source/test paths at their latest contents plus this checkpoint,
  including every new S1/S2 delta path.
* `/tmp/openpocket-acc-sse-fence-review.diff` is the incremental delta against
  the saved pre-fix contents, rather than a HEAD diff containing earlier repairs.
* RED: `/tmp/openpocket-acc-sse-fence-red-behavior.log`.
* GREEN: `/tmp/openpocket-acc-sse-fence-targeted-final.log` (exact command at top).
* Typecheck: `/tmp/openpocket-acc-sse-fence-typecheck-final.log`.
* ABA isolated GREEN: `/tmp/openpocket-acc-sse-fence-aba-green.log`.

Source/tests are frozen after those green results. The main agent owns unified
frontend tests/build, candidate APK/native acceptance, new-fix review and publishing.
The earlier 435-file green full run is historical and does not validate these final
fixes or Lagrange's later changes. Production ACC cancellation/approval/handoff
success is still not inferred from isolated protocol fixtures.

## Remaining checks outside the authorized SSE repair

An extra `node scripts/check-fetch-bounds.mjs` run exited **1** for
`src/stores/liveSubtitle.ts#<module>`: requestTranslation still uses raw fetch for
`/api/meetings/translate`, without a signal/deadline. At that run, its entire
requestTranslation block was byte-identical to Codeup580 (git show HEAD comparison);
this SSE agent did not change it. No exception/ALLOW was added and no fix was pulled
into this SSE-only scope. Full test:all does not run that standalone gate, so its
green result does not imply all publication gates are green.

Coordination update from the user: Lagrange found no new P1/P2 in the previous 21
repair files, and now owns TaskBoardView.vue/liveSubtitle.ts and their narrow
helpers/tests for three separately reproduced baseline P2 issues (old-authority
board cache after 401, stale subtitle language responses, shared debounce dropping
all but the last segment). This SSE agent will not edit those two files. Their
final tests/review and the extra fetch gate reconciliation belong to that work and
the main agent's unified validation, not this frozen SSE checkpoint.

## Validation artifacts

* Full Go: `/tmp/openpocket-codeup-repair-go-final.qr3rqC` — exit 0.
* Historical frontend: `/tmp/openpocket-codeup-repair-tests.LQHk2X` — exit 1 before
  the authorized SSE repair; retained as historical evidence.
* Latest full frontend: `/tmp/openpocket-acc-sse-test-all-final.log` — exit 0,
  435/435 files, zero failure events, elapsed 65.459 seconds.
* Initial SSE typecheck: `/tmp/openpocket-acc-sse-typecheck-final.log` — exit 0.
* Initial SSE targeted: `/tmp/openpocket-acc-sse-targeted-final-tests.log` — 32/32, exit 0;
  verbatim test-command section of `/tmp/openpocket-acc-sse-targeted-final.log`.
* Extra standalone fetch gate: `/tmp/openpocket-acc-sse-fetch-bounds.log` — exit 1,
  pre-existing subtitle request above; extracted from the same combined log.
* Native gate red: `/tmp/openpocket-acc-sse-red-unmount.log`. Buffer/deadline red:
  `/tmp/openpocket-acc-sse-red-bounds.log` (12 pass / 2 fail); subsequent
  `/tmp/openpocket-acc-sse-green-bounds.log` passes all 16 then-existing behavior tests.
  Two additional protocol boundary tests pass in the latest 32-test group/full run.
* Targeted subtitle/base64/error/overlay/CSS checks — 82 pass, zero failures.
* Targeted ACC error/keyboard/style/census checks — 45 pass, zero failures.
* `gofmt -l` for changed Go files and `git diff --check` — clean.

## Task Stop Summary
- status: partial
- goal: Repair actual Codeup baseline failures without weakening gates or changing protected work
- baseline: 5805020b0a893efb137bfbeaa875114868a4200e; codex/maestro-codeup-audit-20261115
- changed_files: 26
- in_scope_files: 26
- out_of_scope_files: 0
- unnecessary_changes: none
- accidental_changes: none
- verification: Final S1/S2 RED 3 passed and 9 failed; final targeted GREEN 42/42 passed; final typecheck passed; historical frontend full 435/435 passed before later deltas; no new full run by this agent
- unresolved: S1/S2 fixes frozen; main-agent unified validation/build/native acceptance and final review remain; no production API/device acceptance claim from local fixtures
- next_action: Main agent uses the latest S1/S2 SHA manifest and incremental diff for unified frontend/build/native validation and publication decisions; this agent stops source/test writes
