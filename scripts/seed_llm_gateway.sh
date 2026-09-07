#!/usr/bin/env bash
# 把 kaixuan 网关默认配置写入 admin 主库（user_settings + llm-gateway/config）。
# API Key 只从 envs / 环境变量读，不写进仓库。
#
# 用法：
#   POCKET_API_BASE=http://127.0.0.1:8090 \
#   POCKET_ADMIN_PASS=<admin-password> \
#   bash scripts/seed_llm_gateway.sh
set -euo pipefail

API_BASE="${POCKET_API_BASE:-http://127.0.0.1:8090}"
ADMIN_USER="${POCKET_ADMIN_USER:-admin}"
ADMIN_PASS="${POCKET_ADMIN_PASS:-${POCKET_AUTH_PASS:-}}"
ENVS_LOADER="${ENVS_LOADER:-$HOME/workspace/ai-native-tools/envs/loader.sh}"
NOW="$(date +%s)"

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
  printf '%s' "$raw" | awk '{print $1}'
}

API_KEY="${POCKET_LLM_GATEWAY_API_KEY:-$(env_get POCKET_LLM_GATEWAY_API_KEY)}"
BASE_URL="${POCKET_LLM_GATEWAY_URL:-https://llm.kxpms.cn/v1}"
if [ -z "$API_KEY" ]; then
  echo "[seed] POCKET_LLM_GATEWAY_API_KEY is required" >&2
  exit 1
fi

LOGIN_JSON="$(POCKET_ADMIN_USER="$ADMIN_USER" POCKET_ADMIN_PASS="$ADMIN_PASS" python3 -c 'import json,os; print(json.dumps({"username":os.environ["POCKET_ADMIN_USER"],"password":os.environ["POCKET_ADMIN_PASS"]}))')"
TOKEN="$(curl -fsS -X POST "$API_BASE/api/auth/login" -H 'Content-Type: application/json' -d "$LOGIN_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')"
if [ -z "$TOKEN" ]; then
  echo "[seed] login failed" >&2
  exit 1
fi

MODELS_JSON='["claude-fable-5","claude-opus-4-8","claude-sonnet-4-6","claude-sonnet-5","gpt-5.6","gpt-5.5","gpt-5.4","glm-5.2","minimax-m3","deepseek-v4-pro","mimo-v2.5-pro"]'
GW_BODY="$(BASE_URL="$BASE_URL" API_KEY="$API_KEY" MODELS_JSON="$MODELS_JSON" python3 -c 'import json,os; print(json.dumps({"baseURL":os.environ["BASE_URL"],"apiKey":os.environ["API_KEY"],"format":"openai-chat","preferredModels":json.loads(os.environ["MODELS_JSON"])}))')"
PUT_BODY="$(BASE_URL="$BASE_URL" API_KEY="$API_KEY" MODELS_JSON="$MODELS_JSON" NOW="$NOW" python3 -c 'import json,os; print(json.dumps({"payload":{"baseURL":os.environ["BASE_URL"],"format":"openai-chat","models":[],"preferredModels":json.loads(os.environ["MODELS_JSON"])},"updatedAt":int(os.environ["NOW"]),"secret":os.environ["API_KEY"]}))')"

# 网关 POST 必须成功；user-settings 在旧二进制上可能 404，不能挡住网关写入。
curl -fsS -X POST "$API_BASE/api/llm-gateway/config" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "$GW_BODY" >/dev/null

if ! curl -fsS -X PUT "$API_BASE/api/user-settings/llm_gateway/default" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "$PUT_BODY" >/dev/null; then
  echo "[seed] user-settings PUT skipped (route missing or conflict); gateway config written" >&2
fi

echo "[seed] llm gateway written for admin at $API_BASE (updatedAt=$NOW)"
