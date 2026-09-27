-- Emoji reactions on comments (2026-09-27).
--
-- One row per (comment, viewer): a viewer holds at most ONE reaction on a
-- comment, and choosing a new emoji replaces the old one (upsert on the
-- primary key). The legacy POST /v1/comments/:id/like route now toggles the
-- viewer's row between '❤️' and none on this same table, so old and new
-- clients count the same thing.
--
-- comments.like_count is NO LONGER maintained for reactions: every read
-- surface computes like_count = reaction_count from this table at read time
-- (see store/postgres/comment_reactions.go). The column stays for the
-- dislike toggle's legacy mutual-exclusion write and for old rows; nothing
-- reads it for the wire any more.
CREATE TABLE IF NOT EXISTS comment_reactions (
    comment_id  UUID        NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id     UUID        NOT NULL,
    emoji       TEXT        NOT NULL CHECK (char_length(emoji) BETWEEN 1 AND 16),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (comment_id, user_id)
);

-- The per-page aggregate: SELECT comment_id, emoji, COUNT(*) ... GROUP BY 1,2.
CREATE INDEX IF NOT EXISTS idx_comment_reactions_comment_emoji
    ON comment_reactions (comment_id, emoji);
