-- Transcode delivery start counter (long-video audit lv-6, 2026-11-17).
--
-- A job that is OOM-killed, evicted or crashes returns no error, so the
-- in-process retry cap never sees it and Kafka redelivers it for ever. The
-- worker now counts STARTS before any work and FINISHES whenever a run
-- returns (success, error or a clean drain); starts - finishes is the number
-- of runs that lost their worker. Past MEDIA_TRANSCODE_MAX_LOST_WORKERS the
-- delivery is recorded failed + manual_review like any terminal error.
--
--   transcode_delivery_event     the request (event id) the counts belong
--                                to; a new request resets both counts.
--   transcode_delivery_starts    runs started for that request.
--   transcode_delivery_finishes  runs that returned for that request.
--
-- Expand-only: nullable / defaulted columns on media_assets, which no other
-- writer names. An older worker ignores them; a rollback leaves them unused.
-- They do not reference media, so reclaim_policy.go has nothing to classify.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS transcode_delivery_event    TEXT,
    ADD COLUMN IF NOT EXISTS transcode_delivery_starts   INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS transcode_delivery_finishes INT NOT NULL DEFAULT 0;
