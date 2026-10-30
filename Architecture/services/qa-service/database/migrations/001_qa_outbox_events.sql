-- qa.outbox_events: the transactional outbox (Q&A backend audit L2,
-- 30 Oct 2026).
--
-- Q&A events used to be written to Kafka from a detached goroutine with a
-- 5 s timeout, so a shutdown or a broker hiccup lost them (and with them the
-- "answered your question" notifications). Question/answer creation and
-- edits and comment creation now write their event here in the same
-- transaction as the row (internal/store/outbox.go); the other events are
-- inserted right after their write (internal/events/producer.go).
-- shared/outbox.Publisher (cmd/server, outbox.Config{DBSchema: "qa"})
-- relays them to the qa-events topic. payload is the whole shared envelope,
-- the Kafka value consumers already decode.
--
-- Schema-local, like food.outbox_events and dating.outbox_events: qa-service
-- shares the app database, whose public.outbox_events belongs to another
-- declaration with another shape.
--
-- The relay claim columns (shared/outbox ClaimColumns) are part of the table
-- from the start:
--   claimed_until  lease of the relay that claimed the row; also the back-off
--                  of a row the broker refused.
--   attempts       counted failed publishes (the broker refused the record).
--   last_error     why the last publish failed (error text, never the payload).
--   failed_at      dead letter: skipped by the relay, kept for an operator.
--                  Requeue: UPDATE qa.outbox_events SET failed_at = NULL, attempts = 0
--                  WHERE id = ...;
--
-- New objects only: no lock on any existing table.
CREATE SCHEMA IF NOT EXISTS qa;

CREATE TABLE IF NOT EXISTS qa.outbox_events (
    id            BIGSERIAL PRIMARY KEY,
    event_type    TEXT        NOT NULL,
    partition_key TEXT        NOT NULL,
    payload       JSONB       NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at  TIMESTAMPTZ,
    claimed_until TIMESTAMPTZ,
    attempts      INT         NOT NULL DEFAULT 0,
    last_error    TEXT,
    failed_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_qa_outbox_unpublished
    ON qa.outbox_events (id) WHERE published_at IS NULL;
