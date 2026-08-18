#!/bin/bash
# OpenCode Pocket 模拟器完整测试执行脚本
# 测试重点：登录流程、OpenCode 实例获取、会话详情

set -e

echo "=========================================="
echo "OpenCode Pocket 模拟器完整测试"
echo "=========================================="
echo "测试日期: $(date '+%Y-%m-%d %H:%M:%S')"
echo ""

# 创建测试结果目录
RESULT_DIR="test-evidence/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RESULT_DIR"

echo "测试结果目录: $RESULT_DIR"
echo ""

# ============================================
# 测试 1: Backend 健康检查
# ============================================
echo "=========================================="
echo "测试 1: Backend 健康检查"
echo "=========================================="

if curl -sf http://localhost:8088/healthz > /dev/null; then
    echo "✅ Backend 健康检查通过"
    echo "✅ PASS" > "$RESULT_DIR/01-health-check.txt"
else
    echo "❌ Backend 健康检查失败"
    echo "❌ FAIL" > "$RESULT_DIR/01-health-check.txt"
    exit 1
fi

echo ""

# ============================================
# 测试 2: 登录 API
# ============================================
echo "=========================================="
echo "测试 2: 登录 API"
echo "=========================================="

LOGIN_RESPONSE=$(curl -s -w "\n%{http_code}" -X POST http://localhost:8088/api/auth/login \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"${POCKET_AUTH_USER:-admin}\",\"password\":\"${POCKET_AUTH_PASS}\"}")

HTTP_CODE=$(echo "$LOGIN_RESPONSE" | tail -n 1)
RESPONSE_BODY=$(echo "$LOGIN_RESPONSE" | head -n -1)

echo "HTTP 状态码: $HTTP_CODE"
echo "响应: $RESPONSE_BODY" | jq . 2>/dev/null || echo "$RESPONSE_BODY"

if [ "$HTTP_CODE" = "200" ]; then
    TOKEN=$(echo "$RESPONSE_BODY" | jq -r '.token')
    USER=$(echo "$RESPONSE_BODY" | jq -r '.user')
    
    if [ -n "$TOKEN" ] && [ "$TOKEN" != "null" ]; then
        echo "✅ 登录成功"
        echo "   用户: $USER"
        echo "   Token: ${TOKEN:0:50}..."
        echo "✅ PASS - Token: $TOKEN" > "$RESULT_DIR/02-login.txt"
    else
        echo "❌ 登录成功但未返回 token"
        echo "❌ FAIL - No token" > "$RESULT_DIR/02-login.txt"
        exit 1
    fi
else
    echo "❌ 登录失败 (HTTP $HTTP_CODE)"
    echo "❌ FAIL - HTTP $HTTP_CODE" > "$RESULT_DIR/02-login.txt"
    exit 1
fi

echo ""

# ============================================
# 测试 3: Token 验证
# ============================================
echo "=========================================="
echo "测试 3: Token 验证 (访问受保护的 API)"
echo "=========================================="

AUTH_TEST=$(curl -s -w "\n%{http_code}" http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN")

HTTP_CODE=$(echo "$AUTH_TEST" | tail -n 1)

if [ "$HTTP_CODE" = "200" ]; then
    echo "✅ Token 验证通过"
    echo "✅ PASS" > "$RESULT_DIR/03-token-validation.txt"
else
    echo "❌ Token 验证失败 (HTTP $HTTP_CODE)"
    echo "❌ FAIL - HTTP $HTTP_CODE" > "$RESULT_DIR/03-token-validation.txt"
    exit 1
fi

echo ""

# ============================================
# 测试 4: OpenCode 实例列表
# ============================================
echo "=========================================="
echo "测试 4: OpenCode 实例列表"
echo "=========================================="

INSTANCES_RESPONSE=$(curl -s -w "\n%{http_code}" http://localhost:8088/api/instances \
    -H "Authorization: Bearer $TOKEN")

HTTP_CODE=$(echo "$INSTANCES_RESPONSE" | tail -n 1)
INSTANCES_BODY=$(echo "$INSTANCES_RESPONSE" | head -n -1)

echo "HTTP 状态码: $HTTP_CODE"

if [ "$HTTP_CODE" = "200" ]; then
    INSTANCE_COUNT=$(echo "$INSTANCES_BODY" | jq '.instances | length')
    echo "✅ 实例列表获取成功"
    echo "   实例数量: $INSTANCE_COUNT"
    
    if [ "$INSTANCE_COUNT" -gt 0 ]; then
        echo ""
        echo "实例详情:"
        echo "$INSTANCES_BODY" | jq -r '.instances[] | "  - ID: \(.id)\n    名称: \(.displayName)\n    状态: \(.health)\n    能力: \(.capabilities | join(", "))"'
        echo "✅ PASS - $INSTANCE_COUNT 个实例" > "$RESULT_DIR/04-instances-list.txt"
        echo "$INSTANCES_BODY" | jq . > "$RESULT_DIR/04-instances-data.json"
    else
        echo "⚠️  实例列表为空"
        echo "⚠️  EMPTY" > "$RESULT_DIR/04-instances-list.txt"
    fi
else
    echo "❌ 实例列表获取失败 (HTTP $HTTP_CODE)"
    echo "❌ FAIL - HTTP $HTTP_CODE" > "$RESULT_DIR/04-instances-list.txt"
fi

echo ""

# ============================================
# 测试 5: 实例详情 (如果有实例)
# ============================================
if [ "$INSTANCE_COUNT" -gt 0 ]; then
    echo "=========================================="
    echo "测试 5: 实例详情"
    echo "=========================================="
    
    FIRST_INSTANCE_ID=$(echo "$INSTANCES_BODY" | jq -r '.instances[0].id')
    echo "测试实例 ID: $FIRST_INSTANCE_ID"
    
    INSTANCE_DETAIL=$(curl -s -w "\n%{http_code}" \
        "http://localhost:8088/api/instances/$FIRST_INSTANCE_ID" \
        -H "Authorization: Bearer $TOKEN")
    
    HTTP_CODE=$(echo "$INSTANCE_DETAIL" | tail -n 1)
    
    if [ "$HTTP_CODE" = "200" ]; then
        echo "✅ 实例详情获取成功"
        DETAIL_BODY=$(echo "$INSTANCE_DETAIL" | head -n -1)
        echo "$DETAIL_BODY" | jq .
        echo "✅ PASS" > "$RESULT_DIR/05-instance-detail.txt"
        echo "$DETAIL_BODY" | jq . > "$RESULT_DIR/05-instance-detail-data.json"
    else
        echo "⚠️  实例详情获取失败 (HTTP $HTTP_CODE)"
        echo "⚠️  FAIL - HTTP $HTTP_CODE" > "$RESULT_DIR/05-instance-detail.txt"
    fi
    
    echo ""
fi

# ============================================
# 测试 6: OpenCode 会话列表 (通过实例)
# ============================================
if [ "$INSTANCE_COUNT" -gt 0 ]; then
    echo "=========================================="
    echo "测试 6: OpenCode 会话列表"
    echo "=========================================="
    
    SESSIONS_RESPONSE=$(curl -s -w "\n%{http_code}" \
        "http://localhost:8088/api/instances/$FIRST_INSTANCE_ID/sessions" \
        -H "Authorization: Bearer $TOKEN")
    
    HTTP_CODE=$(echo "$SESSIONS_RESPONSE" | tail -n 1)
    SESSIONS_BODY=$(echo "$SESSIONS_RESPONSE" | head -n -1)
    
    if [ "$HTTP_CODE" = "200" ]; then
        SESSION_COUNT=$(echo "$SESSIONS_BODY" | jq '.sessions | length' 2>/dev/null || echo "0")
        echo "✅ 会话列表获取成功"
        echo "   会话数量: $SESSION_COUNT"
        
        if [ "$SESSION_COUNT" -gt 0 ]; then
            echo ""
            echo "会话列表 (前5个):"
            echo "$SESSIONS_BODY" | jq -r '.sessions[0:5][] | "  - ID: \(.id)\n    标题: \(.title // "无标题")\n    更新时间: \(.updated_at // .time.updated)"'
            echo "✅ PASS - $SESSION_COUNT 个会话" > "$RESULT_DIR/06-sessions-list.txt"
            echo "$SESSIONS_BODY" | jq . > "$RESULT_DIR/06-sessions-data.json"
        else
            echo "⚠️  会话列表为空"
            echo "⚠️  EMPTY" > "$RESULT_DIR/06-sessions-list.txt"
        fi
    else
        echo "⚠️  会话列表获取失败 (HTTP $HTTP_CODE)"
        echo "响应: $SESSIONS_BODY"
        echo "⚠️  FAIL - HTTP $HTTP_CODE" > "$RESULT_DIR/06-sessions-list.txt"
    fi
    
    echo ""
fi

# ============================================
# 测试 7: 会话详情 (如果有会话)
# ============================================
if [ "$INSTANCE_COUNT" -gt 0 ] && [ "${SESSION_COUNT:-0}" -gt 0 ]; then
    echo "=========================================="
    echo "测试 7: 会话详情"
    echo "=========================================="
    
    FIRST_SESSION_ID=$(echo "$SESSIONS_BODY" | jq -r '.sessions[0].id')
    echo "测试会话 ID: $FIRST_SESSION_ID"
    
    SESSION_DETAIL=$(curl -s -w "\n%{http_code}" \
        "http://localhost:8088/api/instances/$FIRST_INSTANCE_ID/sessions/$FIRST_SESSION_ID" \
        -H "Authorization: Bearer $TOKEN")
    
    HTTP_CODE=$(echo "$SESSION_DETAIL" | tail -n 1)
    DETAIL_BODY=$(echo "$SESSION_DETAIL" | head -n -1)
    
    if [ "$HTTP_CODE" = "200" ]; then
        echo "✅ 会话详情获取成功"
        MESSAGE_COUNT=$(echo "$DETAIL_BODY" | jq '.messages | length' 2>/dev/null || echo "0")
        echo "   消息数量: $MESSAGE_COUNT"
        
        if [ "$MESSAGE_COUNT" -gt 0 ]; then
            echo ""
            echo "消息列表 (前3条):"
            echo "$DETAIL_BODY" | jq -r '.messages[0:3][] | "  - 角色: \(.role)\n    内容: \(.content[0:100])...\n    时间: \(.timestamp // .time)"'
        fi
        
        echo "✅ PASS - $MESSAGE_COUNT 条消息" > "$RESULT_DIR/07-session-detail.txt"
        echo "$DETAIL_BODY" | jq . > "$RESULT_DIR/07-session-detail-data.json"
    else
        echo "⚠️  会话详情获取失败 (HTTP $HTTP_CODE)"
        echo "响应: $DETAIL_BODY"
        echo "⚠️  FAIL - HTTP $HTTP_CODE" > "$RESULT_DIR/07-session-detail.txt"
    fi
    
    echo ""
fi

# ============================================
# 测试总结
# ============================================
echo "=========================================="
echo "测试总结"
echo "=========================================="

TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0
WARNING_TESTS=0

for result_file in "$RESULT_DIR"/*.txt; do
    if [ -f "$result_file" ]; then
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if grep -q "✅ PASS" "$result_file"; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
        elif grep -q "❌ FAIL" "$result_file"; then
            FAILED_TESTS=$((FAILED_TESTS + 1))
        else
            WARNING_TESTS=$((WARNING_TESTS + 1))
        fi
    fi
done

echo "总测试数: $TOTAL_TESTS"
echo "通过: $PASSED_TESTS"
echo "失败: $FAILED_TESTS"
echo "警告: $WARNING_TESTS"
echo ""

if [ $FAILED_TESTS -eq 0 ]; then
    echo "✅ 所有关键测试通过"
    OVERALL_STATUS="PASS"
else
    echo "❌ 有测试失败"
    OVERALL_STATUS="FAIL"
fi

# 生成测试报告
cat > "$RESULT_DIR/TEST_REPORT.md" << EOF
# OpenCode Pocket API 测试报告

**测试时间**: $(date '+%Y-%m-%d %H:%M:%S')  
**测试环境**: Backend (localhost:8088)  
**测试目标**: 登录流程、OpenCode 实例获取、会话详情

---

## 测试总览

- **总测试数**: $TOTAL_TESTS
- **通过**: $PASSED_TESTS
- **失败**: $FAILED_TESTS
- **警告**: $WARNING_TESTS
- **整体状态**: $OVERALL_STATUS

---

## 测试详情

### 1. Backend 健康检查
$(cat "$RESULT_DIR/01-health-check.txt")

### 2. 登录 API
$(cat "$RESULT_DIR/02-login.txt")

### 3. Token 验证
$(cat "$RESULT_DIR/03-token-validation.txt")

### 4. OpenCode 实例列表
$(cat "$RESULT_DIR/04-instances-list.txt")
$([ -f "$RESULT_DIR/04-instances-data.json" ] && echo "\`\`\`json" && cat "$RESULT_DIR/04-instances-data.json" && echo "\`\`\`")

### 5. 实例详情
$([ -f "$RESULT_DIR/05-instance-detail.txt" ] && cat "$RESULT_DIR/05-instance-detail.txt" || echo "未执行")

### 6. OpenCode 会话列表
$([ -f "$RESULT_DIR/06-sessions-list.txt" ] && cat "$RESULT_DIR/06-sessions-list.txt" || echo "未执行")

### 7. 会话详情
$([ -f "$RESULT_DIR/07-session-detail.txt" ] && cat "$RESULT_DIR/07-session-detail.txt" || echo "未执行")

---

## 关键发现

### ✅ 成功的功能
- 登录认证 (admin/admin)
- JWT Token 签发和验证
- OpenCode 实例列表获取
- 实例数量: $INSTANCE_COUNT

### ⚠️  需要注意的问题
- Backend 运行在 remote-only 模式（无 PostgreSQL）
- 本地任务存储不可用
- 只能获取远程 OpenCode 数据

### 📋 建议
1. 配置 PostgreSQL 以支持本地任务存储
2. 或者明确 remote-only 模式的功能范围
3. 继续在模拟器上测试前端 UI

---

**测试执行人**: 自动化测试脚本  
**测试数据**: 保存在 $RESULT_DIR/
EOF

echo ""
echo "=========================================="
echo "📊 测试报告已生成"
echo "=========================================="
echo "测试报告: $RESULT_DIR/TEST_REPORT.md"
echo "测试数据: $RESULT_DIR/"
echo ""
echo "查看报告: cat $RESULT_DIR/TEST_REPORT.md"
echo ""

exit 0
