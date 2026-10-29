# Workspace authority delegation

RedClaw can discover a user's openpocket workspaces and obtain a fresh membership
binding through these dedicated routes:

- `GET /internal/workspace-authority/workspaces`
- `GET /internal/workspace-authority/workspaces/{id}/binding`

The service requires `IDENTITY_SHARED_SECRET`, a configured
`POCKET_REDCLAW_TENANT_ID`, and an initialized PostgreSQL identity store. The
shared secret stays in the existing service trust domain. The JWT signing key is
HMAC-SHA256 of that secret with `openpocket-workspace-delegation:v1` as message;
this isolates request credentials from ordinary identity tokens and membership
assertions. Requests use issuer `redclaw`, exclusive audience
`pocket-workspace-authority`, actor `redclaw.admin`, scope
`workspace-membership:read`, a UUID `jti`, and explicit `iat`, `nbf`, `exp`. The
signed `sub` and `tenant_id` come from RedClaw's authenticated principal. Signed
`method` and decoded `path` must match the incoming request; query parameters are
not accepted. Validity is at most 30 seconds and never exceeds the original
RedClaw session expiry.

After verification, PostgreSQL atomically consumes `(issuer,jti)` in
`workspace_delegation_nonces`, so replay is rejected across process restarts and
replicas sharing the store. Expired rows are removed on subsequent verified
requests. A failed or uncertain request must use a newly signed credential.
The store migration adds only this nonce table and expiry index. Existing
workspaces and members are unchanged. Discovery excludes expired membership;
binding issuance queries current membership and rejects revocation or expiry.

This credential is never accepted as an ordinary pocket-api bearer. The API's
existing issuer/audience validation remains unchanged. A request does not
declare itself a workspace member; only the authority can sign that result.
Responses use `Cache-Control: no-store`, and neither the bridge nor authority
caches request credentials. Bindings expire no later than the member's own
expiry. Revoking membership prevents new bindings, while previously issued
bindings retain their bounded validity window (the existing default is 15
minutes, capped by membership expiry); this is not instantaneous revocation of
those bindings. The one-use delegation request itself lasts at most 30 seconds.

To disable the bridge, unset RedClaw's `PORTAL_WORKSPACE_AUTHORITY_URL`. To roll
back this code, revert its commit; retain the harmless nonce table rather than
dropping it during application rollback. No deployment or live identity mapping
is performed by adding these endpoints. Portal v2 browsing/mutation and cloud
reset epochs are separate integration work.
