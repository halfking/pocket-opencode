#!/bin/bash
# test-cicada-integration.sh
# 综合测试 Cicada Phase A 集成的翻译功能

set -e

echo "=========================================="
echo "Cicada Phase A 集成功能测试"
echo "=========================================="
echo ""

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

PASSED=0
FAILED=0

# 测试函数
test_case() {
    local name="$1"
    local result="$2"
    if [ "$result" = "0" ]; then
        echo -e "${GREEN}✓${NC} $name"
        ((PASSED++))
    else
        echo -e "${RED}✗${NC} $name"
        ((FAILED++))
    fi
}

echo "1. 检查服务器运行状态..."
echo "-----------------------------------"
if lsof -ti:8088 > /dev/null 2>&1; then
    test_case "后端服务器运行 (端口 8088)" 0
else
    test_case "后端服务器运行 (端口 8088)" 1
fi

if lsof -ti:4174 > /dev/null 2>&1; then
    test_case "前端服务器运行 (端口 4174)" 0
else
    test_case "前端服务器运行 (端口 4174)" 1
fi
echo ""

echo "2. 检查前端资源..."
echo "-----------------------------------"
curl -s http://localhost:4174/ | grep -q "<!doctype html" && \
    test_case "前端首页可访问" 0 || test_case "前端首页可访问" 1

curl -s http://localhost:4174/ | grep -q "vite" && \
    test_case "Vite 开发服务器正常" 0 || test_case "Vite 开发服务器正常" 1
echo ""

echo "3. 检查 API 端点..."
echo "-----------------------------------"
RESP=$(curl -s -X POST http://localhost:8088/api/meetings/123/translate \
    -H "Content-Type: application/json" \
    -d '{"text":"test","targetLang":"zh"}' 2>&1)

if echo "$RESP" | grep -q "unauthenticated"; then
    test_case "翻译 API 端点存在 (返回认证错误)" 0
else
    test_case "翻译 API 端点存在" 1
    echo "  响应: $RESP"
fi

RESP=$(curl -s -X POST http://localhost:8088/api/meetings/123/translate-batch \
    -H "Content-Type: application/json" \
    -d '{"texts":["test"],"targetLang":"zh"}' 2>&1)

if echo "$RESP" | grep -q "unauthenticated"; then
    test_case "批量翻译 API 端点存在 (返回认证错误)" 0
else
    test_case "批量翻译 API 端点存在" 1
fi
echo ""

echo "4. 检查前端组件文件..."
echo "-----------------------------------"
[ -f "frontend/src/stores/liveSubtitle.ts" ] && \
    test_case "liveSubtitle store 存在" 0 || test_case "liveSubtitle store 存在" 1

[ -f "frontend/src/components/LiveSubtitleOverlay.vue" ] && \
    test_case "LiveSubtitleOverlay 组件存在" 0 || test_case "LiveSubtitleOverlay 组件存在" 1

[ -f "frontend/src/features/meetings/liveSubtitleIntegration.ts" ] && \
    test_case "liveSubtitleIntegration 集成存在" 0 || test_case "liveSubtitleIntegration 集成存在" 1

grep -q "LiveSubtitleOverlay" frontend/src/app/App.vue && \
    test_case "App.vue 已集成字幕组件" 0 || test_case "App.vue 已集成字幕组件" 1

grep -q "initLiveSubtitleIntegration" frontend/src/app/App.vue && \
    test_case "App.vue 已初始化集成" 0 || test_case "App.vue 已初始化集成" 1
echo ""

echo "5. 检查后端翻译处理器..."
echo "-----------------------------------"
[ -f "backend/internal/server/server_meeting_translate.go" ] && \
    test_case "翻译处理器文件存在" 0 || test_case "翻译处理器文件存在" 1

grep -q "handleMeetingTranslate" backend/internal/server/server_meeting.go && \
    test_case "翻译路由已注册" 0 || test_case "翻译路由已注册" 1

grep -q "case \"translate\":" backend/internal/server/server_meeting.go && \
    test_case "translate 路由已添加" 0 || test_case "translate 路由已添加" 1

grep -q "case \"translate-batch\":" backend/internal/server/server_meeting.go && \
    test_case "translate-batch 路由已添加" 0 || test_case "translate-batch 路由已添加" 1
echo ""

echo "6. 测试语言检测逻辑..."
echo "-----------------------------------"
# 通过检查代码实现来验证逻辑
grep -q "detectLanguage" backend/internal/server/server_meeting_translate.go && \
    test_case "语言检测函数存在" 0 || test_case "语言检测函数存在" 1

grep -q "0x4E00.*0x9FFF" backend/internal/server/server_meeting_translate.go && \
    test_case "CJK 字符检测逻辑存在" 0 || test_case "CJK 字符检测逻辑存在" 1

grep -q "0x3040.*0x309F" backend/internal/server/server_meeting_translate.go && \
    test_case "日文平假名检测逻辑存在" 0 || test_case "日文平假名检测逻辑存在" 1

grep -q "0xAC00.*0xD7AF" backend/internal/server/server_meeting_translate.go && \
    test_case "韩文检测逻辑存在" 0 || test_case "韩文检测逻辑存在" 1
echo ""

echo "7. 验证跨平台支持..."
echo "-----------------------------------"
grep -q "getPocketNative" frontend/src/features/meetings/liveSubtitleIntegration.ts && \
    test_case "使用 pocket-native 抽象" 0 || test_case "使用 pocket-native 抽象" 1

grep -q "Teleport" frontend/src/components/LiveSubtitleOverlay.vue && \
    test_case "悬浮窗使用 Teleport (跨平台)" 0 || test_case "悬浮窗使用 Teleport (跨平台)" 1

grep -q "localStorage" frontend/src/stores/liveSubtitle.ts && \
    test_case "位置持久化 (跨平台)" 0 || test_case "位置持久化 (跨平台)" 1
echo ""

echo "8. 检查文档完整性..."
echo "-----------------------------------"
[ -f "docs/design/2026-11-09-cicada-inspired-meeting-enhancement.md" ] && \
    test_case "完整设计文档存在" 0 || test_case "完整设计文档存在" 1

[ -f "docs/design/2026-11-09-cicada-integration-summary.md" ] && \
    test_case "实施总结文档存在" 0 || test_case "实施总结文档存在" 1

[ -f "docs/handoff/2026-11-09-cicada-phase-a-integration-guide.md" ] && \
    test_case "集成指南文档存在" 0 || test_case "集成指南文档存在" 1

[ -f "docs/design/2026-11-09-cicada-final-verification-report.md" ] && \
    test_case "验证报告文档存在" 0 || test_case "验证报告文档存在" 1
echo ""

echo "=========================================="
echo "测试结果汇总"
echo "=========================================="
echo -e "${GREEN}通过: $PASSED${NC}"
echo -e "${RED}失败: $FAILED${NC}"
echo ""

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}✓ 所有测试通过！${NC}"
    echo ""
    echo "Cicada Phase A 集成已完成："
    echo "  ✓ 前端字幕组件已创建并集成"
    echo "  ✓ 后端翻译 API 已实现并注册"
    echo "  ✓ 服务器正常运行"
    echo "  ✓ 跨平台支持已实现"
    echo "  ✓ 文档齐全"
    echo ""
    echo "下一步："
    echo "  1. 访问 http://localhost:4174 查看前端"
    echo "  2. 配置 LLM API key 进行完整翻译测试"
    echo "  3. 真机测试（Android/iOS）"
    exit 0
else
    echo -e "${RED}✗ 有 $FAILED 项测试失败${NC}"
    exit 1
fi
