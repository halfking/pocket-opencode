# OpenPocket 功能特性大盘（As-built Inventory）

**日期**: 2026-09-08  
**状态**: 现行代码盘点（source-inspected）  
**目的**: 把仓库里已经存在的功能特性摊开成一张表，作为后续需求/设计/原生化讨论的唯一事实层  
**扫描范围**：`frontend/src/{features,pages,stores,api,native}`、`backend/internal/**`、`docs/`、`deploy/`

---

## 0. 阅读约定

- **Feature 域**：前端 `src/features/<name>/` 一个文件夹即一个域，对应业务能力切片
- **后端模块**：`backend/internal/<name>/` 一个文件夹即一个微服务内聚模块，对应能力后端实现
- **能力位（Cap）**：原生 / Web 共享的"系统能力接入点"，归在 `frontend/src/native/`
- **状态记号**：
  - ✅ 已落地（仓库中存在并被引用）
  - 🟡 部分落地（接口有但仅 Android / Web / 兜底一条腿）
  - ⏳ 占位（SPA/H5/Compat 配置存在但暂未连通）
  - ❌ 未实现
- **证据**：列具体路径 + 关键文件，避免空口声明

---

## 1. 业务 Feature 域（前端 24 个）

### 1.1 AI & 编排（5）

| 域 | 已落地能力 | 入口路由 | 主要文件 | 状态 |
|---|---|---|---|---|
| `ai`（AI 工具控制） | 任务聚合看板 / 分诊条 / 卡片内联操作 / 长按控制 / 健康度信号 | `/ai` | `features/tasks/TasksView.vue`、`composables/usePendingApprovals.ts` | ✅ |
| `ai-chat`（多轮对话） | 流式对话 / 模型选择 / 多会话 / 指令优化 / 角色切换 | `/ai-chat` | `features/ai-chat/AIChatView.vue`、`stores/chatAgentStore.ts`、`api/chatAgent.ts`、`api/llm-bff.ts` | ✅ |
| `agents`（智能体库） | 角色列表 / 创建 / 编辑 / 详情 / 同步到 kxmemory | `/agents`, `/agents/:id`, `/agents/:id/edit` | `features/agents/{AgentLibrary,AgentDetail,AgentEdit}View.vue`、`api/agents.ts` | ✅ |
| `marketplace`（市场） | skill / agent / workbuddy 三大懒加载入口 | `/market/{skills,agents,workbuddy}` | `features/marketplace/{SkillMarket,AgentMarket,Workbuddy}View.vue` | ✅ |
| `cost`（成本/配额） | 配额展示 / 用量统计 / 上限提示 | `/cost` | `features/cost/CostQuotaView.vue`、`api/llm-bff.ts` | ✅ |

### 1.2 个人助理（7）

| 域 | 已落地能力 | 入口路由 | 主要文件 | 状态 |
|---|---|---|---|---|
| `notes`（语音笔记） | 录音 → STT → 笔记；列表 / 详情 / 编辑 / 全文检索 / 摘要 / 附件 | `/notes`, `/notes/:id`, `/notes/new` | `features/notes/*.vue`、`api/notes.ts`、`stores/opencode.ts` | ✅ |
| `pkm`（知识库） | TipTap WYSIWYG / 双向链接 / 今日 / 详情 / Backlinks 面板（懒加载） | `/pkm`, `/pkm/note/:id` | `features/pkm/*.vue`、`native/asset-store.ts` | ✅ |
| `email`（邮箱流水线） | 账户接入 OAuth / IMAP / 收件箱 / 详情 / 摘要 / AI 翻译 / 垃圾清理 / 发票识别导出 | `/email/*`（6+ 页面） | `features/email/*.vue`、`api/email.ts`、`api/email-cleanup.ts` | ✅ |
| `finance`（财务记账） | 发票入账 / 统计 / 时区化 / 分类 | `/finance` | `features/finance/FinanceView.vue`、`api/finance.ts` | ✅ |
| `vault`（密码箱） | 凭据加密存储 / 列表 / 详情 / 生物认证解锁 | `/vault`, `/vault/:id` | `features/vault/*.vue`、`api/vault.ts`、`native/biometricAuth.ts`、`native/keystore.ts` | ✅ |
| `meetings`（会议工作台） | 听见式录音 / 实时转写 / 总结 / 待办 / ACC 下达 | `/meetings/*`（13+ 组件） | `features/meetings/*.vue`、`api/meetings.ts`、`composables/useMeetingRecorder.ts` | ✅ |
| `scheduled-tasks`（计划任务） | 列表 / 详情 / 编辑 / 提示词优化 / 计划字段 / ACC 调度 | `/scheduled-tasks/*` | `features/scheduled-tasks/*.vue`、`api/accTasks.ts` | ✅ |

### 1.3 协作 & 通讯（3）

| 域 | 已落地能力 | 入口路由 | 主要文件 | 状态 |
|---|---|---|---|---|
| `contact`（联系人） | 联系人列表 / 详情 | `/contacts`, `/contacts/:id` | `features/contact/*View.vue` | ✅ |
| `chat`（聊天） | 会话聚合 / 多轮对话（盘古 chatagent） | `/sessions`, `/sessions/:id` | `features/sessions/*.vue`、`stores/chatAgentStore.ts` | ✅ |
| `opencode`（会话兼容层） | OpenCode Hub / Session List / Detail（向 pocketd 兼容旧式页面） | `/opencode/*` | `features/opencode/*.vue`、`api/opencode.ts` | ✅ |

### 1.4 基础设施 & 平台（9）

| 域 | 已落地能力 | 入口路由 | 主要文件 | 状态 |
|---|---|---|---|---|
| `auth`（认证） | 登录 / 注册 / 忘记密码 / SSO 回调 / 主密码对话框 / 生物认证免密 | `/login`, `/register`, `/sso/callback` | `features/auth/*.vue`、`stores/auth.ts`、`api/auth.ts` | ✅ |
| `tasks`（任务域） | 任务详情 / 会话筛选 / Session sheet / Session panel / 转写 | `/tasks/:id`, `/tasks` | `features/tasks/*.vue`、`api/opencode.ts` | ✅ |
| `instances`（实例管理） | 多实例浏览 / 健康检测 | `/instances` | `features/instances/InstanceListView.vue` | ✅ |
| `servers`（服务器选择） | 后端服务器切换 / 登录前服务器探测 | `/server-select` | `features/servers/ServerSelectView.vue` | ✅ |
| `gateway`（LLM 网关） | 提供商 / 凭据 / 模型 / 节点 / 路由 / 概览 / 实时流（8 个页面） | `/gateway/*` | `features/gateway/*.vue`、`api/gateway*.ts` | ✅ |
| `settings`（设置） | 总设置 / 网关设置 / 权限设置 | `/settings`, `/settings/llm-gateway`, `/settings/permissions` | `features/settings/*.vue`、`api/user-settings.ts` | ✅ |
| `imports`（导入） | 数据导入入口 | — | `features/imports/` | ✅ |
| `config`（配置列表） | 动态配置展示 | — | `features/config/ConfigList.vue` | ✅ |
| `common`（通用） | ComingSoon 兜底页 | — | `features/common/ComingSoonView.vue` | ✅ |

---

## 2. 后端能力模块（46 个 internal 包）

| 模块 | 关键职责 | 关键文件 | 状态 |
|---|---|---|---|
| `server` | Echo HTTP 入口 / 中间件 / 移动 API / SSO / 审计 / 限额 / 通知 / LLM BFF / 邮件流水线 / 财务 / 网关代理 / 计划任务 / 会议 / 笔记 / 凭据箱等 130+ handler | `server.go`, `mobile_api.go`, `server_*.go` | ✅ |
| `auth` | JWT 签发 / Refresh / SSO 状态 / 主密码 / 生物认证 | `auth_*.go` | ✅ |
| `agent` | ACP/Codex/Claude 多适配器 / Session / Permission / Question / Streaming | `agent/`, `adapter/` | ✅ |
| `agentbridge` | 跨实例代理桥 | — | ✅ |
| `opencode` | OpenCode HTTP 客户端 / 事件流 / WS Hub / 兼容层 | `opencode_*.go`, `mobile_events_handler.go` | ✅ |
| `websocket` | hub / mobile_hub / plugin_hub（定向广播） | `hub.go`, `mobile_hub.go`, `plugin_hub.go` | ✅ |
| `email` | 收件箱 / IMAP 同步 / 发票 / 飞书推送 / 垃圾清理 / 账户 LWW | 51 个文件 | ✅ |
| `finance` | 发票入账 / 统计 / 时区化 | 13 个文件 | ✅ |
| `meeting` | 会议元数据 / 录制元数据 / 总结 / 待办 / ACC 转交 | 6 个文件 | ✅ |
| `notes` | 笔记 CRUD / 媒体引用 / FTS | 5 个文件 | ✅ |
| `vault` | 凭据箱加密存储 | 4 个文件 | ✅ |
| `task` | 任务模型 / 磁盘回退 / 同步 | 5 个文件 | ✅ |
| `tasksync` | 任务同步（Cursor / OpenCode / ZCode 落盘会话回灌） | 5 个文件 | ✅ |
| `scheduledtask` | 计划任务 / 提示词优化 / 审计桥 | 13 个文件 | ✅ |
| `marketplace` | Skill / Agent / Workflow 注册与下载 | 11 个文件 | ✅ |
| `chatagent` | 盘古多轮 chat / 流式 / 模型选择 | 14 个文件 | ✅ |
| `chat_summary` | 对话摘要 | 7 个文件 | ✅ |
| `llmbff` | 多模态 LLM BFF（OpenAI 兼容 / RedClaw 适配） | 5 个文件 | ✅ |
| `llmgateway` | LLM 网关解析 / 节点 / 凭据 / 限流 | 4 个文件 | ✅ |
| `aigate` | 嵌入 / LLM 无状态代理 | 1 个文件 | ✅ |
| `kxmemory` | kxmemory 客户端（AI 分类 / 摘要 / 提取） | 2 个文件 | ✅ |
| `lobster` | 龙虾初始化 / 配置同步 | 2 个文件 | ✅ |
| `localagent` | 本地小模型（Ollama 等）代理 | 2 个文件 | ✅ |
| `zagclient` | ZAG 服务调用 client | 3 个文件 | ✅ |
| `acchttp` | ACC HTTP 客户端 | 2 个文件 | ✅ |
| `feishu` | 飞书 Webhook / 卡片推送 | 3 个文件 | ✅ |
| `mcp` | MCP 协议服务器 / Bearer 鉴权 / 401/400 判定 | 5 个文件 | ✅ |
| `orchestrator` | 多 agent 编排 | 3 个文件 | ✅ |
| `presentation` | PPT / 海报 / 文档演示生成 | 6 个文件 | ✅ |
| `snippet` | 代码片段 | 3 个文件 | ✅ |
| `quota` | 用量配额 / 计费 | 7 个文件 | ✅ |
| `redclaw` | RedClaw 集成 / FileExporter（已知隔离例外） | 26 个文件 | ✅ |
| `notify` / `notifycenter` | 通知中心 / 多通道推送 | 3 + 3 个文件 | ✅ |
| `notification` | 通知统一抽象 | 1 个文件 | ✅ |
| `stt` | STT 抽象（云端 STT / sherpa 占位） | 1 个文件 | ✅ |
| `updates` | 升级检查 | 1 个文件 | ✅ |
| `model` | 模型元数据 | 1 个文件 | ✅ |
| `usersetting` | 用户设置（双写主库 / 切到 kaixuan 网关） | 6 个文件 | ✅ |
| `identity` | 身份抽象 | 2 个文件 | ✅ |
| `facade` | 业务门面聚合 | 5 个文件 | ✅ |
| `migration` | 数据迁移（SQLite↔PG） | 3 个文件 | ✅ |
| `db` | 数据库抽象 / PG+SQLite 双栈 | 2 个文件 | ✅ |
| `config` | 配置加载 | 2 个文件 | ✅ |
| `registry` | 实例/插件注册中心 | 6 个文件 | ✅ |
| `audit_writer` / 审计存储 | 审计写入器（按域拆 5+ 测试） | `audit_writer*.go`, `audit_*.go` | ✅ |

---

## 3. 原生能力层（`frontend/src/native/` 共 35+ 文件）

| 能力 | 文件 | 用途 | Android | iOS | HarmonyOS |
|---|---|---|---|---|---|
| 本地数据库（SQLite / sql.js） | `local-db.ts`, `sqlDb.ts`, `sqlite-web-init.ts` | 离线持久化、outbox、笔记缓存 | ✅ 原生 SQLite | 🟡 sql.js Web 兜底（缺原生或写） | 🟡 sql.js Web |
| Keystore / Keychain 加密 | `keystore.ts`, `crypto.ts`, `crypto-config.ts` | 主密码 / vault / biometric 私钥 | ✅ AndroidKeyStore | 🟡 Keychain 占位 | ❌ |
| 生物认证 | `biometricAuth.ts`, `biometric-errors.ts`, `capabilities.ts` | 免密解锁 / 操作确认 | ✅ | 🟡 LocalAuthentication 占位 | ❌ |
| 前台麦克风服务 | `background-mic.ts`, `meeting-audio.ts` | 会议录音 / 说话人分段 | ✅ ForegroundService | 🟡 AVAudioSession 占位 | ❌ |
| 离线 outbox | `outboxStore.ts`, `outboxDrain.ts`, `mobileOffline.ts`, `mobileSync*.ts` | 操作离线入队 / 重放 | ✅ | ✅ 通用实现 | 🟡 通用 |
| 草稿持久化 | `draftStore.ts` | 笔记 / 邮件 / 对话草稿 | ✅ | ✅ | 🟡 |
| 资产 / 媒体 | `asset-store.ts`, `list-sync/*`, `config-sync/*` | 图片 / 录音 / 文件双向链接 | ✅ | ✅ | 🟡 |
| VAD / 端点检测 | `vad-segmenter.ts` | 录音分段（≈1.5s 静音） | ✅ | ✅ WebAudio | 🟡 |
| 说话人 | `speaker-diarization.ts`, `speaker-embedding.ts`, `sherpa.ts` | ECAPA 嵌入 / 聚类 | ✅ sherpa AAR | 🟡 通用算法 | ❌ |
| 语言检测 | `detect-lang.ts` | 录音语言识别 | ✅ | ✅ | 🟡 |
| 平台运行时 | `runtime-platform.ts`, `util.ts` | 设备能力探测 / util | ✅ | ✅ | ✅ |
| 模式 / Schema | `schema.ts`, `schema-meetings-v2.sql`, `schema-optimization.sql` | 本地表结构 / 迁移 | ✅ | ✅ | ✅ |
| 向量索引 | `vector.ts` | 本地向量检索 | 🟡 | 🟡 | 🟡 |
| 测试 | `__tests__/*`, `*.test.ts` | 纯函数 / runtime 探测 | ✅ | ✅ | ✅ |

---

## 4. 跨端壳工程现状

| 平台 | 状态 | 关键资产 | 关键缺口 |
|---|---|---|---|
| **Web（H5）** | ✅ 主战场 | `vite` + Vue 3 + Pinia + Capacitor core | 依赖本地 API 时只能连代理 / 远程 |
| **Android** | ✅ 主力壳 | `frontend/android/` + Capacitor plugins | Keystore / mic / FCM 已通 |
| **iOS** | ⏳ 脚手架级 | `frontend/ios/App/AppDelegate.swift` 已就位；`Info.plist` 缺权限说明 | 几乎所有 Plugin 未启用；Keystore / biometric / 后台 mic 需补；CFNetwork 限明文需 NSAllowsArbitraryLoads |
| **HarmonyOS** | ⏳ Phase A 兜底 | `frontend/harmony/entry/src/main/ets/pages/Index.ets` 加载 `$rawfile('index.html')` | 全部 capabilities 报 false，未注入原生能力 |

---

## 5. 已登记的"已知例外"

| 例外 | 描述 | 影响 |
|---|---|---|
| RedClaw FileExporter 全租户落盘 | `internal/redclaw/file_exporter.go` 把所有租户审计写到同一目录 | 仅运维可见，外部 SIEM 过渡 |
| Audit PII 字段保留 | 审计条目 Detail 含敏感值以辅助调试 | 安全 vs 可观测性权衡 |
| dev 守护 pkill 陷阱 | 后端进程被 pkill 会被 supervisor 自动重启 | 调试时需先停 supervisor |

---

## 6. 部署形态（`deploy/`）

| 形态 | 路径 | 用途 |
|---|---|---|
| 本地方案 | `deploy/本地方案/` | 单机自包含，复用宿主 PG |
| ACC Integration | `deploy/acc-integration/` | 接入 ACC / LLM Gateway 联调 |
| 真机切流 | `docs/2026-09-07-local-cutover/` | 252 nginx 同源代理 |
| 远程后端连接 | 用户配置（`user_settings`） | 移动端连远程 pocketd |

---

## 7. 文档体系

| 层 | 路径 | 内容 |
|---|---|---|
| 顶层 | `README.md`, `docs/README.md` | 项目门面 + 索引 |
| 现行方案 | `docs/2026-09-*.md`, `docs/MOBILE_ARCHITECTURE_V2.md`（已 superseded） | 工具/会议/邮件/部署 |
| 移动 UX | `docs/2026-08-27-mobile-ux-design-v2.md` | 设计定稿 v2.0 |
| 备份 | `docs/archive/2026-{07,08,09}/` | 历史报告/交接 |
| 治理 | `docs/governance/{STATUS-MATRIX,REVIEW-PROCESS,EVIDENCE-LEDGER,SUPERSEDED}.md` | 状态矩阵 / 评审 / 证据账本 |
| 安全 | `docs/security/`, `docs/AUTH_REDCLAW_CURRENT.md` | 安全例外与认证契约 |

---

## 8. 一句话总结

> OpenPocket 是一个 **Vue 3 + Capacitor + Go Echo** 的"分布式 AI 编程助手"移动工作台：
> **40+ 业务域、24 个前端 feature、46 个后端模块、35+ 原生能力位**——已经把"AI 编排 / 笔记 / 邮件 / 财务 / 密码箱 / 会议 / 计划任务 / 网关"等近 10 条产品线全部在 Android 上跑通；
> iOS 与 HarmonyOS 仍是占位脚手架（<10% 工作量），是当前"半原生"状态的主要差距。
