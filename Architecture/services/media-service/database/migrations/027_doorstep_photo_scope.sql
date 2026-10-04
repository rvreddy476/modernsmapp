-- Visit/evidence photos are delivered to other participants only through
-- doorstep-service after its booking ownership check, never a public post.
DO $$
DECLARE c record;
BEGIN
    FOR c IN SELECT conname FROM pg_constraint
        WHERE conrelid='media_assets'::regclass AND contype='c'
        AND pg_get_constraintdef(oid) LIKE '%access_scope%'
    LOOP
        EXECUTE format('ALTER TABLE media_assets DROP CONSTRAINT %I', c.conname);
    END LOOP;
END $$;
ALTER TABLE media_assets ADD CONSTRAINT media_assets_access_scope_check
    CHECK(access_scope IS NULL OR access_scope IN ('dating_photo','anonymous','dating_clip','doorstep_photo'));
