#!/bin/bash
# scripts/test-redclaw-integration.sh
# 测试 Pocket ↔ RedClaw 集成链路
#
# NOTE: 这是一个可选的集成测试脚本，用于测试未来的 RedClaw 集成功能。
# 主应用在未配置 POCKET_REDCLAW_BASE_URL 时会优雅降级，RedClaw 相关端点返回 503。
#
# 前置条件：
#   - RedClaw2 仓库位于：/Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go
#   - 此测试不影响核心应用功能
#   - 仅用于开发和集成测试场景

set -e

echo "=== RedClaw Integration E2E Test ==="
echo ""

# 确保测试结束后清理所有后台进程
cleanup() {
    echo ""
    echo "Cleaning up..."
    kill $REDCLAW_PID 2>/dev/null || true
    kill $POCKET_PID 2>/dev/null || true
    wait $REDCLAW_PID 2>/dev/null || true
    wait $POCKET_PID 2>/dev/null || true
    echo "Done."
}
trap cleanup EXIT

# 1. 启动 RedClaw 模拟服务器 (Pocket 集成网关)
echo "[1/4] Starting mock RedClaw gateway..."
cd /Users/xutaohuang/workspace/FreshLab/RedClaw2/enterprise/gateway-go
POCKET_GATEWAY_SECRET="test-e2e-secret" go run ./cmd/gateway &
REDCLAW_PID=$!
sleep 2

# 验证 RedClaw 健康检查
echo "[2/4] Testing RedClaw health endpoint..."
HEALTH_CHECK=$(curl -s http://localhost:8092/health)
if echo "$HEALTH_CHECK" | grep -q '"status":"ok"'; then
    echo "  ✅ Health check passed"
else
    echo "  ❌ Health check failed: $HEALTH_CHECK"
    exit 1
fi

# 测试 RedClaw Chat API
echo "[3/4] Testing RedClaw chat endpoint..."
CHAT_RESPONSE=$(curl -s -X POST http://localhost:8092/api/v1/pocket/llm/chat \
  -H "Authorization: Bearer test-e2e-secret" \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello"}]}')
if echo "$CHAT_RESPONSE" | grep -q '"role":"assistant"'; then
    echo "  ✅ Chat API passed"
else
    echo "  ❌ Chat API failed: $CHAT_RESPONSE"
    exit 1
fi

# 启动 Pocket 后端并测试桥接
echo "[4/4] Starting Pocket backend..."
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/backend
POCKET_REDCLAW_BASE_URL="http://localhost:8092" \
POCKET_REDCLAW_SECRET="test-e2e-secret" \
POCKET_REDCLAW_TENANT_ID="e2e-test" \
POCKET_DEV_AUTH=true \
POCKET_HTTP_PORT=8088 \
go run ./cmd/pocketd &
POCKET_PID=$!
sleep 3

# 测试 Pocket RedClaw 健康检查代理
echo "  Testing Pocket RedClaw health proxy..."
POCKET_HEALTH=$(curl -s http://localhost:8088/api/redclaw/health)
if echo "$POCKET_HEALTH" | grep -q '"connected":true'; then
    echo "  ✅ Pocket RedClaw health proxy passed"
else
    echo "  ❌ Pocket RedClaw health proxy failed: $POCKET_HEALTH"
    exit 1
fi

# 测试 Pocket RedClaw Chat 代理
echo "  Testing Pocket RedClaw chat proxy..."
POCKET_CHAT=$(curl -s -X POST http://localhost:8088/api/redclaw/chat \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello from Pocket"}]}')
if echo "$POCKET_CHAT" | grep -q '"role":"assistant"'; then
    echo "  ✅ Pocket RedClaw chat proxy passed"
else
    echo "  ❌ Pocket RedClaw chat proxy failed: $POCKET_CHAT"
    exit 1
fi

echo ""
echo "=== All E2E tests passed! ==="