-- 024: creator-membership correctness (audit 23 Oct 2026).
--
-- 1. One live subscription per (subscriber, creator). "Live" is every status
--    in which the subscription still belongs to the subscriber and may still
--    grant access: active, cancelled_at_period_end, paused, past_due, grace.
--    Until now only an application-side "SELECT then INSERT" stood between
--    two concurrent subscribe calls and two charges. The index makes the
--    second INSERT fail (the service answers 409 ALREADY_SUBSCRIBED) and its
--    transaction — charge included — roll back. Keep the status list in step
--    with postgres.LiveSubscriptionStatuses.
--
--    Existing duplicates would stop the index from building. They are
--    resolved first: the row with the latest period end is kept, every other
--    live row for the same pair is set to 'expired', and each one so moved is
--    recorded in subscription_events ('deduplicated'). No money moves.
WITH ranked AS (
    SELECT id, status,
           row_number() OVER (
               PARTITION BY subscriber_id, creator_id
               ORDER BY current_period_end DESC, created_at DESC, id DESC
           ) AS rn
    FROM subscriptions
    WHERE status IN ('active', 'cancelled_at_period_end', 'paused', 'past_due', 'grace')
), moved AS (
    UPDATE subscriptions s
    SET status = 'expired'
    FROM ranked r
    WHERE s.id = r.id AND r.rn > 1
    RETURNING s.id, r.status AS old_status
)
INSERT INTO subscription_events (subscription_id, event_type, old_status, new_status, metadata)
SELECT id, 'deduplicated', old_status, 'expired', jsonb_build_object('migration', '024_creator_membership_correctness')
FROM moved;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_one_live
    ON subscriptions (subscriber_id, creator_id)
    WHERE status IN ('active', 'cancelled_at_period_end', 'paused', 'past_due', 'grace');

-- 2. Idempotency keys belong to the subscriber who sent them. The 002 index
--    was global, so subscriber B reusing subscriber A's key was refused, and
--    the lookup (unscoped) handed A's subscription to B. Scope both.
DROP INDEX IF EXISTS idx_subscriptions_idempotency_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscriptions_subscriber_idempotency
    ON subscriptions (subscriber_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- 3. Scheduled downgrade: the tier (and its price, locked when the
--    downgrade was asked for) the next period renews on. Applied and
--    cleared by the renewal; no refund for the current period.
ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS pending_tier_id UUID REFERENCES creator_tiers(id),
    ADD COLUMN IF NOT EXISTS pending_price   BIGINT CHECK (pending_price IS NULL OR pending_price > 0);

-- 4. One row per renewed period. The renewal transaction inserts it BEFORE
--    it charges; a second attempt at the same period (a retry after a
--    failure, a second worker, a stale read) conflicts and charges nothing.
--    idempotency_key is 'renewal:<subscription id>:<period end, unix µs>'.
CREATE TABLE IF NOT EXISTS subscription_renewals (
    subscription_id UUID        NOT NULL REFERENCES subscriptions(id),
    period_end      TIMESTAMPTZ NOT NULL, -- the period end renewed FROM
    idempotency_key TEXT        NOT NULL UNIQUE,
    amount          BIGINT      NOT NULL CHECK (amount > 0),
    currency        TEXT        NOT NULL,
    new_period_end  TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subscription_id, period_end)
);

-- 5. The renewal and expiry workers' scans.
CREATE INDEX IF NOT EXISTS idx_subscriptions_renewal_due
    ON subscriptions (current_period_end)
    WHERE status IN ('active', 'past_due') AND auto_renew = TRUE;
CREATE INDEX IF NOT EXISTS idx_subscriptions_cancel_due
    ON subscriptions (current_period_end)
    WHERE status = 'cancelled_at_period_end';
