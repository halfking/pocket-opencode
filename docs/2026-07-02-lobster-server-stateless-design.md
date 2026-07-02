# 🦞 龙虾触须 — pocketd 无状态 AI 网关设计

**日期**: 2026-07-02
**阶段**: Phase C（pocketd 无状态化）
**状态**: 设计完成 + 核心代码落地（build/vet/test 通过）

> 本文档定义 pocketd 在龙虾架构中的新定位：从 Phase 0 的"数据主存"降级为"纯无状态 AI 计算代理"。用户数据全部在手机本地（见 `lobster-local-storage-design.md`），pocketd 只做 AI 转发，不落地任何用户数据。唯一例外是可选加密云同步（仅存密文 blob）。

---

## 1. 定位转变

| 维度 | Phase 0（旧）| Phase C（新）|
|------|------------|------------|
| **数据主存** | pocketd PostgreSQL（task/notes/email/vault 4 表）| 手机本地 SQLCipher 加密库 |
| **pocketd 职责** | 存数据 + 提供 CRUD API | **纯 AI 计算**（嵌入/LLM/Whisper），不存数据 |
| **kxmemory 职责** | 笔记主存 + AI 编排 | **纯 AI 编排服务**（分类/SSOT/总结的 LLM 调用）|
| **客户端** | 瘦客户端，依赖后端 | **胖客户端**（龙虾），自带本地库 + 向量索引 |
| **隐私** | 服务端持有全部明文数据 | **服务端零知识**，只见临时片段 |

---

## 2. 路由分类

### 无状态 AI 路由（Phase C 新增，核心）

| 路由 | 方法 | 请求 | 响应 | 存储 |
|------|------|------|------|------|
| `/api/embed` | POST | `{text}` | `{embedding, model, dim}` | ❌ 不存 |
| `/api/llm/chat` | POST | `{messages, model?}` | `{content, model}` | ❌ 不存 |
| `/api/stt/transcribe` | POST | 音频文件 | `{text}` | ❌ 不存 |
| `/api/classify` | POST | `{text}` | `{domain, category, tags}` | ❌ 不存 |

**隐私契约**：这些 handler 只转发请求给 AI 提供商，不写任何持久存储。日志只记请求大小和耗时，**绝不记请求内容**。代码在 `internal/aigate/` + `server_assistant.go:handleEmbed/handleLLMChat`。

### 可选云同步路由（默认零持久化，仅 vault blob）

> ⚠️ **架构现状说明（2026-07-02 审计修正）**：本设计的"pocketd 不持久化用户数据"是**目标态**。
> 实际代码中，当配置了 `POCKET_POSTGRES_DSN`（生产部署常用）时，Phase 0 的 task/notes/email
> store 会**自动启用并写入 PG**（用于任务三源聚合缓存、笔记云模式、邮件抓取存储）。
> 只有 **vault store** 的数据是端到端加密 blob（服务端零知识）；task/notes/email 在当前代码下
> 是**明文**存 PG。这是 Phase 0 → Phase C 转型的遗留，完整落地"零持久化"需后续 Phase 把
> task/notes/email 也改为客户端加密上传。当前阶段：**vault 是加密零知识端点；其余 PG 表
> 在云模式启用时明文存储（仅服务端可见，不外泄）**。

| 路由 | 用途 | 存储 |
|------|------|------|
| `/api/vault/sync/` | 用户开启云同步后上传加密 blob | ✅ **仅密文**，服务端无私钥 |

复用 Phase 0 的 vault store（多版本 + is_current + ListVersions），但存的永远是客户端用用户密钥加密的 blob，pocketd 无法解密。

### 认证路由

| 路由 | 用途 |
|------|------|
| `/api/auth/login` | 签发 JWT（嵌入/LLM 调用需认证）|

### 云模式专用路由（Phase 0 残留，默认禁用）

`/api/notes`、`/api/email/*`、`/api/tasks` 这些 Phase 0 路由在新架构下**默认不启用**。它们只在用户明确选择"云存储模式"时激活（保留代码不删除，定位改为可选云后端）。MVP 阶段龙虾客户端不调用它们。

---

## 3. `internal/aigate/` 包

无状态 AI 网关的客户端抽象，OpenAI 兼容（可指向 OpenAI / Groq / 本地）：

```go
type Embedder interface {
    Embed(ctx, text) (embedding []float32, model string, err error)
}
type LLMClient interface {
    Chat(ctx, model, messages) (content string, err error)
}
```

### 配置（`config.go` Phase C 字段）
| 环境变量 | 默认 | 说明 |
|---------|------|------|
| `POCKET_EMBED_BASE_URL` | `https://api.openai.com/v1` | 嵌入 API 地址 |
| `POCKET_EMBED_API_KEY` | — | 嵌入密钥（也接受 `POCKET_OPENAI_API_KEY`）|
| `POCKET_EMBED_MODEL` | `text-embedding-3-small` | 嵌入模型（1536 维）|
| `POCKET_LLM_BASE_URL` | `https://api.groq.com/openai/v1` | LLM API 地址 |
| `POCKET_LLM_API_KEY` | — | LLM 密钥（也接受 `POCKET_GROQ_API_KEY`）|
| `POCKET_LLM_MODEL` | — | 默认 LLM 模型（客户端可覆盖）|

### 构造逻辑（`main.go`）
- `EmbedAPIKey` 非空 → 构造 embedder，启用 `/api/embed`
- `LLMAPIKey` 非空 → 构造 llm，启用 `/api/llm/chat`
- 任一为空 → 对应 handler 返回 503 + 明确提示

---

## 4. 数据流（隐私视角）

### 笔记嵌入（最高频）
```
龙虾客户端                          pocketd（无状态）              嵌入 API
    │                                  │                            │
    │  POST /api/embed                 │                            │
    │  {text: "笔记内容片段"}           │                            │
    │  (只发文本，不发元数据)            │                            │
    ├─────────────────────────────────>│                            │
    │                                  │  POST /embeddings          │
    │                                  │  {input: "笔记内容片段"}    │
    │                                  ├───────────────────────────>│
    │                                  │                            │
    │                                  │  {embedding: [0.1,...]}    │
    │                                  │<───────────────────────────┤
    │  {embedding, model, dim}         │                            │
    │  (pocketd 不存储)                 │                            │
    │<─────────────────────────────────┤                            │
    │                                                                 │
    │  向量存本地 local_note_vectors（加密）                           │
```

**关键**：pocketd 只在请求生命周期内持有文本片段，响应返回后即丢弃。无 DB 写入、无日志记内容。

### LLM 分类
```
龙虾发 snippet 片段 → /api/llm/chat → pocketd 转发 → LLM 返回 → 龙虾存本地
```
客户端只发邮件/笔记的 snippet（前 ~500 字），不发完整内容。

### 转写（Whisper）
```
龙虾发音频文件 → /api/stt/transcribe → pocketd 转发 Groq Whisper → 文本回 → 龙虾存本地
```

---

## 5. 对 Phase 0 代码的处置

| Phase 0 产出 | 新处置 |
|-------------|--------|
| `internal/aigate/`（新）| ✅ Phase C 核心，无状态网关 |
| `internal/db/pg.go` + PG 池 | ⚠️ 保留但降级：只服务"云同步模式"的 vault blob 存储 |
| `task/notes/email` 3 个 PG store | ⚠️ 保留代码，定位改为"云模式可选后端"，默认不部署 |
| `vault` store（多版本）| ✅ 保留并启用：唯一持久化端点（加密 blob）|
| server.go 12 路由 + handler | ✅ 保留 + 新增 `/api/embed`、`/api/llm/chat` |
| config 新字段 | ✅ 全保留 + 新增 6 个 embed/llm 字段 |

**结论：Phase 0 工作不浪费，vault store 直接复用为云同步后端，其他 store 定位降级。**

---

## 6. 安全与隐私保证

1. **无持久化**：`/api/embed`、`/api/llm/chat`、`/api/stt/transcribe`、`/api/classify` 不写任何 DB/文件
2. **内容不入日志**：只记 `{ts, route, status, bytes, duration_ms}`，绝不记 `body.text`/`body.messages`
3. **认证**：所有路由经 JWT 中间件（复用 Phase 0 `JWTSecret`）
4. **限流**（待补）：单用户嵌入/LLM 调用频率限制，防滥用
5. **超时**：嵌入 30s，LLM 60s，转写 120s（音频文件大）
6. **审计**：将来可加"数据流审计日志"——证明无持久化的可验证声明

---

## 7. kxmemory 的新定位

kxmemory（FastAPI）从"笔记主存"改为"AI 编排服务"：
- **不再持久化**笔记/知识数据（PG 8 表改为可选，默认不部署）
- **只做 LLM 编排**：分类 prompt、SSOT 检测的 LLM 调用、每日邮件总结生成
- pocketd 可选地把 LLM 请求转发给 kxmemory（复杂编排），或直接转发给 LLM API（简单调用）

`docs/2026-07-02-kxmemory-api-contract.md` 的定位调整为：**无状态 AI 计算 API**（非数据主存 API）。

---

## 8. 演进路径

```
[现在] pocketd 无状态网关（/embed /llm/chat）+ vault blob 同步
   ↓ 需要复杂 RAG 编排
[Phase B] 龙虾客户端本地 chromem-go + MCP，减少对云端编排的依赖
   ↓ 需要云端协作（多设备实时同步）
[Phase E] 增量加密同步（基于 vault blob + CRDT）
```

pocketd 的无状态化让架构天然可扩展——加 AI 能力只需加无状态路由，不碰数据层。
