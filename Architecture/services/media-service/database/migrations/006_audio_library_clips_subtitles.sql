-- Audio Library: tracks that can be used in Flicks
CREATE TABLE IF NOT EXISTS audio_library (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title          TEXT NOT NULL,
    artist         TEXT NOT NULL DEFAULT '',
    duration_ms    INT NOT NULL,
    waveform_data  JSONB,
    cover_url      TEXT,
    audio_url      TEXT NOT NULL,
    source         TEXT NOT NULL CHECK (source IN ('licensed','original','user_upload')),
    source_post_id UUID,
    source_user_id UUID,
    usage_count    BIGINT NOT NULL DEFAULT 0,
    is_trending    BOOLEAN NOT NULL DEFAULT FALSE,
    is_licensed    BOOLEAN NOT NULL DEFAULT FALSE,
    language       TEXT DEFAULT 'instrumental',
    genre          TEXT,
    mood           TEXT,
    is_active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audio_trending ON audio_library(usage_count DESC) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_audio_source_post ON audio_library(source_post_id) WHERE source = 'original';
CREATE INDEX IF NOT EXISTS idx_audio_genre ON audio_library(genre) WHERE is_active = TRUE;

-- Track which Flicks use which audio
CREATE TABLE IF NOT EXISTS post_audio_refs (
    audio_id UUID NOT NULL REFERENCES audio_library(id) ON DELETE CASCADE,
    post_id  UUID NOT NULL,
    used_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (audio_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_audio_refs_audio ON post_audio_refs(audio_id);

-- Multi-clip editor: ordered clips for a single Flick
CREATE TABLE IF NOT EXISTS media_clips (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id        UUID NOT NULL,
    media_asset_id UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    clip_order     INT NOT NULL,
    trim_start_ms  INT NOT NULL DEFAULT 0,
    trim_end_ms    INT,
    duration_ms    INT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (post_id, clip_order)
);
CREATE INDEX IF NOT EXISTS idx_media_clips_post ON media_clips(post_id);

-- AI-generated subtitles / captions
CREATE TABLE IF NOT EXISTS media_subtitles (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_asset_id  UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    language        VARCHAR(10) NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('auto_generated','manual','translated')),
    format          TEXT NOT NULL DEFAULT 'vtt' CHECK (format IN ('vtt','srt')),
    content_url     TEXT NOT NULL,
    word_level_json JSONB,
    confidence      REAL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_asset_id, language)
);
CREATE INDEX IF NOT EXISTS idx_subtitles_media ON media_subtitles(media_asset_id);
