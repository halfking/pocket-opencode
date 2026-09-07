#!/usr/bin/env bash
# scripts/seed_email_accounts.sh — 把目标邮箱配置种子到 admin（PG SSOT）。
#
# 默认账户来自 docs/2026-09-07-email-pipeline/inputs.md（见目标用户原文）。
# 凭证从同名 .env 读：SEED_<SLUG>_PASSWORD / SEED_<SLUG>_AUTHCODE，避免
# 把明文密码硬编进仓库。脚本幂等：同 email_address 的账户已存在则跳过。
#
# 用法：
#   SEED_KAIXUAN_PASSWORD=h8 \
#   SEED_KAIXUAN_AUTHCODE=SFNe2ARJqJJKRyUV \
#   SEED_QQ_PASSWORD=nregisttdabdbjej \
#   SEED_163_FK_PASSWORD=YHjRfBvpW7qaxv7x \
#   SEED_163_FK1_PASSWORD=EKjUVjagn5y6ghja \
#   SEED_163_KH_PASSWORD=VJgqfnG8A8Atjadp \
#   POCKET_API_BASE=http://127.0.0.1:8090 \
#   POCKET_ADMIN_PASS=<admin-password> \
#   bash scripts/seed_email_accounts.sh

set -euo pipefail

API_BASE="${POCKET_API_BASE:-http://127.0.0.1:8090}"
ADMIN_USER="${POCKET_ADMIN_USER:-admin}"
ADMIN_PASS="${POCKET_ADMIN_PASS:-${POCKET_AUTH_PASS:-d18db57a2e35e792b5223e562be2c3ea}}"

login() {
  curl -fsS -X POST "$API_BASE/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])'
}

TOKEN="$(login)"
AUTH="Authorization: Bearer $TOKEN"

# 列表查询：避免重复插入；返回第一个匹配账户 id
account_id_for() {
  curl -fsS "$API_BASE/api/email/accounts" -H "$AUTH" \
    | python3 -c "import json,sys;d=json.load(sys.stdin);print(next((a['id'] for a in d.get('accounts',[]) if a.get('emailAddress')==sys.argv[1]),''))" "$1"
}

upsert() {
  local label="$1" email="$2" imap="$3" port="$4" smtp="$5" smtp_port="$6" pass_env="$7" auth_env="${8:-}"
  local existing; existing="$(account_id_for "$email")"
  if [ -n "$existing" ]; then
    echo "[seed] $email already exists (id=$existing) — skip"
    return 0
  fi
  local auth_type="password"
  local token_field="\"password\": \"${!pass_env:-}\""
  if [ -n "$auth_env" ] && [ -n "${!auth_env:-}" ]; then
    auth_type="oauth2"
    token_field="\"oauthToken\": \"${!auth_env}\""
  fi
  local body
  body="$(cat <<EOF
{
  "displayName": "$label",
  "emailAddress": "$email",
  "imapHost": "$imap",
  "imapPort": $port,
  "authType": "$auth_type",
  "syncIntervalMin": 15,
  "enabled": true,
  "smtpHost": "$smtp",
  "smtpPort": $smtp_port,
  $token_field
}
EOF
)"
  curl -fsS -X POST "$API_BASE/api/email/accounts" -H "$AUTH" -H 'Content-Type: application/json' -d "$body" >/dev/null \
    || { echo "[seed] FAILED $email"; return 1; }
  echo "[seed] created $email"
}

# 启用手工定时收信（默认 POCKET_EMAIL_FETCH_ENABLED 已是 true）。
echo "== seeding admin email accounts =="
upsert "凯轩企业邮"  "huangxutao@kxpms.cn"  "imap.exmail.qq.com" 993 "smtp.exmail.qq.com" 465 SEED_KAIXUAN_PASSWORD SEED_KAIXUAN_AUTHCODE
upsert "QQ 私人"       "56551681@qq.com"    "imap.qq.com"         993 "smtp.qq.com"         465 SEED_QQ_PASSWORD
upsert "163 / feikemanager"  "feikemanager@163.com"  "imap.163.com" 993 "smtp.163.com" 25 SEED_163_FK_PASSWORD
upsert "163 / feikemanager1" "feikemanager1@163.com" "imap.163.com" 993 "smtp.163.com" 25 SEED_163_FK1_PASSWORD
upsert "163 / kimmy.huang"   "kimmy.huang@163.com"   "imap.163.com" 993 "smtp.163.com" 25 SEED_163_KH_PASSWORD

echo "== done =="