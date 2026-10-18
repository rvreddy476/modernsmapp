-- Migration 066: post screens parity (2026-10-18).
--
-- The photo-post details screen gained four things the posts row could not
-- hold, and one audience it could not name:
--
--   visibility 'friends'  mutual follows only — the author follows the viewer
--                         AND the viewer follows the author. Enforced on every
--                         read path (post.go viewerMayViewPost, the batch,
--                         the profile grid, the media gate, threads). "Only
--                         you" is NOT a new value: it is 'private' with an
--                         empty share list (migration 053).
--   link_url              one https link shown under the caption; NULL when
--                         the post carries none. https only and at most 500
--                         characters; the service refuses javascript:/data:
--                         and every other scheme before this CHECK is reached.
--   ai_generated          the author's "made with AI" label. Distinct from
--                         altered_content (the realistic-alteration
--                         disclosure), which keeps its meaning.
--   location_place_id     the provider's place id for location_name; NULL for
--                         a free-text location. name/lat/lng are unchanged.
--
-- is_branded already exists (migration 007) and is only now read and written.
--
-- post_drafts.post_type: the service has accepted 'reel' and 'video' drafts
-- since Codex P1-5 (validDraftPostTypes), but the CHECK from 026 still said
-- post/poll/article only, so every such save was a 500 from the database.
--
-- Every statement is re-runnable.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_visibility_check;
ALTER TABLE posts ADD CONSTRAINT posts_visibility_check
    CHECK (visibility IN ('public', 'followers', 'friends', 'private', 'unlisted', 'trusted', 'close_friends', 'staged'));

ALTER TABLE posts ADD COLUMN IF NOT EXISTS link_url          TEXT    NULL;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS ai_generated      BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS location_place_id TEXT    NULL;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'posts_link_url_check') THEN
        ALTER TABLE posts ADD CONSTRAINT posts_link_url_check
            CHECK (link_url IS NULL OR (char_length(link_url) <= 500 AND link_url ~* '^https://'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'posts_location_place_id_check') THEN
        ALTER TABLE posts ADD CONSTRAINT posts_location_place_id_check
            CHECK (location_place_id IS NULL OR char_length(location_place_id) BETWEEN 1 AND 255);
    END IF;
END $$;

ALTER TABLE post_drafts DROP CONSTRAINT IF EXISTS post_drafts_post_type_check;
ALTER TABLE post_drafts ADD CONSTRAINT post_drafts_post_type_check
    CHECK (post_type IN ('post', 'poll', 'article', 'reel', 'video'));
