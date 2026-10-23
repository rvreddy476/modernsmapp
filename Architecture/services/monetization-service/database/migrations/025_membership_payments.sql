-- 025: creator-membership money moves onto payments-service (phase 1
-- contract, pinned 23 Oct 2026).
--
-- Until now a join, a renewal and an upgrade debited the subscriber's
-- creator_ledger row and credited the creator's, inside this database. That
-- path is gone. A membership is paid through payments-service (application
-- `memberships`, reference type creator_membership); access is granted ONLY
-- from the signed payment.succeeded event, applied once through
-- membership_payment_inbox. Renewal is by reminder: the member taps Pay each
-- month; nothing is charged automatically.

-- 1. Subscription states. `pending_payment` is a join waiting for its
--    payment. It is NOT live: it grants nothing and is outside
--    idx_subscriptions_one_live. A separate partial unique index allows at
--    most one pending join per (subscriber, creator); a stale one (> 30 min)
--    is replaced by the next join or swept to expired.
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_status_check;
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_status_check
    CHECK (status IN ('pending_payment','active','past_due','grace','paused','cancelled',
                      'cancelled_at_period_end','expired','payment_failed'));

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_one_pending
    ON subscriptions (subscriber_id, creator_id)
    WHERE status = 'pending_payment';
CREATE INDEX IF NOT EXISTS idx_subscriptions_pending_sweep
    ON subscriptions (created_at)
    WHERE status = 'pending_payment';
CREATE INDEX IF NOT EXISTS idx_subscriptions_period_end_live
    ON subscriptions (current_period_end)
    WHERE status IN ('active', 'past_due', 'grace', 'paused', 'cancelled_at_period_end');

-- 2. activated_at: when the membership was first paid for (a pending join has
--    none and can never be renewed). counted: this row is counted in its
--    tier's subscriber_count. The count moves +1 on first activation and -1
--    on expiry or an immediate cancel, keyed on this flag, so it can no
--    longer drift (before: +1 at subscribe, -1 only on unsubscribe; grace
--    and period-end expiry never decremented).
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS activated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS counted      BOOLEAN NOT NULL DEFAULT FALSE;

UPDATE subscriptions
   SET activated_at = COALESCE(current_period_start, created_at)
 WHERE activated_at IS NULL AND status <> 'pending_payment';
UPDATE subscriptions
   SET counted = (status IN ('active', 'cancelled_at_period_end', 'paused', 'past_due', 'grace'));
-- Recount every tier from the flag: this fixes the existing drift.
UPDATE creator_tiers t
   SET subscriber_count = COALESCE((SELECT COUNT(*) FROM subscriptions s WHERE s.tier_id = t.id AND s.counted), 0),
       updated_at = NOW();

-- 3. One row per paid period: a first join, each renewal, each upgrade
--    difference. Its id is the payments reference id. idempotency_key is the
--    payments intent key (creator_membership:<id>), so a retry collapses to
--    one intent.
--
--    One live row per (subscription, kind, period_start): a second payment
--    for the same period cannot be opened. A REFUNDED row and a late capture
--    (money that arrived for a subscription that could no longer use it, and
--    is refunded) do not hold the period: their money is going back, so the
--    period may be bought again. Status: created (intent open) | confirming
--    (the client's callback verified, advisory) | paid | failed | refunded.
CREATE TABLE IF NOT EXISTS membership_payments (
    id                    UUID        PRIMARY KEY,
    subscription_id       UUID        NOT NULL REFERENCES subscriptions(id),
    payer_id              UUID        NOT NULL,
    creator_id            UUID        NOT NULL,
    kind                  TEXT        NOT NULL CHECK (kind IN ('join', 'renewal', 'upgrade')),
    tier_id               UUID        NOT NULL REFERENCES creator_tiers(id),
    tier_name             TEXT        NOT NULL,
    tier_price_paise      BIGINT      NOT NULL CHECK (tier_price_paise > 0),
    from_tier_id          UUID        REFERENCES creator_tiers(id),
    from_tier_name        TEXT        NOT NULL DEFAULT '',
    from_price_paise      BIGINT      NOT NULL DEFAULT 0,
    amount_paise          BIGINT      NOT NULL CHECK (amount_paise > 0),
    fee_paise             BIGINT      NOT NULL DEFAULT 0 CHECK (fee_paise >= 0),
    creator_share_paise   BIGINT      NOT NULL CHECK (creator_share_paise >= 0),
    currency              TEXT        NOT NULL DEFAULT 'INR',
    period_start          TIMESTAMPTZ NOT NULL,
    period_end            TIMESTAMPTZ NOT NULL,
    granted_start         TIMESTAMPTZ,
    granted_end           TIMESTAMPTZ,
    intent_id             UUID,
    provider_order_id     TEXT,
    status                TEXT        NOT NULL DEFAULT 'created'
                          CHECK (status IN ('created', 'confirming', 'paid', 'failed', 'refunded')),
    failure_reason        TEXT,
    late_capture          BOOLEAN     NOT NULL DEFAULT FALSE,
    refund_status         TEXT        CHECK (refund_status IS NULL OR refund_status IN ('requested', 'pending', 'refunded', 'failed')),
    refund_cause          TEXT,
    refund_key            TEXT        UNIQUE,
    refund_paise          BIGINT      NOT NULL DEFAULT 0 CHECK (refund_paise >= 0),
    refunded_paise        BIGINT      NOT NULL DEFAULT 0 CHECK (refunded_paise >= 0),
    refund_reason         TEXT,
    refund_requested_by   UUID,
    refund_command_id     UUID,
    refund_attempts       INT         NOT NULL DEFAULT 0,
    refund_next_attempt_at TIMESTAMPTZ,
    refund_last_error     TEXT,
    idempotency_key       TEXT        NOT NULL UNIQUE,
    paid_at               TIMESTAMPTZ,
    failed_at             TIMESTAMPTZ,
    refunded_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (fee_paise + creator_share_paise = amount_paise),
    CHECK (kind <> 'upgrade' OR from_tier_id IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_membership_payments_one_per_period
    ON membership_payments (subscription_id, kind, period_start)
    WHERE status <> 'refunded' AND NOT late_capture;
CREATE INDEX IF NOT EXISTS idx_membership_payments_subscription
    ON membership_payments (subscription_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_membership_payments_refund_due
    ON membership_payments (refund_next_attempt_at)
    WHERE refund_status = 'requested';

-- 4. The dedupe half of paymentevents.ApplyOnce: one row per payments event,
--    inserted in the same transaction as its effect.
CREATE TABLE IF NOT EXISTS membership_payment_inbox (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    intent_id    TEXT,
    payment_id   UUID        NOT NULL,
    amount_minor BIGINT      NOT NULL,
    currency     TEXT,
    outcome      TEXT,
    detail       TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_membership_payment_inbox_payment
    ON membership_payment_inbox (payment_id, event_type);

-- 5. Renewal reminders: once per (subscription, period end, days_left), 3 days
--    before the period end and on the day.
CREATE TABLE IF NOT EXISTS membership_renewal_reminders (
    subscription_id UUID        NOT NULL REFERENCES subscriptions(id),
    period_end      TIMESTAMPTZ NOT NULL,
    days_left       SMALLINT    NOT NULL CHECK (days_left IN (0, 3)),
    sent_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subscription_id, period_end, days_left)
);
