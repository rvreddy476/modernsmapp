-- Media generation and upload-time identity (Copyright Match plan P-14,
-- 2026-09-29). Expand-only: every column has a default or is nullable, so
-- every existing writer stays valid.
--
--   media_generation     bumped by every re-queue (operator reprocess or the
--                        stall sweeper's automatic retry) BEFORE the new
--                        request is relayed, so it commits before any object
--                        under the asset's keys is overwritten. Carried in
--                        the transcode request and completion payloads.
--   ready_generation     the generation whose transcode last completed
--                        'ready'. Set by completeTranscodeTx only when the
--                        completion's generation equals media_generation; an
--                        older generation's completion sets nothing.
--   upload_confirmed_at  set ONCE, when confirm verified the bytes (the
--                        single PUT and the resumable path both end in
--                        ConfirmUpload). Never overwritten. created_at is set
--                        at init and can be up to 24 h early through a
--                        resumable session, so it is NOT an upload time.
--   upload_time_source   'confirmed' for rows confirmed after this
--                        migration; 'legacy_created_at' for the backfill.
--                        Upload precedence between two assets treats a
--                        legacy side within 24 h as ambiguous.
--   original_etag        the object store's ETag of the original at confirm.
--                        Audit only in phase 1; the fingerprint worker records
--                        the ETags it actually read (input_etags).
--
-- None of these columns references media_assets, so reclaim_policy.go
-- (which classifies media-referencing columns in OTHER tables) has nothing
-- to classify.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS media_generation    BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS ready_generation    BIGINT,
    ADD COLUMN IF NOT EXISTS upload_confirmed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS upload_time_source  TEXT,
    ADD COLUMN IF NOT EXISTS original_etag       TEXT;

-- NOT VALID: existing rows are backfilled just below, but the constraint
-- must never block boot on a row some other path wrote in between.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'media_assets_upload_time_source_check'
    ) THEN
        ALTER TABLE media_assets
            ADD CONSTRAINT media_assets_upload_time_source_check
            CHECK (upload_time_source IN ('confirmed', 'legacy_created_at')) NOT VALID;
    END IF;
END $$;

-- Backfill. Every asset that has left pending_upload had its bytes verified
-- at some point; created_at is the only time we have for it, and it is
-- labelled as such so precedence logic can treat it as ambiguous.
UPDATE media_assets
   SET upload_confirmed_at = COALESCE(upload_confirmed_at, created_at),
       upload_time_source  = COALESCE(upload_time_source, 'legacy_created_at')
 WHERE processing_status <> 'pending_upload'
   AND (upload_confirmed_at IS NULL OR upload_time_source IS NULL);

UPDATE media_assets
   SET ready_generation = media_generation
 WHERE processing_status = 'ready'
   AND ready_generation IS NULL;
