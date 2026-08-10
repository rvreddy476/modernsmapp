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
