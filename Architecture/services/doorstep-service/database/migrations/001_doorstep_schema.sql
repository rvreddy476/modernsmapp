-- doorstep-service 001: the full `doorstep` schema (Doorstep home services).
--
-- Every table of the programme's data model is created here so later lanes
-- (A2 onboarding, A3 bookings/payments, A4 dispatch, A5 visit, A6 admin) add
-- code, not tables. Money is integer paise (BIGINT, *_paise). Idempotent and
-- re-runnable: IF NOT EXISTS everywhere and every constraint inline, so the
-- file can be executed twice against the same database (tested).
--
-- Extensions: postgis (zones, points), btree_gist (exclusion constraints that
-- mix = on uuid/text with && on ranges). On RDS both are allow-listed; the
-- Terraform lane creates btree_gist for the app database.

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE SCHEMA IF NOT EXISTS doorstep;

-- =====================================================================
-- Cities, zones and operating config
-- =====================================================================

CREATE TABLE IF NOT EXISTS doorstep.cities (
    code                              TEXT PRIMARY KEY CHECK (code ~ '^[A-Z]{3}$'),
    name                              TEXT NOT NULL,
    state_code                        TEXT NOT NULL CHECK (state_code ~ '^[0-9]{2}$'), -- GST state code (Telangana 36)
    timezone                          TEXT NOT NULL DEFAULT 'Asia/Kolkata',
    active                            BOOLEAN NOT NULL DEFAULT FALSE,
    extras_charge_now_threshold_paise BIGINT NOT NULL DEFAULT 300000 CHECK (extras_charge_now_threshold_paise >= 0),
    extras_grace_minutes              INT NOT NULL DEFAULT 15 CHECK (extras_grace_minutes BETWEEN 0 AND 1440),
    max_jobs_per_day                  INT NOT NULL DEFAULT 6 CHECK (max_jobs_per_day BETWEEN 1 AND 24),
    offer_window_far_minutes          INT NOT NULL DEFAULT 120 CHECK (offer_window_far_minutes BETWEEN 1 AND 1440),
    offer_window_near_minutes         INT NOT NULL DEFAULT 10 CHECK (offer_window_near_minutes BETWEEN 1 AND 1440),
    offer_far_threshold_minutes       INT NOT NULL DEFAULT 720 CHECK (offer_far_threshold_minutes BETWEEN 1 AND 10080),
    created_at                        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at                        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS doorstep.zones (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_code             TEXT NOT NULL REFERENCES doorstep.cities(code),
    name                  TEXT NOT NULL,
    slug                  TEXT NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    boundary              geography(MultiPolygon, 4326) NOT NULL,
    travel_buffer_minutes INT NOT NULL DEFAULT 30 CHECK (travel_buffer_minutes BETWEEN 0 AND 240),
    active                BOOLEAN NOT NULL DEFAULT TRUE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (city_code, slug)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_zones_boundary ON doorstep.zones USING gist (boundary);

-- =====================================================================
-- Catalogue
-- =====================================================================

CREATE TABLE IF NOT EXISTS doorstep.skills (
    code        TEXT PRIMARY KEY CHECK (code ~ '^[a-z][a-z0-9_]{1,47}$'),
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS doorstep.categories (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug          TEXT NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    -- family drives the GST category (shared/gst <FAMILY>_VIA_ECO / _REGISTERED)
    family        TEXT NOT NULL CHECK (family IN ('HOME_CLEANING','PEST_CONTROL','APPLIANCE_REPAIR','INSTALLATION_REPAIR','PAINTING','BEAUTY_SALON')),
    gender_rule   TEXT NOT NULL DEFAULT 'any' CHECK (gender_rule IN ('any','female_pros_only','male_pros_only')),
    extras_policy TEXT NOT NULL DEFAULT 'rate_card' CHECK (extras_policy IN ('rate_card','catalogue_addons_only')),
    image_url     TEXT,
    sort_order    INT NOT NULL DEFAULT 0,
    active        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Salon allows catalogue add-ons only as extras (never a rate card).
    CHECK (family <> 'BEAUTY_SALON' OR extras_policy = 'catalogue_addons_only')
);

CREATE TABLE IF NOT EXISTS doorstep.services (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category_id       UUID NOT NULL REFERENCES doorstep.categories(id),
    slug              TEXT NOT NULL CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    duration_minutes  INT NOT NULL CHECK (duration_minutes BETWEEN 15 AND 720),
    required_skill    TEXT NOT NULL REFERENCES doorstep.skills(code),
    inclusions        TEXT[] NOT NULL DEFAULT '{}',
    exclusions        TEXT[] NOT NULL DEFAULT '{}',
    image_url         TEXT,
    -- Crew bookings are modelled but switched off at launch: crew_size = 1.
    crew_size         INT NOT NULL DEFAULT 1 CHECK (crew_size BETWEEN 1 AND 6),
    min_before_photos INT NOT NULL DEFAULT 2 CHECK (min_before_photos BETWEEN 0 AND 10),
    min_after_photos  INT NOT NULL DEFAULT 2 CHECK (min_after_photos BETWEEN 0 AND 10),
    rework_days       INT NOT NULL DEFAULT 7 CHECK (rework_days BETWEEN 0 AND 90),
    sort_order        INT NOT NULL DEFAULT 0,
    active            BOOLEAN NOT NULL DEFAULT FALSE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (category_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_services_category ON doorstep.services (category_id, sort_order);

CREATE TABLE IF NOT EXISTS doorstep.service_options (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id       UUID NOT NULL REFERENCES doorstep.services(id),
    name             TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    duration_minutes INT NOT NULL CHECK (duration_minutes BETWEEN 5 AND 720), -- per unit
    max_quantity     INT NOT NULL DEFAULT 1 CHECK (max_quantity BETWEEN 1 AND 20),
    is_default       BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order       INT NOT NULL DEFAULT 0,
    active           BOOLEAN NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_options_service ON doorstep.service_options (service_id, sort_order);
-- At most one default option per service.
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_options_default
    ON doorstep.service_options (service_id) WHERE is_default;

-- Add-on groups follow food-service's rules: count >= max(min_select,
-- is_required ? 1 : 0) and count <= max_select.
CREATE TABLE IF NOT EXISTS doorstep.addon_groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id  UUID NOT NULL REFERENCES doorstep.services(id),
    name        TEXT NOT NULL,
    min_select  INT NOT NULL DEFAULT 0 CHECK (min_select >= 0),
    max_select  INT NOT NULL CHECK (max_select >= 1),
    is_required BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order  INT NOT NULL DEFAULT 0,
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (min_select <= max_select)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_addon_groups_service ON doorstep.addon_groups (service_id, sort_order);

CREATE TABLE IF NOT EXISTS doorstep.addons (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id               UUID NOT NULL REFERENCES doorstep.addon_groups(id),
    name                   TEXT NOT NULL,
    description            TEXT NOT NULL DEFAULT '',
    extra_duration_minutes INT NOT NULL DEFAULT 0 CHECK (extra_duration_minutes BETWEEN 0 AND 240),
    sort_order             INT NOT NULL DEFAULT 0,
    active                 BOOLEAN NOT NULL DEFAULT TRUE,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_addons_group ON doorstep.addons (group_id, sort_order);

-- Effective-dated, GST-inclusive city prices for options and add-ons. A new
-- price closes the open row of the same (city, item); overlapping periods are
-- impossible (exclusion constraints, btree_gist).
CREATE TABLE IF NOT EXISTS doorstep.city_prices (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_code      TEXT NOT NULL REFERENCES doorstep.cities(code),
    item_kind      TEXT NOT NULL CHECK (item_kind IN ('option','addon')),
    option_id      UUID REFERENCES doorstep.service_options(id),
    addon_id       UUID REFERENCES doorstep.addons(id),
    price_paise    BIGINT NOT NULL CHECK (price_paise > 0),
    mrp_paise      BIGINT CHECK (mrp_paise IS NULL OR mrp_paise > 0),
    effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to   TIMESTAMPTZ,
    created_by     UUID,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((item_kind = 'option' AND option_id IS NOT NULL AND addon_id IS NULL)
        OR (item_kind = 'addon' AND addon_id IS NOT NULL AND option_id IS NULL)),
    CHECK (effective_to IS NULL OR effective_to > effective_from),
    CHECK (mrp_paise IS NULL OR mrp_paise >= price_paise),
    CONSTRAINT ex_doorstep_option_price_period EXCLUDE USING gist (
        city_code WITH =, option_id WITH =, tstzrange(effective_from, effective_to, '[)') WITH &&
    ) WHERE (option_id IS NOT NULL),
    CONSTRAINT ex_doorstep_addon_price_period EXCLUDE USING gist (
        city_code WITH =, addon_id WITH =, tstzrange(effective_from, effective_to, '[)') WITH &&
    ) WHERE (addon_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_prices_option ON doorstep.city_prices (option_id, city_code, effective_from DESC) WHERE option_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_prices_addon ON doorstep.city_prices (addon_id, city_code, effective_from DESC) WHERE addon_id IS NOT NULL;

-- Extras rate card per city and category (not free text). Salon categories
-- (extras_policy catalogue_addons_only) have no rate card.
CREATE TABLE IF NOT EXISTS doorstep.rate_cards (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_code    TEXT NOT NULL REFERENCES doorstep.cities(code),
    category_id  UUID NOT NULL REFERENCES doorstep.categories(id),
    code         TEXT NOT NULL CHECK (code ~ '^[a-z0-9_]{2,64}$'),
    name         TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    unit         TEXT NOT NULL CHECK (unit IN ('per_item','per_metre','per_hour','per_visit')),
    price_paise  BIGINT NOT NULL CHECK (price_paise > 0),
    max_quantity INT NOT NULL DEFAULT 10 CHECK (max_quantity BETWEEN 1 AND 100),
    is_part      BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order   INT NOT NULL DEFAULT 0,
    active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (city_code, category_id, code)
);

CREATE TABLE IF NOT EXISTS doorstep.slot_configs (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_code         TEXT NOT NULL REFERENCES doorstep.cities(code),
    category_id       UUID REFERENCES doorstep.categories(id), -- NULL = city default
    open_time         TIME NOT NULL DEFAULT '08:00',
    close_time        TIME NOT NULL DEFAULT '20:00',
    slot_step_minutes INT NOT NULL DEFAULT 30 CHECK (slot_step_minutes IN (15, 30, 60, 90, 120)),
    min_lead_minutes  INT NOT NULL DEFAULT 120 CHECK (min_lead_minutes BETWEEN 0 AND 2880),
    horizon_days      INT NOT NULL DEFAULT 7 CHECK (horizon_days BETWEEN 1 AND 60),
    hold_minutes      INT NOT NULL DEFAULT 10 CHECK (hold_minutes BETWEEN 1 AND 60),
    active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (close_time > open_time)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_slot_config_city_default
    ON doorstep.slot_configs (city_code) WHERE category_id IS NULL AND active;
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_slot_config_category
    ON doorstep.slot_configs (city_code, category_id) WHERE category_id IS NOT NULL AND active;

-- Cancellation fee rules (placeholders, editable as data). The first active
-- rule (category-specific first, then sort_order) whose stage matches and
-- whose minutes_before_lt is NULL or > minutes left decides the fee.
CREATE TABLE IF NOT EXISTS doorstep.cancellation_rules (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_code         TEXT NOT NULL REFERENCES doorstep.cities(code),
    category_id       UUID REFERENCES doorstep.categories(id),
    stage             TEXT NOT NULL CHECK (stage IN ('unassigned','assigned','en_route','arrived','in_progress')),
    minutes_before_lt INT CHECK (minutes_before_lt IS NULL OR minutes_before_lt > 0),
    fee_paise         BIGINT NOT NULL DEFAULT 0 CHECK (fee_paise >= 0),
    allowed           BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order        INT NOT NULL DEFAULT 0,
    active            BOOLEAN NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_cancel_rules_city ON doorstep.cancellation_rules (city_code, stage, sort_order) WHERE active;

CREATE TABLE IF NOT EXISTS doorstep.commission_rules (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_code      TEXT NOT NULL REFERENCES doorstep.cities(code),
    category_id    UUID REFERENCES doorstep.categories(id),
    commission_bps INT NOT NULL CHECK (commission_bps BETWEEN 0 AND 5000),
    effective_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    effective_to   TIMESTAMPTZ,
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (effective_to IS NULL OR effective_to > effective_from)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_commission_city ON doorstep.commission_rules (city_code, category_id) WHERE active;

-- =====================================================================
-- Professionals (A2)
-- =====================================================================

CREATE TABLE IF NOT EXISTS doorstep.professionals (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL UNIQUE,
    status                TEXT NOT NULL DEFAULT 'draft'
                          CHECK (status IN ('draft','pending_verification','approved','suspended','rejected','blocked')),
    display_name          TEXT NOT NULL,
    city_code             TEXT NOT NULL REFERENCES doorstep.cities(code),
    -- Gender comes from DigiLocker Aadhaar only, never self-declared.
    gender                TEXT CHECK (gender IN ('female','male','other')),
    gender_source         TEXT CHECK (gender_source IN ('digilocker')),
    CHECK ((gender IS NULL) = (gender_source IS NULL)),
    photo_media_id        TEXT,
    home_point            geography(Point, 4326),
    service_radius_m      INT NOT NULL DEFAULT 8000 CHECK (service_radius_m BETWEEN 1000 AND 30000),
    max_jobs_per_day      INT NOT NULL DEFAULT 6 CHECK (max_jobs_per_day BETWEEN 1 AND 24),
    rating_sum            BIGINT NOT NULL DEFAULT 0,
    rating_count          INT NOT NULL DEFAULT 0,
    jobs_completed        INT NOT NULL DEFAULT 0,
    offers_received       INT NOT NULL DEFAULT 0,
    offers_accepted       INT NOT NULL DEFAULT 0,
    cancellations_count   INT NOT NULL DEFAULT 0,
    rework_count          INT NOT NULL DEFAULT 0,
    on_duty               BOOLEAN NOT NULL DEFAULT FALSE,
    on_duty_since         TIMESTAMPTZ,
    agreement_version     TEXT,
    agreement_accepted_at TIMESTAMPTZ,
    pan_sealed            BYTEA,
    pan_last4             TEXT,
    status_reason         TEXT,
    incident_suspended    BOOLEAN NOT NULL DEFAULT FALSE,
    approved_at           TIMESTAMPTZ,
    approved_by           UUID,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_pros_status ON doorstep.professionals (status, city_code);
CREATE INDEX IF NOT EXISTS idx_doorstep_pros_home ON doorstep.professionals USING gist (home_point);

CREATE TABLE IF NOT EXISTS doorstep.pro_skills (
    pro_id      UUID NOT NULL REFERENCES doorstep.professionals(id),
    skill_code  TEXT NOT NULL REFERENCES doorstep.skills(code),
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','revoked')),
    verified_at TIMESTAMPTZ,
    verified_by UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pro_id, skill_code)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_skills_skill ON doorstep.pro_skills (skill_code) WHERE status = 'verified';

CREATE TABLE IF NOT EXISTS doorstep.pro_zones (
    pro_id     UUID NOT NULL REFERENCES doorstep.professionals(id),
    zone_id    UUID NOT NULL REFERENCES doorstep.zones(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pro_id, zone_id)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_zones_zone ON doorstep.pro_zones (zone_id);

CREATE TABLE IF NOT EXISTS doorstep.pro_weekly_hours (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id     UUID NOT NULL REFERENCES doorstep.professionals(id),
    weekday    SMALLINT NOT NULL CHECK (weekday BETWEEN 0 AND 6), -- 0 = Sunday
    start_time TIME NOT NULL,
    end_time   TIME NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (end_time > start_time)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_hours ON doorstep.pro_weekly_hours (pro_id, weekday);

CREATE TABLE IF NOT EXISTS doorstep.pro_days_off (
    pro_id     UUID NOT NULL REFERENCES doorstep.professionals(id),
    day        DATE NOT NULL,
    reason     TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (pro_id, day)
);

CREATE TABLE IF NOT EXISTS doorstep.pro_kyc_checks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id      UUID NOT NULL REFERENCES doorstep.professionals(id),
    kind        TEXT NOT NULL CHECK (kind IN ('digilocker_aadhaar','selfie_face_match','pan','bank')),
    status      TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','passed','failed','expired')),
    provider    TEXT NOT NULL DEFAULT '',
    reference   TEXT,
    score       NUMERIC(6,2),
    details     JSONB NOT NULL DEFAULT '{}'::jsonb, -- masked values and media ids only
    verified_at TIMESTAMPTZ,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_kyc ON doorstep.pro_kyc_checks (pro_id, kind, created_at DESC);

CREATE TABLE IF NOT EXISTS doorstep.pro_documents (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id                UUID NOT NULL REFERENCES doorstep.professionals(id),
    kind                  TEXT NOT NULL CHECK (kind IN ('police_certificate','aadhaar','pan','other')),
    media_id              TEXT NOT NULL,
    number_sealed         BYTEA,
    number_last4          TEXT,
    status                TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','rejected')),
    issued_on             DATE,
    expires_on            DATE,
    reason                TEXT,
    reviewed_by           UUID,
    reviewed_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_docs_queue ON doorstep.pro_documents (status, created_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_doorstep_pro_docs_pro ON doorstep.pro_documents (pro_id, kind);

-- Background checks: an admin-reviewed police certificate at launch
-- (source uploaded_document), a vendor later (source provider). Valid 12
-- months; dispatch requires a clear check valid on the slot date.
CREATE TABLE IF NOT EXISTS doorstep.background_checks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id       UUID NOT NULL REFERENCES doorstep.professionals(id),
    source       TEXT NOT NULL CHECK (source IN ('provider','uploaded_document')),
    provider     TEXT,
    external_ref TEXT,
    document_id  UUID REFERENCES doorstep.pro_documents(id),
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','clear','consider','failed','expired')),
    valid_from   DATE,
    valid_until  DATE,
    reviewed_by  UUID,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (source <> 'uploaded_document' OR document_id IS NOT NULL),
    CHECK (source <> 'provider' OR provider IS NOT NULL),
    CHECK (status <> 'clear' OR (valid_from IS NOT NULL AND valid_until IS NOT NULL AND valid_until > valid_from)),
    UNIQUE (provider, external_ref)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_bg_pro ON doorstep.background_checks (pro_id, status, valid_until);

-- Payout accounts (sealed with shared/pii; payouts are OFF, settlements are
-- computed only).
CREATE TABLE IF NOT EXISTS doorstep.pro_payout_accounts (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id                UUID NOT NULL REFERENCES doorstep.professionals(id),
    account_holder        TEXT NOT NULL,
    account_number_sealed BYTEA NOT NULL,
    account_last4         TEXT NOT NULL,
    ifsc                  TEXT NOT NULL CHECK (ifsc ~ '^[A-Z]{4}0[A-Z0-9]{6}$'),
    key_version           TEXT NOT NULL,
    status                TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','verified','failed')),
    active                BOOLEAN NOT NULL DEFAULT TRUE,
    verified_at           TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_payout_active ON doorstep.pro_payout_accounts (pro_id) WHERE active;

CREATE TABLE IF NOT EXISTS doorstep.pro_duty_sessions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id     UUID NOT NULL REFERENCES doorstep.professionals(id),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at   TIMESTAMPTZ,
    CHECK (ended_at IS NULL OR ended_at >= started_at)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_duty_open ON doorstep.pro_duty_sessions (pro_id) WHERE ended_at IS NULL;

-- =====================================================================
-- Customer addresses, quotes (A1), bookings (A3)
-- =====================================================================

CREATE TABLE IF NOT EXISTS doorstep.customer_addresses (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id              UUID NOT NULL,
    label                TEXT NOT NULL,
    line1                TEXT NOT NULL,
    line2                TEXT,
    landmark             TEXT,
    locality             TEXT NOT NULL,
    city_code            TEXT NOT NULL REFERENCES doorstep.cities(code),
    pincode              TEXT NOT NULL CHECK (pincode ~ '^[1-9][0-9]{5}$'),
    location             geography(Point, 4326) NOT NULL,
    zone_id              UUID REFERENCES doorstep.zones(id),
    contact_phone_sealed BYTEA,
    contact_phone_last4  TEXT,
    is_default           BOOLEAN NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at           TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_doorstep_addresses_user ON doorstep.customer_addresses (user_id) WHERE deleted_at IS NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_address_default ON doorstep.customer_addresses (user_id) WHERE is_default AND deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS doorstep.quotes (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id UUID NOT NULL,
    service_id       UUID NOT NULL REFERENCES doorstep.services(id),
    option_id        UUID NOT NULL REFERENCES doorstep.service_options(id),
    quantity         INT NOT NULL CHECK (quantity BETWEEN 1 AND 20),
    city_code        TEXT NOT NULL REFERENCES doorstep.cities(code),
    zone_id          UUID NOT NULL REFERENCES doorstep.zones(id),
    location         geography(Point, 4326) NOT NULL,
    total_paise      BIGINT NOT NULL CHECK (total_paise >= 0),
    taxable_paise    BIGINT NOT NULL CHECK (taxable_paise >= 0),
    tax_paise        BIGINT NOT NULL CHECK (tax_paise >= 0),
    tax_provisional  BOOLEAN NOT NULL,
    tax_note         TEXT NOT NULL DEFAULT '',
    duration_minutes INT NOT NULL CHECK (duration_minutes > 0),
    status           TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','consumed')),
    expires_at       TIMESTAMPTZ NOT NULL,
    consumed_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (total_paise = taxable_paise + tax_paise)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_quotes_user ON doorstep.quotes (customer_user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS doorstep.quote_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id         UUID NOT NULL REFERENCES doorstep.quotes(id) ON DELETE CASCADE,
    line_no          INT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('option','addon')),
    ref_id           UUID NOT NULL,
    price_id         UUID NOT NULL REFERENCES doorstep.city_prices(id),
    name             TEXT NOT NULL,
    quantity         INT NOT NULL CHECK (quantity >= 1),
    unit_price_paise BIGINT NOT NULL CHECK (unit_price_paise >= 0),
    line_total_paise BIGINT NOT NULL CHECK (line_total_paise >= 0),
    taxable_paise    BIGINT NOT NULL,
    tax_paise        BIGINT NOT NULL,
    tax_rate_bps     INT NOT NULL,
    gst_category     TEXT NOT NULL,
    sac              TEXT NOT NULL,
    UNIQUE (quote_id, line_no),
    CHECK (line_total_paise = unit_price_paise * quantity),
    CHECK (line_total_paise = taxable_paise + tax_paise)
);

CREATE TABLE IF NOT EXISTS doorstep.bookings (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id       UUID NOT NULL,
    idempotency_key        TEXT NOT NULL,
    quote_id               UUID REFERENCES doorstep.quotes(id),
    parent_booking_id      UUID REFERENCES doorstep.bookings(id), -- rework child
    city_code              TEXT NOT NULL REFERENCES doorstep.cities(code),
    zone_id                UUID NOT NULL REFERENCES doorstep.zones(id),
    category_id            UUID NOT NULL REFERENCES doorstep.categories(id),
    service_id             UUID NOT NULL REFERENCES doorstep.services(id),
    address_id             UUID REFERENCES doorstep.customer_addresses(id),
    address_snapshot       JSONB NOT NULL,           -- shown to the pro from acceptance to completion + 2 h
    locality               TEXT NOT NULL,            -- the only location shown before acceptance
    location               geography(Point, 4326) NOT NULL,
    slot_start             TIMESTAMPTZ NOT NULL,
    slot_end               TIMESTAMPTZ NOT NULL,
    duration_minutes       INT NOT NULL CHECK (duration_minutes > 0),
    status                 TEXT NOT NULL DEFAULT 'pending_payment' CHECK (status IN (
                               'pending_payment','confirmed','assigned','en_route','arrived','in_progress',
                               'awaiting_extras_payment','completed','cancelled','expired','customer_no_show','pro_no_show')),
    gender_rule            TEXT NOT NULL CHECK (gender_rule IN ('any','female_pros_only','male_pros_only')),
    require_female_pro     BOOLEAN NOT NULL DEFAULT FALSE,
    crew_size              INT NOT NULL DEFAULT 1 CHECK (crew_size BETWEEN 1 AND 6),
    notes                  TEXT,
    total_paise            BIGINT NOT NULL CHECK (total_paise >= 0),
    taxable_paise          BIGINT NOT NULL CHECK (taxable_paise >= 0),
    tax_paise              BIGINT NOT NULL CHECK (tax_paise >= 0),
    paid_paise             BIGINT NOT NULL DEFAULT 0 CHECK (paid_paise >= 0),
    refunded_paise         BIGINT NOT NULL DEFAULT 0 CHECK (refunded_paise >= 0),
    cancellation_fee_paise BIGINT NOT NULL DEFAULT 0 CHECK (cancellation_fee_paise >= 0),
    extras_total_paise     BIGINT NOT NULL DEFAULT 0 CHECK (extras_total_paise >= 0),
    hold_expires_at        TIMESTAMPTZ,
    reschedule_count       INT NOT NULL DEFAULT 0,
    cancelled_by_kind      TEXT CHECK (cancelled_by_kind IN ('customer','pro','admin','system')),
    cancel_reason          TEXT,
    invoice_snapshot       JSONB,                    -- recomputed at completion with the actual professional
    confirmed_at           TIMESTAMPTZ,
    assigned_at            TIMESTAMPTZ,
    en_route_at            TIMESTAMPTZ,
    arrived_at             TIMESTAMPTZ,
    started_at             TIMESTAMPTZ,
    finished_at            TIMESTAMPTZ,
    completed_at           TIMESTAMPTZ,
    cancelled_at           TIMESTAMPTZ,
    version                INT NOT NULL DEFAULT 1,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (customer_user_id, idempotency_key),
    CHECK (slot_end > slot_start),
    CHECK (refunded_paise <= paid_paise)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_customer ON doorstep.bookings (customer_user_id, slot_start DESC);
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_status_slot ON doorstep.bookings (status, slot_start);
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_hold ON doorstep.bookings (hold_expires_at) WHERE status = 'pending_payment';
CREATE INDEX IF NOT EXISTS idx_doorstep_bookings_parent ON doorstep.bookings (parent_booking_id) WHERE parent_booking_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS doorstep.booking_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id       UUID NOT NULL REFERENCES doorstep.bookings(id),
    line_no          INT NOT NULL,
    kind             TEXT NOT NULL CHECK (kind IN ('option','addon')),
    ref_id           UUID NOT NULL,
    price_id         UUID REFERENCES doorstep.city_prices(id),
    name             TEXT NOT NULL,
    quantity         INT NOT NULL CHECK (quantity >= 1),
    unit_price_paise BIGINT NOT NULL CHECK (unit_price_paise >= 0),
    line_total_paise BIGINT NOT NULL CHECK (line_total_paise >= 0),
    taxable_paise    BIGINT NOT NULL,
    tax_paise        BIGINT NOT NULL,
    tax_rate_bps     INT NOT NULL,
    gst_category     TEXT NOT NULL,
    sac              TEXT NOT NULL,
    UNIQUE (booking_id, line_no)
);

CREATE TABLE IF NOT EXISTS doorstep.booking_status_history (
    id          BIGSERIAL PRIMARY KEY,
    booking_id  UUID NOT NULL REFERENCES doorstep.bookings(id),
    from_status TEXT,
    to_status   TEXT NOT NULL,
    actor_kind  TEXT NOT NULL CHECK (actor_kind IN ('customer','pro','system','admin','payment_event')),
    actor_id    UUID,
    reason      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_history_booking ON doorstep.booking_status_history (booking_id, id);

-- Assignments are crew-ready (role lead/helper); only one lead may be
-- offered or accepted at a time per booking.
CREATE TABLE IF NOT EXISTS doorstep.booking_assignments (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id        UUID NOT NULL REFERENCES doorstep.bookings(id),
    pro_id            UUID NOT NULL REFERENCES doorstep.professionals(id),
    role              TEXT NOT NULL DEFAULT 'lead' CHECK (role IN ('lead','helper')),
    status            TEXT NOT NULL DEFAULT 'offered'
                      CHECK (status IN ('offered','accepted','declined','expired','released','completed','no_show','cancelled')),
    attempt           INT NOT NULL DEFAULT 1,
    score             NUMERIC(10,4),
    offered_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    offer_expires_at  TIMESTAMPTZ NOT NULL,
    responded_at      TIMESTAMPTZ,
    decline_reason    TEXT,
    release_cause     TEXT,
    calendar_block_id UUID,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_assignment_active_lead
    ON doorstep.booking_assignments (booking_id) WHERE role = 'lead' AND status IN ('offered','accepted');
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_assignment_active_pro
    ON doorstep.booking_assignments (booking_id, pro_id) WHERE status IN ('offered','accepted');
CREATE INDEX IF NOT EXISTS idx_doorstep_assignments_pro ON doorstep.booking_assignments (pro_id, status);
CREATE INDEX IF NOT EXISTS idx_doorstep_assignments_expiry ON doorstep.booking_assignments (offer_expires_at) WHERE status = 'offered';

-- The professional's calendar. A Postgres exclusion constraint makes double
-- booking (or a double hold) of one professional impossible.
CREATE TABLE IF NOT EXISTS doorstep.pro_calendar_blocks (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id         UUID NOT NULL REFERENCES doorstep.professionals(id),
    kind           TEXT NOT NULL CHECK (kind IN ('hold','booking','break','day_off','travel')),
    booking_id     UUID REFERENCES doorstep.bookings(id),
    during         TSTZRANGE NOT NULL CHECK (NOT isempty(during)),
    active         BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at     TIMESTAMPTZ, -- holds only
    released_at    TIMESTAMPTZ,
    release_reason TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (kind <> 'hold' OR expires_at IS NOT NULL),
    CONSTRAINT ex_doorstep_pro_calendar_no_overlap
        EXCLUDE USING gist (pro_id WITH =, during WITH &&) WHERE (active)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_calendar_booking ON doorstep.pro_calendar_blocks (booking_id) WHERE booking_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_calendar_hold_expiry ON doorstep.pro_calendar_blocks (expires_at) WHERE kind = 'hold' AND active;

-- =====================================================================
-- Visit: extras, photos, OTPs (A5)
-- =====================================================================

CREATE TABLE IF NOT EXISTS doorstep.extras_bills (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id    UUID NOT NULL REFERENCES doorstep.bookings(id),
    amount_paise  BIGINT NOT NULL CHECK (amount_paise >= 0),
    taxable_paise BIGINT NOT NULL DEFAULT 0,
    tax_paise     BIGINT NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'open'
                  CHECK (status IN ('open','payment_pending','paid','outstanding','waived','refunded')),
    due_at        TIMESTAMPTZ,
    paid_at       TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_extras_bills_booking ON doorstep.extras_bills (booking_id);

CREATE TABLE IF NOT EXISTS doorstep.booking_extras (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id        UUID NOT NULL REFERENCES doorstep.bookings(id),
    pro_id            UUID NOT NULL REFERENCES doorstep.professionals(id),
    kind              TEXT NOT NULL CHECK (kind IN ('rate_card','addon')),
    rate_card_id      UUID REFERENCES doorstep.rate_cards(id),
    addon_id          UUID REFERENCES doorstep.addons(id),
    name              TEXT NOT NULL,
    quantity          INT NOT NULL CHECK (quantity >= 1),
    unit_price_paise  BIGINT NOT NULL CHECK (unit_price_paise > 0),
    total_paise       BIGINT NOT NULL CHECK (total_paise > 0),
    status            TEXT NOT NULL DEFAULT 'proposed'
                      CHECK (status IN ('proposed','approved','declined','withdrawn','billed')),
    evidence_media_id TEXT,
    bill_id           UUID REFERENCES doorstep.extras_bills(id),
    proposed_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((kind = 'rate_card' AND rate_card_id IS NOT NULL AND addon_id IS NULL)
        OR (kind = 'addon' AND addon_id IS NOT NULL AND rate_card_id IS NULL)),
    CHECK (total_paise = unit_price_paise * quantity)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_extras_booking ON doorstep.booking_extras (booking_id, status);

CREATE TABLE IF NOT EXISTS doorstep.booking_photos (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES doorstep.bookings(id),
    pro_id     UUID NOT NULL REFERENCES doorstep.professionals(id),
    phase      TEXT NOT NULL CHECK (phase IN ('before','after','kit_seal','extra_evidence')),
    media_id   TEXT NOT NULL,
    lat        DOUBLE PRECISION,
    lng        DOUBLE PRECISION,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (booking_id, media_id)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_photos_booking ON doorstep.booking_photos (booking_id, phase);

-- Start/end OTPs: bcrypt hash for verification, sealed copy for showing the
-- customer; lockout after repeated failures.
CREATE TABLE IF NOT EXISTS doorstep.booking_otps (
    booking_id   UUID NOT NULL REFERENCES doorstep.bookings(id),
    kind         TEXT NOT NULL CHECK (kind IN ('start','end')),
    otp_hash     TEXT NOT NULL,
    otp_sealed   BYTEA,
    attempts     INT NOT NULL DEFAULT 0,
    locked_until TIMESTAMPTZ,
    verified_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (booking_id, kind)
);

-- =====================================================================
-- Money (A3/A5): payments-service application `doorstep`
-- =====================================================================

-- Signed payments-service events applied exactly once (shared/paymentevents ApplyOnce).
CREATE TABLE IF NOT EXISTS doorstep.payment_inbox (
    event_id       TEXT PRIMARY KEY,
    event_type     TEXT NOT NULL,
    reference_type TEXT NOT NULL CHECK (reference_type IN ('doorstep_booking','doorstep_extras')),
    reference_id   UUID NOT NULL,
    outcome        TEXT NOT NULL DEFAULT 'applied',
    received_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS doorstep.payments (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id          UUID NOT NULL REFERENCES doorstep.bookings(id),
    extras_bill_id      UUID REFERENCES doorstep.extras_bills(id),
    reference_type      TEXT NOT NULL CHECK (reference_type IN ('doorstep_booking','doorstep_extras')),
    reference_id        UUID NOT NULL,
    intent_key          TEXT NOT NULL UNIQUE, -- doorstep:booking:{id} / doorstep:extras:{bill}
    payments_intent_id  TEXT,
    amount_paise        BIGINT NOT NULL CHECK (amount_paise > 0),
    status              TEXT NOT NULL DEFAULT 'created'
                        CHECK (status IN ('created','pending','succeeded','failed','refunded','partially_refunded')),
    refunded_paise      BIGINT NOT NULL DEFAULT 0 CHECK (refunded_paise >= 0),
    provider_payment_id TEXT,
    checkout            JSONB,
    captured_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK ((reference_type = 'doorstep_booking' AND reference_id = booking_id AND extras_bill_id IS NULL)
        OR (reference_type = 'doorstep_extras' AND extras_bill_id IS NOT NULL AND reference_id = extras_bill_id)),
    CHECK (refunded_paise <= amount_paise)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_payments_booking ON doorstep.payments (booking_id);

CREATE TABLE IF NOT EXISTS doorstep.refunds (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_id         UUID NOT NULL REFERENCES doorstep.payments(id),
    booking_id         UUID NOT NULL REFERENCES doorstep.bookings(id),
    cause              TEXT NOT NULL,
    idempotency_key    TEXT NOT NULL UNIQUE, -- per cause: doorstep:refund:{booking}:{cause}
    amount_paise       BIGINT NOT NULL CHECK (amount_paise > 0),
    status             TEXT NOT NULL DEFAULT 'requested' CHECK (status IN ('requested','pending','succeeded','failed')),
    payments_refund_id TEXT,
    attempts           INT NOT NULL DEFAULT 0,
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error         TEXT,
    requested_by       UUID,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_refunds_retry ON doorstep.refunds (next_attempt_at) WHERE status IN ('requested','failed');

-- Unpaid extras: blocks the customer's next booking until paid.
CREATE TABLE IF NOT EXISTS doorstep.outstanding (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id UUID NOT NULL,
    booking_id       UUID NOT NULL REFERENCES doorstep.bookings(id),
    extras_bill_id   UUID NOT NULL UNIQUE REFERENCES doorstep.extras_bills(id),
    amount_paise     BIGINT NOT NULL CHECK (amount_paise > 0),
    status           TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','paid','waived')),
    settled_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_outstanding_open ON doorstep.outstanding (customer_user_id) WHERE status = 'open';

-- =====================================================================
-- After the visit, safety, chat, support (A5)
-- =====================================================================

CREATE TABLE IF NOT EXISTS doorstep.rework_requests (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id       UUID NOT NULL REFERENCES doorstep.bookings(id),
    child_booking_id UUID REFERENCES doorstep.bookings(id),
    customer_user_id UUID NOT NULL,
    reason           TEXT NOT NULL,
    media_ids        TEXT[] NOT NULL DEFAULT '{}',
    status           TEXT NOT NULL DEFAULT 'requested'
                     CHECK (status IN ('requested','approved','rejected','scheduled','completed')),
    decided_by       UUID,
    decided_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_doorstep_rework_open
    ON doorstep.rework_requests (booking_id) WHERE status IN ('requested','approved','scheduled');

CREATE TABLE IF NOT EXISTS doorstep.ratings (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id    UUID NOT NULL REFERENCES doorstep.bookings(id),
    rater_kind    TEXT NOT NULL CHECK (rater_kind IN ('customer','pro')),
    rater_user_id UUID NOT NULL,
    ratee_user_id UUID NOT NULL,
    stars         SMALLINT NOT NULL CHECK (stars BETWEEN 1 AND 5),
    tags          TEXT[] NOT NULL DEFAULT '{}',
    comment       TEXT,
    hidden        BOOLEAN NOT NULL DEFAULT FALSE,
    moderated_by  UUID,
    moderated_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (booking_id, rater_kind)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_ratings_ratee ON doorstep.ratings (ratee_user_id) WHERE NOT hidden;

CREATE TABLE IF NOT EXISTS doorstep.incidents (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id         UUID REFERENCES doorstep.bookings(id),
    pro_id             UUID REFERENCES doorstep.professionals(id),
    raised_by_kind     TEXT NOT NULL CHECK (raised_by_kind IN ('customer','pro','system','admin')),
    raised_by_user_id  UUID,
    kind               TEXT NOT NULL CHECK (kind IN ('sos','safety','damage','harassment','theft','unsafe_exit','other')),
    severity           TEXT NOT NULL DEFAULT 'high' CHECK (severity IN ('low','medium','high','critical')),
    status             TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','acknowledged','resolved')),
    description        TEXT,
    lat                DOUBLE PRECISION,
    lng                DOUBLE PRECISION,
    pro_auto_suspended BOOLEAN NOT NULL DEFAULT FALSE,
    acknowledged_by    UUID,
    acknowledged_at    TIMESTAMPTZ,
    resolved_by        UUID,
    resolved_at        TIMESTAMPTZ,
    resolution         TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_incidents_open ON doorstep.incidents (status, created_at) WHERE status <> 'resolved';

CREATE TABLE IF NOT EXISTS doorstep.share_tokens (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id         UUID NOT NULL REFERENCES doorstep.bookings(id),
    created_by_user_id UUID NOT NULL,
    token_hash         TEXT NOT NULL UNIQUE,
    expires_at         TIMESTAMPTZ NOT NULL,
    revoked_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS doorstep.trusted_contacts (
    user_id      UUID PRIMARY KEY,
    name         TEXT NOT NULL,
    phone_sealed BYTEA NOT NULL,
    phone_last4  TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS doorstep.messages (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id     UUID NOT NULL REFERENCES doorstep.bookings(id),
    sender_user_id UUID,
    sender_kind    TEXT NOT NULL CHECK (sender_kind IN ('customer','pro','system')),
    body           TEXT NOT NULL CHECK (length(body) BETWEEN 1 AND 1000),
    read_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_messages_booking ON doorstep.messages (booking_id, created_at);

CREATE TABLE IF NOT EXISTS doorstep.tickets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id  UUID REFERENCES doorstep.bookings(id),
    user_id     UUID NOT NULL,
    user_kind   TEXT NOT NULL CHECK (user_kind IN ('customer','pro')),
    category    TEXT NOT NULL DEFAULT 'other' CHECK (category IN ('payment','quality','safety','damage','professional','other')),
    subject     TEXT NOT NULL,
    body        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open','in_progress','resolved','closed')),
    assigned_to UUID,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_tickets_user ON doorstep.tickets (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_doorstep_tickets_status ON doorstep.tickets (status, created_at);

-- Earnings and settlements: computed only (payouts OFF, like Feast).
CREATE TABLE IF NOT EXISTS doorstep.settlements (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id           UUID NOT NULL REFERENCES doorstep.professionals(id),
    period_start     DATE NOT NULL,
    period_end       DATE NOT NULL,
    gross_paise      BIGINT NOT NULL DEFAULT 0,
    commission_paise BIGINT NOT NULL DEFAULT 0,
    tax_paise        BIGINT NOT NULL DEFAULT 0,
    net_paise        BIGINT NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'computed' CHECK (status IN ('computed','approved','paid')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (period_end >= period_start),
    UNIQUE (pro_id, period_start, period_end)
);

CREATE TABLE IF NOT EXISTS doorstep.earning_lines (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pro_id        UUID NOT NULL REFERENCES doorstep.professionals(id),
    booking_id    UUID REFERENCES doorstep.bookings(id),
    kind          TEXT NOT NULL CHECK (kind IN ('job','extras','incentive','penalty','adjustment','commission')),
    amount_paise  BIGINT NOT NULL, -- signed: commission and penalties are negative
    settlement_id UUID REFERENCES doorstep.settlements(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (booking_id, pro_id, kind)
);
CREATE INDEX IF NOT EXISTS idx_doorstep_earnings_pro ON doorstep.earning_lines (pro_id, created_at);

-- =====================================================================
-- Admin audit, outbox, identity role intents
-- =====================================================================

-- Every admin-internal write, in the same transaction as the write. The
-- actor is the admin-service token's signed act claim.
CREATE TABLE IF NOT EXISTS doorstep.admin_audit_log (
    id            BIGSERIAL PRIMARY KEY,
    actor_user_id UUID NOT NULL,
    permission    TEXT NOT NULL,
    action        TEXT NOT NULL,
    entity        TEXT NOT NULL,
    entity_id     TEXT NOT NULL,
    details       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_doorstep_audit_entity ON doorstep.admin_audit_log (entity, created_at DESC);

-- Schema-local outbox drained by shared/outbox (DBSchema "doorstep"): the
-- public.outbox_events table belongs to another service with another shape.
CREATE TABLE IF NOT EXISTS doorstep.outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT NOT NULL,
    partition_key TEXT NOT NULL,
    payload       JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_doorstep_outbox_unpublished
    ON doorstep.outbox_events (id) WHERE published_at IS NULL;

-- shared/identityroles outbox (role service_professional), drained by the
-- identity role worker into identity-auth's internal role API.
CREATE TABLE IF NOT EXISTS doorstep.identity_role_intents (
    id               BIGSERIAL PRIMARY KEY,
    op               TEXT        NOT NULL CHECK (op IN ('grant','revoke')),
    user_id          UUID        NOT NULL,
    role_name        TEXT        NOT NULL,
    service          TEXT        NOT NULL,
    reason           TEXT        NOT NULL DEFAULT '',
    attempts         INT         NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at     TIMESTAMPTZ,
    dead_lettered_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_doorstep_identity_role_intents_pending
    ON doorstep.identity_role_intents (next_attempt_at)
    WHERE delivered_at IS NULL AND dead_lettered_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_doorstep_identity_role_intents_dead
    ON doorstep.identity_role_intents (dead_lettered_at)
    WHERE dead_lettered_at IS NOT NULL;
