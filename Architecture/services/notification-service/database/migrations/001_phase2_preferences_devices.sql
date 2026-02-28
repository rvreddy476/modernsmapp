-- Phase 2 migration: Notification Preferences and User Devices
-- Applied by notification-service ensureNotifSchema() at startup, this file is the canonical reference.

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id          UUID PRIMARY KEY,
    email_enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    push_enabled     BOOLEAN NOT NULL DEFAULT TRUE,
    sms_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_start TIME,
    quiet_hours_end   TIME,
    muted_types      JSONB NOT NULL DEFAULT '[]',
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS user_devices (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL,
    platform   TEXT NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
    push_token TEXT NOT NULL,
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, platform, push_token)
);

CREATE INDEX IF NOT EXISTS idx_user_devices_user ON user_devices (user_id) WHERE is_active = TRUE;
