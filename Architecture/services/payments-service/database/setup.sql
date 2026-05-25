-- Payments Service Schema
-- Database: commerce_db

CREATE SCHEMA IF NOT EXISTS payments;

CREATE TABLE IF NOT EXISTS payments.payment_intents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payer_id         UUID NOT NULL,
    payee_id         UUID NOT NULL,
    reference_type   TEXT NOT NULL,
    reference_id     UUID NOT NULL,
    amount           NUMERIC(12,2) NOT NULL,
    currency         TEXT NOT NULL DEFAULT 'INR',
    method           TEXT NOT NULL CHECK (method IN ('upi','card','wallet','cod','escrow')),
    status           TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','processing','succeeded','failed','refunded','partially_refunded','disputed')),
    provider_ref     TEXT,
    upi_intent_url   TEXT,
    metadata         JSONB DEFAULT '{}',
    idempotency_key  TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS upi_intent_url TEXT;

-- Audit P6 + P7: partial-refund tracking. Counted in paise-minor so a
-- float64 rupees boundary on the API doesn't bleed precision into the
-- refunded total. Re-creates the status CHECK so older deploys that
-- still have the 6-status constraint accept `partially_refunded`.
ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS refunded_amount_minor BIGINT NOT NULL DEFAULT 0;

DO $$
DECLARE
    r RECORD;
BEGIN
    FOR r IN
        SELECT conname FROM pg_constraint
        WHERE conrelid = 'payments.payment_intents'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%status%'
          AND pg_get_constraintdef(oid) NOT LIKE '%partially_refunded%'
    LOOP
        EXECUTE format('ALTER TABLE payments.payment_intents DROP CONSTRAINT %I', r.conname);
    END LOOP;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'payments.payment_intents'::regclass
          AND contype = 'c'
          AND pg_get_constraintdef(oid) LIKE '%partially_refunded%'
    ) THEN
        ALTER TABLE payments.payment_intents
            ADD CONSTRAINT payment_intents_status_check
            CHECK (status IN ('pending','processing','succeeded','failed','refunded','partially_refunded','disputed'));
    END IF;
END$$;

CREATE INDEX IF NOT EXISTS idx_payment_intents_reference
    ON payments.payment_intents (reference_type, reference_id);
CREATE INDEX IF NOT EXISTS idx_payment_intents_payer
    ON payments.payment_intents (payer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payment_intents_status
    ON payments.payment_intents (status) WHERE status IN ('pending','processing');

CREATE TABLE IF NOT EXISTS payments.payment_audit_log (
    id          BIGSERIAL PRIMARY KEY,
    intent_id   UUID NOT NULL REFERENCES payments.payment_intents(id),
    event       TEXT NOT NULL,
    old_status  TEXT,
    new_status  TEXT,
    actor_id    UUID,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_payment_audit_intent
    ON payments.payment_audit_log (intent_id, created_at DESC);

-- Outbox for transactional event publishing
CREATE TABLE IF NOT EXISTS payments.outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_payments_outbox_unpublished
    ON payments.outbox_events(id) WHERE published_at IS NULL;

-- Audit P3: webhook idempotency. Razorpay retries deliveries; without
-- this table every retry re-runs the state-machine update and re-
-- publishes the Kafka event. The handler now SELECT-INSERTs each
-- event_id and short-circuits when ON CONFLICT DO NOTHING returns 0
-- rows affected.
CREATE TABLE IF NOT EXISTS payments.webhook_events (
    event_id     TEXT PRIMARY KEY,
    event_type   TEXT NOT NULL,
    provider_ref TEXT,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_webhook_events_received_at
    ON payments.webhook_events(received_at DESC);

-- Refund-level idempotency (migration 005). Keyed by Razorpay refund
-- id (not webhook event id) so a second webhook carrying the same
-- refund id silently skips. The webhook handler INSERTs ... ON
-- CONFLICT DO NOTHING and skips ApplyRefund when rows affected = 0.
CREATE TABLE IF NOT EXISTS payments.refunds_applied (
    refund_provider_ref TEXT PRIMARY KEY,
    intent_id           UUID NOT NULL,
    amount_minor        BIGINT NOT NULL CHECK (amount_minor > 0),
    applied_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_refunds_applied_intent
    ON payments.refunds_applied(intent_id, applied_at DESC);
