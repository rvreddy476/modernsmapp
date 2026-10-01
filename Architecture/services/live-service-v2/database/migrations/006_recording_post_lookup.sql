-- 006_recording_post_lookup.sql — find the video a recording became (2 Oct 2026).
--
-- post-service makes the unlisted long video from live.stream.vod_ready and
-- does not report its id back, so live_streams.recording_post_id (migration
-- 005) had no writer. live-service-v2 now asks post-service
--
--   GET {POST_SERVICE_URL}/v1/internal/posts/by-live-stream/<stream id>
--
-- for every import that reached 'done' (vod_ready went out) and stores the
-- answer. The lookup's own state lives on the import job:
--
--   post_lookup_state     pending  still asking
--                         found    recording_post_id is stored
--                         deleted  the post was deleted: nothing is offered
--                                  and nobody asks again
--                         gave_up  no post after 24 hours
--   post_lookup_attempts  sweeper lookups that did not settle it (backoff)
--   post_lookup_next_at   when the sweeper asks next
--   post_lookup_last_at   when anyone last asked (the stream detail read
--                         asks too, at most once per few seconds per stream)
--
-- Every existing 'done' job starts 'pending' and due now, which is the
-- backfill for streams that ended before this migration.
--
-- Additive and idempotent.

ALTER TABLE live_recording_imports
    ADD COLUMN IF NOT EXISTS post_lookup_state    TEXT        NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS post_lookup_attempts INT         NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS post_lookup_next_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS post_lookup_last_at  TIMESTAMPTZ;

ALTER TABLE live_recording_imports DROP CONSTRAINT IF EXISTS live_recording_imports_post_lookup_state_check;
ALTER TABLE live_recording_imports ADD CONSTRAINT live_recording_imports_post_lookup_state_check
    CHECK (post_lookup_state IN ('pending','found','deleted','gave_up'));

CREATE INDEX IF NOT EXISTS idx_live_recording_imports_post_lookup
    ON live_recording_imports (post_lookup_next_at)
    WHERE state = 'done' AND post_lookup_state = 'pending';
