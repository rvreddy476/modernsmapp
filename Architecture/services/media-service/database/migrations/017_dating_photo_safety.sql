-- Dating plan lane D6 — dating photo safety (2026-09-15).
--
-- moderation_labels / moderation_scanner: every label the image scanner
-- returned at upload (not only the blocked ones) and the scanner that
-- produced them. A NULL scanner means the image was never scanned (scanner
-- disabled); dating-service treats that as "needs review", never as clean.
--
-- access_scope: 'dating_photo' once dating-service has prepared the asset
-- (metadata stripped, orientation applied, blurred variant generated). A
-- dating-scoped asset is never delivered through the public read routes to
-- anyone but its uploader; other viewers get a short-lived signed URL that
-- dating-service requests after its own audience decision.
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS moderation_labels    JSONB,
    ADD COLUMN IF NOT EXISTS moderation_scanner   TEXT,
    ADD COLUMN IF NOT EXISTS access_scope         TEXT
        CHECK (access_scope IS NULL OR access_scope IN ('dating_photo')),
    ADD COLUMN IF NOT EXISTS metadata_stripped_at TIMESTAMPTZ;
