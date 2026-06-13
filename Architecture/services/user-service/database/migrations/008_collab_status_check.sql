-- H7: CHECK constraint on channels.collab_status. Two-state field
-- (open|closed) — see store/channels.go:25 — but the DB has historically
-- accepted any TEXT. Push the constraint to the DB so a direct write or
-- a future refactor can't introduce a third value silently.

ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_collab_status_check;
ALTER TABLE channels ADD CONSTRAINT channels_collab_status_check
    CHECK (collab_status IN ('open', 'closed'));
