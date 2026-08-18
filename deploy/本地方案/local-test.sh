#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLAN_DIR="$ROOT_DIR/deploy/本地方案"
BACKEND_DIR="$ROOT_DIR/backend"
FRONTEND_DIR="$ROOT_DIR/frontend"
ENV_FILE="${POCKET_LOCAL_ENV_FILE:-$PLAN_DIR/.env.local}"
BASE_URL="${POCKET_LOCAL_BASE_URL:-http://127.0.0.1:8088}"
FRONTEND_URL="${POCKET_LOCAL_FRONTEND_URL:-http://127.0.0.1:4175}"
ARTIFACT_DIR="$PLAN_DIR/artifacts/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$ARTIFACT_DIR"

run_step() {
  local name="$1"
  shift
  echo "==> $name"
  "$@" 2>&1 | tee "$ARTIFACT_DIR/$name.log"
}

run_step backend-test bash -c "cd '$BACKEND_DIR' && go test ./... -count=1"
run_step backend-vet bash -c "cd '$BACKEND_DIR' && go vet ./..."
run_step backend-build bash -c "cd '$BACKEND_DIR' && go build ./cmd/pocketd"
run_step frontend-typecheck bash -c "cd '$FRONTEND_DIR' && npm run typecheck"
run_step frontend-build bash -c "cd '$FRONTEND_DIR' && npm run build"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "missing $ENV_FILE; run local-db-init.sh or local-up.sh first" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1090
source "$ENV_FILE"
set +a

BASE_URL="${POCKET_LOCAL_BASE_URL:-http://127.0.0.1:${POCKET_HTTP_PORT:-8088}}"
FRONTEND_URL="${POCKET_LOCAL_FRONTEND_URL:-http://127.0.0.1:${POCKET_FRONTEND_PORT:-4175}}"

run_step api-health curl -fsS "$BASE_URL/healthz"
run_step frontend-health curl -fsS "$FRONTEND_URL/healthz"

login_body="{\"username\":\"${POCKET_AUTH_USER:-admin}\",\"password\":\"${POCKET_AUTH_PASS}\"}"
login_response="$(curl -fsS -X POST "$BASE_URL/api/auth/login" -H 'Content-Type: application/json' -d "$login_body")"
printf '%s\n' "$login_response" > "$ARTIFACT_DIR/login.json"
token="$(printf '%s' "$login_response" | jq -r '.token // empty')"
if [[ -z "$token" ]]; then
  echo "login did not return a token" >&2
  exit 1
fi
printf 'token acquired (not persisted)\n' > "$ARTIFACT_DIR/auth.log"

run_step protected-notes curl -fsS "$BASE_URL/api/notes" -H "Authorization: Bearer $token"
run_step protected-notifications curl -fsS "$BASE_URL/api/notifications" -H "Authorization: Bearer $token"
run_step protected-workspaces curl -fsS "$BASE_URL/api/workspaces" -H "Authorization: Bearer $token"

if [[ -n "${POCKET_TEST_POSTGRES_DSN:-}" ]]; then
  run_step postgres-integration bash -c "cd '$BACKEND_DIR' && POCKET_TEST_POSTGRES_DSN='$POCKET_TEST_POSTGRES_DSN' go test ./internal/identity ./internal/agentbridge ./internal/lobster ./internal/notifycenter -count=1"
else
  echo "POCKET_TEST_POSTGRES_DSN is not configured; integration tests skipped" | tee "$ARTIFACT_DIR/postgres-integration.log"
fi

cat > "$ARTIFACT_DIR/summary.txt" <<EOF
status=passed
base_url=$BASE_URL
frontend_url=$FRONTEND_URL
artifact_dir=$ARTIFACT_DIR
external_opencode=${POCKET_LOCAL_SKIP_OPENCODE:-true}
EOF
printf 'local test passed: %s\n' "$ARTIFACT_DIR"
