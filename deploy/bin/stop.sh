#!/usr/bin/env bash
# =====================================================================
# stop.sh — 停止 pocketd + frontend
#
# 默认只 down（保留 volume + 网络）。加 --volumes 同时清掉 pocketd_data
# 命名卷（小心！会丢 sqlite 数据，除非已 backup）。
#
# 用法：
#   ./deploy/bin/stop.sh                # 仅停服务，保留数据
#   ./deploy/bin/stop.sh --volumes      # 停服务并清 pocketd_data 卷
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

REMOVE_VOLUMES=false
FRONTEND_ONLY=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --volumes) REMOVE_VOLUMES=true; shift ;;
    --frontend-only) FRONTEND_ONLY=true; shift ;;
    --help) echo "用法: $0 [--volumes]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done
if [[ "$FRONTEND_ONLY" == true && "$REMOVE_VOLUMES" == true ]]; then
  echo "frontend-only 不允许删除卷" >&2
  exit 2
fi

if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "❌ env file 不存在: ${POCKET_ENV_FILE}" >&2
  echo "   252 上请用: DEPLOY_ENV=server $0" >&2
  exit 1
fi

if [[ "${REMOVE_VOLUMES}" == true ]]; then
  echo "⚠️  --volumes 将执行 docker compose down --volumes"
  echo "   注意：数据目录为宿主 bind mount（${POCKET_DATA_DIR}），compose 删不到它；"
  echo "   如需彻底清数据，请确认后手工 rm -rf ${POCKET_DATA_DIR}"
  read -rp "确认? (yes/no): " ans
  [[ "${ans}" == "yes" ]] || { echo "已取消"; exit 0; }
fi

DOCKER_COMPOSE=(docker compose
  -p "${POCKET_PROJECT_NAME}"
  --env-file "${POCKET_ENV_FILE}"
  -f "${POCKET_COMPOSE_FILE}"
)

if [[ "$FRONTEND_ONLY" == true ]]; then
  [[ "$DEPLOY_ENV" == "local" ]] || { echo "frontend-only 仅允许 local" >&2; exit 2; }
  # The root controller supplies a previously verified, full container ID.
  # Recheck through Compose immediately before signalling the application.
  [[ "${OPP_EXPECT_FRONTEND_ID:-}" =~ ^[a-f0-9]{64}$ ]] || {
    echo "缺少已核实的 OPP_EXPECT_FRONTEND_ID" >&2; exit 2;
  }
  actual_id="$("${DOCKER_COMPOSE[@]}" ps --all -q frontend)"
  [[ "$actual_id" == "$OPP_EXPECT_FRONTEND_ID" ]] || {
    echo "frontend 容器归属已变化，拒绝停止" >&2; exit 2;
  }
  labels="$(docker inspect --format '{{index .Config.Labels "com.docker.compose.project"}}|{{index .Config.Labels "com.docker.compose.service"}}' "$actual_id")"
  [[ "$labels" == "$POCKET_PROJECT_NAME|frontend" ]] || {
    echo "frontend 标签不匹配，拒绝停止" >&2; exit 2;
  }
  # Stop this immutable ID. A concurrent Compose recreation must not cause
  # a second lookup to signal its replacement container.
  docker stop --time 10 "$actual_id"
  echo "✅ 仅停止 frontend；pocketd、网络、卷保持"
  exit 0
fi

if [[ "${REMOVE_VOLUMES}" == true ]]; then
  echo "▶ docker compose down --volumes"
  "${DOCKER_COMPOSE[@]}" down --volumes
else
  echo "▶ docker compose down"
  "${DOCKER_COMPOSE[@]}" down
fi

echo "✅ 已停止 ${POCKET_PROJECT_NAME}"
echo "   数据保留在: ${POCKET_DATA_DIR}"
echo "   日志保留在: ${POCKET_LOG_DIR}"
