-- Database setup for Architecture/monetization-service

CREATE TABLE IF NOT EXISTS wallets (
    user_id       UUID PRIMARY KEY,
    balance       DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    lifetime_earnings DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    pending_payout DECIMAL(12,2) NOT NULL DEFAULT 0.00,
    currency      TEXT NOT NULL DEFAULT 'INR',
    is_frozen     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

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
    wallet_id      UUID NOT NULL REFERENCES wallets(user_id),
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
