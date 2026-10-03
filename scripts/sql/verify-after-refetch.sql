-- 删除 + 回补之后的复核（ASCII-only：psql 客户端在 -c 多行串里遇到中文会报
-- "invalid byte sequence for encoding UTF8"，那是**客户端编码问题**不是数据问题）。
SET search_path TO opencode_pocket;
\echo '--- 回补后复核 ---'
SELECT 'unsanitized' AS k, count(*) AS v FROM emails
 WHERE (length(snippet) > 200 AND snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR snippet ILIKE '%Content-Type%' OR snippet ILIKE '%boundary%' OR snippet ILIKE '%multipart%'
UNION ALL SELECT 'total_rows', count(*) FROM emails
UNION ALL SELECT 'len_501', count(*) FROM emails WHERE length(snippet) = 501
UNION ALL SELECT 'empty_snippet', count(*) FROM emails WHERE snippet = ''
UNION ALL SELECT 'null_snippet', count(*) FROM emails WHERE snippet IS NULL;

\echo '--- 刚回补进来的 29 封（account 5 / 3），看它们的 snippet 形态 ---'
SELECT a.email_address, length(e.snippet) AS len, left(e.snippet, 70) AS head, count(*) OVER (PARTITION BY a.email_address) AS rows_in_acct
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 WHERE e.account_id IN ('acct-1790870162079171800-5','acct-1790870162063806800-3')
   AND e.snippet ~ '=(0D|E5|E7|E4|B8)' OR (e.snippet ILIKE '%Content-Type%' AND e.account_id IN ('acct-1790870162079171800-5','acct-1790870162063806800-3'))
 LIMIT 10;
