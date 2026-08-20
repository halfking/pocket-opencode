#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLAN_DIR="$ROOT_DIR/deploy/本地方案"
ENV_FILE="${POCKET_LOCAL_ENV_FILE:-$PLAN_DIR/.env.local}"
PG_HOST="${POCKET_LOCAL_PG_HOST:-127.0.0.1}"
PG_PORT="${POCKET_LOCAL_PG_PORT:-15432}"
PG_USER="${POCKET_LOCAL_PG_USER:-kxuser}"
PG_DATABASE="${POCKET_LOCAL_PG_DATABASE:-pocket_local}"
PG_PASSWORD="${POCKET_LOCAL_PG_PASSWORD:-kxpass}"
export PGPASSWORD="$PG_PASSWORD"

if command -v psql >/dev/null 2>&1; then
  psql_cmd=(psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DATABASE" -v ON_ERROR_STOP=1)
else
  psql_cmd=(docker exec -e "PGPASSWORD=$PG_PASSWORD" r112_postgres psql -h 127.0.0.1 -U "$PG_USER" -d "$PG_DATABASE" -v ON_ERROR_STOP=1)
fi

"${psql_cmd[@]}" -Atc "SELECT current_database(), current_user, version();"
"${psql_cmd[@]}" -Atc "SELECT count(*) FROM pg_tables WHERE schemaname = 'public';"
"${psql_cmd[@]}" -Atc "SELECT tablename FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename;"
