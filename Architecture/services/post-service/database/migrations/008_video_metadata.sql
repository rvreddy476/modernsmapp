-- 008_video_metadata.sql
-- Video metadata table for unified video module (Flicks + Long Videos)

CREATE TABLE IF NOT EXISTS video_metadata (
    post_id             UUID PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    duration_seconds    REAL NOT NULL DEFAULT 0,
    width               INT,
    height              INT,
    aspect_ratio        TEXT,
    orientation         TEXT NOT NULL DEFAULT 'landscape'
                        CHECK (orientation IN ('portrait', 'landscape', 'square')),
    file_size_bytes     BIGINT,
    mime_type           TEXT,
    codec_video         TEXT,
    codec_audio         TEXT,
    frame_rate          REAL,
    storage_video_url   TEXT,
    playback_url        TEXT,
    thumbnail_url       TEXT,
    trim_start_ms       INT DEFAULT 0,
    trim_end_ms         INT,
    computed_category   TEXT NOT NULL DEFAULT 'flick'
                        CHECK (computed_category IN ('flick', 'long_video')),
    final_category      TEXT NOT NULL DEFAULT 'flick'
                        CHECK (final_category IN ('flick', 'long_video')),
    upload_status       TEXT NOT NULL DEFAULT 'pending'
                        CHECK (upload_status IN ('pending', 'processing', 'ready', 'failed')),
    media_asset_id      UUID,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_video_metadata_status ON video_metadata(upload_status);
CREATE INDEX IF NOT EXISTS idx_video_metadata_category ON video_metadata(final_category);
CREATE INDEX IF NOT EXISTS idx_posts_author_flick ON posts(author_id, created_at DESC) WHERE content_type = 'flick' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_author_long_video ON posts(author_id, created_at DESC) WHERE content_type = 'long_video' AND deleted_at IS NULL;

ALTER TABLE post_engagement_counts ADD COLUMN IF NOT EXISTS view_count INT NOT NULL DEFAULT 0;
