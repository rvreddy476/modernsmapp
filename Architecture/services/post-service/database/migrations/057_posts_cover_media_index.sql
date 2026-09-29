-- Migration 057: index for the cover half of the by-media lookups (2026-09-29).
--
-- PostIDsByMediaID / PostIDsByMediaIDs now also answer "which posts name
-- this asset as their COVER", so a cover inherits its post's audience at
-- the media byte gate (channel RSS feeds: artwork must load signed out).
-- That lookup runs on every media access decision; without an index it is
-- a scan of posts per asset. Partial: most posts have no cover, and a
-- soft-deleted post never answers.
CREATE INDEX IF NOT EXISTS idx_posts_cover_media
    ON posts (cover_media_id)
    WHERE cover_media_id IS NOT NULL AND deleted_at IS NULL;
