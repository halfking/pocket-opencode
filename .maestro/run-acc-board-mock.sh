#!/usr/bin/env bash
# 真机跑「ACC 看板 mock 验收」flow。
#
# 为什么需要这个脚本（而不是直接 `maestro test .maestro/acc-board-mock.yaml`）：
#   MIUI 会拦掉 Maestro driver 发起的**后台 activity 启动**，而 Maestro 的
#   launchApp 又是「先 force-stop、再启动」⇒ 被测包被杀掉了却拉不起来。
#   实测（2026-11-16，设备 4c308e2e / Redmi 2411DRN47C / MIUI + Android 16）：
#     - launchApp 前 pid=26494，launchApp 后 pid 为空，焦点回到 launcher；
#     - logcat: "Abort background activity starts from 10347"
#                （uid 10347 = dev.devicelab.driver.android）+ result code=102。
#   有 shell 权限的调用（am start / monkey）不受该拦截影响 ⇒ 启动交给这里，
#   flow 本身不再含 launchApp（见 acc-board-mock.yaml 的头部注释）。
#
# 用法：
#   DEVICE_ID=4c308e2e .maestro/run-acc-board-mock.sh
# 环境变量：DEVICE_ID / APP_ID / STTDEV_ID / FLOW / MAESTRO_BIN
set -euo pipefail

APP_ID="${APP_ID:-com.kaixuan.opencode.pocket}"
STTDEV_ID="${STTDEV_ID:-com.kaixuan.opencode.pocket.sttdev}"
MAESTRO_BIN="${MAESTRO_BIN:-$HOME/.maestro-runner/bin/maestro-runner}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FLOW="${FLOW:-$SCRIPT_DIR/acc-board-mock.yaml}"

if [ -z "${DEVICE_ID:-}" ]; then
  DEVICE_ID="$(adb devices | awk '$2=="device"{print $1; exit}')"
fi
if [ -z "$DEVICE_ID" ]; then
  echo "✗ 找不到在线设备（adb devices）" >&2
  exit 1
fi
if [ ! -x "$MAESTRO_BIN" ]; then
  echo "✗ 找不到 maestro 可执行文件：$MAESTRO_BIN" >&2
  exit 1
fi

echo "▶ 设备：$DEVICE_ID"

# ① 清掉同机干扰包（sttdev 带前台服务，会盖到被测包前面）
adb -s "$DEVICE_ID" shell am force-stop "$STTDEV_ID" >/dev/null 2>&1 || true

# ② 把被测包拉到前台（shell 权限 ⇒ 不受 MIUI 后台启动拦截）
if ! adb -s "$DEVICE_ID" shell am start -n "$APP_ID/.MainActivity" >/dev/null 2>&1; then
  adb -s "$DEVICE_ID" shell monkey -p "$APP_ID" -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1
fi

# ③ 等焦点真正落到被测包（前台没拿到就别开跑，flow 会白等 60s）
focused=0
for _ in $(seq 1 30); do
  focus="$(adb -s "$DEVICE_ID" shell dumpsys window 2>/dev/null | grep -m1 'mCurrentFocus' || true)"
  case "$focus" in
    *"$APP_ID"*) focused=1; break ;;
  esac
  sleep 1
done
if [ "$focused" -ne 1 ]; then
  echo "✗ 没能把 $APP_ID 拉到前台（focus=$focus）" >&2
  exit 1
fi
echo "▶ app 已在前台"

# ④ 跑 flow（报告写在**当前目录**的 reports/ 下，不落进仓）
MAESTRO_NO_DRIVER_INSTALL=true "$MAESTRO_BIN" --device "$DEVICE_ID" test "$FLOW"
