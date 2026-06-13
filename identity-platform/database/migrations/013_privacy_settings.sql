-- 013_privacy_settings.sql
-- Expands usr.user_settings to the messaging/privacy spec v2 §5.1 field set.
--
-- New columns default to strict values ("privacy by default", spec principle P1).
-- Decision D2: existing users are grandfathered one notch looser on who_can_message
-- so the migration does not silently cut off live conversations. The UPDATE runs
-- once, so every row present now is a pre-existing user; rows inserted afterwards
-- keep the strict 'connections_only' default.

ALTER TABLE usr.user_settings
    ADD COLUMN IF NOT EXISTS who_can_message                   TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_send_connection_request   TEXT    NOT NULL DEFAULT 'friends_of_friends_or_contacts',
    ADD COLUMN IF NOT EXISTS who_can_call                      TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_add_to_groups             TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_online_status         TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_read_receipts         TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_last_seen             TEXT    NOT NULL DEFAULT 'connections_only',
    ADD COLUMN IF NOT EXISTS who_can_see_profile_photo         TEXT    NOT NULL DEFAULT 'everyone',
    ADD COLUMN IF NOT EXISTS allow_phone_discovery             BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS allow_contact_sync_match          BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS discoverable_by_phone_to_contacts BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS strict_privacy_mode               BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS block_unknown_calls               BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS auto_filter_abusive_content       BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS under_18_mode                     BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS privacy_version                   INTEGER NOT NULL DEFAULT 1;

-- Decision D2: grandfather every pre-existing user.
UPDATE usr.user_settings SET who_can_message = 'connections_and_mutual_followers';

-- Spec §8.1: fast lookup of minor accounts for server-enforced strict mode.
CREATE INDEX IF NOT EXISTS idx_user_settings_under18
    ON usr.user_settings (under_18_mode) WHERE under_18_mode = TRUE;
