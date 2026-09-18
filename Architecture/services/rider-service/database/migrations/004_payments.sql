-- 004_payments.sql — Mopedu online payments through payments-service.
--
-- Re-runnable DDL (IF NOT EXISTS / DROP CONSTRAINT IF EXISTS). A ride is paid
-- in cash to the captain, or online (upi / card) through payments-service
-- (application `mopedu`, reference type `mopedu_ride`). Money is marked paid
-- ONLY from a signed payment.succeeded event applied once through
-- rider_payment_inbox; the intent-create echo and the advisory callback
-- verdict never do.

-- 1. Ride payments: the bound intent, the provider's reference, refunds ----
ALTER TABLE rider_ride_payments ADD COLUMN IF NOT EXISTS intent_id          UUID;
ALTER TABLE rider_ride_payments ADD COLUMN IF NOT EXISTS provider_reference TEXT;
ALTER TABLE rider_ride_payments ADD COLUMN IF NOT EXISTS refunded_paise     BIGINT NOT NULL DEFAULT 0 CHECK (refunded_paise >= 0);
ALTER TABLE rider_ride_payments ADD COLUMN IF NOT EXISTS failure_reason     TEXT;
ALTER TABLE rider_ride_payments ADD COLUMN IF NOT EXISTS updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW();

ALTER TABLE rider_ride_payments DROP CONSTRAINT IF EXISTS rider_ride_payments_status_check;
ALTER TABLE rider_ride_payments ADD CONSTRAINT rider_ride_payments_status_check
    CHECK (status IN ('pending_cash_confirmation','pending','confirming','succeeded','failed','refunded','partially_refunded'));

CREATE INDEX IF NOT EXISTS idx_rider_ride_payments_intent
    ON rider_ride_payments(intent_id) WHERE intent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_rider_ride_payments_status_created
    ON rider_ride_payments(status, created_at DESC);

-- 2. Payment inbox: one row per payments-service event, committed in the
--    same transaction as its effect (shared/paymentevents.ApplyOnce). ----
CREATE TABLE IF NOT EXISTS rider_payment_inbox (
    event_id     TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    intent_id    TEXT,
    reference_id UUID NOT NULL,
    amount_minor BIGINT NOT NULL,
    currency     TEXT,
    outcome      TEXT,
    detail       TEXT,
    applied_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_payment_inbox_reference
    ON rider_payment_inbox(reference_id, event_type);

-- 3. Refunds: an admin-requested refund of an online ride payment. Money
--    moves only when payment.refunded says so. ------------------------------
CREATE TABLE IF NOT EXISTS rider_ride_refunds (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id            UUID NOT NULL REFERENCES rider_rides(id),
    payment_id         UUID NOT NULL REFERENCES rider_ride_payments(id),
    intent_id          UUID NOT NULL,
    amount_paise       BIGINT NOT NULL CHECK (amount_paise > 0),
    reason             TEXT NOT NULL,
    status             TEXT NOT NULL DEFAULT 'requested'
                       CHECK (status IN ('requested','accepted','refunded','failed')),
    requested_by       UUID NOT NULL,
    provider_reference TEXT,
    failure_reason     TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_ride_refunds_ride
    ON rider_ride_refunds(ride_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_ride_refunds_status
    ON rider_ride_refunds(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_ride_refunds_intent
    ON rider_ride_refunds(intent_id);

-- 4. Outstanding fees paid directly: the open intent while pending, the
--    intent that settled the row once paid. ---------------------------------
ALTER TABLE rider_customer_outstanding ADD COLUMN IF NOT EXISTS intent_id         UUID;
ALTER TABLE rider_customer_outstanding ADD COLUMN IF NOT EXISTS intent_method     TEXT;
ALTER TABLE rider_customer_outstanding ADD COLUMN IF NOT EXISTS settled_intent_id UUID;
