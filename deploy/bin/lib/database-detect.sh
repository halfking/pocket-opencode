#!/usr/bin/env bash
# =====================================================================
# database-detect.sh — OpenPocket 部署的数据库复用检测库
#
# 设计目标（用户需求）：
#   - 如果系统已有 PG/Redis/MySQL（无论在 docker 还是 systemd / 自建），
#     就直接使用，不创建新实例。
#   - 仅当显式 OPP_DEPLOY_PG=true 且检测不到任何外部实例时才容器化起一个。
#   - 154/245 部署默认 PG/Redis 在 252，detect 命中 252 内网后跳过本地创建。
#
# 公开函数：
#   detect_pg_external            → 0=命中（打印 mode: docker|system|remote|local-port）
#                                   1=未命中
#   detect_redis_external         → 同上
#   detect_mysql_external         → 同上
#   detect_pg_host [host] [port]  → 命中且可达返回 host:port，未命中返回 1
#
# 受 OPP_DEBUG=1 控制额外日志输出。
# 可被 mock（测试用）：定义 OPP_DOCKER_PS_HIT 即可命中 docker 路径。
# =====================================================================

if [[ -n "${__OPP_DB_DETECT_LOADED:-}" ]]; then
  return 0 2>/dev/null || true
fi
__OPP_DB_DETECT_LOADED=1

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/os-detect.sh"

_db_log() {
  [[ "${OPP_DEBUG:-0}" == "1" ]] && echo "[db-detect] $*" >&2 || true
}

# TCP 端口探测，3s 超时
_db_port_open() {
  local host="$1" port="$2"
  if command -v nc >/dev/null 2>&1; then
    nc -z -G 3 "${host}" "${port}" >/dev/null 2>&1
  else
    timeout 3 bash -c "</dev/tcp/${host}/${port}" >/dev/null 2>&1
  fi
}

# 探测 docker 中是否有名为 *<keyword>* 的运行中容器
_db_docker_container_running() {
  local keyword="$1"
  if [[ -n "${OPP_DOCKER_PS_HIT:-}" ]]; then
    _db_log "mock: docker hit for ${keyword}"
    return 0
  fi
  command -v docker >/dev/null 2>&1 || return 1
  docker ps --format '{{.Names}}' 2>/dev/null \
    | grep -iE "(^|[^a-z])${keyword}([^a-z]|$)" >/dev/null
}

# 探测 systemd 是否有运行中的服务
_db_systemd_service_active() {
  local svc="$1"
  if [[ -n "${OPP_SYSTEMCTL_HIT:-}" ]]; then
    _db_log "mock: systemctl hit for ${svc}"
    return 0
  fi
  command -v systemctl >/dev/null 2>&1 || return 1
  systemctl is-active "${svc}" >/dev/null 2>&1
}

# ── 登录验证（deploy-lib/reuse-probe.sh SSOT，可用则增强）─────────
# "有服务"还不够，必须"能登录访问"才算可复用：docker 容器命中后用容器内
# 握手（psql SELECT 1 / redis-cli PING / mysqladmin ping）二次验证。
# SSOT 缺失时退回旧语义（仅容器名/端口命中），不阻断部署。
if [[ -z "${__OPP_DB_DETECT_SOURCED_REUSE:-}" ]]; then
  if [[ -z "${AIAN_DEPLOY_LIB:-}" ]]; then
    _opp_lib="$HOME/workspace/ai-native-tools/deploy-lib"
    [[ -d "$_opp_lib" ]] && AIAN_DEPLOY_LIB="$_opp_lib"
  fi
  if [[ -n "${AIAN_DEPLOY_LIB:-}" && -f "$AIAN_DEPLOY_LIB/reuse-probe.sh" ]]; then
    # shellcheck source=../../../../../deploy-lib/reuse-probe.sh
    source "$AIAN_DEPLOY_LIB/reuse-probe.sh"
    _OPP_REUSE_PROBE=1
  else
    _OPP_REUSE_PROBE=0
  fi
  __OPP_DB_DETECT_SOURCED_REUSE=1
fi

# _db_docker_login_verified <keyword> — docker 命中后逐容器登录验证
# 找到第一个 image 匹配 keyword 且容器内握手成功的容器名；无 SSOT 恒成功。
_db_docker_login_verified() {
  local keyword="$1"
  [[ "${_OPP_REUSE_PROBE:-0}" == "1" ]] || return 0
  local line cname image login_fn
  case "$keyword" in
    postgres) login_fn=_rp_pg_login_container ;;
    redis)    login_fn=_rp_redis_login_container ;;
    mysql)    login_fn=_rp_mysql_login_container ;;
    *) return 0 ;;
  esac
  command -v docker >/dev/null 2>&1 || return 1
  while IFS=$'\t' read -r cname image; do
    [[ -z "$cname" ]] && continue
    if [[ "$image" =~ (^|/)${keyword} ]]; then
      if "$login_fn" "$cname"; then
        _db_log "login verified for container ${cname} (${keyword})"
        return 0
      fi
      _db_log "container ${cname} running but login failed; keep scanning"
    fi
  done < <(docker ps --format '{{.Names}}\t{{.Image}}' 2>/dev/null)
  return 1
}

# loopback 兜底探测：配置的 host 是别名（如 host.docker.internal）时，
# 宿主侧可能不可解析/不可达（macOS Docker Desktop 新版不再写 /etc/hosts），
# 补探 127.0.0.1 避免本机已有实例被漏检、误走容器化路径撞端口。
_db_loopback_fallback() {
  local host="$1" port="$2" label="$3"
  [[ "${host}" == "127.0.0.1" || "${host}" == "localhost" || -z "${host}" ]] && return 1
  if _db_port_open "127.0.0.1" "${port}"; then
    printf 'local-port:127.0.0.1:%s\n' "${port}"
    _db_log "${label}: loopback fallback hit at 127.0.0.1:${port}"
    return 0
  fi
  return 1
}

# ── PostgreSQL ─────────────────────────────────────────────────────
# 返回 0=命中；stdout 写 "mode:host:port"，例如 "docker:127.0.0.1:5432" /
# "system:127.0.0.1:5432" / "local-port:127.0.0.1:5432" / "remote:172.16.2.210:5432"
detect_pg_external() {
  local host="${OPP_PG_HOST:-127.0.0.1}"
  local port="${OPP_PG_PORT:-5432}"

  # 1) docker 容器（pg / postgres / postgresql 都算；命中后登录验证）
  if _db_docker_container_running "postgres"; then
    if _db_docker_login_verified "postgres"; then
      printf 'docker:%s:%s\n' "${host}" "${port}"
      _db_log "PG: docker container matched + login verified (host=${host} port=${port})"
      return 0
    fi
    _db_log "PG: docker containers matched but none passed login; fallthrough"
  fi

  # 2) systemd 服务
  if _db_systemd_service_active "postgresql"; then
    printf 'system:%s:%s\n' "${host}" "${port}"
    _db_log "PG: systemd postgresql active"
    return 0
  fi

  # 3) 端口探测（最宽松，可能误命中；但端口可达意味着有东西在监听）
  if _db_port_open "${host}" "${port}"; then
    # 区分「本机端口」与「远端端口」：host 非 loopback 视为 remote
    if [[ "${host}" == "127.0.0.1" || "${host}" == "localhost" || -z "${host}" ]]; then
      printf 'local-port:%s:%s\n' "${host}" "${port}"
    else
      printf 'remote:%s:%s\n' "${host}" "${port}"
    fi
    _db_log "PG: port reachable at ${host}:${port}"
    return 0
  fi

  _db_loopback_fallback "${host}" "${port}" "PG"
}

# ── Redis ─────────────────────────────────────────────────────────
detect_redis_external() {
  local host="${OPP_REDIS_HOST:-127.0.0.1}"
  local port="${OPP_REDIS_PORT:-6379}"

  if _db_docker_container_running "redis"; then
    if _db_docker_login_verified "redis"; then
      printf 'docker:%s:%s\n' "${host}" "${port}"
      _db_log "Redis: docker container matched + login verified"
      return 0
    fi
    _db_log "Redis: docker containers matched but none passed login; fallthrough"
  fi

  if _db_systemd_service_active "redis" || _db_systemd_service_active "redis-server"; then
    printf 'system:%s:%s\n' "${host}" "${port}"
    return 0
  fi

  if _db_port_open "${host}" "${port}"; then
    if [[ "${host}" == "127.0.0.1" || "${host}" == "localhost" || -z "${host}" ]]; then
      printf 'local-port:%s:%s\n' "${host}" "${port}"
    else
      printf 'remote:%s:%s\n' "${host}" "${port}"
    fi
    return 0
  fi

  _db_loopback_fallback "${host}" "${port}" "Redis"
}

# ── MySQL ─────────────────────────────────────────────────────────
detect_mysql_external() {
  local host="${OPP_MYSQL_HOST:-127.0.0.1}"
  local port="${OPP_MYSQL_PORT:-3306}"

  if _db_docker_container_running "mysql"; then
    if _db_docker_login_verified "mysql"; then
      printf 'docker:%s:%s\n' "${host}" "${port}"
      _db_log "MySQL: docker container matched + login verified"
      return 0
    fi
    _db_log "MySQL: docker containers matched but none passed login; fallthrough"
  fi

  if _db_systemd_service_active "mysql" || _db_systemd_service_active "mysqld"; then
    printf 'system:%s:%s\n' "${host}" "${port}"
    return 0
  fi

  if _db_port_open "${host}" "${port}"; then
    if [[ "${host}" == "127.0.0.1" || "${host}" == "localhost" || -z "${host}" ]]; then
      printf 'local-port:%s:%s\n' "${host}" "${port}"
    else
      printf 'remote:%s:%s\n' "${host}" "${port}"
    fi
    return 0
  fi

  _db_loopback_fallback "${host}" "${port}" "MySQL"
}

# ── 综合判断：给定 host:port 是否真在跑 PG（用 psql 真握手验证）──
detect_pg_host() {
  local host="${1:-127.0.0.1}"
  local port="${2:-5432}"
  _db_port_open "${host}" "${port}" || return 1
  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="${OPP_PG_PASSWORD:-}" psql -h "${host}" -p "${port}" \
      -U "${OPP_PG_USER:-postgres}" -d postgres \
      -tAc "SELECT 1" >/dev/null 2>&1 || return 1
  fi
  printf '%s:%s' "${host}" "${port}"
  return 0
}
