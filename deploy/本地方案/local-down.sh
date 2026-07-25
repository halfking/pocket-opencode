#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLAN_DIR="$ROOT_DIR/deploy/本地方案"
ENV_FILE="${POCKET_LOCAL_ENV_FILE:-$PLAN_DIR/.env.local}"
COMPOSE_FILE="$PLAN_DIR/docker-compose.local.yml"
PROJECT_NAME="opencode-pocket-local"

if [[ ! -f "$ENV_FILE" ]]; then
  echo "missing $ENV_FILE; run local-db-init.sh first" >&2
  exit 1
fi
cd "$PLAN_DIR"
docker compose -p "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" down
