#!/usr/bin/env bash
# =====================================================================
# renew-openpocket-api-cert.sh
# openpocket-api.kxpms.cn 证书续期看门狗（Mac launchd 触发，远端执行）
#
# 设计约束：
#   - 252 自管证书（certbot-renew.timer 每日自动续），本脚本仅做健康检查 +
#     到期前主动触发远端 certbot renew + nginx reload，作为 timer 的兜底。
#   - Mac 不需要本地 certbot / 证书 lineage——纯 SSH 远程操作。
#   - 幂等：30 天内到期才触发 renew，否则仅记录日志后退出。
#
# 触发链路（launchd 每周日 03:30 调用）：
#   Mac launchd → SSH 252 → 检查到期天数 → (可选) certbot renew → nginx reload
#
# 退出码：0 成功（含"未到期无需续"）；1 SSH 连通失败；2 证书不存在；3 续期失败
# =====================================================================
set -euo pipefail

DOMAIN="openpocket-api.kxpms.cn"
UPSTREAM_HOST="${POCKET_UPSTREAM_HOST:-115.29.212.252}"
UPSTREAM_PORT="${POCKET_UPSTREAM_PORT:-25022}"
UPSTREAM_USER="${POCKET_UPSTREAM_USER:-root}"
SSH_KEY="${POCKET_SSH_KEY:-${HOME}/.ssh/id_ed25519}"
LOG_TAG="pocket-cert-renew"
RENEW_THRESHOLD_DAYS="${POCKET_RENEW_THRESHOLD_DAYS:-30}"

log() { /usr/bin/logger -t "$LOG_TAG" -- "$*" 2>/dev/null || true; echo "[$(date -u +%FT%TZ)] $*"; }

SSH_OPTS=(-i "${SSH_KEY}" -p "${UPSTREAM_PORT}" -o ConnectTimeout=10 -o BatchMode=yes -o StrictHostKeyChecking=no)
SSH_CMD="${UPSTREAM_USER}@${UPSTREAM_HOST}"

# ── 前置检查 ──────────────────────────────────────────────────────
if [[ ! -f "${SSH_KEY}" ]]; then
  log "FATAL: 缺 SSH key ${SSH_KEY}" >&2; exit 1
fi

# ── 1. SSH 连通性 ────────────────────────────────────────────────
log "SSH 连通检查 ${SSH_CMD}:${UPSTREAM_PORT} …"
if ! ssh "${SSH_OPTS[@]}" "${SSH_CMD}" 'echo ok' >/dev/null 2>&1; then
  log "FATAL: SSH 不可达 ${SSH_CMD}:${UPSTREAM_PORT}" >&2; exit 1
fi
log "SSH 连通 OK"

# ── 2. 远端检查证书到期天数 ──────────────────────────────────────
REMOTE_CHECK=$(ssh "${SSH_OPTS[@]}" "${SSH_CMD}" bash -s <<'CHECK'
CERT_FILE="/etc/letsencrypt/live/openpocket-api.kxpms.cn/fullchain.pem"
if [[ ! -f "$CERT_FILE" ]]; then
  echo "MISSING"
  exit 0
fi
EXPIRY_EPOCH=$(date -d "$(openssl x509 -enddate -noout -in "$CERT_FILE" | cut -d= -f2)" +%s 2>/dev/null || echo 0)
NOW_EPOCH=$(date +%s)
DAYS_LEFT=$(( (EXPIRY_EPOCH - NOW_EPOCH) / 86400 ))
echo "$DAYS_LEFT"
CHECK
) || { log "FATAL: 远端检查失败" >&2; exit 1; }

if [[ "${REMOTE_CHECK}" == "MISSING" ]]; then
  log "FATAL: 252 上证书文件不存在 ${DOMAIN}" >&2; exit 2
fi

DAYS_LEFT="${REMOTE_CHECK}"
log "证书 ${DOMAIN} 距到期还有 ${DAYS_LEFT} 天（阈值 ${RENEW_THRESHOLD_DAYS} 天）"

if (( DAYS_LEFT > RENEW_THRESHOLD_DAYS )); then
  log "证书未到期，跳过续期（下次检查下周日 03:30）"
  exit 0
fi

# ── 3. 触发远端 certbot renew ────────────────────────────────────
log "证书即将到期（剩 ${DAYS_LEFT} 天），触发远端 certbot renew …"
if ! ssh "${SSH_OPTS[@]}" "${SSH_CMD}" \
  "certbot renew --cert-name ${DOMAIN} --non-interactive --agree-tos" \
  2>&1; then
  log "FATAL: 远端 certbot renew 失败" >&2; exit 3
fi
log "远端 certbot renew 成功"

# ── 4. 远端 nginx reload ─────────────────────────────────────────
log "远端 nginx reload …"
if ! ssh "${SSH_OPTS[@]}" "${SSH_CMD}" \
  "nginx -t -q && systemctl reload nginx" 2>&1; then
  log "FATAL: 远端 nginx reload 失败" >&2; exit 3
fi
log "远端 nginx reload 完成"

# ── 5. 验证新证书 ────────────────────────────────────────────────
NEW_EXPIRY=$(ssh "${SSH_OPTS[@]}" "${SSH_CMD}" \
  "openssl x509 -enddate -noout -in /etc/letsencrypt/live/${DOMAIN}/fullchain.pem | cut -d= -f2" 2>/dev/null || echo 'unknown')
log "续期完成：${DOMAIN} 新到期时间 = ${NEW_EXPIRY}"
log "OK"
