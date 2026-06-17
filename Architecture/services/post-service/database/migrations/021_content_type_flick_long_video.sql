-- Migration: widen chk_content_type to include flick / long_video / video_embed.
--
-- Migration 003 added chk_content_type with only ('post','poll','reel','video').
-- The service later moved to the spec-v2.1 taxonomy — the post-service code now
-- reads and writes 'flick' and 'long_video' (see reel_feed.go, my_uploads.go) and
-- crosspost links insert 'video_embed' — but the CHECK constraint was never
-- updated, so publishing a flick/long video failed with SQLSTATE 23514.
--
-- Idempotent: drop-if-exists then re-add, so it is safe to re-run.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_content_type;

ALTER TABLE posts
    ADD CONSTRAINT chk_content_type
    CHECK (content_type IN (
        'post', 'poll', 'reel', 'video', 'flick', 'long_video', 'video_embed'
    ));
