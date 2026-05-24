-- rider-service schema (Mopedu — B2B2C ride mini-app inside AtPost).
-- Idempotent — every CREATE uses IF NOT EXISTS so the bootstrap can be re-run
-- on every cold start without dropping data. Enums are guarded by DO blocks
-- (PostgreSQL has no CREATE TYPE IF NOT EXISTS).
--
-- BUSINESS MODEL: customers ride free; partners (drivers / vehicle owners /
-- fleet owners) pay a monthly subscription for ride-lead access. This file
-- covers Sprint 1 scope per mopedu/IMPLEMENTATION_PLAN.md §3:
--   * cities, zones (PostGIS polygons)
--   * partners + partner documents
--   * vehicles + vehicle documents
--   * subscription plans + partner subscriptions + subscription payments
--   * partner-Aadhaar verifications (DigiLocker assertion mirror)
--   * fare rules
--   * idempotency (mirrors wallet-service shape)
--   * audit logs
--
-- DPDP ACT: No raw Aadhaar number is stored anywhere. The Aadhaar flow
-- captures only the partner-supplied opaque DigiLocker assertion id +
-- the SHA-256 hash of the document-type label (see internal/digilocker).
-- The raw 12-digit number never crosses the service boundary.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS postgis;

-- 5.1 Enums --------------------------------------------------------------------
DO $$ BEGIN
    CREATE TYPE rider_partner_type AS ENUM (
        'individual_driver',
        'owner_driver',
        'fleet_owner',
        'fleet_driver'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_verification_status AS ENUM (
        'draft',
        'pending',
        'approved',
        'rejected',
        'expired'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_partner_status AS ENUM (
        'draft',
        'pending_verification',
        'approved',
        'suspended',
        'blocked',
        'inactive'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_subscription_status AS ENUM (
        'trial',
        'active',
        'grace_period',
        'expired',
        'cancelled',
        'suspended'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_vehicle_type AS ENUM (
        'bike',
        'auto',
        'mini_cab',
        'sedan',
        'suv',
        'premium',
        'ev_bike',
        'ev_car'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_ride_status AS ENUM (
        'requested',
        'searching_partner',
        'partner_assigned',
        'partner_arriving',
        'arrived',
        'otp_verified',
        'in_progress',
        'completed',
        'cancelled_by_customer',
        'cancelled_by_partner',
        'cancelled_by_admin',
        'expired',
        'failed'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_payment_status AS ENUM (
        'pending',
        'submitted',
        'verified',
        'rejected',
        'refunded',
        'failed'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE rider_document_type AS ENUM (
        'aadhaar',
        'pan',
        'driving_license',
        'profile_photo',
        'police_verification',
        'vehicle_rc',
        'vehicle_insurance',
        'pollution_certificate',
        'permit',
        'fitness_certificate',
        'bank_proof',
        'other'
    );
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- 5.2 Cities and zones ---------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_cities (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(120) NOT NULL,
    state         VARCHAR(120),
    country       VARCHAR(120) NOT NULL DEFAULT 'India',
    currency_code VARCHAR(10) NOT NULL DEFAULT 'INR',
    is_active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (name, state, country)
);

CREATE TABLE IF NOT EXISTS rider_zones (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id    UUID NOT NULL REFERENCES rider_cities(id),
    name       VARCHAR(120) NOT NULL,
    boundary   GEOGRAPHY(POLYGON, 4326),
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_zones_city_id ON rider_zones(city_id);
CREATE INDEX IF NOT EXISTS idx_rider_zones_boundary ON rider_zones USING GIST(boundary);

-- 5.3 Partners -----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_partners (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL,
    partner_type          rider_partner_type NOT NULL,
    fleet_owner_id        UUID NULL,
    full_name             VARCHAR(160) NOT NULL,
    phone                 VARCHAR(30) NOT NULL,
    email                 VARCHAR(160),
    profile_photo_url     TEXT,
    city_id               UUID REFERENCES rider_cities(id),
    status                rider_partner_status NOT NULL DEFAULT 'draft',
    kyc_status            rider_verification_status NOT NULL DEFAULT 'draft',
    bank_status           rider_verification_status NOT NULL DEFAULT 'draft',
    rating                NUMERIC(3,2) NOT NULL DEFAULT 0,
    total_rides_completed INT NOT NULL DEFAULT 0,
    total_rides_cancelled INT NOT NULL DEFAULT 0,
    acceptance_rate       NUMERIC(5,2) NOT NULL DEFAULT 0,
    cancellation_rate     NUMERIC(5,2) NOT NULL DEFAULT 0,
    fraud_score           NUMERIC(6,2) NOT NULL DEFAULT 0,
    is_online             BOOLEAN NOT NULL DEFAULT FALSE,
    last_online_at        TIMESTAMPTZ,
    last_offline_at       TIMESTAMPTZ,
    suspended_reason      TEXT,
    blocked_reason        TEXT,
    approved_at           TIMESTAMPTZ,
    approved_by           UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at            TIMESTAMPTZ,
    CONSTRAINT fk_rider_partner_fleet_owner
        FOREIGN KEY (fleet_owner_id) REFERENCES rider_partners(id)
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_partners_user_id_active
    ON rider_partners(user_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_rider_partners_city_status
    ON rider_partners(city_id, status);
CREATE INDEX IF NOT EXISTS idx_rider_partners_online
    ON rider_partners(is_online) WHERE is_online = TRUE;

-- 5.4 Partner documents --------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_partner_documents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id       UUID NOT NULL REFERENCES rider_partners(id),
    document_type    rider_document_type NOT NULL,
    document_number  VARCHAR(120),
    file_url         TEXT NOT NULL,
    status           rider_verification_status NOT NULL DEFAULT 'pending',
    rejection_reason TEXT,
    verified_by      UUID,
    verified_at      TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_partner_documents_partner
    ON rider_partner_documents(partner_id);
CREATE INDEX IF NOT EXISTS idx_rider_partner_documents_status
    ON rider_partner_documents(status);

-- DPDP-compliant Aadhaar mirror. NO raw Aadhaar number is ever stored —
-- only the opaque DigiLocker assertion reference (`digilocker_ref`) plus
-- the SHA-256 hash of the document-type label. Mirrors dating-service.
CREATE TABLE IF NOT EXISTS rider_partner_aadhaar_verifications (
    partner_id      UUID PRIMARY KEY REFERENCES rider_partners(id),
    digilocker_ref  TEXT NOT NULL,
    doc_type_hash   TEXT NOT NULL,
    issued_at       TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 5.5 Vehicles -----------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_vehicles (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id          UUID NOT NULL REFERENCES rider_partners(id),
    vehicle_type        rider_vehicle_type NOT NULL,
    registration_number VARCHAR(40) NOT NULL,
    brand               VARCHAR(100),
    model               VARCHAR(100),
    color               VARCHAR(60),
    manufacture_year    INT,
    seat_count          INT,
    fuel_type           VARCHAR(40),
    is_ev               BOOLEAN NOT NULL DEFAULT FALSE,
    status              rider_verification_status NOT NULL DEFAULT 'pending',
    rejection_reason    TEXT,
    verified_by         UUID,
    verified_at         TIMESTAMPTZ,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at          TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_vehicle_registration_active
    ON rider_vehicles(registration_number) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_rider_vehicles_partner ON rider_vehicles(partner_id);
CREATE INDEX IF NOT EXISTS idx_rider_vehicles_type_status
    ON rider_vehicles(vehicle_type, status);

-- 5.6 Vehicle documents --------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_vehicle_documents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    vehicle_id       UUID NOT NULL REFERENCES rider_vehicles(id),
    document_type    rider_document_type NOT NULL,
    document_number  VARCHAR(120),
    file_url         TEXT NOT NULL,
    status           rider_verification_status NOT NULL DEFAULT 'pending',
    rejection_reason TEXT,
    verified_by      UUID,
    verified_at      TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_vehicle_documents_vehicle
    ON rider_vehicle_documents(vehicle_id);
CREATE INDEX IF NOT EXISTS idx_rider_vehicle_documents_status
    ON rider_vehicle_documents(status);

-- 5.7 Subscription plans -------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_subscription_plans (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code                VARCHAR(60) NOT NULL UNIQUE,
    name                VARCHAR(120) NOT NULL,
    description         TEXT,
    price_amount        NUMERIC(12,2) NOT NULL,
    currency_code       VARCHAR(10) NOT NULL DEFAULT 'INR',
    billing_period_days INT NOT NULL DEFAULT 30,
    lead_limit          INT,
    fair_use_limit      INT,
    priority_weight     INT NOT NULL DEFAULT 1,
    is_unlimited        BOOLEAN NOT NULL DEFAULT FALSE,
    is_fleet_plan       BOOLEAN NOT NULL DEFAULT FALSE,
    max_drivers         INT,
    grace_period_days   INT NOT NULL DEFAULT 3,
    is_active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 5.8 Partner subscriptions ----------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_partner_subscriptions (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id          UUID NOT NULL REFERENCES rider_partners(id),
    plan_id             UUID NOT NULL REFERENCES rider_subscription_plans(id),
    status              rider_subscription_status NOT NULL DEFAULT 'active',
    starts_at           TIMESTAMPTZ NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    grace_ends_at       TIMESTAMPTZ,
    leads_used          INT NOT NULL DEFAULT 0,
    fair_use_used       INT NOT NULL DEFAULT 0,
    auto_renew          BOOLEAN NOT NULL DEFAULT FALSE,
    cancelled_at        TIMESTAMPTZ,
    cancellation_reason TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_partner_subscriptions_partner
    ON rider_partner_subscriptions(partner_id, status);
CREATE INDEX IF NOT EXISTS idx_rider_partner_subscriptions_expiry
    ON rider_partner_subscriptions(expires_at);

-- 5.9 Subscription payments ----------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_subscription_payments (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id        UUID NOT NULL REFERENCES rider_partners(id),
    subscription_id   UUID REFERENCES rider_partner_subscriptions(id),
    plan_id           UUID NOT NULL REFERENCES rider_subscription_plans(id),
    amount            NUMERIC(12,2) NOT NULL,
    currency_code     VARCHAR(10) NOT NULL DEFAULT 'INR',
    payment_method    VARCHAR(60) NOT NULL,
    payment_reference VARCHAR(160),
    payment_proof_url TEXT,
    wallet_txn_id     UUID,
    status            rider_payment_status NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    verified_by       UUID,
    verified_at       TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_subscription_payments_partner
    ON rider_subscription_payments(partner_id);
CREATE INDEX IF NOT EXISTS idx_rider_subscription_payments_status
    ON rider_subscription_payments(status);

-- 5.11 Fare rules --------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_fare_rules (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id           UUID NOT NULL REFERENCES rider_cities(id),
    vehicle_type      rider_vehicle_type NOT NULL,
    base_fare         NUMERIC(12,2) NOT NULL,
    per_km_fare       NUMERIC(12,2) NOT NULL,
    per_minute_fare   NUMERIC(12,2) NOT NULL DEFAULT 0,
    minimum_fare      NUMERIC(12,2) NOT NULL,
    platform_fee      NUMERIC(12,2) NOT NULL DEFAULT 0,
    night_multiplier  NUMERIC(5,2) NOT NULL DEFAULT 1,
    peak_multiplier   NUMERIC(5,2) NOT NULL DEFAULT 1,
    cancellation_fee  NUMERIC(12,2) NOT NULL DEFAULT 0,
    is_active         BOOLEAN NOT NULL DEFAULT TRUE,
    starts_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ends_at           TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_fare_rules_city_vehicle
    ON rider_fare_rules(city_id, vehicle_type, is_active);

-- 5.12 Rides (sprint 1: minimal create/get for /rides POST + GET) -------------
CREATE TABLE IF NOT EXISTS rider_rides (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id       UUID NOT NULL,
    partner_id             UUID REFERENCES rider_partners(id),
    vehicle_id             UUID REFERENCES rider_vehicles(id),
    city_id                UUID REFERENCES rider_cities(id),
    vehicle_type           rider_vehicle_type NOT NULL,
    status                 rider_ride_status NOT NULL DEFAULT 'requested',
    pickup_address         TEXT NOT NULL,
    pickup_location        GEOGRAPHY(POINT, 4326) NOT NULL,
    drop_address           TEXT NOT NULL,
    drop_location          GEOGRAPHY(POINT, 4326) NOT NULL,
    estimated_distance_km  NUMERIC(10,2),
    estimated_duration_min NUMERIC(10,2),
    estimated_fare         NUMERIC(12,2),
    final_distance_km      NUMERIC(10,2),
    final_duration_min     NUMERIC(10,2),
    final_fare             NUMERIC(12,2),
    payment_method         VARCHAR(40),
    otp_hash               TEXT,
    otp_expires_at         TIMESTAMPTZ,
    requested_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    assigned_at            TIMESTAMPTZ,
    arrived_at             TIMESTAMPTZ,
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    cancelled_at           TIMESTAMPTZ,
    cancelled_by           UUID,
    cancellation_reason    TEXT,
    customer_rating        INT,
    partner_rating         INT,
    customer_feedback      TEXT,
    partner_feedback       TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_rides_customer
    ON rider_rides(customer_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_rides_partner
    ON rider_rides(partner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_rides_status ON rider_rides(status);
CREATE INDEX IF NOT EXISTS idx_rider_rides_pickup_location
    ON rider_rides USING GIST(pickup_location);

-- Idempotency mirror (same shape as wallet-service idempotency table). Every
-- payment-touching call (subscription subscribe, ride create) carries a key.
CREATE TABLE IF NOT EXISTS rider_idempotency (
    key            TEXT PRIMARY KEY,
    user_id        UUID NOT NULL,
    operation      TEXT NOT NULL,
    resource_id    UUID,
    response_body  JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE INDEX IF NOT EXISTS idx_rider_idempotency_expires
    ON rider_idempotency(expires_at);
CREATE INDEX IF NOT EXISTS idx_rider_idempotency_user
    ON rider_idempotency(user_id);

-- 5.16 Audit logs -------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_admin_audit_logs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_user_id UUID NOT NULL,
    action        VARCHAR(120) NOT NULL,
    entity_type   VARCHAR(120) NOT NULL,
    entity_id     UUID,
    old_value     JSONB,
    new_value     JSONB,
    ip_address    VARCHAR(80),
    user_agent    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_admin_audit_logs_admin
    ON rider_admin_audit_logs(admin_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_admin_audit_logs_entity
    ON rider_admin_audit_logs(entity_type, entity_id);

-- ----------------------------------------------------------------------------
-- Seeds. Idempotent INSERTs (ON CONFLICT DO NOTHING) so the bootstrap is safe
-- to re-run on every cold start. We seed the v1 launch cities (Bengaluru,
-- Mumbai, Delhi), one whole-city zone each, six subscription plans (Trial /
-- Basic / Plus / Pro / Elite + Fleet Starter as is_active=false), and a
-- per-(city, vehicle_type) fare rule per Mopedu spec.
-- ----------------------------------------------------------------------------

INSERT INTO rider_cities (name, state, country, currency_code, is_active) VALUES
    ('Bengaluru', 'Karnataka',   'India', 'INR', TRUE),
    ('Mumbai',    'Maharashtra', 'India', 'INR', TRUE),
    ('Delhi',     'Delhi',       'India', 'INR', TRUE)
ON CONFLICT (name, state, country) DO NOTHING;

-- Default zone per city — bounding-box polygon covering the city. Rough
-- coordinates from public bounding-box data; admin can refine via /v1/rider/admin.
WITH blr AS (
    SELECT id FROM rider_cities WHERE name = 'Bengaluru' AND state = 'Karnataka'
), mum AS (
    SELECT id FROM rider_cities WHERE name = 'Mumbai' AND state = 'Maharashtra'
), del AS (
    SELECT id FROM rider_cities WHERE name = 'Delhi' AND state = 'Delhi'
)
INSERT INTO rider_zones (city_id, name, boundary, is_active)
SELECT id, 'Bengaluru — All',
       ST_GeogFromText('SRID=4326;POLYGON((77.45 12.83, 77.78 12.83, 77.78 13.14, 77.45 13.14, 77.45 12.83))'),
       TRUE
FROM blr
WHERE NOT EXISTS (SELECT 1 FROM rider_zones z WHERE z.city_id = blr.id AND z.name = 'Bengaluru — All')
UNION ALL
SELECT id, 'Mumbai — All',
       ST_GeogFromText('SRID=4326;POLYGON((72.78 18.89, 72.99 18.89, 72.99 19.27, 72.78 19.27, 72.78 18.89))'),
       TRUE
FROM mum
WHERE NOT EXISTS (SELECT 1 FROM rider_zones z WHERE z.city_id = mum.id AND z.name = 'Mumbai — All')
UNION ALL
SELECT id, 'Delhi — All',
       ST_GeogFromText('SRID=4326;POLYGON((76.83 28.40, 77.35 28.40, 77.35 28.88, 76.83 28.88, 76.83 28.40))'),
       TRUE
FROM del
WHERE NOT EXISTS (SELECT 1 FROM rider_zones z WHERE z.city_id = del.id AND z.name = 'Delhi — All');

-- Subscription plan seeds. Five live plans + one fleet plan (inactive).
INSERT INTO rider_subscription_plans
    (code, name, description, price_amount, currency_code, billing_period_days,
     lead_limit, fair_use_limit, priority_weight, is_unlimited, is_fleet_plan,
     max_drivers, grace_period_days, is_active)
VALUES
    ('trial_7d',          'Free Trial',     '10 ride leads for 7 days',                  0,    'INR',  7,   10,    10,   50,  FALSE, FALSE, NULL, 0, TRUE),
    ('basic_199',         'Basic Partner',  '100 ride leads per month',                  199,  'INR', 30,  100,   100,  80,  FALSE, FALSE, NULL, 3, TRUE),
    ('plus_299',          'Plus Partner',   '250 ride leads per month',                  299,  'INR', 30,  250,   250, 100,  FALSE, FALSE, NULL, 3, TRUE),
    ('pro_499',           'Pro Partner',    'Unlimited fair-use ride leads (1000)',      499,  'INR', 30, NULL,  1000, 130,  TRUE,  FALSE, NULL, 3, TRUE),
    ('elite_999',         'Elite Partner',  'Priority unlimited fair-use ride leads',    999,  'INR', 30, NULL,  2500, 160,  TRUE,  FALSE, NULL, 3, TRUE),
    ('fleet_starter_1999','Fleet Starter',  'Fleet plan up to 10 drivers',               1999, 'INR', 30, NULL,  5000, 100,  TRUE,  TRUE,    10, 3, FALSE)
ON CONFLICT (code) DO NOTHING;

-- Fare rule seeds per (city, vehicle_type). Matches the Mopedu spec sensible
-- India values. Idempotent via NOT EXISTS guard.
DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT id FROM rider_cities WHERE country = 'India' LOOP
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'bike') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, cancellation_fee)
            VALUES (c.id, 'bike',     15,  6,  0, 25,  10);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'auto') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, cancellation_fee)
            VALUES (c.id, 'auto',     25, 12,  0, 40,  15);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'mini_cab') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, cancellation_fee)
            VALUES (c.id, 'mini_cab', 50, 14,  1, 80,  25);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'sedan') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, cancellation_fee)
            VALUES (c.id, 'sedan',    70, 16,  1, 100, 30);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'suv') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, cancellation_fee)
            VALUES (c.id, 'suv',     100, 20,  2, 150, 40);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'premium') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, cancellation_fee)
            VALUES (c.id, 'premium', 150, 25,  2, 200, 50);
        END IF;
    END LOOP;
END $$;

-- ----------------------------------------------------------------------------
-- Sprint 2: ride lifecycle, offers, partner locations, ride payments.
--
-- Idempotent ALTER TABLE additions (IF NOT EXISTS) so re-running the bootstrap
-- against an S1 schema brings it forward to S2 without dropping data.
-- ----------------------------------------------------------------------------

-- Augment rider_rides for the full state machine. partner_arriving_at +
-- final_fare_paise + cancelled_by_kind + share_token are S2-new; the rest
-- already exist from S1 but the IF NOT EXISTS keeps this re-runnable.
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS partner_id              UUID REFERENCES rider_partners(id);
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS vehicle_id              UUID REFERENCES rider_vehicles(id);
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_hash                TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_expires_at          TIMESTAMPTZ;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS partner_arriving_at     TIMESTAMPTZ;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS started_at              TIMESTAMPTZ;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS completed_at            TIMESTAMPTZ;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS final_distance_km       NUMERIC(10,2);
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS final_duration_min      NUMERIC(10,2);
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS final_fare              NUMERIC(12,2);
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS final_fare_paise        BIGINT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cancellation_fee_paise  BIGINT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cancellation_reason     TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cancelled_by_kind       TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS rating                  SMALLINT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS rating_comment          TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS share_token             TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS flagged_for_review      BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_rider_rides_share_token
    ON rider_rides(share_token) WHERE share_token IS NOT NULL;

-- Status history. Every transition writes one row (actor + reason + ts).
CREATE TABLE IF NOT EXISTS rider_ride_status_history (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id       UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    from_status   TEXT,
    to_status     TEXT NOT NULL,
    actor_kind    TEXT NOT NULL,
    actor_user_id UUID,
    reason        TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_ride_status_history_ride
    ON rider_ride_status_history(ride_id, created_at);

-- Ride offers. Sent in batches of 5 with a 15s expiry per offer.
CREATE TABLE IF NOT EXISTS rider_ride_offers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id     UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    partner_id  UUID NOT NULL REFERENCES rider_partners(id),
    score       NUMERIC(10,2) NOT NULL,
    distance_km NUMERIC(8,2),
    expires_at  TIMESTAMPTZ NOT NULL,
    status      TEXT NOT NULL DEFAULT 'sent'
                CHECK (status IN ('sent','accepted','rejected','expired','superseded')),
    decided_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (ride_id, partner_id)
);
CREATE INDEX IF NOT EXISTS idx_rider_offers_ride_status
    ON rider_ride_offers(ride_id, status);
CREATE INDEX IF NOT EXISTS idx_rider_offers_partner
    ON rider_ride_offers(partner_id, status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_offers_expires
    ON rider_ride_offers(expires_at) WHERE status = 'sent';

-- ─── C2: accept/reject reasons + no-show signals ──────────────────────
ALTER TABLE rider_ride_offers
    ADD COLUMN IF NOT EXISTS reject_reason VARCHAR(120);

ALTER TABLE rider_rides
    ADD COLUMN IF NOT EXISTS no_show_reported_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS no_show_by         UUID;

ALTER TABLE rider_partners
    ADD COLUMN IF NOT EXISTS reject_count_30d INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS no_show_count_30d INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS last_location_at  TIMESTAMPTZ;

-- ─── C4: safety extensions ────────────────────────────────────────────
--
-- rider_masked_calls audits proxied calls so disputes can be replayed.
-- The provider stub returns a fake DID; production wires Exotel /
-- Knowlarity / Twilio Proxy via MASKED_CALL_PROVIDER env.
CREATE TABLE IF NOT EXISTS rider_masked_calls (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id     UUID REFERENCES rider_rides(id) ON DELETE SET NULL,
    caller_id   UUID NOT NULL,
    callee_id   UUID NOT NULL,
    proxy_did   TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    duration_s  INTEGER,
    status      TEXT NOT NULL DEFAULT 'initiated'
                CHECK (status IN ('initiated','connected','completed','failed'))
);
CREATE INDEX IF NOT EXISTS idx_rider_masked_calls_ride ON rider_masked_calls(ride_id, created_at DESC);

-- rider_safety_contact_alerts records every trusted-contact dispatch
-- (SMS / push / call) so an admin can audit who was notified for a SOS.
CREATE TABLE IF NOT EXISTS rider_safety_contact_alerts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id  UUID NOT NULL REFERENCES rider_safety_incidents(id) ON DELETE CASCADE,
    contact_phone TEXT NOT NULL,
    contact_name TEXT,
    channel      TEXT NOT NULL CHECK (channel IN ('sms','push','call')),
    result       TEXT NOT NULL CHECK (result IN ('sent','failed','queued')),
    error        TEXT,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_safety_alerts_incident ON rider_safety_contact_alerts(incident_id);

-- ─── C5: ride/partner rating moderation + partner response ────────────
ALTER TABLE rider_rides
    ADD COLUMN IF NOT EXISTS rating_visibility TEXT NOT NULL DEFAULT 'public'
        CHECK (rating_visibility IN ('public','hidden','flagged')),
    ADD COLUMN IF NOT EXISTS rating_hidden_by  UUID,
    ADD COLUMN IF NOT EXISTS rating_hidden_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS partner_response  TEXT,
    ADD COLUMN IF NOT EXISTS partner_responded_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_rider_rides_rating_visibility
    ON rider_rides(rating_visibility) WHERE rating IS NOT NULL AND rating_visibility != 'public';

-- ─── Wave F: per-ride chat + read receipts ────────────────────────────
CREATE TABLE IF NOT EXISTS rider_ride_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id     UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL,
    author_role TEXT NOT NULL CHECK (author_role IN ('customer','partner','admin')),
    body        TEXT NOT NULL,
    read_by     JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_ride_messages_ride ON rider_ride_messages(ride_id, created_at);

-- Live partner locations. The hot copy lives in Redis (keyed per city geohash);
-- this table is the durable mirror used by the cold-path matcher fallback.
CREATE TABLE IF NOT EXISTS rider_partner_locations (
    partner_id     UUID PRIMARY KEY REFERENCES rider_partners(id) ON DELETE CASCADE,
    last_lat       NUMERIC(9,6) NOT NULL,
    last_lng       NUMERIC(9,6) NOT NULL,
    last_geohash   TEXT NOT NULL,
    last_speed_mps NUMERIC(6,2),
    last_heading   NUMERIC(5,2),
    is_online      BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_locations_geohash
    ON rider_partner_locations(last_geohash) WHERE is_online = TRUE;

-- Per-ride payment record (cash / wallet / upi). cash settles informally;
-- wallet hits wallet-service; upi stays pending until the customer confirms.
CREATE TABLE IF NOT EXISTS rider_ride_payments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id        UUID NOT NULL REFERENCES rider_rides(id),
    partner_id     UUID NOT NULL REFERENCES rider_partners(id),
    amount_paise   BIGINT NOT NULL,
    payment_method TEXT NOT NULL CHECK (payment_method IN ('cash','wallet','upi')),
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending','succeeded','failed','refunded')),
    wallet_txn_id  UUID,
    upi_txn_ref    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_rider_ride_payments_ride
    ON rider_ride_payments(ride_id);
CREATE INDEX IF NOT EXISTS idx_rider_ride_payments_partner_status
    ON rider_ride_payments(partner_id, status, created_at DESC);

-- ----------------------------------------------------------------------------
-- Sprint 3: admin endpoints, safety, complaints, share-ride, audit.
--
-- Idempotent ALTER + CREATE TABLE IF NOT EXISTS additions per
-- mopedu/IMPLEMENTATION_PLAN.md §5 Sprint 3 backend deliverables. The audit
-- table from S1 (rider_admin_audit_logs) is extended with request-context
-- columns (path, method, ip, user_agent, body summary, response status,
-- latency) so the gin middleware can write a single row per admin action.
-- ----------------------------------------------------------------------------

-- 6.1 Complaints (rider_complaints) -------------------------------------------
CREATE TABLE IF NOT EXISTS rider_complaints (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id         UUID NOT NULL REFERENCES rider_rides(id),
    customer_id     UUID NOT NULL,
    partner_id      UUID,
    category        TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'open'
                    CHECK (status IN ('open','under_review','resolved','dismissed')),
    resolution_note TEXT,
    resolved_by     UUID,
    resolved_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_complaints_status
    ON rider_complaints(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_complaints_partner
    ON rider_complaints(partner_id);
CREATE INDEX IF NOT EXISTS idx_rider_complaints_customer
    ON rider_complaints(customer_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_complaints_ride
    ON rider_complaints(ride_id);

-- 6.2 Safety incidents (rider_safety_incidents) -------------------------------
CREATE TABLE IF NOT EXISTS rider_safety_incidents (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id          UUID REFERENCES rider_rides(id),
    customer_id      UUID,
    partner_id       UUID,
    kind             TEXT NOT NULL,
    severity         TEXT NOT NULL DEFAULT 'medium'
                     CHECK (severity IN ('low','medium','high','critical')),
    metadata         JSONB NOT NULL DEFAULT '{}',
    status           TEXT NOT NULL DEFAULT 'open'
                     CHECK (status IN ('open','acknowledged','resolved')),
    acknowledged_by  UUID,
    acknowledged_at  TIMESTAMPTZ,
    resolved_by      UUID,
    resolved_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_safety_status
    ON rider_safety_incidents(status, severity, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_safety_ride
    ON rider_safety_incidents(ride_id);
CREATE INDEX IF NOT EXISTS idx_rider_safety_customer
    ON rider_safety_incidents(customer_id);
CREATE INDEX IF NOT EXISTS idx_rider_safety_partner
    ON rider_safety_incidents(partner_id);

-- 6.3 Share-ride tokens (rider_share_tokens) ---------------------------------
CREATE TABLE IF NOT EXISTS rider_share_tokens (
    token          TEXT PRIMARY KEY,
    ride_id        UUID NOT NULL REFERENCES rider_rides(id),
    customer_id    UUID NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    view_count     INT NOT NULL DEFAULT 0,
    last_viewed_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_rider_share_tokens_ride
    ON rider_share_tokens(ride_id);
CREATE INDEX IF NOT EXISTS idx_rider_share_tokens_expires
    ON rider_share_tokens(expires_at);

-- 6.4 Trusted-contact records (rider_trusted_contacts) -----------------------
CREATE TABLE IF NOT EXISTS rider_trusted_contacts (
    user_id              UUID PRIMARY KEY,
    contact_name         TEXT NOT NULL,
    contact_phone        TEXT NOT NULL,
    contact_relationship TEXT,
    share_location_on_sos BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 6.5 Audit logs — extend the S1 table with request-context columns ----------
-- The gin middleware writes one row per /v1/rider/admin/* request; existing
-- service-level audit calls keep working because the new columns are nullable.
ALTER TABLE rider_admin_audit_logs
    ADD COLUMN IF NOT EXISTS request_path        TEXT;
ALTER TABLE rider_admin_audit_logs
    ADD COLUMN IF NOT EXISTS request_method      TEXT;
ALTER TABLE rider_admin_audit_logs
    ADD COLUMN IF NOT EXISTS request_body        TEXT;
ALTER TABLE rider_admin_audit_logs
    ADD COLUMN IF NOT EXISTS response_status     INT;
ALTER TABLE rider_admin_audit_logs
    ADD COLUMN IF NOT EXISTS latency_ms          INT;

CREATE INDEX IF NOT EXISTS idx_rider_admin_audit_logs_action
    ON rider_admin_audit_logs(action, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_admin_audit_logs_path
    ON rider_admin_audit_logs(request_path);

-- 6.6 Extend rider_partner_status enum with 'rejected' for the admin reject
-- flow. PostgreSQL has no IF NOT EXISTS form for ADD VALUE so we wrap in a
-- DO block + duplicate_object guard.
DO $$ BEGIN
    ALTER TYPE rider_partner_status ADD VALUE 'rejected';
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ----------------------------------------------------------------------------
-- Sprint 4: background jobs, fraud + revenue + reports.
--
-- Adds the support tables the cron framework needs (cron_runs log + reminder
-- bucket dedupe), the rolled-up daily revenue snapshot, and ALTER additions
-- on rider_partners + rider_partner_subscriptions to support nightly metrics
-- recalculation and wallet-driven auto-renewal.
-- ----------------------------------------------------------------------------

-- Daily revenue snapshot (rolled up by job, not query-on-the-fly).
CREATE TABLE IF NOT EXISTS rider_daily_revenue (
    date DATE NOT NULL,
    city_id UUID,
    plan_id UUID,
    subscriptions_count INT NOT NULL DEFAULT 0,
    subscriptions_revenue_paise BIGINT NOT NULL DEFAULT 0,
    rides_count INT NOT NULL DEFAULT 0,
    rides_completed INT NOT NULL DEFAULT 0,
    rides_cancelled INT NOT NULL DEFAULT 0,
    fare_total_paise BIGINT NOT NULL DEFAULT 0,
    cancellation_fees_paise BIGINT NOT NULL DEFAULT 0,
    computed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- (date, city_id, plan_id) is the natural key. NULLs participate in the
-- uniqueness via the COALESCE expression so the "all" rollup row de-dups too.
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_daily_revenue_dim
    ON rider_daily_revenue(
        date,
        COALESCE(city_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(plan_id, '00000000-0000-0000-0000-000000000000'::uuid)
    );

-- Document expiry reminders sent (so we don't spam).
CREATE TABLE IF NOT EXISTS rider_doc_reminders_sent (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id UUID NOT NULL REFERENCES rider_partners(id) ON DELETE CASCADE,
    document_id UUID NOT NULL,
    expires_at DATE NOT NULL,
    bucket TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (document_id, bucket)
);
CREATE INDEX IF NOT EXISTS idx_rider_doc_reminders_partner
    ON rider_doc_reminders_sent(partner_id);

-- Cron run log (so we can see when jobs last ran successfully).
CREATE TABLE IF NOT EXISTS rider_cron_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    status TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','succeeded','failed')),
    rows_processed INT NOT NULL DEFAULT 0,
    error_summary TEXT
);
CREATE INDEX IF NOT EXISTS idx_rider_cron_runs_job ON rider_cron_runs(job, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_rider_cron_runs_running
    ON rider_cron_runs(job, started_at DESC) WHERE status = 'running';

-- Partner metrics snapshot (recomputed nightly).
ALTER TABLE rider_partners
    ADD COLUMN IF NOT EXISTS metrics_recalc_at TIMESTAMPTZ;
ALTER TABLE rider_partners
    ADD COLUMN IF NOT EXISTS completion_rate NUMERIC(5,2) NOT NULL DEFAULT 0;

-- Subscription auto-renewal preference + state.
ALTER TABLE rider_partner_subscriptions
    ADD COLUMN IF NOT EXISTS renewal_attempted_at TIMESTAMPTZ;
ALTER TABLE rider_partner_subscriptions
    ADD COLUMN IF NOT EXISTS renewal_failure_count INT NOT NULL DEFAULT 0;


-- ============================================================
-- P0.3 — Outbox table for durable event publishing
-- ============================================================
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

-- ─── G4.5: scheduled rides ────────────────────────────────────────────
-- scheduled_for, if set, parks the ride in `scheduled` status until a
-- worker activates it (≈ T-15 min). On activation the worker calls
-- MatchRide so dispatch behaves like any just-booked ride.
--
-- The status enum is extended via ALTER TYPE ADD VALUE — Postgres 12+
-- runs this in a single transaction; IF NOT EXISTS keeps re-runs idempotent.
DO $$ BEGIN
    ALTER TYPE rider_ride_status ADD VALUE IF NOT EXISTS 'scheduled';
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE rider_rides
    ADD COLUMN IF NOT EXISTS scheduled_for      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS scheduled_lead_min INTEGER NOT NULL DEFAULT 15,
    ADD COLUMN IF NOT EXISTS activated_at       TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_rider_rides_scheduled
    ON rider_rides(scheduled_for)
    WHERE scheduled_for IS NOT NULL AND activated_at IS NULL;
