-- =============================================================================
-- DATING-SERVICE — Pulse domain schema
-- Bootstrap on startup via embed.go -> BootstrapSchema
-- Source of truth: C:\workspace\atpost\dating\PULSE_DATING_SPEC.md  Section 10
-- All statements are idempotent (IF NOT EXISTS / ADD COLUMN IF NOT EXISTS).
-- =============================================================================

-- gen_random_bytes (profile cohort_salt) and gen_random_uuid come from pgcrypto.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ---------------------------------------------------------------------------
-- Profiles
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_profiles (
    user_id           UUID PRIMARY KEY,
    intent            TEXT NOT NULL DEFAULT 'casual'
        CHECK (intent IN ('casual','serious','marriage')),
    bio               TEXT NOT NULL DEFAULT '',
    gender            TEXT,
    birth_date        DATE,
    city              TEXT,
    state             TEXT,
    country           TEXT,
    latitude          DOUBLE PRECISION,
    longitude         DOUBLE PRECISION,
    location_geohash  TEXT,
    height_cm         INT,
    religion          TEXT,
    community         TEXT,
    occupation        TEXT,
    education         TEXT,
    drinking          TEXT,
    smoking           TEXT,
    exercise          TEXT,
    diet              TEXT,
    wants_children    TEXT,
    family_plans      TEXT,
    blur_mode         BOOLEAN NOT NULL DEFAULT false,
    visible_to_public BOOLEAN NOT NULL DEFAULT true,
    paused            BOOLEAN NOT NULL DEFAULT false,
    language_prefs    TEXT[] NOT NULL DEFAULT '{}',
    trust_tier        TEXT   NOT NULL DEFAULT 'phone'
        CHECK (trust_tier IN ('phone','selfie','aadhaar')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at        TIMESTAMPTZ
);

-- Idempotent migrations: in case an earlier schema lacked some columns.
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS location_geohash  TEXT;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS language_prefs    TEXT[]      NOT NULL DEFAULT '{}';
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS trust_tier        TEXT        NOT NULL DEFAULT 'phone';
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS blur_mode         BOOLEAN     NOT NULL DEFAULT false;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS visible_to_public BOOLEAN     NOT NULL DEFAULT true;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS paused            BOOLEAN     NOT NULL DEFAULT false;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS deleted_at        TIMESTAMPTZ;
-- Sprint 2: echoes refresher bookkeeping + freshness signal for matching.
-- Lane D7: Echoes is an explicit opt-in, so the default is false (and is
-- re-asserted in the D7 section for databases that already have the column).
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS echoes_consent    BOOLEAN     NOT NULL DEFAULT false;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS echo_refreshed_at TIMESTAMPTZ;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS last_active_at    TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS first_name        TEXT;
-- Sprint 6: per-user salt for the soft-launch cohort gate. Stable per-user —
-- generated at profile creation, never rotated. See service/cohort.go.
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS cohort_salt       TEXT;

-- ---------------------------------------------------------------------------
-- Tunes (compatibility / vibe layer)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_tunes (
    user_id            UUID PRIMARY KEY,
    lifestyle_rhythm   SMALLINT,                          -- 1..5 Quiet..Vibrant
    conversation_style TEXT
        CHECK (conversation_style IS NULL OR conversation_style IN ('witty','deep','playful','direct','reflective')),
    faith_weight       SMALLINT,                          -- 1..5
    family_weight      SMALLINT,                          -- 1..5
    region_weight      SMALLINT,                          -- 1..5
    family_plans_axis  SMALLINT,                          -- marriage-only
    education_axis     SMALLINT,                          -- marriage-only
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Photos
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_photos (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL,
    media_id          UUID NOT NULL,
    sort_order        SMALLINT NOT NULL DEFAULT 0,
    is_primary        BOOLEAN  NOT NULL DEFAULT false,
    visibility        TEXT     NOT NULL DEFAULT 'public'
        CHECK (visibility IN ('public','match_only','sparked_only')),
    moderation_status TEXT     NOT NULL DEFAULT 'pending'
        CHECK (moderation_status IN ('pending','approved','rejected')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dating_photos_user
    ON dating_photos(user_id, sort_order);

-- ---------------------------------------------------------------------------
-- Prompts (answers to a static prompt catalog)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_prompts (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    prompt_id  INT  NOT NULL,
    answer     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, prompt_id)
);

CREATE INDEX IF NOT EXISTS idx_dating_prompts_user
    ON dating_prompts(user_id);

-- ---------------------------------------------------------------------------
-- Preferences (discovery filters)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_preferences (
    user_id              UUID PRIMARY KEY,
    min_age              INT,
    max_age              INT,
    distance_km          INT     NOT NULL DEFAULT 25,
    interested_in_gender TEXT,
    intent_filter        TEXT[]  NOT NULL DEFAULT '{}',
    blur_mode_pref       BOOLEAN NOT NULL DEFAULT false,
    language_filter      TEXT[]  NOT NULL DEFAULT '{}',
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Sparks (interest signal aimed at a specific item on a profile)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_sparks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_user_id UUID NOT NULL,
    to_user_id   UUID NOT NULL,
    target_kind  TEXT NOT NULL
        CHECK (target_kind IN ('photo','prompt','tune_axis','echo')),
    target_ref   TEXT NOT NULL,
    note         TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (from_user_id, to_user_id, target_kind, target_ref)
);

CREATE INDEX IF NOT EXISTS idx_dating_sparks_to
    ON dating_sparks(to_user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Stashes (soft-intent revisit shelf)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_stashes (
    user_id             UUID NOT NULL,
    candidate_id        UUID NOT NULL,
    stashed_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at          TIMESTAMPTZ NOT NULL,
    reactivation_signal TEXT,
    PRIMARY KEY (user_id, candidate_id)
);

CREATE INDEX IF NOT EXISTS idx_dating_stashes_user
    ON dating_stashes(user_id, stashed_at DESC);

-- ---------------------------------------------------------------------------
-- Passes
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_passes (
    user_id      UUID NOT NULL,
    candidate_id UUID NOT NULL,
    passed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    reason       TEXT,
    PRIMARY KEY (user_id, candidate_id)
);

CREATE INDEX IF NOT EXISTS idx_dating_passes_user_recent
    ON dating_passes(user_id, passed_at DESC);

-- ---------------------------------------------------------------------------
-- Blocks (mutual hide — hard filter on every candidate query)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_blocks (
    user_id      UUID NOT NULL,
    blocked_id   UUID NOT NULL,
    reason       TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, blocked_id)
);

CREATE INDEX IF NOT EXISTS idx_dating_blocks_blocked
    ON dating_blocks(blocked_id);

-- ---------------------------------------------------------------------------
-- Matches
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_matches (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_a           UUID NOT NULL,
    user_b           UUID NOT NULL,
    status           TEXT NOT NULL DEFAULT 'matched'
        CHECK (status IN ('matched','conversing','quiet','expired','closed')),
    conversation_id  UUID,
    spark_target     JSONB,
    matched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_message_at TIMESTAMPTZ,
    last_message_at  TIMESTAMPTZ,
    expires_at       TIMESTAMPTZ,
    closed_by        UUID,
    CHECK (user_a < user_b)
);

CREATE INDEX IF NOT EXISTS idx_dating_matches_user_a
    ON dating_matches(user_a, status, last_message_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_matches_user_b
    ON dating_matches(user_b, status, last_message_at DESC);

-- ---------------------------------------------------------------------------
-- Vouches (graph-derived endorsements)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_vouches (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    voucher_id   UUID NOT NULL,
    vouchee_id   UUID NOT NULL,
    relationship TEXT,
    community_id UUID,
    note         TEXT,
    status       TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','accepted','declined','revoked')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ,
    UNIQUE (voucher_id, vouchee_id)
);

CREATE INDEX IF NOT EXISTS idx_dating_vouches_vouchee
    ON dating_vouches(vouchee_id, status);

-- ---------------------------------------------------------------------------
-- Verifications (selfie + Aadhaar/DigiLocker — never store the Aadhaar number)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_verifications (
    user_id        UUID PRIMARY KEY,
    selfie_status  TEXT,                         -- pending | passed | failed
    selfie_score   DOUBLE PRECISION,
    selfie_at      TIMESTAMPTZ,
    aadhaar_status TEXT,                         -- pending | verified | failed
    aadhaar_at     TIMESTAMPTZ,
    digilocker_ref TEXT
);

-- DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
-- Aadhaar number is NEVER stored. We persist only:
--   - digilocker_ref: opaque assertion id from DigiLocker (Setu/Signzy partner)
--   - doc_type_hash: SHA-256 of the document type identifier (no PII)
--   - aadhaar_at: timestamp of successful verification
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS doc_type_hash TEXT;

-- ---------------------------------------------------------------------------
-- Safety events (panic, share-location, meet-checkin, etc.)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_safety_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    kind       TEXT NOT NULL,
    details    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);


-- ---------------------------------------------------------------------------
-- Premium subscriptions
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_premium_subscriptions (
    user_id    UUID PRIMARY KEY,
    plan       TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    source     TEXT
);

-- ---------------------------------------------------------------------------
-- Echo cache (snapshot of public AtPost activity surfaced on a profile)
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_echo_cache (
    user_id      UUID PRIMARY KEY,
    reels        JSONB NOT NULL DEFAULT '[]'::jsonb,
    qa_answers   JSONB NOT NULL DEFAULT '[]'::jsonb,
    communities  JSONB NOT NULL DEFAULT '[]'::jsonb,
    posts        JSONB NOT NULL DEFAULT '[]'::jsonb,
    refreshed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- Sprint 4 — Safety center additions
-- ---------------------------------------------------------------------------

-- Safe-meet: scheduled meetups that fire a no-show check 2.5h after the
-- start time. The check-in row is closed when the user confirms 'safe' or
-- escalates with 'help'. See spec §15 safety center.
CREATE TABLE IF NOT EXISTS dating_meets (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    with_user_id    UUID NOT NULL,
    scheduled_at    TIMESTAMPTZ NOT NULL,
    venue           TEXT,
    latitude        DOUBLE PRECISION,
    longitude       DOUBLE PRECISION,
    check_in_status TEXT,
    checked_in_at   TIMESTAMPTZ,
    no_show_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dating_meets_user
    ON dating_meets(user_id, scheduled_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_meets_pending_checkin
    ON dating_meets(scheduled_at)
    WHERE check_in_status IS NULL AND no_show_at IS NULL;

-- Reports (intake for trust-safety-service). Persisted before emit so the
-- panic/report endpoints cannot silently drop a user-safety event.
CREATE TABLE IF NOT EXISTS dating_reports (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_id UUID NOT NULL,
    target_id   UUID NOT NULL,
    category    TEXT NOT NULL,
    details     TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dating_reports_target
    ON dating_reports(target_id, created_at DESC);

-- P0-8 admin queue: status column for the /admin/dating/reports flow.
-- Idempotent ALTER so existing rows default to 'submitted'.
ALTER TABLE dating_reports
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'submitted'
        CHECK (status IN ('submitted','under_review','investigating',
                          'actioned','resolved','dismissed','closed_no_action'));
CREATE INDEX IF NOT EXISTS idx_dating_reports_status_created
    ON dating_reports(status, created_at DESC);

-- §P1-6 sweeper bookkeeping: idempotency markers so the safe-meet
-- reminder + missed-check-in sweepers don't re-fire the same event
-- every minute. NULL = not yet fired — NOW() = sent.
ALTER TABLE dating_meets
    ADD COLUMN IF NOT EXISTS reminder_fired_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS missed_check_in_fired_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_dating_meets_reminder_due
    ON dating_meets(scheduled_at)
    WHERE reminder_fired_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_dating_meets_missed_due
    ON dating_meets(scheduled_at)
    WHERE missed_check_in_fired_at IS NULL AND check_in_status IS NULL;

-- ---------------------------------------------------------------------------
-- Sprint 4 — AI moderation results (shadow + strict)
--
-- SHADOW MODE FOR v1: action_taken='shadow' regardless of confidence when
-- the pulse_moderation_strict feature flag is off. Strict mode may set
-- action_taken to 'warn'|'block'|'held'. Idempotent on (message_id, layer).
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_moderation_results (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id      UUID NOT NULL,
    conversation_id UUID NOT NULL,
    layer           SMALLINT NOT NULL,
    confidence      FLOAT NOT NULL,
    patterns        TEXT[] NOT NULL DEFAULT '{}',
    action_taken    TEXT NOT NULL DEFAULT 'shadow',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (message_id, layer)
);

CREATE INDEX IF NOT EXISTS idx_dating_moderation_results_conv
    ON dating_moderation_results(conversation_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Indexes from spec Section 10.2
-- ---------------------------------------------------------------------------

CREATE INDEX IF NOT EXISTS idx_dating_profiles_intent_geo
    ON dating_profiles(intent) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_dating_profiles_geohash
    ON dating_profiles(location_geohash) WHERE deleted_at IS NULL;
-- P0-10 Phase A: prefix index using text_pattern_ops so the candidate
-- query's LIKE 'cell%' filter is index-bounded. Without this opclass
-- the planner falls back to a sequential scan even with the column
-- indexed normally — LIKE with leading literal only uses an index
-- when text_pattern_ops (or default collation = "C") is in play.
CREATE INDEX IF NOT EXISTS idx_dating_profiles_geohash_prefix
    ON dating_profiles(location_geohash text_pattern_ops)
    WHERE deleted_at IS NULL AND location_geohash IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Sprint 5 — Premium plans, Razorpay/UPI checkout
-- ---------------------------------------------------------------------------
--
-- See PULSE_DATING_SPEC.md §14. Plans are seeded on bootstrap — entries with
-- the well-known ids ('monthly_399', 'quarterly_999', 'yearly_2499',
-- 'boost_49') are upserted by service.SeedPremiumPlans on every boot.

CREATE TABLE IF NOT EXISTS dating_premium_plans (
    id                TEXT PRIMARY KEY,
    plan_type         TEXT NOT NULL CHECK (plan_type IN ('subscription','one_time')),
    name              TEXT NOT NULL,
    price_inr_paise   BIGINT NOT NULL,
    duration_days     INT,
    description       TEXT,
    is_active         BOOLEAN NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS dating_payment_intents (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                  UUID NOT NULL,
    plan_id                  TEXT NOT NULL REFERENCES dating_premium_plans(id),
    amount_inr_paise         BIGINT NOT NULL,
    razorpay_order_id        TEXT NOT NULL UNIQUE,
    razorpay_subscription_id TEXT,
    status                   TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','attempted','paid','failed','cancelled')),
    source                   TEXT NOT NULL DEFAULT 'app',
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    paid_at                  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_dating_payment_intents_user
    ON dating_payment_intents(user_id, created_at DESC);

-- payment_events: idempotency log for Razorpay webhook deliveries. The UNIQUE
-- on razorpay_event_id is the idempotency key — webhook re-deliveries hit the
-- conflict and become a no-op.
CREATE TABLE IF NOT EXISTS dating_payment_events (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    payment_intent_id   UUID REFERENCES dating_payment_intents(id),
    razorpay_event_id   TEXT NOT NULL UNIQUE,
    event_type          TEXT NOT NULL,
    payload             JSONB NOT NULL,
    received_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at        TIMESTAMPTZ
);

ALTER TABLE dating_premium_subscriptions ADD COLUMN IF NOT EXISTS plan_id TEXT;
ALTER TABLE dating_premium_subscriptions ADD COLUMN IF NOT EXISTS razorpay_subscription_id TEXT;
ALTER TABLE dating_premium_subscriptions ADD COLUMN IF NOT EXISTS auto_renew BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE dating_premium_subscriptions ADD COLUMN IF NOT EXISTS cancelled_at TIMESTAMPTZ;

-- ---------------------------------------------------------------------------
-- Sprint 5 — DPDP data export + consent registry
-- ---------------------------------------------------------------------------
--
-- See PULSE_DATING_SPEC.md §15.8. Export job is produced by the data-exporter
-- consumer — the consent log is the audit trail required by the DPDP Act for
-- every consent toggle (Echoes, Aadhaar, AI moderation, location share).

CREATE TABLE IF NOT EXISTS dating_data_exports (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL,
    requested_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at          TIMESTAMPTZ,
    download_url          TEXT,
    download_expires_at   TIMESTAMPTZ,
    status                TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','processing','ready','failed','expired'))
);

CREATE INDEX IF NOT EXISTS idx_dating_data_exports_user
    ON dating_data_exports(user_id, requested_at DESC);

CREATE TABLE IF NOT EXISTS dating_consent_log (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    consent_type    TEXT NOT NULL,
    granted         BOOLEAN NOT NULL,
    policy_version  TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_dating_consent_log_user
    ON dating_consent_log(user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Phase 1 (§P1-1) — profile activation state machine.
--
-- New profiles begin at 'draft' and graduate through the following lifecycle:
--    draft -> pending_photo -> pending_selfie -> active
-- Existing pause / soft-delete actions also drive 'paused' / 'deleted'.
-- 'restricted' + 'suspended' are reserved for trust-safety moderation.
--
-- The legacy boolean columns (paused, deleted_at) remain authoritative for
-- back-compat with the current discovery query — profile_status is the
-- single source of truth going forward and discovery now filters on it.
-- ---------------------------------------------------------------------------
ALTER TABLE dating_profiles
    ADD COLUMN IF NOT EXISTS profile_status TEXT NOT NULL DEFAULT 'draft'
    CHECK (profile_status IN
      ('draft','pending_photo','pending_selfie','pending_review',
       'active','paused','restricted','suspended','deleted'));
CREATE INDEX IF NOT EXISTS idx_dating_profiles_status_active
    ON dating_profiles(profile_status) WHERE profile_status = 'active';

-- §P1-1: re-assert the CHECK constraint on every boot. The
-- ADD COLUMN IF NOT EXISTS above is a no-op once the column exists,
-- which means deployed databases initialised before 'pending_review'
-- was added to the IN-list still carry the older constraint. Drop +
-- re-add (named explicitly) is idempotent and cheap.
DO $$
BEGIN
    -- Postgres auto-generates the constraint name as
    -- dating_profiles_profile_status_check when the CHECK is declared
    -- inline. Drop it (IF EXISTS — safe on fresh installs and on
    -- environments that already have the named constraint) and re-add
    -- under a stable name so the next boot is a true no-op.
    ALTER TABLE dating_profiles
        DROP CONSTRAINT IF EXISTS dating_profiles_profile_status_check;
    ALTER TABLE dating_profiles
        DROP CONSTRAINT IF EXISTS dating_profiles_status_chk;
    ALTER TABLE dating_profiles
        ADD CONSTRAINT dating_profiles_status_chk
        CHECK (profile_status IN
          ('draft','pending_photo','pending_selfie','pending_review',
           'active','paused','restricted','suspended','deleted'));
EXCEPTION WHEN duplicate_object THEN
    NULL;
END $$;

-- ---------------------------------------------------------------------------
-- Lane D2 — guarded status machine + identity-sourced basics.
--
-- prior_status: the onboarding step remembered while a profile is paused,
--   held (pending_review / restricted / suspended) or deleted. Unpause and
--   admin reinstate restore exactly this step.
-- dob_source: 'identity_registration' / 'identity_profile' (identity-profile's
--   internal read), 'identity' (origin unrecorded) or the interim 'client'.
-- first_name_source: 'identity' or the interim 'client'.
-- Only store.TransitionProfileStatus writes profile_status / prior_status /
-- paused at runtime (profile_status_scan_test.go).
-- ---------------------------------------------------------------------------
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS prior_status      TEXT;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS dob_source        TEXT;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS first_name_source TEXT;
DO $$
BEGIN
    ALTER TABLE dating_profiles DROP CONSTRAINT IF EXISTS dating_profiles_prior_status_chk;
    ALTER TABLE dating_profiles ADD CONSTRAINT dating_profiles_prior_status_chk
        CHECK (prior_status IS NULL OR prior_status IN ('draft','pending_photo','pending_selfie','active'));
    ALTER TABLE dating_profiles DROP CONSTRAINT IF EXISTS dating_profiles_dob_source_chk;
    ALTER TABLE dating_profiles ADD CONSTRAINT dating_profiles_dob_source_chk
        CHECK (dob_source IS NULL OR dob_source IN
               ('identity','identity_registration','identity_profile','client'));
    ALTER TABLE dating_profiles DROP CONSTRAINT IF EXISTS dating_profiles_first_name_source_chk;
    ALTER TABLE dating_profiles ADD CONSTRAINT dating_profiles_first_name_source_chk
        CHECK (first_name_source IS NULL OR first_name_source IN ('identity','client'));
EXCEPTION WHEN duplicate_object THEN
    NULL;
END $$;

-- The onboarding step the database evidence supports. The transition writer
-- refuses an onboarding step beyond it:
--   draft          until intent, gender, interested_in, an 18+ birth date,
--                  first name and a location (coordinates or city) are set
--   pending_photo  until a primary photo is approved
--   pending_selfie until a selfie has passed
--   active         otherwise
CREATE OR REPLACE FUNCTION dating_onboarding_step(p_user_id UUID) RETURNS TEXT
LANGUAGE sql STABLE AS $fn$
    SELECT CASE
        WHEN p.user_id IS NULL THEN 'draft'
        WHEN NOT (
                 p.first_name IS NOT NULL AND btrim(p.first_name) <> ''
             AND p.birth_date IS NOT NULL
             AND p.birth_date <= (current_date - INTERVAL '18 years')::date
             AND p.gender IS NOT NULL AND btrim(p.gender) <> ''
             AND p.intent IS NOT NULL
             AND ((p.latitude IS NOT NULL AND p.longitude IS NOT NULL)
                  OR (p.city IS NOT NULL AND btrim(p.city) <> ''))
             AND EXISTS (SELECT 1 FROM dating_preferences pr
                         WHERE pr.user_id = p.user_id
                           AND pr.interested_in_gender IS NOT NULL
                           AND btrim(pr.interested_in_gender) <> ''))
            THEN 'draft'
        WHEN NOT EXISTS (SELECT 1 FROM dating_photos ph
                         WHERE ph.user_id = p.user_id AND ph.is_primary
                           AND ph.moderation_status = 'approved')
            THEN 'pending_photo'
        WHEN NOT EXISTS (SELECT 1 FROM dating_verifications v
                         WHERE v.user_id = p.user_id AND v.selfie_status = 'passed')
            THEN 'pending_selfie'
        ELSE 'active'
    END
    FROM (SELECT p_user_id AS uid) x
    LEFT JOIN dating_profiles p ON p.user_id = x.uid
$fn$;

-- Backfill, idempotent. The earlier backfill promoted draft rows straight
-- to 'active' with no photo or selfie check and forced every paused row
-- (suspended ones included) to 'paused'; both are gone.
--
-- (1) soft-deleted rows are 'deleted'.
UPDATE dating_profiles SET profile_status = 'deleted'
    WHERE deleted_at IS NOT NULL AND profile_status <> 'deleted';
-- (2) a legacy pause flag on an onboarding/active row becomes 'paused',
--     remembering the step.
UPDATE dating_profiles SET prior_status = profile_status, profile_status = 'paused'
    WHERE paused AND deleted_at IS NULL
      AND profile_status IN ('draft','pending_photo','pending_selfie','active');
-- (3) a 'paused' status always carries the flag.
UPDATE dating_profiles SET paused = true
    WHERE profile_status = 'paused' AND NOT paused;
-- (4) unsafe legacy activation: an 'active' step without an approved primary
--     photo or a passed selfie goes back to the step its evidence supports.
UPDATE dating_profiles p SET profile_status = dating_onboarding_step(p.user_id)
    WHERE p.profile_status = 'active'
      AND (NOT EXISTS (SELECT 1 FROM dating_photos ph WHERE ph.user_id = p.user_id
                         AND ph.is_primary AND ph.moderation_status = 'approved')
           OR NOT EXISTS (SELECT 1 FROM dating_verifications v WHERE v.user_id = p.user_id
                         AND v.selfie_status = 'passed'));
UPDATE dating_profiles p SET prior_status = dating_onboarding_step(p.user_id)
    WHERE p.prior_status = 'active'
      AND (NOT EXISTS (SELECT 1 FROM dating_photos ph WHERE ph.user_id = p.user_id
                         AND ph.is_primary AND ph.moderation_status = 'approved')
           OR NOT EXISTS (SELECT 1 FROM dating_verifications v WHERE v.user_id = p.user_id
                         AND v.selfie_status = 'passed'));
-- (5) paused/held rows with no remembered step get one: 'active' when an
--     approved primary photo and a passed selfie exist, else the evidence step.
UPDATE dating_profiles p SET prior_status = CASE
        WHEN EXISTS (SELECT 1 FROM dating_photos ph WHERE ph.user_id = p.user_id
                       AND ph.is_primary AND ph.moderation_status = 'approved')
         AND EXISTS (SELECT 1 FROM dating_verifications v WHERE v.user_id = p.user_id
                       AND v.selfie_status = 'passed')
        THEN 'active'
        ELSE dating_onboarding_step(p.user_id)
    END
    WHERE p.prior_status IS NULL
      AND p.profile_status IN ('paused','pending_review','restricted','suspended');
-- (6) a birth date already on file from before dob_source existed was
--     client-supplied: record that, which also locks it.
UPDATE dating_profiles SET dob_source = 'client'
    WHERE birth_date IS NOT NULL AND dob_source IS NULL;

-- ---------------------------------------------------------------------------
-- §P0-8 — dating_admin_audit (append-only log of every admin action).
--
-- Every report transition + photo moderation flip taken from the
-- /admin/dating console writes one row here. The trigger below makes
-- the table append-only so the trust-safety + compliance team has a
-- tamper-proof trail of who-did-what. PHASE_0_TEST_PLANS.md §P0-8
-- acceptance test D verifies UPDATE/DELETE are rejected.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dating_admin_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_admin_id  UUID NOT NULL,
    action          TEXT NOT NULL,
    target_user_id  UUID,
    target_resource TEXT,
    reason          TEXT,
    policy_code     TEXT,
    internal_notes  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dating_admin_audit_actor
    ON dating_admin_audit(actor_admin_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_admin_audit_target_user
    ON dating_admin_audit(target_user_id, created_at DESC)
    WHERE target_user_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_dating_admin_audit_created
    ON dating_admin_audit(created_at DESC);

-- Immutability: refuse UPDATE/DELETE. Append-only by design.
CREATE OR REPLACE FUNCTION dating_admin_audit_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'dating_admin_audit is append-only';
END $$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS dating_admin_audit_no_update ON dating_admin_audit;
CREATE TRIGGER dating_admin_audit_no_update
    BEFORE UPDATE OR DELETE ON dating_admin_audit
    FOR EACH ROW EXECUTE FUNCTION dating_admin_audit_immutable();

-- ---------------------------------------------------------------------------
-- §P0-7 Phase A — Fake-account risk scoring.
--
-- Aggregates seven signals (verification tier, profile completeness, photo
-- approval, IP/ASN velocity, report count + quality, block rate, spark
-- velocity) into a 0..100 score that maps to one of seven enforcement
-- levels. Phase A defers device-reuse (15w) — the signal is surfaced as
-- null in the signals JSON with a TODO marker so Phase B can wire it
-- without a schema change. The IP/ASN velocity signal is currently a
-- placeholder (no request-log aggregation hook yet) and contributes 0.
-- ---------------------------------------------------------------------------

CREATE TABLE IF NOT EXISTS dating_account_risk (
    user_id           UUID PRIMARY KEY,
    risk_score        INT  NOT NULL CHECK (risk_score BETWEEN 0 AND 100),
    risk_level        TEXT NOT NULL DEFAULT 'allow'
        CHECK (risk_level IN
            ('allow','reduce_reach','require_recheck',
             'hide_from_discovery','chat_hold','admin_review','suspend')),
    signals           JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_evaluated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_dating_account_risk_level
    ON dating_account_risk(risk_level) WHERE risk_level != 'allow';
CREATE INDEX IF NOT EXISTS idx_dating_account_risk_evaluated_at
    ON dating_account_risk(last_evaluated_at);

-- ---------------------------------------------------------------------------
-- §P1-3 — Privacy controls.
--
-- Five user-facing privacy levers, all stored on the profile row so a
-- single read covers the discovery query's hard-filter needs AND the
-- response builder's masking logic. Defaults are conservative-OFF so
-- existing profiles keep their current visibility.
--
--   * incognito                — viewer doesn't appear in anyone else's
--                                deck unless they've already sparked.
--   * hide_last_active         — no last-active bucket on pulse cards
--                                (lane D7: on for new profiles).
--   * approximate_location     — lane D7: no longer read; every distance
--                                is a bucket computed on snapped points.
--   * verified_only_filter     — viewer-side toggle — FetchCandidates
--                                excludes trust_tier 'phone' (must be
--                                'selfie' or 'aadhaar').
--   * blur_photos_until_match  — owner's photos return a blurred URL
--                                for non-matched viewers. Matched
--                                viewers see the original.
--
-- Lane D6: the blurred image is rendered and served by media-service
-- (GET /v1/dating/photos/:id/blurred); the client is never trusted to
-- blur. blurred_url below is legacy and unread.
-- ---------------------------------------------------------------------------
ALTER TABLE dating_profiles
    ADD COLUMN IF NOT EXISTS incognito               BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS hide_last_active        BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS approximate_location    BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS verified_only_filter    BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS blur_photos_until_match BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE dating_photos
    ADD COLUMN IF NOT EXISTS blurred_url TEXT;

-- ---------------------------------------------------------------------------
-- §P1-2 — Transparency controls.
--
-- Surface the moderation reason set by the scanner/admin so the photo
-- owner can see "Why was my photo rejected?". The column is set by
-- SetPhotoModerationStatus and read by the owner-only photo list
-- endpoint (`GET /v1/dating/photos/me`). NULL means "no reason yet" —
-- legacy rows + pending photos.
--
-- Idempotent — pre-existing deploys will pick this up on next bootstrap.
-- ---------------------------------------------------------------------------
ALTER TABLE dating_photos
    ADD COLUMN IF NOT EXISTS moderation_reason TEXT;

-- ---------------------------------------------------------------------------
-- Dating plan lane D6 — dating photo safety.
--
-- moderation_status gains 'pending_review': automated moderation found a
-- borderline label, the image was never scanned, or a primary photo shows
-- no face; a moderator decides. moderation_source records who set the
-- status ('auto' | 'admin'; NULL on rows from before D6). moderation_labels
-- keeps the scanner labels the automated decision used. media_checked_at is
-- when media-service last confirmed the asset (the recheck sweeper's cursor).
-- face_count is set when media-service's provider counted faces.
--
-- blurred_url is no longer read: the blurred image is a media-service
-- rendition served through GET /v1/dating/photos/:id/blurred.
-- ---------------------------------------------------------------------------
ALTER TABLE dating_photos
    ADD COLUMN IF NOT EXISTS moderation_source TEXT,
    ADD COLUMN IF NOT EXISTS moderation_labels JSONB,
    ADD COLUMN IF NOT EXISTS media_checked_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS face_count        INT;

DO $d6$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'dating_photos'::regclass
          AND conname = 'dating_photos_moderation_status_check'
          AND pg_get_constraintdef(oid) LIKE '%pending_review%'
    ) THEN
        BEGIN
            ALTER TABLE dating_photos DROP CONSTRAINT IF EXISTS dating_photos_moderation_status_check;
            ALTER TABLE dating_photos ADD CONSTRAINT dating_photos_moderation_status_check
                CHECK (moderation_status IN ('pending','pending_review','approved','rejected'));
        EXCEPTION WHEN duplicate_object THEN
            NULL; -- another replica's boot added it first
        END;
    END IF;
END
$d6$;

CREATE INDEX IF NOT EXISTS idx_dating_photos_media_recheck
    ON dating_photos (media_checked_at NULLS FIRST)
    WHERE moderation_status IN ('pending','pending_review','approved');

CREATE INDEX IF NOT EXISTS idx_dating_photos_user_media
    ON dating_photos (user_id, media_id);

-- ---------------------------------------------------------------------------
-- Phase 1 notification follow-ups — idempotency markers for the four
-- previously-uncalled publishers (PublishMatchQuietNotify,
-- PublishSafetyPanicAcknowledged, PublishReportStatusUpdated,
-- PublishPremiumPaymentFailure).
--
-- quiet_notified_at gates dating.match.quiet_notify so the sweeper
-- emits at most once per match.
--
-- acknowledged_at + acknowledged_by gate
-- dating.safety.panic.acknowledged so each panic row can be ack'd at
-- most once.
-- ---------------------------------------------------------------------------
ALTER TABLE dating_matches
    ADD COLUMN IF NOT EXISTS quiet_notified_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_dating_matches_quiet_unnotified
    ON dating_matches(status)
    WHERE quiet_notified_at IS NULL AND status = 'quiet';

ALTER TABLE dating_safety_events
    ADD COLUMN IF NOT EXISTS acknowledged_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS acknowledged_by UUID;

CREATE INDEX IF NOT EXISTS idx_dating_safety_panic_open
    ON dating_safety_events(created_at DESC)
    WHERE kind = 'panic' AND acknowledged_at IS NULL;

-- ---------------------------------------------------------------------------
-- §P0-7 Phase B — device-fingerprint + IP/ASN velocity signals.
--
-- A row is upserted on every pulse/spark request that carries an
-- X-Device-Fingerprint header. CountUsersByFingerprint feeds the
-- device-reuse signal (>3 distinct users on a fingerprint = 1.0) —
-- COUNT(DISTINCT user_id) WHERE ip = $1 AND last_seen_at > NOW() -
-- INTERVAL '1 hour' feeds IP/ASN velocity (>5 distinct users / hour =
-- 1.0). The two signals were 0-weighted scaffolds in Phase A.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dating_device_fingerprints (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    fingerprint     TEXT NOT NULL,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip              TEXT,
    UNIQUE(user_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_dating_device_fp_fp
    ON dating_device_fingerprints(fingerprint);
CREATE INDEX IF NOT EXISTS idx_dating_device_fp_ip_recent
    ON dating_device_fingerprints(ip, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_device_fp_user
    ON dating_device_fingerprints(user_id, last_seen_at DESC);

-- ---------------------------------------------------------------------------
-- Lane D3 — blocks and matching integrity.
--
-- declined_at: the recipient declined the spark. Hidden from their incoming
--   list, never told to the sender (the column is not serialised), and not
--   counted as interest by the mutual check.
-- closed_at: when a match was closed (unmatch, block) or expired. The mutual
--   check only counts sparks created after the pair's last closure, so one
--   stale spark can never re-match a pair.
-- dating_spark_ledger: one row per new spark, never deleted by revoke,
--   unmatch or block, so the 24h spark limit cannot be reset by revoking.
-- uq_dating_matches_open_pair: at most one open match per pair. This file
--   never closes duplicates: it creates the index only when none exist, so a
--   duplicate can never fail boot here. The service's boot step
--   (service.ReconcileOpenMatchDuplicates) counts them, closes the extras
--   through the match close path (close_reason 'duplicate', one
--   dating.match.closed each; outside local/dev only with
--   DATING_DEDUPE_OPEN_MATCHES=true) and then ensures the index with the
--   same DDL (store.OpenPairIndexDDL). scripts/count-duplicate-matches.sql
--   counts them read-only.
-- close_reason: why a match closed — 'unmatch', 'block' or 'duplicate'. NULL
--   on rows closed before the column existed.
-- idx_dating_sparks_declined_pair: the decline cooldown lookups (spark
--   create, the deck, the mutual check); partial, so only declined rows.
-- ---------------------------------------------------------------------------
ALTER TABLE dating_sparks  ADD COLUMN IF NOT EXISTS declined_at  TIMESTAMPTZ;
ALTER TABLE dating_matches ADD COLUMN IF NOT EXISTS closed_at    TIMESTAMPTZ;
ALTER TABLE dating_matches ADD COLUMN IF NOT EXISTS close_reason TEXT;

CREATE TABLE IF NOT EXISTS dating_spark_ledger (
    from_user_id UUID        NOT NULL,
    sent_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_dating_spark_ledger_sender
    ON dating_spark_ledger(from_user_id, sent_at DESC);

CREATE INDEX IF NOT EXISTS idx_dating_sparks_declined_pair
    ON dating_sparks(from_user_id, to_user_id, declined_at)
    WHERE declined_at IS NOT NULL;

DO $$
BEGIN
    IF to_regclass('uq_dating_matches_open_pair') IS NULL AND NOT EXISTS (
        SELECT 1 FROM dating_matches
        WHERE status IN ('matched','conversing','quiet')
        GROUP BY user_a, user_b
        HAVING COUNT(*) > 1
    ) THEN
        CREATE UNIQUE INDEX IF NOT EXISTS uq_dating_matches_open_pair
            ON dating_matches(user_a, user_b)
            WHERE status IN ('matched','conversing','quiet');
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- Lane D5 — server-side selfie verification (blink liveness).
--
-- The client asks for a challenge (instruction "blink_twice", at most
-- max_duration_ms), records a short video, uploads it through media-service
-- and submits its media id with the challenge. dating asks media-service
-- (internal route, no user identity) to find two blinks by one consistent
-- face and compare that face with the approved primary photo. No face
-- embedding is stored or accepted anywhere.
--
-- dating_verifications.selfie_status: pending | pending_review | passed | failed
--   selfie_score    rounded 0-100 similarity (older rows: a 0-1 cosine)
--   selfie_provider provider that produced it (rekognition | mock)
--   selfie_media_id the blink video that was checked
--   selfie_blinks   blinks media-service counted in it
--   selfie_review_* why it went to a moderator, who decided, when
-- dating_selfie_challenges: instruction + max duration, 10-minute expiry,
--   used once.
-- dating_selfie_attempts: one row per consumed challenge. The attempt limit
--   counts these whatever their outcome ('error' = no verdict).
-- ---------------------------------------------------------------------------
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS selfie_provider      TEXT;
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS selfie_media_id      UUID;
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS selfie_blinks        INT;
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS selfie_review_reason TEXT;
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS selfie_reviewed_by   UUID;
ALTER TABLE dating_verifications ADD COLUMN IF NOT EXISTS selfie_reviewed_at   TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_dating_verifications_selfie_review
    ON dating_verifications(selfie_at)
    WHERE selfie_status = 'pending_review';

CREATE TABLE IF NOT EXISTS dating_selfie_challenges (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL,
    instruction     TEXT        NOT NULL,
    max_duration_ms INT         NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_dating_selfie_challenges_user
    ON dating_selfie_challenges(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS dating_selfie_attempts (
    id              UUID             PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID             NOT NULL,
    challenge_id    UUID             NOT NULL,
    video_media_id  UUID             NOT NULL,
    instruction     TEXT             NOT NULL,
    outcome         TEXT             NOT NULL DEFAULT 'submitted'
        CHECK (outcome IN ('submitted','passed','failed','pending_review','error')),
    similarity      DOUBLE PRECISION,
    blinks_detected INT,
    frames_analysed INT,
    provider        TEXT,
    reason          TEXT,
    review_decision TEXT CHECK (review_decision IN ('approved','rejected')),
    reviewed_by     UUID,
    reviewed_at     TIMESTAMPTZ,
    created_at      TIMESTAMPTZ      NOT NULL DEFAULT now(),
    decided_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_dating_selfie_attempts_user
    ON dating_selfie_attempts(user_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Lane D7 — location precision and privacy.
--
-- A location is stored snapped to a 0.01 degree grid (~1.1 km), rounding half
-- up, and location_geohash (7 characters) is derived from the snapped point.
-- dating_snap_coordinate / dating_geohash_encode are the same arithmetic as
-- store.SnapCoordinate / store.EncodeGeohash (pinned equal by
-- internal/store/location_it_test.go). The UPDATEs snap rows written before
-- D7 in place; they touch only rows not yet snapped or whose geohash
-- disagrees, so a second boot changes nothing.
--
-- dating_location_changes: one row per accepted location change, the first
--   set included. The profile write refuses a change within the minimum
--   interval or past the daily count (DATING_LOCATION_*). Rows older than 24h
--   are trimmed on write.
-- dating_explain_ledger: one row per explain request, for its daily limit
--   (DATING_EXPLAIN_DAILY_LIMIT).
--
-- Defaults for NEW profiles: hide_last_active on; echoes_consent off (an
-- explicit opt-in); approximate_location on (always on now, and no longer
-- read). Existing rows keep their stored choice.
-- ---------------------------------------------------------------------------
CREATE OR REPLACE FUNCTION dating_snap_coordinate(v DOUBLE PRECISION) RETURNS DOUBLE PRECISION
LANGUAGE sql IMMUTABLE AS $fn$
    SELECT floor(v * 100 + 0.5) / 100
$fn$;

CREATE OR REPLACE FUNCTION dating_geohash_encode(p_lat DOUBLE PRECISION, p_lon DOUBLE PRECISION, p_len INT)
RETURNS TEXT LANGUAGE plpgsql IMMUTABLE AS $fn$
DECLARE
    alphabet CONSTANT TEXT := '0123456789bcdefghjkmnpqrstuvwxyz';
    lat_lo DOUBLE PRECISION := -90;
    lat_hi DOUBLE PRECISION := 90;
    lon_lo DOUBLE PRECISION := -180;
    lon_hi DOUBLE PRECISION := 180;
    mid    DOUBLE PRECISION;
    bits   INT := 0;
    nbits  INT := 0;
    even   BOOLEAN := true;
    gh     TEXT := '';
BEGIN
    IF p_lat IS NULL OR p_lon IS NULL OR p_len IS NULL OR p_len <= 0
       OR p_lat < -90 OR p_lat > 90 OR p_lon < -180 OR p_lon > 180 THEN
        RETURN NULL;
    END IF;
    WHILE length(gh) < p_len LOOP
        IF even THEN
            mid := (lon_lo + lon_hi) / 2;
            IF p_lon >= mid THEN bits := bits * 2 + 1; lon_lo := mid;
            ELSE bits := bits * 2; lon_hi := mid;
            END IF;
        ELSE
            mid := (lat_lo + lat_hi) / 2;
            IF p_lat >= mid THEN bits := bits * 2 + 1; lat_lo := mid;
            ELSE bits := bits * 2; lat_hi := mid;
            END IF;
        END IF;
        even := NOT even;
        nbits := nbits + 1;
        IF nbits = 5 THEN
            gh := gh || substr(alphabet, bits + 1, 1);
            bits := 0;
            nbits := 0;
        END IF;
    END LOOP;
    RETURN gh;
END
$fn$;

UPDATE dating_profiles
SET latitude         = dating_snap_coordinate(latitude),
    longitude        = dating_snap_coordinate(longitude),
    location_geohash = dating_geohash_encode(dating_snap_coordinate(latitude), dating_snap_coordinate(longitude), 7)
WHERE latitude IS NOT NULL AND longitude IS NOT NULL
  AND (latitude  <> dating_snap_coordinate(latitude)
    OR longitude <> dating_snap_coordinate(longitude)
    OR location_geohash IS DISTINCT FROM
       dating_geohash_encode(dating_snap_coordinate(latitude), dating_snap_coordinate(longitude), 7));
UPDATE dating_profiles SET location_geohash = NULL
WHERE (latitude IS NULL OR longitude IS NULL) AND location_geohash IS NOT NULL;

CREATE TABLE IF NOT EXISTS dating_location_changes (
    user_id    UUID        NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_dating_location_changes_user
    ON dating_location_changes(user_id, changed_at DESC);

CREATE TABLE IF NOT EXISTS dating_explain_ledger (
    viewer_id    UUID        NOT NULL,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_dating_explain_ledger_viewer
    ON dating_explain_ledger(viewer_id, requested_at DESC);

-- Column defaults, altered only when they differ (an ALTER takes an exclusive
-- lock, so a no-op boot should not).
DO $d7$
DECLARE
    want RECORD;
BEGIN
    FOR want IN
        SELECT * FROM (VALUES ('hide_last_active', 'true'),
                              ('echoes_consent', 'false'),
                              ('approximate_location', 'true')) AS w(col, def)
    LOOP
        IF (SELECT pg_get_expr(d.adbin, d.adrelid)
            FROM pg_attribute a
            LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
            WHERE a.attrelid = 'dating_profiles'::regclass AND a.attname = want.col)
           IS DISTINCT FROM want.def THEN
            EXECUTE format('ALTER TABLE dating_profiles ALTER COLUMN %I SET DEFAULT %s', want.col, want.def);
        END IF;
    END LOOP;
END
$d7$;


-- ---------------------------------------------------------------------------
-- Lane D8 — safety and moderation.
--
-- dating_panic_incidents: one row per incident. A panic or a meet check-in
--   "help" inside the dedupe window of the user's last unresolved incident
--   updates that incident (trigger_count, last_triggered_at, newest point).
--   The full-precision location lives ONLY here: it is served by the audited
--   admin detail route and, when the user opted in, to their trusted
--   contacts through the service-only notify context. suspected_abuse marks
--   an incident over the daily limit: recorded, never paged. After an account
--   purge user_id holds the stable anonymised subject token and retain_until
--   bounds how long the incident is kept.
-- dating_trusted_contacts: at most three per user; each was an accepted
--   connection or a current match when added.
-- dating_location_shares: a live share to one recipient (trusted contact or
--   current match), exact point, hard expiry. Stopping clears the point; the
--   retention sweeper clears expired points and deletes old rows.
-- dating_reports: fixed reason codes on new rows (NOT VALID keeps legacy
--   rows), evidence references, the auto-block marker, the trust-safety
--   grievance link with its retry bookkeeping, and retain_until once a party
--   is purged (a purged reporter is replaced by their subject token).
-- dating_retained_risk_signals: HMAC-hashed account risk and device signals
--   kept after a purge so ban evasion is still detected; deleted after
--   retain_until.
-- dating_matches.anonymised_at: the purged participant was replaced by their
--   subject token.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS dating_panic_incidents (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id            UUID        NOT NULL,
    source             TEXT        NOT NULL CHECK (source IN ('panic','meet_checkin','legacy')),
    meet_id            UUID,
    latitude           DOUBLE PRECISION,
    longitude          DOUBLE PRECISION,
    context            JSONB       NOT NULL DEFAULT '{}'::jsonb,
    trigger_count      INT         NOT NULL DEFAULT 1,
    first_triggered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_triggered_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    status             TEXT        NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','acknowledged','resolved')),
    suspected_abuse    BOOLEAN     NOT NULL DEFAULT false,
    acknowledged_at    TIMESTAMPTZ,
    acknowledged_by    UUID,
    resolved_at        TIMESTAMPTZ,
    resolved_by        UUID,
    resolution_note    TEXT,
    legacy_event_id    UUID UNIQUE,
    anonymised_at      TIMESTAMPTZ,
    retain_until       TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_dating_panic_incidents_user
    ON dating_panic_incidents(user_id, last_triggered_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_panic_incidents_status
    ON dating_panic_incidents(status, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_panic_incidents_retain
    ON dating_panic_incidents(retain_until) WHERE retain_until IS NOT NULL;

-- Legacy panic rows in dating_safety_events carried the coordinates in
-- details. Copy each into an incident once, then strip the coordinates.
INSERT INTO dating_panic_incidents (user_id, source, latitude, longitude, context,
    first_triggered_at, last_triggered_at, created_at, status,
    acknowledged_at, acknowledged_by, legacy_event_id)
SELECT e.user_id, 'legacy',
       CASE WHEN jsonb_typeof(e.details->'latitude') = 'number'
             AND jsonb_typeof(e.details->'longitude') = 'number'
            THEN (e.details->>'latitude')::double precision END,
       CASE WHEN jsonb_typeof(e.details->'latitude') = 'number'
             AND jsonb_typeof(e.details->'longitude') = 'number'
            THEN (e.details->>'longitude')::double precision END,
       COALESCE(CASE WHEN jsonb_typeof(e.details) = 'object'
                     THEN e.details - 'latitude' - 'longitude' END, '{}'::jsonb),
       e.created_at, e.created_at, e.created_at,
       CASE WHEN e.acknowledged_at IS NULL THEN 'open' ELSE 'acknowledged' END,
       e.acknowledged_at, e.acknowledged_by, e.id
FROM dating_safety_events e
WHERE e.kind = 'panic'
ON CONFLICT (legacy_event_id) DO NOTHING;
UPDATE dating_safety_events
SET details = details - 'latitude' - 'longitude'
WHERE kind = 'panic' AND jsonb_typeof(details) = 'object'
  AND (details ? 'latitude' OR details ? 'longitude');

CREATE TABLE IF NOT EXISTS dating_trusted_contacts (
    user_id                 UUID        NOT NULL,
    contact_id              UUID        NOT NULL,
    share_location_on_panic BOOLEAN     NOT NULL DEFAULT false,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, contact_id),
    CHECK (user_id <> contact_id)
);
CREATE INDEX IF NOT EXISTS idx_dating_trusted_contacts_contact
    ON dating_trusted_contacts(contact_id);

CREATE TABLE IF NOT EXISTS dating_location_shares (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID        NOT NULL,
    recipient_id   UUID        NOT NULL,
    recipient_kind TEXT        NOT NULL CHECK (recipient_kind IN ('trusted_contact','match')),
    latitude       DOUBLE PRECISION,
    longitude      DOUBLE PRECISION,
    expires_at     TIMESTAMPTZ NOT NULL,
    stopped_at     TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (user_id <> recipient_id)
);
CREATE INDEX IF NOT EXISTS idx_dating_location_shares_recipient
    ON dating_location_shares(recipient_id, expires_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_location_shares_user
    ON dating_location_shares(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_location_shares_expires
    ON dating_location_shares(expires_at);

ALTER TABLE dating_reports
    ADD COLUMN IF NOT EXISTS evidence                  JSONB   NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS auto_blocked              BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS grievance_id              UUID,
    ADD COLUMN IF NOT EXISTS grievance_attempts        INT     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS grievance_next_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS grievance_last_error      TEXT,
    ADD COLUMN IF NOT EXISTS reporter_anonymised_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS retain_until              TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_dating_reports_reporter_created
    ON dating_reports(reporter_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_dating_reports_grievance_pending
    ON dating_reports(grievance_next_attempt_at)
    WHERE grievance_id IS NULL AND grievance_next_attempt_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_dating_reports_retain
    ON dating_reports(retain_until) WHERE retain_until IS NOT NULL;
DO $d8$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'dating_reports_reason_chk'
                     AND conrelid = 'dating_reports'::regclass) THEN
        ALTER TABLE dating_reports ADD CONSTRAINT dating_reports_reason_chk
            CHECK (category IN ('harassment','fake_profile','underage','nudity',
                                'scam','hate','violence','spam','other')) NOT VALID;
    END IF;
END
$d8$;

CREATE TABLE IF NOT EXISTS dating_retained_risk_signals (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    subject_token   UUID        NOT NULL,
    kind            TEXT        NOT NULL CHECK (kind IN ('account','device_fingerprint','ip')),
    value_hash      TEXT        NOT NULL,
    risk_score      INT,
    risk_level      TEXT,
    profile_status  TEXT,
    reports_against INT,
    first_seen_at   TIMESTAMPTZ,
    last_seen_at    TIMESTAMPTZ,
    retained_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    retain_until    TIMESTAMPTZ NOT NULL,
    UNIQUE (subject_token, kind, value_hash)
);
CREATE INDEX IF NOT EXISTS idx_dating_retained_risk_signals_hash
    ON dating_retained_risk_signals(kind, value_hash);
CREATE INDEX IF NOT EXISTS idx_dating_retained_risk_signals_retain
    ON dating_retained_risk_signals(retain_until);

ALTER TABLE dating_matches ADD COLUMN IF NOT EXISTS anonymised_at TIMESTAMPTZ;
-- dating_panic_incidents.page_required / paged_at: a page that never reached
-- Kafka is re-published by the sweeper (service.RepublishUnpagedPanics).
ALTER TABLE dating_panic_incidents
    ADD COLUMN IF NOT EXISTS page_required BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS paged_at      TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_dating_panic_incidents_unpaged
    ON dating_panic_incidents(created_at) WHERE page_required AND paged_at IS NULL;
