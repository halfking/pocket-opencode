# OpenPocket Documentation Status Matrix

> **Last updated**: 2026-08-23
> **Scope**: Single source of truth for the current implementation status of every architectural component in OpenPocket.
> **Authority**: This matrix overrides any inline claim of "implemented", "complete", or "verified" found in legacy documents.
>
> **Status legend (ordered weakest → strongest)**:
> - `planned` — design / roadmap only, no code.
> - `in-progress` — code partially written, not yet callable end-to-end.
> - `implemented (unverified)` — code is committed and compiles, but no automated test or runtime check currently exercises the contract end-to-end.
> - `contract-tested` — behavior is pinned by an automated test against a mocked or recorded upstream/downstream contract.
> - `integration-tested` — the component has been exercised end-to-end against a real upstream/downstream (not a mock) in CI or a recorded run, with logs captured in the Evidence Ledger.
> - `production-verified` — observed live in production telemetry, with an on-call owner and a runbook.
> - `superseded` — replaced by a newer doc / component; the replacement is listed in `SUPERSEDED.md`.
>
> **Evidence levels**:
> - `claimed (unverified)` — text-only assertion by the doc author; no reproducible artifact.
> - `source-inspected` — code or config referenced by path:line in the doc was read and matches the claim.
> - `contract-tested` — contract test passes in CI (link to test report in Evidence Ledger).
> - `mock-only` — only a mock / fake / in-memory implementation exists.
> - `integration-tested` — tested end-to-end against a real component.
> - `production-verified` — seen in production telemetry.
>
> **Components below are ordered by criticality for the ZAG doc-governance PR.** If a row disagrees with the code or with another doc, the code wins, and this matrix must be updated.

| Component | Doc | Status | Evidence Level | Supersedes | Superseded By | Source Evidence |
|---|---|---|---|---|---|---|
| OpenCode adapter (legacy `backend/internal/adapter/opencode_http.go`) | `docs/opencode-task-management-architecture.md`, `docs/OPENCODE_*.md` (root + `docs/`) | `implemented (unverified)` | `source-inspected` | — | `docs/新架构v1/02-modules/redclaw-integration.md`, `docs/新架构v1/04-contracts/pocket-zag-incremental.md` | `backend/internal/adapter/opencode_http.go`, `backend/internal/zagclient/` (reserved) |
| ZAG (ZAgentGateway) | `docs/新架构v1/02-modules/zagent-gateway.md`, `docs/security/zag-adr-0001..0007.md` | `in-progress` | `mock-only` | — | — | `backend/internal/zagclient/{client.go,noop.go,client_test.go}`, `docs/新架构v1/04-contracts/_status.md` (reserves row `reserved`) |
| RedClaw adapter (`backend/internal/redclaw/legacy`, `facade`, `mcp`) | `docs/新架构v1/02-modules/redclaw-integration.md`, `docs/优化v4/07-AI编排与智能体控制面.md` | `implemented (unverified)` (legacy) / `contract-tested` (facade) / `implemented (unverified)` (mcp) | `source-inspected` | — | `docs/新架构v1/04-contracts/pocket-adapter-matrix.md` | `backend/internal/redclaw/`, `docs/新架构v1/04-contracts/_status.md` |
| ACC MCP (`backend/internal/host_gateway` ACC connector) | `docs/ACP_INTEGRATION.md`, `docs/新架构v1/02-modules/redclaw-integration.md` | `implemented (unverified)` | `source-inspected` | — | — | `backend/internal/host_gateway/` (MCP), `backend/internal/zagclient/` (pending ZAG ACP) |
| Memora (`kxmemory`) | `docs/新架构v1/02-modules/memory-as-memora.md`, `docs/2026-07-02-kxmemory-api-contract.md` | `contract-tested` | `contract-tested` | — | — | `backend/scripts/test-redclaw-integration.sh`, `docs/优化v4/reports/` |
| IDE connectors (VS Code / Cursor) | `docs/新架构v1/02-modules/ide-control.md`, `docs/superpowers/plans/*redclaw*` | `planned` | `claimed (unverified)` | — | — | (none — design only) |
| LLM gateway (`llm-gateway-go` schema-isolated) | `docs/新架构v1/02-modules/llm-gateway-integration.md`, `docs/security/zag-adr-0003-authz-model.md` | `integration-tested` (PG schema isolation only) | `integration-tested` | — | — | `docs/优化v4/reports/docker-llm-gateway-pg-switch-2026-08-18.md`, `git log feat/audit-pg-hardening` |
| WebSocket hub (`pocketd` WS / SSE) | `docs/WEBSOCKET_REALTIME_UPDATE.md`, `docs/新架构v1/01-architecture/数据流与协议.md` | `implemented (unverified)` | `source-inspected` | — | — | `backend/internal/server/ws_*.go`, `docs/superpowers/specs/2026-08-17-task-approval-projection-design.md` |
| Audit (Postgres-backed PG audit with RLS) | `docs/audits/`, `docs/优化v4/reports/audit-pg-docker-integration-2026-08-18.md`, `docs/security/zag-adr-0007-audit.md` | `integration-tested` | `integration-tested` | — | — | `feat/audit-pg-hardening` branch, `git log main` (post `25727ba`), `docs/优化v4/reports/audit-pg-docker-integration-2026-08-18.md` |
| Multi-tenant auth (`identity-go`) | `docs/security/zag-adr-0003-authz-model.md`, `docs/2026-07-02-deployment-verification-report.md` | `integration-tested` (JWT/RS256 issuer) | `integration-tested` | — | — | `backend/internal/identity/`, `feat/identity-go-cross-trust` (commit `6892ba5`), `docs/优化v4/reports/ios-real-2026-08-18.md` |

## Components explicitly NOT covered above (see `SUPERSEDED.md` for redirect)

- `OPENCODE_FINAL_DELIVERY.md`, `OPENCODE_COMPLETE_VERIFICATION_REPORT.md`, `OPENCODE_INTEGRATION_VERIFICATION.md`, `OPENCODE_DATABASE_VERIFICATION.md`, `OPENCODE_VERIFICATION_STATUS.md` (root)
- `OPENCODE_COMPLETE_API_MAPPING.md`, `OPENCODE_DELIVERY_SUMMARY.md`, `OPENCODE_IMPLEMENTATION_SUMMARY.md` (root)
- `docs/OPENCODE_DEMO_GUIDE.md`, `docs/OPENCODE_DISCOVERY_API.md`, `docs/OPENCODE_FILES_CHECKLIST.md`, `docs/OPENCODE_IMPLEMENTATION_GUIDE.md`
- `docs/superpowers/plans/2026-07-24-phase1-redclaw-integration.md`, `docs/superpowers/plans/2026-07-27-phase-f-redclaw-acp-ai-hub.md`
- The full `docs/优化v4/` set (`01-现状审计与差距.md` … `16-PR2-ADR-api-v4-延后.md`, plus `reports/`)

All of the above are marked `superseded` in-place by an inline banner and indexed in `docs/governance/SUPERSEDED.md`.

## Status transitions (how to update this matrix)

1. Status moves **up** the legend only via an automated test, contract test, or runtime telemetry that is linked in the Evidence Ledger row.
2. Status moves **down** the legend the moment a claim in the doc cannot be reproduced from the linked evidence.
3. A new `superseded` row appears the moment a doc gains a `SUPERSEDED` banner. The doc file is **not deleted** — history is preserved.

## Maintenance rule

This file is owned by the **doc-governance reviewer** for the current sprint. Any PR that touches a status in the table above MUST:
1. Update this row,
2. Add or update an entry in `EVIDENCE-LEDGER.md`,
3. Pass the gate described in `REVIEW-PROCESS.md`.

Failure to update all three is a blocker for merging.
