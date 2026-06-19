-- 022_app_feedback.sql
-- Product feedback submitted from the app's video "More" sheet. This is
-- DISTINCT from trust-safety reports (/v1/reports): feedback is "help us
-- improve" with no moderation workflow, reports flag policy violations.

CREATE TABLE IF NOT EXISTS app_feedback (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL,
    feedback_type  TEXT NOT NULL DEFAULT 'other'
        CHECK (feedback_type IN ('bug','feature','performance','content','ui','other')),
    post_id        UUID,
    message        TEXT NOT NULL CHECK (char_length(message) BETWEEN 1 AND 5000),
    context        TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_app_feedback_user ON app_feedback (user_id, created_at DESC);
