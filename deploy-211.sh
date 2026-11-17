#!/usr/bin/env bash
# Deploy the canonical OpenPocket images to the 211 platform Compose stack.
# Production credentials and database settings remain operator-owned in
# /opt/kaixuan/stacks/platform/docker-compose.platform.yml on 211.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOST="${OPENPOCKET_SSH_HOST:-211}"
REMOTE_DIR="${OPENPOCKET_REMOTE_DIR:-/opt/kaixuan/stacks/platform}"
COMPOSE_FILE="${REMOTE_DIR}/docker-compose.platform.yml"
PROJECT="kaixuan-platform"
TAG="amd64"
BUILD_ONLY=false
DRY_RUN=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --dry-run) DRY_RUN=true; shift ;;
    --build-only) BUILD_ONLY=true; shift ;;
    --help)
      printf 'Usage: %s [--build-only] [--dry-run]\n' "$0"
      printf 'Build both linux/amd64 images, transfer to 211, recreate canonical Compose services, and verify health.\n'
      exit 0 ;;
    *) printf 'Unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done

if [[ "$DRY_RUN" == true ]]; then
  printf 'Would run canonical amd64 image builds and transfer tagged images to %s,\n' "$HOST"
  printf 'then update %s services opencode_pocket_pocketd and opencode_pocket_frontend.\n' "$PROJECT"
  exit 0
fi

command -v docker >/dev/null
command -v ssh >/dev/null
command -v gzip >/dev/null

printf '== Build OpenPocket backend image for linux/amd64 ==\n'
# Use the repository's cross-compilation/runtime image pipeline for pocketd.
(cd "$ROOT_DIR" && OPP_IMAGE_TAG="$TAG" bash deploy/bin/build-images.sh --arch amd64 --backend-only)

printf '== Build OpenPocket same-origin frontend image ==\n'
# Build with the canonical Dockerfile which supplies MOBILE_ALLOW_EMPTY_API_BASE.
(cd "$ROOT_DIR" && MOBILE_ALLOW_EMPTY_API_BASE=1 docker buildx build \
  --builder amd64builder --platform linux/amd64 --load \
  -t "opencode-pocket-frontend:${TAG}" \
  -f Dockerfile.frontend .)

docker image inspect "opencode-pocket:${TAG}" --format 'backend={{.Architecture}} image={{.Id}}'
docker image inspect "opencode-pocket-frontend:${TAG}" --format 'frontend={{.Architecture}} image={{.Id}}'

if [[ "$BUILD_ONLY" == true ]]; then
  printf '== Build-only complete; images remain local. ==\n'
  exit 0
fi

printf '== Record rollback image ids on %s ==\n' "$HOST"
RELEASE="$(date -u +%Y%m%dT%H%M%SZ)"
ssh "$HOST" "set -euo pipefail
  docker image inspect opencode-pocket:${TAG} >/dev/null
  docker image inspect opencode-pocket-frontend:${TAG} >/dev/null
  docker tag opencode-pocket:${TAG} opencode-pocket:rollback-${RELEASE}
  docker tag opencode-pocket-frontend:${TAG} opencode-pocket-frontend:rollback-${RELEASE}
  docker image inspect opencode-pocket:rollback-${RELEASE} --format 'backend rollback={{.Id}}'
  docker image inspect opencode-pocket-frontend:rollback-${RELEASE} --format 'frontend rollback={{.Id}}'"

printf '== Transfer images to %s ==\n' "$HOST"
docker save "opencode-pocket:${TAG}" "opencode-pocket-frontend:${TAG}" \
  | gzip \
  | ssh "$HOST" 'gunzip | docker load'

printf '== Recreate OpenPocket production services and verify ==\n'
ssh "$HOST" "PROJECT='$PROJECT' COMPOSE_FILE='$COMPOSE_FILE' RELEASE='$RELEASE' bash -s" <<'REMOTE'
set -euo pipefail
cd "$(dirname "$COMPOSE_FILE")"

# Fail closed if the operator-owned production definition is absent or points
# at a different project/service contract.
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" config --services \
  | grep -qx opencode_pocket_pocketd
docker compose -p "$PROJECT" -f "$COMPOSE_FILE" config --services \
  | grep -qx opencode_pocket_frontend

rollback() {
  echo 'Deployment verification failed; restoring the prior image tags.' >&2
  docker tag "opencode-pocket:rollback-${RELEASE}" opencode-pocket:amd64
  docker tag "opencode-pocket-frontend:rollback-${RELEASE}" opencode-pocket-frontend:amd64
  docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d --no-deps --force-recreate \
    opencode_pocket_pocketd opencode_pocket_frontend || true
  exit 1
}

if ! docker compose -p "$PROJECT" -f "$COMPOSE_FILE" up -d --no-deps --force-recreate \
  opencode_pocket_pocketd opencode_pocket_frontend; then
  rollback
fi

for i in $(seq 1 40); do
  backend_state=$(docker inspect opencode-pocket-pocketd --format '{{.State.Health.Status}}' 2>/dev/null || true)
  frontend_state=$(docker inspect opencode-pocket-frontend --format '{{.State.Health.Status}}' 2>/dev/null || true)
  if [[ "$backend_state" == healthy && "$frontend_state" == healthy ]]; then
    break
  fi
  if [[ "$backend_state" == unhealthy || "$frontend_state" == unhealthy || $i -eq 40 ]]; then
    docker logs opencode-pocket-pocketd --tail 40 >&2 || true
    docker logs opencode-pocket-frontend --tail 20 >&2 || true
    rollback
  fi
  sleep 3
done

curl -fsS --max-time 10 http://127.0.0.1:8090/healthz
printf '\n'
curl -fsS --max-time 10 http://127.0.0.1:4175/ >/dev/null

docker inspect opencode-pocket-pocketd --format 'backend={{.Config.Image}} image={{.Image}} health={{.State.Health.Status}}'
docker inspect opencode-pocket-frontend --format 'frontend={{.Config.Image}} image={{.Image}} health={{.State.Health.Status}}'
docker logs opencode-pocket-pocketd 2>&1 | grep -E 'Postgres pool initialized|Postgres migration lock (acquired|released)' | tail -5
REMOTE

printf '== OpenPocket deployment verified; rollback tags: rollback-%s ==\n' "$RELEASE"
