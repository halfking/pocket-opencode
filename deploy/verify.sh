#!/usr/bin/env bash
# =====================================================================
# verify.sh — opencode-pocket 部署后验证（rule 22 §7）
#
# 用法: ./deploy/verify.sh [--env local|server|prod] [--tag <tag>]
#       prod 是 legacy deploy.sh 使用的 server 兼容别名。
# 返回: 0 = 通过, 1 = 失败
# =====================================================================

set -euo pipefail

LEGACY_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="opencode-pocket"
CONTAINER_NAME="kx-${SERVICE_NAME}"

# ── 解析参数 ───────────────────────────────────────────────────────
ENV="local"
TAG="latest"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --help) echo "用法: $0 [--env local|server|prod] [--tag <tag>]（prod=server 兼容别名）"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

case "${ENV}" in
  local) DEPLOY_ENV="local" ;;
  server) DEPLOY_ENV="server" ;;
  prod) DEPLOY_ENV="server" ;;
  *) echo "未知环境: ${ENV}（支持 local|server|prod）" >&2; exit 1 ;;
esac
export DEPLOY_ENV

# 调用方可显式传入 legacy env 文件；在 source env.sh 前保存覆盖值。
EXPLICIT_ENV_FILE="${POCKET_DEPLOY_ENV_FILE:-${POCKET_ENV_FILE:-}}"
EXPLICIT_HTTP_PORT="${POCKET_HTTP_PORT:-}"
EXPLICIT_BIND_IP="${POCKET_PORT_BIND_IP:-}"
if [[ -n "${EXPLICIT_ENV_FILE}" ]]; then
  export POCKET_ENV_FILE="${EXPLICIT_ENV_FILE}"
fi

# shellcheck disable=SC1091
source "${LEGACY_SCRIPT_DIR}/legacy-env.sh"
# shellcheck disable=SC1091
source "${LEGACY_SCRIPT_DIR}/bin/env.sh"
ENV_FILE="${POCKET_ENV_FILE}"
DEFAULT_HTTP_PORT="${POCKET_HTTP_PORT}"
DEFAULT_BIND_IP="${POCKET_PORT_BIND_IP}"

if ! command -v curl >/dev/null 2>&1 && ! command -v wget >/dev/null 2>&1; then
  echo "❌ 验证需要 curl 或 wget" >&2
  exit 1
fi

if [[ "${DEPLOY_ENV}" == "server" && ! -f "${ENV_FILE}" ]]; then
  echo "❌ 生产验证缺少环境文件: ${ENV_FILE}" >&2
  exit 1
fi

legacy_env_require_canonical_unique "${ENV_FILE}" \
  POCKET_ENV POCKET_DEV_AUTH POCKET_HTTP_PORT POCKET_PORT_BIND_IP \
  POCKET_JWT_SECRET POCKET_POSTGRES_DSN POCKET_ALLOWED_ORIGINS \
  POCKET_MCP_INSECURE_TLS POCKET_MCP_BASE_URL POCKET_MCP_TENANT_ID \
  POCKET_LLM_BASE_URL POCKET_LLM_API_KEY

read_env_value() {
  legacy_env_value "${ENV_FILE}" "$1"
}

ENV_HTTP_PORT="$(read_env_value POCKET_HTTP_PORT)"
ENV_BIND_IP="$(read_env_value POCKET_PORT_BIND_IP)"
POCKET_HTTP_PORT="${EXPLICIT_HTTP_PORT:-${ENV_HTTP_PORT:-${DEFAULT_HTTP_PORT}}}"
POCKET_PORT_BIND_IP="${EXPLICIT_BIND_IP:-${ENV_BIND_IP:-${DEFAULT_BIND_IP}}}"
if ! legacy_validate_port "${POCKET_HTTP_PORT}"; then
  echo "❌ POCKET_HTTP_PORT 必须是 1-65535 的整数: ${POCKET_HTTP_PORT}" >&2
  exit 1
fi
POCKET_HTTP_PROBE_HOST="$(legacy_probe_host "${POCKET_PORT_BIND_IP}")"
BASE_URL="http://${POCKET_HTTP_PROBE_HOST}:${POCKET_HTTP_PORT}"

PASS=0
FAIL=0

check() {
  local desc="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    echo "  ✅ ${desc}"
    PASS=$((PASS + 1))
  else
    echo "  ❌ ${desc}"
    FAIL=$((FAIL + 1))
  fi
}

container_running() {
  docker ps --filter "name=${CONTAINER_NAME}" --format "{{.Status}}" | grep -q "Up"
}

instances_ok() {
  if command -v curl >/dev/null 2>&1; then
    curl -sf "${BASE_URL}/api/instances" | grep -q '\['
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O - "${BASE_URL}/api/instances" | grep -q '\['
  else
    echo "无 curl/wget，无法验证实例列表" >&2
    return 1
  fi
}

echo "━━━ verify: ${SERVICE_NAME} (env=${ENV}, tag=${TAG}) ━━━"

# ── 1. 容器存活检查 ────────────────────────────────────────────────
check "容器运行状态" container_running

# 等待服务启动（最多 30 秒）
echo "▶ 等待服务启动..."
for _ in {1..30}; do
  if http_ok "${BASE_URL}/healthz"; then
    break
  fi
  sleep 1
done

# ── 2. 健康检查端点 ────────────────────────────────────────────────
check "健康检查 /healthz" http_ok "${BASE_URL}/healthz"

# ── 3. 实例列表 API ────────────────────────────────────────────────
check "实例列表 /api/instances" instances_ok

# ── 4. 关键环境变量检查 ────────────────────────────────────────────
if [[ -f "${ENV_FILE}" ]]; then
  check "JWT_SECRET 已配置" test -n "$(read_env_value POCKET_JWT_SECRET)"

  if [[ "${DEPLOY_ENV}" == "server" ]]; then
    POCKET_ENV_VALUE="$(read_env_value POCKET_ENV)"
    if [[ "${POCKET_ENV_VALUE}" != "production" && "${POCKET_ENV_VALUE}" != "prod" ]]; then
      echo "  ❌ 生产环境必须设置 POCKET_ENV=production"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ 生产环境标识正确"
      PASS=$((PASS + 1))
    fi

    JWT_SECRET_VALUE="$(read_env_value POCKET_JWT_SECRET)"
    if (( ${#JWT_SECRET_VALUE} < 32 )) || [[ "${JWT_SECRET_VALUE}" == "pocket-dev-insecure-secret" ]]; then
      echo "  ❌ 生产环境 JWT 密钥必须至少 32 字节且不能使用默认值"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ JWT 密钥强度符合要求"
      PASS=$((PASS + 1))
    fi

    if [[ "$(read_env_value POCKET_DEV_AUTH)" == "true" ]]; then
      echo "  ❌ 生产环境启用了开发认证"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ 开发认证已禁用"
      PASS=$((PASS + 1))
    fi

    if [[ "$(read_env_value POCKET_MCP_INSECURE_TLS)" == "true" ]]; then
      echo "  ❌ 生产环境启用了 MCP insecure TLS"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ MCP TLS 校验未禁用"
      PASS=$((PASS + 1))
    fi

    if [[ -n "$(read_env_value POCKET_LLM_BASE_URL)" || -n "$(read_env_value POCKET_LLM_API_KEY)" ]]; then
      echo "  ❌ 生产环境禁止直连 LLM provider"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ 未配置直连 LLM provider"
      PASS=$((PASS + 1))
    fi

    check "PostgreSQL DSN 已配置" test -n "$(read_env_value POCKET_POSTGRES_DSN)"
    check "Allowed origins 已配置" test -n "$(read_env_value POCKET_ALLOWED_ORIGINS)"

    MCP_BASE_URL_VALUE="$(read_env_value POCKET_MCP_BASE_URL)"
    if [[ -n "${MCP_BASE_URL_VALUE}" && -z "$(read_env_value POCKET_MCP_TENANT_ID)" ]]; then
      echo "  ❌ 配置 MCP 时必须设置 POCKET_MCP_TENANT_ID"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ MCP tenant 配置符合要求"
      PASS=$((PASS + 1))
    fi
  fi
else
  echo "  ⚠️  未找到环境文件，跳过环境变量检查: ${ENV_FILE}"
fi

# ── 5. 数据目录权限检查 ────────────────────────────────────────────
check "数据目录可写" test -w "${POCKET_DATA_DIR}"

# ── 6. 容器日志检查（无严重错误） ──────────────────────────────────
if ! CONTAINER_LOGS="$(docker logs "${CONTAINER_NAME}" --tail 50 2>&1)"; then
  echo "  ❌ 无法读取容器日志"
  FAIL=$((FAIL + 1))
elif grep -iE "(panic|fatal|error.*database)" <<<"${CONTAINER_LOGS}" >/dev/null 2>&1; then
  echo "  ⚠️  容器日志中发现错误信息"
  FAIL=$((FAIL + 1))
else
  echo "  ✅ 容器日志正常"
  PASS=$((PASS + 1))
fi

echo "━━━ 验证结果 ━━━"
echo "  通过: $PASS, 失败: $FAIL"

if [[ ${FAIL} -gt 0 ]]; then
  echo "❌ 验证未通过，请检查日志或执行 rollback"
  exit 1
fi
echo "✅ 验证通过"
