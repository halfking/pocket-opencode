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
# bash 内建 /dev/tcp 优先：Git Bash 等环境 PATH 上的 nc 可能是 BusyBox 精简版，
# 不支持 -z/-G（报 unknown option 恒非零），不能把探测工具自身的能力缺陷
# 当成「端口不通」——否则本机已有实例会被漏检、误走容器化路径撞端口。
# nc 仅作 /dev/tcp 不可用（个别 bash 编译裁剪）时的兜底。
_db_port_open() {
  local host="$1" port="$2"
  if timeout 3 bash -c "</dev/tcp/${host}/${port}" >/dev/null 2>&1; then
    return 0
  fi
  if command -v nc >/dev/null 2>&1; then
    # macOS/BSD 的 nc 用 -G（连接超时），Linux/GNU 的 nc 只认 -w；
    # 用错标志会报 unknown option 恒非零，把「超时语义」退化成「永远探不通」，
    # 误判本机已有实例不存在而走容器化撞端口。
    if [[ "$(uname -s)" == Darwin ]]; then
      nc -z -G 3 "${host}" "${port}" >/dev/null 2>&1
    else
      nc -z -w 3 "${host}" "${port}" >/dev/null 2>&1
    fi
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

# 找一个「把 <port> 发布到宿主」的引擎容器，作为宿主缺客户端时的探测载体。
#
# 为什么必须有这条兜底：macOS 开发机常常没装 libpq / redis-cli / mysqladmin
# （没 homebrew），但公用服务全跑在 docker 里。没有兜底时探测会因为
# 「psql 不存在」恒失败，把一台明明在跑 PG17 的机器误判成「没有 PG」，
# 转而去另起一个实例撞端口 —— 正是「复用已有公用服务」这条要求要防的事。
#
# 只认「确实发布了该宿主端口」的容器：端口对不上就不能拿它冒充目标实例。
_db_engine_container() {
  local kind="$1" port="$2" sig name image ports
  command -v docker >/dev/null 2>&1 || return 1
  docker ps >/dev/null 2>&1 || return 1
  case "$kind" in
    postgres) sig='postgres|pgvector|citus' ;;
    redis)    sig='redis|valkey|keydb' ;;
    mysql)    sig='mysql|mariadb|percona' ;;
    *) return 1 ;;
  esac
  while IFS=$'\t' read -r name image ports; do
    [[ -n "${name}" ]] || continue
    [[ "${ports}" == *":${port}->"* ]] || continue
    if printf '%s %s' "${name}" "${image}" | tr '[:upper:]' '[:lower:]' | grep -Eq "${sig}"; then
      printf '%s' "${name}"
      return 0
    fi
  done < <(docker ps --format '{{.Names}}\t{{.Image}}\t{{.Ports}}' 2>/dev/null)
  return 1
}

# 在上面找到的容器里跑该引擎的客户端。容器内一律连 127.0.0.1 的引擎标准端口
# （5432/6379/3306）——那是容器自己的内部端口，与宿主发布端口未必相同。
_db_client_in_container() {
  local kind="$1" binary="$2" container="$3" internal_port="$4"
  shift 4
  _db_probe docker exec -i "${container}" "${binary}" -h 127.0.0.1 -p "${internal_port}" "$@" 2>/dev/null
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
        container="$(_db_engine_container postgres "${port}")" || return 1
        PGPASSWORD="${OPP_PG_PASSWORD:-}" PGCONNECT_TIMEOUT=3 \
          _db_client_in_container postgres psql "${container}" 5432 \
            -U "${OPP_PG_USER:-postgres}" -d "${OPP_PG_DB:-postgres}" -tAc 'SELECT 1' >/dev/null 2>&1
      fi
      ;;
    redis)
      if command -v redis-cli >/dev/null 2>&1; then
        reply=$(REDISCLI_AUTH="${OPP_REDIS_PASSWORD:-${REUSE_REDIS_PASSWORD:-}}" \
          _db_probe redis-cli -h "$host" -p "$port" --no-auth-warning PING 2>/dev/null || true)
      else
        container="$(_db_engine_container redis "${port}")" || return 1
        reply=$(REDISCLI_AUTH="${OPP_REDIS_PASSWORD:-${REUSE_REDIS_PASSWORD:-}}" \
          _db_client_in_container redis redis-cli "${container}" 6379 --no-auth-warning PING 2>/dev/null || true)
      fi
      [[ "$reply" == PONG ]]
      ;;
    mysql)
      if command -v mysqladmin >/dev/null 2>&1; then
        reply=$(MYSQL_PWD="${OPP_MYSQL_PASSWORD:-}" _db_probe mysqladmin --connect-timeout=3 \
          -h "$host" -P "$port" -u "${OPP_MYSQL_USER:-root}" ping 2>/dev/null || true)
        [[ "$reply" == "mysqld is alive" ]] && return 0
      elif command -v mysql >/dev/null 2>&1; then
        reply=$(MYSQL_PWD="${OPP_MYSQL_PASSWORD:-}" _db_probe mysql --connect-timeout=3 -h "$host" -P "$port" \
          -u "${OPP_MYSQL_USER:-root}" -e 'SELECT 1' 2>/dev/null || true)
        [[ "${reply}" == *1* ]] && return 0
      else
        container="$(_db_engine_container mysql "${port}")" || return 1
        reply=$(MYSQL_PWD="${OPP_MYSQL_PASSWORD:-}" \
          _db_client_in_container mysql mysql "${container}" 3306 --connect-timeout=3 \
            -u "${OPP_MYSQL_USER:-root}" -e 'SELECT 1' 2>/dev/null || true)
      fi
      [[ "$reply" == "mysqld is alive" ]]
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
