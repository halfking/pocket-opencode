#!/usr/bin/env bash
# =====================================================================
# build-offline-images.sh — 全离线构建 opencode-pocket 两个镜像
#
# 与 build-images.sh 的区别：
#   build-images.sh      要求宿主机装 go，用宿主机交叉编译（252 amd64 发布用）
#   本脚本                宿主机不需要 go —— 在 kx-base/golang:1.27-alpine-arm64
#                        容器里编译，适合开发机 arm64 离线构建
#
# 全程约束：
#   - 基础镜像一律来自 prepare-offline-images.sh 准备好的本地镜像（--pull=false）
#   - Go 依赖优先走宿主机模块缓存（离线）；缓存不全才回落到 goproxy.cn
#   - 前端依赖优先走 npm 缓存；缓存不全才回落到 npm registry
#
# 前置：先跑 ./deploy/bin/prepare-offline-images.sh
#
# 用法：
#   ./deploy/bin/build-offline-images.sh                  # 两个都构建
#   ./deploy/bin/build-offline-images.sh --backend-only
#   ./deploy/bin/build-offline-images.sh --reuse-dist     # 复用已有 frontend/dist
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

GO_BUILDER="${OPP_GO_BUILDER_TAG:-kx-base/golang:1.27-alpine-arm64}"
RUNTIME_BASE="${OPP_RUNTIME_BASE_TAG:-alpine:opp-runtime-arm64}"
FRONTEND_BASE="${OPP_FRONTEND_BASE_TAG:-nginx:alpine}"

BUILD_BACKEND=true
BUILD_FRONTEND=true
REUSE_DIST=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --backend-only)  BUILD_FRONTEND=false; shift ;;
    --frontend-only) BUILD_BACKEND=false; shift ;;
    --reuse-dist)    REUSE_DIST=true; shift ;;
    --help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "未知参数: $1（--help 看用法）" >&2; exit 1 ;;
  esac
done

BACKEND_IMAGE="opencode-pocket:${OPP_IMAGE_TAG}"
FRONTEND_IMAGE="opencode-pocket-frontend:${OPP_IMAGE_TAG}"
TARGET_ARCH="$(docker info --format '{{.Architecture}}')"
case "${TARGET_ARCH}" in
  aarch64) TARGET_ARCH="arm64" ;;
  x86_64)  TARGET_ARCH="amd64" ;;
esac

c_ok="✅"; c_bad="❌"; c_warn="⚠️ "; c_info="ℹ️ "
die() { echo "${c_bad} $*" >&2; exit 1; }

echo "━━━━━━━━━━━━━━━━━━━━━━ 离线构建 openpocket 镜像 ━━━━━━━━━━━━━━━━━━━━━━"
echo "  目标架构   : ${TARGET_ARCH}"
echo "  后端镜像   : ${BACKEND_IMAGE}"
echo "  前端镜像   : ${FRONTEND_IMAGE}"
echo
echo "  资源需求（构建阶段）："
printf '    %-24s %s\n' "内存" "Go 编译峰值 ≈1.5-2 GiB（宿主需 ≥ 4 GiB 可用）"
printf '    %-24s %s\n' "磁盘（临时）" "≈1.5 GiB（二进制 + dist + 镜像层）"
printf '    %-24s %s\n' "宿主内存守护" "docker 默认无限；不需要额外配置"
printf '    %-24s %s\n' "构建耗时" "后端 3-8 分钟（首次含编译缓存），前端 2-5 分钟"
echo

# ── 前置检测 ──────────────────────────────────────────────────────
command -v docker >/dev/null 2>&1 || die "docker 不在 PATH"
docker info >/dev/null 2>&1 || die "docker daemon 未运行"
for img in "${GO_BUILDER}" "${RUNTIME_BASE}"; do
  docker image inspect "${img}" >/dev/null 2>&1 \
    || die "缺少基础镜像 ${img}；先跑 ./deploy/bin/prepare-offline-images.sh"
  arch="$(docker image inspect "${img}" --format '{{.Architecture}}')"
  [[ "${arch}" == "${TARGET_ARCH}" ]] \
    || die "基础镜像 ${img} 是 ${arch}，宿主是 ${TARGET_ARCH}；离线包里没有可用架构"
done
[[ "${BUILD_FRONTEND}" == false ]] || docker image inspect "${FRONTEND_BASE}" >/dev/null 2>&1 \
  || die "缺少基础镜像 ${FRONTEND_BASE}；先跑 ./deploy/bin/prepare-offline-images.sh"
echo "  ${c_ok} 基础镜像齐备（${GO_BUILDER} / ${RUNTIME_BASE}$( [[ "${BUILD_FRONTEND}" == true ]] && echo " / ${FRONTEND_BASE}" )）"

# 宿主可用内存：macOS 用 sysctl，Linux 用 /proc
if [[ "$(uname -s)" == "Darwin" ]]; then
  mem_free_mb=$(( $(sysctl -n hw.memsize 2>/dev/null || echo 0) / 1024 / 1024 ))
  mem_note="宿主物理内存 ${mem_free_mb} MiB"
else
  mem_free_mb=$(awk '/MemTotal/{print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
  mem_note="宿主内存 ${mem_free_mb} MiB"
fi
if [[ "${mem_free_mb:-0}" -lt 2048 ]]; then
  die "${mem_note} < 2048 MiB，Go 编译大概率 OOM；请先释放内存或加 swap"
fi
echo "  ${c_ok} ${mem_note}（≥ 2048 MiB）"

GIT_REV="$(git -C "${REPO_ROOT}" rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OCI_LABELS=(
  --label "org.opencontainers.image.revision=${GIT_REV}"
  --label "org.opencontainers.image.created=${BUILD_TS}"
  --label "org.opencontainers.image.version=${OPP_IMAGE_TAG}-${GIT_REV}"
)

WORK="$(mktemp -d "${TMPDIR:-/tmp}/opp-offline-build.XXXXXX")"
trap 'rm -rf "${WORK}"' EXIT

# ── 1. 后端：在 golang 容器里编译 ─────────────────────────────────
build_backend() {
  # 模块缓存：宿主 ~/go/pkg/mod 挂进容器，先离线（GOFLAGS=-mod=mod GOPROXY=off）。
  # 缓存不全时 go 会明确报缺哪个模块，再回落 goproxy.cn 重下——不静默联网。
  local modcache="${HOME}/go/pkg/mod"
  local host_gocache="${HOME}/.cache/go-build"
  mkdir -p "${host_gocache}"

  echo
  echo "▶ [1/3] 编译 pocketd（${GO_BUILDER}，${TARGET_ARCH}，CGO_ENABLED=0）"

  local go_build_env=(
    -e "CGO_ENABLED=0" -e "GOOS=linux" -e "GOARCH=${TARGET_ARCH}"
    -e "GOPATH=/go" -e "GOMODCACHE=/go/pkg/mod" -e "GOCACHE=/go-build"
    -e "GOFLAGS=-mod=mod" -e "GOPROXY=https://goproxy.cn,direct"
    -e "GOSUMDB=sum.golang.org"
  )

  local mount_modcache=(-v "${modcache}:/go/pkg/mod")
  local mount_gocache=(-v "${host_gocache}:/go-build")

  local go_cmd='set -e
cd /src
echo "  go $(go version | awk "{print \$3}")"
go build -trimpath -ldflags="-s -w" -o /out/pocketd ./cmd/pocketd
# 契约自检：编译产物必须真的含 /api/user-settings 路由，否则镜像上线才炸
grep -q user-settings /out/pocketd || { echo "pocketd missing /api/user-settings" >&2; exit 1; }
ls -lh /out/pocketd'

  local first=1
  for proxy in "off" "https://goproxy.cn,direct"; do
    if [ "${first}" = 1 ]; then
      echo "  ${c_info} 先试离线模块缓存（GOPROXY=off）…"
      first=0
    else
      echo "  ${c_warn} 模块缓存不全，回落到 ${proxy} 补依赖…"
    fi
    if docker run --rm "${go_build_env[@]}" -e "GOPROXY=${proxy}" \
        "${mount_modcache[@]}" "${mount_gocache[@]}" \
        -v "${REPO_ROOT}/backend:/src" \
        -v "${WORK}/backend-out:/out" \
        -w /src \
        --entrypoint /bin/sh "${GO_BUILDER}" -c "${go_cmd}"; then
      return 0
    fi
    echo "  ${c_warn} GOPROXY=${proxy} 失败"
  done

  # /out 用宿主目录直挂，省一次 docker cp
  return 1
}

# /out 需要真实目录，先建
if [[ "${BUILD_BACKEND}" == true ]]; then
  mkdir -p "${WORK}/backend-out"
  if ! build_backend; then
    die "后端编译失败（详见上面 Go 的报错）"
  fi
  mkdir -p "${WORK}/backend/config"
  cp "${WORK}/backend-out/pocketd" "${WORK}/backend/pocketd"
  cp "${REPO_ROOT}/backend/config/version.json" "${WORK}/backend/config/version.json"
fi

# ── 2. 前端：宿主 npm 构建 dist ───────────────────────────────────
build_frontend() {
  echo
  echo "▶ [2/3] 构建前端 dist（宿主 node $(node --version 2>/dev/null || echo '缺失')）"
  command -v node >/dev/null 2>&1 || die "宿主需要 node（vite 构建）；未找到 node"
  command -v npm  >/dev/null 2>&1 || die "宿主需要 npm"
  # MOBILE_ALLOW_EMPTY_API_BASE 与 Dockerfile.frontend 保持一致：
  # 容器内 nginx 同源反代 /api，构建期不能因为没有 API base 而失败。
  ( cd "${REPO_ROOT}/frontend" \
    && MOBILE_ALLOW_EMPTY_API_BASE=1 npm ci --no-audit --no-fund \
    && MOBILE_ALLOW_EMPTY_API_BASE=1 npm run build ) \
    || die "前端构建失败（npm ci 或 vue-tsc/vite 报错）"
  [[ -f "${REPO_ROOT}/frontend/dist/index.html" ]] || die "frontend/dist/index.html 不存在，构建没成功"
  echo "  ${c_ok} dist 就绪（$(du -sh "${REPO_ROOT}/frontend/dist" | cut -f1)）"
}

if [[ "${BUILD_FRONTEND}" == true ]]; then
  if [[ "${REUSE_DIST}" = true && -f "${REPO_ROOT}/frontend/dist/index.html" ]]; then
    echo; echo "▶ [2/3] 复用已有 frontend/dist"
  else
    build_frontend
  fi
fi

# ── 3. 组装镜像 ───────────────────────────────────────────────────
echo
echo "▶ [3/3] 组装镜像"
if [[ "${BUILD_BACKEND}" == true ]]; then
  docker build --pull=false "${OCI_LABELS[@]}" \
    --build-arg "RUNTIME_BASE_IMAGE=${RUNTIME_BASE}" \
    -f "${SCRIPT_DIR}/../docker/Dockerfile.pocketd-offline" \
    -t "${BACKEND_IMAGE}" "${WORK}/backend" >/dev/null \
    || die "后端镜像组装失败"
  echo "  ${c_ok} ${BACKEND_IMAGE}  $(docker image inspect "${BACKEND_IMAGE}" --format '{{.Size}}' | awk '{printf "%.1f MiB", $1/1048576}')"
fi

if [[ "${BUILD_FRONTEND}" == true ]]; then
  fe_ctx="${WORK}/frontend"
  mkdir -p "${fe_ctx}"
  cp -R "${REPO_ROOT}/frontend/dist" "${fe_ctx}/dist"
  cp "${REPO_ROOT}/deploy/本地方案/nginx.conf" "${fe_ctx}/nginx.conf"
  docker build --pull=false "${OCI_LABELS[@]}" \
    --build-arg "FRONTEND_BASE_IMAGE=${FRONTEND_BASE}" \
    -f "${SCRIPT_DIR}/../docker/Dockerfile.frontend-prebuilt" \
    -t "${FRONTEND_IMAGE}" "${fe_ctx}" >/dev/null \
    || die "前端镜像组装失败"
  echo "  ${c_ok} ${FRONTEND_IMAGE}  $(docker image inspect "${FRONTEND_IMAGE}" --format '{{.Size}}' | awk '{printf "%.1f MiB", $1/1048576}')"
fi

# ── 结果 ──────────────────────────────────────────────────────────
echo
echo "  ${c_ok} 构建完成（rev=${GIT_REV}）"
[[ "${BUILD_BACKEND}" == false ]] || docker image inspect "${BACKEND_IMAGE}" \
  --format '    {{.RepoTags}}  arch={{.Architecture}}  size={{.Size}}  rev={{index .Config.Labels "org.opencontainers.image.revision"}}'
[[ "${BUILD_FRONTEND}" == false ]] || docker image inspect "${FRONTEND_IMAGE}" \
  --format '    {{.RepoTags}}  arch={{.Architecture}}  size={{.Size}}  rev={{index .Config.Labels "org.opencontainers.image.revision"}}'
echo
echo "  下一步：./deploy-local.sh   （start.sh 检测到镜像已存在，走 --no-build）"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"