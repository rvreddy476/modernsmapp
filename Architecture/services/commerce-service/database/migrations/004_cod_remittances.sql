-- COD remittance ledger. One row per (shipment, seller) created when the
-- courier confirms delivery of a COD shipment. Tracks the cash collected
-- by the courier and what the seller is owed after platform commission +
-- fees + TDS.
--
-- Lifecycle:
--   pending  — courier has confirmed delivery; cash is in transit
--   settled  — funds remitted to the seller's payout account
--   on_hold  — flagged for manual review (chargeback, dispute, etc.)
--
-- Idempotent — partial-deploy safe.

CREATE TABLE IF NOT EXISTS cod_remittances (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shipment_id        UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
    order_id           UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    seller_id          UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    gross_amount       NUMERIC(12,2) NOT NULL,
    commission_amount  NUMERIC(12,2) NOT NULL DEFAULT 0,
    platform_fee       NUMERIC(12,2) NOT NULL DEFAULT 0,
    tds_amount         NUMERIC(12,2) NOT NULL DEFAULT 0,
    net_amount         NUMERIC(12,2) NOT NULL,
    currency_code      TEXT NOT NULL DEFAULT 'INR',
    status             TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','settled','on_hold')),
    delivered_at       TIMESTAMPTZ NOT NULL,
    settled_at         TIMESTAMPTZ,
    payout_batch_id    UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- One remittance per shipment — second delivery webhook for the same
    -- shipment is a no-op (idempotent ingest from courier retries).
    UNIQUE (shipment_id)
);

CREATE INDEX IF NOT EXISTS idx_cod_remittances_seller_status
    ON cod_remittances (seller_id, status, delivered_at DESC);

CREATE INDEX IF NOT EXISTS idx_cod_remittances_payout_batch
    ON cod_remittances (payout_batch_id) WHERE payout_batch_id IS NOT NULL;
