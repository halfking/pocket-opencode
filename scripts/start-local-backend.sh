#!/usr/bin/env bash
# start-local-backend.sh - Linux counterpart of scripts/start-local-backend.ps1.
#
# WHY THIS EXISTS
#   The real-device Maestro rig can only bootstrap a backend through
#   maestro-run.mjs -> scripts/start-local-backend.ps1, and that is PowerShell.
#   On a Linux host the spawn fails, ensureBackend() returns false, and the run
#   aborts in preflight. Nothing about the *product* is broken; the launcher
#   simply does not exist for the platform. This file is that launcher, and it
#   keeps every guarantee the .ps1 has, because each of those was bought with a
#   real incident:
#
#   1. .env is parsed WITHOUT eval/source. POCKET_AUTH_PASS legitimately
#      contains '&' (the dev admin password does). `set -a; . ./.env` runs an
#      unquoted `POCKET_AUTH_PASS=<value-with-&>` as two commands: the variable
#      is set to the part before the '&' and the rest is executed. The backend
#      then starts perfectly, /healthz answers 200, and login fails with
#      "wrong password" - which is indistinguishable from a broken auth path.
#      Measured on 2026-10-03.
#   2. Double-bind is refused and the port is waited until actually released.
#      A second pocketd on one port means the App may talk to the stale one.
#   3. "healthz answered" is not "the process I started is the one answering".
#      The port owner pid is compared against the launched pid.
#   4. The email master key is checked, because the same key encrypts the mail
#      credentials AND the LLM gateway api key: a wrong one yields a healthy
#      backend with a silently dead mail pipeline and silently no-op AI.
#
# Usage:
#   PORT=18099 bash scripts/start-local-backend.sh
#   POCKET_ENV=/path/to/.env bash scripts/start-local-backend.sh
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${PORT:-18099}"
SCHEMA="${SCHEMA:-opencode_pocket}"
ENV_FILE="${POCKET_ENV:-$ROOT/.env}"
BIN="${POCKETD_BIN:-$ROOT/backend/.verify-bin/pocketd}"

log() { echo "[backend] $*"; }
err() { echo "[backend] $*" >&2; }

# ---------------------------------------------------------------- .env parse
# Deliberately not `source`. Reads KEY=VALUE line by line, honours optional
# single/double quotes, ignores comments and blank lines, and exports with
# `export K=V` so no shell ever parses the value as a command.
load_env_file() {
  local file="$1"
  [ -f "$file" ] || { err "env file not found: $file"; return 1; }
  local line key value
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      ''|'#'*) continue ;;
      *'#'*) line="${line%%#*}" ;;   # strip trailing comment (unquoted values only)
    esac
    case "$line" in *=*) ;; *) continue ;; esac
    key="${line%%=*}"
    value="${line#*=}"
    # trim surrounding whitespace on the key
    key="$(printf '%s' "$key" | tr -d '[:space:]')"
    [ -n "$key" ] || continue
    case "$key" in
      [A-Za-z_]*) ;;
      *) continue ;;   # not a valid identifier; ignore rather than export junk
    esac
    # strip one layer of matching quotes
    case "$value" in
      \"*\") value="${value:1:${#value}-2}" ;;
      \'*\') value="${value:1:${#value}-2}" ;;
    esac
    export "$key=$value"
  done < "$file"
  return 0
}

if ! load_env_file "$ENV_FILE"; then
  err "cannot start without $ENV_FILE. Copy .env.example to .env and fill it in."
  exit 1
fi
log "loaded env from $ENV_FILE"

# Fail loudly on the exact mistake this file exists to prevent.
#
# The check is on the **.env text**, not on a value: a bare `&` in an unquoted
# assignment is the whole bug class, whatever the password happens to be.
# The first version compared the loaded value against one hard-coded literal
# and printed that literal in the error message — which (a) puts a real
# credential into the repository, where TestNoCommittedSecrets correctly
# fails on it, and (b) only ever protected that one password. Matching the
# shape protects every password, including ones nobody has seen yet.
if grep -Eq '^[[:space:]]*(export[[:space:]]+)?POCKET_AUTH_PASS=[^'"'"'"]*&' "$ENV_FILE"; then
  err "POCKET_AUTH_PASS in $ENV_FILE contains '&' but is NOT quoted."
  err "  Any shell that sources this file treats the '&' as a command separator,"
  err "  silently keeping only the part before it. The backend would then start"
  err "  fine and every login would fail with 'wrong password'."
  err "  Fix: quote the whole value ->  POCKET_AUTH_PASS='<value containing &>'"
  exit 1
fi
if [ -z "${POCKET_AUTH_PASS:-}" ] && [ "${POCKET_DEV_AUTH:-}" = "true" ]; then
  err "POCKET_DEV_AUTH=true but POCKET_AUTH_PASS is empty; the dev auth bypass"
  err "refuses to start (see devBypassCredentials in internal/server)."
  exit 1
fi

export POCKET_HTTP_PORT="$PORT"
export POCKET_PG_SCHEMA="$SCHEMA"
# Absolute, because the data dir decides where email_master.key lives and where
# mail bodies/invoices are written. A relative value silently depends on the
# working directory, which is how a backend ends up with a key that decrypts
# 0 of 5 real accounts.
DATA_DIR="${DATA_DIR:-$ROOT/data}"
mkdir -p "$DATA_DIR"
export POCKET_DATA_DIR="$DATA_DIR"
export POCKET_DB_PATH="$DATA_DIR/pocket.db"

# ----------------------------------------------------------------- build bin
# Rebuild when the binary is missing **or when any backend .go file is newer
# than it**.
#
# 只在「二进制不存在」时构建是个真缺陷，不是省事：.ps1 版本沿用了这个条件，
# Linux 版一开始也照抄了。后果是改完 Go 源码再跑，整套真机回归仍在测**旧二进制**
# ——2026-10-03 实测：改了 handleEmailSync 的失败分类，脚本没重建，日志里
# 仍然是旧代码的 "email: sync already in flight" 走进失败分支。
# 这类 rig 里最贵的错误就是「测的不是你以为的那份代码」：改了 bug、重跑、
# 红绿与改动无关，于是得出完全相反的结论。
if [ ! -x "$BIN" ]; then
  log "building pocketd -> $BIN (not present)"
  mkdir -p "$(dirname "$BIN")"
  ( cd "$ROOT/backend" && go build -o "$BIN" ./cmd/pocketd ) || { err "backend build failed"; exit 1; }
elif [ -n "$(find "$ROOT/backend" -name '*.go' -newer "$BIN" -print -quit 2>/dev/null)" ]; then
  log "rebuilding pocketd -> $BIN (backend sources are newer than the binary)"
  ( cd "$ROOT/backend" && go build -o "$BIN" ./cmd/pocketd ) || { err "backend build failed"; exit 1; }
else
  log "pocketd binary is up to date ($BIN)"
fi

# --------------------------------------------------------- free the TCP port
port_owner() {
  ss -ltnpH "sport = :$PORT" 2>/dev/null \
    | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2
}
if [ -n "$(port_owner)" ]; then
  old="$(port_owner)"
  log "port $PORT held by pid $old, stopping it first"
  kill -9 "$old" 2>/dev/null
  for _ in $(seq 1 30); do
    [ -z "$(port_owner)" ] && break
    sleep 0.5
  done
  if [ -n "$(port_owner)" ]; then
    err "port $PORT is still held by pid $old after 15s; refusing to continue"
    exit 1
  fi
  log "port $PORT released"
fi

# ----------------------------------------------------------------- start it
mkdir -p "$ROOT/logs"
stamp="$(date +%Y%m%d-%H%M%S)"
out="$ROOT/logs/pocketd-$PORT-$stamp.out.log"
errlog="$ROOT/logs/pocketd-$PORT-$stamp.err.log"
# Run from backend/: loadVersionConfig resolves config/version.json against the
# process CWD, so launching from the repo root silently serves stale version info.
# `exec` keeps $! equal to the final pocketd pid instead of a wrapper subshell.
#
# setsid + </dev/null + disown: pocketd must be fully detached from this shell.
# A plain `nohup ... &` leaves it as a child of this script, and the script then
# sits in do_wait for a daemon that is meant to outlive it - measured: the first
# run of this script stayed alive for minutes after the backend was already
# serving /healthz 200, so every caller that shells out to it (maestro-run.mjs
# ensureBackend) blocks on a launcher that has nothing left to do.
( cd "$ROOT/backend" && exec setsid "$BIN" >"$out" 2>"$errlog" </dev/null ) &
pid=$!
disown "$pid" 2>/dev/null || true
echo "$pid" > "$ROOT/logs/pocketd-$PORT.pid"
log "launched pid=$pid (logs: $out / $errlog)"

# ---------------------------------------------------------------- wait ready
ready=0
for _ in $(seq 1 60); do
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:$PORT/healthz" 2>/dev/null)"
  if [ "$code" = "200" ]; then ready=1; break; fi
  sleep 0.5
done
if [ "$ready" != "1" ]; then
  err "/healthz unanswered after 30s. stderr tail:"
  tail -20 "$errlog" >&2
  exit 1
fi

# "healthz answered" is NOT "the process I started is the one answering".
owner="$(port_owner)"
if [ -z "$owner" ] || [ "$owner" != "$pid" ]; then
  err "/healthz answers, but port $PORT is owned by pid '${owner:-none}', not $pid."
  err "The process serving is not the one we launched. Refusing to report ready."
  tail -20 "$errlog" >&2
  exit 1
fi

# ------------------------------------------------------------- master key
sleep 0.3
if grep -q 'MASTER KEY LOOKS WRONG' "$errlog" 2>/dev/null; then
  err "WRONG EMAIL MASTER KEY for dataDir '$DATA_DIR'."
  err "  No real account decrypts -> no mail syncs, and the LLM gateway api key"
  err "  (same key) is unreadable -> classification / summary / STT silently no-op."
  grep -m4 -E 'MASTER KEY LOOKS WRONG|decrypt api key' "$errlog" >&2
elif grep -q 'Email credential self-check' "$errlog" 2>/dev/null; then
  log "master key OK (email credential self-check passed for $DATA_DIR)"
else
  log "no email credential self-check line in this boot; mail appears off - gate skipped"
fi

log "pid=$pid ready on $PORT, schema=$SCHEMA, port-owner-verified"
