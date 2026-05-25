CREATE KEYSPACE IF NOT EXISTS postbook WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 3};

CREATE TABLE IF NOT EXISTS postbook.messages (
    chat_id text,
    timestamp timestamp,
    msg_id text,
    sender_id text,
    receiver_id text,
    text text,
    message_type text,
    media_id text,
    reply_to_id text,
    forwarded_from_id text,
    is_edited boolean,
    edited_at timestamp,
    is_deleted boolean,
    deleted_at timestamp,
    PRIMARY KEY (chat_id, timestamp, msg_id)
) WITH CLUSTERING ORDER BY (timestamp DESC, msg_id DESC);

CREATE TABLE IF NOT EXISTS postbook.message_reactions (
    chat_id text,
    msg_id text,
    emoji text,
    user_id text,
    created_at timestamp,
    PRIMARY KEY ((chat_id, msg_id), emoji, user_id)
);
