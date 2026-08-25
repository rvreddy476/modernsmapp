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
