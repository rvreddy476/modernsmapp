-- 002_escrow_holds.sql: Payment hold table for escrow-method payments
CREATE TABLE IF NOT EXISTS payments.payment_holds (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_intent_id UUID NOT NULL REFERENCES payments.payment_intents(id) ON DELETE CASCADE,
    hold_amount       BIGINT NOT NULL CHECK (hold_amount > 0),
    currency          TEXT NOT NULL DEFAULT 'INR',
    release_condition TEXT NOT NULL
                      CHECK (release_condition IN ('order_delivered', 'dispute_resolved', 'manual')),
    release_after     TIMESTAMPTZ,
    released_at       TIMESTAMPTZ,
    released_by       TEXT,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_holds_intent
    ON payments.payment_holds(payment_intent_id);

CREATE INDEX IF NOT EXISTS idx_holds_unreleased
    ON payments.payment_holds(released_at)
    WHERE released_at IS NULL;
