-- Durable cross-store message delivery and canonical chat-media references.
--
-- PostgreSQL reserves an immutable delivery identity before Scylla writes.
-- A retry or repair worker then replays the same Scylla clustering keys and
-- records each outbound event at most once.

ALTER TABLE chat.outbox_events ADD COLUMN IF NOT EXISTS dedupe_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_dedupe_key
    ON chat.outbox_events(dedupe_key) WHERE dedupe_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS chat.message_delivery_intents (
    idempotency_key TEXT PRIMARY KEY,
    request_hash TEXT NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    sender_id UUID NOT NULL,
    message_id UUID NOT NULL UNIQUE,
    bucket TEXT NOT NULL,
    message_ts TIMESTAMPTZ NOT NULL,
    message_type TEXT NOT NULL,
    message_text TEXT NOT NULL DEFAULT '',
    media_id UUID,
    member_ids UUID[] NOT NULL,
    first_request_message BOOLEAN NOT NULL DEFAULT FALSE,
    request_receiver_id UUID,
    source_app TEXT NOT NULL DEFAULT 'chat',
    match_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_message_delivery_pending
    ON chat.message_delivery_intents(created_at) WHERE completed_at IS NULL;

CREATE TABLE IF NOT EXISTS chat.message_media_references (
    message_id UUID PRIMARY KEY,
    media_id UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_message_media_access
    ON chat.message_media_references(media_id, conversation_id);

ALTER TABLE chat.scheduled_messages
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error TEXT,
    ADD COLUMN IF NOT EXISTS failed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_scheduled_msg_retry
    ON chat.scheduled_messages(COALESCE(next_attempt_at, send_at)) WHERE status = 'pending';
