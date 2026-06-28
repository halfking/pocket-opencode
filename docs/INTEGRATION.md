# OpenCode Pocket Integration Guide

This document describes how OpenCode Pocket integrates with externally managed OpenCode instances while keeping the Pocket backend as the single control-plane API for mobile clients.

## Integration Model

```text
[Mobile client]
      |
      v
[OpenCode Pocket frontend]
      |
      v
[OpenCode Pocket backend]
   |                    |
   v                    v
[Instance catalog]     [Task store]
      |
      v
[Managed OpenCode instances]
```

The integration contract is simple:
- Pocket maps a stable `instance_id` to a reachable `apiBaseURL`
- the backend persists tasks locally
- the backend calls compatible OpenCode APIs on behalf of the frontend

## Instance Sources

The current backend supports two practical sources of instance metadata:

1. Explicit catalog configuration through `POCKET_INSTANCE_CATALOG_JSON`
2. An optional external discovery endpoint through `POCKET_INSTANCE_DISCOVERY_*`

For controlled production environments, the explicit catalog is the most predictable option.

Example:

```bash
POCKET_INSTANCE_CATALOG_JSON='[
  {
    "id": "opencode-main",
    "displayName": "OpenCode Main",
    "apiBaseURL": "https://opencode-main.example.internal",
    "environment": "production"
  },
  {
    "id": "opencode-staging",
    "displayName": "OpenCode Staging",
    "apiBaseURL": "https://opencode-staging.example.internal",
    "environment": "staging"
  }
]'
```

## Backend Responsibilities

The Pocket backend is responsible for:
- loading instance metadata
- exposing `/api/instances` for the frontend
- mapping `instance_id` to `apiBaseURL`
- forwarding session and configuration requests to the correct OpenCode instance
- keeping task state in SQLite

## Remote Configuration Flow

When an instance exposes compatible configuration endpoints, Pocket can call:
- `GET /api/config/models`
- `PUT /api/config/models`
- `POST /api/config/reload`
- `POST /api/config/models/test`

Those requests stay server-to-server. Mobile clients only speak to Pocket.

## Environment Design

Recommended public configuration surface:
- `POCKET_HTTP_PORT`
- `POCKET_DB_PATH`
- `POCKET_INSTANCE_CATALOG_JSON`
- `POCKET_INSTANCE_DISCOVERY_BASE_URL`
- `POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN`
- `POCKET_OPENCODE_TIMEOUT_MS`
- `POCKET_WS_HEARTBEAT_MS`
- `POCKET_REMINDER_CHECK_INTERVAL_SEC`

See [DEPLOYMENT_ENV_VARS.md](DEPLOYMENT_ENV_VARS.md) for the full checklist.

## Security Notes

- Do not store live credentials in `.env.example` or repository docs.
- Prefer private DNS names or internal endpoints over public IPs in committed examples.
- Keep instance credentials and discovery credentials in host or server environment variables.
- Route model configuration updates through the backend, not directly from mobile devices.

## Validation

Backend:

```bash
cd backend
go test ./...
```

Frontend:

```bash
cd frontend
npm install
npm run build
```

## Operational Advice

- Use explicit instance IDs that remain stable across redeploys.
- Keep environment labels simple: `development`, `staging`, `production`.
- Treat the instance catalog as deployment configuration, not application content.
- Avoid committing one-off server names, IPs, or delivery reports into the standalone repository.
