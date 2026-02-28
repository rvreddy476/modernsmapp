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
