# RSS portable metadata client

The RSS screen now contains a separate source/article metadata read model. New
records, edits and tombstones are saved locally with the verified owner and an
immutable operation ID, modification time and reset epoch. It uses the shared
record sync engine for signed-workspace v2 push, per-item acknowledgements,
incremental changes, LWW and reset protection. Sources and articles may have the
same external ID because their kinds are part of the identity.

The read model deliberately does not enable a fetched subscription. Portal source
URLs are displayed as text and are not fetched or opened automatically. Existing
pocketd subscriptions and articles are not scanned into the outbox, assigned to
the current owner, or treated as a local change feed. A user can explicitly save
a loaded RSS article's title, source label, URL and summary as a portable copy.
That new intent has its own modification time; it is not presented as the
article's source modification time. Repeating this action updates the same
portable copy. Full article content and unrelated fetch metadata are excluded.

Only source `url/category` and article `source/summary/url` fields are accepted.
Unknown fields, nested data, non-HTTP(S) URLs and URL user/password credentials
are rejected. Oversized records remain local and blocked through the shared
outbox; content is never truncated to report success. Local encryption preferences
apply to the shared projection and outbox.

Account/workspace activation clears cached rows, drafts and pending UI results.
Explicit copy, edit and deletion capture the original owner before asynchronous
work or confirmation. Unknown epochs and pre-reset operations remain blocked;
learning a new epoch never upgrades an old operation. Cloud reset does not delete
local metadata or downloaded article bodies. The panel explains initialization,
blocked operations, retained originals and retry behavior in all nine locales.

Integration with the shared module layer requires `startRssPortalSync()` from
`features/rss/rss-sync-runtime.ts` at startup and the shared record notice for
`module="rss"` in storage settings. These hooks are integrated by the parent
alongside the shared record layer. The runtime must start after auth, policy and
LocalDB activation runtimes, as the note runtime does.

Validation uses real LocalDB leases and SQLite for owner isolation, stable
identities, immutable retry, remote tombstones, reset blocking and explicit
copy privacy. The browser-use fixture mounts the production panel with actual
record SQL and leases; account, platform and network boundaries are isolated.
It exercises offline creation, cached account/workspace changes, remote updates
and deletions, draft clearing and retry. This is not deployed pocketd/RedClaw or
Android/iOS acceptance.

Full automatic synchronization of background-fetched feeds remains separate.
It needs versioned source/item DTOs, mutation times for read/star changes,
source/item tombstones and a reliable local ingestion contract. This slice does
not claim those capabilities or complete four-module R6 acceptance.
