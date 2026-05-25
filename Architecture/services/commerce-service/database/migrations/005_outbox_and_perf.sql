-- HP1 + HP5: transactional outbox + perf indexes for commerce-service.
--
-- Outbox table matches the shared/outbox publisher contract: it polls
-- WHERE published_at IS NULL and stamps published_at after a successful
-- Kafka write. Same shape as food-service so the publisher works without
-- any service-specific code.
--
-- idempotency_key (HP5) is a defence against double-emit: if a service
-- crashes between domain-write and outbox-enqueue and the request is
-- retried, an idempotent caller can pass the same key and a UNIQUE
-- constraint blocks the second event. NULL means "no idempotency check"
-- (legacy callers / events that don't carry a natural key).
--
-- All statements idempotent; safe to re-run.

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    idempotency_key TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);

-- Hot index the publisher hits every poll (cfg.PollInterval defaults to
-- 500ms): partial-index on unpublished rows keeps it tiny even when the
-- table grows to millions of historical rows.
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox_events (id)
    WHERE published_at IS NULL;

-- HP5: idempotency dedup. Partial-unique so we only enforce uniqueness
-- when a caller actually supplies a key; null keys are legitimately
-- distinct rows (one event per legacy emit).
CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_idempotency_key
    ON outbox_events (idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- HP5: missing composite index for GetShipmentByOrderAndSeller, which is
-- called per-order inside the seller-fulfillment loop. The existing
-- idx_shipments_order is single-column, so a multi-shipment order forces
-- a sort to discriminate by seller_id. The composite makes the lookup
-- index-only.
CREATE INDEX IF NOT EXISTS idx_shipments_order_seller
    ON shipments (order_id, seller_id);

-- HP5: returns inbox sort. idx_returns_seller is (seller_id, status) but
-- the list query orders by requested_at DESC; without this index the
-- planner sorts in memory on every inbox page.
CREATE INDEX IF NOT EXISTS idx_returns_seller_requested
    ON return_requests (seller_id, requested_at DESC);
