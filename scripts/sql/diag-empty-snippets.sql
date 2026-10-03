-- Why are 32 rows' snippet empty, and will they ever be filled?
--
-- DeriveSnippet returns "" on purpose ("rather empty than leaking a MIME dump").
-- store.go treats "" as "do not overwrite". So a row that once got "" is
-- **frozen empty forever**: no amount of backfill refreshes it.
-- If those 32 rows are that shape, the email list shows 32 blank previews and
-- re-sync can never fix them. That is a self-healing-chain defect, not a
-- sanitizer defect.
SET search_path TO opencode_pocket;

\echo '--- empty-snippet rows by account and age ---'
SELECT a.email_address,
       count(*) AS empty_rows,
       to_char(to_timestamp(min(e.date)), 'YYYY-MM-DD') AS oldest,
       to_char(to_timestamp(max(e.date)), 'YYYY-MM-DD') AS newest,
       sum(CASE WHEN e.has_attachments THEN 1 ELSE 0 END) AS with_attach
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 WHERE e.snippet = ''
 GROUP BY a.email_address ORDER BY empty_rows DESC;

\echo '--- subjects of the empty rows (are they real mail or parse-error placeholders?) ---'
SELECT left(e.subject, 60) AS subj, length(e.subject) AS subj_len, count(*)
  FROM emails e
 WHERE e.snippet = ''
 GROUP BY 1, 2
 ORDER BY 3 DESC LIMIT 15;

\echo '--- did any of them come from the "parse error" placeholder path? ---'
SELECT count(*) AS parse_error_rows
  FROM emails WHERE snippet LIKE 'parse error:%';

\echo '--- sanity: non-empty rows in the same accounts (is this a per-message shape?) ---'
SELECT a.email_address,
       count(*) FILTER (WHERE e.snippet = '')        AS empty,
       count(*) FILTER (WHERE e.snippet <> '')       AS non_empty
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 GROUP BY a.email_address ORDER BY empty DESC;
