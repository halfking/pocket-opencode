#!/usr/bin/env bash
# =====================================================================
# deploy-local.sh — 本地部署入口（macOS dev 机 / 笔记本）
#
# 默认根目录: ~/Downloads/kaixuan/opp
# Profile:    local（启用 dev-auth、POCKET_ENV=development）
# 数据库:     252 docker 中的权威 PG，经 SSH tunnel 访问（tunnel-252.sh）
#
# 流程：
#   1. 设 DEPLOY_ENV=local，source env.sh
#   2. init-dirs.sh 创建子目录
#   3. 写一份本地 .env 模板到 config/.env.local（如不存在）
#   4. 检查 SSH tunnel 到 252 PG（不通则尝试自动建立/提示）
#   5. start.sh 启服务（自动判断 build / no-build）
#
# 用法：
#   ./deploy/bin/deploy-local.sh
#   DEPLOY_BASE_DIR=/tmp/opp ./deploy/bin/deploy-local.sh   # 临时换根
#   OPP_PG_PASSWORD=<pg密码> ./deploy/bin/deploy-local.sh    # 首次写 DSN 密码
# =====================================================================

set -euo pipefail

export DEPLOY_ENV="${DEPLOY_ENV:-local}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

echo "━━━ deploy-local ━━━"
echo "  DEPLOY_BASE_DIR = ${DEPLOY_BASE_DIR}"

# 1) 创建目录
"${SCRIPT_DIR}/init-dirs.sh"

# 2) 写本地 .env 模板（首次）
if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "  📝 生成本地 .env 模板: ${POCKET_ENV_FILE}"
  read -r DEV_JWT_SECRET _ < <(openssl rand -hex 24 2>/dev/null || echo "local-pocket-jwt-secret-012345678901234567890")
  # DSN 密码不入库：优先取 OPP_PG_PASSWORD，否则留占位符
  PG_PASS="${OPP_PG_PASSWORD:-<在此填入252的PG密码>}"
  cat > "${POCKET_ENV_FILE}" <<EOF
# opencode-pocket 本地部署 .env（deploy-local.sh 自动生成，请按需修改）
# 加 DEPLOY_ENV_DEBUG=1 重跑可看到生效路径。密码等敏感项勿提交仓库。

# ---- 端口（宿主机→容器）----
POCKET_HTTP_PORT=${POCKET_HTTP_PORT}
POCKET_FRONTEND_PORT=${POCKET_FRONTEND_PORT}

# ---- 环境标识 ----
POCKET_ENV=development

# ---- JWT（首次生成随机值；重装不换会导致旧 token 失效）----
POCKET_JWT_SECRET=${DEV_JWT_SECRET}

# ---- Dev mode：自动种子 admin/admin（生产必须 false）----
POCKET_DEV_AUTH=true
POCKET_AUTH_USER=admin
POCKET_AUTH_PASS=admin-local-dev-only

# ---- 数据库：252 docker 中的权威 PG（经 SSH tunnel）----
# tunnel 由 ./deploy/bin/tunnel-252.sh 管理（宿主 localhost:15432 → 252 内网 PG）。
# 容器内经 host.docker.internal 访问宿主 tunnel 端口。
POCKET_POSTGRES_DSN=postgresql://${OPP_PG_USER}:${PG_PASS}@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB}?sslmode=disable
POCKET_PG_SCHEMA=${OPP_PG_SCHEMA}

# ---- 日志级别 ----
POCKET_LOG_LEVEL=info

# ---- CORS（本地前端端口）----
POCKET_ALLOWED_ORIGINS=http://localhost:${POCKET_FRONTEND_PORT},http://127.0.0.1:${POCKET_FRONTEND_PORT}

# ---- OpenCode 实例（dev mock；按需改真实地址）----
POCKET_OPENCODE_INSTANCES=[{"id":"local-opencode","displayName":"Local OpenCode","baseURL":"http://host.docker.internal:4096","environment":"development","capabilities":["session","summary","pty"]}]

# ---- 集成开关（默认关）----
POCKET_EMAIL_FETCH_ENABLED=false
POCKET_FEISHU_APP_ID=
POCKET_KXMEMORY_BASE_URL=
EOF
  if [[ -z "${OPP_PG_PASSWORD:-}" ]]; then
    echo "     ⚠️  DSN 密码为占位符；请编辑 ${POCKET_ENV_FILE} 填入，或"
    echo "        OPP_PG_PASSWORD=<密码> ./deploy/bin/deploy-local.sh 重新生成"
  fi
fi

# 3) 检查到 252 PG 的 tunnel（本地数据库的唯一通道）
if grep -q '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}" && \
   ! grep -q '<在此填入252的PG密码>' "${POCKET_ENV_FILE}"; then
  if ! "${SCRIPT_DIR}/tunnel-252.sh" status >/dev/null 2>&1; then
    echo "▶ 252 PG tunnel 未就绪，尝试自动建立…"
    "${SCRIPT_DIR}/tunnel-252.sh" up || {
      echo "  ❌ tunnel 建立失败；请手工执行:" >&2
      echo "     ./deploy/bin/tunnel-252.sh up   （需 SSH key 或 SSHPASS + sshpass）" >&2
      exit 1
    }
  else
    echo "▶ 252 PG tunnel 已就绪（localhost:${OPP_PG_PORT}）"
  fi
fi

# 4) 拉起服务
exec "${SCRIPT_DIR}/start.sh" "$@"
