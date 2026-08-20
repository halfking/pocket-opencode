#!/bin/bash
# OpenCode Pocket API 测试脚本（简化版）

set -e

echo "=========================================="
echo "OpenCode Pocket API 测试"
echo "=========================================="
echo "测试时间: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

# 创建结果目录
RESULT_DIR="test-evidence/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RESULT_DIR"

PASS_COUNT=0
FAIL_COUNT=0

# 测试 1: 健康检查
echo "测试 1: Backend 健康检查"
if curl -sf http://localhost:8088/healthz > /dev/null; then
    echo "✅ PASS"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ FAIL"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    exit 1
fi
echo ""

# 测试 2: 登录
echo "测试 2: 登录 API"
LOGIN_RESULT=$(curl -s -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"${POCKET_AUTH_USER:-admin}\",\"password\":\"${POCKET_AUTH_PASS}\"}")

TOKEN=$(echo "$LOGIN_RESULT" | jq -r '.token // empty')
USER=$(echo "$LOGIN_RESULT" | jq -r '.user // empty')

if [ -n "$TOKEN" ]; then
    echo "✅ PASS - 登录成功"
    echo "   用户: $USER"
    echo "   Token: ${TOKEN:0:40}..."
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "$LOGIN_RESULT" | jq . > "$RESULT_DIR/login-response.json"
else
    echo "❌ FAIL - 登录失败"
    echo "$LOGIN_RESULT"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    exit 1
fi
echo ""

# 测试 3: Token 验证
echo "测试 3: Token 验证"
AUTH_TEST=$(curl -s -o /dev/null -w "%{http_code}" \
    http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN")

if [ "$AUTH_TEST" = "200" ]; then
    echo "✅ PASS - Token 有效"
    PASS_COUNT=$((PASS_COUNT + 1))
else
    echo "❌ FAIL - Token 无效 (HTTP $AUTH_TEST)"
    FAIL_COUNT=$((FAIL_COUNT + 1))
fi
echo ""

# 测试 4: 实例列表
echo "测试 4: OpenCode 实例列表"
INSTANCES=$(curl -s http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN")

INSTANCE_COUNT=$(echo "$INSTANCES" | jq '.instances | length')
echo "实例数量: $INSTANCE_COUNT"

if [ "$INSTANCE_COUNT" -gt 0 ]; then
    echo "✅ PASS - 获取到 $INSTANCE_COUNT 个实例"
    echo ""
    echo "实例详情:"
    echo "$INSTANCES" | jq -r '.instances[] | "  - ID: \(.id)\n    名称: \(.displayName)\n    状态: \(.health)\n    能力: \(.capabilities | join(", "))"'
    PASS_COUNT=$((PASS_COUNT + 1))
    echo "$INSTANCES" | jq . > "$RESULT_DIR/instances.json"
    
    # 保存第一个实例ID供后续测试
    FIRST_INSTANCE=$(echo "$INSTANCES" | jq -r '.instances[0].id')
else
    echo "⚠️  实例列表为空"
    FIRST_INSTANCE=""
fi
echo ""

# 测试 5: 实例详情
if [ -n "$FIRST_INSTANCE" ]; then
    echo "测试 5: 实例详情 (ID: $FIRST_INSTANCE)"
    INSTANCE_DETAIL=$(curl -s "http://localhost:8088/api/instances/$FIRST_INSTANCE" \
        -H "Authorization: Bearer $TOKEN")
    
    if echo "$INSTANCE_DETAIL" | jq -e '.id' > /dev/null 2>&1; then
        echo "✅ PASS - 获取实例详情成功"
        echo "$INSTANCE_DETAIL" | jq .
        PASS_COUNT=$((PASS_COUNT + 1))
        echo "$INSTANCE_DETAIL" | jq . > "$RESULT_DIR/instance-detail.json"
    else
        echo "⚠️  无法获取实例详情"
        echo "$INSTANCE_DETAIL"
    fi
    echo ""
fi

# 测试 6: 会话列表
if [ -n "$FIRST_INSTANCE" ]; then
    echo "测试 6: OpenCode 会话列表"
    SESSIONS=$(curl -s "http://localhost:8088/api/instances/$FIRST_INSTANCE/sessions" \
        -H "Authorization: Bearer $TOKEN")
    
    SESSION_COUNT=$(echo "$SESSIONS" | jq '.sessions | length' 2>/dev/null || echo "0")
    
    if [ "$SESSION_COUNT" -gt 0 ]; then
        echo "✅ PASS - 获取到 $SESSION_COUNT 个会话"
        echo ""
        echo "会话列表 (前5个):"
        echo "$SESSIONS" | jq -r '.sessions[0:5][] | "  - ID: \(.id)\n    标题: \(.title // "无标题")"' 2>/dev/null || true
        PASS_COUNT=$((PASS_COUNT + 1))
        echo "$SESSIONS" | jq . > "$RESULT_DIR/sessions.json" 2>/dev/null || true
        
        # 保存第一个会话ID
        FIRST_SESSION=$(echo "$SESSIONS" | jq -r '.sessions[0].id' 2>/dev/null || echo "")
    else
        echo "⚠️  会话列表为空或API不可用"
        echo "响应: $SESSIONS"
        FIRST_SESSION=""
    fi
    echo ""
fi

# 测试 7: 会话详情
if [ -n "$FIRST_SESSION" ]; then
    echo "测试 7: 会话详情 (ID: $FIRST_SESSION)"
    SESSION_DETAIL=$(curl -s \
        "http://localhost:8088/api/instances/$FIRST_INSTANCE/sessions/$FIRST_SESSION" \
        -H "Authorization: Bearer $TOKEN")
    
    MESSAGE_COUNT=$(echo "$SESSION_DETAIL" | jq '.messages | length' 2>/dev/null || echo "0")
    
    if [ "$MESSAGE_COUNT" -gt 0 ]; then
        echo "✅ PASS - 获取到 $MESSAGE_COUNT 条消息"
        echo ""
        echo "消息列表 (前3条):"
        echo "$SESSION_DETAIL" | jq -r '.messages[0:3][] | "  - 角色: \(.role)\n    内容: \(.content[0:80])..."' 2>/dev/null || true
        PASS_COUNT=$((PASS_COUNT + 1))
        echo "$SESSION_DETAIL" | jq . > "$RESULT_DIR/session-detail.json" 2>/dev/null || true
    else
        echo "⚠️  会话详情为空或API不可用"
        echo "响应: $SESSION_DETAIL"
    fi
    echo ""
fi

# 总结
echo "=========================================="
echo "测试总结"
echo "=========================================="
echo "通过: $PASS_COUNT"
echo "失败: $FAIL_COUNT"
echo ""

if [ $FAIL_COUNT -eq 0 ]; then
    echo "✅ 所有关键测试通过"
else
    echo "❌ 有测试失败"
fi

echo ""
echo "测试结果保存在: $RESULT_DIR/"
echo ""

# 生成简单报告
cat > "$RESULT_DIR/summary.txt" << EOF
OpenCode Pocket API 测试总结
测试时间: $(date '+%Y-%m-%d %H:%M:%S')

通过: $PASS_COUNT
失败: $FAIL_COUNT

详细结果请查看 $RESULT_DIR/ 目录下的 JSON 文件
EOF

cat "$RESULT_DIR/summary.txt"
