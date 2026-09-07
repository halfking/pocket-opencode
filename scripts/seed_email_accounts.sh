#!/usr/bin/env bash
# 把 envs 里的目标邮箱种子到 pocketd PG（SSOT）。
# 凭证只从 envs loader / 环境变量读，不写进仓库。
# 幂等：同 emailAddress 已存在则 PUT 刷新 IMAP/SMTP 配置与授权码。
#
# 用法：
#   POCKET_API_BASE=http://127.0.0.1:8090 \
#   POCKET_ADMIN_PASS=<admin-password> \
#   bash scripts/seed_email_accounts.sh
set -euo pipefail

API_BASE="${POCKET_API_BASE:-http://127.0.0.1:8090}"
ADMIN_USER="${POCKET_ADMIN_USER:-admin}"
ADMIN_PASS="${POCKET_ADMIN_PASS:-${POCKET_AUTH_PASS:-}}"
ENVS_LOADER="${ENVS_LOADER:-$HOME/workspace/ai-native-tools/envs/loader.sh}"

if [ -z "$ADMIN_PASS" ]; then
  echo "[seed] POCKET_ADMIN_PASS / POCKET_AUTH_PASS is required" >&2
  exit 1
fi

env_get() {
  local key="$1"
  local raw=""
  if [ -x "$ENVS_LOADER" ]; then
    raw="$(bash "$ENVS_LOADER" query "$key" 2>/dev/null || true)"
  fi
  # loader 偶发把 YAML 行尾注释带出来，只取第一个 token
  printf '%s' "$raw" | awk '{print $1}'
}

SEED_KAIXUAN_PASSWORD="${SEED_KAIXUAN_PASSWORD:-$(env_get KAIXUAN_EMAIL_AUTH_CODE)}"
SEED_QQ_PASSWORD="${SEED_QQ_PASSWORD:-$(env_get QQ_EMAIL_AUTH_CODE)}"
SEED_163_FK_PASSWORD="${SEED_163_FK_PASSWORD:-$(env_get EMAIL_163_FEIKEMANAGER_AUTH_CODE)}"
SEED_163_FK1_PASSWORD="${SEED_163_FK1_PASSWORD:-$(env_get EMAIL_163_FEIKEMANAGER1_AUTH_CODE)}"
SEED_163_KH_PASSWORD="${SEED_163_KH_PASSWORD:-$(env_get EMAIL_163_KIMMY_AUTH_CODE)}"

login() {
  curl -fsS -X POST "$API_BASE/api/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" \
    | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])'
}

TOKEN="$(login)"
AUTH="Authorization: Bearer $TOKEN"

account_id_for() {
  curl -fsS "$API_BASE/api/email/accounts" -H "$AUTH" \
    | python3 -c "import json,sys;d=json.load(sys.stdin);print(next((a['id'] for a in d.get('accounts',[]) if a.get('emailAddress')==sys.argv[1]),''))" "$1"
}

upsert() {
  local label="$1" email="$2" imap="$3" port="$4" smtp="$5" smtp_port="$6" pass="$7"
  if [ -z "$pass" ]; then
    echo "[seed] missing password for $email" >&2
    return 1
  fi
  local existing; existing="$(account_id_for "$email")"
  local body
  body="$(SEED_LABEL="$label" SEED_EMAIL="$email" SEED_IMAP="$imap" SEED_PORT="$port" \
    SEED_SMTP="$smtp" SEED_SMTP_PORT="$smtp_port" SEED_PASS="$pass" python3 - <<'PY'
import json, os
print(json.dumps({
  "displayName": os.environ["SEED_LABEL"],
  "emailAddress": os.environ["SEED_EMAIL"],
  "imapHost": os.environ["SEED_IMAP"],
  "imapPort": int(os.environ["SEED_PORT"]),
  "authType": "password",
  "syncIntervalMin": 15,
  "enabled": True,
  "smtpHost": os.environ["SEED_SMTP"],
  "smtpPort": int(os.environ["SEED_SMTP_PORT"]),
  "password": os.environ["SEED_PASS"],
  "smtpPassword": os.environ["SEED_PASS"],
}))
PY
)"
  if [ -n "$existing" ]; then
    curl -fsS -X PUT "$API_BASE/api/email/accounts/$existing" -H "$AUTH" \
      -H 'Content-Type: application/json' -d "$body" >/dev/null \
      || { echo "[seed] FAILED update $email"; return 1; }
    echo "[seed] updated $email"
    return 0
  fi
  curl -fsS -X POST "$API_BASE/api/email/accounts" -H "$AUTH" \
    -H 'Content-Type: application/json' -d "$body" >/dev/null \
    || { echo "[seed] FAILED create $email"; return 1; }
  echo "[seed] created $email"
}

echo "== seeding admin email accounts =="
upsert "凯轩企业邮" "huangxutao@kxpms.cn" "imap.exmail.qq.com" 993 "smtp.exmail.qq.com" 465 "$SEED_KAIXUAN_PASSWORD"
upsert "QQ 私人" "56551681@qq.com" "imap.qq.com" 993 "smtp.qq.com" 465 "$SEED_QQ_PASSWORD"
upsert "163 / feikemanager" "feikemanager@163.com" "imap.163.com" 993 "smtp.163.com" 465 "$SEED_163_FK_PASSWORD"
upsert "163 / feikemanager1" "feikemanager1@163.com" "imap.163.com" 993 "smtp.163.com" 465 "$SEED_163_FK1_PASSWORD"
upsert "163 / kimmy.huang" "kimmy.huang@163.com" "imap.163.com" 993 "smtp.163.com" 465 "$SEED_163_KH_PASSWORD"
echo "== done =="
