-- admin.audit_log records every admin write that passes through admin-service:
-- who acted, on which app, what operation against which target, why, under
-- which request id, and how it ended.
--
-- Columns are added nullable because setup.sql created the table earlier;
-- nothing ever wrote to it, but a pre-existing row must not break the ALTER.
-- The store sets every one of them on each new row.
ALTER TABLE admin.audit_log ADD COLUMN IF NOT EXISTS app         TEXT;
ALTER TABLE admin.audit_log ADD COLUMN IF NOT EXISTS reason      TEXT;
ALTER TABLE admin.audit_log ADD COLUMN IF NOT EXISTS request_id  TEXT;
ALTER TABLE admin.audit_log ADD COLUMN IF NOT EXISTS outcome     TEXT;
ALTER TABLE admin.audit_log ADD COLUMN IF NOT EXISTS status_code INTEGER;

ALTER TABLE admin.audit_log DROP CONSTRAINT IF EXISTS audit_log_outcome_check;
ALTER TABLE admin.audit_log ADD CONSTRAINT audit_log_outcome_check
    CHECK (outcome IS NULL OR outcome IN ('success', 'failure'));

CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created
    ON admin.audit_log (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_actor
    ON admin.audit_log (admin_actor, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_target
    ON admin.audit_log (entity_type, entity_id, created_at DESC);

-- Append-only, following dating_admin_audit. One carve-out: the CERT-In
-- retention sweep (cmd/server auditLogRetentionSweep) deletes rows past the
-- retention window, which is clamped to at least 180 days. A DELETE of a row
-- younger than that floor is refused, so neither an UPDATE nor an early DELETE
-- can rewrite history.
CREATE OR REPLACE FUNCTION admin.audit_log_append_only() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' AND OLD.created_at < NOW() - INTERVAL '180 days' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'admin.audit_log is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_append_only ON admin.audit_log;
CREATE TRIGGER audit_log_append_only
    BEFORE UPDATE OR DELETE ON admin.audit_log
    FOR EACH ROW EXECUTE FUNCTION admin.audit_log_append_only();

CREATE OR REPLACE FUNCTION admin.audit_log_no_truncate() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'admin.audit_log is append-only';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_no_truncate ON admin.audit_log;
CREATE TRIGGER audit_log_no_truncate
    BEFORE TRUNCATE ON admin.audit_log
    FOR EACH STATEMENT EXECUTE FUNCTION admin.audit_log_no_truncate();
