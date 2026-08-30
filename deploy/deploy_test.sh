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
POCKET_JWT_SECRET=contract-test-secret-not-for-runtime
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

if [[ "${1:-}" == "ps" ]]; then
  printf 'Up 1 second\n'
fi
exit 0
EOF
cat >"${fake_bin}/curl" <<'EOF'
#!/usr/bin/env bash
url="${*: -1}"
if [[ "${url}" == */api/instances ]]; then
  printf '[]\n'
fi
exit 0
EOF
chmod +x "${fake_bin}/docker" "${fake_bin}/curl"

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

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/missing.env" POCKET_DATA_DIR="${TMP_DIR}" \
  PATH="${fake_bin}:$PATH" "${ROOT_DIR}/deploy/verify.sh" --env server >/dev/null 2>&1; then
  fail "expected server verification without env file to fail"
fi

if ! POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" "${DEPLOY_SCRIPT}" --env prod --dry-run >/dev/null; then
  fail "expected safe production config dry-run to pass"
fi

DOCKER_CALL_LOG="${TMP_DIR}/safe-docker.log" \
POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" \
POCKET_DEPLOY_TRACKER_DIR="${TMP_DIR}/tracker" \
PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env prod >/dev/null

if ! grep -q -- '-p 8090:8088' "${TMP_DIR}/safe-docker.log"; then
  fail "legacy deploy contract missing host 8090 -> container 8088 mapping"
fi
if grep -q -- '-p 8090:8090' "${TMP_DIR}/safe-docker.log"; then
  fail "legacy deploy contract regressed to host-port -> same container-port mapping"
fi
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

if docker compose version >/dev/null 2>&1; then
  mkdir -p "${TMP_DIR}/compose-data" "${TMP_DIR}/compose-logs"
  DEPLOY_ENV=local \
  OPP_CONTAINER_SUFFIX=-contract \
  OPP_IMAGE_TAG=contract-test \
  POCKET_ENV_FILE="${TMP_DIR}/safe.env" \
  POCKET_DATA_DIR="${TMP_DIR}/compose-data" \
  POCKET_LOG_DIR="${TMP_DIR}/compose-logs" \
  POCKET_HTTP_PORT=8090 \
  POCKET_PORT_BIND_IP=0.0.0.0 \
    docker compose --env-file "${TMP_DIR}/safe.env" -f "${COMPOSE_FILE}" config --format json \
      > "${TMP_DIR}/compose.json"

  python3 - "${TMP_DIR}/compose.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as fh:
    config = json.load(fh)

pocketd = config["services"]["pocketd"]
ports = pocketd.get("ports", [])
expected = any(
    str(port.get("published")) == "8090"
    and int(port.get("target")) == 8088
    and port.get("host_ip") == "0.0.0.0"
    for port in ports
)
if not expected:
    raise SystemExit("rendered compose missing 0.0.0.0:8090 -> 8088 mapping")
if str(pocketd.get("environment", {}).get("POCKET_HTTP_PORT")) != "8088":
    raise SystemExit("rendered compose must force pocketd POCKET_HTTP_PORT=8088")
PY
  echo "rendered compose port mapping 8090 -> 8088: PASS"
else
  echo "rendered compose contract: SKIP (docker compose unavailable)"
fi

echo "deploy production auth guard: PASS"
echo "legacy port mapping 8090 -> 8088: PASS"
echo "compose port mapping 8090 -> 8088: PASS"
