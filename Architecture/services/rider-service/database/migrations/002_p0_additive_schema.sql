-- rider-service migration 002: P0 Additive Schema Capabilities
-- Implements immutable quote snapshots, dispatch attempts, consumer inbox,
-- payment reconciliation, location consents, safety actions, revision concurrency,
-- and active ride constraints.

-- 1. Quote snapshots -----------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_quote_snapshots (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id    UUID,
    city_id             UUID REFERENCES rider_cities(id),
    pickup_lat          NUMERIC(9,6) NOT NULL,
    pickup_lng          NUMERIC(9,6) NOT NULL,
    pickup_label        TEXT,
    pickup_place_id     TEXT,
    drop_lat            NUMERIC(9,6) NOT NULL,
    drop_lng            NUMERIC(9,6) NOT NULL,
    drop_label          TEXT,
    drop_place_id       TEXT,
    route_version       TEXT NOT NULL DEFAULT 'v1',
    fare_policy_version INT NOT NULL DEFAULT 1,
    distance_meters     INT NOT NULL DEFAULT 0,
    duration_seconds    INT NOT NULL DEFAULT 0,
    options             JSONB NOT NULL DEFAULT '[]',
    request_hash        TEXT NOT NULL,
    expires_at          TIMESTAMPTZ NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_quotes_expiry ON rider_quote_snapshots(id, expires_at);
CREATE INDEX IF NOT EXISTS idx_rider_quotes_user_created ON rider_quote_snapshots(customer_user_id, created_at);

-- 2. Dispatch attempts ---------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_dispatch_attempts (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id            UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    generation         INT NOT NULL DEFAULT 1,
    search_radius_km   NUMERIC(6,2) NOT NULL DEFAULT 5.0,
    strategy_version   TEXT NOT NULL DEFAULT 'v1',
    started_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at           TIMESTAMPTZ,
    outcome            TEXT NOT NULL DEFAULT 'pending'
                       CHECK (outcome IN ('pending','offered','assigned','no_drivers','cancelled','failed')),
    candidates_scanned INT NOT NULL DEFAULT 0,
    offers_sent        INT NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (ride_id, generation)
);

CREATE INDEX IF NOT EXISTS idx_rider_dispatch_ride_gen ON rider_dispatch_attempts(ride_id, generation);

-- 3. Consumer inbox ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_consumer_inbox (
    id                 BIGSERIAL PRIMARY KEY,
    consumer_name      VARCHAR(120) NOT NULL,
    event_id           VARCHAR(120) NOT NULL,
    aggregate_revision BIGINT NOT NULL DEFAULT 0,
    applied_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (consumer_name, event_id)
);

CREATE INDEX IF NOT EXISTS idx_rider_inbox_lookup ON rider_consumer_inbox(consumer_name, event_id);

-- 4. Payment reconciliation ----------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_payment_reconciliation (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id          UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    payment_id       UUID REFERENCES rider_ride_payments(id),
    observed_status  TEXT NOT NULL,
    canonical_status TEXT NOT NULL,
    attempt_count    INT NOT NULL DEFAULT 0,
    next_retry_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    terminal_reason  TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_pay_reconcile_retry
    ON rider_payment_reconciliation(next_retry_at)
    WHERE canonical_status = 'reconciliation_required';

-- 5. Location consents ---------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_location_consents (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL,
    role           TEXT NOT NULL CHECK (role IN ('rider','captain')),
    purpose        TEXT NOT NULL,
    notice_version TEXT NOT NULL,
    granted_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    withdrawn_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_location_consents_user ON rider_location_consents(user_id, role);

-- 6. Safety actions ------------------------------------------------------------
CREATE TABLE IF NOT EXISTS rider_safety_actions (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id UUID NOT NULL REFERENCES rider_safety_incidents(id) ON DELETE CASCADE,
    actor_id    UUID NOT NULL,
    actor_role  TEXT NOT NULL DEFAULT 'operator',
    action      TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rider_safety_actions_incident ON rider_safety_actions(incident_id, created_at);

-- 7. Additive columns and constraints on rider_rides ---------------------------
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS quote_id UUID REFERENCES rider_quote_snapshots(id);
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS revision INT NOT NULL DEFAULT 1;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_attempts INT NOT NULL DEFAULT 0;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_locked_until TIMESTAMPTZ;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cash_confirmed_at TIMESTAMPTZ;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cash_confirmed_by UUID;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS fare_breakdown JSONB NOT NULL DEFAULT '{}';

-- Partial unique index: At most one active ride per rider
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_rides_customer_active
    ON rider_rides(customer_user_id)
    WHERE status NOT IN ('completed','cancelled_by_customer','cancelled_by_partner','cancelled_by_admin','expired','failed');

-- Partial unique index: At most one active assigned ride per captain
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_rides_partner_active
    ON rider_rides(partner_id)
    WHERE status IN ('partner_assigned','partner_arriving','arrived','otp_verified','in_progress');

CREATE INDEX IF NOT EXISTS idx_rider_rides_revision ON rider_rides(id, revision);
CREATE INDEX IF NOT EXISTS idx_rider_rides_quote ON rider_rides(quote_id);

-- Add vehicle registration uniqueness and subscription renewal tracking
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_vehicle_registration ON rider_vehicles(registration_number);
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS renewal_failure_count INT NOT NULL DEFAULT 0;
ALTER TABLE rider_partner_subscriptions ADD COLUMN IF NOT EXISTS renewal_attempted_at TIMESTAMPTZ;

-- 8. Enhance rider_share_tokens for SHA-256 hashing at rest -------------------
ALTER TABLE rider_share_tokens ADD COLUMN IF NOT EXISTS token_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE rider_share_tokens ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_rider_share_tokens_hash
    ON rider_share_tokens(token_hash)
    WHERE revoked_at IS NULL;

-- 9. Transactional Outbox Events -----------------------------------------------
CREATE TABLE IF NOT EXISTS rider_outbox_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type     VARCHAR(100) NOT NULL,
    aggregate_type VARCHAR(100) NOT NULL,
    aggregate_id   VARCHAR(100) NOT NULL,
    payload        JSONB NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at   TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_rider_outbox_unpublished
    ON rider_outbox_events(created_at)
    WHERE published_at IS NULL;

-- 10. Rider OTP Secure Retrieval Columns --------------------------------------
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_code TEXT;
ALTER TABLE rider_rides ALTER COLUMN otp_code TYPE TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_encrypted BYTEA;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS otp_hash TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cancelled_by_kind TEXT;
ALTER TABLE rider_rides ADD COLUMN IF NOT EXISTS cancelled_by_user_id UUID;

-- 11. City Geofence Boundary Coordinates ---------------------------------------
ALTER TABLE rider_cities ADD COLUMN IF NOT EXISTS center_lat NUMERIC(9,6);
ALTER TABLE rider_cities ADD COLUMN IF NOT EXISTS center_lng NUMERIC(9,6);
ALTER TABLE rider_cities ADD COLUMN IF NOT EXISTS radius_km NUMERIC(6,2) DEFAULT 40.0;

UPDATE rider_cities SET center_lat = 12.9716, center_lng = 77.5946, radius_km = 45.0 WHERE name = 'Bengaluru' AND center_lat IS NULL;
UPDATE rider_cities SET center_lat = 19.0760, center_lng = 72.8777, radius_km = 45.0 WHERE name = 'Mumbai' AND center_lat IS NULL;
UPDATE rider_cities SET center_lat = 28.7041, center_lng = 77.1025, radius_km = 45.0 WHERE name = 'Delhi' AND center_lat IS NULL;
UPDATE rider_cities SET center_lat = 17.3850, center_lng = 78.4867, radius_km = 45.0 WHERE name = 'Hyderabad' AND center_lat IS NULL;

-- 12. Idempotency Request Fingerprint ------------------------------------------
ALTER TABLE rider_idempotency ADD COLUMN IF NOT EXISTS request_hash TEXT;

-- 13. Revenue Dimension Unique Index -------------------------------------------
CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_daily_revenue_dim
    ON rider_daily_revenue(
        date,
        COALESCE(city_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(plan_id, '00000000-0000-0000-0000-000000000000'::uuid)
    );

-- 14. Payment Status Constraint ------------------------------------------------
ALTER TABLE rider_ride_payments DROP CONSTRAINT IF EXISTS rider_ride_payments_status_check;
ALTER TABLE rider_ride_payments ADD CONSTRAINT rider_ride_payments_status_check
    CHECK (status IN ('pending_cash_confirmation','pending','succeeded','failed','refunded'));

-- 15. Seed Cities, Plans, and Fare Rules ---------------------------------------
INSERT INTO rider_cities (id, name, state, country, currency_code, center_lat, center_lng, radius_km, is_active)
VALUES
    ('a0000000-0000-0000-0000-000000000001', 'Bengaluru', 'Karnataka', 'India', 'INR', 12.9716, 77.5946, 45.0, TRUE),
    ('a0000000-0000-0000-0000-000000000002', 'Mumbai', 'Maharashtra', 'India', 'INR', 19.0760, 72.8777, 45.0, TRUE),
    ('a0000000-0000-0000-0000-000000000003', 'Delhi', 'Delhi', 'India', 'INR', 28.7041, 77.1025, 45.0, TRUE),
    ('a0000000-0000-0000-0000-000000000004', 'Hyderabad', 'Telangana', 'India', 'INR', 17.3850, 78.4867, 45.0, TRUE)
ON CONFLICT (name, state, country) DO UPDATE SET
    center_lat = EXCLUDED.center_lat,
    center_lng = EXCLUDED.center_lng,
    radius_km = EXCLUDED.radius_km,
    is_active = TRUE;

INSERT INTO rider_subscription_plans (id, code, name, description, price_amount, currency_code, billing_period_days, lead_limit, fair_use_limit, priority_weight, is_unlimited, is_fleet_plan, max_drivers, grace_period_days, is_active)
VALUES
    ('b0000000-0000-0000-0000-000000000001', 'trial_7d', '7-Day Free Trial', '7 days free trial with 15 ride leads', 0, 'INR', 7, 15, 15, 10, FALSE, FALSE, NULL, 1, TRUE),
    ('b0000000-0000-0000-0000-000000000002', 'basic_199', 'Basic Partner', '100 ride leads per month', 199, 'INR', 30, 100, 100, 50, FALSE, FALSE, NULL, 3, TRUE),
    ('b0000000-0000-0000-0000-000000000003', 'plus_299', 'Plus Partner', '250 ride leads per month', 299, 'INR', 30, 250, 250, 100, FALSE, FALSE, NULL, 3, TRUE),
    ('b0000000-0000-0000-0000-000000000004', 'pro_499', 'Pro Partner', 'Unlimited fair-use ride leads (1000)', 499, 'INR', 30, NULL, 1000, 130, TRUE, FALSE, NULL, 3, TRUE),
    ('b0000000-0000-0000-0000-000000000005', 'elite_999', 'Elite Partner', 'Priority unlimited fair-use ride leads', 999, 'INR', 30, NULL, 2500, 160, TRUE, FALSE, NULL, 3, TRUE),
    ('b0000000-0000-0000-0000-000000000006', 'fleet_starter_1999', 'Fleet Starter', 'Fleet plan up to 10 drivers', 1999, 'INR', 30, NULL, 5000, 100, TRUE, TRUE, 10, 3, FALSE)
ON CONFLICT (code) DO UPDATE SET
    name = EXCLUDED.name,
    price_amount = EXCLUDED.price_amount,
    is_active = EXCLUDED.is_active;

DO $$
DECLARE
    c RECORD;
BEGIN
    FOR c IN SELECT id FROM rider_cities WHERE country = 'India' LOOP
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'bike') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, platform_fee, cancellation_fee)
            VALUES (c.id, 'bike', 15, 6, 0, 25, 0, 10);
        END IF;
        IF NOT EXISTS (SELECT 1 FROM rider_fare_rules WHERE city_id = c.id AND vehicle_type = 'auto') THEN
            INSERT INTO rider_fare_rules (city_id, vehicle_type, base_fare, per_km_fare, per_minute_fare, minimum_fare, platform_fee, cancellation_fee)
            VALUES (c.id, 'auto', 25, 12, 0, 40, 0, 15);
        END IF;
    END LOOP;
END $$;
