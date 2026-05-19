-- Database setup for media-service (spec-aligned schema)

CREATE TABLE IF NOT EXISTS media_assets (
    id UUID PRIMARY KEY,
    uploader_id UUID NOT NULL,
    file_type TEXT NOT NULL,              -- image, video, audio, document
    media_subtype TEXT NOT NULL,          -- general, avatar, cover, gif
    mime_type TEXT NOT NULL,
    file_size_bytes BIGINT NOT NULL,
    storage_bucket TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    processing_status TEXT NOT NULL,      -- pending_upload, uploaded, processing, ready, failed
    width INT,
    height INT,
    duration_seconds INT,                -- video duration in seconds
    blurhash TEXT,                        -- blur placeholder hash
    alt_text TEXT DEFAULT '',
    original_url VARCHAR(500),
    cdn_url VARCHAR(500),
    thumbnail_url VARCHAR(500),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_media_assets_uploader_id ON media_assets(uploader_id, created_at DESC);

CREATE TABLE IF NOT EXISTS media_variants (
    media_asset_id UUID NOT NULL REFERENCES media_assets(id),
    variant        TEXT NOT NULL,        -- original, thumb_150, small_480, medium_1080, hls_master
    width          INT,
    height         INT,
    size_bytes     BIGINT,
    mime           TEXT NOT NULL,
    object_key     TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (media_asset_id, variant)
);

CREATE TABLE IF NOT EXISTS transcoding_jobs (
    id UUID PRIMARY KEY,
    media_asset_id UUID NOT NULL REFERENCES media_assets(id),
    target_quality VARCHAR(20) NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    output_url VARCHAR(500),
    output_size_bytes BIGINT,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    error_message TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transcoding_jobs_media ON transcoding_jobs(media_asset_id);

-- Idempotent schema upgrades — applied on every boot by BootstrapSchema.
-- migration 008: per-asset content-moderation verdict (video frame scan).
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'pending';

-- migration 007: real S3/MinIO multipart upload backing for resumable uploads.
ALTER TABLE resumable_uploads ADD COLUMN IF NOT EXISTS storage_upload_id TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS resumable_upload_parts (
    upload_id   UUID        NOT NULL REFERENCES resumable_uploads(upload_id) ON DELETE CASCADE,
    part_number INT         NOT NULL,
    etag        TEXT        NOT NULL,
    size_bytes  BIGINT      NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (upload_id, part_number)
);
