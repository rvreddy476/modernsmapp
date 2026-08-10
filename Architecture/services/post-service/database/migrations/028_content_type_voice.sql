-- Module 1 P0-6: 'voice' content type for voice-only posts (audio media
-- plus optional text). Mixed voice carousels are deferred, so this is a
-- distinct content_type rather than a media-kind variation of 'post'.
-- Idempotent: drop-if-exists then re-add, matching migration 021.
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_content_type;

ALTER TABLE posts
    ADD CONSTRAINT chk_content_type
    CHECK (content_type IN (
        'post', 'poll', 'reel', 'video', 'flick', 'long_video', 'video_embed', 'voice'
    ));
