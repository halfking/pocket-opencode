-- After the days=120 backfill: what exactly is left?
--   · 1 row with snippet='' (was 32 before the fix)
--   · 2 rows matching the wide "unsanitized" predicate (was 25)
-- Both are one-row questions, so they are asked here together: if the two
-- populations overlap it matters, and two separate queries could hide that.
SET search_path TO opencode_pocket;

\echo '--- the remaining empty-snippet row ---'
SELECT e.id, a.email_address, e.uid, to_char(to_timestamp(e.date),'YYYY-MM-DD') AS mail_date,
       left(e.subject, 60) AS subj, length(e.snippet) AS len
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 WHERE e.snippet = '';

\echo '--- the remaining wide-predicate rows, with the = count that separates a leak from a URL ---'
WITH wide AS (
  SELECT e.id, e.uid, e.snippet, a.email_address
    FROM emails e JOIN email_accounts a ON a.id = e.account_id
   WHERE (length(e.snippet) > 200 AND e.snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
      OR e.snippet ILIKE '%Content-Type%' OR e.snippet ILIKE '%boundary%' OR e.snippet ILIKE '%multipart%'
)
SELECT email_address, uid,
       array_length(regexp_split_to_array(snippet, '='), 1) - 1 AS eq_count,
       length(snippet) AS len,
       left(snippet, 80) AS head
  FROM wide ORDER BY eq_count DESC;

\echo '--- are they the same rows? (empty ∩ wide) ---'
SELECT count(*) AS both_empty_and_wide
  FROM emails
 WHERE snippet = ''
   AND (snippet ~ '=(E[0-9A-F]{2}|0D=0A)' OR snippet ILIKE '%Content-Type%');
