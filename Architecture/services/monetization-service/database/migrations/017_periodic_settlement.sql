-- 017: pay creators for a PERIOD, not for a day, across all three
-- revenue streams.
--
-- The founder's instruction was: "monetization should be a payment
-- service that runs not every day - monthly once or twice, to calculate
-- the monthly. Real time we are only capturing view time, likes,
-- subscriptions." And then: "subscriptions and tips also should come in
-- monthly."
--
-- What changes, precisely:
--
--   * MEASUREMENT stays daily. analytics.content_daily_summary is still
--     the input, creator_fund_earnings is still one row per (creator,
--     day, content_type, region), the RPM rate and the quality band are
--     still resolved as of the day being measured. Nothing about what is
--     measured or how it is priced moves.
--
--   * PAYMENT becomes periodic. creator_fund_earnings rows are now an
--     ACCRUAL: computed nightly, credited to nobody. The money moves once
--     per settlement period, from creator_fund_period_settlements.
--
--   * The idempotency key moves with it. It used to be
--     (creator, day, content_type, region) - which made a day's *payment*
--     idempotent. It is now (creator, period_key, region) on the
--     settlement row, with the per-day uniqueness kept underneath as the
--     thing that guarantees a day can never be measured twice.
--
-- The double-pay hazard and how it is closed
-- -------------------------------------------
-- Tips and subscriptions are ALREADY credited to the creator's wallet at
-- the moment they happen (Service.SendTip and Service.Subscribe both call
-- ChargeAndCredit / the equivalent inline transfer). A monthly run that
-- *credited* tips and subscription revenue again would pay every tip
-- twice. So the period settlement is a STATEMENT for those two streams
-- and a PAYMENT for the fund stream only:
--
--   fund_*          - accrued over the period, credited by this run.
--   tips_*          - read back from the `tips` rows themselves,
--                     reported, NOT credited (already in the wallet).
--   subscription_*  - read back from the creator's own
--                     transactions(type='earning', reference_type='subscription')
--                     rows, reported, NOT credited (already in the wallet).
--
-- Every paisa on a statement is in exactly one of three buckets:
--   credited_paise          - this settlement paid it (the fund stream).
--   already_credited_paise  - it was in the wallet before this settlement
--                             ran: tips, subscriptions, and any fund day
--                             an overlapping period already paid.
--   pending_paise           - owed and not yet moved.
-- The three sum to net_paise. service.CheckStatementArithmetic refuses to
-- return a statement where they do not, and a test asserts it.

CREATE TABLE IF NOT EXISTS creator_fund_period_settlements (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    creator_id          UUID NOT NULL,
    -- Canonical period key. 'YYYY-MM' for a calendar month,
    -- 'YYYY-MM-H1' (1st-15th) / 'YYYY-MM-H2' (16th-end) for the
    -- twice-monthly cadence. Parsed back into the exact date window by
    -- service.ParsePeriodKey, so the key alone is enough to reproduce
    -- the settlement.
    period_key          TEXT NOT NULL,
    period_start        DATE NOT NULL,
    period_end          DATE NOT NULL,          -- exclusive
    region_code         TEXT NOT NULL DEFAULT 'IN',
    currency            TEXT NOT NULL DEFAULT 'INR',

    -- Stream 1: creator fund (ad-style views revenue). Paid by this run.
    fund_rows               BIGINT NOT NULL DEFAULT 0,
    fund_views              BIGINT NOT NULL DEFAULT 0,
    fund_watch_time_ms      BIGINT NOT NULL DEFAULT 0,
    fund_gross_paise        BIGINT NOT NULL DEFAULT 0,
    fund_platform_fee_paise BIGINT NOT NULL DEFAULT 0,
    fund_net_paise          BIGINT NOT NULL DEFAULT 0,
    fund_platform_fee_bps   BIGINT NOT NULL DEFAULT 0,

    -- Stream 2: tips. Reported, already credited at send time.
    tips_count              BIGINT NOT NULL DEFAULT 0,
    tips_gross_paise        BIGINT NOT NULL DEFAULT 0,
    tips_platform_fee_paise BIGINT NOT NULL DEFAULT 0,
    tips_net_paise          BIGINT NOT NULL DEFAULT 0,
    tips_platform_fee_bps   BIGINT NOT NULL DEFAULT 0,

    -- Stream 3: subscriptions. Reported, already credited at charge time.
    subs_count              BIGINT NOT NULL DEFAULT 0,
    subs_gross_paise        BIGINT NOT NULL DEFAULT 0,
    subs_platform_fee_paise BIGINT NOT NULL DEFAULT 0,
    subs_net_paise          BIGINT NOT NULL DEFAULT 0,
    subs_platform_fee_bps   BIGINT NOT NULL DEFAULT 0,

    -- Period totals. gross = fund + tips + subs, by construction.
    gross_paise             BIGINT NOT NULL DEFAULT 0,
    platform_fee_paise      BIGINT NOT NULL DEFAULT 0,
    net_paise               BIGINT NOT NULL DEFAULT 0,
    -- What this settlement moved into the wallet (the fund stream).
    credited_paise          BIGINT NOT NULL DEFAULT 0,
    -- What had already landed in the wallet before this settlement:
    -- tips, subscriptions, and any fund day an overlapping period paid.
    already_credited_paise  BIGINT NOT NULL DEFAULT 0,
    -- Owed but not yet moved. Zero after a healthy run; non-zero is the
    -- honest way to record a claim that partly failed.
    pending_paise           BIGINT NOT NULL DEFAULT 0,

    status              TEXT NOT NULL DEFAULT 'settled'
        CHECK (status IN ('settled','reversed')),
    settled_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- THE new idempotency key. A second settlement of the same period for
    -- the same creator cannot insert a second row.
    UNIQUE (creator_id, period_key, region_code)
);

CREATE INDEX IF NOT EXISTS idx_cf_period_settlements_creator
    ON creator_fund_period_settlements (creator_id, period_start DESC);
CREATE INDEX IF NOT EXISTS idx_cf_period_settlements_period
    ON creator_fund_period_settlements (period_key, region_code);

-- creator_fund_earnings becomes an accrual ledger. `credited` is the
-- per-day claim flag: the settlement flips false -> true inside the same
-- transaction as the wallet credit, so two pods settling the same period
-- cannot both credit the same day. UNIQUE (creator, day, content_type,
-- region) already guaranteed a day can only be measured once; `credited`
-- now guarantees it can only be PAID once, by exactly one settlement.
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS credited BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS credited_at TIMESTAMPTZ;
ALTER TABLE creator_fund_earnings
    ADD COLUMN IF NOT EXISTS settlement_id UUID;

-- Idempotent for installs that applied an earlier draft of this file.
ALTER TABLE creator_fund_period_settlements
    ADD COLUMN IF NOT EXISTS pending_paise BIGINT NOT NULL DEFAULT 0;

-- Every row that exists before this migration was written by the old
-- per-day path, which credited the wallet as it wrote. Mark them credited
-- so a period settlement covering those days can never pay them a second
-- time. This is the migration's single most important statement.
UPDATE creator_fund_earnings
SET credited = TRUE,
    credited_at = COALESCE(credited_at, settled_at)
WHERE credited = FALSE;

CREATE INDEX IF NOT EXISTS idx_cf_earnings_uncredited
    ON creator_fund_earnings (creator_id, day_bucket)
    WHERE credited = FALSE;
CREATE INDEX IF NOT EXISTS idx_cf_earnings_settlement
    ON creator_fund_earnings (settlement_id)
    WHERE settlement_id IS NOT NULL;

-- The wallet-side transaction kind for a period settlement. Restated in
-- full so this migration is independent of 010/011 apply order.
ALTER TABLE transactions DROP CONSTRAINT IF EXISTS transactions_type_check;
ALTER TABLE transactions ADD CONSTRAINT transactions_type_check
    CHECK (type IN (
        'earning','payout','refund','adjustment','subscription_payment',
        'view_earnings','creator_fund_earning',
        'tip_sent','tip_received',
        'creator_fund_period_settlement'
    ));
