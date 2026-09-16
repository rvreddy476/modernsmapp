-- 009_page_admin_audit.sql: append-only record of every platform decision on
-- a business page or one of its verification documents (admin console,
-- Wave 2 — Content: business pages).
--
-- Every approve, reject, suspend and disable, and every document approve or
-- reject, writes exactly one row here in the same transaction as the status
-- change, so a decision without its audit row cannot commit. Both entry
-- points write it:
--   via = 'admin_service'    the token-only family /v1/users/internal/admin/*;
--                            actor_user_id is the token's signed act claim
--   via = 'pages_allowlist'  the legacy /v1/pages/:id/* routes gated by
--                            PAGES_ADMIN_USER_IDS; actor_user_id is X-User-Id
--
-- page_id carries no foreign key: an account purge deletes the page, and the
-- decision history must outlive it. No personal data beyond the acting admin's
-- id is stored; reason is the admin's own text.
--
-- Append-only, following dating-service's dating_admin_audit: UPDATE, DELETE
-- and TRUNCATE are refused by trigger.
CREATE TABLE IF NOT EXISTS page_admin_audit (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seq           BIGSERIAL NOT NULL UNIQUE,
    actor_user_id UUID NOT NULL
        CHECK (actor_user_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    via           TEXT NOT NULL CHECK (via IN ('admin_service', 'pages_allowlist')),
    action        TEXT NOT NULL CHECK (action IN (
                      'page.approve', 'page.reject', 'page.suspend', 'page.disable',
                      'document.approve', 'document.reject')),
    page_id       UUID NOT NULL,
    document_id   UUID,
    prev_status   TEXT NOT NULL,
    new_status    TEXT NOT NULL,
    reason        TEXT,
    request_id    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT page_admin_audit_document_check CHECK (
        (action LIKE 'document.%' AND document_id IS NOT NULL)
        OR (action LIKE 'page.%' AND document_id IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_page_admin_audit_page
    ON page_admin_audit (page_id, seq);
CREATE INDEX IF NOT EXISTS idx_page_admin_audit_actor
    ON page_admin_audit (actor_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_page_admin_audit_created
    ON page_admin_audit (created_at DESC);

CREATE OR REPLACE FUNCTION page_admin_audit_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'page_admin_audit is append-only';
END $$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS page_admin_audit_no_update ON page_admin_audit;
CREATE TRIGGER page_admin_audit_no_update
    BEFORE UPDATE OR DELETE ON page_admin_audit
    FOR EACH ROW EXECUTE FUNCTION page_admin_audit_immutable();

DROP TRIGGER IF EXISTS page_admin_audit_no_truncate ON page_admin_audit;
CREATE TRIGGER page_admin_audit_no_truncate
    BEFORE TRUNCATE ON page_admin_audit
    FOR EACH STATEMENT EXECUTE FUNCTION page_admin_audit_immutable();

-- Stats and the review queue.
CREATE INDEX IF NOT EXISTS idx_pvd_pending
    ON page_verification_documents (page_id) WHERE status = 'pending';
