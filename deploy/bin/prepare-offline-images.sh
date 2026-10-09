#!/usr/bin/env bash
# =====================================================================
# prepare-offline-images.sh — 离线基础镜像准备（不联网拉取任何镜像）
#
# 为什么需要这个脚本：
#   仓库自带的 Dockerfile.kx-base 默认依赖三个「必须联网才有」的镜像：
#     - 构建器 kx-base:go-vue-optimized（实测 Go 1.24.13 < go.mod 的 1.27.1）
#     - 运行时 alpine:latest
#     - 前端构建器 node:22-bookworm-slim
#   本机要求「基础镜像文件在本地镜像库里，不要去网络中下载」，所以本脚本
#   从本地镜像库 load 离线 tar，并就地「合」出一个最小 arm64 alpine 运行时
#   基底（从已加载的 arm64 alpine 镜像导出 rootfs 去掉 Go 工具链），
#   让整个构建链彻底不触网。
#
# 产出（全部来自本地镜像库，零 registry 访问）：
#   kx-base/golang:1.27-alpine-arm64   构建器（Go 1.27.1 + git + apk + gcc）
#   nginx:alpine                       前端运行时（nginx 1.27.5）
#   alpine:opp-runtime-arm64           pocketd 运行时基底（最小 alpine，含 wget + CA）
#
# 用法：
#   ./deploy/bin/prepare-offline-images.sh            # 准备并校验（幂等，可重复跑）
#   ./deploy/bin/prepare-offline-images.sh --check    # 只检测不改动
#   OPP_BASE_IMAGE_DIR=/path ./deploy/bin/prepare-offline-images.sh   # 指定镜像库
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# ── 可覆盖的输入 ──────────────────────────────────────────────────
# 本机实际镜像库在 ~/workspace/docker-base-images；另两个路径是历史约定，
# 一并探测，避免把「路径不对」误报成「镜像不存在」。
BASE_DIR_CANDIDATES=(
  "${OPP_BASE_IMAGE_DIR:-}"
  "${HOME}/workspace/docker-base-images"
  "${HOME}/work/docker-base-images"
  "${HOME}/workspace/docer-base-images"
)

# 需要的镜像：tag|相对镜像库的 tar 路径|角色
REQUIRED_IMAGES=(
  "kx-base/golang:1.27-alpine-arm64|lang-base/kx-base-golang-1.27-alpine-arm64.tar.gz|构建器 Go 1.27.1"
  "nginx:alpine|lang-base/nginx-1.27.5-alpine-arm64.tar.gz|前端运行时 nginx 1.27.5"
  "redis:7-alpine|cache-mq/redis-7.4.3-alpine-arm64.tar.gz|最小 alpine 载体（合成运行时基底）"
)
# 合成的运行时基底 tag（由 alpine 基础镜像派生，仓库里没有现成 tar）
RUNTIME_BASE_TAG="alpine:opp-runtime-arm64"
# 合成用的来源镜像，按优先级探测：都要 arm64 且是 alpine 系。
#   redis:7-alpine               —— 首选。alpine + redis 二进制，剥掉 redis 就是 ~10MB 纯 alpine
#   kx-base/golang:1.27-alpine   —— 兜底。alpine + Go + gcc/binutils，剥完仍有 ~270MB
RUNTIME_SOURCE_TAGS=(redis:7-alpine kx-base/golang:1.27-alpine-arm64)
# 合成后必须存在的东西（pocketd 运行期依赖，少一个就不给过）
RUNTIME_REQUIRED_PATHS=(bin/sh bin/busybox sbin/apk usr/bin/wget usr/sbin/adduser usr/sbin/addgroup etc/ssl/certs/ca-certificates.crt)

CHECK_ONLY=false
[[ "${1:-}" == "--check" ]] && CHECK_ONLY=true

# ── 小工具 ────────────────────────────────────────────────────────
c_ok="✅"; c_bad="❌"; c_warn="⚠️ "; c_info="ℹ️ "
die() { echo "${c_bad} $*" >&2; exit 1; }

need_cmd() { command -v "$1" >/dev/null 2>&1 || die "缺少命令: $1（${2:-}）"; }

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  elif command -v shasum   >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else echo ""; fi
}

image_arch() { docker image inspect "$1" --format '{{.Architecture}}' 2>/dev/null || echo ""; }
image_size() { docker image inspect "$1" --format '{{.Size}}' 2>/dev/null || echo 0; }
human_size() { awk -v b="$1" 'BEGIN{
  if (b>=1073741824) printf "%.1f GiB", b/1073741824;
  else if (b>=1048576) printf "%.1f MiB", b/1048576;
  else if (b>=1024) printf "%.0f KiB", b/1024;
  else printf "%d B", b }'; }

# ── 0. 资源需求（部署前把「要花什么」摊开讲清楚）─────────────────
# macOS 的 Docker Desktop 默认只在 ~/.docker/bin 放一个 docker 软链，不改
# PATH；从非 GUI shell（CI、SSH、编辑器任务）跑时就会「docker: command not
# found」。这里把已知安装位置补进 PATH，避免用户先手工 export。
if ! command -v docker >/dev/null 2>&1; then
  for docker_bin_dir in \
      "${HOME}/.docker/bin" \
      "/Applications/Docker.app/Contents/Resources/bin" \
      "/usr/local/bin" "/opt/homebrew/bin"; do
    if [ -x "${docker_bin_dir}/docker" ]; then
      export PATH="${docker_bin_dir}:${PATH}"
      echo "ℹ️  docker 不在 PATH，已自动补入: ${docker_bin_dir}"
      break
    fi
  done
fi
need_cmd docker "安装 Docker Desktop / docker engine"
docker info >/dev/null 2>&1 || die "docker daemon 未运行（Docker Desktop 未启动？）"
docker compose version >/dev/null 2>&1 || die "需要 docker compose v2"
need_cmd tar "系统自带"
need_cmd python3 "脚本里的 JSON/摘要处理依赖它"

HOST_ARCH="$(uname -m)"
case "${HOST_ARCH}" in
  arm64|aarch64) HOST_ARCH="arm64" ;;
  x86_64|amd64)  HOST_ARCH="amd64" ;;
  *) die "不支持的宿主架构: ${HOST_ARCH}" ;;
esac

# ── 1. 探测镜像库 ────────────────────────────────────────────────
BASE_DIR=""
for cand in "${BASE_DIR_CANDIDATES[@]}"; do
  [[ -z "${cand}" ]] && continue
  if [ -d "${cand}" ]; then BASE_DIR="${cand}"; break; fi
done
if [ -z "${BASE_DIR}" ]; then
  echo "${c_bad} 找不到本地基础镜像库，探测过：" >&2
  for cand in "${BASE_DIR_CANDIDATES[@]}"; do [[ -n "${cand}" ]] && echo "        - ${cand}" >&2; done
  echo "        用 OPP_BASE_IMAGE_DIR=<路径> 显式指定" >&2
  exit 1
fi

# Docker Desktop 把镜像存在 Linux VM 里，DockerRootDir 在 macOS 宿主上
# 并不存在（df 会直接报错）。所以先试 DockerRootDir，不存在就退回宿主
# 数据卷——两者都要能给出可用空间，否则无法做磁盘预检。
docker_root="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)"
if [ -n "${docker_root}" ] && [ -d "${docker_root}" ]; then
  space_fs="${docker_root}"
else
  space_fs="${HOME}"
  [[ -d /System/Volumes/Data ]] && space_fs=/System/Volumes/Data
fi
avail_gib=$(df -Pk "${space_fs}" 2>/dev/null | awk 'NR==2{print int($4/1024/1024)}')

echo "━━━━━━━━━━━━━━━━━━━━━━━ 离线基础镜像准备 ━━━━━━━━━━━━━━━━━━━━━━━"
echo "  宿主架构   : ${HOST_ARCH}"
echo "  镜像库     : ${BASE_DIR}"
echo "  Docker Root: ${docker_root:-?}（macOS Docker Desktop：镜像存于 VM 内，磁盘按宿主数据卷计）"
echo
echo "  资源需求（本步骤）："
printf '    %-24s %s\n' "磁盘可用空间" "$(
  [ -n "${avail_gib}" ] && echo "≈${avail_gib} GiB（需 ≥ 8 GiB，测的是 ${space_fs}）" || echo "?")"
printf '    %-24s %s\n' "待 load 的离线 tar" "≈250 MiB（golang 158M + nginx 21M + redis 32M）"
printf '    %-24s %s\n' "导入后镜像占用" "≈1.1 GiB（构建器 909MB + nginx 100MB + redis 82MB + 运行时基底 ≈10MB）"
printf '    %-24s %s\n' "registry 网络访问" "不需要（全程 --pull=never / 不联网）"
printf '    %-24s %s\n' "临时工作目录" "${TMPDIR:-/tmp}（约 300 MiB，结束即清）"
echo

if [ -z "${avail_gib}" ]; then
  echo "  ${c_warn} 磁盘可用空间探测不到，跳过磁盘预检"
elif [ "${avail_gib}" -lt 8 ]; then
  die "磁盘可用空间不足：约 ${avail_gib} GiB < 8 GiB；请先 docker system prune 或清理磁盘"
else
  echo "  ${c_ok} 磁盘预检通过（可用 ≈${avail_gib} GiB）"
fi
echo

# ── 2. 逐个检查/载入 ─────────────────────────────────────────────
LOADED=0; REUSED=0; FAILED=0
echo "  ┌─ 基础镜像检查 ─────────────────────────────────────────────"
for entry in "${REQUIRED_IMAGES[@]}"; do
  IFS='|' read -r tag rel role <<< "${entry}"
  tar_path="${BASE_DIR}/${rel}"

  # 已在本地 → 只校验架构，不重复 load
  if arch="$(image_arch "${tag}")" && [ -n "${arch}" ]; then
    if [ "${arch}" != "${HOST_ARCH}" ]; then
      echo "  │ ${c_bad} ${tag}"
      echo "  │     本地是 ${arch}，宿主要 ${HOST_ARCH}；换用 ${HOST_ARCH} 的离线包"
      FAILED=$((FAILED+1)); continue
    fi
    echo "  │ ${c_ok} ${tag}  [已就位 ${arch}]  $(human_size "$(image_size "${tag}")")  ${role}"
    REUSED=$((REUSED+1)); continue
  fi

  if [ ! -f "${tar_path}" ]; then
    echo "  │ ${c_bad} ${tag}  缺离线包: ${rel}"
    FAILED=$((FAILED+1)); continue
  fi

  # 有 sidecar 校验和就校验（sidecar 里的路径可能是构建机的 /tmp 路径，只取哈希值）
  sidecar="${tar_path}.sha256"
  if [ -f "${sidecar}" ]; then
    want="$(awk '{print $1; exit}' "${sidecar}")"
    got="$(sha256_of "${tar_path}")"
    if [ -n "${want}" ] && [ -n "${got}" ] && [ "${want}" != "${got}" ]; then
      echo "  │ ${c_bad} ${tag}  sha256 不匹配"
      echo "  │     期望 ${want:0:16}… 实际 ${got:0:16}…"
      FAILED=$((FAILED+1)); continue
    fi
    echo "  │ ${c_info} ${tag}  sha256 校验通过 ${got:0:16}…"
  fi

  if [ "${CHECK_ONLY}" = true ]; then
    echo "  │ ${c_warn} ${tag}  未载入（--check 模式不改动）"
    continue
  fi

  echo "  │ ${c_info} ${tag}  loading $(basename "${rel}") …"
  if ! docker load -i "${tar_path}" >/dev/null 2>&1; then
    echo "  │ ${c_bad} ${tag}  docker load 失败：${rel}"
    FAILED=$((FAILED+1)); continue
  fi
  arch="$(image_arch "${tag}")"
  if [ "${arch}" != "${HOST_ARCH}" ]; then
    echo "  │ ${c_bad} ${tag}  载入后架构 ${arch} ≠ 宿主 ${HOST_ARCH}"
    FAILED=$((FAILED+1)); continue
  fi
  echo "  │ ${c_ok} ${tag}  已载入 [${arch}]  $(human_size "$(image_size "${tag}")")  ${role}"
  LOADED=$((LOADED+1))
done
echo "  └────────────────────────────────────────────────────────────"
echo

if [ "${FAILED}" -gt 0 ]; then
  die "${FAILED} 个基础镜像不可用；请先补齐镜像库后重跑（不会退回联网下载）"
fi

# ── 3. 合成最小 arm64 alpine 运行时基底 ───────────────────────────
# 镜像库里没有「纯 alpine」tar（只有 alpine:3.18-i386，架构不对），
# 所以从已就绪的 arm64 alpine 镜像导出 rootfs、剥掉它的应用负载后重新组装。
# 纯本地操作：不 build、不 pull、不联网。
synth_runtime() {
  local src_tag="$1" out_tag="$2"
  local work
  work="$(mktemp -d "${TMPDIR:-/tmp}/opp-alpine-rootfs.XXXXXX")"
  # shellcheck disable=SC2064
  trap "rm -rf '${work}'" RETURN

  echo "  ${c_info} 从 ${src_tag} 导出 rootfs（剥掉应用负载）…"
  local cid
  cid="$(docker create "${src_tag}")" || return 1
  # --exclude 让 Go / redis 的文件根本不落盘，省掉一次大目录删除。
  # --no-xattrs 必需：macOS 导出的文件带 com.apple.* 扩展属性，
  # 打进镜像时 Docker 会因 lsetxattr 失败而报 "no such file or directory"。
  # 未命中的 exclude 会被 tar 静默忽略，因此这里可以列全所有来源的负载。
  docker export "${cid}" | tar -x -C "${work}" --no-xattrs \
      --exclude='./usr/local/go' --exclude='./go' \
      --exclude='./usr/local/bin/redis*' --exclude='./usr/local/bin/gosu' \
      --exclude='./usr/local/bin/docker-entrypoint.sh' \
      --exclude='./home/redis' --exclude='./data' || {
    docker rm -f "${cid}" >/dev/null 2>&1 || true; return 1; }
  docker rm -f "${cid}" >/dev/null 2>&1 || true

  # 兜底：万一还有残留 xattr（不同 tar 实现行为不一致），先清一遍
  if command -v xattr >/dev/null 2>&1; then
    xattr -cr "${work}" >/dev/null 2>&1 || true
  fi

  local rootfs_tar="${work%/}/alpine-rootfs.tar"
  COPYFILE_DISABLE=1 tar -C "${work}" --no-xattrs \
      --exclude='./alpine-rootfs.tar' -cf "${rootfs_tar}" . || return 1

  echo "  ${c_info} 组装 ${out_tag} …"
  cat > "${work}/Dockerfile" <<'DOCKERFILE'
# 由 deploy/bin/prepare-offline-images.sh 从本地离线镜像合成：
# 从 arm64 alpine 系镜像导出 rootfs，去掉 Go 工具链后以 scratch 重新组装。
# 目的：拿到「最小 arm64 alpine 运行时基底」，全程不联网。
FROM scratch
ADD alpine-rootfs.tar /
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
CMD ["/bin/sh"]
DOCKERFILE

  docker build -t "${out_tag}" "${work}" >/dev/null || return 1
  return 0
}

echo "  ┌─ 运行时基底 ───────────────────────────────────────────────"
if arch="$(image_arch "${RUNTIME_BASE_TAG}")" && [ "${arch}" == "${HOST_ARCH}" ]; then
  echo "  │ ${c_ok} ${RUNTIME_BASE_TAG}  [已就位 ${arch}]  $(human_size "$(image_size "${RUNTIME_BASE_TAG}")")"
  REUSED=$((REUSED+1))
elif [ "${CHECK_ONLY}" = true ]; then
  echo "  │ ${c_warn} ${RUNTIME_BASE_TAG}  未合成（--check 模式不改动）"
else
  runtime_src=""
  for cand in "${RUNTIME_SOURCE_TAGS[@]}"; do
    if [ "$(image_arch "${cand}")" = "${HOST_ARCH}" ]; then runtime_src="${cand}"; break; fi
  done
  [ -n "${runtime_src}" ] || die "合成基底需要一个 ${HOST_ARCH} 的 alpine 来源镜像（试过: ${RUNTIME_SOURCE_TAGS[*]}），都没就绪"
  if ! synth_runtime "${runtime_src}" "${RUNTIME_BASE_TAG}"; then
    die "合成 ${RUNTIME_BASE_TAG} 失败"
  fi
  echo "  │ ${c_ok} ${RUNTIME_BASE_TAG}  已合成（来源 ${runtime_src}）[$(image_arch "${RUNTIME_BASE_TAG}")]  $(human_size "$(image_size "${RUNTIME_BASE_TAG}")")"
  LOADED=$((LOADED+1))
fi
echo "  └────────────────────────────────────────────────────────────"
echo

# ── 4. 运行时基底能力自检 ─────────────────────────────────────────
# 「基底在」不等于「基底够用」：pocketd 的健康检查要 wget，TLS 要 CA，
# 非 root 运行要 adduser/addgroup。少一个就在这里拦下，别等容器起不来。
echo "  ┌─ 运行时基底能力自检（${RUNTIME_BASE_TAG}）──────────────────"
if [ "$(image_arch "${RUNTIME_BASE_TAG}")" != "${HOST_ARCH}" ]; then
  echo "  │ ${c_bad} 基底不可用（架构不符或未生成），跳过能力自检"
else
  probe='missing=0
for p in '"$(printf "'%s' " "${RUNTIME_REQUIRED_PATHS[@]}" | paste -sd' ' -)"'; do
  [ -e "/${p}" ] && echo "  ok ${p}" || { echo "  no ${p}"; missing=1; }
done
exit $missing'
  if docker run --rm --network none --entrypoint /bin/sh "${RUNTIME_BASE_TAG}" -c "${probe}" 2>/dev/null; then
    n="${#RUNTIME_REQUIRED_PATHS[@]}"
    echo "  │ ${c_ok} ${n} 项运行期依赖齐备（sh/busybox/apk/wget/adduser/addgroup/CA）"
  else
    echo "  │ ${c_bad} 基底缺件，pocketd 容器会起不来；请清掉 ${RUNTIME_BASE_TAG} 后重跑本脚本"
    FAILED=$((FAILED+1))
  fi
fi
echo "  └────────────────────────────────────────────────────────────"
echo

# ── 5. 结果汇总 ───────────────────────────────────────────────────
echo "  准备结果：载入 ${LOADED} 个 / 复用 ${REUSED} 个 / 失败 ${FAILED} 个"
echo
if [ "${FAILED}" -gt 0 ]; then
  die "离线镜像准备未完成（${FAILED} 项失败）"
fi
echo "  ${c_ok} 离线基础镜像全部就绪，可离线执行："
echo "     ./deploy/bin/build-offline-images.sh    # 构建 opencode-pocket 两个镜像"
echo "     ./deploy-local.sh                       # 起服务"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# 供 build-offline-images.sh / Dockerfile 复用
export OPP_RUNTIME_BASE_TAG="${RUNTIME_BASE_TAG}"
export OPP_GO_BUILDER_TAG="kx-base/golang:1.27-alpine-arm64"
export OPP_FRONTEND_BASE_TAG="nginx:alpine"