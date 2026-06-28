# OpenCode Pocket Deployment Environment Variables

This document lists the configuration surface that should be populated at deploy time.

Real values belong in host or server environment variables, secret managers, or deployment-time files outside Git.

## Required Variables

| Variable | Required | Purpose | Example Placeholder |
|---|---|---|---|
| `POCKET_HTTP_PORT` | yes | Backend listen port | `8088` |
| `POCKET_DB_PATH` | yes | SQLite database path | `/opt/opencode-pocket/data/pocket.sqlite` |
| `POCKET_INSTANCE_CATALOG_JSON` | yes* | Explicit instance catalog used by the backend | `<JSON_INSTANCE_CATALOG>` |

`POCKET_INSTANCE_CATALOG_JSON` is required unless you provide an external discovery endpoint and are comfortable with that mode of operation.

## Optional Variables

| Variable | Required | Purpose | Example Placeholder |
|---|---|---|---|
| `POCKET_INSTANCE_DISCOVERY_BASE_URL` | no | External instance discovery endpoint | `https://discovery.example.internal` |
| `POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN` | no | Auth token for the discovery endpoint | `<INSTANCE_DISCOVERY_AUTH_TOKEN>` |
| `POCKET_OPENCODE_TIMEOUT_MS` | no | Backend timeout when talking to instance APIs | `10000` |
| `POCKET_WS_HEARTBEAT_MS` | no | WebSocket heartbeat interval | `15000` |
| `POCKET_REMINDER_CHECK_INTERVAL_SEC` | no | Reminder polling interval | `60` |
| `POCKET_ANDROID_APP_ID` | no | Android package identifier for the shell | `com.example.opencode.pocket` |
| `POCKET_ANDROID_USE_CAPACITOR` | no | Toggle Android shell-specific behavior | `true` |

## Example Catalog Shape

```json
[
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
]
```

## Compatibility Note

The current backend still accepts older compatibility variable names for legacy deployments. New deployments should use the generic names in this document.

## Security Rules

- never commit live tokens, passwords, API keys, or private addresses into repository docs or examples
- use placeholders in all committed configuration files
- rotate any credential immediately if it was ever committed in plain text elsewhere
