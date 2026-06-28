# OpenCode Pocket Implementation Plan

## Objective

Package OpenCode Pocket as a small, portable control service that can be maintained independently and consumed as a git submodule when needed.

## Near-Term Workstreams

### 1. Backend stability

Focus areas:
- keep the task store simple and local
- preserve a stable instance catalog interface
- support compatible remote configuration endpoints
- make configuration names clearer for external users while remaining backward compatible

### 2. Frontend usability

Focus areas:
- mobile-first task list and task detail flow
- clear navigation between tasks, sessions, and instance configuration
- low-friction build and packaging process

### 3. Release hygiene

Focus areas:
- standalone README and license
- placeholder-only environment examples
- concise deployment checklist
- repeatable verification steps for backend and frontend

### 4. Android shell readiness

Focus areas:
- keep Capacitor wrapper minimal
- maintain shared frontend assets as the only product UI source
- leave room for later notification and device features without coupling them to the backend

## Repository Layout

```text
opencode-pocket/
├── LICENSE
├── README.md
├── DESIGN.md
├── IMPLEMENTATION_PLAN.md
├── android/
├── backend/
├── frontend/
├── shared/
└── docs/
```

## Implementation Notes

- The backend remains in Go for a small deployment surface.
- The frontend remains in Vue 3 + Vite for mobile-first iteration speed.
- SQLite remains the default task store for easy local and small-server deployment.
- Public docs describe generic instance discovery and catalog management, not a specific infrastructure product.
- Backward compatibility for older environment variable names is preserved in code where practical.

## Verification Targets

Before a release or submodule update:
- `go test ./...` passes in `backend/`
- `npm run build` passes in `frontend/`
- example environment files contain placeholders only
- public docs avoid production secrets and hard-coded production endpoints
- git history contains only durable project artifacts, not local build output or one-off delivery reports

## Longer-Term Follow-Up

- stronger auth and access control
- richer summaries and task graphing
- improved Android release packaging
- optional fleet-level policy management around remote configuration
