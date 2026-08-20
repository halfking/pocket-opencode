# 个人超级终端 (Personal Super Terminal) — 设计稿 v1.0

> **代号**: Pocket SuperTerminal
> **基线项目**: opencode-pocket (Vue 3 + Capacitor 8 + Go pocketd BFF)
> **整合对象**: llm-gateway-go-3、Redclaw、opencode/zcode CLI
> **状态**: 设计已与 owner 多轮 brainstorming 确认，进入实施计划阶段
> **日期**: 2026-07-13
> **作者**: halfking × ZCode brainstorming session

---

## 0. 北极星

把 opencode-pocket 移动端从"AI 编程助手"重构为 **"一人随身公司个人超级终端"**: 个人超级工具 + 记事本 + 会议记录 + 帐本 + 邮件管理 + 任务代码管理 + AI 网关与 Redclaw 管理端 + 平台运营管理端。

双端同时上架 (Android + iOS)，支持 1 人为主 + 偶尔 1–3 人协作。

## 1. 现有资产盘点 (基线)

| 项目 | 形态 | 现状 | 在新终端中的角色 |
|---|---|---|---|
| **opencode-pocket** | Vue 3 + Capacitor 8 Android + Go (pocketd) BFF | 18 路由，5 空壳 (meetings/approvals/relations/resume/notifications)；本地 SQLCipher + AndroidKeyStore ("Lobster 硬壳")；9 语言 i18n；SSE+WS 双通道 | **客户端载体 (要扩展)** |
| **Redclaw** | Python FastAPI 微服务集群 + LLM Router | 完整企业级多租户 AI Agent 平台；OpenAI + Bedrock Converse 双协议；三层路由；RBAC 4 角色 11 权限；**无移动端** | **能力后端 (按需桥接)** |
| **llm-gateway-go-3** | Go 多租户 AI 网关 (SI-LLM-Gateway) | OpenAI/Anthropic/Responses 三兼容 + SSE 流式；智能双层路由；凭据池；RLS 多租户；38+ 表 | **AI 调用底座 (主网关)** |
| **opencode CLI** | Node 单包 (~129MB) + ACP/MCP | serve/web/attach/acp/mcp/run/session/agent/plugin 等子命令；`opencode web` 起 Web UI (非 Electron) | **本地 Agent 引擎 (远端 Agent)** |

> **关键洞察**: 四个项目当前互不打通。协议层"约等于 OpenAI 兼容"但事件信封/路由/租户模型/鉴权各自一套。超级终端的第一件事不是写 UI, 而是**统一一层 BFF/协议适配层**, 把后三个能力收敛成 pocket 移动端可消费的形态。

### 兼容性要点 (实施时必读)
- 后端 `notes` 表**已带 `workspace_id` 列** (DEFAULT 'default'); 其余表 (tasks/emails/vault_sync/opencode_sessions/llm_gateway_configs 等) 需 `ALTER TABLE ADD COLUMN workspace_id TEXT DEFAULT 'default'` 增量迁移
- 前端 `local_workspaces` + `local_notes.workspace_id` **已预留**, 但所有写路径仍写 null — 需补 workspaceId 传入
- `POST /api/opencode/dispatch` **已接受 `task_id`** 但未写回 `task_session_links`; Agent Bridge 接入时补 `attachSession`
- Task 状态字段为 free string, 前端硬编码 `active/blocked/completed` + `high/medium/low` (6+ 处), 重命名需同步
- Task 聚合三源 `local|opencode|acc`; 新设计若引入新源需扩展 enum
- OpenCode V2 envelope 已支持 27 个 `session.next.*` 事件 + 重连续传 (`?after=lastSeq`)
- e2ee 当前覆盖 Notes/Vault; 任务产物附件可复用 `vault_sync` 的 (user_id, blob_ciphertext, version, is_current) 模式
- `/api/mobile/sessions*` `/api/sessions` `/api/opencode/sessions` 三个层级路由命名重叠, 新设计需明确归属

## 2. 整体架构

### 2.1 子产品分层

```
┌─────────────────────────────────────────────────────────────┐
│            S0: 终端底座 (SuperShell)                          │
│  Identity Core · LLM BFF · Lobster Vault · Agent Bridge ·    │
│  Notification Center                                         │
└─────────────────────────────────────────────────────────────┘
                ▲                ▲                ▲
                │                │                │
        ┌───────┴──────┐  ┌──────┴──────┐  ┌──────┴──────┐
        │ S1 个人工具组 │  │ S2 工作流组  │  │ S3 经营管理组│
        │ (单兵作战)    │  │ (日常协作)  │  │ (公司视角)   │
        └──────────────┘  └─────────────┘  └─────────────┘
        • PKM 记事本      • 会议记录       • 帐本 (收付实现制)
          (WYSIWYG+        • 邮件管理       • LLM 网关运营
           反向链接+        (分级自动回复)   • Redclaw 观测+轻控
           全模态)         • Contact 实体   • 平台自助管理
        • 任务代码管理
          (多 Agent +
          多平台 Git +
          CodeUp)
```

### 2.2 系统拓扑

```
                      ┌──────────────────────────────┐
                      │      Pocket Mobile App        │  Vue 3 + Capacitor 8
                      │  (iOS + Android, 双端同时上架) │
                      └──────────────────────────────┘
                  ┌──────┬──────┬──────┬──────┬───────┐
                  ▼      ▼      ▼      ▼      ▼       ▼
                S0-A   S0-B   S0-C   S0-D   S0-E    UI
              Identity  BFF  Vault  Bridge Notify
                  │      │      │      │      │
                  ▼      ▼      ▼      ▼      ▼
                  ┌──────────────────────────────┐
                  │           pocketd BFF          │
                  │  鉴权·同步·事件总线·路由·配额   │
                  │  ·审计                        │
                  └──────────────────────────────┘
            ┌───────┬───────────┬──────────┬──────────┐
            ▼       ▼           ▼          ▼          ▼
       关系型DB   llm-gateway  Redclaw    APNs     SSH/Wol
       (云端)    -go-3 (主)   (按需桥接)  /FCM      → 远端 Agent
```

## 3. S0 — 终端底座

### 3.1 五大子系统

| 子系统 | 单一职责 | 给上层提供的接口 |
|---|---|---|
| **S0-A: Identity Core** | 账号/会话/Token 颁发刷新、邀请撤销、设备指纹 | `IdentityService.login/logout/invite/listDevices/refresh` |
| **S0-B: LLM BFF** | 协议适配 (OpenAI/Anthropic/Responses 三兼容)、流式转发、限流透传 | `LLMClient.chat/stream/embed/usage` |
| **S0-C: Lobster Vault** | 本地加密多模态存储 + 差量同步引擎 + 全文/向量检索 | `Vault.assets/search/upsert/sync` |
| **S0-D: Agent Bridge** | ACP 网关、统一"远端 Agent"管理、与 opencode CLI 互通 | `AgentService.list/send/acpCall` |
| **S0-E: Notification Center** | 通知规则、模板、APNs/FCM token 管理、前台 WS 推送 | `NotifyCenter.subscribe/push/list` |

### 3.2 六大决策 (全部已确认)

1. **身份模型**: 个人 Workspace + 临时邀请
   - `workspace` 表主键 `ws_id`, 默认 `ws_id = me`
   - 邀请最多 3 人 → 映射到隔离的 `shadow_workspace`, 共享部分资源
   - RBAC 简化为 2 级: `owner` / `invitee`
2. **AI 网关**: llm-gateway-go-3 作为唯一出口
   - pocketd 持有 admin token, 移动端永远通过 BFF 间接调用
   - 移动端运营管理端 = llm-gateway 控制面子集
3. **同步范式**: 类型差异化同步
   - `e2ee_local_first` (私密加密数据: 笔记/密码箱/会议录音/账本原始凭证)
   - `cloud_authoritative` (协作型数据: 任务/邮件 tag/共享账本汇总)
   - `cloud_readonly` (只读元数据: 模型列表/网关配置/运营指标)
4. **Lobster 存储**: SQLite + sqlite-vec + AES-GCM blob 文件
   - 上层统一抽象: `Asset(id, kind, meta, blobs[], embeddings[])`
   - iOS 同步: Keychain + 加密 SQLite
5. **推送**: APNs/FCM + WebSocket 混合
   - 前台 WS 直推, 后台/锁屏 APNs/FCM
   - 配 Notification Store + 通知中心 Tab
6. **CLI Bridge**: Agent Cloud Bridge
   - pocketd 暴露 ACP over HTTP/WS, 远端 opencode CLI 包装成统一 `Agent`

### 3.3 关键 Schema

```sql
-- S0-A
workspaces (ws_id, owner_id, name, type, created_at)
workspace_members (ws_id, user_id, role, invited_at, expires_at)
devices (device_id, user_id, ws_id, fingerprint, push_token, os, last_seen)

-- S0-B
llm_gateway_profiles (id, name, endpoint, admin_token_ref, models[], updated_by)
model_usage (id, ws_id, model, prompt_tokens, completion_tokens, cost, ts)

-- S0-C (Lobster 本地表)
assets (id, ws_id, kind, title, body, source, sync_mode, updated_at, deleted_at)
asset_blobs (asset_id, idx, path, size, hash, encryption_meta)
asset_fts (asset_id, body, ts_rank)
asset_vec (asset_id, embedding, model)
sync_log (id, asset_id, op, ts, retries)

-- S0-D
agents (agent_id, ws_id, kind, endpoint, status, capabilities[])
agent_sessions (id, agent_id, status, started_at)
agent_messages (id, session_id, role, content, ts)

-- S0-E
notification_rules (id, ws_id, event_source, event_type, channels, quiet_hours)
notifications (id, ws_id, kind, title, body, payload, read_at, created_at)
```

## 4. S1 — 个人工具组

### 4.1 S1-Notes PKM

**形态**: 完整 PKM (Roam 流派) + WYSIWYG 编辑器 (TipTap) + AI 自动+手动混合 + 全模态输入

**核心模型**:
```
Note
 ├── Block[] (树状, WYSIWYG)
 │    ├── Block (text|image|file|code|quote|callout)
 │    ├── Block / Embed
 │    └── Wikilink [[...]]
 ├── Tags[], Mentions[]
 ├── LinkedNotes[]               ← 反向链接
 └── Sources                     ← voice|share|clipboard|email|clipper|pdf
```

**录入路径 (5 入口)**:
| 入口 | 触发 | 数据流 |
|---|---|---|
| 手写/语音 | "+" 按钮 | voice → STT → Block |
| 图片 | 相册/相机/粘贴板 | image → vision OCR → 多模态 Block |
| Share Extension | 系统分享意图 | receiver → inbox 待整理 |
| 邮件导入 | S2 邮件转发 | mailhook → draft note |
| 网页剪藏/PDF | Safari Action / 文件 | content extraction → 全文+链接 |

**视图**: Today / Daily Note / Graph / Inbox / Search (FTS+向量) / AI Chat (RAG)

**AI 增强**:
- 后台自动: 实时 wikilink 候选 (embedding 相似度) / 空闲时间 tag & cluster 建议
- 用户触发: 摘要 / 润色 / 翻译 / 跨笔记问答 / 闪念合并

### 4.2 S1-Task 任务代码管理

**形态**: 多 Agent 协同 + 多平台 Git (GitHub/GitLab/Gitee/Bitbucket/**CodeUp 阿里云**)

**多 Agent 协同**:
- 一个 task 可拆给多 Agent 同时/顺序工作 (DAG: 开发 → 测试 → 审查)
- 每个 Agent 是独立 ACP server (可以是不同远端机器)
- 复用 `task_session_links`, 扩展 `task_agents` 表

**Code 只读**: PR/MR 列表 + diff viewer + CI 状态, **不做**移动端编辑/commit

### 4.3 数据模型

```sql
-- S1-Notes PKM
notes (id, ws_id, title, summary, cover_blob_id, source, status, deleted_at, ...)
note_blocks (id, note_id, parent_id, order_idx, kind, content_json, attrs, ...)
note_links (id, src_note_id, dst_note_id, src_block_id, kind, auto)
note_embeddings (note_id, model, vec)
note_sources (note_id, kind, ref_id, ref_meta)

-- S1-Task
tasks (id, ws_id, title, description, status, priority,
       source ENUM('local','opencode','acc','agent'), ...)
task_agents (id, task_id, agent_id, role ENUM('planner','developer','tester','reviewer'),
             order_idx, status, started_at, ended_at)
task_artifacts (id, task_id, kind, blob_id, source_agent, ref_id)

-- S1-CodeView
git_providers (id, ws_id, kind ENUM('github','gitlab','gitee','bitbucket','codeup'),
               base_url, oauth_token_ref, nickname)
git_repos (id, provider_id, ws_id, external_id, full_name, default_branch, sync_meta)
git_prs (id, repo_id, external_id, number, title, state, author, head_sha, base_sha, url, updated_at)
git_pipelines (id, repo_id, sha, status, started_at, ended_at, web_url)
```

### 4.4 5 阶段实施路线
| 阶段 | Scope | 周期 |
|---|---|---|
| v1.0 | 单 Agent + 仅 GitHub + Notes 升级为 PKM | 6-8 周 |
| v1.1 | 多 Agent 协同 + Graph + AI Chat | 4 周 |
| v1.2 | 多平台 Git (含 CodeUp) + PR/CI | 4 周 |
| v1.3 | 全模态录入 (PDF/邮件/剪藏) | 6 周 |
| v1.4 | 闪念合并 / 周报 / Daily Note 自动化 | 3 周 |

## 5. S2 — 工作流组

### 5.1 S2-Meetings 会议记录

**形态**: 实时流式转写 + AI Diarization + 完整后处理 (摘要+待办+决议+历史对比)

**流程**:
```
开始会议 → 后台录音 + 流式 STT + 实时显示 chunk
        → 离线兜底 (STT 失败 → 录完整 + 录后 STT)
会议结束 → 后处理流水线:
  1. AI 摘要 → meeting_summaries(kind='summary')
  2. 待办提取 → 创建 Task (source='meeting')
  3. 决议提取 → meeting_summaries(kind='decisions')
  4. 历史对比 → meeting_summaries(kind='insight') — 对比同主题最近 3 场
  → 全部输出作为 Block 汇总到一条 Note (自动 PKM 化)
```

### 5.2 S2-Email 邮件管理

**形态**: 分级自动回复 + Contact 抽取 (Project 靠 Tag)

**分级自动回复**:
- AI 自评 confidence + 类别
- `confidence > 0.9 AND 类别 ∈ {排期, 问候, 库存}` → 自动发
- `confidence < 0.9 OR 类别 ∈ {合同, 财务, 法律}` → 仅草稿
- 强制审计: 发之前落 `email_send_log`

### 5.3 S2-Contact (跨模块实体)

自动从邮件/会议/任务提取联系人 (merge by email), 联系人详情页聚合所有相关邮件/会议/笔记/任务/Timeline。

### 5.4 数据模型

```sql
-- S2-Meetings
meetings (id, ws_id, title, started_at, ended_at, status, source)
meeting_recordings (id, meeting_id, blob_id, sample_rate, format, duration_ms)
meeting_transcripts (id, meeting_id, start_ms, end_ms, speaker_id, text, lang, confidence)
meeting_speakers (id, meeting_id, label, voiceprint_ref, role_hint)
meeting_summaries (id, meeting_id, kind, content, model, created_at)
meeting_action_items (id, meeting_id, task_id)
meeting_note_refs (meeting_id, note_id)

-- S2-Email (扩展)
emails ADD COLUMN ai_reply_draft TEXT, ai_auto_send_enabled BOOL, ai_confidence FLOAT
emails ADD COLUMN thread_root_id UUID, contact_ids UUID[]
email_send_log (id, email_id, sent_at, auto BOOL, model, prompt_tokens, completion_tokens)
email_account_groups (id, ws_id, name, color, account_ids[])

-- S2-Contact (跨模块)
contacts (id, ws_id, primary_email, primary_phone, display_name, avatar_blob_id,
          org, title, source_kind, source_refs JSONB, last_seen_at, created_at)
contact_emails (contact_id, email_id, role)
contact_meetings (contact_id, meeting_id, role)
contact_notes (contact_id, note_id)
contact_tasks (contact_id, task_id)
contact_tags (contact_id, tag_id)
```

## 6. S3 — 经营管理组

### 6.1 S3-Ledger 帐本

**形态**: 邮件+凭证 OCR 导入 + 收付实现制 + AI 分析/草稿/预测 (入账付款人工确认)

**录入流程**:
```
触发源:
  A. 手动 → draft transaction
  B. 邮件 (S2-Email 钩子) → 识别附件 → OCR → draft + voucher_blob
  C. 拍照/截图 (Share Extension) → OCR → draft + voucher_blob
  D. S1-Task 中 Agent 产出 → source='agent' → draft

AI 增强 (异步):
  1. 分类建议
  2. 关联 Contact (counterparty_contact_id)
  3. 异常检测 (金额突增/重复凭证)
  4. 周期识别 → 建议 recurring 规则

人工确认 → status='confirmed' → 写 ledger_audit
```

**现金流预测**: 每周/用户主动触发, LLM + 数值模型, horizon 7/14/30 天, 异常 → 推送告警

### 6.2 S3-Console 运营管理端

**形态**: 移动端轻量 (告警+快速审批+配置快查) + PC 端全功能

**Redclaw 接入**: 观测 + 轻量控制 (start/stop/restart Agent, 改路由配置, 手动重跑任务, 全部落 `redclaw_actions_log`)

**平台运营**: 仅自助管理 (我的 workspace 资源/配额/审计/推送设备/备份)

### 6.3 移动端 vs PC 端 功能矩阵

| 功能 | 移动端 | PC 端 |
|---|---|---|
| Dashboard 概览 | ✅ | ✅ |
| 告警查看 + 一键处置 | ✅ | ✅ |
| 帐本录入/确认 | ✅ | ✅ |
| 凭证 OCR | ✅ (拍照) | ⚪ (上传) |
| 现金流预测查看 | ✅ | ✅ |
| Provider CRUD | ❌ | ✅ |
| 路由规则编辑 | ❌ | ✅ |
| 限流/配额精细配置 | ❌ | ✅ |
| Redclaw Agent 编排 | 观测+轻控 | ✅ 全功能 |
| 审计日志完整查询 | ❌ | ✅ |
| 平台自助 (设备/备份) | ✅ | ✅ |

### 6.4 数据模型

```sql
-- S3-Ledger
accounts (id, ws_id, name, kind, currency, opening_balance, archived_at)
transactions (id, ws_id, account_id, amount, direction, occurred_at, category,
              counterparty_contact_id, note_id, source, voucher_blob_id,
              status, created_at, confirmed_at)
recurring (id, ws_id, amount, direction, cron, counterparty_contact_id, category, note)
budgets (id, ws_id, period, category, limit_amount, used_amount)
cashflow_forecast (id, ws_id, horizon_days, expected_in, expected_out, model, generated_at)
ledger_audit (id, ws_id, tx_id, op, actor, before, after, ts)

-- S3-Console-LLM
llm_provider_status (id, ws_id, gateway_id, provider, model, healthy, latency_p95,
                     error_rate, last_check, mirrored_at)
llm_quota_usage (id, ws_id, gateway_id, period, used_tokens, used_cost, limit_tokens)
llm_alert_rules (id, ws_id, metric, threshold, channel, enabled)
llm_actions_log (id, ws_id, gateway_id, action, payload, result, actor, ts)

-- S3-Console-Ops
redclaw_agents_status (id, ws_id, agent_id, name, status, last_heartbeat,
                       error_count, mirrored_at)
redclaw_tasks (id, ws_id, external_id, agent_id, status, started_at, ended_at,
               error_summary, mirrored_at)
redclaw_actions_log (id, ws_id, target, action, payload, result, actor, ts)

-- S3-Console-Platform
platform_devices (id, ws_id, name, os, push_token, last_seen, trusted)
platform_backups (id, ws_id, kind, size, blob_id, status, created_at, restored_at)
platform_quota (id, ws_id, kind, used, limit, reset_at)
```

## 7. 跨子系统数据流

### 7.1 Contact 作为统一身份
```
S2-Email 收件人 ─┐
S2-Meeting 说话人 ─┼─→ contacts (merge by email/phone)
S1-Task 关联人   ─┤
S3-Ledger 交易对手 ─┘
        │
        ▼
   Contact 详情页
   = TA 所有邮件 + 会议 + 笔记 + 任务 + 交易 + Timeline
```

### 7.2 Note 作为统一沉淀
```
S2-Meeting 摘要     ─→ Note (block 化, source='meeting')
S2-Email 重点邮件   ─→ Note (source='email')
S3-Ledger 大额交易  ─→ Note (反向链接, source='ledger')
S1-Task Agent 产物  ─→ Note (source='agent')
        │
        ▼
   Note 进入 PKM
   = 反向链接 + 向量索引 + RAG 问答
```

### 7.3 Task 作为统一动作
```
S2-Meeting 待办     ─→ Task (source='meeting')
S2-Email 任务提取   ─→ Task (source='email')
S3-Ledger 周期账单  ─→ Task (source='ledger')
S1-Notes 闪念待办   ─→ Task (source='note')
        │
        ▼
   Task 调度
   = 多 Agent 协同 + Git PR 关联 + 通知
```

## 8. 风险登记册

| # | 风险 | 影响 | 缓解 | 子系统 |
|---|---|---|---|---|
| R1 | iOS 后台录音被中断 | 会议丢字 | 双保险: 本地持续 + 上游语音 session | S2-Meetings |
| R2 | WYSIWYG 移动端卡顿 | 体验差 | TipTap Performance mode + 虚拟滚动 | S1-Notes |
| R3 | AI 自动回复发错对象 | 商业损失 | 收件人二次确认 + Thread 对比 + 审计 | S2-Email |
| R4 | OCR 凭证识别准确率 | 账目错误 | 双模型交叉验证 + 低置信度强制人工 | S3-Ledger |
| R5 | 移动端误操作 (重启 Agent/停凭据) | 服务中断 | 危险操作二次确认 + 审计回滚 | S3-Console |
| R6 | llm-gateway Admin Token 泄露 | 全网关失控 | Token 永不出 pocketd; 移动端只走 BFF | S0-B/S3-Console |
| R7 | 联系人合并冲突 | 数据污染 | 用户可手动 split; 保留 audit log | S2-Contact |
| R8 | CodeUp OAuth 境内备案 | 无法上架 | 配置域名白名单 / 第三方代理 | S1-CodeView |
| R9 | 多 Agent 状态机复杂度 | 难维护 | 状态机用 DSL 描述, pocketd 运行时解释 | S1-Task |
| R10 | 现金流预测误导 | 决策失误 | 标注"模型估算"+ 置信区间 | S3-Ledger |
| R11 | iOS SQLCipher 性能 | 启动慢 | 备用 react-native-quick-sqlite + Keychain | S0-C |
| R12 | 跨境数据传输合规 | 法律风险 | 自动回复走境内模型 (网关区域路由) | S2-Email |

## 9. 验收标准 (每子系统)

| 子系统 | MVP 验收 |
|---|---|
| S0-A Identity | 单 workspace 创建/邀请 1 人/设备指纹/Token 刷新 |
| S0-B LLM BFF | chat/stream/embed 三接口打通 llm-gateway-go-3 |
| S0-C Lobster | 多模态 asset 写入 + FTS+vec 检索 + 三种 sync_mode |
| S0-D Agent Bridge | 调起 1 个远端 opencode CLI Agent 跑完一个 task |
| S0-E Notification | APNs + FCM 双端收到推送 + 前台 WS 实时 |
| S1-Notes | WYSIWYG 写 block + `[[wikilink]]` + AI 摘要 + 图片 OCR |
| S1-Task | 多 Agent DAG (开发→测试) + GitHub PR 查看 |
| S1-CodeView | CodeUp OAuth + PR 列表 + diff viewer |
| S2-Meetings | 30 分钟录音 + 实时转写 + Diarization + 摘要/待办/决议 |
| S2-Email | 分级自动回复 (简单自动发, 复杂草稿) + Contact 自动抽取 |
| S3-Ledger | 邮件发票 OCR → draft → 人工确认 + 现金流 7 天预测 |
| S3-Console | Dashboard + 1 键处置告警 + PC 端 provider CRUD |

## 10. 下一步

本设计稿经 owner 多轮 brainstorming 确认。接下来:

1. **本稿提交 git** (docs/superpowers/specs/)
2. **调用 writing-plans skill** 生成分阶段实施计划 (按 S0 → S1 → S2 → S3 顺序, 每个子系统独立 plan)
3. 实施 plan 时, 每个子系统走 TDD: 先写迁移 + 契约测试, 再写 handler, 最后写 UI

---

## 附录 A: Brainstorming 决策日志

| # | 决策点 | 选项 | 日期 |
|---|---|---|---|
| 1 | 产品形态 | 单体超级 App | 2026-07-12 |
| 2 | 协作深度 | 1 人 + 偶尔 1-3 人协作 | 2026-07-12 |
| 3 | AI 网关策略 | 做完整运营管理端 | 2026-07-12 |
| 4 | iOS 支持 | 双端同时上架 | 2026-07-12 |
| 5 | S0 身份模型 | 个人 Workspace + 临时邀请 | 2026-07-12 |
| 6 | S0 AI 网关 | llm-gateway-go-3 唯一出口 | 2026-07-12 |
| 7 | S0 同步范式 | 类型差异化同步 | 2026-07-12 |
| 8 | S0 Lobster 存储 | SQLite + vec + blob | 2026-07-12 |
| 9 | S0 推送 | APNs/FCM + WS 混合 | 2026-07-12 |
| 10 | S0 CLI Bridge | Agent Cloud Bridge | 2026-07-12 |
| 11 | S1 记事本形态 | 完整 PKM (Roam 流派) | 2026-07-12 |
| 12 | S1 PKM 编辑器 | WYSIWYG (TipTap) | 2026-07-12 |
| 13 | S1 PKM AI 增强 | 自动+手动混合 | 2026-07-12 |
| 14 | S1 记事本输入 | 全模态 | 2026-07-12 |
| 15 | S1 Task Scope | Scope 2: 任务+代码只读 | 2026-07-12 |
| 16 | S1 Agent 协同 | 多 Agent 协同 | 2026-07-12 |
| 17 | S1 Git 集成 | 多平台 SaaS (含 CodeUp) | 2026-07-12 |
| 18 | S2 会议含金量 | 实时流式 + Diarization | 2026-07-12 |
| 19 | S2 会议后处理 | 完整级 (摘要+待办+决议+对比) | 2026-07-12 |
| 20 | S2 邮件 AI 回复 | 分级自动 | 2026-07-12 |
| 21 | S2 实体打通 | 抽 Contact | 2026-07-12 |
| 22 | S3 帐本数据源 | 邮件+凭证 OCR | 2026-07-13 |
| 23 | S3 会计制式 | 收付实现制 | 2026-07-13 |
| 24 | S3 帐本 AI 深度 | 分析+草稿+预测 | 2026-07-13 |
| 25 | S3 运营端边界 | 移动端轻量 + PC 全功能 | 2026-07-13 |
| 26 | S3 Redclaw 接入 | 观测 + 轻量控制 | 2026-07-13 |
| 27 | S3 平台运营 | 仅自助管理 | 2026-07-13 |
