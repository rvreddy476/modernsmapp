-- Migration 003: Add is_active to sessions + create trusted_devices table
-- Run against: identity_db

\connect identity_db;

-- Add is_active flag to sessions (supplements revoked_at for quick filtering)
ALTER TABLE auth.sessions
    ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;

-- Index for active sessions lookup
CREATE INDEX IF NOT EXISTS idx_sessions_user_active
    ON auth.sessions(user_id, is_active)
    WHERE is_active = TRUE;

-- Trusted devices table — remember devices for 2FA skip
CREATE TABLE IF NOT EXISTS auth.trusted_devices (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID        NOT NULL REFERENCES auth.users(user_id) ON DELETE CASCADE,
    device_fingerprint  VARCHAR(255) NOT NULL,
    device_name         VARCHAR(100),
    last_used_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trusted_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trusted_devices_user
    ON auth.trusted_devices(user_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_trusted_devices_user_fingerprint
    ON auth.trusted_devices(user_id, device_fingerprint);
