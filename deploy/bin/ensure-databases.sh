#!/usr/bin/env bash
# =====================================================================
# ensure-databases.sh — OpenPocket 部署：DB 复用 vs 容器化决策
#
# 输入：
#   OPP_DEPLOY_PG / OPP_DEPLOY_REDIS / OPP_DEPLOY_MYSQL  (true/false/external)
#   OPP_PG_HOST / OPP_PG_PORT / OPP_REDIS_HOST / ...     (DB 目标)
#   DEPLOY_BASE_DIR                                       (本机 base dir)
#
# 行为：
#   1. 对每个 DB 调 database-detect.sh 探测外部实例
#   2. 命中 → mode=external/remote/local-port，DSN 已存在
#   3. 未命中 + OPP_DEPLOY_<DB>=true → 容器化起一个（数据持久化到
#      ${DEPLOY_BASE_DIR}/<db>/），写 DSN 到 .env
#   4. 未命中 + OPP_DEPLOY_<DB>=false → mode=remote-required，
#      警告 DSN 必须在 .env 中由用户/上游脚本注入
#   5. OPP_DEPLOY_<DB>=external → 不探测，DSN 必须 .env 已存在
#
# 输出（写入 .env，不覆盖已有值）：
#   POCKET_POSTGRES_DSN  POCKET_REDIS_URL  POCKET_MYSQL_DSN
#
# 用法：
#   source deploy/bin/env.sh
#   ./deploy/bin/ensure-databases.sh
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"
# shellcheck disable=SC1091
source "${LIB_DIR}/database-detect.sh"

mkdir -p "${POCKET_CONFIG_DIR}" "${POCKET_RUN_DIR}"

# 写 env 文件的工具（不覆盖已有键）
_env_ensure_key() {
  local key="$1" value="$2" file="${3:-${POCKET_ENV_FILE}}"
  [[ -n "${value}" ]] || return 0
  [[ -f "${file}" ]] || : > "${file}"
  chmod 600 "${file}"
  # 已存在则跳过（不强制覆盖）
  if grep -qE "^${key}=" "${file}"; then
    return 0
  fi
  printf '%s=%s\n' "${key}" "${value}" >> "${file}"
}

# 把检测结果写到 stdout + 设为 OPP_*_MODE
_ensure_db() {
  local name="$1" detect_func="$2" host_var="$3" port_var="$4" \
        deploy_flag="$5" data_dir="$6" image="$7" default_port="$8"
  local mode="${name^^}_MODE"

  local deploy_value="${!deploy_flag:-false}"
  local host="${!host_var:-127.0.0.1}"
  local port="${!port_var:-${default_port}}"

  echo "━━━ ${name}: deploy=${deploy_value} target=${host}:${port} ━━━"

  # OPP_DEPLOY_*=external：跳过探测，由调用方保证 DSN 在 .env 中
  if [[ "${deploy_value}" == "external" ]]; then
    export "${mode}=external"
    echo "  ⏭  ${name}: 强制 external 模式，跳过探测（DSN 由 .env 提供）"
    return 0
  fi

  # 探测
  local hit
  if hit="$("${detect_func}" 2>/dev/null)"; then
    local hit_mode hit_host hit_port
    IFS=':' read -r hit_mode hit_host hit_port <<<"${hit}"
    # 命中即归一到 external（具体是 docker/system/remote/local-port 都视为外部实例）
    # 上游 deploy 脚本只需要"是不是外部、要不要容器化"两个信号；具体来源由日志保留。
    export "${mode}=external"
    echo "  ✅ ${name}: 命中外部实例 (${hit_mode} @ ${hit_host}:${hit_port})；不创建新实例"
    return 0
  fi

  # 未命中
  if [[ "${deploy_value}" == "true" ]]; then
    echo "  🆕 ${name}: 未发现外部实例，按 OPP_DEPLOY_${name^^}=true 起容器化实例"
    mkdir -p "${data_dir}"
    _start_container_db "${name}" "${image}" "${data_dir}" "${host}" "${port}"
    export "${mode}=container"
    return 0
  fi

  # false / 未设置：不创建
  export "${mode}=remote-required"
  echo "  ⚠️  ${name}: 未发现外部实例且 OPP_DEPLOY_${name^^}=false；"
  echo "      期望 DSN 由 .env 或上游 deploy 脚本注入远端（如 252）"
  return 0
}

# 启动容器化 DB（调用 docker compose -f docker-compose.db.yml）
_start_container_db() {
  local name="$1" image="$2" data_dir="$3" host="$4" port="$5"
  local svc="${name}"
  local compose_file="${SCRIPT_DIR}/docker-compose.db.yml"

  command -v docker >/dev/null 2>&1 || {
    echo "  ❌ docker 未安装，无法容器化 ${name}" >&2
    return 1
  }

  # 起对应 service
  local project="opp-db-${name}"
  DOCKER_DB_HOST="${host}" \
  DOCKER_DB_PORT="${port}" \
  DOCKER_DB_IMAGE="${image}" \
  DOCKER_DB_DATA_DIR="${data_dir}" \
  docker compose -p "${project}" -f "${compose_file}" up -d "${svc}" || {
    echo "  ❌ 容器化 ${name} 失败" >&2
    return 1
  }

  echo "  ✅ ${name} 容器已起 (port=${port}, data=${data_dir})"

  # 写 DSN 到 .env（容器化路径要求 openssl 在场以生成强密码）
  if ! command -v openssl >/dev/null 2>&1; then
    echo "  ❌ 容器化 ${name} 需要 openssl 生成随机密码，当前不可用" >&2
    echo "     安装 openssl（macOS: brew install openssl；Linux: apt/yum install openssl）" >&2
    return 1
  fi
  case "${name}" in
    postgres)
      local pass="${OPP_PG_PASSWORD:-$(openssl rand -hex 16)}"
      _env_ensure_key "OPP_PG_PASSWORD" "${pass}"
      _env_ensure_key "POCKET_POSTGRES_DSN" \
        "postgresql://${OPP_PG_USER:-llm_gateway}:${pass}@${host}:${port}/${OPP_PG_DB:-pocket}?sslmode=disable"
      ;;
    redis)
      _env_ensure_key "POCKET_REDIS_URL" "redis://${host}:${port}/0"
      ;;
    mysql)
      local root_pass="${OPP_MYSQL_PASSWORD:-$(openssl rand -hex 16)}"
      _env_ensure_key "OPP_MYSQL_PASSWORD" "${root_pass}"
      _env_ensure_key "POCKET_MYSQL_DSN" \
        "mysql://root:${root_pass}@${host}:${port}/${OPP_MYSQL_DB:-openpocket}"
      ;;
  esac
}

# 主流程
ensure_pg()      { _ensure_db postgres detect_pg_external      OPP_PG_HOST      OPP_PG_PORT      OPP_DEPLOY_PG      "${POCKET_PG_DATA_DIR}"      "postgres:17" 5432 ; }
ensure_redis()   { _ensure_db redis    detect_redis_external   OPP_REDIS_HOST   OPP_REDIS_PORT   OPP_DEPLOY_REDIS   "${POCKET_REDIS_DATA_DIR}"   "redis:7"     6379 ; }
ensure_mysql()   { _ensure_db mysql    detect_mysql_external   OPP_MYSQL_HOST   OPP_MYSQL_PORT   OPP_DEPLOY_MYSQL   "${POCKET_MYSQL_DATA_DIR}"   "mysql:8"     3306 ; }

# 按开关依次执行（只有 true 或 external 才跑 detect / 容器化；false/unset 跳过）
[[ "${OPP_DEPLOY_PG}" == "true" || "${OPP_DEPLOY_PG}" == "external" ]]      && ensure_pg      || true
[[ "${OPP_DEPLOY_REDIS}" == "true" || "${OPP_DEPLOY_REDIS}" == "external" ]] && ensure_redis   || true
[[ "${OPP_DEPLOY_MYSQL}" == "true" || "${OPP_DEPLOY_MYSQL}" == "external" ]] && ensure_mysql   || true

# 重新 export 状态供后续脚本读取
export OPP_PG_MODE OPP_REDIS_MODE OPP_MYSQL_MODE

echo
echo "━━━ ensure-databases 总结 ━━━"
printf '  PG    : mode=%-18s  target=%s:%s  deploy=%s\n' \
  "${OPP_PG_MODE}" "${OPP_PG_HOST}" "${OPP_PG_PORT}" "${OPP_DEPLOY_PG}"
printf '  Redis : mode=%-18s  target=%s:%s  deploy=%s\n' \
  "${OPP_REDIS_MODE}" "${OPP_REDIS_HOST:-127.0.0.1}" "${OPP_REDIS_PORT:-6379}" "${OPP_DEPLOY_REDIS}"
printf '  MySQL : mode=%-18s  target=%s:%s  deploy=%s\n' \
  "${OPP_MYSQL_MODE}" "${OPP_MYSQL_HOST:-127.0.0.1}" "${OPP_MYSQL_PORT:-3306}" "${OPP_DEPLOY_MYSQL}"
