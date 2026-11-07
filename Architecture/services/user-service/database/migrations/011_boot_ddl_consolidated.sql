-- 011 — the schema objects that only cmd/server/main.go created (2026-11-07,
-- audit US-L5).
--
-- main.go ran a block of DDL on every boot after BootstrapSchema
-- (ensurePhase6Schema plus inline "migrations" 005 to 009), each statement's
-- error only logged. Most of it repeated database/setup.sql and migrations
-- 001, 005, 006 and 007; it also still declared the retired three-value page
-- status CHECK that setup.sql drops. The objects below were declared nowhere
-- else, so they move here and the boot DDL is gone: the schema is
-- setup.sql plus this folder, and a failure stops the boot instead of being
-- logged and ignored.
--
-- Every statement is IF NOT EXISTS, so on a database the old boot code
-- already shaped this is a no-op. The one ALTER runs only when the column is
-- missing (a fresh database), so an existing one never takes the table lock.

-- channels.is_default: the default channel EnsurePublisher creates.
DO $$
BEGIN
    IF to_regclass('channels') IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM pg_attribute
        WHERE attrelid = to_regclass('channels') AND attname = 'is_default' AND NOT attisdropped
    ) THEN
        ALTER TABLE channels ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE;
    END IF;
END $$;

-- Global handle registry: uniqueness across accounts, channels and pages.
CREATE TABLE IF NOT EXISTS handles (
    handle     TEXT PRIMARY KEY,
    owner_type TEXT NOT NULL,
    owner_id   UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_handles_owner ON handles (owner_id, owner_type);

-- Handle history, for redirects after a rename.
CREATE TABLE IF NOT EXISTS handle_history (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    handle     TEXT NOT NULL,
    owner_type TEXT NOT NULL,
    owner_id   UUID NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_handle_history_handle ON handle_history (handle);

-- Durable cursor for the app.users <- profile.profiles reconcile job.
CREATE TABLE IF NOT EXISTS projection_sync_checkpoint (
    projection_name TEXT PRIMARY KEY,
    last_synced_at  TIMESTAMPTZ NOT NULL,
    last_success_at TIMESTAMPTZ,
    last_error      TEXT,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Consumer dead-letter queue: failed Kafka events parked for replay.
CREATE TABLE IF NOT EXISTS consumer_dlq (
    id          BIGSERIAL PRIMARY KEY,
    topic       TEXT NOT NULL,
    event_type  TEXT NOT NULL DEFAULT '',
    payload     TEXT NOT NULL,
    error       TEXT NOT NULL,
    failed_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    replayed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_consumer_dlq_unreplayed ON consumer_dlq (id) WHERE replayed_at IS NULL;
