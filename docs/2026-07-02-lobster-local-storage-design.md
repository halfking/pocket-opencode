# 🦞 龙虾硬壳 — 本地加密存储设计

**日期**: 2026-07-02
**阶段**: Phase A（本地存储地基）
**状态**: 设计完成 + 核心代码落地

> 本文档定义龙虾架构（隐私优先本地智能体）的本地数据层：选型、schema、向量检索、加密方案、与可选云同步的关系。对应代码：`frontend/src/native/{local-db,schema,vector}.ts` + `frontend/src/features/notes/notes-store.ts`。

---

## 1. 选型决策

### 数据库：`@capacitor-community/sqlite` v8.1 + SQLCipher

| 候选 | 结论 |
|------|------|
| `@capacitor-community/sqlite` | ✅ **采用**。活跃维护（v8.1.0，2026-03 发布），原生支持 SQLCipher AES-256，`SQLiteDBConnection` 暴露 `loadExtension`（供将来 sqlite-vec）|
| `@capawesome-team/capacitor-sqlite` | ❌ 排除。GitHub 仓库 404、npm 公开 registry 无此包——付费/私有分发，不可用 |
| ObjectBox | ❌ 无 at-rest 加密、无 Capacitor 绑定 |
| Realm | ❌ Atlas SDK 2025-09 EOL |
| DuckDB | ❌ OLAP 引擎，不适合 OLTP 写入 |

**结论**：一个 SQLCipher 加密 .db 文件承载全部用户数据。`community/sqlite` 提供 `createConnection(db, encrypted=true, mode='secret', ...)` 直接启用加密。

### 向量检索：MVP 纯 JS 余弦，演进到 sqlite-vec / chromem-go

| 阶段 | 方案 | 规模上限 | 延迟 |
|------|------|---------|------|
| **MVP（现在）** | 纯 JS `Float32Array` 点积 | ~50,000 条 | 10k 条 ~37ms |
| Phase A 演进 | `loadExtension` 加载 sqlite-vec `.so`（Android）| 百万级 | <5ms |
| Phase B | chromem-go（gomobile）本地索引 | 百万级 | <5ms，含过滤 |

调研已证实 10,000 条 × 1536 维纯 JS 暴力余弦仅需 ~37ms（`dev.to` 基准），对个人笔记场景完全够用。**sqlite-vec/chromem-go 推迟到 5 万条以上**——避免过早优化，也绕开原生扩展加载的所有坑。

---

## 2. 数据库 Schema（`frontend/src/native/schema.ts`）

全部表用 `local_` 前缀，与服务端可选云同步表的 `cloud_` 前缀区分。SQLite 方言。

| 表 | 用途 | 向量 | FTS |
|----|------|------|-----|
| `local_notes` | 语音/文字笔记 | ✓ `local_note_vectors` | ✓ `local_notes_fts` |
| `local_workspaces` | Notion 式工作区 | | |
| `local_smart_links` | RAG 关联 | | |
| `local_todos` | 待办（笔记提取/手动）| | |
| `local_email_accounts` | 邮箱账户（凭证加密）| | |
| `local_emails` | 邮件 | | |
| `local_vault_entries` | 密码箱（密文 blob）| | |
| `local_meetings` + `local_meeting_segments` | 会议/声纹分段 | | |
| `local_chat_messages` + `local_chat_conversations` | 聊天聚合 | | |
| `local_sync_state` | 云同步水位 | | |

### 设计约定
- **时间戳**：统一 `INTEGER`（Unix 毫秒），避免时区问题
- **软删除**：`deleted_at INTEGER`（非 NULL = 已删），保留可恢复
- **向量解耦**：`local_note_vectors` 独立于 `local_notes`，便于重建索引（换嵌入模型时只需清空向量表重算）
- **FTS5 外部内容表**：`local_notes_fts` 映射到 `local_notes`，用触发器（`local_notes_ai/ad/au`）自动同步

---

## 3. 加密方案

### 数据库加密（SQLCipher）
```
用户主密码 → (Argon2id, 本地 salt) → dbSecret
dbSecret → SQLCipher PBKDF2 → AES-256 页级加密
```
- 主密码由用户设置，App 首次启动引导
- `dbSecret` 不落盘明文，运行时内存持有；可用 `@capacitor-community/secure-storage` 或 AndroidKeyStore 持久化（需用户生物特征解锁）
- `localDB.init(dbSecret)` 时 `createConnection(db, encrypted=true, mode='secret')`

### 字段级加密（敏感字段额外加固）
- `local_email_accounts.credential_encrypted`：IMAP 密码用 AES-GCM（本地主密钥）再加密一层，即使 dbSecret 泄露，邮件凭证仍安全
- `local_vault_entries.entry_ciphertext`：密码箱条目用独立 VeK（见密码箱设计文档），与 dbSecret 解耦

---

## 4. 向量检索设计（`frontend/src/native/vector.ts`）

### 数据流
```
笔记创建 → content(文本) → pocketd /api/embed → 嵌入 API → 向量(1536维) → 本地 local_note_vectors
查询     → queryText    → pocketd /api/embed → 向量       → 本地 JS 点积   → TopK
```

**隐私保证**：pocketd 只收到文本片段，不见 `audio_path`、`tags`、`workspace` 等元数据。嵌入 API（OpenAI text-embedding-3-small）也只见片段。

### VectorIndex 实现
- App 启动时 `load()` 把全部向量加载到连续 `Float32Array`（10k 条 × 1536 维 × 4B ≈ 61MB，可接受）
- `search()` 暴力点积（归一化后 = 余弦相似度）
- `add()`/`remove()` 增量/全量更新内存索引
- 所有向量 **L2 归一化存储**，使点积直接等于余弦相似度

### 混合检索（RRF）
`notes-store.ts` 的 `searchHybrid` 用 Reciprocal Rank Fusion 融合 FTS5 BM25 + 向量余弦：
```
score = Σ 1/(60 + rank_i)    // 各路结果按排名贡献
```
这是 2025-2026 移动端 RAG 的推荐融合方法，无界、无需归一化各路分数。

---

## 5. 与服务端（pocketd）的关系

**铁律：服务端默认无状态。** 本地表全部只在手机本地，pocketd 不存它们。

| 场景 | 客户端发什么 | pocketd 做什么 | pocketd 存什么 |
|------|------------|--------------|--------------|
| 笔记嵌入 | content 文本 | 转发嵌入 API | 无（纯转发）|
| 笔记分类 | content 片段 | 转发 LLM | 无 |
| 邮件分类 | snippet 片段 | 转发 LLM | 无 |
| 转写 | 音频文件 | 转发 Whisper | 无 |
| **云同步**（可选）| 整库加密 blob | 存 blob | **仅密文**，无私钥 |

pocketd 的 `/api/vault/sync/` 是**唯一持久化端点**，且只存用户密钥加密的 blob，服务端零知识。

---

## 6. 演进路径

```
[现在] SQLCipher + JS 余弦 + FTS5
   ↓ 数据量 > 5万 OR 需要 SQL 过滤的语义搜索
[Phase A 演进] loadExtension + sqlite-vec .so (Android)
   ↓ 需要本地推理/复杂 RAG/工具编排
[Phase B] chromem-go (gomobile) 本地智能体核心
```

### sqlite-vec 集成检查清单（将来）
1. 确认 `community/sqlite` 构建是否启用 `load_extension`（SQLCipher 默认禁用，需 `SQLITE_ENABLE_LOAD_EXTENSION` 编译宏）
2. 编译 sqlite-vec `vec0.so` for `arm64-v8a`（参考 `asg017/sqlite-vec` 的 Android build）
3. `localDB.tryLoadVecExtension(soPath)` 尝试加载，失败则保持 JS 兜底
4. `vec0` 虚拟表 + SQL 过滤：`SELECT * FROM vec0 WHERE ... ORDER BY distance LIMIT k`

---

## 7. 范本 Store 模式（`notes-store.ts`）

所有 feature store 遵循同一模式：
1. **写**：只进 `localDB`（加密）
2. **嵌入**：`embedAndStore()` 异步发片段给 pocketd，失败不阻塞写入
3. **搜索**：`searchFullText`（FTS）/ `searchSemantic`（向量）/ `searchHybrid`（RRF 融合）
4. **删除**：软删除 + 移除向量

后续 emails-store / vault-store / meetings-store 照此实现。
