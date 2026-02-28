-- Feed Service Keyspace
CREATE KEYSPACE IF NOT EXISTS social_feed WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

CREATE TABLE IF NOT EXISTS social_feed.home_timeline_by_user (
    user_id uuid,
    bucket int,
    ts timeuuid,
    post_id uuid,
    author_id uuid,
    created_at timestamp,
    PRIMARY KEY ((user_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC);

CREATE TABLE IF NOT EXISTS social_feed.author_timeline_by_author (
    author_id uuid,
    bucket int,
    ts timeuuid,
    post_id uuid,
    created_at timestamp,
    PRIMARY KEY ((author_id, bucket), ts)
) WITH CLUSTERING ORDER BY (ts DESC);


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

