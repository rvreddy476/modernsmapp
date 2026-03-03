-- 005_review_status.sql: Add review_status column for spam flagging
ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS review_status TEXT NOT NULL DEFAULT 'approved'
        CHECK (review_status IN ('approved', 'flagged', 'rejected'));

CREATE INDEX IF NOT EXISTS idx_posts_review_status
    ON posts(review_status)
    WHERE review_status != 'approved';
