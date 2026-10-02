#!/usr/bin/env bash
# Try one DOM mutation, then run one Maestro flow, and report whether the
# device server died. Used to bisect what in our page makes UiAutomator's
# accessibility walk hang on this Android 16 / OriginOS device.
#
# Established baseline (2026-10-03, vivo V2436A):
#   native Settings app .............. 0 deaths  (driver/OS fine)
#   our Activity + about:blank ....... 0 deaths  (WebView itself fine)
#   our Activity + https://localhost/#/ai ... deaths (our page content)
#
# Usage: ./scripts/maestro-bisect.sh <label> <css-or-js-file>
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
D="${POCKET_SERIAL:?set POCKET_SERIAL}"
LABEL="$1"; MUT="$2"

export JAVA_HOME="$HOME/tools/jdk-21.0.12.1+1"
export ANDROID_HOME="$HOME/tools/android-sdk"
export PATH="$HOME/.maestro/bin:$JAVA_HOME/bin:$PATH"

adb -s "$D" shell am force-stop com.kaixuan.opencode.pocket >/dev/null 2>&1
sleep 1
adb -s "$D" shell monkey -p com.kaixuan.opencode.pocket -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1
sleep 8

POCKET_SERIAL="$D" timeout 90 node -e "
import('$ROOT/scripts/lib/adb-cdp.mjs').then(async (m) => {
  const c = await m.openCdp()
  try {
    const src = require('fs').readFileSync('$MUT','utf8')
    await c.ev(src)
    console.log('  mutation applied')
  } finally { await c.close() }
})
" 2>&1 | tail -1

cat > "$ROOT/.maestro/_bisect-probe.yaml" <<'YAML'
appId: com.kaixuan.opencode.pocket
---
- assertVisible: "ZZZ_bisect_probe"
YAML

START=$(date +%s)
timeout 170 maestro --device "$D" test --no-reinstall-driver "$ROOT/.maestro/_bisect-probe.yaml" >/tmp/bisect.log 2>&1
EL=$(( $(date +%s) - START ))
L=$(ls -t ~/.maestro/tests/ | head -1)
DEATHS=$(grep -c DeviceServerDiedException "$HOME/.maestro/tests/$L/maestro.log" 2>/dev/null || echo 0)
echo "[$LABEL] elapsed=${EL}s deaths=$DEATHS"
rm -f "$ROOT/.maestro/_bisect-probe.yaml"
