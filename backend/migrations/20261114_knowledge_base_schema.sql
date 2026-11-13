-- Knowledge Base Integration Schema Migration
-- Date: 2026-11-14
-- Phase: 1 - Data Model Enhancement
-- Description: Add notebooks, note links, sync state, and saved searches tables

-- ============================================================================
-- 1. Notebooks Table (笔记本)
-- ============================================================================
CREATE TABLE IF NOT EXISTS notebooks (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    workspace_id TEXT,
    name TEXT NOT NULL,
    parent_id TEXT,  -- 支持嵌套笔记本
    icon TEXT,       -- emoji 图标
    color TEXT,      -- hex 颜色值，如 #3498db
    sort_order INTEGER DEFAULT 0,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY (parent_id) REFERENCES notebooks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notebooks_user_workspace ON notebooks(user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_notebooks_parent ON notebooks(parent_id);
CREATE INDEX IF NOT EXISTS idx_notebooks_sort ON notebooks(user_id, sort_order);

-- ============================================================================
-- 2. Enhance Notes Table (添加 notebook_id)
-- ============================================================================
ALTER TABLE notes ADD COLUMN notebook_id TEXT REFERENCES notebooks(id);
CREATE INDEX IF NOT EXISTS idx_notes_notebook ON notes(notebook_id);

-- ============================================================================
-- 3. Note Links Table (笔记间关联)
-- ============================================================================
CREATE TABLE IF NOT EXISTS note_links (
    id TEXT PRIMARY KEY,
    source_note_id TEXT NOT NULL,
    target_note_id TEXT NOT NULL,
    link_type TEXT NOT NULL,  -- 'related', 'reference', 'parent', 'child', 'bidirectional'
    confidence REAL DEFAULT 1.0,  -- AI 生成的链接有置信度 0-1
    created_by TEXT NOT NULL,  -- 'user' | 'ai'
    reason TEXT,  -- AI 推荐理由
    created_at INTEGER NOT NULL,
    FOREIGN KEY (source_note_id) REFERENCES notes(id) ON DELETE CASCADE,
    FOREIGN KEY (target_note_id) REFERENCES notes(id) ON DELETE CASCADE,
    UNIQUE(source_note_id, target_note_id, link_type)
);

CREATE INDEX IF NOT EXISTS idx_note_links_source ON note_links(source_note_id);
CREATE INDEX IF NOT EXISTS idx_note_links_target ON note_links(target_note_id);
CREATE INDEX IF NOT EXISTS idx_note_links_type ON note_links(link_type);
CREATE INDEX IF NOT EXISTS idx_note_links_created_by ON note_links(created_by);

-- ============================================================================
-- 4. Sync State Table (同步状态追踪)
-- ============================================================================
CREATE TABLE IF NOT EXISTS sync_state (
    note_id TEXT PRIMARY KEY,
    memora_id TEXT,  -- memora 侧的 ID
    content_hash TEXT NOT NULL,  -- SHA256(content)
    last_synced_at INTEGER,
    sync_status TEXT NOT NULL,  -- 'pending', 'synced', 'conflict', 'error'
    error_message TEXT,
    version INTEGER DEFAULT 1,
    retry_count INTEGER DEFAULT 0,
    next_retry_at INTEGER,
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_sync_state_status ON sync_state(sync_status);
CREATE INDEX IF NOT EXISTS idx_sync_state_memora_id ON sync_state(memora_id);
CREATE INDEX IF NOT EXISTS idx_sync_state_retry ON sync_state(next_retry_at) WHERE sync_status = 'error';

-- ============================================================================
-- 5. Saved Searches Table (保存的搜索)
-- ============================================================================
CREATE TABLE IF NOT EXISTS saved_searches (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    query TEXT NOT NULL,
    filters TEXT,  -- JSON: {tags, notebook, dateRange, domain}
    created_at INTEGER NOT NULL,
    last_used_at INTEGER
);

CREATE INDEX IF NOT EXISTS idx_saved_searches_user ON saved_searches(user_id);
CREATE INDEX IF NOT EXISTS idx_saved_searches_last_used ON saved_searches(user_id, last_used_at DESC);

-- ============================================================================
-- 6. Search History Table (搜索历史，可选)
-- ============================================================================
CREATE TABLE IF NOT EXISTS search_history (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    query TEXT NOT NULL,
    filters TEXT,  -- JSON
    results_count INTEGER,
    search_time_ms INTEGER,  -- 搜索耗时
    created_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_search_history_user ON search_history(user_id, created_at DESC);

-- ============================================================================
-- 7. Note Versions Table (笔记版本历史，可选增强)
-- ============================================================================
-- 注意：当前 notes 表已有 version_number 和 parent_id 支持版本链
-- 此表用于存储完整的版本历史元信息
CREATE TABLE IF NOT EXISTS note_versions (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL,
    version_number INTEGER NOT NULL,
    title TEXT,
    content_hash TEXT NOT NULL,  -- SHA256(content)
    change_reason TEXT,  -- 修改原因
    created_by TEXT NOT NULL,  -- 'user' | 'system' | 'sync'
    created_at INTEGER NOT NULL,
    FOREIGN KEY (note_id) REFERENCES notes(id) ON DELETE CASCADE,
    UNIQUE(note_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_note_versions_note ON note_versions(note_id, version_number DESC);

-- ============================================================================
-- 8. Tags Table (标签表，用于自动补全和统计)
-- ============================================================================
CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    usage_count INTEGER DEFAULT 0,  -- 使用次数
    last_used_at INTEGER,
    color TEXT,  -- 可选的标签颜色
    UNIQUE(user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tags_user ON tags(user_id);
CREATE INDEX IF NOT EXISTS idx_tags_usage ON tags(user_id, usage_count DESC);

-- ============================================================================
-- 9. Knowledge Graph Edges Table (知识图谱边，用于可视化)
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_edges (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,  -- 'note_link', 'tag_similarity', 'temporal', 'ai_related'
    weight REAL DEFAULT 1.0,  -- 权重，用于图谱布局
    metadata TEXT,  -- JSON，存储额外信息
    created_at INTEGER NOT NULL,
    UNIQUE(source_id, target_id, edge_type)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_edges_source ON knowledge_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_edges_target ON knowledge_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_edges_type ON knowledge_edges(edge_type);

-- ============================================================================
-- 10. 视图：笔记本统计
-- ============================================================================
CREATE VIEW IF NOT EXISTS notebook_stats AS
SELECT 
    nb.id,
    nb.name,
    nb.user_id,
    nb.parent_id,
    COUNT(DISTINCT n.id) as note_count,
    MAX(n.updated_at) as last_note_updated_at
FROM notebooks nb
LEFT JOIN notes n ON n.notebook_id = nb.id AND n.is_latest = 1
GROUP BY nb.id, nb.name, nb.user_id, nb.parent_id;

-- ============================================================================
-- 11. 视图：标签统计
-- ============================================================================
CREATE VIEW IF NOT EXISTS tag_stats AS
SELECT 
    t.id,
    t.user_id,
    t.name,
    COUNT(DISTINCT n.id) as note_count,
    MAX(n.updated_at) as last_used_at
FROM tags t
LEFT JOIN notes n ON n.is_latest = 1 
    AND ('["' || t.name || '"]' IN (n.tags) OR n.tags LIKE '%"' || t.name || '"%')
GROUP BY t.id, t.user_id, t.name;

-- ============================================================================
-- 12. 全文搜索索引（SQLite FTS5）
-- ============================================================================
-- 为笔记创建全文搜索表
CREATE VIRTUAL TABLE IF NOT EXISTS notes_fts USING fts5(
    note_id UNINDEXED,
    title,
    content,
    tags,
    tokenize = 'unicode61 remove_diacritics 2'
);

-- 初始化 FTS 表（需要在应用启动时执行）
-- INSERT INTO notes_fts(note_id, title, content, tags)
-- SELECT id, title, content, tags FROM notes WHERE is_latest = 1;

-- 创建触发器自动更新 FTS 索引
CREATE TRIGGER IF NOT EXISTS notes_fts_insert AFTER INSERT ON notes
WHEN NEW.is_latest = 1
BEGIN
    INSERT INTO notes_fts(note_id, title, content, tags)
    VALUES (NEW.id, NEW.title, NEW.content, NEW.tags);
END;

CREATE TRIGGER IF NOT EXISTS notes_fts_update AFTER UPDATE ON notes
WHEN NEW.is_latest = 1
BEGIN
    DELETE FROM notes_fts WHERE note_id = NEW.id;
    INSERT INTO notes_fts(note_id, title, content, tags)
    VALUES (NEW.id, NEW.title, NEW.content, NEW.tags);
END;

CREATE TRIGGER IF NOT EXISTS notes_fts_delete AFTER DELETE ON notes
BEGIN
    DELETE FROM notes_fts WHERE note_id = OLD.id;
END;

-- ============================================================================
-- 13. 数据完整性触发器
-- ============================================================================

-- 删除笔记本时，将其下笔记移到父笔记本（如有）或置空
CREATE TRIGGER IF NOT EXISTS notebook_delete_cascade
BEFORE DELETE ON notebooks
BEGIN
    UPDATE notes 
    SET notebook_id = OLD.parent_id 
    WHERE notebook_id = OLD.id;
END;

-- 更新标签使用统计
CREATE TRIGGER IF NOT EXISTS update_tag_usage
AFTER INSERT ON notes
WHEN NEW.tags IS NOT NULL AND NEW.tags != ''
BEGIN
    -- 这里简化处理，实际应在应用层解析 JSON 数组并更新
    UPDATE tags SET usage_count = usage_count + 1, last_used_at = NEW.created_at
    WHERE user_id = NEW.user_id;
END;

-- ============================================================================
-- 14. 初始数据（可选）
-- ============================================================================

-- 创建默认笔记本
-- INSERT OR IGNORE INTO notebooks (id, user_id, workspace_id, name, icon, color, sort_order, created_at, updated_at)
-- VALUES 
--     ('nb-default', 'local', NULL, '默认笔记本', '📝', '#3498db', 0, strftime('%s', 'now') * 1000, strftime('%s', 'now') * 1000),
--     ('nb-work', 'local', NULL, '工作笔记', '💼', '#2ecc71', 1, strftime('%s', 'now') * 1000, strftime('%s', 'now') * 1000),
--     ('nb-study', 'local', NULL, '学习笔记', '📚', '#9b59b6', 2, strftime('%s', 'now') * 1000, strftime('%s', 'now') * 1000),
--     ('nb-life', 'local', NULL, '生活笔记', '🏠', '#f39c12', 3, strftime('%s', 'now') * 1000, strftime('%s', 'now') * 1000);

-- ============================================================================
-- Migration Complete
-- ============================================================================
-- 此脚本创建了知识库集成所需的所有表、索引、视图和触发器
-- 执行后需要在应用层实现对应的 CRUD 操作和同步逻辑
