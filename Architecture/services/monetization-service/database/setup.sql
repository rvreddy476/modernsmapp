-- Database setup for Architecture/monetization-service

-- creator_ledger (renamed from `wallets` 2026-04-30, Phase 2 §D4).
-- Holds creator earnings: lifetime_earnings, pending_payout, balance.
-- NOT a consumer wallet — that lives in wallet-service.
CREATE TABLE IF NOT EXISTS creator_ledger (
    user_id       UUID PRIMARY KEY,
    balance       DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    lifetime_earnings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    pending_payout DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    currency      TEXT NOT NULL DEFAULT 'INR',
    is_frozen     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Migrate any legacy `wallets` TABLE into creator_ledger before swapping
-- it for a view alias. Older deploys created `wallets` as a real table;
-- when this schema re-runs, `CREATE OR REPLACE VIEW wallets` fails with
-- `"wallets" is not a view (SQLSTATE 42809)` because a table by that
-- name still exists. The block below copies any surviving rows into
-- creator_ledger (idempotent via ON CONFLICT), drops the table, then
-- the view creation below succeeds. Schemas drifted? Worst case the
-- INSERT errors and the deploy stops — that's the right outcome for
-- a non-trivial column mismatch.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_class
        WHERE relname = 'wallets' AND relkind = 'r'
    ) THEN
        INSERT INTO creator_ledger (
            user_id, balance, lifetime_earnings, pending_payout,
            currency, is_frozen, created_at, updated_at
        )
        SELECT
            user_id, balance, lifetime_earnings, pending_payout,
            currency, is_frozen, created_at, updated_at
        FROM wallets
        ON CONFLICT (user_id) DO NOTHING;
        DROP TABLE wallets CASCADE;
    END IF;
END $$;

-- Deprecated read-only alias (drop after 2026-10-30).
CREATE OR REPLACE VIEW wallets AS SELECT * FROM creator_ledger;

CREATE TABLE IF NOT EXISTS creator_tiers (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id       UUID NOT NULL,
    name             TEXT NOT NULL,
    price            DECIMAL(8,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    perks            JSONB NOT NULL DEFAULT '[]',
    subscriber_count INTEGER NOT NULL DEFAULT 0,
    is_active        BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_creator_tiers_creator ON creator_tiers (creator_id);

CREATE TABLE IF NOT EXISTS transactions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    wallet_id      UUID NOT NULL REFERENCES creator_ledger(user_id),
    type           TEXT NOT NULL CHECK (type IN ('earning', 'payout', 'refund', 'adjustment', 'subscription_payment')),
    amount         DECIMAL(12,2) NOT NULL,
    currency       TEXT NOT NULL DEFAULT 'INR',
    status         TEXT NOT NULL DEFAULT 'completed' CHECK (status IN ('pending', 'completed', 'failed', 'reversed')),
    reference_type TEXT NOT NULL DEFAULT '',
    reference_id   TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_transactions_wallet ON transactions (wallet_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_type ON transactions (wallet_id, type, created_at DESC);

CREATE TABLE IF NOT EXISTS payout_methods (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    method_type      TEXT NOT NULL CHECK (method_type IN ('upi', 'bank_transfer', 'paypal')),
    details_encrypted TEXT NOT NULL,
    is_verified      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_payout_methods_user ON payout_methods (user_id);

CREATE TABLE IF NOT EXISTS subscriptions (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subscriber_id        UUID NOT NULL,
    creator_id           UUID NOT NULL,
    tier_id              UUID NOT NULL REFERENCES creator_tiers(id),
    tier_name            TEXT NOT NULL,
    price                DECIMAL(8,2) NOT NULL,
    currency             TEXT NOT NULL DEFAULT 'INR',
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled', 'expired', 'paused')),
    current_period_start TIMESTAMPTZ NOT NULL DEFAULT now(),
    current_period_end   TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '30 days',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_subscriber ON subscriptions (subscriber_id, status);
CREATE INDEX IF NOT EXISTS idx_subscriptions_creator ON subscriptions (creator_id, status);

CREATE TABLE IF NOT EXISTS tax_info (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL UNIQUE,
    country             TEXT NOT NULL,
    tax_data_encrypted  TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'pending' CHECK (verification_status IN ('pending', 'verified', 'rejected')),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monetization_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    table_name   TEXT NOT NULL,
    operation    TEXT NOT NULL,
    old_data     JSONB,
    new_data     JSONB,
    performer_id UUID,
    ip_address   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_audit_log_table ON monetization_audit_log (table_name, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_audit_log_performer ON monetization_audit_log (performer_id, created_at DESC);
