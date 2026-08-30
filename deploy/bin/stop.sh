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
while [[ $# -gt 0 ]]; do
  case "$1" in
    --volumes) REMOVE_VOLUMES=true; shift ;;
    --help) echo "用法: $0 [--volumes]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

if [[ "${REMOVE_VOLUMES}" == true ]]; then
  echo "⚠️  --volumes 将删除 ${POCKET_DATA_DIR} 关联的 pocketd_data 命名卷"
  read -rp "确认? (yes/no): " ans
  [[ "${ans}" == "yes" ]] || { echo "已取消"; exit 0; }
fi

DOCKER_COMPOSE=(docker compose
  -p "${POCKET_PROJECT_NAME}"
  --env-file "${POCKET_ENV_FILE}"
  -f "${POCKET_COMPOSE_FILE}"
)

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
