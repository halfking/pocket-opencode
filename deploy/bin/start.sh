#!/usr/bin/env bash
# =====================================================================
# start.sh — 启动 pocketd + frontend（deploy/bin/docker-compose.opp.yml）
#
# 镜像策略（offline-first，与仓库既有约定一致）：
#   - 本地已有 opencode-pocket:${OPP_IMAGE_TAG} → 直接 up（--no-build）
#   - 没有镜像但 kx-base 离线镜像已加载 → 现场构建
#   - 都没有 → 报错并提示 load-images.sh / images/ 目录
#
# blue-green 集成（2026-09-03）：
#   - 自动 stage 当前发布到 bin/${OPP_VERSION_BUILD}/
#   - 健康检查通过后 bin/current → bin/${OPP_VERSION_BUILD}
#   - 健康检查失败 → 自动 rollback 到 OPP_PREVIOUS_BUILD
#   - bin/current 缺失 → 自动初始化一次（兼容旧部署升级）
#
# 用法：
#   ./deploy/bin/start.sh              # 自动判断 build / no-build
#   ./deploy/bin/start.sh --build      # 强制重新构建
#   ./deploy/bin/start.sh --no-build   # 强制只用已存在镜像
#   ./deploy/bin/start.sh --backend-only  # 只起 pocketd（不起 frontend）
#   ./deploy/bin/start.sh --dry-run    # 不真起容器，只跑 detect + stage + healthcheck 模拟
#   ./deploy/bin/start.sh --rollback   # 回滚到上一个 verified 版本
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"
# shellcheck disable=SC1091
source "${LIB_DIR}/blue-green.sh"

BUILD_MODE="auto"
BACKEND_ONLY=false
DRY_RUN=false
ACTION="deploy"   # deploy | rollback
while [[ $# -gt 0 ]]; do
  case "$1" in
    --build) BUILD_MODE="build"; shift ;;
    --no-build) BUILD_MODE="no-build"; shift ;;
    --backend-only) BACKEND_ONLY=true; shift ;;
    --dry-run) DRY_RUN=true; shift ;;
    --rollback) ACTION="rollback"; shift ;;
    --help) echo "用法: $0 [--build|--no-build] [--backend-only] [--dry-run] [--rollback]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

# ── blue-green：rollback 模式直接走 bg_rollback ──────────────────
if [[ "${ACTION}" == "rollback" ]]; then
  echo "━━━ rollback ━━━"
  previous="$(bg_rollback)"
  echo "  已回滚到 bin/${previous}"
  # 回滚后再走一次 deploy（用旧版本镜像）
  OPP_VERSION_BUILD="${previous}"
  export OPP_VERSION_BUILD
fi

command -v docker >/dev/null 2>&1 || { echo "❌ docker 未安装"; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "❌ 需要 docker compose v2（docker-compose v1 不支持）"; exit 1; }
if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "❌ env file 不存在: ${POCKET_ENV_FILE}" >&2
  echo "   本地: ./deploy-local.sh 会自动生成" >&2
  echo "   154 : 手工填 ${POCKET_ENV_FILE}，再 ./deploy-154.sh" >&2
  echo "   245 : 手工填 ${POCKET_ENV_FILE}，再 ./deploy-245.sh" >&2
  echo "   252 : 手工填 ${POCKET_ENV_FILE}，再 ./deploy-252.sh" >&2
  exit 1
fi
[[ -f "${POCKET_COMPOSE_FILE}" ]] || { echo "❌ compose 缺失: ${POCKET_COMPOSE_FILE}"; exit 1; }
# http_ok 由 env.sh 提供（curl 优先，无则 wget）

# 离线 kx-base 镜像仅覆盖 arm64；amd64（如 252）必须用 save/load-images 流程，
# 不能现场 --build。把架构门禁提到任何构建决策之前。
check_arch_for_build() {
  if [[ "$(uname -m)" != "arm64" ]]; then
    echo "❌ 本机 $(uname -m)，kx-base 离线包仅覆盖 arm64——现场 --build 不可行" >&2
    echo "   请用 save-images.sh 导出 amd64 镜像 + load-images.sh 导入，勿现场 --build" >&2
    return 1
  fi
  return 0
}

# 端口绑定 IP 是否落在本机接口上（避免 154/245/252 在非目标机器上 bind 失败）
ip_addr_has() {
  local target="$1"
  [[ "${target}" == "0.0.0.0" || -z "${target}" ]] && return 0
  if command -v ip >/dev/null 2>&1; then
    ip -4 -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | grep -qx "${target}"
  elif command -v ifconfig >/dev/null 2>&1; then
    ifconfig 2>/dev/null | grep -E "inet ${target}( |$)" >/dev/null
  else
    # 无 ip/ifconfig 时降级放行（不强制阻断）
    return 0
  fi
}

# ── blue-green：计算版本 id 并 stage ─────────────────────────────
if [[ -z "${OPP_VERSION_BUILD}" ]]; then
  bg_compute_id >/dev/null
fi
bg_init

# current 不存在 → 自动 stage 一个新的（兼容老环境首次升级）
if [[ ! -L "${POCKET_BIN_DIR}/current" ]]; then
  echo "  🆕 bin/current 不存在，自动 stage ${OPP_VERSION_BUILD}"
  bg_stage "${OPP_VERSION_BUILD}"
  bg_switch "${OPP_VERSION_BUILD}" "" >/dev/null
fi

# 把 compose snippet（如果存在）拼到主 compose 上
COMPOSE_SNIPPET="$(bg_compose_snippet)"
DOCKER_COMPOSE=(docker compose
  -p "${POCKET_PROJECT_NAME}"
  --env-file "${POCKET_ENV_FILE}"
  -f "${POCKET_COMPOSE_FILE}"
)
if [[ -n "${COMPOSE_SNIPPET}" ]]; then
  DOCKER_COMPOSE+=(-f "${COMPOSE_SNIPPET}")
fi

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
    if backend_image_exists && { [[ "${BACKEND_ONLY}" == true ]] || frontend_image_exists; }; then
      UP_ARGS+=(--no-build)
      echo "▶ 镜像已存在，直接启动（--no-build；强制重建加 --build）"
    elif kx_base_exists; then
      check_arch_for_build || exit 1
      UP_ARGS+=(--build)
      echo "▶ 镜像缺失但 ${KX_BASE_TAG_EFFECTIVE} 可用，现场构建"
    else
      echo "❌ 既没有所需镜像，也没有 ${KX_BASE_TAG_EFFECTIVE}" >&2
      [[ "${BACKEND_ONLY}" == true ]] && echo "   （--backend-only：只需 ${BACKEND_IMAGE}）" >&2
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
    check_arch_for_build || exit 1
    UP_ARGS+=(--build)
    ;;
  no-build)
    backend_image_exists || { echo "❌ 镜像不存在: ${BACKEND_IMAGE}"; exit 1; }
    [[ "${BACKEND_ONLY}" == true ]] || frontend_image_exists || { echo "❌ 镜像不存在: ${FRONTEND_IMAGE}"; exit 1; }
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

# ── 端口绑定 IP 预检：避免在 eth0 没目标 IP 的机器上 bind 失败 ─────────────
if [[ "${DRY_RUN:-false}" != "true" ]] && ! ip_addr_has "${POCKET_PORT_BIND_IP}"; then
  echo "❌ POCKET_PORT_BIND_IP=${POCKET_PORT_BIND_IP} 不在本机任何接口上" >&2
  echo "   当前 hostname=$(hostname -s 2>/dev/null || hostname)" >&2
  echo "   154/245/252 应在 eth0 配目标 IP；本地开发用 0.0.0.0（默认）" >&2
  echo "   临时绕过：export POCKET_PORT_BIND_IP=0.0.0.0 后重跑" >&2
  exit 1
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
echo "  HTTP_PORT = ${POCKET_HTTP_PORT}@${POCKET_PORT_BIND_IP}  FRONTEND_PORT = ${POCKET_FRONTEND_PORT}"

echo "▶ docker compose up ${UP_ARGS[*]}$([[ "${BACKEND_ONLY}" == true ]] && echo ' pocketd')"
if [[ "${DRY_RUN}" == true ]]; then
  echo "  🧪 --dry-run: 跳过 docker compose up，仅打印预期命令"
  printf '    %q ' "${DOCKER_COMPOSE[@]}" >&2
  printf ' %q' "${UP_ARGS[@]}" >&2
  printf ' %s\n' "${BACKEND_ONLY:+pocketd}" >&2
  exit 0
fi
if [[ "${BACKEND_ONLY}" == true ]]; then
  "${DOCKER_COMPOSE[@]}" up "${UP_ARGS[@]}" pocketd
else
  "${DOCKER_COMPOSE[@]}" up "${UP_ARGS[@]}"
fi

# ── 健康检查 ──────────────────────────────────────────────────────
echo "▶ 等待 /healthz 通过（最多 60s）…"
for _ in $(seq 1 30); do
  BE_OK=false; FE_OK=true
  http_ok "http://${POCKET_HTTP_PROBE_HOST}:${POCKET_HTTP_PORT}/healthz" && BE_OK=true
  if [[ "${BACKEND_ONLY}" != true ]]; then
    FE_OK=false
    http_ok "http://localhost:${POCKET_FRONTEND_PORT}/healthz" && FE_OK=true
  fi
  if [[ "${BE_OK}" == true && "${FE_OK}" == true ]]; then
    echo "  ✅ pocketd   http://${POCKET_HTTP_PROBE_HOST}:${POCKET_HTTP_PORT}"
    [[ "${BACKEND_ONLY}" == true ]] || echo "  ✅ frontend  http://localhost:${POCKET_FRONTEND_PORT}"
    echo "${START_TS}" > "${POCKET_LOG_DIR}/.last-healthy"
    bg_mark_healthy "${OPP_VERSION_BUILD}"
    "${DOCKER_COMPOSE[@]}" ps
    exit 0
  fi
  sleep 2
done

# 健康检查失败 → 自动 rollback（如果有 previous）
echo "❌ 启动超时（60s），pocketd 最后 80 行日志：" >&2
"${DOCKER_COMPOSE[@]}" logs --tail=80 pocketd || true
if [[ -n "${OPP_PREVIOUS_BUILD}" ]] && [[ -d "${POCKET_BIN_DIR}/${OPP_PREVIOUS_BUILD}" ]]; then
  echo "  🔁 尝试自动回滚到 ${OPP_PREVIOUS_BUILD}"
  bg_rollback || true
fi
exit 1
