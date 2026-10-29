# Meeting client synchronization

The meeting adapter uses the shared record store, engine, epoch guard and
transport. New owned meetings commit their source row, portable projection and
immutable operation in one SQLite transaction. Title, attendee and minute edits
capture the original modification time and known reset epoch before asynchronous
work. Transcript, audio, location and recording-status updates do not enqueue a
portable version. Existing rows without a managed projection remain local; a
current login does not establish historical provenance.

The projection contains only `held_at`, `attendees` and `minutes`, plus the portal
title and version identity. Raw transcript, refined transcript, audio paths,
segments, linked-note ids and attachments are excluded. The old pocketd mirror
calls in meeting ingest have been replaced by queue draining, so a completed
refinement cannot bypass the version or reset protocol.

Remote application changes only the portable meeting fields. New remote meetings
are completed records with no audio. Remote deletion writes a local tombstone;
it does not call audio cleanup or remove segments, transcript or files. Local
deletion remains an explicit user action. Stable external ids are retained when
a remote meeting is edited locally.

The record tables include module and kind in their identity, so different kinds
can share a server external id safely. Each module has an independent cursor and
epoch. Parent changes and child tombstones can use `commitMany` for one atomic
transaction. Conditional source writes pass a guard that also protects projection
and outbox insertion. Queue bodies follow the user's local encryption setting.

Sequential local edits at the same millisecond extend the observed operation id
when needed to retain their order without changing the actual timestamp. The
comparison uses UTF-8 byte order, matching SQLite BINARY and the server's Go
string comparison. If a greater id cannot be produced within 200 bytes, or the
source clock is older than the managed version, the edit is explicitly rejected
and the existing metadata sheet keeps its draft. Before any source write, the
transaction asserts every managed projection is still the version observed
before encryption. A concurrent remote/local winner aborts the whole batch,
including parent/child source writes and outbox entries. Duplicate identities in
a batch are rejected. The assertion table remains empty on successful writes.

The settings notice exposes failed, blocked, uninitialized and unresolved-reset
states. Old or unknown-epoch operations are retained and never upgraded during a
pull. A new explicit local save can create a new intent using the already-known
current epoch. All requests use the captured account, workspace binding and
module policy; account or policy changes cancel the old operation.

Complete recordings now use IndexedDB version 2 with a separate owned store and
keys containing user, workspace and meeting id. Version 1 recordings stay in the
legacy store unclaimed. Recording finalization passes its original operation;
full-audio transcription and explicit deletion use the same owner handle.
Asynchronous reads cannot publish a prior account's Blob URL.

Local validation uses the actual LocalDB/jeep SQLite bridge and SQL.js, production
transport/auth/policy wrappers with isolated network responses, Chromium
IndexedDB, and actual MeetingListView/KeepAlive and notice components with
isolated account/data boundaries. It covers source/outbox rollback, privacy
fields, exact acknowledgements, offline retries, remote conflicts and tombstones,
reset races, same external id across kinds, account/workspace switches, legacy
audio preservation and stale IndexedDB opens. Browser-use clicks verified the
same ownership and view behaviors. Native device recovery and a deployed
pocketd-to-RedClaw environment were not exercised by this slice.
