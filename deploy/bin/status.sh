#!/usr/bin/env bash
# =====================================================================
# status.sh — 查看 pocketd / frontend 运行状态
#
# 展示：
#   - docker compose ps
#   - 容器存活状态
#   - /healthz 探测结果
#   - 数据/日志/配置 目录大小
#   - 最近一次启动时间戳
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

echo "━━━ status: ${POCKET_PROJECT_NAME} (${DEPLOY_ENV}) ━━━"

if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "⚠️  env file 不存在: ${POCKET_ENV_FILE}（compose 状态查询将失败）"
  echo "   252 上请用: DEPLOY_ENV=server $0"
fi

DOCKER_COMPOSE=(docker compose
  -p "${POCKET_PROJECT_NAME}"
  --env-file "${POCKET_ENV_FILE}"
  -f "${POCKET_COMPOSE_FILE}"
)

# 容器状态
echo "▶ docker compose ps"
if "${DOCKER_COMPOSE[@]}" ps --format json >/dev/null 2>&1; then
  "${DOCKER_COMPOSE[@]}" ps --format "table {{.Name}}\t{{.State}}\t{{.Status}}\t{{.Ports}}"
else
  "${DOCKER_COMPOSE[@]}" ps
fi

# /healthz 探测
echo
echo "▶ 健康检查"
if http_ok "http://localhost:${POCKET_HTTP_PORT}/healthz"; then
  echo "  ✅ pocketd /healthz OK (http://localhost:${POCKET_HTTP_PORT})"
else
  echo "  ❌ pocketd /healthz 失败"
fi
if http_ok "http://localhost:${POCKET_FRONTEND_PORT}/healthz"; then
  echo "  ✅ frontend /healthz OK (http://localhost:${POCKET_FRONTEND_PORT})"
else
  echo "  ⚠️  frontend /healthz 失败（可能未启动或前端不走 /healthz）"
fi

# 目录大小
echo
echo "▶ 目录占用"
du -sh "${POCKET_DATA_DIR}" 2>/dev/null || echo "  data:    (missing)"
du -sh "${POCKET_LOG_DIR}" 2>/dev/null || echo "  logs:    (missing)"
du -sh "${POCKET_BACKUP_DIR}" 2>/dev/null || echo "  backup:  (missing)"
du -sh "${POCKET_IMAGE_DIR}" 2>/dev/null || echo "  images:  (missing)"

# 最近启动
if [[ -f "${POCKET_LOG_DIR}/.last-start" ]]; then
  echo
  echo "▶ 最近启动: $(cat "${POCKET_LOG_DIR}/.last-start")"
fi
if [[ -f "${POCKET_LOG_DIR}/.last-healthy" ]]; then
  echo "  最近健康: $(cat "${POCKET_LOG_DIR}/.last-healthy")"
fi
