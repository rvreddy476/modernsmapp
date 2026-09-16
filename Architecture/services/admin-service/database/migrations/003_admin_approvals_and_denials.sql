-- Two-person approval and a complete admin trail.
--
-- 1. admin.audit_log now records every request to an admin route, including
--    the ones refused before they ran (missing permission, MFA or step-up) and
--    the approval lifecycle. Outcomes widen accordingly:
--      success | failure  the operation ran; 2xx or not
--      denied             refused by admin-service before it ran
--      pending            a two-person request is waiting for a second admin
--      rejected           a second admin rejected a two-person request
ALTER TABLE admin.audit_log DROP CONSTRAINT IF EXISTS audit_log_outcome_check;
ALTER TABLE admin.audit_log ADD CONSTRAINT audit_log_outcome_check
    CHECK (outcome IS NULL OR outcome IN ('success', 'failure', 'denied', 'pending', 'rejected'));

-- 2. admin.approvals holds a two-person operation between the first admin's
--    request and a second admin's decision. payload_hash covers app, operation,
--    target and the canonical payload; execution re-checks it.
CREATE TABLE IF NOT EXISTS admin.approvals (
    id                  UUID PRIMARY KEY,
    app                 TEXT NOT NULL,
    operation           TEXT NOT NULL,
    target_type         TEXT NOT NULL,
    target_id           TEXT NOT NULL,
    payload             JSONB NOT NULL,
    payload_hash        TEXT NOT NULL,
    requester           UUID NOT NULL,
    requester_reason    TEXT NOT NULL,
    required_permission TEXT NOT NULL,
    status              TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'executed')),
    approver            UUID,
    decision_reason     TEXT,
    result_status       INTEGER,
    result_outcome      TEXT CHECK (result_outcome IS NULL OR result_outcome IN ('success', 'failure')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at          TIMESTAMPTZ,
    executed_at         TIMESTAMPTZ,
    expires_at          TIMESTAMPTZ NOT NULL,
    -- The requester can never be the approver, whatever the application does.
    CONSTRAINT approvals_not_self_decided CHECK (approver IS NULL OR approver <> requester),
    CONSTRAINT approvals_decided_has_approver CHECK (status IN ('pending', 'expired') OR approver IS NOT NULL),
    CONSTRAINT approvals_executed_has_result CHECK (status <> 'executed' OR (executed_at IS NOT NULL AND result_outcome IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_admin_approvals_pending
    ON admin.approvals (required_permission, created_at DESC) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_admin_approvals_target
    ON admin.approvals (app, target_type, target_id, created_at DESC);
