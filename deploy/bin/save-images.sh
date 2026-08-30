#!/usr/bin/env bash
# =====================================================================
# save-images.sh — 导出部署镜像到外部 images/ 目录（离线分发用）
#
# 把 opencode-pocket / opencode-pocket-frontend 两个镜像 docker save
# 到 ${POCKET_IMAGE_DIR}，文件名带 tag + 时间戳。
#
# 典型流程（本地 → 252）：
#   本地:  ./deploy/bin/save-images.sh            # 生成 images/*.tar.gz
#   传输:  scp images/*.tar.gz user@252:/opt/kaixuan/opp/images/
#   252 :  sudo ./deploy/bin/load-images.sh
#
# 注意：252 是 linux/amd64。本地（Apple Silicon）构建时需指定平台：
#   docker build --platform linux/amd64 ...（参考 deploy/build-image.sh）
#   或对 arm64 源镜像用 buildx 重建，否则 252 上跑不起来。
#
# 用法：
#   ./deploy/bin/save-images.sh                # 导出 backend + frontend（gzip）
#   ./deploy/bin/save-images.sh --backend-only
#   ./deploy/bin/save-images.sh --raw          # 不压缩（快，文件大）
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

BACKEND_ONLY=false
COMPRESS=true
while [[ $# -gt 0 ]]; do
  case "$1" in
    --backend-only) BACKEND_ONLY=true; shift ;;
    --raw) COMPRESS=false; shift ;;
    --help) echo "用法: $0 [--backend-only] [--raw]"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

mkdir -p "${POCKET_IMAGE_DIR}"
TS="$(date +%Y%m%d-%H%M%S)"

save_one() {
  local img="$1" name="$2"
  if ! docker image inspect "${img}" >/dev/null 2>&1; then
    echo "❌ 镜像不存在: ${img}（先 start.sh --build 或 build-image.sh）" >&2
    exit 1
  fi
  local arch
  arch="$(docker image inspect "${img}" --format '{{.Architecture}}')"
  local out="${POCKET_IMAGE_DIR}/${name}_${OPP_IMAGE_TAG}_${arch}_${TS}.tar"
  echo "▶ docker save ${img} → ${out}"
  docker save -o "${out}" "${img}"
  if [[ "${COMPRESS}" == true ]]; then
    echo "▶ gzip ${out}"
    gzip -f "${out}"
    out="${out}.gz"
  fi
  du -sh "${out}"
}

save_one "opencode-pocket:${OPP_IMAGE_TAG}" "opencode-pocket"
if [[ "${BACKEND_ONLY}" == false ]]; then
  save_one "opencode-pocket-frontend:${OPP_IMAGE_TAG}" "opencode-pocket-frontend"
fi

echo "✅ 导出完成 → ${POCKET_IMAGE_DIR}/"
echo "   252 加载: sudo ./deploy/bin/load-images.sh"
