# Evidence Ledger — External Dependencies

> **Last updated**: 2026-08-23
> **Purpose**: Record, for every external capability that OpenPocket depends on, the precise pin (commit SHA, branch, endpoint, test log) we relied on. This is the single place reviewers go to verify a "yes, we depend on X and X was at version Y at time T" claim.
>
> **Rule**: a `STATUS-MATRIX.md` row that says `contract-tested` / `integration-tested` / `production-verified` **must** have a row here that names the artifact, its SHA, and the test log that produced the evidence. No exception.

## How to use this file

1. When you add a new external dependency, add a row below with `Status: pending-evidence`. Do not promote the matrix row above `implemented (unverified)` until you have a log path here.
2. When you run a contract / integration / production check, update the row with `Status: <level>` plus the log path.
3. When a dependency moves upstream in a breaking way (e.g. OpenCode routing changes), add a `Last verified date` entry on the old row and start a new row pinned to the new SHA.

---

## OpenCode runtime (external: `/Users/xutaohuang/workspace/ai/opencode`)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/ai/opencode` (external; not in this repo) |
| Pinned commit SHA | **NOT YET PINNED** — see `REVIEW-QUEUE.md` |
| Endpoint contract reference | `docs/opencode-task-management-architecture.md` (legacy, stale); planned canonical contract is `docs/新架构v1/03-roadmap/接口规范.md` |
| Test log reference | none captured (ZAG↔OpenCode contract tests not yet written) |
| Evidence level | `claimed (unverified)` |
| Last verified date | — |
| Status | **pending-evidence** — do NOT cite OpenCode HTTP routes as "stable" until a SHA + test log exist here. |

## RedClaw façade (external: `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go`)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go` |
| Pinned commit SHA | **NOT YET PINNED** |
| Endpoint contract reference | `docs/新架构v1/04-contracts/pocket-adapter-matrix.md`, `docs/新架构v1/02-modules/redclaw-integration.md` |
| Test log reference | none — recorded run only via `docs/优化v4/reports/docker-acc-integration-2026-08-18.md` (which references the deploy, not the façade SHA) |
| Evidence level | `source-inspected` (paths only, no SHA pin) |
| Last verified date | — |
| Status | **pending-evidence** — the audit (`docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md`) explicitly says RedClaw **cannot** replace OpenCode. Until a pin exists, only `/api/v2/{tasks,approvals,notifications,memory}` may be cited, and only at `contract-tested`. |

## RedClaw orchestrator (same external repo, different module)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/services/platform-go` (orchestrator subdir) |
| Pinned commit SHA | **NOT YET PINNED** |
| Endpoint contract reference | `docs/新架构v1/02-modules/redclaw-integration.md` |
| Test log reference | none |
| Evidence level | `source-inspected` |
| Last verified date | — |
| Status | **pending-evidence** |

## `agentcontainer` (external)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/.../agentcontainer` |
| Pinned commit SHA | **NOT YET PINNED** |
| Endpoint contract reference | `docs/新架构v1/02-modules/redclaw-integration.md` (describes `/invoke` route family) |
| Test log reference | none — only doc inspection |
| Evidence level | `source-inspected` |
| Last verified date | — |
| Status | **pending-evidence** |

## RedClaw generic connectors (external)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/.../connectors` |
| Pinned commit SHA | **NOT YET PINNED** |
| Endpoint contract reference | none — **no OpenCode/IDE connector exists** in the generic connector set per the v3 audit |
| Test log reference | none |
| Evidence level | `claimed (unverified)` only — explicitly contradicted by `docs/新架构v1/00-research/RedClaw作为OpenCode后端审计.md` |
| Last verified date | — |
| Status | **blocked** — do not implement IDE adapters on top of generic connectors until the audit's red flag is resolved. |

## OpenClaw runtime (external: `RedClaw/.../openclaw`)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/ai-native-tools/RedClaw/.../openclaw` |
| Pinned commit SHA | **NOT YET PINNED** |
| Endpoint contract reference | `docs/新架构v1/02-modules/zagent-gateway.md` (mentions OpenClaw as an alternative executor) |
| Test log reference | none |
| Evidence level | `source-inspected` |
| Last verified date | — |
| Status | **pending-evidence** |

## ACC MCP (in-repo: `backend/internal/host_gateway`)

| Field | Value |
|---|---|
| Repo path | `backend/internal/host_gateway/` |
| Pinned commit SHA | current `main` head at time of this row |
| Endpoint contract reference | `docs/ACP_INTEGRATION.md`, `docs/新架构v1/02-modules/redclaw-integration.md` |
| Test log reference | `docs/优化v4/reports/docker-acc-integration-2026-08-18.md`, `docs/优化v4/reports/docker-acc-integration-verify-2026-08-18.md` |
| Evidence level | `integration-tested` (deploy-level — ACC and pocketd on `acc-local-net`) |
| Last verified date | 2026-08-18 |
| Status | **integration-tested** — sufficient for low-risk writes; **not** sufficient for high-risk writes, which need `production-verified`. |

## Memora (`kxmemory-go`, external: same monorepo)

| Field | Value |
|---|---|
| Repo path | `/Users/xutaohuang/workspace/.../kxmemory-go` (single-tenant PG variant) |
| Pinned commit SHA | **NOT YET PINNED** — the same `kxmemory-rls-pg17` instance is used by ACC + pocketd per `docs/优化v4/reports/docker-acc-integration-2026-08-18.md`, but no SHA is captured |
| Endpoint contract reference | `docs/2026-07-02-kxmemory-api-contract.md` (legacy), `docs/新架构v1/02-modules/memory-as-memora.md` |
| Test log reference | `backend/scripts/test-redclaw-integration.sh` |
| Evidence level | `contract-tested` (path-based) |
| Last verified date | 2026-08-18 |
| Status | **contract-tested** |

## LLM providers (multiple)

| Field | Value |
|---|---|
| Repo path | external SaaS / managed providers |
| Pinned commit SHA | N/A (service endpoints) |
| Endpoint contract reference | `docs/2026-07-02-backend-tasks-kxmemory-llmgateway.md`, `docs/新架构v1/02-modules/llm-gateway-integration.md` |
| Test log reference | `docs/优化v4/reports/docker-llm-gateway-pg-switch-2026-08-18.md` |
| Evidence level | `integration-tested` (provider switching under PG schema isolation) |
| Last verified date | 2026-08-18 |
| Status | **integration-tested** for the gateway routing; per-provider availability still depends on the upstream SLA and is **not** covered here. |

## Identity provider (`identity-go`, in-repo)

| Field | Value |
|---|---|
| Repo path | `backend/internal/identity/` |
| Pinned commit SHA | `6892ba5` (merge commit, `feat/identity-go-cross-trust`) |
| Endpoint contract reference | `docs/security/zag-adr-0003-authz-model.md` |
| Test log reference | `docs/优化v4/reports/ios-real-2026-08-18.md`, CI for `identity-go-cross-trust` |
| Evidence level | `integration-tested` |
| Last verified date | 2026-08-19 |
| Status | **integration-tested** |

## Audit (Postgres, in-repo: `feat/audit-pg-hardening`)

| Field | Value |
|---|---|
| Repo path | `backend/internal/audit/` (and PG migrations under `migrations/`) |
| Pinned commit SHA | `25727ba` (current `main`, "fix(audit): production audit hardening") |
| Endpoint contract reference | `docs/security/zag-adr-0007-audit.md`, `docs/audits/` |
| Test log reference | `docs/优化v4/reports/audit-pg-docker-integration-2026-08-18.md`; recent commits: `b2a383b`, `8355fed`, `8104995`, `1a1fee7`, `4f9e253`, `02f943d` |
| Evidence level | `integration-tested` (with PG schema isolation + RLS) |
| Last verified date | 2026-08-21 |
| Status | **integration-tested** |

## WebSocket hub (in-repo)

| Field | Value |
|---|---|
| Repo path | `backend/internal/server/ws_*.go` |
| Pinned commit SHA | current `main` head at time of this row |
| Endpoint contract reference | `docs/WEBSOCKET_REALTIME_UPDATE.md`, `docs/新架构v1/01-architecture/数据流与协议.md`, `docs/superpowers/specs/2026-08-17-task-approval-projection-design.md` |
| Test log reference | none — WS push behavior currently captured only in mobile side commit messages (`7bd8b32`, `e2a399b`, `268fe92`, `7fb782b`) |
| Evidence level | `source-inspected` |
| Last verified date | — |
| Status | **pending-evidence** — a dedicated WS contract test must be captured before the WS hub can be cited at `contract-tested`. |

## `@capacitor/local-notifications`（npm 依赖，P0 移动端本地通知）

| Field | Value |
|---|---|
| Repo path | `frontend/src/composables/useApprovalAlerts.ts`（唯一消费方，指挥中心 needs-input 超 3min 通知 + Deep Link） |
| Pinned version | `@capacitor/local-notifications@8.3.1`（`frontend/package.json`，2026-08-27 安装）；`@capacitor/core`/`android` 对齐 `8.5.0`，`cap sync android` 已注册（`frontend/android/capacitor.settings.gradle`） |
| Endpoint contract reference | 官方插件 API（schedule/cancel/checkPermissions/addListener `localNotificationActionPerformed`）；设计依据 `docs/2026-08-27-mobile-ux-design-v2.md` §4.2-5 |
| Test log reference | none — 未做真机通知触达验证（Android 13+ POST_NOTIFICATIONS 运行时申请在代码内，未验证） |
| Evidence level | `source-inspected` |
| Last verified date | — |
| Status | **pending-evidence** — 真机（vivo X Fold5）验证通知触达与 Deep Link 弹审批 Sheet 后方可提升。 |

备注（设计偏差记录，2026-08-27）：设计方案 v2 §4.2-4 指定经 `plugin_hub.go` 的 `session.stop` 命令通道下发停止。代码核查发现该链路存在协议缺口：`POST /api/plugin/command`（`backend/internal/server/server_plugin_ws.go:146`）按 `{type:<command>, payload}` 下发，而插件（`opencode-plugin/src/index.ts:321`）只识别 `{type:'command', data:<RemoteCommand>}` 信封，二者不匹配且全仓库无调用方。P0 改用已验证的移动端通道 `POST /api/mobile/sessions/:id/interrupt`（`backend/internal/server/mobile_session_handler.go:659` → opencode adapter `InterruptSession` → 上游 `/session/:id/abort`；会话页停止按钮同一链路）。plugin_hub 命令信封的修复或废弃留待 P1 通讯收窄时决策。

## P0 移动端 UX 门禁运行记录（in-repo，2026-08-27）

| Field | Value |
|---|---|
| 覆盖范围 | 指挥中心 P0 全部改动：`health.ts` 五态模型（contract-tested，`frontend/src/features/tasks/__tests__/health.test.mjs` 8 用例）、`useInstanceApprovals.ts`、`useApprovalAlerts.ts`、`TasksView.vue`、`SessionConversationView.vue`、`api/client.ts`（interruptSession）、基线修复（`usePendingApprovals.ts` 签名 ×2、`sqlite-web-init.ts`、`SettingsLLMGateway.vue` 模板闭合） |
| 绿色运行日志 | `test-evidence/P0-mobile-ux/gate-run-2026-08-27.log`（vue-tsc PASS / vite build ✓ / 7 套件 84 用例全 fail=0 / cap sync 4 插件含 local-notifications）；审计报告 `test-evidence/P0-mobile-ux/AUDIT-2026-08-27.md` |
| CI 接线 | `.github/workflows/frontend.yml` 新增 health.test.mjs 步骤；**2026-08-27 审计修正**：CI Node 20 不支持 `.ts` import（ERR_UNKNOWN_FILE_EXTENSION），node-version 已升 "22" 并随审计报告提交，绿色 run 以该次 push 为准 |
| Evidence level | 健康度模型 `contract-tested`；UI 集成与本地通知 `source-inspected`（真机未验证） |
| Status | **contract-tested（仅 health 模型）** — UI/通知链路维持 `implemented (unverified)`，待 vivo X Fold5 真机验证。 |
