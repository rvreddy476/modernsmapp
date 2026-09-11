-- 019: a correctable ledger, part two — every accrual says how it was
-- computed, sub-paise remainders are carried instead of truncated, and
-- the fund has a cap.
--
-- Versioning. rate_id and band_id name the exact rows that priced the
-- day; rule_version names the formula ('cf-1' is the carry formula below;
-- rows from before this migration are stamped 'cf-0' because they were
-- priced by per-step integer truncation); input_revision is the sha256 of
-- the analytics rows the day was measured from, including their
-- updated_at, so a re-accrual can tell that its inputs moved and refuse
-- rather than silently no-op.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS rate_id UUID;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS band_id UUID;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS rule_version TEXT NOT NULL DEFAULT 'cf-1';
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS input_revision TEXT;

-- Rounding. Money is computed in micro-paise (1 paise = 1,000,000):
--   base_micro  = views x rpm_paise x 1000
--   gross_micro = base_micro x multiplier_bps / 10000
--   total       = gross_micro + carry_in
--   gross_paise = total / 1,000,000 ; carry_out = total % 1,000,000
-- Truncation happens once, at the carry boundary, and the remainder rolls
-- into the next day's accrual for the same (creator, content_type,
-- region). One flick view (300 micro-paise x 1000 = 300,000) is no longer
-- lost; four of them pay a paise.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS gross_micro_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS carry_in_micro_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS carry_out_micro_paise BIGINT NOT NULL DEFAULT 0;
-- Why a row accrued less than its views were worth, or nothing:
-- 'budget_capped' (partial: the day took exactly what remained under the
-- cap) or 'budget_exhausted' (a zero row; the views, rate and band are
-- still recorded so the day is provably measured). NULL is a full accrual.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS skip_reason TEXT;

-- Rows that exist before this migration were priced by the old formula.
-- Stamp them so the two populations are distinguishable forever.
UPDATE creator_fund_earnings
SET rule_version = 'cf-0'
WHERE rule_version = 'cf-1'
  AND gross_micro_paise = 0
  AND input_revision IS NULL;

CREATE TABLE IF NOT EXISTS creator_fund_carry (
    creator_id        UUID NOT NULL,
    content_type      TEXT NOT NULL CHECK (content_type IN ('long_video','flick')),
    region_code       TEXT NOT NULL DEFAULT 'IN',
    carry_micro_paise BIGINT NOT NULL DEFAULT 0
        CHECK (carry_micro_paise >= 0 AND carry_micro_paise < 1000000),
    last_day_bucket   DATE,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (creator_id, content_type, region_code)
);

-- The cap. One row per settlement period and region; accrued_paise is
-- raised inside the same transaction as each accrual, under a row lock,
-- so the sum of gross_paise for the period can never exceed cap_paise.
-- A period with no row is uncapped (the accrual logs that once). Once
-- exhausted_at is set, every later accrual in the period is a zero row —
-- a PROSPECTIVE stop: nothing already earned is reduced, which is also
-- why the admin route refuses to lower cap_paise below accrued_paise.
CREATE TABLE IF NOT EXISTS creator_fund_budgets (
    period_key       TEXT NOT NULL,
    region_code      TEXT NOT NULL DEFAULT 'IN',
    cap_paise        BIGINT NOT NULL CHECK (cap_paise >= 0),
    accrued_paise    BIGINT NOT NULL DEFAULT 0 CHECK (accrued_paise >= 0),
    exhausted_at     TIMESTAMPTZ,
    exhausted_on_day DATE,
    notes            TEXT,
    created_by       UUID,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (period_key, region_code),
    CHECK (accrued_paise <= cap_paise)
);

-- The statement carries the cap it was accrued under, the day the cap was
-- reached, and how many of its fund rows were recorded at zero because of
-- it, so the sentence under the total can say why.
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS budget_cap_paise BIGINT;
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS budget_exhausted_on_day DATE;
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS fund_rows_skipped BIGINT NOT NULL DEFAULT 0;
