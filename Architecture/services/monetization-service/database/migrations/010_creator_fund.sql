-- Tier 3a: Creator Fund — eligibility, RPM rates, daily earnings ledger
--
-- Three new tables wire up YouTube-Partner-Program-style monetization:
--   * creator_fund_eligibility: per-creator gate. Re-evaluated nightly.
--   * monetization_rpm_rates: per-content-type rate sheet (paise per 1000 views).
--   * creator_fund_earnings: daily settlement rows, unique per
--     (creator, day, content_type), so a worker re-run is idempotent.
--
-- Earnings are computed as views * rpm_paise / 1000, split 70/30
-- (creator/platform). Both sides are written through the existing
-- double-entry ledger (accounts + ledger_entries), so reconciliation
-- continues to work end-to-end.

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

-- Seed defaults: long-form video earns ~₹50 per 1000 quality views,
-- flicks earn ~₹3 per 1000. Numbers chosen as conservative launch
-- baselines; admins can tune via PUT /admin/creator-fund/rates.
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

-- Extend the transactions type CHECK to recognise the two new fund
-- transaction kinds. Old types stay valid; new types fall through the
-- existing wallet/transaction plumbing untouched.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN (
        'earning','payout','refund','adjustment','subscription_payment',
        'view_earnings','creator_fund_earning'
    ));
