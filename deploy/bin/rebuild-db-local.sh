#!/usr/bin/env bash
# =====================================================================
# rebuild-db-local.sh — 本地 llm-gateway-pg「整库重建」+ 专家角色种子入库
#
# 按 2026-08-18 原方案（docs/优化v4/reports/docker-llm-gateway-pg-switch-*.md）
# 在本地共享容器 llm-gateway-pg 中重建 openpocket 数据库：
#   - 库：kaixuan（若缺失则创建；本脚本只重建其中的 opencode_pocket schema）
#   - 应用角色：pocket_app（专用登录角色，随机密码写入未跟踪 env 文件）
#   - schema：opencode_pocket（DROP CASCADE 后由 pocketd 各 store 重建全表）
#   - 种子：deploy/sql/chat_agents_seed.sql（内置专家角色/提示词 277 个）
#
# 强约束（deploy/acc-integration/README.md）：
#   - 绝不触碰容器本身 / 其他库（acc_db、llm_gateway、postgres）/ 共享角色 llm_gateway
#   - 禁止 DROP DATABASE；破坏性操作仅限本服务 schema（opencode_pocket）
#
# 用法：
#   ./deploy/bin/rebuild-db-local.sh            # 交互确认后执行
#   ./deploy/bin/rebuild-db-local.sh --yes      # 跳过确认（CI/自动化）
#
# 产物：
#   备份   ${POCKET_BACKUP_DIR}/opencode_pocket-<ts>.sql.gz（重建前自动备份）
#   凭据   ${POCKET_BACKUP_DIR}/../config/rebuild-local.env（600，不入库）
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── 参数（可用环境变量覆盖） ────────────────────────────────────────
PG_CONTAINER="${OPP_LOCAL_PG_CONTAINER:-llm-gateway-pg}"
PG_ADMIN="${OPP_LOCAL_PG_ADMIN:-llm_gateway}"        # 容器内超级用户（socket trust）
PG_DB="${OPP_LOCAL_PG_DB:-kaixuan}"                  # 原方案业务库
PG_SCHEMA="${POCKET_PG_SCHEMA:-opencode_pocket}"
APP_ROLE="${OPP_LOCAL_PG_APP_ROLE:-pocket_app}"
PG_HOST_PORT="${OPP_LOCAL_PG_PORT:-15432}"           # llm-gateway-pg 宿主映射
SEED_SQL="${SCRIPT_DIR}/../sql/chat_agents_seed.sql"
BACKUP_DIR="${POCKET_BACKUP_DIR:-${HOME}/Downloads/kaixuan/opp/backup}"
CONFIG_DIR="${POCKET_CONFIG_DIR:-${HOME}/Downloads/kaixuan/opp/config}"
CRED_FILE="${CONFIG_DIR}/rebuild-local.env"
ASSUME_YES=false
[[ "${1:-}" == "--yes" ]] && ASSUME_YES=true

fail() { echo "❌ $1" >&2; exit 1; }
info() { echo "▶ $1"; }

[[ -f "${SEED_SQL}" ]] || fail "种子文件缺失: ${SEED_SQL}（先运行 backend/cmd/gen-agent-seed 生成）"

# 容器内 psql（socket + trust，无需密码）
psql_admin() {
  docker exec -i "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d "${PG_DB}" -v ON_ERROR_STOP=1 "$@"
}
# 应用角色执行（socket trust）；注入 search_path 使种子 DDL/INSERT 落在目标 schema
psql_app() {
  docker exec -i -e PGOPTIONS="-c search_path=${PG_SCHEMA}" "${PG_CONTAINER}" \
    psql -U "${APP_ROLE}" -d "${PG_DB}" -v ON_ERROR_STOP=1 "$@"
}

# ── 1. 预检 ────────────────────────────────────────────────────────
info "预检容器与身份…"
docker inspect "${PG_CONTAINER}" >/dev/null 2>&1 || fail "容器不存在: ${PG_CONTAINER}"
rol_attrs="$(docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d postgres -tAc \
  "select rolsuper from pg_roles where rolname='${PG_ADMIN}';")"
[[ "${rol_attrs}" == "t" ]] || fail "${PG_ADMIN} 不是超级用户，无法执行重建"

if ! docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d postgres -tAc \
  "select 1 from pg_database where datname='${PG_DB}';" | grep -q 1; then
  info "库 ${PG_DB} 不存在，按原方案创建（UTF8）…"
  docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d postgres -c \
    "CREATE DATABASE \"${PG_DB}\" ENCODING 'UTF8';" >/dev/null
fi
docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d "${PG_DB}" -tAc "select 1;" >/dev/null \
  || fail "无法连接库 ${PG_DB}"

TABLE_COUNT_BEFORE="$(psql_admin -tAc \
  "select count(*) from information_schema.tables where table_schema='${PG_SCHEMA}';" || echo 0)"

# ── 2. 确认 ────────────────────────────────────────────────────────
if [[ "${ASSUME_YES}" != true ]]; then
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  容器   : ${PG_CONTAINER}"
  echo "  库     : ${PG_DB}（其他库不受影响）"
  echo "  schema : ${PG_SCHEMA}（DROP CASCADE 重建，现有 ${TABLE_COUNT_BEFORE} 张表）"
  echo "  种子   : ${SEED_SQL} ($(grep -c '^INSERT' "${SEED_SQL}") 个内置角色)"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  read -r -p "确认重建？输入 yes 继续: " answer
  [[ "${answer}" == "yes" ]] || fail "已取消"
fi

mkdir -p "${BACKUP_DIR}" "${CONFIG_DIR}"

# ── 3. 备份现有 schema ─────────────────────────────────────────────
if [[ "${TABLE_COUNT_BEFORE}" -gt 0 ]]; then
  TS="$(date +%Y%m%d-%H%M%S)"
  BACKUP_FILE="${BACKUP_DIR}/opencode_pocket-${TS}.sql.gz"
  info "备份现有 schema → ${BACKUP_FILE}"
  docker exec "${PG_CONTAINER}" pg_dump -U "${PG_ADMIN}" -d "${PG_DB}" --schema="${PG_SCHEMA}" \
    | gzip > "${BACKUP_FILE}" || fail "备份失败，中止（未做任何破坏性操作）"
else
  info "现有 schema 无表，跳过备份"
fi

# ── 4. 专用应用角色（不动共享 llm_gateway 的密码） ─────────────────
info "确保应用角色 ${APP_ROLE}…"
APP_PASSWORD="$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | xxd -p | head -c 48)"
if docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d postgres -tAc \
  "select 1 from pg_roles where rolname='${APP_ROLE}';" | grep -q 1; then
  docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d postgres -c \
    "ALTER ROLE \"${APP_ROLE}\" LOGIN PASSWORD '${APP_PASSWORD}';" >/dev/null
else
  docker exec "${PG_CONTAINER}" psql -U "${PG_ADMIN}" -d postgres -c \
    "CREATE ROLE \"${APP_ROLE}\" LOGIN PASSWORD '${APP_PASSWORD}';" >/dev/null
fi
psql_admin -c "GRANT CONNECT ON DATABASE \"${PG_DB}\" TO \"${APP_ROLE}\";" >/dev/null
# pocketd 启动时会 CREATE SCHEMA IF NOT EXISTS（即使已存在也需库级 CREATE 权限）
psql_admin -c "GRANT CREATE ON DATABASE \"${PG_DB}\" TO \"${APP_ROLE}\";" >/dev/null

# ── 5. 重建 schema ─────────────────────────────────────────────────
info "重建 schema ${PG_SCHEMA}…"
psql_admin -c "DROP SCHEMA IF EXISTS \"${PG_SCHEMA}\" CASCADE;"
psql_admin -c "CREATE SCHEMA \"${PG_SCHEMA}\" AUTHORIZATION \"${APP_ROLE}\";"
psql_admin -c "GRANT USAGE, CREATE ON SCHEMA \"${PG_SCHEMA}\" TO \"${APP_ROLE}\";"

# 写凭据文件（600，不入库；供本地 pocketd / acc 栈使用）
cat > "${CRED_FILE}" <<EOF
# 由 rebuild-db-local.sh 生成（$(date +%F\ %T)）—— 请勿提交到仓库
POCKET_POSTGRES_DSN=postgresql://${APP_ROLE}:${APP_PASSWORD}@127.0.0.1:${PG_HOST_PORT}/${PG_DB}?sslmode=disable
POCKET_PG_SCHEMA=${PG_SCHEMA}
EOF
chmod 600 "${CRED_FILE}"
info "凭据已写入 ${CRED_FILE}（权限 600）"

# ── 6. 启动一次后端建全表（各 store 的 CREATE TABLE IF NOT EXISTS） ──
info "启动 pocketd 初始化全部表…"
BACKEND_DIR="${SCRIPT_DIR}/../../backend"
BIN="$(mktemp -t pocketd-rebuild-XXXXXX)"
trap 'rm -f "${BIN}"' EXIT
( cd "${BACKEND_DIR}" && go build -o "${BIN}" ./cmd/pocketd ) || fail "后端编译失败"

POCKET_DATA_DIR="$(mktemp -d)"
trap 'rm -rf "${POCKET_DATA_DIR}"; rm -f "${BIN}"' EXIT
DSN="postgresql://${APP_ROLE}:${APP_PASSWORD}@127.0.0.1:${PG_HOST_PORT}/${PG_DB}?sslmode=disable"
# 临时启动仅用于建表：随机 JWT（≥32B）与合规首用户密码，跑完即弃
POCKET_POSTGRES_DSN="${DSN}" \
POCKET_PG_SCHEMA="${PG_SCHEMA}" \
POCKET_HTTP_PORT="18099" \
POCKET_DATA_DIR="${POCKET_DATA_DIR}" \
POCKET_DEV_AUTH="true" \
POCKET_AUTH_PASS="rebuild-$(openssl rand -hex 8 2>/dev/null || echo localadmin)" \
POCKET_JWT_SECRET="$(openssl rand -hex 32 2>/dev/null || head -c 48 /dev/urandom | xxd -p)" \
  "${BIN}" >/tmp/pocketd-rebuild.log 2>&1 &
POCKETD_PID=$!

HEALTHZ_OK=false
for _ in $(seq 1 60); do
  if curl -sf "http://127.0.0.1:18099/healthz" >/dev/null 2>&1; then HEALTHZ_OK=true; break; fi
  if ! kill -0 "${POCKETD_PID}" 2>/dev/null; then break; fi
  sleep 1
done
kill "${POCKETD_PID}" 2>/dev/null || true
wait "${POCKETD_PID}" 2>/dev/null || true
[[ "${HEALTHZ_OK}" == true ]] || { echo "──── pocketd 日志末尾 ────" >&2; tail -30 /tmp/pocketd-rebuild.log >&2; fail "pocketd 未能健康启动（建表未完成）"; }
info "pocketd 健康启动，表结构初始化完成"

# ── 7. 应用种子（以 app 角色执行 → 对象属主正确） ──────────────────
info "应用专家角色种子…"
psql_app < "${SEED_SQL}" >/dev/null || fail "种子 SQL 执行失败"

# ── 8. 验证 ────────────────────────────────────────────────────────
info "验证重建结果…"
TABLE_COUNT="$(psql_app -tAc \
  "select count(*) from information_schema.tables where table_schema='${PG_SCHEMA}';")"
BUILTIN_COUNT="$(psql_app -tAc \
  "select count(*) from ${PG_SCHEMA}.chat_agents where is_builtin=1;")"
SEED_COUNT="$(grep -c '^INSERT' "${SEED_SQL}")"
EMPTY_PROMPTS="$(psql_app -tAc \
  "select count(*) from ${PG_SCHEMA}.chat_agents where is_builtin=1 and (system_prompt is null or system_prompt='');")"

[[ "${TABLE_COUNT}" -gt 20 ]] || fail "表数量异常: ${TABLE_COUNT}（预期 27 张左右）"
[[ "${BUILTIN_COUNT}" == "${SEED_COUNT}" ]] || fail "内置角色数 ${BUILTIN_COUNT} ≠ 种子数 ${SEED_COUNT}"
[[ "${EMPTY_PROMPTS}" == "0" ]] || fail "存在 ${EMPTY_PROMPTS} 个空提示词的内置角色"

echo ""
echo "✅ 重建完成"
echo "   库/角色  : ${PG_DB} / ${APP_ROLE}"
echo "   schema   : ${PG_SCHEMA}（${TABLE_COUNT} 张表）"
echo "   内置角色 : ${BUILTIN_COUNT} 个（提示词非空）"
echo "   凭据     : ${CRED_FILE}（本地 pocketd 使用该 DSN）"
[[ "${TABLE_COUNT_BEFORE}" -gt 0 ]] && echo "   回滚     : 恢复 ${BACKUP_DIR}/opencode_pocket-*.sql.gz（gunzip | docker exec -i ${PG_CONTAINER} psql -U ${PG_ADMIN} -d ${PG_DB}）"
