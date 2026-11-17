-- 021_report_takedown.sql: an upheld report takes the post down (reels-29).
--
-- A report on a post (reel, video or post) resolved as upheld ('resolved')
-- owes post-service one signed reject decision (policy "report-v1",
-- decision id = the report id, post-service migration 087). The obligation
-- is written here IN the transaction that resolves the report, so a
-- post-service outage never loses it: the sender (service/report_takedown.go)
-- claims due rows, sends, and records the outcome on the row, where the
-- moderation queue can see it.
--
--   takedown_status  NULL        nothing owed (not a post, or not upheld)
--                    pending     owed; next try at takedown_next_attempt_at
--                    sent        post-service recorded the decision
--                    superseded  nothing to take down (post gone, or its state
--                                cannot be rejected)
--                    failed      post-service refused for good (see last_error)
--                    cancelled   the report was dismissed before it was sent
--
-- Additive and re-runnable: nullable columns, IF NOT EXISTS. Rollback is
-- dropping the index, the constraints and the seven columns; code without
-- them ignores the columns, but a row still 'pending' would then never be
-- sent, so drain (or record) pending rows before rolling back.

ALTER TABLE trust.reports
    ADD COLUMN IF NOT EXISTS takedown_status          TEXT,
    ADD COLUMN IF NOT EXISTS takedown_reason          TEXT,
    ADD COLUMN IF NOT EXISTS takedown_actor_id        UUID,
    ADD COLUMN IF NOT EXISTS takedown_attempts        INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS takedown_last_error      TEXT,
    ADD COLUMN IF NOT EXISTS takedown_next_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS takedown_done_at         TIMESTAMPTZ;

ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_takedown_status_check;
ALTER TABLE trust.reports ADD CONSTRAINT reports_takedown_status_check
    CHECK (takedown_status IS NULL
        OR takedown_status IN ('pending', 'sent', 'superseded', 'failed', 'cancelled'));

-- A pending takedown names who decided it and why: the signed claims need both.
ALTER TABLE trust.reports DROP CONSTRAINT IF EXISTS reports_takedown_pending_complete;
ALTER TABLE trust.reports ADD CONSTRAINT reports_takedown_pending_complete
    CHECK (takedown_status IS DISTINCT FROM 'pending'
        OR (takedown_actor_id IS NOT NULL AND COALESCE(takedown_reason, '') <> ''
            AND takedown_next_attempt_at IS NOT NULL));

CREATE INDEX IF NOT EXISTS idx_reports_takedown_due
    ON trust.reports (takedown_next_attempt_at, id) WHERE takedown_status = 'pending';
