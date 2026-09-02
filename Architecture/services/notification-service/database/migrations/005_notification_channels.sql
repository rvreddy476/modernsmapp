-- Migration 005: In-app vs Push channel split (TikTok-style parity toggles).
--
-- Every push_* category column gains an inapp_* twin controlling whether the
-- durable inbox row (and realtime WS event) is created at all. Three new
-- categories arrive with both halves: reposts, LIVE, and messages (DMs +
-- message requests).
--
-- DEFENSIVE PREAMBLE: migration 003 used CREATE TABLE IF NOT EXISTS, which is
-- a no-op on databases where migration 001 already created the legacy-shaped
-- table (user_id UUID, no per-category columns). Such databases never got the
-- v2 columns at all. Re-assert every 003 column here with ADD COLUMN IF NOT
-- EXISTS so this migration leaves the table complete regardless of history.

-- 003 columns, re-asserted (no-ops where 003 actually created the table).
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS quiet_hours_enabled     BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS quiet_hours_tz          VARCHAR(50);
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_likes              BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_super_likes        BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_comments           BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_replies            BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_mentions           BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_follows            BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_friend_requests    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_group_posts        BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_group_mentions     BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_channel_updates    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_channel_urgent     BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_community_posts    BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_community_mentions BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_event_reminders    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_system             BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS email_digest            VARCHAR(10) NOT NULL DEFAULT 'weekly';

-- Like pushes are ON by default (TikTok parity). Only the column default and
-- the in-code default change: existing rows keep whatever the user stored.
-- (An UPDATE for "still at the old default" is deliberately NOT done — a row
-- at FALSE cannot be distinguished from a user who chose FALSE.)
ALTER TABLE notification_preferences ALTER COLUMN push_likes SET DEFAULT TRUE;

-- In-app twins for every existing push_* category (inbox row + realtime).
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_likes              BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_super_likes        BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_comments           BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_replies            BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_mentions           BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_follows            BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_friend_requests    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_group_posts        BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_group_mentions     BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_channel_updates    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_channel_urgent     BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_community_posts    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_community_mentions BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_event_reminders    BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_system             BOOLEAN NOT NULL DEFAULT TRUE;

-- New categories: reposts, LIVE, messages — both halves, default on.
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_reposts   BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_reposts  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_live      BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_live     BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS push_messages  BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE notification_preferences ADD COLUMN IF NOT EXISTS inapp_messages BOOLEAN NOT NULL DEFAULT TRUE;
