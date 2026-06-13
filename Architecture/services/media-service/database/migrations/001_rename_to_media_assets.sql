-- This historical rename only applies when an old `media` table is present.
-- Fresh databases get `media_assets` directly from setup.sql, so this whole
-- migration becomes a no-op.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_tables WHERE schemaname = 'public' AND tablename = 'media'
    ) THEN
        -- 1. Rename table
        ALTER TABLE media RENAME TO media_assets;

        -- 2. Rename columns
        ALTER TABLE media_assets RENAME COLUMN owner_user_id TO uploader_id;
        ALTER TABLE media_assets RENAME COLUMN mime TO mime_type;
        ALTER TABLE media_assets RENAME COLUMN size_bytes TO file_size_bytes;
        ALTER TABLE media_assets RENAME COLUMN bucket TO storage_bucket;
        ALTER TABLE media_assets RENAME COLUMN object_key TO storage_key;
        ALTER TABLE media_assets RENAME COLUMN status TO processing_status;
        ALTER TABLE media_assets RENAME COLUMN duration_ms TO duration_seconds;

        -- 3. Convert ms -> seconds for existing data
        UPDATE media_assets SET duration_seconds = duration_seconds / 1000
            WHERE duration_seconds IS NOT NULL;

        -- 4. Split kind -> file_type + media_subtype
        ALTER TABLE media_assets ADD COLUMN file_type TEXT NOT NULL DEFAULT 'image';
        ALTER TABLE media_assets ADD COLUMN media_subtype TEXT NOT NULL DEFAULT 'general';
        UPDATE media_assets SET
            file_type = CASE WHEN kind = 'video' THEN 'video' ELSE 'image' END,
            media_subtype = CASE
                WHEN kind = 'avatar' THEN 'avatar'
                WHEN kind = 'cover' THEN 'cover'
                WHEN kind = 'gif' THEN 'gif'
                ELSE 'general'
            END;
        ALTER TABLE media_assets DROP COLUMN kind;
        ALTER TABLE media_assets ALTER COLUMN file_type DROP DEFAULT;
        ALTER TABLE media_assets ALTER COLUMN media_subtype DROP DEFAULT;

        -- 5. Rename init -> pending_upload
        UPDATE media_assets SET processing_status = 'pending_upload'
            WHERE processing_status = 'init';

        -- 6. Add spec columns (nullable, unpopulated for now)
        ALTER TABLE media_assets ADD COLUMN original_url VARCHAR(500);
        ALTER TABLE media_assets ADD COLUMN cdn_url VARCHAR(500);
        ALTER TABLE media_assets ADD COLUMN thumbnail_url VARCHAR(500);

        -- 7. Rename index
        ALTER INDEX IF EXISTS idx_media_owner_user_id RENAME TO idx_media_assets_uploader_id;

        -- 8. Rename media_variants FK column
        ALTER TABLE media_variants RENAME COLUMN media_id TO media_asset_id;
        ALTER TABLE media_variants DROP CONSTRAINT media_variants_pkey;
        ALTER TABLE media_variants ADD PRIMARY KEY (media_asset_id, variant);
    END IF;
END $$;

-- 9. Create transcoding_jobs table (idempotent, runs in either path)
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
