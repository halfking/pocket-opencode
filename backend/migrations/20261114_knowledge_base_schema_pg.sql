-- Knowledge Base Integration Schema Migration (PostgreSQL版本)
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
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    FOREIGN KEY (parent_id) REFERENCES notebooks(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_notebooks_user_workspace ON notebooks(user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_notebooks_parent ON notebooks(parent_id);
CREATE INDEX IF NOT EXISTS idx_notebooks_sort ON notebooks(user_id, sort_order);

-- ============================================================================
-- 2. Enhance Notes Table (添加 notebook_id)
-- ============================================================================
-- 检查列是否已存在，避免重复添加
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'notes' AND column_name = 'notebook_id'
    ) THEN
        ALTER TABLE notes ADD COLUMN notebook_id TEXT REFERENCES notebooks(id);
        CREATE INDEX idx_notes_notebook ON notes(notebook_id);
    END IF;
END $$;

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
    created_at BIGINT NOT NULL,
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
    last_synced_at BIGINT,
    sync_status TEXT NOT NULL,  -- 'pending', 'synced', 'conflict', 'error'
    error_message TEXT,
    version INTEGER DEFAULT 1,
    retry_count INTEGER DEFAULT 0,
    next_retry_at BIGINT
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
    filters JSONB,  -- PostgreSQL使用JSONB: {tags, notebook, dateRange, domain}
    created_at BIGINT NOT NULL,
    last_used_at BIGINT
);

CREATE INDEX IF NOT EXISTS idx_saved_searches_user ON saved_searches(user_id);
CREATE INDEX IF NOT EXISTS idx_saved_searches_last_used ON saved_searches(user_id, last_used_at DESC);

-- ============================================================================
-- 6. Search History Table (搜索历史)
-- ============================================================================
CREATE TABLE IF NOT EXISTS search_history (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    query TEXT NOT NULL,
    filters JSONB,  -- PostgreSQL使用JSONB
    results_count INTEGER,
    search_time_ms INTEGER,  -- 搜索耗时
    created_at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_search_history_user ON search_history(user_id, created_at DESC);

-- ============================================================================
-- 7. Note Versions Table (笔记版本历史)
-- ============================================================================
CREATE TABLE IF NOT EXISTS note_versions (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL,
    version_number INTEGER NOT NULL,
    title TEXT,
    content_hash TEXT NOT NULL,  -- SHA256(content)
    change_reason TEXT,  -- 修改原因
    created_by TEXT NOT NULL,  -- 'user' | 'system' | 'sync'
    created_at BIGINT NOT NULL,
    UNIQUE(note_id, version_number)
);

CREATE INDEX IF NOT EXISTS idx_note_versions_note ON note_versions(note_id, version_number DESC);

-- ============================================================================
-- 8. Tags Table (标签表)
-- ============================================================================
CREATE TABLE IF NOT EXISTS tags (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    usage_count INTEGER DEFAULT 0,  -- 使用次数
    last_used_at BIGINT,
    color TEXT,  -- 可选的标签颜色
    UNIQUE(user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_tags_user ON tags(user_id);
CREATE INDEX IF NOT EXISTS idx_tags_usage ON tags(user_id, usage_count DESC);

-- ============================================================================
-- 9. Knowledge Graph Edges Table (知识图谱边)
-- ============================================================================
CREATE TABLE IF NOT EXISTS knowledge_edges (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    edge_type TEXT NOT NULL,  -- 'note_link', 'tag_similarity', 'temporal', 'ai_related'
    weight REAL DEFAULT 1.0,  -- 权重，用于图谱布局
    metadata JSONB,  -- PostgreSQL使用JSONB
    created_at BIGINT NOT NULL,
    UNIQUE(source_id, target_id, edge_type)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_edges_source ON knowledge_edges(source_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_edges_target ON knowledge_edges(target_id);
CREATE INDEX IF NOT EXISTS idx_knowledge_edges_type ON knowledge_edges(edge_type);

-- ============================================================================
-- 10. 视图：笔记本统计
-- ============================================================================
CREATE OR REPLACE VIEW notebook_stats AS
SELECT 
    nb.id,
    nb.name,
    nb.user_id,
    nb.parent_id,
    COUNT(DISTINCT n.id) as note_count,
    MAX(n.updated_at) as last_note_updated_at
FROM notebooks nb
LEFT JOIN notes n ON n.notebook_id = nb.id AND n.is_latest = true
GROUP BY nb.id, nb.name, nb.user_id, nb.parent_id;

-- ============================================================================
-- 11. 视图：标签统计
-- ============================================================================
CREATE OR REPLACE VIEW tag_stats AS
SELECT 
    t.id,
    t.user_id,
    t.name,
    COUNT(DISTINCT n.id) as note_count,
    MAX(n.updated_at) as last_used_at
FROM tags t
LEFT JOIN notes n ON n.is_latest = true 
    AND n.tags @> to_jsonb(ARRAY[t.name])  -- PostgreSQL JSONB contains
GROUP BY t.id, t.user_id, t.name;

-- ============================================================================
-- 12. 全文搜索配置（PostgreSQL使用不同的方式）
-- ============================================================================
-- PostgreSQL使用tsvector和GIN索引进行全文搜索
-- 为notes表添加全文搜索列
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'notes' AND column_name = 'search_vector'
    ) THEN
        ALTER TABLE notes ADD COLUMN search_vector tsvector;
        
        -- 创建GIN索引
        CREATE INDEX idx_notes_search ON notes USING GIN(search_vector);
        
        -- 更新现有数据
        UPDATE notes SET search_vector = 
            setweight(to_tsvector('english', coalesce(title,'')), 'A') ||
            setweight(to_tsvector('english', coalesce(content,'')), 'B');
    END IF;
END $$;

-- 创建触发器自动更新search_vector
CREATE OR REPLACE FUNCTION notes_search_vector_update() RETURNS trigger AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', coalesce(NEW.title,'')), 'A') ||
        setweight(to_tsvector('english', coalesce(NEW.content,'')), 'B');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS notes_search_vector_trigger ON notes;
CREATE TRIGGER notes_search_vector_trigger
    BEFORE INSERT OR UPDATE OF title, content
    ON notes
    FOR EACH ROW
    EXECUTE FUNCTION notes_search_vector_update();

-- ============================================================================
-- Migration Complete
-- ============================================================================
-- PostgreSQL版本的知识库集成Schema已创建
-- 包含所有必要的表、索引、视图和触发器
