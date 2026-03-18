-- Migration 003: Enhanced notification preferences with granular push controls
-- Drop and recreate the notification_preferences table with expanded columns.
-- The old table (from ensureNotifSchema) had user_id UUID; this uses TEXT to match
-- the X-User-Id header format used across the platform.

CREATE TABLE IF NOT EXISTS notification_preferences (
    user_id             TEXT PRIMARY KEY,
    push_enabled        BOOLEAN NOT NULL DEFAULT TRUE,
    email_enabled       BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    quiet_hours_start   TIME,
    quiet_hours_end     TIME,
    quiet_hours_tz      VARCHAR(50),
    push_likes          BOOLEAN NOT NULL DEFAULT FALSE,
    push_super_likes    BOOLEAN NOT NULL DEFAULT TRUE,
    push_comments       BOOLEAN NOT NULL DEFAULT TRUE,
    push_replies        BOOLEAN NOT NULL DEFAULT TRUE,
    push_mentions       BOOLEAN NOT NULL DEFAULT TRUE,
    push_follows        BOOLEAN NOT NULL DEFAULT TRUE,
    push_friend_requests BOOLEAN NOT NULL DEFAULT TRUE,
    push_group_posts    BOOLEAN NOT NULL DEFAULT TRUE,
    push_group_mentions BOOLEAN NOT NULL DEFAULT TRUE,
    push_channel_updates BOOLEAN NOT NULL DEFAULT TRUE,
    push_channel_urgent BOOLEAN NOT NULL DEFAULT TRUE,
    push_community_posts BOOLEAN NOT NULL DEFAULT FALSE,
    push_community_mentions BOOLEAN NOT NULL DEFAULT TRUE,
    push_event_reminders BOOLEAN NOT NULL DEFAULT TRUE,
    push_system         BOOLEAN NOT NULL DEFAULT TRUE,
    email_digest        VARCHAR(10) NOT NULL DEFAULT 'weekly',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
