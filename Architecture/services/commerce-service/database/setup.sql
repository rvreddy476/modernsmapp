-- ============================================================
-- Commerce Service — commerce_db
-- Production-grade e-commerce schema (v2)
-- Separate database, independently deployable.
-- ============================================================

-- ─── Sellers & Stores ───────────────────────────────────────

CREATE TABLE IF NOT EXISTS sellers (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL UNIQUE,           -- maps to identity user
    seller_type          TEXT NOT NULL DEFAULT 'individual'
                             CHECK (seller_type IN ('individual','business','brand_owner','local_retailer')),
    store_name           TEXT NOT NULL,
    brand_name           TEXT,
    legal_business_name  TEXT,
    slug                 TEXT NOT NULL UNIQUE,
    description          TEXT,
    logo_media_id        UUID,
    banner_media_id      UUID,
    email                TEXT NOT NULL,
    phone                TEXT,
    whatsapp_number      TEXT,
    support_email        TEXT,
    support_phone        TEXT,
    country              TEXT NOT NULL DEFAULT 'IN',
    state                TEXT,
    city                 TEXT,
    address_line_1       TEXT,
    address_line_2       TEXT,
    postal_code          TEXT,
    gst_number           TEXT,
    pan_number           TEXT,
    verification_status  TEXT NOT NULL DEFAULT 'pending'
                             CHECK (verification_status IN ('pending','verified','rejected','suspended')),
    store_status         TEXT NOT NULL DEFAULT 'active'
                             CHECK (store_status IN ('active','inactive','suspended','banned')),
    quality_score        REAL NOT NULL DEFAULT 0,
    performance_tier     TEXT NOT NULL DEFAULT 'standard'
                             CHECK (performance_tier IN ('standard','silver','gold','platinum')),
    avg_rating           REAL NOT NULL DEFAULT 0,
    review_count         INT NOT NULL DEFAULT 0,
    follower_count       INT NOT NULL DEFAULT 0,
    total_products       INT NOT NULL DEFAULT 0,
    total_orders         INT NOT NULL DEFAULT 0,
    is_featured          BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sellers_user    ON sellers(user_id);
CREATE INDEX IF NOT EXISTS idx_sellers_slug    ON sellers(slug);
CREATE INDEX IF NOT EXISTS idx_sellers_status  ON sellers(store_status) WHERE store_status = 'active';

CREATE TABLE IF NOT EXISTS seller_addresses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    address_type    TEXT NOT NULL CHECK (address_type IN ('business','pickup','return','warehouse')),
    contact_name    TEXT NOT NULL,
    phone           TEXT NOT NULL,
    address_line_1  TEXT NOT NULL,
    address_line_2  TEXT,
    city            TEXT NOT NULL,
    state           TEXT NOT NULL,
    country         TEXT NOT NULL DEFAULT 'IN',
    postal_code     TEXT NOT NULL,
    is_default      BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_seller_addresses_seller ON seller_addresses(seller_id);

CREATE TABLE IF NOT EXISTS seller_payout_accounts (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id            UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    account_holder_name  TEXT NOT NULL,
    bank_name            TEXT,
    account_number       TEXT NOT NULL,
    ifsc_code            TEXT,
    account_type         TEXT NOT NULL DEFAULT 'savings'
                             CHECK (account_type IN ('savings','current')),
    upi_id               TEXT,
    verification_status  TEXT NOT NULL DEFAULT 'pending'
                             CHECK (verification_status IN ('pending','verified','rejected')),
    is_primary           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_payout_accounts_seller ON seller_payout_accounts(seller_id);

CREATE TABLE IF NOT EXISTS seller_followers (
    seller_id   UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    followed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (seller_id, user_id)
);

-- ─── Product Categories ──────────────────────────────────────

CREATE TABLE IF NOT EXISTS product_categories (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    parent_id         UUID REFERENCES product_categories(id) ON DELETE SET NULL,
    name              TEXT NOT NULL,
    slug              TEXT NOT NULL UNIQUE,
    description       TEXT,
    icon_media_id     UUID,
    banner_media_id   UUID,
    display_order     INT NOT NULL DEFAULT 0,
    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    is_featured       BOOLEAN NOT NULL DEFAULT FALSE,
    seo_title         TEXT,
    seo_description   TEXT,
    custom_filters    JSONB NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_categories_parent ON product_categories(parent_id);
CREATE INDEX IF NOT EXISTS idx_categories_slug   ON product_categories(slug);
CREATE INDEX IF NOT EXISTS idx_categories_active ON product_categories(is_active, display_order);

CREATE TABLE IF NOT EXISTS product_brands (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    slug        TEXT NOT NULL UNIQUE,
    logo_media_id UUID,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Tax Classes ─────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS tax_classes (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL UNIQUE,
    cgst_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    sgst_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    igst_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    cess_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- seed default GST classes
INSERT INTO tax_classes (name, cgst_percentage, sgst_percentage, igst_percentage)
VALUES
    ('GST 0%',  0,  0,  0),
    ('GST 5%',  2.5, 2.5, 5),
    ('GST 12%', 6,  6,  12),
    ('GST 18%', 9,  9,  18),
    ('GST 28%', 14, 14, 28)
ON CONFLICT (name) DO NOTHING;

-- ─── Products & Variants ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS products (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id             UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    category_id           UUID REFERENCES product_categories(id),
    brand_id              UUID REFERENCES product_brands(id),
    tax_class_id          UUID REFERENCES tax_classes(id),
    title                 TEXT NOT NULL,
    short_title           TEXT,
    slug                  TEXT NOT NULL UNIQUE,
    description           TEXT,
    short_description     TEXT,
    brand_name            TEXT,
    manufacturer_name     TEXT,
    product_type          TEXT NOT NULL DEFAULT 'physical'
                              CHECK (product_type IN ('physical','digital','service')),
    condition             TEXT NOT NULL DEFAULT 'new'
                              CHECK (condition IN ('new','refurbished','used')),
    sku_root              TEXT,
    status                TEXT NOT NULL DEFAULT 'draft'
                              CHECK (status IN ('draft','active','paused','archived')),
    visibility            TEXT NOT NULL DEFAULT 'public'
                              CHECK (visibility IN ('public','private','password')),
    approval_status       TEXT NOT NULL DEFAULT 'pending'
                              CHECK (approval_status IN ('pending','approved','rejected','flagged')),
    rejection_reason      TEXT,
    moderation_flags      TEXT[] NOT NULL DEFAULT '{}',
    primary_image_media_id UUID,
    video_media_id        UUID,
    weight_grams          INT,
    length_cm             NUMERIC(8,2),
    width_cm              NUMERIC(8,2),
    height_cm             NUMERIC(8,2),
    country_of_origin     TEXT DEFAULT 'IN',
    warranty_info         TEXT,
    return_policy_type    TEXT NOT NULL DEFAULT 'standard'
                              CHECK (return_policy_type IN ('no_return','7_days','15_days','30_days','custom')),
    return_policy_days    INT NOT NULL DEFAULT 7,
    hsn_code              TEXT,
    meta_title            TEXT,
    meta_description      TEXT,
    search_keywords       TEXT[],
    avg_rating            REAL NOT NULL DEFAULT 0,
    review_count          INT NOT NULL DEFAULT 0,
    order_count           INT NOT NULL DEFAULT 0,
    view_count            BIGINT NOT NULL DEFAULT 0,
    wishlist_count        INT NOT NULL DEFAULT 0,
    is_featured           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at          TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_products_seller      ON products(seller_id, status);
CREATE INDEX IF NOT EXISTS idx_products_category    ON products(category_id) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_approval    ON products(approval_status, status);
CREATE INDEX IF NOT EXISTS idx_products_created     ON products(created_at DESC) WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_products_featured    ON products(is_featured) WHERE status = 'active' AND is_featured = TRUE;

CREATE TABLE IF NOT EXISTS product_variants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    sku             TEXT NOT NULL UNIQUE,
    barcode         TEXT,
    option_1_name   TEXT,
    option_1_value  TEXT,
    option_2_name   TEXT,
    option_2_value  TEXT,
    option_3_name   TEXT,
    option_3_value  TEXT,
    mrp             NUMERIC(12,2) NOT NULL,
    selling_price   NUMERIC(12,2) NOT NULL,
    cost_price      NUMERIC(12,2),
    currency_code   TEXT NOT NULL DEFAULT 'INR',
    status          TEXT NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active','inactive','out_of_stock')),
    image_media_id  UUID,
    weight_grams    INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_variants_product ON product_variants(product_id);
CREATE INDEX IF NOT EXISTS idx_variants_sku     ON product_variants(sku);

CREATE TABLE IF NOT EXISTS product_media (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    media_id    UUID NOT NULL,
    media_type  TEXT NOT NULL DEFAULT 'image'
                    CHECK (media_type IN ('image','video','size_chart','infographic')),
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_product_media_product ON product_media(product_id, sort_order);

CREATE TABLE IF NOT EXISTS product_attributes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    value      TEXT NOT NULL,
    unit       TEXT,
    sort_order INT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_product_attrs_product ON product_attributes(product_id);

-- ─── Inventory ───────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS inventory_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id      UUID NOT NULL UNIQUE REFERENCES product_variants(id) ON DELETE CASCADE,
    seller_id       UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    total_qty       INT NOT NULL DEFAULT 0,
    reserved_qty    INT NOT NULL DEFAULT 0,
    damaged_qty     INT NOT NULL DEFAULT 0,
    returned_qty    INT NOT NULL DEFAULT 0,
    safety_stock    INT NOT NULL DEFAULT 0,
    low_stock_alert INT NOT NULL DEFAULT 5,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_inv_qty CHECK (total_qty >= 0 AND reserved_qty >= 0)
);
CREATE INDEX IF NOT EXISTS idx_inventory_seller ON inventory_items(seller_id);

CREATE TABLE IF NOT EXISTS inventory_reservations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id  UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    order_id    UUID,          -- null = cart reservation
    user_id     UUID NOT NULL,
    quantity    INT NOT NULL CHECK (quantity > 0),
    type        TEXT NOT NULL DEFAULT 'cart'
                    CHECK (type IN ('cart','checkout','order')),
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_reservations_variant  ON inventory_reservations(variant_id, expires_at);
CREATE INDEX IF NOT EXISTS idx_reservations_order    ON inventory_reservations(order_id) WHERE order_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_reservations_user     ON inventory_reservations(user_id);

CREATE TABLE IF NOT EXISTS inventory_adjustments (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id   UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    seller_id    UUID NOT NULL,
    delta        INT NOT NULL,   -- positive = add, negative = remove
    reason_code  TEXT NOT NULL CHECK (reason_code IN ('purchase','return','damage','theft','correction','recount','return_qc_pass','return_qc_fail')),
    notes        TEXT,
    created_by   UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_inv_adjustments_variant ON inventory_adjustments(variant_id, created_at DESC);

-- ─── Bulk Import Jobs ────────────────────────────────────────

CREATE TABLE IF NOT EXISTS product_import_jobs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    filename        TEXT NOT NULL,
    file_media_id   UUID,
    status          TEXT NOT NULL DEFAULT 'uploaded'
                        CHECK (status IN ('uploaded','validating','validation_failed','ready_to_import','importing','partially_imported','completed','failed')),
    total_rows      INT NOT NULL DEFAULT 0,
    valid_rows      INT NOT NULL DEFAULT 0,
    imported_rows   INT NOT NULL DEFAULT 0,
    error_rows      INT NOT NULL DEFAULT 0,
    error_file_id   UUID,
    dry_run         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_import_jobs_seller ON product_import_jobs(seller_id, created_at DESC);

-- ─── Pricing & Discounts ─────────────────────────────────────

CREATE TABLE IF NOT EXISTS coupons (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           UUID REFERENCES sellers(id) ON DELETE CASCADE,  -- null = platform coupon
    code                TEXT NOT NULL UNIQUE,
    description         TEXT,
    discount_type       TEXT NOT NULL CHECK (discount_type IN ('percentage','flat','buy_x_get_y','free_shipping')),
    discount_value      NUMERIC(10,2) NOT NULL,
    max_discount_amount NUMERIC(10,2),
    min_order_amount    NUMERIC(10,2) NOT NULL DEFAULT 0,
    max_uses            INT,
    uses_count          INT NOT NULL DEFAULT 0,
    max_uses_per_user   INT NOT NULL DEFAULT 1,
    applicable_to       TEXT NOT NULL DEFAULT 'all'
                            CHECK (applicable_to IN ('all','category','product','seller')),
    applicable_ids      UUID[] DEFAULT '{}',
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    starts_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at          TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_coupons_code    ON coupons(code) WHERE is_active = TRUE;
CREATE INDEX IF NOT EXISTS idx_coupons_seller  ON coupons(seller_id) WHERE seller_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS coupon_usages (
    coupon_id  UUID NOT NULL REFERENCES coupons(id),
    user_id    UUID NOT NULL,
    order_id   UUID NOT NULL,
    used_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (coupon_id, order_id)
);
CREATE INDEX IF NOT EXISTS idx_coupon_usages_user ON coupon_usages(user_id, coupon_id);

-- ─── Cart ────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS carts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_carts_user ON carts(user_id);

CREATE TABLE IF NOT EXISTS cart_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id         UUID NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
    variant_id      UUID NOT NULL REFERENCES product_variants(id),
    product_id      UUID NOT NULL REFERENCES products(id),
    quantity        INT NOT NULL DEFAULT 1 CHECK (quantity > 0),
    price_snapshot  NUMERIC(12,2) NOT NULL,     -- selling price at add time
    added_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (cart_id, variant_id)
);
CREATE INDEX IF NOT EXISTS idx_cart_items_cart ON cart_items(cart_id);

-- ─── Customer Addresses ──────────────────────────────────────

CREATE TABLE IF NOT EXISTS customer_addresses (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID NOT NULL,
    label            TEXT NOT NULL DEFAULT 'Home',
    contact_name     TEXT NOT NULL,
    phone            TEXT NOT NULL,
    address_line_1   TEXT NOT NULL,
    address_line_2   TEXT,
    landmark         TEXT,
    city             TEXT NOT NULL,
    state            TEXT NOT NULL,
    country          TEXT NOT NULL DEFAULT 'IN',
    postal_code      TEXT NOT NULL,
    address_type     TEXT NOT NULL DEFAULT 'home'
                         CHECK (address_type IN ('home','work','other')),
    is_default       BOOLEAN NOT NULL DEFAULT FALSE,
    latitude         NUMERIC(10,8),
    longitude        NUMERIC(11,8),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_cust_addresses_user ON customer_addresses(user_id);

-- ─── Wishlists ───────────────────────────────────────────────

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
    variant_id  UUID NOT NULL REFERENCES product_variants(id),
    product_id  UUID NOT NULL REFERENCES products(id),
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (wishlist_id, variant_id)
);
CREATE INDEX IF NOT EXISTS idx_wishlist_items_user_product ON wishlist_items(product_id);

CREATE TABLE IF NOT EXISTS stock_alerts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    variant_id UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    alerted    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, variant_id)
);

-- ─── Orders ──────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS orders (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id    UUID NOT NULL,
    order_number        TEXT NOT NULL UNIQUE,  -- e.g. ORD-2024-000001
    subtotal            NUMERIC(12,2) NOT NULL,
    discount_amount     NUMERIC(12,2) NOT NULL DEFAULT 0,
    shipping_charges    NUMERIC(12,2) NOT NULL DEFAULT 0,
    tax_amount          NUMERIC(12,2) NOT NULL DEFAULT 0,
    coupon_code         TEXT,
    coupon_discount     NUMERIC(12,2) NOT NULL DEFAULT 0,
    final_amount        NUMERIC(12,2) NOT NULL,
    currency_code       TEXT NOT NULL DEFAULT 'INR',
    payment_method      TEXT CHECK (payment_method IN ('upi','card','net_banking','wallet','cod','emi','bnpl')),
    payment_status      TEXT NOT NULL DEFAULT 'pending'
                            CHECK (payment_status IN ('pending','processing','paid','failed','refund_pending','refunded','partially_refunded')),
    payment_id          TEXT,          -- gateway transaction reference
    payment_gateway     TEXT,
    delivery_address_id UUID REFERENCES customer_addresses(id),
    delivery_address_snapshot JSONB, -- snapshot at order time
    billing_address_id  UUID REFERENCES customer_addresses(id),
    delivery_instructions TEXT,
    gift_message        TEXT,
    invoice_requested   BOOLEAN NOT NULL DEFAULT FALSE,
    status              TEXT NOT NULL DEFAULT 'created'
                            CHECK (status IN ('created','payment_pending','paid','confirmed','packed','shipped','out_for_delivery','delivered','cancelled','return_requested','return_approved','return_rejected','return_picked_up','returned','refund_pending','refunded')),
    cancellation_reason TEXT,
    cancelled_by        TEXT CHECK (cancelled_by IN ('customer','seller','system','admin')),
    idempotency_key     TEXT UNIQUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders(customer_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_status   ON orders(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_orders_number   ON orders(order_number);

CREATE TABLE IF NOT EXISTS order_items (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id             UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id           UUID NOT NULL REFERENCES products(id),
    variant_id           UUID NOT NULL REFERENCES product_variants(id),
    seller_id            UUID NOT NULL REFERENCES sellers(id),
    product_title        TEXT NOT NULL,
    variant_details      JSONB NOT NULL DEFAULT '{}',  -- {option_1_name, option_1_value, ...}
    sku                  TEXT NOT NULL,
    quantity             INT NOT NULL CHECK (quantity > 0),
    unit_mrp             NUMERIC(12,2) NOT NULL,
    unit_price           NUMERIC(12,2) NOT NULL,
    discount_amount      NUMERIC(12,2) NOT NULL DEFAULT 0,
    tax_amount           NUMERIC(12,2) NOT NULL DEFAULT 0,
    final_price          NUMERIC(12,2) NOT NULL,
    status               TEXT NOT NULL DEFAULT 'confirmed'
                             CHECK (status IN ('confirmed','packed','shipped','out_for_delivery','delivered','cancelled','return_requested','returned','refunded')),
    shipment_id          UUID,
    tracking_number      TEXT,
    return_eligible_until TIMESTAMPTZ,
    delivered_at         TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_items_order   ON order_items(order_id);
CREATE INDEX IF NOT EXISTS idx_order_items_seller  ON order_items(seller_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_order_items_product ON order_items(product_id);

CREATE TABLE IF NOT EXISTS order_status_history (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    from_status TEXT,
    to_status   TEXT NOT NULL,
    changed_by  UUID,
    actor_type  TEXT NOT NULL DEFAULT 'system'
                    CHECK (actor_type IN ('system','customer','seller','admin')),
    notes       TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_order_history_order ON order_status_history(order_id, created_at DESC);

-- ─── Payments ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS payments (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id        UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL,
    amount          NUMERIC(12,2) NOT NULL,
    currency        TEXT NOT NULL DEFAULT 'INR',
    payment_method  TEXT NOT NULL,
    gateway         TEXT NOT NULL,
    gateway_order_id TEXT,
    gateway_txn_id  TEXT,
    status          TEXT NOT NULL DEFAULT 'initiated'
                        CHECK (status IN ('initiated','pending','processing','success','failed','refund_initiated','refund_pending','refunded','partially_refunded')),
    idempotency_key TEXT UNIQUE,
    raw_response    JSONB,
    failure_reason  TEXT,
    initiated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at    TIMESTAMPTZ,
    UNIQUE (gateway, gateway_txn_id)
);
CREATE INDEX IF NOT EXISTS idx_payments_order  ON payments(order_id);
CREATE INDEX IF NOT EXISTS idx_payments_user   ON payments(user_id, initiated_at DESC);
CREATE INDEX IF NOT EXISTS idx_payments_status ON payments(status);

CREATE TABLE IF NOT EXISTS refunds (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id          UUID NOT NULL REFERENCES payments(id),
    order_id            UUID NOT NULL REFERENCES orders(id),
    return_id           UUID,
    amount              NUMERIC(12,2) NOT NULL,
    currency            TEXT NOT NULL DEFAULT 'INR',
    reason              TEXT NOT NULL,
    refund_method       TEXT NOT NULL CHECK (refund_method IN ('original_payment','wallet','store_credit','bank_transfer')),
    gateway_refund_id   TEXT,
    status              TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed')),
    initiated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_refunds_order   ON refunds(order_id);
CREATE INDEX IF NOT EXISTS idx_refunds_payment ON refunds(payment_id);

-- ─── Shipping ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS shipping_partners (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    code        TEXT NOT NULL UNIQUE,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    tracking_url_template TEXT,
    config      JSONB NOT NULL DEFAULT '{}'
);
INSERT INTO shipping_partners (name, code, tracking_url_template) VALUES
    ('Delhivery', 'delhivery', 'https://www.delhivery.com/track/package/{awb}'),
    ('Blue Dart',  'bluedart',  'https://www.bluedart.com/tracking/{awb}'),
    ('Ecom Express','ecom',    'https://ecomexpress.in/tracking/?awb_field={awb}'),
    ('Shiprocket', 'shiprocket', 'https://app.shiprocket.in/tracking/{awb}'),
    ('Shadowfax',  'shadowfax', 'https://www.shadowfax.in/tracking/{awb}')
ON CONFLICT (code) DO NOTHING;

CREATE TABLE IF NOT EXISTS shipping_packages (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id                UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    seller_id               UUID NOT NULL REFERENCES sellers(id),
    shipping_partner_id     UUID REFERENCES shipping_partners(id),
    awb_number              TEXT UNIQUE,
    tracking_url            TEXT,
    weight_grams            INT,
    length_cm               NUMERIC(8,2),
    width_cm                NUMERIC(8,2),
    height_cm               NUMERIC(8,2),
    shipping_label_media_id UUID,
    pickup_address_id       UUID REFERENCES seller_addresses(id),
    pickup_scheduled_at     TIMESTAMPTZ,
    picked_up_at            TIMESTAMPTZ,
    estimated_delivery_date DATE,
    delivered_at            TIMESTAMPTZ,
    current_status          TEXT NOT NULL DEFAULT 'pending'
                                CHECK (current_status IN ('pending','label_created','pickup_scheduled','picked_up','in_transit','out_for_delivery','delivered','failed_delivery','rto_initiated','rto_delivered')),
    current_location        TEXT,
    rto_initiated_at        TIMESTAMPTZ,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_packages_order  ON shipping_packages(order_id);
CREATE INDEX IF NOT EXISTS idx_packages_awb    ON shipping_packages(awb_number) WHERE awb_number IS NOT NULL;

CREATE TABLE IF NOT EXISTS shipment_tracking_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    package_id  UUID NOT NULL REFERENCES shipping_packages(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    description TEXT,
    location    TEXT,
    source      TEXT NOT NULL DEFAULT 'webhook' CHECK (source IN ('webhook','manual','api')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tracking_events_package ON shipment_tracking_events(package_id, occurred_at DESC);

-- ─── Reviews ─────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS reviews (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    variant_id      UUID REFERENCES product_variants(id),
    seller_id       UUID NOT NULL REFERENCES sellers(id),
    order_item_id   UUID NOT NULL REFERENCES order_items(id),
    reviewer_id     UUID NOT NULL,
    rating          INT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    title           TEXT,
    body            TEXT,
    is_verified_purchase BOOLEAN NOT NULL DEFAULT TRUE,
    is_published    BOOLEAN NOT NULL DEFAULT TRUE,
    helpful_count   INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (reviewer_id, order_item_id)
);
CREATE INDEX IF NOT EXISTS idx_reviews_product ON reviews(product_id, is_published, rating);
CREATE INDEX IF NOT EXISTS idx_reviews_seller  ON reviews(seller_id, is_published);

CREATE TABLE IF NOT EXISTS review_media (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id  UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    media_id   UUID NOT NULL,
    media_type TEXT NOT NULL DEFAULT 'image',
    sort_order INT NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS review_votes (
    review_id  UUID NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL,
    is_helpful BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (review_id, user_id)
);

CREATE TABLE IF NOT EXISTS review_responses (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    review_id  UUID NOT NULL UNIQUE REFERENCES reviews(id) ON DELETE CASCADE,
    seller_id  UUID NOT NULL REFERENCES sellers(id),
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── Returns & Refunds ───────────────────────────────────────

CREATE TABLE IF NOT EXISTS return_requests (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id             UUID NOT NULL REFERENCES orders(id),
    order_item_id        UUID NOT NULL REFERENCES order_items(id),
    customer_user_id     UUID NOT NULL,
    seller_id            UUID NOT NULL REFERENCES sellers(id),
    reason_code          TEXT NOT NULL CHECK (reason_code IN ('size_fit','defective','wrong_item','damaged','not_as_described','changed_mind','other')),
    reason_description   TEXT,
    image_media_ids      UUID[] NOT NULL DEFAULT '{}',
    status               TEXT NOT NULL DEFAULT 'requested'
                             CHECK (status IN ('requested','approved','rejected','pickup_scheduled','picked_up','received','qc_pass','qc_fail','refund_initiated','completed')),
    requested_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    approved_at          TIMESTAMPTZ,
    rejected_at          TIMESTAMPTZ,
    rejection_reason     TEXT,
    refund_amount        NUMERIC(12,2),
    refund_id            UUID REFERENCES refunds(id)
);
CREATE INDEX IF NOT EXISTS idx_returns_order    ON return_requests(order_id);
CREATE INDEX IF NOT EXISTS idx_returns_customer ON return_requests(customer_user_id, requested_at DESC);
CREATE INDEX IF NOT EXISTS idx_returns_seller   ON return_requests(seller_id, status);

CREATE TABLE IF NOT EXISTS return_pickups (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    return_id       UUID NOT NULL REFERENCES return_requests(id) ON DELETE CASCADE,
    package_id      UUID REFERENCES shipping_packages(id),
    scheduled_at    TIMESTAMPTZ,
    picked_up_at    TIMESTAMPTZ,
    delivery_address_id UUID REFERENCES customer_addresses(id),
    status          TEXT NOT NULL DEFAULT 'scheduled'
                        CHECK (status IN ('scheduled','picked_up','failed','cancelled'))
);

-- ─── Payout System ───────────────────────────────────────────

CREATE TABLE IF NOT EXISTS payout_batches (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_date          DATE NOT NULL,
    payout_cycle_start  DATE NOT NULL,
    payout_cycle_end    DATE NOT NULL,
    total_sellers       INT NOT NULL DEFAULT 0,
    total_amount        NUMERIC(14,2) NOT NULL DEFAULT 0,
    status              TEXT NOT NULL DEFAULT 'pending'
                            CHECK (status IN ('pending','processing','completed','failed','partial')),
    processed_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS payout_transactions (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id         UUID NOT NULL REFERENCES payout_batches(id),
    seller_id        UUID NOT NULL REFERENCES sellers(id),
    order_ids        UUID[] NOT NULL DEFAULT '{}',
    gross_amount     NUMERIC(12,2) NOT NULL,
    commission_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    platform_fee     NUMERIC(12,2) NOT NULL DEFAULT 0,
    tax_deducted     NUMERIC(12,2) NOT NULL DEFAULT 0,
    adjustment_amount NUMERIC(12,2) NOT NULL DEFAULT 0,  -- +bonus, -penalty
    net_amount       NUMERIC(12,2) NOT NULL,
    bank_account_id  UUID REFERENCES seller_payout_accounts(id),
    transfer_reference TEXT,
    status           TEXT NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending','processing','completed','failed')),
    failure_reason   TEXT,
    initiated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_payout_txns_seller ON payout_transactions(seller_id, initiated_at DESC);
CREATE INDEX IF NOT EXISTS idx_payout_txns_batch  ON payout_transactions(batch_id);

-- ─── Fraud Detection ─────────────────────────────────────────

CREATE TABLE IF NOT EXISTS fraud_scores (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type     TEXT NOT NULL CHECK (entity_type IN ('order','user','seller')),
    entity_id       UUID NOT NULL,
    risk_score      NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (risk_score BETWEEN 0 AND 100),
    risk_level      TEXT NOT NULL DEFAULT 'low' CHECK (risk_level IN ('low','medium','high','critical')),
    signals         JSONB NOT NULL DEFAULT '{}',
    action_taken    TEXT,
    reviewed_by     UUID,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (entity_type, entity_id)
);
CREATE INDEX IF NOT EXISTS idx_fraud_scores_entity ON fraud_scores(entity_type, risk_level);

-- ─── Support Tickets ─────────────────────────────────────────

CREATE TABLE IF NOT EXISTS support_tickets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    seller_id       UUID REFERENCES sellers(id),
    order_id        UUID REFERENCES orders(id),
    category        TEXT NOT NULL CHECK (category IN ('order','payment','product','return','account','other')),
    subject         TEXT NOT NULL,
    description     TEXT NOT NULL,
    priority        TEXT NOT NULL DEFAULT 'normal' CHECK (priority IN ('low','normal','high','urgent')),
    status          TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_progress','waiting_customer','resolved','closed')),
    assigned_to     UUID,
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_tickets_user    ON support_tickets(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tickets_status  ON support_tickets(status, priority);
CREATE INDEX IF NOT EXISTS idx_tickets_order   ON support_tickets(order_id) WHERE order_id IS NOT NULL;

-- ─── Commission Rules ─────────────────────────────────────────

CREATE TABLE IF NOT EXISTS commission_rules (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id         UUID REFERENCES product_categories(id),
    seller_tier         TEXT,  -- null = all tiers
    commission_pct      NUMERIC(5,2) NOT NULL,
    platform_fee_pct    NUMERIC(5,2) NOT NULL DEFAULT 0,
    min_commission      NUMERIC(8,2) NOT NULL DEFAULT 0,
    max_commission      NUMERIC(8,2),
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    effective_from      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_until     TIMESTAMPTZ
);

-- ─── Sequence for human-readable order numbers ───────────────
CREATE SEQUENCE IF NOT EXISTS order_number_seq START 10001;

CREATE OR REPLACE FUNCTION generate_order_number() RETURNS TEXT AS $$
BEGIN
    RETURN 'ORD-' || TO_CHAR(NOW(), 'YYYY') || '-' || LPAD(nextval('order_number_seq')::TEXT, 6, '0');
END;
$$ LANGUAGE plpgsql;

-- ─── Idempotent schema upgrades — applied on every BootstrapSchema boot.
-- Phase 2.4 — review moderation status + seller response.
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS moderation_status TEXT NOT NULL DEFAULT 'approved';
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS seller_response TEXT;
ALTER TABLE reviews ADD COLUMN IF NOT EXISTS seller_responded_at TIMESTAMPTZ;

-- Phase 3.4 — admin moderation needs a "request changes" terminal state
-- distinct from rejection. The original CHECK was already implicitly
-- broken (CreateProduct writes 'draft', ApproveProductByAdmin writes
-- 'live'); the rewrite documents the full set the code actually uses.
ALTER TABLE products DROP CONSTRAINT IF EXISTS products_approval_status_check;
ALTER TABLE products ADD CONSTRAINT products_approval_status_check
    CHECK (approval_status IN ('draft','pending','approved','rejected','flagged','changes_requested','live'));

-- Phase F2.3 — bulk SKU import + Phase F2.2 RFQ added two new fulfillment
-- job kinds. Idempotent ALTER so existing deployments accept the new
-- enum values without a manual migration.
ALTER TABLE fulfillment_jobs DROP CONSTRAINT IF EXISTS fulfillment_jobs_kind_check;
ALTER TABLE fulfillment_jobs ADD CONSTRAINT fulfillment_jobs_kind_check
    CHECK (kind IN (
        'fulfill_paid_order',
        'process_return_approved',
        'bulk_import_validate',
        'bulk_import_execute'
    ));

-- ─── Phase 6.1 — Fulfillment job queue ─────────────────────────
-- Replaces `go s.fulfillPaidOrder()` with a durable retry-with-backoff
-- worker so a service restart no longer drops in-flight side effects
-- (invoice issuance, shipment booking, refund initiation).

CREATE TABLE IF NOT EXISTS fulfillment_jobs (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL
                       CHECK (kind IN (
                           'fulfill_paid_order',
                           'process_return_approved',
                           'bulk_import_validate',
                           'bulk_import_execute'
                       )),
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','processing','done','dead')),
    attempts        INT NOT NULL DEFAULT 0,
    next_run_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    dead_letter_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_fulfillment_jobs_pending
    ON fulfillment_jobs(status, next_run_at)
    WHERE status IN ('pending','processing');
CREATE INDEX IF NOT EXISTS idx_fulfillment_jobs_dead
    ON fulfillment_jobs(dead_letter_at DESC)
    WHERE status = 'dead';

-- ─── Phase 6.2 — Stock reservation expiry tracking ─────────────
-- The reservation table existed but lacked an explicit expiry-only
-- index; the worker needs to find rows where expires_at <= NOW()
-- cheaply, separate from the variant-scoped lookup index.
CREATE INDEX IF NOT EXISTS idx_inventory_reservations_expiry
    ON inventory_reservations(expires_at);

-- ─── Phase F2.1 — Tiered B2B pricing ──────────────────────────
-- Seller-defined quantity tier breaks per variant. priceCart looks up
-- the highest min_qty band <= cart_line.quantity and uses that price
-- instead of variant.selling_price. Non-overlapping tiers are enforced
-- by the (variant_id, min_qty) UNIQUE constraint plus a service-layer
-- check on max_qty.
CREATE TABLE IF NOT EXISTS product_price_tiers (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    variant_id    UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    min_qty       INT  NOT NULL CHECK (min_qty >= 1),
    max_qty       INT  CHECK (max_qty IS NULL OR max_qty >= min_qty),
    price         NUMERIC(10,2) NOT NULL CHECK (price > 0),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(variant_id, min_qty)
);
CREATE INDEX IF NOT EXISTS idx_price_tiers_variant ON product_price_tiers(variant_id);

-- ─── Phase F2.2 — Request For Quote (RFQ) ─────────────────────
-- Buyer-initiated quote requests. The seller responds with a quote
-- (rfq_quotes); on acceptance, AcceptRFQQuote bypasses priceCart and
-- creates an order at the negotiated price. Personal + org buyers
-- both supported (organization_id is nullable).
CREATE TABLE IF NOT EXISTS rfqs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_user_id   UUID NOT NULL,
    organization_id UUID REFERENCES organizations(id),
    seller_id       UUID NOT NULL REFERENCES sellers(id),
    status          TEXT NOT NULL DEFAULT 'requested'
                       CHECK (status IN ('requested','quoted','accepted','expired','rejected','cancelled')),
    message_text    TEXT,
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rfqs_buyer  ON rfqs(buyer_user_id);
CREATE INDEX IF NOT EXISTS idx_rfqs_seller ON rfqs(seller_id, status);
CREATE INDEX IF NOT EXISTS idx_rfqs_org    ON rfqs(organization_id) WHERE organization_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS rfq_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rfq_id          UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    variant_id      UUID NOT NULL REFERENCES product_variants(id),
    quantity        INT  NOT NULL CHECK (quantity > 0),
    notes           TEXT
);
CREATE INDEX IF NOT EXISTS idx_rfq_items_rfq ON rfq_items(rfq_id);

CREATE TABLE IF NOT EXISTS rfq_quotes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rfq_id          UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    quoted_total    NUMERIC(12,2) NOT NULL CHECK (quoted_total > 0),
    line_prices     JSONB NOT NULL,
    validity_days   INT NOT NULL CHECK (validity_days BETWEEN 1 AND 90),
    quoted_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    accepted_at     TIMESTAMPTZ,
    order_id        UUID REFERENCES orders(id)
);
CREATE INDEX IF NOT EXISTS idx_rfq_quotes_rfq ON rfq_quotes(rfq_id);

-- ─── Phase F2.3 — Bulk SKU upload — already-present table ────
-- product_import_jobs already exists from earlier work (status,
-- total_rows, valid_rows, imported_rows, error_file_id). No new
-- table needed; service layer fills it in.

-- ─── Phase 5 — B2B / Organizations ─────────────────────────────
-- Business customers (corporates, schools, govt) buying through the
-- same checkout, but with org context: shared billing address, GSTIN
-- invoice, PO number, approval workflow, credit terms.

CREATE TABLE IF NOT EXISTS organizations (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name                    TEXT NOT NULL,
    legal_name              TEXT,
    gstin                   TEXT,
    pan                     TEXT,
    billing_email           TEXT,
    billing_phone           TEXT,
    billing_address_id      UUID REFERENCES customer_addresses(id),
    -- Approval threshold (in INR major). Orders >= threshold need an
    -- approver. NULL means no approval gate.
    approval_threshold      NUMERIC(12,2),
    -- Credit terms — null/0 means prepay only.
    credit_terms_days       INT NOT NULL DEFAULT 0
                              CHECK (credit_terms_days >= 0 AND credit_terms_days <= 90),
    credit_limit            NUMERIC(12,2),
    status                  TEXT NOT NULL DEFAULT 'active'
                              CHECK (status IN ('active','suspended','closed')),
    created_by_user_id      UUID,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_organizations_status ON organizations(status);
CREATE INDEX IF NOT EXISTS idx_organizations_gstin ON organizations(gstin)
    WHERE gstin IS NOT NULL;

CREATE TABLE IF NOT EXISTS organization_members (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL,
    -- Roles drive RBAC:
    --   admin    — manage org settings + members
    --   buyer    — place orders up to approval_threshold
    --   approver — sign off on orders above threshold
    --   finance  — view invoices + ledger only
    role            TEXT NOT NULL
                       CHECK (role IN ('admin','buyer','approver','finance')),
    status          TEXT NOT NULL DEFAULT 'active'
                       CHECK (status IN ('invited','active','removed')),
    invited_email   TEXT,
    invited_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    joined_at       TIMESTAMPTZ,
    UNIQUE(organization_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_org_members_user ON organization_members(user_id);
CREATE INDEX IF NOT EXISTS idx_org_members_org ON organization_members(organization_id);

CREATE TABLE IF NOT EXISTS organization_invites (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email           TEXT NOT NULL,
    role            TEXT NOT NULL
                       CHECK (role IN ('admin','buyer','approver','finance')),
    token           TEXT NOT NULL UNIQUE,
    invited_by      UUID NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    accepted_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_org_invites_token ON organization_invites(token);
CREATE INDEX IF NOT EXISTS idx_org_invites_org ON organization_invites(organization_id);

-- Order-level B2B context. NULL organization_id = retail order; the
-- existing customer_user_id field continues to identify the placer.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS organization_id UUID
    REFERENCES organizations(id);
ALTER TABLE orders ADD COLUMN IF NOT EXISTS po_number TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS cost_center TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS billing_address_snapshot JSONB;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS invoice_email TEXT;
-- Approval state machine, parallel to status. NULL = no approval required.
ALTER TABLE orders ADD COLUMN IF NOT EXISTS approval_status TEXT
    CHECK (approval_status IN ('not_required','pending','approved','rejected'));
ALTER TABLE orders ADD COLUMN IF NOT EXISTS approved_by_user_id UUID;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS approved_at TIMESTAMPTZ;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS approval_notes TEXT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS credit_terms_days INT NOT NULL DEFAULT 0;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS payment_due_date TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_orders_organization ON orders(organization_id)
    WHERE organization_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_orders_approval ON orders(approval_status)
    WHERE approval_status = 'pending';
