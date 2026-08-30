#!/usr/bin/env bash
# =====================================================================
# rollback.sh — opencode-pocket 回滚脚本（rule 22 §8）
#
# 用法: ./deploy/rollback.sh [--env local|server|prod]
# 说明: 回滚到前一个部署版本（默认从 /var/lib/deploy-tracker/ 读取）
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SERVICE_NAME="opencode-pocket"
CONTAINER_NAME="kx-${SERVICE_NAME}"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/legacy-env.sh"

ENV="local"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --env) ENV="$2"; shift 2 ;;
    --help) echo "用法: $0 [--env local|server|prod]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

case "${ENV}" in
  local) DEPLOY_ENV="local" ;;
  server|prod) DEPLOY_ENV="server" ;;
  *) echo "未知环境: ${ENV}（支持 local|server|prod）" >&2; exit 1 ;;
esac

echo "━━━ rollback: ${SERVICE_NAME} (env=${ENV}) ━━━"

DEPLOY_TRACKER_DIR="${POCKET_DEPLOY_TRACKER_DIR:-/var/lib/deploy-tracker}"
CURRENT_IMAGE_FILE="${DEPLOY_TRACKER_DIR}/${SERVICE_NAME}_current_image"
PREV_IMAGE_FILE="${DEPLOY_TRACKER_DIR}/${SERVICE_NAME}_prev_image"
LOCK_DIR="${DEPLOY_TRACKER_DIR}/${SERVICE_NAME}.lock"
mkdir -p "${DEPLOY_TRACKER_DIR}"
legacy_acquire_lock "${LOCK_DIR}"
trap legacy_release_lock EXIT

if [[ ! -s "${PREV_IMAGE_FILE}" ]]; then
  echo "❌ 未找到前一个镜像记录（${PREV_IMAGE_FILE}）" >&2
  exit 1
fi
PREV_IMAGE="$(<"${PREV_IMAGE_FILE}")"
if ! legacy_validate_image_reference "${PREV_IMAGE}"; then
  echo "❌ 前一个镜像记录格式非法: ${PREV_IMAGE_FILE}" >&2
  exit 1
fi
TRACKED_CURRENT_IMAGE=""
if [[ -s "${CURRENT_IMAGE_FILE}" ]]; then
  TRACKED_CURRENT_IMAGE="$(<"${CURRENT_IMAGE_FILE}")"
  if ! legacy_validate_image_reference "${TRACKED_CURRENT_IMAGE}"; then
    echo "❌ 当前镜像记录格式非法: ${CURRENT_IMAGE_FILE}" >&2
    exit 1
  fi
fi

EXPLICIT_ENV_FILE="${POCKET_DEPLOY_ENV_FILE:-}"
if [[ -n "${EXPLICIT_ENV_FILE}" ]]; then
  if [[ ! -f "${EXPLICIT_ENV_FILE}" ]]; then
    echo "❌ 显式环境文件不存在: ${EXPLICIT_ENV_FILE}" >&2
    exit 1
  fi
  ENV_FILE="${EXPLICIT_ENV_FILE}"
else
  ENV_FILE="${SCRIPT_DIR}/.env"
  [[ -f "${ENV_FILE}" ]] || ENV_FILE="${SCRIPT_DIR}/../backend/.env"
fi
if [[ ! -f "${ENV_FILE}" ]]; then
  echo "❌ 回滚缺少环境文件: ${ENV_FILE}" >&2
  exit 1
fi

PRODUCTION_ENV=false
[[ "${DEPLOY_ENV}" == "server" ]] && PRODUCTION_ENV=true
legacy_validate_managed_env "${ENV_FILE}" "${PRODUCTION_ENV}"
PORT="$(legacy_resolve_value POCKET_HTTP_PORT "${ENV_FILE}" 8090)"
if ! legacy_validate_port "${PORT}"; then
  echo "❌ POCKET_HTTP_PORT 必须是 1-65535 的整数: ${PORT}" >&2
  exit 1
fi
DEFAULT_BIND_IP="0.0.0.0"
[[ "${DEPLOY_ENV}" == "server" ]] && DEFAULT_BIND_IP="172.16.2.210"
BIND_IP="$(legacy_resolve_value POCKET_PORT_BIND_IP "${ENV_FILE}" "${DEFAULT_BIND_IP}")"
DATA_DIR="${POCKET_DATA_DIR:-${SCRIPT_DIR}/../data}"
NETWORK="${POCKET_DOCKER_NETWORK:-kaixuan_local_net}"
mkdir -p "${DATA_DIR}"

command -v docker >/dev/null 2>&1 || { echo "❌ docker 未安装" >&2; exit 1; }

RUNNING_IMAGE=""
if RUNNING_IMAGE="$(legacy_container_image "${CONTAINER_NAME}")"; then
  :
else
  inspect_status=$?
  (( inspect_status == 1 )) || exit 1
fi
RESTORE_IMAGE="${RUNNING_IMAGE:-${TRACKED_CURRENT_IMAGE}}"

restore_original() {
  if ! legacy_remove_container "${CONTAINER_NAME}"; then
    echo "❌ 无法清理失败回滚容器，服务状态需人工确认" >&2
    return 1
  fi
  if [[ -z "${RESTORE_IMAGE}" ]]; then
    echo "❌ 无回滚前镜像可恢复" >&2
    return 1
  fi
  echo "⚠️  恢复回滚前镜像: ${RESTORE_IMAGE}" >&2
  if ! legacy_start_container "${CONTAINER_NAME}" "${RESTORE_IMAGE}" "${ENV_FILE}" \
    "${DATA_DIR}" "${BIND_IP}" "${PORT}" "${NETWORK}"; then
    echo "❌ 恢复回滚前镜像启动失败，服务需要人工介入" >&2
    return 1
  fi
  if ! POCKET_DEPLOY_ENV_FILE="${ENV_FILE}" POCKET_DATA_DIR="${DATA_DIR}" \
    POCKET_HTTP_PORT="${PORT}" POCKET_PORT_BIND_IP="${BIND_IP}" \
    "${SCRIPT_DIR}/verify.sh" --env "${ENV}" --tag restore; then
    echo "❌ 回滚前镜像已启动但恢复验证失败" >&2
    return 1
  fi
  legacy_atomic_write "${CURRENT_IMAGE_FILE}" "${RESTORE_IMAGE}"
  return 0
}

echo "   前镜像: ${PREV_IMAGE}"
echo "▶ 拉取镜像: ${PREV_IMAGE}"
docker pull "${PREV_IMAGE}"

if ! legacy_remove_container "${CONTAINER_NAME}"; then
  echo "❌ 无法清理当前容器，回滚未执行" >&2
  exit 2
fi
echo "▶ 启动前镜像: ${PREV_IMAGE}"
if ! legacy_start_container "${CONTAINER_NAME}" "${PREV_IMAGE}" "${ENV_FILE}" \
  "${DATA_DIR}" "${BIND_IP}" "${PORT}" "${NETWORK}"; then
  echo "❌ 前镜像启动失败" >&2
  if restore_original; then
    exit 1
  fi
  exit 2
fi

echo "▶ 验证回滚后状态..."
if POCKET_DEPLOY_ENV_FILE="${ENV_FILE}" POCKET_DATA_DIR="${DATA_DIR}" \
  POCKET_HTTP_PORT="${PORT}" POCKET_PORT_BIND_IP="${BIND_IP}" \
  "${SCRIPT_DIR}/verify.sh" --env "${ENV}" --tag rollback; then
  legacy_atomic_write "${CURRENT_IMAGE_FILE}" "${PREV_IMAGE}"
  if [[ -n "${RESTORE_IMAGE}" && "${RESTORE_IMAGE}" != "${PREV_IMAGE}" ]]; then
    legacy_atomic_write "${PREV_IMAGE_FILE}" "${RESTORE_IMAGE}"
  else
    rm -f "${PREV_IMAGE_FILE}"
  fi
  echo "✅ 回滚完成（镜像: ${PREV_IMAGE}）"
else
  echo "⚠️  回滚后验证失败，恢复回滚前镜像" >&2
  if restore_original; then
    exit 1
  fi
  exit 2
fi
