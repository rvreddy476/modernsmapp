-- Migration 065: the purge worker and case-specific restrictions (2026-10-17).
--
-- 056 gave post_restrictions.post_id an ON DELETE RESTRICT FK to posts, and
-- post_restriction_events (append-only: its trigger refuses DELETE) points at
-- post_restrictions. Together they made every post that EVER had a hold
-- unpurgeable: the worker's DELETE FROM posts failed with 23503 and was
-- retried every tick forever, and an account purge of its author failed the
-- same way. The restriction rows cannot be deleted either, because their
-- events must survive.
--
-- The rule now (internal/postpurge, store.PurgePost):
--
--   * a post under an ACTIVE restriction is never purged. The worker's scan
--     skips it (active_restriction_count > 0), PurgePost re-checks the rows
--     under the post's row lock and refuses with ErrPurgeHeld, and the worker
--     reports how many overdue posts are held. Once the case releases its
--     hold, the next tick purges the post as usual;
--   * a post whose restrictions are all released is purged, and its
--     restriction rows and their events stay as the hold's audit trail, the
--     same way post_restriction_events.post_id (056) and post_review_audit
--     (048) already outlive the post. Rows carry case ids, states and reason
--     codes, no content.
--
-- So the FK goes. ApplyPostRestriction locks the posts row FOR UPDATE and
-- refuses a missing post (ErrRestrictionSubjectNotFound), so no hold can be
-- placed on a post that does not exist, and PurgePost takes the same lock,
-- so a hold and a purge cannot interleave. The deferred count trigger
-- already returns early when the post is gone.
ALTER TABLE post_restrictions DROP CONSTRAINT IF EXISTS post_restrictions_post_id_fkey;

-- Per-post purge back-off. A post whose purge transaction fails gets a row
-- here and the scan skips it until next_attempt_at (2^attempts minutes,
-- capped at a day), so one bad post costs a retry a day, not one every
-- tick. A successful purge deletes the row in its own transaction. No FK
-- to posts: it is cleaned by the purge, and a row for a post that has
-- since gone some other way is harmless.
CREATE TABLE IF NOT EXISTS post_purge_failures (
    post_id          UUID PRIMARY KEY,
    attempts         INT NOT NULL DEFAULT 0,
    last_error       TEXT,
    first_failed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_failed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_attempt_at  TIMESTAMPTZ NOT NULL
);
