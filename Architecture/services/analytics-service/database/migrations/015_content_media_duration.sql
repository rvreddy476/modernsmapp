-- 015: the server-known media duration on the ownership projection
-- (audit M6, 2026-10-30).
--
-- A playback event used to be judged against the content_duration_ms the
-- client reported. A scripted client could claim a two-hour long video
-- was 2 s long, report 2 s watched, and be credited a display view with a
-- 100% view_score: both feed the creator-fund accrual. The duration a
-- view is judged against is now the one post-service measured, carried on
-- the events analytics already consumes:
--
--   * PostCreated.duration_ms / duration_seconds (known when the post is
--     created after transcode, e.g. a scheduled publish), and
--   * PostSearchEligibilityChanged.duration_ms, emitted in the same
--     transaction that flips a processing video from 'pending' to
--     'approved' once transcode has measured it.
--
-- 0 means unknown. IngestService never awards a display view, a
-- percent_viewed or a view score against an unknown duration; the
-- client's figure is kept for audit only.
--
-- Expand-only.
ALTER TABLE analytics.content_ownership
    ADD COLUMN IF NOT EXISTS media_duration_ms BIGINT NOT NULL DEFAULT 0;

-- One-off backfill for content projected before this column existed.
-- analytics.content_ownership lives in the shared `app` database next to
-- post-service's post_media and media-service's media_assets (see
-- tools/contentownershipbackfill), so the measured duration can be read in
-- place. The same expression post-service puts on the eligibility event
-- (search_eligibility.go): the longest attached video, in milliseconds.
--
-- Guarded: a database without those tables (an isolated analytics test
-- database, a split deployment) skips the backfill, and its content stays
-- unknown until the next eligibility event carries the duration.
DO $$
DECLARE
    has_ms BOOLEAN;
BEGIN
    IF to_regclass('public.post_media') IS NULL
       OR to_regclass('public.media_assets') IS NULL
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns
                      WHERE table_schema = 'public' AND table_name = 'post_media' AND column_name = 'kind')
       OR NOT EXISTS (SELECT 1 FROM information_schema.columns
                      WHERE table_schema = 'public' AND table_name = 'media_assets' AND column_name = 'duration_seconds')
    THEN
        RAISE NOTICE 'analytics 015: post_media/media_assets not in this database; media_duration_ms backfill skipped';
        RETURN;
    END IF;
    SELECT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'public' AND table_name = 'media_assets' AND column_name = 'duration_ms')
      INTO has_ms;
    EXECUTE format($q$
        UPDATE analytics.content_ownership co
           SET media_duration_ms = d.ms
          FROM (SELECT pm.post_id, MAX(%s) AS ms
                  FROM public.post_media pm
                  JOIN public.media_assets ma ON ma.id = pm.media_id
                 WHERE pm.kind = 'video'
                 GROUP BY pm.post_id) d
         WHERE d.post_id = co.content_id
           AND d.ms > 0
           AND d.ms <= 43200000
           AND co.media_duration_ms = 0
    $q$, CASE WHEN has_ms
              THEN 'COALESCE(ma.duration_ms::bigint, ma.duration_seconds::bigint * 1000, 0)'
              ELSE 'COALESCE(ma.duration_seconds::bigint * 1000, 0)' END);
END $$;
