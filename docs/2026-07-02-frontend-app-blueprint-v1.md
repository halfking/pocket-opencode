# 📱 OpenCode Pocket 前端 App 蓝图 v1

**日期**: 2026-07-02
**状态**: v1 规划（基于 Phase 0~5 + 端到端部署验证）
**作用**: 这份文档是"前端开发 + 后端 API 补全"的统一契约。开发哪个模块都按这份对齐。

---

## 0. 架构总览

```
┌─────────────── App 启动 ───────────────┐
│  LoginView                                  │
│    │ handleLogin()                         │
│    ├→ POST /api/auth/login                │
│    ├→ initLobster(masterPwd)              │
│    │    ├─ initAppCrypto (PBKDF2 + AES)   │
│    │    ├─ localDB.init (SQLCipher open) │
│    │    └─ vectorIndex.load()             │
│    └→ router.push('/ai')                  │
│                                            │
│  wsClient = singleton, 自动连接 /ws       │
│    └→ on('note.created', noteStore.add)  │
└────────────────────────────────────────────┘
```

5 个主功能模块 + 3 个原生能力 = 8 个子系统。

---

## 1. 功能矩阵

| 标签 | 模块 | 路由 | 主要 UI | 数据源 | 实时 |
|------|------|------|--------|--------|------|
| 🤖 | **AI 工具** | `/ai` | TasksView（已存在）| 三源任务：local PG + opencode HTTP + acc MCP | `task_created` / `session_attached` |
| 📝 | **笔记** | `/notes` `/notes/:id` | NoteListView + (待) NoteDetailView + NoteEditView | 本地 SQLCipher (notes + note_vectors) | `note.created` |
| 🎙 | **会议** | `/meetings` | ComingSoonView (占位) | 本地 SQLCipher (meetings + meeting_segments) | (Phase 6A 接入) |
| 📨 | **邮件** | `/email` `/email/:id` `/email/summary` | EmailInboxView + (待) EmailDetailView + EmailSummaryView | 本地 SQLCipher (emails) + IMAP 后台抓取 | `email.fetched` / `email.summary_ready` |
| 🔐 | **密码箱** | `/vault` `/vault/:id` | VaultListView + (待) VaultEntryView | 本地 SQLCipher (vault_entries, AES-GCM 密文) | `vault.synced` |
| 💬 | **聊天** | (待) `/chat` `/chat/:id` | (待) ChatListView + ChatDetailView | 本地 SQLCipher (chat_messages) | (Phase 6B 接入) |

| 能力 | 路由 | 用途 |
|------|------|------|
| 启动更新检查 | `/api/app/check-update` | APK 自更新（已有）|
| 健康检查 | `/healthz` | 探活 |
| 飞书回调 | `/callback/feishu` | 飞书事件（已有）|
| WebSocket | `/ws` | 实时事件通道（已存在）|

---

## 2. 路由清单（已注册 27 个 + 规划补 8 个）

### 2.1 已存在（pocketd 27 个）

| Method | Path | Handler | Frontend 调用方 |
|--------|------|---------|----------------|
| GET | `/healthz` | handleHealthz | App 启动探活 |
| GET | `/api/instances` | handleInstances | InstanceListView |
| GET | `/api/sessions/` | handleSessions | SessionListView |
| GET | `/api/sessions` | handleAllSessions | SessionListView |
| GET | `/api/tasks` | handleTasks | TasksView (AI) |
| POST | `/api/tasks` | handleTasks | TasksView (创建本地任务) |
| GET | `/api/tasks/{id}` | handleTaskOperations | TaskDetailView |
| POST | `/api/tasks/{id}/attach-session` | handleAttachSession | TaskDetailView |
| GET | `/api/config/models` | handleModelConfig | (待)Settings/ModelConfig |
| PUT | `/api/config/models` | (与 GET 同) | (待)Settings/ModelConfig |
| POST | `/api/config/reload` | handleConfigReload | (待)Settings |
| POST | `/api/config/models/test` | handleModelTest | (待)Settings |
| WS | `/ws` | handleWebSocket | 全局实时事件 |
| GET | `/api/app/check-update` | handleCheckUpdate | AppUpdateChecker |
| GET | `/api/app/download` | handleDownloadAPK | AppUpdateChecker |
| POST | `/callback/feishu` | handleFeishuCallback | (server-only) |
| POST | `/api/auth/login` | handleAuthLogin | LoginView |
| GET/POST | `/api/notes` | handleNotes | NoteListView |
| GET/DELETE | `/api/notes/{id}` | handleNoteOperations | (待)NoteDetailView |
| GET/POST | `/api/email/accounts` | handleEmailAccounts | (待)EmailAccountSetup |
| GET/PUT/DELETE | `/api/email/accounts/{id}` | handleEmailAccountOps | (待)EmailAccountSetup |
| GET | `/api/email/summaries` | handleEmailSummaries | (待)EmailSummaryView |
| GET | `/api/email/summaries/{date}` | handleEmailSummaryOps | (待)EmailSummaryView |
| GET | `/api/emails` | handleEmails | EmailInboxView |
| POST | `/api/emails/sync` | handleEmailSync | (待)EmailInboxView 手动刷新 |
| PATCH | `/api/emails/{id}` | handleEmailOps | EmailInboxView (标已读) |
| GET/POST | `/api/vault/sync/` | handleVaultSync | VaultListView 云同步按钮 |
| POST | `/api/stt/transcribe` | handleSttTranscribe | VoiceRecorderWidget |
| POST | `/api/embed` | handleEmbed | notes-store embedAndStore() |
| POST | `/api/llm/chat` | handleLLMChat | (待) AI 总结/智能分类 |

### 2.2 需要后端补的端点

| Method | Path | 用途 | Frontend 调用方 |
|--------|------|------|----------------|
| GET | `/api/meetings` | 会议列表 | (待)MeetingListView |
| POST | `/api/meetings` | 创建会议 | (待)MeetingRecordView |
| GET | `/api/meetings/{id}` | 会议详情+分段 | (待)MeetingRecordView |
| PUT | `/api/meetings/{id}` | 更新转写/纪要 | (待)MeetingRecordView |
| DELETE | `/api/meetings/{id}` | 删除会议 | (待)MeetingRecordView |
| POST | `/api/meetings/{id}/segments` | 批量存分段 | (待)MeetingRecordView |
| GET | `/api/chat/conversations` | 会话列表 | (待)ChatListView |
| GET | `/api/chat/messages/{convId}` | 消息列表 | (待)ChatDetailView |
| POST | `/api/chat/messages` | 追加消息（IMAP 抓取） | (Phase 6B 抓取) |
| GET | `/api/vault/entries/{id}` | 密码箱条目详情 | (待)VaultEntryView |
| POST | `/api/vault/entries` | 创建条目 | (待)VaultEntryView |
| PUT | `/api/vault/entries/{id}` | 更新条目 | (待)VaultEntryView |
| DELETE | `/api/vault/entries/{id}` | 删除条目 | (待)VaultEntryView |
| GET | `/api/email/sync/status` | 抓取状态/进度 | (待)EmailAccountSetup |
| POST | `/api/users/me/preferences` | 用户偏好（云端） | (待)Settings |

**优先级排序**：
1. **必须补**：vault 4 个端点（VaultEntryView 必需）
2. **应该补**：meetings 6 个 + email summaries 详情
3. **可选**：chat + preferences（按需）

---

## 3. 5 大模块的 API + 本地调用契约

### 3.1 笔记（notes）— Phase A 已就绪

**前端 store**：`src/features/notes/notes-store.ts`（已存在）
```typescript
// 已实现的 8 个方法
createNote(input): Promise<LocalNote>
updateNote(id, patch): Promise<void>
deleteNote(id): Promise<void>        // 软删除
getNote(id, includeDeleted?): Promise<LocalNote | null>
listNotes({domain?, limit?}): Promise<LocalNote[]>
searchFullText(query, limit?): Promise<SearchResult[]>
searchSemantic(queryText, topK?): Promise<SearchResult[]>
searchHybrid(queryText, topK?): Promise<SearchResult[]>
```

**后端 API**（已有，作为云模式可选）：
- `GET/POST /api/notes` — 列表/创建
- `GET/DELETE /api/notes/{id}` — 详情/删除
- `POST /api/notes/{id}/classify`（**待补**）— 单条重新分类

**云端调用（嵌入 + 分类）**：
```typescript
// notes-store.ts 内部
async function embedAndStore(noteId, content) {
  const res = await http<{embedding, model}>('/api/embed', {  // ← 已存在
    method: 'POST',
    body: JSON.stringify({text: content})
  })
  await vectorIndex.add(noteId, Float32Array.from(res.embedding), res.model)
}
// 分类（可选，kxmemory 部署后启用）
async function classifyViaKxmemory(noteId, content) {
  await http(`/api/notes/${noteId}/classify`, {method: 'POST'})  // ← 待补
}
```

**实时事件**（已存在）：
- `note.created` → note-store 列表/详情刷新
- `note.conflict` (SSOT 冲突，kxmemory 部署后) → 提示用户

**UI 现状与待补**：
- ✅ `NoteListView`（搜索栏 + 域色标 + 录音 FAB）
- ⏳ `NoteDetailView`（Markdown 渲染、编辑、smart_links 推荐、关联知识图）
- ⏳ `NoteEditView`（新建/编辑表单）
- ⏳ `KnowledgeGraphView`（关联图谱可视化）

**Native 桥接**：
- `cap-sherpa`（待建）— sherpa-onnx 本地 ASR
- `native/vector.ts` ✅ — 内存向量索引
- `native/local-db.ts` ✅ — FTS5 + 加密存储

---

### 3.2 邮件（email）— Phase D 完成 / Phase 2 完整功能待补

**前端 store**：`src/features/email/emails-store.ts`（已存在，8 个方法）
```typescript
listAccounts(): Promise<EmailAccount[]>
saveAccount(input): Promise<string>
deleteAccount(id): Promise<void>
listEmails(filter: ListFilter): Promise<LocalEmail[]>
upsertEmail(e): Promise<boolean>     // IMAP 抓取入本地库
markRead(id, read): Promise<void>
setAiClassification(id, cat, imp, sum, action): Promise<void>
updateSyncState(accountId, lastUid): Promise<void>
getUnreadCount(accountId?): Promise<number>
```

**后端 API**（已有云模式）：
- `GET/POST /api/email/accounts` — 列表/添加
- `GET/PUT/DELETE /api/email/accounts/{id}` — 单个管理
- `POST /api/emails/sync` — **核心**：客户端把 IMAP 抓取的邮件列表 POST 给后端，后端批量分类回写
- `PATCH /api/emails/{id}` — 标已读
- `GET /api/email/summaries` — 每日总结列表（**返回空数组占位**）
- `GET /api/email/summaries/{date}` — 单日总结（**未实现**）

**邮件抓取流水**（龙架构在客户端）：
```
1. [客户端] EmailInboxView 点击"刷新"或后台定时器
2. [客户端] fetch(IMAP 客户端，未来用 emersion/go-imap JS 移植)
3. [客户端] emails-store.upsertEmail()  →  写本地库
4. [客户端] http POST /api/emails/sync {emails: [...]}  // ← 批量
5. [服务端] handleEmailSync 写本地 PG 缓存 + 异步调 kxmemory 分类
6. [服务端] classifyEmailsAsync() → kxmemory.ClassifyEmails
7. [服务端] SetClassification() 写 PG + WS 广播 email.classified
8. [客户端] ws.on('email.classified', e) → 邮件 store 更新该条目的 cat/imp
```

**实时事件**（后端当前未广播，待补）：
- `email.classified` — 单封分类完成 → 客户端更新该条
- `email.summary_ready` — 每日总结生成 → 客户端拉取详情

**UI 现状与待补**：
- ✅ `EmailInboxView`（分类筛选、重要性筛选、标已读按钮）
- ⏳ `EmailDetailView`（正文 + AI 摘要卡片 + 建议操作按钮）
- ⏳ `EmailSummaryView`（每日总结 Markdown 渲染）
- ⏳ `EmailAccountSetup`（账户配置向导：选 IMAP 模板 + 测试连接 + 规则）

**Native 桥接**：
- ⏳ `cap-imap`（待建）— IMAP 客户端原生插件
- ⏳ 后台定时器（需 `cap-background-runner` 或 WorkManager）

---

### 3.3 密码箱（vault）— Phase D 基础 + Phase 4 详细页

**前端 store**：`src/features/vault/vault-store.ts`（已存在，8 个方法）
**云同步 store**：`src/features/vault/sync-store.ts`（已存在，4 个方法）
```typescript
// vault-store
listEntries(): Promise<VaultEntryMeta[]>
getEntry(id): Promise<VaultEntry | null>
saveEntry(input): Promise<string>
deleteEntry(id): Promise<void>
touchLastUsed(id): Promise<void>
exportEncryptedBlob(): Promise<{blob, version}>
importEncryptedBlob(blob): Promise<number>
countEntries(): Promise<number>

// sync-store
uploadSync(): Promise<SyncResult>
downloadSync(): Promise<SyncResult>
smartSync(): Promise<SyncResult>     // 本地优先
listVersions(): Promise<{version, createdAt}[]>
```

**后端 API**（已有云同步）：
- `POST /api/vault/sync` — 上传密文 blob
- `GET /api/vault/sync/latest` — 拉取最新
- `GET /api/vault/sync/versions` — 历史版本

**待补端点**：vault 条目本身的 CRUD 在本地完成，**不需要后端 CRUD**（云同步是整库覆盖）。但后端可加：
- `POST /api/vault/sync/{version}/restore` — 恢复到指定历史版本

**Native 桥接**：
- ⏳ `cap-keystore`（待建）— AndroidKeyStore AES + 生物识别
- 当前用 Web Crypto 降级方案 + `native/crypto.ts` 共享 key

**UI 现状与待补**：
- ✅ `VaultListView`（解锁界面 + 列表 + 新增表单 + 生成密码 + 云同步按钮）
- ⏳ `VaultEntryView`（条目详情 + 密码复制自动清空 + TOTP 生成 + 编辑）
- ⏳ `VaultUnlockView`（生物识别 + 主密码解锁向导）

---

### 3.4 会议（meetings）— Phase 6A 待实现

**前端 store**：`src/features/meetings/meetings-store.ts`（已存在骨架，10 个方法）
```typescript
createMeeting(input): Promise<LocalMeeting>
listMeetings(limit?): Promise<LocalMeeting[]>
getMeeting(id): Promise<LocalMeeting | null>
updateTranscript(id, transcript): Promise<void>
updateSummary(id, summary): Promise<void>
deleteMeeting(id): Promise<void>
saveSegment(seg): Promise<string>
getSegments(meetingId): Promise<MeetingSegment[]>
```

**后端 API**：**全待建**（见 §2.2 必补清单）
- `GET/POST /api/meetings` — 列表/创建
- `GET/PUT/DELETE /api/meetings/{id}` — CRUD
- `POST /api/meetings/{id}/segments` — 声纹分段批量入库

> **龙架构下**：会议数据全部本地存，后端端点为可选（"云模式"专用）。MVP **不需要后端实现**。

**云端调用（转写 + 纪要）**：
- 转写：`POST /api/stt/transcribe`（已存在，Multipart 音频上传）
- 纪要生成：`POST /api/llm/chat`（已存在，无状态 LLM 调用）

**Native 桥接**（待建）：
- `cap-sherpa`（与笔记共用）— 录音 + 转写
- `cap-voiceprint` — 声纹 embedding 提取（ECAPA-TDNN）
- 声纹聚类在 client 端做（不依赖服务端）

**UI 现状与待补**：
- 🚧 `ComingSoonView` 占位
- ⏳ `MeetingListView`（会议列表 + 录音 FAB）
- ⏳ `MeetingRecordView`（录音中、波形、声纹实时标签、转写进度）
- ⏳ `TranscriptView`（会后查看分段 + 纪要）

---

### 3.5 聊天聚合（chat）— Phase 6B 设计完成 / 实现待

**前端 store**：`src/features/chat/chat-store.ts`（已存在骨架，6 个方法）
```typescript
listConversations(source?, limit?): Promise<ChatConversation[]>
upsertConversation(c): Promise<void>
saveMessage(m): Promise<string>
getMessages(conversationId, limit?): Promise<ChatMessage[]>
markRead(conversationId): Promise<void>
```

**后端 API**：**全待建**（仅在云模式才需要；MVP 本地为主）

**Native 桥接**（待建）：
- `cap-notification`（NotificationListenerService）— 抓取 IM 通知
- `cap-sms` — 读 SMS
- `cap-telegram` — Telegram Bot API（走 HTTP，无需原生）
- `cap-wechat-pc-bridge` — PC 侧 PyWxDump 导出桥

**UI 现状与待补**：
- ❌ 无入口（路由待注册）
- ⏳ `ChatListView`（会话列表 + 来源 tab + 未读 badge）
- ⏳ `ChatDetailView`（消息流 + AI 总结按钮）
- ⏳ `ChatSourcesView`（来源授权 + 抓取状态）

---

## 4. 原生能力（Native 桥接）清单

| Native 模块 | 文件 | 状态 | 用途 |
|-------------|------|------|------|
| SQLCipher 本地库 | `native/local-db.ts` | ✅ | 加密存储 |
| 数据库 schema | `native/schema.ts` | ✅ | 12 表 + FTS5 |
| 向量索引 | `native/vector.ts` | ✅ | JS 余弦 |
| 共享加密 | `native/crypto.ts` | ✅ | AES-GCM key |
| 启动初始化 | `native/lobster-init.ts` | ✅ | initLobster() |
| 通用插件 | `native/util.ts` | ✅ | registerPluginSafely |
| keystore 桩 | `native/keystore.ts` | ✅ | cap-keystore 桩接口 |
| sherpa 桩 | `native/sherpa.ts` | ✅ | cap-sherpa 桩接口 |
| **cap-keystore 原生** | — | ⏳ 待建 Kotlin | 真实 Keystore + 生物识别 |
| **cap-sherpa 原生** | — | ⏳ 待建 Kotlin | 真实 sherpa-onnx ASR |
| **cap-voiceprint 原生** | — | ⏳ Phase 6A | 声纹 embedding |
| **cap-imap 原生** | — | ⏳ Phase 2 | IMAP 邮件拉取 |
| **cap-notification 原生** | — | ⏳ Phase 6B | 通知监听 |
| **cap-sms 原生** | — | ⏳ Phase 6B | 短信读取 |

---

## 5. 实时事件契约（WebSocket）

`wsClient.on(type, callback)`，已存在（`src/api/websocket.ts`）。

### 5.1 客户端订阅

| 事件 | 触发方 | payload | 订阅模块 |
|------|--------|---------|----------|
| `task_created` | handleTasks POST | `Task` | TasksView |
| `session_attached` | handleAttachSession | `SessionLink` | TaskDetailView |
| `note.created` | handleNotes POST | `Note` | NoteListView / NoteDetailView |
| `note.conflict` | kxmemory 部署后 | `{note_id, conflicts[]}` | NoteDetailView（提示 SSOT）|
| `email.classified` | handleEmailSync async | `{email_id, category, importance, summary}` | EmailInboxView |
| `email.fetched` | scheduler 抓取新邮件 | `Email` | EmailInboxView（角标）|
| `email.summary_ready` | 每日 21:00 触发 | `{date, summary_url}` | EmailSummaryView（通知）|
| `vault.synced` | handleVaultSync POST | `{userId}` | VaultListView（云同步状态）|
| `meeting.transcript_chunk` | (Phase 6A) | `{meeting_id, segment}` | MeetingRecordView |
| `meeting.completed` | (Phase 6A) | `{meeting_id, summary}` | MeetingListView（通知）|
| `chat.fetched` | (Phase 6B) | `{source, count}` | ChatListView（角标）|

### 5.2 服务端需要补的广播点

| 位置 | 应广播事件 |
|------|------------|
| handleEmailSync async 完成后 | `email.classified` |
| handleNotes POST 完成后 | `note.created`（已做）|
| handleTasks POST 完成后 | `task_created`（已做）|
| handleVaultSync POST 完成后 | `vault.synced`（已做）|
| handleEmailSummaries 生成总结 | `email.summary_ready`（**待补**）|
| Phase 6A 会议转写/纪要完成 | `meeting.*`（**待补**）|

---

## 6. 全局状态（Pinia stores）

| Store | 文件 | 状态 | 职责 |
|-------|------|------|------|
| `useAuthStore` | `stores/auth.ts` | ✅ | JWT + 用户名 |
| `useDeviceStore` | `stores/device.ts` | ✅ | 设备分级 + STT 引擎偏好 |
| `useLobsterStore` | `stores/lobster.ts` | ⏳ 待建 | 龙虾就绪状态（initLobster 结果）|
| `useSyncStore` | `stores/sync.ts` | ⏳ 可选 | 跨模块同步状态指示器 |

> **业务状态建议**：每个 feature 维护自己的 store（notes/emails/vault/meetings/chats 各自的 `*Store.ts`），不强行塞进 Pinia，避免跨模块耦合。Pinia 仅承担"跨模块共享"的状态。

---

## 7. 路由清单与权限

| 路由 | 需登录 | 需初始化龙虾 | 说明 |
|------|--------|---------------|------|
| `/login` | — | — | 登录页（同时初始化龙虾）|
| `/servers` | ✅ | — | 实例选择 |
| `/instances` | ✅ | — | 实例列表 |
| `/tasks` | ✅ | — | 任务（AI 工具）|
| `/ai` | ✅ | — | 任务同义词（首页）|
| `/notes` | ✅ | ✅ | 笔记 |
| `/email` | ✅ | ✅ | 邮件 |
| `/vault` | ✅ | ✅ | 密码箱 |
| `/meetings` | ✅ | ✅ | 会议（Phase 6A 启用）|
| `/chat` | ✅ | ✅ | 聊天（Phase 6B 启用）|
| `/sessions` | ✅ | — | 会话 |
| `/settings` | ✅ | — | 设置（云同步开关等）|

**router guard** 增强建议（`router-mobile.ts:beforeEach`）：
```typescript
if (to.meta.requiresAuth && !auth.isAuthenticated) next('/login')
if (to.meta.requiresLobster && !lobster.isReady) next('/login')
```

---

## 8. UI 清单与优先级

### 必须做（MVP 闭环）

| UI | 路由 | 优先级 | 工作量 | 依赖 |
|----|------|--------|--------|------|
| `LoginView` | `/login` | ✅ 已存在 | — | — |
| `AppLayout` | 全局 | ✅ 已存在 | — | — |
| `NoteListView` | `/notes` | ✅ 已存在 | — | — |
| `NoteDetailView` | `/notes/:id` | 🔴 P0 | 1d | notes-store |
| `EmailInboxView` | `/email` | ✅ 已存在 | — | — |
| `EmailDetailView` | `/email/:id` | 🔴 P0 | 1d | emails-store |
| `VaultListView` | `/vault` | ✅ 已存在 | — | — |
| `VaultEntryView` | `/vault/:id` | 🔴 P0 | 1d | vault-store |
| `TasksView` | `/ai` | ✅ 已存在 | — | — |
| `TaskDetailView` | `/tasks/:id` | ✅ 已存在 | — | — |
| `SettingsView` | `/settings` | ✅ 已存在 | — | — |
| `SessionListView` | `/sessions` | ✅ 已存在 | — | — |
| `InstanceListView` | `/instances` | ✅ 已存在 | — | — |
| `ComingSoonView` | `/meetings` | ✅ 已存在 | — | — |

### 应该做（功能完善）

| UI | 优先级 | 工作量 |
|----|--------|--------|
| `NoteEditView`（新建/编辑） | 🟠 P1 | 1d |
| `EmailSummaryView`（每日总结） | 🟠 P1 | 1d |
| `EmailAccountSetup`（账户向导） | 🟠 P1 | 2d |
| `KnowledgeGraphView`（笔记关联图） | 🟡 P2 | 3d |
| `MeetingListView`（Phase 6A） | 🟡 P2 | 2d |
| `MeetingRecordView`（Phase 6A） | 🟡 P2 | 3d |

### 可选（远期）

| UI | 优先级 | 备注 |
|----|--------|------|
| `ChatListView`（Phase 6B） | ⚪ P3 | 微信/短信/Telegram |
| `ChatDetailView` | ⚪ P3 | — |
| `ChatSourcesView` | ⚪ P3 | 来源授权管理 |
| `ModelConfigView`（设置子页） | ⚪ P3 | OpenCode 模型配置 |

---

## 9. 服务调用图（关键流程）

### 9.1 录音→笔记→嵌入→分类

```
[VoiceRecorderWidget]
  ↓ onTranscribed({text, audioPath, durationSec})
[NoteListView]
  ↓ createNote({content, contentType, audioPath, audioDurationMs})
[notes-store]
  ↓ localDB.run(INSERT local_notes)
  ↓ [async] embedAndStore(noteId, content)
        ↓ http POST /api/embed {text}
        ↓ [resp: {embedding, model}]
        ↓ vectorIndex.add(noteId, vec, model)
  ↓ [可选 async] classifyViaKxmemory(noteId, content)
        ↓ http POST /api/notes/{id}/classify
```

### 9.2 邮件 IMAP 抓取→分类

```
[EmailInboxView 刷新按钮 或 客户端定时器]
  ↓ fetch IMAP (cap-imap 原生插件)
  ↓ emails-store.upsertEmail(each)
  ↓ http POST /api/emails/sync {emails: [...]}
  ↓ [server] 写 PG + classifyEmailsAsync
        ↓ kxmemory.ClassifyEmails (snippet only)
        ↓ SetClassification() + WS broadcast email.classified
  ↓ [client] ws.on('email.classified')
        ↓ emails-store 内存数组更新（不重写磁盘）
        ↓ UI 重渲染
```

### 9.3 密码箱云同步

```
[VaultListView 点击 ☁️ 云同步]
  ↓ syncStore.smartSync()
  ↓ 检查 localCount vs cloud version
  ↓ uploadSync():
        ↓ vaultStore.exportEncryptedBlob()
              ↓ localDB.query(local_vault_entries)
              ↓ encryptString(JSON.stringify(rows))   // 整库 AES-GCM
              ↓ return {blob, version}
        ↓ http POST /api/vault/sync {blob, version}
        ↓ [server] vaultStore.PutLatest (事务：标旧版本 is_current=FALSE + 插新版本)
        ↓ [server] WS broadcast vault.synced
  ↓ 状态条显示 "已上传 N 条 v{version}"
```

---

## 10. 下一步行动建议

按"砍掉所有非 MVP 元素、做薄闭环"原则：

### 这一轮要做（**前端 MVP 完整化**）

1. **NoteDetailView**（1d）
   - 调 `notesApi.get(id)` 或 `notesStore.getNote(id)`
   - Markdown 渲染（建议 `markdown-it` 或 `marked`）
   - 显示 smart_links 推荐（暂时 mock 或 `notesStore.searchSemantic(title, 5)` 排除自身）
   - 编辑模式：textarea + 保存按钮

2. **EmailDetailView**（1d）
   - 调 `emailsStore.listEmails` 找详情（需 emails-store 加 `getEmail`）
   - 显示 AI 摘要卡片 + 建议操作按钮（回复/归档/转 todo）
   - PATCH 标已读

3. **VaultEntryView**（1d）
   - 调 `vaultStore.getEntry(id)` 解密
   - 显示密码（默认 •••，点击显示）+ 复制按钮
   - 30 秒后自动清空剪贴板（前端 setTimeout）
   - 编辑/删除按钮

4. **NoteEditView**（1d）
   - 复用 VoiceRecorderWidget
   - 表单：title / content / domain / tags
   - 保存调 `createNote` 或 `updateNote`

5. **EmailAccountSetup**（2d）
   - 选 IMAP 模板（Gmail/QQ/163/Outlook 预设）
   - 表单：host/port/email/password
   - 调 `emailApi.addAccount` 或 `emailsStore.saveAccount`
   - 显示测试连接状态

6. **router guard 增强**（0.5d）
   - 未登录跳 /login
   - 未初始化龙虾跳 /login

7. **后端补 notes 详情 + 分类端点**（**必做**支持上面）
   - `POST /api/notes/{id}/classify` — 触发单条 AI 分类
   - 已有 `GET /api/notes/{id}` 详情，需要确认返回完整 Note

8. **后端补 email summaries 详情**（1d）
   - `GET /api/email/summaries/{date}` 返回 DailySummary 实际内容

### 工作量汇总

| 任务 | 天数 |
|------|------|
| NoteDetailView + 编辑 | 1.5 |
| EmailDetailView + AccountSetup | 2.5 |
| VaultEntryView | 1 |
| router guard 增强 | 0.5 |
| 后端补 2-3 个端点 | 1 |
| 联调 + bug 修复 | 1 |
| **合计** | **7.5 人天（约 1.5 周）** |

完成后 → MVP 端到端可演示：
- 登录 → 录音 → 转写 → 笔记 → 搜索
- 邮件抓取 → 分类 → 标已读 → 看每日总结
- 密码箱 → 录入 → 云同步 → 跨设备恢复

### 后续 Phase（按 5 大模块优先级）

- Phase 2 完整：EmailAccountSetup + IMAP 抓取（需要 cap-imap 原生插件）
- Phase 3 完整：NoteDetailView + 关联图谱
- Phase 4 完整：VaultEntryView + TOTP + cap-keystore
- Phase 5 完整：MCP 任务实时推送（待 ACC 团队）
- Phase 6A 完整：会议 + 声纹 + cap-sherpa
- Phase 6B 完整：聊天聚合 + cap-notification/sms

---

## 11. 文档版本

- v1（2026-07-02）：基于 Phase 0-5 现状梳理
- 后续版本随模块迭代更新