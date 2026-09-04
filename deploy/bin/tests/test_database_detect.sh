#!/usr/bin/env bash
# =====================================================================
# test_database_detect.sh — 单元合约测试：database-detect.sh
#
# 用法：
#   bash deploy/bin/tests/test_database_detect.sh
#
# 覆盖：
#   1. OPP_DOCKER_PS_HIT 模拟 docker 命中 → detect 返回 mode=docker
#   2. OPP_SYSTEMCTL_HIT 模拟 systemctl 命中 → detect 返回 mode=system
#   3. 端口可达 + 127.0.0.1 → mode=local-port
#   4. 端口不可达 + 没 docker/systemctl → detect 失败
#   5. detect_pg_external / detect_redis_external / detect_mysql_external 形态一致
# =====================================================================

set -uo pipefail

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
expect_eq() {
  local actual="$1" expected="$2" label="$3"
  if [[ "${actual}" == "${expected}" ]]; then
    pass "${label}: '${actual}'"
  else
    fail "${label}: expected='${expected}' got='${actual}'"
  fi
}
expect_empty() {
  local actual="$1" label="$2"
  if [[ -z "${actual}" ]]; then
    pass "${label}"
  else
    fail "${label}: expected empty, got '${actual}'"
  fi
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

# 全局累积要清理的 fake bin 与 listener pid
declare -a FAKE_BINS=()
declare -a LISTENER_PIDS=()
cleanup() {
  for pid in "${LISTENER_PIDS[@]}"; do
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
  for fb in "${FAKE_BINS[@]}"; do
    rm -rf "${fb}" 2>/dev/null || true
  done
}
trap cleanup EXIT

# 创建 fake docker / systemctl（hits 控制）
make_fake_bin() {
  local hits="$1"
  local fb
  fb="$(mktemp -d -t opp-fake-bin.XXXXXX)"
  FAKE_BINS+=("${fb}")
  case "${hits}" in
    docker)
      cat > "${fb}/docker" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "ps" ]]; then
  printf 'opp-db-postgres\nopp-db-redis\nopp-db-mysql\nopp-other\n'
fi
exit 0
EOF
      chmod +x "${fb}/docker"
      ;;
    system)
      cat > "${fb}/systemctl" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "is-active" ]]; then exit 0; fi
exit 0
EOF
      chmod +x "${fb}/systemctl"
      ;;
  esac
  printf '%s' "${fb}"
}

# 起本地监听端口（用于 port-reachable 测试）
start_listener() {
  local host="$1" port="$2"
  if ! command -v python3 >/dev/null 2>&1; then
    echo "SKIP: python3 unavailable"
    return 1
  fi
  python3 -c "
import socket, time
srv = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
try:
    srv.bind(('${host}', ${port}))
    srv.listen(5)
except OSError:
    pass
while True: time.sleep(60)
" &
  local pid=$!
  LISTENER_PIDS+=("${pid}")
  sleep 0.6
}

# 跑 detect
run_detect() {
  local fb="$1" func="$2" target_host="$3" target_port="$4"
  PATH="${fb}:${PATH}" \
    bash -c "
      source deploy/bin/env.sh >/dev/null 2>&1
      source deploy/bin/lib/database-detect.sh >/dev/null 2>&1
      OPP_PG_HOST='${target_host}'
      OPP_PG_PORT='${target_port}'
      OPP_REDIS_HOST='${target_host}'
      OPP_REDIS_PORT='${target_port}'
      OPP_MYSQL_HOST='${target_host}'
      OPP_MYSQL_PORT='${target_port}'
      ${func}
    "
}

# ── 测试 1: docker 命中 ─────────────────────────────────────────
fb1="$(make_fake_bin docker)"
result_pg_docker="$(run_detect "${fb1}" "detect_pg_external" "127.0.0.1" "15432")"
expect_eq "${result_pg_docker}" "docker:127.0.0.1:15432" "PG detect via docker"

result_redis_docker="$(run_detect "${fb1}" "detect_redis_external" "127.0.0.1" "6379")"
expect_eq "${result_redis_docker}" "docker:127.0.0.1:6379" "Redis detect via docker"

result_mysql_docker="$(run_detect "${fb1}" "detect_mysql_external" "127.0.0.1" "3306")"
expect_eq "${result_mysql_docker}" "docker:127.0.0.1:3306" "MySQL detect via docker"

# ── 测试 2: systemd 命中 ────────────────────────────────────────
fb2="$(make_fake_bin system)"
result_pg_sys="$(run_detect "${fb2}" "detect_pg_external" "127.0.0.1" "15432")"
expect_eq "${result_pg_sys}" "system:127.0.0.1:15432" "PG detect via systemd"

# ── 测试 3: 端口可达（无 fake docker / systemctl）──────────────
start_listener "127.0.0.1" 15432
result_pg_port="$(run_detect "" "detect_pg_external" "127.0.0.1" "15432")"
expect_eq "${result_pg_port}" "local-port:127.0.0.1:15432" "PG detect via local port"

# ── 测试 4: 端口不可达 + 没 docker/systemctl → 失败 ───────────
# 用一个肯定没人用的端口（避开本机已有的 5432 / 15432）
result_pg_none="$(run_detect "" "detect_pg_external" "127.0.0.1" "54321" 2>/dev/null || true)"
expect_empty "${result_pg_none}" "PG detect: nothing found returns empty"

# ── 测试 5: 远端 IP 端口不可达 → 失败（没法 mock 远端，假设连接超时）──
# 端口也用冷门值：loopback 兜底探测会扫 127.0.0.1:<port>，
# 常用端口（5432 等）在本机可能真有实例监听，导致假命中。
result_remote="$(run_detect "" "detect_pg_external" "192.0.2.1" "54321" 2>/dev/null || true)"  # TEST-NET-1
expect_empty "${result_remote}" "PG detect: remote unreachable returns empty"

echo
echo "━━━ test_database_detect ━━━"
printf '  PASS: %d  FAIL: %d\n' "${PASS}" "${FAIL}"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
