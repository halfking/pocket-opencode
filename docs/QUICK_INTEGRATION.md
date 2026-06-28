# OpenCode Pocket Quick Integration

This guide gets a basic OpenCode Pocket environment running with an explicit instance catalog and placeholder-only configuration.

## Prerequisites

You need:
- Go `1.22+`
- Node.js `18+`
- at least one reachable OpenCode-compatible API endpoint
- a writable local path for SQLite

## 1. Create a local environment file

From the repository root:

```bash
cp .env.integration.example .env
```

Set only the values that apply to your environment. Keep secrets in host or server environment variables, not in Git.

The most important variable is the instance catalog:

```bash
POCKET_INSTANCE_CATALOG_JSON='[
  {
    "id": "opencode-main",
    "displayName": "OpenCode Main",
    "apiBaseURL": "https://opencode-main.example.internal",
    "environment": "production"
  }
]'
```

If your environment exposes a separate discovery service, you can also set:

```bash
POCKET_INSTANCE_DISCOVERY_BASE_URL=https://discovery.example.internal
POCKET_INSTANCE_DISCOVERY_AUTH_TOKEN=<INSTANCE_DISCOVERY_AUTH_TOKEN>
```

## 2. Start the backend

```bash
cd backend
go run cmd/pocketd/main.go
```

Expected log signals:
- task store initialized
- loaded instance catalog from config, if provided
- `pocketd listening on :8088`

## 3. Verify backend endpoints

```bash
curl http://localhost:8088/healthz
curl http://localhost:8088/api/instances
curl http://localhost:8088/api/tasks
```

## 4. Build or run the frontend

```bash
cd frontend
npm install
npm run dev
```

For a production build:

```bash
npm run build
```

## 5. Attach a session to a task

Once an instance is reachable through the configured catalog:

```bash
curl -X POST http://localhost:8088/api/tasks \
  -H "Content-Type: application/json" \
  -d '{"id":"demo-task","title":"Demo task","status":"active","priority":"high"}'

curl "http://localhost:8088/api/sessions/?instance_id=opencode-main"
```

## Common Notes

- Prefer `POCKET_INSTANCE_CATALOG_JSON` when you need stable routing and configuration management.
- The backend still accepts older compatibility variable names for existing deployments, but new setups should use the generic names documented here.
- Public docs intentionally avoid embedding infrastructure-specific discovery assumptions.

## Next Reading

- [INTEGRATION.md](INTEGRATION.md)
- [DEPLOYMENT_ENV_VARS.md](DEPLOYMENT_ENV_VARS.md)
- [DEPLOYMENT_CHECKLIST.md](DEPLOYMENT_CHECKLIST.md)
