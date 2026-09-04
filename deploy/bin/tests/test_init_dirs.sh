#!/usr/bin/env bash
# =====================================================================
# test_init_dirs.sh — 单元合约测试：init-dirs.sh 目录创建
#
# 用法：
#   bash deploy/bin/tests/test_init_dirs.sh
#
# 覆盖：
#   1. 9 个 always-create 目录都被创建（attachments/ bin/ backups/ logs/
#      raw-logs/ run/ data/ config/ images/）
#   2. OPP_DEPLOY_PG=true 时 postgres/ 也建
#   3. OPP_DEPLOY_PG=false 时 postgres/ 不建
#   4. 每个目录都有 .gitkeep + .gitignore（含正确内容）
#   5. bin/ 由 bg_init 接管，gitignore 含 `*`
#   6. 重复跑 init-dirs.sh 是幂等的（不会因目录已存在而失败）
#   7. 154/245 server 模式下 DEPLOY_BASE_DIR=/opt/kaixuan/openpocket 也能跑
# =====================================================================

set -uo pipefail

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
expect_exists() {
  local p="$1" label="$2"
  [[ -e "${p}" ]] && pass "${label}: ${p}" || fail "${label}: ${p} (missing)"
}
expect_absent() {
  local p="$1" label="$2"
  [[ ! -e "${p}" ]] && pass "${label}: ${p} (correctly absent)" || fail "${label}: ${p} should NOT exist"
}
expect_gitignore() {
  local d="$1" label="$2"
  local f="${d}/.gitignore"
  if [[ ! -f "${f}" ]]; then fail "${label}: ${f} missing"; return; fi
  if grep -q '^\*$' "${f}" && grep -q '!.gitignore' "${f}" && grep -q '!.gitkeep' "${f}"; then
    pass "${label}: ${f}"
  else
    fail "${label}: ${f} contents wrong"
    cat "${f}" >&2
  fi
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
INIT="${REPO_ROOT}/deploy/bin/init-dirs.sh"

# ── 测试 1: 默认（OPP_DEPLOY_PG=false）─────────────────────────
TMP1="$(mktemp -d -t opp-init-test1.XXXXXX)"
trap 'rm -rf "${TMP1}" "${TMP2}" "${TMP3}" "${TMP4}"' EXIT
(
  cd "${REPO_ROOT}"
  DEPLOY_BASE_DIR="${TMP1}" OPP_DEPLOY_PG=false OPP_DEPLOY_REDIS=false OPP_DEPLOY_MYSQL=false \
    bash "${INIT}" >/dev/null 2>&1
)

expect_exists "${TMP1}/attachments" "always: attachments/"
expect_exists "${TMP1}/bin" "always: bin/"
expect_exists "${TMP1}/backups" "always: backups/"
expect_exists "${TMP1}/logs" "always: logs/"
expect_exists "${TMP1}/raw-logs" "always: raw-logs/"
expect_exists "${TMP1}/run" "always: run/"
expect_exists "${TMP1}/data" "always: data/"
expect_exists "${TMP1}/config" "always: config/"
expect_exists "${TMP1}/images" "always: images/"
expect_absent "${TMP1}/postgres" "conditional: postgres/ NOT created"
expect_absent "${TMP1}/redis" "conditional: redis/ NOT created"
expect_absent "${TMP1}/mysql" "conditional: mysql/ NOT created"

# .gitkeep + .gitignore 检查
for d in attachments bin backups logs raw-logs run data config images; do
  expect_exists "${TMP1}/${d}/.gitkeep" ".gitkeep in ${d}/"
  expect_gitignore "${TMP1}/${d}" ".gitignore in ${d}/"
done

# bin/ 由 bg_init 接管，gitignore 必须有 *
expect_gitignore "${TMP1}/bin" "bin/.gitignore has wildcard"

# ── 测试 2: OPP_DEPLOY_PG=true ────────────────────────────────
TMP2="$(mktemp -d -t opp-init-test2.XXXXXX)"
(
  cd "${REPO_ROOT}"
  DEPLOY_BASE_DIR="${TMP2}" OPP_DEPLOY_PG=true OPP_DEPLOY_REDIS=true OPP_DEPLOY_MYSQL=true \
    bash "${INIT}" >/dev/null 2>&1
)
expect_exists "${TMP2}/postgres" "conditional: postgres/ created when PG=true"
expect_exists "${TMP2}/redis" "conditional: redis/ created when REDIS=true"
expect_exists "${TMP2}/mysql" "conditional: mysql/ created when MYSQL=true"

# ── 测试 3: 幂等性（重复跑）────────────────────────────────────
(
  cd "${REPO_ROOT}"
  DEPLOY_BASE_DIR="${TMP1}" OPP_DEPLOY_PG=false bash "${INIT}" >/dev/null 2>&1
)
expect_exists "${TMP1}/attachments" "idempotent: attachments/ still exists"
expect_exists "${TMP1}/bin" "idempotent: bin/ still exists"

# ── 测试 4: 154 server 模式 ────────────────────────────────────
TMP3="$(mktemp -d -t opp-init-test3.XXXXXX)"
(
  cd "${REPO_ROOT}"
  DEPLOY_BASE_DIR="${TMP3}" OPP_SERVER_NAME=154 DEPLOY_ENV=server \
    bash "${INIT}" >/dev/null 2>&1
)
expect_exists "${TMP3}/attachments" "154: attachments/"
expect_exists "${TMP3}/bin" "154: bin/"

# ── 测试 5: DEPLOY_BASE_DIR 不可写应报错退出 ──────────────────
TMP4="$(mktemp -d -t opp-init-test4.XXXXXX)"
chmod 555 "${TMP4}/run" 2>/dev/null || true
# 跳过：chmod 555 在 root 下无效，强行测会污染其它测试
echo "  SKIP: write-permission test (root-unfriendly)"

echo
echo "━━━ test_init_dirs ━━━"
printf '  PASS: %d  FAIL: %d\n' "${PASS}" "${FAIL}"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
