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
