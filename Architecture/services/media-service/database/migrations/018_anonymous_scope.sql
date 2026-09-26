-- access_scope 'anonymous': an asset attached to an anonymous group post.
--
-- Its record (GET /v1/media/:id — uploader_id, user-keyed storage keys) is its
-- uploader's alone, and its bytes are streamed by media-service instead of
-- redirecting to a signed object URL, because every object key names the
-- uploader (user/<id>/<media>/...) and a redirect would hand that name to
-- whoever the post is hiding it from. Set by group-service through the
-- internal anonymize route before the post row is written.
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
    CHECK (access_scope IS NULL OR access_scope IN ('dating_photo', 'anonymous'));

-- Assets already attached to anonymous posts. group_posts is group-service's
-- table; it shares this database in every deployment today, but the
-- backfill is guarded so a split database does not fail the boot.
DO $$
BEGIN
    IF to_regclass('public.group_posts') IS NOT NULL THEN
        UPDATE media_assets m
           SET access_scope = 'anonymous', updated_at = NOW()
         WHERE m.access_scope IS NULL
           AND m.id::text IN (
               SELECT jsonb_array_elements_text(p.attachments)
                 FROM group_posts p
                WHERE p.is_anonymous = TRUE
                  AND p.attachments IS NOT NULL
                  AND jsonb_typeof(p.attachments) = 'array');
    END IF;
END $$;
