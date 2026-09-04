#!/usr/bin/env bash
# =====================================================================
# test_blue_green.sh — 单元合约测试：blue-green 版本目录管理
#
# 用法：
#   bash deploy/bin/tests/test_blue_green.sh
#
# 覆盖：
#   1. bg_init 写 bin/.gitkeep + bin/.gitignore
#   2. bg_compute_id 默认生成 {tag}-p{rev}-{ts} 形式
#   3. bg_compute_id 显式传 version+build → {version}.{build}
#   4. bg_stage 创建 bin/<id>/ 与子目录 + version.json + compose snippet
#   5. bg_switch 原子切 current → bin/<id>，previous 记录到 OPP_PREVIOUS_BUILD
#   6. bg_current 解析符号链接
#   7. bg_rollback 切回 previous，原 active 进 .failed
#   8. bg_prune 保留 N 个，剩余进 backups/bin-pruned-*
#   9. 重复 stage 同一个 id 应失败（避免覆盖）
#  10. bg_compose_snippet 路径
# =====================================================================

set -uo pipefail

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
expect_eq() {
  local actual="$1" expected="$2" label="$3"
  if [[ "${actual}" == "${expected}" ]]; then
    pass "${label}: '${actual}'"
  else
    fail "${label}: expected='${expected}' got='${actual}'"
  fi
}
expect_contains() {
  local s="$1" needle="$2" label="$3"
  if [[ "${s}" == *"${needle}"* ]]; then
    pass "${label}: contains '${needle}'"
  else
    fail "${label}: '${s}' does not contain '${needle}'"
  fi
}

# 用 tmp 目录隔离测试
TMP="$(mktemp -d -t opp-bg-test.XXXXXX)"
trap 'rm -rf "${TMP}"' EXIT

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

# 用法：run_case <base> <<<heredoc body>>>
# body 在独立 sub-shell 中跑（避免污染外层环境）
run_case() {
  local base="$1"
  (
    cd "${REPO_ROOT}"
    DEPLOY_BASE_DIR="${base}" \
    OPP_IMAGE_TAG="pocket-opp" \
    OPP_DEPLOY_VERSION="" \
    OPP_DEPLOY_BUILD="" \
    bash -s
  )
}

# ── 测试 1: bg_init ────────────────────────────────────────────
BASE1="${TMP}/case1"
mkdir -p "${BASE1}"
result="$(run_case "${BASE1}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init
  if [[ ! -f "${BIN_DIR}/.gitkeep" ]]; then echo "no gitkeep"; exit 11; fi
  if [[ ! -f "${BIN_DIR}/.gitignore" ]]; then echo "no gitignore"; exit 12; fi
  if ! grep -q "^\*$" "${BIN_DIR}/.gitignore"; then echo "gitignore wrong"; exit 13; fi
  echo "OK"
BASH_EOF
)"
expect_eq "${result}" "OK" "bg_init creates .gitkeep + .gitignore"

# ── 测试 2: bg_compute_id 默认 ─────────────────────────────────
BASE2="${TMP}/case2"
mkdir -p "${BASE2}"
COMPUTED2="$(run_case "${BASE2}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  bg_compute_id
BASH_EOF
)"
expect_contains "${COMPUTED2}" "pocket-opp-p" "bg_compute_id default contains tag+rev"

# ── 测试 3: bg_compute_id 显式 ─────────────────────────────────
BASE3="${TMP}/case3"
mkdir -p "${BASE3}"
COMPUTED3="$(run_case "${BASE3}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="1.2.3"
  OPP_DEPLOY_BUILD="005"
  bg_compute_id
BASH_EOF
)"
expect_eq "${COMPUTED3}" "1.2.3.005" "bg_compute_id explicit version.build"

# ── 测试 4: bg_stage + version.json ───────────────────────────
BASE4="${TMP}/case4"
mkdir -p "${BASE4}"
result="$(run_case "${BASE4}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="1.0.0"
  OPP_DEPLOY_BUILD="001"
  id="$(bg_compute_id)"
  bg_stage "${id}"
  if [[ ! -d "${BIN_DIR}/${id}" ]]; then echo "no dir"; exit 11; fi
  if [[ ! -d "${BIN_DIR}/${id}/migration-pre.d" ]]; then echo "no pre.d"; exit 12; fi
  if [[ ! -d "${BIN_DIR}/${id}/migration-post.d" ]]; then echo "no post.d"; exit 13; fi
  if [[ ! -f "${BIN_DIR}/${id}/version.json" ]]; then echo "no version.json"; exit 14; fi
  if [[ ! -f "${BIN_DIR}/${id}/pocketd-compose-snippet.yml" ]]; then echo "no snippet"; exit 15; fi
  if ! grep -q "\"id\": \"${id}\"" "${BIN_DIR}/${id}/version.json"; then echo "version.json id wrong"; exit 16; fi
  echo "OK"
BASH_EOF
)"
expect_eq "${result}" "OK" "bg_stage creates dir + subdirs + version.json + snippet"

# ── 测试 5: bg_switch 切 current ──────────────────────────────
BASE5="${TMP}/case5"
mkdir -p "${BASE5}"
result="$(run_case "${BASE5}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="1.0.0"
  OPP_DEPLOY_BUILD="001"
  id1="$(bg_compute_id)"
  bg_stage "${id1}" >/dev/null
  bg_switch "${id1}" >/dev/null
  if [[ ! -L "${CURRENT_LINK}" ]]; then echo "current not link"; exit 11; fi
  if [[ "$(readlink ${CURRENT_LINK})" != "${id1}" ]]; then echo "current points wrong"; exit 12; fi
  echo "OK"
BASH_EOF
)"
expect_eq "${result}" "OK" "bg_switch creates current → id1 symlink"

# 切到第二个版本，记录 previous
BASE5B="${TMP}/case5b"
mkdir -p "${BASE5B}"
PREV5="$(run_case "${BASE5B}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="1.0.0"
  OPP_DEPLOY_BUILD="001"
  id1="$(bg_compute_id)"
  bg_stage "${id1}" >/dev/null
  bg_switch "${id1}" >/dev/null
  OPP_DEPLOY_VERSION="1.0.0"
  OPP_DEPLOY_BUILD="002"
  id2="$(bg_compute_id)"
  bg_stage "${id2}" >/dev/null
  bg_switch "${id2}" >/dev/null
  printf "%s" "${OPP_PREVIOUS_BUILD}"
BASH_EOF
)"
expect_eq "${PREV5}" "1.0.0.001" "bg_switch records previous = id1"

# ── 测试 6: bg_current 解析符号链接 ──────────────────────────
BASE6="${TMP}/case6"
mkdir -p "${BASE6}"
CUR6="$(run_case "${BASE6}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="2.0.0"
  OPP_DEPLOY_BUILD="100"
  id="$(bg_compute_id)"
  bg_stage "${id}" >/dev/null
  bg_switch "${id}" >/dev/null
  bg_current
BASH_EOF
)"
expect_eq "${CUR6}" "2.0.0.100" "bg_current returns active id"

# ── 测试 7: bg_rollback 切回 previous ────────────────────────
BASE7="${TMP}/case7"
mkdir -p "${BASE7}"
result="$(run_case "${BASE7}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="1.0.0"
  OPP_DEPLOY_BUILD="001"
  id1="$(bg_compute_id)"
  bg_stage "${id1}" >/dev/null
  bg_switch "${id1}" >/dev/null
  OPP_DEPLOY_VERSION="1.0.0"
  OPP_DEPLOY_BUILD="002"
  id2="$(bg_compute_id)"
  bg_stage "${id2}" >/dev/null
  bg_switch "${id2}" >/dev/null
  rolled="$(bg_rollback)"
  if [[ "${rolled}" != "${id1}" ]]; then echo "rollback wrong: ${rolled}"; exit 11; fi
  if [[ "$(readlink ${CURRENT_LINK})" != "${id1}" ]]; then echo "current not back to id1"; exit 12; fi
  if [[ ! -d "${BIN_DIR}/${id2}.failed" ]]; then echo "id2 not marked failed"; exit 13; fi
  echo "OK"
BASH_EOF
)"
expect_eq "${result}" "OK" "bg_rollback switches current back + marks old as .failed"

# ── 测试 8: bg_prune 保留 N 个 ────────────────────────────────
BASE8="${TMP}/case8"
mkdir -p "${BASE8}"
PRUNE_RESULT="$(run_case "${BASE8}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  # 建 7 个版本目录
  for i in 1 2 3 4 5 6 7; do
    OPP_DEPLOY_VERSION="1.0.0"
    OPP_DEPLOY_BUILD="00${i}"
    id="$(bg_compute_id)"
    mkdir -p "${BIN_DIR}/${id}"
    printf "fake version\n" > "${BIN_DIR}/${id}/version.json"
  done
  total_before="$(bg_list | wc -l | tr -d ' ')"
  bg_prune 5 >/dev/null
  total_after="$(bg_list | wc -l | tr -d ' ')"
  echo "${total_before}:${total_after}"
BASH_EOF
)"
expect_eq "${PRUNE_RESULT}" "7:5" "bg_prune 5 → keeps 5, prunes 2"

# ── 测试 9: 重复 stage 同一 id 应失败 ────────────────────────
BASE9="${TMP}/case9"
mkdir -p "${BASE9}"
result="$(run_case "${BASE9}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="9.9.9"
  OPP_DEPLOY_BUILD="999"
  id="$(bg_compute_id)"
  bg_stage "${id}" >/dev/null
  if bg_stage "${id}" >/dev/null 2>&1; then
    echo "BUG: should have failed"
    exit 1
  fi
  echo "OK"
BASH_EOF
)"
expect_eq "${result}" "OK" "bg_stage refuses to overwrite existing id"

# ── 测试 10: bg_compose_snippet 路径 ─────────────────────────
BASE10="${TMP}/case10"
mkdir -p "${BASE10}"
SNIPPET_PATH="$(run_case "${BASE10}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="3.0.0"
  OPP_DEPLOY_BUILD="010"
  id="$(bg_compute_id)"
  bg_stage "${id}" >/dev/null
  # 显式传 id（不依赖 current 符号链接）
  bg_compose_snippet "${id}"
BASH_EOF
)"
expect_contains "${SNIPPET_PATH}" "bin/3.0.0.010/pocketd-compose-snippet.yml" "bg_compose_snippet returns full path"

# bg_compose_snippet 在 current 已切时也能取到
BASE10B="${TMP}/case10b"
mkdir -p "${BASE10B}"
SNIPPET_PATH_B="$(run_case "${BASE10B}" <<'BASH_EOF'
  source deploy/bin/env.sh >/dev/null 2>&1
  source "${LIB_DIR}/blue-green.sh"
  bg_init >/dev/null
  OPP_DEPLOY_VERSION="3.0.0"
  OPP_DEPLOY_BUILD="011"
  id="$(bg_compute_id)"
  bg_stage "${id}" >/dev/null
  bg_switch "${id}" >/dev/null
  # 不传 id：从 current 解析
  bg_compose_snippet
BASH_EOF
)"
expect_contains "${SNIPPET_PATH_B}" "bin/3.0.0.011/pocketd-compose-snippet.yml" "bg_compose_snippet falls back to current"

echo
echo "━━━ test_blue_green ━━━"
printf '  PASS: %d  FAIL: %d\n' "${PASS}" "${FAIL}"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
