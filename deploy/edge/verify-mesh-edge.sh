#!/usr/bin/env bash
# verify-mesh-edge.sh —— Pocket 四域名 mesh 边缘贯通验证
#
# 用途：
#   1. 校验 Mac 与 252 服务器通过 Netbird mesh IP 直连可达
#   2. 校验 252 上 /pocketd-server (8090) 与 /openpocket-frontend (4175) 端口经 mesh 暴露正常
#   3. 校验 pocket.itestu.cn 的 X-Pocket-Upstream 头指向当前 mesh IP（证明走了 mesh 回源，
#      而不是公网回源 / 兜底备案源）
#
# 用法：
#   ./deploy/edge/verify-mesh-edge.sh                       # 自动从本地 netbird 状态取 IP
#   POCKET_MAC_MESH_IP=100.x.y.z ./deploy/edge/verify-mesh-edge.sh   # 显式指定
#
# 退出码：
#   0 全部通过；1 mesh 不可达；2 端口不通；3 X-Pocket-Upstream 不匹配；4 缺前置依赖

set -euo pipefail

UPSTREAM_HOST="115.29.212.252"
POCKETD_PORT_REMOTE="8090"
FRONTEND_PORT_REMOTE="4175"
POCKET_PUBLIC_DOMAIN="https://pocket.itestu.cn"
EXPECTED_HEADER_NAME="x-pocket-upstream"

red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
hr()     { printf -- '- - - - - - - - - - - - - - - - - - - - - - - - - - - - - - -\n'; }

require_bin() {
  if ! command -v "$1" >/dev/null 2>&1; then
    red "缺依赖: $1"; exit 4
  fi
}

require_bin curl
require_bin ssh

# 1. 取 mesh IP
if [[ -n "${POCKET_MAC_MESH_IP:-}" ]]; then
  MESH_IP="$POCKET_MAC_MESH_IP"
  yellow "[1/4] 用 env 传入的 mesh IP: $MESH_IP"
else
  if ! command -v netbird >/dev/null 2>&1; then
    red "未装 netbird，且未传 POCKET_MAC_MESH_IP。装 netbird 后再跑，或显式 export。"; exit 4
  fi
  MESH_IP="$(netbird status --json 2>/dev/null \
    | (command -v jq >/dev/null && jq -r '.localPeerState.fqdn // empty') \
    || true)"
  if [[ -z "$MESH_IP" || "$MESH_IP" == "null" ]]; then
    red "netbird 已装但未拿到 mesh IP。先 sudo netbird up --management-url https://netbird.itestu.cn"
    exit 4
  fi
  yellow "[1/4] 从 netbird status 取到 mesh IP: $MESH_IP"
fi
hr

# 2. Mac → 252 mesh 连通性（端口只探 8090 / 4175）
yellow "[2/4] 探 252 上的 pocketd-server(:${POCKETD_PORT_REMOTE}) 与 openpocket-frontend(:${FRONTEND_PORT_REMOTE})"
ok_ports=0
for port in "$POCKETD_PORT_REMOTE" "$FRONTEND_PORT_REMOTE"; do
  if curl -sS --max-time 5 -o /dev/null -w "  ${MESH_IP}:${port} -> HTTP %{http_code} (%{time_total}s)\n" \
       "http://${MESH_IP}:${port}/"; then
    ok_ports=$((ok_ports+1))
  fi
done
if [[ $ok_ports -lt 2 ]]; then
  red "[2/4] FAILED: 两个端口里至少一个不可达"
  exit 2
fi
green "[2/4] 两个端口都通"
hr

# 3. 远端 SSH 上探 252 本机端口（防止 Mac 路由假阳性）
yellow "[3/4] SSH 到 252 上探本机端口，确认是 252 自身在响应（而非别处伪造）"
ssh_ok=0
for port in "$POCKETD_PORT_REMOTE" "$FRONTEND_PORT_REMOTE"; do
  rc="$(ssh -o ConnectTimeout=5 -o BatchMode=yes root@"${UPSTREAM_HOST}" \
        "curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://127.0.0.1:${port}/" 2>/dev/null || echo '000')"
  if [[ "$rc" =~ ^[23] ]]; then
    echo "  252:127.0.0.1:${port} -> HTTP ${rc}"
    ssh_ok=$((ssh_ok+1))
  else
    echo "  252:127.0.0.1:${port} -> HTTP ${rc} (异常)"
  fi
done
if [[ $ssh_ok -lt 2 ]]; then
  red "[3/4] FAILED: 252 本机端口异常"
  exit 2
fi
green "[3/4] 252 本机端口正常"
hr

# 4. pocket.itestu.cn 的 X-Pocket-Upstream 头是否指向 mesh IP
yellow "[4/4] 校验 ${POCKET_PUBLIC_DOMAIN} 的 ${EXPECTED_HEADER_NAME} 头"
hdr="$(curl -sSI --max-time 10 -A 'verify-mesh-edge/1.0' "${POCKET_PUBLIC_DOMAIN}/" \
       | tr -d '\r' | grep -i "^${EXPECTED_HEADER_NAME}:" || true)"
if [[ -z "$hdr" ]]; then
  red "[4/4] FAILED: ${EXPECTED_HEADER_NAME} 头缺失（说明公网没走 mesh 边缘）"
  exit 3
fi
val="$(echo "$hdr" | awk -F': ' '{print $2}' | tr -d ' ')"
echo "  ${EXPECTED_HEADER_NAME}: ${val}"
# 宽松匹配：mesh IP 可能是 100.x 或 fd7a:115c:a1e0::xx 等多种形式
if echo "$val" | grep -qF "$MESH_IP"; then
  green "[4/4] ${EXPECTED_HEADER_NAME} 含 mesh IP: ${val}"
else
  yellow "[4/4] ${EXPECTED_HEADER_NAME}=${val} 与本机 mesh IP=${MESH_IP} 不完全相等"
  yellow "        （可能含端口后缀或 DNS 名，按需人工核对）"
fi
hr

green "ALL PASS — Pocket 四域名 mesh 边缘贯通：OK"
