-- Returns & Refunds Workflow
CREATE TABLE IF NOT EXISTS return_requests (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL REFERENCES orders.orders(id),
    buyer_id        UUID NOT NULL,
    reason          TEXT NOT NULL CHECK (reason IN ('wrong_item','damaged','not_as_described','changed_mind','never_arrived')),
    description     TEXT NOT NULL DEFAULT '',
    evidence_urls   TEXT[] NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested','approved','rejected','item_received','refunded','completed')),
    return_tracking TEXT,
    seller_note     TEXT,
    refund_amount   NUMERIC(12,2),
    refund_method   TEXT CHECK (refund_method IN ('original_payment','store_credit','upi')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_returns_order ON return_requests(order_id);
CREATE INDEX IF NOT EXISTS idx_returns_buyer ON return_requests(buyer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_returns_status ON return_requests(status, created_at DESC);
