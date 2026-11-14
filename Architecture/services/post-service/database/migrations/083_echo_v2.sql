-- Migration 083: Echo v2 (docs/designs/echo-v2-contract.md, sections 1-3).
--
-- 1. Views. note_views holds one row per (note, viewer): counted_at is the
--    moment the viewer last counted. A view counts again only once that is
--    24 hours old (a rolling 24 hours, not a calendar day), and the counter
--    lives on the note row (posts.note_view_count). The author and
--    signed-out viewers never write a row (service/notes_v2.go).
--
-- 2. Scheduled notes reuse posts.publish_at (migration 042): publish_at IS
--    NOT NULL means scheduled. The note publisher (internal/noteschedule)
--    claims due notes with FOR UPDATE SKIP LOCKED; the ordinary post
--    schedule worker skips notes. note_schedules is the publisher's state:
--    the lease (next_attempt_at), the publish_error the author sees, the
--    first failure (the 24-hour retry window starts there) and failed_at
--    once the window is spent (status 'failed'). The row goes when the
--    note publishes or the author reschedules.
--
-- 3. Indexes for quotes, trends, hashtag pages, search, the due scan and
--    the bookmark count.
--
-- Idempotent throughout.

ALTER TABLE posts ADD COLUMN IF NOT EXISTS note_view_count BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS note_views (
    note_id    UUID        NOT NULL,
    viewer_id  UUID        NOT NULL,
    counted_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (note_id, viewer_id)
);
CREATE INDEX IF NOT EXISTS idx_note_views_counted_at ON note_views (counted_at);

CREATE TABLE IF NOT EXISTS note_schedules (
    note_id         UUID PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    publish_error   TEXT,
    attempts        INT NOT NULL DEFAULT 0,
    first_failed_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    failed_at       TIMESTAMPTZ
);

-- A note's quotes, newest first (GET /v1/notes/:id/quotes, quote_count).
CREATE INDEX IF NOT EXISTS idx_posts_note_quotes
    ON posts (quote_of_id, created_at DESC)
    WHERE content_type = 'note' AND quote_of_id IS NOT NULL AND deleted_at IS NULL;

-- Recent top-level notes (trends).
CREATE INDEX IF NOT EXISTS idx_posts_note_recent_top
    ON posts (created_at DESC)
    WHERE content_type = 'note' AND reply_to_id IS NULL AND deleted_at IS NULL;

-- Note hashtag pages.
CREATE INDEX IF NOT EXISTS idx_posts_note_hashtags
    ON posts USING GIN (hashtags)
    WHERE content_type = 'note' AND deleted_at IS NULL;

-- Note search (Postgres full text, 'simple' configuration).
CREATE INDEX IF NOT EXISTS idx_posts_note_fts
    ON posts USING GIN (to_tsvector('simple'::regconfig, text))
    WHERE content_type = 'note' AND deleted_at IS NULL;

-- The note publisher's due scan and the author's Scheduled list.
CREATE INDEX IF NOT EXISTS idx_posts_note_scheduled
    ON posts (author_id, publish_at, id)
    WHERE content_type = 'note' AND publish_at IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_note_due
    ON posts (publish_at)
    WHERE content_type = 'note' AND publish_at IS NOT NULL AND deleted_at IS NULL;

-- bookmark_count: saves of one post.
CREATE INDEX IF NOT EXISTS idx_saved_items_post_target
    ON saved_items (target_id)
    WHERE target_type = 'post';
