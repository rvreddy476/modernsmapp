-- Bring the role outbox from setup.sql into the tracked upgrade path.
-- Approval and suspension enqueue here in the same transaction as the decision.
CREATE SCHEMA IF NOT EXISTS rider;
CREATE TABLE IF NOT EXISTS rider.identity_role_intents (
    id BIGSERIAL PRIMARY KEY,
    op TEXT NOT NULL CHECK (op IN ('grant','revoke')),
    user_id UUID NOT NULL,
    role_name TEXT NOT NULL,
    service TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    attempts INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ,
    dead_lettered_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_rider_identity_role_intents_pending
    ON rider.identity_role_intents (next_attempt_at)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_rider_identity_role_intents_dead
    ON rider.identity_role_intents (dead_lettered_at)
    WHERE dead_lettered_at IS NOT NULL;
