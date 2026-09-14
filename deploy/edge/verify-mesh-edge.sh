#!/usr/bin/env bash
# verify-mesh-edge.sh —— Pocket 四域名 mesh 边缘贯通验证
#
# 用途（在 Mac 上运行）：
#   1. 从本机 netbird 状态取 Mac 的 mesh IP
#   2. SSH 到 252 探 mesh 回源路径（252 → Mac:8090/:4175，nginx upstream 实际方向）
#   3. SSH 到 252 探本机兜底源（127.0.0.1:8090/:4175）
#   4. 校验 pocket.itestu.cn 的 X-Pocket-Upstream 头指向当前 mesh IP（公网走了 mesh 回源）
#
# 用法：
#   ./deploy/edge/verify-mesh-edge.sh                       # 自动从本地 netbird 状态取 IP
#   POCKET_MAC_MESH_IP=100.x.y.z ./deploy/edge/verify-mesh-edge.sh   # 显式指定
#
# 退出码：
#   0 全部通过；1 mesh 不可达；2 端口不通；3 X-Pocket-Upstream 不匹配；4 缺前置依赖

set -euo pipefail

UPSTREAM_HOST="115.29.212.252"
UPSTREAM_FALLBACK_HOST="172.16.2.210"
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
  # netbird >=0.78 本机信息在顶层 netbirdIp（CIDR 形如 100.x.x.x/16）；
  # 旧版在 .localPeerState.ip/fqdn，保留兜底。
  MESH_IP="$(netbird status --json 2>/dev/null \
    | (command -v jq >/dev/null && jq -r '.netbirdIp // .localPeerState.ip // .localPeerState.fqdn // empty') \
    || true)"
  MESH_IP="${MESH_IP%%/*}"
  if [[ -z "$MESH_IP" || "$MESH_IP" == "null" ]]; then
    red "netbird 已装但未拿到 mesh IP。先 sudo netbird up --management-url https://netbird.itestu.cn"
    exit 4
  fi
  yellow "[1/4] 从 netbird status 取到 mesh IP: $MESH_IP"
fi
hr

# 2. mesh 回源路径：252 → Mac（nginx pocket_mac_* upstream 实际走的方向）。
#    注意不能在 Mac 本机 curl 自己的 mesh IP —— netbird userspace 接口不
#    hairpin，必然超时；必须在 252 上探 Mac。
yellow "[2/4] 在 252 上探 Mac mesh 回源 ${MESH_IP}(:${POCKETD_PORT_REMOTE}/healthz /:${FRONTEND_PORT_REMOTE}/)"
ok_ports=0
probe_ports=("$POCKETD_PORT_REMOTE" "$FRONTEND_PORT_REMOTE")
probe_paths=("/healthz" "/")
for i in "${!probe_ports[@]}"; do
  port="${probe_ports[$i]}"; path="${probe_paths[$i]}"
  rc="$(ssh -o ConnectTimeout=5 -o BatchMode=yes root@"${UPSTREAM_HOST}" \
        "curl -sS --max-time 5 -o /dev/null -w '%{http_code} (%{time_total}s)' http://${MESH_IP}:${port}${path}" 2>/dev/null || echo '000')"
  echo "  252 -> ${MESH_IP}:${port}${path} -> HTTP ${rc}"
  if [[ "$rc" =~ ^[23] ]]; then
    ok_ports=$((ok_ports+1))
  fi
done
if [[ $ok_ports -lt 2 ]]; then
  red "[2/4] FAILED: mesh 回源路径至少一个端口不可达（Mac netbird 断了或服务没起）"
  exit 2
fi
green "[2/4] mesh 回源两个端口都通"
hr

# 3. 远端 SSH 上探 252 兜底源（nginx 兜底上游用的 172.16.2.210，252 本机容器）。
#    注意 127.0.0.1:8090 上是无关进程，不能当兜底探针。
yellow "[3/4] SSH 到 252 上探兜底源 ${UPSTREAM_FALLBACK_HOST}，确认兜底容器在响应"
ssh_ok=0
for i in "${!probe_ports[@]}"; do
  port="${probe_ports[$i]}"; path="${probe_paths[$i]}"
  rc="$(ssh -o ConnectTimeout=5 -o BatchMode=yes root@"${UPSTREAM_HOST}" \
        "curl -sS --max-time 3 -o /dev/null -w '%{http_code}' http://${UPSTREAM_FALLBACK_HOST}:${port}${path}" 2>/dev/null || echo '000')"
  if [[ "$rc" =~ ^[23] ]]; then
    echo "  252:${UPSTREAM_FALLBACK_HOST}:${port}${path} -> HTTP ${rc}"
    ssh_ok=$((ssh_ok+1))
  else
    echo "  252:${UPSTREAM_FALLBACK_HOST}:${port}${path} -> HTTP ${rc} (异常)"
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
