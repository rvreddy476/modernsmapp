-- Reviewer workflow rework: reviewers Approve (publish) or Escalate (with
-- comments) to a super-admin, who decides reject | request_edits | approve.

ALTER TABLE reviewer.review_assignments DROP CONSTRAINT IF EXISTS assignments_decision_chk;
ALTER TABLE reviewer.review_assignments ADD CONSTRAINT assignments_decision_chk
    CHECK (decision IS NULL OR decision IN ('approve','escalate','reject','flag_unsafe'));

CREATE TABLE IF NOT EXISTS reviewer.escalations (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_id        UUID NOT NULL,
    creator_id        UUID NOT NULL,
    reviewer_id       UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id     UUID REFERENCES reviewer.review_assignments(id),
    reviewer_comments TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'open',
    admin_decision    TEXT,
    admin_notes       TEXT,
    resolved_by       UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at       TIMESTAMPTZ,
    CONSTRAINT escalations_status_chk CHECK (status IN ('open','resolved')),
    CONSTRAINT escalations_decision_chk CHECK (admin_decision IS NULL OR admin_decision IN ('reject','request_edits','approve'))
);
CREATE INDEX IF NOT EXISTS idx_escalations_open
    ON reviewer.escalations (created_at) WHERE status = 'open';
CREATE INDEX IF NOT EXISTS idx_escalations_creator
    ON reviewer.escalations (creator_id, created_at DESC);
