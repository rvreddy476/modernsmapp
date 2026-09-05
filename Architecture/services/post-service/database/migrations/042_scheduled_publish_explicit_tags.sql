-- Migration 042: scheduled publish + explicit hashtags/mentions (2026-09-05).
--
-- Founder: the reel studio submits, returns to the profile, uploads in the
-- background and then publishes — or SCHEDULES. Hashtags and mentions are
-- separate form fields, not mixed into the caption.
--
-- posts.publish_at was added by migration 010 and never wired. Its contract
-- from now on:
--
--   publish_at IS NOT NULL  ⇔  the post is SCHEDULED: stored, author-only,
--                              absent from every feed, search and hashtag
--                              surface, and no PostCreated has been emitted.
--   publish_at IS NULL      ⇔  the post is live (or was never scheduled).
--
-- The schedule worker (internal/postschedule) publishes a due post by
-- clearing publish_at, stamping published_at and moving created_at to the
-- publish moment so it sorts as new, then emitting the same PostCreated a
-- fresh post emits. "Publish now" (PATCH /schedule {"publish_at": null})
-- takes the same path.
--
-- mention_usernames carries the explicit + caption-parsed @mentions as
-- usernames, which is what the client sends and what post_mentions stores.
-- The older posts.mentions UUID[] column was never populated by any code
-- path and is left alone.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS publish_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS published_at TIMESTAMPTZ;
ALTER TABLE posts ADD COLUMN IF NOT EXISTS mention_usernames TEXT[] NOT NULL DEFAULT '{}';

-- Due-scan for the worker (exists from 010 on most databases).
CREATE INDEX IF NOT EXISTS idx_posts_scheduled ON posts(publish_at) WHERE publish_at IS NOT NULL;

-- The author's "Scheduled" list, newest publish_at first.
CREATE INDEX IF NOT EXISTS idx_posts_author_scheduled
    ON posts(author_id, publish_at DESC, id DESC)
    WHERE publish_at IS NOT NULL AND deleted_at IS NULL;
