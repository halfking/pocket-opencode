# Superseded Documents Index

> **Last updated**: 2026-08-27
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

## Group 5 — 历史冲刺点时性报告与旧计划（project root, 2026-06-27 ~ 2026-07-08）

这批文档是旧冲刺（移动端 v2、模拟器/本地部署验证、Phase 1-7、六/七轮审计）当场产生的交付/测试/部署/修复/计划记录。它们声称的"完成/成功/最终"状态属于当时，不可作为当前能力证据。能力现状一律以 `docs/governance/STATUS-MATRIX.md` 为准；规划现状以 `docs/新架构v1/03-roadmap/里程碑.md` 为准。全部已于 2026-08-27 加 `STATUS: superseded` 横幅。

| 类别 | 文件（project root） | Replaced by |
|---|---|---|
| 交付/完成报告 | `ARCHITECTURE_DELIVERABLES.md`, `ARCHITECTURE_REFACTORING_SUMMARY.md`, `BACKEND_IMPLEMENTATION_REPORT.md`, `BACKEND_VERIFICATION_REPORT.md`, `COMPLETION_REPORT.md`, `COMPONENTS_IMPLEMENTATION_SUMMARY.md`, `COMPREHENSIVE_UPGRADE_SUMMARY.md`, `INTEGRATION_COMPLETE.md`, `PHASE_1_2_DELIVERY.md`, `PHASE4_FINAL_SUMMARY.md`, `PHASE4_IMPLEMENTATION_REPORT.md`, `PLUGIN_IMPLEMENTATION_SUMMARY.md`, `PROJECT_DELIVERY_SUMMARY.md`, `FEISHU_CALLBACK_INTEGRATION.md` | `docs/governance/STATUS-MATRIX.md` |
| FINAL/DEPLOYMENT 系列 | `FINAL_DELIVERY{,_REPORT,_REPORT_V2,_REPORT_V3,_CHECKLIST,_PHASE_6_7}.md`, `FINAL_DEPLOYMENT_{REPORT_2026-06-29,SUCCESS,REPORT_2026-06-29_v2}.md`, `FINAL_{PROJECT_SUMMARY,SUMMARY,SUMMARY_WITH_FLOWCHARTS,TEST_SUMMARY,V2_REPORT,FIX_REPORT_2026-07-07,VERIFICATION_REPORT_2026-07-07,AUDIT_AND_DEPLOYMENT_CHECKLIST}.md`, `DEPLOYMENT_{COMPLETE_SUMMARY,COMPLETION_SUMMARY,READY_SUMMARY,REPORT_2026-06-29,SUCCESS,ARCHITECTURE_PLAN}.md`, `DEVELOPMENT_PROGRESS_2026-06-29.md`, `PROJECT_{AUDIT_REPORT,COMPLETION_REPORT_FINAL,STATUS_HANDOFF}.md` | `docs/governance/STATUS-MATRIX.md` |
| 测试报告/计划 | `COMPLETE_FUNCTIONAL_TEST_REPORT.md`, `COMPLETE_{INTEGRATION_,}TEST_REPORT{,_2026-07-07}.md`, `CURRENT_TEST_STATUS.md`, `EMULATOR_TEST_REPORT.md`, `SIMULATOR_TEST_{REPORT,REPORT_2026-07-07,PLAN}.md`, `LOCAL_{DEPLOYMENT_REPORT_2026-07-07,DEPLOYMENT_SUCCESS,TEST_COMPLETE_REPORT,COMPLETE_TEST_PLAN,INTEGRATION_TEST_PLAN,WEB_TEST_PLAN}.md`, `MANUAL_TEST_GUIDE.md`, `MOBILE_TEST_{QUICK_START,REPORT_2026-07-04,VERIFICATION_PLAN}.md`, `QUICK_START_TESTING.md`, `REAL_STATUS_AUDIT.md`, `VERIFICATION_TEST_REPORT.md`, `PRE_DEPLOYMENT_AUDIT.md` | `docs/governance/STATUS-MATRIX.md` |
| 修复/排查记录 | `ANDROID_APP_BUILD_SUCCESS.md`, `APK_FIX_REPORT.md`, `APP_INSTALLED_READY_FOR_TEST.md`, `CRITICAL_ISSUE_MIXED_CONTENT_BLOCKER.md`, `FRONTEND_API_FIX.md`, `INSTANCE_LIST_DEBUG_GUIDE.md`, `LOBSTER_FIX_READY.md`, `MCP_CONNECTION_FINAL_ANALYSIS.md`, `MCP_TLS_SNI_ISSUE.md`, `MANUAL_INSTALL_INSTRUCTIONS.md`, `QUICK_FIX_SUMMARY.md`, `ROUTER_GUARD_FIX.md` | `docs/governance/STATUS-MATRIX.md` |
| Phase 验证 | `PHASE_1_JWT_IMPLEMENTATION.md`, `PHASE_3_VERIFICATION.md` ~ `PHASE_6_VERIFICATION_REPORT.md`, `PHASE_7_{PLAN,SPRINT_1_REPORT,SPRINT_1_TEST_REPORT}.md` | `docs/governance/STATUS-MATRIX.md` |
| 审计（历史轮次） | `AUDIT_HANDOFF_SUMMARY.md`, `AUDIT_REPORT.md`, `AUDIT_REPORT_R7.md`, `AUDIT_R7_FIXES_VERIFICATION.md`, `AUDIT_ROUND_{6_PLAN,7_COMPLETE,7_PROMPT}.md`, `SECURITY_AUDIT_EMAIL_AUTH.md`, `SECURITY_AUDIT_REPORT_R8.md` | `docs/governance/STATUS-MATRIX.md`（近期审计 `AUDIT_REPORT_2026-07-25.md`、`AUDIT_REPORT_AUDIT_PG_2026_08_19.md` 仍有效，未列入） |
| 旧计划/提示词 | `IMPLEMENTATION_PLAN.md`, `NEXT_STEPS_PLAN.md`, `PLAN_C_RESEARCH_RESULTS.md`, `PLAN_REAL_TASKS.md`, `PORT_MIGRATION_PLAN.md`, `PROMPT_{BACKEND,UI}_SESSION.md`, `REMAINING_TASKS.md`, `TODO_CHECKLIST.md`, `CURRENT_STATUS_AND_OPTIONS.md`, `TODAY_SUMMARY_AND_RECOMMENDATION.md`, `SESSION_HANDOFF.md`, `SESSION_SUMMARY_2026-07-06.md` | `docs/新架构v1/03-roadmap/里程碑.md` |

**保留未归档**（近期证据/运行手册）：`48H_MODIFICATION_REPORT.md`、`ACP_DELIVERY_REPORT.md`、`PHASE2_TASK_PLAN.md`、`AUDIT_REPORT_2026-07-25.md`、`AUDIT_REPORT_AUDIT_PG_2026_08_19.md`、`SESSION_HANDOFF_2026_08_14.md`、`SESSION_HANDOFF_AUDIT_PG_2026_08_19.md`、`P0C/P1/P2/P3_*_2026_08_*.md`、`VIVO_USB_DEBUG_GUIDE.md`、`OPERATIONS_GUIDE.md`、`DEPLOYMENT_GUIDE.md`、`INSTALLATION_GUIDE.md`、`DEPLOYMENT_CHECKLIST.md`、`DEPLOY_AFTER_184_RESET.md`、`AUTO_UPDATE_FEATURE.md`、`DESIGN.md`、`DATA_ARCHITECTURE.md`、`README.md`。

## Group 6 — 移动端 UI/交互设计文档（被 v2 统一设计方案取代）

移动端产品流程与交互设计的现行唯一来源是 **`docs/2026-08-27-mobile-ux-design-v2.md`**（上位设计系统规范 `docs/2026-07-02-ui-ux-design-system.md` 仍然有效）。以下文档的设计面全部被其取代并吸收：

| 文件 | Replaced by |
|---|---|
| `docs/2026-07-03-mobile-interaction-optimization.md` | `docs/2026-08-27-mobile-ux-design-v2.md` |
| `docs/MOBILE_ARCHITECTURE_V2.md` | `docs/2026-08-27-mobile-ux-design-v2.md` |
| `NAVIGATION_ARCHITECTURE.md` | `docs/2026-08-27-mobile-ux-design-v2.md` §3（路由实现事实以 `frontend/src/app/router-mobile.ts` 为准） |
| `MOBILE_V2_COMPLETION_REPORT.md` | `docs/2026-08-27-mobile-ux-design-v2.md` |
| `UI_OPTIMIZATION_CODEX_STYLE.md` | `docs/2026-08-27-mobile-ux-design-v2.md`（tokens 体系仍生效，见 `frontend/src/styles/tokens.css`） |
| `RESPONSIVE_V5_READY.md` | `docs/2026-08-27-mobile-ux-design-v2.md` §4.5 |
| `UI_TEST_CHECKLIST.md` | `docs/2026-08-27-mobile-ux-design-v2.md` §1.3 验收锚点 |
| `UI_UX_IMPLEMENTATION_SUMMARY.md` | `docs/2026-08-27-mobile-ux-design-v2.md` |

## How a doc gets here

A doc moves into this index when **any one** of the following is true:
1. It claims a status (complete / verified / final / production-ready) that is not reproducible from current code.
2. Its replacements have superseded its design surface (architecture, plan, product spec).
3. The reader's first instinct when seeing the title would be to trust it as current — but it isn't.

The presence of an inline `STATUS: superseded` banner is the on-disk marker; this file is the human index.
