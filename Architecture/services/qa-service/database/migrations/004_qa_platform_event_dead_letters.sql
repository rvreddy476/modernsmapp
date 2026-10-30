-- Durable dead letters for platform-events the consumer could not apply
-- (Q&A backend audit M8, 30 Oct 2026).
--
-- The consumer used to commit each offset before processing and only log a
-- failure, so an account erasure or a community ban that met a Postgres
-- failover was lost. It now commits only after the event is applied, retries
-- a failing event with back-off, and after MaxAttempts parks it here (and
-- only then commits) so one poison message cannot hold the partition
-- forever. An operator replays a row by re-publishing its value; resolved_at
-- marks it handled.
--
-- New table only.
CREATE TABLE IF NOT EXISTS qa_platform_event_dead_letters (
    id           BIGSERIAL PRIMARY KEY,
    event_type   TEXT        NOT NULL DEFAULT '',
    topic        TEXT        NOT NULL,
    kafka_partition INT      NOT NULL,
    kafka_offset BIGINT      NOT NULL,
    value        BYTEA       NOT NULL,
    attempts     INT         NOT NULL,
    last_error   TEXT        NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ,
    UNIQUE (topic, kafka_partition, kafka_offset)
);
