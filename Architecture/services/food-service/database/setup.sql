-- ============================================================
-- Postbook Food Mini App - PostgreSQL Schema
-- Schema: food
-- Recommended DB: PostgreSQL 15+
-- ============================================================

CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Optional but recommended for geo queries.
-- Enable only if PostGIS is installed in your environment.
-- CREATE EXTENSION IF NOT EXISTS postgis;

CREATE SCHEMA IF NOT EXISTS food;

-- ============================================================
-- ENUMS
-- ============================================================

DO $$ BEGIN
    CREATE TYPE food.partner_status AS ENUM (
        'DRAFT',
        'PENDING_REVIEW',
        'APPROVED',
        'REJECTED',
        'SUSPENDED',
        'CLOSED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.restaurant_status AS ENUM (
        'DRAFT',
        'PENDING_REVIEW',
        'APPROVED',
        'REJECTED',
        'ACTIVE',
        'INACTIVE',
        'TEMP_CLOSED',
        'SUSPENDED',
        'CLOSED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.document_status AS ENUM (
        'PENDING',
        'APPROVED',
        'REJECTED',
        'EXPIRED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.food_type AS ENUM (
        'VEG',
        'NON_VEG',
        'EGG',
        'VEGAN',
        'JAIN'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.order_status AS ENUM (
        'DRAFT',
        'PLACED',
        'PAYMENT_PENDING',
        'PAYMENT_FAILED',
        'CONFIRMED',
        'RESTAURANT_REJECTED',
        'PREPARING',
        'READY_FOR_PICKUP',
        'DELIVERY_ASSIGNING',
        'DELIVERY_ASSIGNED',
        'PICKED_UP',
        'OUT_FOR_DELIVERY',
        'DELIVERED',
        'CANCELLED_BY_CUSTOMER',
        'CANCELLED_BY_RESTAURANT',
        'CANCELLED_BY_ADMIN',
        'REFUND_PENDING',
        'REFUNDED',
        'FAILED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.payment_status AS ENUM (
        'NOT_REQUIRED',
        'PENDING',
        'AUTHORIZED',
        'CAPTURED',
        'FAILED',
        'REFUND_PENDING',
        'PARTIALLY_REFUNDED',
        'REFUNDED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.payment_method AS ENUM (
        'ONLINE',
        'COD',
        'WALLET'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.delivery_partner_status AS ENUM (
        'DRAFT',
        'PENDING_REVIEW',
        'APPROVED',
        'REJECTED',
        'ACTIVE',
        'OFFLINE',
        'SUSPENDED',
        'CLOSED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.assignment_status AS ENUM (
        'CREATED',
        'ASSIGNED',
        'ACCEPTED',
        'REJECTED',
        'ARRIVED_AT_RESTAURANT',
        'PICKED_UP',
        'ARRIVED_AT_CUSTOMER',
        'DELIVERED',
        'FAILED',
        'CANCELLED'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.coupon_type AS ENUM (
        'PERCENTAGE',
        'FLAT'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.settlement_status AS ENUM (
        'PENDING',
        'PROCESSING',
        'PAID',
        'FAILED',
        'ON_HOLD'
    );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ============================================================
-- COMMON UPDATED_AT TRIGGER
-- ============================================================

CREATE OR REPLACE FUNCTION food.set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- ============================================================
-- RESTAURANT PARTNER AND RESTAURANTS
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_restaurant_partners_owner ON food.restaurant_partners(owner_user_id);
CREATE INDEX IF NOT EXISTS ix_food_restaurant_partners_status ON food.restaurant_partners(status);

DROP TRIGGER IF EXISTS trg_restaurant_partners_updated_at ON food.restaurant_partners;
CREATE TRIGGER trg_restaurant_partners_updated_at
BEFORE UPDATE ON food.restaurant_partners
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_restaurants_partner ON food.restaurants(partner_id);
CREATE INDEX IF NOT EXISTS ix_food_restaurants_owner ON food.restaurants(owner_user_id);
CREATE INDEX IF NOT EXISTS ix_food_restaurants_status ON food.restaurants(status);
CREATE INDEX IF NOT EXISTS ix_food_restaurants_city ON food.restaurants(city);
CREATE INDEX IF NOT EXISTS ix_food_restaurants_location ON food.restaurants(latitude, longitude);

DROP TRIGGER IF EXISTS trg_restaurants_updated_at ON food.restaurants;
CREATE TRIGGER trg_restaurants_updated_at
BEFORE UPDATE ON food.restaurants
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_restaurant_documents_restaurant ON food.restaurant_documents(restaurant_id);
CREATE INDEX IF NOT EXISTS ix_food_restaurant_documents_status ON food.restaurant_documents(status);

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

CREATE INDEX IF NOT EXISTS ix_food_restaurant_images_restaurant ON food.restaurant_images(restaurant_id);

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

CREATE INDEX IF NOT EXISTS ix_food_operating_hours_restaurant_day ON food.restaurant_operating_hours(restaurant_id, day_of_week);

DROP TRIGGER IF EXISTS trg_operating_hours_updated_at ON food.restaurant_operating_hours;
CREATE TRIGGER trg_operating_hours_updated_at
BEFORE UPDATE ON food.restaurant_operating_hours
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_service_areas_restaurant ON food.restaurant_service_areas(restaurant_id);
CREATE INDEX IF NOT EXISTS ix_food_service_areas_city ON food.restaurant_service_areas(city);

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

CREATE INDEX IF NOT EXISTS ix_food_service_areas_active_city ON food.service_areas(is_active, city);

DROP TRIGGER IF EXISTS trg_service_areas_updated_at ON food.service_areas;
CREATE TRIGGER trg_service_areas_updated_at
BEFORE UPDATE ON food.service_areas
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

-- ============================================================
-- CUISINES AND MENU
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_menu_categories_restaurant ON food.menu_categories(restaurant_id);

DROP TRIGGER IF EXISTS trg_menu_categories_updated_at ON food.menu_categories;
CREATE TRIGGER trg_menu_categories_updated_at
BEFORE UPDATE ON food.menu_categories
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_menu_items_restaurant ON food.menu_items(restaurant_id);
CREATE INDEX IF NOT EXISTS ix_food_menu_items_category ON food.menu_items(category_id);
CREATE INDEX IF NOT EXISTS ix_food_menu_items_available ON food.menu_items(is_available, is_active);

DROP TRIGGER IF EXISTS trg_menu_items_updated_at ON food.menu_items;
CREATE TRIGGER trg_menu_items_updated_at
BEFORE UPDATE ON food.menu_items
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_variants_item ON food.menu_item_variants(menu_item_id);

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

CREATE INDEX IF NOT EXISTS ix_food_addon_groups_item ON food.menu_item_addon_groups(menu_item_id);

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

CREATE INDEX IF NOT EXISTS ix_food_addons_group ON food.menu_item_addons(addon_group_id);

-- ============================================================
-- DELIVERY PARTNERS
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_delivery_partners_status ON food.delivery_partners(status);
CREATE INDEX IF NOT EXISTS ix_food_delivery_partners_city ON food.delivery_partners(city);
CREATE INDEX IF NOT EXISTS ix_food_delivery_partners_online ON food.delivery_partners(is_online);

DROP TRIGGER IF EXISTS trg_delivery_partners_updated_at ON food.delivery_partners;
CREATE TRIGGER trg_delivery_partners_updated_at
BEFORE UPDATE ON food.delivery_partners
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

CREATE TABLE IF NOT EXISTS food.delivery_partner_availability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    is_online BOOLEAN NOT NULL,
    changed_by UUID,
    reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ix_food_delivery_availability_partner_time ON food.delivery_partner_availability(delivery_partner_id, created_at DESC);

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

CREATE INDEX IF NOT EXISTS ix_food_delivery_docs_partner ON food.delivery_partner_documents(delivery_partner_id);

CREATE TABLE IF NOT EXISTS food.delivery_partner_locations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    delivery_partner_id UUID NOT NULL REFERENCES food.delivery_partners(id) ON DELETE CASCADE,
    latitude NUMERIC(10,7) NOT NULL,
    longitude NUMERIC(10,7) NOT NULL,
    accuracy_meters NUMERIC(8,2),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ix_food_delivery_locations_partner_time ON food.delivery_partner_locations(delivery_partner_id, recorded_at DESC);

-- ============================================================
-- CUSTOMER ADDRESS, CART
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_customer_addresses_user ON food.customer_addresses(user_id, is_deleted);

DROP TRIGGER IF EXISTS trg_customer_addresses_updated_at ON food.customer_addresses;
CREATE TRIGGER trg_customer_addresses_updated_at
BEFORE UPDATE ON food.customer_addresses
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

CREATE TABLE IF NOT EXISTS food.carts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL UNIQUE,
    restaurant_id UUID REFERENCES food.restaurants(id) ON DELETE SET NULL,
    coupon_code VARCHAR(80),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ix_food_carts_user ON food.carts(user_id);

DROP TRIGGER IF EXISTS trg_carts_updated_at ON food.carts;
CREATE TRIGGER trg_carts_updated_at
BEFORE UPDATE ON food.carts
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_cart_items_cart ON food.cart_items(cart_id);

DROP TRIGGER IF EXISTS trg_cart_items_updated_at ON food.cart_items;
CREATE TRIGGER trg_cart_items_updated_at
BEFORE UPDATE ON food.cart_items
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

CREATE TABLE IF NOT EXISTS food.cart_item_addons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    cart_item_id UUID NOT NULL REFERENCES food.cart_items(id) ON DELETE CASCADE,
    addon_id UUID NOT NULL REFERENCES food.menu_item_addons(id) ON DELETE RESTRICT,
    quantity INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_cart_addon_qty CHECK (quantity > 0)
);

CREATE INDEX IF NOT EXISTS ix_food_cart_item_addons_item ON food.cart_item_addons(cart_item_id);

-- ============================================================
-- COUPONS
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_coupons_code ON food.coupons(code);
CREATE INDEX IF NOT EXISTS ix_food_coupons_restaurant ON food.coupons(restaurant_id);
CREATE INDEX IF NOT EXISTS ix_food_coupons_active ON food.coupons(is_active, starts_at, ends_at);

DROP TRIGGER IF EXISTS trg_coupons_updated_at ON food.coupons;
CREATE TRIGGER trg_coupons_updated_at
BEFORE UPDATE ON food.coupons
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

-- ============================================================
-- ORDERS
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_orders_user_created ON food.orders(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_orders_restaurant_created ON food.orders(restaurant_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_orders_status ON food.orders(status);
CREATE INDEX IF NOT EXISTS ix_food_orders_payment_status ON food.orders(payment_status);
CREATE INDEX IF NOT EXISTS ix_food_orders_created ON food.orders(created_at DESC);

DROP TRIGGER IF EXISTS trg_orders_updated_at ON food.orders;
CREATE TRIGGER trg_orders_updated_at
BEFORE UPDATE ON food.orders
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_order_items_order ON food.order_items(order_id);

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

CREATE INDEX IF NOT EXISTS ix_food_order_item_addons_order_item ON food.order_item_addons(order_item_id);

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

CREATE INDEX IF NOT EXISTS ix_food_order_status_history_order ON food.order_status_history(order_id, created_at DESC);

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

CREATE INDEX IF NOT EXISTS ix_food_payments_order ON food.payments(order_id);
CREATE INDEX IF NOT EXISTS ix_food_payments_provider_payment ON food.payments(provider_payment_id);

DROP TRIGGER IF EXISTS trg_payments_updated_at ON food.payments;
CREATE TRIGGER trg_payments_updated_at
BEFORE UPDATE ON food.payments
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_refunds_order ON food.refunds(order_id);

-- ============================================================
-- DELIVERY ASSIGNMENTS
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_assignments_partner_status ON food.delivery_assignments(delivery_partner_id, status);
CREATE INDEX IF NOT EXISTS ix_food_assignments_status ON food.delivery_assignments(status);

DROP TRIGGER IF EXISTS trg_delivery_assignments_updated_at ON food.delivery_assignments;
CREATE TRIGGER trg_delivery_assignments_updated_at
BEFORE UPDATE ON food.delivery_assignments
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_tracking_assignment_time ON food.delivery_tracking_events(assignment_id, created_at DESC);

-- ============================================================
-- COUPON REDEMPTIONS, RATINGS
-- ============================================================

CREATE TABLE IF NOT EXISTS food.coupon_redemptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    coupon_id UUID NOT NULL REFERENCES food.coupons(id) ON DELETE RESTRICT,
    order_id UUID NOT NULL UNIQUE REFERENCES food.orders(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    discount_amount NUMERIC(12,2) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_food_coupon_redemption_discount CHECK (discount_amount >= 0)
);

CREATE INDEX IF NOT EXISTS ix_food_coupon_redemptions_coupon ON food.coupon_redemptions(coupon_id);
CREATE INDEX IF NOT EXISTS ix_food_coupon_redemptions_user ON food.coupon_redemptions(user_id);

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

CREATE INDEX IF NOT EXISTS ix_food_restaurant_ratings_restaurant ON food.restaurant_ratings(restaurant_id);

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

CREATE INDEX IF NOT EXISTS ix_food_delivery_ratings_partner ON food.delivery_ratings(delivery_partner_id);

-- ============================================================
-- SETTLEMENTS
-- ============================================================

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

CREATE INDEX IF NOT EXISTS ix_food_restaurant_settlements_restaurant ON food.restaurant_settlements(restaurant_id, period_start, period_end);
CREATE INDEX IF NOT EXISTS ix_food_restaurant_settlements_status ON food.restaurant_settlements(status);

DROP TRIGGER IF EXISTS trg_restaurant_settlements_updated_at ON food.restaurant_settlements;
CREATE TRIGGER trg_restaurant_settlements_updated_at
BEFORE UPDATE ON food.restaurant_settlements
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_delivery_settlements_partner ON food.delivery_partner_settlements(delivery_partner_id, period_start, period_end);
CREATE INDEX IF NOT EXISTS ix_food_delivery_settlements_status ON food.delivery_partner_settlements(status);

DROP TRIGGER IF EXISTS trg_delivery_partner_settlements_updated_at ON food.delivery_partner_settlements;
CREATE TRIGGER trg_delivery_partner_settlements_updated_at
BEFORE UPDATE ON food.delivery_partner_settlements
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

-- ============================================================
-- SUPPORT, AUDIT, IDEMPOTENCY
-- ============================================================

CREATE TABLE IF NOT EXISTS food.support_tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id UUID REFERENCES food.orders(id) ON DELETE SET NULL,
    user_id UUID NOT NULL,
    restaurant_id UUID REFERENCES food.restaurants(id) ON DELETE SET NULL,
    delivery_partner_id UUID REFERENCES food.delivery_partners(id) ON DELETE SET NULL,
    category VARCHAR(80) NOT NULL,
    subject VARCHAR(200) NOT NULL,
    description TEXT,
    status VARCHAR(40) NOT NULL DEFAULT 'OPEN',
    assigned_to UUID,
    resolution TEXT,
    resolved_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ix_food_support_tickets_user ON food.support_tickets(user_id);
CREATE INDEX IF NOT EXISTS ix_food_support_tickets_status ON food.support_tickets(status);

DROP TRIGGER IF EXISTS trg_support_tickets_updated_at ON food.support_tickets;
CREATE TRIGGER trg_support_tickets_updated_at
BEFORE UPDATE ON food.support_tickets
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

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

CREATE INDEX IF NOT EXISTS ix_food_admin_audit_actor ON food.admin_audit_logs(actor_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_admin_audit_entity ON food.admin_audit_logs(entity_type, entity_id);

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

CREATE INDEX IF NOT EXISTS ix_food_idempotency_created ON food.idempotency_keys(created_at);

-- ============================================================
-- SEED DATA
-- ============================================================

INSERT INTO food.cuisines (name, slug, sort_order)
VALUES
    ('Biryani', 'biryani', 1),
    ('South Indian', 'south-indian', 2),
    ('North Indian', 'north-indian', 3),
    ('Chinese', 'chinese', 4),
    ('Fast Food', 'fast-food', 5),
    ('Healthy', 'healthy', 6)
ON CONFLICT (slug) DO NOTHING;

INSERT INTO food.service_areas (
    id, name, city, state, postal_code, center_latitude, center_longitude, radius_km, is_active
)
VALUES
    ('11111111-1111-4111-8111-111111111111', 'Central Hyderabad', 'Hyderabad', 'Telangana', '500001', 17.3850000, 78.4867000, 12, TRUE),
    ('11111111-1111-4111-8111-111111111112', 'South Bengaluru', 'Bengaluru', 'Karnataka', '560034', 12.9352000, 77.6245000, 10, TRUE)
ON CONFLICT (name, city) DO NOTHING;

INSERT INTO food.restaurant_partners (
    id, owner_user_id, legal_name, display_name, phone, email, status, approved_at
)
VALUES
    ('22222222-2222-4222-8222-222222222221', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1', 'FiGo Kitchens Private Limited', 'FiGo Kitchens', '+910000000001', 'partner@figo.local', 'APPROVED', NOW()),
    ('22222222-2222-4222-8222-222222222222', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2', 'Amma Home Foods', 'Amma Home Kitchen', '+910000000002', 'amma@figo.local', 'APPROVED', NOW())
ON CONFLICT (id) DO NOTHING;

INSERT INTO food.restaurants (
    id, partner_id, owner_user_id, name, slug, description, phone, email, status, is_open, is_accepting_orders,
    address_line1, city, state, postal_code, latitude, longitude, avg_rating, rating_count,
    min_order_amount, packaging_fee, avg_preparation_minutes, commission_percentage, approved_at
)
VALUES
    (
        '33333333-3333-4333-8333-333333333331',
        '22222222-2222-4222-8222-222222222221',
        'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1',
        'FiGo Biryani House',
        'figo-biryani-house',
        'Hyderabadi biryani, kebabs, and fast lunch bowls.',
        '+910000000101',
        'biryani@figo.local',
        'ACTIVE',
        TRUE,
        TRUE,
        '12 Market Road',
        'Hyderabad',
        'Telangana',
        '500001',
        17.3850000,
        78.4867000,
        4.60,
        128,
        149,
        12,
        28,
        15,
        NOW()
    ),
    (
        '33333333-3333-4333-8333-333333333332',
        '22222222-2222-4222-8222-222222222222',
        'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2',
        'Amma Tiffins',
        'amma-tiffins',
        'Fresh idli, dosa, pongal, and homestyle breakfast.',
        '+910000000102',
        'tiffins@figo.local',
        'ACTIVE',
        TRUE,
        TRUE,
        '8 Temple Street',
        'Bengaluru',
        'Karnataka',
        '560034',
        12.9352000,
        77.6245000,
        4.80,
        94,
        99,
        8,
        18,
        12,
        NOW()
    )
ON CONFLICT (slug) DO NOTHING;

INSERT INTO food.restaurant_cuisines (restaurant_id, cuisine_id)
SELECT '33333333-3333-4333-8333-333333333331', id FROM food.cuisines WHERE slug IN ('biryani', 'north-indian')
ON CONFLICT DO NOTHING;

INSERT INTO food.restaurant_cuisines (restaurant_id, cuisine_id)
SELECT '33333333-3333-4333-8333-333333333332', id FROM food.cuisines WHERE slug IN ('south-indian', 'healthy')
ON CONFLICT DO NOTHING;

INSERT INTO food.restaurant_images (id, restaurant_id, image_url, image_type, sort_order)
VALUES
    ('44444444-4444-4444-8444-444444444441', '33333333-3333-4333-8333-333333333331', 'https://images.unsplash.com/photo-1563379091339-03246963d96c?auto=format&fit=crop&w=1200&q=80', 'hero', 1),
    ('44444444-4444-4444-8444-444444444442', '33333333-3333-4333-8333-333333333332', 'https://images.unsplash.com/photo-1589301760014-d929f3979dbc?auto=format&fit=crop&w=1200&q=80', 'hero', 1)
ON CONFLICT (id) DO NOTHING;

INSERT INTO food.menu_categories (id, restaurant_id, name, description, sort_order)
VALUES
    ('55555555-5555-4555-8555-555555555551', '33333333-3333-4333-8333-333333333331', 'Biryani Bowls', 'Single-serve biryani bowls for quick meals.', 1),
    ('55555555-5555-4555-8555-555555555552', '33333333-3333-4333-8333-333333333331', 'Kebabs', 'Grilled starters and sides.', 2),
    ('55555555-5555-4555-8555-555555555553', '33333333-3333-4333-8333-333333333332', 'Breakfast', 'Fresh South Indian breakfast plates.', 1),
    ('55555555-5555-4555-8555-555555555554', '33333333-3333-4333-8333-333333333332', 'Meals', 'Homestyle lunch plates.', 2)
ON CONFLICT (id) DO NOTHING;

INSERT INTO food.menu_items (
    id, restaurant_id, category_id, name, description, food_type, base_price, discount_price,
    image_url, preparation_minutes, is_available, is_recommended, tax_percentage
)
VALUES
    ('66666666-6666-4666-8666-666666666661', '33333333-3333-4333-8333-333333333331', '55555555-5555-4555-8555-555555555551', 'Chicken Dum Biryani Bowl', 'Aromatic basmati rice, dum chicken, salan, and raita.', 'NON_VEG', 229, 199, 'https://images.unsplash.com/photo-1633945274405-b6c8069047b0?auto=format&fit=crop&w=900&q=80', 28, TRUE, TRUE, 5),
    ('66666666-6666-4666-8666-666666666662', '33333333-3333-4333-8333-333333333331', '55555555-5555-4555-8555-555555555551', 'Paneer Biryani Bowl', 'Paneer tikka layered with biryani rice and mint raita.', 'VEG', 199, 179, 'https://images.unsplash.com/photo-1630409351241-e90e7f5e434d?auto=format&fit=crop&w=900&q=80', 24, TRUE, FALSE, 5),
    ('66666666-6666-4666-8666-666666666663', '33333333-3333-4333-8333-333333333331', '55555555-5555-4555-8555-555555555552', 'Chicken Seekh Kebab', 'Smoky minced chicken kebabs with chutney.', 'NON_VEG', 179, NULL, 'https://images.unsplash.com/photo-1599487488170-d11ec9c172f0?auto=format&fit=crop&w=900&q=80', 18, TRUE, FALSE, 5),
    ('66666666-6666-4666-8666-666666666664', '33333333-3333-4333-8333-333333333332', '55555555-5555-4555-8555-555555555553', 'Ghee Podi Idli', 'Soft idlis tossed with ghee and house podi.', 'VEG', 99, 89, 'https://images.unsplash.com/photo-1589302168068-964664d93dc0?auto=format&fit=crop&w=900&q=80', 12, TRUE, TRUE, 5),
    ('66666666-6666-4666-8666-666666666665', '33333333-3333-4333-8333-333333333332', '55555555-5555-4555-8555-555555555553', 'Masala Dosa', 'Crisp dosa with potato masala, chutney, and sambar.', 'VEG', 129, NULL, 'https://images.unsplash.com/photo-1668236543090-82eba5ee5976?auto=format&fit=crop&w=900&q=80', 16, TRUE, TRUE, 5),
    ('66666666-6666-4666-8666-666666666666', '33333333-3333-4333-8333-333333333332', '55555555-5555-4555-8555-555555555554', 'Mini South Meal', 'Rice, sambar, rasam, poriyal, curd, and pickle.', 'VEG', 159, 139, 'https://images.unsplash.com/photo-1626777552726-4a6b54c97e46?auto=format&fit=crop&w=900&q=80', 20, TRUE, FALSE, 5)
ON CONFLICT (id) DO NOTHING;

INSERT INTO food.coupons (
    id, code, title, description, coupon_type, discount_value, max_discount_amount, min_order_amount,
    total_usage_limit, per_user_usage_limit, starts_at, ends_at, is_active, funded_by
)
VALUES
    ('77777777-7777-4777-8777-777777777771', 'FIGO50', 'FiGo launch offer', 'Get 50 rupees off on your first FiGo order.', 'FLAT', 50, 50, 199, 10000, 1, NOW() - INTERVAL '1 day', NOW() + INTERVAL '90 days', TRUE, 'PLATFORM')
ON CONFLICT (code) DO NOTHING;

-- ============================================================
-- NOTES FOR APPLICATION LAYER
-- ============================================================

-- 1. Validate all order status transitions in application service.
-- 2. Use SELECT ... FOR UPDATE when changing order status.
-- 3. Calculate order prices server-side.
-- 4. Never trust cart totals from client.
-- 5. Store order snapshots before payment confirmation.
-- 6. Use idempotency_keys for order placement and payment callback.
-- 7. Insert order_status_history for every order status change.
-- 8. Insert admin_audit_logs for approval, rejection, refund, settlement, and suspension actions.
-- 9. Add PostGIS geometry columns later if location search needs high scale.
-- 10. Add partitioning for orders/order_status_history/delivery_tracking_events when volume grows.


-- ============================================================
-- P0.3 — Outbox table for durable event publishing
-- ============================================================
-- Domain write + outbox row inside the same tx so an event cannot be
-- silently dropped on a Kafka outage. shared/outbox.Publisher polls
-- this table and writes to Kafka, marks rows published, and retries
-- with backoff. The default table name `outbox_events` matches the
-- shared publisher contract.
CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished
    ON outbox_events (id)
    WHERE published_at IS NULL;

-- ─── B1: kitchen queue + SLA accept deadline ──────────────────────────
--
-- `sla_accept_seconds` is the per-restaurant grace window for accepting
-- a CONFIRMED order. The default (180s = 3 min) matches industry norms;
-- partners can tune per-restaurant from the partner dashboard.
--
-- `accept_deadline_at` is set on the orders table when payment is
-- captured (status moves to CONFIRMED). The auto-reject worker scans
-- orders with `status='CONFIRMED' AND accept_deadline_at < NOW()` and
-- transitions them to RESTAURANT_REJECTED with reason='sla_breach'.
ALTER TABLE food.restaurants
    ADD COLUMN IF NOT EXISTS sla_accept_seconds INTEGER NOT NULL DEFAULT 180
    CHECK (sla_accept_seconds BETWEEN 30 AND 1800);

ALTER TABLE food.orders
    ADD COLUMN IF NOT EXISTS accept_deadline_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS ix_food_orders_accept_sla
    ON food.orders(accept_deadline_at)
    WHERE status = 'CONFIRMED' AND accept_deadline_at IS NOT NULL;

-- ─── B2: stock-out log + substitution flow ────────────────────────────
--
-- menu_item_stockouts is an append-only audit log that records every
-- time a partner toggles an item to unavailable (and back). The
-- partner SLA dashboard reads it for "items unavailable longer than
-- 24 h" detection.
CREATE TABLE IF NOT EXISTS food.menu_item_stockouts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    menu_item_id  UUID NOT NULL REFERENCES food.menu_items(id) ON DELETE CASCADE,
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    is_available  BOOLEAN NOT NULL,
    reason        VARCHAR(120),
    changed_by    UUID,
    changed_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_food_stockouts_item ON food.menu_item_stockouts(menu_item_id, changed_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_stockouts_restaurant ON food.menu_item_stockouts(restaurant_id, changed_at DESC);

-- order_substitutions captures the partner's "we don't have item X,
-- here's Y instead" proposal + the customer's response. The status
-- machine is `proposed → approved | declined | cancelled`. If declined
-- the order line is removed via the cancellation flow.
DO $$ BEGIN
    CREATE TYPE food.substitution_status AS ENUM ('proposed','approved','declined','cancelled');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

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
CREATE INDEX IF NOT EXISTS ix_food_substitutions_order ON food.order_substitutions(order_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_substitutions_status ON food.order_substitutions(status);

DROP TRIGGER IF EXISTS trg_food_substitutions_updated_at ON food.order_substitutions;
CREATE TRIGGER trg_food_substitutions_updated_at
BEFORE UPDATE ON food.order_substitutions
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

-- ─── B3: menu moderation ──────────────────────────────────────────────
--
-- moderation_status governs whether a menu item is visible to
-- customers. Default `approved` so existing items stay live; new items
-- created by partner start `approved` too, with the admin queue
-- reviewing reports/auto-flags asynchronously. `flagged` items remain
-- visible but are surfaced in the admin queue; `rejected` items are
-- hidden from customer-facing listings.
DO $$ BEGIN
    CREATE TYPE food.moderation_status AS ENUM ('approved','flagged','rejected','pending_review');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE food.menu_items
    ADD COLUMN IF NOT EXISTS moderation_status food.moderation_status NOT NULL DEFAULT 'approved',
    ADD COLUMN IF NOT EXISTS moderation_reason TEXT,
    ADD COLUMN IF NOT EXISTS moderated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS moderated_by UUID;

CREATE INDEX IF NOT EXISTS ix_food_menu_items_moderation
    ON food.menu_items(moderation_status)
    WHERE moderation_status IN ('flagged','pending_review');

-- menu_item_reports captures customer-side complaints (wrong photo,
-- offensive name, allergen mismatch, …). Many reports on one item
-- auto-flips moderation_status to `flagged` for admin review.
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
CREATE INDEX IF NOT EXISTS ix_food_item_reports_item ON food.menu_item_reports(menu_item_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_item_reports_pending ON food.menu_item_reports(created_at DESC) WHERE resolved_at IS NULL;

-- ─── B4: delivery offer fan-out ───────────────────────────────────────
--
-- When an order goes DELIVERY_ASSIGNING the dispatch worker mints one
-- food.delivery_offers row per nearby online partner. First to accept
-- wins (a tx promotes the offer to the existing delivery_assignments
-- row); the rest are auto-rejected. Offers expire after 25 seconds.
DO $$ BEGIN
    CREATE TYPE food.delivery_offer_status AS ENUM ('pending','accepted','rejected','expired','superseded');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

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
CREATE INDEX IF NOT EXISTS ix_food_offers_partner_pending
    ON food.delivery_offers(delivery_partner_id, expires_at)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS ix_food_offers_order ON food.delivery_offers(order_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_offers_expiry ON food.delivery_offers(expires_at) WHERE status = 'pending';

-- ─── B5: pickup / delivery proof ──────────────────────────────────────
--
-- pickup_code + delivery_code already exist on food.delivery_assignments
-- (4-6 digit OTPs). B5 adds the proof-of-pickup / proof-of-delivery
-- image URLs (stored as MinIO object keys) and a verified flag set
-- when the matching code is submitted.
ALTER TABLE food.delivery_assignments
    ADD COLUMN IF NOT EXISTS pickup_verified_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delivery_verified_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS proof_of_pickup_url   TEXT,
    ADD COLUMN IF NOT EXISTS proof_of_delivery_url TEXT;

-- ─── B6: support tickets + refund request ─────────────────────────────
--
-- Tickets are the customer's entry point for anything from
-- "missing item" to "refund please" to "driver was rude". Each ticket
-- can reference an order (most do) and is owned by one customer; the
-- admin queue acts on tickets via SetStatus + optional approved refund.
DO $$ BEGIN
    CREATE TYPE food.ticket_status AS ENUM ('open','in_progress','resolved','closed','cancelled');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE food.refund_status AS ENUM ('requested','approved','rejected','processed');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

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
CREATE INDEX IF NOT EXISTS ix_food_tickets_customer ON food.support_tickets(customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_tickets_status   ON food.support_tickets(status);
CREATE INDEX IF NOT EXISTS ix_food_tickets_order    ON food.support_tickets(order_id) WHERE order_id IS NOT NULL;

DROP TRIGGER IF EXISTS trg_food_tickets_updated_at ON food.support_tickets;
CREATE TRIGGER trg_food_tickets_updated_at
BEFORE UPDATE ON food.support_tickets
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

CREATE TABLE IF NOT EXISTS food.ticket_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ticket_id   UUID NOT NULL REFERENCES food.support_tickets(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL,
    is_admin    BOOLEAN NOT NULL DEFAULT FALSE,
    body        TEXT NOT NULL,
    attachments JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_food_ticket_messages_ticket ON food.ticket_messages(ticket_id, created_at);

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
CREATE INDEX IF NOT EXISTS ix_food_refunds_order    ON food.refund_requests(order_id);
CREATE INDEX IF NOT EXISTS ix_food_refunds_customer ON food.refund_requests(customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_refunds_status   ON food.refund_requests(status);

DROP TRIGGER IF EXISTS trg_food_refunds_updated_at ON food.refund_requests;
CREATE TRIGGER trg_food_refunds_updated_at
BEFORE UPDATE ON food.refund_requests
FOR EACH ROW EXECUTE FUNCTION food.set_updated_at();

-- ─── B7: item-level reviews ───────────────────────────────────────────
--
-- Existing restaurant_ratings + delivery_ratings cover the order-level
-- 1-5 stars. Item-level reviews let a customer rate the specific
-- dish; the aggregate feeds into menu_items.avg_rating + count.
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
CREATE INDEX IF NOT EXISTS ix_food_item_reviews_item ON food.item_reviews(menu_item_id, created_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_item_reviews_customer ON food.item_reviews(customer_id);

-- Aggregate columns on menu_items so PDPs render the score cheaply.
ALTER TABLE food.menu_items
    ADD COLUMN IF NOT EXISTS avg_rating  NUMERIC(3,2),
    ADD COLUMN IF NOT EXISTS rating_count INTEGER NOT NULL DEFAULT 0;

-- ─── Wave E: fraud scores + finance settlements ───────────────────────
--
-- fraud_scores: rolling per-user fraud signal. The worker writes one
-- row per (user_id, signal) and the admin queue reads aggregated.
CREATE TABLE IF NOT EXISTS food.fraud_scores (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    signal      VARCHAR(60) NOT NULL,
    score       NUMERIC(6,2) NOT NULL,
    detail      JSONB NOT NULL DEFAULT '{}'::jsonb,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_food_fraud_user_signal ON food.fraud_scores(user_id, signal, computed_at DESC);
CREATE INDEX IF NOT EXISTS ix_food_fraud_recent ON food.fraud_scores(computed_at DESC);

-- settlement_files: ops-team export records (CSV in MinIO + audit row).
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
CREATE INDEX IF NOT EXISTS ix_food_settlement_files_period ON food.settlement_files(period_start, period_end, kind);

-- G4.1: settlement_files inline body. file_url stays for future MinIO
-- offload; until then the worker stores the generated CSV inline so the
-- download endpoint can stream it back without external storage.
ALTER TABLE food.settlement_files
    ADD COLUMN IF NOT EXISTS body BYTEA,
    ALTER COLUMN file_url DROP NOT NULL,
    ALTER COLUMN file_url SET DEFAULT '';

-- ─── Wave F: per-order conversations + read receipts ──────────────────
--
-- order_messages is the conversation between customer, restaurant, and
-- delivery partner for a single order. Quick + lightweight (no media
-- in v1, just text). Admin can read every message for moderation /
-- escalation.
--
-- author_role is one of customer | restaurant | delivery | admin.
-- read_by is an array of {role,user_id,at} triples; the recipient app
-- POSTs to /read once the message is shown.
CREATE TABLE IF NOT EXISTS food.order_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    order_id    UUID NOT NULL REFERENCES food.orders(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL,
    author_role VARCHAR(16) NOT NULL CHECK (author_role IN ('customer','restaurant','delivery','admin')),
    body        TEXT NOT NULL,
    read_by     JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS ix_food_order_messages_order ON food.order_messages(order_id, created_at);

-- ─── G4.2: GST invoice fields ─────────────────────────────────────────
-- invoice_number stable on regenerate so a re-pull of
-- /v1/food/orders/:id/invoice returns the same document.
ALTER TABLE food.restaurants
    ADD COLUMN IF NOT EXISTS gstin VARCHAR(20);
ALTER TABLE food.menu_items
    ADD COLUMN IF NOT EXISTS hsn_code VARCHAR(20);
ALTER TABLE food.orders
    ADD COLUMN IF NOT EXISTS invoice_number VARCHAR(40);

-- One row per fiscal year, allocated lazily at first invoice. Storing
-- last allocated number keeps numbering deterministic across replicas
-- without a global Postgres SEQUENCE that's hard to reset annually.
CREATE TABLE IF NOT EXISTS food.invoice_sequences (
    financial_year VARCHAR(10) PRIMARY KEY,
    last_number    BIGINT NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── G4.4: loyalty (points + tier) ────────────────────────────────────
-- Per-user balance + append-only ledger. Earn = +N on DELIVERED order,
-- redeem = -N on apply-at-checkout. Tier is a computed view over
-- lifetime_earned.
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
CREATE INDEX IF NOT EXISTS ix_food_loyalty_ledger_user
    ON food.loyalty_ledger(user_id, created_at DESC);

-- ─── G4.6: referral tracking ──────────────────────────────────────────
-- code is per-user, alphanumeric, unique. credits + bonus paid once
-- the referee places their first DELIVERED order (worker decides).
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
CREATE INDEX IF NOT EXISTS ix_food_referrals_referrer
    ON food.referrals(referrer_id, created_at DESC);

-- ─── P2: delivery batching ────────────────────────────────────────────
--
-- A batch lets one partner pick up 2-3 orders at the same restaurant
-- within a short time window (default 5 min) and deliver them in
-- sequence. Each member order keeps its own delivery_assignment row
-- (so OTPs, ratings, payouts stay per-order); batch_id links them so
-- the worker offers + accepts the whole group atomically.
--
-- Sequence is the partner's pickup/drop order; lower = earlier. The
-- dispatch worker assigns it by created_at within the batch.
CREATE TABLE IF NOT EXISTS food.delivery_batches (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    restaurant_id UUID NOT NULL REFERENCES food.restaurants(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','assigned','cancelled','completed')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    assigned_at   TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS ix_food_batches_status ON food.delivery_batches(status, created_at);
CREATE INDEX IF NOT EXISTS ix_food_batches_restaurant ON food.delivery_batches(restaurant_id, created_at DESC);

ALTER TABLE food.delivery_assignments
    ADD COLUMN IF NOT EXISTS batch_id UUID REFERENCES food.delivery_batches(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS batch_sequence SMALLINT;
CREATE INDEX IF NOT EXISTS ix_food_assignments_batch ON food.delivery_assignments(batch_id) WHERE batch_id IS NOT NULL;

ALTER TABLE food.delivery_offers
    ADD COLUMN IF NOT EXISTS batch_id UUID REFERENCES food.delivery_batches(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS ix_food_offers_batch ON food.delivery_offers(batch_id) WHERE batch_id IS NOT NULL;
