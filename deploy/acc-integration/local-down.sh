#!/usr/bin/env bash
# local-down.sh — 停 opencode-pocket 的 acc 集成部署容器与卷（保留 .env）。

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
docker compose -f docker-compose.yml down --remove-orphans