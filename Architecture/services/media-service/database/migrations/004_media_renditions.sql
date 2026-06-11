-- 004_media_renditions.sql: Production-grade renditions tracking per Gold Spec
-- Replaces simple transcoding_jobs with a full renditions lifecycle table.

CREATE TABLE IF NOT EXISTS media_renditions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id        UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    rendition_type  TEXT NOT NULL,  -- 'video', 'thumbnail', 'preview_gif', 'sprite_sheet', 'audio', 'waveform', 'hls_variant', 'hls_segment'
    quality         TEXT NOT NULL,  -- '360p', '480p', '720p', '1080p', '4k', 'thumb_150', 'thumb_300', 'preview', 'master', 'audio_aac'
    object_key      TEXT,           -- blob storage key (NULL until generated)
    mime_type       TEXT,
    width           INT,
    height          INT,
    size_bytes      BIGINT,
    duration_ms     INT,
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending, processing, completed, failed, retrying
    retry_count     INT NOT NULL DEFAULT 0,
    max_retries     INT NOT NULL DEFAULT 3,
    error_message   TEXT,
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_renditions_media_id ON media_renditions(media_id);
CREATE INDEX IF NOT EXISTS idx_renditions_status ON media_renditions(status) WHERE status IN ('pending', 'processing', 'retrying', 'failed');
CREATE UNIQUE INDEX IF NOT EXISTS idx_renditions_media_quality ON media_renditions(media_id, rendition_type, quality);

COMMENT ON TABLE media_renditions IS 'Tracks every output rendition for a media asset with retry support';

-- Audio tracks table for the audio/music system (Gold Spec §5.5).
--
-- NOTE on cross-service collision: post-service migration 013 also creates
-- `audio_tracks` in the same `app` DB with a different shape (media_id,
-- use_count, is_trending, original_post_id, ...). Whichever service boots
-- first wins the CREATE TABLE race. Mirror the defensive pattern post-service
-- 013 already uses — add the media-service-specific columns idempotently so
-- the subsequent CREATE INDEX statements have something to reference.
-- The full reconciliation (one canonical owner of audio_tracks) is tracked
-- separately as tech debt; this just keeps both services bootable on a
-- shared DB.
CREATE TABLE IF NOT EXISTS audio_tracks (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_media_id  UUID REFERENCES media_assets(id) ON DELETE SET NULL,
    source_reel_id   UUID,          -- references post-service reels; not FK'd across services
    title            TEXT NOT NULL DEFAULT 'Original Sound',
    artist           TEXT NOT NULL DEFAULT '',
    genre            TEXT,
    audio_key        TEXT,           -- blob storage key for extracted audio (AAC/M4A)
    waveform_key     TEXT,           -- blob storage key for waveform JSON
    duration_ms      INT NOT NULL DEFAULT 0,
    sample_rate      INT,
    status           TEXT NOT NULL DEFAULT 'processing',  -- processing, ready, rejected, deleted
    is_original      BOOLEAN NOT NULL DEFAULT TRUE,
    license_type     TEXT NOT NULL DEFAULT 'standard',    -- standard, creative_commons, licensed
    usage_count      INT NOT NULL DEFAULT 0,              -- async-updated snapshot (truth in analytics)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotent column adds — cover the case where post-service migration 013
-- already created `audio_tracks` with its own shape and these columns are
-- missing. All nullable on the additive path; the index below tolerates
-- nulls via the partial predicate.
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_media_id UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_reel_id  UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS audio_key       TEXT;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS waveform_key    TEXT;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS sample_rate     INT;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS status          TEXT NOT NULL DEFAULT 'processing';
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS license_type    TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS usage_count     INT  NOT NULL DEFAULT 0;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS idx_audio_tracks_source_media ON audio_tracks(source_media_id) WHERE source_media_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_audio_tracks_status ON audio_tracks(status);
CREATE INDEX IF NOT EXISTS idx_audio_tracks_usage ON audio_tracks(usage_count DESC) WHERE status = 'ready';

COMMENT ON TABLE audio_tracks IS 'Audio/music layer for reels — trending sounds, attribution, reuse chain';

-- Resumable uploads tracking (Gold Spec §5.2)
CREATE TABLE IF NOT EXISTS resumable_uploads (
    upload_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id        UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    uploader_id     UUID NOT NULL,
    total_bytes     BIGINT NOT NULL,
    uploaded_bytes  BIGINT NOT NULL DEFAULT 0,
    chunk_size      INT NOT NULL DEFAULT 5242880,  -- 5 MB default
    total_parts     INT NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT 'initiated',  -- initiated, uploading, completing, completed, expired
    mime_type       TEXT NOT NULL,
    object_key      TEXT NOT NULL,
    upload_token    TEXT,  -- S3 multipart upload ID or tus token
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_resumable_uploads_media ON resumable_uploads(media_id);
CREATE INDEX IF NOT EXISTS idx_resumable_uploads_status ON resumable_uploads(status) WHERE status IN ('initiated', 'uploading');
CREATE INDEX IF NOT EXISTS idx_resumable_uploads_expiry ON resumable_uploads(expires_at) WHERE status != 'completed';

COMMENT ON TABLE resumable_uploads IS 'Tracks multipart/resumable upload sessions for large files';
