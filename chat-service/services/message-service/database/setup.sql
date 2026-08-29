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
ALTER TABLE chat.outbox_events ADD COLUMN IF NOT EXISTS dedupe_key TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_outbox_dedupe_key
    ON chat.outbox_events(dedupe_key) WHERE dedupe_key IS NOT NULL;

-- Durable coordinator for the cross-store message write. The HTTP request
-- reserves one immutable identity here before writing Scylla. Both an
-- idempotent client retry and the repair worker can then replay the exact same
-- Scylla clustering key, inbox projections and outbox events.
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
ALTER TABLE chat.scheduled_messages
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_error TEXT,
    ADD COLUMN IF NOT EXISTS failed_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_scheduled_msg_retry
    ON chat.scheduled_messages(COALESCE(next_attempt_at, send_at)) WHERE status = 'pending';

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

-- ===== 005: production chat pass (chat directive §3.4, §5.2) =====
-- All statements idempotent — BootstrapSchema replays this file on boot.

-- Groups gain a real OWNER role. The old CHECK ('admin','member') is swapped
-- for one that admits 'owner'; the DO block keeps the swap idempotent.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint
               WHERE conname = 'conversation_members_role_check') THEN
        ALTER TABLE chat.conversation_members
            DROP CONSTRAINT conversation_members_role_check;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint
                   WHERE conname = 'conversation_members_role_check_v2') THEN
        ALTER TABLE chat.conversation_members
            ADD CONSTRAINT conversation_members_role_check_v2
            CHECK (role IN ('owner', 'admin', 'member'));
    END IF;
END $$;

-- One-time promotion: existing group creators become owners so the exactly-
-- one-owner invariant holds for pre-pass groups. Guarded so a group that
-- already has an owner is never touched twice.
UPDATE chat.conversation_members m
SET role = 'owner'
FROM chat.conversations c
WHERE c.id = m.conversation_id
  AND c.type = 'group'
  AND c.created_by = m.user_id
  AND m.left_at IS NULL
  AND NOT EXISTS (
      SELECT 1 FROM chat.conversation_members o
      WHERE o.conversation_id = m.conversation_id
        AND o.role = 'owner' AND o.left_at IS NULL);

-- Group avatar + denormalized last-message metadata for the inbox list.
-- The preview is written by the SAME durable delivery completion that writes
-- Scylla, so replay produces the same value (idempotent upsert by ts guard).
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS avatar_media_id UUID,
    ADD COLUMN IF NOT EXISTS last_message_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_message_preview TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_message_sender UUID;

-- Group invitations (consent path for who_can_add_to_groups). Membership is
-- NOT created until the invitee accepts. One pending invite per
-- (conversation, invitee) — retries collapse onto the existing row.
CREATE TABLE IF NOT EXISTS chat.group_invitations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    inviter_id      UUID NOT NULL,
    invitee_id      UUID NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','accepted','declined','revoked','expired')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_invitations_pending
    ON chat.group_invitations(conversation_id, invitee_id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_group_invitations_invitee
    ON chat.group_invitations(invitee_id, created_at DESC) WHERE status = 'pending';

-- Durable per-member read cursors: the unread watermark the inbox and push
-- reconciliation repair against (directive §5.3, CH-LB-4.6).
CREATE TABLE IF NOT EXISTS chat.read_cursors (
    conversation_id      UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_id              UUID NOT NULL,
    last_read_message_id UUID,
    last_read_at         TIMESTAMPTZ NOT NULL,
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_read_cursors_user ON chat.read_cursors(user_id);

-- Local privacy-policy projection for the HOT paths (send/typing/receipts).
-- Populated lazily from identity user-service and invalidated by the
-- user.settings_changed event — the send path never makes an HTTP call.
CREATE TABLE IF NOT EXISTS chat.user_policy (
    user_id                 UUID PRIMARY KEY,
    chat_paused             BOOLEAN NOT NULL DEFAULT FALSE,
    send_typing_indicators  BOOLEAN NOT NULL DEFAULT TRUE,
    read_receipts_visibility TEXT   NOT NULL DEFAULT 'connections_only',
    privacy_version         INT     NOT NULL DEFAULT 0,
    refreshed_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Cooldown lookups: the most recent request between a pair, regardless of
-- conversation (a declined request must not be recreatable under a fresh
-- idempotency key — directive §3.3).
CREATE INDEX IF NOT EXISTS idx_message_requests_pair
    ON chat.message_requests(sender_id, receiver_id, created_at DESC);

-- Membership generations (final-verification P0-4 correction): entitlement
-- tokens and revocation markers compare GENERATIONS drawn from this sequence
-- while holding the conversation row lock, so their order follows the actual
-- serialization of membership writes — transaction-start NOW() timestamps
-- could invert (an early-started removal committing after a later rejoin
-- carried the OLDER timestamp, and the rejoin token outranked the marker).
CREATE SEQUENCE IF NOT EXISTS chat.membership_gen_seq;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS join_gen BIGINT;

-- Durable revocation intents (Blocker-2 final correction): every sever
-- writes its (conversation, user, sever generation) intent IN THE SAME
-- TRANSACTION, so a committed sever always leaves a durable record that the
-- Redis deny marker must still be written. The repair worker retries arming
-- until the marker is durable, then deletes the intent — removal success
-- never depends on a client or consumer choosing to retry.
CREATE TABLE IF NOT EXISTS chat.revocation_intents (
    conversation_id UUID NOT NULL,
    user_id         UUID NOT NULL,
    sever_gen       BIGINT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id)
);

-- Durable preview-repair obligations (MP-LB-1): a message deletion writes
-- this row BEFORE the Scylla soft delete, so a crash, Scylla read failure,
-- PostgreSQL write failure or restart after the delete can never leave the
-- deleted text in chat.conversations.last_message_preview indefinitely — the
-- repair worker resumes it. PRIMARY KEY on message_id makes a replayed
-- deletion idempotent. next_attempt_at doubles as a claim lease: workers
-- claim with FOR UPDATE SKIP LOCKED and push it forward, so two replicas
-- can never process the same obligation concurrently and a crashed worker's
-- claim simply expires.
CREATE TABLE IF NOT EXISTS chat.preview_repair_obligations (
    message_id      UUID PRIMARY KEY,
    conversation_id UUID NOT NULL,
    bucket          TEXT NOT NULL,
    deleted_ts      TIMESTAMPTZ NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_preview_repair_due
    ON chat.preview_repair_obligations(next_attempt_at);
