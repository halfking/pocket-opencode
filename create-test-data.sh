#!/bin/bash
# 创建测试数据

set -e

echo "=========================================="
echo "创建测试数据"
echo "=========================================="

# 获取 token
echo "1. 获取认证 token..."
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}' \
    | jq -r '.token')

if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then
    echo "❌ 登录失败"
    exit 1
fi
echo "✅ 登录成功"

# 创建测试任务
echo ""
echo "2. 创建测试任务..."

# 任务 1
TASK1=$(curl -s -X POST http://localhost:8088/api/tasks \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "title": "测试任务 1 - 登录流程验证",
        "description": "验证模拟器登录功能",
        "status": "pending",
        "priority": "high"
    }')

echo "$TASK1" | jq -e '.id' > /dev/null && echo "✅ 任务 1 创建成功" || echo "⚠️  任务 1 创建失败"

# 任务 2
TASK2=$(curl -s -X POST http://localhost:8088/api/tasks \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "title": "测试任务 2 - OpenCode 实例管理",
        "description": "测试实例列表和详情功能",
        "status": "in_progress",
        "priority": "medium"
    }')

echo "$TASK2" | jq -e '.id' > /dev/null && echo "✅ 任务 2 创建成功" || echo "⚠️  任务 2 创建失败"

# 任务 3
TASK3=$(curl -s -X POST http://localhost:8088/api/tasks \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{
        "title": "测试任务 3 - 会话详情查看",
        "description": "测试任务会话和消息显示",
        "status": "completed",
        "priority": "low"
    }')

echo "$TASK3" | jq -e '.id' > /dev/null && echo "✅ 任务 3 创建成功" || echo "⚠️  任务 3 创建失败"

# 验证任务列表
echo ""
echo "3. 验证任务列表..."
TASKS=$(curl -s http://localhost:8088/api/tasks \
    -H "Authorization: Bearer $TOKEN")

TASK_COUNT=$(echo "$TASKS" | jq '.tasks | length')
echo "✅ 任务总数: $TASK_COUNT"

if [ "$TASK_COUNT" -gt 0 ]; then
    echo ""
    echo "任务列表:"
    echo "$TASKS" | jq -r '.tasks[] | "  - [\(.status)] \(.title)"'
fi

echo ""
echo "=========================================="
echo "测试数据创建完成"
echo "=========================================="
