-- 016: when is a rolled-up day final? A named answer for monetization
-- (review 2026-11-07).
--
-- The daily rollup re-rolls every open day every 15 minutes for 48 hours
-- after it ends (aggregation/daily.go, freezeWindow). monetization-service
-- used to price "yesterday" at 03:00, inside that window, so a late view
-- changed the rows behind an accrued day and failed the creator's whole
-- period settlement with INPUT_REVISION_CHANGED. It now prices a day only
-- once analytics says the day is final, and this view is where it asks.
--
-- ============================================================================
--  analytics.v_daily_rollup_finality_v1 is a VERSIONED CONTRACT, exactly
--  like v_creator_daily_metrics_v1 (migration 012): never change its
--  columns or their meaning; a different shape is a _v2 beside it.
--
--  One row, one column:
--    final_before  DATE. Every day_bucket strictly before it is final:
--                  its 48-hour window has closed and the catch-up walk
--                  will never rewrite it again. Days on or after it may
--                  still change.
--
--  It is the smaller of two facts, so neither can make a day final early:
--    * the rollup watermark (aggregation_settings.daily_rollup_watermark):
--      the walk starts there, so nothing before it is ever re-rolled. It
--      only moves past a day once that day has aged out of the window;
--    * today (UTC) minus two days: aggregation.IsFrozen's rule, so a
--      watermark written ahead of time cannot finalise an open day.
--  No watermark row yet means nothing is final (the view returns NULL and
--  the reader treats NULL as "wait").
--
--  The forced operator rewrite (?force=1) can still touch a final day; that
--  is a deliberate correction, and monetization records the difference as
--  a pending adjustment rather than repricing paid money.
--
--  aggregation.FinalBefore is the same rule in Go, and a test pins the two
--  to IsFrozen.
-- ============================================================================
--
-- Expand-only: a new view, nothing altered.
--
-- LEAST ignores NULLs, so the missing-watermark case is spelled out: a
-- bare LEAST would fall back to today - 2 and finalise days the walk has
-- not yet visited.
CREATE OR REPLACE VIEW analytics.v_daily_rollup_finality_v1 AS
SELECT CASE
           WHEN w.watermark IS NULL THEN NULL
           ELSE LEAST(w.watermark, (NOW() AT TIME ZONE 'UTC')::date - 2)
       END AS final_before
FROM (
    SELECT (SELECT NULLIF(TRIM(value), '')::date
              FROM analytics.aggregation_settings
             WHERE key = 'daily_rollup_watermark') AS watermark
) AS w;

COMMENT ON VIEW analytics.v_daily_rollup_finality_v1 IS
    'Versioned contract read by monetization-service: day_bucket < final_before is final (48h re-roll window closed). Do not alter; a new shape is v_daily_rollup_finality_v2.';
