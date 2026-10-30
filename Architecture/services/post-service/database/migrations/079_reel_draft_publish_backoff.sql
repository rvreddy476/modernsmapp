-- 079: the scheduled reel-draft queue backs off (2026-10-30, audit HIGH 1).
--
-- The schedule worker claims the 50 due reel drafts with the earliest
-- schedule_at. A draft whose publication failed used to go straight back to
-- 'draft' with its schedule_at unchanged, so it was first in line again on
-- the next tick: 50 drafts that could never publish held the whole claim
-- window and nobody else's scheduled reel was claimed again.
--
-- Now a refusal of the draft itself (a value CreatePost refuses, no video,
-- media that is gone, ...) parks the draft as 'blocked' with the reason, and
-- a transient failure returns it with a next attempt time:
--
--   * publish_attempts counts the failed attempts since the draft was last
--     scheduled; past the limit the draft is parked ('publish_failed');
--   * next_attempt_at is when the worker may claim it again. NULL = due at
--     schedule_at. The claim skips a draft whose next attempt is still
--     ahead, so it no longer holds a place in the window.
--
-- Scheduling again (PATCH schedule_at, POST /publish with schedule_at)
-- clears both.

ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS publish_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;

-- The claim's due scan: drafts waiting on a time, in the order they are due.
CREATE INDEX IF NOT EXISTS idx_reel_drafts_due
    ON reel_drafts ((COALESCE(next_attempt_at, schedule_at)))
    WHERE status = 'draft' AND schedule_at IS NOT NULL;

-- A draft takes the visibilities CreatePost takes (service.PostVisibilities).
-- 006's inline CHECK left out 'friends', so saving a friends-only reel draft
-- was a 500 while publishing one directly worked.
ALTER TABLE reel_drafts DROP CONSTRAINT IF EXISTS reel_drafts_visibility_check;
ALTER TABLE reel_drafts ADD CONSTRAINT reel_drafts_visibility_check
    CHECK (visibility IN ('public', 'followers', 'friends', 'private', 'unlisted'));
