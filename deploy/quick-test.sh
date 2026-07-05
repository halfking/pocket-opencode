#!/bin/bash
# OpenCode Pocket 快速测试验证脚本
# 用于验证部署后的系统功能

set -e

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 配置
API_BASE="${API_BASE:-http://localhost:8088}"
TEST_USER="${TEST_USER:-admin}"
TEST_PASS="${TEST_PASS:-admin}"

echo "========================================"
echo "OpenCode Pocket 快速测试验证"
echo "========================================"
echo "API Base: $API_BASE"
echo "Test User: $TEST_USER"
echo ""

# 测试计数
PASS_COUNT=0
FAIL_COUNT=0

# 测试函数
test_api() {
    local name=$1
    local method=$2
    local endpoint=$3
    local data=$4
    local headers=$5
    local expected_code=${6:-200}
    
    echo -n "测试: $name ... "
    
    if [ "$method" = "GET" ]; then
        response=$(curl -s -w "\n%{http_code}" $headers "$API_BASE$endpoint")
    else
        response=$(curl -s -w "\n%{http_code}" -X $method $headers -H "Content-Type: application/json" -d "$data" "$API_BASE$endpoint")
    fi
    
    http_code=$(echo "$response" | tail -n 1)
    body=$(echo "$response" | sed '$d')
    
    if [ "$http_code" = "$expected_code" ]; then
        echo -e "${GREEN}✅ PASS${NC} (HTTP $http_code)"
        PASS_COUNT=$((PASS_COUNT + 1))
        return 0
    else
        echo -e "${RED}❌ FAIL${NC} (HTTP $http_code, expected $expected_code)"
        echo "Response: $body"
        FAIL_COUNT=$((FAIL_COUNT + 1))
        return 1
    fi
}

echo "========================================="
echo "1. 基础健康检查"
echo "========================================="

# 健康检查 (使用 /healthz 而不是 /api/health)
test_api "Backend 健康检查" "GET" "/healthz" "" "" "200"

echo ""
echo "========================================="
echo "2. 认证测试"
echo "========================================="

# 登录测试
echo -n "测试: 用户登录 ... "
login_response=$(curl -s -X POST "$API_BASE/api/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"$TEST_USER\",\"password\":\"$TEST_PASS\"}")

TOKEN=$(echo "$login_response" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('token',''))" 2>/dev/null || echo "")

if [ -n "$TOKEN" ]; then
    echo -e "${GREEN}✅ PASS${NC}"
    echo "   Token: ${TOKEN:0:40}..."
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}❌ FAIL${NC}"
    echo "   Response: $login_response"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo ""
    echo "无法获取认证 token，后续测试将失败"
    exit 1
fi

AUTH_HEADER="-H \"Authorization: Bearer $TOKEN\""

echo ""
echo "========================================="
echo "3. 任务管理 API 测试"
echo "========================================="

# 列出任务
echo -n "测试: 列出所有任务 ... "
tasks_response=$(curl -s -H "Authorization: Bearer $TOKEN" "$API_BASE/api/tasks")
task_count=$(echo "$tasks_response" | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('tasks',[])))" 2>/dev/null || echo "0")

if [ $? -eq 0 ]; then
    echo -e "${GREEN}✅ PASS${NC} ($task_count 个任务)"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}❌ FAIL${NC}"
    echo "   Response: $tasks_response"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# 创建任务
TASK_ID="test-$(date +%s)"
echo -n "测试: 创建新任务 (ID: $TASK_ID) ... "
create_response=$(curl -s -X POST "$API_BASE/api/tasks" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"id\":\"$TASK_ID\",\"title\":\"测试任务\",\"description\":\"自动化测试创建\",\"status\":\"active\",\"priority\":\"high\"}")

created_id=$(echo "$create_response" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('id',''))" 2>/dev/null || echo "")

if [ "$created_id" = "$TASK_ID" ]; then
    echo -e "${GREEN}✅ PASS${NC}"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}❌ FAIL${NC}"
    echo "   Response: $create_response"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

# 获取任务详情
if [ "$created_id" = "$TASK_ID" ]; then
    echo -n "测试: 获取任务详情 ... "
    detail_response=$(curl -s -H "Authorization: Bearer $TOKEN" "$API_BASE/api/tasks/$TASK_ID")
    detail_id=$(echo "$detail_response" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('id',''))" 2>/dev/null || echo "")
    
    if [ "$detail_id" = "$TASK_ID" ]; then
        echo -e "${GREEN}✅ PASS${NC}"
        PASS_COUNT=$((PASS_COUNT + 1))
    else
        echo -e "${RED}❌ FAIL${NC}"
        echo "   Response: $detail_response"
        FAIL_COUNT=$((FAIL_COUNT + 1))
    fi
fi

echo ""
echo "========================================="
echo "4. 实例管理 API 测试"
echo "========================================="

# 列出实例
echo -n "测试: 列出实例 ... "
instances_response=$(curl -s -H "Authorization: Bearer $TOKEN" "$API_BASE/api/instances")
if echo "$instances_response" | python3 -c "import json,sys; json.load(sys.stdin)" 2>/dev/null; then
    echo -e "${GREEN}✅ PASS${NC}"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo -e "${RED}❌ FAIL${NC}"
    echo "   Response: $instances_response"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi

echo ""
echo "========================================="
echo "5. 数据库连接测试"
echo "========================================="

if command -v psql &> /dev/null; then
    echo -n "测试: PostgreSQL 连接 ... "
    
    # 从环境变量读取配置
    source ../.env 2>/dev/null || true
    
    if [ -n "$DB_USER" ] && [ -n "$DB_NAME" ]; then
        PGPASSWORD=$DB_PASSWORD psql -h ${DB_HOST:-localhost} -U $DB_USER -d $DB_NAME -c "SELECT COUNT(*) FROM tasks;" &>/dev/null
        if [ $? -eq 0 ]; then
            task_count=$(PGPASSWORD=$DB_PASSWORD psql -h ${DB_HOST:-localhost} -U $DB_USER -d $DB_NAME -t -c "SELECT COUNT(*) FROM tasks;" 2>/dev/null | xargs)
            echo -e "${GREEN}✅ PASS${NC} ($task_count 条记录)"
            PASS_COUNT=$((PASS_COUNT + 1))
        else
            echo -e "${RED}❌ FAIL${NC}"
            FAIL_COUNT=$((FAIL_COUNT + 1))
        fi
    else
        echo -e "${YELLOW}⚠️  SKIP${NC} (未配置数据库环境变量)"
    fi
else
    echo "测试: PostgreSQL 连接 ... ${YELLOW}⚠️  SKIP${NC} (psql 未安装)"
fi

echo ""
echo "========================================="
echo "测试总结"
echo "========================================="
echo -e "通过: ${GREEN}$PASS_COUNT${NC}"
echo -e "失败: ${RED}$FAIL_COUNT${NC}"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo -e "${GREEN}✅ 所有测试通过！系统运行正常。${NC}"
    exit 0
else
    echo -e "${RED}❌ 有 $FAIL_COUNT 个测试失败，请检查日志。${NC}"
    exit 1
fi
