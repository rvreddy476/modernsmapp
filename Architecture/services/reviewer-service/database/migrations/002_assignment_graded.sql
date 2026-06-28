-- Phase 2 grading: mark whether an assignment has been backtested against the
-- engagement answer-key. Additive + idempotent for DBs bootstrapped at Phase 1.
ALTER TABLE reviewer.review_assignments
    ADD COLUMN IF NOT EXISTS graded BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_assignments_to_grade
    ON reviewer.review_assignments (decided_at)
    WHERE status = 'completed' AND graded = FALSE;
