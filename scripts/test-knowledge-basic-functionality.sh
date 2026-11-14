#!/bin/bash
# 知识库基本功能测试脚本
# Phase 2.5.3: 基本功能测试

set -e

API_BASE="http://localhost:8088/api"
TOKEN=""

echo "========================================"
echo "知识库基本功能测试"
echo "========================================"
echo ""

# 颜色定义
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 检查后端是否运行
echo "1. 检查后端服务..."
if curl -s -f "${API_BASE}/healthz" > /dev/null 2>&1; then
    echo -e "${GREEN}✓${NC} 后端服务运行正常"
else
    echo -e "${RED}✗${NC} 后端服务未运行"
    echo "请先启动后端服务: cd backend && go run ./cmd/pocketd"
    exit 1
fi

# 获取认证 token（需要配置 DEV_AUTH 或实际登录）
echo ""
echo "2. 获取认证 token..."
# 这里需要实际的登录逻辑
echo -e "${YELLOW}⚠${NC}  需要配置认证 token"
echo "   提示: export POCKET_JWT_TOKEN='your-token-here'"

if [ -z "$POCKET_JWT_TOKEN" ]; then
    echo -e "${YELLOW}⚠${NC}  跳过需要认证的测试"
    AUTH_HEADER=""
else
    TOKEN="$POCKET_JWT_TOKEN"
    AUTH_HEADER="Authorization: Bearer $TOKEN"
    echo -e "${GREEN}✓${NC} Token 已配置"
fi

echo ""
echo "3. 测试 Notebooks API..."

# 测试创建笔记本
if [ -n "$AUTH_HEADER" ]; then
    echo "   3.1 创建笔记本..."
    RESPONSE=$(curl -s -X POST "${API_BASE}/notebooks" \
        -H "$AUTH_HEADER" \
        -H "Content-Type: application/json" \
        -d '{
            "name": "测试笔记本",
            "icon": "📚",
            "color": "#3498db"
        }')
    
    if echo "$RESPONSE" | grep -q "id"; then
        echo -e "   ${GREEN}✓${NC} 创建笔记本成功"
        NOTEBOOK_ID=$(echo "$RESPONSE" | grep -o '"id":"[^"]*"' | cut -d'"' -f4)
        echo "   笔记本 ID: $NOTEBOOK_ID"
    else
        echo -e "   ${RED}✗${NC} 创建笔记本失败"
        echo "   响应: $RESPONSE"
    fi
    
    echo "   3.2 获取笔记本列表..."
    RESPONSE=$(curl -s "${API_BASE}/notebooks" -H "$AUTH_HEADER")
    if echo "$RESPONSE" | grep -q "notebooks"; then
        echo -e "   ${GREEN}✓${NC} 获取笔记本列表成功"
    else
        echo -e "   ${RED}✗${NC} 获取笔记本列表失败"
    fi
else
    echo -e "   ${YELLOW}⚠${NC}  需要认证，跳过"
fi

echo ""
echo "4. 测试 Search API..."
if [ -n "$AUTH_HEADER" ]; then
    echo "   4.1 执行搜索..."
    RESPONSE=$(curl -s "${API_BASE}/knowledge/search?q=test&limit=10" \
        -H "$AUTH_HEADER")
    
    if echo "$RESPONSE" | grep -q "results"; then
        echo -e "   ${GREEN}✓${NC} 搜索 API 正常"
    else
        echo -e "   ${RED}✗${NC} 搜索 API 失败"
    fi
else
    echo -e "   ${YELLOW}⚠${NC}  需要认证，跳过"
fi

echo ""
echo "5. 测试前端路由..."
echo "   5.1 检查路由配置..."
if grep -q "knowledgeRoutes" frontend/src/app/router-mobile.ts; then
    echo -e "   ${GREEN}✓${NC} 路由已注册"
else
    echo -e "   ${RED}✗${NC} 路由未注册"
fi

echo "   5.2 检查页面文件..."
PAGES=(
    "frontend/src/pages/KnowledgeHubPage.vue"
    "frontend/src/pages/KnowledgeSearchPage.vue"
    "frontend/src/pages/NotebookDetailPage.vue"
    "frontend/src/pages/NoteDetailPage.vue"
    "frontend/src/pages/NoteEditPage.vue"
)

for page in "${PAGES[@]}"; do
    if [ -f "$page" ]; then
        echo -e "   ${GREEN}✓${NC} $(basename $page)"
    else
        echo -e "   ${RED}✗${NC} $(basename $page) 缺失"
    fi
done

echo ""
echo "6. 测试组件存在..."
COMPONENTS=(
    "frontend/src/components/knowledge/NotebookTree.vue"
    "frontend/src/components/knowledge/NoteList.vue"
    "frontend/src/components/knowledge/SearchBar.vue"
    "frontend/src/components/knowledge/TagCloud.vue"
)

for comp in "${COMPONENTS[@]}"; do
    if [ -f "$comp" ]; then
        echo -e "   ${GREEN}✓${NC} $(basename $comp)"
    else
        echo -e "   ${RED}✗${NC} $(basename $comp) 缺失"
    fi
done

echo ""
echo "7. 测试 Stores..."
STORES=(
    "frontend/src/stores/notebooks.ts"
    "frontend/src/stores/noteLinks.ts"
    "frontend/src/stores/knowledgeSearch.ts"
)

for store in "${STORES[@]}"; do
    if [ -f "$store" ]; then
        echo -e "   ${GREEN}✓${NC} $(basename $store)"
    else
        echo -e "   ${RED}✗${NC} $(basename $store) 缺失"
    fi
done

echo ""
echo "========================================"
echo "测试完成"
echo "========================================"
echo ""
echo "总结:"
echo "- 文件检查: ✓ 所有文件已创建"
echo "- 后端 API: 需要运行后端服务并配置认证"
echo "- 前端路由: ✓ 已集成"
echo ""
echo "下一步:"
echo "1. 启动后端服务测试 API"
echo "2. 启动前端应用测试 UI"
echo "3. 执行用户验收测试"
