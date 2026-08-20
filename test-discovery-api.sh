#!/bin/bash
# OpenCode 实例发现和任务感知 API 测试脚本

set -e

BASE_URL="${BASE_URL:-http://localhost:9010}"
INSTANCE_ID="${INSTANCE_ID:-opencode-71}"

echo "🧪 OpenCode API 测试"
echo "===================="
echo "API 地址: $BASE_URL"
echo ""

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 测试函数
test_api() {
    local name=$1
    local endpoint=$2
    local method=${3:-GET}
    
    echo -n "测试: $name ... "
    
    if [ "$method" = "GET" ]; then
        response=$(curl -s -w "\n%{http_code}" "$BASE_URL$endpoint")
    else
        response=$(curl -s -w "\n%{http_code}" -X "$method" "$BASE_URL$endpoint")
    fi
    
    http_code=$(echo "$response" | tail -n1)
    body=$(echo "$response" | head -n-1)
    
    if [ "$http_code" = "200" ]; then
        echo -e "${GREEN}✓ 成功${NC}"
        return 0
    else
        echo -e "${RED}✗ 失败 (HTTP $http_code)${NC}"
        return 1
    fi
}

# 1. 测试实例发现 API
echo "1️⃣  实例发现 API"
echo "-------------------"
if test_api "发现所有实例" "/api/opencode/discover"; then
    echo "   响应示例:"
    curl -s "$BASE_URL/api/opencode/discover" | jq -C '.' | head -20
    echo ""
fi
echo ""

# 2. 测试任务感知 API - 获取指定实例的任务
echo "2️⃣  任务感知 API"
echo "-------------------"
if test_api "获取实例任务列表" "/api/opencode/instances/$INSTANCE_ID/tasks"; then
    echo "   响应示例:"
    curl -s "$BASE_URL/api/opencode/instances/$INSTANCE_ID/tasks" | jq -C '.tasks[0]' 2>/dev/null || echo "   (无任务)"
    echo ""
fi

# 2.1 测试状态过滤
if test_api "获取进行中的任务" "/api/opencode/instances/$INSTANCE_ID/tasks?status=busy"; then
    count=$(curl -s "$BASE_URL/api/opencode/instances/$INSTANCE_ID/tasks?status=busy" | jq '.filtered')
    echo "   进行中的任务数: $count"
    echo ""
fi

# 2.2 测试所有实例的任务聚合
if test_api "获取所有任务（聚合）" "/api/opencode/tasks"; then
    echo "   统计信息:"
    curl -s "$BASE_URL/api/opencode/tasks" | jq -C '.instanceStats, .statusStats'
    echo ""
fi
echo ""

# 3. 测试任务详情 API
echo "3️⃣  任务详情 API"
echo "-------------------"
# 先获取一个任务 ID
TASK_ID=$(curl -s "$BASE_URL/api/opencode/instances/$INSTANCE_ID/tasks" | jq -r '.tasks[0].id' 2>/dev/null)

if [ "$TASK_ID" != "null" ] && [ -n "$TASK_ID" ]; then
    if test_api "获取任务详情" "/api/opencode/tasks/$TASK_ID?instance_id=$INSTANCE_ID"; then
        echo "   任务信息:"
        curl -s "$BASE_URL/api/opencode/tasks/$TASK_ID?instance_id=$INSTANCE_ID" | jq -C '{id, title, status, messageCount, duration, summary}' | head -15
        echo ""
    fi
else
    echo -e "${YELLOW}⚠ 跳过（没有找到任务）${NC}"
fi
echo ""

# 4. 测试性能
echo "4️⃣  性能测试"
echo "-------------------"
echo -n "测试: 发现 API 响应时间 ... "
start_time=$(date +%s%3N)
curl -s "$BASE_URL/api/opencode/discover" > /dev/null
end_time=$(date +%s%3N)
duration=$((end_time - start_time))
echo -e "${GREEN}${duration}ms${NC}"

echo -n "测试: 任务列表 API 响应时间 ... "
start_time=$(date +%s%3N)
curl -s "$BASE_URL/api/opencode/instances/$INSTANCE_ID/tasks" > /dev/null
end_time=$(date +%s%3N)
duration=$((end_time - start_time))
echo -e "${GREEN}${duration}ms${NC}"
echo ""

# 5. 测试健康检查
echo "5️⃣  健康检查"
echo "-------------------"
test_api "后端健康检查" "/healthz"
echo ""

# 总结
echo "=========================================="
echo "✅ API 测试完成！"
echo ""
echo "📝 可用的 API 端点："
echo "  - GET  /api/opencode/discover"
echo "  - GET  /api/opencode/instances/{id}/tasks"
echo "  - GET  /api/opencode/tasks"
echo "  - GET  /api/opencode/tasks/{id}?instance_id=xxx"
echo ""
echo "📚 详细文档: docs/OPENCODE_DISCOVERY_API.md"
echo "=========================================="
