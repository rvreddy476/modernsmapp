-- Module 4 rescue: transactional media events and durable poison quarantine.
--
-- A media state transition and the event describing it must be one database
-- commit.  Kafka publication is deliberately outside that transaction and is
-- retried from this table; publishing twice is safe because event_id is stable.
CREATE TABLE IF NOT EXISTS media_event_outbox (
    event_id        TEXT PRIMARY KEY,
    -- No FK by design: deletion/takedown must not erase an event that was
    -- already committed but not yet published.
    media_asset_id  UUID NOT NULL,
    event_type      TEXT NOT NULL,
    actor_user_id   UUID,
    payload         JSONB NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at    TIMESTAMPTZ,
    attempts        INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (media_asset_id, event_type)
);

CREATE INDEX IF NOT EXISTS idx_media_event_outbox_pending
    ON media_event_outbox (created_at)
    WHERE published_at IS NULL;

-- A malformed Kafka record may never become decodable.  It may be committed
-- only after its exact bytes and coordinates are recorded here so operators
-- can inspect/replay it without stalling every later upload on the partition.
CREATE TABLE IF NOT EXISTS media_transcode_quarantine (
    topic           TEXT NOT NULL,
    partition_id    INT NOT NULL,
    offset_id       BIGINT NOT NULL,
    message_key     BYTEA,
    message_value   BYTEA NOT NULL,
    failure         TEXT NOT NULL,
    quarantined_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (topic, partition_id, offset_id)
);
