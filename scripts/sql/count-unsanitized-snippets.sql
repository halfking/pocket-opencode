-- 只读诊断（v2）：29 条未净化 snippet 的分布与进入时间。
--
-- v1 的两处错误已修：
--   · email_accounts 的地址列是 email_address，不是 email
--   · emails.created_at 是 bigint（epoch 秒），不是 timestamp ⇒ date_trunc 直接调用会报
--     "function date_trunc(unknown, bigint) does not exist"，必须先 to_timestamp()
SET search_path TO opencode_pocket;

\echo '--- 按账户分布：是哪几个账户在灌未净化内容 ---'
SELECT a.email_address, count(*) AS bad, min(to_timestamp(e.created_at)) AS earliest, max(to_timestamp(e.created_at)) AS latest
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 WHERE (length(e.snippet) > 200 AND e.snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR e.snippet ILIKE '%Content-Type%' OR e.snippet ILIKE '%boundary%' OR e.snippet ILIKE '%multipart%'
 GROUP BY a.email_address ORDER BY bad DESC;

\echo '--- 按小时分布：坏行是「历史存量」还是「仍在新增」 ---'
SELECT to_char(to_timestamp(created_at), 'YYYY-MM-DD HH24:00') AS hour_bucket, count(*)
  FROM emails
 WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%'
 GROUP BY 1 ORDER BY 1;

\echo '--- 全库最近一次入库时间（用来判断"新数据还在进"） ---'
SELECT to_char(to_timestamp(max(created_at)), 'YYYY-MM-DD HH24:MI:SS') AS newest_row,
       to_char(to_timestamp(min(created_at)), 'YYYY-MM-DD HH24:MI:SS') AS oldest_row
  FROM emails;

\echo '--- 这 29 行的 snippet 长度分布（未净化通常是撑满上限的 501） ---'
SELECT length(snippet) AS len, count(*)
  FROM emails
 WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%'
 GROUP BY 1 ORDER BY 1;
