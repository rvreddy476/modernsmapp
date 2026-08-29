-- rider-service migration 001: Initial baseline schema
-- Matches setup.sql bootstrap DDL.

CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS postgis;

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
        'failed',
        'scheduled'
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
    completion_rate       NUMERIC(5,2) NOT NULL DEFAULT 0,
    reject_count_30d      INTEGER NOT NULL DEFAULT 0,
    no_show_count_30d     INTEGER NOT NULL DEFAULT 0,
    fraud_score           NUMERIC(6,2) NOT NULL DEFAULT 0,
    is_online             BOOLEAN NOT NULL DEFAULT FALSE,
    last_online_at        TIMESTAMPTZ,
    last_offline_at       TIMESTAMPTZ,
    last_location_at      TIMESTAMPTZ,
    metrics_recalc_at     TIMESTAMPTZ,
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

CREATE TABLE IF NOT EXISTS rider_partner_aadhaar_verifications (
    partner_id      UUID PRIMARY KEY REFERENCES rider_partners(id),
    digilocker_ref  TEXT NOT NULL,
    doc_type_hash   TEXT NOT NULL,
    issued_at       TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_vehicle_registration
    ON rider_vehicles(registration_number);

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
    cancelled_at           TIMESTAMPTZ,
    cancellation_reason    TEXT,
    renewal_failure_count  INT NOT NULL DEFAULT 0,
    renewal_attempted_at   TIMESTAMPTZ,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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
    final_fare_paise       BIGINT,
    cancellation_fee_paise BIGINT,
    partner_arriving_at    TIMESTAMPTZ,
    no_show_reported_at    TIMESTAMPTZ,
    no_show_by             UUID,
    rating_visibility      TEXT NOT NULL DEFAULT 'public',
    rating_hidden_by       UUID,
    rating_hidden_at       TIMESTAMPTZ,
    partner_response       TEXT,
    partner_responded_at   TIMESTAMPTZ,
    scheduled_lead_min     INTEGER NOT NULL DEFAULT 15,
    activated_at           TIMESTAMPTZ,
    flagged_for_review     BOOLEAN NOT NULL DEFAULT FALSE,
    share_token            TEXT,
    payment_method         VARCHAR(40),
    otp_hash               TEXT,
    otp_expires_at         TIMESTAMPTZ,
    scheduled_for          TIMESTAMPTZ,
    requested_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    assigned_at            TIMESTAMPTZ,
    arrived_at             TIMESTAMPTZ,
    started_at             TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    cancelled_at           TIMESTAMPTZ,
    cancelled_by           UUID,
    cancelled_by_kind      TEXT,
    cancelled_by_user_id   UUID,
    cancellation_reason    TEXT,
    customer_rating        INT,
    partner_rating         INT,
    customer_feedback      TEXT,
    partner_feedback       TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rider_idempotency (
    key            TEXT PRIMARY KEY,
    user_id        UUID NOT NULL,
    operation      TEXT NOT NULL,
    resource_id    UUID,
    response_body  JSONB,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at     TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);

CREATE TABLE IF NOT EXISTS rider_admin_audit_logs (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_user_id  UUID NOT NULL,
    action         VARCHAR(120) NOT NULL,
    entity_type    VARCHAR(120) NOT NULL,
    entity_id      UUID,
    old_value      JSONB,
    new_value      JSONB,
    ip_address     VARCHAR(80),
    user_agent     TEXT,
    request_path   TEXT,
    request_method TEXT,
    request_body   TEXT,
    response_status INT,
    latency_ms     INT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS rider_ride_offers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id     UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    partner_id  UUID NOT NULL REFERENCES rider_partners(id),
    score       NUMERIC(10,2) NOT NULL,
    distance_km NUMERIC(8,2),
    expires_at  TIMESTAMPTZ NOT NULL,
    status      TEXT NOT NULL DEFAULT 'sent'
                CHECK (status IN ('sent','accepted','rejected','expired','superseded')),
    reject_reason VARCHAR(120),
    decided_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (ride_id, partner_id)
);

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

CREATE TABLE IF NOT EXISTS rider_ride_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id     UUID NOT NULL REFERENCES rider_rides(id) ON DELETE CASCADE,
    author_id   UUID NOT NULL,
    author_role TEXT NOT NULL CHECK (author_role IN ('customer','partner','admin')),
    body        TEXT NOT NULL,
    read_by     JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE TABLE IF NOT EXISTS rider_ride_payments (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    ride_id        UUID NOT NULL REFERENCES rider_rides(id),
    partner_id     UUID NOT NULL REFERENCES rider_partners(id),
    amount_paise   BIGINT NOT NULL,
    payment_method TEXT NOT NULL CHECK (payment_method IN ('cash','wallet','upi')),
    status         TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending_cash_confirmation','pending','succeeded','failed','refunded')),
    wallet_txn_id  UUID,
    upi_txn_ref    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at     TIMESTAMPTZ
);

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

CREATE TABLE IF NOT EXISTS rider_share_tokens (
    token          TEXT PRIMARY KEY,
    ride_id        UUID NOT NULL REFERENCES rider_rides(id),
    customer_id    UUID NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    view_count     INT NOT NULL DEFAULT 0,
    last_viewed_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS rider_trusted_contacts (
    user_id              UUID PRIMARY KEY,
    contact_name         TEXT NOT NULL,
    contact_phone        TEXT NOT NULL,
    contact_relationship TEXT,
    share_location_on_sos BOOLEAN NOT NULL DEFAULT TRUE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

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

CREATE UNIQUE INDEX IF NOT EXISTS ux_rider_daily_revenue_dim
    ON rider_daily_revenue(
        date,
        COALESCE(city_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(plan_id, '00000000-0000-0000-0000-000000000000'::uuid)
    );

CREATE TABLE IF NOT EXISTS rider_doc_reminders_sent (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    partner_id UUID NOT NULL REFERENCES rider_partners(id) ON DELETE CASCADE,
    document_id UUID NOT NULL,
    expires_at DATE NOT NULL,
    bucket TEXT NOT NULL,
    sent_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (document_id, bucket)
);

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

CREATE SCHEMA IF NOT EXISTS rider;
CREATE TABLE IF NOT EXISTS rider.outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);
