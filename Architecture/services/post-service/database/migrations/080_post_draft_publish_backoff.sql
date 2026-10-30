-- 080: the scheduled composer-draft queue backs off (2026-10-30, tube audit H1).
--
-- 079 fixed this for reel_drafts; post_drafts had the same jam. The worker
-- claims the 50 due drafts with the earliest schedule_at, and a draft whose
-- publication failed for any reason but a handful was released back to
-- 'draft' with its schedule_at unchanged, so it was first in line again on
-- the next tick. Fifty drafts that could never publish (a long video saved
-- with no title, a text over the limit, ...) held the whole window and
-- nobody else's scheduled post was claimed again.
--
-- Now a refusal of the draft itself parks it as 'blocked' with a reason
-- (service.postDraftBlockReason), and a transient failure returns it with a
-- next attempt time:
--
--   * publish_attempts counts the failed attempts since the draft was last
--     scheduled or edited; past the limit the draft is parked
--     ('publish_failed');
--   * next_attempt_at is when the worker may claim it again. NULL = due at
--     schedule_at. The claim skips a draft whose next attempt is still
--     ahead, so it no longer holds a place in the window.
--
-- An edit that changes schedule_at, or revives a blocked draft, clears both.

ALTER TABLE post_drafts ADD COLUMN IF NOT EXISTS publish_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE post_drafts ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

-- The claim's due scan: drafts waiting on a time, in the order they are due.
CREATE INDEX IF NOT EXISTS idx_post_drafts_next_attempt
    ON post_drafts ((COALESCE(next_attempt_at, schedule_at)))
    WHERE status = 'draft' AND schedule_at IS NOT NULL;
