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
        modules <@ ARRAY['reels','commerce','chat','dating','food','qa','posttube']::TEXT[]
    ),
    CONSTRAINT module_preferences_home_module_known CHECK (
        home_module IN ('feed','reels','commerce','chat','dating','food','qa','posttube')
    )
);
