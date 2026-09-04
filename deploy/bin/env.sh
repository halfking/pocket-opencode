#!/usr/bin/env bash
# =====================================================================
# env.sh — opencode-pocket 部署环境变量中心（deploy/bin/* 共享）
#
# 所有 deploy/bin/ 下的脚本都应该 source 本文件，以获得一致的目录、
# 端口、compose project 配置。任何子目录覆盖变量都以 POCKET_ 开头。
#
# 用法：
#   source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
#   # 或由 deploy-local.sh / deploy-154.sh / deploy-245.sh 在 source 前预设 DEPLOY_ENV
#
# 关键变量（输入）：
#   DEPLOY_ENV         local | server（决定 DEPLOY_BASE_DIR 默认值）
#   DEPLOY_BASE_DIR    顶层目录（可显式覆盖默认；不设则按 OS 自动选）
#   OPP_DEPLOY_VERSION / OPP_DEPLOY_BUILD   用于 bin/{version}.{build}/
#   OPP_DEPLOY_PG / OPP_DEPLOY_REDIS / OPP_DEPLOY_MYSQL   本机是否容器化部署该 DB
#
# 关键变量（输出 / 派生）：
#   POCKET_BASE_DIR / POCKET_DATA_DIR / POCKET_LOG_DIR
#   POCKET_CONFIG_DIR / POCKET_IMAGE_DIR / POCKET_BACKUP_DIR
#   POCKET_ATTACHMENTS_DIR / POCKET_RAW_LOG_DIR / POCKET_RUN_DIR
#   POCKET_HTTP_PORT / POCKET_FRONTEND_PORT
#   POCKET_PROJECT_NAME / POCKET_COMPOSE_FILE / POCKET_ENV_FILE
#   OPP_OS_KIND / OPP_SERVER_NAME / OPP_VERSION_BUILD / OPP_PREVIOUS_BUILD
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
LIB_DIR="${SCRIPT_DIR}/lib"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
export REPO_ROOT LIB_DIR
unset _env_self

# ── 0. OS 检测（先 source 库，让后续路径解析用得上） ──────────────
# shellcheck source=lib/os-detect.sh
source "${LIB_DIR}/os-detect.sh"
export OPP_OS_KIND="$(os_kind)"

# ── 1. DEPLOY_ENV 选择默认值 ─────────────────────────────────────
# 默认走本地；显式 DEPLOY_ENV=server 才走服务器路径。
export DEPLOY_ENV="${DEPLOY_ENV:-local}"

# 默认 DEPLOY_BASE_DIR：
#   1) 显式 DEPLOY_BASE_DIR（外部 export）→ 直接使用
#   2) DEPLOY_ENV=local → 按 OS 派生（macOS=~/kaixuan/openpocket；
#      Linux=同样，但有 WSL 走 linux；Windows=D:/ 或 C:/kaixuan/openpocket）
#   3) DEPLOY_ENV=server → 按 OPP_SERVER_NAME（154/245）用 /opt/kaixuan/openpocket
#   4) 若 OPP_SERVER_NAME 未指定，回退到默认 Linux /opt/kaixuan/openpocket
if [[ -z "${DEPLOY_BASE_DIR:-}" ]]; then
  case "${DEPLOY_ENV}" in
    local)
      DEPLOY_BASE_DIR="$(os_detect_base_dir "${HOME:-/tmp}")"
      ;;
    server)
      : "${OPP_SERVER_NAME:=}"
      if [[ -n "${OPP_SERVER_NAME}" ]]; then
        DEPLOY_BASE_DIR="/opt/kaixuan/openpocket"
      else
        # 没有 server 名但走 server 环境：回退到 Linux 默认
        DEPLOY_BASE_DIR="/opt/kaixuan/openpocket"
      fi
      ;;
    *)
      echo "[env.sh] unknown DEPLOY_ENV='${DEPLOY_ENV}' (expected: local|server)" >&2
      return 1 2>/dev/null || exit 1
      ;;
  esac
fi
export DEPLOY_BASE_DIR

# 旧路径兼容：若 DEPLOY_BASE_DIR 未设过且存在 ~/Downloads/kaixuan/opp 且里面
# 有 .last-healthy 等已部署过的痕迹，发出一次性迁移提示（不强制改路径）。
# 这一行只在 macOS 上有意义（默认已切到 ~/kaixuan/openpocket）。
if [[ "${OPP_OS_KIND}" == "darwin" && "${DEPLOY_BASE_DIR}" == "${HOME}/kaixuan/openpocket" ]]; then
  if [[ -d "${HOME}/Downloads/kaixuan/opp" && ! -f "${DEPLOY_BASE_DIR}/.migrated-from-old-path" ]]; then
    if [[ -f "${HOME}/Downloads/kaixuan/opp/logs/.last-healthy" ]]; then
      echo "[env.sh] ⚠️  检测到旧部署 ${HOME}/Downloads/kaixuan/opp；新默认在 ${DEPLOY_BASE_DIR}" >&2
      echo "        如需继续使用旧路径：DEPLOY_BASE_DIR=${HOME}/Downloads/kaixuan/opp $0" >&2
      echo "        或迁移：mv ${HOME}/Downloads/kaixuan/opp ${DEPLOY_BASE_DIR}" >&2
    fi
  fi
fi

# compose project / 容器名后缀：按 DEPLOY_BASE_DIR 末段派生。
# 同机多套部署（正式 + 临时测试）即使同一 DEPLOY_ENV，也不同 project，
# 避免 up --force-recreate 互相踩容器。正式目录 ~/kaixuan/openpocket → -openpocket。
# 服务器（154/245）则用 OPP_SERVER_NAME 直接派生后缀，保证两机 compose
# project 名不会因 base-dir 重名而互踩。
if [[ "${DEPLOY_ENV}" == "server" && -n "${OPP_SERVER_NAME}" ]]; then
  OPP_NAME_SUFFIX="${OPP_SERVER_NAME}"
else
  OPP_NAME_SUFFIX="$(basename "${DEPLOY_BASE_DIR}" | tr -c 'a-zA-Z0-9-' '-' | sed 's/^-*//;s/-*$//' | cut -c1-12)"
fi
# 目录名全为非 ASCII 等情况下降级为 DEPLOY_ENV，避免出现 "--" 类怪后缀
[[ -n "${OPP_NAME_SUFFIX}" ]] || OPP_NAME_SUFFIX="${DEPLOY_ENV}"
export OPP_NAME_SUFFIX
export OPP_CONTAINER_SUFFIX="-${OPP_NAME_SUFFIX}"
: "${POCKET_PROJECT_NAME:=opencode-pocket-${DEPLOY_ENV}${OPP_CONTAINER_SUFFIX}}"
export POCKET_PROJECT_NAME

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

# ── 1.6 数据库部署开关（用户需求：仅当本机被指派为该 DB 宿主时生成） ──
#   OPP_DEPLOY_PG / OPP_DEPLOY_REDIS / OPP_DEPLOY_MYSQL  三选一：true / false / external
#     true    → 若未发现外部实例，本机容器化起一个（数据持久化到 ${DEPLOY_BASE_DIR}/<db>/）
#     false   → 不在本机起；DSN 由 deploy-{local,154,245}.sh 注入远端（如 252）
#     external→ 强制走外部（不探测，DSN 直接由 .env 提供）
#   默认：
#     deploy-local.sh  → OPP_DEPLOY_PG=true,  其余=false
#     deploy-154.sh    → 三个都=false（PG/Redis 都在 252）
#     deploy-245.sh    → 三个都=false（PG/Redis 都在 252）
: "${OPP_DEPLOY_PG:=false}"
: "${OPP_DEPLOY_REDIS:=false}"
: "${OPP_DEPLOY_MYSQL:=false}"
export OPP_DEPLOY_PG OPP_DEPLOY_REDIS OPP_DEPLOY_MYSQL
export OPP_PG_MODE="${OPP_PG_MODE:-uninit}"   # uninit|external|container|remote-required
export OPP_REDIS_MODE="${OPP_REDIS_MODE:-uninit}"
export OPP_MYSQL_MODE="${OPP_MYSQL_MODE:-uninit}"

# ── 1.7 blue-green 发布开关 ──────────────────────────────────────
#   OPP_VERSION_BUILD        当前活跃版本目录名（如 pocket-opp-p7f3a1b-20260903123045）
#   OPP_PREVIOUS_BUILD       上一个 verified 版本（用于回滚）
#   OPP_DEPLOY_VERSION       显式版本号（如 1.2.3）
#   OPP_DEPLOY_BUILD         显式 build 号（如 005）
export OPP_VERSION_BUILD="${OPP_VERSION_BUILD:-}"
export OPP_PREVIOUS_BUILD="${OPP_PREVIOUS_BUILD:-}"
export OPP_DEPLOY_VERSION="${OPP_DEPLOY_VERSION:-}"
export OPP_DEPLOY_BUILD="${OPP_DEPLOY_BUILD:-}"

# 252 连接信息（tunnel-252.sh 用；密码不入库，从 OPP_PG_PASSWORD /
# SSHPASS 环境变量或本地未跟踪文件读取）
export OPP_252_SSH_HOST="${OPP_252_SSH_HOST:-115.29.212.252}"
export OPP_252_SSH_PORT="${OPP_252_SSH_PORT:-25022}"
export OPP_252_SSH_USER="${OPP_252_SSH_USER:-root}"
export OPP_252_PG_INTERNAL_HOST="${OPP_252_PG_INTERNAL_HOST:-172.16.2.210}"

# ── 2. 子目录派生（支持单点覆盖） ───────────────────────────────
# 没显式给 POCKET_*_DIR 时，按 ${DEPLOY_BASE_DIR}/<sub> 拼接。
# 目录清单（2026-09-03 重构）：用户需求中明确列出 attachments/ bin/ backups/
# logs/ raw-logs/ run/ 六个核心目录，外加既有 data/ config/ images/。
# postgres/ redis/ mysql/ 三个 DB 目录由 init-dirs.sh 在 OPP_DEPLOY_*=true 时创建。
: "${POCKET_BASE_DIR:=${DEPLOY_BASE_DIR}}"
: "${POCKET_DATA_DIR:=${DEPLOY_BASE_DIR}/data}"
: "${POCKET_LOG_DIR:=${DEPLOY_BASE_DIR}/logs}"
: "${POCKET_RAW_LOG_DIR:=${DEPLOY_BASE_DIR}/raw-logs}"
: "${POCKET_CONFIG_DIR:=${DEPLOY_BASE_DIR}/config}"
: "${POCKET_IMAGE_DIR:=${DEPLOY_BASE_DIR}/images}"
: "${POCKET_BACKUP_DIR:=${DEPLOY_BASE_DIR}/backups}"
: "${POCKET_ATTACHMENTS_DIR:=${DEPLOY_BASE_DIR}/attachments}"
: "${POCKET_RUN_DIR:=${DEPLOY_BASE_DIR}/run}"
: "${POCKET_BIN_DIR:=${DEPLOY_BASE_DIR}/bin}"
# 条件 DB 数据目录（容器化部署 PG/Redis/MySQL 时使用）。未启用时不强求存在。
: "${POCKET_PG_DATA_DIR:=${DEPLOY_BASE_DIR}/postgres}"
: "${POCKET_REDIS_DATA_DIR:=${DEPLOY_BASE_DIR}/redis}"
: "${POCKET_MYSQL_DATA_DIR:=${DEPLOY_BASE_DIR}/mysql}"
export POCKET_BASE_DIR POCKET_DATA_DIR POCKET_LOG_DIR POCKET_RAW_LOG_DIR \
       POCKET_CONFIG_DIR POCKET_IMAGE_DIR POCKET_BACKUP_DIR \
       POCKET_ATTACHMENTS_DIR POCKET_RUN_DIR POCKET_BIN_DIR \
       POCKET_PG_DATA_DIR POCKET_REDIS_DATA_DIR POCKET_MYSQL_DATA_DIR

# ── 2.5 服务器识别（154 / 245） ──────────────────────────────────
# deploy-154.sh / deploy-245.sh 在 source env.sh 之前 export OPP_SERVER_NAME
# 用于差异化绑定 IP、端口、env 文件命名。
export OPP_SERVER_NAME="${OPP_SERVER_NAME:-}"
case "${OPP_SERVER_NAME}" in
  154|245|252) ;;
  "") ;;
  *) echo "[env.sh] WARN: 未知 OPP_SERVER_NAME='${OPP_SERVER_NAME}'（仅 154/245/252 走 server 流程）" >&2 ;;
esac

# ── 3. 端口 & compose project（可按环境覆盖） ────────────────────
# 端口定稿（2026-08-31）：8088 不再使用，后端宿主端口统一默认 8090。
# 另：252 上 kxpms-cert-manager 常驻 127.0.0.1:8090（loopback），
# pocketd 的宿主端口须绑 eth0 内网 IP 规避冲突（与 pg-252-pg17 同款绑法），
# 公网/内网访问均经 172.16.2.210:8090。本地默认 0.0.0.0 全接口。
#
# 154/245 重构（2026-09-03）：154/245 都是独立服务器，各自绑自己的 eth0 IP；
# 端口 154 用 8090，245 用 8091（避免二者在不同机器但本地端口冲突无意义，
# 但若在同一台 jump 机上做演练也能区分）。
if [[ "${DEPLOY_ENV}" == "server" ]]; then
  case "${OPP_SERVER_NAME}" in
    154)
      : "${POCKET_PORT_BIND_IP:=172.16.2.154}"
      : "${POCKET_HTTP_PORT:=8090}"
      : "${POCKET_FRONTEND_PORT:=4175}"
      ;;
    245)
      : "${POCKET_PORT_BIND_IP:=172.16.2.245}"
      : "${POCKET_HTTP_PORT:=8091}"
      : "${POCKET_FRONTEND_PORT:=4176}"
      ;;
    252)
      # 252 是后端 API 宿主 + PG 宿主（DSN 走 172.16.2.210:5432 本机内网）；
      # 端口 8092 / 4177 与 154 (8090/4175)、245 (8091/4176) 错开。
      : "${POCKET_PORT_BIND_IP:=172.16.2.252}"
      : "${POCKET_HTTP_PORT:=8092}"
      : "${POCKET_FRONTEND_PORT:=4177}"
      ;;
    *)
      # 旧 deploy-252 的兼容路径（无 OPP_SERVER_NAME 但 DEPLOY_ENV=server 时）
      : "${POCKET_PORT_BIND_IP:=172.16.2.210}"
      : "${POCKET_HTTP_PORT:=8090}"
      : "${POCKET_FRONTEND_PORT:=4175}"
      ;;
  esac
else
  : "${POCKET_PORT_BIND_IP:=0.0.0.0}"
  : "${POCKET_HTTP_PORT:=8090}"
  : "${POCKET_FRONTEND_PORT:=4175}"
fi
export POCKET_HTTP_PORT POCKET_FRONTEND_PORT POCKET_PORT_BIND_IP
# 健康探测主机：绑定了具体 IP 时 localhost 探不到，用绑定 IP 本身
if [[ "${POCKET_PORT_BIND_IP}" != "0.0.0.0" ]]; then
  export POCKET_HTTP_PROBE_HOST="${POCKET_PORT_BIND_IP}"
else
  export POCKET_HTTP_PROBE_HOST="localhost"
fi
# 别名：acc-integration compose 用 *_HOST_PORT 命名，这里同步导出，
# 方便同一批变量在两套 compose 间复用。
export POCKET_HOST_PORT="${POCKET_HOST_PORT:-${POCKET_HTTP_PORT}}"
export POCKET_FRONTEND_HOST_PORT="${POCKET_FRONTEND_HOST_PORT:-${POCKET_FRONTEND_PORT}}"

# env_file 在 config/ 下，命名按 DEPLOY_ENV + OPP_SERVER_NAME 区分，避免本地/服务器互踩
#   local  → .env.local
#   server + 154 → .env.154
#   server + 245 → .env.245
#   server + 252 → .env.252
#   server + (未指定 server) → .env.server（兼容旧 deploy-252）
case "${DEPLOY_ENV}" in
  local)
    : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.local}"
    ;;
  server)
    case "${OPP_SERVER_NAME}" in
      154) : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.154}" ;;
      245) : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.245}" ;;
      252) : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.252}" ;;
      *)   : "${POCKET_ENV_FILE:=${POCKET_CONFIG_DIR}/.env.server}" ;;
    esac
    ;;
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

# ── 5. pocketd 容器内路径 ─────────────────────────────────────────
# （compose 已直接写 /app/data，无需变量注入）

# ── 5.5 共享 helper：HTTP 健康探测（curl 优先，无则 wget） ─────────
# 252 最小安装可能只有其一；两者皆无时降级为放行并告警。
http_ok() {
  local url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -sf "${url}" >/dev/null 2>&1
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O /dev/null "${url}" 2>/dev/null
  else
    echo "⚠️  无 curl/wget，跳过 HTTP 探测: ${url}" >&2
    return 0
  fi
}

# ── 6. 调试输出（可选） ──────────────────────────────────────────
if [[ "${POCKET_ENV_DEBUG:-0}" == "1" || "${OPP_DEBUG:-0}" == "1" ]]; then
  cat <<EOF
[env.sh] OS_KIND=${OPP_OS_KIND}
[env.sh] DEPLOY_ENV=${DEPLOY_ENV}
[env.sh] OPP_SERVER_NAME=${OPP_SERVER_NAME:-}
[env.sh] DEPLOY_BASE_DIR=${DEPLOY_BASE_DIR}
[env.sh] POCKET_DATA_DIR=${POCKET_DATA_DIR}
[env.sh] POCKET_LOG_DIR=${POCKET_LOG_DIR}
[env.sh] POCKET_RAW_LOG_DIR=${POCKET_RAW_LOG_DIR}
[env.sh] POCKET_ATTACHMENTS_DIR=${POCKET_ATTACHMENTS_DIR}
[env.sh] POCKET_BIN_DIR=${POCKET_BIN_DIR}
[env.sh] POCKET_RUN_DIR=${POCKET_RUN_DIR}
[env.sh] POCKET_CONFIG_DIR=${POCKET_CONFIG_DIR}
[env.sh] POCKET_ENV_FILE=${POCKET_ENV_FILE}
[env.sh] POCKET_COMPOSE_FILE=${POCKET_COMPOSE_FILE}
[env.sh] POCKET_HTTP_PORT=${POCKET_HTTP_PORT}
[env.sh] POCKET_FRONTEND_PORT=${POCKET_FRONTEND_PORT}
[env.sh] POCKET_PORT_BIND_IP=${POCKET_PORT_BIND_IP}
[env.sh] PG = ${OPP_PG_USER}@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB} (schema=${OPP_PG_SCHEMA})
[env.sh] OPP_DEPLOY_PG=${OPP_DEPLOY_PG}  OPP_PG_MODE=${OPP_PG_MODE}
[env.sh] OPP_DEPLOY_REDIS=${OPP_DEPLOY_REDIS}  OPP_REDIS_MODE=${OPP_REDIS_MODE}
[env.sh] OPP_DEPLOY_MYSQL=${OPP_DEPLOY_MYSQL}  OPP_MYSQL_MODE=${OPP_MYSQL_MODE}
[env.sh] OPP_VERSION_BUILD=${OPP_VERSION_BUILD:-<unset>}
[env.sh] OPP_PREVIOUS_BUILD=${OPP_PREVIOUS_BUILD:-<unset>}
EOF
fi
