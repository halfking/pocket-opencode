-- ============================================================
-- 数据库性能优化与审计修复 SQL
-- 版本: v1.0.0
-- 日期: 2026-07-02
-- 用途: 补充缺失索引、添加触发器、优化查询性能
-- ============================================================

-- ============================================================
-- 一、补充缺失的索引
-- ============================================================

-- 邮件表：按分类和日期查询的复合索引
CREATE INDEX IF NOT EXISTS idx_emails_category_date 
ON local_emails(category, date DESC) 
WHERE deleted_at IS NULL;

-- 邮件表：按重要性查询
CREATE INDEX IF NOT EXISTS idx_emails_importance 
ON local_emails(importance, date DESC) 
WHERE is_read = 0;

-- 笔记表：domain + updated_at 复合索引（列表查询优化）
CREATE INDEX IF NOT EXISTS idx_notes_domain_updated 
ON local_notes(domain, updated_at DESC) 
WHERE deleted_at IS NULL;

-- 笔记表：workspace 内按更新时间排序
CREATE INDEX IF NOT EXISTS idx_notes_workspace_updated 
ON local_notes(workspace_id, updated_at DESC) 
WHERE deleted_at IS NULL;

-- 待办表：按状态和截止时间查询
CREATE INDEX IF NOT EXISTS idx_todos_status_due 
ON local_todos(status, due_at) 
WHERE status != 'completed' AND due_at IS NOT NULL;

-- 智能关联表：target 反向查询索引
CREATE INDEX IF NOT EXISTS idx_links_target_type 
ON local_smart_links(target_note_id, link_type);

-- 会议分段表：按会议和时间查询
CREATE INDEX IF NOT EXISTS idx_segments_meeting_time 
ON local_meeting_segments(meeting_id, start_ms);

-- ============================================================
-- 二、数据一致性触发器
-- ============================================================

-- 笔记软删除时自动清理关联数据
DROP TRIGGER IF EXISTS local_notes_soft_delete;
CREATE TRIGGER local_notes_soft_delete 
AFTER UPDATE OF deleted_at ON local_notes
WHEN new.deleted_at IS NOT NULL
BEGIN
    -- 删除向量
    DELETE FROM local_note_vectors WHERE note_id = new.id;
    
    -- 删除智能关联
    DELETE FROM local_smart_links 
    WHERE source_note_id = new.id OR target_note_id = new.id;
    
    -- 待办设为 cancelled（不删除，保留记录）
    UPDATE local_todos 
    SET status = 'cancelled', updated_at = strftime('%s', 'now') * 1000
    WHERE note_id = new.id AND status != 'completed';
END;

-- 笔记更新时自动更新 updated_at
DROP TRIGGER IF EXISTS local_notes_update_timestamp;
CREATE TRIGGER local_notes_update_timestamp
AFTER UPDATE ON local_notes
WHEN new.deleted_at IS NULL
BEGIN
    UPDATE local_notes 
    SET updated_at = strftime('%s', 'now') * 1000
    WHERE id = new.id;
END;

-- 邮件统计表（优化未读数查询）
CREATE TABLE IF NOT EXISTS local_email_stats (
    account_id TEXT PRIMARY KEY,
    unread_count INTEGER DEFAULT 0,
    total_count INTEGER DEFAULT 0,
    last_updated INTEGER,
    FOREIGN KEY (account_id) REFERENCES local_email_accounts(id) ON DELETE CASCADE
);

-- 邮件插入时更新统计
DROP TRIGGER IF EXISTS local_emails_insert_stats;
CREATE TRIGGER local_emails_insert_stats
AFTER INSERT ON local_emails
BEGIN
    INSERT INTO local_email_stats (account_id, unread_count, total_count, last_updated)
    VALUES (new.account_id, CASE WHEN new.is_read = 0 THEN 1 ELSE 0 END, 1, strftime('%s', 'now') * 1000)
    ON CONFLICT(account_id) DO UPDATE SET
        unread_count = unread_count + CASE WHEN new.is_read = 0 THEN 1 ELSE 0 END,
        total_count = total_count + 1,
        last_updated = strftime('%s', 'now') * 1000;
END;

-- 邮件标记为已读时更新统计
DROP TRIGGER IF EXISTS local_emails_update_stats;
CREATE TRIGGER local_emails_update_stats
AFTER UPDATE OF is_read ON local_emails
WHEN old.is_read != new.is_read
BEGIN
    UPDATE local_email_stats
    SET unread_count = unread_count + CASE WHEN new.is_read = 0 THEN 1 ELSE -1 END,
        last_updated = strftime('%s', 'now') * 1000
    WHERE account_id = new.account_id;
END;

-- ============================================================
-- 三、数据完整性约束增强
-- ============================================================

-- 清理孤儿向量（没有对应笔记的向量）
DELETE FROM local_note_vectors 
WHERE note_id NOT IN (SELECT id FROM local_notes);

-- 清理孤儿智能关联
DELETE FROM local_smart_links 
WHERE source_note_id NOT IN (SELECT id FROM local_notes WHERE deleted_at IS NULL)
   OR target_note_id NOT IN (SELECT id FROM local_notes WHERE deleted_at IS NULL);

-- 清理孤儿待办
UPDATE local_todos
SET note_id = NULL
WHERE note_id IS NOT NULL 
  AND note_id NOT IN (SELECT id FROM local_notes WHERE deleted_at IS NULL);

-- ============================================================
-- 四、查询优化视图
-- ============================================================

-- 笔记列表视图（包含向量状态）
DROP VIEW IF EXISTS v_notes_list;
CREATE VIEW v_notes_list AS
SELECT 
    n.id,
    n.title,
    n.content,
    n.domain,
    n.category,
    n.tags,
    n.created_at,
    n.updated_at,
    n.workspace_id,
    w.name AS workspace_name,
    CASE WHEN v.note_id IS NOT NULL THEN 1 ELSE 0 END AS has_vector,
    (SELECT COUNT(*) FROM local_smart_links WHERE source_note_id = n.id) AS link_count
FROM local_notes n
LEFT JOIN local_workspaces w ON n.workspace_id = w.id
LEFT JOIN local_note_vectors v ON n.id = v.note_id
WHERE n.deleted_at IS NULL;

-- 邮件列表视图（包含账户信息）
DROP VIEW IF EXISTS v_emails_list;
CREATE VIEW v_emails_list AS
SELECT 
    e.id,
    e.subject,
    e.snippet,
    e.from_address,
    e.from_name,
    e.date,
    e.is_read,
    e.is_starred,
    e.category,
    e.importance,
    e.has_attachments,
    a.display_name AS account_name,
    a.email_address AS account_email
FROM local_emails e
INNER JOIN local_email_accounts a ON e.account_id = a.id
ORDER BY e.date DESC;

-- 待办列表视图（包含关联笔记）
DROP VIEW IF EXISTS v_todos_list;
CREATE VIEW v_todos_list AS
SELECT 
    t.id,
    t.title,
    t.description,
    t.status,
    t.priority,
    t.due_at,
    t.completed_at,
    t.created_at,
    t.updated_at,
    n.title AS note_title,
    n.id AS note_id
FROM local_todos t
LEFT JOIN local_notes n ON t.note_id = n.id AND n.deleted_at IS NULL
ORDER BY 
    CASE t.priority
        WHEN 'urgent' THEN 1
        WHEN 'high' THEN 2
        WHEN 'medium' THEN 3
        WHEN 'low' THEN 4
    END,
    t.due_at ASC;

-- ============================================================
-- 五、性能监控表
-- ============================================================

-- 查询性能日志（开发环境使用）
CREATE TABLE IF NOT EXISTS _perf_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    query_type TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    rows_affected INTEGER,
    timestamp INTEGER NOT NULL
);

-- 创建索引
CREATE INDEX IF NOT EXISTS idx_perf_log_type 
ON _perf_log(query_type, timestamp DESC);

-- ============================================================
-- 六、数据清理存储过程（定期执行）
-- ============================================================

-- 注：SQLite 不支持存储过程，以下是手动执行的清理 SQL

-- 清理 30 天前的软删除记录
-- DELETE FROM local_notes WHERE deleted_at IS NOT NULL AND deleted_at < (strftime('%s', 'now') - 30*24*60*60) * 1000;

-- 清理已完成超过 90 天的待办
-- DELETE FROM local_todos WHERE status = 'completed' AND completed_at < (strftime('%s', 'now') - 90*24*60*60) * 1000;

-- 清理超过 7 天的已读邮件（可选）
-- DELETE FROM local_emails WHERE is_read = 1 AND date < (strftime('%s', 'now') - 7*24*60*60) * 1000;

-- 清理性能日志（保留最近 7 天）
-- DELETE FROM _perf_log WHERE timestamp < (strftime('%s', 'now') - 7*24*60*60) * 1000;

-- ============================================================
-- 七、数据库维护命令
-- ============================================================

-- VACUUM 回收空间（定期执行，建议每周一次）
-- VACUUM;

-- 分析表统计信息（优化查询计划）
ANALYZE;

-- 完整性检查
PRAGMA integrity_check;

-- 外键检查
PRAGMA foreign_key_check;

-- ============================================================
-- 八、升级记录
-- ============================================================

CREATE TABLE IF NOT EXISTS _schema_migrations (
    version TEXT PRIMARY KEY,
    description TEXT,
    applied_at INTEGER NOT NULL
);

-- 记录本次升级
INSERT OR IGNORE INTO _schema_migrations (version, description, applied_at)
VALUES ('2026-07-02-performance-optimization', '性能优化与审计修复', strftime('%s', 'now') * 1000);

-- ============================================================
-- 执行完成
-- ============================================================
SELECT '数据库优化完成！' AS status;
SELECT 
    (SELECT COUNT(*) FROM sqlite_master WHERE type='index') AS total_indexes,
    (SELECT COUNT(*) FROM sqlite_master WHERE type='trigger') AS total_triggers,
    (SELECT COUNT(*) FROM sqlite_master WHERE type='view') AS total_views;
