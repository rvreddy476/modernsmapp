-- 002_invoices_shipments.sql — invoices, shipments, and COD support

-- Invoice sequence per financial year
CREATE TABLE IF NOT EXISTS invoice_sequences (
    financial_year TEXT PRIMARY KEY,
    last_sequence  BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Invoices (one per order; one seller per order in MVP)
CREATE TABLE IF NOT EXISTS invoices (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL UNIQUE,
    invoice_number  TEXT NOT NULL UNIQUE,
    financial_year  TEXT NOT NULL,
    sequence        BIGINT NOT NULL,
    seller_id       UUID NOT NULL,
    buyer_user_id   UUID NOT NULL,
    grand_total     NUMERIC(12,2) NOT NULL,
    currency_code   TEXT NOT NULL DEFAULT 'INR',
    is_interstate   BOOLEAN NOT NULL DEFAULT FALSE,
    cgst_total      NUMERIC(12,2) NOT NULL DEFAULT 0,
    sgst_total      NUMERIC(12,2) NOT NULL DEFAULT 0,
    igst_total      NUMERIC(12,2) NOT NULL DEFAULT 0,
    html_media_key  TEXT,       -- key in MinIO where rendered HTML lives
    pdf_media_key   TEXT,       -- future: PDF path
    issued_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invoices_buyer ON invoices(buyer_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_invoices_seller ON invoices(seller_id, created_at DESC);

-- Shipments (outbound courier shipments per order)
CREATE TABLE IF NOT EXISTS shipments (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id           UUID NOT NULL,
    seller_id          UUID NOT NULL,
    courier            TEXT NOT NULL,       -- 'shiprocket' | 'delhivery' | 'manual'
    tracking_number    TEXT,                -- AWB / consignment number
    courier_order_id   TEXT,                -- courier-side reference
    label_url          TEXT,                -- shipping label PDF
    tracking_url       TEXT,                -- public tracking page
    status             TEXT NOT NULL DEFAULT 'booked',
                                            -- booked | picked_up | in_transit | out_for_delivery | delivered | rto_initiated | rto_delivered | lost
    eta                TIMESTAMPTZ,
    shipped_at         TIMESTAMPTZ,
    delivered_at       TIMESTAMPTZ,
    last_event_at      TIMESTAMPTZ,
    raw_payload        JSONB,               -- last webhook payload (debugging)
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (courier, tracking_number)
);

CREATE INDEX IF NOT EXISTS idx_shipments_order ON shipments(order_id);
CREATE INDEX IF NOT EXISTS idx_shipments_seller ON shipments(seller_id, status);

-- Shipment events (full tracking timeline)
CREATE TABLE IF NOT EXISTS shipment_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shipment_id  UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    location     TEXT,
    remark       TEXT,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_shipment_events_shipment ON shipment_events(shipment_id, occurred_at DESC);

-- COD collection tracking (money collected by courier, owed to seller)
CREATE TABLE IF NOT EXISTS cod_collections (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL UNIQUE,
    shipment_id   UUID REFERENCES shipments(id) ON DELETE SET NULL,
    amount        NUMERIC(12,2) NOT NULL,
    currency_code TEXT NOT NULL DEFAULT 'INR',
    status        TEXT NOT NULL DEFAULT 'pending', -- pending | collected | remitted | failed
    collected_at  TIMESTAMPTZ,
    remitted_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
