# Newly created RSS subscription metadata

The RSS client now explicitly enrolls newly created subscriptions and newly
inserted starter subscriptions in a portable metadata journal. A preexisting
subscription is never enrolled by a list, migration, repeated starter import or
ordinary edit. Remote portal sources remain metadata and never enable fetching.

The new source and its first journal event commit together. Source edits journal
only changed portable fields; fetch scheduling, enabled flags, errors, read/star
state and other private settings do not rewrite portal metadata. Feed updates
carry the publisher's original `updated_at`; an absent timestamp is retained as
an error, never replaced by the download time. Automatic feed updates also stop
when the portable version has an independent user edit. A native title-only
edit preserves unrelated portable fields, and a newer explicit source edit can
restore a tombstone using its complete valid source snapshot.

Journal rows contain only source title/URL/category or item title/source/URL/
summary. They exclude content, credentials, filters, drafts and attachments.
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

## Rollout and rollback

This is additive schema owned by the RSS store startup migration: two source
provenance columns, a durable journal, and source/item triggers. It does not
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

RSS subscription creation still requires an available pocketd. This change does
not implement a device-local fetcher or offline executable subscription creation.
Read/star state is not in the portal whitelist. Historical subscriptions and
feeds without original update times remain outside automatic export. Production
cross-service and Android/iOS acceptance have not been performed.
