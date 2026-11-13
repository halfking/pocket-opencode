#!/bin/bash
# Knowledge Base API 测试脚本
# 测试新实现的 Notebooks 和 Note Links API

set -e

API_BASE="${API_BASE:-http://localhost:8088}"
TOKEN="${POCKET_JWT_TOKEN:-}"

if [ -z "$TOKEN" ]; then
    echo "⚠️  警告: POCKET_JWT_TOKEN 未设置，某些测试可能失败"
    echo "请先登录获取 token 或设置环境变量"
fi

echo "==================================="
echo "Knowledge Base API 测试"
echo "==================================="
echo "API Base: $API_BASE"
echo ""

# 辅助函数
api_call() {
    local method=$1
    local endpoint=$2
    local data=$3
    
    if [ -z "$data" ]; then
        curl -s -X "$method" \
            -H "Authorization: Bearer $TOKEN" \
            -H "Content-Type: application/json" \
            "$API_BASE$endpoint"
    else
        curl -s -X "$method" \
            -H "Authorization: Bearer $TOKEN" \
            -H "Content-Type: application/json" \
            -d "$data" \
            "$API_BASE$endpoint"
    fi
}

echo "1. 测试 Notebooks API"
echo "-----------------------------------"

# 1.1 列出笔记本（应该为空或有数据）
echo -n "GET /api/notebooks ... "
RESPONSE=$(api_call GET /api/notebooks)
if echo "$RESPONSE" | jq . > /dev/null 2>&1; then
    echo "✓"
    echo "$RESPONSE" | jq -r 'if type=="array" then "  笔记本数: \(length)" else "  错误: \(.error // "未知")" end'
else
    echo "❌ (无效响应)"
    echo "$RESPONSE"
fi

# 1.2 创建笔记本
echo -n "POST /api/notebooks ... "
NOTEBOOK_DATA='{
  "name": "测试笔记本",
  "icon": "📚",
  "color": "#3498db"
}'
CREATE_RESPONSE=$(api_call POST /api/notebooks "$NOTEBOOK_DATA")
if echo "$CREATE_RESPONSE" | jq -e '.id' > /dev/null 2>&1; then
    NOTEBOOK_ID=$(echo "$CREATE_RESPONSE" | jq -r '.id')
    echo "✓ (ID: $NOTEBOOK_ID)"
else
    echo "❌"
    echo "$CREATE_RESPONSE"
fi

# 1.3 获取笔记本详情
if [ -n "$NOTEBOOK_ID" ]; then
    echo -n "GET /api/notebooks/$NOTEBOOK_ID ... "
    DETAIL_RESPONSE=$(api_call GET "/api/notebooks/$NOTEBOOK_ID")
    if echo "$DETAIL_RESPONSE" | jq -e '.name' > /dev/null 2>&1; then
        echo "✓"
        echo "$DETAIL_RESPONSE" | jq -r '  "  名称: \(.name), 笔记数: \(.noteCount // 0)"'
    else
        echo "❌"
    fi
fi

echo ""
echo "2. 测试 Note Links API"
echo "-----------------------------------"

# 2.1 创建测试笔记
echo -n "创建测试笔记 1 ... "
NOTE1_DATA='{
  "content": "这是测试笔记 1",
  "title": "测试笔记 1",
  "contentType": "text"
}'
NOTE1_RESPONSE=$(api_call POST /api/notes "$NOTE1_DATA")
if echo "$NOTE1_RESPONSE" | jq -e '.id' > /dev/null 2>&1; then
    NOTE1_ID=$(echo "$NOTE1_RESPONSE" | jq -r '.id')
    echo "✓ (ID: $NOTE1_ID)"
else
    echo "❌"
fi

echo -n "创建测试笔记 2 ... "
NOTE2_DATA='{
  "content": "这是测试笔记 2",
  "title": "测试笔记 2",
  "contentType": "text"
}'
NOTE2_RESPONSE=$(api_call POST /api/notes "$NOTE2_DATA")
if echo "$NOTE2_RESPONSE" | jq -e '.id' > /dev/null 2>&1; then
    NOTE2_ID=$(echo "$NOTE2_RESPONSE" | jq -r '.id')
    echo "✓ (ID: $NOTE2_ID)"
else
    echo "❌"
fi

# 2.2 创建笔记关联
if [ -n "$NOTE1_ID" ] && [ -n "$NOTE2_ID" ]; then
    echo -n "POST /api/notes/$NOTE1_ID/links ... "
    LINK_DATA="{
      \"targetNoteId\": \"$NOTE2_ID\",
      \"linkType\": \"related\"
    }"
    LINK_RESPONSE=$(api_call POST "/api/notes/$NOTE1_ID/links" "$LINK_DATA")
    if echo "$LINK_RESPONSE" | jq -e '.id' > /dev/null 2>&1; then
        LINK_ID=$(echo "$LINK_RESPONSE" | jq -r '.id')
        echo "✓ (ID: $LINK_ID)"
    else
        echo "❌"
        echo "$LINK_RESPONSE"
    fi
    
    # 2.3 获取相关笔记
    echo -n "GET /api/notes/$NOTE1_ID/related ... "
    RELATED_RESPONSE=$(api_call GET "/api/notes/$NOTE1_ID/related")
    if echo "$RELATED_RESPONSE" | jq . > /dev/null 2>&1; then
        echo "✓"
        echo "$RELATED_RESPONSE" | jq -r 'if .manualLinks then "  手动链接数: \(.manualLinks | length)" else "" end'
    else
        echo "❌"
    fi
fi

echo ""
echo "3. 测试 Enhanced Notes API"
echo "-----------------------------------"

# 3.1 按笔记本过滤笔记
if [ -n "$NOTEBOOK_ID" ]; then
    echo -n "GET /api/notes?notebook=$NOTEBOOK_ID ... "
    NOTES_RESPONSE=$(api_call GET "/api/notes?notebook=$NOTEBOOK_ID")
    if echo "$NOTES_RESPONSE" | jq . > /dev/null 2>&1; then
        echo "✓"
        echo "$NOTES_RESPONSE" | jq -r 'if .notes then "  笔记数: \(.notes | length)" else "  错误: \(.error // "未知")" end'
    else
        echo "❌"
    fi
fi

echo ""
echo "==================================="
echo "测试完成"
echo "==================================="

# 清理（可选）
echo ""
echo "清理测试数据..."
if [ -n "$LINK_ID" ]; then
    echo -n "删除链接 ... "
    api_call DELETE "/api/note-links/$LINK_ID" > /dev/null && echo "✓" || echo "❌"
fi

if [ -n "$NOTE1_ID" ]; then
    echo -n "删除笔记 1 ... "
    api_call DELETE "/api/notes/$NOTE1_ID" > /dev/null && echo "✓" || echo "❌"
fi

if [ -n "$NOTE2_ID" ]; then
    echo -n "删除笔记 2 ... "
    api_call DELETE "/api/notes/$NOTE2_ID" > /dev/null && echo "✓" || echo "❌"
fi

if [ -n "$NOTEBOOK_ID" ]; then
    echo -n "删除笔记本 ... "
    api_call DELETE "/api/notebooks/$NOTEBOOK_ID" > /dev/null && echo "✓" || echo "❌"
fi

echo ""
echo "测试脚本执行完毕"
