#!/bin/bash
# 数据库迁移验证脚本
# 用于验证 knowledge base schema 是否正确创建

set -e

DB_PATH="${1:-./data/pocket.db}"

echo "==================================="
echo "Knowledge Base Schema 验证脚本"
echo "==================================="
echo "数据库路径: $DB_PATH"
echo ""

if [ ! -f "$DB_PATH" ]; then
    echo "❌ 错误: 数据库文件不存在: $DB_PATH"
    exit 1
fi

echo "✓ 数据库文件存在"
echo ""

# 检查表是否存在
echo "检查表结构..."
echo "-----------------------------------"

TABLES=(
    "notebooks"
    "note_links"
    "sync_state"
    "saved_searches"
    "search_history"
    "note_versions"
    "tags"
    "knowledge_edges"
    "notes_fts"
)

for table in "${TABLES[@]}"; do
    if sqlite3 "$DB_PATH" "SELECT name FROM sqlite_master WHERE type='table' AND name='$table';" | grep -q "$table"; then
        echo "✓ $table"
    else
        echo "❌ $table (缺失)"
    fi
done

echo ""
echo "检查视图..."
echo "-----------------------------------"

VIEWS=(
    "notebook_stats"
    "tag_stats"
)

for view in "${VIEWS[@]}"; do
    if sqlite3 "$DB_PATH" "SELECT name FROM sqlite_master WHERE type='view' AND name='$view';" | grep -q "$view"; then
        echo "✓ $view"
    else
        echo "❌ $view (缺失)"
    fi
done

echo ""
echo "检查索引..."
echo "-----------------------------------"

# 关键索引
INDEXES=(
    "idx_notebooks_user_workspace"
    "idx_notebooks_parent"
    "idx_notes_notebook"
    "idx_note_links_source"
    "idx_note_links_target"
    "idx_sync_state_status"
)

INDEX_COUNT=0
for idx in "${INDEXES[@]}"; do
    if sqlite3 "$DB_PATH" "SELECT name FROM sqlite_master WHERE type='index' AND name='$idx';" | grep -q "$idx"; then
        echo "✓ $idx"
        ((INDEX_COUNT++))
    else
        echo "⚠ $idx (缺失 - 可能是自动创建)"
    fi
done

echo ""
echo "检查触发器..."
echo "-----------------------------------"

TRIGGERS=(
    "notes_fts_insert"
    "notes_fts_update"
    "notes_fts_delete"
    "notebook_delete_cascade"
)

for trigger in "${TRIGGERS[@]}"; do
    if sqlite3 "$DB_PATH" "SELECT name FROM sqlite_master WHERE type='trigger' AND name='$trigger';" | grep -q "$trigger"; then
        echo "✓ $trigger"
    else
        echo "❌ $trigger (缺失)"
    fi
done

echo ""
echo "检查 notes 表的 notebook_id 列..."
echo "-----------------------------------"

if sqlite3 "$DB_PATH" "PRAGMA table_info(notes);" | grep -q "notebook_id"; then
    echo "✓ notes.notebook_id 列已添加"
else
    echo "❌ notes.notebook_id 列缺失"
fi

echo ""
echo "==================================="
echo "验证完成"
echo "==================================="

# 统计信息
echo ""
echo "数据统计:"
NOTE_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM notes WHERE is_latest=1;" 2>/dev/null || echo "0")
NOTEBOOK_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM notebooks;" 2>/dev/null || echo "0")
LINK_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM note_links;" 2>/dev/null || echo "0")

echo "  笔记数: $NOTE_COUNT"
echo "  笔记本数: $NOTEBOOK_COUNT"
echo "  链接数: $LINK_COUNT"

echo ""
echo "如需应用迁移，请运行:"
echo "  sqlite3 $DB_PATH < backend/migrations/20261114_knowledge_base_schema.sql"
