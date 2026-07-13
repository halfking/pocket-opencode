/**
 * schema.ts — 龙虾本地加密库的表结构定义（SQLite 方言）。
 *
 * 设计原则：
 * - 所有表带前缀 `local_`，与云同步表的 `cloud_` 前缀区分
 * - 时间戳统一用 INTEGER（Unix 毫秒），避免跨时区问题
 * - 向量单独存 `_vectors` 表（Float32Array 序列化为 BLOB），与文本解耦
 * - 全文搜索用 FTS5 外部内容表，与主表同步
 * - 软删除用 `deleted_at INTEGER`（非 NULL = 已删除），保留可恢复
 *
 * 与服务端（pocketd）的关系：这些表只在手机本地，pocketd 不存它们。
 * 云同步（可选）时整库导出为加密 blob 上传，非逐表同步。
 */
export const SCHEMA_SQL = `
-- ============================================================
-- 语音笔记（核心数据）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_notes (
    id TEXT PRIMARY KEY,
    workspace_id TEXT,
    title TEXT,
    content TEXT NOT NULL,           -- Markdown 正文
    content_type TEXT DEFAULT 'voice', -- voice / text / mixed
    domain TEXT,                     -- work / study / life / idea
    category TEXT,
    tags TEXT,                       -- JSON array string
    audio_path TEXT,                 -- 本地文件路径（转写后可清理）
    audio_duration_ms INTEGER DEFAULT 0,
    created_by_voice INTEGER DEFAULT 1,  -- BOOLEAN as 0/1
    embedding_model TEXT,            -- 生成向量用的模型，便于重建
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_notes_domain ON local_notes(domain) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_notes_updated ON local_notes(updated_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_notes_workspace ON local_notes(workspace_id) WHERE deleted_at IS NULL;

-- FTS5 全文索引（外部内容表，与 local_notes 同步）
CREATE VIRTUAL TABLE IF NOT EXISTS local_notes_fts USING fts5(
    title, content, content='local_notes', content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);

-- 笔记增删改时同步 FTS 的触发器
CREATE TRIGGER IF NOT EXISTS local_notes_ai AFTER INSERT ON local_notes BEGIN
    INSERT INTO local_notes_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
END;
CREATE TRIGGER IF NOT EXISTS local_notes_ad AFTER DELETE ON local_notes BEGIN
    INSERT INTO local_notes_fts(local_notes_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
END;
CREATE TRIGGER IF NOT EXISTS local_notes_au AFTER UPDATE ON local_notes BEGIN
    INSERT INTO local_notes_fts(local_notes_fts, rowid, title, content) VALUES ('delete', old.rowid, old.title, old.content);
    INSERT INTO local_notes_fts(rowid, title, content) VALUES (new.rowid, new.title, new.content);
END;

-- ============================================================
-- 笔记向量（与 local_notes 1:1，独立存储便于重建）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_note_vectors (
    note_id TEXT PRIMARY KEY,
    embedding BLOB NOT NULL,         -- Float32Array 序列化：4 bytes/维
    dim INTEGER NOT NULL,            -- 维度（如 1536）
    model TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (note_id) REFERENCES local_notes(id) ON DELETE CASCADE
);

-- ============================================================
-- 工作区（Notion 式层级）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    icon TEXT,
    description TEXT,
    is_default INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- ============================================================
-- 智能关联（RAG 检索出的关联）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_smart_links (
    id TEXT PRIMARY KEY,
    source_note_id TEXT NOT NULL,
    target_note_id TEXT NOT NULL,
    link_type TEXT NOT NULL,         -- references / updates / contradicts / complements / related_to
    confidence REAL DEFAULT 0,
    reason TEXT,
    created_at INTEGER NOT NULL,
    FOREIGN KEY (source_note_id) REFERENCES local_notes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_note_id) REFERENCES local_notes(id) ON DELETE CASCADE,
    UNIQUE(source_note_id, target_note_id, link_type)
);
CREATE INDEX IF NOT EXISTS idx_links_source ON local_smart_links(source_note_id);
CREATE INDEX IF NOT EXISTS idx_links_target ON local_smart_links(target_note_id);

-- ============================================================
-- 待办（从笔记提取或手动创建）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_todos (
    id TEXT PRIMARY KEY,
    note_id TEXT,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT DEFAULT 'pending',   -- pending / in_progress / completed / cancelled
    priority TEXT DEFAULT 'medium',  -- low / medium / high / urgent
    due_at INTEGER,
    completed_at INTEGER,
    tags TEXT,
    extracted_from_voice INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (note_id) REFERENCES local_notes(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_todos_status ON local_todos(status);
CREATE INDEX IF NOT EXISTS idx_todos_due ON local_todos(due_at) WHERE due_at IS NOT NULL;

-- ============================================================
-- 邮箱账户（凭证本地加密）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_email_accounts (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    email_address TEXT NOT NULL,
    imap_host TEXT NOT NULL,
    imap_port INTEGER DEFAULT 993,
    auth_type TEXT DEFAULT 'password',
    credential_encrypted TEXT NOT NULL,  -- 用本地主密钥 AES-GCM 加密的 IMAP 密码/token
    sync_interval_min INTEGER DEFAULT 15,
    last_synced_uid INTEGER,
    last_synced_at INTEGER,
    rules TEXT,                      -- JSON: {whitelist, keywords, blacklist}
    enabled INTEGER DEFAULT 1,
    created_at INTEGER NOT NULL
);

-- ============================================================
-- 邮件（抓取后本地存）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_emails (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    message_id TEXT,                 -- IMAP Message-ID，去重用
    uid INTEGER,
    from_address TEXT NOT NULL,
    from_name TEXT,
    subject TEXT,
    snippet TEXT,                    -- 正文前 ~500 字
    date INTEGER NOT NULL,
    is_read INTEGER DEFAULT 0,
    is_starred INTEGER DEFAULT 0,
    category TEXT,                   -- work / bill / notification / personal / marketing / spam
    importance TEXT,                 -- high / medium / low
    ai_summary TEXT,                 -- LLM 分类时返回的摘要（只发 snippet 给 LLM）
    suggested_action TEXT,
    has_attachments INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    UNIQUE(account_id, message_id),
    FOREIGN KEY (account_id) REFERENCES local_email_accounts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_emails_date ON local_emails(date DESC);
CREATE INDEX IF NOT EXISTS idx_emails_unread ON local_emails(is_read) WHERE is_read = 0;

-- ============================================================
-- 密码箱条目（敏感度最高，VeK 加密的密文存此处）
-- 注：cap-keystore 原生插件管理加解密，本表存的是已加密 blob
-- ============================================================
CREATE TABLE IF NOT EXISTS local_vault_entries (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    username TEXT,
    url TEXT,
    entry_ciphertext TEXT NOT NULL,  -- AES-GCM 密文（含 iv 前缀 + 密文，见 native/crypto.ts）
    iv TEXT DEFAULT '',              -- 兼容字段（密文已内含 iv，保留列避免迁移）
    category TEXT,                   -- login / card / note / identity
    icon TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    last_used_at INTEGER
);

-- ============================================================
-- 会议（Phase 6A）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_meetings (
    id TEXT PRIMARY KEY,
    title TEXT,
    audio_path TEXT,
    duration_ms INTEGER,
    transcript TEXT,                 -- 完整转写（本地存）
    summary TEXT,                    -- AI 纪要（发片段生成）
    started_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL,
    deleted_at INTEGER
);

-- ============================================================
-- 会议分段（声纹 + 时间戳）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_meeting_segments (
    id TEXT PRIMARY KEY,
    meeting_id TEXT NOT NULL,
    speaker_label TEXT,              -- 说话人（声纹聚类后）
    start_ms INTEGER NOT NULL,
    end_ms INTEGER NOT NULL,
    text TEXT NOT NULL,
    FOREIGN KEY (meeting_id) REFERENCES local_meetings(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_segments_meeting ON local_meeting_segments(meeting_id, start_ms);

-- ============================================================
-- 聊天消息（Phase 6B，三路抓取后本地存）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_chat_messages (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,            -- wechat / sms / telegram / feishu
    conversation_id TEXT NOT NULL,
    sender TEXT,
    text TEXT NOT NULL,
    ts INTEGER NOT NULL,
    is_outgoing INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_conv ON local_chat_messages(conversation_id, ts);

CREATE TABLE IF NOT EXISTS local_chat_conversations (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    name TEXT,
    last_message_at INTEGER,
    unread_count INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL
);

-- ============================================================
-- 同步状态追踪（哪些表已导出到云 blob）
-- ============================================================
CREATE TABLE IF NOT EXISTS local_sync_state (
    table_name TEXT PRIMARY KEY,
    last_synced_at INTEGER,
    last_synced_rowid INTEGER,
    pending_changes INTEGER DEFAULT 0
);

-- ============================================================
-- S0-C: Lobster Vault 3.x — 统一 Asset 抽象
--
-- 设计目标（spec §3.2 决策 4）：把新业务（S1 PKM block、S2 会议录音 blob、
-- S3 凭证图、附件等）收敛到统一的 Asset 模型，而不是继续为每种业务加表。
--
-- Asset = (id, workspace_id, kind, title, meta_json, sync_mode, blobs[], vectors[])
--   - kind: note / meeting_audio / meeting_transcript / voucher_image / pdf /
--           pdf_attachment / voice_memo / screenshot / mixed / ...
--   - meta_json: 业务自定义字段（block 树、标签、关联 id 等）
--   - sync_mode: e2ee_local_first | cloud_authoritative | cloud_readonly
--
-- 与老表的关系：local_notes / local_emails / local_vault_entries /
-- local_meetings 保持不动（已实现的特定业务），新业务一律走 Asset。
-- 老业务可在后续 sprint 通过适配器逐步迁移到 Asset。
--
-- 加密：body_text / meta_json 由调用方决定是否加密（敏感字段走
-- encryptString，元数据如 title 留明文以便 FTS 检索）。
-- blob 文件（录音/图片）单独走 AES-GCM 分块加密，存 local_asset_blobs。
-- ============================================================
CREATE TABLE IF NOT EXISTS local_assets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL DEFAULT 'default',
    kind TEXT NOT NULL,                -- note / meeting_audio / voucher_image / ...
    title TEXT,                        -- 明文，便于 FTS + 列表展示
    body_text TEXT DEFAULT '',         -- 主体文本（可能加密，由调用方决定）
    body_encrypted INTEGER DEFAULT 0,  -- 1 = body_text 是密文
    meta_json TEXT DEFAULT '{}',       -- 业务自定义元数据（JSON）
    source TEXT,                       -- voice / share / email / clipper / pdf / manual
    sync_mode TEXT NOT NULL DEFAULT 'e2ee_local_first',
    -- e2ee_local_first | cloud_authoritative | cloud_readonly
    client_rev INTEGER NOT NULL DEFAULT 1,  -- 客户端修订号，每次更新 +1
    server_rev INTEGER DEFAULT 0,      -- 服务端已知修订号（0 = 未同步）
    dirty INTEGER NOT NULL DEFAULT 1,  -- 1 = 本地改动待推送
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER                 -- 非 NULL = 软删除（墓碑）
);
CREATE INDEX IF NOT EXISTS idx_assets_workspace_kind ON local_assets(workspace_id, kind) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_assets_updated ON local_assets(updated_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_assets_dirty ON local_assets(dirty) WHERE dirty = 1 AND deleted_at IS NULL;

-- Asset FTS（仅索引明文 title + body；加密的 body 不进 FTS）
CREATE VIRTUAL TABLE IF NOT EXISTS local_assets_fts USING fts5(
    title, body, content='local_assets', content_rowid='rowid',
    tokenize='unicode61 remove_diacritics 2'
);
-- 注意：触发器只在 body_encrypted=0 时才有意义；加密 body 进 FTS 会泄露明文。
-- 这里用 AFTER INSERT/UPDATE 全量同步，调用方负责不把加密 body 写进 body_text
-- （加密内容应进 meta_json 或独立 blob）。
CREATE TRIGGER IF NOT EXISTS local_assets_ai AFTER INSERT ON local_assets BEGIN
    INSERT INTO local_assets_fts(rowid, title, body)
    VALUES (new.rowid, new.title, CASE WHEN new.body_encrypted = 0 THEN new.body_text ELSE '' END);
END;
CREATE TRIGGER IF NOT EXISTS local_assets_ad AFTER DELETE ON local_assets BEGIN
    INSERT INTO local_assets_fts(local_assets_fts, rowid, title, body)
    VALUES ('delete', old.rowid, old.title, CASE WHEN old.body_encrypted = 0 THEN old.body_text ELSE '' END);
END;
CREATE TRIGGER IF NOT EXISTS local_assets_au AFTER UPDATE ON local_assets BEGIN
    INSERT INTO local_assets_fts(local_assets_fts, rowid, title, body)
    VALUES ('delete', old.rowid, old.title, CASE WHEN old.body_encrypted = 0 THEN old.body_text ELSE '' END);
    INSERT INTO local_assets_fts(rowid, title, body)
    VALUES (new.rowid, new.title, CASE WHEN new.body_encrypted = 0 THEN new.body_text ELSE '' END);
END;

-- ============================================================
-- Asset blob（大文件分块加密存储）
--
-- 一个 asset 可有多个 blob（如多图笔记、分块录音）。每个 blob 独立
-- AES-GCM 加密，cipher_text 存库（小文件）或引用外部文件路径（大文件）。
-- ============================================================
CREATE TABLE IF NOT EXISTS local_asset_blobs (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    idx INTEGER NOT NULL DEFAULT 0,     -- blob 顺序（多 blob 时）
    kind TEXT,                          -- image / audio / pdf / file / ...
    cipher_text TEXT,                   -- 小文件：base64(iv + 密文) 直存
    file_path TEXT,                     -- 大文件：外部加密文件路径
    size_bytes INTEGER DEFAULT 0,       -- 原始大小（加密前）
    hash TEXT,                          -- 原始内容 sha256（去重用）
    created_at INTEGER NOT NULL,
    FOREIGN KEY (asset_id) REFERENCES local_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_blobs_asset ON local_asset_blobs(asset_id, idx);

-- ============================================================
-- Asset 向量（语义检索，与 local_note_vectors 平行）
--
-- 一个 asset 可有多个向量（如 block 级 embedding）。独立表便于按 kind
-- 重建索引。查询时复用 vectorIndex 的暴力点积（见 vector.ts）。
-- ============================================================
CREATE TABLE IF NOT EXISTS local_asset_vectors (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    embedding BLOB NOT NULL,            -- Float32Array 序列化
    dim INTEGER NOT NULL,
    model TEXT NOT NULL,
    chunk_idx INTEGER DEFAULT 0,        -- 分块 embedding 的索引（0 = 整篇）
    created_at INTEGER NOT NULL,
    FOREIGN KEY (asset_id) REFERENCES local_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_vectors_asset ON local_asset_vectors(asset_id);
`
