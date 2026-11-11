-- 027: Momentum Premium, phase 0 (short-posts design 3.1-3.3, founder took
-- every default on 11 Nov 2026). Built switched OFF: PREMIUM_ENABLED=false.
--
-- One platform plan sold as one-off passes of 30 and 365 days. No auto-renew:
-- renewal is a reminder with a pay button, as for creator memberships. Money
-- moves only through payments-service (application `premium`, reference type
-- platform_premium, one intent per premium_periods row), and a pass is
-- granted ONLY from the signed payment.succeeded event, applied once through
-- premium_payment_inbox. Prices are NOT stored here: they are configuration
-- (PREMIUM_PRICE_30D_PAISE / PREMIUM_PRICE_365D_PAISE) and a plan without a
-- price cannot be bought. A period row keeps the amount it was sold at.
--
-- Access: the identity role `premium`, held until the paid period ends plus a
-- 3-day grace (expires_at), reconciled from premium_entitlements by the role
-- sync worker. Stacking a second pass extends from the current end.

-- 1. The catalogue. A plan is a code and a length; the founder sets prices.
CREATE TABLE IF NOT EXISTS premium_plans (
    code          TEXT        PRIMARY KEY,
    name          TEXT        NOT NULL,
    duration_days INT         NOT NULL CHECK (duration_days > 0),
    active        BOOLEAN     NOT NULL DEFAULT TRUE,
    sort_order    INT         NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO premium_plans (code, name, duration_days, sort_order) VALUES
    ('premium_30d',  'Momentum Premium, 30 days',  30,  1),
    ('premium_365d', 'Momentum Premium, 365 days', 365, 2)
ON CONFLICT (code) DO NOTHING;

-- 2. One row per pass bought: its id is the payments reference id, its
--    idempotency_key the payments intent key (platform_premium:<id>).
--    period_start / period_end are set by the signed event (the pass starts
--    at the buyer's current end, or now) and may be moved earlier when an
--    earlier pass in the same chain is refunded.
--    Status: created (intent open) | abandoned (unpaid past 30 minutes or
--    replaced; a capture that still arrives activates it) | paid | failed |
--    refunded (fully; partial refunds are recorded in refunded_paise and
--    leave the pass in place).
CREATE TABLE IF NOT EXISTS premium_periods (
    id                UUID        PRIMARY KEY,
    user_id           UUID        NOT NULL,
    plan_code         TEXT        NOT NULL REFERENCES premium_plans(code),
    duration_days     INT         NOT NULL CHECK (duration_days > 0),
    amount_paise      BIGINT      NOT NULL CHECK (amount_paise > 0),
    currency          TEXT        NOT NULL DEFAULT 'INR',
    status            TEXT        NOT NULL DEFAULT 'created'
                      CHECK (status IN ('created', 'abandoned', 'paid', 'failed', 'refunded')),
    intent_id         UUID,
    provider_order_id TEXT,
    period_start      TIMESTAMPTZ,
    period_end        TIMESTAMPTZ,
    refunded_paise    BIGINT      NOT NULL DEFAULT 0 CHECK (refunded_paise >= 0),
    idempotency_key   TEXT        NOT NULL UNIQUE,
    paid_at           TIMESTAMPTZ,
    failed_at         TIMESTAMPTZ,
    refunded_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (status <> 'paid' OR (period_start IS NOT NULL AND period_end IS NOT NULL AND period_end > period_start))
);

-- One open checkout per (user, plan): a retried checkout reuses it, so a
-- double tap opens one intent, not two.
CREATE UNIQUE INDEX IF NOT EXISTS idx_premium_periods_one_open
    ON premium_periods (user_id, plan_code)
    WHERE status = 'created';
CREATE INDEX IF NOT EXISTS idx_premium_periods_user
    ON premium_periods (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_premium_periods_open_sweep
    ON premium_periods (created_at)
    WHERE status = 'created';

-- 3. One row per buyer: what they hold and what identity has been told.
--    ends_at       end of the paid coverage (the latest paid period_end).
--    access_until  when the role must stop: ends_at + 3 days of grace; a
--                  refund that leaves no coverage sets it to the refund time.
--    role_synced_until  the expires_at identity last accepted for `premium`
--                  (NULL: never granted, or revoked). The role sync worker
--                  works every row where this disagrees with access_until.
CREATE TABLE IF NOT EXISTS premium_entitlements (
    user_id              UUID        PRIMARY KEY,
    ends_at              TIMESTAMPTZ NOT NULL,
    access_until         TIMESTAMPTZ NOT NULL,
    role_synced_until    TIMESTAMPTZ,
    role_attempts        INT         NOT NULL DEFAULT 0,
    role_next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    role_last_error      TEXT,
    ended_notified_until TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_premium_entitlements_role_due
    ON premium_entitlements (role_next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_premium_entitlements_ends
    ON premium_entitlements (ends_at);

-- 4. The dedupe half of paymentevents.ApplyOnce for application premium.
CREATE TABLE IF NOT EXISTS premium_payment_inbox (
    event_id     TEXT        PRIMARY KEY,
    event_type   TEXT        NOT NULL,
    intent_id    TEXT,
    period_id    UUID        NOT NULL,
    amount_minor BIGINT      NOT NULL,
    currency     TEXT,
    outcome      TEXT,
    detail       TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_premium_payment_inbox_period
    ON premium_payment_inbox (period_id, event_type);

-- 5. Renewal reminders: once per (user, ends_at, days_left), 3 days before
--    the end and on the day.
CREATE TABLE IF NOT EXISTS premium_renewal_reminders (
    user_id   UUID        NOT NULL,
    ends_at   TIMESTAMPTZ NOT NULL,
    days_left SMALLINT    NOT NULL CHECK (days_left IN (0, 3)),
    sent_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, ends_at, days_left)
);
