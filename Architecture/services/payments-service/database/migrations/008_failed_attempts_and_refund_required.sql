-- payments-service migration 008 — a failed attempt is not a failed payment
--
-- Razorpay lets a customer retry on the same order after a failed attempt.
-- The webhook used to finalise the intent FAILED on the first
-- `payment.failed`, so the retry's `payment.captured` was refused by the state
-- machine (failed never becomes succeeded): the customer was charged and the
-- order stayed failed.
--
-- A `payment.failed` webhook now only RECORDS the attempt. The record is the
-- provider inbox row it already wrote (payments.provider_events carries the
-- event type, order id, payment id and received_at), and the intent stays
-- pending. Only the reconciler finalises FAILED, once the order has no
-- captured or in-flight payment and has been quiet for
-- PAYMENTS_FAILED_ATTEMPT_WINDOW.
--
-- Expand-only: one partial index and one new table. Nothing an old writer
-- reads or writes changes shape, so a mixed fleet keeps working mid-rollout.

-- The reconciler's quiet-window lookup: the latest failed attempt on an order.
CREATE INDEX IF NOT EXISTS idx_provider_events_failed_attempts
    ON payments.provider_events (provider_order_id, received_at DESC)
    WHERE event_type = 'payment.failed';

-- A capture that arrived after its intent was already finalised FAILED (the
-- customer paid after the retry window closed). The money is at the provider
-- and nothing downstream will fulfil against it, so it is owed back.
--
-- payments does NOT call the provider's refund API for these automatically;
-- that is an explicit operator decision. This row is the durable, alarmable
-- record that the refund is owed. `resolved_at` / `resolution` are for the
-- operator who settles it.
--
-- One row per provider payment. Razorpay announces one capture under more
-- than one event id (payment.captured and order.paid), and redelivers either,
-- so the dedupe key is the payment, not the event.
CREATE TABLE IF NOT EXISTS payments.refund_required (
    id                  BIGSERIAL   PRIMARY KEY,
    intent_id           UUID        NOT NULL REFERENCES payments.payment_intents(id),
    provider            TEXT        NOT NULL,
    provider_order_id   TEXT,
    provider_payment_id TEXT        NOT NULL,
    event_id            TEXT        NOT NULL,
    amount_minor        BIGINT      NOT NULL DEFAULT 0,
    currency            TEXT,
    reason              TEXT        NOT NULL,
    detected_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at         TIMESTAMPTZ,
    resolution          TEXT,
    CONSTRAINT uq_refund_required_provider_payment UNIQUE (provider, provider_payment_id),
    CONSTRAINT chk_refund_required_payment_not_blank CHECK (length(btrim(provider_payment_id)) > 0)
);

CREATE INDEX IF NOT EXISTS idx_refund_required_unresolved
    ON payments.refund_required (detected_at)
    WHERE resolved_at IS NULL;
