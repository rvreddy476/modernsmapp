-- dating.outbox_events: the transactional outbox (dating backend audit H2,
-- 30 Oct 2026).
--
-- Chat closes a dating conversation only when it consumes dating.match.closed
-- or dating.user.blocked. Those used to be published with kafka-go on the
-- request context after the closure committed, so a dropped connection or a
-- broker hiccup left the chat open. Block, unmatch, suspension, a
-- chat-blocking risk level and profile deletion now write their events here
-- in the same transaction as the state change, and shared/outbox.Publisher
-- (cmd/server, outbox.Config{DBSchema: "dating"}) relays them to the dating
-- topic. payload is the whole shared envelope, the Kafka value consumers
-- already decode.
--
-- Schema-local, like food.outbox_events and doorstep.outbox_events: dating
-- shares the app database, whose public.outbox_events belongs to another
-- declaration with another shape.
--
-- The relay claim columns (shared/outbox ClaimColumns) are part of the table
-- from the start:
--   claimed_until  lease of the relay that claimed the row; also the back-off
--                  of a row the broker refused.
--   attempts       counted failed publishes (the broker refused the record).
--                  A Kafka outage is not counted and never dead-letters.
--   last_error     why the last publish failed (error text, never the payload).
--   failed_at      dead letter: skipped by the relay, kept for an operator.
--                  Requeue: UPDATE dating.outbox_events SET failed_at = NULL, attempts = 0
--                  WHERE id = ...;
--
-- New objects only: no lock on any existing table.
CREATE SCHEMA IF NOT EXISTS dating;

CREATE TABLE IF NOT EXISTS dating.outbox_events (
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

CREATE INDEX IF NOT EXISTS idx_dating_outbox_unpublished
    ON dating.outbox_events (id) WHERE published_at IS NULL;
