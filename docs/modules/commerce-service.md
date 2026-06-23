# Module: commerce-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /addresses/:addressId
DELETE /cart/items/:variantId
DELETE /organizations/:orgId/members/:userId
DELETE /variants/:variantId
GET /addresses
GET /affiliate/:linkId
GET /cart
GET /cart/coupon-preview
GET /categories
GET /jobs/dead-letter
GET /me/returns
GET /orders
GET /orders/:orderId
GET /orders/:orderId/invoice
GET /orders/:orderId/items
GET /orders/:orderId/shipment
GET /orders/:orderId/shipments
GET /organizations/me
GET /organizations/:orgId
GET /organizations/:orgId/members
GET /organizations/:orgId/orders
GET /organizations/:orgId/orders/pending-approval
GET /payout/preview
GET /payouts/pending
GET /products
GET /products/:productId
GET /products/:productId/attributes
GET /products/:productId/media
GET /products/:productId/preview
GET /products/:productId/reviews
GET /products/:productId/variants
GET /products/queue
GET /returns/:returnId
GET /returns/:returnId/refund-preview
GET /rfqs
GET /rfqs/:rfqId
GET /seller/bulk-import
GET /seller/bulk-import/:jobId
GET /seller/bulk-import/:jobId/errors.csv
GET /seller/cod-remittances
GET /seller/earnings
GET /seller/earnings.csv
GET /seller/fulfillment
GET /seller/orders
GET /seller/orders/:orderId
GET /seller/returns
GET /seller/rfqs
GET /sellers/me
GET /sellers/queue
GET /sellers/:sellerId
GET /sellers/:sellerId/products
GET /serviceability
GET /status
GET /v1/commerce/dashboard
GET /variants/:variantId/price-tiers
PATCH /addresses/:addressId
PATCH /cart/items/by-variant/:variantId
PATCH /organizations/:orgId
PATCH /organizations/:orgId/members/:userId
PATCH /variants/:variantId
POST /addresses
POST /addresses/:addressId/default
POST /cart/items
POST /checkout/quote
POST /cod-remittances/:remittanceId/settle
POST /orders/checkout
POST /orders/:orderId/approve
POST /orders/:orderId/cancel
POST /orders/:orderId/invoice
POST /orders/:orderId/payment/confirm
POST /orders/:orderId/reject
POST /orders/:orderId/returns
POST /orders/:orderId/shipment
POST /organizations
POST /organizations/invites/:token/accept
POST /organizations/:orgId/members
POST /products
POST /products/:productId/approve
POST /products/:productId/media
POST /products/:productId/reject
POST /products/:productId/request-changes
POST /products/:productId/reviews
POST /products/:productId/variants
POST /returns/:returnId/approve
POST /returns/:returnId/reject
POST /reviews/:reviewId/seller-response
POST /rfqs
POST /rfqs/:rfqId/quote
POST /rfqs/:rfqId/quotes/:quoteId/accept
POST /rfqs/:rfqId/reject
POST /seller/bulk-import/:jobId/execute
POST /seller/bulk-import/:jobId/upload-complete
POST /seller/bulk-import/presigned-url
POST /sellers/onboard
POST /sellers/:sellerId/approve
POST /sellers/:sellerId/kyc/verify
POST /sellers/:sellerId/reject
POST /sellers/:sellerId/request-changes
POST /sellers/:sellerId/suspend
POST /shipments/courier-callback
POST /shipments/webhooks/:courier
POST /start
POST /submit
POST /v1/commerce/products/:productId/submit
PUT /products/:productId/attributes
PUT /step/basic
PUT /step/documents
PUT /step/fulfillment
PUT /step/payout
PUT /step/storefront
PUT /variants/:variantId/price-tiers
GROUP /v1/commerce
GROUP /v1/commerce/internal
GROUP /v1/commerce/onboarding
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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

CREATE TABLE IF NOT EXISTS seller_followers (
    seller_id   UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL,
    followed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (seller_id, user_id)
);

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

CREATE TABLE IF NOT EXISTS product_brands (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    slug        TEXT NOT NULL UNIQUE,
    logo_media_id UUID,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS tax_classes (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT NOT NULL UNIQUE,
    cgst_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    sgst_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    igst_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    cess_percentage  NUMERIC(5,2) NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS product_media (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id  UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    media_id    UUID NOT NULL,
    media_type  TEXT NOT NULL DEFAULT 'image'
                    CHECK (media_type IN ('image','video','size_chart','infographic')),
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS product_attributes (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    value      TEXT NOT NULL,
    unit       TEXT,
    sort_order INT NOT NULL DEFAULT 0
);

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

CREATE TABLE IF NOT EXISTS coupon_usages (
    coupon_id  UUID NOT NULL REFERENCES coupons(id),
    user_id    UUID NOT NULL,
    order_id   UUID NOT NULL,
    used_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (coupon_id, order_id)
);

CREATE TABLE IF NOT EXISTS carts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS wishlists (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    name       TEXT NOT NULL DEFAULT 'My Wishlist',
    is_public  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS wishlist_items (
    wishlist_id UUID NOT NULL REFERENCES wishlists(id) ON DELETE CASCADE,
    variant_id  UUID NOT NULL REFERENCES product_variants(id),
    product_id  UUID NOT NULL REFERENCES products(id),
    added_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (wishlist_id, variant_id)
);

CREATE TABLE IF NOT EXISTS stock_alerts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    variant_id UUID NOT NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    alerted    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, variant_id)
);

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
    idempotency_key     TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS shipping_partners (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL UNIQUE,
    code        TEXT NOT NULL UNIQUE,
    is_active   BOOLEAN NOT NULL DEFAULT TRUE,
    tracking_url_template TEXT,
    config      JSONB NOT NULL DEFAULT '{}'
);

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

CREATE TABLE IF NOT EXISTS shipment_tracking_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    package_id  UUID NOT NULL REFERENCES shipping_packages(id) ON DELETE CASCADE,
    event_type  TEXT NOT NULL,
    description TEXT,
    location    TEXT,
    source      TEXT NOT NULL DEFAULT 'webhook' CHECK (source IN ('webhook','manual','api')),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

-- enum values without a manual migration. Moved AFTER the CREATE TABLE
-- above so first-run bootstrap doesn't error on a missing relation.
ALTER TABLE fulfillment_jobs DROP CONSTRAINT IF EXISTS fulfillment_jobs_kind_check;
ALTER TABLE fulfillment_jobs ADD CONSTRAINT fulfillment_jobs_kind_check
    CHECK (kind IN (
        'fulfill_paid_order',
        'process_return_approved',
        'bulk_import_validate',
        'bulk_import_execute'
    ));

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

CREATE TABLE IF NOT EXISTS rfqs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    buyer_user_id   UUID NOT NULL,
    -- FK to organizations(id) added via idempotent ALTER at end of file —
    -- organizations table is defined further down in Phase 5 and the
    -- naive `strings.Split(sql, ";")` bootstrapper applies statements
    -- in file order, so an inline FK fails on first-run install.
    organization_id UUID,
    seller_id       UUID NOT NULL REFERENCES sellers(id),
    status          TEXT NOT NULL DEFAULT 'requested'
                       CHECK (status IN ('requested','quoted','accepted','expired','rejected','cancelled')),
    message_text    TEXT,
    requested_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rfq_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    rfq_id          UUID NOT NULL REFERENCES rfqs(id) ON DELETE CASCADE,
    variant_id      UUID NOT NULL REFERENCES product_variants(id),
    quantity        INT  NOT NULL CHECK (quantity > 0),
    notes           TEXT
);

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

CREATE TABLE IF NOT EXISTS seller_documents (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id           UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    document_type       TEXT NOT NULL
                            CHECK (document_type IN ('gst_certificate','pan_card','aadhaar','passport',
                                                     'business_registration','address_proof','cancelled_cheque','other')),
    document_number     TEXT,
    media_id            UUID NOT NULL,          -- uploaded via media-service
    verification_status TEXT NOT NULL DEFAULT 'pending'
                            CHECK (verification_status IN ('pending','verified','rejected','needs_correction')),
    remarks             TEXT,
    uploaded_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at         TIMESTAMPTZ,
    reviewed_by         UUID,
    UNIQUE (seller_id, document_type)
);

CREATE TABLE IF NOT EXISTS seller_fulfillment_settings (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id            UUID NOT NULL UNIQUE REFERENCES sellers(id) ON DELETE CASCADE,
    pickup_address_id    UUID REFERENCES seller_addresses(id),
    warehouse_address_id UUID REFERENCES seller_addresses(id),
    delivery_modes       TEXT[] NOT NULL DEFAULT '{"platform"}',
    shipping_regions_json JSONB,
    dispatch_sla_hours   INT NOT NULL DEFAULT 48,
    return_supported     BOOLEAN NOT NULL DEFAULT TRUE,
    return_window_days   INT NOT NULL DEFAULT 7,
    cod_enabled          BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS seller_onboarding_reviews (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    seller_id       UUID NOT NULL REFERENCES sellers(id) ON DELETE CASCADE,
    action          TEXT NOT NULL
                        CHECK (action IN ('approve','reject','request_changes','suspend','unsuspend','reopen')),
    notes           TEXT,
    actor_user_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS product_moderation_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id      UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    action          TEXT NOT NULL
                        CHECK (action IN ('approve','reject','request_changes','suspend','archive')),
    reason          TEXT,
    actor_user_id   UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS invoice_sequences (
    financial_year TEXT PRIMARY KEY,
    last_sequence  BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS shipment_events (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    shipment_id  UUID NOT NULL REFERENCES shipments(id) ON DELETE CASCADE,
    status       TEXT NOT NULL,
    location     TEXT,
    remark       TEXT,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    idempotency_key TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);

```

## API types (request/response Go structs with JSON tags)
```go
type productPreviewResponse struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Slug               string  `json:"slug"`
	PrimaryImageMediaID *string `json:"primary_image_media_id,omitempty"` // client resolves via media-service
	Price              float64 `json:"price,omitempty"`
	Currency           string  `json:"currency,omitempty"` // ISO 4217
	Status             string  `json:"status"`
	Visibility         string  `json:"visibility"`
}

type initiateBulkUploadReq struct {
	Filename string `json:"filename" binding:"required"`
}

type createProductReq struct {
	CategoryID       *uuid.UUID `json:"category_id"`
	BrandID          *uuid.UUID `json:"brand_id"`
	TaxClassID       *uuid.UUID `json:"tax_class_id"`
	Title            string     `json:"title" binding:"required"`
	ShortTitle       *string    `json:"short_title"`
	Description      *string    `json:"description"`
	ShortDescription *string    `json:"short_description"`
	BrandName        *string    `json:"brand_name"`
	ManufacturerName *string    `json:"manufacturer_name"`
	ProductType      string     `json:"product_type"`
	Condition        string     `json:"condition"`
	ReturnPolicyType string     `json:"return_policy_type"`
	ReturnPolicyDays int        `json:"return_policy_days"`
	HSNCode          *string    `json:"hsn_code"`
	// Logistics + legal-metrology (Phase 3.1 — schema has the columns).
	PrimaryImageMediaID *uuid.UUID         `json:"primary_image_media_id"`
	VideoMediaID        *uuid.UUID         `json:"video_media_id"`
	WeightGrams         *int               `json:"weight_grams"`
	LengthCm            *float64           `json:"length_cm"`
	WidthCm             *float64           `json:"width_cm"`
	HeightCm            *float64           `json:"height_cm"`
	CountryOfOrigin     *string            `json:"country_of_origin"`
	WarrantyInfo        *string            `json:"warranty_info"`
	SearchKeywords      []string           `json:"search_keywords"`
	MetaTitle           *string            `json:"meta_title"`
	MetaDescription     *string            `json:"meta_description"`
	Variants            []createVariantReq `json:"variants" binding:"required,min=1"`
}

type createVariantReq struct {
	SKU          string   `json:"sku" binding:"required"`
	Option1Name  *string  `json:"option_1_name"`
	Option1Value *string  `json:"option_1_value"`
	Option2Name  *string  `json:"option_2_name"`
	Option2Value *string  `json:"option_2_value"`
	Option3Name  *string  `json:"option_3_name"`
	Option3Value *string  `json:"option_3_value"`
	MRP          float64  `json:"mrp" binding:"required"`
	SellingPrice float64  `json:"selling_price" binding:"required"`
	CostPrice    *float64 `json:"cost_price"`
	StockQty     int      `json:"stock_qty"`
}

type addProductMediaReq struct {
	MediaID   uuid.UUID `json:"media_id" binding:"required"`
	MediaType string    `json:"media_type"`
	SortOrder int       `json:"sort_order"`
}

type setProductAttrsReq struct {
	Attributes []productAttrPayload `json:"attributes"`
}

type productAttrPayload struct {
	Name  string  `json:"name" binding:"required"`
	Value string  `json:"value" binding:"required"`
	Unit  *string `json:"unit"`
}

type addVariantReq struct {
	SKU          string   `json:"sku" binding:"required"`
	Barcode      *string  `json:"barcode"`
	Option1Name  *string  `json:"option_1_name"`
	Option1Value *string  `json:"option_1_value"`
	Option2Name  *string  `json:"option_2_name"`
	Option2Value *string  `json:"option_2_value"`
	Option3Name  *string  `json:"option_3_name"`
	Option3Value *string  `json:"option_3_value"`
	MRP          float64  `json:"mrp" binding:"required"`
	SellingPrice float64  `json:"selling_price" binding:"required"`
	CostPrice    *float64 `json:"cost_price"`
	CurrencyCode string   `json:"currency_code"`
	WeightGrams  *int     `json:"weight_grams"`
}

type priceTierReq struct {
	Tiers []struct {
		MinQty int     `json:"min_qty" binding:"required"`
		MaxQty *int    `json:"max_qty"`
		Price  float64 `json:"price" binding:"required"`
	} `json:"tiers"`
}

type createReviewReq struct {
	SellerID    uuid.UUID `json:"seller_id" binding:"required"`
	OrderItemID uuid.UUID `json:"order_item_id" binding:"required"`
	Rating      int       `json:"rating" binding:"required,min=1,max=5"`
	Title       *string   `json:"title"`
	Body        *string   `json:"body"`
}

type onboardSellerReq struct {
	SellerType  string  `json:"seller_type"`
	StoreName   string  `json:"store_name" binding:"required"`
	BrandName   *string `json:"brand_name"`
	Slug        string  `json:"slug"`
	Description *string `json:"description"`
	Email       string  `json:"email" binding:"required"`
	Phone       *string `json:"phone"`
	GSTNumber   *string `json:"gst_number"`
	State       *string `json:"state"`
	City        *string `json:"city"`
	PostalCode  *string `json:"postal_code"`
}

type addToCartReq struct {
	VariantID uuid.UUID `json:"variant_id" binding:"required"`
	Quantity  int       `json:"quantity" binding:"required,min=1"`
}

type updateCartItemReq struct {
	Quantity int `json:"quantity"`
}

type checkoutReq struct {
	AddressID      uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod  string    `json:"payment_method" binding:"required"`
	CouponCode     string    `json:"coupon_code"`
	GiftMessage    *string   `json:"gift_message"`
	IdempotencyKey string    `json:"idempotency_key"`

	// Phase 5 — optional B2B context.
	OrganizationID *uuid.UUID `json:"organization_id"`
	PONumber       *string    `json:"po_number"`
	CostCenter     *string    `json:"cost_center"`
	InvoiceEmail   *string    `json:"invoice_email"`
}

type quoteReq struct {
	AddressID     uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod string    `json:"payment_method" binding:"required"`
	CouponCode    string    `json:"coupon_code"`
}

type cancelOrderReq struct {
	Reason string `json:"reason"`
}

type confirmPaymentReq struct {
	PaymentIntentID   uuid.UUID `json:"payment_intent_id" binding:"required"`
	RazorpayOrderID   string    `json:"razorpay_order_id" binding:"required"`
	RazorpayPaymentID string    `json:"razorpay_payment_id" binding:"required"`
	RazorpaySignature string    `json:"razorpay_signature" binding:"required"`
	AmountMinor       int64     `json:"amount_minor,omitempty"`
	Gateway           string    `json:"gateway,omitempty"`
}

type createReturnItem struct {
	OrderItemID       uuid.UUID `json:"order_item_id" binding:"required"`
	SellerID          uuid.UUID `json:"seller_id" binding:"required"`
	ReasonCode        string    `json:"reason_code" binding:"required"`
	ReasonDescription *string   `json:"reason_description"`
}

type createReturnReq struct {
	Items           []createReturnItem `json:"items"`
	PickupAddressID *uuid.UUID         `json:"pickup_address_id"`
	// Legacy single-item top-level fields:
	OrderItemID       *uuid.UUID `json:"order_item_id"`
	SellerID          *uuid.UUID `json:"seller_id"`
	ReasonCode        string     `json:"reason_code"`
	ReasonDescription *string    `json:"reason_description"`
}

type sellerResponseReq struct {
	Response string `json:"response" binding:"required"`
}

type rejectReturnReq struct {
	Reason string `json:"reason" binding:"required"`
}

type addAddressReq struct {
	AddressType  string  `json:"address_type"`
	FullName     string  `json:"full_name" binding:"required"`
	Phone        string  `json:"phone" binding:"required"`
	AddressLine1 string  `json:"address_line_1" binding:"required"`
	AddressLine2 *string `json:"address_line_2"`
	Landmark     *string `json:"landmark"`
	City         string  `json:"city" binding:"required"`
	State        string  `json:"state" binding:"required"`
	PostalCode   string  `json:"postal_code" binding:"required"`
	Country      string  `json:"country"`
	IsDefault    bool    `json:"is_default"`
}

type startOnboardingReq struct {
	BusinessPageID *uuid.UUID `json:"business_page_id"`
	StoreName      string     `json:"store_name" binding:"required"`
	Email          string     `json:"email" binding:"required"`
	SellerType     string     `json:"seller_type"`
	BusinessType   string     `json:"business_type"`
}

type saveBasicReq struct {
	StoreName    string  `json:"store_name" binding:"required"`
	OwnerName    string  `json:"owner_name" binding:"required"`
	BusinessType string  `json:"business_type" binding:"required"`
	SellerType   string  `json:"seller_type"`
	Email        string  `json:"email" binding:"required"`
	Phone        *string `json:"phone"`
	State        *string `json:"state"`
	City         *string `json:"city"`
	PostalCode   *string `json:"postal_code"`
	Description  *string `json:"description"`
}

type saveStorefrontReq struct {
	BrandName       *string    `json:"brand_name"`
	LogoMediaID     *uuid.UUID `json:"logo_media_id"`
	BannerMediaID   *uuid.UUID `json:"banner_media_id"`
	Tagline         *string    `json:"tagline"`
	SupportPhone    *string    `json:"support_phone"`
	SupportEmail    *string    `json:"support_email"`
	SocialLinksJSON []byte     `json:"social_links"`
}

type docInput struct {
	DocumentType   string     `json:"document_type" binding:"required"`
	DocumentNumber *string    `json:"document_number"`
	MediaID        uuid.UUID  `json:"media_id" binding:"required"`
}

type saveDocumentsReq struct {
	Documents []docInput `json:"documents" binding:"required,min=1"`
}

type saveFulfillmentReq struct {
	DeliveryModes    []string `json:"delivery_modes"`
	CODEnabled       bool     `json:"cod_enabled"`
	DispatchSLAHours int      `json:"dispatch_sla_hours"`
	ReturnSupported  bool     `json:"return_supported"`
	ReturnWindowDays int      `json:"return_window_days"`
}

type savePayoutReq struct {
	AccountHolderName string  `json:"account_holder_name" binding:"required"`
	BankName          *string `json:"bank_name"`
	AccountNumber     string  `json:"account_number" binding:"required"`
	IFSCCode          *string `json:"ifsc_code"`
	UPIID             *string `json:"upi_id"`
}

type adminActionReq struct {
	Reason  string `json:"reason"`
	Notes   string `json:"notes"`
	Changes string `json:"changes"`
}

type createOrgReq struct {
	Name              string     `json:"name" binding:"required"`
	LegalName         *string    `json:"legal_name"`
	GSTIN             *string    `json:"gstin"`
	PAN               *string    `json:"pan"`
	BillingEmail      *string    `json:"billing_email"`
	BillingPhone      *string    `json:"billing_phone"`
	BillingAddressID  *uuid.UUID `json:"billing_address_id"`
	ApprovalThreshold *float64   `json:"approval_threshold"`
	CreditTermsDays   int        `json:"credit_terms_days"`
	CreditLimit       *float64   `json:"credit_limit"`
}

type updateOrgReq struct {
	Name              string     `json:"name"`
	LegalName         *string    `json:"legal_name"`
	GSTIN             *string    `json:"gstin"`
	PAN               *string    `json:"pan"`
	BillingEmail      *string    `json:"billing_email"`
	BillingPhone      *string    `json:"billing_phone"`
	BillingAddressID  *uuid.UUID `json:"billing_address_id"`
	ApprovalThreshold *float64   `json:"approval_threshold"`
	CreditTermsDays   int        `json:"credit_terms_days"`
	CreditLimit       *float64   `json:"credit_limit"`
}

type inviteMemberReq struct {
	Email string `json:"email" binding:"required"`
	Role  string `json:"role" binding:"required"`
}

type updateMemberRoleReq struct {
	Role string `json:"role" binding:"required"`
}

type approvalActionReq struct {
	Notes  string `json:"notes"`
	Reason string `json:"reason"`
}

type createRFQReq struct {
	OrganizationID *uuid.UUID `json:"organization_id"`
	SellerID       uuid.UUID  `json:"seller_id" binding:"required"`
	Message        *string    `json:"message"`
	Items          []struct {
		VariantID uuid.UUID `json:"variant_id" binding:"required"`
		Quantity  int       `json:"quantity" binding:"required"`
		Notes     *string   `json:"notes"`
	} `json:"items" binding:"required,min=1"`
}

type sendRFQQuoteReq struct {
	ValidityDays int `json:"validity_days" binding:"required"`
	LinePrices   []struct {
		RFQItemID uuid.UUID `json:"rfq_item_id" binding:"required"`
		UnitPrice float64   `json:"unit_price" binding:"required"`
	} `json:"line_prices" binding:"required,min=1"`
}

type acceptRFQQuoteReq struct {
	AddressID      uuid.UUID `json:"address_id" binding:"required"`
	PaymentMethod  string    `json:"payment_method"`
	PONumber       *string   `json:"po_number"`
	CostCenter     *string   `json:"cost_center"`
	InvoiceEmail   *string   `json:"invoice_email"`
	IdempotencyKey string    `json:"idempotency_key"`
}
```
