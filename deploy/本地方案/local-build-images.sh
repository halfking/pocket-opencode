#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PLAN_DIR="$ROOT_DIR/deploy/本地方案"
mkdir -p "$PLAN_DIR/artifacts"

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go -C "$ROOT_DIR/backend" build -trimpath -ldflags='-s -w' -o "$PLAN_DIR/artifacts/pocketd" ./cmd/pocketd

docker build --pull=false --network=none -f "$ROOT_DIR/Dockerfile.runtime" -t opencode-pocket:pocket-local "$ROOT_DIR"
docker build --pull=false --network=none -f "$ROOT_DIR/Dockerfile.frontend" -t opencode-pocket-frontend:pocket-local "$ROOT_DIR"

echo "local images built without pull: opencode-pocket:pocket-local opencode-pocket-frontend:pocket-local"
