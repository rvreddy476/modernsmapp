-- user-service schema.
--
-- user-service reads usr.users and usr.user_settings, which are provisioned by
-- auth-service (services/auth-service/database/setup.sql) alongside the auth
-- base tables. Only the table this service genuinely owns is created here.
--
-- usr.inbox_events was defined in database/migrations/006_inbox_events.sql and
-- never applied — the boot-time migration runner was pointed at a disabled
-- directory, so the table does not exist in any environment.
--
-- Every statement is idempotent, so the pipeline can re-run this file safely.

CREATE SCHEMA IF NOT EXISTS usr;

-- Inbox for the Kafka consumer. Delivery is at-least-once, so the consumer
-- records each event id and skips repeats. Without this, a redelivered
-- user-created event reapplies a write that was already applied.
CREATE TABLE IF NOT EXISTS usr.inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);

-- Supports the retention sweep. The table is append-only per event, so without
-- a cleanup path indexed by time it grows forever.
CREATE INDEX IF NOT EXISTS idx_usr_inbox_cleanup
    ON usr.inbox_events (processed_at);

-- Module 3 — user region for server-driven module availability.
--
-- usr.users is provisioned by auth-service's setup.sql, which does not know
-- about region. The column is added here, idempotently, because this service
-- is the one that reads and writes it. ADD COLUMN IF NOT EXISTS is additive
-- DDL, safe to re-run on every boot.
ALTER TABLE usr.users ADD COLUMN IF NOT EXISTS region TEXT;

-- Module 3 — per-user module preferences (privacy-first, server-driven).
--
-- No row means "defaults": all modules on, home is the feed, onboarding not
-- completed. The CHECK constraints keep the column in the vocabulary the
-- server understands — an unknown module name can never be stored, however
-- the table is reached.
CREATE TABLE IF NOT EXISTS usr.module_preferences (
    user_id                 UUID PRIMARY KEY,
    modules                 TEXT[] NOT NULL,
    home_module             TEXT NOT NULL DEFAULT 'feed',
    onboarding_completed_at TIMESTAMPTZ NULL,
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- Every element of modules must be a known module name. <@ is
    -- "is contained by": vacuously true for the empty array, which is a
    -- legitimate privacy-first choice (feed only).
    CONSTRAINT module_preferences_modules_known CHECK (
        modules <@ ARRAY['reels','commerce','chat','dating','food','qa','posttube','mobility','home_services']::TEXT[]
    ),
    CONSTRAINT module_preferences_home_module_known CHECK (
        home_module IN ('feed','reels','commerce','chat','dating','food','qa','posttube','mobility','home_services')
    )
);

-- Re-runnable: the two CHECKs above only apply to a freshly created table.
-- When a module is added (mobility, 2026-09-18; home_services for Doorstep,
-- 2026-10-04), the constraints on an
-- existing table are replaced here so the same list applies everywhere.
ALTER TABLE usr.module_preferences DROP CONSTRAINT IF EXISTS module_preferences_modules_known;
ALTER TABLE usr.module_preferences ADD CONSTRAINT module_preferences_modules_known CHECK (
    modules <@ ARRAY['reels','commerce','chat','dating','food','qa','posttube','mobility','home_services']::TEXT[]
);
ALTER TABLE usr.module_preferences DROP CONSTRAINT IF EXISTS module_preferences_home_module_known;
ALTER TABLE usr.module_preferences ADD CONSTRAINT module_preferences_home_module_known CHECK (
    home_module IN ('feed','reels','commerce','chat','dating','food','qa','posttube','mobility','home_services')
);

-- Chat parity (2026-10-18, CONTRACT §7) — inbox safety and bubble colour.
--
-- usr.user_settings is created by auth-service's setup.sql, but these four
-- columns are added HERE, the same way region is added to usr.users above:
-- this service is the one that reads and writes them, and the embedded DDL
-- ships in the same binary as the code that selects them, so the two cannot
-- be deployed at different versions. The schema guard (requirements.go)
-- asserts every column after this file runs.
--
--   dm_safe_mode              requests from risky senders land in the
--                             "filtered" request sub-list. ON by default.
--   dm_sensitive_media_filter clients blur media flagged sensitive.
--   dm_filtered_keywords      ≤100 lower-cased, trimmed entries of 1..30
--                             characters (normalised by the service).
--   chat_bubble_style         own outgoing bubble colour, one of the six
--                             palette names below.
--
-- Additive, defaulted and idempotent: re-running this file is a no-op.
ALTER TABLE usr.user_settings
    ADD COLUMN IF NOT EXISTS dm_safe_mode              BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS dm_sensitive_media_filter BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS dm_filtered_keywords      TEXT[]  NOT NULL DEFAULT '{}'::TEXT[],
    ADD COLUMN IF NOT EXISTS chat_bubble_style         TEXT    NOT NULL DEFAULT 'default';

-- Backstops for writes that bypass the service. Dropped and re-added so the
-- vocabulary stays identical everywhere when it changes.
ALTER TABLE usr.user_settings DROP CONSTRAINT IF EXISTS user_settings_chat_bubble_style_known;
ALTER TABLE usr.user_settings ADD CONSTRAINT user_settings_chat_bubble_style_known CHECK (
    chat_bubble_style IN ('default','ocean','ember','forest','violet','mono')
);
ALTER TABLE usr.user_settings DROP CONSTRAINT IF EXISTS user_settings_dm_filtered_keywords_max;
ALTER TABLE usr.user_settings ADD CONSTRAINT user_settings_dm_filtered_keywords_max CHECK (
    cardinality(dm_filtered_keywords) <= 100
);

-- Mentions and tags (2026-10-24). who_can_mention: who may @mention this
-- account in a post or comment; who_can_tag: who may tag it on a post
-- (tagged_user_ids). Both everyone | following | no_one, default everyone;
-- "following" = accounts this user follows. post-service enforces them via
-- graph-service (a disallowed mention / tag is silently dropped). Added here
-- for the same reason as the chat-parity columns above; additive, defaulted
-- and idempotent.
ALTER TABLE usr.user_settings
    ADD COLUMN IF NOT EXISTS who_can_mention TEXT NOT NULL DEFAULT 'everyone',
    ADD COLUMN IF NOT EXISTS who_can_tag     TEXT NOT NULL DEFAULT 'everyone';
ALTER TABLE usr.user_settings DROP CONSTRAINT IF EXISTS user_settings_who_can_mention_known;
ALTER TABLE usr.user_settings ADD CONSTRAINT user_settings_who_can_mention_known CHECK (
    who_can_mention IN ('everyone','following','no_one')
);
ALTER TABLE usr.user_settings DROP CONSTRAINT IF EXISTS user_settings_who_can_tag_known;
ALTER TABLE usr.user_settings ADD CONSTRAINT user_settings_who_can_tag_known CHECK (
    who_can_tag IN ('everyone','following','no_one')
);
-- Minors and Strict Privacy Mode accounts never hold "everyone" (the service
-- clamps on write, applyStrictMode); this backfills existing rows. It only
-- matches the clamped value, so it is a no-op once applied.
UPDATE usr.user_settings
   SET who_can_mention = CASE WHEN who_can_mention = 'everyone' THEN 'following' ELSE who_can_mention END,
       who_can_tag     = CASE WHEN who_can_tag     = 'everyone' THEN 'following' ELSE who_can_tag     END,
       privacy_version = privacy_version + 1, updated_at = NOW()
 WHERE (under_18_mode OR strict_privacy_mode)
   AND (who_can_mention = 'everyone' OR who_can_tag = 'everyone');

-- DM consent model (founder-approved 2026-10-20) — value migration.
--
-- who_can_message now has exactly four values: everyone_message_requests,
-- followers_message_requests, people_i_follow, no_one. who_can_call drops
-- connections_only (accepting a request no longer creates a graph
-- connection), leaving no_one and accepted_chats_only. The service maps the
-- legacy values on every write and read (internal/service/user.go
-- legacyWhoCanMessage / legacyWhoCanCall — keep the maps identical); these
-- UPDATEs bring stored rows into line.
--
-- Safe to run on every boot: each statement only matches a value the service
-- can no longer store, so once a row is migrated it is never matched again
-- and no later user choice is overwritten. privacy_version is bumped on the
-- rows that change so downstream permission caches keyed by it re-fetch.
UPDATE usr.user_settings
   SET who_can_message = 'people_i_follow',
       privacy_version = privacy_version + 1, updated_at = NOW()
 WHERE who_can_message IN ('connections_only', 'connections_and_mutual_followers');

UPDATE usr.user_settings
   SET who_can_message = 'followers_message_requests',
       privacy_version = privacy_version + 1, updated_at = NOW()
 WHERE who_can_message = 'friends_of_friends_requests';

-- Minors (and Strict Privacy Mode accounts, which share the clamp) may never
-- hold a request-open value. The service clamps on write; this backfills rows
-- written before the clamp existed. Runs AFTER the legacy map above, so a
-- minor's friends_of_friends_requests is clamped in the same boot.
UPDATE usr.user_settings
   SET who_can_message = 'people_i_follow',
       privacy_version = privacy_version + 1, updated_at = NOW()
 WHERE (under_18_mode OR strict_privacy_mode)
   AND who_can_message IN ('everyone_message_requests', 'followers_message_requests');

UPDATE usr.user_settings
   SET who_can_call = 'accepted_chats_only',
       privacy_version = privacy_version + 1, updated_at = NOW()
 WHERE who_can_call = 'connections_only';

-- New rows: the column default moves with the contract. auth-service's
-- setup.sql (which creates the column) sets the same default; repeating it
-- here keeps a user-service-first deploy from inserting the retired value.
ALTER TABLE usr.user_settings ALTER COLUMN who_can_call SET DEFAULT 'accepted_chats_only';
