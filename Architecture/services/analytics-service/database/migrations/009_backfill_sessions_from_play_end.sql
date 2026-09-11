-- 009: one playback session per historical play_end (plan Phase 1A,
-- deploy step 2).
--
-- The aggregators will read views from analytics.playback_sessions
-- once ANALYTICS_VIEW_SOURCE flips to 'sessions'. For hours before the
-- sessions table existed there are no live rows, so this writes one
-- finalised session per play_end row already in events_raw — exactly
-- the row the old aggregation counted, carrying exactly the figures it
-- counted — so the new aggregation reproduces the old one per hour
-- bucket. That zero-diff reconciliation is the gate before the flip.
--
-- Faithful to the old row, not to the new rules, on purpose:
--   * first_seen is the play_end timestamp, because that is the bucket
--     the hourly aggregator attributed the view to;
--   * is_display_view is the flag ingest stamped at the time, under the
--     rules that applied then, not model.IsDisplayView re-run today;
--   * percent_covered is percent_viewed, because no heartbeat bitmap
--     exists for these rows, and view_score is what the old
--     view_score_total summed: LEAST(percent_viewed, 100) / 100 for a
--     display view, else 0;
--   * is_self_view is stamped honestly (viewer = creator), so the
--     reconciliation must compare with the aggregators' self-view
--     exclusion in mind: a historical self-view counted then and is
--     excluded now, and that difference is expected, not a defect.
--
-- ON CONFLICT DO NOTHING: any session already written live since 008
-- shipped wins over its backfilled shadow. Rows without a session id or
-- with an unparseable content/creator id cannot be sessions and are
-- left where they are; the raw table is untouched.

INSERT INTO analytics.playback_sessions (
    actor_id, session_id, content_id, creator_id, content_type,
    first_seen, last_seen, content_duration_ms,
    watched_ms, watched_ms_reported, max_playhead_ms, max_continuous_ms,
    loop_count, seek_count, playback_speed, coverage, covered_ms,
    percent_viewed, percent_covered, is_self_view, end_reason,
    finalized_at, finalize_reason, is_display_view, view_score, source
)
SELECT DISTINCT ON (e.user_id, e.session_id, (e.payload->>'content_id')::uuid)
    e.user_id,
    e.session_id,
    (e.payload->>'content_id')::uuid,
    (e.payload->>'creator_id')::uuid,
    COALESCE(NULLIF(e.payload->>'content_type', ''), 'unknown'),
    e.ts,
    e.ts,
    d.duration_ms,
    w.watched_ms,
    COALESCE((e.payload->>'watched_ms_reported')::bigint, w.watched_ms),
    0,
    COALESCE((e.payload->>'max_continuous_watch_ms')::bigint, 0),
    COALESCE((e.payload->>'loop_count')::int, 0),
    0,
    1,
    NULL,
    CASE WHEN d.duration_ms > 0 THEN LEAST(w.watched_ms, d.duration_ms) ELSE w.watched_ms END,
    p.percent_viewed,
    p.percent_viewed,
    (e.user_id = (e.payload->>'creator_id')::uuid),
    e.payload->>'end_reason',
    e.ts,
    'play_end',
    v.is_display_view,
    CASE WHEN v.is_display_view THEN p.percent_viewed / 100.0 ELSE 0 END,
    'backfill'
FROM analytics.events_raw e
CROSS JOIN LATERAL (SELECT COALESCE((e.payload->>'watched_ms_total')::bigint, 0) AS watched_ms) w
CROSS JOIN LATERAL (SELECT COALESCE((e.payload->>'content_duration_ms')::bigint, 0) AS duration_ms) d
CROSS JOIN LATERAL (SELECT LEAST(GREATEST(COALESCE((e.payload->>'percent_viewed')::double precision, 0), 0), 100) AS percent_viewed) p
CROSS JOIN LATERAL (SELECT COALESCE((e.payload->>'is_display_view')::boolean, false) AS is_display_view) v
WHERE e.type = 'play_end'
  AND e.user_id IS NOT NULL
  AND e.session_id IS NOT NULL
  AND e.payload->>'content_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
  AND e.payload->>'creator_id' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
ORDER BY e.user_id, e.session_id, (e.payload->>'content_id')::uuid, e.ts, e.received_at
ON CONFLICT (actor_id, session_id, content_id) DO NOTHING;
