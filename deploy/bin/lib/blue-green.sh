#!/usr/bin/env bash
# =====================================================================
# blue-green.sh — OpenPocket 部署的版本目录管理（blue-green 切换）
#
# 设计目标（用户需求）：
#   - bin/{version}.{build}/  每个发布版本独立目录
#   - bin/current            符号链接指向当前活跃版本
#   - 切换是原子的（ln -sfn）
#   - 健康检查通过才切换；失败自动回滚到上一个 verified 版本
#   - 旧版本可被 prune（默认保留最近 5 个），被 prune 的进 backups/bin-pruned-*
#
# 公开函数：
#   bg_init                      初始化 bin/ 与 bin/.gitkeep（如果不存在）
#   bg_compute_id [version] [build]
#                                计算并 export OPP_VERSION_BUILD（默认用 git rev-parse + 时间戳）
#   bg_stage <id>                创建 bin/<id>/ 与子目录（compose snippet / hooks / version.json）
#   bg_switch <id> [previous]    原子切换 bin/current → bin/<id>
#   bg_current                   打印当前活跃版本 id（解析 bin/current 符号链接）
#   bg_list                      列出 bin/*-<id>/* 目录，按 mtime 倒序
#   bg_rollback                  把 bin/current 指回上一个 verified 版本（读 version.json）
#   bg_prune [keep=5]            保留最近 N 个版本，其余移到 backups/bin-pruned-<ts>/
#   bg_compose_snippet <id>      输出 bin/<id>/pocketd-compose-snippet.yml 路径（不存在则空）
#
# 用法：
#   source "$(dirname "${BASH_SOURCE[0]}")/lib/blue-green.sh"
#   bg_init
#   id="$(bg_compute_id)"
#   bg_stage "${id}"
#   bg_switch "${id}" "${previous:-}"
# =====================================================================

if [[ -n "${__OPP_BG_LOADED:-}" ]]; then
  return 0 2>/dev/null || true
fi
__OPP_BG_LOADED=1

# 依赖 env.sh 提供 DEPLOY_BASE_DIR（必须在 source env.sh 后再 source 本文件）
: "${DEPLOY_BASE_DIR:?DEPLOY_BASE_DIR must be set before sourcing blue-green.sh}"

BIN_DIR="${DEPLOY_BASE_DIR}/bin"
CURRENT_LINK="${BIN_DIR}/current"
BACKUPS_DIR="${DEPLOY_BASE_DIR}/backups"

_bg_log() {
  echo "[blue-green] $*" >&2
}

bg_init() {
  mkdir -p "${BIN_DIR}"
  [[ -f "${BIN_DIR}/.gitkeep" ]] || : > "${BIN_DIR}/.gitkeep"
  # 写一份 .gitignore，防止误提交运行时产物
  cat > "${BIN_DIR}/.gitignore" <<'EOF'
*
!.gitignore
!.gitkeep
EOF
}

# 计算版本目录 id。规则：
#   1) 显式 OPP_DEPLOY_VERSION + OPP_DEPLOY_BUILD  → {version}.{build}
#   2) 否则用 git rev-parse --short HEAD + epoch 秒的后 4 位 → {tag}-p{rev}-{ts}
#   3) 非 git 仓库（如临时 tarball）→ local-pXXXXXX-YYYYMMDDHHMMSS
bg_compute_id() {
  local version="${1:-${OPP_DEPLOY_VERSION:-}}"
  local build="${2:-${OPP_DEPLOY_BUILD:-}}"

  if [[ -n "${version}" && -n "${build}" ]]; then
    OPP_VERSION_BUILD="${version}.${build}"
    export OPP_VERSION_BUILD
    printf '%s' "${OPP_VERSION_BUILD}"
    return 0
  fi

  local rev="local"
  if command -v git >/dev/null 2>&1 && \
     git -C "${REPO_ROOT:-.}" rev-parse --short HEAD >/dev/null 2>&1; then
    rev="$(git -C "${REPO_ROOT:-.}" rev-parse --short HEAD)"
  fi
  local tag="${OPP_IMAGE_TAG:-pocket-opp}"
  local ts
  ts="$(date +%Y%m%d%H%M%S)"
  OPP_VERSION_BUILD="${tag}-p${rev}-${ts}"
  export OPP_VERSION_BUILD
  printf '%s' "${OPP_VERSION_BUILD}"
}

# 创建一个 bin/<id>/ 子目录并填好骨架文件
bg_stage() {
  local id="${1:-${OPP_VERSION_BUILD:-}}"
  [[ -n "${id}" ]] || { echo "❌ bg_stage: 缺少 version.build id" >&2; return 1; }

  local target="${BIN_DIR}/${id}"
  if [[ -e "${target}" ]]; then
    echo "❌ bin/${id} 已存在，拒绝覆盖（清理后重跑，或换一个新 id）" >&2
    return 1
  fi

  mkdir -p "${target}/migration-pre.d" "${target}/migration-post.d"

  # 写 version.json：保存上一次活跃版本，便于回滚
  local previous=""
  if [[ -L "${CURRENT_LINK}" ]]; then
    previous="$(readlink "${CURRENT_LINK}" || true)"
  fi
  local commit="unknown"
  if command -v git >/dev/null 2>&1 && \
     git -C "${REPO_ROOT:-.}" rev-parse --short HEAD >/dev/null 2>&1; then
    commit="$(git -C "${REPO_ROOT:-.}" rev-parse --short HEAD)"
  fi

  cat > "${target}/version.json" <<EOF
{
  "id": "${id}",
  "version": "${OPP_DEPLOY_VERSION:-}",
  "build": "${OPP_DEPLOY_BUILD:-}",
  "image_tag": "${OPP_IMAGE_TAG:-pocket-opp}",
  "commit": "${commit}",
  "deployed_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "previous": "${previous}",
  "host": "$(uname -n 2>/dev/null || echo unknown)",
  "deploy_env": "${DEPLOY_ENV:-local}"
}
EOF

  # 写一份 pocketd-compose-snippet.yml：用于 docker compose -f ... -f snippet.yml 拼接
  cat > "${target}/pocketd-compose-snippet.yml" <<EOF
# OpenPocket release ${id} — 由 deploy/bin/lib/blue-green.sh 生成
# 用法：docker compose -f docker-compose.opp.yml -f bin/current/pocketd-compose-snippet.yml up
# 当前内容是占位（snippet 仅在需要时启用额外服务）
version: "3.9"
services: {}
EOF

  echo "${id}" > "${target}/.deployed-by"
  _bg_log "staged bin/${id}/"
}

# 原子切换 current → id；可选 previous 用于回滚
bg_switch() {
  local id="${1:-}"
  [[ -n "${id}" ]] || { echo "❌ bg_switch: 缺少 id" >&2; return 1; }

  local target="${BIN_DIR}/${id}"
  [[ -d "${target}" ]] || { echo "❌ bin/${id} 不存在（先 bg_stage）" >&2; return 1; }

  # 记录回滚目标（如果调用方没给）
  local previous=""
  if [[ -L "${CURRENT_LINK}" ]]; then
    previous="$(readlink "${CURRENT_LINK}" || true)"
  fi

  ln -sfn "${id}" "${CURRENT_LINK}"
  _bg_log "bin/current → ${id}（previous=${previous:-<none>}）"

  # 落地 started_at / last_healthy_at 到 version.json
  local started_at
  started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  # 用 python 安全地改 JSON 字段；如果没有 python 则直接追加
  if command -v python3 >/dev/null 2>&1; then
    python3 - <<PYEOF 2>/dev/null || true
import json, sys, pathlib
p = pathlib.Path("${target}/version.json")
try:
    data = json.loads(p.read_text())
except Exception:
    data = {}
data["started_at"] = "${started_at}"
data["last_healthy_at"] = None
data["active"] = True
p.write_text(json.dumps(data, indent=2, ensure_ascii=False))
PYEOF
  fi

  OPP_PREVIOUS_BUILD="${previous}"
  export OPP_PREVIOUS_BUILD
  printf '%s' "${id}"
}

# 打印当前活跃版本 id（解析符号链接；current 缺失则空）
bg_current() {
  if [[ -L "${CURRENT_LINK}" ]]; then
    readlink "${CURRENT_LINK}"
  fi
}

# 列出版本目录，按 mtime 倒序（最新在前）。stdout 每行一个 id
# 注意：用 command find 绕过 zcode 的 bfs wrapper（bfs 不支持 -printf）。
bg_list() {
  [[ -d "${BIN_DIR}" ]] || return 0
  # 使用 ls + sort 实现（避免依赖 find -printf；macOS BSD 与 Linux GNU 都支持）
  # 排序按修改时间倒序，最新的在前
  ls -1t "${BIN_DIR}/" 2>/dev/null \
    | grep -v '^\.gitkeep$' \
    | while read -r entry; do
        [[ -d "${BIN_DIR}/${entry}" ]] && printf '%s\n' "${entry}"
      done
}

# 回滚到上一个版本（读 current/version.json 的 previous）
bg_rollback() {
  if [[ ! -L "${CURRENT_LINK}" ]]; then
    echo "❌ bin/current 不是符号链接，无法回滚" >&2
    return 1
  fi
  local current
  current="$(readlink "${CURRENT_LINK}")"
  local previous=""
  if [[ -f "${BIN_DIR}/${current}/version.json" ]]; then
    if command -v python3 >/dev/null 2>&1; then
      previous="$(python3 -c "import json,sys; print(json.load(open('${BIN_DIR}/${current}/version.json')).get('previous',''))" 2>/dev/null || true)"
    else
      previous="$(grep -o '"previous"[[:space:]]*:[[:space:]]*"[^"]*"' "${BIN_DIR}/${current}/version.json" 2>/dev/null | sed 's/.*"\(.*\)"$/\1/')"
    fi
  fi

  if [[ -z "${previous}" ]] || [[ ! -d "${BIN_DIR}/${previous}" ]]; then
    echo "❌ 无可回滚的上一版本（previous='${previous}'，目录不存在）" >&2
    return 1
  fi

  ln -sfn "${previous}" "${CURRENT_LINK}"
  _bg_log "rollback: bin/current → ${previous}（原 ${current}）"

  # 把失败的版本加 .failed 后缀
  if [[ -d "${BIN_DIR}/${current}" ]] && [[ "${current}" != "${previous}" ]]; then
    if [[ ! -e "${BIN_DIR}/${current}.failed" ]]; then
      mv "${BIN_DIR}/${current}" "${BIN_DIR}/${current}.failed"
      _bg_log "失败版本已移到 bin/${current}.failed/"
    fi
  fi

  printf '%s' "${previous}"
}

# 保留最近 keep 个版本，其余移到 backups/bin-pruned-<ts>/
bg_prune() {
  local keep="${1:-5}"
  [[ -d "${BIN_DIR}" ]] || return 0

  local total
  total="$(bg_list | wc -l | tr -d ' ')"
  if (( total <= keep )); then
    _bg_log "prune: 当前 ${total} 个版本，无需清理（keep=${keep}）"
    return 0
  fi

  local pruned=0
  local archive="${BACKUPS_DIR}/bin-pruned-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "${archive}"

  # bg_list 是按 mtime 倒序；保留前 keep 个，剩下的是要 prune 的
  local to_prune
  to_prune="$(bg_list | tail -n +$((keep + 1)))"
  while IFS= read -r id; do
    [[ -n "${id}" ]] || continue
    # 不动 current 指向的版本与 .failed 后缀的版本
    [[ -e "${BIN_DIR}/${id}" ]] || continue
    if [[ "$(readlink "${CURRENT_LINK}" 2>/dev/null || true)" == "${id}" ]]; then
      _bg_log "skip prune: ${id} is current"
      continue
    fi
    if [[ "${id}" == *.failed ]]; then
      _bg_log "skip prune: ${id} already marked failed"
      continue
    fi
    mv "${BIN_DIR}/${id}" "${archive}/" && pruned=$((pruned + 1))
  done <<<"${to_prune}"
  _bg_log "prune: moved ${pruned} → ${archive}"
}

# 输出 compose snippet 路径（不在则空字符串）
bg_compose_snippet() {
  local id="${1:-$(bg_current)}"
  [[ -n "${id}" ]] || { printf ''; return 0; }
  local f="${BIN_DIR}/${id}/pocketd-compose-snippet.yml"
  [[ -f "${f}" ]] && printf '%s' "${f}" || printf ''
}

# 健康检查后的回调：把 last_healthy_at 写入 version.json
bg_mark_healthy() {
  local id="${1:-$(bg_current)}"
  [[ -n "${id}" ]] || return 0
  local f="${BIN_DIR}/${id}/version.json"
  [[ -f "${f}" ]] || return 0
  if command -v python3 >/dev/null 2>&1; then
    local ts
    ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    python3 - <<PYEOF 2>/dev/null || true
import json, pathlib
p = pathlib.Path("${f}")
try:
    data = json.loads(p.read_text())
except Exception:
    data = {}
data["last_healthy_at"] = "${ts}"
p.write_text(json.dumps(data, indent=2, ensure_ascii=False))
PYEOF
  fi
}
