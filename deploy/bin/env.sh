#!/usr/bin/env bash
# =====================================================================
# env.sh — opencode-pocket 部署环境变量中心（deploy/bin/* 共享）
#
# 所有 deploy/bin/ 下的脚本都应该 source 本文件，以获得一致的目录、
# 端口、compose project 配置。任何子目录覆盖变量都以 POCKET_ 开头。
#
# 用法：
#   source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
#   # 或由 deploy-local.sh / deploy-252.sh 在 source 前预设 DEPLOY_ENV
#
# 关键变量（输入）：
#   DEPLOY_ENV         local | server（决定 DEPLOY_BASE_DIR 默认值）
#   DEPLOY_BASE_DIR    顶层目录（可显式覆盖默认）
#
# 关键变量（输出 / 派生）：
#   POCKET_BASE_DIR / POCKET_DATA_DIR / POCKET_LOG_DIR
#   POCKET_CONFIG_DIR / POCKET_IMAGE_DIR / POCKET_BACKUP_DIR
#   POCKET_HTTP_PORT / POCKET_FRONTEND_PORT
#   POCKET_PROJECT_NAME / POCKET_COMPOSE_FILE / POCKET_ENV_FILE
# =====================================================================

# ── 防止重复 source 时覆盖已设置的值 ──────────────────────────────
if [[ -n "${__POCKET_ENV_LOADED:-}" ]]; then
  return 0 2>/dev/null || true
fi
__POCKET_ENV_LOADED=1

# 定位 env.sh 自身：bash source 时用 BASH_SOURCE；zsh source 时 BASH_SOURCE
# 为空，zsh 会把 $0 设为被 source 的文件路径，两者取其一即可。
_env_self="${BASH_SOURCE[0]:-$0}"
# shellcheck disable=SC2155
export SCRIPT_DIR="$(cd "$(dirname "${_env_self}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
export REPO_ROOT
unset _env_self

# ── 1. DEPLOY_ENV 选择默认值 ─────────────────────────────────────
# 默认走本地；显式 DEPLOY_ENV=server 才走 /opt/kaixuan/opp。
export DEPLOY_ENV="${DEPLOY_ENV:-local}"

case "${DEPLOY_ENV}" in
  local)
    : "${DEPLOY_BASE_DIR:=${HOME}/Downloads/kaixuan/opp}"
    : "${POCKET_PROJECT_NAME:=opencode-pocket-local}"
    ;;
  server)
    : "${DEPLOY_BASE_DIR:=/opt/kaixuan/opp}"
    : "${POCKET_PROJECT_NAME:=opencode-pocket}"
    ;;
  *)
    echo "[env.sh] unknown DEPLOY_ENV='${DEPLOY_ENV}' (expected: local|server)" >&2
    return 1 2>/dev/null || exit 1
    ;;
esac
export DEPLOY_BASE_DIR

# ── 1.5 数据库拓扑（openpocket 唯一权威 PG 在 252 的 docker 中） ──
#   local  : 宿主 SSH tunnel localhost:15432 → 252 内网 172.16.2.210:5432
#            （容器内经 host.docker.internal:15432 访问；tunnel-252.sh 管理）
#   server : 252 本机 docker PG，容器经内网地址直连，无需 tunnel。
#   库：252 上已有专用 `pocket` 库（2026-08-31 探测确认；其 public schema
#   有一套零数据的旧空表，不用它）。表建在 opencode_pocket schema，由
#   后端 migration 全权管理。
export OPP_PG_HOST="${OPP_PG_HOST:-$([[ "${DEPLOY_ENV}" == "server" ]] && echo "172.16.2.210" || echo "host.docker.internal")}"
export OPP_PG_PORT="${OPP_PG_PORT:-$([[ "${DEPLOY_ENV}" == "server" ]] && echo "5432" || echo "15432")}"
export OPP_PG_DB="${OPP_PG_DB:-pocket}"
export OPP_PG_USER="${OPP_PG_USER:-llm_gateway}"
export OPP_PG_SCHEMA="${OPP_PG_SCHEMA:-opencode_pocket}"

# 252 连接信息（tunnel-252.sh 用；密码不入库，从 OPP_PG_PASSWORD /
# SSHPASS 环境变量或本地未跟踪文件读取）
export OPP_252_SSH_HOST="${OPP_252_SSH_HOST:-115.29.212.252}"
export OPP_252_SSH_PORT="${OPP_252_SSH_PORT:-25022}"
export OPP_252_SSH_USER="${OPP_252_SSH_USER:-root}"
export OPP_252_PG_INTERNAL_HOST="${OPP_252_PG_INTERNAL_HOST:-172.16.2.210}"

# ── 2. 子目录派生（支持单点覆盖） ───────────────────────────────
# 没显式给 POCKET_*_DIR 时，按 ${DEPLOY_BASE_DIR}/<sub> 拼接。
: "${POCKET_BASE_DIR:=${DEPLOY_BASE_DIR}}"
: "${POCKET_DATA_DIR:=${DEPLOY_BASE_DIR}/data}"
: "${POCKET_LOG_DIR:=${DEPLOY_BASE_DIR}/logs}"
: "${POCKET_CONFIG_DIR:=${DEPLOY_BASE_DIR}/config}"
: "${POCKET_IMAGE_DIR:=${DEPLOY_BASE_DIR}/images}"
: "${POCKET_BACKUP_DIR:=${DEPLOY_BASE_DIR}/backup}"
export POCKET_BASE_DIR POCKET_DATA_DIR POCKET_LOG_DIR \
       POCKET_CONFIG_DIR POCKET_IMAGE_DIR POCKET_BACKUP_DIR

# ── 3. 端口 & compose project（可按环境覆盖） ────────────────────
: "${POCKET_HTTP_PORT:=8088}"
: "${POCKET_FRONTEND_PORT:=4175}"
export POCKET_HTTP_PORT POCKET_FRONTEND_PORT
# 别名：acc-integration compose 用 *_HOST_PORT 命名，这里同步导出，
# 方便同一批变量在两套 compose 间复用。
export POCKET_HOST_PORT="${POCKET_HOST_PORT:-${POCKET_HTTP_PORT}}"
export POCKET_FRONTEND_HOST_PORT="${POCKET_FRONTEND_HOST_PORT:-${POCKET_FRONTEND_PORT}}"

# env_file 在 config/ 下，命名按 DEPLOY_ENV 区分，避免本地/服务器互踩
case "${DEPLOY_ENV}" in
  local)  : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.local}" ;;
  server) : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.server}" ;;
esac
export POCKET_ENV_FILE

# 独立部署 compose（本地/252 共用）。acc-integration 的 compose 保持独立，
# 两者互不影响；如需 acc 联调，见 README 的 OPP_NET_EXTERNAL 说明。
export POCKET_COMPOSE_FILE="${SCRIPT_DIR}/docker-compose.opp.yml"

# 镜像 tag（save-images.sh / load-images.sh 与 compose 共用）
: "${OPP_IMAGE_TAG:=pocket-opp}"
export OPP_IMAGE_TAG

# 网络开关：默认自建 opp-<env>-net；置 OPP_NET_EXTERNAL=true 并指定
# OPP_NET_NAME 时并入既有外部网络（如 acc-local-net）。
export OPP_NET_NAME="${OPP_NET_NAME:-opp-${DEPLOY_ENV}-net}"
export OPP_NET_EXTERNAL="${OPP_NET_EXTERNAL:-false}"

# ── 4. 兼容旧 deploy/ 脚本的环境变量名 ──────────────────────────
# 旧 deploy.sh / verify.sh 读 POCKET_DEPLOY_ENV_FILE；这里同步过去。
export POCKET_DEPLOY_ENV_FILE="${POCKET_ENV_FILE}"

# ── 5. pocketd 容器内路径（被 docker-compose.override.yml 引用） ──
export POCKET_DATA_DIR_IN_CONTAINER="/app/data"

# ── 6. 调试输出（可选） ──────────────────────────────────────────
if [[ "${POCKET_ENV_DEBUG:-0}" == "1" ]]; then
  cat <<EOF
[env.sh] DEPLOY_ENV=${DEPLOY_ENV}
[env.sh] DEPLOY_BASE_DIR=${DEPLOY_BASE_DIR}
[env.sh] POCKET_DATA_DIR=${POCKET_DATA_DIR}
[env.sh] POCKET_LOG_DIR=${POCKET_LOG_DIR}
[env.sh] POCKET_CONFIG_DIR=${POCKET_CONFIG_DIR}
[env.sh] POCKET_ENV_FILE=${POCKET_ENV_FILE}
[env.sh] POCKET_COMPOSE_FILE=${POCKET_COMPOSE_FILE}
[env.sh] POCKET_HTTP_PORT=${POCKET_HTTP_PORT}
[env.sh] POCKET_FRONTEND_PORT=${POCKET_FRONTEND_PORT}
[env.sh] PG = ${OPP_PG_USER}@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB} (schema=${OPP_PG_SCHEMA})
EOF
fi
