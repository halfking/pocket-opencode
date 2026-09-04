#!/usr/bin/env bash
# =====================================================================
# deploy-local.sh — 本地开发部署入口（macOS dev 机 / 笔记本 / Windows）
#
# 行为：
#   1. 默认 DEPLOY_BASE_DIR 走 os_detect_base_dir
#      - macOS:   ${HOME}/kaixuan/openpocket
#      - Windows: D:/kaixuan/openpocket（如 D 盘存在且可写），否则 C:/
#      - Linux:   /opt/kaixuan/openpocket（dev 用，非典型）
#   2. 默认 OPP_DEPLOY_PG=true；其它 DB 容器化默认关闭
#   3. PG 走容器化（除非本机已有 docker postgres 或 5432 被占）
#   4. 启动流程：init-dirs → ensure-databases → start
#
# 用法：
#   ./deploy-local.sh
#   OPP_PG_PASSWORD=<pwd> ./deploy-local.sh    # 首次注入 PG 密码
#   DEPLOY_BASE_DIR=/tmp/opp ./deploy-local.sh # 临时换根
#   ./deploy-local.sh --rollback               # 回滚到上一个版本
#   ./deploy-local.sh --dry-run                # 不真起容器，只跑探测
#   ./deploy-local.sh --backend-only           # 只起后端
# =====================================================================

set -euo pipefail

# 保留 entry 脚本的根目录（env.sh 会 export 它自己的 SCRIPT_DIR，覆盖这里的）
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_DIR="${ROOT_DIR}"
export DEPLOY_ENV="${DEPLOY_ENV:-local}"

# ── 本地开关：必须在 source env.sh 之前预设 ──────────────────────
# env.sh 对这些变量用 `:=` 兜底，若在 source 之后再赋值会因"已非空"而不生效
# （实测导致 OPP_DEPLOY_PG 恒为 false、PG 端口恒为 15432，DSN 不写入）。
export OPP_DEPLOY_PG="${OPP_DEPLOY_PG:-true}"
export OPP_DEPLOY_REDIS="${OPP_DEPLOY_REDIS:-false}"
export OPP_DEPLOY_MYSQL="${OPP_DEPLOY_MYSQL:-false}"
# 本机 PG 走 host.docker.internal 让容器访问宿主端口（避免绑 0.0.0.0:5432）
export OPP_PG_HOST="${OPP_PG_HOST:-host.docker.internal}"
export OPP_PG_PORT="${OPP_PG_PORT:-5432}"

# shellcheck disable=SC1091
source "${ROOT_DIR}/deploy/bin/env.sh"

# env.sh export 的 SCRIPT_DIR 是 deploy/bin/；后续脚本调用路径要还原
SCRIPT_DIR="${ROOT_DIR}"

echo "━━━ deploy-local ━━━"
echo "  OS_KIND         = ${OPP_OS_KIND}"
echo "  DEPLOY_BASE_DIR = ${DEPLOY_BASE_DIR}"
echo "  HTTP_PORT       = ${POCKET_HTTP_PORT}@${POCKET_PORT_BIND_IP}"
echo "  FRONTEND_PORT   = ${POCKET_FRONTEND_PORT}"

# ── 平台门禁 ────────────────────────────────────────────────────
case "${OPP_OS_KIND}" in
  darwin|linux|wsl)
    echo "  ✅ OS 适配: ${OPP_OS_KIND}（原生支持）"
    ;;
  windows-msys)
    echo "  ⚠️  Windows MSYS / Git-Bash 路径支持（注意 docker daemon 是 Windows 上的 Docker Desktop）"
    if ! command -v docker.exe >/dev/null 2>&1 && ! command -v docker >/dev/null 2>&1; then
      echo "  ❌ docker 未找到；请安装 Docker Desktop for Windows 并启用 WSL2 后端" >&2
      exit 1
    fi
    ;;
  *)
    echo "  ❌ 不支持的 OS: ${OPP_OS_KIND}（darwin/linux/wsl/windows-msys 之一）" >&2
    exit 1
    ;;
esac

echo "  PG 拓扑: deploy=${OPP_DEPLOY_PG} target=${OPP_PG_HOST}:${OPP_PG_PORT}"

# ── 1) 建目录 ──────────────────────────────────────────────────────
"${SCRIPT_DIR}/deploy/bin/init-dirs.sh"

# ── 2) DB 复用 vs 容器化 ─────────────────────────────────────────
"${SCRIPT_DIR}/deploy/bin/ensure-databases.sh"

# ── 3) 生成 .env.local（首次或密码缺）─────────────────────────────
DSN_PLACEHOLDER='SET_PG_PASSWORD_HERE'

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
  # JWT 密钥必须真随机；openssl 缺失时拒绝生成（不可降级为可预测字符串）
  if ! command -v openssl >/dev/null 2>&1; then
    echo "  ❌ 生成 JWT 密钥需要 openssl，当前不可用" >&2
    echo "     安装后重跑（macOS 自带；Linux: apt/yum install openssl）" >&2
    exit 1
  fi
  read -r DEV_JWT_SECRET _ < <(openssl rand -hex 24)
  cat > "${POCKET_ENV_FILE}" <<EOF
# openpocket 本地部署 .env（deploy-local.sh 自动生成，请按需修改）
# 加 POCKET_ENV_DEBUG=1 或 OPP_DEBUG=1 重跑可看到生效路径。密码等敏感项勿提交仓库。
# 端口调整用环境变量（不在此文件）：POCKET_HTTP_PORT / POCKET_FRONTEND_PORT

# ---- 环境标识 ----
POCKET_ENV=development

# ---- JWT（首次生成随机值；重装不换会导致旧 token 失效）----
POCKET_JWT_SECRET=${DEV_JWT_SECRET}

# ---- Dev mode：自动种子 admin/admin（生产必须 false）----
POCKET_DEV_AUTH=true
POCKET_AUTH_USER=admin
POCKET_AUTH_PASS=admin-local-dev-only
# 新版 auth 要求 RedClaw Admin 或显式 legacy 开关；本地 dev 走 legacy
POCKET_AUTH_LEGACY_ONLY=true

# ---- 数据库 ----
# 检测到外部 PG：${OPP_PG_MODE}（${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB}）
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
  chmod 600 "${POCKET_ENV_FILE}"
}

# 注入 PG 密码到 DSN（端口不写死，按 env.sh 派生的为准）
inject_pg_dsn() {
  local password="$1"
  local tmp
  tmp="$(mktemp -t pocket-env.XXXXXX)"
  # 任何提前退出都不留 tmp（mv 成功后会清掉 tmp 变量，trap 不再删已搬走的）
  trap '[[ -n "${tmp:-}" && -e "${tmp}" ]] && rm -f "${tmp}"' RETURN
  grep -v '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}" > "${tmp}" || true
  printf 'POCKET_POSTGRES_DSN=postgresql://%s:%s@%s:%s/%s?sslmode=disable\n' \
    "${OPP_PG_USER}" "$(urlencode "${password}")" "${OPP_PG_HOST}" "${OPP_PG_PORT}" "${OPP_PG_DB}" >> "${tmp}"
  mv "${tmp}" "${POCKET_ENV_FILE}"
  tmp=""
  chmod 600 "${POCKET_ENV_FILE}"
}

if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  PG_PASS="${OPP_PG_PASSWORD:-${DSN_PLACEHOLDER}}"
  write_env_template
  if [[ "${OPP_PG_MODE}" == "external" ]] || [[ "${OPP_PG_MODE}" == "container" ]]; then
    # detect 已经命中或已自动容器化；DSN 应该已经在 .env 里
    if ! grep -q '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}"; then
      inject_pg_dsn "${PG_PASS}"
    fi
  fi
  if [[ -z "${OPP_PG_PASSWORD:-}" ]] && grep -q "${DSN_PLACEHOLDER}" "${POCKET_ENV_FILE}" 2>/dev/null; then
    echo "     ⚠️  DSN 密码为占位符；重跑注入（自动替换占位行）:"
    echo "        OPP_PG_PASSWORD=<密码> ./deploy-local.sh"
    echo "     或手工编辑 ${POCKET_ENV_FILE}"
  fi
elif grep -q "${DSN_PLACEHOLDER}" "${POCKET_ENV_FILE}" 2>/dev/null && [[ -n "${OPP_PG_PASSWORD:-}" ]]; then
  echo "  🔁 检测到占位密码 + OPP_PG_PASSWORD，重写 DSN 行"
  inject_pg_dsn "${OPP_PG_PASSWORD}"
fi

# ── 4) 拉起服务 ───────────────────────────────────────────────────
exec "${SCRIPT_DIR}/deploy/bin/start.sh" "$@"
