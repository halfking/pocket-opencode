# OpenCode Pocket

OpenCode Pocket is a mobile-first control surface for managing multiple OpenCode instances, task context, and remote model configuration from a single place.

It is intended for small teams and individual operators who need a lightweight way to:
- register and organize OpenCode instances
- inspect sessions across instances
- group work by task instead of by raw session list
- update model/provider configuration remotely
- prepare a stable Android shell around the same web frontend

## Status

This repository is maintained as a standalone project and can also be consumed as a git submodule from a larger deployment monorepo.

The current codebase includes:
- `backend/`: Go API server and local persistence
- `frontend/`: Vue 3 mobile-oriented web frontend
- `android/`: Capacitor-based Android shell scaffold
- `shared/`: shared schemas and contracts
- `docs/`: deployment, integration, and configuration guidance

## What It Does

OpenCode Pocket focuses on operational control, not on replacing OpenCode itself.

Core capabilities in the current implementation:
- task CRUD with local SQLite persistence
- multi-instance inventory and session listing
- task-to-session attachment workflow
- remote model configuration endpoints for OpenCode-compatible instances
- mobile-oriented frontend for task and session workflows
- Android wrapper scaffold for packaging the same frontend as an app

## Architecture

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
[Instance discovery]   [SQLite task store]
            |
            v
[Managed OpenCode instances]
```

The backend accepts either:
- an explicit instance catalog through environment variables, or
- an external instance discovery endpoint managed outside this repository

This repository does not assume any specific discovery product in its public documentation. Operators are expected to provide instance metadata and management connectivity through their own environment.

## Long-Term Plan

The project roadmap is intentionally conservative.

Near term:
- harden multi-instance inventory and health reporting
- improve task-to-session navigation from mobile
- stabilize remote model configuration workflows
- complete Android packaging and release process

Mid term:
- device-aware authentication and authorization
- richer instance metadata and health summaries
- background reminders and notification delivery
- better offline and reconnect behavior for mobile clients

Long term:
- foldable/tablet layout refinement
- richer task graph and session summarization
- optional fleet-level policy controls for model configuration
- broader deployment automation around packaged releases

## Required Components

A minimal deployment needs:
- Go `1.22+` for the backend
- Node.js `18+` for the frontend build
- a reachable OpenCode-compatible API endpoint for each managed instance
- either SQLite local storage or a writable filesystem path for the task database
- optional Android toolchain if you want to build the mobile shell

Detailed configuration is documented in [docs/DEPLOYMENT_ENV_VARS.md](docs/DEPLOYMENT_ENV_VARS.md).

## Quick Start

### Backend

```bash
cd backend
go run cmd/pocketd/main.go
```

### Frontend

```bash
cd frontend
npm install
npm run build
npm run dev
```

### Verification

```bash
cd backend && go test ./...
cd ../frontend && npm run build
```

## Documentation

- [docs/QUICK_INTEGRATION.md](docs/QUICK_INTEGRATION.md)
- [docs/INTEGRATION.md](docs/INTEGRATION.md)
- [docs/PRODUCTION_DEPLOYMENT.md](docs/PRODUCTION_DEPLOYMENT.md)
- [docs/DEPLOYMENT_ENV_VARS.md](docs/DEPLOYMENT_ENV_VARS.md)
- [docs/DEPLOYMENT_CHECKLIST.md](docs/DEPLOYMENT_CHECKLIST.md)
- [docs/MODEL_CONFIG_UI.md](docs/MODEL_CONFIG_UI.md)
- [DESIGN.md](DESIGN.md)
- [IMPLEMENTATION_PLAN.md](IMPLEMENTATION_PLAN.md)

## License

This project is distributed under the PolyForm Noncommercial License 1.0.0.

That means the source is available, but commercial use is not permitted under the default license terms. This is not an OSI open source license.
