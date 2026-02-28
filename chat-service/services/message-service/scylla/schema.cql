CREATE KEYSPACE IF NOT EXISTS chatservice
  WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

-- Messages table: bucketed by month for bounded partition growth.
-- Partition key: (conversation_id, bucket) where bucket = 'YYYYMM'.
-- Works for both 1:1 and group conversations.
CREATE TABLE IF NOT EXISTS chatservice.messages (
    conversation_id UUID,
    bucket TEXT,
    ts TIMESTAMP,
    msg_id UUID,
    sender_id UUID,
    type TEXT,
    text TEXT,
    media_id UUID,
    is_deleted BOOLEAN,
    created_at TIMESTAMP,
    PRIMARY KEY ((conversation_id, bucket), ts, msg_id)
) WITH CLUSTERING ORDER BY (ts DESC, msg_id DESC);

-- Inbox projection: one row per member per message.
-- Deduplicated on read by conversation_id to get latest state.
CREATE TABLE IF NOT EXISTS chatservice.conversations_by_user (
    user_id UUID,
    bucket TEXT,
    last_ts TIMESTAMP,
    conversation_id UUID,
    last_sender_id UUID,
    last_text TEXT,
    PRIMARY KEY ((user_id, bucket), last_ts, conversation_id)
) WITH CLUSTERING ORDER BY (last_ts DESC, conversation_id DESC);

-- Message reactions: one row per user per emoji per message.
-- Same partition scope as messages for co-located reads.
CREATE TABLE IF NOT EXISTS chatservice.message_reactions (
    conversation_id UUID,
    bucket TEXT,
    msg_ts TIMESTAMP,
    msg_id UUID,
    emoji TEXT,
    user_id UUID,
    created_at TIMESTAMP,
    PRIMARY KEY ((conversation_id, bucket), msg_ts, msg_id, emoji, user_id)
) WITH CLUSTERING ORDER BY (msg_ts DESC, msg_id DESC, emoji ASC, user_id ASC);
