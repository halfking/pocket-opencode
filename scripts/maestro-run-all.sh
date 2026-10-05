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

SERIAL="${POCKET_SERIAL:-}"
PKG="${POCKET_APP_ID:-com.kaixuan.opencode.pocket}"
[ -n "$SERIAL" ] || { echo "[run] 需要 POCKET_SERIAL" >&2; exit 2; }
LOGDIR="${POCKET_LOGDIR:-/tmp/opp-maestro}"
mkdir -p "$LOGDIR"

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
    echo "[run] ❌ $name 跳过：前台拿不到 $PKG（最后焦点：$(focus_owner)）"
    printf 'SKIP_ENV  %s\n' "$name" >> "$RESULTS"; SKIP=$((SKIP+1)); continue
  fi
  # ★ 每条都走**完整前置**（真实登录 + 起点归位），不用 POCKET_SKIP_CDP_LOGIN 省钱。
  #   2026-10-05 踩过：第二条用 SKIP_CDP_LOGIN=1「假定还登着」，
  #   结果 notes-crud 的现场截图直接是**登录页**（密码框空），
  #   断言报「首页 is visible」—— 看着像功能缺失，实为起点不成立。
  #   起点是**每条 flow 的契约**，跨 flow 沿用等于把假设当事实。
  #   （同一条纪律在 smm-client 侧叫「起点契约必须一致」。）

  node "$ROOT/scripts/maestro-run.mjs" "$ROOT/$f" > "$LOGDIR/$name.log" 2>&1
  rc=$?
  # ★ 2026-10-06 设备掉线要单独归类，别记成 FAIL。
  #   实测：真机跑到一半 USB 断了一下，maestro 报
  #   "Device 4c308e2e was requested, but it is not connected."
  #   然后退出非 0 —— 形态与「断言不成立」同形，但它**不是产品或 flow 的问题**。
  #   记成 FAIL 会让人去查一个根本没坏的东西（与这轮反复修的
  #   「基础设施伪装成产品缺陷」是同一个漏洞，只是方向反过来）。
  if [ "$rc" = 0 ]; then
    echo "[run] ✅ $name"; printf 'PASS      %s\n' "$name" >> "$RESULTS"; PASS=$((PASS+1))
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
