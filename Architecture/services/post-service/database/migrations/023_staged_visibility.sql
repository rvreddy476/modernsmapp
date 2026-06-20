-- Phase 4b: add a 'staged' visibility tier for the reviewer test-audience flow.
-- Staged video is served to the organic audience (reel_feed includes it) so it
-- accrues real engagement; the reviewer-service promotion worker flips it to
-- 'public' once approved and engagement is healthy.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_visibility_check;
ALTER TABLE posts ADD CONSTRAINT posts_visibility_check
    CHECK (visibility IN ('public', 'followers', 'private', 'unlisted', 'trusted', 'close_friends', 'staged'));
