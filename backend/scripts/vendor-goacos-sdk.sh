#!/usr/bin/env bash
# 薄封装：真正干活的是 goacos 仓里的 scripts/vendor-sdk-copy.sh。
#
# 为什么是薄封装，而不是每个仓各存一份完整的脚本：
# 那份脚本要处理「消费方的 go 指令」—— 嵌套模块的 go 指令必须 **≤ 消费方主模块**
# 的 go 指令，否则本机 toolchain 不够高时消费方的构建会直接失败
# （实测：主模块 go 1.27.1 + 嵌套模块 go 1.27.2 ⇒
#   go: module ./dep requires go >= 1.27.2 (running go 1.27.1)
#  而 GOTOOLCHAIN=auto 不会因为依赖要求更高版本就自动切换）。
# 这条规则一改，散在各仓的副本就会漂 ⇒ 规则留在 SDK 老家，各仓只留一个入口。
#
# 用法：
#   scripts/vendor-goacos-sdk.sh <goacos-ref>
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "${HERE}/.." && pwd)"

# goacos 仓的位置：先看环境变量，再往上找两级。
#
# ⚠ 不能只找一层：消费方可能是**嵌套模块**（openpocket/backend 的 go.mod 在
#   openpocket/backend/ 下，而 goacos 仓与 openpocket/ 同级）⇒ 两级都要试。
find_goacos() {
  if [ -n "${GOACOS_REPO:-}" ]; then
    echo "${GOACOS_REPO}"
    return 0
  fi
  for cand in "${REPO}/../goacos" "${REPO}/../../goacos"; do
    if [ -f "${cand}/scripts/vendor-sdk-copy.sh" ]; then
      (cd "${cand}" && pwd)
      return 0
    fi
  done
  return 1
}

if ! GOACOS="$(find_goacos)"; then
  echo "找不到 goacos 仓（找过：${REPO}/../goacos、${REPO}/../../goacos）。" >&2
  echo "用 GOACOS_REPO=<路径> 指定。" >&2
  exit 2
fi

exec "${GOACOS}/scripts/vendor-sdk-copy.sh" "${REPO}" "$@"
