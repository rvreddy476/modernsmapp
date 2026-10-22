-- Migration 069: a reel draft remembers the kind of video it is
-- (2026-10-22).
--
-- The upload studio measures the clip and patches content_type ('flick' or
-- 'long_video') onto the draft it saves. The draft had no column for it, so
-- the patch dropped it silently, and both publish paths (PublishDraft and
-- the schedule worker) hard-coded 'flick': a long video saved as a draft
-- and then published came out as a reel.
--
-- Existing rows stay NULL. NULL means "the author's client never said":
-- publish classifies it from the measured duration and frame, as the
-- direct create path does, and falls back to 'flick' (what every legacy
-- draft got) while the video is not yet measured. See draftContentType in
-- service/drafts.go. No backfill here: media_assets belongs to
-- media-service, and the publish-time rule already covers every NULL row.

ALTER TABLE reel_drafts ADD COLUMN IF NOT EXISTS content_type TEXT;

ALTER TABLE reel_drafts DROP CONSTRAINT IF EXISTS chk_reel_drafts_content_type;
ALTER TABLE reel_drafts ADD CONSTRAINT chk_reel_drafts_content_type
    CHECK (content_type IS NULL OR content_type IN ('flick', 'long_video'));
