-- Live recordings become media assets (1 Oct 2026).
--
-- live-service-v2's LiveKit egress writes an MP4 of each finished stream to
-- the recordings bucket. post-service makes the unlisted long video from
-- live.stream.vod_ready only when the recording is a registered media_assets
-- row, and nothing registered one. POST /v1/media/internal/recordings/import
-- (internal/service/recording_import.go) now copies the object into this
-- service's own layout and creates the row; these two columns make that
-- import idempotent.
--
-- import_source names the producer ('live_recording') and import_source_ref
-- its own id for the object (the stream id). One asset per (source, ref):
-- a retried import finds the row the first one made instead of copying the
-- recording a second time. NULL on every upload, which the partial index
-- leaves out, so ordinary uploads are untouched.
--
-- The columns live on media_assets itself, not in a ledger table: the
-- reclaim policy (internal/store/postgres/reclaim_policy.go) refuses to run
-- while any *_media_id column outside media_assets is unclassified, and an
-- idempotency key is not a claim on the asset.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS import_source     TEXT,
    ADD COLUMN IF NOT EXISTS import_source_ref TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_media_assets_import_source
    ON media_assets (import_source, import_source_ref)
    WHERE import_source IS NOT NULL;
