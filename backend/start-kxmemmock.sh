#!/usr/bin/env bash
# start-kxmemmock.sh — 启动本地 kxmemory mock 服务
#
# 用法：
#   ./start-kxmemmock.sh                # 监听 8089 端口
#   POCKET_KXMEMMOCK_PORT=9000 ./start-kxmemmock.sh
#
# 与 pocketd 配合：
#   POCKET_KXMEMORY_BASE_URL=http://127.0.0.1:8089 ./pocketd

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PORT="${POCKET_KXMEMMOCK_PORT:-8089}"
LOG_FILE="${SCRIPT_DIR}/logs/kxmemmock.log"
PID_FILE="${SCRIPT_DIR}/logs/kxmemmock.pid"

mkdir -p "${SCRIPT_DIR}/logs"

# 如果已经在跑，先停掉
if [[ -f "${PID_FILE}" ]] && kill -0 "$(cat "${PID_FILE}")" 2>/dev/null; then
  echo "[kxmemmock] already running with PID $(cat "${PID_FILE}"), stopping first"
  kill "$(cat "${PID_FILE}")" || true
  rm -f "${PID_FILE}"
fi

# 构建
cd "${SCRIPT_DIR}"
if [[ ! -f ./kxmemmock ]] || [[ ./cmd/kxmemmock/main.go -nt ./kxmemmock ]]; then
  echo "[kxmemmock] building..."
  go build -o ./kxmemmock ./cmd/kxmemmock
fi

# 启动
echo "[kxmemmock] starting on port ${PORT}, logs → ${LOG_FILE}"
nohup ./kxmemmock -port "${PORT}" >"${LOG_FILE}" 2>&1 &
PID=$!
echo "${PID}" >"${PID_FILE}"

# 等服务起来
for i in {1..20}; do
  if curl -sf "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
    echo "[kxmemmock] ✓ ready (PID=${PID})"
    echo "[kxmemmock] endpoints:"
    echo "  POST http://127.0.0.1:${PORT}/v1/notes/classify"
    echo "  POST http://127.0.0.1:${PORT}/v1/emails/classify"
    echo "  POST http://127.0.0.1:${PORT}/v1/emails/daily-summary"
    echo "  GET  http://127.0.0.1:${PORT}/healthz"
    echo ""
    echo "[kxmemmock] To use with pocketd:"
    echo "  export POCKET_KXMEMORY_BASE_URL=http://127.0.0.1:${PORT}"
    exit 0
  fi
  sleep 0.2
done

echo "[kxmemmock] ✗ failed to start within 4s; check ${LOG_FILE}"
exit 1