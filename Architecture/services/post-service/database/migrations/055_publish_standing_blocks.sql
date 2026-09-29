-- 055: parked publication (Copyright Match plan section 6.4, P-5).
--
-- Every publication path now asks trust-safety for the author's standing.
-- Background paths (the schedule worker, the draft workers) cannot answer
-- a 403, so a refused or persistently unknown item is PARKED with a reason
-- the author's UI can show, instead of being retried every tick forever.
--
--   * composer drafts already have status 'blocked' + blocked_reason (026);
--   * reel drafts gain the same pair here;
--   * a scheduled post keeps its row and its publish_at (the author's
--     intent) but gets a row in post_publish_blocks, which the worker's due
--     scan excludes. Rescheduling, "publish now" and the flip itself clear
--     it, so the author re-arms the post by acting on it.

CREATE TABLE IF NOT EXISTS post_publish_blocks (
    post_id    UUID PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    reason     TEXT NOT NULL,
    blocked_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS blocked_reason TEXT;

-- 006 declared the status CHECK inline on the column, so Postgres named it
-- reel_drafts_status_check.
ALTER TABLE reel_drafts DROP CONSTRAINT IF EXISTS reel_drafts_status_check;
ALTER TABLE reel_drafts ADD CONSTRAINT reel_drafts_status_check
    CHECK (status IN ('draft', 'processing', 'publishing_pending', 'published', 'rejected', 'deleted', 'blocked'));
