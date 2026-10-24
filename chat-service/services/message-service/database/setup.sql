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
    role TEXT NOT NULL DEFAULT 'member' CHECK (role IN ('owner', 'admin', 'member')),
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
-- Account control: a hidden user (deactivated / deletion scheduled) is
-- reported offline and their typing indicators are dropped. Never erased here.
ALTER TABLE chat.user_profiles ADD COLUMN IF NOT EXISTS hidden BOOLEAN NOT NULL DEFAULT FALSE;

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

-- Quoted replies (2026-09-02): the durable intent carries the sender's quote
-- snapshot so repair replays it into Scylla exactly as the live path would.
ALTER TABLE chat.message_delivery_intents ADD COLUMN IF NOT EXISTS reply_to_id UUID;
ALTER TABLE chat.message_delivery_intents ADD COLUMN IF NOT EXISTS reply_to_preview TEXT NOT NULL DEFAULT '';
ALTER TABLE chat.message_delivery_intents ADD COLUMN IF NOT EXISTS reply_to_sender_id UUID;

-- ===== 006: chat-app pass (groups hardening, 2026-09-05) =====
-- The CREATE TABLE above now admits 'owner' directly; the 005 DO block keeps
-- older databases (created with the two-role CHECK) converging. Group
-- description (<= 300 runes, enforced in the service) and shareable invite
-- links. See migrations/006_group_roles_invite_links_description.sql.
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS chat.group_invite_links (
    code            TEXT PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    created_by      UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ,
    max_uses        INT,
    uses            INT NOT NULL DEFAULT 0,
    revoked_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_invite_links_live
    ON chat.group_invite_links(conversation_id) WHERE revoked_at IS NULL;

-- ===== 007: dating first-message notification (Dating lane D4, 2026-09-15) =====
-- One durable obligation per dating conversation to tell dating-service the
-- conversation got its first message, so the match stops expiring. See
-- migrations/007_dating_first_message_notifications.sql.
CREATE TABLE IF NOT EXISTS chat.dating_first_message_notifications (
    conversation_id UUID PRIMARY KEY REFERENCES chat.conversations(id) ON DELETE CASCADE,
    match_id        UUID NOT NULL,
    actor_id        UUID NOT NULL,
    message_id      UUID NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_status     INT,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at    TIMESTAMPTZ,
    terminal_at     TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_dating_first_message_due
    ON chat.dating_first_message_notifications(next_attempt_at)
    WHERE delivered_at IS NULL AND terminal_at IS NULL;

-- ===== 008: dating first move (Dating mechanic M5, 2026-10-02) =====
-- Who may send the first message in a dating-match conversation. NULL or
-- empty: anyone. See migrations/008_dating_first_move.sql.
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS dating_first_movers UUID[];

-- ===== 009: dating match extras (Dating mechanic M9, 2026-10-02) =====
-- Read receipts and calls in dating conversations. Both flags default off.
-- See migrations/009_dating_match_extras.sql.
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS dating_receipts_gated BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE chat.conversations ADD COLUMN IF NOT EXISTS dating_call_after_exchange BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS first_sent_at TIMESTAMPTZ;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS dating_receipts_until TIMESTAMPTZ;

-- ===== 010: post cards (TikTok parity T-25 "Send to friends", 2026-10-14) =====
-- message_type is unconstrained TEXT, so a "post" message needs no CHECK
-- change. The durable intent carries the shared post id so repair replays it
-- into Scylla. Only the id is stored; clients hydrate it as the viewer. See
-- migrations/010_post_message_cards.sql.
ALTER TABLE chat.message_delivery_intents ADD COLUMN IF NOT EXISTS post_id UUID;

-- ===== 011: chat parity (CONTRACT §1-3, §5-7, 2026-10-18) =====
-- Mirrors migrations/011_chat_parity.sql statement for statement.
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

-- Chat parity fix pass 2 (migration 012): mark a conversation unread for
-- yourself. Cleared by POST /read (with the unread counter) or by
-- PUT /unread {"unread": false}.
ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS marked_unread BOOLEAN NOT NULL DEFAULT FALSE;

-- ===== 013: disappearing messages (20 Oct 2026) =====
-- See migrations/013_disappearing_messages.sql for the full rationale.
ALTER TABLE chat.conversations
    ADD COLUMN IF NOT EXISTS disappear_after_ms   BIGINT,
    ADD COLUMN IF NOT EXISTS disappear_updated_by UUID,
    ADD COLUMN IF NOT EXISTS disappear_updated_at TIMESTAMPTZ;
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'conversations_disappear_after_ms_allowed'
    ) THEN
        ALTER TABLE chat.conversations
            ADD CONSTRAINT conversations_disappear_after_ms_allowed
            CHECK (disappear_after_ms IS NULL OR disappear_after_ms IN (86400000, 604800000, 7776000000));
    END IF;
END $$;

-- 2. One row per message sent while the timer was on. expires_at is fixed
--    at send time (created_at + the timer then in force): changing or turning
--    off the timer later never moves it. The expiry worker claims due rows
--    with FOR UPDATE SKIP LOCKED (next_attempt_at doubles as the lease), so
--    it never scans Scylla. deleted_at records that the Postgres side of the
--    delete-for-everyone already ran, so a retry never releases an unread
--    count twice. The row is removed once the message is fully gone.
CREATE TABLE IF NOT EXISTS chat.message_expiries (
    message_id      UUID PRIMARY KEY,
    conversation_id UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    bucket          TEXT NOT NULL,
    message_ts      TIMESTAMPTZ NOT NULL,
    sender_id       UUID NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempt_count   INT NOT NULL DEFAULT 0,
    deleted_at      TIMESTAMPTZ,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_message_expiries_due
    ON chat.message_expiries(next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_message_expiries_conversation
    ON chat.message_expiries(conversation_id);

-- ===== 014: DM consent model (20 Oct 2026) =====
-- See migrations/014_dm_consent_requests.sql. The 30-day request expiry
-- sweep claims due pending requests by expires_at.
CREATE INDEX IF NOT EXISTS idx_message_requests_pending_expiry
    ON chat.message_requests(expires_at) WHERE status = 'pending';

-- ===== 015: how a member joined a group (22 Oct 2026) =====
-- See migrations/015_group_join_method.sql. AddMemberCapped records the way
-- in (invite_link | added | invitation) and who added them, so a newcomer's
-- client can show "X added you" above the history that starts at the join.
-- Existing rows stay NULL and get no note.
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS join_method TEXT;
ALTER TABLE chat.conversation_members ADD COLUMN IF NOT EXISTS added_by UUID;

-- ===== 016: field encryption at rest (22 Oct 2026) =====
-- See migrations/016_field_encryption.sql for the full rationale.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'chat' AND table_name = 'message_requests'
          AND column_name = 'preview' AND data_type <> 'text'
    ) THEN
        ALTER TABLE chat.message_requests ALTER COLUMN preview TYPE TEXT;
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS chat.field_key_ring (
    version            INT PRIMARY KEY CHECK (version > 0),
    wrapped_key        BYTEA NOT NULL,
    kms_key_id         TEXT NOT NULL,
    encryption_context JSONB NOT NULL,
    active             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retired_at         TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_field_key_ring_one_active
    ON chat.field_key_ring ((active)) WHERE active;

CREATE TABLE IF NOT EXISTS chat.field_backfill_progress (
    target       TEXT PRIMARY KEY,
    resume_after TEXT NOT NULL DEFAULT '',
    done         BOOLEAN NOT NULL DEFAULT FALSE,
    scanned      BIGINT NOT NULL DEFAULT 0,
    sealed       BIGINT NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_outbox_published_at
    ON chat.outbox_events(published_at) WHERE published_at IS NOT NULL;

-- Delivered ticks (migration 017, founder 22 Oct 2026): each member's
-- delivered cursor, advanced by POST /conversations/:id/delivered. A sender's
-- message is "delivered" when its ts <= every other active member's cursor.
CREATE TABLE IF NOT EXISTS chat.delivered_cursors (
    conversation_id           UUID NOT NULL REFERENCES chat.conversations(id) ON DELETE CASCADE,
    user_id                   UUID NOT NULL,
    last_delivered_message_id UUID,
    last_delivered_at         TIMESTAMPTZ NOT NULL,
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (conversation_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_delivered_cursors_user ON chat.delivered_cursors(user_id);

-- ===== 018: hidden chats (24 Oct 2026) =====
-- See migrations/018_hidden_chats.sql for the full rationale.
ALTER TABLE chat.conversation_members
    ADD COLUMN IF NOT EXISTS locked    BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS locked_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_conversation_members_locked
    ON chat.conversation_members(user_id) WHERE locked;

-- The account's two secrets, set once per account and shared by every device.
-- Only Argon2id PHC strings are ever stored; the CHECK makes a raw secret
-- impossible to write even by a buggy code path.
CREATE TABLE IF NOT EXISTS chat.hidden_chat_settings (
    user_id            UUID PRIMARY KEY,
    secret_key_hash    TEXT NOT NULL,
    chat_password_hash TEXT NOT NULL,
    -- strict: a reset after sign-in wipes (clear-for-me) every locked
    -- conversation instead of keeping it behind the new secrets.
    strict             BOOLEAN NOT NULL DEFAULT FALSE,
    -- shortcut: the web key combination that opens the key prompt (display
    -- preference only; NULL = the client default Ctrl+Shift+H).
    shortcut           TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT hidden_chat_settings_hashes_only CHECK (
        secret_key_hash LIKE '$argon2id$%' AND chat_password_hash LIKE '$argon2id$%'
    ),
    CONSTRAINT hidden_chat_settings_shortcut_len CHECK (shortcut IS NULL OR length(shortcut) <= 64)
);

-- Wrong-attempt counters per account and secret. Kept in Postgres (not only
-- Redis) so a restart or a Redis flush cannot reset a lockout. 5 failures
-- lock the secret; each lockout raises lockout_level (15 min, 1 h, 24 h).
CREATE TABLE IF NOT EXISTS chat.hidden_chat_attempts (
    user_id         UUID NOT NULL,
    secret_kind     TEXT NOT NULL CHECK (secret_kind IN ('secret_key', 'chat_password')),
    failures        INT NOT NULL DEFAULT 0,
    lockout_level   INT NOT NULL DEFAULT 0,
    locked_until    TIMESTAMPTZ,
    last_failure_at TIMESTAMPTZ,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, secret_kind)
);

-- Reveal / open session tokens. The client holds 32 random bytes; only their
-- SHA-256 is stored, bound to the user and the device (the access token's
-- session id). expires_at slides forward 15 minutes on every valid use.
CREATE TABLE IF NOT EXISTS chat.hidden_chat_tokens (
    token_hash   BYTEA PRIMARY KEY,
    user_id      UUID NOT NULL,
    device_key   TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('reveal', 'open')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_hidden_chat_tokens_user ON chat.hidden_chat_tokens(user_id);
