#!/bin/bash
# scripts/test-all.sh
# OpenCode Pocket 全量测试脚本

set -e

echo "================================================"
echo "  OpenCode Pocket - Full Test Suite"
echo "================================================"
echo ""

cd "$(dirname "$0")/../backend"

# 变量
PASS=0
FAIL=0
FAILED_PACKAGES=""

run_tests() {
    local pkg=$1
    local name=$2
    echo "📦 Testing $name..."

    if go test "./internal/$pkg/" -count=1 -v 2>&1 | tail -5; then
        echo "  ✅ $name passed"
        PASS=$((PASS + 1))
    else
        echo "  ❌ $name failed"
        FAIL=$((FAIL + 1))
        FAILED_PACKAGES="$FAILED_PACKAGES $name"
    fi
    echo ""
}

# 运行所有模块测试
run_tests "snippet"       "代码片段"
run_tests "meeting"       "会议总结"
run_tests "chat_summary"  "聊天总结"
run_tests "redclaw"       "RedClaw 集成"
run_tests "presentation"  "产品方案/PPT"
run_tests "notes"         "笔记分类"
run_tests "finance"       "记账"
run_tests "server"        "Server API"

# 编译验证
echo "🔨 Building..."
if go build ./cmd/pocketd; then
    echo "  ✅ Build successful"
    PASS=$((PASS + 1))
else
    echo "  ❌ Build failed"
    FAIL=$((FAIL + 1))
    FAILED_PACKAGES="$FAILED_PACKAGES build"
fi

echo ""
echo "================================================"
echo "  Results: $PASS passed, $FAIL failed"
if [ $FAIL -gt 0 ]; then
    echo "  Failed: $FAILED_PACKAGES"
    exit 1
fi
echo "================================================"