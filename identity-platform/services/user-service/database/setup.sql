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
