#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_SCRIPT="${ROOT_DIR}/deploy/deploy.sh"
COMPOSE_FILE="${ROOT_DIR}/deploy/bin/docker-compose.opp.yml"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

fail() {
  echo "$1" >&2
  exit 1
}

cat >"${TMP_DIR}/unsafe.env" <<'EOF'
POCKET_ENV=production
POCKET_DEV_AUTH=true
POCKET_HTTP_PORT=8090
EOF

cat >"${TMP_DIR}/safe.env" <<'EOF'
POCKET_ENV=production
POCKET_DEV_AUTH=false
POCKET_HTTP_PORT=8090
POCKET_JWT_SECRET=contract-test-secret-32-bytes-minimum-value
POCKET_POSTGRES_DSN=postgresql://contract-test.invalid/pocket
POCKET_ALLOWED_ORIGINS=https://contract-test.invalid
POCKET_MCP_INSECURE_TLS=false
EOF

cat >"${TMP_DIR}/bound.env" <<'EOF'
POCKET_ENV=production
POCKET_DEV_AUTH=false
POCKET_HTTP_PORT=9122
POCKET_PORT_BIND_IP=172.16.2.210
POCKET_JWT_SECRET=contract-test-secret-32-bytes-minimum-value
POCKET_POSTGRES_DSN=postgresql://contract-test.invalid/pocket
POCKET_ALLOWED_ORIGINS=https://contract-test.invalid
POCKET_MCP_INSECURE_TLS=false
EOF

cat >"${TMP_DIR}/duplicate.env" <<'EOF'
POCKET_ENV=production
POCKET_DEV_AUTH=false
POCKET_HTTP_PORT=8090
POCKET_JWT_SECRET=contract-test-secret-32-bytes-minimum-value
POCKET_JWT_SECRET=overridden-secret-that-must-be-rejected
POCKET_POSTGRES_DSN=postgresql://contract-test.invalid/pocket
POCKET_ALLOWED_ORIGINS=https://contract-test.invalid
EOF

cat >"${TMP_DIR}/noncanonical.env" <<'EOF'
POCKET_ENV="production"
POCKET_DEV_AUTH=false
POCKET_HTTP_PORT=8090
POCKET_JWT_SECRET=contract-test-secret-32-bytes-minimum-value
POCKET_POSTGRES_DSN=postgresql://contract-test.invalid/pocket
POCKET_ALLOWED_ORIGINS=https://contract-test.invalid
EOF

fake_bin="${TMP_DIR}/bin"
mkdir -p "${fake_bin}"
cat >"${fake_bin}/docker" <<'EOF'
#!/usr/bin/env bash
{
  printf 'docker'
  printf ' %q' "$@"
  printf '\n'
} >> "${DOCKER_CALL_LOG}"

state_file="${FAKE_DOCKER_STATE_FILE:-${DOCKER_CALL_LOG}.state}"
init_file="${state_file}.initialized"
if [[ ! -e "${init_file}" ]]; then
  [[ -n "${FAKE_CURRENT_IMAGE:-}" ]] && printf '%s\n' "${FAKE_CURRENT_IMAGE}" > "${state_file}"
  : > "${init_file}"
fi

case "${1:-}" in
  inspect)
    [[ -s "${state_file}" ]] || exit 1
    cat "${state_file}"
    ;;
  ps)
    if [[ "${FAKE_PS_FAIL:-0}" == "1" ]]; then
      exit 1
    fi
    if [[ " $* " == *" {{.Names}} "* ]]; then
      [[ -s "${state_file}" ]] && printf 'kx-opencode-pocket\n'
    elif [[ -s "${state_file}" ]]; then
      printf 'Up 1 second\n'
    fi
    ;;
  rm)
    rm -f "${state_file}"
    ;;
  run)
    candidate_image="${*: -1}"
    printf '%s\n' "${candidate_image}" > "${state_file}"
    if [[ -n "${FAKE_FAIL_RUN_ONCE_FILE:-}" && -f "${FAKE_FAIL_RUN_ONCE_FILE}" ]]; then
      rm -f "${FAKE_FAIL_RUN_ONCE_FILE}"
      exit 1
    fi
    if [[ "${FAKE_FAIL_IMAGE:-}" == "${candidate_image}" ]]; then
      exit 1
    fi
    printf 'fake-container-id\n'
    ;;
esac
exit 0
EOF
cat >"${fake_bin}/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
printf '%s\n' "${url}" >> "${HTTP_CALL_LOG}"

if [[ "${FAKE_HTTP_ALWAYS_FAIL:-0}" == "1" ]]; then
  exit 1
fi
if [[ -n "${FAKE_HTTP_FAIL_ONCE_FILE:-}" && -f "${FAKE_HTTP_FAIL_ONCE_FILE}" ]]; then
  rm -f "${FAKE_HTTP_FAIL_ONCE_FILE}"
  exit 1
fi

expected_base="${EXPECTED_HTTP_BASE:?EXPECTED_HTTP_BASE must be set}"
case "${url}" in
  "${expected_base}/healthz") exit 0 ;;
  "${expected_base}/api/instances") printf '[]\n'; exit 0 ;;
  *) printf 'unexpected HTTP URL: %s\n' "${url}" >&2; exit 1 ;;
esac
EOF
cat >"${fake_bin}/sleep" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "${fake_bin}/docker" "${fake_bin}/curl" "${fake_bin}/sleep"

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/unsafe.env" DOCKER_CALL_LOG="${TMP_DIR}/unsafe-docker.log" PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env prod >/dev/null 2>&1; then
  fail "expected unsafe production config to fail"
fi
if [[ -s "${TMP_DIR}/unsafe-docker.log" ]]; then
  fail "docker was called before unsafe config was rejected"
fi

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/unsafe.env" DOCKER_CALL_LOG="${TMP_DIR}/unsafe-server-docker.log" PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected unsafe server config to fail"
fi
if [[ -s "${TMP_DIR}/unsafe-server-docker.log" ]]; then
  fail "docker was called before unsafe server config was rejected"
fi

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/duplicate.env" DOCKER_CALL_LOG="${TMP_DIR}/duplicate-docker.log" \
  PATH="${fake_bin}:$PATH" "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected duplicate security keys to be rejected"
fi
if [[ -s "${TMP_DIR}/duplicate-docker.log" ]]; then
  fail "docker was called before duplicate env keys were rejected"
fi

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/noncanonical.env" DOCKER_CALL_LOG="${TMP_DIR}/noncanonical-docker.log" \
  PATH="${fake_bin}:$PATH" "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected noncanonical managed env value to be rejected"
fi
if [[ -s "${TMP_DIR}/noncanonical-docker.log" ]]; then
  fail "docker was called before noncanonical env value was rejected"
fi

mkdir -p "${TMP_DIR}/locked-tracker/opencode-pocket.lock"
if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/locked-tracker" \
  DOCKER_CALL_LOG="${TMP_DIR}/locked-docker.log" PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected concurrent deployment lock to reject deploy"
fi
if [[ -s "${TMP_DIR}/locked-docker.log" ]]; then
  fail "docker was called while deployment lock was held"
fi

mkdir -p "${TMP_DIR}/inspect-fail-tracker"
if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/inspect-fail-tracker" \
  DOCKER_CALL_LOG="${TMP_DIR}/inspect-fail-docker.log" FAKE_PS_FAIL=1 PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected Docker container query failure to abort deploy"
fi
if grep -qE '^docker (stop|rm|run)' "${TMP_DIR}/inspect-fail-docker.log"; then
  fail "deploy modified containers after Docker query failure"
fi

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/missing.env" DOCKER_CALL_LOG="${TMP_DIR}/missing-deploy-docker.log" \
  PATH="${fake_bin}:$PATH" "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected deploy with explicit missing env file to fail"
fi
if [[ -s "${TMP_DIR}/missing-deploy-docker.log" ]]; then
  fail "docker was called after explicit missing env file"
fi

mkdir -p "${TMP_DIR}/forged-lock-tracker/opencode-pocket.lock"
printf '%s\n' '999999' > "${TMP_DIR}/forged-lock-tracker/opencode-pocket.lock/owner_pid"
if POCKET_DEPLOY_LOCK_HELD=1 POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
  POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/forged-lock-tracker" \
  DOCKER_CALL_LOG="${TMP_DIR}/forged-lock-docker.log" PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env server >/dev/null 2>&1; then
  fail "expected forged inherited lock to be rejected"
fi
if [[ -s "${TMP_DIR}/forged-lock-docker.log" ]]; then
  fail "docker was called after forged inherited lock"
fi

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/missing.env" POCKET_DATA_DIR="${TMP_DIR}" \
  PATH="${fake_bin}:$PATH" "${ROOT_DIR}/deploy/verify.sh" --env server >/dev/null 2>&1; then
  fail "expected server verification without env file to fail"
fi

if ! POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" "${DEPLOY_SCRIPT}" --env prod --dry-run >/dev/null; then
  fail "expected safe production config dry-run to pass"
fi

DOCKER_CALL_LOG="${TMP_DIR}/safe-docker.log" \
HTTP_CALL_LOG="${TMP_DIR}/safe-http.log" \
EXPECTED_HTTP_BASE="http://172.16.2.210:8090" \
POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/tracker" \
POCKET_DATA_DIR="${TMP_DIR}/legacy-data" \
PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env prod >/dev/null

if ! grep -q -- '-p 172.16.2.210:8090:8088' "${TMP_DIR}/safe-docker.log"; then
  fail "legacy server deploy contract missing host 172.16.2.210:8090 -> container 8088 mapping"
fi
if grep -q -- '-p 172.16.2.210:8090:8090' "${TMP_DIR}/safe-docker.log"; then
  fail "legacy deploy contract regressed to host-port -> same container-port mapping"
fi
if ! grep -q -- '-e POCKET_HTTP_PORT=8088' "${TMP_DIR}/safe-docker.log"; then
  fail "legacy deploy must force pocketd to listen on container port 8088"
fi
grep -Fxq 'http://172.16.2.210:8090/healthz' "${TMP_DIR}/safe-http.log" || \
  fail "legacy verification did not probe the default server health URL"
grep -Fxq 'http://172.16.2.210:8090/api/instances' "${TMP_DIR}/safe-http.log" || \
  fail "legacy verification did not probe the default server instances URL"

DOCKER_CALL_LOG="${TMP_DIR}/bound-docker.log" \
HTTP_CALL_LOG="${TMP_DIR}/bound-http.log" \
EXPECTED_HTTP_BASE="http://172.16.2.210:9122" \
POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/bound.env" \
POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/bound-tracker" \
POCKET_DATA_DIR="${TMP_DIR}/bound-data" \
PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env server >/dev/null

if ! grep -q -- '-p 172.16.2.210:9122:8088' "${TMP_DIR}/bound-docker.log"; then
  fail "legacy deploy did not read bind IP/port from env file"
fi
grep -Fxq 'http://172.16.2.210:9122/healthz' "${TMP_DIR}/bound-http.log" || \
  fail "legacy verification did not probe env-file bind IP/port"

DOCKER_CALL_LOG="${TMP_DIR}/direct-verify-docker.log" \
HTTP_CALL_LOG="${TMP_DIR}/direct-verify-http.log" \
EXPECTED_HTTP_BASE="http://172.16.2.210:9122" \
FAKE_CURRENT_IMAGE="registry.kxpms.cn/kaixuan-platform-opencode-pocket:verified" \
POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/bound.env" \
POCKET_DATA_DIR="${TMP_DIR}/bound-data" \
PATH="${fake_bin}:$PATH" \
  "${ROOT_DIR}/deploy/verify.sh" --env server >/dev/null

grep -Fxq 'http://172.16.2.210:9122/healthz' "${TMP_DIR}/direct-verify-http.log" || \
  fail "direct verify did not resolve env-file bind IP/port"

DOCKER_CALL_LOG="${TMP_DIR}/custom-docker.log" \
HTTP_CALL_LOG="${TMP_DIR}/custom-http.log" \
EXPECTED_HTTP_BASE="http://172.16.2.210:9123" \
POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/custom-tracker" \
POCKET_DATA_DIR="${TMP_DIR}/custom-data" \
POCKET_HTTP_PORT=9123 \
POCKET_PORT_BIND_IP=172.16.2.210 \
PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env server >/dev/null

if ! grep -q -- '-p 172.16.2.210:9123:8088' "${TMP_DIR}/custom-docker.log"; then
  fail "legacy deploy contract missing custom bind IP/port -> container 8088 mapping"
fi
grep -Fxq 'http://172.16.2.210:9123/healthz' "${TMP_DIR}/custom-http.log" || \
  fail "legacy verification did not probe the custom health URL"
grep -Fxq 'http://172.16.2.210:9123/api/instances' "${TMP_DIR}/custom-http.log" || \
  fail "legacy verification did not probe the custom instances URL"

mkdir -p "${TMP_DIR}/recovery-tracker" "${TMP_DIR}/recovery-data"
touch "${TMP_DIR}/fail-run-once"
if DOCKER_CALL_LOG="${TMP_DIR}/recovery-docker.log" \
  HTTP_CALL_LOG="${TMP_DIR}/recovery-http.log" \
  EXPECTED_HTTP_BASE="http://172.16.2.210:8090" \
  FAKE_CURRENT_IMAGE="registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable" \
  FAKE_FAIL_RUN_ONCE_FILE="${TMP_DIR}/fail-run-once" \
  POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
  POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/recovery-tracker" \
  POCKET_DATA_DIR="${TMP_DIR}/recovery-data" \
  PATH="${fake_bin}:$PATH" \
    "${DEPLOY_SCRIPT}" --env prod --tag broken-candidate >/dev/null 2>&1; then
  fail "expected failed new-container start to return failure after recovery"
fi
if ! grep -q 'registry.kxpms.cn/kaixuan-platform-opencode-pocket:broken-candidate' "${TMP_DIR}/recovery-docker.log"; then
  fail "recovery contract did not attempt the candidate image"
fi
if ! grep -q 'docker run .*registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable' "${TMP_DIR}/recovery-docker.log"; then
  fail "recovery contract did not restart the previous image"
fi
[[ "$(<"${TMP_DIR}/recovery-tracker/opencode-pocket_current_image")" == \
  "registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable" ]] || \
  fail "recovery contract did not restore current image"
[[ ! -e "${TMP_DIR}/recovery-tracker/opencode-pocket_prev_image" ]] || \
  fail "failed candidate must not remain as a rollback target"

mkdir -p "${TMP_DIR}/manual-tracker" "${TMP_DIR}/manual-data"
printf '%s\n' 'registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current' \
  > "${TMP_DIR}/manual-tracker/opencode-pocket_current_image"
printf '%s\n' 'registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable' \
  > "${TMP_DIR}/manual-tracker/opencode-pocket_prev_image"
DOCKER_CALL_LOG="${TMP_DIR}/manual-docker.log" \
HTTP_CALL_LOG="${TMP_DIR}/manual-http.log" \
EXPECTED_HTTP_BASE="http://172.16.2.210:8090" \
FAKE_CURRENT_IMAGE="registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current" \
POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/manual-tracker" \
POCKET_DATA_DIR="${TMP_DIR}/manual-data" \
PATH="${fake_bin}:$PATH" \
  "${ROOT_DIR}/deploy/rollback.sh" --env server >/dev/null

[[ "$(<"${TMP_DIR}/manual-tracker/opencode-pocket_current_image")" == \
  "registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable" ]] || \
  fail "manual rollback did not promote previous image"
[[ "$(<"${TMP_DIR}/manual-tracker/opencode-pocket_prev_image")" == \
  "registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current" ]] || \
  fail "manual rollback did not retain the rollback-before image"
if ! grep -q 'docker run .*registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable' \
  "${TMP_DIR}/manual-docker.log"; then
  fail "manual rollback did not start the complete previous image reference"
fi

mkdir -p "${TMP_DIR}/failed-rollback-tracker" "${TMP_DIR}/failed-rollback-data"
printf '%s\n' 'registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current' \
  > "${TMP_DIR}/failed-rollback-tracker/opencode-pocket_current_image"
printf '%s\n' 'registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable' \
  > "${TMP_DIR}/failed-rollback-tracker/opencode-pocket_prev_image"
if DOCKER_CALL_LOG="${TMP_DIR}/failed-rollback-docker.log" \
  HTTP_CALL_LOG="${TMP_DIR}/failed-rollback-http.log" \
  EXPECTED_HTTP_BASE="http://172.16.2.210:8090" \
  FAKE_HTTP_ALWAYS_FAIL=1 \
  FAKE_CURRENT_IMAGE="registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current" \
  POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
  POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/failed-rollback-tracker" \
  POCKET_DATA_DIR="${TMP_DIR}/failed-rollback-data" \
  PATH="${fake_bin}:$PATH" \
    "${ROOT_DIR}/deploy/rollback.sh" --env server >/dev/null 2>&1; then
  fail "expected rollback verification failure"
fi
if ! grep -q 'docker run .*registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current' \
  "${TMP_DIR}/failed-rollback-docker.log"; then
  fail "failed rollback did not restore the rollback-before image"
fi
[[ "$(<"${TMP_DIR}/failed-rollback-tracker/opencode-pocket_current_image")" == \
  "registry.kxpms.cn/kaixuan-platform-opencode-pocket:new-current" ]] || \
  fail "failed rollback changed the current image tracker"
[[ "$(<"${TMP_DIR}/failed-rollback-tracker/opencode-pocket_prev_image")" == \
  "registry.kxpms.cn/kaixuan-platform-opencode-pocket:old-stable" ]] || \
  fail "failed rollback changed the previous image tracker"

# Contract intentionally verifies that deploy.sh forwards its resolved variable.
# shellcheck disable=SC2016
grep -Fq 'POCKET_DEPLOY_ENV_FILE="${ENV_FILE}"' "${DEPLOY_SCRIPT}" || \
  fail "legacy deploy must pass its resolved env file to verify.sh"

default_http_port="$({
  unset DEPLOY_ENV DEPLOY_BASE_DIR POCKET_HTTP_PORT POCKET_PORT_BIND_IP POCKET_ENV_FILE POCKET_DEPLOY_ENV_FILE __POCKET_ENV_LOADED
  # shellcheck disable=SC1091
  HOME="${TMP_DIR}/home" source "${ROOT_DIR}/deploy/bin/env.sh"
  printf '%s' "${POCKET_HTTP_PORT}"
})"
[[ "${default_http_port}" == "8090" ]] || fail "env.sh default host port must remain 8090"

grep -Fq 'POCKET_HTTP_PORT: "8088"' "${COMPOSE_FILE}" || \
  fail "compose must force pocketd to listen on container port 8088"
# Contract intentionally matches literal compose interpolation.
# shellcheck disable=SC2016
grep -Fq '${POCKET_PORT_BIND_IP:-0.0.0.0}:${POCKET_HTTP_PORT:-8090}:8088' "${COMPOSE_FILE}" || \
  fail "compose contract missing host 8090 -> container 8088 mapping"
# Regression pattern is intentionally literal.
# shellcheck disable=SC2016
if grep -Fq '${POCKET_HTTP_PORT:-8090}:${POCKET_HTTP_PORT:-8090}' "${COMPOSE_FILE}"; then
  fail "compose contract regressed to host-port -> same container-port mapping"
fi

command -v docker >/dev/null 2>&1 || fail "docker is required for rendered compose contract"
docker compose version >/dev/null 2>&1 || fail "docker compose v2 is required for rendered compose contract"
command -v python3 >/dev/null 2>&1 || fail "python3 is required for rendered compose contract"

render_compose_contract() {
  local deploy_env="$1"
  local bind_ip="$2"
  local host_port="$3"
  local output_file="$4"

  mkdir -p "${TMP_DIR}/${deploy_env}-data" "${TMP_DIR}/${deploy_env}-logs"
  DEPLOY_ENV="${deploy_env}" \
  OPP_CONTAINER_SUFFIX=-contract \
  OPP_IMAGE_TAG=contract-test \
  POCKET_ENV_FILE="${TMP_DIR}/safe.env" \
  POCKET_DATA_DIR="${TMP_DIR}/${deploy_env}-data" \
  POCKET_LOG_DIR="${TMP_DIR}/${deploy_env}-logs" \
  POCKET_HTTP_PORT="${host_port}" \
  POCKET_PORT_BIND_IP="${bind_ip}" \
    docker compose --env-file "${TMP_DIR}/safe.env" -f "${COMPOSE_FILE}" config --format json \
      > "${output_file}"
}

assert_compose_mapping() {
  local config_file="$1"
  local expected_ip="$2"
  local expected_port="$3"

  python3 - "${config_file}" "${expected_ip}" "${expected_port}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    config = json.load(fh)

expected_ip = sys.argv[2]
expected_port = sys.argv[3]
pocketd = config["services"]["pocketd"]
ports = pocketd.get("ports", [])
expected = any(
    str(port.get("published")) == expected_port
    and int(port.get("target")) == 8088
    and port.get("host_ip") == expected_ip
    for port in ports
)
if not expected:
    raise SystemExit(f"rendered compose missing {expected_ip}:{expected_port} -> 8088 mapping")
if str(pocketd.get("environment", {}).get("POCKET_HTTP_PORT")) != "8088":
    raise SystemExit("rendered compose must force pocketd POCKET_HTTP_PORT=8088")
PY
}

render_compose_contract local 0.0.0.0 8090 "${TMP_DIR}/compose-local.json"
assert_compose_mapping "${TMP_DIR}/compose-local.json" 0.0.0.0 8090
render_compose_contract server 172.16.2.210 9123 "${TMP_DIR}/compose-server.json"
assert_compose_mapping "${TMP_DIR}/compose-server.json" 172.16.2.210 9123
echo "rendered compose local/server port matrix: PASS"

echo "deploy production auth guard: PASS"
echo "legacy port mapping 8090 -> 8088: PASS"
echo "compose port mapping 8090 -> 8088: PASS"
