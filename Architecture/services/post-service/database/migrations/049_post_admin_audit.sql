-- Admin console, Wave 2 — Content: audit for the two moderation writes that
-- had none, and content_reports under migration control.
--
-- 1. content_reports was only ever created by cmd/server/main.go's boot DDL,
--    so a database built from setup.sql + migrations (every integration test,
--    any fresh environment that runs migrations first) had no table behind
--    /v1/reports and the admin reports queue. The definition below is the
--    boot DDL verbatim; both are IF NOT EXISTS, so whichever runs first wins
--    and the other no-ops. The reports_count trigger stays in main.go.
CREATE TABLE IF NOT EXISTS content_reports (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id   UUID NOT NULL,
    target_type   TEXT NOT NULL,
    target_id     UUID NOT NULL,
    reason        TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'pending',
    reviewer_id   TEXT,
    review_note   TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at   TIMESTAMPTZ
);
-- A table created from an older, narrower definition (the purge store test's
-- fixture has no review columns) gains them; the review write needs all three.
ALTER TABLE content_reports ADD COLUMN IF NOT EXISTS reviewer_id TEXT;
ALTER TABLE content_reports ADD COLUMN IF NOT EXISTS review_note TEXT;
ALTER TABLE content_reports ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_content_reports_status ON content_reports(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_content_reports_target ON content_reports(target_type, target_id);
CREATE INDEX IF NOT EXISTS idx_content_reports_reporter ON content_reports(reporter_id);

-- 2. post_admin_audit: one append-only row per content-report review and per
--    comment moderation change, written in the same transaction as the change.
--    Post review_status / visibility changes keep post_review_audit (048) and
--    post decisions keep post_moderation_decisions (033); this table covers
--    the writes neither fits.
--
--    actor_user_id is the acting human: the gateway-verified moderator on the
--    legacy /v1/admin routes, or the signed act claim of an admin-service token
--    on /v1/posts/internal/admin. Never NULL: no change without an actor.
--    No FK to the target: the audit must outlive a purge. No content copied.
CREATE TABLE IF NOT EXISTS post_admin_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id   UUID NOT NULL,
    action          TEXT NOT NULL CHECK (action IN ('report.review','comment.moderate')),
    target_type     TEXT NOT NULL CHECK (target_type IN ('content_report','comment')),
    target_id       UUID NOT NULL,
    previous_value  TEXT NOT NULL,
    new_value       TEXT NOT NULL,
    note            TEXT CHECK (note IS NULL OR length(note) <= 2000),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_post_admin_audit_target
    ON post_admin_audit (target_type, target_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_post_admin_audit_action_time
    ON post_admin_audit (action, created_at DESC);

CREATE OR REPLACE FUNCTION post_admin_audit_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'post_admin_audit is append-only';
END;
$$;

DROP TRIGGER IF EXISTS trg_post_admin_audit_append_only ON post_admin_audit;
CREATE TRIGGER trg_post_admin_audit_append_only
    BEFORE UPDATE OR DELETE ON post_admin_audit
    FOR EACH ROW EXECUTE FUNCTION post_admin_audit_append_only();
