# Superseded Documents Index

> **Last updated**: 2026-08-23
> **Purpose**: Every document that is **not** the source of truth for current implementation decisions. Each entry points to the replacement.
>
> **Rule**: If a reader is unsure whether to trust a doc, they should check this index first. If the doc is listed here, **do not use it** for new work — read the replacement link instead.

## Group 1 — Legacy `OPENCODE_*FINAL*` / `OPENCODE_*COMPLETE*` / `OPENCODE_*VERIFICATION*` / `OPENCODE_*DELIVERY*` reports (project root)

These files were committed directly under `services/opencode-pocket/` (not under `docs/`). They were generated during a sprint that pre-dated the v3 architecture audit and the ZAG redesign. They are preserved for history but must not be cited as evidence of current capability.

| File (project root) | Replaced by |
|---|---|
| `OPENCODE_FINAL_DELIVERY.md` | `docs/新架构v1/README.md`, `docs/governance/STATUS-MATRIX.md` |
| `OPENCODE_COMPLETE_VERIFICATION_REPORT.md` | `docs/governance/STATUS-MATRIX.md`, `docs/新架构v1/04-contracts/_status.md` |
| `OPENCODE_INTEGRATION_VERIFICATION.md` | `docs/governance/STATUS-MATRIX.md` |
| `OPENCODE_DATABASE_VERIFICATION.md` | `docs/优化v4/reports/audit-pg-docker-integration-2026-08-18.md`, `docs/governance/EVIDENCE-LEDGER.md` (audit row) |
| `OPENCODE_VERIFICATION_STATUS.md` | `docs/governance/STATUS-MATRIX.md` |
| `OPENCODE_COMPLETE_API_MAPPING.md` | `docs/新架构v1/03-roadmap/接口规范.md` |
| `OPENCODE_DELIVERY_SUMMARY.md` | `docs/新架构v1/README.md` |
| `OPENCODE_IMPLEMENTATION_SUMMARY.md` | `docs/新架构v1/README.md`, `docs/governance/STATUS-MATRIX.md` |
| `OPENCODE_DISCOVERY_SUMMARY.md` | `docs/新架构v1/04-contracts/pocket-zag-incremental.md` |
| `OPENCODE_ADAPTER_FIXES.md` | `docs/新架构v1/04-contracts/pocket-adapter-matrix.md` |
| `OPENCODE_API_ANALYSIS.md` | `docs/新架构v1/03-roadmap/接口规范.md` |
| `OPENCODE_API_SETUP.md` | `docs/新架构v1/03-roadmap/接口规范.md` |
| `OPENCODE_CONNECTION_STATUS.md` | `docs/governance/STATUS-MATRIX.md` |
| `OPENCODE_INSTANCE_DISCOVERY.md` | `docs/新架构v1/02-modules/redclaw-integration.md` |
| `OPENCODE_INSTANCE_REGISTRATION.md` | `docs/新架构v1/02-modules/redclaw-integration.md` |
| `OPENCODE_PLUGIN_ARCHITECTURE.md` | `docs/新架构v1/02-modules/ide-control.md` |
| `OPENCODE_SESSION_OPTIMIZATION.md` | `docs/新架构v1/02-modules/ide-control.md` |
| `OPENCODE_MOBILE_MANAGEMENT_PLAN.md` | `docs/新架构v1/02-modules/mobile-shell.md` |

## Group 2 — `docs/OPENCODE_*` guide docs (under `docs/`)

| File | Replaced by |
|---|---|
| `docs/OPENCODE_DEMO_GUIDE.md` | `docs/新架构v1/README.md`, `docs/governance/STATUS-MATRIX.md` |
| `docs/OPENCODE_DISCOVERY_API.md` | `docs/新架构v1/04-contracts/pocket-zag-incremental.md` |
| `docs/OPENCODE_FILES_CHECKLIST.md` | `docs/governance/STATUS-MATRIX.md` (OpenCode adapter row) |
| `docs/OPENCODE_IMPLEMENTATION_GUIDE.md` | `docs/新架构v1/README.md`, `docs/governance/STATUS-MATRIX.md` |
| `docs/opencode-task-management-architecture.md` | `docs/新架构v1/02-modules/zagent-gateway.md`, `docs/governance/STATUS-MATRIX.md` |

## Group 3 — RedClaw sprint plans under `docs/superpowers/plans/`

These predate the v3 audit (`docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md`), which concluded that RedClaw **cannot** be used as a drop-in OpenCode backend. Use the v3 modules instead.

| File | Replaced by |
|---|---|
| `docs/superpowers/plans/2026-07-24-phase1-redclaw-integration.md` | `docs/新架构v1/02-modules/redclaw-integration.md`, `docs/新架构v1/04-contracts/pocket-adapter-matrix.md` |
| `docs/superpowers/plans/2026-07-27-phase-f-redclaw-acp-ai-hub.md` | `docs/新架构v1/02-modules/redclaw-integration.md`, `docs/新架构v1/02-modules/ide-control.md` |
| `docs/superpowers/specs/2026-07-27-pocket-foldable-redclaw-integration-design.md` | `docs/新架构v1/02-modules/mobile-shell.md` |
| `docs/superpowers/specs/2026-07-24-opencode-supreme-programmer-mobile-platform-design.md` | `docs/新架构v1/README.md` |

## Group 4 — `docs/优化v4/` (entire directory)

The `docs/优化v4/` plan pre-dates the v3 audit and the ZAG redesign. The functional claims in `优化v4/reports/` are kept as **historical evidence** for the things that were actually run on 2026-08-18 (audit/PG, Docker, iOS real-device), but the **plan / product / architecture / roadmap** files are superseded.

### `优化v4/` plan files (superseded — see v3 replacements)

| File | Replaced by |
|---|---|
| `docs/优化v4/README.md` | `docs/新架构v1/README.md` |
| `docs/优化v4/01-现状审计与差距.md` | `docs/新架构v1/00-research/现有服务能力盘点.md`, `docs/新架构v1/01-architecture/系统总览.md` |
| `docs/优化v4/02-安全审计与整改清单.md` | `docs/新架构v1/01-architecture/安全模型.md`, `docs/security/zag-adr-0001..0007.md` |
| `docs/优化v4/03-产品蓝图与信息架构.md` | `docs/新架构v1/01-architecture/系统总览.md`, `docs/新架构v1/02-modules/mobile-shell.md` |
| `docs/优化v4/04-目标架构与领域拆分.md` | `docs/新架构v1/01-architecture/系统总览.md`, `docs/新架构v1/02-modules/zagent-gateway.md` |
| `docs/优化v4/05-数据模型与本地云同步.md` | `docs/新架构v1/01-architecture/数据流与协议.md` |
| `docs/优化v4/06-隐私安全与凭据边界.md` | `docs/新架构v1/01-architecture/安全模型.md`, `docs/security/zag-adr-0003-authz-model.md` |
| `docs/优化v4/07-AI编排与智能体控制面.md` | `docs/新架构v1/02-modules/redclaw-integration.md`, `docs/新架构v1/02-modules/zagent-gateway.md` |
| `docs/优化v4/08-移动端UI与交互规范.md` | `docs/新架构v1/02-modules/mobile-shell.md` |
| `docs/优化v4/09-实施路线图与验收标准.md` | `docs/新架构v1/03-roadmap/里程碑.md`, `docs/新架构v1/03-roadmap/接口规范.md` |
| `docs/优化v4/10-任务拆解与依赖图.md` | `docs/新架构v1/03-roadmap/里程碑.md` |
| `docs/优化v4/11-并行执行提示词.md` | (deferred — see `REVIEW-QUEUE.md`) |
| `docs/优化v4/12-ADR与风险台账.md` | `docs/新架构v1/architecture-decision-records.md` |
| `docs/优化v4/13-竞品与一手资料对标.md` | `docs/新架构v1/00-research/竞品分析.md` |
| `docs/优化v4/14-首批PR与执行顺序.md` | `docs/新架构v1/03-roadmap/里程碑.md` |
| `docs/优化v4/15-PR1-契约冻结与发布前置.md` | `docs/新架构v1/04-contracts/pocket-zag-incremental.md` |
| `docs/优化v4/16-PR2-ADR-api-v4-延后.md` | `docs/新架构v1/architecture-decision-records.md` |

### `优化v4/reports/` (kept as historical evidence, not superseded)

These are **not** superseded — they are first-hand run logs from 2026-08-18 that back rows in `STATUS-MATRIX.md` and entries in `EVIDENCE-LEDGER.md`. They must be read alongside the matrix; do not delete them.

| File | Used as evidence for |
|---|---|
| `docs/优化v4/reports/audit-pg-docker-integration-2026-08-18.md` | Audit row in `STATUS-MATRIX.md` |
| `docs/优化v4/reports/docker-acc-integration-2026-08-18.md` | ACC connector row |
| `docs/优化v4/reports/docker-acc-integration-verify-2026-08-18.md` | ACC connector row |
| `docs/优化v4/reports/docker-llm-gateway-pg-switch-2026-08-18.md` | LLM gateway row |
| `docs/优化v4/reports/ios-real-2026-08-18.md` | Multi-tenant auth row |

## How a doc gets here

A doc moves into this index when **any one** of the following is true:
1. It claims a status (complete / verified / final / production-ready) that is not reproducible from current code.
2. Its replacements have superseded its design surface (architecture, plan, product spec).
3. The reader's first instinct when seeing the title would be to trust it as current — but it isn't.

The presence of an inline `STATUS: superseded` banner is the on-disk marker; this file is the human index.
