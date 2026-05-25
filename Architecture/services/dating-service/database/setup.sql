-- =============================================================================
-- DATING-SERVICE — Pulse domain schema
-- Bootstrap on startup via embed.go -> BootstrapSchema
-- Source of truth: C:\workspace\atpost\dating\PULSE_DATING_SPEC.md  Section 10
-- All statements are idempotent (IF NOT EXISTS / ADD COLUMN IF NOT EXISTS).
-- =============================================================================

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
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS echoes_consent    BOOLEAN     NOT NULL DEFAULT true;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS echo_refreshed_at TIMESTAMPTZ;
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS last_active_at    TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE dating_profiles ADD COLUMN IF NOT EXISTS first_name        TEXT;
-- Sprint 6: per-user salt for the soft-launch cohort gate. Stable per-user;
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
-- every minute. NULL = not yet fired; NOW() = sent.
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
-- See PULSE_DATING_SPEC.md §14. Plans are seeded on bootstrap; entries with
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
-- on razorpay_event_id is the idempotency key; webhook re-deliveries hit the
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
-- consumer; the consent log is the audit trail required by the DPDP Act for
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
-- back-compat with the current discovery query; profile_status is the
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

-- Backfill: pre-existing rows go to 'active' so they don't disappear from
-- discovery on first deploy. The service layer will downshift any row
-- that fails the new minimum-fields gate the next time it's edited.
UPDATE dating_profiles SET profile_status = 'active'
    WHERE profile_status = 'draft' AND deleted_at IS NULL AND paused = false
      AND first_name IS NOT NULL AND birth_date IS NOT NULL;
UPDATE dating_profiles SET profile_status = 'paused' WHERE paused = true AND deleted_at IS NULL;
UPDATE dating_profiles SET profile_status = 'deleted' WHERE deleted_at IS NOT NULL;

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
--   * hide_last_active         — last_active_at omitted from pulse +
--                                match-list responses.
--   * approximate_location     — distance bucketed to coarse ranges
--                                instead of an exact km value.
--   * verified_only_filter     — viewer-side toggle; FetchCandidates
--                                excludes trust_tier 'phone' (must be
--                                'selfie' or 'aadhaar').
--   * blur_photos_until_match  — owner's photos return a blurred URL
--                                for non-matched viewers. Matched
--                                viewers see the original.
--
-- The blurred URL is uploaded alongside the original by the media
-- pipeline. Phase A leaves blurred_url NULL when not yet generated;
-- the response builder falls back to "<url>?blurred=1" the client
-- honours so blur still takes effect end-to-end.
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
-- device-reuse signal (>3 distinct users on a fingerprint = 1.0);
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

