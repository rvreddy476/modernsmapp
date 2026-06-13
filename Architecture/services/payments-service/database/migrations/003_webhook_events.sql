-- Audit P3: webhook idempotency.
--
-- Razorpay retries webhook deliveries until they get a 2xx response.
-- Before this table, every retry re-processed the event: it called
-- UpdateStatusByProviderRef again (a no-op now that the state machine
-- is enforced) and re-published the corresponding Kafka event, so
-- downstream consumers (commerce-service) saw duplicate
-- payment.captured / payment.refunded events.
--
-- The handler now SELECT-INSERTs each event_id into this table before
-- doing anything. If the row already existed (ON CONFLICT DO NOTHING
-- → 0 rows affected), the retry is silently acked with 200.

CREATE TABLE IF NOT EXISTS payments.webhook_events (
    event_id      TEXT PRIMARY KEY,
    event_type    TEXT NOT NULL,
    provider_ref  TEXT,
    received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_webhook_events_received_at
    ON payments.webhook_events(received_at DESC);
