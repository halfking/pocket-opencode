-- The one remaining "unsanitized" row: real QP leak, or a URL query param?
--
-- 2026-10-04: after fixing DeriveSnippet's ordering, backfill took the count
-- from 25 down to 1. This checks whether that 1 is a real leak.
--
-- Counting '=' occurrences separates the two shapes without needing hex parsing:
--   · a QP leak repeats "=0D=0A" dozens of times  -> many '='
--   · a URL query param has exactly one '='        -> e.g. "?param=9943..."
SET search_path TO opencode_pocket;

WITH wide AS (
  SELECT e.id, e.snippet
    FROM emails e
   WHERE (length(e.snippet) > 200 AND e.snippet ~ '=(E[0-9A-F]{2}|0D=0A|8[0-9A-F]{2}|9[0-9A-F]{2})')
      OR e.snippet ILIKE '%Content-Type%' OR e.snippet ILIKE '%boundary%' OR e.snippet ILIKE '%multipart%'
)
SELECT id,
       array_length(regexp_split_to_array(snippet, '='), 1) - 1 AS eq_count,
       length(snippet) AS len,
       substring(snippet from position('=' in snippet) - 30 for 60) AS around_first_eq
  FROM wide;

\echo '--- shape reference: what a REAL leak row looks like (pre-fix samples, 501 chars) ---'
SELECT length(snippet) AS len,
       array_length(regexp_split_to_array(snippet, '='), 1) - 1 AS eq_count,
       left(snippet, 60) AS head
  FROM emails
 WHERE snippet ~ '=0D=0A=0D=0A'
 LIMIT 3;
