# 🔌 kxmemory ↔ pocketd API 契约

**版本**: v1.1.0（2026-07-02 审计修正）
**日期**: 2026-07-02
**状态**: 契约定义（Phase 1，解决审计阻塞项 #2）

> 本文档定义 pocketd（Go）调用 kxmemory（FastAPI）所有跨服务接口的精确契约：请求/响应 JSON schema、认证、错误码、重试语义。Phase 0 审计发现"kxmemory 接口全凭想象"，本契约作为 pocketd handler 与 kxmemory service 实现的共同依据。

> ⚠️ **路径口径说明（v1.1 审计修正）**：下文 §2/§3 设计的路径前缀为 `/api/voice/`、`/api/email/`（kxmemory 设计稿原始命名）。但 `backend/internal/kxmemory/client.go` 已实现的 3 个方法实际调用的是 **`/v1/notes/classify`、`/v1/emails/classify`、`/v1/emails/daily-summary`**（`/v1/` 前缀）。**kxmemory 团队实现时以 client.go 的 `/v1/` 路径为准**；下文的 `/api/voice/...` 路径仅作为模块归属的设计参考。§2.2~2.8（notes list/detail/put/delete、classify、ssot/check、search）当前**未在 client.go 实现**，属于规划中的端点。

---

## 1. 传输与认证

| 维度 | 规范 |
|------|------|
| 协议 | HTTPS（生产）/ HTTP（本地开发）|
| 数据格式 | JSON（UTF-8）|
| 认证 | `Authorization: Bearer <jwt>`，JWT 由 pocketd `/api/auth/login` 签发，kxmemory 用共享 `POCKET_JWT_SECRET` 校验 |
| 用户标识 | 所有请求带 `user_id`（从 JWT sub 解析），或由 pocketd 在请求体显式传递 |
| 超时 | 客户端 30s（LLM 调用可能慢）；pocketd 用 `cfg.OpenCodeTimeoutMS` 派生 |
| 重试 | 5xx / 网络错误指数退避 3 次（0.5s/2s/8s）；4xx 不重试 |

### 基址
- `POCKET_KXMEMORY_BASE_URL`（pocketd 配置），如 `https://memory.kxpms.cn`
- 所有路径前缀 `/api/voice/`（笔记/SSOT/图谱）或 `/api/email/`（邮箱）或 `/api/chat/`（聊天，Phase 6B）

### 通用错误响应
```json
{ "error": "string", "code": "invalid_request|not_found|conflict|rate_limited|internal", "detail": {} }
```
HTTP 状态码：400 invalid_request / 404 not_found / 409 conflict / 429 rate_limited / 500 internal。

---

## 2. 语音笔记接口

### 2.1 `POST /api/voice/notes/create` — 创建笔记（核心流水）

pocketd 转写完成后调用此接口，触发 AI 分类 + SSOT 检测 + smart_links + 图谱 + L1 记忆。

**请求**
```json
{
  "user_id": "local",
  "content": "今天和客户讨论了 Q3 预算，需要在周五前确认...",
  "title": null,
  "content_type": "voice",
  "domain": null,
  "tags": [],
  "voice_session_id": "vs-abc123",
  "audio_file_path": "/data/audio/2026/07/note-xxx.wav",
  "audio_duration": 23,
  "workspace_id": null
}
```

**响应 200（成功）**
```json
{
  "status": "success",
  "note_id": "note-xxx",
  "classification": {
    "domain": "work",
    "category": "meeting",
    "tags": ["Q3", "预算", "客户"],
    "suggested_title": "Q3 预算讨论",
    "confidence": 0.92
  },
  "smart_links": [
    { "target_id": "note-yyy", "link_type": "related_to", "confidence": 0.81 }
  ],
  "todos": [
    { "text": "周五前确认 Q3 预算", "due_date": "2026-07-05", "priority": "high" }
  ]
}
```

**响应 200（检测到 SSOT 冲突）**
```json
{
  "status": "conflict_detected",
  "draft_id": "draft-xxx",
  "conflicts": [
    {
      "existing_note_id": "note-zzz",
      "existing_title": "Q2 预算",
      "conflict_type": "update",
      "similarity_score": 0.83,
      "suggested_resolution": "merge_update"
    }
  ]
}
```
pocketd 收到 conflict 时仍先把笔记入库（is_latest=TRUE），并广播 `note.conflict` 让 APP 提示用户。

### 2.2 `GET /api/voice/notes/list` — 列表

**Query**: `user_id`, `domain?`, `workspace_id?`, `search?`, `limit=50`, `offset=0`

**响应**
```json
{ "notes": [ { /* NoteMeta */ } ], "total": 120, "limit": 50, "offset": 0 }
```

### 2.3 `GET /api/voice/notes/{note_id}` — 详情

**响应**: 完整 Note + knowledge_blocks + smart_links + todos + related_knowledge（来自 Qdrant/Neo4j）。

### 2.4 `PUT /api/voice/notes/{note_id}` — 更新

内容变更会重新触发 SSOT 检测 + smart_links 更新。

### 2.5 `DELETE /api/voice/notes/{note_id}` — 软删除

置 `is_latest = FALSE`（保留历史，不物理删除）。

### 2.6 `POST /api/voice/classify` — 单独分类（可复用）

pocketd 邮箱/聊天模块也可调用此接口做文本分类。

**请求**: `{ "user_id", "content", "context_hint"? }`
**响应**: `{ "domain", "category", "tags": [], "confidence" }`

### 2.7 `POST /api/voice/ssot/check` — SSOT 冲突检测

**请求**: `{ "user_id", "content", "exclude_note_id"? }`
**响应**: `{ "has_conflict": true, "conflicts": [ /* 同 2.1 conflict 结构 */ ] }`

### 2.8 `GET /api/voice/search?q=<query>` — 混合搜索

PG 全文 + Qdrant 向量 + Neo4j 图谱融合。
**响应**: `{ "results": [ { "note_id", "title", "snippet", "score", "source": "fts|vector|graph" } ] }`

---

## 3. 邮箱 AI 接口

### 3.1 `POST /api/email/classify` — 邮件分类

pocketd 抓取新邮件后调用。

**请求**
```json
{
  "user_id": "local",
  "emails": [
    {
      "email_id": "em-xxx",
      "subject": "Q3 预算审批需周五前确认",
      "from_address": "manager@company.com",
      "from_name": "张经理",
      "snippet": "请尽快确认 Q3 预算..."
    }
  ]
}
```

**响应**（数组，与请求一一对应）
```json
{
  "results": [
    {
      "email_id": "em-xxx",
      "category": "work",
      "importance": "high",
      "ai_summary": "张经理要求周五前确认 Q3 预算",
      "suggested_action": "reply",
      "action_reason": "包含截止日期且需回复确认"
    }
  ]
}
```

分类体系：`work|bill|notification|personal|marketing|spam`
importance：`high|medium|low`
suggested_action：`reply|archive|todo|ignore`

**批量优化**：支持一次最多 20 封，降低 LLM 调用次数。营销类邮件可跳过 LLM（规则预筛）。

### 3.2 `POST /api/email/daily-summary` — 每日总结

pocketd 调度器每日 21:00 触发。

**请求**
```json
{
  "user_id": "local",
  "summary_date": "2026-07-02",
  "emails": [
    { "email_id", "from_name", "subject", "category", "importance", "ai_summary" }
  ]
}
```

**响应**（Markdown）
```json
{
  "content": "# 📬 2026-07-02 邮件摘要\n\n共收到 **23** 封...",
  "total_count": 23,
  "important_count": 5,
  "action_items": [
    { "text": "回复张经理确认 Q3 预算", "source_email_id": "em-xxx" }
  ]
}
```

---

## 4. 数据模型对齐（PG 版 8 表）

pocketd 与 kxmemory **共用同一 PostgreSQL 实例**。pocketd 模块表（task/notes/email/vault）在默认 schema；kxmemory 笔记/知识表放在 `voice_notion` schema（或 `vn_` 前缀）避免冲突。完整 DDL 见 `appendix-a-pg-migration.sql`。

旧 SQLite 方言 → PG 的主要调整：
- `TEXT PRIMARY KEY` 保留（PG TEXT 等同 VARCHAR 无限）
- `BOOLEAN DEFAULT TRUE` 原生（SQLite 用 INTEGER 0/1）
- `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` 保留（PG 原生）
- `tags TEXT -- JSON array` → `tags JSONB`（PG 原生 JSONB，可索引）
- `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` PG 支持
- `INSERT OR IGNORE` → `INSERT ... ON CONFLICT DO NOTHING`
- 索引语法保持

---

## 5. kxmemory 服务职责清单（剥离 STT 依赖后）

| Service | 职责 | 从旧 guide 复用 | Phase 1 改造 |
|---------|------|----------------|-------------|
| `VoiceProcessingService` | 编排：分类→SSOT→建笔记→提取结构→smart_links→图谱→L1→通知 | 编排逻辑 | `_transcribe_audio` 移除（转写已由 pocketd 完成），`_identify_speakers` 移除（pyannote 换 Phase 6A sherpa） |
| `AIClassificationService` | `classify(content, context) → Classification`、实体抽取、todo 提取 | 全部 | LLM 客户端从 OpenAI 改为通用（走 opencode-pocket 的 LLM 配置或环境变量） |
| `SSOTManager` | `check_conflicts`、4 种解决策略 | 全部 | 无（纯数据层，用 ssot_conflicts 表 + hybrid_search） |
| `NotionStyleService` | workspace/block/page 层级 | 全部 | 无 |
| `KnowledgeGraphService`（已有）| `build_graph_from_note`、`find_related_knowledge` | 已存在（GraphRAGService）| 无 |

---

## 6. pocketd 侧调用点（与 server_assistant.go TODO 对应）

| pocketd handler | 调用 kxmemory | 时机 |
|----------------|--------------|------|
| `handleNotes` POST | `POST /api/voice/notes/create` | 笔记创建后 |
| `handleNoteOperations` GET | `GET /api/voice/notes/{id}` | 详情请求 |
| `handleNotes`（搜索）| `GET /api/voice/search` | 搜索 |
| `email/scheduler.go` | `POST /api/email/classify` | 新邮件入站后批处理 |
| `email/scheduler.go` | `POST /api/email/daily-summary` | 每日 21:00 |

pocketd 用 Go `net/http` 直接调 kxmemory（不引入新依赖），客户端封装放 `internal/kxmemory/client.go`（Phase 1 后期创建）。

---

## 7. 待定项

- JWT 与 kxmemory 现有认证（Casdoor）的打通方式——确认 kxmemory 是否接受 pocketd 签发的 JWT，还是用服务间固定 token
- `mem_cube_id` 概念是否保留（旧 schema 中 notes.mem_cube_id NOT NULL，但新架构下可能弱化）
- 聊天模块 `/api/chat/*` 接口（Phase 6B 再定义）
