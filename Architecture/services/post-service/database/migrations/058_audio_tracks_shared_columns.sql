-- Migration 058: the audio_tracks columns post-service reads (2026-09-29).
--
-- media-service owns the sound catalogue and creates its rows; post-service
-- only links a post to a sound (posts.audio_track_id) and, to decide whether
-- the author may use it, reads the sound's source asset and its status.
-- Both columns are media-service's (its migration 004). On a database where
-- post-service migrates first they would be missing and the attach would
-- fail, so they are added here the same way 013 and media-service 004 add
-- each other's columns: idempotently, with the same types and defaults.
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_media_id UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'processing';

-- Where post-service created the table first (013), media_id is NOT NULL,
-- and media-service, which does not write that column, could not insert a
-- sound at all. The owner's rows name their asset in source_media_id.
ALTER TABLE audio_tracks ALTER COLUMN media_id DROP NOT NULL;
