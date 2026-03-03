-- Consumer inbox deduplication tables for identity-platform services
-- Prevents duplicate event processing on Kafka consumer restarts

CREATE TABLE IF NOT EXISTS profile.inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);

CREATE TABLE IF NOT EXISTS usr.inbox_events (
    consumer_name TEXT NOT NULL,
    event_id      TEXT NOT NULL,
    processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (consumer_name, event_id)
);

-- Auto-cleanup: remove entries older than 7 days via a periodic job
CREATE INDEX IF NOT EXISTS idx_profile_inbox_cleanup ON profile.inbox_events (processed_at);
CREATE INDEX IF NOT EXISTS idx_usr_inbox_cleanup ON usr.inbox_events (processed_at);
