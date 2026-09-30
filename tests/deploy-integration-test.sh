#!/usr/bin/env bash
# =====================================================================
# tests/deploy-integration-test.sh — 集成 dry-run 测试
#
# 验证 deploy-local.sh 在临时 base 目录下能完整跑通（dry-run 模式），
# 不真起容器，只验证：
#   1. 根 dry-run 不创建目录/密钥/容器（包括 deploy=true）
#   2. OPP_DEPLOY_PG=true → postgres/ 也建
#   3. dry-run 不创建发布版本或切换 bin/current
#   4. 现有 config/.env.local 内容不变
#   5. 154 / 245 模式能切换（root 校验在 macOS 上跳过）
#   6. dry-run --rollback 不切换版本
#
# 用法：
#   bash tests/deploy-integration-test.sh
#   或被 deploy/bin/tests/run-all.sh 调用
# =====================================================================

set -uo pipefail

PASS=0
FAIL=0
pass() { PASS=$((PASS + 1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
EVIDENCE_DIR="${OPP_TEST_EVIDENCE_DIR:-$(mktemp -d -t opp-deploy-evidence.XXXXXX)}"
mkdir -p "${EVIDENCE_DIR}"

# 临时 base 目录
TMP_BASE="$(mktemp -d -t opp-integ.XXXXXX)"
TMP_ENV="$(mktemp -t opp-integ-env.XXXXXX)"
LOG="${EVIDENCE_DIR}/integration-test.log"

cleanup() {
  rm -rf "${TMP_BASE}" "${TMP_ENV}"
}
trap cleanup EXIT

# ── 1. Full root dry-run must be read-only, including deploy=true / rollback.
echo "━━━ 1. dry-run: no directory, credential or DB mutations ━━━" | tee -a "$LOG"
for flags in '--dry-run' '--dry-run --rollback'; do
  if DEPLOY_BASE_DIR="$TMP_BASE" OPP_DEPLOY_PG=true OPP_DEPLOY_REDIS=true OPP_DEPLOY_MYSQL=true \
      bash "$REPO_ROOT/deploy-local.sh" $flags >> "$LOG" 2>&1; then
    pass "$flags exits successfully without provisioning"
  else
    fail "$flags failed"
  fi
  [[ -z "$(ls -A "$TMP_BASE")" ]] && pass "$flags left base directory empty" || fail "$flags mutated base directory"
done
# An existing environment must be byte-for-byte preserved by frontend-only plan.
mkdir -p "$TMP_BASE/config"
printf 'POCKET_JWT_SECRET=fixture-only\n' > "$TMP_BASE/config/.env.local"
cp "$TMP_BASE/config/.env.local" "$TMP_ENV"
DEPLOY_BASE_DIR="$TMP_BASE" bash "$REPO_ROOT/deploy-local.sh" --dry-run --frontend-only >> "$LOG" 2>&1
cmp -s "$TMP_ENV" "$TMP_BASE/config/.env.local" && pass 'dry-run preserved existing config' || fail 'dry-run changed config'
[[ ! -d "$TMP_BASE/bin" ]] && pass 'dry-run did not stage or switch a release' || fail 'dry-run created bin'

# ── 2. 154 模式（dry-run 在 macOS 上跳过 root 校验；只验证 env 派生）──
echo
echo "━━━ 2. dry-run: deploy-154.sh 派生 ━━━" | tee -a "${LOG}"

TMP_BASE_154="$(mktemp -d -t opp-integ-154.XXXXXX)"
(
  cd "${REPO_ROOT}"
  DEPLOY_BASE_DIR="${TMP_BASE_154}" \
  OPP_SERVER_NAME=154 DEPLOY_ENV=server \
    bash -c '
      source deploy/bin/env.sh
      echo "POCKET_PORT_BIND_IP=${POCKET_PORT_BIND_IP}"
      echo "POCKET_HTTP_PORT=${POCKET_HTTP_PORT}"
      echo "POCKET_ENV_FILE=${POCKET_ENV_FILE}"
      echo "POCKET_PROJECT_NAME=${POCKET_PROJECT_NAME}"
      echo "OPP_PG_HOST=${OPP_PG_HOST}"
    ' 2>&1 | tee -a "${LOG}"
)

[[ -f "${TMP_BASE_154}/config/.env.154" ]] && pass "154 mode: config dir pre-created via init-dirs? (won't create .env.154 since not running full deploy)" || true
# .env.154 不会被自动生成（需要手工填）；但 POCKET_ENV_FILE 应该是 .env.154
grep -q "POCKET_ENV_FILE=.*\.env\.154" <(grep "POCKET_ENV_FILE" "${LOG}" | tail -1) && pass "154 mode: POCKET_ENV_FILE = .env.154" || fail "154 mode: POCKET_ENV_FILE not .env.154"
grep -q "POCKET_PORT_BIND_IP=172.16.2.154" "${LOG}" && pass "154 mode: bind IP = 172.16.2.154" || fail "154 mode: bind IP wrong"

rm -rf "${TMP_BASE_154}"

# ── 3. 245 模式 ────────────────────────────────────────────────
echo
echo "━━━ 3. dry-run: deploy-245.sh 派生 ━━━" | tee -a "${LOG}"

TMP_BASE_245="$(mktemp -d -t opp-integ-245.XXXXXX)"
(
  cd "${REPO_ROOT}"
  DEPLOY_BASE_DIR="${TMP_BASE_245}" \
  OPP_SERVER_NAME=245 DEPLOY_ENV=server \
    bash -c '
      source deploy/bin/env.sh
      echo "POCKET_PORT_BIND_IP=${POCKET_PORT_BIND_IP}"
      echo "POCKET_HTTP_PORT=${POCKET_HTTP_PORT}"
      echo "POCKET_FRONTEND_PORT=${POCKET_FRONTEND_PORT}"
      echo "POCKET_ENV_FILE=${POCKET_ENV_FILE}"
      echo "POCKET_PROJECT_NAME=${POCKET_PROJECT_NAME}"
    ' 2>&1 | tee -a "${LOG}"
)

grep -q "POCKET_PORT_BIND_IP=172.16.2.245" "${LOG}" && pass "245 mode: bind IP = 172.16.2.245" || fail "245 mode: bind IP wrong"
grep -q "POCKET_HTTP_PORT=8091" "${LOG}" && pass "245 mode: HTTP port = 8091" || fail "245 mode: HTTP port wrong"
grep -q "POCKET_FRONTEND_PORT=4176" "${LOG}" && pass "245 mode: frontend port = 4176" || fail "245 mode: frontend port wrong"
grep -q "POCKET_ENV_FILE=.*\.env\.245" "${LOG}" && pass "245 mode: POCKET_ENV_FILE = .env.245" || fail "245 mode: POCKET_ENV_FILE not .env.245"

rm -rf "${TMP_BASE_245}"

# ── 4. OPP_DEPLOY_PG=true → postgres/ 应建（仅 init-dirs 层面；不进 ensure-databases）──
echo
echo "━━━ 4. dry-run: OPP_DEPLOY_PG=true → postgres/ 创建 ━━━" | tee -a "${LOG}"

TMP_BASE_PG="$(mktemp -d -t opp-integ-pg.XXXXXX)"
(
  cd "${REPO_ROOT}"
  # 仅跑 init-dirs.sh（不跑 deploy-local.sh 的完整链路，避免 ensure-databases 尝试起容器）
  DEPLOY_BASE_DIR="${TMP_BASE_PG}" \
  OPP_DEPLOY_PG=true OPP_DEPLOY_REDIS=false OPP_DEPLOY_MYSQL=false \
    bash deploy/bin/init-dirs.sh 2>&1 | tee -a "${LOG}" >/dev/null
)

[[ -d "${TMP_BASE_PG}/postgres" ]] && pass "OPP_DEPLOY_PG=true → postgres/ created" || fail "postgres/ missing when OPP_DEPLOY_PG=true"
[[ ! -d "${TMP_BASE_PG}/redis" ]]  && pass "redis/ correctly absent (OPP_DEPLOY_REDIS=false)" || fail "redis/ should not exist"
[[ ! -d "${TMP_BASE_PG}/mysql" ]]  && pass "mysql/ correctly absent (OPP_DEPLOY_MYSQL=false)" || fail "mysql/ should not exist"

rm -rf "${TMP_BASE_PG}"

echo
echo "━━━ integration dry-run ━━━"
printf '  PASS: %d  FAIL: %d\n' "${PASS}" "${FAIL}"
printf '  Log:  %s\n' "${LOG}"
[[ "${FAIL}" -eq 0 ]] && exit 0 || exit 1
