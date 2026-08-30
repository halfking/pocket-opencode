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
# 占位符必须纯 ASCII 安全字符：写入 DSN 会被 urlencode，占位检测靠原文 grep
DSN_PLACEHOLDER='SET_PG_PASSWORD_HERE'
# PG 密码写入 DSN 前做 percent-encode：含 @ : / ? # $ & 等字符时
# 裸拼会破坏 URL 解析（实测 backend 报 invalid port 后退出）。
urlencode() {
  local s="$1" out="" c i hex
  for ((i = 0; i < ${#s}; i++)); do
    c="${s:i:1}"
    case "${c}" in
      [a-zA-Z0-9.~_-]) out+="${c}" ;;
      *) printf -v hex '%%%02X' "'${c}"; out+="${hex}" ;;
    esac
  done
  printf '%s' "${out}"
}
write_env_template() {
  echo "  📝 生成本地 .env 模板: ${POCKET_ENV_FILE}"
  read -r DEV_JWT_SECRET _ < <(openssl rand -hex 24 2>/dev/null || echo "local-pocket-jwt-secret-012345678901234567890")
  # 注意：heredoc 不含 DSN 行——密码可能带 $ 等字符，单独用 printf 追加避免被 shell 展开。
  # 端口不写入模板：compose 插值以 shell 环境为准（env.sh），写死会成为改不了的死配置；
  # 改端口请用环境变量：POCKET_HTTP_PORT=9090 ./deploy/bin/deploy-local.sh
  cat > "${POCKET_ENV_FILE}" <<EOF
# opencode-pocket 本地部署 .env（deploy-local.sh 自动生成，请按需修改）
# 加 DEPLOY_ENV_DEBUG=1 重跑可看到生效路径。密码等敏感项勿提交仓库。
# 端口调整用环境变量（不在此文件）：POCKET_HTTP_PORT / POCKET_FRONTEND_PORT

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
# DSN 行由脚本追加在文件末尾（密码含特殊字符时需自行 percent-encode）。
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
  # DSN 行单独追加：密码经 percent-encode 后用 printf 原样写入
  printf 'POCKET_POSTGRES_DSN=postgresql://%s:%s@%s:%s/%s?sslmode=disable\n' \
    "${OPP_PG_USER}" "$(urlencode "${PG_PASS}")" "${OPP_PG_HOST}" "${OPP_PG_PORT}" "${OPP_PG_DB}" >> "${POCKET_ENV_FILE}"
  chmod 600 "${POCKET_ENV_FILE}"
}

if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  PG_PASS="${OPP_PG_PASSWORD:-${DSN_PLACEHOLDER}}"
  write_env_template
  if [[ -z "${OPP_PG_PASSWORD:-}" ]]; then
    echo "     ⚠️  DSN 密码为占位符；重跑注入（自动替换占位行）:"
    echo "        OPP_PG_PASSWORD=<密码> ./deploy/bin/deploy-local.sh"
    echo "     或手工编辑 ${POCKET_ENV_FILE}"
  fi
elif grep -q "${DSN_PLACEHOLDER}" "${POCKET_ENV_FILE}" 2>/dev/null && [[ -n "${OPP_PG_PASSWORD:-}" ]]; then
  # P1-2：文件已存在但密码仍是占位符，且本次提供了密码 → 替换 DSN 行
  echo "  🔁 检测到占位密码 + OPP_PG_PASSWORD，重写 DSN 行"
  TMP_ENV="$(mktemp)"
  grep -v '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}" > "${TMP_ENV}"
  printf 'POCKET_POSTGRES_DSN=postgresql://%s:%s@%s:%s/%s?sslmode=disable\n' \
    "${OPP_PG_USER}" "$(urlencode "${OPP_PG_PASSWORD}")" "${OPP_PG_HOST}" "${OPP_PG_PORT}" "${OPP_PG_DB}" >> "${TMP_ENV}"
  mv "${TMP_ENV}" "${POCKET_ENV_FILE}"
  chmod 600 "${POCKET_ENV_FILE}"
fi

# 3) 检查到 252 PG 的 tunnel（本地数据库的唯一通道）
if grep -q '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}" && \
   ! grep -q "${DSN_PLACEHOLDER}" "${POCKET_ENV_FILE}"; then
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
else
  # DSN 缺失或仍是占位密码：起容器也必然连不上 PG 崩溃循环，直接拦截
  echo "❌ POCKET_POSTGRES_DSN 未配置或密码仍是占位符，拒绝启动" >&2
  echo "   注入密码后重跑: OPP_PG_PASSWORD=<密码> ./deploy/bin/deploy-local.sh" >&2
  echo "   （会自动替换 ${POCKET_ENV_FILE} 中的占位 DSN 行）" >&2
  exit 1
fi

# 4) 拉起服务
exec "${SCRIPT_DIR}/start.sh" "$@"
