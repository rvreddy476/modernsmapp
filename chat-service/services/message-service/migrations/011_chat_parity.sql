-- Chat parity programme (18 Oct 2026, lane B1). CONTRACT sections 1, 2, 3,
-- 5, 6 and the request filtering of section 7. Everything is additive and
-- idempotent; database/setup.sql carries the same statements (applied on
-- every boot by BootstrapSchema).

-- §1 Inbox — per-member unread counter and hide / clear-for-me.
--
-- unread_count is incremented for every OTHER active member by the durable
-- delivery completion, guarded by message_delivery_intents.unread_counted_at
-- in the same transaction, so a repair replay never counts a message twice.
-- MarkRead resets it to 0. The inbox also reconciles it against the read
-- cursor (a cursor newer than the last message forces 0), so a lost reset or
-- an increment that raced a read can never leave a badge on a read chat.
--
-- cleared_before is the member's history cut-off: messages at or before it
-- are no longer returned to that member (messages, shared tabs, search).
-- hidden is "deleted for me": the conversation leaves the member's lists
-- until a message newer than cleared_before arrives.
ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS unread_count   INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cleared_before TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS hidden         BOOLEAN NOT NULL DEFAULT FALSE;

-- §2 Messages — the rich-message payload (voice, sticker, gif, profile,
-- sound, live, forwarded, reply kind, media metadata) rides the durable
-- intent as JSON so repair replays it into Scylla (messages.extra) exactly
-- as the live path wrote it. unread_counted_at is the idempotency guard for
-- the unread increment above.
ALTER TABLE chat.message_delivery_intents
    ADD COLUMN IF NOT EXISTS extra             JSONB,
    ADD COLUMN IF NOT EXISTS unread_counted_at TIMESTAMPTZ;

-- §1 last_message.type for the inbox row.
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS last_message_type TEXT NOT NULL DEFAULT '';

-- §1.1 conversation settings: mute calls (call-service reads
-- chat.conversation_settings.mute_calls and does not ring that member) and
-- the per-conversation alert tone.
ALTER TABLE chat.conversation_settings
    ADD COLUMN IF NOT EXISTS mute_calls BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS alert_tone TEXT NOT NULL DEFAULT 'default'
        CHECK (alert_tone IN ('default','chime','pop','bell','silent'));

-- §2.3 delete for me: one row per (viewer, message) the viewer hid.
CREATE TABLE IF NOT EXISTS chat.hidden_messages (
    user_id         UUID NOT NULL,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    message_id      UUID NOT NULL,
    hidden_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, conversation_id, message_id)
);

-- §1.4 shared posts / media tabs. Written by the durable delivery completion
-- (idempotent on the message id), removed when the message is unsent.
CREATE TABLE IF NOT EXISTS chat.conversation_shared_items (
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    message_id      UUID NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('post','media')),
    post_id         UUID,
    media_id        UUID,
    media_kind      TEXT,
    sender_id       UUID NOT NULL,
    sent_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (conversation_id, message_id)
);
CREATE INDEX IF NOT EXISTS idx_conversation_shared_items_kind
    ON chat.conversation_shared_items(conversation_id, kind, sent_at DESC, message_id DESC);

-- §3 streaks, per DM pair. Days are Asia/Kolkata calendar days. user_a /
-- user_b fix which side a_last_day / b_last_day belong to (user_a < user_b).
CREATE TABLE IF NOT EXISTS chat.dm_streaks (
    conversation_id UUID PRIMARY KEY REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_a          UUID NOT NULL,
    user_b          UUID NOT NULL,
    days            INT  NOT NULL DEFAULT 0,
    last_day        DATE,
    a_last_day      DATE,
    b_last_day      DATE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- §5 Notes: a 24-hour status line. PUT replaces the author's live note.
CREATE TABLE IF NOT EXISTS chat.notes (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL,
    text       TEXT NOT NULL,
    audience   TEXT NOT NULL CHECK (audience IN ('everyone','friends','only_me')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notes_user ON chat.notes(user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notes_expires ON chat.notes(expires_at);

-- §2.7 sticker catalogue. Stickers point at media-service assets; the
-- internal admin route writes them. An empty catalogue is valid.
CREATE TABLE IF NOT EXISTS chat.sticker_packs (
    id         UUID PRIMARY KEY,
    name       TEXT NOT NULL,
    sort_order INT  NOT NULL DEFAULT 0,
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS chat.stickers (
    id         UUID PRIMARY KEY,
    pack_id    UUID NOT NULL REFERENCES chat.sticker_packs(id) ON DELETE CASCADE,
    media_id   UUID NOT NULL,
    url        TEXT NOT NULL DEFAULT '',
    width      INT  NOT NULL CHECK (width > 0),
    height     INT  NOT NULL CHECK (height > 0),
    sort_order INT  NOT NULL DEFAULT 0,
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_stickers_pack ON chat.stickers(pack_id, sort_order);
CREATE INDEX IF NOT EXISTS idx_stickers_media ON chat.stickers(media_id);

-- §7 safe requests: the policy projection carries the new identity keys
-- (missing keys project as the defaults: safe mode on, no keywords, no
-- sensitive-media filter, default bubble).
ALTER TABLE chat.user_policy
    ADD COLUMN IF NOT EXISTS dm_safe_mode         BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS dm_filtered_keywords TEXT[]  NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS dm_sensitive_media_filter BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS chat_bubble_style    TEXT    NOT NULL DEFAULT 'default';

-- §7 account age for the "new account" request signal (cached profile).
ALTER TABLE chat.user_profiles
    ADD COLUMN IF NOT EXISTS username           TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS account_created_at TIMESTAMPTZ;

-- Scylla: chatservice.messages gains `extra text` (JSON). message-service
-- adds it at boot (EnsureMessageColumns); for a manual apply:
--   ALTER TABLE chatservice.messages ADD extra text;
