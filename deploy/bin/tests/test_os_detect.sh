#!/usr/bin/env bash
# =====================================================================
# test_os_detect.sh — 单元合约测试：OS 检测 + base dir 派生
#
# 用法：
#   bash deploy/bin/tests/test_os_detect.sh
#   或被 run-all.sh 调用
#
# 覆盖：
#   1. macOS → ${HOME}/kaixuan/openpocket
#   2. Linux → /opt/kaixuan/openpocket
#   3. WSL   → /opt/kaixuan/openpocket
#   4. Windows MSYS + /d 存在 → D:/kaixuan/openpocket
#   5. Windows MSYS + /d 缺失 + /c 存在 → C:/kaixuan/openpocket
#   6. os_kind / os_is_macos / os_is_linux_native / os_is_windows / os_is_wsl
#   7. os_normalize_path: /d/foo → D:/foo
#   8. os_path_separator: macOS/Linux=:, Windows=;
# =====================================================================

set -uo pipefail

LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../lib" && pwd)"

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

# ── 测试 1: macOS ───────────────────────────────────────────────
{
  unset __OPP_OS_DETECT_LOADED
  OPP_OS_KIND_OVERRIDE=darwin
  # shellcheck disable=SC1090
  source "${LIB_DIR}/os-detect.sh"
  expect_eq "$(os_kind)" "darwin" "macOS os_kind"
  expect_eq "$(os_detect_base_dir "/Users/test")" "/Users/test/kaixuan/openpocket" "macOS base dir"
  expect_eq "$(os_is_macos; echo $?)" "0" "macOS os_is_macos"
  expect_eq "$(os_is_windows; echo $?)" "1" "macOS os_is_windows"
  expect_eq "$(os_path_separator)" ":" "macOS PATH separator"
  unset OPP_OS_KIND_OVERRIDE __OPP_OS_DETECT_LOADED
}

# ── 测试 2: Linux ────────────────────────────────────────────────
{
  unset __OPP_OS_DETECT_LOADED
  OPP_OS_KIND_OVERRIDE=linux
  # shellcheck disable=SC1090
  source "${LIB_DIR}/os-detect.sh"
  expect_eq "$(os_kind)" "linux" "linux os_kind"
  expect_eq "$(os_detect_base_dir "/Users/test")" "/opt/kaixuan/openpocket" "linux base dir"
  expect_eq "$(os_is_linux_native; echo $?)" "0" "linux os_is_linux_native"
  expect_eq "$(os_is_wsl; echo $?)" "1" "linux not wsl"
  unset OPP_OS_KIND_OVERRIDE __OPP_OS_DETECT_LOADED
}

# ── 测试 3: WSL ─────────────────────────────────────────────────
{
  unset __OPP_OS_DETECT_LOADED
  OPP_OS_KIND_OVERRIDE=wsl
  # shellcheck disable=SC1090
  source "${LIB_DIR}/os-detect.sh"
  expect_eq "$(os_kind)" "wsl" "wsl os_kind"
  expect_eq "$(os_detect_base_dir "/home/user")" "/opt/kaixuan/openpocket" "wsl base dir"
  expect_eq "$(os_is_wsl; echo $?)" "0" "wsl os_is_wsl"
  expect_eq "$(os_is_linux_native; echo $?)" "0" "wsl is linux_native"
  unset OPP_OS_KIND_OVERRIDE __OPP_OS_DETECT_LOADED
}

# ── 测试 4: Windows MSYS + /d 存在 ──────────────────────────────
{
  unset __OPP_OS_DETECT_LOADED
  OPP_OS_KIND_OVERRIDE=windows-msys
  # shellcheck disable=SC1090
  source "${LIB_DIR}/os-detect.sh"
  expect_eq "$(os_is_windows; echo $?)" "0" "windows os_is_windows"
  expect_eq "$(os_path_separator)" ";" "windows PATH separator"
  expect_eq "$(os_normalize_path "/d/kaixuan/foo")" "D:/kaixuan/foo" "windows /d → D:"
  expect_eq "$(os_normalize_path "/c/Users/x")" "C:/Users/x" "windows /c → C:"
  expect_eq "$(os_normalize_path "/home/x/foo")" "/home/x/foo" "non-windows path passthrough"
  unset OPP_OS_KIND_OVERRIDE __OPP_OS_DETECT_LOADED
}

# ── 测试 5: 未知 OS → 默认 ${HOME}/kaixuan/openpocket ──────────
{
  unset __OPP_OS_DETECT_LOADED
  OPP_OS_KIND_OVERRIDE=plan9
  # shellcheck disable=SC1090
  source "${LIB_DIR}/os-detect.sh"
  expect_eq "$(os_kind)" "plan9" "unknown os kind passthrough"
  expect_eq "$(os_detect_base_dir "/tmp")" "/tmp/kaixuan/openpocket" "unknown OS uses home-based path"
  unset OPP_OS_KIND_OVERRIDE __OPP_OS_DETECT_LOADED
}

# ── 总结 ────────────────────────────────────────────────────────
echo
echo "━━━ test_os_detect ━━━"
printf '  PASS: %d  FAIL: %d\n' "${PASS}" "${FAIL}"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
