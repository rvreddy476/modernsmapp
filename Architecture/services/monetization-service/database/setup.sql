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

-- ─── Creator Fund (Tier 3a) — eligibility, RPM rates, daily earnings.
-- Moved here from cmd/server/main.go::ensureSchema so setup.sql is the
-- single source of truth and ensureSchema can be deleted (it duplicated
-- earlier tables and still defined `wallets` as a table — the latent
-- footgun behind the 2026-05-13 crash loop). Schema is unchanged.
CREATE TABLE IF NOT EXISTS creator_fund_eligibility (
    creator_id              UUID PRIMARY KEY,
    status                  TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','eligible','ineligible','suspended')),
    view_score_90d          DOUBLE PRECISION NOT NULL DEFAULT 0,
    watch_time_ms_90d       BIGINT NOT NULL DEFAULT 0,
    qualifying_content_count INTEGER NOT NULL DEFAULT 0,
    eligible_since          TIMESTAMPTZ,
    suspended_at            TIMESTAMPTZ,
    suspension_reason       TEXT,
    last_evaluated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cf_eligibility_status
    ON creator_fund_eligibility (status, last_evaluated_at);

CREATE TABLE IF NOT EXISTS monetization_rpm_rates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_type    TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code     TEXT NOT NULL DEFAULT 'IN',
    rpm_paise       BIGINT NOT NULL CHECK (rpm_paise >= 0),
    effective_from  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to    TIMESTAMPTZ,
    notes           TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_by      UUID
);
CREATE INDEX IF NOT EXISTS idx_rpm_rates_lookup
    ON monetization_rpm_rates (content_type, region_code, effective_from DESC);

INSERT INTO monetization_rpm_rates (content_type, region_code, rpm_paise, notes)
SELECT 'long_video', 'IN', 5000, 'launch baseline: 50 INR per 1000 views'
WHERE NOT EXISTS (
    SELECT 1 FROM monetization_rpm_rates
    WHERE content_type = 'long_video' AND region_code = 'IN' AND effective_to IS NULL
);
INSERT INTO monetization_rpm_rates (content_type, region_code, rpm_paise, notes)
SELECT 'flick', 'IN', 300, 'launch baseline: 3 INR per 1000 views'
WHERE NOT EXISTS (
    SELECT 1 FROM monetization_rpm_rates
    WHERE content_type = 'flick' AND region_code = 'IN' AND effective_to IS NULL
);

CREATE TABLE IF NOT EXISTS creator_fund_earnings (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id          UUID NOT NULL,
    day_bucket          DATE NOT NULL,
    content_type        TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code         TEXT NOT NULL DEFAULT 'IN',
    view_count          BIGINT NOT NULL DEFAULT 0,
    watch_time_ms       BIGINT NOT NULL DEFAULT 0,
    rpm_paise           BIGINT NOT NULL DEFAULT 0,
    gross_paise         BIGINT NOT NULL DEFAULT 0,
    platform_fee_paise  BIGINT NOT NULL DEFAULT 0,
    net_paise           BIGINT NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'settled'
        CHECK (status IN ('settled','reversed')),
    settled_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (creator_id, day_bucket, content_type, region_code)
);
CREATE INDEX IF NOT EXISTS idx_cf_earnings_creator
    ON creator_fund_earnings (creator_id, day_bucket DESC);
CREATE INDEX IF NOT EXISTS idx_cf_earnings_day
    ON creator_fund_earnings (day_bucket DESC);

-- ─── Tier 3d — Tips / Super Chat
CREATE TABLE IF NOT EXISTS tips (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sender_id       UUID NOT NULL,
    recipient_id    UUID NOT NULL,
    amount_paise    BIGINT NOT NULL CHECK (amount_paise > 0),
    currency        TEXT NOT NULL DEFAULT 'INR',
    message         TEXT,
    post_id         UUID,
    stream_id       UUID,
    status          TEXT NOT NULL DEFAULT 'completed'
        CHECK (status IN ('pending','completed','failed','reversed')),
    failure_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT tips_no_self_tip CHECK (sender_id <> recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_tips_sender    ON tips (sender_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tips_recipient ON tips (recipient_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tips_post      ON tips (post_id, created_at DESC) WHERE post_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_tips_stream    ON tips (stream_id, created_at DESC) WHERE stream_id IS NOT NULL;

-- Extend transactions.type CHECK to recognise the fund + tip kinds.
-- Idempotent re-state so the constraint matches the application code
-- regardless of which historical migration order the DB took.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN (
        'earning','payout','refund','adjustment','subscription_payment',
        'view_earnings','creator_fund_earning',
        'tip_sent','tip_received'
    ));
