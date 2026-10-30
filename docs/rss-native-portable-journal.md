# Newly created RSS subscription metadata

The RSS client now explicitly enrolls newly created subscriptions and newly
inserted starter subscriptions in a portable metadata journal. A preexisting
subscription is never enrolled by a list, migration, repeated starter import or
ordinary edit. Remote portal sources remain metadata and never enable fetching.

The new source and its first journal event commit together. Source edits journal
only changed portable fields; fetch scheduling, enabled flags, errors and other
private settings do not rewrite portal metadata. Read/star operations use an
independent `item-state` identity and never rewrite source/item metadata. Feed updates
carry the publisher's original `updated_at`; an absent timestamp is retained as
an error, never replaced by the download time. Automatic feed updates also stop
when the portable version has an independent user edit. A native title-only
edit preserves unrelated portable fields, and a newer explicit source edit can
restore a tombstone using its complete valid source snapshot.

Journal rows contain source title/URL/category, item title/source/URL/summary,
or a status projection `{item_ext_id,status}`. They exclude content, credentials,
filters, drafts and attachments.
Automatic import blocks URLs with userinfo, query or fragment rather than trying
to distinguish private authentication parameters or silently stripping them.
The original subscription and event remain available to their owner.

`GET /api/rss/portable/changes` returns a bounded, forward ordered page and the
authenticated user/workspace. The workspace must be present in verified claims
and the authority's current membership must be active. Missing authority fails
closed. Opt-in creation/import and mutations or refreshes of managed sources use
the same check; client-supplied owner fields and a `default` fallback provide no
membership authorization.

The journal stores the source's original captured portal epoch on each event.
An explicit later native metadata edit or deletion can use a newly captured
epoch, but no existing journal entry is changed. The client commits each event,
its immutable portable operation and its source checkpoint in one SQLite
transaction. Unknown time, epoch, conflicting portable edits or unreviewable
URLs remain in an owner-isolated local inbox with a visible blocked count.
Clear-cloud resets fence late import transactions and old events cannot acquire
a new epoch. A response or database failure leaves the last committed cursor
available for retry.

New read/star operations on explicitly managed articles first commit an
encrypted owner-scoped SQLite state projection and portable outbox atomically.
The four existing exclusive statuses remain `unread`, `read`, `starred` and
`archived`; removing a star means `read`. The state kind is `item-state` and its
`ext_id` equals the target article's `ext_id` exactly. Native articles use
`pocketd-item:<id>`; manually copied portal articles remain independent.

`POST /api/rss/portable/item-states` applies up to 100 original status versions
under the verified active membership and RSS scope lock. State, immutable
idempotency receipts and journal entries commit in one PostgreSQL transaction.
The winner uses epoch, original action timestamp, then UTF-8 operation ID order.
Retries retain their original ID, timestamp and epoch. The same operation ID
with a changed body is rejected. Older unversioned read/star and bulk endpoints
reject managed articles; historical unmanaged articles retain local native
behavior and never become shared by reading or starring them.
Equivalent RFC3339 zone/fraction spellings of the same exact instant are
idempotent; the first receipt and journal spelling remain unchanged. A distinct
nanosecond or another changed field still conflicts. Precision beyond nine
fractional digits and out-of-range timezone offsets are rejected rather than
silently normalized or truncated by the time parser.

Native journal echoes also retain a portal state's original title and timestamp
spelling (including offsets and trailing zeros), so importing that same version
on another device cannot change an immutable operation body. Local status edits
use an empty state title and carry no article title or full body. Feed lists
merge owner-scoped state overrides before status filtering and pagination, and
resolve supplemental IDs against existing native rows before displaying them.

Remote states update an already existing, explicitly managed native article's
status only. An unknown, deleted or unmanaged target remains a portable
projection and cannot create a source, enable a fetcher, restore a deleted
article or alter body/title/feed timestamps. A status tombstone removes the
portable override and does not manufacture an unread user action. Clear-cloud
fences old state reconciliation; original stale records stay local. Versions
with more than six fractional timestamp digits remain blocked with their exact
source timestamp, because the portal accepts microsecond precision or coarser.

## Rollout and rollback

This is additive schema owned by the RSS store startup migration: two source
provenance columns, a durable journal, status state/receipt tables and source/item triggers. It does not
reassign existing rows. Deploy compatible pocketd writers before clients start
opting in. All portable writers take the scope lock before row locks; scheduler
bookkeeping exits the trigger before requesting that lock. Do not mix old
writers that obtain these locks in the opposite order. A coordinated stop of RSS
writes/fetchers is required before switching versions; this is not an assertion
of rolling-upgrade compatibility.

Application rollback must keep the journal, original epochs and provenance
columns. Stop managed RSS writes and client ingestion before using an older
pocketd writer. Do not drop the triggers while writers continue: that would
create an unrecorded gap, and scanning old rows afterward would not recover
authentic modification times. No deployment or production rollback was executed
in this change.

For a read-only rollback, stop RSS writers/fetchers and client ingestion first,
then atomically disable only the three journal triggers in the RSS schema:
`psql "$POCKET_DATABASE_URL" -v ON_ERROR_STOP=1 -c 'BEGIN; ALTER TABLE rss_sources DISABLE TRIGGER rss_portable_source_write; ALTER TABLE rss_sources DISABLE TRIGGER rss_portable_source_delete; ALTER TABLE rss_items DISABLE TRIGGER rss_portable_item_write; COMMIT;'`.
Keep managed writes stopped while the triggers are disabled. Restore the
compatible application and enable those same triggers in one transaction before
resuming writes or ingestion. This retains every journal row, epoch and owner;
it does not promise recovery of writes made with journaling disabled.

## Validation and remaining boundaries

Real PostgreSQL tests cover opt-in versus historical sources, publisher time,
unknown time, scope isolation, journal rollback, source/child tombstones,
concurrent committed cursors, and scheduler/source lock order. Actual HTTP route
tests cover current/expired/revoked members, absent workspace or authority,
privacy, invalid epochs and bodyless/chunked deletion. Real SQLite tests cover
immutable import/checkpoints, user edits, reset/policy races, encryption and
tombstone restoration. A browser-use fixture mounts the production add-source
and portable panel and exercises new-source import and delayed discovery across
an account change.

Status regressions additionally cover real PostgreSQL idempotency, exact action
time, batch rollback, legacy bypass rejection, epoch ordering, missing/deleted
targets and HTTP membership checks. SQLite tests cover offline local edits,
unchanged request retries, A-B-A ownership, encryption, remote status application,
malformed targets, reset fences, losing concurrent batches and batches exceeding
100 pending projections. Browser-use mounts the production article component
and verifies offline starring, owner changes, immutable retry and remote stars
while preserving the loaded article body.
The production messages hub uses the same object-based offline path for opening
an RSS row and marking visible RSS rows read in one batch. Its owner lifecycle
clears old rows, fences delayed loads and queues status events received during
other source loads. Notification cache entries require exact user/workspace
provenance; a missing owner cache loads a fresh bounded notification page from
zero, without borrowing another owner's incremental cursor.

RSS subscription creation still requires an available pocketd. This change does
not implement a device-local fetcher or offline executable subscription creation.
The native feed list/detail still require pocketd for initial article loading;
an already loaded managed article's status can be edited offline. Historical
subscriptions and feeds without original update times remain outside automatic export. Production
cross-service and Android/iOS acceptance have not been performed.
