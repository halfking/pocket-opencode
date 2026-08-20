#!/bin/bash

# OpenCode 集成测试脚本
# 用于验证适配器与真实 OpenCode API 的集成

set -e

OPENCODE_URL="${OPENCODE_URL:-http://localhost:4096}"
BACKEND_URL="${BACKEND_URL:-http://localhost:8080}"

echo "========================================"
echo "OpenCode 集成测试"
echo "========================================"
echo "OpenCode URL: $OPENCODE_URL"
echo "Backend URL: $BACKEND_URL"
echo ""

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 测试函数
test_api() {
    local name=$1
    local method=$2
    local url=$3
    local expected_status=${4:-200}
    
    echo -n "测试: $name ... "
    
    response=$(curl -s -w "\n%{http_code}" -X "$method" "$url" 2>/dev/null || echo "000")
    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | sed '$d')
    
    if [ "$http_code" = "$expected_status" ]; then
        echo -e "${GREEN}✓ PASS${NC} (HTTP $http_code)"
        echo "  响应: $(echo "$body" | jq -c '.' 2>/dev/null || echo "$body" | head -c 100)"
        return 0
    else
        echo -e "${RED}✗ FAIL${NC} (HTTP $http_code, expected $expected_status)"
        echo "  响应: $(echo "$body" | head -c 200)"
        return 1
    fi
}

# 第一部分：测试 OpenCode 原生 API
echo "========================================"
echo "第一部分：测试 OpenCode 原生 API"
echo "========================================"
echo ""

echo "步骤 1: 检查 OpenCode 健康状态"
test_api "Health Check" GET "$OPENCODE_URL/api/health" 200
echo ""

echo "步骤 2: 列出 Sessions"
test_api "List Sessions" GET "$OPENCODE_URL/api/session?limit=5&order=desc" 200
echo ""

echo "步骤 3: 获取第一个 Session 详情（如果存在）"
FIRST_SESSION=$(curl -s "$OPENCODE_URL/api/session?limit=1" | jq -r '.data[0].id // empty' 2>/dev/null)
if [ -n "$FIRST_SESSION" ]; then
    echo "找到 Session: $FIRST_SESSION"
    test_api "Get Session Detail" GET "$OPENCODE_URL/api/session/$FIRST_SESSION" 200
    echo ""
    
    echo "步骤 4: 获取 Session 消息"
    test_api "Get Session Messages" GET "$OPENCODE_URL/api/session/$FIRST_SESSION/message?limit=10" 200
    echo ""
else
    echo -e "${YELLOW}⚠ 没有找到 Session，跳过详情测试${NC}"
    echo ""
fi

# 第二部分：测试 Pocket 后端适配器
echo "========================================"
echo "第二部分：测试 Pocket 后端适配器"
echo "========================================"
echo ""

echo "步骤 5: 测试后端健康状态"
test_api "Backend Health" GET "$BACKEND_URL/api/health" 200 || echo -e "${YELLOW}⚠ 后端未启动${NC}"
echo ""

echo "步骤 6: 发现 OpenCode 实例"
test_api "Discover Instances" GET "$BACKEND_URL/api/opencode/instances" 200 || echo -e "${YELLOW}⚠ 后端未启动或未配置实例${NC}"
echo ""

echo "步骤 7: 获取实例任务列表"
# 注意：需要先配置实例
echo -e "${YELLOW}⚠ 需要先在后端配置 OpenCode 实例${NC}"
echo ""

# 第三部分：数据格式验证
echo "========================================"
echo "第三部分：数据格式验证"
echo "========================================"
echo ""

echo "验证 Session 响应格式..."
SESSION_RESPONSE=$(curl -s "$OPENCODE_URL/api/session?limit=1" 2>/dev/null || echo '{}')
echo "$SESSION_RESPONSE" | jq '.' > /dev/null 2>&1 && {
    echo -e "${GREEN}✓${NC} 响应是有效的 JSON"
    
    HAS_DATA=$(echo "$SESSION_RESPONSE" | jq 'has("data")' 2>/dev/null)
    if [ "$HAS_DATA" = "true" ]; then
        echo -e "${GREEN}✓${NC} 响应包含 'data' 字段"
    else
        echo -e "${RED}✗${NC} 响应缺少 'data' 字段"
    fi
    
    HAS_CURSOR=$(echo "$SESSION_RESPONSE" | jq 'has("cursor")' 2>/dev/null)
    if [ "$HAS_CURSOR" = "true" ]; then
        echo -e "${GREEN}✓${NC} 响应包含 'cursor' 字段"
    else
        echo -e "${YELLOW}⚠${NC} 响应缺少 'cursor' 字段（可能是空列表）"
    fi
    
    # 验证 SessionInfo 字段
    FIRST_SESSION_DATA=$(echo "$SESSION_RESPONSE" | jq '.data[0]' 2>/dev/null)
    if [ "$FIRST_SESSION_DATA" != "null" ] && [ -n "$FIRST_SESSION_DATA" ]; then
        echo ""
        echo "验证 SessionInfo 字段..."
        for field in "id" "projectID" "title" "time" "tokens" "location"; do
            HAS_FIELD=$(echo "$FIRST_SESSION_DATA" | jq "has(\"$field\")" 2>/dev/null)
            if [ "$HAS_FIELD" = "true" ]; then
                echo -e "${GREEN}✓${NC} SessionInfo.$field 存在"
            else
                echo -e "${RED}✗${NC} SessionInfo.$field 缺失"
            fi
        done
    fi
} || {
    echo -e "${RED}✗${NC} 响应不是有效的 JSON"
}

echo ""
echo "========================================"
echo "测试完成"
echo "========================================"
echo ""
echo "如果看到失败的测试，请检查："
echo "1. OpenCode 是否正在运行（默认端口 4096）"
echo "2. OpenCode 是否启用了 HTTP API 服务器"
echo "3. 后端服务是否正在运行（默认端口 8080）"
echo ""
echo "启动 OpenCode 示例："
echo "  cd ~/workspace/ai/opencode"
echo "  bun run dev"
echo ""
