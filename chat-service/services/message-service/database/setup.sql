CREATE SCHEMA IF NOT EXISTS chat;

CREATE TABLE IF NOT EXISTS chat.conversations (
    id UUID PRIMARY KEY,
    type TEXT NOT NULL CHECK (type IN ('direct', 'group')),
    title TEXT,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- P0-3 (dating PRODUCTION_GAP_ANALYSIS.md): conversations spawned by
-- dating-service carry source_app='dating' + the originating match_id.
-- The send-path gate consults source_app + closed_at to enforce
-- dating-match-specific rules: an active match must exist, neither side
-- has blocked the other (covered by conversation_members.left_at), and
-- the match hasn't closed/expired. Defaults keep all legacy + new
-- conversations as source_app='chat' with no behavioural change.
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS source_app TEXT NOT NULL DEFAULT 'chat'
        CHECK (source_app IN ('chat','dating')),
    ADD COLUMN IF NOT EXISTS match_id UUID,
    ADD COLUMN IF NOT EXISTS closed_at TIMESTAMPTZ;
CREATE UNIQUE INDEX IF NOT EXISTS idx_conversations_dating_match
    ON chat.conversations(match_id) WHERE source_app = 'dating' AND match_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS chat.conversation_members (
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    user_id UUID NOT NULL,
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
    joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- left_at is set when a member is severed from a conversation (e.g. by a
    -- block — messaging/privacy spec §16.1). A non-NULL left_at means the
    -- user is no longer an active member.
    left_at TIMESTAMPTZ,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_conversation_members_user ON chat.conversation_members(user_id);

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
CREATE INDEX IF NOT EXISTS idx_idempotency_expires ON chat.idempotency_keys(expires_at);

CREATE TABLE IF NOT EXISTS chat.outbox_events (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_outbox_unpublished ON chat.outbox_events(id) WHERE published_at IS NULL;

CREATE TABLE IF NOT EXISTS chat.user_profiles (
    user_id UUID PRIMARY KEY,
    display_name TEXT NOT NULL DEFAULT '',
    avatar_media_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ===== Messenger features =====
-- Folded in from migrations/002-004 so BootstrapSchema (which runs setup.sql)
-- applies the FULL schema on every boot. All statements are idempotent.
-- Chat folders for organizing conversations
CREATE TABLE IF NOT EXISTS chat.conversation_settings (
    conversation_id         UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_id                 UUID NOT NULL,
    label                   TEXT CHECK (label IN ('primary','requests','fan_inbox','business','archived','spam')),
    is_muted                BOOLEAN NOT NULL DEFAULT FALSE,
    mute_until              TIMESTAMPTZ,
    disappear_after_ms      BIGINT,
    read_receipts_enabled   BOOLEAN NOT NULL DEFAULT TRUE,
    theme                   TEXT DEFAULT 'default',
    is_pinned               BOOLEAN NOT NULL DEFAULT FALSE,
    pinned_at               TIMESTAMPTZ,
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_conv_settings_user_label ON chat.conversation_settings(user_id, label);

CREATE TABLE IF NOT EXISTS chat.chat_folders (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL,
    name        VARCHAR(50) NOT NULL,
    icon        TEXT DEFAULT 'folder',
    sort_order  INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS chat.chat_folder_conversations (
    folder_id       UUID NOT NULL REFERENCES chat.chat_folders(id) ON DELETE CASCADE,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    added_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (folder_id, conversation_id)
);

-- Pinned messages
CREATE TABLE IF NOT EXISTS chat.conversation_pins (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    message_id      UUID NOT NULL,
    pinned_by       UUID NOT NULL,
    pinned_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_conv_pins_conversation ON chat.conversation_pins(conversation_id, pinned_at DESC);

-- Message requests
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS is_request BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS request_accepted_at TIMESTAMPTZ;
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS request_declined_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS chat.message_request_settings (
    user_id         UUID PRIMARY KEY,
    allow_from      TEXT NOT NULL DEFAULT 'everyone'
        CHECK (allow_from IN ('everyone','followers','friends','nobody')),
    auto_filter_likely_spam BOOLEAN NOT NULL DEFAULT TRUE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Starred messages
CREATE TABLE IF NOT EXISTS chat.starred_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
    message_id      UUID NOT NULL,
    message_preview TEXT,
    starred_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_starred_user ON chat.starred_messages(user_id, starred_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_starred_unique ON chat.starred_messages(user_id, message_id);

-- Chat backups
CREATE TABLE IF NOT EXISTS chat.chat_backups (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID NOT NULL,
    status              TEXT NOT NULL DEFAULT 'in_progress'
        CHECK (status IN ('in_progress','completed','failed')),
    size_bytes          BIGINT,
    message_count       BIGINT,
    encrypted_blob_url  TEXT,
    key_hint            TEXT,
    backup_version      INT NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_chat_backups_user ON chat.chat_backups(user_id, created_at DESC);

-- Scheduled messages
CREATE TABLE IF NOT EXISTS chat.scheduled_messages (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    sender_id       UUID NOT NULL,
    type            TEXT NOT NULL DEFAULT 'text',
    content         TEXT,
    media_id        UUID,
    send_at         TIMESTAMPTZ NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','sent','cancelled','failed')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_scheduled_msg_send ON chat.scheduled_messages(send_at) WHERE status = 'pending';

-- Message translations
CREATE TABLE IF NOT EXISTS chat.message_translations (
    message_id      UUID NOT NULL,
    conversation_id UUID NOT NULL,
    target_lang     VARCHAR(10) NOT NULL,
    translated_text TEXT NOT NULL,
    source_lang     VARCHAR(10),
    translated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, target_lang)
);

-- Message threads
CREATE TABLE IF NOT EXISTS chat.message_threads (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id     UUID NOT NULL REFERENCES chat.conversations(id),
    parent_message_id   UUID NOT NULL,
    reply_count         INT NOT NULL DEFAULT 0,
    last_reply_at       TIMESTAMPTZ,
    last_reply_preview  TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_message_threads_conv ON chat.message_threads(conversation_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_threads_parent ON chat.message_threads(conversation_id, parent_message_id);
-- 003_message_requests.sql
-- Dedicated message-request envelope (messaging/privacy spec v2 §8.6).
--
-- The conversation row already carries is_request=TRUE (from migration 002).
-- This table holds the request lifecycle: who sent it, the first-message
-- preview, status, risk score and expiry. One row per request conversation.

CREATE TABLE IF NOT EXISTS chat.message_requests (
    id              BIGSERIAL PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    sender_id       UUID NOT NULL,
    receiver_id     UUID NOT NULL,
    preview         VARCHAR(500) NOT NULL DEFAULT '',
    status          VARCHAR(16) NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','accepted','ignored','blocked','reported','expired')),
    risk_score      SMALLINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at    TIMESTAMPTZ,
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '30 days')
);

-- One message_requests row per conversation.
CREATE UNIQUE INDEX IF NOT EXISTS idx_message_requests_conversation
    ON chat.message_requests(conversation_id);
-- Drives the recipient's Requests folder.
CREATE INDEX IF NOT EXISTS idx_message_requests_receiver_pending
    ON chat.message_requests(receiver_id, created_at DESC) WHERE status = 'pending';
-- 004_conversation_member_left_at.sql
-- Block-sever support (messaging/privacy spec v2 §16.1).
--
-- When user A blocks user B, A is severed from their shared direct
-- conversation: the conversation disappears from A's inbox and A can no
-- longer send into it. We model the sever non-destructively with a
-- left_at timestamp on the membership row so the conversation history
-- and the other party's view are preserved.

ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS left_at TIMESTAMPTZ;
