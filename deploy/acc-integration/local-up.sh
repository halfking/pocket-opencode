#!/usr/bin/env bash
# local-up.sh — 启动 opencode-pocket 的 acc 集成部署（pocketd + frontend，
# 共享 acc-local-net + 复用 llm-gateway-pg 共享 PG 实例）。
#
# 假设：
#   1. `llm-gateway-pg` 已运行（原 kxmemory-rls-pg17 已于 2026-08-18 合并下线）。
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

# 网络存在性检查 - 自动创建
if ! docker network inspect acc-local-net >/dev/null 2>&1; then
  echo "[local-up] Creating acc-local-net network..."
  docker network create acc-local-net
fi

if ! docker network inspect shared-infra >/dev/null 2>&1; then
  echo "[local-up] Creating shared-infra network..."
  docker network create shared-infra
fi

# kx-base 镜像存在性检查（offline-first）
if ! docker image inspect kx-base:go-vue-optimized >/dev/null 2>&1; then
  echo "[local-up] kx-base:go-vue-optimized not loaded. Run:"
  echo "    docker load -i ~/work/docker-base-images/lang-base/kx-base-go-vue-v2-alpine-slim-arm64.tar.gz"
  exit 1
fi

# PG 可达性（llm-gateway-pg，宿主端口 5432）
if ! docker inspect llm-gateway-pg >/dev/null 2>&1; then
  echo "[local-up] llm-gateway-pg container not found; start the shared PG before pocketd"
  exit 1
fi
if ! docker exec llm-gateway-pg pg_isready -U llm_gateway -d llm_gateway >/dev/null 2>&1; then
  echo "[local-up] llm-gateway-pg is not ready; refusing to start pocketd against an unknown host service"
  exit 1
fi

# 数据库身份验证（防止误连到错误的 PG 实例）
if ! docker exec llm-gateway-pg psql -U llm_gateway -d kaixuan -tAc "SELECT current_database(), current_user;" 2>/dev/null | grep -q "^kaixuan|llm_gateway$"; then
  echo "[local-up] Database identity mismatch; refusing to start pocketd against wrong PG instance"
  exit 1
fi

# 启动。frontend nginx 在启动时解析 pocketd 的容器 IP；pocketd 被重建后
# 旧 nginx 可能仍指向旧 IP 并返回 502，因此两个服务必须一起强制重建。
docker compose -f docker-compose.yml up -d --build --force-recreate pocketd frontend

echo
echo "[local-up] OK.  Watching health…"
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 16 18 20 25 30; do
  sleep 2
  if curl -sf "http://localhost:${POCKET_HOST_PORT:-8088}/healthz" >/dev/null 2>&1 && \
     curl -sf "http://localhost:${POCKET_FRONTEND_HOST_PORT:-4175}/healthz" >/dev/null 2>&1; then
    echo "[local-up] pocketd is healthy on http://localhost:${POCKET_HOST_PORT:-8088}"
    echo "[local-up] frontend is healthy on http://localhost:${POCKET_FRONTEND_HOST_PORT:-4175}"
    echo "[local-up] dev auth enabled; use POCKET_AUTH_USER/POCKET_AUTH_PASS from local .env"
    exit 0
  fi
  echo "[local-up] waiting for /healthz… ${i}"
done

echo "[local-up] timeout; check 'docker compose logs pocketd'"
exit 1