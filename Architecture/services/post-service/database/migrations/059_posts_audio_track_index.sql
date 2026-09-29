-- Migration 059: the reels that play a sound (2026-09-29).
--
-- GET /v1/posts/by-sound/:soundId lists the posts whose audio_track_id is
-- the sound, newest first, by keyset on (created_at, id). Without an index
-- that is a scan of posts per sound page. Partial: most posts play no added
-- sound, and a soft-deleted post is never listed.
CREATE INDEX IF NOT EXISTS idx_posts_audio_track
    ON posts (audio_track_id, created_at DESC, id DESC)
    WHERE audio_track_id IS NOT NULL AND deleted_at IS NULL;

-- The two audio_tracks columns post-service now reads and writes that are
-- media-service's (its migration 004): usage_count, the counter its
-- catalogue shows, which is written together with use_count from now on,
-- and source_reel_id, the reel a sound was taken from. Added the way 058
-- adds source_media_id and status, idempotently and with the owner's types
-- and defaults, so a database post-service migrates first has them.
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS usage_count INT NOT NULL DEFAULT 0;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_reel_id UUID;
