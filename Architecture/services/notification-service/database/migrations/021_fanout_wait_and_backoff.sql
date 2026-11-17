-- Migration 021: upload fan-out waits for the post instead of dropping it,
-- and retries with backoff (long-video audit lv-1, lv-17, lv-19; reels-30).
--
-- Before: the worker claimed any pending job every 5 s, read the post as an
-- anonymous viewer, took the 404 of a still-processing post as "deleted"
-- and marked every subscriber done; a transient error burnt one of five
-- quick attempts and the job was lost after ~30 s.
--
-- available_at  not-before for a pending job. A job whose post is not
--               visible yet (processing, pending review, scheduled, private)
--               is deferred to now + wait backoff; a transient failure to
--               now + retry backoff. Claim skips rows not yet due.
-- wait_reason   why the job is waiting for the post ('' when it is not).
--               A failed row with a wait_reason expired waiting; one
--               without failed on errors and is eligible for replay.
-- waits         consecutive deferrals; bounds the wait (about 72 h).
-- replays       automatic replays of a job that failed on errors.
--
-- Rollback: every column has a default and the old binary names none of
-- them, so it keeps working; it ignores available_at and so claims
-- deferred rows early, which is exactly today's behaviour.
ALTER TABLE subscriber_fanout_jobs ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE subscriber_fanout_jobs ADD COLUMN IF NOT EXISTS wait_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE subscriber_fanout_jobs ADD COLUMN IF NOT EXISTS waits INT NOT NULL DEFAULT 0;
ALTER TABLE subscriber_fanout_jobs ADD COLUMN IF NOT EXISTS replays INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_fanout_jobs_due
    ON subscriber_fanout_jobs (available_at)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_fanout_jobs_failed
    ON subscriber_fanout_jobs (updated_at)
    WHERE status = 'failed';
