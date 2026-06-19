-- Phase 3 (Integrity): scope the active-review uniqueness to primary reviews so
-- audit/shadow second-reviews can run on the same content, and add the flags table.

DROP INDEX IF EXISTS reviewer.one_active_review;
CREATE UNIQUE INDEX IF NOT EXISTS one_active_review
    ON reviewer.review_assignments (content_id)
    WHERE status IN ('assigned','in_progress') AND kind = 'primary';

CREATE TABLE IF NOT EXISTS reviewer.reviewer_flags (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reviewer_id   UUID NOT NULL REFERENCES reviewer.reviewers(id),
    assignment_id UUID REFERENCES reviewer.review_assignments(id),
    flag_type     TEXT NOT NULL,
    severity      INT NOT NULL DEFAULT 1,
    details       TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_reviewer_flags_reviewer
    ON reviewer.reviewer_flags (reviewer_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_flag_assignment_type
    ON reviewer.reviewer_flags (assignment_id, flag_type)
    WHERE assignment_id IS NOT NULL;
