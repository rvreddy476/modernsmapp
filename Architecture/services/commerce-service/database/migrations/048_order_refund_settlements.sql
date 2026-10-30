-- 048 — every settled refund of an order, one row per provider refund.
--
-- Audit 2026-10-30 M2. SettleRefund used to mark the order fully refunded
-- and close every open refund command on ANY payment.refunded naming the
-- order, whatever its amount, so a ₹200 partial refund made from the provider
-- dashboard closed a pending ₹2,000 cancel refund and the ₹1,800 still owed
-- was never sent. It also kept no record, so a redelivered event wrote a
-- second commerce.order.refunded.
--
-- This table is that record. The cumulative refunded amount of an order is
-- the SUM of its rows, and the order is fully refunded only when that sum
-- covers what was paid. refund_id is payments' provider_refund_id
-- (manual:<command_id> for an operator's manual resolution), unique, so one
-- refund counts once even if it is ever published under two event ids; the
-- event id itself is deduped by payment_event_inbox in the same transaction.
--
-- Expand-only: a new table nothing older writes.

CREATE TABLE IF NOT EXISTS order_refund_settlements (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL REFERENCES orders(id),
    refund_id     TEXT NOT NULL UNIQUE CHECK (length(btrim(refund_id)) > 0),
    event_id      TEXT NOT NULL,
    intent_id     TEXT,
    amount_minor  BIGINT NOT NULL CHECK (amount_minor > 0),
    -- payments' intent status after this refund (refunded | partially_refunded).
    -- Recorded for reconciliation; commerce decides from the cumulative sum.
    provider_status TEXT,
    -- The commerce refund command this refund settled, when one matched.
    command_id    UUID REFERENCES order_refund_commands(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_refund_settlements_order
    ON order_refund_settlements (order_id);
