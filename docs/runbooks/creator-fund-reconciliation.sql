-- Creator fund: dashboard versus statement reconciliation.
-- Read-only. Runs against the database that holds both the analytics schema
-- and the monetization tables (they share one database by design; the
-- boundary is the view analytics.v_creator_daily_metrics_v1).
--
-- See docs/runbooks/creator-fund-reconciliation.md for what each class means
-- and the tolerance that applies to it.
--
--   psql -d app -v ON_ERROR_STOP=1 -f docs/runbooks/creator-fund-reconciliation.sql
--   psql -d app -v ON_ERROR_STOP=1 -v since=2026-09-01 -f docs/runbooks/creator-fund-reconciliation.sql

\if :{?since}
\else
  \set since 1970-01-01
\endif

SET TIME ZONE 'UTC';

WITH
-- What the creator's dashboard and the public counter show: the contract
-- view, canonicalised to the two types the fund pays for, exactly as
-- monetization-service's AggregateDailyInputs does it.
dashboard AS (
  SELECT creator_id, day_bucket,
         CASE content_type
           WHEN 'flick'      THEN 'flick'
           WHEN 'reel'       THEN 'flick'
           WHEN 'long_video' THEN 'long_video'
         END AS content_type,
         SUM(views_display)::BIGINT        AS views,
         SUM(watch_time_total_ms)::BIGINT  AS watch_ms
  FROM analytics.v_creator_daily_metrics_v1
  WHERE day_bucket >= :'since'::date
  GROUP BY 1, 2, 3
),
-- What the statement says was paid on: every live earnings row. Reversed
-- rows are out (the statement drops them and shows a memo line instead);
-- budget-exhausted zero rows stay in, with their skip_reason, because they
-- record the views even though nothing accrued.
statement AS (
  SELECT creator_id, day_bucket, content_type,
         SUM(view_count)::BIGINT    AS views,
         SUM(watch_time_ms)::BIGINT AS watch_ms,
         MAX(input_revision)        AS input_revision,
         STRING_AGG(DISTINCT COALESCE(skip_reason, ''), ',') AS skip_reasons
  FROM creator_fund_earnings
  WHERE status <> 'reversed'
    AND day_bucket >= :'since'::date
  GROUP BY 1, 2, 3
),
-- A day still inside the analytics reprocessing window can legitimately
-- move; a frozen day cannot, so on a frozen day the two sides must agree
-- to the view.
open_days AS (
  SELECT day FROM analytics.rollup_progress WHERE status = 'open'
),
joined AS (
  SELECT COALESCE(d.creator_id, s.creator_id)     AS creator_id,
         COALESCE(d.day_bucket, s.day_bucket)     AS day_bucket,
         COALESCE(d.content_type, s.content_type) AS content_type,
         d.views    AS dashboard_views,
         s.views    AS statement_views,
         d.watch_ms AS dashboard_watch_ms,
         s.watch_ms AS statement_watch_ms,
         s.skip_reasons,
         (COALESCE(d.day_bucket, s.day_bucket) IN (SELECT day FROM open_days)) AS day_open
  FROM dashboard d
  FULL OUTER JOIN statement s
    ON s.creator_id = d.creator_id AND s.day_bucket = d.day_bucket AND s.content_type = d.content_type
  WHERE COALESCE(d.content_type, s.content_type) IS NOT NULL
    AND (COALESCE(d.views, 0) > 0 OR s.views IS NOT NULL)
),
classified AS (
  SELECT *,
         CASE
           WHEN statement_views IS NOT NULL AND dashboard_views IS NOT NULL
                AND statement_views = dashboard_views AND statement_watch_ms = dashboard_watch_ms
             THEN 'match'
           WHEN statement_views IS NULL AND day_open
             THEN 'open_day_not_yet_accrued'
           WHEN statement_views IS NULL
             THEN 'no_statement_row'
           WHEN dashboard_views IS NULL
             THEN 'statement_without_dashboard'
           WHEN day_open AND dashboard_views >= statement_views
             THEN 'open_day_dashboard_ahead'
           ELSE 'mismatch'
         END AS class
  FROM joined
)
SELECT class,
       COUNT(*)                                          AS rows,
       COALESCE(SUM(dashboard_views), 0)                 AS dashboard_views,
       COALESCE(SUM(statement_views), 0)                 AS statement_views,
       COALESCE(SUM(dashboard_views - statement_views), 0) AS view_gap,
       MIN(day_bucket)                                   AS first_day,
       MAX(day_bucket)                                   AS last_day
FROM classified
GROUP BY class
ORDER BY class;

-- The lines that need a human: anything a frozen day disagrees on, and any
-- frozen day the dashboard shows views for that the statement never saw.
WITH
dashboard AS (
  SELECT creator_id, day_bucket,
         CASE content_type WHEN 'flick' THEN 'flick' WHEN 'reel' THEN 'flick' WHEN 'long_video' THEN 'long_video' END AS content_type,
         SUM(views_display)::BIGINT AS views, SUM(watch_time_total_ms)::BIGINT AS watch_ms
  FROM analytics.v_creator_daily_metrics_v1 WHERE day_bucket >= :'since'::date GROUP BY 1, 2, 3
),
statement AS (
  SELECT creator_id, day_bucket, content_type, SUM(view_count)::BIGINT AS views, SUM(watch_time_ms)::BIGINT AS watch_ms,
         STRING_AGG(DISTINCT COALESCE(skip_reason, ''), ',') AS skip_reasons
  FROM creator_fund_earnings WHERE status <> 'reversed' AND day_bucket >= :'since'::date GROUP BY 1, 2, 3
)
SELECT COALESCE(d.creator_id, s.creator_id) AS creator_id,
       COALESCE(d.day_bucket, s.day_bucket) AS day_bucket,
       COALESCE(d.content_type, s.content_type) AS content_type,
       d.views AS dashboard_views, s.views AS statement_views,
       d.watch_ms AS dashboard_watch_ms, s.watch_ms AS statement_watch_ms,
       s.skip_reasons,
       e.status AS creator_eligibility
FROM dashboard d
FULL OUTER JOIN statement s
  ON s.creator_id = d.creator_id AND s.day_bucket = d.day_bucket AND s.content_type = d.content_type
LEFT JOIN creator_fund_eligibility e ON e.creator_id = COALESCE(d.creator_id, s.creator_id)
WHERE COALESCE(d.content_type, s.content_type) IS NOT NULL
  AND COALESCE(d.day_bucket, s.day_bucket) NOT IN (SELECT day FROM analytics.rollup_progress WHERE status = 'open')
  AND (COALESCE(d.views, 0) > 0 OR s.views IS NOT NULL)
  AND (s.views IS NULL OR d.views IS NULL OR s.views <> d.views OR s.watch_ms <> d.watch_ms)
ORDER BY 2, 1, 3;
