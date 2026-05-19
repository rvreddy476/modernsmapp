-- 007_resumable_multipart.sql: back the resumable upload API with a real
-- S3/MinIO multipart upload. storage_upload_id is the multipart upload id
-- returned by the object store; resumable_upload_parts records each part's
-- ETag so the upload can be completed (and resumed after a dropped part).
ALTER TABLE resumable_uploads
    ADD COLUMN IF NOT EXISTS storage_upload_id TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS resumable_upload_parts (
    upload_id   UUID        NOT NULL REFERENCES resumable_uploads(upload_id) ON DELETE CASCADE,
    part_number INT         NOT NULL,
    etag        TEXT        NOT NULL,
    size_bytes  BIGINT      NOT NULL,
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (upload_id, part_number)
);
