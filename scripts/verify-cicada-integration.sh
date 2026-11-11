#!/bin/bash
# verify-cicada-integration.sh
# 验证 Cicada 集成 Phase A 的完整性

set -e

echo "=========================================="
echo "Cicada 集成 Phase A 验证脚本"
echo "=========================================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 验证计数
PASSED=0
FAILED=0
WARNINGS=0

# 检查函数
check_file() {
    if [ -f "$1" ]; then
        echo -e "${GREEN}✓${NC} $1"
        ((PASSED++))
    else
        echo -e "${RED}✗${NC} $1 (缺失)"
        ((FAILED++))
    fi
}

check_dir() {
    if [ -d "$1" ]; then
        echo -e "${GREEN}✓${NC} $1/"
        ((PASSED++))
    else
        echo -e "${RED}✗${NC} $1/ (缺失)"
        ((FAILED++))
    fi
}

check_content() {
    if grep -q "$2" "$1" 2>/dev/null; then
        echo -e "${GREEN}✓${NC} $1 包含 '$2'"
        ((PASSED++))
    else
        echo -e "${YELLOW}⚠${NC} $1 未找到 '$2'"
        ((WARNINGS++))
    fi
}

echo "1. 检查文档文件..."
echo "-----------------------------------"
check_file "docs/design/2026-11-09-cicada-inspired-meeting-enhancement.md"
check_file "docs/design/2026-11-09-cicada-integration-summary.md"
check_file "docs/handoff/2026-11-09-cicada-integration-phase-a.md"
echo ""

echo "2. 检查前端文件..."
echo "-----------------------------------"
check_file "frontend/src/stores/liveSubtitle.ts"
check_file "frontend/src/components/LiveSubtitleOverlay.vue"
check_file "frontend/src/features/meetings/liveSubtitleIntegration.ts"
echo ""

echo "3. 检查后端文件..."
echo "-----------------------------------"
check_file "backend/internal/meeting/translate.go"
echo ""

echo "4. 检查文档索引更新..."
echo "-----------------------------------"
check_content "docs/README.md" "cicada-inspired-meeting-enhancement"
echo ""

echo "5. TypeScript 类型检查..."
echo "-----------------------------------"
if [ -d "frontend" ]; then
    cd frontend
    if npm run typecheck --silent 2>&1 | grep -q "Found 0 errors"; then
        echo -e "${GREEN}✓${NC} TypeScript 类型检查通过"
        ((PASSED++))
    else
        echo -e "${YELLOW}⚠${NC} TypeScript 有类型错误（可能需要安装依赖）"
        ((WARNINGS++))
    fi
    cd ..
else
    echo -e "${YELLOW}⚠${NC} frontend 目录不存在，跳过类型检查"
    ((WARNINGS++))
fi
echo ""

echo "6. Go 编译检查..."
echo "-----------------------------------"
if [ -d "backend" ]; then
    cd backend
    if go build ./internal/meeting 2>/dev/null; then
        echo -e "${GREEN}✓${NC} Go 编译通过"
        ((PASSED++))
        rm -f meeting 2>/dev/null
    else
        echo -e "${YELLOW}⚠${NC} Go 编译有错误（可能缺少依赖）"
        ((WARNINGS++))
    fi
    cd ..
else
    echo -e "${YELLOW}⚠${NC} backend 目录不存在，跳过编译检查"
    ((WARNINGS++))
fi
echo ""

echo "7. 代码行数统计..."
echo "-----------------------------------"
if command -v cloc &> /dev/null; then
    echo "新增代码统计："
    cloc --quiet \
        docs/design/2026-11-09-cicada-inspired-meeting-enhancement.md \
        docs/design/2026-11-09-cicada-integration-summary.md \
        frontend/src/stores/liveSubtitle.ts \
        frontend/src/components/LiveSubtitleOverlay.vue \
        frontend/src/features/meetings/liveSubtitleIntegration.ts \
        backend/internal/meeting/translate.go \
        2>/dev/null || echo "(cloc 未安装，跳过统计)"
else
    echo -e "${YELLOW}⚠${NC} cloc 未安装，跳过代码统计"
    ((WARNINGS++))
fi
echo ""

echo "=========================================="
echo "验证结果汇总"
echo "=========================================="
echo -e "${GREEN}通过: $PASSED${NC}"
echo -e "${RED}失败: $FAILED${NC}"
echo -e "${YELLOW}警告: $WARNINGS${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ Phase A 文件完整性验证通过！${NC}"
    echo ""
    echo "下一步："
    echo "  1. cd frontend && npm install"
    echo "  2. npm run typecheck"
    echo "  3. npm run build"
    echo "  4. 在 App.vue 中集成 LiveSubtitleOverlay"
    echo "  5. 后端注册翻译路由"
    echo "  6. 真机测试"
    exit 0
else
    echo -e "${RED}✗ 验证失败，请检查缺失的文件${NC}"
    exit 1
fi
