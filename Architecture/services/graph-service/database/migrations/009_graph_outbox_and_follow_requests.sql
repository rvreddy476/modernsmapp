-- Module 3 M3-P0-6 / SR-2 — durable graph safety events.
--
-- Graph safety events (block/unblock/follow) were published from
-- fire-and-forget goroutines. A broker outage, a pod eviction, or a slow
-- publish during shutdown silently dropped them, and nothing downstream ever
-- learned that a block happened. For a safety signal that chat, feed, search
-- and notifications all consume, "usually delivered" is not a contract.
--
-- The outbox row is written in the SAME transaction as the relationship
-- mutation, so either both exist or neither does. internal/store/outbox_relay.go
-- leases and delivers the rows; without that relay this table would only be a
-- record of events nobody sends.
--
-- SR-5: the follow_requests table that previously lived in this migration has
-- been REMOVED. Launch is public-accounts-only, so there is no private-account
-- follow request to hold. Shipping the table without the service, API,
-- resolver and client work behind it would have left a schema that implies a
-- privacy control the product does not have.

CREATE TABLE IF NOT EXISTS graph_outbox_events (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type     TEXT        NOT NULL,
    -- Pair key so a relay can order per-pair and consumers can dedupe.
    actor_id       UUID        NOT NULL,
    target_id      UUID        NOT NULL,
    -- Monotonic per-pair sequence; lets a consumer drop a stale replay.
    pair_seq       BIGINT      NOT NULL,
    payload        JSONB       NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published      BOOLEAN     NOT NULL DEFAULT FALSE,
    published_at   TIMESTAMPTZ,
    attempts       INT         NOT NULL DEFAULT 0,
    last_error     TEXT
);

-- Relay lease. Multiple relay replicas claim disjoint rows with
-- FOR UPDATE SKIP LOCKED; leased_until makes a lease held by a crashed
-- replica expire instead of stranding the row forever. last_attempt_at
-- drives the retry backoff so a broker outage produces paced retries rather
-- than a hot loop.
ALTER TABLE graph_outbox_events
    ADD COLUMN IF NOT EXISTS leased_until    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_attempt_at TIMESTAMPTZ;

-- Relay scan: oldest unpublished first.
CREATE INDEX IF NOT EXISTS idx_graph_outbox_unpublished
    ON graph_outbox_events (occurred_at)
    WHERE published = FALSE;

CREATE INDEX IF NOT EXISTS idx_graph_outbox_pair
    ON graph_outbox_events (actor_id, target_id, pair_seq);

-- Per-pair monotonic counter. The pair is stored canonically (lower uuid
-- first) so both directions share one sequence and one advisory lock.
CREATE TABLE IF NOT EXISTS graph_pair_seq (
    lo_id    UUID   NOT NULL,
    hi_id    UUID   NOT NULL,
    seq      BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (lo_id, hi_id)
);

-- SR-5 cleanup: drop follow_requests if an earlier build of this migration
-- created it. Public-accounts-only launch has no pending follow requests, and
-- an empty table that implies otherwise is worse than no table.
DROP TABLE IF EXISTS follow_requests;
