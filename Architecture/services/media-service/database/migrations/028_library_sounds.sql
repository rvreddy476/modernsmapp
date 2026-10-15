-- Library sounds on posts (2026-10-15).
--
-- The curated library (audio_library, migration 006) could be browsed but
-- not used: a post links a sound by an audio_tracks id (post-service
-- posts.audio_track_id), and a library row is not one. A library sound is
-- now MATERIALISED as an audio_tracks row on its first use
-- (POST /v1/audio-library/:id/use), and every later use answers that row.
--
--   library_id    the audio_library row the sound is. Non-NULL marks a
--                 platform sound: no source video, no creator, public, heard
--                 by everyone. NULL on every extracted sound.
--
-- One row per library sound, decided by the database: the partial unique
-- index below is the arbiter of ON CONFLICT DO NOTHING, so concurrent first
-- uses leave one row and every caller re-reads it.
--
-- audio_tracks is ONE table shared with post-service; post-service's
-- migration 064 declares the same column the same way, whichever service
-- migrates first.
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS library_id UUID;

CREATE UNIQUE INDEX IF NOT EXISTS uq_audio_tracks_library
    ON audio_tracks (library_id)
    WHERE library_id IS NOT NULL;

-- The library row is the sound's only authority. Deleting it outright
-- clears the link: the track then names neither a source video nor a
-- library row, and every read refuses it (fail closed). Withdrawing a
-- library sound in the ordinary way is is_active = FALSE (no new uses) and,
-- to silence the reels already playing it, status = 'deleted' on the track.
DO $$
BEGIN
    IF to_regclass('audio_library') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'audio_tracks_library_id_fkey'
           AND conrelid = 'audio_tracks'::regclass
    ) THEN
        ALTER TABLE audio_tracks
            ADD CONSTRAINT audio_tracks_library_id_fkey
            FOREIGN KEY (library_id) REFERENCES audio_library(id) ON DELETE SET NULL;
    END IF;
END $$;
