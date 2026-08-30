#!/usr/bin/env bash
# =====================================================================
# build-images.sh — 从最新源码正式构建 opencode-pocket 两镜像
#
# 构建策略（宿主机交叉编译 + 纯 COPY runtime Dockerfile）：
#   - backend ：宿主 go 交叉编译静态二进制（CGO_ENABLED=0 GOOS=linux
#     GOARCH=<arch>），再以 deploy/docker/Dockerfile.pocketd-prebuilt
#     打成镜像。不经模拟器编译，不依赖 arm64-only 的 kx-base 离线包。
#   - frontend：宿主 node/npm 构建 dist/（vite 产物与架构无关），再以
#     deploy/docker/Dockerfile.frontend-prebuilt 复制进 nginx。
#   - runtime base（alpine/nginx）按 --platform 由 registry 解析对应架构，
#     需要网络可达 docker.io（本地 Mac 已验证）。
#
# 产物 tag 与 deploy/bin 其余脚本共用 OPP_IMAGE_TAG（默认 pocket-opp），
# 镜像带 OCI label（revision/created/version）供部署后审计。
#
# 用法：
#   ./deploy/bin/build-images.sh                       # amd64 + arm64 都构建
#   ./deploy/bin/build-images.sh --arch amd64          # 只构建 252 用的 amd64
#   ./deploy/bin/build-images.sh --arch arm64 --backend-only
#   ./deploy/bin/build-images.sh --reuse-dist         # 复用 frontend/dist 跳过 npm 构建
#
# 典型发布流程（252）：
#   ./deploy/bin/build-images.sh --arch amd64          # 构建 amd64
#   ./deploy/bin/save-images.sh                        # 导出 amd64 tars
#   scp ~/Downloads/kaixuan/opp/images/*.tar.gz root@252:/opt/kaixuan/opp/images/
#   （详见 deploy/bin/README.md “252 服务器”节）
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

ARCHES="amd64,arm64"
BACKEND_ONLY=false
REUSE_DIST=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --arch) ARCHES="$2"; shift 2 ;;
    --backend-only) BACKEND_ONLY=true; shift ;;
    --reuse-dist) REUSE_DIST=true; shift ;;
    --help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1（--help 看用法）"; exit 1 ;;
  esac
done

command -v go >/dev/null 2>&1 || { echo "❌ 宿主机需要 go（交叉编译 pocketd）"; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "❌ 宿主机需要 node/npm（构建前端 dist）"; exit 1; }

GIT_REV="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OCI_LABELS=(
  --label "org.opencontainers.image.revision=${GIT_REV}"
  --label "org.opencontainers.image.created=${BUILD_TS}"
  --label "org.opencontainers.image.version=${OPP_IMAGE_TAG}-${GIT_REV}"
)

WORK="$(mktemp -d "${TMPDIR:-/tmp}/opp-build.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

echo "━━━ build-images: rev=${GIT_REV} tag=${OPP_IMAGE_TAG} arches=[${ARCHES}] ━━━"

# ── 1. 前端 dist（架构无关，只构建一次）──────────────────────────
NGINX_CONF_SRC="${REPO_ROOT}/deploy/本地方案/nginx.conf"
[[ -f "${NGINX_CONF_SRC}" ]] || { echo "❌ 缺 ${NGINX_CONF_SRC}"; exit 1; }

if [[ "${BACKEND_ONLY}" == false && "${REUSE_DIST}" == false ]]; then
  echo "▶ [frontend] npm ci + npm run build（host node $(node --version)）"
  (cd "${REPO_ROOT}/frontend" && npm ci --no-audit --no-fund >/dev/null && npm run build)
fi
if [[ "${BACKEND_ONLY}" == false ]]; then
  [[ -f "${REPO_ROOT}/frontend/dist/index.html" ]] || {
    echo "❌ frontend/dist 不存在（构建失败？或用 --reuse-dist 前先构建一次）"; exit 1; }
fi

# ── 2. 逐架构构建 ────────────────────────────────────────────────
IFS=',' read -ra ARCH_LIST <<< "${ARCHES}"
for arch in "${ARCH_LIST[@]}"; do
  case "${arch}" in amd64|arm64) ;; *) echo "❌ 不支持架构: ${arch}（amd64|arm64）"; exit 1 ;; esac

  # backend：交叉编译静态二进制
  echo "▶ [pocketd ${arch}] go build"
  mkdir -p "${WORK}/be-${arch}"
  (cd "${REPO_ROOT}/backend" && \
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" \
    go build -trimpath -ldflags='-s -w' -o "${WORK}/be-${arch}/pocketd" ./cmd/pocketd)
  docker build --platform "linux/${arch}" "${OCI_LABELS[@]}" \
    -f "${SCRIPT_DIR}/../docker/Dockerfile.pocketd-prebuilt" \
    -t "opencode-pocket:${OPP_IMAGE_TAG}" "${WORK}/be-${arch}"

  if [[ "${BACKEND_ONLY}" == false ]]; then
    echo "▶ [frontend ${arch}] 打 nginx 镜像（复用同一份 dist）"
    mkdir -p "${WORK}/fe-${arch}"
    cp -R "${REPO_ROOT}/frontend/dist" "${WORK}/fe-${arch}/dist"
    cp "${NGINX_CONF_SRC}" "${WORK}/fe-${arch}/nginx.conf"
    docker build --platform "linux/${arch}" "${OCI_LABELS[@]}" \
      -f "${SCRIPT_DIR}/../docker/Dockerfile.frontend-prebuilt" \
      -t "opencode-pocket-frontend:${OPP_IMAGE_TAG}" "${WORK}/fe-${arch}"
  fi
done

# ── 3. 架构核验（tag 会被最后一个架构覆盖，逐 arch 构建后应立即 save）──
echo "━━━ 构建完成 ━━━"
docker image inspect "opencode-pocket:${OPP_IMAGE_TAG}" \
  --format 'opencode-pocket:${OPP_IMAGE_TAG}  arch={{.Architecture}}  rev={{index .Config.Labels "org.opencontainers.image.revision"}}'
if [[ "${BACKEND_ONLY}" == false ]]; then
  docker image inspect "opencode-pocket-frontend:${OPP_IMAGE_TAG}" \
    --format 'opencode-pocket-frontend:${OPP_IMAGE_TAG}  arch={{.Architecture}}  rev={{index .Config.Labels "org.opencontainers.image.revision"}}'
fi
echo "  ⚠️  同 tag 多架构会互相覆盖：252 发布请 --arch amd64 构建后立即 ./deploy/bin/save-images.sh"
echo "  252 导出: ./deploy/bin/save-images.sh   本地重启: POCKET_HTTP_PORT=8090 POCKET_FRONTEND_PORT=4176 ./deploy/bin/deploy-local.sh"
