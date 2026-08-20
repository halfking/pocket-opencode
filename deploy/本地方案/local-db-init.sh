#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLAN_DIR="$ROOT_DIR/deploy/本地方案"
ENV_FILE="${POCKET_LOCAL_ENV_FILE:-$PLAN_DIR/.env.local}"
PG_HOST="${POCKET_LOCAL_PG_HOST:-127.0.0.1}"
PG_PORT="${POCKET_LOCAL_PG_PORT:-15432}"
PG_USER="${POCKET_LOCAL_PG_USER:-kxuser}"
PG_DATABASE="${POCKET_LOCAL_PG_DATABASE:-pocket_local}"
PG_ADMIN_DATABASE="${POCKET_LOCAL_PG_ADMIN_DATABASE:-postgres}"
PG_PASSWORD="${POCKET_LOCAL_PG_PASSWORD:-kxpass}"

export PGPASSWORD="$PG_PASSWORD"

if command -v psql >/dev/null 2>&1; then
  psql_cmd=(psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_ADMIN_DATABASE" -v ON_ERROR_STOP=1)
else
  psql_cmd=(docker exec -e "PGPASSWORD=$PG_PASSWORD" r112_postgres psql -h 127.0.0.1 -U "$PG_USER" -d "$PG_ADMIN_DATABASE" -v ON_ERROR_STOP=1)
fi

"${psql_cmd[@]}" -Atc "SELECT 1" >/dev/null

if ! "${psql_cmd[@]}" -Atc "SELECT 1 FROM pg_database WHERE datname = '$PG_DATABASE'" | grep -qx 1; then
  "${psql_cmd[@]}" -c "CREATE DATABASE \"$PG_DATABASE\""
fi

cat > "$PLAN_DIR/.env.local" <<EOF
POCKET_ENV=development
POCKET_HTTP_PORT=${POCKET_HTTP_PORT:-8088}
POCKET_FRONTEND_PORT=${POCKET_FRONTEND_PORT:-4175}
POCKET_POSTGRES_DSN=postgresql://${PG_USER}:${PG_PASSWORD}@host.docker.internal:${PG_PORT}/${PG_DATABASE}?sslmode=disable
POCKET_TEST_POSTGRES_DSN=postgresql://${PG_USER}:${PG_PASSWORD}@${PG_HOST}:${PG_PORT}/${PG_DATABASE}?sslmode=disable
POCKET_JWT_SECRET=${POCKET_JWT_SECRET:-local-pocket-jwt-secret-012345678901234567890}
POCKET_DEV_AUTH=true
POCKET_ALLOWED_ORIGINS=http://localhost:${POCKET_FRONTEND_PORT:-4175},http://127.0.0.1:${POCKET_FRONTEND_PORT:-4175}
POCKET_OPENCODE_INSTANCES=[{"id":"local-opencode","displayName":"Local OpenCode","baseURL":"http://host.docker.internal:4096","environment":"development","capabilities":["session","summary","pty"]}]
POCKET_EMAIL_FETCH_ENABLED=false
EOF

chmod 600 "$PLAN_DIR/.env.local"
echo "database ready: host=$PG_HOST port=$PG_PORT database=$PG_DATABASE user=$PG_USER"
