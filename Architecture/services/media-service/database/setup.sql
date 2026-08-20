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

-- Media-owned canonical reference for chat attachments. The restrictive FK
-- is the serialization mechanism for attach versus physical delete: a send
-- cannot validate an asset and then leave a dangling reference.
CREATE TABLE IF NOT EXISTS media_chat_attachment_reservations (
    reference_id UUID PRIMARY KEY,
    media_id UUID NOT NULL REFERENCES media_assets(id) ON DELETE RESTRICT,
    uploader_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_media_chat_attachment_media
    ON media_chat_attachment_reservations(media_id);

-- migration 002 backfill: vertical orientation flag for reels rendering.
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS is_vertical BOOLEAN NOT NULL DEFAULT FALSE;

-- migration 003 backfill: HLS master playlist key (blob storage path).
-- NULL means HLS not generated for the asset.
ALTER TABLE media_assets ADD COLUMN IF NOT EXISTS hls_master_key TEXT;

-- migration 004 backfill: resumable_uploads table (Gold Spec §5.2).
-- Lives in migrations/ but BootstrapSchema doesn't run those, so the
-- ALTER + parts table below need it created here for fresh installs.
CREATE TABLE IF NOT EXISTS resumable_uploads (
    upload_id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    media_id        UUID NOT NULL REFERENCES media_assets(id) ON DELETE CASCADE,
    uploader_id     UUID NOT NULL,
    total_bytes     BIGINT NOT NULL,
    uploaded_bytes  BIGINT NOT NULL DEFAULT 0,
    chunk_size      BIGINT NOT NULL DEFAULT 5242880,
    status          TEXT NOT NULL DEFAULT 'initiated'
                       CHECK (status IN ('initiated','uploading','completed','aborted','expired')),
    object_key      TEXT NOT NULL,
    last_chunk_at   TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- CREATE TABLE IF NOT EXISTS does not add columns to an already-created
-- table. Earlier bootstraps recorded migration 004 while this setup copy was
-- incomplete, so repair upgrades additively as well as creating correctly.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = current_schema()
           AND table_name = 'resumable_uploads' AND column_name = 'storage_key'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = current_schema()
           AND table_name = 'resumable_uploads' AND column_name = 'object_key'
    ) THEN
        ALTER TABLE resumable_uploads RENAME COLUMN storage_key TO object_key;
    END IF;
END $$;
ALTER TABLE resumable_uploads ADD COLUMN IF NOT EXISTS total_parts INT NOT NULL DEFAULT 0;
ALTER TABLE resumable_uploads ADD COLUMN IF NOT EXISTS mime_type TEXT NOT NULL DEFAULT 'application/octet-stream';
ALTER TABLE resumable_uploads ADD COLUMN IF NOT EXISTS upload_token TEXT;
CREATE INDEX IF NOT EXISTS idx_resumable_uploads_media ON resumable_uploads(media_id);
CREATE INDEX IF NOT EXISTS idx_resumable_uploads_status ON resumable_uploads(status) WHERE status IN ('initiated', 'uploading');
CREATE INDEX IF NOT EXISTS idx_resumable_uploads_expiry ON resumable_uploads(expires_at) WHERE status != 'completed';

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
-- Module 1 P0-6 follow-on fix.
-- migration 009 pinned processing_status to
-- (pending_upload, uploaded, processing, ready, failed) — but the
-- service already writes 'rejected' on magic-byte and safety-scan
-- failures (internal/service/media.go). Those writes discard their error,
-- so every rejection silently violated the constraint and left the asset
-- in its previous state. Voice uploads (P0-6) depend on rejection
-- actually persisting, so 'rejected' becomes a first-class value.
ALTER TABLE media_assets DROP CONSTRAINT IF EXISTS media_assets_processing_status_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_processing_status_check
    CHECK (processing_status IN ('pending_upload', 'uploaded', 'processing', 'ready', 'failed', 'rejected'));
-- Module 1 fixes-v2 / Codex P0-3: fresh-schema correctness.
--
-- setup.sql previously ALTERed media_subtitles without ever creating it.
-- The table lived only in migration 006, and BootstrapSchema applies
-- setup.sql BEFORE the migrations — so a fresh database failed here and
-- never reached the caption-job table. The canonical definition (kept
-- byte-identical to migration 006, which stays for upgrade paths) now
-- lives here, ahead of every ALTER that depends on it.
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

-- Module 1 fixes-v1.
--
-- Codex P0-2: media_subtitles stays the ONE canonical caption/transcript
-- content store (deviation approved in principle), but three things were
-- broken:
--   a) the service wrote source='auto', which the CHECK rejects
--      (allowed: auto_generated | manual | translated);
--   b) content_url was written empty and the transcript text was never
--      persisted anywhere, so CaptionStatus.Text could never populate;
--   c) there was no durable request state, so status was inferred and a
--      failure was forgotten on the next GET.
--
-- Fix: add a `content` column for inline transcript text (URL stays for
-- rendered .vtt files when we produce them), and a separate
-- media_caption_jobs table holding ONLY request lifecycle state — no
-- transcript content, so nothing is duplicated.
ALTER TABLE media_subtitles
    ADD COLUMN IF NOT EXISTS content TEXT,
    ADD COLUMN IF NOT EXISTS edited_by_owner BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- content_url was NOT NULL with no default; inline transcripts have no
-- URL until a .vtt is rendered.
ALTER TABLE media_subtitles ALTER COLUMN content_url SET DEFAULT '';

-- Durable caption request lifecycle (status only — content lives in
-- media_subtitles). "unavailable" is computed at read time when no real
-- backend is configured and is never persisted as a success.
CREATE TABLE IF NOT EXISTS media_caption_jobs (
    media_id     UUID PRIMARY KEY REFERENCES media_assets(id) ON DELETE CASCADE,
    language     TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','running','completed','failed')),
    attempts     INT NOT NULL DEFAULT 0,
    last_error   TEXT,
    claimed_at   TIMESTAMPTZ,
    -- fixes-v2 / Codex P1-4: fences complete/fail/release against a
    -- stale worker whose job was reclaimed.
    claim_token  UUID,
    requested_by UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_caption_jobs_claimable
    ON media_caption_jobs (created_at)
    WHERE status IN ('pending','running');

-- Codex P1-7: decorative is a first-class, persisted state — distinct
-- from "no description yet" — so screen readers can correctly SKIP the
-- image on every surface that references this canonical asset.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS alt_decorative BOOLEAN NOT NULL DEFAULT FALSE;
-- fixes-v2 / Codex P2-1: decorative and a description are mutually
-- exclusive at the database layer, not just in application code.
ALTER TABLE media_assets DROP CONSTRAINT IF EXISTS media_assets_alt_decorative_check;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_alt_decorative_check
    CHECK (NOT (alt_decorative AND COALESCE(alt_text, '') <> ''));
-- Module 1 fixes-v3 / LB-1 requirement 7 — do not lose the blob key.
--
-- The previous orphan-delete path read the object keys, deleted the
-- database rows, and only then deleted the S3/MinIO objects. If the blob
-- deletes failed (or the process died) the rows were already gone, so the
-- keys were lost and the objects leaked forever with nothing left to
-- retry from.
--
-- Object keys are now recorded here in the SAME transaction that removes
-- the media rows, so the reclaim intent is durable before any blob call
-- is attempted. A sweeper drains this table and deletes the row only
-- after the object is confirmed gone. Re-deleting an already-absent
-- object is a no-op, so retries are safe.
CREATE TABLE IF NOT EXISTS media_blob_reclaim (
    object_key  TEXT PRIMARY KEY,
    media_id    UUID NOT NULL,
    attempts    INT NOT NULL DEFAULT 0,
    last_error  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_blob_reclaim_pending ON media_blob_reclaim (created_at);
