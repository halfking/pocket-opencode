# OpenCode Pocket Deployment Checklist

Use this checklist before publishing or deploying OpenCode Pocket.

## Environment Preparation

- [ ] Go `1.22+` is available
- [ ] Node.js `18+` is available
- [ ] a writable path exists for `POCKET_DB_PATH`
- [ ] at least one OpenCode-compatible instance endpoint is reachable
- [ ] runtime secrets are provided outside Git

## Configuration Readiness

- [ ] `.env` or runtime environment is populated from placeholders
- [ ] `POCKET_INSTANCE_CATALOG_JSON` is valid JSON, or a discovery endpoint is configured
- [ ] no committed file contains live tokens, passwords, or private keys
- [ ] no committed example contains production IPs or private hostnames that should remain internal

## Backend Validation

```bash
cd backend
go test ./...
go build -o pocketd cmd/pocketd/main.go
```

Confirm:
- [ ] tests pass
- [ ] backend builds successfully
- [ ] `./pocketd` starts and exposes `/healthz`

## Frontend Validation

```bash
cd frontend
npm install
npm run build
```

Confirm:
- [ ] production build succeeds
- [ ] generated assets are current
- [ ] no obvious runtime configuration is hard-coded into frontend source

## API Smoke Tests

With the backend running:

```bash
curl http://localhost:8088/healthz
curl http://localhost:8088/api/instances
curl http://localhost:8088/api/tasks
```

Optional when a real instance is configured:

```bash
curl "http://localhost:8088/api/sessions/?instance_id=<INSTANCE_ID>"
curl "http://localhost:8088/api/config/models?instance_id=<INSTANCE_ID>"
```

Confirm:
- [ ] health endpoint returns `ok`
- [ ] instance catalog loads as expected
- [ ] task endpoints respond normally

## Android Packaging

Only if you ship the Android shell:

```bash
cd frontend
npm run build
npx cap sync android
cd ../android
./gradlew assembleDebug
```

Confirm:
- [ ] Android assets sync successfully
- [ ] debug build completes

## Release Hygiene

- [ ] `README.md` matches the current purpose and roadmap
- [ ] `LICENSE` is present and accurate
- [ ] docs use placeholders instead of real credentials
- [ ] one-off delivery reports and local build artifacts are excluded from the published repository
