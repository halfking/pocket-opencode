#!/usr/bin/env bash
# =====================================================================
# provision-local-pg.sh — 在「已存在的共享 PostgreSQL」上开 openpocket 的库
#
# 定位：ensure-databases.sh 负责「有没有 PG / 要不要自己起一个」；
#       本脚本负责另一件事——复用别人已经起好的 PG 之后，
#       在上面把 openpocket 需要的 database / schema / 权限准备好。
#
# 原则：
#   - 只增不删：库/角色已存在就复用，绝不 DROP、绝不改别人的密码
#   - 幂等：重复跑结果一致
#   - 复用优先：先用 detect_pg_external 命中共享实例，命中就绝不新起容器
#   - 不泄密：DSN 与密码一律不打印，脚本输出里只出现库名/用户/权限结论
#
# 用法：
#   ./deploy/bin/provision-local-pg.sh
#   OPP_PG_PASSWORD=<密码> ./deploy/bin/provision-local-pg.sh
#   OPP_PG_HOST=127.0.0.1 OPP_PG_PORT=5432 ./deploy/bin/provision-local-pg.sh
#
# 默认目标（与 deploy/bin/env.sh 保持一致）：
#   host=host.docker.internal port=5432 db=pocket user=llm_gateway
#   schema=opencode_pocket
# =====================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# 调用方有没有显式指定端口？env.sh 对 DEPLOY_ENV=local 会把 OPP_PG_PORT
# 默认成 15432（那是连 252 的 SSH 隧道，见 deploy/bin/tunnel-252.sh），
# 但本脚本开的是「本机自己的共享 PG」，默认应该是 5432。
# env.sh 用 := 兜底，source 之后再赋值已非空——所以必须在 source 之前留档。
CALLER_PG_PORT="${OPP_PG_PORT:-}"
CALLER_PG_HOST="${OPP_PG_HOST:-}"
# shellcheck disable=SC1091
source "${SCRIPT_DIR}/env.sh"
# shellcheck disable=SC1091
source "${LIB_DIR}/database-detect.sh"
[[ -z "${CALLER_PG_PORT}" ]] && OPP_PG_PORT=5432
[[ -z "${CALLER_PG_HOST}" ]] && OPP_PG_HOST=host.docker.internal

# macOS Docker Desktop 默认不把 docker 放进 PATH
if ! command -v docker >/dev/null 2>&1; then
  for d in "${HOME}/.docker/bin" "/Applications/Docker.app/Contents/Resources/bin" \
           "/usr/local/bin" "/opt/homebrew/bin"; do
    [ -x "${d}/docker" ] && { export PATH="${d}:${PATH}"; break; }
  done
fi

: "${OPP_PG_HOST:=host.docker.internal}"
: "${OPP_PG_PORT:=5432}"
: "${OPP_PG_DB:=pocket}"
: "${OPP_PG_USER:=llm_gateway}"
: "${OPP_PG_SCHEMA:=opencode_pocket}"
export OPP_PG_HOST OPP_PG_PORT OPP_PG_DB OPP_PG_USER OPP_PG_SCHEMA

c_ok="✅"; c_bad="❌"; c_warn="⚠️ "; c_info="ℹ️ "
die() { echo "${c_bad} $*" >&2; exit 1; }

echo "━━━━━━━━━━━━━━━━━━━━━ 共享 PostgreSQL 开库 ━━━━━━━━━━━━━━━━━━━━━"
echo "  目标     : ${OPP_PG_USER}@${OPP_PG_HOST}:${OPP_PG_PORT}/${OPP_PG_DB}"
echo "  schema   : ${OPP_PG_SCHEMA}"
echo

# ── 1. 先探测：这是「复用」还是「另起」的岔路口 ────────────────────
echo "  ┌─ 复用检测 ────────────────────────────────────────────────"
# 探测要连的是「这台 PG 活不活」，不是「我要建的库在不在」——所以固定连
# 维护库 postgres。首次开库时目标库还不存在，用 OPP_PG_DB 去探会必然失败，
# 把「有 PG 可复用」误判成「没有 PG」。
if OPP_PG_DB=postgres detect_pg_external >/dev/null 2>&1; then
  echo "  │ ${c_ok} 命中已存在的 PostgreSQL 实例 → 复用，不创建新实例"
  echo "  └──────────────────────────────────────────────────────────"
else
  echo "  │ ${c_warn} 没有检测到可用的外部 PG"
  if [[ "${OPP_DEPLOY_PG:-false}" == "true" ]]; then
    echo "  │ ${c_info} OPP_DEPLOY_PG=true，改由 ensure-databases.sh 容器化"
    echo "  └──────────────────────────────────────────────────────────"
    exit 0
  fi
  echo "  │ ${c_bad} OPP_DEPLOY_PG=${OPP_DEPLOY_PG:-false}，无外部 PG 可复用"
  echo "  └──────────────────────────────────────────────────────────"
  echo "  请先确认 ${OPP_PG_HOST}:${OPP_PG_PORT} 上有 PG，或显式 OPP_DEPLOY_PG=true。"
  exit 1
fi
echo

# ── 2. 找到能执行 SQL 的通道 ──────────────────────────────────────
# 宿主通常没有 psql（macOS 无 homebrew）；借道发布该端口的 postgres 容器。
container="$(_db_engine_container postgres "${OPP_PG_PORT}")" || true
psql_runner=()
if command -v psql >/dev/null 2>&1; then
  psql_runner=(psql -h "${OPP_PG_HOST}" -p "${OPP_PG_PORT}")
elif [[ -n "${container}" ]]; then
  psql_runner=(docker exec -i -e PGPASSWORD "${container}" psql -h 127.0.0.1 -p 5432)
  echo "  ${c_info} 宿主无 psql，借用容器 ${container} 执行 SQL"
else
  die "找不到 psql，也没有发布 ${OPP_PG_PORT} 的 postgres 容器"
fi

# PGPASSWORD 通过环境传（不进 argv，避免出现在进程列表里）
export PGPASSWORD="${OPP_PG_PASSWORD:-}"
export PGCONNECT_TIMEOUT=8

psql_exec() { "${psql_runner[@]}" -v ON_ERROR_STOP=1 -X -q "$@"; }

# 管理库始终连 postgres，避免「目标库还不存在」时连不上
admin_args=(-U "${OPP_PG_USER}" -d postgres)

echo "  ┌─ 鉴权 ────────────────────────────────────────────────────"
if ! psql_exec "${admin_args[@]}" -tAc 'SELECT 1' >/dev/null 2>&1; then
  if [[ -z "${OPP_PG_PASSWORD:-}" ]]; then
    die "以 ${OPP_PG_USER} 连接失败且未提供 OPP_PG_PASSWORD；请用 OPP_PG_PASSWORD=<密码> 重跑"
  fi
  die "以 ${OPP_PG_USER} 连接 ${OPP_PG_HOST}:${OPP_PG_PORT} 失败（密码不符或角色不存在）"
fi
echo "  │ ${c_ok} 以 ${OPP_PG_USER} 鉴权成功"
echo "  └──────────────────────────────────────────────────────────"
echo

# ── 3. 开库（幂等）────────────────────────────────────────────────
echo "  ┌─ 建库 / 建 schema ────────────────────────────────────────"
db_exists="$(psql_exec "${admin_args[@]}" -tAc \
  "SELECT 1 FROM pg_database WHERE datname = '${OPP_PG_DB}'" 2>/dev/null || echo 0)"
if [[ "${db_exists}" == "1" ]]; then
  echo "  │ ${c_ok} 数据库 ${OPP_PG_DB} 已存在，复用"
else
  # CREATE DATABASE 不能参数化也不能在事务里跑，这里标识符来自受控默认值，
  # 仍做一次白名单校验，只允许常规标识符字符。
  [[ "${OPP_PG_DB}" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] \
    || die "非法数据库名: ${OPP_PG_DB}"
  psql_exec "${admin_args[@]}" -c "CREATE DATABASE \"${OPP_PG_DB}\" OWNER \"${OPP_PG_USER}\"" \
    || die "创建数据库 ${OPP_PG_DB} 失败"
  echo "  │ ${c_ok} 数据库 ${OPP_PG_DB} 已创建（owner=${OPP_PG_USER}）"
fi

# schema 交给后端 migration 建（它需要 CREATE 权限且自己管版本）；
# 这里只做「可创建性」确认，不提前建，免得和 migration 抢所有权。
schema_state="$(psql_exec -U "${OPP_PG_USER}" -d "${OPP_PG_DB}" -tAc \
  "SELECT COALESCE((SELECT 'present' FROM pg_namespace WHERE nspname='${OPP_PG_SCHEMA}'),
                  CASE WHEN has_database_privilege(current_database(),'CREATE')
                       THEN 'will-be-created-by-migration' ELSE 'NO-CREATE' END)" \
  2>/dev/null || echo UNKNOWN)"
case "${schema_state}" in
  present)  echo "  │ ${c_ok} schema ${OPP_PG_SCHEMA} 已存在" ;;
  will-be-created-by-migration)
            echo "  │ ${c_ok} schema ${OPP_PG_SCHEMA} 待后端 migration 创建（角色有 CREATE 权限）" ;;
  *)        die "schema ${OPP_PG_SCHEMA} ${schema_state}；${OPP_PG_USER} 缺少数据库 CREATE 权限" ;;
esac
echo "  └──────────────────────────────────────────────────────────"
echo

# ── 4. 结果 ───────────────────────────────────────────────────────
echo "  ${c_ok} 开库完成"
echo "     database : ${OPP_PG_DB}（owner ${OPP_PG_USER}）"
echo "     schema   : ${OPP_PG_SCHEMA}"
echo
echo "  接下来交给部署脚本注入 DSN（密码不回显、不入库）："
echo "     OPP_PG_PASSWORD=<密码> ./deploy-local.sh"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"