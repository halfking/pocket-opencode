#!/bin/bash
# PostgreSQL Schema 迁移验证脚本
# 用于验证 knowledge base schema 是否正确创建

set -e

# 从环境变量或参数获取数据库连接信息
DB_DSN="${POCKET_POSTGRES_DSN:-postgresql://postgres:postgres@localhost:5432/pocket_test?sslmode=disable}"

echo "==================================="
echo "Knowledge Base Schema 验证脚本 (PostgreSQL)"
echo "==================================="
echo "数据库: $DB_DSN"
echo ""

# 检查 psql 是否可用
if ! command -v psql &> /dev/null; then
    echo "❌ 错误: psql 命令未找到，请安装 PostgreSQL 客户端"
    exit 1
fi

# 提取数据库连接信息
DB_URL="$DB_DSN"

echo "✓ psql 命令可用"
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
)

for table in "${TABLES[@]}"; do
    if psql "$DB_URL" -t -c "SELECT to_regclass('public.$table');" 2>/dev/null | grep -q "$table"; then
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
    if psql "$DB_URL" -t -c "SELECT to_regclass('public.$view');" 2>/dev/null | grep -q "$view"; then
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
    "idx_notes_search"
)

for idx in "${INDEXES[@]}"; do
    if psql "$DB_URL" -t -c "SELECT indexname FROM pg_indexes WHERE indexname='$idx';" 2>/dev/null | grep -q "$idx"; then
        echo "✓ $idx"
    else
        echo "⚠ $idx (缺失)"
    fi
done

echo ""
echo "检查触发器..."
echo "-----------------------------------"

if psql "$DB_URL" -t -c "SELECT tgname FROM pg_trigger WHERE tgname='notes_search_vector_trigger';" 2>/dev/null | grep -q "notes_search_vector_trigger"; then
    echo "✓ notes_search_vector_trigger"
else
    echo "❌ notes_search_vector_trigger (缺失)"
fi

echo ""
echo "检查 notes 表的 notebook_id 列..."
echo "-----------------------------------"

if psql "$DB_URL" -t -c "SELECT column_name FROM information_schema.columns WHERE table_name='notes' AND column_name='notebook_id';" 2>/dev/null | grep -q "notebook_id"; then
    echo "✓ notes.notebook_id 列已添加"
else
    echo "❌ notes.notebook_id 列缺失"
fi

echo ""
echo "检查 notes 表的 search_vector 列..."
echo "-----------------------------------"

if psql "$DB_URL" -t -c "SELECT column_name FROM information_schema.columns WHERE table_name='notes' AND column_name='search_vector';" 2>/dev/null | grep -q "search_vector"; then
    echo "✓ notes.search_vector 列已添加"
else
    echo "❌ notes.search_vector 列缺失"
fi

echo ""
echo "==================================="
echo "验证完成"
echo "==================================="

# 统计信息
echo ""
echo "数据统计:"
NOTE_COUNT=$(psql "$DB_URL" -t -c "SELECT COUNT(*) FROM notes WHERE is_latest=true;" 2>/dev/null || echo "0")
NOTEBOOK_COUNT=$(psql "$DB_URL" -t -c "SELECT COUNT(*) FROM notebooks;" 2>/dev/null || echo "0")
LINK_COUNT=$(psql "$DB_URL" -t -c "SELECT COUNT(*) FROM note_links;" 2>/dev/null || echo "0")

echo "  笔记数: $(echo $NOTE_COUNT | tr -d ' ')"
echo "  笔记本数: $(echo $NOTEBOOK_COUNT | tr -d ' ')"
echo "  链接数: $(echo $LINK_COUNT | tr -d ' ')"

echo ""
echo "如需应用迁移，请运行:"
echo "  psql \$POCKET_POSTGRES_DSN < backend/migrations/20261114_knowledge_base_schema_pg.sql"
