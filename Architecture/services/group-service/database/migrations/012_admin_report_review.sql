-- Admin console (Wave 2 — Content, Chat): the group report queue.
--
-- group_reports (006) had a reviewed_by column nothing wrote and no route
-- that read the table. admin-service's token family
-- (/v1/groups/internal/admin) now lists reports and records a decision.
-- A decision is uphold or dismiss with a reason; group-service has no
-- enforcement tied to it (see internal/http/admin_token.go).

ALTER TABLE group_reports ADD COLUMN IF NOT EXISTS review_reason TEXT;
ALTER TABLE group_reports ADD COLUMN IF NOT EXISTS reviewed_at   TIMESTAMPTZ;

-- Queue read path, newest first, and the 7-day decided count.
CREATE INDEX IF NOT EXISTS idx_group_reports_status_created
    ON group_reports(status, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_group_reports_reviewed_at
    ON group_reports(reviewed_at) WHERE reviewed_at IS NOT NULL;

-- Every admin decision writes one row here in the same transaction as the
-- change. actor_id is the token's signed act claim, never a header.
-- Append-only, following dating_admin_audit. No foreign keys: the trail must
-- outlive the group and the report it names.
CREATE TABLE IF NOT EXISTS group_admin_audit (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id     UUID NOT NULL,
    action       TEXT NOT NULL,
    target_type  TEXT NOT NULL,
    target_id    UUID NOT NULL,
    reason       TEXT NOT NULL DEFAULT '',
    metadata     JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_admin_audit_target
    ON group_admin_audit(target_type, target_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_group_admin_audit_actor
    ON group_admin_audit(actor_id, created_at DESC);

CREATE OR REPLACE FUNCTION group_admin_audit_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'group_admin_audit is append-only';
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS group_admin_audit_no_update ON group_admin_audit;
CREATE TRIGGER group_admin_audit_no_update
    BEFORE UPDATE OR DELETE ON group_admin_audit
    FOR EACH ROW EXECUTE FUNCTION group_admin_audit_immutable();
