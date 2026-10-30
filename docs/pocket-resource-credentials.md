# Restricted pocket resource credentials

RedClaw login tokens remain invalid as ordinary `pocket-api` bearer tokens.
The additional resource protocol permits selected legacy resources without
weakening either ordinary verifier or sharing an administrator's role.

The client requests one scope from RedClaw's authenticated
`POST /api/v1/portal/workspaces/:workspace/resource-token`. RedClaw validates the
persisted parent session and current module policy, then signs a one-use
30-second request for this server's
`POST /internal/resource-authority/workspaces/:workspace/token`. The authority
checks the configured tenant, consumes the nonce in PostgreSQL and rechecks
active workspace membership. Its response contains `token`, `user_id`,
`tenant_id`, `workspace_id`, `scopes`, `issued_at` and `expires_at`.

Resource JWTs have their own local-key derivation, issuer `pocket.resource` and
exclusive audience `pocket-resource-api`. They expire within 30 seconds and no
later than the parent session or member expiry. They cannot be used as login,
refresh, workspace delegation, portal or ordinary pocket credentials. Member
roles come from the authority's current member record.

## Per-request enforcement

Every resource request checks the exact method/path scope, current member, and
the persisted parent session plus current cloud policy through the dedicated
RedClaw session introspection protocol. Introspection signatures have a separate
key derivation/audience and a five-second lifetime. An introspection proof may
be replayed within that window, but it is read-only and rechecks the actual
session and policy on every use; it never replays a cached authorization result.
The token exchange itself remains single-use across replicas.

`POCKET_RESOURCE_SESSION_AUTHORITY_URL` is a fixed RedClaw origin: HTTPS, or
numeric loopback HTTP for local tests. Userinfo, paths, queries, fragments,
redirects and environment proxies are rejected. The request times out in two
seconds; absent configuration or an unavailable verifier fails closed with 503.
Configure the same `IDENTITY_SHARED_SECRET` trust and `POCKET_REDCLAW_TENANT_ID`
as the existing workspace delegation protocol. A local JWT signer and the
identity PostgreSQL store must be available. No `default` workspace fallback is
used by this protocol.

Scopes are intentionally limited:

- `rss`: the explicitly enumerated RSS source/item/filter/digest and portable
  journal methods in `auth.ResourceScope`; no arbitrary prefix delegation.
- `email-metadata`: GET account lists, envelope lists, daily summaries and
  folder metadata. Account responses exclude IMAP/SMTP configuration and rules.
  Sending, credentials, OAuth, mailbox mutation, bodies and attachments are
  denied, as are auth, admin and unrelated APIs.
- `embedding`: only POST `/api/embed`, governed by the notes cloud policy.
  Generic LLM and streaming APIs are outside this scope.

Allowed calls have a deadline at credential expiry, cancellation monitoring and
an eight-MiB bounded nonstreaming response buffer. Authorization is checked again
before any response is published. The resource protocol does not promise that
long operations can outlive its 30-second admission. No streaming route is
allowed. An in-flight expiry/revocation or oversized result returns 409
`resource_authorization_changed`; writes may already have committed, so clients
must not automatically replay those ambiguous operations. Data already sent to
an external service cannot be recalled.

## Failure and rollback

An invalid resource token is 401 and may be exchanged once more using the
unchanged parent session. Missing scope or membership and local policy are 403;
introspection failure is 503. RedClaw exchanges identify confirmed parent
revocation with `parent_session_invalid`. An upstream authority signature or
configuration failure must not be interpreted as revocation of a valid login.

Rollback disables resource access while retaining ordinary authentication and
all stored data: `unset POCKET_RESOURCE_SESSION_AUTHORITY_URL`. Coordinate
disabling the client capability and RedClaw exchange before removing server
code. Workspace nonce/revocation history must remain intact.

Validation covers domain separation, scoped routes, PostgreSQL membership and
nonce replay, parent session checks, policy changes and response fencing.
Production configuration and native devices require separate acceptance.
