-- Original sounds on reels (2026-09-29): one sound per source video.
--
-- A sound is the audio of a source video, extracted once and reused by every
-- reel that plays it. Until now nothing stopped two rows for one source: the
-- extract route looked for an existing row and then inserted, and the index
-- on source_media_id (migration 004) is not unique. This migration makes the
-- database the arbiter, so the ensure path can insert with ON CONFLICT DO
-- NOTHING and re-read.
--
-- audio_tracks is ONE table shared with post-service (see the note in
-- migration 004). The three columns below are post-service's, declared
-- exactly as its migration 013 declares them; they are added here because
-- media-service now reads use_count and writes creator_user_id and
-- is_public, and on a database where media-service migrates first they
-- would be missing.
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS creator_user_id UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS is_public BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS use_count INT NOT NULL DEFAULT 0;

-- De-duplicate before the unique index can be built: keep the OLDEST row of
-- each source, point everything that referenced a younger one at it, carry
-- the younger rows' uses over, then delete them.
DO $$
DECLARE
    ref RECORD;
BEGIN
    -- No sound may be inserted between the de-duplication and the index.
    LOCK TABLE audio_tracks IN SHARE ROW EXCLUSIVE MODE;

    CREATE TEMP TABLE audio_track_duplicates ON COMMIT DROP AS
    SELECT id, keep_id, uses
      FROM (
            SELECT id,
                   GREATEST(COALESCE(usage_count, 0), COALESCE(use_count, 0)) AS uses,
                   FIRST_VALUE(id) OVER (
                       PARTITION BY source_media_id
                       ORDER BY created_at ASC, id ASC
                   ) AS keep_id
              FROM audio_tracks
             WHERE source_media_id IS NOT NULL
           ) ranked
     WHERE id <> keep_id;

    IF NOT EXISTS (SELECT 1 FROM audio_track_duplicates) THEN
        RETURN;
    END IF;

    -- posts.audio_track_id is post-service's column (its migration 013). It
    -- is re-pointed when it exists, whether or not it carries a constraint.
    IF EXISTS (
        SELECT 1 FROM pg_attribute
         WHERE attrelid = to_regclass('posts')
           AND attname = 'audio_track_id'
           AND NOT attisdropped
    ) THEN
        EXECUTE 'UPDATE posts p SET audio_track_id = d.keep_id
                   FROM audio_track_duplicates d
                  WHERE p.audio_track_id = d.id';
    END IF;

    -- Any other single-column foreign key to audio_tracks(id) would refuse
    -- the delete below and with it the service's boot.
    FOR ref IN
        SELECT con.conrelid::regclass AS child, att.attname AS col
          FROM pg_constraint con
          JOIN pg_attribute att
            ON att.attrelid = con.conrelid AND att.attnum = con.conkey[1]
         WHERE con.contype = 'f'
           AND con.confrelid = 'audio_tracks'::regclass
           AND array_length(con.conkey, 1) = 1
           AND con.conrelid <> 'audio_tracks'::regclass
    LOOP
        EXECUTE format(
            'UPDATE %s c SET %I = d.keep_id FROM audio_track_duplicates d WHERE c.%I = d.id',
            ref.child, ref.col, ref.col);
    END LOOP;

    UPDATE audio_tracks a
       SET usage_count = GREATEST(COALESCE(a.usage_count, 0), COALESCE(a.use_count, 0)) + moved.uses,
           use_count   = GREATEST(COALESCE(a.usage_count, 0), COALESCE(a.use_count, 0)) + moved.uses,
           updated_at  = NOW()
      FROM (SELECT keep_id, SUM(uses)::INT AS uses FROM audio_track_duplicates GROUP BY keep_id) moved
     WHERE a.id = moved.keep_id;

    DELETE FROM audio_tracks a
     USING audio_track_duplicates d
     WHERE a.id = d.id;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS uq_audio_tracks_source_media
    ON audio_tracks (source_media_id)
    WHERE source_media_id IS NOT NULL;
