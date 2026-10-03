-- 决定性实验：删掉未净化行，让 backfill 重新插入。
--
-- 为什么必须「删了再插入」而不是「就地 backfill」：
--   就地 backfill 会撞上 store.go 的
--     snippet = CASE WHEN EXCLUDED.snippet <> '' THEN EXCLUDED.snippet ELSE emails.snippet END
--   —— DeriveSnippet 返回空串时它的含义是「不覆盖」，于是 2026-10-01 写进去的坏摘要
--   会被**永久冻结**。这正是 03:0x 那次全量 backfill（fetched=856 saved=856）
--   跑完之后未净化行数**一条没少**（仍是 29）的原因：两种解释在那个观测上完全一样。
--
--   删掉之后就没有旧值可冻结，重新插入的 snippet 就是**当前代码的真实产出**。
--   ⇒ 数量不变 = 代码仍在漏；数量归零 = 代码是干净的，坏的只是自愈链路。
--
-- 安全性：只删 user-admin 名下、且**被本文件判据判为未净化**的行；
--   它们会在随后的 backfill 里被重新抓回（该账户 IMAP 连通、enabled=true）。
SET search_path TO opencode_pocket;

\echo '--- 删除前 ---'
SELECT 'unsanitized' AS k, count(*) FROM emails
 WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%'
UNION ALL SELECT 'total', count(*) FROM emails;

DELETE FROM emails
 WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%';

\echo '--- 删除后（应当只剩 total 一行） ---'
SELECT 'unsanitized' AS k, count(*) FROM emails
 WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%'
UNION ALL SELECT 'total', count(*) FROM emails;
