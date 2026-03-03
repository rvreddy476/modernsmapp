-- Database setup for Architecture/message-service

CREATE SCHEMA IF NOT EXISTS chat;

CREATE TABLE IF NOT EXISTS chat.conversations (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL,                 -- 'direct' or 'group'
    name TEXT,                          -- group name; NULL for DMs
    icon_url TEXT,                      -- group icon URL
    created_by UUID,                    -- creator user_id
    last_message_at TIMESTAMPTZ,       -- for sort ordering
    last_message_preview TEXT,          -- snippet for list view
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS chat.conversation_members (
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member', -- 'admin', 'moderator', 'member'
    nickname TEXT,                       -- per-conversation nickname
    is_muted BOOLEAN NOT NULL DEFAULT false,
    last_read_message_id TEXT,          -- for unread count
    last_read_at TIMESTAMPTZ,
    joined_at TIMESTAMPTZ NOT NULL,
    left_at TIMESTAMPTZ,               -- NULL = still in
    PRIMARY KEY (conversation_id, user_id)
);

CREATE TABLE IF NOT EXISTS chat.direct_conversation_keys (
    user_a UUID NOT NULL,
    user_b UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    PRIMARY KEY (user_a, user_b),
    CHECK (user_a < user_b)
);

CREATE TABLE IF NOT EXISTS chat.message_reads (
    conversation_id UUID NOT NULL,
    user_id UUID NOT NULL,
    message_id TEXT NOT NULL,
    read_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id, message_id)
);

CREATE INDEX IF NOT EXISTS idx_message_reads_msg ON chat.message_reads (message_id);
CREATE INDEX IF NOT EXISTS idx_conv_members_user ON chat.conversation_members (user_id);

-- Phase 5: Pinned messages
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS pinned_message_id TEXT,
    ADD COLUMN IF NOT EXISTS pinned_at         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pinned_by         UUID;

-- ============================================================
-- message_reactions — normalized reactions (v2.1, replaces JSONB)
-- ============================================================
CREATE TABLE IF NOT EXISTS chat.message_reactions (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id    TEXT NOT NULL,
    user_id       UUID NOT NULL,
    reaction_type TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(message_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_msg_reactions_message ON chat.message_reactions (message_id);
CREATE INDEX IF NOT EXISTS idx_msg_reactions_user ON chat.message_reactions (user_id, created_at DESC);
