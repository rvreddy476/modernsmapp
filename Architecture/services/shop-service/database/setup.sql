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

-- ─── Seller Storefronts ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS storefronts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL,
    handle          TEXT NOT NULL UNIQUE,
    display_name    TEXT NOT NULL,
    tagline         TEXT,
    banner_media_id UUID,
    logo_media_id   UUID,
    about           TEXT,
    policies        JSONB,
    is_verified     BOOLEAN NOT NULL DEFAULT FALSE,
    total_sales     BIGINT NOT NULL DEFAULT 0,
    avg_rating      REAL NOT NULL DEFAULT 0,
    review_count    INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_storefronts_seller ON storefronts(seller_id);
CREATE INDEX IF NOT EXISTS idx_storefronts_handle ON storefronts(handle);

CREATE TABLE IF NOT EXISTS storefront_featured (
    storefront_id   UUID NOT NULL REFERENCES storefronts(id) ON DELETE CASCADE,
    listing_id      UUID NOT NULL,
    position        INT NOT NULL,
    PRIMARY KEY (storefront_id, position)
);

CREATE TABLE IF NOT EXISTS storefront_collections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    storefront_id   UUID NOT NULL REFERENCES storefronts(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    cover_media_id  UUID,
    sort_order      INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS collection_listings (
    collection_id   UUID NOT NULL REFERENCES storefront_collections(id) ON DELETE CASCADE,
    listing_id      UUID NOT NULL,
    position        INT NOT NULL,
    PRIMARY KEY (collection_id, position)
);

-- ─── Product Tagging in Posts ────────────────────────────────────────
CREATE TABLE IF NOT EXISTS post_product_tags (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    post_id      UUID NOT NULL,
    listing_id   UUID NOT NULL,
    position     JSONB,
    appear_at_ms INT,
    hide_at_ms   INT,
    click_count  BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_product_tags_post ON post_product_tags(post_id);
CREATE INDEX IF NOT EXISTS idx_product_tags_listing ON post_product_tags(listing_id);

-- ─── Wishlists ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS wishlists (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    name       TEXT NOT NULL DEFAULT 'My Wishlist',
    is_public  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wishlists_user ON wishlists(user_id);

CREATE TABLE IF NOT EXISTS wishlist_items (
    wishlist_id UUID NOT NULL REFERENCES wishlists(id) ON DELETE CASCADE,
    listing_id  UUID NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (wishlist_id, listing_id)
);
CREATE INDEX IF NOT EXISTS idx_wishlist_items_listing ON wishlist_items(listing_id);

CREATE TABLE IF NOT EXISTS stock_alerts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    listing_id UUID NOT NULL,
    alerted    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, listing_id)
);

-- ─── Group Buying ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS group_buys (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    listing_id       UUID NOT NULL,
    initiator_id     UUID NOT NULL,
    target_qty       INT NOT NULL,
    current_qty      INT NOT NULL DEFAULT 1,
    discounted_price NUMERIC(12,2) NOT NULL,
    original_price   NUMERIC(12,2) NOT NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    status           TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','fulfilled','expired','cancelled')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_group_buys_listing ON group_buys(listing_id, status);

CREATE TABLE IF NOT EXISTS group_buy_participants (
    group_buy_id      UUID NOT NULL REFERENCES group_buys(id) ON DELETE CASCADE,
    user_id           UUID NOT NULL,
    payment_intent_id UUID,
    joined_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_buy_id, user_id)
);

-- ─── Ads ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS ad_campaigns (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    advertiser_id           UUID NOT NULL,
    name                    TEXT NOT NULL,
    objective               TEXT NOT NULL CHECK (objective IN ('awareness','reach','engagement','traffic','conversion','lead_gen','app_install')),
    status                  TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','review','active','paused','completed','rejected')),
    budget_type             TEXT NOT NULL DEFAULT 'daily' CHECK (budget_type IN ('daily','lifetime')),
    budget_amount           NUMERIC(10,2) NOT NULL,
    currency                VARCHAR(3) NOT NULL DEFAULT 'INR',
    starts_at               TIMESTAMPTZ NOT NULL,
    ends_at                 TIMESTAMPTZ,
    spent_amount            NUMERIC(10,2) NOT NULL DEFAULT 0,
    attribution_window_days INT NOT NULL DEFAULT 7,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_campaigns_advertiser ON ad_campaigns(advertiser_id, status);
CREATE INDEX IF NOT EXISTS idx_campaigns_active ON ad_campaigns(status, starts_at) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS ad_sets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id UUID NOT NULL REFERENCES ad_campaigns(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    targeting   JSONB NOT NULL DEFAULT '{}',
    placement   TEXT[] NOT NULL DEFAULT '{}',
    bid_type    TEXT NOT NULL DEFAULT 'auto' CHECK (bid_type IN ('auto','manual_cpc','manual_cpm')),
    bid_amount  NUMERIC(8,2),
    daily_budget NUMERIC(10,2),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ad_creatives (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id  UUID NOT NULL REFERENCES ad_campaigns(id) ON DELETE CASCADE,
    content_type TEXT NOT NULL CHECK (content_type IN ('post_boost','reel_boost','story','banner','carousel')),
    post_id      UUID,
    headline     TEXT,
    body_text    TEXT,
    cta_type     TEXT CHECK (cta_type IN ('shop_now','learn_more','sign_up','download','book_now','contact_us')),
    cta_url      TEXT,
    media_ids    UUID[] DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ad_performance (
    campaign_id UUID NOT NULL REFERENCES ad_campaigns(id),
    creative_id UUID REFERENCES ad_creatives(id),
    date        DATE NOT NULL,
    impressions BIGINT NOT NULL DEFAULT 0,
    clicks      BIGINT NOT NULL DEFAULT 0,
    conversions BIGINT NOT NULL DEFAULT 0,
    spend       NUMERIC(10,2) NOT NULL DEFAULT 0,
    reach       BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (campaign_id, date)
);

CREATE TABLE IF NOT EXISTS ad_frequency_caps (
    campaign_id           UUID NOT NULL REFERENCES ad_campaigns(id),
    max_per_user_per_day  INT NOT NULL DEFAULT 3,
    max_per_user_per_week INT NOT NULL DEFAULT 10,
    PRIMARY KEY (campaign_id)
);
