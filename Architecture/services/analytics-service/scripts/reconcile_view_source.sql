-- Reconciliation gate for the play_end -> sessions cutover (plan Phase 1,
-- deploy sequence step 3). Read-only.
--
-- Compares, per (hour bucket, content), the view columns the hourly
-- aggregator derives from play_end rows (the legacy path, hourlyScanSQL)
-- with the ones it derives from finalised playback sessions
-- (hourlySessionScanSQL). Both sides exclude self-views and attribute a
-- session to the hour of its first_seen, which for a backfilled session
-- is the play_end's ts.
--
-- THE GATE: the first result set must be EMPTY before
-- ANALYTICS_VIEW_SOURCE is flipped to sessions. If any pre-cutover hour
-- still differs on a money column when the flip happens, pre-cutover
-- money changes.
--
-- Money columns (must be zero-diff): views_display, view_score_total,
-- watch_time_ms, unique_viewers, play_ends.
-- Informational columns (may legitimately differ on LIVE sessions,
-- because percent_covered replaces percent_viewed after cutover; must
-- match on backfilled ones): completions, early_swipes, rewatches.
--
-- Usage (dev, read-only):
--   docker compose exec -T postgres psql -U postgres -d app -v ON_ERROR_STOP=1 \
--       -f - < services/analytics-service/scripts/reconcile_view_source.sql
-- Restrict the range with the two timestamps in `range`.

WITH range AS (
    SELECT
        -- Edit these to scope the comparison. Default: everything the
        -- backfill could have covered.
        TIMESTAMPTZ '2024-01-01 00:00:00+00' AS from_ts,
        date_trunc('hour', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS to_ts
),
old AS (
    SELECT
        date_trunc('hour', ts AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hour_bucket,
        payload->>'content_id'                                      AS content_id,
        COUNT(*) FILTER (WHERE COALESCE((payload->>'is_display_view')::boolean, false)) AS views_display,
        COALESCE(SUM(LEAST((payload->>'percent_viewed')::double precision, 100.0) / 100.0)
            FILTER (WHERE COALESCE((payload->>'is_display_view')::boolean, false)), 0) AS view_score_total,
        COALESCE(SUM((payload->>'watched_ms_total')::bigint), 0)   AS watch_time_ms,
        COUNT(DISTINCT user_id)                                     AS unique_viewers,
        COUNT(*)                                                    AS play_ends,
        COUNT(*) FILTER (WHERE (payload->>'percent_viewed')::double precision >= 95) AS completions,
        COUNT(*) FILTER (WHERE payload->>'end_reason' = 'swipe_next'
                           AND (payload->>'percent_viewed')::double precision < 25) AS early_swipes,
        COUNT(*) FILTER (WHERE COALESCE((payload->>'loop_count')::int, 0) > 0)      AS rewatches
    FROM analytics.events_raw, range
    WHERE ts >= range.from_ts AND ts < range.to_ts
      AND type = 'play_end'
      AND NOT COALESCE((payload->>'is_self_view')::boolean, false)
      AND payload->>'content_id' IS NOT NULL
      AND payload->>'creator_id' IS NOT NULL
    GROUP BY 1, 2
),
new AS (
    SELECT
        date_trunc('hour', first_seen AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hour_bucket,
        content_id::text                                                    AS content_id,
        COUNT(*) FILTER (WHERE is_display_view)                             AS views_display,
        COALESCE(SUM(view_score) FILTER (WHERE is_display_view), 0)         AS view_score_total,
        COALESCE(SUM(watched_ms), 0)                                        AS watch_time_ms,
        COUNT(DISTINCT actor_id)                                            AS unique_viewers,
        COUNT(*)                                                            AS play_ends,
        COUNT(*) FILTER (WHERE (CASE WHEN source = 'backfill' THEN percent_viewed ELSE percent_covered END) >= 95) AS completions,
        COUNT(*) FILTER (WHERE end_reason = 'swipe_next'
                           AND (CASE WHEN source = 'backfill' THEN percent_viewed ELSE percent_covered END) < 25) AS early_swipes,
        COUNT(*) FILTER (WHERE loop_count > 0)                              AS rewatches,
        COUNT(*) FILTER (WHERE source = 'live')                             AS live_sessions
    FROM analytics.playback_sessions, range
    WHERE first_seen >= range.from_ts AND first_seen < range.to_ts
      AND finalized_at IS NOT NULL
      AND NOT is_self_view
    GROUP BY 1, 2
),
joined AS (
    SELECT
        COALESCE(o.hour_bucket, n.hour_bucket) AS hour_bucket,
        COALESCE(o.content_id, n.content_id)   AS content_id,
        COALESCE(o.views_display, 0)    AS old_views,    COALESCE(n.views_display, 0)    AS new_views,
        COALESCE(o.view_score_total, 0) AS old_score,    COALESCE(n.view_score_total, 0) AS new_score,
        COALESCE(o.watch_time_ms, 0)    AS old_watch,    COALESCE(n.watch_time_ms, 0)    AS new_watch,
        COALESCE(o.unique_viewers, 0)   AS old_unique,   COALESCE(n.unique_viewers, 0)   AS new_unique,
        COALESCE(o.play_ends, 0)        AS old_ends,     COALESCE(n.play_ends, 0)        AS new_ends,
        COALESCE(o.completions, 0)      AS old_complete, COALESCE(n.completions, 0)      AS new_complete,
        COALESCE(o.early_swipes, 0)     AS old_early,    COALESCE(n.early_swipes, 0)     AS new_early,
        COALESCE(o.rewatches, 0)        AS old_rewatch,  COALESCE(n.rewatches, 0)        AS new_rewatch,
        COALESCE(n.live_sessions, 0)    AS live_sessions
    FROM old o
    FULL OUTER JOIN new n ON n.hour_bucket = o.hour_bucket AND n.content_id = o.content_id
)
-- Result set 1: every (hour, content) whose MONEY columns differ.
-- Must be empty before the flip.
SELECT hour_bucket, content_id,
       old_views, new_views,
       round(old_score::numeric, 6) AS old_score, round(new_score::numeric, 6) AS new_score,
       old_watch, new_watch, old_unique, new_unique, old_ends, new_ends,
       live_sessions
FROM joined
WHERE old_views <> new_views
   OR abs(old_score - new_score) > 1e-6
   OR old_watch <> new_watch
   OR old_unique <> new_unique
   OR old_ends <> new_ends
ORDER BY hour_bucket, content_id;

-- Result set 2: summary. money_diff_buckets must be 0; the informational
-- diffs are listed separately.
WITH range AS (
    SELECT TIMESTAMPTZ '2024-01-01 00:00:00+00' AS from_ts,
           date_trunc('hour', NOW() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS to_ts
),
old AS (
    SELECT date_trunc('hour', ts AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hour_bucket,
           payload->>'content_id' AS content_id,
           COUNT(*) FILTER (WHERE COALESCE((payload->>'is_display_view')::boolean, false)) AS views_display,
           COALESCE(SUM(LEAST((payload->>'percent_viewed')::double precision, 100.0) / 100.0)
               FILTER (WHERE COALESCE((payload->>'is_display_view')::boolean, false)), 0) AS view_score_total,
           COALESCE(SUM((payload->>'watched_ms_total')::bigint), 0) AS watch_time_ms,
           COUNT(DISTINCT user_id) AS unique_viewers,
           COUNT(*) AS play_ends,
           COUNT(*) FILTER (WHERE (payload->>'percent_viewed')::double precision >= 95) AS completions
    FROM analytics.events_raw, range
    WHERE ts >= range.from_ts AND ts < range.to_ts AND type = 'play_end'
      AND NOT COALESCE((payload->>'is_self_view')::boolean, false)
      AND payload->>'content_id' IS NOT NULL AND payload->>'creator_id' IS NOT NULL
    GROUP BY 1, 2
),
new AS (
    SELECT date_trunc('hour', first_seen AT TIME ZONE 'UTC') AT TIME ZONE 'UTC' AS hour_bucket,
           content_id::text AS content_id,
           COUNT(*) FILTER (WHERE is_display_view) AS views_display,
           COALESCE(SUM(view_score) FILTER (WHERE is_display_view), 0) AS view_score_total,
           COALESCE(SUM(watched_ms), 0) AS watch_time_ms,
           COUNT(DISTINCT actor_id) AS unique_viewers,
           COUNT(*) AS play_ends,
           COUNT(*) FILTER (WHERE (CASE WHEN source = 'backfill' THEN percent_viewed ELSE percent_covered END) >= 95) AS completions
    FROM analytics.playback_sessions, range
    WHERE first_seen >= range.from_ts AND first_seen < range.to_ts
      AND finalized_at IS NOT NULL AND NOT is_self_view
    GROUP BY 1, 2
),
joined AS (
    SELECT COALESCE(o.hour_bucket, n.hour_bucket) AS hour_bucket,
           COALESCE(o.views_display, 0) AS old_views, COALESCE(n.views_display, 0) AS new_views,
           COALESCE(o.view_score_total, 0) AS old_score, COALESCE(n.view_score_total, 0) AS new_score,
           COALESCE(o.watch_time_ms, 0) AS old_watch, COALESCE(n.watch_time_ms, 0) AS new_watch,
           COALESCE(o.unique_viewers, 0) AS old_unique, COALESCE(n.unique_viewers, 0) AS new_unique,
           COALESCE(o.play_ends, 0) AS old_ends, COALESCE(n.play_ends, 0) AS new_ends,
           COALESCE(o.completions, 0) AS old_complete, COALESCE(n.completions, 0) AS new_complete
    FROM old o FULL OUTER JOIN new n ON n.hour_bucket = o.hour_bucket AND n.content_id = o.content_id
)
SELECT
    COUNT(*)                                                        AS compared_buckets,
    COUNT(*) FILTER (WHERE old_views <> new_views
                        OR abs(old_score - new_score) > 1e-6
                        OR old_watch <> new_watch
                        OR old_unique <> new_unique
                        OR old_ends <> new_ends)                    AS money_diff_buckets,
    COUNT(*) FILTER (WHERE old_complete <> new_complete)            AS completions_diff_buckets,
    SUM(old_views) AS old_views_total, SUM(new_views) AS new_views_total,
    SUM(old_ends)  AS old_play_ends,   SUM(new_ends)  AS new_sessions,
    (SELECT COUNT(*) FROM analytics.playback_sessions WHERE finalized_at IS NULL) AS open_sessions
FROM joined;
