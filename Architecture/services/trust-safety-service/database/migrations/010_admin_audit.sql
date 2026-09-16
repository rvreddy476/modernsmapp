-- 010_admin_audit.sql: append-only record of who changed a report, an
-- appeal or a grievance.
--
-- Every change to a report, appeal or grievance writes exactly one row here
-- in the same transaction as the change, so a change without its audit row
-- cannot commit. The actor is either a verified human (actor_user_id) or a
-- named service caller (actor_service, e.g. dating-service opening a
-- grievance for a dating report) — never a made-up placeholder.
--
-- Grievance assignment history (IT Rules 2021 grievance record) lives here
-- too: every grievance row carries prev_assignee/new_assignee, so the
-- officer hand-over trail is the rows where they differ, in seq order.
--
-- Append-only, following dating-service's dating_admin_audit: UPDATE,
-- DELETE and TRUNCATE are refused by trigger.
CREATE TABLE IF NOT EXISTS trust.admin_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq             BIGSERIAL NOT NULL UNIQUE,
    actor_type      TEXT NOT NULL CHECK (actor_type IN ('user', 'service')),
    actor_user_id   UUID,
    actor_service   TEXT,
    action          TEXT NOT NULL CHECK (action <> ''),
    target_type     TEXT NOT NULL CHECK (target_type IN ('report', 'appeal', 'grievance')),
    target_id       UUID NOT NULL,
    prev_status     TEXT,
    new_status      TEXT,
    prev_assignee   UUID,
    new_assignee    UUID,
    prev_resolution TEXT,
    new_resolution  TEXT,
    reason          TEXT,
    request_id      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT admin_audit_actor_check CHECK (
        (actor_type = 'user' AND actor_user_id IS NOT NULL
            AND actor_user_id <> '00000000-0000-0000-0000-000000000000'::uuid
            AND actor_service IS NULL)
        OR (actor_type = 'service' AND actor_user_id IS NULL
            AND actor_service IS NOT NULL AND btrim(actor_service) <> '')
    )
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_target
    ON trust.admin_audit (target_type, target_id, seq);
CREATE INDEX IF NOT EXISTS idx_admin_audit_actor_user
    ON trust.admin_audit (actor_user_id, created_at DESC)
    WHERE actor_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_admin_audit_created
    ON trust.admin_audit (created_at DESC);

CREATE OR REPLACE FUNCTION trust.admin_audit_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'trust.admin_audit is append-only';
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS admin_audit_no_update ON trust.admin_audit;
CREATE TRIGGER admin_audit_no_update
    BEFORE UPDATE OR DELETE ON trust.admin_audit
    FOR EACH ROW EXECUTE FUNCTION trust.admin_audit_immutable();

DROP TRIGGER IF EXISTS admin_audit_no_truncate ON trust.admin_audit;
CREATE TRIGGER admin_audit_no_truncate
    BEFORE TRUNCATE ON trust.admin_audit
    FOR EACH STATEMENT EXECUTE FUNCTION trust.admin_audit_immutable();

-- The 15-day breach queue: unresolved grievances ordered by due_at.
CREATE INDEX IF NOT EXISTS idx_grievances_overdue
    ON trust.grievances (due_at)
    WHERE status IN ('open', 'acknowledged');
