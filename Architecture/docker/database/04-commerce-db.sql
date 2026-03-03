-- =============================================================================
-- COMMERCE_DB — Marketplace, Orders/Bookings, and Payments schemas
-- Run against: commerce_db
-- =============================================================================

\connect commerce_db;

-- ============================================================
-- marketplace schema — listings (v2.1)
-- ============================================================
CREATE SCHEMA IF NOT EXISTS marketplace;

CREATE TABLE IF NOT EXISTS marketplace.listings (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL,
    title           VARCHAR(200) NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    listing_type    TEXT NOT NULL DEFAULT 'product'
        CHECK (listing_type IN ('product', 'service')),
    price           NUMERIC(12,2) NOT NULL,
    currency        VARCHAR(3) NOT NULL DEFAULT 'INR',
    category        VARCHAR(100),
    subcategory     VARCHAR(100),
    media_ids       UUID[] NOT NULL DEFAULT '{}',
    tags            TEXT[] NOT NULL DEFAULT '{}',
    -- product-specific
    stock           INT,
    sku             TEXT,
    -- service-specific
    duration_mins   INT,
    availability_tz TEXT NOT NULL DEFAULT 'Asia/Kolkata',
    -- location
    lat             DOUBLE PRECISION,
    lng             DOUBLE PRECISION,
    city            TEXT,
    -- state
    status          TEXT NOT NULL DEFAULT 'draft'
        CHECK (status IN ('draft', 'active', 'paused', 'sold_out', 'archived')),
    -- chat-to-order link
    chat_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_listings_seller ON marketplace.listings (seller_id);
CREATE INDEX IF NOT EXISTS idx_listings_category ON marketplace.listings (category, listing_type) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_listings_location ON marketplace.listings (city) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_listings_created ON marketplace.listings (created_at DESC) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS marketplace.listing_reviews (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id   UUID NOT NULL REFERENCES marketplace.listings(id) ON DELETE CASCADE,
    reviewer_id  UUID NOT NULL,
    rating       INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review_text  TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(listing_id, reviewer_id)
);
CREATE INDEX IF NOT EXISTS idx_listing_reviews_listing ON marketplace.listing_reviews (listing_id, created_at DESC);

-- ============================================================
-- orders schema — orders, bookings, disputes (v2.1)
-- ============================================================
CREATE SCHEMA IF NOT EXISTS orders;

CREATE TABLE IF NOT EXISTS orders.orders (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_id          UUID NOT NULL,
    seller_id         UUID NOT NULL,
    listing_id        UUID REFERENCES marketplace.listings(id),
    status            TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','confirmed','shipped','delivered','completed','cancelled','disputed')),
    total             NUMERIC(12,2) NOT NULL,
    currency          VARCHAR(3) NOT NULL DEFAULT 'INR',
    shipping_address  JSONB,
    notes             TEXT NOT NULL DEFAULT '',
    payment_intent_id UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_orders_buyer ON orders.orders (buyer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_seller ON orders.orders (seller_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders.orders (status, created_at DESC);

CREATE TABLE IF NOT EXISTS orders.order_items (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES orders.orders(id) ON DELETE CASCADE,
    listing_id          UUID NOT NULL,
    title               TEXT NOT NULL,
    quantity            INT NOT NULL DEFAULT 1,
    price_at_purchase   NUMERIC(12,2) NOT NULL,
    currency            VARCHAR(3) NOT NULL DEFAULT 'INR'
);
CREATE INDEX IF NOT EXISTS idx_order_items_order ON orders.order_items (order_id);

CREATE TABLE IF NOT EXISTS orders.bookings (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id          UUID NOT NULL,
    provider_id          UUID NOT NULL,
    service_listing_id   UUID NOT NULL REFERENCES marketplace.listings(id),
    slot_start           TIMESTAMPTZ NOT NULL,
    slot_end             TIMESTAMPTZ NOT NULL,
    status               TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','confirmed','cancelled','completed','no_show')),
    payment_intent_id    UUID,
    notes                TEXT NOT NULL DEFAULT '',
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_bookings_customer ON orders.bookings (customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_bookings_provider ON orders.bookings (provider_id, slot_start);
CREATE INDEX IF NOT EXISTS idx_bookings_status ON orders.bookings (status, slot_start);

CREATE TABLE IF NOT EXISTS orders.booking_slots (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id    UUID NOT NULL,
    listing_id     UUID NOT NULL REFERENCES marketplace.listings(id) ON DELETE CASCADE,
    slot_start     TIMESTAMPTZ NOT NULL,
    slot_end       TIMESTAMPTZ NOT NULL,
    is_available   BOOLEAN NOT NULL DEFAULT TRUE,
    booking_id     UUID REFERENCES orders.bookings(id)
);
CREATE INDEX IF NOT EXISTS idx_booking_slots_provider ON orders.booking_slots (provider_id, slot_start) WHERE is_available = TRUE;
CREATE INDEX IF NOT EXISTS idx_booking_slots_listing ON orders.booking_slots (listing_id, slot_start);

CREATE TABLE IF NOT EXISTS orders.disputes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL REFERENCES orders.orders(id),
    opened_by     UUID NOT NULL,
    reason        TEXT NOT NULL,
    status        TEXT NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','under_review','resolved','closed')),
    resolution    TEXT,
    evidence_urls TEXT[] NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_disputes_order ON orders.disputes (order_id);
CREATE INDEX IF NOT EXISTS idx_disputes_status ON orders.disputes (status, created_at DESC);

-- ============================================================
-- payments schema (v2.1)
-- ============================================================
CREATE SCHEMA IF NOT EXISTS payments;

CREATE TABLE IF NOT EXISTS payments.payment_intents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payer_id         UUID NOT NULL,
    payee_id         UUID NOT NULL,
    reference_type   TEXT NOT NULL CHECK (reference_type IN ('order','booking','subscription','tip')),
    reference_id     UUID NOT NULL,
    amount           NUMERIC(12,2) NOT NULL,
    currency         VARCHAR(3) NOT NULL DEFAULT 'INR',
    method           TEXT NOT NULL
        CHECK (method IN ('upi','card','wallet','cod','escrow')),
    status           TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','processing','succeeded','failed','refunded','disputed')),
    provider_ref     TEXT,
    upi_intent_url   TEXT,
    metadata         JSONB NOT NULL DEFAULT '{}',
    idempotency_key  TEXT NOT NULL UNIQUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_payment_intents_reference ON payments.payment_intents (reference_type, reference_id);
CREATE INDEX IF NOT EXISTS idx_payment_intents_payer ON payments.payment_intents (payer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_payment_intents_status ON payments.payment_intents (status, created_at DESC);

CREATE TABLE IF NOT EXISTS payments.payment_audit_log (
    id           BIGSERIAL PRIMARY KEY,
    intent_id    UUID NOT NULL REFERENCES payments.payment_intents(id),
    event        TEXT NOT NULL,
    old_status   TEXT,
    new_status   TEXT,
    actor_id     UUID,
    metadata     JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_payment_audit_intent ON payments.payment_audit_log (intent_id, created_at DESC);

-- ============================================================
-- outbox + inbox for commerce_db
-- ============================================================
CREATE TABLE IF NOT EXISTS commerce_outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    schema_name   TEXT NOT NULL,
    event_type    TEXT NOT NULL,
    partition_key TEXT NOT NULL DEFAULT '',
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_commerce_outbox_unpublished ON commerce_outbox_events(id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS commerce_inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      UUID NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);
