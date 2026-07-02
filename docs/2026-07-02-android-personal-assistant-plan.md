# 📱 OpenCode Pocket 个人助理 APP — Android 扩展完整方案

**版本**: v1.0.0
**日期**: 2026-07-02
**状态**: 设计方案
**作者**: OpenCode Pocket 团队

> 配套文档：
> - 本地语音识别评估：[`2026-07-02-android-stt-evaluation.md`](./2026-07-02-android-stt-evaluation.md)
> - 密码箱专项设计：[`2026-07-02-password-vault-design.md`](./2026-07-02-password-vault-design.md)
> - 邮箱模块设计：[`2026-07-02-email-assistant-design.md`](./2026-07-02-email-assistant-design.md)
> - 代码骨架实施：[`2026-07-02-implementation-skeleton.md`](./2026-07-02-implementation-skeleton.md)

---

## 🎯 产品定位

把现有的 **OpenCode Pocket**（OpenCode 实例移动控制台）从单一运维工具，扩展为一款**个人助理 APP**，整合五大功能模块：

| 模块 | 一句话定位 | 核心价值 |
|------|----------|---------|
| 🧠 **AI 工具控制** | 远程管理 OpenCode 实例、任务、会话 | **已有**，保留并增强 |
| 📝 **语音笔记** | 语音优先的智能笔记与知识沉淀 | 语音录入 + AI 分类 + SSOT |
| 🎙️ **会议记录** | 会议录音转写 + 声纹识别发言人 | 多人会议自动区分谁说了什么 |
| 📨 **邮箱助手** | 多邮箱聚合 + 重要邮件抓取分类 + 每日总结 | 不再漏掉关键邮件 |
| 🔐 **密码箱** | 生物识别保护的本地密码管家 | 安全 + 跨设备同步 |

**设计原则**：简洁美观、专业不复杂、紧凑充实、重功能轻交互。

---

## 🏗️ 关键架构决策

### 决策 1：扩展现有 Capacitor + Vue3，而非重写 Kotlin 原生

**理由**：
- opencode-pocket 已有可运行的 Capacitor 包壳 APP（含 APK 自更新机制），重写代价巨大
- 五大模块中只有"语音识别"和"生物识别"需要深度原生能力，其余都是表单/列表/详情，WebView 完全胜任
- Capacitor 插件机制可桥接任意原生能力（sherpa-onnx、Keystore、IMAP 后台任务）
- 代码骨架量减少 70%，2-3 周可见成效

**代价**：语音识别性能略低于纯原生（多一层 JS bridge），但 sherpa-onnx RTF 0.06-0.15 足够缓冲。

### 决策 2：后端双轨制 —— Go（pocketd）+ Python（kxmemory FastAPI）

```
┌──────────────────────────────────────────────────────────┐
│              OpenCode Pocket APP (Capacitor+Vue3)         │
└────────────┬──────────────────────────┬──────────────────┘
             │                          │
   ┌─────────▼──────────┐    ┌──────────▼───────────────┐
   │  pocketd (Go)       │    │  kxmemory FastAPI (新)    │
   │  已有 + 扩展:        │    │  新增:                    │
   │  - 实例/任务/会话    │    │  - 语音笔记 AI 处理       │
   │  - 邮箱抓取(新)      │    │  - 会议转写/声纹          │
   │  - 密码箱同步代理(新)│    │  - 邮件智能分类/总结       │
   │  - APK 自更新        │    │  - 知识图谱/SSOT          │
   └─────┬──────────────┘    └──────────┬───────────────┘
         │                              │
   ┌─────▼─────┐         ┌──────────────▼──────────────┐
   │  SQLite    │         │ PostgreSQL + Qdrant + Neo4j  │
   │ (本地任务) │         │ (笔记/知识图谱/向量检索)      │
   └───────────┘         └──────────────────────────────┘
```

**分工原则**：
- **pocketd（Go）** 负责：实时控制面（实例/任务）、本地轻量持久化（SQLite）、需要常驻后台的任务（邮箱抓取定时器）、对外 webhook 入口
- **kxmemory（FastAPI）** 负责：所有重 AI 计算的（语音转写调度、LLM 分类、向量检索、图谱、SSOT），复用已设计的 8 张表

两套后端通过 APP 层聚合（APP 按功能模块分别请求），不强制打通。

### 决策 3：语音识别 —— 本地优先（sherpa-onnx）+ 云端兜底（Groq）

详见 STT 评估报告。核心：中文本地用 sherpa-onnx Paraformer（CER 8-10%），云端用 Groq Whisper Large v3 Turbo（$0.04/hr）。

---

## 🧩 模块化架构

### 整体分层

```
┌─────────────────────────────────────────────────────────┐
│                    UI 层 (Vue3 组件)                     │
│   notes/  meetings/  ai-tools/  email/  vault/          │
├─────────────────────────────────────────────────────────┤
│              状态层 (Pinia stores)                       │
│   useNotesStore  useEmailStore  useVaultStore ...       │
├─────────────────────────────────────────────────────────┤
│              API 层 (api/client.ts 分模块)               │
│   notes.ts  meetings.ts  email.ts  vault.ts  ai.ts      │
├─────────────────────────────────────────────────────────┤
│         原生能力层 (Capacitor 插件)                       │
│   cap-sherpa  cap-keystore  cap-biometric  cap-imap     │
├─────────────────────────────────────────────────────────┤
│              后端 (pocketd Go + kxmemory FastAPI)        │
└─────────────────────────────────────────────────────────┘
```

### 前端目录扩展（在现有 `frontend/src/` 基础上）

```
frontend/src/
├── app/
│   ├── App.vue                 # 改造：包裹 AppLayout
│   ├── AppLayout.vue           # 新增：统一顶栏 + 底部导航 + <router-view/>
│   └── router-mobile.ts        # 扩展：新增 5 个模块路由
├── stores/                     # 新增 Pinia
│   ├── auth.ts                 # 真实 auth（替换假 admin/admin）
│   ├── notes.ts
│   ├── email.ts
│   ├── vault.ts
│   └── device.ts               # 设备分级、STT 引擎选择
├── api/
│   ├── client.ts               # 已有，重构为统一 fetch + auth 注入
│   ├── notes.ts                # 新
│   ├── meetings.ts             # 新
│   ├── email.ts                # 新
│   ├── vault.ts                # 新
│   └── stt.ts                  # 新：本地/云端 STT 调度
├── native/                     # 新：Capacitor 插件 TS 封装
│   ├── sherpa.ts               # cap-sherpa 桥接
│   ├── keystore.ts             # cap-keystore 桥接
│   ├── biometric.ts            # @capacitor-community/biometric
│   └── background-task.ts      # 后台邮箱抓取
├── features/
│   ├── auth/ ...               # 已有，增强
│   ├── tasks/ ...              # 已有（AI 工具控制）
│   ├── notes/                  # 新
│   │   ├── NoteListView.vue
│   │   ├── NoteDetailView.vue
│   │   ├── VoiceRecorderWidget.vue
│   │   └── KnowledgeGraphView.vue
│   ├── meetings/               # 新
│   │   ├── MeetingListView.vue
│   │   ├── MeetingRecordView.vue
│   │   └── TranscriptView.vue
│   ├── email/                  # 新
│   │   ├── EmailInboxView.vue
│   │   ├── EmailSummaryView.vue
│   │   └── EmailAccountSetup.vue
│   └── vault/                  # 新
│       ├── VaultListView.vue
│       ├── VaultEntryView.vue
│       └── VaultUnlockView.vue
├── styles/
│   └── tokens.css              # 新：CSS 变量设计系统 + 暗色主题
└── components/
    ├── BottomNav.vue           # 新：从各 view 抽取的统一底部导航
    ├── TopBar.vue              # 新
    └── ...
```

### 后端目录扩展（pocketd Go）

```
backend/internal/
├── server/server.go            # 扩展：注册新路由 + Server 新增字段
├── config/config.go            # 扩展：Postgres/Groq/Email/MCP/KxMemory env vars
├── db/                         # 新：统一 PostgreSQL 连接池
│   └── pg.go
├── task/                       # 已有（CRUD 范本，Phase 0 迁 PG）
├── feishu/                     # 已有（webhook 范本）
├── notes/                      # 新：笔记缓存 store（PG）
│   ├── store.go
│   └── note.go
├── email/                      # 新：IMAP 抓取 + 定时器（PG）
│   ├── model.go
│   ├── store.go
│   ├── fetcher.go              # IMAP 拉取（调 emersion/go-imap）
│   └── scheduler.go            # cron 定时
│   # 注：分类/总结的 LLM 逻辑在 kxmemory 侧，pocketd 不实现 classifier.go/summarizer.go
├── vault/                      # 新：密码箱同步代理（PG，存密文 blob）
│   └── store.go
├── stt/                        # 新：Groq Whisper 云端兜底代理
│   └── transcribe.go
└── meeting/                    # 新：会议元数据 + 转写任务队列（PG，Phase 6A）
    └── store.go
```

> **数据层**：Phase 0 起 pocket 后端从 SQLite 迁移到 PostgreSQL，与 kxmemory 共用同一 PG 实例（不同 schema 或表前缀），统一数据层。下文凡是"SQLite"均应理解为迁移后的 PostgreSQL。

---

## 📝 模块一：语音笔记

### 核心流程

```
用户长按录音按钮
    ↓
MediaRecorder 采集音频（16kHz mono PCM/WAV）
    ↓
设备分级判断 → 本地 sherpa-onnx 或 云端 Groq
    ↓
转写文本 + 置信度
    ↓ (低置信度自动回退云端重转)
POST /api/notes  (pocketd)
    ↓
pocketd 存本地 SQLite + 转发 kxmemory 做分类/SSOT
    ↓
kxmemory: LLM 自动分类(work/study/life/idea) + 向量化 + 图谱关联
    ↓
WebSocket 推送 note.created → APP 实时刷新
```

### 数据模型

复用已设计的 `notes` / `workspaces` / `knowledge_blocks` / `smart_links` 表（见 kxmemory `appendix-a-voice-notion-migration.sql`）。pocketd 侧仅缓存最近笔记的元数据（id/title/updated_at）供离线浏览，完整内容向 kxmemory 拉取。

### 关键交互
- **录音页**：大圆形录音按钮（长按录音，松开转写），实时波形显示
- **笔记列表**：按 domain 分组的紧凑卡片列表，支持搜索
- **详情页**：Markdown 渲染 + 编辑 + 关联推荐（smart_links）+ 知识图谱节点
- **AI 操作**：一键总结、改写、生成待办、关联到其他笔记

---

## 🎙️ 模块二：会议记录（声纹识别）

### 核心流程

```
会议开始 → 启动录音（可后台）
    ↓
持续采集 + VAD 分段
    ↓
每段音频: sherpa-onnx 转写 + 声纹 embedding 提取
    ↓
声纹聚类（首次需用户标注 1-2 段"我是谁"）
    ↓
生成带发言人标签的转写稿
    ↓ (会议结束)
kxmemory 生成会议纪要（决议/行动项/待办）
    ↓
存入 notes（domain=work, type=meeting）+ 自动提取 todos
```

### 声纹识别方案
- **Embedding 模型**：sherpa-onnx 同项目下的说话人识别模型（ECAPA-TDNN / 3D-Speaker），on-device 可跑
- **聚类**：增量层次聚类，无需预知人数；新声纹与已有库比对（余弦相似度 > 0.7 视为同一人）
- **冷启动**：前 1-2 次会议用户手动标注发言人，之后自动识别
- **隐私**：声纹向量本地存储，不上传

### 数据模型（kxmemory 新增表）

```sql
CREATE TABLE meetings (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    title TEXT,
    started_at TIMESTAMP NOT NULL,
    ended_at TIMESTAMP,
    audio_file_path TEXT,
    duration_sec INTEGER,
    participant_count INTEGER,
    summary TEXT,                -- AI 生成的会议纪要
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE meeting_segments (
    id TEXT PRIMARY KEY,
    meeting_id TEXT NOT NULL,
    speaker_label TEXT,          -- "说话人1" / "张三"
    speaker_embedding_id TEXT,   -- 关联声纹库
    start_ms INTEGER,
    end_ms INTEGER,
    transcript TEXT,
    confidence REAL,
    FOREIGN KEY (meeting_id) REFERENCES meetings(id) ON DELETE CASCADE
);

CREATE TABLE voiceprints (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    display_name TEXT,
    embedding BLOB,              -- 声纹向量
    sample_count INTEGER DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

---

## 📨 模块三：邮箱助手

> 详细设计见 [`2026-07-02-email-assistant-design.md`](./2026-07-02-email-assistant-design.md)，此处概要。

### 核心能力
- **多邮箱聚合**：IMAP 协议接入 Gmail/QQ/Outlook/企业邮箱等多个账户
- **智能抓取**：后台定时拉取，按规则过滤（重要发件人、关键词、未读）
- **AI 分类**：LLM 自动打标签（工作/账单/通知/私人/垃圾）
- **每日总结**：每天固定时间生成"今日邮件摘要"推送到 APP

### 架构
```
pocketd 后台 scheduler (cron)
    ↓ 每 15 分钟
IMAP fetcher (Go imapsync 库)
    ↓
去重 + 存本地 SQLite (emails 表)
    ↓
调 kxmemory /api/email/classify (LLM 分类)
    ↓
每日 21:00 触发 /api/email/daily-summary (LLM 总结)
    ↓
WebSocket push email.summary_ready → APP
```

详见邮箱专项文档。

---

## 🔐 模块四：密码箱

> 详细设计见 [`2026-07-02-password-vault-design.md`](./2026-07-02-password-vault-design.md)，此处概要。

### 核心能力
- **生物识别解锁**：指纹/面部（BiometricPrompt）
- **AES-256 加密存储**：主密钥由 Android Keystore 保护
- **密码生成器**：可配置长度/字符集/强度评估
- **跨设备同步**：端到端加密，pocketd 仅存密文

详见密码箱专项文档。

---

## 🧠 模块五：AI 工具控制（增强现有）

在现有 tasks/sessions/instances 基础上增强：
- **语音指令**："新建任务：优化登录性能" → 解析为 task 创建
- **会话纪要**：从 OpenCode 会话提取决策、变更文件、下一步
- **跨实例任务看板**：聚合多实例任务的统一视图
- **快捷指令**：常用操作一键触发（重启实例、切换模型、查看日志）

---

## 🎨 UI/UX 设计系统

### 现状问题
当前前端**无设计系统**：颜色硬编码（`#667eea`/`#764ba2` 到处复制）、无暗色模式、bottom-nav 在每个 view 里重复、无 Pinia、登录是假的 admin/admin。

### 改造方案（基础设施先行）

**1. CSS Token 设计系统**（`styles/tokens.css`）：
```css
:root {
  /* 品牌色 */
  --brand-primary: #667eea;
  --brand-gradient: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  /* 语义色 */
  --bg-base: #f7f7fa;
  --bg-card: #ffffff;
  --text-primary: #111827;
  --text-secondary: #6b7280;
  --border: #e5e7eb;
  /* 功能色 */
  --success: #10b981; --warning: #f59e0b; --danger: #ef4444;
  /* 间距/圆角 */
  --radius-sm: 8px; --radius-md: 12px; --radius-lg: 16px;
  --space-1: 4px; --space-2: 8px; --space-3: 12px; --space-4: 16px;
}
@media (prefers-color-scheme: dark) {
  :root { --bg-base:#0f0f14; --bg-card:#1a1a22; --text-primary:#f3f4f6; --text-secondary:#9ca3af; --border:#2a2a35; }
}
```

**2. 统一 Layout**（`AppLayout.vue`）：抽取 TopBar + BottomNav，包裹 router-view。底部导航从 4 项扩为 5 项 + "更多"。

**3. 底部导航重构**（5 主功能 + 更多菜单）：
```
┌──────────────────────────────────────────┐
│              TopBar (标题)                │
├──────────────────────────────────────────┤
│                                          │
│            <router-view />               │
│                                          │
├──────────────────────────────────────────┤
│ 🤖AI  📝笔记  🎙会议  📨邮件  ⋮更多     │
└──────────────────────────────────────────┘
```
"更多"菜单进入：🔐密码箱、⚙️设置、💻实例、💬会话。

**4. 真实 Auth**：Pinia `useAuthStore` + JWT，替换假 admin/admin。fetch 拦截器自动注入 `Authorization: Bearer`。

**5. 紧凑充实的设计语言**：
- 列表项紧凑（密度高），信息密度优先
- 卡片化分组，减少视觉噪音
- 关键操作底部浮动按钮（FAB）
- 语音输入以大按钮为入口，贯穿各模块

---

## 🔄 数据流与实时性

### WebSocket 事件（复用 pocketd wsHub，新增类型）
| 事件类型 | 触发 | 前端响应 |
|---------|------|---------|
| `note.created` | 新笔记 | 笔记列表刷新 |
| `meeting.transcript_chunk` | 转写进度 | 实时显示转写文字 |
| `meeting.completed` | 会议结束 | 推送纪要 |
| `email.fetched` | 新邮件 | 邮件角标 +1 |
| `email.summary_ready` | 每日总结完成 | 推送通知 |
| `vault.synced` | 密码箱同步 | 刷新列表 |
| `task.*` | 已有 | 任务更新 |

### 离线策略
- 笔记/会议/密码箱：本地优先写 SQLite，联网后同步
- 邮箱：后台任务在 pocketd 侧执行，APP 离线也能抓取
- 语音：本地 sherpa-onnx 离线可用，云端兜底需联网

---

## 🔌 Capacitor 插件清单（新增）

| 插件 | 用途 | 实现方式 |
|------|------|---------|
| `cap-sherpa`（自研） | 本地语音识别 + 声纹 | 封装 sherpa-onnx Android AAR |
| `cap-keystore`（自研） | 密码箱加密存储 | Android Keystore + AES-256 |
| `@capacitor-community/biometric` | 生物识别 | 现成插件 |
| `@capacitor-community/media-capture` | 录音 | 现成插件 |
| `@capacitor/local-notifications` | 本地通知（邮件/会议提醒） | 现成插件 |
| `@capacitor/background-runner` | 后台任务（邮箱抓取触发） | 现成插件 |

---

## 📅 实施路线图

### Phase 0：基础设施（第 1 周）
- [ ] 引入 Pinia + 真实 auth store
- [ ] 建立 CSS token 设计系统 + 暗色模式
- [ ] 抽取 AppLayout / BottomNav / TopBar 组件
- [ ] fetch 拦截器注入 auth token
- [ ] 清理空目录（views/、router/、stores/ 旧）和重复 task 组件

### Phase 1：语音笔记 MVP（第 2-3 周）
- [ ] 集成 Groq 云端 Whisper（最快跑通端到端）
- [ ] VoiceRecorderWidget + 转写 + 笔记列表/详情
- [ ] pocketd notes store + kxmemory 笔记 API
- [ ] WebSocket note.created

### Phase 2：邮箱助手（第 3-4 周）
- [ ] pocketd IMAP fetcher + scheduler
- [ ] 邮箱账户配置 UI
- [ ] kxmemory 邮件分类 + 每日总结
- [ ] 邮件列表 + 每日摘要推送

### Phase 3：密码箱（第 4-5 周）
- [ ] cap-keystore 插件
- [ ] 生物识别解锁
- [ ] 密码条目 CRUD + 生成器
- [ ] 跨设备加密同步

### Phase 4：会议记录（第 5-7 周）
- [ ] cap-sherpa 插件（sherpa-onnx 封装）
- [ ] 录音 + VAD + 转写
- [ ] 声纹识别 + 发言人标注
- [ ] 会议纪要生成

### Phase 5：本地 STT + 优化（第 7-8 周）
- [ ] sherpa-onnx Paraformer 本地引擎接入
- [ ] 设备分级策略 + Vosk 兜底
- [ ] 置信度回退云端逻辑
- [ ] VAD 门控 + 电量优化

### Phase 6：AI 工具控制增强（持续）
- [ ] 语音指令解析
- [ ] 会话纪要提取
- [ ] 跨实例看板

---

## ⚠️ 风险与缓解

| 风险 | 影响 | 概率 | 缓解 |
|------|------|------|------|
| sherpa-onnx 无现成 Capacitor 插件 | 高 | 高 | 自研桥接插件（2-3 天），参考官方 Android demo；先用 Groq 云端跑通 MVP |
| kxmemory 后端尚未实现 | 高 | 高 | Phase 1 可先用 pocketd 本地 SQLite + 直接调 Groq/LLM API，kxmemory 逐步接入 |
| Capacitor WebView 录音/后台限制 | 中 | 中 | 录音用 MediaRecorder API 已验证可用；后台邮箱抓取放 pocketd 服务端规避 |
| 邮箱 IMAP 凭证安全 | 高 | 中 | 凭证用 Keystore 加密存储，IMAP 连接走 pocketd（不在客户端存密码） |
| 密码箱跨设备同步安全 | 高 | 中 | 端到端加密，主密钥不经服务端明文 |
| APP 体积膨胀（模型+插件） | 中 | 中 | 模型按需下载（首次启动），动态分区 |

---

## 📊 成功指标

**功能完整性**：5 大模块全部可用，覆盖 90% 日常场景
**性能**：APP 启动 < 1.5s，本地 STT RTF < 0.3，搜索 < 500ms
**安全**：密码箱零明文泄露，生物识别误识率 < 0.01%
**体验**：暗色模式、紧凑布局、NPS > 50

---

## 🔗 与现有方案的关系

本方案是对 `kxmemory/docs/2026-07-01-voice-notion-app-plan.md`（Flutter 版）的**修正与落地**：
- 客户端从 Flutter → Capacitor+Vue3（扩展 opencode-pocket）
- 本地 STT 从 Whisper.cpp → sherpa-onnx（中文更准）
- 云端 STT 从 OpenAI → Groq（便宜 89%）
- 新增邮箱、密码箱、会议三个模块
- 数据库设计（8 张表）继续复用，会议模块新增 3 张表

原 Voice-Notion 的笔记/知识图谱/SSOT 设计继续有效，作为本方案"语音笔记"和"会议记录"模块的 AI 后端规范。
