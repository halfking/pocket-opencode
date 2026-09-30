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
#   detect_pg_external            → 0=命中（打印 mode: remote|local-port）
#                                   1=未命中
#   detect_redis_external         → 同上
#   detect_mysql_external         → 同上
#   detect_pg_host [host] [port]  → 命中且可达返回 host:port，未命中返回 1
#
# 受 OPP_DEBUG=1 控制额外日志输出。
# 协议探测不证明目标库/schema/权限；后端启动前必须运行 check-databases.py。
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
    if [[ "$(uname -s)" == Darwin ]]; then
      nc -z -G 3 "${host}" "${port}" >/dev/null 2>&1
    else
      nc -z -w 3 "${host}" "${port}" >/dev/null 2>&1
    fi
  else
    timeout 3 bash -c "</dev/tcp/${host}/${port}" >/dev/null 2>&1
  fi
}

# Bound network probes even when a CLI has no native connect timeout.
_db_probe() {
  python3 -c 'import subprocess,sys
try:
 r=subprocess.run(sys.argv[1:],capture_output=True,text=True,timeout=5)
 print(r.stdout,end="");sys.exit(r.returncode)
except (OSError,subprocess.TimeoutExpired):sys.exit(1)' "$@"
}

# Only the configured endpoint is a reuse candidate. A similarly named Docker
# container or an open TCP port cannot prove that this DSN reaches that service.
_db_protocol_ready() {
  local kind="$1" host="$2" port="$3" reply=""
  case "$kind" in
    postgres)
      local pg_ready
      pg_ready="$(command -v pg_isready || true)"
      if [[ -z "$pg_ready" ]]; then
        for pg_ready in /opt/homebrew/opt/libpq/bin/pg_isready /usr/local/opt/libpq/bin/pg_isready; do
          [[ ! -x "$pg_ready" ]] || break
        done
      fi
      if [[ -x "$pg_ready" ]]; then
        reply=$(_db_probe "$pg_ready" -h "$host" -p "$port" -t 3 2>/dev/null || true)
        [[ "$reply" == *"accepting connections"* ]]
      elif command -v psql >/dev/null 2>&1; then
        PGPASSWORD="${OPP_PG_PASSWORD:-}" PGCONNECT_TIMEOUT=3 psql -h "$host" -p "$port" \
          -U "${OPP_PG_USER:-postgres}" -d "${OPP_PG_DB:-postgres}" \
          -tAc 'SELECT 1' >/dev/null 2>&1
      else
        return 1
      fi
      ;;
    redis)
      command -v redis-cli >/dev/null 2>&1 || return 1
      reply=$(REDISCLI_AUTH="${OPP_REDIS_PASSWORD:-${REUSE_REDIS_PASSWORD:-}}" \
        _db_probe redis-cli -h "$host" -p "$port" --no-auth-warning PING 2>/dev/null || true)
      [[ "$reply" == PONG ]]
      ;;
    mysql)
      if command -v mysqladmin >/dev/null 2>&1; then
        reply=$(MYSQL_PWD="${OPP_MYSQL_PASSWORD:-}" _db_probe mysqladmin --connect-timeout=3 \
          -h "$host" -P "$port" -u "${OPP_MYSQL_USER:-root}" ping 2>/dev/null || true)
        [[ "$reply" == "mysqld is alive" ]]
      elif command -v mysql >/dev/null 2>&1; then
        MYSQL_PWD="${OPP_MYSQL_PASSWORD:-}" _db_probe mysql --connect-timeout=3 -h "$host" -P "$port" -u "${OPP_MYSQL_USER:-root}" \
          -e 'SELECT 1' >/dev/null 2>&1
      else
        return 1
      fi
      ;;
    *) return 1 ;;
  esac
}

_db_detect_configured() {
  local kind="$1" host="$2" port="$3" mode="remote"
  [[ "$host" == "127.0.0.1" || "$host" == "localhost" ]] && mode="local-port"
  if _db_protocol_ready "$kind" "$host" "$port"; then
    printf '%s:%s:%s\n' "$mode" "$host" "$port"
    return 0
  fi
  # Docker Desktop exposes the container host alias inside containers, but the
  # macOS host itself may not resolve that alias. Verify the same published port
  # on loopback; never fall back to an unrelated remote target.
  if [[ "$host" == "host.docker.internal" ]] && \
      _db_protocol_ready "$kind" 127.0.0.1 "$port"; then
    printf 'local-port:127.0.0.1:%s\n' "$port"
    return 0
  fi
  return 1
}

detect_pg_external() {
  _db_detect_configured postgres "${OPP_PG_HOST:-127.0.0.1}" "${OPP_PG_PORT:-5432}"
}

detect_redis_external() {
  _db_detect_configured redis "${OPP_REDIS_HOST:-127.0.0.1}" "${OPP_REDIS_PORT:-6379}"
}

detect_mysql_external() {
  _db_detect_configured mysql "${OPP_MYSQL_HOST:-127.0.0.1}" "${OPP_MYSQL_PORT:-3306}"
}

# ── 综合判断：给定 host:port 是否真在跑 PG（用 psql 真握手验证）──
detect_pg_host() {
  local host="${1:-127.0.0.1}"
  local port="${2:-5432}"
  _db_port_open "${host}" "${port}" || return 1
  if command -v psql >/dev/null 2>&1; then
    PGPASSWORD="${OPP_PG_PASSWORD:-}" PGCONNECT_TIMEOUT=3 psql -h "${host}" -p "${port}" \
      -U "${OPP_PG_USER:-postgres}" -d "${OPP_PG_DB:-postgres}" \
      -tAc "SELECT 1" >/dev/null 2>&1 || return 1
  else
    return 1
  fi
  printf '%s:%s' "${host}" "${port}"
  return 0
}
