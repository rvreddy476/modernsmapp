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
                       CHECK (status IN ('pending','processing','succeeded','failed','refunded','disputed')),
    provider_ref     TEXT,
    upi_intent_url   TEXT,
    metadata         JSONB DEFAULT '{}',
    idempotency_key  TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

ALTER TABLE payments.payment_intents
    ADD COLUMN IF NOT EXISTS upi_intent_url TEXT;

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
