# Module: food-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
DELETE /addresses/:addressId
DELETE /cart
DELETE /cart/items/:cartItemId
DELETE /item-reviews/:reviewId
DELETE /menu/categories/:categoryId
DELETE /menu/items/:itemId
GET /addresses
GET /admin
GET /assignments
GET /assignments/:assignmentId/tracking
GET /assignments/current
GET /audit-logs
GET /cart
GET /coupons
GET /cuisines
GET /dashboard
GET /delivery-partners/pending
GET /earnings
GET /fraud/top
GET /history
GET /home
GET /me
GET /me/capabilities
GET /me/loyalty
GET /menu-items/:itemId/reviews
GET /me/referral
GET /moderation/queue
GET /:orderId/batch
GET /orders
GET /orders/:orderId
GET /orders/:orderId/invoice
GET /orders/:orderId/messages
GET /orders/:orderId/substitutions
GET /orders/:orderId/tracking
GET /profile
GET /protected
GET /refunds
GET /reports/compliance
GET /reports/coupon-abuse
GET /reports/delivery-sla
GET /reports/orders
GET /reports/payment-recon
GET /reports/refunds
GET /reports/restaurant-sla
GET /reports/revenue
GET /restaurants
GET /restaurants/pending
GET /restaurants/:restaurantId
GET /restaurants/:restaurantId/kitchen-queue
GET /restaurants/:restaurantId/menu
GET /restaurants/:restaurantId/menu/categories
GET /restaurants/:restaurantId/orders
GET /restaurants/:restaurantId/prep-time
GET /restaurants/:restaurantId/reports/summary
GET /restaurants/:restaurantId/settlements
GET /search
GET /service-areas
GET /settlements/delivery-partners
GET /settlements/files
GET /settlements/files/:id/download
GET /settlements/restaurants
GET /support/tickets
GET /support/tickets/me
GET /support/tickets/:ticketId
PATCH /addresses/:addressId
PATCH /cart/items/:cartItemId
PATCH /coupons/:couponId
PATCH /delivery-partners/:partnerId/status
PATCH /menu/categories/:categoryId
PATCH /menu/items/:itemId
PATCH /menu/items/:itemId/availability
PATCH /profile
PATCH /restaurants/:restaurantId
PATCH /restaurants/:restaurantId/status
PATCH /service-areas/:areaId
POST /addresses
POST /assignments/:assignmentId/accept
POST /assignments/:assignmentId/arrived-customer
POST /assignments/:assignmentId/arrived-restaurant
POST /assignments/:assignmentId/delivered
POST /assignments/:assignmentId/picked-up
POST /assignments/:assignmentId/reject
POST /availability
POST /cart/items
POST /coupons
POST /coupons/validate
POST /delivery-partners/:partnerId/approve
POST /delivery-partners/:partnerId/reject
POST /documents
POST /location
POST /me/loyalty/redeem
POST /menu-items/:itemId/report
POST /menu-items/:itemId/reviews
POST /me/referral/apply
POST /moderation/menu-items/:itemId
POST /:offerId/accept
POST /:offerId/reject
POST /:orderId/proof
POST /orders
POST /orders/:orderId/accept
POST /orders/:orderId/cancel
POST /orders/:orderId/mark-preparing
POST /orders/:orderId/mark-ready
POST /orders/:orderId/messages
POST /orders/:orderId/messages/:msgId/read
POST /orders/:orderId/payments/confirm
POST /orders/:orderId/payments/intents
POST /orders/:orderId/ratings/delivery
POST /orders/:orderId/ratings/restaurant
POST /orders/:orderId/refund
POST /orders/:orderId/reject
POST /orders/:orderId/substitutions
POST /orders/:orderId/substitutions/:subId/respond
POST /orders/:orderId/verify-delivery
POST /orders/:orderId/verify-pickup
POST /profile
POST /realtime/token
POST /refunds
POST /refunds/:refundId/decide
POST /restaurants
POST /restaurants/:restaurantId/approve
POST /restaurants/:restaurantId/documents
POST /restaurants/:restaurantId/images
POST /restaurants/:restaurantId/menu/categories
POST /restaurants/:restaurantId/menu/items
POST /restaurants/:restaurantId/reject
POST /service-areas
POST /settlements/delivery-partners/:settlementId/mark-paid
POST /settlements/files
POST /settlements/generate
POST /settlements/restaurants/:settlementId/mark-paid
POST /support/tickets
POST /support/tickets/:ticketId/messages
POST /support/tickets/:ticketId/status
GROUP /admin
GROUP /delivery
GROUP /delivery/offers
GROUP /delivery/orders
GROUP /partner
GROUP /v1/food
```

## Database schema (CREATE TABLE — full column DDL)
```sql
CREATE TABLE IF NOT EXISTS food.restaurant_partners (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_user_id UUID NOT NULL,
    legal_name VARCHAR(200) NOT NULL,
    display_name VARCHAR(200),
    phone VARCHAR(30),
    email VARCHAR(255),
    status food.partner_status NOT NULL DEFAULT 'DRAFT',
    rejection_reason TEXT,
    approved_by UUID,
    approved_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.restaurants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id UUID NOT NULL REFERENCES food.restaurant_partners(id) ON DELETE RESTRICT,
    owner_user_id UUID NOT NULL,
    name VARCHAR(200) NOT NULL,
    slug VARCHAR(220) NOT NULL,
    description TEXT,
    phone VARCHAR(30),
    email VARCHAR(255),
    status food.restaurant_status NOT NULL DEFAULT 'DRAFT',
    is_open BOOLEAN NOT NULL DEFAULT FALSE,
    is_accepting_orders BOOLEAN NOT NULL DEFAULT FALSE,
    address_line1 VARCHAR(255) NOT NULL,
    address_line2 VARCHAR(255),
    city VARCHAR(120) NOT NULL,
    state VARCHAR(120),
    country VARCHAR(120) NOT NULL DEFAULT 'India',
    postal_code VARCHAR(20),
    latitude NUMERIC(10, 7),
    longitude NUMERIC(10, 7),
    avg_rating NUMERIC(3,2) NOT NULL DEFAULT 0,
    rating_count INTEGER NOT NULL DEFAULT 0,
    min_order_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    packaging_fee NUMERIC(12,2) NOT NULL DEFAULT 0,
    avg_preparation_minutes INTEGER NOT NULL DEFAULT 25,
    commission_percentage NUMERIC(5,2) NOT NULL DEFAULT 15.00,
    rejection_reason TEXT,
    approved_by UUID,
    approved_at TIMESTAMPTZ,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_food_restaurant_slug UNIQUE(slug),
    CONSTRAINT ck_food_restaurant_lat CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    CONSTRAINT ck_food_restaurant_lng CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),
    CONSTRAINT ck_food_restaurant_commission CHECK (commission_percentage >= 0 AND commission_percentage <= 100)
);

CREATE TABLE IF NOT EXISTS food.restaurant_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    document_type VARCHAR(80) NOT NULL,
    document_number VARCHAR(100),
    media_id UUID,
    file_url TEXT,
    status food.document_status NOT NULL DEFAULT 'PENDING',
    rejection_reason TEXT,
    expires_at TIMESTAMPTZ,
    verified_by UUID,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.restaurant_images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    media_id UUID,
    image_url TEXT NOT NULL,
    image_type VARCHAR(40) NOT NULL DEFAULT 'gallery',
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.restaurant_operating_hours (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    day_of_week SMALLINT NOT NULL,
    opens_at TIME NOT NULL,
    closes_at TIME NOT NULL,
    is_closed BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_operating_day CHECK (day_of_week BETWEEN 0 AND 6)
);

CREATE TABLE IF NOT EXISTS food.restaurant_service_areas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    area_name VARCHAR(150) NOT NULL,
    city VARCHAR(120),
    postal_code VARCHAR(20),
    radius_km NUMERIC(8,2) NOT NULL DEFAULT 5,
    center_latitude NUMERIC(10,7),
    center_longitude NUMERIC(10,7),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.service_areas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(150) NOT NULL,
    city VARCHAR(120) NOT NULL,
    state VARCHAR(120),
    country VARCHAR(120) NOT NULL DEFAULT 'India',
    postal_code VARCHAR(20),
    center_latitude NUMERIC(10,7),
    center_longitude NUMERIC(10,7),
    radius_km NUMERIC(8,2) NOT NULL DEFAULT 8,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_food_service_area_name_city UNIQUE(name, city),
    CONSTRAINT ck_food_service_area_radius CHECK (radius_km > 0)
);

CREATE TABLE IF NOT EXISTS food.cuisines (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(120) NOT NULL UNIQUE,
    slug VARCHAR(140) NOT NULL UNIQUE,
    image_url TEXT,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.restaurant_cuisines (
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    cuisine_id UUID NOT NULL REFERENCES food.cuisines(id) ON DELETE RESTRICT,
    PRIMARY KEY (restaurant_id, cuisine_id)
);

CREATE TABLE IF NOT EXISTS food.menu_categories (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    description TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.menu_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    category_id UUID REFERENCES food.menu_categories(id) ON DELETE SET NULL,
    name VARCHAR(200) NOT NULL,
    description TEXT,
    food_type food.food_type NOT NULL DEFAULT 'VEG',
    base_price NUMERIC(12,2) NOT NULL,
    discount_price NUMERIC(12,2),
    image_url TEXT,
    media_id UUID,
    preparation_minutes INTEGER NOT NULL DEFAULT 20,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    is_recommended BOOLEAN NOT NULL DEFAULT FALSE,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    tax_percentage NUMERIC(5,2) NOT NULL DEFAULT 0,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_menu_item_base_price CHECK (base_price >= 0),
    CONSTRAINT ck_food_menu_item_discount_price CHECK (discount_price IS NULL OR discount_price >= 0),
    CONSTRAINT ck_food_menu_item_tax CHECK (tax_percentage >= 0 AND tax_percentage <= 100)
);

CREATE TABLE IF NOT EXISTS food.menu_item_variants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_item_id UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE CASCADE,
    name VARCHAR(120) NOT NULL,
    price NUMERIC(12,2) NOT NULL,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_variant_price CHECK (price >= 0)
);

CREATE TABLE IF NOT EXISTS food.menu_item_addon_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_item_id UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    min_select INTEGER NOT NULL DEFAULT 0,
    max_select INTEGER NOT NULL DEFAULT 1,
    is_required BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_addon_group_select CHECK (min_select >= 0 AND max_select >= min_select)
);

CREATE TABLE IF NOT EXISTS food.menu_item_addons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    addon_group_id UUID NOT NULL REFERENCES food.menu_item_addon_groups(id) ON DELETE CASCADE,
    name VARCHAR(150) NOT NULL,
    price NUMERIC(12,2) NOT NULL DEFAULT 0,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_addon_price CHECK (price >= 0)
);

CREATE TABLE IF NOT EXISTS food.delivery_partners (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE,
    full_name VARCHAR(200) NOT NULL,
    phone VARCHAR(30) NOT NULL,
    email VARCHAR(255),
    status food.delivery_partner_status NOT NULL DEFAULT 'DRAFT',
    vehicle_type VARCHAR(50),
    vehicle_number VARCHAR(50),
    city VARCHAR(120),
    current_latitude NUMERIC(10,7),
    current_longitude NUMERIC(10,7),
    is_online BOOLEAN NOT NULL DEFAULT FALSE,
    approved_by UUID,
    approved_at TIMESTAMPTZ,
    rejection_reason TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.delivery_partner_availability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    is_online BOOLEAN NOT NULL,
    changed_by UUID,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.delivery_partner_documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    document_type VARCHAR(80) NOT NULL,
    document_number VARCHAR(100),
    media_id UUID,
    file_url TEXT,
    status food.document_status NOT NULL DEFAULT 'PENDING',
    rejection_reason TEXT,
    expires_at TIMESTAMPTZ,
    verified_by UUID,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.delivery_partner_locations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    latitude NUMERIC(10,7) NOT NULL,
    longitude NUMERIC(10,7) NOT NULL,
    accuracy_meters NUMERIC(8,2),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.customer_addresses (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    label VARCHAR(80),
    receiver_name VARCHAR(200),
    phone VARCHAR(30),
    address_line1 VARCHAR(255) NOT NULL,
    address_line2 VARCHAR(255),
    landmark VARCHAR(255),
    city VARCHAR(120) NOT NULL,
    state VARCHAR(120),
    country VARCHAR(120) NOT NULL DEFAULT 'India',
    postal_code VARCHAR(20),
    latitude NUMERIC(10,7),
    longitude NUMERIC(10,7),
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.carts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE,
    restaurant_id UUID REFERENCES food.restaurants(id) ON DELETE SET NULL,
    coupon_code VARCHAR(80),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.cart_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_id UUID NOT NULL REFERENCES food.carts(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    menu_item_id UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE RESTRICT,
    variant_id UUID REFERENCES food.menu_item_variants(id) ON DELETE SET NULL,
    quantity INTEGER NOT NULL,
    item_instruction TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_cart_item_qty CHECK (quantity > 0)
);

CREATE TABLE IF NOT EXISTS food.cart_item_addons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_item_id UUID NOT NULL REFERENCES food.cart_items(id) ON DELETE CASCADE,
    addon_id UUID NOT NULL REFERENCES food.menu_item_addons(id) ON DELETE RESTRICT,
    quantity INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_cart_addon_qty CHECK (quantity > 0)
);

CREATE TABLE IF NOT EXISTS food.coupons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(80) NOT NULL UNIQUE,
    title VARCHAR(150) NOT NULL,
    description TEXT,
    coupon_type food.coupon_type NOT NULL,
    discount_value NUMERIC(12,2) NOT NULL,
    max_discount_amount NUMERIC(12,2),
    min_order_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    restaurant_id UUID REFERENCES food.restaurants(id) ON DELETE CASCADE,
    total_usage_limit INTEGER,
    per_user_usage_limit INTEGER NOT NULL DEFAULT 1,
    used_count INTEGER NOT NULL DEFAULT 0,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    funded_by VARCHAR(30) NOT NULL DEFAULT 'PLATFORM',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_coupon_discount CHECK (discount_value >= 0),
    CONSTRAINT ck_food_coupon_dates CHECK (ends_at > starts_at)
);

CREATE TABLE IF NOT EXISTS food.orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_number VARCHAR(40) NOT NULL UNIQUE,
    user_id UUID NOT NULL,
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE RESTRICT,
    customer_address_id UUID REFERENCES food.customer_addresses(id) ON DELETE SET NULL,
    status food.order_status NOT NULL DEFAULT 'PLACED',
    payment_status food.payment_status NOT NULL DEFAULT 'PENDING',
    payment_method food.payment_method NOT NULL DEFAULT 'ONLINE',

    restaurant_name_snapshot VARCHAR(200) NOT NULL,
    restaurant_address_snapshot JSONB NOT NULL,
    delivery_address_snapshot JSONB NOT NULL,

    item_subtotal NUMERIC(12,2) NOT NULL DEFAULT 0,
    addon_total NUMERIC(12,2) NOT NULL DEFAULT 0,
    packaging_fee NUMERIC(12,2) NOT NULL DEFAULT 0,
    tax_total NUMERIC(12,2) NOT NULL DEFAULT 0,
    delivery_fee NUMERIC(12,2) NOT NULL DEFAULT 0,
    platform_fee NUMERIC(12,2) NOT NULL DEFAULT 0,
    restaurant_discount NUMERIC(12,2) NOT NULL DEFAULT 0,
    coupon_discount NUMERIC(12,2) NOT NULL DEFAULT 0,
    final_amount NUMERIC(12,2) NOT NULL DEFAULT 0,

    coupon_code VARCHAR(80),
    commission_percentage_snapshot NUMERIC(5,2) NOT NULL DEFAULT 0,
    commission_amount NUMERIC(12,2) NOT NULL DEFAULT 0,

    estimated_preparation_minutes INTEGER,
    estimated_delivery_minutes INTEGER,
    customer_instruction TEXT,
    cancellation_reason TEXT,
    cancelled_by UUID,
    cancelled_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,

    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    placed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT ck_food_order_amounts CHECK (
        item_subtotal >= 0 AND addon_total >= 0 AND packaging_fee >= 0 AND tax_total >= 0
        AND delivery_fee >= 0 AND platform_fee >= 0 AND restaurant_discount >= 0
        AND coupon_discount >= 0 AND final_amount >= 0
    )
);

CREATE TABLE IF NOT EXISTS food.order_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    menu_item_id UUID REFERENCES food.menu_items(id) ON DELETE SET NULL,
    variant_id UUID REFERENCES food.menu_item_variants(id) ON DELETE SET NULL,
    item_name_snapshot VARCHAR(200) NOT NULL,
    variant_name_snapshot VARCHAR(120),
    food_type_snapshot food.food_type NOT NULL,
    unit_price_snapshot NUMERIC(12,2) NOT NULL,
    quantity INTEGER NOT NULL,
    tax_percentage_snapshot NUMERIC(5,2) NOT NULL DEFAULT 0,
    tax_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    line_total NUMERIC(12,2) NOT NULL,
    item_instruction TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_order_item_qty CHECK (quantity > 0),
    CONSTRAINT ck_food_order_item_prices CHECK (unit_price_snapshot >= 0 AND tax_amount >= 0 AND line_total >= 0)
);

CREATE TABLE IF NOT EXISTS food.order_item_addons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_item_id UUID NOT NULL REFERENCES food.order_items(id) ON DELETE CASCADE,
    addon_id UUID REFERENCES food.menu_item_addons(id) ON DELETE SET NULL,
    addon_name_snapshot VARCHAR(150) NOT NULL,
    unit_price_snapshot NUMERIC(12,2) NOT NULL,
    quantity INTEGER NOT NULL DEFAULT 1,
    line_total NUMERIC(12,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_order_addon_qty CHECK (quantity > 0),
    CONSTRAINT ck_food_order_addon_price CHECK (unit_price_snapshot >= 0 AND line_total >= 0)
);

CREATE TABLE IF NOT EXISTS food.order_status_history (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    from_status food.order_status,
    to_status food.order_status NOT NULL,
    changed_by UUID,
    reason TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.payments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    payment_method food.payment_method NOT NULL,
    status food.payment_status NOT NULL DEFAULT 'PENDING',
    provider VARCHAR(80),
    provider_payment_id VARCHAR(200),
    provider_order_id VARCHAR(200),
    amount NUMERIC(12,2) NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'INR',
    raw_response JSONB NOT NULL DEFAULT '{}'::jsonb,
    paid_at TIMESTAMPTZ,
    failed_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_payment_amount CHECK (amount >= 0)
);

CREATE TABLE IF NOT EXISTS food.refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    payment_id UUID REFERENCES food.payments(id) ON DELETE SET NULL,
    amount NUMERIC(12,2) NOT NULL,
    reason TEXT NOT NULL,
    status VARCHAR(40) NOT NULL DEFAULT 'PENDING',
    provider_refund_id VARCHAR(200),
    requested_by UUID,
    processed_by UUID,
    processed_at TIMESTAMPTZ,
    raw_response JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_refund_amount CHECK (amount >= 0)
);

CREATE TABLE IF NOT EXISTS food.delivery_assignments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL UNIQUE REFERENCES food.orders(id) ON DELETE CASCADE,
    delivery_partner_id UUID REFERENCES food.delivery_partners(id) ON DELETE SET NULL,
    status food.assignment_status NOT NULL DEFAULT 'CREATED',
    pickup_code VARCHAR(20),
    delivery_code VARCHAR(20),
    distance_km NUMERIC(8,2),
    delivery_fee NUMERIC(12,2) NOT NULL DEFAULT 0,
    delivery_partner_payout NUMERIC(12,2) NOT NULL DEFAULT 0,
    assigned_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    picked_up_at TIMESTAMPTZ,
    delivered_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    failure_reason TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.delivery_tracking_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    assignment_id UUID NOT NULL REFERENCES food.delivery_assignments(id) ON DELETE CASCADE,
    delivery_partner_id UUID REFERENCES food.delivery_partners(id) ON DELETE SET NULL,
    status food.assignment_status NOT NULL,
    latitude NUMERIC(10,7),
    longitude NUMERIC(10,7),
    note TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.coupon_redemptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id UUID NOT NULL REFERENCES food.coupons(id) ON DELETE RESTRICT,
    order_id UUID NOT NULL UNIQUE REFERENCES food.orders(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    discount_amount NUMERIC(12,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_coupon_redemption_discount CHECK (discount_amount >= 0)
);

CREATE TABLE IF NOT EXISTS food.restaurant_ratings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL UNIQUE REFERENCES food.orders(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    rating SMALLINT NOT NULL,
    review TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_restaurant_rating CHECK (rating BETWEEN 1 AND 5)
);

CREATE TABLE IF NOT EXISTS food.delivery_ratings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID NOT NULL UNIQUE REFERENCES food.orders(id) ON DELETE CASCADE,
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    rating SMALLINT NOT NULL,
    review TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_delivery_rating CHECK (rating BETWEEN 1 AND 5)
);

CREATE TABLE IF NOT EXISTS food.restaurant_settlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE RESTRICT,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    gross_order_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    commission_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    refund_adjustment NUMERIC(12,2) NOT NULL DEFAULT 0,
    penalty_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    payout_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    status food.settlement_status NOT NULL DEFAULT 'PENDING',
    paid_reference VARCHAR(200),
    paid_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_restaurant_settlement_dates CHECK (period_end >= period_start)
);

CREATE TABLE IF NOT EXISTS food.delivery_partner_settlements (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE RESTRICT,
    period_start DATE NOT NULL,
    period_end DATE NOT NULL,
    delivery_count INTEGER NOT NULL DEFAULT 0,
    gross_earning_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    incentive_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    penalty_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    payout_amount NUMERIC(12,2) NOT NULL DEFAULT 0,
    status food.settlement_status NOT NULL DEFAULT 'PENDING',
    paid_reference VARCHAR(200),
    paid_at TIMESTAMPTZ,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_delivery_settlement_dates CHECK (period_end >= period_start)
);

CREATE TABLE IF NOT EXISTS food.admin_audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id UUID NOT NULL,
    action VARCHAR(120) NOT NULL,
    entity_type VARCHAR(80) NOT NULL,
    entity_id UUID,
    old_value JSONB,
    new_value JSONB,
    ip_address VARCHAR(80),
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.idempotency_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID,
    key VARCHAR(120) NOT NULL,
    request_hash VARCHAR(128),
    response_status INTEGER,
    response_body JSONB,
    locked_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ,
    CONSTRAINT uq_food_idempotency_user_key UNIQUE(user_id, key)
);

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS food.menu_item_stockouts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_item_id  UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    is_available  BOOLEAN NOT NULL,
    reason        VARCHAR(120),
    changed_by    UUID,
    changed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.order_substitutions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    original_item_id    UUID NOT NULL,
    original_item_name  VARCHAR(200) NOT NULL,
    suggested_item_id   UUID,
    suggested_item_name VARCHAR(200),
    price_diff          NUMERIC(10,2) NOT NULL DEFAULT 0,
    note                TEXT,
    status              food.substitution_status NOT NULL DEFAULT 'proposed',
    proposed_by         UUID NOT NULL,
    responded_by        UUID,
    responded_at        TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.menu_item_reports (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_item_id UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE CASCADE,
    reporter_id  UUID NOT NULL,
    category     VARCHAR(60) NOT NULL,
    detail       TEXT,
    resolved_at  TIMESTAMPTZ,
    resolved_by  UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.delivery_offers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id            UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    status              food.delivery_offer_status NOT NULL DEFAULT 'pending',
    distance_km         NUMERIC(8,2),
    expires_at          TIMESTAMPTZ NOT NULL,
    responded_at        TIMESTAMPTZ,
    reject_reason       VARCHAR(120),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(order_id, delivery_partner_id)
);

CREATE TABLE IF NOT EXISTS food.support_tickets (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id   UUID NOT NULL,
    order_id      UUID REFERENCES food.orders(id) ON DELETE SET NULL,
    category      VARCHAR(60) NOT NULL,
    subject       VARCHAR(200) NOT NULL,
    detail        TEXT,
    status        food.ticket_status NOT NULL DEFAULT 'open',
    assigned_to   UUID,
    resolved_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.ticket_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id   UUID NOT NULL REFERENCES food.support_tickets(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL,
    is_admin    BOOLEAN NOT NULL DEFAULT FALSE,
    body        TEXT NOT NULL,
    attachments JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.refund_requests (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id     UUID REFERENCES food.support_tickets(id) ON DELETE CASCADE,
    order_id      UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    customer_id   UUID NOT NULL,
    amount        NUMERIC(10,2) NOT NULL CHECK (amount >= 0),
    reason        TEXT,
    status        food.refund_status NOT NULL DEFAULT 'requested',
    decided_by    UUID,
    decided_at    TIMESTAMPTZ,
    refund_txn_id TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.item_reviews (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id      UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    menu_item_id  UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE CASCADE,
    customer_id   UUID NOT NULL,
    rating        SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    review        TEXT,
    photo_urls    JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(order_id, menu_item_id, customer_id)
);

CREATE TABLE IF NOT EXISTS food.fraud_scores (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    signal      VARCHAR(60) NOT NULL,
    score       NUMERIC(6,2) NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.settlement_files (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    period_start  DATE NOT NULL,
    period_end    DATE NOT NULL,
    kind          VARCHAR(32) NOT NULL CHECK (kind IN ('restaurant','delivery')),
    file_url      TEXT NOT NULL,
    row_count     INTEGER NOT NULL DEFAULT 0,
    total_amount  NUMERIC(12,2) NOT NULL DEFAULT 0,
    generated_by  UUID,
    generated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.order_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL,
    author_role VARCHAR(16) NOT NULL CHECK (author_role IN ('customer','restaurant','delivery','admin')),
    body        TEXT NOT NULL,
    read_by     JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.invoice_sequences (
    financial_year VARCHAR(10) PRIMARY KEY,
    last_number    BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.loyalty_balances (
    user_id          UUID PRIMARY KEY,
    points_balance   INTEGER NOT NULL DEFAULT 0,
    lifetime_earned  INTEGER NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.loyalty_ledger (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    order_id    UUID REFERENCES food.orders(id) ON DELETE SET NULL,
    delta       INTEGER NOT NULL,   -- +earn, -redeem
    reason      VARCHAR(60) NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.referral_codes (
    user_id     UUID PRIMARY KEY,
    code        VARCHAR(20) NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS food.referrals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id  UUID NOT NULL,
    referee_id   UUID NOT NULL,
    code_used    VARCHAR(20) NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending','rewarded','rejected')),
    rewarded_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(referee_id) -- one referrer per referee
);

CREATE TABLE IF NOT EXISTS food.delivery_batches (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','assigned','cancelled','completed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    assigned_at   TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ
);

```

## API types (request/response Go structs with JSON tags)
```go
type RejectDeliveryOfferRequest struct {
	Reason string `json:"reason,omitempty"`
}

type VerifyOTPRequest struct {
	Code string `json:"code"`
}

type AttachProofRequest struct {
	Which string `json:"which"` // pickup | delivery
	URL   string `json:"url"`
}

type CreateItemReviewRequest struct {
	OrderID    uuid.UUID `json:"order_id"`
	MenuItemID uuid.UUID `json:"menu_item_id"`
	Rating     int       `json:"rating"`
	Review     string    `json:"review,omitempty"`
	PhotoURLs  []string  `json:"photo_urls,omitempty"`
}

type ReportMenuItemRequest struct {
	Category string `json:"category"`
	Detail   string `json:"detail,omitempty"`
}

type AdminModerateMenuItemRequest struct {
	Status string `json:"status"` // approved | rejected | pending_review | flagged
	Reason string `json:"reason,omitempty"`
}

type AppendOrderMessageRequest struct {
	Body string `json:"body"`
}

type MarkOrderMessageReadRequest struct {
	Role string `json:"role"` // customer | restaurant | delivery | admin
}

type RedeemLoyaltyRequest struct {
	OrderID *uuid.UUID `json:"order_id,omitempty"`
	Points  int        `json:"points"`
}

type ApplyReferralRequest struct {
	Code string `json:"code"`
}

type AdminGenerateSettlementFileRequest struct {
	Kind        string `json:"kind"`
	PeriodStart string `json:"period_start"`
	PeriodEnd   string `json:"period_end"`
}

type ProposeSubstitutionRequest struct {
	OriginalItemID    uuid.UUID  `json:"original_item_id"`
	SuggestedItemID   *uuid.UUID `json:"suggested_item_id,omitempty"`
	SuggestedItemName *string    `json:"suggested_item_name,omitempty"`
	PriceDiff         float64    `json:"price_diff"`
	Note              *string    `json:"note,omitempty"`
}

type RespondSubstitutionRequest struct {
	Response string `json:"response"` // approved | declined | cancelled
}

type CreateTicketRequest struct {
	OrderID  *uuid.UUID `json:"order_id,omitempty"`
	Category string     `json:"category"`
	Subject  string     `json:"subject"`
	Detail   string     `json:"detail,omitempty"`
}

type AppendMessageRequest struct {
	Body string `json:"body"`
}

type AdminSetTicketStatusRequest struct {
	Status string `json:"status"`
}

type CustomerRefundRequest struct {
	OrderID  uuid.UUID  `json:"order_id"`
	TicketID *uuid.UUID `json:"ticket_id,omitempty"`
	Amount   float64    `json:"amount"`
	Reason   string     `json:"reason,omitempty"`
}

type AdminDecideRefundRequest struct {
	Status string `json:"status"` // approved | rejected
	Reason string `json:"reason,omitempty"`
}
```
