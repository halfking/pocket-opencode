-- Why are the 25 dirty rows not refreshed by backfill? UID coverage vs fetch range.
SET search_path TO opencode_pocket;

\echo '--- dirty-row UID distribution per account ---'
SELECT a.email_address,
       count(*) AS dirty_rows,
       min(e.uid) AS uid_min,
       max(e.uid) AS uid_max,
       count(*) FILTER (WHERE e.uid IS NULL) AS uid_null
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 WHERE (length(e.snippet) > 200 AND e.snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
    OR e.snippet ILIKE '%Content-Type%' OR e.snippet ILIKE '%boundary%' OR e.snippet ILIKE '%multipart%'
 GROUP BY a.email_address ORDER BY dirty_rows DESC;

\echo '--- overall UID coverage per account: rows / uid_min / uid_max / missing-uid rows ---'
SELECT a.email_address,
       count(*) AS total_rows,
       count(*) FILTER (WHERE e.uid IS NULL) AS uid_null,
       min(e.uid) AS uid_min,
       max(e.uid) AS uid_max,
       count(DISTINCT e.message_id) AS distinct_msgid
  FROM emails e JOIN email_accounts a ON a.id = e.account_id
 GROUP BY a.email_address ORDER BY total_rows DESC;

\echo '--- did the upsert even fire? xmin age vs the 25 dirty rows ---'
SELECT count(*) FILTER (WHERE e.xmin::text::bigint > 0) AS rows_ever_updated,
       count(*) FILTER (WHERE e.xmin::text::bigint = 0) AS rows_never_updated
  FROM emails e
 WHERE e.account_id = 'acct-1790870162079171800-5';

\echo '--- sample of dirty rows: id / uid / message_id / date ---'
SELECT e.uid, length(e.snippet) AS len, e.date, left(e.message_id, 40) AS msgid
  FROM emails e
 WHERE e.account_id = 'acct-1790870162079171800-5'
   AND e.snippet ~ '=(0D|E5|E7|E4|B8)'
 ORDER BY e.uid DESC LIMIT 12;
