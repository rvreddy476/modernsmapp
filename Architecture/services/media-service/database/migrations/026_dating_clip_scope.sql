-- access_scope 'dating_clip': a ≤30 s voice or video answer to a Pulse
-- dating profile prompt (2 Oct 2026).
--
-- Set by dating-service through POST /internal/v1/media/dating-clips/:id/prepare
-- (internal/service/dating_clip.go). Like 'dating_photo', a dating clip is
-- never delivered through the public read routes to anyone but its uploader
-- (DatingScopeDenies); other viewers get a short-lived signed URL that
-- dating-service requests after its own audience decision.
--
-- Additive and idempotent: every existing access_scope CHECK is dropped and
-- the widened one added, exactly as 018 did. No row changes.
DO $$
DECLARE c record;
BEGIN
    FOR c IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'media_assets'::regclass AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%access_scope%'
    LOOP
        EXECUTE format('ALTER TABLE media_assets DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;

ALTER TABLE media_assets
    ADD CONSTRAINT media_assets_access_scope_check
    CHECK (access_scope IS NULL OR access_scope IN ('dating_photo', 'anonymous', 'dating_clip'));
