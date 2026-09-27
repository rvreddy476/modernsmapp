-- MTube captions list for creators (2026-09-27).
--
-- A caption row gains a creator-facing review state. Uploaded and
-- owner-corrected tracks are published on write (the default); a track the
-- caption job generated is set to a draft by the job's completion path
-- (service/voice.go runCaptionJob) and stays one until the creator publishes
-- it from GET /v1/subtitles/mine / PATCH /v1/subtitles/:mediaId/:language.
--
-- The default is TRUE deliberately: every row that exists today was either
-- uploaded by its owner or already shown to viewers, and a migration must
-- not silently hide any of them.
--
-- updated_at already exists (migration 012); repeated here idempotently so
-- the column this feature sorts on is guaranteed by the migration that
-- introduces the feature.
ALTER TABLE media_subtitles
    ADD COLUMN IF NOT EXISTS published  BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- The creator list walks (media_asset_id, updated_at) per uploader; the
-- uploader itself is resolved through media_assets(uploader_id), which is
-- already indexed (idx_media_assets_uploader_id).
CREATE INDEX IF NOT EXISTS idx_media_subtitles_media_updated
    ON media_subtitles (media_asset_id, updated_at DESC);
