#!/usr/bin/env bash
# pg-backfill-slow-query.sh —— 给**存量**实例补上慢查询观测（2026-10-17）。
#
# ── 为什么需要它 ──────────────────────────────────────────────────────
#
# `pg-init-slow-query.sql` 挂在 `/docker-entrypoint-initdb.d/`，而 postgres 镜像
# **只在数据目录为空的那一次**执行该目录下的脚本。
# ⇒ 已经跑过初始化的实例**永远不会**再跑它 ⇒ `pg_stat_statements` 没装，
# `pocket_slow_statements` 视图也没有。
#
# 而 `docs/guides/DEPLOYMENT_GUIDE.md` 让运维执行
# `SELECT * FROM pocket_slow_statements …` —— 在存量实例上那条语句必然报
# 「relation does not exist」。**文档承诺的排障手段在存量环境里不存在。**
#
# ⇒ 这不是「文档写错了」，是新增配置只对新环境生效，而没人对存量环境做过补齐。
# 本脚本把那一步补上，并且**先查后做**（幂等）。
#
# ── 两件事必须分开做 ──────────────────────────────────────────────────
#
# ① `shared_preload_libraries` 只能走**启动参数**，`ALTER SYSTEM` 补不回来
#    （要重启才生效）。它写在 docker-compose.db.yml 的 `command:` 里，
#    所以存量实例只要 `docker compose up -d` 重建容器就自动带上。
# ② 扩展本身与那个视图可以在**运行期**建，不需要重建。
#
# ⚠ 本脚本只做 ②，并**报告** ① 的状态。让脚本偷偷重建用户的容器是不合适的
#   —— 那是一次会影响正在跑的服务的中断，必须由人决定。
#
# 用法：
#   deploy/bin/pg-backfill-slow-query.sh                  # 自动找 opp-db-postgres
#   OPP_PG_CONTAINER=my-pg deploy/bin/pg-backfill-slow-query.sh
#   OPP_PG_CONTAINER=my-pg OPP_PG_DB=pocket deploy/bin/pg-backfill-slow-query.sh --dry-run
set -euo pipefail

CONTAINER="${OPP_PG_CONTAINER:-opp-db-postgres}"
DB="${OPP_PG_DB:-pocket}"
USER="${OPP_PG_USER:-llm_gateway}"
DRY_RUN=0
[[ "${1:-}" == "--dry-run" ]] && DRY_RUN=1

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SQL_FILE="$HERE/pg-init-slow-query.sql"

die() { echo "❌ $*" >&2; exit 1; }

command -v docker >/dev/null 2>&1 || die "找不到 docker"
[[ -f "$SQL_FILE" ]] || die "缺少 $SQL_FILE"

if ! docker inspect "$CONTAINER" >/dev/null 2>&1; then
  die "容器 $CONTAINER 不存在。先起服务，或用 OPP_PG_CONTAINER 指定实际容器名。"
fi

# 容器在跑吗？（停止的容器 exec 必然失败，而那与「扩展没装」是两回事）
state="$(docker inspect --format '{{.State.Status}}' "$CONTAINER")"
[[ "$state" == "running" ]] || die "容器 $CONTAINER 状态是 '$state'，请先启动它"

psql_q() { docker exec -i -u postgres "$CONTAINER" psql -U "$USER" -d "$1" -tAc "$2" 2>/dev/null; }

# ── ① 报告 shared_preload_libraries（只报告，不擅自重启）──────────────────
preload="$(psql_q postgres 'SHOW shared_preload_libraries;' || true)"
if [[ "$preload" == *pg_stat_statements* ]]; then
  echo "✅ shared_preload_libraries 已含 pg_stat_statements：$preload"
else
  echo "⚠️  shared_preload_libraries 里**没有** pg_stat_statements（当前：${preload:-<读不到>}）"
  echo "   ⇒ 扩展即使建了也不会有数据。要生效需要重建容器（会中断服务，请自行决定）："
  echo "     cd $(dirname "$HERE") && docker compose -p opp-db-postgres -f docker-compose.db.yml up -d postgres"
fi

# ── ② 检查目标库是否存在 ───────────────────────────────────────────────
if [[ "$(psql_q postgres "SELECT 1 FROM pg_database WHERE datname='$DB';")" != "1" ]]; then
  die "数据库 $DB 不存在。若这正是「POSTGRES_DB 被跳过创建」那个故障，先按
   docker-compose.db.yml 的注释处理数据目录（命名卷 / 属主），再重跑本脚本。"
fi

# ── ③ 补扩展与视图（幂等）─────────────────────────────────────────────
have_ext="$(psql_q "$DB" "SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements';")"
have_view="$(psql_q "$DB" "SELECT 1 FROM pg_views WHERE viewname='pocket_slow_statements';")"

if [[ "$have_ext" == "1" && "$have_view" == "1" ]]; then
  echo "✅ $DB 上 pg_stat_statements 与 pocket_slow_statements 都已就绪，无需改动"
  echo "   验证：docker exec $CONTAINER psql -U $USER -d $DB -c 'SELECT * FROM pocket_slow_statements LIMIT 5;'"
  exit 0
fi

if [[ "$DRY_RUN" == "1" ]]; then
  echo "🔎 --dry-run：将执行 $SQL_FILE（扩展存在=$have_ext，视图存在=$have_view）"
  exit 0
fi

echo "▶ 对 $DB 执行 $SQL_FILE …"
docker exec -i -u postgres "$CONTAINER" psql -v ON_ERROR_STOP=1 -U "$USER" -d "$DB" < "$SQL_FILE" \
  || die "执行失败：$SQL_FILE"

# 复核 —— 不复核就等于相信「我执行过了」，而那正是本仓反复记录的失效形态
ext_after="$(psql_q "$DB" "SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements';")"
view_after="$(psql_q "$DB" "SELECT 1 FROM pg_views WHERE viewname='pocket_slow_statements';")"
[[ "$ext_after" == "1" ]] || die "复核失败：pg_stat_statements 扩展仍不存在"
[[ "$view_after" == "1" ]] || die "复核失败：pocket_slow_statements 视图仍不存在"

echo "✅ 补齐完成并已复核：扩展 ✓ 视图 ✓"
echo "   验证：docker exec $CONTAINER psql -U $USER -d $DB -c 'SELECT * FROM pocket_slow_statements LIMIT 5;'"
