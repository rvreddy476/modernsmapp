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
