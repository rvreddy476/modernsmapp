-- 014_trusted_circle_settings.sql — friends-sheets spec §3.3.
-- Four per-feature Trusted Circle (close-friends) toggles. Defaults match the
-- spec: close-friends posts / location pings / after-hours posts default ON,
-- audio-room auto-invite defaults OFF. Idempotent.
ALTER TABLE usr.user_settings
    ADD COLUMN IF NOT EXISTS tc_close_friends_posts BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS tc_location_pings      BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS tc_after_hours_posts   BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS tc_audio_room_invite   BOOLEAN NOT NULL DEFAULT FALSE;
