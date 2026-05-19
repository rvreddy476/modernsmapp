-- 018_review_status_pending.sql: allow the 'pending' review state.
-- Lets the moderation pipeline hold content awaiting a verdict
-- (e.g. an async content-safety classifier) without a schema change.
ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_review_status_check;

ALTER TABLE posts
    ADD CONSTRAINT posts_review_status_check
    CHECK (review_status IN ('approved', 'flagged', 'rejected', 'pending'));
