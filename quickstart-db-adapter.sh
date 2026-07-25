#!/bin/bash

# OpenCode 数据库适配器快速开始指南

set -e

echo "========================================"
echo "OpenCode 数据库适配器 - 快速开始"
echo "========================================"
echo ""

# 检查 Go 环境
if ! command -v go &> /dev/null; then
    echo "❌ 未找到 Go，请先安装 Go 1.21+"
    exit 1
fi

echo "✓ Go 版本: $(go version)"
echo ""

# 检查数据库文件
DB_PATH="$HOME/.local/share/opencode/opencode.db"
if [ ! -f "$DB_PATH" ]; then
    echo "❌ 未找到 OpenCode 数据库"
    echo "   路径: $DB_PATH"
    echo ""
    echo "请确保 OpenCode 已安装并至少运行过一次"
    exit 1
fi

echo "✓ 找到 OpenCode 数据库"
echo "  路径: $DB_PATH"
echo ""

# 检查数据库可读性
if ! sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM session;" > /dev/null 2>&1; then
    echo "⚠️  无法读取数据库，可能需要权限"
fi

# 统计 session 数量
SESSION_COUNT=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM session;" 2>/dev/null || echo "未知")
echo "✓ Sessions 数量: $SESSION_COUNT"
echo ""

echo "----------------------------------------"
echo "步骤 1: 安装依赖"
echo "----------------------------------------"

cd backend

echo "添加 SQLite 驱动..."
if grep -q "github.com/mattn/go-sqlite3" go.mod; then
    echo "✓ SQLite 驱动已存在"
else
    echo "安装 github.com/mattn/go-sqlite3..."
    go get github.com/mattn/go-sqlite3 || {
        echo "⚠️  网络安装失败，尝试使用本地缓存..."
        echo "如果继续失败，请手动添加到 go.mod:"
        echo "  require github.com/mattn/go-sqlite3 v1.14.18"
    }
fi

echo ""
echo "----------------------------------------"
echo "步骤 2: 编译演示程序"
echo "----------------------------------------"

echo "编译 opencode-db-demo..."
if go build -o ../opencode-db-demo ./cmd/opencode-db-demo; then
    echo "✓ 编译成功"
else
    echo "❌ 编译失败"
    echo ""
    echo "可能的原因："
    echo "1. 缺少 C 编译器 (CGO 需要)"
    echo "   Mac: xcode-select --install"
    echo "   Linux: apt-get install build-essential"
    echo ""
    echo "2. 网络问题无法下载依赖"
    echo "   尝试: export GOPROXY=https://goproxy.cn,direct"
    exit 1
fi

cd ..

echo ""
echo "----------------------------------------"
echo "步骤 3: 运行演示程序"
echo "----------------------------------------"
echo ""

./opencode-db-demo

echo ""
echo "========================================"
echo "快速开始完成！"
echo "========================================"
echo ""
echo "下一步："
echo "1. 查看演示程序代码: backend/cmd/opencode-db-demo/main.go"
echo "2. 查看适配器代码: backend/internal/adapter/opencode_db.go"
echo "3. 运行测试: cd backend && go test ./internal/adapter/opencode_db_test.go -v"
echo "4. 阅读文档: backend/internal/adapter/README_DB_ADAPTER.md"
echo ""
