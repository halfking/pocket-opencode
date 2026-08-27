# OpenPocket Documentation Status Matrix

> **Last updated**: 2026-08-27（P1.5+ 追加轮：距顶双重 safe-area 修复 + 会话活跃/归档分区 + ScrollChromePortal 目标丢失修复）
> 上一版：2026-08-27（P1 落地轮）— 本轮：**P1.5 界面减负**（`docs/2026-08-27-p1.5-ui-declutter.md`，纯前端）—— ①修 AppLayout 双头 bug（`hideAppHeader` 路由 meta 此前从未被壳层消费 → 会话工作台顶部 4 层条堆叠，真机证据 `test-evidence/P1-mobile-ux/shots/vivo-04..05`）；②自托管 Material Symbols 子集字体（3.8MB→4KB，31 ligature，再生脚本 `frontend/scripts/subset-material-symbols.sh`）——修复工程 15 个文件图标在真机渲染为 ligature 原文（"rrow_back"）的存量缺陷；③SessionStatusBar 重构为动态状态图标（审批呼吸/运行旋转单击停止/空闲播放单击继续，信号即界面 §2.2；DD-1 停止无二次确认已登记）；④头部收敛两行合一（[←][状态图标] 标题+信号副标题 [⋮]）+ 实例信息收进 ⋮ 抽屉；⑤模板 chips 常驻行收进 bolt 快速指令面板（释放整行）。真机验证 V-1..V-8 全 PASS（vivo X Fold5，`test-evidence/P1.5-mobile-ux/device-verification-2026-08-27.md`）；**验证中发现 P1 事件/流层存量缺陷 2 项登记 P2**（SSE 不投递 assistant 输出且无关闭信号→图标被钉死运行态；同文 prompt 重复投递）。底导"⋮ 更多"已由 3e4a9a3 先行满足，本轮不动 BottomNav（并行流 WIP 纪律）。
> 再上一版：2026-08-27（P0 落地轮）—— ①pocketd 事件层：`session.activity`/`round.completed`/`task.health` 三事件（WsEnvelopeV1 + event_id 幂等 + 500ms coalescing + 节流）+ `GET /api/mobile/events/snapshot` 快照追赶端点，Go↔TS 双向 fixture 契约测试 `contract-tested`；②会话工作台：状态条（审批等待/phase 秒表/停止）+ 轮次折叠时间线（round.completed 摘要为权威、消息流降级）+ 详情抽屉（旧页统计/导出并入）+ 旧详情路由 301 兜底（发现旧 opencode 路由从未注册，死链）；③输入系统：SessionComposer（目标 chip/指令模板/停下二次确认/voice 入草稿）+ 每会话草稿 SQLite（`local_drafts`）；④存量红清理：`backend/agent_echo` 误提交 arm64 二进制删除（Linux CI exec format error 根因，stdio 测试改源码现场编译）+ `internal/security` handoff 快照误提交断头 WIP 删除。`task.health` 前端消费登记为 P2。DB/网络/ZAG 健康未变。
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
| Mobile frontend (Vue 3 + Capacitor) | `docs/2026-08-27-mobile-ux-design-v2.md`（产品/交互现行规范）, `docs/2026-08-27-p1-contracts-frozen.md`（P1 契约）, `docs/2026-07-02-ui-ux-design-system.md`（设计系统） | `implemented (unverified)` — P0 已落地（分诊条/健康信号/内联审批/长按控制/本地通知，真机未验证通知链路）；**P1 已落地并真机验证**（vivo X Fold5 integration：会话工作台轮次化/状态条/详情抽屉/Composer/草稿持久化杀进程存活；通知触达与 Deep Link 点击两子项无真实审批触发源维持 unverified）；**P1.5 界面减负已落地并真机验证**（2026-08-27 vivo X Fold5：双头修复/动态状态图标停续 toggle/快速指令面板/⋮ 抽屉收纳/自托管图标字体 4KB——V-1..V-8 全 PASS，`test-evidence/P1.5-mobile-ux/device-verification-2026-08-27.md`；P2-P3 未开工 | P0 健康度模型 `contract-tested`（8 用例，`test-evidence/P0-mobile-ux/gate-run-2026-08-27.log`）；P1 UI **`integration-tested`**（`test-evidence/P1-mobile-ux/device-verification-2026-08-27.md` §9：真实 opencode 会话往返、round.completed 时间线渲染、?prompt= 预填、模板 chip、草稿 force-stop 存活、本地库 33 表全量）；P1 事件消费纯逻辑 `contract-tested`（useSessionEvents 16 + useSessionDrafts 15 + draftStore 4 + fixture 6，`test-evidence/P1-mobile-ux/gate-run-2026-08-27.log`） | `NAVIGATION_ARCHITECTURE.md`, `MOBILE_ARCHITECTURE_V2.md`, `docs/2026-07-03-mobile-interaction-optimization.md`（均 superseded） | — | `frontend/src/features/tasks/{health.ts,useInstanceApprovals.ts,TasksView.vue}`, `frontend/src/composables/useApprovalAlerts.ts`, `frontend/src/features/sessions/{SessionConversationView.vue,SessionStatusBar.vue,RoundTimeline.vue,SessionDetailDrawer.vue,SessionComposer.vue,useSessionEvents.ts,useSessionDrafts.ts}`, `frontend/src/native/{draftStore.ts,local-db.ts(splitSqlStatements 接线——真机修复),schema.ts(local_drafts)}`, `frontend/src/services/sessionEvents.ts`。停止通道：`api.interruptSession` → `POST /api/mobile/sessions/:id/interrupt`（plugin_hub `session.stop` 信封协议缺口见 EVIDENCE-LEDGER 备注）。`task.health` 前端消费 P2。P2 登记：旧库升级无迁移机制、同路由组件复用串台、**DEFECT-P2-SSE-1**（SSE 不投递 assistant 输出且无关闭信号→isStreaming 卡真，P1.5 真机发现，证据 §4）、**DEFECT-P2-DUP-1**（同文 prompt 重复投递，存疑）。P1.5 证据：信号三态纯函数 `contract-tested`（+3 用例，gate log §1/§4 `test-evidence/P1.5-mobile-ux/gate-run-2026-08-27.log` 193/193）；UI `integration-tested`（V-1..V-8 vivo 截图 `shots/vivo-p15-01..06.png`）；设计决策 DD-1..6 `docs/2026-08-27-p1.5-ui-declutter.md`。**P1.5+ 追加轮**：①safe-area 唯一来源纪律（详情页头部距顶 90→39px，/ai 标题 78→51px，V-9/V-10）；②会话活跃/归档分区（默认活跃+分段切换+左滑恢复，sessionArchive.ts 统一存储+旧 key 迁移，V-11..V-14；DD-7 归档语义与 Acc 关联边界）；③**存量 bug 修复：ScrollChromePortal 的 `#app-chrome-sub` 目标被重构丢失→五视图工具栏静默不渲染**（恢复目标元素，V-15）；证据 `test-evidence/P1.5-mobile-ux/device-verification-2026-08-27.md` §7 + `shots/vivo-p15-07..10.png` |
| pocketd 会话事件层（`session.activity`/`round.completed`/`task.health` + snapshot） | `docs/2026-08-27-p1-contracts-frozen.md` §2/§3（冻结契约）, `docs/2026-08-27-mobile-ux-design-v2.md` §5.1/§5.2 | `integration-tested` | `integration-tested` | — | — | `backend/internal/opencode/session_event_broadcaster.go`（+`_test.go` Go fixture；**30s 周期重扫**——真机发现启动单扫漏晚启实例）, `backend/internal/adapter/opencode_http.go`（**OpenCodeEvent.UnmarshalJSON 信封归一化**——真机发现 v1.14.33 `{type,properties}` 漂移，回归测试 `opencode_event_envelope_test.go`）, `backend/internal/server/mobile_events_handler.go`, `frontend/src/services/sessionEvents.ts`（+TS fixture，Go↔TS 双向锁定）；绿色日志 `test-evidence/P1-mobile-ux/gate-run-2026-08-27.log`；**真机集成证据**（vivo X Fold5 + 真实 opencode：round.completed 到达时间线渲染）`test-evidence/P1-mobile-ux/device-verification-2026-08-27.md` §9–§10 |

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
