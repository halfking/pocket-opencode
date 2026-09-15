#!/usr/bin/env bash
# =====================================================================
# apply-edge-conf.sh — 把 deploy/edge/*.conf 模板渲染并幂等应用到 252 边缘 nginx
#
# 流程：渲染(替换 __MAC_MESH_IP__) → 远端备份 conf.d 旧文件 → scp 上传 →
#       nginx -t 门禁（失败自动回滚备份并退出，绝不带病 reload）→ reload。
#
# 用法：
#   ./deploy/edge/apply-edge-conf.sh                        # 默认 mesh IP 100.106.126.138
#   POCKET_MAC_MESH_IP=100.106.x.x ./deploy/edge/apply-edge-conf.sh
#   OPP_EDGE_SSH_HOST=252 ./deploy/edge/apply-edge-conf.sh  # ssh config alias，默认 252
#
# 前置：
#   - ssh alias 可达（~/.ssh/config Host 252 → root@115.29.212.252:25022）
#   - kxpms 三方证书已签出（certbot certonly --webroot -w /var/www/certbot -d <domain>）：
#     openpocket-api.kxpms.cn / openpocket.kxpms.cn / openpocket-web.kxpms.cn
#   - *-80.conf 文件先于本脚本或同期上线，否则 ACME HTTP-01 没法续期（certbot 已签的现仍可用）
# =====================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EDGE_HOST="${OPP_EDGE_SSH_HOST:-252}"
# Mac mesh IP 默认取永久化的 nb-mac-01（per netbird-mesh-admin: login_expiration_enabled=false + never-expires setup key）
MAC_MESH_IP="${POCKET_MAC_MESH_IP:-100.106.192.58}"
REMOTE_CONF_DIR="/etc/nginx/conf.d"
REMOTE_BACKUP_DIR="${REMOTE_CONF_DIR}/backups"
STAMP="$(date +%Y%m%d-%H%M%S)"

# 9443 vhost + 80 端口 ACME 配套（顺序无关，nginx -t 全局校验）
CONFS=(
  pocket.itestu.cn.conf
  openpocket-api.itestu.cn.conf
  openpocket-api.kxpms.cn.conf
  openpocket.kxpms.cn.conf
  openpocket-web.kxpms.cn.conf
  pocket.kxpms-cn-9443.conf
  openpocket-api.kxpms.cn-80.conf
  openpocket.kxpms.cn-80.conf
  openpocket-web.kxpms.cn-80.conf
)

log() { printf '\033[1;32m  ✅\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m  ⚠\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m  ❌\033[0m %s\n' "$*" >&2; exit 1; }

# ── 1. 渲染模板到临时目录 ─────────────────────────────────────────
TMPDIR_RENDER="$(mktemp -d)"
trap 'rm -rf "${TMPDIR_RENDER}"' EXIT
for f in "${CONFS[@]}"; do
  [[ -f "${SCRIPT_DIR}/${f}" ]] || die "模板缺失: ${SCRIPT_DIR}/${f}"
  sed "s/__MAC_MESH_IP__/${MAC_MESH_IP}/g" "${SCRIPT_DIR}/${f}" > "${TMPDIR_RENDER}/${f}"
done
log "模板渲染完成（mesh IP=${MAC_MESH_IP}）: ${CONFS[*]}"

# ── 2. 远端连通性 + 证书就绪检查 ──────────────────────────────────
# kxpms 三方证书必须先签出（certbot certonly --webroot -w /var/www/certbot -d <domain>），
# 否则 9443 vhost 的 ssl_certificate 路径 nginx -t 会失败。
REQUIRED_CERTS=(
  openpocket-api.kxpms.cn
  openpocket.kxpms.cn
  openpocket-web.kxpms.cn
)
for c in "${REQUIRED_CERTS[@]}"; do
  ssh "${EDGE_HOST}" "test -d /etc/letsencrypt/live/${c}" \
    || die "252 缺 ${c} 证书；先执行: ssh ${EDGE_HOST} certbot certonly --webroot -w /var/www/certbot -d ${c}"
done

# ── 3. 远端备份旧配置（无则跳过） ─────────────────────────────────
ssh "${EDGE_HOST}" "mkdir -p ${REMOTE_BACKUP_DIR} && for f in ${CONFS[*]}; do [ -f ${REMOTE_CONF_DIR}/\$f ] && cp -a ${REMOTE_CONF_DIR}/\$f ${REMOTE_BACKUP_DIR}/\$f.bak-${STAMP}; done; true"
log "远端旧配置已备份到 ${REMOTE_BACKUP_DIR}/*.bak-${STAMP}"

# ── 4. 上传渲染产物 ───────────────────────────────────────────────
scp -q "${TMPDIR_RENDER}"/*.conf "${EDGE_HOST}:${REMOTE_CONF_DIR}/"
log "已上传 ${#CONFS[@]} 份 vhost 到 ${EDGE_HOST}:${REMOTE_CONF_DIR}/"

# ── 5. nginx -t 门禁：失败回滚 ─────────────────────────────────────
if ssh "${EDGE_HOST}" "nginx -t -q"; then
  log "nginx -t 通过"
else
  warn "nginx -t 失败，自动回滚备份"
  ssh "${EDGE_HOST}" "for f in ${CONFS[*]}; do [ -f ${REMOTE_BACKUP_DIR}/\$f.bak-${STAMP} ] && cp -a ${REMOTE_BACKUP_DIR}/\$f.bak-${STAMP} ${REMOTE_CONF_DIR}/\$f; done; nginx -t -q"
  die "已回滚到 ${STAMP} 前状态；请检查模板后重试"
fi

# ── 6. reload ─────────────────────────────────────────────────────
ssh "${EDGE_HOST}" "systemctl reload nginx"
log "nginx reload 完成 — pocket 9 个 vhost 生效（5 个 9443 + 3 个 kxpms -80 + pocket.itestu.cn-80 等已在仓外的）"
