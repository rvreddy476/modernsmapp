-- 008_ops_alerts.sql: operator alerts raised by consumers when a safety
-- event needs a human (Dating plan lane D8: every dating panic, and loudly a
-- panic that reached no responder). Rows reference incidents, never user ids
-- or locations, so they need no purge handling.
CREATE TABLE IF NOT EXISTS notify_meta.ops_alerts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source          TEXT        NOT NULL,
    kind            TEXT        NOT NULL,
    severity        TEXT        NOT NULL CHECK (severity IN ('info', 'warning', 'critical')),
    subject_id      UUID,
    dedupe_key      TEXT        UNIQUE,
    detail          JSONB       NOT NULL DEFAULT '{}'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    acknowledged_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_ops_alerts_open
    ON notify_meta.ops_alerts (created_at DESC)
    WHERE acknowledged_at IS NULL;
