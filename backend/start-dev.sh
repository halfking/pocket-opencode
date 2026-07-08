#!/bin/bash
# Backend 开发模式启动脚本

set -e

cd "$(dirname "$0")"

echo "=========================================="
echo "启动 OpenCode Pocket Backend (开发模式)"
echo "=========================================="

# 停止已有进程
if pgrep -f pocketd > /dev/null; then
    echo "停止现有 backend 进程..."
    killall pocketd 2>/dev/null || true
    sleep 1
fi

# 设置环境变量
export POCKET_DEV_AUTH=true
export POCKET_JWT_SECRET=test-secret-key-for-phase7-validation
export POCKET_HTTP_PORT=8088
export POCKET_DB_PATH=./data/pocket.sqlite

# ✨ 新增：配置本地 OpenCode 实例
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "local-opencode",
    "displayName": "本地 OpenCode 实例",
    "baseURL": "http://localhost:4096",
    "environment": "development",
    "capabilities": ["session", "summary", "pty"]
  }
]'

echo ""
echo "环境配置:"
echo "  POCKET_DEV_AUTH: $POCKET_DEV_AUTH"
echo "  POCKET_HTTP_PORT: $POCKET_HTTP_PORT"
echo "  POCKET_DB_PATH: $POCKET_DB_PATH"
echo ""
echo "OpenCode 实例配置:"
echo "$POCKET_OPENCODE_INSTANCES" | jq .
echo ""

# 启动 backend
echo "启动 backend..."
nohup ./pocketd > ../logs/backend-dev.log 2>&1 &

sleep 2

# 验证启动
if pgrep -f pocketd > /dev/null; then
    PID=$(pgrep -f pocketd | head -1)
    echo "✅ Backend 启动成功 (PID: $PID)"
    
    # 健康检查
    if curl -sf http://localhost:8088/healthz > /dev/null; then
        echo "✅ 健康检查通过"
    else
        echo "❌ 健康检查失败"
        exit 1
    fi
    
    # 测试登录
    echo ""
    echo "测试登录 API..."
    LOGIN_RESULT=$(curl -s -X POST http://localhost:8088/api/auth/login \
        -H "Content-Type: application/json" \
        -d '{"username":"admin","password":"admin"}')
    
    TOKEN=$(echo "$LOGIN_RESULT" | jq -r '.token // empty')
    
    if [ -n "$TOKEN" ]; then
        echo "✅ 登录测试通过"
        echo "   Token: ${TOKEN:0:30}..."
        
        # 检查实例列表
        echo ""
        echo "检查 OpenCode 实例..."
        INSTANCES=$(curl -s http://localhost:8088/api/instances \
            -H "Authorization: Bearer $TOKEN")
        
        INSTANCE_COUNT=$(echo "$INSTANCES" | jq '.instances | length')
        echo "✅ 实例列表: $INSTANCE_COUNT 个实例"
        
        if [ "$INSTANCE_COUNT" -gt 0 ]; then
            echo ""
            echo "实例详情:"
            echo "$INSTANCES" | jq -r '.instances[] | "  - ID: \(.id)\n    名称: \(.displayName)\n    地址: \(.baseURL // "N/A")\n    状态: \(.health)"'
        fi
    else
        echo "❌ 登录测试失败"
        echo "   响应: $LOGIN_RESULT"
        exit 1
    fi
    
    echo ""
    echo "=========================================="
    echo "Backend 就绪，可以开始测试"
    echo "=========================================="
    echo ""
    echo "日志文件: ../logs/backend-dev.log"
    echo "查看日志: tail -f ../logs/backend-dev.log"
    echo "停止服务: killall pocketd"
    echo ""
else
    echo "❌ Backend 启动失败"
    echo "查看日志: cat ../logs/backend-dev.log"
    exit 1
fi
