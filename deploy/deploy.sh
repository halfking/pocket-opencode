#!/usr/bin/env bash
# =====================================================================
# deploy.sh — opencode-pocket 部署脚本（rule 22 §6.3）
#
# 用法: ./deploy/deploy.sh [--env local|server|prod] [--tag <tag>] [--dry-run]
#       prod 是 server 的 legacy 兼容别名。
# 自动验证: 部署后自动调用 verify.sh，失败触发 rollback
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="opencode-pocket"
CONTAINER_NAME="kx-${SERVICE_NAME}"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/legacy-env.sh"

# ── 默认值 ─────────────────────────────────────────────────────────
ENV="local"
TAG="latest"
DRY_RUN=false

# ── 解析参数 ───────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --help) echo "用法: $0 [--env local|server|prod] [--tag <tag>] [--dry-run]（prod=server 兼容别名）"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

case "${ENV}" in
  local|server|prod) ;;
  *) echo "未知环境: ${ENV}（支持 local|server|prod）" >&2; exit 1 ;;
esac

echo "=== deploy: ${SERVICE_NAME} (env=${ENV}, tag=${TAG}) ==="

# ── 1. 前置配置检查 ─────────────────────────────────────────────────
DEPLOY_DIR="${SCRIPT_DIR}"
EXPLICIT_ENV_FILE="${POCKET_DEPLOY_ENV_FILE:-}"
if [[ -n "${EXPLICIT_ENV_FILE}" ]]; then
  if [[ ! -f "${EXPLICIT_ENV_FILE}" ]]; then
    echo "❌ 显式环境文件不存在: ${EXPLICIT_ENV_FILE}" >&2
    exit 1
  fi
  ENV_FILE="${EXPLICIT_ENV_FILE}"
else
  ENV_FILE="${DEPLOY_DIR}/.env"
  if [[ ! -f "${ENV_FILE}" ]]; then
    ENV_FILE="${SCRIPT_DIR}/../backend/.env"
  fi
fi

PRODUCTION_ENV=false
[[ "${ENV}" == "prod" || "${ENV}" == "server" ]] && PRODUCTION_ENV=true
legacy_validate_managed_env "${ENV_FILE}" "${PRODUCTION_ENV}"

read_env_value() {
  legacy_env_value "${ENV_FILE}" "$1"
}

# ── 2. dry-run ──────────────────────────────────────────────────────
if [[ "$DRY_RUN" == true ]]; then
  echo "[DRY-RUN] 以下命令将被执行:"
  echo "  docker pull registry.kxpms.cn/kaixuan-platform-${SERVICE_NAME}:${TAG}"
  echo "  docker stop ${CONTAINER_NAME} 2>/dev/null || true"
  echo "  docker rm ${CONTAINER_NAME} 2>/dev/null || true"
  echo "  docker run -d --name ${CONTAINER_NAME} ..."
  echo "[DRY-RUN] ✅ 完成（未实际执行）"
  exit 0
fi

# 解析完整运行上下文；任何配置错误都必须在 Docker 操作前失败。
PORT="$(legacy_resolve_value POCKET_HTTP_PORT "${ENV_FILE}" 8090)"
if ! legacy_validate_port "${PORT}"; then
  echo "❌ POCKET_HTTP_PORT 必须是 1-65535 的整数: ${PORT}" >&2
  exit 1
fi
DEFAULT_BIND_IP="0.0.0.0"
[[ "${ENV}" == "server" || "${ENV}" == "prod" ]] && DEFAULT_BIND_IP="172.16.2.210"
BIND_IP="$(legacy_resolve_value POCKET_PORT_BIND_IP "${ENV_FILE}" "${DEFAULT_BIND_IP}")"
DATA_DIR="${POCKET_DATA_DIR:-${SCRIPT_DIR}/../data}"
NETWORK="${POCKET_DOCKER_NETWORK:-kaixuan_local_net}"
mkdir -p "${DATA_DIR}"

# ── 1. 前置检查与事务锁 ──────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || { echo "❌ docker 未安装"; exit 1; }
DEPLOY_TRACKER_DIR="${POCKET_DEPLOY_TRACKER_DIR:-/var/lib/deploy-tracker}"
CURRENT_IMAGE_FILE="${DEPLOY_TRACKER_DIR}/${SERVICE_NAME}_current_image"
PREV_IMAGE_FILE="${DEPLOY_TRACKER_DIR}/${SERVICE_NAME}_prev_image"
LOCK_DIR="${DEPLOY_TRACKER_DIR}/${SERVICE_NAME}.lock"
mkdir -p "${DEPLOY_TRACKER_DIR}"
legacy_acquire_lock "${LOCK_DIR}"
trap legacy_release_lock EXIT

# ── 2. 拉取镜像 ────────────────────────────────────────────────────
IMAGE="registry.kxpms.cn/kaixuan-platform-${SERVICE_NAME}:${TAG}"
echo "▶ 拉取镜像: ${IMAGE}"
docker pull "${IMAGE}"

# ── 3. 保存当前版本信息（用于回滚） ────────────────────────────────
CURRENT_IMAGE=""
if CURRENT_IMAGE="$(legacy_container_image "${CONTAINER_NAME}")"; then
  legacy_atomic_write "${PREV_IMAGE_FILE}" "${CURRENT_IMAGE}"
else
  inspect_status=$?
  if (( inspect_status == 2 )); then
    exit 1
  fi
  rm -f "${PREV_IMAGE_FILE}"
fi

# ── 4. 停止旧容器 ──────────────────────────────────────────────────
echo "▶ 清理旧容器: ${CONTAINER_NAME}"
if ! legacy_remove_container "${CONTAINER_NAME}"; then
  echo "❌ 旧容器清理失败，停止部署" >&2
  exit 1
fi

restore_previous_image() {
  if [[ -z "${CURRENT_IMAGE}" ]]; then
    echo "❌ 无前版本可自动恢复，请人工介入" >&2
    return 2
  fi
  if ! legacy_remove_container "${CONTAINER_NAME}"; then
    echo "❌ 无法清理失败候选容器，服务状态需人工确认" >&2
    return 2
  fi
  echo "⚠️  恢复部署前镜像: ${CURRENT_IMAGE}" >&2
  if ! legacy_start_container "${CONTAINER_NAME}" "${CURRENT_IMAGE}" "${ENV_FILE}" \
    "${DATA_DIR}" "${BIND_IP}" "${PORT}" "${NETWORK}"; then
    echo "❌ 部署前镜像恢复启动失败，服务可能停机" >&2
    return 2
  fi
  if ! POCKET_DEPLOY_ENV_FILE="${ENV_FILE}" POCKET_DATA_DIR="${DATA_DIR}" \
    POCKET_HTTP_PORT="${PORT}" POCKET_PORT_BIND_IP="${BIND_IP}" \
    "${SCRIPT_DIR}/verify.sh" --env "${ENV}" --tag restore; then
    echo "❌ 部署前镜像已启动但恢复验证失败" >&2
    return 2
  fi
  legacy_atomic_write "${CURRENT_IMAGE_FILE}" "${CURRENT_IMAGE}"
  rm -f "${PREV_IMAGE_FILE}"
  echo "✅ 已恢复部署前镜像" >&2
}

# ── 5. 启动新容器 ──────────────────────────────────────────────────
IMAGE="registry.kxpms.cn/kaixuan-platform-${SERVICE_NAME}:${TAG}"
echo "▶ 启动新容器: ${CONTAINER_NAME}"
if ! legacy_start_container "${CONTAINER_NAME}" "${IMAGE}" "${ENV_FILE}" \
  "${DATA_DIR}" "${BIND_IP}" "${PORT}" "${NETWORK}"; then
  echo "❌ 新容器启动失败"
  if restore_previous_image; then
    exit 1
  fi
  exit 2
fi

echo "✅ 部署完成"

# ── 6. 自动验证 ────────────────────────────────────────────────────
echo "▶ 运行验证..."
if POCKET_DEPLOY_ENV_FILE="${ENV_FILE}" POCKET_DATA_DIR="${DATA_DIR}" \
  POCKET_HTTP_PORT="${PORT}" POCKET_PORT_BIND_IP="${BIND_IP}" \
  "${SCRIPT_DIR}/verify.sh" --env "${ENV}" --tag "${TAG}"; then
  legacy_atomic_write "${CURRENT_IMAGE_FILE}" "${IMAGE}"
  echo "✅ 验证通过"
else
  echo "⚠️  验证失败，触发回滚..."
  if restore_previous_image; then
    exit 1
  fi
  exit 2
fi
