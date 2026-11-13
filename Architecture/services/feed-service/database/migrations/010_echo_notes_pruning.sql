-- Migration 010 (2026-11-13): echo_notes pruning (service/notes_prune.go).
--
-- A row is pruned once its note is past the timeline retention (360 days;
-- no notes_timeline row and no For You source reaches it any more), or once
-- it has been deleted for longer than post-service's restore window, after
-- which post-service has purged the post and it can never be restored.
-- deleted_at is when PostDeleted marked it (SetNoteDeleted); rows deleted
-- before this migration start their window now.
ALTER TABLE echo_notes ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
UPDATE echo_notes SET deleted_at = NOW() WHERE deleted AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_echo_notes_created_at ON echo_notes (created_at);
CREATE INDEX IF NOT EXISTS idx_echo_notes_deleted_at ON echo_notes (deleted_at) WHERE deleted;
