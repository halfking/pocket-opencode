-- =============================================================================
-- pg-init-slow-query.sql — 首次初始化时装上慢查询观测
--
-- 背景（2026-10-17 审计）
-- ------------------------
-- 在此之前，本仓**没有任何一处**让 PostgreSQL 报出慢查询：
--   · `log_min_duration_statement` 取默认值 **-1**（关闭）⇒ 一条慢 SQL 都不落日志；
--   · `pg_stat_statements` 扩展**没有安装** ⇒ 累计统计无从查询；
--   · 而 `docs/guides/DEPLOYMENT_GUIDE.md:532` 却让运维执行
--     `SELECT * FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 10;`
--     —— 那条语句在默认配置下必然报 `relation "pg_stat_statements" does not exist`。
--
-- ⇒ 文档承诺的排障手段在默认部署里**根本不存在**。慢 SQL 只能靠猜。
--
-- 这里只做两件最小的事：
--   1. 装上 `pg_stat_statements`（必须在 shared_preload_libraries 里预加载才能工作，
--      那一半在 docker-compose.db.yml 的 command 里）；
--   2. 预置一条视图，把「按总耗时排序的 TOP N」固化下来，
--      免得每次排障都要现写 mean/total 的排序口径。
--
-- ⚠️ 本文件只在**数据目录为空的那一次**初始化时执行（postgres 镜像的既定语义）。
--    已经跑过初始化的实例不会重跑它 —— 那种情况请手工执行：
--      CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
-- =============================================================================

CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

-- 按**总**耗时排序（不是按单次均值）：均值高但只跑过一次的语句通常不是问题，
-- 总耗时高的才是真正吃掉数据库时间的地方。calls 一起给出，用来区分两者。
CREATE OR REPLACE VIEW pocket_slow_statements AS
SELECT
    calls,
    round(total_exec_time::numeric, 1) AS total_ms,
    round(mean_exec_time::numeric, 2)  AS mean_ms,
    round(stddev_exec_time::numeric, 2) AS stddev_ms,
    rows,
    left(regexp_replace(query, '\s+', ' ', 'g'), 160) AS query
FROM pg_stat_statements
WHERE query NOT LIKE '%pg_stat_statements%'
ORDER BY total_exec_time DESC;

COMMENT ON VIEW pocket_slow_statements IS
    '慢查询 TOP榜（按总耗时）。用法：SELECT * FROM pocket_slow_statements LIMIT 20;';
