-- Tier 2b: comment moderation queue.
--
-- Reports against comments already land in content_reports (target_type
-- = 'comment'), but the comments table itself has no notion of "this
-- one is hidden / under review". This migration adds two columns:
--
--   moderation_status — visible | hidden | removed | review
--   flagged_count     — how many distinct reports have hit this row
--
-- Visible (default) is rendered like before. Hidden takes the comment
-- out of the public thread but keeps the row for audit. Removed is a
-- harder takedown (also out of thread; admin-only undo). Review is
-- "auto-flagged at N reports, awaiting moderator decision" — still
-- rendered to the author, hidden from everyone else.
--
-- Existing rows default to 'visible' and 0 reports, so this is a
-- backwards-compatible add.

ALTER TABLE comments
    ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'visible'
        CHECK (moderation_status IN ('visible','hidden','removed','review'));

ALTER TABLE comments
    ADD COLUMN IF NOT EXISTS flagged_count INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_comments_moderation_status
    ON comments (moderation_status, created_at DESC)
    WHERE moderation_status IN ('hidden','removed','review');

CREATE INDEX IF NOT EXISTS idx_comments_flagged
    ON comments (flagged_count DESC, created_at DESC)
    WHERE flagged_count > 0;
