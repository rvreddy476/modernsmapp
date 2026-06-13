-- Trust score state (messaging/privacy spec §8.11, §10.1, §10.2).
-- Phase 1: read-only — this table is populated by a periodic recompute job.
-- No enforcement, gates, or behavior changes are driven off these columns yet.

CREATE TABLE IF NOT EXISTS trust.user_trust_state (
    user_id                 UUID PRIMARY KEY,
    trust_score             SMALLINT NOT NULL DEFAULT 50
        CHECK (trust_score BETWEEN 0 AND 100),
    trust_tier              VARCHAR(16) NOT NULL DEFAULT 'new'
        CHECK (trust_tier IN ('new', 'low', 'standard', 'trusted', 'verified')),
    account_age_days        INTEGER NOT NULL DEFAULT 0,
    reports_received        INTEGER NOT NULL DEFAULT 0,
    blocks_received         INTEGER NOT NULL DEFAULT 0,
    connection_accept_ratio NUMERIC(4,3),
    last_recomputed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    shadowbanned            BOOLEAN NOT NULL DEFAULT FALSE,
    suspended_until         TIMESTAMPTZ
);

-- Lets the recompute job pick the stalest rows first.
CREATE INDEX IF NOT EXISTS idx_user_trust_state_recomputed
    ON trust.user_trust_state (last_recomputed_at);
