#!/usr/bin/env bash
# Contract tests: configured protocol, never a name or bare listening port.
set -uo pipefail
PASS=0 FAIL=0
pass() { PASS=$((PASS + 1)); printf '  PASS %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  FAIL %s\n' "$1"; }
expect_eq() { [[ "$1" == "$2" ]] && pass "$3" || fail "$3: expected '$2', got '$1'"; }
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
FAKE_BIN="$(mktemp -d -t opp-db-test.XXXXXX)"
trap 'rm -rf "$FAKE_BIN"' EXIT
cat > "$FAKE_BIN/pg_isready" <<'EOF'
#!/usr/bin/env bash
while [[ $# -gt 0 ]]; do case "$1" in -h) target="$2"; shift 2;; *) shift;; esac; done
if [[ "$target" == "${TEST_READY_HOST:-}" ]]; then echo 'accepting connections'; exit 0; fi
echo 'no response'; exit 2
EOF
cat > "$FAKE_BIN/redis-cli" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "${TEST_REDIS_REPLY:-NOAUTH Authentication required.}"
exit 0
EOF
cat > "$FAKE_BIN/mysqladmin" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "${TEST_MYSQL_REPLY:-Access denied}"
exit 0
EOF
cat > "$FAKE_BIN/docker" <<'EOF'
#!/usr/bin/env bash
printf 'postgres-misleading-name\nredis-misleading-name\n'
EOF
cat > "$FAKE_BIN/nc" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$FAKE_BIN"/*
run_detect() {
  PATH="$FAKE_BIN:$PATH" bash -c '
    source "$1/deploy/bin/lib/database-detect.sh"
    OPP_PG_HOST="$3" OPP_PG_PORT="$4" OPP_REDIS_HOST="$3" OPP_REDIS_PORT="$4" OPP_MYSQL_HOST="$3" OPP_MYSQL_PORT="$4"
    "$2"
  ' _ "$REPO_ROOT" "$1" "$2" "$3"
}
export TEST_READY_HOST=127.0.0.1
expect_eq "$(run_detect detect_pg_external 127.0.0.1 5432)" 'local-port:127.0.0.1:5432' 'configured PG ready'
expect_eq "$(run_detect detect_pg_external host.docker.internal 5432)" 'local-port:127.0.0.1:5432' 'Docker host alias verifies same loopback port'
expect_eq "$(run_detect detect_pg_external db.example 5432 || true)" '' 'remote failure cannot reuse unrelated loopback'
export TEST_READY_HOST=db.example
expect_eq "$(run_detect detect_pg_external db.example 5432)" 'remote:db.example:5432' 'configured remote protocol'
unset TEST_READY_HOST
expect_eq "$(run_detect detect_pg_external 127.0.0.1 5432 || true)" '' 'open TCP and misleading Docker names cannot prove PostgreSQL'
expect_eq "$(run_detect detect_redis_external 127.0.0.1 6379 || true)" '' 'Redis NOAUTH rc0 rejected'
export TEST_REDIS_REPLY=PONG
expect_eq "$(run_detect detect_redis_external 127.0.0.1 6379)" 'local-port:127.0.0.1:6379' 'Redis PONG required'
expect_eq "$(run_detect detect_mysql_external 127.0.0.1 3306 || true)" '' 'MySQL access denied rc0 rejected'
export TEST_MYSQL_REPLY='mysqld is alive'
expect_eq "$(run_detect detect_mysql_external 127.0.0.1 3306)" 'local-port:127.0.0.1:3306' 'MySQL authenticated response'
printf '  PASS: %d  FAIL: %d\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]]
