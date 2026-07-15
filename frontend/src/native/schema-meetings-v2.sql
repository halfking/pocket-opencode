-- 会议模块 v2 迁移（2026-07-15）
-- 为已有数据库补充新列，幂等执行（列已存在则跳过）

-- local_meetings 扩展列
ALTER TABLE local_meetings ADD COLUMN location TEXT;
ALTER TABLE local_meetings ADD COLUMN participants TEXT;
ALTER TABLE local_meetings ADD COLUMN live_summary TEXT;
ALTER TABLE local_meetings ADD COLUMN refined_transcript TEXT;
ALTER TABLE local_meetings ADD COLUMN recommendations TEXT;
ALTER TABLE local_meetings ADD COLUMN status TEXT DEFAULT 'completed';

-- local_meeting_segments 扩展列
ALTER TABLE local_meeting_segments ADD COLUMN lang TEXT DEFAULT 'zh';
ALTER TABLE local_meeting_segments ADD COLUMN confidence REAL DEFAULT 1.0;

-- 声纹库
CREATE TABLE IF NOT EXISTS local_voiceprints (
    id TEXT PRIMARY KEY,
    display_name TEXT,
    embedding BLOB,
    sample_count INTEGER DEFAULT 1,
    created_at INTEGER NOT NULL
);

INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at)
VALUES ('2026-07-15-meetings-v2', '会议模块扩展字段 + 声纹库', strftime('%s', 'now') * 1000);
