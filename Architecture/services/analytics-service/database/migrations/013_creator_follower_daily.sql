-- 013: net follower change per creator per day (TikTok parity, 2026-10-15).
--
-- GET /v1/analytics/creator/me returned followers_delta = null because
-- nothing in analytics knew about follows. graph-service already announces
-- every follow-edge change on social.events.v1:
--
--   UserFollowed    a NEW follow edge. A follow of a private account is only
--                   a request (graph.follow_requested, ignored here); the
--                   edge, and this event, appear when the request is
--                   accepted.
--   UserUnfollowed  an unfollow call. Counted only when edge_removed is
--                   true (or absent, from a producer that predates it): a
--                   repeat unfollow of a pair that no longer follows is
--                   published too and is not a lost follower.
--   UserBlocked     a block severs follows in both directions without a
--                   UserUnfollowed; removed_follow_forward/_reverse say
--                   which, and each is counted as an unfollow.
--
-- consumers.FollowerDeltaConsumer folds them into the table below.
-- History starts when this migration runs: there is no backfill, because
-- graph-service keeps only the follows that still exist (an unfollow
-- deletes the row), so the past cannot be reconstructed honestly.
-- follower_tracking.started_at records that moment.
--
-- Not money: nothing here feeds monetization.

CREATE TABLE IF NOT EXISTS analytics.creator_follower_daily (
    creator_id  UUID   NOT NULL,
    day_bucket  DATE   NOT NULL,
    follows     BIGINT NOT NULL DEFAULT 0 CHECK (follows >= 0),
    unfollows   BIGINT NOT NULL DEFAULT 0 CHECK (unfollows >= 0),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (creator_id, day_bucket)
);

-- One row per applied graph event, written in the same transaction as the
-- counter increment, so a Kafka redelivery (or an outbox re-publish, which
-- reuses the outbox row id as event_id) finds its receipt and changes
-- nothing. Holds no user id: purging a user has nothing to erase here, and
-- the receipt keeps a post-purge redelivery from re-creating their rows.
-- The consumer prunes receipts older than 30 days (well past the topic's
-- retention, so nothing that old can be redelivered).
CREATE TABLE IF NOT EXISTS analytics.follower_event_receipts (
    event_id    TEXT PRIMARY KEY CHECK (length(event_id) BETWEEN 1 AND 128),
    event_type  TEXT NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_follower_event_receipts_accepted
    ON analytics.follower_event_receipts (accepted_at);

-- Single row: when follower tracking began. Its presence is what turns
-- followers_delta from null ("not tracked yet") into a number, 0 included.
CREATE TABLE IF NOT EXISTS analytics.follower_tracking (
    id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO analytics.follower_tracking (id, started_at)
VALUES (1, NOW())
ON CONFLICT (id) DO NOTHING;
