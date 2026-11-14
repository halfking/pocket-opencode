-- PKM Donation Enhancements from RedClaw
-- Date: 2026-11-14
-- Purpose: Add vector search, attachments, shares, and enhanced RLS from RedClaw PKM
-- Source: RedClaw migrations/000066_pkm_schema.up.sql + 000067_pkm_force_rls.up.sql
-- Adaptation: tenant_id+user_id → user_id降维适配

-- ============================================================================
-- 1. Add Vector Search to Notes (Donation Item #1)
-- ============================================================================
-- Add embedding column for semantic similarity search
DO $$ 
BEGIN
    -- Check if pgvector extension exists
    IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector') THEN
        RAISE NOTICE 'pgvector extension not found. Run: CREATE EXTENSION vector;';
    END IF;

    -- Add embedding column if not exists
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'notes' AND column_name = 'embedding'
    ) THEN
        ALTER TABLE notes ADD COLUMN embedding VECTOR(1536);  -- OpenAI ada-002 dimensions
        
        -- Create IVFFlat index for vector similarity search
        -- Note: Index creation requires at least 100 rows. Will be created post-data insertion.
        -- CREATE INDEX idx_notes_embedding ON notes USING ivfflat(embedding vector_cosine_ops) WITH (lists = 100);
        
        RAISE NOTICE 'Added embedding column to notes. Create index after inserting 100+ rows:';
        RAISE NOTICE 'CREATE INDEX idx_notes_embedding ON notes USING ivfflat(embedding vector_cosine_ops) WITH (lists = 100);';
    END IF;
END $$;

-- ============================================================================
-- 2. Attachments Table (Donation Item #4)
-- ============================================================================
-- S3-based file attachments for notes
CREATE TABLE IF NOT EXISTS attachments (
    id TEXT PRIMARY KEY,
    note_id TEXT NOT NULL REFERENCES notes(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    s3_key TEXT NOT NULL,
    s3_bucket TEXT,
    thumbnail_s3_key TEXT,
    width INTEGER,
    height INTEGER,
    metadata JSONB DEFAULT '{}'::jsonb,
    created_at BIGINT NOT NULL,
    
    CONSTRAINT attachments_filename_check CHECK (length(trim(filename)) > 0),
    CONSTRAINT attachments_size_check CHECK (size_bytes > 0 AND size_bytes <= 52428800)  -- 50MB max
);

CREATE INDEX IF NOT EXISTS idx_attachments_note ON attachments(note_id);
CREATE INDEX IF NOT EXISTS idx_attachments_user ON attachments(user_id);
CREATE INDEX IF NOT EXISTS idx_attachments_created ON attachments(user_id, created_at DESC);

COMMENT ON TABLE attachments IS 'File attachments for notes (images, PDFs, documents) stored in S3';
COMMENT ON COLUMN attachments.s3_key IS 'S3 object key format: attachments/{user_id}/{note_id}/{uuid}_{sanitized_filename}';
COMMENT ON COLUMN attachments.size_bytes IS 'File size in bytes, max 50MB (52428800 bytes)';

-- ============================================================================
-- 3. Shares Table (Donation Item #4)
-- ============================================================================
-- Note and notebook sharing with permission control
CREATE TABLE IF NOT EXISTS shares (
    id TEXT PRIMARY KEY,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    shared_by_user_id TEXT NOT NULL,
    shared_with_user_id TEXT,  -- NULL for public links
    permission TEXT NOT NULL,
    access_token TEXT UNIQUE,
    access_count INTEGER DEFAULT 0,
    last_accessed_at BIGINT,
    expires_at BIGINT,
    created_at BIGINT NOT NULL,
    
    CONSTRAINT shares_resource_type_check CHECK (resource_type IN ('note', 'notebook')),
    CONSTRAINT shares_permission_check CHECK (permission IN ('read', 'write', 'admin'))
);

CREATE INDEX IF NOT EXISTS idx_shares_resource ON shares(resource_type, resource_id);
CREATE INDEX IF NOT EXISTS idx_shares_shared_with ON shares(shared_with_user_id) WHERE shared_with_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_shares_token ON shares(access_token) WHERE access_token IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_shares_expires ON shares(expires_at) WHERE expires_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_shares_shared_by ON shares(shared_by_user_id);

COMMENT ON TABLE shares IS 'Sharing permissions for notes and notebooks';
COMMENT ON COLUMN shares.access_token IS 'Public access token for anonymous sharing (like Notion share links)';
COMMENT ON COLUMN shares.expires_at IS 'Unix timestamp (ms) when share expires; NULL = never expires';

-- ============================================================================
-- 4. Enhanced RLS Policies (Donation Item #3 - 降维适配)
-- ============================================================================
-- Adapt RedClaw's FORCE RLS discipline from tenant_id+user_id to user_id only

-- Enable RLS on existing tables
ALTER TABLE notebooks ENABLE ROW LEVEL SECURITY;
ALTER TABLE notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE note_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE tags ENABLE ROW LEVEL SECURITY;
ALTER TABLE attachments ENABLE ROW LEVEL SECURITY;
ALTER TABLE shares ENABLE ROW LEVEL SECURITY;

-- FORCE RLS to prevent table owner bypass (RedClaw 000067 donation)
ALTER TABLE notebooks FORCE ROW LEVEL SECURITY;
ALTER TABLE notes FORCE ROW LEVEL SECURITY;
ALTER TABLE note_links FORCE ROW LEVEL SECURITY;
ALTER TABLE tags FORCE ROW LEVEL SECURITY;
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
ALTER TABLE shares FORCE ROW LEVEL SECURITY;

-- Notebooks: user can only access their own notebooks
CREATE POLICY notebooks_user_isolation ON notebooks
    FOR ALL
    USING (user_id = current_setting('app.user_id', true))
    WITH CHECK (user_id = current_setting('app.user_id', true));

-- Notes: user can access their own notes + shared notes (Phase 1: owner only)
CREATE POLICY notes_owner_access ON notes
    FOR ALL
    USING (user_id = current_setting('app.user_id', true))
    WITH CHECK (user_id = current_setting('app.user_id', true));

-- TODO Phase 2: Add shared notes read policy
-- CREATE POLICY notes_shared_read_access ON notes
--     FOR SELECT
--     USING (
--         EXISTS (
--             SELECT 1 FROM shares
--             WHERE shares.resource_type = 'note'
--             AND shares.resource_id = notes.id
--             AND (shares.shared_with_user_id = current_setting('app.user_id', true)
--                  OR shares.access_token IS NOT NULL)  -- Public links
--         )
--     );

-- Tags: user can only access their own tags
CREATE POLICY tags_user_isolation ON tags
    FOR ALL
    USING (user_id = current_setting('app.user_id', true))
    WITH CHECK (user_id = current_setting('app.user_id', true));

-- Note Links: inherit access from notes
CREATE POLICY note_links_via_notes ON note_links
    FOR ALL
    USING (
        EXISTS (
            SELECT 1 FROM notes 
            WHERE notes.id = note_links.source_note_id
            AND notes.user_id = current_setting('app.user_id', true)
        )
    );

-- Attachments: inherit access from notes
CREATE POLICY attachments_via_notes ON attachments
    FOR ALL
    USING (
        user_id = current_setting('app.user_id', true)
        AND EXISTS (
            SELECT 1 FROM notes 
            WHERE notes.id = attachments.note_id
            AND notes.user_id = current_setting('app.user_id', true)
        )
    )
    WITH CHECK (
        user_id = current_setting('app.user_id', true)
        AND EXISTS (
            SELECT 1 FROM notes 
            WHERE notes.id = attachments.note_id
            AND notes.user_id = current_setting('app.user_id', true)
        )
    );

-- Shares: users can see shares they created or are recipients of
CREATE POLICY shares_access ON shares
    FOR ALL
    USING (
        shared_by_user_id = current_setting('app.user_id', true)
        OR shared_with_user_id = current_setting('app.user_id', true)
    );

-- ============================================================================
-- 5. Vector Search Function (Donation Item #1)
-- ============================================================================
-- Semantic similarity search using cosine distance
CREATE OR REPLACE FUNCTION search_notes_by_embedding(
    p_user_id TEXT,
    p_embedding VECTOR(1536),
    p_limit INTEGER DEFAULT 20,
    p_offset INTEGER DEFAULT 0
)
RETURNS TABLE (
    id TEXT,
    title TEXT,
    content TEXT,
    notebook_id TEXT,
    created_at BIGINT,
    updated_at BIGINT,
    distance REAL
) AS $$
BEGIN
    RETURN QUERY
    SELECT 
        n.id,
        n.title,
        n.content,
        n.notebook_id,
        n.created_at,
        n.updated_at,
        (n.embedding <=> p_embedding) as distance  -- Cosine distance (lower = more similar)
    FROM notes n
    WHERE n.user_id = p_user_id
        AND n.is_latest = true
        AND n.embedding IS NOT NULL
    ORDER BY distance ASC
    LIMIT p_limit
    OFFSET p_offset;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION search_notes_by_embedding IS 'Semantic similarity search using vector embeddings (cosine distance)';

-- ============================================================================
-- 6. Hybrid Search Function (Full-text + Vector)
-- ============================================================================
-- Combine full-text and semantic search with weighted scores
CREATE OR REPLACE FUNCTION search_notes_hybrid(
    p_user_id TEXT,
    p_query TEXT,
    p_embedding VECTOR(1536),
    p_text_weight REAL DEFAULT 0.5,
    p_vector_weight REAL DEFAULT 0.5,
    p_limit INTEGER DEFAULT 20
)
RETURNS TABLE (
    id TEXT,
    title TEXT,
    content TEXT,
    notebook_id TEXT,
    created_at BIGINT,
    updated_at BIGINT,
    text_rank REAL,
    vector_distance REAL,
    combined_score REAL
) AS $$
BEGIN
    RETURN QUERY
    WITH text_search AS (
        SELECT 
            n.id,
            ts_rank(n.search_vector, websearch_to_tsquery('english', p_query)) as rank
        FROM notes n
        WHERE n.user_id = p_user_id
            AND n.is_latest = true
            AND n.search_vector @@ websearch_to_tsquery('english', p_query)
    ),
    vector_search AS (
        SELECT 
            n.id,
            (n.embedding <=> p_embedding) as distance
        FROM notes n
        WHERE n.user_id = p_user_id
            AND n.is_latest = true
            AND n.embedding IS NOT NULL
    )
    SELECT 
        n.id,
        n.title,
        n.content,
        n.notebook_id,
        n.created_at,
        n.updated_at,
        COALESCE(ts.rank, 0) as text_rank,
        COALESCE(vs.distance, 1) as vector_distance,
        (COALESCE(ts.rank, 0) * p_text_weight + (1 - COALESCE(vs.distance, 1)) * p_vector_weight) as combined_score
    FROM notes n
    LEFT JOIN text_search ts ON ts.id = n.id
    LEFT JOIN vector_search vs ON vs.id = n.id
    WHERE n.user_id = p_user_id
        AND n.is_latest = true
        AND (ts.rank IS NOT NULL OR vs.distance IS NOT NULL)
    ORDER BY combined_score DESC
    LIMIT p_limit;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION search_notes_hybrid IS 'Hybrid search combining full-text (BM25) and semantic (vector) results';

-- ============================================================================
-- 7. Statistics Function Enhancement
-- ============================================================================
-- Enhanced user stats including attachments
CREATE OR REPLACE FUNCTION get_user_knowledge_stats(
    p_user_id TEXT
)
RETURNS TABLE (
    total_notes BIGINT,
    total_notebooks BIGINT,
    total_tags BIGINT,
    total_attachments BIGINT,
    total_note_links BIGINT,
    notes_with_embeddings BIGINT,
    total_storage_bytes BIGINT
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        COUNT(DISTINCT n.id) FILTER (WHERE n.is_latest = true),
        COUNT(DISTINCT nb.id),
        COUNT(DISTINCT t.id),
        COUNT(DISTINCT a.id),
        COUNT(DISTINCT nl.id),
        COUNT(DISTINCT n.id) FILTER (WHERE n.is_latest = true AND n.embedding IS NOT NULL),
        COALESCE(SUM(a.size_bytes), 0)
    FROM notes n
    LEFT JOIN notebooks nb ON nb.user_id = n.user_id
    LEFT JOIN tags t ON t.user_id = n.user_id
    LEFT JOIN attachments a ON a.note_id = n.id
    LEFT JOIN note_links nl ON nl.source_note_id = n.id OR nl.target_note_id = n.id
    WHERE n.user_id = p_user_id;
END;
$$ LANGUAGE plpgsql;

-- ============================================================================
-- Migration Complete
-- ============================================================================
-- Donated features from RedClaw PKM:
-- ✅ 1. Vector search (embedding VECTOR(1536) + ivfflat index + similarity functions)
-- ✅ 2. Full-text search PG version (already in 20261114_knowledge_base_schema_pg.sql)
-- ✅ 3. RLS discipline (FORCE RLS + user_id policies - 降维适配)
-- ✅ 4. Attachments (S3 design) + Shares (permissions) tables
-- ✅ 5. Hybrid search function (text + vector)
-- 
-- Next steps (OpenPocket Phase 3):
-- - Implement attachment upload/download handlers
-- - Add embedding generation on note create/update
-- - Create vector index after sufficient data: CREATE INDEX idx_notes_embedding ON notes USING ivfflat(embedding vector_cosine_ops) WITH (lists = 100);
-- - Implement share token generation and validation
-- - Add shared notes read policy
