# OpenCode Pocket Production Deployment

This document describes a portable production deployment shape for OpenCode Pocket without assuming a specific infrastructure product.

## Deployment Topology

```text
[Mobile web / Android shell]
            |
            v
[OpenCode Pocket frontend]
            |
            v
[OpenCode Pocket backend]
   |                    |
   v                    v
[Instance catalog]     [SQLite task store]
            |
            v
[Managed OpenCode instances]
```

## Deployment Goals

A production deployment should provide:
- a stable backend address for mobile clients
- a writable filesystem for SQLite data
- a curated instance catalog or external discovery endpoint
- TLS termination appropriate for your environment
- operator-managed secrets outside the repository

## Required Runtime Inputs

At minimum, configure:
- `POCKET_HTTP_PORT`
- `POCKET_DB_PATH`
- `POCKET_INSTANCE_CATALOG_JSON` or an equivalent discovery endpoint

Optional but common:
- `POCKET_INSTANCE_DISCOVERY_BASE_URL`
- `POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN`
- `POCKET_OPENCODE_TIMEOUT_MS`
- `POCKET_WS_HEARTBEAT_MS`
- `POCKET_REMINDER_CHECK_INTERVAL_SEC`

Full variable reference: [DEPLOYMENT_ENV_VARS.md](DEPLOYMENT_ENV_VARS.md)

## Build and Run

### Backend

```bash
cd backend
go build -o pocketd cmd/pocketd/main.go
./pocketd
```

### Frontend

```bash
cd frontend
npm install
npm run build
```

## Recommended Service Layout

Example server layout:

```text
/opt/opencode-pocket/
  backend/
  frontend/
  data/
  .env
```

Recommended runtime behaviors:
- run the backend under a service manager such as `systemd` or an equivalent supervisor
- keep the SQLite database on persistent local storage
- terminate TLS upstream or at the same host, depending on your platform
- inject secrets through runtime environment variables or a secret manager

## Example Startup Sequence

```bash
cd /opt/opencode-pocket/backend
export $(grep -v '^#' ../.env | xargs)
./pocketd
```

If your shell environment or secret manager already injects variables, prefer that over sourcing from a local file.

## Verification

After deployment, verify:

```bash
curl http://127.0.0.1:8088/healthz
curl http://127.0.0.1:8088/api/instances
curl http://127.0.0.1:8088/api/tasks
```

And confirm from the codebase:

```bash
cd backend && go test ./...
cd ../frontend && npm run build
```

## Android Packaging

If you package the frontend into the Android shell:

```bash
cd frontend
npm run build
npx cap sync android
cd ../android
./gradlew assembleDebug
```

## Security Requirements

- keep live secrets out of Git
- do not publish private instance endpoints in public docs
- use placeholders in committed configuration examples
- audit generated logs and reports before sharing them externally
- expose only the minimum backend surface required by mobile clients
