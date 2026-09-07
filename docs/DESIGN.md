# OpenCode Pocket Design

## Goal

OpenCode Pocket is a mobile-first control surface for coordinating multiple OpenCode instances from a single interface.

The design goals are:
- keep instance access simple for operators on mobile devices
- organize work by task and session relationships instead of raw process lists
- support remote model configuration through the Pocket backend
- preserve a path to Android packaging without forking product logic
- remain small enough to run as a standalone sidecar service

## System Shape

```text
[Mobile web / Android shell]
            |
            v
[Vue frontend]
            |
            v
[Go backend]
   |                    |
   v                    v
[Instance catalog]     [SQLite task store]
            |
            v
[Managed OpenCode instances]
```

The backend is the trust boundary and coordination layer. Mobile clients talk only to the Pocket backend. The backend resolves instance metadata, performs task persistence, and proxies compatible management calls to OpenCode instances.

## Core Concepts

### Instance catalog

Pocket needs a stable mapping between an operator-facing instance ID and the base URL used to talk to that instance.

The catalog may come from:
- explicit environment configuration
- an external discovery endpoint managed outside this repository
- future fleet-management integrations

### Task-centric workflow

Operators usually care about a task outcome first and a raw session second. Pocket therefore models:
- tasks
- session attachments
- per-task summaries and session roles

### Remote configuration

Some OpenCode instances expose configuration endpoints for model/provider management. Pocket keeps those operations behind the backend so the frontend only deals with one control API.

### Android shell

The Android directory is a thin wrapper over the same web UI. Platform features such as push, biometric unlock, deep links, and offline queue recovery can be added there without changing the control-plane model.

## Security Posture

This repository must not contain production secrets.

Security expectations for deployment:
- instance credentials stay in host or server environment variables
- example files use placeholders only
- operators expose only the minimum reachable instance endpoints needed by Pocket
- remote configuration requests flow through the backend instead of directly from the phone
- logs and documents must avoid embedding production API keys, passwords, or private addresses

## Scope Boundaries

Included in this repository:
- Pocket backend API
- Pocket frontend UI
- Android shell scaffold
- deployment documentation and environment variable templates

Not included in this repository:
- a specific discovery product implementation in public docs
- infrastructure automation for every deployment topology
- production secrets or server-local configuration state
- direct changes to OpenCode server behavior outside its exposed APIs

## Evolution Path

Short term:
- make instance catalog management more robust
- improve mobile session/task navigation
- refine model configuration UX and validation

Medium term:
- add stronger device-level auth controls
- improve health reporting and reconnect handling
- add reminders and notifications

Long term:
- support richer task graphs and summaries
- expand tablet/foldable layouts
- integrate with broader fleet-management systems
