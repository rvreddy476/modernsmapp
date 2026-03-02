CREATE SCHEMA IF NOT EXISTS shop;

CREATE TABLE IF NOT EXISTS shop.products (
    id UUID PRIMARY KEY,
    seller_id UUID NOT NULL,
    title VARCHAR(200) NOT NULL,
    description TEXT,
    price NUMERIC(12,2) NOT NULL,
    currency VARCHAR(3) DEFAULT 'INR',
    category VARCHAR(50),
    media_ids UUID[] DEFAULT '{}',
    stock INT DEFAULT 0,
    status VARCHAR(20) DEFAULT 'draft', -- draft, active, sold_out, archived
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_products_seller ON shop.products (seller_id);
CREATE INDEX IF NOT EXISTS idx_products_category ON shop.products (category) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_created ON shop.products (created_at DESC) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS shop.cart_items (
    user_id UUID NOT NULL,
    product_id UUID NOT NULL REFERENCES shop.products(id),
    quantity INT DEFAULT 1,
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, product_id)
);

CREATE TABLE IF NOT EXISTS shop.orders (
    id UUID PRIMARY KEY,
    buyer_id UUID NOT NULL,
    seller_id UUID NOT NULL,
    status VARCHAR(20) DEFAULT 'pending', -- pending, confirmed, shipped, delivered, cancelled
    total NUMERIC(12,2) NOT NULL,
    currency VARCHAR(3) DEFAULT 'INR',
    shipping_address JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_buyer ON shop.orders (buyer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_seller ON shop.orders (seller_id, created_at DESC);

CREATE TABLE IF NOT EXISTS shop.order_items (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES shop.orders(id),
    product_id UUID NOT NULL REFERENCES shop.products(id),
    quantity INT NOT NULL DEFAULT 1,
    price_at_purchase NUMERIC(12,2) NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_order_items_order ON shop.order_items (order_id);
