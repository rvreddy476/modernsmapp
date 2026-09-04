-- Migration 006 (post "more" sheet, 2026-09-04): per-viewer feed feedback.
--
-- One row per (viewer, post), holding the viewer's LATEST answer —
-- "interested" or "not_interested" — so a second tap replaces the first
-- instead of stacking. Read on two paths:
--
--   * not_interested is a hard per-post exclusion applied at the hydration
--     tail of every surface (internal/service/feedback.go,
--     applyFeedbackFilter), alongside feed_hides, which until now had no
--     reader at all.
--   * author_id is the ranking hook: the viewer's net feedback per author is
--     mirrored into Redis (feed:author_feedback:{viewer}) and read by the
--     ranker as the `authorPenalty` term the scoring formula already named.
--
-- author_id / category are copied from the post at feedback time (one
-- post-service batch call) so the ranking read needs no join. Rows go with
-- the viewer on user.purge_requested (internal/store/postgres/purge.go).
CREATE TABLE IF NOT EXISTS feed_feedback (
    user_id    UUID NOT NULL,
    post_id    UUID NOT NULL,
    author_id  UUID NOT NULL,
    category   TEXT NOT NULL DEFAULT '',
    signal     TEXT NOT NULL CHECK (signal IN ('interested', 'not_interested')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, post_id)
);
CREATE INDEX IF NOT EXISTS idx_feed_feedback_user_author ON feed_feedback (user_id, author_id);
