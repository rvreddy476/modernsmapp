-- Migration: add is_vertical flag for reel rendering orientation
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS is_vertical BOOLEAN NOT NULL DEFAULT FALSE;

-- Backfill existing video records where height > width
UPDATE media_assets
   SET is_vertical = TRUE
 WHERE file_type = 'video'
   AND height IS NOT NULL
   AND width IS NOT NULL
   AND height > width;
