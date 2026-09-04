-- Tube (2026-09-05): duration on the wire.
--
-- media_assets.duration_seconds is the whole-second value the transcode
-- worker has always stored. Players need the ffprobe precision
-- (VideoMeta.DurationMs) for the scrubber, watch-progress percentages and
-- the "5:07" badge, so the millisecond value is persisted next to it.
-- Backfill from the seconds column so legacy rows are never 0/absent.
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS duration_ms INT;
UPDATE media_assets
   SET duration_ms = duration_seconds * 1000
 WHERE duration_ms IS NULL AND duration_seconds IS NOT NULL;
