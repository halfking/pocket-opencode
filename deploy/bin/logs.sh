#!/usr/bin/env bash
# =====================================================================
# logs.sh — 拉取并落盘 pocketd / frontend 日志
#
# 行为：
#   - 默认把 docker logs 拉下来写到 ${POCKET_LOG_DIR}/<service>-YYYYMMDD.log
#   - --follow 跟随输出（不落盘）
#   - --rotate 把当前 .log 重命名为 .log.YYYYMMDD-HHMMSS 并 touch 新空文件
#
# 用法：
#   ./deploy/bin/logs.sh                 # 拉 pocketd + frontend 各 200 行落盘
#   ./deploy/bin/logs.sh --follow         # docker compose logs -f（不落盘）
#   ./deploy/bin/logs.sh --service pocketd --tail 500
#   ./deploy/bin/logs.sh --rotate
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

SERVICE=""
FOLLOW=false
ROTATE=false
TAIL=200
while [[ $# -gt 0 ]]; do
  case "$1" in
    --service|-s) SERVICE="$2"; shift 2 ;;
    --follow|-f) FOLLOW=true; shift ;;
    --rotate) ROTATE=true; shift ;;
    --tail|-n) TAIL="$2"; shift 2 ;;
    --help) echo "用法: $0 [--service <name>] [--follow] [--rotate] [--tail N]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "❌ env file 不存在: ${POCKET_ENV_FILE}" >&2
  echo "   252 上请用: DEPLOY_ENV=server $0" >&2
  exit 1
fi

mkdir -p "${POCKET_LOG_DIR}"

DOCKER_COMPOSE=(docker compose
  -p "${POCKET_PROJECT_NAME}"
  --env-file "${POCKET_ENV_FILE}"
  -f "${POCKET_COMPOSE_FILE}"
)

if [[ "${FOLLOW}" == true ]]; then
  FOLLOW_ARGS=(logs -f --tail="${TAIL}")
  if [[ -n "${SERVICE}" ]]; then
    FOLLOW_ARGS+=("${SERVICE}")
  fi
  exec "${DOCKER_COMPOSE[@]}" "${FOLLOW_ARGS[@]}"
fi

if [[ "${ROTATE}" == true ]]; then
  DATE_TAG="$(date +%Y%m%d-%H%M%S)"
  if [[ -n "${SERVICE}" ]]; then
    SVCS_ROT=("${SERVICE}")
  else
    SVCS_ROT=(pocketd frontend)
  fi
  for svc in "${SVCS_ROT[@]}"; do
    f="${POCKET_LOG_DIR}/${svc}.log"
    [[ -f "${f}" ]] && mv "${f}" "${f}.${DATE_TAG}"
    : > "${f}"
  done
  echo "✅ 日志已轮转（${DATE_TAG}）"
  exit 0
fi

DATE_TAG="$(date +%Y%m%d)"
if [[ -n "${SERVICE}" ]]; then
  SVCS=("${SERVICE}")
else
  SVCS=(pocketd frontend)
fi

for svc in "${SVCS[@]}"; do
  out="${POCKET_LOG_DIR}/${svc}.log"
  echo "▶ ${svc}: ${TAIL} 行 → ${out}"
  "${DOCKER_COMPOSE[@]}" logs --no-color --tail="${TAIL}" "${svc}" >> "${out}" || true
done

echo "✅ 落盘完成；tail -f ${POCKET_LOG_DIR}/pocketd.log"
