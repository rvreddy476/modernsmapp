# Module: rider-service

_Auto-extracted from source (DDL + route registrations). Verbatim — the source of truth is the code._

## HTTP routes
```
GET /audit-logs
GET /cities
GET /complaints
GET /complaints/me
GET /dashboard
GET /documents
GET /offers/incoming
GET /partners
GET /partners/:id
GET /partners/me
GET /partners/me/dashboard
GET /partners/me/documents
GET /partners/me/earnings
GET /partners/me/vehicles
GET /payments
GET /reports/cohort-retention
GET /reports/compliance
GET /reports/cron-runs
GET /reports/customer-cohort
GET /reports/matching-health
GET /reports/partner-quality
GET /reports/revenue
GET /reports/safety
GET /reports/supply-demand
GET /rides
GET /rides/:id
GET /rides/:id/messages
GET /rides/live
GET /rides/me
GET /safety-incidents
GET /safety/incidents/:id/alerts
GET /share/:token
GET /subscriptions/me
GET /subscriptions/plans
GET /trusted-contact
GET /vehicles
GET /vehicles/:id/documents
PATCH /cities/:id
PATCH /fare-rules/:id
PATCH /partners/me
PATCH /zones/:id
POST /cities
POST /complaints/:id/update-status
POST /documents/:id/reject
POST /documents/:id/verify
POST /estimate
POST /fare-rules
POST /no-label
POST /offers/:id/accept
POST /offers/:id/reject
POST /partners
POST /partners/:id/approve
POST /partners/:id/block
POST /partners/:id/reject
POST /partners/:id/suspend
POST /partners/me/aadhaar/callback
POST /partners/me/aadhaar/start
POST /partners/me/documents
POST /partners/me/location
POST /partners/me/offline
POST /partners/me/online
POST /partners/me/vehicles
POST /payments/:id/reject
POST /payments/:id/verify
POST /realtime/token
POST /rides
POST /rides/:id/arrived
POST /rides/:id/arriving
POST /rides/:id/cancel
POST /rides/:id/complain
POST /rides/:id/complete
POST /rides/:id/messages
POST /rides/:id/messages/:msgId/read
POST /rides/:id/no-show
POST /rides/:id/rate
POST /rides/:id/rating/response
POST /rides/:id/rating/visibility
POST /rides/:id/share
POST /rides/:id/sos
POST /rides/:id/start
POST /safety-incidents/:id/acknowledge
POST /safety-incidents/:id/resolve
POST /safety/masked-call
POST /subscriptions/payment-proof
POST /subscriptions/subscribe
POST /vehicles/:id/documents
POST /vehicles/:id/reject
POST /vehicles/:id/verify
POST /zones
PUT /trusted-contact
GROUP /v1/rider
GROUP /v1/rider/admin
```

## Database schema (CREATE TABLE — full column DDL)
```sql
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
    cancelled_at        TIMESTAMPTZ,
    cancellation_reason TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
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
                   CHECK (status IN ('pending','succeeded','failed','refunded')),
    wallet_txn_id  UUID,
    upi_txn_ref    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at     TIMESTAMPTZ
);

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

CREATE TABLE IF NOT EXISTS rider.outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    event_type      TEXT NOT NULL,
    partition_key   TEXT NOT NULL,
    payload         JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ
);

```

## API types (request/response Go structs with JSON tags)
```go
type reasonRequest struct {
	Reason string `json:"reason"`
}

type resolveIncidentRequest struct {
	Note string `json:"note"`
}

type createCityRequest struct {
	Name         string `json:"name"`
	State        string `json:"state,omitempty"`
	Country      string `json:"country,omitempty"`
	CurrencyCode string `json:"currency_code,omitempty"`
}

type updateCityRequest struct {
	Name         *string `json:"name,omitempty"`
	State        *string `json:"state,omitempty"`
	Country      *string `json:"country,omitempty"`
	CurrencyCode *string `json:"currency_code,omitempty"`
	IsActive     *bool   `json:"is_active,omitempty"`
}

type createZoneRequest struct {
	CityID      uuid.UUID `json:"city_id"`
	Name        string    `json:"name"`
	BoundaryWKT string    `json:"boundary_wkt"`
}

type updateZoneRequest struct {
	Name        *string `json:"name,omitempty"`
	BoundaryWKT *string `json:"boundary_wkt,omitempty"`
	IsActive    *bool   `json:"is_active,omitempty"`
}

type createFareRuleRequest struct {
	CityID          uuid.UUID `json:"city_id"`
	VehicleType     string    `json:"vehicle_type"`
	BaseFare        float64   `json:"base_fare"`
	PerKMFare       float64   `json:"per_km_fare"`
	PerMinuteFare   float64   `json:"per_minute_fare"`
	MinimumFare     float64   `json:"minimum_fare"`
	PlatformFee     float64   `json:"platform_fee"`
	NightMultiplier float64   `json:"night_multiplier"`
	PeakMultiplier  float64   `json:"peak_multiplier"`
	CancellationFee float64   `json:"cancellation_fee"`
}

type updateFareRuleRequest struct {
	BaseFare        *float64 `json:"base_fare,omitempty"`
	PerKMFare       *float64 `json:"per_km_fare,omitempty"`
	PerMinuteFare   *float64 `json:"per_minute_fare,omitempty"`
	MinimumFare     *float64 `json:"minimum_fare,omitempty"`
	PlatformFee     *float64 `json:"platform_fee,omitempty"`
	NightMultiplier *float64 `json:"night_multiplier,omitempty"`
	PeakMultiplier  *float64 `json:"peak_multiplier,omitempty"`
	CancellationFee *float64 `json:"cancellation_fee,omitempty"`
	IsActive        *bool    `json:"is_active,omitempty"`
}

type createComplaintRequest struct {
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

type updateComplaintStatusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

type submitDocumentRequest struct {
	DocumentType   string  `json:"document_type"`
	DocumentNumber *string `json:"document_number,omitempty"`
	FileURL        string  `json:"file_url"`
	ExpiresAt      *string `json:"expires_at,omitempty"` // RFC3339
}

type estimateRequest struct {
	PickupLat       float64   `json:"pickup_lat"`
	PickupLng       float64   `json:"pickup_lng"`
	DropLat         float64   `json:"drop_lat"`
	DropLng         float64   `json:"drop_lng"`
	VehicleType     string    `json:"vehicle_type"`
	CityID          uuid.UUID `json:"city_id"`
	SurgeMultiplier float64   `json:"surge_multiplier,omitempty"`
}

type InitiateMaskedCallRequest struct {
	CalleeID uuid.UUID  `json:"callee_id"`
	RideID   *uuid.UUID `json:"ride_id,omitempty"`
}

type PostNoShowRequest struct {
	Reason string `json:"reason,omitempty"`
}

type rejectOfferRequest struct {
	Reason string `json:"reason"`
}

type createPartnerRequest struct {
	PartnerType string     `json:"partner_type"`
	FullName    string     `json:"full_name"`
	Phone       string     `json:"phone"`
	Email       *string    `json:"email,omitempty"`
	CityID      *uuid.UUID `json:"city_id,omitempty"`
}

type patchPartnerRequest struct {
	FullName        *string    `json:"full_name,omitempty"`
	Email           *string    `json:"email,omitempty"`
	ProfilePhotoURL *string    `json:"profile_photo_url,omitempty"`
	CityID          *uuid.UUID `json:"city_id,omitempty"`
}

type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

type updateLocationRequest struct {
	Lat     float64  `json:"lat"`
	Lng     float64  `json:"lng"`
	Speed   *float64 `json:"speed,omitempty"`
	Heading *float64 `json:"heading,omitempty"`
}

type AdminHideRatingRequest struct {
	Visibility string `json:"visibility"` // hidden | flagged | public
}

type PartnerRespondRatingRequest struct {
	Response string `json:"response"`
}

type AppendRideMessageRequest struct {
	Body string `json:"body"`
}

type MarkRideMessageReadRequest struct {
	Role string `json:"role"` // customer | partner | admin
}

type cancelRideRequest struct {
	Reason         string `json:"reason"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type rateRideRequest struct {
	Rating  int16  `json:"rating"`
	Comment string `json:"comment,omitempty"`
}

type startRideRequest struct {
	OTP string `json:"otp"`
}

type completeRideRequest struct {
	FinalDistanceKM  float64 `json:"final_distance_km"`
	FinalDurationMin int     `json:"final_duration_min"`
	IdempotencyKey   string  `json:"idempotency_key"`
}

type rideLocation struct {
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

type createRideRequest struct {
	Pickup         rideLocation `json:"pickup"`
	Drop           rideLocation `json:"drop"`
	VehicleType    string       `json:"vehicle_type"`
	CityID         *uuid.UUID   `json:"city_id,omitempty"`
	PaymentMethod  string       `json:"payment_method,omitempty"`
	IdempotencyKey string       `json:"idempotency_key"`
}

type sosRequest struct {
	Lat *float64 `json:"lat,omitempty"`
	Lng *float64 `json:"lng,omitempty"`
}

type trustedContactRequest struct {
	Name         string  `json:"name"`
	Phone        string  `json:"phone"`
	Relationship *string `json:"relationship,omitempty"`
	ShareOnSOS   bool    `json:"share_on_sos"`
}

type subscribeRequest struct {
	PlanID         uuid.UUID `json:"plan_id"`
	PaymentMethod  string    `json:"payment_method"`
	IdempotencyKey string    `json:"idempotency_key"`
}

type paymentProofRequest struct {
	PaymentID uuid.UUID `json:"payment_id"`
	FileURL   string    `json:"file_url"`
}

type addVehicleRequest struct {
	VehicleType        string  `json:"vehicle_type"`
	RegistrationNumber string  `json:"registration_number"`
	Brand              *string `json:"brand,omitempty"`
	Model              *string `json:"model,omitempty"`
	Color              *string `json:"color,omitempty"`
	ManufactureYear    *int    `json:"manufacture_year,omitempty"`
	Year               *int    `json:"year,omitempty"` // alias accepted from mobile
	SeatCount          *int    `json:"seat_count,omitempty"`
	FuelType           *string `json:"fuel_type,omitempty"`
	IsEV               bool    `json:"is_ev,omitempty"`
}
```
