#!/bin/bash

# OpenCode 快速测试启动脚本
# 用于快速验证 OpenCode API 可用性

set -e

echo "========================================"
echo "OpenCode 快速测试"
echo "========================================"
echo ""

# 检查 OpenCode 是否运行
echo "检查 OpenCode 服务..."
if curl -s http://localhost:4096/api/health > /dev/null 2>&1; then
    echo "✅ OpenCode 正在运行 (http://localhost:4096)"
else
    echo "❌ OpenCode 未运行"
    echo ""
    echo "请在另一个终端中启动 OpenCode："
    echo "  cd ~/workspace/ai/opencode"
    echo "  bun run dev"
    echo ""
    exit 1
fi

echo ""
echo "测试 API 端点..."
echo ""

# 测试 health
echo "1. 测试 /api/health"
curl -s http://localhost:4096/api/health | jq '.'
echo ""

# 测试 session list
echo "2. 测试 /api/session (列出前3个)"
curl -s "http://localhost:4096/api/session?limit=3&order=desc" | jq '.data | length as $count | {count: $count, sessions: map({id, title, updated: .time.updated})}'
echo ""

# 获取第一个 session 详情
FIRST_SESSION=$(curl -s "http://localhost:4096/api/session?limit=1" | jq -r '.data[0].id // empty')

if [ -n "$FIRST_SESSION" ]; then
    echo "3. 测试 /api/session/$FIRST_SESSION (获取详情)"
    curl -s "http://localhost:4096/api/session/$FIRST_SESSION" | jq '.data | {id, title, projectID, cost, tokens: {input: .tokens.input, output: .tokens.output}}'
    echo ""
    
    echo "4. 测试 /api/session/$FIRST_SESSION/message (获取前5条消息)"
    curl -s "http://localhost:4096/api/session/$FIRST_SESSION/message?limit=5" | jq '.data | length as $count | {count: $count, messageTypes: map(.type)}'
    echo ""
else
    echo "⚠️  没有找到 session"
    echo ""
    echo "创建一个测试 session："
    echo "  curl -X POST http://localhost:4096/api/session \\"
    echo "    -H 'Content-Type: application/json' \\"
    echo "    -d '{\"location\": {\"directory\": \"$(pwd)\"}}'"
    echo ""
fi

echo "========================================"
echo "测试完成！"
echo "========================================"
echo ""
echo "API 端点摘要："
echo "  健康检查: GET /api/health"
echo "  列出会话: GET /api/session?limit=N&order=desc"
echo "  会话详情: GET /api/session/:sessionID"
echo "  会话消息: GET /api/session/:sessionID/message?limit=N"
echo ""
echo "响应格式："
echo "  - 所有响应包装在 { \"data\": ... } 中"
echo "  - 列表响应包含 cursor 字段用于分页"
echo ""
