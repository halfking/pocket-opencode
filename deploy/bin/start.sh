#!/usr/bin/env bash
# =====================================================================
# start.sh — 启动 pocketd + frontend（deploy/bin/docker-compose.opp.yml）
#
# 镜像策略（offline-first，与仓库既有约定一致）：
#   - 本地已有 opencode-pocket:${OPP_IMAGE_TAG} → 直接 up（--no-build）
#   - 没有镜像但 kx-base 离线镜像已加载 → 现场构建
#   - 都没有 → 报错并提示 load-images.sh / images/ 目录
#
# 用法：
#   ./deploy/bin/start.sh              # 自动判断 build / no-build
#   ./deploy/bin/start.sh --build      # 强制重新构建
#   ./deploy/bin/start.sh --no-build   # 强制只用已存在镜像
#   ./deploy/bin/start.sh --backend-only  # 只起 pocketd（不起 frontend）
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

BUILD_MODE="auto"
BACKEND_ONLY=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --build) BUILD_MODE="build"; shift ;;
    --no-build) BUILD_MODE="no-build"; shift ;;
    --backend-only) BACKEND_ONLY=true; shift ;;
    --help) echo "用法: $0 [--build|--no-build] [--backend-only]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

command -v docker >/dev/null 2>&1 || { echo "❌ docker 未安装"; exit 1; }
[[ -f "${POCKET_ENV_FILE}" ]] || {
  echo "❌ env file 不存在: ${POCKET_ENV_FILE}" >&2
  echo "   本地: ./deploy/bin/deploy-local.sh 会自动生成" >&2
  echo "   252 : 手工填入 ${POCKET_CONFIG_DIR}/.env.server" >&2
  exit 1
}
[[ -f "${POCKET_COMPOSE_FILE}" ]] || { echo "❌ compose 缺失: ${POCKET_COMPOSE_FILE}"; exit 1; }

DOCKER_COMPOSE=(docker compose
  -p "${POCKET_PROJECT_NAME}"
  --env-file "${POCKET_ENV_FILE}"
  -f "${POCKET_COMPOSE_FILE}"
)

# ── 镜像策略判定 ──────────────────────────────────────────────────
BACKEND_IMAGE="opencode-pocket:${OPP_IMAGE_TAG}"
FRONTEND_IMAGE="opencode-pocket-frontend:${OPP_IMAGE_TAG}"
KX_BASE_TAG_EFFECTIVE="${KX_BASE_TAG:-kx-base:go-vue-optimized}"

backend_image_exists() { docker image inspect "${BACKEND_IMAGE}" >/dev/null 2>&1; }
frontend_image_exists() { docker image inspect "${FRONTEND_IMAGE}" >/dev/null 2>&1; }
kx_base_exists()       { docker image inspect "${KX_BASE_TAG_EFFECTIVE}" >/dev/null 2>&1; }

UP_ARGS=(-d --force-recreate)
case "${BUILD_MODE}" in
  auto)
    if backend_image_exists && frontend_image_exists; then
      UP_ARGS+=(--no-build)
      echo "▶ 镜像已存在，直接启动（--no-build；强制重建加 --build）"
    elif kx_base_exists; then
      UP_ARGS+=(--build)
      echo "▶ 镜像缺失但 ${KX_BASE_TAG_EFFECTIVE} 可用，现场构建"
    else
      echo "❌ 既没有 ${BACKEND_IMAGE}，也没有 ${KX_BASE_TAG_EFFECTIVE}" >&2
      echo "   离线导入: ./deploy/bin/load-images.sh   （tars 放 ${POCKET_IMAGE_DIR}）" >&2
      echo "   或加载 kx-base 后用 --build 现场构建" >&2
      exit 1
    fi
    ;;
  build)
    kx_base_exists || {
      echo "❌ 构建需要 ${KX_BASE_TAG_EFFECTIVE}，当前未加载" >&2
      echo "   docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz" >&2
      exit 1
    }
    UP_ARGS+=(--build)
    ;;
  no-build)
    backend_image_exists || { echo "❌ 镜像不存在: ${BACKEND_IMAGE}"; exit 1; }
    frontend_image_exists || { echo "❌ 镜像不存在: ${FRONTEND_IMAGE}"; exit 1; }
    UP_ARGS+=(--no-build)
    ;;
esac

# ── 外部网络预检（仅当显式并入外部网络时） ────────────────────────
if [[ "${OPP_NET_EXTERNAL}" == "true" ]]; then
  docker network inspect "${OPP_NET_NAME}" >/dev/null 2>&1 || {
    echo "❌ OPP_NET_EXTERNAL=true 但网络不存在: ${OPP_NET_NAME}" >&2
    echo "   docker network create ${OPP_NET_NAME}" >&2
    exit 1
  }
fi

mkdir -p "${POCKET_LOG_DIR}"
START_TS="$(date +%Y%m%d-%H%M%S)"
echo "${START_TS}" > "${POCKET_LOG_DIR}/.last-start"

echo "━━━ start: ${POCKET_PROJECT_NAME} (${DEPLOY_ENV}) ━━━"
echo "  ENV_FILE  = ${POCKET_ENV_FILE}"
echo "  COMPOSE   = ${POCKET_COMPOSE_FILE}"
echo "  DATA_DIR  = ${POCKET_DATA_DIR}"
echo "  LOG_DIR   = ${POCKET_LOG_DIR}"
echo "  NET       = ${OPP_NET_NAME} (external=${OPP_NET_EXTERNAL})"
echo "  HTTP_PORT = ${POCKET_HTTP_PORT}  FRONTEND_PORT = ${POCKET_FRONTEND_PORT}"

echo "▶ docker compose up ${UP_ARGS[*]}$([[ "${BACKEND_ONLY}" == true ]] && echo ' pocketd')"
if [[ "${BACKEND_ONLY}" == true ]]; then
  "${DOCKER_COMPOSE[@]}" up "${UP_ARGS[@]}" pocketd
else
  "${DOCKER_COMPOSE[@]}" up "${UP_ARGS[@]}"
fi

# ── 健康检查 ──────────────────────────────────────────────────────
echo "▶ 等待 /healthz 通过（最多 60s）…"
for _ in $(seq 1 30); do
  BE_OK=false; FE_OK=true
  curl -sf "http://localhost:${POCKET_HTTP_PORT}/healthz" >/dev/null 2>&1 && BE_OK=true
  if [[ "${BACKEND_ONLY}" != true ]]; then
    FE_OK=false
    curl -sf "http://localhost:${POCKET_FRONTEND_PORT}/healthz" >/dev/null 2>&1 && FE_OK=true
  fi
  if [[ "${BE_OK}" == true && "${FE_OK}" == true ]]; then
    echo "  ✅ pocketd   http://localhost:${POCKET_HTTP_PORT}"
    [[ "${BACKEND_ONLY}" == true ]] || echo "  ✅ frontend  http://localhost:${POCKET_FRONTEND_PORT}"
    echo "${START_TS}" > "${POCKET_LOG_DIR}/.last-healthy"
    "${DOCKER_COMPOSE[@]}" ps
    exit 0
  fi
  sleep 2
done

echo "❌ 启动超时（60s），pocketd 最后 80 行日志：" >&2
"${DOCKER_COMPOSE[@]}" logs --tail=80 pocketd || true
exit 1
