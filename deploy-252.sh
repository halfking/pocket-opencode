#!/usr/bin/env bash
# =====================================================================
# deploy-252.sh — 252 生产部署入口（Linux）
#
# 默认配置（与 154 / 245 同构，端口和绑 IP 不同）：
#   - DEPLOY_BASE_DIR=/opt/kaixuan/openpocket
#   - OPP_SERVER_NAME=252
#   - 绑 eth0 内网 IP 172.16.2.210（服务器名"252"来自公网 115.29.212.252，
#     内网 IP 实为 172.16.2.210；8090/4175 已被其它服务占用，故错开）
#   - HTTP_PORT=8092  FRONTEND_PORT=4177
#   - PG 在 252 本机 docker 中（172.16.2.210:5432，由 .env.252 注入）
#   - OPP_DEPLOY_PG/REDIS/MYSQL 全部 false（DSN 由 .env.252 提供）
#
# 用法：
#   sudo ./deploy-252.sh
#   sudo ./deploy-252.sh --rollback
#   sudo ./deploy-252.sh --dry-run   # 演练模式：跳过 PG TCP / 容器起停
#   sudo DEPLOY_BASE_DIR=/srv/kaixuan/openpocket ./deploy-252.sh
# =====================================================================

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCRIPT_DIR="${ROOT_DIR}"
export DEPLOY_ENV="${DEPLOY_ENV:-server}"
export OPP_SERVER_NAME="${OPP_SERVER_NAME:-252}"

# 提取 --dry-run / --rollback 到顶层 DRY_RUN（让 PG TCP 探测与 IP 预检能识别）
DRY_RUN=false
for _arg in "$@"; do
  case "${_arg}" in
    --dry-run)   DRY_RUN=true ;;
    --rollback)  export ACTION="rollback" ;;
  esac
done
export DRY_RUN

# shellcheck disable=SC1091
source "${ROOT_DIR}/deploy/bin/env.sh"

# env.sh export 的 SCRIPT_DIR 是 deploy/bin/；后续脚本调用路径要还原
SCRIPT_DIR="${ROOT_DIR}"

echo "━━━ deploy-252 ━━━"
echo "  OS_KIND         = ${OPP_OS_KIND}"
echo "  DEPLOY_BASE_DIR = ${DEPLOY_BASE_DIR}"
echo "  HTTP_PORT       = ${POCKET_HTTP_PORT}@${POCKET_PORT_BIND_IP}"
echo "  FRONTEND_PORT   = ${POCKET_FRONTEND_PORT}"

# ── 平台门禁：252 是 Linux 生产 ──────────────────────────────────
if [[ "${OPP_OS_KIND}" != "linux" && "${OPP_OS_KIND}" != "wsl" ]]; then
  echo "  ❌ OS_KIND=${OPP_OS_KIND}，生产 252 必须 Linux（或 WSL）；非生产演练请用 deploy-local.sh" >&2
  exit 1
fi

# ── 服务器根目录权限检查 ────────────────────────────────────────
if [[ "${DEPLOY_BASE_DIR}" == /opt/* || "${DEPLOY_BASE_DIR}" == /srv/* ]]; then
  if [[ "$(id -u)" -ne 0 ]]; then
    echo "  ❌ 服务器部署到 ${DEPLOY_BASE_DIR} 需要 root，请用 sudo 重跑" >&2
    exit 1
  fi
fi

# ── 服务器 DB 策略：PG/Redis 在 252 本机内网，本机不部署 ─────────────
export OPP_DEPLOY_PG="${OPP_DEPLOY_PG:-false}"
export OPP_DEPLOY_REDIS="${OPP_DEPLOY_REDIS:-false}"
export OPP_DEPLOY_MYSQL="${OPP_DEPLOY_MYSQL:-false}"
# 252 上连 252 内网 PG（本机内网地址，DSN 由 .env.252 提供）
export OPP_PG_HOST="${OPP_PG_HOST:-172.16.2.210}"
export OPP_PG_PORT="${OPP_PG_PORT:-5432}"

# ── 1) 建目录 ──────────────────────────────────────────────────
"${SCRIPT_DIR}/deploy/bin/init-dirs.sh"

# ── 2) DB 检测 ─────────────────────────────────────────────────
"${SCRIPT_DIR}/deploy/bin/ensure-databases.sh"

# ── 3) 服务器 .env 必须存在 ────────────────────────────────────
if [[ ! -f "${POCKET_ENV_FILE}" ]]; then
  echo "  ❌ 服务器 .env 不存在: ${POCKET_ENV_FILE}" >&2
  echo "     请先把生产密钥、DSN、JWT_SECRET 等填入该文件" >&2
  echo "     模板可参考: deploy/acc-integration/.env.example" >&2
  exit 1
fi

read_env_stripped() {
  awk -F= -v key="$1" '$1 == key {sub(/^[^=]*=/, ""); gsub(/\r/, ""); gsub(/^"|"$/, ""); print; exit}' "${POCKET_ENV_FILE}"
}
if [[ "$(read_env_stripped POCKET_DEV_AUTH)" == "true" ]]; then
  echo "  ❌ 服务器 .env 里 POCKET_DEV_AUTH=true，拒绝启动" >&2
  exit 1
fi

POCKET_ENV_VALUE="$(read_env_stripped POCKET_ENV)"
if [[ "${POCKET_ENV_VALUE}" != "production" && "${POCKET_ENV_VALUE}" != "prod" ]]; then
  echo "  ❌ 服务器 .env 必须设置 POCKET_ENV=production（当前: '${POCKET_ENV_VALUE}'）" >&2
  exit 1
fi

if ! grep -q '^POCKET_POSTGRES_DSN=' "${POCKET_ENV_FILE}"; then
  echo "  ❌ 服务器 .env 缺少 POCKET_POSTGRES_DSN" >&2
  echo "     252 连本机 PG 示例: postgresql://${OPP_PG_USER}:<密码>@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB}?sslmode=disable" >&2
  exit 1
fi
# PG 主机/端口白名单校验：防 env 注入特殊字符进 /dev/tcp 探测串
if ! [[ "${OPP_PG_HOST}" =~ ^[A-Za-z0-9._-]+$ ]] || ! [[ "${OPP_PG_PORT}" =~ ^[0-9]+$ ]]; then
  echo "  ❌ OPP_PG_HOST/OPP_PG_PORT 含非法字符: '${OPP_PG_HOST}:${OPP_PG_PORT}'" >&2
  exit 1
fi
# dry-run 跳过 PG TCP 探测（演练环境本机通常不在 252 内网）
if [[ "${DRY_RUN:-false}" == "true" ]]; then
  echo "  ⏭  --dry-run: 跳过 PG TCP 探测（演练环境无法连 252 内网）"
else
  if timeout 3 bash -c "</dev/tcp/${OPP_PG_HOST}/${OPP_PG_PORT}" >/dev/null 2>&1; then
    echo "  ✅ PG 可达: ${OPP_PG_HOST}:${OPP_PG_PORT}"
  else
    echo "  ❌ PG 不可达: ${OPP_PG_HOST}:${OPP_PG_PORT}（252 本机 docker PG 未启动？）" >&2
    exit 1
  fi
fi

# ── 4) 拉起服务（透传 --backend-only / --rollback 等参数）────
exec "${SCRIPT_DIR}/deploy/bin/start.sh" "$@"
