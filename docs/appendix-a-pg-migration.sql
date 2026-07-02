-- ============================================
-- KxMemory Voice-Notion 数据库迁移（PostgreSQL 版）
-- 由 SQLite 版 appendix-a-voice-notion-migration.sql 改写而来（Phase 1）。
-- 主要方言调整：
--   - tags/properties JSON → JSONB（可索引）
--   - BOOLEAN 原生（SQLite 用 INTEGER 0/1）
--   - 触发器改用 PG 的 CREATE FUNCTION + CREATE TRIGGER
--   - 放在独立 schema voice_notion，与 pocketd 的 task/email/vault 表隔离
-- 版本: v1.0.0-pg | 日期: 2026-07-02
-- ============================================

-- 独立 schema，避免与 pocketd 模块表（public.task/email/vault/notes）冲突
CREATE SCHEMA IF NOT EXISTS voice_notion;
SET search_path TO voice_notion, public;

BEGIN;

-- ============================================
-- 1. 笔记表 (Notion Page)
-- ============================================
CREATE TABLE IF NOT EXISTS notes (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    mem_cube_id TEXT,                          -- 弱化为可空（新架构下概念淡化）
    workspace_id TEXT,

    title TEXT,
    content TEXT NOT NULL,
    content_type TEXT DEFAULT 'voice' CHECK(content_type IN ('voice', 'text', 'mixed')),

    domain TEXT CHECK(domain IN ('work', 'study', 'life', 'idea')),
    category TEXT,
    tags JSONB DEFAULT '[]'::jsonb,

    parent_id TEXT,
    position INTEGER DEFAULT 0,

    voice_session_id TEXT,
    audio_file_path TEXT,
    audio_duration INTEGER,
    transcript_path TEXT,

    is_latest BOOLEAN DEFAULT TRUE,
    previous_version TEXT,
    version_number INTEGER DEFAULT 1,

    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    created_by_voice BOOLEAN DEFAULT TRUE,

    view_count INTEGER DEFAULT 0,
    edit_count INTEGER DEFAULT 0,
    reference_count INTEGER DEFAULT 0,

    FOREIGN KEY (parent_id) REFERENCES notes(id) ON DELETE CASCADE,
    FOREIGN KEY (workspace_id) REFERENCES workspaces(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_notes_user_domain ON notes(user_id, domain);
CREATE INDEX IF NOT EXISTS idx_notes_workspace ON notes(workspace_id);
CREATE INDEX IF NOT EXISTS idx_notes_parent ON notes(parent_id);
CREATE INDEX IF NOT EXISTS idx_notes_created_at ON notes(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notes_latest ON notes(is_latest) WHERE is_latest = TRUE;
-- GIN 索引让 tags JSONB 包含查询高效
CREATE INDEX IF NOT EXISTS idx_notes_tags ON notes USING GIN (tags);

-- ============================================
-- 2. 工作区表 (Notion Workspace)
-- ============================================
CREATE TABLE IF NOT EXISTS workspaces (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    icon TEXT,
    description TEXT,
    layout_config JSONB DEFAULT '{}'::jsonb,
    is_default BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_workspaces_user ON workspaces(user_id);

-- ============================================
-- 3. 知识块表 (Notion Block)
-- ============================================
CREATE TABLE IF NOT EXISTS knowledge_blocks (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL,
    block_type TEXT NOT NULL CHECK(block_type IN ('text', 'heading', 'todo', 'code', 'quote', 'image', 'audio')),
    content TEXT,
    position INTEGER DEFAULT 0,
    properties JSONB DEFAULT '{}'::jsonb,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_blocks_note ON knowledge_blocks(note_id, position);

-- ============================================
-- 4. 待办事项表
-- ============================================
CREATE TABLE IF NOT EXISTS todos (
    id TEXT PRIMARY KEY,
    note_id TEXT,
    user_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT,
    status TEXT DEFAULT 'pending' CHECK(status IN ('pending', 'in_progress', 'completed', 'cancelled')),
    priority TEXT DEFAULT 'medium' CHECK(priority IN ('low', 'medium', 'high', 'urgent')),
    due_date TIMESTAMP,
    completed_at TIMESTAMP,
    assigned_to TEXT,
    tags JSONB DEFAULT '[]'::jsonb,
    extracted_from_voice BOOLEAN DEFAULT FALSE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE SET NULL
);

CREATE INDEX IF NOT EXISTS idx_todos_user_status ON todos(user_id, status);
CREATE INDEX IF NOT EXISTS idx_todos_due_date ON todos(due_date);
CREATE INDEX IF NOT EXISTS idx_todos_priority ON todos(priority);

-- ============================================
-- 5. 智能关联表
-- ============================================
CREATE TABLE IF NOT EXISTS smart_links (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    link_type TEXT NOT NULL CHECK(link_type IN ('references', 'updates', 'contradicts', 'complements', 'related_to')),
    confidence REAL DEFAULT 0.0,
    reason TEXT,
    created_by TEXT DEFAULT 'ai',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (source_id) REFERENCES notes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_id) REFERENCES notes(id) ON DELETE CASCADE,
    UNIQUE(source_id, target_id, link_type)
);

CREATE INDEX IF NOT EXISTS idx_smart_links_source ON smart_links(source_id);
CREATE INDEX IF NOT EXISTS idx_smart_links_target ON smart_links(target_id);
CREATE INDEX IF NOT EXISTS idx_smart_links_type ON smart_links(link_type);

-- ============================================
-- 6. SSOT 冲突记录表
-- ============================================
CREATE TABLE IF NOT EXISTS ssot_conflicts (
    id TEXT PRIMARY KEY,
    note_1 TEXT NOT NULL,
    note_2 TEXT NOT NULL,
    conflict_type TEXT CHECK(conflict_type IN ('update', 'contradiction', 'complement', 'duplicate')),
    similarity_score REAL,
    resolution_status TEXT DEFAULT 'pending' CHECK(resolution_status IN ('pending', 'auto_resolved', 'user_resolved', 'ignored')),
    resolution_type TEXT,                       -- merge_update/merge_complement/keep_both/ignore
    resolution_result TEXT,
    resolved_by TEXT,
    resolved_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (note_1) REFERENCES notes(id) ON DELETE CASCADE,
    FOREIGN KEY (note_2) REFERENCES notes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_conflicts_status ON ssot_conflicts(resolution_status);
CREATE INDEX IF NOT EXISTS idx_conflicts_created ON ssot_conflicts(created_at DESC);

-- ============================================
-- 7. AI 分类历史表
-- ============================================
CREATE TABLE IF NOT EXISTS classification_history (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL,
    domain TEXT,
    category TEXT,
    tags JSONB DEFAULT '[]'::jsonb,
    confidence REAL,
    classification_data JSONB DEFAULT '{}'::jsonb,
    user_corrected BOOLEAN DEFAULT FALSE,
    corrected_domain TEXT,
    corrected_category TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_classification_note ON classification_history(note_id);
CREATE INDEX IF NOT EXISTS idx_classification_corrected ON classification_history(user_corrected) WHERE user_corrected = TRUE;

-- ============================================
-- 8. 用户偏好设置表
-- ============================================
CREATE TABLE IF NOT EXISTS user_preferences (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL UNIQUE,

    voice_language TEXT DEFAULT 'zh',
    auto_transcribe BOOLEAN DEFAULT TRUE,
    auto_classify BOOLEAN DEFAULT TRUE,

    default_domain TEXT,
    default_workspace_id TEXT,
    custom_categories JSONB DEFAULT '[]'::jsonb,

    auto_resolve_conflicts BOOLEAN DEFAULT TRUE,
    conflict_resolution_strategy TEXT DEFAULT 'auto',

    default_view TEXT DEFAULT 'list',
    theme TEXT DEFAULT 'system',

    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (default_workspace_id) REFERENCES workspaces(id)
);

-- ============================================
-- 9. 视图：用户笔记统计
-- ============================================
CREATE OR REPLACE VIEW user_note_stats AS
SELECT
    user_id,
    COUNT(*) AS total_notes,
    SUM(CASE WHEN created_by_voice THEN 1 ELSE 0 END) AS voice_notes,
    SUM(CASE WHEN domain = 'work' THEN 1 ELSE 0 END) AS work_notes,
    SUM(CASE WHEN domain = 'study' THEN 1 ELSE 0 END) AS study_notes,
    SUM(CASE WHEN domain = 'life' THEN 1 ELSE 0 END) AS life_notes,
    SUM(CASE WHEN domain = 'idea' THEN 1 ELSE 0 END) AS idea_notes,
    MAX(created_at) AS last_note_at
FROM notes
WHERE is_latest = TRUE
GROUP BY user_id;

-- ============================================
-- 10. 触发器：自动更新时间戳（PG 函数 + 触发器）
-- ============================================
CREATE OR REPLACE FUNCTION touch_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS update_notes_timestamp ON notes;
CREATE TRIGGER update_notes_timestamp
BEFORE UPDATE ON notes
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

DROP TRIGGER IF EXISTS update_todos_timestamp ON todos;
CREATE TRIGGER update_todos_timestamp
BEFORE UPDATE ON todos
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

COMMIT;

-- ============================================
-- 验证迁移
-- ============================================
SELECT 'Voice Notion PG 迁移完成' AS status;

SELECT tablename AS name, 'table' AS type
FROM pg_tables WHERE schemaname = 'voice_notion'
UNION ALL
SELECT indexname, 'index' FROM pg_indexes WHERE schemaname = 'voice_notion'
ORDER BY type, name;
