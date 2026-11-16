-- 030: introductory zero commission (16 Nov 2026).
--
-- "0% commission for the first 3 months": per creator, from the UTC day their
-- monetization was approved (creator_fund_eligibility.eligible_since) or the
-- launch date if later, for 3 calendar months, half-open [start, end) in UTC.
-- The platform's commission is 0 on every earning whose event time is inside
-- the window. The rule lives in internal/service/intro_commission.go.
--
-- Additive only. Nothing here reprices or rewrites a row: existing fund rows
-- keep the money they were priced at and simply have no recorded rule (NULL),
-- which the statement reports as "standard". Existing settlements keep their
-- figures; their commission breakdown is NULL until a run refreshes them.

-- 1. Each fund accrual row records the rate it was ACTUALLY priced at and why.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS platform_fee_bps        BIGINT
        CHECK (platform_fee_bps IS NULL OR (platform_fee_bps >= 0 AND platform_fee_bps <= 10000)),
    ADD COLUMN IF NOT EXISTS commission_reason       TEXT
        CHECK (commission_reason IS NULL OR commission_reason IN ('standard', 'intro_zero_commission')),
    ADD COLUMN IF NOT EXISTS commission_window_start TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS commission_window_end   TIMESTAMPTZ;

-- An intro row names its window and takes nothing; a standard row names none.
ALTER TABLE creator_fund_earnings DROP CONSTRAINT IF EXISTS creator_fund_earnings_intro_commission_check;
ALTER TABLE creator_fund_earnings ADD CONSTRAINT creator_fund_earnings_intro_commission_check
    CHECK (
        commission_reason IS DISTINCT FROM 'intro_zero_commission'
        OR (platform_fee_bps = 0 AND platform_fee_paise = 0
            AND commission_window_start IS NOT NULL
            AND commission_window_end IS NOT NULL
            AND commission_window_start < commission_window_end
            AND day_bucket >= (commission_window_start AT TIME ZONE 'UTC')::date
            AND day_bucket <  (commission_window_end AT TIME ZONE 'UTC')::date)
    );

-- 2. Each period settlement carries the per-stream commission breakdown the
--    creator reads (rate applied, reason, window) and the creator's window.
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS commission         JSONB,
    ADD COLUMN IF NOT EXISTS intro_window_start TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS intro_window_end   TIMESTAMPTZ;
