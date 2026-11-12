-- Migration 009 (2026-11-12): Echo notes (docs/designs/echo-contract.md).
--
-- echo_notes: one row per note feed-service saw on PostCreated. It answers
-- three questions the Scylla timelines cannot:
--   * is this note top-level? PostRestored and post.visibility_changed do not
--     carry reply_to_id, and a reply must never be fanned out again;
--   * what has a pulled (large) author posted? read at Following time for
--     authors whose notes are not pushed;
--   * which public top-level notes are recent? For You's fast-rising source
--     (post-service's recent-public list leaves notes out).
-- deleted follows PostDeleted / PostRestored; the row is removed on a user
-- purge (store/postgres/purge.go).
CREATE TABLE IF NOT EXISTS echo_notes (
    post_id     UUID PRIMARY KEY,
    author_id   UUID        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    top_level   BOOLEAN     NOT NULL,
    visibility  TEXT        NOT NULL DEFAULT 'public',
    deleted     BOOLEAN     NOT NULL DEFAULT FALSE,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_echo_notes_author_live
    ON echo_notes (author_id, created_at DESC) WHERE top_level AND NOT deleted;
CREATE INDEX IF NOT EXISTS idx_echo_notes_recent_live
    ON echo_notes (created_at DESC) WHERE top_level AND NOT deleted;

-- echo_pull_authors: authors whose notes are pulled at read time because
-- their follower count passed ECHO_FANOUT_MAX_FOLLOWERS when a note was
-- fanned out. (Celeb-flagged authors in celeb_authors are pulled too.)
CREATE TABLE IF NOT EXISTS echo_pull_authors (
    author_id      UUID PRIMARY KEY,
    follower_count INTEGER     NOT NULL,
    flagged_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
