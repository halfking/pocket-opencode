#!/bin/bash
# test-opencode-connection.sh
# OpenCode API 连接完整测试脚本

set -e

echo "=========================================="
echo "OpenCode API 连接测试"
echo "=========================================="

# 1. 检查 OpenCode API 是否运行
echo ""
echo "1. 检查 OpenCode API..."
if curl -sf http://localhost:4096/api/session > /dev/null 2>&1; then
    echo "✅ OpenCode API 正常运行"
    SESSION_COUNT=$(curl -s http://localhost:4096/api/session 2>/dev/null | jq '.data | length' 2>/dev/null || echo "N/A")
    echo "   会话数量: $SESSION_COUNT"
else
    echo "❌ OpenCode API 未运行"
    echo ""
    echo "请启动 OpenCode:"
    echo "  cd ~/workspace/ai/opencode"
    echo "  bun run dev"
    echo ""
    exit 1
fi

# 2. 检查 Pocket Backend
echo ""
echo "2. 检查 Pocket Backend..."
if curl -sf http://localhost:8088/healthz > /dev/null; then
    echo "✅ Backend 正常运行"
else
    echo "❌ Backend 未运行"
    echo ""
    echo "请启动 Backend:"
    echo "  cd $(dirname "$0")/backend"
    echo "  ./start-dev.sh"
    echo ""
    exit 1
fi

# 3. 测试登录
echo ""
echo "3. 测试登录..."
TOKEN=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d '{"username":"admin","password":"admin"}' | jq -r '.token // empty')

if [ -n "$TOKEN" ]; then
    echo "✅ 登录成功"
    echo "   Token: ${TOKEN:0:40}..."
else
    echo "❌ 登录失败"
    exit 1
fi

# 4. 测试实例列表
echo ""
echo "4. 测试实例发现..."
INSTANCES=$(curl -s http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN")

INSTANCE_COUNT=$(echo "$INSTANCES" | jq '.instances | length')
echo "实例数量: $INSTANCE_COUNT"

if [ "$INSTANCE_COUNT" -gt 0 ]; then
    echo ""
    echo "实例详情:"
    echo "$INSTANCES" | jq -r '.instances[] | "  - ID: \(.id)\n    名称: \(.displayName)\n    状态: \(.health)\n    地址: \(.baseURL // "N/A")"'
    
    HEALTH=$(echo "$INSTANCES" | jq -r '.instances[0].health')
    
    if [ "$HEALTH" = "healthy" ]; then
        echo ""
        echo "✅ OpenCode 实例连接成功！"
        
        # 5. 测试获取会话列表（如果有会话的话）
        echo ""
        echo "5. 测试获取会话列表..."
        # 这里可以添加会话列表测试
        
    elif [ "$HEALTH" = "unknown" ]; then
        echo ""
        echo "⚠️  实例状态: unknown"
        echo "可能原因："
        echo "  1. OpenCode API 是桌面版（返回 HTML）"
        echo "  2. OpenCode API 端口不是 4096"
        echo "  3. Backend 还没有执行健康检查"
        echo ""
        echo "解决方案："
        echo "  - 确保运行的是 OpenCode API 服务器 (bun run dev)"
        echo "  - 不是桌面应用 (OpenCode.app)"
    else
        echo ""
        echo "⚠️  实例状态: $HEALTH"
    fi
else
    echo "⚠️  没有发现实例"
fi

echo ""
echo "=========================================="
echo "测试完成"
echo "=========================================="
echo ""
echo "💡 提示:"
echo "如果实例状态是 'unknown'，请确保："
echo "1. OpenCode API 服务器正在运行 (cd ~/workspace/ai/opencode && bun run dev)"
echo "2. 端口是 4096"
echo "3. 返回 JSON 格式，不是 HTML"
echo ""
