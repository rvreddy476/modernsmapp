-- Webhook-driven partial-refund settlement (the P6/P7 follow-up the
-- partial-refund commit flagged).
--
-- payments-service's webhook handler previously had a single line
-- mapping refund.processed → UpdateStatusByProviderRef("refunded"),
-- which (a) didn't carry the refund amount, so a partial Razorpay
-- refund got booked as a full one, and (b) tried to transition
-- partially_refunded → refunded unconditionally — the state machine
-- now correctly rejects that when refunded_amount_minor hasn't
-- caught up to intent_amount_minor.
--
-- The new flow uses ApplyRefund(intent, amount_minor), which is the
-- same primitive InitiateRefund (the user-initiated path) uses. To
-- keep it idempotent under Razorpay's retry + manual-replay behavior
-- (which can re-deliver the same refund event with a fresh event_id),
-- this table records each refund_provider_ref the first time we see
-- it. A second webhook carrying the same refund id ON CONFLICT skips.
-- That dedup is per-refund, not per-webhook-event — the existing
-- webhook_events dedup only catches identical event_ids.

CREATE TABLE IF NOT EXISTS payments.refunds_applied (
    refund_provider_ref TEXT PRIMARY KEY,
    intent_id           UUID NOT NULL,
    amount_minor        BIGINT NOT NULL CHECK (amount_minor > 0),
    applied_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_refunds_applied_intent
    ON payments.refunds_applied(intent_id, applied_at DESC);
