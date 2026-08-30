#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLAN_DIR="$ROOT_DIR/deploy/本地方案"
COMPOSE_FILE="$PLAN_DIR/docker-compose.local.yml"
PROJECT_NAME="opencode-pocket-local"
ENV_FILE="${POCKET_LOCAL_ENV_FILE:-$PLAN_DIR/.env.local}"

if [[ ! -f "$ENV_FILE" ]]; then
  "$PLAN_DIR/local-db-init.sh"
fi

if [[ -f "$ENV_FILE" ]]; then
  set -a
  # shellcheck disable=SC1090
  source "$ENV_FILE"
  set +a
fi

# 确保 r112_net 网络存在
if ! docker network inspect r112_net >/dev/null 2>&1; then
  echo "Creating r112_net network..."
  docker network create r112_net
fi

if ! docker image inspect opencode-pocket:pocket-local >/dev/null 2>&1; then
  echo "missing local image opencode-pocket:pocket-local; build or docker load it explicitly" >&2
  exit 1
fi
if ! docker image inspect opencode-pocket-frontend:pocket-local >/dev/null 2>&1; then
  echo "missing local image opencode-pocket-frontend:pocket-local; build or docker load it explicitly" >&2
  exit 1
fi

cd "$PLAN_DIR"
docker compose -p "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" config >/dev/null
docker compose -p "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" up -d --no-build

for attempt in $(seq 1 30); do
  if curl -fsS "http://127.0.0.1:${POCKET_HTTP_PORT:-8088}/healthz" >/dev/null 2>&1; then
    echo "pocketd ready on :${POCKET_HTTP_PORT:-8088}"
    break
  fi
  if [[ "$attempt" == 30 ]]; then
docker compose -p "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" ps
docker compose -p "$PROJECT_NAME" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" logs --tail=80 pocketd
    exit 1
  fi
  sleep 2
done

curl -fsS "http://127.0.0.1:${POCKET_FRONTEND_PORT:-4175}/healthz"
