-- Echo notes timeline (docs/designs/echo-contract.md, "feed-service").
--
-- The Scylla schema lives in Architecture/docker/scylla/schema.cql,
-- schema.qa.cql and schema.prod.cql, outside this service. This block goes
-- into all three, after social_feed.home_timeline_by_user; it is kept here
-- so the feed-service tests (internal/store/scylla/notes_test.go) pin the
-- primary key the store's statements are checked against.
--
-- Same shape, bucketing and retention as home_timeline_by_user: one row per
-- (follower, note), clustered newest first by TimelineTS. Reverse-index rows
-- live in timeline_index_by_post_v2 with timeline_kind = 'notes'.
CREATE TABLE IF NOT EXISTS social_feed.notes_timeline (
    user_id uuid,
    bucket int,
    ts timeuuid,
    post_id uuid,
    author_id uuid,
    created_at timestamp,
    content_type text,
    PRIMARY KEY ((user_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC) AND default_time_to_live = 31104000;
