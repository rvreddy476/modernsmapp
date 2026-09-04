-- Migration 007 ("Don't recommend this account", 2026-09-04): per-viewer
-- author-level feed feedback — YouTube's "Don't recommend this channel".
--
-- A separate table rather than a nullable post_id on feed_feedback: that
-- table's primary key is (user_id, post_id) and every reader of it
-- (ExcludedPostIDs, AuthorFeedbackNet, the purge) assumes one row per
-- answered post. A NULL post_id would break the key (NULLs never conflict,
-- so "latest answer wins" would silently stack rows), need a partial unique
-- index to patch it, and put `post_id IS NULL` guards in every query. An
-- author mute is also its own thing to the client — listed and undone on
-- its own (GET /v1/feed/feedback/authors) — so it gets its own key.
--
-- One row per (viewer, author), holding the viewer's LATEST answer, same
-- latest-wins shape as feed_feedback: "not_interested" is the mute,
-- "interested" clears it (the row is kept with the flipped signal so
-- updated_at records the undo). Read on two paths:
--
--   * not_interested drops EVERY post by the author at the hydration tail
--     of every surface (internal/service/feedback.go, applyFeedbackFilter),
--     fail-closed like the block filter;
--   * the ranker's authorPenalty term treats an active mute as the maximum
--     penalty (internal/ranking/scorer.go, NetWithMute), mirrored into
--     feed:author_feedback:{viewer} alongside the post-level answers.
--
-- Rows go with the viewer AND with the author on user.purge_requested
-- (internal/store/postgres/purge.go).
CREATE TABLE IF NOT EXISTS feed_author_feedback (
    user_id    UUID NOT NULL,
    author_id  UUID NOT NULL,
    signal     TEXT NOT NULL CHECK (signal IN ('interested', 'not_interested')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, author_id)
);
CREATE INDEX IF NOT EXISTS idx_feed_author_feedback_author ON feed_author_feedback (author_id);
