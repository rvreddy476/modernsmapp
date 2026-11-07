-- 026: review 2026-11-07. Expand-only: new tables and new nullable or
-- defaulted columns; nothing dropped, nothing renamed, no constraint
-- tightened. A binary from before this migration keeps working against it.

-- 1. A final day whose analytics moved anyway (an operator's forced
--    rewrite, analytics migration 016) no longer fails the creator's
--    period. The accrual records the difference here, priced at the day's
--    own rate and quality multiplier, and carries on. Nothing on this row
--    moves money: posting it is an explicit, audited correction
--    (PostAdjustment, cause creator_fund_earning_correction) by a person.
CREATE TABLE IF NOT EXISTS creator_fund_revision_adjustments (
    id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- No foreign key, like settlement_id on the earnings: fixture and
    -- remediation deletes of earnings rows must not be blocked by a memo.
    earning_id                UUID NOT NULL,
    creator_id                UUID NOT NULL,
    day_bucket                DATE NOT NULL,
    content_type              TEXT NOT NULL,
    region_code               TEXT NOT NULL,
    recorded_revision         TEXT NOT NULL,
    observed_revision         TEXT NOT NULL,
    recorded_views            BIGINT NOT NULL,
    observed_views            BIGINT NOT NULL,
    -- Gross before the carry, in micro-paise, at the earning's own rate
    -- and multiplier: the like-for-like difference, signed.
    recorded_gross_micro_paise BIGINT NOT NULL,
    observed_gross_micro_paise BIGINT NOT NULL,
    delta_gross_micro_paise   BIGINT NOT NULL,
    status                    TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'posted', 'dismissed')),
    created_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- One row per distinct drift: re-running the accrual over the same
    -- moved inputs records it once.
    UNIQUE (earning_id, observed_revision)
);
CREATE INDEX IF NOT EXISTS idx_cf_revision_adjustments_pending
    ON creator_fund_revision_adjustments (creator_id, day_bucket)
    WHERE status = 'pending';

-- 2. Fundraisers in integer paise. goal_amount / raised_amount stay (and
--    are kept in step by the new code) for any reader that predates this.
ALTER TABLE fundraisers
    ADD COLUMN IF NOT EXISTS goal_paise BIGINT;
ALTER TABLE fundraisers
    ADD COLUMN IF NOT EXISTS raised_paise BIGINT NOT NULL DEFAULT 0;
UPDATE fundraisers
   SET goal_paise = ROUND(goal_amount * 100)::BIGINT
 WHERE goal_paise IS NULL;
UPDATE fundraisers
   SET raised_paise = ROUND(raised_amount * 100)::BIGINT
 WHERE raised_paise = 0 AND raised_amount <> 0;

-- 3. A donation counts only on payments-service's signed payment.succeeded
--    event (application fundraisers, reference type fundraiser_donation,
--    reference id = donations.id). The donate call writes a 'pending' row
--    that counts for nothing; the consumer moves it.
--
--    Rows written before this migration were recorded from a client-supplied
--    intent id with no payment check. They are labelled legacy_unverified,
--    not deleted and not uncounted: what to do with them is a data decision,
--    and the label makes them findable.
ALTER TABLE donations
    ADD COLUMN IF NOT EXISTS amount_paise BIGINT;
ALTER TABLE donations
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'legacy_unverified';
ALTER TABLE donations
    ADD COLUMN IF NOT EXISTS refunded_paise BIGINT NOT NULL DEFAULT 0;
ALTER TABLE donations
    ADD COLUMN IF NOT EXISTS intent_id TEXT;
ALTER TABLE donations
    ADD COLUMN IF NOT EXISTS counted_at TIMESTAMPTZ;
UPDATE donations
   SET amount_paise = ROUND(amount * 100)::BIGINT
 WHERE amount_paise IS NULL;
-- A pending donation has no intent until payments names one.
ALTER TABLE donations
    ALTER COLUMN payment_intent_id DROP NOT NULL;
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'donations_status_check') THEN
        ALTER TABLE donations ADD CONSTRAINT donations_status_check
            CHECK (status IN ('legacy_unverified', 'pending', 'succeeded', 'failed',
                              'partially_refunded', 'refunded'));
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_donations_fundraiser_counted
    ON donations (fundraiser_id, created_at DESC)
    WHERE status IN ('legacy_unverified', 'succeeded', 'partially_refunded');

-- The dedupe half of paymentevents.ApplyOnce for donations: one row per
-- payments event, inserted in the same transaction as its effect.
CREATE TABLE IF NOT EXISTS donation_payment_inbox (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    intent_id    TEXT,
    donation_id  UUID        NOT NULL,
    amount_minor BIGINT      NOT NULL,
    currency     TEXT,
    outcome      TEXT,
    detail       TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_donation_payment_inbox_donation
    ON donation_payment_inbox (donation_id, event_type);
