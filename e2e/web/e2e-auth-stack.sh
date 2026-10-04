#!/usr/bin/env bash
# e2e-auth-stack.sh — 一条命令起「真后端 + dev server + 登录后 e2e」。
#
# 为什么需要它（2026-10-06）：
#   本仓登录后页面的 e2e 长期跑不起来，理由记在旧 spec 里：「靠 UI 登录 +
#   首次主密码创建，进不去无凭据环境」。后端仓里其实有 `cmd/gen-jwt`，用与
#   pocketd 同一个 secret 签一枚真 token 就能进——但这套要同时满足三件事，
#   手工敲容易漏，于是固化成脚本：
#     ① 后端必须用 **同一个** POCKET_JWT_SECRET（否则 token 一律 401）；
#     ② vite 必须以 VITE_API_PROXY 指向那个后端（否则打的是 8090 的别的实例）；
#     ③ dev server 必须 --strictPort（否则端口被占时 Vite 静默改端口，
#        而 curl 旧端口仍返回 200 ⇒ 对着陈旧 module graph 跑验收）。
#
# 用法：
#   ./e2e/web/e2e-auth-stack.sh                 # 起栈 + 跑 authenticated-shell
#   ./e2e/web/e2e-auth-stack.sh responsive-shell # 换 spec
#   ./e2e/web/e2e-auth-stack.sh --keep          # 跑完不收栈（便于手工调试）
#
# 清理：脚本退出时按 PID 精确 kill 本次拉起的进程，不做 killall。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
BACKEND="$REPO_ROOT/backend"
FRONTEND="$REPO_ROOT/frontend"

API_PORT="${E2E_API_PORT:-8088}"
WEB_PORT="${E2E_WEB_PORT:-4190}"
JWT_SECRET="${E2E_JWT_SECRET:-test-secret-key-for-phase7-validation}"
DB_PATH="${E2E_DB_PATH:-/tmp/openpocket-e2e.sqlite}"
SPEC="${1:-authenticated-shell}"
KEEP="${2:-}"

LOG_DIR="$(mktemp -d /tmp/openpocket-e2e.XXXXXX)"
BACK_PID=""; WEB_PID=""

cleanup() {
  if [ "$KEEP" = "--keep" ]; then
    echo "[e2e-stack] --keep：保留后端(PID $BACK_PID) 与 vite(PID $WEB_PID)，日志在 $LOG_DIR"
    return
  fi
  for pid in "$WEB_PID" "$BACK_PID"; do
    [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
  done
  # 杀完**必须验证端口真的空了**再宣告收栈成功。
  # 2026-10-06 实测过：`( cd X; ./pocketd ) &` 的 `$!` 是子 shell 而非 pocketd，
  # kill 完子 shell 后 pocketd 被 init 收养（PPID=1）继续占端口，
  # 而脚本当时照样打印「已收栈」——一个**没有自证**的清理比不做清理更坏，
  # 因为下一次运行会被端口检查拒绝，而失败原因指向别处。
  sleep 1
  local leftover=""
  lsof -nP -iTCP:"$API_PORT" -sTCP:LISTEN >/dev/null 2>&1 && leftover="$leftover 后端:$API_PORT"
  lsof -nP -iTCP:"$WEB_PORT" -sTCP:LISTEN >/dev/null 2>&1 && leftover="$leftover vite:$WEB_PORT"
  if [ -n "$leftover" ]; then
    echo "[e2e-stack] ⚠️ 收栈不干净，仍被占用：$leftover"
    echo "            残留 PID：$(lsof -nP -tiTCP:"$API_PORT" -sTCP:LISTEN 2>/dev/null | tr '\n' ' ')$(lsof -nP -tiTCP:"$WEB_PORT" -sTCP:LISTEN 2>/dev/null | tr '\n' ' ')"
    echo "            手动 kill 后再跑。日志在 $LOG_DIR"
  else
    echo "[e2e-stack] 已收栈并验证端口已释放（后端:$API_PORT vite:${WEB_PORT}）。日志在 $LOG_DIR"
  fi
}
trap cleanup EXIT

# ── 1. 后端 ────────────────────────────────────────────────────────────
if lsof -nP -iTCP:"$API_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "[e2e-stack] 端口 $API_PORT 已被占用：$(lsof -nP -iTCP:"$API_PORT" -sTCP:LISTEN | tail -1)"
  echo "            换端口用 E2E_API_PORT=<n>，别 kill 别人的进程（可能是姊妹仓的 pocketd）。"
  exit 1
fi
# ⚠️ 必须用 `exec`：`( cd X; ./pocketd ) &` 里的 `$!` 是**子 shell** 的 PID，
# kill 它只是让子 shell 死，pocketd 会被 init 收养变成孤儿（PPID=1）继续占着
# 端口——**收栈看起来成功了，其实没杀干净**。2026-10-06 实测踩到：
# 脚本退出后 8088 仍被 pocketd 占住，下一次运行直接被端口检查拒绝。
# `exec` 让子 shell 被 pocketd **替换**，于是 `$!` 就是目标进程本身。
echo "[e2e-stack] 起后端 :${API_PORT}（DB=${DB_PATH}）"
(
  cd "$BACKEND"
  exec env \
    POCKET_JWT_SECRET="$JWT_SECRET" \
    POCKET_AUTH_LEGACY_ONLY=true \
    POCKET_DEV_AUTH=true \
    POCKET_HTTP_PORT="$API_PORT" \
    POCKET_DB_PATH="$DB_PATH" \
    ./pocketd
) > "$LOG_DIR/backend.log" 2>&1 &
BACK_PID=$!

for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$API_PORT/healthz" >/dev/null 2>&1; then break; fi
  sleep 0.5
done
if ! curl -sf "http://127.0.0.1:$API_PORT/healthz" >/dev/null 2>&1; then
  echo "[e2e-stack] 后端 30s 内没就绪，日志："
  tail -20 "$LOG_DIR/backend.log"
  exit 1
fi
echo "[e2e-stack] 后端就绪"

# ── 2. dev server（--strictPort：端口被占就失败，不静默改端口）────────────
if lsof -nP -iTCP:"$WEB_PORT" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "[e2e-stack] 端口 $WEB_PORT 已被占用。换端口用 E2E_WEB_PORT=<n>。"
  echo "            ⚠️ 不要图省事去掉 --strictPort：Vite 会静默改端口，"
  echo "            而旧端口仍返回 200 ⇒ 会对着陈旧 module graph 跑验收。"
  exit 1
fi
echo "[e2e-stack] 起 vite :$WEB_PORT → 后端 :$API_PORT"
(
  cd "$FRONTEND"
  exec env VITE_API_PROXY="http://127.0.0.1:$API_PORT" \
    npx vite --port "$WEB_PORT" --host 127.0.0.1 --strictPort
) > "$LOG_DIR/vite.log" 2>&1 &
WEB_PID=$!

for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1; then break; fi
  sleep 0.5
done
if ! curl -sf "http://127.0.0.1:$WEB_PORT/" >/dev/null 2>&1; then
  echo "[e2e-stack] vite 30s 内没就绪，日志："
  tail -20 "$LOG_DIR/vite.log"
  exit 1
fi
echo "[e2e-stack] vite 就绪"

# ── 3. 验证 vite 代理真的通到**我们这个**后端，再跑 e2e ──────────────────
# 必要性：vite 默认代理 8090，而 8090 常被姊妹仓的 pocketd 占着。
# 若 VITE_API_PROXY 没生效，e2e 会对着**另一个后端**跑出一片 401，
# 症状与「token 无效」完全一样 ⇒ 必须在跑之前把归因分开。
PROXY_ME=$(curl -s "http://127.0.0.1:$WEB_PORT/api/auth/me" \
  -H "Authorization: Bearer $(cd "$BACKEND" && POCKET_JWT_SECRET="$JWT_SECRET" \
      go run ./cmd/gen-jwt --user e2e-user --role tenant_admin --workspace e2e-ws --ttl 5m 2>/dev/null | tail -1)" \
  -w '\n%{http_code}' -o /dev/null 2>/dev/null | tail -1)
if [ "$PROXY_ME" != "200" ]; then
  echo "[e2e-stack] 经 vite 代理打 /api/auth/me 返回 ${PROXY_ME:-无响应}（期望 200）。"
  echo "            先查 VITE_API_PROXY=$API_PORT 是否生效，再查后端是否活着。"
  exit 1
fi
echo "[e2e-stack] 代理链路已自证：vite :$WEB_PORT → 后端 :$API_PORT → /api/auth/me 200"

echo "[e2e-stack] 跑 spec：$SPEC"
cd "$SCRIPT_DIR"
E2E_BASE_URL="http://127.0.0.1:$WEB_PORT" \
E2E_JWT_SECRET="$JWT_SECRET" \
  npx playwright test "$SPEC" --reporter=list
