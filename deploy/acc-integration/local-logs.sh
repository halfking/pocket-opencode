#!/usr/bin/env bash
# local-logs.sh — 跟踪 pocketd + frontend 日志

set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"
docker compose -f docker-compose.yml logs -f --tail=200