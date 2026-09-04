#!/usr/bin/env bash
# =====================================================================
# load-images.sh — 从外部 images/ 目录加载离线镜像（252 服务器用）
#
# 扫描 ${POCKET_IMAGE_DIR} 下所有 *.tar / *.tar.gz，逐个 docker load。
# 本脚本面向 252，默认 DEPLOY_ENV=server（即 /opt/kaixuan/opp/images）；
# 本地复用时显式 DEPLOY_ENV=local。
#
# 用法：
#   sudo DEPLOY_ENV=server ./deploy/bin/load-images.sh        # 全部加载（默认 server）
#   sudo ./deploy/bin/load-images.sh --latest                 # 只加载每个镜像最新一份（按 mtime）
# =====================================================================

set -euo pipefail

export DEPLOY_ENV="${DEPLOY_ENV:-server}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"

LATEST_ONLY=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --latest) LATEST_ONLY=true; shift ;;
    --help) echo "用法: $0 [--latest]  （默认 DEPLOY_ENV=server）"; exit 0 ;;
    *) echo "未知参数: $1"; exit 1 ;;
  esac
done

if [[ ! -d "${POCKET_IMAGE_DIR}" ]]; then
  echo "❌ images 目录不存在: ${POCKET_IMAGE_DIR}" >&2
  echo "   当前 DEPLOY_ENV=${DEPLOY_ENV}；252 上请确认已 scp 到 /opt/kaixuan/opp/images/" >&2
  echo "   本地复用请显式: DEPLOY_ENV=local $0" >&2
  exit 1
fi

shopt -s nullglob
TARS=("${POCKET_IMAGE_DIR}"/*.tar "${POCKET_IMAGE_DIR}"/*.tar.gz)
shopt -u nullglob

if [[ ${#TARS[@]} -eq 0 ]]; then
  echo "❌ ${POCKET_IMAGE_DIR} 下没有 *.tar / *.tar.gz" >&2
  exit 1
fi

# --latest：每个镜像名只保留 mtime 最新的一份（arm64/amd64 tar 混存时各选其一）
if [[ "${LATEST_ONLY}" == true ]]; then
  LATEST_TARS=()
  # 长名前缀在前，避免 frontend 被 pocket 前缀误匹配
  for prefix in "opencode-pocket-frontend" "opencode-pocket"; do
    best=""
    for f in "${TARS[@]}"; do
      base="$(basename "${f}")"
      [[ "${base}" == "${prefix}"_* ]] || continue
      if [[ -z "${best}" ]] || [[ "${f}" -nt "${best}" ]]; then
        best="${f}"
      fi
    done
    [[ -n "${best}" ]] && LATEST_TARS+=("${best}")
  done
  # bash 3.2 + set -u 下空数组展开保护
  if [[ ${#LATEST_TARS[@]} -gt 0 ]]; then
    TARS=("${LATEST_TARS[@]}")
  else
    echo "❌ --latest 未匹配到任何镜像文件（前缀 opencode-pocket*）" >&2
    exit 1
  fi
fi

echo "━━━ load-images: ${POCKET_IMAGE_DIR} (DEPLOY_ENV=${DEPLOY_ENV}) ━━━"
for f in "${TARS[@]}"; do
  echo "▶ docker load < $(basename "${f}")"
  if [[ "${f}" == *.gz ]]; then
    gunzip -c "${f}" | docker load
  else
    docker load -i "${f}"
  fi
done

echo "✅ 加载完成；验证："
docker images | grep -E "opencode-pocket" || true
echo "   下一步: sudo ./deploy-252.sh   # 254/245 同理（按服务器角色选）"