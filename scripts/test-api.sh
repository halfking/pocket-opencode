#!/bin/bash
# OpenCode Pocket 测试脚本
# 用途: 执行完整的API和功能测试

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

echo "=========================================="
echo "OpenCode Pocket API 测试"
echo "=========================================="
echo ""

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

BASE_URL="http://localhost:8088"
PASS_COUNT=0
FAIL_COUNT=0

# 测试函数
test_api() {
    local test_name="$1"
    local method="$2"
    local endpoint="$3"
    local data="$4"
    local expected="$5"
    
    echo -n "测试: $test_name ... "
    
    if [ "$method" = "POST" ]; then
        response=$(curl -s -X POST "$BASE_URL$endpoint" \
            -H "Content-Type: application/json" \
            -d "$data")
    else
        response=$(curl -s "$BASE_URL$endpoint")
    fi
    
    if echo "$response" | grep -q "$expected"; then
        echo -e "${GREEN}✓ 通过${NC}"
        PASS_COUNT=$((PASS_COUNT + 1))
        return 0
    else
        echo -e "${RED}✗ 失败${NC}"
        echo "  响应: $response"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        return 1
    fi
}

# 开始测试
echo "执行 API 测试..."
echo ""

# 1. 健康检查
test_api "健康检查" "GET" "/healthz" "" "ok"

# 2. 登录测试
TOKEN_RESPONSE=$(curl -s -X POST "$BASE_URL/api/auth/login" \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}')
TOKEN=$(echo "$TOKEN_RESPONSE" | grep -o '"token":"[^"]*"' | cut -d'"' -f4)

if [ -n "$TOKEN" ]; then
    echo -e "测试: 登录功能 ... ${GREEN}✓ 通过${NC}"
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "  Token: ${TOKEN:0:20}..."
else
    echo -e "测试: 登录功能 ... ${RED}✗ 失败${NC}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# 3. 实例列表
test_api "实例列表" "GET" "/api/instances" "" "instances"

# 4. 任务列表（需要token）
echo -n "测试: 任务列表 ... "
TASKS_RESPONSE=$(curl -s "$BASE_URL/api/tasks" \
    -H "Authorization: Bearer $TOKEN")

if echo "$TASKS_RESPONSE" | grep -q "tasks"; then
    echo -e "${GREEN}✓ 通过${NC}"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}✗ 失败${NC}"
    echo "  响应: $TASKS_RESPONSE"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# 5. 版本检查
test_api "版本检查" "GET" "/api/app/check-update" "" "version"

echo ""
echo "=========================================="
echo "测试结果汇总"
echo "=========================================="
echo ""
echo "通过: $PASS_COUNT"
echo "失败: $FAIL_COUNT"
echo "总计: $((PASS_COUNT + FAIL_COUNT))"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✓ 所有测试通过！${NC}"
    exit 0
else
    echo -e "${RED}✗ 部分测试失败${NC}"
    exit 1
fi
