# Module: media-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /:mediaId
DELETE /:ownerType/:ownerId/:slotName
DELETE /sessions/:id
GET /:audioId
GET /:audioId/url
GET /:mediaId
GET /:mediaId/renditions
GET /:mediaId/serve
GET /:mediaId/serve/:variant
GET /:mediaId/status
GET /:mediaId/url
GET /:mediaId/url/:variant
GET /:ownerType/:ownerId
GET /:postId
GET /resumable/:uploadId
GET /search
GET /sessions
GET /sticker-packs
GET /stickers
GET /templates
GET /trending
GET /v1/media/:mediaId/suggested-frames
PATCH /:mediaId/alt-text
POST /:audioId/use
POST /batch
POST /confirm
POST /extract/:mediaId
POST /init
POST /:mediaId
POST /:mediaId/auto
POST /:mediaId/extract-frame
POST /:mediaId/frames
POST /:postId
POST /resumable/init
POST /resumable/:uploadId/chunk
POST /resumable/:uploadId/complete
POST /sessions
POST /stickers/:id/use
POST /upload/presigned
POST /v1/media/:mediaId/cover-frame
POST /voiceover
PUT /:ownerType/:ownerId/:slotName
PUT /sessions/:id
GROUP /v1/audio
GROUP /v1/audio-library
GROUP /v1/clips
GROUP /v1/media
GROUP /v1/media/slots
GROUP /v1/media/studio
GROUP /v1/media/upload
GROUP /v1/subtitles
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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

CREATE TABLE IF NOT EXISTS resumable_uploads (
    upload_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id        UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    uploader_id     UUID NOT NULL,
    total_bytes     BIGINT NOT NULL,
    uploaded_bytes  BIGINT NOT NULL DEFAULT 0,
    chunk_size      BIGINT NOT NULL DEFAULT 5242880,
    status          TEXT NOT NULL DEFAULT 'initiated'
                       CHECK (status IN ('initiated','uploading','completed','aborted','expired')),
    storage_key     TEXT NOT NULL,
    last_chunk_at   TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS resumable_upload_parts (
    upload_id   UUID        NOT NULL REFERENCES resumable_uploads(upload_id) ON DELETE CASCADE,
    part_number INT         NOT NULL,
    etag        TEXT        NOT NULL,
    size_bytes  BIGINT      NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (upload_id, part_number)
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

CREATE TABLE IF NOT EXISTS owner_media_slots (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type      TEXT NOT NULL,          -- 'profile', 'channel', 'group', 'module_profile'
    owner_id        UUID NOT NULL,
    slot_name       TEXT NOT NULL,           -- 'avatar', 'banner', 'watermark', 'intro_video', 'cover'
    media_asset_id  UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    status          TEXT NOT NULL DEFAULT 'pending',  -- 'pending', 'active', 'replaced', 'removed'
    crop_x          DOUBLE PRECISION,       -- normalized 0.0-1.0
    crop_y          DOUBLE PRECISION,
    crop_w          DOUBLE PRECISION,
    crop_h          DOUBLE PRECISION,
    focal_x         DOUBLE PRECISION DEFAULT 0.5,
    focal_y         DOUBLE PRECISION DEFAULT 0.5,
    assigned_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    replaced_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS owner_media_resolved (
    owner_type      TEXT NOT NULL,
    owner_id        UUID NOT NULL,
    slot_name       TEXT NOT NULL,
    media_asset_id  UUID NOT NULL,
    blurhash        TEXT,
    width           INT,
    height          INT,
    variants        JSONB NOT NULL DEFAULT '{}',   -- {"thumb_150": "object_key", "small_480": "object_key", ...}
    focal_x         DOUBLE PRECISION DEFAULT 0.5,
    focal_y         DOUBLE PRECISION DEFAULT 0.5,
    resolved_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (owner_type, owner_id, slot_name)
);

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

CREATE TABLE IF NOT EXISTS post_audio_refs (
    audio_id UUID NOT NULL REFERENCES audio_library(id) ON DELETE CASCADE,
    post_id  UUID NOT NULL,
    used_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (audio_id, post_id)
);

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

CREATE TABLE IF NOT EXISTS resumable_upload_parts (
    upload_id   UUID        NOT NULL REFERENCES resumable_uploads(upload_id) ON DELETE CASCADE,
    part_number INT         NOT NULL,
    etag        TEXT        NOT NULL,
    size_bytes  BIGINT      NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (upload_id, part_number)
);

```

## API types (request/response Go structs with JSON tags)
```go
type ExtractAudioRequest struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

type InitUploadRequest struct {
	FileType      string `json:"file_type" binding:"required,oneof=image video"`
	MediaSubtype  string `json:"media_subtype"`
	MimeType      string `json:"mime_type" binding:"required"`
	FileSizeBytes int64  `json:"file_size_bytes" binding:"required,min=1"`
	AltText       string `json:"alt_text"`
}

type ConfirmUploadRequest struct {
	MediaID string `json:"media_id" binding:"required"`
}

type BatchMediaURLsRequest struct {
	IDs []string `json:"ids" binding:"required,min=1,max=50"`
}

type UpdateAltTextRequest struct {
	AltText string `json:"alt_text" binding:"required"`
}

type GetPresignedUploadURLRequest struct {
	Filename    string `json:"filename" binding:"required"`
	ContentType string `json:"content_type" binding:"required"`
}

type ExtractFrameRequest struct {
	TimestampMs int `json:"timestamp_ms"`
}

type jwtClaims struct {
	Sub    string `json:"sub"`
	UserID string `json:"user_id"`
	Exp    int64  `json:"exp"`
}

type InitResumableUploadRequest struct {
	FileType   string `json:"file_type" binding:"required,oneof=image video"`
	MimeType   string `json:"mime_type" binding:"required"`
	TotalBytes int64  `json:"total_bytes" binding:"required,min=1"`
}

type assignSlotRequest struct {
	MediaAssetID string              `json:"media_asset_id" binding:"required"`
	Crop         *service.CropParams `json:"crop"`
}

type batchOwnersRequest struct {
	Owners []postgres.OwnerRef `json:"owners" binding:"required,min=1,max=100"`
}

type createSessionReq struct {
	Mode         string          `json:"mode" binding:"required"`
	StateJSON    json.RawMessage `json:"state_json"`
	ThumbnailB64 string          `json:"thumbnail_base64"`
}

type SuggestedFrame struct {
	OffsetMs     int     `json:"offset_ms"`
	ThumbnailURL string  `json:"thumbnail_url"`
	QualityScore float64 `json:"quality_score"`
}

type setCoverFrameReq struct {
	OffsetMs int `json:"offset_ms"`
}
```
