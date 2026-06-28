-- Reviewer workflow: a super-admin can request edits, putting the post in
-- 'needs_changes' (hidden, like flagged) until the creator edits & resubmits.

ALTER TABLE posts DROP CONSTRAINT IF EXISTS posts_review_status_check;
ALTER TABLE posts ADD CONSTRAINT posts_review_status_check
    CHECK (review_status IN ('approved', 'flagged', 'rejected', 'pending', 'needs_changes'));
