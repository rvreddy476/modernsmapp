-- Migration 008 (audit L11, 2026-10-30): author_id indexes for the purge.
--
-- user.purge_requested runs
--   DELETE FROM user_interactions WHERE viewer_id = $1 OR author_id = $1
--   DELETE FROM feed_feedback     WHERE user_id   = $1 OR author_id = $1
-- (internal/store/postgres/purge.go). Neither table had an index that
-- starts with author_id (user_interactions: viewer_id only; feed_feedback:
-- (user_id, post_id) and (user_id, author_id)), so the author half of the
-- OR was a sequential scan of the whole table on every purge. With these,
-- Postgres answers each OR with a BitmapOr of two index scans.
--
-- Plain CREATE INDEX: the migration runner applies each file in a
-- transaction, where CONCURRENTLY is not allowed. Both tables are small
-- (per-viewer feedback and interaction rows), so the brief write lock is
-- acceptable.
CREATE INDEX IF NOT EXISTS idx_interactions_author ON user_interactions (author_id);
CREATE INDEX IF NOT EXISTS idx_feed_feedback_author ON feed_feedback (author_id);
