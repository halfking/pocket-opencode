#!/usr/bin/env bash
# openpocket 真机全量回归 —— **逐条跑**，每条前强制校验前台。
#
# 为什么不能一次 `maestro-run.mjs flow1 flow2 …`（那才是原本的跑法）：
#   本机同时装着 com.kaixuan.opencode.pocket 与 …pocket.sttdev，
#   两者 MainActivity 同名。sttdev **有保活**：实测 force-stop 之后（它有保活，会自己回来）
#   它仍被外部重新启用并夺回前台（disabled 列表变空、新 pid 重新出现）。
#   ⇒ 「禁用并存包」这个前置**在这台设备上不持久**，
#     一次性跑 22 条时，中途某条的前台可能已经不是被测 App，
#     而 Maestro **不会为此报错** —— 断言会落在另一个 App 上，
#     报出来是「某元素不可见」，与「App 没这个功能」同形。
#   实测 _goto-pkm 就这样失败过（"主导航" is visible）。
#
# 所以这里改成：**一条一个 maestro 调用**，每条之前
#   ① 再次 force-stop 同族包（**不** disable-user，见函数注释里的二分实测）
#   ② **读回 mCurrentFocus 确认它真的属于被测包**（不确认就是环境脏，不硬跑）
#   ③ 确认不对就重新拉起 App，仍不对则把该条记为 SKIP_ENV（不是 FAIL）
#
# 跑完请手动恢复：adb shell pm enable com.kaixuan.opencode.pocket.sttdev
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"

# ─────────────────────────────────────────────────────────────
# 2026-10-06：maestro driver 卡死要单列一类，别记成 FAIL。
#
# 实测（Redmi 4c308e2e，全程在线，meetings-entry）：
#   控制台只有 `[Failed] meetings-entry (2m 12s)`，**一个断言字样都没有**。
# 真因在 maestro 的产物里：
#   DEADLINE_EXCEEDED: deadline exceeded after 119.999874916s
#     at MaestroDriverGrpc$…BlockingStub.deviceInfo
# driver 的 gRPC（adb forward 到 127.0.0.1:7001）整个不响应，前置 RPC
# deviceInfo 120s 不返回 ⇒ **那条 tap 根本没被求值**。
#
# 为什么必须单列：形态与「断言不成立」完全同形（非 0 退出 + [Failed]），
# 但它既不是产品缺陷也不是 flow 缺陷。记成 FAIL 会让人去查没坏的东西。
#
# ★ 为什么读 ~/.maestro/tests 而不是 --output：
#   实测（最小 flow 单独验证）**maestro 这个版本不把命令产物写进 --output**，
#   目录预建也不写；它固定落在 ~/.maestro/tests/<时间戳>/。
#   而它**一次运行可能建不止一个**目录（实测 052317/052318 一对、
#   052418/052419 一对）⇒ 只能靠「跑批前后对拍目录差集」认领，
#   不能取「最新的一个」。
#   （因此这个修复**不需要碰 maestro-run.mjs**，那个文件是 CRLF，改它风险高。）
#
# ⚠️ 已知局限：若有**另一个 maestro 进程并发**在跑，差集会把它的目录也算进来。
#   本跑批严格串行，串行下差集即本轮产物；并发场景需要更严格的认领。
MAESTRO_TESTS_DIR="${POCKET_MAESTRO_TESTS_DIR:-$HOME/.maestro/tests}"

tests_snapshot() { ls -1 "$MAESTRO_TESTS_DIR" 2>/dev/null | sort; }

# 参数：若干个 maestro 产物目录名。任一目录里的任一 commands-*.json
# 同时含 DEADLINE_EXCEEDED 与 deviceInfo ⇒ 判为 driver 卡死。
# 两个条件都要，是为了不把普通超时/重试误判成 driver 故障。
driver_hung() {
  local d f hit=1
  for d in "$@"; do
    [ -n "$d" ] || continue
    for f in "$MAESTRO_TESTS_DIR/$d"/commands-*.json; do
      [ -e "$f" ] || continue
      if grep -q 'DEADLINE_EXCEEDED' "$f" && grep -q 'deviceInfo' "$f"; then
        echo "      ↳ driver 卡死证据：$d/$(basename "$f")"
        hit=0
      fi
    done
  done
  return $hit
}

# --selftest：两臂验证，缺一不可。
#   正臂 = driver 卡死形态；负臂 = **真实断言失败形态**。
#   没有负臂，这条规则就是个「什么都归 SKIP_ENV」的假判据，
#   会把真产品缺陷一起吞掉 —— 那比原来的误判更糟。
if [ "${1:-}" = "--selftest" ]; then
  T="$(mktemp -d)"; rc=0
  mkdir -p "$T/drv" "$T/assertfail" "$T/half" "$T/passrun"
  printf '%s\n' '[{"metadata":{"status":"FAILED","error":{"message":"DEADLINE_EXCEEDED: deadline exceeded after 119.9s","stackTrace":[{"className":"maestro_android.MaestroDriverGrpc$MaestroDriverBlockingStub","methodName":"deviceInfo"}]}}}]' > "$T/drv/commands-a.json"
  printf '%s\n' '[{"metadata":{"status":"FAILED","error":{"message":"Assertion is not true: element not visible"}}}]' > "$T/assertfail/commands-a.json"
  printf '%s\n' '[{"metadata":{"status":"FAILED","error":{"message":"DEADLINE_EXCEEDED: deadline exceeded"}}}]' > "$T/half/commands-a.json"
  printf '%s\n' '[{"metadata":{"status":"COMPLETED"}},{"metadata":{"status":"WARNED"}},{"metadata":{"status":"COMPLETED"}}]' > "$T/passrun/commands-a.json"
  old="$MAESTRO_TESTS_DIR"; MAESTRO_TESTS_DIR="$T"
  driver_hung drv && echo "  ✅ 正臂：driver 卡死被识别" || { echo "  ❌ 正臂：该认的没认"; rc=1; }
  driver_hung assertfail && { echo "  ❌ 负臂：真实断言失败被误吞"; rc=1; } || echo "  ✅ 负臂：真实断言失败仍判 FAIL"
  driver_hung half && { echo "  ❌ 负臂：签名不完整被误判"; rc=1; } || echo "  ✅ 负臂：签名不完整不误判"
  driver_hung passrun && { echo "  ❌ 负臂：正常通过的一轮被误判"; rc=1; } || echo "  ✅ 负臂：正常通过的一轮不误判"
  driver_hung nonexistent-dir && { echo "  ❌ 负臂：无产物目录被判 driver"; rc=1; } || echo "  ✅ 负臂：无产物目录不误判"
  MAESTRO_TESTS_DIR="$old"
  exit $rc
fi

SERIAL="${POCKET_SERIAL:-}"
PKG="${POCKET_APP_ID:-com.kaixuan.opencode.pocket}"
[ -n "$SERIAL" ] || { echo "[run] 需要 POCKET_SERIAL" >&2; exit 2; }
LOGDIR="${POCKET_LOGDIR:-/tmp/opp-maestro}"
mkdir -p "$LOGDIR"

# ── ANDROID_HOME 缺失 ⇒ Maestro 看不见任何 Android 设备 ──────────────────
# 2026-10-07 实测：ANDROID_HOME 未设时
#   `maestro --udid emulator-5554 test x.yaml` 直接报
#   「Device emulator-5554 was requested, but it is not connected.」，
# 而同一时刻 `adb devices` 明明显示它是 `device` 状态、shell 也通。
# ⇒ 症状与「设备没连上」**完全同构**，真因却在调用方环境。
#
# 为什么必须在这里兜住：这个错误形态会被 rig 的 driver-hung 判定吞成
# SKIP_ENV（deviceInfo 拿不到 ⇒ 记「环境不行」而非 FAIL），
# 于是整批 flow 被当成环境问题跳过——一轮验收什么都没验到，
# 却看不出是环境配置漏了。放在跑批入口修一次，比逐条 flow 排查便宜得多。
#
# 尊重已有设置：变量已指到**真实存在**的 SDK 就不动（用户可能指向别的 SDK）。
# 注意判据是「目录真的存在」而不是「变量非空」——ANDROID_HOME 指向一个
# 不存在的路径时，Maestro 一样看不见设备，症状与没设时完全相同。
# 只判非空会把这种坏配置放过去（实测：设成 /nonexistent-xxx 时整批 flow
# 全被记成 exit=2，仍然看不出是环境问题）。
if [ ! -d "${ANDROID_HOME:-}/platform-tools" ]; then
  for _sdk in "${ANDROID_HOME:-}" "$HOME/Library/Android/sdk" "$HOME/Android/Sdk" "/usr/local/share/android-sdk"; do
    if [ -n "$_sdk" ] && [ -d "$_sdk/platform-tools" ]; then
      export ANDROID_HOME="$_sdk"
      export ANDROID_SDK_ROOT="$_sdk"
      break
    fi
  done
  unset _sdk
fi
if [ ! -d "${ANDROID_HOME:-}/platform-tools" ]; then
  echo "[run] 找不到可用的 Android SDK（ANDROID_HOME=${ANDROID_HOME:-<未设>}，其下没有 platform-tools）" >&2
  echo "       Maestro 会把设备误判成「未连接」，这批 flow 会全被记成 exit=2。" >&2
  echo "       请 export ANDROID_HOME=<你的 SDK 路径> 后重试。" >&2
  exit 2
fi

# ★ 2026-10-07 修一处既有隐患：`$PKG（`（全角括号紧跟）会被 bash 并进变量名，
#   在 UTF-8 locale 下找的是 `PKG（` 这个不存在的变量 ⇒ set -u 直接致命，
#   脚本在**批次中途**死掉：不打印汇总、后续 flow 全部静默丢失。
#   只在「设备不可用」这条 SKIP_ENV 路径上触发，所以平时看不出来。
#   复现：POCKET_SERIAL=<不存在的设备> bash scripts/maestro-run-all.sh <flow>
#         （LC_ALL=C 下不触发 ⇒ 是 locale 依赖，别当成偶发）
#   ⇒ 这类「$VAR + 全角标点」一律写成 ${VAR}。
focus_owner() { adb -s "$SERIAL" shell dumpsys window 2>/dev/null | grep mCurrentFocus | head -1; }

# 强制前台归位；成功返回 0
# ★ 2026-10-06 **不要**在这里 `pm disable-user` 同族包。二分实测：
#   加上它 ⇒ 流程从 2/2 通过变成失败（连续两次，报 fetch 超时 /「打开菜单」不可见）；
#   去掉它 ⇒ 恢复通过。包状态变更会打断正在起来的被测 App。
#   而且它本来也不持久（跑批中途被外部重新启用并夺回前台）。
#
# ★ 2026-10-06 **先验后动**，不要无条件重启：
#   harness 自己的 preflight 也会 force-stop + 重启 App。若这里每条再无条件重启一次，
#   等于**每条 flow 被重启两遍**，CDP forward 指向刚被杀掉的进程时
#   /json/list 会挂成 UND_ERR_HEADERS_TIMEOUT（实测 _goto-pkm 就是这样红的）。
#   ⇒ 前台已经对就直接返回，只有确实不对才动手。
ensure_foreground() {
  # 快路径：前台已经是被测包，什么都不做
  case "$(focus_owner)" in *"$PKG"*) return 0 ;; esac
  for i in 1 2 3; do
    for p in $(adb -s "$SERIAL" shell pm list packages "$PKG" 2>/dev/null | tr -d '\r' | sed 's/^package://'); do
      [ "$p" = "$PKG" ] && continue
      adb -s "$SERIAL" shell am force-stop "$p" >/dev/null 2>&1
    done
    adb -s "$SERIAL" shell am force-stop "$PKG" >/dev/null 2>&1
    adb -s "$SERIAL" shell monkey -p "$PKG" -c android.intent.category.LAUNCHER 1 >/dev/null 2>&1
    for w in 1 2 3 4 5 6; do
      sleep 2
      case "$(focus_owner)" in
        *"$PKG"*) return 0 ;;
      esac
    done
  done
  return 1
}

declare -a FLOWS=("$@")
[ ${#FLOWS[@]} -gt 0 ] || FLOWS=($(ls -1 "$ROOT"/.maestro/*.yaml))

PASS=0; FAIL=0; SKIP=0
RESULTS="$LOGDIR/summary.txt"; : > "$RESULTS"

for f in "${FLOWS[@]}"; do
  name=$(basename "$f" .yaml)
  printf '\n======== %s ========\n' "$name"
  if ! ensure_foreground; then
    echo "[run] ❌ $name 跳过：前台拿不到 ${PKG}（最后焦点：$(focus_owner)）"
    printf 'SKIP_ENV  %s\n' "$name" >> "$RESULTS"; SKIP=$((SKIP+1)); continue
  fi
  # ★ 每条都走**完整前置**（真实登录 + 起点归位），不用 POCKET_SKIP_CDP_LOGIN 省钱。
  #   2026-10-05 踩过：第二条用 SKIP_CDP_LOGIN=1「假定还登着」，
  #   结果 notes-crud 的现场截图直接是**登录页**（密码框空），
  #   断言报「首页 is visible」—— 看着像功能缺失，实为起点不成立。
  #   起点是**每条 flow 的契约**，跨 flow 沿用等于把假设当事实。
  #   （同一条纪律在 smm-client 侧叫「起点契约必须一致」。）

  __before="$(tests_snapshot)"
  node "$ROOT/scripts/maestro-run.mjs" "$ROOT/$f" > "$LOGDIR/$name.log" 2>&1
  rc=$?
  __after="$(tests_snapshot)"
  # ★ 2026-10-06 设备掉线要单独归类，别记成 FAIL。
  #   实测：真机跑到一半 USB 断了一下，maestro 报
  #   "Device 4c308e2e was requested, but it is not connected."
  #   然后退出非 0 —— 形态与「断言不成立」同形，但它**不是产品或 flow 的问题**。
  #   记成 FAIL 会让人去查一个根本没坏的东西（与这轮反复修的
  #   「基础设施伪装成产品缺陷」是同一个漏洞，只是方向反过来）。
  if [ "$rc" = 0 ]; then
    echo "[run] ✅ $name"; printf 'PASS      %s\n' "$name" >> "$RESULTS"; PASS=$((PASS+1))
  elif __newdirs="$(comm -13 <(printf '%s\n' "$__before") <(printf '%s\n' "$__after") | grep -v '^$')" \
       && [ -n "$__newdirs" ] && driver_hung $__newdirs; then
    echo "[run] ⚠️  $name 跳过：maestro driver gRPC 卡死（基础设施，不是产品/flow 问题）"
    printf 'SKIP_ENV  %s\n' "$name" >> "$RESULTS"; SKIP=$((SKIP+1))
  elif grep -qE 'but it is not connected|device .* not found|device offline' "$LOGDIR/$name.log" 2>/dev/null; then
    echo "[run] ⚠️  $name 跳过：设备掉线（基础设施，不是产品/flow 问题）"
    printf 'SKIP_ENV  %s\n' "$name" >> "$RESULTS"; SKIP=$((SKIP+1))
  else
    echo "[run] ❌ $name (exit=$rc)"
    grep -E "Assertion|FAILED|not found|Timeout" "$LOGDIR/$name.log" | head -3 | sed 's/^/      /'
    printf 'FAIL      %s\n' "$name" >> "$RESULTS"; FAIL=$((FAIL+1))
  fi

done

echo
echo "================ 汇总 ================"
echo "  PASS=$PASS  FAIL=$FAIL  SKIP_ENV=$SKIP  共 ${#FLOWS[@]}"
cat "$RESULTS"
echo "  逐条日志：$LOGDIR"
echo "  ⚠️ 跑完请恢复并存包：adb -s $SERIAL shell pm enable ${PKG}.sttdev"
[ "$FAIL" = 0 ] || exit 1
