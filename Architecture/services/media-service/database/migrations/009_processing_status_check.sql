-- H7: CHECK constraint on media_assets.processing_status. setup.sql
-- already documents the accepted values in a comment
-- ("pending_upload, uploaded, processing, ready, failed") but never
-- enforced them at the DB. Service paths set these values consistently
-- (internal/service/media.go, internal/service/audio.go); push the
-- constraint to the DB as defence-in-depth so a direct write or a
-- future refactor can't introduce a typo silently.

ALTER TABLE media_assets DROP CONSTRAINT IF EXISTS media_assets_processing_status_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_processing_status_check
    CHECK (processing_status IN ('pending_upload', 'uploaded', 'processing', 'ready', 'failed'));
