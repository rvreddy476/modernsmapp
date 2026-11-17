-- Migration 086: a series or playlist never blocks a channel's deletion (lv-7,
-- long-video readiness audit, 2026-11-17).
--
-- video_series.channel_id and playlists.channel_id reference channels(id)
-- with no ON DELETE (migration 012), and until this release post-service
-- stored any channel_id a client sent. user-service's account purge deletes
-- the user's channel, so one row by ANYONE pointing at that channel made the
-- purge fail on the foreign key (SQLSTATE 23503) on every retry: a stranger
-- could block another person's account erasure for good.
--
-- This migration:
--   1. clears every existing reference to a channel the row's creator does
--      not own (the rows the old hole could have written; a creator's own
--      channel is kept), and
--   2. re-creates both foreign keys as ON DELETE SET NULL, so a channel's
--      deletion clears any reference that remains or appears later.
-- The code half (service/channel_ref_authz.go) refuses a stranger's channel
-- on create and edit from this release on.
--
-- Safe with the previous image running, and after a rollback to it:
--   * the constraint is the same rule (channel_id is NULL or a channels.id);
--     only the delete action changes. The previous image writes nothing the
--     new constraint refuses, and reads the column exactly as before;
--   * SET NULL on a nullable column the previous image already handles as
--     "no channel" (the field is optional on every route);
--   * the previous image can still store a stranger's channel (its handler
--     has no check), but with SET NULL that reference no longer blocks a
--     purge; it is cleared when the channel goes.
-- Step 1 changes data a rollback does not restore: only references that
-- named someone else's channel, which no reader relies on (the series and
-- playlist pages read the creator, not channel_id, for ownership).
--
-- Locking: the DROP / ADD CONSTRAINT pair takes ACCESS EXCLUSIVE on
-- video_series / playlists and SHARE ROW EXCLUSIVE on channels until this
-- file's transaction commits; the ADD validates by one scan of each (small)
-- table. lock_timeout caps the wait: after 5 s the migration fails, the pod
-- does not start and the next start tries again.
--
-- Idempotent: a foreign key that is already ON DELETE SET NULL is left alone.

SET LOCAL lock_timeout = '5s';

-- 1. References to a channel the row's creator does not own.
UPDATE video_series vs
   SET channel_id = NULL, updated_at = NOW()
 WHERE vs.channel_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM channels c WHERE c.id = vs.channel_id AND c.user_id = vs.creator_id);

UPDATE playlists p
   SET channel_id = NULL, updated_at = NOW()
 WHERE p.channel_id IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM channels c WHERE c.id = p.channel_id AND c.user_id = p.creator_id);

-- 2. Both foreign keys become ON DELETE SET NULL. The existing constraint is
-- found by shape, not name (012 declared it inline), and dropped only when
-- its delete action is not already SET NULL ('n').
DO $$
DECLARE
    t   TEXT;
    con RECORD;
BEGIN
    FOREACH t IN ARRAY ARRAY['video_series', 'playlists'] LOOP
        IF EXISTS (
            SELECT 1 FROM pg_constraint
             WHERE conrelid = t::regclass AND confrelid = 'channels'::regclass
               AND contype = 'f' AND confdeltype = 'n'
        ) THEN
            CONTINUE;
        END IF;
        FOR con IN
            SELECT conname FROM pg_constraint
             WHERE conrelid = t::regclass AND confrelid = 'channels'::regclass AND contype = 'f'
        LOOP
            EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', t, con.conname);
        END LOOP;
        EXECUTE format(
            -- compat: ok same FK rule re-created with ON DELETE SET NULL (086); the previous image writes nothing it refuses
            'ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE SET NULL',
            t, t || '_channel_id_fkey');
    END LOOP;
END $$;

-- SET NULL looks rows up by channel_id on every channel delete.
CREATE INDEX IF NOT EXISTS idx_video_series_channel_id ON video_series (channel_id) WHERE channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_playlists_channel_id ON playlists (channel_id) WHERE channel_id IS NOT NULL;
