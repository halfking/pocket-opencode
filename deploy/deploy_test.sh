#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEPLOY_SCRIPT="${ROOT_DIR}/deploy/deploy.sh"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

cat >"${TMP_DIR}/unsafe.env" <<'EOF'
POCKET_ENV=production
POCKET_DEV_AUTH=true
POCKET_HTTP_PORT=8088
EOF

cat >"${TMP_DIR}/safe.env" <<'EOF'
POCKET_ENV=production
POCKET_DEV_AUTH=false
POCKET_HTTP_PORT=8088
EOF

fake_bin="${TMP_DIR}/bin"
mkdir -p "${fake_bin}"
cat >"${fake_bin}/docker" <<'EOF'
#!/usr/bin/env bash
printf 'docker called\n' >> "${DOCKER_CALL_LOG}"
exit 0
EOF
chmod +x "${fake_bin}/docker"

if POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/unsafe.env" DOCKER_CALL_LOG="${TMP_DIR}/docker.log" PATH="${fake_bin}:$PATH" \
  "${DEPLOY_SCRIPT}" --env prod >/dev/null 2>&1; then
  echo "expected unsafe production config to fail" >&2
  exit 1
fi
if [[ -s "${TMP_DIR}/docker.log" ]]; then
  echo "docker was called before unsafe config was rejected" >&2
  exit 1
fi

if ! POCKET_DEPLOY_ENV_FILE="${TMP_DIR}/safe.env" "${DEPLOY_SCRIPT}" --env prod --dry-run >/dev/null; then
  echo "expected safe production config dry-run to pass" >&2
  exit 1
fi

echo "deploy production auth guard: PASS"
