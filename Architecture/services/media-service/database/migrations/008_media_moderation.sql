-- 008_media_moderation.sql: per-asset content-moderation verdict.
-- The transcode worker frame-scans every video and sets this; post-service
-- gates a video post's visibility on it. 'pending' until the scan runs.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (moderation_status IN ('pending', 'passed', 'rejected'));
