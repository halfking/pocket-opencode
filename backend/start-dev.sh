#!/bin/bash
# Backend 开发模式启动脚本

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

echo "=========================================="
echo "启动 OpenCode Pocket Backend (开发模式)"
echo "=========================================="

# 设置环境变量
export POCKET_DEV_AUTH=true
# 349a14e 认证加固后：legacy 本地 JWT 路径需显式 POCKET_AUTH_LEGACY_ONLY=true，
# 否则启动即要求 POCKET_REDCLAW_ADMIN_URL。dev 脚本缺省走 legacy（可被外部覆盖）。
export POCKET_AUTH_LEGACY_ONLY="${POCKET_AUTH_LEGACY_ONLY:-true}"
export POCKET_JWT_SECRET=test-secret-key-for-phase7-validation
export POCKET_HTTP_PORT=8088
export POCKET_DB_PATH=./data/pocket.sqlite

# 停止已有实例：只杀监听 POCKET_HTTP_PORT 的进程。不能 killall pocketd——
# 姊妹仓（ai-native-tools/openpocket）有同名进程，2026-09-05 实测其 pocketd
# 常驻本机 :8090，按名全杀会误伤。
OLD_PIDS="$(lsof -nP -tiTCP:"$POCKET_HTTP_PORT" -sTCP:LISTEN 2>/dev/null || true)"
if [ -n "$OLD_PIDS" ]; then
    echo "停止监听端口 $POCKET_HTTP_PORT 的现有 backend (PID: $(echo $OLD_PIDS | tr '\n' ' '))..."
    kill $OLD_PIDS 2>/dev/null || true
    sleep 1
fi

# dev 登录冒烟凭据：脚本原先从不导出 POCKET_AUTH_PASS，登录测试一直发空密码。
# 未显式设置时回退到后端 dev 缺省（admin / Veritrans&9527）。
export POCKET_AUTH_USER="${POCKET_AUTH_USER:-admin}"
export POCKET_AUTH_PASS="${POCKET_AUTH_PASS:-Veritrans&9527}"

# AI 网关配置：从仓库根 .env 读取（不回显密钥），保证 /api/llm/* 开箱可用。
ROOT_ENV="$(cd "$SCRIPT_DIR/.." && pwd)/.env"
read_env_key() {
  python3 - "$ROOT_ENV" "$1" <<'PY'
import sys, re, pathlib
p, key = pathlib.Path(sys.argv[1]), sys.argv[2]
for line in p.read_text().splitlines():
    s = line.strip()
    if not s or s.startswith('#'):
        continue
    m = re.match(r'^([A-Z0-9_]+)\s*=\s*(.*)$', s)
    if m and m.group(1) == key:
        print(m.group(2).strip().strip('"\''), end='')
        break
PY
}
if [ -f "$ROOT_ENV" ]; then
  GW_URL="$(read_env_key POCKET_LLM_GATEWAY_URL)"
  GW_KEY="$(read_env_key POCKET_LLM_GATEWAY_API_KEY)"
  export POCKET_LLM_GATEWAY_URL="${POCKET_LLM_GATEWAY_URL:-$GW_URL}"
  export POCKET_LLM_GATEWAY_API_KEY="${POCKET_LLM_GATEWAY_API_KEY:-$GW_KEY}"
  echo "AI 网关: ${POCKET_LLM_GATEWAY_URL:-<未配置>} (key: ${POCKET_LLM_GATEWAY_API_KEY:+已注入})"
fi

# ✨ 新增：配置本地 OpenCode 实例
export POCKET_OPENCODE_INSTANCES='[
  {
    "id": "local-opencode",
    "displayName": "本地 OpenCode 实例",
    "apiBaseURL": "http://localhost:4096",
    "workspaceId": "default",
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

# 验证启动（按端口找 PID：本机可能并存其他仓库的同名 pocketd 进程；
# 轮询 healthz——初始化实测可耗 8s+（16:18 现场），固定 sleep 2 会误报失败）
PID=""
for _ in $(seq 1 30); do
    PID="$(lsof -nP -tiTCP:"$POCKET_HTTP_PORT" -sTCP:LISTEN 2>/dev/null | head -1)"
    if [ -n "$PID" ] && curl -sf "http://localhost:$POCKET_HTTP_PORT/healthz" > /dev/null 2>&1; then
        break
    fi
    PID=""
    sleep 1
done
if [ -n "$PID" ]; then
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
        -d "{\"username\":\"${POCKET_AUTH_USER:-admin}\",\"password\":\"${POCKET_AUTH_PASS}\"}")
    
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
    echo "停止服务: lsof -tiTCP:$POCKET_HTTP_PORT -sTCP:LISTEN | xargs kill"
    echo ""
else
    echo "❌ Backend 启动失败"
    echo "查看日志: cat ../logs/backend-dev.log"
    exit 1
fi
