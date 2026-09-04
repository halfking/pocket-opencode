#!/usr/bin/env bash
# =====================================================================
# run-all.sh — 跑全部 deploy/bin 单元合约测试 + 集成 dry-run
#
# 用法：
#   bash deploy/bin/tests/run-all.sh
#
# 退出码：所有测试都 PASS 则 0，否则 1
# =====================================================================

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
INTEGRATION_TEST="${REPO_ROOT}/../tests/deploy-integration-test.sh"
[[ ! -f "${INTEGRATION_TEST}" ]] && INTEGRATION_TEST="${REPO_ROOT}/tests/deploy-integration-test.sh"

TOTAL_PASS=0
TOTAL_FAIL=0
FAILED_TESTS=()

for t in "${SCRIPT_DIR}"/test_*.sh; do
  name="$(basename "${t}")"
  echo
  echo "════════════════════════════════════════════════════════════════"
  echo "  ▶ ${name}"
  echo "════════════════════════════════════════════════════════════════"
  if ! bash "${t}"; then
    FAILED_TESTS+=("${name}")
  fi
done

# 集成 dry-run
echo
echo "════════════════════════════════════════════════════════════════"
echo "  ▶ tests/deploy-integration-test.sh"
echo "════════════════════════════════════════════════════════════════"
if ! bash "${INTEGRATION_TEST}"; then
  FAILED_TESTS+=("deploy-integration-test.sh")
fi

echo
echo "════════════════════════════════════════════════════════════════"
echo "  ▶ 单元合约测试 + 集成 dry-run 汇总"
echo "════════════════════════════════════════════════════════════════"

for t in "${SCRIPT_DIR}"/test_*.sh "${INTEGRATION_TEST}"; do
  name="$(basename "${t}")"
  out="$(bash "${t}" 2>&1 | tail -5)"
  pass_line="$(echo "${out}" | grep -E 'PASS:.*FAIL:' | tail -1)"
  echo "  ${name}: ${pass_line:-<no summary>}"
done

if [[ ${#FAILED_TESTS[@]} -eq 0 ]]; then
  echo
  echo "✅ 全部测试通过"
  exit 0
else
  echo
  echo "❌ 失败的测试: ${FAILED_TESTS[*]}"
  exit 1
fi
