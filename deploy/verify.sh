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

# 调用方可显式传入 legacy env 文件；在 source env.sh 前映射到统一变量名。
EXPLICIT_ENV_FILE="${POCKET_DEPLOY_ENV_FILE:-${POCKET_ENV_FILE:-}}"
HTTP_PORT_WAS_EXPLICIT=false
[[ -n "${POCKET_HTTP_PORT:-}" ]] && HTTP_PORT_WAS_EXPLICIT=true
if [[ -n "${EXPLICIT_ENV_FILE}" ]]; then
  export POCKET_ENV_FILE="${EXPLICIT_ENV_FILE}"
fi

# shellcheck disable=SC1091
source "${LEGACY_SCRIPT_DIR}/bin/env.sh"
ENV_FILE="${POCKET_ENV_FILE}"

if [[ "${DEPLOY_ENV}" == "server" && ! -f "${ENV_FILE}" ]]; then
  echo "❌ 生产验证缺少环境文件: ${ENV_FILE}" >&2
  exit 1
fi

read_env_value() {
  local key="$1"
  awk -F= -v key="${key}" '$1 == key {sub(/^[^=]*=/, ""); gsub(/\r/, ""); gsub(/^"|"$/, ""); print; exit}' "${ENV_FILE}"
}

# legacy deploy.sh 把宿主端口写在 env 文件里；显式 shell 变量优先，
# 否则兼容读取文件，最后沿用 env.sh 的统一默认值。
if [[ "${HTTP_PORT_WAS_EXPLICIT}" != true && -f "${ENV_FILE}" ]]; then
  ENV_HTTP_PORT="$(read_env_value POCKET_HTTP_PORT)"
  [[ -z "${ENV_HTTP_PORT}" ]] || POCKET_HTTP_PORT="${ENV_HTTP_PORT}"
fi
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
    if [[ -z "${JWT_SECRET_VALUE}" || "${JWT_SECRET_VALUE}" == "pocket-dev-insecure-secret" ]]; then
      echo "  ❌ 生产环境 JWT 密钥缺失或仍为默认值"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ JWT 密钥已自定义"
      PASS=$((PASS + 1))
    fi

    if [[ "$(read_env_value POCKET_DEV_AUTH)" == "true" ]]; then
      echo "  ❌ 生产环境启用了开发认证"
      FAIL=$((FAIL + 1))
    else
      echo "  ✅ 开发认证已禁用"
      PASS=$((PASS + 1))
    fi
  fi
else
  echo "  ⚠️  未找到环境文件，跳过环境变量检查: ${ENV_FILE}"
fi

# ── 5. 数据目录权限检查 ────────────────────────────────────────────
check "数据目录可写" test -w "${POCKET_DATA_DIR}"

# ── 6. 容器日志检查（无严重错误） ──────────────────────────────────
if docker logs "${CONTAINER_NAME}" --tail 50 2>&1 | grep -iE "(panic|fatal|error.*database)" >/dev/null 2>&1; then
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
