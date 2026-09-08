# OpenPocket 需求与设计（Reverse-engineered As-built）

**日期**: 2026-09-08  
**状态**: 现行方案（as-built reverse-engineered plan）  
**上游盘点**: [`2026-09-08-feature-inventory.md`](./2026-09-08-feature-inventory.md)  
**取代关系**: 取代 `MOBILE_ARCHITECTURE_V2.md` 的"双屏布局 / 双屏检测 / 双屏 Presentation API"段落（与 v2 移动 UX 不冲突，本方案补齐 native 层）；其它专题以原文档为准：
- 移动 UX 总纲 → [`2026-08-27-mobile-ux-design-v2.md`](./2026-08-27-mobile-ux-design-v2.md)
- 会议工作台 → [`2026-09-08-meetings-studio.md`](./2026-09-08-meetings-studio.md)
- 邮件流水线 → `docs/2026-09-06-email-local-invoice.md`
- 部署 & 真机切流 → `docs/2026-09-07-local-cutover/`
- 主密码 / 生物认证 → `docs/2026-09-08-master-password-biometric-unlock.md`

---

## 1. 反向整理的需求（按用户故事组织）

### 1.1 角色与场景

| 角色 | 核心场景 | 一句话痛点 |
|---|---|---|
| **远程驾驶员**（开发者） | 白天碎片时间用手机看任务、批权限、回 AI 提问 | 想 30s 内知道"该不该管"，两步内处理 |
| **会议记录员** | 开会时录音，自动转写、总结、下待办 | 一边讲一边还要打字 |
| **邮件运营 / 财务** | 收件箱每天来几十封，垃圾邮件 / 发票分开 | 发票要归类、要 PDF、要有汇总 |
| **跨设备使用者** | 家中电脑 + 手机 + 折叠屏，临时切换形态 | 不能因为换设备就丢上下文 |
| **系统集成方** | ACC / RedClaw / kxmemory / 飞书 | 要契约、要审计、要可观测 |

### 1.2 功能性需求（Functional）

> 每条带 **优先级 / 验收锚点 / 证据** 三栏。优先级：P0 必须有 / P1 应该 / P2 可以 / P3 视情况。

#### 1.2.1 AI 编排与指挥

| ID | 需求 | 优先级 | 验收锚点 | 证据 |
|---|---|---|---|---|
| F-AI-01 | 一屏看见"有没有事需处理" | P0 | ≤3s 内冷启动到分诊条，扫描一眼 | `TasksView.vue` 分诊条 |
| F-AI-02 | 待审批 / 提问两步内处理 | P0 | 从通知/列表 → 完成 ≤2 tap ≤5s | `usePendingApprovals.ts` + outbox |
| F-AI-03 | 健康度信号统一 5 态 | P0 | needs-input > stalled > error > running > idle | `docs/2026-08-27-mobile-ux-design-v2.md §4.1` |
| F-AI-04 | 多轮对话流式 | P0 | 模型切换 / 流式 / 草稿 / 历史 | `chatAgentStore.ts`, `llm-bff.ts` |
| F-AI-05 | 智能体角色库 | P1 | 列表 / 创建 / 编辑 / 详情 / 同步 | `agents/*View.vue` |
| F-AI-06 | 成本 / 配额可视化 | P1 | 用量 + 上限 + 告警 | `CostQuotaView.vue` |

#### 1.2.2 个人助理七件套

| ID | 需求 | 优先级 | 验收锚点 | 证据 |
|---|---|---|---|---|
| F-NOTE-01 | 录音 → STT → 笔记 | P0 | ≤3s 反馈 VAD 分段；离线草稿不丢 | `NoteRecordingStudio.vue` + VAD |
| F-NOTE-02 | 全文检索 / 摘要 | P1 | FTS5 + 服务端 search_text 重算 | `notes/*` `b25c03d` commit |
| F-NOTE-03 | 知识库双向链接 | P2 | TipTap + Backlinks 面板 | `pkm/PkmEditor.vue` |
| F-EMAIL-01 | 真实 IMAP 同步 | P0 | OAuth / IMAP / 增量 UID 准确 | `email/*` `87c8b55` 等 |
| F-EMAIL-02 | 垃圾批量清理 | P1 | 条件过滤 + 一键移入垃圾箱 | `EmailSpamCleanupView.vue` |
| F-EMAIL-03 | 发票识别 + A4 网格 PDF + 飞书推送 | P0 | 单份合并 + LWW 兜底 + PDF 飞书 | `docs/2026-09-06-email-local-invoice.md` |
| F-FIN-01 | 发票自动入账 + 时区化统计 | P0 | 财务 PG 记账 + 时区 + 分类 | `finance/*` |
| F-VAULT-01 | 凭据加密存储 + 主密码 | P0 | Keystore + 生物认证免密 | `vault/*`, `keystore.ts`, `biometricAuth.ts` |
| F-MTG-01 | 听见式录音 + 实时转写 | P0 | 前台 mic + VAD + 句级转写 | `meetings/*`, `background-mic.ts` |
| F-MTG-02 | 一键总结 / 待办提取 / ACC 下达 | P0 | meeting-skills + `scheduledTasksApi.create` | `MeetingStudioMenu.vue`, `meeting-page-actions.ts` |
| F-TASK-01 | 计划任务（含 ACC） | P1 | 创建 / 详情 / 提示词优化 / 触发 | `scheduled-tasks/*View.vue` |

#### 1.2.3 网关 / 设置 / 协作

| ID | 需求 | 优先级 | 验收锚点 | 证据 |
|---|---|---|---|---|
| F-GW-01 | LLM 网关管理（提供商 / 凭据 / 模型 / 节点 / 路由） | P0 | 8 个页面 / cipher mask / SSRF 防护 | `gateway/*` `495+177` lines |
| F-GW-02 | 用户设置双写主库 + 切到 kaixuan 网关 | P0 | `user_settings` 走主库 / `170f992` | `usersetting/*` |
| F-AUTH-01 | 主密码 / SSO / 生物认证免密 | P0 | 5 个 auth 页面 + `6348bf9` 提交 | `auth/*` |
| F-SYNC-01 | 移动端连远程后端（用户配置连接） | P0 | 同源服务器切换 + 连接探测 | `fa8517f` 提交 |
| F-CHAT-01 | 多会话 AI 对话 / 工作台 | P1 | 会话聚合 + round timeline + FAB | `sessions/*View.vue` `48b406c` |
| F-OC-01 | OpenCode 实例 / 任务管理 | P1 | 兼容层 + 任务分组 | `opencode/*`, `tasks/*` |

### 1.3 非功能性需求（Non-functional）

| 维度 | 目标 | 衡量 |
|---|---|---|
| **冷启动** | ≤3s（缓存态首屏） | Web 端 cold start + 首屏 LCP |
| **API 响应** | P95 < 100ms | `pprof` + Echo metric |
| **WebSocket 稳定** | ≥3h 无断开 | 心跳 + 自动重连 |
| **离线入队** | 审批 / 输入 / 上传 离线不丢 | outbox 表 + 幂等键 |
| **可观测性** | 审计 100% 入库；本地 + 远程双写 | `audit_writer*.go` 5 个域测试 |
| **多租户隔离** | 默认按 workspace 强隔离 | 例外仅 `redclaw/file_exporter.go` |
| **安全性** | JWT 24h + Refresh / Keystore / SSRF 防护 / 限速 / 幂等 | `SECURITY_FIXES_2026_08_14.md` |
| **可观测的 PII 处置** | 审计 Detail 字段写入边界有文档化 | `AUTH_REDCLAW_CURRENT.md` |
| **类型安全** | 前端 typecheck + 后端 `go vet` / `go test` | GitHub Actions + 本地 `make test-pg` |
| **跨端代码共享** | 一套 Vue 业务代码 100% 复用 | `frontend/src` 跨端编译 |
| **原生壳可替换** | 业务代码不感知壳 | 通过 `runtime-platform.ts` 能力探测 |

### 1.4 约束与边界

- **不实现**：自家 IMAP / SMTP 全协议栈、RedClaw 商业版、生产级推送网关、不做 Flyme / MIUI 深度定制
- **暂缓**：iOS / HarmonyOS 业务全功能、cap-sherpa 原生流式 STT、ECAPA AAR 落 iOS、思维导图、声纹库管理页
- **必须保留**：会议隐私边界（本地优先 + 元数据上云）；审计 PII 例外的可视化说明；与 ACC / kxmemory / RedClaw 的契约不退化

---

## 2. 设计（Architecture & Module Contracts）

### 2.1 分层

```
┌────────────────────────────────────────────────────────┐
│  Native Shell  (Android ✓ / iOS ⏳ / HarmonyOS ⏳)       │
│  Capacitor 7 · 平台 Plugin · WebView 容器               │
└────────────────────────────────────────────────────────┘
                            ↕  Capacitor bridge
┌────────────────────────────────────────────────────────┐
│  Vue 3 Web App  (一套业务代码)                          │
│  ├ pages / features/<domain>/                          │
│  ├ stores (Pinia: 14 个)                               │
│  ├ api (HTTP/WS/SSE 26 个)                             │
│  ├ native/  (原生能力适配层 35+ 文件)                   │
│  └ composables/  (跨域复用逻辑)                         │
└────────────────────────────────────────────────────────┘
                            ↕  HTTPS / WSS / SSE
┌────────────────────────────────────────────────────────┐
│  Go Backend (pocketd)                                  │
│  ├ Echo HTTP + gorilla/websocket                       │
│  ├ internal/server/* (130+ handler)                    │
│  ├ internal/<domain>/*  (按业务内聚)                   │
│  ├ Agent Bridge (ACP / Codex / Claude / LocalAgent)    │
│  └ Audit + Quota + Notify + kxmemory Client            │
└────────────────────────────────────────────────────────┘
                            ↕  (内部网 / 跨网)
┌────────────────────────┐  ┌─────────────────────────────┐
│  PostgreSQL            │  │  External Services          │
│  (r112_pg / llm-pg)    │  │  ACC · kxmemory · RedClaw   │
│  SQLite (本地优先)      │  │  LLM Gateway · 飞书 · FCM    │
└────────────────────────┘  └─────────────────────────────┘
```

### 2.2 关键契约（Module Contracts）

| 契约 | 说明 | 验证方式 |
|---|---|---|
| **OpenCode 兼容层** | `internal/opencode/*` + `frontend/src/api/opencode.ts` | `opencode-contract.md` + 契约测试 |
| **ACP JSON-RPC 2.0** | `internal/agent/*` stdio | `test_acp_stdio_real/` |
| **移动 API（mobile_api.go）** | 移动端统一入口，所有域走 `requiresAuth + device` | `mobile_api_isolation_test.go` |
| **WS 事件总线** | `internal/websocket/{hub, mobile_hub, plugin_hub}.go` + 前端 `idempotentWsBus` | 幂等键 + 心跳 |
| **审批事件** | `approval.permission/question.pending/resolved` | 前端 `approvalEvents.ts` |
| **邮件 LWW** | 账户配置按 Last-Write-Wins 双写 | `email/*` LWW 单元测试 |
| **审计** | `audit_writer*.go` 5 域写入 + Postgres / SQLite 双栈 | `audit_pg_test.go` 等 5 测试 |
| **MCP Bearer** | MCP 服务鉴权 + 401/400 结构化判定 | `audit` 修复 commit |
| **调度任务** | `scheduledTasksApi.create(kind=redclaw_chat, maxRuns=1)` | `meeting-page-actions.ts` |

### 2.3 关键数据流（4 个核心场景）

#### A. 指挥中心分诊

```
WS 事件 → idempotentWsBus → usePendingApprovals → TasksView 分诊条
                                             ↘ approvalStore (outbox 入队)
WS activity → session.activity / round.completed → 健康度聚合 → TasksView L1
```

#### B. 会议听见式

```
mic (前台服务 / WebAudio) → VAD 分段 → sherpa ECAPA 说话人 → ingestSpeechBlob
      → 云端 STT → local_meetings.segments → 转写滚动
UI 「总结」按钮 → meeting-skills (LLM) → 纪要 / 待办 (local_todos)
                 → ACC: scheduledTasksApi.create → pocketd → kxmemory
```

#### C. 邮件发票闭环

```
IMAP 抓取 (POP3/textproto 已修) → 解析 → kxmemory 分类 → local_emails
                            ↘ 发票 → A4 网格 PDF (pdfcpu+fpdf)
                                   → 飞书推送
                                   → 共享目录兜底
                                   → finance 入账 (PG + 时区)
```

#### D. 离线审批

```
用户点「批准」→ approvalStore 写 outbox → 即时显示「⏳ 待发送」
online 后 → outboxDrain → idempotent key → server 幂等响应 → UI 收敛
冲突（409）→ 标记 re-confirm / 重新拉取
```

### 2.4 一致性 & 状态管理

| 层 | 机制 | 备注 |
|---|---|---|
| 前端本地 | SQLite (native) / sql.js (web) | outbox / draft / asset / 自增 ID |
| 会话内 | Pinia reactive | `auth / session / opencode / chatAgent / approval` 等 14 个 |
| 后端进程内 | sync.Map + Mutex | WebSocket Hub |
| 跨实例 | PostgreSQL / Redis（按部署） | 不在仓内实现 |
| 跨设备 | WebSocket + Last-Write-Wins | 邮箱 / 用户设置已用 |

---

## 3. 路线图（合并自各专题文档）

| 期 | 内容 | 来源 | 验收 |
|---|---|---|---|
| **v1.0** 已完成 | 后端 + Web + Android 骨架 | `README.md` | E2E 100% |
| **v1.1** 已完成 | ACP stdio / Session / 权限 / WS 定向广播 | `v1.1 commit` | 全联通 |
| **v1.2** 进行中 | 邮件流水线 + 发票 + 财务 + AI 工具控制 | `166ca96` 起 20+ commit | 文档 + 自动化测试 |
| **v2.0** 规划中 | iOS 主力壳 / 折叠屏形态层 / FCM+APNs | 本文档 §4 | 见 §4 |
| **P3 运维依赖** | FCM / APNs / 证书 | mobile-ux v2 §6 | 后台免开 App |

---

## 4. 风险 & 缓解

| 风险 | 描述 | 缓解 |
|---|---|---|
| **iOS / HarmonyOS 业务全断** | 仅 Web 兜底 | 见 [`2026-09-08-native-and-cross-platform.md`](./2026-09-08-native-and-cross-platform.md) §3-§5 |
| **PII 审计例外** | RedClaw 全租户落盘 | 文档化 + 运维目录权限 + 已知例外表 |
| **多 agent 协议碎片** | ACP / Codex / Claude / LocalAgent 并存 | agent bridge 统一 + 契约测试 |
| **PG ↔ SQLite 双栈漂移** | 两套 schema | migration 包 + 测试隔离 schema |
| **离线一致性** | outbox 与服务端的版本冲突 | 幂等键 + re-confirm 流 |
| **会议录音隐私** | 长时前台 mic + 切后台 | Android: ForegroundService + 必要权限 |

---

## 5. 取代与归档

| 文档 | 处置 |
|---|---|
| `MOBILE_ARCHITECTURE_V2.md` 双屏布局章节 | 已 superseded（v2 移动 UX 取代）；本文补齐 native 切片 |
| `OPENCODE_MOBILE_MANAGEMENT_PLAN.md`（root） | 已归档（需求吸收进 §1） |
| `NAVIGATION_ARCHITECTURE.md`（root） | 已归档（路由以 `router-mobile.ts` 为准） |

