# ENEX Markdown synchronization

New ENEX imports use the notes v2 projection with `format: "markdown"`.
The complete Markdown, ordered tags, original creation time and known Evernote
modification time travel as text. The import identifier supports local duplicate
detection after a remote copy. Attachment bytes, source author metadata and local
blob records remain on their device; remote edits and tombstones preserve them.

Remote Markdown materializes as an `enex_import` asset and appears in ordinary
notes. PKM links redirect it to the Markdown notes renderer; the HTML editor and
its write API cannot normalize Markdown into HTML. Portal title/content edits
preserve the format discriminator. An incompatible format change rejects the
page without advancing its cursor or changing the original.

The import captures account, workspace and reset epoch before any await. Blob
reads and writes follow the original owner lease. An interrupted import can be
repeated to attach missing resources to its already managed parent without
overwriting later body edits. Historical unclaimed assets are never assigned to
the active user. Unknown source modification times, excessive payloads and old
epochs remain blocked with the full local content retained. An explicit later
edit creates a new operation; old immutable operations are not silently rebased.

Notes and generic records share the same bounded UTF-8 operation ordering.
Same-time later local intentions retain their real timestamp. Source mutations,
projection and outbox publish under a transaction comparison against the version
observed before source reads. A competing winner rolls the edit back explicitly.

File-backed notes stage unique revision paths and publish child rows, pointers
and their operation together. Failure cannot replace the previously referenced
body or attachments. New media notes first retain their complete owned text as
an inline blocked draft; final publication compares against that exact initial
version. A later edit cannot be overwritten by delayed attachment completion.
Unreferenced native revision files can remain after failed writes; these do not
replace live originals and require later storage reclamation. Device filesystem
acceptance was not performed in this local code validation.

Validation uses real SQLite/LocalDB regressions, production PKM/notes rendering
in Chromium, browser-use interactions, type checking and a production build.
The browser harness isolates platform, authentication and search boundaries;
its note SQL, projection, import and rendered components are production code.
This is not deployed cross-service or Android/iOS end-to-end evidence.
