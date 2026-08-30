#!/usr/bin/env bash
# =====================================================================
# tunnel-252.sh — 本地 → 252 docker PG 的 SSH tunnel 管理（仅 local 用）
#
# openpocket 的权威 PG 在 252 的 docker 中（内网 172.16.2.210:5432），
# 公网不开放 5432，本地容器必须经 SSH tunnel 访问：
#
#   宿主 localhost:15432  ──ssh──▶  252 内网 172.16.2.210:5432
#   容器内 DSN 用 host.docker.internal:15432（compose 已配 host-gateway）
#
# 凭据来源（按优先级，均不入库）：
#   1. 环境变量 SSHPASS（配合 sshpass）
#   2. SSH key（推荐：ssh-copy-id 到 252 后免密，无需 sshpass）
#
# 用法：
#   ./deploy/bin/tunnel-252.sh up       # 建立/复用 tunnel
#   ./deploy/bin/tunnel-252.sh status   # 检查端口与连通性
#   ./deploy/bin/tunnel-252.sh down     # 关闭 tunnel
# =====================================================================

set -euo pipefail

export DEPLOY_ENV="${DEPLOY_ENV:-local}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

ACTION="${1:-status}"
LOCAL_PORT="${OPP_PG_PORT}"   # local 环境下即 tunnel 本地端口（默认 15432）

port_open() {
  local host="$1" port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z -G 3 "${host}" "${port}" >/dev/null 2>&1
  else
    timeout 3 bash -c "</dev/tcp/${host}/${port}" >/dev/null 2>&1
  fi
}

tunnel_pid() {
  lsof -tiTCP:"${LOCAL_PORT}" -sTCP:LISTEN 2>/dev/null | head -1 || true
}

case "${ACTION}" in
  status)
    echo "━━━ tunnel-252: status ━━━"
    if pid="$(tunnel_pid)"; [[ -n "${pid}" ]]; then
      echo "  ✅ 端口 ${LOCAL_PORT} 由 pid=${pid} 监听（tunnel 在跑）"
    else
      echo "  ❌ 端口 ${LOCAL_PORT} 无监听（tunnel 未建立）"
      exit 1
    fi
    if port_open "127.0.0.1" "${LOCAL_PORT}"; then
      echo "  ✅ 127.0.0.1:${LOCAL_PORT} TCP 可达"
    else
      echo "  ⚠️  127.0.0.1:${LOCAL_PORT} TCP 探测失败"
      exit 1
    fi
    echo "  DSN 目标: ${OPP_PG_USER}@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB}"
    ;;

  up)
    echo "━━━ tunnel-252: up ━━━"
    if pid="$(tunnel_pid)"; [[ -n "${pid}" ]]; then
      echo "  ✅ tunnel 已存在（pid=${pid}），复用"
      exit 0
    fi
    SSH_CMD=(ssh -f -N
      -o ServerAliveInterval=30
      -o ServerAliveCountMax=3
      -o ExitOnForwardFailure=yes
      -o StrictHostKeyChecking=accept-new
      -p "${OPP_252_SSH_PORT}"
      -L "${LOCAL_PORT}:${OPP_252_PG_INTERNAL_HOST}:5432"
      "${OPP_252_SSH_USER}@${OPP_252_SSH_HOST}")
    if [[ -n "${SSHPASS:-}" ]] && command -v sshpass >/dev/null 2>&1; then
      sshpass -e "${SSH_CMD[@]}"
    else
      # 无 sshpass 时用 BatchMode 快速失败，避免卡在交互式密码提示；
      # 请先配 SSH key（ssh-copy-id）或安装 sshpass 并设 SSHPASS。
      echo "${SSH_CMD[@]}"
      "${SSH_CMD[@]}" -o BatchMode=yes
    fi
    sleep 2
    if port_open "127.0.0.1" "${LOCAL_PORT}"; then
      echo "  ✅ tunnel 建立: localhost:${LOCAL_PORT} → ${OPP_252_PG_INTERNAL_HOST}:5432（经 ${OPP_252_SSH_HOST}）"
    else
      echo "  ❌ tunnel 建立后端口仍不通；检查 SSH 凭据/网络" >&2
      exit 1
    fi
    ;;

  down)
    echo "━━━ tunnel-252: down ━━━"
    pid="$(tunnel_pid)" || true
    if [[ -n "${pid:-}" ]]; then
      kill "${pid}" && echo "  ✅ 已关闭 tunnel（pid=${pid}）"
    else
      echo "  （无 tunnel 在跑）"
    fi
    ;;

  *)
    echo "用法: $0 {up|down|status}" >&2
    exit 1
    ;;
esac
