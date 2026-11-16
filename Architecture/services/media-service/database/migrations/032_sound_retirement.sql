-- Retired sounds (2026-11-16): a sound outlives its source video.
--
-- Until now deleting the source video (the owner's delete, or the post purge
-- after the restore window) reclaimed the sound's audio and waveform and
-- marked the row 'deleted' (8eea25e91), so every reel that used the sound
-- went silent. Decision taken: when the AUTHOR deletes the video, the sound
-- is RETIRED, not destroyed:
--
--   status = 'retired'   no new reel may attach it, it is left out of search,
--                        trending and its own sound page, and a reel that
--                        already plays it keeps playing it (the /v1/audio
--                        serve route admits a viewer of THAT reel,
--                        ?post_id=<reel>; service/audio_reads.go).
--
-- Its objects are reclaimed only when (a) the author's account is erased
-- (PurgeUser), (b) the sound is taken down (POST /v1/media/internal/sounds/
-- :audioId/takedown), or (c) no post references it any more (the retired
-- sound sweep in the blob reclaim worker).
--
-- audio_tracks.status already exists (migration 004: processing, ready,
-- rejected, deleted; no CHECK constraint, and post-service's migration 013
-- declares it the same way); 'retired' is a new value of it. The columns
-- below keep what a retired row lost with its source:
--
--   former_source_media_id  the deleted video the sound was taken from. Not
--                           a reference (the asset is gone): it is the
--                           media_id the reclaim ledger records.
--   source_uploader_id      the owner of the sound's objects
--                           (audio/<uploader>/<media>/), so an account
--                           erasure still finds the sounds of videos the user
--                           deleted before.
--   retired_at              when the source went; the sweep waits a grace
--                           period after it.
--
-- audio_tracks is ONE table shared with post-service. Only media-service
-- reads these columns; post-service reads status (a retired sound plays on
-- the posts that carry it and is refused for a new attach, which already
-- requires 'ready').
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS former_source_media_id UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS source_uploader_id UUID;
ALTER TABLE audio_tracks ADD COLUMN IF NOT EXISTS retired_at TIMESTAMPTZ;

-- Every sound with a live source records its owner now, so the column is
-- complete whichever path later retires it.
DO $$
BEGIN
    IF to_regclass('media_assets') IS NOT NULL THEN
        UPDATE audio_tracks t
           SET source_uploader_id = m.uploader_id
          FROM media_assets m
         WHERE m.id = t.source_media_id
           AND t.source_uploader_id IS NULL;
    END IF;
END $$;

-- The sweep's scan and the erasure's lookup.
CREATE INDEX IF NOT EXISTS idx_audio_tracks_retired
    ON audio_tracks (retired_at)
    WHERE status = 'retired';
CREATE INDEX IF NOT EXISTS idx_audio_tracks_source_uploader
    ON audio_tracks (source_uploader_id)
    WHERE source_uploader_id IS NOT NULL;
