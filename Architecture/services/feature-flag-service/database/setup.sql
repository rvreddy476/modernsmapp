-- Database setup for Architecture/feature-flag-service

CREATE SCHEMA IF NOT EXISTS flags;

CREATE TABLE IF NOT EXISTS flags.flags (
    key TEXT PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    rollout_pct INT NOT NULL DEFAULT 0,
    target_user_ids TEXT[], -- UUIDs as strings
    payload JSONB,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Flag change audit trail
CREATE TABLE IF NOT EXISTS flags.flag_audit_log (
    id         BIGSERIAL PRIMARY KEY,
    flag_key   TEXT NOT NULL,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL CHECK (action IN ('created', 'updated', 'deleted')),
    old_value  JSONB,
    new_value  JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_flag_audit_key
    ON flags.flag_audit_log(flag_key, created_at DESC);

-- A/B experiment lifecycle columns
ALTER TABLE flags.flags
    ADD COLUMN IF NOT EXISTS experiment_name TEXT,
    ADD COLUMN IF NOT EXISTS hypothesis TEXT,
    ADD COLUMN IF NOT EXISTS start_date TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS end_date TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS control_group_pct INT DEFAULT 0 CHECK (control_group_pct >= 0 AND control_group_pct <= 100),
    ADD COLUMN IF NOT EXISTS treatment_group_pct INT DEFAULT 0 CHECK (treatment_group_pct >= 0 AND treatment_group_pct <= 100);

-- A/B experiment conversions
CREATE TABLE IF NOT EXISTS flags.experiment_conversions (
    id          BIGSERIAL PRIMARY KEY,
    flag_key    TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    variant     TEXT NOT NULL CHECK (variant IN ('control', 'treatment')),
    event_type  TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_conversions_flag
    ON flags.experiment_conversions(flag_key, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_conversions_user
    ON flags.experiment_conversions(user_id, flag_key);

-- =============================================================================
-- Seed: Pulse (dating module) feature flags
-- Plan: C:\workspace\atpost\dating\IMPLEMENTATION_PLAN.md §7
-- Spec: C:\workspace\atpost\dating\PULSE_DATING_SPEC.md §18 (open questions)
-- All inserts are idempotent: ON CONFLICT (key) DO NOTHING preserves any
-- runtime overrides applied via the admin API. The payload JSONB carries
-- description + owner since the flags table has no dedicated columns.
-- =============================================================================

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_enabled_master', FALSE, 0,
    '{"description":"Master switch for the entire Pulse dating module","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_orbital_default', TRUE, 100,
    '{"description":"New users land in Orbital mode by default; off => List mode","owner":"dating-team","default":"on"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_premium_enabled', FALSE, 0,
    '{"description":"Premium tier purchase + premium-only features","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_aadhaar_enabled', FALSE, 0,
    '{"description":"DigiLocker / Aadhaar verification flow visible","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_moderation_strict', FALSE, 0,
    '{"description":"AI moderation in strict mode (off = shadow mode)","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_vouching_enabled', FALSE, 0,
    '{"description":"Vouching flow visible","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_safe_meet_enabled', FALSE, 0,
    '{"description":"Safe-meet check-in scheduler","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;

INSERT INTO flags.flags (key, enabled, rollout_pct, payload)
VALUES (
    'pulse_boost_enabled', FALSE, 0,
    '{"description":"Pulse Boost button visible (premium feature)","owner":"dating-team","default":"off"}'::jsonb
)
ON CONFLICT (key) DO NOTHING;
