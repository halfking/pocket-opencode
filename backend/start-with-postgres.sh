#!/bin/bash
# Backend 启动脚本 - 带 PostgreSQL 支持

set -e

echo "=========================================="
echo "启动 OpenCode Pocket Backend"
echo "=========================================="

# 停止现有进程
if pgrep -f pocketd > /dev/null; then
    echo "停止现有 Backend..."
    killall pocketd 2>/dev/null || true
    sleep 2
fi

# 设置环境变量
export POCKET_DEV_AUTH=true
export POCKET_JWT_SECRET=test-secret-key-for-phase7-validation
export POCKET_HTTP_PORT=8088
export POCKET_DB_PATH=./data/pocket.sqlite

# PostgreSQL 配置
export POCKET_POSTGRES_DSN="postgres://pocket_user:pocket_pass@localhost:5432/pocket_db?sslmode=disable"

# OpenCode 实例配置
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
echo "  HTTP Port: $POCKET_HTTP_PORT"
echo "  Dev Auth: $POCKET_DEV_AUTH"
echo "  PostgreSQL: 已配置"
echo "  OpenCode: 已配置"
echo ""

# 启动
nohup ./pocketd > ../logs/backend-postgres.log 2>&1 &

sleep 3

# 验证
if pgrep -f pocketd > /dev/null; then
    PID=$(pgrep -f pocketd | head -1)
    echo "✅ Backend 启动成功 (PID: $PID)"
    
    # 健康检查
    if curl -sf http://localhost:8088/healthz > /dev/null; then
        echo "✅ 健康检查通过"
    fi
    
    # 测试登录
    TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
        -H "Content-Type: application/json" \
        -d '{"username":"admin","password":"admin"}' | jq -r '.token // empty')
    
    if [ -n "$TOKEN" ]; then
        echo "✅ 登录测试通过"
        echo ""
        echo "Backend 就绪！"
    fi
else
    echo "❌ Backend 启动失败"
    tail -30 ../logs/backend-postgres.log
    exit 1
fi
