-- Feed Service Keyspace
CREATE KEYSPACE IF NOT EXISTS social_feed WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

CREATE TABLE IF NOT EXISTS social_feed.home_timeline_by_user (
    user_id uuid,
    bucket int,
    ts timeuuid,
    post_id uuid,
    author_id uuid,
    created_at timestamp,
    content_type text,
    PRIMARY KEY ((user_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC);

CREATE TABLE IF NOT EXISTS social_feed.author_timeline_by_author (
    author_id uuid,
    bucket int,
    ts timeuuid,
    post_id uuid,
    created_at timestamp,
    content_type text,
    PRIMARY KEY ((author_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC);

-- HF4: reverse index for UpdatePostContentType. The fan-out tables
-- partition on (user_id, bucket) and (author_id, bucket); finding all
-- rows for a given post_id used to need ALLOW FILTERING, which scans
-- the whole table. This index lets us look up the precise set of
-- (user_id|author_id, bucket, ts) triples to rewrite — partition lookup
-- via the (post_id) key, no scan.
--
-- timeline_kind = 'home' for home_timeline_by_user rows, 'author' for
-- author_timeline_by_author rows. owner_id is user_id or author_id
-- accordingly. We write to this table inside AddToHomeTimeline +
-- AddToAuthorTimeline so it stays in sync; the back-populate worker
-- in cmd/backfill walks existing timelines once per deploy.
CREATE TABLE IF NOT EXISTS social_feed.timeline_index_by_post (
    post_id       uuid,
    timeline_kind text,
    owner_id      uuid,
    bucket        int,
    ts            timeuuid,
    PRIMARY KEY ((post_id), timeline_kind, owner_id, bucket, ts)
);

-- User-post interactions for ranking penalty (source of truth behind Redis cache)
-- 7-day TTL matches the interaction penalty relevance window.
CREATE TABLE IF NOT EXISTS social_feed.user_post_interactions (
    user_id  uuid,
    post_id  uuid,
    PRIMARY KEY (user_id, post_id)
) WITH default_time_to_live = 604800;


-- Notification Service Keyspace
CREATE KEYSPACE IF NOT EXISTS social_notify WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

CREATE TABLE IF NOT EXISTS social_notify.notifications_by_user (
    user_id uuid,
    bucket int,
    ts timeuuid,
    notification_id uuid,
    type text,
    actor_user_id uuid,
    entity_type text,
    entity_id uuid,
    deep_link text,
    is_read boolean,
    created_at timestamp,
    PRIMARY KEY ((user_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC);


-- Engagement Service Keyspace (used by post-service)
CREATE KEYSPACE IF NOT EXISTS social_engagement WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

-- Post Likes (SHARDED by hash(user_id) % 16 to avoid hot partitions on viral posts)
CREATE TABLE IF NOT EXISTS social_engagement.post_likes (
    post_id     uuid,
    shard       smallint,
    user_id     uuid,
    created_at  timestamp,
    PRIMARY KEY ((post_id, shard), user_id)
) WITH CLUSTERING ORDER BY (user_id ASC);

-- Reverse lookup: "what posts did I like?"
CREATE TABLE IF NOT EXISTS social_engagement.user_likes (
    user_id     uuid,
    post_id     uuid,
    created_at  timestamp,
    PRIMARY KEY ((user_id), created_at, post_id)
) WITH CLUSTERING ORDER BY (created_at DESC, post_id ASC);

-- Comment Likes (no sharding needed — individual comments won't get millions of likes)
CREATE TABLE IF NOT EXISTS social_engagement.comment_likes (
    comment_id  uuid,
    user_id     uuid,
    created_at  timestamp,
    PRIMARY KEY ((comment_id), user_id)
) WITH CLUSTERING ORDER BY (user_id ASC);

-- Post Shares (SHARDED same as likes)
CREATE TABLE IF NOT EXISTS social_engagement.post_shares (
    post_id     uuid,
    shard       smallint,
    user_id     uuid,
    created_at  timestamp,
    share_type  text,
    quote_text  text,
    PRIMARY KEY ((post_id, shard), user_id)
) WITH CLUSTERING ORDER BY (user_id ASC);

-- Reverse lookup: "what posts did I share?"
CREATE TABLE IF NOT EXISTS social_engagement.user_shares (
    user_id     uuid,
    post_id     uuid,
    created_at  timestamp,
    share_type  text,
    PRIMARY KEY ((user_id), created_at, post_id)
) WITH CLUSTERING ORDER BY (created_at DESC, post_id ASC);

-- User Bookmarks with collection support (partition by user+collection for efficient queries)
CREATE TABLE IF NOT EXISTS social_engagement.user_bookmarks (
    user_id     uuid,
    collection  text,
    created_at  timestamp,
    post_id     uuid,
    PRIMARY KEY ((user_id, collection), created_at, post_id)
) WITH CLUSTERING ORDER BY (created_at DESC, post_id ASC);

-- Bookmark membership check: "did I bookmark this post?"
CREATE TABLE IF NOT EXISTS social_engagement.bookmark_check (
    user_id     uuid,
    post_id     uuid,
    collection  text,
    PRIMARY KEY ((user_id), post_id)
);

-- Reactions by post (used by post-service interactions)
CREATE TABLE IF NOT EXISTS social_engagement.reactions_by_post (
    post_id     text,
    user_id     text,
    reaction    text,
    created_at  timestamp,
    PRIMARY KEY ((post_id), user_id)
);

-- Comments by post (bucketed by month for time-series queries)
CREATE TABLE IF NOT EXISTS social_engagement.comments_by_post (
    post_id     text,
    bucket      int,
    ts          timeuuid,
    comment_id  text,
    author_id   text,
    text        text,
    is_deleted  boolean,
    PRIMARY KEY ((post_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC);

-- Post counters (ScyllaDB counter table for like/comment counts)
CREATE TABLE IF NOT EXISTS social_engagement.post_counters (
    post_id        text PRIMARY KEY,
    like_count     counter,
    comment_count  counter
);

-- Engagement Counters (regular BIGINT with LWT, NOT native counter type)
-- Authority for likes/shares/bookmarks. Updated by ScyllaDB consumer via read-then-CAS.
CREATE TABLE IF NOT EXISTS social_engagement.engagement_counters (
    target_type   text,
    target_id     uuid,
    counter_type  text,
    count         bigint,
    updated_at    timestamp,
    PRIMARY KEY ((target_type, target_id), counter_type)
);


-- Chat Service Keyspace (used by chat-service message-service + ws-gateway)
CREATE KEYSPACE IF NOT EXISTS chatservice WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

CREATE TABLE IF NOT EXISTS chatservice.messages (
    conversation_id uuid,
    bucket text,
    ts timestamp,
    msg_id uuid,
    sender_id uuid,
    type text,
    text text,
    media_id uuid,
    is_deleted boolean,
    created_at timestamp,
    PRIMARY KEY ((conversation_id, bucket), ts, msg_id)
) WITH CLUSTERING ORDER BY (ts DESC, msg_id DESC);

CREATE TABLE IF NOT EXISTS chatservice.conversations_by_user (
    user_id uuid,
    bucket text,
    last_ts timestamp,
    conversation_id uuid,
    last_sender_id uuid,
    last_text text,
    PRIMARY KEY ((user_id, bucket), last_ts, conversation_id)
) WITH CLUSTERING ORDER BY (last_ts DESC, conversation_id DESC);

CREATE TABLE IF NOT EXISTS chatservice.message_reactions (
    conversation_id uuid,
    bucket text,
    msg_ts timestamp,
    msg_id uuid,
    emoji text,
    user_id uuid,
    created_at timestamp,
    PRIMARY KEY ((conversation_id, bucket), msg_ts, msg_id, emoji, user_id)
) WITH CLUSTERING ORDER BY (msg_ts DESC, msg_id DESC, emoji ASC, user_id ASC);


-- Video Analytics Keyspace (used by analytics-service)
CREATE KEYSPACE IF NOT EXISTS social_analytics WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

CREATE TABLE IF NOT EXISTS social_analytics.watch_sessions (
    content_id     UUID,
    session_id     TEXT,
    viewer_id      UUID,
    content_type   TEXT,
    duration_ms    BIGINT,
    watched_ms     BIGINT,
    percent_viewed DOUBLE,
    loop_count     INT,
    is_display_view BOOLEAN,
    milestones_hit SET<TEXT>,
    surface        TEXT,
    country        TEXT,
    device_hash    TEXT,
    is_autoplay    BOOLEAN,
    end_reason     TEXT,
    trust_factor   DOUBLE,
    vqs            DOUBLE,
    created_at     TIMESTAMP,
    updated_at     TIMESTAMP,
    PRIMARY KEY ((content_id), session_id)
) WITH default_time_to_live = 7776000;

CREATE TABLE IF NOT EXISTS social_analytics.viewer_history (
    viewer_id  UUID,
    content_id UUID,
    first_view TIMESTAMP,
    view_count INT,
    PRIMARY KEY ((viewer_id), content_id)
) WITH default_time_to_live = 7776000;

-- User Pair Signals (used by suggestion-service for scoring)
-- Precomputed pair-level signals refreshed by batch jobs
CREATE TABLE IF NOT EXISTS social_analytics.user_pair_signals (
    viewer_id               UUID,
    candidate_id            UUID,
    mutual_friend_count     SMALLINT,
    mutual_strong_count     SMALLINT,
    common_group_count      SMALLINT,
    same_city               BOOLEAN,
    same_school             BOOLEAN,
    same_company            BOOLEAN,
    mutual_follow           BOOLEAN,
    profile_view_7d         SMALLINT,
    reverse_view_7d         SMALLINT,
    chat_interaction_30d    SMALLINT,
    content_coengagement_7d SMALLINT,
    triadic_closure_score   FLOAT,
    interest_similarity     FLOAT,
    last_updated_at         TIMESTAMP,
    PRIMARY KEY (viewer_id, candidate_id)
) WITH default_time_to_live = 604800;

