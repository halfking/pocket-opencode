#!/usr/bin/env bash
# local-up.sh — 启动 opencode-pocket 的 acc 集成部署（pocketd + frontend，
# 共享 acc-local-net + 复用 kxmemory-rls-pg17）。
#
# 假设：
#   1. `kxmemory-rls-pg17` 已运行（acc-local-net 网络成员）。
#   2. `acc-go-local` 可选运行（同网络）；acc-go 期望 POCKET_BASE_URL 指向它。
#   3. kx-base 离线镜像已 `docker load`：
#        docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz
#      加载后 `docker images` 应看到 `kx-base:go-vue-optimized`。
#   4. nginx-alpine 与 alpine:latest 基础镜像离线存在（见 OFFLINE_IMAGES.md）。
#
# 行为：
#   - 不执行 `docker pull`（pull_policy: never）。
#   - 自动 build（如本地镜像不存在）。
#   - 启动 pocketd 后等 /healthz 通过才启动 frontend。

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")"

if [ ! -f .env ]; then
  echo "[local-up] .env missing; run: cp .env.example .env"
  exit 1
fi

# 网络存在性检查
if ! docker network inspect acc-local-net >/dev/null 2>&1; then
  echo "[local-up] acc-local-net does not exist. Create it or start the kxmemory-rls-pg17 stack first:"
  echo "    docker network create acc-local-net"
  exit 1
fi

# kx-base 镜像存在性检查（offline-first）
if ! docker image inspect kx-base:go-vue-optimized >/dev/null 2>&1; then
  echo "[local-up] kx-base:go-vue-optimized not loaded. Run:"
  echo "    docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz"
  exit 1
fi

# PG 可达性
if ! docker exec kxmemory-rls-pg17 pg_isready -U kxuser -d kaixuan >/dev/null 2>&1; then
  echo "[local-up] WARNING: kxmemory-rls-pg17 not ready (pg_isready failed); pocketd will retry on boot"
fi

# 启动
docker compose -f docker-compose.yml up -d --build

echo
echo "[local-up] OK.  Watching health…"
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 16 18 20 25 30; do
  sleep 2
  if curl -sf http://localhost:${POCKET_HOST_PORT:-8088}/healthz >/dev/null 2>&1; then
    echo "[local-up] pocketd is healthy on http://localhost:${POCKET_HOST_PORT:-8088}"
    echo "[local-up] frontend  http://localhost:${POCKET_FRONTEND_HOST_PORT:-4175}"
    echo "[local-up] login: admin / admin1234 (POCKET_DEV_AUTH=true)"
    exit 0
  fi
  echo "[local-up] waiting for /healthz… ${i}"
done

echo "[local-up] timeout; check 'docker compose logs pocketd'"
exit 1