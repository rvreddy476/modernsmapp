CREATE SCHEMA IF NOT EXISTS chat;

CREATE TABLE IF NOT EXISTS chat.conversations (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('direct', 'group')),
    title TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chat.conversation_members (
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX idx_conversation_members_user ON chat.conversation_members(user_id);

CREATE TABLE IF NOT EXISTS chat.direct_conversation_keys (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);

CREATE TABLE IF NOT EXISTS chat.idempotency_keys (
    key TEXT PRIMARY KEY,
    request_hash TEXT NOT NULL,
    response JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours')
);
CREATE INDEX idx_idempotency_expires ON chat.idempotency_keys(expires_at);

CREATE TABLE IF NOT EXISTS chat.outbox_events (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX idx_outbox_unpublished ON chat.outbox_events(id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS chat.user_profiles (
    user_id UUID PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    avatar_media_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
