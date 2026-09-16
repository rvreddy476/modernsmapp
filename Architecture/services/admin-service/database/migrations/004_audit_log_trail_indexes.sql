-- The console's audit page (GET /v1/admin/audit) pages newest first by
-- (created_at, id), usually restricted to one app.
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_app_created
    ON admin.audit_log (app, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_admin_audit_log_created_id
    ON admin.audit_log (created_at DESC, id DESC);
